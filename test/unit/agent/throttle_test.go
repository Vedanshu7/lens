package agent_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/Vedanshu7/lens/internal/agent"
)

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"unknown", slog.LevelInfo},
		{"", slog.LevelInfo},
	}
	for _, tc := range cases {
		if got := agent.ParseLogLevel(tc.input); got != tc.want {
			t.Errorf("ParseLogLevel(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestThrottle_Allow_FirstCallPasses(t *testing.T) {
	th := agent.NewThrottle(100)
	ok, wait := th.Allow("svc-a")
	if !ok || wait != 0 {
		t.Errorf("first Allow: want (true, 0), got (%v, %v)", ok, wait)
	}
}

func TestThrottle_Allow_ImmediateSecondCallThrottled(t *testing.T) {
	th := agent.NewThrottle(100)
	th.Allow("svc-a") //nolint:errcheck

	ok, wait := th.Allow("svc-a")
	if ok || wait <= 0 {
		t.Errorf("immediate second Allow: want (false, >0), got (%v, %v)", ok, wait)
	}
}

func TestThrottle_Allow_PassesAfterCooldown(t *testing.T) {
	cooldown := 100 * time.Millisecond
	th := agent.NewThrottle(int(cooldown.Milliseconds()))
	th.Allow("svc-a") //nolint:errcheck

	time.Sleep(cooldown + 10*time.Millisecond)

	ok, wait := th.Allow("svc-a")
	if !ok || wait != 0 {
		t.Errorf("Allow after cooldown: want (true, 0), got (%v, %v)", ok, wait)
	}
}

func TestThrottle_SetServiceCooldown_OverridesDefault(t *testing.T) {
	th := agent.NewThrottle(1000) // 1s default
	th.SetServiceCooldown("fast-svc", 10)

	th.Allow("fast-svc") //nolint:errcheck
	time.Sleep(20 * time.Millisecond)

	ok, _ := th.Allow("fast-svc")
	if !ok {
		t.Error("per-service cooldown should have expired after 20ms, but request was throttled")
	}

	// Default cooldown still applies to other services.
	th.Allow("slow-svc") //nolint:errcheck
	ok2, _ := th.Allow("slow-svc")
	if ok2 {
		t.Error("default 1s cooldown should block slow-svc immediately after first call")
	}
}

func TestThrottle_Allow_IndependentKeys(t *testing.T) {
	th := agent.NewThrottle(500)

	ok1, _ := th.Allow("svc-a")
	ok2, _ := th.Allow("svc-b")
	if !ok1 || !ok2 {
		t.Fatal("different keys must not share cooldown")
	}

	ok1b, _ := th.Allow("svc-a")
	ok2b, _ := th.Allow("svc-b")
	if ok1b {
		t.Error("svc-a second call should be throttled")
	}
	if ok2b {
		t.Error("svc-b second call should be throttled")
	}
}
