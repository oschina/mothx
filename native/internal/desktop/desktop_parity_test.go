package desktop

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// These regressions cover the Desktop-parity round: canonical subagent
// lifecycle events, plan title/note meta, the collapsed live-thought preview,
// bounded tool projections, the app-wide background scope, the quick-start
// scratch directory plus picker history, and the runtime binary rollback.

func subagentEvent(sessionID, agentID, status string) sessionEvent {
	return sessionEvent{
		SessionID:         sessionID,
		Event:             "subagent",
		AgentID:           agentID,
		Status:            status,
		MemberID:          "member-1",
		MemberDisplayName: "Critic",
		MemberEmoji:       "🧐",
		MemberRole:        "critic",
		ExpertID:          "team-1",
	}
}

func TestSubagentEventProjectsInlineCardAndRoster(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.handleSessionEvent(subagentEvent("s1", "child-1", "started"))

	if len(a.messages) != 1 || a.messages[0].Kind != messageSubagent {
		t.Fatalf("inline subagent card missing: %+v", a.messages)
	}
	m := a.messages[0]
	if m.SubagentID != "child-1" || m.SubagentStatus != "running" || m.Done {
		t.Fatalf("running card = %+v", m)
	}
	if m.SubagentTitle != "🧐 Critic" || m.SubagentRole != "critic" || m.SubagentExpertID != "team-1" {
		t.Fatalf("card identity = %+v", m)
	}
	total, running, failed := a.subagentSummary("s1")
	if total != 1 || running != 1 || failed != 0 {
		t.Fatalf("summary = %d/%d/%d", total, running, failed)
	}

	// A second event for the same child upserts one card, not two.
	a.handleSessionEvent(subagentEvent("s1", "child-1", "done"))
	if len(a.messages) != 1 {
		t.Fatalf("card duplicated: %d messages", len(a.messages))
	}
	m = a.messages[0]
	if m.SubagentStatus != "completed" || !m.Done {
		t.Fatalf("terminal card = %+v", m)
	}
	total, running, _ = a.subagentSummary("s1")
	if total != 1 || running != 0 {
		t.Fatalf("terminal summary = %d/%d", total, running)
	}
}

func TestSubagentStatusNormalization(t *testing.T) {
	cases := map[string]string{
		"started": "running", "ready": "running", "running": "running", "": "running",
		"done": "completed", "completed": "completed",
		"error": "failed", "failed": "failed",
		"canceled": "cancelled", "cancelled": "cancelled",
		"incomplete": "incomplete",
	}
	for status, want := range cases {
		if got := subagentStatusKind(status); got != want {
			t.Errorf("subagentStatusKind(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestSubagentEventRoutesToBackgroundSession(t *testing.T) {
	a := testApp()
	a.currentSession = "visible"
	a.appendMessage(&chatMessage{ID: "visible-1", Kind: messageAssistant, Text: "hello", Done: true})

	a.handleSessionEvent(subagentEvent("background", "child-9", "started"))

	if len(a.messages) != 1 || a.messages[0].ID != "visible-1" {
		t.Fatalf("background event leaked into the visible transcript: %+v", a.messages)
	}
	snapshot, ok := a.sessionStates["background"]
	if !ok || len(snapshot.Messages) != 1 || snapshot.Messages[0].Kind != messageSubagent {
		t.Fatalf("background snapshot = %+v", snapshot)
	}
	if total, _, _ := a.subagentSummary("background"); total != 1 {
		t.Fatalf("background roster total = %d", total)
	}
	if total, _, _ := a.subagentSummary("visible"); total != 0 {
		t.Fatalf("visible roster must stay empty, got %d", total)
	}
}

func TestSubagentSummaryCountsFailedChildren(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.handleSessionEvent(subagentEvent("s1", "c1", "started"))
	a.handleSessionEvent(subagentEvent("s1", "c2", "error"))
	total, running, failed := a.subagentSummary("s1")
	if total != 2 || running != 1 || failed != 1 {
		t.Fatalf("summary = %d/%d/%d", total, running, failed)
	}
}

func TestSubagentRosterCapKeepsRunningAndNewestTerminal(t *testing.T) {
	roster := make([]subagentInfoView, 0, maxSubagentsPerSession+50)
	for i := 0; i < maxSubagentsPerSession+50; i++ {
		status := "completed"
		if i >= 100 {
			status = "running"
		}
		roster = append(roster, subagentInfoView{AgentID: fmt.Sprintf("c%d", i), Status: status})
	}
	capped := capSubagentRoster(roster)
	if len(capped) != maxSubagentsPerSession {
		t.Fatalf("capped length = %d", len(capped))
	}
	running := 0
	for _, info := range capped {
		if subagentStatusKind(info.Status) == "running" {
			running++
		}
	}
	if running != maxSubagentsPerSession-50 {
		t.Fatalf("running kept = %d", running)
	}
	// Running children lead; the newest terminal children fill the remainder
	// and the oldest terminal projection is dropped.
	if capped[0].AgentID != "c100" {
		t.Fatalf("first kept agent = %s", capped[0].AgentID)
	}
	last := capped[len(capped)-1]
	if last.AgentID != "c99" {
		t.Fatalf("last kept agent = %s", last.AgentID)
	}
	for _, info := range capped {
		if info.AgentID == "c0" {
			t.Fatal("oldest terminal child must be dropped")
		}
	}
}

func TestSubagentEventSyncsOpenPanel(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.subagent.Open = true
	a.subagent.SessionID = "s1"
	a.subagent.Agents = []subagentInfoView{{AgentID: "child-1", Status: "running", MessageCount: 4}}

	a.handleSessionEvent(subagentEvent("s1", "child-1", "completed"))

	if len(a.subagent.Agents) != 1 {
		t.Fatalf("panel agents = %+v", a.subagent.Agents)
	}
	info := a.subagent.Agents[0]
	if info.Status != "completed" || info.Active || info.MessageCount != 4 {
		t.Fatalf("panel merge lost server fields: %+v", info)
	}
}

func TestRefreshSubagentRosterRequiresCapabilityAndClient(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	// No feature and no client: the refresh must be a silent no-op.
	a.refreshSubagentRoster("s1")
	if len(a.subagentRosterSeq) != 0 {
		t.Fatalf("roster refresh started without capability: %+v", a.subagentRosterSeq)
	}
}

func TestPlanMetaProjectsTitleAndNote(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "plan",
		Entries:       []planEntry{{Content: "step", Status: "in_progress"}},
		Meta: map[string]json.RawMessage{
			"mothx.dev": json.RawMessage(`{"title":"发布计划","note":"先测试后发布"}`),
		},
	}})
	plan := a.activePlanMessage()
	if plan == nil || plan.PlanTitle != "发布计划" || plan.PlanNote != "先测试后发布" {
		t.Fatalf("plan meta = %+v", plan)
	}

	// An absent annotation keeps the generic labels instead of inventing one.
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "plan",
		Entries:       []planEntry{{Content: "step", Status: "completed"}},
	}})
	plan = a.activePlanMessage()
	if plan == nil || plan.PlanTitle != "" || plan.PlanNote != "" {
		t.Fatalf("plain plan must not keep stale meta: %+v", plan)
	}
}

