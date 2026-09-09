package main

import (
	"html/template"
	"log"
	"net/http"
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
		Phone:     strings.TrimSpace(r.FormValue("phone")),
		Email:     strings.ToLower(strings.TrimSpace(r.FormValue("email"))),
	}

	data := viewData{Error: ""}
	if ct.Email == "" || !strings.Contains(ct.Email, "@") {
		data.Error = "A valid email address is required."
	} else if _, err := v.contacts.Put(r.Context(), user.ID, nil, ct); err != nil {
		data.Error = "Could not save the contact."
	}

	contacts, _ := v.contacts.List(r.Context(), user.ID)
	data.Contacts = contacts
	renderView(w, v.contactsT, "contacts-rows", data)
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
