package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

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

func (u *Users) CreateHandler(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := u.Create(
		r.Context(),
		req.Email,
		req.Password,
		req.DisplayName,
	)
	if err != nil {
		if errors.Is(err, ErrUserAlreadyExists) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, ErrDomainNotAllowed) {
			writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(
		w,
		http.StatusCreated,
		toUserResponse(user),
	)
}

func (u *Users) ListHandler(w http.ResponseWriter, r *http.Request) {
	users, err := u.List(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(
		w,
		http.StatusOK,
		listUserResponses(users),
	)
}

func (u *Users) GetHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	user, err := u.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}

		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(
		w,
		http.StatusOK,
		toUserResponse(user),
	)
}

func (u *Users) UpdateHandler(w http.ResponseWriter, r *http.Request) {
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

	err = u.Update(
		r.Context(),
		id,
		req.DisplayName,
		req.Enabled,
	)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}

		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (u *Users) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	err = u.Delete(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}

		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (u *Users) ChangePasswordHandler(w http.ResponseWriter, r *http.Request) {
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

	err = u.ChangePassword(
		r.Context(),
		id,
		req.Password,
	)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}

		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func listUserResponses(users []User) []userResponse {
	res := make([]userResponse, 0, len(users))
	for i := range users {
		res = append(res, toUserResponse(&users[i]))
	}
	return res
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(
		w,
		status,
		map[string]string{
			"error": message,
		},
	)
}
