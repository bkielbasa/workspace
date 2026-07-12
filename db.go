package main

import (
    "database/sql"
    "fmt"
    "time"

    "github.com/XSAM/otelsql"
    _ "github.com/jackc/pgx/v5/stdlib"
)

func OpenDB(databaseURL string) (*sql.DB, error) {
    // wrap pgx with OpenTelemetry instrumentation
    db, err := otelsql.Open("pgx", databaseURL,
        otelsql.WithAttributes(),
    )
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return db, nil
}
