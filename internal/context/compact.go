// Package context provides bounded, structured conversation compaction.
package context

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
)

// Summarizer turns messages that are about to leave the model context into a
// structured summary. Production-quality summarization is deliberately left to
// a Provider integration; tests can provide a deterministic implementation.
type Summarizer interface {
	Summarize(context.Context, []provider.Message) (Summary, error)
}

// Summary is the fixed shape used in the compacted context prefix.
type Summary struct {
	Completed   []string `json:"completed"`
	Pending     []string `json:"pending"`
	KeyFiles    []string `json:"key_files"`
	Decisions   []string `json:"decisions"`
	Constraints []string `json:"constraints"`
}

// Format keeps the required fields visible to both a model and a human
// inspecting a captured conversation.
func (s Summary) Format() string {
	encoded, err := json.Marshal(s)
	if err != nil {
		return "context_summary: {}"
	}
	return "context_summary: " + string(encoded)
}

// Config controls when and how a conversation is compacted.
type Config struct {
	BudgetTokens int
	RecentRounds int
	Summarizer   Summarizer
}

// Report records the deterministic size estimates used by the runtime Trace
// and evidence report.
type Report struct {
	Compacted       bool
	BeforeTokens    int
	AfterTokens     int
	BeforeMessages  int
	AfterMessages   int
	DroppedMessages int
}

// Compactor applies Config to a provider-neutral conversation.
type Compactor struct {
	config Config
}

// NewCompactor creates a compactor. A nil summarizer makes compaction a no-op.
func NewCompactor(config Config) *Compactor {
	return &Compactor{config: config}
}

// Compact replaces older complete rounds with one structured system message.
// A round is grouped at user-message boundaries, so an assistant tool call and
// its following tool result are always retained or removed together.
func (c *Compactor) Compact(ctx context.Context, messages []provider.Message) ([]provider.Message, Report, error) {
	report := Report{BeforeTokens: EstimateTokens(messages), BeforeMessages: len(messages)}
	if c == nil || c.config.BudgetTokens <= 0 || c.config.RecentRounds < 1 || c.config.Summarizer == nil || report.BeforeTokens <= c.config.BudgetTokens {
		report.AfterTokens = report.BeforeTokens
		report.AfterMessages = report.BeforeMessages
		return cloneMessages(messages), report, nil
	}

	prefix, groups := splitConversation(messages)
	if len(groups) <= c.config.RecentRounds {
		report.AfterTokens = report.BeforeTokens
		report.AfterMessages = report.BeforeMessages
		return cloneMessages(messages), report, nil
	}

	// Protect the newest N rounds. Dropping additional recent rounds would make
	// the compactor violate its explicit retention contract.
	dropCount := len(groups) - c.config.RecentRounds
	dropped := flatten(groups[:dropCount])
	summary, err := c.config.Summarizer.Summarize(ctx, cloneMessages(dropped))
	if err != nil {
		return nil, report, err
	}
	compactPrefix := append(cloneMessages(prefix), provider.Message{Role: provider.RoleSystem, Text: summary.Format()})
	compacted := append(compactPrefix, cloneMessages(flatten(groups[dropCount:]))...)
	report.Compacted = true
	report.DroppedMessages = len(dropped)
	report.AfterTokens = EstimateTokens(compacted)
	report.AfterMessages = len(compacted)
	return compacted, report, nil
}

// EstimateTokens is a stable, provider-neutral estimate. It intentionally does
// not claim to match a model tokenizer; the runtime uses it only for budgeting.
func EstimateTokens(messages []provider.Message) int {
	total := 0
	for _, message := range messages {
		bytes, _ := json.Marshal(message)
		total += (utf8.RuneCount(bytes) + 3) / 4
	}
	return total
}

func splitConversation(messages []provider.Message) ([]provider.Message, [][]provider.Message) {
	prefix := make([]provider.Message, 0)
	groups := make([][]provider.Message, 0)
	current := make([]provider.Message, 0)
	for _, message := range messages {
		if message.Role == provider.RoleSystem && len(current) == 0 && len(groups) == 0 {
			prefix = append(prefix, message)
			continue
		}
		if message.Role == provider.RoleUser && len(current) > 0 {
			groups = append(groups, current)
			current = nil
		}
		current = append(current, message)
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return prefix, groups
}

func flatten(groups [][]provider.Message) []provider.Message {
	var messages []provider.Message
	for _, group := range groups {
		messages = append(messages, group...)
	}
	return messages
}

func cloneMessages(messages []provider.Message) []provider.Message {
	cloned := make([]provider.Message, len(messages))
	for i, message := range messages {
		cloned[i] = message
		cloned[i].OutputItems = append([]provider.OutputItem(nil), message.OutputItems...)
		cloned[i].ToolResults = append([]provider.ToolResult(nil), message.ToolResults...)
	}
	return cloned
}

// DeterministicSummarizer is useful for local demos and evidence generation.
// It is not a substitute for measuring a live model's summary quality.
type DeterministicSummarizer struct{}

// Summarize records a small, reproducible summary of the source messages.
func (DeterministicSummarizer) Summarize(_ context.Context, messages []provider.Message) (Summary, error) {
	return Summary{
		Completed:   []string{fmt.Sprintf("summarized %d messages", len(messages))},
		Pending:     []string{"current task"},
		KeyFiles:    []string{"conversation"},
		Decisions:   []string{"keep recent rounds"},
		Constraints: []string{"deterministic fixture"},
	}, nil
}
