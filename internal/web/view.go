package web

import (
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
)

var templateFuncs = template.FuncMap{
	"fmtDate":        func(t time.Time) string { return t.Local().Format("Jan 2, 2006") },
	"fmtTime":        func(t time.Time) string { return t.Local().Format("15:04") },
	"contactInitial": contactInitial,
	"contactSearch":  contactSearchText,
	"contactMatches": contactMatches,
}

type viewData struct {
	Title      string
	Section    string
	User       *identity.User
	CSRFToken  string
	Contacts   []contacts.Contact
	Contact    *contacts.Contact
	Query      string
	MatchCount int
	IsNew      bool
	Events     []calendar.Event
	Week       weekView
	Wide       bool
	PageCSS    template.CSS
	StyleNonce string
	Error      string
}

type views struct {
	contacts contactsService
	calendar calendarService

	home         *template.Template
	contactsT    *template.Template
	contactEditT *template.Template
	calendarT    *template.Template
	login        *template.Template
}

func newViews(files fs.FS, contactService contactsService, calendarService calendarService) (*views, error) {
	base := []string{
		"web/templates/layout.html",
		"web/templates/nav.html",
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
	contactEditT, err := page("web/templates/contact-edit.html")
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
		home: home, contactsT: contactsT, contactEditT: contactEditT,
		calendarT: calendarT, login: login,
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
	v.renderContactsPage(w, r, user, nil)
}

func (v *views) contactPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	id, err := parseUUIDPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contact, err := v.contacts.Get(r.Context(), user.ID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	v.renderContactsPage(w, r, user, &contact)
}

func (v *views) renderContactsPage(w http.ResponseWriter, r *http.Request, user *identity.User, selected *contacts.Contact) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	data := viewData{
		Title: "Contacts", Section: "contacts", User: user,
		CSRFToken: csrfTokenFromRequest(r), Contact: selected, Query: query,
	}
	list, err := v.contacts.List(r.Context(), user.ID)
	if err != nil {
		data.Error = "Could not load your contacts."
	} else {
		sort.SliceStable(list, func(i, j int) bool {
			return strings.ToLower(list[i].DisplayName()) < strings.ToLower(list[j].DisplayName())
		})
		data.Contacts = list
		data.MatchCount = len(filterContacts(list, query))
	}
	renderView(w, r, v.contactsT, "layout", data)
}

func (v *views) contactNewPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	contact := contacts.Contact{}
	renderView(w, r, v.contactEditT, "layout", viewData{
		Title: "New contact", Section: "contacts", User: user,
		CSRFToken: csrfTokenFromRequest(r), Contact: &contact, IsNew: true,
	})
}

func (v *views) contactEditPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	id, err := parseUUIDPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contact, err := v.contacts.Get(r.Context(), user.ID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	renderView(w, r, v.contactEditT, "layout", viewData{
		Title: "Edit " + contact.DisplayName(), Section: "contacts", User: user,
		CSRFToken: csrfTokenFromRequest(r), Contact: &contact,
	})
}

func (v *views) contactCreate(w http.ResponseWriter, r *http.Request) {
	v.saveContact(w, r, UserFromContext(r.Context()), nil)
}

func (v *views) contactUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDPath(r)
	if err != nil {
		http.Error(w, "invalid contact id", http.StatusBadRequest)
		return
	}
	v.saveContact(w, r, UserFromContext(r.Context()), &id)
}

func (v *views) saveContact(w http.ResponseWriter, r *http.Request, user *identity.User, id *uuid.UUID) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	contact := contactFromForm(r)
	if id != nil {
		contact.ID = *id
	}
	if message := validateContact(contact); message != "" {
		renderView(w, r, v.contactEditT, "layout", viewData{
			Title: "Edit contact", Section: "contacts", User: user,
			CSRFToken: csrfTokenFromRequest(r), Contact: &contact,
			IsNew: id == nil, Error: message,
		})
		return
	}
	saved, err := v.contacts.PutStructured(r.Context(), user.ID, id, contact)
	if err != nil {
		renderView(w, r, v.contactEditT, "layout", viewData{
			Title: "Edit contact", Section: "contacts", User: user,
			CSRFToken: csrfTokenFromRequest(r), Contact: &contact,
			IsNew: id == nil, Error: "Could not save the contact.",
		})
		return
	}
	http.Redirect(w, r, "/contacts/"+saved.ID.String(), http.StatusSeeOther)
}

func (v *views) calendarsPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	v.renderWeek(w, r, user, "")
}

