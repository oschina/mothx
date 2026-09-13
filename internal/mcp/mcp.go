package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/imageproc"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/tools"
)

const mcpProtocolVersion = "2025-11-25"

const (
	mcpInitializeTimeout = 15 * time.Second
	mcpListToolsTimeout  = 15 * time.Second
	mcpCallTimeout       = 60 * time.Second
	mcpMaxListPages      = 100
	mcpInboundQueueSize  = 128
	mcpMaxResponseBytes  = 16 << 20

	// mcpMaxProjectedImages caps how many images a single MCP tool result may
	// carry into the conversation. It matches the ACP tool-call projection cap
	// (internal/acp acpToolImageMaxCount) so the Desktop renderer never receives
	// more image blocks than it can project.
	mcpMaxProjectedImages = 4
)

type ServerConfig = config.MCPServer

type Client struct {
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	pending map[string]chan mcpResponse
	mu      sync.Mutex
	wmu     sync.Mutex
	smu     sync.RWMutex
	closed  atomic.Bool
	nextID  int64

	transport  string
	httpClient *http.Client
	httpURL    string
	messageURL string
	headers    map[string]string
	sseCancel  context.CancelFunc
	sessionID  string
	callbacks  Callbacks
	ctx        context.Context
	cancel     context.CancelFunc
	inbound    chan RPCRequest

	// imagePolicy resolves the provider/model aware image preprocessing policy
	// for this connection. It is evaluated on every tool call rather than
	// snapshotted at connect time: the shared Runtime connects MCP servers while
	// building the session, but the registry only receives its provider/model
	// image hint later, when the Agent is built
	// (internal/agent configureRegistryImageHint). Resolving eagerly would pin
	// every connection to the generic policy and lose the provider limits.
	imagePolicy func(imageproc.Mode) imageproc.Policy
}

// imagePolicyFor resolves the image policy for one tool call, falling back to
// the generic defaults when no registry was attached to this connection.
func (c *Client) imagePolicyFor(mode imageproc.Mode) imageproc.Policy {
	if c != nil && c.imagePolicy != nil {
		return c.imagePolicy(mode)
	}
	return imageproc.DefaultPolicy(mode)
}

func (c *Client) currentSessionID() string {
	c.smu.RLock()
	defer c.smu.RUnlock()
	return c.sessionID
}

func (c *Client) setSessionID(sid string) {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return
	}
	c.smu.Lock()
	defer c.smu.Unlock()
	c.sessionID = sid
}

func (c *Client) context() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}

func (c *Client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.context(), cancel)
	return requestCtx, func() {
		stop()
		cancel()
	}
}

func (c *Client) startInboundLoop() {
	c.inbound = make(chan RPCRequest, mcpInboundQueueSize)
	go func() {
		for {
			select {
			case <-c.context().Done():
				return
			case msg := <-c.inbound:
				c.handleInboundRequest(msg)
			}
		}
	}()
}

func (c *Client) dispatchInboundRequest(msg RPCRequest) {
	if c.inbound == nil {
		c.handleInboundRequest(msg)
		return
	}
	select {
	case <-c.context().Done():
	case c.inbound <- msg:
	default:
		if len(msg.ID) > 0 {
			go func() {
				_ = c.writeMessage(map[string]any{
					"jsonrpc": "2.0",
					"id":      msg.ID,
					"error": &RPCError{
						Code:    -32000,
						Message: "MCP inbound request queue is full",
					},
				})
			}()
		}
	}
}

type Callbacks struct {
	OnNotification          func(serverName, method string, params json.RawMessage)
	OnSamplingCreateMessage func(ctx context.Context, serverName string, params json.RawMessage) (json.RawMessage, *RPCError)
}

type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type mcpResponse struct {
	Result json.RawMessage
	Error  *RPCError
}

type mcpToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type mcpListToolsResult struct {
	Tools      []mcpToolInfo `json:"tools"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

type mcpCallToolResult struct {
	Content []mcpContentBlock `json:"content,omitempty"`
	IsError bool              `json:"isError,omitempty"`
}

type mcpResourceInfo struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type mcpListResourcesResult struct {
	Resources  []mcpResourceInfo `json:"resources"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

type mcpResourceReadResult struct {
	Contents []mcpContentBlock `json:"contents,omitempty"`
}

type mcpPromptInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type mcpListPromptsResult struct {
	Prompts    []mcpPromptInfo `json:"prompts"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type mcpPromptGetResult struct {
	Description string            `json:"description,omitempty"`
	Messages    []mcpPromptSample `json:"messages,omitempty"`
}

type mcpPromptSample struct {
	Role    string          `json:"role"`
	Content mcpContentBlock `json:"content"`
}

type mcpContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`
	Blob     string          `json:"blob,omitempty"`
	URI      string          `json:"uri,omitempty"`
	MimeType string          `json:"mimeType,omitempty"`
	JSON     json.RawMessage `json:"json,omitempty"`
}

func ConnectServers(ctx context.Context, configs []ServerConfig, registry *tools.Registry, callbacks Callbacks) ([]*Client, error) {
	var clients []*Client
	seenServers := make(map[string]struct{})
	registeredToolNames := make(map[string]struct{})
	for _, t := range registry.All() {
		registeredToolNames[t.Name()] = struct{}{}
	}
	for _, cfg := range configs {
		trimmedName := strings.TrimSpace(cfg.Name)
		if _, ok := seenServers[trimmedName]; ok {
			CloseClients(clients)
			return nil, fmt.Errorf("duplicate MCP server name %q", cfg.Name)
		}
		seenServers[trimmedName] = struct{}{}
		client, err := newMCPClient(ctx, cfg, callbacks)
		if err != nil {
			CloseClients(clients)
			return nil, err
		}
		// Bind the image policy lazily through the registry method value. The
		// registry receives its provider/model image hint when the Agent is
		// built, which happens after MCP connection, so the policy must be
		// resolved per tool call rather than captured here.
		if registry != nil {
			client.imagePolicy = registry.ImagePolicy
		}
		clients = append(clients, client)
		toolInfos, err := client.listTools(ctx)
		if err != nil {
			CloseClients(clients)
			return nil, err
		}
		for _, info := range toolInfos {
			if strings.TrimSpace(info.Name) == "" {
				continue
			}
			tool := newMCPTool(client, info, registeredToolNames)
			registeredToolNames[tool.Name()] = struct{}{}
			registry.Register(tool)
		}
		resourceInfos, err := client.listResources(ctx)
		if err != nil && !isMCPMethodNotFound(err) {
			CloseClients(clients)
			return nil, fmt.Errorf("list MCP resources for %q: %w", client.name, err)
		}
		if err == nil {
			for _, info := range resourceInfos {
				if strings.TrimSpace(info.URI) == "" {
					continue
				}
				tool := newMCPResourceTool(client, info, registeredToolNames)
				registeredToolNames[tool.Name()] = struct{}{}
				registry.Register(tool)
			}
		}
		promptInfos, err := client.listPrompts(ctx)
		if err != nil && !isMCPMethodNotFound(err) {
			CloseClients(clients)
			return nil, fmt.Errorf("list MCP prompts for %q: %w", client.name, err)
		}
		if err == nil {
			for _, info := range promptInfos {
				if strings.TrimSpace(info.Name) == "" {
					continue
				}
				tool := newMCPPromptTool(client, info, registeredToolNames)
				registeredToolNames[tool.Name()] = struct{}{}
				registry.Register(tool)
			}
		}
	}
	return clients, nil
}

func isMCPMethodNotFound(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == -32601
}

func CloseClients(clients []*Client) {
	for _, client := range clients {
		client.Close()
	}
}

func newMCPClient(ctx context.Context, cfg ServerConfig, callbacks Callbacks) (*Client, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("MCP server name is required")
	}
	transport := strings.TrimSpace(cfg.Type)
	if transport == "" {
		transport = "stdio"
	}
	switch transport {
	case "stdio":
		return newMCPStdioClient(ctx, cfg, callbacks)
	case "http":
		return newMCPHTTPClient(ctx, cfg, false, callbacks)
	case "sse":
		return newMCPHTTPClient(ctx, cfg, true, callbacks)
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q for server %q", cfg.Type, cfg.Name)
	}
}

func newMCPStdioClient(ctx context.Context, cfg ServerConfig, callbacks Callbacks) (*Client, error) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return nil, fmt.Errorf("MCP server %q command is required", cfg.Name)
	}
	env, err := mergeMCPEnvironment(cfg.Env)
	if err != nil {
		return nil, fmt.Errorf("MCP server %q environment: %w", cfg.Name, err)
	}
	resolvedCommand, err := resolveMCPCommand(command, env)
	if err != nil {
		return nil, fmt.Errorf("resolve MCP server %q command %q: %w", cfg.Name, command, err)
	}

	clientCtx, clientCancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(clientCtx, resolvedCommand, cfg.Args...)
	cmd.Env = env
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		clientCancel()
		return nil, fmt.Errorf("open MCP stdin for %q: %w", cfg.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		clientCancel()
		return nil, fmt.Errorf("open MCP stdout for %q: %w", cfg.Name, err)
	}
	if err := cmd.Start(); err != nil {
		clientCancel()
		return nil, fmt.Errorf("start MCP server %q: %w", cfg.Name, err)
	}

	client := &Client{
		name:      cfg.Name,
		cmd:       cmd,
		stdin:     stdin,
		pending:   make(map[string]chan mcpResponse),
		transport: "stdio",
		callbacks: callbacks,
		ctx:       clientCtx,
		cancel:    clientCancel,
	}
	client.startInboundLoop()
	go client.readLoop(stdout)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				client.closePending(fmt.Errorf("MCP server %q wait panic: %v", cfg.Name, r))
			}
		}()
		_ = cmd.Wait()
		client.closePending(fmt.Errorf("MCP server %q exited", cfg.Name))
	}()

	initCtx, cancel := context.WithTimeout(ctx, mcpInitializeTimeout)
	defer cancel()
	if _, err := client.call(initCtx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "vibecoding",
			"title":   "VibeCoding",
			"version": "dev",
		},
	}); err != nil {
		client.Close()
		return nil, fmt.Errorf("initialize MCP server %q: %w", cfg.Name, err)
	}
	if err := client.notify("notifications/initialized", nil); err != nil {
		client.Close()
		return nil, fmt.Errorf("initialize MCP server %q: %w", cfg.Name, err)
	}
	return client, nil
}

