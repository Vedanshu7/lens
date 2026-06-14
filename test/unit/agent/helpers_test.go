package agent_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Vedanshu7/lens/internal/agent"
	"github.com/Vedanshu7/lens/internal/persistence"
	_ "github.com/Vedanshu7/lens/internal/persistence/memory"
	"github.com/Vedanshu7/lens/internal/target"
	"github.com/Vedanshu7/lens/test/testutil"
)

func defaultCfg() agent.Config {
	return agent.Config{CooldownMS: 50}
}

func newTestAgent(t *testing.T, cfg agent.Config) *agent.Agent {
	t.Helper()
	store, err := persistence.New("memory", nil)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	a := agent.NewFromDeps(
		cfg, store,
		&testutil.StubTargetClient{},
		&testutil.StubTransport{},
		&testutil.StubResolver{},
		target.TargetInfo{Service: "test-svc", Instance: "test-inst"},
	)
	t.Cleanup(func() { a.Shutdown(t.Context()) })
	return a
}

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
