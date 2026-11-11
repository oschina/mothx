package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/provider"
)

// Member completion statuses carried by MemberCompletion.Status. They match
// the terminal managed-agent states so adapters can project them uniformly.
const (
	MemberStatusDone       = "done"
	MemberStatusError      = "error"
	MemberStatusCanceled   = "canceled"
	MemberStatusIncomplete = "incomplete"
)

// Member notification kinds. A mailbox item is either a terminal completion or
// a blocking question the member asked the lead instead of the human.
const (
	MemberItemCompletion = "completion"
	MemberItemQuestion   = "question"
	// MemberItemFinding is a non-terminal worker finding. It enters lead
	// context and may wake an idle lead, but it never ends the worker turn.
	MemberItemFinding = "finding"
)

// MemberStatusQuestion is the status projected for MemberItemQuestion entries
// in subagent_wait summaries.
const MemberStatusQuestion = "question"

// MemberStatusFinding is the status projected for MemberItemFinding entries.
const MemberStatusFinding = "finding"

// Rune budgets for the steering-injected completion payload. Error payloads
// use a smaller budget to reserve room for the appended next-step hint.
const (
	memberPayloadRunes      = 3500
	memberErrorPayloadRunes = 3000
	memberTruncationSuffix  = "…[truncated]"
)

// MemberCompletion is one member notification queued in a MemberMailbox.
// For completions, Payload carries the final response (done) or the error text
// (error/canceled/incomplete). For MemberItemQuestion entries, Payload carries
// the question text and QuestionID/Options identify the pending request the
// lead must answer with subagent_answer.
type MemberCompletion struct {
	Kind        string // "" (completion) | MemberItemCompletion | MemberItemQuestion
	MemberID    string
	DisplayName string
	Status      string // "done" | "error" | "canceled" | "incomplete" | "question"
	Payload     string // final result, error explanation, or question text
	QuestionID  string
	Options     []string
	// External marks a completion from an external ACP worker instead of an
	// expert-team member. MemberID then carries the canonical child handle and
	// the next-step hint points at the external-worker tools.
	External bool
	// Wake marks an item that may start or wake a lead Run. Completion and
	// question items are waking; a non-waking item (for example a future
	// progress/notice projection) stays readable through HasPending but never
	// signals activity or triggers a mailbox continuation.
	Wake bool
	// MessageID optionally identifies the Runtime-owned evidence row for an
	// external worker item. The mailbox never interprets it; a delivery
	// observer installed by Runtime uses it to record consumption.
	MessageID string
}

// MemberMailbox is the session-level in-memory queue of member completions.
// The runtime drains it at agent-loop iteration boundaries through
// DrainSteering (composed into GetSteeringMessages by the runtime assembly
// layer); subagent_wait merges a blocked lead into activity through
// WaitForActivity. A mailbox never wakes or starts a run by itself.
// The zero value is not usable; create one with NewMemberMailbox. All methods
// are safe on a nil mailbox so callers can skip nil checks at use sites.
type MemberMailbox struct {
	mu       sync.Mutex
	queue    []MemberCompletion
	activity chan struct{} // capacity 1, non-blocking wakeup hint
	// activityListeners receive the same non-blocking hint as activity. They
	// are an additive projection for a Runtime-owned continuation scheduler;
	// the mailbox still owns the queue and never starts a run itself.
	activityListeners    map[uint64]func()
	nextActivityListener uint64
	// runningChildren reports whether any managed child is still running. It is
	// installed by AgentManager when the member context is bound and lets the
	// lead's would-stop hook wait for members without knowing the manager.
	runningChildren func() bool
	// deliveryObserver receives the items removed by DrainSteering. Runtime owns
	// the concrete observer; the mailbox only reports consumption.
	deliveryObserver func([]MemberCompletion)
	// supersededObserver receives items displaced by EnqueueCoalesced. Runtime
	// uses it to close the evidence row the lead will never consume.
	supersededObserver func([]MemberCompletion)
}

// NewMemberMailbox creates an empty mailbox.
func NewMemberMailbox() *MemberMailbox {
	return &MemberMailbox{activity: make(chan struct{}, 1)}
}

// SetRunningPredicate installs the "any member still running" probe used by the
// lead's follow-up hook. Nil clears a previous binding.
func (m *MemberMailbox) SetRunningPredicate(running func() bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.runningChildren = running
	m.mu.Unlock()
}

// AddActivityListener registers a callback invoked after a completion is
// queued. The callback is advisory, must not block, and never changes queue
// ownership. The returned function removes the listener and is safe to call
// more than once.
func (m *MemberMailbox) AddActivityListener(listener func()) func() {
	if m == nil || listener == nil {
		return func() {}
	}
	m.mu.Lock()
	if m.activityListeners == nil {
		m.activityListeners = make(map[uint64]func())
	}
	m.nextActivityListener++
	id := m.nextActivityListener
	m.activityListeners[id] = listener
	m.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.activityListeners, id)
			m.mu.Unlock()
		})
	}
}

