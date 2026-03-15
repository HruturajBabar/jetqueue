package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hruturajbabar/jetqueue/internal/store"
)

func newAPITestStore(t *testing.T) *store.Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	return st
}

type publishedCall struct {
	subject string
	data    []byte
}

type fakeOutboxPublisher struct {
	calls     []publishedCall
	failAt    int
	callCount int
}

func (f *fakeOutboxPublisher) Publish(ctx context.Context, subject string, data []byte) error {
	f.callCount++
	if f.failAt > 0 && f.callCount == f.failAt {
		return errors.New("forced publish failure")
	}

	f.calls = append(f.calls, publishedCall{
		subject: subject,
		data:    data,
	})
	return nil
}

func TestPublishOutboxOnce_Success(t *testing.T) {
	ctx := context.Background()
	st := newAPITestStore(t)
	pub := &fakeOutboxPublisher{}

	insertOutboxRow(t, st, "jobs.default", `{"job_id":"1"}`)
	insertOutboxRow(t, st, "jobs.email", `{"job_id":"2"}`)

	if err := publishOutboxOnce(ctx, st, pub, 50); err != nil {
		t.Fatalf("publishOutboxOnce error: %v", err)
	}

	if len(pub.calls) != 2 {
		t.Fatalf("expected 2 publish calls, got %d", len(pub.calls))
	}

	rows, err := st.FetchUnsentOutbox(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no unsent outbox rows, got %d", len(rows))
	}
}

func mustBeginTx(t *testing.T, st *store.Store) *sql.Tx {
	t.Helper()

	tx, err := st.DB.Begin()
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Commit()
	})
	return tx
}

func insertOutboxRow(t *testing.T, st *store.Store, topic, payload string) {
	t.Helper()

	_, err := st.DB.Exec(`
INSERT INTO outbox(topic, payload_json, created_at_unix, sent_at_unix)
VALUES (?, ?, 0, 0)
`, topic, payload)
	if err != nil {
		t.Fatalf("failed to insert outbox row: %v", err)
	}
}

func TestPublishOutboxOnce_PartialFailureLeavesRemainingUnsent(t *testing.T) {
	ctx := context.Background()
	st := newAPITestStore(t)
	pub := &fakeOutboxPublisher{failAt: 2}

	insertOutboxRow(t, st, "jobs.default", `{"job_id":"1"}`)
	insertOutboxRow(t, st, "jobs.email", `{"job_id":"2"}`)
	insertOutboxRow(t, st, "jobs.image", `{"job_id":"3"}`)

	err := publishOutboxOnce(ctx, st, pub, 50)
	if err == nil {
		t.Fatalf("expected publishOutboxOnce to fail")
	}

	if len(pub.calls) != 1 {
		t.Fatalf("expected exactly 1 successful publish before failure, got %d", len(pub.calls))
	}

	rows, err := st.FetchUnsentOutbox(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 unsent rows remaining, got %d", len(rows))
	}

	if rows[0].Topic != "jobs.email" {
		t.Fatalf("expected first remaining row to be jobs.email, got %s", rows[0].Topic)
	}
	if rows[1].Topic != "jobs.image" {
		t.Fatalf("expected second remaining row to be jobs.image, got %s", rows[1].Topic)
	}
}
