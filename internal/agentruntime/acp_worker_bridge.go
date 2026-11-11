package agentruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	agentpkg "github.com/oschina/mothx/agent"
	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/agentruntime/workerdriver"
	"github.com/oschina/mothx/internal/config"
)

// maxSubagentResultBytes bounds the worker text the parent can see. The full
// remote transcript stays a child projection; the lead only receives a bounded
// completion envelope.
const maxSubagentResultBytes = 16 * 1024

// maxACPWorkerMessageBytes leaves room for Runtime-bounded attachments encoded
// as base64 in one ACP prompt frame.
const maxACPWorkerMessageBytes = 64 << 20

// ACPWorkerInvocation is the already-authorized Runtime input to one external
// ACP worker turn. It contains no adapter state or provider content; Router
// task construction owns that conversion before this boundary.
type ACPWorkerInvocation struct {
	Profile               config.ACPAgentProfile
	Contract              SubagentTaskContract
	WorkDir               string
	AdditionalDirectories []string
	// ResumeSessionID requests an explicit reconnect through session/resume or
	// session/load (Runtime chooses from advertised capabilities). It is used
	// only for an already-created remote session; the caller must not send a
	// prompt through the old connection after this value is set.
	ResumeSessionID string
	Launcher        acpclient.Launcher
	ClientInfo      acpclient.Implementation
	// ReadTextFile advertises the Runtime-owned read-only filesystem bridge.
	// WriteTextFile advertises the Runtime-owned scoped write bridge, and
	// TerminalAccess opts into the profile-gated terminal bridge.
	ReadTextFile   bool
	WriteTextFile  bool
	TerminalAccess bool
	// MCPBridgeAvailable reports that this process has a live loopback worker
	// bridge. The Runtime sets it when (and only when) the bridge is running;
	// a false value degrades MCPHTTP without affecting the prompt.
	MCPBridgeAvailable bool
	// PrepareMCPServers is called after initialize negotiated the capability set.
	// It returns the session/new mcpServers payload and a revoke function. A
	// preparation failure only logs and degrades the bridge; the prompt proceeds.
	PrepareMCPServers func(workerdriver.WorkerCapabilities) ([]any, func(), error)
	HandleRequest     acpclient.RequestHandler
	OnNotification    func(acpclient.Message)
	// OnEvent receives canonical, non-terminal child events. The proxy owns the
	// single EventRunFinished terminal event.
	OnEvent func(agentpkg.Event)
	// pool is the Runtime-owned reusable process pool. Nil keeps the bridge
	// standalone for focused protocol tests and diagnostics.
	pool *acpConnectionPool
}

// ACPWorkerResult is the protocol result of one remote worker turn. The proxy
// converts it into the single canonical terminal event.
type ACPWorkerResult struct {
	AgentInfo  acpclient.Implementation
	SessionID  string
	StopReason string
	Output     string
	Truncated  bool
	ToolCalls  int
	// DroppedUpdates counts session updates the Runtime discarded because the
	// Agent emitted them faster than they could be projected. A non-zero value
	// means the transcript is missing its beginning.
	DroppedUpdates uint64
}

// ACPWorkerBridge owns one ACP v1 worker connection. It is intentionally not
// an Agent, session store, or queue; Runtime supplies policy and consumes the
// typed protocol result.
type ACPWorkerBridge struct{}

// Run obtains one initialized ACP connection, creates one remote session, and
// runs one bounded prompt turn. A Runtime-owned pool may reuse the process for
// other sessions; standalone callers own the process for this invocation.
func (ACPWorkerBridge) Run(ctx context.Context, invocation ACPWorkerInvocation) (ACPWorkerResult, error) {
	worker, err := (ACPWorkerBridge{}).Open(ctx, invocation)
	if err != nil {
		return ACPWorkerResult{}, err
	}
	defer func() { _ = worker.Close(ctx) }()
	return worker.Prompt(ctx, acpPromptContent(worker.Capabilities(), invocation.Contract, invocation.WorkDir), invocation.OnEvent)
}

