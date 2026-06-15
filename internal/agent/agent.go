// Package agent implements the Lens cache-visibility sidecar.
// It manages the connection to the target service, coordinates peer discovery,
// routes invalidation and fetch requests, and emits structured telemetry events.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Vedanshu7/lens/config"
	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/observability"
	"github.com/Vedanshu7/lens/internal/persistence"
	"github.com/Vedanshu7/lens/internal/store"
	"github.com/Vedanshu7/lens/internal/target"
	"github.com/Vedanshu7/lens/internal/transport"
)

// Config holds all runtime configuration for the Lens sidecar.
// It is populated from lens.yaml when present; LENS_* env vars serve as
// fallbacks for any field not set in the file. Secrets (token, passwords)
// are always sourced from env vars even when a config file is present.
type Config struct {
	// TargetURL is the base HTTP URL of the service this sidecar is attached to.
	TargetURL string
	// Port is the HTTP port the sidecar listens on.
	Port string
	// BindAddr is the local address the HTTP server binds to.
	BindAddr string
	// AdvertiseAddr is the IP peers use to reach this pod.
	AdvertiseAddr string
	// Token is the shared secret for request authentication. Empty disables auth.
	Token string
	// CooldownMS is the minimum milliseconds between invalidations for the same service.
	CooldownMS int
	// Cooldowns overrides CooldownMS for specific services (service name → cooldown ms).
	Cooldowns map[string]int
	// BatchWindowMS is the debounce window for coalescing invalidations (ms). 0 disables batching.
	BatchWindowMS int
	// RateLimitRPS is the per-IP request rate limit (requests per second). 0 disables limiting.
	RateLimitRPS int
	// RateLimitBurst is the per-IP token-bucket burst size.
	RateLimitBurst int
	// ReplayEnabled controls whether missed invalidations are replayed on startup.
	ReplayEnabled bool
	// ReplayWindowHours limits how far back the replay log is scanned.
	ReplayWindowHours int
	// LogLevel is the minimum log level ("debug", "info", "warn", "error").
	LogLevel string

	// Transport names the active transport provider (e.g. "grpc", "nats", "kafka").
	Transport string
	// TransportConfig is passed verbatim to the transport factory.
	TransportConfig map[string]any

	// Persistence names the active persistence provider (e.g. "redis", "memory").
	Persistence string
	// PersistenceConfig is passed verbatim to the persistence factory.
	PersistenceConfig map[string]any

	// Discovery names the active discovery provider (e.g. "memberlist", "static", "dnssrv").
	Discovery string
	// DiscoveryConfig is passed verbatim to the discovery factory.
	DiscoveryConfig map[string]any

	// Target names the active target provider (e.g. "http", "unix", "grpc").
	Target string
	// TargetConfig is passed verbatim to the target provider factory.
	TargetConfig map[string]any

	// ObserverEnabled controls whether the observability subsystem is active.
	ObserverEnabled bool
	// ObserverProviders lists the observability providers and their configs.
	ObserverProviders []ObserverProviderConfig
}

// ObserverProviderConfig names an observability provider and its configuration.
type ObserverProviderConfig struct {
	Name   string
	Config map[string]any
}

// configPaths lists the locations searched for lens.yaml in order.
var configPaths = []string{"./lens.yaml", "/etc/lens/lens.yaml"}

