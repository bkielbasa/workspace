package mail

import (
	"context"
	"log/slog"

	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type MessageRepository interface {
	Append(ctx context.Context, message *Message) error
	Get(ctx context.Context, id uuid.UUID) (*Message, error)
	List(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]Message, error)
	UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error
	Delete(ctx context.Context, id uuid.UUID) error
	Move(ctx context.Context, id, mailboxID uuid.UUID) error
}

type Mail struct {
	repo   MessageRepository
	tracer trace.Tracer
}

func NewMail(repo MessageRepository) *Mail {
	return &Mail{repo: repo, tracer: otel.Tracer("mail")}
}

func (m *Mail) Append(ctx context.Context, message *Message) error {
	ctx, span := m.tracer.Start(ctx, "mail.append")
	defer span.End()
	return m.repo.Append(ctx, message)
}

func (m *Mail) Get(ctx context.Context, id uuid.UUID) (*Message, error) {
	ctx, span := m.tracer.Start(ctx, "mail.get")
	defer span.End()
	return m.repo.Get(ctx, id)
}

func (m *Mail) List(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]Message, error) {
	ctx, span := m.tracer.Start(ctx, "mail.list")
	defer span.End()
	obs.Log(ctx, slog.LevelInfo, "mail.List called",
		"mailbox_id", mailboxID.String(),
		"limit", limit,
		"offset", offset,
	)
	return m.repo.List(ctx, mailboxID, limit, offset)
}

func (m *Mail) UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error {
	ctx, span := m.tracer.Start(ctx, "mail.update_flags")
	defer span.End()
	return m.repo.UpdateFlags(ctx, id, seen, flagged, answered, deleted, draft)
}

func (m *Mail) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := m.tracer.Start(ctx, "mail.delete")
	defer span.End()
	return m.repo.Delete(ctx, id)
}

func (m *Mail) Move(ctx context.Context, id, mailboxID uuid.UUID) error {
	ctx, span := m.tracer.Start(ctx, "mail.move")
	defer span.End()
	return m.repo.Move(ctx, id, mailboxID)
}

func (m *Mail) Copy(ctx context.Context, id, mailboxID uuid.UUID) (*Message, error) {
	ctx, span := m.tracer.Start(ctx, "mail.copy")
	defer span.End()
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
