package tui

import (
	"strings"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
)

// resetTranscriptState clears rendered conversation bookkeeping without
// changing session/provider configuration.
func (a *App) resetTranscriptState() {
	a.invalidateToolModalCache()
	a.messages = nil
	a.hiddenEventIdx = make(map[int]bool)
	a.toolResults = nil
	a.liveContent = ""
	a.currentPlan = nil
	a.assistantRaw = make(map[int]string)
	a.assistantBuilders = make(map[int]*strings.Builder)
	a.assistantRendered = make(map[int]string)
	a.assistantDirty = make(map[int]bool)
	a.thinkRaw = make(map[int]string)
	a.thinkBuilders = make(map[int]*strings.Builder)
	a.printedMessageIdx = make(map[int]bool)
	a.agentActivities = make(map[agentpkg.AgentID]*agentActivity)
	a.agentActivityOrder = nil
	a.currentAssistantIdx = -1
	a.currentThinkIdx = -1
	a.currentApprovalIdx = -1
	a.closeToolModal()
	a.closeESMPanel()
}

func (a *App) clearQueuedInput() {
	a.inputQueueMu.Lock()
	a.inputQueue = a.inputQueue[:0]
	a.lastInputTime = time.Time{}
	a.inputQueueMu.Unlock()
}