func (v *views) renderWeek(w http.ResponseWriter, r *http.Request, user *identity.User, errMsg string) {
	query := strings.TrimSpace(r.URL.Query().Get("week"))
	if query == "" {
		query = strings.TrimSpace(r.FormValue("week"))
	}
	data := viewData{
		Title: "Calendar", Section: "calendars", User: user,
		CSRFToken: csrfTokenFromRequest(r), Wide: true, Error: errMsg,
	}
	list, err := v.calendar.List(r.Context(), user.ID)
	if err != nil && data.Error == "" {
		data.Error = "Could not load your events."
	}
	data.Week = buildWeek(time.Now(), list, query)
	if errMsg != "" {
		data.Week.FormOpen = true
		data.Week.FormAction = "/calendars"
		data.Week.FormHeading = "New event"
		data.Week.FormSubmit = "Add event"
		data.Week.FormTitle = strings.TrimSpace(r.FormValue("title"))
		data.Week.FormLocation = strings.TrimSpace(r.FormValue("location"))
		data.Week.FormDescription = strings.TrimSpace(r.FormValue("description"))
		if start := strings.TrimSpace(r.FormValue("starts_at")); start != "" {
			data.Week.StartValue = start
		}
		if end := strings.TrimSpace(r.FormValue("ends_at")); end != "" {
			data.Week.EndValue = end
		}
	}
	data.PageCSS = data.Week.CSS
	data.StyleNonce = styleNonce(r.Context())
	renderView(w, r, v.calendarT, "layout", data)
}

func (v *views) renderWeekWithEdit(w http.ResponseWriter, r *http.Request, user *identity.User, id uuid.UUID, errMsg string) {
	query := strings.TrimSpace(r.URL.Query().Get("week"))
	if query == "" {
		query = strings.TrimSpace(r.FormValue("week"))
	}
	data := viewData{
		Title: "Calendar", Section: "calendars", User: user,
		CSRFToken: csrfTokenFromRequest(r), Wide: true, Error: errMsg,
	}
	list, err := v.calendar.List(r.Context(), user.ID)
	if err != nil && data.Error == "" {
		data.Error = "Could not load your events."
	}
	data.Week = buildWeek(time.Now(), list, query)
	data.Week.FormOpen = true
	data.Week.FormAction = "/calendars/" + id.String()
	data.Week.FormHeading = "Edit event"
	data.Week.FormSubmit = "Save changes"
	data.Week.FormTitle = strings.TrimSpace(r.FormValue("title"))
	data.Week.FormLocation = strings.TrimSpace(r.FormValue("location"))
	data.Week.FormDescription = strings.TrimSpace(r.FormValue("description"))
	if start := strings.TrimSpace(r.FormValue("starts_at")); start != "" {
		data.Week.StartValue = start
	}
	if end := strings.TrimSpace(r.FormValue("ends_at")); end != "" {
		data.Week.EndValue = end
	}
	data.PageCSS = data.Week.CSS
	data.StyleNonce = styleNonce(r.Context())
	renderView(w, r, v.calendarT, "layout", data)
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
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/contacts")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/contacts", http.StatusSeeOther)
}

func (v *views) calendarsAdd(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	title := strings.TrimSpace(r.FormValue("title"))
	start, startErr := time.ParseInLocation("2006-01-02T15:04", strings.TrimSpace(r.FormValue("starts_at")), time.Local)
	end, endErr := time.ParseInLocation("2006-01-02T15:04", strings.TrimSpace(r.FormValue("ends_at")), time.Local)

	switch {
	case title == "":
		v.renderWeek(w, r, user, "A title is required.")
		return
	case startErr != nil || endErr != nil:
		v.renderWeek(w, r, user, "Enter valid start and end times.")
		return
	case !end.After(start):
		v.renderWeek(w, r, user, "The event must end after it starts.")
		return
	}
	event, err := calendar.NewWebEvent(user.ID, calendar.Details{
		Title:       title,
		Location:    strings.TrimSpace(r.FormValue("location")),
		Description: strings.TrimSpace(r.FormValue("description")),
		StartsAt:    start.UTC(),
		EndsAt:      end.UTC(),
	})
	if err != nil {
		v.renderWeek(w, r, user, "Could not save the event.")
		return
	}
	if _, err := v.calendar.Put(r.Context(), event); err != nil {
		v.renderWeek(w, r, user, "Could not save the event.")
		return
	}
	http.Redirect(w, r, weekPath(mondayOf(start).Format("2006-01-02")), http.StatusSeeOther)
}

