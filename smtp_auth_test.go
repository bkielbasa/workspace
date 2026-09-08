package main

import (
	"bufio"
	"encoding/base64"
	"strings"
	"testing"
)

// exchange runs readAuthCredentials against a scripted client and reports the
// credentials it extracted along with everything the server wrote back.
func exchange(t *testing.T, mechanism, initial string, clientLines ...string) (string, string, bool, []string) {
	t.Helper()

	client := ""
	for _, line := range clientLines {
		client += line + "\r\n"
	}

	var replies []string
	user, pass, ok := readAuthCredentials(
		mechanism,
		initial,
		bufio.NewReader(strings.NewReader(client)),
		func(reply string) { replies = append(replies, reply) },
	)
	return user, pass, ok, replies
}

func b64(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

// Outlook sends the username on the AUTH command itself. A server that
// challenges for a username anyway reads the password as the username, which
// is what made SMTP submission reject every Outlook login.
func TestAuthLoginInitialResponse(t *testing.T) {
	user, pass, ok, replies := exchange(t, "LOGIN", b64("contact@cloudlift.run"), b64("secret"))

	if !ok {
		t.Fatalf("auth exchange failed, replies: %v", replies)
	}
	if user != "contact@cloudlift.run" {
		t.Errorf("username = %q, want contact@cloudlift.run", user)
	}
	if pass != "secret" {
		t.Errorf("password = %q, want secret", pass)
	}
	// The username was already supplied, so only the password is prompted.
	if len(replies) != 1 || replies[0] != "334 UGFzc3dvcmQ6" {
		t.Errorf("replies = %v, want only a password challenge", replies)
	}
}

func TestAuthLoginChallenged(t *testing.T) {
	user, pass, ok, replies := exchange(t, "LOGIN", "", b64("contact@cloudlift.run"), b64("secret"))

	if !ok {
		t.Fatalf("auth exchange failed, replies: %v", replies)
	}
	if user != "contact@cloudlift.run" || pass != "secret" {
		t.Errorf("credentials = %q/%q", user, pass)
	}
	want := []string{"334 VXNlcm5hbWU6", "334 UGFzc3dvcmQ6"}
	if len(replies) != 2 || replies[0] != want[0] || replies[1] != want[1] {
		t.Errorf("replies = %v, want %v", replies, want)
	}
}

func TestAuthPlainInitialResponse(t *testing.T) {
	user, pass, ok, replies := exchange(t, "PLAIN", b64("\x00contact@cloudlift.run\x00secret"))

	if !ok {
		t.Fatalf("auth exchange failed, replies: %v", replies)
	}
	if user != "contact@cloudlift.run" || pass != "secret" {
		t.Errorf("credentials = %q/%q", user, pass)
	}
	if len(replies) != 0 {
		t.Errorf("replies = %v, want no challenge", replies)
	}
}

// Clients are equally entitled to send a bare "AUTH PLAIN" and wait for the
// empty challenge before sending the payload.
func TestAuthPlainChallenged(t *testing.T) {
	user, pass, ok, replies := exchange(t, "PLAIN", "", b64("\x00contact@cloudlift.run\x00secret"))

	if !ok {
		t.Fatalf("auth exchange failed, replies: %v", replies)
	}
	if user != "contact@cloudlift.run" || pass != "secret" {
		t.Errorf("credentials = %q/%q", user, pass)
	}
	if len(replies) != 1 || replies[0] != "334 " {
		t.Errorf("replies = %v, want an empty challenge", replies)
	}
}

func TestAuthCancellation(t *testing.T) {
	if _, _, ok, replies := exchange(t, "LOGIN", "", "*"); ok {
		t.Errorf("cancelled exchange succeeded, replies: %v", replies)
	}
}

func TestAuthRejectsBadInput(t *testing.T) {
	cases := []struct {
		name        string
		mechanism   string
		initial     string
		clientLines []string
		wantReply   string
	}{
		{
			name:      "unknown mechanism",
			mechanism: "CRAM-MD5",
			wantReply: "504 unrecognized authentication type",
		},
		{
			name:      "malformed base64",
			mechanism: "LOGIN",
			initial:   "!!!not base64!!!",
			wantReply: "501 invalid base64",
		},
		{
			name:      "plain payload missing password",
			mechanism: "PLAIN",
			initial:   b64("contact@cloudlift.run"),
			wantReply: "501 invalid auth format",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ok, replies := exchange(t, tc.mechanism, tc.initial, tc.clientLines...)
			if ok {
				t.Fatalf("exchange succeeded, want rejection")
			}
			if len(replies) == 0 || replies[len(replies)-1] != tc.wantReply {
				t.Errorf("replies = %v, want %q", replies, tc.wantReply)
			}
		})
	}
}
