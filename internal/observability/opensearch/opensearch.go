// Package opensearchobserver ships Lens telemetry events to OpenSearch or
// Elasticsearch using the Bulk API. Events are batched and flushed every
// 5 seconds or when 200 events accumulate.
//
// Required config keys:
//
//	url   — OpenSearch/ES base URL (e.g. https://localhost:9200)
//	index — target index name (default: "lens-events")
//
// Optional config keys:
//
//	username           — HTTP basic-auth username
//	password           — HTTP basic-auth password
//	apiKey             — Base64-encoded "id:api_key" (overrides basic auth)
//	insecureSkipVerify — set "true" to disable TLS certificate verification
package opensearchobserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Vedanshu7/lens/internal/observability"
)

func init() {
	observability.Register("opensearch", func(cfg map[string]any) (observability.Observer, error) {
		url, _ := cfg["url"].(string)
		if url == "" {
			return nil, fmt.Errorf("opensearch observer: url is required")
		}
		index, _ := cfg["index"].(string)
		if index == "" {
			index = "lens-events"
		}
		username, _ := cfg["username"].(string)
		password, _ := cfg["password"].(string)
		apiKey, _ := cfg["apiKey"].(string)
		skipVerify, _ := cfg["insecureSkipVerify"].(string)

		transport := http.DefaultTransport
		if skipVerify == "true" {
			transport = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			}
		}

		o := &osObserver{
			bulkURL:  strings.TrimRight(url, "/") + "/" + index + "/_bulk",
			username: username,
			password: password,
			apiKey:   apiKey,
			client:   &http.Client{Transport: transport, Timeout: 10 * time.Second},
			buf:      make([]osEvent, 0, 200),
			done:     make(chan struct{}),
		}
		go o.flusher()
		return o, nil
	})
}

type osObserver struct {
	mu       sync.Mutex
	buf      []osEvent
	bulkURL  string
	username string
	password string
	apiKey   string
	client   *http.Client
	done     chan struct{}
}

type osEvent struct {
	Timestamp time.Time `json:"@timestamp"`
	Kind      string    `json:"kind"`
	Service   string    `json:"service"`
	Instance  string    `json:"instance"`
	Transport string    `json:"transport,omitempty"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
	LatencyMs float64   `json:"latency_ms"`
	Confirmed int       `json:"confirmed"`
	Total     int       `json:"total"`
}

func eventToOSDoc(e observability.Event) osEvent {
	ts := e.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return osEvent{
		Timestamp: ts,
		Kind:      string(e.Kind),
		Service:   e.Service,
		Instance:  e.Instance,
		Transport: e.Transport,
		Success:   e.Success,
		Error:     e.Error,
		LatencyMs: e.LatencyMs,
		Confirmed: e.Confirmed,
		Total:     e.Total,
	}
}

func (o *osObserver) Record(_ context.Context, e observability.Event) error {
	o.mu.Lock()
	o.buf = append(o.buf, eventToOSDoc(e))
	full := len(o.buf) >= 200
	o.mu.Unlock()
	if full {
		o.flush()
	}
	return nil
}

func (o *osObserver) Close() error {
	close(o.done)
	o.flush()
	return nil
}

func (o *osObserver) flusher() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-o.done:
			return
		case <-ticker.C:
			o.flush()
		}
	}
}

var bulkMeta = []byte(`{"index":{}}` + "\n")

func (o *osObserver) flush() {
	o.mu.Lock()
	if len(o.buf) == 0 {
		o.mu.Unlock()
		return
	}
	events := o.buf
	o.buf = make([]osEvent, 0, 200)
	o.mu.Unlock()

	var body bytes.Buffer
	for _, ev := range events {
		body.Write(bulkMeta)
		enc, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		body.Write(enc)
		body.WriteByte('\n')
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, o.bulkURL, &body)
	if err != nil {
		slog.Warn("opensearch observer: build request failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	switch {
	case o.apiKey != "":
		req.Header.Set("Authorization", "ApiKey "+o.apiKey)
	case o.username != "":
		req.SetBasicAuth(o.username, o.password)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		slog.Warn("opensearch observer: bulk write failed", "err", err)
		return
	}
	defer func() {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()              //nolint:errcheck
	}()
	if resp.StatusCode >= 300 {
		slog.Warn("opensearch observer: bulk rejected", "status", resp.StatusCode)
	}
}
