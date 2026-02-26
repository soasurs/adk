package postgres

import (
	"context"

	"github.com/google/uuid"
	"soasurs.dev/soasurs/adk/internal/storage"
)

type Store struct {
	db *Postgres
	*SessionStore
	*MessageStore
	*RunStore
}

func NewStore(db *Postgres) *Store {
	return &Store{
		db:           db,
		SessionStore: NewSessionStore(db),
		MessageStore: NewMessageStore(db),
		RunStore:     NewRunStore(db),
	}
}

var _ storage.Store = (*Store)(nil)

func (s *Store) SaveToolCall(ctx context.Context, tc *storage.ToolCall) error {
	return s.RunStore.SaveToolCall(ctx, tc)
}

func (s *Store) GetSessionByAgent(ctx context.Context, agentID string, limit int) ([]storage.Session, error) {
	query := `
		SELECT id, agent_id, metadata, created_at, updated_at
		FROM sessions
		WHERE agent_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := s.db.DB().Query(ctx, query, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []storage.Session
	for rows.Next() {
		session := storage.Session{}
		var metadataJSON []byte
		err := rows.Scan(
			&session.ID,
			&session.AgentID,
			&metadataJSON,
			&session.CreatedAt,
			&session.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		if len(metadataJSON) > 0 {
			session.Metadata = unmarshalJSON(metadataJSON)
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

func (s *Store) CreateSessionWithID(ctx context.Context, session *storage.Session) error {
	if session.ID == uuid.Nil {
		session.ID = uuid.New()
	}
	return s.CreateSession(ctx, session)
}
