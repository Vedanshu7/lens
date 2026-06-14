package store_test

import (
	"strings"
	"testing"

	"github.com/Vedanshu7/lens/internal/store"
)

func TestNodeKey_ContainsServiceAndInstance(t *testing.T) {
	k := store.NodeKey("svc", "inst")
	if !strings.Contains(k, "svc") || !strings.Contains(k, "inst") {
		t.Errorf("NodeKey: unexpected format %q", k)
	}
	if !strings.HasPrefix(k, store.KeyPrefix) {
		t.Errorf("NodeKey: missing prefix, got %q", k)
	}
}

func TestCacheKey_ContainsServiceAndInstance(t *testing.T) {
	k := store.CacheKey("svc", "inst")
	if !strings.Contains(k, "cache") || !strings.Contains(k, "svc") || !strings.Contains(k, "inst") {
		t.Errorf("CacheKey: unexpected format %q", k)
	}
}

func TestLogKey_ContainsService(t *testing.T) {
	k := store.LogKey("svc")
	if !strings.Contains(k, "log") || !strings.Contains(k, "svc") {
		t.Errorf("LogKey: unexpected format %q", k)
	}
}

func TestCheckpointKey_ContainsServiceAndInstance(t *testing.T) {
	k := store.CheckpointKey("svc", "inst")
	if !strings.Contains(k, "checkpoint") || !strings.Contains(k, "svc") || !strings.Contains(k, "inst") {
		t.Errorf("CheckpointKey: unexpected format %q", k)
	}
}

func TestAuditKey_ContainsAudit(t *testing.T) {
	k := store.AuditKey()
	if !strings.Contains(k, "audit") {
		t.Errorf("AuditKey: unexpected format %q", k)
	}
}

func TestServiceSetKey_ContainsService(t *testing.T) {
	k := store.ServiceSetKey("svc")
	if !strings.Contains(k, "svc") {
		t.Errorf("ServiceSetKey: unexpected format %q", k)
	}
}

func TestServicesSetKey_ContainsServices(t *testing.T) {
	k := store.ServicesSetKey()
	if !strings.Contains(k, "services") {
		t.Errorf("ServicesSetKey: unexpected format %q", k)
	}
}

func TestProvidersKey_ContainsService(t *testing.T) {
	k := store.ProvidersKey("svc")
	if !strings.Contains(k, "providers") || !strings.Contains(k, "svc") {
		t.Errorf("ProvidersKey: unexpected format %q", k)
	}
}

func TestKeys_AreUnique(t *testing.T) {
	keys := []string{
		store.NodeKey("s", "i"),
		store.CacheKey("s", "i"),
		store.LogKey("s"),
		store.CheckpointKey("s", "i"),
		store.AuditKey(),
		store.ServiceSetKey("s"),
		store.ServicesSetKey(),
		store.ProvidersKey("s"),
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Errorf("duplicate key: %q", k)
		}
		seen[k] = true
	}
}

func TestKeys_AllHavePrefix(t *testing.T) {
	keys := []string{
		store.NodeKey("s", "i"),
		store.CacheKey("s", "i"),
		store.LogKey("s"),
		store.CheckpointKey("s", "i"),
		store.AuditKey(),
		store.ServiceSetKey("s"),
		store.ServicesSetKey(),
		store.ProvidersKey("s"),
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, store.KeyPrefix) {
			t.Errorf("key %q does not start with KeyPrefix %q", k, store.KeyPrefix)
		}
	}
}
