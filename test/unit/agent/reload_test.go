package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Vedanshu7/lens/internal/agent"
)

const reloadDebounce = 400 * time.Millisecond

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeYAML: %v", err)
	}
}

func TestWatchConfig_ReducesCooldown(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "lens.yaml")
	writeYAML(t, cfgPath, "apiVersion: v1\nkind: LensConfig\nagent:\n  cooldownMs: 5000\n")

	cfg := defaultCfg()
	cfg.CooldownMS = 5000
	a := newTestAgent(t, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go a.WatchConfig(ctx, cfgPath)
	time.Sleep(50 * time.Millisecond) // let the watcher start

	// Prime lastSeen so the cooldown gates the next call.
	a.Throttle.Allow("svc")
	ok, _ := a.Throttle.Allow("svc")
	if ok {
		t.Fatal("expected Allow to be blocked at 5s cooldown before reload")
	}

	// Write a new config with a 1ms cooldown.
	writeYAML(t, cfgPath, "apiVersion: v1\nkind: LensConfig\nagent:\n  cooldownMs: 1\n")
	time.Sleep(reloadDebounce) // debounce fires after 200ms; wait 400ms to be safe

	// After >1ms cooldown, Allow should now pass.
	ok, _ = a.Throttle.Allow("svc")
	if !ok {
		t.Error("expected Allow to pass after cooldownMs reloaded to 1ms")
	}
}

func TestWatchConfig_PerServiceCooldown(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "lens.yaml")
	writeYAML(t, cfgPath, "apiVersion: v1\nkind: LensConfig\nagent:\n  cooldownMs: 5000\n")

	cfg := defaultCfg()
	cfg.CooldownMS = 5000
	a := newTestAgent(t, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go a.WatchConfig(ctx, cfgPath)
	time.Sleep(50 * time.Millisecond)

	a.Throttle.Allow("svc-b")
	ok, _ := a.Throttle.Allow("svc-b")
	if ok {
		t.Fatal("expected svc-b to be throttled before reload")
	}

	// Reload with a per-service 1ms override for svc-b.
	writeYAML(t, cfgPath, "apiVersion: v1\nkind: LensConfig\nagent:\n  cooldownMs: 5000\n  cooldowns:\n    svc-b: 1\n")
	time.Sleep(reloadDebounce)

	ok, _ = a.Throttle.Allow("svc-b")
	if !ok {
		t.Error("expected svc-b to pass after per-service cooldown reloaded to 1ms")
	}
}

func TestWatchConfig_InvalidYAML_DoesNotCrash(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "lens.yaml")
	writeYAML(t, cfgPath, "apiVersion: v1\nkind: LensConfig\nagent:\n  cooldownMs: 50\n")

	a := newTestAgent(t, defaultCfg())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go a.WatchConfig(ctx, cfgPath)
	time.Sleep(50 * time.Millisecond)

	// Write malformed YAML — watcher should log a warning and not panic.
	writeYAML(t, cfgPath, "{ invalid yaml: :::")
	time.Sleep(reloadDebounce)
	// Reaching here without a panic is the assertion.
}

func TestWatchConfig_MissingFile_DoesNotCrash(t *testing.T) {
	a := newTestAgent(t, defaultCfg())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// WatchConfig should return gracefully when the file doesn't exist.
	done := make(chan struct{})
	go func() {
		a.WatchConfig(ctx, "/tmp/nonexistent-lens-config-abc123.yaml")
		close(done)
	}()
	select {
	case <-done:
		// expected: WatchConfig returned because Add failed
	case <-time.After(2 * time.Second):
		t.Error("WatchConfig did not return after failing to watch nonexistent file")
	}
}

func TestFindConfigPath_NoFile_ReturnsEmpty(t *testing.T) {
	// In the test environment there is no lens.yaml in cwd or /etc/lens/.
	// FindConfigPath should return "".
	// (This test may be environment-dependent; skip if a config file happens to exist.)
	p := agent.FindConfigPath()
	if p != "" {
		// A real config file exists; the test is vacuously correct.
		t.Skipf("skipping: config file found at %s", p)
	}
}
