package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	netmail "net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/format/ics"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
)

var templateFuncs = template.FuncMap{
	"fmtDate":                  func(t time.Time) string { return t.Local().Format("Jan 2, 2006") },
	"fmtTime":                  func(t time.Time) string { return t.Local().Format("15:04") },
	"contactInitial":           contactInitial,
	"contactSearch":            contactSearchText,
	"contactMatches":           contactMatches,
	"mailboxIcon":              mailboxIcon,
	"mailboxTitle":             mailboxTitle,
	"senderName":               func(s string) string { name, _ := parseSender(s); return name },
	"senderEmail":              func(s string) string { _, addr := parseSender(s); return addr },
	"contactInitialFromSender": contactInitialFromSender,
	"joinRecipients":           func(recipients []string) string { return strings.Join(recipients, ", ") },
	"formatMailDate":           formatMailDate,
	"formatDetailDate":         formatDetailDate,
	"formatBytes":              formatBytes,
	"fmtDateOpt":               fmtDateOpt,
}

type viewData struct {
	Title          string
	Section        string
	User           *identity.User
	CSRFToken      string
	Contacts       []contacts.Contact
	Contact        *contacts.Contact
	Query          string
	MatchCount     int
	IsNew          bool
	Events         []calendar.Event
	Week           weekView
	Wide           bool
	PageCSS        template.CSS
	StyleNonce     string
	Error          string
	Success        string
	Mailboxes      []mail.MailboxInfo
	CurrentBox     string
	Messages       []mailViewItem
	Message        *mailViewDetail
	ComposeOpen    bool
	ComposeTo      string
	ComposeSubject string
	ComposeBody    string
	// ComposeRecipients lists contacts with emails for the To autocomplete.
	ComposeRecipients []composeRecipient
	// AppPasswords lists the user's device credentials, if configured.
	AppPasswords []identity.AppPassword
	// DevicesReady reports whether device setup (app passwords) is on.
	DevicesReady bool
}

type views struct {
	contacts contactsService
	calendar calendarService
	mail     mailService

	home         *template.Template
	contactsT    *template.Template
	contactEditT *template.Template
	calendarT    *template.Template
	mailT        *template.Template
	profileT     *template.Template
	login        *template.Template
}

func newViews(files fs.FS, contactService contactsService, calendarService calendarService, mailService mailService) (*views, error) {
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
	mailT, err := page("web/templates/mail.html")
	if err != nil {
		return nil, err
	}
	profileT, err := page("web/templates/profile.html")
	if err != nil {
		return nil, err
	}
	login, err := template.New("").Funcs(templateFuncs).ParseFS(files, "web/templates/login.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse login template: %w", err)
	}

	return &views{
		contacts: contactService, calendar: calendarService, mail: mailService,
		home: home, contactsT: contactsT, contactEditT: contactEditT,
		calendarT: calendarT, mailT: mailT, profileT: profileT, login: login,
	}, nil
}

