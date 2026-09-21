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
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
)

func TestSettingsHandlers(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	user := &identity.User{
		ID:          userID,
		Email:       "alice@example.com",
		DisplayName: "Alice Smith",
		Enabled:     true,
	}

	// Setup mock repositories/services
	sigs := &mockSignatureRepo{
		sigs: []mail.Signature{
			{ID: uuid.New(), UserID: userID, Name: "Personal", Content: "Cheers,\nAli", IsDefault: true},
		},
	}
	rules := &mockRuleRepo{
		rules: []mail.Rule{
			{
				ID:       uuid.New(),
				UserID:   userID,
				Name:     "Spam Rule",
				Enabled:  true,
				Priority: 1,
			},
		},
	}
	mailSvc := &mockMailService{}
	userSvc := &profileMockUserService{user: user}
	sessSvc := newProfileMockSessionService(userID)
	sess, _ := sessSvc.Create(context.Background(), userID, 24*time.Hour)

	server, err := web.New(files, contactService{}, calendarService{}, mailSvc, sessSvc, userSvc, false)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	server.SetMailSettings(sigs, rules)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	csrfToken := "test-csrf-token"

	// 1. Test GET /settings renders default section profile
	t.Run("GET /settings renders profile", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/settings", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Alice Smith") {
			t.Errorf("expected body to contain user display name 'Alice Smith'")
		}
	})

	// 2. Test GET /settings?section=signatures renders signatures list
	t.Run("GET /settings?section=signatures renders signatures", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/settings?section=signatures", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Personal") {
			t.Errorf("expected body to contain signature 'Personal'")
		}
	})

	// 3. Test GET /settings?section=rules renders rules list
	t.Run("GET /settings?section=rules renders rules", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/settings?section=rules", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Spam Rule") {
			t.Errorf("expected body to contain rule 'Spam Rule'")
		}
	})

	// 4. Test POST /settings/profile updates display name and redirects
	t.Run("POST /settings/profile updates name", func(t *testing.T) {
		form := url.Values{
			"_csrf":        {csrfToken},
			"display_name": {"Alice Jones"},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings/profile", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("expected status 303, got %d", rec.Code)
		}
		loc := rec.Header().Get("Location")
		if !strings.Contains(loc, "/settings?section=profile") || !strings.Contains(loc, "success=profile") {
			t.Errorf("expected redirect to /settings?section=profile&success=profile, got %q", loc)
		}
		if userSvc.lastUpdatedName != "Alice Jones" {
			t.Errorf("expected user service to be updated with 'Alice Jones', got %q", userSvc.lastUpdatedName)
		}
	})

	// 5. Test POST /settings/password changes password and redirects
	t.Run("POST /settings/password changes password", func(t *testing.T) {
		form := url.Values{
			"_csrf":            {csrfToken},
			"current_password": {"correct-password"},
			"new_password":     {"brandnewpass123"},
			"confirm_password": {"brandnewpass123"},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings/password", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("expected status 303, got %d", rec.Code)
		}
		loc := rec.Header().Get("Location")
		if !strings.Contains(loc, "/settings?section=password") || !strings.Contains(loc, "success=password") {
			t.Errorf("expected redirect to /settings?section=password&success=password, got %q", loc)
		}
	})

	// 6. Test POST /settings/signatures creates signature and redirects
	t.Run("POST /settings/signatures creates signature", func(t *testing.T) {
		form := url.Values{
			"_csrf":   {csrfToken},
			"name":    {"Work"},
			"content": {"Best regards,\nAlice"},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings/signatures", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("expected status 303, got %d", rec.Code)
		}
		loc := rec.Header().Get("Location")
		if !strings.Contains(loc, "/settings?section=signatures") {
			t.Errorf("expected redirect to contain /settings?section=signatures, got %q", loc)
		}
	})

	// 7. Test POST /settings/rules creates rule and redirects
	t.Run("POST /settings/rules creates rule", func(t *testing.T) {
		form := url.Values{
			"_csrf":           {csrfToken},
			"name":            {"Filter News"},
			"match_mode":      {"any"},
			"cond_field[]":    {"subject"},
			"cond_op[]":       {"contains"},
			"cond_val[]":      {"news"},
			"action_type[]":   {"mark_read"},
			"stop_processing": {"on"},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings/rules", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("expected status 303, got %d", rec.Code)
		}
		loc := rec.Header().Get("Location")
		if !strings.Contains(loc, "/settings?section=rules") {
			t.Errorf("expected redirect to contain /settings?section=rules, got %q", loc)
		}
	})

	// 8. Test POST /settings/rules/apply-inbox invokes ApplyRulesToInbox and redirects
	t.Run("POST /settings/rules/apply-inbox triggers rules", func(t *testing.T) {
		form := url.Values{
			"_csrf": {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings/rules/apply-inbox", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("expected status 303, got %d", rec.Code)
		}
		loc := rec.Header().Get("Location")
		if !strings.Contains(loc, "/settings?section=rules") {
			t.Errorf("expected redirect to contain /settings?section=rules, got %q", loc)
		}
		if !mailSvc.applyRulesCalled {
			t.Errorf("expected ApplyRulesToInbox to be called on mail service")
		}
	})
}

