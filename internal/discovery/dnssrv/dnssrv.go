//go:build lens_dnssrv

// Package dnssrvdiscovery implements peer discovery by polling DNS SRV records.
// It requires no external infrastructure beyond a DNS server that returns
// SRV records under _lens._tcp.<service>.<domain>. Peers are discovered
// by polling at a configurable interval and comparing against the previous set.
package dnssrvdiscovery

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/persistence"
)

func init() {
	discovery.Register("dnssrv", func(_ persistence.Backend, cfg map[string]any) (discovery.Resolver, error) {
		domain, _ := cfg["domain"].(string)
		if domain == "" {
			return nil, fmt.Errorf("dnssrv: domain is required")
		}
		pollSec := 30
		if v, ok := cfg["pollIntervalSeconds"].(int); ok && v > 0 {
			pollSec = v
		}
		grpcPort, _ := cfg["grpcPort"].(string)
		if grpcPort == "" {
			grpcPort = "8901"
		}
		agentPort, _ := cfg["agentPort"].(string)
		if agentPort == "" {
			agentPort = "8900"
		}
		return &dnsSRVResolver{
			domain:       domain,
			pollInterval: time.Duration(pollSec) * time.Second,
			grpcPort:     grpcPort,
			agentPort:    agentPort,
			eventCh:      make(chan discovery.Event, 64),
			known:        make(map[string]discovery.ServiceInstance),
		}, nil
	})
}

type dnsSRVResolver struct {
	mu           sync.RWMutex
	self         discovery.ServiceInstance
	domain       string
	pollInterval time.Duration
	grpcPort     string
	agentPort    string
	eventCh      chan discovery.Event
	known        map[string]discovery.ServiceInstance
	cancel       context.CancelFunc
}

// Register stores the self descriptor and starts the background poll goroutine.
func (r *dnsSRVResolver) Register(ctx context.Context, self discovery.ServiceInstance) error {
	r.mu.Lock()
	r.self = self
	r.mu.Unlock()

	pollCtx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()

	go r.poll(pollCtx, self.Service)
	return nil
}

// Deregister cancels the poll goroutine. DNS-SRV has no explicit deregistration.
func (r *dnsSRVResolver) Deregister(_ context.Context, _ discovery.ServiceInstance) error {
	r.mu.RLock()
	cancel := r.cancel
	r.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Peers returns the current set of discovered instances for service, excluding self.
func (r *dnsSRVResolver) Peers(_ context.Context, service string) ([]discovery.ServiceInstance, error) {
	r.mu.RLock()
	self := r.self
	known := r.known
	r.mu.RUnlock()

	var out []discovery.ServiceInstance
	for _, si := range known {
		if si.Service == service && si.Instance != self.Instance {
			out = append(out, si)
		}
	}
	return out, nil
}

// Watch returns the channel that emits EventJoin and EventLeave as DNS records change.
func (r *dnsSRVResolver) Watch(_ context.Context) (<-chan discovery.Event, error) {
	return r.eventCh, nil
}

// Close cancels the poll goroutine and closes the event channel.
func (r *dnsSRVResolver) Close() error {
	r.mu.RLock()
	cancel := r.cancel
	r.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	close(r.eventCh)
	return nil
}

// poll queries DNS SRV records at each tick, diffs against the known set,
// and emits EventJoin or EventLeave for each change.
func (r *dnsSRVResolver) poll(ctx context.Context, service string) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	r.resolve(service)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.resolve(service)
		}
	}
}

// resolve performs one DNS SRV lookup and reconciles the result against r.known.
func (r *dnsSRVResolver) resolve(service string) {
	_, addrs, err := net.LookupSRV("lens", "tcp", service+"."+r.domain)
	if err != nil {
		slog.Warn("dnssrv: lookup failed", "service", service, "domain", r.domain, "err", err)
		return
	}

	r.mu.RLock()
	self := r.self
	r.mu.RUnlock()

	current := make(map[string]discovery.ServiceInstance, len(addrs))
	for _, srv := range addrs {
		host := srv.Target
		port := fmt.Sprintf("%d", srv.Port)
		inst := net.JoinHostPort(host, port)
		if inst == self.Instance {
			continue
		}
		current[inst] = discovery.ServiceInstance{
			Service:  service,
			Instance: inst,
			GRPCAddr: net.JoinHostPort(host, r.grpcPort),
			AgentURL: "http://" + net.JoinHostPort(host, r.agentPort),
		}
	}

	r.mu.Lock()
	prev := r.known
	r.known = current
	r.mu.Unlock()

	for key, si := range current {
		if _, existed := prev[key]; !existed {
			slog.Info("dnssrv: peer joined", "instance", si.Instance)
			select {
			case r.eventCh <- discovery.Event{Type: discovery.EventJoin, Instance: si}:
			default:
			}
		}
	}
	for key, si := range prev {
		if _, still := current[key]; !still {
			slog.Info("dnssrv: peer left", "instance", si.Instance)
			select {
			case r.eventCh <- discovery.Event{Type: discovery.EventLeave, Instance: si}:
			default:
			}
		}
	}
}

// Compile-time check that dnsSRVResolver satisfies discovery.Resolver.
var _ discovery.Resolver = (*dnsSRVResolver)(nil)
