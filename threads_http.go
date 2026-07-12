package main

import (
    "encoding/json"
    "net/http"

    "github.com/google/uuid"
)

type ThreadsHTTP struct {
    threads *Threads
}

func (h *ThreadsHTTP) ListHandler(w http.ResponseWriter, r *http.Request) {
    uidStr := r.URL.Query().Get("user_id")
    uid, err := uuid.Parse(uidStr)
    if err != nil {
        http.Error(w, "invalid user_id", 400)
        return
    }

    res, err := h.threads.List(r.Context(), uid)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    json.NewEncoder(w).Encode(res)
}
