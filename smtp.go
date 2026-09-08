package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log"
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
}

func NewSMTPServer(addr string, d *Delivery, u *Users) *SMTPServer {
	return &SMTPServer{addr: addr, delivery: d, users: u}
}

func NewSMTPTLSServer(addr string, d *Delivery, u *Users, tlsCfg *tls.Config) *SMTPServer {
	return &SMTPServer{addr: addr, delivery: d, users: u, tlsConfig: tlsCfg}
}

func (s *SMTPServer) ListenAndServe() error {
	var ln net.Listener
	var err error
	if s.tlsConfig != nil {
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

	write("220 mail.local ESMTP ready")

	tlsEnabled := false

	var from string
	var to []string
	var authedUser *User

	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(strings.ToUpper(line), "EHLO"):
			write("250-mail.local")
			write("250-PIPELINING")
			write("250-8BITMIME")
			write("250-AUTH LOGIN PLAIN")
			if !tlsEnabled {
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

			write("220 Ready to start TLS")

			// NOTE: self-signed / placeholder cert; replace in production
			cert, err := tls.X509KeyPair(localCert, localKey)
			if err != nil {
				log.Println("tls cert error:", err)
				return
			}

			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
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

		case strings.HasPrefix(strings.ToUpper(line), "AUTH PLAIN"):
			// AUTH PLAIN base64(\0user\0pass)
			parts := strings.Split(line, " ")
			if len(parts) < 3 {
				write("501 invalid auth")
				continue
			}
			decoded, err := base64Decode(parts[2])
			if err != nil {
				write("501 invalid base64")
				continue
			}
			seg := strings.Split(decoded, "\x00")
			if len(seg) < 3 {
				write("501 invalid auth format")
				continue
			}
			email := seg[1]
			pass := seg[2]

			user, err := s.users.Authenticate(contextBackground(), email, pass)
			incSMTPAuth(contextBackground(), err == nil)
			if err != nil {
				write("535 auth failed")
				continue
			}
			authedUser = user
			write("235 authenticated")

		case strings.HasPrefix(strings.ToUpper(line), "AUTH LOGIN"):
			write("334 VXNlcm5hbWU6") // "Username:" base64

			uline, _ := rw.ReadString('\n')
			username, _ := base64Decode(strings.TrimSpace(uline))

			write("334 UGFzc3dvcmQ6") // "Password:"
			pline, _ := rw.ReadString('\n')
			password, _ := base64Decode(strings.TrimSpace(pline))

			user, err := s.users.Authenticate(contextBackground(), username, password)
			incSMTPAuth(contextBackground(), err == nil)
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

var localCert = []byte(`-----BEGIN CERTIFICATE-----
MIIB...fake
-----END CERTIFICATE-----`)

var localKey = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIB...fake
-----END RSA PRIVATE KEY-----`)
