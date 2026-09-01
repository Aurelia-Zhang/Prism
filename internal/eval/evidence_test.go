package eval

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/Aurelia-Zhang/Prism/internal/observability"
)

var updateEvidence = flag.Bool("update-evidence", false, "regenerate the checked-in E1 evidence artifacts")

func TestE1EvidenceArtifacts(t *testing.T) {
	suite := FixtureSuite()
	caseResult, snapshot, err := ExecuteCase(context.Background(), suite.Cases[0])
	if err != nil {
		t.Fatalf("evidence fixture did not pass: result=%#v err=%v", caseResult, err)
	}
	store, err := observability.Open(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), snapshot.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	timeline := observability.Replay(loaded)
	exporter := tracetest.NewInMemoryExporter()
	if err := observability.ExportSnapshot(context.Background(), loaded, exporter); err != nil {
		t.Fatal(err)
	}
	otelSummary := observability.SummarizeSpans(exporter.GetSpans().Snapshots())
	report, err := RunSuite(context.Background(), suite)
	if err != nil {
		t.Fatal(err)
	}
	traceJSON, _ := json.MarshalIndent(snapshot, "", "  ")
	replayText := observability.RenderTimeline(timeline)
	otelJSON, _ := json.MarshalIndent(otelSummary, "", "  ")
	reportJSON, _ := json.MarshalIndent(report, "", "  ")
	artifacts := map[string][]byte{
		"trace.json":       append(traceJSON, '\n'),
		"replay.txt":       []byte(replayText),
		"otel.json":        append(otelJSON, '\n'),
		"eval-report.json": append(reportJSON, '\n'),
		"eval-report.md":   []byte(Markdown(report)),
	}
	_, currentFile, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(currentFile), "..", "..", "docs", "evidence", "e1")
	if *updateEvidence {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range artifacts {
			if err := os.WriteFile(filepath.Join(directory, name), content, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, want := range artifacts {
		got, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("evidence artifact %s differs; run go test ./internal/eval -run TestE1EvidenceArtifacts -update-evidence", name)
		}
	}
}
