package main

import (
	"strconv"
	"strings"
)

// applicationSettingsForm holds the editable state of the application
// settings dialog. Numeric fields are stored as strings so the UI can show
// empty inputs; they are validated and converted at patch time.
type applicationSettingsForm struct {
	// defaults
	DefaultMode       string
	EnablePlanTool    bool
	EnableArtifact    bool
	EnableACPArtifact bool
	Authored          bool
	UpdateCheck       bool

	// contextFiles
	ContextEnabled bool
	ExtraFiles     string

	// compaction
	CompactionEnabled  bool
	ReserveTokens      string
	KeepRecentTokens   string
	Tokenizer          string
	TokenizerModel     string
	CompactionTemplate string

	// toolExecution
	ToolMode        string
	ToolConcurrency string

	// webSearch
	WebSearchEnabled         bool
	WebSearchBackend         string
	WebSearchProvider        string
	WebSearchBaseURL         string
	WebSearchToken           string
	WebSearchTokenConfigured bool
	ClearWebSearchToken      bool

	// imageGeneration
	ImageEnabled         bool
	ImageProvider        string
	ImageAPI             string
	ImageBaseURL         string
	ImageModel           string
	ImageToken           string
	ImageTokenConfigured bool
	ClearImageToken      bool

	// retry
	RetryEnabled bool
	MaxRetries   string
	BaseDelay    string

	// statusLine
	StatusLineEnabled bool
	StatusType        string
	StatusCommand     string
	StatusPadding     string
	StatusRefresh     string
	StatusTimeout     string
	StatusFallback    string

	// sandbox
	SandboxEnabled bool
	AllowNetwork   bool
	ProtectGit     bool
	SandboxLevel   string
	BwrapPath      string
	TmpSize        string
	AllowedRead    string
	AllowedWrite   string
	DeniedPaths    string

	// approval
	ConfirmWrite  bool
	BashWhitelist string
	BashBlacklist string
}

func newApplicationSettingsForm() applicationSettingsForm {
	return applicationSettingsForm{
		DefaultMode:       "yolo",
		UpdateCheck:       true,
		ContextEnabled:    true,
		CompactionEnabled: true,
		WebSearchBackend:  "deepseek",
		ToolMode:          "parallel",
		ToolConcurrency:   "1",
		RetryEnabled:      true,
		SandboxLevel:      "none",
	}
}

func applicationSettingsFormFromView(view *applicationSettingsView) applicationSettingsForm {
	f := newApplicationSettingsForm()
	if view == nil {
		return f
	}

	f.DefaultMode = view.Defaults.DefaultMode
	f.EnablePlanTool = view.Defaults.EnablePlanTool
	f.EnableArtifact = view.Defaults.EnableArtifact
	f.EnableACPArtifact = view.Defaults.EnableACPArtifact
	f.Authored = view.Defaults.Authored
	f.UpdateCheck = view.Defaults.UpdateCheck

	f.ContextEnabled = view.ContextFiles.Enabled
	f.ExtraFiles = strings.Join(view.ContextFiles.ExtraFiles, "\n")

	f.CompactionEnabled = view.Compaction.Enabled
	f.ReserveTokens = strconv.Itoa(view.Compaction.ReserveTokens)
	f.KeepRecentTokens = strconv.Itoa(view.Compaction.KeepRecentTokens)
	f.Tokenizer = view.Compaction.Tokenizer
	f.TokenizerModel = view.Compaction.TokenizerModel
	f.CompactionTemplate = view.Compaction.Template

	if view.ToolExecution.Mode != "" {
		f.ToolMode = view.ToolExecution.Mode
	}
	if view.ToolExecution.MaxConcurrency > 0 {
		f.ToolConcurrency = strconv.Itoa(view.ToolExecution.MaxConcurrency)
	}

	f.WebSearchEnabled = view.WebSearch.Enabled
	if view.WebSearch.Backend != "" {
		f.WebSearchBackend = view.WebSearch.Backend
	}
	f.WebSearchProvider = view.WebSearch.Provider
	f.WebSearchBaseURL = view.WebSearch.BaseURL
	f.WebSearchTokenConfigured = view.WebSearch.TokenConfigured

	f.ImageEnabled = view.ImageGeneration.Enabled
	f.ImageProvider = view.ImageGeneration.Provider
	f.ImageAPI = view.ImageGeneration.APIType
	f.ImageBaseURL = view.ImageGeneration.BaseURL
	f.ImageModel = view.ImageGeneration.Model
	f.ImageTokenConfigured = view.ImageGeneration.TokenConfigured

	f.RetryEnabled = view.Retry.Enabled
	f.MaxRetries = strconv.Itoa(view.Retry.MaxRetries)
	f.BaseDelay = strconv.Itoa(view.Retry.BaseDelayMs)

	f.StatusLineEnabled = view.StatusLine.Enabled
	f.StatusType = view.StatusLine.Type
	f.StatusCommand = view.StatusLine.Command
	f.StatusPadding = strconv.Itoa(view.StatusLine.Padding)
	f.StatusRefresh = strconv.Itoa(view.StatusLine.RefreshInterval)
	f.StatusTimeout = strconv.Itoa(view.StatusLine.TimeoutMs)
	f.StatusFallback = view.StatusLine.Fallback

	f.SandboxEnabled = view.Sandbox.Enabled
	if view.Sandbox.Level != "" {
		f.SandboxLevel = view.Sandbox.Level
	}
	f.BwrapPath = view.Sandbox.BwrapPath
	f.AllowNetwork = view.Sandbox.AllowNetwork
	f.AllowedRead = strings.Join(view.Sandbox.AllowedRead, "\n")
	f.AllowedWrite = strings.Join(view.Sandbox.AllowedWrite, "\n")
	f.DeniedPaths = strings.Join(view.Sandbox.DeniedPaths, "\n")
	f.TmpSize = view.Sandbox.TmpSize
	f.ProtectGit = view.Sandbox.ProtectGit

	f.ConfirmWrite = view.Approval.ConfirmBeforeWrite
	f.BashWhitelist = strings.Join(view.Approval.BashWhitelist, "\n")
	f.BashBlacklist = strings.Join(view.Approval.BashBlacklist, "\n")

	return f
}

