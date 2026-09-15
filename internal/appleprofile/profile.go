// Package appleprofile builds the iOS configuration profile that sets up
// Mail, Contacts, Calendar and Files. iOS has no mail auto-configuration
// protocol, so opening the generated URL in Safari and installing the result
// writes the account settings for the user.
//
// The profile is unsigned, so iOS labels it "Unverified" during installation.
// Signing needs a certificate issued for profile signing; the settings it
// carries are public either way, and no master password is ever included —
// only per-device app passwords, which the owner can revoke.
package appleprofile

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const ContentType = "application/x-apple-aspen-config"

// Namespace keeps the identifiers for an address stable, so installing
// again replaces the account instead of adding a duplicate.
var namespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("workspace-apple-mail-profile"))

const (
	imapTLSPort       = 993
	submissionTLSPort = 465
	httpsPort         = 443
)

// Build renders the profile for address. webHost serves the Files clip and
// should be the host the profile was downloaded from. An empty password
// leaves the password fields out and iOS prompts during installation; a
// per-device app password suppresses every prompt. Master passwords must
// never be passed.
func Build(address, mailHost, davHost, webHost, password string) ([]byte, error) {
	address = strings.TrimSpace(address)
	_, domain, ok := strings.Cut(address, "@")
	if !ok || domain == "" || mailHost == "" || davHost == "" || webHost == "" {
		return nil, fmt.Errorf("appleprofile: address and hosts required")
	}

	profileUUID := uuid.NewSHA1(namespace, []byte(address))
	accountUUID := uuid.NewSHA1(namespace, []byte("account:"+address))
	cardDAVUUID := uuid.NewSHA1(namespace, []byte("carddav:"+address))
	calDAVUUID := uuid.NewSHA1(namespace, []byte("caldav:"+address))
	webclipUUID := uuid.NewSHA1(namespace, []byte("webclip:"+address))

	profile := plist{}
	profile.dict(func(p *plist) {
		p.key("PayloadContent")
		p.array(func(p *plist) {
			p.dict(func(p *plist) {
				p.stringEntry("PayloadType", "com.apple.mail.managed")
				p.rawEntry("PayloadVersion", "<integer>1</integer>")
				p.stringEntry("PayloadIdentifier", "run.cloudlift.workspace.mail."+address)
				p.stringEntry("PayloadUUID", accountUUID.String())
				p.stringEntry("PayloadDisplayName", "Mail account "+address)

				p.stringEntry("EmailAccountDescription", address)
				p.stringEntry("EmailAccountName", address)
				p.stringEntry("EmailAccountType", "EmailTypeIMAP")
				p.stringEntry("EmailAddress", address)

				p.stringEntry("IncomingMailServerHostName", mailHost)
				p.rawEntry("IncomingMailServerPortNumber", fmt.Sprintf("<integer>%d</integer>", imapTLSPort))
				p.rawEntry("IncomingMailServerUseSSL", "<true/>")
				p.stringEntry("IncomingMailServerAuthentication", "EmailAuthPassword")
				p.stringEntry("IncomingMailServerUsername", address)

				p.stringEntry("OutgoingMailServerHostName", mailHost)
				p.rawEntry("OutgoingMailServerPortNumber", fmt.Sprintf("<integer>%d</integer>", submissionTLSPort))
				p.rawEntry("OutgoingMailServerUseSSL", "<true/>")
				p.stringEntry("OutgoingMailServerAuthentication", "EmailAuthPassword")
				p.stringEntry("OutgoingMailServerUsername", address)
				// The two accounts share a password, so the device should not
				// ask for it twice.
				p.rawEntry("OutgoingPasswordSameAsIncomingPassword", "<true/>")

				if password != "" {
					p.stringEntry("IncomingPassword", password)
					p.stringEntry("OutgoingPassword", password)
				}
			})

			// Mail alone gives the device Mail and Notes. Contacts and
			// Calendar are separate account types and need their own
			// payloads, pointing at the CardDAV and CalDAV collections.
			p.dict(func(p *plist) {
				p.stringEntry("PayloadType", "com.apple.carddav.account")
				p.rawEntry("PayloadVersion", "<integer>1</integer>")
				p.stringEntry("PayloadIdentifier", "run.cloudlift.workspace.carddav."+address)
				p.stringEntry("PayloadUUID", cardDAVUUID.String())
				p.stringEntry("PayloadDisplayName", "Contacts for "+address)

				p.stringEntry("CardDAVAccountDescription", address+" contacts")
				p.stringEntry("CardDAVHostName", davHost)
				p.rawEntry("CardDAVPort", fmt.Sprintf("<integer>%d</integer>", httpsPort))
				p.rawEntry("CardDAVUseSSL", "<true/>")
				p.stringEntry("CardDAVUsername", address)
				p.stringEntry("CardDAVPrincipalURL", "/dav/")
				if password != "" {
					p.stringEntry("CardDAVPassword", password)
				}
			})

			p.dict(func(p *plist) {
				p.stringEntry("PayloadType", "com.apple.caldav.account")
				p.rawEntry("PayloadVersion", "<integer>1</integer>")
				p.stringEntry("PayloadIdentifier", "run.cloudlift.workspace.caldav."+address)
				p.stringEntry("PayloadUUID", calDAVUUID.String())
				p.stringEntry("PayloadDisplayName", "Calendar for "+address)

				p.stringEntry("CalDAVAccountDescription", address+" calendar")
				p.stringEntry("CalDAVHostName", davHost)
				p.rawEntry("CalDAVPort", fmt.Sprintf("<integer>%d</integer>", httpsPort))
				p.rawEntry("CalDAVUseSSL", "<true/>")
				p.stringEntry("CalDAVUsername", address)
				p.stringEntry("CalDAVPrincipalURL", "/cal/")
				if password != "" {
					p.stringEntry("CalDAVPassword", password)
				}
			})

			// iOS profiles have no native WebDAV account type, so files ride
			// along as a Web Clip: a home-screen icon opening the file
			// browser. webHost serves both the profile link and the UI.
			p.dict(func(p *plist) {
				p.stringEntry("PayloadType", "com.apple.webclip.managed")
				p.rawEntry("PayloadVersion", "<integer>1</integer>")
				p.stringEntry("PayloadIdentifier", "run.cloudlift.workspace.webclip."+address)
				p.stringEntry("PayloadUUID", webclipUUID.String())
				p.stringEntry("PayloadDisplayName", "Files for "+address)

				p.stringEntry("Label", "Files")
				p.stringEntry("URL", "https://"+webHost+"/files")
				p.rawEntry("IsRemovable", "<true/>")
				p.rawEntry("Precomposed", "<true/>")
			})
		})

		p.stringEntry("PayloadType", "Configuration")
		p.rawEntry("PayloadVersion", "<integer>1</integer>")
		p.stringEntry("PayloadIdentifier", "run.cloudlift.workspace."+address)
		p.stringEntry("PayloadUUID", profileUUID.String())
		p.stringEntry("PayloadDisplayName", domain+" mail")
		p.stringEntry("PayloadDescription", "Configures "+address+" for Mail, Contacts, Calendar and Files.")
		p.stringEntry("PayloadOrganization", domain)
		p.rawEntry("PayloadRemovalDisallowed", "<false/>")
	})

	var out bytes.Buffer
	out.WriteString(xml.Header)
	out.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	out.WriteString(`<plist version="1.0">` + "\n")
	out.Write(profile.bytes())
	out.WriteString("</plist>\n")
	return out.Bytes(), nil
}

