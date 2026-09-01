package eval

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
)

func TestFixtureSuiteRunsFiveRealScenarios(t *testing.T) {
	report, err := RunSuite(context.Background(), FixtureSuite())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Cases) != 5 || report.Metrics.CaseCount != 5 {
		t.Fatalf("report case count = %#v", report)
	}
	if report.Metrics.SuccessRate != 1 {
		t.Fatalf("success rate = %v report=%#v", report.Metrics.SuccessRate, report)
	}
	for _, item := range report.Cases {
		if !item.Passed || item.TraceID == "" || item.SpanCount == 0 {
			t.Fatalf("fixture case did not pass with trace evidence: %#v", item)
		}
	}
	if report.Cases[2].Steps < 5 || report.Cases[3].Steps < 2 || report.Cases[4].Steps < 7 {
		t.Fatalf("fixture paths did not execute expected steps: %#v", report.Cases)
	}
	if report.Metrics.InputTokens == 0 || report.Metrics.OutputTokens == 0 || report.Metrics.P50LatencyMS <= 0 {
		t.Fatalf("metrics did not consume trace usage/timing: %#v", report.Metrics)
	}
	markdown := Markdown(report)
	if len(markdown) == 0 || !contains(markdown, "success_rate: 1.00") || contains(markdown, "p95") {
		t.Fatalf("markdown does not match metric policy: %s", markdown)
	}
}

func TestCalculateMetricsUsesCaseResults(t *testing.T) {
	metrics := CalculateMetrics([]CaseResult{
		{Passed: true, Steps: 2, LatencyMS: 10, Usage: usage(1, 2, 3, 4)},
		{Passed: false, Steps: 4, LatencyMS: 30, Usage: usage(5, 6, 7, 8)},
		{Passed: true, Steps: 6, LatencyMS: 20, Usage: usage(9, 10, 11, 12)},
	})
	if metrics.CaseCount != 3 || metrics.SuccessRate != 2.0/3.0 || metrics.AverageSteps != 4 || metrics.P50LatencyMS != 20 || metrics.InputTokens != 15 || metrics.CacheWriteTokens != 24 {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}
}

func TestFixtureEvalDoesNotChangeCurrentRepositoryWorktreeState(t *testing.T) {
	branchBefore := currentGitState(t, "branch", "--show-current")
	worktreesBefore := currentGitState(t, "worktree", "list", "--porcelain")
	statusBefore := currentGitState(t, "status", "--short", "--branch")
	if _, err := RunSuite(context.Background(), FixtureSuite()); err != nil {
		t.Fatal(err)
	}
	if got := currentGitState(t, "branch", "--show-current"); got != branchBefore {
		t.Fatalf("current branch changed: before=%q after=%q", branchBefore, got)
	}
	if got := currentGitState(t, "worktree", "list", "--porcelain"); got != worktreesBefore {
		t.Fatalf("current repository worktrees changed: before=%q after=%q", worktreesBefore, got)
	}
	if got := currentGitState(t, "status", "--short", "--branch"); got != statusBefore {
		t.Fatalf("current repository status changed: before=%q after=%q", statusBefore, got)
	}
}

func currentGitState(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func usage(input, output, read, write int64) provider.Usage {
	return provider.Usage{InputTokens: input, OutputTokens: output, CacheReadTokens: read, CacheWriteTokens: write}
}

func contains(value, part string) bool {
	return strings.Contains(value, part)
}
