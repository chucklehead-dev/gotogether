package payd

import (
	"context"
	"database/sql"
	"fmt"
)

// schema is the service's tables, in SQL that PostgreSQL and SQLite both
// take. An account numbers its charges (n_charges), which names them: a
// charge's id is its account's and its number.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS accounts (
		id TEXT PRIMARY KEY,
		balance BIGINT NOT NULL,
		n_charges BIGINT NOT NULL
	)`,
	// one row per idempotency key: pending from the claim, done with the
	// response it answered
	`CREATE TABLE IF NOT EXISTS idempotency (
		key TEXT PRIMARY KEY,
		req_hash TEXT NOT NULL,
		status TEXT NOT NULL,
		code INTEGER,
		body TEXT,
		charge_id TEXT
	)`,
	`CREATE TABLE IF NOT EXISTS charges (
		id TEXT PRIMARY KEY,
		idem_key TEXT NOT NULL,
		account TEXT NOT NULL,
		amount BIGINT NOT NULL,
		capture_id TEXT NOT NULL
	)`,
}

// Migrate creates the service's tables if they do not exist.
func Migrate(ctx context.Context, db *sql.DB) error {
	for _, q := range schema {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("payd: migrate: %w", err)
		}
	}
	return nil
}
