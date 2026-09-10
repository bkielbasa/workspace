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

const (
	KindHome   = "home"
	KindWork   = "work"
	KindSchool = "school"
)

// ParseKind maps a form value onto one of the three contact kinds.
func ParseKind(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case KindWork:
		return KindWork
	case KindSchool:
		return KindSchool
	default:
		return KindHome
	}
}

// Kind is the user-facing purpose of this field: home, work, or school.
func (f Field) Kind() string {
	for _, t := range f.Type {
		switch strings.ToUpper(strings.TrimSpace(t)) {
		case "WORK":
			return KindWork
		case "SCHOOL", "X-SCHOOL":
			return KindSchool
		case "HOME":
			return KindHome
		}
	}
	return KindHome
}

// KindLabel is the sentence-case label shown in the UI.
func (f Field) KindLabel() string {
	switch f.Kind() {
	case KindWork:
		return "Work"
	case KindSchool:
		return "School"
	default:
		return "Personal"
	}
}

// TypeForKind is the vCard TYPE token for a UI kind.
func TypeForKind(kind string) string {
	switch ParseKind(kind) {
	case KindWork:
		return "WORK"
	case KindSchool:
		return "SCHOOL"
	default:
		return "HOME"
	}
}

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
	if len(c.Phones) > 0 {
		return c.Phones[0].Value
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
