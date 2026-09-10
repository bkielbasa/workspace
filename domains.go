package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var (
	ErrDomainNotFound      = errors.New("domain not found")
	ErrDomainAlreadyExists = errors.New("domain already exists")
)

type Domain struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Domains struct {
	db     *sql.DB
	tracer trace.Tracer
}

func NewDomains(db *sql.DB) *Domains {
	return &Domains{db: db, tracer: otel.Tracer("domains")}
}

func (d *Domains) Create(ctx context.Context, name string) (*Domain, error) {
	ctx, span := d.tracer.Start(ctx, "domains.create")
	defer span.End()

	name = strings.ToLower(strings.TrimSpace(name))

	var existing uuid.UUID
	err := d.db.QueryRowContext(ctx, `SELECT id FROM domains WHERE name = $1`, name).Scan(&existing)
	switch {
	case err == nil:
		return nil, ErrDomainAlreadyExists
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("check existing domain: %w", err)
	}

	domain := &Domain{}
	err = d.db.QueryRowContext(
		ctx,
		`INSERT INTO domains (name) VALUES ($1)
		 RETURNING id, name, created_at, updated_at`,
		name,
	).Scan(&domain.ID, &domain.Name, &domain.CreatedAt, &domain.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create domain: %w", err)
	}

	return domain, nil
}

func (d *Domains) Get(ctx context.Context, id uuid.UUID) (*Domain, error) {
	ctx, span := d.tracer.Start(ctx, "domains.get")
	defer span.End()

	domain := &Domain{}
	err := d.db.QueryRowContext(
		ctx,
		`SELECT id, name, created_at, updated_at FROM domains WHERE id = $1`,
		id,
	).Scan(&domain.ID, &domain.Name, &domain.CreatedAt, &domain.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDomainNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}

	return domain, nil
}

func (d *Domains) GetByName(ctx context.Context, name string) (*Domain, error) {
	ctx, span := d.tracer.Start(ctx, "domains.get_by_name")
	defer span.End()

	name = strings.ToLower(strings.TrimSpace(name))

	domain := &Domain{}
	err := d.db.QueryRowContext(
		ctx,
		`SELECT id, name, created_at, updated_at FROM domains WHERE name = $1`,
		name,
	).Scan(&domain.ID, &domain.Name, &domain.CreatedAt, &domain.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDomainNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get domain by name: %w", err)
	}

	return domain, nil
}

func (d *Domains) List(ctx context.Context) ([]Domain, error) {
	ctx, span := d.tracer.Start(ctx, "domains.list")
	defer span.End()

	rows, err := d.db.QueryContext(
		ctx,
		`SELECT id, name, created_at, updated_at FROM domains ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()

	domains := make([]Domain, 0)
	for rows.Next() {
		var domain Domain
		if err := rows.Scan(&domain.ID, &domain.Name, &domain.CreatedAt, &domain.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan domain: %w", err)
		}
		domains = append(domains, domain)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate domains: %w", err)
	}

	return domains, nil
}

func (d *Domains) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := d.tracer.Start(ctx, "domains.delete")
	defer span.End()

	result, err := d.db.ExecContext(ctx, `DELETE FROM domains WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rows == 0 {
		return ErrDomainNotFound
	}

	return nil
}

// Exists returns true if the given domain name is registered.
func (d *Domains) Exists(ctx context.Context, name string) (bool, error) {
	ctx, span := d.tracer.Start(ctx, "domains.exists")
	defer span.End()

	name = strings.ToLower(strings.TrimSpace(name))

	var id uuid.UUID
	err := d.db.QueryRowContext(ctx, `SELECT id FROM domains WHERE name = $1`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check domain: %w", err)
	}

	return true, nil
}
