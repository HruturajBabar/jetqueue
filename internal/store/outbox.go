package store

import (
	"context"
	"database/sql"
	"time"
)

type OutboxRow struct {
	ID          int64
	Topic       string
	PayloadJSON string
}

func (s *Store) EnqueueOutboxTx(ctx context.Context, tx *sql.Tx, topic string, payloadJSON string) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO outbox(topic, payload_json, created_at_unix, sent_at_unix)
VALUES(?, ?, ?, 0)
`, topic, payloadJSON, time.Now().Unix())
	return err
}

func (s *Store) FetchUnsentOutbox(ctx context.Context, limit int) ([]OutboxRow, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, topic, payload_json
FROM outbox
WHERE sent_at_unix = 0
ORDER BY id ASC
LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OutboxRow
	for rows.Next() {
		var r OutboxRow
		if err := rows.Scan(&r.ID, &r.Topic, &r.PayloadJSON); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkOutboxSent(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `
UPDATE outbox SET sent_at_unix = ? WHERE id = ?
`, time.Now().Unix(), id)
	return err
}
