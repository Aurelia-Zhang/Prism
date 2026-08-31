package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Aurelia-Zhang/Prism/internal/mcp"
	"github.com/Aurelia-Zhang/Prism/internal/tool"
)

// TestExternalSmoke is opt-in because it needs a locally installed MCP server.
// Set PRISM_MCP_EXTERNAL_COMMAND and optionally PRISM_MCP_EXTERNAL_ARGS,
// PRISM_MCP_EXTERNAL_NAME, PRISM_MCP_EXTERNAL_TOOL, and PRISM_MCP_EXTERNAL_INPUT.
func TestExternalSmoke(t *testing.T) {
	command := os.Getenv("PRISM_MCP_EXTERNAL_COMMAND")
	if command == "" {
		t.Skip("set PRISM_MCP_EXTERNAL_COMMAND to run the external MCP smoke test")
	}
	name := os.Getenv("PRISM_MCP_EXTERNAL_NAME")
	if name == "" {
		name = "external"
	}
	registry := tool.NewRegistry()
	client, err := mcp.NewClient(registry, mcp.Config{
		Name:    name,
		Command: command,
		Args:    strings.Fields(os.Getenv("PRISM_MCP_EXTERNAL_ARGS")),
		Env:     strings.Fields(os.Getenv("PRISM_MCP_EXTERNAL_ENV")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	toolName := os.Getenv("PRISM_MCP_EXTERNAL_TOOL")
	if toolName == "" {
		return
	}
	input := os.Getenv("PRISM_MCP_EXTERNAL_INPUT")
	if input == "" {
		input = `{}`
	}
	if !json.Valid([]byte(input)) {
		t.Fatalf("PRISM_MCP_EXTERNAL_INPUT is invalid JSON")
	}
	qualified := client.QualifiedName(toolName)
	if _, err := client.Call(context.Background(), qualified, json.RawMessage(input)); err != nil {
		t.Fatal(err)
	}
}
