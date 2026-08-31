// Package memory stores scoped memories and performs lexical/vector retrieval.
package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Scope is the isolation boundary for a memory entry.
type Scope string

const (
	ScopeSession  Scope = "session"
	ScopeProject  Scope = "project"
	ScopeLongTerm Scope = "long-term"
)

// SearchMode selects one retrieval signal or their reciprocal-rank fusion.
type SearchMode string

const (
	SearchBM25   SearchMode = "bm25"
	SearchVector SearchMode = "vector"
	SearchHybrid SearchMode = "hybrid"
)

// Embedder is intentionally small so a deterministic vector implementation can
// be used in tests and a real service can be added later.
type Embedder interface {
	Embed(context.Context, string) ([]float32, error)
}

// Entry is a stored memory.
type Entry struct {
	ID        string
	Scope     Scope
	ScopeID   string
	Content   string
	Metadata  map[string]string
	CreatedAt time.Time
}

// Result is a retrieved memory and its score for the selected mode.
type Result struct {
	Entry
	Score float64
}

// ScopeRef identifies one isolated memory namespace.
type ScopeRef struct {
	Scope   Scope
	ScopeID string
}

// Store is a schema-version-1 SQLite memory database. SetMaxOpenConns(1) also
// makes :memory: databases behave predictably in tests and demos.
type Store struct {
	db       *sql.DB
	embedder Embedder
}

// Open opens one SQLite file and initializes the fixed v1 schema.
func Open(path string, embedder Embedder) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, embedder: embedder}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// DB exposes the same database connection for output metadata. Callers should
// close the Store once all output operations are complete.
func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

// Close closes the underlying SQLite file.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) init() error {
	if s == nil || s.db == nil {
		return errors.New("memory store is nil")
	}
	_, err := s.db.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);
		INSERT INTO schema_version(version)
			SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM schema_version);
		CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			scope TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			content TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT '{}',
			embedding BLOB NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS memories_scope_idx ON memories(scope, scope_id);
		CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
			memory_id UNINDEXED,
			scope UNINDEXED,
			scope_id UNINDEXED,
			content
		);`)
	return err
}

// Write inserts a memory and its embedding in one transaction.
func (s *Store) Write(ctx context.Context, scope Scope, scopeID, content string, metadata map[string]string) (Entry, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return Entry{}, err
	}
	if strings.TrimSpace(content) == "" {
		return Entry{}, errors.New("memory content must be non-empty")
	}
	if s == nil || s.db == nil || s.embedder == nil {
		return Entry{}, errors.New("memory store and embedder are required")
	}
	vector, err := s.embedder.Embed(ctx, content)
	if err != nil {
		return Entry{}, fmt.Errorf("embed memory: %w", err)
	}
	if len(vector) == 0 {
		return Entry{}, errors.New("embedder returned an empty vector")
	}
	metadataJSON, err := json.Marshal(metadataOrEmpty(metadata))
	if err != nil {
		return Entry{}, fmt.Errorf("encode memory metadata: %w", err)
	}
	id := memoryID(content)
	created := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO memories(id, scope, scope_id, content, metadata, embedding, created_at) VALUES(?, ?, ?, ?, ?, ?, ?)`, id, scope, scopeID, content, string(metadataJSON), encodeVector(vector), created.Format(time.RFC3339Nano)); err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO memory_fts(memory_id, scope, scope_id, content) VALUES(?, ?, ?, ?)`, id, scope, scopeID, content)
	}
	if err != nil {
		_ = tx.Rollback()
		return Entry{}, err
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, err
	}
	return Entry{ID: id, Scope: scope, ScopeID: scopeID, Content: content, Metadata: metadataOrEmpty(metadata), CreatedAt: created}, nil
}

// Delete removes one memory only when both its ID and scope match.
func (s *Store) Delete(ctx context.Context, scope Scope, scopeID, id string) error {
	if err := validateScope(scope, scopeID); err != nil {
		return err
	}
	if s == nil || s.db == nil {
		return errors.New("memory store is nil")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM memory_fts WHERE memory_id = ? AND scope = ? AND scope_id = ?`, id, scope, scopeID); err == nil {
		_, err = tx.ExecContext(ctx, `DELETE FROM memories WHERE id = ? AND scope = ? AND scope_id = ?`, id, scope, scopeID)
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Recall returns up to k entries from one isolated scope.
func (s *Store) Recall(ctx context.Context, ref ScopeRef, query string, k int, mode SearchMode) ([]Result, error) {
	if err := validateScope(ref.Scope, ref.ScopeID); err != nil {
		return nil, err
	}
	if k <= 0 {
		return []Result{}, nil
	}
	if s == nil || s.db == nil {
		return nil, errors.New("memory store is nil")
	}
	switch mode {
	case SearchBM25:
		return s.recallBM25(ctx, ref, query, k)
	case SearchVector:
		return s.recallVector(ctx, ref, query, k)
	case SearchHybrid:
		return s.recallHybrid(ctx, ref, query, k)
	default:
		return nil, fmt.Errorf("unknown memory search mode %q", mode)
	}
}

func (s *Store) recallBM25(ctx context.Context, ref ScopeRef, query string, k int) ([]Result, error) {
	ftsQuery := ftsQuery(query)
	if ftsQuery == "" {
		return []Result{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.scope, m.scope_id, m.content, m.metadata, m.created_at, bm25(memory_fts) FROM memory_fts JOIN memories m ON m.id = memory_fts.memory_id WHERE memory_fts MATCH ? AND m.scope = ? AND m.scope_id = ? ORDER BY bm25(memory_fts) LIMIT ?`, ftsQuery, ref.Scope, ref.ScopeID, k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]Result, 0, k)
	for rows.Next() {
		entry, score, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, Result{Entry: entry, Score: -score})
	}
	return results, rows.Err()
}

func (s *Store) recallVector(ctx context.Context, ref ScopeRef, query string, k int) ([]Result, error) {
	if s == nil || s.embedder == nil {
		return nil, errors.New("memory embedder is required for vector recall")
	}
	vector, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, scope, scope_id, content, metadata, created_at, embedding FROM memories WHERE scope = ? AND scope_id = ?`, ref.Scope, ref.ScopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]Result, 0)
	for rows.Next() {
		var entry Entry
		var metadataJSON, created string
		var encoded []byte
		if err := rows.Scan(&entry.ID, &entry.Scope, &entry.ScopeID, &entry.Content, &metadataJSON, &created, &encoded); err != nil {
			return nil, err
		}
		entry.Metadata = decodeMetadata(metadataJSON)
		entry.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		results = append(results, Result{Entry: entry, Score: cosine(vector, decodeVector(encoded))})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortResults(results)
	if len(results) > k {
		results = results[:k]
	}
	return results, nil
}

func (s *Store) recallHybrid(ctx context.Context, ref ScopeRef, query string, k int) ([]Result, error) {
	limit := k
	if limit < 20 {
		limit = 20
	}
	lexical, err := s.recallBM25(ctx, ref, query, limit)
	if err != nil {
		return nil, err
	}
	vector, err := s.recallVector(ctx, ref, query, limit)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Result, len(lexical)+len(vector))
	for rank, result := range lexical {
		result.Score = 1.0 / float64(60+rank+1)
		byID[result.ID] = result
	}
	for rank, result := range vector {
		result.Score = byID[result.ID].Score + 1.0/float64(60+rank+1)
		byID[result.ID] = result
	}
	results := make([]Result, 0, len(byID))
	for _, result := range byID {
		results = append(results, result)
	}
	sortResults(results)
	if len(results) > k {
		results = results[:k]
	}
	return results, nil
}

type rowScanner interface{ Scan(...any) error }

func scanEntry(rows rowScanner) (Entry, float64, error) {
	var entry Entry
	var metadataJSON, created string
	var score float64
	if err := rows.Scan(&entry.ID, &entry.Scope, &entry.ScopeID, &entry.Content, &metadataJSON, &created, &score); err != nil {
		return Entry{}, 0, err
	}
	entry.Metadata = decodeMetadata(metadataJSON)
	entry.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return entry, score, nil
}

func sortResults(results []Result) {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].ID < results[j].ID
		}
		return results[i].Score > results[j].Score
	})
}

