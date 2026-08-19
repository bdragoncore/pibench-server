package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Store is a minimal SQLite-backed session log: just enough to list past
// chats and replay a conversation, not opencode's full schema.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS session (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS message (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	role TEXT NOT NULL,
	data TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS message_session_idx ON message(session_id, created_at);
`

func openStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // modernc.org/sqlite: keep it simple, avoid locking contention
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) createSession(title string) (string, error) {
	id := uuid.NewString()
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`INSERT INTO session (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)`, id, title, now, now)
	return id, err
}

func (s *Store) touchSession(id string) error {
	_, err := s.db.Exec(`UPDATE session SET updated_at = ? WHERE id = ?`, time.Now().UnixMilli(), id)
	return err
}

func (s *Store) addMessage(sessionID string, msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO message (id, session_id, role, data, created_at) VALUES (?, ?, ?, ?, ?)`,
		uuid.NewString(), sessionID, msg.Role, string(data), time.Now().UnixMilli())
	if err != nil {
		return err
	}
	return s.touchSession(sessionID)
}

func (s *Store) loadMessages(sessionID string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT data FROM message WHERE session_id = ? ORDER BY created_at ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var m Message
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

type sessionSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

func (s *Store) listSessions() ([]sessionSummary, error) {
	rows, err := s.db.Query(`SELECT id, title, created_at, updated_at FROM session ORDER BY updated_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []sessionSummary
	for rows.Next() {
		var ss sessionSummary
		if err := rows.Scan(&ss.ID, &ss.Title, &ss.CreatedAt, &ss.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

func (s *Store) sessionExists(id string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM session WHERE id = ?`, id).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check session: %w", err)
	}
	return count > 0, nil
}

func (s *Store) deleteSession(id string) error {
	if _, err := s.db.Exec(`DELETE FROM message WHERE session_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM session WHERE id = ?`, id)
	return err
}

