package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

func (s *Server) currentUser(r *http.Request) *identity.User {
	return UserFromContext(r.Context())
}

func (s *Server) csrfToken(r *http.Request) string {
	return csrfTokenFromRequest(r)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	if s.views == nil || s.views.notesT == nil {
		http.Error(w, "views not initialized", http.StatusInternalServerError)
		return
	}
	tplName := name
	if tplName == "notes.html" || tplName == "layout" {
		tplName = "layout"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.views.notesT.ExecuteTemplate(w, tplName, data); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func parseNoteID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(r.PathValue("id"))
}

func parseItemID(r *http.Request) (uuid.UUID, error) {
	val := r.PathValue("item_id")
	if val == "" {
		val = r.PathValue("itemID")
	}
	return uuid.Parse(val)
}

func (s *Server) notesPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	if user == nil {
		user = s.currentUser(r)
	}
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if s.notes == nil {
		http.Error(w, "notes service unavailable", http.StatusServiceUnavailable)
		return
	}

	notesList, err := s.notes.ListNotes(r.Context(), user.ID, false, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"Title":     "Notes",
		"Section":   "notes",
		"User":      user,
		"CSRFToken": s.csrfToken(r),
		"Notes":     notesList,
	}

	if r.Header.Get("HX-Request") == "true" {
		s.render(w, "notesList", data)
		return
	}
	s.render(w, "notes.html", data)
}

func (s *Server) notesLiveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	if s.notes == nil {
		http.Error(w, "notes service unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.notes.Broker().Subscribe()
	defer s.notes.Broker().Unsubscribe(ch)

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: note_update\ndata: {\"note_id\":\"%s\",\"type\":\"%s\"}\n\n", ev.NoteID, ev.Type)
			flusher.Flush()
		}
	}
}

