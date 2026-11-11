package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxHTTPRequestBody bounds one MCP request. The bridge is loopback-only and
// per-child scoped, but an untrusted worker must still not be able to allocate
// unbounded memory.
const maxHTTPRequestBody = 1 << 20

// NewHTTPHandler serves the minimal MCP streamable-HTTP shape used by the
// Worker Bridge: POST JSON in, POST JSON out. It intentionally supports only
// initialize, tools/list, and tools/call, and explicitly rejects SSE. It has no
// authentication of its own: callers wrap it with the Runtime-owned token
// middleware.
//
// Wire contract:
//   - POST /mcp with Content-Type: application/json returns one JSON-RPC
//     response with Content-Type: application/json.
//   - A notification (no id) returns 202 Accepted with an empty body.
//   - Accept: text/event-stream returns 406 Not Acceptable; this server never
//     opens an SSE stream.
//   - Any other method or path returns 405/404.
func NewHTTPHandler(handler ServerHandler) http.Handler {
	return &httpHandler{handler: handler}
}

type httpHandler struct {
	handler ServerHandler
}

func (h *httpHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if h.handler == nil {
		http.Error(response, "MCP server handler is required", http.StatusInternalServerError)
		return
	}
	if request.URL.Path != "/mcp" {
		http.NotFound(response, request)
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "MCP HTTP accepts POST /mcp only", http.StatusMethodNotAllowed)
		return
	}
	if requiresEventStreamOnly(request.Header.Get("Accept")) {
		http.Error(response, "MCP SSE is not supported; use a single JSON POST response", http.StatusNotAcceptable)
		return
	}
	if contentType := request.Header.Get("Content-Type"); contentType != "" && !isJSONContentType(contentType) {
		http.Error(response, "MCP HTTP requires Content-Type: application/json", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxHTTPRequestBody+1))
	if err != nil {
		http.Error(response, "read MCP request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) > maxHTTPRequestBody {
		http.Error(response, "MCP request exceeds the size limit", http.StatusRequestEntityTooLarge)
		return
	}
	var rpcRequest RPCRequest
	if err := json.Unmarshal(body, &rpcRequest); err != nil {
		writeJSONResponse(response, http.StatusBadRequest, serverError(nil, -32700, "parse error"))
		return
	}
	if strings.TrimSpace(rpcRequest.Method) == "" {
		writeJSONResponse(response, http.StatusBadRequest, serverError(rpcRequest.ID, -32600, "invalid request"))
		return
	}
	if len(rpcRequest.ID) == 0 {
		// Notifications need no reply, including notifications/initialized.
		response.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSONResponse(response, http.StatusOK, serveRequest(request.Context(), h.handler, rpcRequest))
}

func writeJSONResponse(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	encoder := json.NewEncoder(response)
	if err := encoder.Encode(value); err != nil {
		// The status/headers are already written; logging is the writer's job.
		return
	}
}

// requiresEventStreamOnly reports an Accept header that rules out JSON. A
// normal Streamable HTTP client advertises both application/json and
// text/event-stream; that must still receive a JSON response, because this
// server never opens an SSE stream. Only an SSE-only Accept is rejected.
func requiresEventStreamOnly(accept string) bool {
	sawEventStream, sawJSON := false, false
	for _, value := range strings.Split(accept, ",") {
		mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
		switch {
		case mediaType == "text/event-stream":
			sawEventStream = true
		case mediaType == "application/json", strings.HasSuffix(mediaType, "+json"), mediaType == "*/*":
			sawJSON = true
		}
	}
	return sawEventStream && !sawJSON
}

func isJSONContentType(value string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// ErrMCPHTTPUnsupported is returned by callers that need a stable diagnostic
// when a worker requests an unsupported transport. It is not used by the
// handler itself; the HTTP status is the wire contract.
var ErrMCPHTTPUnsupported = fmt.Errorf("only POST /mcp JSON is supported")
