package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oschina/mothx/internal/tui/i18n"
	"github.com/oschina/mothx/internal/tui/renderutil"
	"github.com/startvibecoding/GoStreamingMarkdown/gsm"
)

func (a *App) updateViewportContent() {
	if a.program != nil {
		a.liveContent = ""
		return
	}
	content := a.renderTranscriptContent()
	a.liveContent = content
}

func (a *App) renderTranscriptContent() string {
	count := len(a.messages)
	if a.currentThinkIdx >= count {
		count = a.currentThinkIdx + 1
	}
	if a.currentAssistantIdx >= count {
		count = a.currentAssistantIdx + 1
	}
	indices := make([]int, 0, count)
	for idx := 0; idx < count; idx++ {
		indices = append(indices, idx)
	}
	return a.joinTranscriptBlocks(indices)
}

func (a *App) renderLiveTranscriptContent() string {
	if a.program == nil {
		return a.liveContent
	}

	count := len(a.messages)
	if a.currentThinkIdx >= count {
		count = a.currentThinkIdx + 1
	}
	if a.currentAssistantIdx >= count {
		count = a.currentAssistantIdx + 1
	}
	indices := make([]int, 0, 2)
	for idx := 0; idx < count; idx++ {
		if a.printedMessageIdx[idx] {
			continue
		}
		isCurrentApproval := a.waitingForApproval && a.currentApprovalIdx >= 0 && idx == a.currentApprovalIdx
		// Keep an active tool in the managed viewport until its terminal result
		// arrives. This is shared by compact and full event display so the
		// transient "running" row is never committed to terminal scrollback.
		isRunningTool := a.isToolMessageIndex(idx) && a.toolResultRunningAt(idx)
		if idx != a.currentThinkIdx && idx != a.currentAssistantIdx && !isCurrentApproval && !isRunningTool {
			continue
		}
		indices = append(indices, idx)
	}
	return a.joinTranscriptBlocks(indices)
}

// minToolGroupSize is the number of tool calls that ran in parallel at which
// the transcript collapses them into one tree block with a count title instead
// of rendering one independent row per call.
const minToolGroupSize = 2

// joinTranscriptBlocks renders the given message indices in order, joining the
// non-empty blocks with a blank line. Tool calls that ran in parallel are
// coalesced into a single tree block so the batch reads as one unit for its
// whole lifetime, from "running" through its committed terminal state.
func (a *App) joinTranscriptBlocks(indices []int) string {
	blocks := make([]string, 0, len(indices))
	emitted := make(map[int]bool)
	for _, idx := range indices {
		if gid := a.toolGroupIDAt(idx); gid > 0 && a.isMultiToolGroup(gid) {
			if emitted[gid] {
				continue
			}
			emitted[gid] = true
			if block := a.renderToolGroupBlock(gid); block != "" {
				blocks = append(blocks, block)
			}
			continue
		}
		if rendered := a.renderTranscriptBlock(idx); rendered != "" {
			blocks = append(blocks, rendered)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func (a *App) renderTranscriptBlock(idx int) string {
	rendered := strings.TrimRight(a.renderMessageAt(idx), "\n")
	if strings.TrimSpace(rendered) == "" {
		return ""
	}
	return rendered
}

// renderToolGroupBlock renders a parallel tool-call batch as a tree: a title
// carrying the live count followed by one indented branch per call. The title
// switches from the running form to the completed form once every call has
// reached a terminal state, so the group keeps its shape in scrollback.
func (a *App) renderToolGroupBlock(groupID int) string {
	members := a.toolGroupMembers(groupID)
	if len(members) < minToolGroupSize {
		return ""
	}
	running := 0
	for _, member := range members {
		if member.status == toolResultStatusRunning {
			running++
		}
	}
	title := i18n.MsgToolGroupDone
	if running > 0 {
		title = i18n.MsgToolGroupRunning
	}
	lines := make([]string, 0, len(members)+1)
	lines = append(lines, toolStyle.Render(a.translator.Text(title, len(members))))
	for i, member := range members {
		branch := "├─ "
		if i == len(members)-1 {
			branch = "└─ "
		}
		body := strings.TrimRight(a.renderToolResult(*member), "\n")
		if strings.TrimSpace(body) == "" {
			continue
		}
		lines = append(lines, treeIndentBlock(toolStyle.Render(branch), body))
	}
	return strings.Join(lines, "\n")
}

// treeIndentBlock prefixes the first line of body with the (styled) tree branch
// and aligns every continuation line under it.
func treeIndentBlock(branch, body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 1 {
		return branch + lines[0]
	}
	var b strings.Builder
	b.WriteString(branch)
	b.WriteString(lines[0])
	for _, line := range lines[1:] {
		b.WriteString("\n   ")
		b.WriteString(line)
	}
	return b.String()
}

// toolGroupIDAt reports the parallel-group id of the tool row at a message
// index, or 0 when the index is not a tool row.
func (a *App) toolGroupIDAt(idx int) int {
	if result := a.toolResultAt(idx); result != nil {
		return result.groupID
	}
	return 0
}

// toolGroupMembers returns the rows of a parallel group in message order.
func (a *App) toolGroupMembers(groupID int) []*toolResult {
	if groupID <= 0 {
		return nil
	}
	var members []*toolResult
	for i := range a.toolResults {
		if a.toolResults[i].groupID == groupID {
			members = append(members, &a.toolResults[i])
		}
	}
	return members
}

func (a *App) isMultiToolGroup(groupID int) bool {
	return groupID > 0 && len(a.toolGroupMembers(groupID)) >= minToolGroupSize
}

func (a *App) toolResultAt(idx int) *toolResult {
	for i := range a.toolResults {
		if a.toolResults[i].msgIndex == idx {
			return &a.toolResults[i]
		}
	}
	return nil
}

func (a *App) toolResultRunningAt(idx int) bool {
	for _, result := range a.toolResults {
		if result.msgIndex == idx {
			return result.status == toolResultStatusRunning
		}
	}
	return false
}

func (a *App) configureMarkdownRenderer() {
	width := renderutil.MarkdownStyleWrapWidth(a.assistantMarkdownWidth())
	a.mdRenderer = gsm.NewStream(width, nil)
}

func (a *App) assistantMarkdownWidth() int {
	width := a.width
	if width <= 0 {
		width = 80
	}
	width -= lipgloss.Width(a.translator.Text(i18n.MsgAssistantPrefix))
	if width < 1 {
		return 1
	}
	return width
}

func (a *App) renderFixedHeight(view string) string {
	if a.height <= 0 {
		return view
	}
	view = strings.TrimRight(view, "\n")
	lines := strings.Split(view, "\n")
	if len(lines) > a.height {
		lines = lines[len(lines)-a.height:]
	}
	for len(lines) < a.height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
