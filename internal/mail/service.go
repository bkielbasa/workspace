package mail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"sort"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type deliverer interface {
	Deliver(ctx context.Context, recipient string, message *Message) error
}

// Service unifies mailboxes, messages, search, and delivery into a domain service.
type Service struct {
	mailboxes MailboxRepository
	messages  MessageRepository
	search    SearchRepository
	delivery  deliverer
	hostname  string
	tracer    trace.Tracer
}

func NewService(
	mailboxes MailboxRepository,
	messages MessageRepository,
	search SearchRepository,
	delivery deliverer,
	hostname string,
) *Service {
	return &Service{
		mailboxes: mailboxes,
		messages:  messages,
		search:    search,
		delivery:  delivery,
		hostname:  hostname,
		tracer:    otel.Tracer("mail.service"),
	}
}

func (s *Service) EnsureDefaultMailboxes(ctx context.Context, userID uuid.UUID) error {
	ctx, span := s.tracer.Start(ctx, "mail.ensure_default_mailboxes")
	defer span.End()
	return s.mailboxes.CreateDefault(ctx, userID)
}

var mailboxOrder = map[string]int{
	"INBOX":     1,
	"Sent":      2,
	"Drafts":    3,
	"Archive":   4,
	"Trash":     5,
	"Spam":      6,
	"Important": 7,
	"All":       8,
}

func (s *Service) ListMailboxes(ctx context.Context, userID uuid.UUID) ([]MailboxInfo, error) {
	ctx, span := s.tracer.Start(ctx, "mail.list_mailboxes")
	defer span.End()

	boxes, err := s.mailboxes.ListWithCounts(ctx, userID)
	if err != nil {
		return nil, err
	}

	sort.SliceStable(boxes, func(i, j int) bool {
		rI, okI := mailboxOrder[boxes[i].Name]
		if !okI {
			rI = 100
		}
		rJ, okJ := mailboxOrder[boxes[j].Name]
		if !okJ {
			rJ = 100
		}
		if rI != rJ {
			return rI < rJ
		}
		return boxes[i].Name < boxes[j].Name
	})

	return boxes, nil
}

func (s *Service) GetMailbox(ctx context.Context, userID uuid.UUID, name string) (*Mailbox, error) {
	ctx, span := s.tracer.Start(ctx, "mail.get_mailbox")
	defer span.End()
	return s.mailboxes.GetByName(ctx, userID, name)
}

func (s *Service) ListMessages(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]Message, error) {
	ctx, span := s.tracer.Start(ctx, "mail.list_messages")
	defer span.End()
	return s.messages.List(ctx, mailboxID, limit, offset)
}

func (s *Service) SearchMessages(ctx context.Context, userID uuid.UUID, query string) ([]Message, error) {
	ctx, span := s.tracer.Start(ctx, "mail.search_messages")
	defer span.End()
	if s.search == nil {
		return nil, nil
	}
	return s.search.Messages(ctx, userID, query)
}

func (s *Service) GetMessage(ctx context.Context, userID, messageID uuid.UUID) (*Message, *Mailbox, error) {
	ctx, span := s.tracer.Start(ctx, "mail.get_message")
	defer span.End()
	return s.messages.GetForUser(ctx, userID, messageID)
}

func (s *Service) UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error {
	ctx, span := s.tracer.Start(ctx, "mail.update_flags")
	defer span.End()
	return s.messages.UpdateFlags(ctx, id, seen, flagged, answered, deleted, draft)
}

func (s *Service) DeleteMessage(ctx context.Context, userID, messageID uuid.UUID) error {
	ctx, span := s.tracer.Start(ctx, "mail.delete_message")
	defer span.End()

	msg, mb, err := s.messages.GetForUser(ctx, userID, messageID)
	if err != nil {
		return err
	}

	if strings.EqualFold(mb.Name, "Trash") {
		return s.messages.Delete(ctx, msg.ID)
	}

	trashBox, err := s.mailboxes.GetByName(ctx, userID, "Trash")
	if err != nil {
		return s.messages.Delete(ctx, msg.ID)
	}
	return s.messages.Move(ctx, msg.ID, trashBox.ID)
}

func (s *Service) SendMessage(ctx context.Context, user *identity.User, to, subject, body string) (*Message, error) {
	return s.SendMessageWithAttachments(ctx, user, to, subject, body, nil)
}

