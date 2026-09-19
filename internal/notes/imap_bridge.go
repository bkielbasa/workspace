package notes

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bklimczak/workspace/internal/format/applenote"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

var uuidPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

type NotesBridge interface {
	SyncNoteToIMAP(ctx context.Context, user *identity.User, n *Note) error
	DeleteNoteFromIMAP(ctx context.Context, userID, noteID uuid.UUID) error
	HandleIMAPAppend(ctx context.Context, userID uuid.UUID, mailboxName, raw string) (*Note, error)
	HandleIMAPExpunge(ctx context.Context, userID uuid.UUID, mailboxName string, messageIDs []uuid.UUID) error
	EnsureNotesMailbox(ctx context.Context, userID uuid.UUID) (*mail.Mailbox, error)
	StartEventListener(ctx context.Context, userLookup func(uuid.UUID) (*identity.User, error))
}

type IMAPBridge struct {
	notesSvc *Service
	mail     mail.MessageRepository
	mboxes   mail.MailboxRepository

	mu       sync.Mutex
	suppress map[uuid.UUID]time.Time
}

func NewIMAPBridge(notesSvc *Service, mail mail.MessageRepository, mboxes mail.MailboxRepository) *IMAPBridge {
	return &IMAPBridge{
		notesSvc: notesSvc,
		mail:     mail,
		mboxes:   mboxes,
		suppress: make(map[uuid.UUID]time.Time),
	}
}

func (b *IMAPBridge) suppressNote(id uuid.UUID) {
	if id == uuid.Nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.suppress[id] = time.Now().Add(10 * time.Second)
}

func (b *IMAPBridge) isSuppressed(id uuid.UUID) bool {
	if id == uuid.Nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	exp, ok := b.suppress[id]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(b.suppress, id)
		return false
	}
	delete(b.suppress, id)
	return true
}

func (b *IMAPBridge) EnsureNotesMailbox(ctx context.Context, userID uuid.UUID) (*mail.Mailbox, error) {
	mb, err := b.mboxes.GetByName(ctx, userID, "Notes")
	if err == nil {
		return mb, nil
	}

	mbs, listErr := b.mboxes.List(ctx, userID)
	if listErr == nil {
		for _, m := range mbs {
			if strings.EqualFold(m.Name, "Notes") {
				return &m, nil
			}
		}
	}

	return b.mboxes.Create(ctx, userID, "Notes")
}

func (b *IMAPBridge) listAllMessages(ctx context.Context, mailboxID uuid.UUID) ([]mail.Message, error) {
	const pageSize = 500
	var all []mail.Message
	offset := 0
	for {
		msgs, err := b.mail.List(ctx, mailboxID, pageSize, offset)
		if err != nil {
			return nil, err
		}
		if len(msgs) == 0 {
			break
		}
		all = append(all, msgs...)
		if len(msgs) < pageSize {
			break
		}
		offset += len(msgs)
		if offset >= 10000 {
			break
		}
	}
	return all, nil
}

func messageMatchesNote(msg *mail.Message, noteID uuid.UUID) bool {
	idStr := noteID.String()
	if strings.Contains(msg.MessageID, idStr) {
		return true
	}
	if strings.Contains(msg.RawMessage, "X-Universally-Unique-Identifier: "+idStr) {
		return true
	}
	if strings.Contains(msg.RawMessage, "<"+idStr+"@") {
		return true
	}
	parsed, err := applenote.Parse(msg.RawMessage)
	if err == nil && parsed.ID == noteID {
		return true
	}
	return false
}

func extractUUIDFromRaw(raw string) uuid.UUID {
	matches := uuidPattern.FindAllString(raw, -1)
	for _, m := range matches {
		if id, err := uuid.Parse(m); err == nil && id != uuid.Nil {
			return id
		}
	}
	return uuid.Nil
}

func (b *IMAPBridge) SyncNoteToIMAP(ctx context.Context, user *identity.User, n *Note) error {
	if user == nil || n == nil {
		return errors.New("user and note cannot be nil")
	}

	notesBox, err := b.EnsureNotesMailbox(ctx, user.ID)
	if err != nil {
		return fmt.Errorf("ensure notes mailbox: %w", err)
	}

	existingMsgs, err := b.listAllMessages(ctx, notesBox.ID)
	if err != nil {
		return fmt.Errorf("list existing notes messages: %w", err)
	}

	for _, msg := range existingMsgs {
		if messageMatchesNote(&msg, n.ID) {
			_ = b.mail.Delete(ctx, msg.ID)
		}
	}

	raw := applenote.Format(n, user.Email)

	now := time.Now()
	if !n.UpdatedAt.IsZero() {
		now = n.UpdatedAt
	}

	newMsg := &mail.Message{
		MailboxID:  notesBox.ID,
		MessageID:  fmt.Sprintf("<%s@workspace.local>", n.ID),
		Sender:     user.Email,
		Recipients: []string{user.Email},
		Subject:    n.Title,
		RawMessage: raw,
		MimeType:   "text/plain",
		Charset:    "utf-8",
		SizeBytes:  int64(len(raw)),
		Seen:       true,
		ReceivedAt: now,
		SentAt:     &now,
	}

	return b.mail.Append(ctx, newMsg)
}