func mergeMCPEnvironment(overrides []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}) ([]string, error) {
	env := append([]string(nil), os.Environ()...)
	indices := make(map[string]int, len(env))
	for i, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok {
			// Keep the last inherited value because execve uses the last
			// duplicate environment entry on Unix-like systems.
			indices[normalizeMCPEnvName(name)] = i
		}
	}
	for _, override := range overrides {
		name := strings.TrimSpace(override.Name)
		if name == "" {
			return nil, errors.New("environment variable name is empty")
		}
		if strings.ContainsAny(name, "=\x00") || strings.ContainsRune(override.Value, 0) {
			return nil, fmt.Errorf("invalid environment variable %q", name)
		}
		entry := name + "=" + override.Value
		key := normalizeMCPEnvName(name)
		if index, ok := indices[key]; ok {
			env[index] = entry
		} else {
			indices[key] = len(env)
			env = append(env, entry)
		}
	}
	return env, nil
}

func normalizeMCPEnvName(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func mcpEnvValue(env []string, name string) string {
	key := normalizeMCPEnvName(name)
	value := ""
	for _, entry := range env {
		entryName, currentValue, ok := strings.Cut(entry, "=")
		if ok && normalizeMCPEnvName(entryName) == key {
			value = currentValue
		}
	}
	return value
}

func resolveMCPCommand(command string, env []string) (string, error) {
	if strings.ContainsRune(command, 0) {
		return "", fmt.Errorf("command contains NUL byte")
	}
	if strings.ContainsRune(command, os.PathSeparator) || (runtime.GOOS == "windows" && strings.ContainsAny(command, `/\\`)) {
		if err := checkMCPExecutable(command); err != nil {
			return "", &exec.Error{Name: command, Err: err}
		}
		return command, nil
	}

	pathValue := mcpEnvValue(env, "PATH")
	for _, dir := range filepath.SplitList(pathValue) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, command)
		for _, path := range mcpCommandCandidates(candidate, env) {
			if err := checkMCPExecutable(path); err == nil {
				return path, nil
			}
		}
	}
	return "", &exec.Error{Name: command, Err: exec.ErrNotFound}
}

func mcpCommandCandidates(candidate string, env []string) []string {
	if runtime.GOOS != "windows" || filepath.Ext(candidate) != "" {
		return []string{candidate}
	}
	extensions := filepath.SplitList(strings.ReplaceAll(mcpEnvValue(env, "PATHEXT"), ";", string(os.PathListSeparator)))
	if len(extensions) == 0 {
		extensions = []string{".COM", ".EXE", ".BAT", ".CMD"}
	}
	paths := make([]string, 0, len(extensions))
	for _, ext := range extensions {
		if ext != "" {
			paths = append(paths, candidate+ext)
		}
	}
	return paths
}

func checkMCPExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory")
	}
	if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
		return os.ErrPermission
	}
	return nil
}

func newMCPHTTPClient(ctx context.Context, cfg ServerConfig, legacySSE bool, callbacks Callbacks) (*Client, error) {
	rawURL := strings.TrimSpace(cfg.URL)
	if rawURL == "" {
		return nil, fmt.Errorf("MCP server %q url is required for %s transport", cfg.Name, cfg.Type)
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return nil, fmt.Errorf("MCP server %q url must be a valid http(s) URL", cfg.Name)
	}

	headers := map[string]string{}
	for _, h := range cfg.Headers {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			continue
		}
		headers[name] = h.Value
	}
	client := &Client{
		name:       cfg.Name,
		pending:    make(map[string]chan mcpResponse),
		transport:  cfg.Type,
		httpClient: &http.Client{},
		httpURL:    rawURL,
		headers:    headers,
		callbacks:  callbacks,
	}
	client.ctx, client.cancel = context.WithCancel(ctx)
	client.startInboundLoop()
	if legacySSE {
		msgURL := strings.TrimSpace(cfg.MessageURL)
		if msgURL == "" {
			client.cancel()
			return nil, fmt.Errorf("MCP server %q messageUrl is required for sse transport", cfg.Name)
		}
		parsedMessageURL, err := url.Parse(msgURL)
		if err != nil || (parsedMessageURL.Scheme != "http" && parsedMessageURL.Scheme != "https") {
			client.cancel()
			return nil, fmt.Errorf("MCP server %q messageUrl must be a valid http(s) URL", cfg.Name)
		}
		client.messageURL = msgURL
		client.sseCancel = client.cancel
		go client.readSSELoop(client.ctx, rawURL)
	}

	initCtx, cancel := context.WithTimeout(ctx, mcpInitializeTimeout)
	defer cancel()
	if _, err := client.call(initCtx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "vibecoding",
			"title":   "VibeCoding",
			"version": "dev",
		},
	}); err != nil {
		client.Close()
		return nil, fmt.Errorf("initialize MCP server %q: %w", cfg.Name, err)
	}
	if err := client.notify("notifications/initialized", nil); err != nil {
		client.Close()
		return nil, fmt.Errorf("initialize MCP server %q: %w", cfg.Name, err)
	}
	return client, nil
}

