package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// listToken derives a collection tag from member ETags so clients that cache
// getctag/sync-token re-sync exactly when the collection changes.
func listToken(etags []string) string {
	h := sha256.New()
	for _, e := range etags {
		h.Write([]byte(e))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:20]
}

func main() {
	cfg := loadConfig()

	initLogger()

	shutdown := initTelemetry(context.Background())
	defer shutdown()

	initMetrics()

	log.Printf("connecting to database...")

	var db *sql.DB
	var err error

	for i := 1; i <= 10; i++ {
		db, err = OpenDB(cfg.databaseURL)
		if err == nil {
			break
		}
		log.Printf("db connection failed (attempt %d/10): %v", i, err)
	}

	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	mailboxes := NewMailboxes(db)

	domains := NewDomains(db)

	aliases := NewAliases(db, domains)

	// Session store backs the web login flow. It is wired into the user store
	// so credential changes and account disable can revoke active sessions.
	sessions := NewSessions(db)

	users := NewUsers(db, mailboxes, domains, sessions)

	auth := NewAuth(sessions, users, cfg.cookieSecure)

	// Periodically purge expired sessions so stale rows do not accumulate.
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if n, err := sessions.CleanupExpired(context.Background()); err != nil {
				log.Printf("session cleanup: %v", err)
			} else if n > 0 {
				log.Printf("session cleanup: removed %d expired sessions", n)
			}
		}
	}()

	mail := NewMail(db)

	log.Printf("seeding data...")
	RunSeed(context.Background(), users, mail, mailboxes)

	contacts := NewContacts(db)

	carddav := &CardDAV{contacts: contacts, users: users}
	cal := NewCalendar(db)
	caldav := &CalDAV{cal: cal, users: users}

	view := newViews(contacts, cal)

	outbox := NewOutbox(db)
	threads := NewThreads(db)

	delivery := NewDelivery(users, mailboxes, mail, outbox, threads, aliases)

	certFile := getEnv("TLS_CERT_FILE", "")
	keyFile := getEnv("TLS_KEY_FILE", "")
	var tlsCfg *tls.Config
	if certFile != "" && keyFile != "" {
		var err error
		tlsCfg, err = LoadTLSConfig(certFile, keyFile)
		if err != nil {
			log.Printf("TLS disabled (failed to load certs): %v", err)
			tlsCfg = nil
		}
	} else {
		log.Printf("TLS disabled (no cert/key provided)")
	}

	log.Printf("starting SMTP on :2526")
	smtp := NewSMTPServer(":2526", delivery, users, tlsCfg)
	go func() {
		if err := smtp.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	// Submission port. Mail clients default to 587 with STARTTLS when they are
	// configured by hand, so this listener is what most setup wizards probe;
	// 465 alone is not enough.
	log.Printf("starting SMTP submission on :2588")
	submission := NewSMTPServer(":2588", delivery, users, tlsCfg)
	go func() {
		if err := submission.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	if tlsCfg != nil {
		log.Printf("starting SMTPS on :2466")
		smtps := NewSMTPTLSServer(":2466", delivery, users, tlsCfg)
		go func() {
			if err := smtps.ListenAndServe(); err != nil {
				log.Fatal(err)
			}
		}()
	}

	// worker := NewWorker(outbox, delivery, initDKIM())
	// log.Printf("starting worker...")
	// worker.Start()

	log.Printf("starting IMAP on :1144")
	imap := NewIMAPServer(":1144", users, mail, mailboxes)
	go func() {
		if err := imap.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	if tlsCfg != nil {
		log.Printf("starting IMAPS on :1994")
		imaps := NewIMAPTLSServer(":1994", users, mail, mailboxes, tlsCfg)
		go func() {
			if err := imaps.ListenAndServe(); err != nil {
				log.Fatal(err)
			}
		}()
	}

	mux := http.NewServeMux()

	// Apple client discovery (required for auto-config of CardDAV/CalDAV)
	mux.HandleFunc("/.well-known/carddav", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dav/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/.well-known/caldav", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/cal/", http.StatusMovedPermanently)
	})

	// Mail client auto-configuration (Autodiscover + Mozilla autoconfig).
	// CardDAV and CalDAV are reached over HTTPS, which the mail host does not
	// serve on the local network; DAV_HOST names the host that does.
	davHost := cfg.davHost
	if davHost == "" {
		davHost = mailHostname
	}
	(&discovery{mailHost: mailHostname, davHost: davHost, domains: domains}).register(mux)

	// domains
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// ----- web session auth -----
	// Public: sign-in page, create-session, and static assets.
	mux.HandleFunc("GET /login", auth.LoginPageHandler(view))
	mux.HandleFunc("POST /login", auth.LoginHandler)

	mux.Handle("/static/", staticHandler())

	// Authenticated: app, session revocation, and current-user.
	mux.HandleFunc("GET /{$}", auth.page(view.homePage))
	mux.HandleFunc("GET /me", auth.requireAuth(auth.MeHandler))
	mux.HandleFunc("POST /logout", auth.requireAuth(auth.requireCSRF(auth.LogoutHandler)))

	// Web pages (server-rendered, HTMX fragments)
	mux.HandleFunc("GET /contacts", auth.page(view.contactsPage))
	mux.HandleFunc("GET /contacts/rows", auth.requireAuth(view.contactsRows))
	mux.HandleFunc("POST /contacts/rows", auth.requireAuth(auth.requireCSRF(view.contactsAdd)))
	mux.HandleFunc("DELETE /contacts/{id}", auth.requireAuth(auth.requireCSRF(view.contactsDelete)))
	mux.HandleFunc("POST /contacts/{id}/emails", auth.requireAuth(auth.requireCSRF(view.contactsAddField("emails"))))
	mux.HandleFunc("POST /contacts/{id}/phones", auth.requireAuth(auth.requireCSRF(view.contactsAddField("phones"))))
	mux.HandleFunc("DELETE /contacts/{id}/emails/{index}", auth.requireAuth(auth.requireCSRF(view.contactsRemoveField("emails"))))
	mux.HandleFunc("DELETE /contacts/{id}/phones/{index}", auth.requireAuth(auth.requireCSRF(view.contactsRemoveField("phones"))))

	mux.HandleFunc("GET /calendars", auth.page(view.calendarsPage))
	mux.HandleFunc("GET /calendars/rows", auth.requireAuth(view.calendarsRows))
	mux.HandleFunc("POST /calendars/rows", auth.requireAuth(auth.requireCSRF(view.calendarsAdd)))
	mux.HandleFunc("DELETE /calendars/{id}", auth.requireAuth(auth.requireCSRF(view.calendarsDelete)))

	// ----- domains (admin, session-only) -----
	mux.HandleFunc("POST /domains", auth.requireAuth(auth.requireCSRF(domains.CreateHandler)))
	mux.HandleFunc("GET /domains", auth.requireAuth(domains.ListHandler))
	mux.HandleFunc("GET /domains/{id}", auth.requireAuth(domains.GetHandler))
	mux.HandleFunc("DELETE /domains/{id}", auth.requireAuth(auth.requireCSRF(domains.DeleteHandler)))

	// aliases (scoped under domains)
	mux.HandleFunc("POST /domains/{domainID}/aliases", auth.requireAuth(auth.requireCSRF(aliases.CreateHandler)))
	mux.HandleFunc("GET /domains/{domainID}/aliases", auth.requireAuth(aliases.ListHandler))
	mux.HandleFunc("DELETE /domains/{domainID}/aliases/{id}", auth.requireAuth(auth.requireCSRF(aliases.DeleteHandler)))

	// users (scoped under domains for creation)
	mux.HandleFunc("POST /domains/{domainID}/users", auth.requireAuth(auth.requireCSRF(domains.CreateUserHandler(users))))
	mux.HandleFunc("GET /users", auth.requireAuth(users.ListHandler))
	mux.HandleFunc("GET /users/{id}", auth.requireAuth(users.GetHandler))
	mux.HandleFunc("PATCH /users/{id}", auth.requireAuth(auth.requireCSRF(users.UpdateHandler)))
	mux.HandleFunc("DELETE /users/{id}", auth.requireAuth(auth.requireCSRF(users.DeleteHandler)))
	mux.HandleFunc("POST /users/{id}/password", auth.requireAuth(auth.requireCSRF(users.ChangePasswordHandler)))

	// threads
	threadsHTTP := &ThreadsHTTP{threads: threads}
	mux.HandleFunc("GET /threads", auth.requireAuth(threadsHTTP.ListHandler))

	// CardDAV (very minimal)
	mux.Handle("/dav/", carddav)
	mux.Handle("/cal/", caldav)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		incHTTP(r.Context())
		mux.ServeHTTP(w, r)
	})

	otelHandler := otelhttp.NewHandler(securityHeaders(handler), "http")

	server := &http.Server{
		Addr:              cfg.httpAddr,
		Handler:           otelHandler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	log.Printf("http server listening on %s", cfg.httpAddr)

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
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
		log.Printf("dkim disabled: %v", err)
		return nil
	}

	key, err := LoadDKIMPrivateKey(data)
	if err != nil {
		log.Printf("dkim disabled: %v", err)
		return nil
	}

	log.Printf("dkim enabled for domain %s (selector=%s)", domain, selector)

	return &DKIM{
		Domain:   domain,
		Selector: selector,
		Private:  key,
	}
}
