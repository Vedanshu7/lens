package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// broadcastToRegions forwards an invalidation to all configured remote regions.
// It runs each region call in its own goroutine and logs results; it never
// blocks or affects the local response. An incoming request that already carries
// X-Lens-Cross-Region: true is not forwarded again (loop prevention).
func (a *Agent) broadcastToRegions(service string, pattern *string) {
	if len(a.Config.Regions) == 0 {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"service": service,
		"pattern": pattern,
	})
	for _, r := range a.Config.Regions {
		r := r
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			url := strings.TrimRight(r.URL, "/") + "/api/invalidate"
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
			if err != nil {
				slog.Warn("multi-region: build request failed", "region", r.Name, "err", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Lens-Cross-Region", "true")
			if r.Token != "" {
				req.Header.Set("x-lens-token", r.Token)
			}
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				slog.Warn("multi-region: broadcast failed", "region", r.Name, "err", err)
				return
			}
			defer func() {
				io.Copy(io.Discard, resp.Body) //nolint:errcheck
				resp.Body.Close()              //nolint:errcheck
			}()
			slog.Info("multi-region: broadcast sent", "region", r.Name, "status", resp.StatusCode)
		}()
	}
}
