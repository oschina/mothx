package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/oschina/mothx/internal/mcp"
	"github.com/oschina/mothx/internal/serve"
)

// Serve config management is a thin, secret-safe projection of internal/serve.
// It only reads and writes the global serve.json (not any project/workdir
// layer), reuses the existing LoadConfig/SaveConfig path, and never returns
// auth tokens, channel credentials, CORS origins, allowed work dirs, hook
// commands, or provider secrets.

var (
	manageServePatchTopLevel = map[string]bool{
		"api":         true,
		"features":    true,
		"webUI":       true,
		"cron":        true,
		"memory":      true,
		"security":    true,
		"agent":       true,
		"lobsterMode": true,
	}

	manageServePatchAPIFields = map[string]bool{
		"listen":                  true,
		"defaultMode":             true,
		"defaultThinkingLevel":    true,
		"enableSubAgents":         true,
		"enableDelegate":          true,
		"enableWorkflows":         true,
		"enableWebSearch":         true,
		"enableBrowser":           true,
		"enableArtifact":          true,
		"enableA2AMaster":         true,
		"toolVisibility":          true,
		"systemPromptMode":        true,
		"requestTimeoutSeconds":   true,
		"backgroundRunMaxSeconds": true,
		"maxConcurrentRequests":   true,
		"logLevel":                true,
		"session":                 true,
	}

	manageServePatchAPISessionFields = map[string]bool{
		"idleTimeoutSeconds": true,
		"maxSessions":        true,
	}

	manageServePatchFeaturesFields = map[string]bool{
		"webUI":      true,
		"openAIAPI":  true,
		"multiAgent": true,
		"cron":       true,
		"memory":     true,
	}

	manageServePatchWebUIFields = map[string]bool{
		"enabled": true,
		"dir":     true,
	}

	manageServePatchCronFields = map[string]bool{
		"enabled":  true,
		"interval": true,
	}

	manageServePatchMemoryFields = map[string]bool{
		"enabled": true,
		"path":    true,
	}

	manageServePatchSecurityFields = map[string]bool{
		"smartApprovals": true,
	}

	manageServePatchAgentFields = map[string]bool{
		"maxTurns":                 true,
		"budgetPressure":           true,
		"contextPressure":          true,
		"budgetPressureThreshold":  true,
		"contextPressureThreshold": true,
		"runStaleTimeoutSeconds":   true,
		"runMaxDurationSeconds":    true,
		"backgroundRunMaxSecs":     true,
	}

	manageServePatchToolVisibilityFields = map[string]bool{
		"mode":   true,
		"detail": true,
	}
)

var manageServeAllowedLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

var manageServeAllowedToolVisibilityModes = map[string]bool{
	"content":   true,
	"sse_event": true,
	"none":      true,
}

var manageServeAllowedToolVisibilityDetails = map[string]bool{
	"collapsed": true,
	"expanded":  true,
}

var manageServeAllowedSystemPromptModes = map[string]bool{
	"append": true,
	"ignore": true,
}

