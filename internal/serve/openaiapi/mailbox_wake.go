package openaiapi

import (
	"context"
	"log"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
)

// configureSessionRuntime projects the server's provider/model into the shared
// Runtime so a Runtime-owned continuation has real request configuration. A
// foreground Run still supplies its per-request values explicitly.
func (s *Server) configureSessionRuntime(sess *APISession) {
	if s == nil || sess == nil || sess.Runtime == nil || s.provider == nil || s.model == nil {
		return
	}
	mode := sess.Runtime.Mode
	if mode == "" && s.cfg != nil {
		mode = s.cfg.DefaultMode
	}
	if mode == "" {
		mode = agentruntime.ModeYolo
	}
	thinking := provider.ThinkingLevel("")
	if s.cfg != nil {
		thinking = provider.ThinkingLevel(s.cfg.DefaultThinkingLevel)
	}
	if thinking == "" && s.settings != nil {
		thinking = provider.ThinkingLevel(s.settings.DefaultThinkingLevel)
	}
	if err := sess.Runtime.ConfigureSession(s.provider, s.providerName, s.model, mode, thinking); err != nil {
		log.Printf("[serve] configure session runtime for %s: %v", sess.ID, err)
	}
}

// enableSessionMailboxWake attaches a Serve/WebUI session to the Runtime-owned
// automatic continuation path. A worker finding or question can therefore wake
// an idle lead into a canonical Run; this file only projects Runtime events and
// owns no scheduler, mailbox, or run state machine of its own.
func (s *Server) enableSessionMailboxWake(sess *APISession) {
	if s == nil || sess == nil || sess.Runtime == nil || sess.Runtime.Mailbox == nil {
		return
	}
	if s.settings == nil {
		return
	}
	if p, _, model, _, _ := sess.Runtime.ConfigSnapshot(); p == nil || model == nil {
		// No session provider/model means a Runtime continuation could never run.
		// Leave the scheduler off instead of retrying a permanently invalid build.
		return
	}
	// Durable continuation needs the same persistence hooks as a foreground
	// Run, but the Runtime keeps ownership of begin/observe/finish.
	execution := sess.ensureExecution()
	execution.SetRunStore(agentruntime.RunStore{SessionDir: s.settings.GetSessionDir()})
	execution.SetEventSink(s.runtimeRunEventSink(sess))
	sess.Runtime.SetExecution(execution)

	err := sess.Runtime.EnableMailboxWake(context.Background(), agentruntime.MailboxWakeOptions{
		Build: agentruntime.AgentBuildOptions{
			Allow:        s.getAllow(),
			MultiAgent:   sess.MultiAgent,
			DelegateMode: sess.DelegateMode,
			Workflows:    sess.Workflows,
		},
		ConfigureBuild: func(build *agentruntime.AgentBuildOptions) {
			build.MultiAgent = sess.MultiAgent
			build.DelegateMode = sess.DelegateMode
			build.Workflows = sess.Workflows
			build.Allow = s.getAllow()
		},
		OnStarted: func(run agentruntime.MailboxWakeRun) {
			sess.beginRunBookkeeping(run.RunID)
			s.publishSessionRuntime(sess)
		},
		OnAgent: func(run agentruntime.MailboxWakeRun, a *agent.Agent) {
			sess.approvalMu.Lock()
			if sess.activeRunID == run.RunID {
				sess.activeRunAgent = a
			}
			sess.approvalMu.Unlock()
			if execution := sess.executionRuntime(); execution != nil {
				execution.SetAgent(a)
			}
		},
		OnEvent: func(run agentruntime.MailboxWakeRun, ev agent.Event) {
			s.projectMailboxWakeEvent(sess, run, ev)
		},
		OnFinished: func(run agentruntime.MailboxWakeRun, state agentruntime.RunState, runErr error) {
			s.finishMailboxWakeRun(sess, run, state, runErr)
		},
	})
	if err != nil {
		log.Printf("[serve] enable mailbox wake for %s: %v", sess.ID, err)
	}
}

