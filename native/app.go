package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

type connState int

const (
	connIdle connState = iota
	connStarting
	connReady
	connFailed
)

const (
	nativeVersion       = "0.1.0"
	nativeChineseSlogan = "MothX用中国词元赋能你的任何工作"
)

type nativeApp struct {
	win *mygo.Window
	acp *acpClient

	conn       connState
	connErr    string
	stderr     []string
	features   map[string]bool
	featuresMu sync.RWMutex
	closing    atomic.Bool
	reloadSeq  int64

	sessions       []listedSession
	sessionsLoaded bool
	sessionsErr    string

	currentSession string
	loadingSession string
	currentTitle   string
	workspace      string
	draftCwd       string
	cwd            string

	messages        []*chatMessage
	messageIndex    map[string]*chatMessage
	planMessageID   string
	localMessageSeq int
	list            ui.ListState
	md              markdown.Options

	running            bool
	creatingSession    bool
	status             string
	awaitingUserEcho   bool
	localUserMessageID string
	usageUsed          int
	usageSize          int
	usageCost          float64
	usageCurrency      string

	draft       string
	attachments []attachment

	configOptions  []configOption
	modelLabel     string
	modelValue     string
	thinkingLabel  string
	thinkingValue  string
	mode           string
	availableModes []sessionMode
	commands       []command

	search       string
	searching    bool
	history      bool
	historyOpen  bool
	historyQuery string
	sidebar      bool
	collapsed    map[string]bool

	dark     bool
	themeSet bool

	decisions       []decision
	decisionOpen    bool
	questionText    string
	questionChoices map[string]*bool

	refreshing atomic.Bool

	settingsOpen     bool
	settingsCategory string
	settingsTab      string
	settingsLoading  bool
	settingsSaving   bool
	settingsErr      string
	settingsStatus   string
	settingsSeq      int64
	settingsView     *applicationSettingsView
	settingsForm     applicationSettingsForm

	providers  providerPanelState
	mcp        mcpPanelState
	knowledge  knowledgePanelState
	skills     skillsPanelState
	skillhub   skillHubPanelState
	experts    expertsPanelState
	acpWorkers acpWorkersPanelState
	memory     memoryPanelState
	cron       cronPanelState
	channels   channelsPanelState
	serve      servePanelState
}

func newNativeApp() *nativeApp {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	cwd, _ = filepath.Abs(cwd)
	a := &nativeApp{
		conn:            connIdle,
		cwd:             cwd,
		workspace:       cwd,
		draftCwd:        cwd,
		sidebar:         true,
		collapsed:       make(map[string]bool),
		messageIndex:    make(map[string]*chatMessage),
		questionChoices: make(map[string]*bool),
		dark:            true,
		providers:       newProviderPanelState(),
		mcp:             newMCPPanelState(),
		knowledge:       newKnowledgePanelState(),
		skills:          newSkillsPanelState(),
		skillhub:        newSkillHubPanelState(),
		experts:         newExpertsPanelState(),
		acpWorkers:      newACPWorkersPanelState(),
		memory:          newMemoryPanelState(),
		cron:            newCronPanelState(),
		channels:        newChannelsPanelState(),
		serve:           newServePanelState(),
	}
	a.md = markdown.Options{WrapCode: true}
	a.list.FollowEnd = true
	a.list.FollowAnimation = &ui.Smooth
	return a
}

func (a *nativeApp) dispatch(fn func()) {
	if a.win == nil {
		return
	}
	a.win.Update(fn)
}

func (a *nativeApp) setConn(state connState, errText string) {
	a.conn = state
	a.connErr = errText
}

