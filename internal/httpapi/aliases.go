package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type aliasService interface {
	Create(ctx context.Context, domainID uuid.UUID, address, destination string) (*identity.Alias, error)
	ListByDomain(ctx context.Context, domainID uuid.UUID) ([]identity.Alias, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type AliasHandlers struct {
	aliases aliasService
}

// NewAliasHandlers returns the JSON handlers for the domain-scoped alias routes.
func NewAliasHandlers(aliases aliasService) *AliasHandlers {
	return &AliasHandlers{aliases: aliases}
}

type createAliasRequest struct {
	Address     string `json:"address"`
	Destination string `json:"destination"`
}

func (h *AliasHandlers) CreateHandler(w http.ResponseWriter, r *http.Request) {
	domainID, err := uuid.Parse(r.PathValue("domainID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid domain id")
		return
	}

	var req createAliasRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	alias, err := h.aliases.Create(r.Context(), domainID, req.Address, req.Destination)
	if err != nil {
		if errors.Is(err, identity.ErrAliasAlreadyExists) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, identity.ErrDomainNotFound) {
			writeJSONError(w, http.StatusNotFound, "domain not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, alias)
}

func (h *AliasHandlers) ListHandler(w http.ResponseWriter, r *http.Request) {
	domainID, err := uuid.Parse(r.PathValue("domainID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid domain id")
		return
	}

	aliases, err := h.aliases.ListByDomain(r.Context(), domainID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, aliases)
}

func (h *AliasHandlers) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid alias id")
		return
	}

	if err := h.aliases.Delete(r.Context(), id); err != nil {
		if errors.Is(err, identity.ErrAliasNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
