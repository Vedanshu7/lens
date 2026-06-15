// Package zeromqtransport implements the Transport interface using ZeroMQ sockets.
// Each sidecar binds a ROUTER socket to receive incoming messages and maintains
// a pool of DEALER sockets for outbound connections to peer ROUTER endpoints.
// Broadcast is performed in parallel with a per-peer goroutine; Get is a
// synchronous DEALER send followed by a single response receive.
package zeromqtransport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/go-zeromq/zmq4"

	"github.com/Vedanshu7/lens/internal/transport"
)

func init() {
	transport.Register("zeromq", func(host transport.TransportHost, cfg map[string]any) (transport.Transport, error) {
		port, _ := cfg["zmqPort"].(string)
		if port == "" {
			port = "8902"
		}
		return newZeroMQTransport(host, port)
	})
}

type zmqMessage struct {
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Origin  string          `json:"origin"`
	Key     string          `json:"key,omitempty"`
}

type zmqTransport struct {
	host    transport.TransportHost
	port    string
	router  zmq4.Socket
	dealers sync.Map
	cancel  context.CancelFunc
}

func newZeroMQTransport(host transport.TransportHost, port string) (*zmqTransport, error) {
	ctx, cancel := context.WithCancel(context.Background())

	router := zmq4.NewRouter(ctx)
	addr := "tcp://*:" + port
	if err := router.Listen(addr); err != nil {
		cancel()
		return nil, fmt.Errorf("zeromq: bind router %s: %w", addr, err)
	}

	t := &zmqTransport{
		host:   host,
		port:   port,
		router: router,
		cancel: cancel,
	}
	go t.serve(ctx)

	slog.Info("zeromq transport ready", "port", port)
	return t, nil
}

// Broadcast sends an invalidation message to each peer in parallel and collects
// Acks from peers that respond within 2 seconds.
func (t *zmqTransport) Broadcast(ctx context.Context, svc string, payload []byte) ([]transport.Ack, error) {
	peers := t.host.PeersForService(svc)
	if len(peers) == 0 {
		t.host.WriteInvalidationLog(ctx, svc, payload)
		return nil, nil
	}

	msg := zmqMessage{
		Action:  "invalidate",
		Payload: json.RawMessage(payload),
		Origin:  t.host.SelfInstance(),
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("zeromq broadcast marshal: %w", err)
	}

	type result struct {
		ack transport.Ack
		err error
	}
	results := make(chan result, len(peers))

	for _, peer := range peers {
		go func(p transport.PeerAddr) {
			ack, sendErr := t.sendRecv(p.GRPCAddr, data, 2*time.Second)
			results <- result{ack: ack, err: sendErr}
		}(peer)
	}

	var acks []transport.Ack
	deadline := time.After(2 * time.Second)
	for range peers {
		select {
		case r := <-results:
			if r.err == nil {
				acks = append(acks, r.ack)
			}
		case <-deadline:
		}
	}

	t.host.WriteInvalidationLog(ctx, svc, payload)
	return acks, nil
}

// Get sends a get request to the specific peer instance and returns the response body.
func (t *zmqTransport) Get(ctx context.Context, svc, instance, key string) ([]byte, error) {
	peers := t.host.PeersForService(svc)
	addr := ""
	for _, p := range peers {
		if p.Instance == instance {
			addr = p.GRPCAddr
			break
		}
	}
	if addr == "" {
		return nil, fmt.Errorf("zeromq get: instance %q not found", instance)
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("zeromq get: bad addr %q: %w", addr, err)
	}
	peerAddr := net.JoinHostPort(host, t.port)

	msg := zmqMessage{Action: "get", Key: key, Origin: t.host.SelfInstance()}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("zeromq get marshal: %w", err)
	}

	timeout := 3 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
	}

	ack, err := t.sendRecv(peerAddr, data, timeout)
	if err != nil {
		return nil, err
	}
	if !ack.Success {
		return nil, fmt.Errorf("zeromq get: %s", ack.Error)
	}
	return json.Marshal(ack)
}

// Close cancels the serve goroutine and closes all pooled sockets.
func (t *zmqTransport) Close() error {
	t.cancel()
	t.dealers.Range(func(key, value any) bool {
		if d, ok := value.(zmq4.Socket); ok {
			d.Close() //nolint:errcheck
		}
		return true
	})
	return t.router.Close()
}

