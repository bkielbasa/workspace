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
	"github.com/google/uuid"
)

type contactsService interface {
	List(context.Context, uuid.UUID) ([]contacts.Contact, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (contacts.Contact, error)
	PutStructured(context.Context, uuid.UUID, *uuid.UUID, contacts.Contact) (*contacts.Contact, error)
	DeleteByID(context.Context, uuid.UUID, uuid.UUID) error
}

type calendarService interface {
	List(context.Context, uuid.UUID) ([]calendar.Event, error)
	Put(context.Context, calendar.Event) (*calendar.Event, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
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
	sessions sessionsService
	users    usersService
	secure   bool
	limiter  *loginLimiter
	views    *views
}

// New constructs the web server from the root embedded filesystem and services.
// files must contain the existing web/templates and web/static directories.
func New(files fs.FS, contacts contactsService, calendars calendarService, sessions sessionsService, users usersService, secure bool) (*Server, error) {
	switch {
	case files == nil:
		return nil, fmt.Errorf("web: nil filesystem")
	case contacts == nil:
		return nil, fmt.Errorf("web: nil contacts service")
	case calendars == nil:
		return nil, fmt.Errorf("web: nil calendar service")
	case sessions == nil:
		return nil, fmt.Errorf("web: nil sessions service")
	case users == nil:
		return nil, fmt.Errorf("web: nil users service")
	}

	v, err := newViews(files, contacts, calendars)
	if err != nil {
		return nil, err
	}
	return &Server{
		files: files, contacts: contacts, calendar: calendars,
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

	mux.HandleFunc("GET /contacts", s.page(s.views.contactsPage))
	mux.HandleFunc("GET /contacts/rows", s.RequireAuth(s.views.contactsRows))
	mux.HandleFunc("POST /contacts/rows", s.RequireAuth(s.RequireCSRF(s.views.contactsAdd)))
	mux.HandleFunc("DELETE /contacts/{id}", s.RequireAuth(s.RequireCSRF(s.views.contactsDelete)))
	mux.HandleFunc("POST /contacts/{id}/emails", s.RequireAuth(s.RequireCSRF(s.views.contactsAddField("emails"))))
	mux.HandleFunc("POST /contacts/{id}/phones", s.RequireAuth(s.RequireCSRF(s.views.contactsAddField("phones"))))
	mux.HandleFunc("DELETE /contacts/{id}/emails/{index}", s.RequireAuth(s.RequireCSRF(s.views.contactsRemoveField("emails"))))
	mux.HandleFunc("DELETE /contacts/{id}/phones/{index}", s.RequireAuth(s.RequireCSRF(s.views.contactsRemoveField("phones"))))

	mux.HandleFunc("GET /calendars", s.page(s.views.calendarsPage))
	mux.HandleFunc("GET /calendars/rows", s.RequireAuth(s.views.calendarsRows))
	mux.HandleFunc("POST /calendars/rows", s.RequireAuth(s.RequireCSRF(s.views.calendarsAdd)))
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
