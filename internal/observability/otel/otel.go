// Package otelobserver exports Lens telemetry events via OpenTelemetry (OTLP).
// Supports any OTLP-compatible backend: Jaeger, Grafana Tempo, Datadog,
// Honeycomb, New Relic, Dynatrace, and others.
//
// Build with: go build -tags lens_otel ./...
package otelobserver

import (
	"context"
	"fmt"

	"github.com/Vedanshu7/lens/internal/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

func init() {
	observability.Register("otel", func(cfg map[string]any) (observability.Observer, error) {
		endpoint, _ := cfg["endpoint"].(string)
		serviceName, _ := cfg["serviceName"].(string)
		if serviceName == "" {
			serviceName = "lens"
		}

		ctx := context.Background()
		exp, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(endpoint),
		)
		if err != nil {
			return nil, fmt.Errorf("otel: exporter: %w", err)
		}

		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(resource.NewWithAttributes(
				semconv.SchemaURL,
				semconv.ServiceNameKey.String(serviceName),
			)),
		)
		otel.SetTracerProvider(tp)

		return &otelObserver{
			tp:     tp,
			tracer: tp.Tracer("lens"),
		}, nil
	})
}

type otelObserver struct {
	tp     *sdktrace.TracerProvider
	tracer trace.Tracer
}

// Record creates an OTLP span for event e, attaching service, instance,
// transport, latency, and any error or pattern attributes.
func (o *otelObserver) Record(ctx context.Context, e observability.Event) error {
	_, span := o.tracer.Start(ctx, string(e.Kind))
	defer span.End()

	span.SetAttributes(
		attribute.String("lens.service", e.Service),
		attribute.String("lens.instance", e.Instance),
		attribute.String("lens.transport", e.Transport),
		attribute.Bool("lens.success", e.Success),
		attribute.Float64("lens.latency_ms", e.LatencyMs),
	)
	if e.Error != "" {
		span.SetAttributes(attribute.String("lens.error", e.Error))
	}
	if e.Pattern != nil {
		span.SetAttributes(attribute.String("lens.pattern", *e.Pattern))
	}
	if e.Key != nil {
		span.SetAttributes(attribute.String("lens.key", *e.Key))
	}
	return nil
}

// Close flushes any buffered spans and shuts down the tracer provider.
func (o *otelObserver) Close() error {
	return o.tp.Shutdown(context.Background())
}
