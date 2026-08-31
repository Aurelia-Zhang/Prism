package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

const anthropicDefaultBaseURL = "https://api.anthropic.com/v1"

// AnthropicConfig configures the Messages API adapter.
type AnthropicConfig struct {
	BaseURL          string
	APIKey           string
	Model            string
	AnthropicVersion string
	MaxTokens        int
	HTTPClient       *http.Client
	Client           *http.Client
	Retry            RetryConfig
}

// AnthropicMessages is a focused streaming adapter for Anthropic Messages.
type AnthropicMessages struct {
	config AnthropicConfig
}

// AnthropicProvider is an explicit Provider-suffixed name.
type AnthropicProvider = AnthropicMessages

// NewAnthropicMessages creates an Anthropic Messages provider.
func NewAnthropicMessages(config AnthropicConfig) *AnthropicMessages {
	return &AnthropicMessages{config: config}
}

// NewAnthropic is a short alias for NewAnthropicMessages.
func NewAnthropic(config AnthropicConfig) *AnthropicMessages { return NewAnthropicMessages(config) }

// NewAnthropicProvider is an explicit constructor alias.
func NewAnthropicProvider(config AnthropicConfig) *AnthropicMessages {
	return NewAnthropicMessages(config)
}

// CompleteStream is an alias for Stream for integrations using that spelling.
func (p *AnthropicMessages) CompleteStream(ctx context.Context, messages []Message, tools []ToolDefinition, sink EventSink) (Response, error) {
	return p.Stream(ctx, messages, tools, sink)
}

func (p *AnthropicMessages) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (Response, error) {
	return p.Stream(ctx, messages, tools, nil)
}

func (p *AnthropicMessages) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, sink EventSink) (response Response, err error) {
	if p == nil {
		return response, NewError("invalid_request", "Anthropic provider is nil")
	}
	if strings.TrimSpace(p.config.Model) == "" {
		return response, NewError("invalid_request", "Anthropic model is required")
	}
	version := p.config.AnthropicVersion
	if version == "" {
		version = "2023-06-01"
	}
	err = Retry(ctx, p.config.Retry, func(attempt int) (error, bool) {
		if sink != nil {
			if sinkErr := sink(StreamEvent{Kind: EventAttempt, Attempt: Attempt{Provider: "anthropic", Model: p.config.Model, Number: attempt}}); sinkErr != nil {
				return sinkErr, true
			}
		}
		candidate, emitted, requestErr := p.streamAttempt(ctx, messages, tools, sink, version)
		if requestErr == nil {
			response = candidate
			return nil, emitted
		}
		if sink != nil {
			structured := ErrorFrom(requestErr, "network")
			if sinkErr := sink(StreamEvent{Kind: EventAttempt, Attempt: Attempt{Provider: "anthropic", Model: p.config.Model, Number: attempt, Error: structured}}); sinkErr != nil {
				return sinkErr, true
			}
		}
		return requestErr, emitted
	})
	if err != nil {
		return Response{}, err
	}
	return response, nil
}

