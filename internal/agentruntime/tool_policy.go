package agentruntime

import (
	"path"
	"strings"

	"github.com/oschina/mothx/internal/agent"
)

// CommandRisk is the unattended-execution risk assigned to a bash command.
type CommandRisk string

const (
	CommandRiskLow    CommandRisk = "low"
	CommandRiskMedium CommandRisk = "medium"
	CommandRiskHigh   CommandRisk = "high"
)

// ToolCallPolicyDecision is the source policy result for one tool call.
type ToolCallPolicyDecision struct {
	Block  bool
	Reason string
}

// EvaluateToolCall applies non-overridable source policy before approval. A
// forced mode controls agent behavior only; it never disables this guard.
func (p ExecutionPolicy) EvaluateToolCall(toolName string, args map[string]any) ToolCallPolicyDecision {
	if !p.HasForcedMode() || toolName != "bash" {
		return ToolCallPolicyDecision{}
	}
	command, _ := args["command"].(string)
	if ClassifyBashCommand(command) != CommandRiskHigh {
		return ToolCallPolicyDecision{}
	}
	return ToolCallPolicyDecision{
		Block:  true,
		Reason: "channel execution policy blocked high risk bash command",
	}
}

// ClassifyBashCommand classifies command risk for unattended execution. High
// risk detection tokenizes shell control operators and executable paths so
// quoting, compound commands, and common flag variants cannot bypass it.
func ClassifyBashCommand(command string) CommandRisk {
	command = strings.TrimSpace(command)
	if containsHighRiskBash(command) {
		return CommandRiskHigh
	}

	mediumRiskPrefixes := []string{
		"mv ", "cp -r",
		"git push", "git reset --hard", "git clean",
		"npm publish", "go install",
		"apt ", "yum ", "brew ", "pip install",
		"docker ", "kubectl ",
		"curl ", "wget ",
		"ssh ", "scp ",
	}
	for _, prefix := range mediumRiskPrefixes {
		if strings.HasPrefix(command, prefix) {
			return CommandRiskMedium
		}
	}

	lowRiskPrefixes := []string{
		"go ", "make ", "npm ", "yarn ", "node ",
		"python ", "pip ",
		"git status", "git log", "git diff", "git branch",
		"ls", "cat ", "head ", "tail ", "wc ",
		"echo ", "printf ",
		"grep ", "find ", "which ", "type ",
		"cd ", "pwd", "env", "printenv",
	}
	for _, prefix := range lowRiskPrefixes {
		if strings.HasPrefix(command, prefix) {
			return CommandRiskLow
		}
	}

	return CommandRiskMedium
}

func containsHighRiskBash(command string) bool {
	tokens := bashRiskTokens(command)
	for i, token := range tokens {
		base := executableBase(token)
		switch {
		case base == "rm":
			if segmentHasFlag(tokens, i+1, 'r', "--recursive") || segmentHasFlag(tokens, i+1, 'R', "--recursive") {
				return true
			}
		case base == "dd", base == "shred", strings.HasPrefix(base, "mkfs"):
			return true
		case base == "chmod":
			if segmentContains(tokens, i+1, "777") || segmentHasFlag(tokens, i+1, 'R', "--recursive") {
				return true
			}
		case base == "chown":
			if segmentHasFlag(tokens, i+1, 'R', "--recursive") {
				return true
			}
		case base == "sudo", base == "su", base == "shutdown", base == "reboot", base == "halt":
			return true
		case base == "killall":
			return true
		case base == "kill":
			if segmentContains(tokens, i+1, "-9", "--signal=KILL", "--signal=9") {
				return true
			}
		case base == "eval", base == "exec":
			return true
		case isShellExecutable(base):
			if segmentHasFlag(tokens, i+1, 'c', "--command") {
				return true
			}
		case isInlineInterpreter(base):
			if segmentHasFlag(tokens, i+1, 'c', "--command") || segmentHasFlag(tokens, i+1, 'e', "--eval") {
				return true
			}
		case base == "git":
			if segmentContainsSequence(tokens, i+1, "reset", "--hard") || segmentContainsForcedGitClean(tokens, i+1) {
				return true
			}
		case base == "find":
			if segmentContains(tokens, i+1, "-delete") {
				return true
			}
		}

		if token == ">" && i+1 < len(tokens) && strings.HasPrefix(tokens[i+1], "/dev/") {
			return true
		}
		if token == "|" && i+1 < len(tokens) && isShellExecutable(executableBase(tokens[i+1])) {
			return true
		}
	}
	return false
}

