//go:build lens_natsdiscovery

// Package natsdiscovery implements peer discovery using NATS core pub/sub.
// Each instance publishes periodic heartbeats on lens.presence.{service}.
// A background goroutine tracks last-seen timestamps and emits leave events
// for peers that miss three consecutive heartbeat windows.
//
// No external infrastructure beyond the NATS broker is required.
package natsdiscovery

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/persistence"
)

const (
	heartbeatInterval = 5 * time.Second
	deadTimeout       = 16 * time.Second // miss 3 heartbeats
)

func init() {
	discovery.Register("nats", func(_ persistence.Backend, cfg map[string]any) (discovery.Resolver, error) {
		url, _ := cfg["natsUrl"].(string)
		if url == "" {
			url = nats.DefaultURL
		}
		nc, err := nats.Connect(url,
			nats.RetryOnFailedConnect(true),
			nats.MaxReconnects(-1),
		)
		if err != nil {
			return nil, err
		}
		return &natsResolver{
			nc:       nc,
			peers:    map[string]peerEntry{},
			eventCh:  make(chan discovery.Event, 64),
		}, nil
	})
}

type presence struct {
	Service  string `json:"service"`
	Instance string `json:"instance"`
	AgentURL string `json:"agentUrl"`
	Left     bool   `json:"left,omitempty"`
}

type peerEntry struct {
	si       discovery.ServiceInstance
	lastSeen time.Time
}

type natsResolver struct {
	mu      sync.RWMutex
	nc      *nats.Conn
	self    discovery.ServiceInstance
	peers   map[string]peerEntry
	eventCh chan discovery.Event
}

func (r *natsResolver) Register(ctx context.Context, self discovery.ServiceInstance) error {
	r.mu.Lock()
	r.self = self
	r.mu.Unlock()

	// Subscribe to presence announcements from peers.
	subj := "lens.presence." + self.Service
	if _, err := r.nc.Subscribe(subj, r.handlePresence); err != nil {
		return err
	}

	// Publish own heartbeats.
	go r.heartbeatLoop(ctx, self)
	// Reap dead peers.
	go r.reapLoop(ctx)

	slog.Info("nats discovery ready", "service", self.Service, "instance", self.Instance)
	return nil
}

func (r *natsResolver) heartbeatLoop(ctx context.Context, self discovery.ServiceInstance) {
	data, _ := json.Marshal(presence{
		Service:  self.Service,
		Instance: self.Instance,
		AgentURL: self.AgentURL,
	})
	subj := "lens.presence." + self.Service
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	r.nc.Publish(subj, data) //nolint:errcheck
	for {
		select {
		case <-ticker.C:
			r.nc.Publish(subj, data) //nolint:errcheck
		case <-ctx.Done():
			return
		}
	}
}

func (r *natsResolver) reapLoop(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.reapDead()
		case <-ctx.Done():
			return
		}
	}
}

func (r *natsResolver) reapDead() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-deadTimeout)
	for inst, entry := range r.peers {
		if entry.lastSeen.Before(cutoff) {
			slog.Info("nats discovery: peer timed out", "instance", inst)
			select {
			case r.eventCh <- discovery.Event{Type: discovery.EventLeave, Instance: entry.si}:
			default:
			}
			delete(r.peers, inst)
		}
	}
}

func (r *natsResolver) handlePresence(msg *nats.Msg) {
	var p presence
	if err := json.Unmarshal(msg.Data, &p); err != nil {
		return
	}
	r.mu.RLock()
	self := r.self
	r.mu.RUnlock()
	if p.Instance == self.Instance {
		return // own heartbeat
	}

	si := discovery.ServiceInstance{
		Service:  p.Service,
		Instance: p.Instance,
		AgentURL: p.AgentURL,
	}

	r.mu.Lock()
	_, known := r.peers[p.Instance]
	r.peers[p.Instance] = peerEntry{si: si, lastSeen: time.Now()}
	r.mu.Unlock()

	if p.Left {
		slog.Info("nats discovery: peer left", "instance", p.Instance)
		r.mu.Lock()
		delete(r.peers, p.Instance)
		r.mu.Unlock()
		select {
		case r.eventCh <- discovery.Event{Type: discovery.EventLeave, Instance: si}:
		default:
		}
		return
	}

	ev := discovery.EventUpdate
	if !known {
		ev = discovery.EventJoin
		slog.Info("nats discovery: peer joined", "instance", p.Instance, "service", p.Service)
	}
	select {
	case r.eventCh <- discovery.Event{Type: ev, Instance: si}:
	default:
	}
}

func (r *natsResolver) Deregister(ctx context.Context, self discovery.ServiceInstance) error {
	data, _ := json.Marshal(presence{
		Service:  self.Service,
		Instance: self.Instance,
		AgentURL: self.AgentURL,
		Left:     true,
	})
	r.nc.Publish("lens.presence."+self.Service, data) //nolint:errcheck
	return nil
}

func (r *natsResolver) Peers(_ context.Context, service string) ([]discovery.ServiceInstance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []discovery.ServiceInstance
	for _, entry := range r.peers {
		if entry.si.Service == service {
			out = append(out, entry.si)
		}
	}
	return out, nil
}

func (r *natsResolver) Watch(_ context.Context) (<-chan discovery.Event, error) {
	return r.eventCh, nil
}

func (r *natsResolver) Close() error {
	close(r.eventCh)
	return r.nc.Drain()
}
