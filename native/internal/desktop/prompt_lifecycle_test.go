package desktop

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/native/internal/feature/hosttest"
)

// promptSignals connects a runtime that holds its prompts: got is touched
// once the runtime has a prompt, and the prompt is answered once release
// exists.
func promptSignals(t *testing.T, scenario, stop string) (a *nativeApp, queue *dispatchQueue, got, release string) {
	t.Helper()
	a, queue = connectTestApp(t)
	got, release = hosttest.SignalFiles(t)
	t.Setenv(hosttest.EnvStop, stop)
	setLaunchBinaryOverride(acpChildShim(t, scenario))
	go a.connect()
	if !queue.runUntil(t, 20*time.Second, func() bool {
		return a.conn == connReady && connectAttemptsIdle(a) && a.client() != nil
	}) {
		t.Fatalf("the runtime never connected: conn=%d err=%q", a.conn, a.connErr)
	}
	return a, queue, got, release
}

func waitForFile(t *testing.T, path string) {
	hosttest.WaitForFile(t, path)
}

func taskState(a *nativeApp, id string) *sessionSnapshot {
	state, ok := a.sessionStates[id]
	if !ok {
		return nil
	}
	return &state
}

func hasMessageWithPrefix(messages []*chatMessage, prefix string) bool {
	for _, m := range messages {
		if strings.HasPrefix(m.Text, prefix) {
			return true
		}
	}
	return false
}

// A long run can answer after the user switched tasks. Its answer belongs to
// the task it ran in; the open task's run state and transcript must not be
// touched.
func TestPromptCompletionAppliesToItsOwnTask(t *testing.T) {
	a, queue, got, release := promptSignals(t, "prompt", "max_tokens")
	a.currentSession = "s1"
	a.draft = "hello from s1"
	a.send()
	waitForFile(t, got)
	queue.drain()

	// The user opens another task, which has a run of its own, while the first
	// one is still going.
	a.saveActiveSession()
	a.clearSessionState("s2")
	a.running = true
	a.appendMessage(&chatMessage{ID: "s2:1", Kind: messageAssistant, Text: "still here", Done: true})

	hosttest.Release(t, release)
	if !queue.runUntil(t, 20*time.Second, func() bool {
		return taskState(a, "s1") != nil && !taskState(a, "s1").Running
	}) {
		t.Fatal("the task that ran the prompt never recorded its end")
	}

	if a.currentSession != "s2" {
		t.Fatalf("the open task changed to %q", a.currentSession)
	}
	if !a.running {
		t.Fatal("another task's answer ended the open task's run")
	}
	if hasMessageWithPrefix(a.messages, "运行结束") {
		t.Fatal("another task's answer was written into the open transcript")
	}
	if last := a.messages[len(a.messages)-1]; last.ID != "s2:1" {
		t.Fatalf("the open transcript changed: last message %q", last.ID)
	}
	first := taskState(a, "s1")
	last := first.Messages[len(first.Messages)-1]
	if last.Kind != messageSystem || last.Text != "运行结束：max_tokens" {
		t.Fatalf("the task that ran the prompt must record how it ended, got %+v", last)
	}
}

// A prompt that fails while another task is open is reported there, not in the
// open task's transcript or composer.
func TestPromptFailureLeavesTheOpenTaskAlone(t *testing.T) {
	a, queue, _, _ := promptSignals(t, "failprompt", "")
	a.currentSession = "s1"
	a.draft = "hello"
	a.send()
	a.saveActiveSession()
	a.clearSessionState("s2")
	a.draft = "typing in s2"

	if !queue.runUntil(t, 20*time.Second, func() bool {
		return taskState(a, "s1") != nil && !taskState(a, "s1").Running
	}) {
		t.Fatal("the task whose prompt failed never returned to idle")
	}
	if a.currentSession != "s2" || len(a.messages) != 0 {
		t.Fatalf("the open task's transcript changed: session=%q messages=%d", a.currentSession, len(a.messages))
	}
	if a.draft != "typing in s2" {
		t.Fatalf("the open task's composer = %q, want it untouched", a.draft)
	}
	failed := taskState(a, "s1")
	last := failed.Messages[len(failed.Messages)-1]
	if last.Kind != messageError || !strings.Contains(last.Text, "发送失败") {
		t.Fatalf("the failing task must show why, got %+v", last)
	}
}

// A failed first send (an unconfigured provider is the common case) must not
// cost the person what they typed and attached.
func TestFailedSendRestoresTheDraft(t *testing.T) {
	a, queue, _, _ := promptSignals(t, "failnew", "")
	a.attachments = []attachment{{Name: "notes.txt", Path: "/tmp/notes.txt", MimeType: "text/plain", Size: 12}}
	a.draft = "  keep me  "
	a.send()
	if !queue.runUntil(t, 20*time.Second, func() bool { return !a.creatingSession && !a.running }) {
		t.Fatal("the shell never returned to idle after the failed send")
	}
	if a.draft != "  keep me  " {
		t.Fatalf("draft = %q, want it restored", a.draft)
	}
	if len(a.attachments) != 1 || a.attachments[0].Name != "notes.txt" {
		t.Fatalf("attachments = %+v, want them restored", a.attachments)
	}
	for _, m := range a.messages {
		if m.Kind == messageUser {
			t.Fatal("the bubble for a prompt the Runtime never accepted must go with its text")
		}
	}
	if last := a.messages[len(a.messages)-1]; last.Kind != messageError || !strings.Contains(last.Text, "发送失败") {
		t.Fatalf("the failure must stay visible, got %+v", a.messages[len(a.messages)-1])
	}
}

