# Changelog (Current Version)

This file contains the changes for the **current version only**. The full history of all versions lives in [docs/en/changelog.md](en/changelog.md).

## v1.3.101

### ✨ New Features

- **Serve: Placeholder API Token Warnings**
  - `mothx serve init-config` ships a well-known template token, which is equivalent to publishing the API key once auth is enabled — and nothing said so. The token is now a single named constant (`serve.PlaceholderAuthToken`) with explicit detection (`IsPlaceholderAuthToken`/`UsesPlaceholderAuthToken`), and a replacement warning is printed wherever the template is created (`mothx serve init-config` and the `--init-serve` CLI path) and again on `mothx serve` startup while `api.auth.enabled` is true and the token is still in place.
  - The startup check only fires for an enabled auth block, so the default template (auth disabled, loopback only) stays quiet and a replaced token is never flagged. `serve init-config` now writes through the command's own stderr, so the warning travels with the command output.

- **Members Wait for Their Lead on Interactive Surfaces**
  - A session that can spawn members but is not bound to an expert team now keeps its run open for still-running members on interactive surfaces (TUI, Web UI, ACP), so a member's completion or question is handled within the same run instead of waiting for the next lead run. Headless and asynchronous sources (CLI, cron, WeChat/Feishu) still end the turn normally and deliver member notifications at their next iteration or next run; a bound expert team always waits.

- **Bound Teams Keep the Full Sub-Agent Tool Set**
  - A bound expert team always exposes the complete canonical sub-agent tool set (`subagent_spawn`, `subagent_status`, `subagent_send`, `subagent_wait`, `subagent_answer`, `subagent_destroy`). Per-tool toggles that disable individual sub-agent tools apply only to non-team multi-agent sessions; the team capability is authoritative and never drops tools.

### 🐛 Bug Fixes

- **MCP: Image Tool Results Reach the Model Instead of a Placeholder**
  - An MCP tool that returned image content reached the model as the literal string `[image content: image/png]`. The base64 payload was already present in the MCP response, but the client decoded every content block into text only, and the tool returned a text-only result, so a screenshot-style MCP server could report coordinates while the model never saw the picture. `resources/read` binary resources were worse: the `blob` field had no matching struct field, so the payload was dropped during decode and not even the placeholder appeared.
  - `tools/call` and `resources/read` now project image blocks into `tools.ToolResult.Contents` as real provider image content, reusing the same provider-aware preprocessing as the `read` and `browser` screenshot tools, and decode `blob`/`uri` resource fields. Results without images keep the historical text-only shape, so existing text MCP tools are unchanged. Malformed, oversized, or excess images (capped at 4 per result, matching the ACP projection limit) degrade to a text note instead of failing the call. The image capability gate in Agent Core still decides whether a non-vision model receives images at all.

- **SQLite: Transient Busy Transaction Begins Are Retried**
  - Several processes opening one session directory race onto the single writer lock. The DSN begins non-read-only transactions with `BEGIN IMMEDIATE`, so a begin can outlast the connection's `busy_timeout` while other processes keep committing under `synchronous(FULL)`, and a healthy database failed with `database is locked (5)`.
  - The retry policy now lives in `internal/db` next to the DSN ownership: `BeginTx` (Bun), `BeginSQLTx` (raw `*sql.DB`), and `RunInTx` retry only `SQLITE_BUSY`/`SQLITE_LOCKED` inside a bounded budget (90 s) with exponential backoff (200 ms up to a 2 s cap); non-transient errors are returned unchanged and a caller's context deadline still wins.
  - DAO `Begin`/`BeginTx`/`RunInTx` (including the bindings helpers), `internal/db.Write`, and the session schema initialization/migration boundary all use it, so concurrent startup and ordinary writes no longer turn a transient writer conflict into a hard failure.

- **Workflow: Runaway DSL Scripts Are Bounded by a Wall-Clock Budget**
  - A workflow source such as `while (true) {}` could pin the process forever whenever its caller passed a context without a deadline. Source evaluation — which only builds the node graph, since worker agents run natively afterwards — now runs under two bounds: the caller's context and a wall-clock budget, whichever fires first interrupts the VM.
  - The budgets are 30 s for a workflow run and 5 s for the interactive `workflow_lint` authoring check, which must fail fast; a timeout surfaces as the sentinel `ErrJSEvaluationTimeout` (the lint result carries a stable, readable error), while a caller cancellation keeps returning its context error. `Runner.EvalTimeout` lets callers tighten the budget, and the zero value keeps the documented defaults.

- **Cancelled or Expired Decisions No Longer Block Forking**
  - A session whose only decision had actually been cancelled or timed out was still treated as having a pending decision, so forking it was rejected as `source session is active`. Every decision-ledger reader now shares one vocabulary, so cancelled and timed-out decisions (and the legacy channel request name) clear correctly and the fork proceeds. The durable decision event name and its `{"decision": …}` envelope also gained a single owner each, so cross-entry decision recovery reads the same records regardless of which surface wrote them.

### ✅ Tests

- Database: `internal/db` pins the begin-retry policy — only SQLITE_BUSY/SQLITE_LOCKED are retried, other driver errors and an expiring context are surfaced unchanged, driver codes are classified through the error's own `Code()` method, and `RunInTx` keeps commit/rollback semantics.
- Workflow: a runaway script is interrupted by a 50 ms budget (previously that test hung), a successful evaluation behaves exactly as before, and the lint path reports an invalid result with the timeout message.
- Serve: the generated template keeps the same constant the detection uses, a whitespace-padded placeholder is still recognized while a real or empty token is not, and the `serve init-config` output must contain the warning.
- Agent loop: ten real `bash echo` calls run through one parallel batch (the rendezvous only closes once all ten workers are live), and ordered-start coverage pins the launch semantics — starts in the model's declared order, an in-flight call unaffected by an earlier approval wait, queued calls released when an earlier call fails, and the same ordered handle for background tool calls.
- Runtime: the cross-process takeover test now retries the expire-then-takeover pair inside a bounded budget and gives helper startup a load-tolerant window, so a loaded machine fails setup with a clear diagnosis instead of failing the invariant under test.
- Decision ledger: round-trip and replay coverage for the shared event name, envelope, and loaders; the legacy channel event name still decodes; and a cancelled decision no longer blocks a fork.
- Member wait: an interactive (TUI) non-team lead holds its run open for a running member, while a headless (CLI) one ends normally.
- Architecture: a guard rejects new use of the legacy session run/lease APIs or a low-level `agent.New` in adapter test files, with a documented allowlist for the remaining fixtures.
- `systeminit`: the shared `/systeminit` prompt is pinned — interactive-only question guidance, trimmed extra instructions before the final note, blank extra input ignored, and determinism.