// acpPromptContent is the capability-negotiated projection of one validated
// Runtime task contract. Unsupported content is never silently dropped: the
// text prompt names the omitted attachment and its workspace-relative path.
func acpPromptContent(capabilities map[string]any, contract SubagentTaskContract, workDir string) []acpclient.ContentBlock {
	text := contract.RenderPrompt(workDir)
	if len(contract.Attachments) == 0 {
		return []acpclient.ContentBlock{{Type: "text", Text: text}}
	}
	caps := promptCapabilities(capabilities)
	image := caps.PromptImage
	audio := caps.PromptAudio
	embedded := caps.PromptEmbeddedContext
	blocks := make([]acpclient.ContentBlock, 0, 1+len(contract.Attachments))
	var delivery strings.Builder
	delivery.WriteString("\n## Attachment delivery\n")
	for _, attachment := range contract.Attachments {
		switch {
		case attachment.Kind == AttachmentImage && image:
			blocks = append(blocks, acpclient.ContentBlock{
				Type: "image", Data: base64.StdEncoding.EncodeToString(attachment.Data), MIMEType: attachment.MediaType,
			})
			fmt.Fprintf(&delivery, "- %s: delivered as ACP image content.\n", attachment.Filename)
		case attachment.Kind == AttachmentAudio && audio:
			blocks = append(blocks, acpclient.ContentBlock{
				Type: "audio", Data: base64.StdEncoding.EncodeToString(attachment.Data), MIMEType: attachment.MediaType,
			})
			fmt.Fprintf(&delivery, "- %s: delivered as ACP audio content.\n", attachment.Filename)
		case embedded:
			resource := &acpclient.Resource{
				URI: acpAttachmentURI(attachment), MIMEType: attachment.MediaType,
			}
			if attachment.Kind == AttachmentFile && (strings.HasPrefix(attachment.MediaType, "text/") ||
				strings.Contains(attachment.MediaType, "json") || strings.Contains(attachment.MediaType, "xml")) {
				resource.Text = string(attachment.Data)
			} else {
				resource.Blob = base64.StdEncoding.EncodeToString(attachment.Data)
			}
			blocks = append(blocks, acpclient.ContentBlock{Type: "resource", Resource: resource})
			fmt.Fprintf(&delivery, "- %s: delivered as an embedded ACP resource.\n", attachment.Filename)
		default:
			fmt.Fprintf(&delivery, "- %s: not inlined because the ACP Agent did not advertise a compatible prompt capability; Runtime resource %q (%s, %d bytes, session-relative path %s) remains available to the parent.\n",
				attachment.Filename, attachment.ID, attachment.MediaType, attachment.Bytes, attachment.RelativePath)
		}
	}
	text += delivery.String()
	return append([]acpclient.ContentBlock{{Type: "text", Text: text}}, blocks...)
}

// acpAttachmentURI is an opaque identity for a Runtime-owned input resource.
// It must not be a file URI: an isolated worker's worktree is a fresh git
// worktree, so the session-workspace attachment path may not exist there.
func acpAttachmentURI(attachment SubagentAttachment) string {
	return "urn:mothx:attachment:" + url.PathEscape(attachment.ID)
}

func acquireACPWorkerConnection(ctx context.Context, invocation ACPWorkerInvocation, env []string) (*acpWorkerConnection, bool, error) {
	if invocation.pool != nil {
		conn, err := invocation.pool.acquire(ctx, invocation, env)
		return conn, false, err
	}
	conn := &acpWorkerConnection{
		profile:  invocation.Profile,
		ready:    make(chan struct{}),
		sessions: make(map[string]*acpWorkerSession),
	}
	conn.start(ctx, invocation, env)
	<-conn.ready
	if conn.initErr != nil {
		return nil, true, conn.initErr
	}
	return conn, true, nil
}

// boundedACPStderr keeps only a bounded amount of worker stderr for local
// diagnostics and never exposes it to the model. Exceeding the limit discards
// the excess instead of blocking the child.
type boundedACPStderr struct {
	buf   []byte
	limit int
}

func (w *boundedACPStderr) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		return len(p), nil
	}
	remaining := w.limit - len(w.buf)
	if remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		w.buf = append(w.buf, p[:remaining]...)
	}
	return len(p), nil
}

// acpRemoteToolCallID namespaces an opaque remote tool-call ID so it can never
// collide with a MothX tool call or be mistaken for a canonical Run ID.
func acpRemoteToolCallID(id string) string {
	if id == "" {
		return ""
	}
	return "acp:" + id
}

func decodeACPToolInput(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return map[string]any{"_raw": string(raw)}
	}
	return args
}

// safeACPBaseEnvironment is the minimal inherited environment an external
// worker needs to execute normally. The profile's own `environment` list
// overlays it; no other process variables (tokens, proxies with credentials,
// cloud credentials) are forwarded implicitly.
var safeACPBaseEnvironment = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TMP", "TEMP",
	"LANG", "LC_ALL", "LC_CTYPE", "TERM",
	"SystemRoot", "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "USERPROFILE",
	"APPDATA", "LOCALAPPDATA", "PROGRAMDATA",
}

func acpWorkerEnvironment(profile config.ACPAgentProfile) ([]string, error) {
	configured := config.LoadEnv().List()
	values := map[string]string{}
	for _, name := range safeACPBaseEnvironment {
		if value, ok := os.LookupEnv(name); ok {
			values[name] = value
		}
	}
	for _, name := range profile.Environment {
		value, ok := configured[name]
		if !ok {
			value, ok = os.LookupEnv(name)
		}
		if !ok {
			return nil, fmt.Errorf("ACP worker profile %q requires environment variable %q", profile.ID, name)
		}
		values[name] = value
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, name+"="+values[name])
	}
	return pairs, nil
}

// maxACPProjectionTextBytes bounds one projected remote field so a verbose or
// hostile worker cannot inflate canonical events or the parent context.
const maxACPProjectionTextBytes = 4 * 1024

func boundACPText(text string) (string, bool) {
	if len(text) <= maxACPProjectionTextBytes {
		return text, false
	}
	bounded, _ := truncateACPText(text, maxACPProjectionTextBytes)
	return bounded, true
}

// truncateACPText returns the longest valid UTF-8 prefix no longer than limit
// bytes. A plain byte slice could split a multi-byte rune and corrupt the
// projected event or completion text.
func truncateACPText(text string, limit int) (string, bool) {
	if limit <= 0 {
		return "", len(text) > 0
	}
	if len(text) <= limit {
		return text, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}
