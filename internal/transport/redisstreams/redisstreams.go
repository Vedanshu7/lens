// Package redistreamstransport implements the Transport interface using Redis Streams.
// Broadcast messages are XADD'd to a per-service stream and consumed by all
// instances via XREADGROUP. Point-to-point Get uses a per-instance request stream
// and a temporary reply stream identified by a UUID.
package redistreamstransport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/Vedanshu7/lens/internal/transport"
)

func init() {
	transport.Register("redis-streams", func(host transport.TransportHost, cfg map[string]any) (transport.Transport, error) {
		addr, _ := cfg["addr"].(string)
		if addr == "" {
			addr = "localhost:6379"
		}
		db := 0
		if v, ok := cfg["db"].(int); ok {
			db = v
		}
		return newRedisStreamsTransport(host, addr, db)
	})
}

type rsTransport struct {
	host   transport.TransportHost
	client *goredis.Client
	cancel context.CancelFunc
}

func newRedisStreamsTransport(host transport.TransportHost, addr string, db int) (*rsTransport, error) {
	client := goredis.NewClient(&goredis.Options{Addr: addr, DB: db})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis-streams connect: %w", err)
	}

	t := &rsTransport{host: host, client: client}

	svc := host.SelfService()
	inst := host.SelfInstance()
	bcastStream := streamKey(svc)
	getStream := getStreamKey(inst)

	ctx := context.Background()
	if err := ensureGroup(ctx, client, bcastStream, svc); err != nil {
		client.Close() //nolint:errcheck
		return nil, fmt.Errorf("redis-streams: broadcast group: %w", err)
	}
	if err := ensureGroup(ctx, client, getStream, inst); err != nil {
		client.Close() //nolint:errcheck
		return nil, fmt.Errorf("redis-streams: get group: %w", err)
	}

	readerCtx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	go t.readBroadcast(readerCtx, bcastStream, svc, inst)
	go t.readGet(readerCtx, getStream, inst)

	slog.Info("redis-streams transport ready", "broadcast", bcastStream, "get", getStream)
	return t, nil
}

// Broadcast XADDs payload to the service stream and returns immediately.
// Delivery is guaranteed by Redis Streams persistence and consumer group replay.
func (t *rsTransport) Broadcast(ctx context.Context, svc string, payload []byte) ([]transport.Ack, error) {
	args := &goredis.XAddArgs{
		Stream: streamKey(svc),
		Values: map[string]any{
			"action":  "invalidate",
			"payload": payload,
			"origin":  t.host.SelfInstance(),
		},
	}
	if err := t.client.XAdd(ctx, args).Err(); err != nil {
		return nil, fmt.Errorf("redis-streams broadcast: %w", err)
	}
	t.host.WriteInvalidationLog(ctx, svc, payload)
	return nil, nil
}

// Get sends a point-to-point request to instance's get stream and waits for
// the response on a temporary reply stream for up to 3 seconds.
func (t *rsTransport) Get(ctx context.Context, svc, instance, key string) ([]byte, error) {
	replyStream := getReplyKey(t.host.SelfInstance())
	reqPayload, _ := json.Marshal(map[string]string{"key": key, "svc": svc})

	addArgs := &goredis.XAddArgs{
		Stream: getStreamKey(instance),
		Values: map[string]any{
			"payload": reqPayload,
			"replyTo": replyStream,
		},
	}
	if err := t.client.XAdd(ctx, addArgs).Err(); err != nil {
		return nil, fmt.Errorf("redis-streams get send: %w", err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	msgs, err := t.client.XRead(readCtx, &goredis.XReadArgs{
		Streams: []string{replyStream, "0"},
		Count:   1,
		Block:   3 * time.Second,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("redis-streams get reply: %w", err)
	}
	if len(msgs) == 0 || len(msgs[0].Messages) == 0 {
		return nil, fmt.Errorf("redis-streams get: no reply")
	}
	msg := msgs[0].Messages[0]
	t.client.XDel(ctx, replyStream, msg.ID) //nolint:errcheck

	body, _ := msg.Values["body"].(string)
	return []byte(body), nil
}

// Close cancels the reader goroutines and closes the Redis client.
func (t *rsTransport) Close() error {
	t.cancel()
	return t.client.Close()
}

// readBroadcast consumes from the service broadcast stream via XREADGROUP,
// calls ApplyInvalidation for each message, and ACKs after processing.
func (t *rsTransport) readBroadcast(ctx context.Context, stream, group, consumer string) {
	for ctx.Err() == nil {
		msgs, err := t.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{stream, ">"},
			Count:    10,
			Block:    500 * time.Millisecond,
		}).Result()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		for _, stream := range msgs {
			for _, msg := range stream.Messages {
				payload, _ := msg.Values["payload"].(string)
				origin, _ := msg.Values["origin"].(string)
				t.host.ApplyInvalidation(ctx, []byte(payload), origin)
				t.client.XAck(ctx, stream.Stream, group, msg.ID) //nolint:errcheck
			}
		}
	}
}

// readGet consumes from the instance-specific get stream, processes each request
// by calling GetFromTarget, and XADDs the response to the caller's reply stream.
func (t *rsTransport) readGet(ctx context.Context, stream, group string) {
	consumer := t.host.SelfInstance()
	for ctx.Err() == nil {
		msgs, err := t.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{stream, ">"},
			Count:    10,
			Block:    500 * time.Millisecond,
		}).Result()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		for _, s := range msgs {
			for _, msg := range s.Messages {
				t.client.XAck(ctx, stream, group, msg.ID) //nolint:errcheck
				replyTo, _ := msg.Values["replyTo"].(string)
				payload, _ := msg.Values["payload"].(string)
				if replyTo == "" {
					continue
				}
				body, err := t.host.GetFromTarget(ctx, []byte(payload))
				if err != nil {
					slog.Warn("redis-streams get: target unreachable", "err", err)
					body = []byte(`{"error":"target unreachable"}`)
				}
				t.client.XAdd(ctx, &goredis.XAddArgs{ //nolint:errcheck
					Stream: replyTo,
					MaxLen: 1,
					Values: map[string]any{"body": string(body)},
				})
			}
		}
	}
}

// ensureGroup creates stream (MKSTREAM) and a consumer group at id "$" if they
// do not already exist. BUSYGROUP errors are treated as success.
func ensureGroup(ctx context.Context, client *goredis.Client, stream, group string) error {
	err := client.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return err
	}
	return nil
}

func streamKey(svc string) string     { return "lens:stream:" + svc }
func getStreamKey(inst string) string { return "lens:get:" + inst }
func getReplyKey(inst string) string  { return "lens:get:resp:" + inst }

// Compile-time check that rsTransport satisfies transport.Transport.
var _ transport.Transport = (*rsTransport)(nil)
