package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mattn/go-sqlite3"
)

type CreateJobInput struct {
	JobID          string
	Queue          string
	Type           string
	PayloadJSON    string
	MaxAttempts    int
	IdempotencyKey string
}

type CreateJobResult struct {
	JobID   string
	Deduped bool
}

var ErrBadInput = errors.New("bad input")

func (s *Store) CreateJobWithIdempotencyAndOutbox(ctx context.Context, in CreateJobInput, outboxTopic string, outboxPayload string) (CreateJobResult, error) {
	if in.JobID == "" || in.Queue == "" || in.Type == "" {
		return CreateJobResult{}, ErrBadInput
	}
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = 5
	}

	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return CreateJobResult{}, err
	}
	defer tx.Rollback()

	now := time.Now().Unix()

	// If idempotency key is present: insert mapping first (unique).
	// If it already exists, return the existing job_id.
	if in.IdempotencyKey != "" {
		_, err := tx.ExecContext(ctx, `
INSERT INTO idempotency_keys(key, job_id, created_at_unix)
VALUES(?, ?, ?)
`, in.IdempotencyKey, in.JobID, now)

		if err != nil {
			var se sqlite3.Error
			if errors.As(err, &se) && se.ExtendedCode == sqlite3.ErrConstraintPrimaryKey {
				var existing string
				row := tx.QueryRowContext(ctx, `SELECT job_id FROM idempotency_keys WHERE key = ?`, in.IdempotencyKey)
				if err := row.Scan(&existing); err != nil {
					return CreateJobResult{}, err
				}
				// Do NOT enqueue outbox again.
				if err := tx.Commit(); err != nil {
					return CreateJobResult{}, err
				}
				return CreateJobResult{JobID: existing, Deduped: true}, nil
			}
			return CreateJobResult{}, err
		}
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO jobs(job_id, queue, type, payload_json, status, attempt, max_attempts, last_error, created_at_unix, updated_at_unix)
VALUES(?, ?, ?, ?, 'queued', 0, ?, '', ?, ?)
`, in.JobID, in.Queue, in.Type, in.PayloadJSON, in.MaxAttempts, now, now)
	if err != nil {
		return CreateJobResult{}, err
	}

	if err := s.EnqueueOutboxTx(ctx, tx, outboxTopic, outboxPayload); err != nil {
		return CreateJobResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return CreateJobResult{}, err
	}
	return CreateJobResult{JobID: in.JobID, Deduped: false}, nil
}
