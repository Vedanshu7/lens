package agent_test

import (
	"testing"

	"github.com/Vedanshu7/lens/internal/agent"
	_ "github.com/Vedanshu7/lens/internal/discovery/static"
	_ "github.com/Vedanshu7/lens/internal/persistence/memory"
	_ "github.com/Vedanshu7/lens/internal/target/http"
)

func TestValidateConfig_MissingTransport(t *testing.T) {
	cfg := agent.Config{Persistence: "memory", Discovery: "static", Target: "http"}
	if err := agent.ValidateConfig(cfg); err == nil {
		t.Error("want error for missing transport, got nil")
	}
}

func TestValidateConfig_UnknownTransport(t *testing.T) {
	cfg := agent.Config{Transport: "unknown", Persistence: "memory", Discovery: "static", Target: "http"}
	if err := agent.ValidateConfig(cfg); err == nil {
		t.Error("want error for unregistered transport, got nil")
	}
}

func TestValidateConfig_MissingDiscovery(t *testing.T) {
	cfg := agent.Config{Transport: "__test_transport_registry__", Persistence: "memory", Target: "http"}
	if err := agent.ValidateConfig(cfg); err == nil {
		t.Error("want error for missing discovery, got nil")
	}
}

func TestValidateConfig_UnknownPersistence(t *testing.T) {
	cfg := agent.Config{
		Transport: "__test_transport_registry__", Persistence: "unknown",
		Discovery: "static", Target: "http",
	}
	if err := agent.ValidateConfig(cfg); err == nil {
		t.Error("want error for unregistered persistence, got nil")
	}
}

func TestValidateConfig_UnknownTarget(t *testing.T) {
	cfg := agent.Config{
		Transport: "__test_transport_registry__", Persistence: "memory",
		Discovery: "static", Target: "unknown",
	}
	if err := agent.ValidateConfig(cfg); err == nil {
		t.Error("want error for unregistered target, got nil")
	}
}

func TestValidateConfig_ValidConfig(t *testing.T) {
	cfg := agent.Config{
		Transport:   "__test_transport_registry__",
		Persistence: "memory",
		Discovery:   "static",
		Target:      "http",
	}
	if err := agent.ValidateConfig(cfg); err != nil {
		t.Errorf("want nil error for valid config, got: %v", err)
	}
}

func TestValidateConfig_UnknownObserver(t *testing.T) {
	cfg := agent.Config{
		Transport:   "__test_transport_registry__",
		Persistence: "memory",
		Discovery:   "static",
		Target:      "http",
		ObserverProviders: []agent.ObserverProviderConfig{
			{Name: "unknown-obs"},
		},
	}
	if err := agent.ValidateConfig(cfg); err == nil {
		t.Error("want error for unregistered observer, got nil")
	}
}
