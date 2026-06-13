// Package transport defines the Transport interface for sidecar-to-sidecar messaging
// and provides a provider registry so implementations can be selected at runtime.
package transport

import (
	"context"
	"fmt"
	"sync"
)

// Transport handles sidecar-to-sidecar communication for invalidation broadcast
// and direct key fetching.
type Transport interface {
	// Broadcast delivers payload to all live peers of svc and collects acknowledgements.
	// svc is the logical service name shared by all replicas.
	Broadcast(ctx context.Context, svc string, payload []byte) ([]Ack, error)
	// Get fetches the value of key from the specific instance of svc.
	Get(ctx context.Context, svc, instance, key string) ([]byte, error)
	// Close releases all resources held by the transport.
	Close() error
}

// TransportHost is the subset of Agent that transport providers call back into.
// Using this interface instead of a concrete Agent pointer breaks the import
// cycle between the transport sub-packages and the agent package.
type TransportHost interface {
	// PeersForService returns minimal address information for all live peers of svc.
	PeersForService(svc string) []PeerAddr
	// ApplyInvalidation forwards payload to this sidecar's own target service.
	ApplyInvalidation(ctx context.Context, payload []byte, origin string)
	// WriteInvalidationLog appends payload to the persistence replay log for svc.
	WriteInvalidationLog(ctx context.Context, svc string, payload []byte)
	// GetFromTarget proxies a get request to this sidecar's own target service.
	// payload is the JSON-encoded get request; the response body is returned.
	GetFromTarget(ctx context.Context, payload []byte) ([]byte, error)
	// SelfInstance returns the unique identifier of this sidecar's instance.
	SelfInstance() string
	// SelfService returns the logical service name of this sidecar's target.
	SelfService() string
}

// PeerAddr holds the minimal address information a transport needs to reach a peer.
type PeerAddr struct {
	// Instance is the unique identifier of the peer.
	Instance string
	// GRPCAddr is the peer's gRPC listen address in "host:port" form.
	GRPCAddr string
}

// Ack is the per-instance acknowledgement of a broadcast invalidation.
type Ack struct {
	Instance string `json:"instance"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

// Factory creates a Transport. host provides callbacks into the agent.
// cfg is passed from the provider config block in lens.yaml.
type Factory func(host TransportHost, cfg map[string]any) (Transport, error)

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

// Has reports whether name has been registered as a transport provider.
func Has(name string) bool {
	mu.RLock()
	_, ok := registry[name]
	mu.RUnlock()
	return ok
}

// New constructs the named transport provider with host and cfg.
// Returns an error if name is not registered.
func New(host TransportHost, name string, cfg map[string]any) (Transport, error) {
	mu.RLock()
	f, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("transport provider %q not registered (forgot blank import?)", name)
	}
	return f(host, cfg)
}
