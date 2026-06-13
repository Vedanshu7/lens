// Package memorypersistence implements a persistence Backend backed by in-process maps.
// All data is lost on process restart. TTLs and expiry are no-ops.
// Use for local development and tests where no external store is available.
package memorypersistence

import (
	"context"
	"sync"
	"time"

	"github.com/Vedanshu7/lens/internal/persistence"
)

func init() {
	persistence.Register("memory", func(cfg map[string]any) (persistence.Backend, error) {
		return &backend{
			kv:     map[string]string{},
			lists:  map[string][]string{},
			hashes: map[string]map[string]string{},
			sets:   map[string]map[string]struct{}{},
		}, nil
	})
}

type backend struct {
	mu     sync.RWMutex
	kv     map[string]string
	lists  map[string][]string
	hashes map[string]map[string]string
	sets   map[string]map[string]struct{}
}

// Get returns the value stored at key, or an empty string if absent.
func (b *backend) Get(_ context.Context, key string) (string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.kv[key], nil
}

// Set stores val at key. ttl is ignored by this backend.
func (b *backend) Set(_ context.Context, key, val string, _ time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.kv[key] = val
	return nil
}

// Del removes keys from all store types.
func (b *backend) Del(_ context.Context, keys ...string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, k := range keys {
		delete(b.kv, k)
		delete(b.lists, k)
		delete(b.hashes, k)
		delete(b.sets, k)
	}
	return nil
}

// LPush prepends vals to the list at key, matching Redis LPush semantics.
func (b *backend) LPush(_ context.Context, key string, vals ...string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(vals) - 1; i >= 0; i-- {
		b.lists[key] = append([]string{vals[i]}, b.lists[key]...)
	}
	return nil
}

// LRange returns elements from start to stop (inclusive), clamping to list bounds.
func (b *backend) LRange(_ context.Context, key string, start, stop int64) ([]string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	list := b.lists[key]
	n := int64(len(list))
	if stop < 0 {
		stop = n + stop
	}
	if start < 0 {
		start = 0
	}
	if stop >= n {
		stop = n - 1
	}
	var out []string
	if start <= stop {
		out = make([]string, stop-start+1)
		copy(out, list[start:stop+1])
	}
	return out, nil
}

// LTrim retains only the elements between start and stop (inclusive).
func (b *backend) LTrim(_ context.Context, key string, start, stop int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.lists[key]
	n := int64(len(list))
	if stop < 0 {
		stop = n + stop
	}
	if start >= n || start > stop {
		b.lists[key] = nil
	} else {
		if stop >= n {
			stop = n - 1
		}
		b.lists[key] = list[start : stop+1]
	}
	return nil
}

// HSet sets field to val in the hash stored at key.
func (b *backend) HSet(_ context.Context, key, field, val string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hashes[key] == nil {
		b.hashes[key] = map[string]string{}
	}
	b.hashes[key][field] = val
	return nil
}

// HGetAll returns a copy of all fields and values in the hash stored at key.
func (b *backend) HGetAll(_ context.Context, key string) (map[string]string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := map[string]string{}
	for k, v := range b.hashes[key] {
		out[k] = v
	}
	return out, nil
}

// HGetAllMulti returns field maps for each key in order. Missing keys yield an empty map.
func (b *backend) HGetAllMulti(_ context.Context, keys []string) ([]map[string]string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	results := make([]map[string]string, len(keys))
	for i, key := range keys {
		out := map[string]string{}
		for k, v := range b.hashes[key] {
			out[k] = v
		}
		results[i] = out
	}
	return results, nil
}

// SAdd adds members to the set stored at key.
func (b *backend) SAdd(_ context.Context, key string, members ...string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sets[key] == nil {
		b.sets[key] = map[string]struct{}{}
	}
	for _, m := range members {
		b.sets[key][m] = struct{}{}
	}
	return nil
}

// SRem removes members from the set stored at key.
func (b *backend) SRem(_ context.Context, key string, members ...string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range members {
		delete(b.sets[key], m)
	}
	return nil
}

// SMembers returns all members of the set stored at key.
func (b *backend) SMembers(_ context.Context, key string) ([]string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.sets[key]))
	for m := range b.sets[key] {
		out = append(out, m)
	}
	return out, nil
}

// Expire is a no-op for this backend; TTLs are not tracked in memory.
func (b *backend) Expire(_ context.Context, _ string, _ time.Duration) error { return nil }

// Ping always returns nil because the in-process store is always available.
func (b *backend) Ping(_ context.Context) error { return nil }

// Close is a no-op; there are no resources to release.
func (b *backend) Close() error { return nil }

// Pipeline returns a Pipeliner that executes operations immediately and sequentially.
func (b *backend) Pipeline() persistence.Pipeliner {
	return &memPipeliner{b: b}
}

// memPipeliner runs each operation immediately. No batching is needed for an in-process store.
type memPipeliner struct{ b *backend }

// Set executes the set operation immediately.
func (p *memPipeliner) Set(ctx context.Context, key, val string, ttl time.Duration) {
	p.b.Set(ctx, key, val, ttl) //nolint:errcheck
}

// HSet executes the hash-set operation immediately.
func (p *memPipeliner) HSet(ctx context.Context, key, field, val string) {
	p.b.HSet(ctx, key, field, val) //nolint:errcheck
}

// LPush executes the list-prepend operation immediately.
func (p *memPipeliner) LPush(ctx context.Context, key string, vals ...string) {
	p.b.LPush(ctx, key, vals...) //nolint:errcheck
}

// LTrim executes the list-trim operation immediately.
func (p *memPipeliner) LTrim(ctx context.Context, key string, start, stop int64) {
	p.b.LTrim(ctx, key, start, stop) //nolint:errcheck
}

// Expire executes the expire operation immediately (no-op for this backend).
func (p *memPipeliner) Expire(ctx context.Context, key string, ttl time.Duration) {
	p.b.Expire(ctx, key, ttl) //nolint:errcheck
}

// Exec is a no-op because operations are executed immediately.
func (p *memPipeliner) Exec(_ context.Context) error { return nil }
