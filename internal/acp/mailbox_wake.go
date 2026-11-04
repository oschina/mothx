package acp

import (
	"context"
	"log"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
)

// enableSessionMailboxWake attaches this ACP session to the Runtime-owned
// automatic continuation path. A worker completion can start a new lead Run
// only after the session is idle and the submission coordinator admits it; the
// ACP layer only projects the canonical Run and its events.
func (s *server) enableSessionMailboxWake(rt *sessionRuntime) {
	if s == nil || rt == nil || rt.runtime == nil || rt.runtime.Mailbox == nil {
		return
	}
	err := rt.runtime.EnableMailboxWake(context.Background(), agentruntime.MailboxWakeOptions{
		Build: agentruntime.AgentBuildOptions{
			Allow:        s.allow,
			MultiAgent:   s.multiAgent,
			DelegateMode: s.delegate,
			Workflows:    s.workflows,
		},
		OnStarted: func(run agentruntime.MailboxWakeRun) {
			s.projectMailboxWakeStarted(rt, run)
		},
		OnAgent: func(run agentruntime.MailboxWakeRun, a *agent.Agent) {
			rt.cancelMu.Lock()
			if rt.runID == run.RunID {
				rt.agent = agent.NewAgentAdapter(a)
			}
			rt.cancelMu.Unlock()
		},
		OnEvent: func(run agentruntime.MailboxWakeRun, event agent.Event) {
			public := agent.EventToPublic(event)
			s.handleAgentEvent(rt.id, public)
			if event.AgentID == "" && event.Type == agent.EventQuestionRequest {
				go s.handleQuestion(s.mailboxWakeContext(rt), rt, run.RunID, public)
			}
		},
		OnArtifact: func(run agentruntime.MailboxWakeRun, record agentruntime.SessionAttachment) {
			_ = s.notify(rt.id, artifactSessionUpdate(record.ID, record.Filename, string(record.Kind), record.MediaType, record.Bytes, run.RunID))
		},
		ConfigureBuild: func(build *agentruntime.AgentBuildOptions) {
			build.MultiAgent = s.multiAgent
			build.DelegateMode = s.delegate
			build.Workflows = s.workflows
			build.Allow = s.allow
			build.ApprovalHandler = func(toolCallID, toolName string, args map[string]any) bool {
				runID := s.sessionRunID(rt.id)
				if runID == "" || rt.execution == nil || rt.execution.WaitForApproval(runID) != nil {
					return false
				}
				defer func() { _ = rt.execution.Resume(runID) }()
				return s.requestPermissionContext(s.mailboxWakeContext(rt), rt.id, toolCallID, toolName, args)
			}
		},
		OnFinished: func(run agentruntime.MailboxWakeRun, state agentruntime.RunState, err error) {
			s.projectMailboxWakeFinished(rt, run, state, err)
		},
	})
	if err != nil {
		log.Printf("[acp] enable mailbox wake for %s: %v", rt.id, err)
	}
}

func (s *server) projectMailboxWakeStarted(rt *sessionRuntime, run agentruntime.MailboxWakeRun) {
	if rt == nil || rt.runtime == nil {
		return
	}
	_, _, model, _, thinking := rt.runtime.ConfigSnapshot()
	rt.cancelMu.Lock()
	if rt.mailboxWakeCancel != nil {
		rt.mailboxWakeCancel()
	}
	wakeCtx, wakeCancel := context.WithCancel(context.Background())
	rt.mailboxWakeCancel = wakeCancel
	rt.mailboxWakeCtx = wakeCtx
	rt.promptID = "wake_" + run.RunID
	rt.runID = run.RunID
	rt.streamSegment = 0
	rt.messageID = acpStreamMessageID(rt.id, rt.promptID, "message", 0)
	rt.thoughtMessageID = acpStreamMessageID(rt.id, rt.promptID, "thought", 0)
	rt.userMessageID = ""
	rt.activeModel = model
	rt.activeMode = run.Mode
	rt.activeThinking = thinking
	rt.terminalNotified = false
	rt.cancelMu.Unlock()
	s.notifyRunStatus(rt.id, run.RunID, "running")
}

func (s *server) projectMailboxWakeFinished(rt *sessionRuntime, run agentruntime.MailboxWakeRun, state agentruntime.RunState, err error) {
	if rt == nil {
		return
	}
	if rt.decisions != nil {
		for _, request := range rt.decisions.ClearRunWithValue(run.RunID, "") {
			status := decisionTerminalStatusACP(state)
			s.persistDecisionRecord(rt.id, run.RunID, request.ID, request.Kind, status, "", map[string]any{"reason": "automatic continuation ended"})
		}
	}
	rt.cancelMu.Lock()
	if rt.mailboxWakeCancel != nil {
		rt.mailboxWakeCancel()
		rt.mailboxWakeCancel = nil
	}
	rt.mailboxWakeCtx = nil
	if rt.runID == run.RunID {
		rt.promptID = ""
		rt.runID = ""
		rt.messageID = ""
		rt.thoughtMessageID = ""
		rt.userMessageID = ""
		rt.activeModel = nil
		rt.activeMode = ""
		rt.activeThinking = ""
	}
	rt.cancelMu.Unlock()
	s.notifyRunStatus(rt.id, run.RunID, acpRunStatus(string(state)))
	if err != nil {
		log.Printf("[acp] automatic continuation for %s ended %s: %v", rt.id, state, err)
	}
}

func (s *server) mailboxWakeContext(rt *sessionRuntime) context.Context {
	if rt == nil {
		return context.Background()
	}
	rt.cancelMu.Lock()
	defer rt.cancelMu.Unlock()
	if rt.mailboxWakeCtx == nil {
		return context.Background()
	}
	return rt.mailboxWakeCtx
}

func decisionTerminalStatusACP(state agentruntime.RunState) string {
	switch state {
	case agentruntime.RunStateCancelled, agentruntime.RunStateCancelling:
		return "cancelled"
	default:
		return "timed_out"
	}
}
