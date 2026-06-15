package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/Vedanshu7/lens/internal/observability"
)

// sseHub fans out observability events to connected SSE clients.
// It implements Observer so it can be plugged into MultiObserver.
type sseHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
	closed  bool
}

func newSSEHub() *sseHub {
	return &sseHub{clients: make(map[chan []byte]struct{})}
}

// Record encodes the event as a JSON SSE data frame and enqueues it for each
// connected client. Slow clients are silently dropped rather than blocking the
// invalidation hot path.
func (h *sseHub) Record(_ context.Context, e observability.Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	msg := fmt.Appendf(nil, "data: %s\n\n", data)
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
		}
	}
	return nil
}

// Close shuts down the hub and disconnects all clients.
func (h *sseHub) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	for ch := range h.clients {
		close(ch)
	}
	h.clients = nil
	return nil
}

// subscribe streams SSE events to the HTTP client until the request context is cancelled.
func (h *sseHub) subscribe(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan []byte, 64)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.clients[ch] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
	}()

	// Initial comment frame confirms the stream is alive.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			w.Write(msg) //nolint:errcheck
			flusher.Flush()
		}
	}
}
