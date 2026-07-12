package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var defaultMailboxes = []string{
	"INBOX",
	"Sent",
	"Drafts",
	"Trash",
	"Archive",
}

type Mailbox struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Name        string
	UIDValidity uint64
	CreatedAt   time.Time
}

type Mailboxes struct {
	db *sql.DB
}

func (m *Mailboxes) CreateDefault(ctx context.Context, userID uuid.UUID) error {
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
