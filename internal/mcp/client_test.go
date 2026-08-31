package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Aurelia-Zhang/Prism/internal/mcp"
	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/Aurelia-Zhang/Prism/internal/tool"
)

func TestMain(m *testing.M) {
	if os.Getenv("PRISM_MCP_FIXTURE") == "1" {
		runFixture(os.Getenv("PRISM_MCP_FIXTURE_MODE"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fixtureClient(t *testing.T, registry *tool.Registry, mode string) *mcp.Client {
	t.Helper()
	client, err := mcp.NewClient(registry, mcp.Config{
		Name:    "fixture",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPFixtureProcess"},
		Env: []string{
			"PRISM_MCP_FIXTURE=1",
			"PRISM_MCP_FIXTURE_MODE=" + mode,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestMCPFixtureProcess(t *testing.T) {
	t.Skip("executed through TestMain as an MCP fixture subprocess")
}

func TestClientLifecycleListAndCall(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Tool{Name: "local", Schema: json.RawMessage(`{}`), Handler: func(context.Context, json.RawMessage) (string, error) { return "local", nil }}); err != nil {
		t.Fatal(err)
	}
	client := fixtureClient(t, registry, "normal")
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	definitions := registry.Definitions()
	if len(definitions) != 2 || definitions[0].Name != "local" || definitions[1].Name != "mcp.fixture.echo" {
		t.Fatalf("unexpected discovered tools: %#v", definitions)
	}
	result, err := registry.Call(context.Background(), provider.ToolCall{
		ID:        "call-1",
		Name:      "mcp.fixture.echo",
		Arguments: json.RawMessage(`{"value":"ok"}`),
	})
	if err != nil || result.Error != nil || result.Content != `called echo with {"value":"ok"}` {
		t.Fatalf("MCP call failed: result=%#v err=%v", result, err)
	}
}

func TestClientProtocolError(t *testing.T) {
	registry := tool.NewRegistry()
	client := fixtureClient(t, registry, "protocol-error")
	err := client.Start(context.Background())
	var protocolErr *mcp.RPCError
	if !errors.As(err, &protocolErr) || protocolErr.Code != -32001 {
		t.Fatalf("expected JSON-RPC error, got %v", err)
	}
	if len(registry.Definitions()) != 0 {
		t.Fatalf("protocol failure registered tools: %#v", registry.Definitions())
	}
}

func TestClientExitRemovesOwnToolsAndBoundsStderr(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Tool{Name: "local", Schema: json.RawMessage(`{}`), Handler: func(context.Context, json.RawMessage) (string, error) { return "local", nil }}); err != nil {
		t.Fatal(err)
	}
	client, err := mcp.NewClient(registry, mcp.Config{
		Name:            "exit-fixture",
		Command:         os.Args[0],
		Args:            []string{"-test.run=TestMCPFixtureProcess"},
		Env:             []string{"PRISM_MCP_FIXTURE=1", "PRISM_MCP_FIXTURE_MODE=exit"},
		MaxStderrBytes:  16,
		MaxMessageBytes: 64 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err = client.Wait()
	var exitErr *mcp.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exit error, got %v", err)
	}
	if len(client.Stderr()) > 16 {
		t.Fatalf("stderr was not bounded: %d bytes", len(client.Stderr()))
	}
	definitions := registry.Definitions()
	if len(definitions) != 1 || definitions[0].Name != "local" {
		t.Fatalf("server tools were not removed: %#v", definitions)
	}
}

func TestClientCallCancellation(t *testing.T) {
	registry := tool.NewRegistry()
	client := fixtureClient(t, registry, "cancel")
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.Call(ctx, "mcp.fixture.wait", json.RawMessage(`{}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func runFixture(mode string) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	var waitingCall json.RawMessage
	for scanner.Scan() {
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			return
		}
		switch request.Method {
		case "initialize":
			writeFixtureResponse(request.ID, map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "fixture", "version": "1"}}, nil)
		case "notifications/initialized":
			continue
		case "tools/list":
			if mode == "protocol-error" {
				writeFixtureResponse(request.ID, nil, map[string]any{"code": -32001, "message": "fixture list failed"})
				continue
			}
			tools := []map[string]any{{"name": "echo", "description": "fixture echo", "inputSchema": map[string]any{"type": "object"}}}
			if mode == "cancel" {
				tools = append(tools, map[string]any{"name": "wait", "inputSchema": map[string]any{"type": "object"}})
			}
			writeFixtureResponse(request.ID, map[string]any{"tools": tools}, nil)
			if mode == "exit" {
				_, _ = os.Stderr.WriteString(strings.Repeat("stderr-", 64))
				return
			}
		case "tools/call":
			if mode == "cancel" && request.Params.Name == "wait" {
				waitingCall = append(json.RawMessage(nil), request.ID...)
				continue
			}
			writeFixtureResponse(request.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": fmt.Sprintf("called %s with %s", request.Params.Name, request.Params.Arguments)}}, "isError": false}, nil)
		case "notifications/cancelled":
			if len(waitingCall) > 0 {
				writeFixtureResponse(waitingCall, map[string]any{"content": []map[string]string{{"type": "text", "text": "cancelled"}}}, nil)
				waitingCall = nil
			}
		}
	}
}

func writeFixtureResponse(id json.RawMessage, result any, rpcErr any) {
	response := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id)}
	if rpcErr != nil {
		response["error"] = rpcErr
	} else {
		response["result"] = result
	}
	data, _ := json.Marshal(response)
	_, _ = os.Stdout.Write(append(data, '\n'))
}
