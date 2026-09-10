package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

type mailboxRepository struct{ db *sql.DB }

func NewMailboxRepository(db *sql.DB) mail.MailboxRepository {
	return &mailboxRepository{db: db}
}

func (r *mailboxRepository) CreateDefault(ctx context.Context, userID uuid.UUID) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := createDefaultMailboxes(ctx, tx, userID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (r *mailboxRepository) List(ctx context.Context, userID uuid.UUID) ([]mail.Mailbox, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, name, uid_validity, created_at
		FROM mailboxes WHERE user_id = $1 ORDER BY id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list mailboxes: %w", err)
	}
	defer rows.Close()
	mailboxes := make([]mail.Mailbox, 0)
	for rows.Next() {
		var mailbox mail.Mailbox
		if err := rows.Scan(&mailbox.ID, &mailbox.UserID, &mailbox.Name,
			&mailbox.UIDValidity, &mailbox.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan mailbox: %w", err)
		}
		mailboxes = append(mailboxes, mailbox)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mailboxes: %w", err)
	}
	return mailboxes, nil
}

func (r *mailboxRepository) ListWithCounts(ctx context.Context, userID uuid.UUID) ([]mail.MailboxInfo, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT mb.id, mb.user_id, mb.name,
		       COUNT(m.id) AS total_count,
		       COUNT(m.id) FILTER (WHERE NOT m.seen) AS unread_count
		FROM mailboxes mb
		LEFT JOIN messages m ON m.mailbox_id = mb.id
		WHERE mb.user_id = $1
		GROUP BY mb.id, mb.user_id, mb.name
		ORDER BY mb.created_at, mb.id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list mailboxes with counts: %w", err)
	}
	defer rows.Close()
	var list []mail.MailboxInfo
	for rows.Next() {
		var info mail.MailboxInfo
		if err := rows.Scan(&info.ID, &info.UserID, &info.Name, &info.TotalCount, &info.UnreadCount); err != nil {
			return nil, fmt.Errorf("scan mailbox info: %w", err)
		}
		list = append(list, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mailboxes: %w", err)
	}
	return list, nil
}

func (r *mailboxRepository) GetByName(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	mailbox := &mail.Mailbox{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, uid_validity, created_at
		FROM mailboxes WHERE user_id = $1 AND name = $2
	`, userID, name).Scan(
		&mailbox.ID, &mailbox.UserID, &mailbox.Name, &mailbox.UIDValidity, &mailbox.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, mail.ErrMailboxNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get mailbox: %w", err)
	}
	return mailbox, nil
}

func (r *mailboxRepository) Create(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	mailbox := &mail.Mailbox{}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO mailboxes (user_id, name) VALUES ($1, $2)
		RETURNING id, user_id, name, uid_validity, created_at
	`, userID, name).Scan(
		&mailbox.ID, &mailbox.UserID, &mailbox.Name, &mailbox.UIDValidity, &mailbox.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create mailbox: %w", err)
	}
	return mailbox, nil
}

type messageRepository struct{ db *sql.DB }

func NewMessageRepository(db *sql.DB) mail.MessageRepository {
	return &messageRepository{db: db}
}

