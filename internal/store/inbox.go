package store

import (
	"context"
	"database/sql"
	"time"
)

// HasProcessedMessage returns true if msgID is already recorded as processed.
// Used by workers to ensure idempotency under at-least-once delivery.
func (s *Store) HasProcessedMessage(ctx context.Context, msgID string) (bool, error) {
	var dummy int
	err := s.DB.QueryRowContext(ctx, `
SELECT 1
FROM processed_messages
WHERE msg_id = ?
LIMIT 1
`, msgID).Scan(&dummy)

	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}

// MarkMessageProcessed inserts msgID into processed_messages.
// If it already exists, this is a no-op (idempotent).
func (s *Store) MarkMessageProcessed(ctx context.Context, msgID string) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT OR IGNORE INTO processed_messages(msg_id, processed_at_unix)
VALUES(?, ?)
`, msgID, time.Now().Unix())
	return err
}

// dedupe logic
