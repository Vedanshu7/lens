package unit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Vedanshu7/lens/internal/agent"
	"github.com/Vedanshu7/lens/internal/persistence"
	_ "github.com/Vedanshu7/lens/internal/persistence/memory"
	"github.com/Vedanshu7/lens/internal/target"
	"github.com/Vedanshu7/lens/test/testutil"
)

func newTestAgent(t *testing.T, cfg agent.Config) *agent.Agent {
	t.Helper()
	store, err := persistence.New("memory", nil)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	tc := &testutil.StubTargetClient{}
	tr := &testutil.StubTransport{}
	disc := &testutil.StubResolver{}
	info := target.TargetInfo{Service: "test-svc", Instance: "test-inst"}
	a := agent.NewFromDeps(cfg, store, tc, tr, disc, info)
	t.Cleanup(func() { a.Shutdown(context.Background()) })
	return a
}

func defaultCfg() agent.Config {
	return agent.Config{CooldownMS: 50}
}

// post sends a JSON POST to the agent handler and returns the recorder.
func agentPost(t *testing.T, a *agent.Agent, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	return w
}

func agentGet(t *testing.T, a *agent.Agent, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	return w
}

// --- Health ---

func TestHandleHealth_ReturnsOK(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/health")
	if w.Code != http.StatusOK {
		t.Errorf("health: want 200, got %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	if body["redis"] != true {
		t.Errorf("health.redis: want true, got %v", body["redis"])
	}
}

// --- Authentication ---

func TestAuthenticate_NoTokenConfigured_AllowsAll(t *testing.T) {
	a := newTestAgent(t, agent.Config{CooldownMS: 50})
	req := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Error("no token configured: request should not be rejected as unauthorized")
	}
}

func TestAuthenticate_WrongToken_Returns401(t *testing.T) {
	cfg := defaultCfg()
	cfg.Token = "secret"
	a := newTestAgent(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	req.Header.Set("x-lens-token", "wrong")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: want 401, got %d", w.Code)
	}
}

func TestAuthenticate_CorrectToken_Passes(t *testing.T) {
	cfg := defaultCfg()
	cfg.Token = "secret"
	a := newTestAgent(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	req.Header.Set("x-lens-token", "secret")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("correct token: should not be 401, got %d", w.Code)
	}
}

// --- Invalidate ---

func TestHandleInvalidate_ReturnsOK(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentPost(t, a, "/api/invalidate", map[string]any{
		"service": "test-svc",
		"pattern": nil,
	})
	if w.Code != http.StatusOK {
		t.Errorf("invalidate: want 200, got %d\nbody: %s", w.Code, w.Body.String())
	}
}