func (c *Client) listTools(ctx context.Context) ([]mcpToolInfo, error) {
	listCtx, cancel := context.WithTimeout(ctx, mcpListToolsTimeout)
	defer cancel()

	var all []mcpToolInfo
	cursor := ""
	for page := 0; page < mcpMaxListPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := c.call(listCtx, "tools/list", params)
		if err != nil {
			return nil, fmt.Errorf("list MCP tools for %q: %w", c.name, err)
		}
		var out mcpListToolsResult
		if err := json.Unmarshal(result, &out); err != nil {
			return nil, fmt.Errorf("decode MCP tools for %q: %w", c.name, err)
		}
		all = append(all, out.Tools...)
		if out.NextCursor == "" {
			return all, nil
		}
		cursor = out.NextCursor
	}
	return nil, fmt.Errorf("list MCP tools for %q: too many pages", c.name)
}

func (c *Client) callTool(ctx context.Context, name string, args map[string]any) (mcpCallToolResult, error) {
	result, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return mcpCallToolResult{}, err
	}
	var out mcpCallToolResult
	if err := json.Unmarshal(result, &out); err != nil {
		return mcpCallToolResult{}, err
	}
	if out.IsError {
		return out, fmt.Errorf("%s", mcpContentToText(out.Content))
	}
	return out, nil
}

func (c *Client) listResources(ctx context.Context) ([]mcpResourceInfo, error) {
	listCtx, cancel := context.WithTimeout(ctx, mcpListToolsTimeout)
	defer cancel()

	var all []mcpResourceInfo
	cursor := ""
	for page := 0; page < mcpMaxListPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := c.call(listCtx, "resources/list", params)
		if err != nil {
			return nil, err
		}
		var out mcpListResourcesResult
		if err := json.Unmarshal(result, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Resources...)
		if out.NextCursor == "" {
			return all, nil
		}
		cursor = out.NextCursor
	}
	return nil, fmt.Errorf("list MCP resources for %q: too many pages", c.name)
}

func (c *Client) readResource(ctx context.Context, uri string) (mcpResourceReadResult, error) {
	result, err := c.call(ctx, "resources/read", map[string]any{"uri": uri})
	if err != nil {
		return mcpResourceReadResult{}, err
	}
	var out mcpResourceReadResult
	if err := json.Unmarshal(result, &out); err != nil {
		return mcpResourceReadResult{}, err
	}
	return out, nil
}

func (c *Client) listPrompts(ctx context.Context) ([]mcpPromptInfo, error) {
	listCtx, cancel := context.WithTimeout(ctx, mcpListToolsTimeout)
	defer cancel()

	var all []mcpPromptInfo
	cursor := ""
	for page := 0; page < mcpMaxListPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := c.call(listCtx, "prompts/list", params)
		if err != nil {
			return nil, err
		}
		var out mcpListPromptsResult
		if err := json.Unmarshal(result, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Prompts...)
		if out.NextCursor == "" {
			return all, nil
		}
		cursor = out.NextCursor
	}
	return nil, fmt.Errorf("list MCP prompts for %q: too many pages", c.name)
}

