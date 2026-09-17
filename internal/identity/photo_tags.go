package identity

import (
	"context"

	"github.com/google/uuid"
)

// PhotoTagRepository persists reusable tags keyed by owner and the
// library-relative path (month/name.ext). The same tag attaches to many
// photos; Set replaces a photo's whole tag set.
type PhotoTagRepository interface {
	Set(ctx context.Context, userID uuid.UUID, path string, tags []string) error
	// ByPhoto returns every photo's tags for gallery rendering in one query.
	ByPhoto(ctx context.Context, userID uuid.UUID) (map[string][]string, error)
	// All returns every tag the user ever used, for suggestions.
	All(ctx context.Context, userID uuid.UUID) ([]string, error)
}
