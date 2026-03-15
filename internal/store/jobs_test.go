package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hruturajbabar/jetqueue/internal/types"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	return st
}

func insertJobRow(t *testing.T, st *Store, jobID string, status types.JobStatus, attempt int, maxAttempts int) {
	t.Helper()

	_, err := st.DB.Exec(`
INSERT INTO jobs(job_id, queue, type, payload_json, status, attempt, max_attempts, last_error, created_at_unix, updated_at_unix)
VALUES (?, 'default', 'echo', '{}', ?, ?, ?, '', 0, 0)
`, jobID, status, attempt, maxAttempts)
	if err != nil {
		t.Fatalf("failed to insert job row: %v", err)
	}
}

func TestMarkJobRunning_FromQueued_Succeeds(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	insertJobRow(t, st, "job-1", types.StatusQueued, 0, 3)

	if err := st.MarkJobRunning(ctx, "job-1", 0); err != nil {
		t.Fatalf("MarkJobRunning returned error: %v", err)
	}

	job, err := st.GetJobByID(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}

	if job.Status != types.StatusRunning {
		t.Fatalf("expected running, got %s", job.Status)
	}
}

func TestMarkJobRunning_FromSucceeded_Fails(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	insertJobRow(t, st, "job-2", types.StatusSucceeded, 0, 3)

	err := st.MarkJobRunning(ctx, "job-2", 1)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if err != ErrInvalidStateTransition {
		t.Fatalf("expected ErrInvalidStateTransition, got %v", err)
	}
}

func TestMarkMessageProcessed_Idempotent(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	processed, err := st.HasProcessedMessage(ctx, "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if processed {
		t.Fatalf("expected message to be unprocessed initially")
	}

	if err := st.MarkMessageProcessed(ctx, "msg-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkMessageProcessed(ctx, "msg-1"); err != nil {
		t.Fatal(err)
	}

	processed, err = st.HasProcessedMessage(ctx, "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if !processed {
		t.Fatalf("expected message to be processed")
	}
}
