// Package agent implements the provider-neutral Agent Loop.
package agent

import (
	"context"
	"encoding/json"
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
		modelSpan := run.Root().StartChild("model.call")
		response, err := r.provider.Complete(ctx, cloneMessages(conversation), definitions)
		totalUsage = addUsage(totalUsage, response.Usage)
		if err != nil {
			structured := provider.ErrorFrom(err, "provider_error")
			modelSpan.End(trace.StatusError, structured, response.Usage)
			return result, structured
		}
		modelSpan.End(trace.StatusOK, nil, response.Usage)
		conversation = append(conversation, provider.Message{
			Role:        provider.RoleAssistant,
			OutputItems: cloneOutputItems(response.Output),
		})

		calls := toolCalls(response.Output)
		if len(calls) == 0 {
			return Result{Messages: conversation}, nil
		}
		results, err := r.executeTools(ctx, modelSpan, calls)
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

func (r *Runner) executeTools(ctx context.Context, parent *trace.Span, calls []provider.ToolCall) ([]provider.ToolResult, error) {
	childContext, cancel := context.WithCancel(ctx)
	defer cancel()
	spans := make([]*trace.Span, len(calls))
	results := make([]provider.ToolResult, len(calls))
	for i := range calls {
		spans[i] = parent.StartChild("tool.call")
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

func toolCalls(output []provider.OutputItem) []provider.ToolCall {
	calls := make([]provider.ToolCall, 0)
	for _, item := range output {
		if item.Kind == provider.OutputToolCall && item.ToolCall != nil {
			call := *item.ToolCall
			call.Arguments = append(json.RawMessage(nil), call.Arguments...)
			calls = append(calls, call)
		}
	}
	return calls
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
