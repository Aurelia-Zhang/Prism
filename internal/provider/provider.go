// Package provider defines the protocol-neutral contracts used by the agent runtime.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Role identifies the author of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// OutputKind identifies an item in a provider response.
type OutputKind string

const (
	OutputText     OutputKind = "text"
	OutputToolCall OutputKind = "tool_call"
)

// ToolCall is an ordered request from the model to invoke a tool.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// OutputItem preserves the order of text and tool-call items returned by a model.
type OutputItem struct {
	Kind     OutputKind `json:"kind"`
	Text     string     `json:"text,omitempty"`
	ToolCall *ToolCall  `json:"tool_call,omitempty"`
}

// Error is a structured runtime error that can be returned to a caller or model.
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
	Cause     error  `json:"-"`
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap preserves cancellation and other underlying error checks for callers.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewError creates a structured runtime error.
func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// ErrorFrom converts an ordinary error into a structured error.
func ErrorFrom(err error, fallbackCode string) *Error {
	if err == nil {
		return nil
	}
	var structured *Error
	if errors.As(err, &structured) {
		return structured
	}
	code := fallbackCode
	if errors.Is(err, context.Canceled) {
		code = "context_canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = "context_deadline_exceeded"
	}
	return &Error{Code: code, Message: err.Error(), Cause: err}
}

// ToolResult is the ordered result of a ToolCall.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content,omitempty"`
	Error      *Error `json:"error,omitempty"`
}

// Usage contains token buckets common to model providers.
type Usage struct {
	InputTokens      int64 `json:"input_tokens,omitempty"`
	OutputTokens     int64 `json:"output_tokens,omitempty"`
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
}

// StopReason describes why a provider stopped generating.
type StopReason string

const (
	StopReasonEndTurn  StopReason = "end_turn"
	StopReasonToolCall StopReason = "tool_call"
	StopReasonMaxRound StopReason = "max_rounds"
	StopReasonCanceled StopReason = "canceled"
	StopReasonError    StopReason = "error"
)

// Message is the provider-neutral conversation representation.
type Message struct {
	Role        Role         `json:"role"`
	Text        string       `json:"text,omitempty"`
	OutputItems []OutputItem `json:"output_items,omitempty"`
	ToolResults []ToolResult `json:"tool_results,omitempty"`
}

// ToolDefinition describes a JSON Schema validated tool to a provider.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema"`
}

// Response is a provider response with output items kept in their original order.
type Response struct {
	Output     []OutputItem `json:"output,omitempty"`
	Usage      Usage        `json:"usage"`
	StopReason StopReason   `json:"stop_reason,omitempty"`
}

// Provider is the only capability required by the agent loop.
type Provider interface {
	Complete(context.Context, []Message, []ToolDefinition) (Response, error)
}