func (c *Client) getPrompt(ctx context.Context, name string, args map[string]any) (mcpPromptGetResult, error) {
	params := map[string]any{"name": name}
	if len(args) > 0 {
		params["arguments"] = args
	}
	result, err := c.call(ctx, "prompts/get", params)
	if err != nil {
		return mcpPromptGetResult{}, err
	}
	var out mcpPromptGetResult
	if err := json.Unmarshal(result, &out); err != nil {
		return mcpPromptGetResult{}, err
	}
	return out, nil
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.transport == "http" {
		return c.callHTTP(ctx, method, params)
	}
	if c.transport == "sse" {
		return c.callSSE(ctx, method, params)
	}
	id := atomic.AddInt64(&c.nextID, 1)
	key := fmt.Sprintf("%d", id)
	ch := make(chan mcpResponse, 1)

	c.mu.Lock()
	c.pending[key] = ch
	c.mu.Unlock()

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	if err := c.writeMessage(msg); err != nil {
		c.removePending(key)
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.removePending(key)
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

func (c *Client) callSSE(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	key := fmt.Sprintf("%d", id)
	ch := make(chan mcpResponse, 1)
	c.mu.Lock()
	c.pending[key] = ch
	c.mu.Unlock()

	result, err := c.callHTTPInternal(ctx, method, params, false, &id)
	if err != nil {
		c.removePending(key)
		return nil, err
	}
	if len(result) > 0 && string(result) != "{}" {
		c.removePending(key)
		return result, nil
	}
	select {
	case <-ctx.Done():
		c.removePending(key)
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

func (c *Client) notify(method string, params any) error {
	if c.transport == "http" || c.transport == "sse" {
		ctx, cancel := context.WithTimeout(context.Background(), mcpCallTimeout)
		defer cancel()
		_, err := c.callHTTPInternal(ctx, method, params, true, nil)
		return err
	}
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	return c.writeMessage(msg)
}

func (c *Client) callHTTP(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.callHTTPInternal(ctx, method, params, false, nil)
}

func (c *Client) callHTTPInternal(ctx context.Context, method string, params any, isNotification bool, reqID *int64) (json.RawMessage, error) {
	requestCtx, requestCancel := c.requestContext(ctx)
	defer requestCancel()
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	var id int64
	if !isNotification {
		if reqID != nil {
			id = *reqID
		} else {
			id = atomic.AddInt64(&c.nextID, 1)
		}
		msg["id"] = id
	}
	if params != nil {
		msg["params"] = params
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	target := c.httpURL
	if c.transport == "sse" {
		target = c.messageURL
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if operationID, ok := tools.OperationIDFromContext(requestCtx); ok {
		// MCP does not standardize an idempotency field, but HTTP MCP servers
		// can opt into the common header without changing JSON-RPC semantics.
		req.Header.Set("Idempotency-Key", operationID)
	}
	if sid := c.currentSessionID(); sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if sid := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); sid != "" {
		c.setSessionID(sid)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if isNotification || resp.StatusCode == http.StatusAccepted || resp.ContentLength == 0 {
		return json.RawMessage(`{}`), nil
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/event-stream") {
		return parseSSECallResponse(resp.Body, id)
	}
	var rpcResp RPCRequest
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, mcpMaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(responseBody) > mcpMaxResponseBytes {
		return nil, fmt.Errorf("MCP response exceeds %d bytes", mcpMaxResponseBytes)
	}
	if err := json.Unmarshal(responseBody, &rpcResp); err != nil {
		return nil, err
	}
	if rpcResp.JSONRPC != "2.0" {
		return nil, fmt.Errorf("invalid JSON-RPC version %q", rpcResp.JSONRPC)
	}
	if len(rpcResp.ID) == 0 || rpcResponseIDKey(rpcResp.ID) != fmt.Sprintf("%d", id) {
		return nil, fmt.Errorf("JSON-RPC response id %q does not match request id %d", string(rpcResp.ID), id)
	}
	if len(rpcResp.Error) > 0 {
		var rpcErr RPCError
		if err := json.Unmarshal(rpcResp.Error, &rpcErr); err == nil {
			return nil, &rpcErr
		}
		return nil, fmt.Errorf("%s", string(rpcResp.Error))
	}
	return rpcResp.Result, nil
}

func parseSSECallResponse(r io.Reader, expectID int64) (json.RawMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var payload strings.Builder
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			if payload.Len() > 0 {
				payload.WriteByte('\n')
			}
			payload.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if line == "" && payload.Len() > 0 {
			var rpcResp RPCRequest
			if err := json.Unmarshal([]byte(payload.String()), &rpcResp); err == nil {
				if rpcResp.JSONRPC == "2.0" && rpcResponseIDKey(rpcResp.ID) == fmt.Sprintf("%d", expectID) {
					if len(rpcResp.Error) > 0 {
						var rpcErr RPCError
						if err := json.Unmarshal(rpcResp.Error, &rpcErr); err == nil {
							return nil, &rpcErr
						}
						return nil, fmt.Errorf("%s", string(rpcResp.Error))
					}
					return rpcResp.Result, nil
				}
			}
			payload.Reset()
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("no RPC response found in SSE stream")
}

func (c *Client) writeMessage(msg any) error {
	if c.closed.Load() {
		return errors.New("MCP client is closed")
	}
	if c.transport == "http" || c.transport == "sse" {
		ctx, cancel := context.WithTimeout(c.context(), mcpCallTimeout)
		defer cancel()
		return c.postRPCMessage(ctx, msg)
	}
	if c.stdin == nil {
		return errors.New("MCP stdin is not available")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.stdin.Write(data); err != nil {
		return err
	}
	_, err = c.stdin.Write([]byte("\n"))
	return err
}

func (c *Client) postRPCMessage(ctx context.Context, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	target := c.httpURL
	if c.transport == "sse" && c.messageURL != "" {
		target = c.messageURL
	}
	requestCtx, requestCancel := c.requestContext(ctx)
	defer requestCancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if sid := c.currentSessionID(); sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if sid := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); sid != "" {
		c.setSessionID(sid)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Client) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var msg RPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if len(msg.Method) > 0 {
			c.dispatchInboundRequest(msg)
			continue
		}
		if len(msg.ID) == 0 {
			continue
		}
		key := rpcResponseIDKey(msg.ID)
		c.mu.Lock()
		ch, ok := c.pending[key]
		if ok {
			delete(c.pending, key)
		}
		c.mu.Unlock()
		if ok {
			resp := mcpResponse{Result: msg.Result}
			if len(msg.Error) > 0 {
				var rpcErr RPCError
				if err := json.Unmarshal(msg.Error, &rpcErr); err == nil {
					resp.Error = &rpcErr
				} else {
					resp.Error = &RPCError{Code: -32000, Message: string(msg.Error)}
				}
			}
			ch <- resp
		}
	}
	if err := scanner.Err(); err != nil {
		c.closePending(fmt.Errorf("MCP server %q output error: %v", c.name, err))
		return
	}
	c.closePending(fmt.Errorf("MCP server %q output closed", c.name))
}

func (c *Client) readSSELoop(ctx context.Context, streamURL string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		c.closePending(fmt.Errorf("MCP server %q sse request: %v", c.name, err))
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.closePending(fmt.Errorf("MCP server %q sse connect: %v", c.name, err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		c.closePending(fmt.Errorf("MCP server %q sse HTTP %d: %s", c.name, resp.StatusCode, strings.TrimSpace(string(data))))
		return
	}
	if sid := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); sid != "" {
		c.setSessionID(sid)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var dataLines []string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		if line != "" {
			continue
		}
		if len(dataLines) == 0 {
			continue
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		var msg RPCRequest
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			continue
		}
		if len(msg.Method) > 0 {
			c.dispatchInboundRequest(msg)
			continue
		}
		if len(msg.ID) == 0 {
			continue
		}
		key := rpcResponseIDKey(msg.ID)
		c.mu.Lock()
		ch, ok := c.pending[key]
		if ok {
			delete(c.pending, key)
		}
		c.mu.Unlock()
		if !ok {
			continue
		}
		respMsg := mcpResponse{Result: msg.Result}
		if len(msg.Error) > 0 {
			var rpcErr RPCError
			if err := json.Unmarshal(msg.Error, &rpcErr); err == nil {
				respMsg.Error = &rpcErr
			} else {
				respMsg.Error = &RPCError{Code: -32000, Message: string(msg.Error)}
			}
		}
		ch <- respMsg
	}
	if err := sc.Err(); err != nil {
		c.closePending(fmt.Errorf("MCP server %q sse stream error: %v", c.name, err))
		return
	}
	c.closePending(fmt.Errorf("MCP server %q sse stream closed", c.name))
}

func (c *Client) removePending(key string) {
	c.mu.Lock()
	delete(c.pending, key)
	c.mu.Unlock()
}

func (c *Client) closePending(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[string]chan mcpResponse)
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- mcpResponse{Error: &RPCError{Code: -32000, Message: err.Error()}}
	}
}

func (c *Client) Close() {
	if !c.closed.CompareAndSwap(false, true) {
		return
	}
	if c.cancel != nil {
		c.cancel()
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	c.closePending(fmt.Errorf("MCP client %q closed", c.name))
	if c.sseCancel != nil {
		c.sseCancel()
	}
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

func RawIDKey(id json.RawMessage) string {
	return strings.Trim(string(id), "\"")
}

func rpcResponseIDKey(id json.RawMessage) string {
	return string(bytes.TrimSpace(id))
}

type mcpTool struct {
	client *Client
	info   mcpToolInfo
	name   string
}

type mcpResourceTool struct {
	client *Client
	info   mcpResourceInfo
	name   string
}

type mcpPromptTool struct {
	client *Client
	info   mcpPromptInfo
	name   string
}

func newMCPTool(client *Client, info mcpToolInfo, existing map[string]struct{}) tools.Tool {
	base := "mcp_" + SanitizeToolName(client.name) + "_" + SanitizeToolName(info.Name)
	name := uniqueToolName(base, existing)
	return &mcpTool{
		client: client,
		info:   info,
		name:   name,
	}
}

func (t *mcpTool) Name() string {
	return t.name
}

func (t *mcpTool) Description() string {
	if t.info.Description != "" {
		return t.info.Description
	}
	return "Tool provided by MCP server " + t.client.name
}

func (t *mcpTool) PromptSnippet() string {
	return fmt.Sprintf("%s: MCP tool %q from server %q", t.name, t.info.Name, t.client.name)
}

func (t *mcpTool) PromptGuidelines() []string {
	return nil
}

func (t *mcpTool) Parameters() json.RawMessage {
	if len(t.info.InputSchema) == 0 {
		return json.RawMessage(`{"type":"object"}`)
	}
	return t.info.InputSchema
}

func (t *mcpTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	result, err := t.client.callTool(ctx, t.info.Name, params)
	text, contents := t.client.projectMCPContent(result.Content)
	if text == "" && err != nil {
		text = err.Error()
	}
	if err != nil {
		// The agent loop replaces the result contents with the error text, so
		// projecting images here would only waste decoding work.
		return tools.NewTextToolResult(text), err
	}
	if len(contents) == 0 {
		return tools.NewTextToolResult(text), nil
	}
	return tools.ToolResult{Text: text, Contents: contents}, nil
}

func newMCPResourceTool(client *Client, info mcpResourceInfo, existing map[string]struct{}) tools.Tool {
	id := info.Name
	if strings.TrimSpace(id) == "" {
		id = info.URI
	}
	base := "mcp_" + SanitizeToolName(client.name) + "_resource_" + SanitizeToolName(id)
	return &mcpResourceTool{
		client: client,
		info:   info,
		name:   uniqueToolName(base, existing),
	}
}

func (t *mcpResourceTool) Name() string { return t.name }
func (t *mcpResourceTool) Description() string {
	if strings.TrimSpace(t.info.Description) != "" {
		return t.info.Description
	}
	return "Read MCP resource " + t.info.URI + " from server " + t.client.name
}
func (t *mcpResourceTool) PromptSnippet() string {
	return fmt.Sprintf("%s: MCP resource reader for %q on %q", t.name, t.info.URI, t.client.name)
}
func (t *mcpResourceTool) PromptGuidelines() []string { return nil }
func (t *mcpResourceTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"uri":{"type":"string","description":"Override resource URI (optional)."}}}`)
}
func (t *mcpResourceTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	uri := t.info.URI
	if v, ok := params["uri"].(string); ok && strings.TrimSpace(v) != "" {
		uri = v
	}
	out, err := t.client.readResource(ctx, uri)
	text, contents := t.client.projectMCPContent(out.Contents)
	if text == "" && err != nil {
		text = err.Error()
	}
	if err != nil {
		return tools.NewTextToolResult(text), err
	}
	if len(contents) == 0 {
		return tools.NewTextToolResult(text), nil
	}
	return tools.ToolResult{Text: text, Contents: contents}, nil
}

func newMCPPromptTool(client *Client, info mcpPromptInfo, existing map[string]struct{}) tools.Tool {
	base := "mcp_" + SanitizeToolName(client.name) + "_prompt_" + SanitizeToolName(info.Name)
	return &mcpPromptTool{
		client: client,
		info:   info,
		name:   uniqueToolName(base, existing),
	}
}

func (t *mcpPromptTool) Name() string { return t.name }
func (t *mcpPromptTool) Description() string {
	if strings.TrimSpace(t.info.Description) != "" {
		return t.info.Description
	}
	return "Render MCP prompt " + t.info.Name + " from server " + t.client.name
}
func (t *mcpPromptTool) PromptSnippet() string {
	return fmt.Sprintf("%s: MCP prompt %q from server %q", t.name, t.info.Name, t.client.name)
}
func (t *mcpPromptTool) PromptGuidelines() []string { return nil }
func (t *mcpPromptTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":true,"description":"Arguments passed to prompts/get."}`)
}
func (t *mcpPromptTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	out, err := t.client.getPrompt(ctx, t.info.Name, params)
	var parts []string
	if strings.TrimSpace(out.Description) != "" {
		parts = append(parts, out.Description)
	}
	for _, msg := range out.Messages {
		content := mcpContentToText([]mcpContentBlock{msg.Content})
		if strings.TrimSpace(content) == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s]\n%s", msg.Role, content))
	}
	text := strings.Join(parts, "\n\n")
	if text == "" && err != nil {
		text = err.Error()
	}
	return tools.NewTextToolResult(text), err
}

func SanitizeToolName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "tool"
	}
	return out
}

