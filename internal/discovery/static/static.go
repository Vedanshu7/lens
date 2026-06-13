//go:build lens_static

// Package staticdiscovery implements a discovery provider backed by a fixed seed list.
// Peers are known at startup and never change; this provider emits Join events for
// each seed when Watch is called, then leaves the channel idle. Use it for
// single-node development or fixed-topology clusters where gossip is not available.
package staticdiscovery

import (
	"context"
	"sync"

	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/persistence"
)

func init() {
	discovery.Register("static", func(_ persistence.Backend, cfg map[string]any) (discovery.Resolver, error) {
		var seeds []discovery.ServiceInstance
		if raw, ok := cfg["seeds"].([]any); ok {
			for _, s := range raw {
				if si, ok := s.(map[string]any); ok {
					inst := discovery.ServiceInstance{}
					inst.Service, _ = si["service"].(string)
					inst.Instance, _ = si["instance"].(string)
					inst.GRPCAddr, _ = si["grpcAddr"].(string)
					inst.AgentURL, _ = si["agentURL"].(string)
					if inst.Instance != "" {
						seeds = append(seeds, inst)
					}
				}
			}
		}
		return &staticResolver{seeds: seeds, watchCh: make(chan discovery.Event, 1)}, nil
	})
}

type staticResolver struct {
	mu      sync.RWMutex
	self    discovery.ServiceInstance
	seeds   []discovery.ServiceInstance
	watchCh chan discovery.Event
}

// Register stores the self descriptor so Peers can exclude this instance.
func (r *staticResolver) Register(_ context.Context, self discovery.ServiceInstance) error {
	r.mu.Lock()
	r.self = self
	r.mu.Unlock()
	return nil
}

// Deregister is a no-op for static discovery.
func (r *staticResolver) Deregister(_ context.Context, _ discovery.ServiceInstance) error {
	return nil
}

// Peers returns seed instances for service, excluding self.
func (r *staticResolver) Peers(_ context.Context, service string) ([]discovery.ServiceInstance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []discovery.ServiceInstance
	for _, s := range r.seeds {
		if s.Service == service && s.Instance != r.self.Instance {
			out = append(out, s)
		}
	}
	return out, nil
}

// Watch publishes initial Join events for all seeds, then leaves the channel
// idle because the static topology never changes.
func (r *staticResolver) Watch(_ context.Context) (<-chan discovery.Event, error) {
	go func() {
		r.mu.RLock()
		seeds := make([]discovery.ServiceInstance, len(r.seeds))
		copy(seeds, r.seeds)
		self := r.self
		r.mu.RUnlock()

		for _, s := range seeds {
			if s.Instance != self.Instance {
				r.watchCh <- discovery.Event{Type: discovery.EventJoin, Instance: s}
			}
		}
	}()
	return r.watchCh, nil
}

// Close closes the event channel so the watchPeers goroutine exits.
func (r *staticResolver) Close() error {
	defer func() { recover() }() //nolint:errcheck
	close(r.watchCh)
	return nil
}

// Compile-time check that staticResolver satisfies discovery.Resolver.
var _ discovery.Resolver = (*staticResolver)(nil)
