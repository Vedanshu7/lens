package unit_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/Vedanshu7/lens/internal/agent"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
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
	for _, tc := range tests {
		got := agent.ParseLogLevel(tc.input)
		if got != tc.want {
			t.Errorf("ParseLogLevel(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestThrottle_Allow(t *testing.T) {
	cooldown := 100 * time.Millisecond
	th := agent.NewThrottle(int(cooldown.Milliseconds()))

	ok, wait := th.Allow("svc-a")
	if !ok || wait != 0 {
		t.Fatalf("first Allow: want (true, 0), got (%v, %v)", ok, wait)
	}

	ok, wait = th.Allow("svc-a")
	if ok || wait <= 0 {
		t.Fatalf("immediate second Allow: want (false, >0), got (%v, %v)", ok, wait)
	}

	time.Sleep(cooldown + 10*time.Millisecond)

	ok, wait = th.Allow("svc-a")
	if !ok || wait != 0 {
		t.Fatalf("Allow after cooldown: want (true, 0), got (%v, %v)", ok, wait)
	}
}

func TestThrottle_IndependentKeys(t *testing.T) {
	th := agent.NewThrottle(500)

	ok1, _ := th.Allow("svc-a")
	ok2, _ := th.Allow("svc-b")
	if !ok1 || !ok2 {
		t.Fatal("different keys must not share cooldown")
	}

	// second call on svc-a should be throttled; svc-b unaffected
	ok1b, _ := th.Allow("svc-a")
	ok2b, _ := th.Allow("svc-b")
	if ok1b {
		t.Error("svc-a second call should be throttled")
	}
	if ok2b {
		t.Error("svc-b second call should be throttled")
	}
}
