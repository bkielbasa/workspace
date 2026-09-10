package web

import (
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
)

var templateFuncs = template.FuncMap{
	"fmtDate": func(t time.Time) string { return t.Local().Format("Jan 2, 2006") },
	"fmtTime": func(t time.Time) string { return t.Local().Format("15:04") },
}

type viewData struct {
	Title     string
	Section   string
	User      *identity.User
	CSRFToken string
	Contacts  []contacts.Contact
	Events    []calendar.Event
	Error     string
}

type views struct {
	contacts contactsService
	calendar calendarService

	home      *template.Template
	contactsT *template.Template
	calendarT *template.Template
	login     *template.Template
}

func newViews(files fs.FS, contactService contactsService, calendarService calendarService) (*views, error) {
	base := []string{
		"web/templates/layout.html",
		"web/templates/nav.html",
		"web/templates/contacts-rows.html",
		"web/templates/calendars-rows.html",
	}
	page := func(extra string) (*template.Template, error) {
		names := append(append([]string{}, base...), extra)
		t, err := template.New("").Funcs(templateFuncs).ParseFS(files, names...)
		if err != nil {
			return nil, fmt.Errorf("web: parse %s: %w", extra, err)
		}
		return t, nil
	}

	home, err := page("web/templates/home.html")
	if err != nil {
		return nil, err
	}
	contactsT, err := page("web/templates/contacts.html")
	if err != nil {
		return nil, err
	}
	calendarT, err := page("web/templates/calendars.html")
	if err != nil {
		return nil, err
	}
	login, err := template.New("").Funcs(templateFuncs).ParseFS(files, "web/templates/login.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse login template: %w", err)
	}

	return &views{
		contacts: contactService, calendar: calendarService,
		home: home, contactsT: contactsT, calendarT: calendarT, login: login,
	}, nil
}

func renderView(w http.ResponseWriter, r *http.Request, t *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		obs.Log(r.Context(), slog.LevelError, "render template failed", "template", name, "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

type pageHandler func(http.ResponseWriter, *http.Request, *identity.User)

func (v *views) homePage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	renderView(w, r, v.home, "layout", viewData{
		Title: "Home", Section: "home", User: user, CSRFToken: csrfTokenFromRequest(r),
	})
}

func (v *views) contactsPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	data := viewData{
		Title: "Contacts", Section: "contacts", User: user, CSRFToken: csrfTokenFromRequest(r),
	}
	list, err := v.contacts.List(r.Context(), user.ID)
	if err != nil {
		data.Error = "Could not load your contacts."
	} else {
		data.Contacts = list
	}
	renderView(w, r, v.contactsT, "layout", data)
}

func (v *views) calendarsPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	data := viewData{
		Title: "Calendars", Section: "calendars", User: user, CSRFToken: csrfTokenFromRequest(r),
	}
	list, err := v.calendar.List(r.Context(), user.ID)
	if err != nil {
		data.Error = "Could not load your events."
	} else {
		data.Events = list
	}
	renderView(w, r, v.calendarT, "layout", data)
}

func (v *views) contactsRows(w http.ResponseWriter, r *http.Request) {
	v.renderContactsRows(w, r, UserFromContext(r.Context()))
}

func (v *views) contactsAdd(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	ct := contacts.Contact{
		FirstName: strings.TrimSpace(r.FormValue("first_name")),
		LastName:  strings.TrimSpace(r.FormValue("last_name")),
		Company:   strings.TrimSpace(r.FormValue("company")),
		Title:     strings.TrimSpace(r.FormValue("title")),
	}
	if email := strings.TrimSpace(r.FormValue("email")); email != "" {
		ct.Emails = []contacts.Field{{Value: strings.ToLower(email), Type: []string{"INTERNET"}}}
	}
	if phone := strings.TrimSpace(r.FormValue("phone")); phone != "" {
		ct.Phones = []contacts.Field{{Value: phone, Type: []string{"CELL", "VOICE"}}}
	}

	data := viewData{}
	if len(ct.Emails) == 0 || !strings.Contains(ct.Emails[0].Value, "@") {
		data.Error = "A valid email address is required."
	} else if _, err := v.contacts.PutStructured(r.Context(), user.ID, nil, ct); err != nil {
		data.Error = "Could not save the contact."
	}
	data.Contacts, _ = v.contacts.List(r.Context(), user.ID)
	renderView(w, r, v.contactsT, "contacts-rows", data)
}

