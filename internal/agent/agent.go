// Package agent implements the provider-neutral Agent Loop.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	contextx "github.com/Aurelia-Zhang/Prism/internal/context"
	"github.com/Aurelia-Zhang/Prism/internal/memory"
	"github.com/Aurelia-Zhang/Prism/internal/output"
	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/Aurelia-Zhang/Prism/internal/tool"
	"github.com/Aurelia-Zhang/Prism/internal/trace"
)

// Runner calls a Provider, executes same-round tools concurrently, and feeds
// their ordered results back into the next provider call.
type Runner struct {
	provider  provider.Provider
	tools     *tool.Registry
	recorder  *trace.Recorder
	maxRounds int
}

type runOptions struct {
	eventSink provider.EventSink
	runtime   RuntimeConfig
}

// RuntimeConfig enables the optional C1 context and persistence path. A zero
// value preserves the T1/T2 Agent Loop behavior.
type RuntimeConfig struct {
	Compactor            *contextx.Compactor
	Memory               *memory.Store
	MemoryScopes         []memory.ScopeRef
	MemoryMode           memory.SearchMode
	MemoryTopK           int
	SessionID            string
	ProjectID            string
	LongTermID           string
	OutputStore          *output.Store
	LargeOutputThreshold int
	TraceStore           TraceStore
}

// TraceStore is the persistence seam used after a run has closed its root span.
// observability.Store implements it without coupling the Agent Loop to SQLite.
type TraceStore interface {
	Save(context.Context, trace.Snapshot) error
}

// RunOption configures one Agent Loop run without breaking the T1 call shape.
type RunOption func(*runOptions)

// WithEventSink receives normalized events when the Provider supports streaming.
func WithEventSink(sink provider.EventSink) RunOption {
	return func(options *runOptions) { options.eventSink = sink }
}

// WithRuntime enables optional context compaction, memory recall, and output
// persistence for one run.
func WithRuntime(config RuntimeConfig) RunOption {
	return func(options *runOptions) { options.runtime = config }
}

// NewRunner creates a runner. maxRounds is the maximum number of Provider calls.
func NewRunner(provider provider.Provider, tools *tool.Registry, recorder *trace.Recorder, maxRounds int) *Runner {
	return &Runner{provider: provider, tools: tools, recorder: recorder, maxRounds: maxRounds}
}

// Result contains the conversation and the complete trace for a run.
type Result struct {
	Messages   []provider.Message  `json:"messages"`
	StopReason provider.StopReason `json:"stop_reason,omitempty"`
	Trace      trace.Snapshot      `json:"trace"`
}