func (a *nativeApp) connect() {
	seq := atomic.AddInt64(&a.reloadSeq, 1)
	bin := resolveMothxBinary()
	if bin == "" {
		a.dispatch(func() {
			a.setConn(connFailed, "找不到 mothx 可执行文件。请设置 MOTHX_BIN，或把 mothx 放进 PATH。")
		})
		return
	}
	a.dispatch(func() {
		a.setConn(connStarting, "")
		a.stderr = nil
		a.features = make(map[string]bool)
		// A reconnect invalidates the management projection as well as ordinary
		// session requests. Leave the settings page so an in-flight response
		// can neither render stale values nor leave its spinner active.
		a.closeSettings()
	})

	var client *acpClient
	client = newACPClient(
		func(msg incoming) { a.handleIncoming(client, msg) },
		func(line string) {
			a.dispatch(func() {
				a.stderr = append(a.stderr, line)
				if len(a.stderr) > 40 {
					a.stderr = a.stderr[len(a.stderr)-40:]
				}
			})
		},
		func(err error) {
			if a.closing.Load() {
				return
			}
			text := "ACP 进程已退出"
			if err != nil {
				text += "：" + err.Error()
			}
			a.dispatch(func() {
				if a.conn != connFailed {
					a.setConn(connFailed, text)
				}
				a.running = false
				a.creatingSession = false
				a.status = ""
			})
		},
	)
	if err := client.start(bin, a.cwd, os.Environ()); err != nil {
		a.dispatch(func() { a.setConn(connFailed, "无法启动 ACP："+err.Error()) })
		return
	}
	a.dispatch(func() { a.acp = client })

	raw, err := client.call("initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
			"session":  map[string]any{"configOptions": map[string]any{"boolean": map[string]any{}}},
		},
		"clientInfo": map[string]any{
			"name":    "mothx-native",
			"title":   "MothX Native",
			"version": nativeVersion,
		},
		"_meta": map[string]any{"mothx": map[string]any{"surface": "native"}},
	})
	if err != nil {
		client.close()
		a.dispatch(func() { a.setConn(connFailed, "ACP 初始化失败："+err.Error()) })
		return
	}
	var init initializeResult
	if err := json.Unmarshal(raw, &init); err != nil {
		client.close()
		a.dispatch(func() { a.setConn(connFailed, "ACP initialize 响应无效："+err.Error()) })
		return
	}
	features := parseFeatures(init)
	a.dispatch(func() {
		if seq != atomic.LoadInt64(&a.reloadSeq) {
			return
		}
		a.setConn(connReady, "")
		a.setFeatures(features)
	})
	if seq != atomic.LoadInt64(&a.reloadSeq) {
		client.close()
		return
	}
	a.refreshDraftConfig(client, a.cwd, features)
	a.refreshSessions(client, a.cwd, features)
}

func (a *nativeApp) setFeatures(features map[string]bool) {
	a.featuresMu.Lock()
	a.features = features
	a.featuresMu.Unlock()
}

func (a *nativeApp) featureSnapshot() map[string]bool {
	a.featuresMu.RLock()
	defer a.featuresMu.RUnlock()
	if a.features == nil {
		return nil
	}
	out := make(map[string]bool, len(a.features))
	for k, v := range a.features {
		out[k] = v
	}
	return out
}

func (a *nativeApp) featureEnabled(name string) bool {
	a.featuresMu.RLock()
	defer a.featuresMu.RUnlock()
	return a.features[name]
}

func parseFeatures(init initializeResult) map[string]bool {
	features := make(map[string]bool)
	raw, ok := init.Meta["mothx.dev"]
	if !ok {
		raw, ok = init.Meta["mothx"]
	}
	if !ok {
		return features
	}
	var meta struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return features
	}
	for _, f := range meta.Features {
		features[f] = true
	}
	return features
}

func (a *nativeApp) refreshDraftConfig(client *acpClient, cwd string, features map[string]bool) {
	if client == nil {
		return
	}
	if len(features) > 0 && !features["sessionDraftConfigOptions"] {
		return
	}
	raw, err := client.call("mothx/session/draft-config-options", map[string]any{"cwd": cwd})
	if err != nil {
		return
	}
	var res draftConfigResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return
	}
	a.dispatch(func() {
		selectedCwd := a.workspace
		if selectedCwd == "" {
			selectedCwd = a.draftCwd
		}
		if a.currentSession != "" || selectedCwd != cwd || len(res.ConfigOptions) == 0 {
			return
		}
		a.configOptions = res.ConfigOptions
		a.syncModelState()
	})
}

