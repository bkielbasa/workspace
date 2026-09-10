package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type userService interface {
	Create(ctx context.Context, email, password, displayName string) (*identity.User, error)
	Get(ctx context.Context, id uuid.UUID) (*identity.User, error)
	List(ctx context.Context) ([]identity.User, error)
	Update(ctx context.Context, id uuid.UUID, displayName string, enabled bool) error
	Delete(ctx context.Context, id uuid.UUID) error
	ChangePassword(ctx context.Context, id uuid.UUID, password string) error
}

type UserHandlers struct {
	users userService
}

// NewUserHandlers returns the JSON handlers for the /users routes.
func NewUserHandlers(users userService) *UserHandlers {
	return &UserHandlers{users: users}
}

type createUserRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type updateUserRequest struct {
	DisplayName string `json:"display_name"`
	Enabled     bool   `json:"enabled"`
}

type changePasswordRequest struct {
	Password string `json:"password"`
}

// userResponse is the public representation of a user. It deliberately omits
// the password hash so credentials are never leaked over the wire.
type userResponse struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toUserResponse(u *identity.User) userResponse {
	return userResponse{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Enabled:     u.Enabled,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
}

func listUserResponses(users []identity.User) []userResponse {
	res := make([]userResponse, 0, len(users))
	for i := range users {
		res = append(res, toUserResponse(&users[i]))
	}
	return res
}

func (h *UserHandlers) CreateHandler(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := h.users.Create(r.Context(), req.Email, req.Password, req.DisplayName)
	if err != nil {
		if errors.Is(err, identity.ErrUserAlreadyExists) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, identity.ErrDomainNotAllowed) {
			writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, toUserResponse(user))
}

func (h *UserHandlers) ListHandler(w http.ResponseWriter, r *http.Request) {
	users, err := h.users.List(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, listUserResponses(users))
}

func (h *UserHandlers) GetHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	user, err := h.users.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, identity.ErrUserNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}

		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toUserResponse(user))
}

func (h *UserHandlers) UpdateHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var req updateUserRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err = h.users.Update(r.Context(), id, req.DisplayName, req.Enabled)
	if err != nil {
		if errors.Is(err, identity.ErrUserNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}

		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *UserHandlers) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	err = h.users.Delete(r.Context(), id)
	if err != nil {
		if errors.Is(err, identity.ErrUserNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}

		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *UserHandlers) ChangePasswordHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var req changePasswordRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err = h.users.ChangePassword(r.Context(), id, req.Password)
	if err != nil {
		if errors.Is(err, identity.ErrUserNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}

		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