func (v *views) calendarsUpdate(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	id, err := parseUUIDPath(r)
	if err != nil {
		http.Error(w, "invalid event id", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	event, err := v.calendar.Get(r.Context(), user.ID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	startStr := strings.TrimSpace(r.FormValue("starts_at"))
	endStr := strings.TrimSpace(r.FormValue("ends_at"))
	start, startErr := time.ParseInLocation("2006-01-02T15:04", startStr, time.Local)
	end, endErr := time.ParseInLocation("2006-01-02T15:04", endStr, time.Local)

	hasTitle := r.Form.Has("title")
	title := strings.TrimSpace(r.FormValue("title"))

	switch {
	case hasTitle && title == "":
		v.renderWeekWithEdit(w, r, user, id, "A title is required.")
		return
	case startErr != nil || endErr != nil:
		v.renderWeekWithEdit(w, r, user, id, "Enter valid start and end times.")
		return
	case !end.After(start):
		v.renderWeekWithEdit(w, r, user, id, "The event must end after it starts.")
		return
	}

	if hasTitle {
		event.Title = title
		event.Location = strings.TrimSpace(r.FormValue("location"))
		event.Description = strings.TrimSpace(r.FormValue("description"))
	}
	event.StartsAt = start.UTC()
	event.EndsAt = end.UTC()
	event.ETag = uuid.NewString()
	event.ICS = ""

	if _, err := v.calendar.Put(r.Context(), *event); err != nil {
		v.renderWeekWithEdit(w, r, user, id, "Could not save the event.")
		return
	}

	target := weekPath(strings.TrimSpace(r.FormValue("week")))
	if target == "/calendars" {
		target = weekPath(mondayOf(start).Format("2006-01-02"))
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
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
	target := weekPath(strings.TrimSpace(r.URL.Query().Get("week")))
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func parseUUIDPath(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse id: %w", err)
	}
	return id, nil
}

func contactFromForm(r *http.Request) contacts.Contact {
	contact := contacts.Contact{
		FirstName: strings.TrimSpace(r.FormValue("first_name")),
		LastName:  strings.TrimSpace(r.FormValue("last_name")),
		Company:   strings.TrimSpace(r.FormValue("company")),
		Title:     strings.TrimSpace(r.FormValue("title")),
	}
	contact.Emails = fieldsFromForm(r.Form["emails"], r.Form["email_kinds"], true)
	contact.Phones = fieldsFromForm(r.Form["phones"], r.Form["phone_kinds"], false)
	return contact
}

func fieldsFromForm(values, kinds []string, email bool) []contacts.Field {
	var fields []contacts.Field
	for i, value := range values {
		value = strings.TrimSpace(value)
		if email {
			value = strings.ToLower(value)
		}
		if value == "" {
			continue
		}
		kind := contacts.KindHome
		if i < len(kinds) {
			kind = contacts.ParseKind(kinds[i])
		}
		fields = append(fields, contacts.Field{
			Value: value,
			Type:  []string{contacts.TypeForKind(kind)},
		})
	}
	return fields
}

func validateContact(contact contacts.Contact) string {
	if contact.DisplayName() == "" && len(contact.Emails) == 0 && len(contact.Phones) == 0 {
		return "Add a name, company, email, or phone number."
	}
	for _, email := range contact.Emails {
		address, err := mail.ParseAddress(email.Value)
		if err != nil || !strings.EqualFold(address.Address, email.Value) {
			return "Enter a valid email address."
		}
	}
	return ""
}

func filterContacts(list []contacts.Contact, query string) []contacts.Contact {
	filtered := make([]contacts.Contact, 0)
	for _, contact := range list {
		if contactMatches(contact, query) {
			filtered = append(filtered, contact)
		}
	}
	return filtered
}

func contactMatches(contact contacts.Contact, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	return query == "" || strings.Contains(strings.ToLower(contactSearchText(contact)), query)
}

func contactSearchText(contact contacts.Contact) string {
	values := []string{
		contact.DisplayName(), contact.FirstName, contact.LastName,
		contact.Company, contact.Title, contact.Email,
	}
	for _, field := range contact.Emails {
		values = append(values, field.Value)
	}
	for _, field := range contact.Phones {
		values = append(values, field.Value)
	}
	return strings.Join(values, " ")
}

func contactInitial(contact contacts.Contact) string {
	name := strings.TrimSpace(contact.DisplayName())
	if name == "" {
		return "?"
	}
	return strings.ToUpper(string([]rune(name)[0]))
}