func (a *nativeApp) refreshSessions(client *acpClient, cwd string, features map[string]bool) {
	if client == nil {
		return
	}
	if a.refreshing.Swap(true) {
		return
	}
	defer a.refreshing.Store(false)
	if cwd == "" {
		cwd = a.cwd
	}
	var raw json.RawMessage
	var err error
	if features["sessionListAll"] || len(features) == 0 {
		raw, err = client.call("mothx/session/listAll", map[string]any{"scope": "all"})
	}
	if err != nil || len(raw) == 0 {
		raw, err = client.call("session/list", map[string]any{"cwd": cwd})
	}
	if err != nil {
		a.dispatch(func() {
			a.sessionsErr = err.Error()
			a.sessionsLoaded = true
		})
		return
	}
	var res sessionListResult
	if err := json.Unmarshal(raw, &res); err != nil {
		a.dispatch(func() {
			a.sessionsErr = err.Error()
			a.sessionsLoaded = true
		})
		return
	}
	a.dispatch(func() {
		a.sessions = res.Sessions
		a.sessionsErr = ""
		a.sessionsLoaded = true
		for _, listed := range res.Sessions {
			if listed.SessionID != a.currentSession {
				continue
			}
			a.workspace = listed.Cwd
			a.draftCwd = listed.Cwd
			if a.currentTitle == "" || a.currentTitle == "新会话" {
				a.currentTitle = listed.Title
			}
		}
	})
}

func (a *nativeApp) handleIncoming(client *acpClient, msg incoming) {
	switch msg.Method {
	case "session/update":
		var n sessionUpdateNotification
		if err := json.Unmarshal(msg.Params, &n); err != nil {
			return
		}
		a.dispatch(func() { a.applyUpdate(n) })
	case "_mothx/session_event":
		var ev sessionEvent
		if err := json.Unmarshal(msg.Params, &ev); err != nil {
			return
		}
		a.dispatch(func() { a.handleSessionEvent(ev) })
	case "$/cancel_request":
		return
	default:
		if msg.hasID() {
			a.handleReverse(client, msg)
		}
	}
}

func (a *nativeApp) handleReverse(client *acpClient, msg incoming) {
	switch msg.Method {
	case "session/request_permission":
		var p permissionParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			if client != nil {
				_ = client.respondError(msg.ID, -32602, "invalid permission params")
			}
			return
		}
		title := p.ToolCall.Title
		if title == "" {
			title = "工具调用"
		}
		d := decision{
			ID:      append(json.RawMessage(nil), msg.ID...),
			Kind:    "permission",
			Session: p.SessionID,
			Title:   title,
			Prompt:  permissionPrompt(p.ToolCall.RawInput),
		}
		for _, opt := range p.Options {
			label := opt.Name
			switch opt.Kind {
			case "allow_once", "allow":
				label = "允许一次"
			case "reject_once":
				label = "拒绝"
			}
			d.Options = append(d.Options, decisionOption{ID: opt.OptionID, Label: label, Kind: opt.Kind})
		}
		a.queueDecision(d)
	case "mothx/requestQuestion", "_mothx/request_question":
		var p questionParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			if client != nil {
				_ = client.respondError(msg.ID, -32602, "invalid question params")
			}
			return
		}
		prompt := p.Prompt
		if prompt == "" {
			prompt = p.Question
		}
		title := p.Title
		if title == "" {
			title = "MothX 想确认一下"
		}
		d := decision{
			ID:      append(json.RawMessage(nil), msg.ID...),
			Kind:    "question",
			Session: p.SessionID,
			Title:   title,
			Prompt:  prompt,
			Multi:   p.Multi,
		}
		d.Options = parseQuestionOptions(p.Options)
		a.queueDecision(d)
	case "elicitation/create":
		var p elicitationParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			if client != nil {
				_ = client.respondError(msg.ID, -32602, "invalid elicitation params")
			}
			return
		}
		d := decision{
			ID:      append(json.RawMessage(nil), msg.ID...),
			Kind:    "elicitation",
			Session: p.SessionID,
			Title:   "MothX 想确认一下",
			Prompt:  p.Message,
			Elicit:  true,
		}
		for name, prop := range p.Schema.Properties {
			if prop.Title != "" {
				d.Title = prop.Title
			}
			for _, enum := range prop.Enum {
				d.Options = append(d.Options, decisionOption{ID: enum, Label: enum})
			}
			if len(d.Options) == 0 {
				d.Options = append(d.Options, decisionOption{ID: name, Label: prop.Title})
			}
		}
		a.queueDecision(d)
	default:
		if client != nil && msg.hasID() {
			_ = client.respondError(msg.ID, -32601, "method not found: "+msg.Method)
		}
	}
}

