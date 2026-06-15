package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/observability"
	"github.com/Vedanshu7/lens/internal/store"
)

// agentURLForPeer resolves the HTTP agent base URL for the given svc/instance pair.
// Returns this sidecar's own URL when the pair matches self. Returns an error when
// the instance is not present in the peer map.
func (a *Agent) agentURLForPeer(svc, instance string) (string, error) {
	if svc == a.Info.Service && instance == a.Info.Instance {
		return a.selfURL(), nil
	}
	var result string
	var err error
	if v, ok := a.peers.Load(instance); ok {
		si := v.(discovery.ServiceInstance)
		if si.Service == svc {
			result = si.AgentURL
		} else {
			err = fmt.Errorf("instance not found: %s/%s", svc, instance)
		}
	} else {
		err = fmt.Errorf("instance not found: %s/%s", svc, instance)
	}
	return result, err
}

// validName matches strings that are safe to use as service or instance identifiers.
var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateName returns an error when v is empty or contains characters outside
// letters, digits, underscore, and hyphen.
func validateName(v string) error {
	var err error
	if v == "" || !validName.MatchString(v) {
		err = fmt.Errorf("name must be non-empty and contain only letters, digits, _ or -")
	}
	return err
}

// responseRecorder captures the HTTP status code written by a handler.
type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}

// trackRequest wraps next to record the HTTP status code in Prometheus after the call.
func (a *Agent) trackRequest(endpoint string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rr := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next(rr, r)
		a.Metrics.httpRequests.WithLabelValues(endpoint, strconv.Itoa(rr.status)).Inc()
	}
}

// authenticate rejects requests whose x-lens-token header does not match the
// configured token. When no token is configured all requests are allowed through.
func (a *Agent) authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.Config.Token != "" && r.Header.Get("x-lens-token") != a.Config.Token {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"}) //nolint:errcheck
			return
		}
		next(w, r)
	}
}

// requireReady rejects requests with 503 when the agent has not yet connected
// to its target service.
func (a *Agent) requireReady(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.ready() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "agent not connected to target yet"}) //nolint:errcheck
			return
		}
		next(w, r)
	}
}

// Routes returns the HTTP mux for the Lens agent.
func (a *Agent) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", a.trackRequest("health", a.handleHealth))
	// /api/declare is authenticated but not gated by requireReady — it is intentionally
	// callable during startup before the agent has fully connected to its target.
	mux.HandleFunc("POST /api/declare", a.trackRequest("declare", a.authenticate(a.handleDeclare)))
	mux.Handle("GET /metrics", promhttp.Handler())

	gate := func(pattern string, h http.HandlerFunc) {
		name := strings.TrimPrefix(strings.Fields(pattern)[1], "/api/")
		mux.HandleFunc(pattern, a.trackRequest(name, a.authenticate(a.requireReady(h))))
	}
	gate("GET /api/services", a.handleServices)
	gate("GET /api/nodes", a.handleNodes)
	gate("GET /api/keys", a.handleKeys)
	gate("GET /api/providers", a.handleProviders)
	gate("POST /api/fetch", a.handleFetch)
	gate("POST /api/invalidate", a.handleInvalidate)
	gate("GET /api/audit", a.handleAudit)

	a.registerObsRoutes(mux)

	return mux
}

// DeclareRequest is sent by the target service to register a cache key schema.
type DeclareRequest struct {
	KeyName      string           `json:"keyName"`
	KeySchema    *json.RawMessage `json:"keySchema"`
	TTLInSeconds int              `json:"ttlInSeconds,omitempty"`
}

func (a *Agent) handleDeclare(w http.ResponseWriter, r *http.Request) {
	if !a.ready() {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req DeclareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	val, _ := json.Marshal(map[string]any{
		"keySchema":    req.KeySchema,
		"ttlInSeconds": req.TTLInSeconds,
		"registeredAt": time.Now().UTC().Format(time.RFC3339),
	})

	ctx := r.Context()
	pipe := a.store.Pipeline()
	pipe.HSet(ctx, a.cacheKey(), req.KeyName, string(val))
	pipe.Expire(ctx, a.cacheKey(), 3*24*time.Hour)
	if err := pipe.Exec(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *Agent) handleServices(w http.ResponseWriter, _ *http.Request) {
	services := a.listServices()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"services": services}) //nolint:errcheck
}

func (a *Agent) listServices() []string {
	return a.allServices()
}

