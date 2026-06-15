package discovery_test

import (
	"testing"

	_ "github.com/Vedanshu7/lens/internal/discovery/mdns"

	"github.com/Vedanshu7/lens/internal/discovery"
)

func TestMDNSDiscovery_Registered(t *testing.T) {
	if !discovery.Has("mdns") {
		t.Error("expected mdns to be registered")
	}
}

func TestMDNSDiscovery_DefaultConfig(t *testing.T) {
	r, err := discovery.New(nil, "mdns", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error with default config: %v", err)
	}
	r.Close() //nolint:errcheck
}
