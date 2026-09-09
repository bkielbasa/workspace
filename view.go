package main

import (
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// tmplFuncs are shared formatters available to every template.
var tmplFuncs = template.FuncMap{
	"fmtDate": func(t time.Time) string { return t.Local().Format("Jan 2, 2006") },
	"fmtTime": func(t time.Time) string { return t.Local().Format("15:04") },
}

// viewData is passed to every page. Page templates use Title, Section, User
// and CSRFToken (via the layout); fragments use the list fields.
type viewData struct {
	Title     string
	Section   string
	User      *User
	CSRFToken string

	Contacts []Contact
	Events   []Event
	Error    string
}

// views owns the rendered page templates and the services they need. Each page
// gets its own template set: layout + nav + shared fragments + that page's
// content template, so "content" never collides between pages.
type views struct {
	contacts *Contacts
	cal      *Calendar

	home       *template.Template
	contactsT  *template.Template
	calendarsT *template.Template
	loginT     *template.Template
}

func newViews(contacts *Contacts, cal *Calendar) *views {
	base := []string{
		"web/templates/layout.html",
		"web/templates/nav.html",
		"web/templates/contacts-rows.html",
		"web/templates/calendars-rows.html",
	}
	page := func(extra ...string) *template.Template {
		files := append(append([]string{}, base...), extra...)
		return template.Must(template.New("").Funcs(tmplFuncs).ParseFS(webFS, files...))
	}

	return &views{
		contacts:   contacts,
		cal:        cal,
		home:       page("web/templates/home.html"),
		contactsT:  page("web/templates/contacts.html"),
		calendarsT: page("web/templates/calendars.html"),
		loginT: template.Must(template.New("").Funcs(tmplFuncs).
			ParseFS(webFS, "web/templates/login.html")),
	}
}

func renderView(w http.ResponseWriter, t *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

// pageHandler renders a full HTML page for an authenticated user.
type pageHandler func(w http.ResponseWriter, r *http.Request, user *User)

// ---- pages --------------------------------------------------------------

func (v *views) homePage(w http.ResponseWriter, r *http.Request, user *User) {
	renderView(w, v.home, "layout", viewData{
		Title:     "Home",
		Section:   "home",
		User:      user,
		CSRFToken: csrfTokenFromRequest(r),
	})
}

func (v *views) contactsPage(w http.ResponseWriter, r *http.Request, user *User) {
	contacts, err := v.contacts.List(r.Context(), user.ID)
	if err != nil {
		renderView(w, v.contactsT, "layout", viewData{
			Title: "Contacts", Section: "contacts", User: user,
			CSRFToken: csrfTokenFromRequest(r), Error: "Could not load your contacts.",
		})
		return
	}
	renderView(w, v.contactsT, "layout", viewData{
		Title: "Contacts", Section: "contacts", User: user,
		CSRFToken: csrfTokenFromRequest(r), Contacts: contacts,
	})
}

func (v *views) calendarsPage(w http.ResponseWriter, r *http.Request, user *User) {
	events, err := v.cal.List(r.Context(), user.ID)
	if err != nil {
		renderView(w, v.calendarsT, "layout", viewData{
			Title: "Calendars", Section: "calendars", User: user,
			CSRFToken: csrfTokenFromRequest(r), Error: "Could not load your events.",
		})
		return
	}
	renderView(w, v.calendarsT, "layout", viewData{
		Title: "Calendars", Section: "calendars", User: user,
		CSRFToken: csrfTokenFromRequest(r), Events: events,
	})
}

// ---- contacts fragments (HTMX) ------------------------------------------

func (v *views) contactsRows(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	contacts, err := v.contacts.List(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "could not load contacts", http.StatusInternalServerError)
		return
	}
	renderView(w, v.contactsT, "contacts-rows", viewData{Contacts: contacts})
}

func (v *views) contactsAdd(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	ct := Contact{
		FirstName: strings.TrimSpace(r.FormValue("first_name")),
		LastName:  strings.TrimSpace(r.FormValue("last_name")),
		Company:   strings.TrimSpace(r.FormValue("company")),
		Title:     strings.TrimSpace(r.FormValue("title")),
	}
	if email := strings.TrimSpace(r.FormValue("email")); email != "" {
		ct.Emails = []VCardField{{Value: strings.ToLower(email), Type: []string{"INTERNET"}}}
	}
	if phone := strings.TrimSpace(r.FormValue("phone")); phone != "" {
		ct.Phones = []VCardField{{Value: phone, Type: []string{"CELL", "VOICE"}}}
	}

	data := viewData{Error: ""}
	if len(ct.Emails) == 0 || !strings.Contains(ct.Emails[0].Value, "@") {
		data.Error = "A valid email address is required."
	} else if _, err := v.contacts.PutContact(r.Context(), user.ID, nil, ct); err != nil {
		data.Error = "Could not save the contact."
	}

	contacts, _ := v.contacts.List(r.Context(), user.ID)
	data.Contacts = contacts
	renderView(w, v.contactsT, "contacts-rows", data)
}

// contactsAddField appends an email or phone to an existing contact and
// re-renders the rows fragment.
func (v *views) contactsAddField(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		id, err := parseUUIDPath(r)
		if err != nil {
			http.Error(w, "invalid contact id", http.StatusBadRequest)
			return
		}
		if kind != "emails" && kind != "phones" {
			http.Error(w, "unknown field kind", http.StatusBadRequest)
			return
		}
		ct, err := v.contacts.Get(r.Context(), user.ID, id)
		if err != nil {
			http.Error(w, "contact not found", http.StatusNotFound)
			return
		}
		field := "phone"
		if kind == "emails" {
			field = "email"
		}
		val := strings.TrimSpace(r.FormValue(field))
		if val == "" {
			http.Error(w, "a value is required", http.StatusBadRequest)
			return
		}
		if kind == "emails" {
			ct.Emails = append(ct.Emails, VCardField{Value: strings.ToLower(val), Type: []string{"INTERNET"}})
		} else {
			ct.Phones = append(ct.Phones, VCardField{Value: val, Type: []string{"CELL", "VOICE"}})
		}
		if _, err := v.contacts.PutContact(r.Context(), user.ID, &id, ct); err != nil {
			http.Error(w, "could not update contact", http.StatusInternalServerError)
			return
		}
		v.renderContactsRows(w, r, user)
	}
}

