package store

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	DB *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`
PRAGMA journal_mode=WAL;
`,
		`
PRAGMA synchronous=NORMAL;
`,
		`
CREATE TABLE IF NOT EXISTS jobs (
  job_id TEXT PRIMARY KEY,
  queue TEXT NOT NULL,
  type TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  status TEXT NOT NULL,
  attempt INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 5,
  last_error TEXT NOT NULL DEFAULT '',
  created_at_unix INTEGER NOT NULL,
  updated_at_unix INTEGER NOT NULL
);
`,
		`
CREATE TABLE IF NOT EXISTS idempotency_keys (
  key TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  created_at_unix INTEGER NOT NULL
);
`,
		`
CREATE TABLE IF NOT EXISTS outbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  topic TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  created_at_unix INTEGER NOT NULL,
  sent_at_unix INTEGER NOT NULL DEFAULT 0
);
`,
		`
CREATE INDEX IF NOT EXISTS idx_outbox_unsent ON outbox(sent_at_unix, id);
`,
		`
CREATE TABLE IF NOT EXISTS processed_messages (
  msg_id TEXT PRIMARY KEY,
  processed_at_unix INTEGER NOT NULL
);
`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}

	if err := ensureColumn(db, "jobs", "created_at_unix_ms", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(db, "jobs", "updated_at_unix_ms", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}

	// Backfill existing rows if needed using second-resolution values.
	if _, err := db.Exec(`
UPDATE jobs
SET created_at_unix_ms = created_at_unix * 1000
WHERE created_at_unix_ms = 0
`); err != nil {
		return err
	}

	if _, err := db.Exec(`
UPDATE jobs
SET updated_at_unix_ms = updated_at_unix * 1000
WHERE updated_at_unix_ms = 0
`); err != nil {
		return err
	}

	return nil
}

func ensureColumn(db *sql.DB, tableName, columnName, columnDef string) error {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, tableName))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var ctype string
		var notNull int
		var dfltValue sql.NullString
		var pk int

		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == columnName {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, tableName, columnName, columnDef))
	return err
}
