//go:build lens_kafka

// Package kafkatransport implements the Transport interface using Apache Kafka.
// Broadcast messages are published to a per-service topic consumed by all
// replicas. Point-to-point Get uses a per-instance request topic and a
// per-instance reply topic with correlation IDs to match responses.
package kafkatransport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	kafka "github.com/segmentio/kafka-go"
	"github.com/vedanshu/lens/internal/transport"
)

func init() {
	transport.Register("kafka", func(host transport.TransportHost, cfg map[string]any) (transport.Transport, error) {
		brokers, _ := cfg["brokers"].(string)
		if brokers == "" {
			brokers = "localhost:9092"
		}
		return newKafkaTransport(host, strings.Split(brokers, ","))
	})
}

type kafkaMessage struct {
	Action      string `json:"action"`
	Payload     []byte `json:"payload,omitempty"`
	Origin      string `json:"origin"`
	Key         string `json:"key,omitempty"`
	ReplyTopic  string `json:"replyTopic,omitempty"`
	Correlation string `json:"correlation,omitempty"`
}

type kafkaTransport struct {
	host    transport.TransportHost
	brokers []string
	writer  *kafka.Writer

	mu          sync.Mutex
	replyWaiters map[string]chan []byte

	cancel context.CancelFunc
}

func newKafkaTransport(host transport.TransportHost, brokers []string) (*kafkaTransport, error) {
	writer := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Balancer:     &kafka.LeastBytes{},
		WriteTimeout: 2 * time.Second,
		RequiredAcks: kafka.RequireOne,
	}

	t := &kafkaTransport{
		host:         host,
		brokers:      brokers,
		writer:       writer,
		replyWaiters: make(map[string]chan []byte),
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel

	svc := host.SelfService()
	inst := host.SelfInstance()

	go t.consumeBroadcast(ctx, svc, inst)
	go t.consumeGet(ctx, inst)
	go t.consumeGetReply(ctx, inst)

	slog.Info("kafka transport ready",
		"broadcast", broadcastTopic(svc),
		"get", getTopic(inst),
		"reply", getReplyTopic(inst),
	)
	return t, nil
}

// Broadcast publishes payload to the service broadcast topic. All replicas
// consume the topic independently. Returns nil acks because Kafka fan-out is
// handled by consumer groups, not by the publisher collecting replies.
func (t *kafkaTransport) Broadcast(ctx context.Context, svc string, payload []byte) ([]transport.Ack, error) {
	msg := kafkaMessage{
		Action:  "invalidate",
		Payload: payload,
		Origin:  t.host.SelfInstance(),
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("kafka broadcast marshal: %w", err)
	}

	if err := t.writer.WriteMessages(ctx, kafka.Message{
		Topic: broadcastTopic(svc),
		Value: data,
	}); err != nil {
		return nil, fmt.Errorf("kafka broadcast write: %w", err)
	}

	t.host.WriteInvalidationLog(ctx, svc, payload)
	return nil, nil
}

// Get publishes a request to the target instance's get topic and waits for a
// response on this instance's reply topic, matched by correlation ID.
func (t *kafkaTransport) Get(ctx context.Context, svc, instance, key string) ([]byte, error) {
	corr := fmt.Sprintf("%s-%d", t.host.SelfInstance(), time.Now().UnixNano())
	replyCh := make(chan []byte, 1)

	t.mu.Lock()
	t.replyWaiters[corr] = replyCh
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.replyWaiters, corr)
		t.mu.Unlock()
	}()

	msg := kafkaMessage{
		Action:      "get",
		Key:         key,
		Origin:      t.host.SelfInstance(),
		ReplyTopic:  getReplyTopic(t.host.SelfInstance()),
		Correlation: corr,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("kafka get marshal: %w", err)
	}

	if err := t.writer.WriteMessages(ctx, kafka.Message{
		Topic: getTopic(instance),
		Value: data,
	}); err != nil {
		return nil, fmt.Errorf("kafka get write: %w", err)
	}

	timeout := 3 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
	}

	select {
	case body := <-replyCh:
		return body, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("kafka get: timeout waiting for reply (corr=%s)", corr)
	}
}

// Close cancels reader goroutines and flushes the writer.
func (t *kafkaTransport) Close() error {
	t.cancel()
	return t.writer.Close()
}

// consumeBroadcast reads from the service broadcast topic and calls ApplyInvalidation.
// Each instance uses its own consumer group so it receives every message independently.
func (t *kafkaTransport) consumeBroadcast(ctx context.Context, svc, inst string) {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: t.brokers,
		Topic:   broadcastTopic(svc),
		GroupID: "lens-" + inst,
	})
	defer r.Close() //nolint:errcheck

	for ctx.Err() == nil {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("kafka broadcast read error", "err", err)
			continue
		}
		var msg kafkaMessage
		if err := json.Unmarshal(m.Value, &msg); err != nil {
			continue
		}
		if msg.Origin == t.host.SelfInstance() {
			continue
		}
		t.host.ApplyInvalidation(ctx, msg.Payload, msg.Origin)
	}
}

// consumeGet reads from this instance's get topic and responds via the caller's reply topic.
func (t *kafkaTransport) consumeGet(ctx context.Context, inst string) {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: t.brokers,
		Topic:   getTopic(inst),
		GroupID: "lens-get-" + inst,
	})
	defer r.Close() //nolint:errcheck

	for ctx.Err() == nil {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("kafka get read error", "err", err)
			continue
		}
		var msg kafkaMessage
		if err := json.Unmarshal(m.Value, &msg); err != nil {
			continue
		}
		if msg.ReplyTopic == "" {
			continue
		}

		reqPayload, _ := json.Marshal(map[string]string{"key": msg.Key})
		body, err := t.host.GetFromTarget(ctx, reqPayload)
		if err != nil {
			slog.Warn("kafka get: target unreachable", "err", err)
			body = []byte(`{"error":"target unreachable"}`)
		}

		reply := kafkaMessage{Correlation: msg.Correlation, Payload: body}
		replyData, _ := json.Marshal(reply)
		writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		t.writer.WriteMessages(writeCtx, kafka.Message{ //nolint:errcheck
			Topic: msg.ReplyTopic,
			Value: replyData,
		})
		cancel()
	}
}

// consumeGetReply reads from this instance's reply topic and routes each response
// to the waiting Get call via its correlation ID channel.
func (t *kafkaTransport) consumeGetReply(ctx context.Context, inst string) {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: t.brokers,
		Topic:   getReplyTopic(inst),
		GroupID: "lens-get-resp-" + inst,
	})
	defer r.Close() //nolint:errcheck

	for ctx.Err() == nil {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("kafka reply read error", "err", err)
			continue
		}
		var msg kafkaMessage
		if err := json.Unmarshal(m.Value, &msg); err != nil {
			continue
		}
		t.mu.Lock()
		ch, ok := t.replyWaiters[msg.Correlation]
		t.mu.Unlock()
		if ok {
			select {
			case ch <- msg.Payload:
			default:
			}
		}
	}
}

func broadcastTopic(svc string) string  { return "lens." + svc + ".broadcast" }
func getTopic(inst string) string       { return "lens.get." + inst }
func getReplyTopic(inst string) string  { return "lens.get.resp." + inst }

// Compile-time check that kafkaTransport satisfies transport.Transport.
var _ transport.Transport = (*kafkaTransport)(nil)
