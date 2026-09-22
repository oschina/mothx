package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/expert"
	"github.com/oschina/mothx/internal/mcp"
)

// Expert management is an additive ACP projection of internal/expert.Manager.
// Desktop never reads expert directories itself; Runtime keeps owning both
// layered resolution and session expert binding.

type manageExpertsRequest struct {
	Cwd    string                `json:"cwd,omitempty"`
	Scope  expert.Scope          `json:"scope,omitempty"`
	Name   string                `json:"name,omitempty"`
	Bundle *expert.ManagedBundle `json:"bundle,omitempty"`
	Meta   requestMeta           `json:"_meta,omitempty"`
}

func (s *server) manageExpertManager(in manageExpertsRequest) (*expert.Manager, expert.Scope, string, error) {
	scope := in.Scope
	if scope == "" {
		scope = expert.ScopeGlobal // Product default: newly managed teams are global.
	}
	if scope != expert.ScopeGlobal && scope != expert.ScopeProject {
		return nil, "", "", fmt.Errorf("expert scope %q is not supported", scope)
	}
	if scope == expert.ScopeGlobal {
		return expert.NewManager(""), scope, "", nil
	}
	cwd, _, err := s.resolveWorkspace(in.Meta, in.Cwd)
	if err != nil || strings.TrimSpace(cwd) == "" {
		if err == nil {
			err = fmt.Errorf("project scope requires a workspace cwd")
		}
		return nil, "", "", err
	}
	return expert.NewManager(cwd), scope, cwd, nil
}

func manageExpertRPCError(err error) *mcp.RPCError {
	message := err.Error()
	code := "expert_operation_failed"
	status := -32000
	if strings.Contains(message, "scope") || strings.Contains(message, "invalid") || strings.Contains(message, "requires") || strings.Contains(message, "not found") || strings.Contains(message, "already exists") {
		code = "expert_invalid_request"
		status = -32602
	}
	return acpStructuredRPCError(status, code, message, nil)
}

func (s *server) handleManageExpertsList(req rpcRequest) {
	var in manageExpertsRequest
	if len(bytes.TrimSpace(req.Params)) > 0 {
		if err := json.Unmarshal(req.Params, &in); err != nil {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "invalid params", nil))
			return
		}
	}
	manager, scope, cwd, err := s.manageExpertManager(in)
	if err != nil {
		s.writeResponse(req.ID, nil, manageExpertRPCError(err))
		return
	}
	items, err := manager.ListScope(scope)
	if err != nil {
		s.writeResponse(req.ID, nil, manageExpertRPCError(err))
		return
	}
	// effectiveExperts is what a new/loaded SessionRuntime can select; experts
	// is the scope-specific editable list requested by the management view.
	s.writeResponse(req.ID, map[string]any{"scope": scope, "cwd": cwd, "experts": items, "effectiveExperts": manager.List()}, nil)
}

func (s *server) handleManageExpertsGet(req rpcRequest) {
	var in manageExpertsRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "name is required", nil))
		return
	}
	manager, scope, cwd, err := s.manageExpertManager(in)
	if err != nil {
		s.writeResponse(req.ID, nil, manageExpertRPCError(err))
		return
	}
	bundle, err := manager.Get(scope, strings.TrimSpace(in.Name))
	if err != nil {
		s.writeResponse(req.ID, nil, manageExpertRPCError(err))
		return
	}
	s.writeResponse(req.ID, map[string]any{"scope": scope, "cwd": cwd, "bundle": bundle}, nil)
}

func (s *server) handleManageExpertsCreate(req rpcRequest) {
	var in manageExpertsRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || in.Bundle == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "bundle is required", nil))
		return
	}
	manager, scope, cwd, err := s.manageExpertManager(in)
	if err != nil {
		s.writeResponse(req.ID, nil, manageExpertRPCError(err))
		return
	}
	bundle, err := manager.Create(scope, *in.Bundle)
	if err != nil {
		s.writeResponse(req.ID, nil, manageExpertRPCError(err))
		return
	}
	s.writeResponse(req.ID, map[string]any{"scope": scope, "cwd": cwd, "bundle": bundle}, nil)
}

func (s *server) handleManageExpertsUpdate(req rpcRequest) {
	var in manageExpertsRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || in.Bundle == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "bundle is required", nil))
		return
	}
	manager, scope, cwd, err := s.manageExpertManager(in)
	if err != nil {
		s.writeResponse(req.ID, nil, manageExpertRPCError(err))
		return
	}
	bundle, err := manager.Update(scope, *in.Bundle)
	if err != nil {
		s.writeResponse(req.ID, nil, manageExpertRPCError(err))
		return
	}
	s.writeResponse(req.ID, map[string]any{"scope": scope, "cwd": cwd, "bundle": bundle}, nil)
}

func (s *server) handleManageExpertsDelete(req rpcRequest) {
	var in manageExpertsRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "name is required", nil))
		return
	}
	manager, scope, cwd, err := s.manageExpertManager(in)
	if err != nil {
		s.writeResponse(req.ID, nil, manageExpertRPCError(err))
		return
	}
	name := strings.TrimSpace(in.Name)
	if err := manager.Delete(scope, name); err != nil {
		s.writeResponse(req.ID, nil, manageExpertRPCError(err))
		return
	}
	s.writeResponse(req.ID, map[string]any{"deleted": true, "scope": scope, "cwd": cwd, "name": name}, nil)
}
