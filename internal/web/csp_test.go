package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The week grid positions events with generated CSS. That only reaches the
// browser if the <style> nonce matches the one advertised in the CSP.
func TestSecurityHeadersNonceMatchesRenderedStyle(t *testing.T) {
	var rendered string
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rendered = styleNonce(r.Context())
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/calendars", nil))

	if rendered == "" {
		t.Fatal("handler received no style nonce")
	}
	policy := recorder.Header().Get("Content-Security-Policy")
	if want := "style-src 'self' 'unsafe-inline' 'nonce-" + rendered + "'"; !strings.Contains(policy, want) {
		t.Fatalf("CSP %q does not contain %q", policy, want)
	}
	// Scripts must stay fully banned even though inline styles are allowed
	// for sanitized mail bodies.
	if strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("CSP must not allow inline scripts: %q", policy)
	}
}

func TestSecurityHeadersNonceIsPerRequest(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	nonce := regexp.MustCompile(`'nonce-([^']+)'`)

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/calendars", nil))
		match := nonce.FindStringSubmatch(recorder.Header().Get("Content-Security-Policy"))
		if match == nil {
			t.Fatal("no nonce in CSP")
		}
		if seen[match[1]] {
			t.Fatalf("nonce %q reused across requests", match[1])
		}
		seen[match[1]] = true
	}
}

func TestStyleNonceAbsentByDefault(t *testing.T) {
	if got := styleNonce(context.Background()); got != "" {
		t.Fatalf("styleNonce(empty ctx) = %q, want empty", got)
	}
}
