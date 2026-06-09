// Package stdoutobserver writes structured Lens telemetry events to standard output.
// Events are encoded as JSON lines by default, or as a human-readable text line when
// format is set to "text". Suitable for capture by any log-shipping agent.
package stdoutobserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/vedanshu/lens/internal/observability"
)

func init() {
	observability.Register("stdout", func(cfg map[string]any) (observability.Observer, error) {
		format, _ := cfg["format"].(string)
		if format == "" {
			format = "json"
		}
		return &stdoutObserver{format: format}, nil
	})
}

type stdoutObserver struct{ format string }

// Record writes event e to stdout. format "text" emits a single log line;
// any other value encodes e as a JSON object followed by a newline.
func (s *stdoutObserver) Record(_ context.Context, e observability.Event) error {
	var err error
	switch s.format {
	case "text":
		_, err = fmt.Fprintf(os.Stdout, "[lens] kind=%s service=%s instance=%s success=%v latency_ms=%.2f\n",
			e.Kind, e.Service, e.Instance, e.Success, e.LatencyMs)
	default:
		err = json.NewEncoder(os.Stdout).Encode(e)
	}
	return err
}

// Close is a no-op and returns nil.
func (s *stdoutObserver) Close() error { return nil }
