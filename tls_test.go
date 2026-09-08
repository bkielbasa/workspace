package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeKeyPair writes a self-signed certificate for host and returns its paths.
func writeKeyPair(t *testing.T, dir, host string) (string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certFile, keyFile
}

func commonName(t *testing.T, reloader *certReloader) string {
	t.Helper()

	certificate, err := reloader.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return parsed.Subject.CommonName
}

// A renewed certificate has to be served without restarting the process,
// otherwise the mail listeners keep presenting the superseded pair until they
// are serving an expired certificate.
func TestCertReloaderPicksUpRenewal(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeKeyPair(t, dir, "mail.example.test")

	reloader, err := newCertReloader(certFile, keyFile)
	if err != nil {
		t.Fatalf("newCertReloader: %v", err)
	}
	if got := commonName(t, reloader); got != "mail.example.test" {
		t.Fatalf("common name = %q", got)
	}

	// Renewal: same paths, new contents, later modification time.
	writeKeyPair(t, dir, "renewed.example.test")
	future := time.Now().Add(time.Hour)
	for _, name := range []string{certFile, keyFile} {
		if err := os.Chtimes(name, future, future); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	// Force the interval to elapse rather than waiting for it.
	reloader.mu.Lock()
	reloader.checked = time.Now().Add(-2 * reloader.checkInterval)
	reloader.mu.Unlock()

	if got := commonName(t, reloader); got != "renewed.example.test" {
		t.Errorf("common name after renewal = %q, want renewed.example.test", got)
	}
}

func TestCertReloaderServesPreviousOnFailure(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeKeyPair(t, dir, "mail.example.test")

	reloader, err := newCertReloader(certFile, keyFile)
	if err != nil {
		t.Fatalf("newCertReloader: %v", err)
	}

	// A truncated file, as observed mid-write, must not fail handshakes.
	if err := os.WriteFile(certFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(certFile, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	reloader.mu.Lock()
	reloader.checked = time.Now().Add(-2 * reloader.checkInterval)
	reloader.mu.Unlock()

	if got := commonName(t, reloader); got != "mail.example.test" {
		t.Errorf("common name = %q, want the previous certificate", got)
	}
}

// STARTTLS availability is decided by inspecting Certificates, so it has to
// stay populated alongside GetCertificate.
func TestLoadTLSConfigReportsConfiguredCertificate(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeKeyPair(t, dir, "mail.example.test")

	config, err := LoadTLSConfig(certFile, keyFile)
	if err != nil {
		t.Fatalf("LoadTLSConfig: %v", err)
	}
	if len(config.Certificates) == 0 {
		t.Error("Certificates is empty, STARTTLS would report TLS unavailable")
	}
	if config.GetCertificate == nil {
		t.Error("GetCertificate is not set, renewals would be missed")
	}
}

func TestLoadTLSConfigWithoutPathsIsDisabled(t *testing.T) {
	config, err := LoadTLSConfig("", "")
	if err != nil {
		t.Fatalf("LoadTLSConfig: %v", err)
	}
	if config != nil {
		t.Error("expected TLS to be disabled without paths")
	}
}
