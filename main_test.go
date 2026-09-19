package main

import (
	"context"
	"crypto/tls"
	"testing"

	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

type mockMainMailStore struct{}

func (m *mockMainMailStore) Append(ctx context.Context, message *mail.Message) error {
	return nil
}
func (m *mockMainMailStore) Get(ctx context.Context, id uuid.UUID) (*mail.Message, error) {
	return nil, nil
}
func (m *mockMainMailStore) GetForUser(ctx context.Context, userID, id uuid.UUID) (*mail.Message, *mail.Mailbox, error) {
	return nil, nil, nil
}
func (m *mockMainMailStore) List(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error) {
	return nil, nil
}
func (m *mockMainMailStore) ListSummary(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error) {
	return nil, nil
}
func (m *mockMainMailStore) UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error {
	return nil
}
func (m *mockMainMailStore) Delete(ctx context.Context, id uuid.UUID) error { return nil }
func (m *mockMainMailStore) Move(ctx context.Context, id, mailboxID uuid.UUID) error {
	return nil
}
func (m *mockMainMailStore) Copy(ctx context.Context, id, mailboxID uuid.UUID) (*mail.Message, error) {
	return nil, nil
}

type mockMainMailboxStore struct{}

func (m *mockMainMailboxStore) Create(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	return nil, nil
}
func (m *mockMainMailboxStore) CreateDefault(ctx context.Context, userID uuid.UUID) error {
	return nil
}
func (m *mockMainMailboxStore) GetByName(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	return nil, nil
}
func (m *mockMainMailboxStore) List(ctx context.Context, userID uuid.UUID) ([]mail.Mailbox, error) {
	return nil, nil
}
func (m *mockMainMailboxStore) ListWithCounts(ctx context.Context, userID uuid.UUID) ([]mail.MailboxInfo, error) {
	return nil, nil
}
func (m *mockMainMailboxStore) EnsureDefaults(ctx context.Context, userID uuid.UUID) error {
	return nil
}

func TestMainWiring(t *testing.T) {
	tlsCfg := &tls.Config{}
	notesSvc := notes.NewService(nil, notes.NewBroker())
	msgStore := &mockMainMailStore{}
	mboxStore := &mockMainMailboxStore{}

	cleartext, tlsServer, bridge := wireIMAPServers(nil, msgStore, mboxStore, notesSvc, tlsCfg)

	if bridge == nil {
		t.Fatal("main.go not yet wired: imap notes bridge is nil")
	}
	if cleartext == nil || cleartext.NotesBridge() == nil {
		t.Fatal("main.go not yet wired: cleartext IMAP server has no notes bridge")
	}
	if tlsServer == nil || tlsServer.NotesBridge() == nil {
		t.Fatal("main.go not yet wired: TLS IMAP server has no notes bridge")
	}
}
