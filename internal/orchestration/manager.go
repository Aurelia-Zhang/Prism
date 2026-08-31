package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/Aurelia-Zhang/Prism/internal/trace"
)

type Manager struct {
	store     *TaskStore
	worktrees *WorktreeManager
	worker    Worker
	router    Router
	arbiter   Arbiter
	recorder  *trace.Recorder
}

type ManagerConfig struct {
	Store     *TaskStore
	Worktrees *WorktreeManager
	Worker    Worker
	Router    Router
	Arbiter   Arbiter
	Recorder  *trace.Recorder
}

type RunResult struct {
	Task  Task           `json:"task"`
	Trace trace.Snapshot `json:"trace"`
}

type FanOutResult struct {
	Parent   Task           `json:"parent"`
	Children []Task         `json:"children"`
	Decision Decision       `json:"decision"`
	Trace    trace.Snapshot `json:"trace"`
}

func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Store == nil || config.Worktrees == nil || config.Worker == nil {
		return nil, errors.New("store, worktrees, and worker are required")
	}
	if config.Router == nil {
		config.Router = RuleRouter{}
	}
	if config.Arbiter == nil {
		config.Arbiter = DeterministicArbiter{}
	}
	if config.Recorder == nil {
		config.Recorder = trace.NewRecorder(trace.Options{})
	}
	return &Manager{store: config.Store, worktrees: config.Worktrees, worker: config.Worker, router: config.Router, arbiter: config.Arbiter, recorder: config.Recorder}, nil
}

func (m *Manager) Run(ctx context.Context, spec TaskSpec) (result RunResult, runErr error) {
	run := m.recorder.StartRun()
	task, err := m.store.Create(ctx, spec)
	if err != nil {
		run.Root().End(trace.StatusError, provider.ErrorFrom(err, "task_create_error"), provider.Usage{})
		return RunResult{Trace: run.Snapshot()}, err
	}
	defer func() {
		status := trace.StatusOK
		if runErr != nil {
			status = trace.StatusError
		}
		run.Root().End(status, provider.ErrorFrom(runErr, "orchestration_error"), provider.Usage{})
		result.Trace = run.Snapshot()
		_ = m.store.SetTrace(context.Background(), task.ID, run.Snapshot())
	}()
	if runErr = m.executeTask(ctx, run, task.ID); runErr != nil {
		result = RunResult{Task: m.mustGet(task.ID)}
		return result, runErr
	}
	task, runErr = m.store.Get(context.Background(), task.ID)
	result = RunResult{Task: task}
	return result, runErr
}

// RunFanOut starts two or more children concurrently, each in its own worktree.
// A failed child consumes its configured retry budget before arbitration.
func (m *Manager) RunFanOut(ctx context.Context, spec TaskSpec, strategies []string) (result FanOutResult, runErr error) {
	if len(strategies) < 2 {
		return result, errors.New("fan-out requires at least two strategies")
	}
	run := m.recorder.StartRun()
	parentSpec := spec
	parentSpec.Strategy = "fan-out"
	parentSpec.MaxRetries = 0
	parent, err := m.store.Create(ctx, parentSpec)
	if err != nil {
		run.Root().End(trace.StatusError, provider.ErrorFrom(err, "task_create_error"), provider.Usage{})
		return result, err
	}
	defer func() {
		status := trace.StatusOK
		if runErr != nil {
			status = trace.StatusError
		}
		run.Root().End(status, provider.ErrorFrom(runErr, "orchestration_error"), provider.Usage{})
		result.Trace = run.Snapshot()
		_ = m.store.SetTrace(context.Background(), parent.ID, result.Trace)
	}()
	if err := m.store.Start(ctx, parent.ID); err != nil {
		return result, err
	}

	children := make([]Task, len(strategies))
	for i, strategy := range strategies {
		childSpec := spec
		childSpec.ID = ""
		childSpec.ParentID = parent.ID
		childSpec.Strategy = strategy
		child, err := m.store.Create(ctx, childSpec)
		if err != nil {
			runErr = err
			return result, err
		}
		children[i] = child
	}

	var wait sync.WaitGroup
	errorsCh := make(chan error, len(children))
	for _, child := range children {
		wait.Add(1)
		go func(taskID string) {
			defer wait.Done()
			if err := m.executeTask(ctx, run, taskID); err != nil {
				errorsCh <- err
			}
		}(child.ID)
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if ctx.Err() != nil {
			runErr = ctx.Err()
			break
		}
		if runErr == nil {
			runErr = err
		}
	}
	if ctx.Err() != nil {
		runErr = ctx.Err()
		_ = m.store.Cancel(context.Background(), parent.ID, provider.ErrorFrom(ctx.Err(), "context_canceled"))
		return result, runErr
	}

	var candidates []Candidate
	for i := range children {
		children[i] = m.mustGet(children[i].ID)
		candidates = append(candidates, Candidate{TaskID: children[i].ID, Strategy: children[i].Strategy, Status: children[i].Status, Summary: resultSummary(children[i]), ChangedFiles: children[i].ChangedFiles, Usage: children[i].Usage})
	}
	decision, err := m.arbitrate(ctx, run.Root(), parent.ID, candidates)
	if err != nil {
		_ = m.store.Fail(context.Background(), parent.ID, provider.ErrorFrom(err, "arbitration_error"), nil, provider.Usage{})
		return result, err
	}
	winner := findTask(children, decision.WinnerID)
	if winner.Status == StatusSucceeded {
		if err := m.store.Complete(context.Background(), parent.ID, decision, provider.Usage{}); err != nil {
			return result, err
		}
		runErr = nil
	} else {
		runErr = provider.NewError("no_successful_child", "arbitration found no successful child")
		_ = m.store.Fail(context.Background(), parent.ID, provider.ErrorFrom(runErr, "no_successful_child"), decision, provider.Usage{})
	}
	result.Parent = m.mustGet(parent.ID)
	result.Children = children
	result.Decision = decision
	return result, runErr
}

