package main

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
)

type dummyContactService struct{}

func (dummyContactService) List(context.Context, uuid.UUID) ([]contacts.Contact, error) {
	return nil, nil
}
func (dummyContactService) Get(context.Context, uuid.UUID, uuid.UUID) (contacts.Contact, error) {
	return contacts.Contact{}, nil
}
func (dummyContactService) PutStructured(context.Context, uuid.UUID, *uuid.UUID, contacts.Contact) (*contacts.Contact, error) {
	return nil, nil
}
func (dummyContactService) DeleteByID(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

type dummyCalendarService struct{}

func (dummyCalendarService) Get(context.Context, uuid.UUID, uuid.UUID) (*calendar.Event, error) {
	return nil, nil
}
func (dummyCalendarService) List(context.Context, uuid.UUID) ([]calendar.Event, error) {
	return nil, nil
}
func (dummyCalendarService) Put(context.Context, calendar.Event) (*calendar.Event, error) {
	return nil, nil
}
func (dummyCalendarService) Delete(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

type dummyMailService struct{}

func (dummyMailService) EnsureDefaultMailboxes(context.Context, uuid.UUID) error { return nil }
func (dummyMailService) ListMailboxes(context.Context, uuid.UUID) ([]mail.MailboxInfo, error) {
	return nil, nil
}
func (dummyMailService) GetMailbox(context.Context, uuid.UUID, string) (*mail.Mailbox, error) {
	return nil, nil
}
func (dummyMailService) ListMessages(context.Context, uuid.UUID, int, int) ([]mail.Message, error) {
	return nil, nil
}
func (dummyMailService) SearchMessages(context.Context, uuid.UUID, string) ([]mail.Message, error) {
	return nil, nil
}
func (dummyMailService) GetMessage(context.Context, uuid.UUID, uuid.UUID) (*mail.Message, *mail.Mailbox, error) {
	return nil, nil, nil
}
func (dummyMailService) UpdateFlags(context.Context, uuid.UUID, bool, bool, bool, bool, bool) error {
	return nil
}
func (dummyMailService) DeleteMessage(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (dummyMailService) SendMessage(context.Context, *identity.User, string, string, string) (*mail.Message, error) {
	return nil, nil
}

type dummySessionService struct{}

func (dummySessionService) Create(context.Context, uuid.UUID, time.Duration) (*identity.Session, error) {
	return nil, nil
}
func (dummySessionService) GetByToken(context.Context, string) (*identity.Session, error) {
	return nil, nil
}
func (dummySessionService) Delete(context.Context, string) error { return nil }

type dummyUserService struct{}

func (dummyUserService) Authenticate(context.Context, string, string) (*identity.User, error) {
	return nil, nil
}
func (dummyUserService) Get(context.Context, uuid.UUID) (*identity.User, error) { return nil, nil }
func (dummyUserService) Update(context.Context, uuid.UUID, string, bool) error  { return nil }
func (dummyUserService) ChangePassword(context.Context, uuid.UUID, string) error { return nil }

func TestMuxRouteRegistrationNoConflict(t *testing.T) {
	mux := http.NewServeMux()

	// Register discovery routes (including /mail/config-v1.1.xml)
	disc := &discovery{mailHost: "mail.example.com", davHost: "mail.example.com"}
	disc.register(mux)

	// Register web UI routes (including /mail, /mail/message/{id}, etc.)
	webUI, err := web.New(os.DirFS("."), dummyContactService{}, dummyCalendarService{}, dummyMailService{}, dummySessionService{}, dummyUserService{}, false)
	if err != nil {
		t.Fatalf("failed to initialize webUI: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("mux route registration panicked with conflict: %v", r)
		}
	}()

	webUI.RegisterRoutes(mux)
}
