// Package memberlistdiscovery implements peer discovery using the Hashicorp memberlist
// library for gossip-based cluster membership. It does not require any external
// infrastructure — peers discover each other by joining a bootstrap seed list
// read from the persistence backend on startup.
package memberlistdiscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/persistence"
	"github.com/Vedanshu7/lens/internal/store"
)

func init() {
	discovery.Register("memberlist", func(backend persistence.Backend, cfg map[string]any) (discovery.Resolver, error) {
		gossipPort := 7946
		if v, ok := cfg["bindPort"].(int); ok {
			gossipPort = v
		}
		return &mlResolver{
			backend:    backend,
			gossipPort: gossipPort,
			eventCh:    make(chan discovery.Event, 64),
		}, nil
	})
}

// nodeMeta is serialised into the memberlist node metadata field and broadcast
// to all peers via gossip so they can reconstruct a full ServiceInstance.
type nodeMeta struct {
	Service  string `json:"service"`
	Instance string `json:"instance"`
	GRPCPort string `json:"grpcPort"`
	AgentURL string `json:"agentURL"`
}

type mlResolver struct {
	mu         sync.RWMutex
	backend    persistence.Backend
	gossipPort int
	eventCh    chan discovery.Event
	list       *memberlist.Memberlist
	self       discovery.ServiceInstance
}

// Register announces self to the cluster. It writes self to the persistence
// backend so future restarts can bootstrap, reads existing seeds, creates the
// memberlist, and joins with retry logic to handle simultaneous restarts.
func (r *mlResolver) Register(ctx context.Context, self discovery.ServiceInstance) error {
	r.mu.Lock()
	r.self = self
	r.mu.Unlock()

	if err := r.backend.SAdd(ctx, store.ServiceSetKey(self.Service), self.Instance); err != nil {
		slog.Warn("memberlist: failed to add to service set", "err", err)
	}
	if err := r.backend.Set(ctx, store.NodeKey(self.Service, self.Instance), self.AgentURL, 30*time.Minute); err != nil {
		slog.Warn("memberlist: failed to write node key", "err", err)
	}

	seeds, err := r.bootstrapSeeds(ctx, self.Service, self.Instance)
	if err != nil {
		slog.Warn("memberlist: bootstrap partial", "err", err)
	}

	cfg := memberlist.DefaultLANConfig()
	cfg.Name = self.Instance
	cfg.BindPort = r.gossipPort
	cfg.AdvertisePort = r.gossipPort
	cfg.Delegate = &delegate{r: r}
	cfg.Events = &eventDelegate{r: r}
	cfg.Logger = nil
	cfg.LogOutput = noopWriter{}

	list, err := memberlist.Create(cfg)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.list = list
	r.mu.Unlock()

	if len(seeds) > 0 {
		if err := retryJoin(list, seeds, 30*time.Second); err != nil {
			slog.Warn("memberlist: join failed after retries", "err", err)
		}
	}
	return nil
}

// Deregister removes self from the persistence set and leaves the gossip cluster.
func (r *mlResolver) Deregister(ctx context.Context, self discovery.ServiceInstance) error {
	if err := r.backend.SRem(ctx, store.ServiceSetKey(self.Service), self.Instance); err != nil {
		slog.Warn("memberlist: failed to remove from service set", "err", err)
	}
	r.mu.RLock()
	list := r.list
	r.mu.RUnlock()
	var err error
	if list != nil {
		err = list.Leave(5 * time.Second)
	}
	return err
}

// Peers returns all live members of service, excluding self.
func (r *mlResolver) Peers(_ context.Context, service string) ([]discovery.ServiceInstance, error) {
	r.mu.RLock()
	list := r.list
	self := r.self
	r.mu.RUnlock()
	var out []discovery.ServiceInstance
	if list != nil {
		for _, n := range list.Members() {
			if n.Name != self.Instance {
				if si, ok := parseNodeMeta(n); ok && si.Service == service {
					out = append(out, si)
				}
			}
		}
	}
	return out, nil
}

// Watch returns the event channel that receives Join, Leave, and Update events.
func (r *mlResolver) Watch(_ context.Context) (<-chan discovery.Event, error) {
	return r.eventCh, nil
}

// Close shuts down the memberlist and closes the event channel.
func (r *mlResolver) Close() error {
	r.mu.RLock()
	list := r.list
	r.mu.RUnlock()
	if list != nil {
		_ = list.Shutdown()
	}
	close(r.eventCh)
	return nil
}

// bootstrapSeeds reads the service instance set from persistence and resolves
// each instance's gossip address so memberlist can join the existing cluster.
// self is excluded to avoid the node trying to join itself.
func (r *mlResolver) bootstrapSeeds(ctx context.Context, service, selfInstance string) ([]string, error) {
	instances, err := r.backend.SMembers(ctx, store.ServiceSetKey(service))
	if err != nil {
		return nil, err
	}
	var seeds []string
	for _, inst := range instances {
		if inst == selfInstance {
			continue
		}
		agentURL, err := r.backend.Get(ctx, store.NodeKey(service, inst))
		if err != nil || agentURL == "" {
			continue
		}
		host := hostFromURL(agentURL)
		if host != "" {
			seeds = append(seeds, net.JoinHostPort(host, fmt.Sprintf("%d", r.gossipPort)))
		}
	}
	return seeds, nil
}