func TestPlanPanelRendersRuntimeTitle(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "plan",
		Entries:       []planEntry{{Content: "step one", Status: "in_progress"}},
		Meta: map[string]json.RawMessage{
			"mothx.dev": json.RawMessage(`{"title":"Release plan","note":"test first"}`),
		},
	}})
	tt := ui.NewTester(a.view, 1180, 760)
	if !textPresent(tt, "Release plan") || !textPresent(tt, "test first") {
		t.Fatalf("plan panel missing runtime title/note: %q", tt.Texts())
	}
}

func TestThoughtPreviewIsBoundedTail(t *testing.T) {
	long := strings.Repeat("a", 500) + "  latest   tail \n"
	preview := thoughtPreview(long)
	if !strings.HasSuffix(preview, "latest tail") {
		t.Fatalf("preview tail = %q", preview)
	}
	if len([]rune(preview)) > 600 {
		t.Fatalf("preview length = %d", len([]rune(preview)))
	}
	if strings.ContainsAny(preview, "\n\t") {
		t.Fatalf("preview must be compacted to one line: %q", preview)
	}
}

func TestCollapsedStreamingThoughtShowsLivePreview(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "agent_thought_chunk",
		MessageID:     "t1",
		Content:       json.RawMessage(`{"type":"text","text":"considering the latest edge case"}`),
	}})
	m := a.messageByID("thought:t1")
	if m == nil || !m.Collapsed || m.Done {
		t.Fatalf("streaming thought = %+v", m)
	}
	tt := ui.NewTester(a.view, 1180, 760)
	if !textPresent(tt, "considering the latest edge case") {
		t.Fatalf("collapsed live thought must preview its tail: %q", tt.Texts())
	}
}

func TestToolProjectionIsBoundedAtIngestion(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	longText := strings.Repeat("x", maxToolTextChars+5000)
	longDiff := strings.Repeat("y", maxToolDiffChars+5000)
	content, _ := json.Marshal([]map[string]any{
		{"type": "content", "content": map[string]any{"type": "text", "text": longText}},
		{"type": "diff", "path": "/tmp/f", "oldText": longDiff, "newText": longDiff},
	})
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "tool_call_update",
		ToolCallID:    "tool-1",
		Status:        "completed",
		Content:       content,
	}})
	m := a.messageByID("tool:tool-1")
	if m == nil {
		t.Fatal("tool card missing")
	}
	if len([]rune(m.ToolText)) > maxToolTextChars+2 {
		t.Fatalf("tool text not bounded: %d runes", len([]rune(m.ToolText)))
	}
	if len(m.ToolDiffs) != 1 {
		t.Fatalf("diffs = %+v", m.ToolDiffs)
	}
	for _, diff := range m.ToolDiffs {
		if len([]rune(diff.OldText)) > maxToolDiffChars+2 || len([]rune(diff.NewText)) > maxToolDiffChars+2 {
			t.Fatalf("diff not bounded: %d/%d runes", len([]rune(diff.OldText)), len([]rune(diff.NewText)))
		}
	}
}

