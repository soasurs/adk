package database

const (
	defaultSessionsTable   = "sessions"
	defaultEventsTable     = "events"
	defaultTurnsTable      = "turns"
	defaultMigrationsTable = "schema_migrations"
)

const sessionColumns = `
	session_id,
	app_id,
	user_id,
	created_at,
	deleted_at
`

const eventColumns = `
	event_id,
	session_id,
	turn_id,
	author,
	role,
	text,
	reasoning_text,
	tool_calls,
	tool_result,
	tool_call_id,
	finish_reason,
	parts,
	prompt_tokens,
	completion_tokens,
	total_tokens,
	usage_details,
	created_at,
	updated_at,
	archived_at,
	deleted_at
`

const turnColumns = `
	turn_id,
	session_id,
	status,
	reason,
	failure_code,
	failure_message,
	failure_stage,
	started_at,
	finished_at
`

// queries holds all pre-built SQL expressions for a set of table names.
type queries struct {
	createSession       string
	getSession          string
	deleteSession       string
	createEvent         string
	deleteEvent         string
	getEvents           string
	listEvents          string
	listArchivedEvents  string
	getArchiveBoundary  string
	archiveActiveEvents string
	archiveEventsBefore string
	createTurn          string
	getTurn             string
	listTurns           string
	finalizeTurn        string
	interruptRunning    string
}

// buildQueries constructs SQL expressions using the provided table names.
// Table names are validated before this function is called.
func buildQueries(sessionsTable, eventsTable, turnsTable string) *queries {
	return &queries{
		createSession: `
			INSERT INTO ` + sessionsTable + ` (
				session_id,
				app_id,
				user_id,
				created_at,
				deleted_at
			)
			VALUES ($1, $2, $3, $4, $5)
		`,
		getSession: `
			SELECT ` + sessionColumns + `
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
				turn_id,
				author,
				role,
				text,
				reasoning_text,
				tool_calls,
				tool_result,
				tool_call_id,
				finish_reason,
				parts,
				prompt_tokens,
				completion_tokens,
				total_tokens,
				usage_details,
				created_at,
				updated_at,
				archived_at,
				deleted_at
			)
			VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8,
				$9, $10, $11, $12, $13, $14, $15, $16,
				$17, $18, 0, 0
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
			SELECT ` + eventColumns + `
			FROM ` + eventsTable + `
			WHERE session_id = $1
				AND deleted_at = 0
				AND archived_at = 0
			ORDER BY created_at ASC, event_id ASC
			LIMIT $2 OFFSET $3
		`,
		listEvents: `
			SELECT ` + eventColumns + `
			FROM ` + eventsTable + `
			WHERE session_id = $1
				AND deleted_at = 0
				AND archived_at = 0
			ORDER BY created_at ASC, event_id ASC
		`,
		listArchivedEvents: `
			SELECT ` + eventColumns + `
			FROM ` + eventsTable + `
			WHERE session_id = $1
				AND archived_at > 0
				AND deleted_at = 0
			ORDER BY created_at ASC, event_id ASC
		`,
		getArchiveBoundary: `
			SELECT created_at
			FROM ` + eventsTable + `
			WHERE session_id = $1
				AND event_id = $2
				AND deleted_at = 0
				AND archived_at = 0
		`,
		archiveActiveEvents: `
			UPDATE ` + eventsTable + `
			SET archived_at = $1
			WHERE session_id = $2
				AND deleted_at = 0
				AND archived_at = 0
		`,
		archiveEventsBefore: `
			UPDATE ` + eventsTable + `
			SET archived_at = $1
			WHERE session_id = $2
				AND deleted_at = 0
				AND archived_at = 0
				AND (created_at < $3 OR (created_at = $3 AND event_id < $4))
		`,
		createTurn: `
			INSERT INTO ` + turnsTable + ` (
				session_id,
				turn_id,
				status,
				reason,
				started_at,
				finished_at
			)
			VALUES ($1, $2, $3, $4, $5, $6)
		`,
		getTurn: `
			SELECT ` + turnColumns + `
			FROM ` + turnsTable + `
			WHERE session_id = $1
				AND turn_id = $2
			LIMIT 1
		`,
		listTurns: `
			SELECT ` + turnColumns + `
			FROM ` + turnsTable + `
			WHERE session_id = $1
			ORDER BY started_at ASC, turn_id ASC
		`,
		finalizeTurn: `
			UPDATE ` + turnsTable + `
			SET status = $1,
				reason = $2,
				failure_code = $3,
				failure_message = $4,
				failure_stage = $5,
				finished_at = $6
			WHERE session_id = $7
				AND turn_id = $8
				AND status = 'running'
		`,
		interruptRunning: `
			UPDATE ` + turnsTable + `
			SET status = 'interrupted',
				reason = $1,
				failure_code = '',
				failure_message = '',
				failure_stage = '',
				finished_at = $2
			WHERE session_id = $3
				AND status = 'running'
		`,
	}
}

