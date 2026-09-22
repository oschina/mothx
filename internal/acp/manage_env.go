package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/mcp"
)

// Env management is a thin, secret-safe projection of internal/config/env.go.
// It exposes the global env.json through mothx/manage/env/*. Values are never
// returned, echoed, or leaked in errors; only sorted names and a configured
// flag are visible. Mutations use one atomic patch with explicit set/unset
// lists so the client cannot round-trip secret values.

type manageEnvVariableView struct {
	Name            string `json:"name"`
	ValueConfigured bool   `json:"valueConfigured"`
}

type manageEnvView struct {
	Variables []manageEnvVariableView `json:"variables"`
}

func manageEnvViewFromConfig(cfg *config.EnvConfig) manageEnvView {
	vars := cfg.List()
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	view := manageEnvView{Variables: make([]manageEnvVariableView, 0, len(names))}
	for _, name := range names {
		view.Variables = append(view.Variables, manageEnvVariableView{
			Name:            name,
			ValueConfigured: true,
		})
	}
	return view
}

var manageEnvPatchFields = map[string]bool{
	"set":   true,
	"unset": true,
}

var manageEnvSetEntryFields = map[string]bool{"name": true, "value": true}

func (s *server) handleManageEnvGet(req rpcRequest) {
	cfg := config.LoadEnv()
	s.writeResponse(req.ID, manageEnvViewFromConfig(cfg), nil)
}

func (s *server) handleManageEnvPatch(req rpcRequest) {
	fields, rpcErr := manageDecodeWhitelist(req.Params, manageEnvPatchFields, "env_field_not_allowed")
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	if len(fields) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"set or unset must contain at least one variable", nil))
		return
	}

	set, rpcErr := manageEnvDecodeSet(fields["set"])
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	unset, rpcErr := manageEnvDecodeUnset(fields["unset"])
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	if len(set) == 0 && len(unset) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"set or unset must contain at least one variable", nil))
		return
	}

	for name := range set {
		for _, u := range unset {
			if name == u {
				s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "env_name_conflict",
					fmt.Sprintf("name %q cannot appear in both set and unset", name), map[string]any{"name": name}))
				return
			}
		}
	}

	cfg := config.LoadEnv()
	if err := cfg.ApplyPatch(set, unset); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "env_save_failed", err.Error(), nil))
		return
	}

	s.writeResponse(req.ID, manageEnvViewFromConfig(cfg), nil)
}

func manageEnvDecodeSet(raw json.RawMessage) (map[string]string, *mcp.RPCError) {
	if len(raw) == 0 {
		return map[string]string{}, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, manageEnvFieldInvalid("set must be an array of {name, value} objects", "set", -1)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, manageEnvFieldInvalid("set must be an array of {name, value} objects", "set", -1)
	}
	set := make(map[string]string, len(entries))
	for i, entryRaw := range entries {
		fields, rpcErr := manageDecodeWhitelist(entryRaw, manageEnvSetEntryFields, "env_field_not_allowed")
		if rpcErr != nil {
			return nil, manageEnvFieldInvalid("each set entry may contain only name and value", "set", i)
		}
		if len(fields) != 2 || len(fields["name"]) == 0 || len(fields["value"]) == 0 {
			return nil, manageEnvFieldInvalid("each set entry requires name and value strings", "set", i)
		}
		name, value, rpcErr := manageEnvDecodeEntry(fields["name"], fields["value"], i)
		if rpcErr != nil {
			return nil, rpcErr
		}
		if _, exists := set[name]; exists {
			return nil, acpStructuredRPCError(-32602, "env_name_duplicate",
				fmt.Sprintf("set[%d]: duplicate name %q", i, name), map[string]any{"field": "set", "index": i})
		}
		set[name] = value
	}
	return set, nil
}

func manageEnvDecodeEntry(rawName, rawValue json.RawMessage, index int) (string, string, *mcp.RPCError) {
	if bytes.Equal(bytes.TrimSpace(rawName), []byte("null")) || bytes.Equal(bytes.TrimSpace(rawValue), []byte("null")) {
		return "", "", manageEnvFieldInvalid("each set entry requires name and value strings", "set", index)
	}
	var name, value string
	if err := json.Unmarshal(rawName, &name); err != nil {
		return "", "", manageEnvFieldInvalid("each set entry requires name and value strings", "set", index)
	}
	if err := json.Unmarshal(rawValue, &value); err != nil {
		return "", "", manageEnvFieldInvalid("each set entry requires name and value strings", "set", index)
	}
	name = strings.TrimSpace(name)
	if err := config.ValidateEnvName(name); err != nil {
		return "", "", acpStructuredRPCError(-32602, "env_name_invalid",
			fmt.Sprintf("set[%d]: %v", index, err), map[string]any{"field": "set", "index": index})
	}
	return name, value, nil
}

func manageEnvDecodeUnset(raw json.RawMessage) ([]string, *mcp.RPCError) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, manageEnvFieldInvalid("unset must be an array of variable names", "unset", -1)
	}
	var names []json.RawMessage
	if err := json.Unmarshal(trimmed, &names); err != nil {
		return nil, manageEnvFieldInvalid("unset must be an array of variable names", "unset", -1)
	}
	unset := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for i, rawName := range names {
		if bytes.Equal(bytes.TrimSpace(rawName), []byte("null")) {
			return nil, manageEnvFieldInvalid("unset must contain only names", "unset", i)
		}
		var name string
		if err := json.Unmarshal(rawName, &name); err != nil {
			return nil, manageEnvFieldInvalid("unset must contain only names", "unset", i)
		}
		name = strings.TrimSpace(name)
		if err := config.ValidateEnvName(name); err != nil {
			return nil, acpStructuredRPCError(-32602, "env_name_invalid",
				fmt.Sprintf("unset[%d]: %v", i, err), map[string]any{"field": "unset", "index": i})
		}
		if _, exists := seen[name]; exists {
			return nil, acpStructuredRPCError(-32602, "env_name_duplicate",
				fmt.Sprintf("unset[%d]: duplicate name %q", i, name), map[string]any{"field": "unset", "index": i})
		}
		seen[name] = struct{}{}
		unset = append(unset, name)
	}
	return unset, nil
}

func manageEnvFieldInvalid(message, field string, index int) *mcp.RPCError {
	data := map[string]any{"field": field}
	if index >= 0 {
		data["index"] = index
	}
	return acpStructuredRPCError(-32602, "env_field_invalid", message, data)
}
