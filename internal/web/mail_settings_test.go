package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
)

// MockSignatureRepository implements mail.SignatureRepository in memory for testing
type mockSignatureRepo struct {
	sigs []mail.Signature
}

func (m *mockSignatureRepo) Create(ctx context.Context, sig *mail.Signature) error {
	sig.ID = uuid.New()
	if sig.IsDefault {
		for i := range m.sigs {
			m.sigs[i].IsDefault = false
		}
	}
	m.sigs = append(m.sigs, *sig)
	return nil
}

func (m *mockSignatureRepo) GetByID(ctx context.Context, userID, id uuid.UUID) (*mail.Signature, error) {
	for _, s := range m.sigs {
		if s.UserID == userID && s.ID == id {
			return &s, nil
		}
	}
	return nil, mail.ErrSignatureNotFound
}

func (m *mockSignatureRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]mail.Signature, error) {
	var userSigs []mail.Signature
	for _, s := range m.sigs {
		if s.UserID == userID {
			userSigs = append(userSigs, s)
		}
	}
	return userSigs, nil
}

func (m *mockSignatureRepo) GetDefault(ctx context.Context, userID uuid.UUID) (*mail.Signature, error) {
	for _, s := range m.sigs {
		if s.UserID == userID && s.IsDefault {
			return &s, nil
		}
	}
	return nil, mail.ErrSignatureNotFound
}

func (m *mockSignatureRepo) Update(ctx context.Context, sig *mail.Signature) error {
	for i, s := range m.sigs {
		if s.UserID == sig.UserID && s.ID == sig.ID {
			if sig.IsDefault {
				for j := range m.sigs {
					m.sigs[j].IsDefault = false
				}
			}
			m.sigs[i] = *sig
			return nil
		}
	}
	return mail.ErrSignatureNotFound
}

func (m *mockSignatureRepo) SetDefault(ctx context.Context, userID, id uuid.UUID) error {
	found := false
	for i, s := range m.sigs {
		if s.UserID == userID && s.ID == id {
			m.sigs[i].IsDefault = true
			found = true
		} else if s.UserID == userID {
			m.sigs[i].IsDefault = false
		}
	}
	if !found {
		return mail.ErrSignatureNotFound
	}
	return nil
}

func (m *mockSignatureRepo) Delete(ctx context.Context, userID, id uuid.UUID) error {
	for i, s := range m.sigs {
		if s.UserID == userID && s.ID == id {
			m.sigs = append(m.sigs[:i], m.sigs[i+1:]...)
			return nil
		}
	}
	return mail.ErrSignatureNotFound
}

// MockRuleRepository implements mail.RuleRepository in memory for testing
type mockRuleRepo struct {
	rules []mail.Rule
}

func (m *mockRuleRepo) Create(ctx context.Context, rule *mail.Rule) error {
	rule.ID = uuid.New()
	rule.Priority = len(m.rules)
	m.rules = append(m.rules, *rule)
	return nil
}

func (m *mockRuleRepo) GetByID(ctx context.Context, userID, id uuid.UUID) (*mail.Rule, error) {
	for _, r := range m.rules {
		if r.UserID == userID && r.ID == id {
			return &r, nil
		}
	}
	return nil, mail.ErrRuleNotFound
}

func (m *mockRuleRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]mail.Rule, error) {
	var userRules []mail.Rule
	for _, r := range m.rules {
		if r.UserID == userID {
			userRules = append(userRules, r)
		}
	}
	return userRules, nil
}

func (m *mockRuleRepo) ListEnabled(ctx context.Context, userID uuid.UUID) ([]mail.Rule, error) {
	var enabled []mail.Rule
	for _, r := range m.rules {
		if r.UserID == userID && r.Enabled {
			enabled = append(enabled, r)
		}
	}
	return enabled, nil
}

func (m *mockRuleRepo) Update(ctx context.Context, rule *mail.Rule) error {
	for i, r := range m.rules {
		if r.UserID == rule.UserID && r.ID == rule.ID {
			m.rules[i] = *rule
			return nil
		}
	}
	return mail.ErrRuleNotFound
}

func (m *mockRuleRepo) SetEnabled(ctx context.Context, userID, id uuid.UUID, enabled bool) error {
	for i, r := range m.rules {
		if r.UserID == userID && r.ID == id {
			m.rules[i].Enabled = enabled
			return nil
		}
	}
	return mail.ErrRuleNotFound
}

