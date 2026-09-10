package smtp

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	stdmail "net/mail"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Authenticator verifies the credentials presented by a submitting client.
type Authenticator interface {
	Authenticate(ctx context.Context, email, password string) (*identity.User, error)
}

// Delivery routes an accepted message to a recipient. IsLocal reports whether
// a recipient has a local mailbox, which gates relaying for unauthenticated
// connections.
type Delivery interface {
	IsLocal(ctx context.Context, recipient string) error
	Deliver(ctx context.Context, recipient string, message *mail.Message) error
}

// Mailboxes resolves a user's mailbox by name, used to find Sent.
type Mailboxes interface {
	GetByName(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error)
}

// Messages stores a message, used to keep the submitter's copy of what it sent.
type Messages interface {
	Append(ctx context.Context, message *mail.Message) error
}

type Server struct {
	addr      string
	hostname  string
	delivery  Delivery
	users     Authenticator
	mailboxes Mailboxes
	messages  Messages
	tlsConfig *tls.Config
	wrapped   bool
	tracer    trace.Tracer
}

// NewServer returns a server for the submission/MX port, where TLS is reached
// through STARTTLS. hostname is announced in the banner and EHLO reply and
// must be the resolvable FQDN of this host.
func NewServer(addr, hostname string, d Delivery, u Authenticator, mailboxes Mailboxes, messages Messages, tlsCfg *tls.Config) *Server {
	return newServer(addr, hostname, d, u, mailboxes, messages, tlsCfg, false)
}

// NewTLSServer returns a server for the implicit-TLS port, where the
// connection is already encrypted before the first command.
func NewTLSServer(addr, hostname string, d Delivery, u Authenticator, mailboxes Mailboxes, messages Messages, tlsCfg *tls.Config) *Server {
	return newServer(addr, hostname, d, u, mailboxes, messages, tlsCfg, true)
}

func newServer(addr, hostname string, d Delivery, u Authenticator, mailboxes Mailboxes, messages Messages, tlsCfg *tls.Config, wrapped bool) *Server {
	return &Server{
		addr:      addr,
		hostname:  hostname,
		delivery:  d,
		users:     u,
		mailboxes: mailboxes,
		messages:  messages,
		tlsConfig: tlsCfg,
		wrapped:   wrapped,
		tracer:    otel.Tracer("smtp"),
	}
}

func (s *Server) ListenAndServe() error {
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
	obs.Log(context.Background(), slog.LevelInfo, "smtp listening", "addr", s.addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			obs.Log(context.Background(), slog.LevelError, "smtp accept error", "error", err)
			continue
		}
		go s.handleConn(conn)
	}
}

func newReadWriter(conn net.Conn) *bufio.ReadWriter {
	return bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	// basic timeouts
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))

	ctx, span := s.tracer.Start(context.Background(), "smtp.session")
	defer span.End()

	rw := newReadWriter(conn)

	write := func(msg string) {
		fmt.Fprintf(rw, "%s\r\n", msg)
		rw.Flush()
	}

	write("220 " + s.hostname + " ESMTP ready")

	// On the implicit-TLS listener the connection is already encrypted, so the
	// session must start in the TLS state: otherwise STARTTLS is advertised
	// inside TLS and a client taking us up on it would negotiate a nested
	// handshake.
	tlsEnabled := s.wrapped

	obs.Log(ctx, slog.LevelInfo, "smtp connection",
		"remote", conn.RemoteAddr().String(),
		"local", conn.LocalAddr().String(),
		"implicit_tls", s.wrapped,
	)

	var from string
	var to []string
	var authedUser *identity.User

	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)

		// Log the command verb only; AUTH lines and message data carry
		// credentials and message content.
		if verb := commandVerb(line); verb != "" {
			obs.Log(ctx, slog.LevelInfo, "smtp cmd",
				"verb", verb,
				"remote", conn.RemoteAddr().String(),
				"tls", tlsEnabled,
			)
		}

		switch {
		case strings.HasPrefix(strings.ToUpper(line), "EHLO"):
			write("250-" + s.hostname)
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
				obs.Log(ctx, slog.LevelError, "tls handshake error", "error", err)
				return
			}

			// swap connection + reader/writer
			conn = tlsConn
			rw = newReadWriter(conn)
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

			user, err := s.users.Authenticate(ctx, username, password)
			obs.SMTPAuth(ctx, err == nil)
			obs.Log(ctx, slog.LevelInfo, "smtp auth attempt",
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
			raw := prepareMessage(data.String(), from, strings.Join(to, ", "), s.hostname)
			msg := &mail.Message{
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
					if e := s.delivery.IsLocal(ctx, rcpt); e != nil {
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
			deliverCtx, dataSpan := s.tracer.Start(ctx, "smtp.deliver")
			for _, rcpt := range to {
				if e := s.delivery.Deliver(deliverCtx, rcpt, msg); e != nil {
					err = e
				}
			}
			dataSpan.End()
			if err != nil {
				write("550 delivery failed")
			} else if authedUser != nil {
				if err := s.saveSent(deliverCtx, authedUser, msg); err != nil {
					obs.Log(deliverCtx, slog.LevelError, "store sent copy", "error", err)
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

func (s *Server) saveSent(ctx context.Context, user *identity.User, submitted *mail.Message) error {
	ctx, span := s.tracer.Start(ctx, "smtp.save_sent")
	defer span.End()

	mailbox, err := s.mailboxes.GetByName(ctx, user.ID, "Sent")
	if err != nil {
		return fmt.Errorf("find Sent mailbox: %w", err)
	}

	copy := *submitted
	copy.ID = uuid.Nil
	copy.UID = 0
	copy.MailboxID = mailbox.ID
	copy.Recipients = append([]string(nil), submitted.Recipients...)
	copy.Seen = true
	copy.ReceivedAt = time.Now()
	return s.messages.Append(ctx, &copy)
}
