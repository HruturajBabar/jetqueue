package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/hruturajbabar/jetqueue/internal/metrics"
	"github.com/hruturajbabar/jetqueue/internal/store"
	"github.com/hruturajbabar/jetqueue/internal/types"
	"github.com/prometheus/client_golang/prometheus"
)

type fakeMessage struct {
	subject string
	data    []byte
	attempt int

	acked    bool
	termed   bool
	nakDelay time.Duration
}

func (m *fakeMessage) SubjectName() string {
	return m.subject
}

func (m *fakeMessage) DataBytes() []byte {
	return m.data
}

func (m *fakeMessage) DeliveryAttempt() (int, error) {
	return m.attempt, nil
}

func (m *fakeMessage) Ack() error {
	m.acked = true
	return nil
}

func (m *fakeMessage) NakWithDelay(d time.Duration) error {
	m.nakDelay = d
	return nil
}

func (m *fakeMessage) Term() error {
	m.termed = true
	return nil
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.db")

	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}

	return st
}

type fakeDLQPublisher struct {
	msgs []types.DLQMsg
}

func (f *fakeDLQPublisher) PublishDLQ(ctx context.Context, msg types.DLQMsg) error {
	f.msgs = append(f.msgs, msg)
	return nil
}

func newTestMetrics() *metrics.Metrics {
	reg := prometheus.NewRegistry()
	return metrics.New(reg)
}

func TestHandleMessage_EchoSuccess(t *testing.T) {
	ctx := context.Background()

	st := newTestStore(t)
	met := newTestMetrics()
	dlq := &fakeDLQPublisher{}

	jobID := "job-1"

	payload := `{"message":"hello"}`

	jobMsg := types.JobMsg{
		JobID:       jobID,
		Queue:       "default",
		Type:        "echo",
		PayloadJSON: payload,
		MaxAttempts: 3,
	}

	data, _ := json.Marshal(jobMsg)

	msg := &fakeMessage{
		subject: "jobs.default",
		data:    data,
		attempt: 0,
	}

	// insert queued job
	_, err := st.DB.Exec(`
INSERT INTO jobs(job_id, queue, type, payload_json, status, attempt, max_attempts, created_at_unix, updated_at_unix)
VALUES (?, ?, ?, ?, 'queued', 0, 3, 0, 0)
`, jobID, "default", "echo", payload)
	if err != nil {
		t.Fatal(err)
	}

	err = handleMessage(ctx, st, dlq, msg, met)
	if err != nil {
		t.Fatalf("handleMessage error: %v", err)
	}

	if !msg.acked {
		t.Fatalf("expected message to be acked")
	}

	job, err := st.GetJobByID(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}

	if job.Status != types.StatusSucceeded {
		t.Fatalf("expected succeeded, got %s", job.Status)
	}
}

