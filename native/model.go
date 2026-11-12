package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
)

type messageKind int

const (
	messageUser messageKind = iota
	messageAssistant
	messageThought
	messageTool
	messagePlan
	messageSystem
	messageArtifact
	messageDecision
	messageStatus
	messageError
)

type toolDiff struct {
	Path    string
	OldText string
	NewText string
}

type chatMessage struct {
	ID   string
	Kind messageKind
	Text string
	At   time.Time
	Done bool

	// Reasoning folds a thought message.
	Collapsed bool

	// Tool messages.
	ToolID        string
	ToolTitle     string
	ToolKind      string
	ToolStatus    string
	ToolText      string
	ToolInput     map[string]any
	ToolDiffs     []toolDiff
	ToolLocations []string
	ToolExpanded  bool

	// Plan messages.
	Plan []planEntry

	// Artifact messages.
	ArtifactID   string
	Filename     string
	MediaType    string
	ArtifactKind string
	ArtifactSize int
	// ArtifactFromToolCall marks the publish_artifact fallback projection.
	// It is never openable as a canonical attachment on its own.
	ArtifactFromToolCall bool

	// Decision messages are canonical reverse requests projected inline.
	Decision *decision

	// Status/error transcript entries.
	ErrorCode      string
	ErrorRetryable bool

	// Images carried by user/assistant/tool content.
	Images     []chatImage
	ToolImages []chatImage
}

type chatImage struct {
	MimeType string
	Data     []byte
	Image    ui.ImageSource
}

type attachment struct {
	Name     string
	Path     string
	MimeType string
	Data     string
	Size     int64
	Embedded bool
	Preview  ui.ImageSource
}

// attachmentChatImages projects attached images into the optimistic user
// message so the transcript previews what was sent before ACP echoes it.
func attachmentChatImages(attachments []attachment) []chatImage {
	var images []chatImage
	for _, f := range attachments {
		if f.Preview == nil {
			continue
		}
		var data []byte
		if strings.HasPrefix(strings.ToLower(f.MimeType), "image/") {
			if f.Embedded && f.Data != "" {
				if decoded, err := base64.StdEncoding.DecodeString(f.Data); err == nil {
					data = decoded
				}
			}
		}
		images = appendUniqueImage(images, chatImage{MimeType: f.MimeType, Data: data, Image: f.Preview})
	}
	return images
}

const nativeMaxEmbeddedAttachment = 20 << 20

type decision struct {
	ID            json.RawMessage
	Kind          string // permission, question, elicitation
	Session       string
	Title         string
	Prompt        string
	Options       []decisionOption
	RawInput      map[string]any
	Deadline      time.Time
	Answer        string
	Choices       map[string]*bool
	Resolved      bool
	ResolvedValue string
	Multi         bool
	Elicit        bool
}

type decisionOption struct {
	ID    string
	Label string
	Kind  string
}

func (a *nativeApp) resetTranscript() {
	a.messages = nil
	a.messageIndex = make(map[string]*chatMessage)
	a.planMessageID = ""
	a.awaitingUserEcho = false
	a.localUserMessageID = ""
	a.list = ui.ListState{}
	a.transcriptNextCursor = ""
	a.transcriptLoading = false
	a.statusMessageID = ""
	atomic.AddInt64(&a.transcriptSeq, 1)
	a.usageUsed = 0
	a.usageSize = 0
	a.usageCost = 0
	a.usageCurrency = ""
}

func (a *nativeApp) messageByID(id string) *chatMessage {
	if a.messageIndex == nil {
		a.messageIndex = make(map[string]*chatMessage)
	}
	if m, ok := a.messageIndex[id]; ok {
		return m
	}
	return nil
}

func (a *nativeApp) appendMessage(m *chatMessage) *chatMessage {
	if m.At.IsZero() {
		m.At = time.Now()
	}
	if a.messageIndex == nil {
		a.messageIndex = make(map[string]*chatMessage)
	}
	if existing, ok := a.messageIndex[m.ID]; ok {
		return existing
	}
	a.messages = append(a.messages, m)
	a.messageIndex[m.ID] = m
	return m
}

func (a *nativeApp) upsertMessage(id string, kind messageKind) *chatMessage {
	if m := a.messageByID(id); m != nil {
		return m
	}
	return a.appendMessage(&chatMessage{ID: id, Kind: kind})
}

