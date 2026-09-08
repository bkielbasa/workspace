package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	cfg := loadConfig()

	initLogger()

	shutdown := initTracer(context.Background())
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

	log.Printf("starting SMTP on :2525")
	smtp := NewSMTPServer(":2525", delivery, users)
	go func() {
		if err := smtp.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

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

	// Autoconfig for email + CardDAV + CalDAV
	mux.HandleFunc("/.well-known/autoconfig/mail/config-v1.1.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		host := r.Host
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<clientConfig version="1.1">
  <emailProvider id="local">
    <domain>%s</domain>

    <incomingServer type="imap">
      <hostname>%s</hostname>
      <port>993</port>
      <socketType>SSL</socketType>
      <authentication>password-cleartext</authentication>
      <username>%%EMAILADDRESS%%</username>
    </incomingServer>

    <outgoingServer type="smtp">
      <hostname>%s</hostname>
      <port>465</port>
      <socketType>SSL</socketType>
      <authentication>password-cleartext</authentication>
      <username>%%EMAILADDRESS%%</username>
    </outgoingServer>

    <addressBook type="carddav">
      <url>https://%s/dav/</url>
    </addressBook>

    <calendar type="caldav">
      <url>https://%s/cal/</url>
    </calendar>

  </emailProvider>
</clientConfig>`, host, host, host, host, host)
	})

	// Microsoft Outlook / iOS autodiscover. The client POSTs (or GETs)
	// /autodiscover/autodiscover.xml and expects a settings response.
	mux.HandleFunc("/autodiscover/autodiscover.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")

		email := r.URL.Query().Get("Email")
		if email == "" {
			// Outlook POSTs an XML body containing <EMailAddress>...</EMailAddress>
			body, _ := io.ReadAll(io.LimitReader(r.Body, 16*1024))
			if i := strings.Index(string(body), "<EMailAddress>"); i >= 0 {
				rest := string(body)[i+len("<EMailAddress>"):]
				if j := strings.Index(rest, "</EMailAddress>"); j >= 0 {
					email = strings.TrimSpace(rest[:j])
				}
			}
		}

		host := r.Host
		// autodiscover.cloudlift.run / autoconfig.cloudlift.run -> mail.cloudlift.run
		for _, prefix := range []string{"autodiscover.", "autoconfig.", "www."} {
			if strings.HasPrefix(host, prefix) {
				host = strings.TrimPrefix(host, prefix)
				break
			}
		}
		if host == "mail.local" {
			host = "mail.cloudlift.run"
		}

		login := email
		if login == "" {
			login = "@" + host
		}

		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<Autodiscover xmlns="http://schemas.microsoft.com/exchange/2010/autodiscover">
  <Response xmlns="http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a">
    <Account>
      <AccountType>email</AccountType>
      <Action>settings</Action>
      <Protocol>
        <Type>IMAP</Type>
        <Server>%s</Server>
        <Port>993</Port>
        <DomainRequired>off</DomainRequired>
        <LoginName>%s</LoginName>
        <SPA>off</SPA>
        <SSL>on</SSL>
        <AuthRequired>on</AuthRequired>
      </Protocol>
      <Protocol>
        <Type>SMTP</Type>
        <Server>%s</Server>
        <Port>465</Port>
        <DomainRequired>off</DomainRequired>
        <LoginName>%s</LoginName>
        <SPA>off</SPA>
        <SSL>on</SSL>
        <AuthRequired>on</AuthRequired>
      </Protocol>
    </Account>
  </Response>
</Autodiscover>`, host, login, host, login)
	})

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
