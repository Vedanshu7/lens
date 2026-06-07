package agent

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus counters, histograms, and gauges for the Lens agent.
type Metrics struct {
	invalidateTotal    *prometheus.CounterVec
	invalidateDuration *prometheus.HistogramVec
	instancesActive    *prometheus.GaugeVec
	fetchTotal         *prometheus.CounterVec
	httpRequests       *prometheus.CounterVec
	replayApplied      *prometheus.CounterVec
}

func newMetrics() *Metrics {
	return &Metrics{
		invalidateTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "lens_invalidate_total",
			Help: "Total cache invalidation operations by service and status.",
		}, []string{"service", "status"}),
		invalidateDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lens_invalidate_duration_seconds",
			Help:    "Duration of invalidation operations in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"service"}),
		instancesActive: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lens_instances_active",
			Help: "Number of active instances per service.",
		}, []string{"service"}),
		fetchTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "lens_fetch_total",
			Help: "Total fetch operations by service and transport.",
		}, []string{"service", "transport"}),
		httpRequests: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "lens_http_requests_total",
			Help: "Total HTTP requests handled by endpoint and status code.",
		}, []string{"endpoint", "status_code"}),
		replayApplied: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "lens_replay_applied_total",
			Help: "Total missed invalidations replayed on agent restart.",
		}, []string{"service"}),
	}
}

// Throttle enforces a per-key cooldown to prevent invalidation storms.
// Allow is safe for concurrent use.
type Throttle struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
	cooldown time.Duration
}

func newThrottle(cooldownMS int) *Throttle {
	return &Throttle{
		lastSeen: make(map[string]time.Time),
		cooldown: time.Duration(cooldownMS) * time.Millisecond,
	}
}

// Allow reports whether key is within its rate limit.
// Returns (true, 0) when the call is allowed, or (false, retryAfter)
// when the caller must wait retryAfter before the next allowed call.
func (t *Throttle) Allow(key string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	allowed := true
	wait := time.Duration(0)
	if last, ok := t.lastSeen[key]; ok {
		if remaining := t.cooldown - now.Sub(last); remaining > 0 {
			allowed = false
			wait = remaining
		}
	}
	if allowed {
		t.lastSeen[key] = now
	}
	return allowed, wait
}
