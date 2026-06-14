// Package targetgrpc registers the "grpc" target provider, which communicates
// with the co-located app service via gRPC. The app must implement the
// LensTarget service defined in internal/proto/target.proto.
package targetgrpc

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	targetv1 "github.com/Vedanshu7/lens/internal/proto/targetv1"
	"github.com/Vedanshu7/lens/internal/target"
)

func init() {
	target.Register("grpc", func(cfg map[string]any) (target.TargetClient, error) {
		addr, _ := cfg["grpcAddr"].(string)
		if addr == "" {
			addr = "localhost:8902"
		}
		token, _ := cfg["token"].(string)
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, fmt.Errorf("target grpc: dial %s: %w", addr, err)
		}
		return &grpcClient{
			conn:   conn,
			client: targetv1.NewLensTargetClient(conn),
			token:  token,
		}, nil
	})
}

type grpcClient struct {
	conn   *grpc.ClientConn
	client targetv1.LensTargetClient
	token  string
}

func (c *grpcClient) ctx(parent context.Context) context.Context {
	if c.token == "" {
		return parent
	}
	return metadata.AppendToOutgoingContext(parent, "x-lens-token", c.token)
}

func (c *grpcClient) Info(ctx context.Context) (target.TargetInfo, error) {
	resp, err := c.client.Info(c.ctx(ctx), &targetv1.TargetInfoRequest{})
	if err != nil {
		return target.TargetInfo{}, err
	}
	return target.TargetInfo{Service: resp.Service, Instance: resp.Instance}, nil
}

func (c *grpcClient) Invalidate(ctx context.Context, payload []byte) error {
	_, err := c.client.Invalidate(c.ctx(ctx), &targetv1.TargetInvalidateRequest{Payload: payload})
	return err
}

func (c *grpcClient) Get(ctx context.Context, key string) ([]byte, error) {
	resp, err := c.client.Get(c.ctx(ctx), &targetv1.TargetGetRequest{Key: key})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *grpcClient) Keys(ctx context.Context, pattern, limit, offset string) ([]byte, error) {
	resp, err := c.client.Keys(c.ctx(ctx), &targetv1.TargetKeysRequest{
		Pattern: pattern,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *grpcClient) Close() error {
	return c.conn.Close()
}
