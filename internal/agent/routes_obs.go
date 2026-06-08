package agent

import (
	"encoding/json"
	"net/http"
)

// registerObsRoutes adds /api/obs/* routes to mux when a SQL observer is active.
// These routes are omitted entirely when no SQL observer is configured because
// they require direct query access to the lens_events table.
func (a *Agent) registerObsRoutes(mux *http.ServeMux) {
	sqlObs, ok := a.Obs.SQLObserver()
	if !ok {
		return
	}

	gate := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, a.authenticate(a.requireReady(h)))
	}

	gate("GET /api/obs/latency", func(w http.ResponseWriter, r *http.Request) {
		svc := r.URL.Query().Get("service")
		from := r.URL.Query().Get("from")
		if from == "" {
			from = "1 hour"
		}
		buckets, err := sqlObs.QueryLatency(r.Context(), svc, from)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"buckets": buckets}) //nolint:errcheck
	})

	gate("GET /api/obs/deadpods", func(w http.ResponseWriter, r *http.Request) {
		svc := r.URL.Query().Get("service")
		from := r.URL.Query().Get("from")
		if from == "" {
			from = "24 hours"
		}
		events, err := sqlObs.QueryDeadPods(r.Context(), svc, from)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"events": events}) //nolint:errcheck
	})

	gate("GET /api/obs/discovery", func(w http.ResponseWriter, r *http.Request) {
		from := r.URL.Query().Get("from")
		if from == "" {
			from = "24 hours"
		}
		events, err := sqlObs.QueryDiscovery(r.Context(), from)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"events": events}) //nolint:errcheck
	})

	gate("GET /api/obs/flow", func(w http.ResponseWriter, r *http.Request) {
		svc := r.URL.Query().Get("service")
		from := r.URL.Query().Get("from")
		if from == "" {
			from = "24 hours"
		}
		stats, err := sqlObs.QueryFlow(r.Context(), svc, from)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats) //nolint:errcheck
	})

	gate("GET /api/obs/summary", func(w http.ResponseWriter, r *http.Request) {
		svc := r.URL.Query().Get("service")
		from := r.URL.Query().Get("from")
		if from == "" {
			from = "24 hours"
		}
		stats, err := sqlObs.QuerySummary(r.Context(), svc, from)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats) //nolint:errcheck
	})
}
