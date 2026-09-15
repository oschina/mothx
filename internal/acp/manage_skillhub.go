package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/mcp"
)

// manageSkillHubMarketView is the safe, token-free projection of one SkillHub
// market entry. The apiToken field is never echoed; clients learn only whether
// a usable token is currently configured.
type manageSkillHubMarketView struct {
	ID                 string `json:"id"`
	Name               string `json:"name,omitempty"`
	SiteURL            string `json:"siteURL,omitempty"`
	APIURL             string `json:"apiURL,omitempty"`
	Enabled            bool   `json:"enabled"`
	APITokenConfigured bool   `json:"apiTokenConfigured"`
}

// manageSkillHubView assembles the token-free SkillHub view model.
func manageSkillHubView(settings *config.Settings) map[string]any {
	viewMarkets := make([]manageSkillHubMarketView, 0, len(settings.SkillHub.Markets))
	for _, market := range settings.SkillHub.Markets {
		viewMarkets = append(viewMarkets, manageSkillHubMarketView{
			ID:                 market.ID,
			Name:               market.Name,
			SiteURL:            market.SiteURL,
			APIURL:             market.APIURL,
			Enabled:            market.Enabled,
			APITokenConfigured: manageSecretUsable(market.APIToken),
		})
	}
	officialHandles := settings.SkillHub.OfficialHandles
	if officialHandles == nil {
		officialHandles = []string{}
	}
	defaultInstallScope := settings.SkillHub.DefaultInstallScope
	if strings.TrimSpace(defaultInstallScope) == "" {
		defaultInstallScope = config.DefaultSettings().SkillHub.DefaultInstallScope
	}
	defaultMarket := manageSkillHubDefaultMarket(settings)
	return map[string]any{
		"defaultMarket":       defaultMarket,
		"defaultInstallScope": defaultInstallScope,
		"officialHandles":     officialHandles,
		"markets":             viewMarkets,
	}
}

// manageSkillHubDefaultMarket resolves the canonical default market once: the
// persisted setting wins, and the product default (skillhub.cn) fills an empty
// value. Every ACP projection and catalog fallback must use this resolver
// instead of guessing from market ordering or an adapter-local constant.
func manageSkillHubDefaultMarket(settings *config.Settings) string {
	if settings == nil {
		return config.DefaultSettings().SkillHub.DefaultMarket
	}
	defaultMarket := strings.TrimSpace(settings.SkillHub.DefaultMarket)
	if defaultMarket == "" {
		defaultMarket = config.DefaultSettings().SkillHub.DefaultMarket
	}
	return defaultMarket
}

// handleManageSkillHubGet projects the current SkillHub settings without ever
// exposing an apiToken value.
func (s *server) handleManageSkillHubGet(req rpcRequest) {
	settings, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, manageSkillHubView(settings), nil)
}

var manageSkillHubPatchTopFields = map[string]bool{
	"defaultMarket":       true,
	"defaultInstallScope": true,
	"officialHandles":     true,
	"markets":             true,
}

var manageSkillHubMarketFields = map[string]bool{
	"id":            true,
	"name":          true,
	"siteURL":       true,
	"apiURL":        true,
	"enabled":       true,
	"apiToken":      true,
	"clearApiToken": true,
}

var manageSkillHubAllowedScopes = map[string]bool{
	"project": true,
	"global":  true,
}

// handleManageSkillHubPatch applies a whitelist patch to the global skillHub
// object. Market tokens are write-only: an omitted apiToken keeps the existing
// secret, clearApiToken=true removes it, and a submitted apiToken overwrites it.
// Unknown sibling fields on the skillHub object and on each market object are
// preserved byte-identical.
func (s *server) handleManageSkillHubPatch(req rpcRequest) {
	var envelope struct {
		Patch json.RawMessage `json:"patch"`
	}
	if err := json.Unmarshal(req.Params, &envelope); err != nil || len(bytes.TrimSpace(envelope.Patch)) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"patch object with at least one supported skillHub field is required", nil))
		return
	}
	fields, rpcErr := manageDecodeWhitelist(envelope.Patch, manageSkillHubPatchTopFields, "skillhub_field_not_allowed")
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	if len(fields) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"patch object with at least one supported skillHub field is required", nil))
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

	// Pre-validate every supplied field before touching the raw settings object.
	if err := manageValidateSkillHubPatch(fields); err != nil {
		if rpcErr, ok := err.(*mcp.RPCError); ok {
			s.writeResponse(req.ID, nil, rpcErr)
			return
		}
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}

	if err := manageMergeRawObject(raw, "skillHub", func(skillHub map[string]json.RawMessage) error {
		for field, value := range fields {
			switch field {
			case "defaultMarket":
				text, _, _ := manageDecodeOptionalString(value)
				skillHub["defaultMarket"] = mustManageJSON(strings.TrimSpace(text))
			case "defaultInstallScope":
				text, _, _ := manageDecodeOptionalString(value)
				skillHub["defaultInstallScope"] = mustManageJSON(strings.TrimSpace(text))
			case "officialHandles":
				skillHub["officialHandles"] = value
			case "markets":
				drafts, _ := manageDecodeSkillHubMarkets(value)
				existing := manageSkillHubExistingMarkets(skillHub)
				merged, err := manageMergeSkillHubMarkets(existing, drafts)
				if err != nil {
					return err
				}
				skillHub["markets"] = mustManageJSON(merged)
			}
		}
		return nil
	}); err != nil {
		if rpcErr, ok := err.(*mcp.RPCError); ok {
			s.writeResponse(req.ID, nil, rpcErr)
			return
		}
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}

	if err := config.SaveGlobalSettingsPatch(map[string]any{"skillHub": raw["skillHub"]}); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_save_failed",
			manageRedactSecrets(err.Error(), settings), nil))
		return
	}
	updated, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, manageSkillHubView(updated), nil)
}

