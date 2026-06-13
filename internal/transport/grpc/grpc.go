//go:build lens_grpc

// Package grpctransport implements the Transport interface using direct pod-to-pod gRPC.
// It starts a gRPC server on the configured port and maintains a pool of outbound
// client connections, evicting them on failure so re-dial happens automatically.
package grpctransport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	lensv1 "github.com/Vedanshu7/lens/internal/proto/lensv1"
	"github.com/Vedanshu7/lens/internal/transport"
)

func init() {
	transport.Register("grpc", func(host transport.TransportHost, cfg map[string]any) (transport.Transport, error) {
		grpcPort, _ := cfg["grpcPort"].(string)
		if grpcPort == "" {
			grpcPort = "8901"
		}
		return newGRPCTransport(host, grpcPort)
	})
}

type grpcTransport struct {
	host   transport.TransportHost
	server *grpc.Server
	conns  sync.Map
}

func newGRPCTransport(host transport.TransportHost, grpcPort string) (*grpcTransport, error) {
	t := &grpcTransport{host: host}

	lis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		return nil, fmt.Errorf("grpc listen: %w", err)
	}
	t.server = grpc.NewServer()
	lensv1.RegisterLensAgentServer(t.server, &grpcHandler{host: host})

	go func() {
		slog.Info("grpc server listening", "port", grpcPort)
		if err := t.server.Serve(lis); err != nil {
			slog.Error("grpc server exited", "err", err)
		}
	}()
	return t, nil
}

// Broadcast fans out payload as an Invalidate RPC to all live peers of svc in parallel.
// Peers that do not respond within 2 seconds are recorded in the invalidation log.
// Returns one Ack per peer, in no guaranteed order.
func (t *grpcTransport) Broadcast(ctx context.Context, svc string, payload []byte) ([]transport.Ack, error) {
	peers := t.host.PeersForService(svc)
	if len(peers) == 0 {
		t.host.WriteInvalidationLog(ctx, svc, payload)
		return nil, nil
	}

	ch := make(chan transport.Ack, len(peers))
	now := time.Now().UTC().Format(time.RFC3339)

	for _, p := range peers {
		p := p
		go func() {
			conn, err := t.conn(p.GRPCAddr)
			if err != nil {
				ch <- transport.Ack{Instance: p.Instance, Success: false, Error: err.Error()}
				return
			}
			callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			resp, err := lensv1.NewLensAgentClient(conn).Invalidate(callCtx, &lensv1.InvalidateRequest{
				Payload: payload,
				Origin:  t.host.SelfInstance(),
				Time:    now,
			})
			if err != nil {
				t.evictConn(p.GRPCAddr)
				ch <- transport.Ack{Instance: p.Instance, Success: false, Error: err.Error()}
				return
			}
			ch <- transport.Ack{Instance: resp.Instance, Success: resp.Success, Error: resp.Error}
		}()
	}

	deadline := time.After(2 * time.Second)
	var acks []transport.Ack
	for range len(peers) {
		select {
		case ack := <-ch:
			acks = append(acks, ack)
		case <-deadline:
			t.host.WriteInvalidationLog(ctx, svc, payload)
			return acks, nil
		}
	}
	t.host.WriteInvalidationLog(ctx, svc, payload)
	return acks, nil
}

// Get fetches key from the specific instance of svc via a direct gRPC call.
// Returns an error if the peer is not found or the RPC fails.
func (t *grpcTransport) Get(ctx context.Context, svc, instance, key string) ([]byte, error) {
	var grpcAddr string
	for _, p := range t.host.PeersForService(svc) {
		if p.Instance == instance {
			grpcAddr = p.GRPCAddr
			break
		}
	}
	if grpcAddr == "" {
		return nil, fmt.Errorf("peer not found: %s/%s", svc, instance)
	}

	conn, err := t.conn(grpcAddr)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]string{"key": key})
	resp, err := lensv1.NewLensAgentClient(conn).Get(ctx, &lensv1.GetRequest{Payload: payload})
	if err != nil {
		t.evictConn(grpcAddr)
		return nil, err
	}
	return resp.Body, nil
}

// Close stops the gRPC server and closes all cached client connections.
func (t *grpcTransport) Close() error {
	t.server.GracefulStop()
	t.conns.Range(func(_, v any) bool {
		_ = v.(*grpc.ClientConn).Close()
		return true
	})
	return nil
}

func (t *grpcTransport) evictConn(addr string) {
	if v, ok := t.conns.LoadAndDelete(addr); ok {
		_ = v.(*grpc.ClientConn).Close()
	}
}

func (t *grpcTransport) conn(addr string) (*grpc.ClientConn, error) {
	var result *grpc.ClientConn
	var err error
	if v, ok := t.conns.Load(addr); ok {
		result = v.(*grpc.ClientConn)
	} else {
		var c *grpc.ClientConn
		c, err = grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()), clientKeepalive)
		if err == nil {
			actual, loaded := t.conns.LoadOrStore(addr, c)
			if loaded {
				_ = c.Close()
				result = actual.(*grpc.ClientConn)
			} else {
				result = c
			}
		}
	}
	return result, err
}

// clientKeepalive sends a keepalive ping every 30 s so that stale TCP connections
// are detected before the next RPC call rather than at call time.
var clientKeepalive = grpc.WithKeepaliveParams(keepalive.ClientParameters{
	Time:                30 * time.Second,
	Timeout:             5 * time.Second,
	PermitWithoutStream: true,
})

// grpcHandler is separate from grpcTransport to avoid the naming conflict between
// Transport.Get(ctx, svc, instance, key) and LensAgentServer.Get(ctx, *GetRequest).
type grpcHandler struct {
	lensv1.UnimplementedLensAgentServer
	host transport.TransportHost
}

// Invalidate applies the invalidation payload from the origin peer and acknowledges it.
func (h *grpcHandler) Invalidate(ctx context.Context, req *lensv1.InvalidateRequest) (*lensv1.InvalidateResponse, error) {
	h.host.ApplyInvalidation(ctx, req.Payload, req.Origin)
	return &lensv1.InvalidateResponse{
		Instance: h.host.SelfInstance(),
		Success:  true,
	}, nil
}

// Get proxies the request payload to this sidecar's target service and returns the response body.
func (h *grpcHandler) Get(ctx context.Context, req *lensv1.GetRequest) (*lensv1.GetResponse, error) {
	body, err := h.host.GetFromTarget(ctx, req.Payload)
	var resp *lensv1.GetResponse
	if err != nil {
		resp = &lensv1.GetResponse{StatusCode: 502}
	} else {
		resp = &lensv1.GetResponse{Body: body, StatusCode: 200}
	}
	return resp, nil
}
