package types

type JobStatus string

const (
	StatusQueued         JobStatus = "queued"
	StatusRunning        JobStatus = "running"
	StatusSucceeded      JobStatus = "succeeded"
	StatusRetryScheduled JobStatus = "retry_scheduled"
	StatusFailed         JobStatus = "failed"
	StatusDLQ            JobStatus = "dlq"
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

type JobMsg struct {
	JobID       string `json:"job_id"`
	Queue       string `json:"queue"`
	Type        string `json:"type"`
	PayloadJSON string `json:"payload_json"`
	Attempt     int    `json:"attempt"`
	MaxAttempts int    `json:"max_attempts"`
	CreatedAt   int64  `json:"created_at_unix"`
}

type EchoPayload struct {
	Message string `json:"message"`
}

type SleepPayload struct {
	DurationMS int `json:"duration_ms"`
}

type DLQMsg struct {
	JobID           string `json:"job_id"`
	Queue           string `json:"queue"`
	Type            string `json:"type"`
	PayloadJSON     string `json:"payload_json"`
	Attempt         int    `json:"attempt"`
	MaxAttempts     int    `json:"max_attempts"`
	FailedAtUnix    int64  `json:"failed_at_unix"`
	LastError       string `json:"last_error"`
	OriginalSubject string `json:"original_subject"`
}
