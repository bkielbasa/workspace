package main

import (
	"os"
	"strconv"
	"strings"
)

type config struct {
	httpAddr      string
	databaseURL   string
	mailHost      string
	davHost       string
	primaryDomain string
	cookieSecure  bool
	filesDataDir  string
	filesQuota    int64
	filesMaxFile  int64
	// Photos live in a separate tree so libraries never mix with files.
	photosDataDir string
	smbPasswdFile string
}

// mailHostname is the canonical hostname announced in SMTP banners and EHLO
// replies, used as the outbound HELO name and as the domain of generated
// Message-IDs. Receiving MTAs check that the HELO name is a resolvable FQDN,
// so this must not stay at the local-dev default in production; it is set from
// MAIL_HOST at startup.
var mailHostname = "mail.local"

func loadConfig() config {
	primaryDomain := strings.ToLower(strings.TrimSpace(getEnv("PRIMARY_DOMAIN", "cloudlift.run")))
	if primaryDomain == "" {
		primaryDomain = "cloudlift.run"
	}

	cfg := config{
		httpAddr:      getEnv("HTTP_ADDR", ":8080"),
		databaseURL:   getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/workspace?sslmode=disable"),
		mailHost:      getEnv("MAIL_HOST", ""),
		davHost:       getEnv("DAV_HOST", ""),
		primaryDomain: primaryDomain,
		cookieSecure:  envBool("COOKIE_SECURE", false),
		filesDataDir:  getEnv("FILES_DATA_DIR", "./data/files"),
		filesQuota:    envBytes("FILES_QUOTA_BYTES", 10<<30),
		filesMaxFile:  envBytes("FILES_MAX_FILE_BYTES", 1<<30),
		photosDataDir: getEnv("PHOTOS_DATA_DIR", "./data/photos"),
		// Empty disables Samba credential sync (dev setups without the volume).
		smbPasswdFile: getEnv("SMB_PASSWD_FILE", "/data/samba/smbpasswd"),
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

func envBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}

	return parsed
}

func envBytes(key string, defaultValue int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return defaultValue
	}

	return parsed
}