func renderView(w http.ResponseWriter, r *http.Request, t *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Authenticated pages are personal and change often; keep browsers
	// (and any cache between us and them) from serving stale copies.
	w.Header().Set("Cache-Control", "no-store")
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
		data.Week.FormAttendees = strings.TrimSpace(r.FormValue("attendees"))
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
	data.Week.FormAttendees = strings.TrimSpace(r.FormValue("attendees"))
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
	event.Attendees = parseAttendeeList(r.FormValue("attendees"), user.Email)
	saved, err := v.calendar.Put(r.Context(), event)
	if err != nil {
		v.renderWeek(w, r, user, "Could not save the event.")
		return
	}
	if saved != nil && len(saved.Attendees) > 0 {
		v.sendEventInvites(r.Context(), user, *saved, "REQUEST", saved.Attendees)
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

	old := *event
	if hasTitle {
		event.Title = title
		event.Location = strings.TrimSpace(r.FormValue("location"))
		event.Description = strings.TrimSpace(r.FormValue("description"))
	}
	if r.Form.Has("attendees") {
		event.Attendees = parseAttendeeList(r.FormValue("attendees"), user.Email)
	}
	event.StartsAt = start.UTC()
	event.EndsAt = end.UTC()
	event.ETag = uuid.NewString()
	event.ICS = ""

	attendeesChanged := !equalEmails(old.Attendees, event.Attendees)
	detailsChanged := old.Title != event.Title ||
		old.Location != event.Location ||
		old.Description != event.Description ||
		!old.StartsAt.Equal(event.StartsAt) ||
		!old.EndsAt.Equal(event.EndsAt)
	notify := attendeesChanged || detailsChanged

	if len(event.Attendees) > 0 && notify {
		event.Sequence++
	}
	saved, err := v.calendar.Put(r.Context(), *event)
	if err != nil {
		v.renderWeekWithEdit(w, r, user, id, "Could not save the event.")
		return
	}

	removed := subtractEmails(old.Attendees, event.Attendees)
	if len(removed) > 0 && saved != nil {
		v.sendEventInvites(r.Context(), user, *saved, "CANCEL", removed)
	}
	if saved != nil && len(event.Attendees) > 0 && notify {
		v.sendEventInvites(r.Context(), user, *saved, "REQUEST", event.Attendees)
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
	if event, err := v.calendar.Get(r.Context(), user.ID, id); err == nil && event != nil && len(event.Attendees) > 0 {
		v.sendEventInvites(r.Context(), user, *event, "CANCEL", event.Attendees)
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

// sendEventInvites emails an iTIP invitation to recipients. Failures are
// logged but never block saving the event itself.
func (v *views) sendEventInvites(ctx context.Context, user *identity.User, event calendar.Event, method string, recipients []string) {
	if len(recipients) == 0 {
		return
	}
	cancelled := strings.ToUpper(strings.TrimSpace(method)) == "CANCEL"
	subject := "Invitation: " + event.Title
	if cancelled {
		subject = "Cancelled: " + event.Title
	}
	body := inviteMailBody(event.Title, event.Location, event.Description, event.StartsAt, event.EndsAt, cancelled)
	icsData := ics.BuildInvite(event, user.Email, method)
	for _, to := range recipients {
		if _, err := v.mail.SendInvite(ctx, user, to, subject, body, icsData, method); err != nil {
			obs.Log(ctx, slog.LevelError, "send invite failed",
				"recipient", to, "method", method, "error", err)
		}
	}
}

// equalEmails compares attendee lists case-insensitively, ignoring order.
func equalEmails(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, e := range a {
		counts[strings.ToLower(strings.TrimSpace(e))]++
	}
	for _, e := range b {
		key := strings.ToLower(strings.TrimSpace(e))
		if counts[key] == 0 {
			return false
		}
		counts[key]--
	}
	return true
}

// subtractEmails returns entries of a absent from b (case-insensitive).
func subtractEmails(a, b []string) []string {
	inB := map[string]bool{}
	for _, e := range b {
		inB[strings.ToLower(strings.TrimSpace(e))] = true
	}
	var out []string
	for _, e := range a {
		if !inB[strings.ToLower(strings.TrimSpace(e))] {
			out = append(out, e)
		}
	}
	return out
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
		address, err := netmail.ParseAddress(email.Value)
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

// composeRecipients flattens every filled email of the user's contacts into
// a sorted, deduplicated suggestion list for the compose To field.
func (v *views) composeRecipients(ctx context.Context, userID uuid.UUID) []composeRecipient {
	list, err := v.contacts.List(ctx, userID)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []composeRecipient
	for _, c := range list {
		name := strings.TrimSpace(c.DisplayName())
		for _, f := range c.Emails {
			email := strings.TrimSpace(f.Value)
			if !strings.Contains(email, "@") {
				continue
			}
			key := strings.ToLower(email)
			if seen[key] {
				continue
			}
			seen[key] = true
			label := name
			if label == "" || strings.EqualFold(label, email) {
				label = email
			}
			out = append(out, composeRecipient{Name: label, Email: email})
			if len(out) >= 200 {
				break
			}
		}
		if len(out) >= 200 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Email < out[j].Email
	})
	return out
}

func (v *views) mailPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	_ = v.mail.EnsureDefaultMailboxes(r.Context(), user.ID)
	boxes, err := v.mail.ListMailboxes(r.Context(), user.ID)
	if err != nil {
		obs.Log(r.Context(), slog.LevelError, "list mailboxes failed", "error", err)
	}
	currentBox := strings.TrimSpace(r.URL.Query().Get("box"))
	if currentBox == "" {
		currentBox = "INBOX"
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	var msgs []mail.Message
	if query != "" {
		msgs, err = v.mail.SearchMessages(r.Context(), user.ID, query)
		if err != nil {
			obs.Log(r.Context(), slog.LevelError, "search messages failed", "error", err)
		}
	} else {
		mb, err := v.mail.GetMailbox(r.Context(), user.ID, currentBox)
		if err == nil && mb != nil {
			msgs, err = v.mail.ListMessages(r.Context(), mb.ID, 50, 0)
			if err != nil {
				obs.Log(r.Context(), slog.LevelError, "list messages failed", "error", err)
			}
		}
	}

	items := make([]mailViewItem, 0, len(msgs))
	for _, m := range msgs {
		_, snippet := parseMailContent(m.RawMessage, m.MimeType)
		name, _ := parseSender(m.Sender)
		items = append(items, mailViewItem{
			ID:         m.ID.String(),
			Sender:     m.Sender,
			SenderName: name,
			Recipients: m.Recipients,
			Subject:    decodeHeader(m.Subject),
			Snippet:    snippet,
			Seen:       m.Seen,
			Flagged:    m.Flagged,
			ReceivedAt: m.ReceivedAt,
		})
	}

	composeOpen := r.URL.Query().Get("compose") == "true"
	composeTo := r.URL.Query().Get("to")
	composeSubject := r.URL.Query().Get("subject")

	renderView(w, r, v.mailT, "layout", viewData{
		Title:             "Mail",
		Section:           "mail",
		User:              user,
		CSRFToken:         csrfTokenFromRequest(r),
		Wide:              true,
		Mailboxes:         boxes,
		CurrentBox:        currentBox,
		Messages:          items,
		Query:             query,
		MatchCount:        len(items),
		ComposeOpen:       composeOpen,
		ComposeTo:         composeTo,
		ComposeSubject:    composeSubject,
		ComposeRecipients: v.composeRecipients(r.Context(), user.ID),
	})
}

func (v *views) mailDetailPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	id, err := parseUUIDPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	msg, mb, err := v.mail.GetMessage(r.Context(), user.ID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if !msg.Seen {
		_ = v.mail.UpdateFlags(r.Context(), msg.ID, true, msg.Flagged, msg.Answered, msg.Deleted, msg.Draft)
		msg.Seen = true
	}

	boxes, _ := v.mail.ListMailboxes(r.Context(), user.ID)

	currentBox := strings.TrimSpace(r.URL.Query().Get("box"))
	if currentBox == "" && mb != nil {
		currentBox = mb.Name
	}
	if currentBox == "" {
		currentBox = "INBOX"
	}

	bodyText, _ := parseMailContent(msg.RawMessage, msg.MimeType)
	bodyHTML, bodyCSS := parseMailHTML(msg.RawMessage)
	name, addr := parseSender(msg.Sender)
	invite, _ := findMailInvite(msg.RawMessage)
	onCalendar := false
	if invite != nil {
		if existing, err := v.calendar.GetByUID(r.Context(), user.ID, invite.UID); err == nil && existing != nil {
			onCalendar = true
		}
	}

	detail := &mailViewDetail{
		ID:               msg.ID.String(),
		Sender:           msg.Sender,
		SenderName:       name,
		SenderAddr:       addr,
		Recipients:       msg.Recipients,
		Subject:          decodeHeader(msg.Subject),
		BodyText:    bodyText,
		BodyHTML:    template.HTML(bodyHTML),
		BodyCSS:     template.CSS(bodyCSS),
		Seen:             msg.Seen,
		Flagged:          msg.Flagged,
		ReceivedAt:       msg.ReceivedAt,
		BoxName:          currentBox,
		Attachments:      mail.ParseAttachments(msg.RawMessage),
		Invite:           invite,
		InviteOnCalendar: onCalendar,
	}

	renderView(w, r, v.mailT, "layout", viewData{
		Title:             detail.Subject,
		Section:           "mail",
		User:              user,
		CSRFToken:         csrfTokenFromRequest(r),
		StyleNonce:        styleNonce(r.Context()),
		Wide:              true,
		Mailboxes:         boxes,
		CurrentBox:        currentBox,
		Message:           detail,
		ComposeRecipients: v.composeRecipients(r.Context(), user.ID),
	})
}

// maxComposeRequestBytes caps the whole compose POST (body + files).
const maxComposeRequestBytes = 16 << 20

func (v *views) renderComposeError(w http.ResponseWriter, r *http.Request, user *identity.User, box, to, subject, body, errMsg string) {
	boxes, _ := v.mail.ListMailboxes(r.Context(), user.ID)
	renderView(w, r, v.mailT, "layout", viewData{
		Title:             "Mail",
		Section:           "mail",
		User:              user,
		CSRFToken:         csrfTokenFromRequest(r),
		Wide:              true,
		Mailboxes:         boxes,
		CurrentBox:        box,
		ComposeOpen:       true,
		ComposeTo:         to,
		ComposeSubject:    subject,
		ComposeBody:       body,
		ComposeRecipients: v.composeRecipients(r.Context(), user.ID),
		Error:             errMsg,
	})
}

// collectUploads reads attached files from a multipart compose form.
// Browsers send an empty part when no file is chosen; those are skipped.
func collectUploads(r *http.Request) ([]mail.Attachment, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	var atts []mail.Attachment
	for _, fh := range r.MultipartForm.File["attachments"] {
		if strings.TrimSpace(fh.Filename) == "" {
			continue
		}
		if len(atts) >= mail.MaxAttachmentCount {
			return nil, fmt.Errorf("too many attachments (max %d)", mail.MaxAttachmentCount)
		}
		f, err := fh.Open()
		if err != nil {
			return nil, fmt.Errorf("cannot read %q", fh.Filename)
		}
		data, err := io.ReadAll(io.LimitReader(f, mail.MaxAttachmentBytes+1))
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("cannot read %q", fh.Filename)
		}
		if int64(len(data)) > mail.MaxAttachmentBytes {
			return nil, fmt.Errorf("%q exceeds %d MB", fh.Filename, mail.MaxAttachmentBytes>>20)
		}
		atts = append(atts, mail.Attachment{
			Filename:    fh.Filename,
			ContentType: http.DetectContentType(data),
			Data:        data,
		})
	}
	return atts, nil
}

func (v *views) mailSend(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, maxComposeRequestBytes)
	if err := r.ParseMultipartForm(maxComposeRequestBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		obs.Log(r.Context(), slog.LevelWarn, "compose parse error", "error", err)
		v.renderComposeError(w, r, user, "INBOX", "", "", "", "Message too large (max 16 MB).")
		return
	}
	to := strings.TrimSpace(r.FormValue("to"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	body := r.FormValue("body")
	box := strings.TrimSpace(r.FormValue("box"))
	if box == "" {
		box = "INBOX"
	}

	if to == "" {
		v.renderComposeError(w, r, user, box, to, subject, body, "Recipient address is required.")
		return
	}

	atts, err := collectUploads(r)
	if err != nil {
		v.renderComposeError(w, r, user, box, to, subject, body, err.Error())
		return
	}

	if _, err := v.mail.SendMessageWithAttachments(r.Context(), user, to, subject, body, atts); err != nil {
		obs.Log(r.Context(), slog.LevelError, "send message failed", "error", err)
		v.renderComposeError(w, r, user, box, to, subject, body, "Failed to send message: "+err.Error())
		return
	}

	http.Redirect(w, r, "/mail?box=Sent", http.StatusSeeOther)
}

// mailAttachmentDownload serves one attachment of a message the user owns.
// Files are always served as downloads (never inline) with nosniff so a
// hostile HTML/SVG attachment cannot run in the session origin.
func (v *views) mailAttachmentDownload(w http.ResponseWriter, r *http.Request, user *identity.User) {
	id, err := parseUUIDPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil || idx < 0 {
		http.NotFound(w, r)
		return
	}

	msg, _, err := v.mail.GetMessage(r.Context(), user.ID, id)
	if err != nil || msg == nil {
		http.NotFound(w, r)
		return
	}
	att, err := mail.ExtractAttachment(msg.RawMessage, idx)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	contentType := "application/octet-stream"
	if mt, _, err := mime.ParseMediaType(att.ContentType); err == nil && mt != "" {
		contentType = mt
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "attachment; "+encodeDispositionFilename(att.Filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, att.Filename, msg.ReceivedAt, bytes.NewReader(att.Data))
}

// mailAddToCalendar imports a meeting invitation from a message into the
// user's calendar. Re-adding applies newer SEQUENCE updates; CANCEL removes
// the matching event. Events are matched by iCalendar UID.
func (v *views) mailAddToCalendar(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	id, err := parseUUIDPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	msg, _, err := v.mail.GetMessage(r.Context(), user.ID, id)
	if err != nil || msg == nil {
		http.NotFound(w, r)
		return
	}
	invite, ok := findMailInvite(msg.RawMessage)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if invite.IsCancel() {
		if existing, err := v.calendar.GetByUID(r.Context(), user.ID, invite.UID); err == nil && existing != nil {
			_ = v.calendar.Delete(r.Context(), user.ID, existing.ID)
		}
		http.Redirect(w, r, "/calendars", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, v.upsertInviteEvent(r.Context(), user, invite), http.StatusSeeOther)
}

// upsertInviteEvent imports a new invitation or applies a newer SEQUENCE
// update to the matching event. It returns the redirect target.
func (v *views) upsertInviteEvent(ctx context.Context, user *identity.User, invite *mailInvite) string {
	if existing, err := v.calendar.GetByUID(ctx, user.ID, invite.UID); err == nil && existing != nil {
		if invite.Sequence < existing.Sequence {
			return inviteWeekPath(existing.StartsAt)
		}
		existing.Title = invite.Title
		existing.Location = invite.Location
		existing.Description = invite.Description
		if !invite.StartsAt.IsZero() {
			existing.StartsAt = invite.StartsAt
		}
		if !invite.EndsAt.IsZero() {
			existing.EndsAt = invite.EndsAt
		}
		existing.Sequence = invite.Sequence
		existing.ICS = invite.ICS
		existing.ETag = uuid.NewString()
		if saved, err := v.calendar.Put(ctx, *existing); err == nil && saved != nil {
			return inviteWeekPath(saved.StartsAt)
		}
		return inviteWeekPath(existing.StartsAt)
	}

	event, err := ics.Parse(invite.ICS, user.ID, inviteResource(invite.UID))
	if err != nil {
		obs.Log(ctx, slog.LevelError, "import invite failed", "error", err)
		return "/calendars"
	}
	event.ETag = uuid.NewString()
	if saved, err := v.calendar.Put(ctx, event); err != nil {
		obs.Log(ctx, slog.LevelError, "import invite failed", "error", err)
		return "/calendars"
	} else {
		return inviteWeekPath(saved.StartsAt)
	}
}

// mailRSVP answers an invitation like Outlook's Accept/Maybe/Decline: it
// emails a METHOD:REPLY to the organizer and, unless declining, imports the
// event (a decline removes an already-imported one).
func (v *views) mailRSVP(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	back := "/mail"
	id, err := parseUUIDPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	msg, _, err := v.mail.GetMessage(r.Context(), user.ID, id)
	if err != nil || msg == nil {
		http.NotFound(w, r)
		return
	}
	back = "/mail/message/" + id.String()
	invite, ok := findMailInvite(msg.RawMessage)
	if !ok || invite.Organizer == "" {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}

	partstat, verb := "", ""
	switch strings.ToLower(strings.TrimSpace(r.FormValue("response"))) {
	case "accept":
		partstat, verb = "ACCEPTED", "accepted"
	case "tentative":
		partstat, verb = "TENTATIVE", "tentatively accepted"
	case "decline":
		partstat, verb = "DECLINED", "declined"
	default:
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}

	title := firstNonEmpty(decodeHeader(msg.Subject), invite.Title, "the invitation")
	name := strings.TrimSpace(user.DisplayName)
	if name == "" {
		name = user.Email
	}
	icsData := ics.BuildReply(invite.UID, invite.Sequence, invite.Organizer, user.Email, partstat, time.Now())
	body := name + " has " + verb + " '" + title + "'."
	if _, err := v.mail.SendInvite(r.Context(), user, invite.Organizer, "Re: "+title, body, icsData, "REPLY"); err != nil {
		obs.Log(r.Context(), slog.LevelError, "send reply failed", "error", err)
	}

	if partstat == "DECLINED" {
		if existing, err := v.calendar.GetByUID(r.Context(), user.ID, invite.UID); err == nil && existing != nil {
			_ = v.calendar.Delete(r.Context(), user.ID, existing.ID)
		}
	} else {
		v.upsertInviteEvent(r.Context(), user, invite)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// inviteResource derives a URL-safe CalDAV resource name from an iTIP UID.
func inviteResource(uid string) string {
	var b strings.Builder
	for _, r := range uid {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.' || r == '-' || r == '_' || r == '@':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return uuid.NewString()
	}
	return b.String()
}

// encodeDispositionFilename renders a safe Content-Disposition filename
// parameter, with RFC 5987 encoding for non-ASCII names.
func encodeDispositionFilename(name string) string {
	ascii := true
	for i := 0; i < len(name); i++ {
		if name[i] < 0x20 || name[i] >= 0x7f || name[i] == '"' || name[i] == '\\' {
			ascii = false
			break
		}
	}
	safe := strings.ReplaceAll(name, `"`, `'`)
	safe = strings.ReplaceAll(safe, `\`, "_")
	if ascii {
		return `filename="` + safe + `"`
	}
	return `filename="attachment"; filename*=UTF-8''` + url.PathEscape(name)
}

func (v *views) mailToggleStar(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	id, err := parseUUIDPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	msg, _, err := v.mail.GetMessage(r.Context(), user.ID, id)
	if err == nil && msg != nil {
		_ = v.mail.UpdateFlags(r.Context(), msg.ID, msg.Seen, !msg.Flagged, msg.Answered, msg.Deleted, msg.Draft)
	}
	box := r.FormValue("box")
	target := "/mail"
	if box != "" {
		target += "?box=" + box
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (v *views) mailToggleRead(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	id, err := parseUUIDPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	msg, _, err := v.mail.GetMessage(r.Context(), user.ID, id)
	if err == nil && msg != nil {
		_ = v.mail.UpdateFlags(r.Context(), msg.ID, !msg.Seen, msg.Flagged, msg.Answered, msg.Deleted, msg.Draft)
	}
	box := r.FormValue("box")
	target := "/mail"
	if box != "" {
		target += "?box=" + box
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (v *views) mailDelete(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	id, err := parseUUIDPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = v.mail.DeleteMessage(r.Context(), user.ID, id)
	box := r.FormValue("box")
	target := "/mail"
	if box != "" {
		target += "?box=" + box
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
