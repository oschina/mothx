package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/mcp"
	"github.com/startvibecoding/mothx/internal/memory"
	"github.com/startvibecoding/mothx/internal/provider"
	providerfactory "github.com/startvibecoding/mothx/internal/provider/factory"
	"github.com/startvibecoding/mothx/internal/serve"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/skills"
	"github.com/startvibecoding/mothx/internal/stats"
)

// This file hosts the Phase 3 management-plane extension methods of the
// desktop ACP gap plan (docs/proposal/desktop-acp-frontend-gap-proposal.md
// §6.1, mothx/manage/*). Every handler is a thin projection of existing
// internal services — config, provider factory, skills, mcp.json, cron
// store/scheduler, stats DB queries, and the memory store — so no business
// logic is duplicated here and no adapter-local storage is created. ACP v1
// and all pre-existing field semantics stay unchanged; the methods are purely
// additive and discoverable through initialize._meta.mothx.dev.features.
//
// Secret safety model (§3.3): API keys are only ever projected masked
// (sk-***abc), writes go through the strict settings/patch whitelist, and
// provider connectivity probes never echo key material in responses, errors,
// or logs.

const (
	// manageProviderTestTimeout caps one minimal provider probe call.
	manageProviderTestTimeout = 5 * time.Second
	// manageMemoryMaxBytes caps one memory put payload (1MB).
	manageMemoryMaxBytes = 1 << 20
	// manageStatsDefaultDays is the default timeseries window.
	manageStatsDefaultDays = 14
	// defaultManageCronInterval mirrors the serve/TUI scheduler tick.
	defaultManageCronInterval = 30 * time.Second
)

// handleManageRequest routes the mothx/manage/* extension family. Unknown
// management methods receive a structured manage_method_not_found error.
func (s *server) handleManageRequest(req rpcRequest) {
	switch req.Method {
	case "mothx/manage/settings/get":
		s.handleManageSettingsGet(req)
	case "mothx/manage/settings/patch":
		s.handleManageSettingsPatch(req)
	case "mothx/manage/application/get":
		s.handleManageApplicationGet(req)
	case "mothx/manage/application/patch":
		s.handleManageApplicationPatch(req)
	case "mothx/manage/serve/get":
		s.handleManageServeConfigGet(req)
	case "mothx/manage/serve/patch":
		s.handleManageServeConfigPatch(req)
	case "mothx/manage/channels/get":
		s.handleManageChannelsGet(req)
	case "mothx/manage/channels/patch":
		s.handleManageChannelsPatch(req)
	case "mothx/manage/providers/list":
		s.handleManageProvidersList(req)
	case "mothx/manage/providers/save":
		s.handleManageProvidersSave(req)
	case "mothx/manage/providers/delete":
		s.handleManageProvidersDelete(req)
	case "mothx/manage/providers/discover":
		s.handleManageProvidersDiscover(req)
	case "mothx/manage/providers/test":
		s.handleManageProvidersTest(req)
	case "mothx/manage/skills/list":
		s.handleManageSkillsList(req)
	case "mothx/manage/skills/set":
		s.handleManageSkillsSet(req)
	case "mothx/manage/mcp/list":
		s.handleManageMCPList(req)
	case "mothx/manage/mcp/set":
		s.handleManageMCPSet(req)
	case "mothx/manage/cron/list":
		s.handleManageCronList(req)
	case "mothx/manage/cron/create":
		s.handleManageCronCreate(req)
	case "mothx/manage/cron/update":
		s.handleManageCronUpdate(req)
	case "mothx/manage/cron/remove":
		s.handleManageCronRemove(req)
	case "mothx/manage/cron/run":
		s.handleManageCronRun(req)
	case "mothx/manage/stats/summary":
		s.handleManageStatsSummary(req)
	case "mothx/manage/stats/timeseries":
		s.handleManageStatsTimeseries(req)
	case "mothx/manage/memory/get":
		s.handleManageMemoryGet(req)
	case "mothx/manage/memory/put":
		s.handleManageMemoryPut(req)
	case "mothx/manage/deliveries/list":
		s.handleManageDeliveriesList(req)
	case "mothx/manage/deliveries/retry":
		s.handleManageDeliveriesRetry(req)
	case "mothx/manage/skillhub/get":
		s.handleManageSkillHubGet(req)
	case "mothx/manage/skillhub/patch":
		s.handleManageSkillHubPatch(req)
	case "mothx/manage/skillhub/markets":
		s.handleManageSkillHubMarkets(req)
	case "mothx/manage/skillhub/categories":
		s.handleManageSkillHubCategories(req)
	case "mothx/manage/skillhub/official":
		s.handleManageSkillHubOfficial(req)
	case "mothx/manage/skillhub/search":
		s.handleManageSkillHubSearch(req)
	case "mothx/manage/skillhub/detail":
		s.handleManageSkillHubDetail(req)
	case "mothx/manage/skillhub/targets":
		s.handleManageSkillHubTargets(req)
	case "mothx/manage/skillhub/installed":
		s.handleManageSkillHubInstalled(req)
	case "mothx/manage/skillhub/install":
		s.handleManageSkillHubInstall(req)
	case "mothx/manage/skillhub/activate":
		s.handleManageSkillHubActivate(req)
	case "mothx/manage/skillhub/uninstall":
		s.handleManageSkillHubUninstall(req)
	case "mothx/manage/experts/list":
		s.handleManageExpertsList(req)
	case "mothx/manage/experts/get":
		s.handleManageExpertsGet(req)
	case "mothx/manage/experts/create":
		s.handleManageExpertsCreate(req)
	case "mothx/manage/experts/update":
		s.handleManageExpertsUpdate(req)
	case "mothx/manage/experts/delete":
		s.handleManageExpertsDelete(req)
	case "mothx/manage/knowledge-bases/list":
		s.handleManageKnowledgeBasesList(req)
	case "mothx/manage/knowledge-bases/get":
		s.handleManageKnowledgeBasesGet(req)
	case "mothx/manage/knowledge-bases/create":
		s.handleManageKnowledgeBasesCreate(req)
	case "mothx/manage/knowledge-bases/update":
		s.handleManageKnowledgeBasesUpdate(req)
	case "mothx/manage/knowledge-bases/delete":
		s.handleManageKnowledgeBasesDelete(req)
	case "mothx/manage/knowledge-bases/scan":
		s.handleManageKnowledgeBasesScan(req)
	case "mothx/manage/knowledge-bases/status":
		s.handleManageKnowledgeBasesStatus(req)
	case "mothx/manage/knowledge-bases/query":
		s.handleManageKnowledgeBasesQuery(req)
	case "mothx/manage/env/get":
		s.handleManageEnvGet(req)
	case "mothx/manage/env/patch":
		s.handleManageEnvPatch(req)
	default:
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32601, "manage_method_not_found",
			fmt.Sprintf("unknown management method %q", req.Method), nil))
	}
}

// --- shared manage helpers ----------------------------------------------------

// manageSettings loads the effective settings (defaults merged with global
// and project files) exactly like every other runtime entry point.
func (s *server) manageSettings() (*config.Settings, error) {
	settings, err := config.LoadSettings()
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	return settings, nil
}

// manageWorkDir returns the negotiated workspace cwd, falling back to the
// process cwd for direct/unit fixtures without workspace metadata.
func (s *server) manageWorkDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workspaceCwd != "" {
		return s.workspaceCwd
	}
	return s.cwd
}

// manageSecretUsable reports whether a resolved key value is an actual
// secret. Unresolved ${ENV} placeholders and !shell references are config
// syntax, not key material, and project as "no key configured".
func manageSecretUsable(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	return !strings.HasPrefix(value, "${") && !strings.HasPrefix(value, "!")
}

// manageMaskSecret masks a resolved key as prefix + *** + suffix so clients
// can recognize which key is configured without ever seeing the plaintext.
func manageMaskSecret(value string) string {
	if len(value) <= 6 {
		return "***"
	}
	return value[:3] + "***" + value[len(value)-3:]
}

// manageMaskedKey projects one provider's masked key, or nil when no usable
// key is configured.
func manageMaskedKey(settings *config.Settings, providerName string) *string {
	resolved := settings.ResolveKey(providerName)
	if !manageSecretUsable(resolved) {
		return nil
	}
	masked := manageMaskSecret(resolved)
	return &masked
}

// manageRedactSecrets defensively scrubs every configured provider key from
// an outgoing human-readable message. Provider and factory errors normally do
// not embed credentials; this keeps the guarantee even if an upstream error
// text changes.
func manageRedactSecrets(message string, settings *config.Settings) string {
	if settings == nil || message == "" {
		return message
	}
	for name := range settings.Providers {
		resolved := settings.ResolveKey(name)
		if !manageSecretUsable(resolved) || len(resolved) < 4 {
			continue
		}
		message = strings.ReplaceAll(message, resolved, "***")
	}
	if resolved := settings.ResolveImageGenerationToken(); manageSecretUsable(resolved) && len(resolved) >= 4 {
		message = strings.ReplaceAll(message, resolved, "***")
	}
	for _, market := range settings.SkillHub.Markets {
		if !manageSecretUsable(market.APIToken) || len(market.APIToken) < 4 {
			continue
		}
		message = strings.ReplaceAll(message, market.APIToken, "***")
	}
	return message
}

