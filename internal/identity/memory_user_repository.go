package identity

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryUserRepository is an in-memory implementation of UserRepository.
type MemoryUserRepository struct {
	mu           sync.RWMutex
	usersByID    map[uuid.UUID]*User
	usersByEmail map[string]*User
	usersByName  map[string]*User
}

// NewMemoryUserRepository creates an empty MemoryUserRepository.
func NewMemoryUserRepository() *MemoryUserRepository {
	return &MemoryUserRepository{
		usersByID:    make(map[uuid.UUID]*User),
		usersByEmail: make(map[string]*User),
		usersByName:  make(map[string]*User),
	}
}

func (m *MemoryUserRepository) Create(ctx context.Context, email, username, passwordHash, displayName string) (*User, error) {
	return m.create(ctx, email, username, passwordHash, displayName, nil)
}

func (m *MemoryUserRepository) CreateWithOrg(ctx context.Context, email, username, passwordHash, displayName string, orgID uuid.UUID) (*User, error) {
	return m.create(ctx, email, username, passwordHash, displayName, &orgID)
}

func (m *MemoryUserRepository) create(_ context.Context, email, username, passwordHash, displayName string, orgID *uuid.UUID) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanEmail := strings.ToLower(strings.TrimSpace(email))
	cleanUser := strings.ToLower(strings.TrimSpace(username))

	if _, exists := m.usersByEmail[cleanEmail]; exists {
		return nil, ErrUserAlreadyExists
	}
	if cleanUser != "" {
		if _, exists := m.usersByName[cleanUser]; exists {
			return nil, ErrUserAlreadyExists
		}
	}

	now := time.Now()
	var orgIDCopy *uuid.UUID
	if orgID != nil {
		id := *orgID
		orgIDCopy = &id
	}

	u := &User{
		ID:             uuid.New(),
		OrganizationID: orgIDCopy,
		Email:          cleanEmail,
		Username:       cleanUser,
		PasswordHash:   passwordHash,
		DisplayName:    displayName,
		Enabled:        true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	m.usersByID[u.ID] = u
	m.usersByEmail[cleanEmail] = u
	if cleanUser != "" {
		m.usersByName[cleanUser] = u
	}

	copy := *u
	return &copy, nil
}

func (m *MemoryUserRepository) Get(_ context.Context, id uuid.UUID) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, exists := m.usersByID[id]
	if !exists {
		return nil, ErrUserNotFound
	}
	copy := *u
	return &copy, nil
}

func (m *MemoryUserRepository) GetByEmail(_ context.Context, email string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cleanEmail := strings.ToLower(strings.TrimSpace(email))
	u, exists := m.usersByEmail[cleanEmail]
	if !exists {
		return nil, ErrUserNotFound
	}
	copy := *u
	return &copy, nil
}

func (m *MemoryUserRepository) GetByUsername(_ context.Context, username string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cleanUser := strings.ToLower(strings.TrimSpace(username))
	u, exists := m.usersByName[cleanUser]
	if !exists {
		return nil, ErrUserNotFound
	}
	copy := *u
	return &copy, nil
}

func (m *MemoryUserRepository) List(_ context.Context) ([]User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]User, 0, len(m.usersByID))
	for _, u := range m.usersByID {
		out = append(out, *u)
	}
	return out, nil
}

func (m *MemoryUserRepository) Update(_ context.Context, id uuid.UUID, displayName string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, exists := m.usersByID[id]
	if !exists {
		return ErrUserNotFound
	}
	u.DisplayName = displayName
	u.Enabled = enabled
	u.UpdatedAt = time.Now()
	return nil
}

func (m *MemoryUserRepository) Delete(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, exists := m.usersByID[id]
	if !exists {
		return ErrUserNotFound
	}
	delete(m.usersByID, id)
	delete(m.usersByEmail, u.Email)
	if u.Username != "" {
		delete(m.usersByName, u.Username)
	}
	return nil
}

func (m *MemoryUserRepository) ChangePassword(_ context.Context, id uuid.UUID, passwordHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, exists := m.usersByID[id]
	if !exists {
		return ErrUserNotFound
	}
	u.PasswordHash = passwordHash
	u.UpdatedAt = time.Now()
	return nil
}

func (m *MemoryUserRepository) SetUsername(_ context.Context, id uuid.UUID, username string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, exists := m.usersByID[id]
	if !exists {
		return ErrUserNotFound
	}
	cleanUser := strings.ToLower(strings.TrimSpace(username))
	if cleanUser != "" {
		if other, ok := m.usersByName[cleanUser]; ok && other.ID != id {
			return ErrUserAlreadyExists
		}
	}
	if u.Username != "" {
		delete(m.usersByName, u.Username)
	}
	u.Username = cleanUser
	if cleanUser != "" {
		m.usersByName[cleanUser] = u
	}
	u.UpdatedAt = time.Now()
	return nil
}

func (m *MemoryUserRepository) SetOrganization(_ context.Context, id, orgID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, exists := m.usersByID[id]
	if !exists {
		return ErrUserNotFound
	}
	idCopy := orgID
	u.OrganizationID = &idCopy
	u.UpdatedAt = time.Now()
	return nil
}
