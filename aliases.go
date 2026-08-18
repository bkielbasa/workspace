package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrAliasNotFound      = errors.New("alias not found")
	ErrAliasAlreadyExists = errors.New("alias already exists")
)

type Alias struct {
	ID          uuid.UUID `json:"id"`
	DomainID    uuid.UUID `json:"domain_id"`
	Address     string    `json:"address"`
	Destination string    `json:"destination"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Aliases struct {
	db      *sql.DB
	domains *Domains
}

func (a *Aliases) Create(ctx context.Context, domainID uuid.UUID, address, destination string) (*Alias, error) {
	address = strings.ToLower(strings.TrimSpace(address))
	destination = strings.ToLower(strings.TrimSpace(destination))

	// ensure domain exists
	if _, err := a.domains.Get(ctx, domainID); err != nil {
		return nil, fmt.Errorf("domain lookup: %w", err)
	}

	var existing uuid.UUID
	err := a.db.QueryRowContext(ctx, `SELECT id FROM aliases WHERE address = $1`, address).Scan(&existing)
	switch {
	case err == nil:
		return nil, ErrAliasAlreadyExists
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("check existing alias: %w", err)
	}

	alias := &Alias{}
	err = a.db.QueryRowContext(
		ctx,
		`INSERT INTO aliases (domain_id, address, destination)
		 VALUES ($1, $2, $3)
		 RETURNING id, domain_id, address, destination, created_at, updated_at`,
		domainID, address, destination,
	).Scan(&alias.ID, &alias.DomainID, &alias.Address, &alias.Destination, &alias.CreatedAt, &alias.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create alias: %w", err)
	}

	return alias, nil
}

func (a *Aliases) Get(ctx context.Context, id uuid.UUID) (*Alias, error) {
	alias := &Alias{}
	err := a.db.QueryRowContext(
		ctx,
		`SELECT id, domain_id, address, destination, created_at, updated_at
		 FROM aliases WHERE id = $1`,
		id,
	).Scan(&alias.ID, &alias.DomainID, &alias.Address, &alias.Destination, &alias.CreatedAt, &alias.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAliasNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get alias: %w", err)
	}

	return alias, nil
}

func (a *Aliases) ListByDomain(ctx context.Context, domainID uuid.UUID) ([]Alias, error) {
	rows, err := a.db.QueryContext(
		ctx,
		`SELECT id, domain_id, address, destination, created_at, updated_at
		 FROM aliases WHERE domain_id = $1 ORDER BY address`,
		domainID,
	)
	if err != nil {
		return nil, fmt.Errorf("list aliases: %w", err)
	}
	defer rows.Close()

	aliases := make([]Alias, 0)
	for rows.Next() {
		var alias Alias
		if err := rows.Scan(&alias.ID, &alias.DomainID, &alias.Address, &alias.Destination, &alias.CreatedAt, &alias.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan alias: %w", err)
		}
		aliases = append(aliases, alias)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate aliases: %w", err)
	}

	return aliases, nil
}

func (a *Aliases) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := a.db.ExecContext(ctx, `DELETE FROM aliases WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete alias: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rows == 0 {
		return ErrAliasNotFound
	}

	return nil
}

// Resolve looks up an alias by its address and returns the destination email, or empty string if not found.
func (a *Aliases) Resolve(ctx context.Context, address string) (string, error) {
	address = strings.ToLower(strings.TrimSpace(address))

	var destination string
	err := a.db.QueryRowContext(
		ctx,
		`SELECT destination FROM aliases WHERE address = $1`,
		address,
	).Scan(&destination)

	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve alias: %w", err)
	}

	return destination, nil
}
