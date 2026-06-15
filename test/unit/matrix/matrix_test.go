// Package matrix_test verifies that the five Lens provider layers are independently
// swappable. Each test case wires a different combination of in-process providers
// through agent.NewFromDeps and asserts that the core invalidation flow works
// identically regardless of which provider fills each slot.
//
// Only providers that require no external services (memory persistence, static
// discovery) are used here. Add more rows as additional in-process providers land.
package matrix_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Vedanshu7/lens/internal/agent"
	"github.com/Vedanshu7/lens/internal/discovery"
	_ "github.com/Vedanshu7/lens/internal/discovery/static"
	"github.com/Vedanshu7/lens/internal/persistence"
	_ "github.com/Vedanshu7/lens/internal/persistence/memory"
	"github.com/Vedanshu7/lens/internal/target"
	"github.com/Vedanshu7/lens/internal/transport"
	"github.com/Vedanshu7/lens/test/testutil"
)

// combo defines one provider combination to exercise.
type combo struct {
	name        string
	mkStore     func(t *testing.T) persistence.Backend
	mkDisc      func(t *testing.T) discovery.Resolver
	mkTransport func(t *testing.T) transport.Transport
}

var combos = []combo{
	{
		name:        "memory / stub-discovery / stub-transport",
		mkStore:     memoryBackend,
		mkDisc:      stubDiscovery,
		mkTransport: stubTransport,
	},
	{
		name:        "memory / static-discovery / stub-transport",
		mkStore:     memoryBackend,
		mkDisc:      staticDiscovery,
		mkTransport: stubTransport,
	},
}

// TestMatrix_Invalidate_LocalTarget verifies that POST /api/invalidate reaches
// the local target client for every provider combination.
func TestMatrix_Invalidate_LocalTarget(t *testing.T) {
	for _, c := range combos {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			invalidated := 0
			tc := &testutil.StubTargetClient{
				InvalidateFn: func(_ context.Context, _ []byte) error {
					invalidated++
					return nil
				},
			}

			a := buildAgent(t, c, tc)
			postInvalidate(t, a, "svc", http.StatusOK)

			if invalidated == 0 {
				t.Errorf("combo %q: local target was not invalidated", c.name)
			}
		})
	}
}

// TestMatrix_Invalidate_WritesToPersistence verifies that /api/invalidate appends
// an entry to the replay log in persistence for every provider combination.
func TestMatrix_Invalidate_WritesToPersistence(t *testing.T) {
	for _, c := range combos {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			backend := c.mkStore(t)
			a := buildAgentWithStore(t, c, backend, &testutil.StubTargetClient{})
			postInvalidate(t, a, "svc", http.StatusOK)

			// handleInvalidate writes to the global audit log (lens:audit) on every call.
			entries, err := backend.LRange(context.Background(), "lens:audit", 0, -1)
			if err != nil {
				t.Fatalf("combo %q: LRange failed: %v", c.name, err)
			}
			if len(entries) == 0 {
				t.Errorf("combo %q: audit log is empty after invalidation", c.name)
			}
		})
	}
}

// TestMatrix_ProviderSwap_SameOutcome verifies that swapping the discovery
// provider does not change the observable invalidation outcome.
func TestMatrix_ProviderSwap_SameOutcome(t *testing.T) {
	type result struct {
		status      int
		invalidated int
	}

	var results []result

	for _, c := range combos {
		c := c
		invalidated := 0
		tc := &testutil.StubTargetClient{
			InvalidateFn: func(_ context.Context, _ []byte) error {
				invalidated++
				return nil
			},
		}
		a := buildAgent(t, c, tc)
		w := postInvalidate(t, a, "svc", -1) // -1 = no assertion
		results = append(results, result{status: w.Code, invalidated: invalidated})
	}

	for i := 1; i < len(results); i++ {
		if results[i].status != results[0].status {
			t.Errorf("combo[%d] status %d != combo[0] status %d", i, results[i].status, results[0].status)
		}
		if results[i].invalidated != results[0].invalidated {
			t.Errorf("combo[%d] invalidated %d != combo[0] invalidated %d", i, results[i].invalidated, results[0].invalidated)
		}
	}
}

// --- helpers ---

func buildAgent(t *testing.T, c combo, tc target.TargetClient) *agent.Agent {
	t.Helper()
	return buildAgentWithStore(t, c, c.mkStore(t), tc)
}

func buildAgentWithStore(t *testing.T, c combo, store persistence.Backend, tc target.TargetClient) *agent.Agent {
	t.Helper()
	return agent.NewFromDeps(
		agent.Config{CooldownMS: 0},
		store,
		tc,
		c.mkTransport(t),
		c.mkDisc(t),
		target.TargetInfo{Service: "svc", Instance: "inst"},
	)
}

// postInvalidate sends POST /api/invalidate for the given service.
// If wantStatus >= 0 the response code is asserted.
func postInvalidate(t *testing.T, a *agent.Agent, svc string, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"service": svc})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/invalidate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	if wantStatus >= 0 && w.Code != wantStatus {
		t.Errorf("POST /api/invalidate: want %d, got %d: %s", wantStatus, w.Code, w.Body.String())
	}
	return w
}

// --- provider constructors ---

func memoryBackend(t *testing.T) persistence.Backend {
	t.Helper()
	b, err := persistence.New("memory", nil)
	if err != nil {
		t.Fatalf("memory backend: %v", err)
	}
	return b
}

func stubDiscovery(_ *testing.T) discovery.Resolver {
	return &testutil.StubResolver{}
}

func staticDiscovery(t *testing.T) discovery.Resolver {
	t.Helper()
	disc, err := discovery.New(nil, "static", map[string]any{})
	if err != nil {
		t.Fatalf("static discovery: %v", err)
	}
	return disc
}

func stubTransport(_ *testing.T) transport.Transport {
	return &testutil.StubTransport{
		BroadcastFn: func(_ context.Context, _ string, _ []byte) ([]transport.Ack, error) {
			return nil, nil
		},
	}
}
