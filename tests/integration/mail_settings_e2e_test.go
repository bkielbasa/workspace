package integration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

// memSignatureRepo implements mail.SignatureRepository in-memory for testing.
type memSignatureRepo struct {
	mu   sync.Mutex
	sigs map[uuid.UUID]*mail.Signature
}

func newMemSignatureRepo() *memSignatureRepo {
	return &memSignatureRepo{sigs: make(map[uuid.UUID]*mail.Signature)}
}

func (m *memSignatureRepo) Create(ctx context.Context, sig *mail.Signature) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sig.ID == uuid.Nil {
		sig.ID = uuid.New()
	}
	now := time.Now().UTC()
	sig.CreatedAt = now
	sig.UpdatedAt = now
	if sig.IsDefault {
		for _, s := range m.sigs {
			if s.UserID == sig.UserID {
				s.IsDefault = false
			}
		}
	}
	cp := *sig
	m.sigs[sig.ID] = &cp
	return nil
}

func (m *memSignatureRepo) GetByID(ctx context.Context, userID, id uuid.UUID) (*mail.Signature, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sig, ok := m.sigs[id]
	if !ok || sig.UserID != userID {
		return nil, mail.ErrSignatureNotFound
	}
	cp := *sig
	return &cp, nil
}

func (m *memSignatureRepo) GetDefault(ctx context.Context, userID uuid.UUID) (*mail.Signature, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sig := range m.sigs {
		if sig.UserID == userID && sig.IsDefault {
			cp := *sig
			return &cp, nil
		}
	}
	return nil, mail.ErrSignatureNotFound
}

func (m *memSignatureRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]mail.Signature, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []mail.Signature
	for _, sig := range m.sigs {
		if sig.UserID == userID {
			res = append(res, *sig)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].CreatedAt.Before(res[j].CreatedAt)
	})
	return res, nil
}

func (m *memSignatureRepo) Update(ctx context.Context, sig *mail.Signature) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.sigs[sig.ID]
	if !ok || existing.UserID != sig.UserID {
		return mail.ErrSignatureNotFound
	}
	if sig.IsDefault {
		for _, s := range m.sigs {
			if s.UserID == sig.UserID && s.ID != sig.ID {
				s.IsDefault = false
			}
		}
	}
	sig.UpdatedAt = time.Now().UTC()
	cp := *sig
	m.sigs[sig.ID] = &cp
	return nil
}

func (m *memSignatureRepo) SetDefault(ctx context.Context, userID, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sig, ok := m.sigs[id]
	if !ok || sig.UserID != userID {
		return mail.ErrSignatureNotFound
	}
	for _, s := range m.sigs {
		if s.UserID == userID {
			s.IsDefault = (s.ID == id)
		}
	}
	return nil
}

func (m *memSignatureRepo) Delete(ctx context.Context, userID, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sig, ok := m.sigs[id]
	if !ok || sig.UserID != userID {
		return mail.ErrSignatureNotFound
	}
	delete(m.sigs, id)
	return nil
}

// memRuleRepo implements mail.RuleRepository in-memory for testing.
type memRuleRepo struct {
	mu    sync.Mutex
	rules map[uuid.UUID]*mail.Rule
}

func newMemRuleRepo() *memRuleRepo {
	return &memRuleRepo{rules: make(map[uuid.UUID]*mail.Rule)}
}

func (m *memRuleRepo) Create(ctx context.Context, rule *mail.Rule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	now := time.Now().UTC()
	rule.CreatedAt = now
	rule.UpdatedAt = now
	cp := *rule
	m.rules[rule.ID] = &cp
	return nil
}

func (m *memRuleRepo) GetByID(ctx context.Context, userID, id uuid.UUID) (*mail.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rule, ok := m.rules[id]
	if !ok || rule.UserID != userID {
		return nil, mail.ErrRuleNotFound
	}
	cp := *rule
	return &cp, nil
}

func (m *memRuleRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]mail.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []mail.Rule
	for _, rule := range m.rules {
		if rule.UserID == userID {
			res = append(res, *rule)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Priority < res[j].Priority
	})
	return res, nil
}

func (m *memRuleRepo) ListEnabled(ctx context.Context, userID uuid.UUID) ([]mail.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []mail.Rule
	for _, rule := range m.rules {
		if rule.UserID == userID && rule.Enabled {
			res = append(res, *rule)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Priority < res[j].Priority
	})
	return res, nil
}

func (m *memRuleRepo) Update(ctx context.Context, rule *mail.Rule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.rules[rule.ID]
	if !ok || existing.UserID != rule.UserID {
		return mail.ErrRuleNotFound
	}
	rule.UpdatedAt = time.Now().UTC()
	cp := *rule
	m.rules[rule.ID] = &cp
	return nil
}

