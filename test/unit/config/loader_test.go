package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Vedanshu7/lens/config"
)

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "lens*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	f.Close() //nolint:errcheck
	return f.Name()
}

func TestLoad_BasicFields(t *testing.T) {
	path := writeYAML(t, `
apiVersion: lens/v1
transport:
  provider: grpc
discovery:
  provider: memberlist
persistence:
  provider: redis
agent:
  port: "9000"
  logLevel: debug
`)
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Transport.ProviderName() != "grpc" {
		t.Errorf("Transport: want grpc, got %q", f.Transport.ProviderName())
	}
	if f.Discovery.ProviderName() != "memberlist" {
		t.Errorf("Discovery: want memberlist, got %q", f.Discovery.ProviderName())
	}
	if f.Agent.Port != "9000" {
		t.Errorf("Port: want 9000, got %q", f.Agent.Port)
	}
	if f.Agent.LogLevel != "debug" {
		t.Errorf("LogLevel: want debug, got %q", f.Agent.LogLevel)
	}
}

func TestLoad_EnvVarSubstitution(t *testing.T) {
	t.Setenv("TEST_NATS_URL", "nats://broker:4222")

	path := writeYAML(t, `
transport:
  provider: nats
  config:
    natsUrl: ${TEST_NATS_URL}
`)
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Transport.Config["natsUrl"] != "nats://broker:4222" {
		t.Errorf("env substitution: got %v", f.Transport.Config["natsUrl"])
	}
}

func TestLoad_UnsetEnvVar_PreservesLiteral(t *testing.T) {
	os.Unsetenv("UNSET_VAR_XYZ") //nolint:errcheck
	path := writeYAML(t, `
transport:
  provider: grpc
  config:
    addr: ${UNSET_VAR_XYZ}
`)
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Transport.Config["addr"] != "${UNSET_VAR_XYZ}" {
		t.Errorf("unset env: want literal ${UNSET_VAR_XYZ}, got %v", f.Transport.Config["addr"])
	}
}

func TestLoad_EmptyPath_ReturnsZeroFile(t *testing.T) {
	f, err := config.Load("")
	if err != nil {
		t.Fatalf("Load empty path: %v", err)
	}
	if f.Transport.ProviderName() != "" {
		t.Errorf("empty path should return zero File, got transport %q", f.Transport.ProviderName())
	}
}

func TestLoad_MissingFile_ReturnsError(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("Load missing file: expected error, got nil")
	}
}

func TestLoad_ObserverProviders(t *testing.T) {
	path := writeYAML(t, `
observer:
  enabled: true
  providers:
    - name: prometheus
    - name: stdout
`)
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !f.Observer.Enabled {
		t.Error("Observer.Enabled: want true")
	}
	if len(f.Observer.Providers) != 2 {
		t.Fatalf("Observer.Providers: want 2, got %d", len(f.Observer.Providers))
	}
	if f.Observer.Providers[0].ProviderName() != "prometheus" {
		t.Errorf("Provider[0]: want prometheus, got %q", f.Observer.Providers[0].ProviderName())
	}
}

func TestLoad_TargetBlock(t *testing.T) {
	path := writeYAML(t, `
target:
  provider: unix
  config:
    socketPath: /tmp/app.sock
`)
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Target.ProviderName() != "unix" {
		t.Errorf("Target provider: want unix, got %q", f.Target.ProviderName())
	}
	if f.Target.Config["socketPath"] != "/tmp/app.sock" {
		t.Errorf("Target socketPath: got %v", f.Target.Config["socketPath"])
	}
}
