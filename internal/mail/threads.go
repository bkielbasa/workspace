package mail

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type ThreadRepository interface {
	List(ctx context.Context, userID uuid.UUID) ([]Thread, error)
	FindByMessageID(ctx context.Context, messageID string) (uuid.UUID, error)
	FindBySubject(ctx context.Context, subject string) (uuid.UUID, error)
	SetThreadID(ctx context.Context, messageID, threadID uuid.UUID) error
}

type Threads struct {
	repo   ThreadRepository
	tracer trace.Tracer
}

func NewThreads(repo ThreadRepository) *Threads {
	return &Threads{repo: repo, tracer: otel.Tracer("threads")}
}

func (t *Threads) List(ctx context.Context, userID uuid.UUID) ([]Thread, error) {
	ctx, span := t.tracer.Start(ctx, "threads.list")
	defer span.End()
	return t.repo.List(ctx, userID)
}

func (t *Threads) Assign(ctx context.Context, msg *Message) error {
	ctx, span := t.tracer.Start(ctx, "threads.assign")
	defer span.End()

	var threadID uuid.UUID
	if msg.InReplyTo != "" {
		threadID, _ = t.repo.FindByMessageID(ctx, msg.InReplyTo)
	}
	if threadID == uuid.Nil && msg.References != "" {
		for _, r := range strings.Split(msg.References, " ") {
			if r == "" {
				continue
			}
			id, err := t.repo.FindByMessageID(ctx, r)
			if err == nil && id != uuid.Nil {
				threadID = id
				break
			}
		}
	}
	if threadID == uuid.Nil {
		threadID, _ = t.repo.FindBySubject(ctx, normalizeSubject(msg.Subject))
	}
	if threadID == uuid.Nil {
		threadID = uuid.New()
	}
	if err := t.repo.SetThreadID(ctx, msg.ID, threadID); err != nil {
		return fmt.Errorf("assign thread: %w", err)
	}
	return nil
}

func normalizeSubject(s string) string {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for strings.HasPrefix(lower, "re:") || strings.HasPrefix(lower, "fwd:") {
		if strings.HasPrefix(lower, "re:") {
			s = strings.TrimSpace(s[3:])
		} else if strings.HasPrefix(lower, "fwd:") {
			s = strings.TrimSpace(s[4:])
		}
		lower = strings.ToLower(s)
	}
	return s
}