// Run executes the loop until normal completion, cancellation, a fatal provider
// error, or maxRounds. maxRounds counts Provider calls, not tool rounds.
func (r *Runner) Run(ctx context.Context, messages []provider.Message, options ...RunOption) (result Result, runErr error) {
	if r == nil || r.recorder == nil {
		return result, provider.NewError("invalid_runner", "provider, tools, and recorder are required")
	}
	run := r.recorder.StartRun()
	conversation := cloneMessages(messages)
	runOptions := runOptions{}
	for _, option := range options {
		if option != nil {
			option(&runOptions)
		}
	}
	runtime := runOptions.runtime
	var totalUsage provider.Usage
	defer func() {
		if runErr == nil {
			run.Root().End(trace.StatusOK, nil, totalUsage)
		} else {
			run.Root().End(trace.StatusError, provider.ErrorFrom(runErr, "agent_error"), totalUsage)
		}
		if result.Messages == nil {
			result.Messages = cloneMessages(conversation)
		}
		result.Trace = run.Snapshot()
		if runtime.TraceStore != nil {
			if err := runtime.TraceStore.Save(context.Background(), result.Trace); err != nil && runErr == nil {
				runErr = provider.ErrorFrom(err, "trace_persist_error")
			}
		}
	}()

	if r.provider == nil || r.tools == nil {
		return result, provider.NewError("invalid_runner", "provider, tools, and recorder are required")
	}
	if r.maxRounds <= 0 {
		return result, provider.NewError("invalid_max_rounds", "max rounds must be greater than zero")
	}
	if runtime.OutputStore != nil && !r.tools.Has("fetch_output") {
		if err := r.tools.Register(runtime.OutputStore.Tool()); err != nil {
			return result, provider.ErrorFrom(err, "output_tool_registration_error")
		}
	}
	definitions := r.tools.Definitions()
	var err error
	conversation, err = r.recallMemory(ctx, run.Root(), conversation, runtime)
	if err != nil {
		return result, err
	}

	for round := 0; round < r.maxRounds; round++ {
		conversation, err = r.compactContext(ctx, run.Root(), conversation, runtime)
		if err != nil {
			return result, err
		}
		modelSpan := run.Root().StartChildWithAttributes("model.call", trace.Attributes{
			"agent.round": strconv.Itoa(round + 1),
		})
		if err := ctx.Err(); err != nil {
			structured := provider.ErrorFrom(err, "context_canceled")
			modelSpan.End(trace.StatusError, structured, provider.Usage{})
			return result, structured
		}
		response, err := r.complete(ctx, cloneMessages(conversation), definitions, modelSpan, runOptions.eventSink)
		totalUsage = addUsage(totalUsage, response.Usage)
		if cancellation := ctx.Err(); cancellation != nil {
			structured := provider.ErrorFrom(cancellation, "context_canceled")
			modelSpan.End(trace.StatusError, structured, response.Usage)
			return result, structured
		}
		if err != nil {
			structured := provider.ErrorFrom(err, "provider_error")
			modelSpan.End(trace.StatusError, structured, response.Usage)
			return result, structured
		}
		calls, responseErr := validateResponse(response)
		if responseErr != nil {
			modelSpan.End(trace.StatusError, responseErr, response.Usage)
			return result, responseErr
		}
		modelSpan.End(trace.StatusOK, nil, response.Usage)
		conversation = append(conversation, provider.Message{
			Role:        provider.RoleAssistant,
			OutputItems: cloneOutputItems(response.Output),
		})

		if len(calls) == 0 {
			return Result{Messages: conversation, StopReason: response.StopReason}, nil
		}
		results, err := r.executeTools(ctx, run.Root(), calls, round+1, runtime)
		if err != nil {
			return result, provider.ErrorFrom(err, "tool_execution_error")
		}
		conversation = append(conversation, provider.Message{
			Role:        provider.RoleTool,
			ToolResults: results,
		})
		if round+1 == r.maxRounds {
			return result, provider.NewError("max_rounds_exceeded", "maximum provider call count reached")
		}
	}
	return result, provider.NewError("max_rounds_exceeded", "maximum provider call count reached")
}

func (r *Runner) executeTools(ctx context.Context, parent *trace.Span, calls []provider.ToolCall, round int, runtime RuntimeConfig) ([]provider.ToolResult, error) {
	childContext, cancel := context.WithCancel(ctx)
	defer cancel()
	spans := make([]*trace.Span, len(calls))
	results := make([]provider.ToolResult, len(calls))
	for i := range calls {
		spans[i] = parent.StartChildWithAttributes("tool.call", trace.Attributes{
			"tool.name":    calls[i].Name,
			"tool.call_id": calls[i].ID,
			"agent.round":  strconv.Itoa(round),
		})
	}

	var wait sync.WaitGroup
	var errorMu sync.Mutex
	var executionErr error
	for i, call := range calls {
		wait.Add(1)
		go func(index int, call provider.ToolCall) {
			defer wait.Done()
			result, err := r.tools.Call(childContext, call)
			if err != nil {
				spans[index].End(trace.StatusError, provider.ErrorFrom(err, "tool_execution_error"), provider.Usage{})
				errorMu.Lock()
				if executionErr == nil {
					executionErr = err
				}
				errorMu.Unlock()
				cancel()
				return
			}
			if runtime.OutputStore != nil && runtime.LargeOutputThreshold > 0 && len(result.Content) > runtime.LargeOutputThreshold {
				originalSize := len(result.Content)
				persistSpan := spans[index].StartChildWithAttributes("output.persist", trace.Attributes{"output.original_bytes": strconv.Itoa(originalSize)})
				var record output.Record
				result, record, err = persistLargeOutput(ctx, runtime.OutputStore, result)
				if err != nil {
					persistSpan.End(trace.StatusError, provider.ErrorFrom(err, "output_persist_error"), provider.Usage{})
					spans[index].End(trace.StatusError, provider.ErrorFrom(err, "output_persist_error"), provider.Usage{})
					errorMu.Lock()
					if executionErr == nil {
						executionErr = err
					}
					errorMu.Unlock()
					cancel()
					return
				}
				persistSpan.SetAttribute("output.id", record.ID)
				persistSpan.SetAttribute("output.persisted_bytes", strconv.Itoa(len(result.Content)))
				persistSpan.End(trace.StatusOK, nil, provider.Usage{})
			}
			results[index] = result
			if result.Error != nil {
				spans[index].End(trace.StatusError, result.Error, provider.Usage{})
			} else {
				spans[index].End(trace.StatusOK, nil, provider.Usage{})
			}
		}(i, call)
	}
	wait.Wait()
	errorMu.Lock()
	defer errorMu.Unlock()
	if executionErr != nil {
		return nil, executionErr
	}
	return results, nil
}

