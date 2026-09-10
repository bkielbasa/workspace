package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
)

// Apple devices have no mail auto-configuration protocol: iOS Mail queries
// neither Autodiscover nor the Mozilla autoconfig document, so nothing served
// over HTTP is ever looked for. A configuration profile is the mechanism Apple
// does provide — the user opens this URL in Safari and installs the result,
// which writes the account settings for them.
//
// The profile is unsigned, so iOS labels it "Unverified" during installation.
// Signing needs a certificate issued for profile signing; the settings it
// carries are public either way, and no password is included.
const appleProfileContentType = "application/x-apple-aspen-config"

// appleProfileNamespace keeps the identifiers for an address stable, so
// installing again replaces the account instead of adding a duplicate.
var appleProfileNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("workspace-apple-mail-profile"))

func (d *discovery) appleProfile(w http.ResponseWriter, r *http.Request) {
	address := requestedAddress(r)
	if address == "" {
		http.Error(w, "an email address is required, for example ?email=you@example.com", http.StatusBadRequest)
		return
	}

	_, domain, ok := strings.Cut(address, "@")
	if !ok || domain == "" {
		http.Error(w, "a full email address is required, for example ?email=you@example.com", http.StatusBadRequest)
		return
	}

	profileUUID := uuid.NewSHA1(appleProfileNamespace, []byte(address))
	accountUUID := uuid.NewSHA1(appleProfileNamespace, []byte("account:"+address))
	cardDAVUUID := uuid.NewSHA1(appleProfileNamespace, []byte("carddav:"+address))
	calDAVUUID := uuid.NewSHA1(appleProfileNamespace, []byte("caldav:"+address))

	profile := applePlist{}
	profile.dict(func(p *applePlist) {
		p.key("PayloadContent")
		p.array(func(p *applePlist) {
			p.dict(func(p *applePlist) {
				p.stringEntry("PayloadType", "com.apple.mail.managed")
				p.rawEntry("PayloadVersion", "<integer>1</integer>")
				p.stringEntry("PayloadIdentifier", "run.cloudlift.workspace.mail."+address)
				p.stringEntry("PayloadUUID", accountUUID.String())
				p.stringEntry("PayloadDisplayName", "Mail account "+address)

				p.stringEntry("EmailAccountDescription", address)
				p.stringEntry("EmailAccountName", address)
				p.stringEntry("EmailAccountType", "EmailTypeIMAP")
				p.stringEntry("EmailAddress", address)

				p.stringEntry("IncomingMailServerHostName", d.mailHost)
				p.rawEntry("IncomingMailServerPortNumber", fmt.Sprintf("<integer>%d</integer>", imapTLSPort))
				p.rawEntry("IncomingMailServerUseSSL", "<true/>")
				p.stringEntry("IncomingMailServerAuthentication", "EmailAuthPassword")
				p.stringEntry("IncomingMailServerUsername", address)

				p.stringEntry("OutgoingMailServerHostName", d.mailHost)
				p.rawEntry("OutgoingMailServerPortNumber", fmt.Sprintf("<integer>%d</integer>", submissionTLSPort))
				p.rawEntry("OutgoingMailServerUseSSL", "<true/>")
				p.stringEntry("OutgoingMailServerAuthentication", "EmailAuthPassword")
				p.stringEntry("OutgoingMailServerUsername", address)
				// The two accounts share a password, so the device should not
				// ask for it twice.
				p.rawEntry("OutgoingPasswordSameAsIncomingPassword", "<true/>")
			})

			// Mail alone gives the device Mail and Notes. Contacts and
			// Calendar are separate account types and need their own
			// payloads, pointing at the CardDAV and CalDAV collections.
			p.dict(func(p *applePlist) {
				p.stringEntry("PayloadType", "com.apple.carddav.account")
				p.rawEntry("PayloadVersion", "<integer>1</integer>")
				p.stringEntry("PayloadIdentifier", "run.cloudlift.workspace.carddav."+address)
				p.stringEntry("PayloadUUID", cardDAVUUID.String())
				p.stringEntry("PayloadDisplayName", "Contacts for "+address)

				p.stringEntry("CardDAVAccountDescription", address+" contacts")
				p.stringEntry("CardDAVHostName", d.davHost)
				p.rawEntry("CardDAVPort", fmt.Sprintf("<integer>%d</integer>", httpsPort))
				p.rawEntry("CardDAVUseSSL", "<true/>")
				p.stringEntry("CardDAVUsername", address)
				p.stringEntry("CardDAVPrincipalURL", "/dav/")
			})

			p.dict(func(p *applePlist) {
				p.stringEntry("PayloadType", "com.apple.caldav.account")
				p.rawEntry("PayloadVersion", "<integer>1</integer>")
				p.stringEntry("PayloadIdentifier", "run.cloudlift.workspace.caldav."+address)
				p.stringEntry("PayloadUUID", calDAVUUID.String())
				p.stringEntry("PayloadDisplayName", "Calendar for "+address)

				p.stringEntry("CalDAVAccountDescription", address+" calendar")
				p.stringEntry("CalDAVHostName", d.davHost)
				p.rawEntry("CalDAVPort", fmt.Sprintf("<integer>%d</integer>", httpsPort))
				p.rawEntry("CalDAVUseSSL", "<true/>")
				p.stringEntry("CalDAVUsername", address)
				p.stringEntry("CalDAVPrincipalURL", "/cal/")
			})
		})

		p.stringEntry("PayloadType", "Configuration")
		p.rawEntry("PayloadVersion", "<integer>1</integer>")
		p.stringEntry("PayloadIdentifier", "run.cloudlift.workspace."+address)
		p.stringEntry("PayloadUUID", profileUUID.String())
		p.stringEntry("PayloadDisplayName", domain+" mail")
		p.stringEntry("PayloadDescription", "Configures "+address+" for Mail, Contacts and Calendar.")
		p.stringEntry("PayloadOrganization", domain)
		p.rawEntry("PayloadRemovalDisallowed", "<false/>")
	})

	w.Header().Set("Content-Type", appleProfileContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+domain+`-mail.mobileconfig"`)
	if _, err := w.Write(profile.bytes()); err != nil {
		obs.Log(r.Context(), slog.LevelError, "discovery: writing apple profile failed", "error", err)
	}
}