// InstanceInfo describes a live instance of a service as returned by /api/nodes.
type InstanceInfo struct {
	Instance string `json:"instance"`
	AgentURL string `json:"agentUrl"`
}

func (a *Agent) handleNodes(w http.ResponseWriter, r *http.Request) {
	svc := r.URL.Query().Get("service")
	if err := validateName(svc); err != nil {
		http.Error(w, "service: "+err.Error(), http.StatusBadRequest)
		return
	}

	nodes := a.listNodes(svc)
	a.Metrics.instancesActive.WithLabelValues(svc).Set(float64(len(nodes)))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"instances": nodes}) //nolint:errcheck
}

func (a *Agent) listNodes(svc string) []InstanceInfo {
	nodes := make([]InstanceInfo, 0)
	if a.Info.Service == svc {
		nodes = append(nodes, InstanceInfo{Instance: a.Info.Instance, AgentURL: a.selfURL()})
	}
	a.peers.Range(func(_, v any) bool {
		si := v.(discovery.ServiceInstance)
		if si.Service == svc {
			nodes = append(nodes, InstanceInfo{Instance: si.Instance, AgentURL: si.AgentURL})
		}
		return true
	})
	return nodes
}

func (a *Agent) handleKeys(w http.ResponseWriter, r *http.Request) {
	svc := r.URL.Query().Get("service")
	if err := validateName(svc); err != nil {
		http.Error(w, "service: "+err.Error(), http.StatusBadRequest)
		return
	}
	instance := r.URL.Query().Get("instance")
	pattern := r.URL.Query().Get("pattern")
	limit := r.URL.Query().Get("limit")
	offset := r.URL.Query().Get("offset")

	ctx := r.Context()

	if instance != "" {
		a.forwardKeys(ctx, w, r, svc, instance, pattern, limit, offset)
		return
	}

	nodes := a.listNodes(svc)
	keys := make([]string, len(nodes))
	for i, n := range nodes {
		keys[i] = store.CacheKey(svc, n.Instance)
	}
	hashes, err := a.store.HGetAllMulti(ctx, keys)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var result []map[string]any
	var failedInstances []string
	for i, n := range nodes {
		if hashes[i] == nil {
			slog.Warn("failed to load keys", "instance", n.Instance)
			failedInstances = append(failedInstances, n.Instance)
			continue
		}
		for keyName, meta := range hashes[i] {
			var m map[string]any
			json.Unmarshal([]byte(meta), &m) //nolint:errcheck
			m["keyName"] = keyName
			m["instance"] = n.Instance
			result = append(result, m)
		}
	}

	if pattern != "" {
		lp := strings.ToLower(pattern)
		filtered := result[:0]
		for _, entry := range result {
			if name, ok := entry["keyName"].(string); ok && strings.Contains(strings.ToLower(name), lp) {
				filtered = append(filtered, entry)
			}
		}
		result = filtered
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"keys":            result,
		"source":          "registry",
		"failedInstances": failedInstances,
	})
}

