package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type memUserStore struct {
	mu      sync.Mutex
	byID    map[uuid.UUID]*identity.User
	byEmail map[string]*identity.User
}

func newMemUserStore() *memUserStore {
	return &memUserStore{
		byID:    make(map[uuid.UUID]*identity.User),
		byEmail: make(map[string]*identity.User),
	}
}

func (m *memUserStore) Create(_ context.Context, email, username, hash, name string) (*identity.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.byID {
		if existing.Username == username || existing.Email == email {
			return nil, identity.ErrUserAlreadyExists
		}
	}
	u := &identity.User{
		ID:           uuid.New(),
		Email:        email,
		Username:     username,
		PasswordHash: hash,
		DisplayName:  name,
		Enabled:      true,
	}
	m.byID[u.ID] = u
	m.byEmail[email] = u
	cp := *u
	return &cp, nil
}

func (m *memUserStore) Get(_ context.Context, id uuid.UUID) (*identity.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return nil, identity.ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

func (m *memUserStore) GetByEmail(_ context.Context, email string) (*identity.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byEmail[email]
	if !ok {
		return nil, identity.ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

func (m *memUserStore) GetByUsername(_ context.Context, username string) (*identity.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.byEmail {
		if u.Username == username {
			cp := *u
			return &cp, nil
		}
	}
	return nil, identity.ErrUserNotFound
}

func (m *memUserStore) List(_ context.Context) ([]identity.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []identity.User
	for _, u := range m.byID {
		list = append(list, *u)
	}
	return list, nil
}

func (m *memUserStore) Update(_ context.Context, id uuid.UUID, name string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return identity.ErrUserNotFound
	}
	u.DisplayName, u.Enabled = name, enabled
	m.byEmail[u.Email] = u
	return nil
}

func (m *memUserStore) Delete(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.byID[id]; ok {
		delete(m.byEmail, u.Email)
		delete(m.byID, id)
	}
	return nil
}

func (m *memUserStore) ChangePassword(_ context.Context, id uuid.UUID, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return identity.ErrUserNotFound
	}
	u.PasswordHash = hash
	return nil
}

func (m *memUserStore) SetUsername(_ context.Context, id uuid.UUID, username string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return identity.ErrUserNotFound
	}
	u.Username = username
	return nil
}

func TestCreateUserReturnsPrimaryEmail(t *testing.T) {
	// Verify creating a user with username creates primary email <username>@cloudlift.run
	users := identity.NewUsers(newMemUserStore(), nil, "cloudlift.run")
	handlers := NewUserHandlers(users)

	body := `{"username":"dave","password":"s3cret-password","display_name":"Dave"}`
	req := httptest.NewRequest("POST", "/users", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handlers.CreateHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	json.NewDecoder(rec.Body).Decode(&res)
	if res["email"] != "dave@cloudlift.run" {
		t.Errorf("expected email 'dave@cloudlift.run', got %v", res["email"])
	}
	if res["username"] != "dave" {
		t.Errorf("expected username 'dave', got %v", res["username"])
	}
}

func TestCreateUserReturnsPrimaryEmailWithEmailField(t *testing.T) {
	// Verify creating a user with email fallback derives username and creates primary email
	users := identity.NewUsers(newMemUserStore(), nil, "cloudlift.run")
	handlers := NewUserHandlers(users)

	body := `{"email":"alice@example.com","password":"s3cret-password","display_name":"Alice"}`
	req := httptest.NewRequest("POST", "/users", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handlers.CreateHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	json.NewDecoder(rec.Body).Decode(&res)
	if res["email"] != "alice@cloudlift.run" {
		t.Errorf("expected email 'alice@cloudlift.run', got %v", res["email"])
	}
	if res["username"] != "alice" {
		t.Errorf("expected username 'alice', got %v", res["username"])
	}
}

func TestCreateUserConflict(t *testing.T) {
	users := identity.NewUsers(newMemUserStore(), nil, "cloudlift.run")
	handlers := NewUserHandlers(users)

	body := `{"username":"bob","password":"s3cret-password","display_name":"Bob"}`
	req := httptest.NewRequest("POST", "/users", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handlers.CreateHandler(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", rec.Code)
	}

	// Attempt to create the same user again
	req2 := httptest.NewRequest("POST", "/users", strings.NewReader(body))
	rec2 := httptest.NewRecorder()
	handlers.CreateHandler(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict, got %d", rec2.Code)
	}
}

func TestCreateUserValidationErrorsReturnBadRequest(t *testing.T) {
	users := identity.NewUsers(newMemUserStore(), nil, "cloudlift.run")
	handlers := NewUserHandlers(users)

	// Password too short (< 8 chars)
	body := `{"username":"validuser","password":"short","display_name":"User"}`
	req := httptest.NewRequest("POST", "/users", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handlers.CreateHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for short password, got %d", rec.Code)
	}

	// Username invalid (spaces/symbols)
	body2 := `{"username":"invalid name!","password":"valid-password","display_name":"User"}`
	req2 := httptest.NewRequest("POST", "/users", strings.NewReader(body2))
	rec2 := httptest.NewRecorder()
	handlers.CreateHandler(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for invalid username, got %d", rec2.Code)
	}
}

type mockDomainService struct {
	domain *identity.Domain
}

func (m *mockDomainService) Create(ctx context.Context, name string) (*identity.Domain, error) {
	return nil, nil
}
func (m *mockDomainService) Get(ctx context.Context, id uuid.UUID) (*identity.Domain, error) {
	if m.domain != nil && m.domain.ID == id {
		return m.domain, nil
	}
	return nil, identity.ErrDomainNotFound
}
func (m *mockDomainService) List(ctx context.Context) ([]identity.Domain, error) {
	return nil, nil
}
func (m *mockDomainService) Delete(ctx context.Context, id uuid.UUID) error {
	return nil
}

func TestDomainCreateUserReturnsPrimaryEmail(t *testing.T) {
	users := identity.NewUsers(newMemUserStore(), nil, "cloudlift.run")
	domainID := uuid.New()
	domSvc := &mockDomainService{domain: &identity.Domain{ID: domainID, Name: "custom.org"}}
	handlers := NewDomainHandlers(domSvc, users)

	body := `{"local_part":"carol","password":"s3cret-password","display_name":"Carol"}`
	req := httptest.NewRequest("POST", "/domains/"+domainID.String()+"/users", strings.NewReader(body))
	req.SetPathValue("domainID", domainID.String())
	rec := httptest.NewRecorder()
	handlers.CreateUserHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	json.NewDecoder(rec.Body).Decode(&res)
	if res["email"] != "carol@cloudlift.run" {
		t.Errorf("expected email 'carol@cloudlift.run', got %v", res["email"])
	}
	if res["username"] != "carol" {
		t.Errorf("expected username 'carol', got %v", res["username"])
	}
}

func TestDomainCreateUserValidationErrorsReturnBadRequest(t *testing.T) {
	users := identity.NewUsers(newMemUserStore(), nil, "cloudlift.run")
	domainID := uuid.New()
	domSvc := &mockDomainService{domain: &identity.Domain{ID: domainID, Name: "custom.org"}}
	handlers := NewDomainHandlers(domSvc, users)

	// Short password
	body := `{"local_part":"carol","password":"short","display_name":"Carol"}`
	req := httptest.NewRequest("POST", "/domains/"+domainID.String()+"/users", strings.NewReader(body))
	req.SetPathValue("domainID", domainID.String())
	rec := httptest.NewRecorder()
	handlers.CreateUserHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for short password, got %d", rec.Code)
	}
}

