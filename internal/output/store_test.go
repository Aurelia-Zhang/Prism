package output

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aurelia-Zhang/Prism/internal/memory"
)

func TestPersistFetchAndHash(t *testing.T) {
	store, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	outputs, err := NewStore(store.DB(), filepath.Join(t.TempDir(), "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	original := "0123456789-large-output"
	record, err := outputs.Persist(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(original))
	if record.Hash != hex.EncodeToString(digest[:]) || record.Size != int64(len(original)) {
		t.Fatalf("metadata mismatch: %#v", record)
	}
	content, fetched, err := outputs.Fetch(context.Background(), record.ID, 5, 7)
	if err != nil || string(content) != original[5:12] || fetched.ID != record.ID {
		t.Fatalf("range fetch failed: content=%q record=%#v err=%v", content, fetched, err)
	}
	toolOutput, _, err := outputs.Fetch(context.Background(), record.ID, 0, 0)
	if err != nil || string(toolOutput) != original {
		t.Fatalf("full fetch failed: %q %v", toolOutput, err)
	}
	toolResult, err := outputs.Tool().Handler(context.Background(), json.RawMessage(`{"id":"`+record.ID+`","offset":2,"limit":4}`))
	if err != nil || !strings.Contains(toolResult, `"content":"2345"`) {
		t.Fatalf("fetch_output tool failed: result=%q err=%v", toolResult, err)
	}
}
