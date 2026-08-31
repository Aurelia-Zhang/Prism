package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestOpenAIResponsesAggregatesOrderedItemsAndContinuation(t *testing.T) {
	var requests atomic.Int32
	messages := []Message{
		{Role: RoleUser, Text: "find"},
		{Role: RoleAssistant, OutputItems: []OutputItem{
			{Kind: OutputOpaque, Opaque: &OpaqueItem{Provider: "openai", Type: "reasoning", ID: "prior-reason", Raw: json.RawMessage(`{"type":"reasoning","id":"prior-reason","summary":[],"encrypted_content":"prior-secret"}`)}},
			{Kind: OutputOpaque, Opaque: &OpaqueItem{Provider: "anthropic", Type: "thinking", Raw: json.RawMessage(`{"type":"thinking","thinking":"do not send"}`)}},
			{Kind: OutputToolCall, ToolCall: &ToolCall{ID: "prior-call", Name: "lookup", Arguments: json.RawMessage(`{"q":"prior"}`)}},
		}},
		{Role: RoleTool, ToolResults: []ToolResult{{ToolCallID: "prior-call", Name: "lookup", Content: "prior-result"}}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected request: path=%s authorization=%s", request.URL.Path, request.Header.Get("Authorization"))
		}
		requests.Add(1)
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "gpt-test" {
			t.Fatalf("model was not mapped: %#v", body)
		}
		include, _ := body["include"].([]any)
		if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
			t.Fatalf("reasoning continuation was not requested: %#v", body["include"])
		}
		mappedTools, _ := body["tools"].([]any)
		if len(mappedTools) != 1 || payloadMap(mappedTools[0])["name"] != "lookup" {
			t.Fatalf("tools were not mapped: %#v", body["tools"])
		}
		var sawReasoning, sawToolOutput, sawForeignOpaque bool
		for _, value := range body["input"].([]any) {
			item := payloadMap(value)
			switch stringValue(item["type"]) {
			case "reasoning":
				sawReasoning = item["encrypted_content"] == "prior-secret"
			case "function_call_output":
				sawToolOutput = item["call_id"] == "prior-call" && item["output"] == "prior-result"
			case "thinking":
				sawForeignOpaque = true
			}
		}
		if !sawReasoning || !sawToolOutput || sawForeignOpaque {
			t.Fatalf("OpenAI continuation input mismatch: %#v", body["input"])
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		parts := []string{
			sse("response.output_item.added", map[string]any{"type": "response.output_item.added", "item": map[string]any{"type": "function_call", "id": "call-1", "call_id": "call-1", "name": "lookup"}}),
			sse("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "item_id": "call-1", "delta": "{\"q\":"}),
			sse("response.output_item.added", map[string]any{"type": "response.output_item.added", "item": map[string]any{"type": "reasoning", "id": "reason-1", "summary": []any{}}}),
			sse("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": "text-1", "delta": "hello"}),
			sse("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "reasoning", "id": "reason-1", "summary": []any{}, "encrypted_content": "secret"}}),
			sse("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{map[string]any{"type": "function_call", "id": "call-1", "call_id": "call-1", "name": "lookup", "arguments": "{\"q\":\"ok\"}"}, map[string]any{"type": "reasoning", "id": "reason-1", "summary": []any{}, "encrypted_content": "secret"}, map[string]any{"type": "message", "id": "text-1", "content": []any{map[string]any{"type": "output_text", "text": "hello"}}}}, "usage": map[string]any{"input_tokens": 11, "output_tokens": 7}}}),
		}
		for _, part := range parts {
			for index := 0; index < len(part); index += 3 {
				end := index + 3
				if end > len(part) {
					end = len(part)
				}
				_, _ = writer.Write([]byte(part[index:end]))
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	var events []StreamEvent
	provider := NewOpenAIResponses(OpenAIConfig{BaseURL: server.URL, APIKey: "test-key", Model: "gpt-test", Retry: RetryConfig{Sleeper: noSleep}})
	response, err := provider.Stream(context.Background(), messages, []ToolDefinition{{Name: "lookup", Schema: json.RawMessage(`{"type":"object"}`)}}, func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || response.StopReason != StopReasonToolCall || response.Usage.InputTokens != 11 || response.Usage.OutputTokens != 7 {
		t.Fatalf("unexpected response: %#v requests=%d", response, requests.Load())
	}
	if len(response.Output) != 3 || response.Output[0].Kind != OutputToolCall || response.Output[1].Kind != OutputOpaque || response.Output[2].Text != "hello" {
		t.Fatalf("output order or deduplication failed: %#v", response.Output)
	}
	if response.Output[1].Opaque == nil || !strings.Contains(string(response.Output[1].Opaque.Raw), "encrypted_content") {
		t.Fatalf("reasoning continuation was not preserved: %#v", response.Output[1])
	}
	if len(events) == 0 || events[0].Kind != EventAttempt {
		t.Fatalf("attempt/event sink was not called: %#v", events)
	}
}

func TestAnthropicMessagesAggregatesThinkingToolAndUsage(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Text: "search"},
		{Role: RoleAssistant, OutputItems: []OutputItem{
			{Kind: OutputOpaque, Opaque: &OpaqueItem{Provider: "anthropic", Type: "thinking", Raw: json.RawMessage(`{"type":"thinking","thinking":"plan","signature":"sig"}`)}},
			{Kind: OutputOpaque, Opaque: &OpaqueItem{Provider: "openai", Type: "reasoning", Raw: json.RawMessage(`{"type":"reasoning","id":"do-not-send"}`)}},
			{Kind: OutputToolCall, ToolCall: &ToolCall{ID: "prior-tool", Name: "search", Arguments: json.RawMessage(`{"q":"prior"}`)}},
		}},
		{Role: RoleTool, ToolResults: []ToolResult{{ToolCallID: "prior-tool", Name: "search", Content: "prior-result"}}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages" || request.Header.Get("x-api-key") != "anthropic-key" || request.Header.Get("anthropic-version") != "2024-01-01" {
			t.Fatalf("unexpected Anthropic headers/request: %s %s %s", request.URL.Path, request.Header.Get("x-api-key"), request.Header.Get("anthropic-version"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		var sawThinking, sawToolUse, sawToolResult, sawForeignOpaque bool
		for _, messageValue := range body["messages"].([]any) {
			message := payloadMap(messageValue)
			for _, blockValue := range message["content"].([]any) {
				block := payloadMap(blockValue)
				switch stringValue(block["type"]) {
				case "thinking":
					sawThinking = block["signature"] == "sig"
				case "tool_use":
					sawToolUse = block["id"] == "prior-tool"
				case "tool_result":
					sawToolResult = block["tool_use_id"] == "prior-tool"
				case "reasoning":
					sawForeignOpaque = true
				}
			}
		}
		if !sawThinking || !sawToolUse || !sawToolResult || sawForeignOpaque {
			t.Fatalf("Anthropic continuation input mismatch: %#v", body["messages"])
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"cache_read_input_tokens\":2,\"cache_creation_input_tokens\":3}}}\n\n"))
		_, _ = writer.Write([]byte("event: ping\ndata: {\"type\":\"ping\"}\n\n"))
		_, _ = writer.Write([]byte("event: future_envelope\ndata: {\"type\":\"future_envelope\"}\n\n"))
		_, _ = writer.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"plan\"}}\n\n"))
		_, _ = writer.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig\"}}\n\n"))
		_, _ = writer.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		_, _ = writer.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-1\",\"name\":\"search\",\"input\":{}}}\n\n"))
		_, _ = writer.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"x\\\":1}\"}}\n\n"))
		_, _ = writer.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n"))
		_, _ = writer.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":4}}\n\n"))
		_, _ = writer.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()
	provider := NewAnthropicMessages(AnthropicConfig{BaseURL: server.URL, APIKey: "anthropic-key", Model: "claude-test", AnthropicVersion: "2024-01-01", Retry: RetryConfig{Sleeper: noSleep}})
	response, err := provider.Complete(context.Background(), messages, []ToolDefinition{{Name: "search", Schema: json.RawMessage(`{"type":"object"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if response.StopReason != StopReasonToolCall || response.Usage != (Usage{InputTokens: 5, OutputTokens: 4, CacheReadTokens: 2, CacheWriteTokens: 3}) {
		t.Fatalf("usage/stop mapping failed: %#v", response)
	}
	if len(response.Output) != 2 || response.Output[0].Kind != OutputOpaque || response.Output[1].ToolCall == nil || string(response.Output[1].ToolCall.Arguments) != `{"x":1}` {
		t.Fatalf("Anthropic output aggregation failed: %#v", response.Output)
	}
}

func TestProviderDefaultEndpoints(t *testing.T) {
	tests := []struct {
		name        string
		defaultBase string
		resource    string
		want        string
	}{
		{name: "openai", defaultBase: openAIDefaultBaseURL, resource: "responses", want: "https://api.openai.com/v1/responses"},
		{name: "anthropic", defaultBase: anthropicDefaultBaseURL, resource: "messages", want: "https://api.anthropic.com/v1/messages"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := providerEndpoint("", testCase.defaultBase, testCase.resource)
			if err != nil || got != testCase.want {
				t.Fatalf("providerEndpoint()=%q, %v; want %q", got, err, testCase.want)
			}
		})
	}
}

func TestAnthropicUsesDefaultEndpoint(t *testing.T) {
	var requestURL string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestURL = request.URL.String()
		stream := "event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n" +
			"event: message_delta\ndata: {\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
			"event: message_stop\ndata: {}\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(stream)),
			Request:    request,
		}, nil
	})}
	_, err := NewAnthropic(AnthropicConfig{Model: "claude-test", HTTPClient: client}).Complete(context.Background(), []Message{{Role: RoleUser, Text: "hello"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if requestURL != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("Anthropic default request URL=%q", requestURL)
	}
}

func TestAnthropicMaxTokensHasProviderStopReason(t *testing.T) {
	state := newAnthropicStreamState(nil)
	if err := state.handle(sseEvent{Name: "message_start", Data: []byte(`{"message":{"usage":{"input_tokens":1}}}`)}); err != nil {
		t.Fatal(err)
	}
	if err := state.handle(sseEvent{Name: "message_delta", Data: []byte(`{"delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":2}}`)}); err != nil {
		t.Fatal(err)
	}
	if err := state.handle(sseEvent{Name: "message_stop", Data: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if response := state.response(); response.StopReason != StopReasonMaxTokens {
		t.Fatalf("stop reason=%q, want %q", response.StopReason, StopReasonMaxTokens)
	}
}

func TestRetryAfterAndPartialStreamDoNotRetry(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		writer.Header().Set("Retry-After", "1")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer server.Close()
	var waits []time.Duration
	provider := NewOpenAI(OpenAIConfig{BaseURL: server.URL, Model: "gpt", Retry: RetryConfig{MaxAttempts: 3, MaxTotalWait: 2 * time.Second, Sleeper: func(_ context.Context, duration time.Duration) error { waits = append(waits, duration); return nil }}})
	_, err := provider.Complete(context.Background(), nil, nil)
	structured := new(Error)
	if !errors.As(err, &structured) || structured.Code != "rate_limit" || attempts.Load() != 3 || len(waits) != 2 || waits[0] != time.Second {
		t.Fatalf("Retry-After was not honored: err=%v attempts=%d waits=%v", err, attempts.Load(), waits)
	}

	attempts.Store(0)
	partial := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"text\",\"delta\":\"partial\"}\n\n"))
	}))
	defer partial.Close()
	provider = NewOpenAI(OpenAIConfig{BaseURL: partial.URL, Model: "gpt", Retry: RetryConfig{MaxAttempts: 3, Sleeper: noSleep}})
	_, err = provider.Complete(context.Background(), nil, nil)
	if attempts.Load() != 1 {
		t.Fatalf("partial stream was retried: attempts=%d err=%v", attempts.Load(), err)
	}
}

func TestStreamingCancellationStopsWithoutRetry(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		_, _ = writer.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"text\",\"delta\":\"partial\"}\n\n"))
		flusher.Flush()
		<-request.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	provider := NewOpenAI(OpenAIConfig{BaseURL: server.URL, Model: "gpt", Retry: RetryConfig{MaxAttempts: 3, Sleeper: noSleep}})
	_, err := provider.Complete(ctx, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) || attempts.Load() != 1 {
		t.Fatalf("cancellation was retried or remapped: attempts=%d err=%v", attempts.Load(), err)
	}
}

func TestHTTPErrorMapping(t *testing.T) {
	cases := []struct {
		status    int
		message   string
		code      string
		retryable bool
	}{
		{http.StatusUnauthorized, "bad key", "authentication", false},
		{http.StatusForbidden, "denied", "permission", false},
		{http.StatusBadRequest, "invalid schema", "invalid_request", false},
		{http.StatusTooManyRequests, "slow down", "rate_limit", true},
		{http.StatusBadRequest, "maximum context length exceeded", "context_length", false},
		{http.StatusBadRequest, "content_filter blocked", "content_filter", false},
		{http.StatusRequestTimeout, "timeout", "timeout", true},
		{http.StatusBadGateway, "upstream", "server_error", true},
	}
	for _, testCase := range cases {
		mapped := mapHTTPError(testCase.status, testCase.message)
		if mapped.Code != testCase.code || mapped.Retryable != testCase.retryable {
			t.Errorf("mapHTTPError(%d, %q)=%+v, want code=%s retryable=%v", testCase.status, testCase.message, mapped, testCase.code, testCase.retryable)
		}
	}
}

func noSleep(context.Context, time.Duration) error { return nil }

func sse(event string, payload any) string {
	encoded, _ := json.Marshal(payload)
	return "event: " + event + "\ndata: " + string(encoded) + "\n\n"
}
