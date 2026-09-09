package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
)

// discovery serves the client auto-configuration protocols. Mail clients pick
// their settings through one of three mechanisms, and each expects its own
// URL and document format:
//
//   - Outlook (and Exchange-derived clients) fetch the Autodiscover v1 POX
//     document, discovering it either directly at autodiscover.<domain> or
//     through the Autodiscover v2 JSON endpoint.
//   - Thunderbird and clients following the Mozilla convention fetch a
//     clientConfig document from autoconfig.<domain>/mail/config-v1.1.xml,
//     falling back to <domain>/.well-known/autoconfig/....
//   - Clients implementing RFC 6186 look up SRV records, which are served
//     from DNS rather than from here.
type discovery struct {
	// mailHost is the host clients connect to for IMAP and submission.
	mailHost string
	// davHost serves the CardDAV and CalDAV collections over HTTPS. It is
	// not the mail host: on the local network that name resolves to the mail
	// load balancer, which publishes no HTTPS port.
	davHost string
	// domains lists the mail domains this server is authoritative for; the
	// Mozilla document has to name them, since a client rejects a
	// configuration that does not cover the address it asked about.
	domains *Domains
}

// imapPort and the submission ports advertised to clients. Implicit TLS is
// advertised for submission because it is the port proven reachable from
// outside; STARTTLS on 587 is offered to Mozilla clients as an alternative.
const (
	imapTLSPort        = 993
	submissionTLSPort  = 465
	submissionSTARTTLS = 587
	httpsPort          = 443
)

func (d *discovery) register(mux *http.ServeMux) {
	// Mozilla autoconfig. Thunderbird looks under the autoconfig subdomain
	// first and only then at the mail domain's well-known location, so both
	// paths must be served.
	mux.HandleFunc("/mail/config-v1.1.xml", d.mozillaAutoconfig)
	mux.HandleFunc("/.well-known/autoconfig/mail/config-v1.1.xml", d.mozillaAutoconfig)

	// Autodiscover v2 (JSON), in both its query and path forms.
	mux.HandleFunc("/autodiscover/autodiscover.json", d.autodiscoverJSON)
	mux.HandleFunc("/autodiscover/autodiscover.json/", d.autodiscoverJSON)

	// Autodiscover v1 (POX XML).
	mux.HandleFunc("/autodiscover/autodiscover.xml", d.autodiscoverXML)

	// Apple configuration profile. Apple devices look for no configuration
	// document of their own, so this is fetched by the user rather than
	// discovered by the client.
	mux.HandleFunc("/apple/mail.mobileconfig", d.appleProfile)
}

// requestedAddress returns the address the client is asking about. Thunderbird
// passes it as a query parameter, Outlook in the POST body or a query
// parameter.
func requestedAddress(r *http.Request) string {
	for _, key := range []string{"emailaddress", "Email", "email"} {
		if value := r.URL.Query().Get(key); value != "" {
			return strings.TrimSpace(value)
		}
	}

	if r.Method == http.MethodPost {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 16*1024))
		if address := xmlElement(string(body), "EMailAddress"); address != "" {
			return address
		}
	}

	// Autodiscover v2 addresses the mailbox in the path:
	// /autodiscover/autodiscover.json/v1.0/<email>
	if _, rest, found := strings.Cut(r.URL.Path, "/autodiscover.json/v1.0/"); found {
		return strings.TrimSpace(strings.Trim(rest, "/"))
	}

	return ""
}

func xmlElement(document, name string) string {
	_, rest, found := strings.Cut(document, "<"+name+">")
	if !found {
		return ""
	}
	value, _, found := strings.Cut(rest, "</"+name+">")
	if !found {
		return ""
	}
	return strings.TrimSpace(value)
}

// serviceDomains returns the mail domains to advertise. The domains the server
// actually hosts come from the database; the address the client asked about is
// included so a freshly provisioned domain still resolves to a usable
// configuration.
func (d *discovery) serviceDomains(ctx context.Context, address string) []string {
	seen := map[string]bool{}
	var result []string

	add := func(domain string) {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" || seen[domain] {
			return
		}
		seen[domain] = true
		result = append(result, domain)
	}

	if _, domain, found := strings.Cut(address, "@"); found {
		add(domain)
	}

	if d.domains != nil {
		hosted, err := d.domains.List(ctx)
		if err != nil {
			logWithTrace(ctx, slog.LevelWarn, "discovery: could not list domains", "error", err)
		}
		for _, domain := range hosted {
			add(domain.Name)
		}
	}

	if len(result) == 0 {
		// Last resort: derive the mail domain from the advertised host by
		// dropping its leftmost label (mail.example.com -> example.com).
		if _, parent, found := strings.Cut(d.mailHost, "."); found && strings.Contains(parent, ".") {
			add(parent)
		} else {
			add(d.mailHost)
		}
	}

	return result
}

