package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

type createAliasRequest struct {
	Address     string `json:"address"`
	Destination string `json:"destination"`
}

func (a *Aliases) CreateHandler(w http.ResponseWriter, r *http.Request) {
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

	alias, err := a.Create(r.Context(), domainID, req.Address, req.Destination)
	if err != nil {
		if errors.Is(err, ErrAliasAlreadyExists) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, ErrDomainNotFound) {
			writeJSONError(w, http.StatusNotFound, "domain not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, alias)
}

func (a *Aliases) ListHandler(w http.ResponseWriter, r *http.Request) {
	domainID, err := uuid.Parse(r.PathValue("domainID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid domain id")
		return
	}

	aliases, err := a.ListByDomain(r.Context(), domainID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, aliases)
}

func (a *Aliases) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid alias id")
		return
	}

	if err := a.Delete(r.Context(), id); err != nil {
		if errors.Is(err, ErrAliasNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
