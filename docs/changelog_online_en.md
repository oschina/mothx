# Changelog (Current Version)

This file contains the changes for the **current version only**. The full history of all versions lives in [docs/en/changelog.md](en/changelog.md).

## v1.3.101

### ✨ New Features

- **Serve: Placeholder API Token Warnings**
  - `mothx serve init-config` ships a well-known template token, which is equivalent to publishing the API key once auth is enabled — and nothing said so. The token is now a single named constant (`serve.PlaceholderAuthToken`) with explicit detection (`IsPlaceholderAuthToken`/`UsesPlaceholderAuthToken`), and a replacement warning is printed wherever the template is created (`mothx serve init-config` and the `--init-serve` CLI path) and again on `mothx serve` startup while `api.auth.enabled` is true and the token is still in place.
  - The startup check only fires for an enabled auth block, so the default template (auth disabled, loopback only) stays quiet and a replaced token is never flagged. `serve init-config` now writes through the command's own stderr, so the warning travels with the command output.

### 🐛 Bug Fixes

- **SQLite: Transient Busy Transaction Begins Are Retried**
  - Several processes opening one session directory race onto the single writer lock. The DSN begins non-read-only transactions with `BEGIN IMMEDIATE`, so a begin can outlast the connection's `busy_timeout` while other processes keep committing under `synchronous(FULL)`, and a healthy database failed with `database is locked (5)`.
  - The retry policy now lives in `internal/db` next to the DSN ownership: `BeginTx` (Bun), `BeginSQLTx` (raw `*sql.DB`), and `RunInTx` retry only `SQLITE_BUSY`/`SQLITE_LOCKED` inside a bounded budget (90 s) with exponential backoff (200 ms up to a 2 s cap); non-transient errors are returned unchanged and a caller's context deadline still wins.
  - DAO `Begin`/`BeginTx`/`RunInTx` (including the bindings helpers), `internal/db.Write`, and the session schema initialization/migration boundary all use it, so concurrent startup and ordinary writes no longer turn a transient writer conflict into a hard failure.

- **Workflow: Runaway DSL Scripts Are Bounded by a Wall-Clock Budget**
  - A workflow source such as `while (true) {}` could pin the process forever whenever its caller passed a context without a deadline. Source evaluation — which only builds the node graph, since worker agents run natively afterwards — now runs under two bounds: the caller's context and a wall-clock budget, whichever fires first interrupts the VM.
  - The budgets are 30 s for a workflow run and 5 s for the interactive `workflow_lint` authoring check, which must fail fast; a timeout surfaces as the sentinel `ErrJSEvaluationTimeout` (the lint result carries a stable, readable error), while a caller cancellation keeps returning its context error. `Runner.EvalTimeout` lets callers tighten the budget, and the zero value keeps the documented defaults.

### ✅ Tests

- Database: `internal/db` pins the begin-retry policy — only SQLITE_BUSY/SQLITE_LOCKED are retried, other driver errors and an expiring context are surfaced unchanged, driver codes are classified through the error's own `Code()` method, and `RunInTx` keeps commit/rollback semantics.
- Workflow: a runaway script is interrupted by a 50 ms budget (previously that test hung), a successful evaluation behaves exactly as before, and the lint path reports an invalid result with the timeout message.
- Serve: the generated template keeps the same constant the detection uses, a whitespace-padded placeholder is still recognized while a real or empty token is not, and the `serve init-config` output must contain the warning.
- Agent loop: ten real `bash echo` calls run through one parallel batch (the rendezvous only closes once all ten workers are live), and ordered-start coverage pins the launch semantics — starts in the model's declared order, an in-flight call unaffected by an earlier approval wait, queued calls released when an earlier call fails, and the same ordered handle for background tool calls.
- Runtime: the cross-process takeover test now retries the expire-then-takeover pair inside a bounded budget and gives helper startup a load-tolerant window, so a loaded machine fails setup with a clear diagnosis instead of failing the invariant under test.
