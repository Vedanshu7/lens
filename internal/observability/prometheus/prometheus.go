// Package promobserver bridges structured Lens events to Prometheus counters and histograms.
// It complements the baseline counters in agent/metrics.go with finer-grained
// latency histograms and peer-event counters exposed at /metrics.
package promobserver

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/Vedanshu7/lens/internal/observability"
)

func init() {
	observability.Register("prometheus", func(_ map[string]any) (observability.Observer, error) {
		return newPrometheusObserver(), nil
	})
}

type promObserver struct {
	invalidateLatency *prometheus.HistogramVec
	fetchLatency      *prometheus.HistogramVec
	peerEvents        *prometheus.CounterVec
	deadPods          prometheus.Counter
}

func newPrometheusObserver() *promObserver {
	return &promObserver{
		invalidateLatency: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lens_obs_invalidate_latency_ms",
			Help:    "Invalidation round-trip latency in milliseconds by transport.",
			Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
		}, []string{"service", "transport"}),
		fetchLatency: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lens_obs_fetch_latency_ms",
			Help:    "Fetch latency in milliseconds by transport.",
			Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
		}, []string{"service", "transport"}),
		peerEvents: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "lens_obs_peer_events_total",
			Help: "Peer join and leave events by service and type.",
		}, []string{"service", "type"}),
		deadPods: promauto.NewCounter(prometheus.CounterOpts{
			Name: "lens_obs_dead_pods_total",
			Help: "Dead pod detections — peers that failed to acknowledge after timeout.",
		}),
	}
}

// Record updates Prometheus metrics for the event kinds this provider cares about.
// Unrecognised event kinds are silently ignored.
func (p *promObserver) Record(_ context.Context, e observability.Event) error {
	switch e.Kind {
	case observability.EventInvalidate:
		if e.LatencyMs > 0 {
			p.invalidateLatency.WithLabelValues(e.Service, e.Transport).Observe(e.LatencyMs)
		}
	case observability.EventFetch:
		if e.LatencyMs > 0 {
			p.fetchLatency.WithLabelValues(e.Service, e.Transport).Observe(e.LatencyMs)
		}
	case observability.EventPeerJoin:
		p.peerEvents.WithLabelValues(e.Service, "join").Inc()
	case observability.EventPeerLeave:
		p.peerEvents.WithLabelValues(e.Service, "leave").Inc()
	case observability.EventDeadPod:
		p.deadPods.Inc()
	}
	return nil
}

// Close is a no-op because Prometheus metrics are registered globally and
// their lifecycle is managed by the default registerer.
func (p *promObserver) Close() error { return nil }
