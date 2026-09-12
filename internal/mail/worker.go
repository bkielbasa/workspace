package mail

import (
	"context"
	"fmt"
	"log/slog"
	stdmail "net/mail"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/obs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type DKIMSigner interface {
	Sign(raw string) (string, error)
}

type Worker struct {
	outbox   *Outbox
	delivery *Delivery
	dkim     DKIMSigner
	hostname string
	tracer   trace.Tracer
}

func NewWorker(outbox *Outbox, delivery *Delivery, dkim DKIMSigner, hostname string) *Worker {
	return &Worker{
		outbox:   outbox,
		delivery: delivery,
		dkim:     dkim,
		hostname: hostname,
		tracer:   otel.Tracer("worker"),
	}
}

func (w *Worker) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.RunOnce(ctx)
			}
		}
	}()
}

func (w *Worker) RunOnce(ctx context.Context) {
	ctx, span := w.tracer.Start(ctx, "worker.run_once")
	defer span.End()

	msgs, err := w.outbox.FetchBatch(ctx, 10)
	if err != nil {
		obs.Log(ctx, slog.LevelError, "outbox fetch error", "error", err)
		return
	}

	for _, m := range msgs {
		w.processMessage(ctx, m)
	}
}

func (w *Worker) processMessage(ctx context.Context, m OutboxMessage) {
	ctx, span := w.tracer.Start(ctx, "worker.process_message")
	defer span.End()

	raw := m.Data

	if w.dkim != nil {
		signed, err := w.dkim.Sign(raw)
		if err == nil {
			raw = signed
		} else {
			obs.Log(ctx, slog.LevelWarn, "dkim sign error", "error", err)
		}
	}

	from := extractFrom(raw)
	raw = prepareOutboundMessage(raw, from, m.Recipient, w.hostname)

	err := w.delivery.DeliverOutbound(m.Recipient, &Message{
		RawMessage: raw,
		Sender:     from,
	})
	if err != nil {
		_ = w.outbox.MarkFailure(ctx, m.ID, m.Attempts)
		span.RecordError(err)
		obs.Log(ctx, slog.LevelWarn, "outbox delivery failed",
			"id", m.ID, "recipient", m.Recipient, "error", err)
		return
	}

	_ = w.outbox.MarkSuccess(ctx, m.ID)
	obs.Log(ctx, slog.LevelInfo, "outbox message delivered",
		"id", m.ID, "recipient", m.Recipient)
}

func extractFrom(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) >= 5 && strings.EqualFold(line[:5], "from:") {
			value := strings.TrimSpace(line[5:])
			if address, err := stdmail.ParseAddress(value); err == nil {
				return address.Address
			}
			return value
		}
	}
	return ""
}

func prepareOutboundMessage(raw, from, to, hostname string) string {
	headers, body := splitHeaderBody(raw)

	h := map[string]string{}
	for _, line := range strings.Split(headers, "\r\n") {
		if i := strings.Index(line, ":"); i > 0 {
			k := strings.ToLower(strings.TrimSpace(line[:i]))
			v := strings.TrimSpace(line[i+1:])
			h[k] = v
		}
	}

	if h["date"] == "" {
		headers = "Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" + headers
	}

	if h["message-id"] == "" && hostname != "" {
		headers = fmt.Sprintf("Message-ID: <%d@%s>\r\n%s", time.Now().UnixNano(), hostname, headers)
	}

	if h["from"] == "" && from != "" {
		headers = "From: " + from + "\r\n" + headers
	}

	if h["to"] == "" && to != "" {
		headers = "To: " + to + "\r\n" + headers
	}

	if h["mime-version"] == "" {
		headers = "MIME-Version: 1.0\r\n" + headers
	}

	if h["content-type"] == "" {
		headers = "Content-Type: text/plain; charset=UTF-8\r\n" + headers
	}

	if body != "" {
		return headers + "\r\n" + body
	}
	return headers
}

func splitHeaderBody(raw string) (string, string) {
	parts := strings.SplitN(raw, "\r\n\r\n", 2)
	if len(parts) != 2 {
		parts = strings.SplitN(raw, "\n\n", 2)
		if len(parts) != 2 {
			return raw, ""
		}
	}
	return parts[0], parts[1]
}
