package mail

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrMessageNotFound = errors.New("message not found")
	ErrMailboxNotFound = errors.New("mailbox not found")
)

type Mailbox struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Name        string
	UIDValidity uint64
	CreatedAt   time.Time
}

type MailboxInfo struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Name        string
	TotalCount  int
	UnreadCount int
}

type Message struct {
	ID         uuid.UUID
	MailboxID  uuid.UUID
	UID        uint64
	MessageID  string
	Sender     string
	Recipients []string
	Subject    string
	InReplyTo  string
	References string
	RawMessage string
	MimeType   string
	Charset    string
	SizeBytes  int64
	Seen       bool
	Flagged    bool
	Answered   bool
	Deleted    bool
	Draft      bool
	ReceivedAt time.Time
	SentAt     *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Thread struct {
	ID      uuid.UUID
	Subject string
	Count   int
}

type OutboxMessage struct {
	ID        string
	Recipient string
	Data      string
	Attempts  int
}

type Label struct {
	ID     uuid.UUID
	UserID uuid.UUID
	Name   string
}