func mcpContentToText(blocks []mcpContentBlock) string {
	var parts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case "image", "audio":
			parts = append(parts, fmt.Sprintf("[%s content: %s]", block.Type, block.MimeType))
		default:
			if block.Type == "json" && len(block.JSON) > 0 {
				parts = append(parts, string(block.JSON))
				continue
			}
			data, _ := json.Marshal(block)
			if len(data) > 0 {
				parts = append(parts, string(data))
			}
		}
	}
	return strings.Join(parts, "\n")
}

// classifyMCPBlock normalizes the two MCP content spellings. tools/call
// results carry an explicit type, while resources/read contents omit "type"
// entirely and distinguish text from binary through "text" versus "blob"
// (MCP BlobResourceContents).
func classifyMCPBlock(block mcpContentBlock) (kind, payload, mimeType string) {
	switch block.Type {
	case "text", "json", "audio":
		return block.Type, "", block.MimeType
	case "image":
		return "image", block.Data, block.MimeType
	case "":
		switch {
		case block.Blob != "":
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(block.MimeType)), "image/") {
				return "image", block.Blob, block.MimeType
			}
			return "blob", "", block.MimeType
		case block.Data != "":
			return "image", block.Data, block.MimeType
		default:
			return "text", "", block.MimeType
		}
	default:
		return block.Type, "", block.MimeType
	}
}

