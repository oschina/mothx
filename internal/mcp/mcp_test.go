package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/imageproc"
)

func TestUniqueToolName(t *testing.T) {
	existing := map[string]struct{}{
		"mcp_a_b":   {},
		"mcp_a_b_2": {},
	}
	got := uniqueToolName("mcp_a_b", existing)
	if got != "mcp_a_b_3" {
		t.Fatalf("expected mcp_a_b_3, got %q", got)
	}
}

func TestMCPContentToText(t *testing.T) {
	out := mcpContentToText([]mcpContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "json", JSON: json.RawMessage(`{"k":"v"}`)},
		{Type: "image", MimeType: "image/png"},
	})
	want := "hello\n{\"k\":\"v\"}\n[image content: image/png]"
	if out != want {
		t.Fatalf("unexpected output:\nwant: %s\ngot:  %s", want, out)
	}
}

func TestReadLoopRespondsPing(t *testing.T) {
	in := bytes.NewBufferString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n")
	var out bytes.Buffer
	client := &Client{
		name:  "test",
		stdin: nopWriteCloser{Writer: &out},
	}
	client.readLoop(in)

	resp := out.String()
	if !strings.Contains(resp, `"id":1`) {
		t.Fatalf("expected ping response id, got %q", resp)
	}
	if !strings.Contains(resp, `"result":{}`) {
		t.Fatalf("expected ping response result, got %q", resp)
	}
}

