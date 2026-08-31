package context

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
)

func TestCompactKeepsStructuredSummaryRecentRoundsAndToolPair(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.RoleSystem, Text: "stable instructions"},
		{Role: provider.RoleUser, Text: "old request"},
		{Role: provider.RoleAssistant, OutputItems: []provider.OutputItem{{Kind: provider.OutputText, Text: strings.Repeat("old answer ", 80)}}},
		{Role: provider.RoleUser, Text: "second request"},
		{Role: provider.RoleAssistant, OutputItems: []provider.OutputItem{{Kind: provider.OutputToolCall, ToolCall: &provider.ToolCall{ID: "call-2", Name: "read", Arguments: json.RawMessage(`{}`)}}}},
		{Role: provider.RoleTool, ToolResults: []provider.ToolResult{{ToolCallID: "call-2", Name: "read", Content: "second result"}}},
		{Role: provider.RoleAssistant, Text: "second follow-up"},
		{Role: provider.RoleUser, Text: "latest request"},
		{Role: provider.RoleAssistant, OutputItems: []provider.OutputItem{{Kind: provider.OutputText, Text: "latest answer"}}},
	}
	compactor := NewCompactor(Config{BudgetTokens: 45, RecentRounds: 2, Summarizer: DeterministicSummarizer{}})
	compacted, report, err := compactor.Compact(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Compacted || report.BeforeMessages != len(messages) || report.AfterMessages >= report.BeforeMessages {
		t.Fatalf("unexpected compaction report: %#v", report)
	}
	t.Logf("compaction evidence: before_tokens=%d after_tokens=%d before_messages=%d after_messages=%d", report.BeforeTokens, report.AfterTokens, report.BeforeMessages, report.AfterMessages)
	if len(compacted) < 5 {
		t.Fatalf("structured summary missing: %#v", compacted)
	}
	var summaryText string
	for _, message := range compacted {
		if message.Role == provider.RoleSystem && strings.Contains(message.Text, "context_summary") {
			summaryText = message.Text
		}
	}
	for _, field := range []string{"completed", "pending", "key_files", "decisions", "constraints"} {
		if !strings.Contains(summaryText, `"`+field+`"`) {
			t.Fatalf("structured summary missing %s: %q", field, summaryText)
		}
	}
	encoded, _ := json.Marshal(compacted)
	if strings.Contains(string(encoded), "old request") || !strings.Contains(string(encoded), "latest request") || !strings.Contains(string(encoded), "second result") || !strings.Contains(string(encoded), "call-2") {
		t.Fatalf("compaction dropped a recent round or split tool pair: %s", encoded)
	}
}

func TestEstimateTokensIsStable(t *testing.T) {
	messages := []provider.Message{{Role: provider.RoleUser, Text: "four words"}}
	if got := EstimateTokens(messages); got != EstimateTokens(messages) || got == 0 {
		t.Fatalf("unstable token estimate: %d", got)
	}
}
