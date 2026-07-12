package main

import (
    "encoding/json"
    "net/http"

    "github.com/google/uuid"
)

type ContactsHTTP struct {
    contacts *Contacts
    users    *Users
}

func (h *ContactsHTTP) ListHandler(w http.ResponseWriter, r *http.Request) {
    uidStr := r.URL.Query().Get("user_id")
    uid, err := uuid.Parse(uidStr)
    if err != nil {
        http.Error(w, "invalid user_id", 400)
        return
    }

    res, err := h.contacts.List(r.Context(), uid)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    json.NewEncoder(w).Encode(res)
}

func (h *ContactsHTTP) UpsertHandler(w http.ResponseWriter, r *http.Request) {
    var in struct {
        UserID string `json:"user_id"`
        Email  string `json:"email"`
        Name   string `json:"name"`
    }

    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        http.Error(w, "bad request", 400)
        return
    }

    uid, err := uuid.Parse(in.UserID)
    if err != nil {
        http.Error(w, "invalid user_id", 400)
        return
    }

    ct, err := h.contacts.Upsert(r.Context(), uid, in.Email, in.Name)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    json.NewEncoder(w).Encode(ct)
}

func (h *ContactsHTTP) DeleteHandler(w http.ResponseWriter, r *http.Request) {
    var in struct {
        UserID string `json:"user_id"`
        Email  string `json:"email"`
    }

    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        http.Error(w, "bad request", 400)
        return
    }

    uid, err := uuid.Parse(in.UserID)
    if err != nil {
        http.Error(w, "invalid user_id", 400)
        return
    }

    if err := h.contacts.Delete(r.Context(), uid, in.Email); err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    w.WriteHeader(204)
}
