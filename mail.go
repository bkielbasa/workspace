package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"log/slog"
)

var ErrMessageNotFound = errors.New("message not found")

type Message struct {
	ID        uuid.UUID
	MailboxID uuid.UUID
	UID       uint64

	MessageID string

	Sender     string
	Recipients []string

	Subject string

	InReplyTo  string
	References string

	RawMessage string

	MimeType string
	Charset  string

	SizeBytes int64

	Seen     bool
	Flagged  bool
	Answered bool
	Deleted  bool
	Draft    bool

	ReceivedAt time.Time
	SentAt     sql.NullTime

	CreatedAt time.Time
	UpdatedAt time.Time
}

type Mail struct {
	db *sql.DB
}

func (m *Mail) Append(ctx context.Context, message *Message) error {
	err := m.db.QueryRowContext(
		ctx,
		`
		INSERT INTO messages (
			mailbox_id,
			message_id,

			sender,
			recipients,

			subject,

			in_reply_to,
			references_header,

			raw_message,

			mime_type,
			charset,

			size_bytes,

			seen,
			flagged,
			answered,
			deleted,
			draft,

			received_at,
			sent_at
		)
		VALUES (
			$1,$2,
			$3,$4,
			$5,
			$6,$7,
			$8,
			$9,$10,
			$11,
			$12,$13,$14,$15,$16,
			$17,$18
		)
		RETURNING
			id,
			uid,
			created_at,
			updated_at
		`,
		message.MailboxID,
		message.MessageID,

		message.Sender,
		message.Recipients,

		message.Subject,

		message.InReplyTo,
		message.References,

		message.RawMessage,

		message.MimeType,
		message.Charset,

		message.SizeBytes,

		message.Seen,
		message.Flagged,
		message.Answered,
		message.Deleted,
		message.Draft,

		message.ReceivedAt,
		message.SentAt,
	).Scan(
		&message.ID,
		&message.UID,
		&message.CreatedAt,
		&message.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("append message: %w", err)
	}

	return nil
}

func (m *Mail) Get(ctx context.Context, id uuid.UUID) (*Message, error) {
	message := &Message{}
	var recipientsRaw string

	err := m.db.QueryRowContext(
		ctx,
		`
		SELECT
			id,
			mailbox_id,
			uid,

message_id,

			sender,
			array_to_string(recipients, ',') AS recipients,

			subject,

			in_reply_to,
			references_header,

			raw_message,

			mime_type,

			charset,

			size_bytes,

			seen,
			flagged,
			answered,
			deleted,
			draft,

			received_at,
			sent_at,

			created_at,
			updated_at
		FROM messages
		WHERE id = $1
		`,
		id,
	).Scan(
		&message.ID,
		&message.MailboxID,
		&message.UID,

		&message.MessageID,

		&message.Sender,
		&recipientsRaw,

		&message.Subject,

		&message.InReplyTo,
		&message.References,

		&message.RawMessage,

		&message.MimeType,
		&message.Charset,

		&message.SizeBytes,

		&message.Seen,
		&message.Flagged,
		&message.Answered,
		&message.Deleted,
		&message.Draft,

		&message.ReceivedAt,
		&message.SentAt,

		&message.CreatedAt,
		&message.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMessageNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("get message: %w", err)
	}

	message.Recipients = parseRecipients(recipientsRaw)

	return message, nil
}

// parseRecipients converts a comma-joined recipients string into a slice.
func parseRecipients(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	recipients := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			recipients = append(recipients, p)
		}
	}
	return recipients
}

func (m *Mail) List(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]Message, error) {
	logWithTrace(ctx, slog.LevelInfo, "mail.List called",
		"mailbox_id", mailboxID.String(),
		"limit", limit,
		"offset", offset,
	)
	rows, err := m.db.QueryContext(
		ctx,
		`
		SELECT
			id,
			mailbox_id,
			uid,

			message_id,

			sender,
			array_to_string(recipients, ',') AS recipients,

			subject,

			in_reply_to,
			references_header,

			raw_message,

			size_bytes,

			seen,
			flagged,
			answered,
			deleted,
			draft,

			received_at,

			created_at,
			updated_at
		FROM messages
		WHERE mailbox_id = $1
		ORDER BY received_at DESC
		LIMIT $2
		OFFSET $3
		`,
		mailboxID.String(),
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	messages := make([]Message, 0)

	for rows.Next() {
		var message Message
		var recipientsRaw string

		err := rows.Scan(
			&message.ID,
			&message.MailboxID,
			&message.UID,

			&message.MessageID,

			&message.Sender,
			&recipientsRaw,

			&message.Subject,

			&message.InReplyTo,
			&message.References,

			&message.RawMessage,

			&message.SizeBytes,

			&message.Seen,
			&message.Flagged,
			&message.Answered,
			&message.Deleted,
			&message.Draft,

			&message.ReceivedAt,

			&message.CreatedAt,
			&message.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}

		message.Recipients = parseRecipients(recipientsRaw)

		logWithTrace(ctx, slog.LevelDebug, "mail.List row",
			"id", message.ID.String(),
			"mailbox_id", message.MailboxID.String(),
			"subject", message.Subject,
			"received_at", message.ReceivedAt,
		)

		messages = append(messages, message)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}

	return messages, nil
}

func (m *Mail) UpdateFlags(
	ctx context.Context,
	id uuid.UUID,
	seen bool,
	flagged bool,
	answered bool,
	deleted bool,
	draft bool,
) error {
	result, err := m.db.ExecContext(
		ctx,
		`
		UPDATE messages
		SET
			seen = $2,
			flagged = $3,
			answered = $4,
			deleted = $5,
			draft = $6,
			updated_at = NOW()
		WHERE id = $1
		`,
		id,
		seen,
		flagged,
		answered,
		deleted,
		draft,
	)
	if err != nil {
		return fmt.Errorf("update flags: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rows == 0 {
		return ErrMessageNotFound
	}

	return nil
}

func (m *Mail) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := m.db.ExecContext(
		ctx,
		`
		DELETE FROM messages
		WHERE id = $1
		`,
		id,
	)
	if err != nil {
		return fmt.Errorf("delete message: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rows == 0 {
		return ErrMessageNotFound
	}

	return nil
}

func (m *Mail) Move(ctx context.Context, id, mailboxID uuid.UUID) error {
	result, err := m.db.ExecContext(ctx, `
		UPDATE messages
		SET mailbox_id = $2, updated_at = NOW()
		WHERE id = $1
	`, id, mailboxID)
	if err != nil {
		return fmt.Errorf("move message: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("move rows affected: %w", err)
	}
	if rows == 0 {
		return ErrMessageNotFound
	}
	return nil
}

func (m *Mail) Copy(ctx context.Context, id, mailboxID uuid.UUID) (*Message, error) {
	message, err := m.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	copy := *message
	copy.ID = uuid.Nil
	copy.UID = 0
	copy.MailboxID = mailboxID
	copy.Recipients = append([]string(nil), message.Recipients...)
	if err := m.Append(ctx, &copy); err != nil {
		return nil, err
	}
	return &copy, nil
}