// LoadConfig builds a Config by first applying LENS_* env var defaults, then
// overlaying any lens.yaml found in the current directory or /etc/lens/.
func LoadConfig() Config {
	db, _ := strconv.Atoi(envOr("LENS_REDIS_DB", "0"))
	cooldown, _ := strconv.Atoi(envOr("LENS_COOLDOWN_MS", "1000"))
	batchWindow, _ := strconv.Atoi(envOr("LENS_BATCH_WINDOW_MS", "0"))
	replayHours, _ := strconv.Atoi(envOr("LENS_REPLAY_WINDOW_HOURS", "24"))
	gossipPort, _ := strconv.Atoi(envOr("LENS_GOSSIP_PORT", "7946"))
	rateLimitRPS, _ := strconv.Atoi(envOr("LENS_RATE_LIMIT_RPS", "100"))
	rateLimitBurst, _ := strconv.Atoi(envOr("LENS_RATE_LIMIT_BURST", "200"))

	cfg := Config{
		TargetURL:         envOr("LENS_TARGET_URL", "http://localhost:8080"),
		Port:              envOr("LENS_PORT", "8900"),
		BindAddr:          envOr("LENS_BIND_ADDR", "127.0.0.1"),
		AdvertiseAddr:     envOr("LENS_ADVERTISE_ADDR", detectAdvertiseAddr()),
		Token:             os.Getenv("LENS_TOKEN"),
		CooldownMS:        cooldown,
		BatchWindowMS:     batchWindow,
		RateLimitRPS:      rateLimitRPS,
		RateLimitBurst:    rateLimitBurst,
		ReplayEnabled:     envOr("LENS_REPLAY_ENABLED", "true") != "false",
		ReplayWindowHours: replayHours,
		LogLevel:          envOr("LENS_LOG_LEVEL", "info"),
		Transport:         envOr("LENS_TRANSPORT", ""),
		Persistence:       envOr("LENS_PERSISTENCE", "redis"),
		Discovery:         envOr("LENS_DISCOVERY", ""),
		Target:            envOr("LENS_TARGET_PROVIDER", "http"),
		TransportConfig:   map[string]any{"grpcPort": envOr("LENS_GRPC_PORT", "8901"), "natsUrl": envOr("LENS_NATS_URL", "nats://localhost:4222")},
		PersistenceConfig: map[string]any{"addr": envOr("LENS_REDIS_ADDR", "localhost:6379"), "db": db},
		DiscoveryConfig:   map[string]any{"bindPort": gossipPort},
		TargetConfig: map[string]any{
			"targetURL":  envOr("LENS_TARGET_URL", "http://localhost:8080"),
			"socketPath": os.Getenv("LENS_TARGET_SOCKET_PATH"),
			"grpcAddr":   envOr("LENS_TARGET_GRPC_ADDR", "localhost:8902"),
		},
	}

	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			f, err := config.Load(path)
			if err != nil {
				slog.Warn("lens: could not parse config file, using env vars", "path", path, "err", err)
				break
			}
			applyFile(&cfg, f)
			slog.Info("lens: loaded config", "path", path)
			break
		}
	}

	// Token is always env-only — inject after applyFile so YAML cannot override it.
	cfg.TargetConfig["token"] = cfg.Token

	return cfg
}

// applyFile overlays the parsed YAML file onto cfg. YAML wins for every field
// it sets explicitly; env var values already in cfg remain for unset fields.
// Token is always sourced from env to keep secrets out of the config file.
func applyFile(cfg *Config, f config.File) {
	if f.Agent.TargetURL != "" {
		cfg.TargetURL = f.Agent.TargetURL
	}
	if f.Agent.Port != "" {
		cfg.Port = f.Agent.Port
	}
	if f.Agent.BindAddr != "" {
		cfg.BindAddr = f.Agent.BindAddr
	}
	if f.Agent.AdvertiseAddr != "" {
		cfg.AdvertiseAddr = f.Agent.AdvertiseAddr
	}
	if f.Agent.CooldownMs != 0 {
		cfg.CooldownMS = f.Agent.CooldownMs
	}
	if len(f.Agent.Cooldowns) > 0 {
		cfg.Cooldowns = f.Agent.Cooldowns
	}
	if f.Agent.LogLevel != "" {
		cfg.LogLevel = f.Agent.LogLevel
	}
	if f.Agent.Replay.WindowHours != 0 {
		cfg.ReplayWindowHours = f.Agent.Replay.WindowHours
	}
	cfg.ReplayEnabled = f.Agent.Replay.Enabled

	if n := f.Transport.ProviderName(); n != "" {
		cfg.Transport = n
	}
	if len(f.Transport.Config) > 0 {
		cfg.TransportConfig = f.Transport.Config
	}

	if n := f.Persistence.ProviderName(); n != "" {
		cfg.Persistence = n
	}
	if len(f.Persistence.Config) > 0 {
		cfg.PersistenceConfig = f.Persistence.Config
	}

	if n := f.Discovery.ProviderName(); n != "" {
		cfg.Discovery = n
	}
	if len(f.Discovery.Config) > 0 {
		cfg.DiscoveryConfig = f.Discovery.Config
	}

	if n := f.Target.ProviderName(); n != "" {
		cfg.Target = n
	}
	if len(f.Target.Config) > 0 {
		cfg.TargetConfig = f.Target.Config
	} else if f.Agent.TargetURL != "" && (f.Target.ProviderName() == "" || f.Target.ProviderName() == "http") {
		// Backward compat: agent.targetURL without a target block still configures the http provider.
		cfg.TargetConfig["targetURL"] = f.Agent.TargetURL
	}

	cfg.ObserverEnabled = f.Observer.Enabled
	if len(f.Observer.Providers) > 0 {
		cfg.ObserverProviders = make([]ObserverProviderConfig, len(f.Observer.Providers))
		for i, p := range f.Observer.Providers {
			cfg.ObserverProviders[i] = ObserverProviderConfig{Name: p.ProviderName(), Config: p.Config}
		}
	}
}

