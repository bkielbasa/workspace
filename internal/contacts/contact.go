package contacts

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidContact = errors.New("invalid contact")

// Field is one repeating property (email, phone, ...).
type Field struct {
	Type   []string `json:"type,omitempty"`
	Value  string   `json:"value"`
	Header string   `json:"header,omitempty"`
}

func (f Field) TypeLabel() string { return strings.Join(f.Type, "/") }

// Contact is format-agnostic. VCard holds a round-trip payload when one exists.
type Contact struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Email     string
	Name      string
	FirstName string
	LastName  string
	Company   string
	Title     string
	UID       string
	Emails    []Field
	Phones    []Field
	VCard     string
	ETag      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (c Contact) DisplayName() string {
	first := strings.TrimSpace(c.FirstName)
	last := strings.TrimSpace(c.LastName)
	if first != "" || last != "" {
		return strings.TrimSpace(first + " " + last)
	}
	if company := strings.TrimSpace(c.Company); company != "" {
		return company
	}
	if name := strings.TrimSpace(c.Name); name != "" {
		return name
	}
	if len(c.Emails) > 0 {
		return c.Emails[0].Value
	}
	return c.Email
}

// Normalize derives primary email and display name before persist.
func Normalize(ct *Contact) {
	if len(ct.Emails) > 0 {
		ct.Email = ct.Emails[0].Value
	}
	ct.Email = strings.TrimSpace(ct.Email)
	ct.Name = ct.DisplayName()
}
