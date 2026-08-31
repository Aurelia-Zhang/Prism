package memory

import (
	"context"
	"path/filepath"
	"testing"
)

type fixtureEmbedder struct {
	vectors map[string][]float32
}

func (e fixtureEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if vector, ok := e.vectors[text]; ok {
		return vector, nil
	}
	return []float32{0, 1}, nil
}

func TestMemoryScopesAndDeleteAreIsolated(t *testing.T) {
	store := openFixtureStore(t)
	for _, item := range []struct {
		scope Scope
		id    string
		text  string
	}{
		{ScopeSession, "session-a", "session secret"},
		{ScopeProject, "project-a", "project architecture"},
		{ScopeLongTerm, "global", "long term preference"},
	} {
		entry, err := store.Write(context.Background(), item.scope, item.id, item.text, nil)
		if err != nil {
			t.Fatal(err)
		}
		if item.scope == ScopeSession {
			if err := store.Delete(context.Background(), item.scope, item.id, entry.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	results, err := store.Recall(context.Background(), ScopeRef{Scope: ScopeProject, ScopeID: "project-a"}, "architecture", 3, SearchBM25)
	if err != nil || len(results) != 1 || results[0].Content != "project architecture" {
		t.Fatalf("project scope recall failed: results=%#v err=%v", results, err)
	}
	other, err := store.Recall(context.Background(), ScopeRef{Scope: ScopeProject, ScopeID: "project-b"}, "architecture", 3, SearchBM25)
	if err != nil || len(other) != 0 {
		t.Fatalf("scope leaked: results=%#v err=%v", other, err)
	}
	deleted, err := store.Recall(context.Background(), ScopeRef{Scope: ScopeSession, ScopeID: "session-a"}, "secret", 3, SearchBM25)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("delete failed: results=%#v err=%v", deleted, err)
	}
	longTerm, err := store.Recall(context.Background(), ScopeRef{Scope: ScopeLongTerm, ScopeID: "global"}, "preference", 3, SearchBM25)
	if err != nil || len(longTerm) != 1 || longTerm[0].Content != "long term preference" {
		t.Fatalf("long-term scope recall failed: results=%#v err=%v", longTerm, err)
	}
}

func TestMemoryBM25VectorAndHybridReturnExpectedTopK(t *testing.T) {
	embedder := fixtureEmbedder{vectors: map[string][]float32{
		"database":        {1, 0},
		"sqlite database": {0.9, 0.1},
		"garden":          {0, 1},
	}}
	store := openStore(t, embedder)
	for _, text := range []string{"database indexing", "sqlite database", "garden notes"} {
		if _, err := store.Write(context.Background(), ScopeProject, "p", text, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []SearchMode{SearchBM25, SearchVector, SearchHybrid} {
		results, err := store.Recall(context.Background(), ScopeRef{Scope: ScopeProject, ScopeID: "p"}, "database", 1, mode)
		if err != nil || len(results) != 1 || results[0].Content == "garden notes" {
			t.Fatalf("mode %s returned wrong top-k: results=%#v err=%v", mode, results, err)
		}
		t.Logf("recall evidence: mode=%s top1=%q", mode, results[0].Content)
	}
}

func openFixtureStore(t *testing.T) *Store {
	t.Helper()
	return openStore(t, fixtureEmbedder{})
}

func openStore(t *testing.T, embedder Embedder) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "memory.db"), embedder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
