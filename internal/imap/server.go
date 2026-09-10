package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const capabilities = "IMAP4rev1 SASL-IR AUTH=PLAIN AUTH=LOGIN IDLE NAMESPACE UIDPLUS"

// Authenticator verifies the credentials supplied by LOGIN and AUTHENTICATE.
type Authenticator interface {
	Authenticate(ctx context.Context, email, password string) (*identity.User, error)
}

// MailboxStore is the mailbox access this server needs.
type MailboxStore interface {
	List(ctx context.Context, userID uuid.UUID) ([]mail.Mailbox, error)
	GetByName(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error)
	Create(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error)
	EnsureDefaults(ctx context.Context, userID uuid.UUID) error
}

// MessageStore is the message access this server needs.
type MessageStore interface {
	Append(ctx context.Context, message *mail.Message) error
	Get(ctx context.Context, id uuid.UUID) (*mail.Message, error)
	List(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error)
	UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error
	Delete(ctx context.Context, id uuid.UUID) error
	Move(ctx context.Context, id, mailboxID uuid.UUID) error
	Copy(ctx context.Context, id, mailboxID uuid.UUID) (*mail.Message, error)
}

type Server struct {
	addr      string
	users     Authenticator
	mail      MessageStore
	mboxes    MailboxStore
	tlsConfig *tls.Config
	tracer    trace.Tracer
}

func NewServer(addr string, users Authenticator, messages MessageStore, mailboxes MailboxStore) *Server {
	return &Server{addr: addr, users: users, mail: messages, mboxes: mailboxes, tracer: otel.Tracer("imap")}
}

func NewTLSServer(addr string, users Authenticator, messages MessageStore, mailboxes MailboxStore, tlsCfg *tls.Config) *Server {
	return &Server{addr: addr, users: users, mail: messages, mboxes: mailboxes, tlsConfig: tlsCfg, tracer: otel.Tracer("imap")}
}

