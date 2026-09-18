// Package web implements the server-rendered web UI and cookie session flow.
package web

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/files"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/notes"
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
	GetByUID(context.Context, uuid.UUID, string) (*calendar.Event, error)
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
	SendMessageWithAttachments(context.Context, *identity.User, string, string, string, []mail.Attachment) (*mail.Message, error)
	SendInvite(context.Context, *identity.User, string, string, string, string, string) (*mail.Message, error)
}

type sessionsService interface {
	Create(context.Context, uuid.UUID, time.Duration) (*identity.Session, error)
	GetByToken(context.Context, string) (*identity.Session, error)
	Delete(context.Context, string) error
}

type appPasswordsService interface {
	Rotate(ctx context.Context, userID uuid.UUID, name string) (string, *identity.AppPassword, error)
	List(ctx context.Context, userID uuid.UUID) ([]identity.AppPassword, error)
	Revoke(ctx context.Context, userID, id uuid.UUID) error
}

// filesService backs the Drive file browser. It mirrors files.Store so the
// live store plugs in directly; virtual paths are slash-separated.
type filesService interface {
	EnsureUserRoot(home string) error
	Stat(home string, name string) (files.File, error)
	ListDir(home string, name string) ([]files.File, error)
	Open(home string, name string) (io.ReadSeekCloser, files.File, error)
	Write(home string, name string, data io.Reader, size int64) error
	Mkdir(home string, name string) error
	Move(home string, from, to string, overwrite bool) error
	Remove(home string, name string) error
	LocalPath(home string, name string) (string, error)
}

// photoUploadAuth verifies upload credentials (master or app password).
type photoUploadAuth interface {
	Authenticate(ctx context.Context, login, password string) (*identity.User, error)
}

// photoTagStore persists reusable photo tags (owner + library path).
type photoTagStore interface {
	Set(ctx context.Context, userID uuid.UUID, path string, tags []string) error
	ByPhoto(ctx context.Context, userID uuid.UUID) (map[string][]string, error)
	All(ctx context.Context, userID uuid.UUID) ([]string, error)
}

// photoAlbumStore persists albums; a photo belongs to many at once.
type photoAlbumStore interface {
	Create(ctx context.Context, userID uuid.UUID, name string) (*identity.PhotoAlbum, error)
	List(ctx context.Context, userID uuid.UUID) ([]identity.PhotoAlbum, error)
	Delete(ctx context.Context, userID, albumID uuid.UUID) error
	Add(ctx context.Context, userID, albumID uuid.UUID, path string) error
	Remove(ctx context.Context, userID, albumID uuid.UUID, path string) error
	Paths(ctx context.Context, userID, albumID uuid.UUID) ([]string, error)
	Memberships(ctx context.Context, userID uuid.UUID) (map[string][]uuid.UUID, error)
}

type usersService interface {
	Authenticate(context.Context, string, string) (*identity.User, error)
	Get(context.Context, uuid.UUID) (*identity.User, error)
	Update(context.Context, uuid.UUID, string, bool) error
	ChangePassword(context.Context, uuid.UUID, string) error
	List(context.Context) ([]identity.User, error)
	Delete(context.Context, uuid.UUID) error
	SetUsername(ctx context.Context, id uuid.UUID, username string) error
}

type invitesService interface {
	CreateInvite(ctx context.Context, email, displayName string) (string, *identity.Invite, error)
	Lookup(ctx context.Context, token string) (*identity.Invite, error)
	Accept(ctx context.Context, token, displayName, password string) (*identity.User, error)
	List(ctx context.Context) ([]identity.Invite, error)
	Revoke(ctx context.Context, id uuid.UUID) error
}