// applyUpdate folds one session/update notification into the transcript.
func (a *nativeApp) applyUpdate(n sessionUpdateNotification) {
	if n.SessionID == "" {
		return
	}
	if n.SessionID != a.currentSession && n.SessionID != a.loadingSession {
		a.withSessionState(n.SessionID, func() { a.applyUpdate(n) })
		return
	}
	u := n.Update
	switch u.SessionUpdate {
	case "user_message_chunk":
		if a.awaitingUserEcho {
			if m := a.messageByID(a.localUserMessageID); m != nil {
				a.awaitingUserEcho = false
				a.localUserMessageID = ""
				m.Done = true
				if text := contentText(u.Content); text != "" && !strings.Contains(m.Text, "\n\n附件：") {
					m.Text = text
				}
				for _, image := range contentImages(u.Content) {
					m.Images = appendUniqueImage(m.Images, image)
				}
				return
			}
		}
		id := "user:" + u.MessageID
		if u.MessageID == "" {
			id = a.nextLocalMessageID("user")
		}
		m := a.upsertMessage(id, messageUser)
		m.Done = true
		if text := contentText(u.Content); text != "" {
			m.Text = appendChunk(m.Text, text)
		}
		for _, image := range contentImages(u.Content) {
			m.Images = appendUniqueImage(m.Images, image)
		}
	case "agent_message_chunk":
		id := "agent:" + u.MessageID
		if u.MessageID == "" {
			id = a.nextLocalMessageID("agent")
		}
		m := a.upsertMessage(id, messageAssistant)
		if text := contentText(u.Content); text != "" {
			m.Text = appendChunk(m.Text, text)
		}
		for _, image := range contentImages(u.Content) {
			m.Images = appendUniqueImage(m.Images, image)
		}
	case "agent_thought_chunk":
		id := "thought:" + u.MessageID
		if u.MessageID == "" {
			id = a.nextLocalMessageID("thought")
		}
		m := a.upsertMessage(id, messageThought)
		if m.Text == "" {
			m.Collapsed = true
		}
		if text := contentText(u.Content); text != "" {
			m.Text = appendChunk(m.Text, text)
		}
	case "tool_call", "tool_call_update":
		if u.ToolCallID == "" {
			return
		}
		id := "tool:" + u.ToolCallID
		m := a.upsertMessage(id, messageTool)
		m.ToolID = u.ToolCallID
		if u.Title != "" {
			m.ToolTitle = u.Title
		}
		if u.Kind != "" {
			m.ToolKind = u.Kind
		}
		if u.Status != "" {
			m.ToolStatus = u.Status
		}
		if u.RawInput != nil {
			m.ToolTitle = toolTitle(m.ToolTitle, u.RawInput)
			m.ToolInput = u.RawInput
		}
		if text := toolContentText(u.Content, u.RawOutput); text != "" {
			m.ToolText = text
		}
		if diffs := toolDiffContents(u.Content); len(diffs) > 0 {
			m.ToolDiffs = diffs
		}
		for _, image := range toolContentImages(u.Content) {
			m.ToolImages = appendUniqueImage(m.ToolImages, image)
		}
		if len(u.Locations) > 0 {
			m.ToolLocations = make([]string, 0, len(u.Locations))
			for _, location := range u.Locations {
				if strings.TrimSpace(location.Path) != "" {
					m.ToolLocations = append(m.ToolLocations, location.Path)
				}
			}
		}
		if u.Status != "" && u.Status != "pending" && u.Status != "in_progress" {
			m.Done = true
		}
		a.maybeAddToolArtifactFallback(m)
	case "plan":
		a.updatePlan(u.Entries)
	case "usage_update":
		a.usageUsed = u.Used
		a.usageSize = u.Size
		if u.Cost != nil {
			a.usageCost = u.Cost.Amount
			a.usageCurrency = u.Cost.Currency
		}
		if cache := usageCacheFromMeta(u.Meta); cache != nil {
			a.usageCache = cache
		}
	case "session_info_update":
		if u.Title != "" {
			a.currentTitle = u.Title
			a.updateSessionTitle(n.SessionID, u.Title, u.UpdatedAt)
		}
	case "config_option_update":
		if len(u.ConfigOptions) > 0 {
			a.configOptions = u.ConfigOptions
		}
		a.syncModelState()
	case "current_mode_update":
		if u.CurrentModeID != "" {
			a.mode = u.CurrentModeID
		}
	case "available_commands_update":
		a.commands = u.Commands
	case "artifact":
		if u.ArtifactID == "" {
			return
		}
		m := a.upsertMessage("artifact:"+u.ArtifactID, messageArtifact)
		m.ArtifactID = u.ArtifactID
		m.Filename = u.Filename
		m.MediaType = u.MediaType
		m.ArtifactKind = u.Kind
		m.ArtifactSize = u.Size
		m.ArtifactFromToolCall = false
		m.Done = true
		a.dropToolArtifactFallbacks(m.Filename)
	}
}

