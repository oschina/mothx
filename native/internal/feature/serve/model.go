// Package serve owns the Native projection of the ACP Serve configuration
// management contract (mothx/manage/serve/get and patch).
package serve

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
)

// This file owns the Native projection of the ACP Serve management contract
// (mothx/manage/serve/get and mothx/manage/serve/patch). Serve configuration is
// a thin, secret-safe projection of the global serve.json; this panel only
// reads and writes it through ACP and never starts, restarts, or proxies a
// `mothx serve` process.

var (
	serveModes             = []string{"yolo", "agent", "plan", "os"}
	serveThinkingLevels    = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	serveLogLevels         = []string{"debug", "info", "warn", "error"}
	serveSystemPromptModes = []string{"append", "ignore"}
	serveToolModes         = []string{"content", "sse_event", "none"}
	serveToolDetails       = []string{"collapsed", "expanded"}
)

// ToolVisibilityView mirrors the api.toolVisibility section.
type ToolVisibilityView struct {
	Mode   string `json:"mode"`
	Detail string `json:"detail"`
}

// APISessionView mirrors the api.session section. Numeric fields are
// pointers so an omitted field keeps the Desktop default instead of decoding
// to a misleading zero.
type APISessionView struct {
	IdleTimeoutSeconds *int `json:"idleTimeoutSeconds"`
	MaxSessions        *int `json:"maxSessions"`
}

// APIView mirrors the safe api section of a serve get/patch response.
type APIView struct {
	Listen                  string             `json:"listen"`
	DefaultMode             string             `json:"defaultMode"`
	DefaultThinkingLevel    string             `json:"defaultThinkingLevel"`
	EnableSubAgents         bool               `json:"enableSubAgents"`
	EnableDelegate          bool               `json:"enableDelegate"`
	EnableWorkflows         bool               `json:"enableWorkflows"`
	EnableWebSearch         bool               `json:"enableWebSearch"`
	EnableBrowser           bool               `json:"enableBrowser"`
	EnableArtifact          bool               `json:"enableArtifact"`
	EnableA2AMaster         bool               `json:"enableA2AMaster"`
	ToolVisibility          ToolVisibilityView `json:"toolVisibility"`
	SystemPromptMode        string             `json:"systemPromptMode"`
	RequestTimeoutSeconds   *int               `json:"requestTimeoutSeconds"`
	BackgroundRunMaxSeconds *int               `json:"backgroundRunMaxSeconds"`
	MaxConcurrentRequests   *int               `json:"maxConcurrentRequests"`
	LogLevel                string             `json:"logLevel"`
	Session                 APISessionView     `json:"session"`
}

type FeaturesView struct {
	WebUI      bool `json:"webUI"`
	OpenAIAPI  bool `json:"openAIAPI"`
	MultiAgent bool `json:"multiAgent"`
	Cron       bool `json:"cron"`
	Memory     bool `json:"memory"`
}

type WebUIView struct {
	Enabled bool   `json:"enabled"`
	Dir     string `json:"dir"`
}

type CronView struct {
	Enabled  bool `json:"enabled"`
	Interval *int `json:"interval"`
}

type MemoryView struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"`
}

type SecurityView struct {
	SmartApprovals bool `json:"smartApprovals"`
}

type AgentView struct {
	MaxTurns                 *int     `json:"maxTurns"`
	BudgetPressure           bool     `json:"budgetPressure"`
	ContextPressure          bool     `json:"contextPressure"`
	BudgetPressureThreshold  *float64 `json:"budgetPressureThreshold"`
	ContextPressureThreshold *float64 `json:"contextPressureThreshold"`
	RunStaleTimeoutSeconds   *int     `json:"runStaleTimeoutSeconds"`
	RunMaxDurationSeconds    *int     `json:"runMaxDurationSeconds"`
	BackgroundRunMaxSecs     *int     `json:"backgroundRunMaxSecs"`
}

// ConfigView mirrors the complete mothx/manage/serve get/patch response.
type ConfigView struct {
	API         APIView      `json:"api"`
	Features    FeaturesView `json:"features"`
	WebUI       WebUIView    `json:"webUI"`
	Cron        CronView     `json:"cron"`
	Memory      MemoryView   `json:"memory"`
	Security    SecurityView `json:"security"`
	Agent       AgentView    `json:"agent"`
	LobsterMode bool         `json:"lobsterMode"`
}

