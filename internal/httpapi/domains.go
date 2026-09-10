package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type domainService interface {
	Create(ctx context.Context, name string) (*identity.Domain, error)
	Get(ctx context.Context, id uuid.UUID) (*identity.Domain, error)
	List(ctx context.Context) ([]identity.Domain, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// userCreator is the slice of the user service the domain-scoped user creation
// route needs.
type userCreator interface {
	Create(ctx context.Context, email, password, displayName string) (*identity.User, error)
}

type DomainHandlers struct {
	domains domainService
	users   userCreator
}

// NewDomainHandlers returns the JSON handlers for the /domains routes,
// including the domain-scoped user creation route.
func NewDomainHandlers(domains domainService, users userCreator) *DomainHandlers {
	return &DomainHandlers{domains: domains, users: users}
}

type createDomainRequest struct {
	Name string `json:"name"`
}

type createDomainUserRequest struct {
	LocalPart   string `json:"local_part"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

func (h *DomainHandlers) CreateHandler(w http.ResponseWriter, r *http.Request) {
	var req createDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	domain, err := h.domains.Create(r.Context(), req.Name)
	if err != nil {
		if errors.Is(err, identity.ErrDomainAlreadyExists) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, domain)
}

func (h *DomainHandlers) ListHandler(w http.ResponseWriter, r *http.Request) {
	domains, err := h.domains.List(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, domains)
}

func (h *DomainHandlers) GetHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid domain id")
		return
	}

	domain, err := h.domains.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, identity.ErrDomainNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, domain)
}

func (h *DomainHandlers) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid domain id")
		return
	}

	if err := h.domains.Delete(r.Context(), id); err != nil {
		if errors.Is(err, identity.ErrDomainNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// CreateUserHandler creates a user scoped to a domain. The user's email is
// constructed as <local_part>@<domain>.
func (h *DomainHandlers) CreateUserHandler(w http.ResponseWriter, r *http.Request) {
	domainID, err := uuid.Parse(r.PathValue("domainID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid domain id")
		return
	}

	domain, err := h.domains.Get(r.Context(), domainID)
	if err != nil {
		if errors.Is(err, identity.ErrDomainNotFound) {
			writeJSONError(w, http.StatusNotFound, "domain not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req createDomainUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	localPart := strings.ToLower(strings.TrimSpace(req.LocalPart))
	if localPart == "" {
		writeJSONError(w, http.StatusBadRequest, "local_part is required")
		return
	}

	email := fmt.Sprintf("%s@%s", localPart, domain.Name)

	user, err := h.users.Create(r.Context(), email, req.Password, req.DisplayName)
	if err != nil {
		if errors.Is(err, identity.ErrUserAlreadyExists) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, toUserResponse(user))
}