func manageServeConfigView(cfg *serve.Config) map[string]any {
	return map[string]any{
		"api": map[string]any{
			"listen":               cfg.API.Listen,
			"defaultMode":          cfg.API.DefaultMode,
			"defaultThinkingLevel": cfg.API.DefaultThinkingLevel,
			"enableSubAgents":      cfg.API.EnableSubAgents,
			"enableDelegate":       cfg.API.EnableDelegate,
			"enableWorkflows":      cfg.API.EnableWorkflows,
			"enableWebSearch":      cfg.API.EnableWebSearch,
			"enableBrowser":        cfg.API.EnableBrowser,
			"enableArtifact":       cfg.API.EnableArtifact,
			"enableA2AMaster":      cfg.API.EnableA2AMaster,
			"toolVisibility": map[string]any{
				"mode":   cfg.API.ToolVisibility.Mode,
				"detail": cfg.API.ToolVisibility.Detail,
			},
			"systemPromptMode":        cfg.API.SystemPromptMode,
			"requestTimeoutSeconds":   cfg.API.RequestTimeoutSecs,
			"backgroundRunMaxSeconds": cfg.API.BackgroundRunMaxSecs,
			"maxConcurrentRequests":   cfg.API.MaxConcurrentReqs,
			"logLevel":                cfg.API.LogLevel,
			"session": map[string]any{
				"idleTimeoutSeconds": cfg.API.Session.IdleTimeoutSeconds,
				"maxSessions":        cfg.API.Session.MaxSessions,
			},
		},
		"features": map[string]any{
			"webUI":      cfg.Features.WebUI,
			"openAIAPI":  cfg.Features.OpenAIAPI,
			"multiAgent": cfg.Features.MultiAgent,
			"cron":       cfg.Features.Cron,
			"memory":     cfg.Features.Memory,
		},
		"webUI": map[string]any{
			"enabled": cfg.WebUI.Enabled,
			"dir":     cfg.WebUI.Dir,
		},
		"cron": map[string]any{
			"enabled":  cfg.Cron.Enabled,
			"interval": cfg.Cron.Interval,
		},
		"memory": map[string]any{
			"enabled": cfg.Memory.Enabled,
			"path":    cfg.Memory.Path,
		},
		"security": map[string]any{
			"smartApprovals": cfg.Security.SmartApprovals,
		},
		"agent": map[string]any{
			"maxTurns":                 cfg.Agent.MaxTurns,
			"budgetPressure":           cfg.Agent.BudgetPressure,
			"contextPressure":          cfg.Agent.ContextPressure,
			"budgetPressureThreshold":  cfg.Agent.BudgetPressureThreshold,
			"contextPressureThreshold": cfg.Agent.ContextPressureThreshold,
			"runStaleTimeoutSeconds":   cfg.Agent.RunStaleTimeoutSecs,
			"runMaxDurationSeconds":    cfg.Agent.RunMaxDurationSecs,
			"backgroundRunMaxSecs":     cfg.Agent.BackgroundRunMaxSecs,
		},
		"lobsterMode": cfg.LobsterMode,
	}
}

func manageServeLoadGlobalConfig() (*serve.Config, error) {
	return serve.LoadConfigFrom(serve.ConfigPath())
}

func manageServeSaveGlobalConfig(cfg *serve.Config) error {
	return serve.SaveConfig(serve.ConfigPath(), cfg)
}

func (s *server) handleManageServeConfigGet(req rpcRequest) {
	cfg, err := manageServeLoadGlobalConfig()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "serve_config_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, manageServeConfigView(cfg), nil)
}

type manageServePatchRequest struct {
	Patch map[string]json.RawMessage `json:"patch"`
}

func (s *server) handleManageServeConfigPatch(req rpcRequest) {
	var envelope manageServePatchRequest
	if err := json.Unmarshal(req.Params, &envelope); err != nil || len(envelope.Patch) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"patch object with at least one allowed section is required", nil))
		return
	}

	for key := range envelope.Patch {
		if !manageServePatchTopLevel[key] {
			allowed := make([]string, 0, len(manageServePatchTopLevel))
			for field := range manageServePatchTopLevel {
				allowed = append(allowed, field)
			}
			sort.Strings(allowed)
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "serve_field_not_allowed",
				fmt.Sprintf("serve config section %q is not writable", key),
				map[string]any{"field": key, "allowed": allowed}))
			return
		}
	}

	cfg, err := manageServeLoadGlobalConfig()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "serve_config_unavailable", err.Error(), nil))
		return
	}

	for _, key := range sortedKeys(envelope.Patch) {
		raw := envelope.Patch[key]
		switch key {
		case "api":
			if rpcErr := manageServePatchAPI(cfg, raw); rpcErr != nil {
				s.writeResponse(req.ID, nil, rpcErr)
				return
			}
		case "features":
			if rpcErr := manageServePatchFeatures(cfg, raw); rpcErr != nil {
				s.writeResponse(req.ID, nil, rpcErr)
				return
			}
		case "webUI":
			if rpcErr := manageServePatchWebUI(cfg, raw); rpcErr != nil {
				s.writeResponse(req.ID, nil, rpcErr)
				return
			}
		case "cron":
			if rpcErr := manageServePatchCron(cfg, raw); rpcErr != nil {
				s.writeResponse(req.ID, nil, rpcErr)
				return
			}
		case "memory":
			if rpcErr := manageServePatchMemory(cfg, raw); rpcErr != nil {
				s.writeResponse(req.ID, nil, rpcErr)
				return
			}
		case "security":
			if rpcErr := manageServePatchSecurity(cfg, raw); rpcErr != nil {
				s.writeResponse(req.ID, nil, rpcErr)
				return
			}
		case "agent":
			if rpcErr := manageServePatchAgent(cfg, raw); rpcErr != nil {
				s.writeResponse(req.ID, nil, rpcErr)
				return
			}
		case "lobsterMode":
			if rpcErr := manageServePatchLobsterMode(cfg, raw); rpcErr != nil {
				s.writeResponse(req.ID, nil, rpcErr)
				return
			}
		}
	}

	if err := manageServeSaveGlobalConfig(cfg); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "serve_config_save_failed", err.Error(), nil))
		return
	}

	s.writeResponse(req.ID, manageServeConfigView(cfg), nil)
}