// SetDeliveryObserver installs the Runtime-owned consumption observer. It is
// advisory and may be called with the drained items after they leave the queue.
func (m *MemberMailbox) SetDeliveryObserver(observer func([]MemberCompletion)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.deliveryObserver = observer
	m.mu.Unlock()
}

// SetSupersededObserver installs the Runtime-owned observer for items displaced
// by EnqueueCoalesced. It is advisory, runs outside the mailbox lock, and must
// not block.
func (m *MemberMailbox) SetSupersededObserver(observer func([]MemberCompletion)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.supersededObserver = observer
	m.mu.Unlock()
}

// RunningChildren reports whether members are still running. It always reports
// false when no predicate is installed, so a mailbox that is only used for
// notifications never blocks a run.
func (m *MemberMailbox) RunningChildren() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	running := m.runningChildren
	m.mu.Unlock()
	return running != nil && running()
}

// Enqueue appends a completion to the queue and, when it is a waking item,
// non-blockingly signals activity so a waiting subagent_wait returns promptly
// and the Runtime continuation scheduler may consider a new Run. A non-waking
// item is still queued for status/UI reads but never starts a Run.
func (m *MemberMailbox) Enqueue(c MemberCompletion) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.queue = append(m.queue, c)
	if !c.Wake {
		m.mu.Unlock()
		return
	}
	listeners := make([]func(), 0, len(m.activityListeners))
	for _, listener := range m.activityListeners {
		listeners = append(listeners, listener)
	}
	m.mu.Unlock()
	select {
	case m.activity <- struct{}{}:
	default:
	}
	for _, listener := range listeners {
		func() {
			defer func() { _ = recover() }()
			listener()
		}()
	}
}

// EnqueueCoalesced queues a non-terminal finding, replacing an existing pending
// finding from the same member instead of accumulating a burst. The replaced
// payload is superseded: the lead only needs the latest finding segment. The
// superseded item is reported to the Runtime observer so its evidence row does
// not stay "created" forever. It still signals activity when the item is waking.
func (m *MemberMailbox) EnqueueCoalesced(c MemberCompletion) {
	if m == nil {
		return
	}
	m.mu.Lock()
	var superseded []MemberCompletion
	for i := len(m.queue) - 1; i >= 0; i-- {
		if m.queue[i].Kind == c.Kind && m.queue[i].MemberID == c.MemberID {
			superseded = append(superseded, m.queue[i])
			m.queue[i] = c
			break
		}
	}
	if len(superseded) == 0 {
		m.queue = append(m.queue, c)
	}
	observer := m.supersededObserver
	if !c.Wake {
		m.mu.Unlock()
		notifySuperseded(observer, superseded)
		return
	}
	listeners := make([]func(), 0, len(m.activityListeners))
	for _, listener := range m.activityListeners {
		listeners = append(listeners, listener)
	}
	m.mu.Unlock()
	notifySuperseded(observer, superseded)
	select {
	case m.activity <- struct{}{}:
	default:
	}
	for _, listener := range listeners {
		func() {
			defer func() { _ = recover() }()
			listener()
		}()
	}
}

func notifySuperseded(observer func([]MemberCompletion), items []MemberCompletion) {
	if observer == nil || len(items) == 0 {
		return
	}
	func() {
		defer func() { _ = recover() }()
		observer(items)
	}()
}

// HasPending reports whether undelivered completions are queued.
func (m *MemberMailbox) HasPending() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue) > 0
}

// HasWakingPending reports whether at least one queued item may start or wake
// a lead Run. Only the mailbox-wake path uses this predicate; status and UI
// projections keep using HasPending so a non-waking notice stays visible.
func (m *MemberMailbox) HasWakingPending() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.queue {
		if item.Wake {
			return true
		}
	}
	return false
}

// DrainSteering removes and returns every queued completion in enqueue order,
// formatted as system-injected steering messages (one per completion). It
// returns nil when nothing is pending. The activity hint is drained alongside
// the queue so a later WaitForActivity does not return on a stale signal.
func (m *MemberMailbox) DrainSteering() []provider.Message {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	pending := m.queue
	m.queue = nil
	observer := m.deliveryObserver
	m.mu.Unlock()
	select {
	case <-m.activity:
	default:
	}
	if len(pending) == 0 {
		return nil
	}
	if observer != nil {
		drained := append([]MemberCompletion(nil), pending...)
		observer(drained)
	}
	messages := make([]provider.Message, 0, len(pending))
	for _, c := range pending {
		messages = append(messages, provider.NewSystemInjectedUserMessage(formatMemberItem(c)))
	}
	return messages
}

// PendingSummary returns a read-only snapshot of the queued completions
// without removing them. subagent_wait uses it for its pending list; content
// delivery stays exclusively with DrainSteering.
func (m *MemberMailbox) PendingSummary() []MemberCompletion {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queue) == 0 {
		return nil
	}
	out := make([]MemberCompletion, len(m.queue))
	copy(out, m.queue)
	return out
}