// self-contained mocks

type mockSignatureRepo struct {
	sigs []mail.Signature
}

func (m *mockSignatureRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]mail.Signature, error) {
	return m.sigs, nil
}

func (m *mockSignatureRepo) GetByID(ctx context.Context, userID, id uuid.UUID) (*mail.Signature, error) {
	for _, s := range m.sigs {
		if s.ID == id {
			return &s, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockSignatureRepo) GetDefault(ctx context.Context, userID uuid.UUID) (*mail.Signature, error) {
	for _, s := range m.sigs {
		if s.IsDefault {
			return &s, nil
		}
	}
	return nil, nil
}

func (m *mockSignatureRepo) Create(ctx context.Context, s *mail.Signature) error {
	m.sigs = append(m.sigs, *s)
	return nil
}

func (m *mockSignatureRepo) Update(ctx context.Context, s *mail.Signature) error {
	for i, ex := range m.sigs {
		if ex.ID == s.ID {
			m.sigs[i] = *s
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func (m *mockSignatureRepo) Delete(ctx context.Context, userID, id uuid.UUID) error {
	var filtered []mail.Signature
	for _, s := range m.sigs {
		if s.ID != id {
			filtered = append(filtered, s)
		}
	}
	m.sigs = filtered
	return nil
}

func (m *mockSignatureRepo) SetDefault(ctx context.Context, userID, id uuid.UUID) error {
	for i := range m.sigs {
		m.sigs[i].IsDefault = (m.sigs[i].ID == id)
	}
	return nil
}

type mockRuleRepo struct {
	rules []mail.Rule
}

func (m *mockRuleRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]mail.Rule, error) {
	return m.rules, nil
}

func (m *mockRuleRepo) ListEnabled(ctx context.Context, userID uuid.UUID) ([]mail.Rule, error) {
	var enabled []mail.Rule
	for _, r := range m.rules {
		if r.Enabled {
			enabled = append(enabled, r)
		}
	}
	return enabled, nil
}

func (m *mockRuleRepo) GetByID(ctx context.Context, userID, id uuid.UUID) (*mail.Rule, error) {
	for _, r := range m.rules {
		if r.ID == id {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockRuleRepo) Create(ctx context.Context, r *mail.Rule) error {
	m.rules = append(m.rules, *r)
	return nil
}

func (m *mockRuleRepo) Update(ctx context.Context, r *mail.Rule) error {
	for i, ex := range m.rules {
		if ex.ID == r.ID {
			m.rules[i] = *r
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func (m *mockRuleRepo) Delete(ctx context.Context, userID, id uuid.UUID) error {
	var filtered []mail.Rule
	for _, r := range m.rules {
		if r.ID != id {
			filtered = append(filtered, r)
		}
	}
	m.rules = filtered
	return nil
}

func (m *mockRuleRepo) SetEnabled(ctx context.Context, userID, id uuid.UUID, enabled bool) error {
	for i := range m.rules {
		if m.rules[i].ID == id {
			m.rules[i].Enabled = enabled
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func (m *mockRuleRepo) Reorder(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) error {
	var ordered []mail.Rule
	for _, id := range ids {
		for _, r := range m.rules {
			if r.ID == id {
				ordered = append(ordered, r)
				break
			}
		}
	}
	m.rules = ordered
	return nil
}

type mockMailService struct {
	mailServiceStub
	applyRulesCalled bool
}

func (m *mockMailService) ApplyRulesToInbox(ctx context.Context, userID uuid.UUID) (int, error) {
	m.applyRulesCalled = true
	return 1, nil
}

type profileMockUserService struct {
	testUserService
	user            *identity.User
	lastUpdatedName string
}

func (m *profileMockUserService) Get(ctx context.Context, id uuid.UUID) (*identity.User, error) {
	return m.user, nil
}

func (m *profileMockUserService) Authenticate(ctx context.Context, email, password string) (*identity.User, error) {
	if password == "correct-password" {
		return m.user, nil
	}
	return nil, fmt.Errorf("auth fail")
}

func (m *profileMockUserService) Update(ctx context.Context, id uuid.UUID, displayName string, enabled bool) error {
	m.lastUpdatedName = displayName
	m.user.DisplayName = displayName
	return nil
}

type profileMockSessionService struct {
	testSessionService
	userID uuid.UUID
}

func newProfileMockSessionService(userID uuid.UUID) *profileMockSessionService {
	return &profileMockSessionService{userID: userID}
}

func (s *profileMockSessionService) Create(ctx context.Context, userID uuid.UUID, ttl time.Duration) (*identity.Session, error) {
	return &identity.Session{UserID: userID, Token: "valid-session"}, nil
}

func (s *profileMockSessionService) GetByToken(ctx context.Context, token string) (*identity.Session, error) {
	return &identity.Session{UserID: s.userID, Token: "valid-session"}, nil
}
