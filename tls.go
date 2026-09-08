package main

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

type TLSConfig struct {
	CertFile string
	KeyFile  string
}

// certReloader serves the key pair from disk and picks up replacements.
//
// Certificates are renewed long before the process is restarted: cert-manager
// rewrites the mounted secret every couple of months, and a listener holding
// the pair it read at startup goes on presenting the superseded certificate
// until something happens to restart it, eventually serving an expired one.
// The HTTP ingress reloads on its own; these are the mail listeners, so they
// have to do it themselves.
type certReloader struct {
	certFile string
	keyFile  string

	// checkInterval bounds how often the files are stat'ed, so a busy
	// listener does not hit the filesystem on every handshake.
	checkInterval time.Duration

	mu          sync.RWMutex
	certificate *tls.Certificate
	modified    time.Time
	checked     time.Time
}

func newCertReloader(certFile, keyFile string) (*certReloader, error) {
	reloader := &certReloader{
		certFile:      certFile,
		keyFile:       keyFile,
		checkInterval: time.Minute,
	}
	if err := reloader.reload(); err != nil {
		return nil, err
	}
	return reloader, nil
}

// modTime reports the most recent modification across both files.
func (c *certReloader) modTime() (time.Time, error) {
	var latest time.Time
	for _, name := range []string{c.certFile, c.keyFile} {
		info, err := os.Stat(name)
		if err != nil {
			return time.Time{}, err
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest, nil
}

func (c *certReloader) reload() error {
	certificate, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return fmt.Errorf("load tls cert: %w", err)
	}
	modified, err := c.modTime()
	if err != nil {
		return fmt.Errorf("stat tls cert: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.certificate = &certificate
	c.modified = modified
	c.checked = time.Now()
	return nil
}

// GetCertificate satisfies tls.Config.GetCertificate.
func (c *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.RLock()
	certificate := c.certificate
	checked := c.checked
	modified := c.modified
	c.mu.RUnlock()

	if time.Since(checked) < c.checkInterval {
		return certificate, nil
	}

	c.mu.Lock()
	c.checked = time.Now()
	c.mu.Unlock()

	current, err := c.modTime()
	if err != nil {
		// Keep serving what we have; a missing file during an atomic
		// replacement must not fail the handshake.
		slog.Warn("tls: could not stat certificate", "error", err)
		return certificate, nil
	}
	if !current.After(modified) {
		return certificate, nil
	}

	if err := c.reload(); err != nil {
		slog.Error("tls: reload failed, serving previous certificate", "error", err)
		return certificate, nil
	}
	slog.Info("tls: certificate reloaded", "cert", c.certFile)

	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.certificate, nil
}

func LoadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		return nil, nil
	}

	reloader, err := newCertReloader(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		// Certificates is kept populated so callers can still tell that a
		// certificate is configured; GetCertificate takes precedence during
		// the handshake and is what picks up renewals.
		Certificates:   []tls.Certificate{*reloader.certificate},
		GetCertificate: reloader.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}, nil
}
