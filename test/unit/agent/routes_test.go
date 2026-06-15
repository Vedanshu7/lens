package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Vedanshu7/lens/internal/agent"
	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/persistence"
	_ "github.com/Vedanshu7/lens/internal/persistence/memory"
	"github.com/Vedanshu7/lens/internal/target"
	"github.com/Vedanshu7/lens/internal/transport"
	"github.com/Vedanshu7/lens/test/testutil"
)

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
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/services")
	if w.Code == http.StatusUnauthorized {
		t.Error("no token configured: should not return 401")
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
	w := agentPost(t, a, "/api/invalidate", map[string]any{"service": "test-svc"})
	if w.Code != http.StatusOK {
		t.Errorf("invalidate: want 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestHandleInvalidate_InvalidService_Returns400(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentPost(t, a, "/api/invalidate", map[string]any{"service": "bad service!"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid service: want 400, got %d", w.Code)
	}
}

func TestHandleInvalidate_Throttled_Returns429(t *testing.T) {
	cfg := defaultCfg()
	cfg.CooldownMS = 60000
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

func TestHandleInvalidate_BroadcastFails_Returns500(t *testing.T) {
	store, _ := persistence.New("memory", nil)
	tr := &testutil.StubTransport{
		BroadcastFn: func(_ context.Context, _ string, _ []byte) ([]transport.Ack, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	a := agent.NewFromDeps(defaultCfg(), store, &testutil.StubTargetClient{}, tr, &testutil.StubResolver{},
		target.TargetInfo{Service: "test-svc", Instance: "i1"})
	defer a.Shutdown(context.Background())

	w := agentPost(t, a, "/api/invalidate", map[string]any{"service": "test-svc"})
	if w.Code != http.StatusInternalServerError {
		t.Errorf("broadcast failure: want 500, got %d\n%s", w.Code, w.Body.String())
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

func TestHandleDeclare_InvalidJSON_Returns400(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	req := httptest.NewRequest(http.MethodPost, "/api/declare", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad json: want 400, got %d", w.Code)
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
	found := false
	for _, inst := range instances {
		m, _ := inst.(map[string]any)
		if m["instance"] == "test-inst" {
			found = true
		}
	}
	if !found {
		t.Errorf("nodes: want test-inst in list, got %v", instances)
	}
}

func TestHandleNodes_InvalidService_Returns400(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/nodes?service=bad.name")
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid service: want 400, got %d", w.Code)
	}
}

func TestHandleNodes_IncludesPeer(t *testing.T) {
	a := newTestAgent(t, defaultCfg())

	ch := make(chan discovery.Event, 1)
	go a.WatchPeers(ch)
	ch <- discovery.Event{
		Type:     discovery.EventJoin,
		Instance: discovery.ServiceInstance{Service: "test-svc", Instance: "peer-99", AgentURL: "http://peer99:8900"},
	}
	time.Sleep(20 * time.Millisecond)
	close(ch)

	w := agentGet(t, a, "/api/nodes?service=test-svc")
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	instances, _ := body["instances"].([]any)
	found := false
	for _, inst := range instances {
		m, _ := inst.(map[string]any)
		if m["instance"] == "peer-99" {
			found = true
		}
	}
	if !found {
		t.Errorf("nodes: want peer-99 in list, got %v", instances)
	}
}

// --- Fetch ---

func TestHandleFetch_LocalInstance_CallsTarget(t *testing.T) {
	store, _ := persistence.New("memory", nil)
	called := false
	tc := &testutil.StubTargetClient{
		GetFn: func(_ context.Context, key string) ([]byte, error) {
			called = true
			return []byte(`{"found":true,"value":"x"}`), nil
		},
	}
	a := agent.NewFromDeps(defaultCfg(), store, tc, &testutil.StubTransport{}, &testutil.StubResolver{},
		target.TargetInfo{Service: "test-svc", Instance: "test-inst"})
	defer a.Shutdown(context.Background())

	w := agentPost(t, a, "/api/fetch", map[string]any{
		"service": "test-svc", "instance": "test-inst", "key": "k",
	})
	if w.Code != http.StatusOK {
		t.Errorf("fetch: want 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !called {
		t.Error("fetch: target.Get was not called")
	}
}

func TestHandleFetch_ProxyLoop_Returns508(t *testing.T) {
	a := newTestAgent(t, defaultCfg())

	b, _ := json.Marshal(map[string]any{"service": "other-svc", "instance": "other-inst", "key": "k"})
	req := httptest.NewRequest(http.MethodPost, "/api/fetch", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lens-Proxied", "1")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusLoopDetected {
		t.Errorf("proxy loop: want 508, got %d", w.Code)
	}
}

func TestHandleFetch_InvalidJSON_Returns400(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	req := httptest.NewRequest(http.MethodPost, "/api/fetch", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad json: want 400, got %d", w.Code)
	}
}

func TestHandleFetch_RemoteTransportError_Returns502(t *testing.T) {
	store, _ := persistence.New("memory", nil)
	tr := &testutil.StubTransport{
		GetFn: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return nil, fmt.Errorf("peer unreachable")
		},
	}
	a := agent.NewFromDeps(defaultCfg(), store, &testutil.StubTargetClient{}, tr, &testutil.StubResolver{},
		target.TargetInfo{Service: "test-svc", Instance: "self"})
	defer a.Shutdown(context.Background())

	w := agentPost(t, a, "/api/fetch", map[string]any{
		"service": "test-svc", "instance": "other-inst", "key": "k",
	})
	if w.Code != http.StatusBadGateway {
		t.Errorf("transport error: want 502, got %d", w.Code)
	}
}

func TestHandleFetch_TargetError_Returns502(t *testing.T) {
	store, _ := persistence.New("memory", nil)
	tc := &testutil.StubTargetClient{
		GetFn: func(_ context.Context, _ string) ([]byte, error) {
			return nil, fmt.Errorf("target down")
		},
	}
	a := agent.NewFromDeps(defaultCfg(), store, tc, &testutil.StubTransport{}, &testutil.StubResolver{},
		target.TargetInfo{Service: "test-svc", Instance: "test-inst"})
	defer a.Shutdown(context.Background())

	w := agentPost(t, a, "/api/fetch", map[string]any{
		"service": "test-svc", "instance": "test-inst", "key": "k",
	})
	if w.Code != http.StatusBadGateway {
		t.Errorf("target error: want 502, got %d", w.Code)
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

func TestHandleAudit_LimitParam(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	agentPost(t, a, "/api/invalidate", map[string]any{"service": "test-svc"})
	a.Throttle.Allow("test-svc") // drain throttle
	agentPost(t, a, "/api/invalidate", map[string]any{"service": "test-svc"})
	a.Throttle.Allow("test-svc")
	agentPost(t, a, "/api/invalidate", map[string]any{"service": "test-svc"})

	w := agentGet(t, a, "/api/audit?limit=2")
	if w.Code != http.StatusOK {
		t.Fatalf("audit limit: want 200, got %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	if _, ok := body["count"]; !ok {
		t.Error("audit limit: response missing count field")
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

func TestRequireReady_HealthReturns503WhenNotLive(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	a.SetReady(false)

	if w := agentGet(t, a, "/api/health"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("health while not live: want 503, got %d", w.Code)
	}
}

func TestRequireReady_DeclareAllowedBeforeLive(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	a.SetReady(false)

	w := agentPost(t, a, "/api/declare", map[string]any{"keyName": "k", "ttlInSeconds": 60})
	if w.Code != http.StatusAccepted {
		t.Errorf("declare while not live: want 202, got %d", w.Code)
	}
}

// --- Services ---

func TestHandleServices_IncludesSelf(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/services")
	if w.Code != http.StatusOK {
		t.Fatalf("services: want 200, got %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	services, _ := body["services"].([]any)
	found := false
	for _, s := range services {
		if s == "test-svc" {
			found = true
		}
	}
	if !found {
		t.Errorf("services: want test-svc in list, got %v", services)
	}
}

// --- Keys ---

func TestHandleKeys_InvalidService_Returns400(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/keys?service=bad.name")
	if w.Code != http.StatusBadRequest {
		t.Errorf("keys bad service: want 400, got %d", w.Code)
	}
}

func TestHandleKeys_AllInstances_Returns200(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	agentPost(t, a, "/api/declare", map[string]any{"keyName": "user:1", "ttlInSeconds": 60})

	w := agentGet(t, a, "/api/keys?service=test-svc")
	if w.Code != http.StatusOK {
		t.Fatalf("keys: want 200, got %d\n%s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	if _, ok := body["keys"]; !ok {
		t.Error("keys: response missing 'keys' field")
	}
}

func TestHandleKeys_WithPattern_FiltersResults(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	agentPost(t, a, "/api/declare", map[string]any{"keyName": "user:123"})
	agentPost(t, a, "/api/declare", map[string]any{"keyName": "product:456"})

	w := agentGet(t, a, "/api/keys?service=test-svc&pattern=user")
	if w.Code != http.StatusOK {
		t.Fatalf("keys with pattern: want 200, got %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	keys, _ := body["keys"].([]any)
	for _, k := range keys {
		km, _ := k.(map[string]any)
		if name, _ := km["keyName"].(string); name == "product:456" {
			t.Error("pattern=user: should not return product:456")
		}
	}
}

func TestForwardKeys_LocalInstance_CallsTarget(t *testing.T) {
	store, _ := persistence.New("memory", nil)
	called := false
	tc := &testutil.StubTargetClient{
		KeysFn: func(_ context.Context, _, _, _ string) ([]byte, error) {
			called = true
			return []byte(`[{"key":"k1"}]`), nil
		},
	}
	a := agent.NewFromDeps(defaultCfg(), store, tc, &testutil.StubTransport{}, &testutil.StubResolver{},
		target.TargetInfo{Service: "test-svc", Instance: "test-inst"})
	defer a.Shutdown(context.Background())

	w := agentGet(t, a, "/api/keys?service=test-svc&instance=test-inst")
	if w.Code != http.StatusOK {
		t.Errorf("forwardKeys local: want 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !called {
		t.Error("forwardKeys local: targetClient.Keys was not called")
	}
}

func TestForwardKeys_ProxyLoop_Returns508(t *testing.T) {
	a := newTestAgent(t, defaultCfg())

	req := httptest.NewRequest(http.MethodGet, "/api/keys?service=test-svc&instance=other-inst", nil)
	req.Header.Set("X-Lens-Proxied", "1")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusLoopDetected {
		t.Errorf("proxy loop keys: want 508, got %d", w.Code)
	}
}

func TestForwardKeys_UnknownInstance_Returns404(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/keys?service=test-svc&instance=nonexistent")
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown instance: want 404, got %d", w.Code)
	}
}

func TestForwardKeys_WrongServiceForInstance_Returns404(t *testing.T) {
	a := newTestAgent(t, defaultCfg())

	ch := make(chan discovery.Event, 1)
	go a.WatchPeers(ch)
	ch <- discovery.Event{
		Type: discovery.EventJoin,
		Instance: discovery.ServiceInstance{
			Service: "other-svc", Instance: "peer-x", AgentURL: "http://peer-x:8900",
		},
	}
	time.Sleep(20 * time.Millisecond)
	close(ch)

	w := agentGet(t, a, "/api/keys?service=test-svc&instance=peer-x")
	if w.Code != http.StatusNotFound {
		t.Errorf("service mismatch: want 404, got %d", w.Code)
	}
}

// --- Providers ---

func TestHandleProviders_Self_Returns200(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/providers?service=test-svc")
	if w.Code != http.StatusOK {
		t.Fatalf("providers self: want 200, got %d\n%s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	if _, ok := body["transport"]; !ok {
		t.Error("providers: response missing 'transport' field")
	}
}

func TestHandleProviders_InvalidService_Returns400(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/providers?service=bad.name")
	if w.Code != http.StatusBadRequest {
		t.Errorf("providers invalid service: want 400, got %d", w.Code)
	}
}

func TestHandleProviders_UnknownService_Returns404(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	w := agentGet(t, a, "/api/providers?service=other-svc")
	if w.Code != http.StatusNotFound {
		t.Errorf("providers unknown service: want 404, got %d", w.Code)
	}
}

func TestSelfProviders_IncludesObserverList(t *testing.T) {
	store, _ := persistence.New("memory", nil)
	cfg := agent.Config{
		CooldownMS:        50,
		ObserverProviders: []agent.ObserverProviderConfig{{Name: "my-obs"}},
	}
	a := agent.NewFromDeps(cfg, store, &testutil.StubTargetClient{}, &testutil.StubTransport{}, &testutil.StubResolver{},
		target.TargetInfo{Service: "test-svc", Instance: "i1"})
	defer a.Shutdown(context.Background())

	w := agentGet(t, a, "/api/health")
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	providers, _ := body["providers"].(map[string]any)
	observers, _ := providers["observers"].([]any)
	if len(observers) == 0 {
		t.Error("selfProviders: observers list should be non-empty when ObserverProviders configured")
	}
}