// usageCacheProjection is the additive mothx.dev prompt-cache projection.
// It is only authoritative when ACP advertises usageCacheProjection; the
// hit-rate denominator is the Runtime's totalInputTokens, never a local guess.
type usageCacheProjection struct {
	CacheRead        int `json:"cacheRead"`
	CacheWrite       int `json:"cacheWrite"`
	TotalInputTokens int `json:"totalInputTokens"`
}

func usageCacheFromMeta(meta map[string]json.RawMessage) *usageCacheProjection {
	if len(meta) == 0 {
		return nil
	}
	raw, ok := meta["mothx.dev"]
	if !ok || len(raw) == 0 {
		return nil
	}
	var cache usageCacheProjection
	if err := json.Unmarshal(raw, &cache); err != nil {
		return nil
	}
	if cache.TotalInputTokens <= 0 {
		return nil
	}
	return &cache
}

// setStatusLine projects the current Runtime status into one replaceable
// transcript entry. It is presentation only: run/session state stays canonical.
func (a *nativeApp) setStatusLine(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if a.statusMessageID == "" {
		a.statusMessageID = a.nextLocalMessageID("status")
	}
	m := a.upsertMessage(a.statusMessageID, messageStatus)
	m.Text = text
	m.Done = false
}

func (a *nativeApp) finishStatusLine(status string) {
	if a.statusMessageID == "" {
		return
	}
	m := a.messageByID(a.statusMessageID)
	if m == nil {
		return
	}
	switch status {
	case "cancelled", "canceled":
		m.Text = "已取消"
	case "failed":
		m.Text = "运行失败"
	case "incomplete":
		m.Text = "未完成"
	default:
		m.Text = "运行完成"
	}
	m.Done = true
}

func (a *nativeApp) appendErrorCard(text, code string, retryable bool) {
	if strings.TrimSpace(text) == "" {
		return
	}
	a.appendMessage(&chatMessage{
		ID:             a.nextLocalMessageID("error"),
		Kind:           messageError,
		Text:           text,
		Done:           true,
		ErrorCode:      code,
		ErrorRetryable: retryable,
	})
}

func errorInfoFields(info map[string]any) (string, bool, string) {
	if len(info) == 0 {
		return "", false, ""
	}
	code, _ := info["code"].(string)
	retryable, _ := info["retryable"].(bool)
	detail, _ := info["detail"].(string)
	return code, retryable, detail
}

func (a *nativeApp) appendErrorFromErr(prefix string, err error) {
	if err == nil {
		return
	}
	text := prefix + err.Error()
	code := ""
	retryable := false
	var rpc *rpcError
	if errors.As(err, &rpc) && rpc != nil {
		if len(rpc.Data) > 0 {
			var data map[string]any
			if json.Unmarshal(rpc.Data, &data) == nil {
				if c, _ := data["code"].(string); c != "" {
					code = c
				}
				if r, ok := data["retryable"].(bool); ok {
					retryable = r
				}
			}
		}
	}
	a.appendErrorCard(text, code, retryable)
}

func (a *nativeApp) nextLocalMessageID(prefix string) string {
	a.localMessageSeq++
	return fmt.Sprintf("%s:local:%d", prefix, a.localMessageSeq)
}

func (a *nativeApp) updatePlan(entries []planEntry) {
	if len(entries) == 0 {
		return
	}
	if a.planMessageID == "" {
		a.planMessageID = a.nextLocalMessageID("plan")
	}
	m := a.upsertMessage(a.planMessageID, messagePlan)
	m.Plan = entries
}

func (a *nativeApp) updateSessionTitle(sessionID, title, updatedAt string) {
	for i := range a.sessions {
		if a.sessions[i].SessionID == sessionID {
			a.sessions[i].Title = title
			if updatedAt != "" {
				a.sessions[i].UpdatedAt = updatedAt
			}
			return
		}
	}
}

