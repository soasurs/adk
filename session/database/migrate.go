package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// migrationTables carries the runtime-resolved names of all tables managed by
// the migration engine. Passing a struct instead of individual string parameters
// keeps the up-function signature stable as new tables are added in future
// migrations.
type migrationTables struct {
	sessions, events, migrations string
}

// migration describes a single, forward-only schema change.
// There is no down function by design: applications handle rollback at their
// own release boundary. Append-only migrations keep the history auditable and
// the engine simple across the supported SQL backends.
type migration struct {
	version int
	up      func(t migrationTables) []string
}

// migrations holds the sequence of schema changes over time.
// New entries must be appended at the end; the version numbers must be
// monotonically increasing integers starting at 1.
var migrations = []migration{
	{
		version: 1,
		up:      migrationV1SQL,
	},
}

// InitSchema creates or updates the necessary tables and indexes for the
// database session service. It tracks schema changes in a migrations table to
// safely apply new columns or tables over time. It accepts the same Option
// values as NewDatabaseSessionService so that both calls target the same set
// of tables.
//
// InitSchema is idempotent: it is safe to call on every application startup.
func InitSchema(ctx context.Context, db *sqlx.DB, opts ...Option) error {
	svc := &databaseSessionService{
		sessionsTable:   defaultSessionsTable,
		eventsTable:     defaultEventsTable,
		migrationsTable: defaultMigrationsTable,
	}
	for _, opt := range opts {
		opt(svc)
	}

	if err := validateTableName(svc.sessionsTable); err != nil {
		return err
	}
	if err := validateTableName(svc.eventsTable); err != nil {
		return err
	}
	if err := validateTableName(svc.migrationsTable); err != nil {
		return err
	}

	t := migrationTables{
		sessions:   svc.sessionsTable,
		events:     svc.eventsTable,
		migrations: svc.migrationsTable,
	}

	// ── 1. Ensure migrations table exists ──────────────────────────────────
	if _, err := db.ExecContext(ctx, createMigrationsTableSQL(t.migrations)); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	// ── 2. Apply pending migrations sequentially ───────────────────────────
	for _, m := range migrations {
		if err := applyMigration(ctx, db, t, m); err != nil {
			return fmt.Errorf("apply migration v%d: %w", m.version, err)
		}
	}

	return nil
}

func applyMigration(ctx context.Context, db *sqlx.DB, t migrationTables, m migration) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // rolled back on commit path too; sql.ErrTxDone is expected

	// Check if this migration version is already applied.
	var exists int
	err = tx.GetContext(ctx, &exists, migrationAppliedSQL(t.migrations), m.version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		// Row found — migration already applied.
		return nil
	}

	// Execute all DDL statements for this migration.
	for _, stmt := range m.up(t) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	// Record that this migration has been applied.
	if _, err := tx.ExecContext(ctx, recordMigrationSQL(t.migrations), m.version); err != nil {
		return err
	}

	return tx.Commit()
}
