package obs

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

func InitMetrics() {
	meter = otel.Meter("workspace")
	httpRequests, _ = meter.Int64Counter("workspace_http_requests_total")
	deliveriesOK, _ = meter.Int64Counter("workspace_delivery_success_total")
	deliveriesFail, _ = meter.Int64Counter("workspace_delivery_failure_total")
	imapLogins, _ = meter.Int64Counter("workspace_imap_login_attempts_total")
	smtpAuths, _ = meter.Int64Counter("workspace_smtp_auth_attempts_total")
}

func HTTPRequest(ctx context.Context) {
	if httpRequests != nil {
		httpRequests.Add(ctx, 1)
	}
}

func DeliveryOK(ctx context.Context) {
	if deliveriesOK != nil {
		deliveriesOK.Add(ctx, 1)
	}
}

func DeliveryFail(ctx context.Context) {
	if deliveriesFail != nil {
		deliveriesFail.Add(ctx, 1)
	}
}

func IMAPLogin(ctx context.Context, ok bool) {
	if imapLogins != nil {
		imapLogins.Add(ctx, 1, metric.WithAttributes(attribute.Bool("result", ok)))
	}
}

func SMTPAuth(ctx context.Context, ok bool) {
	if smtpAuths != nil {
		smtpAuths.Add(ctx, 1, metric.WithAttributes(attribute.Bool("result", ok)))
	}
}