func manageServePatchAPI(cfg *serve.Config, raw json.RawMessage) *mcp.RPCError {
	fields, rpcErr := manageServeDecodeSection(raw, "api", manageServePatchAPIFields)
	if rpcErr != nil {
		return rpcErr
	}
	for _, field := range sortedKeys(fields) {
		value := fields[field]
		switch field {
		case "listen":
			str, rpcErr := manageServeRequireString(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.Listen = str
		case "defaultMode":
			str, rpcErr := manageServeRequireString(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			if !manageAllowedModes[str] {
				return manageServeFieldInvalid(field, "mode must be one of agent, plan, yolo, os")
			}
			cfg.API.DefaultMode = str
		case "defaultThinkingLevel":
			str, rpcErr := manageServeRequireString(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			if !manageAllowedThinkingLevels[str] {
				return manageServeFieldInvalid(field, "thinking level must be one of off, minimal, low, medium, high, xhigh, max")
			}
			cfg.API.DefaultThinkingLevel = str
		case "enableSubAgents":
			b, rpcErr := manageServeRequireBool(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.EnableSubAgents = b
			cfg.Features.MultiAgent = b
		case "enableDelegate":
			b, rpcErr := manageServeRequireBool(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.EnableDelegate = b
		case "enableWorkflows":
			b, rpcErr := manageServeRequireBool(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.EnableWorkflows = b
		case "enableWebSearch":
			b, rpcErr := manageServeRequireBool(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.EnableWebSearch = b
		case "enableBrowser":
			b, rpcErr := manageServeRequireBool(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.EnableBrowser = b
		case "enableArtifact":
			b, rpcErr := manageServeRequireBool(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.EnableArtifact = b
		case "enableA2AMaster":
			b, rpcErr := manageServeRequireBool(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.EnableA2AMaster = b
		case "toolVisibility":
			tvFields, rpcErr := manageServeDecodeSection(value, "api.toolVisibility", manageServePatchToolVisibilityFields)
			if rpcErr != nil {
				return rpcErr
			}
			for _, tvField := range sortedKeys(tvFields) {
				tvRaw := tvFields[tvField]
				switch tvField {
				case "mode":
					str, rpcErr := manageServeRequireString(tvRaw, "toolVisibility.mode")
					if rpcErr != nil {
						return rpcErr
					}
					if !manageServeAllowedToolVisibilityModes[str] {
						return manageServeFieldInvalid("toolVisibility.mode", "mode must be one of content, sse_event, none")
					}
					cfg.API.ToolVisibility.Mode = str
				case "detail":
					str, rpcErr := manageServeRequireString(tvRaw, "toolVisibility.detail")
					if rpcErr != nil {
						return rpcErr
					}
					if !manageServeAllowedToolVisibilityDetails[str] {
						return manageServeFieldInvalid("toolVisibility.detail", "detail must be one of collapsed, expanded")
					}
					cfg.API.ToolVisibility.Detail = str
				}
			}
		case "systemPromptMode":
			str, rpcErr := manageServeRequireString(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			if !manageServeAllowedSystemPromptModes[str] {
				return manageServeFieldInvalid(field, "systemPromptMode must be one of append, ignore")
			}
			cfg.API.SystemPromptMode = str
		case "requestTimeoutSeconds":
			i, rpcErr := manageServeRequireInt(value, field, 1)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.RequestTimeoutSecs = i
		case "backgroundRunMaxSeconds":
			i, rpcErr := manageServeRequireInt(value, field, 1)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.BackgroundRunMaxSecs = i
			cfg.Agent.BackgroundRunMaxSecs = i
		case "maxConcurrentRequests":
			i, rpcErr := manageServeRequireInt(value, field, 0)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.MaxConcurrentReqs = i
		case "logLevel":
			str, rpcErr := manageServeRequireString(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			if !manageServeAllowedLogLevels[str] {
				return manageServeFieldInvalid(field, "logLevel must be one of debug, info, warn, error")
			}
			cfg.API.LogLevel = str
		case "session":
			if rpcErr := manageServePatchAPISession(cfg, value); rpcErr != nil {
				return rpcErr
			}
		}
	}
	return nil
}

func manageServePatchAPISession(cfg *serve.Config, raw json.RawMessage) *mcp.RPCError {
	fields, rpcErr := manageServeDecodeSection(raw, "api.session", manageServePatchAPISessionFields)
	if rpcErr != nil {
		return rpcErr
	}
	for _, field := range sortedKeys(fields) {
		value := fields[field]
		switch field {
		case "idleTimeoutSeconds":
			i, rpcErr := manageServeRequireInt(value, "api.session."+field, 1)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.Session.IdleTimeoutSeconds = i
		case "maxSessions":
			i, rpcErr := manageServeRequireInt(value, "api.session."+field, 0)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.API.Session.MaxSessions = i
		}
	}
	return nil
}

func manageServePatchFeatures(cfg *serve.Config, raw json.RawMessage) *mcp.RPCError {
	fields, rpcErr := manageServeDecodeSection(raw, "features", manageServePatchFeaturesFields)
	if rpcErr != nil {
		return rpcErr
	}
	for _, field := range sortedKeys(fields) {
		value := fields[field]
		b, rpcErr := manageServeRequireBool(value, "features."+field)
		if rpcErr != nil {
			return rpcErr
		}
		switch field {
		case "webUI":
			cfg.Features.WebUI = b
			cfg.WebUI.Enabled = b
		case "openAIAPI":
			cfg.Features.OpenAIAPI = b
		case "multiAgent":
			cfg.Features.MultiAgent = b
			cfg.API.EnableSubAgents = b
		case "cron":
			cfg.Features.Cron = b
			cfg.Cron.Enabled = b
		case "memory":
			cfg.Features.Memory = b
			cfg.Memory.Enabled = b
		}
	}
	return nil
}

func manageServePatchWebUI(cfg *serve.Config, raw json.RawMessage) *mcp.RPCError {
	fields, rpcErr := manageServeDecodeSection(raw, "webUI", manageServePatchWebUIFields)
	if rpcErr != nil {
		return rpcErr
	}
	for _, field := range sortedKeys(fields) {
		value := fields[field]
		switch field {
		case "enabled":
			b, rpcErr := manageServeRequireBool(value, "webUI.enabled")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.WebUI.Enabled = b
			cfg.Features.WebUI = b
		case "dir":
			str, rpcErr := manageServeRequireString(value, "webUI.dir")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.WebUI.Dir = str
		}
	}
	return nil
}

func manageServePatchCron(cfg *serve.Config, raw json.RawMessage) *mcp.RPCError {
	fields, rpcErr := manageServeDecodeSection(raw, "cron", manageServePatchCronFields)
	if rpcErr != nil {
		return rpcErr
	}
	for _, field := range sortedKeys(fields) {
		value := fields[field]
		switch field {
		case "enabled":
			b, rpcErr := manageServeRequireBool(value, "cron.enabled")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Cron.Enabled = b
			cfg.Features.Cron = b
		case "interval":
			i, rpcErr := manageServeRequireInt(value, "cron.interval", 1)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Cron.Interval = i
		}
	}
	return nil
}

func manageServePatchMemory(cfg *serve.Config, raw json.RawMessage) *mcp.RPCError {
	fields, rpcErr := manageServeDecodeSection(raw, "memory", manageServePatchMemoryFields)
	if rpcErr != nil {
		return rpcErr
	}
	for _, field := range sortedKeys(fields) {
		value := fields[field]
		switch field {
		case "enabled":
			b, rpcErr := manageServeRequireBool(value, "memory.enabled")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Memory.Enabled = b
			cfg.Features.Memory = b
		case "path":
			str, present, err := manageDecodeOptionalString(value)
			if err != nil {
				return manageServeFieldInvalid("memory.path", "value must be a string")
			}
			if present {
				cfg.Memory.Path = str
			}
		}
	}
	return nil
}

func manageServePatchSecurity(cfg *serve.Config, raw json.RawMessage) *mcp.RPCError {
	fields, rpcErr := manageServeDecodeSection(raw, "security", manageServePatchSecurityFields)
	if rpcErr != nil {
		return rpcErr
	}
	for _, field := range sortedKeys(fields) {
		value := fields[field]
		switch field {
		case "smartApprovals":
			b, rpcErr := manageServeRequireBool(value, "security.smartApprovals")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Security.SmartApprovals = b
		}
	}
	return nil
}

func manageServePatchAgent(cfg *serve.Config, raw json.RawMessage) *mcp.RPCError {
	fields, rpcErr := manageServeDecodeSection(raw, "agent", manageServePatchAgentFields)
	if rpcErr != nil {
		return rpcErr
	}
	for _, field := range sortedKeys(fields) {
		value := fields[field]
		switch field {
		case "maxTurns":
			i, rpcErr := manageServeRequireInt(value, field, 1)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Agent.MaxTurns = i
		case "budgetPressure":
			b, rpcErr := manageServeRequireBool(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Agent.BudgetPressure = b
		case "contextPressure":
			b, rpcErr := manageServeRequireBool(value, field)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Agent.ContextPressure = b
		case "budgetPressureThreshold":
			f, rpcErr := manageServeRequireFloat(value, field, 0, 1)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Agent.BudgetPressureThreshold = f
		case "contextPressureThreshold":
			f, rpcErr := manageServeRequireFloat(value, field, 0, 1)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Agent.ContextPressureThreshold = f
		case "runStaleTimeoutSeconds":
			i, rpcErr := manageServeRequireInt(value, field, 1)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Agent.RunStaleTimeoutSecs = i
		case "runMaxDurationSeconds":
			i, rpcErr := manageServeRequireInt(value, field, 1)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Agent.RunMaxDurationSecs = i
		case "backgroundRunMaxSecs":
			i, rpcErr := manageServeRequireInt(value, field, 1)
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Agent.BackgroundRunMaxSecs = i
		}
	}
	return nil
}

func manageServePatchLobsterMode(cfg *serve.Config, raw json.RawMessage) *mcp.RPCError {
	b, rpcErr := manageServeRequireBool(raw, "lobsterMode")
	if rpcErr != nil {
		return rpcErr
	}
	cfg.LobsterMode = b
	return nil
}

// manageServeDecodeSection applies the Serve management whitelist to one
// nested section. json.Unmarshal accepts null into a map, which would turn a
// malformed ACP patch into a silent no-op, so reject null and empty objects
// explicitly at this public boundary.
func manageServeDecodeSection(raw json.RawMessage, section string, allowed map[string]bool) (map[string]json.RawMessage, *mcp.RPCError) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, manageServeFieldInvalid(section, "a non-empty object is required")
	}
	fields, rpcErr := manageDecodeWhitelist(trimmed, allowed, "serve_field_not_allowed")
	if rpcErr != nil {
		return nil, rpcErr
	}
	if len(fields) == 0 {
		return nil, manageServeFieldInvalid(section, "a non-empty object is required")
	}
	return fields, nil
}

func manageServeRequireString(raw json.RawMessage, field string) (string, *mcp.RPCError) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || strings.TrimSpace(s) == "" {
		return "", manageServeFieldInvalid(field, "a non-empty string is required")
	}
	return strings.TrimSpace(s), nil
}

func manageServeRequireBool(raw json.RawMessage, field string) (bool, *mcp.RPCError) {
	trimmed := bytes.TrimSpace(raw)
	var b bool
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || json.Unmarshal(trimmed, &b) != nil {
		return false, manageServeFieldInvalid(field, "a boolean value is required")
	}
	return b, nil
}

func manageServeRequireInt(raw json.RawMessage, field string, min int) (int, *mcp.RPCError) {
	trimmed := bytes.TrimSpace(raw)
	var i int
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || json.Unmarshal(trimmed, &i) != nil || i < min {
		return 0, manageServeFieldInvalid(field, fmt.Sprintf("an integer >= %d is required", min))
	}
	return i, nil
}

func manageServeRequireFloat(raw json.RawMessage, field string, min, max float64) (float64, *mcp.RPCError) {
	trimmed := bytes.TrimSpace(raw)
	var f float64
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || json.Unmarshal(trimmed, &f) != nil || f < min || f > max {
		return 0, manageServeFieldInvalid(field, fmt.Sprintf("a number between %.0f and %.0f is required", min, max))
	}
	return f, nil
}

func manageServeFieldInvalid(field, message string) *mcp.RPCError {
	return acpStructuredRPCError(-32602, "serve_field_invalid",
		fmt.Sprintf("field %s: %s", field, message),
		map[string]any{"field": field})
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- §6.x channels (WeChat/Feishu) -------------------------------------------
//
// Channel configuration is a thin, secret-safe projection of serve.json. Only
// global serve.json is edited; no project/workdir layer is consulted. Secrets
// are write-only: GET returns only boolean configured flags, never the stored
// credential path, app id, or app secret.

type manageChannelsView struct {
	Artifact bool                     `json:"artifact"`
	Wechat   manageChannelsWechatView `json:"wechat"`
	Feishu   manageChannelsFeishuView `json:"feishu"`
}

type manageChannelsWechatView struct {
	Enabled              bool   `json:"enabled"`
	WorkDir              string `json:"workDir"`
	AutoTyping           bool   `json:"autoTyping"`
	CredentialConfigured bool   `json:"credentialConfigured"`
}

type manageChannelsFeishuView struct {
	Enabled             bool   `json:"enabled"`
	WorkDir             string `json:"workDir"`
	AppIDConfigured     bool   `json:"appIDConfigured"`
	AppSecretConfigured bool   `json:"appSecretConfigured"`
}

var (
	manageChannelsPatchTopLevel = map[string]bool{
		"artifact": true,
		"wechat":   true,
		"feishu":   true,
	}

	manageChannelsWechatFields = map[string]bool{
		"enabled":       true,
		"workDir":       true,
		"autoTyping":    true,
		"credPath":      true,
		"clearCredPath": true,
	}

	manageChannelsFeishuFields = map[string]bool{
		"enabled":        true,
		"workDir":        true,
		"appId":          true,
		"appSecret":      true,
		"clearAppId":     true,
		"clearAppSecret": true,
	}
)

func manageChannelsConfigView(cfg *serve.Config) manageChannelsView {
	return manageChannelsView{
		Artifact: cfg.Channels.Artifact,
		Wechat: manageChannelsWechatView{
			Enabled:              cfg.Channels.Wechat.Enabled,
			WorkDir:              cfg.Channels.Wechat.WorkDir,
			AutoTyping:           cfg.Channels.Wechat.AutoTyping,
			CredentialConfigured: cfg.Channels.Wechat.CredPath != "",
		},
		Feishu: manageChannelsFeishuView{
			Enabled:             cfg.Channels.Feishu.Enabled,
			WorkDir:             cfg.Channels.Feishu.WorkDir,
			AppIDConfigured:     cfg.Channels.Feishu.AppID != "",
			AppSecretConfigured: cfg.Channels.Feishu.AppSecret != "",
		},
	}
}

func (s *server) handleManageChannelsGet(req rpcRequest) {
	cfg, err := manageServeLoadGlobalConfig()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "serve_config_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, manageChannelsConfigView(cfg), nil)
}

type manageChannelsPatchRequest struct {
	Patch map[string]json.RawMessage `json:"patch"`
}

func (s *server) handleManageChannelsPatch(req rpcRequest) {
	var envelope manageChannelsPatchRequest
	if err := json.Unmarshal(req.Params, &envelope); err != nil || len(envelope.Patch) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"patch object with at least one allowed section is required", nil))
		return
	}

	cfg, err := manageServeLoadGlobalConfig()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "serve_config_unavailable", err.Error(), nil))
		return
	}

	for _, key := range sortedKeys(envelope.Patch) {
		raw := envelope.Patch[key]
		if !manageChannelsPatchTopLevel[key] {
			allowed := make([]string, 0, len(manageChannelsPatchTopLevel))
			for field := range manageChannelsPatchTopLevel {
				allowed = append(allowed, field)
			}
			sort.Strings(allowed)
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "serve_field_not_allowed",
				fmt.Sprintf("channel config section %q is not writable", key),
				map[string]any{"field": key, "allowed": allowed}))
			return
		}
		if key == "artifact" {
			value, rpcErr := manageServeRequireBool(raw, "artifact")
			if rpcErr != nil {
				s.writeResponse(req.ID, nil, rpcErr)
				return
			}
			cfg.Channels.Artifact = value
			continue
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			s.writeResponse(req.ID, nil, manageServeFieldInvalid(key, "a non-empty object is required"))
			return
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &fields); err != nil {
			s.writeResponse(req.ID, nil, manageServeFieldInvalid(key, "value must be an object"))
			return
		}
		if len(fields) == 0 {
			s.writeResponse(req.ID, nil, manageServeFieldInvalid(key, "a non-empty object is required"))
			return
		}
		var rpcErr *mcp.RPCError
		switch key {
		case "wechat":
			rpcErr = manageChannelsPatchWechat(cfg, fields)
		case "feishu":
			rpcErr = manageChannelsPatchFeishu(cfg, fields)
		}
		if rpcErr != nil {
			s.writeResponse(req.ID, nil, rpcErr)
			return
		}
	}

	if err := manageServeSaveGlobalConfig(cfg); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "serve_config_save_failed", err.Error(), nil))
		return
	}

	s.writeResponse(req.ID, manageChannelsConfigView(cfg), nil)
}