// manageRawGlobalSettings reads the current global settings.json as raw
// top-level keys so nested patches preserve sibling and unknown fields
// exactly. The write itself always stays in config.SaveGlobalSettingsPatch.
func manageRawGlobalSettings() (map[string]json.RawMessage, error) {
	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(config.GlobalSettingsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return raw, nil
		}
		return nil, fmt.Errorf("read global settings: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return raw, nil
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse global settings: %w", err)
	}
	return raw, nil
}

// manageMergeRawObject merges one top-level object key of the raw settings
// map in place, keeping every sibling field byte-identical.
func manageMergeRawObject(raw map[string]json.RawMessage, key string, mutate func(map[string]json.RawMessage) error) error {
	object := map[string]json.RawMessage{}
	if existing, ok := raw[key]; ok && len(bytes.TrimSpace(existing)) > 0 && !bytes.Equal(bytes.TrimSpace(existing), []byte("null")) {
		if err := json.Unmarshal(existing, &object); err != nil {
			return fmt.Errorf("parse settings key %s: %w", key, err)
		}
	}
	if err := mutate(object); err != nil {
		return err
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("encode settings key %s: %w", key, err)
	}
	raw[key] = encoded
	return nil
}

// manageDecodeOptionalString decodes a whitelist field that may be absent.
func manageDecodeOptionalString(raw json.RawMessage) (value string, present bool, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", false, nil
	}
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", true, fmt.Errorf("value must be a string")
	}
	return value, true, nil
}

// manageDecodeOptionalBool decodes a whitelist boolean field that may be absent.
func manageDecodeOptionalBool(raw json.RawMessage) (value bool, present bool, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false, false, nil
	}
	if !bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false")) {
		return false, true, fmt.Errorf("value must be a boolean")
	}
	value = bytes.Equal(trimmed, []byte("true"))
	return value, true, nil
}

// manageDecodeWhitelist decodes a params object and rejects every key outside
// the whitelist with a structured error carrying the stable machine code.
func manageDecodeWhitelist(params json.RawMessage, allowed map[string]bool, code string) (map[string]json.RawMessage, *mcp.RPCError) {
	fields := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(params)) > 0 {
		if err := json.Unmarshal(params, &fields); err != nil {
			return nil, acpStructuredRPCError(-32602, "invalid_params", "params must be a JSON object", nil)
		}
	}
	rejected := make([]string, 0)
	for field := range fields {
		if !allowed[field] {
			rejected = append(rejected, field)
		}
	}
	if len(rejected) > 0 {
		sort.Strings(rejected)
		allowedList := make([]string, 0, len(allowed))
		for field := range allowed {
			allowedList = append(allowedList, field)
		}
		sort.Strings(allowedList)
		return nil, acpStructuredRPCError(-32602, code,
			fmt.Sprintf("field %q is not allowed", rejected[0]),
			map[string]any{"field": rejected[0], "rejected": rejected, "allowed": allowedList})
	}
	return fields, nil
}

// --- §6.1 settings ------------------------------------------------------------

// manageSettingsPatchFields is the strict whitelist of settings/patch. Every
// entry maps onto the existing internal/config schema; fields owned by other
// configuration surfaces (serve.json features such as memoryEnabled) are
// deliberately rejected with settings_field_not_allowed.
var manageSettingsPatchFields = map[string]bool{
	"defaultProvider":  true,
	"defaultModel":     true,
	"defaultMode":      true,
	"thinkingLevel":    true,
	"providerKey":      true,
	"providerBaseUrl":  true,
	"sandboxEnabled":   true,
	"webSearchEnabled": true,
}

// manageAllowedModes and manageAllowedThinkingLevels mirror the mode and
// thinking vocabulary this ACP process already exposes through
// session/set_mode and the provider thinking levels.
var manageAllowedModes = map[string]bool{
	agentruntime.ModeAgent: true,
	agentruntime.ModePlan:  true,
	agentruntime.ModeYolo:  true,
	agentruntime.ModeOS:    true,
}

var manageAllowedThinkingLevels = map[string]bool{
	string(provider.ThinkingOff):     true,
	string(provider.ThinkingMinimal): true,
	string(provider.ThinkingLow):     true,
	string(provider.ThinkingMedium):  true,
	string(provider.ThinkingHigh):    true,
	string(provider.ThinkingXHigh):   true,
	string(provider.ThinkingMax):     true,
}

type manageProviderView struct {
	Name             string  `json:"name"`
	MaskedKey        *string `json:"maskedKey"`
	BaseURL          string  `json:"baseUrl,omitempty"`
	ModelCount       int     `json:"modelCount"`
	APIKeyConfigured bool    `json:"apiKeyConfigured"`
	IsDefault        bool    `json:"isDefault,omitempty"`
}

// manageProviderConfigView is the editable, non-secret part of one provider
// configuration. It deliberately exposes the effective config because that is
// the catalog the Runtime, TUI, channels, and WebUI all resolve. API keys are
// projected only as a mask; callers must submit a new key explicitly when they
// want to change one.
//
// GlobalOverride says this provider has an entry in global settings.json. For
// a preset provider, deleting that entry resets the override to its built-in
// defaults. For a user-defined provider it removes the provider altogether.
type manageProviderConfigView struct {
	ID               string                `json:"id"`
	Provider         config.ProviderConfig `json:"provider"`
	MaskedKey        *string               `json:"maskedKey"`
	APIKeyConfigured bool                  `json:"apiKeyConfigured"`
	IsDefault        bool                  `json:"isDefault,omitempty"`
	GlobalOverride   bool                  `json:"globalOverride,omitempty"`
}

// manageProviderConfigs projects the factory-effective config of every
// provider without ever serializing a credential. The raw global key map is
// used only to mark whether a reset/delete operation is meaningful.
func manageProviderConfigs(settings *config.Settings) ([]manageProviderConfigView, error) {
	raw, err := manageRawGlobalSettings()
	if err != nil {
		return nil, err
	}
	globalProviders := map[string]json.RawMessage{}
	if data, ok := raw["providers"]; ok && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &globalProviders); err != nil {
			return nil, fmt.Errorf("parse settings providers: %w", err)
		}
	}
	ids := make([]string, 0, len(settings.Providers))
	for id := range settings.Providers {
		ids = append(ids, id)
	}
	providerfactory.SortProviderIDs(ids)
	views := make([]manageProviderConfigView, 0, len(ids))
	for _, id := range ids {
		resolved := config.ResolveProviderConfig(id, settings)
		if resolved == nil {
			continue
		}
		// ResolveProviderConfig returns a fresh merge, but copy the value before
		// sanitizing so future changes cannot accidentally alter the settings
		// object used by the provider factory.
		projected := *resolved
		projected.APIKey = ""
		// Headers and response metadata can contain credentials too. They are
		// intentionally outside this Desktop-facing editor until they receive a
		// dedicated redacted management contract; omitting them also keeps an
		// ordinary provider/model edit from round-tripping secret header values.
		projected.Headers = nil
		projected.Responses = config.ResponsesConfig{}
		masked := manageMaskedKey(settings, id)
		views = append(views, manageProviderConfigView{
			ID:               id,
			Provider:         projected,
			MaskedKey:        masked,
			APIKeyConfigured: masked != nil,
			IsDefault:        id == settings.DefaultProvider,
			GlobalOverride:   globalProviders[id] != nil,
		})
	}
	return views, nil
}

// manageProviderViews projects the configured provider list with masked keys.
// The provider set and model counts come from the same factory resolution the
// model catalog uses, so the management view never re-parses raw settings.
func manageProviderViews(settings *config.Settings) []manageProviderView {
	ids := make([]string, 0, len(settings.Providers))
	for id := range settings.Providers {
		ids = append(ids, id)
	}
	providerfactory.SortProviderIDs(ids)
	views := make([]manageProviderView, 0, len(ids))
	for _, id := range ids {
		baseURL := ""
		if pc := config.ResolveProviderConfig(id, settings); pc != nil {
			baseURL = pc.BaseURL
		}
		masked := manageMaskedKey(settings, id)
		views = append(views, manageProviderView{
			Name:             id,
			MaskedKey:        masked,
			BaseURL:          baseURL,
			ModelCount:       len(providerfactory.ResolvedModels(settings, id)),
			APIKeyConfigured: masked != nil,
			IsDefault:        id == settings.DefaultProvider,
		})
	}
	return views
}

