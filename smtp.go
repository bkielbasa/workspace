package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log"
	"log/slog"
	"net"
	stdmail "net/mail"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
)

type SMTPServer struct {
	addr      string
	delivery  *Delivery
	users     *Users
	tlsConfig *tls.Config
	wrapped   bool
}

func NewSMTPServer(addr string, d *Delivery, u *Users, tlsCfg *tls.Config) *SMTPServer {
	return &SMTPServer{addr: addr, delivery: d, users: u, tlsConfig: tlsCfg}
}

func NewSMTPTLSServer(addr string, d *Delivery, u *Users, tlsCfg *tls.Config) *SMTPServer {
	return &SMTPServer{addr: addr, delivery: d, users: u, tlsConfig: tlsCfg, wrapped: true}
}

func (s *SMTPServer) ListenAndServe() error {
	var ln net.Listener
	var err error
	if s.wrapped {
		ln, err = tls.Listen("tcp", s.addr, s.tlsConfig)
	} else {
		ln, err = net.Listen("tcp", s.addr)
	}
	if err != nil {
		return err
	}
	log.Printf("smtp listening on %s", s.addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("smtp accept error:", err)
			continue
		}
		go s.handleConn(conn)
	}
}

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

func (s *SMTPServer) handleConn(conn net.Conn) {
	defer conn.Close()

	// basic timeouts
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))

	tracer := otel.Tracer("smtp")
	ctx, span := tracer.Start(contextBackground(), "smtp.session")
	defer span.End()

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	write := func(msg string) {
		fmt.Fprintf(rw, "%s\r\n", msg)
		rw.Flush()
	}

	write("220 " + mailHostname + " ESMTP ready")

	// On the implicit-TLS listener the connection is already encrypted, so the
	// session must start in the TLS state: otherwise STARTTLS is advertised
	// inside TLS and a client taking us up on it would negotiate a nested
	// handshake.
	tlsEnabled := s.wrapped

	logWithTrace(ctx, slog.LevelInfo, "smtp connection",
		"remote", conn.RemoteAddr().String(),
		"local", conn.LocalAddr().String(),
		"implicit_tls", s.wrapped,
	)

	var from string
	var to []string
	var authedUser *User

	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)

		// Log the command verb only; AUTH lines and message data carry
		// credentials and message content.
		if verb := commandVerb(line); verb != "" {
			logWithTrace(ctx, slog.LevelInfo, "smtp cmd",
				"verb", verb,
				"remote", conn.RemoteAddr().String(),
				"tls", tlsEnabled,
			)
		}

		switch {
		case strings.HasPrefix(strings.ToUpper(line), "EHLO"):
			write("250-" + mailHostname)
			write("250-PIPELINING")
			write("250-8BITMIME")
			// Only offer AUTH once the channel is encrypted, so clients never
			// consider sending credentials in the clear.
			if tlsEnabled || s.tlsConfig == nil {
				write("250-AUTH LOGIN PLAIN")
			}
			if !tlsEnabled && s.tlsConfig != nil {
				write("250-STARTTLS")
			}
			write("250 OK")

		case strings.HasPrefix(strings.ToUpper(line), "HELO"):
			write("250 Hello")

		case strings.ToUpper(line) == "STARTTLS":
			if tlsEnabled {
				write("454 TLS already active")
				continue
			}
			if s.tlsConfig == nil || len(s.tlsConfig.Certificates) == 0 {
				write("454 TLS unavailable: no server certificate configured")
				continue
			}

			write("220 Ready to start TLS")

			tlsConn := tls.Server(conn, s.tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				log.Println("tls handshake error:", err)
				return
			}

			// swap connection + reader/writer
			conn = tlsConn
			rw = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
			tlsEnabled = true
			authedUser = nil // per RFC, reset state after STARTTLS
			from = ""
			to = nil

		case strings.HasPrefix(strings.ToUpper(line), "AUTH "):
			fields := strings.Fields(line)
			if len(fields) < 2 {
				write("501 syntax: AUTH mechanism [initial-response]")
				continue
			}
			// Credentials must not travel in the clear. When no certificate is
			// configured at all (local development) there is no TLS to require.
			if !tlsEnabled && s.tlsConfig != nil {
				write("538 encryption required for requested authentication mechanism")
				continue
			}
			mechanism := strings.ToUpper(fields[1])
			initial := ""
			if len(fields) > 2 {
				initial = fields[2]
			}

			username, password, ok := readAuthCredentials(mechanism, initial, rw.Reader, write)
			if !ok {
				continue
			}

			user, err := s.users.Authenticate(contextBackground(), username, password)
			incSMTPAuth(contextBackground(), err == nil)
			logWithTrace(ctx, slog.LevelInfo, "smtp auth attempt",
				"mechanism", mechanism,
				"user", username,
				"remote", conn.RemoteAddr().String(),
				"tls", tlsEnabled,
				"ok", err == nil,
			)
			if err != nil {
				write("535 auth failed")
				continue
			}
			authedUser = user
			write("235 authenticated")

		case strings.HasPrefix(strings.ToUpper(line), "MAIL FROM:"):
			sender := extractEmail(line)
			// empty sender (<>) is a valid DSN/bounce envelope
			if sender != "" {
				if _, err := stdmail.ParseAddress(sender); err != nil {
					write("501 invalid sender")
					continue
				}
			}
			// Authenticated users must use their own address as the envelope
			// sender; unauthenticated (inbound MX) connections are not checked
			// here — relay control happens at DATA time.
			if authedUser != nil && !strings.EqualFold(sender, authedUser.Email) {
				write("550 sender mismatch")
				continue
			}
			from = sender
			write("250 OK")

		case strings.HasPrefix(strings.ToUpper(line), "RCPT TO:"):
			recipient := extractEmail(line)
			if _, err := stdmail.ParseAddress(recipient); err != nil {
				write("501 invalid recipient")
				continue
			}
			to = append(to, recipient)
			write("250 OK")

		case strings.ToUpper(line) == "DATA":
			if from == "" || len(to) == 0 {
				write("503 valid MAIL FROM and RCPT TO required before DATA")
				continue
			}
			write("354 End data with <CR><LF>.<CR><LF>")

			var data strings.Builder
			for {
				l, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
				// RFC 5321 transparency: undo dot-stuffing performed by the client.
				if strings.HasPrefix(l, "..") {
					l = l[1:]
				}
				data.WriteString(l)
			}

			// build Message and deliver per-recipient
			raw := prepareMessage(data.String(), from, strings.Join(to, ", "))
			msg := &Message{
				Sender:     from,
				Recipients: append([]string(nil), to...),
				RawMessage: raw,
				MessageID:  smtpHeader(raw, "Message-ID"),
				Subject:    smtpHeader(raw, "Subject"),
				InReplyTo:  smtpHeader(raw, "In-Reply-To"),
				References: smtpHeader(raw, "References"),
				SizeBytes:  int64(len(raw)),
			}
			if authedUser == nil {
				// Unauthenticated connection (inbound from the internet via MX):
				// accept delivery only to existing local mailboxes. Anything else
				// is rejected so the server is never an open relay.
				relayDenied := ""
				for _, rcpt := range to {
					if e := s.delivery.IsLocal(contextBackground(), rcpt); e != nil {
						relayDenied = rcpt
						break
					}
				}
				if relayDenied != "" {
					write(fmt.Sprintf("550 relay not permitted: %s", relayDenied))
					from = ""
					to = nil
					continue
				}
			}

			var err error
			_, dataSpan := tracer.Start(ctx, "smtp.deliver")
			for _, rcpt := range to {
				if e := s.delivery.Deliver(contextBackground(), rcpt, msg); e != nil {
					err = e
				}
			}
			dataSpan.End()
			if err != nil {
				write("550 delivery failed")
			} else if authedUser != nil {
				if err := s.saveSent(ctx, authedUser, msg); err != nil {
					log.Printf("store sent copy: %v", err)
					write("451 could not save sent copy")
				} else {
					write("250 OK")
				}
			} else {
				write("250 OK queued")
			}

			// reset state
			from = ""
			to = nil

		case strings.ToUpper(line) == "QUIT":
			write("221 Bye")
			return

		default:
			write("502 Command not implemented")
		}
	}
}

func (s *SMTPServer) saveSent(ctx context.Context, user *User, submitted *Message) error {
	mailbox, err := s.delivery.mailboxes.GetByName(ctx, user.ID, "Sent")
	if err != nil {
		return fmt.Errorf("find Sent mailbox: %w", err)
	}

	copy := *submitted
	copy.ID = [16]byte{}
	copy.UID = 0
	copy.MailboxID = mailbox.ID
	copy.Recipients = append([]string(nil), submitted.Recipients...)
	copy.Seen = true
	copy.ReceivedAt = time.Now()
	return s.delivery.mail.Append(ctx, &copy)
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

func contextBackground() context.Context {
	return context.Background()
}
