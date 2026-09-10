package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var defaultMailboxes = []string{
	"INBOX",
	"Sent",
	"Drafts",
	"Trash",
	"Archive",
	"Spam",
	"All",
	"Important",
}

type Mailbox struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Name        string
	UIDValidity uint64
	CreatedAt   time.Time
}

type Mailboxes struct {
	db     *sql.DB
	tracer trace.Tracer
}

func NewMailboxes(db *sql.DB) *Mailboxes {
	return &Mailboxes{db: db, tracer: otel.Tracer("mailboxes")}
}

func (m *Mailboxes) CreateDefault(ctx context.Context, userID uuid.UUID) error {
	ctx, span := m.tracer.Start(ctx, "mailboxes.create_default")
	defer span.End()

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := m.CreateDefaultTx(ctx, tx, userID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (m *Mailboxes) CreateDefaultTx(ctx context.Context, tx *sql.Tx, userID uuid.UUID) error {
	ctx, span := m.tracer.Start(ctx, "mailboxes.create_default_tx")
	defer span.End()

	for _, name := range defaultMailboxes {
		_, err := tx.ExecContext(
			ctx,
			`
			INSERT INTO mailboxes (
				user_id,
				name
			)
			VALUES ($1, $2)
			`,
			userID,
			name,
		)
		if err != nil {
			return fmt.Errorf("create mailbox %s: %w", name, err)
		}
	}

	return nil
}

func (m *Mailboxes) List(ctx context.Context, userID uuid.UUID) ([]Mailbox, error) {
	ctx, span := m.tracer.Start(ctx, "mailboxes.list")
	defer span.End()

	rows, err := m.db.QueryContext(
		ctx,
		`
		SELECT
			id,
			user_id,
			name,
			uid_validity,
			created_at
		FROM mailboxes
		WHERE user_id = $1
		ORDER BY id
		`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list mailboxes: %w", err)
	}
	defer rows.Close()

	mailboxes := make([]Mailbox, 0)

	for rows.Next() {
		var mailbox Mailbox

		err := rows.Scan(
			&mailbox.ID,
			&mailbox.UserID,
			&mailbox.Name,
			&mailbox.UIDValidity,
			&mailbox.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan mailbox: %w", err)
		}

		mailboxes = append(mailboxes, mailbox)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mailboxes: %w", err)
	}

	return mailboxes, nil
}

func (m *Mailboxes) GetByName(ctx context.Context, userID uuid.UUID, name string) (*Mailbox, error) {
	ctx, span := m.tracer.Start(ctx, "mailboxes.get_by_name")
	defer span.End()

	mailbox := &Mailbox{}

	err := m.db.QueryRowContext(
		ctx,
		`
		SELECT
			id,
			user_id,
			name,
			uid_validity,
			created_at
		FROM mailboxes
		WHERE user_id = $1
		AND name = $2
		`,
		userID,
		name,
	).Scan(
		&mailbox.ID,
		&mailbox.UserID,
		&mailbox.Name,
		&mailbox.UIDValidity,
		&mailbox.CreatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("mailbox not found")
	}

	if err != nil {
		return nil, fmt.Errorf("get mailbox: %w", err)
	}

	return mailbox, nil
}

func (m *Mailboxes) Create(ctx context.Context, userID uuid.UUID, name string) (*Mailbox, error) {
	ctx, span := m.tracer.Start(ctx, "mailboxes.create")
	defer span.End()

	mb := &Mailbox{}
	err := m.db.QueryRowContext(
		ctx,
		`
        INSERT INTO mailboxes (user_id, name)
        VALUES ($1, $2)
        RETURNING id, user_id, name, uid_validity, created_at
        `,
		userID,
		name,
	).Scan(
		&mb.ID,
		&mb.UserID,
		&mb.Name,
		&mb.UIDValidity,
		&mb.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create mailbox: %w", err)
	}
	return mb, nil
}

func (m *Mailboxes) EnsureDefaults(ctx context.Context, userID uuid.UUID) error {
	ctx, span := m.tracer.Start(ctx, "mailboxes.ensure_defaults")
	defer span.End()

	existing, err := m.List(ctx, userID)
	if err != nil {
		return err
	}
	have := make(map[string]struct{}, len(existing))
	for _, mb := range existing {
		have[strings.ToLower(mb.Name)] = struct{}{}
	}
	for _, name := range defaultMailboxes {
		if _, ok := have[strings.ToLower(name)]; ok {
			continue
		}
		if _, err := m.Create(ctx, userID, name); err != nil {
			return err
		}
	}
	return nil
}