// plist builds the XML property list a configuration profile is made of.
// The format nests dictionaries of keys and values, which the encoding/xml
// struct tags do not model, so it is written directly with values escaped.
type plist struct {
	buffer bytes.Buffer
	depth  int
}

func (p *plist) bytes() []byte { return p.buffer.Bytes() }

func (p *plist) indent() {
	p.buffer.WriteString(strings.Repeat("\t", p.depth))
}

func (p *plist) write(format string, args ...any) {
	p.indent()
	fmt.Fprintf(&p.buffer, format+"\n", args...)
}

func (p *plist) dict(body func(*plist)) {
	p.write("<dict>")
	p.depth++
	body(p)
	p.depth--
	p.write("</dict>")
}

func (p *plist) array(body func(*plist)) {
	p.write("<array>")
	p.depth++
	body(p)
	p.depth--
	p.write("</array>")
}

func (p *plist) key(name string) {
	p.write("<key>%s</key>", escapeXML(name))
}

func (p *plist) stringEntry(key, value string) {
	p.key(key)
	p.write("<string>%s</string>", escapeXML(value))
}

// rawEntry writes a value that is not a string, such as an integer or boolean.
func (p *plist) rawEntry(key, value string) {
	p.key(key)
	p.write("%s", value)
}

func escapeXML(value string) string {
	var escaped bytes.Buffer
	xml.EscapeText(&escaped, []byte(value))
	return escaped.String()
}