// manageSettingsView assembles the settings view model shared by
// settings/get and settings/patch responses. Keys appear masked only.
func (s *server) manageSettingsView() (map[string]any, error) {
	settings, err := s.manageSettings()
	if err != nil {
		return nil, err
	}
	defaultMode := settings.DefaultMode
	if strings.TrimSpace(defaultMode) == "" {
		// Product default mode rule: empty resolves to yolo.
		defaultMode = agentruntime.ModeYolo
	}
	disabled := settings.SkillsDisabled()
	if disabled == nil {
		disabled = []string{}
	}
	return map[string]any{
		"defaultProvider":  settings.DefaultProvider,
		"defaultModel":     settings.DefaultModel,
		"defaultMode":      defaultMode,
		"thinkingLevel":    settings.DefaultThinkingLevel,
		"providers":        manageProviderViews(settings),
		"sandboxEnabled":   settings.Sandbox.Enabled,
		"sandboxLevel":     settings.Sandbox.Level,
		"webSearchEnabled": settings.IsWebSearchEnabled(),
		"skillsDisabled":   disabled,
		"memoryEnabled":    manageMemoryEnabled(),
	}, nil
}

func (s *server) handleManageSettingsGet(req rpcRequest) {
	view, err := s.manageSettingsView()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, view, nil)
}

// handleManageSettingsPatch applies a strict-whitelist patch to the global
// settings file through config.SaveGlobalSettingsPatch. Nested objects
// (providers, sandbox, webSearch) are merged at the raw JSON level so sibling
// and unknown fields survive byte-identical. The response is the fresh
// settings view model with masked keys.
func (s *server) handleManageSettingsPatch(req rpcRequest) {
	var envelope struct {
		Patch map[string]json.RawMessage `json:"patch"`
	}
	if err := json.Unmarshal(req.Params, &envelope); err != nil || len(envelope.Patch) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"patch object with at least one allowed field is required", nil))
		return
	}
	settings, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	fields := make([]string, 0, len(envelope.Patch))
	for field := range envelope.Patch {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	allowedList := make([]string, 0, len(manageSettingsPatchFields))
	for field := range manageSettingsPatchFields {
		allowedList = append(allowedList, field)
	}
	sort.Strings(allowedList)
	for _, field := range fields {
		if !manageSettingsPatchFields[field] {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "settings_field_not_allowed",
				fmt.Sprintf("settings field %q is not writable through mothx/manage/settings/patch", field),
				map[string]any{"field": field, "allowed": allowedList}))
			return
		}
	}

	raw, err := manageRawGlobalSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	updates := map[string]any{}
	providersTouched := false
	invalid := func(field, message string) {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "settings_field_invalid",
			fmt.Sprintf("field %s: %s", field, message), map[string]any{"field": field}))
	}
	for _, field := range fields {
		value := envelope.Patch[field]
		switch field {
		case "defaultProvider":
			text, _, decodeErr := manageDecodeOptionalString(value)
			if decodeErr != nil || strings.TrimSpace(text) == "" {
				invalid(field, "a non-empty provider name is required")
				return
			}
			text = strings.TrimSpace(text)
			if settings.GetProviderConfig(text) == nil && config.DefaultProviderConfig(text) == nil {
				invalid(field, fmt.Sprintf("unknown provider %q", text))
				return
			}
			updates["defaultProvider"] = text
		case "defaultModel":
			text, _, decodeErr := manageDecodeOptionalString(value)
			if decodeErr != nil || strings.TrimSpace(text) == "" {
				invalid(field, "a non-empty model id is required")
				return
			}
			updates["defaultModel"] = strings.TrimSpace(text)
		case "defaultMode":
			text, _, decodeErr := manageDecodeOptionalString(value)
			if decodeErr != nil || !manageAllowedModes[strings.TrimSpace(text)] {
				invalid(field, "mode must be one of agent, plan, yolo, os")
				return
			}
			updates["defaultMode"] = strings.TrimSpace(text)
		case "thinkingLevel":
			text, _, decodeErr := manageDecodeOptionalString(value)
			if decodeErr != nil || !manageAllowedThinkingLevels[strings.TrimSpace(text)] {
				invalid(field, "thinkingLevel must be one of off, minimal, low, medium, high, xhigh, max")
				return
			}
			updates["defaultThinkingLevel"] = strings.TrimSpace(text)
		case "providerKey", "providerBaseUrl":
			var entry struct {
				Name string  `json:"name"`
				Key  *string `json:"key,omitempty"`
				URL  *string `json:"url,omitempty"`
			}
			if err := json.Unmarshal(value, &entry); err != nil || strings.TrimSpace(entry.Name) == "" {
				invalid(field, "an object with a non-empty provider name is required")
				return
			}
			name := strings.TrimSpace(entry.Name)
			if settings.GetProviderConfig(name) == nil && config.DefaultProviderConfig(name) == nil {
				invalid(field, fmt.Sprintf("unknown provider %q", name))
				return
			}
			target := "apiKey"
			replacement := entry.Key
			if field == "providerBaseUrl" {
				target = "baseUrl"
				replacement = entry.URL
			}
			if replacement == nil {
				invalid(field, fmt.Sprintf("%s requires a %s value (use an empty string to clear)", field, target))
				return
			}
			if field == "providerBaseUrl" {
				*replacement = strings.TrimSpace(*replacement)
			}
			mergeErr := manageMergeRawObject(raw, "providers", func(providers map[string]json.RawMessage) error {
				entryRaw := map[string]json.RawMessage{}
				if existing, ok := providers[name]; ok && len(bytes.TrimSpace(existing)) > 0 {
					if err := json.Unmarshal(existing, &entryRaw); err != nil {
						return fmt.Errorf("parse provider %s: %w", name, err)
					}
				}
				encoded, err := json.Marshal(*replacement)
				if err != nil {
					return err
				}
				entryRaw[target] = encoded
				encodedEntry, err := json.Marshal(entryRaw)
				if err != nil {
					return err
				}
				providers[name] = encodedEntry
				return nil
			})
			if mergeErr != nil {
				s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", mergeErr.Error(), nil))
				return
			}
			providersTouched = true
		case "sandboxEnabled", "webSearchEnabled":
			enabled, _, decodeErr := manageDecodeOptionalBool(value)
			if decodeErr != nil {
				invalid(field, "a boolean value is required")
				return
			}
			key := "sandbox"
			if field == "webSearchEnabled" {
				key = "webSearch"
			}
			if err := manageMergeRawObject(raw, key, func(object map[string]json.RawMessage) error {
				encoded, err := json.Marshal(enabled)
				if err != nil {
					return err
				}
				object["enabled"] = encoded
				return nil
			}); err != nil {
				s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
				return
			}
			updates[key] = raw[key]
		}
	}
	if providersTouched {
		updates["providers"] = raw["providers"]
	}
	if err := config.SaveGlobalSettingsPatch(updates); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_save_failed",
			manageRedactSecrets(err.Error(), settings), nil))
		return
	}
	view, err := s.manageSettingsView()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, view, nil)
}

// --- §6.1 providers -----------------------------------------------------------

// handleManageProvidersList projects the factory-resolvable provider list
// plus the shared model catalog. Both come from providerfactory.ResolvedModels
// and SortProviderIDs — the same single source the serve model catalog and the
// TUI pickers use — so no second catalog logic exists here.
func (s *server) handleManageProvidersList(req rpcRequest) {
	settings, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	catalog, err := s.manageProvidersCatalog(settings)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, catalog, nil)
}

// manageProviderSaveRequest keeps credentials separate from the serializable
// ProviderConfig. The provider object is therefore safe to echo in a response
// and an omitted apiKey means "leave the current secret source untouched".
type manageProviderSaveRequest struct {
	ID         string          `json:"id"`
	PreviousID string          `json:"previousId,omitempty"`
	Provider   json.RawMessage `json:"provider"`
	APIKey     *string         `json:"apiKey,omitempty"`
}

var manageProviderWritableFields = map[string]bool{
	"vendor":              true,
	"baseUrl":             true,
	"httpProxy":           true,
	"forceHTTP11":         true,
	"headers":             true,
	"api":                 true,
	"thinkingFormat":      true,
	"cacheControl":        true,
	"maxImagesPerRequest": true,
	"responses":           true,
	"models":              true,
}

// manageDecodeProviderDraft validates the schema supplied by a management
// client. It uses config.ProviderConfig for the canonical settings shape while
// preserving unknown fields already present in settings.json on save.
func manageDecodeProviderDraft(raw json.RawMessage) (config.ProviderConfig, map[string]json.RawMessage, *mcp.RPCError) {
	fields, rpcErr := manageDecodeWhitelist(raw, manageProviderWritableFields, "provider_field_not_allowed")
	if rpcErr != nil {
		return config.ProviderConfig{}, nil, rpcErr
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return config.ProviderConfig{}, nil, acpStructuredRPCError(-32602, "invalid_params", "invalid provider object", nil)
	}
	var draft config.ProviderConfig
	if err := json.Unmarshal(encoded, &draft); err != nil {
		return config.ProviderConfig{}, nil, acpStructuredRPCError(-32602, "provider_field_invalid", err.Error(), nil)
	}
	seenModels := make(map[string]struct{}, len(draft.Models))
	for _, model := range draft.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			return config.ProviderConfig{}, nil, acpStructuredRPCError(-32602, "provider_field_invalid", "model id is required", map[string]any{"field": "models"})
		}
		if _, ok := seenModels[id]; ok {
			return config.ProviderConfig{}, nil, acpStructuredRPCError(-32602, "provider_field_invalid", fmt.Sprintf("duplicate model id %q", id), map[string]any{"field": "models"})
		}
		seenModels[id] = struct{}{}
	}
	return draft, fields, nil
}

