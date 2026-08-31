// Package mcp implements the MCP stdio transport used by Prism.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/Aurelia-Zhang/Prism/internal/tool"
)

const (
	defaultProtocolVersion = "2024-11-05"
	defaultClientName      = "prism"
	defaultClientVersion   = "dev"
	defaultStderrLimit     = 64 * 1024
	defaultMessageLimit    = 4 * 1024 * 1024
)

var (
	// ErrNotStarted is returned when an operation requires a started client.
	ErrNotStarted = errors.New("mcp client is not started")
	// ErrClosed is returned when the client was closed before a request completed.
	ErrClosed = errors.New("mcp client is closed")
	// ErrToolNotFound is returned when a namespaced MCP tool is unknown.
	ErrToolNotFound = errors.New("mcp tool is not registered")
)

// Config describes an MCP server process.
type Config struct {
	// Name is the stable namespace component used for registered tool names.
	Name    string
	Command string
	Args    []string
	// Env contains additional or overriding environment entries in KEY=VALUE form.
	Env []string

	ProtocolVersion string
	ClientName      string
	ClientVersion   string
	MaxStderrBytes  int
	MaxMessageBytes int
}

// ProtocolError reports malformed or unsupported JSON-RPC protocol traffic.
type ProtocolError struct {
	Message string
	Cause   error
}

func (e *ProtocolError) Error() string {
	if e == nil {
		return ""
	}
	return "mcp protocol error: " + e.Message
}

func (e *ProtocolError) Unwrap() error { return e.Cause }

// RPCError is an error object returned by the MCP server for a JSON-RPC request.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("mcp rpc error %d: %s", e.Code, e.Message)
}

// ExitError reports an unexpected MCP server exit and includes bounded stderr.
type ExitError struct {
	Err    error
	Stderr string
}

func (e *ExitError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return "mcp server exited"
	}
	return "mcp server exited: " + e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error"`
}

type pendingResponse struct {
	result json.RawMessage
	err    *RPCError
	goErr  error
}

type boundedStderr struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func (b *boundedStderr) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		b.data = append(b.data, data[:remaining]...)
	}
	if len(data) > remaining {
		b.truncated = true
	}
	return len(data), nil
}

func (b *boundedStderr) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

// Client owns one MCP server process and registers its discovered tools.
type Client struct {
	registry *tool.Registry
	config   Config

	mu              sync.Mutex
	writeMu         sync.Mutex
	started         bool
	finished        bool
	closing         bool
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	done            chan struct{}
	exitErr         error
	terminalErr     error
	nextID          int64
	pending         map[int64]chan pendingResponse
	toolNames       []string
	serverToolNames map[string]string
	stderr          boundedStderr
}

// NewClient creates a client that will register discovered tools in registry.
func NewClient(registry *tool.Registry, config Config) (*Client, error) {
	if registry == nil {
		return nil, errors.New("mcp registry is required")
	}
	if strings.TrimSpace(config.Name) == "" {
		return nil, errors.New("mcp server name is required")
	}
	if strings.TrimSpace(config.Command) == "" {
		return nil, errors.New("mcp command is required")
	}
	config.Name = strings.TrimSpace(config.Name)
	if config.ProtocolVersion == "" {
		config.ProtocolVersion = defaultProtocolVersion
	}
	if config.ClientName == "" {
		config.ClientName = defaultClientName
	}
	if config.ClientVersion == "" {
		config.ClientVersion = defaultClientVersion
	}
	if config.MaxStderrBytes <= 0 {
		config.MaxStderrBytes = defaultStderrLimit
	}
	if config.MaxMessageBytes <= 0 {
		config.MaxMessageBytes = defaultMessageLimit
	}
	return &Client{
		registry:        registry,
		config:          config,
		done:            make(chan struct{}),
		pending:         make(map[int64]chan pendingResponse),
		serverToolNames: make(map[string]string),
		stderr:          boundedStderr{limit: config.MaxStderrBytes},
	}, nil
}

// Start launches the server, performs initialize/initialized, discovers tools,
// and registers them under the mcp.<server>.<tool> namespace.
func (c *Client) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	if c.started || c.finished {
		c.mu.Unlock()
		return errors.New("mcp client has already been started")
	}
	cmd := exec.CommandContext(ctx, c.config.Command, c.config.Args...)
	cmd.Env = mergeEnv(os.Environ(), c.config.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("mcp stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		c.mu.Unlock()
		return fmt.Errorf("mcp stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		c.mu.Unlock()
		return fmt.Errorf("mcp stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		c.mu.Unlock()
		return fmt.Errorf("start mcp server: %w", err)
	}
	c.started = true
	c.cmd = cmd
	c.stdin = stdin
	c.stderr.limit = c.config.MaxStderrBytes
	c.mu.Unlock()

	stderrDone := make(chan struct{})
	go c.readStdout(stdout)
	go c.readStderr(stderr, stderrDone)
	go c.waitProcess(stderrDone)

	initializeResult, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": c.config.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    c.config.ClientName,
			"version": c.config.ClientVersion,
		},
	})
	if err != nil {
		_ = c.Close()
		return err
	}
	if len(initializeResult) == 0 {
		_ = c.Close()
		return &ProtocolError{Message: "initialize response has no result"}
	}
	if err := c.notification("notifications/initialized", nil); err != nil {
		_ = c.Close()
		return err
	}
	listResult, err := c.request(ctx, "tools/list", nil)
	if err != nil {
		_ = c.Close()
		return err
	}
	tools, err := decodeToolsList(listResult)
	if err != nil {
		_ = c.Close()
		return err
	}
	_, err = c.registerTools(tools)
	if err != nil {
		_ = c.Close()
		return err
	}
	return nil
}

type discoveredTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func decodeToolsList(result json.RawMessage) ([]discoveredTool, error) {
	var response struct {
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, &ProtocolError{Message: "tools/list result is invalid", Cause: err}
	}
	if len(response.Tools) == 0 || string(response.Tools) == "null" {
		return nil, &ProtocolError{Message: "tools/list result has no tools array"}
	}
	var tools []discoveredTool
	if err := json.Unmarshal(response.Tools, &tools); err != nil {
		return nil, &ProtocolError{Message: "tools/list result has an invalid tools array", Cause: err}
	}
	for i := range tools {
		if strings.TrimSpace(tools[i].Name) == "" {
			return nil, &ProtocolError{Message: "tools/list contains a tool without a name"}
		}
		if len(tools[i].InputSchema) == 0 {
			tools[i].InputSchema = json.RawMessage(`{"type":"object"}`)
		}
		if !json.Valid(tools[i].InputSchema) {
			return nil, &ProtocolError{Message: fmt.Sprintf("tool %q has invalid inputSchema", tools[i].Name)}
		}
	}
	return tools, nil
}

func (c *Client) registerTools(discovered []discoveredTool) ([]string, error) {
	registered := make([]string, 0, len(discovered))
	serverNames := make(map[string]string, len(discovered))
	for _, discoveredTool := range discovered {
		qualified := c.qualifiedName(discoveredTool.Name)
		original := discoveredTool.Name
		if _, exists := serverNames[qualified]; exists {
			c.registry.Unregister(registered...)
			return nil, fmt.Errorf("duplicate MCP tool name %q", qualified)
		}
		handler := func(ctx context.Context, arguments json.RawMessage) (string, error) {
			return c.callServerTool(ctx, original, arguments)
		}
		if err := c.registry.Register(tool.Tool{
			Name:        qualified,
			Description: discoveredTool.Description,
			Schema:      append(json.RawMessage(nil), discoveredTool.InputSchema...),
			Handler:     handler,
		}); err != nil {
			c.registry.Unregister(registered...)
			return nil, fmt.Errorf("register MCP tool %q: %w", qualified, err)
		}
		registered = append(registered, qualified)
		serverNames[qualified] = original
	}

	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		c.registry.Unregister(registered...)
		return registered, nil
	}
	c.toolNames = append(c.toolNames, registered...)
	for qualified, original := range serverNames {
		c.serverToolNames[qualified] = original
	}
	c.mu.Unlock()
	return registered, nil
}

func (c *Client) qualifiedName(serverTool string) string {
	return "mcp." + url.QueryEscape(c.config.Name) + "." + url.QueryEscape(serverTool)
}

// QualifiedName returns the registry name for a server-provided tool name.
func (c *Client) QualifiedName(serverTool string) string { return c.qualifiedName(serverTool) }

// Call invokes a discovered MCP tool by its registered namespaced name.
func (c *Client) Call(ctx context.Context, qualifiedName string, arguments json.RawMessage) (string, error) {
	c.mu.Lock()
	original, ok := c.serverToolNames[qualifiedName]
	c.mu.Unlock()
	if !ok {
		return "", ErrToolNotFound
	}
	return c.callServerTool(ctx, original, arguments)
}

func (c *Client) callServerTool(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	if !json.Valid(arguments) {
		return "", &ProtocolError{Message: "tools/call arguments are invalid JSON"}
	}
	result, err := c.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return "", err
	}
	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return "", &ProtocolError{Message: "tools/call result is invalid", Cause: err}
	}
	content := make([]string, 0, len(response.Content))
	for _, block := range response.Content {
		if block.Type == "text" {
			content = append(content, block.Text)
		}
	}
	if len(content) == 0 && len(response.StructuredContent) > 0 && string(response.StructuredContent) != "null" {
		content = append(content, string(response.StructuredContent))
	}
	if response.IsError {
		return "", fmt.Errorf("MCP tool %q returned an error: %s", name, strings.Join(content, "\n"))
	}
	return strings.Join(content, "\n"), nil
}

// Stderr returns the captured server stderr, capped by Config.MaxStderrBytes.
func (c *Client) Stderr() string { return c.stderr.String() }

// Wait waits for the server to exit. An unexpected exit is returned as ExitError.
func (c *Client) Wait() error {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return ErrNotStarted
	}
	done := c.done
	c.mu.Unlock()
	<-done
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exitErr
}

