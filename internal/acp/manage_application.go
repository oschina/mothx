package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/mcp"
)

// Application settings are the safe, Runtime-owned subset of settings.json
// that WebUI exposes through its application settings form. This ACP contract
// deliberately excludes provider credentials, image-generation tokens,
// arbitrary provider headers, SkillHub tokens, and sandbox passEnv values.
// Those values must never be echoed to a Desktop renderer.
var manageApplicationSections = map[string]map[string]bool{
	"defaults": {
		"defaultMode": true, "enablePlanTool": true, "enableArtifact": true, "enableACPArtifact": true,
		"authored": true, "updateCheck": true,
	},
	"contextFiles": {
		"enabled": true, "extraFiles": true,
	},
	"compaction": {
		"enabled": true, "reserveTokens": true, "keepRecentTokens": true,
		"tokenizer": true, "tokenizerModel": true, "template": true,
	},
	"toolExecution": {
		"mode": true, "maxConcurrency": true,
	},
	"webSearch": {
		"enabled": true, "provider": true, "providerType": true, "model": true,
	},
	"imageGeneration": {
		"enabled": true, "provider": true, "apiType": true, "baseUrl": true, "model": true, "token": true,
	},
	"retry": {
		"enabled": true, "maxRetries": true, "baseDelayMs": true,
	},
	"statusLine": {
		"enabled": true, "type": true, "command": true, "padding": true,
		"refreshInterval": true, "timeoutMs": true, "fallback": true,
	},
	"sandbox": {
		"enabled": true, "level": true, "bwrapPath": true, "allowNetwork": true,
		"allowedRead": true, "allowedWrite": true, "deniedPaths": true, "tmpSize": true, "protectGit": true,
	},
	"approval": {
		"bashWhitelist": true, "bashBlacklist": true, "confirmBeforeWrite": true,
	},
}

var manageApplicationConfigKey = map[string]string{
	"contextFiles":    "contextFiles",
	"compaction":      "compaction",
	"toolExecution":   "toolExecution",
	"webSearch":       "webSearch",
	"imageGeneration": "imageGeneration",
	"retry":           "retry",
	"statusLine":      "statusLine",
	"sandbox":         "sandbox",
	"approval":        "approval",
}

var manageApplicationSectionNames = map[string]bool{
	"defaults": true, "contextFiles": true, "compaction": true, "toolExecution": true,
	"webSearch": true, "imageGeneration": true, "retry": true, "statusLine": true,
	"sandbox": true, "approval": true,
}

func manageOptionalBool(value *bool) bool {
	return value != nil && *value
}

func manageApplicationView(settings *config.Settings) map[string]any {
	defaultMode := strings.TrimSpace(settings.DefaultMode)
	if defaultMode == "" {
		defaultMode = agentruntime.ModeYolo
	}
	return map[string]any{
		"defaults": map[string]any{
			"defaultMode": defaultMode, "enablePlanTool": manageOptionalBool(settings.EnablePlanTool),
			"enableArtifact": settings.IsArtifactEnabled(), "enableACPArtifact": settings.IsACPArtifactEnabled(),
			"authored": settings.Authored, "updateCheck": settings.UpdateCheck == nil || *settings.UpdateCheck,
		},
		"contextFiles": map[string]any{
			"enabled": settings.ContextFiles.Enabled, "extraFiles": append([]string(nil), settings.ContextFiles.ExtraFiles...),
		},
		"compaction": map[string]any{
			"enabled": settings.Compaction.Enabled, "reserveTokens": settings.Compaction.ReserveTokens,
			"keepRecentTokens": settings.Compaction.KeepRecentTokens, "tokenizer": settings.Compaction.Tokenizer,
			"tokenizerModel": settings.Compaction.TokenizerModel, "template": settings.Compaction.Template,
		},
		"toolExecution": map[string]any{
			"mode": settings.ToolExecution.EffectiveMode(), "maxConcurrency": settings.ToolExecution.EffectiveMaxConcurrency(),
		},
		"webSearch": map[string]any{
			"enabled": settings.IsWebSearchEnabled(), "provider": settings.WebSearch.Provider,
			"providerType": settings.WebSearch.ProviderType, "model": settings.WebSearch.Model,
		},
		"imageGeneration": map[string]any{
			"enabled": settings.IsImageGenerationEnabled(), "provider": settings.ImageGeneration.Provider,
			"apiType": settings.ImageGeneration.APIType, "baseUrl": settings.ImageGeneration.BaseURL,
			"model":           settings.ImageGeneration.Model,
			"tokenConfigured": manageSecretUsable(settings.ResolveImageGenerationToken()),
		},
		"retry": map[string]any{
			"enabled": settings.Retry.Enabled, "maxRetries": settings.Retry.MaxRetries, "baseDelayMs": settings.Retry.BaseDelayMs,
		},
		"statusLine": map[string]any{
			"enabled": settings.StatusLine.Enabled, "type": settings.StatusLine.Type, "command": settings.StatusLine.Command,
			"padding": settings.StatusLine.Padding, "refreshInterval": settings.StatusLine.RefreshInterval,
			"timeoutMs": settings.StatusLine.TimeoutMs, "fallback": settings.StatusLine.Fallback,
		},
		"sandbox": map[string]any{
			"enabled": settings.Sandbox.Enabled, "level": settings.Sandbox.Level, "bwrapPath": settings.Sandbox.BwrapPath,
			"allowNetwork": settings.Sandbox.AllowNetwork, "allowedRead": append([]string(nil), settings.Sandbox.AllowedRead...),
			"allowedWrite": append([]string(nil), settings.Sandbox.AllowedWrite...),
			"deniedPaths":  append([]string(nil), settings.Sandbox.DeniedPaths...), "tmpSize": settings.Sandbox.TmpSize,
			"protectGit": settings.Sandbox.ProtectGit,
		},
		"approval": map[string]any{
			"bashWhitelist":      append([]string(nil), settings.Approval.BashWhitelist...),
			"bashBlacklist":      append([]string(nil), settings.Approval.BashBlacklist...),
			"confirmBeforeWrite": manageOptionalBool(settings.Approval.ConfirmBeforeWrite),
		},
	}
}

