package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"soasurs.dev/soasurs/adk/internal/storage"
)

type SessionStore struct {
	db *Postgres
}

func NewSessionStore(db *Postgres) *SessionStore {
	return &SessionStore{db: db}
}

func (s *SessionStore) CreateSession(ctx context.Context, session *storage.Session) error {
	query := `
		INSERT INTO sessions (id, agent_id, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := s.db.DB().Exec(ctx, query,
		session.ID,
		session.AgentID,
		session.Metadata,
		session.CreatedAt,
		session.UpdatedAt,
	)
	return err
}

func (s *SessionStore) GetSession(ctx context.Context, id uuid.UUID) (*storage.Session, error) {
	query := `
		SELECT id, agent_id, metadata, created_at, updated_at
		FROM sessions
		WHERE id = $1
	`
	session := &storage.Session{}
	var metadataJSON []byte
	err := s.db.DB().QueryRow(ctx, query, id).Scan(
		&session.ID,
		&session.AgentID,
		&metadataJSON,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	session.Metadata = make(map[string]any)
	if len(metadataJSON) > 0 {
		session.Metadata = unmarshalJSON(metadataJSON)
	}

	return session, nil
}

func (s *SessionStore) UpdateSession(ctx context.Context, session *storage.Session) error {
	query := `
		UPDATE sessions
		SET agent_id = $2, metadata = $3, updated_at = $4
		WHERE id = $1
	`
	_, err := s.db.DB().Exec(ctx, query,
		session.ID,
		session.AgentID,
		session.Metadata,
		time.Now(),
	)
	return err
}

func (s *SessionStore) DeleteSession(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM sessions WHERE id = $1`
	_, err := s.db.DB().Exec(ctx, query, id)
	return err
}
