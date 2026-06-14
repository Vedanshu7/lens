// Package target defines the TargetClient interface for sidecar-to-service
// communication and provides a provider registry so implementations can be
// selected at runtime via the target.provider config key.
package target

import (
	"context"
	"fmt"
	"sync"
)

// TargetInfo is the identity payload returned by the target service's info endpoint.
type TargetInfo struct {
	Service  string `json:"service"`
	Instance string `json:"instance"`
}

// TargetClient abstracts all communication from the Lens sidecar to its
// co-located target service. It is safe for concurrent use.
type TargetClient interface {
	// Info fetches the service identity from the target's info endpoint.
	Info(ctx context.Context) (TargetInfo, error)
	// Invalidate delivers a cache invalidation payload to the target.
	// Returns an error on network failure or non-2xx / 5xx response so that
	// callers can apply retry logic at the agent layer.
	Invalidate(ctx context.Context, payload []byte) error
	// Get fetches the current value of key from the target's cache.
	// Returns the raw response body.
	Get(ctx context.Context, key string) ([]byte, error)
	// Keys fetches the target's declared cache key list, applying optional
	// pattern, limit, and offset filters. Returns the raw response body.
	Keys(ctx context.Context, pattern, limit, offset string) ([]byte, error)
	// Close releases all resources held by this client.
	Close() error
}

// Factory creates a TargetClient from provider config.
type Factory func(cfg map[string]any) (TargetClient, error)

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

// Has reports whether name has been registered as a target provider.
func Has(name string) bool {
	mu.RLock()
	_, ok := registry[name]
	mu.RUnlock()
	return ok
}

// New constructs the named target provider from cfg.
// Returns an error if name is not registered.
func New(name string, cfg map[string]any) (TargetClient, error) {
	mu.RLock()
	f, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("target provider %q not registered (forgot blank import?)", name)
	}
	return f(cfg)
}