func (a *Agent) forwardKeys(ctx context.Context, w http.ResponseWriter, r *http.Request, svc, instance, pattern, limit, offset string) {
	q := url.Values{}
	if pattern != "" {
		q.Set("pattern", pattern)
	}
	if limit != "" {
		q.Set("limit", limit)
	}
	if offset != "" {
		q.Set("offset", offset)
	}
	if instance == a.Info.Instance && svc == a.Info.Service {
		body, err := a.targetClient.Keys(ctx, pattern, limit, offset)
		if err != nil {
			http.Error(w, fmt.Sprintf("target unreachable: %v", err), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
		return
	}

	if r.Header.Get("X-Lens-Proxied") != "" {
		http.Error(w, "proxy loop detected", http.StatusLoopDetected)
		return
	}

	agentURL, err := a.agentURLForPeer(svc, instance)
	if err != nil {
		http.Error(w, "instance not found", http.StatusNotFound)
		return
	}

	proxyReq, _ := http.NewRequestWithContext(ctx, "GET", agentURL+"/api/keys?"+r.URL.RawQuery, nil)
	proxyReq.Header.Set("X-Lens-Proxied", "true")
	if a.Config.Token != "" {
		proxyReq.Header.Set("x-lens-token", a.Config.Token)
	}
	resp, err := a.ProxyHTTP.Do(proxyReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("agent unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer func() {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()              //nolint:errcheck
	}()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}

// FetchRequest queries a specific instance for a cache key's current value.
type FetchRequest struct {
	Service  string `json:"service"`
	Instance string `json:"instance"`
	Key      string `json:"key"`
}

func (a *Agent) handleFetch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req FetchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	if req.Instance == a.Info.Instance && req.Service == a.Info.Service {
		a.Metrics.fetchTotal.WithLabelValues(req.Service, "local").Inc()
		a.fetchFromTarget(ctx, w, req.Key)
		return
	}

	if r.Header.Get("X-Lens-Proxied") != "" {
		http.Error(w, "proxy loop detected", http.StatusLoopDetected)
		return
	}

	fetchCtx, fetchCancel := context.WithTimeout(ctx, 3*time.Second)
	defer fetchCancel()

	fetchStart := time.Now()
	data, err := a.transport.Get(fetchCtx, req.Service, req.Instance, req.Key)
	fetchMs := float64(time.Since(fetchStart).Milliseconds())
	if err != nil {
		a.Obs.Record(ctx, observability.Event{ //nolint:errcheck
			Service: req.Service, Instance: a.Info.Instance,
			Kind: observability.EventFetch, Transport: a.Config.Transport,
			Success: false, Error: err.Error(), LatencyMs: fetchMs,
			Key: &req.Key,
		})
		http.Error(w, fmt.Sprintf("instance unreachable: %v", err), http.StatusBadGateway)
		return
	}
	a.Metrics.fetchTotal.WithLabelValues(req.Service, a.Config.Transport).Inc()
	a.Obs.Record(ctx, observability.Event{ //nolint:errcheck
		Service: req.Service, Instance: a.Info.Instance,
		Kind: observability.EventFetch, Transport: a.Config.Transport,
		Success: true, LatencyMs: fetchMs, Key: &req.Key,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint:errcheck
}

func (a *Agent) fetchFromTarget(ctx context.Context, w http.ResponseWriter, key string) {
	body, err := a.targetClient.Get(ctx, key)
	if err != nil {
		http.Error(w, fmt.Sprintf("target unreachable: %v", err), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body) //nolint:errcheck
}

// InvalidateRequest triggers cache invalidation across all instances of a service.
type InvalidateRequest struct {
	Service string  `json:"service"`
	Pattern *string `json:"pattern"`
}

// InstanceResult is the per-instance outcome of an invalidation broadcast.
type InstanceResult struct {
	Instance string `json:"instance"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

func (a *Agent) handleInvalidate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req InvalidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateName(req.Service); err != nil {
		http.Error(w, "service: "+err.Error(), http.StatusBadRequest)
		return
	}

	if ok, wait := a.Throttle.Allow(req.Service); !ok {
		w.Header().Set("Retry-After", fmt.Sprintf("%.0f", wait.Seconds()))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"error":      "rate limited",
			"retryAfter": wait.Milliseconds(),
		})
		return
	}

	ctx := r.Context()
	start := time.Now()
	payload, _ := json.Marshal(map[string]any{"pattern": req.Pattern})

	nodes := a.listNodes(req.Service)
	total := len(nodes)
	a.Metrics.instancesActive.WithLabelValues(req.Service).Set(float64(total))

	localCh := make(chan InstanceResult, 1)
	if req.Service == a.Info.Service {
		go func() {
			if err := a.targetClient.Invalidate(ctx, payload); err != nil {
				slog.Warn("local invalidation failed", "err", err)
				localCh <- InstanceResult{Instance: a.Info.Instance, Success: false, Error: err.Error()}
				return
			}
			localCh <- InstanceResult{Instance: a.Info.Instance, Success: true}
		}()
	}

	acks, err := a.transport.Broadcast(ctx, req.Service, payload)
	if err != nil {
		slog.Error("broadcast failed", "err", err)
		http.Error(w, fmt.Sprintf("broadcast failed: %v", err), http.StatusInternalServerError)
		return
	}

	var results []InstanceResult
	for _, ack := range acks {
		results = append(results, InstanceResult{
			Instance: ack.Instance,
			Success:  ack.Success,
			Error:    ack.Error,
		})
	}

	if req.Service == a.Info.Service {
		select {
		case res := <-localCh:
			results = append(results, res)
		case <-time.After(200 * time.Millisecond):
			results = append(results, InstanceResult{Instance: a.Info.Instance, Success: false, Error: "local call timed out"})
		}
	}

	responded := map[string]bool{}
	for _, res := range results {
		responded[res.Instance] = true
	}
	for _, n := range nodes {
		if !responded[n.Instance] {
			results = append(results, InstanceResult{Instance: n.Instance, Success: false, Error: "no response (timeout)"})
		}
	}

	confirmed := 0
	for _, res := range results {
		if res.Success {
			confirmed++
		}
	}

	elapsed := time.Since(start)
	elapsedMs := float64(elapsed.Milliseconds())
	a.Metrics.invalidateDuration.WithLabelValues(req.Service).Observe(elapsed.Seconds())
	status := "success"
	if confirmed < total {
		status = "partial"
	}
	if confirmed == 0 && total > 0 {
		status = "failure"
	}
	a.Metrics.invalidateTotal.WithLabelValues(req.Service, status).Inc()
	a.Obs.Record(ctx, observability.Event{ //nolint:errcheck
		Service: req.Service, Instance: a.Info.Instance,
		Kind: observability.EventInvalidate, Transport: a.Config.Transport,
		Success: confirmed == total, LatencyMs: elapsedMs,
		Confirmed: confirmed, Total: total, Pattern: req.Pattern,
	})
	for _, res := range results {
		if !res.Success && res.Error == "no response (timeout)" {
			a.Obs.Record(ctx, observability.Event{ //nolint:errcheck
				Service: req.Service, Instance: a.Info.Instance,
				Kind: observability.EventDeadPod, Transport: a.Config.Transport,
				Success: false, PeerID: res.Instance,
			})
		}
	}

	auditEntry, _ := json.Marshal(map[string]any{
		"ts":        time.Now().UTC().Format(time.RFC3339),
		"action":    "invalidate",
		"service":   req.Service,
		"pattern":   req.Pattern,
		"initiator": a.Info.Instance,
		"confirmed": confirmed,
		"total":     total,
	})
	pipe := a.store.Pipeline()
	pipe.LPush(ctx, store.AuditKey(), string(auditEntry))
	pipe.LTrim(ctx, store.AuditKey(), 0, 499)
	pipe.Expire(ctx, store.AuditKey(), 7*24*time.Hour)
	pipe.Exec(ctx) //nolint:errcheck

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"service":   req.Service,
		"total":     total,
		"confirmed": confirmed,
		"instances": results,
	})
}

func (a *Agent) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := int64(50)
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if n, err := strconv.ParseInt(ls, 10, 64); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	entries, err := a.store.LRange(r.Context(), store.AuditKey(), 0, limit-1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]json.RawMessage, 0, len(entries))
	for _, e := range entries {
		out = append(out, json.RawMessage(e))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"entries": out, "count": len(out)}) //nolint:errcheck
}

func (a *Agent) selfProviders() map[string]any {
	obs := make([]string, 0, len(a.Config.ObserverProviders))
	for _, p := range a.Config.ObserverProviders {
		obs = append(obs, p.Name)
	}
	return map[string]any{
		"transport":   a.Config.Transport,
		"persistence": a.Config.Persistence,
		"discovery":   a.Config.Discovery,
		"target":      a.Config.Target,
		"observers":   obs,
	}
}

func (a *Agent) handleHealth(w http.ResponseWriter, r *http.Request) {
	storeOK := a.store.Ping(r.Context()) == nil
	targetOK := a.ready()
	_, hasObs := a.Obs.SQLObserver()
	w.Header().Set("Content-Type", "application/json")
	if !storeOK || !targetOK {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"redis":         storeOK,
		"target":        targetOK,
		"observability": hasObs,
		"providers":     a.selfProviders(),
	})
}

// handleProviders returns the provider stack (transport/persistence/discovery/observers)
// for any service. Self is read from config directly; other services are read from
// the Redis key written by that service's sidecar on connect.
func (a *Agent) handleProviders(w http.ResponseWriter, r *http.Request) {
	svc := r.URL.Query().Get("service")
	if err := validateName(svc); err != nil {
		http.Error(w, "service: "+err.Error(), http.StatusBadRequest)
		return
	}

	if svc == a.Info.Service {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(a.selfProviders()) //nolint:errcheck
		return
	}

	// Other services write their provider stack to Redis on connect.
	raw, err := a.store.Get(r.Context(), store.ProvidersKey(svc))
	if err != nil || raw == "" {
		http.Error(w, "providers not found for service: "+svc, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(raw)) //nolint:errcheck
}
