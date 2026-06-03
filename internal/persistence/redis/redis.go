// Package redispersistence implements the persistence Backend using Redis.
// It wraps go-redis/v9 and maps every Backend method to the equivalent Redis command.
package redispersistence

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/vedanshu/lens/internal/persistence"
)

func init() {
	persistence.Register("redis", func(cfg map[string]any) (persistence.Backend, error) {
		addr, _ := cfg["addr"].(string)
		if addr == "" {
			addr = "localhost:6379"
		}
		db := 0
		if v, ok := cfg["db"].(int); ok {
			db = v
		}
		password, _ := cfg["password"].(string)
		return &backend{
			c: goredis.NewClient(&goredis.Options{Addr: addr, DB: db, Password: password}),
		}, nil
	})
}

type backend struct{ c *goredis.Client }

// Get returns the value at key, or an empty string when the key does not exist.
func (b *backend) Get(ctx context.Context, key string) (string, error) {
	v, err := b.c.Get(ctx, key).Result()
	if errors.Is(err, goredis.Nil) {
		return "", nil
	}
	return v, err
}

// Set stores val at key with the given TTL. A zero TTL means no expiry.
func (b *backend) Set(ctx context.Context, key, val string, ttl time.Duration) error {
	return b.c.Set(ctx, key, val, ttl).Err()
}

// Del removes one or more keys.
func (b *backend) Del(ctx context.Context, keys ...string) error {
	return b.c.Del(ctx, keys...).Err()
}

// LPush prepends vals to the list at key.
func (b *backend) LPush(ctx context.Context, key string, vals ...string) error {
	args := make([]any, len(vals))
	for i, v := range vals {
		args[i] = v
	}
	return b.c.LPush(ctx, key, args...).Err()
}

// LRange returns elements from start to stop (inclusive).
func (b *backend) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return b.c.LRange(ctx, key, start, stop).Result()
}

// LTrim retains only the elements between start and stop.
func (b *backend) LTrim(ctx context.Context, key string, start, stop int64) error {
	return b.c.LTrim(ctx, key, start, stop).Err()
}

// HSet sets field to val in the hash stored at key.
func (b *backend) HSet(ctx context.Context, key, field, val string) error {
	return b.c.HSet(ctx, key, field, val).Err()
}

// HGetAll returns all fields and values of the hash stored at key.
func (b *backend) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return b.c.HGetAll(ctx, key).Result()
}

// HGetAllMulti fetches multiple hashes in a single pipelined round-trip.
// Results are in the same order as keys; missing keys produce an empty map.
func (b *backend) HGetAllMulti(ctx context.Context, keys []string) ([]map[string]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	pipe := b.c.Pipeline()
	cmds := make([]*goredis.MapStringStringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.HGetAll(ctx, k)
	}
	_, pipeErr := pipe.Exec(ctx)
	if pipeErr != nil && !errors.Is(pipeErr, goredis.Nil) {
		return nil, pipeErr
	}
	results := make([]map[string]string, len(keys))
	var err error
	for i, cmd := range cmds {
		var m map[string]string
		m, err = cmd.Result()
		if err != nil && !errors.Is(err, goredis.Nil) {
			return nil, err
		}
		if m == nil {
			m = map[string]string{}
		}
		results[i] = m
	}
	return results, nil
}

// SAdd adds members to the set stored at key.
func (b *backend) SAdd(ctx context.Context, key string, members ...string) error {
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	return b.c.SAdd(ctx, key, args...).Err()
}

// SRem removes members from the set stored at key.
func (b *backend) SRem(ctx context.Context, key string, members ...string) error {
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	return b.c.SRem(ctx, key, args...).Err()
}

// SMembers returns all members of the set stored at key.
func (b *backend) SMembers(ctx context.Context, key string) ([]string, error) {
	return b.c.SMembers(ctx, key).Result()
}

// Expire sets a TTL on key.
func (b *backend) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return b.c.Expire(ctx, key, ttl).Err()
}

// Ping checks the Redis connection.
func (b *backend) Ping(ctx context.Context) error {
	return b.c.Ping(ctx).Err()
}

// Pipeline returns a Pipeliner backed by a Redis pipeline.
func (b *backend) Pipeline() persistence.Pipeliner {
	return &pipeliner{pipe: b.c.Pipeline()}
}

// Close closes the underlying Redis client connection.
func (b *backend) Close() error {
	return b.c.Close()
}

type pipeliner struct{ pipe goredis.Pipeliner }

// Set queues a Redis SET command.
func (p *pipeliner) Set(ctx context.Context, key, val string, ttl time.Duration) {
	p.pipe.Set(ctx, key, val, ttl)
}

// HSet queues a Redis HSET command.
func (p *pipeliner) HSet(ctx context.Context, key, field, val string) {
	p.pipe.HSet(ctx, key, field, val)
}

// LPush queues a Redis LPUSH command.
func (p *pipeliner) LPush(ctx context.Context, key string, vals ...string) {
	args := make([]any, len(vals))
	for i, v := range vals {
		args[i] = v
	}
	p.pipe.LPush(ctx, key, args...)
}

// LTrim queues a Redis LTRIM command.
func (p *pipeliner) LTrim(ctx context.Context, key string, start, stop int64) {
	p.pipe.LTrim(ctx, key, start, stop)
}

// Expire queues a Redis EXPIRE command.
func (p *pipeliner) Expire(ctx context.Context, key string, ttl time.Duration) {
	p.pipe.Expire(ctx, key, ttl)
}

// Exec flushes all queued commands. Returns the first error encountered.
func (p *pipeliner) Exec(ctx context.Context) error {
	_, err := p.pipe.Exec(ctx)
	return err
}
