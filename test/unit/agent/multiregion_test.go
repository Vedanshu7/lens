package agent_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Vedanshu7/lens/internal/agent"
)

func TestMultiRegion_InvalidateForwardedToRegion(t *testing.T) {
	received := make(chan string, 10)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Lens-Cross-Region") != "true" {
			t.Errorf("expected X-Lens-Cross-Region: true, got %q", r.Header.Get("X-Lens-Cross-Region"))
		}
		received <- string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"confirmed":0,"total":0}`)) //nolint:errcheck
	}))
	defer remote.Close()

	cfg := defaultCfg()
	cfg.Regions = []agent.RegionConfig{
		{Name: "us-east", URL: remote.URL, Token: ""},
	}
	a := newTestAgent(t, cfg)

	body := strings.NewReader(`{"service":"svc","pattern":null}`)
	req := httptest.NewRequest(http.MethodPost, "/api/invalidate", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from local invalidate, got %d", w.Code)
	}

	select {
	case raw := <-received:
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("invalid JSON forwarded: %v", err)
		}
		if payload["service"] != "svc" {
			t.Errorf("expected service=svc, got %v", payload["service"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected invalidation to be forwarded to remote region within 2s")
	}
}

func TestMultiRegion_CrossRegionHeader_PreventsLoop(t *testing.T) {
	looped := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		looped = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))
	defer remote.Close()

	cfg := defaultCfg()
	cfg.Regions = []agent.RegionConfig{{Name: "eu-west", URL: remote.URL}}
	a := newTestAgent(t, cfg)

	body := strings.NewReader(`{"service":"svc","pattern":null}`)
	req := httptest.NewRequest(http.MethodPost, "/api/invalidate", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lens-Cross-Region", "true") // simulate incoming cross-region request
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)

	time.Sleep(100 * time.Millisecond)

	if looped {
		t.Error("expected no re-broadcast when X-Lens-Cross-Region header is set (loop prevention)")
	}
}
