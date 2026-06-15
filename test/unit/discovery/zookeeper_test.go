package discovery_test

import (
	"testing"

	_ "github.com/Vedanshu7/lens/internal/discovery/zookeeper"

	"github.com/Vedanshu7/lens/internal/discovery"
)

func TestZookeeperDiscovery_MissingServers_ReturnsError(t *testing.T) {
	_, err := discovery.New(nil, "zookeeper", map[string]any{})
	if err == nil {
		t.Error("expected error when servers config is missing")
	}
}

func TestZookeeperDiscovery_Registered(t *testing.T) {
	if !discovery.Has("zookeeper") {
		t.Error("expected zookeeper to be registered")
	}
}