func permissionPrompt(input map[string]any) string {
	if input == nil {
		return ""
	}
	if command, ok := input["command"].(string); ok && command != "" {
		return command
	}
	if path, ok := input["path"].(string); ok && path != "" {
		return path
	}
	if query, ok := input["query"].(string); ok && query != "" {
		return query
	}
	return ""
}

func parseQuestionOptions(raw json.RawMessage) []decisionOption {
	if len(raw) == 0 {
		return nil
	}
	var objects []questionOption
	if err := json.Unmarshal(raw, &objects); err == nil {
		var out []decisionOption
		for _, opt := range objects {
			label := opt.Label
			if label == "" {
				label = opt.ID
			}
			out = append(out, decisionOption{ID: opt.ID, Label: label})
		}
		return out
	}
	var stringsOpt []string
	if err := json.Unmarshal(raw, &stringsOpt); err == nil {
		out := make([]decisionOption, 0, len(stringsOpt))
		for _, s := range stringsOpt {
			out = append(out, decisionOption{ID: s, Label: s})
		}
		return out
	}
	return nil
}

func (a *nativeApp) queueDecision(d decision) {
	a.dispatch(func() {
		a.decisions = append(a.decisions, d)
		if !a.decisionOpen {
			a.openNextDecision()
		}
	})
}

func (a *nativeApp) openNextDecision() {
	a.questionText = ""
	a.questionChoices = make(map[string]*bool)
	a.decisionOpen = len(a.decisions) > 0
	if d := a.currentDecision(); d != nil && d.Multi {
		for _, opt := range d.Options {
			v := false
			a.questionChoices[opt.ID] = &v
		}
	}
}

func (a *nativeApp) currentDecision() *decision {
	if len(a.decisions) == 0 {
		return nil
	}
	return &a.decisions[0]
}

func (a *nativeApp) answerDecision(optionID string) {
	d := a.currentDecision()
	if d == nil {
		return
	}
	client := a.acp
	answer := strings.TrimSpace(a.questionText)
	if optionID != "" {
		answer = optionID
		if !d.Multi {
			a.questionText = optionID
		}
	}
	switch d.Kind {
	case "permission":
		if client != nil {
			if optionID == "" {
				_ = client.respond(d.ID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
			} else {
				_ = client.respond(d.ID, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}})
			}
		}
	case "elicitation":
		if client != nil {
			if answer == "" {
				_ = client.respond(d.ID, map[string]any{"action": "decline"})
			} else {
				_ = client.respond(d.ID, map[string]any{"action": "accept", "content": map[string]any{"answer": answer}})
			}
		}
	default:
		if client != nil {
			if d.Multi {
				var answers []string
				for _, opt := range d.Options {
					if ptr := a.questionChoices[opt.ID]; ptr != nil && *ptr {
						answers = append(answers, opt.ID)
					}
				}
				if answer != "" {
					answers = append(answers, answer)
				}
				if len(answers) == 0 {
					_ = client.respond(d.ID, map[string]any{"cancelled": true})
				} else {
					_ = client.respond(d.ID, map[string]any{"answers": answers})
				}
			} else if answer == "" {
				_ = client.respond(d.ID, map[string]any{"cancelled": true})
			} else {
				_ = client.respond(d.ID, map[string]any{"answer": answer})
			}
		}
	}
	a.decisions = a.decisions[1:]
	a.openNextDecision()
}