// Form holds the editable state of the Serve panel. Numeric fields stay
// strings so empty or malformed input can be rejected at save time instead of
// silently becoming zero.
type Form struct {
	// api
	Listen           string
	WebUIDir         string
	RequestTimeout   string
	BackgroundRunMax string
	MaxConcurrent    string
	LogLevel         string
	DefaultMode      string
	DefaultThinking  string
	SystemPromptMode string

	// features
	WebUI         bool
	OpenAIAPI     bool
	MultiAgent    bool
	CronFeature   bool
	MemoryFeature bool

	// api capabilities
	EnableDelegate  bool
	EnableWorkflows bool
	EnableWebSearch bool
	EnableBrowser   bool
	EnableArtifact  bool
	EnableA2AMaster bool

	// api.toolVisibility
	ToolMode   string
	ToolDetail string

	// automation
	CronInterval string
	MemoryPath   string

	// security
	SmartApprovals bool

	// api.session
	IdleTimeout string
	MaxSessions string

	// agent
	MaxTurns              string
	BudgetPressure        bool
	ContextPressure       bool
	BudgetThreshold       string
	ContextThreshold      string
	RunStaleTimeout       string
	RunMaxDuration        string
	AgentBackgroundRunMax string

	// top level
	LobsterMode bool
}

func NewForm() Form {
	return Form{
		RequestTimeout:        "1",
		BackgroundRunMax:      "1",
		MaxConcurrent:         "0",
		LogLevel:              "info",
		DefaultMode:           "yolo",
		DefaultThinking:       "medium",
		SystemPromptMode:      "append",
		ToolMode:              "content",
		ToolDetail:            "collapsed",
		CronInterval:          "1",
		IdleTimeout:           "1800",
		MaxSessions:           "0",
		MaxTurns:              "1",
		BudgetThreshold:       "0.2",
		ContextThreshold:      "0.55",
		RunStaleTimeout:       "1",
		RunMaxDuration:        "1",
		AgentBackgroundRunMax: "1",
	}
}

func IntString(value *int, fallback int) string {
	if value != nil {
		return strconv.Itoa(*value)
	}
	return strconv.Itoa(fallback)
}

func FloatString(value *float64, fallback float64) string {
	if value != nil {
		return strconv.FormatFloat(*value, 'f', -1, 64)
	}
	return strconv.FormatFloat(fallback, 'f', -1, 64)
}

// FormFromView mirrors the Desktop ServePanel defaults: missing numeric
// or enumeration fields fall back to the same values the Desktop form uses.
func FormFromView(view *ConfigView) Form {
	form := NewForm()
	if view == nil {
		return form
	}

	form.Listen = view.API.Listen
	form.WebUIDir = view.WebUI.Dir
	if view.API.LogLevel != "" {
		form.LogLevel = view.API.LogLevel
	}
	if view.API.DefaultMode != "" {
		form.DefaultMode = view.API.DefaultMode
	}
	if view.API.DefaultThinkingLevel != "" {
		form.DefaultThinking = view.API.DefaultThinkingLevel
	}
	if view.API.SystemPromptMode != "" {
		form.SystemPromptMode = view.API.SystemPromptMode
	}
	if view.API.ToolVisibility.Mode != "" {
		form.ToolMode = view.API.ToolVisibility.Mode
	}
	if view.API.ToolVisibility.Detail != "" {
		form.ToolDetail = view.API.ToolVisibility.Detail
	}
	form.RequestTimeout = IntString(view.API.RequestTimeoutSeconds, 1)
	form.BackgroundRunMax = IntString(view.API.BackgroundRunMaxSeconds, 1)
	form.MaxConcurrent = IntString(view.API.MaxConcurrentRequests, 0)
	form.IdleTimeout = IntString(view.API.Session.IdleTimeoutSeconds, 1800)
	form.MaxSessions = IntString(view.API.Session.MaxSessions, 0)

	form.WebUI = view.Features.WebUI
	form.OpenAIAPI = view.Features.OpenAIAPI
	form.MultiAgent = view.Features.MultiAgent
	form.CronFeature = view.Features.Cron
	form.MemoryFeature = view.Features.Memory

	// The feature switches are the canonical projection, matching the Desktop
	// panel. The server normalizes api.enableSubAgents, webUI.enabled,
	// cron.enabled, and memory.enabled to the same values, so the editor does
	// not expose duplicate toggles for the same concept.

	form.EnableDelegate = view.API.EnableDelegate
	form.EnableWorkflows = view.API.EnableWorkflows
	form.EnableWebSearch = view.API.EnableWebSearch
	form.EnableBrowser = view.API.EnableBrowser
	form.EnableArtifact = view.API.EnableArtifact
	form.EnableA2AMaster = view.API.EnableA2AMaster

	form.CronInterval = IntString(view.Cron.Interval, 1)
	form.MemoryPath = view.Memory.Path
	form.SmartApprovals = view.Security.SmartApprovals

	form.MaxTurns = IntString(view.Agent.MaxTurns, 1)
	form.BudgetPressure = view.Agent.BudgetPressure
	form.ContextPressure = view.Agent.ContextPressure
	form.BudgetThreshold = FloatString(view.Agent.BudgetPressureThreshold, 0.2)
	form.ContextThreshold = FloatString(view.Agent.ContextPressureThreshold, 0.55)
	form.RunStaleTimeout = IntString(view.Agent.RunStaleTimeoutSeconds, 1)
	form.RunMaxDuration = IntString(view.Agent.RunMaxDurationSeconds, 1)
	form.AgentBackgroundRunMax = IntString(view.Agent.BackgroundRunMaxSecs, 1)
	form.LobsterMode = view.LobsterMode

	return form
}