// manageValidateSkillHubPatch validates the supplied top-level fields and the
// market array without altering settings. It returns an *mcp.RPCError for
// validation failures so the caller can project a structured error response.
func manageValidateSkillHubPatch(fields map[string]json.RawMessage) error {
	for field, value := range fields {
		switch field {
		case "defaultMarket":
			text, present, err := manageDecodeOptionalString(value)
			if err != nil || !present || strings.TrimSpace(text) == "" {
				return acpStructuredRPCError(-32602, "skillhub_field_invalid",
					"a non-empty string value is required", map[string]any{"field": field})
			}
		case "defaultInstallScope":
			text, present, err := manageDecodeOptionalString(value)
			if err != nil || !present || !manageSkillHubAllowedScopes[strings.TrimSpace(text)] {
				return acpStructuredRPCError(-32602, "skillhub_field_invalid",
					"install scope must be one of project, global", map[string]any{"field": field})
			}
		case "officialHandles":
			var handles []string
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &handles) != nil {
				return acpStructuredRPCError(-32602, "skillhub_field_invalid",
					"an array of strings is required", map[string]any{"field": field})
			}
			for _, handle := range handles {
				if strings.TrimSpace(handle) == "" {
					return acpStructuredRPCError(-32602, "skillhub_field_invalid",
						"array entries must be non-empty strings", map[string]any{"field": field})
				}
			}
		case "markets":
			drafts, rpcErr := manageDecodeSkillHubMarkets(value)
			if rpcErr != nil {
				return rpcErr
			}
			seen := make(map[string]struct{}, len(drafts))
			for _, draft := range drafts {
				if _, ok := seen[draft.ID]; ok {
					return acpStructuredRPCError(-32602, "skillhub_field_invalid",
						fmt.Sprintf("duplicate market id %q", draft.ID), map[string]any{"field": "markets", "id": draft.ID})
				}
				seen[draft.ID] = struct{}{}
			}
		}
	}
	return nil
}

// manageSkillHubMarketDraft carries one validated market entry from a patch
// request. Unknown fields from persisted market entries are retained during
// merge, but requests themselves have a strict whitelist.
type manageSkillHubMarketDraft struct {
	ID            string
	Name          string
	SiteURL       string
	APIURL        string
	Enabled       bool
	APIToken      *string
	ClearAPIToken bool
}

