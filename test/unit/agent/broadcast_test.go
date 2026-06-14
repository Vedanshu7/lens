package agent_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vedanshu7/lens/internal/agent"
	"github.com/Vedanshu7/lens/internal/persistence"
	_ "github.com/Vedanshu7/lens/internal/persistence/memory"
	"github.com/Vedanshu7/lens/internal/target"
	"github.com/Vedanshu7/lens/test/testutil"
)

func newBroadcastAgent(t *testing.T, tc *testutil.StubTargetClient) *agent.Agent {
	t.Helper()
	store, err := persistence.New("memory", nil)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	a := agent.NewFromDeps(
		agent.Config{CooldownMS: 0},
		store, tc,
		&testutil.StubTransport{},
		&testutil.StubResolver{},
		target.TargetInfo{Service: "svc", Instance: "inst"},
	)
	t.Cleanup(func() { a.Shutdown(context.Background()) })
	return a
}

func TestApplyInvalidation_SucceedsFirstAttempt(t *testing.T) {
	var calls atomic.Int32
	tc := &testutil.StubTargetClient{
		InvalidateFn: func(_ context.Context, _ []byte) error {
			calls.Add(1)
			return nil
		},
	}
	a := newBroadcastAgent(t, tc)
	a.ApplyInvalidation(context.Background(), []byte(`{}`), "test")
	if calls.Load() != 1 {
		t.Errorf("Invalidate call count: want 1, got %d", calls.Load())
	}
}

func TestApplyInvalidation_RetriesOnTransientFailure(t *testing.T) {
	var calls atomic.Int32
	tc := &testutil.StubTargetClient{
		InvalidateFn: func(_ context.Context, _ []byte) error {
			if calls.Add(1) < 3 {
				return errors.New("transient error")
			}
			return nil
		},
	}
	a := newBroadcastAgent(t, tc)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.ApplyInvalidation(ctx, []byte(`{}`), "test")

	if calls.Load() != 3 {
		t.Errorf("want 3 attempts (2 failures + 1 success), got %d", calls.Load())
	}
}

func TestApplyInvalidation_AbandonedAfterMaxRetries(t *testing.T) {
	var calls atomic.Int32
	tc := &testutil.StubTargetClient{
		InvalidateFn: func(_ context.Context, _ []byte) error {
			calls.Add(1)
			return errors.New("permanent error")
		},
	}
	a := newBroadcastAgent(t, tc)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a.ApplyInvalidation(ctx, []byte(`{}`), "test")

	if calls.Load() != 3 {
		t.Errorf("want exactly 3 attempts (maxRetries), got %d", calls.Load())
	}
}

func TestApplyInvalidation_ContextCancellation_StopsRetries(t *testing.T) {
	var calls atomic.Int32
	tc := &testutil.StubTargetClient{
		InvalidateFn: func(_ context.Context, _ []byte) error {
			calls.Add(1)
			return errors.New("always fails")
		},
	}
	a := newBroadcastAgent(t, tc)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.ApplyInvalidation(ctx, []byte(`{}`), "test")
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ApplyInvalidation did not respect context cancellation within 3s")
	}
	if calls.Load() > 2 {
		t.Errorf("expected ≤2 attempts after cancellation, got %d", calls.Load())
	}
}