func OptionAllowed(options []string, value string) bool {
	for _, option := range options {
		if option == value {
			return true
		}
	}
	return false
}

func ParseInt(label, raw string, min int) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < min {
		return 0, i18n.LocalizedErrorf("%s必须是大于等于 %d 的整数", i18n.T(label), min)
	}
	return value, nil
}

func ParseFloat(label, raw string, min, max float64) (float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < min || value > max {
		return 0, i18n.LocalizedErrorf("%s必须是 %g 到 %g 之间的数字", i18n.T(label), min, max)
	}
	return value, nil
}

type Numbers struct {
	requestTimeout        int
	backgroundRunMax      int
	maxConcurrent         int
	cronInterval          int
	idleTimeout           int
	maxSessions           int
	maxTurns              int
	budgetThreshold       float64
	contextThreshold      float64
	runStaleTimeout       int
	runMaxDuration        int
	agentBackgroundRunMax int
}

// ParseForm validates the whole editor and converts its numeric strings
// once. The ACP server remains the final authority, but an invalid form never
// crosses the ACP boundary.
func ParseForm(form *Form) (Numbers, error) {
	var numbers Numbers
	if form == nil {
		return numbers, i18n.LocalizedError("Serve 配置不存在")
	}
	if strings.TrimSpace(form.Listen) == "" {
		return numbers, i18n.LocalizedError("监听地址不能为空")
	}
	if strings.TrimSpace(form.WebUIDir) == "" {
		return numbers, i18n.LocalizedError("Web UI 目录不能为空")
	}
	if !OptionAllowed(serveModes, strings.TrimSpace(form.DefaultMode)) {
		return numbers, i18n.LocalizedErrorf("默认模式必须是以下值之一：%s", strings.Join(serveModes, "、"))
	}
	if !OptionAllowed(serveThinkingLevels, strings.TrimSpace(form.DefaultThinking)) {
		return numbers, i18n.LocalizedErrorf("默认思考级别必须是以下值之一：%s", strings.Join(serveThinkingLevels, "、"))
	}
	if !OptionAllowed(serveLogLevels, strings.TrimSpace(form.LogLevel)) {
		return numbers, i18n.LocalizedErrorf("日志级别必须是以下值之一：%s", strings.Join(serveLogLevels, "、"))
	}
	if !OptionAllowed(serveSystemPromptModes, strings.TrimSpace(form.SystemPromptMode)) {
		return numbers, i18n.LocalizedErrorf("系统提示模式必须是以下值之一：%s", strings.Join(serveSystemPromptModes, "、"))
	}
	if !OptionAllowed(serveToolModes, strings.TrimSpace(form.ToolMode)) {
		return numbers, i18n.LocalizedErrorf("工具可见模式必须是以下值之一：%s", strings.Join(serveToolModes, "、"))
	}
	if !OptionAllowed(serveToolDetails, strings.TrimSpace(form.ToolDetail)) {
		return numbers, i18n.LocalizedErrorf("工具详情级别必须是以下值之一：%s", strings.Join(serveToolDetails, "、"))
	}

	var err error
	if numbers.requestTimeout, err = ParseInt("请求超时", form.RequestTimeout, 1); err != nil {
		return numbers, err
	}
	if numbers.backgroundRunMax, err = ParseInt("后台运行上限", form.BackgroundRunMax, 1); err != nil {
		return numbers, err
	}
	if numbers.maxConcurrent, err = ParseInt("最大并发请求数", form.MaxConcurrent, 0); err != nil {
		return numbers, err
	}
	if numbers.cronInterval, err = ParseInt("Cron 检查间隔", form.CronInterval, 1); err != nil {
		return numbers, err
	}
	if numbers.idleTimeout, err = ParseInt("会话空闲超时", form.IdleTimeout, 1); err != nil {
		return numbers, err
	}
	if numbers.maxSessions, err = ParseInt("最大会话数", form.MaxSessions, 0); err != nil {
		return numbers, err
	}
	if numbers.maxTurns, err = ParseInt("最大轮次", form.MaxTurns, 1); err != nil {
		return numbers, err
	}
	if numbers.budgetThreshold, err = ParseFloat("预算压力阈值", form.BudgetThreshold, 0, 1); err != nil {
		return numbers, err
	}
	if numbers.contextThreshold, err = ParseFloat("上下文压力阈值", form.ContextThreshold, 0, 1); err != nil {
		return numbers, err
	}
	if numbers.runStaleTimeout, err = ParseInt("运行停滞超时", form.RunStaleTimeout, 1); err != nil {
		return numbers, err
	}
	if numbers.runMaxDuration, err = ParseInt("运行最长时长", form.RunMaxDuration, 1); err != nil {
		return numbers, err
	}
	if numbers.agentBackgroundRunMax, err = ParseInt("Agent 后台运行上限", form.AgentBackgroundRunMax, 1); err != nil {
		return numbers, err
	}
	return numbers, nil
}