func validateScope(scope Scope, scopeID string) error {
	if scope != ScopeSession && scope != ScopeProject && scope != ScopeLongTerm {
		return fmt.Errorf("invalid memory scope %q", scope)
	}
	if strings.TrimSpace(scopeID) == "" {
		return errors.New("memory scope ID must be non-empty")
	}
	return nil
}

func ftsQuery(query string) string {
	terms := strings.Fields(strings.TrimSpace(query))
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.ReplaceAll(term, `"`, "")
		if term != "" {
			quoted = append(quoted, `"`+term+`"`)
		}
	}
	return strings.Join(quoted, " OR ")
}

func memoryID(content string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", time.Now().UnixNano(), content)))
	return "mem-" + hex.EncodeToString(hash[:8])
}

func encodeVector(vector []float32) []byte {
	encoded := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(encoded[i*4:], math.Float32bits(value))
	}
	return encoded
}

func decodeVector(encoded []byte) []float32 {
	vector := make([]float32, len(encoded)/4)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(encoded[i*4:]))
	}
	return vector
}

func cosine(left, right []float32) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return 0
	}
	var dot, leftNorm, rightNorm float64
	for i := range left {
		x, y := float64(left[i]), float64(right[i])
		dot += x * y
		leftNorm += x * x
		rightNorm += y * y
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return dot / math.Sqrt(leftNorm*rightNorm)
}

func metadataOrEmpty(metadata map[string]string) map[string]string {
	if metadata == nil {
		return map[string]string{}
	}
	copy := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copy[key] = value
	}
	return copy
}

func decodeMetadata(encoded string) map[string]string {
	metadata := map[string]string{}
	if json.Unmarshal([]byte(encoded), &metadata) != nil || metadata == nil {
		return map[string]string{}
	}
	return metadata
}
