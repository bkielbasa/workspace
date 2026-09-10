package obs

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"
)

var logger *slog.Logger

// Init installs the JSON logger used by every package. It also becomes the
// slog default so output from dependencies that still use the standard log
// package is emitted in the same format instead of plain text.
func Init() {
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
}

// Log emits a structured record, correlated with the span in ctx when there
// is one.
func Log(ctx context.Context, level slog.Level, msg string, attrs ...any) {
	span := trace.SpanFromContext(ctx)
	sc := span.SpanContext()
	if sc.IsValid() {
		attrs = append(attrs, "trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
	}
	l := logger
	if l == nil {
		l = slog.Default()
	}
	l.Log(ctx, level, msg, attrs...)
}

// Fatal logs at error level and terminates the process. It replaces
// log.Fatal so startup failures keep the same format as everything else.
func Fatal(ctx context.Context, msg string, attrs ...any) {
	Log(ctx, slog.LevelError, msg, attrs...)
	os.Exit(1)
}