func TestNativePreferencesRoundTripScopeAndWorkspaceHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), nativePreferencesFile)
	a := testApp()
	a.prefsPath = path
	a.home.backgroundScope = "home"
	a.recordWorkspace("/work/one")
	a.recordWorkspace("/work/two")
	a.recordWorkspace("/work/one")
	a.saveNativePreferences()

	prefs, ok := loadNativePreferences(path)
	if !ok {
		t.Fatal("preferences not saved")
	}
	if prefs.HomeBackgroundScope != "home" || prefs.LastWorkspace != "/work/one" {
		t.Fatalf("prefs = %+v", prefs)
	}
	if len(prefs.RecentWorkspaces) != 2 || prefs.RecentWorkspaces[0] != "/work/one" || prefs.RecentWorkspaces[1] != "/work/two" {
		t.Fatalf("recent workspaces = %+v", prefs.RecentWorkspaces)
	}

	restored := testApp()
	restored.prefsPath = path
	restored.applyNativePreferences(prefs)
	if restored.home.backgroundScope != "home" || restored.lastWorkspace != "/work/one" || len(restored.recentWorkspaces) != 2 {
		t.Fatalf("restored = scope %q last %q recent %+v", restored.home.backgroundScope, restored.lastWorkspace, restored.recentWorkspaces)
	}
}

func TestRecordWorkspaceCapsHistory(t *testing.T) {
	a := testApp()
	a.prefsPath = filepath.Join(t.TempDir(), nativePreferencesFile)
	for i := 0; i < maxRecentWorkspaces+5; i++ {
		a.recordWorkspace(fmt.Sprintf("/work/%d", i))
	}
	if len(a.recentWorkspaces) != maxRecentWorkspaces {
		t.Fatalf("history length = %d", len(a.recentWorkspaces))
	}
	if a.recentWorkspaces[0] != fmt.Sprintf("/work/%d", maxRecentWorkspaces+4) {
		t.Fatalf("newest directory must lead: %+v", a.recentWorkspaces)
	}
}

func testBackgroundImage(t *testing.T) ui.ImageSource {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(onePixelPNG)
	if err != nil {
		t.Fatal(err)
	}
	image, err := ui.DecodeImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	return image
}

func TestBackgroundScopeDefaultsToAppAndGatesRendering(t *testing.T) {
	a := testApp()
	if a.home.backgroundScope != "app" {
		t.Fatalf("default scope = %q", a.home.backgroundScope)
	}
	if a.appBackgroundActive() {
		t.Fatal("no image must not activate the app background")
	}
	a.home.backgroundImage = testBackgroundImage(t)
	if !a.appBackgroundActive() {
		t.Fatal("default scope with image must paint the whole app")
	}
	a.home.backgroundScope = "home"
	if a.appBackgroundActive() {
		t.Fatal("home scope must not paint the whole app")
	}
}

func TestAppBackgroundRendersHeadless(t *testing.T) {
	image := testBackgroundImage(t)
	for _, scope := range []string{"app", "home"} {
		a := testApp()
		a.home.backgroundImage = image
		a.home.backgroundScope = scope
		a.home.backgroundOpacity = 0.4
		a.home.backgroundBlur = 2
		tt := ui.NewTester(a.view, 1180, 760)
		if !textPresent(tt, "MothX") {
			t.Fatalf("scope %s broke the shell render: %q", scope, tt.Texts())
		}
	}
}

func TestQuickStartDirectoryIsPrivateScratch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := createQuickStartDirectory()
	if dir == "" {
		t.Fatal("quick-start directory not created")
	}
	if filepath.Dir(dir) != filepath.Join(home, "MothX") {
		t.Fatalf("scratch parent = %s", filepath.Dir(dir))
	}
	if !strings.HasPrefix(filepath.Base(dir), "mothx_projects_") {
		t.Fatalf("scratch name = %s", filepath.Base(dir))
	}
	if strings.Contains(filepath.Base(dir), ":") {
		t.Fatalf("scratch name must stay Windows-safe: %s", filepath.Base(dir))
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("scratch dir invalid: %v", err)
	}

	a := testApp()
	a.initQuickStartWorkspace()
	if a.workspace == "" || a.draftCwd == "" || a.cwd == "" {
		t.Fatalf("quick-start left empty directories: %+v", a)
	}
	if filepath.Dir(a.workspace) != filepath.Join(home, "MothX") {
		t.Fatalf("quick-start workspace = %q", a.workspace)
	}
	if a.workspace != a.draftCwd || a.workspace != a.cwd {
		t.Fatalf("quick-start must align child cwd and draft: %q %q %q", a.cwd, a.workspace, a.draftCwd)
	}
}