// mozillaClientConfig is the Thunderbird auto-configuration document.
type mozillaClientConfig struct {
	XMLName  xml.Name `xml:"clientConfig"`
	Version  string   `xml:"version,attr"`
	Provider struct {
		ID               string   `xml:"id,attr"`
		Domains          []string `xml:"domain"`
		DisplayName      string   `xml:"displayName"`
		DisplayShortName string   `xml:"displayShortName"`
		Incoming         mozillaServer
		Outgoing         []mozillaServer
		AddressBook      struct {
			Type string `xml:"type,attr"`
			URL  string `xml:"url"`
		} `xml:"addressBook"`
		Calendar struct {
			Type string `xml:"type,attr"`
			URL  string `xml:"url"`
		} `xml:"calendar"`
	} `xml:"emailProvider"`
}

type mozillaServer struct {
	XMLName        xml.Name `xml:""`
	Type           string   `xml:"type,attr"`
	Hostname       string   `xml:"hostname"`
	Port           int      `xml:"port"`
	SocketType     string   `xml:"socketType"`
	Authentication string   `xml:"authentication"`
	Username       string   `xml:"username"`
}

func (d *discovery) mozillaAutoconfig(w http.ResponseWriter, r *http.Request) {
	domains := d.serviceDomains(r.Context(), requestedAddress(r))

	config := mozillaClientConfig{Version: "1.1"}
	config.Provider.ID = domains[0]
	config.Provider.Domains = domains
	config.Provider.DisplayName = domains[0]
	config.Provider.DisplayShortName = domains[0]

	config.Provider.Incoming = mozillaServer{
		XMLName:        xml.Name{Local: "incomingServer"},
		Type:           "imap",
		Hostname:       d.mailHost,
		Port:           imapTLSPort,
		SocketType:     "SSL",
		Authentication: "password-cleartext",
		Username:       "%EMAILADDRESS%",
	}
	// Implicit TLS first, STARTTLS offered as the alternative.
	for _, outgoing := range []struct {
		port       int
		socketType string
	}{
		{submissionTLSPort, "SSL"},
		{submissionSTARTTLS, "STARTTLS"},
	} {
		config.Provider.Outgoing = append(config.Provider.Outgoing, mozillaServer{
			XMLName:        xml.Name{Local: "outgoingServer"},
			Type:           "smtp",
			Hostname:       d.mailHost,
			Port:           outgoing.port,
			SocketType:     outgoing.socketType,
			Authentication: "password-cleartext",
			Username:       "%EMAILADDRESS%",
		})
	}

	config.Provider.AddressBook.Type = "carddav"
	config.Provider.AddressBook.URL = "https://" + d.mailHost + "/dav/"
	config.Provider.Calendar.Type = "caldav"
	config.Provider.Calendar.URL = "https://" + d.mailHost + "/cal/"

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Write([]byte(xml.Header))
	if err := xml.NewEncoder(w).Encode(config); err != nil {
		logWithTrace(r.Context(), slog.LevelError, "discovery: encoding autoconfig failed", "error", err)
	}
}

// autodiscoverJSON implements Autodiscover v2, whose only job is to tell the
// client where to find the protocol it asked for. Returning the v1 URL here is
// what lets an Outlook client that starts at the JSON endpoint reach the POX
// document.
func (d *discovery) autodiscoverJSON(w http.ResponseWriter, r *http.Request) {
	writeJSON := func(status int, body any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			logWithTrace(r.Context(), slog.LevelError, "discovery: encoding json failed", "error", err)
		}
	}

	address := requestedAddress(r)
	if address == "" {
		writeJSON(http.StatusBadRequest, map[string]string{
			"ErrorCode":    "MandatoryParameterMissing",
			"ErrorMessage": "The parameter 'Email' is required.",
		})
		return
	}

	protocol := r.URL.Query().Get("Protocol")
	if protocol == "" {
		protocol = "AutodiscoverV1"
	}

	// Every other protocol here is an Exchange service this server does not
	// implement. Answering "that protocol is invalid" still says Autodiscover
	// lives at this domain, which invites clients to treat the account as
	// Exchange and try to sync it over protocols that are not there. Not
	// found is the accurate answer: no such service is hosted.
	if !strings.EqualFold(protocol, "AutodiscoverV1") {
		writeJSON(http.StatusNotFound, map[string]string{
			"ErrorCode":    "ProtocolNotFound",
			"ErrorMessage": fmt.Sprintf("The protocol '%s' is not hosted for this domain. This is an IMAP and SMTP server.", protocol),
		})
		return
	}

	// The address has to be carried into the URL. Clients fetch exactly what
	// is returned here, with a GET and no body, so an address left out of the
	// query string is lost and the v1 document comes back without a login
	// name for the client to use.
	url := "https://" + autodiscoverHost(d.mailHost, address) +
		"/autodiscover/autodiscover.xml?Email=" + neturl.QueryEscape(address)

	writeJSON(http.StatusOK, map[string]string{
		"Protocol": "AutodiscoverV1",
		"Url":      url,
	})
}