// validateConfig checks that the configured provider names are registered.
// If a provider is missing, the binary was not built with it — run lens-build
// with a lens.yaml that includes the provider to get a binary that has it.
func validateConfig(cfg Config) error {
	if cfg.Transport == "" {
		return fmt.Errorf("transport provider not set: add 'transport.provider' to lens.yaml or set LENS_TRANSPORT")
	}
	if !transport.Has(cfg.Transport) {
		return fmt.Errorf("transport provider %q is not registered; rebuild with lens-build after adding it to lens.yaml", cfg.Transport)
	}
	if !persistence.Has(cfg.Persistence) {
		return fmt.Errorf("persistence provider %q is not registered; rebuild with lens-build after adding it to lens.yaml", cfg.Persistence)
	}
	if cfg.Discovery == "" {
		return fmt.Errorf("discovery provider not set: add 'discovery.provider' to lens.yaml or set LENS_DISCOVERY")
	}
	if !discovery.Has(cfg.Discovery) {
		return fmt.Errorf("discovery provider %q is not registered; rebuild with lens-build after adding it to lens.yaml", cfg.Discovery)
	}
	if !target.Has(cfg.Target) {
		return fmt.Errorf("target provider %q is not registered; rebuild with lens-build after adding it to lens.yaml", cfg.Target)
	}
	for _, p := range cfg.ObserverProviders {
		if !observability.Has(p.Name) {
			return fmt.Errorf("observer provider %q is not registered; rebuild with lens-build after adding it to lens.yaml", p.Name)
		}
	}
	return nil
}