// defaultQueries is built from the default table names for backward compatibility.
var defaultQueries = buildQueries(defaultSessionsTable, defaultEventsTable, defaultTurnsTable)

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

func migrationV2SQL(t migrationTables) []string {
	return []string{
		`
			ALTER TABLE ` + t.events + `
			ADD COLUMN tool_result TEXT NOT NULL DEFAULT ''
		`,
	}
}

func migrationV3SQL(t migrationTables) []string {
	return []string{
		`
			ALTER TABLE ` + t.events + `
			ADD COLUMN turn_id TEXT NOT NULL DEFAULT ''
		`,
	}
}

func migrationV4SQL(t migrationTables) []string {
	return []string{
		`
			ALTER TABLE ` + t.events + `
			ADD COLUMN usage_details TEXT NOT NULL DEFAULT ''
		`,
	}
}

func migrationV5SQL(t migrationTables) []string {
	return []string{
		`
			ALTER TABLE ` + t.sessions + `
			DROP COLUMN updated_at
		`,
		`
			CREATE INDEX IF NOT EXISTS idx_` + t.sessions + `_owner_created
			ON ` + t.sessions + ` (
				app_id,
				user_id,
				deleted_at,
				created_at,
				session_id
			)
		`,
		`
			CREATE INDEX IF NOT EXISTS idx_` + t.sessions + `_owner_session
			ON ` + t.sessions + ` (
				app_id,
				user_id,
				deleted_at,
				session_id
			)
		`,
	}
}

func migrationV6SQL(t migrationTables) []string {
	return []string{
		`
			ALTER TABLE ` + t.events + `
			RENAME COLUMN compacted_at TO archived_at
		`,
	}
}

func migrationV7SQL(t migrationTables) []string {
	return []string{
		`
			CREATE TABLE IF NOT EXISTS ` + t.turns + ` (
				session_id  TEXT   NOT NULL,
				turn_id     TEXT   NOT NULL,
				status      TEXT   NOT NULL,
				reason      TEXT   NOT NULL DEFAULT '',
				started_at  BIGINT NOT NULL,
				finished_at BIGINT NOT NULL DEFAULT 0,
				PRIMARY KEY (session_id, turn_id)
			)
		`,
		`
			CREATE INDEX IF NOT EXISTS idx_` + t.turns + `_session_timeline
			ON ` + t.turns + ` (session_id, started_at, turn_id)
		`,
		`
			INSERT INTO ` + t.turns + ` (
				session_id,
				turn_id,
				status,
				reason,
				started_at,
				finished_at
			)
			SELECT
				session_id,
				turn_id,
				'completed',
				'',
				MIN(created_at),
				MAX(updated_at)
			FROM ` + t.events + `
			WHERE turn_id <> ''
			GROUP BY session_id, turn_id
		`,
	}
}

func migrationV8SQL(t migrationTables) []string {
	return []string{
		`
			ALTER TABLE ` + t.turns + `
			ADD COLUMN failure_code TEXT NOT NULL DEFAULT ''
		`,
		`
			ALTER TABLE ` + t.turns + `
			ADD COLUMN failure_message TEXT NOT NULL DEFAULT ''
		`,
		`
			ALTER TABLE ` + t.turns + `
			ADD COLUMN failure_stage TEXT NOT NULL DEFAULT ''
		`,
	}
}
