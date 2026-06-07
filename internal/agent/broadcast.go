package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/vedanshu/lens/internal/store"
)

// Message is the invalidation payload exchanged between sidecars via the transport layer.
type Message struct {
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
	Origin  string          `json:"origin"`
	Time    string          `json:"time"`
	ReplyTo string          `json:"replyTo,omitempty"`
}

// RPC is the legacy direct-channel message format, retained for backwards compatibility.
type RPC struct {
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
	ReplyTo string          `json:"replyTo"`
}

// maxRetries is the number of delivery attempts made before an invalidation is abandoned.
const maxRetries = 3

// applyInvalidation delivers m.Payload to the target service's invalidate endpoint
// with up to maxRetries exponential-backoff attempts. Each failed attempt waits
// attempt × 1 second before retrying.
func (a *Agent) applyInvalidation(ctx context.Context, m Message) {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err := a.post(ctx,
			a.Config.TargetURL+"/internal/lens/invalidate",
			"application/json",
			strings.NewReader(string(m.Payload)),
		)
		if err != nil {
			lastErr = err
			slog.Warn("invalidation attempt failed", "attempt", attempt, "max", maxRetries, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(attempt) * time.Second):
			}
			continue
		}
		closeBody(resp)
		if resp.StatusCode >= 500 {
			lastErr = nil
			slog.Warn("invalidation: target error", "attempt", attempt, "status", resp.StatusCode)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(attempt) * time.Second):
			}
			continue
		}
		slog.Info("invalidation applied", "origin", m.Origin)
		return
	}
	slog.Error("invalidation failed after retries", "origin", m.Origin, "err", lastErr)
}

// writeInvalidationLog appends payload to the persistence replay log for svc.
// Restarting instances read this log via replayMissed to catch up on missed events.
// The log is capped at 100 entries with a 24-hour TTL.
func (a *Agent) writeInvalidationLog(ctx context.Context, svc string, payload []byte) {
	entry, _ := json.Marshal(map[string]any{
		"payload": json.RawMessage(payload),
		"ts":      time.Now().UTC().Format(time.RFC3339),
		"origin":  a.Info.Instance,
	})
	logKey := store.LogKey(svc)
	pipe := a.store.Pipeline()
	pipe.LPush(ctx, logKey, string(entry))
	pipe.LTrim(ctx, logKey, 0, 99)
	pipe.Expire(ctx, logKey, 24*time.Hour)
	pipe.Exec(ctx) //nolint:errcheck
}