func applicationLines(s string) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func applicationInt(s string, min int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < min {
		return min
	}
	return n
}

func applicationStringListsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func defaultsDirty(view applicationSettingsDefaults, form *applicationSettingsForm) bool {
	return view.DefaultMode != form.DefaultMode ||
		view.EnablePlanTool != form.EnablePlanTool ||
		view.EnableArtifact != form.EnableArtifact ||
		view.EnableACPArtifact != form.EnableACPArtifact ||
		view.Authored != form.Authored ||
		view.UpdateCheck != form.UpdateCheck
}

func contextFilesDirty(view applicationContextFilesView, form *applicationSettingsForm) bool {
	return view.Enabled != form.ContextEnabled ||
		!applicationStringListsEqual(view.ExtraFiles, applicationLines(form.ExtraFiles))
}

func compactionDirty(view applicationCompactionView, form *applicationSettingsForm) bool {
	return view.Enabled != form.CompactionEnabled ||
		view.ReserveTokens != applicationInt(form.ReserveTokens, 0) ||
		view.KeepRecentTokens != applicationInt(form.KeepRecentTokens, 0) ||
		view.Tokenizer != form.Tokenizer ||
		view.TokenizerModel != form.TokenizerModel ||
		view.Template != form.CompactionTemplate
}

func toolExecutionDirty(view applicationToolExecutionView, form *applicationSettingsForm) bool {
	mode := view.Mode
	if mode == "" {
		mode = "parallel"
	}
	maxConcurrency := view.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	return mode != form.ToolMode ||
		maxConcurrency != applicationInt(form.ToolConcurrency, 1)
}

func webSearchDirty(view applicationWebSearchView, form *applicationSettingsForm) bool {
	backend := view.Backend
	if backend == "" {
		backend = "deepseek"
	}
	return view.Enabled != form.WebSearchEnabled ||
		backend != form.WebSearchBackend ||
		view.Provider != form.WebSearchProvider ||
		view.BaseURL != form.WebSearchBaseURL ||
		form.WebSearchToken != "" || form.ClearWebSearchToken
}

func imageGenerationDirty(view applicationImageGenerationView, form *applicationSettingsForm) bool {
	return view.Enabled != form.ImageEnabled ||
		view.Provider != form.ImageProvider ||
		view.APIType != form.ImageAPI ||
		view.BaseURL != form.ImageBaseURL ||
		view.Model != form.ImageModel ||
		form.ImageToken != "" || form.ClearImageToken
}

func retryDirty(view applicationRetryView, form *applicationSettingsForm) bool {
	return view.Enabled != form.RetryEnabled ||
		view.MaxRetries != applicationInt(form.MaxRetries, 0) ||
		view.BaseDelayMs != applicationInt(form.BaseDelay, 0)
}

func statusLineDirty(view applicationStatusLineView, form *applicationSettingsForm) bool {
	return view.Enabled != form.StatusLineEnabled ||
		view.Type != form.StatusType ||
		view.Command != form.StatusCommand ||
		view.Padding != applicationInt(form.StatusPadding, 0) ||
		view.RefreshInterval != applicationInt(form.StatusRefresh, 0) ||
		view.TimeoutMs != applicationInt(form.StatusTimeout, 0) ||
		view.Fallback != form.StatusFallback
}

func sandboxDirty(view applicationSandboxView, form *applicationSettingsForm) bool {
	level := view.Level
	if level == "" {
		level = "none"
	}
	return view.Enabled != form.SandboxEnabled ||
		level != form.SandboxLevel ||
		view.BwrapPath != form.BwrapPath ||
		view.AllowNetwork != form.AllowNetwork ||
		!applicationStringListsEqual(view.AllowedRead, applicationLines(form.AllowedRead)) ||
		!applicationStringListsEqual(view.AllowedWrite, applicationLines(form.AllowedWrite)) ||
		!applicationStringListsEqual(view.DeniedPaths, applicationLines(form.DeniedPaths)) ||
		view.TmpSize != form.TmpSize ||
		view.ProtectGit != form.ProtectGit
}