// projectMCPContent converts MCP content blocks into a human-readable text
// summary plus rich content blocks for the model. Text and json blocks keep
// their existing text rendering. Image blocks become real provider image
// content so a vision model sees the pixels instead of a placeholder.
//
// A tool result without images keeps the historical text-only shape (nil
// contents), so existing MCP text tools are byte-for-byte unchanged.
func (c *Client) projectMCPContent(blocks []mcpContentBlock) (string, []provider.ContentBlock) {
	var parts []string
	var images []provider.ContentBlock

	for _, block := range blocks {
		kind, payload, mimeType := classifyMCPBlock(block)
		switch kind {
		case "text":
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case "json":
			if len(block.JSON) > 0 {
				parts = append(parts, string(block.JSON))
			}
		case "image":
			image, note := c.projectMCPImage(payload, mimeType, len(images))
			if image != nil {
				images = append(images, provider.ContentBlock{Type: "image", Image: image})
			}
			if note != "" {
				parts = append(parts, note)
			}
		case "audio":
			// No provider converter or provider.ContentBlock variant carries
			// audio, so keep the historical placeholder instead of dropping it.
			parts = append(parts, fmt.Sprintf("[%s content: %s]", block.Type, mimeType))
		case "blob":
			parts = append(parts, fmt.Sprintf("[binary content: %s]", mimeType))
		default:
			data, err := json.Marshal(block)
			if err == nil && len(data) > 0 {
				parts = append(parts, string(data))
			}
		}
	}

	text := strings.Join(parts, "\n")
	if len(images) == 0 {
		return text, nil
	}
	contents := make([]provider.ContentBlock, 0, len(images)+1)
	if text != "" {
		contents = append(contents, provider.ContentBlock{Type: "text", Text: text})
	}
	return text, append(contents, images...)
}

