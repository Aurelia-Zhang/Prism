package observability

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/Aurelia-Zhang/Prism/internal/trace"
)

func TestTraceSQLiteRoundTrip(t *testing.T) {
	store, err := Open("file:trace-roundtrip?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := fixtureSnapshot()
	if err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), snapshot.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, loaded) {
		t.Fatalf("round trip changed snapshot:\nwant=%#v\ngot=%#v", snapshot, loaded)
	}
	if _, err := store.Load(context.Background(), "missing"); !errors.Is(err, ErrTraceNotFound) {
		t.Fatalf("missing trace error = %v", err)
	}
}

func TestReplayPreservesSequenceAndParentDepth(t *testing.T) {
	timeline := Replay(fixtureSnapshot())
	if len(timeline.Spans) != 3 || timeline.Spans[0].Name != "agent.run" || timeline.Spans[1].Name != "model.call" || timeline.Spans[2].Name != "tool.call" {
		t.Fatalf("unexpected timeline order: %#v", timeline.Spans)
	}
	if timeline.Spans[0].Depth != 0 || timeline.Spans[1].Depth != 1 || timeline.Spans[2].Depth != 2 {
		t.Fatalf("unexpected timeline depth: %#v", timeline.Spans)
	}
	text := RenderTimeline(timeline)
	want := "trace root\nagent.run status=ok duration=2ms usage=input=4 output=2 cache_read=1 cache_write=0\n  model.call status=ok duration=1ms usage=input=4 output=2 cache_read=0 cache_write=0\n    tool.call status=error duration=1ms usage=input=0 output=0 cache_read=0 cache_write=0 error=bad_args\n"
	if text != want {
		t.Fatalf("timeline text differs:\n%s", text)
	}
}

func TestOTelMappingAndSensitiveAttributeFiltering(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	snapshot := fixtureSnapshot()
	snapshot.Spans[0].Attributes = trace.Attributes{}
	snapshot.Spans[0].Attributes["memory.query"] = "full prompt must not leave process"
	snapshot.Spans[0].Attributes["api_key"] = "secret"
	snapshot.Spans[0].Attributes["safe"] = "yes"
	if err := ExportSnapshot(context.Background(), snapshot, exporter); err != nil {
		t.Fatal(err)
	}
	spans := exporter.GetSpans().Snapshots()
	if len(spans) != 3 {
		t.Fatalf("exported spans = %#v", spans)
	}
	byName := make(map[string]sdktrace.ReadOnlySpan)
	for _, span := range spans {
		byName[span.Name()] = span
	}
	root, model, tool := byName["agent.run"], byName["model.call"], byName["tool.call"]
	if model.Parent().SpanID() != root.SpanContext().SpanID() || tool.Parent().SpanID() != model.SpanContext().SpanID() {
		t.Fatalf("exported parent chain is incorrect")
	}
	if tool.Status().Code != codes.Error {
		t.Fatalf("child status = %s", tool.Status().Code)
	}
	attributes := make(map[string]string)
	for _, value := range root.Attributes() {
		attributes[string(value.Key)] = fmt.Sprint(value.Value.AsInterface())
	}
	if attributes["prism.safe"] != "yes" || attributes["prism.memory.query"] != "" || attributes["prism.api_key"] != "" {
		t.Fatalf("sensitive or safe attributes were mapped incorrectly: %#v", attributes)
	}
	if attributes["prism.usage.input_tokens"] != "4" {
		t.Fatalf("usage attribute missing: %#v", attributes)
	}
	toolAttributes := make(map[string]string)
	for _, value := range tool.Attributes() {
		toolAttributes[string(value.Key)] = fmt.Sprint(value.Value.AsInterface())
	}
	if toolAttributes["prism.tool.name"] != "echo" || toolAttributes["prism.tool.arguments"] != "" || toolAttributes["prism.error.code"] != "bad_args" {
		t.Fatalf("tool mapping or sensitive filtering is incorrect: %#v", toolAttributes)
	}
}

func fixtureSnapshot() trace.Snapshot {
	return trace.Snapshot{
		TraceID: "root",
		Spans: []trace.SpanSnapshot{
			{ID: "root", Name: "agent.run", Status: trace.StatusOK, StartTime: time.Unix(100, 0).UTC(), EndTime: time.Unix(100, 2_000_000).UTC(), StartSequence: 1, EndSequence: 3, Usage: provider.Usage{InputTokens: 4, OutputTokens: 2, CacheReadTokens: 1}},
			{ID: "model", Name: "model.call", ParentSpanID: "root", Status: trace.StatusOK, StartTime: time.Unix(100, 500_000).UTC(), EndTime: time.Unix(100, 1_500_000).UTC(), StartSequence: 2, EndSequence: 1, Usage: provider.Usage{InputTokens: 4, OutputTokens: 2}},
			{ID: "tool", Name: "tool.call", ParentSpanID: "model", Status: trace.StatusError, StartTime: time.Unix(100, 1_000_000).UTC(), EndTime: time.Unix(100, 2_000_000).UTC(), StartSequence: 3, EndSequence: 2, Attributes: trace.Attributes{"tool.name": "echo", "tool.arguments": "full arguments"}, Error: &provider.Error{Code: "bad_args", Message: "invalid arguments", RetryAfter: time.Second}},
		},
	}
}
