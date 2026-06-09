// Package webhookobserver delivers Lens telemetry events to an HTTP endpoint as JSON.
// Compatible with Grafana Loki push API, Alertmanager, and any custom HTTP receiver.
package webhookobserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/vedanshu/lens/internal/observability"
)

func init() {
	observability.Register("webhook", func(cfg map[string]any) (observability.Observer, error) {
		url, _ := cfg["url"].(string)
		if url == "" {
			return nil, fmt.Errorf("webhook observer: url is required")
		}
		headers := map[string]string{}
		if h, ok := cfg["headers"].(map[string]any); ok {
			for k, v := range h {
				if s, ok := v.(string); ok {
					headers[k] = s
				}
			}
		}
		return &webhookObserver{
			url:     url,
			headers: headers,
			client:  &http.Client{Timeout: 5 * time.Second},
		}, nil
	})
}

type webhookObserver struct {
	url     string
	headers map[string]string
	client  *http.Client
}

// Record serialises event e as JSON and POSTs it to the configured URL.
// Returns an error if marshalling, the request, or the server response fails.
func (w *webhookObserver) Record(ctx context.Context, e observability.Event) error {
	var err error
	var body []byte
	body, err = json.Marshal(e)
	if err == nil {
		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, "POST", w.url, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			for k, v := range w.headers {
				req.Header.Set(k, v)
			}
			var resp *http.Response
			resp, err = w.client.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode >= 400 {
					err = fmt.Errorf("webhook: server returned %d", resp.StatusCode)
				}
			}
		}
	}
	return err
}

// Close is a no-op and returns nil.
func (w *webhookObserver) Close() error { return nil }
