package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/google/uuid"
)

type contactRepository struct{ db *sql.DB }

func NewContactRepository(db *sql.DB) contacts.Repository {
	return &contactRepository{db: db}
}

func (r *contactRepository) List(ctx context.Context, userID uuid.UUID) ([]contacts.Contact, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+contactColumns+` FROM contacts
		WHERE user_id = $1 ORDER BY updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []contacts.Contact
	for rows.Next() {
		contact, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, contact)
	}
	return result, rows.Err()
}

func (r *contactRepository) Get(ctx context.Context, userID, contactID uuid.UUID) (contacts.Contact, error) {
	return scanContact(r.db.QueryRowContext(ctx, `
		SELECT `+contactColumns+` FROM contacts WHERE id = $1 AND user_id = $2
	`, contactID, userID))
}

func (r *contactRepository) ByEmail(ctx context.Context, userID uuid.UUID, email string) (contacts.Contact, error) {
	return scanContact(r.db.QueryRowContext(ctx, `
		SELECT `+contactColumns+` FROM contacts WHERE user_id = $1 AND email = $2
	`, userID, email))
}

func (r *contactRepository) Put(ctx context.Context, userID uuid.UUID, id *uuid.UUID, contact contacts.Contact) (*contacts.Contact, error) {
	emails, err := json.Marshal(contact.Emails)
	if err != nil {
		return nil, fmt.Errorf("marshal emails: %w", err)
	}
	phones, err := json.Marshal(contact.Phones)
	if err != nil {
		return nil, fmt.Errorf("marshal phones: %w", err)
	}
	etag := uuid.NewString()

	var row *sql.Row
	if id != nil && *id != uuid.Nil {
		row = r.db.QueryRowContext(ctx, `
			INSERT INTO contacts (
				id, user_id, email, name, first_name, last_name, company,
				title, emails, phones, vcard, etag
			)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			ON CONFLICT (id) DO UPDATE SET
				email=EXCLUDED.email, name=EXCLUDED.name,
				first_name=EXCLUDED.first_name, last_name=EXCLUDED.last_name,
				company=EXCLUDED.company, title=EXCLUDED.title,
				emails=EXCLUDED.emails, phones=EXCLUDED.phones,
				vcard=EXCLUDED.vcard, etag=EXCLUDED.etag, updated_at=NOW()
			RETURNING `+contactColumns,
			*id, userID, contact.Email, contact.Name, contact.FirstName,
			contact.LastName, contact.Company, contact.Title, emails, phones,
			contact.VCard, etag,
		)
	} else {
		row = r.db.QueryRowContext(ctx, `
			INSERT INTO contacts (
				user_id, email, name, first_name, last_name, company,
				title, emails, phones, vcard, etag
			)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (user_id, email) WHERE email <> '' DO UPDATE SET
				name=EXCLUDED.name, first_name=EXCLUDED.first_name,
				last_name=EXCLUDED.last_name, company=EXCLUDED.company,
				title=EXCLUDED.title, emails=EXCLUDED.emails,
				phones=EXCLUDED.phones, vcard=EXCLUDED.vcard,
				etag=EXCLUDED.etag, updated_at=NOW()
			RETURNING `+contactColumns,
			userID, contact.Email, contact.Name, contact.FirstName,
			contact.LastName, contact.Company, contact.Title, emails, phones,
			contact.VCard, etag,
		)
	}
	saved, err := scanContact(row)
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

func (r *contactRepository) Delete(ctx context.Context, userID uuid.UUID, email string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM contacts WHERE user_id = $1 AND email = $2`, userID, email)
	return err
}

func (r *contactRepository) DeleteByID(ctx context.Context, userID, contactID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM contacts WHERE id = $1 AND user_id = $2`, contactID, userID)
	return err
}