func (a *nativeApp) cancelDecision() {
	d := a.currentDecision()
	if d == nil {
		a.decisionOpen = false
		return
	}
	client := a.acp
	switch d.Kind {
	case "permission":
		if client != nil {
			_ = client.respond(d.ID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
		}
	case "elicitation":
		if client != nil {
			_ = client.respond(d.ID, map[string]any{"action": "decline"})
		}
	default:
		if client != nil {
			_ = client.respond(d.ID, map[string]any{"cancelled": true})
		}
	}
	a.decisions = a.decisions[1:]
	a.openNextDecision()
}

func (a *nativeApp) newChat() {
	if a.running || a.creatingSession || a.loadingSession != "" {
		a.status = "请先停止当前运行"
		return
	}
	if a.settingsOpen {
		a.closeSettings()
	}
	a.currentSession = ""
	a.loadingSession = ""
	a.currentTitle = ""
	a.running = false
	a.awaitingUserEcho = false
	a.localUserMessageID = ""
	a.status = ""
	a.draft = ""
	a.attachments = nil
	a.resetTranscript()
	cwd := a.workspace
	if cwd == "" {
		cwd = a.draftCwd
	}
	if cwd == "" {
		cwd = a.cwd
	}
	go a.refreshDraftConfig(a.acp, cwd, a.featureSnapshot())
}

func (a *nativeApp) newChatIn(cwd string) {
	if a.running || a.creatingSession || a.loadingSession != "" {
		a.status = "请先停止当前运行"
		return
	}
	if cwd != "" {
		a.workspace = cwd
		a.draftCwd = cwd
	}
	a.newChat()
}

func (a *nativeApp) chooseWorkspace() {
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            win,
			Title:             "打开工作区",
			Directory:         true,
			CreateDirectories: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		path := paths[0]
		a.dispatch(func() { a.newChatIn(path) })
	}()
}

func (a *nativeApp) attachFiles() {
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:   win,
			Title:    "附加文件",
			Multiple: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.dispatch(func() {
			for _, p := range paths {
				abs, err := filepath.Abs(p)
				if err != nil {
					abs = p
				}
				a.attachments = append(a.attachments, attachment{Name: filepath.Base(abs), Path: abs})
			}
		})
	}()
}

func (a *nativeApp) removeAttachment(i int) {
	if i < 0 || i >= len(a.attachments) {
		return
	}
	a.attachments = append(a.attachments[:i], a.attachments[i+1:]...)
}

func (a *nativeApp) selectSession(s listedSession) {
	if a.running || a.loadingSession != "" {
		a.status = "请先停止当前运行"
		return
	}
	if a.settingsOpen {
		a.closeSettings()
	}
	a.currentSession = s.SessionID
	a.loadingSession = s.SessionID
	a.currentTitle = s.Title
	a.workspace = s.Cwd
	a.draftCwd = s.Cwd
	a.running = false
	a.status = "正在载入会话…"
	a.resetTranscript()
	client := a.acp
	if client == nil {
		a.status = ""
		return
	}
	features := a.featureSnapshot()
	go func() {
		raw, err := client.call("session/load", map[string]any{
			"sessionId":    s.SessionID,
			"cwd":          s.Cwd,
			"historyLimit": 0,
		})
		if err != nil {
			a.dispatch(func() {
				a.loadingSession = ""
				a.currentSession = ""
				a.currentTitle = ""
				a.status = ""
				a.appendMessage(&chatMessage{ID: a.nextLocalMessageID("error"), Kind: messageSystem, Text: "载入会话失败：" + err.Error(), Done: true})
			})
			return
		}
		var res sessionResult
		_ = json.Unmarshal(raw, &res)
		a.dispatch(func() {
			if a.currentSession != s.SessionID {
				return
			}
			a.loadingSession = ""
			a.running = false
			a.status = ""
			a.applySessionResult(res)
			if res.Execution != nil && res.Execution.Busy {
				a.running = true
				a.status = "该会话正在运行…"
			}
			for _, m := range a.messages {
				m.Done = true
			}
			if a.currentTitle == "" && res.SessionID != "" {
				a.currentTitle = s.Title
			}
		})
		go a.refreshSessions(client, s.Cwd, features)
	}()
}

