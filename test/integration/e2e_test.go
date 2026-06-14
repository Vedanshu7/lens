// Package integration contains end-to-end tests that wire real providers
// together against in-process servers. No external services (Redis, NATS, etc.)
// are required — all infrastructure runs within the test process.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vedanshu7/lens/internal/agent"
	"github.com/Vedanshu7/lens/internal/persistence"
	_ "github.com/Vedanshu7/lens/internal/persistence/memory"
	"github.com/Vedanshu7/lens/internal/target"
	_ "github.com/Vedanshu7/lens/internal/target/http"
	"github.com/Vedanshu7/lens/test/testutil"
)

// fakeApp builds an httptest.Server that simulates a real application
// responding to Lens sidecar callbacks. It records every call it receives.
type fakeApp struct {
	server          *httptest.Server
	invalidateCalls atomic.Int32
	getCalls        atomic.Int32
	service         string
	instance        string
}

func newFakeApp(t *testing.T, service, instance string) *fakeApp {
	t.Helper()
	app := &fakeApp{service: service, instance: instance}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /internal/lens/info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(target.TargetInfo{Service: service, Instance: instance}) //nolint:errcheck
	})
	mux.HandleFunc("POST /internal/lens/invalidate", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		app.invalidateCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /internal/lens/get", func(w http.ResponseWriter, _ *http.Request) {
		app.getCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"found": true, "value": "test-value"}) //nolint:errcheck
	})
	mux.HandleFunc("GET /internal/lens/keys", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"key":"user:123"}]`)) //nolint:errcheck
	})

	app.server = httptest.NewServer(mux)
	t.Cleanup(app.server.Close)
	return app
}

// newAgentWithHTTPTarget creates a real agent backed by an in-process HTTP target.
// It does NOT call Dial — use Dial() explicitly in tests that need the full connect path.
func newAgentWithHTTPTarget(t *testing.T, app *fakeApp) *agent.Agent {
	t.Helper()
	store, err := persistence.New("memory", nil)
	if err != nil {
		t.Fatalf("persistence.New: %v", err)
	}
	tc, err := target.New("http", map[string]any{"targetURL": app.server.URL})
	if err != nil {
		t.Fatalf("target.New: %v", err)
	}
	cfg := agent.Config{CooldownMS: 0}
	a := agent.NewFromDeps(
		cfg, store, tc,
		&testutil.StubTransport{},
		&testutil.StubResolver{},
		target.TargetInfo{Service: app.service, Instance: app.instance},
	)
	t.Cleanup(func() { a.Shutdown(context.Background()) })
	return a
}

// --- Invalidation reaching the real target ---

func TestE2E_Invalidate_ReachesTarget(t *testing.T) {
	app := newFakeApp(t, "e2e-svc", "e2e-inst-1")
	a := newAgentWithHTTPTarget(t, app)

	body, _ := json.Marshal(map[string]any{"service": "e2e-svc"})
	req := httptest.NewRequest(http.MethodPost, "/api/invalidate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("invalidate: want 200, got %d\n%s", w.Code, w.Body.String())
	}
	if app.invalidateCalls.Load() != 1 {
		t.Errorf("invalidate: want 1 call to target, got %d", app.invalidateCalls.Load())
	}
}

// --- Fetch hitting the real target ---

func TestE2E_Fetch_LocalInstance_ReachesTarget(t *testing.T) {
	app := newFakeApp(t, "e2e-svc", "e2e-inst-1")
	a := newAgentWithHTTPTarget(t, app)

	body, _ := json.Marshal(map[string]any{
		"service":  "e2e-svc",
		"instance": "e2e-inst-1",
		"key":      "user:1",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fetch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("fetch: want 200, got %d\n%s", w.Code, w.Body.String())
	}
	if app.getCalls.Load() != 1 {
		t.Errorf("fetch: want 1 call to target.Get, got %d", app.getCalls.Load())
	}
}

// --- Dial connects and marks agent live ---

func TestE2E_Dial_ConnectsToTarget(t *testing.T) {
	app := newFakeApp(t, "dial-svc", "dial-inst-1")

	store, err := persistence.New("memory", nil)
	if err != nil {
		t.Fatalf("persistence.New: %v", err)
	}
	tc, err := target.New("http", map[string]any{"targetURL": app.server.URL})
	if err != nil {
		t.Fatalf("target.New: %v", err)
	}

	// Use NewFromDeps but mark NOT live — Dial() should bring it live.
	a := agent.NewFromDeps(
		agent.Config{CooldownMS: 0, ReplayEnabled: false},
		store, tc,
		&testutil.StubTransport{},
		&testutil.StubResolver{},
		target.TargetInfo{}, // empty; Dial will fill from Info endpoint
	)
	a.SetReady(false)
	t.Cleanup(func() { a.Shutdown(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.Dial(ctx); err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// After Dial the agent should be live and Info should be populated.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	a.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("health after Dial: want 200, got %d", w.Code)
	}

	var health map[string]any
	json.Unmarshal(w.Body.Bytes(), &health) //nolint:errcheck
	if health["target"] != true {
		t.Errorf("health.target after Dial: want true, got %v", health["target"])
	}
}

// --- ReplayMissed via Dial with checkpoint ---

func TestE2E_Dial_ReplaysMissedInvalidations(t *testing.T) {
	app := newFakeApp(t, "replay-svc", "replay-inst-1")

	store, err := persistence.New("memory", nil)
	if err != nil {
		t.Fatalf("persistence.New: %v", err)
	}
	tc, err := target.New("http", map[string]any{"targetURL": app.server.URL})
	if err != nil {
		t.Fatalf("target.New: %v", err)
	}

	// Step 1: Write a checkpoint (simulating a previous shutdown).
	ctx := context.Background()
	past := time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339)
	store.Set(ctx, "lens:checkpoint:replay-svc:replay-inst-1", past, 24*time.Hour) //nolint:errcheck

	// Step 2: Write a log entry timestamped after the checkpoint.
	entry, _ := json.Marshal(map[string]any{
		"payload": json.RawMessage(`{"pattern":null}`),
		"ts":      time.Now().UTC().Format(time.RFC3339),
		"origin":  "other-inst",
	})
	store.LPush(ctx, "lens:log:replay-svc", string(entry)) //nolint:errcheck

	// Step 3: Create agent (not live) and Dial.
	a := agent.NewFromDeps(
		agent.Config{CooldownMS: 0, ReplayEnabled: true, ReplayWindowHours: 1},
		store, tc,
		&testutil.StubTransport{},
		&testutil.StubResolver{},
		target.TargetInfo{},
	)
	a.SetReady(false)
	t.Cleanup(func() { a.Shutdown(context.Background()) })

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := a.Dial(dialCtx); err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Step 4: The replay should have called app.Invalidate once.
	if app.invalidateCalls.Load() != 1 {
		t.Errorf("replay: want 1 invalidation call, got %d", app.invalidateCalls.Load())
	}
}

// --- WriteInvalidationLog → Dial replays ---

func TestE2E_WriteLog_ThenDial_Replays(t *testing.T) {
	app := newFakeApp(t, "wlog-svc", "wlog-inst-1")

	store, err := persistence.New("memory", nil)
	if err != nil {
		t.Fatalf("persistence.New: %v", err)
	}
	tc, err := target.New("http", map[string]any{"targetURL": app.server.URL})
	if err != nil {
		t.Fatalf("target.New: %v", err)
	}

	// Write checkpoint first
	ctx := context.Background()
	past := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	store.Set(ctx, "lens:checkpoint:wlog-svc:wlog-inst-1", past, 24*time.Hour) //nolint:errcheck

	// Use a helper agent just to write the log entry
	helper := agent.NewFromDeps(
		agent.Config{},
		store,
		&testutil.StubTargetClient{},
		&testutil.StubTransport{},
		&testutil.StubResolver{},
		target.TargetInfo{Service: "wlog-svc", Instance: "helper"},
	)
	helper.WriteInvalidationLog(ctx, "wlog-svc", []byte(`{"pattern":null}`))
	helper.Shutdown(ctx)

	// Now create the real agent and Dial
	a := agent.NewFromDeps(
		agent.Config{CooldownMS: 0, ReplayEnabled: true, ReplayWindowHours: 1},
		store, tc,
		&testutil.StubTransport{},
		&testutil.StubResolver{},
		target.TargetInfo{},
	)
	a.SetReady(false)
	t.Cleanup(func() { a.Shutdown(ctx) })

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.Dial(dialCtx); err != nil {
		t.Fatalf("Dial: %v", err)
	}

	if app.invalidateCalls.Load() < 1 {
		t.Errorf("replay after WriteLog: want ≥1 invalidation call, got %d", app.invalidateCalls.Load())
	}
}

// --- Health reflects real target liveness ---

func TestE2E_Health_TargetDown_LiveFalse(t *testing.T) {
	// Start and immediately stop a server to get an unreachable URL
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	store, err := persistence.New("memory", nil)
	if err != nil {
		t.Fatalf("persistence.New: %v", err)
	}
	tc, err := target.New("http", map[string]any{"targetURL": deadURL})
	if err != nil {
		t.Fatalf("target.New: %v", err)
	}
	a := agent.NewFromDeps(
		agent.Config{CooldownMS: 0},
		store, tc,
		&testutil.StubTransport{},
		&testutil.StubResolver{},
		target.TargetInfo{Service: "dead-svc", Instance: "i1"},
	)
	a.SetReady(false) // not live — dial would fail against dead server
	t.Cleanup(func() { a.Shutdown(context.Background()) })

	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	var h map[string]any
	json.Unmarshal(w.Body.Bytes(), &h) //nolint:errcheck
	if h["target"] != false {
		t.Errorf("health.target when not live: want false, got %v", h["target"])
	}
}
