package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type Search struct {
	db     *sql.DB
	tracer trace.Tracer
}

func NewSearch(db *sql.DB) *Search {
	return &Search{db: db, tracer: otel.Tracer("search")}
}

func (s *Search) Messages(ctx context.Context, userID uuid.UUID, q string) ([]Message, error) {
	ctx, span := s.tracer.Start(ctx, "search.messages")
	defer span.End()

	rows, err := s.db.QueryContext(ctx, `
        SELECT m.id, m.mailbox_id, m.message_id, m.sender, m.recipients,
               m.subject, m.in_reply_to, m.references_header, m.size_bytes,
               m.seen, m.flagged, m.answered, m.deleted, m.draft,
               m.received_at, m.created_at, m.updated_at
        FROM messages m
        JOIN mailboxes mb ON m.mailbox_id = mb.id
        WHERE mb.user_id = $1
          AND (m.subject ILIKE '%' || $2 || '%' OR m.raw_message ILIKE '%' || $2 || '%')
        ORDER BY m.received_at DESC
        LIMIT 50
    `, userID, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(
			&m.ID, &m.MailboxID, &m.MessageID, &m.Sender, &m.Recipients,
			&m.Subject, &m.InReplyTo, &m.References, &m.SizeBytes,
			&m.Seen, &m.Flagged, &m.Answered, &m.Deleted, &m.Draft,
			&m.ReceivedAt, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
