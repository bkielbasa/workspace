package imap

import (
	"context"
	"time"

	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

// An IMAP session context lives as long as the connection, so a store call
// inheriting it directly can block forever. A hung query then leaves the
// client waiting on a reply that never arrives, which is what Apple Mail
// reports as "the server doesn't respond". These wrappers give every call
// its own deadline, so the failure surfaces as an error the handler can
// answer instead of silence.

type boundedMessages struct {
	inner   MessageStore
	timeout time.Duration
}

func newBoundedMessages(inner MessageStore, timeout time.Duration) MessageStore {
	if inner == nil {
		return nil
	}
	return boundedMessages{inner: inner, timeout: timeout}
}

func (b boundedMessages) Append(ctx context.Context, message *mail.Message) error {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.Append(ctx, message)
}

func (b boundedMessages) Get(ctx context.Context, id uuid.UUID) (*mail.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.Get(ctx, id)
}

func (b boundedMessages) List(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.List(ctx, mailboxID, limit, offset)
}

func (b boundedMessages) ListSummary(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.ListSummary(ctx, mailboxID, limit, offset)
}

func (b boundedMessages) UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.UpdateFlags(ctx, id, seen, flagged, answered, deleted, draft)
}

func (b boundedMessages) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.Delete(ctx, id)
}

func (b boundedMessages) Move(ctx context.Context, id, mailboxID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.Move(ctx, id, mailboxID)
}

func (b boundedMessages) Copy(ctx context.Context, id, mailboxID uuid.UUID) (*mail.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.Copy(ctx, id, mailboxID)
}

type boundedMailboxes struct {
	inner   MailboxStore
	timeout time.Duration
}

func newBoundedMailboxes(inner MailboxStore, timeout time.Duration) MailboxStore {
	if inner == nil {
		return nil
	}
	return boundedMailboxes{inner: inner, timeout: timeout}
}

func (b boundedMailboxes) List(ctx context.Context, userID uuid.UUID) ([]mail.Mailbox, error) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.List(ctx, userID)
}

func (b boundedMailboxes) GetByName(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.GetByName(ctx, userID, name)
}

func (b boundedMailboxes) Create(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.Create(ctx, userID, name)
}

func (b boundedMailboxes) EnsureDefaults(ctx context.Context, userID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.EnsureDefaults(ctx, userID)
}
