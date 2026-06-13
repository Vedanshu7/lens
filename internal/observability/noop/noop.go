// Package noop provides a no-op observability observer that discards all events.
// It is the default when observer mode is disabled, adding zero overhead to the hot path.
package noop

import (
	"context"

	"github.com/Vedanshu7/lens/internal/observability"
)

func init() {
	observability.Register("noop", func(_ map[string]any) (observability.Observer, error) {
		return &noopObserver{}, nil
	})
}

type noopObserver struct{}

// Record discards the event and returns nil.
func (*noopObserver) Record(_ context.Context, _ observability.Event) error { return nil }

// Close is a no-op and returns nil.
func (*noopObserver) Close() error { return nil }
