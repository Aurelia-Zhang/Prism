package provider

import (
	"context"
	"os"
	"testing"
	"time"
)

// These tests are opt-in. They record only provider/model/result/usage metadata,
// never credentials or model content.
func TestOpenAILiveSmoke(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("set OPENAI_API_KEY to run the OpenAI live smoke")
	}
	model := os.Getenv("PRISM_OPENAI_MODEL")
	if model == "" {
		model = "gpt-4.1-mini"
	}
	started := time.Now()
	response, err := NewOpenAI(OpenAIConfig{APIKey: key, Model: model}).Complete(context.Background(), []Message{{Role: RoleUser, Text: "Reply with one short word."}}, nil)
	if err != nil {
		t.Fatalf("OpenAI live smoke failed provider=openai model=%s elapsed=%s: %v", model, time.Since(started), err)
	}
	t.Logf("live provider=openai model=%s result=ok elapsed=%s usage=%+v", model, time.Since(started), response.Usage)
}

func TestAnthropicLiveSmoke(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("set ANTHROPIC_API_KEY to run the Anthropic live smoke")
	}
	model := os.Getenv("PRISM_ANTHROPIC_MODEL")
	if model == "" {
		model = "claude-3-5-haiku-latest"
	}
	started := time.Now()
	response, err := NewAnthropic(AnthropicConfig{APIKey: key, Model: model}).Complete(context.Background(), []Message{{Role: RoleUser, Text: "Reply with one short word."}}, nil)
	if err != nil {
		t.Fatalf("Anthropic live smoke failed provider=anthropic model=%s elapsed=%s: %v", model, time.Since(started), err)
	}
	t.Logf("live provider=anthropic model=%s result=ok elapsed=%s usage=%+v", model, time.Since(started), response.Usage)
}
