package main

import (
	"bufio"
	"strings"
	"testing"
)

func imapExchange(t *testing.T, mechanism, initial string, clientLines ...string) (string, string, bool, []string) {
	t.Helper()

	client := ""
	for _, line := range clientLines {
		client += line + "\r\n"
	}

	var replies []string
	user, pass, ok := readIMAPCredentials(
		mechanism,
		initial,
		bufio.NewReader(strings.NewReader(client)),
		func(reply string) { replies = append(replies, reply) },
	)
	return user, pass, ok, replies
}

func TestIMAPAuthPlainInitialResponse(t *testing.T) {
	user, pass, ok, _ := imapExchange(t, "PLAIN", b64("\x00contact@cloudlift.run\x00secret"))
	if !ok {
		t.Fatal("auth exchange failed")
	}
	if user != "contact@cloudlift.run" || pass != "secret" {
		t.Errorf("credentials = %q/%q", user, pass)
	}
}

func TestIMAPAuthPlainChallenged(t *testing.T) {
	user, pass, ok, replies := imapExchange(t, "PLAIN", "", b64("\x00contact@cloudlift.run\x00secret"))
	if !ok {
		t.Fatal("auth exchange failed")
	}
	if user != "contact@cloudlift.run" || pass != "secret" {
		t.Errorf("credentials = %q/%q", user, pass)
	}
	// RFC 3501 continuation requests are "+" SP [base64]; a bare "+" is
	// malformed and strict clients reject it.
	if len(replies) != 1 || replies[0] != "+ " {
		t.Errorf("replies = %v, want a single %q", replies, "+ ")
	}
}

// LOGIN is advertised in the capability list, so it must work.
func TestIMAPAuthLogin(t *testing.T) {
	user, pass, ok, replies := imapExchange(t, "LOGIN", "", b64("contact@cloudlift.run"), b64("secret"))
	if !ok {
		t.Fatalf("auth exchange failed, replies: %v", replies)
	}
	if user != "contact@cloudlift.run" || pass != "secret" {
		t.Errorf("credentials = %q/%q", user, pass)
	}
	want := []string{"+ VXNlcm5hbWU6", "+ UGFzc3dvcmQ6"}
	if len(replies) != 2 || replies[0] != want[0] || replies[1] != want[1] {
		t.Errorf("replies = %v, want %v", replies, want)
	}
}

func TestIMAPAuthLoginInitialResponse(t *testing.T) {
	user, pass, ok, replies := imapExchange(t, "LOGIN", b64("contact@cloudlift.run"), b64("secret"))
	if !ok {
		t.Fatalf("auth exchange failed, replies: %v", replies)
	}
	if user != "contact@cloudlift.run" || pass != "secret" {
		t.Errorf("credentials = %q/%q", user, pass)
	}
	if len(replies) != 1 || replies[0] != "+ UGFzc3dvcmQ6" {
		t.Errorf("replies = %v, want only a password challenge", replies)
	}
}

func TestIMAPAuthUnknownMechanism(t *testing.T) {
	if _, _, ok, _ := imapExchange(t, "CRAM-MD5", ""); ok {
		t.Error("unknown mechanism accepted")
	}
}

// Every mechanism named in the greeting and CAPABILITY response has to be
// implemented, otherwise a client can pick one and be locked out.
func TestAdvertisedMechanismsAreImplemented(t *testing.T) {
	for _, mechanism := range []string{"PLAIN", "LOGIN"} {
		if _, _, ok, _ := imapExchange(t, mechanism, b64("\x00contact@cloudlift.run\x00secret"), b64("secret")); !ok {
			t.Errorf("advertised mechanism %s is not implemented", mechanism)
		}
	}
}
