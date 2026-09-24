# Changelog (Current Version)

This file contains the changes for the **current version only**. The full history of all versions lives in [docs/en/changelog.md](en/changelog.md).

## v1.3.103

### 🐛 Bug Fixes

- **Context Compaction No Longer Fails with "max iterations (1) exceeded"**
  - Compaction summarizes the conversation through a child Agent built with `MaxIterations: 1` — exactly one LLM turn for the summary. Recovery retries inside the loop (empty-response retry, output-limit escalation and continuation, content rejection, context-overflow recovery, and Responses remote-state replay) each issued a fresh provider request without spending the logical iteration counter, so any single recovery consumed the only iteration and the whole compaction aborted with `generate summary: max iterations (1) exceeded`. Because a failed compaction leaves the oversized context in place, the error then repeated on every subsequent turn.
  - These recovery attempts no longer consume the logical iteration budget — each path keeps its own bounded retry counter (2 empty-response retries, 3 output continuations, 2 content-rejection stages), so `MaxIterations` now means "productive LLM turns" and a summarizer survives empty or truncated provider responses instead of dying. This aligns the loop with the existing transport-recovery rule and the shared principle that recovery must not consume the iteration budget.
  - The summarizer child keeps a limit of 3 as a safety margin for a stray phantom tool-call turn (its tool set stays empty), and the misleading "tool result summarization returned empty result" error was renamed to "summarization returned empty result".
  - Regression tests cover empty-response and output-limit recovery within a 1-iteration budget and the summarizer child recovering from empty provider responses.
