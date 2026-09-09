package main

import "os"

type config struct {
	httpAddr    string
	databaseURL string
	mailHost    string
	davHost     string
}

// mailHostname is the canonical hostname announced in SMTP banners and EHLO
// replies, used as the outbound HELO name and as the domain of generated
// Message-IDs. Receiving MTAs check that the HELO name is a resolvable FQDN,
// so this must not stay at the local-dev default in production; it is set from
// MAIL_HOST at startup.
var mailHostname = "mail.local"

func loadConfig() config {
	cfg := config{
		httpAddr:    getEnv("HTTP_ADDR", ":8080"),
		databaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/workspace?sslmode=disable"),
		mailHost:    getEnv("MAIL_HOST", ""),
		davHost:     getEnv("DAV_HOST", ""),
	}

	if cfg.mailHost != "" {
		mailHostname = cfg.mailHost
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	return value
}
