package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/caldav"
	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/carddav"
	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/files"
	"github.com/bklimczak/workspace/internal/format/vcard"
	"github.com/bklimczak/workspace/internal/httpapi"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/imap"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/notes"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/bklimczak/workspace/internal/postgres"
	"github.com/bklimczak/workspace/internal/smb"
	"github.com/bklimczak/workspace/internal/smtp"
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	cfg := loadConfig()

	obs.Init()
	ctx := context.Background()
	shutdown := initTelemetry(ctx)
	defer shutdown()
	obs.InitMetrics()

	obs.Log(ctx, slog.LevelInfo, "connecting to database")

	var db *sql.DB
	var err error
	for i := 1; i <= 10; i++ {
		db, err = postgres.Open(cfg.databaseURL)
		if err == nil {
			break
		}
		obs.Log(ctx, slog.LevelWarn, "database connection failed", "attempt", i, "max_attempts", 10, "error", err)
	}
	if err != nil {
		obs.Fatal(ctx, "database unavailable", "error", err)
	}
	defer db.Close()

	mailboxes := mail.NewMailboxes(postgres.NewMailboxRepository(db))
	domains := identity.NewDomains(postgres.NewDomainRepository(db))
	aliases := identity.NewAliases(postgres.NewAliasRepository(db), domains)
	sessions := identity.NewSessions(postgres.NewSessionRepository(db))
	// Samba credentials mirror the account lifecycle so the file server
	// authenticates the same passwords. Empty path disables the sync.
	var pwSync []identity.PasswordSync
	if strings.TrimSpace(cfg.smbPasswdFile) != "" {
		if mgr, err := smb.NewManager(cfg.smbPasswdFile); err != nil {
			obs.Fatal(ctx, "samba user database unavailable", "error", err)
		} else {
			pwSync = append(pwSync, mgr)
		}
	}
	users := identity.NewUsers(postgres.NewUserRepository(db), sessions, pwSync...)
	appPasswords := identity.NewAppPasswords(postgres.NewAppPasswordRepository(db), users)
	// Device protocols accept master passwords and per-device app passwords.
	// The web UI keeps master-only login (see web.New below).
	deviceAuth := identity.NewDeviceAuth(users, appPasswords)
	messages := mail.NewMail(postgres.NewMessageRepository(db))
	threads := mail.NewThreads(postgres.NewThreadRepository(db))
	outbox := mail.NewOutbox(postgres.NewOutboxRepository(db))
	contactSvc := contacts.NewService(postgres.NewContactRepository(db), vcard.Encode)
	calendarSvc := calendar.NewService(postgres.NewCalendarRepository(db))
	notesRepo := postgres.NewNotesRepository(db)
	notesSvc := notes.NewService(notesRepo, notes.NewBroker())
	fileStore, err := files.NewStore(cfg.filesDataDir, cfg.filesQuota, cfg.filesMaxFile)
	if err != nil {
		obs.Fatal(ctx, "files store unavailable", "error", err)
	}
	// Photos live in a separate tree (same quotas) so libraries never mix.
	photoStore, err := files.NewStore(cfg.photosDataDir, cfg.filesQuota, cfg.filesMaxFile)
	if err != nil {
		obs.Fatal(ctx, "photos store unavailable", "error", err)
	}
	delivery := mail.NewDelivery(users, mailboxes, messages, outbox, threads, aliases, mailHostname)
	searchRepo := postgres.NewSearchRepository(db)
	mailSvc := mail.NewService(mailboxes, messages, searchRepo, delivery, mailHostname)

	dkim := initDKIM()
	var dkimSigner mail.DKIMSigner
	if dkim != nil {
		dkimSigner = dkim
	}
	worker := mail.NewWorker(outbox, delivery, dkimSigner, mailHostname)
	worker.Start(ctx)
	obs.Log(ctx, slog.LevelInfo, "background outbox worker started")

	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if n, err := sessions.CleanupExpired(context.Background()); err != nil {
				obs.Log(context.Background(), slog.LevelError, "session cleanup failed", "error", err)
			} else if n > 0 {
				obs.Log(context.Background(), slog.LevelInfo, "session cleanup removed expired sessions", "count", n)
			}
		}
	}()

	certFile := getEnv("TLS_CERT_FILE", "")
	keyFile := getEnv("TLS_KEY_FILE", "")
	var tlsCfg *tls.Config
	if certFile != "" && keyFile != "" {
		var loadErr error
		tlsCfg, loadErr = LoadTLSConfig(certFile, keyFile)
		if loadErr != nil {
			obs.Log(ctx, slog.LevelWarn, "TLS disabled: failed to load certs", "error", loadErr)
			tlsCfg = nil
		}
	} else {
		obs.Log(ctx, slog.LevelInfo, "TLS disabled: no cert/key provided")
	}

	// Each server logs its own listening address once the socket is bound.
	go mustListen(smtp.NewServer(":2525", mailHostname, delivery, deviceAuth, mailboxes, messages, tlsCfg))
	go mustListen(smtp.NewServer(":2587", mailHostname, delivery, deviceAuth, mailboxes, messages, tlsCfg))
	if tlsCfg != nil {
		go mustListen(smtp.NewTLSServer(":2465", mailHostname, delivery, deviceAuth, mailboxes, messages, tlsCfg))
	}

	imapCleartext, imapTLS, imapNotesBridge := wireIMAPServers(deviceAuth, messages, mailboxes, notesSvc, tlsCfg)
	go imapNotesBridge.StartEventListener(ctx, func(userID uuid.UUID) (*identity.User, error) {
		return users.GetByID(ctx, userID)
	})
	go mustListen(imapCleartext)
	if imapTLS != nil {
		go mustListen(imapTLS)
	}

	webUI, err := web.New(webFS, contactSvc, calendarSvc, mailSvc, sessions, users, cfg.cookieSecure)
	if err != nil {
		obs.Fatal(ctx, "web UI initialization failed", "error", err)
	}
	api := httpapi.NewServer(users, domains, aliases, threads)

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/carddav", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dav/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/.well-known/caldav", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/cal/", http.StatusMovedPermanently)
	})

	davHost := cfg.davHost
	if davHost == "" {
		davHost = mailHostname
	}
	webUI.SetDeviceSetup(appPasswords, mailHostname, davHost)
	webUI.SetFiles(fileStore)
	webUI.SetPhotos(photoStore, deviceAuth)
	webUI.SetPhotoTags(postgres.NewPhotoTagRepository(db))
	webUI.SetPhotoAlbums(postgres.NewPhotoAlbumRepository(db))
	webUI.SetInvites(identity.NewInvites(postgres.NewInviteRepository(db), users))
	webUI.SetNotes(notesSvc)
	configureSSO(webUI, users, db)
	(&discovery{mailHost: mailHostname, davHost: davHost, domains: domains}).register(mux)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	webUI.RegisterRoutes(mux)

	mux.HandleFunc("POST /domains", webUI.RequireAuth(webUI.RequireCSRF(api.Domains.CreateHandler)))
	mux.HandleFunc("GET /domains", webUI.RequireAuth(api.Domains.ListHandler))
	mux.HandleFunc("GET /domains/{id}", webUI.RequireAuth(api.Domains.GetHandler))
	mux.HandleFunc("DELETE /domains/{id}", webUI.RequireAuth(webUI.RequireCSRF(api.Domains.DeleteHandler)))
	mux.HandleFunc("POST /domains/{domainID}/aliases", webUI.RequireAuth(webUI.RequireCSRF(api.Aliases.CreateHandler)))
	mux.HandleFunc("GET /domains/{domainID}/aliases", webUI.RequireAuth(api.Aliases.ListHandler))
	mux.HandleFunc("DELETE /domains/{domainID}/aliases/{id}", webUI.RequireAuth(webUI.RequireCSRF(api.Aliases.DeleteHandler)))
	mux.HandleFunc("POST /domains/{domainID}/users", webUI.RequireAuth(webUI.RequireCSRF(api.Domains.CreateUserHandler)))
	mux.HandleFunc("GET /users", webUI.RequireAuth(api.Users.ListHandler))
	mux.HandleFunc("GET /users/{id}", webUI.RequireAuth(api.Users.GetHandler))
	mux.HandleFunc("PATCH /users/{id}", webUI.RequireAuth(webUI.RequireCSRF(api.Users.UpdateHandler)))
	mux.HandleFunc("DELETE /users/{id}", webUI.RequireAuth(webUI.RequireCSRF(api.Users.DeleteHandler)))
	mux.HandleFunc("POST /users/{id}/password", webUI.RequireAuth(webUI.RequireCSRF(api.Users.ChangePasswordHandler)))
	mux.HandleFunc("GET /threads", webUI.RequireAuth(api.Threads.ListHandler))

	mux.Handle("/dav/", carddav.New(contactSvc, deviceAuth))
	mux.Handle("/cal/", caldav.NewWithTasks(calendarSvc, notesSvc, deviceAuth))
	// Both the subtree and the exact root: some clients never follow
	// the mux's trailing-slash redirect on PROPFIND.
	filesHandler := files.New(fileStore, deviceAuth)
	mux.Handle("/files/", filesHandler)
	mux.Handle("/files", filesHandler)
	// Photos are a separate tree with their own WebDAV mount.
	photosHandler := files.NewMounted(photoStore, deviceAuth, "/photos/")
	mux.Handle("/photos/", photosHandler)
	mux.Handle("/photos", photosHandler)
	// The DAV hostname doubles as a files endpoint: clients pointed at the
	// bare host land on their tree root instead of a 404.
	filesRoot := files.NewMounted(fileStore, deviceAuth, "/")
	for _, method := range []string{"PROPFIND", "OPTIONS", "MKCOL", "PUT", "DELETE", "MOVE", "COPY", "HEAD", "LOCK", "UNLOCK", "PROPPATCH"} {
		mux.Handle(method+" /{$}", filesRoot)
	}

	// Prometheus scrape endpoint for the prometheus.io/scrape
	// ServiceMonitor (same OTel counters as the OTLP pipeline).
	mux.Handle("/metrics", promhttp.Handler())

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		obs.HTTPRequest(r.Context())
		mux.ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:              cfg.httpAddr,
		Handler:           otelhttp.NewHandler(web.SecurityHeaders(handler), "http"),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	obs.Log(ctx, slog.LevelInfo, "http listening", "addr", cfg.httpAddr)
	if err := server.ListenAndServe(); err != nil {
		obs.Fatal(ctx, "http server stopped", "error", err)
	}
}