// serve reads messages from the ROUTER socket and dispatches each to handle.
func (t *zmqTransport) serve(ctx context.Context) {
	for ctx.Err() == nil {
		msg, err := t.router.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("zeromq: recv error", "err", err)
			continue
		}
		go t.handle(msg)
	}
}

// handle processes one incoming ROUTER frame. The reply is sent back on the
// same socket using the identity frame that ZeroMQ prepended automatically.
func (t *zmqTransport) handle(msg zmq4.Msg) {
	if len(msg.Frames) < 2 {
		return
	}
	identity := msg.Frames[0]
	body := msg.Frames[len(msg.Frames)-1]

	var req zmqMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return
	}

	ctx := context.Background()
	var replyData []byte

	switch req.Action {
	case "invalidate":
		t.host.ApplyInvalidation(ctx, req.Payload, req.Origin)
		ack := transport.Ack{Instance: t.host.SelfInstance(), Success: true}
		replyData, _ = json.Marshal(ack)
	case "get":
		respBody, err := t.host.GetFromTarget(ctx, []byte(fmt.Sprintf(`{"key":%q}`, req.Key)))
		if err != nil {
			slog.Warn("zeromq get: target unreachable", "err", err)
			ack := transport.Ack{Instance: t.host.SelfInstance(), Success: false, Error: err.Error()}
			replyData, _ = json.Marshal(ack)
		} else {
			replyData = respBody
		}
	default:
		return
	}

	reply := zmq4.NewMsgFrom(identity, replyData)
	if err := t.router.Send(reply); err != nil {
		slog.Warn("zeromq: send reply error", "err", err)
	}
}

// sendRecv dials or reuses a DEALER socket to addr, sends data, and waits up
// to timeout for a single reply frame. On error the DEALER is evicted from the pool.
func (t *zmqTransport) sendRecv(addr string, data []byte, timeout time.Duration) (transport.Ack, error) {
	dialAddr := "tcp://" + addr
	dealer, err := t.getDealer(dialAddr)
	if err != nil {
		return transport.Ack{}, fmt.Errorf("zeromq dial %s: %w", dialAddr, err)
	}

	if err := dealer.Send(zmq4.NewMsg(data)); err != nil {
		t.evictDealer(dialAddr, dealer)
		return transport.Ack{}, fmt.Errorf("zeromq send: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	type recvResult struct {
		msg zmq4.Msg
		err error
	}
	ch := make(chan recvResult, 1)
	go func() {
		msg, err := dealer.Recv()
		ch <- recvResult{msg, err}
	}()

	var ack transport.Ack
	select {
	case <-ctx.Done():
		t.evictDealer(dialAddr, dealer)
		return ack, fmt.Errorf("zeromq recv timeout after %s", timeout)
	case r := <-ch:
		if r.err != nil {
			t.evictDealer(dialAddr, dealer)
			return ack, fmt.Errorf("zeromq recv: %w", r.err)
		}
		if len(r.msg.Frames) > 0 {
			json.Unmarshal(r.msg.Frames[0], &ack) //nolint:errcheck
		}
	}
	return ack, nil
}

// getDealer returns a cached DEALER socket for dialAddr, creating one if absent.
func (t *zmqTransport) getDealer(dialAddr string) (zmq4.Socket, error) {
	if v, ok := t.dealers.Load(dialAddr); ok {
		return v.(zmq4.Socket), nil
	}
	ctx := context.Background()
	dealer := zmq4.NewDealer(ctx)
	if err := dealer.Dial(dialAddr); err != nil {
		return nil, err
	}
	actual, loaded := t.dealers.LoadOrStore(dialAddr, dealer)
	if loaded {
		dealer.Close() //nolint:errcheck
		return actual.(zmq4.Socket), nil
	}
	return dealer, nil
}

// evictDealer removes the DEALER at dialAddr from the pool and closes it.
func (t *zmqTransport) evictDealer(dialAddr string, dealer zmq4.Socket) {
	t.dealers.LoadAndDelete(dialAddr)
	dealer.Close() //nolint:errcheck
}

// Compile-time check that zmqTransport satisfies transport.Transport.
var _ transport.Transport = (*zmqTransport)(nil)
