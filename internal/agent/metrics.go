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

func newMetrics() *Metrics { return newMetricsWithReg(prometheus.DefaultRegisterer) }

func newMetricsWithReg(reg prometheus.Registerer) *Metrics {
	f := promauto.With(reg)
	return &Metrics{
		invalidateTotal: f.NewCounterVec(prometheus.CounterOpts{
			Name: "lens_invalidate_total",
			Help: "Total cache invalidation operations by service and status.",
		}, []string{"service", "status"}),
		invalidateDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lens_invalidate_duration_seconds",
			Help:    "Duration of invalidation operations in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"service"}),
		instancesActive: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lens_instances_active",
			Help: "Number of active instances per service.",
		}, []string{"service"}),
		fetchTotal: f.NewCounterVec(prometheus.CounterOpts{
			Name: "lens_fetch_total",
			Help: "Total fetch operations by service and transport.",
		}, []string{"service", "transport"}),
		httpRequests: f.NewCounterVec(prometheus.CounterOpts{
			Name: "lens_http_requests_total",
			Help: "Total HTTP requests handled by endpoint and status code.",
		}, []string{"endpoint", "status_code"}),
		replayApplied: f.NewCounterVec(prometheus.CounterOpts{
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

func newThrottle(cooldownMS int) *Throttle { return NewThrottle(cooldownMS) }

// NewThrottle creates a Throttle with the given cooldown in milliseconds.
func NewThrottle(cooldownMS int) *Throttle {
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

// Evict removes entries that have not been seen for longer than the cooldown period.
// It should be called periodically to prevent the map from growing unbounded when
// many distinct service names are used.
func (t *Throttle) Evict() {
	cutoff := time.Now().Add(-t.cooldown)
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, v := range t.lastSeen {
		if v.Before(cutoff) {
			delete(t.lastSeen, k)
		}
	}
}