func (m *memRuleRepo) SetEnabled(ctx context.Context, userID, id uuid.UUID, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rule, ok := m.rules[id]
	if !ok || rule.UserID != userID {
		return mail.ErrRuleNotFound
	}
	rule.Enabled = enabled
	rule.UpdatedAt = time.Now().UTC()
	return nil
}

func (m *memRuleRepo) Reorder(ctx context.Context, userID uuid.UUID, orderedIDs []uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, id := range orderedIDs {
		if rule, ok := m.rules[id]; ok && rule.UserID == userID {
			rule.Priority = i + 1
			rule.UpdatedAt = time.Now().UTC()
		}
	}
	return nil
}

func (m *memRuleRepo) Delete(ctx context.Context, userID, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rule, ok := m.rules[id]
	if !ok || rule.UserID != userID {
		return mail.ErrRuleNotFound
	}
	delete(m.rules, id)
	return nil
}

type memUserLookup struct {
	users map[string]*identity.User
}

func (m *memUserLookup) GetByEmail(ctx context.Context, email string) (*identity.User, error) {
	u, ok := m.users[strings.ToLower(email)]
	if !ok {
		return nil, fmt.Errorf("user not found: %s", email)
	}
	return u, nil
}

type memThreadRepo struct{}

func (m *memThreadRepo) List(ctx context.Context, userID uuid.UUID) ([]mail.Thread, error) {
	return nil, nil
}

func (m *memThreadRepo) FindByMessageID(ctx context.Context, messageID string) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (m *memThreadRepo) FindBySubject(ctx context.Context, subject string) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (m *memThreadRepo) SetThreadID(ctx context.Context, messageID, threadID uuid.UUID) error {
	return nil
}

