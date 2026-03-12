package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/hruturajbabar/jetqueue/internal/types"
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
var ErrInvalidStateTransition = errors.New("invalid job state transition")
var ErrJobNotFound = errors.New("no job found")

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

func (s *Store) updateJobState(
	ctx context.Context,
	jobId string,
	newStatus types.JobStatus,
	fromStatus types.JobStatus,
	attempt int,
	lastError string,
) error {
	res, err := s.DB.ExecContext(ctx, `
UPDATE jobs
SET status = ?, attempt = ?, last_error = ?, updated_at_unix = ?
WHERE job_id = ? and status = ?
`, newStatus, attempt, lastError, time.Now().Unix(), jobId, fromStatus)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return ErrInvalidStateTransition
	}

	return nil
}

func (s *Store) MarkJobRunning(ctx context.Context, jobId string, attempt int) error {
	res, err := s.DB.ExecContext(ctx, `
UPDATE jobs
SET status = ?, attempt = ?, last_error = '', updated_at_unix = ?
WHERE job_id = ? AND status IN (?, ?)
`, types.StatusRunning, attempt, time.Now().Unix(), jobId, types.StatusQueued, types.StatusRetryScheduled)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return ErrInvalidStateTransition
	}

	return nil
}

func (s *Store) MarkJobSucceeded(ctx context.Context, jobId string, attempt int) error {
	return s.updateJobState(ctx, jobId, types.StatusSucceeded, types.StatusRunning, attempt, "")
}

func (s *Store) MarkJobFailed(ctx context.Context, jobId string, attempt int, lastError string) error {
	return s.updateJobState(ctx, jobId, types.StatusFailed, types.StatusRunning, attempt, lastError)
}

func (s *Store) MarkJobRetryScheduled(ctx context.Context, jobId string, attempt int, lastError string) error {
	return s.updateJobState(ctx, jobId, types.StatusRetryScheduled, types.StatusRunning, attempt, lastError)
}

func (s *Store) GetJobByID(ctx context.Context, jobID string) (*types.Job, error) {
	job := &types.Job{}

	err := s.DB.QueryRowContext(ctx, `
SELECT 
	job_id, 
	queue,
	type,
	payload_json,
	status,
	attempt,
	max_attempts,
	COALESCE(last_error, ''),
	created_at_unix,
	updated_at_unix
FROM jobs
WHERE job_id = ?
`, jobID).Scan(
		&job.JobID,
		&job.Queue,
		&job.Type,
		&job.PayloadJSON,
		&job.Status,
		&job.Attempt,
		&job.MaxAttempts,
		&job.LastError,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrJobNotFound
		}
		return nil, err
	}
	return job, nil
}

func (s *Store) ListJobs(ctx context.Context, queue string, status string, limit int) ([]*types.Job, error) {
	query := `
SELECT 
	job_id, 
	queue,
	type,
	payload_json,
	status,
	attempt,
	max_attempts,
	COALESCE(last_error, ''),
	created_at_unix,
	updated_at_unix
FROM jobs
`
	var conditions []string
	var args []any

	if queue != "" {
		conditions = append(conditions, "queue = ?")
		args = append(args, queue)
	}
	if status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, status)
	}
	if len(conditions) > 0 {
		query += "WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at_unix ORDER DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := []*types.Job{}
	for rows.Next() {
		var job types.Job
		err := rows.Scan(
			&job.JobID,
			&job.Queue,
			&job.Type,
			&job.PayloadJSON,
			&job.Status,
			&job.Attempt,
			&job.MaxAttempts,
			&job.LastError,
			&job.CreatedAt,
			&job.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, &job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return jobs, nil
}
