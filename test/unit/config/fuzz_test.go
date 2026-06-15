package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Vedanshu7/lens/config"
)

// FuzzConfigLoad feeds arbitrary bytes as lens.yaml content and verifies
// the loader never panics regardless of input. It must either return a
// valid File or a non-nil error — never panic.
func FuzzConfigLoad(f *testing.F) {
	f.Add([]byte("apiVersion: v1\nkind: LensConfig\n"))
	f.Add([]byte("transport:\n  provider: grpc\n"))
	f.Add([]byte(""))
	f.Add([]byte("not: valid: yaml: at: all: :::"))
	f.Add([]byte("agent:\n  cooldownMs: -1\n  port: \"abc\"\n"))
	f.Add([]byte(string(make([]byte, 65536))))

	f.Fuzz(func(t *testing.T, data []byte) {
		tmp := filepath.Join(t.TempDir(), "lens.yaml")
		if err := os.WriteFile(tmp, data, 0600); err != nil {
			t.Skip("cannot write temp file")
		}
		// Must not panic — error is acceptable.
		_, _ = config.Load(tmp)
	})
}
