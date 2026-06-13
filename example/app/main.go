// Package main is a minimal demo service that implements the three Lens sidecar
// endpoints (info, invalidate, get) alongside a simple in-memory cache.
// It is used to demonstrate Lens locally and on EKS without requiring any
// real cache infrastructure.
package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
)

func main() {
	port := envOr("APP_PORT", "8080")
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	app := &demoApp{
		service:  envOr("APP_SERVICE", "demo"),
		instance: envOr("APP_INSTANCE", hostname()),
		lensURL:  envOr("LENS_URL", "http://localhost:8900"),
	}

	mux := http.NewServeMux()

	// Public demo API
	mux.HandleFunc("POST /api/cache", app.handleWrite)
	mux.HandleFunc("GET /api/cache/{key}", app.handleRead)
	mux.HandleFunc("DELETE /api/cache", app.handleInvalidateAll)
	mux.HandleFunc("GET /api/cache", app.handleList)

	// Lens sidecar protocol endpoints
	mux.HandleFunc("GET /internal/lens/info", app.handleInfo)
	mux.HandleFunc("POST /internal/lens/invalidate", app.handleLensInvalidate)
	mux.HandleFunc("POST /internal/lens/get", app.handleLensGet)

	slog.Info("demo app listening", "port", port, "service", app.service, "instance", app.instance)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

type demoApp struct {
	service  string
	instance string
	lensURL  string
	cache    sync.Map
}

// --- Public API ---

// handleWrite stores a key/value pair in the local cache.
func (a *demoApp) handleWrite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		http.Error(w, "body must be {key, value}", http.StatusBadRequest)
		return
	}
	a.cache.Store(body.Key, body.Value)
	slog.Info("cache write", "key", body.Key)
	w.WriteHeader(http.StatusNoContent)
}

// handleRead returns the cached value for key, or 404 if absent.
func (a *demoApp) handleRead(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	val, ok := a.cache.Load(key)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	slog.Info("cache read", "key", key)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"key": key, "value": val.(string)}) //nolint:errcheck
}

// handleList returns all keys currently in the cache.
func (a *demoApp) handleList(w http.ResponseWriter, r *http.Request) {
	var keys []string
	a.cache.Range(func(k, _ any) bool {
		keys = append(keys, k.(string))
		return true
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"keys": keys, "instance": a.instance}) //nolint:errcheck
}

// handleInvalidateAll asks the local Lens sidecar to broadcast an invalidation
// to all instances of this service.
func (a *demoApp) handleInvalidateAll(w http.ResponseWriter, r *http.Request) {
	payload, _ := json.Marshal(map[string]any{
		"service": a.service,
		"pattern": nil,
	})
	resp, err := http.Post(a.lensURL+"/api/invalidate", "application/json", bytes.NewReader(payload))
	if err != nil {
		slog.Error("lens broadcast failed", "err", err)
		http.Error(w, "broadcast failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	slog.Info("broadcast sent", "status", resp.StatusCode)
	w.WriteHeader(http.StatusNoContent)
}

// --- Lens sidecar protocol ---

// handleInfo returns the service name and instance ID so the sidecar can
// register this instance in the discovery backend.
func (a *demoApp) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"service":  a.service,
		"instance": a.instance,
	})
}

// handleLensInvalidate receives a broadcast from the sidecar and clears all
// cache keys whose names contain the given pattern. An empty or absent pattern
// clears the entire cache.
func (a *demoApp) handleLensInvalidate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pattern *string `json:"pattern"`
	}
	json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck

	pattern := ""
	if body.Pattern != nil {
		pattern = *body.Pattern
	}

	cleared := 0
	a.cache.Range(func(k, _ any) bool {
		if pattern == "" || strings.Contains(k.(string), pattern) {
			a.cache.Delete(k)
			cleared++
		}
		return true
	})
	slog.Info("cache invalidated", "pattern", pattern, "cleared", cleared)
	w.WriteHeader(http.StatusOK)
}

// handleLensGet returns a cached value by key so peer sidecars can fetch entries
// from this instance during cross-pod cache lookups.
func (a *demoApp) handleLensGet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		http.Error(w, "body must be {key}", http.StatusBadRequest)
		return
	}
	val, ok := a.cache.Load(body.Key)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"key": body.Key, "value": val.(string)}) //nolint:errcheck
}

// --- Helpers ---

func hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		return "unknown"
	}
	return h
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