func (s *server) manageProvidersCatalog(settings *config.Settings) (map[string]any, error) {
	views := manageProviderViews(settings)
	configs, err := manageProviderConfigs(settings)
	if err != nil {
		return nil, err
	}
	models := make([]map[string]any, 0)
	for _, view := range views {
		for _, model := range providerfactory.ResolvedModels(settings, view.Name) {
			if model == nil {
				continue
			}
			models = append(models, map[string]any{
				"id": model.ID, "name": model.Name, "provider": view.Name,
				"input": append([]string(nil), model.Input...), "reasoning": model.Reasoning,
			})
		}
	}
	return map[string]any{
		"providers": views, "providerConfigs": configs, "models": models,
		"defaultProvider": settings.DefaultProvider, "defaultModel": settings.DefaultModel,
	}, nil
}

// handleManageProvidersSave creates or updates one global provider overlay.
// It intentionally writes through SaveGlobalSettingsPatch so unrelated global
// settings remain sparse and unknown provider fields survive an edit.
func (s *server) handleManageProvidersSave(req rpcRequest) {
	var in manageProviderSaveRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.ID) == "" || len(bytes.TrimSpace(in.Provider)) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "id and provider are required", nil))
		return
	}
	in.ID = strings.TrimSpace(in.ID)
	in.PreviousID = strings.TrimSpace(in.PreviousID)
	if strings.Contains(in.ID, "/") {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "provider_field_invalid", "provider id must not contain '/'", map[string]any{"field": "id"}))
		return
	}
	_, fields, rpcErr := manageDecodeProviderDraft(in.Provider)
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	settings, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	raw, err := manageRawGlobalSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	sourceID := in.PreviousID
	if sourceID == "" {
		sourceID = in.ID
	}
	if sourceID != in.ID && settings.GetProviderConfig(sourceID) == nil && config.DefaultProviderConfig(sourceID) == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "provider_not_found", fmt.Sprintf("provider %q is not configured", sourceID), nil))
		return
	}
	if err := manageMergeRawObject(raw, "providers", func(providers map[string]json.RawMessage) error {
		if sourceID != in.ID {
			if _, ok := providers[sourceID]; !ok {
				return fmt.Errorf("only a global provider override can be renamed")
			}
			if _, exists := providers[in.ID]; exists {
				return fmt.Errorf("provider %q already exists in global settings", in.ID)
			}
		}
		entry := map[string]json.RawMessage{}
		if existing, ok := providers[sourceID]; ok && len(bytes.TrimSpace(existing)) > 0 {
			if err := json.Unmarshal(existing, &entry); err != nil {
				return fmt.Errorf("parse provider %s: %w", sourceID, err)
			}
		}
		// Replace only explicitly supplied fields. This both preserves unknown
		// settings and avoids erasing intentionally non-projected secret header
		// values when Desktop changes a model or endpoint.
		for key := range fields {
			delete(entry, key)
		}
		for key, value := range fields {
			entry[key] = value
		}
		if in.APIKey != nil {
			encoded, err := json.Marshal(*in.APIKey)
			if err != nil {
				return err
			}
			entry["apiKey"] = encoded
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		providers[in.ID] = encoded
		if sourceID != in.ID {
			delete(providers, sourceID)
		}
		return nil
	}); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "provider_save_failed", err.Error(), nil))
		return
	}
	updates := map[string]any{"providers": raw["providers"]}
	if sourceID != in.ID && settings.DefaultProvider == sourceID {
		updates["defaultProvider"] = in.ID
	}
	if err := config.SaveGlobalSettingsPatch(updates); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_save_failed", manageRedactSecrets(err.Error(), settings), nil))
		return
	}
	refreshed, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	catalog, err := s.manageProvidersCatalog(refreshed)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, catalog, nil)
}

type manageProviderDeleteRequest struct {
	ID string `json:"id"`
}

// handleManageProvidersDelete removes a global provider overlay. Preset
// providers consequently reset to their built-in config; custom providers
// disappear. The active default must be changed first to avoid a dangling
// default that would break every Runtime adapter equally.
func (s *server) handleManageProvidersDelete(req rpcRequest) {
	var in manageProviderDeleteRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "provider id is required", nil))
		return
	}
	in.ID = strings.TrimSpace(in.ID)
	settings, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	if settings.DefaultProvider == in.ID {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "provider_default_in_use", "choose another default provider before deleting this one", nil))
		return
	}
	raw, err := manageRawGlobalSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	found := false
	if err := manageMergeRawObject(raw, "providers", func(providers map[string]json.RawMessage) error {
		if _, ok := providers[in.ID]; !ok {
			return nil
		}
		delete(providers, in.ID)
		found = true
		return nil
	}); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	if !found {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "provider_not_custom", fmt.Sprintf("provider %q has no global override to delete", in.ID), nil))
		return
	}
	if err := config.SaveGlobalSettingsPatch(map[string]any{"providers": raw["providers"]}); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_save_failed", manageRedactSecrets(err.Error(), settings), nil))
		return
	}
	refreshed, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	catalog, err := s.manageProvidersCatalog(refreshed)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, catalog, nil)
}

type manageProviderDiscoverRequest struct {
	API         string            `json:"api"`
	BaseURL     string            `json:"baseUrl"`
	APIKey      string            `json:"apiKey"`
	HTTPProxy   string            `json:"httpProxy"`
	ForceHTTP11 bool              `json:"forceHTTP11"`
	Headers     map[string]string `json:"headers"`
}

// handleManageProvidersDiscover is the same provider-owned discovery path as
// the WebUI draft editor. Results are drafts only: the user must still save
// them through providers/save before they enter the shared model catalog.
func (s *server) handleManageProvidersDiscover(req rpcRequest) {
	var in manageProviderDiscoverRequest
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "invalid provider discovery params", nil))
		return
	}
	in.API = strings.TrimSpace(in.API)
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	if in.API == "" || in.BaseURL == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "api and baseUrl are required", nil))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, err := provider.DiscoverModels(ctx, provider.DiscoverModelsOptions{
		API: in.API, BaseURL: in.BaseURL, APIKey: in.APIKey, HTTPProxy: in.HTTPProxy,
		ForceHTTP11: in.ForceHTTP11, Headers: in.Headers,
	})
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "provider_discovery_failed", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, map[string]any{"models": models}, nil)
}

type manageProviderTestRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
}

// handleManageProvidersTest performs one minimal call (1-token ping, 5s
// timeout) against a stored provider through the shared provider factory —
// the same probe semantics as the serve provider test endpoint. Responses and
// errors never contain key material.
func (s *server) handleManageProvidersTest(req rpcRequest) {
	var in manageProviderTestRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.Provider) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "provider is required", nil))
		return
	}
	name := strings.TrimSpace(in.Provider)
	settings, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	if settings.GetProviderConfig(name) == nil && config.DefaultProviderConfig(name) == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "provider_not_found",
			fmt.Sprintf("provider %q is not configured", name), nil))
		return
	}
	modelID := strings.TrimSpace(in.Model)
	if modelID == "" && name == settings.DefaultProvider {
		modelID = settings.DefaultModel
	}
	p, model, err := providerfactory.Create(settings, name, modelID)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "provider_test_failed",
			manageRedactSecrets(fmt.Sprintf("create provider: %v", err), settings), nil))
		return
	}
	targetModel := modelID
	if model != nil {
		targetModel = model.ID
	}
	ctx, cancel := context.WithTimeout(context.Background(), manageProviderTestTimeout)
	defer cancel()
	started := time.Now()
	events := p.Chat(ctx, provider.ChatParams{
		ModelID: targetModel, ThinkingLevel: provider.ThinkingOff, MaxTokens: 1,
		Messages: []provider.Message{{Role: "user", Content: "ping"}},
	})
	for event := range events {
		if event.Type == provider.StreamError {
			message := "model request failed"
			if event.Error != nil {
				message = event.Error.Error()
			}
			s.writeResponse(req.ID, map[string]any{
				"ok":       false,
				"provider": name,
				"model":    targetModel,
				"error":    manageRedactSecrets(message, settings),
			}, nil)
			return
		}
		if event.Type == provider.StreamDone {
			s.writeResponse(req.ID, map[string]any{
				"ok":        true,
				"provider":  name,
				"model":     targetModel,
				"latencyMs": time.Since(started).Milliseconds(),
			}, nil)
			return
		}
	}
	s.writeResponse(req.ID, map[string]any{
		"ok":       false,
		"provider": name,
		"model":    targetModel,
		"error":    "model request ended without a completion",
	}, nil)
}

