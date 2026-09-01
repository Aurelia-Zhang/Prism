package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Aurelia-Zhang/Prism/internal/observability"
	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/Aurelia-Zhang/Prism/internal/tool"
	"github.com/Aurelia-Zhang/Prism/internal/trace"
)

func TestRunPersistsClosedTraceThroughRuntimeConfig(t *testing.T) {
	store, err := observability.Open(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fake := &fakeProvider{responses: []provider.Response{textResponse("persisted")}}
	result, err := NewRunner(fake, tool.NewRegistry(), trace.NewRecorder(trace.Options{}), 1).Run(context.Background(), nil, WithRuntime(RuntimeConfig{TraceStore: store}))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), result.Trace.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TraceID != result.Trace.TraceID || len(loaded.Spans) != len(result.Trace.Spans) {
		t.Fatalf("persisted trace differs: result=%#v loaded=%#v", result.Trace, loaded)
	}
}
