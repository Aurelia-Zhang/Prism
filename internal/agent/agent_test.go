package agent

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/Aurelia-Zhang/Prism/internal/tool"
	"github.com/Aurelia-Zhang/Prism/internal/trace"
)

type fakeProvider struct {
	mu        sync.Mutex
	responses []provider.Response
	errors    []error
	calls     [][]provider.Message
}

func (p *fakeProvider) Complete(_ context.Context, messages []provider.Message, _ []provider.ToolDefinition) (provider.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, cloneMessages(messages))
	index := len(p.calls) - 1
	var response provider.Response
	if index < len(p.responses) {
		response = p.responses[index]
	}
	if index < len(p.errors) && p.errors[index] != nil {
		return response, p.errors[index]
	}
	return response, nil
}

func (p *fakeProvider) callMessages(index int) []provider.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneMessages(p.calls[index])
}

func textResponse(text string) provider.Response {
	return provider.Response{Output: []provider.OutputItem{{Kind: provider.OutputText, Text: text}}, StopReason: provider.StopReasonEndTurn}
}

func callResponse(calls ...provider.ToolCall) provider.Response {
	output := make([]provider.OutputItem, 0, len(calls))
	for i := range calls {
		call := calls[i]
		output = append(output, provider.OutputItem{Kind: provider.OutputToolCall, ToolCall: &call})
	}
	return provider.Response{Output: output, StopReason: provider.StopReasonToolCall}
}

func newRunner(t *testing.T, fake provider.Provider, registry *tool.Registry, maxRounds int) *Runner {
	t.Helper()
	return NewRunner(fake, registry, trace.NewRecorder(trace.Options{}), maxRounds)
}

func TestRunStopsNormallyWithoutTools(t *testing.T) {
	fake := &fakeProvider{responses: []provider.Response{textResponse("hello")}}
	result, err := newRunner(t, fake, tool.NewRegistry(), 1).Run(context.Background(), []provider.Message{{Role: provider.RoleUser, Text: "hi"}})
	if err != nil || len(result.Messages) != 2 || result.Messages[1].OutputItems[0].Text != "hello" {
		t.Fatalf("normal run failed: result=%#v err=%v", result, err)
	}
	assertTraceFinished(t, result.Trace)
	if result.Trace.Spans[0].Status != trace.StatusOK || result.Trace.Spans[1].Name != "model.call" {
		t.Fatalf("unexpected trace: %#v", result.Trace)
	}
}

