package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Contact struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Email     string
	Name      string
	FirstName string
	LastName  string
	Company   string
	Title     string
	Phone     string
	VCard     string
	ETag      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DisplayName is used by page templates.
func (c Contact) DisplayName() string { return c.displayName() }

const contactColumns = `id, user_id, email, name, first_name, last_name, company, title, phone, vcard, etag, created_at, updated_at`

type scanRow interface {
	Scan(dest ...any) error
}

func scanContact(row scanRow) (Contact, error) {
	var ct Contact
	err := row.Scan(
		&ct.ID, &ct.UserID, &ct.Email, &ct.Name,
		&ct.FirstName, &ct.LastName, &ct.Company, &ct.Title, &ct.Phone,
		&ct.VCard, &ct.ETag, &ct.CreatedAt, &ct.UpdatedAt,
	)
	return ct, err
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
// row (one contact per email); otherwise the resource id does, which is how
// CardDAV clients update a specific card. The card is rebuilt via buildVCard,
// preserving any CardDAV fields the caller did not supply.
func (c *Contacts) Put(ctx context.Context, userID uuid.UUID, id *uuid.UUID, ct Contact) (*Contact, error) {
	if strings.TrimSpace(ct.VCard) == "" {
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
	}
	ct.Email = strings.TrimSpace(ct.Email)
	ct.Name = ct.DisplayName()
	etag := uuid.New().String()

	saved := &Contact{}
	var err error
	if id != nil && *id != uuid.Nil {
		err = c.db.QueryRowContext(ctx, `
            INSERT INTO contacts (id, user_id, email, name, first_name, last_name, company, title, phone, vcard, etag)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
            ON CONFLICT (id) DO UPDATE SET
                email = EXCLUDED.email,
                name = EXCLUDED.name,
                first_name = EXCLUDED.first_name,
                last_name = EXCLUDED.last_name,
                company = EXCLUDED.company,
                title = EXCLUDED.title,
                phone = EXCLUDED.phone,
                vcard = EXCLUDED.vcard,
                etag = EXCLUDED.etag,
                updated_at = NOW()
            RETURNING `+contactColumns,
			*id, userID, ct.Email, ct.Name, ct.FirstName, ct.LastName, ct.Company, ct.Title, ct.Phone, ct.VCard, etag,
		).Scan(
			&saved.ID, &saved.UserID, &saved.Email, &saved.Name,
			&saved.FirstName, &saved.LastName, &saved.Company, &saved.Title, &saved.Phone,
			&saved.VCard, &saved.ETag, &saved.CreatedAt, &saved.UpdatedAt,
		)
	} else {
		err = c.db.QueryRowContext(ctx, `
            INSERT INTO contacts (user_id, email, name, first_name, last_name, company, title, phone, vcard, etag)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
            ON CONFLICT (user_id, email) WHERE email <> ''
            DO UPDATE SET
                name = EXCLUDED.name,
                first_name = EXCLUDED.first_name,
                last_name = EXCLUDED.last_name,
                company = EXCLUDED.company,
                title = EXCLUDED.title,
                phone = EXCLUDED.phone,
                vcard = EXCLUDED.vcard,
                etag = EXCLUDED.etag,
                updated_at = NOW()
            RETURNING `+contactColumns,
			userID, ct.Email, ct.Name, ct.FirstName, ct.LastName, ct.Company, ct.Title, ct.Phone, ct.VCard, etag,
		).Scan(
			&saved.ID, &saved.UserID, &saved.Email, &saved.Name,
			&saved.FirstName, &saved.LastName, &saved.Company, &saved.Title, &saved.Phone,
			&saved.VCard, &saved.ETag, &saved.CreatedAt, &saved.UpdatedAt,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("put contact: %w", err)
	}
	return saved, nil
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
