package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Vedanshu7/lens/internal/agent"
	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/persistence"
	_ "github.com/Vedanshu7/lens/internal/persistence/memory"
	"github.com/Vedanshu7/lens/internal/store"
	"github.com/Vedanshu7/lens/internal/target"
	"github.com/Vedanshu7/lens/test/testutil"
)

// --- SelfInstance / SelfService ---

func TestSelfInstance_ReturnsInfoInstance(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	if got := a.SelfInstance(); got != "test-inst" {
		t.Errorf("SelfInstance: want test-inst, got %q", got)
	}
}

func TestSelfService_ReturnsInfoService(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	if got := a.SelfService(); got != "test-svc" {
		t.Errorf("SelfService: want test-svc, got %q", got)
	}
}

// --- WatchPeers ---

func TestWatchPeers_JoinAddsToServiceList(t *testing.T) {
	a := newTestAgent(t, defaultCfg())

	ch := make(chan discovery.Event, 2)
	go a.WatchPeers(ch)

	ch <- discovery.Event{
		Type: discovery.EventJoin,
		Instance: discovery.ServiceInstance{
			Service: "test-svc", Instance: "peer-1", AgentURL: "http://peer1:8900",
		},
	}
	time.Sleep(20 * time.Millisecond)
	close(ch)

	found := false
	for _, p := range a.PeersForService("test-svc") {
		if p.Instance == "peer-1" {
			found = true
		}
	}
	if !found {
		t.Error("WatchPeers: EventJoin did not add peer-1")
	}
}

func TestWatchPeers_LeaveRemovesFromServiceList(t *testing.T) {
	a := newTestAgent(t, defaultCfg())

	ch := make(chan discovery.Event, 3)
	go a.WatchPeers(ch)

	ch <- discovery.Event{
		Type:     discovery.EventJoin,
		Instance: discovery.ServiceInstance{Service: "test-svc", Instance: "peer-x"},
	}
	time.Sleep(20 * time.Millisecond)

	ch <- discovery.Event{
		Type:     discovery.EventLeave,
		Instance: discovery.ServiceInstance{Service: "test-svc", Instance: "peer-x"},
	}
	time.Sleep(20 * time.Millisecond)
	close(ch)

	for _, p := range a.PeersForService("test-svc") {
		if p.Instance == "peer-x" {
			t.Error("WatchPeers: EventLeave did not remove peer-x")
		}
	}
}

// --- PeersForService ---

func TestPeersForService_ExcludesSelf(t *testing.T) {
	a := newTestAgent(t, defaultCfg())

	ch := make(chan discovery.Event, 2)
	go a.WatchPeers(ch)

	ch <- discovery.Event{
		Type:     discovery.EventJoin,
		Instance: discovery.ServiceInstance{Service: "test-svc", Instance: "test-inst"},
	}
	ch <- discovery.Event{
		Type:     discovery.EventJoin,
		Instance: discovery.ServiceInstance{Service: "test-svc", Instance: "peer-2"},
	}
	time.Sleep(30 * time.Millisecond)
	close(ch)

	for _, p := range a.PeersForService("test-svc") {
		if p.Instance == "test-inst" {
			t.Error("PeersForService: must not return self (test-inst)")
		}
	}
}

// --- WriteInvalidationLog ---

func TestWriteInvalidationLog_AppearsInStore(t *testing.T) {
	ctx := context.Background()
	b, _ := persistence.New("memory", nil)
	defer b.Close() //nolint:errcheck

	a := agent.NewFromDeps(
		agent.Config{},
		b, &testutil.StubTargetClient{}, &testutil.StubTransport{}, &testutil.StubResolver{},
		target.TargetInfo{Service: "svc", Instance: "inst"},
	)
	defer a.Shutdown(ctx)

	payload := []byte(`{"pattern":null}`)
	a.WriteInvalidationLog(ctx, "svc", payload)

	entries, err := b.LRange(ctx, store.LogKey("svc"), 0, -1)
	if err != nil {
		t.Fatalf("LRange: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 log entry, got %d", len(entries))
	}

	var e struct {
		Payload json.RawMessage `json:"payload"`
		Ts      string          `json:"ts"`
		Origin  string          `json:"origin"`
	}
	if err := json.Unmarshal([]byte(entries[0]), &e); err != nil {
		t.Fatalf("unmarshal log entry: %v", err)
	}
	if string(e.Payload) != string(payload) {
		t.Errorf("log payload: want %q, got %q", payload, e.Payload)
	}
	if e.Origin != "inst" {
		t.Errorf("log origin: want inst, got %q", e.Origin)
	}
	if e.Ts == "" {
		t.Error("log ts: want non-empty timestamp")
	}
}

// --- GetFromTarget ---

func TestGetFromTarget_CallsTargetClient(t *testing.T) {
	called := false
	tc := &testutil.StubTargetClient{
		GetFn: func(_ context.Context, key string) ([]byte, error) {
			called = true
			return []byte(`{"found":true,"value":"hello"}`), nil
		},
	}
	b, _ := persistence.New("memory", nil)
	a := agent.NewFromDeps(
		defaultCfg(), b, tc, &testutil.StubTransport{}, &testutil.StubResolver{},
		target.TargetInfo{Service: "svc", Instance: "inst"},
	)
	defer a.Shutdown(context.Background())

	body, err := a.GetFromTarget(context.Background(), []byte(`{"key":"my-key"}`))
	if err != nil {
		t.Fatalf("GetFromTarget: %v", err)
	}
	if !called {
		t.Error("GetFromTarget: target.Get was not called")
	}
	if string(body) != `{"found":true,"value":"hello"}` {
		t.Errorf("GetFromTarget: unexpected body %q", body)
	}
}

func TestGetFromTarget_InvalidPayload_ReturnsError(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	_, err := a.GetFromTarget(context.Background(), []byte(`not-json`))
	if err == nil {
		t.Error("GetFromTarget: want error for bad payload, got nil")
	}
}

// --- allServices includes store members ---

func TestAllServices_IncludesStoreMembers(t *testing.T) {
	ctx := context.Background()
	b, _ := persistence.New("memory", nil)
	b.SAdd(ctx, "lens:services", "external-svc") //nolint:errcheck

	a := agent.NewFromDeps(
		defaultCfg(), b,
		&testutil.StubTargetClient{}, &testutil.StubTransport{}, &testutil.StubResolver{},
		target.TargetInfo{Service: "self-svc", Instance: "i1"},
	)
	defer a.Shutdown(ctx)

	w := agentGet(t, a, "/api/services")
	if w.Code != http.StatusOK {
		t.Fatalf("services: want 200, got %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck
	svcs, _ := body["services"].([]any)
	found := false
	for _, s := range svcs {
		if s == "external-svc" {
			found = true
		}
	}
	if !found {
		t.Errorf("services: expected external-svc from store SMembers, got %v", svcs)
	}
}