func TestRunFeedsSingleToolResultBack(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Tool{Name: "echo", Schema: json.RawMessage(`{"type":"object","required":["value"]}`), Handler: func(_ context.Context, args json.RawMessage) (string, error) { return string(args), nil }}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{responses: []provider.Response{
		callResponse(provider.ToolCall{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"value":"ok"}`)}),
		textResponse("done"),
	}}
	result, err := newRunner(t, fake, registry, 2).Run(context.Background(), []provider.Message{{Role: provider.RoleUser, Text: "run"}})
	if err != nil {
		t.Fatal(err)
	}
	secondCall := fake.callMessages(1)
	if len(secondCall) != 3 || secondCall[2].Role != provider.RoleTool || len(secondCall[2].ToolResults) != 1 || secondCall[2].ToolResults[0].Content != `{"value":"ok"}` {
		t.Fatalf("tool result was not fed back: %#v", secondCall)
	}
	if result.Trace.Spans[2].ParentSpanID != result.Trace.Spans[1].ID || result.Trace.Spans[2].Status != trace.StatusOK {
		t.Fatalf("tool span parent/status incorrect: %#v", result.Trace)
	}
	assertTraceFinished(t, result.Trace)
}

func TestRunExecutesSameRoundToolsConcurrentlyAndKeepsOrder(t *testing.T) {
	registry := tool.NewRegistry()
	started := make(chan string, 2)
	finished := make(chan string, 2)
	release := map[string]chan struct{}{"a": make(chan struct{}), "b": make(chan struct{})}
	for _, name := range []string{"a", "b"} {
		name := name
		if err := registry.Register(tool.Tool{Name: name, Schema: json.RawMessage(`{}`), Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
			started <- name
			<-release[name]
			finished <- name
			return name + " result", nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeProvider{responses: []provider.Response{callResponse(
		provider.ToolCall{ID: "a", Name: "a", Arguments: json.RawMessage(`{}`)},
		provider.ToolCall{ID: "b", Name: "b", Arguments: json.RawMessage(`{}`)},
	), textResponse("done")}}
	runDone := make(chan struct{})
	var result Result
	var runErr error
	go func() {
		result, runErr = newRunner(t, fake, registry, 2).Run(context.Background(), nil)
		close(runDone)
	}()
	seen := map[string]bool{<-started: true, <-started: true}
	if len(seen) != 2 {
		t.Fatal("same-round handlers did not both start before either was released")
	}
	close(release["b"])
	if got := <-finished; got != "b" {
		t.Fatalf("expected b to finish first, got %s", got)
	}
	close(release["a"])
	if got := <-finished; got != "a" {
		t.Fatalf("expected a to finish second, got %s", got)
	}
	<-runDone
	if runErr != nil {
		t.Fatal(runErr)
	}
	toolResults := fake.callMessages(1)[1].ToolResults
	if len(toolResults) != 2 || toolResults[0].ToolCallID != "a" || toolResults[1].ToolCallID != "b" {
		t.Fatalf("results were not returned in call order: %#v", toolResults)
	}
	if toolResults[0].Content != "a result" || toolResults[1].Content != "b result" {
		t.Fatalf("unexpected ordered results: %#v", toolResults)
	}
	assertTraceFinished(t, result.Trace)
}

func TestRunFeedsValidationFailureAndHandlerFailureBack(t *testing.T) {
	registry := tool.NewRegistry()
	var invoked int
	if err := registry.Register(tool.Tool{Name: "strict", Schema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`), Handler: func(context.Context, json.RawMessage) (string, error) {
		invoked++
		return "should not run", nil
	}}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{responses: []provider.Response{
		callResponse(provider.ToolCall{ID: "bad", Name: "strict", Arguments: json.RawMessage(`{"value":7}`)}),
		textResponse("corrected"),
	}}
	result, err := newRunner(t, fake, registry, 2).Run(context.Background(), nil)
	if err != nil || invoked != 0 {
		t.Fatalf("validation should be feedback, not invocation: result=%#v err=%v invoked=%d", result, err, invoked)
	}
	feedback := fake.callMessages(1)[1].ToolResults[0]
	if feedback.Error == nil || feedback.Error.Code != "tool_arguments_schema_invalid" {
		t.Fatalf("missing validation feedback: %#v", feedback)
	}

	failing := tool.NewRegistry()
	if err := failing.Register(tool.Tool{Name: "fails", Schema: json.RawMessage(`{}`), Handler: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("downstream") }}); err != nil {
		t.Fatal(err)
	}
	failingProvider := &fakeProvider{responses: []provider.Response{callResponse(provider.ToolCall{ID: "f", Name: "fails", Arguments: json.RawMessage(`{}`)}), textResponse("recovered")}}
	if _, err := newRunner(t, failingProvider, failing, 2).Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := failingProvider.callMessages(1)[1].ToolResults[0].Error.Code; got != "tool_handler_error" {
		t.Fatalf("missing handler failure feedback: %s", got)
	}
}

func TestRunProviderFatalError(t *testing.T) {
	fake := &fakeProvider{errors: []error{provider.NewError("provider_fatal", "model unavailable")}}
	result, err := newRunner(t, fake, tool.NewRegistry(), 2).Run(context.Background(), nil)
	structured, ok := err.(*provider.Error)
	if !ok || structured.Code != "provider_fatal" {
		t.Fatalf("unexpected provider error: %v", err)
	}
	if result.Trace.Spans[1].Status != trace.StatusError || result.Trace.Spans[0].Status != trace.StatusError {
		t.Fatalf("fatal error did not close spans: %#v", result.Trace)
	}
	assertTraceFinished(t, result.Trace)
}