func (a *nativeApp) syncModelState() {
	a.modelLabel = ""
	a.thinkingLabel = ""
	for _, opt := range a.configOptions {
		switch opt.ID {
		case "model":
			a.modelLabel = choiceName(opt, opt.CurrentValue)
			a.modelValue = opt.CurrentValue
		case "thinking_level", "thought_level", "thinking":
			a.thinkingLabel = choiceName(opt, opt.CurrentValue)
			a.thinkingValue = opt.CurrentValue
		case "mode":
			if opt.CurrentValue != "" {
				a.mode = opt.CurrentValue
			}
		}
	}
}

func choiceName(opt configOption, value string) string {
	for _, c := range opt.Options {
		if c.Value == value {
			if c.Name != "" {
				return c.Name
			}
			return c.Value
		}
	}
	if value == "" {
		return opt.Name
	}
	return value
}

func (a *nativeApp) applySessionResult(res sessionResult) {
	if res.SessionID != "" {
		a.currentSession = res.SessionID
	}
	if res.Modes != nil {
		if res.Modes.CurrentModeID != "" {
			a.mode = res.Modes.CurrentModeID
		}
		a.availableModes = res.Modes.AvailableModes
	}
	if len(res.ConfigOptions) > 0 {
		a.configOptions = res.ConfigOptions
	}
	a.syncModelState()
	if res.History != nil {
		sessionID := res.SessionID
		if sessionID == "" {
			sessionID = a.currentSession
		}
		a.applyTranscriptPage(sessionID, res.History, false)
	}
}

func (a *nativeApp) markRunDone(stopReason string) {
	a.running = false
	a.awaitingUserEcho = false
	a.localUserMessageID = ""
	a.status = ""
	if stopReason == "cancelled" {
		a.status = "已停止"
	}
	a.finishStatusLine(stopReason)
	for _, m := range a.messages {
		m.Done = true
	}
	// A queued worktree "open in new session" intent may have become ready
	// while this run was still held; consume it now that the shell is idle.
	a.settleWorktreeOpen()
}

func (a *nativeApp) handleSessionEvent(ev sessionEvent) {
	if ev.Event == "cron_completed" {
		a.handleCronCompletedEvent(ev)
		return
	}
	if ev.SessionID != "" && ev.SessionID != a.currentSession && ev.SessionID != a.loadingSession {
		a.withSessionState(ev.SessionID, func() { a.handleSessionEvent(ev) })
		a.projectSessionRunEvent(ev)
		return
	}
	a.projectSessionRunEvent(ev)
	switch ev.Event {
	case "decision_deadline":
		a.applyDecisionDeadline(ev)
	case "run_status":
		switch ev.Status {
		case "running":
			a.running = true
			a.status = "正在生成…"
			a.setStatusLine("正在生成…")
		case "completed", "failed", "cancelled", "incomplete", "idle":
			a.markRunDone(ev.Status)
		}
	case "terminal":
		a.markRunDone(ev.Status)
		if ev.Error != "" {
			code, retryable, detail := errorInfoFields(ev.ErrorInfo)
			text := ev.Error
			if detail != "" && !strings.Contains(text, detail) {
				text += "\n" + detail
			}
			a.appendErrorCard(text, code, retryable)
		}
	case "retrying":
		a.status = "模型连接中断，正在重试…"
		if ev.Message != "" {
			a.status = ev.Message
		}
		a.setStatusLine(a.status)
	case "status":
		if ev.Message != "" {
			a.status = ev.Message
			a.setStatusLine(ev.Message)
		}
	case "turn_started":
		a.running = true
		a.status = "正在生成…"
		a.setStatusLine("正在生成…")
	case "turn_finished":
		a.status = "正在整理结果…"
		a.setStatusLine("正在整理结果…")
	case "compaction_started":
		a.status = "正在压缩上下文…"
		a.setStatusLine("正在压缩上下文…")
	case "compaction_finished":
		a.status = "正在生成…"
		a.setStatusLine("正在生成…")
	}
}

func decisionIDString(raw json.RawMessage) string {
	var id string
	if err := json.Unmarshal(raw, &id); err == nil {
		return id
	}
	return strings.Trim(string(raw), "\"")
}