// --- §6.1 skills ----------------------------------------------------------------

type manageSkillsRequest struct {
	Cwd     string `json:"cwd,omitempty"`
	Name    string `json:"name,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}

// manageSkillsManager builds the skills discovery manager for one cwd through
// the same constructor and directory precedence the agent runtime uses.
func (s *server) manageSkillsManager(cwd string) (*skills.Manager, string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = s.manageWorkDir()
	}
	if !filepath.IsAbs(cwd) {
		return nil, "", fmt.Errorf("cwd must be an absolute path")
	}
	settings, err := s.manageSettings()
	if err != nil {
		return nil, "", err
	}
	manager := skills.NewManagerWithProjectDirs(settings.GetGlobalSkillsDir(), skills.ProjectSkillDirs(cwd))
	_ = manager.Load()
	return manager, cwd, nil
}

func (s *server) handleManageSkillsList(req rpcRequest) {
	var in manageSkillsRequest
	if len(bytes.TrimSpace(req.Params)) > 0 {
		if err := json.Unmarshal(req.Params, &in); err != nil {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "invalid params", nil))
			return
		}
	}
	manager, cwd, err := s.manageSkillsManager(in.Cwd)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "skills_unavailable", err.Error(), nil))
		return
	}
	items := make([]map[string]any, 0)
	for _, skill := range manager.ListAll() {
		items = append(items, map[string]any{
			"name":        skill.Name,
			"description": skill.Description,
			"source":      skill.Source,
			"enabled":     !manager.IsSkillDisabled(skill.Name),
		})
	}
	s.writeResponse(req.ID, map[string]any{"cwd": cwd, "skills": items}, nil)
}

// handleManageSkillsSet toggles one skill through the global
// settings.skills.disabled list — the single enable/disable source every
// skills.Manager load consults — and live-applies the new list to this
// process's runtime skills manager.
func (s *server) handleManageSkillsSet(req rpcRequest) {
	var in manageSkillsRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.Name) == "" || in.Enabled == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "name and enabled are required", nil))
		return
	}
	name := strings.TrimSpace(in.Name)
	manager, _, err := s.manageSkillsManager(in.Cwd)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "skills_unavailable", err.Error(), nil))
		return
	}
	found := false
	for _, skill := range manager.ListAll() {
		if skill.Name == name {
			found = true
			break
		}
	}
	if !found {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "skill_not_found",
			fmt.Sprintf("skill %q is not available", name), nil))
		return
	}
	raw, err := manageRawGlobalSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	current := config.SkillsSettings{}
	if existing, ok := raw["skills"]; ok && len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &current); err != nil {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable",
				fmt.Sprintf("parse skills settings: %v", err), nil))
			return
		}
	}
	next := make([]string, 0, len(current.Disabled)+1)
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		next = append(next, value)
	}
	for _, value := range current.Disabled {
		if *in.Enabled && value == name {
			continue
		}
		add(value)
	}
	if !*in.Enabled {
		add(name)
	}
	sort.Strings(next)
	updates := map[string]any{}
	if len(next) == 0 {
		// Keep sparse settings sparse: drop the section entirely.
		updates["skills"] = nil
	} else {
		updates["skills"] = map[string]any{"disabled": next}
	}
	if err := config.SaveGlobalSettingsPatch(updates); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_save_failed", err.Error(), nil))
		return
	}
	// Live-apply to this process's runtime manager so the toggle takes effect
	// without a restart; other processes pick it up on their next skills load.
	if s.skillsMgr != nil {
		s.skillsMgr.SetDisabledSkills(next)
	}
	s.writeResponse(req.ID, map[string]any{
		"name":           name,
		"enabled":        *in.Enabled,
		"skillsDisabled": next,
	}, nil)
}

// --- §6.1 mcp -------------------------------------------------------------------

// manageMCPViews projects the complete local mcp.json schema. Desktop talks
// only to its locally spawned ACP child over stdio, so MCP environment and
// header values must remain editable just as they are in the local Web UI.
func manageMCPViews(cfg *config.MCPConfig) []map[string]any {
	views := make([]map[string]any, 0, len(cfg.MCPServers))
	for _, srv := range cfg.MCPServers {
		view := map[string]any{
			"name":    srv.Name,
			"type":    srv.Type,
			"enabled": config.MCPServerEnabled(srv),
		}
		if srv.Command != "" {
			view["command"] = srv.Command
		}
		if len(srv.Args) > 0 {
			view["args"] = append([]string(nil), srv.Args...)
		}
		if srv.URL != "" {
			view["url"] = srv.URL
		}
		if srv.MessageURL != "" {
			view["messageUrl"] = srv.MessageURL
		}
		if len(srv.Env) > 0 {
			view["env"] = append([]struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}(nil), srv.Env...)
		}
		if len(srv.Headers) > 0 {
			view["headers"] = append([]struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}(nil), srv.Headers...)
		}
		views = append(views, view)
	}
	return views
}

type manageMCPTarget struct {
	Scope     string
	SessionID string
	Path      string
}

func (s *server) resolveManageMCPTarget(scope, sessionID string) (manageMCPTarget, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "global"
	}
	switch scope {
	case "global":
		return manageMCPTarget{Scope: scope, Path: config.GlobalMCPPath()}, nil
	case "project":
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			return manageMCPTarget{}, fmt.Errorf("project MCP management requires sessionId")
		}
		rt := s.sessionRuntime(sessionID)
		if rt == nil || rt.runtime == nil || strings.TrimSpace(rt.runtime.WorkDir) == "" {
			return manageMCPTarget{}, fmt.Errorf("session %q is not active", sessionID)
		}
		return manageMCPTarget{
			Scope:     scope,
			SessionID: sessionID,
			Path:      filepath.Join(rt.runtime.WorkDir, config.ProjectMCPPath()),
		}, nil
	default:
		return manageMCPTarget{}, fmt.Errorf("MCP scope must be global or project")
	}
}

func (s *server) manageMCPConfigAtPath(path string) (*config.MCPConfig, error) {
	cfg, err := config.LoadMCPConfig(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &config.MCPConfig{}, nil
		}
		return nil, err
	}
	if cfg == nil {
		cfg = &config.MCPConfig{}
	}
	config.NormalizeMCPConfig(cfg)
	return cfg, nil
}

func (s *server) handleManageMCPList(req rpcRequest) {
	var in struct {
		Scope     string `json:"scope,omitempty"`
		SessionID string `json:"sessionId,omitempty"`
	}
	if len(bytes.TrimSpace(req.Params)) > 0 {
		if err := json.Unmarshal(req.Params, &in); err != nil {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "invalid MCP scope parameters", nil))
			return
		}
	}
	target, err := s.resolveManageMCPTarget(in.Scope, in.SessionID)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "mcp_scope_invalid", err.Error(), nil))
		return
	}
	cfg, err := s.manageMCPConfigAtPath(target.Path)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "mcp_unavailable",
			fmt.Sprintf("load MCP config: %v", err), nil))
		return
	}
	s.writeResponse(req.ID, map[string]any{
		"scope":     target.Scope,
		"sessionId": target.SessionID,
		"path":      target.Path,
		"servers":   manageMCPViews(cfg),
	}, nil)
}

// manageMCPServerFields is the mcp/set whitelist. MCP configuration is local
// Desktop-to-ACP IPC, so headers and environment variables are first-class,
// editable fields rather than a lossy name-only projection.
var manageMCPServerFields = map[string]bool{
	"name": true, "type": true, "command": true, "args": true,
	"url": true, "messageUrl": true, "enabled": true, "headers": true, "env": true,
}

var manageMCPServerTypes = map[string]bool{"stdio": true, "http": true, "sse": true}

// handleManageMCPSet fully replaces the global mcp.json server list from the
// complete whitelisted input. Servers absent from the input are removed.
func (s *server) handleManageMCPSet(req rpcRequest) {
	var envelope struct {
		Servers   []json.RawMessage `json:"servers"`
		Scope     string            `json:"scope,omitempty"`
		SessionID string            `json:"sessionId,omitempty"`
	}
	if len(bytes.TrimSpace(req.Params)) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "servers is required", nil))
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(req.Params))
	if err := decoder.Decode(&envelope); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "servers array is required", nil))
		return
	}
	if envelope.Servers == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "servers array is required", nil))
		return
	}
	target, err := s.resolveManageMCPTarget(envelope.Scope, envelope.SessionID)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "mcp_scope_invalid", err.Error(), nil))
		return
	}
	existing, err := s.manageMCPConfigAtPath(target.Path)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "mcp_unavailable",
			fmt.Sprintf("load MCP config: %v", err), nil))
		return
	}
	byName := make(map[string]config.MCPServer, len(existing.MCPServers))
	for _, srv := range existing.MCPServers {
		byName[srv.Name] = srv
	}
	invalidServer := func(message string) {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "mcp_server_invalid", message, nil))
	}
	next := make([]config.MCPServer, 0, len(envelope.Servers))
	seen := map[string]bool{}
	for _, rawEntry := range envelope.Servers {
		fields := map[string]json.RawMessage{}
		if err := json.Unmarshal(rawEntry, &fields); err != nil {
			invalidServer("each server entry must be a JSON object")
			return
		}
		rejected := ""
		for field := range fields {
			if !manageMCPServerFields[field] {
				if rejected == "" || field < rejected {
					rejected = field
				}
			}
		}
		if rejected != "" {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "mcp_field_not_allowed",
				fmt.Sprintf("MCP server field %q is not writable through mothx/manage/mcp/set", rejected),
				map[string]any{"field": rejected}))
			return
		}
		name, _, nameErr := manageDecodeOptionalString(fields["name"])
		if nameErr != nil || strings.TrimSpace(name) == "" {
			invalidServer("each server entry requires a non-empty name")
			return
		}
		name = strings.TrimSpace(name)
		if seen[name] {
			invalidServer(fmt.Sprintf("duplicate MCP server name %q", name))
			return
		}
		seen[name] = true
		entry := byName[name]
		entry.Name = name
		if value, present, fieldErr := manageDecodeOptionalString(fields["type"]); fieldErr != nil {
			invalidServer(fmt.Sprintf("server %s: type must be a string", name))
			return
		} else if present {
			trimmed := strings.TrimSpace(value)
			if trimmed != "" && !manageMCPServerTypes[trimmed] {
				invalidServer(fmt.Sprintf("server %s: type must be one of stdio, http, sse", name))
				return
			}
			entry.Type = trimmed
		}
		for _, field := range []struct {
			key    string
			target *string
		}{
			{"command", &entry.Command},
			{"url", &entry.URL},
			{"messageUrl", &entry.MessageURL},
		} {
			value, present, fieldErr := manageDecodeOptionalString(fields[field.key])
			if fieldErr != nil {
				invalidServer(fmt.Sprintf("server %s: %s must be a string", name, field.key))
				return
			}
			if present {
				*field.target = strings.TrimSpace(value)
			}
		}
		if rawArgs, present := fields["args"]; present {
			var args []string
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				invalidServer(fmt.Sprintf("server %s: args must be an array of strings", name))
				return
			}
			entry.Args = args
		}
		for _, field := range []struct {
			key    string
			target *[]struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}
		}{
			{"headers", &entry.Headers},
			{"env", &entry.Env},
		} {
			rawPairs, present := fields[field.key]
			if !present {
				continue
			}
			var pairs []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}
			if err := json.Unmarshal(rawPairs, &pairs); err != nil {
				invalidServer(fmt.Sprintf("server %s: %s must be an array of name/value objects", name, field.key))
				return
			}
			for index := range pairs {
				pairs[index].Name = strings.TrimSpace(pairs[index].Name)
				if pairs[index].Name == "" {
					invalidServer(fmt.Sprintf("server %s: %s entries require a non-empty name", name, field.key))
					return
				}
			}
			*field.target = pairs
		}
		if rawEnabled, present := fields["enabled"]; present {
			enabled, _, fieldErr := manageDecodeOptionalBool(rawEnabled)
			if fieldErr != nil {
				invalidServer(fmt.Sprintf("server %s: enabled must be a boolean", name))
				return
			}
			entry.Enabled = &enabled
		}
		next = append(next, entry)
	}
	// Transport sanity: normalized stdio entries need a command; remote
	// entries need a URL. Validation happens on the normalized copy.
	cfg := &config.MCPConfig{MCPServers: next}
	config.NormalizeMCPConfig(cfg)
	for _, srv := range cfg.MCPServers {
		switch srv.Type {
		case "stdio":
			if strings.TrimSpace(srv.Command) == "" {
				invalidServer(fmt.Sprintf("server %s: stdio transport requires a command", srv.Name))
				return
			}
		case "http", "sse":
			if strings.TrimSpace(srv.URL) == "" {
				invalidServer(fmt.Sprintf("server %s: %s transport requires a url", srv.Name, srv.Type))
				return
			}
		}
	}
	if err := config.SaveMCPConfig(target.Path, cfg); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "mcp_unavailable",
			fmt.Sprintf("save MCP config: %v", err), nil))
		return
	}
	saved, err := s.manageMCPConfigAtPath(target.Path)
	if err != nil {
		saved = cfg
	}
	s.writeResponse(req.ID, map[string]any{
		"scope":     target.Scope,
		"sessionId": target.SessionID,
		"path":      target.Path,
		"servers":   manageMCPViews(saved),
	}, nil)
}

// --- §6.1 cron ------------------------------------------------------------------

// manageCronInterval resolves the ACP in-process scheduler tick. The default
// matches serve/TUI (30s); tests may shorten it with MOTHX_ACP_CRON_INTERVAL
// (Go duration), mirroring the --question-timeout style env injection.
func manageCronInterval() time.Duration {
	if value := strings.TrimSpace(os.Getenv("MOTHX_ACP_CRON_INTERVAL")); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultManageCronInterval
}

// ensureManageCron idempotently starts the ACP in-process cron runtime: the
// shared SQLite store plus the existing cron.Scheduler driven by an
// AgentManager built through the canonical agentruntime construction path.
// Job execution reuses the scheduler's transient sub-agent runner, which
// resolves mode/source through agentruntime.ResolvePolicy with SourceCron
// independently of any desktop session's forced-yolo semantics.
func (s *server) ensureManageCron() (*cron.Scheduler, cron.CronStore, error) {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if s.cronScheduler != nil && s.cronStore != nil {
		return s.cronScheduler, s.cronStore, nil
	}
	if s.settings == nil || s.runtime == nil || s.p == nil || s.m == nil {
		return nil, nil, errors.New("ACP cron runtime prerequisites are unavailable")
	}
	sessionDir := s.settings.GetSessionDir()
	store := cron.NewSQLiteCronStore(sessionDir)
	manager := s.cronAgentMgr
	if manager == nil {
		built, err := agentruntime.NewAgentManager(agentruntime.AgentManagerOptions{
			Runtime: s.runtime, Provider: s.p, Model: s.m, Settings: s.settings,
			ProviderName: s.providerName, Allow: s.allow, MultiAgentEnabled: true,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("create cron agent manager: %w", err)
		}
		manager = built
		s.cronAgentMgr = built
	}
	scheduler := cron.NewSchedulerWithSessionDirAndHandler(store, manager, manageCronInterval(), sessionDir, s.runKnowledgeBaseCronJob)
	// Reconcile schedules persisted by older Desktop processes before the
	// first tick. This keeps the knowledge-base configuration authoritative and
	// removes any namespaced job whose base was deleted while ACP was offline.
	if err := s.syncAllKnowledgeBaseSchedulesWithStore(store); err != nil {
		return nil, nil, fmt.Errorf("sync knowledge base schedules: %w", err)
	}
	scheduler.SetJobCompletionObserver(s.observeManageCronJob)
	scheduler.Start()
	s.cronScheduler = scheduler
	s.cronStore = store
	return scheduler, store, nil
}

// stopManageCron stops the in-process scheduler at server shutdown. It is
// idempotent and safe when the scheduler was never started.
func (s *server) stopManageCron() {
	if s == nil {
		return
	}
	s.cronMu.Lock()
	scheduler := s.cronScheduler
	s.cronScheduler = nil
	s.cronMu.Unlock()
	if scheduler != nil {
		scheduler.Stop()
	}
}

// observeManageCronJob projects the additive cron_completed session event
// when one in-process cron run finishes. The event carries the job identity
// and terminal status only; the response text stays in the job's store row.
func (s *server) observeManageCronJob(job cron.CronJob, response string, runErr error) {
	status := "success"
	if runErr != nil {
		status = "failed"
	}
	params := map[string]any{
		"event":  "cron_completed",
		"jobId":  job.ID,
		"status": status,
	}
	if job.SessionID != "" {
		params["sessionId"] = job.SessionID
	}
	_ = s.notifyExtension("_mothx/session_event", params)
}

// manageCronJobView projects one stored job. The A2A token is a credential
// and is never projected, mirroring the serve cron API's public shape.
func manageCronJobView(job cron.CronJob) map[string]any {
	view := map[string]any{
		"id":         job.ID,
		"name":       job.Name,
		"prompt":     job.Prompt,
		"schedule":   job.Schedule,
		"oneshot":    job.OneShot,
		"mode":       job.Mode,
		"enabled":    job.Enabled,
		"runCount":   job.RunCount,
		"lastStatus": job.LastStatus,
	}
	if job.SessionID != "" {
		view["sessionId"] = job.SessionID
	}
	if job.WorkDir != "" {
		view["workDir"] = job.WorkDir
	}
	if job.A2ATarget != "" {
		view["a2aTarget"] = job.A2ATarget
	}
	if !job.CreatedAt.IsZero() {
		view["createdAt"] = job.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !job.LastRun.IsZero() {
		view["lastRun"] = job.LastRun.UTC().Format(time.RFC3339)
	}
	if !job.NextRun.IsZero() {
		view["nextRun"] = job.NextRun.UTC().Format(time.RFC3339)
	}
	if job.LastError != "" {
		view["lastError"] = job.LastError
	}
	return view
}

// manageCronJobFields is the create/update whitelist aligned with the serve
// /api/cron request shape minus the session/credential/workdir fields the
// management plane owns itself.
var manageCronJobFields = map[string]bool{
	"name": true, "schedule": true, "prompt": true, "mode": true, "enabled": true,
}

func (s *server) handleManageCronList(req rpcRequest) {
	scheduler, store, err := s.ensureManageCron()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "cron_unavailable", err.Error(), nil))
		return
	}
	jobs, err := store.List()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "cron_unavailable",
			fmt.Sprintf("list cron jobs: %v", err), nil))
		return
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	views := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		views = append(views, manageCronJobView(job))
	}
	s.writeResponse(req.ID, map[string]any{
		"enabled": true,
		"running": scheduler.IsRunning(),
		"jobs":    views,
	}, nil)
}

// manageCronJobFromFields assembles and normalizes a job from whitelisted
// patch fields. Mode and schedule validation mirror the shared cron
// normalization so every management surface accepts one vocabulary.
func (s *server) manageCronJobFromFields(base cron.CronJob, fields map[string]json.RawMessage) (cron.CronJob, *mcp.RPCError) {
	job := base
	if raw, ok := fields["name"]; ok {
		value, _, err := manageDecodeOptionalString(raw)
		if err != nil {
			return job, acpStructuredRPCError(-32602, "cron_field_invalid", "name must be a string", map[string]any{"field": "name"})
		}
		job.Name = strings.TrimSpace(value)
	}
	if raw, ok := fields["prompt"]; ok {
		value, _, err := manageDecodeOptionalString(raw)
		if err != nil {
			return job, acpStructuredRPCError(-32602, "cron_field_invalid", "prompt must be a string", map[string]any{"field": "prompt"})
		}
		job.Prompt = value
	}
	if raw, ok := fields["schedule"]; ok {
		value, _, err := manageDecodeOptionalString(raw)
		if err != nil {
			return job, acpStructuredRPCError(-32602, "cron_field_invalid", "schedule must be a string", map[string]any{"field": "schedule"})
		}
		job.Schedule = strings.TrimSpace(value)
	}
	if raw, ok := fields["mode"]; ok {
		value, _, err := manageDecodeOptionalString(raw)
		if err != nil {
			return job, acpStructuredRPCError(-32602, "cron_field_invalid", "mode must be a string", map[string]any{"field": "mode"})
		}
		job.Mode = strings.TrimSpace(value)
	}
	if raw, ok := fields["enabled"]; ok {
		value, _, err := manageDecodeOptionalBool(raw)
		if err != nil {
			return job, acpStructuredRPCError(-32602, "cron_field_invalid", "enabled must be a boolean", map[string]any{"field": "enabled"})
		}
		job.Enabled = value
	}
	if strings.TrimSpace(job.Name) == "" {
		return job, acpStructuredRPCError(-32602, "cron_field_invalid", "name is required", map[string]any{"field": "name"})
	}
	if strings.TrimSpace(job.Prompt) == "" {
		return job, acpStructuredRPCError(-32602, "cron_field_invalid", "prompt is required", map[string]any{"field": "prompt"})
	}
	if job.Mode != "" && job.Mode != "agent" && job.Mode != "yolo" {
		return job, acpStructuredRPCError(-32602, "cron_mode_invalid",
			fmt.Sprintf("mode %q must be agent or yolo", job.Mode), map[string]any{"field": "mode"})
	}
	if err := cron.NormalizeJobSchedule(&job); err != nil {
		return job, acpStructuredRPCError(-32602, "cron_schedule_invalid",
			fmt.Sprintf("schedule: %v", err), map[string]any{"field": "schedule"})
	}
	return job, nil
}

func (s *server) handleManageCronCreate(req rpcRequest) {
	fields, rpcErr := manageDecodeWhitelist(req.Params, manageCronJobFields, "cron_field_not_allowed")
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	_, store, err := s.ensureManageCron()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "cron_unavailable", err.Error(), nil))
		return
	}
	base := cron.CronJob{Enabled: true, WorkDir: s.manageWorkDir()}
	job, rpcErr := s.manageCronJobFromFields(base, fields)
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	created, err := store.Create(job)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "cron_unavailable",
			fmt.Sprintf("create cron job: %v", err), nil))
		return
	}
	s.writeResponse(req.ID, map[string]any{"job": manageCronJobView(*created)}, nil)
}

// manageCronIDFields is the whitelist of update/remove/run: the job identity
// plus the create/update field whitelist.
func manageCronIDFields() map[string]bool {
	allowed := map[string]bool{"id": true}
	for field := range manageCronJobFields {
		allowed[field] = true
	}
	return allowed
}

func (s *server) handleManageCronUpdate(req rpcRequest) {
	fields, rpcErr := manageDecodeWhitelist(req.Params, manageCronIDFields(), "cron_field_not_allowed")
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	id, _, err := manageDecodeOptionalString(fields["id"])
	if err != nil || strings.TrimSpace(id) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "id is required", nil))
		return
	}
	_, store, err := s.ensureManageCron()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "cron_unavailable", err.Error(), nil))
		return
	}
	existing, err := store.Get(strings.TrimSpace(id))
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "cron_job_not_found",
			fmt.Sprintf("cron job %q not found", strings.TrimSpace(id)), nil))
		return
	}
	job, rpcErr := s.manageCronJobFromFields(*existing, fields)
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	if err := store.Update(job); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "cron_unavailable",
			fmt.Sprintf("update cron job: %v", err), nil))
		return
	}
	s.writeResponse(req.ID, map[string]any{"job": manageCronJobView(job)}, nil)
}

func (s *server) handleManageCronRemove(req rpcRequest) {
	fields, rpcErr := manageDecodeWhitelist(req.Params, map[string]bool{"id": true}, "cron_field_not_allowed")
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	id, _, err := manageDecodeOptionalString(fields["id"])
	if err != nil || strings.TrimSpace(id) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "id is required", nil))
		return
	}
	_, store, err := s.ensureManageCron()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "cron_unavailable", err.Error(), nil))
		return
	}
	if err := store.Delete(strings.TrimSpace(id)); err != nil {
		code := "cron_unavailable"
		if strings.Contains(err.Error(), "not found") {
			code = "cron_job_not_found"
		}
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, code,
			fmt.Sprintf("delete cron job: %v", err), nil))
		return
	}
	s.writeResponse(req.ID, map[string]any{"id": strings.TrimSpace(id), "deleted": true}, nil)
}

// handleManageCronRun triggers one immediate execution through the shared
// scheduler claim path; completion is projected as the cron_completed event.
func (s *server) handleManageCronRun(req rpcRequest) {
	fields, rpcErr := manageDecodeWhitelist(req.Params, map[string]bool{"id": true}, "cron_field_not_allowed")
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	id, _, err := manageDecodeOptionalString(fields["id"])
	if err != nil || strings.TrimSpace(id) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "id is required", nil))
		return
	}
	scheduler, _, err := s.ensureManageCron()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "cron_unavailable", err.Error(), nil))
		return
	}
	if err := scheduler.RunNow(strings.TrimSpace(id)); err != nil {
		code := "cron_run_failed"
		switch {
		case errors.Is(err, cron.ErrJobAlreadyRunning):
			code = "cron_job_running"
		case strings.Contains(err.Error(), "not found"):
			code = "cron_job_not_found"
		}
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, code,
			fmt.Sprintf("run cron job: %v", err), nil))
		return
	}
	s.writeResponse(req.ID, map[string]any{"ok": true, "jobId": strings.TrimSpace(id), "triggered": true}, nil)
}

// --- §6.1 stats -----------------------------------------------------------------

type manageStatsRequest struct {
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	Group string `json:"group,omitempty"`
}

var manageStatsGroups = map[string]bool{"day": true, "1h": true, "week": true, "month": true}

// manageStatsQuery maps the request onto the shared stats.Query through
// stats.ParseQueryParams (the same parser the stats dashboard and serve use),
// adding an RFC3339 fallback and structured validation.
func manageStatsQuery(in manageStatsRequest) (stats.Query, *mcp.RPCError) {
	values := url.Values{}
	if value := strings.TrimSpace(in.From); value != "" {
		values.Set("from", value)
	}
	if value := strings.TrimSpace(in.To); value != "" {
		values.Set("to", value)
	}
	group := strings.TrimSpace(in.Group)
	if group == "" {
		group = "day"
	}
	if !manageStatsGroups[group] {
		return stats.Query{}, acpStructuredRPCError(-32602, "stats_group_invalid",
			fmt.Sprintf("group %q must be one of day, 1h, week, month", group), nil)
	}
	values.Set("groupBy", group)
	query := stats.ParseQueryParams(values)
	// ParseQueryParams only accepts YYYY-MM-DD; RFC3339 instants are an
	// additive convenience for protocol clients.
	if value := strings.TrimSpace(in.From); value != "" && query.From.IsZero() {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return stats.Query{}, acpStructuredRPCError(-32602, "stats_time_invalid",
				"from must use YYYY-MM-DD or RFC3339", nil)
		}
		query.From = parsed
	}
	if value := strings.TrimSpace(in.To); value != "" && query.To.IsZero() {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return stats.Query{}, acpStructuredRPCError(-32602, "stats_time_invalid",
				"to must use YYYY-MM-DD or RFC3339", nil)
		}
		query.To = parsed
	}
	return query, nil
}

// manageStatsDB opens the shared sessions.db stats queries. The second
// result reports whether the database exists; a missing database projects
// empty stats exactly like the serve stats endpoint.
func (s *server) manageStatsDB() (*stats.DB, bool, error) {
	if s.settings == nil {
		return nil, false, errors.New("ACP settings are unavailable")
	}
	dbPath := filepath.Join(s.settings.GetSessionDir(), "sessions.db")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	db, err := stats.Open(dbPath)
	if err != nil {
		return nil, false, err
	}
	return db, true, nil
}

func (s *server) handleManageStatsSummary(req rpcRequest) {
	var in manageStatsRequest
	if len(bytes.TrimSpace(req.Params)) > 0 {
		if err := json.Unmarshal(req.Params, &in); err != nil {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "invalid params", nil))
			return
		}
	}
	query, rpcErr := manageStatsQuery(in)
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	sessionsCount := 0
	if s.settings != nil {
		if details, err := session.ListAllDetailed(s.settings.GetSessionDir()); err == nil {
			sessionsCount = len(details)
		}
	}
	db, exists, err := s.manageStatsDB()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "stats_unavailable", err.Error(), nil))
		return
	}
	summary := &stats.Summary{}
	if exists {
		defer db.Close()
		summary, err = db.Summary(query)
		if err != nil {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "stats_unavailable", err.Error(), nil))
			return
		}
	}
	// request_stats records token usage but no monetary cost, so cost stays 0
	// until the shared schema tracks pricing (documented leftover).
	result := map[string]any{
		"sessions": sessionsCount,
		"runs":     summary.TotalRequests,
		"tokens": map[string]any{
			"input":  summary.InputTokens,
			"output": summary.OutputTokens,
			"total":  summary.TotalTokens,
		},
		"cost": 0.0,
	}
	if !query.From.IsZero() {
		result["since"] = query.From.UTC().Format(time.RFC3339)
	}
	if !query.To.IsZero() {
		result["until"] = query.To.UTC().Format(time.RFC3339)
	}
	s.writeResponse(req.ID, result, nil)
}

func (s *server) handleManageStatsTimeseries(req rpcRequest) {
	var in manageStatsRequest
	if len(bytes.TrimSpace(req.Params)) > 0 {
		if err := json.Unmarshal(req.Params, &in); err != nil {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "invalid params", nil))
			return
		}
	}
	query, rpcErr := manageStatsQuery(in)
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	if query.From.IsZero() {
		query.From = time.Now().AddDate(0, 0, -manageStatsDefaultDays)
	}
	points := make([]map[string]any, 0)
	db, exists, err := s.manageStatsDB()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "stats_unavailable", err.Error(), nil))
		return
	}
	if exists {
		defer db.Close()
		aggregates, err := db.TimeSeries(query)
		if err != nil {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "stats_unavailable", err.Error(), nil))
			return
		}
		for _, aggregate := range aggregates {
			points = append(points, map[string]any{
				"date":   aggregate.Label,
				"runs":   aggregate.Requests,
				"tokens": aggregate.TotalTokens,
				"cost":   0.0,
			})
		}
	}
	result := map[string]any{
		"group":  query.GroupBy,
		"points": points,
		"from":   query.From.UTC().Format(time.RFC3339),
	}
	if !query.To.IsZero() {
		result["to"] = query.To.UTC().Format(time.RFC3339)
	}
	s.writeResponse(req.ID, result, nil)
}

// --- §6.1 memory ------------------------------------------------------------------

// manageMemoryEnabled projects the memory feature flag from the serve
// configuration (its single source); the default config enables memory.
func manageMemoryEnabled() bool {
	cfg, err := serve.LoadConfig()
	if err != nil || cfg == nil {
		return true
	}
	return cfg.Features.Memory
}

// manageMemoryStore resolves memory.md through the same source as serve
// /api/memory: the serve config's explicit memory path when configured,
// otherwise the global ~/.mothx/memory.md. Read/write/resolve semantics stay
// in memory.Store.
func (s *server) manageMemoryStore() *memory.Store {
	explicitPath := ""
	if cfg, err := serve.LoadConfig(); err == nil && cfg != nil {
		explicitPath = strings.TrimSpace(cfg.Memory.Path)
	}
	if explicitPath == "" {
		explicitPath = filepath.Join(config.ConfigDir(), "memory.md")
	}
	return memory.NewStore(explicitPath, s.manageWorkDir())
}

func manageMemoryUpdatedAt(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339Nano)
}

// manageDeliveriesSessionDir resolves the session database directory for the
// deliveries projection. The negotiated server settings win; a direct fixture
// without settings falls back to the same global settings source as the other
// manage handlers.
func (s *server) manageDeliveriesSessionDir() (string, error) {
	if s != nil {
		s.mu.Lock()
		settings := s.settings
		s.mu.Unlock()
		if settings != nil && strings.TrimSpace(settings.GetSessionDir()) != "" {
			return settings.GetSessionDir(), nil
		}
	}
	settings, err := s.manageSettings()
	if err != nil {
		return "", err
	}
	return settings.GetSessionDir(), nil
}

// deliveryFailureRetryable reports whether the operation's failure is
// transport-level and therefore reopenable by the operator.
func deliveryFailureRetryable(failureCode string) bool {
	switch failureCode {
	case "delivery_retries_exhausted", "transport_error":
		return true
	default:
		return false
	}
}

func (s *server) handleManageDeliveriesList(req rpcRequest) {
	var in struct {
		SessionID string `json:"sessionId"`
		Limit     int    `json:"limit"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &in); err != nil {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "params must be an object", nil))
			return
		}
	}
	sessionDir, err := s.manageDeliveriesSessionDir()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "manage_unavailable", err.Error(), nil))
		return
	}
	failures, err := session.ListDeliveryFailures(context.Background(), sessionDir, in.SessionID, in.Limit)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "deliveries_unavailable", err.Error(), nil))
		return
	}
	items := make([]map[string]any, 0, len(failures))
	for _, failure := range failures {
		items = append(items, map[string]any{
			"operationId":   failure.OperationID,
			"intentId":      failure.IntentID,
			"sessionId":     failure.SessionID,
			"runId":         failure.RunID,
			"platform":      failure.Platform,
			"targetId":      failure.TargetID,
			"operationKind": failure.OperationKind,
			"status":        failure.Status,
			"failureCode":   failure.FailureCode,
			"attemptCount":  failure.AttemptCount,
			"updatedAt":     failure.UpdatedAt.UTC().Format(time.RFC3339Nano),
			"retryable":     deliveryFailureRetryable(failure.FailureCode),
		})
	}
	s.writeResponse(req.ID, map[string]any{"deliveries": items, "count": len(items)}, nil)
}

