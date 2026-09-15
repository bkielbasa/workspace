package main

import (
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/bklimczak/workspace/internal/appleprofile"
	"github.com/bklimczak/workspace/internal/obs"
)

// Apple devices have no mail auto-configuration protocol: iOS Mail queries
// neither Autodiscover nor the Mozilla autoconfig document, so nothing served
// over HTTP is ever looked for. A configuration profile is the mechanism Apple
// does provide — the user opens this URL in Safari and installs the result,
// which writes the account settings for them.
//
// The profile is unsigned, so iOS labels it "Unverified" during installation.
// Signing needs a certificate issued for profile signing; the settings it
// carries are public either way. Passwords are embedded only as per-device
// app passwords, never the master password.

func (d *discovery) appleProfile(w http.ResponseWriter, r *http.Request) {
	address := requestedAddress(r)
	if address == "" {
		http.Error(w, "an email address is required, for example ?email=you@example.com", http.StatusBadRequest)
		return
	}

	profile, err := appleprofile.Build(address, d.mailHost, d.davHost, profileHost(r), "")
	if err != nil {
		http.Error(w, "a full email address is required, for example ?email=you@example.com", http.StatusBadRequest)
		return
	}

	_, domain, _ := strings.Cut(address, "@")
	w.Header().Set("Content-Type", appleprofile.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+domain+`-mail.mobileconfig"`)
	if _, err := w.Write(profile); err != nil {
		obs.Log(r.Context(), slog.LevelError, "discovery: writing apple profile failed", "error", err)
	}
}

// profileHost returns the host serving the profile (and the web UI),
// without any port. The profile link on the account page is relative, so
// the request host is the host the device must keep using.
func profileHost(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return "localhost"
	}
	if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
		return h
	}
	return host
}
