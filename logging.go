package main

import (
    "context"
    "log/slog"
    "os"

    "go.opentelemetry.io/otel/trace"
)

var logger *slog.Logger

func initLogger() {
    handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo,
    })
    logger = slog.New(handler)
}

func logWithTrace(ctx context.Context, level slog.Level, msg string, attrs ...any) {
    span := trace.SpanFromContext(ctx)
    sc := span.SpanContext()

    if sc.IsValid() {
        attrs = append(attrs,
            "trace_id", sc.TraceID().String(),
            "span_id", sc.SpanID().String(),
        )
    }

    logger.Log(ctx, level, msg, attrs...)
}
