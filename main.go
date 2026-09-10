package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/bklimczak/workspace/internal/caldav"
	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/carddav"
	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/format/vcard"
	"github.com/bklimczak/workspace/internal/httpapi"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/imap"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/bklimczak/workspace/internal/postgres"
	"github.com/bklimczak/workspace/internal/smtp"
	"github.com/bklimczak/workspace/internal/web"
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
	users := identity.NewUsers(postgres.NewUserRepository(db), sessions)
	messages := mail.NewMail(postgres.NewMessageRepository(db))
	threads := mail.NewThreads(postgres.NewThreadRepository(db))
	outbox := mail.NewOutbox(postgres.NewOutboxRepository(db))
	contactSvc := contacts.NewService(postgres.NewContactRepository(db), vcard.Encode)
	calendarSvc := calendar.NewService(postgres.NewCalendarRepository(db))
	delivery := mail.NewDelivery(users, mailboxes, messages, outbox, threads, aliases, mailHostname)
	searchRepo := postgres.NewSearchRepository(db)
	mailSvc := mail.NewService(mailboxes, messages, searchRepo, delivery, mailHostname)

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
	go mustListen(smtp.NewServer(":2526", mailHostname, delivery, users, mailboxes, messages, tlsCfg))
	go mustListen(smtp.NewServer(":2588", mailHostname, delivery, users, mailboxes, messages, tlsCfg))
	if tlsCfg != nil {
		go mustListen(smtp.NewTLSServer(":2466", mailHostname, delivery, users, mailboxes, messages, tlsCfg))
	}

	go mustListen(imap.NewServer(":1144", users, messages, mailboxes))
	if tlsCfg != nil {
		go mustListen(imap.NewTLSServer(":1994", users, messages, mailboxes, tlsCfg))
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

	mux.Handle("/dav/", carddav.New(contactSvc, users))
	mux.Handle("/cal/", caldav.New(calendarSvc, users))

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

type listener interface {
	ListenAndServe() error
}

func mustListen(s listener) {
	if err := s.ListenAndServe(); err != nil {
		obs.Fatal(context.Background(), "listener stopped", "error", err)
	}
}
