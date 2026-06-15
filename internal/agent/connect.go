package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/store"
	"github.com/Vedanshu7/lens/internal/target"
	itransport "github.com/Vedanshu7/lens/internal/transport"
)

// Connect retries dialing the target service in a loop until ctx is cancelled.
// After a successful dial it blocks until the reconnect channel fires, then
// waits 5 seconds before attempting to reconnect.
func (a *Agent) Connect(ctx context.Context) {
	go a.evictThrottle(ctx)
	go a.evictIPLimiter(ctx)
	for {
		if err := a.dial(ctx); err != nil {
			slog.Warn("waiting for target", "err", err, "retryIn", "10s")
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
				continue
			}
		}

		<-a.reconnectCh
		if ctx.Err() != nil {
			return
		}

		slog.Warn("connection lost, reconnecting")
		a.live.Store(false)
		a.cancelDial()
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// dial verifies persistence connectivity, resolves the target service identity,
// and lazily initialises transport and discovery on the first call. Subsequent
// calls after a reconnect reuse the existing providers because gRPC and gossip
// connections are independent of the target HTTP connection.
func (a *Agent) dial(ctx context.Context) error {
	// Cancel any context from a previous dial session, then create a fresh one.
	// dialCtx is cancelled by cancelDial() when the connection is lost or on shutdown,
	// aborting any in-progress operations (ping, info fetch, replay).
	a.cancelDial()
	dialCtx, cancel := context.WithCancel(ctx)
	a.dialCancel = cancel

	if err := a.store.Ping(dialCtx); err != nil {
		return fmt.Errorf("persistence: %w", err)
	}

	info, err := a.fetchTargetInfo(dialCtx)
	if err != nil {
		return fmt.Errorf("target info: %w", err)
	}
	a.Info = info
	slog.Info("connected to target", "service", info.Service, "instance", info.Instance)

	// Publish provider stack and service name to Redis so any peer (even on a
	// different transport/gossip cluster) can discover this service and its stack.
	if provJSON, err := json.Marshal(a.selfProviders()); err == nil {
		a.store.Set(dialCtx, store.ProvidersKey(info.Service), string(provJSON), 24*time.Hour) //nolint:errcheck
	}
	a.store.SAdd(dialCtx, store.ServicesSetKey(), info.Service) //nolint:errcheck

	if a.transport == nil {
		t, err := itransport.New(a, a.Config.Transport, a.Config.TransportConfig)
		if err != nil {
			return fmt.Errorf("transport: %w", err)
		}
		a.transport = t
	}

	if a.disc == nil {
		disc, err := discovery.New(a.store, a.Config.Discovery, a.Config.DiscoveryConfig)
		if err != nil {
			return fmt.Errorf("discovery: %w", err)
		}
		a.disc = disc

		grpcPort, _ := a.Config.TransportConfig["grpcPort"].(string)
		if grpcPort == "" {
			grpcPort = "8901"
		}
		self := discovery.ServiceInstance{
			Service:  a.Info.Service,
			Instance: a.Info.Instance,
			GRPCAddr: a.Config.AdvertiseAddr + ":" + grpcPort,
			AgentURL: a.selfURL(),
		}
		if err := disc.Register(dialCtx, self); err != nil {
			return fmt.Errorf("discovery register: %w", err)
		}

		eventCh, err := disc.Watch(dialCtx)
		if err != nil {
			return fmt.Errorf("discovery watch: %w", err)
		}
		go a.watchPeers(eventCh)
	}

	if a.Config.ReplayEnabled {
		if err := a.replayMissed(dialCtx); err != nil {
			slog.Warn("replay failed", "err", err)
		}
	}

	a.reconnectCh = make(chan struct{}, 1)
	a.live.Store(true)
	return nil
}

// Dial verifies persistence, resolves target identity, and marks the agent live.
// Exported so integration tests can drive the connection lifecycle directly.
func (a *Agent) Dial(ctx context.Context) error { return a.dial(ctx) }

// evictThrottle runs until ctx is cancelled, evicting stale Throttle entries every minute.
func (a *Agent) evictThrottle(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.Throttle.Evict()
		}
	}
}

// evictIPLimiter runs until ctx is cancelled, evicting idle IP limiter entries every 5 minutes.
func (a *Agent) evictIPLimiter(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.rateLim.evict()
		}
	}
}

// fetchTargetInfo calls the target client's Info method and returns the decoded identity.
func (a *Agent) fetchTargetInfo(ctx context.Context) (target.TargetInfo, error) {
	return a.targetClient.Info(ctx)
}

// deregister writes a checkpoint timestamp and removes this instance from discovery.
// Called during graceful shutdown so peers stop routing to this instance.
func (a *Agent) deregister(ctx context.Context) {
	if a.disc != nil {
		a.disc.Deregister(ctx, discovery.ServiceInstance{ //nolint:errcheck
			Service:  a.Info.Service,
			Instance: a.Info.Instance,
		})
	}
	err := a.store.Set(ctx,
		store.CheckpointKey(a.Info.Service, a.Info.Instance),
		time.Now().UTC().Format(time.RFC3339),
		24*time.Hour,
	)
	if err != nil {
		slog.Error("deregister checkpoint failed", "err", err)
	}
	slog.Info("deregistered", "service", a.Info.Service, "instance", a.Info.Instance)
}

// replayMissed reads the replay log from persistence and applies any invalidations
// that arrived after the last recorded checkpoint and within the replay window.
func (a *Agent) replayMissed(ctx context.Context) error {
	checkpoint, err := a.store.Get(ctx, store.CheckpointKey(a.Info.Service, a.Info.Instance))
	if err != nil {
		return fmt.Errorf("read checkpoint: %w", err)
	}
	if checkpoint == "" {
		return nil
	}
	lastSeen, err := time.Parse(time.RFC3339, checkpoint)
	if err != nil {
		return fmt.Errorf("parse checkpoint: %w", err)
	}

	entries, err := a.store.LRange(ctx, store.LogKey(a.Info.Service), 0, -1)
	if err != nil {
		return fmt.Errorf("read log: %w", err)
	}

	cutoff := time.Now().UTC().Add(-time.Duration(a.Config.ReplayWindowHours) * time.Hour)
	applied := 0
	for _, raw := range entries {
		var e struct {
			Payload json.RawMessage `json:"payload"`
			Ts      string          `json:"ts"`
		}
		if json.Unmarshal([]byte(raw), &e) != nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339, e.Ts)
		if err != nil || !ts.After(lastSeen) || ts.Before(cutoff) {
			continue
		}
		if err := a.targetClient.Invalidate(ctx, e.Payload); err != nil {
			slog.Warn("replay invalidation failed", "err", err)
			continue
		}
		applied++
		a.Metrics.replayApplied.WithLabelValues(a.Info.Service).Inc()
	}

	if applied > 0 {
		slog.Info("replay complete", "service", a.Info.Service, "applied", applied)
	}
	return nil
}
