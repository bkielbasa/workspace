// Package web implements the server-rendered web UI and cookie session flow.
package web

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

type contactsService interface {
	List(context.Context, uuid.UUID) ([]contacts.Contact, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (contacts.Contact, error)
	PutStructured(context.Context, uuid.UUID, *uuid.UUID, contacts.Contact) (*contacts.Contact, error)
	DeleteByID(context.Context, uuid.UUID, uuid.UUID) error
}

type calendarService interface {
	Get(context.Context, uuid.UUID, uuid.UUID) (*calendar.Event, error)
	List(context.Context, uuid.UUID) ([]calendar.Event, error)
	Put(context.Context, calendar.Event) (*calendar.Event, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
}

type mailService interface {
	EnsureDefaultMailboxes(context.Context, uuid.UUID) error
	ListMailboxes(context.Context, uuid.UUID) ([]mail.MailboxInfo, error)
	GetMailbox(context.Context, uuid.UUID, string) (*mail.Mailbox, error)
	ListMessages(context.Context, uuid.UUID, int, int) ([]mail.Message, error)
	SearchMessages(context.Context, uuid.UUID, string) ([]mail.Message, error)
	GetMessage(context.Context, uuid.UUID, uuid.UUID) (*mail.Message, *mail.Mailbox, error)
	UpdateFlags(context.Context, uuid.UUID, bool, bool, bool, bool, bool) error
	DeleteMessage(context.Context, uuid.UUID, uuid.UUID) error
	SendMessage(context.Context, *identity.User, string, string, string) (*mail.Message, error)
}

type sessionsService interface {
	Create(context.Context, uuid.UUID, time.Duration) (*identity.Session, error)
	GetByToken(context.Context, string) (*identity.Session, error)
	Delete(context.Context, string) error
}

type usersService interface {
	Authenticate(context.Context, string, string) (*identity.User, error)
	Get(context.Context, uuid.UUID) (*identity.User, error)
}

// Server owns the web templates, assets, and cookie authentication policy.
type Server struct {
	files    fs.FS
	contacts contactsService
	calendar calendarService
	mail     mailService
	sessions sessionsService
	users    usersService
	secure   bool
	limiter  *loginLimiter
	views    *views
}

// New constructs the web server from the root embedded filesystem and services.
// files must contain the existing web/templates and web/static directories.
func New(files fs.FS, contacts contactsService, calendars calendarService, mail mailService, sessions sessionsService, users usersService, secure bool) (*Server, error) {
	switch {
	case files == nil:
		return nil, fmt.Errorf("web: nil filesystem")
	case contacts == nil:
		return nil, fmt.Errorf("web: nil contacts service")
	case calendars == nil:
		return nil, fmt.Errorf("web: nil calendar service")
	case mail == nil:
		return nil, fmt.Errorf("web: nil mail service")
	case sessions == nil:
		return nil, fmt.Errorf("web: nil sessions service")
	case users == nil:
		return nil, fmt.Errorf("web: nil users service")
	}

	v, err := newViews(files, contacts, calendars, mail)
	if err != nil {
		return nil, err
	}
	return &Server{
		files: files, contacts: contacts, calendar: calendars, mail: mail,
		sessions: sessions, users: users, secure: secure,
		limiter: newLoginLimiter(30, 15*time.Minute), views: v,
	}, nil
}

// RegisterRoutes registers all routes owned by the web package.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.Handle("/static/", s.staticHandler())

	mux.HandleFunc("GET /{$}", s.page(s.views.homePage))
	mux.HandleFunc("GET /me", s.RequireAuth(s.me))
	mux.HandleFunc("POST /logout", s.RequireAuth(s.RequireCSRF(s.logout)))

	mux.HandleFunc("GET /mail", s.page(s.views.mailPage))
	mux.HandleFunc("GET /mail/message/{id}", s.page(s.views.mailDetailPage))
	mux.HandleFunc("POST /mail/send", s.RequireAuth(s.RequireCSRF(s.views.mailSend)))
	mux.HandleFunc("POST /mail/message/{id}/toggle-star", s.RequireAuth(s.RequireCSRF(s.views.mailToggleStar)))
	mux.HandleFunc("POST /mail/message/{id}/toggle-read", s.RequireAuth(s.RequireCSRF(s.views.mailToggleRead)))
	mux.HandleFunc("POST /mail/message/{id}/delete", s.RequireAuth(s.RequireCSRF(s.views.mailDelete)))

	mux.HandleFunc("GET /contacts", s.page(s.views.contactsPage))
	mux.HandleFunc("GET /contacts/new", s.page(s.views.contactNewPage))
	mux.HandleFunc("POST /contacts", s.RequireAuth(s.RequireCSRF(s.views.contactCreate)))
	mux.HandleFunc("GET /contacts/{id}", s.page(s.views.contactPage))
	mux.HandleFunc("GET /contacts/{id}/edit", s.page(s.views.contactEditPage))
	mux.HandleFunc("POST /contacts/{id}", s.RequireAuth(s.RequireCSRF(s.views.contactUpdate)))
	mux.HandleFunc("DELETE /contacts/{id}", s.RequireAuth(s.RequireCSRF(s.views.contactsDelete)))

	mux.HandleFunc("GET /calendars", s.page(s.views.calendarsPage))
	mux.HandleFunc("POST /calendars", s.RequireAuth(s.RequireCSRF(s.views.calendarsAdd)))
	mux.HandleFunc("POST /calendars/{id}", s.RequireAuth(s.RequireCSRF(s.views.calendarsUpdate)))
	mux.HandleFunc("DELETE /calendars/{id}", s.RequireAuth(s.RequireCSRF(s.views.calendarsDelete)))
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.validSession(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	renderView(w, r, s.views.login, "login", nil)
}

func (s *Server) validSession(r *http.Request) bool {
	token := sessionToken(r)
	if token == "" {
		return false
	}
	_, err := s.sessions.GetByToken(r.Context(), token)
	return err == nil
}

func csrfTokenFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Server) staticHandler() http.Handler {
	sub, err := fs.Sub(s.files, "web/static")
	if err != nil {
		panic(err)
	}
	inner := http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		inner.ServeHTTP(w, r)
	})
}