// manageDecodeSkillHubMarkets parses the markets array and validates every
// supplied field before returning the normalized market drafts.
func manageDecodeSkillHubMarkets(raw json.RawMessage) ([]manageSkillHubMarketDraft, *mcp.RPCError) {
	var items []json.RawMessage
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &items) != nil {
		return nil, acpStructuredRPCError(-32602, "skillhub_field_invalid", "markets must be an array of objects", map[string]any{"field": "markets"})
	}

	drafts := make([]manageSkillHubMarketDraft, 0, len(items))
	for i, item := range items {
		fields, rpcErr := manageDecodeWhitelist(item, manageSkillHubMarketFields, "skillhub_market_field_not_allowed")
		if rpcErr != nil {
			// Keep the original whitelist error details while attaching the
			// market-array location. This must live in production code rather
			// than relying on the similarly named test helper.
			data := map[string]any{}
			if original, ok := rpcErr.Data.(map[string]any); ok {
				for key, value := range original {
					data[key] = value
				}
			}
			data["field"] = fmt.Sprintf("markets[%d]", i)
			return nil, acpStructuredRPCError(-32602, "skillhub_market_field_not_allowed",
				rpcErr.Message, data)
		}
		idRaw := fields["id"]
		var id string
		if err := json.Unmarshal(idRaw, &id); err != nil {
			return nil, acpStructuredRPCError(-32602, "skillhub_field_invalid",
				"market id must be a non-empty string", map[string]any{"field": fmt.Sprintf("markets[%d].id", i)})
		}
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, acpStructuredRPCError(-32602, "skillhub_field_invalid",
				"market id is required", map[string]any{"field": fmt.Sprintf("markets[%d].id", i)})
		}
		draft := manageSkillHubMarketDraft{ID: id}
		if nameRaw, ok := fields["name"]; ok {
			if err := json.Unmarshal(nameRaw, &draft.Name); err != nil {
				return nil, manageSkillHubMarketFieldError(i, "name", "market name must be a string")
			}
		}
		if siteRaw, ok := fields["siteURL"]; ok {
			if err := json.Unmarshal(siteRaw, &draft.SiteURL); err != nil {
				return nil, manageSkillHubMarketFieldError(i, "siteURL", "site URL must be a string")
			}
		}
		if apiRaw, ok := fields["apiURL"]; ok {
			if err := json.Unmarshal(apiRaw, &draft.APIURL); err != nil {
				return nil, manageSkillHubMarketFieldError(i, "apiURL", "API URL must be a string")
			}
		}
		if enabledRaw, ok := fields["enabled"]; ok {
			if err := json.Unmarshal(enabledRaw, &draft.Enabled); err != nil {
				return nil, manageSkillHubMarketFieldError(i, "enabled", "enabled must be a boolean")
			}
		}
		if tokenRaw, ok := fields["apiToken"]; ok {
			var token string
			if err := json.Unmarshal(tokenRaw, &token); err != nil {
				return nil, manageSkillHubMarketFieldError(i, "apiToken", "API token must be a string")
			}
			draft.APIToken = &token
		}
		if clearRaw, ok := fields["clearApiToken"]; ok {
			if err := json.Unmarshal(clearRaw, &draft.ClearAPIToken); err != nil {
				return nil, manageSkillHubMarketFieldError(i, "clearApiToken", "clearApiToken must be a boolean")
			}
		}
		if draft.ClearAPIToken && draft.APIToken != nil {
			return nil, manageSkillHubMarketFieldError(i, "apiToken", "apiToken and clearApiToken cannot be set together")
		}
		drafts = append(drafts, draft)
	}
	return drafts, nil
}

func manageSkillHubMarketFieldError(index int, field, message string) *mcp.RPCError {
	return acpStructuredRPCError(-32602, "skillhub_field_invalid", message,
		map[string]any{"field": fmt.Sprintf("markets[%d].%s", index, field)})
}

// manageSkillHubExistingMarkets reads the current markets array from raw
// settings and indexes them by id, preserving all fields including secrets.
func manageSkillHubExistingMarkets(skillHub map[string]json.RawMessage) map[string]map[string]json.RawMessage {
	result := map[string]map[string]json.RawMessage{}
	marketsRaw, ok := skillHub["markets"]
	if !ok || len(bytes.TrimSpace(marketsRaw)) == 0 {
		return result
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(marketsRaw, &items); err != nil {
		return result
	}
	for _, item := range items {
		idRaw, ok := item["id"]
		if !ok {
			continue
		}
		var id string
		if err := json.Unmarshal(idRaw, &id); err != nil {
			continue
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		copy := map[string]json.RawMessage{}
		for k, v := range item {
			copy[k] = v
		}
		result[id] = copy
	}
	return result
}

// manageMergeSkillHubMarkets builds the final markets array in patch order.
// Existing markets supply their apiToken when the patch omits one, and
// clearApiToken=true removes it. Duplicate ids in the patch are rejected.
func manageMergeSkillHubMarkets(existing map[string]map[string]json.RawMessage, drafts []manageSkillHubMarketDraft) ([]map[string]json.RawMessage, error) {
	seen := make(map[string]struct{}, len(drafts))
	merged := make([]map[string]json.RawMessage, 0, len(drafts))
	for _, draft := range drafts {
		if _, ok := seen[draft.ID]; ok {
			return nil, fmt.Errorf("duplicate market id %q", draft.ID)
		}
		seen[draft.ID] = struct{}{}

		entry := map[string]json.RawMessage{}
		if old, ok := existing[draft.ID]; ok {
			for k, v := range old {
				entry[k] = v
			}
		}
		entry["id"] = mustManageJSON(draft.ID)
		entry["name"] = mustManageJSON(draft.Name)
		entry["siteURL"] = mustManageJSON(draft.SiteURL)
		entry["apiURL"] = mustManageJSON(draft.APIURL)
		entry["enabled"] = mustManageJSON(draft.Enabled)
		if draft.ClearAPIToken {
			delete(entry, "apiToken")
		} else if draft.APIToken != nil {
			entry["apiToken"] = mustManageJSON(*draft.APIToken)
		}
		merged = append(merged, entry)
	}
	return merged, nil
}

// mustManageJSON marshals a value for raw-object merging. It panics only on
// values that encoding/json cannot represent, which never occur here.
func mustManageJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal %v: %v", value, err))
	}
	return data
}
