package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/Aurelia-Zhang/Prism/internal/trace"
)

func TestTaskLifecycleRetryAndSQLiteReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tasks.db")
	store, err := OpenTaskStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(context.Background(), TaskSpec{ID: "lifecycle", Strategy: "test", Goal: "exercise lifecycle", BaseRef: "main", MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != StatusQueued || task.Attempt != 1 {
		t.Fatalf("created task = %+v", task)
	}
	if err := store.Start(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Progress(context.Background(), task.ID, 42); err != nil {
		t.Fatal(err)
	}
	if err := store.Fail(context.Background(), task.ID, provider.NewError("fixture_failed", "first attempt"), map[string]string{"attempt": "1"}, provider.Usage{InputTokens: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Retry(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Start(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(context.Background(), task.ID, map[string]string{"ok": "yes"}, provider.Usage{OutputTokens: 8}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenTaskStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reopened, err := store.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != StatusSucceeded || reopened.Attempt != 2 || reopened.Progress != 100 || reopened.Usage.OutputTokens != 8 {
		t.Fatalf("reopened task = %+v", reopened)
	}
	attempts, err := store.Attempts(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].Status != StatusFailed || attempts[1].Status != StatusSucceeded {
		t.Fatalf("attempt history = %+v", attempts)
	}
	if err := store.Progress(context.Background(), task.ID, 1); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("progress after terminal status error = %v", err)
	}
}

func TestTaskCancellation(t *testing.T) {
	store, err := OpenTaskStore(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	task, err := store.Create(context.Background(), TaskSpec{Goal: "cancel", BaseRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(context.Background(), task.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(context.Background(), task.ID); got.Status != StatusCancelled {
		t.Fatalf("status = %s", got.Status)
	}
	if err := store.Start(context.Background(), task.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("start cancelled task error = %v", err)
	}
}

func TestWorktreeIsolationAndManagerOwnedCleanup(t *testing.T) {
	repo := newGitRepo(t)
	manager, err := NewWorktreeManager(repo, filepath.Join(t.TempDir(), "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := manager.Create(context.Background(), "task-one", 1, "main")
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch == "" || info.HEAD == "" || info.Path == repo {
		t.Fatalf("worktree info = %+v", info)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "worker.txt"), []byte("isolated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err = manager.Inspect(context.Background(), info.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.ChangedFiles) != 1 || info.ChangedFiles[0] != "worker.txt" || info.DiffSummary == "" {
		t.Fatalf("changed files = %+v", info.ChangedFiles)
	}
	if err := manager.Cleanup(context.Background(), filepath.Join(t.TempDir(), "not-owned")); err == nil {
		t.Fatal("cleanup unexpectedly accepted an unowned path")
	}
	if err := manager.Cleanup(context.Background(), info.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(info.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists, stat error = %v", err)
	}
}

func TestCommandWorkerJSONRoundTripAndCancellation(t *testing.T) {
	worker := CommandWorker{Command: []string{os.Args[0], "-test.run=TestWorkerFixtureProcess"}, Env: []string{"PRISM_WORKER_FIXTURE=success"}}
	result, err := worker.Run(context.Background(), WorkerRequest{TaskID: "fixture-task", Goal: "write a file", WorktreePath: t.TempDir(), ModelProfile: "fast", Budget: 20, Strategy: "test", Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSucceeded || result.Usage.InputTokens != 11 || result.Summary == "" {
		t.Fatalf("worker result = %+v", result)
	}

	cancelCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	blocking := CommandWorker{Command: []string{os.Args[0], "-test.run=TestWorkerFixtureProcess"}, Env: []string{"PRISM_WORKER_FIXTURE=block"}}
	_, err = blocking.Run(cancelCtx, WorkerRequest{TaskID: "blocking", Goal: "wait", WorktreePath: t.TempDir()})
	var structured *provider.Error
	if !errors.As(err, &structured) || (structured.Code != "worker_canceled" && structured.Code != "context_deadline_exceeded") {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestManagerCancellationPersistsCancelledTask(t *testing.T) {
	repo := newGitRepo(t)
	dir := t.TempDir()
	store, err := OpenTaskStore(filepath.Join(dir, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	worktrees, err := NewWorktreeManager(repo, filepath.Join(dir, "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ManagerConfig{
		Store: store, Worktrees: worktrees,
		Worker: CommandWorker{Command: []string{os.Args[0], "-test.run=TestWorkerFixtureProcess"}, Env: []string{"PRISM_WORKER_FIXTURE=block"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := manager.Run(ctx, TaskSpec{Goal: "cancel worker", BaseRef: "main"})
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("manager cancellation error = %v", err)
	}
	if result.Task.Status != StatusCancelled {
		t.Fatalf("cancelled task = %+v", result.Task)
	}
}

func TestRuleRouterReasons(t *testing.T) {
	router := RuleRouter{}
	checks := []struct {
		request RouteRequest
		name    string
		reason  string
	}{
		{RouteRequest{Complexity: 1, TokenBudget: 100}, "fast", "token budget"},
		{RouteRequest{Complexity: 5, ToolRequired: true}, "balanced", "balanced"},
		{RouteRequest{Complexity: 9}, "strong", "high"},
		{RouteRequest{Complexity: 1, RetryHistory: 1}, "strong", "retry history"},
	}
	for _, check := range checks {
		profile, reason := router.Route(check.request)
		if profile.Name != check.name || !strings.Contains(reason, check.reason) {
			t.Errorf("route(%+v) = %s, %q", check.request, profile.Name, reason)
		}
	}
}

func TestFanOutRetryArbitrationAndTrace(t *testing.T) {
	repo := newGitRepo(t)
	dir := t.TempDir()
	store, err := OpenTaskStore(filepath.Join(dir, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	worktrees, err := NewWorktreeManager(repo, filepath.Join(dir, "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	worker := CommandWorker{Command: []string{os.Args[0], "-test.run=TestWorkerFixtureProcess"}, Env: []string{"PRISM_WORKER_FIXTURE=fanout"}}
	recorder := trace.NewRecorder(trace.Options{})
	manager, err := NewManager(ManagerConfig{Store: store, Worktrees: worktrees, Worker: worker, Recorder: recorder})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.RunFanOut(context.Background(), TaskSpec{ID: "parent-demo", Goal: "implement two alternatives", Constraints: []string{"local only"}, BaseRef: "main", Complexity: 5, ToolRequired: true, TokenBudget: 4096, MaxRetries: 1}, []string{"stable", "flaky"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Parent.Status != StatusSucceeded || len(result.Children) != 2 || result.Decision.WinnerID == "" {
		t.Fatalf("fan-out result = %+v", result)
	}
	var flaky Task
	for _, child := range result.Children {
		if child.Strategy == "flaky" {
			flaky = child
		}
		if child.Branch == "" || child.HEAD == "" || len(child.ChangedFiles) != 1 {
			t.Fatalf("child worktree evidence = %+v", child)
		}
	}
	if flaky.Attempt != 2 || flaky.Status != StatusSucceeded {
		t.Fatalf("flaky child after retry = %+v", flaky)
	}
	attempts, err := store.Attempts(context.Background(), flaky.ID)
	if err != nil || len(attempts) != 2 || attempts[0].Status != StatusFailed || attempts[1].Status != StatusSucceeded {
		t.Fatalf("flaky attempts = %+v, err=%v", attempts, err)
	}
	names := make(map[string]bool)
	for _, span := range result.Trace.Spans {
		names[span.Name] = true
	}
	for _, name := range []string{"model.route", "task.spawn", "task.wait", "task.retry", "task.arbitrate"} {
		if !names[name] {
			t.Fatalf("trace missing %q: %+v", name, names)
		}
	}
	for _, span := range result.Trace.Spans {
		if span.Name == "model.route" || span.Name == "task.spawn" || span.Name == "task.wait" || span.Name == "task.retry" || span.Name == "task.arbitrate" {
			for _, key := range []string{"task.id", "attempt", "strategy", "profile", "route.reason", "status", "usage"} {
				if span.Attributes[key] == "" {
					t.Fatalf("span %s missing attribute %q: %+v", span.Name, key, span.Attributes)
				}
			}
		}
	}
	if result.Decision.Scores[0].Score < 100 || !strings.Contains(result.Decision.Reason, "deterministic") {
		t.Fatalf("decision = %+v", result.Decision)
	}
	t.Logf("parent=%s status=%s winner=%s decision=%s", result.Parent.ID, result.Parent.Status, result.Decision.WinnerID, result.Decision.Reason)
	for _, child := range result.Children {
		attempts, _ := store.Attempts(context.Background(), child.ID)
		for _, attempt := range attempts {
			t.Logf("strategy=%s task=%s attempt=%d status=%s duration=%s branch=%s head=%s files=%v usage=%+v route=%s", child.Strategy, child.ID, attempt.Number, attempt.Status, duration(attempt.StartedAt, attempt.CompletedAt), attempt.Branch, attempt.HEAD, attempt.ChangedFiles, attempt.Usage, attempt.RouteReason)
		}
	}
}

func TestWorkerFixtureProcess(t *testing.T) {
	mode := os.Getenv("PRISM_WORKER_FIXTURE")
	if mode == "" {
		return
	}
	if mode == "block" {
		time.Sleep(10 * time.Second)
		return
	}
	var request WorkerRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(2)
	}
	if request.WorktreePath != "" {
		_ = os.WriteFile(filepath.Join(request.WorktreePath, request.Strategy+".txt"), []byte(request.Strategy+"\n"), 0o644)
	}
	status := StatusSucceeded
	usage := provider.Usage{InputTokens: 11, OutputTokens: 7}
	if mode == "fanout" && request.Strategy == "flaky" && request.Attempt == 1 {
		status = StatusFailed
		usage = provider.Usage{InputTokens: 5, OutputTokens: 3}
	}
	result := WorkerResult{Status: status, Summary: request.Strategy + " fixture result", Validation: []string{"fixture command completed"}, Usage: usage}
	if status == StatusFailed {
		result.Error = provider.NewError("fixture_failed", "intentional first-attempt failure")
	}
	encoded, _ := json.Marshal(result)
	_, _ = os.Stdout.Write(encoded)
	os.Exit(0)
}

func newGitRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repo, "init", "-b", "main")
	gitTestCommand(t, repo, "config", "user.email", "test@example.com")
	gitTestCommand(t, repo, "config", "user.name", "Prism Test")
	if err := os.WriteFile(filepath.Join(repo, "README.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repo, "add", "README.txt")
	gitTestCommand(t, repo, "commit", "-m", "base")
	return repo
}

func gitTestCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func duration(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return "n/a"
	}
	return end.Sub(start).String()
}
