package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
)

type IMAPServer struct {
	addr      string
	users     *Users
	mail      *Mail
	mboxes    *Mailboxes
	tlsConfig *tls.Config
	tracer    trace.Tracer
}

func NewIMAPServer(addr string, u *Users, m *Mail, mb *Mailboxes) *IMAPServer {
	return &IMAPServer{addr: addr, users: u, mail: m, mboxes: mb, tracer: otel.Tracer("imap")}
}

func NewIMAPTLSServer(addr string, u *Users, m *Mail, mb *Mailboxes, tlsCfg *tls.Config) *IMAPServer {
	return &IMAPServer{addr: addr, users: u, mail: m, mboxes: mb, tlsConfig: tlsCfg, tracer: otel.Tracer("imap")}
}

func (s *IMAPServer) ListenAndServe() error {
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
	log.Printf("imap listening on %s", s.addr)

	for {
		c, err := ln.Accept()
		if err != nil {
			log.Println("imap accept:", err)
			continue
		}
		go s.handle(c)
	}
}

// readIMAPCredentials runs the AUTHENTICATE exchange for the SASL mechanisms
// named in the server's capability list.
//
// Both mechanisms accept an initial response (RFC 4959 SASL-IR) and both must
// also work when the client waits for a continuation request. LOGIN is
// advertised, so it has to be implemented: a client that picks an advertised
// mechanism the server then rejects cannot authenticate at all.
func readIMAPCredentials(mechanism, initial string, r *bufio.Reader, write func(string)) (username, password string, ok bool) {
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

func (s *IMAPServer) handle(conn net.Conn) {
	defer conn.Close()

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	write := func(line string) {
		fmt.Fprintf(rw, "%s\r\n", line)
		rw.Flush()
	}

	// Greeting with capabilities (Apple Mail requires this)
	write("* OK [CAPABILITY IMAP4rev1 SASL-IR AUTH=PLAIN AUTH=LOGIN IDLE NAMESPACE UIDPLUS] ready")

	var authed *User
	var selected *Mailbox
	var selectedMsgs []Message

	// Proper OTEL context + session span
	ctx, sessionSpan := s.tracer.Start(context.Background(), "imap.session")
	defer sessionSpan.End()

	logWithTrace(ctx, slog.LevelInfo, "imap connection",
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

		// log every raw IMAP command for debugging
		logWithTrace(ctx, slog.LevelInfo, "imap raw recv",
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

		// Per-command span
		_, cmdSpan := s.tracer.Start(ctx, "imap.command")
		cmdSpan.SetAttributes(
			attribute.String("imap.command", cmd),
			attribute.String("imap.raw", line),
		)

		switch cmd {
		case "CAPABILITY":
			caps := "IMAP4rev1 SASL-IR AUTH=PLAIN AUTH=LOGIN IDLE NAMESPACE UIDPLUS"
			write("* CAPABILITY " + caps)
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

			logWithTrace(ctx, slog.LevelInfo, "imap login attempt",
				"user", user,
			)

			u, err := s.users.Authenticate(ctx, user, pass)
			incIMAPLogin(ctx, err == nil)
			if err != nil {
				logWithTrace(ctx, slog.LevelError, "imap auth failed",
					"user", user,
					"error", err,
				)
				write(tag + " NO auth failed")
				continue
			}

			logWithTrace(ctx, slog.LevelInfo, "imap auth success",
				"user", user,
			)

			authed = u
			_ = s.mboxes.EnsureDefaults(ctx, authed.ID)
			write(tag + " OK [CAPABILITY IMAP4rev1 SASL-IR AUTH=PLAIN AUTH=LOGIN IDLE NAMESPACE UIDPLUS] LOGIN completed")

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

			user, pass, ok := readIMAPCredentials(mech, initial, rw.Reader, write)
			if !ok {
				write(tag + " NO auth failed")
				continue
			}

			logWithTrace(ctx, slog.LevelInfo, "imap auth attempt", "mechanism", mech, "user", user)

			u, err := s.users.Authenticate(ctx, user, pass)
			incIMAPLogin(ctx, err == nil)
			if err != nil {
				logWithTrace(ctx, slog.LevelError, "imap auth failed", "mechanism", mech, "user", user, "error", err)
				write(tag + " NO auth failed")
				continue
			}

			authed = u
			_ = s.mboxes.EnsureDefaults(ctx, authed.ID)
			logWithTrace(ctx, slog.LevelInfo, "imap auth success", "mechanism", mech, "user", user)
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

			logWithTrace(ctx, slog.LevelInfo, "imap SELECT start",
				"mailbox_id", mb.ID.String(),
			)
			msgs, errList := s.mail.List(ctx, mb.ID, 1000, 0)
			if errList != nil {
				logWithTrace(ctx, slog.LevelError, "imap SELECT list error",
					"error", errList,
				)
			}
			selectedMsgs = msgs
			logWithTrace(ctx, slog.LevelInfo, "imap SELECT",
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
			logWithTrace(ctx, slog.LevelInfo, "imap SEARCH",
				"result_ids", strings.Join(ids, ","),
				"messages_count", len(selectedMsgs),
			)
			write("* SEARCH " + strings.Join(ids, " "))
			write(tag + " OK SEARCH completed")

		case "FETCH":
			logWithTrace(ctx, slog.LevelInfo, "imap FETCH entered", "args", args)
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

			logWithTrace(ctx, slog.LevelInfo, "imap FETCH start",
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
					logWithTrace(ctx, slog.LevelError, "imap FETCH get error", "error", err)
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

				logWithTrace(ctx, slog.LevelInfo, "imap FETCH message",
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
				logWithTrace(ctx, slog.LevelInfo, "imap UID SEARCH",
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

				logWithTrace(ctx, slog.LevelInfo, "imap FETCH start",
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
						logWithTrace(ctx, slog.LevelError, "imap FETCH get error",
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

					logWithTrace(ctx, slog.LevelInfo, "imap FETCH message",
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

				logWithTrace(ctx, slog.LevelInfo, "imap FETCH done")
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
				remaining := make([]Message, 0, len(selectedMsgs))
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

			var remaining []Message
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

func writeFetchLiteral(w *bufio.Writer, prefix, literal string) error {
	if _, err := fmt.Fprintf(w, "%s\r\n%s)\r\n", prefix, literal); err != nil {
		return err
	}
	return w.Flush()
}

func mailboxAttributes(name string) string {
	attrs := []string{"\\HasNoChildren"}
	switch strings.ToLower(name) {
	case "sent":
		attrs = append(attrs, "\\Sent")
	case "drafts":
		attrs = append(attrs, "\\Drafts")
	case "trash":
		attrs = append(attrs, "\\Trash")
	case "archive":
		attrs = append(attrs, "\\Archive")
	case "spam", "junk":
		attrs = append(attrs, "\\Junk")
	case "all":
		attrs = append(attrs, "\\All")
	case "important":
		attrs = append(attrs, "\\Important")
	}
	return strings.Join(attrs, " ")
}

func parseLiteralMarker(marker string) (int, bool) {
	marker = strings.TrimSpace(marker)
	if len(marker) < 3 || marker[0] != '{' || marker[len(marker)-1] != '}' {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(marker, "{"), "}"))
	return n, err == nil && n >= 0
}

func readLiteral(reader *bufio.Reader, size int) (string, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return "", err
	}
	return string(data), nil
}

func messageFromAppend(mailboxID uuid.UUID, raw, flags string) *Message {
	message := &Message{
		MailboxID:  mailboxID,
		MessageID:  smtpHeader(raw, "Message-ID"),
		Sender:     smtpHeader(raw, "From"),
		Subject:    smtpHeader(raw, "Subject"),
		InReplyTo:  smtpHeader(raw, "In-Reply-To"),
		References: smtpHeader(raw, "References"),
		RawMessage: raw,
		SizeBytes:  int64(len(raw)),
		ReceivedAt: time.Now(),
	}
	to := smtpHeader(raw, "To")
	if to != "" {
		message.Recipients = strings.Split(to, ",")
	}
	applyFlags(message, "FLAGS", strings.Trim(flags, "() "))
	return message
}

func (s *IMAPServer) copyOrMove(ctx context.Context, user *User, messages []Message, set, destination string, byUID, move bool) ([]Message, []int, error) {
	mailbox, err := s.mboxes.GetByName(ctx, user.ID, destination)
	if err != nil {
		return nil, nil, fmt.Errorf("no such mailbox")
	}
	maxUID := int(nextUID(messages) - 1)
	remaining := make([]Message, 0, len(messages))
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

func formatEnvelope(message *Message, date string) string {
	from := imapAddressList([]string{message.Sender})
	return fmt.Sprintf("(%s %s %s %s %s %s NIL NIL %s %s)",
		imapNString(date),
		imapNString(message.Subject),
		from,
		from,
		from,
		imapAddressList(message.Recipients),
		imapNString(message.InReplyTo),
		imapNString(message.MessageID),
	)
}

func imapAddressList(addresses []string) string {
	items := make([]string, 0, len(addresses))
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		local, domain, found := strings.Cut(strings.Trim(address, "<>"), "@")
		if !found || local == "" || domain == "" {
			items = append(items, fmt.Sprintf("(NIL NIL %s NIL)", imapNString(address)))
			continue
		}
		items = append(items, fmt.Sprintf("(NIL NIL %s %s)", imapNString(local), imapNString(domain)))
	}
	if len(items) == 0 {
		return "NIL"
	}
	return "(" + strings.Join(items, " ") + ")"
}

func imapNString(value string) string {
	if value == "" {
		return "NIL"
	}
	value = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\r", "", "\n", "").Replace(value)
	return "\"" + value + "\""
}

func parseSeq(seq string, max int) (int, int) {
	if seq == "" {
		return 1, max
	}

	if seq == "*" {
		return max, max
	}

	if strings.Contains(seq, ":") {
		parts := strings.Split(seq, ":")

		var start int
		if parts[0] == "*" {
			start = max
		} else {
			start, _ = strconv.Atoi(parts[0])
		}

		var end int
		if parts[1] == "*" {
			end = max
		} else {
			end, _ = strconv.Atoi(parts[1])
		}

		if start <= 0 {
			start = 1
		}
		if end <= 0 || end > max {
			end = max
		}
		if start > end {
			start, end = end, start
		}
		return start, end
	}

	// single number
	n, _ := strconv.Atoi(seq)
	if n <= 0 || n > max {
		return 1, max
	}
	return n, n
}

func messageSetContains(set string, n, max int) bool {
	for _, item := range strings.Split(set, ",") {
		bounds := strings.SplitN(item, ":", 2)
		start, end := 0, 0
		if bounds[0] == "*" {
			start = max
		} else {
			start, _ = strconv.Atoi(bounds[0])
		}
		if len(bounds) == 1 {
			end = start
		} else if bounds[1] == "*" {
			end = max
		} else {
			end, _ = strconv.Atoi(bounds[1])
		}
		if start > end {
			start, end = end, start
		}
		if n >= start && n <= end {
			return true
		}
	}
	return false
}

func nextUID(messages []Message) uint64 {
	var max uint64
	for _, message := range messages {
		if message.UID > max {
			max = message.UID
		}
	}
	return max + 1
}

func mailboxUpdates(previous, current []Message) []string {
	currentByUID := make(map[uint64]Message, len(current))
	for _, message := range current {
		currentByUID[message.UID] = message
	}
	updates := make([]string, 0)
	expunged := 0
	for sequence, message := range previous {
		if _, ok := currentByUID[message.UID]; !ok {
			updates = append(updates, fmt.Sprintf("* %d EXPUNGE", sequence+1-expunged))
			expunged++
		}
	}
	if len(previous) != len(current) {
		updates = append(updates, fmt.Sprintf("* %d EXISTS", len(current)))
	}
	previousByUID := make(map[uint64]Message, len(previous))
	for _, message := range previous {
		previousByUID[message.UID] = message
	}
	for sequence, message := range current {
		if before, ok := previousByUID[message.UID]; ok && flagsFor(&before) != flagsFor(&message) {
			updates = append(updates, fmt.Sprintf("* %d FETCH (FLAGS (%s) UID %d)", sequence+1, flagsFor(&message), message.UID))
		}
	}
	return updates
}

func flagsFor(m *Message) string {
	var f []string
	if m.Seen {
		f = append(f, "\\Seen")
	}
	if m.Flagged {
		f = append(f, "\\Flagged")
	}
	if m.Answered {
		f = append(f, "\\Answered")
	}
	if m.Deleted {
		f = append(f, "\\Deleted")
	}
	if m.Draft {
		f = append(f, "\\Draft")
	}
	return strings.Join(f, " ")
}

func countRecent(msgs []Message) int {
	// naive: treat unseen as recent
	c := 0
	for _, m := range msgs {
		if !m.Seen {
			c++
		}
	}
	return c
}

func uidFromID(id string) uint32 {
	var h uint32
	for i := 0; i < len(id); i++ {
		h = h*31 + uint32(id[i])
	}
	if h == 0 {
		return 1
	}
	return h
}

func applyFlags(m *Message, op string, flags string) {
	op = strings.TrimSuffix(strings.ToUpper(op), ".SILENT")
	set := strings.Split(flags, " ")

	apply := func(flag string, val bool) {
		switch flag {
		case "\\Seen":
			m.Seen = val
		case "\\Flagged":
			m.Flagged = val
		case "\\Answered":
			m.Answered = val
		case "\\Deleted":
			m.Deleted = val
		case "\\Draft":
			m.Draft = val
		}
	}

	switch op {
	case "FLAGS":
		// replace
		m.Seen = false
		m.Flagged = false
		m.Answered = false
		m.Deleted = false
		m.Draft = false
		for _, f := range set {
			apply(f, true)
		}

	case "+FLAGS":
		for _, f := range set {
			apply(f, true)
		}

	case "-FLAGS":
		for _, f := range set {
			apply(f, false)
		}
	}
}
