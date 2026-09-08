package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"log"
	"net/http"
	"os"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

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

	mailboxes := &Mailboxes{
		db: db,
	}

	domains := &Domains{db: db}

	aliases := &Aliases{
		db:      db,
		domains: domains,
	}

	users := &Users{
		db:        db,
		mailboxes: mailboxes,
		domains:   domains,
	}

	mail := &Mail{
		db: db,
	}

	log.Printf("seeding data...")
	RunSeed(context.Background(), users, mail, mailboxes)

	contacts := &Contacts{db: db}
	contactsHTTP := &ContactsHTTP{
		contacts: contacts,
		users:    users,
	}

	carddav := &CardDAV{contacts: contacts}
	cal := &Calendar{db: db}
	caldav := &CalDAV{cal: cal, users: users}

	delivery := &Delivery{
		users:     users,
		mailboxes: mailboxes,
		mail:      mail,
		outbox:    &Outbox{db: db},
		threads:   &Threads{db: db},
		aliases:   aliases,
	}

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

	log.Printf("starting SMTP on :2525")
	smtp := NewSMTPServer(":2525", delivery, users, tlsCfg)
	go func() {
		if err := smtp.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	// Submission port. Mail clients default to 587 with STARTTLS when they are
	// configured by hand, so this listener is what most setup wizards probe;
	// 465 alone is not enough.
	log.Printf("starting SMTP submission on :2587")
	submission := NewSMTPServer(":2587", delivery, users, tlsCfg)
	go func() {
		if err := submission.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	if tlsCfg != nil {
		log.Printf("starting SMTPS on :2465")
		smtps := NewSMTPTLSServer(":2465", delivery, users, tlsCfg)
		go func() {
			if err := smtps.ListenAndServe(); err != nil {
				log.Fatal(err)
			}
		}()
	}

	worker := &Worker{
		outbox:   delivery.outbox,
		delivery: delivery,
		dkim:     initDKIM(),
	}
	log.Printf("starting worker...")
	worker.Start()

	log.Printf("starting IMAP on :1143")
	imap := NewIMAPServer(":1143", users, mail, mailboxes)
	go func() {
		if err := imap.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	if tlsCfg != nil {
		log.Printf("starting IMAPS on :1993")
		imaps := NewIMAPTLSServer(":1993", users, mail, mailboxes, tlsCfg)
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
	(&discovery{mailHost: mailHostname, domains: domains}).register(mux)

	// domains
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// domains
	mux.HandleFunc("POST /domains", domains.CreateHandler)
	mux.HandleFunc("GET /domains", domains.ListHandler)
	mux.HandleFunc("GET /domains/{id}", domains.GetHandler)
	mux.HandleFunc("DELETE /domains/{id}", domains.DeleteHandler)

	// aliases (scoped under domains)
	mux.HandleFunc("POST /domains/{domainID}/aliases", aliases.CreateHandler)
	mux.HandleFunc("GET /domains/{domainID}/aliases", aliases.ListHandler)
	mux.HandleFunc("DELETE /domains/{domainID}/aliases/{id}", aliases.DeleteHandler)

	// users (scoped under domains for creation)
	mux.HandleFunc("POST /domains/{domainID}/users", domains.CreateUserHandler(users))
	mux.HandleFunc("GET /users", users.ListHandler)
	mux.HandleFunc("GET /users/{id}", users.GetHandler)
	mux.HandleFunc("PATCH /users/{id}", users.UpdateHandler)
	mux.HandleFunc("DELETE /users/{id}", users.DeleteHandler)
	mux.HandleFunc("POST /users/{id}/password", users.ChangePasswordHandler)

	// contacts
	mux.HandleFunc("GET /contacts", contactsHTTP.ListHandler)
	mux.HandleFunc("POST /contacts", contactsHTTP.UpsertHandler)
	mux.HandleFunc("DELETE /contacts", contactsHTTP.DeleteHandler)

	// threads
	threadsHTTP := &ThreadsHTTP{threads: &Threads{db: db}}
	mux.HandleFunc("GET /threads", threadsHTTP.ListHandler)

	// CardDAV (very minimal)
	mux.Handle("/dav/", carddav)
	mux.Handle("/cal/", caldav)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		incHTTP(r.Context())
		mux.ServeHTTP(w, r)
	})

	otelHandler := otelhttp.NewHandler(handler, "http")

	server := &http.Server{
		Addr:    cfg.httpAddr,
		Handler: otelHandler,
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
