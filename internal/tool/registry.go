// Package tool implements a JSON Schema validated tool registry.
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Handler executes a registered tool with its original JSON arguments.
type Handler func(context.Context, json.RawMessage) (string, error)

// Tool is a named JSON Schema validated tool.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Handler     Handler
}

type registeredTool struct {
	Tool
	compiled *jsonschema.Schema
}

// Registry stores tools in registration order and supports concurrent calls.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]registeredTool
	order []string
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]registeredTool)}
}

// Register validates and adds a unique tool.
func (r *Registry) Register(tool Tool) error {
	if strings.TrimSpace(tool.Name) == "" || tool.Name != strings.TrimSpace(tool.Name) {
		return fmt.Errorf("tool name must be non-empty and trimmed")
	}
	if strings.ContainsAny(tool.Name, "/#?\x00\n\r\t") {
		return fmt.Errorf("tool name %q contains unsupported characters", tool.Name)
	}
	if tool.Handler == nil {
		return fmt.Errorf("tool %q has no handler", tool.Name)
	}
	if len(tool.Schema) == 0 {
		return fmt.Errorf("tool %q has an empty schema", tool.Name)
	}
	var document any
	if err := json.Unmarshal(tool.Schema, &document); err != nil {
		return fmt.Errorf("tool %q schema is invalid JSON: %w", tool.Name, err)
	}
	compiler := jsonschema.NewCompiler()
	location := "memory://tool/" + tool.Name
	if err := compiler.AddResource(location, document); err != nil {
		return fmt.Errorf("tool %q schema cannot be added: %w", tool.Name, err)
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		return fmt.Errorf("tool %q schema is invalid: %w", tool.Name, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[tool.Name]; exists {
		return fmt.Errorf("tool %q is already registered", tool.Name)
	}
	tool.Schema = append(json.RawMessage(nil), tool.Schema...)
	r.tools[tool.Name] = registeredTool{Tool: tool, compiled: compiled}
	r.order = append(r.order, tool.Name)
	return nil
}

// Unregister removes tools by name and returns the number of tools removed.
// Names that are not registered are ignored.
func (r *Registry) Unregister(names ...string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for _, name := range names {
		if _, exists := r.tools[name]; exists {
			delete(r.tools, name)
			removed++
		}
	}
	if removed == 0 {
		return 0
	}
	order := r.order[:0]
	for _, name := range r.order {
		if _, exists := r.tools[name]; exists {
			order = append(order, name)
		}
	}
	r.order = order
	return removed
}

// Has reports whether a tool name is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.tools[name]
	return exists
}

// Definitions returns a stable, detached list for a provider request.
func (r *Registry) Definitions() []provider.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	definitions := make([]provider.ToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		registered := r.tools[name]
		definitions = append(definitions, provider.ToolDefinition{
			Name:        registered.Name,
			Description: registered.Description,
			Schema:      append(json.RawMessage(nil), registered.Schema...),
		})
	}
	return definitions
}

// Call validates arguments and invokes a tool. Validation and handler failures are
// returned as ToolResult values; context cancellation is returned as a Go error.
func (r *Registry) Call(ctx context.Context, call provider.ToolCall) (provider.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return provider.ToolResult{}, err
	}
	result := provider.ToolResult{ToolCallID: call.ID, Name: call.Name}

	r.mu.RLock()
	registered, exists := r.tools[call.Name]
	r.mu.RUnlock()
	if !exists {
		result.Error = provider.NewError("tool_not_found", fmt.Sprintf("tool %q is not registered", call.Name))
		return result, nil
	}

	var arguments any
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		result.Error = &provider.Error{Code: "invalid_arguments_json", Message: err.Error()}
		return result, nil
	}
	if err := registered.compiled.Validate(arguments); err != nil {
		result.Error = &provider.Error{Code: "tool_arguments_schema_invalid", Message: err.Error()}
		return result, nil
	}

	content, err := registered.Handler(ctx, append(json.RawMessage(nil), call.Arguments...))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return provider.ToolResult{}, err
		}
		result.Error = provider.ErrorFrom(err, "tool_handler_error")
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return provider.ToolResult{}, err
	}
	result.Content = content
	return result, nil
}
