// Package queue persists heartbeats on disk between `inlinr heartbeat`
// invocations and server uploads. SQLite-backed so multiple plugin-spawned
// processes can enqueue concurrently without corrupting state.
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/inlinrhq/inlinr-cli/internal/heartbeat"
)

type Queue struct {
	db *sql.DB
}

// Open initialises the SQLite store, creating the file + schema if missing.
func Open(path string) (*Queue, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS heartbeats (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			body     TEXT    NOT NULL,
			enqueued INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_enqueued ON heartbeats(enqueued);

		CREATE TABLE IF NOT EXISTS last_emits (
			entity TEXT NOT NULL,
			branch TEXT NOT NULL DEFAULT '',
			editor TEXT NOT NULL DEFAULT '',
			time   REAL NOT NULL,
			PRIMARY KEY (entity, branch, editor)
		);
	`); err != nil {
		db.Close()
		return nil, err
	}
	return &Queue{db: db}, nil
}

// ShouldEmit returns true if a heartbeat for (entity, branch, editor) at
// `time` should be enqueued. Saves (isWrite) always pass. Otherwise we
// suppress if the previous emit for the same key was within rateLimitSec.
// rateLimitSec <= 0 disables the check.
func (q *Queue) ShouldEmit(ctx context.Context, h heartbeat.Heartbeat, rateLimitSec int) (bool, error) {
	if h.IsWrite || rateLimitSec <= 0 {
		return true, nil
	}
	branch := strOrEmpty(h.Branch)
	editor := strOrEmpty(h.Editor)

	var last float64
	err := q.db.QueryRowContext(ctx,
		`SELECT time FROM last_emits WHERE entity = ? AND branch = ? AND editor = ?`,
		h.Entity, branch, editor,
	).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && h.Time-last < float64(rateLimitSec) {
		return false, nil
	}
	return true, nil
}

// MarkEmitted records the time of the accepted beat so ShouldEmit can
// throttle subsequent calls. Caller should invoke this only after enqueueing.
func (q *Queue) MarkEmitted(ctx context.Context, h heartbeat.Heartbeat) error {
	_, err := q.db.ExecContext(ctx,
		`INSERT INTO last_emits (entity, branch, editor, time) VALUES (?, ?, ?, ?)
		 ON CONFLICT(entity, branch, editor) DO UPDATE SET time = excluded.time`,
		h.Entity, strOrEmpty(h.Branch), strOrEmpty(h.Editor), h.Time,
	)
	return err
}

func strOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (q *Queue) Close() error { return q.db.Close() }

// Enqueue stores a heartbeat for later flush.
func (q *Queue) Enqueue(ctx context.Context, h heartbeat.Heartbeat) error {
	body, err := json.Marshal(h)
	if err != nil {
		return err
	}
	_, err = q.db.ExecContext(ctx,
		`INSERT INTO heartbeats (body, enqueued) VALUES (?, ?)`,
		string(body), time.Now().Unix(),
	)
	return err
}

// Count returns the number of queued heartbeats.
func (q *Queue) Count(ctx context.Context) (int, error) {
	var n int
	err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM heartbeats`).Scan(&n)
	return n, err
}

// Batch represents a chunk of heartbeats pulled from the queue, with their
// row IDs so the caller can selectively delete successfully-sent ones.
type Batch struct {
	IDs   []int64
	Beats []heartbeat.Heartbeat
}

// Take pops up to `limit` heartbeats off the queue (FIFO). Does not delete
// them — the caller must call Ack/Nack after the server responds.
func (q *Queue) Take(ctx context.Context, limit int) (Batch, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, body FROM heartbeats ORDER BY id ASC LIMIT ?`, limit,
	)
	if err != nil {
		return Batch{}, err
	}
	defer rows.Close()

	var b Batch
	for rows.Next() {
		var id int64
		var body string
		if err := rows.Scan(&id, &body); err != nil {
			return Batch{}, err
		}
		var h heartbeat.Heartbeat
		if err := json.Unmarshal([]byte(body), &h); err != nil {
			// corrupt row — drop it so it doesn't block the queue forever
			_, _ = q.db.ExecContext(ctx, `DELETE FROM heartbeats WHERE id = ?`, id)
			continue
		}
		b.IDs = append(b.IDs, id)
		b.Beats = append(b.Beats, h)
	}
	return b, rows.Err()
}

// Ack deletes rows the server accepted (201/202) or rejected as bad (400).
// Both outcomes are terminal — no point keeping them.
func (q *Queue) Ack(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := ""
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = id
	}
	_, err := q.db.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM heartbeats WHERE id IN (%s)`, placeholders),
		args...,
	)
	return err
}
