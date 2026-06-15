package observability_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "github.com/Vedanshu7/lens/internal/observability/influxdb"

	"github.com/Vedanshu7/lens/internal/observability"
)

func TestInfluxDBObserver_WritesLineProtocol(t *testing.T) {
	var received []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		received = append(received, string(body))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	obs, err := observability.New("influxdb", map[string]any{
		"url":    srv.URL,
		"token":  "test-token",
		"org":    "myorg",
		"bucket": "lens",
	})
	if err != nil {
		t.Fatalf("create observer: %v", err)
	}

	_ = obs.Record(context.Background(), observability.Event{
		Kind:      observability.EventInvalidate,
		Service:   "svc1",
		Instance:  "inst1",
		Success:   true,
		LatencyMs: 12.5,
		Confirmed: 3,
		Total:     3,
		Timestamp: time.Unix(0, 0).UTC(),
	})

	if err := obs.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if len(received) == 0 {
		t.Fatal("expected at least one write request to InfluxDB")
	}
	line := received[0]
	if !strings.Contains(line, "lens_event,") {
		t.Errorf("expected line protocol measurement, got: %q", line)
	}
	if !strings.Contains(line, "kind=invalidate") {
		t.Errorf("expected kind=invalidate tag, got: %q", line)
	}
	if !strings.Contains(line, "service=svc1") {
		t.Errorf("expected service=svc1 tag, got: %q", line)
	}
	if !strings.Contains(line, "success=true") {
		t.Errorf("expected success=true field, got: %q", line)
	}
}

func TestInfluxDBObserver_MissingConfig_ReturnsError(t *testing.T) {
	_, err := observability.New("influxdb", map[string]any{
		"url": "http://localhost:8086",
	})
	if err == nil {
		t.Error("expected error for missing token/org/bucket")
	}
}
