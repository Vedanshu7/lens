// Package discovery defines the Resolver interface for peer discovery and
// provides a provider registry so implementations can be selected at runtime
// via blank imports without modifying this package.
package discovery

import (
	"context"
	"fmt"
	"sync"

	"github.com/vedanshu/lens/internal/persistence"
)

// Resolver discovers and tracks live peers of a named service.
type Resolver interface {
	// Register announces this instance to the discovery backend so peers can find it.
	Register(ctx context.Context, self ServiceInstance) error
	// Deregister removes this instance from the discovery backend on clean shutdown.
	Deregister(ctx context.Context, self ServiceInstance) error
	// Peers returns the current live set of instances for service.
	// Fast implementations use an in-memory cache populated by Watch events.
	Peers(ctx context.Context, service string) ([]ServiceInstance, error)
	// Watch returns a channel that emits peer lifecycle events.
	// The caller should drain this channel until it is closed.
	Watch(ctx context.Context) (<-chan Event, error)
	// Close releases any resources held by the resolver.
	Close() error
}

// ServiceInstance describes a live instance of a service.
type ServiceInstance struct {
	// Service is the logical service name shared by all replicas.
	Service string
	// Instance is the unique identifier for this replica.
	Instance string
	// GRPCAddr is the peer's gRPC address in "host:port" form, set by the discovery provider.
	GRPCAddr string
	// AgentURL is the HTTP base URL of the peer's Lens sidecar ("http://host:port").
	AgentURL string
	// Meta holds provider-specific tags (e.g., region, zone).
	Meta map[string]string
}

// EventType classifies a peer lifecycle event.
type EventType string

const (
	// EventJoin is emitted when a new peer joins the cluster.
	EventJoin EventType = "join"
	// EventLeave is emitted when a peer departs the cluster.
	EventLeave EventType = "leave"
	// EventUpdate is emitted when a peer's metadata changes.
	EventUpdate EventType = "update"
)

// Event is a peer lifecycle notification emitted by Watch.
type Event struct {
	Type     EventType
	Instance ServiceInstance
}

// Factory creates a Resolver. backend is supplied so providers can use
// persistence for bootstrap seed reading and self-registration.
type Factory func(backend persistence.Backend, cfg map[string]any) (Resolver, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register records f under name so it can be selected at runtime.
// It is called from provider init() functions.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = f
}

// Has reports whether name has been registered as a discovery provider.
func Has(name string) bool {
	mu.RLock()
	_, ok := registry[name]
	mu.RUnlock()
	return ok
}

// New constructs the named discovery provider. backend is passed to the
// provider factory for bootstrap seed access. Returns an error if name is
// not registered.
func New(backend persistence.Backend, name string, cfg map[string]any) (Resolver, error) {
	mu.RLock()
	f, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("discovery provider %q not registered (forgot blank import?)", name)
	}
	return f(backend, cfg)
}
