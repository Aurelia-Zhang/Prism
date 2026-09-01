// Package observability persists and exports Prism runtime traces.
package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/Aurelia-Zhang/Prism/internal/trace"
	_ "modernc.org/sqlite"
)

const schemaVersion = 1

// ErrTraceNotFound is returned when a trace ID is not present in SQLite.
var ErrTraceNotFound = errors.New("trace not found")

// Store is a fixed schema-version-1 SQLite trace store. A single connection
// keeps in-memory databases deterministic and matches the existing stores.
type Store struct {
	db *sql.DB
}

// Open opens a SQLite file and initializes the trace schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// OpenTraceStore is an explicit alias for callers that have more than one
// SQLite store in scope.
func OpenTraceStore(path string) (*Store, error) { return Open(path) }

// Close closes the underlying SQLite file.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) init() error {
	if s == nil || s.db == nil {
		return errors.New("observability store is nil")
	}
	_, err := s.db.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE IF NOT EXISTS trace_schema_version (version INTEGER NOT NULL);
		INSERT INTO trace_schema_version(version)
			SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM trace_schema_version);
		CREATE TABLE IF NOT EXISTS traces (
			trace_id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS trace_spans (
			trace_id TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
			span_id TEXT NOT NULL,
			parent_span_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			start_time TEXT NOT NULL,
			end_time TEXT NOT NULL DEFAULT '',
			start_sequence INTEGER NOT NULL,
			end_sequence INTEGER NOT NULL DEFAULT 0,
			usage_json TEXT NOT NULL DEFAULT '{}',
			attributes_json TEXT NOT NULL DEFAULT '{}',
			error_json TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(trace_id, span_id)
		);
		CREATE INDEX IF NOT EXISTS trace_spans_order_idx ON trace_spans(trace_id, start_sequence);
	`)
	if err != nil {
		return err
	}
	var version int
	if err := s.db.QueryRow(`SELECT version FROM trace_schema_version LIMIT 1`).Scan(&version); err != nil {
		return err
	}
	if version != schemaVersion {
		return fmt.Errorf("unsupported observability schema version %d", version)
	}
	return nil
}

// Save atomically replaces one trace and all its spans.
func (s *Store) Save(ctx context.Context, snapshot trace.Snapshot) error {
	if s == nil || s.db == nil {
		return errors.New("observability store is nil")
	}
	traceID := snapshot.TraceID
	if traceID == "" {
		traceID = traceIDFromSpans(snapshot.Spans)
	}
	if traceID == "" {
		return errors.New("trace ID is required")
	}
	createdAt := time.Now().UTC()
	if len(snapshot.Spans) > 0 && !snapshot.Spans[0].StartTime.IsZero() {
		createdAt = snapshot.Spans[0].StartTime.UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM traces WHERE trace_id = ?`, traceID); err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO traces(trace_id, created_at) VALUES(?, ?)`, traceID, stamp(createdAt))
	}
	for _, span := range snapshot.Spans {
		if err != nil {
			break
		}
		usage, marshalErr := json.Marshal(span.Usage)
		if marshalErr != nil {
			err = marshalErr
			break
		}
		attributes, marshalErr := json.Marshal(span.Attributes)
		if marshalErr != nil {
			err = marshalErr
			break
		}
		encodedError := ""
		if span.Error != nil {
			encoded, marshalErr := marshalError(span.Error)
			if marshalErr != nil {
				err = marshalErr
				break
			}
			encodedError = string(encoded)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO trace_spans(trace_id, span_id, parent_span_id, name, status, start_time, end_time, start_sequence, end_sequence, usage_json, attributes_json, error_json) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			traceID, span.ID, span.ParentSpanID, span.Name, span.Status, stamp(span.StartTime), optionalStamp(span.EndTime), span.StartSequence, span.EndSequence, string(usage), string(attributes), encodedError)
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Write is a concise alias for Save.
func (s *Store) Write(ctx context.Context, snapshot trace.Snapshot) error {
	return s.Save(ctx, snapshot)
}

// Load reads one persisted trace without invoking any runtime component.
func (s *Store) Load(ctx context.Context, traceID string) (trace.Snapshot, error) {
	if s == nil || s.db == nil {
		return trace.Snapshot{}, errors.New("observability store is nil")
	}
	var storedID string
	if err := s.db.QueryRowContext(ctx, `SELECT trace_id FROM traces WHERE trace_id = ?`, traceID).Scan(&storedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return trace.Snapshot{}, fmt.Errorf("%w: %s", ErrTraceNotFound, traceID)
		}
		return trace.Snapshot{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT span_id, parent_span_id, name, status, start_time, end_time, start_sequence, end_sequence, usage_json, attributes_json, error_json FROM trace_spans WHERE trace_id = ? ORDER BY start_sequence, span_id`, traceID)
	if err != nil {
		return trace.Snapshot{}, err
	}
	defer rows.Close()
	snapshot := trace.Snapshot{TraceID: storedID, Spans: make([]trace.SpanSnapshot, 0)}
	for rows.Next() {
		var span trace.SpanSnapshot
		var status, start, end, usageJSON, attributesJSON, errorJSON string
		if err := rows.Scan(&span.ID, &span.ParentSpanID, &span.Name, &status, &start, &end, &span.StartSequence, &span.EndSequence, &usageJSON, &attributesJSON, &errorJSON); err != nil {
			return trace.Snapshot{}, err
		}
		span.Status = trace.Status(status)
		span.StartTime, err = time.Parse(time.RFC3339Nano, start)
		if err != nil {
			return trace.Snapshot{}, err
		}
		span.EndTime = parseOptional(end)
		if err := json.Unmarshal([]byte(usageJSON), &span.Usage); err != nil {
			return trace.Snapshot{}, err
		}
		if err := json.Unmarshal([]byte(attributesJSON), &span.Attributes); err != nil {
			return trace.Snapshot{}, err
		}
		if errorJSON != "" {
			span.Error, err = unmarshalError(errorJSON)
			if err != nil {
				return trace.Snapshot{}, err
			}
		}
		snapshot.Spans = append(snapshot.Spans, span)
	}
	if err := rows.Err(); err != nil {
		return trace.Snapshot{}, err
	}
	return snapshot, nil
}

// Read is a concise alias for Load.
func (s *Store) Read(ctx context.Context, traceID string) (trace.Snapshot, error) {
	return s.Load(ctx, traceID)
}

func traceIDFromSpans(spans []trace.SpanSnapshot) string {
	for _, span := range spans {
		if span.ParentSpanID == "" {
			return span.ID
		}
	}
	if len(spans) > 0 {
		return spans[0].ID
	}
	return ""
}

func stamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func optionalStamp(value time.Time) string { return stamp(value) }

func parseOptional(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

type persistedError struct {
	Code       string        `json:"code"`
	Message    string        `json:"message"`
	Retryable  bool          `json:"retryable,omitempty"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
}

func marshalError(source *provider.Error) ([]byte, error) {
	return json.Marshal(persistedError{Code: source.Code, Message: source.Message, Retryable: source.Retryable, RetryAfter: source.RetryAfter})
}

func unmarshalError(encoded string) (*provider.Error, error) {
	var stored persistedError
	if err := json.Unmarshal([]byte(encoded), &stored); err != nil {
		return nil, err
	}
	return &provider.Error{Code: stored.Code, Message: stored.Message, Retryable: stored.Retryable, RetryAfter: stored.RetryAfter}, nil
}
