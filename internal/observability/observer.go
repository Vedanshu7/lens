// Package observability defines the Observer interface for structured telemetry events
// and provides a provider registry and MultiObserver fan-out implementation.
// Providers register themselves via init() and are selected at runtime through blank imports.
package observability

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Observer receives structured telemetry events emitted by the Lens agent.
type Observer interface {
	// Record delivers a single event to the observability backend.
	// Implementations must not block the caller; they should buffer internally.
	Record(ctx context.Context, event Event) error
	// Close flushes any buffered events and releases resources.
	Close() error
}

// EventKind classifies a telemetry event by the operation that produced it.
type EventKind string

const (
	// EventInvalidate is emitted when a cache invalidation is broadcast.
	EventInvalidate EventKind = "invalidate"
	// EventFetch is emitted when a key is fetched from a remote peer.
	EventFetch EventKind = "fetch"
	// EventPeerJoin is emitted when a new peer joins the cluster.
	EventPeerJoin EventKind = "peer_join"
	// EventPeerLeave is emitted when a peer departs the cluster.
	EventPeerLeave EventKind = "peer_leave"
	// EventReplay is emitted when missed invalidations are replayed on startup.
	EventReplay EventKind = "replay"
	// EventDiscovery is emitted when the peer list is resolved.
	EventDiscovery EventKind = "discovery"
	// EventHTTPRequest is emitted for every inbound HTTP request to the agent.
	EventHTTPRequest EventKind = "http_request"
	// EventDeadPod is emitted when a peer fails to acknowledge a broadcast.
	EventDeadPod EventKind = "dead_pod"
)

// Event is a structured telemetry record emitted by the agent at key moments.
type Event struct {
	// Timestamp is the UTC time the event occurred. Populated by Record if zero.
	Timestamp time.Time
	// Service is the logical service name.
	Service string
	// Instance is the unique identifier of the emitting replica.
	Instance string
	// Kind classifies the event.
	Kind EventKind
	// Transport names the active transport provider (e.g. "grpc", "nats").
	Transport string
	// Success indicates whether the operation completed without error.
	Success bool
	// Error holds the error message when Success is false.
	Error string
	// LatencyMs is the operation duration in milliseconds.
	LatencyMs float64
	// DiscoveryMs is the time taken to resolve the peer list.
	DiscoveryMs float64
	// Pattern is the invalidation pattern, set for EventInvalidate.
	Pattern *string
	// Key is the cache key, set for EventFetch.
	Key *string
	// Confirmed is the number of peers that acknowledged the broadcast.
	Confirmed int
	// Total is the total number of peers targeted by the broadcast.
	Total int
	// PeerID identifies the affected peer for EventPeerJoin, EventPeerLeave, and EventDeadPod.
	PeerID string
	// Count is the number of entries applied, set for EventReplay.
	Count int
}

// Factory creates an Observer from provider config.
type Factory func(cfg map[string]any) (Observer, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register records f under name so it can be selected at runtime.
// It is called from provider init() functions.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = f
}

// Has reports whether name has been registered as an observability provider.
func Has(name string) bool {
	mu.RLock()
	_, ok := registry[name]
	mu.RUnlock()
	return ok
}

// New constructs the named observability provider from cfg.
// Returns an error if name is not registered.
func New(name string, cfg map[string]any) (Observer, error) {
	mu.RLock()
	f, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("observability provider %q not registered (forgot blank import?)", name)
	}
	return f(cfg)
}

// multiObsChanSize is the capacity of the MultiObserver event channel.
// When the buffer is full, Record drops the event rather than blocking.
const multiObsChanSize = 512

// MultiObserver fans out events to all configured observers via a bounded channel.
// A single drain goroutine delivers events sequentially so observers do not need
// to be thread-safe with respect to each other.
type MultiObserver struct {
	observers []Observer
	sqlObs    SQLQuerier
	ch        chan Event
	done      chan struct{}
}