func bashRiskTokens(command string) []string {
	command = strings.NewReplacer(
		"&&", " && ", "||", " || ", ">>", " >> ", "<<", " << ",
		";", " ; ", "|", " | ", "&", " & ", ">", " > ", "<", " < ",
		"(", " ( ", ")", " ) ", "{", " { ", "}", " } ", "`", " ` ",
		"\n", " ; ", "\r", " ; ",
	).Replace(command)
	command = strings.Map(func(char rune) rune {
		switch char {
		case '\'', '"', '\\':
			return -1
		default:
			return char
		}
	}, command)
	return strings.Fields(command)
}

func executableBase(token string) string {
	return path.Base(strings.TrimSpace(token))
}

func isShellExecutable(base string) bool {
	switch base {
	case "sh", "bash", "dash", "zsh", "ksh", "fish":
		return true
	default:
		return false
	}
}

func isInlineInterpreter(base string) bool {
	switch base {
	case "python", "python2", "python3", "node", "perl", "ruby", "php", "pwsh", "powershell":
		return true
	default:
		return false
	}
}

func isShellBoundary(token string) bool {
	switch token {
	case ";", "&&", "||", "|", "&", "(", ")", "{", "}", "`", ">", ">>", "<", "<<":
		return true
	default:
		return false
	}
}

func segmentEnd(tokens []string, start int) int {
	end := start
	for end < len(tokens) && !isShellBoundary(tokens[end]) {
		end++
	}
	return end
}

func segmentContains(tokens []string, start int, values ...string) bool {
	end := segmentEnd(tokens, start)
	for _, token := range tokens[start:end] {
		for _, value := range values {
			if token == value {
				return true
			}
		}
	}
	return false
}

func segmentHasFlag(tokens []string, start int, shortFlag rune, longFlag string) bool {
	end := segmentEnd(tokens, start)
	for _, token := range tokens[start:end] {
		if token == longFlag || strings.HasPrefix(token, longFlag+"=") {
			return true
		}
		if strings.HasPrefix(token, "-") && !strings.HasPrefix(token, "--") && strings.ContainsRune(token[1:], shortFlag) {
			return true
		}
	}
	return false
}

func segmentContainsSequence(tokens []string, start int, first, second string) bool {
	end := segmentEnd(tokens, start)
	firstSeen := false
	for _, token := range tokens[start:end] {
		if token == first {
			firstSeen = true
			continue
		}
		if firstSeen && token == second {
			return true
		}
	}
	return false
}

func segmentContainsForcedGitClean(tokens []string, start int) bool {
	end := segmentEnd(tokens, start)
	cleanSeen := false
	for _, token := range tokens[start:end] {
		if token == "clean" {
			cleanSeen = true
			continue
		}
		if cleanSeen && strings.HasPrefix(token, "-") && strings.Contains(token, "f") {
			return true
		}
	}
	return false
}

func beforeToolCallForPolicy(policy ExecutionPolicy, adapterHook func(agent.BeforeToolCallContext) *agent.ToolCallBlockResult) func(agent.BeforeToolCallContext) *agent.ToolCallBlockResult {
	if !policy.HasForcedMode() && adapterHook == nil {
		return nil
	}
	return func(ctx agent.BeforeToolCallContext) *agent.ToolCallBlockResult {
		args, _ := ctx.Args.(map[string]any)
		decision := policy.EvaluateToolCall(ctx.ToolCall.Name, args)
		if decision.Block {
			return &agent.ToolCallBlockResult{Block: true, Reason: decision.Reason}
		}
		if adapterHook != nil {
			return adapterHook(ctx)
		}
		return nil
	}
}
