package smtp

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// commandVerb returns the upper-cased verb of an SMTP command line, or an
// empty string when the client sent nothing but whitespace. Clients do send
// bare line endings, and strings.Fields yields no fields for them.
func commandVerb(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}

// readAuthCredentials runs the RFC 4954 AUTH exchange for the PLAIN and LOGIN
// mechanisms and returns the credentials the client supplied.
//
// Both mechanisms may carry an initial response on the AUTH command itself,
// and both must equally work when the client waits to be challenged. Outlook
// sends the username inline as "AUTH LOGIN <base64>": a server that ignores
// the initial response and challenges for a username anyway receives the
// client's password in reply, so authentication can never succeed.
//
// Any protocol-level failure is reported to the client here and reported as
// ok=false; the caller only has to abandon the command.
func readAuthCredentials(mechanism, initial string, r *bufio.Reader, write func(string)) (username, password string, ok bool) {
	// read returns the next client line, resolving the "*" cancellation.
	read := func() (string, bool) {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", false
		}
		line = strings.TrimSpace(line)
		if line == "*" {
			write("501 authentication canceled")
			return "", false
		}
		return line, true
	}

	// challenge prompts with a base64 string and decodes the reply.
	challenge := func(prompt string) (string, bool) {
		write("334 " + prompt)
		line, ok := read()
		if !ok {
			return "", false
		}
		decoded, err := base64Decode(line)
		if err != nil {
			write("501 invalid base64")
			return "", false
		}
		return decoded, true
	}

	switch mechanism {
	case "PLAIN":
		encoded := initial
		if encoded == "" {
			// An empty challenge asks for the whole payload.
			write("334 ")
			if encoded, ok = read(); !ok {
				return "", "", false
			}
		}
		decoded, err := base64Decode(encoded)
		if err != nil {
			write("501 invalid base64")
			return "", "", false
		}
		// base64(authzid \0 authcid \0 password)
		segments := strings.Split(decoded, "\x00")
		if len(segments) < 3 {
			write("501 invalid auth format")
			return "", "", false
		}
		return segments[1], segments[2], true

	case "LOGIN":
		if initial != "" {
			decoded, err := base64Decode(initial)
			if err != nil {
				write("501 invalid base64")
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
		write("504 unrecognized authentication type")
		return "", "", false
	}
}

func smtpHeader(raw, name string) string {
	prefix := strings.ToLower(name) + ":"
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

func extractEmail(line string) string {
	parts := strings.Split(line, ":")
	if len(parts) < 2 {
		return ""
	}
	v := strings.TrimSpace(parts[1])
	v = strings.Trim(v, "<>")
	return v
}

func base64Decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// prepareMessage fills in the headers a submitting client may have omitted.
// Generated Message-IDs are scoped to hostname, the name this server answers
// to, so they stay globally unique.
func prepareMessage(raw, from, to, hostname string) string {
	headers, body := splitHeaderBody(raw)

	h := map[string]string{}
	for _, line := range strings.Split(headers, "\r\n") {
		if i := strings.Index(line, ":"); i > 0 {
			k := strings.ToLower(strings.TrimSpace(line[:i]))
			v := strings.TrimSpace(line[i+1:])
			h[k] = v
		}
	}

	if h["date"] == "" {
		headers = "Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" + headers
	}

	if h["message-id"] == "" {
		headers = fmt.Sprintf("Message-ID: <%d@%s>\r\n%s", time.Now().UnixNano(), hostname, headers)
	}

	if h["from"] == "" {
		headers = "From: " + from + "\r\n" + headers
	}

	if h["to"] == "" {
		headers = "To: " + to + "\r\n" + headers
	}

	if h["mime-version"] == "" {
		headers = "MIME-Version: 1.0\r\n" + headers
	}

	if h["content-type"] == "" {
		headers = "Content-Type: text/plain; charset=UTF-8\r\n" + headers
	}

	return headers + "\r\n" + body
}

func splitHeaderBody(raw string) (string, string) {
	parts := strings.SplitN(raw, "\r\n\r\n", 2)
	if len(parts) != 2 {
		return raw, ""
	}
	return parts[0], parts[1]
}