func (s *Server) notesCreate(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.notes == nil {
		http.Error(w, "notes service unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	body := strings.TrimSpace(r.FormValue("body"))
	kind := strings.TrimSpace(r.FormValue("kind"))
	if kind != string(notes.KindList) {
		kind = string(notes.KindNote)
	}
	color := strings.TrimSpace(r.FormValue("color"))
	if color == "" {
		color = "default"
	}
	isShared := r.FormValue("is_family_shared") == "true" || r.FormValue("is_family_shared") == "on"

	n := notes.Note{
		Title:          title,
		Body:           body,
		Kind:           notes.Kind(kind),
		Color:          color,
		IsFamilyShared: isShared,
	}

	created, err := s.notes.CreateNote(r.Context(), user.ID, n)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if created.Kind == notes.KindList && body != "" {
		lines := strings.Split(body, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				_, _ = s.notes.AddItem(r.Context(), user.ID, created.ID, line)
			}
		}
	}

	if r.Header.Get("HX-Request") == "true" {
		notesList, err := s.notes.ListNotes(r.Context(), user.ID, false, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data := map[string]any{
			"CSRFToken": s.csrfToken(r),
			"Notes":     notesList,
		}
		s.render(w, "notesList", data)
		return
	}

	http.Redirect(w, r, "/notes", http.StatusSeeOther)
}

func (s *Server) notesDetail(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.notes == nil {
		http.Error(w, "notes service unavailable", http.StatusServiceUnavailable)
		return
	}

	id, err := parseNoteID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	note, err := s.notes.GetNote(r.Context(), user.ID, id)
	if err != nil {
		if errors.Is(err, notes.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"Note":      note,
		"CSRFToken": s.csrfToken(r),
	}
	s.render(w, "noteCard", data)
}

func (s *Server) notesUpdate(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.notes == nil {
		http.Error(w, "notes service unavailable", http.StatusServiceUnavailable)
		return
	}

	id, err := parseNoteID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	existing, err := s.notes.GetNote(r.Context(), user.ID, id)
	if err != nil {
		if errors.Is(err, notes.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	if r.Form.Has("title") {
		existing.Title = strings.TrimSpace(r.FormValue("title"))
	}
	if r.Form.Has("body") {
		existing.Body = strings.TrimSpace(r.FormValue("body"))
	}
	if r.Form.Has("color") {
		existing.Color = strings.TrimSpace(r.FormValue("color"))
	}
	if r.Form.Has("is_pinned") {
		existing.IsPinned = r.FormValue("is_pinned") == "true" || r.FormValue("is_pinned") == "on"
	}
	if r.Form.Has("is_archived") {
		existing.IsArchived = r.FormValue("is_archived") == "true" || r.FormValue("is_archived") == "on"
	}
	if r.Form.Has("is_family_shared") {
		existing.IsFamilyShared = r.FormValue("is_family_shared") == "true" || r.FormValue("is_family_shared") == "on"
	}

	updated, err := s.notes.UpdateNote(r.Context(), user.ID, user.IsAdmin, *existing)
	if err != nil {
		if errors.Is(err, notes.ErrForbidden) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if errors.Is(err, notes.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		data := map[string]any{
			"Note":      updated,
			"CSRFToken": s.csrfToken(r),
		}
		s.render(w, "noteCard", data)
		return
	}

	http.Redirect(w, r, "/notes", http.StatusSeeOther)
}

func (s *Server) notesDelete(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.notes == nil {
		http.Error(w, "notes service unavailable", http.StatusServiceUnavailable)
		return
	}

	id, err := parseNoteID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = s.notes.DeleteNote(r.Context(), user.ID, user.IsAdmin, id)
	if err != nil {
		if errors.Is(err, notes.ErrForbidden) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if errors.Is(err, notes.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		notesList, err := s.notes.ListNotes(r.Context(), user.ID, false, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data := map[string]any{
			"CSRFToken": s.csrfToken(r),
			"Notes":     notesList,
		}
		s.render(w, "notesList", data)
		return
	}

	http.Redirect(w, r, "/notes", http.StatusSeeOther)
}

func (s *Server) notesShare(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.notes == nil {
		http.Error(w, "notes service unavailable", http.StatusServiceUnavailable)
		return
	}

	id, err := parseNoteID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	note, err := s.notes.GetNote(r.Context(), user.ID, id)
	if err != nil {
		if errors.Is(err, notes.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = r.ParseForm()
	if r.Form.Has("is_family_shared") {
		note.IsFamilyShared = r.FormValue("is_family_shared") == "true" || r.FormValue("is_family_shared") == "on"
	} else {
		note.IsFamilyShared = !note.IsFamilyShared
	}

	updated, err := s.notes.UpdateNote(r.Context(), user.ID, user.IsAdmin, *note)
	if err != nil {
		if errors.Is(err, notes.ErrForbidden) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		data := map[string]any{
			"Note":      updated,
			"CSRFToken": s.csrfToken(r),
		}
		s.render(w, "noteCard", data)
		return
	}

	http.Redirect(w, r, "/notes", http.StatusSeeOther)
}

func (s *Server) notesTogglePin(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.notes == nil {
		http.Error(w, "notes service unavailable", http.StatusServiceUnavailable)
		return
	}

	id, err := parseNoteID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	note, err := s.notes.GetNote(r.Context(), user.ID, id)
	if err != nil {
		if errors.Is(err, notes.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	note.IsPinned = !note.IsPinned
	_, err = s.notes.UpdateNote(r.Context(), user.ID, user.IsAdmin, *note)
	if err != nil {
		if errors.Is(err, notes.ErrForbidden) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		notesList, err := s.notes.ListNotes(r.Context(), user.ID, false, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data := map[string]any{
			"CSRFToken": s.csrfToken(r),
			"Notes":     notesList,
		}
		s.render(w, "notesList", data)
		return
	}

	http.Redirect(w, r, "/notes", http.StatusSeeOther)
}

func (s *Server) notesAddItem(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.notes == nil {
		http.Error(w, "notes service unavailable", http.StatusServiceUnavailable)
		return
	}

	noteID, err := parseNoteID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	content := strings.TrimSpace(r.FormValue("content"))
	if content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}

	_, err = s.notes.AddItem(r.Context(), user.ID, noteID, content)
	if err != nil {
		if errors.Is(err, notes.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		note, err := s.notes.GetNote(r.Context(), user.ID, noteID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data := map[string]any{
			"Note":      note,
			"CSRFToken": s.csrfToken(r),
		}
		s.render(w, "noteCard", data)
		return
	}

	http.Redirect(w, r, "/notes", http.StatusSeeOther)
}

func (s *Server) notesToggleItem(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.notes == nil {
		http.Error(w, "notes service unavailable", http.StatusServiceUnavailable)
		return
	}

	noteID, err := parseNoteID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	itemID, err := parseItemID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	_ = r.ParseForm()
	var completed bool
	if r.Form.Has("completed") {
		val := r.FormValue("completed")
		completed = val == "on" || val == "true" || val == "1"
	} else if len(r.Form) > 0 {
		completed = false
	} else {
		// Toggle current state if no form fields provided
		note, err := s.notes.GetNote(r.Context(), user.ID, noteID)
		if err == nil && note != nil {
			for _, it := range note.Items {
				if it.ID == itemID {
					completed = !it.Completed
					break
				}
			}
		}
	}

	_, err = s.notes.ToggleItem(r.Context(), user.ID, noteID, itemID, completed)
	if err != nil {
		if errors.Is(err, notes.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		note, err := s.notes.GetNote(r.Context(), user.ID, noteID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data := map[string]any{
			"Note":      note,
			"CSRFToken": s.csrfToken(r),
		}
		s.render(w, "noteCard", data)
		return
	}

	http.Redirect(w, r, "/notes", http.StatusSeeOther)
}

func (s *Server) notesDeleteItem(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.notes == nil {
		http.Error(w, "notes service unavailable", http.StatusServiceUnavailable)
		return
	}

	noteID, err := parseNoteID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	itemID, err := parseItemID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = s.notes.DeleteItem(r.Context(), user.ID, noteID, itemID)
	if err != nil {
		if errors.Is(err, notes.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		note, err := s.notes.GetNote(r.Context(), user.ID, noteID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data := map[string]any{
			"Note":      note,
			"CSRFToken": s.csrfToken(r),
		}
		s.render(w, "noteCard", data)
		return
	}

	http.Redirect(w, r, "/notes", http.StatusSeeOther)
}
