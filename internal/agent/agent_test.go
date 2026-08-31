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

type streamingFakeProvider struct {
	response provider.Response
}

func (p *streamingFakeProvider) Complete(context.Context, []provider.Message, []provider.ToolDefinition) (provider.Response, error) {
	return p.response, nil
}

func (p *streamingFakeProvider) Stream(_ context.Context, _ []provider.Message, _ []provider.ToolDefinition, sink provider.EventSink) (provider.Response, error) {
	if err := sink(provider.StreamEvent{Kind: provider.EventAttempt, Attempt: provider.Attempt{Provider: "fixture", Model: "fixture-model", Number: 1}}); err != nil {
		return provider.Response{}, err
	}
	if err := sink(provider.StreamEvent{Kind: provider.EventTextDelta, Text: "hello"}); err != nil {
		return provider.Response{}, err
	}
	return p.response, nil
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

func TestRunUsesOptionalStreamingProviderAndEventSink(t *testing.T) {
	fake := &streamingFakeProvider{response: textResponse("hello")}
	var events []provider.StreamEvent
	result, err := newRunner(t, fake, tool.NewRegistry(), 1).Run(context.Background(), nil, WithEventSink(func(event provider.StreamEvent) error {
		events = append(events, event)
		return nil
	}))
	if err != nil || len(events) != 2 || events[0].Kind != provider.EventAttempt || events[1].Kind != provider.EventTextDelta {
		t.Fatalf("streaming path did not preserve events: events=%#v err=%v", events, err)
	}
	model := result.Trace.Spans[1]
	if model.Attributes["provider"] != "fixture" || model.Attributes["model"] != "fixture-model" || model.Attributes["attempt"] != "1" {
		t.Fatalf("streaming attempt was not traced: %#v", model)
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
	if result.Trace.Spans[1].ParentSpanID != result.Trace.Spans[0].ID || result.Trace.Spans[2].ParentSpanID != result.Trace.Spans[0].ID || result.Trace.Spans[2].Status != trace.StatusOK {
		t.Fatalf("tool span parent/status incorrect: %#v", result.Trace)
	}
	if result.Trace.Spans[2].Attributes["tool.name"] != "echo" || result.Trace.Spans[2].Attributes["tool.call_id"] != "c1" || result.Trace.Spans[2].Attributes["agent.round"] != "1" {
		t.Fatalf("tool span attributes are incomplete: %#v", result.Trace.Spans[2].Attributes)
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
		return "corrected result", nil
	}}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{responses: []provider.Response{
		callResponse(provider.ToolCall{ID: "bad", Name: "strict", Arguments: json.RawMessage(`{"value":7}`)}),
		callResponse(provider.ToolCall{ID: "good", Name: "strict", Arguments: json.RawMessage(`{"value":"fixed"}`)}),
		textResponse("corrected"),
	}}
	result, err := newRunner(t, fake, registry, 3).Run(context.Background(), nil)
	if err != nil || invoked != 1 || len(fake.calls) != 3 {
		t.Fatalf("validation should be corrected and invoked: result=%#v err=%v invoked=%d calls=%d", result, err, invoked, len(fake.calls))
	}
	feedback := fake.callMessages(1)[1].ToolResults[0]
	if feedback.Error == nil || feedback.Error.Code != "tool_arguments_schema_invalid" {
		t.Fatalf("missing validation feedback: %#v", feedback)
	}
	correctedInput := fake.callMessages(2)
	if len(correctedInput) != 4 || correctedInput[3].ToolResults[0].Content != "corrected result" {
		t.Fatalf("corrected tool result was not fed back: %#v", correctedInput)
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
	fake := &fakeProvider{responses: []provider.Response{callResponse(provider.ToolCall{ID: "again-1", Name: "again", Arguments: json.RawMessage(`{}`)}), callResponse(provider.ToolCall{ID: "again-2", Name: "again", Arguments: json.RawMessage(`{}`)})}}
	result, err = newRunner(t, fake, registry, 2).Run(context.Background(), nil)
	if err == nil || err.(*provider.Error).Code != "max_rounds_exceeded" {
		t.Fatalf("max rounds boundary was not enforced: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("max rounds made %d provider calls", len(fake.calls))
	}
	assertTraceFinished(t, result.Trace)
}

func TestRunChecksStopReasonAndOutputConsistency(t *testing.T) {
	validCall := provider.ToolCall{ID: "valid", Name: "echo", Arguments: json.RawMessage(`{}`)}
	tests := []struct {
		name        string
		response    provider.Response
		wantCode    string
		wantSuccess bool
	}{
		{name: "end turn", response: textResponse("done"), wantSuccess: true},
		{name: "valid tool call", response: callResponse(validCall), wantSuccess: true},
		{name: "end turn with tool", response: provider.Response{Output: []provider.OutputItem{{Kind: provider.OutputToolCall, ToolCall: &validCall}}, StopReason: provider.StopReasonEndTurn}, wantCode: "invalid_provider_output"},
		{name: "tool call without output", response: provider.Response{StopReason: provider.StopReasonToolCall}, wantCode: "invalid_provider_output"},
		{name: "tool call with nil item", response: provider.Response{Output: []provider.OutputItem{{Kind: provider.OutputToolCall}}, StopReason: provider.StopReasonToolCall}, wantCode: "invalid_provider_output"},
		{name: "error", response: provider.Response{StopReason: provider.StopReasonError}, wantCode: "provider_error_response"},
		{name: "canceled", response: provider.Response{StopReason: provider.StopReasonCanceled}, wantCode: "provider_canceled"},
		{name: "unknown", response: provider.Response{StopReason: provider.StopReason("future_reason")}, wantCode: "invalid_stop_reason"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			registry := tool.NewRegistry()
			if err := registry.Register(tool.Tool{Name: "echo", Schema: json.RawMessage(`{}`), Handler: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}); err != nil {
				t.Fatal(err)
			}
			responses := []provider.Response{testCase.response}
			if testCase.wantSuccess {
				responses = append(responses, textResponse("done"))
			}
			fake := &fakeProvider{responses: responses}
			_, err := newRunner(t, fake, registry, 2).Run(context.Background(), nil)
			if testCase.wantSuccess {
				if err != nil {
					t.Fatalf("valid response failed: %v", err)
				}
				return
			}
			structured, ok := err.(*provider.Error)
			if !ok || structured.Code != testCase.wantCode {
				t.Fatalf("got error=%v, want code %q", err, testCase.wantCode)
			}
		})
	}
}

func TestRunDoesNotCallProviderWhenAlreadyCanceledAndChecksAfterCall(t *testing.T) {
	preCanceled := &fakeProvider{responses: []provider.Response{textResponse("must not run")}}
	preContext, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := newRunner(t, preCanceled, tool.NewRegistry(), 1).Run(preContext, nil)
	if err == nil || !errors.Is(err, context.Canceled) || len(preCanceled.calls) != 0 {
		t.Fatalf("pre-canceled context called provider or returned success: err=%v calls=%d", err, len(preCanceled.calls))
	}
	if result.Trace.Spans[1].Status != trace.StatusError || result.Trace.Spans[0].Status != trace.StatusError {
		t.Fatalf("pre-canceled spans were not ended as errors: %#v", result.Trace)
	}

	postContext, postCancel := context.WithCancel(context.Background())
	postCanceled := &cancelingProvider{cancel: postCancel}
	result, err = newRunner(t, postCanceled, tool.NewRegistry(), 1).Run(postContext, nil)
	if err == nil || !errors.Is(err, context.Canceled) || postCanceled.calls != 1 {
		t.Fatalf("post-call cancellation was ignored: err=%v calls=%d", err, postCanceled.calls)
	}
	if result.Trace.Spans[1].Status != trace.StatusError || result.Trace.Spans[0].Status != trace.StatusError {
		t.Fatalf("post-canceled spans were not ended as errors: %#v", result.Trace)
	}
}

type blockingProvider struct{ started chan struct{} }

func (p *blockingProvider) Complete(ctx context.Context, _ []provider.Message, _ []provider.ToolDefinition) (provider.Response, error) {
	close(p.started)
	<-ctx.Done()
	return provider.Response{}, ctx.Err()
}

type cancelingProvider struct {
	calls  int
	cancel context.CancelFunc
}

func (p *cancelingProvider) Complete(ctx context.Context, _ []provider.Message, _ []provider.ToolDefinition) (provider.Response, error) {
	p.calls++
	p.cancel()
	return textResponse("must be rejected"), nil
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
		{Output: []provider.OutputItem{{Kind: provider.OutputToolCall, ToolCall: &provider.ToolCall{ID: "corrected-call", Name: "strict", Arguments: json.RawMessage(`{"value":"fixed"}`)}}}, Usage: provider.Usage{InputTokens: 7, OutputTokens: 2}, StopReason: provider.StopReasonToolCall},
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
		result, err = NewRunner(fake, registry, recorder, 4).Run(context.Background(), nil)
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
	if result.Messages[1].ToolResults[0].Error == nil || result.Messages[3].ToolResults[0].Content != "strict result" || result.Messages[5].ToolResults[0].ToolCallID != "alpha-call" {
		t.Fatalf("artifact path did not preserve validation feedback and tool order: %#v", result.Messages)
	}
	root := result.Trace.Spans[0]
	if root.Usage.InputTokens != 37 || root.Usage.OutputTokens != 11 || root.Usage.CacheReadTokens != 1 || root.Usage.CacheWriteTokens != 1 {
		t.Fatalf("artifact path did not aggregate usage: %#v", root.Usage)
	}
}