// SQLQuerier is implemented by the SQL observer to expose query access
// for the /api/obs/* dashboard routes.
type SQLQuerier interface {
	Observer
	// QueryLatency returns per-minute latency percentiles for service over interval.
	QueryLatency(ctx context.Context, service, interval string) ([]LatencyBucket, error)
	// QueryDeadPods returns dead-pod detection events for service over interval.
	QueryDeadPods(ctx context.Context, service, interval string) ([]DeadPodEvent, error)
	// QueryDiscovery returns peer discovery timeline events over interval.
	QueryDiscovery(ctx context.Context, interval string) ([]DiscoveryEvent, error)
	// QueryFlow returns aggregated flow statistics for service over interval.
	QueryFlow(ctx context.Context, service, interval string) (*FlowStats, error)
	// QuerySummary returns aggregate metrics for service over interval.
	QuerySummary(ctx context.Context, service, interval string) (*SummaryStats, error)
}

// LatencyBucket holds per-minute latency percentiles returned by /api/obs/latency.
type LatencyBucket struct {
	Bucket    time.Time `json:"bucket"`
	Transport string    `json:"transport"`
	P50       float64   `json:"p50"`
	P95       float64   `json:"p95"`
	P99       float64   `json:"p99"`
}

// DeadPodEvent describes a single dead-pod detection returned by /api/obs/deadpods.
type DeadPodEvent struct {
	Timestamp   time.Time `json:"ts"`
	PeerID      string    `json:"peerId"`
	DetectionMs float64   `json:"detectionMs"`
}

// DiscoveryEvent describes a single peer discovery resolution returned by /api/obs/discovery.
type DiscoveryEvent struct {
	Timestamp    time.Time `json:"ts"`
	Instance     string    `json:"instance"`
	PeerCount    int       `json:"peerCount"`
	ResolutionMs float64   `json:"resolutionMs"`
}

// FlowStats summarises operation counts by kind and outcome for /api/obs/flow.
type FlowStats struct {
	Invalidate struct {
		Total   int `json:"total"`
		Success int `json:"success"`
		Partial int `json:"partial"`
		Failure int `json:"failure"`
	} `json:"invalidate"`
	Fetch struct {
		Total   int `json:"total"`
		Success int `json:"success"`
		Failure int `json:"failure"`
	} `json:"fetch"`
	Replay struct {
		Total int `json:"total"`
	} `json:"replay"`
}

// SummaryStats holds aggregate metrics for a service window, returned by /api/obs/summary.
type SummaryStats struct {
	TotalInvalidations int     `json:"totalInvalidations"`
	AvgLatencyMs       float64 `json:"avgLatencyMs"`
	P99LatencyMs       float64 `json:"p99LatencyMs"`
	FailureRatePct     float64 `json:"failureRatePct"`
	DeadPodsDetected   int     `json:"deadPodsDetected"`
	PeersJoined        int     `json:"peersJoined"`
	PeersLeft          int     `json:"peersLeft"`
}

// NewMultiObserver builds a MultiObserver and starts its drain goroutine.
// If observers is empty, the returned MultiObserver discards all events with zero overhead.
func NewMultiObserver(observers []Observer) *MultiObserver {
	m := &MultiObserver{
		observers: observers,
		ch:        make(chan Event, multiObsChanSize),
		done:      make(chan struct{}),
	}
	for _, o := range observers {
		if sq, ok := o.(SQLQuerier); ok {
			m.sqlObs = sq
			break
		}
	}
	go m.drain()
	return m
}

// drain is the single goroutine that delivers events to each observer in order.
func (m *MultiObserver) drain() {
	defer close(m.done)
	for e := range m.ch {
		for _, o := range m.observers {
			o.Record(context.Background(), e) //nolint:errcheck
		}
	}
}

// Record enqueues event e for delivery. Returns immediately when no observers
// are configured. Drops the event when the buffer is full so the invalidation
// hot path is never blocked.
func (m *MultiObserver) Record(_ context.Context, e Event) error {
	if len(m.observers) == 0 {
		return nil
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	select {
	case m.ch <- e:
	default:
	}
	return nil
}

// Close drains the event channel and shuts down all observers.
func (m *MultiObserver) Close() error {
	close(m.ch)
	<-m.done
	for _, o := range m.observers {
		o.Close() //nolint:errcheck
	}
	return nil
}

// SQLObserver returns the SQL observer if one is configured, allowing
// dashboard routes to execute queries against the lens_events table.
// ok is false when no SQL observer is active.
func (m *MultiObserver) SQLObserver() (SQLQuerier, bool) {
	return m.sqlObs, m.sqlObs != nil
}