// applePlist builds the XML property list a configuration profile is made of.
// The format nests dictionaries of keys and values, which the encoding/xml
// struct tags do not model, so it is written directly with values escaped.
type applePlist struct {
	buffer bytes.Buffer
	depth  int
}

func (p *applePlist) indent() {
	p.buffer.WriteString(strings.Repeat("\t", p.depth))
}

func (p *applePlist) write(format string, args ...any) {
	p.indent()
	fmt.Fprintf(&p.buffer, format+"\n", args...)
}

func (p *applePlist) dict(body func(*applePlist)) {
	p.write("<dict>")
	p.depth++
	body(p)
	p.depth--
	p.write("</dict>")
}

func (p *applePlist) array(body func(*applePlist)) {
	p.write("<array>")
	p.depth++
	body(p)
	p.depth--
	p.write("</array>")
}

func (p *applePlist) key(name string) {
	p.write("<key>%s</key>", escapeXML(name))
}

func (p *applePlist) stringEntry(key, value string) {
	p.key(key)
	p.write("<string>%s</string>", escapeXML(value))
}

// rawEntry writes a value that is not a string, such as an integer or boolean.
func (p *applePlist) rawEntry(key, value string) {
	p.key(key)
	p.write("%s", value)
}

func (p *applePlist) bytes() []byte {
	var out bytes.Buffer
	out.WriteString(xml.Header)
	out.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	out.WriteString(`<plist version="1.0">` + "\n")
	out.Write(p.buffer.Bytes())
	out.WriteString("</plist>\n")
	return out.Bytes()
}

func escapeXML(value string) string {
	var escaped bytes.Buffer
	xml.EscapeText(&escaped, []byte(value))
	return escaped.String()
}