type imapMessageStore interface {
	imap.MessageStore
	mail.MessageRepository
}

type imapMailboxStore interface {
	imap.MailboxStore
	mail.MailboxRepository
}

func wireIMAPServers(deviceAuth imap.Authenticator, messages imapMessageStore, mailboxes imapMailboxStore, notesSvc *notes.Service, tlsCfg *tls.Config) (*imap.Server, *imap.Server, *notes.IMAPBridge) {
	var bridge *notes.IMAPBridge
	if notesSvc != nil && messages != nil && mailboxes != nil {
		bridge = notes.NewIMAPBridge(notesSvc, messages, mailboxes)
	}

	cleartext := imap.NewServer(":1143", deviceAuth, messages, mailboxes)
	if bridge != nil {
		cleartext.SetNotesBridge(bridge)
	}

	var tlsServer *imap.Server
	if tlsCfg != nil {
		tlsServer = imap.NewTLSServer(":1993", deviceAuth, messages, mailboxes, tlsCfg)
		if bridge != nil {
			tlsServer.SetNotesBridge(bridge)
		}
	}
	return cleartext, tlsServer, bridge
}

type listener interface {
	ListenAndServe() error
}

// configureSSO enables OIDC sign-in when OIDC_ISSUER is set. It fails hard on
// malformed config: an operator who intends SSO wants to know immediately.
// Without OIDC_ISSUER the app keeps password-only login untouched.
func configureSSO(webUI *web.Server, users *identity.Users, db *sql.DB) {
	issuer := getEnv("OIDC_ISSUER", "")
	if issuer == "" {
		return
	}
	redirectURL := getEnv("OIDC_REDIRECT_URL", "")
	if redirectURL == "" {
		redirectURL = "https://" + mailHostname + "/login/sso/callback"
	}
	var allowedDomains []string
	if raw := getEnv("OIDC_ALLOWED_DOMAINS", ""); raw != "" {
		for _, d := range strings.Split(raw, ",") {
			if d = strings.TrimSpace(d); d != "" {
				allowedDomains = append(allowedDomains, d)
			}
		}
	}
	provider := web.OIDCProvider{
		Name:                 getEnv("OIDC_PROVIDER_NAME", "Single Sign-On"),
		Issuer:               issuer,
		ClientID:             getEnv("OIDC_CLIENT_ID", ""),
		ClientSecret:         getEnv("OIDC_CLIENT_SECRET", ""),
		RedirectURL:          redirectURL,
		Scopes:               strings.Fields(getEnv("OIDC_SCOPES", "openid profile email")),
		EmailClaim:           getEnv("OIDC_EMAIL_CLAIM", "email"),
		NameClaim:            getEnv("OIDC_NAME_CLAIM", "name"),
		AllowedDomains:       allowedDomains,
		RequireEmailVerified: envBool("OIDC_REQUIRE_EMAIL_VERIFIED", true),
		AutoCreate:           envBool("OIDC_AUTO_CREATE", true),
	}
	sso := identity.NewSSO(postgres.NewSSORepository(db), users)
	if err := webUI.SetOIDC(provider, sso); err != nil {
		obs.Fatal(context.Background(), "sso configuration failed", "error", err)
	}
}

func mustListen(s listener) {
	if err := s.ListenAndServe(); err != nil {
		obs.Fatal(context.Background(), "listener stopped", "error", err)
	}
}

func initDKIM() *DKIM {
	domain := getEnv("DKIM_DOMAIN", "")
	selector := getEnv("DKIM_SELECTOR", "default")
	keyPath := getEnv("DKIM_PRIVATE_KEY_FILE", "")

	if domain == "" || keyPath == "" {
		return nil
	}

	data, err := os.ReadFile(keyPath)
	if err != nil {
		obs.Log(context.Background(), slog.LevelWarn, "dkim disabled: failed to read key file", "error", err)
		return nil
	}

	key, err := LoadDKIMPrivateKey(data)
	if err != nil {
		obs.Log(context.Background(), slog.LevelWarn, "dkim disabled: invalid key", "error", err)
		return nil
	}

	obs.Log(context.Background(), slog.LevelInfo, "dkim enabled", "domain", domain, "selector", selector)

	return &DKIM{
		Domain:   domain,
		Selector: selector,
		Private:  key,
	}
}
