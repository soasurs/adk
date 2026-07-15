package database

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"time"

	"github.com/huandu/go-sqlbuilder"
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

// WithTablePrefix sets a prefix for all managed table names.
func WithTablePrefix(prefix string) Option {
	return func(s *databaseSessionService) {
		s.sessionsTable = prefix + defaultSessionsTable
		s.eventsTable = prefix + defaultEventsTable
		s.turnsTable = prefix + defaultTurnsTable
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

// WithTurnsTable overrides the turns table name.
func WithTurnsTable(name string) Option {
	return func(s *databaseSessionService) {
		s.turnsTable = name
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
	turnsTable          string
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
// By default it uses the table names "sessions", "events", and "turns".
// Use table Option functions to customise the names and avoid conflicts in
// shared databases.
// Use WithRunLocker to serialize Runner turns with an application-provided
// distributed lock.
// Returns an error if any configured table name is not a valid SQL identifier.
func NewDatabaseSessionService(db *sqlx.DB, opts ...Option) (session.SessionService, error) {
	svc := &databaseSessionService{
		db:              db,
		sessionsTable:   defaultSessionsTable,
		eventsTable:     defaultEventsTable,
		turnsTable:      defaultTurnsTable,
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
	if err := validateTableName(svc.turnsTable); err != nil {
		return nil, err
	}
	if err := validateTableName(svc.migrationsTable); err != nil {
		return nil, err
	}
	if svc.runLockerConfigured && svc.runLocker == nil {
		return nil, errors.New("database: run locker is nil")
	}
	svc.q = buildQueries(svc.sessionsTable, svc.eventsTable, svc.turnsTable)
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

func (ss *databaseSessionService) ListSessions(ctx context.Context, req session.ListSessionsRequest) ([]session.Session, error) {
	req, err := req.Normalize()
	if err != nil {
		return nil, err
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("session_id", "app_id", "user_id", "created_at", "deleted_at")
	sb.From(ss.sessionsTable)
	sb.Where(
		sb.Equal("app_id", req.AppID),
		sb.Equal("user_id", req.UserID),
		sb.Equal("deleted_at", 0),
	)
	if req.SortBy == session.SessionSortBySessionID {
		if req.SortOrder == session.SortDescending {
			sb.OrderByDesc("session_id")
		} else {
			sb.OrderByAsc("session_id")
		}
	} else {
		if req.SortOrder == session.SortDescending {
			sb.OrderByDesc("created_at").OrderByAsc("session_id")
		} else {
			sb.OrderByAsc("created_at").OrderByAsc("session_id")
		}
	}
	sb.Limit(int(req.Limit)).Offset(int(req.Offset))

	sql, args := sb.Build()
	rows := make([]*databaseSession, 0)
	if err := ss.db.SelectContext(ctx, &rows, sql, args...); err != nil {
		return nil, err
	}

	sessions := make([]session.Session, len(rows))
	for i, row := range rows {
		row.db = ss.db
		row.q = ss.q
		sessions[i] = row
	}
	return sessions, nil
}