// contactsRemoveField deletes the field at index from an existing contact.
func (v *views) contactsRemoveField(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		id, err := parseUUIDPath(r)
		if err != nil {
			http.Error(w, "invalid contact id", http.StatusBadRequest)
			return
		}
		idx, err := strconv.Atoi(r.PathValue("index"))
		if err != nil || idx < 0 {
			http.Error(w, "invalid field index", http.StatusBadRequest)
			return
		}
		ct, err := v.contacts.Get(r.Context(), user.ID, id)
		if err != nil {
			http.Error(w, "contact not found", http.StatusNotFound)
			return
		}
		if kind == "emails" {
			if idx >= len(ct.Emails) {
				http.Error(w, "invalid field index", http.StatusBadRequest)
				return
			}
			ct.Emails = append(ct.Emails[:idx], ct.Emails[idx+1:]...)
		} else {
			if idx >= len(ct.Phones) {
				http.Error(w, "invalid field index", http.StatusBadRequest)
				return
			}
			ct.Phones = append(ct.Phones[:idx], ct.Phones[idx+1:]...)
		}
		if _, err := v.contacts.PutContact(r.Context(), user.ID, &id, ct); err != nil {
			http.Error(w, "could not update contact", http.StatusInternalServerError)
			return
		}
		v.renderContactsRows(w, r, user)
	}
}

func (v *views) renderContactsRows(w http.ResponseWriter, r *http.Request, user *User) {
	contacts, err := v.contacts.List(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "could not load contacts", http.StatusInternalServerError)
		return
	}
	renderView(w, v.contactsT, "contacts-rows", viewData{Contacts: contacts})
}

func (v *views) contactsDelete(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	id, err := parseUUIDPath(r)
	if err != nil {
		http.Error(w, "invalid contact id", http.StatusBadRequest)
		return
	}
	if err := v.contacts.DeleteByID(r.Context(), user.ID, id); err != nil {
		http.Error(w, "could not delete contact", http.StatusInternalServerError)
		return
	}
	contacts, err := v.contacts.List(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "could not load contacts", http.StatusInternalServerError)
		return
	}
	renderView(w, v.contactsT, "contacts-rows", viewData{Contacts: contacts})
}

// ---- calendar fragments (HTMX) ------------------------------------------

func (v *views) calendarsRows(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	events, err := v.cal.List(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "could not load events", http.StatusInternalServerError)
		return
	}
	renderView(w, v.calendarsT, "calendars-rows", viewData{Events: events})
}

func (v *views) calendarsAdd(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	title := strings.TrimSpace(r.FormValue("title"))
	start, err1 := time.ParseInLocation("2006-01-02T15:04", strings.TrimSpace(r.FormValue("starts_at")), time.Local)
	end, err2 := time.ParseInLocation("2006-01-02T15:04", strings.TrimSpace(r.FormValue("ends_at")), time.Local)

	data := viewData{Error: ""}
	switch {
	case title == "":
		data.Error = "A title is required."
	case err1 != nil || err2 != nil:
		data.Error = "Enter valid start and end times."
	case !end.After(start):
		data.Error = "The event must end after it starts."
	default:
		if _, err := v.cal.Upsert(r.Context(), user.ID, title, start.UTC(), end.UTC()); err != nil {
			data.Error = "Could not save the event."
		}
	}

	events, _ := v.cal.List(r.Context(), user.ID)
	data.Events = events
	renderView(w, v.calendarsT, "calendars-rows", data)
}

func (v *views) calendarsDelete(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	id, err := parseUUIDPath(r)
	if err != nil {
		http.Error(w, "invalid event id", http.StatusBadRequest)
		return
	}
	if err := v.cal.Delete(r.Context(), user.ID, id); err != nil {
		http.Error(w, "could not delete event", http.StatusInternalServerError)
		return
	}
	events, err := v.cal.List(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "could not load events", http.StatusInternalServerError)
		return
	}
	renderView(w, v.calendarsT, "calendars-rows", viewData{Events: events})
}

func parseUUIDPath(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(r.PathValue("id"))
}
