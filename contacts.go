package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Contact struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Email     string // primary email (first in Emails)
	Name      string
	FirstName string
	LastName  string
	Company   string
	Title     string
	UID       string // vCard UID; regenerated from ID when a card has none
	Emails    []VCardField
	Phones    []VCardField
	VCard     string
	ETag      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DisplayName is used by page templates.
func (c Contact) DisplayName() string { return c.displayName() }

const contactColumns = `id, user_id, email, name, first_name, last_name, company, title, emails, phones, vcard, etag, created_at, updated_at`

type scanRow interface {
	Scan(dest ...any) error
}

func scanContact(row scanRow) (Contact, error) {
	var ct Contact
	var emails, phones []byte
	err := row.Scan(
		&ct.ID, &ct.UserID, &ct.Email, &ct.Name,
		&ct.FirstName, &ct.LastName, &ct.Company, &ct.Title,
		&emails, &phones, &ct.VCard, &ct.ETag, &ct.CreatedAt, &ct.UpdatedAt,
	)
	if err != nil {
		return ct, err
	}
	if len(emails) > 0 {
		_ = json.Unmarshal(emails, &ct.Emails)
	}
	if len(phones) > 0 {
		_ = json.Unmarshal(phones, &ct.Phones)
	}
	return ct, nil
}

func (ct *Contact) marshalLists() ([]byte, []byte, error) {
	emails, err := json.Marshal(ct.Emails)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal emails: %w", err)
	}
	phones, err := json.Marshal(ct.Phones)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal phones: %w", err)
	}
	return emails, phones, nil
}

type Contacts struct {
	db *sql.DB
}

func (c *Contacts) List(ctx context.Context, userID uuid.UUID) ([]Contact, error) {
	rows, err := c.db.QueryContext(ctx, `
        SELECT `+contactColumns+`
        FROM contacts
        WHERE user_id = $1
        ORDER BY updated_at DESC
    `, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []Contact
	for rows.Next() {
		ct, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		res = append(res, ct)
	}
	return res, rows.Err()
}

func (c *Contacts) Get(ctx context.Context, userID, contactID uuid.UUID) (Contact, error) {
	row := c.db.QueryRowContext(ctx, `
        SELECT `+contactColumns+`
        FROM contacts
        WHERE id = $1 AND user_id = $2
    `, contactID, userID)
	return scanContact(row)
}

func (c *Contacts) ByEmail(ctx context.Context, userID uuid.UUID, email string) (Contact, error) {
	row := c.db.QueryRowContext(ctx, `
        SELECT `+contactColumns+`
        FROM contacts
        WHERE user_id = $1 AND email = $2
    `, userID, email)
	return scanContact(row)
}

// Put inserts or updates a contact. If id is nil the primary email picks the
// row (one contact per primary email); otherwise the resource id does, which is
// how CardDAV clients update a specific card.
// PutContact inserts or updates a contact from its structured fields (web UI).
// The card is rebuilt from those fields, preserving every line CardDAV sent
// that the model does not regenerate (ADR, NOTE, BDAY, ...).
func (c *Contacts) PutContact(ctx context.Context, userID uuid.UUID, id *uuid.UUID, ct Contact) (*Contact, error) {
	resolvePrimaryEmail(&ct)

	var prev string
	if id != nil && *id != uuid.Nil {
		if old, err := c.Get(ctx, userID, *id); err == nil {
			prev = old.VCard
		}
	} else if ct.Email != "" {
		if old, err := c.ByEmail(ctx, userID, ct.Email); err == nil && old.ID != uuid.Nil {
			prev = old.VCard
		}
	}
	ct.VCard = buildVCard(ct, prev)
	return c.persist(ctx, userID, id, ct)
}

// PutCard stores an incoming vCard (CardDAV PUT) verbatim; the client is the
// authority on the card contents, so raw fields are never dropped.
func (c *Contacts) PutCard(ctx context.Context, userID uuid.UUID, id *uuid.UUID, ct Contact) (*Contact, error) {
	resolvePrimaryEmail(&ct)
	if strings.TrimSpace(ct.VCard) == "" {
		ct.VCard = buildVCard(ct, "")
	}
	return c.persist(ctx, userID, id, ct)
}

func resolvePrimaryEmail(ct *Contact) {
	if len(ct.Emails) > 0 {
		ct.Email = ct.Emails[0].Value
	}
	ct.Email = strings.TrimSpace(ct.Email)
}

// persist writes a contact row, replacing on the primary email when no id is
// given (one contact per primary email) or on the resource id otherwise.
func (c *Contacts) persist(ctx context.Context, userID uuid.UUID, id *uuid.UUID, ct Contact) (*Contact, error) {
	ct.Name = ct.DisplayName()
	ct.Email = strings.TrimSpace(ct.Email)
	etag := uuid.New().String()

	emailsJSON, phonesJSON, err := ct.marshalLists()
	if err != nil {
		return nil, err
	}

	var saved Contact
	if id != nil && *id != uuid.Nil {
		row := c.db.QueryRowContext(ctx, `
            INSERT INTO contacts (id, user_id, email, name, first_name, last_name, company, title, emails, phones, vcard, etag)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
            ON CONFLICT (id) DO UPDATE SET
                email = EXCLUDED.email,
                name = EXCLUDED.name,
                first_name = EXCLUDED.first_name,
                last_name = EXCLUDED.last_name,
                company = EXCLUDED.company,
                title = EXCLUDED.title,
                emails = EXCLUDED.emails,
                phones = EXCLUDED.phones,
                vcard = EXCLUDED.vcard,
                etag = EXCLUDED.etag,
                updated_at = NOW()
            RETURNING `+contactColumns,
			*id, userID, ct.Email, ct.Name, ct.FirstName, ct.LastName, ct.Company, ct.Title,
			emailsJSON, phonesJSON, ct.VCard, etag,
		)
		saved, err = scanContact(row)
	} else {
		row := c.db.QueryRowContext(ctx, `
            INSERT INTO contacts (user_id, email, name, first_name, last_name, company, title, emails, phones, vcard, etag)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
            ON CONFLICT (user_id, email) WHERE email <> ''
            DO UPDATE SET
                name = EXCLUDED.name,
                first_name = EXCLUDED.first_name,
                last_name = EXCLUDED.last_name,
                company = EXCLUDED.company,
                title = EXCLUDED.title,
                emails = EXCLUDED.emails,
                phones = EXCLUDED.phones,
                vcard = EXCLUDED.vcard,
                etag = EXCLUDED.etag,
                updated_at = NOW()
            RETURNING `+contactColumns,
			userID, ct.Email, ct.Name, ct.FirstName, ct.LastName, ct.Company, ct.Title,
			emailsJSON, phonesJSON, ct.VCard, etag,
		)
		saved, err = scanContact(row)
	}
	return &saved, nil
}

func (c *Contacts) Delete(ctx context.Context, userID uuid.UUID, email string) error {
	_, err := c.db.ExecContext(ctx, `
        DELETE FROM contacts
        WHERE user_id = $1 AND email = $2
    `, userID, email)
	return err
}

func (c *Contacts) DeleteByID(ctx context.Context, userID, contactID uuid.UUID) error {
	_, err := c.db.ExecContext(ctx, `
        DELETE FROM contacts
        WHERE id = $1 AND user_id = $2
    `, contactID, userID)
	return err
}