func (a *nativeApp) send() {
	text := strings.TrimSpace(a.draft)
	if text == "" || a.running || a.creatingSession {
		return
	}
	if a.conn != connReady || a.acp == nil {
		a.appendMessage(&chatMessage{ID: a.nextLocalMessageID("error"), Kind: messageSystem, Text: "尚未连接到 MothX ACP。", Done: true})
		return
	}
	attachments := append([]attachment(nil), a.attachments...)
	sessionID := a.currentSession
	cwd := a.workspace
	if cwd == "" {
		cwd = a.draftCwd
	}
	client := a.acp
	features := a.featureSnapshot()

	a.draft = ""
	a.attachments = nil
	a.running = true
	if sessionID == "" {
		a.creatingSession = true
	}
	a.status = "正在生成…"
	display := text
	if len(attachments) > 0 {
		var names []string
		for _, f := range attachments {
			names = append(names, f.Name)
		}
		display += "\n\n附件：" + strings.Join(names, "、")
	}
	localUserID := a.nextLocalMessageID("user")
	a.appendMessage(&chatMessage{ID: localUserID, Kind: messageUser, Text: display, Done: true})
	a.awaitingUserEcho = true
	a.localUserMessageID = localUserID

	go func() {
		if sessionID == "" {
			raw, err := client.call("session/new", map[string]any{"cwd": cwd})
			if err != nil {
				a.promptFailed(err)
				return
			}
			var res sessionResult
			if err := json.Unmarshal(raw, &res); err != nil {
				a.promptFailed(err)
				return
			}
			sessionID = res.SessionID
			a.dispatch(func() {
				a.applySessionResult(res)
				a.currentTitle = "新会话"
				a.creatingSession = false
			})
			go a.refreshSessions(client, cwd, features)
		}
		blocks := []contentBlock{{Type: "text", Text: text}}
		for _, f := range attachments {
			uri := fileURI(f.Path)
			blocks = append(blocks, contentBlock{Type: "resource_link", Name: f.Name, URI: uri})
		}
		raw, err := client.call("session/prompt", map[string]any{
			"sessionId": sessionID,
			"prompt":    blocks,
			"_meta": map[string]any{
				"mothx": map[string]any{"surface": "native"},
			},
		})
		if err != nil {
			a.promptFailed(err)
			return
		}
		var res promptResult
		_ = json.Unmarshal(raw, &res)
		a.dispatch(func() {
			a.markRunDone(res.StopReason)
			if res.StopReason != "" && res.StopReason != "end_turn" && res.StopReason != "cancelled" {
				a.appendMessage(&chatMessage{ID: a.nextLocalMessageID("system"), Kind: messageSystem, Text: "运行结束：" + res.StopReason, Done: true})
			}
		})
		go a.refreshSessions(client, cwd, features)
	}()
}

func (a *nativeApp) promptFailed(err error) {
	a.dispatch(func() {
		a.running = false
		a.creatingSession = false
		a.awaitingUserEcho = false
		a.localUserMessageID = ""
		a.status = ""
		a.appendMessage(&chatMessage{ID: a.nextLocalMessageID("error"), Kind: messageSystem, Text: "发送失败：" + err.Error(), Done: true})
	})
}

func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	uriPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath}).String()
}

func (a *nativeApp) stop() {
	if !a.running {
		return
	}
	client := a.acp
	sessionID := a.currentSession
	a.status = "正在停止…"
	if client != nil && sessionID != "" {
		go func() {
			_, _ = client.call("session/cancel", map[string]any{"sessionId": sessionID})
		}()
		return
	}
	a.markRunDone("cancelled")
}

func (a *nativeApp) setConfigOption(configID, value string) {
	client := a.acp
	if client == nil || a.running || a.creatingSession {
		return
	}
	features := a.featureSnapshot()
	sessionID := a.currentSession
	cwd := a.workspace
	if cwd == "" {
		cwd = a.draftCwd
	}
	creating := sessionID == ""
	if creating {
		a.creatingSession = true
	}
	go func() {
		if sessionID == "" {
			raw, err := client.call("session/new", map[string]any{"cwd": cwd})
			if err != nil {
				a.dispatch(func() { a.creatingSession = false })
				return
			}
			var res sessionResult
			if err := json.Unmarshal(raw, &res); err != nil {
				a.dispatch(func() { a.creatingSession = false })
				return
			}
			sessionID = res.SessionID
			a.dispatch(func() { a.applySessionResult(res) })
			go a.refreshSessions(client, cwd, features)
		}
		raw, err := client.call("session/set_config_option", map[string]any{
			"sessionId": sessionID,
			"configId":  configID,
			"value":     value,
		})
		if err != nil {
			a.dispatch(func() {
				a.creatingSession = false
				a.status = "切换失败：" + err.Error()
			})
			return
		}
		var res sessionResult
		if err := json.Unmarshal(raw, &res); err == nil && len(res.ConfigOptions) > 0 {
			a.dispatch(func() {
				a.configOptions = res.ConfigOptions
				a.syncModelState()
				a.creatingSession = false
			})
		} else if creating {
			a.dispatch(func() { a.creatingSession = false })
		}
	}()
}

