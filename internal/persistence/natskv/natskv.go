//go:build lens_natskv

// Package natskv implements the persistence Backend using NATS JetStream KeyValue.
// Strings, lists, hashes, and sets are all stored in a single JetStream KV
// bucket named "lens". Each data type uses a key prefix (v:, l:, h:, s:) to
// avoid collisions. TTLs are simulated by embedding an expiry timestamp in the
// stored value and checking it on read.
//
// Because JetStream KV lacks per-entry TTL in nats.go v1.x, expired entries are
// cleaned up lazily: they are deleted on the next read of that key.
//
// List/hash/set operations use optimistic concurrency via kv.Update (revision
// matching), retrying up to 8 times on write conflict.
package natskv

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/Vedanshu7/lens/internal/persistence"
)

const (
	maxRetries = 8
	retryWait  = 5 * time.Millisecond
	bucket     = "lens"
)

func init() {
	persistence.Register("natskv", func(cfg map[string]any) (persistence.Backend, error) {
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
		js, err := nc.JetStream()
		if err != nil {
			nc.Close()
			return nil, err
		}
		kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
			Bucket:  bucket,
			Storage: nats.FileStorage,
		})
		if err != nil {
			// Bind to existing bucket created by another sidecar.
			kv, err = js.KeyValue(bucket)
			if err != nil {
				nc.Close()
				return nil, err
			}
		}
		return &backend{nc: nc, kv: kv}, nil
	})
}

// entry wraps any stored value with an optional expiry timestamp.
// V holds the raw string (for strings) or JSON-encoded structure (for lists/hashes/sets).
type entry struct {
	V   string `json:"v"`
	Exp string `json:"exp,omitempty"` // RFC3339; empty = no expiry
}

type backend struct {
	nc *nats.Conn
	kv nats.KeyValue
}

// kget reads and decodes the entry at natsKey. Returns zero-value entry if
// the key is missing or expired (and deletes it in the expired case).
func (b *backend) kget(natsKey string) (entry, uint64, error) {
	e, err := b.kv.Get(natsKey)
	if errors.Is(err, nats.ErrKeyNotFound) {
		return entry{}, 0, nil
	}
	if err != nil {
		return entry{}, 0, err
	}
	var en entry
	if err := json.Unmarshal(e.Value(), &en); err != nil {
		return entry{}, e.Revision(), err
	}
	if en.Exp != "" {
		t, _ := time.Parse(time.RFC3339, en.Exp)
		if time.Now().After(t) {
			b.kv.Delete(natsKey) //nolint:errcheck
			return entry{}, 0, nil
		}
	}
	return en, e.Revision(), nil
}

func (b *backend) kput(natsKey string, en entry) error {
	data, err := json.Marshal(en)
	if err != nil {
		return err
	}
	_, err = b.kv.Put(natsKey, data)
	return err
}

// kupdate uses optimistic concurrency control (revision check). Retries on conflict.
func (b *backend) kupdate(natsKey string, en entry, rev uint64) error {
	data, err := json.Marshal(en)
	if err != nil {
		return err
	}
	_, err = b.kv.Update(natsKey, data, rev)
	return err
}

// expStr converts a positive TTL to an RFC3339 expiry string.
func expStr(ttl time.Duration) string {
	if ttl <= 0 {
		return ""
	}
	return time.Now().Add(ttl).UTC().Format(time.RFC3339)
}

// --- Backend interface ---

func (b *backend) Get(_ context.Context, key string) (string, error) {
	en, _, err := b.kget("v:" + key)
	return en.V, err
}

func (b *backend) Set(_ context.Context, key, val string, ttl time.Duration) error {
	return b.kput("v:"+key, entry{V: val, Exp: expStr(ttl)})
}

func (b *backend) Del(_ context.Context, keys ...string) error {
	prefixes := []string{"v:", "l:", "h:", "s:"}
	for _, k := range keys {
		for _, pfx := range prefixes {
			b.kv.Delete(pfx + k) //nolint:errcheck
		}
	}
	return nil
}

func (b *backend) Expire(_ context.Context, key string, ttl time.Duration) error {
	exp := expStr(ttl)
	prefixes := []string{"v:", "l:", "h:", "s:"}
	for _, pfx := range prefixes {
		nk := pfx + key
		for attempt := 0; attempt < maxRetries; attempt++ {
			en, rev, err := b.kget(nk)
			if err != nil || en.V == "" {
				break // key not found under this prefix
			}
			en.Exp = exp
			if err := b.kupdate(nk, en, rev); err == nil {
				return nil
			}
			time.Sleep(retryWait)
		}
	}
	return nil
}

