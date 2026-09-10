package web_test

import (
	"context"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
)

func TestNewParsesEmbeddedWebFiles(t *testing.T) {
	files := os.DirFS("../..")
	if _, err := fs.Stat(files, "web/templates/contact-edit.html"); err != nil {
		t.Fatalf("test filesystem: %v", err)
	}

	if _, err := web.New(files, contactService{}, calendarService{}, sessionService{}, userService{}, false); err != nil {
		t.Fatalf("web.New() error = %v", err)
	}
}

type contactService struct{}

func (contactService) List(context.Context, uuid.UUID) ([]contacts.Contact, error) {
	return nil, nil
}

func (contactService) Get(context.Context, uuid.UUID, uuid.UUID) (contacts.Contact, error) {
	return contacts.Contact{}, nil
}

func (contactService) PutStructured(context.Context, uuid.UUID, *uuid.UUID, contacts.Contact) (*contacts.Contact, error) {
	return &contacts.Contact{}, nil
}

func (contactService) DeleteByID(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

type calendarService struct{}

func (calendarService) List(context.Context, uuid.UUID) ([]calendar.Event, error) {
	return nil, nil
}

func (calendarService) Put(context.Context, calendar.Event) (*calendar.Event, error) {
	return &calendar.Event{}, nil
}

func (calendarService) Delete(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

type sessionService struct{}

func (sessionService) Create(context.Context, uuid.UUID, time.Duration) (*identity.Session, error) {
	return &identity.Session{}, nil
}

func (sessionService) GetByToken(context.Context, string) (*identity.Session, error) {
	return &identity.Session{}, nil
}

func (sessionService) Delete(context.Context, string) error {
	return nil
}

type userService struct{}

func (userService) Authenticate(context.Context, string, string) (*identity.User, error) {
	return &identity.User{}, nil
}

func (userService) Get(context.Context, uuid.UUID) (*identity.User, error) {
	return &identity.User{}, nil
}
