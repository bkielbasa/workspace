package mail

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

// Define mock structures for testing

type mockUserLookup struct {
	user *identity.User
}

func (m *mockUserLookup) GetByEmail(ctx context.Context, email string) (*identity.User, error) {
	if m.user != nil && m.user.Email == email {
		return m.user, nil
	}
	return nil, errors.New("user not found")
}

type mockMailboxRepository struct {
	mailboxes     []Mailbox
	failOnCreate  bool
	createInvoked bool
}

func (m *mockMailboxRepository) CreateDefault(ctx context.Context, userID uuid.UUID) error {
	return nil
}

func (m *mockMailboxRepository) List(ctx context.Context, userID uuid.UUID) ([]Mailbox, error) {
	return m.mailboxes, nil
}

func (m *mockMailboxRepository) ListWithCounts(ctx context.Context, userID uuid.UUID) ([]MailboxInfo, error) {
	return nil, nil
}

func (m *mockMailboxRepository) GetByName(ctx context.Context, userID uuid.UUID, name string) (*Mailbox, error) {
	for _, mb := range m.mailboxes {
		if strings.EqualFold(mb.Name, name) {
			return &mb, nil
		}
	}
	return nil, ErrMailboxNotFound
}

func (m *mockMailboxRepository) Create(ctx context.Context, userID uuid.UUID, name string) (*Mailbox, error) {
	m.createInvoked = true
	if m.failOnCreate {
		return nil, errors.New("database error on create")
	}
	mb := Mailbox{
		ID:     uuid.New(),
		UserID: userID,
		Name:   name,
	}
	m.mailboxes = append(m.mailboxes, mb)
	return &mb, nil
}

type mockMessageRepository struct {
	messages []Message
}

func (m *mockMessageRepository) Append(ctx context.Context, message *Message) error {
	if message.ID == uuid.Nil {
		message.ID = uuid.New()
	}
	m.messages = append(m.messages, *message)
	return nil
}

func (m *mockMessageRepository) Get(ctx context.Context, id uuid.UUID) (*Message, error) {
	for _, msg := range m.messages {
		if msg.ID == id {
			return &msg, nil
		}
	}
	return nil, ErrMessageNotFound
}

func (m *mockMessageRepository) GetForUser(ctx context.Context, userID, id uuid.UUID) (*Message, *Mailbox, error) {
	return nil, nil, nil
}

func (m *mockMessageRepository) List(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]Message, error) {
	var msgs []Message
	for _, msg := range m.messages {
		if msg.MailboxID == mailboxID {
			msgs = append(msgs, msg)
		}
	}
	return msgs, nil
}

func (m *mockMessageRepository) ListSummary(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]Message, error) {
	return m.List(ctx, mailboxID, limit, offset)
}

func (m *mockMessageRepository) UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error {
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
	return ErrMessageNotFound
}

func (m *mockMessageRepository) Delete(ctx context.Context, id uuid.UUID) error {
	for i, msg := range m.messages {
		if msg.ID == id {
			m.messages = append(m.messages[:i], m.messages[i+1:]...)
			return nil
		}
	}
	return ErrMessageNotFound
}

func (m *mockMessageRepository) Move(ctx context.Context, id, mailboxID uuid.UUID) error {
	for i, msg := range m.messages {
		if msg.ID == id {
			m.messages[i].MailboxID = mailboxID
			return nil
		}
	}
	return ErrMessageNotFound
}

type mockRuleRepository struct {
	rules []Rule
}

func (m *mockRuleRepository) Create(ctx context.Context, rule *Rule) error {
	return nil
}

func (m *mockRuleRepository) GetByID(ctx context.Context, userID, id uuid.UUID) (*Rule, error) {
	return nil, nil
}

func (m *mockRuleRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]Rule, error) {
	return m.rules, nil
}

func (m *mockRuleRepository) ListEnabled(ctx context.Context, userID uuid.UUID) ([]Rule, error) {
	var enabled []Rule
	for _, r := range m.rules {
		if r.Enabled && r.UserID == userID {
			enabled = append(enabled, r)
		}
	}
	return enabled, nil
}

func (m *mockRuleRepository) Update(ctx context.Context, rule *Rule) error {
	return nil
}

func (m *mockRuleRepository) SetEnabled(ctx context.Context, userID uuid.UUID, id uuid.UUID, enabled bool) error {
	return nil
}

func (m *mockRuleRepository) Reorder(ctx context.Context, userID uuid.UUID, orderedIDs []uuid.UUID) error {
	return nil
}

func (m *mockRuleRepository) Delete(ctx context.Context, userID uuid.UUID, id uuid.UUID) error {
	return nil
}

// Tests

