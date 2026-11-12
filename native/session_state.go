package main

import "github.com/egoist/mygo/ui"

// sessionSnapshot is the per-session shell state the background run path keeps
// while another session is visible. It is a projection cache only: transcript
// content, configuration, usage, and run status remain Runtime-owned; this
// struct lets the UI keep them when the user switches tasks.
type sessionSnapshot struct {
	ID             string
	LoadingSession string

	Messages             []*chatMessage
	MessageIndex         map[string]*chatMessage
	PlanMessageID        string
	StatusMessageID      string
	LocalMessageSeq      int
	TranscriptNextCursor string
	TranscriptLoading    bool
	Running              bool
	Status               string
	AwaitingUserEcho     bool
	LocalUserMessageID   string
	UsageUsed            int
	UsageSize            int
	UsageCost            float64
	UsageCurrency        string
	UsageCache           *usageCacheProjection
	ConfigOptions        []configOption
	ModelLabel           string
	ModelValue           string
	ThinkingLabel        string
	ThinkingValue        string
	Mode                 string
	AvailableModes       []sessionMode
	Commands             []command
	List                 ui.ListState
}

func (a *nativeApp) snapshotSession() sessionSnapshot {
	return sessionSnapshot{
		ID:                   a.currentSession,
		Messages:             a.messages,
		MessageIndex:         a.messageIndex,
		PlanMessageID:        a.planMessageID,
		StatusMessageID:      a.statusMessageID,
		LocalMessageSeq:      a.localMessageSeq,
		TranscriptNextCursor: a.transcriptNextCursor,
		TranscriptLoading:    a.transcriptLoading,
		Running:              a.running,
		Status:               a.status,
		AwaitingUserEcho:     a.awaitingUserEcho,
		LocalUserMessageID:   a.localUserMessageID,
		UsageUsed:            a.usageUsed,
		UsageSize:            a.usageSize,
		UsageCost:            a.usageCost,
		UsageCurrency:        a.usageCurrency,
		UsageCache:           a.usageCache,
		ConfigOptions:        a.configOptions,
		ModelLabel:           a.modelLabel,
		ModelValue:           a.modelValue,
		ThinkingLabel:        a.thinkingLabel,
		ThinkingValue:        a.thinkingValue,
		Mode:                 a.mode,
		AvailableModes:       a.availableModes,
		Commands:             a.commands,
		List:                 a.list,
	}
}

func (a *nativeApp) restoreSession(s sessionSnapshot) {
	a.currentSession = s.ID
	a.loadingSession = s.LoadingSession
	a.messages = s.Messages
	a.messageIndex = s.MessageIndex
	if a.messageIndex == nil {
		a.messageIndex = make(map[string]*chatMessage)
	}
	a.planMessageID = s.PlanMessageID
	a.statusMessageID = s.StatusMessageID
	a.localMessageSeq = s.LocalMessageSeq
	a.transcriptNextCursor = s.TranscriptNextCursor
	a.transcriptLoading = s.TranscriptLoading
	a.running = s.Running
	a.status = s.Status
	a.awaitingUserEcho = s.AwaitingUserEcho
	a.localUserMessageID = s.LocalUserMessageID
	a.usageUsed = s.UsageUsed
	a.usageSize = s.UsageSize
	a.usageCost = s.UsageCost
	a.usageCurrency = s.UsageCurrency
	a.usageCache = s.UsageCache
	a.configOptions = s.ConfigOptions
	a.modelLabel = s.ModelLabel
	a.modelValue = s.ModelValue
	a.thinkingLabel = s.ThinkingLabel
	a.thinkingValue = s.ThinkingValue
	a.mode = s.Mode
	a.availableModes = s.AvailableModes
	a.commands = s.Commands
	a.list = s.List
}

func (a *nativeApp) clearSessionState(id string) {
	a.currentSession = id
	a.loadingSession = ""
	a.messages = nil
	a.messageIndex = make(map[string]*chatMessage)
	a.planMessageID = ""
	a.statusMessageID = ""
	a.localMessageSeq = 0
	a.transcriptNextCursor = ""
	a.transcriptLoading = false
	a.running = false
	a.status = ""
	a.awaitingUserEcho = false
	a.localUserMessageID = ""
	a.usageUsed = 0
	a.usageSize = 0
	a.usageCost = 0
	a.usageCurrency = ""
	a.usageCache = nil
	a.configOptions = nil
	a.modelLabel = ""
	a.modelValue = ""
	a.thinkingLabel = ""
	a.thinkingValue = ""
	a.mode = ""
	a.availableModes = nil
	a.commands = nil
	a.list = ui.ListState{FollowEnd: true, FollowAnimation: &ui.Smooth}
}

// saveActiveSession stores the visible task before switching away. A draft
// shell (no current session) has no canonical row to cache.
func (a *nativeApp) saveActiveSession() {
	if a.currentSession == "" || a.sessionStates == nil {
		return
	}
	a.sessionStates[a.currentSession] = a.snapshotSession()
}

// selectSessionState swaps the visible state to one task. It keeps cached
// messages and run status so switching back is immediate; the following
// session/load reconciles the cache with the Runtime.
func (a *nativeApp) selectSessionState(id string) {
	a.saveActiveSession()
	if state, ok := a.sessionStates[id]; ok {
		a.restoreSession(state)
		a.list = ui.ListState{FollowEnd: true, FollowAnimation: &ui.Smooth}
		return
	}
	a.clearSessionState(id)
}

// withSessionState applies a background session event to that session's cache
// without disturbing the visible task. The active snapshot is restored even
// when the reducer changes title/config/run fields.
func (a *nativeApp) withSessionState(id string, fn func()) {
	if id == "" || id == a.currentSession || id == a.loadingSession {
		fn()
		return
	}
	saved := a.snapshotSession()
	if state, ok := a.sessionStates[id]; ok {
		a.restoreSession(state)
	} else {
		a.clearSessionState(id)
	}
	a.currentSession = id
	a.loadingSession = ""
	a.backgroundRouting = true
	fn()
	a.backgroundRouting = false
	a.sessionStates[id] = a.snapshotSession()
	a.restoreSession(saved)
}

// setSessionRunLocal updates the sidebar/history lastRun projection from a live
// event. It never invents a terminal state: the event's status is projected as
// given, with Active only for non-terminal run phases.
func (a *nativeApp) setSessionRunLocal(sessionID, status string, active bool) {
	if sessionID == "" {
		return
	}
	update := func(sessions []listedSession) {
		for i := range sessions {
			if sessions[i].SessionID != sessionID {
				continue
			}
			if sessions[i].Meta.LastRun == nil {
				sessions[i].Meta.LastRun = &sessionLastRun{}
			}
			sessions[i].Meta.LastRun.Active = active
			sessions[i].Meta.LastRun.Status = status
			if !active {
				// A terminal run_status invalidates the full ownership snapshot;
				// the next list refresh restores any still-busy transition state.
				sessions[i].Meta.Execution = nil
			}
		}
	}
	update(a.sessions)
	update(a.historyPanel.Sessions)
}
