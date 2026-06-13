// Package agent implements the Lens cache-visibility sidecar.
// It manages the connection to the target service, coordinates peer discovery,
// routes invalidation and fetch requests, and emits structured telemetry events.
package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vedanshu/lens/config"
	"github.com/vedanshu/lens/internal/discovery"
	"github.com/vedanshu/lens/internal/observability"
	"github.com/vedanshu/lens/internal/persistence"
	"github.com/vedanshu/lens/internal/store"
	"github.com/vedanshu/lens/internal/transport"
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
	replayHours, _ := strconv.Atoi(envOr("LENS_REPLAY_WINDOW_HOURS", "24"))
	gossipPort, _ := strconv.Atoi(envOr("LENS_GOSSIP_PORT", "7946"))

	cfg := Config{
		TargetURL:         envOr("LENS_TARGET_URL", "http://localhost:8080"),
		Port:              envOr("LENS_PORT", "8900"),
		BindAddr:          envOr("LENS_BIND_ADDR", "127.0.0.1"),
		AdvertiseAddr:     envOr("LENS_ADVERTISE_ADDR", detectAdvertiseAddr()),
		Token:             os.Getenv("LENS_TOKEN"),
		CooldownMS:        cooldown,
		ReplayEnabled:     envOr("LENS_REPLAY_ENABLED", "true") != "false",
		ReplayWindowHours: replayHours,
		LogLevel:          envOr("LENS_LOG_LEVEL", "info"),
		Transport:         envOr("LENS_TRANSPORT", ""),
		Persistence:       envOr("LENS_PERSISTENCE", "redis"),
		Discovery:         envOr("LENS_DISCOVERY", ""),
		TransportConfig:   map[string]any{"grpcPort": envOr("LENS_GRPC_PORT", "8901"), "natsUrl": envOr("LENS_NATS_URL", "nats://localhost:4222")},
		PersistenceConfig: map[string]any{"addr": envOr("LENS_REDIS_ADDR", "localhost:6379"), "db": db},
		DiscoveryConfig:   map[string]any{"bindPort": gossipPort},
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

	cfg.ObserverEnabled = f.Observer.Enabled
	if len(f.Observer.Providers) > 0 {
		cfg.ObserverProviders = make([]ObserverProviderConfig, len(f.Observer.Providers))
		for i, p := range f.Observer.Providers {
			cfg.ObserverProviders[i] = ObserverProviderConfig{Name: p.ProviderName(), Config: p.Config}
		}
	}
}

// validateConfig checks that the configured provider names are compiled in.
// Returns a clear error so the user knows exactly which build tag is missing.
func validateConfig(cfg Config) error {
	if cfg.Transport == "" {
		return fmt.Errorf("transport provider not set: add 'transport.provider' to lens.yaml or set LENS_TRANSPORT")
	}
	if !transport.Has(cfg.Transport) {
		return fmt.Errorf("transport %q is not compiled in; rebuild with -tags lens_%s", cfg.Transport, cfg.Transport)
	}
	if !persistence.Has(cfg.Persistence) {
		return fmt.Errorf("persistence %q is not compiled in", cfg.Persistence)
	}
	if cfg.Discovery == "" {
		return fmt.Errorf("discovery provider not set: add 'discovery.provider' to lens.yaml or set LENS_DISCOVERY")
	}
	if !discovery.Has(cfg.Discovery) {
		return fmt.Errorf("discovery %q is not compiled in; rebuild with -tags lens_%s", cfg.Discovery, cfg.Discovery)
	}
	for _, p := range cfg.ObserverProviders {
		if !observability.Has(p.Name) {
			return fmt.Errorf("observer %q is not compiled in", p.Name)
		}
	}
	return nil
}

// detectAdvertiseAddr returns this node's primary outbound IP by opening a UDP
// socket without sending any traffic. Falls back to "127.0.0.1" on error.
func detectAdvertiseAddr() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
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

// TargetInfo is the identity payload returned by /internal/lens/info on the target service.
type TargetInfo struct {
	Service  string `json:"service"`
	Instance string `json:"instance"`
}

// Agent is the Lens cache-visibility sidecar. It is safe for concurrent use.
type Agent struct {
	// Config is the resolved runtime configuration.
	Config Config
	// Info holds the target service identity discovered on first connection.
	Info TargetInfo
	// HTTP is the client used for requests to the target service.
	HTTP *http.Client
	// ProxyHTTP is the client used for proxied requests to peer sidecars.
	ProxyHTTP *http.Client
	// Metrics holds the Prometheus instrumentation for this agent.
	Metrics *Metrics
	// Throttle enforces per-service invalidation rate limits.
	Throttle *Throttle
	// Obs is the multi-observer fan-out for structured telemetry events.
	Obs *observability.MultiObserver

	store     persistence.Backend
	transport transport.Transport
	disc      discovery.Resolver

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

	var observers []observability.Observer
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

	return &Agent{
		Config:    cfg,
		store:     store,
		Obs:       observability.NewMultiObserver(observers),
		HTTP:      &http.Client{Timeout: 5 * time.Second},
		ProxyHTTP: &http.Client{Timeout: 2 * time.Second},
		Metrics:   newMetrics(),
		Throttle:  newThrottle(cfg.CooldownMS),
	}
}

// Shutdown cancels in-flight connections, deregisters this instance from
// discovery, and closes all providers.
func (a *Agent) Shutdown(ctx context.Context) {
	a.cancelDial()
	if a.ready() {
		a.deregister(ctx)
	}
	if a.transport != nil {
		a.transport.Close()
	}
	if a.disc != nil {
		a.disc.Close()
	}
	a.Obs.Close()
	a.store.Close()
}

func (a *Agent) ready() bool { return a.live.Load() }

func (a *Agent) cancelDial() {
	if a.dialCancel != nil {
		a.dialCancel()
		a.dialCancel = nil
	}
}

// spawn runs fn in a named goroutine with panic recovery.
func (a *Agent) spawn(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("goroutine panic", "name", name, "panic", r)
			}
		}()
		fn()
	}()
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
// and returns the response body.
func (a *Agent) GetFromTarget(ctx context.Context, payload []byte) ([]byte, error) {
	resp, err := a.post(ctx,
		a.Config.TargetURL+"/internal/lens/get",
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, err
	}
	defer closeBody(resp)
	return io.ReadAll(resp.Body)
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

func (a *Agent) nodeKey() string {
	return "lens:node:" + a.Info.Service + ":" + a.Info.Instance
}

func (a *Agent) cacheKey() string {
	return "lens:cache:" + a.Info.Service + ":" + a.Info.Instance
}

func (a *Agent) selfURL() string {
	return "http://" + a.Config.AdvertiseAddr + ":" + a.Config.Port
}

func (a *Agent) post(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	if a.Config.Token != "" {
		req.Header.Set("x-lens-token", a.Config.Token)
	}
	return a.HTTP.Do(req)
}

func (a *Agent) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if a.Config.Token != "" {
		req.Header.Set("x-lens-token", a.Config.Token)
	}
	return a.HTTP.Do(req)
}

func closeBody(resp *http.Response) {
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
}