func (b *backend) Ping(_ context.Context) error {
	_, err := b.kv.Status()
	return err
}

func (b *backend) Close() error {
	return b.nc.Drain()
}

// --- List operations (simulated with JSON-encoded []string at l:{key}) ---

func (b *backend) LPush(_ context.Context, key string, vals ...string) error {
	nk := "l:" + key
	for attempt := 0; attempt < maxRetries; attempt++ {
		en, rev, err := b.kget(nk)
		if err != nil {
			return err
		}
		var list []string
		if en.V != "" {
			json.Unmarshal([]byte(en.V), &list) //nolint:errcheck
		}
		// Prepend in reverse order so first val ends up at index 0.
		for i := len(vals) - 1; i >= 0; i-- {
			list = append([]string{vals[i]}, list...)
		}
		data, _ := json.Marshal(list)
		newEntry := entry{V: string(data), Exp: en.Exp}
		var opErr error
		if rev == 0 {
			opErr = b.kput(nk, newEntry)
		} else {
			opErr = b.kupdate(nk, newEntry, rev)
		}
		if opErr == nil {
			return nil
		}
		time.Sleep(retryWait)
	}
	return errors.New("natskv: LPush max retries exceeded")
}

func (b *backend) LRange(_ context.Context, key string, start, stop int64) ([]string, error) {
	en, _, err := b.kget("l:" + key)
	if err != nil || en.V == "" {
		return nil, err
	}
	var list []string
	if err := json.Unmarshal([]byte(en.V), &list); err != nil {
		return nil, err
	}
	n := int64(len(list))
	if start < 0 {
		start = max64(0, n+start)
	}
	if stop < 0 {
		stop = n + stop
	} else if stop >= n {
		stop = n - 1
	}
	if start > stop || start >= n {
		return nil, nil
	}
	return list[start : stop+1], nil
}