// projectMailboxWakeEvent maps canonical Agent events onto the existing WebUI
// SSE projections. Persistence and run semantics stay in the Runtime.
func (s *Server) projectMailboxWakeEvent(sess *APISession, run agentruntime.MailboxWakeRun, ev agent.Event) {
	if sess == nil {
		return
	}
	switch ev.Type {
	case agent.EventHostedItem:
		if ev.HostedItem != nil {
			s.publishTranscriptEvent(sess.ID, TranscriptStreamEvent{Type: "hosted_item", HostedItem: hostedItemEvent(ev.HostedItem)})
		}
	case agent.EventTextDelta:
		s.publishTranscriptEvent(sess.ID, assistantDeltaTranscriptEvent(ev.TextDelta, ev.AgentID, ev))
	case agent.EventToolCall:
		name, callID := resolveToolEvent(ev)
		s.publishToolEvent(sess.ID, ToolStatusEvent{
			Tool: name, ToolCallID: callID, AgentID: string(ev.AgentID),
			Status: "running", Args: ev.ToolArgs,
		})
	case agent.EventToolExecutionEnd:
		status := "completed"
		if ev.ToolError != nil {
			status = "failed"
		}
		name := ev.ToolName
		if name == "" {
			name, _ = resolveToolEvent(ev)
		}
		s.publishToolEvent(sess.ID, ToolStatusEvent{
			Tool: name, ToolCallID: ev.ToolCallID, AgentID: string(ev.AgentID),
			Status: status, Args: ev.ToolArgs,
			Summary: toolStatusSummary(ev.ToolResult, ev.ToolError),
			IsError: ev.ToolError != nil, HasDetail: ev.ToolCallID != "",
		})
	case agent.EventToolApprovalRequest:
		a := sess.activeWakeAgent(run.RunID)
		if a == nil {
			return
		}
		if execution := sess.executionRuntime(); execution != nil {
			_ = execution.WaitForApproval(run.RunID)
		}
		s.registerSessionApproval(sess, a, ev)
	case agent.EventQuestionRequest:
		a := sess.activeWakeAgent(run.RunID)
		if a == nil {
			return
		}
		s.registerSessionQuestion(sess, a, run.RunID, ev)
	case agent.EventRunFinished:
		if ev.AgentID == "" && len(ev.Attachments) > 0 {
			s.publishTranscriptEvent(sess.ID, assistantAttachmentsTranscriptEvent(ev.Attachments, ev.AgentID))
		}
	}
}

func (s *Server) finishMailboxWakeRun(sess *APISession, run agentruntime.MailboxWakeRun, state agentruntime.RunState, runErr error) {
	if sess == nil {
		return
	}
	status := mailboxWakeRunStatus(state)
	s.clearSessionApprovalsForRun(sess, run.RunID, status, "automatic continuation ended")
	sess.finishRun(run.RunID)
	s.publishSessionRuntime(sess)
	s.publishSessionStreamDone(sess.ID, run.RunID, status)
	if runErr != nil {
		log.Printf("[serve] automatic continuation for %s ended %s: %v", sess.ID, state, runErr)
	}
}

func mailboxWakeRunStatus(state agentruntime.RunState) string {
	if state == agentruntime.RunStateCancelled {
		return "canceled"
	}
	return string(state)
}

// activeWakeAgent returns the Agent that owns this Runtime continuation so
// approval and question payloads can resolve through the same protocol hook a
// foreground run uses.
func (sess *APISession) activeWakeAgent(runID string) *agent.Agent {
	if sess == nil || runID == "" {
		return nil
	}
	sess.approvalMu.Lock()
	defer sess.approvalMu.Unlock()
	if sess.activeRunID != runID || sess.activeRunStatus != "running" {
		return nil
	}
	return sess.activeRunAgent
}