func (v *views) contactsAddField(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		id, err := parseUUIDPath(r)
		if err != nil {
			http.Error(w, "invalid contact id", http.StatusBadRequest)
			return
		}
		ct, err := v.contacts.Get(r.Context(), user.ID, id)
		if err != nil {
			http.Error(w, "contact not found", http.StatusNotFound)
			return
		}

		var value string
		switch kind {
		case "emails":
			value = strings.TrimSpace(r.FormValue("email"))
			if value != "" {
				ct.Emails = append(ct.Emails, contacts.Field{Value: strings.ToLower(value), Type: []string{"INTERNET"}})
			}
		case "phones":
			value = strings.TrimSpace(r.FormValue("phone"))
			if value != "" {
				ct.Phones = append(ct.Phones, contacts.Field{Value: value, Type: []string{"CELL", "VOICE"}})
			}
		default:
			http.Error(w, "unknown field kind", http.StatusBadRequest)
			return
		}
		if value == "" {
			http.Error(w, "a value is required", http.StatusBadRequest)
			return
		}
		if _, err := v.contacts.PutStructured(r.Context(), user.ID, &id, ct); err != nil {
			http.Error(w, "could not update contact", http.StatusInternalServerError)
			return
		}
		v.renderContactsRows(w, r, user)
	}
}

func (v *views) contactsRemoveField(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		id, err := parseUUIDPath(r)
		if err != nil {
			http.Error(w, "invalid contact id", http.StatusBadRequest)
			return
		}
		index, err := strconv.Atoi(r.PathValue("index"))
		if err != nil || index < 0 {
			http.Error(w, "invalid field index", http.StatusBadRequest)
			return
		}
		ct, err := v.contacts.Get(r.Context(), user.ID, id)
		if err != nil {
			http.Error(w, "contact not found", http.StatusNotFound)
			return
		}
		switch kind {
		case "emails":
			if index >= len(ct.Emails) {
				http.Error(w, "invalid field index", http.StatusBadRequest)
				return
			}
			ct.Emails = append(ct.Emails[:index], ct.Emails[index+1:]...)
		case "phones":
			if index >= len(ct.Phones) {
				http.Error(w, "invalid field index", http.StatusBadRequest)
				return
			}
			ct.Phones = append(ct.Phones[:index], ct.Phones[index+1:]...)
		default:
			http.Error(w, "unknown field kind", http.StatusBadRequest)
			return
		}
		if _, err := v.contacts.PutStructured(r.Context(), user.ID, &id, ct); err != nil {
			http.Error(w, "could not update contact", http.StatusInternalServerError)
			return
		}
		v.renderContactsRows(w, r, user)
	}
}

func (v *views) renderContactsRows(w http.ResponseWriter, r *http.Request, user *identity.User) {
	list, err := v.contacts.List(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "could not load contacts", http.StatusInternalServerError)
		return
	}
	renderView(w, r, v.contactsT, "contacts-rows", viewData{Contacts: list})
}

func (v *views) contactsDelete(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	id, err := parseUUIDPath(r)
	if err != nil {
		http.Error(w, "invalid contact id", http.StatusBadRequest)
		return
	}
	if err := v.contacts.DeleteByID(r.Context(), user.ID, id); err != nil {
		http.Error(w, "could not delete contact", http.StatusInternalServerError)
		return
	}
	v.renderContactsRows(w, r, user)
}

func (v *views) calendarsRows(w http.ResponseWriter, r *http.Request) {
	v.renderCalendarRows(w, r, UserFromContext(r.Context()))
}

func (v *views) calendarsAdd(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	title := strings.TrimSpace(r.FormValue("title"))
	start, startErr := time.ParseInLocation("2006-01-02T15:04", strings.TrimSpace(r.FormValue("starts_at")), time.Local)
	end, endErr := time.ParseInLocation("2006-01-02T15:04", strings.TrimSpace(r.FormValue("ends_at")), time.Local)

	data := viewData{}
	switch {
	case title == "":
		data.Error = "A title is required."
	case startErr != nil || endErr != nil:
		data.Error = "Enter valid start and end times."
	case !end.After(start):
		data.Error = "The event must end after it starts."
	default:
		event, err := calendar.NewWebEvent(user.ID, title, start.UTC(), end.UTC())
		if err != nil {
			data.Error = "Could not save the event: " + err.Error()
		} else if _, err := v.calendar.Put(r.Context(), event); err != nil {
			data.Error = "Could not save the event: " + err.Error()
		}
	}
	data.Events, _ = v.calendar.List(r.Context(), user.ID)
	renderView(w, r, v.calendarT, "calendars-rows", data)
}

func (v *views) calendarsDelete(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	id, err := parseUUIDPath(r)
	if err != nil {
		http.Error(w, "invalid event id", http.StatusBadRequest)
		return
	}
	if err := v.calendar.Delete(r.Context(), user.ID, id); err != nil {
		http.Error(w, "could not delete event", http.StatusInternalServerError)
		return
	}
	v.renderCalendarRows(w, r, user)
}

func (v *views) renderCalendarRows(w http.ResponseWriter, r *http.Request, user *identity.User) {
	list, err := v.calendar.List(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "could not load events", http.StatusInternalServerError)
		return
	}
	renderView(w, r, v.calendarT, "calendars-rows", viewData{Events: list})
}

func parseUUIDPath(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse id: %w", err)
	}
	return id, nil
}
