package identity

import (
	"context"

	"github.com/google/uuid"
)

// PhotoLabelRepository persists per-photo labels keyed by owner and the
// library-relative path (month/name.ext). Empty label deletes the row.
type PhotoLabelRepository interface {
	Get(ctx context.Context, userID uuid.UUID, path string) (string, error)
	Set(ctx context.Context, userID uuid.UUID, path, label string) error
}