type notesService interface {
	Broker() *notes.Broker
	CreateNote(ctx context.Context, userID uuid.UUID, n notes.Note) (*notes.Note, error)
	GetNote(ctx context.Context, userID uuid.UUID, id uuid.UUID) (*notes.Note, error)
	ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]notes.Note, error)
	UpdateNote(ctx context.Context, userID uuid.UUID, isAdmin bool, n notes.Note) (*notes.Note, error)
	DeleteNote(ctx context.Context, userID uuid.UUID, isAdmin bool, id uuid.UUID) error
	AddItem(ctx context.Context, userID uuid.UUID, noteID uuid.UUID, content string) (*notes.NoteItem, error)
	AddItemWithID(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, content string) (*notes.NoteItem, error)
	UpdateItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, content string, completed bool) (*notes.NoteItem, error)
	ToggleItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, completed bool) (*notes.NoteItem, error)
	DeleteItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID) error
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
	// sso + oidc power the login-page "Sign in with ..." button. sso is a
	// small adapter over identity.SSO (provisioning/linking); oidc is the
	// live provider client, nil while SSO is disabled.
	sso  ssoservice
	oidc *oidcClient
	// appPasswords and DAV hosts wire the device setup (iPhone profile).
	// They are optional: without them the profile page hides that card.
	appPasswords appPasswordsService
	mailHost     string
	davHost      string
	// invites wires family onboarding. Optional like the above.
	invites invitesService
	// notes backs the Keep-style notes and checklists dashboard.
	notes notesService
}

// SetDeviceSetup enables the iPhone profile flow with embedded per-device
// app passwords. Call once after New; handlers tolerate it being unset.
func (s *Server) SetDeviceSetup(apps appPasswordsService, mailHost, davHost string) {
	s.appPasswords = apps
	s.mailHost = mailHost
	s.davHost = davHost
}

// SetInvites enables family invite management. Call once after New.
func (s *Server) SetInvites(invites invitesService) {
	s.invites = invites
}

// SetNotes enables the notes and checklists dashboard.
func (s *Server) SetNotes(svc notesService) {
	s.notes = svc
}

// SetFiles enables the Drive file browser backed by the file store.
func (s *Server) SetFiles(svc filesService) {
	s.views.files = svc
}

// SetPhotos enables the gallery and upload API on a separate photo tree.
func (s *Server) SetPhotos(svc filesService, auth photoUploadAuth) {
	s.views.photos = svc
	s.views.photoAuth = auth
}

// SetPhotoTags enables reusable photo tags in the gallery modal.
// Optional like the other setters; the editor hides without it.
func (s *Server) SetPhotoTags(store photoTagStore) {
	s.views.tagStore = store
}