// detectAdvertiseAddr returns this node's primary outbound IP by opening a UDP
// socket without sending any traffic. Falls back to "127.0.0.1" on error.
func detectAdvertiseAddr() string {
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close() //nolint:errcheck
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// ParseLogLevel converts a level string to the matching slog.Level.
// Unrecognised strings default to slog.LevelInfo.
func ParseLogLevel(level string) slog.Level {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return l
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Agent is the Lens cache-visibility sidecar. It is safe for concurrent use.
type Agent struct {
	// Config is the resolved runtime configuration.
	Config Config
	// Info holds the target service identity discovered on first connection.
	Info target.TargetInfo
	// ProxyHTTP is the client used for proxied requests to peer sidecars.
	ProxyHTTP *http.Client
	// Metrics holds the Prometheus instrumentation for this agent.
	Metrics *Metrics
	// Throttle enforces per-service invalidation rate limits.
	Throttle *Throttle
	// rateLim enforces per-IP and global HTTP request rate limits.
	rateLim *ipRateLimiter
	// Obs is the multi-observer fan-out for structured telemetry events.
	Obs *observability.MultiObserver

	targetClient target.TargetClient
	store        persistence.Backend
	transport    transport.Transport
	disc         discovery.Resolver

	batch *batcher
	sse   *sseHub

	peers       sync.Map
	live        atomic.Bool
	dialCancel  context.CancelFunc
	reconnectCh chan struct{}
}

// New constructs an Agent from cfg. Persistence is initialised immediately;
// transport and discovery are deferred until after the target service identity
// is known (see dial). Panics with a clear message if a required provider is
// not compiled in.
func New(cfg Config) *Agent {
	if err := validateConfig(cfg); err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	store, err := persistence.New(cfg.Persistence, cfg.PersistenceConfig)
	if err != nil {
		slog.Error("failed to init persistence", "provider", cfg.Persistence, "err", err)
		os.Exit(1)
	}

	tc, err := target.New(cfg.Target, cfg.TargetConfig)
	if err != nil {
		slog.Error("failed to init target client", "provider", cfg.Target, "err", err)
		os.Exit(1)
	}

	hub := newSSEHub()
	observers := []observability.Observer{hub}
	if cfg.ObserverEnabled {
		for _, pc := range cfg.ObserverProviders {
			o, err := observability.New(pc.Name, pc.Config)
			if err != nil {
				slog.Error("failed to init observer", "provider", pc.Name, "err", err)
				continue
			}
			observers = append(observers, o)
		}
	}

	a := &Agent{
		Config:       cfg,
		store:        store,
		targetClient: tc,
		sse:          hub,
		Obs:          observability.NewMultiObserver(observers),
		ProxyHTTP:    &http.Client{Timeout: 2 * time.Second},
		Metrics:      newMetrics(),
		Throttle:     newThrottle(cfg.CooldownMS),
		rateLim:      newIPRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst),
	}
	for svc, ms := range cfg.Cooldowns {
		a.Throttle.SetServiceCooldown(svc, ms)
	}
	if cfg.BatchWindowMS > 0 {
		a.batch = newBatcher(cfg.BatchWindowMS, a.executeBroadcast)
	}
	return a
}

// NewFromDeps constructs an Agent with pre-built dependencies injected directly.
// Use in tests or embedding scenarios where the factory registry is not needed.
// The agent is marked live immediately; no dial() call is required.
func NewFromDeps(cfg Config, store persistence.Backend, tc target.TargetClient, tr transport.Transport, disc discovery.Resolver, info target.TargetInfo) *Agent {
	hub := newSSEHub()
	a := &Agent{
		Config:       cfg,
		Info:         info,
		store:        store,
		targetClient: tc,
		transport:    tr,
		disc:         disc,
		sse:          hub,
		Obs:          observability.NewMultiObserver([]observability.Observer{hub}),
		ProxyHTTP:    &http.Client{Timeout: 2 * time.Second},
		Metrics:      newMetricsWithReg(prometheus.NewRegistry()),
		Throttle:     newThrottle(cfg.CooldownMS),
		rateLim:      newIPRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst),
		reconnectCh:  make(chan struct{}, 1),
	}
	if cfg.BatchWindowMS > 0 {
		a.batch = newBatcher(cfg.BatchWindowMS, a.executeBroadcast)
	}
	a.live.Store(true)
	return a
}

// SetReady marks the agent as live (true) or not-live (false).
// Intended for tests that need to verify not-ready behaviour.
func (a *Agent) SetReady(v bool) { a.live.Store(v) }

// ValidateConfig checks that all provider names in cfg are registered.
// Returns a descriptive error when a required provider is missing.
func ValidateConfig(cfg Config) error { return validateConfig(cfg) }

// WatchPeers consumes eventCh and keeps the in-memory peer map up to date.
// Exported so integration tests can drive peer events without a real discovery provider.
func (a *Agent) WatchPeers(eventCh <-chan discovery.Event) { a.watchPeers(eventCh) }

// Shutdown cancels in-flight connections, deregisters this instance from
// discovery, and closes all providers.
func (a *Agent) Shutdown(ctx context.Context) {
	a.cancelDial()
	if a.ready() {
		a.deregister(ctx)
	}
	if a.transport != nil {
		if err := a.transport.Close(); err != nil {
			slog.Warn("transport close", "err", err)
		}
	}
	if a.disc != nil {
		if err := a.disc.Close(); err != nil {
			slog.Warn("discovery close", "err", err)
		}
	}
	if err := a.Obs.Close(); err != nil {
		slog.Warn("observer close", "err", err)
	}
	if err := a.store.Close(); err != nil {
		slog.Warn("store close", "err", err)
	}
	if a.targetClient != nil {
		if err := a.targetClient.Close(); err != nil {
			slog.Warn("target close", "err", err)
		}
	}
}

