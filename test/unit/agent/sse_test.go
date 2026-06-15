package agent_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// flushRecorder wraps httptest.Recorder and adds http.Flusher support.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (fr *flushRecorder) Flush() { fr.flushed++ }

func TestSSEStream_RequiresAuth(t *testing.T) {
	cfg := defaultCfg()
	cfg.Token = "secret"
	a := newTestAgent(t, cfg)

	w := agentGet(t, a, "/api/events/stream")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestSSEStream_ConnectedFrame(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	a.SetReady(true)

	req := httptest.NewRequest(http.MethodGet, "/api/events/stream", nil)
	ctx, cancel := context.WithCancel(t.Context())
	req = req.WithContext(ctx)

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Routes().ServeHTTP(rec, req)
	}()

	// Give the handler time to write the initial comment frame.
	time.Sleep(60 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, ": connected") {
		t.Errorf("expected ': connected' frame in body, got: %q", body)
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected text/event-stream content-type, got %q", rec.Header().Get("Content-Type"))
	}
}

func TestSSEStream_DeliversEvent(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	a.SetReady(true)

	req := httptest.NewRequest(http.MethodGet, "/api/events/stream", nil)
	ctx, cancel := context.WithCancel(t.Context())
	req = req.WithContext(ctx)

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Routes().ServeHTTP(rec, req)
	}()

	// Wait for connection, then trigger an invalidation that emits an event.
	time.Sleep(30 * time.Millisecond)
	body := bytes.NewBufferString(`{"service":"svc","pattern":null}`)
	inv := httptest.NewRequest(http.MethodPost, "/api/invalidate", body)
	inv.Header.Set("Content-Type", "application/json")
	a.Routes().ServeHTTP(httptest.NewRecorder(), inv)

	time.Sleep(60 * time.Millisecond)
	cancel()
	<-done

	got := rec.Body.String()
	if !strings.Contains(got, "data:") {
		t.Errorf("expected at least one SSE data frame, got: %q", got)
	}
}