// SendInvite sends an iTIP invitation (METHOD:REQUEST or METHOD:CANCEL)
// with a text/calendar part alongside a plain-text summary. Recipients'
// mail clients render this as a meeting invitation.
func (s *Service) SendInvite(ctx context.Context, user *identity.User, to, subject, body, icsData, icsMethod string) (*Message, error) {
	ctx, span := s.tracer.Start(ctx, "mail.send_invite")
	defer span.End()

	to = strings.TrimSpace(to)
	if to == "" {
		return nil, errors.New("recipient required")
	}
	method := strings.ToUpper(strings.TrimSpace(icsMethod))
	if method == "" {
		method = "REQUEST"
	}

	hostname := s.hostname
	if hostname == "" {
		hostname = "mail.local"
	}
	now := time.Now()
	msgID := fmt.Sprintf("<%d@%s>", now.UnixNano(), hostname)

	var buf bytes.Buffer
	buf.WriteString("From: " + sanitizeHeaderValue(user.Email) + "\r\n")
	buf.WriteString("To: " + sanitizeHeaderValue(to) + "\r\n")
	buf.WriteString("Subject: " + sanitizeHeaderValue(subject) + "\r\n")
	buf.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	buf.WriteString("Message-ID: " + msgID + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")

	boundary := mimeBoundary()
	buf.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n")
	buf.WriteString("\r\n")

	w := multipart.NewWriter(&buf)
	_ = w.SetBoundary(boundary)

	text, _ := w.CreatePart(map[string][]string{
		"Content-Type":              {"text/plain; charset=UTF-8"},
		"Content-Transfer-Encoding": {"8bit"},
	})
	_, _ = io.WriteString(text, body)

	cal, _ := w.CreatePart(map[string][]string{
		"Content-Type":              {fmt.Sprintf("text/calendar; charset=UTF-8; method=%s; name=\"invite.ics\"", method)},
		"Content-Transfer-Encoding": {"base64"},
		"Content-Disposition":       {"attachment; filename=\"invite.ics\""},
	})
	_, _ = writeBase64Lines(cal, []byte(icsData))
	_ = w.Close()
	raw := buf.String()

	msg := &Message{
		MessageID:  msgID,
		Sender:     user.Email,
		Recipients: []string{to},
		Subject:    sanitizeHeaderValue(subject),
		RawMessage: raw,
		MimeType:   "multipart/mixed",
		Charset:    "UTF-8",
		SizeBytes:  int64(len(raw)),
		ReceivedAt: now,
		SentAt:     &now,
		Seen:       true,
	}

	if s.delivery != nil {
		if err := s.delivery.Deliver(ctx, to, msg); err != nil {
			obs.Log(ctx, slog.LevelWarn, "mail delivery error", "error", err)
		}
	}

	sentBox, err := s.mailboxes.GetByName(ctx, user.ID, "Sent")
	if err == nil && sentBox != nil {
		sentCopy := *msg
		sentCopy.ID = uuid.Nil
		sentCopy.UID = 0
		sentCopy.MailboxID = sentBox.ID
		sentCopy.Seen = true
		sentCopy.ReceivedAt = now
		_ = s.messages.Append(ctx, &sentCopy)
		return &sentCopy, nil
	}

	return msg, nil
}

// SendMessageWithAttachments sends a message, building a multipart/mixed
// body when files are attached. Limits from the upload policy apply.
func (s *Service) SendMessageWithAttachments(ctx context.Context, user *identity.User, to, subject, body string, atts []Attachment) (*Message, error) {
	ctx, span := s.tracer.Start(ctx, "mail.send_message")
	defer span.End()

	to = strings.TrimSpace(to)
	if to == "" {
		return nil, errors.New("recipient required")
	}
	if len(atts) > MaxAttachmentCount {
		return nil, fmt.Errorf("too many attachments (max %d)", MaxAttachmentCount)
	}
	var total int64
	for i := range atts {
		atts[i].Filename = sanitizeFilename(atts[i].Filename)
		if atts[i].Filename == "" {
			atts[i].Filename = "attachment"
		}
		if strings.TrimSpace(atts[i].ContentType) == "" {
			atts[i].ContentType = "application/octet-stream"
		}
		if int64(len(atts[i].Data)) > MaxAttachmentBytes {
			return nil, fmt.Errorf("attachment %q exceeds %d MB", atts[i].Filename, MaxAttachmentBytes>>20)
		}
		total += int64(len(atts[i].Data))
	}
	if total > MaxAttachmentsTotalBytes {
		return nil, fmt.Errorf("attachments exceed %d MB total", MaxAttachmentsTotalBytes>>20)
	}

	hostname := s.hostname
	if hostname == "" {
		hostname = "mail.local"
	}

	now := time.Now()
	msgID := fmt.Sprintf("<%d@%s>", now.UnixNano(), hostname)
	raw, mimeType := buildRawMessage(user.Email, to, subject, hostname, msgID, now, body, atts)

	msg := &Message{
		MessageID:  msgID,
		Sender:     user.Email,
		Recipients: []string{to},
		Subject:    sanitizeHeaderValue(subject),
		RawMessage: raw,
		MimeType:   mimeType,
		Charset:    "UTF-8",
		SizeBytes:  int64(len(raw)),
		ReceivedAt: now,
		SentAt:     &now,
		Seen:       true,
	}

	if s.delivery != nil {
		if err := s.delivery.Deliver(ctx, to, msg); err != nil {
			obs.Log(ctx, slog.LevelWarn, "mail delivery error", "error", err)
		}
	}

	sentBox, err := s.mailboxes.GetByName(ctx, user.ID, "Sent")
	if err == nil && sentBox != nil {
		sentCopy := *msg
		sentCopy.ID = uuid.Nil
		sentCopy.UID = 0
		sentCopy.MailboxID = sentBox.ID
		sentCopy.Seen = true
		sentCopy.ReceivedAt = now
		_ = s.messages.Append(ctx, &sentCopy)
		return &sentCopy, nil
	}

	return msg, nil
}