func ValidateForm(form *Form) error {
	_, err := ParseForm(form)
	return err
}

// BuildPatch projects the full editor into the ACP patch envelope. Every
// writable and ACP-safe serve field is included, matching the Desktop panel;
// secrets, hooks, CORS origins, and allowed work directories are never part of
// this contract.
func BuildPatch(form Form) (map[string]any, error) {
	numbers, err := ParseForm(&form)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"patch": map[string]any{
			"api": map[string]any{
				"listen":               strings.TrimSpace(form.Listen),
				"defaultMode":          strings.TrimSpace(form.DefaultMode),
				"defaultThinkingLevel": strings.TrimSpace(form.DefaultThinking),
				"enableSubAgents":      form.MultiAgent,
				"enableDelegate":       form.EnableDelegate,
				"enableWorkflows":      form.EnableWorkflows,
				"enableWebSearch":      form.EnableWebSearch,
				"enableBrowser":        form.EnableBrowser,
				"enableArtifact":       form.EnableArtifact,
				"enableA2AMaster":      form.EnableA2AMaster,
				"toolVisibility": map[string]any{
					"mode":   strings.TrimSpace(form.ToolMode),
					"detail": strings.TrimSpace(form.ToolDetail),
				},
				"systemPromptMode":        strings.TrimSpace(form.SystemPromptMode),
				"requestTimeoutSeconds":   numbers.requestTimeout,
				"backgroundRunMaxSeconds": numbers.backgroundRunMax,
				"maxConcurrentRequests":   numbers.maxConcurrent,
				"logLevel":                strings.TrimSpace(form.LogLevel),
				"session": map[string]any{
					"idleTimeoutSeconds": numbers.idleTimeout,
					"maxSessions":        numbers.maxSessions,
				},
			},
			"features": map[string]any{
				"webUI":      form.WebUI,
				"openAIAPI":  form.OpenAIAPI,
				"multiAgent": form.MultiAgent,
				"cron":       form.CronFeature,
				"memory":     form.MemoryFeature,
			},
			"webUI": map[string]any{
				"enabled": form.WebUI,
				"dir":     strings.TrimSpace(form.WebUIDir),
			},
			"cron": map[string]any{
				"enabled":  form.CronFeature,
				"interval": numbers.cronInterval,
			},
			"memory": map[string]any{
				"enabled": form.MemoryFeature,
				"path":    strings.TrimSpace(form.MemoryPath),
			},
			"security": map[string]any{
				"smartApprovals": form.SmartApprovals,
			},
			"agent": map[string]any{
				"maxTurns":                 numbers.maxTurns,
				"budgetPressure":           form.BudgetPressure,
				"contextPressure":          form.ContextPressure,
				"budgetPressureThreshold":  numbers.budgetThreshold,
				"contextPressureThreshold": numbers.contextThreshold,
				"runStaleTimeoutSeconds":   numbers.runStaleTimeout,
				"runMaxDurationSeconds":    numbers.runMaxDuration,
				"backgroundRunMaxSecs":     numbers.agentBackgroundRunMax,
			},
			"lobsterMode": form.LobsterMode,
		},
	}, nil
}

