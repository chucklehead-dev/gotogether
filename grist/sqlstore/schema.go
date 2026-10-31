package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// A migration is one version of grist's schema: the statements that take
// a database from the version before it to it, every dialect's alike.
type migration struct {
	version int64
	why     string
	stmts   []string
}

// migrations are the supported schema's versions, in order (read-only).
// Version 2 starts with immutable event identities. Version 1 and unversioned
// pre-release tables cannot safely be backfilled: their events already caused
// effects under position-based keys. Initialize a fresh database instead.
var migrations = []migration{
	{2, "grist's tables with immutable event identities", schema},
}

// SchemaVersion is the schema version this sqlstore initializes. Open refuses
// older schemas whose stored events have no immutable identity, and newer ones.
const SchemaVersion = 2

// schemaTable is the schema's version, one row (id 1). Its row is also
// the migrations' lock: a migration updates it first, so two nodes
// opening one database at once (two pods of a rollout) migrate it once,
// the second waiting for the first and then finding nothing to do.
const schemaTable = `CREATE TABLE IF NOT EXISTS grist_schema (id BIGINT NOT NULL, version BIGINT NOT NULL, PRIMARY KEY (id))`

// migrate brings db's schema to SchemaVersion under the schema row's lock
// (`UPDATE grist_schema ... RETURNING version`, as an append takes the
// tenant's head: the dialects' one statement that locks on all three), each
// migration and its version in one transaction. A database at a later
// version is refused.
func migrate(ctx context.Context, db *sql.DB) error { return migrateTo(ctx, db, migrations) }

// migrateTo is migrate to the last of ms.
func migrateTo(ctx context.Context, db *sql.DB, ms []migration) error {
	for try := 1; ; try++ {
		err := migrateOnce(ctx, db, ms)
		if err == nil || !store.Transient(err) || try >= 10 || ctx.Err() != nil {
			return err
		}
		// a lost race at boot: PostgreSQL's CREATE TABLE IF NOT EXISTS of
		// two sessions at once can fail one with 23505, a serializable
		// level's lock with 40001
		qenv.Sleep(ctx, time.Duration(try)*50*time.Millisecond)
	}
}

// errNewerSchema is a database a newer grist migrated.
var errNewerSchema = errors.New("sqlstore: the database's schema is newer than this binary's")

var errOlderSchema = errors.New("sqlstore: the database's schema has no immutable event identities")

// migrateOnce applies the migrations after the database's version, under
// the schema row's lock, in one transaction.
func migrateOnce(ctx context.Context, db *sql.DB, ms []migration) error {
	last := ms[len(ms)-1].version
	if _, err := db.ExecContext(ctx, schemaTable); err != nil {
		return fmt.Errorf("sqlstore: creating grist_schema: %w", classify(err))
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO grist_schema (id, version) VALUES (1, 0) ON CONFLICT (id) DO NOTHING`); err != nil {
		return fmt.Errorf("sqlstore: grist_schema's row: %w", classify(err))
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return classify(err)
	}
	defer tx.Rollback()
	var v int64
	if err := tx.QueryRowContext(ctx, `UPDATE grist_schema SET version = version + $1 WHERE id = $2 RETURNING version`, 0, 1).Scan(&v); err != nil {
		return classify(err)
	}
	if v > last {
		return fmt.Errorf("%w: it is at version %d, this sqlstore migrates to %d: run a binary built with the grist that migrated it (a rollback past a migration needs the newer binary's down migration, which grist does not write)", errNewerSchema, v, last)
	}
	if v > 0 && v < ms[0].version {
		return store.Permanent(fmt.Errorf("%w: version %d cannot be backfilled safely: initialize a fresh schema with this prerelease; existing events may already have caused effects under position-based keys", errOlderSchema, v))
	}
	if v == last {
		return nil
	}
	for _, m := range ms {
		if m.version <= v {
			continue
		}
		for _, q := range m.stmts {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				if v == 0 && strings.Contains(err.Error(), "already exists") {
					return store.Permanent(fmt.Errorf("%w: unversioned tables cannot be backfilled safely: initialize a fresh schema with this prerelease: %v", errOlderSchema, err))
				}
				return fmt.Errorf("sqlstore: migration %d (%s): %s: %w", m.version, m.why, q, classify(err))
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE grist_schema SET version = $1 WHERE id = $2`, m.version, 1); err != nil {
			return classify(err)
		}
	}
	return classify(tx.Commit())
}

// Version is the database's schema version (grist_schema).
func (s *Store) Version(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `SELECT version FROM grist_schema WHERE id = $1`, 1).Scan(&v)
	return v, classify(err)
}

// Retention is what Prune deletes: step rows committed before
// StepsBefore, markers of commits before MarkersBefore (the zero time:
// none).
type Retention struct {
	StepsBefore, MarkersBefore time.Time
}

// Pruned is what a Prune deleted.
type Pruned struct {
	Steps, Markers int64
}

// Prune deletes what r's horizons put out of the tables that grow with
// every commit, for every tenant: grist_steps' rows committed before
// r.StepsBefore (each tenant's last row kept, which a running relay reads
// to tell a pruned log from one that went backwards), and grist_markers'
// markers at positions whose events were appended before r.MarkersBefore
// (a key's first answer is final only within the horizon: a retry after
// it runs the command again; give one far longer than any client
// retries). A node started after a prune records a tenant's step log from
// its oldest row kept: export the steps first (`<app> steps -export`)
// where a whole history is wanted. Events, the index, checkpoints and
// snapshots are never pruned.
func (s *Store) Prune(ctx context.Context, r Retention) (Pruned, error) {
	var out Pruned
	ts, err := s.Tenants(ctx)
	if err != nil {
		return out, err
	}
	for _, t := range ts {
		if !r.StepsBefore.IsZero() {
			n, err := s.StepCount(ctx, t)
			if err != nil {
				return out, err
			}
			res, err := s.db.ExecContext(ctx, `DELETE FROM grist_steps WHERE tenant = $1 AND seq < $2 AND at_ns < $3`, t, n, r.StepsBefore.UnixNano())
			if err != nil {
				return out, classify(err)
			}
			k, _ := res.RowsAffected()
			out.Steps += k
		}
		if !r.MarkersBefore.IsZero() {
			var p int64
			err := s.db.QueryRowContext(ctx, `SELECT pos FROM grist_events WHERE tenant = $1 AND at_ms < $2 ORDER BY pos DESC LIMIT 1`, t, r.MarkersBefore.UnixMilli()).Scan(&p)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				continue
			case err != nil:
				return out, classify(err)
			}
			res, err := s.db.ExecContext(ctx, `DELETE FROM grist_markers WHERE tenant = $1 AND pos <= $2`, t, p)
			if err != nil {
				return out, classify(err)
			}
			k, _ := res.RowsAffected()
			out.Markers += k
		}
	}
	return out, nil
}
