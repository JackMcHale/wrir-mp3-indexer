package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type Event struct {
	Sequence  int64  `json:"sequence"`
	Operation string `json:"operation"`
	ID        string `json:"id"`
	Track     *Track `json:"track,omitempty"`
}

func OpenStore(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000; PRAGMA journal_mode = WAL;`); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.createSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) createSchema() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS scans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			root TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			completed_at TEXT
		);
		CREATE TABLE IF NOT EXISTS tracks (
			id TEXT PRIMARY KEY,
			root TEXT NOT NULL,
			path TEXT NOT NULL,
			size INTEGER NOT NULL,
			mod_time_ns INTEGER NOT NULL,
			document TEXT NOT NULL,
			last_seen_run INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS outbox (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			operation TEXT NOT NULL CHECK (operation IN ('upsert', 'delete')),
			track_id TEXT NOT NULL,
			document TEXT,
			delivered INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS outbox_pending ON outbox(delivered, sequence);
	`)
	return err
}

func (s *Store) BeginScan(root string) (int64, error) {
	result, err := s.db.Exec(`INSERT INTO scans(root) VALUES (?)`, root)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) Unchanged(id string, size, modTimeNS int64, force bool) (bool, error) {
	if force {
		return false, nil
	}
	var oldSize, oldModTimeNS int64
	err := s.db.QueryRow(`SELECT size, mod_time_ns FROM tracks WHERE id = ?`, id).Scan(&oldSize, &oldModTimeNS)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return oldSize == size && oldModTimeNS == modTimeNS, nil
}

func (s *Store) MarkSeen(runID int64, id string) error {
	_, err := s.db.Exec(`UPDATE tracks SET last_seen_run = ? WHERE id = ?`, runID, id)
	return err
}

func (s *Store) Upsert(runID int64, track Track, size, modTimeNS int64) (bool, error) {
	document, err := json.Marshal(track)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var root string
	if err := tx.QueryRow(`SELECT root FROM scans WHERE id = ?`, runID).Scan(&root); err != nil {
		return false, err
	}

	var previousDocument string
	err = tx.QueryRow(`SELECT document FROM tracks WHERE id = ?`, track.ID).Scan(&previousDocument)
	isNew := errors.Is(err, sql.ErrNoRows)
	if err != nil && !isNew {
		return false, err
	}
	changed := isNew || previousDocument != string(document)
	if changed {
		if _, err := tx.Exec(`INSERT INTO outbox(operation, track_id, document) VALUES ('upsert', ?, ?)`, track.ID, string(document)); err != nil {
			return false, err
		}
	}
	_, err = tx.Exec(`
		INSERT INTO tracks(id, root, path, size, mod_time_ns, document, last_seen_run)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			root = excluded.root,
			path = excluded.path,
			size = excluded.size,
			mod_time_ns = excluded.mod_time_ns,
			document = excluded.document,
			last_seen_run = excluded.last_seen_run
	`, track.ID, root, track.Path, size, modTimeNS, string(document), runID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed, nil
}

func (s *Store) CompleteScan(runID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var root string
	if err := tx.QueryRow(`SELECT root FROM scans WHERE id = ?`, runID).Scan(&root); err != nil {
		return err
	}

	rows, err := tx.Query(`SELECT id FROM tracks WHERE root = ? AND last_seen_run <> ?`, root, runID)
	if err != nil {
		return err
	}
	var missing []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		missing = append(missing, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range missing {
		if _, err := tx.Exec(`INSERT INTO outbox(operation, track_id) VALUES ('delete', ?)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM tracks WHERE id = ? AND root = ?`, id, root); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE scans SET completed_at = CURRENT_TIMESTAMP WHERE id = ?`, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PendingEvents(limit int) ([]Event, error) {
	rows, err := s.db.Query(`
		SELECT sequence, operation, track_id, document
		FROM outbox WHERE delivered = 0 ORDER BY sequence LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var event Event
		var document sql.NullString
		if err := rows.Scan(&event.Sequence, &event.Operation, &event.ID, &document); err != nil {
			return nil, err
		}
		if document.Valid {
			var track Track
			if err := json.Unmarshal([]byte(document.String), &track); err != nil {
				return nil, fmt.Errorf("decode event %d: %w", event.Sequence, err)
			}
			event.Track = &track
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) Ack(sequence int64) error {
	result, err := s.db.Exec(`UPDATE outbox SET delivered = 1 WHERE sequence = ?`, sequence)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("event sequence %d not found", sequence)
	}
	return nil
}