func TestMailSettings_E2E_SignaturesAndRules(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	userEmail := "alice@cloudlift.run"
	user := &identity.User{
		ID:       userID,
		Email:    userEmail,
		Username: "alice",
	}

	// 1. Signatures End-to-End
	sigRepo := newMemSignatureRepo()

	sig1 := &mail.Signature{
		UserID:    userID,
		Name:      "Work Signature",
		Content:   "Alice - Lead Architect\nCloudlift",
		IsDefault: true,
	}
	if err := sigRepo.Create(ctx, sig1); err != nil {
		t.Fatalf("Create sig1: %v", err)
	}

	sig2 := &mail.Signature{
		UserID:    userID,
		Name:      "Personal Signature",
		Content:   "Cheers,\nAlice",
		IsDefault: false,
	}
	if err := sigRepo.Create(ctx, sig2); err != nil {
		t.Fatalf("Create sig2: %v", err)
	}

	// Verify default is sig1
	defSig, err := sigRepo.GetDefault(ctx, userID)
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	if defSig.ID != sig1.ID {
		t.Errorf("Expected default %v, got %v", sig1.ID, defSig.ID)
	}

	// Switch default to sig2
	if err := sigRepo.SetDefault(ctx, userID, sig2.ID); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	defSig, err = sigRepo.GetDefault(ctx, userID)
	if err != nil {
		t.Fatalf("GetDefault after switch: %v", err)
	}
	if defSig.ID != sig2.ID {
		t.Errorf("Expected default %v, got %v", sig2.ID, defSig.ID)
	}

	// Verify sig1 is no longer default
	s1, err := sigRepo.GetByID(ctx, userID, sig1.ID)
	if err != nil {
		t.Fatalf("GetByID sig1: %v", err)
	}
	if s1.IsDefault {
		t.Errorf("Expected sig1 IsDefault to be false")
	}

	// Delete sig1
	if err := sigRepo.Delete(ctx, userID, sig1.ID); err != nil {
		t.Fatalf("Delete sig1: %v", err)
	}
	sigs, err := sigRepo.ListByUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(sigs) != 1 || sigs[0].ID != sig2.ID {
		t.Errorf("Expected only sig2 remaining, got %d signatures", len(sigs))
	}

	// 2. Rules Delivery Integration End-to-End
	mailboxRepo := newMemMailboxRepo()
	inbox, err := mailboxRepo.Create(ctx, userID, "INBOX")
	if err != nil {
		t.Fatalf("Create INBOX: %v", err)
	}
	msgRepo := newMemMessageRepo()
	ruleRepo := newMemRuleRepo()
	usersLookup := &memUserLookup{users: map[string]*identity.User{userEmail: user}}

	mailboxes := mail.NewMailboxes(mailboxRepo)
	messages := mail.NewMail(msgRepo)
	threads := mail.NewThreads(&memThreadRepo{})

	mailDelivery := mail.NewDelivery(
		usersLookup,
		mailboxes,
		messages,
		nil,
		threads,
		nil,
		"cloudlift.run",
	)
	mailDelivery.SetRules(ruleRepo)

	// Add Rule: If sender contains "alerts@github.com", move to "GitHub" and mark read
	rule1 := &mail.Rule{
		UserID:         userID,
		Name:           "Route GitHub Alerts",
		Enabled:        true,
		Priority:       1,
		MatchMode:      "all",
		StopProcessing: true,
		Conditions: []mail.RuleCondition{
			{Field: "from", Operator: "contains", Value: "alerts@github.com"},
		},
		Actions: []mail.RuleAction{
			{Type: "move_to_folder", Target: "GitHub"},
			{Type: "mark_read"},
		},
	}
	if err := ruleRepo.Create(ctx, rule1); err != nil {
		t.Fatalf("Create rule1: %v", err)
	}

	// Deliver matching incoming email
	incomingMsg := &mail.Message{
		Sender:     "alerts@github.com",
		Recipients: []string{userEmail},
		Subject:    "Build failed in main",
		RawMessage: "From: alerts@github.com\r\nTo: alice@cloudlift.run\r\nSubject: Build failed\r\n\r\nPipeline failed.",
	}
	if err := mailDelivery.Deliver(ctx, userEmail, incomingMsg); err != nil {
		t.Fatalf("Deliver incoming email: %v", err)
	}

	// Verify message in message repository
	if len(msgRepo.messages) != 1 {
		t.Fatalf("Expected 1 delivered message, got %d", len(msgRepo.messages))
	}
	delivered := msgRepo.messages[0]
	if !delivered.Seen {
		t.Errorf("Expected message to be marked Seen")
	}

	// Verify mailbox is the dynamically created "GitHub" mailbox
	ghMailbox, err := mailboxRepo.GetByName(ctx, userID, "GitHub")
	if err != nil {
		t.Fatalf("GetByName GitHub: %v", err)
	}
	if delivered.MailboxID != ghMailbox.ID {
		t.Errorf("Expected message in GitHub mailbox (%v), got %v", ghMailbox.ID, delivered.MailboxID)
	}

	// 3. Service ApplyRulesToInbox End-to-End
	mailSvc := mail.NewService(mailboxes, messages, nil, mailDelivery, "cloudlift.run")
	mailSvc.SetRules(ruleRepo)

	// Append a message to INBOX that hasn't had rules applied yet
	inboxMsg := &mail.Message{
		ID:         uuid.New(),
		MailboxID:  inbox.ID,
		Sender:     "boss@company.com",
		Recipients: []string{userEmail},
		Subject:    "Urgent meeting",
		Seen:       false,
		Flagged:    false,
		RawMessage: "From: boss@company.com\r\nTo: alice@cloudlift.run\r\nSubject: Urgent meeting\r\n\r\nMeet in 5 minutes.",
	}
	if err := msgRepo.Append(ctx, inboxMsg); err != nil {
		t.Fatalf("Append inboxMsg: %v", err)
	}

	// Create Rule for boss: star it and move to "Important"
	rule2 := &mail.Rule{
		UserID:         userID,
		Name:           "Star Boss Emails",
		Enabled:        true,
		Priority:       2,
		MatchMode:      "all",
		StopProcessing: false,
		Conditions: []mail.RuleCondition{
			{Field: "from", Operator: "contains", Value: "boss@company.com"},
		},
		Actions: []mail.RuleAction{
			{Type: "star"},
			{Type: "move_to_folder", Target: "Important"},
		},
	}
	if err := ruleRepo.Create(ctx, rule2); err != nil {
		t.Fatalf("Create rule2: %v", err)
	}

	// Run manual rules execution on INBOX
	affected, err := mailSvc.ApplyRulesToInbox(ctx, userID)
	if err != nil {
		t.Fatalf("ApplyRulesToInbox: %v", err)
	}
	if affected != 1 {
		t.Errorf("Expected 1 message affected, got %d", affected)
	}

	// Verify inboxMsg has been updated
	updatedMsg, err := msgRepo.Get(ctx, inboxMsg.ID)
	if err != nil {
		t.Fatalf("Get updatedMsg: %v", err)
	}
	if !updatedMsg.Flagged {
		t.Errorf("Expected updatedMsg to be Flagged")
	}
	importantBox, err := mailboxRepo.GetByName(ctx, userID, "Important")
	if err != nil {
		t.Fatalf("GetByName Important: %v", err)
	}
	if updatedMsg.MailboxID != importantBox.ID {
		t.Errorf("Expected updatedMsg in Important mailbox (%v), got %v", importantBox.ID, updatedMsg.MailboxID)
	}
}
