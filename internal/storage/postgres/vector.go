package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"soasurs.dev/soasurs/adk/pkg/memory"
)

type VectorStore struct {
	db *Postgres
}

func NewVectorStore(db *Postgres) *VectorStore {
	return &VectorStore{db: db}
}

func (v *VectorStore) Insert(ctx context.Context, sessionID string, messageID string, text string, vector []float32) error {
	query := `
		INSERT INTO embeddings (id, session_id, message_id, content, vector, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET content = $4, vector = $5, metadata = $6
	`

	id := uuid.New()
	if messageID != "" {
		if parsed, err := uuid.Parse(messageID); err == nil {
			id = parsed
		}
	}

	metadata := map[string]any{
		"type": "embedding",
	}

	vectorStr := formatVector(vector)

	_, err := v.db.DB().Exec(ctx, query, id, sessionID, messageID, text, vectorStr, metadata, time.Now())
	return err
}

func (v *VectorStore) Search(ctx context.Context, sessionID string, query string, limit int) ([]memory.VectorResult, error) {
	querySQL := `
		SELECT id, message_id, content, 1 - (vector <=> $1::vector) as similarity
		FROM embeddings
		WHERE session_id = $2
		ORDER BY vector <=> $1::vector
		LIMIT $3
	`

	vectorStr := formatVectorString(limit)

	rows, err := v.db.DB().Query(ctx, querySQL, vectorStr, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []memory.VectorResult
	for rows.Next() {
		var r memory.VectorResult
		var id uuid.UUID
		var similarity float32

		err := rows.Scan(&id, &r.MessageID, &r.Text, &similarity)
		if err != nil {
			return nil, err
		}

		r.Score = 1 - similarity
		results = append(results, r)
	}

	return results, nil
}

func (v *VectorStore) Delete(ctx context.Context, sessionID string, messageID string) error {
	query := `DELETE FROM embeddings WHERE session_id = $1 AND message_id = $2`
	_, err := v.db.DB().Exec(ctx, query, sessionID, messageID)
	return err
}

func (v *VectorStore) DeleteBySession(ctx context.Context, sessionID string) error {
	query := `DELETE FROM embeddings WHERE session_id = $1`
	_, err := v.db.DB().Exec(ctx, query, sessionID)
	return err
}

func formatVector(vector []float32) string {
	if len(vector) == 0 {
		return "[0]"
	}

	b, _ := json.Marshal(vector)
	return string(b)
}

func formatVectorString(dim int) string {
	result := make([]float32, dim)
	return formatVector(result)
}