func (r *Runner) recallMemory(ctx context.Context, parent *trace.Span, messages []provider.Message, runtime RuntimeConfig) ([]provider.Message, error) {
	if runtime.Memory == nil {
		return messages, nil
	}
	query := lastUserText(messages)
	if strings.TrimSpace(query) == "" {
		return messages, nil
	}
	scopes := runtime.MemoryScopes
	if len(scopes) == 0 {
		if runtime.SessionID != "" {
			scopes = append(scopes, memory.ScopeRef{Scope: memory.ScopeSession, ScopeID: runtime.SessionID})
		}
		if runtime.ProjectID != "" {
			scopes = append(scopes, memory.ScopeRef{Scope: memory.ScopeProject, ScopeID: runtime.ProjectID})
		}
		longTermID := runtime.LongTermID
		if longTermID == "" {
			longTermID = "global"
		}
		scopes = append(scopes, memory.ScopeRef{Scope: memory.ScopeLongTerm, ScopeID: longTermID})
	}
	topK := runtime.MemoryTopK
	if topK <= 0 {
		topK = 3
	}
	mode := runtime.MemoryMode
	if mode == "" {
		mode = memory.SearchHybrid
	}
	span := parent.StartChildWithAttributes("memory.recall", trace.Attributes{"memory.query": query, "memory.top_k": strconv.Itoa(topK)})
	var recalled []string
	for _, scope := range scopes {
		results, err := runtime.Memory.Recall(ctx, scope, query, topK, mode)
		if err != nil {
			span.End(trace.StatusError, provider.ErrorFrom(err, "memory_recall_error"), provider.Usage{})
			return nil, provider.ErrorFrom(err, "memory_recall_error")
		}
		span.SetAttribute("memory."+string(scope.Scope)+".count", strconv.Itoa(len(results)))
		for _, result := range results {
			recalled = append(recalled, fmtMemory(result))
		}
	}
	span.End(trace.StatusOK, nil, provider.Usage{})
	if len(recalled) == 0 {
		return messages, nil
	}
	return prependSystem(messages, "memory_recall:\n"+strings.Join(recalled, "\n")), nil
}

func (r *Runner) compactContext(ctx context.Context, parent *trace.Span, messages []provider.Message, runtime RuntimeConfig) ([]provider.Message, error) {
	if runtime.Compactor == nil {
		return messages, nil
	}
	compacted, report, err := runtime.Compactor.Compact(ctx, messages)
	if err != nil {
		return nil, provider.ErrorFrom(err, "context_compact_error")
	}
	if !report.Compacted {
		return compacted, nil
	}
	span := parent.StartChildWithAttributes("context.compact", trace.Attributes{
		"context.before_tokens":    strconv.Itoa(report.BeforeTokens),
		"context.after_tokens":     strconv.Itoa(report.AfterTokens),
		"context.before_messages":  strconv.Itoa(report.BeforeMessages),
		"context.after_messages":   strconv.Itoa(report.AfterMessages),
		"context.dropped_messages": strconv.Itoa(report.DroppedMessages),
	})
	span.End(trace.StatusOK, nil, provider.Usage{})
	return compacted, nil
}

func lastUserText(messages []provider.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == provider.RoleUser && messages[i].Text != "" {
			return messages[i].Text
		}
	}
	return ""
}

func prependSystem(messages []provider.Message, text string) []provider.Message {
	result := make([]provider.Message, 0, len(messages)+1)
	result = append(result, provider.Message{Role: provider.RoleSystem, Text: text})
	return append(result, messages...)
}

func fmtMemory(result memory.Result) string {
	return fmt.Sprintf("[%s/%s score=%.4f] %s", result.Scope, result.ScopeID, result.Score, result.Content)
}

func persistLargeOutput(ctx context.Context, store *output.Store, result provider.ToolResult) (provider.ToolResult, output.Record, error) {
	record, err := store.Persist(ctx, result.Content)
	if err != nil {
		return provider.ToolResult{}, output.Record{}, err
	}
	excerpt := result.Content
	if len(excerpt) > 240 {
		excerpt = excerpt[:240]
	}
	response, err := json.Marshal(struct {
		OutputID      string `json:"output_id"`
		OriginalBytes int    `json:"original_bytes"`
		Summary       string `json:"summary"`
	}{record.ID, len(result.Content), excerpt})
	if err != nil {
		return provider.ToolResult{}, output.Record{}, err
	}
	result.Content = string(response)
	return result, record, nil
}