func TestRuntimeBinaryRollbackRestoresPreviousPreference(t *testing.T) {
	setRuntimeBinaryPreference("/opt/previous-mothx", false)
	t.Cleanup(func() { setRuntimeBinaryPreference("", false) })

	a := testApp()
	a.closing.Store(true) // the rollback retry must not spawn a real child
	a.binaryRollbackPrevious = getRuntimeBinaryPreference()
	a.binaryRollbackPending = true
	setRuntimeBinaryPreference("/opt/broken-mothx", false)

	a.rollbackRuntimeBinaryIfPending()

	if getRuntimeBinaryPreference() != "/opt/previous-mothx" {
		t.Fatalf("preference after rollback = %q", getRuntimeBinaryPreference())
	}
	if a.binaryRollbackPending || a.binaryRollbackPrevious != "" {
		t.Fatalf("rollback state not cleared: %+v", a)
	}
	if len(a.toasts) == 0 || !a.toasts[0].Error {
		t.Fatalf("rollback must surface an error toast: %+v", a.toasts)
	}

	// A second failure surfaces normally instead of looping.
	a.rollbackRuntimeBinaryIfPending()
	if getRuntimeBinaryPreference() != "/opt/previous-mothx" {
		t.Fatalf("second rollback changed the preference: %q", getRuntimeBinaryPreference())
	}
}

func TestSubagentSummaryButtonProjectsLiveCounts(t *testing.T) {
	SetLocale("en")
	t.Cleanup(func() { SetLocale("zh") })
	a := testApp()
	SetLocale("en")
	a.currentSession = "s1"
	a.setFeatures(map[string]bool{"subagentPanel": true})
	a.handleSessionEvent(subagentEvent("s1", "c1", "started"))
	a.handleSessionEvent(subagentEvent("s1", "c2", "failed"))

	tt := ui.NewTester(a.view, 1180, 760)
	if !textPresent(tt, "Sub-agents 1/2 running") {
		t.Fatalf("header summary missing live counts: %q", tt.Texts())
	}
}

func TestSubagentCardAndSummaryTranslate(t *testing.T) {
	SetLocale("en")
	t.Cleanup(func() { SetLocale("zh") })
	if got := T("整个应用"); got != "Whole app" {
		t.Fatalf("scope label = %q", got)
	}
	if got := Tf("子代理 %d/%d 运行中", 1, 2); got != "Sub-agents 1/2 running" {
		t.Fatalf("summary format = %q", got)
	}
	if got := Tf("子代理 %d", 3); got != "Sub-agents 3" {
		t.Fatalf("total format = %q", got)
	}
	if got := T("所选运行时无法启动，已回退到之前的二进制"); !strings.Contains(got, "rolled back") {
		t.Fatalf("rollback toast = %q", got)
	}
}

func TestTranscriptExportIncludesSubagentAndPlanMeta(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.handleSessionEvent(subagentEvent("s1", "c1", "completed"))
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "plan",
		Entries:       []planEntry{{Content: "ship it", Status: "completed"}},
		Meta: map[string]json.RawMessage{
			"mothx.dev": json.RawMessage(`{"title":"Release","note":"carefully"}`),
		},
	}})
	text := a.transcriptPlainText()
	if !strings.Contains(text, "Sub-agent: 🧐 Critic — completed") {
		t.Fatalf("export missing subagent card:\n%s", text)
	}
	if !strings.Contains(text, "## Plan: Release") || !strings.Contains(text, "carefully") {
		t.Fatalf("export missing plan meta:\n%s", text)
	}
}

// onePixelPNG is a 1x1 transparent PNG used by the background render tests.
const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

