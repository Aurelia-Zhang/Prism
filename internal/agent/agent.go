// Package agent implements the provider-neutral Agent Loop.
package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"

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

// NewRunner creates a runner. maxRounds is the maximum number of Provider calls.
func NewRunner(provider provider.Provider, tools *tool.Registry, recorder *trace.Recorder, maxRounds int) *Runner {
	return &Runner{provider: provider, tools: tools, recorder: recorder, maxRounds: maxRounds}
}

// Result contains the conversation and the complete trace for a run.
type Result struct {
	Messages []provider.Message `json:"messages"`
	Trace    trace.Snapshot     `json:"trace"`
}

// Run executes the loop until normal completion, cancellation, a fatal provider
// error, or maxRounds. maxRounds counts Provider calls, not tool rounds.
func (r *Runner) Run(ctx context.Context, messages []provider.Message) (result Result, runErr error) {
	if r == nil || r.recorder == nil {
		return result, provider.NewError("invalid_runner", "provider, tools, and recorder are required")
	}
	run := r.recorder.StartRun()
	conversation := cloneMessages(messages)
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
	}()

	if r.provider == nil || r.tools == nil {
		return result, provider.NewError("invalid_runner", "provider, tools, and recorder are required")
	}
	if r.maxRounds <= 0 {
		return result, provider.NewError("invalid_max_rounds", "max rounds must be greater than zero")
	}
	definitions := r.tools.Definitions()

	for round := 0; round < r.maxRounds; round++ {
		modelSpan := run.Root().StartChildWithAttributes("model.call", trace.Attributes{
			"agent.round": strconv.Itoa(round + 1),
		})
		if err := ctx.Err(); err != nil {
			structured := provider.ErrorFrom(err, "context_canceled")
			modelSpan.End(trace.StatusError, structured, provider.Usage{})
			return result, structured
		}
		response, err := r.provider.Complete(ctx, cloneMessages(conversation), definitions)
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
			return Result{Messages: conversation}, nil
		}
		results, err := r.executeTools(ctx, run.Root(), calls, round+1)
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

func (r *Runner) executeTools(ctx context.Context, parent *trace.Span, calls []provider.ToolCall, round int) ([]provider.ToolResult, error) {
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