func (a *nativeApp) applyDecisionDeadline(ev sessionEvent) {
	requestID := strings.TrimSpace(ev.RequestID)
	if requestID == "" {
		return
	}
	for _, message := range a.messages {
		if message.Decision == nil || decisionIDString(message.Decision.ID) != requestID {
			continue
		}
		message.Decision.Deadline = time.Time{}
		if ev.Remaining != nil {
			message.Decision.Deadline = time.Now().Add(time.Duration(*ev.Remaining) * time.Millisecond)
		} else if ev.Deadline != "" {
			if deadline, err := time.Parse(time.RFC3339, ev.Deadline); err == nil {
				message.Decision.Deadline = deadline
			}
		}
		return
	}
}

// projectSessionRunEvent updates the sidebar/history lastRun dot without
// waiting for the next list refresh. Terminal events clear Active; retrying,
// turn, and running statuses keep it set.
func (a *nativeApp) projectSessionRunEvent(ev sessionEvent) {
	if ev.SessionID == "" {
		return
	}
	switch ev.Event {
	case "run_status":
		if ev.Status == "" {
			return
		}
		a.setSessionRunLocal(ev.SessionID, ev.Status, sessionStatusIsActive(ev.Status))
	case "terminal":
		if ev.Status == "" {
			return
		}
		a.setSessionRunLocal(ev.SessionID, ev.Status, false)
	case "turn_started":
		a.setSessionRunLocal(ev.SessionID, "running", true)
	case "retrying":
		a.setSessionRunLocal(ev.SessionID, "running", true)
	case "turn_finished", "compaction_started", "compaction_finished":
		return
	}
}

func sessionStatusIsActive(status string) bool {
	switch status {
	case "created", "queued", "running", "planning", "working", "cancelling", "terminalizing",
		"waiting_for_approval", "waiting_for_question":
		return true
	default:
		return false
	}
}

func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var block contentBlock
	if err := json.Unmarshal(raw, &block); err != nil {
		return ""
	}
	if block.Type == "text" {
		return block.Text
	}
	return ""
}

func contentImages(raw json.RawMessage) []chatImage {
	raw = trimJSON(raw)
	if len(raw) == 0 {
		return nil
	}
	var block contentBlock
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil
	}
	return imageFromContentBlock(block)
}

