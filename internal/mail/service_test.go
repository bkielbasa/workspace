package mail_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

type mockMailboxRepo struct {
	boxes []mail.MailboxInfo
}

func (m *mockMailboxRepo) Create(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	return nil, nil
}

func (m *mockMailboxRepo) CreateDefault(ctx context.Context, userID uuid.UUID) error {
	return nil
}

func (m *mockMailboxRepo) List(ctx context.Context, userID uuid.UUID) ([]mail.Mailbox, error) {
	return nil, nil
}

func (m *mockMailboxRepo) ListWithCounts(ctx context.Context, userID uuid.UUID) ([]mail.MailboxInfo, error) {
	return m.boxes, nil
}

func (m *mockMailboxRepo) GetByName(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	for _, b := range m.boxes {
		if strings.EqualFold(b.Name, name) {
			return &mail.Mailbox{ID: b.ID, UserID: b.UserID, Name: b.Name}, nil
		}
	}
	return nil, mail.ErrMailboxNotFound
}

func (m *mockMailboxRepo) Get(ctx context.Context, id uuid.UUID) (*mail.Mailbox, error) {
	for _, b := range m.boxes {
		if b.ID == id {
			return &mail.Mailbox{ID: b.ID, UserID: b.UserID, Name: b.Name}, nil
		}
	}
	return nil, mail.ErrMailboxNotFound
}

func (m *mockMailboxRepo) Delete(ctx context.Context, id uuid.UUID) error {
	return nil
}

type mockMessageRepo struct {
	messages []mail.Message
	boxes    map[uuid.UUID]string
	deleted  []uuid.UUID
}

func (m *mockMessageRepo) Append(ctx context.Context, message *mail.Message) error {
	if message.ID == uuid.Nil {
		message.ID = uuid.New()
	}
	m.messages = append(m.messages, *message)
	return nil
}

func (m *mockMessageRepo) Get(ctx context.Context, id uuid.UUID) (*mail.Message, error) {
	for _, msg := range m.messages {
		if msg.ID == id {
			return &msg, nil
		}
	}
	return nil, mail.ErrMessageNotFound
}

func (m *mockMessageRepo) GetForUser(ctx context.Context, userID, id uuid.UUID) (*mail.Message, *mail.Mailbox, error) {
	for _, msg := range m.messages {
		if msg.ID == id {
			boxName := m.boxes[msg.MailboxID]
			if boxName == "" {
				boxName = "INBOX"
			}
			return &msg, &mail.Mailbox{ID: msg.MailboxID, UserID: userID, Name: boxName}, nil
		}
	}
	return nil, nil, mail.ErrMessageNotFound
}

func (m *mockMessageRepo) List(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error) {
	var result []mail.Message
	for _, msg := range m.messages {
		if msg.MailboxID == mailboxID {
			result = append(result, msg)
		}
	}
	return result, nil
}

func (m *mockMessageRepo) ListUIDs(ctx context.Context, mailboxID uuid.UUID) ([]uint32, error) {
	return nil, nil
}

func (m *mockMessageRepo) GetByUID(ctx context.Context, mailboxID uuid.UUID, uid uint32) (*mail.Message, error) {
	return nil, nil
}

func (m *mockMessageRepo) UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error {
	for i, msg := range m.messages {
		if msg.ID == id {
			m.messages[i].Seen = seen
			m.messages[i].Flagged = flagged
			m.messages[i].Answered = answered
			m.messages[i].Deleted = deleted
			m.messages[i].Draft = draft
			return nil
		}
	}
	return mail.ErrMessageNotFound
}

func (m *mockMessageRepo) Move(ctx context.Context, messageID, targetMailboxID uuid.UUID) error {
	for i, msg := range m.messages {
		if msg.ID == messageID {
			m.messages[i].MailboxID = targetMailboxID
			return nil
		}
	}
	return mail.ErrMessageNotFound
}

func (m *mockMessageRepo) Delete(ctx context.Context, id uuid.UUID) error {
	m.deleted = append(m.deleted, id)
	return nil
}

type mockDeliverer struct {
	deliveredTo string
	deliveredMsg *mail.Message
}