func (s *server) handleManageApplicationGet(req rpcRequest) {
	settings, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, manageApplicationView(settings), nil)
}

func manageApplicationDecodeSection(section string, raw json.RawMessage) (map[string]json.RawMessage, *mcp.RPCError) {
	allowed, ok := manageApplicationSections[section]
	if !ok {
		return nil, acpStructuredRPCError(-32602, "application_section_not_allowed",
			fmt.Sprintf("application section %q is not supported", section), map[string]any{"section": section})
	}
	fields, rpcErr := manageDecodeWhitelist(raw, allowed, "application_field_not_allowed")
	if rpcErr != nil {
		return nil, rpcErr
	}
	for field, value := range fields {
		if err := manageApplicationValidateField(section, field, value); err != nil {
			return nil, acpStructuredRPCError(-32602, "application_field_invalid",
				fmt.Sprintf("field %s.%s: %v", section, field, err),
				map[string]any{"section": section, "field": field})
		}
	}
	return fields, nil
}

func manageApplicationValidateField(section, field string, raw json.RawMessage) error {
	boolField := func() error {
		_, present, err := manageDecodeOptionalBool(raw)
		if err != nil || !present {
			return fmt.Errorf("a boolean value is required")
		}
		return nil
	}
	stringField := func() error {
		_, present, err := manageDecodeOptionalString(raw)
		if err != nil || !present {
			return fmt.Errorf("a string value is required")
		}
		return nil
	}
	intField := func(min int) error {
		var value int
		if err := json.Unmarshal(raw, &value); err != nil || value < min {
			return fmt.Errorf("an integer >= %d is required", min)
		}
		return nil
	}
	stringListField := func() error {
		var values []string
		if err := json.Unmarshal(raw, &values); err != nil {
			return fmt.Errorf("an array of strings is required")
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("array entries must be non-empty strings")
			}
		}
		return nil
	}

	switch section {
	case "defaults":
		switch field {
		case "defaultMode":
			value, _, err := manageDecodeOptionalString(raw)
			if err != nil || !manageAllowedModes[strings.TrimSpace(value)] {
				return fmt.Errorf("must be one of agent, plan, yolo, os")
			}
			return nil
		default:
			return boolField()
		}
	case "contextFiles":
		if field == "extraFiles" {
			return stringListField()
		}
		return boolField()
	case "compaction":
		if field == "enabled" {
			return boolField()
		}
		if field == "reserveTokens" || field == "keepRecentTokens" {
			return intField(0)
		}
		return stringField()
	case "toolExecution":
		if field == "maxConcurrency" {
			return intField(1)
		}
		value, _, err := manageDecodeOptionalString(raw)
		if err != nil || (value != "parallel" && value != "sequential") {
			return fmt.Errorf("must be parallel or sequential")
		}
		return nil
	case "webSearch", "imageGeneration":
		if field == "enabled" {
			return boolField()
		}
		return stringField()
	case "retry":
		if field == "enabled" {
			return boolField()
		}
		return intField(0)
	case "statusLine":
		if field == "enabled" {
			return boolField()
		}
		if field == "padding" || field == "refreshInterval" || field == "timeoutMs" {
			return intField(0)
		}
		return stringField()
	case "sandbox":
		if field == "enabled" || field == "allowNetwork" || field == "protectGit" {
			return boolField()
		}
		if field == "allowedRead" || field == "allowedWrite" || field == "deniedPaths" {
			return stringListField()
		}
		return stringField()
	case "approval":
		if field == "confirmBeforeWrite" {
			return boolField()
		}
		return stringListField()
	default:
		return fmt.Errorf("unsupported application section")
	}
}

func (s *server) handleManageApplicationPatch(req rpcRequest) {
	var envelope struct {
		Patch json.RawMessage
	}
	if err := json.Unmarshal(req.Params, &envelope); err != nil || len(bytes.TrimSpace(envelope.Patch)) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"patch object with at least one supported application section is required", nil))
		return
	}
	sections, rpcErr := manageDecodeWhitelist(envelope.Patch, manageApplicationSectionNames, "application_section_not_allowed")
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	if len(sections) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"patch object with at least one supported application section is required", nil))
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
	names := make([]string, 0, len(sections))
	for section := range sections {
		names = append(names, section)
	}
	sort.Strings(names)
	updates := map[string]any{}
	for _, section := range names {
		fields, sectionErr := manageApplicationDecodeSection(section, sections[section])
		if sectionErr != nil {
			s.writeResponse(req.ID, nil, sectionErr)
			return
		}
		if section == "defaults" {
			for field, value := range fields {
				updates[field] = json.RawMessage(value)
			}
			continue
		}
		configKey := manageApplicationConfigKey[section]
		if err := manageMergeRawObject(raw, configKey, func(target map[string]json.RawMessage) error {
			for field, value := range fields {
				target[field] = value
			}
			return nil
		}); err != nil {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
			return
		}
		updates[configKey] = raw[configKey]
	}
	if err := config.SaveGlobalSettingsPatch(updates); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_save_failed",
			manageRedactSecrets(err.Error(), settings), nil))
		return
	}
	updated, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	s.applyACPArtifactSetting(updated.IsACPArtifactEnabled())
	s.writeResponse(req.ID, manageApplicationView(updated), nil)
}