// Text typed while a run was going is newer than the failed send; the failed
// text then stays in its bubble and the composer is left alone.
func TestFailedSendKeepsWhatWasTypedSince(t *testing.T) {
	a, queue, got, release := promptSignals(t, "prompt", "fail")
	a.currentSession = "s1"
	a.draft = "first attempt"
	a.send()
	waitForFile(t, got)
	queue.drain()
	a.draft = "typed meanwhile"

	hosttest.Release(t, release)
	if !queue.runUntil(t, 20*time.Second, func() bool { return !a.running }) {
		t.Fatal("the shell never returned to idle after the failed send")
	}
	if a.draft != "typed meanwhile" {
		t.Fatalf("draft = %q, want the newer text kept", a.draft)
	}
	if !hasMessageWithPrefix(a.messages, "first attempt") {
		t.Fatal("the failed text must stay readable in its bubble")
	}
}

// A cancelled run's late answer must not end the run that followed it.
func TestLateAnswerOfAnEarlierSendDoesNotEndTheNextRun(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.promptToken = 2
	a.running = true

	a.completePrompt("s1", 1, "end_turn")
	if !a.running {
		t.Fatal("an earlier send's answer ended the next run")
	}

	a.completePrompt("s1", 2, "end_turn")
	if a.running {
		t.Fatal("the owning send's answer must end its run")
	}
}

// The person can open another task while a draft's session is being created.
// The new task must not pull the shell back onto it; it becomes a background
// task and the prompt still runs there.
func TestCreatedSessionDoesNotPullTheShellBack(t *testing.T) {
	a, queue, got, release := promptSignals(t, "prompt", "end_turn")
	a.draft = "a new task"
	a.send()
	waitForFile(t, got)

	a.clearSessionState("s2")
	queue.drain()

	if a.currentSession != "s2" {
		t.Fatalf("the new task pulled the shell back: session=%q", a.currentSession)
	}
	if a.creatingSession {
		t.Fatal("creatingSession must be released when the shell moved on")
	}
	created := taskState(a, "created-1")
	if created == nil || !created.Running || created.PromptToken != 1 {
		t.Fatalf("the created task must keep running in the background: %+v", created)
	}

	hosttest.Release(t, release)
	if !queue.runUntil(t, 20*time.Second, func() bool {
		return taskState(a, "created-1") != nil && !taskState(a, "created-1").Running
	}) {
		t.Fatal("the background task never recorded the end of its prompt")
	}
	if a.currentSession != "s2" {
		t.Fatalf("the open task changed to %q", a.currentSession)
	}
}

// A pasted screenshot with no caption is a prompt, as it is on Desktop. The
// request carries the attachment and no empty text block.
func TestAttachmentAloneCanBeSent(t *testing.T) {
	a, queue, got, release := promptSignals(t, "prompt", "end_turn")
	a.currentSession = "s1"
	a.attachments = []attachment{{Name: "shot.png", Path: "/tmp/shot.png", MimeType: "image/png", Size: 1200}}

	a.send()
	hosttest.WaitForFile(t, got)

	if !a.running {
		t.Fatal("an attachment on its own must start a send")
	}
	request, err := os.ReadFile(got + ".request")
	if err != nil {
		t.Fatal(err)
	}
	wire := string(request)
	if !strings.Contains(wire, `"`+"type"+`"`+`:`+`"`+"resource_link"+`"`) || strings.Contains(wire, `"`+"type"+`"`+`:`+`"`+"text"+`"`) {
		t.Fatalf("prompt blocks = %s, want the link only", wire)
	}
	hosttest.Release(t, release)
	if !queue.runUntil(t, 20*time.Second, func() bool { return !a.running }) {
		t.Fatal("the prompt never finished")
	}
	// send() writes a status line and then the bubble, which keeps the
	// attachment note as its text because there is no caption.
	var bubble *chatMessage
	for _, m := range a.messages {
		if m.Kind == messageUser {
			bubble = m
		}
	}
	if bubble == nil || bubble.Text != T("附件：")+"shot.png" {
		messages := make([]string, 0, len(a.messages))
		for _, m := range a.messages {
			messages = append(messages, fmt.Sprintf("kind=%d text=%q", m.Kind, m.Text))
		}
		t.Fatalf("bubble = %v, want the attachment note only", messages)
	}
}

// With nothing typed and nothing attached there is still nothing to send.
func TestSendWithNothingIsStillIgnored(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.send()
	if a.running || len(a.messages) != 0 || a.promptToken != 0 {
		t.Fatalf("an empty composer must not send: running=%v messages=%d token=%d",
			a.running, len(a.messages), a.promptToken)
	}
}
