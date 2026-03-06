package types

type JobStatus string

const (
	StatusQueued    JobStatus = "queued"
	StatusRunning   JobStatus = "running"
	StatusSucceeded JobStatus = "succeeded"
	StatusFailed    JobStatus = "failed"
	StatusDLQ       JobStatus = "dlq"
)

type Job struct {
	JobID       string
	Queue       string
	Type        string
	PayloadJSON string
	Status      JobStatus
	Attempt     int
	MaxAttempts int
	LastError   string
	CreatedAt   int64
	UpdatedAt   int64
}