func TestHandleMessage_AlreadyProcessed_AcksAndSkips(t *testing.T) {
	ctx := context.Background()

	st := newTestStore(t)
	met := newTestMetrics()
	dlq := &fakeDLQPublisher{}

	jobID := "job-dup"
	payload := `{"message":"hello again"}`

	jobMsg := types.JobMsg{
		JobID:       jobID,
		Queue:       "default",
		Type:        "echo",
		PayloadJSON: payload,
		MaxAttempts: 3,
	}

	data, err := json.Marshal(jobMsg)
	if err != nil {
		t.Fatal(err)
	}

	msg := &fakeMessage{
		subject: "jobs.default",
		data:    data,
		attempt: 1,
	}

	_, err = st.DB.Exec(`
INSERT INTO jobs(job_id, queue, type, payload_json, status, attempt, max_attempts, created_at_unix, updated_at_unix)
VALUES (?, ?, ?, ?, 'succeeded', 0, 3, 0, 0)
`, jobID, "default", "echo", payload)
	if err != nil {
		t.Fatal(err)
	}

	if err := st.MarkMessageProcessed(ctx, jobID); err != nil {
		t.Fatal(err)
	}

	err = handleMessage(ctx, st, dlq, msg, met)
	if err != nil {
		t.Fatalf("handleMessage error: %v", err)
	}

	if !msg.acked {
		t.Fatalf("expected duplicate message to be acked")
	}
	if msg.termed {
		t.Fatalf("did not expect duplicate message to be termed")
	}
	if msg.nakDelay != 0 {
		t.Fatalf("did not expect duplicate message to be retried")
	}
	if len(dlq.msgs) != 0 {
		t.Fatalf("did not expect DLQ publish for duplicate message")
	}

	job, err := st.GetJobByID(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != types.StatusSucceeded {
		t.Fatalf("expected job to remain succeeded, got %s", job.Status)
	}
}

func TestHandleMessage_UnknownType_Retries(t *testing.T) {
	ctx := context.Background()

	st := newTestStore(t)
	met := newTestMetrics()
	dlq := &fakeDLQPublisher{}

	jobID := "job-retry"
	payload := `{}`

	jobMsg := types.JobMsg{
		JobID:       jobID,
		Queue:       "default",
		Type:        "unknown_type",
		PayloadJSON: payload,
		MaxAttempts: 3,
	}

	data, err := json.Marshal(jobMsg)
	if err != nil {
		t.Fatal(err)
	}

	msg := &fakeMessage{
		subject: "jobs.default",
		data:    data,
		attempt: 0,
	}

	_, err = st.DB.Exec(`
INSERT INTO jobs(job_id, queue, type, payload_json, status, attempt, max_attempts, created_at_unix, updated_at_unix)
VALUES (?, ?, ?, ?, 'queued', 0, 3, 0, 0)
`, jobID, "default", "unknown_type", payload)
	if err != nil {
		t.Fatal(err)
	}

	err = handleMessage(ctx, st, dlq, msg, met)
	if err != nil {
		t.Fatalf("handleMessage error: %v", err)
	}

	if msg.acked {
		t.Fatalf("did not expect retrying message to be acked")
	}
	if msg.termed {
		t.Fatalf("did not expect retrying message to be termed")
	}
	if msg.nakDelay <= 0 {
		t.Fatalf("expected retrying message to have a positive retry delay")
	}
	if len(dlq.msgs) != 0 {
		t.Fatalf("did not expect DLQ publish while retries remain")
	}

	job, err := st.GetJobByID(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}

	if job.Status != types.StatusRetryScheduled {
		t.Fatalf("expected retry_scheduled, got %s", job.Status)
	}
	if job.Attempt != 1 {
		t.Fatalf("expected attempt=1, got %d", job.Attempt)
	}
	if job.LastError == "" {
		t.Fatalf("expected last_error to be populated")
	}
}

func TestHandleMessage_UnknownType_ExhaustedToDLQ(t *testing.T) {
	ctx := context.Background()

	st := newTestStore(t)
	met := newTestMetrics()
	dlq := &fakeDLQPublisher{}

	jobID := "job-dlq"
	payload := `{}`

	jobMsg := types.JobMsg{
		JobID:       jobID,
		Queue:       "default",
		Type:        "unknown_type",
		PayloadJSON: payload,
		MaxAttempts: 2,
	}

	data, err := json.Marshal(jobMsg)
	if err != nil {
		t.Fatal(err)
	}

	msg := &fakeMessage{
		subject: "jobs.default",
		data:    data,
		attempt: 1,
	}

	_, err = st.DB.Exec(`
INSERT INTO jobs(job_id, queue, type, payload_json, status, attempt, max_attempts, created_at_unix, updated_at_unix)
VALUES (?, ?, ?, ?, 'queued', 1, 2, 0, 0)
`, jobID, "default", "unknown_type", payload)
	if err != nil {
		t.Fatal(err)
	}

	err = handleMessage(ctx, st, dlq, msg, met)
	if err != nil {
		t.Fatalf("handleMessage error: %v", err)
	}

	if msg.acked {
		t.Fatalf("did not expect exhausted message to be acked")
	}
	if !msg.termed {
		t.Fatalf("expected exhausted message to be termed")
	}
	if msg.nakDelay != 0 {
		t.Fatalf("did not expect exhausted message to be retried")
	}
	if len(dlq.msgs) != 1 {
		t.Fatalf("expected exactly one DLQ publish, got %d", len(dlq.msgs))
	}

	job, err := st.GetJobByID(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}

	if job.Status != types.StatusFailed {
		t.Fatalf("expected failed, got %s", job.Status)
	}
	if job.Attempt != 2 {
		t.Fatalf("expected attempt=2, got %d", job.Attempt)
	}
	if job.LastError == "" {
		t.Fatalf("expected last_error to be populated")
	}

	dlqMsg := dlq.msgs[0]
	if dlqMsg.JobID != jobID {
		t.Fatalf("expected DLQ job_id=%s, got %s", jobID, dlqMsg.JobID)
	}
	if dlqMsg.Attempt != 2 {
		t.Fatalf("expected DLQ attempt=2, got %d", dlqMsg.Attempt)
	}
	if dlqMsg.OriginalSubject != "jobs.default" {
		t.Fatalf("expected original subject jobs.default, got %s", dlqMsg.OriginalSubject)
	}
}

func TestHandleMessage_InvalidJSON_AcksAndSkips(t *testing.T) {
	ctx := context.Background()

	st := newTestStore(t)
	met := newTestMetrics()
	dlq := &fakeDLQPublisher{}

	msg := &fakeMessage{
		subject: "jobs.default",
		data:    []byte(`{"job_id":`), // malformed JSON
		attempt: 0,
	}

	err := handleMessage(ctx, st, dlq, msg, met)
	if err != nil {
		t.Fatalf("handleMessage error: %v", err)
	}

	if !msg.acked {
		t.Fatalf("expected invalid JSON message to be acked")
	}
	if msg.termed {
		t.Fatalf("did not expect invalid JSON message to be termed")
	}
	if msg.nakDelay != 0 {
		t.Fatalf("did not expect invalid JSON message to be retried")
	}
	if len(dlq.msgs) != 0 {
		t.Fatalf("did not expect invalid JSON message to be sent to DLQ")
	}
}
