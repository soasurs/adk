package database

const (
	defaultSessionsTable   = "sessions"
	defaultEventsTable     = "events"
	defaultMigrationsTable = "schema_migrations"
)

// queries holds all pre-built SQL expressions for a set of table names.
type queries struct {
	createSession       string
	getSession          string
	deleteSession       string
	createEvent         string
	deleteEvent         string
	getEvents           string
	listEvents          string
	listCompactedEvents string
	compactActiveEvents string
	compactEventsBefore string
}

// buildQueries constructs SQL expressions using the provided table names.
// Table names are validated before this function is called.
func buildQueries(sessionsTable, eventsTable string) *queries {
	return &queries{
		createSession: `
			INSERT INTO ` + sessionsTable + ` (
				session_id,
				app_id,
				user_id,
				created_at,
				updated_at,
				deleted_at
			)
			VALUES ($1, $2, $3, $4, $5, $6)
		`,
		getSession: `
			SELECT *
			FROM ` + sessionsTable + `
			WHERE session_id = $1
				AND deleted_at = $2
			LIMIT 1
		`,
		deleteSession: `
			UPDATE ` + sessionsTable + `
			SET deleted_at = $1
			WHERE session_id = $2
				AND deleted_at = $3
		`,
		createEvent: `
			INSERT INTO ` + eventsTable + ` (
				event_id,
				session_id,
				author,
				role,
				text,
				reasoning_text,
				tool_calls,
				tool_call_id,
				finish_reason,
				parts,
				prompt_tokens,
				completion_tokens,
				total_tokens,
				created_at,
				updated_at,
				compacted_at,
				deleted_at
			)
			VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8,
				$9, $10, $11, $12, $13, $14, $15, 0, 0
			)
		`,
		deleteEvent: `
			UPDATE ` + eventsTable + `
			SET deleted_at = $1
			WHERE session_id = $2
				AND event_id = $3
				AND deleted_at = 0
		`,
		getEvents: `
			SELECT *
			FROM ` + eventsTable + `
			WHERE session_id = $1
				AND deleted_at = 0
				AND compacted_at = 0
			ORDER BY created_at ASC, event_id ASC
			LIMIT $2 OFFSET $3
		`,
		listEvents: `
			SELECT *
			FROM ` + eventsTable + `
			WHERE session_id = $1
				AND deleted_at = 0
				AND compacted_at = 0
			ORDER BY created_at ASC, event_id ASC
		`,
		listCompactedEvents: `
			SELECT *
			FROM ` + eventsTable + `
			WHERE session_id = $1
				AND compacted_at > 0
				AND deleted_at = 0
			ORDER BY created_at ASC, event_id ASC
		`,
		compactActiveEvents: `
			UPDATE ` + eventsTable + `
			SET compacted_at = $1
			WHERE session_id = $2
				AND deleted_at = 0
				AND compacted_at = 0
		`,
		compactEventsBefore: `
			UPDATE ` + eventsTable + `
			SET compacted_at = $1
			WHERE session_id = $2
				AND deleted_at = 0
				AND compacted_at = 0
				AND event_id < $3
		`,
	}
}

// defaultQueries is built from the default table names for backward compatibility.
var defaultQueries = buildQueries(defaultSessionsTable, defaultEventsTable)

func createMigrationsTableSQL(table string) string {
	return `
		CREATE TABLE IF NOT EXISTS ` + table + ` (
			version INTEGER PRIMARY KEY
		)
	`
}

func migrationAppliedSQL(table string) string {
	return `
		SELECT 1
		FROM ` + table + `
		WHERE version = $1
	`
}

func recordMigrationSQL(table string) string {
	return `
		INSERT INTO ` + table + ` (version)
		VALUES ($1)
	`
}

func migrationV1SQL(t migrationTables) []string {
	return []string{
		`
			CREATE TABLE IF NOT EXISTS ` + t.sessions + ` (
				session_id TEXT PRIMARY KEY,
				app_id     TEXT   NOT NULL DEFAULT '',
				user_id    TEXT   NOT NULL DEFAULT '',
				created_at BIGINT NOT NULL,
				updated_at BIGINT NOT NULL,
				deleted_at BIGINT NOT NULL
			)
		`,
		`
			CREATE TABLE IF NOT EXISTS ` + t.events + ` (
				event_id          BIGINT PRIMARY KEY,
				session_id        TEXT   NOT NULL,
				author            TEXT    NOT NULL DEFAULT '',
				role              TEXT    NOT NULL DEFAULT '',
				text              TEXT    NOT NULL DEFAULT '',
				reasoning_text    TEXT    NOT NULL DEFAULT '',
				tool_calls        TEXT    NOT NULL DEFAULT '[]',
				tool_call_id      TEXT    NOT NULL DEFAULT '',
				finish_reason     TEXT    NOT NULL DEFAULT '',
				parts             TEXT    NOT NULL DEFAULT '[]',
				prompt_tokens     BIGINT NOT NULL DEFAULT 0,
				completion_tokens BIGINT NOT NULL DEFAULT 0,
				total_tokens      BIGINT NOT NULL DEFAULT 0,
				created_at        BIGINT NOT NULL,
				updated_at        BIGINT NOT NULL,
				compacted_at      BIGINT NOT NULL DEFAULT 0,
				deleted_at        BIGINT NOT NULL
			)
		`,
		`
			CREATE INDEX IF NOT EXISTS idx_` + t.events + `_session
			ON ` + t.events + ` (
				session_id,
				deleted_at,
				compacted_at,
				created_at
			)
		`,
	}
}
