// Package persistence defines the Backend interface for key-value, list, hash,
// and set storage and provides a provider registry. All 13 Redis call sites in
// the agent map onto these methods so any compatible store can be substituted.
package persistence

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Backend abstracts key-value, list, hash, and set storage operations.
type Backend interface {
	// Get returns the value stored at key, or an empty string if the key does not exist.
	Get(ctx context.Context, key string) (string, error)
	// Set stores val at key with the given TTL. A zero TTL means no expiry.
	Set(ctx context.Context, key, val string, ttl time.Duration) error
	// Del removes one or more keys.
	Del(ctx context.Context, keys ...string) error
	// LPush prepends vals to the list at key.
	LPush(ctx context.Context, key string, vals ...string) error
	// LRange returns the elements of the list at key from start to stop (inclusive).
	LRange(ctx context.Context, key string, start, stop int64) ([]string, error)
	// LTrim trims the list at key to the elements between start and stop (inclusive).
	LTrim(ctx context.Context, key string, start, stop int64) error
	// HSet sets field to val in the hash stored at key.
	HSet(ctx context.Context, key, field, val string) error
	// HGetAll returns all fields and values of the hash stored at key.
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	// HGetAllMulti fetches multiple hash keys in a single round-trip.
	// Results are returned in the same order as keys; missing keys yield an empty map.
	HGetAllMulti(ctx context.Context, keys []string) ([]map[string]string, error)
	// SAdd adds members to the set stored at key.
	SAdd(ctx context.Context, key string, members ...string) error
	// SRem removes members from the set stored at key.
	SRem(ctx context.Context, key string, members ...string) error
	// SMembers returns all members of the set stored at key.
	SMembers(ctx context.Context, key string) ([]string, error)
	// Expire sets a TTL on key. Returns no error if the key does not exist.
	Expire(ctx context.Context, key string, ttl time.Duration) error
	// Ping verifies the backend connection is alive.
	Ping(ctx context.Context) error
	// Pipeline returns a Pipeliner that batches write operations into a single round-trip.
	Pipeline() Pipeliner
	// Close releases all resources held by this backend.
	Close() error
}

// Pipeliner batches multiple write operations into one round-trip.
// Non-Redis backends may execute operations immediately and sequentially.
type Pipeliner interface {
	// Set queues a set operation.
	Set(ctx context.Context, key, val string, ttl time.Duration)
	// HSet queues a hash field set operation.
	HSet(ctx context.Context, key, field, val string)
	// LPush queues a list prepend operation.
	LPush(ctx context.Context, key string, vals ...string)
	// LTrim queues a list trim operation.
	LTrim(ctx context.Context, key string, start, stop int64)
	// Expire queues a TTL set operation.
	Expire(ctx context.Context, key string, ttl time.Duration)
	// Exec flushes all queued operations and returns the first error encountered.
	Exec(ctx context.Context) error
}

// Factory creates a Backend from provider config.
type Factory func(cfg map[string]any) (Backend, error)

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

// Has reports whether name has been registered as a persistence provider.
func Has(name string) bool {
	mu.RLock()
	_, ok := registry[name]
	mu.RUnlock()
	return ok
}

// New constructs the named persistence provider from cfg.
// Returns an error if name is not registered.
func New(name string, cfg map[string]any) (Backend, error) {
	mu.RLock()
	f, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("persistence provider %q not registered (forgot blank import?)", name)
	}
	return f(cfg)
}