func (s *Server) ListenAndServe() error {
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
	obs.Log(context.Background(), slog.LevelInfo, "imap listening", "addr", s.addr)

	for {
		c, err := ln.Accept()
		if err != nil {
			obs.Log(context.Background(), slog.LevelError, "imap accept error", "error", err)
			continue
		}
		go s.handle(c)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	write := func(line string) {
		fmt.Fprintf(rw, "%s\r\n", line)
		rw.Flush()
	}

	// Greeting with capabilities (Apple Mail requires this)
	write("* OK [CAPABILITY " + capabilities + "] ready")

	var authed *identity.User
	var selected *mail.Mailbox
	var selectedMsgs []mail.Message

	ctx, sessionSpan := s.tracer.Start(context.Background(), "imap.session")
	defer sessionSpan.End()

	obs.Log(ctx, slog.LevelInfo, "imap connection",
		"remote", conn.RemoteAddr().String(),
		"local", conn.LocalAddr().String(),
		"implicit_tls", s.tlsConfig != nil,
	)

	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		rawLine := line
		line = strings.TrimSpace(line)

		obs.Log(ctx, slog.LevelInfo, "imap raw recv",
			"raw", strings.TrimSpace(rawLine),
		)

		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		tag := parts[0]
		cmd := strings.ToUpper(parts[1])
		var args []string
		if len(parts) > 2 {
			args = parts[2:]
		}

		_, cmdSpan := s.tracer.Start(ctx, "imap.command")
		cmdSpan.SetAttributes(
			attribute.String("imap.command", cmd),
			attribute.String("imap.raw", line),
		)

		switch cmd {
		case "CAPABILITY":
			write("* CAPABILITY " + capabilities)
			write(tag + " OK CAPABILITY completed")

		case "STARTTLS":
			// This server exposes separate cleartext and implicit-TLS ports;
			// neither listener is an RFC 3501 STARTTLS endpoint.
			write(tag + " NO STARTTLS not available")
			continue

		case "NOOP":
			write(tag + " OK NOOP completed")

		case "ID":
			write("* ID (\"name\" \"workspace\" \"version\" \"1.0\")")
			write(tag + " OK ID completed")

		case "NAMESPACE":
			write("* NAMESPACE ((\"\" \"/\")) NIL NIL")
			write(tag + " OK NAMESPACE completed")

		case "LIST", "LSUB":
			if authed == nil {
				write(tag + " NO not authenticated")
				continue
			}
			mailboxes, err := s.mboxes.List(ctx, authed.ID)
			if err != nil {
				write(tag + " NO could not list mailboxes")
				continue
			}
			for _, mailbox := range mailboxes {
				write(fmt.Sprintf("* %s (%s) \"/\" %s", cmd, mailboxAttributes(mailbox.Name), imapNString(mailbox.Name)))
			}
			write(tag + " OK " + cmd + " completed")

		case "CREATE":
			if authed == nil {
				write(tag + " NO not authenticated")
				continue
			}
			if len(parts) < 3 {
				write(tag + " BAD")
				continue
			}
			name := strings.Trim(parts[2], "\"")
			_, err := s.mboxes.Create(ctx, authed.ID, name)
			if err != nil {
				write(tag + " NO cannot create mailbox")
				continue
			}
			write(tag + " OK CREATE completed")

		case "LOGIN":
			if len(parts) < 4 {
				write(tag + " BAD")
				continue
			}
			user := strings.TrimSpace(strings.Trim(parts[2], "\""))
			pass := strings.TrimSpace(strings.Trim(parts[3], "\""))

			obs.Log(ctx, slog.LevelInfo, "imap login attempt",
				"user", user,
			)

			u, err := s.users.Authenticate(ctx, user, pass)
			obs.IMAPLogin(ctx, err == nil)
			if err != nil {
				obs.Log(ctx, slog.LevelError, "imap auth failed",
					"user", user,
					"error", err,
				)
				write(tag + " NO auth failed")
				continue
			}

			obs.Log(ctx, slog.LevelInfo, "imap auth success",
				"user", user,
			)

			authed = u
			_ = s.mboxes.EnsureDefaults(ctx, authed.ID)
			write(tag + " OK [CAPABILITY " + capabilities + "] LOGIN completed")

		case "AUTHENTICATE":
			if len(parts) < 3 {
				write(tag + " BAD")
				continue
			}

			mech := strings.ToUpper(parts[2])
			initial := ""
			if len(parts) >= 4 {
				initial = parts[3]
			}

			user, pass, ok := readCredentials(mech, initial, rw.Reader, write)
			if !ok {
				write(tag + " NO auth failed")
				continue
			}

			obs.Log(ctx, slog.LevelInfo, "imap auth attempt", "mechanism", mech, "user", user)

			u, err := s.users.Authenticate(ctx, user, pass)
			obs.IMAPLogin(ctx, err == nil)
			if err != nil {
				obs.Log(ctx, slog.LevelError, "imap auth failed", "mechanism", mech, "user", user, "error", err)
				write(tag + " NO auth failed")
				continue
			}

			authed = u
			_ = s.mboxes.EnsureDefaults(ctx, authed.ID)
			obs.Log(ctx, slog.LevelInfo, "imap auth success", "mechanism", mech, "user", user)
			write(tag + " OK AUTHENTICATE completed")

		case "SELECT":
			if authed == nil {
				write(tag + " NO not authenticated")
				continue
			}
			name := "INBOX"
			if len(parts) >= 3 {
				name = strings.Trim(parts[2], "\"")
			}

			mb, err := s.mboxes.GetByName(ctx, authed.ID, name)
			if err != nil {
				write(tag + " NO no such mailbox")
				continue
			}
			selected = mb

			obs.Log(ctx, slog.LevelInfo, "imap SELECT start",
				"mailbox_id", mb.ID.String(),
			)
			msgs, errList := s.mail.List(ctx, mb.ID, 1000, 0)
			if errList != nil {
				obs.Log(ctx, slog.LevelError, "imap SELECT list error",
					"error", errList,
				)
			}
			selectedMsgs = msgs
			obs.Log(ctx, slog.LevelInfo, "imap SELECT",
				"mailbox", name,
				"messages_count", len(selectedMsgs),
			)
			// Strict, minimal SELECT response for client compatibility
			write("* FLAGS (\\Seen \\Answered \\Flagged \\Deleted \\Draft)")
			write("* OK [PERMANENTFLAGS (\\Seen \\Answered \\Flagged \\Deleted \\Draft)]")

			write(fmt.Sprintf("* %d EXISTS", len(selectedMsgs)))
			write(fmt.Sprintf("* %d RECENT", countRecent(selectedMsgs)))

			// first unseen (optional but helps clients)
			unseen := 0
			for i, m := range selectedMsgs {
				full, err := s.mail.Get(ctx, m.ID)
				if err == nil && !full.Seen {
					unseen = i + 1
					break
				}
			}
			if unseen > 0 {
				write(fmt.Sprintf("* OK [UNSEEN %d]", unseen))
			}

			// UID metadata (no extra text)
			write(fmt.Sprintf("* OK [UIDVALIDITY %d]", selected.UIDValidity))
			write(fmt.Sprintf("* OK [UIDNEXT %d]", nextUID(selectedMsgs)))

			write(tag + " OK [READ-WRITE] SELECT completed")

		case "APPEND":
			if authed == nil {
				write(tag + " NO not authenticated")
				continue
			}
			if len(parts) < 4 {
				write(tag + " BAD invalid APPEND")
				continue
			}
			mailboxName := strings.Trim(parts[2], "\"")
			mailbox, err := s.mboxes.GetByName(ctx, authed.ID, mailboxName)
			if err != nil {
				write(tag + " NO no such mailbox")
				continue
			}
			literalSize, ok := parseLiteralMarker(parts[len(parts)-1])
			if !ok {
				write(tag + " BAD APPEND requires a literal")
				continue
			}
			write("+ Ready for literal data")
			raw, err := readLiteral(rw.Reader, literalSize)
			if err != nil {
				return
			}
			flags := strings.Join(parts[3:len(parts)-1], " ")
			message := messageFromAppend(mailbox.ID, raw, flags)
			if err := s.mail.Append(ctx, message); err != nil {
				write(tag + " NO could not append message")
				continue
			}
			write(fmt.Sprintf("%s OK [APPENDUID %d %d] APPEND completed", tag, mailbox.UIDValidity, message.UID))

		case "SEARCH":
			if selected == nil {
				write(tag + " NO no mailbox selected")
				continue
			}
			// minimal: return all sequence numbers
			var ids []string
			for i := range selectedMsgs {
				ids = append(ids, fmt.Sprintf("%d", i+1))
			}
			obs.Log(ctx, slog.LevelInfo, "imap SEARCH",
				"result_ids", strings.Join(ids, ","),
				"messages_count", len(selectedMsgs),
			)
			write("* SEARCH " + strings.Join(ids, " "))
			write(tag + " OK SEARCH completed")

		case "FETCH":
			obs.Log(ctx, slog.LevelInfo, "imap FETCH entered", "args", args)
			if authed == nil {
				write(tag + " NO not authenticated")
				continue
			}
			if selected == nil {
				write(tag + " NO no mailbox selected")
				continue
			}

			if len(selectedMsgs) == 0 {
				write(tag + " OK FETCH completed")
				continue
			}

			// determine requested sequence range
			seq := "1:*"
			if len(args) >= 1 {
				seq = args[0]
			}

			start, end := parseSeq(seq, len(selectedMsgs))

			obs.Log(ctx, slog.LevelInfo, "imap FETCH start",
				"range", seq,
				"start", start,
				"end", end,
			)

			// detect header-only request
			reqStr := strings.ToUpper(strings.Join(args, " "))
			wantHeader := strings.Contains(reqStr, "BODY.PEEK[HEADER]") || strings.Contains(reqStr, "BODY[HEADER]")

			for i := start; i <= end; i++ {
				if i <= 0 || i > len(selectedMsgs) {
					continue
				}

				m := selectedMsgs[i-1]
				full, err := s.mail.Get(ctx, m.ID)
				if err != nil {
					obs.Log(ctx, slog.LevelError, "imap FETCH get error", "error", err)
					continue
				}

				flags := flagsFor(full)
				raw := full.RawMessage

				date := full.ReceivedAt.Format("2-Jan-2006 15:04:05 -0700")
				size := len(raw)

				envelope := formatEnvelope(full, date)

				body := raw
				bodyLabel := "BODY[]"
				if wantHeader {
					parts := strings.SplitN(raw, "\r\n\r\n", 2)
					body = parts[0] + "\r\n\r\n"
					bodyLabel = "BODY[HEADER]"
				}

				// minimal BODYSTRUCTURE (single-part text/plain fallback)
				// ("TEXT" "PLAIN" ("CHARSET" "UTF-8") NIL NIL "7BIT" <size> <lines>)
				lines := strings.Count(body, "\n")
				bodyStruct := fmt.Sprintf("(\"TEXT\" \"PLAIN\" (\"CHARSET\" \"UTF-8\") NIL NIL \"7BIT\" %d %d)", size, lines)

				uid := fmt.Sprintf("%d", full.UID)

				obs.Log(ctx, slog.LevelInfo, "imap FETCH message",
					"seq", i,
					"uid", uid,
					"header_only", wantHeader,
				)

				// A literal is followed by exactly its declared bytes.  The
				// remainder of the FETCH response follows immediately after
				// those bytes, with one final CRLF after the closing paren.
				_ = writeFetchLiteral(rw.Writer, fmt.Sprintf(
					"* %d FETCH (UID %s FLAGS (%s) INTERNALDATE \"%s\" RFC822.SIZE %d ENVELOPE %s BODYSTRUCTURE %s %s {%d}",
					i,
					uid,
					flags,
					date,
					size,
					envelope,
					bodyStruct,
					bodyLabel,
					len(body),
				), body)
			}

			write(tag + " OK FETCH completed")

		case "UID":
			if len(parts) < 3 {
				write(tag + " BAD UID")
				continue
			}
			sub := strings.ToUpper(parts[2])
			if sub == "SEARCH" {
				if selected == nil {
					write(tag + " NO no mailbox selected")
					continue
				}
				var ids []string
				for _, message := range selectedMsgs {
					ids = append(ids, fmt.Sprintf("%d", message.UID))
				}
				obs.Log(ctx, slog.LevelInfo, "imap UID SEARCH",
					"result_ids", strings.Join(ids, ","),
					"messages_count", len(selectedMsgs),
				)
				write("* SEARCH " + strings.Join(ids, " "))
				write(tag + " OK UID SEARCH completed")
				continue
			}
			if sub == "FETCH" {
				if selected == nil {
					write(tag + " NO no mailbox selected")
					continue
				}

				if len(parts) < 4 {
					write(tag + " BAD UID FETCH")
					continue
				}

				uidArg := parts[3]

				obs.Log(ctx, slog.LevelInfo, "imap FETCH start",
					"requested", parts,
					"messages", len(selectedMsgs),
				)

				// detect if client requested headers only
				req := strings.ToUpper(strings.Join(parts, " "))
				wantHeader := strings.Contains(req, "BODY.PEEK[HEADER]") || strings.Contains(req, "BODY[HEADER]")

				maxUID := int(nextUID(selectedMsgs) - 1)
				for idx, m := range selectedMsgs {
					uid := fmt.Sprintf("%d", m.UID)
					if !messageSetContains(uidArg, int(m.UID), maxUID) {
						continue
					}

					full, err := s.mail.Get(ctx, m.ID)
					if err != nil {
						obs.Log(ctx, slog.LevelError, "imap FETCH get error",
							"msg_id", m.ID.String(),
							"error", err,
						)
						continue
					}

					flags := flagsFor(full)
					raw := full.RawMessage

					seq := idx + 1
					date := full.ReceivedAt.Format("2-Jan-2006 15:04:05 -0700")
					size := len(raw)

					envelope := formatEnvelope(full, date)

					// choose correct body section
					body := raw
					bodyLabel := "BODY[]"
					if wantHeader {
						parts := strings.SplitN(raw, "\r\n\r\n", 2)
						body = parts[0] + "\r\n\r\n"
						bodyLabel = "BODY[HEADER]"
					}

					obs.Log(ctx, slog.LevelInfo, "imap FETCH message",
						"seq", seq,
						"uid", uid,
						"size", size,
						"header_only", wantHeader,
					)

					_ = writeFetchLiteral(rw.Writer, fmt.Sprintf(
						"* %d FETCH (UID %s FLAGS (%s) INTERNALDATE \"%s\" RFC822.SIZE %d ENVELOPE %s %s {%d}",
						seq,
						uid,
						flags,
						date,
						size,
						envelope,
						bodyLabel,
						len(body),
					), body)
				}

				obs.Log(ctx, slog.LevelInfo, "imap FETCH done")
				write(tag + " OK UID FETCH completed")
				continue
			}
			if sub == "COPY" || sub == "MOVE" {
				if selected == nil || authed == nil || len(parts) < 5 {
					write(tag + " BAD invalid UID " + sub)
					continue
				}
				updated, expunged, err := s.copyOrMove(ctx, authed, selectedMsgs, parts[3], strings.Trim(parts[4], "\""), true, sub == "MOVE")
				if err != nil {
					write(tag + " NO " + err.Error())
					continue
				}
				if sub == "MOVE" {
					for offset, seq := range expunged {
						write(fmt.Sprintf("* %d EXPUNGE", seq-offset))
					}
					selectedMsgs = updated
				}
				write(tag + " OK UID " + sub + " completed")
				continue
			}
			if sub == "EXPUNGE" {
				if selected == nil || len(parts) < 4 {
					write(tag + " BAD invalid UID EXPUNGE")
					continue
				}
				remaining := make([]mail.Message, 0, len(selectedMsgs))
				expunged := 0
				maxUID := int(nextUID(selectedMsgs) - 1)
				for seq, message := range selectedMsgs {
					full, err := s.mail.Get(ctx, message.ID)
					if err == nil && full.Deleted && messageSetContains(parts[3], int(message.UID), maxUID) {
						if s.mail.Delete(ctx, message.ID) == nil {
							write(fmt.Sprintf("* %d EXPUNGE", seq+1-expunged))
							expunged++
							continue
						}
					}
					remaining = append(remaining, message)
				}
				selectedMsgs = remaining
				write(tag + " OK UID EXPUNGE completed")
				continue
			}
			if sub == "STORE" {
				if selected == nil {
					write(tag + " NO no mailbox selected")
					continue
				}
				if len(parts) < 6 {
					write(tag + " BAD invalid UID STORE")
					continue
				}

				uidSet := parts[3]
				op := parts[4]
				flagsArg := strings.Trim(strings.Join(parts[5:], " "), "()")
				silent := strings.HasSuffix(strings.ToUpper(op), ".SILENT")

				maxUID := int(nextUID(selectedMsgs) - 1)
				for idx, msg := range selectedMsgs {
					if !messageSetContains(uidSet, int(msg.UID), maxUID) {
						continue
					}
					full, err := s.mail.Get(ctx, msg.ID)
					if err != nil {
						continue
					}
					applyFlags(full, op, flagsArg)
					if err := s.mail.UpdateFlags(ctx, full.ID, full.Seen, full.Flagged, full.Answered, full.Deleted, full.Draft); err != nil {
						write(tag + " NO could not update flags")
						continue
					}
					if !silent {
						write(fmt.Sprintf("* %d FETCH (FLAGS (%s) UID %d)", idx+1, flagsFor(full), full.UID))
					}
				}
				write(tag + " OK UID STORE completed")
				continue
			}
			write(tag + " BAD unsupported UID command")

		case "STORE":
			if authed == nil {
				write(tag + " NO not authenticated")
				continue
			}
			if selected == nil {
				write(tag + " NO no mailbox selected")
				continue
			}
			if len(parts) < 5 {
				write(tag + " BAD invalid STORE")
				continue
			}

			seq := parts[2]
			op := parts[3] // FLAGS / +FLAGS / -FLAGS, optionally .SILENT
			flagsArg := strings.Join(parts[4:], " ")
			flagsArg = strings.Trim(flagsArg, "()")
			silent := strings.HasSuffix(strings.ToUpper(op), ".SILENT")

			start, end := parseSeq(seq, len(selectedMsgs))

			for i := start; i <= end; i++ {
				msg := selectedMsgs[i-1]
				full, err := s.mail.Get(ctx, msg.ID)
				if err != nil {
					continue
				}

				applyFlags(full, op, flagsArg)

				if err := s.mail.UpdateFlags(
					ctx,
					full.ID,
					full.Seen,
					full.Flagged,
					full.Answered,
					full.Deleted,
					full.Draft,
				); err != nil {
					write(tag + " NO could not update flags")
					continue
				}

				if !silent {
					write(fmt.Sprintf("* %d FETCH (FLAGS (%s) UID %d)", i, flagsFor(full), full.UID))
				}
			}

			write(tag + " OK STORE completed")

		case "COPY", "MOVE":
			if authed == nil || selected == nil || len(parts) < 4 {
				write(tag + " BAD invalid " + cmd)
				continue
			}
			updated, expunged, err := s.copyOrMove(ctx, authed, selectedMsgs, parts[2], strings.Trim(parts[3], "\""), false, cmd == "MOVE")
			if err != nil {
				write(tag + " NO " + err.Error())
				continue
			}
			if cmd == "MOVE" {
				for offset, seq := range expunged {
					write(fmt.Sprintf("* %d EXPUNGE", seq-offset))
				}
				selectedMsgs = updated
			}
			write(tag + " OK " + cmd + " completed")

		case "EXPUNGE":
			if authed == nil || selected == nil {
				write(tag + " NO not ready")
				continue
			}

			var remaining []mail.Message
			seqNum := 1

			for _, m := range selectedMsgs {
				full, err := s.mail.Get(ctx, m.ID)
				if err != nil {
					continue
				}

				if full.Deleted {
					_ = s.mail.Delete(ctx, full.ID)
					write(fmt.Sprintf("* %d EXPUNGE", seqNum))
					continue
				}

				remaining = append(remaining, m)
				seqNum++
			}

			selectedMsgs = remaining
			write(tag + " OK EXPUNGE completed")

		case "CLOSE":
			if authed == nil || selected == nil {
				write(tag + " NO no mailbox selected")
				continue
			}
			for _, message := range selectedMsgs {
				full, err := s.mail.Get(ctx, message.ID)
				if err == nil && full.Deleted {
					_ = s.mail.Delete(ctx, message.ID)
				}
			}
			selected = nil
			selectedMsgs = nil
			write(tag + " OK CLOSE completed")

		case "LOGOUT":
			write("* BYE")
			write(tag + " OK LOGOUT completed")
			return

		case "IDLE":
			write("+ idling")
			for {
				_ = conn.SetReadDeadline(time.Now().Add(time.Second))
				line, err := rw.ReadString('\n')
				if err == nil {
					_ = conn.SetReadDeadline(time.Time{})
					if strings.EqualFold(strings.TrimSpace(line), "DONE") {
						write(tag + " OK IDLE completed")
					} else {
						write(tag + " BAD expected DONE while idling")
					}
					break
				}
				if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
					return
				}
				if selected == nil {
					continue
				}
				updated, listErr := s.mail.List(ctx, selected.ID, 1000, 0)
				if listErr != nil {
					continue
				}
				for _, update := range mailboxUpdates(selectedMsgs, updated) {
					write(update)
				}
				selectedMsgs = updated
			}

		default:
			write(tag + " BAD unsupported")
		}

		cmdSpan.End()
	}
}

func (s *Server) copyOrMove(ctx context.Context, user *identity.User, messages []mail.Message, set, destination string, byUID, move bool) ([]mail.Message, []int, error) {
	mailbox, err := s.mboxes.GetByName(ctx, user.ID, destination)
	if err != nil {
		return nil, nil, fmt.Errorf("no such mailbox")
	}
	maxUID := int(nextUID(messages) - 1)
	remaining := make([]mail.Message, 0, len(messages))
	expunged := make([]int, 0)
	for index, message := range messages {
		value, max := index+1, len(messages)
		if byUID {
			value, max = int(message.UID), maxUID
		}
		if !messageSetContains(set, value, max) {
			remaining = append(remaining, message)
			continue
		}
		if move {
			if err := s.mail.Move(ctx, message.ID, mailbox.ID); err != nil {
				return nil, nil, err
			}
			expunged = append(expunged, index+1)
			continue
		}
		if _, err := s.mail.Copy(ctx, message.ID, mailbox.ID); err != nil {
			return nil, nil, err
		}
		remaining = append(remaining, message)
	}
	return remaining, expunged, nil
}
