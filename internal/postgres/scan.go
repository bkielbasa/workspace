package postgres

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/mail"
)

type scanRow interface {
	Scan(dest ...any) error
}

const contactColumns = `id, user_id, email, name, first_name, last_name, company, title, emails, phones, vcard, etag, created_at, updated_at`

func scanContact(row scanRow) (contacts.Contact, error) {
	var contact contacts.Contact
	var emails, phones []byte
	err := row.Scan(
		&contact.ID, &contact.UserID, &contact.Email, &contact.Name,
		&contact.FirstName, &contact.LastName, &contact.Company, &contact.Title,
		&emails, &phones, &contact.VCard, &contact.ETag, &contact.CreatedAt, &contact.UpdatedAt,
	)
	if err != nil {
		return contact, err
	}
	if len(emails) > 0 {
		_ = json.Unmarshal(emails, &contact.Emails)
	}
	if len(phones) > 0 {
		_ = json.Unmarshal(phones, &contact.Phones)
	}
	return contact, nil
}

const eventColumns = `id, user_id, title, COALESCE(location, ''), COALESCE(description, ''), starts_at, ends_at, COALESCE(uid, ''), COALESCE(ics, ''), COALESCE(resource, id::text), etag, COALESCE(attendees, '{}'), COALESCE(sequence, 0), created_at, updated_at`

func scanEvent(row scanRow) (calendar.Event, error) {
	var event calendar.Event
	var starts, ends sql.NullTime
	var attendees sql.NullString
	err := row.Scan(
		&event.ID, &event.UserID, &event.Title, &event.Location, &event.Description,
		&starts, &ends, &event.UID, &event.ICS, &event.Resource, &event.ETag,
		&attendees, &event.Sequence,
		&event.CreatedAt, &event.UpdatedAt,
	)
	if err != nil {
		return event, err
	}
	event.StartsAt = starts.Time
	event.EndsAt = ends.Time
	event.Attendees = parseTextArray(attendees.String)
	return event, nil
}

func parseRecipients(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	recipients := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			recipients = append(recipients, part)
		}
	}
	return recipients
}

// parseTextArray decodes a Postgres text[] literal ({a,"b,c",NULL}).
// The pgx stdlib driver hands arrays to database/sql as their literal
// string form, so structs cannot scan them into []string directly.
func parseTextArray(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil
	}
	if !strings.HasPrefix(raw, "{") || !strings.HasSuffix(raw, "}") {
		return nil
	}
	inner := raw[1 : len(raw)-1]
	var out []string
	var cur strings.Builder
	inQuotes := false
	escaped := false
	flush := func() {
		out = append(out, cur.String())
		cur.Reset()
	}
	for _, r := range inner {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuotes = !inQuotes
		case r == ',' && !inQuotes:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	var clean []string
	for _, v := range out {
		if v == "NULL" {
			continue
		}
		clean = append(clean, v)
	}
	return clean
}

func scanMessage(row scanRow, includeBodyMetadata bool) (mail.Message, error) {
	var message mail.Message
	var recipients string
	dest := []any{
		&message.ID, &message.MailboxID, &message.UID, &message.MessageID,
		&message.Sender, &recipients, &message.Subject, &message.InReplyTo,
		&message.References, &message.RawMessage,
	}
	if includeBodyMetadata {
		dest = append(dest, &message.MimeType, &message.Charset)
	}
	dest = append(dest,
		&message.SizeBytes, &message.Seen, &message.Flagged, &message.Answered,
		&message.Deleted, &message.Draft, &message.ReceivedAt, &message.SentAt,
		&message.CreatedAt, &message.UpdatedAt,
	)
	if err := row.Scan(dest...); err != nil {
		return message, err
	}
	message.Recipients = parseRecipients(recipients)
	// Repairs messages stored before the ingest separator fix.
	message.RawMessage = mail.EnsureHeaderBodySeparator(message.RawMessage)
	return message, nil
}
