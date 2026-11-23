package desktop

import (
	"github.com/oschina/mothx/native/internal/acp"
)

// Wire-type aliases keep the desktop composition shell readable while the
// canonical ACP v1 payloads live in the private internal/acp package.
type rpcError = acp.RPCError
type incoming = acp.Incoming
type acpClient = acp.Client

var newACPClient = acp.NewClient

type clientInfo = acp.ClientInfo
type initializeResult = acp.InitializeResult
type sessionMode = acp.SessionMode
type sessionModeState = acp.SessionModeState
type configChoice = acp.ConfigChoice
type configOption = acp.ConfigOption
type sessionResult = acp.SessionResult
type transcriptPageResult = acp.TranscriptPageResult
type executionState = acp.ExecutionState
type draftConfigResult = acp.DraftConfigResult
type listedSession = acp.ListedSession
type sessionMeta = acp.SessionMeta
type sessionLastRun = acp.SessionLastRun
type sessionListResult = acp.SessionListResult
type contentBlock = acp.ContentBlock
type promptResult = acp.PromptResult
type sessionUpdate = acp.SessionUpdate
type toolLocation = acp.ToolLocation
type usageCost = acp.UsageCost
type planEntry = acp.PlanEntry
type command = acp.Command
type sessionUpdateNotification = acp.SessionUpdateNotification
type sessionEvent = acp.SessionEvent
type permissionToolCall = acp.PermissionToolCall
type permissionOption = acp.PermissionOption
type permissionParams = acp.PermissionParams
type questionOption = acp.QuestionOption
type questionParams = acp.QuestionParams
type elicitationParams = acp.ElicitationParams
type applicationSettingsDefaults = acp.ApplicationSettingsDefaults
type applicationContextFilesView = acp.ApplicationContextFilesView
type applicationCompactionView = acp.ApplicationCompactionView
type applicationToolExecutionView = acp.ApplicationToolExecutionView
type applicationWebSearchView = acp.ApplicationWebSearchView
type applicationImageGenerationView = acp.ApplicationImageGenerationView
type applicationRetryView = acp.ApplicationRetryView
type applicationStatusLineView = acp.ApplicationStatusLineView
type applicationSandboxView = acp.ApplicationSandboxView
type applicationApprovalView = acp.ApplicationApprovalView
type applicationSettingsView = acp.ApplicationSettingsView