func TestDelivery_RuleMoveAndMarkRead(t *testing.T) {
	userID := uuid.New()
	user := &identity.User{
		ID:    userID,
		Email: "recipient@example.com",
	}

	inboxID := uuid.New()
	mbRepo := &mockMailboxRepository{
		mailboxes: []Mailbox{
			{ID: inboxID, UserID: userID, Name: "INBOX"},
		},
	}
	mailboxes := NewMailboxes(mbRepo)

	msgRepo := &mockMessageRepository{}
	mailMessages := NewMail(msgRepo)

	ruleRepo := &mockRuleRepository{
		rules: []Rule{
			{
				ID:        uuid.New(),
				UserID:    userID,
				Name:      "Move Alert to Custom Folder",
				Enabled:   true,
				Priority:  1,
				MatchMode: "all",
				Conditions: []RuleCondition{
					{
						Field:    RuleFieldSubject,
						Operator: RuleOperatorContains,
						Value:    "Alert",
					},
				},
				Actions: []RuleAction{
					{
						Type:   RuleActionMoveToFolder,
						Target: "Alerts",
					},
					{
						Type: RuleActionMarkRead,
					},
				},
			},
		},
	}

	delivery := NewDelivery(&mockUserLookup{user: user}, mailboxes, mailMessages, nil, nil, nil, "localhost")
	delivery.SetRules(ruleRepo)

	msg := &Message{
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		Subject:    "Important Alert!",
		RawMessage: "Subject: Important Alert!\r\n\r\nThis is an alert body.",
	}

	err := delivery.Deliver(context.Background(), "recipient@example.com", msg)
	if err != nil {
		t.Fatalf("Deliver returned unexpected error: %v", err)
	}

	if len(msgRepo.messages) != 1 {
		t.Fatalf("expected exactly 1 message delivered, got %d", len(msgRepo.messages))
	}

	delivered := msgRepo.messages[0]

	// Verify it was marked read
	if !delivered.Seen {
		t.Error("expected delivered message to be marked read")
	}

	// Verify custom folder was created and matched
	var customBox *Mailbox
	for _, mb := range mbRepo.mailboxes {
		if mb.Name == "Alerts" {
			customBox = &mb
			break
		}
	}

	if customBox == nil {
		t.Fatal("expected 'Alerts' custom mailbox to be created")
	}

	if delivered.MailboxID != customBox.ID {
		t.Errorf("expected message to be delivered to %s (ID %v), but was in %v", customBox.Name, customBox.ID, delivered.MailboxID)
	}
}

func TestDelivery_FallbackToInbox(t *testing.T) {
	userID := uuid.New()
	user := &identity.User{
		ID:    userID,
		Email: "recipient@example.com",
	}

	inboxID := uuid.New()
	mbRepo := &mockMailboxRepository{
		mailboxes: []Mailbox{
			{ID: inboxID, UserID: userID, Name: "INBOX"},
		},
		failOnCreate: true, // Forces creation of custom folder to fail
	}
	mailboxes := NewMailboxes(mbRepo)

	msgRepo := &mockMessageRepository{}
	mailMessages := NewMail(msgRepo)

	ruleRepo := &mockRuleRepository{
		rules: []Rule{
			{
				ID:        uuid.New(),
				UserID:    userID,
				Name:      "Move Alert to Custom Folder",
				Enabled:   true,
				Priority:  1,
				MatchMode: "all",
				Conditions: []RuleCondition{
					{
						Field:    RuleFieldSubject,
						Operator: RuleOperatorContains,
						Value:    "Alert",
					},
				},
				Actions: []RuleAction{
					{
						Type:   RuleActionMoveToFolder,
						Target: "Alerts",
					},
				},
			},
		},
	}

	delivery := NewDelivery(&mockUserLookup{user: user}, mailboxes, mailMessages, nil, nil, nil, "localhost")
	delivery.SetRules(ruleRepo)

	msg := &Message{
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		Subject:    "Important Alert!",
		RawMessage: "Subject: Important Alert!\r\n\r\nThis is an alert body.",
	}

	err := delivery.Deliver(context.Background(), "recipient@example.com", msg)
	if err != nil {
		t.Fatalf("Deliver returned unexpected error: %v", err)
	}

	if len(msgRepo.messages) != 1 {
		t.Fatalf("expected exactly 1 message delivered, got %d", len(msgRepo.messages))
	}

	delivered := msgRepo.messages[0]

	// Verify it fell back to INBOX
	if delivered.MailboxID != inboxID {
		t.Errorf("expected message to fallback to INBOX (ID %v), but got %v", inboxID, delivered.MailboxID)
	}
}

