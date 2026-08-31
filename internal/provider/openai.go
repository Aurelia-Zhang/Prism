package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// OpenAIConfig configures the Responses API adapter.
type OpenAIConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
	Client     *http.Client
	Retry      RetryConfig
}

// OpenAIResponses is a focused adapter for the OpenAI Responses streaming API.
type OpenAIResponses struct {
	config OpenAIConfig
}

// OpenAIProvider is kept as an explicit name for callers that prefer the
// Provider suffix.
type OpenAIProvider = OpenAIResponses

// NewOpenAIResponses creates an OpenAI Responses provider.
func NewOpenAIResponses(config OpenAIConfig) *OpenAIResponses {
	return &OpenAIResponses{config: config}
}

// NewOpenAI is a short alias for NewOpenAIResponses.
func NewOpenAI(config OpenAIConfig) *OpenAIResponses { return NewOpenAIResponses(config) }

// NewOpenAIProvider is an explicit constructor alias.
func NewOpenAIProvider(config OpenAIConfig) *OpenAIResponses { return NewOpenAIResponses(config) }

// CompleteStream is an alias for Stream for integrations using that spelling.
func (p *OpenAIResponses) CompleteStream(ctx context.Context, messages []Message, tools []ToolDefinition, sink EventSink) (Response, error) {
	return p.Stream(ctx, messages, tools, sink)
}

func (p *OpenAIResponses) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (Response, error) {
	return p.Stream(ctx, messages, tools, nil)
}

func (p *OpenAIResponses) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, sink EventSink) (response Response, err error) {
	if p == nil {
		return response, NewError("invalid_request", "OpenAI provider is nil")
	}
	if strings.TrimSpace(p.config.Model) == "" {
		return response, NewError("invalid_request", "OpenAI model is required")
	}
	err = Retry(ctx, p.config.Retry, func(attempt int) (error, bool) {
		if sink != nil {
			if sinkErr := sink(StreamEvent{Kind: EventAttempt, Attempt: Attempt{Provider: "openai", Model: p.config.Model, Number: attempt}}); sinkErr != nil {
				return sinkErr, true
			}
		}
		candidate, attemptEmitted, requestErr := p.streamAttempt(ctx, messages, tools, sink)
		if requestErr == nil {
			response = candidate
			return nil, attemptEmitted
		}
		if sink != nil {
			structured := ErrorFrom(requestErr, "network")
			if sinkErr := sink(StreamEvent{Kind: EventAttempt, Attempt: Attempt{Provider: "openai", Model: p.config.Model, Number: attempt, Error: structured}}); sinkErr != nil {
				return sinkErr, true
			}
		}
		return requestErr, attemptEmitted
	})
	if err != nil {
		return Response{}, err
	}
	return response, nil
}