func imageFromContentBlock(block contentBlock) []chatImage {
	if block.Type != "image" || strings.TrimSpace(block.Data) == "" {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(block.Data)
	if err != nil || len(data) == 0 {
		return nil
	}
	image, err := ui.DecodeImage(data)
	if err != nil {
		return nil
	}
	return []chatImage{{MimeType: block.MimeType, Data: data, Image: image}}
}

func appendUniqueImage(images []chatImage, image chatImage) []chatImage {
	for _, existing := range images {
		if existing.MimeType == image.MimeType && bytes.Equal(existing.Data, image.Data) {
			return images
		}
	}
	return append(images, image)
}

type toolContentItem struct {
	Type    string        `json:"type"`
	Content *contentBlock `json:"content"`
	Path    string        `json:"path"`
	OldText string        `json:"oldText"`
	NewText string        `json:"newText"`
}

func toolDiffContents(raw json.RawMessage) []toolDiff {
	raw = trimJSON(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var items []toolContentItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	var diffs []toolDiff
	for _, item := range items {
		if item.Type != "diff" {
			continue
		}
		diffs = append(diffs, toolDiff{Path: item.Path, OldText: item.OldText, NewText: item.NewText})
	}
	return diffs
}

func toolContentImages(raw json.RawMessage) []chatImage {
	raw = trimJSON(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var items []toolContentItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	var images []chatImage
	for _, item := range items {
		if item.Type != "content" || item.Content == nil {
			continue
		}
		for _, image := range imageFromContentBlock(*item.Content) {
			images = appendUniqueImage(images, image)
		}
	}
	return images
}

func toolContentText(raw json.RawMessage, output map[string]any) string {
	raw = trimJSON(raw)
	if len(raw) > 0 && raw[0] == '[' {
		var items []toolContentItem
		if err := json.Unmarshal(raw, &items); err == nil {
			var parts []string
			for _, item := range items {
				if item.Type == "content" && item.Content != nil && item.Content.Text != "" {
					parts = append(parts, item.Content.Text)
				}
				if item.Type == "diff" && item.Path != "" {
					parts = append(parts, "diff: "+item.Path)
				}
			}
			if len(parts) > 0 {
				return truncate(strings.Join(parts, "\n"), 20000)
			}
		}
	}
	if output != nil {
		if s, ok := output["content"].(string); ok && s != "" {
			return truncate(s, 20000)
		}
		if s, ok := output["error"].(string); ok && s != "" {
			return truncate(s, 20000)
		}
		if m, ok := output["diff"].(map[string]any); ok {
			if p, ok := m["path"].(string); ok {
				return "diff: " + p
			}
		}
	}
	return ""
}

func trimJSON(raw json.RawMessage) json.RawMessage {
	for len(raw) > 0 && (raw[0] == ' ' || raw[0] == '\n' || raw[0] == '\t' || raw[0] == '\r') {
		raw = raw[1:]
	}
	return raw
}

func toolTitle(title string, input map[string]any) string {
	command, _ := input["command"].(string)
	path, _ := input["path"].(string)
	switch {
	case title != "" && command != "":
		return title + " · " + truncateOneLine(command, 80)
	case title != "":
		return title
	case command != "":
		return command
	case path != "":
		return filepath.Base(path)
	}
	return T("工具调用")
}

func appendChunk(current, chunk string) string {
	if chunk == "" {
		return current
	}
	if current == "" {
		return chunk
	}
	// History replay sometimes re-emits the whole text; avoid duplicating it.
	if strings.HasPrefix(chunk, current) {
		return chunk
	}
	return current + chunk
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…"
}

func truncateOneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func relativeTime(iso string) string {
	if iso == "" {
		return ""
	}
	tm, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		if tm, err = time.Parse(time.RFC3339, iso); err != nil {
			return ""
		}
	}
	d := time.Since(tm)
	if currentLocale() == "en" {
		switch {
		case d < time.Minute:
			return "just now"
		case d < time.Hour:
			return fmt.Sprintf("%dm ago", int(d.Minutes()))
		case d < 24*time.Hour:
			return fmt.Sprintf("%dh ago", int(d.Hours()))
		case d < 7*24*time.Hour:
			return fmt.Sprintf("%dd ago", int(d.Hours()/24))
		case d < 365*24*time.Hour:
			return tm.Format("Jan 02")
		default:
			return tm.Format("2006-01-02")
		}
	}
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d 天", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return tm.Format("01-02")
	default:
		return tm.Format("2006-01-02")
	}
}

func sessionDisplayTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return T("未命名会话")
	}
	// The Runtime's default titles are canonical data, but an English shell
	// presents their localized equivalent instead of leaking the Chinese
	// default into lists, the header, or accessibility labels.
	switch title {
	case "新会话", "未命名会话":
		return T(title)
	default:
		return title
	}
}

func (a *nativeApp) sessionTitle() string {
	if a.currentTitle != "" {
		return sessionDisplayTitle(a.currentTitle)
	}
	if a.currentSession != "" {
		return T("会话")
	}
	return T("新会话")
}

func (a *nativeApp) modelDisplay() string {
	var parts []string
	if a.modelLabel != "" {
		parts = append(parts, a.modelLabel)
	}
	if a.thinkingLabel != "" && !strings.EqualFold(a.thinkingLabel, "off") {
		parts = append(parts, a.thinkingLabel)
	}
	if len(parts) == 0 {
		return "MothX"
	}
	return strings.Join(parts, " ")
}

func (a *nativeApp) workspaceName() string {
	cwd := a.workspace
	if cwd == "" {
		cwd = a.draftCwd
	}
	name := filepath.Base(cwd)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return cwd
	}
	return name
}

func prettyToolInput(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	data, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return ""
	}
	return truncate(string(data), 12000)
}

func toolDiffText(diff toolDiff) string {
	var b strings.Builder
	if diff.Path != "" {
		b.WriteString(diff.Path)
		b.WriteString("\n")
	}
	for _, line := range strings.Split(diff.OldText, "\n") {
		if line == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	for _, line := range strings.Split(diff.NewText, "\n") {
		if line == "" {
			continue
		}
		b.WriteString("+ ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return truncate(b.String(), 12000)
}

func decisionDeadlineLabel(deadline time.Time) string {
	if deadline.IsZero() {
		return ""
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return T("已到确认截止时间")
	}
	seconds := int(remaining.Round(time.Second) / time.Second)
	if seconds >= 60 {
		return Tf("约 %d 分 %d 秒后超时", seconds/60, seconds%60)
	}
	return Tf("约 %d 秒后超时", seconds)
}
