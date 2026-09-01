package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aurelia-Zhang/Prism/internal/eval"
	"github.com/Aurelia-Zhang/Prism/internal/observability"
	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/Aurelia-Zhang/Prism/internal/trace"
)

func TestTraceCLIShowExportAndMissing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "trace.db")
	store, err := observability.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := trace.Snapshot{TraceID: "cli-trace", Spans: []trace.SpanSnapshot{{ID: "cli-trace", Name: "agent.run", Status: trace.StatusOK, StartTime: time.Unix(100, 0).UTC(), EndTime: time.Unix(100, 1_000_000).UTC(), StartSequence: 1, EndSequence: 1, Usage: provider.Usage{InputTokens: 1}}}}
	if err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	show := captureStdout(t, func() int { return run([]string{"trace", "show", dbPath, "cli-trace"}) })
	if !strings.Contains(show, "trace cli-trace") || !strings.Contains(show, "agent.run status=ok") {
		t.Fatalf("show output = %q", show)
	}
	export := captureStdout(t, func() int { return run([]string{"trace", "export", dbPath, "cli-trace"}) })
	if !strings.Contains(export, `"trace_id": "cli-trace"`) || !strings.Contains(export, `"duration": "1ms"`) {
		t.Fatalf("export output = %q", export)
	}
	if code := run([]string{"trace", "show", dbPath, "missing"}); code == 0 {
		t.Fatal("missing trace unexpectedly succeeded")
	}
}

func TestEvalCLIWritesJSONFactSourceAndMarkdown(t *testing.T) {
	output := filepath.Join(t.TempDir(), "report.json")
	if code := run([]string{"eval", "run", "fixture", "--output", output}); code != 0 {
		t.Fatalf("eval CLI returned %d", code)
	}
	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var report eval.Report
	if err := json.Unmarshal(encoded, &report); err != nil {
		t.Fatal(err)
	}
	if report.Metrics.CaseCount != 5 || report.Metrics.SuccessRate != 1 {
		t.Fatalf("CLI report = %#v", report)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(output), "report.md")); err != nil {
		t.Fatalf("markdown report was not generated: %v", err)
	}
}

func captureStdout(t *testing.T, call func() int) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = write
	code := call()
	_ = write.Close()
	os.Stdout = original
	if code != 0 {
		t.Fatalf("CLI returned %d", code)
	}
	encoded, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	return string(encoded)
}
