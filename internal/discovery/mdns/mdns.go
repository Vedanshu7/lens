// Package mdnsdiscovery implements the Resolver interface using mDNS/Zeroconf.
// Each agent advertises itself as a _lens._tcp service on the local network.
// Peer discovery works without any central coordinator, making this ideal for
// local development, bare-metal clusters, or edge deployments.
//
// Optional config keys:
//
//	service — mDNS service type (default: "_lens._tcp")
//	domain  — mDNS domain (default: "local.")
//	port    — port advertised in the mDNS record (default: 8900)
package mdnsdiscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/hashicorp/mdns"

	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/persistence"
)

func init() {
	discovery.Register("mdns", func(_ persistence.Backend, cfg map[string]any) (discovery.Resolver, error) {
		service, _ := cfg["service"].(string)
		if service == "" {
			service = "_lens._tcp"
		}
		domain, _ := cfg["domain"].(string)
		if domain == "" {
			domain = "local."
		}
		port := 8900
		if p, ok := cfg["port"].(int); ok && p > 0 {
			port = p
		}
		return &mdnsResolver{
			service: service,
			domain:  domain,
			port:    port,
			peers:   make(map[string]discovery.ServiceInstance),
			eventCh: make(chan discovery.Event, 64),
		}, nil
	})
}

type mdnsResolver struct {
	mu      sync.RWMutex
	self    discovery.ServiceInstance
	service string
	domain  string
	port    int
	server  *mdns.Server
	peers   map[string]discovery.ServiceInstance
	eventCh chan discovery.Event
}

// Register advertises this agent via mDNS. The instance metadata is encoded
// as a JSON TXT record so peers can reconstruct the full ServiceInstance.
func (r *mdnsResolver) Register(_ context.Context, self discovery.ServiceInstance) error {
	r.mu.Lock()
	r.self = self
	r.mu.Unlock()

	meta, err := json.Marshal(self)
	if err != nil {
		return err
	}

	host := resolveHostname()
	info := []string{string(meta)}
	svcInfo, err := mdns.NewMDNSService(
		self.Instance,
		r.service,
		r.domain,
		host,
		r.port,
		nil,
		info,
	)
	if err != nil {
		return fmt.Errorf("mdns service: %w", err)
	}
	srv, err := mdns.NewServer(&mdns.Config{Zone: svcInfo})
	if err != nil {
		return fmt.Errorf("mdns server: %w", err)
	}
	r.mu.Lock()
	r.server = srv
	r.mu.Unlock()
	slog.Info("mdns: registered", "service", r.service, "instance", self.Instance)
	return nil
}

func (r *mdnsResolver) Deregister(_ context.Context, _ discovery.ServiceInstance) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.server != nil {
		return r.server.Shutdown()
	}
	return nil
}

func (r *mdnsResolver) Peers(_ context.Context, service string) ([]discovery.ServiceInstance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []discovery.ServiceInstance
	for _, si := range r.peers {
		if si.Service == service && si.Instance != r.self.Instance {
			out = append(out, si)
		}
	}
	return out, nil
}

// Watch starts a continuous mDNS browse loop in the background and delivers
// join/leave events to the returned channel.
func (r *mdnsResolver) Watch(ctx context.Context) (<-chan discovery.Event, error) {
	go r.browseLoop(ctx)
	return r.eventCh, nil
}

func (r *mdnsResolver) browseLoop(ctx context.Context) {
	entries := make(chan *mdns.ServiceEntry, 32)
	go func() {
		for entry := range entries {
			r.handleEntry(ctx, entry)
		}
	}()

	params := &mdns.QueryParam{
		Service:             r.service,
		Domain:              r.domain,
		Entries:             entries,
		WantUnicastResponse: false,
	}
	for {
		select {
		case <-ctx.Done():
			close(entries)
			return
		default:
		}
		if err := mdns.Query(params); err != nil {
			slog.Warn("mdns: browse error", "err", err)
		}
	}
}

func (r *mdnsResolver) handleEntry(ctx context.Context, entry *mdns.ServiceEntry) {
	if len(entry.InfoFields) == 0 {
		return
	}
	var si discovery.ServiceInstance
	// TXT record is the JSON-encoded ServiceInstance.
	if err := json.Unmarshal([]byte(entry.InfoFields[0]), &si); err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if si.Instance == r.self.Instance {
		return
	}
	if _, exists := r.peers[si.Instance]; !exists {
		r.peers[si.Instance] = si
		select {
		case r.eventCh <- discovery.Event{Type: discovery.EventJoin, Instance: si}:
		case <-ctx.Done():
		default:
		}
	}
}

func (r *mdnsResolver) Close() error {
	r.mu.Lock()
	srv := r.server
	r.mu.Unlock()
	if srv != nil {
		srv.Shutdown() //nolint:errcheck
	}
	close(r.eventCh)
	return nil
}

func resolveHostname() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "localhost."
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return strings.ReplaceAll(ipnet.IP.String(), ".", "-") + ".local."
			}
		}
	}
	return "localhost."
}

var _ discovery.Resolver = (*mdnsResolver)(nil)