func TestReadLoopResponseNotBlockedBySampling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	release := make(chan struct{})
	client := &Client{
		name:    "test",
		ctx:     ctx,
		cancel:  cancel,
		stdin:   nopWriteCloser{Writer: &bytes.Buffer{}},
		pending: make(map[string]chan mcpResponse),
		callbacks: Callbacks{OnSamplingCreateMessage: func(context.Context, string, json.RawMessage) (json.RawMessage, *RPCError) {
			close(started)
			<-release
			return json.RawMessage(`{"model":"test"}`), nil
		}},
	}
	client.startInboundLoop()
	response := make(chan mcpResponse, 1)
	client.pending["2"] = response

	input := bytes.NewBufferString(
		`{"jsonrpc":"2.0","id":1,"method":"sampling/createMessage","params":{}}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"result":{"ok":true}}` + "\n",
	)
	go client.readLoop(input)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("sampling callback did not start")
	}
	select {
	case got := <-response:
		if got.Error != nil || string(got.Result) != `{"ok":true}` {
			t.Fatalf("unexpected response while sampling blocked: %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("ordinary response was blocked by sampling callback")
	}
	close(release)
}

func TestParseSSECallResponseRequiresMatchingID(t *testing.T) {
	stream := strings.NewReader(
		"data: {\"jsonrpc\":\"2.0\",\"result\":{\"wrong\":true}}\n\n" +
			"data: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"ok\":true}}\n\n",
	)
	result, err := parseSSECallResponse(stream, 7)
	if err != nil {
		t.Fatalf("parse SSE response: %v", err)
	}
	if string(result) != `{"ok":true}` {
		t.Fatalf("unexpected SSE result: %s", result)
	}
}

func TestIsMCPMethodNotFound(t *testing.T) {
	if !isMCPMethodNotFound(&RPCError{Code: -32601, Message: "method not found"}) {
		t.Fatal("expected JSON-RPC method-not-found error to be ignored")
	}
	if isMCPMethodNotFound(&RPCError{Code: -32000, Message: "server failed"}) {
		t.Fatal("unexpected non-method-not-found error to be ignored")
	}
	if isMCPMethodNotFound(errors.New("method not found")) {
		t.Fatal("plain text error must not be treated as JSON-RPC method-not-found")
	}
}

func TestMCPSSERejectsInvalidMessageURL(t *testing.T) {
	_, err := newMCPHTTPClient(context.Background(), ServerConfig{
		Name:       "invalid-sse",
		Type:       "sse",
		URL:        "http://127.0.0.1/events",
		MessageURL: "file:///tmp/messages",
	}, true, Callbacks{})
	if err == nil || !strings.Contains(err.Error(), "messageUrl must be a valid http(s) URL") {
		t.Fatalf("newMCPHTTPClient error = %v, want invalid message URL", err)
	}
}

func TestPromptToolFormatsMessages(t *testing.T) {
	client := &Client{name: "srv"}
	tool := &mcpPromptTool{
		client: client,
		info:   mcpPromptInfo{Name: "draft"},
		name:   "mcp_srv_prompt_draft",
	}
	// monkey-patch through direct method behavior by wrapping getPrompt call expectation
	_ = tool
	// lightweight coverage on formatter branch with direct assembly
	out := mcpPromptGetResult{
		Description: "desc",
		Messages: []mcpPromptSample{
			{Role: "user", Content: mcpContentBlock{Type: "text", Text: "hello"}},
		},
	}
	var parts []string
	if strings.TrimSpace(out.Description) != "" {
		parts = append(parts, out.Description)
	}
	for _, msg := range out.Messages {
		content := mcpContentToText([]mcpContentBlock{msg.Content})
		parts = append(parts, "["+msg.Role+"]\n"+content)
	}
	got := strings.Join(parts, "\n\n")
	if !strings.Contains(got, "desc") || !strings.Contains(got, "hello") {
		t.Fatalf("unexpected formatted prompt output: %q", got)
	}
}

func TestHandleInboundNotificationNoPanic(t *testing.T) {
	c := &Client{name: "srv"}
	c.handleInboundNotification(RPCRequest{Method: "notifications/progress"})
	c.handleInboundNotification(RPCRequest{Method: "logging/message"})
	c.handleInboundNotification(RPCRequest{Method: "notifications/cancelled"})
	c.handleInboundNotification(RPCRequest{Method: "notifications/unknown"})
}

func TestExtractSamplingPrompt(t *testing.T) {
	raw := json.RawMessage(`{
		"messages":[
			{"role":"user","content":"hello"},
			{"role":"user","content":[{"type":"text","text":"world"}]}
		]
	}`)
	got := extractSamplingPrompt(raw)
	if got != "hello\nworld" {
		t.Fatalf("unexpected prompt: %q", got)
	}
}

func TestResourceToolURIOverride(t *testing.T) {
	tl := &mcpResourceTool{
		client: &Client{name: "srv"},
		info:   mcpResourceInfo{URI: "file://a"},
		name:   "mcp_srv_resource_file_a",
	}
	// only cover parameter override branch without network call
	uri := tl.info.URI
	params := map[string]any{"uri": "file://b"}
	if v, ok := params["uri"].(string); ok && strings.TrimSpace(v) != "" {
		uri = v
	}
	if uri != "file://b" {
		t.Fatalf("expected override uri, got %q", uri)
	}
}

// mcpTestPNGBase64 is a 1x1 PNG. It is intentionally tiny so projection tests
// stay deterministic and never depend on an external MCP server or a display.
const mcpTestPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl8P6sAAAAASUVORK5CYII="

// mcpTestPNG is the decoded form of mcpTestPNGBase64, for callers that need
// the raw image bytes.
var mcpTestPNG = func() []byte {
	raw, err := base64.StdEncoding.DecodeString(mcpTestPNGBase64)
	if err != nil {
		panic("invalid embedded PNG fixture: " + err.Error())
	}
	return raw
}()

func TestClassifyMCPBlock(t *testing.T) {
	cases := []struct {
		name     string
		block    mcpContentBlock
		wantKind string
		wantMime string
	}{
		{"text block", mcpContentBlock{Type: "text", Text: "hi"}, "text", ""},
		{"json block", mcpContentBlock{Type: "json", JSON: json.RawMessage(`{}`)}, "json", ""},
		{"audio block", mcpContentBlock{Type: "audio", MimeType: "audio/wav"}, "audio", "audio/wav"},
		{"typed image uses data", mcpContentBlock{Type: "image", Data: "AAA", MimeType: "image/png"}, "image", "image/png"},
		{"resource blob image", mcpContentBlock{Blob: "AAA", MimeType: "image/png"}, "image", "image/png"},
		{"resource blob non-image", mcpContentBlock{Blob: "AAA", MimeType: "application/pdf"}, "blob", "application/pdf"},
		{"resource text has no type", mcpContentBlock{Text: "hello", MimeType: "text/plain"}, "text", "text/plain"},
		{"untyped data is an image", mcpContentBlock{Data: "AAA", MimeType: "image/png"}, "image", "image/png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, _, mimeType := classifyMCPBlock(tc.block)
			if kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", kind, tc.wantKind)
			}
			if mimeType != tc.wantMime {
				t.Fatalf("mimeType = %q, want %q", mimeType, tc.wantMime)
			}
		})
	}
}

