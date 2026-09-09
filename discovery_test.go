package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
)

func testDiscovery() *discovery {
	return &discovery{mailHost: "mail.cloudlift.run"}
}

func serve(t *testing.T, handler http.HandlerFunc, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader = strings.NewReader(body)
	request := httptest.NewRequest(method, target, reader)
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func TestRequestedAddress(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		body   string
		want   string
	}{
		{
			name:   "thunderbird query parameter",
			method: http.MethodGet,
			target: "/mail/config-v1.1.xml?emailaddress=contact@cloudlift.run",
			want:   "contact@cloudlift.run",
		},
		{
			name:   "outlook query parameter",
			method: http.MethodGet,
			target: "/autodiscover/autodiscover.xml?Email=contact@cloudlift.run",
			want:   "contact@cloudlift.run",
		},
		{
			name:   "outlook post body",
			method: http.MethodPost,
			target: "/autodiscover/autodiscover.xml",
			body:   "<Autodiscover><Request><EMailAddress>contact@cloudlift.run</EMailAddress></Request></Autodiscover>",
			want:   "contact@cloudlift.run",
		},
		{
			name:   "autodiscover v2 path",
			method: http.MethodGet,
			target: "/autodiscover/autodiscover.json/v1.0/contact@cloudlift.run",
			want:   "contact@cloudlift.run",
		},
		{
			name:   "absent",
			method: http.MethodGet,
			target: "/autodiscover/autodiscover.xml",
			want:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			if got := requestedAddress(request); got != tc.want {
				t.Errorf("requestedAddress = %q, want %q", got, tc.want)
			}
		})
	}
}

// Thunderbird discards a configuration whose <domain> does not cover the
// address it asked about, so the mail domain must be advertised rather than
// the host clients connect to.
func TestAutoconfigAdvertisesMailDomain(t *testing.T) {
	recorder := serve(t, testDiscovery().mozillaAutoconfig, http.MethodGet,
		"/mail/config-v1.1.xml?emailaddress=contact@cloudlift.run", "")

	body := recorder.Body.String()
	if !strings.Contains(body, "<domain>cloudlift.run</domain>") {
		t.Errorf("missing mail domain, got:\n%s", body)
	}
	if strings.Contains(body, "<domain>mail.cloudlift.run</domain>") {
		t.Error("advertised the mail host as the mail domain")
	}
}

func TestAutoconfigServerSettings(t *testing.T) {
	recorder := serve(t, testDiscovery().mozillaAutoconfig, http.MethodGet,
		"/mail/config-v1.1.xml?emailaddress=contact@cloudlift.run", "")
	body := recorder.Body.String()

	for _, want := range []string{
		"<incomingServer type=\"imap\">",
		"<port>993</port>",
		"<socketType>SSL</socketType>",
		"<port>587</port>",
		"<socketType>STARTTLS</socketType>",
		"<username>%EMAILADDRESS%</username>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	// Both submission options have to be offered.
	if strings.Count(body, "<outgoingServer") != 2 {
		t.Errorf("want 2 outgoing servers, got %d", strings.Count(body, "<outgoingServer"))
	}
}

// Without an address there is no login name, and a settings document missing
// one leaves the client unable to finish: Outlook stops there rather than
// falling back to asking.
func TestAutodiscoverReportsErrorWithoutAddress(t *testing.T) {
	recorder := serve(t, testDiscovery().autodiscoverXML, http.MethodGet, "/autodiscover/autodiscover.xml", "")

	body := recorder.Body.String()
	if strings.Contains(body, "LoginName") {
		t.Errorf("LoginName present without a known address:\n%s", body)
	}
	if !strings.Contains(body, "<ErrorCode>600</ErrorCode>") {
		t.Errorf("want an autodiscover error document, got:\n%s", body)
	}
	if strings.Contains(body, "<Protocol>") {
		t.Error("returned settings the client cannot use")
	}
}

// Clients fetch the URL from the v2 response verbatim, with a GET and no
// body. If the address is not in that URL it is lost, and the v1 document
// comes back with no login name.
func TestAutodiscoverJSONCarriesAddressIntoURL(t *testing.T) {
	recorder := serve(t, testDiscovery().autodiscoverJSON, http.MethodGet,
		"/autodiscover/autodiscover.json/v1.0/contact@cloudlift.run?Protocol=AutodiscoverV1", "")

	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}

	parsed, err := neturl.Parse(body["Url"])
	if err != nil {
		t.Fatalf("Url is not a URL: %v", err)
	}
	if got := parsed.Query().Get("Email"); got != "contact@cloudlift.run" {
		t.Fatalf("Url carries Email=%q, want contact@cloudlift.run (Url=%s)", got, body["Url"])
	}

	// Following that URL must yield a document with the login name in it.
	followed := serve(t, testDiscovery().autodiscoverXML, http.MethodGet, parsed.RequestURI(), "")
	if count := strings.Count(followed.Body.String(), "<LoginName>contact@cloudlift.run</LoginName>"); count != 2 {
		t.Errorf("following the URL gave %d login names, want 2:\n%s", count, followed.Body.String())
	}
}

func TestAutodiscoverLoginNameIsFullAddress(t *testing.T) {
	recorder := serve(t, testDiscovery().autodiscoverXML, http.MethodPost, "/autodiscover/autodiscover.xml",
		"<Autodiscover><Request><EMailAddress>contact@cloudlift.run</EMailAddress></Request></Autodiscover>")

	body := recorder.Body.String()
	if strings.Count(body, "<LoginName>contact@cloudlift.run</LoginName>") != 2 {
		t.Errorf("want the full address as LoginName for both protocols:\n%s", body)
	}
	for _, want := range []string{"<Type>IMAP</Type>", "<Type>SMTP</Type>", "<Port>993</Port>", "<Port>465</Port>"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestAutodiscoverJSONPointsAtV1(t *testing.T) {
	recorder := serve(t, testDiscovery().autodiscoverJSON, http.MethodGet,
		"/autodiscover/autodiscover.json?Email=contact@cloudlift.run&Protocol=AutodiscoverV1", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body["Protocol"] != "AutodiscoverV1" {
		t.Errorf("Protocol = %q", body["Protocol"])
	}
	if want := "https://autodiscover.cloudlift.run/autodiscover/autodiscover.xml?Email=contact%40cloudlift.run"; body["Url"] != want {
		t.Errorf("Url = %q, want %q", body["Url"], want)
	}
}

func TestAutodiscoverJSONRejectsUnsupported(t *testing.T) {
	cases := []struct {
		name   string
		target string
		want   string
	}{
		{
			name:   "exchange protocol",
			target: "/autodiscover/autodiscover.json?Email=contact@cloudlift.run&Protocol=ActiveSync",
			want:   "InvalidProtocol",
		},
		{
			name:   "no address",
			target: "/autodiscover/autodiscover.json",
			want:   "MandatoryParameterMissing",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := serve(t, testDiscovery().autodiscoverJSON, http.MethodGet, tc.target, "")
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", recorder.Code)
			}
			var body map[string]string
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			if body["ErrorCode"] != tc.want {
				t.Errorf("ErrorCode = %q, want %q", body["ErrorCode"], tc.want)
			}
		})
	}
}

func TestServiceDomainsFallsBackToMailHostParent(t *testing.T) {
	got := testDiscovery().serviceDomains(t.Context(), "")
	if len(got) != 1 || got[0] != "cloudlift.run" {
		t.Errorf("serviceDomains = %v, want [cloudlift.run]", got)
	}
}
