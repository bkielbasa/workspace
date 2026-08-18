package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type createDomainRequest struct {
	Name string `json:"name"`
}

func (d *Domains) CreateHandler(w http.ResponseWriter, r *http.Request) {
	var req createDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	domain, err := d.Create(r.Context(), req.Name)
	if err != nil {
		if errors.Is(err, ErrDomainAlreadyExists) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, domain)
}

func (d *Domains) ListHandler(w http.ResponseWriter, r *http.Request) {
	domains, err := d.List(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, domains)
}

func (d *Domains) GetHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid domain id")
		return
	}

	domain, err := d.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrDomainNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, domain)
}

func (d *Domains) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid domain id")
		return
	}

	if err := d.Delete(r.Context(), id); err != nil {
		if errors.Is(err, ErrDomainNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type createDomainUserRequest struct {
	LocalPart   string `json:"local_part"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

// CreateUserHandler returns an http.HandlerFunc that creates a user scoped to a domain.
// The user's email is constructed as <local_part>@<domain>.
func (d *Domains) CreateUserHandler(users *Users) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domainID, err := uuid.Parse(r.PathValue("domainID"))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid domain id")
			return
		}

		domain, err := d.Get(r.Context(), domainID)
		if err != nil {
			if errors.Is(err, ErrDomainNotFound) {
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

		user, err := users.Create(r.Context(), email, req.Password, req.DisplayName)
		if err != nil {
			if errors.Is(err, ErrUserAlreadyExists) {
				writeJSONError(w, http.StatusConflict, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, user)
	}
}
