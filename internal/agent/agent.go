// Package agent implements the Lens cache-visibility sidecar.
// It manages the connection to the target service, coordinates peer discovery,
// routes invalidation and fetch requests, and emits structured telemetry events.
package agent

import (
	"bytes"
	"context"
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

	"github.com/vedanshu/lens/internal/discovery"
	"github.com/vedanshu/lens/internal/observability"
	"github.com/vedanshu/lens/internal/persistence"
	"github.com/vedanshu/lens/internal/transport"
)

// Config holds all runtime configuration for the Lens sidecar.
// Values are read from LENS_* environment variables by LoadConfig.
type Config struct {
	// TargetURL is the base HTTP URL of the service this sidecar is attached to.
	TargetURL string
	// Port is the HTTP port the sidecar listens on.
	Port string
	// BindAddr is the local address the HTTP server binds to.
	BindAddr string
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

	// Transport names the transport provider ("grpc" or "nats").
	Transport string
	// Persistence names the persistence provider ("redis" or "memory").
	Persistence string
	// Discovery names the discovery provider ("memberlist" or "static").
	Discovery string

	// RedisAddr is the Redis server address in "host:port" form.
	RedisAddr string
	// RedisDB is the Redis database index.
	RedisDB int

	// GRPCPort is the port the gRPC server listens on.
	GRPCPort string
	// NATSUrl is the NATS server URL.
	NATSUrl string

	// GossipPort is the UDP port used by the memberlist gossip protocol.
	GossipPort int
	// AdvertiseAddr is the IP peers use to reach this pod.
	// Defaults to the auto-detected outbound IP so a 0.0.0.0 bind never leaks
	// as a peer address. Override with LENS_ADVERTISE_ADDR when behind NAT.
	AdvertiseAddr string

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

// LoadConfig reads configuration from LENS_* environment variables and returns
// a Config populated with defaults for any unset variables.
func LoadConfig() Config {
	db, _ := strconv.Atoi(envOr("LENS_REDIS_DB", "0"))
	cooldown, _ := strconv.Atoi(envOr("LENS_COOLDOWN_MS", "1000"))
	replayHours, _ := strconv.Atoi(envOr("LENS_REPLAY_WINDOW_HOURS", "24"))
	gossipPort, _ := strconv.Atoi(envOr("LENS_GOSSIP_PORT", "7946"))
	return Config{
		TargetURL:         envOr("LENS_TARGET_URL", "http://localhost:8080"),
		Port:              envOr("LENS_PORT", "8900"),
		RedisAddr:         envOr("LENS_REDIS_ADDR", "localhost:6379"),
		RedisDB:           db,
		BindAddr:          envOr("LENS_BIND_ADDR", "127.0.0.1"),
		Token:             os.Getenv("LENS_TOKEN"),
		CooldownMS:        cooldown,
		ReplayEnabled:     envOr("LENS_REPLAY_ENABLED", "true") != "false",
		ReplayWindowHours: replayHours,
		LogLevel:          envOr("LENS_LOG_LEVEL", "info"),
		Transport:         envOr("LENS_TRANSPORT", "grpc"),
		Persistence:       envOr("LENS_PERSISTENCE", "redis"),
		Discovery:         envOr("LENS_DISCOVERY", "memberlist"),
		GRPCPort:          envOr("LENS_GRPC_PORT", "8901"),
		GossipPort:        gossipPort,
		NATSUrl:           envOr("LENS_NATS_URL", "nats://localhost:4222"),
		AdvertiseAddr:     envOr("LENS_ADVERTISE_ADDR", detectAdvertiseAddr()),
	}
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

	transportCfg   map[string]any
	discoveryCfg   map[string]any
	persistenceCfg map[string]any
}

// New constructs an Agent from cfg. Persistence is initialised immediately;
// transport and discovery are deferred until after the target service identity
// is known (see dial).
func New(cfg Config) *Agent {
	persistenceCfg := map[string]any{
		"addr": cfg.RedisAddr,
		"db":   cfg.RedisDB,
	}
	store, err := persistence.New(cfg.Persistence, persistenceCfg)
	if err != nil {
		slog.Error("failed to init persistence", "provider", cfg.Persistence, "err", err)
		panic(err)
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
		transportCfg: map[string]any{
			"grpcPort": cfg.GRPCPort,
			"natsUrl":  cfg.NATSUrl,
		},
		discoveryCfg: map[string]any{
			"bindPort": cfg.GossipPort,
		},
		persistenceCfg: persistenceCfg,
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

// allServices returns deduplicated service names from the live peer map,
// always including this instance's own service.
func (a *Agent) allServices() []string {
	seen := map[string]bool{}
	if a.Info.Service != "" {
		seen[a.Info.Service] = true
	}
	a.peers.Range(func(_, v any) bool {
		seen[v.(discovery.ServiceInstance).Service] = true
		return true
	})
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