func (m *Manager) executeTask(ctx context.Context, run *trace.Run, taskID string) error {
	for {
		task, err := m.store.Get(context.Background(), taskID)
		if err != nil {
			return err
		}
		profile, reason := m.router.Route(RouteRequest{Complexity: task.Complexity, ToolRequired: task.ToolRequired, TokenBudget: task.TokenBudget, RetryHistory: task.Attempt - 1})
		routeSpan := run.Root().StartChildWithAttributes("model.route", baseAttributes(task, profile.Name))
		routeSpan.SetAttribute("route.reason", reason)
		routeSpan.SetAttribute("retry.history", strconv.Itoa(task.Attempt-1))
		if err := m.store.SetRoute(context.Background(), task.ID, profile.Name, reason); err != nil {
			routeSpan.End(trace.StatusError, provider.ErrorFrom(err, "task_route_persist_error"), provider.Usage{})
			return err
		}
		routeSpan.SetAttribute("status", string(StatusSucceeded))
		routeSpan.End(trace.StatusOK, nil, provider.Usage{})
		if err := m.store.Start(context.Background(), task.ID); err != nil {
			return err
		}

		spawnSpan := run.Root().StartChildWithAttributes("task.spawn", baseAttributes(task, profile.Name))
		info, err := m.worktrees.Create(ctx, task.ID, task.Attempt, task.BaseRef)
		if err != nil {
			structured := provider.ErrorFrom(err, "worktree_create_error")
			spawnSpan.SetAttribute("error.code", structured.Code)
			spawnSpan.End(trace.StatusError, structured, provider.Usage{})
			return m.failOrCancel(ctx, run, task, nil, structured)
		}
		spawnSpan.SetAttribute("worktree", info.Path)
		spawnSpan.SetAttribute("branch", info.Branch)
		spawnSpan.SetAttribute("route.reason", reason)
		spawnSpan.SetAttribute("status", string(StatusSucceeded))
		spawnSpan.End(trace.StatusOK, nil, provider.Usage{})
		if err := m.store.SetWorktree(context.Background(), task.ID, info); err != nil {
			return err
		}

		waitSpan := run.Root().StartChildWithAttributes("task.wait", baseAttributes(task, profile.Name))
		started := time.Now()
		workerResult, workerErr := m.worker.Run(ctx, WorkerRequest{TaskID: task.ID, Goal: task.Goal, Constraints: task.Constraints, WorktreePath: info.Path, ModelProfile: profile.Name, Budget: task.TokenBudget, Strategy: task.Strategy, Attempt: task.Attempt})
		waitSpan.SetAttribute("duration_ms", strconv.FormatInt(time.Since(started).Milliseconds(), 10))
		waitSpan.SetAttribute("usage.input_tokens", strconv.FormatInt(workerResult.Usage.InputTokens, 10))
		waitSpan.SetAttribute("usage.output_tokens", strconv.FormatInt(workerResult.Usage.OutputTokens, 10))
		waitSpan.SetAttribute("usage", fmt.Sprintf("input=%d output=%d", workerResult.Usage.InputTokens, workerResult.Usage.OutputTokens))
		waitSpan.SetAttribute("route.reason", reason)
		operationCtx := ctx
		if operationCtx.Err() != nil {
			operationCtx = context.Background()
		}
		finalInfo, inspectErr := m.worktrees.Inspect(operationCtx, info.Path)
		if inspectErr == nil {
			info = finalInfo
			_ = m.store.SetWorktree(context.Background(), task.ID, info)
		}
		if workerErr != nil {
			structured := provider.ErrorFrom(workerErr, "worker_error")
			waitSpan.SetAttribute("error.code", structured.Code)
			waitSpan.SetAttribute("status", string(StatusFailed))
			waitSpan.End(trace.StatusError, structured, workerResult.Usage)
			if err := m.failOrCancel(ctx, run, task, &workerResult, structured); err != nil {
				return err
			}
		} else {
			workerResult.ChangedFiles = info.ChangedFiles
			if inspectErr != nil {
				workerResult.Status = StatusFailed
				workerResult.Error = provider.ErrorFrom(inspectErr, "worktree_inspect_error")
			}
			if workerResult.Status == StatusSucceeded {
				waitSpan.SetAttribute("status", string(StatusSucceeded))
				waitSpan.End(trace.StatusOK, nil, workerResult.Usage)
				if err := m.store.Complete(context.Background(), task.ID, workerResult, workerResult.Usage); err != nil {
					return err
				}
			} else if workerResult.Status == StatusCancelled {
				structured := workerResult.Error
				if structured == nil {
					structured = provider.NewError("worker_canceled", "worker canceled")
				}
				waitSpan.SetAttribute("error.code", structured.Code)
				waitSpan.End(trace.StatusError, structured, workerResult.Usage)
				if err := m.store.Cancel(context.Background(), task.ID, structured); err != nil {
					return err
				}
			} else {
				structured := workerResult.Error
				if structured == nil {
					structured = provider.NewError("worker_failed", "worker returned failed status")
				}
				waitSpan.SetAttribute("error.code", structured.Code)
				waitSpan.End(trace.StatusError, structured, workerResult.Usage)
				if err := m.store.Fail(context.Background(), task.ID, structured, workerResult, workerResult.Usage); err != nil {
					return err
				}
			}
		}
		_ = m.store.SetTrace(context.Background(), task.ID, run.Snapshot())

		current := m.mustGet(task.ID)
		if current.Status == StatusFailed && current.Attempt <= current.MaxRetries {
			retrySpan := run.Root().StartChildWithAttributes("task.retry", baseAttributes(current, profile.Name))
			retrySpan.SetAttribute("retry.from_attempt", strconv.Itoa(current.Attempt))
			retrySpan.SetAttribute("route.reason", current.RouteReason)
			next, retryErr := m.store.Retry(context.Background(), current.ID)
			if retryErr != nil {
				retrySpan.SetAttribute("error.code", "retry_error")
				retrySpan.End(trace.StatusError, provider.ErrorFrom(retryErr, "retry_error"), provider.Usage{})
				return retryErr
			}
			retrySpan.SetAttribute("retry.to_attempt", strconv.Itoa(next))
			retrySpan.SetAttribute("status", string(StatusSucceeded))
			retrySpan.End(trace.StatusOK, nil, provider.Usage{})
			_ = m.store.SetTrace(context.Background(), task.ID, run.Snapshot())
			continue
		}
		if current.Status == StatusCancelled {
			return context.Canceled
		}
		if current.Status == StatusFailed {
			return current.Error
		}
		return nil
	}
}

