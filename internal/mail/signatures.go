package mail

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Signature struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Name      string
	Content   string
	IsDefault bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SignatureRepository interface {
	Create(ctx context.Context, sig *Signature) error
	GetByID(ctx context.Context, userID, id uuid.UUID) (*Signature, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]Signature, error)
	GetDefault(ctx context.Context, userID uuid.UUID) (*Signature, error)
	Update(ctx context.Context, sig *Signature) error
	SetDefault(ctx context.Context, userID uuid.UUID, id uuid.UUID) error
	Delete(ctx context.Context, userID uuid.UUID, id uuid.UUID) error
}
