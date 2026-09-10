package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

type threadService interface {
	List(ctx context.Context, userID uuid.UUID) ([]mail.Thread, error)
}

type ThreadHandlers struct {
	threads threadService
}

// NewThreadHandlers returns the JSON handlers for the /threads routes.
func NewThreadHandlers(threads threadService) *ThreadHandlers {
	return &ThreadHandlers{threads: threads}
}

func (h *ThreadHandlers) ListHandler(w http.ResponseWriter, r *http.Request) {
	uid, err := uuid.Parse(r.URL.Query().Get("user_id"))
	if err != nil {
		http.Error(w, "invalid user_id", http.StatusBadRequest)
		return
	}

	res, err := h.threads.List(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(res)
}