type State struct {
	Form    Form
	Loaded  bool
	Loading bool
	Saving  bool
	Err     string

	Status        string
	StatusIsError bool

	// Seq guards every asynchronous Serve call. Bumping it invalidates any
	// in-flight get/patch response, which is how tab exits, settings close, and
	// reconnects prevent stale state from landing.
	Seq int64
}

type Panel struct {
	State
}

func NewPanel() Panel {
	return Panel{State: State{Form: NewForm()}}
}

func (p *Panel) Capable(h host.Base) bool {
	return h.FeatureEnabled("manageServeConfig")
}

func (p *Panel) Current(h host.Base, seq, reloadSeq int64) bool {
	return h.SettingsOpen() && h.SettingsTab() == "serve" &&
		seq == atomic.LoadInt64(&p.Seq) &&
		reloadSeq == h.ReloadSequence()
}

// invalidateServePanel drops transient state and makes every in-flight Serve
// response stale. Loaded is reset so re-entering the tab reloads.
func (p *Panel) Invalidate() {
	atomic.AddInt64(&p.Seq, 1)
	p.Form = NewForm()
	p.Loaded = false
	p.Loading = false
	p.Saving = false
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
}

// startServePanelLoad starts one capability-gated Serve read. It is called
// from the UI thread on tab entry.
func (p *Panel) StartLoad(h host.Base) {
	if !h.SettingsOpen() || h.SettingsTab() != "serve" {
		return
	}
	if p.Loading {
		return
	}
	if !p.Capable(h) {
		p.Loaded = true
		p.Err = "当前运行时不支持 Serve 配置管理（manageServeConfig）。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	p.Loading = true
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
	startReloadSeq := h.ReloadSequence()
	go p.load(h, seq, startReloadSeq)
}

func (p *Panel) load(h host.Base, seq, startReloadSeq int64) {
	client := h.ACP()
	if client == nil {
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Loading = false
			p.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.Call("mothx/manage/serve/get", map[string]any{})
	h.Dispatch(func() {
		if !p.Current(h, seq, startReloadSeq) {
			return
		}
		p.Loading = false
		if err != nil {
			p.Err = "读取 Serve 配置失败：" + err.Error()
			return
		}
		var view ConfigView
		if err := json.Unmarshal(raw, &view); err != nil {
			p.Err = "Serve 配置响应无效：" + err.Error()
			return
		}
		p.ApplyView(&view)
	})
}

func (p *Panel) ApplyView(view *ConfigView) {
	if view == nil {
		return
	}
	p.Form = FormFromView(view)
	p.Loaded = true
	p.Loading = false
	p.Err = ""
}

// saveServeSettings writes the full grouped editor through serve/patch. It is
// the only write path; the panel never touches serve.json directly.
func (p *Panel) Save(h host.Base) {
	if !p.Capable(h) {
		p.Status = "当前运行时不支持 Serve 配置管理（manageServeConfig）。"
		p.StatusIsError = true
		return
	}
	if p.Loading || p.Saving {
		return
	}
	request, err := BuildPatch(p.Form)
	if err != nil {
		p.Status = "保存失败：" + err.Error()
		p.StatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "保存失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}

	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Saving = true
	p.Status = "正在保存 Serve 配置…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/serve/patch", request)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Saving = false
			if err != nil {
				p.Status = "保存 Serve 配置失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view ConfigView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.Status = "保存后 Serve 配置响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.ApplyView(&view)
			p.Status = "Serve 配置已保存"
			p.StatusIsError = false
		})
	}()
}