func (s *server) handleManageDeliveriesRetry(req rpcRequest) {
	var in struct {
		OperationID string `json:"operationId"`
	}
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.OperationID) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "operationId is required", nil))
		return
	}
	sessionDir, err := s.manageDeliveriesSessionDir()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "manage_unavailable", err.Error(), nil))
		return
	}
	reopened, err := session.ReopenFailedDeliveryOperation(context.Background(), sessionDir, in.OperationID, time.Now().UTC())
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "delivery_retry_failed", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, map[string]any{"operationId": in.OperationID, "retried": reopened}, nil)
}

func (s *server) handleManageMemoryGet(req rpcRequest) {
	store := s.manageMemoryStore()
	content, path, source, err := store.Read()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "memory_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, map[string]any{
		"content":   content,
		"path":      path,
		"source":    source,
		"size":      len(content),
		"updatedAt": manageMemoryUpdatedAt(path),
	}, nil)
}

func (s *server) handleManageMemoryPut(req rpcRequest) {
	var in struct {
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(req.Params, &in); err != nil || in.Content == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "content is required", nil))
		return
	}
	if len(*in.Content) > manageMemoryMaxBytes {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "memory_too_large",
			fmt.Sprintf("memory content exceeds the %d byte limit", manageMemoryMaxBytes),
			map[string]any{"size": len(*in.Content), "maxBytes": manageMemoryMaxBytes}))
		return
	}
	store := s.manageMemoryStore()
	if err := store.WriteAll(*in.Content); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "memory_unavailable", err.Error(), nil))
		return
	}
	content, path, source, err := store.Read()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "memory_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, map[string]any{
		"size":      len(content),
		"updatedAt": manageMemoryUpdatedAt(path),
		"path":      path,
		"source":    source,
	}, nil)
}