func (a *nativeApp) shutdown() {
	a.closing.Store(true)
	if a.acp != nil {
		a.acp.close()
	}
}

// Settings lifecycle is implemented in settings_nav.go. The methods below
// handle the application-settings load/save/reconnect path only.

func (a *nativeApp) loadApplicationSettings(seq int64) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.settingsOpen || seq != atomic.LoadInt64(&a.settingsSeq) {
				return
			}
			a.settingsLoading = false
			a.settingsErr = "尚未连接到 MothX ACP。"
		})
		return
	}
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	raw, err := client.call("mothx/manage/application/get", map[string]any{})
	a.dispatch(func() {
		if !a.settingsOpen || seq != atomic.LoadInt64(&a.settingsSeq) {
			return
		}
		if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
			return
		}
		a.settingsLoading = false
		if err != nil {
			a.settingsErr = "读取应用设置失败：" + err.Error()
			return
		}
		var view applicationSettingsView
		if err := json.Unmarshal(raw, &view); err != nil {
			a.settingsErr = "应用设置响应无效：" + err.Error()
			return
		}
		a.settingsView = &view
		a.settingsForm = applicationSettingsFormFromView(&view)
		a.settingsErr = ""
	})
}

func (a *nativeApp) saveApplicationSettings() {
	if a.settingsSaving || a.settingsLoading {
		return
	}
	if a.settingsView == nil {
		a.settingsStatus = "保存失败：设置尚未加载。"
		return
	}
	patch := applicationSettingsPatch(a.settingsView, &a.settingsForm)
	if len(patch) == 0 {
		a.settingsStatus = "没有需要保存的变更"
		return
	}
	client := a.acp
	if client == nil {
		a.settingsStatus = "保存失败：尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.AddInt64(&a.settingsSeq, 1)
	a.settingsSaving = true
	a.settingsStatus = "正在保存…"
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		raw, err := client.call("mothx/manage/application/patch", map[string]any{"patch": patch})
		a.dispatch(func() {
			if !a.settingsOpen || seq != atomic.LoadInt64(&a.settingsSeq) {
				return
			}
			if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			a.settingsSaving = false
			if err != nil {
				a.settingsStatus = "保存失败：" + err.Error()
				return
			}
			var view applicationSettingsView
			if err := json.Unmarshal(raw, &view); err != nil {
				a.settingsStatus = "保存后响应无效：" + err.Error()
				return
			}
			a.settingsView = &view
			a.settingsForm = applicationSettingsFormFromView(&view)
			a.settingsStatus = "应用设置已保存"
		})
	}()
}

func (a *nativeApp) onWebSearchBackendChanged(backend string) {
	if a.settingsForm.WebSearchBackend == backend {
		return
	}
	a.settingsForm.WebSearchBackend = backend
	a.settingsForm.WebSearchBaseURL = ""
	a.settingsForm.WebSearchToken = ""
	a.settingsForm.ClearWebSearchToken = true
}

// small helpers used by the view.

func (a *nativeApp) currentModelOption() *configOption {
	for i := range a.configOptions {
		if a.configOptions[i].ID == "model" {
			return &a.configOptions[i]
		}
	}
	return nil
}

func (a *nativeApp) currentThinkingOption() *configOption {
	for i := range a.configOptions {
		switch a.configOptions[i].ID {
		case "thinking_level", "thought_level", "thinking":
			return &a.configOptions[i]
		}
	}
	return nil
}

func (a *nativeApp) connectionLabel() string {
	switch a.conn {
	case connStarting:
		return "连接中"
	case connReady:
		return "已连接"
	case connFailed:
		return "离线"
	default:
		return "未连接"
	}
}

func (a *nativeApp) usageLabel() string {
	if a.usageUsed <= 0 {
		return ""
	}
	if a.usageSize <= 0 {
		return fmt.Sprintf("%d tokens", a.usageUsed)
	}
	return fmt.Sprintf("%d / %d", a.usageUsed, a.usageSize)
}

func (a *nativeApp) workspaceLabel() string {
	return a.workspaceName()
}
