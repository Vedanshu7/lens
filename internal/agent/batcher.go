package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

func marshalInvalidatePayload(pattern *string) ([]byte, error) {
	return json.Marshal(map[string]any{"pattern": pattern})
}

// batcher coalesces invalidation requests per service within a debounce window.
// The last pattern received wins; a nil pattern (full invalidation) always takes
// precedence over a non-nil pattern. When the window elapses, fn is called once
// with the coalesced service and pattern.
type batcher struct {
	mu      sync.Mutex
	pending map[string]*batchEntry
	window  time.Duration
	fn      func(ctx context.Context, service string, pattern *string)
}

type batchEntry struct {
	pattern *string
	timer   *time.Timer
}

func newBatcher(windowMS int, fn func(ctx context.Context, service string, pattern *string)) *batcher {
	return &batcher{
		pending: make(map[string]*batchEntry),
		window:  time.Duration(windowMS) * time.Millisecond,
		fn:      fn,
	}
}

// add queues an invalidation for service, resetting the debounce timer.
func (b *batcher) add(service string, pattern *string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.pending[service]
	if ok {
		e.timer.Stop()
		// nil pattern means full invalidation — takes precedence over any glob.
		if pattern == nil {
			e.pattern = nil
		} else if e.pattern != nil {
			e.pattern = pattern
		}
	} else {
		e = &batchEntry{pattern: pattern}
		b.pending[service] = e
	}

	e.timer = time.AfterFunc(b.window, func() {
		b.mu.Lock()
		entry, ok := b.pending[service]
		if !ok {
			b.mu.Unlock()
			return
		}
		delete(b.pending, service)
		b.mu.Unlock()
		b.fn(context.Background(), service, entry.pattern)
	})
}

// executeBroadcast performs the actual broadcast for a single service+pattern.
// Called directly (sync path) or via batcher (async path).
func (a *Agent) executeBroadcast(ctx context.Context, service string, pattern *string) {
	nodes := a.listNodes(service)
	total := len(nodes)

	payload, _ := marshalInvalidatePayload(pattern)

	if service == a.Info.Service {
		go func() {
			if err := a.targetClient.Invalidate(ctx, payload); err != nil {
				slog.Warn("local invalidation failed", "service", service, "err", err)
			}
		}()
	}

	acks, err := a.transport.Broadcast(ctx, service, payload)
	if err != nil {
		slog.Error("broadcast failed", "service", service, "err", err)
		return
	}

	confirmed := 0
	for _, ack := range acks {
		if ack.Success {
			confirmed++
		}
	}

	status := "success"
	if confirmed < total {
		status = "partial"
	}
	if confirmed == 0 && total > 0 {
		status = "failure"
	}
	a.Metrics.invalidateTotal.WithLabelValues(service, status).Inc()
	a.writeInvalidationLog(ctx, service, payload)
	slog.Info("batched broadcast complete", "service", service, "confirmed", confirmed, "total", total)
}
