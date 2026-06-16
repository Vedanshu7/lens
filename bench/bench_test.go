// Package bench_test provides micro-benchmarks for the Lens paper evaluation.
//
// These benchmarks measure internal agent overhead using in-process stubs and
// require no external infrastructure (no Redis, no NATS, no Docker).
//
// Run all benchmarks:
//
//	go test -bench=. -benchmem -benchtime=5s -count=3 ./bench/
//
// Run a single benchmark family:
//
//	go test -bench=BenchmarkInvalidateRoute -benchmem -benchtime=5s -count=3 ./bench/
//
// For end-to-end numbers with real transports and real pods, use:
//
//	cd example && docker compose -f docker-compose.nats-standalone.yml up --build -d
//	bash example/load_test.sh
package bench_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Vedanshu7/lens/internal/agent"
	"github.com/Vedanshu7/lens/internal/persistence"
	_ "github.com/Vedanshu7/lens/internal/persistence/memory"
	"github.com/Vedanshu7/lens/internal/target"
	"github.com/Vedanshu7/lens/internal/transport"
	"github.com/Vedanshu7/lens/test/testutil"
)

func TestMain(m *testing.M) {
	// Silence the structured logger so benchmark output is readable.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	})))
	os.Exit(m.Run())
}

// newBenchAgent builds a minimal agent using in-process stubs.
// CooldownMS=0 disables per-service throttling so benchmarks measure handler
// overhead, not rate-limit wait time.
func newBenchAgent(b *testing.B, tr transport.Transport, tc target.TargetClient) *agent.Agent {
	b.Helper()
	st, err := persistence.New("memory", nil)
	if err != nil {
		b.Fatalf("persistence: %v", err)
	}
	a := agent.NewFromDeps(
		agent.Config{CooldownMS: 0},
		st, tc, tr,
		&testutil.StubResolver{},
		target.TargetInfo{Service: "bench-svc", Instance: "bench-inst"},
	)
	b.Cleanup(func() { a.Shutdown(context.Background()) })
	return a
}

// stubAcks returns a slice of N successful acks, simulating N healthy peers.
func stubAcks(n int) []transport.Ack {
	acks := make([]transport.Ack, n)
	for i := range acks {
		acks[i] = transport.Ack{Instance: fmt.Sprintf("peer-%d", i), Success: true}
	}
	return acks
}

// invalidateBody is a pre-encoded POST /api/invalidate body.
var invalidateBody = func() []byte {
	b, _ := json.Marshal(map[string]any{"service": "bench-svc", "pattern": "user:"})
	return b
}()

// BenchmarkInvalidateRoute measures the full /api/invalidate handler round-trip:
// JSON decode → throttle check → transport.Broadcast → audit-log write → JSON encode.
//
// Sub-benchmarks vary the number of simulated peer acknowledgements to show how
// fan-out count affects handler latency. The transport is a stub that returns N
// acks synchronously, so network RTT is excluded. Compare these numbers with
// end-to-end load_test.sh results to quantify what the sidecar itself contributes.
func BenchmarkInvalidateRoute(b *testing.B) {
	for _, n := range []int{0, 1, 5, 10, 20} {
		n := n
		b.Run(fmt.Sprintf("peers=%d", n), func(b *testing.B) {
			acks := stubAcks(n)
			tr := &testutil.StubTransport{
				BroadcastFn: func(_ context.Context, _ string, _ []byte) ([]transport.Ack, error) {
					return acks, nil
				},
			}
			a := newBenchAgent(b, tr, &testutil.StubTargetClient{})
			handler := a.Routes()

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					req := httptest.NewRequest(http.MethodPost, "/api/invalidate",
						bytes.NewReader(invalidateBody))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, req)
					if w.Code != http.StatusOK {
						b.Errorf("want 200, got %d: %s", w.Code, w.Body.String())
					}
				}
			})
		})
	}
}

// BenchmarkApplyInvalidation measures the per-pod receive path: the Lens retry
// wrapper calling the target service's /internal/lens/invalidate endpoint.
// This is the overhead Lens adds on the receiving side — every pod that gets a
// broadcast message pays this cost.
func BenchmarkApplyInvalidation(b *testing.B) {
	var calls atomic.Int64
	tc := &testutil.StubTargetClient{
		InvalidateFn: func(_ context.Context, _ []byte) error {
			calls.Add(1)
			return nil
		},
	}
	a := newBenchAgent(b, &testutil.StubTransport{}, tc)
	payload := []byte(`{"pattern":"user:"}`)
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		a.ApplyInvalidation(ctx, payload, "bench-peer")
	}
	b.ReportMetric(float64(calls.Load()), "target_calls")
}

