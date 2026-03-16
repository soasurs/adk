package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
		return fmt.Errorf("database: invalid table name %q: must match [A-Za-z_][A-Za-z0-9_]*", name)
	}
	return nil
}

// Option is a functional option for configuring a DatabaseSessionService.
type Option func(*databaseSessionService)

// WithTablePrefix sets a prefix for both the sessions and messages table names.
// For example, WithTablePrefix("myapp_") will use tables "myapp_sessions" and "myapp_messages".
func WithTablePrefix(prefix string) Option {
	return func(s *databaseSessionService) {
		s.sessionsTable = prefix + defaultSessionsTable
		s.messagesTable = prefix + defaultMessagesTable
	}
}

// WithSessionsTable overrides the sessions table name.
func WithSessionsTable(name string) Option {
	return func(s *databaseSessionService) {
		s.sessionsTable = name
	}
}

// WithMessagesTable overrides the messages table name.
func WithMessagesTable(name string) Option {
	return func(s *databaseSessionService) {
		s.messagesTable = name
	}
}

type databaseSessionService struct {
	db            *sqlx.DB
	sessionsTable string
	messagesTable string
	q             *queries
}

// NewDatabaseSessionService creates a new database-backed SessionService.
// By default it uses the table names "sessions" and "messages".
// Use Option functions such as WithTablePrefix, WithSessionsTable, or WithMessagesTable
// to customise the table names and avoid conflicts in shared databases.
// Returns an error if any configured table name is not a valid SQL identifier.
func NewDatabaseSessionService(db *sqlx.DB, opts ...Option) (session.SessionService, error) {
	svc := &databaseSessionService{
		db:            db,
		sessionsTable: defaultSessionsTable,
		messagesTable: defaultMessagesTable,
	}
	for _, opt := range opts {
		opt(svc)
	}
	if err := validateTableName(svc.sessionsTable); err != nil {
		return nil, err
	}
	if err := validateTableName(svc.messagesTable); err != nil {
		return nil, err
	}
	svc.q = buildQueries(svc.sessionsTable, svc.messagesTable)
	return svc, nil
}

func (ss *databaseSessionService) CreateSession(ctx context.Context, sessionID int64) (session.Session, error) {
	return newDatabaseSession(ctx, ss.db, sessionID, ss.q)
}

func (ss *databaseSessionService) DeleteSession(ctx context.Context, sessionID int64) error {
	now := time.Now()
	_, err := ss.db.ExecContext(ctx, ss.q.deleteSession, now.UnixMilli(), sessionID, 0)
	return err
}

func (ss *databaseSessionService) GetSession(ctx context.Context, sessionID int64) (session.Session, error) {
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
