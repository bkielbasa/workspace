package main

import (
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
)

func TestAppleProfileSettings(t *testing.T) {
	recorder := serve(t, testDiscovery().appleProfile, http.MethodGet,
		"/apple/mail.mobileconfig?email=contact@cloudlift.run", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	// iOS only offers to install the profile when it is served as one.
	if got := recorder.Header().Get("Content-Type"); got != appleProfileContentType {
		t.Errorf("Content-Type = %q, want %q", got, appleProfileContentType)
	}

	body := recorder.Body.String()
	for _, want := range []string{
		"<string>com.apple.mail.managed</string>",
		"<string>EmailTypeIMAP</string>",
		"<string>contact@cloudlift.run</string>",
		"<string>mail.cloudlift.run</string>",
		"<integer>993</integer>",
		"<integer>465</integer>",
		"<key>IncomingMailServerUseSSL</key>\n\t\t\t<true/>",
		"<key>OutgoingPasswordSameAsIncomingPassword</key>\n\t\t\t<true/>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// A password must never be embedded; the device prompts for it.
	if strings.Contains(strings.ToLower(body), "<key>emailaccountpassword</key>") {
		t.Error("profile embeds a password")
	}
}

// The profile has to be well-formed XML or the device rejects it outright.
func TestAppleProfileIsWellFormed(t *testing.T) {
	recorder := serve(t, testDiscovery().appleProfile, http.MethodGet,
		"/apple/mail.mobileconfig?email=contact@cloudlift.run", "")

	decoder := xml.NewDecoder(strings.NewReader(recorder.Body.String()))
	for {
		_, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("profile is not well-formed: %v", err)
		}
	}
}

// Reinstalling must replace the account rather than add a second copy, which
// is keyed off the identifiers staying stable for an address.
func TestAppleProfileIdentifiersAreStable(t *testing.T) {
	first := serve(t, testDiscovery().appleProfile, http.MethodGet,
		"/apple/mail.mobileconfig?email=contact@cloudlift.run", "").Body.String()
	second := serve(t, testDiscovery().appleProfile, http.MethodGet,
		"/apple/mail.mobileconfig?email=contact@cloudlift.run", "").Body.String()

	if first != second {
		t.Error("profile is not stable across requests")
	}

	other := serve(t, testDiscovery().appleProfile, http.MethodGet,
		"/apple/mail.mobileconfig?email=someone@cloudlift.run", "").Body.String()
	if other == first {
		t.Error("different addresses produced the same profile")
	}
}

func TestAppleProfileEscapesAddress(t *testing.T) {
	recorder := serve(t, testDiscovery().appleProfile, http.MethodGet,
		"/apple/mail.mobileconfig?email=%3Cscript%3E@cloudlift.run", "")

	body := recorder.Body.String()
	if strings.Contains(body, "<script>") {
		t.Errorf("address was not escaped:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected the address to appear escaped")
	}
}

func TestAppleProfileRequiresAddress(t *testing.T) {
	for _, target := range []string{
		"/apple/mail.mobileconfig",
		"/apple/mail.mobileconfig?email=not-an-address",
	} {
		recorder := serve(t, testDiscovery().appleProfile, http.MethodGet, target, "")
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", target, recorder.Code)
		}
	}
}