func (b *IMAPBridge) DeleteNoteFromIMAP(ctx context.Context, userID, noteID uuid.UUID) error {
	mb, err := b.mboxes.GetByName(ctx, userID, "Notes")
	if err != nil {
		if errors.Is(err, mail.ErrMailboxNotFound) {
			return nil
		}
		mbs, listErr := b.mboxes.List(ctx, userID)
		if listErr != nil {
			return nil
		}
		for _, m := range mbs {
			if strings.EqualFold(m.Name, "Notes") {
				mb = &m
				break
			}
		}
		if mb == nil {
			return nil
		}
	}

	existingMsgs, err := b.listAllMessages(ctx, mb.ID)
	if err != nil {
		return fmt.Errorf("list notes messages: %w", err)
	}

	for _, msg := range existingMsgs {
		if messageMatchesNote(&msg, noteID) {
			_ = b.mail.Delete(ctx, msg.ID)
		}
	}

	return nil
}

func (b *IMAPBridge) HandleIMAPAppend(ctx context.Context, userID uuid.UUID, mailboxName, raw string) (*Note, error) {
	isNotesMailbox := strings.EqualFold(mailboxName, "Notes")
	isNoteHeader := strings.Contains(strings.ToLower(raw), "x-uniform-type-identifier: com.apple.mail-note")
	if !isNotesMailbox && !isNoteHeader {
		return nil, nil
	}

	parsed, err := applenote.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse apple note: %w", err)
	}

	if !isNotesMailbox && !parsed.IsNote {
		return nil, nil
	}

	noteID := parsed.ID
	if noteID == uuid.Nil {
		noteID = extractUUIDFromRaw(raw)
		if noteID == uuid.Nil {
			noteID = uuid.New()
		}
	}

	title := parsed.Title
	if title == "" {
		title = "Untitled Note"
	}

	b.suppressNote(noteID)

	existing, err := b.notesSvc.GetNote(ctx, userID, noteID)
	if err == nil && existing != nil {
		updated := *existing
		updated.Title = title
		updated.Body = parsed.Body
		if !parsed.Date.IsZero() {
			updated.UpdatedAt = parsed.Date
		}
		return b.notesSvc.UpdateNote(ctx, userID, false, updated)
	}

	if errors.Is(err, ErrNotFound) || existing == nil {
		newNote := Note{
			ID:        noteID,
			UserID:    userID,
			Title:     title,
			Body:      parsed.Body,
			Kind:      KindNote,
			CreatedAt: parsed.Date,
			UpdatedAt: parsed.Date,
		}
		return b.notesSvc.CreateNote(ctx, userID, newNote)
	}

	return nil, err
}

func (b *IMAPBridge) HandleIMAPExpunge(ctx context.Context, userID uuid.UUID, mailboxName string, messageIDs []uuid.UUID) error {
	if !strings.EqualFold(mailboxName, "Notes") {
		return nil
	}

	for _, id := range messageIDs {
		var targetNoteID uuid.UUID
		msg, err := b.mail.Get(ctx, id)
		if err == nil && msg != nil {
			parsed, parseErr := applenote.Parse(msg.RawMessage)
			if parseErr == nil && parsed.ID != uuid.Nil {
				targetNoteID = parsed.ID
			} else {
				targetNoteID = extractUUIDFromRaw(msg.RawMessage)
			}
		}

		if targetNoteID == uuid.Nil {
			targetNoteID = id
		}

		b.suppressNote(targetNoteID)
		err = b.notesSvc.DeleteNote(ctx, userID, false, targetNoteID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			// Ignore ErrNotFound (already deleted)
		}
	}

	return nil
}

func (b *IMAPBridge) StartEventListener(ctx context.Context, userLookup func(uuid.UUID) (*identity.User, error)) {
	events := b.notesSvc.Broker().Subscribe()
	defer b.notesSvc.Broker().Unsubscribe(events)

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if b.isSuppressed(ev.NoteID) {
				continue
			}

			user, err := userLookup(ev.UserID)
			if err != nil || user == nil {
				continue
			}

			switch ev.Type {
			case "note_created", "note_updated":
				n, err := b.notesSvc.GetNote(ctx, ev.UserID, ev.NoteID)
				if err != nil || n == nil {
					continue
				}
				if n.Kind == KindNote || n.Kind == "" {
					_ = b.SyncNoteToIMAP(ctx, user, n)
				}
			case "note_deleted":
				_ = b.DeleteNoteFromIMAP(ctx, ev.UserID, ev.NoteID)
			}
		}
	}
}