func (m *mockRuleRepo) Reorder(ctx context.Context, userID uuid.UUID, orderedIDs []uuid.UUID) error {
	idMap := make(map[uuid.UUID]int)
	for i, id := range orderedIDs {
		idMap[id] = i
	}
	for i, r := range m.rules {
		if r.UserID == userID {
			if priority, ok := idMap[r.ID]; ok {
				m.rules[i].Priority = priority
			}
		}
	}
	return nil
}

func (m *mockRuleRepo) Delete(ctx context.Context, userID, id uuid.UUID) error {
	for i, r := range m.rules {
		if r.UserID == userID && r.ID == id {
			m.rules = append(m.rules[:i], m.rules[i+1:]...)
			return nil
		}
	}
	return mail.ErrRuleNotFound
}

// Mail service stub helper
type mockMailService struct {
	mailServiceStub
	applyRulesCalled bool
	lastApplyUserID  uuid.UUID
}

func (m *mockMailService) ApplyRulesToInbox(ctx context.Context, userID uuid.UUID) (int, error) {
	m.applyRulesCalled = true
	m.lastApplyUserID = userID
	return 3, nil
}

func TestMailSettings_Get(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	sigs := &mockSignatureRepo{
		sigs: []mail.Signature{
			{ID: uuid.New(), UserID: userID, Name: "Work Signature", Content: "Alice\nEngineer", IsDefault: true},
		},
	}
	rules := &mockRuleRepo{
		rules: []mail.Rule{
			{
				ID:             uuid.New(),
				UserID:         userID,
				Name:           "Spam Rule",
				Enabled:        true,
				MatchMode:      "all",
				StopProcessing: true,
				Conditions:     []mail.RuleCondition{{Field: mail.RuleFieldFrom, Operator: mail.RuleOperatorContains, Value: "spam"}},
				Actions:        []mail.RuleAction{{Type: mail.RuleActionMoveToTrash}},
			},
		},
	}
	mailSvc := &mockMailService{}

	server, err := web.New(files, contactService{}, calendarService{}, mailSvc, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetMailSettings(sigs, rules)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	// 1. Test signatures tab (default)
	{
		req := httptest.NewRequest(http.MethodGet, "/mail/settings", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /mail/settings (signatures) status = %d, want %d", rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Work Signature") {
			t.Errorf("missing signature 'Work Signature'")
		}
		if strings.Contains(body, "Spam Rule") {
			t.Errorf("expected rules list to be hidden on signatures tab")
		}
	}

	// 2. Test rules tab
	{
		req := httptest.NewRequest(http.MethodGet, "/mail/settings?tab=rules", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /mail/settings?tab=rules status = %d, want %d", rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Spam Rule") {
			t.Errorf("missing rule 'Spam Rule'")
		}
		if strings.Contains(body, "Work Signature") {
			t.Errorf("expected signatures list to be hidden on rules tab")
		}
	}
}

func TestMailSettings_SignaturesCRUD(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	sigs := &mockSignatureRepo{}
	rules := &mockRuleRepo{}
	mailSvc := &mockMailService{}

	server, err := web.New(files, contactService{}, calendarService{}, mailSvc, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetMailSettings(sigs, rules)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	csrfToken := "test-csrf"

	// 1. Create signature
	{
		form := url.Values{
			"name":       {"Personal"},
			"content":    {"Cheers,\nAli"},
			"is_default": {"on"},
			"_csrf":      {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, "/mail/settings/signatures", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST create signature status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		userSigs, _ := sigs.ListByUser(context.Background(), userID)
		if len(userSigs) != 1 {
			t.Fatalf("expected 1 signature, got %d", len(userSigs))
		}
		if userSigs[0].Name != "Personal" || !userSigs[0].IsDefault {
			t.Errorf("incorrect signature properties: %+v", userSigs[0])
		}
	}

	// Create second signature
	var sig2ID uuid.UUID
	{
		form := url.Values{
			"name":       {"Work"},
			"content":    {"Regards,\nAlice"},
			"is_default": {""},
			"_csrf":      {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, "/mail/settings/signatures", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		userSigs, _ := sigs.ListByUser(context.Background(), userID)
		if len(userSigs) != 2 {
			t.Fatalf("expected 2 signatures, got %d", len(userSigs))
		}
		sig2ID = userSigs[1].ID
	}

	// 2. Set default
	{
		form := url.Values{
			"_csrf": {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/mail/settings/signatures/%s/default", sig2ID), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST set default status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		sig1, _ := sigs.GetByID(context.Background(), userID, sigs.sigs[0].ID)
		sig2, _ := sigs.GetByID(context.Background(), userID, sig2ID)

		if sig1.IsDefault || !sig2.IsDefault {
			t.Errorf("expected sig2 to be default and sig1 not default")
		}
	}

	// 3. Delete
	{
		form := url.Values{
			"_csrf": {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/mail/settings/signatures/%s/delete", sig2ID), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST delete status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		userSigs, _ := sigs.ListByUser(context.Background(), userID)
		if len(userSigs) != 1 {
			t.Errorf("expected 1 signature after delete, got %d", len(userSigs))
		}
	}
}

func TestMailSettings_RulesCRUDAndApply(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	sigs := &mockSignatureRepo{}
	rules := &mockRuleRepo{}
	mailSvc := &mockMailService{}

	server, err := web.New(files, contactService{}, calendarService{}, mailSvc, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetMailSettings(sigs, rules)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	csrfToken := "test-csrf"

	// 1. Create Rule
	{
		form := url.Values{
			"name":            {"Filter News"},
			"match_mode":      {"any"},
			"cond_field[]":    {"subject", "from"},
			"cond_op[]":       {"contains", "equals"},
			"cond_val[]":      {"newsletter", "news@example.com"},
			"action_type[]":   {"move_to_folder", "mark_read"},
			"action_target[]": {"Archive", ""},
			"stop_processing": {"on"},
			"_csrf":           {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, "/mail/settings/rules", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST create rule status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		userRules, _ := rules.ListByUser(context.Background(), userID)
		if len(userRules) != 1 {
			t.Fatalf("expected 1 rule, got %d", len(userRules))
		}
		rule := userRules[0]
		if rule.Name != "Filter News" || rule.MatchMode != "any" || !rule.StopProcessing || !rule.Enabled {
			t.Errorf("incorrect rule simple properties: %+v", rule)
		}
		if len(rule.Conditions) != 2 || rule.Conditions[0].Field != mail.RuleFieldSubject || rule.Conditions[1].Value != "news@example.com" {
			t.Errorf("incorrect rule conditions: %+v", rule.Conditions)
		}
		if len(rule.Actions) != 2 || rule.Actions[0].Type != mail.RuleActionMoveToFolder || rule.Actions[0].Target != "Archive" {
			t.Errorf("incorrect rule actions: %+v", rule.Actions)
		}
	}

	ruleID := rules.rules[0].ID

	// 2. Toggle Rule
	{
		form := url.Values{
			"enabled": {"false"},
			"_csrf":   {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/mail/settings/rules/%s/toggle", ruleID), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST toggle status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		r, _ := rules.GetByID(context.Background(), userID, ruleID)
		if r.Enabled {
			t.Errorf("expected rule to be disabled after toggle")
		}
	}

	// 3. Apply inbox
	{
		form := url.Values{
			"_csrf": {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, "/mail/settings/rules/apply-inbox", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST apply-inbox status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		if !mailSvc.applyRulesCalled {
			t.Errorf("expected mail.Service.ApplyRulesToInbox to be called")
		}
		if mailSvc.lastApplyUserID != userID {
			t.Errorf("expected userID %v, got %v", userID, mailSvc.lastApplyUserID)
		}
	}
}

func TestMailSettings_AuthAndCSRF(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	sigs := &mockSignatureRepo{}
	rules := &mockRuleRepo{}
	mailSvc := &mockMailService{}

	server, err := web.New(files, contactService{}, calendarService{}, mailSvc, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetMailSettings(sigs, rules)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	// 1. Mutate signature without authentication -> 302 redirect to login
	{
		form := url.Values{
			"name":  {"test"},
			"_csrf": {"test-csrf"},
		}
		req := httptest.NewRequest(http.MethodPost, "/mail/settings/signatures", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized for unauthenticated mutate request, got %d", rec.Code)
		}
	}

	// 2. Mutate signature with auth but missing CSRF -> 403 Forbidden
	{
		form := url.Values{
			"name": {"test"},
		}
		req := httptest.NewRequest(http.MethodPost, "/mail/settings/signatures", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden for missing CSRF, got %d", rec.Code)
		}
	}
}