func (r *messageRepository) Append(ctx context.Context, message *mail.Message) error {
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO messages (
			mailbox_id, message_id, sender, recipients, subject, in_reply_to,
			references_header, raw_message, mime_type, charset, size_bytes,
			seen, flagged, answered, deleted, draft, received_at, sent_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING id, uid, created_at, updated_at
	`, message.MailboxID, message.MessageID, message.Sender, message.Recipients,
		message.Subject, message.InReplyTo, message.References, message.RawMessage,
		message.MimeType, message.Charset, message.SizeBytes, message.Seen,
		message.Flagged, message.Answered, message.Deleted, message.Draft,
		message.ReceivedAt, message.SentAt,
	).Scan(&message.ID, &message.UID, &message.CreatedAt, &message.UpdatedAt)
	if err != nil {
		return fmt.Errorf("append message: %w", err)
	}
	return nil
}

const messageColumns = `
	id, mailbox_id, uid, message_id, sender,
	array_to_string(recipients, ',') AS recipients, subject, in_reply_to,
	references_header, raw_message, mime_type, charset, size_bytes,
	seen, flagged, answered, deleted, draft, received_at, sent_at,
	created_at, updated_at`

const joinedMessageColumns = `
	m.id, m.mailbox_id, m.uid, m.message_id, m.sender,
	array_to_string(m.recipients, ',') AS recipients, m.subject, m.in_reply_to,
	m.references_header, m.raw_message, m.mime_type, m.charset, m.size_bytes,
	m.seen, m.flagged, m.answered, m.deleted, m.draft, m.received_at, m.sent_at,
	m.created_at, m.updated_at`

func (r *messageRepository) Get(ctx context.Context, id uuid.UUID) (*mail.Message, error) {
	message, err := scanMessage(r.db.QueryRowContext(ctx,
		`SELECT `+messageColumns+` FROM messages WHERE id = $1`, id), true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, mail.ErrMessageNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get message: %w", err)
	}
	return &message, nil
}

func (r *messageRepository) GetForUser(ctx context.Context, userID, id uuid.UUID) (*mail.Message, *mail.Mailbox, error) {
	var (
		message    mail.Message
		mailbox    mail.Mailbox
		recipients string
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT m.id, m.mailbox_id, m.uid, m.message_id, m.sender,
		       array_to_string(m.recipients, ',') AS recipients, m.subject, m.in_reply_to,
		       m.references_header, m.raw_message, m.mime_type, m.charset, m.size_bytes,
		       m.seen, m.flagged, m.answered, m.deleted, m.draft, m.received_at, m.sent_at,
		       m.created_at, m.updated_at,
		       mb.id, mb.user_id, mb.name, mb.uid_validity, mb.created_at
		FROM messages m
		JOIN mailboxes mb ON m.mailbox_id = mb.id
		WHERE m.id = $1 AND mb.user_id = $2
	`, id, userID).Scan(
		&message.ID, &message.MailboxID, &message.UID, &message.MessageID,
		&message.Sender, &recipients, &message.Subject, &message.InReplyTo,
		&message.References, &message.RawMessage, &message.MimeType, &message.Charset,
		&message.SizeBytes, &message.Seen, &message.Flagged, &message.Answered,
		&message.Deleted, &message.Draft, &message.ReceivedAt, &message.SentAt,
		&message.CreatedAt, &message.UpdatedAt,
		&mailbox.ID, &mailbox.UserID, &mailbox.Name, &mailbox.UIDValidity, &mailbox.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, mail.ErrMessageNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get message for user: %w", err)
	}
	message.Recipients = parseRecipients(recipients)
	return &message, &mailbox, nil
}

func (r *messageRepository) List(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+messageColumns+` FROM messages
		WHERE mailbox_id = $1 ORDER BY received_at DESC LIMIT $2 OFFSET $3
	`, mailboxID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	messages := make([]mail.Message, 0)
	for rows.Next() {
		message, err := scanMessage(rows, true)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return messages, nil
}

func (r *messageRepository) UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE messages SET seen=$2, flagged=$3, answered=$4, deleted=$5,
			draft=$6, updated_at=NOW() WHERE id=$1
	`, id, seen, flagged, answered, deleted, draft)
	return affectedOrNotFound(result, err, "update flags", mail.ErrMessageNotFound)
}

func (r *messageRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM messages WHERE id = $1`, id)
	return affectedOrNotFound(result, err, "delete message", mail.ErrMessageNotFound)
}