// Close terminates the server and removes all of its registered tools.
func (c *Client) Close() error {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return nil
	}
	if c.finished {
		err := c.exitErr
		c.mu.Unlock()
		return err
	}
	c.closing = true
	cmd := c.cmd
	c.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return c.Wait()
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return nil, ErrNotStarted
	}
	if c.finished || c.closing {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.nextID++
	id := c.nextID
	response := make(chan pendingResponse, 1)
	c.pending[id] = response
	c.mu.Unlock()

	if err := c.write(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		c.removePending(id)
		c.terminate(err)
		return nil, err
	}
	select {
	case received := <-response:
		if received.goErr != nil {
			return nil, received.goErr
		}
		if received.err != nil {
			return nil, received.err
		}
		return received.result, nil
	case <-ctx.Done():
		c.removePending(id)
		_ = c.notification("notifications/cancelled", map[string]any{
			"requestId": id,
			"reason":    ctx.Err().Error(),
		})
		return nil, ctx.Err()
	case <-c.done:
		c.removePending(id)
		c.mu.Lock()
		err := c.exitErr
		c.mu.Unlock()
		if err == nil {
			return nil, ErrClosed
		}
		return nil, err
	}
}

func (c *Client) notification(method string, params any) error {
	return c.write(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *Client) write(message rpcRequest) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	stdin := c.stdin
	finished := c.finished
	c.mu.Unlock()
	if stdin == nil || finished {
		return ErrClosed
	}
	n, err := stdin.Write(data)
	if err != nil {
		return fmt.Errorf("write MCP request: %w", err)
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func (c *Client) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) readStdout(stdout io.ReadCloser) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), c.config.MaxMessageBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var envelope rpcEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			c.protocolFailure("invalid JSON line", err)
			return
		}
		if envelope.JSONRPC != "2.0" {
			c.protocolFailure("response has unsupported jsonrpc version", nil)
			return
		}
		if len(envelope.ID) == 0 {
			continue
		}
		if envelope.Method != "" {
			c.protocolFailure("server requests are not supported", nil)
			return
		}
		var id int64
		if err := json.Unmarshal(envelope.ID, &id); err != nil || id <= 0 {
			c.protocolFailure("response id is not a positive integer", err)
			return
		}
		if envelope.Error != nil && len(envelope.Result) != 0 {
			c.protocolFailure("response contains both result and error", nil)
			return
		}
		if envelope.Error == nil && len(envelope.Result) == 0 {
			c.protocolFailure("response contains neither result nor error", nil)
			return
		}
		c.mu.Lock()
		response := c.pending[id]
		c.mu.Unlock()
		if response == nil {
			continue
		}
		response <- pendingResponse{result: append(json.RawMessage(nil), envelope.Result...), err: envelope.Error}
	}
	if err := scanner.Err(); err != nil {
		c.mu.Lock()
		finished := c.finished
		closing := c.closing
		c.mu.Unlock()
		if !finished && !closing {
			c.protocolFailure("stdout line exceeds message limit", err)
		}
	}
}

func (c *Client) readStderr(stderr io.ReadCloser, done chan<- struct{}) {
	defer close(done)
	_, _ = io.Copy(&c.stderr, stderr)
}

func mergeEnv(base, overrides []string) []string {
	result := append([]string(nil), base...)
	positions := make(map[string]int, len(result))
	for index, entry := range result {
		if key, _, ok := strings.Cut(entry, "="); ok {
			positions[key] = index
		}
	}
	for _, entry := range overrides {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if index, exists := positions[key]; exists {
			result[index] = entry
		} else {
			positions[key] = len(result)
			result = append(result, entry)
		}
	}
	return result
}

func (c *Client) waitProcess(stderrDone <-chan struct{}) {
	c.mu.Lock()
	cmd := c.cmd
	c.mu.Unlock()
	waitErr := cmd.Wait()
	<-stderrDone
	c.finish(waitErr)
}

func (c *Client) protocolFailure(message string, cause error) {
	err := &ProtocolError{Message: message, Cause: cause}
	c.terminate(err)
}

func (c *Client) terminate(err error) {
	c.mu.Lock()
	if c.finished || c.closing {
		c.mu.Unlock()
		return
	}
	if c.terminalErr == nil {
		c.terminalErr = err
	}
	cmd := c.cmd
	c.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (c *Client) finish(waitErr error) {
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.finished = true
	var err error
	if c.closing {
		err = nil
	} else if c.terminalErr != nil {
		err = c.terminalErr
	} else {
		err = &ExitError{Err: waitErr, Stderr: c.stderr.String()}
	}
	c.exitErr = err
	names := append([]string(nil), c.toolNames...)
	c.toolNames = nil
	c.serverToolNames = make(map[string]string)
	close(c.done)
	pendingErr := err
	if pendingErr == nil {
		pendingErr = ErrClosed
	}
	for id, response := range c.pending {
		response <- pendingResponse{goErr: pendingErr}
		delete(c.pending, id)
	}
	c.registry.Unregister(names...)
	c.mu.Unlock()
}
