package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type SummaryStore struct {
	db *Postgres
}

func NewSummaryStore(db *Postgres) *SummaryStore {
	return &SummaryStore{db: db}
}

func (s *SummaryStore) Create(ctx context.Context, sessionID string, content string, tokenCount int) error {
	query := `
		INSERT INTO summaries (id, session_id, content, token_count, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := s.db.DB().Exec(ctx, query, uuid.New(), sessionID, content, tokenCount, time.Now())
	return err
}

func (s *SummaryStore) GetLatest(ctx context.Context, sessionID string) (string, error) {
	query := `
		SELECT content
		FROM summaries
		WHERE session_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`

	var content string
	err := s.db.DB().QueryRow(ctx, query, sessionID).Scan(&content)
	if err != nil {
		return "", err
	}

	return content, nil
}

func (s *SummaryStore) DeleteBySession(ctx context.Context, sessionID string) error {
	query := `DELETE FROM summaries WHERE session_id = $1`
	_, err := s.db.DB().Exec(ctx, query, sessionID)
	return err
}
