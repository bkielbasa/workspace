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

const eventColumns = `id, user_id, title, starts_at, ends_at, COALESCE(uid, ''), COALESCE(ics, ''), COALESCE(resource, id::text), etag, created_at, updated_at`

func scanEvent(row scanRow) (calendar.Event, error) {
	var event calendar.Event
	var starts, ends sql.NullTime
	err := row.Scan(
		&event.ID, &event.UserID, &event.Title, &starts, &ends,
		&event.UID, &event.ICS, &event.Resource, &event.ETag,
		&event.CreatedAt, &event.UpdatedAt,
	)
	if err != nil {
		return event, err
	}
	event.StartsAt = starts.Time
	event.EndsAt = ends.Time
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
	return message, nil
}