func approvalDirty(view applicationApprovalView, form *applicationSettingsForm) bool {
	return view.ConfirmBeforeWrite != form.ConfirmWrite ||
		!applicationStringListsEqual(view.BashWhitelist, applicationLines(form.BashWhitelist)) ||
		!applicationStringListsEqual(view.BashBlacklist, applicationLines(form.BashBlacklist))
}

// applicationSettingsPatch builds a minimal patch containing only the sections
// that differ from the loaded view. Tokens are included only when explicitly
// set (non-blank) or cleared; otherwise the token field is omitted so a mere
// open/save cycle cannot overwrite stored credentials.
func applicationSettingsPatch(view *applicationSettingsView, form *applicationSettingsForm) map[string]any {
	patch := map[string]any{}
	if view == nil {
		return patch
	}

	if defaultsDirty(view.Defaults, form) {
		patch["defaults"] = map[string]any{
			"defaultMode":       form.DefaultMode,
			"enablePlanTool":    form.EnablePlanTool,
			"enableArtifact":    form.EnableArtifact,
			"enableACPArtifact": form.EnableACPArtifact,
			"authored":          form.Authored,
			"updateCheck":       form.UpdateCheck,
		}
	}

	if contextFilesDirty(view.ContextFiles, form) {
		patch["contextFiles"] = map[string]any{
			"enabled":    form.ContextEnabled,
			"extraFiles": applicationLines(form.ExtraFiles),
		}
	}

	if compactionDirty(view.Compaction, form) {
		patch["compaction"] = map[string]any{
			"enabled":          form.CompactionEnabled,
			"reserveTokens":    applicationInt(form.ReserveTokens, 0),
			"keepRecentTokens": applicationInt(form.KeepRecentTokens, 0),
			"tokenizer":        form.Tokenizer,
			"tokenizerModel":   form.TokenizerModel,
			"template":         form.CompactionTemplate,
		}
	}

	if toolExecutionDirty(view.ToolExecution, form) {
		patch["toolExecution"] = map[string]any{
			"mode":           form.ToolMode,
			"maxConcurrency": applicationInt(form.ToolConcurrency, 1),
		}
	}

	if webSearchDirty(view.WebSearch, form) {
		web := map[string]any{
			"enabled":  form.WebSearchEnabled,
			"backend":  form.WebSearchBackend,
			"provider": form.WebSearchProvider,
			"baseUrl":  form.WebSearchBaseURL,
		}
		if form.WebSearchToken != "" {
			web["token"] = form.WebSearchToken
		} else if form.ClearWebSearchToken {
			web["token"] = ""
		}
		patch["webSearch"] = web
	}

	if imageGenerationDirty(view.ImageGeneration, form) {
		img := map[string]any{
			"enabled":  form.ImageEnabled,
			"provider": form.ImageProvider,
			"apiType":  form.ImageAPI,
			"baseUrl":  form.ImageBaseURL,
			"model":    form.ImageModel,
		}
		if form.ImageToken != "" {
			img["token"] = form.ImageToken
		} else if form.ClearImageToken {
			img["token"] = ""
		}
		patch["imageGeneration"] = img
	}

	if retryDirty(view.Retry, form) {
		patch["retry"] = map[string]any{
			"enabled":     form.RetryEnabled,
			"maxRetries":  applicationInt(form.MaxRetries, 0),
			"baseDelayMs": applicationInt(form.BaseDelay, 0),
		}
	}

	if statusLineDirty(view.StatusLine, form) {
		patch["statusLine"] = map[string]any{
			"enabled":         form.StatusLineEnabled,
			"type":            form.StatusType,
			"command":         form.StatusCommand,
			"padding":         applicationInt(form.StatusPadding, 0),
			"refreshInterval": applicationInt(form.StatusRefresh, 0),
			"timeoutMs":       applicationInt(form.StatusTimeout, 0),
			"fallback":        form.StatusFallback,
		}
	}

	if sandboxDirty(view.Sandbox, form) {
		patch["sandbox"] = map[string]any{
			"enabled":      form.SandboxEnabled,
			"level":        form.SandboxLevel,
			"bwrapPath":    form.BwrapPath,
			"allowNetwork": form.AllowNetwork,
			"allowedRead":  applicationLines(form.AllowedRead),
			"allowedWrite": applicationLines(form.AllowedWrite),
			"deniedPaths":  applicationLines(form.DeniedPaths),
			"tmpSize":      form.TmpSize,
			"protectGit":   form.ProtectGit,
		}
	}

	if approvalDirty(view.Approval, form) {
		patch["approval"] = map[string]any{
			"confirmBeforeWrite": form.ConfirmWrite,
			"bashWhitelist":      applicationLines(form.BashWhitelist),
			"bashBlacklist":      applicationLines(form.BashBlacklist),
		}
	}

	return patch
}