// projectMCPImage decodes one image payload and applies the same
// provider-aware preprocessing the read and browser screenshot tools use. It
// returns the usable image plus the text note to emit. A malformed, oversized
// or excess image degrades to a note instead of failing the tool call: one bad
// payload must not invalidate an otherwise successful MCP call.
func (c *Client) projectMCPImage(payload, mimeType string, already int) (*provider.ImageContent, string) {
	label := strings.TrimSpace(mimeType)
	if label == "" {
		label = "unknown"
	}
	if strings.TrimSpace(payload) == "" {
		return nil, fmt.Sprintf("[Image %s omitted: empty payload]", label)
	}
	if already >= mcpMaxProjectedImages {
		return nil, fmt.Sprintf("[Image %s omitted: at most %d images per tool result]", label, mcpMaxProjectedImages)
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Sprintf("[Image %s omitted: invalid base64 payload]", label)
	}
	result, err := imageproc.PrepareBytes(raw, c.imagePolicyFor(imageproc.ModeAuto))
	if err != nil {
		return nil, fmt.Sprintf("[Image %s omitted: %v]", label, err)
	}
	image := &provider.ImageContent{
		Data:           base64.StdEncoding.EncodeToString(result.Data),
		MimeType:       result.MimeType,
		Width:          result.Meta.Width,
		Height:         result.Meta.Height,
		Bytes:          result.Meta.Bytes,
		OriginalWidth:  result.Meta.OriginalWidth,
		OriginalHeight: result.Meta.OriginalHeight,
		OriginalBytes:  result.Meta.OriginalBytes,
		Detail:         result.Meta.Detail,
		Scale:          result.Meta.Scale,
		Cropped:        result.Meta.Cropped,
		CropX:          result.Meta.CropX,
		CropY:          result.Meta.CropY,
		CropWidth:      result.Meta.CropWidth,
		CropHeight:     result.Meta.CropHeight,
	}
	return image, fmt.Sprintf("[Image: %s %dx%d, %s]", result.MimeType, result.Meta.Width, result.Meta.Height, mcpFormatBytes(result.Meta.Bytes))
}

func mcpFormatBytes(n int) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	kb := float64(n) / unit
	if kb < unit {
		return fmt.Sprintf("%.1fKB", kb)
	}
	return fmt.Sprintf("%.1fMB", kb/unit)
}

func uniqueToolName(base string, existing map[string]struct{}) string {
	if _, ok := existing[base]; !ok {
		return base
	}
	for i := 2; i < 1_000_000; i++ {
		candidate := fmt.Sprintf("%s_%d", base, i)
		if _, ok := existing[candidate]; !ok {
			return candidate
		}
	}
	return fmt.Sprintf("%s_%d", base, time.Now().UnixNano())
}

func extractSamplingPrompt(params json.RawMessage) string {
	var req struct {
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return ""
	}

	var parts []string
	for _, msg := range req.Messages {
		switch content := msg.Content.(type) {
		case string:
			if strings.TrimSpace(content) != "" {
				parts = append(parts, content)
			}
		case []any:
			for _, item := range content {
				block, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if blockType, _ := block["type"].(string); blockType != "" && blockType != "text" {
					continue
				}
				text, _ := block["text"].(string)
				if strings.TrimSpace(text) != "" {
					parts = append(parts, text)
				}
			}
		case map[string]any:
			text, _ := content["text"].(string)
			if strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func (c *Client) handleInboundRequest(msg RPCRequest) {
	if len(msg.ID) == 0 {
		c.handleInboundNotification(msg)
		return
	}
	switch msg.Method {
	case "ping":
		_ = c.writeMessage(map[string]any{
			"jsonrpc": "2.0",
			"id":      msg.ID,
			"result":  map[string]any{},
		})
	case "sampling/createMessage":
		if c.callbacks.OnSamplingCreateMessage != nil {
			result, rpcErr := c.callbacks.OnSamplingCreateMessage(c.context(), c.name, msg.Params)
			if rpcErr != nil {
				_ = c.writeMessage(map[string]any{
					"jsonrpc": "2.0",
					"id":      msg.ID,
					"error":   rpcErr,
				})
				return
			}
			var anyResult any = map[string]any{}
			if len(result) > 0 {
				_ = json.Unmarshal(result, &anyResult)
			}
			_ = c.writeMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"result":  anyResult,
			})
			return
		}
		_ = c.writeMessage(map[string]any{
			"jsonrpc": "2.0",
			"id":      msg.ID,
			"error": map[string]any{
				"code":    -32601,
				"message": "sampling/createMessage is not enabled in this ACP runtime yet",
			},
		})
	default:
		_ = c.writeMessage(map[string]any{
			"jsonrpc": "2.0",
			"id":      msg.ID,
			"error": map[string]any{
				"code":    -32601,
				"message": "method not found",
			},
		})
	}
}

func (c *Client) handleInboundNotification(msg RPCRequest) {
	if c.callbacks.OnNotification != nil {
		c.callbacks.OnNotification(c.name, msg.Method, msg.Params)
	}
	switch msg.Method {
	case "notifications/progress":
		return
	case "notifications/message", "logging/message":
		return
	case "notifications/cancelled":
		return
	default:
		return
	}
}