func (b *backend) LTrim(_ context.Context, key string, start, stop int64) error {
	nk := "l:" + key
	for attempt := 0; attempt < maxRetries; attempt++ {
		en, rev, err := b.kget(nk)
		if err != nil || en.V == "" {
			return err
		}
		var list []string
		json.Unmarshal([]byte(en.V), &list) //nolint:errcheck
		n := int64(len(list))
		if start < 0 {
			start = max64(0, n+start)
		}
		if stop < 0 {
			stop = n + stop
		} else if stop >= n {
			stop = n - 1
		}
		if start > stop || start >= n {
			list = nil
		} else {
			list = list[start : stop+1]
		}
		data, _ := json.Marshal(list)
		newEntry := entry{V: string(data), Exp: en.Exp}
		if b.kupdate(nk, newEntry, rev) == nil {
			return nil
		}
		time.Sleep(retryWait)
	}
	return errors.New("natskv: LTrim max retries exceeded")
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// --- Hash operations (simulated with JSON-encoded map[string]string at h:{key}) ---

func (b *backend) HSet(_ context.Context, key, field, val string) error {
	nk := "h:" + key
	for attempt := 0; attempt < maxRetries; attempt++ {
		en, rev, err := b.kget(nk)
		if err != nil {
			return err
		}
		m := map[string]string{}
		if en.V != "" {
			json.Unmarshal([]byte(en.V), &m) //nolint:errcheck
		}
		m[field] = val
		data, _ := json.Marshal(m)
		newEntry := entry{V: string(data), Exp: en.Exp}
		var opErr error
		if rev == 0 {
			opErr = b.kput(nk, newEntry)
		} else {
			opErr = b.kupdate(nk, newEntry, rev)
		}
		if opErr == nil {
			return nil
		}
		time.Sleep(retryWait)
	}
	return errors.New("natskv: HSet max retries exceeded")
}

func (b *backend) HGetAll(_ context.Context, key string) (map[string]string, error) {
	en, _, err := b.kget("h:" + key)
	if err != nil || en.V == "" {
		return map[string]string{}, err
	}
	m := map[string]string{}
	if err := json.Unmarshal([]byte(en.V), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (b *backend) HGetAllMulti(_ context.Context, keys []string) ([]map[string]string, error) {
	out := make([]map[string]string, len(keys))
	for i, key := range keys {
		en, _, err := b.kget("h:" + key)
		if err != nil {
			out[i] = map[string]string{}
			continue
		}
		m := map[string]string{}
		if en.V != "" {
			json.Unmarshal([]byte(en.V), &m) //nolint:errcheck
		}
		out[i] = m
	}
	return out, nil
}

// --- Set operations (simulated with JSON-encoded []string at s:{key}) ---

func (b *backend) SAdd(_ context.Context, key string, members ...string) error {
	nk := "s:" + key
	for attempt := 0; attempt < maxRetries; attempt++ {
		en, rev, err := b.kget(nk)
		if err != nil {
			return err
		}
		existing := map[string]struct{}{}
		var set []string
		if en.V != "" {
			json.Unmarshal([]byte(en.V), &set) //nolint:errcheck
		}
		for _, m := range set {
			existing[m] = struct{}{}
		}
		for _, m := range members {
			if _, ok := existing[m]; !ok {
				set = append(set, m)
				existing[m] = struct{}{}
			}
		}
		data, _ := json.Marshal(set)
		newEntry := entry{V: string(data), Exp: en.Exp}
		var opErr error
		if rev == 0 {
			opErr = b.kput(nk, newEntry)
		} else {
			opErr = b.kupdate(nk, newEntry, rev)
		}
		if opErr == nil {
			return nil
		}
		time.Sleep(retryWait)
	}
	return errors.New("natskv: SAdd max retries exceeded")
}

func (b *backend) SRem(_ context.Context, key string, members ...string) error {
	nk := "s:" + key
	for attempt := 0; attempt < maxRetries; attempt++ {
		en, rev, err := b.kget(nk)
		if err != nil || en.V == "" {
			return err
		}
		remove := map[string]struct{}{}
		for _, m := range members {
			remove[m] = struct{}{}
		}
		var set []string
		var existing []string
		json.Unmarshal([]byte(en.V), &existing) //nolint:errcheck
		for _, m := range existing {
			if _, ok := remove[m]; !ok {
				set = append(set, m)
			}
		}
		data, _ := json.Marshal(set)
		newEntry := entry{V: string(data), Exp: en.Exp}
		if b.kupdate(nk, newEntry, rev) == nil {
			return nil
		}
		time.Sleep(retryWait)
	}
	return errors.New("natskv: SRem max retries exceeded")
}

func (b *backend) SMembers(_ context.Context, key string) ([]string, error) {
	en, _, err := b.kget("s:" + key)
	if err != nil || en.V == "" {
		return nil, err
	}
	var set []string
	if err := json.Unmarshal([]byte(en.V), &set); err != nil {
		return nil, err
	}
	return set, nil
}

// --- Pipeline (sequential execution — no network batching possible with KV) ---

func (b *backend) Pipeline() persistence.Pipeliner {
	return &pipeliner{b: b}
}

type pipeliner struct {
	mu  sync.Mutex
	b   *backend
	ops []func() error
}

func (p *pipeliner) Set(ctx context.Context, key, val string, ttl time.Duration) {
	p.mu.Lock()
	p.ops = append(p.ops, func() error { return p.b.Set(ctx, key, val, ttl) })
	p.mu.Unlock()
}

func (p *pipeliner) HSet(ctx context.Context, key, field, val string) {
	p.mu.Lock()
	p.ops = append(p.ops, func() error { return p.b.HSet(ctx, key, field, val) })
	p.mu.Unlock()
}

func (p *pipeliner) LPush(ctx context.Context, key string, vals ...string) {
	p.mu.Lock()
	copied := append([]string(nil), vals...)
	p.ops = append(p.ops, func() error { return p.b.LPush(ctx, key, copied...) })
	p.mu.Unlock()
}

func (p *pipeliner) LTrim(ctx context.Context, key string, start, stop int64) {
	p.mu.Lock()
	p.ops = append(p.ops, func() error { return p.b.LTrim(ctx, key, start, stop) })
	p.mu.Unlock()
}

func (p *pipeliner) Expire(ctx context.Context, key string, ttl time.Duration) {
	p.mu.Lock()
	p.ops = append(p.ops, func() error { return p.b.Expire(ctx, key, ttl) })
	p.mu.Unlock()
}

func (p *pipeliner) Exec(_ context.Context) error {
	p.mu.Lock()
	ops := p.ops
	p.ops = nil
	p.mu.Unlock()
	for _, op := range ops {
		if err := op(); err != nil {
			return err
		}
	}
	return nil
}
