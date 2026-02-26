package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"soasurs.dev/soasurs/adk/internal/storage"
)

type RunStore struct {
	db *Postgres
}

func NewRunStore(db *Postgres) *RunStore {
	return &RunStore{db: db}
}

func (r *RunStore) CreateRun(ctx context.Context, run *storage.Run) error {
	query := `
		INSERT INTO runs (id, session_id, status, input, output, error, started_at, completed_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := r.db.DB().Exec(ctx, query,
		run.ID,
		run.SessionID,
		run.Status,
		run.Input,
		run.Output,
		run.Error,
		run.StartedAt,
		run.CompletedAt,
		run.CreatedAt,
	)
	return err
}

func (r *RunStore) GetRun(ctx context.Context, id uuid.UUID) (*storage.Run, error) {
	query := `
		SELECT id, session_id, status, input, output, error, started_at, completed_at, created_at
		FROM runs
		WHERE id = $1
	`
	run := &storage.Run{}
	err := r.db.DB().QueryRow(ctx, query, id).Scan(
		&run.ID,
		&run.SessionID,
		&run.Status,
		&run.Input,
		&run.Output,
		&run.Error,
		&run.StartedAt,
		&run.CompletedAt,
		&run.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	toolCalls, err := r.getToolCalls(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	run.ToolCalls = toolCalls

	return run, nil
}

func (r *RunStore) UpdateRun(ctx context.Context, run *storage.Run) error {
	query := `
		UPDATE runs
		SET status = $2, output = $3, error = $4, started_at = $5, completed_at = $6
		WHERE id = $1
	`
	_, err := r.db.DB().Exec(ctx, query,
		run.ID,
		run.Status,
		run.Output,
		run.Error,
		run.StartedAt,
		run.CompletedAt,
	)
	return err
}

func (r *RunStore) GetRunsBySession(ctx context.Context, sessionID uuid.UUID, limit int) ([]storage.Run, error) {
	query := `
		SELECT id, session_id, status, input, output, error, started_at, completed_at, created_at
		FROM runs
		WHERE session_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := r.db.DB().Query(ctx, query, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []storage.Run
	for rows.Next() {
		run := storage.Run{}
		err := rows.Scan(
			&run.ID,
			&run.SessionID,
			&run.Status,
			&run.Input,
			&run.Output,
			&run.Error,
			&run.StartedAt,
			&run.CompletedAt,
			&run.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		toolCalls, err := r.getToolCalls(ctx, run.ID)
		if err != nil {
			return nil, err
		}
		run.ToolCalls = toolCalls

		runs = append(runs, run)
	}

	return runs, nil
}

func (r *RunStore) getToolCalls(ctx context.Context, runID uuid.UUID) ([]storage.ToolCall, error) {
	query := `
		SELECT id, run_id, name, args, result, error
		FROM tool_calls
		WHERE run_id = $1
	`
	rows, err := r.db.DB().Query(ctx, query, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var toolCalls []storage.ToolCall
	for rows.Next() {
		tc := storage.ToolCall{}
		var argsJSON, resultJSON []byte
		err := rows.Scan(
			&tc.ID,
			&tc.RunID,
			&tc.Name,
			&argsJSON,
			&resultJSON,
			&tc.Error,
		)
		if err != nil {
			return nil, err
		}

		if len(argsJSON) > 0 {
			json.Unmarshal(argsJSON, &tc.Args)
		}
		if len(resultJSON) > 0 {
			json.Unmarshal(resultJSON, &tc.Result)
		}

		toolCalls = append(toolCalls, tc)
	}

	return toolCalls, nil
}

func (r *RunStore) SaveToolCall(ctx context.Context, tc *storage.ToolCall) error {
	query := `
		INSERT INTO tool_calls (id, run_id, name, args, result, error, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := r.db.DB().Exec(ctx, query,
		tc.ID,
		tc.RunID,
		tc.Name,
		marshalJSON(tc.Args),
		marshalJSON(tc.Result),
		tc.Error,
		time.Now(),
	)
	return err
}