func TestHandleInvalidate_InvalidService_Returns400(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentPost(t, a, "/api/invalidate", map[string]any{
		"service": "bad service!",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid service: want 400, got %d", w.Code)
	}
}

func TestHandleInvalidate_Throttled_Returns429(t *testing.T) {
	cfg := defaultCfg()
	cfg.CooldownMS = 60000 // 60s — definitely throttled on second call
	a := newTestAgent(t, cfg)

	agentPost(t, a, "/api/invalidate", map[string]any{"service": "test-svc"})
	w := agentPost(t, a, "/api/invalidate", map[string]any{"service": "test-svc"})
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("throttled: want 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("throttled: Retry-After header missing")
	}
}

// --- Declare ---

func TestHandleDeclare_ReturnsOK(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentPost(t, a, "/api/declare", map[string]any{
		"keyName":      "user:123",
		"ttlInSeconds": 3600,
	})
	if w.Code != http.StatusOK {
		t.Errorf("declare: want 200, got %d", w.Code)
	}
}

// --- Nodes ---

func TestHandleNodes_ReturnsSelf(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/nodes?service=test-svc")
	if w.Code != http.StatusOK {
		t.Fatalf("nodes: want 200, got %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	instances, _ := body["instances"].([]any)
	if len(instances) != 1 {
		t.Errorf("nodes: want 1 instance, got %d", len(instances))
	}
}

func TestHandleNodes_InvalidService_Returns400(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/nodes?service=bad.name")
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid service: want 400, got %d", w.Code)
	}
}

// --- Fetch ---

func TestHandleFetch_LocalInstance_CallsTarget(t *testing.T) {
	store, _ := persistence.New("memory", nil)
	called := false
	tc := &testutil.StubTargetClient{
		GetFn: func(_ context.Context, key string) ([]byte, error) {
			called = true
			return []byte(fmt.Sprintf(`{"found":true,"value":%q}`, key)), nil
		},
	}
	a := agent.NewFromDeps(defaultCfg(), store, tc, &testutil.StubTransport{}, &testutil.StubResolver{},
		target.TargetInfo{Service: "test-svc", Instance: "test-inst"})
	defer a.Shutdown(context.Background())

	w := agentPost(t, a, "/api/fetch", map[string]any{
		"service":  "test-svc",
		"instance": "test-inst",
		"key":      "my-key",
	})
	if w.Code != http.StatusOK {
		t.Errorf("fetch: want 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !called {
		t.Error("fetch: target.Get was not called")
	}
}

// --- Audit ---

func TestHandleAudit_EmptyLog(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/audit")
	if w.Code != http.StatusOK {
		t.Fatalf("audit: want 200, got %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	if body["count"] != float64(0) {
		t.Errorf("audit empty: want count=0, got %v", body["count"])
	}
}

func TestHandleAudit_PopulatedAfterInvalidate(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	agentPost(t, a, "/api/invalidate", map[string]any{"service": "test-svc"})

	w := agentGet(t, a, "/api/audit")
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	if body["count"] == float64(0) {
		t.Error("audit: expected at least 1 entry after invalidate")
	}
}

// --- RequireReady gate ---

func TestRequireReady_NotLive_Returns503(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	a.SetReady(false)

	for _, path := range []string{"/api/invalidate", "/api/nodes?service=test-svc", "/api/audit"} {
		var w *httptest.ResponseRecorder
		if path == "/api/invalidate" {
			w = agentPost(t, a, path, map[string]any{"service": "test-svc"})
		} else {
			w = agentGet(t, a, path)
		}
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s while not live: want 503, got %d", path, w.Code)
		}
	}
}

func TestRequireReady_HealthAndDeclare_SkipGate(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	a.SetReady(false)

	// /api/health and /api/declare are NOT gated by requireReady
	if w := agentGet(t, a, "/api/health"); w.Code != http.StatusOK {
		t.Errorf("health while not live: want 200, got %d", w.Code)
	}
	// declare returns 202 when not ready (accepted but deferred)
	w := agentPost(t, a, "/api/declare", map[string]any{"keyName": "k", "ttlInSeconds": 60})
	if w.Code != http.StatusAccepted {
		t.Errorf("declare while not live: want 202, got %d", w.Code)
	}
}

// --- handleFetch additional paths ---

func TestHandleFetch_ProxyLoop_Returns508(t *testing.T) {
	a := newTestAgent(t, defaultCfg())

	b, _ := json.Marshal(map[string]any{
		"service":  "other-svc",
		"instance": "other-inst",
		"key":      "k",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fetch", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lens-Proxied", "1")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusLoopDetected {
		t.Errorf("proxy loop: want 508, got %d", w.Code)
	}
}

func TestHandleFetch_RemoteInstance_UsesTransport(t *testing.T) {
	store, _ := persistence.New("memory", nil)
	called := false
	tr := &testutil.StubTransport{
		GetFn: func(_ context.Context, svc, instance, key string) ([]byte, error) {
			called = true
			return []byte(`{"found":true,"value":"remote"}`), nil
		},
	}
	a := agent.NewFromDeps(defaultCfg(), store, &testutil.StubTargetClient{}, tr, &testutil.StubResolver{},
		target.TargetInfo{Service: "test-svc", Instance: "self"})
	defer a.Shutdown(context.Background())

	w := agentPost(t, a, "/api/fetch", map[string]any{
		"service":  "test-svc",
		"instance": "other-inst",
		"key":      "my-key",
	})
	if w.Code != http.StatusOK {
		t.Errorf("remote fetch: want 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !called {
		t.Error("remote fetch: transport.Get was not called")
	}
}

// --- handleServices ---

func TestHandleServices_IncludesSelf(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/services")
	if w.Code != http.StatusOK {
		t.Fatalf("services: want 200, got %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	services, _ := body["services"].([]any)
	if len(services) == 0 {
		t.Error("services: expected at least own service in list")
	}
}

// --- handleKeys ---

func TestHandleKeys_MissingService_Returns400(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/keys?service=bad.name")
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid service: want 400, got %d", w.Code)
	}
}

func TestHandleKeys_ValidService_Returns200(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/keys?service=test-svc&instance=test-inst")
	// target stub returns `[]` for Keys, so should succeed
	if w.Code != http.StatusOK {
		t.Errorf("keys: want 200, got %d\n%s", w.Code, w.Body.String())
	}
}