func (r *Runner) complete(ctx context.Context, messages []provider.Message, definitions []provider.ToolDefinition, modelSpan *trace.Span, sink provider.EventSink) (provider.Response, error) {
	streaming, ok := r.provider.(provider.StreamingProvider)
	if !ok {
		return r.provider.Complete(ctx, messages, definitions)
	}
	traceSink := func(event provider.StreamEvent) error {
		if event.Kind == provider.EventAttempt {
			modelSpan.SetAttribute("provider", event.Attempt.Provider)
			modelSpan.SetAttribute("model", event.Attempt.Model)
			modelSpan.SetAttribute("attempt", strconv.Itoa(event.Attempt.Number))
			if event.Attempt.Error != nil {
				modelSpan.SetAttribute("error.code", event.Attempt.Error.Code)
				modelSpan.SetAttribute("retryable", strconv.FormatBool(event.Attempt.Error.Retryable))
			} else {
				modelSpan.DeleteAttribute("error.code")
				modelSpan.DeleteAttribute("retryable")
			}
		}
		if sink != nil {
			return sink(event)
		}
		return nil
	}
	return streaming.Stream(ctx, messages, definitions, traceSink)
}

func validateResponse(response provider.Response) ([]provider.ToolCall, *provider.Error) {
	calls := make([]provider.ToolCall, 0)
	toolItemCount := 0
	for _, item := range response.Output {
		if item.Kind != provider.OutputToolCall {
			continue
		}
		toolItemCount++
		if item.ToolCall == nil || item.ToolCall.ID == "" || item.ToolCall.Name == "" {
			return nil, provider.NewError("invalid_provider_output", "tool_call output item is missing id or name")
		}
		call := *item.ToolCall
		call.Arguments = append(json.RawMessage(nil), call.Arguments...)
		calls = append(calls, call)
	}

	switch response.StopReason {
	case provider.StopReasonEndTurn:
		if toolItemCount > 0 {
			return nil, provider.NewError("invalid_provider_output", "end_turn response contains a tool call")
		}
		return calls, nil
	case provider.StopReasonMaxTokens:
		if toolItemCount > 0 {
			return nil, provider.NewError("invalid_provider_output", "max_tokens response contains a tool call")
		}
		return calls, nil
	case provider.StopReasonToolCall:
		if len(calls) == 0 || len(calls) != toolItemCount {
			return nil, provider.NewError("invalid_provider_output", "tool_call stop reason requires a valid tool call")
		}
		return calls, nil
	case provider.StopReasonError:
		return nil, provider.NewError("provider_error_response", "provider returned error stop reason")
	case provider.StopReasonCanceled:
		return nil, provider.NewError("provider_canceled", "provider returned canceled stop reason")
	default:
		return nil, provider.NewError("invalid_stop_reason", "provider returned unknown stop reason")
	}
}

func cloneMessages(messages []provider.Message) []provider.Message {
	cloned := make([]provider.Message, len(messages))
	for i, message := range messages {
		cloned[i] = message
		cloned[i].OutputItems = cloneOutputItems(message.OutputItems)
		cloned[i].ToolResults = cloneToolResults(message.ToolResults)
	}
	return cloned
}

func cloneToolResults(results []provider.ToolResult) []provider.ToolResult {
	cloned := make([]provider.ToolResult, len(results))
	for i, result := range results {
		cloned[i] = result
		if result.Error != nil {
			errorCopy := *result.Error
			cloned[i].Error = &errorCopy
		}
	}
	return cloned
}

func cloneOutputItems(items []provider.OutputItem) []provider.OutputItem {
	cloned := make([]provider.OutputItem, len(items))
	for i, item := range items {
		cloned[i] = item
		if item.ToolCall != nil {
			call := *item.ToolCall
			call.Arguments = append(json.RawMessage(nil), call.Arguments...)
			cloned[i].ToolCall = &call
		}
		if item.Opaque != nil {
			opaque := *item.Opaque
			opaque.Raw = append(json.RawMessage(nil), opaque.Raw...)
			cloned[i].Opaque = &opaque
		}
	}
	return cloned
}

func addUsage(left, right provider.Usage) provider.Usage {
	return provider.Usage{
		InputTokens:      left.InputTokens + right.InputTokens,
		OutputTokens:     left.OutputTokens + right.OutputTokens,
		CacheReadTokens:  left.CacheReadTokens + right.CacheReadTokens,
		CacheWriteTokens: left.CacheWriteTokens + right.CacheWriteTokens,
	}
}
