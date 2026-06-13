//go:build lens_nats

// Package natstransport implements the Transport interface using NATS.
// All instances subscribe to a shared broadcast subject so the NATS broker handles
// fan-out, and each instance also subscribes to a direct-addressed get subject
// for peer-to-peer key fetching.
//
// Subjects:
//
//	lens.invalidate.<svc>          — broadcast to all instances of svc
//	lens.get.<svc>.<instance>      — direct get RPC to a specific instance
package natstransport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/vedanshu/lens/internal/transport"
)

func init() {
	transport.Register("nats", func(host transport.TransportHost, cfg map[string]any) (transport.Transport, error) {
		url, _ := cfg["natsUrl"].(string)
		if url == "" {
			url = nats.DefaultURL
		}
		return newNATSTransport(host, url)
	})
}

type natsTransport struct {
	host transport.TransportHost
	nc   *nats.Conn
}

func newNATSTransport(host transport.TransportHost, url string) (*natsTransport, error) {
	nc, err := nats.Connect(url,
		nats.Name("lens-"+host.SelfInstance()),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	t := &natsTransport{host: host, nc: nc}
	svc := host.SelfService()
	inst := host.SelfInstance()
	bcastSubj := "lens.invalidate." + svc
	getSubj := "lens.get." + svc + "." + inst

	_, err = nc.Subscribe(bcastSubj, t.handleInvalidate)
	if err == nil {
		_, err = nc.Subscribe(getSubj, t.handleGet)
		if err != nil {
			err = fmt.Errorf("nats subscribe get: %w", err)
		}
	} else {
		err = fmt.Errorf("nats subscribe broadcast: %w", err)
	}

	if err != nil {
		nc.Close()
		return nil, err
	}
	slog.Info("nats transport ready", "broadcast", bcastSubj, "get", getSubj)
	return t, nil
}

// Broadcast publishes payload to all peers of svc and collects up to one reply
// per known peer within 2 seconds. Returns one Ack per responding peer.
func (t *natsTransport) Broadcast(ctx context.Context, svc string, payload []byte) ([]transport.Ack, error) {
	peers := t.host.PeersForService(svc)
	expected := len(peers)

	msg := nats.NewMsg("lens.invalidate." + svc)
	msg.Data = payload
	msg.Reply = nats.NewInbox()

	sub, err := t.nc.SubscribeSync(msg.Reply)
	if err != nil {
		return nil, fmt.Errorf("nats subscribe reply: %w", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	if err := t.nc.PublishMsg(msg); err != nil {
		return nil, fmt.Errorf("nats publish: %w", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var acks []transport.Ack
	for i := 0; i < expected; i++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		reply, err := sub.NextMsg(remaining)
		if err != nil {
			break
		}
		var ack transport.Ack
		if json.Unmarshal(reply.Data, &ack) == nil {
			acks = append(acks, ack)
		}
	}

	t.host.WriteInvalidationLog(ctx, svc, payload)
	return acks, nil
}

// Get sends a request to the direct get subject of instance within svc.
// The timeout is derived from ctx deadline when present; otherwise 3 seconds.
func (t *natsTransport) Get(ctx context.Context, svc, instance, key string) ([]byte, error) {
	subj := "lens.get." + svc + "." + instance
	payload, _ := json.Marshal(map[string]string{"key": key})

	timeout := 3 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
	}

	reply, err := t.nc.Request(subj, payload, timeout)
	if err != nil {
		return nil, fmt.Errorf("nats get: %w", err)
	}
	return reply.Data, nil
}

// Close drains the NATS connection, flushing all pending messages before closing.
func (t *natsTransport) Close() error {
	return t.nc.Drain()
}

func (t *natsTransport) handleInvalidate(msg *nats.Msg) {
	ctx := context.Background()
	t.host.ApplyInvalidation(ctx, msg.Data, "")

	ack, _ := json.Marshal(transport.Ack{Instance: t.host.SelfInstance(), Success: true})
	if msg.Reply != "" {
		t.nc.Publish(msg.Reply, ack) //nolint:errcheck
	}
}

func (t *natsTransport) handleGet(msg *nats.Msg) {
	ctx := context.Background()
	body, err := t.host.GetFromTarget(ctx, msg.Data)
	if err != nil {
		slog.Warn("nats get: target unreachable", "err", err)
		t.nc.Publish(msg.Reply, []byte(`{"error":"target unreachable"}`)) //nolint:errcheck
		return
	}
	t.nc.Publish(msg.Reply, body) //nolint:errcheck
}