// solidImage encodes a flat color so paint-order regressions can probe exact
// pixels: a white background makes a missing veil or an image painted above
// the shell unmistakable.
func solidImage(t *testing.T, c color.RGBA) ui.ImageSource {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	source, err := ui.DecodeImage(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func assertPixelNear(t *testing.T, img *image.RGBA, x, y int, want [3]uint8, tol uint8) {
	t.Helper()
	got := img.RGBAAt(x, y)
	for i, v := range []uint8{got.R, got.G, got.B} {
		diff := int(v) - int(want[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > int(tol) {
			t.Fatalf("pixel (%d,%d) = (%d,%d,%d), want ~(%d,%d,%d)", x, y, got.R, got.G, got.B, want[0], want[1], want[2])
		}
	}
}

// TestBackgroundLayersPaintUnderShell pins the acceptance result: the user
// image and its veil paint below the shell (negative ZIndex), never above it.
// MyGo paints absolute children over flow siblings by default, so a missing
// ZIndex would hide the whole UI behind the image.
func TestBackgroundLayersPaintUnderShell(t *testing.T) {
	white := solidImage(t, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	// Dark theme background #191a1f under a 0.66 veil over white.
	veilWant := [3]uint8{103, 104, 107}
	// User bubble #2a2c34 at 0.82 over that veil.
	bubbleWant := [3]uint8{53, 55, 62}

	a := testApp()
	a.themeMode = "dark"
	a.themeSet = false
	a.home.backgroundImage = white
	a.home.backgroundScope = "app"
	a.home.backgroundOpacity = 1
	a.currentSession = "s1"
	a.messages = []*chatMessage{{ID: "u1", Kind: messageUser, Text: "veil probe", Done: true}}
	a.messageIndex = map[string]*chatMessage{"u1": a.messages[0]}
	tt := ui.NewTester(a.view, 1180, 760)
	assertPixelNear(t, tt.Image(), 600, 300, veilWant, 8)
	label, ok := tt.Find("veil probe")
	if !ok {
		t.Fatalf("user bubble text missing: %q", tt.Texts())
	}
	// Sample inside the bubble padding, left of the glyphs themselves.
	assertPixelNear(t, tt.Image(), int(label.X)-6, int(label.Y+label.H/2), bubbleWant, 8)

	// Home scope veils only the home region, still below its content.
	a.home.backgroundScope = "home"
	a.currentSession = ""
	a.messages = nil
	a.messageIndex = make(map[string]*chatMessage)
	tt = ui.NewTester(a.view, 1180, 760)
	assertPixelNear(t, tt.Image(), 300, 150, veilWant, 8)
	if !textPresent(tt, "MothX") {
		t.Fatalf("home content missing over background: %q", tt.Texts())
	}
}

func TestWorkspacePickerProjectsRecentDirectories(t *testing.T) {
	a := testApp()
	a.prefsPath = filepath.Join(t.TempDir(), nativePreferencesFile)
	a.workspace = "/work/project"
	a.draftCwd = "/work/project"
	a.recentWorkspaces = []string{"/work/alpha", "/work/beta"}
	tt := ui.NewTester(a.view, 1180, 760)
	// The workspace basename also appears as plain text (header/home), so
	// target the composer menu button by its accessible role.
	button, ok := tt.AccessByRole(ui.RoleMenuButton, "project")
	if !ok {
		t.Fatalf("workspace menu button missing: %q", tt.Texts())
	}
	tt.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
	menu := tt.Menu()
	if len(menu) != 4 || menu[0] != T("选择目录")+"…" || menu[2] != "/work/alpha" || menu[3] != "/work/beta" {
		t.Fatalf("workspace menu = %q", menu)
	}
	if err := tt.Click("/work/alpha"); err != nil {
		t.Fatalf("choose recent directory: %v", err)
	}
	if a.workspace != "/work/alpha" || a.draftCwd != "/work/alpha" {
		t.Fatalf("recent choice not applied: %q %q", a.workspace, a.draftCwd)
	}
	if a.lastWorkspace != "/work/alpha" || a.recentWorkspaces[0] != "/work/alpha" {
		t.Fatalf("history not updated: last %q recent %+v", a.lastWorkspace, a.recentWorkspaces)
	}
}

func TestApplyWorkspaceChoiceRespectsRunningSession(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.sessions = []listedSession{{
		SessionID: "s1",
		Cwd:       "/work",
		Meta:      sessionMeta{LastRun: &sessionLastRun{Active: true, Status: "running"}},
	}}
	a.applyWorkspaceChoice("/elsewhere")
	if a.workspace == "/elsewhere" {
		t.Fatal("a running session must not be retargeted by a recent-directory choice")
	}
	if a.status == "" {
		t.Fatal("busy rejection must surface a status line")
	}
}
func TestSendAsksDirectoryForUnsetNewTaskDraft(t *testing.T) {
	a := testApp()
	a.draftSource = "new-task"
	a.workspace = ""
	a.draftCwd = ""
	a.draft = "hello"
	a.send()
	// With no window the picker resolves to cancel: the draft survives and
	// no run starts without a directory.
	if a.draft != "hello" {
		t.Fatalf("draft must survive a cancelled directory pick: %q", a.draft)
	}
	if a.status != "未选择工作目录，新任务未发送" {
		t.Fatalf("cancel status = %q", a.status)
	}
	if a.running || a.creatingSession {
		t.Fatal("a directory-less new task must not start a run")
	}
	// A quick-start draft with its scratch directory sends without asking.
	a.draftSource = "quick-start"
	a.workspace = "/tmp/scratch"
	a.draftCwd = "/tmp/scratch"
	a.status = ""
	a.send()
	if a.status == "未选择工作目录，新任务未发送" {
		t.Fatal("quick-start draft must not be asked for a directory")
	}
}

func TestElicitationFormCardRendersFields(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	var p elicitationParams
	raw := `{"sessionId":"s1","message":"Configure","requestedSchema":{"type":"object","properties":{"region":{"type":"string","title":"Region","enum":["cn","us"]},"note":{"type":"string","title":"Note"}}}}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	d := buildElicitationDecision(json.RawMessage(`"req-form"`), p)
	a.messages = []*chatMessage{{ID: "decision:req-form", Kind: messageDecision, Decision: &d}}
	a.messageIndex = map[string]*chatMessage{"decision:req-form": a.messages[0]}
	tt := ui.NewTester(a.view, 1180, 760)
	if !textPresent(tt, "Region") || !textPresent(tt, "Note") {
		t.Fatalf("form fields missing: %q", tt.Texts())
	}
	if err := tt.Click("cn"); err != nil {
		t.Fatalf("choose enum value: %v", err)
	}
	var region *decisionField
	for i := range d.Fields {
		if d.Fields[i].Name == "region" {
			region = &d.Fields[i]
		}
	}
	if region == nil || region.Value != "cn" {
		t.Fatalf("enum click did not set field value: %+v", d.Fields)
	}
}

func TestElicitationProjectionToleratesReplayedFormSchemas(t *testing.T) {
	// The Runtime's form projection embeds legacy question options as
	// {id,label} objects inside the "answer" property enum.
	var p elicitationParams
	raw := `{"sessionId":"s1","message":"Pick a color","requestedSchema":{"type":"object","properties":{"answer":{"type":"string","title":"Answer","enum":[{"id":"red","label":"Red"},{"id":"blue","label":"Blue"}]}},"required":["answer"]}}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("object-enum schema must parse: %v", err)
	}
	d := buildElicitationDecision(json.RawMessage(`7`), p)
	if d.Kind != "elicitation" || d.Title != "Answer" || len(d.Options) != 2 {
		t.Fatalf("elicitation decision = %+v", d)
	}
	if d.Options[0].ID != "red" || d.Options[0].Label != "Red" || d.Options[1].Label != "Blue" {
		t.Fatalf("enum options = %+v", d.Options)
	}

	// Plain string enums keep working, and a multi-property schema becomes a
	// real form: one field per property instead of mixing every option set.
	raw = `{"sessionId":"s1","message":"m","requestedSchema":{"type":"object","properties":{"other":{"type":"string","enum":["x"]},"answer":{"type":"string","enum":["y","z"]}}}}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	d = buildElicitationDecision(json.RawMessage(`8`), p)
	if len(d.Fields) != 2 || len(d.Options) != 0 {
		t.Fatalf("multi-property decision = %+v", d)
	}
	if d.Fields[0].Name != "answer" || len(d.Fields[0].Options) != 2 || d.Fields[0].Options[0].Label != "y" {
		t.Fatalf("answer field = %+v", d.Fields[0])
	}
	if d.Fields[1].Name != "other" || len(d.Fields[1].Options) != 1 || d.Fields[1].Options[0].Label != "x" {
		t.Fatalf("other field = %+v", d.Fields[1])
	}
	// Form answers are submitted as content keyed by property name.
	d.Fields[0].Value = "y"
	d.Fields[1].Value = "x"
	content := elicitationContent(&d)
	if content["answer"] != "y" || content["other"] != "x" || len(content) != 2 {
		t.Fatalf("form content = %+v", content)
	}
	if label := elicitationResolvedLabel(&d, content); label != "answer=y, other=x" {
		t.Fatalf("resolved label = %q", label)
	}
	d.Fields[0].Value = ""
	d.Fields[1].Value = ""
	if len(elicitationContent(&d)) != 0 {
		t.Fatal("an empty form must decline, not accept blank content")
	}

	// A free-text schema yields no options; the card still offers the input.
	raw = `{"sessionId":"s1","message":"m","requestedSchema":{"type":"object","properties":{"answer":{"type":"string","title":"Answer"}}}}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	d = buildElicitationDecision(json.RawMessage(`9`), p)
	if len(d.Options) != 0 || d.Prompt != "m" {
		t.Fatalf("free-text decision = %+v", d)
	}
}
func TestDraftProviderModelProjectionBeforeDirectoryPick(t *testing.T) {
	catalog := providerCatalogView{
		Providers: []providerSummary{
			{Name: "alpha", APIKeyConfigured: true},
			{Name: "beta", APIKeyConfigured: false},
		},
		Models: []providerCatalogModel{
			{ID: "m1", Provider: "alpha"},
			{ID: "m2", Name: "M2", Provider: "alpha"},
			{ID: "other", Provider: "beta"},
		},
		DefaultProvider: "alpha",
		DefaultModel:    "m2",
	}
	existing := []configOption{{Type: "select", ID: "mode", Name: "Mode", CurrentValue: "yolo"}}
	options, provider, model := projectDraftProviderModel(catalog, map[string]string{}, existing)
	if provider != "alpha" || model != "m2" {
		t.Fatalf("default selection = %q/%q", provider, model)
	}
	if len(options) != 3 || options[0].ID != "mode" {
		t.Fatalf("existing options must survive: %+v", options)
	}
	var providerOption, modelOption *configOption
	for i := range options {
		switch options[i].ID {
		case "provider":
			providerOption = &options[i]
		case "model":
			modelOption = &options[i]
		}
	}
	if providerOption == nil || len(providerOption.Options) != 1 || providerOption.Options[0].Value != "alpha" {
		t.Fatalf("unconfigured providers must be hidden: %+v", providerOption)
	}
	if modelOption == nil || len(modelOption.Options) != 2 || modelOption.CurrentValue != "m2" {
		t.Fatalf("model choices = %+v", modelOption)
	}

	// An explicit draft selection wins over the catalog default.
	_, provider, model = projectDraftProviderModel(catalog, map[string]string{"model": "m1"}, nil)
	if provider != "alpha" || model != "m1" {
		t.Fatalf("draft selection = %q/%q", provider, model)
	}

	// A runtime that projects no usable key must still leave a selectable menu.
	catalog.Providers[0].APIKeyConfigured = false
	options, provider, _ = projectDraftProviderModel(catalog, nil, nil)
	if provider != "alpha" || len(options) == 0 {
		t.Fatalf("all-unconfigured fallback = %q %+v", provider, options)
	}
	var po *configOption
	for i := range options {
		if options[i].ID == "provider" {
			po = &options[i]
		}
	}
	if po == nil || len(po.Options) != 2 {
		t.Fatalf("fallback must keep every provider: %+v", po)
	}
}

func TestSidebarHomeAndExpertsNavigationMatchDesktop(t *testing.T) {
	a := sidebarTestApp(t)
	a.setFeatures(map[string]bool{
		"projects": true, "sessionMeta": true, "sessionFork": true,
		"sessionWorkDir": true, "manageExperts": true,
	})
	a.currentSession = "s1"
	a.currentTitle = "Open Task"
	a.messages = []*chatMessage{{ID: "u1", Kind: messageUser, Text: "hello", Done: true}}

	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"首页", "技能", "会话历史", "自动化", "主角团", "资料库"} {
		if !textPresent(tt, want) {
			t.Fatalf("sidebar nav missing %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("首页"); err != nil {
		t.Fatalf("open home: %v", err)
	}
	if a.page != "home" || a.currentSession != "s1" || len(a.messages) != 1 {
		t.Fatalf("home nav must preserve the active session: page=%q session=%q messages=%d", a.page, a.currentSession, len(a.messages))
	}

	tt = ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("主角团"); err != nil {
		t.Fatalf("open experts: %v", err)
	}
	if !a.settingsOpen || a.settingsTab != "experts" {
		t.Fatalf("experts nav = settings %v tab %q", a.settingsOpen, a.settingsTab)
	}
}

func TestSidebarExpertsEntryIsFeatureGated(t *testing.T) {
	a := sidebarTestApp(t)
	a.setFeatures(map[string]bool{"projects": true, "sessionMeta": true})
	tt := ui.NewTester(a.view, 1180, 760)
	if textPresent(tt, "主角团") {
		t.Fatalf("experts nav must hide without manageExperts: %q", tt.Texts())
	}
}

func TestStartupErrorMarkerProjectsMessageFixAndSettingsAction(t *testing.T) {
	parsed, ok := parseNativeStartupErrorLine("MOTHX_ACP_ERROR {\"code\":\"config_invalid\",\"message\":\"provider unusable\",\"fix\":\"fix settings.json\"}")
	if !ok || parsed.Code != "config_invalid" || parsed.Message != "provider unusable" || parsed.Fix != "fix settings.json" {
		t.Fatalf("parsed startup error = %+v ok=%v", parsed, ok)
	}

	a := testApp()
	a.setStartupError(parsed)
	a.failConnection("ACP 进程已退出")
	if a.conn != connFailed || a.connErr != "provider unusable" || a.connFix != "fix settings.json" {
		t.Fatalf("failed connection = %v err=%q fix=%q", a.conn, a.connErr, a.connFix)
	}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"provider unusable", "修复：fix settings.json", "打开设置"} {
		if !textPresent(tt, want) {
			t.Fatalf("connection error missing %q: %q", want, tt.Texts())
		}
	}
}

func TestNativeACPChildUsesDesktopDecisionTimeouts(t *testing.T) {
	t.Setenv("MOTHX_ACP_PERMISSION_TIMEOUT", "1m")
	t.Setenv("MOTHX_ACP_QUESTION_TIMEOUT", "2m")
	env := nativeACPEnvironment()
	count := map[string]int{}
	for _, entry := range env {
		switch {
		case entry == "MOTHX_ACP_PERMISSION_TIMEOUT=30m":
			count["permission"]++
		case entry == "MOTHX_ACP_QUESTION_TIMEOUT=30m":
			count["question"]++
		case entry == "MOTHX_ACP_PERMISSION_TIMEOUT=1m" || entry == "MOTHX_ACP_QUESTION_TIMEOUT=2m":
			t.Fatalf("stale decision timeout leaked into ACP env: %q", entry)
		}
	}
	if count["permission"] != 1 || count["question"] != 1 {
		t.Fatalf("decision timeout env = %#v", count)
	}
}

func TestComposerProviderOptionsHideUnconfiguredProviders(t *testing.T) {
	a := testApp()
	a.applyProviderCatalog(providerCatalogView{Providers: []providerSummary{
		{Name: "openai", APIKeyConfigured: false},
		{Name: "deepseek", APIKeyConfigured: true},
		{Name: "anthropic", APIKeyConfigured: true},
	}})
	option := configOption{ID: "provider", Options: []configChoice{
		{Value: "openai", Name: "OpenAI"},
		{Value: "anthropic", Name: "Anthropic"},
		{Value: "deepseek", Name: "DeepSeek"},
	}}
	choices := a.visibleProviderChoices(option)
	if len(choices) != 2 || choices[0].Value != "deepseek" || choices[1].Value != "anthropic" {
		t.Fatalf("visible provider choices = %+v", choices)
	}

	// With no configured provider the menu must keep every choice.
	a.applyProviderCatalog(providerCatalogView{Providers: []providerSummary{
		{Name: "openai"}, {Name: "deepseek"},
	}})
	if choices := a.visibleProviderChoices(option); len(choices) != 3 {
		t.Fatalf("unconfigured runtime must keep all providers: %+v", choices)
	}
}

func TestPlanProgressPanelCollapsesAndReopens(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.currentTitle = "Plan"
	a.planMessageID = "plan-1"
	plan := &chatMessage{
		ID: "plan-1", Kind: messagePlan, Done: true, PlanTitle: "Build", PlanNote: "note",
		Plan: []planEntry{{Content: "step one", Status: "in_progress"}},
	}
	a.messages = []*chatMessage{plan}
	a.messageIndex = map[string]*chatMessage{"plan-1": plan}
	tt := ui.NewTester(a.view, 1180, 760)
	if !textPresent(tt, "step one") || !textPresent(tt, "0/1 已完成") {
		t.Fatalf("plan entry/count missing: %q", tt.Texts())
	}
	if err := tt.Click("收起计划进度"); err != nil {
		t.Fatalf("collapse plan: %v", err)
	}
	if !a.planCollapsed || a.planCollapsedID != "plan-1" {
		t.Fatalf("plan collapse state = %v/%q", a.planCollapsed, a.planCollapsedID)
	}
	tt = ui.NewTester(a.view, 1180, 760)
	if !textPresent(tt, "展开计划进度") {
		t.Fatalf("collapsed plan must expose the expand action: %q", tt.Texts())
	}
	if err := tt.Click("展开计划进度"); err != nil {
		t.Fatalf("expand plan: %v", err)
	}
	if a.planCollapsed {
		t.Fatal("plan stayed collapsed after expand")
	}
	if tt = ui.NewTester(a.view, 1180, 760); !textPresent(tt, "收起计划进度") {
		t.Fatalf("expanded plan must expose the collapse action: %q", tt.Texts())
	}
}

func TestNativeAutoRestartBackoffMatchesDesktop(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 15 * time.Second}
	for index, expected := range want {
		if got := nativeAutoRestartDelay(index + 1); got != expected {
			t.Fatalf("delay(%d) = %v, want %v", index+1, got, expected)
		}
	}
	if nativeNonRetryableStartupError(nativeStartupError{Code: "config_invalid"}) != true ||
		nativeNonRetryableStartupError(nativeStartupError{Code: "provider_unusable"}) != true ||
		nativeNonRetryableStartupError(nativeStartupError{Code: "acp_startup"}) != false {
		t.Fatal("non-retryable startup error classification mismatch")
	}

	a := testApp()
	a.setStartupError(nativeStartupError{Code: "config_invalid", Message: "bad config"})
	a.maybeScheduleAutoRestart()
	if a.autoRestartPending || a.autoRestartAttempts != 0 {
		t.Fatalf("non-retryable error scheduled a restart: %+v", a)
	}

	a = testApp()
	a.autoRestartAttempts = nativeMaxAutoRestartAttempts
	a.maybeScheduleAutoRestart()
	if a.autoRestartPending {
		t.Fatal("restart scheduled past the attempt limit")
	}
}

func TestSkillIconURLRestrictionAndCache(t *testing.T) {
	if _, ok := validSkillIconURL("https://cdn.example/icon.png"); !ok {
		t.Fatal("https icon URL rejected")
	}
	for _, raw := range []string{"file:///tmp/icon.png", "javascript:alert(1)", "data:image/png;base64,AAAA", "not a url"} {
		if _, ok := validSkillIconURL(raw); ok {
			t.Fatalf("invalid icon URL accepted: %q", raw)
		}
	}

	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	decoded, err := ui.DecodeImage(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	a := testApp()
	url := "https://cdn.example/icon.png"
	a.skillIconImages = map[string]ui.ImageSource{url: decoded}
	if got := a.skillIcon(skillHubSkill{IconURL: url}); got != decoded {
		t.Fatalf("cached icon not returned: %#v", got)
	}

	a.skillIconFailed = map[string]bool{}
	if got := a.skillIcon(skillHubSkill{IconURL: "file:///tmp/icon.png"}); got != nil {
		t.Fatalf("invalid scheme returned an image: %#v", got)
	}
	if !a.skillIconFailed["file:///tmp/icon.png"] {
		t.Fatal("invalid icon scheme was not cached as failed")
	}
}

func TestHomeProjectsDesktopConnectionSlogan(t *testing.T) {
	a := testApp()
	a.conn = connReady
	a.runtimeVersion = "1.3.109"
	a.page = "home"
	tt := ui.NewTester(a.view, 1180, 760)
	if !textPresent(tt, "已连接 · mothx 1.3.109 · ACP v1") {
		t.Fatalf("home connection slogan missing: %q", tt.Texts())
	}
	a.conn = connStarting
	if got := a.homeConnectionSlogan(); got != "正在连接 ACP…" {
		t.Fatalf("connecting slogan = %q", got)
	}
}

func TestPlanCardUsesDesktopDefaultTitleAndStepMarkers(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	plan := &chatMessage{ID: "plan-1", Kind: messagePlan, Done: true, Plan: []planEntry{
		{Content: "done step", Status: "completed"},
		{Content: "running step", Status: "in_progress"},
		{Content: "failed step", Status: "failed"},
	}}
	a.messages = []*chatMessage{plan}
	a.messageIndex = map[string]*chatMessage{"plan-1": plan}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"执行计划 · 3 步", "done step", "running step", "failed step"} {
		if !textPresent(tt, want) {
			t.Fatalf("plan card missing %q: %q", want, tt.Texts())
		}
	}
}
