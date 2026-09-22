package channels

import (
	"fmt"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/util"
)

// Security provides user whitelist validation and smart approval logic for messaging channel mode.
type Security struct {
	cfg *Config
}

// NewSecurity creates a security manager.
func NewSecurity(cfg *Config) *Security {
	return &Security{cfg: cfg}
}

// CheckWorkDirAllowed returns nil if the working directory is allowed.
func (s *Security) CheckWorkDirAllowed(workDir string) error {
	allowed := s.cfg.Security.AllowedWorkDirs
	if len(allowed) == 0 {
		// No restriction
		return nil
	}

	for _, dir := range allowed {
		within, err := util.IsWithinPath(dir, workDir)
		if err == nil && within {
			return nil
		}
	}

	return fmt.Errorf("working directory %s not in allowed_work_dirs", workDir)
}

// CommandRiskLevel classifies the risk level of a bash command.
// Returns "low", "medium", or "high".
func CommandRiskLevel(command string) string {
	return string(agentruntime.ClassifyBashCommand(command))
}

// ApprovalDecision represents the result of an approval check.
type ApprovalDecision struct {
	Approved  bool
	Reason    string
	RiskLevel string
}

// FormatApprovalNotification formats a notification for medium/high risk tool calls.
func FormatApprovalNotification(toolName string, args map[string]any, riskLevel string, approved bool) string {
	var icon, status string
	if approved {
		icon = "⚠️"
		status = "auto-approved"
	} else {
		icon = "🚫"
		status = "blocked"
	}

	var detail string
	if toolName == "bash" {
		if cmd, ok := args["command"]; ok {
			cmdStr := fmt.Sprintf("%v", cmd)
			if len(cmdStr) > 80 {
				cmdStr = cmdStr[:80] + "..."
			}
			detail = cmdStr
		}
	} else {
		if path, ok := args["path"]; ok {
			detail = fmt.Sprintf("%v", path)
		}
	}

	if detail != "" {
		return fmt.Sprintf("%s [%s] %s %s (%s risk)", icon, toolName, detail, status, riskLevel)
	}
	return fmt.Sprintf("%s [%s] %s (%s risk)", icon, toolName, status, riskLevel)
}

// ShouldAutoApprove returns true if the tool call can be auto-approved in messaging channel mode.
// In messaging channel mode, bots run unattended so we need stricter auto-approval rules.
func (s *Security) ShouldAutoApprove(toolName string, args map[string]any, mode string) bool {
	if !s.cfg.Security.SmartApprovals {
		// Smart approvals disabled — fall back to mode-based behavior
		return mode == "yolo" || mode == "os"
	}

	switch toolName {
	case "read", "ls", "grep", "find", "skill_ref", "memory", "plan", "jobs":
		// Read-only tools: always auto-approve
		return true

	case "write", "edit":
		// File modifications: auto-approve in agent/yolo mode
		return mode == "agent" || mode == "yolo" || mode == "os"

	case "bash":
		command, _ := args["command"].(string)
		risk := CommandRiskLevel(command)
		switch mode {
		case "yolo", "os":
			return risk != "high" // yolo/os still block high-risk
		case "agent":
			return risk == "low" // agent only auto-approves low-risk
		default:
			return false
		}

	case "kill":
		return mode == "agent" || mode == "yolo" || mode == "os"

	default:
		return mode == "yolo" || mode == "os"
	}
}
