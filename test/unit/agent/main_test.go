package agent_test

import (
	"context"
	"os"
	"testing"

	"github.com/Vedanshu7/lens/internal/transport"
)

func TestMain(m *testing.M) {
	transport.Register("__test_transport_registry__", func(_ transport.TransportHost, _ map[string]any) (transport.Transport, error) {
		return &noopTransport{}, nil
	})
	os.Exit(m.Run())
}

type noopTransport struct{}

func (n *noopTransport) Broadcast(_ context.Context, _ string, _ []byte) ([]transport.Ack, error) {
	return nil, nil
}
func (n *noopTransport) Get(_ context.Context, _, _, _ string) ([]byte, error) { return nil, nil }
func (n *noopTransport) Close() error                                           { return nil }
