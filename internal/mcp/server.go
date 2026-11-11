package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// ServerTool is a tool exposed by a local MCP server. It deliberately mirrors
// the MCP tools/list wire contract without coupling a server implementation to
// the client-side registry types.
type ServerTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ServerContent is one MCP tool-result content block.
type ServerContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ServerToolResult is the response payload of tools/call.
type ServerToolResult struct {
	Content []ServerContent `json:"content,omitempty"`
	IsError bool            `json:"isError,omitempty"`
}

// ServerHandler supplies domain behavior for a standard stdio MCP server.
// Protocol framing, initialize and tool dispatch remain in this package so
// domain packages do not implement their own JSON-RPC server.
type ServerHandler interface {
	ListTools(context.Context) ([]ServerTool, error)
	CallTool(context.Context, string, json.RawMessage) (ServerToolResult, error)
}

// ServerNamer optionally supplies the MCP serverInfo name. Domain handlers may
// implement it; the protocol layer remains the only JSON-RPC dispatcher.
type ServerNamer interface {
	ServerName() string
}

func serverInfo(handler ServerHandler) map[string]any {
	name := "mothx"
	if namer, ok := handler.(ServerNamer); ok {
		if configured := strings.TrimSpace(namer.ServerName()); configured != "" {
			name = configured
		}
	}
	return map[string]any{"name": name, "version": "dev"}
}

// ServeStdio serves the MCP stdio transport until input closes or ctx is
// cancelled. It supports initialize, tools/list and tools/call; optional
// resources/prompts are reported as unsupported.
func ServeStdio(ctx context.Context, input io.Reader, output io.Writer, handler ServerHandler) error {
	if handler == nil {
		return fmt.Errorf("MCP server handler is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reader := bufio.NewScanner(input)
	reader.Buffer(make([]byte, 4<<10), mcpMaxResponseBytes)
	encoder := json.NewEncoder(output)
	var writeMu sync.Mutex
	write := func(value any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return encoder.Encode(value)
	}
	for reader.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var request RPCRequest
		if err := json.Unmarshal(reader.Bytes(), &request); err != nil {
			if writeErr := write(serverError(nil, -32700, "parse error")); writeErr != nil {
				return writeErr
			}
			continue
		}
		if strings.TrimSpace(request.Method) == "" {
			if len(request.ID) > 0 {
				if err := write(serverError(request.ID, -32600, "invalid request")); err != nil {
					return err
				}
			}
			continue
		}
		if len(request.ID) == 0 {
			// Notifications, including notifications/initialized, need no reply.
			continue
		}
		if err := write(serveRequest(ctx, handler, request)); err != nil {
			return err
		}
	}
	if err := reader.Err(); err != nil {
		return fmt.Errorf("read MCP stdio request: %w", err)
	}
	return nil
}

func serveRequest(ctx context.Context, handler ServerHandler, request RPCRequest) any {
	switch request.Method {
	case "initialize":
		return serverResult(request.ID, map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      serverInfo(handler),
		})
	case "tools/list":
		tools, err := handler.ListTools(ctx)
		if err != nil {
			return serverError(request.ID, -32000, err.Error())
		}
		return serverResult(request.ID, map[string]any{"tools": tools})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || strings.TrimSpace(params.Name) == "" {
			return serverError(request.ID, -32602, "tools/call requires a tool name")
		}
		if len(params.Arguments) == 0 {
			params.Arguments = json.RawMessage(`{}`)
		}
		result, err := handler.CallTool(ctx, strings.TrimSpace(params.Name), params.Arguments)
		if err != nil {
			result = ServerToolResult{IsError: true, Content: []ServerContent{{Type: "text", Text: err.Error()}}}
		}
		return serverResult(request.ID, result)
	default:
		return serverError(request.ID, -32601, "method not found")
	}
}

func serverResult(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func serverError(id json.RawMessage, code int, message string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": &RPCError{Code: code, Message: message}}
}
