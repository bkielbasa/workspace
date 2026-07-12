package main

import (
    "context"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/metric"
)

var (
    meter = otel.Meter("workspace")

    httpRequests metric.Int64Counter
    deliveriesOK metric.Int64Counter
    deliveriesFail metric.Int64Counter
)

func initMetrics() {
    httpRequests, _ = meter.Int64Counter("http_requests_total")
    deliveriesOK, _ = meter.Int64Counter("delivery_success_total")
    deliveriesFail, _ = meter.Int64Counter("delivery_failure_total")
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
