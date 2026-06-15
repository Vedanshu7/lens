package agent

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/Vedanshu7/lens/config"
	"github.com/Vedanshu7/lens/internal/observability"
)

// FindConfigPath returns the first config path from the standard search list
// that exists on disk, or "" if none is found.
func FindConfigPath() string {
	for _, p := range configPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// WatchConfig watches the config file at path for changes and hot-applies
// reloadable fields: logLevel, cooldownMs, and per-service cooldowns.
// Changes to transport, persistence, discovery, or target provider are logged
// as warnings and skipped — a process restart is required for those fields.
// Changes are debounced by 200ms to avoid reload storms on editor saves.
// WatchConfig blocks until ctx is cancelled.
func (a *Agent) WatchConfig(ctx context.Context, path string) {
	// Capture provider names from the startup config for change detection.
	// a.Config is never written after New() returns, so these reads are race-free.
	origTransport := a.Config.Transport
	origPersistence := a.Config.Persistence
	origDiscovery := a.Config.Discovery
	origTarget := a.Config.Target

	w, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("hot-reload: could not create watcher", "err", err)
		return
	}
	defer w.Close() //nolint:errcheck

	if err := w.Add(path); err != nil {
		slog.Warn("hot-reload: could not watch config file", "path", path, "err", err)
		return
	}

	apply := func() {
		f, err := config.Load(path)
		if err != nil {
			slog.Warn("hot-reload: could not parse config", "path", path, "err", err)
			return
		}

		// Non-reloadable: warn if changed, skip.
		if n := f.Transport.ProviderName(); n != "" && n != origTransport {
			slog.Warn("hot-reload: transport provider change requires restart",
				"current", origTransport, "new", n)
		}
		if n := f.Persistence.ProviderName(); n != "" && n != origPersistence {
			slog.Warn("hot-reload: persistence provider change requires restart",
				"current", origPersistence, "new", n)
		}
		if n := f.Discovery.ProviderName(); n != "" && n != origDiscovery {
			slog.Warn("hot-reload: discovery provider change requires restart",
				"current", origDiscovery, "new", n)
		}
		if n := f.Target.ProviderName(); n != "" && n != origTarget {
			slog.Warn("hot-reload: target provider change requires restart",
				"current", origTarget, "new", n)
		}

		// logLevel: rebuild the default logger with the new level.
		if f.Agent.LogLevel != "" {
			lvl := ParseLogLevel(f.Agent.LogLevel)
			slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
		}

		// cooldownMs: update the Throttle default (thread-safe via Throttle.mu).
		if f.Agent.CooldownMs != 0 {
			a.Throttle.SetDefaultCooldown(f.Agent.CooldownMs)
		}

		// per-service cooldowns.
		for svc, ms := range f.Agent.Cooldowns {
			a.Throttle.SetServiceCooldown(svc, ms)
		}

		a.Obs.Record(ctx, observability.Event{ //nolint:errcheck
			Kind:     observability.EventConfigReload,
			Service:  a.Info.Service,
			Instance: a.Info.Instance,
			Success:  true,
		})
		slog.Info("hot-reload: config reloaded", "path", path)
	}

	var debounce *time.Timer
	for {
		select {
		case <-ctx.Done():
			if debounce != nil {
				debounce.Stop()
			}
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Has(fsnotify.Write) || ev.Has(fsnotify.Create) {
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(200*time.Millisecond, apply)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			slog.Warn("hot-reload: watcher error", "err", err)
		}
	}
}