func TestRunCancellationAndMaxRounds(t *testing.T) {
	cancelled := &blockingProvider{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-cancelled.started
		cancel()
	}()
	result, err := newRunner(t, cancelled, tool.NewRegistry(), 2).Run(ctx, nil)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was not returned: %v", err)
	}
	assertTraceFinished(t, result.Trace)

	registry := tool.NewRegistry()
	if err := registry.Register(tool.Tool{Name: "again", Schema: json.RawMessage(`{}`), Handler: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{responses: []provider.Response{callResponse(provider.ToolCall{Name: "again", Arguments: json.RawMessage(`{}`)}), callResponse(provider.ToolCall{Name: "again", Arguments: json.RawMessage(`{}`)})}}
	result, err = newRunner(t, fake, registry, 2).Run(context.Background(), nil)
	if err == nil || err.(*provider.Error).Code != "max_rounds_exceeded" {
		t.Fatalf("max rounds boundary was not enforced: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("max rounds made %d provider calls", len(fake.calls))
	}
	assertTraceFinished(t, result.Trace)
}

type blockingProvider struct{ started chan struct{} }

func (p *blockingProvider) Complete(ctx context.Context, _ []provider.Message, _ []provider.ToolDefinition) (provider.Response, error) {
	close(p.started)
	<-ctx.Done()
	return provider.Response{}, ctx.Err()
}

func assertTraceFinished(t *testing.T, snapshot trace.Snapshot) {
	t.Helper()
	for _, span := range snapshot.Spans {
		if span.Status == trace.StatusRunning || span.EndSequence == 0 {
			t.Fatalf("trace contains a running span: %#v", snapshot)
		}
	}
}

var updateTraceArtifact = flag.Bool("update-trace-artifact", false, "regenerate the checked-in T1 trace artifact")

type traceArtifact struct {
	Trace    trace.Snapshot     `json:"trace"`
	Messages []provider.Message `json:"messages"`
}

func TestTraceArtifact(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Tool{Name: "strict", Schema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required": ["value"]}`), Handler: func(context.Context, json.RawMessage) (string, error) { return "strict result", nil }}); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 2)
	finished := make(chan string, 2)
	release := map[string]chan struct{}{"alpha": make(chan struct{}), "beta": make(chan struct{})}
	for _, name := range []string{"alpha", "beta"} {
		name := name
		if err := registry.Register(tool.Tool{Name: name, Schema: json.RawMessage(`{"type":"object"}`), Handler: func(context.Context, json.RawMessage) (string, error) {
			started <- name
			<-release[name]
			finished <- name
			return name + " result", nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeProvider{responses: []provider.Response{
		{Output: []provider.OutputItem{{Kind: provider.OutputToolCall, ToolCall: &provider.ToolCall{ID: "invalid", Name: "strict", Arguments: json.RawMessage(`{"value":9}`)}}}, Usage: provider.Usage{InputTokens: 10, OutputTokens: 2, CacheReadTokens: 1, CacheWriteTokens: 1}, StopReason: provider.StopReasonToolCall},
		{Output: []provider.OutputItem{{Kind: provider.OutputToolCall, ToolCall: &provider.ToolCall{ID: "alpha-call", Name: "alpha", Arguments: json.RawMessage(`{}`)}}, {Kind: provider.OutputToolCall, ToolCall: &provider.ToolCall{ID: "beta-call", Name: "beta", Arguments: json.RawMessage(`{}`)}}}, Usage: provider.Usage{InputTokens: 12, OutputTokens: 4}, StopReason: provider.StopReasonToolCall},
		{Output: []provider.OutputItem{{Kind: provider.OutputText, Text: "complete"}}, Usage: provider.Usage{InputTokens: 8, OutputTokens: 3}, StopReason: provider.StopReasonEndTurn},
	}}
	nextID := 0
	recorder := trace.NewRecorder(trace.Options{
		Clock:       func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
		IDGenerator: func() string { nextID++; return "t1-span-" + string(rune('0'+nextID)) },
	})
	resultDone := make(chan struct{})
	var result Result
	var err error
	go func() {
		result, err = NewRunner(fake, registry, recorder, 3).Run(context.Background(), nil)
		close(resultDone)
	}()
	<-started
	<-started
	close(release["beta"])
	if got := <-finished; got != "beta" {
		t.Fatalf("expected beta to finish first, got %s", got)
	}
	close(release["alpha"])
	if got := <-finished; got != "alpha" {
		t.Fatalf("expected alpha to finish second, got %s", got)
	}
	<-resultDone
	if err != nil {
		t.Fatal(err)
	}
	artifact := traceArtifact{Trace: result.Trace, Messages: result.Messages}
	encoded, marshalErr := json.MarshalIndent(artifact, "", "  ")
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	encoded = append(encoded, '\n')
	_, currentFile, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "docs", "evidence", "t1", "trace.json")
	if *updateTraceArtifact {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(want) != string(encoded) {
		t.Fatalf("trace artifact differs; run go test ./internal/agent -run TestTraceArtifact -update-trace-artifact")
	}
	if result.Messages[1].ToolResults[0].Error == nil || result.Messages[3].ToolResults[0].ToolCallID != "alpha-call" {
		t.Fatalf("artifact path did not preserve validation feedback and tool order: %#v", result.Messages)
	}
	root := result.Trace.Spans[0]
	if root.Usage.InputTokens != 30 || root.Usage.OutputTokens != 9 || root.Usage.CacheReadTokens != 1 || root.Usage.CacheWriteTokens != 1 {
		t.Fatalf("artifact path did not aggregate usage: %#v", root.Usage)
	}
}