// autodiscoverHost returns the host serving the POX document for an address.
func autodiscoverHost(mailHost, address string) string {
	if _, domain, found := strings.Cut(address, "@"); found && domain != "" {
		return "autodiscover." + strings.ToLower(domain)
	}
	return mailHost
}

// autodiscoverResponse is the Autodiscover v1 POX document.
type autodiscoverResponse struct {
	XMLName  xml.Name `xml:"http://schemas.microsoft.com/exchange/2010/autodiscover Autodiscover"`
	Response struct {
		XMLNS   string `xml:"xmlns,attr"`
		Account struct {
			AccountType string `xml:"AccountType"`
			Action      string `xml:"Action"`
			Protocols   []autodiscoverProtocol
		} `xml:"Account"`
	} `xml:"Response"`
}

type autodiscoverProtocol struct {
	XMLName        xml.Name `xml:"Protocol"`
	Type           string   `xml:"Type"`
	Server         string   `xml:"Server"`
	Port           int      `xml:"Port"`
	DomainRequired string   `xml:"DomainRequired"`
	LoginName      string   `xml:"LoginName,omitempty"`
	SPA            string   `xml:"SPA"`
	SSL            string   `xml:"SSL"`
	Encryption     string   `xml:"Encryption"`
	AuthRequired   string   `xml:"AuthRequired"`
}

// autodiscoverError is the POX error document. Autodiscover reports failures
// in the body of a 200 response rather than with an HTTP status.
type autodiscoverError struct {
	XMLName  xml.Name `xml:"http://schemas.microsoft.com/exchange/2010/autodiscover Autodiscover"`
	Response struct {
		Error struct {
			Time      string `xml:"Time,attr"`
			ErrorCode int    `xml:"ErrorCode"`
			Message   string `xml:"Message"`
		} `xml:"Error"`
	} `xml:"Response"`
}

func (d *discovery) autodiscoverXML(w http.ResponseWriter, r *http.Request) {
	login := requestedAddress(r)

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")

	// Without an address there is no login name to hand back, and a settings
	// document missing one leaves the client unable to finish configuring the
	// account. Reporting the request as invalid makes it ask rather than
	// accept a configuration it cannot use.
	if login == "" {
		var failure autodiscoverError
		failure.Response.Error.Time = time.Now().Format("15:04:05.0000000")
		failure.Response.Error.ErrorCode = 600
		failure.Response.Error.Message = "Invalid Request"

		w.Write([]byte(xml.Header))
		if err := xml.NewEncoder(w).Encode(failure); err != nil {
			logWithTrace(r.Context(), slog.LevelError, "discovery: encoding autodiscover error failed", "error", err)
		}
		return
	}

	var response autodiscoverResponse
	response.Response.XMLNS = "http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a"
	response.Response.Account.AccountType = "email"
	response.Response.Account.Action = "settings"
	response.Response.Account.Protocols = []autodiscoverProtocol{
		{
			Type:           "IMAP",
			Server:         d.mailHost,
			Port:           imapTLSPort,
			DomainRequired: "off",
			LoginName:      login,
			SPA:            "off",
			SSL:            "on",
			Encryption:     "SSL",
			AuthRequired:   "on",
		},
		{
			Type:           "SMTP",
			Server:         d.mailHost,
			Port:           submissionTLSPort,
			DomainRequired: "off",
			LoginName:      login,
			SPA:            "off",
			SSL:            "on",
			Encryption:     "SSL",
			AuthRequired:   "on",
		},
	}

	w.Write([]byte(xml.Header))
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		logWithTrace(r.Context(), slog.LevelError, "discovery: encoding autodiscover failed", "error", err)
	}
}
