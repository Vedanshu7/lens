package observability_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "github.com/Vedanshu7/lens/internal/observability/opensearch"

	"github.com/Vedanshu7/lens/internal/observability"
)

func TestOpenSearchObserver_BulkWrite(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":false,"items":[]}`)) //nolint:errcheck
	}))
	defer srv.Close()

	obs, err := observability.New("opensearch", map[string]any{
		"url":   srv.URL,
		"index": "test-lens",
	})
	if err != nil {
		t.Fatalf("create observer: %v", err)
	}

	_ = obs.Record(context.Background(), observability.Event{
		Kind:      observability.EventInvalidate,
		Service:   "svc1",
		Instance:  "i1",
		Success:   true,
		LatencyMs: 7.0,
		Timestamp: time.Unix(1000, 0).UTC(),
	})

	if err := obs.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if !strings.Contains(gotBody, `"kind":"invalidate"`) {
		t.Errorf("expected kind=invalidate in body, got: %q", gotBody)
	}
	if !strings.Contains(gotBody, `"service":"svc1"`) {
		t.Errorf("expected service=svc1 in body, got: %q", gotBody)
	}
	// Bulk API format: each doc prefixed by {"index":{}}
	if !strings.Contains(gotBody, `{"index":{}}`) {
		t.Errorf("expected bulk meta line, got: %q", gotBody)
	}
}

func TestOpenSearchObserver_MissingURL_ReturnsError(t *testing.T) {
	_, err := observability.New("opensearch", map[string]any{})
	if err == nil {
		t.Error("expected error when url is missing")
	}
}