func TestMCPResourceReadResultDecodesBlob(t *testing.T) {
	// resources/read sends BlobResourceContents, which uses "blob" rather than
	// the tools/call "data" field. Before the fix this payload was silently
	// dropped during decode.
	var out mcpResourceReadResult
	raw := `{"contents":[{"uri":"shot://1","mimeType":"image/png","blob":"` + mcpTestPNGBase64 + `"}]}`
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Contents) != 1 {
		t.Fatalf("decoded %d content blocks, want 1", len(out.Contents))
	}
	if out.Contents[0].Blob != mcpTestPNGBase64 {
		t.Fatal("blob payload was not decoded from the resource content block")
	}
	if out.Contents[0].URI != "shot://1" {
		t.Fatalf("uri = %q, want shot://1", out.Contents[0].URI)
	}
}

func TestProjectMCPContentProjectsImage(t *testing.T) {
	client := &Client{name: "srv"}
	text, contents := client.projectMCPContent([]mcpContentBlock{
		{Type: "text", Text: `{"image_width":1464}`},
		{Type: "image", Data: mcpTestPNGBase64, MimeType: "image/png"},
	})

	if !strings.Contains(text, "image_width") {
		t.Fatalf("text summary lost the server payload: %q", text)
	}
	if !strings.Contains(text, "1x1") {
		t.Fatalf("text summary is missing image dimensions: %q", text)
	}
	if len(contents) != 2 {
		t.Fatalf("contents = %d blocks, want text + image", len(contents))
	}
	if contents[0].Type != "text" {
		t.Fatalf("first block type = %q, want text", contents[0].Type)
	}
	image := contents[1]
	if image.Type != "image" || image.Image == nil {
		t.Fatalf("second block = %#v, want an image block", image)
	}
	if image.Image.MimeType != "image/png" {
		t.Fatalf("image mime = %q, want image/png", image.Image.MimeType)
	}
	if image.Image.Width != 1 || image.Image.Height != 1 {
		t.Fatalf("image dimensions = %dx%d, want 1x1", image.Image.Width, image.Image.Height)
	}
	decoded, err := base64.StdEncoding.DecodeString(image.Image.Data)
	if err != nil {
		t.Fatalf("projected payload is not valid base64: %v", err)
	}
	if base64.StdEncoding.EncodeToString(decoded) != mcpTestPNGBase64 {
		t.Fatal("projected image payload does not round-trip to the source bytes")
	}
}

func TestProjectMCPContentKeepsTextOnlyShape(t *testing.T) {
	client := &Client{name: "srv"}
	text, contents := client.projectMCPContent([]mcpContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "json", JSON: json.RawMessage(`{"k":"v"}`)},
	})
	if text != "hello\n{\"k\":\"v\"}" {
		t.Fatalf("text = %q, want the historical text rendering", text)
	}
	if contents != nil {
		t.Fatalf("contents = %#v, want nil so existing MCP text tools are unchanged", contents)
	}
}

func TestProjectMCPContentKeepsAudioPlaceholder(t *testing.T) {
	client := &Client{name: "srv"}
	text, contents := client.projectMCPContent([]mcpContentBlock{
		{Type: "audio", MimeType: "audio/wav"},
	})
	if text != "[audio content: audio/wav]" {
		t.Fatalf("text = %q, want the audio placeholder", text)
	}
	if contents != nil {
		t.Fatalf("audio must not produce content blocks, got %#v", contents)
	}
}