func (r *messageRepository) Move(ctx context.Context, id, mailboxID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE messages SET mailbox_id = $2, updated_at = NOW() WHERE id = $1
	`, id, mailboxID)
	return affectedOrNotFound(result, err, "move message", mail.ErrMessageNotFound)
}

type threadRepository struct{ db *sql.DB }

func NewThreadRepository(db *sql.DB) mail.ThreadRepository {
	return &threadRepository{db: db}
}

func (r *threadRepository) List(ctx context.Context, userID uuid.UUID) ([]mail.Thread, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(thread_id, m.id), subject, COUNT(*)
		FROM messages m JOIN mailboxes mb ON m.mailbox_id = mb.id
		WHERE mb.user_id = $1
		GROUP BY COALESCE(thread_id, m.id), subject
		ORDER BY MAX(received_at) DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var threads []mail.Thread
	for rows.Next() {
		var thread mail.Thread
		if err := rows.Scan(&thread.ID, &thread.Subject, &thread.Count); err != nil {
			return nil, err
		}
		threads = append(threads, thread)
	}
	return threads, rows.Err()
}

func (r *threadRepository) FindByMessageID(ctx context.Context, messageID string) (uuid.UUID, error) {
	var threadID uuid.UUID
	err := r.db.QueryRowContext(ctx, `
		SELECT thread_id FROM messages WHERE message_id = $1 LIMIT 1
	`, messageID).Scan(&threadID)
	return threadID, err
}

func (r *threadRepository) FindBySubject(ctx context.Context, subject string) (uuid.UUID, error) {
	var threadID uuid.UUID
	err := r.db.QueryRowContext(ctx, `
		SELECT thread_id FROM messages WHERE subject = $1 AND thread_id IS NOT NULL LIMIT 1
	`, subject).Scan(&threadID)
	return threadID, err
}

func (r *threadRepository) SetThreadID(ctx context.Context, messageID, threadID uuid.UUID) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE messages SET thread_id = $2 WHERE id = $1`, messageID, threadID); err != nil {
		return fmt.Errorf("assign thread: %w", err)
	}
	return nil
}

type outboxRepository struct{ db *sql.DB }

func NewOutboxRepository(db *sql.DB) mail.OutboxRepository {
	return &outboxRepository{db: db}
}

func (r *outboxRepository) Enqueue(ctx context.Context, recipient, data string) error {
	if _, err := r.db.ExecContext(ctx, `INSERT INTO outbox (recipient, data) VALUES ($1, $2)`, recipient, data); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	return nil
}

func (r *outboxRepository) FetchBatch(ctx context.Context, limit int) ([]mail.OutboxMessage, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, recipient, data, attempts FROM outbox
		WHERE next_attempt_at <= NOW() ORDER BY created_at LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []mail.OutboxMessage
	for rows.Next() {
		var message mail.OutboxMessage
		if err := rows.Scan(&message.ID, &message.Recipient, &message.Data, &message.Attempts); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (r *outboxRepository) MarkSuccess(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM outbox WHERE id = $1`, id)
	return err
}

func (r *outboxRepository) MarkFailure(ctx context.Context, id string, attempts int) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE outbox SET attempts = $2, next_attempt_at = $3 WHERE id = $1
	`, id, attempts+1, time.Now().Add(time.Duration(attempts+1)*time.Minute))
	return err
}

type searchRepository struct{ db *sql.DB }

func NewSearchRepository(db *sql.DB) mail.SearchRepository {
	return &searchRepository{db: db}
}

func (r *searchRepository) Messages(ctx context.Context, userID uuid.UUID, query string) ([]mail.Message, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+joinedMessageColumns+`
		FROM messages m JOIN mailboxes mb ON m.mailbox_id = mb.id
		WHERE mb.user_id = $1
		  AND (m.subject ILIKE '%' || $2 || '%' OR m.raw_message ILIKE '%' || $2 || '%')
		ORDER BY m.received_at DESC LIMIT 50
	`, userID, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []mail.Message
	for rows.Next() {
		message, err := scanMessage(rows, true)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

type labelRepository struct{ db *sql.DB }

func NewLabelRepository(db *sql.DB) mail.LabelRepository {
	return &labelRepository{db: db}
}

func (r *labelRepository) Create(ctx context.Context, userID uuid.UUID, name string) (*mail.Label, error) {
	label := &mail.Label{}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO labels (user_id, name) VALUES ($1, $2)
		RETURNING id, user_id, name
	`, userID, name).Scan(&label.ID, &label.UserID, &label.Name)
	if err != nil {
		return nil, fmt.Errorf("create label: %w", err)
	}
	return label, nil
}

func (r *labelRepository) AddToMessage(ctx context.Context, messageID, labelID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO message_labels (message_id, label_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, messageID, labelID)
	return err
}

func (r *labelRepository) RemoveFromMessage(ctx context.Context, messageID, labelID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM message_labels WHERE message_id = $1 AND label_id = $2
	`, messageID, labelID)
	return err
}