// SetPhotoAlbums enables albums and the gallery sidebar.
// Optional like the other setters; nav and modal sections hide without it.
func (s *Server) SetPhotoAlbums(store photoAlbumStore) {
	s.views.albumStore = store
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

	v, err := newViews(files, contacts, calendars, mail, sessions, users)
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
	mux.HandleFunc("GET /login/sso", s.ssoLogin)
	mux.HandleFunc("GET /login/sso/callback", s.ssoCallback)
	mux.Handle("/static/", s.staticHandler())

	mux.HandleFunc("GET /{$}", s.page(s.views.homePage))
	mux.HandleFunc("GET /me", s.RequireAuth(s.me))
	mux.HandleFunc("POST /logout", s.RequireAuth(s.RequireCSRF(s.logout)))
	mux.HandleFunc("GET /profile", s.page(s.profilePage))
	mux.HandleFunc("POST /profile", s.RequireAuth(s.RequireCSRF(s.profileUpdate)))
	mux.HandleFunc("POST /profile/password", s.RequireAuth(s.RequireCSRF(s.profileChangePassword)))
	mux.HandleFunc("POST /profile/iphone-profile", s.RequireAuth(s.RequireCSRF(s.iphoneProfile)))
	mux.HandleFunc("GET /profile/iphone-profile/download", s.iphoneProfileDownload)
	mux.HandleFunc("POST /profile/app-passwords", s.RequireAuth(s.RequireCSRF(s.appPasswordCreate)))
	mux.HandleFunc("POST /profile/app-passwords/revoke", s.RequireAuth(s.RequireCSRF(s.appPasswordRevoke)))
	mux.HandleFunc("POST /admin/invites", s.RequireAuth(s.RequireCSRF(s.inviteCreate)))
	mux.HandleFunc("POST /admin/invites/revoke", s.RequireAuth(s.RequireCSRF(s.inviteRevoke)))
	mux.HandleFunc("DELETE /admin/users/{id}", s.RequireAuth(s.RequireCSRF(s.adminUserDelete)))
	mux.HandleFunc("GET /invite/accept", s.inviteAcceptPage)
	mux.HandleFunc("POST /invite/accept", s.inviteAccept)

	mux.HandleFunc("GET /drive", s.page(s.views.drivePage))
	mux.HandleFunc("GET /drive/download", s.RequireAuth(s.views.driveDownload))
	mux.HandleFunc("POST /drive/upload", s.RequireAuth(s.RequireCSRF(s.views.driveUpload)))
	mux.HandleFunc("POST /drive/mkdir", s.RequireAuth(s.RequireCSRF(s.views.driveMkdir)))
	mux.HandleFunc("POST /drive/delete", s.RequireAuth(s.RequireCSRF(s.views.driveDelete)))
	mux.HandleFunc("POST /drive/rename", s.RequireAuth(s.RequireCSRF(s.views.driveRename)))

	mux.HandleFunc("GET /gallery", s.page(s.views.galleryPage))
	mux.HandleFunc("GET /gallery/content", s.page(s.views.galleryContent))
	mux.HandleFunc("POST /gallery/tags", s.RequireAuth(s.RequireCSRF(s.views.photoTags)))
	mux.HandleFunc("POST /gallery/albums", s.RequireAuth(s.RequireCSRF(s.views.albumCreate)))
	mux.HandleFunc("POST /gallery/albums/delete", s.RequireAuth(s.RequireCSRF(s.views.albumDelete)))
	mux.HandleFunc("POST /gallery/albums/toggle", s.RequireAuth(s.RequireCSRF(s.views.albumToggle)))
	mux.HandleFunc("GET /gallery/file", s.RequireAuth(s.views.galleryFile))
	mux.HandleFunc("GET /gallery/preview", s.RequireAuth(s.views.galleryPreview))
	mux.HandleFunc("POST /gallery/delete", s.RequireAuth(s.RequireCSRF(s.views.galleryDelete)))
	mux.HandleFunc("POST /api/upload", s.views.photoUpload)

	mux.HandleFunc("GET /mail", s.page(s.views.mailPage))
	mux.HandleFunc("GET /mail/message/{id}", s.page(s.views.mailDetailPage))
	mux.HandleFunc("GET /mail/message/{id}/attachment/{idx}", s.page(s.views.mailAttachmentDownload))
	mux.HandleFunc("POST /mail/message/{id}/add-to-calendar", s.RequireAuth(s.RequireCSRF(s.views.mailAddToCalendar)))
	mux.HandleFunc("POST /mail/message/{id}/rsvp", s.RequireAuth(s.RequireCSRF(s.views.mailRSVP)))
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

	mux.HandleFunc("GET /notes", s.page(s.notesPage))
	mux.HandleFunc("GET /notes/live", s.RequireAuth(s.notesLiveSSE))
	mux.HandleFunc("POST /notes", s.RequireAuth(s.RequireCSRF(s.notesCreate)))
	mux.HandleFunc("GET /notes/{id}", s.RequireAuth(s.notesDetail))
	mux.HandleFunc("POST /notes/{id}", s.RequireAuth(s.RequireCSRF(s.notesUpdate)))
	mux.HandleFunc("POST /notes/{id}/delete", s.RequireAuth(s.RequireCSRF(s.notesDelete)))
	mux.HandleFunc("POST /notes/{id}/share", s.RequireAuth(s.RequireCSRF(s.notesShare)))
	mux.HandleFunc("POST /notes/{id}/toggle-pin", s.RequireAuth(s.RequireCSRF(s.notesTogglePin)))
	mux.HandleFunc("POST /notes/{id}/items", s.RequireAuth(s.RequireCSRF(s.notesAddItem)))
	mux.HandleFunc("POST /notes/{id}/items/{item_id}/toggle", s.RequireAuth(s.RequireCSRF(s.notesToggleItem)))
	mux.HandleFunc("POST /notes/{id}/items/{item_id}/delete", s.RequireAuth(s.RequireCSRF(s.notesDeleteItem)))
}

// loginViewData carries the login form context: an optional banner error and
// the SSO buttons rendered under the password form.
type loginViewData struct {
	Error        string
	SSOProviders []ssoProviderView
}

type ssoProviderView struct {
	Name string
	Href string
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.validSession(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	data := loginViewData{Error: r.URL.Query().Get("error")}
	if s.oidc != nil {
		data.SSOProviders = []ssoProviderView{{Name: s.oidc.idp.Name, Href: "/login/sso"}}
	}
	renderView(w, r, s.views.login, "login", data)
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
