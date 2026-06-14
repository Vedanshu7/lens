package observability_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vedanshu7/lens/internal/observability"
)

type recordingObserver struct {
	events []observability.Event
	closed atomic.Bool
}

func (r *recordingObserver) Record(_ context.Context, e observability.Event) error {
	r.events = append(r.events, e)
	return nil
}
func (r *recordingObserver) Close() error { r.closed.Store(true); return nil }

func TestMultiObserver_NoObservers_RecordIsNoOp(t *testing.T) {
	m := observability.NewMultiObserver(nil)
	if err := m.Record(context.Background(), observability.Event{Kind: observability.EventInvalidate}); err != nil {
		t.Errorf("Record with no observers: want nil error, got %v", err)
	}
	m.Close() //nolint:errcheck
}

func TestMultiObserver_WithObserver_DeliversEvent(t *testing.T) {
	obs := &recordingObserver{}
	m := observability.NewMultiObserver([]observability.Observer{obs})

	m.Record(context.Background(), observability.Event{Kind: observability.EventInvalidate, Service: "svc-a"}) //nolint:errcheck
	m.Close()                                                                                                  //nolint:errcheck

	if len(obs.events) != 1 {
		t.Fatalf("want 1 event delivered, got %d", len(obs.events))
	}
	if obs.events[0].Service != "svc-a" {
		t.Errorf("event.Service: want svc-a, got %q", obs.events[0].Service)
	}
}

func TestMultiObserver_Record_SetsTimestampIfZero(t *testing.T) {
	obs := &recordingObserver{}
	m := observability.NewMultiObserver([]observability.Observer{obs})
	before := time.Now().UTC()

	m.Record(context.Background(), observability.Event{Kind: observability.EventFetch}) //nolint:errcheck
	m.Close()                                                                           //nolint:errcheck

	if len(obs.events) == 0 {
		t.Fatal("no events delivered")
	}
	if obs.events[0].Timestamp.IsZero() {
		t.Error("Record: want non-zero Timestamp when event.Timestamp is zero")
	}
	if obs.events[0].Timestamp.Before(before) {
		t.Error("Record: Timestamp is before test start")
	}
}

func TestMultiObserver_Close_CallsObserverClose(t *testing.T) {
	obs := &recordingObserver{}
	m := observability.NewMultiObserver([]observability.Observer{obs})
	m.Close() //nolint:errcheck

	if !obs.closed.Load() {
		t.Error("Close: observer.Close() was not called")
	}
}

func TestMultiObserver_MultipleObservers_AllReceiveEvent(t *testing.T) {
	obs1 := &recordingObserver{}
	obs2 := &recordingObserver{}
	m := observability.NewMultiObserver([]observability.Observer{obs1, obs2})

	m.Record(context.Background(), observability.Event{Kind: observability.EventPeerJoin, Service: "x"}) //nolint:errcheck
	m.Close()                                                                                            //nolint:errcheck

	if len(obs1.events) != 1 {
		t.Errorf("obs1: want 1 event, got %d", len(obs1.events))
	}
	if len(obs2.events) != 1 {
		t.Errorf("obs2: want 1 event, got %d", len(obs2.events))
	}
}

func TestMultiObserver_SQLObserver_None(t *testing.T) {
	m := observability.NewMultiObserver([]observability.Observer{&recordingObserver{}})
	defer m.Close() //nolint:errcheck

	_, ok := m.SQLObserver()
	if ok {
		t.Error("SQLObserver: want false when no SQL observer configured")
	}
}

type testSQLObserver struct{ recordingObserver }

func (s *testSQLObserver) QueryLatency(_ context.Context, _, _ string) ([]observability.LatencyBucket, error) {
	return nil, nil
}
func (s *testSQLObserver) QueryDeadPods(_ context.Context, _, _ string) ([]observability.DeadPodEvent, error) {
	return nil, nil
}
func (s *testSQLObserver) QueryDiscovery(_ context.Context, _ string) ([]observability.DiscoveryEvent, error) {
	return nil, nil
}
func (s *testSQLObserver) QueryFlow(_ context.Context, _, _ string) (*observability.FlowStats, error) {
	return nil, nil
}
func (s *testSQLObserver) QuerySummary(_ context.Context, _, _ string) (*observability.SummaryStats, error) {
	return nil, nil
}

func TestMultiObserver_SQLObserver_Present(t *testing.T) {
	sqlObs := &testSQLObserver{}
	m := observability.NewMultiObserver([]observability.Observer{sqlObs})
	defer m.Close() //nolint:errcheck

	got, ok := m.SQLObserver()
	if !ok {
		t.Fatal("SQLObserver: want true when SQL observer is configured")
	}
	if got != sqlObs {
		t.Error("SQLObserver: did not return the configured SQL observer")
	}
}
