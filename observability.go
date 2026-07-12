package main

import (
    "context"
    "log"
    "time"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

func initTracer(ctx context.Context) func() {
    exporter, err := otlptracehttp.New(ctx,
        otlptracehttp.WithEndpoint("lgtm:4318"),
        otlptracehttp.WithInsecure(),
    )
    if err != nil {
        log.Printf("otel exporter error: %v", err)
        return func() {}
    }

    res, _ := resource.New(ctx,
        resource.WithAttributes(
            semconv.ServiceName("workspace"),
        ),
    )

    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(res),
    )

    otel.SetTracerProvider(tp)

    return func() {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        _ = tp.Shutdown(ctx)
    }
}
