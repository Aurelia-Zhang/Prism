// Package output persists large tool output outside the model context.
package output

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Aurelia-Zhang/Prism/internal/tool"
)

// Record is metadata for one persisted tool output.
type Record struct {
	ID        string    `json:"id"`
	Hash      string    `json:"hash"`
	Size      int64     `json:"size"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
}

// Store writes bytes to a configurable directory and records metadata in the
// caller's SQLite database. The database is normally memory.Store.DB().
type Store struct {
	db        *sql.DB
	directory string
}

// NewStore creates the output metadata table in an existing SQLite database.
func NewStore(db *sql.DB, directory string) (*Store, error) {
	if db == nil {
		return nil, errors.New("output store database is required")
	}
	if directory == "" {
		return nil, errors.New("output directory is required")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS outputs (
		id TEXT PRIMARY KEY,
		hash TEXT NOT NULL,
		size INTEGER NOT NULL,
		path TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`); err != nil {
		return nil, err
	}
	return &Store{db: db, directory: directory}, nil
}

// Persist stores complete content and returns its hash, byte size, and path.
func (s *Store) Persist(ctx context.Context, content string) (Record, error) {
	if s == nil || s.db == nil {
		return Record{}, errors.New("output store is nil")
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	bytes := []byte(content)
	digest := sha256.Sum256(bytes)
	hash := hex.EncodeToString(digest[:])
	id := fmt.Sprintf("out-%d-%s", time.Now().UnixNano(), hash[:12])
	path := filepath.Join(s.directory, id+".txt")
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		return Record{}, err
	}
	record := Record{ID: id, Hash: hash, Size: int64(len(bytes)), Path: path, CreatedAt: time.Now().UTC()}
	_, err := s.db.ExecContext(ctx, `INSERT INTO outputs(id, hash, size, path, created_at) VALUES(?, ?, ?, ?, ?)`, record.ID, record.Hash, record.Size, record.Path, record.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		_ = os.Remove(path)
		return Record{}, err
	}
	return record, nil
}

// Fetch reads a byte range from a persisted output. A non-positive limit means
// read through the end of the output.
func (s *Store) Fetch(ctx context.Context, id string, offset, limit int64) ([]byte, Record, error) {
	record, err := s.Lookup(ctx, id)
	if err != nil {
		return nil, Record{}, err
	}
	if offset < 0 || limit < 0 {
		return nil, Record{}, errors.New("output offset and limit must not be negative")
	}
	content, err := os.ReadFile(record.Path)
	if err != nil {
		return nil, Record{}, err
	}
	if int64(len(content)) <= offset {
		return []byte{}, record, nil
	}
	end := int64(len(content))
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return content[offset:end], record, nil
}

// Lookup returns output metadata without reading the file.
func (s *Store) Lookup(ctx context.Context, id string) (Record, error) {
	if s == nil || s.db == nil {
		return Record{}, errors.New("output store is nil")
	}
	var record Record
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id, hash, size, path, created_at FROM outputs WHERE id = ?`, id).Scan(&record.ID, &record.Hash, &record.Size, &record.Path, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, fmt.Errorf("output %q not found", id)
		}
		return Record{}, err
	}
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return record, nil
}

// Tool returns the fetch_output tool used by the Agent Loop when output
// persistence is enabled.
func (s *Store) Tool() tool.Tool {
	return tool.Tool{
		Name:        "fetch_output",
		Description: "fetch a byte range from a persisted large tool output",
		Schema:      json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":0}},"required":["id"],"additionalProperties":false}`),
		Handler: func(ctx context.Context, arguments json.RawMessage) (string, error) {
			var request struct {
				ID     string `json:"id"`
				Offset int64  `json:"offset"`
				Limit  int64  `json:"limit"`
			}
			if err := json.Unmarshal(arguments, &request); err != nil {
				return "", err
			}
			content, record, err := s.Fetch(ctx, request.ID, request.Offset, request.Limit)
			if err != nil {
				return "", err
			}
			response, err := json.Marshal(struct {
				ID      string `json:"output_id"`
				Offset  int64  `json:"offset"`
				Size    int64  `json:"original_size"`
				Content string `json:"content"`
			}{record.ID, request.Offset, record.Size, string(content)})
			return string(response), err
		},
	}
}
