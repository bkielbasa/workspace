package postgres

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/XSAM/otelsql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// OpenDB creates a PostgreSQL connection pool instrumented with OpenTelemetry.
func OpenDB(databaseURL string) (*sql.DB, error) {
	db, err := otelsql.Open("pgx", databaseURL, otelsql.WithAttributes())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

// Open is a concise alias for OpenDB.
func Open(databaseURL string) (*sql.DB, error) {
	return OpenDB(databaseURL)
}
