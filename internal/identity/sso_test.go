package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type memSSORepo struct {
	links []*SSOLink
}

func (m *memSSORepo) GetByProviderSubject(_ context.Context, provider, subject string) (*SSOLink, error) {
	for _, l := range m.links {
		if l.Provider == provider && l.Subject == subject {
			return l, nil
		}
	}
	return nil, ErrUserNotFound
}

func (m *memSSORepo) Link(_ context.Context, userID uuid.UUID, provider, subject, email string) error {
	m.links = append(m.links, &SSOLink{
		UserID: userID, Provider: provider, Subject: subject, Email: email,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	return nil
}

// memUsersRepo is a full in-memory UserRepository fit for provisioning.
type memUsersRepo struct {
	users map[string]*User // by email
	seq   int
}

func (m *memUsersRepo) Create(_ context.Context, email, username, passwordHash, displayName string) (*User, error) {
	if _, ok := m.users[email]; ok {
		return nil, ErrUserAlreadyExists
	}
	m.seq++
	u := &User{ID: uuid.New(), Email: email, Username: username,
		PasswordHash: passwordHash, DisplayName: displayName, Enabled: true}
	m.users[email] = u
	return u, nil
}

func (m *memUsersRepo) Get(_ context.Context, id uuid.UUID) (*User, error) {
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, ErrUserNotFound
}

func (m *memUsersRepo) GetByEmail(_ context.Context, email string) (*User, error) {
	if u, ok := m.users[email]; ok {
		return u, nil
	}
	return nil, ErrUserNotFound
}

func (m *memUsersRepo) GetByUsername(_ context.Context, username string) (*User, error) {
	for _, u := range m.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, ErrUserNotFound
}

func (m *memUsersRepo) List(context.Context) ([]User, error) { return nil, nil }

func (m *memUsersRepo) Update(context.Context, uuid.UUID, string, bool) error { return nil }

func (m *memUsersRepo) Delete(context.Context, uuid.UUID) error { return nil }

func (m *memUsersRepo) ChangePassword(context.Context, uuid.UUID, string) error { return nil }

func (m *memUsersRepo) SetUsername(context.Context, uuid.UUID, string) error { return nil }

func newSSOTest(t *testing.T, users map[string]*User) (*SSO, *memSSORepo, *memUsersRepo) {
	t.Helper()
	urepo := &memUsersRepo{users: users}
	links := &memSSORepo{}
	return NewSSO(links, NewUsers(urepo, nil, "cloudlift.run")), links, urepo
}

func TestSSOAuthenticateReusesExistingLink(t *testing.T) {
	userID := uuid.New()
	ssos, links, _ := newSSOTest(t, map[string]*User{
		"a@example.com": {ID: userID, Email: "a@example.com", Enabled: true},
	})
	links.links = append(links.links, &SSOLink{UserID: userID, Provider: "p", Subject: "s1"})

	got, err := ssos.Authenticate(context.Background(), "p", "s1", "a@example.com", "Alice", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != userID {
		t.Fatalf("returned user id %s, want %s", got.ID, userID)
	}
}

func TestSSOAuthenticateRejectsDisabled(t *testing.T) {
	userID := uuid.New()
	ssos, links, _ := newSSOTest(t, map[string]*User{
		"a@example.com": {ID: userID, Email: "a@example.com", Enabled: false},
	})
	links.links = append(links.links, &SSOLink{UserID: userID, Provider: "p", Subject: "s1"})

	if _, err := ssos.Authenticate(context.Background(), "p", "s1", "a@example.com", "Alice", true); !errors.Is(err, ErrSSONotAllowed) {
		t.Fatalf("err = %v, want ErrSSONotAllowed", err)
	}
}

func TestSSOMergeByEmailLinksExistingAccount(t *testing.T) {
	userID := uuid.New()
	ssos, links, _ := newSSOTest(t, map[string]*User{
		"a@example.com": {ID: userID, Email: "a@example.com", DisplayName: "Alice", Enabled: true},
	})

	got, err := ssos.Authenticate(context.Background(), "p", "new-subj", "A@Example.com", "Alice", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != userID {
		t.Fatalf("linked to %s, want %s", got.ID, userID)
	}
	if len(links.links) != 1 || links.links[0].Subject != "new-subj" {
		t.Fatalf("link not recorded: %+v", links.links)
	}
}

func TestSSOAutoCreateProvisionsNewAccount(t *testing.T) {
	ssos, links, _ := newSSOTest(t, map[string]*User{})

	got, err := ssos.Authenticate(context.Background(), "p", "subj-1", "new@example.com", "New User", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "new@cloudlift.run" || got.Username != "new" || !got.Enabled {
		t.Fatalf("provisioned user wrong: %+v", got)
	}
	// SSO-only accounts must not be usable with a password they never set:
	// a thrown-away random hash means password login stays blocked.
	if got.PasswordHash == "" {
		t.Fatal("expected a password hash placeholder")
	}
	if len(links.links) != 1 {
		t.Fatalf("expected one link, got %d", len(links.links))
	}
}

func TestSSONoAutoCreateReturnsNotFound(t *testing.T) {
	ssos, _, _ := newSSOTest(t, map[string]*User{})

	if _, err := ssos.Authenticate(context.Background(), "p", "subj-1", "nobody@example.com", "", false); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}

func TestSSOSecondProviderLinksSameAccount(t *testing.T) {
	userID := uuid.New()
	ssos, links, _ := newSSOTest(t, map[string]*User{
		"a@example.com": {ID: userID, Email: "a@example.com", Enabled: true},
	})
	links.links = append(links.links, &SSOLink{UserID: userID, Provider: "p1", Subject: "s1"})

	got, err := ssos.Authenticate(context.Background(), "p2", "s9", "a@example.com", "Alice", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != userID {
		t.Fatalf("second provider linked to %s, want %s", got.ID, userID)
	}
	if len(links.links) != 2 {
		t.Fatalf("expected two links, got %d", len(links.links))
	}
}