func (d *mockDeliverer) Deliver(ctx context.Context, recipient string, message *mail.Message) error {
	d.deliveredTo = recipient
	d.deliveredMsg = message
	return nil
}

func TestMailServiceListMailboxesOrder(t *testing.T) {
	userID := uuid.New()
	mbRepo := &mockMailboxRepo{
		boxes: []mail.MailboxInfo{
			{ID: uuid.New(), UserID: userID, Name: "Trash"},
			{ID: uuid.New(), UserID: userID, Name: "INBOX"},
			{ID: uuid.New(), UserID: userID, Name: "Sent"},
			{ID: uuid.New(), UserID: userID, Name: "Archive"},
			{ID: uuid.New(), UserID: userID, Name: "CustomFolder"},
		},
	}
	svc := mail.NewService(mbRepo, &mockMessageRepo{}, nil, nil, "mail.test")
	ordered, err := svc.ListMailboxes(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}

	expectedOrder := []string{"INBOX", "Sent", "Archive", "Trash", "CustomFolder"}
	for i, exp := range expectedOrder {
		if ordered[i].Name != exp {
			t.Errorf("at index %d expected %q, got %q", i, exp, ordered[i].Name)
		}
	}
}

func TestMailServiceSendMessage(t *testing.T) {
	userID := uuid.New()
	sentBoxID := uuid.New()
	mbRepo := &mockMailboxRepo{
		boxes: []mail.MailboxInfo{
			{ID: sentBoxID, UserID: userID, Name: "Sent"},
		},
	}
	msgRepo := &mockMessageRepo{boxes: map[uuid.UUID]string{sentBoxID: "Sent"}}
	deliverer := &mockDeliverer{}
	svc := mail.NewService(mbRepo, msgRepo, nil, deliverer, "mail.test")

	user := &identity.User{
		ID:    userID,
		Email: "alice@mail.test",
	}

	msg, err := svc.SendMessage(context.Background(), user, "bob@example.com", "Test Subject", "Test Body")
	if err != nil {
		t.Fatal(err)
	}

	if deliverer.deliveredTo != "bob@example.com" {
		t.Errorf("expected delivered to bob@example.com, got %q", deliverer.deliveredTo)
	}
	if msg.Subject != "Test Subject" {
		t.Errorf("expected subject 'Test Subject', got %q", msg.Subject)
	}
	if len(msgRepo.messages) != 1 {
		t.Fatalf("expected 1 message appended to Sent box, got %d", len(msgRepo.messages))
	}
	if msgRepo.messages[0].MailboxID != sentBoxID {
		t.Errorf("expected sent message saved in Sent box")
	}
}

func TestMailServiceDeleteMessage(t *testing.T) {
	userID := uuid.New()
	inboxID := uuid.New()
	trashID := uuid.New()
	msg1ID := uuid.New()
	msg2ID := uuid.New()

	mbRepo := &mockMailboxRepo{
		boxes: []mail.MailboxInfo{
			{ID: inboxID, UserID: userID, Name: "INBOX"},
			{ID: trashID, UserID: userID, Name: "Trash"},
		},
	}
	msgRepo := &mockMessageRepo{
		messages: []mail.Message{
			{ID: msg1ID, MailboxID: inboxID, Subject: "Inbox Msg"},
			{ID: msg2ID, MailboxID: trashID, Subject: "Trash Msg"},
		},
		boxes: map[uuid.UUID]string{
			inboxID: "INBOX",
			trashID: "Trash",
		},
	}
	svc := mail.NewService(mbRepo, msgRepo, nil, nil, "mail.test")

	// Delete from INBOX moves to Trash
	if err := svc.DeleteMessage(context.Background(), userID, msg1ID); err != nil {
		t.Fatal(err)
	}
	if msgRepo.messages[0].MailboxID != trashID {
		t.Errorf("expected msg1 moved to Trash, mailbox_id is %v", msgRepo.messages[0].MailboxID)
	}

	// Delete from Trash permanently deletes
	if err := svc.DeleteMessage(context.Background(), userID, msg2ID); err != nil {
		t.Fatal(err)
	}
	if len(msgRepo.deleted) != 1 || msgRepo.deleted[0] != msg2ID {
		t.Errorf("expected msg2 permanently deleted")
	}
}