func (p *AnthropicMessages) streamAttempt(ctx context.Context, messages []Message, tools []ToolDefinition, sink EventSink, version string) (Response, bool, error) {
	maxTokens := p.config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	body, err := json.Marshal(anthropicRequest{Model: p.config.Model, System: anthropicSystem(messages), Messages: anthropicMessages(messages), MaxTokens: maxTokens, Tools: anthropicTools(tools), Stream: true})
	if err != nil {
		return Response{}, false, &Error{Code: "invalid_request", Message: err.Error(), Cause: err}
	}
	url, err := providerEndpoint(p.config.BaseURL, anthropicDefaultBaseURL, "messages")
	if err != nil {
		return Response{}, false, &Error{Code: "invalid_request", Message: err.Error(), Cause: err}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, false, networkError(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("anthropic-version", version)
	if p.config.APIKey != "" {
		request.Header.Set("x-api-key", p.config.APIKey)
	}
	client := p.config.HTTPClient
	if client == nil {
		client = p.config.Client
	}
	response, err := defaultHTTPClient(client).Do(request)
	if err != nil {
		return Response{}, false, networkError(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Response{}, false, readHTTPError(response)
	}
	state := newAnthropicStreamState(sink)
	readErr := readSSE(ctx, response.Body, state.handle)
	if readErr != nil {
		if structured, ok := readErr.(*Error); ok {
			return Response{}, state.emitted, structured
		}
		return Response{}, state.emitted, networkError(readErr)
	}
	if state.terminalRank == 0 {
		return Response{}, state.emitted, streamProtocolError("stream ended without message_stop")
	}
	if state.terminalErr != nil {
		return Response{}, state.emitted, state.terminalErr
	}
	return state.response(), state.emitted, nil
}

type anthropicRequest struct {
	Model     string           `json:"model"`
	System    string           `json:"system,omitempty"`
	Messages  []map[string]any `json:"messages"`
	MaxTokens int              `json:"max_tokens"`
	Tools     []map[string]any `json:"tools,omitempty"`
	Stream    bool             `json:"stream"`
}

func anthropicSystem(messages []Message) string {
	var parts []string
	for _, message := range messages {
		if message.Role == RoleSystem && message.Text != "" {
			parts = append(parts, message.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func anthropicMessages(messages []Message) []map[string]any {
	result := make([]map[string]any, 0)
	for _, message := range messages {
		if message.Role == RoleSystem {
			continue
		}
		blocks := make([]any, 0)
		if message.Text != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": message.Text})
		}
		for _, item := range message.OutputItems {
			switch item.Kind {
			case OutputText:
				blocks = append(blocks, map[string]any{"type": "text", "text": item.Text})
			case OutputToolCall:
				if item.ToolCall != nil {
					var input any
					if json.Unmarshal(item.ToolCall.Arguments, &input) != nil {
						input = map[string]any{}
					}
					blocks = append(blocks, map[string]any{"type": "tool_use", "id": item.ToolCall.ID, "name": item.ToolCall.Name, "input": input})
				}
			case OutputOpaque:
				if item.Opaque != nil && item.Opaque.Provider == "anthropic" && len(item.Opaque.Raw) != 0 {
					var raw map[string]any
					if json.Unmarshal(item.Opaque.Raw, &raw) == nil {
						blocks = append(blocks, raw)
					}
				}
			}
		}
		for _, toolResult := range message.ToolResults {
			content := toolResult.Content
			isError := false
			if toolResult.Error != nil {
				content, isError = toolResult.Error.Error(), true
			}
			blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": toolResult.ToolCallID, "content": content, "is_error": isError})
		}
		if len(blocks) != 0 {
			role := "user"
			if message.Role == RoleAssistant {
				role = "assistant"
			}
			result = append(result, map[string]any{"role": role, "content": blocks})
		}
	}
	return result
}

func anthropicTools(definitions []ToolDefinition) []map[string]any {
	result := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		var schema any
		if json.Unmarshal(definition.Schema, &schema) != nil {
			continue
		}
		result = append(result, map[string]any{"name": definition.Name, "description": definition.Description, "input_schema": schema})
	}
	return result
}

type anthropicBlock struct {
	index     int
	typ       string
	id        string
	name      string
	text      string
	args      string
	thinking  string
	signature string
	raw       map[string]any
	item      OutputItem
	complete  bool
}

type anthropicStreamState struct {
	sink         EventSink
	blocks       []*anthropicBlock
	byIndex      map[int]*anthropicBlock
	usage        Usage
	stop         StopReason
	terminalRank int
	terminalErr  *Error
	emitted      bool
}

func newAnthropicStreamState(sink EventSink) *anthropicStreamState {
	return &anthropicStreamState{sink: sink, byIndex: make(map[int]*anthropicBlock)}
}

func (s *anthropicStreamState) handle(event sseEvent) error {
	var payload map[string]any
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return streamProtocolError("invalid JSON event: " + err.Error())
	}
	switch event.Name {
	case "message_start":
		return s.setUsage(payloadMap(payloadMap(payload["message"])["usage"]))
	case "content_block_start":
		return s.blockStart(intValue(payload["index"]), payloadMap(payload["content_block"]))
	case "content_block_delta":
		return s.blockDelta(intValue(payload["index"]), payloadMap(payload["delta"]))
	case "content_block_stop":
		return s.blockStop(intValue(payload["index"]))
	case "message_delta":
		if err := s.setUsage(payloadMap(payload["usage"])); err != nil && payload["usage"] != nil {
			return err
		}
		s.setStopReason(stringValue(payloadMap(payload["delta"])["stop_reason"]))
	case "message_stop":
		if s.stop == "" {
			s.stop = StopReasonEndTurn
		}
		return s.terminal(1, nil)
	case "ping":
		return nil
	case "error":
		return s.terminal(3, anthropicEventError(payload))
	default:
		// New top-level envelope events are safe to ignore. Unknown content
		// blocks and deltas remain explicit errors because dropping them could
		// corrupt provider continuation state.
		return nil
	}
	return nil
}

func (s *anthropicStreamState) setUsage(raw map[string]any) error {
	if raw == nil {
		return streamProtocolError("usage event is missing usage")
	}
	if _, ok := raw["input_tokens"]; ok {
		s.usage.InputTokens = int64Value(raw["input_tokens"])
	}
	if _, ok := raw["output_tokens"]; ok {
		s.usage.OutputTokens = int64Value(raw["output_tokens"])
	}
	if _, ok := raw["cache_read_input_tokens"]; ok {
		s.usage.CacheReadTokens = int64Value(raw["cache_read_input_tokens"])
	}
	if _, ok := raw["cache_creation_input_tokens"]; ok {
		s.usage.CacheWriteTokens = int64Value(raw["cache_creation_input_tokens"])
	}
	if s.sink != nil {
		return s.emit(StreamEvent{Kind: EventUsage, Usage: s.usage})
	}
	return nil
}

func (s *anthropicStreamState) blockStart(index int, raw map[string]any) error {
	if raw == nil {
		return streamProtocolError("content block is missing")
	}
	block := &anthropicBlock{index: index, typ: stringValue(raw["type"]), id: stringValue(raw["id"]), name: stringValue(raw["name"]), raw: cloneMap(raw)}
	if block.typ != "text" && block.typ != "tool_use" && block.typ != "thinking" && block.typ != "redacted_thinking" {
		return streamProtocolError("unsupported Anthropic content block " + block.typ)
	}
	if block.typ == "text" {
		block.text = stringValue(raw["text"])
	}
	s.byIndex[index] = block
	s.blocks = append(s.blocks, block)
	s.updateItem(block)
	return nil
}

func (s *anthropicStreamState) blockDelta(index int, delta map[string]any) error {
	block := s.byIndex[index]
	if block == nil || delta == nil {
		return streamProtocolError("content block delta has no block")
	}
	switch stringValue(delta["type"]) {
	case "text_delta":
		block.text += stringValue(delta["text"])
		block.raw["text"] = block.text
		s.updateItem(block)
		return s.emit(StreamEvent{Kind: EventTextDelta, Text: stringValue(delta["text"]), ItemID: block.id})
	case "input_json_delta":
		block.args += stringValue(delta["partial_json"])
		block.raw["input"] = json.RawMessage(block.args)
		s.updateItem(block)
		return s.emit(StreamEvent{Kind: EventToolArguments, ArgumentsDelta: stringValue(delta["partial_json"]), ItemID: block.id, ToolCallID: block.id, ToolName: block.name})
	case "thinking_delta":
		block.thinking += stringValue(delta["thinking"])
		block.raw["thinking"] = block.thinking
		s.updateItem(block)
	case "signature_delta":
		block.signature += stringValue(delta["signature"])
		block.raw["signature"] = block.signature
		s.updateItem(block)
	default:
		return streamProtocolError("unsupported Anthropic delta " + stringValue(delta["type"]))
	}
	return nil
}

func (s *anthropicStreamState) blockStop(index int) error {
	block := s.byIndex[index]
	if block == nil {
		return streamProtocolError("content block stop has no block")
	}
	if !block.complete {
		block.complete = true
		return s.emit(StreamEvent{Kind: EventCompletedItem, ItemID: block.id, Item: block.item})
	}
	return nil
}

func (s *anthropicStreamState) updateItem(block *anthropicBlock) {
	switch block.typ {
	case "text":
		block.item = OutputItem{Kind: OutputText, Text: block.text}
	case "tool_use":
		args := block.args
		if args == "" {
			if input := block.raw["input"]; input != nil {
				encoded, _ := json.Marshal(input)
				args = string(encoded)
			}
		}
		block.item = OutputItem{Kind: OutputToolCall, ToolCall: &ToolCall{ID: block.id, Name: block.name, Arguments: json.RawMessage(args)}}
	case "thinking", "redacted_thinking":
		block.item = OutputItem{Kind: OutputOpaque, Opaque: &OpaqueItem{Provider: "anthropic", Type: block.typ, ID: block.id, Raw: marshalRaw(block.raw)}}
	}
}

func (s *anthropicStreamState) setStopReason(reason string) {
	switch reason {
	case "tool_use":
		s.stop = StopReasonToolCall
	case "max_tokens":
		s.stop = StopReasonMaxTokens
	case "end_turn", "stop_sequence", "":
		if s.stop == "" {
			s.stop = StopReasonEndTurn
		}
	default:
		s.stop = StopReasonEndTurn
	}
}

func (s *anthropicStreamState) emit(event StreamEvent) error {
	if event.Kind == EventTextDelta || event.Kind == EventToolArguments || event.Kind == EventCompletedItem {
		s.emitted = true
	}
	if s.sink == nil {
		return nil
	}
	return s.sink(event)
}

func (s *anthropicStreamState) terminal(rank int, err *Error) error {
	if rank >= s.terminalRank {
		s.terminalRank = rank
		if err != nil {
			s.terminalErr = err
		}
	}
	return nil
}

func (s *anthropicStreamState) response() Response {
	output := make([]OutputItem, 0, len(s.blocks))
	for _, block := range s.blocks {
		output = append(output, block.item)
	}
	return Response{Output: output, Usage: s.usage, StopReason: s.stop}
}

func anthropicEventError(payload map[string]any) *Error {
	inner := payloadMap(payload["error"])
	message := stringValue(inner["message"])
	return mapProviderAPIError(stringValue(inner["type"]), "", message)
}

func intValue(value any) int {
	result, _ := value.(float64)
	return int(result)
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
