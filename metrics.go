package main

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter          metric.Meter
	httpRequests   metric.Int64Counter
	deliveriesOK   metric.Int64Counter
	deliveriesFail metric.Int64Counter
	imapLogins     metric.Int64Counter
	smtpAuths      metric.Int64Counter
)

func initMetrics() {
	// The meter must be created after the global MeterProvider is set
	// (initTelemetry), otherwise it binds to a no-op provider.
	meter = otel.Meter("workspace")

	httpRequests, _ = meter.Int64Counter("workspace_http_requests_total",
		metric.WithDescription("Inbound HTTP requests"),
	)
	deliveriesOK, _ = meter.Int64Counter("workspace_delivery_success_total",
		metric.WithDescription("Successfully delivered messages"),
	)
	deliveriesFail, _ = meter.Int64Counter("workspace_delivery_failure_total",
		metric.WithDescription("Failed message deliveries"),
	)
	imapLogins, _ = meter.Int64Counter("workspace_imap_login_attempts_total",
		metric.WithDescription("IMAP login attempts"),
	)
	smtpAuths, _ = meter.Int64Counter("workspace_smtp_auth_attempts_total",
		metric.WithDescription("SMTP AUTH attempts"),
	)
}

func incHTTP(ctx context.Context) {
	httpRequests.Add(ctx, 1)
}

func incDeliveryOK(ctx context.Context) {
	deliveriesOK.Add(ctx, 1)
}

func incDeliveryFail(ctx context.Context) {
	deliveriesFail.Add(ctx, 1)
}

func incIMAPLogin(ctx context.Context, ok bool) {
	imapLogins.Add(ctx, 1, metric.WithAttributes(
		attribute.Bool("result", ok),
	))
}

func incSMTPAuth(ctx context.Context, ok bool) {
	smtpAuths.Add(ctx, 1, metric.WithAttributes(
		attribute.Bool("result", ok),
	))
}
