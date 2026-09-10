package imap

import (
	"bufio"
	"encoding/base64"
	"strings"
)

// readCredentials runs the AUTHENTICATE exchange for the SASL mechanisms named
// in the server's capability list.
//
// Both mechanisms accept an initial response (RFC 4959 SASL-IR) and both must
// also work when the client waits for a continuation request. LOGIN is
// advertised, so it has to be implemented: a client that picks an advertised
// mechanism the server then rejects cannot authenticate at all.
func readCredentials(mechanism, initial string, r *bufio.Reader, write func(string)) (username, password string, ok bool) {
	// Continuation requests are "+" SP [base64], and "*" cancels.
	challenge := func(prompt string) (string, bool) {
		write("+ " + prompt)
		line, err := r.ReadString('\n')
		if err != nil {
			return "", false
		}
		line = strings.TrimSpace(line)
		if line == "*" {
			return "", false
		}
		decoded, err := base64Decode(line)
		if err != nil {
			return "", false
		}
		return decoded, true
	}

	switch mechanism {
	case "PLAIN":
		// base64(authzid \0 authcid \0 password), either inline or in
		// response to an empty continuation request.
		decoded := ""
		if initial != "" {
			payload, err := base64Decode(initial)
			if err != nil {
				return "", "", false
			}
			decoded = payload
		} else if decoded, ok = challenge(""); !ok {
			return "", "", false
		}
		segments := strings.Split(decoded, "\x00")
		if len(segments) < 3 {
			return "", "", false
		}
		return segments[1], segments[2], true

	case "LOGIN":
		if initial != "" {
			decoded, err := base64Decode(initial)
			if err != nil {
				return "", "", false
			}
			username = decoded
		} else if username, ok = challenge("VXNlcm5hbWU6"); !ok { // "Username:"
			return "", "", false
		}
		if password, ok = challenge("UGFzc3dvcmQ6"); !ok { // "Password:"
			return "", "", false
		}
		return username, password, true

	default:
		return "", "", false
	}
}

func base64Decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
