// Package influxdbobserver ships Lens telemetry events to InfluxDB v2 using
// the HTTP write API and line protocol. Events are buffered and flushed every
// 5 seconds or when the buffer reaches 200 lines, whichever comes first.
//
// Required config keys:
//
//	url    — InfluxDB base URL (e.g. http://localhost:8086)
//	token  — InfluxDB API token
//	org    — InfluxDB organization name
//	bucket — InfluxDB bucket name
package influxdbobserver

import (
	"bytes"
	"context"
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
	observability.Register("influxdb", func(cfg map[string]any) (observability.Observer, error) {
		url, _ := cfg["url"].(string)
		token, _ := cfg["token"].(string)
		org, _ := cfg["org"].(string)
		bucket, _ := cfg["bucket"].(string)
		if url == "" || token == "" || org == "" || bucket == "" {
			return nil, fmt.Errorf("influxdb observer: url, token, org, and bucket are required")
		}
		o := &influxObserver{
			writeURL: url + "/api/v2/write?org=" + org + "&bucket=" + bucket + "&precision=ms",
			token:    token,
			client:   &http.Client{Timeout: 10 * time.Second},
			buf:      make([]string, 0, 200),
			done:     make(chan struct{}),
		}
		go o.flusher()
		return o, nil
	})
}

type influxObserver struct {
	mu       sync.Mutex
	buf      []string
	writeURL string
	token    string
	client   *http.Client
	done     chan struct{}
}

// Record buffers the event as an InfluxDB line protocol measurement.
func (o *influxObserver) Record(_ context.Context, e observability.Event) error {
	line := eventToLine(e)
	o.mu.Lock()
	o.buf = append(o.buf, line)
	full := len(o.buf) >= 200
	o.mu.Unlock()
	if full {
		o.flush()
	}
	return nil
}

// Close flushes buffered events and stops the background flusher.
func (o *influxObserver) Close() error {
	close(o.done)
	o.flush()
	return nil
}

func (o *influxObserver) flusher() {
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

func (o *influxObserver) flush() {
	o.mu.Lock()
	if len(o.buf) == 0 {
		o.mu.Unlock()
		return
	}
	lines := o.buf
	o.buf = make([]string, 0, 200)
	o.mu.Unlock()

	body := strings.Join(lines, "\n")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, o.writeURL, bytes.NewBufferString(body))
	if err != nil {
		slog.Warn("influxdb observer: build request failed", "err", err)
		return
	}
	req.Header.Set("Authorization", "Token "+o.token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := o.client.Do(req)
	if err != nil {
		slog.Warn("influxdb observer: write failed", "err", err)
		return
	}
	defer func() {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()              //nolint:errcheck
	}()
	if resp.StatusCode >= 300 {
		slog.Warn("influxdb observer: write rejected", "status", resp.StatusCode)
	}
}

// eventToLine converts an observability event to an InfluxDB line protocol record.
func eventToLine(e observability.Event) string {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	tags := fmt.Sprintf("kind=%s,service=%s,instance=%s", escape(string(e.Kind)), escape(e.Service), escape(e.Instance))
	if e.Transport != "" {
		tags += ",transport=" + escape(e.Transport)
	}
	success := "false"
	if e.Success {
		success = "true"
	}
	fields := fmt.Sprintf("success=%s,latency_ms=%.3f,confirmed=%di,total=%di", success, e.LatencyMs, e.Confirmed, e.Total)
	if e.Error != "" {
		fields += `,error="` + escapeStr(e.Error) + `"`
	}
	ts := e.Timestamp.UnixMilli()
	return fmt.Sprintf("lens_event,%s %s %d", tags, fields, ts)
}

// escape replaces characters that are special in InfluxDB tag keys/values.
func escape(s string) string {
	s = strings.ReplaceAll(s, " ", "\\ ")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "=", "\\=")
	return s
}

func escapeStr(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}
