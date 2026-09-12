package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct{ db *sql.DB }

func openStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("CREATE TABLE IF NOT EXISTS runs (id TEXT PRIMARY KEY, created TEXT NOT NULL, payload TEXT NOT NULL)"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec("CREATE TABLE IF NOT EXISTS shared_key_gate (id INTEGER PRIMARY KEY CHECK(id=1), last_started INTEGER NOT NULL)"); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err = s.interruptRuns(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Save(run *Run) error {
	payload, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO runs (id,created,payload) VALUES (?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", run.ID, run.Created, string(payload))
	return err
}

// Create reserves the shared AI allowance and saves the queued run atomically.
func (s *Store) Create(run *Run, sharedKey bool, now time.Time) (bool, time.Time, error) {
	if !sharedKey {
		return true, time.Time{}, s.Save(run)
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return false, time.Time{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, time.Time{}, err
	}
	defer tx.Rollback()
	cutoff := now.Unix() - int64(time.Hour.Seconds())
	result, err := tx.Exec(`INSERT INTO shared_key_gate (id,last_started) VALUES (1,?)
		ON CONFLICT(id) DO UPDATE SET last_started=excluded.last_started
		WHERE shared_key_gate.last_started <= ?`, now.Unix(), cutoff)
	if err != nil {
		return false, time.Time{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, time.Time{}, err
	}
	if changed == 0 {
		var last int64
		if err := tx.QueryRow("SELECT last_started FROM shared_key_gate WHERE id=1").Scan(&last); err != nil {
			return false, time.Time{}, err
		}
		return false, time.Unix(last, 0).Add(time.Hour).UTC(), nil
	}
	if _, err := tx.Exec("INSERT INTO runs (id,created,payload) VALUES (?,?,?)", run.ID, run.Created, string(payload)); err != nil {
		return false, time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return false, time.Time{}, err
	}
	return true, time.Time{}, nil
}

func (s *Store) SharedKeyNextAt() (time.Time, error) {
	var last int64
	err := s.db.QueryRow("SELECT last_started FROM shared_key_gate WHERE id=1").Scan(&last)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(last, 0).Add(time.Hour).UTC(), nil
}

func (s *Store) Get(id string) (*Run, error) {
	var payload []byte
	if err := s.db.QueryRow("SELECT payload FROM runs WHERE id=?", id).Scan(&payload); err != nil {
		return nil, err
	}
	var run Run
	if err := json.Unmarshal(payload, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *Store) GetForOwner(id, ownerID string) (*Run, error) {
	var payload []byte
	if err := s.db.QueryRow("SELECT payload FROM runs WHERE id=? AND json_extract(payload,'$.owner_id')=?", id, ownerID).Scan(&payload); err != nil {
		return nil, err
	}
	var run Run
	if err := json.Unmarshal(payload, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *Store) ListForOwner(ownerID string, limit int) ([]*Run, error) {
	rows, err := s.db.Query("SELECT payload FROM runs WHERE json_extract(payload,'$.owner_id')=? ORDER BY created DESC LIMIT ?", ownerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []*Run{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var run Run
		if err := json.Unmarshal(payload, &run); err != nil {
			return nil, err
		}
		runs = append(runs, &run)
	}
	return runs, rows.Err()
}

func (s *Store) List(limit int) ([]*Run, error) {
	rows, err := s.db.Query("SELECT payload FROM runs ORDER BY created DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []*Run{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var run Run
		if err := json.Unmarshal(payload, &run); err != nil {
			return nil, err
		}
		runs = append(runs, &run)
	}
	return runs, rows.Err()
}

func (s *Store) CountToday() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM runs WHERE substr(created,1,10)=? AND json_extract(payload,'$.mode')='retrieval'", time.Now().UTC().Format("2006-01-02")).Scan(&count)
	return count, err
}

func (s *Store) interruptRuns() error {
	runs, err := s.List(1000000)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.Status == "queued" || run.Status == "running" {
			run.Status = "failed"
			run.Error = "Server restarted during this investigation. Please retry."
			if err := s.Save(run); err != nil {
				return fmt.Errorf("mark interrupted run: %w", err)
			}
		}
	}
	return nil
}
