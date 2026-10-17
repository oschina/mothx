# Changelog (Current Version)

This file contains the changes for the **current version only**. The full history of all versions lives in [docs/en/changelog.md](en/changelog.md).

## v1.3.105-pre-1

### ✨ New Features

- **Sessions reject unusable working directories early.** `internal/session` is the single owner of working-directory validation, with the Runtime API delegating to it. Session creation, reopening, rebinding, ACP `mothx/session/setWorkDir`, and channel session resolution now require an existing absolute directory before persisting or assembling sandbox and tool resources. An ACP same-directory request no longer detaches a usable runtime when its directory has disappeared, and a channel run validates the bound session's persisted cwd rather than the platform default.
- **Desktop uses an explicit draft-directory model.** The legacy workspace-default fallback and settings panel were removed. Quick-start drafts receive one process-local `mkdtemp` scratch directory, while a New Task draft remains unset until its native picker or worktree action supplies a directory. Picker history is only a convenience for the next dialog; draft state carries a generation so a stale quick-start, picker, or ACP completion cannot overwrite a newer New Task.
- **Runtime coordinates local session submissions.** `SessionRuntime` owns one `(database identity, session ID)` FIFO `SubmissionCoordinator`, and `ExecutionRuntime.Completion` is the authoritative completion barrier. TUI queued input, ACP prompt admission, Channel/Cron/ESM/Knowledge admission handoffs, shutdown, cancellation, and structured local/external busy classification now share that lifecycle instead of polling lease errors or starting a successor from an Agent finish event.

### 🐛 Bug Fixes

- **Rotating a bound channel session preserves its durable directory.** Rotation inherits the existing session's persisted cwd instead of falling back to the platform default, and its fenced compare-and-set compares canonical paths so an equivalent stored form such as a trailing separator still succeeds while a real concurrent change is rejected.
- **Feishu tool-image cleanup is concurrency-safe.** Each delivery materializes images beneath a unique private temporary directory and removes the whole directory on every exit path, preventing parallel runs from collecting one another's files and leaving no image bytes behind in the work directory.