func TestService_ApplyRulesToInbox(t *testing.T) {
	userID := uuid.New()
	inboxID := uuid.New()
	trashID := uuid.New()

	mbRepo := &mockMailboxRepository{
		mailboxes: []Mailbox{
			{ID: inboxID, UserID: userID, Name: "INBOX"},
			{ID: trashID, UserID: userID, Name: "Trash"},
		},
	}

	msg1ID := uuid.New()
	msg2ID := uuid.New()
	msg3ID := uuid.New()

	msgRepo := &mockMessageRepository{
		messages: []Message{
			{
				ID:         msg1ID,
				MailboxID:  inboxID,
				Sender:     "spammer@example.com",
				Subject:    "SPAM offer",
				RawMessage: "Subject: SPAM offer\r\n\r\nBuy custom stuff now!",
			},
			{
				ID:         msg2ID,
				MailboxID:  inboxID,
				Sender:     "friend@example.com",
				Subject:    "Regular text",
				RawMessage: "Subject: Regular text\r\n\r\nHey how are you?",
			},
			{
				ID:         msg3ID,
				MailboxID:  inboxID,
				Sender:     "boss@example.com",
				Subject:    "Project update",
				RawMessage: "Subject: Project update\r\n\r\nStatus is fine.",
			},
		},
	}

	ruleRepo := &mockRuleRepository{
		rules: []Rule{
			{
				ID:        uuid.New(),
				UserID:    userID,
				Name:      "Spam to Trash",
				Enabled:   true,
				Priority:  1,
				MatchMode: "all",
				Conditions: []RuleCondition{
					{
						Field:    RuleFieldSubject,
						Operator: RuleOperatorContains,
						Value:    "SPAM",
					},
				},
				Actions: []RuleAction{
					{
						Type: RuleActionMoveToTrash,
					},
				},
			},
			{
				ID:        uuid.New(),
				UserID:    userID,
				Name:      "Star Regular Friend",
				Enabled:   true,
				Priority:  2,
				MatchMode: "all",
				Conditions: []RuleCondition{
					{
						Field:    RuleFieldFrom,
						Operator: RuleOperatorContains,
						Value:    "friend",
					},
				},
				Actions: []RuleAction{
					{
						Type: RuleActionStar,
					},
				},
			},
		},
	}

	svc := NewService(mbRepo, msgRepo, nil, nil, "localhost")
	svc.SetRules(ruleRepo)

	count, err := svc.ApplyRulesToInbox(context.Background(), userID)
	if err != nil {
		t.Fatalf("ApplyRulesToInbox returned unexpected error: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 affected messages, got %d", count)
	}

	// Check msg1 (SPAM) was moved to Trash
	msg1, err := msgRepo.Get(context.Background(), msg1ID)
	if err != nil {
		t.Fatal(err)
	}
	if msg1.MailboxID != trashID {
		t.Errorf("expected spam message to be in Trash, but was in %v", msg1.MailboxID)
	}

	// Check msg2 (Friend) was starred
	msg2, err := msgRepo.Get(context.Background(), msg2ID)
	if err != nil {
		t.Fatal(err)
	}
	if !msg2.Flagged {
		t.Error("expected message from friend to be starred")
	}

	// Check msg3 (Boss) remains unchanged (in Inbox and not starred/seen)
	msg3, err := msgRepo.Get(context.Background(), msg3ID)
	if err != nil {
		t.Fatal(err)
	}
	if msg3.MailboxID != inboxID {
		t.Errorf("expected boss message to remain in INBOX, but was in %v", msg3.MailboxID)
	}
	if msg3.Flagged {
		t.Error("expected boss message to not be starred")
	}
}

func TestApplyRulesToInbox_Discard(t *testing.T) {
	userID := uuid.New()
	inboxID := uuid.New()

	mbRepo := &mockMailboxRepository{
		mailboxes: []Mailbox{
			{ID: inboxID, UserID: userID, Name: "INBOX"},
		},
	}

	msgID := uuid.New()
	msgRepo := &mockMessageRepository{
		messages: []Message{
			{
				ID:         msgID,
				MailboxID:  inboxID,
				Sender:     "spammer@example.com",
				Subject:    "DELETE ME",
				RawMessage: "Subject: DELETE ME\r\n\r\nThis is spam.",
			},
		},
	}

	ruleRepo := &mockRuleRepository{
		rules: []Rule{
			{
				ID:        uuid.New(),
				UserID:    userID,
				Name:      "Spam Delete",
				Enabled:   true,
				Priority:  1,
				MatchMode: "all",
				Conditions: []RuleCondition{
					{
						Field:    RuleFieldSubject,
						Operator: RuleOperatorContains,
						Value:    "DELETE ME",
					},
				},
				Actions: []RuleAction{
					{
						Type: RuleActionDelete,
					},
				},
			},
		},
	}

	svc := NewService(mbRepo, msgRepo, nil, nil, "localhost")
	svc.SetRules(ruleRepo)

	count, err := svc.ApplyRulesToInbox(context.Background(), userID)
	if err != nil {
		t.Fatalf("ApplyRulesToInbox returned error: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 affected message, got %d", count)
	}

	// Verify message was deleted from repository
	_, err = msgRepo.Get(context.Background(), msgID)
	if err == nil {
		t.Error("expected message to be deleted, but it was found in msgRepo")
	}
}
