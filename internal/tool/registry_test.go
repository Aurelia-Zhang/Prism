package tool

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
)

const objectSchema = `{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}`

func TestRegistryRegisterDefinitionsAndCall(t *testing.T) {
	registry := NewRegistry()
	var calls atomic.Int32
	if err := registry.Register(Tool{Name: "weather", Description: "read weather", Schema: json.RawMessage(objectSchema), Handler: func(_ context.Context, arguments json.RawMessage) (string, error) {
		calls.Add(1)
		return string(arguments), nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(Tool{Name: "clock", Schema: json.RawMessage(`{"type":"object"}`), Handler: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}); err != nil {
		t.Fatal(err)
	}
	definitions := registry.Definitions()
	if len(definitions) != 2 || definitions[0].Name != "weather" || definitions[1].Name != "clock" {
		t.Fatalf("definitions are not stable: %#v", definitions)
	}
	definitions[0].Schema[0] = 'X'
	result, err := registry.Call(context.Background(), provider.ToolCall{ID: "call-1", Name: "weather", Arguments: json.RawMessage(`{"city":"Xi'an"}`)})
	if err != nil || result.Error != nil || result.Content != `{"city":"Xi'an"}` || calls.Load() != 1 {
		t.Fatalf("valid call failed: result=%#v err=%v calls=%d", result, err, calls.Load())
	}
}

func TestRegistryRejectsDuplicateAndInvalidSchema(t *testing.T) {
	registry := NewRegistry()
	tool := Tool{Name: "same", Schema: json.RawMessage(`true`), Handler: func(context.Context, json.RawMessage) (string, error) { return "", nil }}
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tool); err == nil {
		t.Fatal("duplicate tool was accepted")
	}
	if err := registry.Register(Tool{Name: "bad", Schema: json.RawMessage(`{"type":"not-a-schema-type"}`), Handler: tool.Handler}); err == nil {
		t.Fatal("invalid schema was accepted")
	}
	if err := registry.Register(Tool{Name: "", Schema: json.RawMessage(`true`), Handler: tool.Handler}); err == nil {
		t.Fatal("empty name was accepted")
	}
}

func TestRegistryValidationAndHandlerErrorsDoNotInvokeHandler(t *testing.T) {
	registry := NewRegistry()
	var calls atomic.Int32
	if err := registry.Register(Tool{Name: "weather", Schema: json.RawMessage(objectSchema), Handler: func(context.Context, json.RawMessage) (string, error) {
		calls.Add(1)
		return "unexpected", nil
	}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []byte
		code string
	}{
		{"invalid JSON", []byte(`{"city"`), "invalid_arguments_json"},
		{"schema mismatch", []byte(`{"city":3}`), "tool_arguments_schema_invalid"},
	}
	for _, testCase := range cases {
		result, err := registry.Call(context.Background(), provider.ToolCall{ID: testCase.name, Name: "weather", Arguments: json.RawMessage(testCase.args)})
		if err != nil || result.Error == nil || result.Error.Code != testCase.code {
			t.Errorf("%s: result=%#v err=%v", testCase.name, result, err)
		}
	}
	missing, err := registry.Call(context.Background(), provider.ToolCall{ID: "missing", Name: "unknown", Arguments: json.RawMessage(`{}`)})
	if err != nil || missing.Error == nil || missing.Error.Code != "tool_not_found" {
		t.Fatalf("missing tool: result=%#v err=%v", missing, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("handler was invoked for invalid calls: %d", calls.Load())
	}

	if err := registry.Register(Tool{Name: "fails", Schema: json.RawMessage(`{}`), Handler: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("backend unavailable") }}); err != nil {
		t.Fatal(err)
	}
	failure, err := registry.Call(context.Background(), provider.ToolCall{ID: "fails", Name: "fails", Arguments: json.RawMessage(`null`)})
	if err != nil || failure.Error == nil || failure.Error.Code != "tool_handler_error" {
		t.Fatalf("handler failure: result=%#v err=%v", failure, err)
	}
}

func TestRegistryCancellationIsNotToolResult(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Tool{Name: "wait", Schema: json.RawMessage(`{}`), Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := registry.Call(ctx, provider.ToolCall{Name: "wait", Arguments: json.RawMessage(`{}`)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was converted to a result: %v", err)
	}
}
