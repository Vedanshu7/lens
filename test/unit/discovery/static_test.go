package discovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/Vedanshu7/lens/internal/discovery"
	_ "github.com/Vedanshu7/lens/internal/discovery/static"
	"github.com/Vedanshu7/lens/internal/persistence"
	_ "github.com/Vedanshu7/lens/internal/persistence/memory"
)

func newStaticResolver(t *testing.T, seeds []map[string]any) discovery.Resolver {
	t.Helper()
	store, err := persistence.New("memory", nil)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	out := make([]any, len(seeds))
	for i, s := range seeds {
		out[i] = s
	}
	r, err := discovery.New(store, "static", map[string]any{"seeds": out})
	if err != nil {
		t.Fatalf("create static resolver: %v", err)
	}
	t.Cleanup(func() { r.Close() }) //nolint:errcheck
	return r
}

func TestStatic_Peers_ReturnsByService(t *testing.T) {
	ctx := context.Background()
	r := newStaticResolver(t, []map[string]any{
		{"service": "svc-a", "instance": "inst-1", "agentURL": "http://host1:8080"},
		{"service": "svc-a", "instance": "inst-2", "agentURL": "http://host2:8080"},
		{"service": "svc-b", "instance": "inst-3", "agentURL": "http://host3:8080"},
	})

	peers, err := r.Peers(ctx, "svc-a")
	if err != nil {
		t.Fatalf("Peers: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("want 2 peers for svc-a, got %d: %v", len(peers), peers)
	}
	for _, p := range peers {
		if p.Service != "svc-a" {
			t.Errorf("peer service: want svc-a, got %q", p.Service)
		}
	}
}

func TestStatic_Peers_ExcludesSelf(t *testing.T) {
	ctx := context.Background()
	r := newStaticResolver(t, []map[string]any{
		{"service": "svc", "instance": "self", "agentURL": "http://self:8080"},
		{"service": "svc", "instance": "peer", "agentURL": "http://peer:8080"},
	})

	r.Register(ctx, discovery.ServiceInstance{Service: "svc", Instance: "self"}) //nolint:errcheck

	peers, err := r.Peers(ctx, "svc")
	if err != nil {
		t.Fatalf("Peers: %v", err)
	}
	if len(peers) != 1 || peers[0].Instance != "peer" {
		t.Errorf("want 1 peer (peer), got %v", peers)
	}
}

func TestStatic_Peers_UnknownService_ReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	r := newStaticResolver(t, []map[string]any{
		{"service": "svc-a", "instance": "inst-1"},
	})

	peers, err := r.Peers(ctx, "svc-unknown")
	if err != nil {
		t.Fatalf("Peers: %v", err)
	}
	if len(peers) != 0 {
		t.Errorf("unknown service: want 0 peers, got %d", len(peers))
	}
}

func TestStatic_Watch_EmitsJoinEventsForSeeds(t *testing.T) {
	ctx := context.Background()
	r := newStaticResolver(t, []map[string]any{
		{"service": "svc", "instance": "peer-1"},
		{"service": "svc", "instance": "peer-2"},
	})

	ch, err := r.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	received := make([]discovery.Event, 0, 2)
	timeout := time.After(500 * time.Millisecond)
	for len(received) < 2 {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("Watch channel closed early")
			}
			received = append(received, ev)
		case <-timeout:
			t.Fatalf("timeout waiting for events; got %d of 2", len(received))
		}
	}
	for _, ev := range received {
		if ev.Type != discovery.EventJoin {
			t.Errorf("event type: want Join, got %v", ev.Type)
		}
	}
}

func TestStatic_Deregister_IsNoOp(t *testing.T) {
	ctx := context.Background()
	r := newStaticResolver(t, []map[string]any{
		{"service": "svc", "instance": "inst-1"},
	})

	if err := r.Deregister(ctx, discovery.ServiceInstance{Service: "svc", Instance: "inst-1"}); err != nil {
		t.Errorf("Deregister: %v", err)
	}
	peers, _ := r.Peers(ctx, "svc")
	if len(peers) != 1 {
		t.Error("Deregister should be no-op; peers were removed")
	}
}