// retryJoin retries list.Join until at least one seed responds or deadline elapses.
// This handles simultaneous pod restarts where all seeds are briefly unavailable.
func retryJoin(list *memberlist.Memberlist, seeds []string, deadline time.Duration) error {
	end := time.Now().Add(deadline)
	var lastErr error
	for time.Now().Before(end) {
		n, err := list.Join(seeds)
		if err == nil {
			slog.Info("memberlist: joined", "peers", n)
			return nil
		}
		lastErr = err
		slog.Debug("memberlist: join attempt failed, retrying", "err", err)
		time.Sleep(2 * time.Second)
	}
	return lastErr
}

// delegate implements memberlist.Delegate to broadcast node metadata via gossip.
type delegate struct{ r *mlResolver }

// NodeMeta serialises this node's service, instance, gRPC port, and agent URL
// into a JSON blob that peers receive on join or update. limit is the maximum
// byte count memberlist will accept.
func (d *delegate) NodeMeta(limit int) []byte {
	d.r.mu.RLock()
	self := d.r.self
	d.r.mu.RUnlock()

	grpcPort := ""
	if self.GRPCAddr != "" {
		_, grpcPort, _ = net.SplitHostPort(self.GRPCAddr)
	}

	m := nodeMeta{
		Service:  self.Service,
		Instance: self.Instance,
		GRPCPort: grpcPort,
		AgentURL: self.AgentURL,
	}
	b, _ := json.Marshal(m)
	if len(b) > limit {
		return nil
	}
	return b
}

// NotifyMsg is required by memberlist.Delegate but unused by this implementation.
func (d *delegate) NotifyMsg([]byte) {}

// GetBroadcasts is required by memberlist.Delegate but unused by this implementation.
func (d *delegate) GetBroadcasts(int, int) [][]byte { return nil }

// LocalState is required by memberlist.Delegate but unused by this implementation.
func (d *delegate) LocalState(bool) []byte { return nil }

// MergeRemoteState is required by memberlist.Delegate but unused by this implementation.
func (d *delegate) MergeRemoteState([]byte, bool) {}

// eventDelegate implements memberlist.EventDelegate to translate memberlist events
// into discovery.Events on the resolver's event channel.
type eventDelegate struct{ r *mlResolver }

// NotifyJoin fires when a new node joins; self-join events are suppressed.
func (d *eventDelegate) NotifyJoin(n *memberlist.Node) {
	si, ok := parseNodeMeta(n)
	if !ok {
		return
	}
	d.r.mu.RLock()
	self := d.r.self
	d.r.mu.RUnlock()
	if si.Instance == self.Instance {
		return
	}
	slog.Info("peer joined", "instance", si.Instance, "service", si.Service)
	select {
	case d.r.eventCh <- discovery.Event{Type: discovery.EventJoin, Instance: si}:
	default:
	}
}

// NotifyLeave fires when a node departs the cluster.
func (d *eventDelegate) NotifyLeave(n *memberlist.Node) {
	si, ok := parseNodeMeta(n)
	if !ok {
		return
	}
	slog.Info("peer left", "instance", si.Instance, "service", si.Service)
	select {
	case d.r.eventCh <- discovery.Event{Type: discovery.EventLeave, Instance: si}:
	default:
	}
}

// NotifyUpdate fires when a node's metadata changes.
func (d *eventDelegate) NotifyUpdate(n *memberlist.Node) {
	si, ok := parseNodeMeta(n)
	if !ok {
		return
	}
	select {
	case d.r.eventCh <- discovery.Event{Type: discovery.EventUpdate, Instance: si}:
	default:
	}
}

// parseNodeMeta deserialises the memberlist node metadata into a ServiceInstance.
// ok is false when metadata is absent or malformed.
func parseNodeMeta(n *memberlist.Node) (discovery.ServiceInstance, bool) {
	if len(n.Meta) == 0 {
		return discovery.ServiceInstance{}, false
	}
	var m nodeMeta
	if err := json.Unmarshal(n.Meta, &m); err != nil || m.Service == "" {
		return discovery.ServiceInstance{}, false
	}
	return discovery.ServiceInstance{
		Service:  m.Service,
		Instance: m.Instance,
		GRPCAddr: net.JoinHostPort(n.Addr.String(), m.GRPCPort),
		AgentURL: m.AgentURL,
	}, true
}

// hostFromURL extracts the hostname from an HTTP URL of the form "http://host:port".
func hostFromURL(u string) string {
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "https://")
	host, _, err := net.SplitHostPort(u)
	if err != nil {
		return u
	}
	return host
}

// noopWriter discards all memberlist log output.
type noopWriter struct{}

// Write discards p and reports success.
func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
