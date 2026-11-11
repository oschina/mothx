package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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
)

type chatMessage struct {
	ID   string
	Kind messageKind
	Text string
	At   time.Time
	Done bool

	// Reasoning folds a thought message.
	Collapsed bool

	// Tool messages.
	ToolID       string
	ToolTitle    string
	ToolKind     string
	ToolStatus   string
	ToolText     string
	ToolExpanded bool

	// Plan messages.
	Plan []planEntry

	// Artifact messages.
	ArtifactID string
	Filename   string
	MediaType  string
}

type attachment struct {
	Name string
	Path string
}

type decision struct {
	ID      json.RawMessage
	Kind    string // permission, question, elicitation
	Session string
	Title   string
	Prompt  string
	Options []decisionOption
	Multi   bool
	Elicit  bool
}

type decisionOption struct {
	ID    string
	Label string
	Kind  string
}

type sessionGroup struct {
	Cwd      string
	Name     string
	Sessions []listedSession
}

func (a *nativeApp) resetTranscript() {
	a.messages = nil
	a.messageIndex = make(map[string]*chatMessage)
	a.planMessageID = ""
	a.awaitingUserEcho = false
	a.localUserMessageID = ""
	a.list = ui.ListState{}
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
	if a.currentSession != "" && n.SessionID != a.currentSession {
		return
	}
	if a.loadingSession != "" && n.SessionID != a.loadingSession {
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
	case "agent_message_chunk":
		id := "agent:" + u.MessageID
		if u.MessageID == "" {
			id = a.nextLocalMessageID("agent")
		}
		m := a.upsertMessage(id, messageAssistant)
		if text := contentText(u.Content); text != "" {
			m.Text = appendChunk(m.Text, text)
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
		}
		if text := toolContentText(u.Content, u.RawOutput); text != "" {
			m.ToolText = text
		}
		if u.Status != "" && u.Status != "pending" && u.Status != "in_progress" {
			m.Done = true
		}
	case "plan":
		a.updatePlan(u.Entries)
	case "usage_update":
		a.usageUsed = u.Used
		a.usageSize = u.Size
		if u.Cost != nil {
			a.usageCost = u.Cost.Amount
			a.usageCurrency = u.Cost.Currency
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
		m.Done = true
	}
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
		}
	}
	if a.mode == "" {
		for _, opt := range a.configOptions {
			if opt.ID == "mode" {
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
}

func (a *nativeApp) markRunDone(stopReason string) {
	a.running = false
	a.awaitingUserEcho = false
	a.localUserMessageID = ""
	a.status = ""
	if stopReason == "cancelled" {
		a.status = "已停止"
	}
	for _, m := range a.messages {
		m.Done = true
	}
}

func (a *nativeApp) handleSessionEvent(ev sessionEvent) {
	if ev.Event == "cron_completed" {
		a.handleCronCompletedEvent(ev)
		return
	}
	if ev.SessionID != "" && a.currentSession != "" && ev.SessionID != a.currentSession {
		return
	}
	switch ev.Event {
	case "run_status":
		switch ev.Status {
		case "running":
			a.running = true
			a.status = "正在生成…"
		case "completed", "failed", "cancelled", "incomplete", "idle":
			a.markRunDone(ev.Status)
		}
	case "terminal":
		a.markRunDone(ev.Status)
		if ev.Error != "" {
			a.appendMessage(&chatMessage{ID: a.nextLocalMessageID("error"), Kind: messageSystem, Text: ev.Error, Done: true})
		}
	case "retrying":
		a.status = "模型连接中断，正在重试…"
		if ev.Message != "" {
			a.status = ev.Message
		}
	case "status":
		if ev.Message != "" {
			a.status = ev.Message
		}
	case "turn_started":
		a.running = true
		a.status = "正在生成…"
	case "turn_finished":
		a.status = "正在整理结果…"
	case "compaction_started":
		a.status = "正在压缩上下文…"
	case "compaction_finished":
		a.status = "正在生成…"
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

func toolContentText(raw json.RawMessage, output map[string]any) string {
	raw = trimJSON(raw)
	if len(raw) > 0 && raw[0] == '[' {
		var items []struct {
			Type    string        `json:"type"`
			Content *contentBlock `json:"content"`
			Path    string        `json:"path"`
		}
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
	return "工具调用"
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

func (a *nativeApp) groups() []sessionGroup {
	query := strings.ToLower(strings.TrimSpace(a.search))
	byCwd := make(map[string][]listedSession)
	latest := make(map[string]time.Time)
	for _, s := range a.sessions {
		if query != "" {
			haystack := strings.ToLower(s.Title + " " + s.Cwd + " " + s.Model)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		byCwd[s.Cwd] = append(byCwd[s.Cwd], s)
		if tm, err := time.Parse(time.RFC3339Nano, s.UpdatedAt); err == nil {
			if tm.After(latest[s.Cwd]) {
				latest[s.Cwd] = tm
			}
		}
	}
	current := a.workspace
	if current == "" {
		current = a.draftCwd
	}
	if current != "" {
		if _, ok := byCwd[current]; !ok && query == "" {
			byCwd[current] = nil
		}
	}
	var groups []sessionGroup
	for cwd, sessions := range byCwd {
		sort.SliceStable(sessions, func(i, j int) bool {
			return sessions[i].UpdatedAt > sessions[j].UpdatedAt
		})
		name := filepath.Base(cwd)
		if name == "." || name == string(filepath.Separator) || name == "" {
			name = cwd
		}
		groups = append(groups, sessionGroup{Cwd: cwd, Name: name, Sessions: sessions})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Cwd == current {
			return true
		}
		if groups[j].Cwd == current {
			return false
		}
		return latest[groups[i].Cwd].After(latest[groups[j].Cwd])
	})
	return groups
}

func (a *nativeApp) sessionTitle() string {
	if a.currentTitle != "" {
		return a.currentTitle
	}
	if a.currentSession != "" {
		return "会话"
	}
	return "新会话"
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