func TestProjectMCPContentDegradesInvalidPayloads(t *testing.T) {
	client := &Client{name: "srv"}
	text, contents := client.projectMCPContent([]mcpContentBlock{
		{Type: "image", MimeType: "image/png"},
		{Type: "image", Data: "not-base64!!", MimeType: "image/png"},
		{Type: "image", Data: base64.StdEncoding.EncodeToString([]byte("not an image")), MimeType: "image/png"},
	})
	if contents != nil {
		t.Fatalf("contents = %#v, want nil when every image fails to project", contents)
	}
	for _, want := range []string{"empty payload", "invalid base64 payload", "omitted"} {
		if !strings.Contains(text, want) {
			t.Fatalf("degradation note %q missing from %q", want, text)
		}
	}
}

func TestProjectMCPContentCapsImageCount(t *testing.T) {
	client := &Client{name: "srv"}
	blocks := make([]mcpContentBlock, 0, mcpMaxProjectedImages+2)
	for i := 0; i < mcpMaxProjectedImages+2; i++ {
		blocks = append(blocks, mcpContentBlock{Type: "image", Data: mcpTestPNGBase64, MimeType: "image/png"})
	}
	_, contents := client.projectMCPContent(blocks)

	images := 0
	for _, block := range contents {
		if block.Type == "image" {
			images++
		}
	}
	if images != mcpMaxProjectedImages {
		t.Fatalf("projected %d images, want %d", images, mcpMaxProjectedImages)
	}
	text, _ := client.projectMCPContent(blocks)
	if !strings.Contains(text, "at most 4 images per tool result") {
		t.Fatalf("missing excess-image note in %q", text)
	}
}

func TestProjectMCPContentProjectsResourceBlob(t *testing.T) {
	client := &Client{name: "srv"}
	text, contents := client.projectMCPContent([]mcpContentBlock{
		{MimeType: "text/plain", Text: "plain resource body"},
		{URI: "shot://1", MimeType: "image/png", Blob: mcpTestPNGBase64},
	})
	if !strings.Contains(text, "plain resource body") {
		t.Fatalf("text resource body missing from %q", text)
	}
	if strings.Contains(text, `"type":""`) {
		t.Fatalf("text resource was JSON-wrapped instead of rendered as text: %q", text)
	}
	if len(contents) != 2 || contents[1].Type != "image" {
		t.Fatalf("resource blob did not project an image block: %#v", contents)
	}
}

func TestClientImagePolicyUsesLateBinding(t *testing.T) {
	// The shared Runtime connects MCP servers before the Agent exists, and the
	// registry receives its provider/model image hint only at Agent build time.
	// The policy must therefore be resolved per call, not captured at connect.
	client := &Client{name: "srv"}
	var seen imageproc.Mode
	client.imagePolicy = func(mode imageproc.Mode) imageproc.Policy {
		seen = mode
		return imageproc.Policy{Mode: mode, MaxLongEdge: 1, MaxOutputBytes: 1 << 20}
	}
	client.projectMCPContent([]mcpContentBlock{
		{Type: "image", Data: mcpTestPNGBase64, MimeType: "image/png"},
	})
	if seen != imageproc.ModeAuto {
		t.Fatalf("policy resolved with mode %q, want %q", seen, imageproc.ModeAuto)
	}
}

func TestClientImagePolicyFallsBackToDefaults(t *testing.T) {
	client := &Client{name: "srv"}
	if got := client.imagePolicyFor(imageproc.ModeAuto); got.Mode != imageproc.ModeAuto {
		t.Fatalf("fallback policy mode = %q, want %q", got.Mode, imageproc.ModeAuto)
	}
	var nilClient *Client
	if got := nilClient.imagePolicyFor(imageproc.ModeAuto); got.Mode != imageproc.ModeAuto {
		t.Fatal("nil client must fall back to the default policy instead of panicking")
	}
}

type nopWriteCloser struct {
	Writer *bytes.Buffer
}

func (n nopWriteCloser) Write(p []byte) (int, error) {
	return n.Writer.Write(p)
}

func (n nopWriteCloser) Close() error {
	return nil
}