func (p *OpenAIResponses) streamAttempt(ctx context.Context, messages []Message, tools []ToolDefinition, sink EventSink) (Response, bool, error) {
	body, err := json.Marshal(openAIRequest{Model: p.config.Model, Input: openAIInput(messages), Tools: openAITools(tools), Stream: true})
	if err != nil {
		return Response{}, false, &Error{Code: "invalid_request", Message: err.Error(), Cause: err}
	}
	url, err := providerEndpoint(p.config.BaseURL, "responses")
	if err != nil {
		return Response{}, false, &Error{Code: "invalid_request", Message: err.Error(), Cause: err}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, false, networkError(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if p.config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+p.config.APIKey)
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
	state := newOpenAIStreamState(sink)
	readErr := readSSE(ctx, response.Body, state.handle)
	if readErr != nil {
		if structured, ok := readErr.(*Error); ok {
			return Response{}, state.emitted, structured
		}
		return Response{}, state.emitted, networkError(readErr)
	}
	if state.terminalRank == 0 {
		return Response{}, state.emitted, streamProtocolError("stream ended without a terminal response event")
	}
	if state.terminalErr != nil {
		return Response{}, state.emitted, state.terminalErr
	}
	return state.response(), state.emitted, nil
}

func providerEndpoint(base, resource string) (string, error) {
	base = strings.TrimRight(base, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if strings.HasSuffix(base, "/responses") || strings.HasSuffix(base, "/messages") {
		return base, nil
	}
	if strings.HasSuffix(base, "/v1") {
		return endpoint(base, resource)
	}
	return endpoint(base+"/v1", resource)
}

type openAIRequest struct {
	Model  string           `json:"model"`
	Input  []map[string]any `json:"input"`
	Tools  []map[string]any `json:"tools,omitempty"`
	Stream bool             `json:"stream"`
}

func openAITools(definitions []ToolDefinition) []map[string]any {
	tools := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		var schema any
		if json.Unmarshal(definition.Schema, &schema) != nil {
			continue
		}
		tools = append(tools, map[string]any{"type": "function", "name": definition.Name, "description": definition.Description, "parameters": schema})
	}
	return tools
}

func openAIInput(messages []Message) []map[string]any {
	input := make([]map[string]any, 0)
	for _, message := range messages {
		if message.Text != "" {
			input = append(input, map[string]any{"role": string(message.Role), "content": message.Text})
		}
		for _, item := range message.OutputItems {
			switch item.Kind {
			case OutputOpaque:
				if item.Opaque != nil && len(item.Opaque.Raw) != 0 {
					var raw map[string]any
					if json.Unmarshal(item.Opaque.Raw, &raw) == nil {
						input = append(input, raw)
					}
				}
			case OutputText:
				input = append(input, map[string]any{"role": "assistant", "content": item.Text})
			case OutputToolCall:
				if item.ToolCall != nil {
					input = append(input, map[string]any{"type": "function_call", "id": item.ToolCall.ID, "call_id": item.ToolCall.ID, "name": item.ToolCall.Name, "arguments": string(item.ToolCall.Arguments)})
				}
			}
		}
		for _, result := range message.ToolResults {
			content := result.Content
			if result.Error != nil {
				content = result.Error.Error()
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": result.ToolCallID, "output": content})
		}
	}
	return input
}

type openAIItem struct {
	id       string
	typ      string
	text     string
	args     string
	name     string
	raw      json.RawMessage
	item     OutputItem
	complete bool
}

type openAIStreamState struct {
	sink         EventSink
	items        []*openAIItem
	byID         map[string]*openAIItem
	usage        Usage
	stop         StopReason
	terminalRank int
	terminalErr  *Error
	emitted      bool
}

func newOpenAIStreamState(sink EventSink) *openAIStreamState {
	return &openAIStreamState{sink: sink, byID: make(map[string]*openAIItem)}
}

func (s *openAIStreamState) handle(event sseEvent) error {
	if string(event.Data) == "[DONE]" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return streamProtocolError("invalid JSON event: " + err.Error())
	}
	typ := event.Name
	if typ == "" {
		typ = stringValue(payload["type"])
	}
	switch typ {
	case "response.output_item.added":
		_, err := s.addItem(payloadMap(payload["item"]))
		return err
	case "response.output_item.done":
		item, err := s.addItem(payloadMap(payload["item"]))
		if err != nil {
			return err
		}
		if item != nil && !item.complete {
			item.complete = true
			return s.emit(StreamEvent{Kind: EventCompletedItem, Item: item.item, ItemID: item.id})
		}
	case "response.output_text.delta":
		item := s.ensure(stringValue(payload["item_id"]), "text")
		item.text += stringValue(payload["delta"])
		item.item = OutputItem{Kind: OutputText, Text: item.text}
		return s.emit(StreamEvent{Kind: EventTextDelta, Text: stringValue(payload["delta"]), ItemID: item.id})
	case "response.function_call_arguments.delta":
		item := s.ensure(stringValue(payload["item_id"]), "function_call")
		item.args += stringValue(payload["delta"])
		item.item = OutputItem{Kind: OutputToolCall, ToolCall: &ToolCall{ID: item.id, Name: item.name, Arguments: json.RawMessage(item.args)}}
		return s.emit(StreamEvent{Kind: EventToolArguments, ArgumentsDelta: stringValue(payload["delta"]), ItemID: item.id, ToolCallID: item.id, ToolName: item.name})
	case "response.completed":
		return s.completed(payload)
	case "response.incomplete":
		details := payloadMap(payloadMap(payload["response"])["incomplete_details"])
		reason := stringValue(details["reason"])
		if reason == "" {
			reason = "OpenAI response was incomplete"
		}
		return s.terminal(2, NewError("context_length", reason))
	case "response.failed", "error", "response.error":
		return s.terminal(3, openAIEventError(payload))
	case "response.usage":
		return s.setUsage(payloadMap(payload["usage"]))
	default:
		if strings.HasSuffix(typ, ".usage") {
			return s.setUsage(payloadMap(payload["usage"]))
		}
	}
	return nil
}

func (s *openAIStreamState) completed(payload map[string]any) error {
	response := payloadMap(payload["response"])
	if response == nil {
		response = payload
	}
	if usage := payloadMap(response["usage"]); usage != nil {
		if err := s.setUsage(usage); err != nil {
			return err
		}
	}
	if output, ok := response["output"].([]any); ok {
		for _, value := range output {
			if _, err := s.addItem(payloadMap(value)); err != nil {
				return err
			}
		}
	}
	stop := stringValue(response["status"])
	if stop == "incomplete" {
		return s.terminal(2, NewError("context_length", "OpenAI response was incomplete"))
	}
	if len(s.items) > 0 && hasToolCalls(s.items) {
		s.stop = StopReasonToolCall
	} else {
		s.stop = StopReasonEndTurn
	}
	return s.terminal(1, nil)
}

func (s *openAIStreamState) addItem(raw map[string]any) (*openAIItem, error) {
	if raw == nil {
		return nil, streamProtocolError("output item is missing")
	}
	id := stringValue(raw["id"])
	item := s.byID[id]
	if item == nil || id == "" {
		item = &openAIItem{id: id, typ: stringValue(raw["type"]), raw: marshalRaw(raw)}
		s.items = append(s.items, item)
		if id != "" {
			s.byID[id] = item
		}
	}
	item.raw = marshalRaw(raw)
	if itemType := stringValue(raw["type"]); itemType != "" {
		item.typ = itemType
	}
	switch item.typ {
	case "message", "output_text":
		item.text = openAIText(raw)
		item.item = OutputItem{Kind: OutputText, Text: item.text}
	case "function_call":
		item.name = stringValue(raw["name"])
		if item.name == "" {
			item.name = stringValue(raw["function_name"])
		}
		item.args = stringValue(raw["arguments"])
		callID := stringValue(raw["call_id"])
		if callID == "" {
			callID = item.id
		}
		item.item = OutputItem{Kind: OutputToolCall, ToolCall: &ToolCall{ID: callID, Name: item.name, Arguments: json.RawMessage(item.args)}}
	case "reasoning":
		item.item = OutputItem{Kind: OutputOpaque, Opaque: &OpaqueItem{Provider: "openai", Type: item.typ, ID: item.id, Raw: append(json.RawMessage(nil), item.raw...)}}
	default:
		return nil, streamProtocolError("unsupported output item type " + item.typ)
	}
	return item, nil
}

func (s *openAIStreamState) ensure(id, typ string) *openAIItem {
	if id == "" {
		id = fmt.Sprintf("stream-item-%d", len(s.items)+1)
	}
	if item := s.byID[id]; item != nil {
		return item
	}
	item := &openAIItem{id: id, typ: typ}
	s.items = append(s.items, item)
	s.byID[id] = item
	return item
}

func (s *openAIStreamState) setUsage(raw map[string]any) error {
	if raw == nil {
		return streamProtocolError("usage event is missing usage")
	}
	s.usage.InputTokens = int64Value(raw["input_tokens"])
	s.usage.OutputTokens = int64Value(raw["output_tokens"])
	if s.sink != nil {
		return s.emit(StreamEvent{Kind: EventUsage, Usage: s.usage})
	}
	return nil
}

func (s *openAIStreamState) emit(event StreamEvent) error {
	if event.Kind == EventTextDelta || event.Kind == EventToolArguments || event.Kind == EventCompletedItem {
		s.emitted = true
	}
	if s.sink == nil {
		return nil
	}
	return s.sink(event)
}

func (s *openAIStreamState) terminal(rank int, err *Error) error {
	if rank >= s.terminalRank {
		s.terminalRank = rank
		if err != nil {
			s.terminalErr = err
		}
	}
	return nil
}

func (s *openAIStreamState) response() Response {
	output := make([]OutputItem, 0, len(s.items))
	for _, item := range s.items {
		if item.item.Kind == OutputToolCall && item.item.ToolCall != nil && item.item.ToolCall.Arguments == nil {
			item.item.ToolCall.Arguments = json.RawMessage(item.args)
		}
		output = append(output, item.item)
	}
	return Response{Output: output, Usage: s.usage, StopReason: s.stop}
}

func openAIEventError(payload map[string]any) *Error {
	err := payloadMap(payload["error"])
	if err == nil {
		response := payloadMap(payload["response"])
		err = payloadMap(response["error"])
		if err == nil {
			err = response
		}
	}
	message := stringValue(err["message"])
	if message == "" {
		message = "OpenAI stream failed"
	}
	return mapProviderAPIError(stringValue(err["type"]), stringValue(err["code"]), message)
}

func mapProviderAPIError(typ, code, message string) *Error {
	lower := strings.ToLower(typ + " " + code + " " + message)
	result := &Error{Code: "server_error", Message: message, Retryable: true}
	switch {
	case strings.Contains(lower, "auth") || strings.Contains(lower, "api_key"):
		result.Code, result.Retryable = "authentication", false
	case strings.Contains(lower, "permission") || strings.Contains(lower, "forbidden"):
		result.Code, result.Retryable = "permission", false
	case strings.Contains(lower, "invalid"):
		result.Code, result.Retryable = "invalid_request", false
	case strings.Contains(lower, "rate") || strings.Contains(lower, "quota"):
		result.Code = "rate_limit"
	case strings.Contains(lower, "context") || strings.Contains(lower, "token"):
		result.Code, result.Retryable = "context_length", false
	case strings.Contains(lower, "filter") || strings.Contains(lower, "safety"):
		result.Code, result.Retryable = "content_filter", false
	}
	return result
}

func openAIText(raw map[string]any) string {
	if text := stringValue(raw["text"]); text != "" {
		return text
	}
	content, _ := raw["content"].([]any)
	var builder strings.Builder
	for _, value := range content {
		part := payloadMap(value)
		builder.WriteString(stringValue(part["text"]))
	}
	return builder.String()
}

func hasToolCalls(items []*openAIItem) bool {
	for _, item := range items {
		if item.item.Kind == OutputToolCall {
			return true
		}
	}
	return false
}

func payloadMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func int64Value(value any) int64 {
	result, _ := value.(float64)
	return int64(result)
}

func marshalRaw(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return append(json.RawMessage(nil), encoded...)
}
