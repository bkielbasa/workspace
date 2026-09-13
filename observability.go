package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/bklimczak/workspace/internal/obs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

// otlpEndpoint returns the OTLP receiver used by the cluster's observability
// stack (Grafana Alloy), overridable per environment.
func otlpEndpoint() string {
	if v := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); v != "" {
		return v
	}
	return "alloy.monitoring:4318"
}

func initTelemetry(ctx context.Context) func() {
	stop := func() {}

	sel, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("workspace"),
		),
	)

	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(otlpEndpoint()),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		obs.Log(ctx, slog.LevelError, "otel trace exporter error", "error", err)
		return stop
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(sel),
	)
	otel.SetTracerProvider(tp)

	// Expose the app's counters as Prometheus metrics via the same OTLP
	// receiver; new runtime/host metrics are intentionally limited to app
	// instrumentation so the dashboards stay scoped to the service.
	mexp, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint(otlpEndpoint()),
		otlpmetrichttp.WithInsecure(),
	)
	if err != nil {
		obs.Log(ctx, slog.LevelError, "otel metric exporter error", "error", err)
		return stop
	}

	readers := []metric.Reader{
		metric.NewPeriodicReader(mexp, metric.WithInterval(15*time.Second)),
	}

	// Prometheus pull path for the prometheus.io/scrape ServiceMonitor:
	// the same counters are also readable at GET /metrics. A missing
	// reader here only disables scraping; OTLP delivery above still works.
	if preader, err := otelprom.New(); err != nil {
		obs.Log(ctx, slog.LevelError, "prometheus metric reader error", "error", err)
	} else {
		readers = append(readers, preader)
	}

	opts := make([]metric.Option, 0, len(readers)+1)
	for _, r := range readers {
		opts = append(opts, metric.WithReader(r))
	}
	opts = append(opts, metric.WithResource(sel))
	mp := metric.NewMeterProvider(opts...)
	otel.SetMeterProvider(mp)

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = mp.Shutdown(ctx)
		_ = tp.Shutdown(ctx)
	}
}