// WaitForActivity blocks until the mailbox signals activity, the timeout
// elapses (timedOut=true, err=nil), or ctx is done (err wraps ctx.Err).
func (m *MemberMailbox) WaitForActivity(ctx context.Context, timeout time.Duration) (bool, error) {
	if m == nil {
		return false, errors.New("member mailbox is not initialized")
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, fmt.Errorf("wait for member activity: %w", ctx.Err())
	case <-timer.C:
		return true, nil
	case <-m.activity:
		return false, nil
	}
}

// formatMemberItem renders one mailbox item as the machine-readable steering
// envelope. Questions and completions use distinct markers so the lead never
// mistakes a member question for a finished task.
func formatMemberItem(c MemberCompletion) string {
	if c.Kind == MemberItemQuestion {
		return formatMemberQuestion(c)
	}
	if c.Kind == MemberItemFinding {
		return formatMemberFinding(c)
	}
	return formatMemberCompletion(c)
}

// formatMemberFinding renders a non-terminal worker finding. It is explicitly
// worker-reported evidence, never an instruction or a completion replacement.
func formatMemberFinding(c MemberCompletion) string {
	var b strings.Builder
	b.WriteString("[WORKER_FINDING] 系统注入的 worker 上报上下文（非用户输入，worker-reported 证据）。\n")
	fmt.Fprintf(&b, "worker: %s\n", c.MemberID)
	b.WriteString("payload:\n")
	b.WriteString(truncateMemberPayload(c.Payload, memberPayloadRunes))
	b.WriteString("\n")
	if c.External {
		fmt.Fprintf(&b, "下一步：用 delegate_worker_mgr(action:%q, handle:%q) 核对证据；worker 仍在运行，除非你决定 send 或 destroy。", "status", c.MemberID)
	} else {
		fmt.Fprintf(&b, "下一步：用 subagent_status(handle:%q) 核对证据；worker 仍在运行，除非你决定 send 或 destroy。", c.MemberID)
	}
	return b.String()
}

// formatMemberQuestion renders a member question the lead must answer. It never
// impersonates user intent; the member is blocked until subagent_answer runs.
func formatMemberQuestion(c MemberCompletion) string {
	var b strings.Builder
	b.WriteString("[MEMBER_QUESTION] 系统注入的成员提问上下文（非用户输入）。成员正在等待你的回答。\n")
	if c.DisplayName != "" {
		fmt.Fprintf(&b, "member: %s（%s）\n", c.MemberID, c.DisplayName)
	} else {
		fmt.Fprintf(&b, "member: %s\n", c.MemberID)
	}
	fmt.Fprintf(&b, "question_id: %s\n", c.QuestionID)
	b.WriteString("question:\n")
	b.WriteString(truncateMemberPayload(c.Payload, memberPayloadRunes))
	b.WriteString("\n")
	if len(c.Options) > 0 {
		fmt.Fprintf(&b, "options: %s\n", strings.Join(c.Options, " | "))
	}
	if c.External {
		fmt.Fprintf(&b, "下一步：用 delegate_worker_mgr(action:%q, question_id:%q, answer:\"…\") 回答；worker 会继续执行。", "answer", c.QuestionID)
	} else {
		fmt.Fprintf(&b, "下一步：用 subagent_answer(handle:%q, question_id:%q, answer:\"…\") 回答；成员会继续执行。", c.MemberID, c.QuestionID)
	}
	return b.String()
}

// formatMemberCompletion renders one terminal completion as the machine-readable
// steering envelope. The [MEMBER_COMPLETION] marker keeps the injection
// identifiable as system-provided member state context; it never impersonates
// user intent.
func formatMemberCompletion(c MemberCompletion) string {
	var b strings.Builder
	b.WriteString("[MEMBER_COMPLETION] 系统注入的成员状态上下文（非用户输入）。\n")
	if c.DisplayName != "" {
		fmt.Fprintf(&b, "member: %s（%s）\n", c.MemberID, c.DisplayName)
	} else {
		fmt.Fprintf(&b, "member: %s\n", c.MemberID)
	}
	fmt.Fprintf(&b, "status: %s\n", c.Status)
	b.WriteString("payload:\n")
	if c.Status == MemberStatusError {
		b.WriteString(truncateMemberPayload(c.Payload, memberErrorPayloadRunes))
		b.WriteString("\n")
		if c.External {
			fmt.Fprintf(&b, "下一步：如仍需该外部 worker，用 delegate_worker_mgr(action:%q, handle:%q) 检查状态，或用 delegate_worker_mgr(action:%q, handle:%q, message:\"…\") 继续；需要新任务时用 delegate_worker_mgr(action:%q, agent:<绑定的 profile>, task:…)。", "status", c.MemberID, "send", c.MemberID, "spawn")
		} else {
			fmt.Fprintf(&b, "下一步：如仍需该成员，用 subagent_spawn(member:%q, task:…) 重新派发任务。", c.MemberID)
		}
	} else {
		b.WriteString(truncateMemberPayload(c.Payload, memberPayloadRunes))
	}
	return b.String()
}

// truncateMemberPayload truncates s to at most limit runes, marking the cut
// with the truncation suffix.
func truncateMemberPayload(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + memberTruncationSuffix
}
