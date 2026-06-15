// Package testutil provides stub implementations of the Lens layer interfaces
// for use in tests. All stubs are safe for concurrent use and default to no-op
// behaviour unless a function field is set.
package testutil

import (
	"context"

	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/observability"
	"github.com/Vedanshu7/lens/internal/target"
	"github.com/Vedanshu7/lens/internal/transport"
)

// StubTransport is a no-op transport. Set BroadcastFn or GetFn to control behaviour.
type StubTransport struct {
	BroadcastFn func(ctx context.Context, svc string, payload []byte) ([]transport.Ack, error)
	GetFn       func(ctx context.Context, svc, instance, key string) ([]byte, error)
}

func (s *StubTransport) Broadcast(ctx context.Context, svc string, payload []byte) ([]transport.Ack, error) {
	if s.BroadcastFn != nil {
		return s.BroadcastFn(ctx, svc, payload)
	}
	return nil, nil
}

func (s *StubTransport) Get(ctx context.Context, svc, instance, key string) ([]byte, error) {
	if s.GetFn != nil {
		return s.GetFn(ctx, svc, instance, key)
	}
	return nil, nil
}

func (s *StubTransport) Close() error { return nil }

// StubResolver is a no-op discovery resolver. Set PeersFn or WatchFn to control behaviour.
type StubResolver struct {
	PeersFn func(ctx context.Context, service string) ([]discovery.ServiceInstance, error)
	WatchFn func(ctx context.Context) (<-chan discovery.Event, error)
}

func (s *StubResolver) Register(_ context.Context, _ discovery.ServiceInstance) error { return nil }
func (s *StubResolver) Deregister(_ context.Context, _ discovery.ServiceInstance) error {
	return nil
}

func (s *StubResolver) Peers(ctx context.Context, service string) ([]discovery.ServiceInstance, error) {
	if s.PeersFn != nil {
		return s.PeersFn(ctx, service)
	}
	return nil, nil
}

func (s *StubResolver) Watch(ctx context.Context) (<-chan discovery.Event, error) {
	if s.WatchFn != nil {
		return s.WatchFn(ctx)
	}
	ch := make(chan discovery.Event)
	close(ch)
	return ch, nil
}

func (s *StubResolver) Close() error { return nil }

// StubTargetClient is a no-op target client. Set function fields to control behaviour.
type StubTargetClient struct {
	InfoFn       func(ctx context.Context) (target.TargetInfo, error)
	InvalidateFn func(ctx context.Context, payload []byte) error
	GetFn        func(ctx context.Context, key string) ([]byte, error)
	KeysFn       func(ctx context.Context, pattern, limit, offset string) ([]byte, error)
}

func (s *StubTargetClient) Info(ctx context.Context) (target.TargetInfo, error) {
	if s.InfoFn != nil {
		return s.InfoFn(ctx)
	}
	return target.TargetInfo{Service: "test-svc", Instance: "test-inst"}, nil
}

func (s *StubTargetClient) Invalidate(ctx context.Context, payload []byte) error {
	if s.InvalidateFn != nil {
		return s.InvalidateFn(ctx, payload)
	}
	return nil
}

func (s *StubTargetClient) Get(ctx context.Context, key string) ([]byte, error) {
	if s.GetFn != nil {
		return s.GetFn(ctx, key)
	}
	return []byte(`{"found":false}`), nil
}

func (s *StubTargetClient) Keys(ctx context.Context, pattern, limit, offset string) ([]byte, error) {
	if s.KeysFn != nil {
		return s.KeysFn(ctx, pattern, limit, offset)
	}
	return []byte(`[]`), nil
}

func (s *StubTargetClient) Close() error { return nil }

// StubHost is a no-op transport.TransportHost for testing transport providers directly.
type StubHost struct{}

func (StubHost) PeersForService(_ string) []transport.PeerAddr              { return nil }
func (StubHost) ApplyInvalidation(_ context.Context, _ []byte, _ string)    {}
func (StubHost) WriteInvalidationLog(_ context.Context, _ string, _ []byte) {}
func (StubHost) GetFromTarget(_ context.Context, _ []byte) ([]byte, error) {
	return []byte(`{}`), nil
}
func (StubHost) SelfInstance() string { return "stub-inst" }
func (StubHost) SelfService() string  { return "stub-svc" }

// StubObserver is a no-op observer. Set RecordFn to inspect recorded events.
type StubObserver struct {
	RecordFn func(ctx context.Context, event observability.Event) error
}

func (s *StubObserver) Record(ctx context.Context, event observability.Event) error {
	if s.RecordFn != nil {
		return s.RecordFn(ctx, event)
	}
	return nil
}

func (s *StubObserver) Close() error { return nil }