func manageChannelsPatchWechat(cfg *serve.Config, fields map[string]json.RawMessage) *mcp.RPCError {
	if _, hasSet := fields["credPath"]; hasSet {
		if _, hasClear := fields["clearCredPath"]; hasClear {
			return manageServeFieldInvalid("wechat.credPath", "credPath and clearCredPath cannot both be set")
		}
	}
	for _, field := range sortedKeys(fields) {
		raw := fields[field]
		if !manageChannelsWechatFields[field] {
			return manageServeFieldInvalid("wechat."+field, "field is not allowed")
		}
		switch field {
		case "enabled":
			b, rpcErr := manageServeRequireBool(raw, "wechat.enabled")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Channels.Wechat.Enabled = b
			cfg.Features.Wechat = b
		case "workDir":
			str, rpcErr := manageServeRequireString(raw, "wechat.workDir")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Channels.Wechat.WorkDir = str
		case "autoTyping":
			b, rpcErr := manageServeRequireBool(raw, "wechat.autoTyping")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Channels.Wechat.AutoTyping = b
		case "credPath":
			str, rpcErr := manageServeRequireString(raw, "wechat.credPath")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Channels.Wechat.CredPath = str
		case "clearCredPath":
			b, rpcErr := manageServeRequireBool(raw, "wechat.clearCredPath")
			if rpcErr != nil {
				return rpcErr
			}
			if b {
				cfg.Channels.Wechat.CredPath = ""
			}
		}
	}
	return nil
}