// BenchmarkWriteInvalidationLog measures the replay-log write path: the cost
// of recording an event to the persistence layer so that pods restarting later
// can replay missed invalidations. This is called once per invalidation event
// in the transport layer.
func BenchmarkWriteInvalidationLog(b *testing.B) {
	a := newBenchAgent(b, &testutil.StubTransport{}, &testutil.StubTargetClient{})
	payload := []byte(`{"pattern":"user:"}`)
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		a.WriteInvalidationLog(ctx, "bench-svc", payload)
	}
}

// BenchmarkHealthRoute measures the /api/health handler: one store.Ping + one
// agent.ready() check. This is the baseline cost of Lens's simplest endpoint
// and represents the lower bound on HTTP handler overhead.
func BenchmarkHealthRoute(b *testing.B) {
	a := newBenchAgent(b, &testutil.StubTransport{}, &testutil.StubTargetClient{})
	handler := a.Routes()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				b.Errorf("want 200, got %d", w.Code)
			}
		}
	})
}

// BenchmarkInvalidateRoute_RealTarget measures the /api/invalidate handler when
// the target endpoint is a real in-process HTTP server (not a stub). This
// captures the combined overhead of Lens processing plus an actual HTTP call to
// the co-located application, giving a realistic lower-bound end-to-end number
// for same-host communication (no network RTT, just loopback).
func BenchmarkInvalidateRoute_RealTarget(b *testing.B) {
	var targetCalls atomic.Int64
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	b.Cleanup(app.Close)

	appClient := app.Client()
	tc := &testutil.StubTargetClient{
		InvalidateFn: func(ctx context.Context, payload []byte) error {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
				app.URL+"/internal/lens/invalidate", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			resp, err := appClient.Do(req)
			if err != nil {
				return err
			}
			resp.Body.Close()
			return nil
		},
	}

	// 3 peers return acks synchronously; the target call is what this bench measures.
	acks := stubAcks(3)
	tr := &testutil.StubTransport{
		BroadcastFn: func(_ context.Context, _ string, _ []byte) ([]transport.Ack, error) {
			return acks, nil
		},
	}

	a := newBenchAgent(b, tr, tc)
	handler := a.Routes()

	b.ResetTimer()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/api/invalidate",
			bytes.NewReader(invalidateBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			b.Errorf("want 200, got %d: %s", w.Code, w.Body.String())
		}
	}
	b.ReportMetric(float64(targetCalls.Load()), "app_calls")
}

// BenchmarkConcurrentInvalidations measures sustained invalidation throughput
// under realistic concurrent load: GOMAXPROCS goroutines each firing invalidations
// at 5 simulated peers. This is the primary throughput figure for the paper —
// it shows how many cluster-wide cache invalidations Lens can process per second
// on a single node before it becomes the bottleneck.
func BenchmarkConcurrentInvalidations(b *testing.B) {
	acks := stubAcks(5)
	tr := &testutil.StubTransport{
		BroadcastFn: func(_ context.Context, _ string, _ []byte) ([]transport.Ack, error) {
			return acks, nil
		},
	}
	a := newBenchAgent(b, tr, &testutil.StubTargetClient{})
	handler := a.Routes()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodPost, "/api/invalidate",
				bytes.NewReader(invalidateBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
		}
	})
}

// BenchmarkReplayLog_Seed measures the throughput of writing to the invalidation
// replay log — the path executed by every transport provider when an invalidation
// event is received. This determines how quickly Lens can process a burst of
// incoming events before persistence becomes the bottleneck.
func BenchmarkReplayLog_Seed(b *testing.B) {
	var wg sync.WaitGroup
	a := newBenchAgent(b, &testutil.StubTransport{}, &testutil.StubTargetClient{})
	ctx := context.Background()

	payloads := [][]byte{
		[]byte(`{"pattern":"user:"}`),
		[]byte(`{"pattern":"session:"}`),
		[]byte(`{"pattern":"product:"}`),
		[]byte(`{"pattern":null}`),
	}

	b.ResetTimer()
	for i := range b.N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.WriteInvalidationLog(ctx, "bench-svc", payloads[i%len(payloads)])
		}()
	}
	wg.Wait()
}
