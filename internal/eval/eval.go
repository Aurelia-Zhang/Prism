// Package eval runs small, versioned scenario suites against real Prism paths.
package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aurelia-Zhang/Prism/internal/agent"
	contextx "github.com/Aurelia-Zhang/Prism/internal/context"
	"github.com/Aurelia-Zhang/Prism/internal/orchestration"
	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/Aurelia-Zhang/Prism/internal/tool"
	"github.com/Aurelia-Zhang/Prism/internal/trace"
)

const suiteVersion = 1

// Suite is the deliberately small, versioned eval input format.
type Suite struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Fixture bool   `json:"fixture,omitempty"`
	Cases   []Case `json:"cases"`
}

// Case contains rule assertions for one deterministic scenario.
type Case struct {
	ID             string   `json:"id"`
	Input          string   `json:"input"`
	Timeout        string   `json:"timeout"`
	ExpectedStatus string   `json:"expected_status"`
	RequiredSpans  []string `json:"required_spans,omitempty"`
	ExpectedText   string   `json:"expected_text,omitempty"`
	ExpectedError  string   `json:"expected_error,omitempty"`
}

// CaseResult records facts from a real Runner or Manager result and its Trace.
type CaseResult struct {
	ID           string         `json:"id"`
	Status       string         `json:"status"`
	Passed       bool           `json:"passed"`
	Steps        int            `json:"steps"`
	Usage        provider.Usage `json:"usage"`
	LatencyMS    int64          `json:"latency_ms"`
	TraceID      string         `json:"trace_id"`
	SpanCount    int            `json:"span_count"`
	ObservedText string         `json:"observed_text,omitempty"`
	ErrorCode    string         `json:"error_code,omitempty"`
	FailedChecks []string       `json:"failed_checks,omitempty"`
	SpanNames    []string       `json:"-"`
}

