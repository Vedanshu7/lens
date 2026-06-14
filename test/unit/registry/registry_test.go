package registry_test

import (
	"context"
	"testing"

	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/observability"
	"github.com/Vedanshu7/lens/internal/persistence"
	_ "github.com/Vedanshu7/lens/internal/persistence/memory"
	"github.com/Vedanshu7/lens/internal/target"
	_ "github.com/Vedanshu7/lens/internal/target/http"
	"github.com/Vedanshu7/lens/internal/transport"
)

// --- persistence ---

func TestPersistenceRegistry_HasMemory(t *testing.T) {
	if !persistence.Has("memory") {
		t.Error("persistence.Has(memory): want true")
	}
}

func TestPersistenceRegistry_HasMissing(t *testing.T) {
	if persistence.Has("__not_registered__") {
		t.Error("persistence.Has(unregistered): want false")
	}
}

func TestPersistenceRegistry_NewMemory(t *testing.T) {
	b, err := persistence.New("memory", nil)
	if err != nil {
		t.Fatalf("persistence.New(memory): %v", err)
	}
	if err := b.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
	b.Close() //nolint:errcheck
}

func TestPersistenceRegistry_NewUnknown_ReturnsError(t *testing.T) {
	_, err := persistence.New("__not_registered__", nil)
	if err == nil {
		t.Error("persistence.New(unknown): want error, got nil")
	}
}

// --- target ---

func TestTargetRegistry_HasHTTP(t *testing.T) {
	if !target.Has("http") {
		t.Error("target.Has(http): want true")
	}
}

func TestTargetRegistry_HasMissing(t *testing.T) {
	if target.Has("__not_registered__") {
		t.Error("target.Has(unregistered): want false")
	}
}

func TestTargetRegistry_NewHTTP(t *testing.T) {
	tc, err := target.New("http", map[string]any{"targetURL": "http://localhost:9999"})
	if err != nil {
		t.Fatalf("target.New(http): %v", err)
	}
	tc.Close() //nolint:errcheck
}

func TestTargetRegistry_NewUnknown_ReturnsError(t *testing.T) {
	_, err := target.New("__not_registered__", nil)
	if err == nil {
		t.Error("target.New(unknown): want error, got nil")
	}
}

// --- transport ---

func TestTransportRegistry_HasMissing(t *testing.T) {
	if transport.Has("__not_registered__") {
		t.Error("transport.Has(unregistered): want false")
	}
}

func TestTransportRegistry_NewUnknown_ReturnsError(t *testing.T) {
	_, err := transport.New(nil, "__not_registered__", nil)
	if err == nil {
		t.Error("transport.New(unknown): want error, got nil")
	}
}

func TestTransportRegistry_RegisterAndHas(t *testing.T) {
	name := "__test_transport_registry_reg__"
	transport.Register(name, func(_ transport.TransportHost, _ map[string]any) (transport.Transport, error) {
		return &stubTransport{}, nil
	})
	if !transport.Has(name) {
		t.Errorf("transport.Has(%q): want true after Register", name)
	}
	tr, err := transport.New(nil, name, nil)
	if err != nil {
		t.Fatalf("transport.New: %v", err)
	}
	tr.Close() //nolint:errcheck
}

type stubTransport struct{}

func (s *stubTransport) Broadcast(_ context.Context, _ string, _ []byte) ([]transport.Ack, error) {
	return nil, nil
}
func (s *stubTransport) Get(_ context.Context, _, _, _ string) ([]byte, error) { return nil, nil }
func (s *stubTransport) Close() error                                           { return nil }

// --- discovery ---

func TestDiscoveryRegistry_HasMissing(t *testing.T) {
	if discovery.Has("__not_registered__") {
		t.Error("discovery.Has(unregistered): want false")
	}
}

func TestDiscoveryRegistry_NewUnknown_ReturnsError(t *testing.T) {
	store, _ := persistence.New("memory", nil)
	defer store.Close() //nolint:errcheck
	_, err := discovery.New(store, "__not_registered__", nil)
	if err == nil {
		t.Error("discovery.New(unknown): want error, got nil")
	}
}

// --- observability ---

func TestObservabilityRegistry_HasMissing(t *testing.T) {
	if observability.Has("__not_registered__") {
		t.Error("observability.Has(unregistered): want false")
	}
}

func TestObservabilityRegistry_NewUnknown_ReturnsError(t *testing.T) {
	_, err := observability.New("__not_registered__", nil)
	if err == nil {
		t.Error("observability.New(unknown): want error, got nil")
	}
}

func TestObservabilityRegistry_RegisterAndHas(t *testing.T) {
	name := "__test_obs_registry_reg__"
	observability.Register(name, func(_ map[string]any) (observability.Observer, error) {
		return &stubObserver{}, nil
	})
	if !observability.Has(name) {
		t.Errorf("observability.Has(%q): want true after Register", name)
	}
	obs, err := observability.New(name, nil)
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	obs.Close() //nolint:errcheck
}

type stubObserver struct{}

func (o *stubObserver) Record(_ context.Context, _ observability.Event) error { return nil }
func (o *stubObserver) Close() error                                           { return nil }
