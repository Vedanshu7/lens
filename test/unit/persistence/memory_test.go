package persistence_test

import (
	"context"
	"sort"
	"testing"

	"github.com/Vedanshu7/lens/internal/persistence"
	_ "github.com/Vedanshu7/lens/internal/persistence/memory"
)

func newMemBackend(t *testing.T) persistence.Backend {
	t.Helper()
	b, err := persistence.New("memory", nil)
	if err != nil {
		t.Fatalf("create memory backend: %v", err)
	}
	t.Cleanup(func() { b.Close() }) //nolint:errcheck
	return b
}

func TestMemoryBackend_GetSet(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend(t)

	got, err := b.Get(ctx, "missing")
	if err != nil || got != "" {
		t.Fatalf("Get missing key: want (\"\", nil), got (%q, %v)", got, err)
	}

	if err := b.Set(ctx, "k", "v", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err = b.Get(ctx, "k")
	if err != nil || got != "v" {
		t.Fatalf("Get after Set: want (\"v\", nil), got (%q, %v)", got, err)
	}
}

func TestMemoryBackend_Del(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend(t)

	b.Set(ctx, "a", "1", 0) //nolint:errcheck
	b.Set(ctx, "b", "2", 0) //nolint:errcheck
	b.Del(ctx, "a", "b")    //nolint:errcheck

	for _, k := range []string{"a", "b"} {
		if v, _ := b.Get(ctx, k); v != "" {
			t.Errorf("Del: key %q still present", k)
		}
	}
}

func TestMemoryBackend_LPush_MatchesRedisSemantics(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend(t)

	b.LPush(ctx, "list", "a", "b", "c") //nolint:errcheck

	got, err := b.LRange(ctx, "list", 0, -1)
	if err != nil {
		t.Fatalf("LRange: %v", err)
	}
	want := []string{"c", "b", "a"}
	if len(got) != len(want) {
		t.Fatalf("LRange len: want %d, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("LRange[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestMemoryBackend_LTrim(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend(t)

	b.LPush(ctx, "list", "a", "b", "c", "d", "e") //nolint:errcheck
	b.LTrim(ctx, "list", 0, 1)                     //nolint:errcheck

	got, _ := b.LRange(ctx, "list", 0, -1)
	if len(got) != 2 {
		t.Fatalf("LTrim: want 2 elements, got %d (%v)", len(got), got)
	}
}

func TestMemoryBackend_Hash(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend(t)

	b.HSet(ctx, "h", "f1", "v1") //nolint:errcheck
	b.HSet(ctx, "h", "f2", "v2") //nolint:errcheck

	m, err := b.HGetAll(ctx, "h")
	if err != nil {
		t.Fatalf("HGetAll: %v", err)
	}
	if m["f1"] != "v1" || m["f2"] != "v2" {
		t.Errorf("HGetAll: got %v", m)
	}
}

func TestMemoryBackend_HGetAllMulti(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend(t)

	b.HSet(ctx, "h1", "k", "v1") //nolint:errcheck
	b.HSet(ctx, "h2", "k", "v2") //nolint:errcheck

	results, err := b.HGetAllMulti(ctx, []string{"h1", "h2", "missing"})
	if err != nil {
		t.Fatalf("HGetAllMulti: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("HGetAllMulti: want 3 results, got %d", len(results))
	}
	if results[0]["k"] != "v1" || results[1]["k"] != "v2" {
		t.Errorf("HGetAllMulti values wrong: %v", results)
	}
	if len(results[2]) != 0 {
		t.Errorf("HGetAllMulti missing key should return empty map, got %v", results[2])
	}
}

func TestMemoryBackend_SetOperations(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend(t)

	b.SAdd(ctx, "s", "x", "y", "z") //nolint:errcheck

	members, err := b.SMembers(ctx, "s")
	if err != nil {
		t.Fatalf("SMembers: %v", err)
	}
	sort.Strings(members)
	if len(members) != 3 || members[0] != "x" || members[1] != "y" || members[2] != "z" {
		t.Errorf("SMembers: got %v", members)
	}

	b.SRem(ctx, "s", "y") //nolint:errcheck
	members, _ = b.SMembers(ctx, "s")
	sort.Strings(members)
	if len(members) != 2 || members[0] != "x" || members[1] != "z" {
		t.Errorf("SRem: got %v", members)
	}
}

func TestMemoryBackend_Pipeline(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend(t)

	pipe := b.Pipeline()
	pipe.Set(ctx, "pk", "pv", 0)
	pipe.HSet(ctx, "ph", "f", "v")
	pipe.LPush(ctx, "pl", "a", "b")
	if err := pipe.Exec(ctx); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if v, _ := b.Get(ctx, "pk"); v != "pv" {
		t.Errorf("pipeline Set: want \"pv\", got %q", v)
	}
	if m, _ := b.HGetAll(ctx, "ph"); m["f"] != "v" {
		t.Errorf("pipeline HSet: want \"v\", got %q", m["f"])
	}
	if l, _ := b.LRange(ctx, "pl", 0, -1); len(l) != 2 {
		t.Errorf("pipeline LPush: want 2 elements, got %d", len(l))
	}
}

func TestMemoryBackend_Ping(t *testing.T) {
	b := newMemBackend(t)
	if err := b.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