func (a *Agent) ready() bool { return a.live.Load() }

func (a *Agent) cancelDial() {
	if a.dialCancel != nil {
		a.dialCancel()
		a.dialCancel = nil
	}
}

// SelfInstance returns the unique identifier of this sidecar's target instance.
func (a *Agent) SelfInstance() string { return a.Info.Instance }

// SelfService returns the logical service name of this sidecar's target.
func (a *Agent) SelfService() string { return a.Info.Service }

// PeersForService returns the minimal address set for all live peers of svc,
// excluding this instance.
func (a *Agent) PeersForService(svc string) []transport.PeerAddr {
	var peers []transport.PeerAddr
	a.peers.Range(func(_, v any) bool {
		si := v.(discovery.ServiceInstance)
		if si.Service == svc && si.Instance != a.Info.Instance {
			peers = append(peers, transport.PeerAddr{
				Instance: si.Instance,
				GRPCAddr: si.GRPCAddr,
			})
		}
		return true
	})
	return peers
}

// ApplyInvalidation forwards payload to the target service's invalidate endpoint.
// origin identifies the peer that initiated the invalidation.
func (a *Agent) ApplyInvalidation(ctx context.Context, payload []byte, origin string) {
	a.applyInvalidation(ctx, Message{
		Action:  "invalidate",
		Payload: payload,
		Origin:  origin,
	})
}

// WriteInvalidationLog appends payload to the replay log in persistence for svc.
func (a *Agent) WriteInvalidationLog(ctx context.Context, svc string, payload []byte) {
	a.writeInvalidationLog(ctx, svc, payload)
}

// GetFromTarget forwards a get request payload to this sidecar's own target service
// and returns the response body. payload is JSON-encoded {"key":"..."}.
func (a *Agent) GetFromTarget(ctx context.Context, payload []byte) ([]byte, error) {
	var req struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("GetFromTarget: decode payload: %w", err)
	}
	return a.targetClient.Get(ctx, req.Key)
}

// allServices returns deduplicated service names from the live peer map plus
// any services registered in the Redis services set (covers cross-transport
// services that share persistence but not a gossip cluster).
func (a *Agent) allServices() []string {
	seen := map[string]bool{}
	if a.Info.Service != "" {
		seen[a.Info.Service] = true
	}
	a.peers.Range(func(_, v any) bool {
		seen[v.(discovery.ServiceInstance).Service] = true
		return true
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if members, err := a.store.SMembers(ctx, store.ServicesSetKey()); err == nil {
		for _, svc := range members {
			if svc != "" {
				seen[svc] = true
			}
		}
	}
	svcs := make([]string, 0, len(seen))
	for s := range seen {
		svcs = append(svcs, s)
	}
	return svcs
}

// watchPeers consumes the discovery event channel and keeps the peer map in sync.
// It exits when eventCh is closed.
func (a *Agent) watchPeers(eventCh <-chan discovery.Event) {
	for ev := range eventCh {
		switch ev.Type {
		case discovery.EventJoin, discovery.EventUpdate:
			a.peers.Store(ev.Instance.Instance, ev.Instance)
			a.Obs.Record(context.Background(), observability.Event{ //nolint:errcheck
				Service:  ev.Instance.Service,
				Instance: a.Info.Instance,
				Kind:     observability.EventPeerJoin,
				PeerID:   ev.Instance.Instance,
				Success:  true,
			})
		case discovery.EventLeave:
			a.peers.Delete(ev.Instance.Instance)
			a.Obs.Record(context.Background(), observability.Event{ //nolint:errcheck
				Service:  ev.Instance.Service,
				Instance: a.Info.Instance,
				Kind:     observability.EventPeerLeave,
				PeerID:   ev.Instance.Instance,
				Success:  true,
			})
		}
	}
}

func (a *Agent) cacheKey() string {
	return "lens:cache:" + a.Info.Service + ":" + a.Info.Instance
}

func (a *Agent) selfURL() string {
	return "http://" + a.Config.AdvertiseAddr + ":" + a.Config.Port
}
