package mail

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type MailboxRepository interface {
	CreateDefault(ctx context.Context, userID uuid.UUID) error
	List(ctx context.Context, userID uuid.UUID) ([]Mailbox, error)
	GetByName(ctx context.Context, userID uuid.UUID, name string) (*Mailbox, error)
	Create(ctx context.Context, userID uuid.UUID, name string) (*Mailbox, error)
}

var defaultMailboxes = []string{
	"INBOX", "Sent", "Drafts", "Trash", "Archive", "Spam", "All", "Important",
}

type Mailboxes struct {
	repo   MailboxRepository
	tracer trace.Tracer
}

func NewMailboxes(repo MailboxRepository) *Mailboxes {
	return &Mailboxes{repo: repo, tracer: otel.Tracer("mailboxes")}
}

func (m *Mailboxes) CreateDefault(ctx context.Context, userID uuid.UUID) error {
	ctx, span := m.tracer.Start(ctx, "mailboxes.create_default")
	defer span.End()
	return m.repo.CreateDefault(ctx, userID)
}

func (m *Mailboxes) List(ctx context.Context, userID uuid.UUID) ([]Mailbox, error) {
	ctx, span := m.tracer.Start(ctx, "mailboxes.list")
	defer span.End()
	return m.repo.List(ctx, userID)
}

func (m *Mailboxes) GetByName(ctx context.Context, userID uuid.UUID, name string) (*Mailbox, error) {
	ctx, span := m.tracer.Start(ctx, "mailboxes.get_by_name")
	defer span.End()
	return m.repo.GetByName(ctx, userID, name)
}

func (m *Mailboxes) Create(ctx context.Context, userID uuid.UUID, name string) (*Mailbox, error) {
	ctx, span := m.tracer.Start(ctx, "mailboxes.create")
	defer span.End()
	return m.repo.Create(ctx, userID, name)
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