func manageChannelsPatchFeishu(cfg *serve.Config, fields map[string]json.RawMessage) *mcp.RPCError {
	if _, hasSet := fields["appId"]; hasSet {
		if _, hasClear := fields["clearAppId"]; hasClear {
			return manageServeFieldInvalid("feishu.appId", "appId and clearAppId cannot both be set")
		}
	}
	if _, hasSet := fields["appSecret"]; hasSet {
		if _, hasClear := fields["clearAppSecret"]; hasClear {
			return manageServeFieldInvalid("feishu.appSecret", "appSecret and clearAppSecret cannot both be set")
		}
	}
	for _, field := range sortedKeys(fields) {
		raw := fields[field]
		if !manageChannelsFeishuFields[field] {
			return manageServeFieldInvalid("feishu."+field, "field is not allowed")
		}
		switch field {
		case "enabled":
			b, rpcErr := manageServeRequireBool(raw, "feishu.enabled")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Channels.Feishu.Enabled = b
			cfg.Features.Feishu = b
		case "workDir":
			str, rpcErr := manageServeRequireString(raw, "feishu.workDir")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Channels.Feishu.WorkDir = str
		case "appId":
			str, rpcErr := manageServeRequireString(raw, "feishu.appId")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Channels.Feishu.AppID = str
		case "appSecret":
			str, rpcErr := manageServeRequireString(raw, "feishu.appSecret")
			if rpcErr != nil {
				return rpcErr
			}
			cfg.Channels.Feishu.AppSecret = str
		case "clearAppId":
			b, rpcErr := manageServeRequireBool(raw, "feishu.clearAppId")
			if rpcErr != nil {
				return rpcErr
			}
			if b {
				cfg.Channels.Feishu.AppID = ""
			}
		case "clearAppSecret":
			b, rpcErr := manageServeRequireBool(raw, "feishu.clearAppSecret")
			if rpcErr != nil {
				return rpcErr
			}
			if b {
				cfg.Channels.Feishu.AppSecret = ""
			}
		}
	}
	return nil
}