func (m *Manager) failOrCancel(ctx context.Context, run *trace.Run, task Task, result *WorkerResult, taskErr *provider.Error) error {
	if ctx.Err() != nil || taskErr.Code == "context_canceled" || taskErr.Code == "worker_canceled" {
		if err := m.store.Cancel(context.Background(), task.ID, taskErr); err != nil {
			return err
		}
		_ = m.store.SetTrace(context.Background(), task.ID, run.Snapshot())
		return context.Canceled
	}
	if err := m.store.Fail(context.Background(), task.ID, taskErr, result, usageOf(result)); err != nil {
		return err
	}
	return nil
}

func (m *Manager) arbitrate(ctx context.Context, parent *trace.Span, parentID string, candidates []Candidate) (Decision, error) {
	span := parent.StartChildWithAttributes("task.arbitrate", trace.Attributes{
		"task.id":      parentID,
		"attempt":      "1",
		"strategy":     "fan-out",
		"profile":      "arbiter",
		"route.reason": "deterministic local arbitration",
		"status":       "running",
		"usage":        "input=0 output=0",
	})
	decision, err := m.arbiter.Arbitrate(ctx, parentID, candidates)
	if err != nil {
		span.SetAttribute("error.code", "arbitration_error")
		span.End(trace.StatusError, provider.ErrorFrom(err, "arbitration_error"), provider.Usage{})
		return Decision{}, err
	}
	span.SetAttribute("winner.id", decision.WinnerID)
	span.SetAttribute("reason", decision.Reason)
	span.SetAttribute("status", string(StatusSucceeded))
	span.End(trace.StatusOK, nil, provider.Usage{})
	return decision, nil
}

func baseAttributes(task Task, profile string) trace.Attributes {
	return trace.Attributes{"task.id": task.ID, "attempt": strconv.Itoa(task.Attempt), "strategy": task.Strategy, "profile": profile, "status": "running", "usage": "input=0 output=0"}
}

func (m *Manager) mustGet(id string) Task {
	task, _ := m.store.Get(context.Background(), id)
	return task
}

func findTask(tasks []Task, id string) Task {
	for _, task := range tasks {
		if task.ID == id {
			return task
		}
	}
	return Task{}
}

func resultSummary(task Task) string {
	if len(task.Result) == 0 {
		return ""
	}
	var result WorkerResult
	if json.Unmarshal(task.Result, &result) == nil {
		return result.Summary
	}
	return string(task.Result)
}

func usageOf(result *WorkerResult) provider.Usage {
	if result == nil {
		return provider.Usage{}
	}
	return result.Usage
}
