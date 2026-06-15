// Package zookeeperdiscovery implements the Resolver interface using Apache ZooKeeper.
// Each agent registers an ephemeral sequential znode under /lens/<service>/<instance>
// containing its ServiceInstance metadata as JSON. ZooKeeper automatically removes
// ephemeral nodes when the session expires, providing health-check semantics for free.
//
// Required config keys:
//
//	servers — comma-separated list of ZooKeeper servers (e.g. "zk1:2181,zk2:2181")
//
// Optional config keys:
//
//	sessionTimeoutSecs — ZooKeeper session timeout in seconds (default: 30)
//	basePath           — root znode path (default: "/lens")
package zookeeperdiscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-zookeeper/zk"

	"github.com/Vedanshu7/lens/internal/discovery"
	"github.com/Vedanshu7/lens/internal/persistence"
)

func init() {
	discovery.Register("zookeeper", func(_ persistence.Backend, cfg map[string]any) (discovery.Resolver, error) {
		servers, _ := cfg["servers"].(string)
		if servers == "" {
			return nil, fmt.Errorf("zookeeper discovery: servers is required")
		}
		sessionSecs := 30
		if s, ok := cfg["sessionTimeoutSecs"].(int); ok && s > 0 {
			sessionSecs = s
		}
		basePath, _ := cfg["basePath"].(string)
		if basePath == "" {
			basePath = "/lens"
		}
		hosts := strings.Split(servers, ",")
		conn, _, err := zk.Connect(hosts, time.Duration(sessionSecs)*time.Second,
			zk.WithLogger(zkLogger{}))
		if err != nil {
			return nil, fmt.Errorf("zookeeper connect: %w", err)
		}
		r := &zkResolver{
			conn:     conn,
			basePath: basePath,
			peers:    make(map[string]discovery.ServiceInstance),
			eventCh:  make(chan discovery.Event, 64),
		}
		return r, nil
	})
}

type zkResolver struct {
	mu       sync.RWMutex
	conn     *zk.Conn
	basePath string
	self     discovery.ServiceInstance
	selfPath string
	peers    map[string]discovery.ServiceInstance
	eventCh  chan discovery.Event
}

func (r *zkResolver) Register(ctx context.Context, self discovery.ServiceInstance) error {
	r.mu.Lock()
	r.self = self
	r.mu.Unlock()

	// Ensure base and service path exist.
	svcPath := r.basePath + "/" + self.Service
	for _, p := range []string{r.basePath, svcPath} {
		if err := r.ensurePath(p); err != nil {
			return err
		}
	}

	data, err := json.Marshal(self)
	if err != nil {
		return err
	}
	// Ephemeral node: auto-deleted when session expires.
	nodePath := svcPath + "/" + self.Instance
	exists, stat, err := r.conn.Exists(nodePath)
	if err != nil {
		return fmt.Errorf("zookeeper exists: %w", err)
	}
	if exists {
		_, err = r.conn.Set(nodePath, data, stat.Version)
	} else {
		_, err = r.conn.Create(nodePath, data, zk.FlagEphemeral, zk.WorldACL(zk.PermAll))
	}
	if err != nil {
		return fmt.Errorf("zookeeper register: %w", err)
	}
	r.selfPath = nodePath
	slog.Info("zookeeper: registered", "path", nodePath)
	return nil
}

func (r *zkResolver) Deregister(_ context.Context, self discovery.ServiceInstance) error {
	path := r.basePath + "/" + self.Service + "/" + self.Instance
	exists, stat, err := r.conn.Exists(path)
	if err != nil || !exists {
		return err
	}
	return r.conn.Delete(path, stat.Version)
}

func (r *zkResolver) Peers(_ context.Context, service string) ([]discovery.ServiceInstance, error) {
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

// Watch starts a goroutine that watches the service path in ZooKeeper and
// delivers join/leave events to the returned channel.
func (r *zkResolver) Watch(ctx context.Context) (<-chan discovery.Event, error) {
	r.mu.RLock()
	svc := r.self.Service
	r.mu.RUnlock()
	if svc == "" {
		return nil, fmt.Errorf("zookeeper watch: Register must be called before Watch")
	}
	go r.watchLoop(ctx, svc)
	return r.eventCh, nil
}

func (r *zkResolver) watchLoop(ctx context.Context, service string) {
	svcPath := r.basePath + "/" + service
	for {
		children, _, wch, err := r.conn.ChildrenW(svcPath)
		if err != nil {
			slog.Warn("zookeeper: watch failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}
		r.reconcile(ctx, svcPath, children)
		select {
		case <-ctx.Done():
			return
		case <-wch:
		}
	}
}

func (r *zkResolver) reconcile(ctx context.Context, svcPath string, children []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	seen := make(map[string]bool, len(children))
	for _, child := range children {
		seen[child] = true
		if _, exists := r.peers[child]; exists {
			continue
		}
		data, _, err := r.conn.Get(svcPath + "/" + child)
		if err != nil {
			continue
		}
		var si discovery.ServiceInstance
		if err := json.Unmarshal(data, &si); err != nil {
			continue
		}
		if si.Instance == r.self.Instance {
			continue
		}
		r.peers[child] = si
		select {
		case r.eventCh <- discovery.Event{Type: discovery.EventJoin, Instance: si}:
		case <-ctx.Done():
			return
		default:
		}
	}
	for child, si := range r.peers {
		if !seen[child] {
			delete(r.peers, child)
			select {
			case r.eventCh <- discovery.Event{Type: discovery.EventLeave, Instance: si}:
			case <-ctx.Done():
				return
			default:
			}
		}
	}
}

func (r *zkResolver) Close() error {
	r.conn.Close()
	close(r.eventCh)
	return nil
}

func (r *zkResolver) ensurePath(path string) error {
	exists, _, err := r.conn.Exists(path)
	if err != nil {
		return err
	}
	if !exists {
		_, err = r.conn.Create(path, nil, 0, zk.WorldACL(zk.PermAll))
		if err != nil && err != zk.ErrNodeExists {
			return fmt.Errorf("zookeeper create %s: %w", path, err)
		}
	}
	return nil
}

// zkLogger adapts go-zookeeper's logger to slog.
type zkLogger struct{}

func (zkLogger) Printf(format string, args ...any) {
	slog.Debug("zookeeper", "msg", fmt.Sprintf(format, args...))
}

var _ discovery.Resolver = (*zkResolver)(nil)
