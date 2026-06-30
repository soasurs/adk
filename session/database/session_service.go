package database

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/soasurs/adk/session"
)

// validTableName matches legal SQL identifiers: start with letter or underscore,
// followed by letters, digits, or underscores.
var validTableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateTableName(name string) error {
	if !validTableName.MatchString(name) {
		return &InvalidTableNameError{Name: name}
	}
	return nil
}

// Option is a functional option for configuring a DatabaseSessionService.
type Option func(*databaseSessionService)

// WithRunLocker overrides the locker used by Runner to serialize full turns.
// Database session services do not lock runs by default; multi-process
// deployments should provide a distributed implementation, such as PostgreSQL
// advisory locks or Redis.
func WithRunLocker(locker session.RunScopedLocker) Option {
	return func(s *databaseSessionService) {
		s.runLockerConfigured = true
		s.runLocker = locker
	}
}

// WithTablePrefix sets a prefix for the sessions, events, and migrations table names.
// For example, WithTablePrefix("myapp_") will use tables "myapp_sessions", "myapp_events", and "myapp_schema_migrations".
func WithTablePrefix(prefix string) Option {
	return func(s *databaseSessionService) {
		s.sessionsTable = prefix + defaultSessionsTable
		s.eventsTable = prefix + defaultEventsTable
		s.migrationsTable = prefix + defaultMigrationsTable
	}
}

// WithSessionsTable overrides the sessions table name.
func WithSessionsTable(name string) Option {
	return func(s *databaseSessionService) {
		s.sessionsTable = name
	}
}

// WithEventsTable overrides the events table name.
func WithEventsTable(name string) Option {
	return func(s *databaseSessionService) {
		s.eventsTable = name
	}
}

// WithMigrationsTable overrides the schema migrations table name.
func WithMigrationsTable(name string) Option {
	return func(s *databaseSessionService) {
		s.migrationsTable = name
	}
}

type databaseSessionService struct {
	db                  *sqlx.DB
	sessionsTable       string
	eventsTable         string
	migrationsTable     string
	q                   *queries
	runLockerConfigured bool
	runLocker           session.RunScopedLocker
}

type lockingDatabaseSessionService struct {
	*databaseSessionService
	runLocker session.RunScopedLocker
}

func (ss *lockingDatabaseSessionService) LockRun(ctx context.Context, key session.RunLockKey) (func(), error) {
	return ss.runLocker.LockRun(ctx, key)
}

// NewDatabaseSessionService creates a new SQL database-backed SessionService.
// The caller owns the *sqlx.DB and its driver configuration. SQLite and
// PostgreSQL are covered by this package's tests.
// By default it uses the table names "sessions" and "events".
// Use Option functions such as WithTablePrefix, WithSessionsTable, or WithEventsTable
// to customise the table names and avoid conflicts in shared databases.
// Use WithRunLocker to serialize Runner turns with an application-provided
// distributed lock.
// Returns an error if any configured table name is not a valid SQL identifier.
func NewDatabaseSessionService(db *sqlx.DB, opts ...Option) (session.SessionService, error) {
	svc := &databaseSessionService{
		db:              db,
		sessionsTable:   defaultSessionsTable,
		eventsTable:     defaultEventsTable,
		migrationsTable: defaultMigrationsTable,
	}
	for _, opt := range opts {
		opt(svc)
	}
	if err := validateTableName(svc.sessionsTable); err != nil {
		return nil, err
	}
	if err := validateTableName(svc.eventsTable); err != nil {
		return nil, err
	}
	if err := validateTableName(svc.migrationsTable); err != nil {
		return nil, err
	}
	if svc.runLockerConfigured && svc.runLocker == nil {
		return nil, errors.New("database: run locker is nil")
	}
	svc.q = buildQueries(svc.sessionsTable, svc.eventsTable)
	if svc.runLocker != nil {
		return &lockingDatabaseSessionService{
			databaseSessionService: svc,
			runLocker:              svc.runLocker,
		}, nil
	}
	return svc, nil
}

func (ss *databaseSessionService) CreateSession(ctx context.Context, req session.CreateSessionRequest) (session.Session, error) {
	return newDatabaseSession(ctx, ss.db, req, ss.q)
}

func (ss *databaseSessionService) DeleteSession(ctx context.Context, sessionID string) error {
	now := time.Now()
	_, err := ss.db.ExecContext(ctx, ss.q.deleteSession, now.UnixMilli(), sessionID, 0)
	return err
}

func (ss *databaseSessionService) GetSession(ctx context.Context, sessionID string) (session.Session, error) {
	s := new(databaseSession)
	err := ss.db.GetContext(ctx, s, ss.q.getSession, sessionID, 0)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	s.db = ss.db
	s.q = ss.q
	return s, nil
}