// Metrics is computed only from CaseResult values.
type Metrics struct {
	CaseCount        int     `json:"case_count"`
	SuccessRate      float64 `json:"success_rate"`
	AverageSteps     float64 `json:"average_steps"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	P50LatencyMS     float64 `json:"p50_latency_ms"`
}

// Report is the JSON fact source for both machine and Markdown reports.
type Report struct {
	SuiteID string       `json:"suite_id"`
	Version int          `json:"version"`
	Fixture bool         `json:"fixture"`
	Cases   []CaseResult `json:"cases"`
	Metrics Metrics      `json:"metrics"`
}

// FixtureSuite returns the five E1 local scenarios.
func FixtureSuite() Suite {
	return Suite{
		Version: suiteVersion,
		ID:      "e1-fixture-v1",
		Fixture: true,
		Cases: []Case{
			{ID: "text-completion", Input: "say hello", Timeout: "1s", ExpectedStatus: "succeeded", ExpectedText: "hello from fixture"},
			{ID: "single-tool", Input: "call echo", Timeout: "1s", ExpectedStatus: "succeeded", RequiredSpans: []string{"tool.call"}, ExpectedText: "tool complete"},
			{ID: "validation-self-correction", Input: "use strict tool", Timeout: "1s", ExpectedStatus: "succeeded", RequiredSpans: []string{"tool.call"}, ExpectedText: "corrected"},
			{ID: "context-compact", Input: "retain the latest task", Timeout: "1s", ExpectedStatus: "succeeded", RequiredSpans: []string{"context.compact"}, ExpectedText: "compacted"},
			{ID: "failure-retry-routing", Input: "run the local worker", Timeout: "2s", ExpectedStatus: "succeeded", RequiredSpans: []string{"model.route", "task.retry"}},
		},
	}
}

// LoadSuite accepts a JSON suite or the explicit built-in `fixture` selector.
func LoadSuite(path string) (Suite, error) {
	if path == "fixture" || path == "builtin:fixture" {
		return FixtureSuite(), nil
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, err
	}
	var suite Suite
	if err := json.Unmarshal(encoded, &suite); err != nil {
		return Suite{}, fmt.Errorf("decode eval suite: %w", err)
	}
	if err := validateSuite(suite); err != nil {
		return Suite{}, err
	}
	return suite, nil
}

func validateSuite(suite Suite) error {
	if suite.Version != suiteVersion {
		return fmt.Errorf("unsupported eval suite version %d", suite.Version)
	}
	if strings.TrimSpace(suite.ID) == "" || len(suite.Cases) == 0 {
		return errors.New("eval suite ID and cases are required")
	}
	seen := make(map[string]bool, len(suite.Cases))
	for _, item := range suite.Cases {
		if item.ID == "" || seen[item.ID] {
			return fmt.Errorf("eval case ID must be unique and non-empty: %q", item.ID)
		}
		seen[item.ID] = true
		if item.ExpectedStatus == "" {
			return fmt.Errorf("eval case %q expected status is required", item.ID)
		}
		if item.Timeout != "" {
			if _, err := time.ParseDuration(item.Timeout); err != nil {
				return fmt.Errorf("eval case %q timeout: %w", item.ID, err)
			}
		}
	}
	return nil
}

// RunSuite executes each case and computes metrics from its actual result.
func RunSuite(ctx context.Context, suite Suite) (Report, error) {
	if err := validateSuite(suite); err != nil {
		return Report{}, err
	}
	report := Report{SuiteID: suite.ID, Version: suite.Version, Fixture: suite.Fixture, Cases: make([]CaseResult, 0, len(suite.Cases))}
	for _, item := range suite.Cases {
		caseCtx := ctx
		timeout := time.Second
		if item.Timeout != "" {
			parsed, _ := time.ParseDuration(item.Timeout)
			timeout = parsed
		}
		var cancel context.CancelFunc
		caseCtx, cancel = context.WithTimeout(caseCtx, timeout)
		result, err := runCase(caseCtx, item)
		cancel()
		result = assertCase(item, result, err)
		report.Cases = append(report.Cases, result)
	}
	report.Metrics = CalculateMetrics(report.Cases)
	return report, nil
}

// CalculateMetrics computes the deliberately small E1 metric set. P95 and
// significance tests are intentionally absent because this suite has five cases.
func CalculateMetrics(cases []CaseResult) Metrics {
	metrics := Metrics{CaseCount: len(cases)}
	if len(cases) == 0 {
		return metrics
	}
	latencies := make([]int64, 0, len(cases))
	for _, item := range cases {
		if item.Passed {
			metrics.SuccessRate++
		}
		metrics.AverageSteps += float64(item.Steps)
		metrics.InputTokens += item.Usage.InputTokens
		metrics.OutputTokens += item.Usage.OutputTokens
		metrics.CacheReadTokens += item.Usage.CacheReadTokens
		metrics.CacheWriteTokens += item.Usage.CacheWriteTokens
		latencies = append(latencies, item.LatencyMS)
	}
	metrics.SuccessRate /= float64(len(cases))
	metrics.AverageSteps /= float64(len(cases))
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	middle := len(latencies) / 2
	if len(latencies)%2 == 1 {
		metrics.P50LatencyMS = float64(latencies[middle])
	} else {
		metrics.P50LatencyMS = float64(latencies[middle-1]+latencies[middle]) / 2
	}
	return metrics
}

// Markdown renders the same Report values used for JSON, without recomputing.
func Markdown(report Report) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Eval Report: %s\n\n", report.SuiteID)
	fmt.Fprintf(&builder, "Version: %d\nFixture: %t\n\n", report.Version, report.Fixture)
	builder.WriteString("| Case | Passed | Status | Steps | Latency (ms) | Input | Output | Cache read | Cache write |\n")
	builder.WriteString("|---|---:|---|---:|---:|---:|---:|---:|---:|\n")
	for _, item := range report.Cases {
		fmt.Fprintf(&builder, "| %s | %t | %s | %d | %d | %d | %d | %d | %d |\n", item.ID, item.Passed, item.Status, item.Steps, item.LatencyMS, item.Usage.InputTokens, item.Usage.OutputTokens, item.Usage.CacheReadTokens, item.Usage.CacheWriteTokens)
	}
	builder.WriteString("\n## Metrics\n\n")
	fmt.Fprintf(&builder, "case_count: %d\nsuccess_rate: %.2f\naverage_steps: %.2f\ninput_tokens: %d\noutput_tokens: %d\ncache_read_tokens: %d\ncache_write_tokens: %d\np50_latency_ms: %.2f\n", report.Metrics.CaseCount, report.Metrics.SuccessRate, report.Metrics.AverageSteps, report.Metrics.InputTokens, report.Metrics.OutputTokens, report.Metrics.CacheReadTokens, report.Metrics.CacheWriteTokens, report.Metrics.P50LatencyMS)
	return builder.String()
}

type fixtureProvider struct {
	responses []provider.Response
	index     int
}

func (p *fixtureProvider) Complete(context.Context, []provider.Message, []provider.ToolDefinition) (provider.Response, error) {
	if p.index >= len(p.responses) {
		return provider.Response{}, provider.NewError("fixture_exhausted", "fixture provider has no response")
	}
	response := p.responses[p.index]
	p.index++
	return response, nil
}

func runCase(ctx context.Context, item Case) (CaseResult, error) {
	result, _, err := ExecuteCase(ctx, item)
	return result, err
}

// ExecuteCase exposes the actual CaseResult and Trace used by the evaluator.
// Callers can persist or export the returned Trace without re-running it.
func ExecuteCase(ctx context.Context, item Case) (CaseResult, trace.Snapshot, error) {
	if item.ID == "failure-retry-routing" {
		return runOrchestrationCase(ctx, item)
	}
	return runAgentCase(ctx, item)
}

func runAgentCase(ctx context.Context, item Case) (CaseResult, trace.Snapshot, error) {
	var responses []provider.Response
	registry := tool.NewRegistry()
	var messages []provider.Message
	switch item.ID {
	case "text-completion":
		responses = []provider.Response{textResponse("hello from fixture", provider.Usage{InputTokens: 10, OutputTokens: 4})}
	case "single-tool":
		registerFixtureTool(registry, "echo", `{"type":"object"}`, func(string) string { return "echoed" })
		responses = []provider.Response{toolResponse("echo-1", "echo", provider.Usage{InputTokens: 12, OutputTokens: 3}), textResponse("tool complete", provider.Usage{InputTokens: 8, OutputTokens: 3})}
	case "validation-self-correction":
		registerFixtureTool(registry, "strict", `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`, func(string) string { return "fixed" })
		responses = []provider.Response{invalidToolResponse("bad-1", "strict", provider.Usage{InputTokens: 8, OutputTokens: 2}), toolResponse("good-1", "strict", provider.Usage{InputTokens: 8, OutputTokens: 2}), textResponse("corrected", provider.Usage{InputTokens: 8, OutputTokens: 2})}
	case "context-compact":
		messages = []provider.Message{{Role: provider.RoleUser, Text: "old request"}, {Role: provider.RoleAssistant, Text: strings.Repeat("history ", 24)}, {Role: provider.RoleUser, Text: item.Input}}
		responses = []provider.Response{textResponse("compacted", provider.Usage{InputTokens: 14, OutputTokens: 3})}
	default:
		return CaseResult{ID: item.ID}, trace.Snapshot{}, provider.NewError("unknown_fixture_case", item.ID)
	}
	recorder := fixtureRecorder(item.ID)
	var options []agent.RunOption
	if item.ID == "context-compact" {
		options = append(options, agent.WithRuntime(agent.RuntimeConfig{Compactor: contextx.NewCompactor(contextx.Config{BudgetTokens: 20, RecentRounds: 1, Summarizer: contextx.DeterministicSummarizer{}})}))
	}
	result, err := agent.NewRunner(&fixtureProvider{responses: responses}, registry, recorder, 4).Run(ctx, messages, options...)
	caseResult := resultFromTrace(item.ID, result.Trace, err)
	caseResult.Status = statusFromError(err)
	if err == nil {
		caseResult.Status = "succeeded"
		caseResult.ErrorCode = ""
	}
	caseResult.ObservedText = lastAssistantText(result.Messages)
	return caseResult, result.Trace, err
}

func runOrchestrationCase(ctx context.Context, item Case) (CaseResult, trace.Snapshot, error) {
	temp, err := os.MkdirTemp("", "prism-e1-eval-")
	if err != nil {
		return CaseResult{ID: item.ID}, trace.Snapshot{}, err
	}
	defer os.RemoveAll(temp)
	repo, err := createFixtureRepository(temp)
	if err != nil {
		return CaseResult{ID: item.ID}, trace.Snapshot{}, err
	}
	store, err := orchestration.OpenTaskStore(filepath.Join(temp, "tasks.db"))
	if err != nil {
		return CaseResult{ID: item.ID}, trace.Snapshot{}, err
	}
	defer store.Close()
	worktrees, err := orchestration.NewWorktreeManager(repo, filepath.Join(temp, "worktrees"))
	if err != nil {
		return CaseResult{ID: item.ID}, trace.Snapshot{}, err
	}
	manager, err := orchestration.NewManager(orchestration.ManagerConfig{Store: store, Worktrees: worktrees, Worker: fixtureWorker{}, Recorder: fixtureRecorder(item.ID)})
	if err != nil {
		return CaseResult{ID: item.ID}, trace.Snapshot{}, err
	}
	taskID := "e1-retry-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	result, runErr := manager.Run(ctx, orchestration.TaskSpec{ID: taskID, Strategy: "fixture", Goal: item.Input, BaseRef: "HEAD", Complexity: 9, TokenBudget: 4096, MaxRetries: 1})
	caseResult := resultFromTrace(item.ID, result.Trace, runErr)
	caseResult.Status = string(result.Task.Status)
	if result.Task.Error != nil {
		caseResult.ErrorCode = result.Task.Error.Code
	}
	return caseResult, result.Trace, runErr
}

func assertCase(item Case, result CaseResult, err error) CaseResult {
	checks := make([]string, 0)
	if result.Status != item.ExpectedStatus {
		checks = append(checks, "status")
	}
	if item.ExpectedText != "" && result.ObservedText != item.ExpectedText {
		checks = append(checks, "expected_text")
	}
	if item.ExpectedError != "" && result.ErrorCode != item.ExpectedError {
		checks = append(checks, "expected_error")
	}
	returnResultSpans := result.SpanNames
	for _, required := range item.RequiredSpans {
		found := false
		for _, name := range returnResultSpans {
			if name == required {
				found = true
				break
			}
		}
		if !found {
			checks = append(checks, "required_span:"+required)
		}
	}
	result.FailedChecks = checks
	result.Passed = len(checks) == 0 && ((err == nil) == (item.ExpectedStatus == "succeeded"))
	return result
}

func resultFromTrace(id string, snapshot trace.Snapshot, runErr error) CaseResult {
	usage := provider.Usage{}
	if len(snapshot.Spans) > 0 {
		usage = snapshot.Spans[0].Usage
	}
	if usage == (provider.Usage{}) {
		for _, span := range snapshot.Spans[1:] {
			usage.InputTokens += span.Usage.InputTokens
			usage.OutputTokens += span.Usage.OutputTokens
			usage.CacheReadTokens += span.Usage.CacheReadTokens
			usage.CacheWriteTokens += span.Usage.CacheWriteTokens
		}
	}
	latency := int64(0)
	if len(snapshot.Spans) > 0 && !snapshot.Spans[0].EndTime.IsZero() {
		latency = snapshot.Spans[0].EndTime.Sub(snapshot.Spans[0].StartTime).Milliseconds()
	}
	result := CaseResult{ID: id, Status: statusFromError(runErr), Steps: len(snapshot.Spans) - 1, Usage: usage, LatencyMS: latency, TraceID: snapshot.TraceID, SpanCount: len(snapshot.Spans)}
	if runErr != nil {
		if structured := provider.ErrorFrom(runErr, "eval_error"); structured != nil {
			result.ErrorCode = structured.Code
		}
	}
	result.SpanNames = make([]string, 0, len(snapshot.Spans))
	for _, span := range snapshot.Spans {
		result.SpanNames = append(result.SpanNames, span.Name)
	}
	return result
}

func statusFromError(err error) string {
	if err == nil {
		return "succeeded"
	}
	return "failed"
}

func lastAssistantText(messages []provider.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != provider.RoleAssistant {
			continue
		}
		for j := len(messages[i].OutputItems) - 1; j >= 0; j-- {
			if messages[i].OutputItems[j].Text != "" {
				return messages[i].OutputItems[j].Text
			}
		}
		if messages[i].Text != "" {
			return messages[i].Text
		}
	}
	return ""
}

func registerFixtureTool(registry *tool.Registry, name, schema string, response func(string) string) {
	_ = registry.Register(tool.Tool{Name: name, Schema: json.RawMessage(schema), Handler: func(_ context.Context, args json.RawMessage) (string, error) { return response(string(args)), nil }})
}

func textResponse(text string, usage provider.Usage) provider.Response {
	return provider.Response{Output: []provider.OutputItem{{Kind: provider.OutputText, Text: text}}, Usage: usage, StopReason: provider.StopReasonEndTurn}
}

func toolResponse(id, name string, usage provider.Usage) provider.Response {
	return provider.Response{Output: []provider.OutputItem{{Kind: provider.OutputToolCall, ToolCall: &provider.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(`{}`)}}}, Usage: usage, StopReason: provider.StopReasonToolCall}
}

func invalidToolResponse(id, name string, usage provider.Usage) provider.Response {
	return provider.Response{Output: []provider.OutputItem{{Kind: provider.OutputToolCall, ToolCall: &provider.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(`{"value":7}`)}}}, Usage: usage, StopReason: provider.StopReasonToolCall}
}

func fixtureRecorder(caseID string) *trace.Recorder {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	current := base
	nextID := 0
	return trace.NewRecorder(trace.Options{
		Clock:       func() time.Time { value := current; current = current.Add(time.Millisecond); return value },
		IDGenerator: func() string { nextID++; return caseID + "-span-" + strconv.Itoa(nextID) },
	})
}

type fixtureWorker struct{}

func (fixtureWorker) Run(ctx context.Context, request orchestration.WorkerRequest) (orchestration.WorkerResult, error) {
	if err := ctx.Err(); err != nil {
		return orchestration.WorkerResult{}, err
	}
	if err := os.WriteFile(filepath.Join(request.WorktreePath, "fixture-worker.txt"), []byte(request.Strategy+"\n"), 0o644); err != nil {
		return orchestration.WorkerResult{}, err
	}
	usage := provider.Usage{InputTokens: 6, OutputTokens: 4}
	if request.Attempt == 1 {
		return orchestration.WorkerResult{Status: orchestration.StatusFailed, Summary: "fixture retry", Usage: usage, Error: provider.NewError("fixture_retry", "intentional first attempt failure")}, nil
	}
	return orchestration.WorkerResult{Status: orchestration.StatusSucceeded, Summary: "fixture worker succeeded", Usage: usage}, nil
}

func createFixtureRepository(parent string) (string, error) {
	repo := filepath.Join(parent, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return "", err
	}
	if err := runFixtureGit(repo, "init", "-b", "main"); err != nil {
		return "", err
	}
	if err := runFixtureGit(repo, "config", "user.email", "prism-e1-fixture@example.com"); err != nil {
		return "", err
	}
	if err := runFixtureGit(repo, "config", "user.name", "Prism E1 Fixture"); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(repo, "README.txt"), []byte("Prism E1 local fixture\n"), 0o644); err != nil {
		return "", err
	}
	if err := runFixtureGit(repo, "add", "README.txt"); err != nil {
		return "", err
	}
	if err := runFixtureGit(repo, "commit", "-m", "fixture base"); err != nil {
		return "", err
	}
	return repo, nil
}

func runFixtureGit(repo string, args ...string) error {
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, strings.TrimSpace(string(output)))
	}
	return nil
}
