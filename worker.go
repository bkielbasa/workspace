package main

import (
	"context"
	"log"
	stdmail "net/mail"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type Worker struct {
	outbox   *Outbox
	delivery *Delivery
	dkim     *DKIM
	tracer   trace.Tracer
}

func NewWorker(outbox *Outbox, delivery *Delivery, dkim *DKIM) *Worker {
	return &Worker{
		outbox:   outbox,
		delivery: delivery,
		dkim:     dkim,
		tracer:   otel.Tracer("worker"),
	}
}

func (w *Worker) Start() {
	go func() {
		for {
			w.runOnce()
			time.Sleep(5 * time.Second)
		}
	}()
}

func (w *Worker) runOnce() {
	ctx := context.Background()

	ctx, span := w.tracer.Start(ctx, "worker.runOnce")
	defer span.End()

	msgs, err := w.outbox.FetchBatch(ctx, 10)
	if err != nil {
		log.Println("outbox fetch error:", err)
		return
	}

	for _, m := range msgs {
		ctx, msgSpan := w.tracer.Start(ctx, "worker.processMessage")
		raw := m.Data

		if w.dkim != nil {
			signed, err := w.dkim.Sign(raw)
			if err == nil {
				raw = signed
			}
		}

		from := extractFrom(raw)
		raw = prepareMessage(raw, from, m.Recipient)

		err := w.delivery.deliverOutbound(m.Recipient, &Message{
			RawMessage: raw,
			Sender:     from,
		})
		if err != nil {
			_ = w.outbox.MarkFailure(ctx, m.ID, m.Attempts)
			msgSpan.RecordError(err)
			msgSpan.End()
			continue
		}

		_ = w.outbox.MarkSuccess(ctx, m.ID)
		msgSpan.End()
	}
}

func extractFrom(raw string) string {
	// very basic: look for "From:" line
	for _, line := range splitLines(raw) {
		if len(line) > 5 && line[:5] == "From:" {
			value := strings.TrimSpace(line[5:])
			if address, err := stdmail.ParseAddress(value); err == nil {
				return address.Address
			}
			return value
		}
	}
	return ""
}

func splitLines(s string) []string {
	var res []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			res = append(res, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		res = append(res, s[start:])
	}
	return res
}
