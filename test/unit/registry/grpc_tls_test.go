package registry_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/Vedanshu7/lens/internal/transport/grpc"

	"github.com/Vedanshu7/lens/internal/transport"
	"github.com/Vedanshu7/lens/test/testutil"
)

func TestGRPCTransport_PlainText_Connects(t *testing.T) {
	if !transport.Has("grpc") {
		t.Fatal("grpc transport not registered")
	}
	tr, err := transport.New(&testutil.StubHost{}, "grpc", map[string]any{"grpcPort": "0"})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	tr.Close() //nolint:errcheck
}

func TestGRPCTransport_TLS_ServerStarts(t *testing.T) {
	tmp := t.TempDir()
	certFile, keyFile := generateSelfSigned(t, tmp)

	tr, err := transport.New(&testutil.StubHost{}, "grpc", map[string]any{
		"grpcPort":    "0",
		"tlsCertFile": certFile,
		"tlsKeyFile":  keyFile,
	})
	if err != nil {
		t.Fatalf("create TLS transport: %v", err)
	}
	tr.Close() //nolint:errcheck
}

func TestGRPCTransport_InvalidCert_ReturnsError(t *testing.T) {
	_, err := transport.New(&testutil.StubHost{}, "grpc", map[string]any{
		"grpcPort":    "0",
		"tlsCertFile": "/nonexistent/cert.pem",
		"tlsKeyFile":  "/nonexistent/key.pem",
	})
	if err == nil {
		t.Error("expected error for invalid cert files")
	}
}

// generateSelfSigned creates a temporary self-signed TLS certificate for testing.
func generateSelfSigned(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")

	cf, _ := os.Create(certFile)
	pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}) //nolint:errcheck
	cf.Close()

	keyDER, _ := x509.MarshalECPrivateKey(key)
	kf, _ := os.Create(keyFile)
	pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}) //nolint:errcheck
	kf.Close()

	// Verify the pair loads correctly.
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		t.Fatalf("verify cert/key: %v", err)
	}
	return certFile, keyFile
}
