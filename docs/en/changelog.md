# Changelog
## v1.2.100

### ✨ New Features

- **Independent Artifact Publishing Switches**
  - TUI/CLI, WebUI/API, Desktop/ACP, and messaging channels now have separate artifact publishing switches, all disabled by default. Each entry point enables the shared Runtime-owned `publish_artifact` path without changing the others.

- **Expert Teams Across TUI, WebUI, and Desktop**
  - Sessions can bind a reusable expert bundle with `--expert <id>` or TUI `/expert list|show|bind|unbind|switch` commands. A team bundle injects its lead identity and roster and automatically enables member dispatch; a single-persona bundle changes only the lead identity.
  - Replacing an existing expert creates a fork instead of overwriting the source session. The WebUI expert panel and Desktop ACP **Expert** option use the same Runtime-owned binding and fork path.
  - Member lifecycle projections now carry the member name, emoji, role, and expert identity for TUI, WebUI, and Desktop cards. Member completion is delivered at a lead boundary and never starts a new run by itself.
  - ESM continues only from a genuinely idle, runnable objective. Pending input, decisions, or a member terminal event cannot bypass that gate.
  - Added the built-in `expert-creater` Skill. Activate it with `/skill expert-creater` in TUI/WebUI or `/expert-creater` in Desktop/ACP, then let the current Agent create and install a validated project team under `.mothx/experts/`.

- **WebUI: Slash Command Suggestions in the Chat Composer**
  - Typing `/` in the chat input now shows a suggestion dropdown covering every supported slash command (`/clear`, `/mode`, `/model`, `/defaultModel`, `/models`, `/sessions`, `/status`, `/compact`, `/delegate`, `/alloweditpath`, `/allowautoedit`, `/workflows`, `/skill`, `/skills`, `/rule`, `/esm`, `/help`), with a dedicated subcommand filter for `/esm` (objective/edit/pause/resume/clear/guide).
  - Navigate with ↑/↓, complete with Tab or Enter (Enter sends the prompt when the input already matches the selection), dismiss with Esc, or click an entry; accepting a suggestion places the cursor at the end of the inserted command. The composer keeps proper combobox/listbox ARIA state (`aria-expanded`, `aria-activedescendant`, `aria-selected`).
  - Suggestions are suppressed while a run is active, the API is disabled, or the input spans multiple lines.

- **WebUI: Runtime-Owned Knowledge Base Management**
  - The new **Knowledge** workspace lists, creates, edits, scans, queries, and deletes directory-backed knowledge bases through the same Runtime/session services used by ACP and Desktop. Directory selection falls back to the built-in browser when a native picker is unavailable; source files and index storage remain server-owned.

- **Desktop: Streamlined Home Presets and Prompt-Filling Quick Actions**
  - Home preset tabs are shortened to Work / Code / Create (办公 / 代码 / 创作) with tightened descriptions in both languages.
  - Quick-action chips now fill the composer with a complete, ready-to-send prompt (including editable `[topic]`-style placeholders) instead of a bare label, so one click can start a real task.

- **Desktop: Development Mode (`make desktop-dev` / `npm run dev`)**
  - New dev runner: watches `renderer/src/`, `renderer/index.html`, and `renderer/styles.css`, rebuilds `dist/renderer` on change, and reloads Electron without cache so the ACP child process does not restart; `main/` and `preload/` are built once at startup and require a manual restart after edits.
  - Dev mode (`MOTHX_DESKTOP_DEV=1`) opens DevTools automatically in a detached external window (never docked inside the app), binds the Chrome DevTools Protocol to `127.0.0.1:9223` only (change the local port with `MOTHX_DESKTOP_DEBUG_PORT`), and reloads the window when renderer assets change; it does not start `mothx serve` and adds no HTTP/API channel to the renderer.
  - The dev instance runs on an isolated user data directory (`desktop/.dev-user-data/`, overridable with `MOTHX_DESKTOP_USER_DATA`) so it never competes with an installed Desktop for the single-instance lock or reuses its local display state.

- **Desktop: Online Skill Marketplace (SkillHub Catalog)**
  - New ACP feature key `manageSkillHubCatalog` and an additive `mothx/manage/skillhub/*` method family (markets/categories/official/search/detail/targets/installed/install/activate/uninstall) as a pure ACP projection of the shared SkillHub service; every request must bind to an active session, so Desktop cannot pick arbitrary install directories.
  - The Desktop skills page gains a marketplace section: market/category filters, keyword search, official recommendations, and install/update/activate-into-session/uninstall into the session's project or global skills directory.
  - ACP sessions now track multiple active skills (previously activating a new skill replaced the previous one), so several skills can stay active in the same session.

- **Desktop: App-Wide Background Image Options**
  - Background images can now apply to the whole app or the home view only, with fit modes (cover / contain / stretch / tile) and anchor positions (center / left / right / top / bottom).
  - Background veils and surface blur fade adaptively with image opacity; with an app-wide background, the titlebar gains a readable contrast surface and text shadows so window controls stay legible.
  - Appearance settings move into a standalone Appearance category.

- **Expert Team Member Questions Reach the Lead**
  - A member that needs a decision asks the lead instead of the human: the question is queued in the session mailbox, delivered as a `[MEMBER_QUESTION]` steering message (or as a `subagent_wait` entry with status `question`), and answered with `subagent_answer(handle: "<member>", question_id: "…", answer: "…")`. The user only sees the question projected on the lead's stream.
  - The mailbox path works in every session that can spawn members — multi-agent, delegate, and workflow modes — not only in team-bound sessions, and `subagent_wait` now reports pending member activity there too. Only a team-bound session holds its run open for still-running members; other sessions end the turn normally and see the notification at their next iteration or next run.
  - Answering a question that is no longer pending (already answered, expired, or belonging to another member) reports an error instead of a silent success. A blocking `delegate_subagent` child cannot ask questions, because its caller is parked inside the tool call that would have to answer it.

- **Retry Failed Message Deliveries from Desktop and WebUI**
  - Desktop's Channels settings list the failed durable deliveries of the active session — platform, operation kind, status, failure code, attempt count, and last update — with a per-row retry; WebUI exposes the same Runtime-owned facts through `GET /api/deliveries/failures` and `POST /api/deliveries/retry` in its channel settings.
  - Both entries project the Runtime's own verdict: only a failed transport-level operation is offered a retry, while in-flight, delivered, and permanently failed operations are refused instead of being reopened into another doomed attempt (the refusal they report is the same rule the persistence fence enforces).

### 🐛 Bug Fixes

- **Browser: Built-in Skill No Longer Writes into Projects**
  - Browser guidance now ships as the built-in `vibe-browser` Skill. Enabling Browser in TUI, WebUI, Desktop, ACP, or a channel no longer creates `.skills/vibe-browser/SKILL.md`; intentionally created project or global skills with that name still override the built-in guidance.

- **Channels: Browser Selection Survives Runtime Rehydration**
  - A channel session's persisted Browser selection now drives both initial registry construction and Runtime resource rehydration. Explicitly enabling Browser no longer has the tool removed after the session Runtime attaches.

- **TUI: Prompts Submitted During an Active Run Are Queued Instead of Replacing It**
  - A session allows exactly one foreground execution at a time. Previously, submitting input while a run was active replaced the in-memory run handle, orphaning the active run's terminal cleanup and its runtime lease. Such submissions are now queued in the TUI, and the next queued prompt starts only after the preceding run reaches its canonical terminal state and releases its lease — across every terminal branch (success, failure, incomplete, and cancellation).
  - Queued prompts retain their Runtime-prepared attachments (`agentruntime.PreparedInput`) and re-enter through the same input contract, so attachments survive the delay unchanged.

- **TUI: `/defaultModel` Shares the `/model` Catalog Logic**
  - The `/defaultModel` picker now resolves each provider's model list through `providerfactory.ResolvedModels` — the same factory-resolved catalog (built-in presets merged with settings overrides) that backs `/model` and the WebUI picker — instead of re-parsing raw `settings.json` models. A provider whose settings entry declares only credentials or a partial model list no longer hides the remaining built-in models.

- **Cancelled Runs Report Cancellation Instead of Success**
  - A run cancelled while it waits for its members, or while its final turn is in flight, now ends as cancelled with the canonical `aborted` reason instead of being projected as a completed run; adapters that derive their state from the terminal event (ACP/Desktop) show the cancellation the user asked for.
  - A turn whose answer was cut off by the output limit but recovered by escalation or a continuation is no longer marked incomplete: the truncation flag is scoped to its own turn instead of leaking into the next one.
  - TUI: a retired event stream keeps being drained so an aborted run can always reach its terminal bookkeeping; an aborted cached Agent is discarded before the next submission; tool rows left running by an early run end are terminalized.

- **Messaging Channels: Bounded Delivery Retry Windows**
  - Durable delivery operations retry inside a configurable window (10 minutes by default) instead of being abandoned after a fixed attempt count. Transport-level failures are retried automatically, recovered by the serve reconnect path, or reopened by an operator through ACP `mothx/manage/deliveries/retry`.
  - Permanent failures (platform 4xx, unsupported media, broken projection) stay failed: the failure projection reports a `retryable` flag, the retry entry refuses operations that are not failed or not reopenable instead of clobbering them back into `retry_wait`, and the persistence layer enforces the same fence. Operations that failed only because their dependency did recover with it.

- **Channels: A Disabled Sub-Agent Tool Stays Disabled**
  - Re-pointing the sub-agent tools at the session-scoped manager (which owns the session mailbox) no longer resurrects tools the user switched off: an ordinary multi-agent selection keeps its per-tool choice, while a team binding stays authoritative and always exposes the full team toolset.

- **Parallel Tool Calls Report Their Starts in the Declared Order**
  - A parallel tool-call batch now reports each call's start in the order the model emitted them, so argument parsing and worker scheduling can no longer make a later call appear to start first. The ordering is start-only and never blocks execution: calls still run concurrently, each waits for its own approvals and durable execution records, and completions may finish in any order. Results, transcript order, and provider continuation messages keep their existing guaranteed order, and Responses background runs use the same ordered handle.

### ✅ Tests

- TUI: new coverage asserting that input during an active run queues without replacing the lease owner, and that the queued prompt starts only after the cancellation path finalizes the durable run and releases its lease.
- TUI: `/defaultModel` coverage asserting the dialog's model list matches the factory-created provider list (the `/model` path) for both partial-override and credential-only settings entries.
- Expert Teams: Runtime binding/fork, named-member events, TUI and Serve no-direct-run guards, ACP bind/fork process coverage, Desktop projection, and cross-entry ESM idle-gate coverage.
- Channels: an all-selectable-tools contract test verifies that every available persisted tool selection is present in the resolved session registry.
- Expert Teams: member question → lead → `subagent_answer` round trip, refusal of a question that is no longer pending, mailbox ownership (session lead vs. auxiliary roles, scheduled jobs, and a team-bound ESM worker), and non-team sessions delivering member notifications without holding the run open.
- Agent loop: terminal-state coverage for a run cancelled during the member wait, and for a truncated turn recovered by a follow-up turn.
- Channels: the session mailbox shared by the sub-agent tools and the lead, plus a partial tool selection that must not be resurrected by re-registration.
- Delivery: reopen refusal for permanent failures, verified through both the operator entry and the persistence fence.
- Delivery: the WebUI endpoints cover listing, a successful retry, and the permanent/in-flight/unknown refusals; the Desktop projection covers the same verdict and its bilingual strings.
- Runtime: the orphan-recovery concurrency test now proves overlapping workers with a rendezvous instead of a timing window, so it no longer flakes under load.

## v1.2.99

### ✨ New Features

- **Unified Server-Resolved Model Catalog Across WebUI and TUI**
  - A new `GET /api/models/catalog` endpoint resolves every selectable provider/model through `providerfactory.ResolvedModels`/`SortProviderIDs` — the same shared logic that builds the TUI's provider model lists — and returns canonical defaults plus the sorted provider list. The active provider stays selectable even when it comes from a built-in preset or a serve flag instead of the settings providers map.
  - `stores.js` migrates `models` → `modelCatalog`; the Chat new-session picker now consumes the server catalog instead of merging raw settings JSON client-side, dropping the local `buildModelCatalog`/settings-fallback derivation. Settings surfaces (default provider/model dropdowns, provider editor) consume the same store, and inherited built-in preset models get dedicated zh/en labels.
  - The TUI auth dialog's provider sort delegates to `providerfactory.ProviderSortPriority`, so TUI dialogs and the WebUI catalog share one ordering logic.

- **Enable Supervisor Mode (ESM): Slash-Command Control, Shared Guidance, and Evidence Tracking**
  - WebUI ESM control moves to the same `/esm` slash command as the TUI (`/esm <objective>`, `/esm status|edit|pause|resume|clear|guide`) instead of dedicated graphical controls — the 500-line `ESMControls` component is removed, and chat input and the ESM REST API share one server-side objective path.
  - New guidance module: `/esm guide <text>` queues user guidance stamped with the objective's current version; the Supervisor injects pending guidance into every non-recovery role prompt and consumes it exactly once after the role result is applied — one core-owned lifecycle shared by the TUI and WebUI adapters.
  - New evidence module: a shared `EvidenceTracker` accumulates tool-call evidence per role run (unique tool-call IDs, per-tool counts/errors), so the "tool-backed evidence" checks in `ApplyWorkerResult`/`ApplyReviewResult` cannot diverge between adapters.
  - ESM no longer enforces token/time budgets: the `budget_limited` status, `SetBudget`/budget prompts, and the TUI `/esm budget` subcommand are removed; `TokensUsed`/`TimeUsedMS` remain observability-only counters. The blocked-audit threshold is centralized as `BlockedAuditLimit` (the objective becomes blocked after 3 consecutive runs report the same blocker).
  - Unattended derived runs resolve their execution mode through `agentruntime.ResolveUnattendedMode`: only `os` is inherited from the session mode and every other session mode falls back to `yolo`, so ESM role sub-agents never stop on interactive approval (hard high-risk-command protections remain mode-independent).
  - The Supervisor now runs continuations against the base Run ID (role runs use suffixed IDs derived from it), owns the terminal `FinishRun` call for both adapters, and clears stale streaks left by earlier continuations when a continuation ends.

### 🐛 Bug Fixes

- **ESM: Failed Objectives Pause Instead of Silently Re-running**
  - A non-retryable role failure now pauses the objective and requires an explicit `/esm resume` before it can run again — queued guidance or a future trigger can no longer silently re-run a failed task. Timeouts and retryable transport errors still take the recovery path bounded by `RecoveryLimit`.
  - Serve startup no longer replays historically persisted "active" ESM objectives: a role may have failed just before the process exited, so `Create`/`Edit`/`ResumeESM` are now the only explicit execution entry points.
  - `esmCoordinator` gains `stop`/`stopAll` with done-channels and bounded waits; Serve shutdown cancels every ESM worker and waits for it to release session/runtime references (`SessionRuntime.Shutdown` remains the final resource boundary), and a closed coordinator refuses to start new workers.

- **Native Directory Picker on Headless Servers**
  - The Unix native picker reports itself unavailable when `DISPLAY`/`WAYLAND_DISPLAY` is absent instead of failing silently, letting the Web UI fall back to its built-in directory browser. Launch failures that print diagnostics on stderr now surface as errors instead of being mistaken for a dialog cancel.

### 🔧 Improvements

- **Directory Browser: Windows Drive Roots and Path-Aware Allowed Roots**
  - `/api/browse` allowed-root resolution now takes the requested path, and Windows drive roots are listed through a virtual browse root (drive roots share no common parent for navigation). `DirBrowser` gains an `initialPath` prop, one-shot open semantics, a server-provided `selectable` flag, and refresh support.

- **Tool Recovery Audit Trail**
  - `RequestToolExecutionRecoveryRecords` records explicit user confirmation and returns only matching interrupted calls; the records are retained as audit evidence while recovery starts as a fresh execution, and a new DAO listing (`ListRequestedToolRecoveries`) exposes requested recoveries to Serve. Terminal Runs are never reactivated to consume these records.

### ✅ Tests

- ESM: new/expanded coverage for the guidance lifecycle (version stamping, injection, one-time consumption), evidence tracking, pause-on-non-retryable-failure, base-Run-ID continuations, budget removal, `/esm` slash-command parity, and coordinator `stop`/`stopAll` (including closed-coordinator start refusal).
- Serve: a process-level test asserts startup never replays historical ESM objectives; browse-root tests cover allowed-root resolution and Windows drive-root listing; native picker tests cover the headless-unavailable and stderr-diagnostic cases.
- Provider factory: catalog resolution and provider ordering tests; settings: `qwen3.8-max-0902` preset assertions.
## v1.2.98

### ✨ New Features

- **New Gitee/Moark Model: `qwen3.8-max-0902`**
  - Added `qwen3.8-max-0902` to the `gitee` and `moark` providers with a 1M context window, 128K max tokens, and text+image input.

### 🔧 Improvements

- **TUI: Dead Code Cleanup and Decision Lifecycle Alignment**
  - Removed unused functions (`renderLiveAssistantMessage`, `renderPlanPanel`, `formatPlanForDisplay`, `normalizeHistoryLineEndings`, `resolveESMStoreDir`, `updateViewportContentWithFollow`) and trimmed the associated plan/ESM test coverage.
  - Question requests are now registered through `DecisionService`, so pending questions persist and replay like approvals; duplicate question requests are rejected.
  - Terminal decision status is mapped from the actual run state instead of always marking pending decisions `cancelled` — only an explicit cancellation records `cancelled`, any other terminal outcome records `timed_out`.
  - The deferred print loop gained a `stopPrintLoop` exit path used on quit and reload so queued transcript lines are drained before teardown.
  - The external status-line refresh is deferred to actual renders, coalescing event-dense bursts into a single refresh.
  - `sessionsDel` now resolves the session directory through the defensive `getSessionDir()` helper.

- **WebUI: Settings Model Lists Match the New-Session Picker**
  - The settings "Default Provider / Default Model" dropdowns now reuse the same server-resolved catalog behind the new-session model picker (`GET /api/models/catalog`), so built-in preset providers and their default models stay selectable there too; unsaved form edits are appended after the catalog entries so they can be chosen before saving.
  - The provider settings reuse the same catalog: the sidebar model-count badge shows the resolved count, and the model list additionally renders built-in preset models that are not written to settings as read-only rows (matching the new-session picker; they are not persisted on save — add a model with the same ID to override).

### 🐛 Bug Fixes

- **Serve Responses Recovery Preserves Terminal Runs**
  - Recovering confirmed interrupted tool calls no longer changes a completed or failed durable Run back to `queued` or reattaches its terminal remote Responses task. Serve now submits a fresh, idempotent recovery message through the normal Runtime input path and starts a new local AgentLoop Run; the previous Run remains immutable.
  - Removed the recovery-only terminal-to-active Run-store APIs so future callers cannot bypass the canonical monotonic lifecycle.

### ✅ Tests

- TUI: new tests cover question decision registration (pending kind, persistence, duplicate rejection) and ESM store directory resolution.
- Serve: Responses recovery tests verify that the terminal parent is preserved, a fresh AgentLoop receives the recovery message, and repeated recovery requests reconcile idempotently to the same new Run.
## v1.2.97

### ✨ New Features

- **Online Model Discovery for Providers**
  - Provider model discovery moved into `internal/provider` as shared helpers (`ModelsEndpoint`, `ResolveSecretRef`, `DiscoverModels`) that fetch and normalize a provider's `/models` listing into `DiscoveredModel`s. The OpenAI-compatible `/v1/provider/models` and model-test endpoints now call these shared helpers instead of duplicating probe plumbing.

- **TUI: Fetch and Search Online Models in the Auth Dialog**
  - A new "Fetch Online Models" entry in the provider model list and model settings views runs discovery against the draft provider's Base URL / API type on a background command and opens an "Add Model · Online List" view where fetched models can be added to or removed from the draft. Nothing is persisted until the provider is saved, and stale results after closing the dialog or switching providers are dropped, with loading/empty/error states and zh/en labels.
  - Typing in the online list filters fetched models, ranked exact > prefix > substring across model ID and display name while preserving discovery order within the same score; Esc clears the query, and a "No models match." hint shows when nothing matches.

### 🔧 Improvements

- **Public SDK Boundary: `agent/` Stays Internal-Free**
  - The provider bridge moved from the public `agent/` package into `bootstrap/` (which external modules already blank-import); it registers the provider resolution hook plus the concrete provider factories at init time.
  - `agent.Builder` no longer pre-resolves the platform session directory (the internal builder resolves the default at Build time) and reports a clear error when a hook is unregistered; the examples now blank-import `bootstrap` instead of internal packages.

- **Session Store Integrity Hardening**
  - `DeleteSession` now prunes child tables without a `session_id` column (`delivery_operations`, `attachment_deliveries`) through their session-owned parents, so deleting a session leaves no orphaned rows.
  - Schema migrations apply in ascending version order instead of slice order; forward table references no longer depend on FK enforcement being disabled.
  - Non-terminal/terminal Run status sets are centralized in `run_store.go` as the single source of truth; SQL literals, partial unique indexes, fork, trajectory, and recovery paths all derive from them.
  - `EndConversationTurn` is idempotent for already-closed turns; conversation entry IDs grow to 64-bit; `IdentityLocks` delegates to a ref-counted lock registry, and the runtime lease bus closes its UDP listener once the last handler unsubscribes.

### 🐛 Bug Fixes

- **TUI: Instant Submit for a Lone Enter**
  - A queued Enter was treated as line-break evidence, so every quick type-then-Enter send waited the full 120ms split-paste coalescing window. The extended idle window now applies only when the queue carries real paste evidence (newline-bearing rune chunks or Enter adjacent to text); a lone deferred Enter keeps the normal 16ms window and submits immediately.

- **Missing Error Reason on Abandoned Durable Runs**
  - A background run abandoned after interrupted tool execution could reach a terminal status without persisting the reason. A dedicated annotation boundary (`RunDAO.UpdateErrorIfEmpty` → `session.AnnotateSessionRunError` → `agentruntime.AnnotateDurableRunError`) now sets the error only while it is still empty — without changing run status, reviving terminal runs, or touching active runs, keeping the first recorded reason authoritative. The Responses API abandon path persists the reason through this boundary.

- **Duplicate User Entry in the Background Run Coordinator**
  - The durable user entry appended during admission is already in replay state after the manager reload; the coordinator now matches the deterministic `RunUserEntryID` and reuses it as the continuation message instead of appending a duplicate to the transcript and the provider request. The check stays idempotent across retries, recovery, and process restarts.

- **Channel Rotation Lease Target**
  - `AcquireRuntimeForRotate` now takes the session directory explicitly from the lifecycle owner (falling back to the dispatcher's configured directory when empty), so the mutation lease and the forced-release wait target the authoritative Session instead of whatever directory the dispatcher holds.

### ✅ Tests

- Architecture: `public_sdk_boundary_test` fails if the public `agent/` package or `example/` modules import internal packages again.
- New session tests: delete integrity (no orphaned rows), ascending migration order, Run status-set consistency, ref-counted lock registry, and lease-bus listener cleanup; widened orphan-recovery timing margins under `-race`.
- Regression tests for the fast lone-Enter submit path (split-paste continuation still protected) and durable-run error annotation.

## v1.2.96

### ✨ New Features

- **Runtime Workspace Input Materialization**
  - A single front-end-neutral input contract now owns user files for every entry point (CLI, TUI, WebUI/API, ACP, WeChat, Feishu). Adapters submit source streams; `internal/agentruntime` materializes accepted files into the project workspace and the first user message declares file paths plus metadata, letting the Agent decide how and whether to read each file.
  - Images are no longer auto-converted to provider image content on ingest; input resources persist via the `input_resources` table with a Runtime-owned lifecycle (`PrepareInput`/`AttachPreparedInput`, discard/delete/cleanup, `input_resource_events`).
  - TUI `/paste-image` now submits a stream the Runtime writes; the Web UI gained attachment upload/preview in chat; ACP prompt content (text/image/file/audio/video) and WeChat/Feishu inbound media all normalize through this same ingress.

- **Lease-Based Execution Admission and Orphaned Run Recovery**
  - The legacy `TryLockRuntime`/`TryLockRuntimes` path was replaced with explicit, purpose- and run-bound runtime leases across CLI, TUI, ACP, Serve, channels, and cron: ref-counted durable lease guards (`AcquireExecutionAdmission`/`AcquireFork`/`AcquireMutations` and run binding) plus recovery/reconciliation modes for stale or orphaned runs.
  - A new `RecoveryCoordinator` converges orphaned runs lease-first through a startup scan and periodic/wake-driven retries, backed by the `session_run_recoveries` table with durable state, retry accounting, and idempotent replay.
  - The session runtime snapshot surfaces admission/recovery facts (`reserved`, `local`, `external`, `detached_remote`, `orphaned`, `recovery_failed`, `inconsistent`); the Web UI shows matching status badges and disables delete/fork while a session is busy.

- **Durable Delivery Outbox**
  - Delivery intents and ordered operations (`delivery_intents`/`delivery_operations`) with deterministic `PlanDelivery` sequences (caption/upload/send/fallback), a Runtime claim/fence/retry coordinator, terminal-state atomic commit of assistant message/Run/turn/event/intent, and service-start recovery.
  - WeChat (image/video/file) and Feishu (image/file) outbound media deliver natively through frozen transport contexts; published artifacts moved into a private store outside the work directory with integrity verification (size + SHA-256) on open.

- **Idempotent Run Submissions**
  - New `runtime_submissions` table with reconcile-on-conflict handling: submit-key conflicts reuse the existing submission instead of creating duplicates, making Run admission retry-safe.

- **New Volcengine Model: `glm-5.3-flash`**
  - Added `glm-5.3-flash` to the `volcengine`, `volcengine-agentplan`, and `volcengine-codingplan` providers with a 1M context window and text+image input; like `glm-5.3`, no default max_tokens is sent.

- **New Gitee/Moark Model: `glm-5.3-flash`**
  - Added `glm-5.3-flash` to the `gitee` and `moark` providers with a 1M context window, 128K max tokens, and text+image input.

- **New AMD Radeon Provider Support**
  - Added the `amd-radeon` vendor with Base URL `https://developer.amd.com.cn/radeon/api/v1` using the OpenAI-compatible protocol.
  - Added `DeepSeek-V4-Flash` (1M context) and `Qwen3.8-Flash-Next` (1M context) models.

### 🔧 Improvements

- **DAO-Only SQL Migration**
  - `internal/db` now owns process-wide SQLite/Bun connection lifecycle and transaction boundaries; all session, cron, stats, ESM, and delivery SQL moved into `internal/dao` persistence objects.
  - Removed the `internal/commondb` compatibility package and the delivery legacy bridge; the architecture guard enforces the DAO-only boundary with a minimal migration-owner allowlist.

- **Web UI Loading and State Stability**
  - Route views (Chat/Sessions/Stats/Cron/Skills/Settings/Login) are lazy-loaded so only the active route's chunk is fetched; lucide/bits-ui/svelte dependencies are grouped into stable vendor chunks.
  - Session runtime state (load/PATCH/polling/mode switching) moved into a unit-testable manager, and loaded history snapshots merge field by field so stale or empty persisted projections never erase live assistant text.

### 🐛 Bug Fixes

- **Cached Input Token Double Charge**
  - Usage accounting now computes uncached input tokens (`UncachedInputTokens`) instead of charging cache reads twice across Anthropic, OpenAI-compatible, and Google wire formats.

- **ListSessionRuns Connection Deadlock**
  - `ListSessionRuns` queried `input_resources` inside the `session_runs` rows loop, blocking forever on the single-connection pool and hanging TUI startup for continued sessions with run records. The outer rows are now drained before a single batched query, with a regression test asserting completion.

- **Durable-Run Terminal Event Stability**
  - `RunExecutor.Finalize` no longer publishes the terminal stream event for durable runs; `FinalizeRun` remains the single publisher after `FinishDurable` commits the assistant message, so WebUI history reloads cannot race the database write. Durable identity is recovered from the canonical Run row when the in-memory marker is gone, and already-closed conversation turns are tolerated so idempotent retries still commit the final entry and terminal event.

### ✅ Tests

- Architecture: `input_contract_guard_test` enforces the single input contract across TUI, CLI, WebUI/API, ACP, and Channel entry points.
- Extended admission/recovery tests for lease-first orphan convergence, execution snapshots, stop handling, idempotency, and cross-process lease behavior; delivery process integration tests for claim/fence/retry and coordinator recovery.
- Regression tests for cached-input-token accounting and the `ListSessionRuns` deadlock.

## v1.2.95

### ✨ New Features

- **New Gitee/Moark model: `qwen3.8-flash`**
  - Supports a 1M context window and text/image input; no max-token limit is sent by default.

- **Durable CLI Runs**
  - CLI `runPrint` now persists canonical durable runs via `agentruntime.ExecutionRuntime`, aligning the CLI path with WebUI, channels, and ACP lifecycle tracking.

- **UDP Runtime Lease Bus**
  - Added best-effort UDP `SessionLeaseBus` for local-process wake-up on runtime lease and run-state changes.
  - Uses directed loopback broadcast with deduplication; SQLite leases and durable rows remain the sole authority.

- **Per-Run Provider/Model Selection (API & Web UI)**
  - `POST /v1/responses` runs accept an optional `provider` field; qualified `provider/model` IDs are parsed and validated (with a structured mismatch error when the provider does not own the model).
  - The run executes with the requested provider's agent built through the shared `SessionRuntime`; run policy snapshots and request fingerprints now record the provider.
  - `/v1/models` now reports each model's provider.

### 🔧 Improvements

- **ClawHub Ambiguous Skill Slug Resolution**
  - When ClawHub returns `409 AMBIGUOUS_SKILL_SLUG` for a bare skill slug, the client now auto-resolves the unique exact-slug match and retries once with the resolved owner, so installing skills like `clawhub.ai/custom-mail-fresh100` works without requiring the `@owner/slug` form.
  - When matches do not identify one candidate, a descriptive error lists every candidate ref instead of exposing the raw 409 payload.

- **Unified Bun Database Access**
  - Added a shared `internal/db` SQLite/Bun connection and transaction layer, with DAO persistence objects for cron, usage statistics, ESM objectives, and channel bindings.
  - Removed the `internal/commondb` compatibility package. Shared connections and shutdown now live in `internal/db`; Bun-backed DAOs own migrated table access, and session transaction helpers use the DAO-owned Bun handle.

- **Event Broker Resync**
  - Event broker now exposes `SubscribeWithResync`; subscriber overflow closes the WebSocket so the client reconnects and replays durable SQLite cursors.

- **Runtime Lease Heartbeat**
  - Lease heartbeat now retries transient SQLite failures for a bounded interval and publishes `acquired`/`released`/`lost` notifications.

- **Session Capabilities (Sandbox/Browser/Web Search)**
  - `SessionRuntime` gains `CapabilitySnapshot`, `ConfigureCapabilities`, and `SetCapabilityOption`; browser and web-search capabilities persist via `session_capabilities` and replay on load, with core tools synchronized accordingly.
  - ACP sessions restore persisted capabilities and additional directories under the runtime lease; sandbox remains process-policy-owned.

- **Web UI Provider-Aware Model Picker**
  - New searchable `ModelPicker` component with modality icons (text/image/audio/video/file) replaces the old model menu.
  - Chat composes a provider-cascading model catalog from `/v1/models` plus configured providers, so selecting a provider narrows to its models and submits the provider with each run.

- **ACP Session Extension Methods**
  - Added `session/fork` and `mothx/session/setTitle` handling, workspace-window negotiation (`cwd`/additional directories), a cascade delete across fork lineage, and an `available_commands_update` notification.
  - Optional editor context is injected as a bounded, untrusted context block; loading historical sessions with a released runtime lease no longer rewrites persisted bindings.

### 🐛 Bug Fixes

- **Background Run Optimistic Concurrency**
  - Reloaded the shared session manager after durable admission so the background coordinator appends the user message to the new leaf instead of failing its optimistic concurrency check.

- **WeChat iLink Inbound Connection Recovery**
  - The WeChat adapter now sends iLink start/stop lifecycle notifications, reports connected only after a successful `getupdates` poll, adopts the server's long-poll timeout, and persists the iLink sync cursor across restarts.
  - Requests now identify the MothX channel version and bot agent, and malformed iLink responses are surfaced instead of being silently treated as empty polls.

### ✅ Tests

- Acquired the runtime lease in `TestResponsesRunAPIAbandonMarksInterruptedToolsWithoutRetry` before inspecting the abandoned tool record, matching the production recovery caller pattern.
- Added ACP tests: loading a historical session with a released lease must not persist defaults, directory updates under the runtime lease, and title changes on historical sessions.
- Added serve tests for per-run provider selection, provider/model mismatch, and qualified-model parsing.

## v1.2.93

### 🐛 Bug Fixes

- **GHCR Image Builds Use Go 1.27**
  - The Docker builder image is now `golang:1.27.0-bookworm`, matching `go.mod`, so GHCR packaging no longer fails with `go.mod requires go >= 1.27 (running go 1.26.1; GOTOOLCHAIN=local)`.

- **Desktop Version Follows Git Tags**
  - Desktop `package.json` keeps a `0.0.0` placeholder instead of a hardcoded release number.
  - Packaging resolves the real version from `MOTHX_VERSION` if set, otherwise the current git tag (`git describe --tags --abbrev=0`), and writes it into `package.json`, `package-lock.json`, and `mothxRuntime.version` at build time.
  - Desktop CI no longer treats a branch name as the version; it uses an explicit tag override or the checkout's git tag.

### 🔧 Improvements

- **ACP Installation Diagnostics**
  - `initialize.agentInfo` now reports the real MothX identity and build version.
  - Added session-free `mothx/doctor`, `mothx doctor --json`, and structured `MOTHX_ACP_ERROR` startup diagnostics with secret-free checks.

- **CI Test Workflow Cleanup**
  - Removed the per-commit GitHub Actions test workflow so tests no longer run on every push.

## v1.2.92

### ✨ New Features

- **Session Fork & Message Branching**
  - Sessions can now fork from any message and branch into alternative continuations; execution intent is wired through fork paths with a runtime lock.
  - Session schema and migrations, run/response stores, entry handling, ESM guidance, the background run coordinator, and the dispatcher all support forks; TUI session commands/runtime and Web UI chat/sessions views are updated.

- **WebUI Trajectory View & Session Log Export**
  - A read-only trajectory projection in the Serve Web UI organizes session messages, tool events, and run events by run/turn/step without forking Agent/Runtime state.
  - Server-side session trajectory and export endpoints live under the existing session route, and the session header gains a log download action (optionally including descendant sessions).

- **WebUI Modernization with shadcn-svelte & lucide**
  - Added Tailwind CSS v4 and shadcn-svelte style components (button, badge, card, dialog, input, switch, tabs, tooltip) with the `$lib` alias and `cn()` utility.
  - Replaced text glyphs with lucide-svelte icons across the sidebar, sessions, settings, and list editor views; added a brand logo mark, a workspace filter menu (all/projects/ungrouped), and a Ctrl+Shift+K new-chat shortcut.

- **Authored-commit Co-Author Setting**
  - A new global "authored" setting (default off) appends the MothX co-author trailer to system prompts, guiding the model to include `Co-Authored-By: MothX <harness@mothx.net>` when creating git commits.
  - Wired through config persistence, the TUI settings dialog, and the Web UI settings form with bilingual labels.

- **New Provider Models**
  - DeepSeek (anthropic + openai): added `deepseek-v4-flash-vision-exp` with 1M context, text+image input, and no default max_tokens.
  - Volcengine codingplan: added `doubao-seed-evolving` with 1M context and text+image input.

### 🔧 Improvements

- **Default Mode is YOLO**
  - New installs and empty-mode fallbacks now use `yolo` instead of `agent`: `settings.json` `defaultMode`, Serve/API `DefaultMode`, CLI/TUI/ACP/WebUI, public SDK `Builder`, and `agentruntime` policy resolution.
  - Explicit `--mode`, persisted session mode, and WeChat/Feishu forced `yolo` still take precedence. Existing configs that set `defaultMode: "agent"` are unchanged.

- **Native Directory Picker for Serve**
  - Serve now opens the operating system's native directory picker (macOS, Windows, Unix) for selecting a working directory instead of typing a path.

- **Unified Settings Components**
  - Extracted shared `SettingsField`, `SettingsSection`, `SettingsSwitch`, and `ProviderEditorDetail` components and unified settings view layout/styling across AppSettings, ServeConfig, Channels, Env, Logs, Memory, Overview, SkillHub, and WorkDir.

### 🐛 Bug Fixes

- **WebUI Sidebar & Session ID Fixes**
  - Sidebar collapsed state now persists in localStorage, with collapse/expand labels.
  - Removed the unused trajectory timeline mode, state, translations, and layout helper.
  - Fixed `AllocateSessionID` to treat "not registered in DB" as available after `sessions.db` exists, with a regression test.

## v1.2.91

### ✨ New Features

- **ACP Session Admission Control**
  - ACP prompts now acquire the shared session runtime lock and check the durable active-run row before starting, serializing the ACP entry point with the TUI, WebUI, and channels.
  - The runtime lock is held for the full lifetime of an admitted run so no other adapter can race its terminal persistence with a new run; a session with an active run is rejected with `session already has an active run`.

- **WebUI Server-Allocated Session IDs**
  - The WebUI now requests its session ID from the server (`POST /api/session-id`) instead of generating one in the browser, making session identity canonical while preserving the delayed-creation "new chat" UX.
  - Server-side allocation deduplicates against reserved and existing session IDs and expires stale reservations after 10 minutes; browser-side random ID generation remains only for run request keys.

- **Session Duplicate ID Rejection**
  - Creating a session with an ID that already exists now fails with `ErrSessionIDExists` instead of silently merging the new header into the old session and forking the conversation.
  - Auto-generated IDs retry on collision (up to 8 attempts); the session header write uses a plain INSERT so duplicate IDs are detected reliably.

### 🔧 Improvements

- **Unified Session Creation**
  - TUI, serve, and CLI now create sessions through `agentruntime.CreateSession` instead of constructing `session.New(...).Init()` directly, centralizing session creation in the shared runtime.

## v1.2.90

### ✨ New Features

- **New Gitee/Moark model: `qwen3.8-27b`**
  - Supports a 1M context window and text/image/video input; no max-token limit is sent by default.

- **ACP Session Config Options**
  - ACP now supports `session/set_config_option` and `session/set_mode` RPC methods, enabling clients to change model, mode, and thinking level per session without rebuilding the agent or provider.
  - Each session carries a consistent `configOptions` catalog in `session/new`, `session/load`, and `session/resume` results, and `session/set_config_option` notifies all connected clients of the updated options.
  - Configuration is persisted to the session history (`model_change`, `mode_change`, `thinking_level_change` entries) and replayed on session load, so bindings survive restarts and survive across multiple adapters sharing the same session.
  - Stable streamed message IDs (`messageId` on `agent_message_chunk`, `agent_thought_chunk`, and `user_message_chunk` updates) group chunks into logical messages per prompt turn.

- **ACP Additional Directories**
  - `session/new`, `session/load`, and `session/resume` accept an `additionalDirectories` array of absolute workspace roots. The tool registry resolves paths within these roots, and the sandbox mounts them as read-only (strict mode) or writable paths.
  - Directory sets are persisted as `additional_directories` session entries and restored on reload.
  - The session list endpoint (`session/list`) exposes `additionalDirectories` per session.

- **ACP Standard Elicitation Form Protocol**
  - When the client advertises `elicitation.form` in client capabilities, question requests use the standard ACP `elicitation/create` method instead of the legacy `_mothx/request_question` extension, with a typed `requestedSchema` envelope.
  - Replay gracefully falls back to the legacy extension shape when a reconnecting client does not advertise form support.

- **ACP FileDiff Protocol Projection**
  - Tool call updates now include a `diff` content type with `path`, `oldText`, and `newText` fields for semantic diff representation, alongside the existing text content. `oldText` is `null` for newly created files.
  - Tool call updates carry `locations` with the affected file path when a diff is present.

### 🔧 Improvements

- **Session Mode and Thinking Level Persistence**
  - New `EntryModeChange` and `EntryAdditionalDirectories` session entry types record mode switches and directory bindings to the session history, enabling full replay and cross-session consistency.
  - The `SessionRuntime` owns `Model`, `Mode`, `ThinkingLevel`, and `AdditionalDirectories` as session-scoped bindings, with `ConfigureSession`, `ConfigSnapshot`, `SetConfigOption`, and `SetAdditionalDirectories` methods for atomic read/write.
  - `BuildAgent` inherits session-level model, mode, and thinking level when the adapter omits overrides, ensuring consistent agent construction across ACP, TUI, and WebUI.

- **ACP Strict JSON-RPC 2.0 Validation**
  - The ACP server now enforces: `initialize` must be called before any other method, `initialize` may only be called once, empty messages are silently skipped, responses to notifications (empty ID) are suppressed, and request IDs are validated as JSON-RPC scalar types.
  - `cancel` requires a `sessionId` parameter and returns an error for unknown sessions.

- **ACP Unique Run IDs Across Connections**
  - Prompt run IDs now include a random suffix, preventing run ID collisions when ACP SDK request IDs repeat across connections.

- **CI Release Notes from Changelog**
  - GitHub release workflows now use `docs/changelog_online_en.md` as the release body instead of auto-generated release notes, ensuring releases carry the curated changelog.

- **FileDiff Enriched with OldText/NewText**
  - `FileDiff` now retains the complete file contents (`OldText` and `NewText`) for protocol projections that require a semantic diff rather than a display patch. `OldText` is `nil` when the write created a previously absent file.

- **ContentBlock File Fields Extended**
  - `FileContent` now carries `Title`, `Description`, and `Size` fields, and the ACP `resource_link` prompt type maps these fields into the provider-neutral file representation.

### 🐛 Fixes

- **ACP Missing Prompt Body Rejection**
  - Prompts with only empty text are now rejected with an `empty prompt` error instead of proceeding to the agent loop.

- **ACP Flaky Test and CI Stability**
  - Fixed wrapped thinking text assertion and flaky Go tests in CI.

## v1.2.89

### 🐛 Fixes

- **Error Message Leak Protection**
  - Model discovery errors in the Serve API no longer reflect upstream response bodies, preventing credentials, private diagnostics, or arbitrary HTML from leaking to the client. The HTTP status code alone is sufficient to explain a model-discovery failure.
  - Preflight error info in run submission now clears the `Detail` field, so raw parser/storage diagnostics are never projected through `DisplayErrorMessage` to the adapter.

### 🔧 Improvements

- **ACP Session Model Configuration**
  - ACP now parses `HARBOR_ACP_REQUESTED_MODEL`, advertises session model/mode/thinking options, persists per-session bindings, and routes agent construction through the shared Session Runtime. Standard config/mode updates, capability-gated form elicitation and `session_info_update`, stable streamed message IDs, multi-session isolation, and invalid-model errors are covered by Go stdio process tests. Harbor compatibility validation remains an explicit external check and is not part of default tests or CI.

- **Version Resolution from CI Branch**
  - The `Makefile` now prefers the `GITEE_BRANCH` environment variable for version string resolution, falling back to `git describe` and then `dev`. This ensures CI builds carry the correct release tag.

## v1.2.88

### ✨ New Features

- **ModelScope Model List Refresh**
  - Expanded the `modelscope` provider to the full 45-model catalog served by `https://api-inference.modelscope.cn/v1`: DeepSeek V4 Pro / Pro-0813 / Flash-0731, Qwen3.5-27B / 35B-A3B / 122B-A10B / 397B-A17B, Qwen3.8-27B, the Qwen3 base / Instruct / Thinking / VL / Next / Coder series, Intern-S1 / S1-mini / S2-Preview, InternVL3.5-241B-A28B, MiniMax M1-80k / M3, GLM-4.7-Flash / GLM-5.2, Step 3.5 / 3.7 Flash, Tencent Hy3, ERNIE-4.5 PT series, and more — each with per-model context, reasoning, and input (text/image/video) capabilities.

### 🔧 Improvements

- **Provider Error Detail in Run Failures**
  - `ErrorInfo` now carries a `Detail` field that preserves the bounded (4KB max) and credential-redacted provider diagnostic alongside the safe fallback `Message`. A new `DisplayErrorMessage()` combines both for adapter consumption, so run failures never drop provider context.
  - The detail propagates through ACP, channels, TUI, the Serve API (events, chat handler, run executor, run API, and session stream), and the Web UI, where localized messages append the diagnostic output.
  - `IsRetryable` now treats all 4xx/5xx HTTP status codes as retryable, with numeric status matching in error strings.

### 🐛 Fixes

- **TUI Compact View Hides Thinking**
  - Fixed an issue where compact event display rendered thinking as empty strings, hiding reasoning content from the transcript. Thinking is now rendered in both compact and full event views, so reasoning is no longer lost and switching to the full view does not duplicate it.

- **Web UI Image Preview Accessibility**
  - Image thumbnails in messages are now keyboard-activatable buttons with proper labels instead of bare clickable images, and the lightbox overlay is keyboard-focusable with Enter/Space to close, resolving accessibility warnings and improving keyboard navigation.

- **Embedded Web UI on Fresh Checkout**
  - A placeholder is now tracked in `ui/dist` so the `//go:embed` directive always matches before the production UI has been built, and the dist ignore rules were anchored accordingly.

## v1.2.87

### ✨ New Features

- **Parallel Tool Execution with Configurable Concurrency**
  - Local function and custom tool calls within one agent turn now run through a bounded parallel worker pool. A new top-level `toolExecution` setting controls the behavior: `mode` (`"parallel"` or `"sequential"`) and `maxConcurrency` (default `10`; `1` runs serially).
  - The setting is exposed in the Web UI under Settings > Tools and in the TUI under `/settings` > Behavior, applies to TUI, Web UI/Serve, channels, and ACP runs, and is inherited by sub-agents and transient agents.
  - Tool completion events may stream out of order, while provider continuation messages are restored in the original call order.

- **TUI Compact Event View**
  - Compact event display is now enabled by default for new and existing sessions. Routine lifecycle, usage, hosted-item, and compaction details stay hidden until `Ctrl+G` switches to the full event view; active tool rows are replaced by their completed result.

- **OS Execution Mode**
  - Added `os` as a bash-only, no-sandbox execution mode across the shared runtime, TUI, WebUI, channels, ACP, and sub-agents. Normal bash calls auto-execute, configured blacklist rules still require approval, and hard high-risk command blocks remain enforced.

- **New Model: `deepseek-v4-pro-0813`**
  - Added the `deepseek-v4-pro-0813` snapshot to the Gitee and Moark providers. It carries a 1M context window, reasoning support, and no default max-token override (the max-token limit is published by the vendor and not sent by default).

- **Web UI Image Lightbox**
  - Added an image lightbox overlay with keyboard navigation (Esc/arrow keys), prev/next buttons, open-in-new-tab, and an image counter.
  - Message image thumbnails are now larger with a hover zoom-in affordance, and input-area image preview cards have been restyled.

### 🔧 Improvements

- **Provider Parallel Tool Call Support**
  - Anthropic, Google, and OpenAI adapters now request parallel tool calls where supported and honor `supportsParallelToolCalls: false` for gateways that reject the flag.

- **Anthropic Tool-Choice Compatibility Flag**
  - Added the `supportsToolChoice` compat flag; Anthropic Messages omits `tool_choice` entirely when the model sets it to `false` (for gateways that reject the field), independent of parallel-tool-call controls.

- **Settings Compat Deep-Copy Fix**
  - `cloneModelCompat` now deep-copies every compatibility field (`SupportsToolChoice`, `SupportsParallelToolCalls`, hosted tools, supported includes, reasoning-effort/strict-mode flags, etc.), so resolved model configs no longer alias the original settings.

- **Channel Approval Parity for OS Mode**
  - Channel runs in `os` mode now follow the same auto-approval rules as `yolo`; high-risk bash commands still require approval.

- **TUI Bash Tool Result Display**
  - Bash tool results now show command status inline: `(running)`, `(succeeded)`, or `(failed (exit code N))`.
  - Status is derived from tool execution state, error, and exit code for accurate feedback.

- **Tool Execution Protocol in System Prompt**
  - The system prompt now declares the tool execution protocol (parallel vs. sequential and the per-batch concurrency limit) so the model's tool-call grouping agrees with the local executor.
  - A single-item fast path in the bounded parallel worker pool skips goroutine/channel overhead for the common one-tool turn.

- **Approval/Question Snapshot Delivery**
  - Runtime snapshots are now published alongside approval/question events, so clients that missed the live frame (dropped WebSocket, late subscribe) still see pending decisions via the runtime projection.
  - A post-replay runtime snapshot is sent on WebSocket subscribe/resume to close the gap between durable replay and live event forwarding.
  - The active run ID is included in 409 conflict responses so clients can reconcile their view and surface the stop control.

- **Cross-Surface CI**
  - Added a GitHub Actions workflow with Go, Web UI, desktop, and installer jobs, plus `make test-all`/`test-ui`/`test-desktop`/`test-npm`/`test-pypi` targets.

### 🐛 Fixes

- **Approval Recording Self-Deadlock**
  - Approval requests are now persisted outside `approvalMu`, so recording approval no longer blocks the Agent on its own mutex.

- **Approval Auto-Deny on Run Cancellation**
  - When a run is cancelled during approval persistence, the pending request is auto-denied with a resolution record instead of leaking a pending state.

- **Session Execution Concurrency Hardening**
  - `APISession.Execution` is now guarded by an RWMutex via `ensureExecution()`/`executionRuntime()` accessors, so background tool progress and request handlers can no longer race on the session execution.
  - Added nil-safety for the agent manager in the ESM coordinator's `RunRole` and fixed SSE `readSSE` CR/LF chunk boundary handling in the Web UI.

## v1.2.86

### ✨ New Features

- **Provider Retry for OpenAI Background Runs**
  - OpenAI Responses background run requests now carry idempotency keys and retry retryable failures with exponential backoff, honoring the configured retry policy.

- **Channel Runtime Settings Sync**
  - Channel (WeChat/Feishu) runs and sub-agents now use the same provider/model/retry configuration as the Web UI; applying settings through the serve API rebuilds the channel dispatcher runtime.

### 🔧 Improvements

- **Immediate Retry Settings Application**
  - The TUI now applies retry settings to the active provider right after saving, without requiring a restart.

- **Serve Settings API Error Reporting**
  - The serve settings API now returns HTTP 500 with the error detail when applying settings fails, instead of only logging it.

- **GLM 5.3 Replaces GLM 5.2**
  - `glm-5.2` has been discontinued by most vendors and is replaced with `glm-5.3` across all affected providers: Z.AI (`zai`, `zai-coding-cn`), Gitee AI, Moark, Volcengine AgentPlan/CodingPlan (where only the existing `glm-5.3` entry remains), Alibaba Bailian Token Plan, Huawei ModelArts, JD Plan, Baidu Qianfan Token Plan, CodePlayz (`opencode-go`), OpenRouter (`z-ai/glm-5.3`), Vercel AI Gateway (`zai/glm-5.3`), and Cloudflare Workers AI (`@cf/zai-org/glm-5.3`). All remain text-only input models.

### 🐛 Fixes

- **TUI Esc Abort Finalizes the Durable Run**
  - Pressing Esc now cancels and finalizes the durable run before the next input is accepted, and run-start failures are surfaced as an error instead of silently stalling.
  - Stale events buffered on a cancelled run's stream are ignored once a new run installs its event channel, so they can no longer mutate or interrupt the next run.

- **Web UI Stale Run Event Filtering**
  - Delayed events from a superseded run are filtered after a replacement run is accepted or after a refresh, so an abandoned stream can no longer overwrite the active run's transcript or status.
  - Run lifecycle versioning guards history reloads, runtime snapshots, response-run polling, and stop handling against out-of-date responses.

## v1.2.83

### ✨ New Features

- **Unified Agent Runtime**
  - Introduced `internal/agentruntime` as the canonical runtime layer shared by TUI, Web UI/Serve, channels, ACP, and A2A.
  - Consolidated session lifecycle, durable run persistence, decision/approval state, run events, and delivery into one path.
  - Adapters now project canonical events to their respective protocols instead of maintaining parallel state machines.

- **Web UI Project Session Management**
  - Added project-scoped session list with search, rename, and delete actions.
  - Empty or unnamed sessions are filtered from history; generated session titles are persisted.
  - Session metadata is now stored and exposed through the serve API.

- **ESM Backend Task System**
  - Added Extended Streaming Mode backend tasks with unified runtime adapters.
  - ESM objectives, progress, and recovery state now flow through the shared runtime.

- **Web UI Cookie Authentication**
  - Serve now supports cookie-based authentication for the Web UI.

### 🔧 Improvements

- **Runtime Controls**
  - Added max thinking level configuration and stabilized runtime control state across reconnects.
  - Work progress is now shown while runs are starting.

- **Error Handling**
  - Unified error handling and retry logic across all adapters.

### 🐛 Fixes

- **SkillHub Preflight**
  - SkillHub install preflight now stays read-only.
- **Session Titles**
  - Latest generated session title is preserved correctly.

## v1.1.82

### ✨ New Features

- **Interactive TUI Internationalization**
  - Added `tuilang` configuration in `settings.json` with `auto`, `zh`, and `en` modes. `auto` selects Chinese only at UTC+08:00; the `/settings` menu supports global/project persistence and applies successful changes immediately.
  - Slash command syntax remains English regardless of the selected display language.

### 🔧 Improvements

- **Unified Run Terminal Status**
  - Every agent run now emits one canonical `EventRunFinished` event with a `TaskStatus` of `success`, `incomplete`, `failed`, or `canceled`.
  - TUI, Web UI/Serve, channels, A2A, ACP, sub-agents, and workflows now classify results from this event; legacy `EventDone`/`EventError` events remain available for compatibility.
  - Cancellation caused by user abort, timeout, or context cancellation is reported distinctly as `canceled`; the TUI shows a status notice instead of a red error.
  - Event consumers now report a protocol failure when a stream closes without a terminal event, instead of treating it as a successful completion.

- **Provider Reliability**
  - OpenAI Responses streams now retry eligible failures reported as stream events.

- **Debugging**
  - Interactive TUI runs now print the one-time pprof server address to the terminal at startup when `--debug` is set; continuous provider debug output remains in `debug.log` via `VIBECODING_DEBUG_LOG_ONLY`.

## v1.1.79

### 💥 Breaking Changes

- **Workflow DSL Migration to JavaScript**
  - Replaced Emacs Lisp-based workflow DSL with JavaScript implementation using goja runtime.
  - Removed `vibeEmacsLispVm` dependency and Elisp evaluator.
  - Provided built-in functions: `agent`, `phase`, `parallel`, `series` for workflow definitions.
  - **Breaking**: Workflow DSL syntax changed from Elisp to JavaScript. Users need to update their workflow definitions accordingly.
  - Preserved existing workflow semantics and API compatibility.

### 🐛 Bug Fixes

- **Channel Run-State Synchronization**
  - Fixed channel (WeChat/Feishu) run-state desynchronization: cancelling a channel run now also aborts the agent (matching the background runtime), so waits that ignore context cancellation can no longer hold the session runtime lock forever and make `/new` report a phantom active run.
  - Root-fixed lost sub-agent terminal events: `AgentManager` now exposes terminal lifecycle listeners, and the channel dispatcher maps each run's root agent to its session so `done`/`error` states of children that outlive the parent event stream (async `subagent_spawn`, run cancellation, forced rotation) are still delivered to observers instead of leaving sub-agents stuck on "running" forever; the external sub-agent sink deduplicates terminal events delivered through both the stream and the listener.
  - Fixed a test-fixture race where channel sub-agent test providers dispatched responses by global call order while the parent follow-up call and the child's first call race; providers now route by request content, making the sub-agent observer tests deterministic.

### ✨ New Features

- **Channel Run Management**
  - Added a channel run watchdog: runs with no agent events for `agent.run_stale_timeout_secs` (default 600s) or exceeding `agent.run_max_duration_secs` (default 4h) are force-stopped and their persisted run state converges to a terminal status.
  - Added the channel `/stop` command, `/new force` and `/clear force` (cancel the active run, wait a grace period, then rotate), a busy hint for non-forced rotation, run-state details in `/status`, and a queued-message notice when a previous message is still executing.
  - Channel rotation no longer blocks on the session runtime lock in the dispatcher fallback path; both rotation paths now share the same try-lock + force semantics.
  - Durable background polling now has a hard cap (`api.backgroundRunMaxSeconds`, default 6h) in both the live and post-restart recovery loops, so a remote run that never reaches a terminal state releases the session runtime lock as `incomplete`.
  - Agent question prompts now also return on context cancellation, so unattended runtimes never block a run forever waiting for an answer.

## v1.1.78

- Background `Idempotency-Key` submissions now record a non-sensitive request fingerprint in existing run events; reusing a key with a different message returns an explicit conflict while compatible retries still reuse the original run, with no schema change.
- Tool execution record reuse now verifies session/turn/provider/tool/argument identity and rejects execution-key collisions instead of reusing a potentially unsafe result.
- Tool result writes are now state-conditional, so late results from an old process cannot overwrite an abandoned or newly recovered record.
- The TUI now discovers non-terminal durable background runs from the shared session database and replays assistant text after the submission boundary when they finish, preserving results across restarts.
- Channels background completion events now retain the canonical assistant entry and a pending-delivery marker; after a dispatcher restart, the next inbound message reconciles the final text/attachments and records delivery to prevent duplicates.
- Restricted channel background tool start/end summaries are now persisted in run events and replayed in order before the final result after a restart.
- Channels now also receive restricted tool status through the existing `ProgressFunc` during live background execution, with run events remaining the reconnect fallback.
- Responses attachment normalization and the WebUI now reject localhost, private, loopback, and link-local HTTPS URLs while retaining provider references for audit.
- Added an optional provider-specific file resolver: Serve only downloads file refs archived in the session, and the WebUI exposes a file attachment download action without proxying arbitrary URLs or refs.
- Added a regression test proving the attachment route accepts an optional non-OpenAI resolver without changing the common Provider interface.
- OpenAI Responses hosted items now use a package-local descriptor registry for capability, resume, and attachment policy; unknown upstream types remain archived without guessed execution or downloads.
- Background Responses `incomplete` runs now preserve partial text, attachments, and `incomplete_reason` instead of being incorrectly converted to `failed`.
- Background poll/recovery now reuses native replay for explicit remote-state invalidation (permission, 404/410, lineage, or expiry); ordinary 429/5xx errors do not trigger fallback, and each local run is replayed at most once automatically.
- Code Interpreter now accepts MothX-private `mothx.maxCalls`/`mothx.timeoutSecs` inside the existing hosted config map; these fields are stripped before upstream requests, quota-limited background results remain `incomplete`, and timeouts cancel the runtime.
- Remote MCP hostnames now receive a confirmatory DNS private-network preflight; only confirmed private/loopback/link-local resolutions are rejected, while DNS failures/timeouts remain allowed and upstream egress remains authoritative.
- Code Interpreter file citations now retain `container_id` provenance and use the OpenAI container-file download endpoint; ordinary file refs retain their existing behavior.
- The WebUI now reads Responses attachment-download capability from `/api/capabilities`; it hides downloads only when the server explicitly reports unsupported, preserving unknown-provider availability.
- WebUI capability loading now degrades independently, so older servers or unavailable capability APIs do not block session and chat data loading.
- `/api/capabilities` now also exposes provider-neutral `attachmentDownload`, so non-OpenAI providers can advertise download support through the optional resolver.
### 🔧 Improvements

- **Responses hosted lifecycle and recovery auditability**
  - Hosted item added/done lifecycle now flows through the provider, agent, Serve, TUI, channels, WebUI, and public SDK; states are available over SSE/transcript and replayed through run events after reconnect.
  - Hosted state projections use a shared field allowlist, scalar checks, and length limits. Interrupted tool execution is reported as `interrupted` instead of an ordinary failure or implicit retry.
  - Tool recovery records whether it was automatic read-only recovery or explicitly user-confirmed; channels use scoped idempotency keys when a platform provides a stable message ID.

### 🧪 Tests

- Added hosted lifecycle fixtures, capability-profile, recovery-audit, cross-entry event-bridge, and race-test coverage.

### ✨ Features

- **Web UI MCP Configuration**
  - Added global MCP configuration editing in Settings and project-level MCP configuration from the active session, using the shared `mcp.json` schema.
  - Supports `stdio`, streamable HTTP, and legacy SSE transports, including command arguments, request headers, environment variables, templates, validation, and atomic persistence.
  - Newly created Serve sessions load the saved project/global MCP configuration and register MCP tools before the agent prompt is frozen.

- **Native OpenAI Responses Web Search**
  - OpenAI Responses providers now expose the native hosted `web_search` capability automatically, without requiring the MothX-local web-search setting.
  - Native provider tools are kept distinct from the configurable local search toggle and are deduplicated when both resolve to the same upstream hosted tool.

- **Large Tool Result Pre-Compaction Summaries**
  - Tool results exceeding the large-result threshold are summarized independently before conversation-wide compaction, preserving file paths, identifiers, commands, errors, decisions, and other continuation-critical facts.
  - Summaries run with bounded parallelism and retry transient rate-limit failures, reducing context pressure while keeping the original tool-result ordering and identity intact.

### 🔧 Improvements

- **Provider Stream Idle Timeout and Automatic Retry**
  - Streaming HTTP clients no longer impose a hard 30-minute wall-clock limit. They now time out only when the upstream stops sending data, so active long-lived SSE streams can continue indefinitely.
  - Agent turns that time out before producing visible output automatically retry up to twice, with progress surfaced to messaging channels and a friendly final error when retries are exhausted.

- **Compaction Timeout and Configuration Cleanup**
  - Compaction no longer uses a separate five-minute deadline; provider streaming timeout and caller cancellation remain authoritative.
  - Removed unused idle-compression settings and UI controls. Compaction now runs in response to context pressure or explicit requests only.

### 🧪 Tests

- Added coverage for stream idle-timeout handling, automatic retry and recovery, large tool-result compaction, rate-limit retries, and the updated compaction behavior.

### ✨ Features

- **Channel Sub-Agent Visibility in the Web UI**
  - Channel-owned sub-agent events are now bridged into the Web UI session runtime, so live progress, tool calls, results, completion states, and transcripts are available through the existing sub-agent views and APIs.
  - Added `/help` to channel commands and synchronized channel sub-agent lifecycle updates without transferring runtime ownership from the channel dispatcher.

### 🔧 Improvements

- **WeChat Message Delivery**
  - Long replies are now split safely at UTF-8 boundaries and include the remaining-delivery count. When the per-message push limit is reached, additional chunks are queued and can be fetched with `/more`.
  - Progress messages share the same delivery budget, and inbound WeChat message IDs are preserved for channel-scoped idempotency and delivery tracking.
  - Truncated HTTP/SSE responses (`io.ErrUnexpectedEOF`) are now treated as retryable provider failures.

### 🧪 Tests

- Added channel sub-agent integration, external sub-agent HTTP, dispatcher lifecycle, MCP editor, session view, WeChat chunking/delivery, and truncated-stream retry coverage.

## v1.1.77

### ✨ Features

- **`disableSamplingParams` Per-Model Compat Flag (Default: On)**
  - Added `compat.disableSamplingParams` (tri-state) to model config. It defaults to on: `temperature`/`top_p` are no longer sent to any model unless the model explicitly opts in with `"disableSamplingParams": false`.
  - Editable in the TUI (`/auth` → model → E. Compatibility → Disable Sampling Params, cycles auto → enabled → disabled) and in the Web UI settings model table (Sampling column, checked = send params).
  - Note: OpenAI-compatible clients that pass `temperature`/`top_p` to the serve API now need the model to opt in for those values to reach the provider.

### 🔧 Improvements

- **Web UI Historical Session Sorting and Timestamps**
  - Session lists are now sorted by last-used time in descending order, with the most recently replied-to session at the top.
  - The Web UI session-management page shows each session's last reply time; the sidebar history keeps the newest-first ordering without displaying an extra timestamp.

- **Sampling Params Suppressed for Thinking/Reasoning Models**
  - Anthropic: `temperature`/`top_p` are now dropped automatically when extended thinking is enabled, matching the API requirement that rejects sampling parameters alongside `thinking`.
  - OpenAI: chat-completions requests using OpenAI-style `reasoning_effort` and Responses-API requests with a `reasoning` block now omit `temperature`/`top_p`, which OpenAI reasoning models reject.

### 🧪 Tests

- Added coverage for sampling-param suppression across Anthropic, OpenAI (chat completions + Responses), and Google providers, including default-drop, explicit opt-in pass-through, and deepseek-format retention cases.

### 🐛 Fixes

- **Web UI Session Mode Updates Stay Responsive**
  - After changing a session's runtime mode or capabilities, the Web UI now applies the authoritative runtime response immediately instead of waiting for the session-list refresh to complete. The local session cache is updated at the same time, and a temporary refresh failure no longer leaves the mode controls blocked or visibly stale.

- **Long sessions stuck permanently after context-window overflow (notably WeChat/Feishu channels)**
  - When a provider rejects a request for exceeding the context window (possible when the token estimate underestimates real usage, e.g. Chinese-heavy chats), the agent now retries once after LLM compaction; if the summarization request itself overflows, it falls back to deterministic truncation (dropping the oldest messages, cutting only at user/assistant turn boundaries) instead of failing every subsequent message with `responses stream failed` until `/new`.
  - The context-guard error path (local estimate exceeds the input budget) uses the same recovery.
  - The truncation is persisted as a compaction entry so channel sessions (which rebuild the agent per message) do not reload the overflowing history.
  - OpenAI Responses API `response.failed` events now parse error details nested in `response.error` (where servers like Kimi put the failure reason) instead of showing only a generic `responses stream failed`; channel sessions also forward compaction/recovery progress to messaging platforms.

### ✨ Features

- **Complete OpenAI Responses Runtime**
  - Expanded `api: "openai-responses"` from basic streaming support into a full Responses runtime: native response-item replay, `previous_response_id` and conversation state modes, reasoning context/mode, prompt-cache controls, service tier, structured output, hosted tools, and tool-call controls are validated against the selected model's compatibility capabilities before requests are sent.
  - Responses are archived as native items and associated with local turns, preserving reasoning, citations/artifacts, tool inputs, and response lineage across session replay. Tool execution is idempotent per response turn, preventing duplicate execution when providers repeat function-call items.
  - Provider `responses.background: true` enables durable remote background Responses runs in Serve. MothX polls and resumes them after restart, executes local function/custom-tool calls, persists lifecycle events and results, and supports run inspection, cancellation, reconnect, and abandonment through the Responses run API.

- **`-P --json` Print-Mode Streaming NDJSON Output**
  - Adds a `--json` flag for use with `-P` (print mode) that streams the response as JSON Lines / NDJSON — one JSON object per line on stdout, so consumers can read events as they arrive instead of waiting for the run to finish.
  - Each agent event becomes its own line: `start` (provider/model/mode), `text_delta`, `think_delta`, `tool_call`, `tool_execution_start`, `tool_execution_end`, `tool_result` (with `name`/`arguments`/`result`/`error`/`diff`), `plan_update`, `usage` (tokens and cost), `context_usage`, `compaction_start`/`compaction_end`, and a terminating `done` or `error` event that signals stream completion. All progress, debug, and diagnostic output still goes to stderr so stdout stays pure NDJSON.
  - Useful for CI, automation scripts, and integration with external programs. To reassemble the assistant text, accumulate every `text_delta` line: `mothx -P --json "summarize" | jq -r 'select(.type=="text_delta").text' | tr -d '\n'`. To inspect the whole stream as typed objects: `mothx -P --json "summarize" | jq -s '.'`.

- **Web Search Availability in Serve Status and Capabilities**
  - The serve API now reflects web search availability from both `serve.json` and app-level `settings.json` in `/api/status`, session capabilities, and default session creation.
  - Web UI tool toggles and status snapshots stay in sync after saving settings or serve config changes.

- **Desktop Release Workflow**
  - Added `.github/workflows/desktop-release.yml` for building and publishing desktop packages on tag push.
  - Supports macOS code signing via `CSC_LINK` (base64 or file path), Windows, and Linux builds with `electron-builder`.
  - Generates SHA-256 checksums and uploads artifacts to GitHub Releases with prerelease detection and auto-generated release notes.

- **New Model: `deepseek-v4-flash-0731`**
  - Added the `deepseek-v4-flash-0731` snapshot to the Gitee and Moark providers. It carries a 1M context window and reasoning support (the max-token limit is published by the vendor).

### 🔧 Improvements

- **Responses Reliability and Retry Handling**
  - Responses stream failures are now classified for the shared provider retry policy rather than failing immediately. Cloudflare HTTP `524` origin timeouts are also retryable, improving resilience for transient upstream and gateway failures.

- **Web UI Settings Polish**
  - Refreshed settings views after saving provider, app, and serve config changes so feature flags and runtime status update immediately.
  - Added tool toggle title hints in the chat composer for clearer discoverability.

- **Desktop Build Hardening**
  - Increased Electron install retry attempts and added mirror fallback (`ELECTRON_MIRROR` → `npmmirror` → default) for CI reliability.
  - Fixed ESM path handling in build scripts and corrected macOS Electron executable path detection.
  - Added `version:set` script to sync `package.json`, `package-lock.json`, and runtime version metadata from the release tag.

- **Serve Session Creation Consistency**
  - Unified web search capability resolution across `getOrCreateSession`, `defaultSessionCapabilities`, and channel runtime status snapshots.

- **Channels Session Picker as Native Select**
  - Replaced the custom session dropdown in the Channels settings with a native `<select>` control, improving accessibility, keyboard navigation, and mobile behavior.

### 🧪 Tests

- Added coverage for web search availability from `settings.json` and serve config in `getOrCreateSession` and status handlers.

### 🐛 Fixes

- **Responses Tool Calls and Channel Session Picker**
  - Deduplicated native Responses function/custom-tool calls within each response turn, so repeated upstream items do not re-run a tool while legitimate identical calls in later turns still execute.
  - Fixed the Channels settings session picker visibility when switching channel configuration.

- **Web UI Chat and Background Runs**
  - Fixed background Web UI runs starting without persisted conversation history. Submitted runs now replay the session before invoking the provider, including after a runtime mode switch.
  - The submit-run request now honors optional `images`, `tools`, and `skills`: images are validated against model input capabilities, `tools` is treated as the authoritative session capability set, and unknown tools or skills return `400`. An explicit `mode` is persisted for later runs.
  - Refreshed sessions now remain busy while a server-side run is queued, running, cancelling, or terminalizing. Lifecycle events received from replay and live WebSocket streams are normalized consistently, so the Stop action and run state remain correct after reconnect.
  - Removed the redundant tool feed from the chat view; tool activity remains available through the session event and tool result views. The composer now shows a running-state placeholder while a run is active.

- **Web UI Settings and Model Picker**
  - Removed the redundant feature-toggle card from the settings overview; feature configuration remains available in Serve Config.
  - Fixed the default-model search selector reopening after an option is selected. The picker now closes reliably before the selection update and prevents focus from reopening it.

## v1.1.76

### ✨ Features

- **Empty Provider Response Detection & Retry**
  - The agent now detects empty responses from providers and automatically retries the request, avoiding dialogue interruptions caused by transient network or upstream issues.

- **vibe-browser v0.1.5**
  - Upgraded the browser automation skill with improved page interaction and screenshot capabilities.

### 🔧 Improvements

- **`insert` Tool Rendering in Web UI and Serve Surfaces**
  - Web UI now renders `insert` tool calls with a dedicated call view showing the target path, structured position (head/tail/before_line/after_line + line number), content size, and `dry_run`/`dedupe`/`create_if_missing` flags.
  - Serve API collapsed-mode tool formatting now renders `insert` diffs (previously only `edit`/`write`); messaging channel progress lines also include the `insert` path.

- **Unified Provider Preset Fallback**
  - Consolidated preset fallback logic across `ResolveKey`, `ResolveProviderHeaders`, and factory to ensure consistent provider detection and header injection behavior across all entry points.

- **Tencent HyPlan: Removed `hy3-preview`**
  - Dropped `hy3-preview` from `tencent-hy-plan` and `tencent-hy-plan-anthropic`; Tencent Cloud plan now only offers `hy3`.

- **Documentation Image Optimization**
  - Converted architecture, comparison, and mode promo images to WebP format, reducing documentation bundle size and improving load times.

### 🐛 Fixes

- **GLM-5.2 and Kimi K2.7 Code Are Text-Only Models**
  - Corrected the capability flags for `glm-5.2` and `kimi-k2.7-code` (including the `kimi-k2.7-code-highspeed` variant, the Fireworks `accounts/fireworks/models/kimi-k2p7-code` and `accounts/fireworks/routers/kimi-k2p7-code-fast` routers, and the routed IDs `moonshotai/kimi-k2.7-code`, `zai/glm-5.2`, `@cf/moonshotai/kimi-k2.7-code`, and `@cf/zai-org/glm-5.2`) across every provider that advertised them as multimodal. These models only accept text input, so their `Input` capability is now `text` only and image/attachment payloads are no longer routed to them. Updated `internal/config/settings.go`, `docs/provider-model-list.md`, and `docs/models.md` accordingly. The `glm-5v-turbo` vision model and other genuinely multimodal models are unchanged.

## v1.1.75

### ✨ Features

- **Output Token Truncation Auto-Recovery**
  - When the model hits the output token limit, the agent automatically retries with an escalated max_tokens cap (up to the model's native output limit), then falls back to a resume-from-suffix recovery loop (up to 3 attempts) that tells the model to continue mid-thought without repeating what was already written.
  - A conservative default output cap of 8192 tokens is used for all known models to avoid excessive token usage; user-explicit max_tokens values are always preserved exactly. Retries and truncation are surfaced as status events in TUI, serve API (SSE), and Web UI.

- **Merged Built-in + Custom Model Lists**
  - Provider model lists now merge built-in presets with user-configured models instead of replacing them entirely. Custom models take precedence by ID; built-in models not configured explicitly remain available with their full preset parameters.

### 🔧 Improvements

- **Legacy Config Migration Removal**
  - Removed the one-time `.vibe` → `.mothx` and `.vibecoding` → `.mothx` auto-migration logic. Project path helpers now live in `internal/config/paths.go` without migration side effects.

- **TUI and Web UI Truncation Status**
  - The TUI shows a warning when a response was truncated by the output token limit, and a status line when an auto-retry is in progress.
  - The Web UI displays retry/status events from the SSE stream in the chat run event list.
  - The serve API reports `finish_reason: "length"` for truncated completions instead of always `"stop"`.

- **`insert` Tool Rendering in Web UI and Serve Surfaces**
  - The Web UI now renders `insert` tool calls with a dedicated call view showing the target path, structural position (head/tail/before_line/after_line + line), content size, and `dry_run`/`dedupe`/`create_if_missing` flags, with a content preview mirroring `write`.
  - Serve API collapsed tool formatting now renders the `insert` diff (previously only `edit`/`write`), and messaging channel progress lines include the `insert` path.

- **Tencent Hunyuan Plan Model List**
  - Removed `hy3-preview` from `tencent-hy-plan` and `tencent-hy-plan-anthropic`; the Tencent Cloud plan now exposes only `hy3`.

## v1.1.74

### ✨ Features

- **Structured Text Insertion Tool: `insert`**
  - Added a dedicated insertion tool for adding content to the beginning or end of an existing UTF-8 text file, or before or after a specific line.
  - Supports boundary newline handling, `exact`/`trimmed`/`line` deduplication, `dry_run` previews, structured insertion metadata, and diffs.
  - Files larger than 32 MiB use streaming insertion with file locking, UTF-8/binary validation, concurrent-modification checks, `fsync`, and atomic rename to protect the original file.
  - Keeps a clear boundary with `edit`: text matching, replacement, and deletion remain `edit` responsibilities; `insert` does not provide match, regex, or occurrence modes.

### 🔧 Improvements

- **Volcengine Doubao Model Update**
  - Removed `doubao-seed-2-0-code` and `doubao-seed-2-0-pro` from AgentPlan / CodingPlan; added `doubao-seed-2.1-turbo`.
  - Added `doubao-seed` thinking format: `reasoning_effort` with `minimal` (no thinking), `low`, `medium`, `high` (default) for Doubao Seed 2.1 / Evolving models. Auto-detected by model ID.

- **npm Publish Proxy Visibility**
  - The npm publish-if-needed script now logs `use [proxy ...]` when it uses `all_proxy`, `https_proxy`, or `http_proxy` (including uppercase variants); proxy credentials are redacted from the log.

### 🐛 Fixes

- **Windows SQLite Session Paths**
  - Fixed Windows drive-letter paths being serialized as an invalid SQLite file URI authority, preventing `invalid uri authority: C:` errors when creating sessions.

## v1.1.73

### ✨ Features

- **MCP PATH-based Command Resolution and Robust Client Lifecycle**
  - stdio MCP server commands no longer require an absolute path; commands are resolved through `PATH` with proper environment merging (case-insensitive on Windows, no duplicates).
  - Added an inbound request queue with backpressure for sampling and notification messages.
  - JSON-RPC response IDs are now validated against request IDs; mismatched responses are rejected.
  - HTTP response body parsing is bounded to 16 MiB; SSE multi-line data payloads are joined with newlines.
  - Client lifecycle uses per-client context with cancellation; `Close` cancels inflight requests and closes idle HTTP connections.
  - Resource and prompt discovery errors (non method-not-found) now fail `ConnectServers` instead of being silently ignored.
  - MCP config files are now written atomically via temp file + rename with `0600` permissions.
  - ACP new-session MCP failures now roll back the persisted session.
  - SSE `messageUrl` values are validated to be `http`/`https`; arbitrary schemes are rejected.

### 🔧 Improvements

- **Unified Session Schema Management**
  - Replaced incremental migration logic (`migrations.go`) with a unified full schema definition in `schema.go`, using `EnsureCurrentSchema()` for idempotent initialization.
  - Implemented robust SQLite connection handling with WAL mode and busy timeout.
  - Added `CloseDatabases()` for proper cleanup on exit and reload, and `OpenStandaloneDB()` for caller-owned database connections.
  - Fixed the cron scheduler goroutine lifecycle with a `WaitGroup` to prevent premature exit.

- **Web UI Approval and Session Cancellation**
  - Cancelling a running Web UI session now aborts its active Agent, including approval waits that have not yet reached the pending queue, preventing sessions from remaining stuck in the running state.
  - Approval requests and terminal decisions are scoped to their session and run, persisted in `session.db`, and unresolved approvals are recorded as cancelled when their run ends.
  - The approval panel automatically selects the first pending approval and clears stale approval state when the queue becomes empty.
  - After refreshing or reopening a running session, the Stop button beside Send can still cancel the server-side run; once termination completes, the session can start a new conversation.

- **Installer Package Versioning**
  - Bumped the npm and PyPI installer packages and all platform-specific npm packages to version `1.1.72`.

- **Web UI Mobile Responsive Adaptation**
  - Sidebar collapses into a mobile drawer (hamburger menu pattern) on screens ≤ 900px, using Svelte-native `matchMedia` store + `{#if}` conditional rendering + `transition:fly`/`fade` animations; overlay and drawer only exist in the DOM when open, preventing click-blocking.
  - Stats page adapts to mobile: KPI grid switches to 2 columns, trend/rank grid stacks vertically, chart padding adjusts for narrow screens, and the recent-requests table renders as a card list.
  - Sessions page converts from table to card list on mobile, with compact metadata rows and inline action buttons.
  - Composer input adapts to mobile: controls wrap naturally without scroll containers (preventing select dropdown clipping), textarea uses 16px font to avoid iOS auto-zoom, and runtime/skill popup panels use `position: fixed` bottom-sheet pattern to prevent screen overflow.
  - AGENTS.md updated with WebUI-specific conventions documenting the Svelte-native mobile approach.

## v1.1.72

### ✨ Features

- **Expanded Project Skill Locations**
  - Project-local skills can now be loaded from `.agents/skills/<name>/SKILL.md`, alongside existing `.mothx/skills`, `.skills`, and `skills` locations.

- **Web UI Chat and Skill Controls**
  - Added a per-session active-skill picker in chat. Selected skills are sent with the completion request and immediately refresh the session context.
  - Added server-side session-run cancellation, so the Web UI Stop action also works after reconnecting or refreshing the page.
  - Streaming failures now emit structured SSE error events and appear in the chat transcript with failed-run status.
  - Added collapsible, syntax-highlighted code blocks with copy buttons in Markdown messages, skill references, and edit previews.

- **SkillHub Installation Targets**
  - The Web UI now lets users select an explicit project skill directory or the global skills directory before installing individual skills or skill sets.
  - Added SkillHub APIs for listing installation targets and replacing a session's active skill set.

### 🔧 Improvements

- **Runtime Sandbox Configuration**
  - Applying Serve configuration updates now refreshes the API server's sandbox manager and every live session's sandbox and tool registry without a restart.
  - Sandbox managers are now isolated by API session work directory, so alternate allowed work directories and their sub-agents retain the correct policy.

### 🐛 Fixes

- **Sandbox Git and Device Compatibility**
  - Git metadata remains visible to sandboxed commands when Git protection is enabled, preserving normal Git operation while retaining one-time Git access handling.
  - Avoided rebinding host `/dev` paths into Bubblewrap, preventing tools such as Git from receiving unusable read-only device files.
  - Corrected Linux `/home` deny-rule normalization so projects located below `/home` are not rejected by the default sandbox policy.

## v1.1.71

### ✨ Features

- **Expanded Provider Model Lists**
  - Synchronized `docs/provider-model-list.md` with `internal/config/settings.go` to reflect the latest model availability.
  - Added significant support for new models across multiple providers including:
    - **OpenAI**: Added `gpt-4o-2024-05-13` variant.
    - **Google Gemini**: Updated model list and counts.
    - **Gitee/Moark**: Added a dedicated section and expanded model list with `glm-5` series, `qwen3.x` series, and `kimi-k2.x` series.
    - **Alibaba Bailian**: Added `qwen3.8-max-preview`, `qwen3.7-plus`, and `glm-5.2` to Token Plan.
    - **Tencent Hunyuan**: Added `hy3` and `hy3-preview` models.
    - **Baidu Qianfan**: Added `Token Plan` subsection with expanded model support (`deepseek-v4`, `glm-5.2`, `kimi-k2.6`, `ernie-5.1`).
    - **Kimi Coding**: Added explicit `ThinkingFormat: kimi` support.
  - Updated the Quick Reference table to reflect accurate model counts and the new `kimi` thinking format.

### 🐛 Fixes

- **Explicit Zero max_tokens Support**
  - Fixed `maxTokens: 0` in model configuration being indistinguishable from an omitted value, which prevented users from disabling the output token limit.
  - Added `fieldSet` tracking to `ModelConfig` so explicitly set zero values are preserved through JSON marshal/unmarshal and correctly propagated to providers.
  - Anthropic provider sends `max_tokens` as `*int` with `omitempty`. Because the Anthropic Messages API requires `max_tokens` and rejects values above the model's output limit, an explicit zero falls back to the default of 16384 instead of omitting the field; OpenAI/Google-style endpoints omit the field and honor the disabled limit.
  - Updated `ResolveMaxTokens` and serve API handler to respect explicit zero and skip the fallback default; negative client-supplied `max_tokens` values are normalized to zero first.
  - Added TUI model editor support to preserve explicit zero max_tokens through the edit state round-trip.

- **Web UI Session History and Failure Visibility**
  - Fixed newly created Web UI chat sessions not appearing in the sidebar/history list immediately when starting from the default session.
  - Added optimistic session-list updates while a new Web UI session is starting, then reconciles with the persisted session list from the server.
  - Web UI task failures now appear directly in the chat transcript with the failure reason, including failed completion requests, session stream errors, and failed run events.
  - Added visual error styling for failed assistant messages so stopped/failed tasks are easier to identify.

## v1.1.69

### ✨ Features

- **Web UI Session Runtime Controls and Approvals**
  - Added per-session runtime controls for switching between `plan`, `agent`, and `yolo` modes without restarting Serve.
  - Added live capability state for browser, web search, delegate, multi-agent, workflows, and A2A master tools, including availability and disabled-reason reporting.
  - Added a Web UI Approval Center for pending bash, file write/edit, delete, and Git access requests, with one-time approval/denial and persistent command/path allow rules.
  - Added approval and tool execution events to the session stream, runtime snapshots, run-event audit records, and reconnect/session recovery handling.

### 🔧 Improvements

- **Kimi K3 Support**
  - Added Kimi K3 to the built-in Kimi and Kimi Coding provider model lists with 1M context support.
  - Added Kimi reasoning-level mapping for `low`, `high`, and `max` reasoning effort values.

- **Web UI and Stats Branding**
  - Added the MothX small favicon to the Web UI, Stats dashboard, and documentation assets.
  - Restored Web UI approval audit history from persisted session run events.

## v1.1.68

### 🐛 Fixes

- **Sandbox Isolation and Policy Consistency**
  - Applied configured sandbox policies to Linux Bubblewrap, including custom binary paths, network access, extra read/write paths, denied paths, environment-variable pass-through, and temporary filesystem size.
  - Isolated OpenAI-compatible API sandbox managers per session work directory, ensuring allowed alternate work directories are correctly mounted and sub-agents inherit the same restrictions.
  - Fixed Channels sub-agents bypassing an enabled sandbox by reusing each session's configured sandbox manager.
  - Prevented configured denied paths from being rebound through additional sandbox path options, and improved sandbox availability error messages.

### ✨ Features

- **SkillHub / ClawHub Marketplace Integration**
  - Added a built-in skill marketplace for browsing, searching, inspecting, viewing files and security evaluations, installing, updating, uninstalling, and activating SkillHub.cn / ClawHub.ai skills from the TUI (`/skillhub`) and the `mothx serve` Web UI.
  - Added a skill marketplace cache and registry factory for extensible market support; serve mode now exposes market, search, detail, install, skill-set, and session activation APIs.
  - The installer validates archive size, path traversal, absolute paths, Windows drive paths, and symbolic links, while protecting hand-written skill directories from being overwritten.

### 🔧 Improvements

- **Sandbox Policy and Git Protection**
  - Added sandbox policy normalization, allow/deny overlap validation, temporary filesystem size parsing, and Bubblewrap capability probing; strict sandbox failures now report their cause instead of silently downgrading.
  - Applied sandbox configuration consistently across CLI, ACP, A2A, Channels, serve API, and sub-agents; the serve API now defaults to sandbox disabled and uses per-session work-directory isolation when enabled.
  - When Git protection is enabled, `.git` metadata is denied by default; Git commands can receive a one-time approval to temporarily access it, preventing ordinary commands from modifying repository internals.
  - Simplified sandbox network configuration and TUI settings, and corrected Linux project-directory and `/proc` mount handling.

### 🐛 Fixes

- **SkillHub Session and Channel Support**
  - Fixed skill installation and activation in the serve Web UI and Channels to use the correct work directory and session state, while ensuring active skills are loaded by the current session and its sub-agents.

## v1.1.67

### ✨ Features

- **Unified Online Installer**
  - Online installation now uses `https://mothx.net/install.sh` on Unix-like systems and `https://mothx.net/install.bat` on Windows.
  - The scripts reuse an existing Node.js installation or install Node.js LTS when missing, then install the latest release with `npm install -g mothx-installer`.

### 🐛 Fixes

- **Corrupt Settings Recovery**
  - Invalid global or project-level `settings.json` files no longer prevent MothX from starting when the malformed file can be backed up successfully.
  - Malformed files are renamed to timestamped backups such as `settings.json.bak_20260715-143000`; name collisions receive a numeric suffix.
  - A warning reports the platform-native absolute backup path. Global failures fall back to defaults, while invalid project settings are ignored without discarding valid global settings.

- **TUI Managed Agent Status**
  - Fixed TUI managed-agent state tracking so main-agent runs in multi-agent, delegate, and workflow modes are marked `running` at turn start and `done` or `error` when the turn finishes.
  - Workflow-mode failures now update the managed agent status to `error` instead of leaving the tab/status UI stuck in `running`.

- **Delegate Sub-Agent Completion**
  - Added regression coverage ensuring the delegate sub-agent tool blocks until the child agent completes and returns the child's final result instead of returning early.

## v1.1.66

### ✨ Features

- **Fuzz Test Targets & Make Target**
  - Added fuzz tests for ESM report parsing (`internal/esm/report_fuzz_test.go`), MCP JSON-RPC message handling (`internal/mcp/mcp_fuzz_test.go`), and utility truncation (`internal/util/truncate_fuzz_test.go`).
  - Added `make fuzz` target that runs every registered fuzz target for a configurable duration (`FUZZTIME`, default `10s`), since Go fuzzing accepts only one package per invocation.

### 🔧 Improvements

- **Per-Model Output Token Limits**
  - Removed the global `maxOutputTokens` setting from `settings.json`. Output limits now always come from the active model's `maxTokens`, avoiding truncation or oversized outputs caused by a one-size-fits-all global cap across providers.
  - Simplified `agent.ResolveMaxTokens` to take only the model; dropped the `MaxTokensSet` gate so built-in model defaults apply automatically without requiring user configuration.
  - Updated CLI/print, ACP server, TUI (`/model` cycling, `ensureAgent`, `?` / BTW help), OpenAI-compatible API (`/compact` and chat completions), AgentFactory, and ESM sub-agent paths to use the new signature.
  - Removed the Max Output Tokens field from the TUI `/settings` Behavior panel; `SaveGlobalSettingsPatch` now strips legacy `maxOutputTokens` keys from existing sparse settings files.
  - Updated `docs/en/configuration.md`, `docs/en/faq.md`, and `README_zh.md` to remove the retired setting; the FAQ now points users at per-model `maxTokens`.

- **Volcengine Plan Default Max Tokens**
  - Lowered the default `MaxTokens` for every model under the `volcengine-agentplan` and `volcengine-codingplan` providers (Ark Code, Doubao Seed 2.0 series, GLM-5.2, Kimi K2.x, DeepSeek V4 Pro/Flash, MiniMax M3/M2.7) to 100K, matching current upstream limits and the CodingPlan model-set note.
  - Corrected the CodingPlan doc note (MiniMax M2.7 is excluded, not M3) and added a `TestVolcenginePlanModelsUseSharedMaxTokens` regression test.

- **TUI Debug Output Cleanup**
  - Interactive TUI runs no longer leak streaming JSON debug lines into the Bubble Tea view while `--debug` is on: debug output still goes to `debug.log` and the pprof server starts as before, but stderr chatter is suppressed via a new `VIBECODING_DEBUG_LOG_ONLY` env var.
  - CLI `--print` mode (and other non-TUI entry points) continues to print `[DEBUG]` lines and pprof status to stderr, and the startup "Debug logging enabled" banner is also limited to print mode.
  - `DebugCompleteResponse` now falls back to a structured string dump when `json.Marshal` fails (e.g. for tool calls that accumulated invalid `json.RawMessage` arguments mid-stream), so malformed payloads never disappear from `debug.log`.
  - `debugpprof.Start` accepts a writer for its log lines; added coverage for the marshal-failure debug path.

- **Test Suite Maintenance**
  - Updated stats dashboard migration expectation to 15 (post-ESM recovery columns).
  - TUI auth-dialog, settings-sparse, and zero-override tests rewritten to exercise `maxContextTokens` instead of the removed `maxOutputTokens` field.

## v1.1.65

### 🔧 Improvements

- **GitHub Release Automation**
  - Added `.github/workflows/release.yml` to automatically build and publish release artifacts when a Git tag is pushed.
  - Workflow builds the Web UI (Node.js 22) and Go binaries (via `make dist`), then creates a GitHub Release with tarballs, `.deb` packages, zip archives, and SHA-256 checksums attached.
  - Pre-release tags (containing `-`, e.g. `v1.1.65-pre`) are automatically marked as prereleases on GitHub; release notes are auto-generated from merged PRs.
- **GitHub npm Packages Publish Workflow**
  - Added `.github/workflows/github-npm-publish.yml` to build and publish the npm installer packages (binary wrappers + the `@scope/mothx` meta package) to GitHub Packages on every tag push, with a `workflow_dispatch` input for manual version overrides.
  - Includes an idempotent `publish_if_needed` check so re-running the workflow against an already-published version skips instead of failing.

### 🐛 Fixes

- **CI npm Package Build Path**
  - Fixed the GitHub npm publish workflow to resolve `package.json` paths correctly when packaging per-platform binary tarballs, preventing publish failures caused by an incorrect working directory.

## v1.1.63

### ✨ Features

- **ESM Automatic Recovery for Interrupted Roles**
  - Added `RecoveryObserver` read-only sub-agent that inspects repository state after an ESM role (worker, critic, or audit) is interrupted by timeout or transport failure.
  - Added `RecoveryObserverTaskPrompt`, `RecoveryReport`, and `ParseRecoveryReport` for structured recovery decision (`resume` / `blocked`).
  - TUI `recoverInterruptedESMRole` converts bounded, recoverable role failures into clean supervisor completions so the next ESM continuation can launch a fresh worker.
  - Timeout-triggered recovery spawns a RecoveryObserver (5 min timeout) to verify repo state and list concrete remaining work.
  - Transport failures (provider errors after built-in retries) are auto-recovered without spawning an observer; a fresh worker retries from current state.
  - DB migration 015 adds `recovery_count` and `recovery_reason` columns to `session_esm_objectives`.
  - `RecoveryLimit = 2` consecutive automatic recoveries permitted; further interruptions pause continuation until `/esm resume`.
  - Recovery state shown in TUI footer (`recover N/2`) and included in steering/worker prompts.
  - Worker progress resets recovery counters on successful continuation.
  - Recovery observer uses the same read-only tool restriction as critic/audit sub-agents.

### 🔧 Improvements

- **TUI ESM Panel & Tool Modal Enhancements**
  - Track `FullThink`, `FullText`, `FullResult`, `LastToolName`, and `LastToolArgs` in agent activity for complete sub-agent activity inspection in the ESM panel.
  - ESM panel now shows `Now / Progress / Next` status with pipeline stage count and remaining work items.
  - Tool modal renders raw assistant/thinking content directly for the main agent; activity timeline entries preserved without truncation.
  - Tool argument keys sorted alphabetically for deterministic display.

### 🐛 Fixes

- **OpenAI-Compatible Provider Tool Argument Parsing**
  - Fixed tool call failures against providers (e.g. Volcengine Ark) that stream tool arguments as raw JSON objects instead of the escaped JSON string returned by the OpenAI API.
  - Introduced `openAIToolArguments` with a custom `UnmarshalJSON` accepting both string-encoded and raw object forms, normalizing to raw JSON.
  - Stream raw argument bytes directly into tool call buffers; marshal back to the OpenAI string form on outbound messages to keep conversation history wire-compatible.
  - Added unit tests covering string, object, null, and streamed tool call round-trip cases.
  - Registered Volcengine Ark as a known provider default and listed it in the provider/model docs.

## v1.1.62

### ✨ Features

- **ESM (Supervisor Mode)**
  - Added `internal/esm` package providing Event State Memory for long-running objectives with persistent state.
  - Added `/esm` command with `edit`, `pause`, `resume`, `clear`, and `budget` subcommands.
  - Registered `get_esm` and `update_esm` tools when ESM objective is active.
  - ESM steering messages injected via `AgentLoopConfig.GetSteeringMessages`.
  - ESM status shown in TUI footer.
  - SQLite-backed storage with `session_esm_objectives` table (migration 010).

- **ESM Completion Review Workflow**
  - Added worker → critic → audit review pipeline for completion candidates.
  - Added `StatusCompleteCandidate` with structured `WorkerReport`/`AuditReport` parsing and validation.
  - Added `completion_review`, `completion_run_id`, `completion_reason`, `blocked_run_id` to Objective schema (migrations 011/012).
  - TUI ESM orchestrates the full review pipeline; only a passing audit marks objective complete.
  - Restricted critic/audit sub-agents to read-only tools and require concrete blockers in reports.
  - Suppressed noisy role-agent lifecycle events in the parent TUI stream for cleaner output.
  - AgentFactory tracks `providerName`/`Vendor` for sub-agent runtime sync.
  - Added `withRuntimeConfig` for flexible factory cloning.
  - Worker reports now accept both `remaining_work` and `missing_work`; any remaining work or blocker rejects a completion claim before critic review.
  - Persisted the current ESM phase, latest worker progress, structured remaining work, and consecutive completion rejection state.
  - Added a three-rejection circuit breaker that pauses automatic continuation until the user runs `/esm resume`.
  - Added a live, scrollable ESM progress panel opened with `Ctrl+E`, including pipeline phase, remaining work, blockers, review details, usage, and current sub-agent activity.

- **Context Compaction Improvements**
  - `/compact` now executes immediately across TUI, channels, and OpenAI API runtimes (was previously deferred).
  - Added `CompactForced` for explicit user-requested compaction that allows summary-only checkpoints when no older history exists outside the recent keep window.
  - Auto-compaction trigger moved before building the next request so plain-text turns cannot miss the trigger point.
  - Switched auto-compaction threshold to percentage-based (80% of context window) via `ShouldCompactPercent`.
  - Fixed session replay to handle summary-only compaction entries where `FirstKeptEntry` is empty.

- **Docker Support**
  - Added `Dockerfile` (Ubuntu default, Debian/Fedora/Alpine variants) with multi-stage build.
  - Added `.github/workflows/ghcr-publish.yml` for CI/CD publishing to GHCR.
  - Added `.dockerignore` for clean builds.
  - Added Docker installation docs to README and getting-started guides (zh/en).

- **Sub-Agent Session Isolation**
  - Added dedicated `sub_session` and `sub_entries` tables for sub-agent, cron, webhook, and ESM worker sessions (migration 013).
  - Added `IsSubAgent` flag to `AgentOptions`; factory automatically routes sub-agents to isolated storage so they no longer pollute the main session list.
  - Cron scheduler, webhook dispatcher, and ESM worker pipeline all opt in to sub-session storage.

### 🔧 Improvements

- **TUI Split-Paste Handling**
  - Disabled bracketed paste by default to prevent TUI freeze when terminals drop the end marker.
  - Improved split-paste Enter deferral: queue Enter only when text was recently typed (within idle delay window), instead of always queueing when buffer is non-empty.
  - Added macOS-specific `console_darwin.go` to keep `WithoutBracketedPaste` where bracketed-paste end markers are unreliable.
  - Removed `WithoutBracketedPaste` from generic Unix (`console_unix.go`) now that the input queue handles split pastes without disabling bracketed paste.
  - Added tests for split-paste coalescing after first-line flush and deferred Enter submit.

- **Web UI Sidebar Fixes**
  - Fixed sidebar: auto-hiding scrollbar for history list, flex-shrink fixes for layout stability.

- **AgentFactory Enhancements**
  - AgentFactory now tracks `providerName` and `Vendor` for sub-agent runtime synchronization.
  - Fix usage stats provider name extraction when `Vendor` is empty.

- **Serve API Security & Hardening**
  - Default listen address changed to loopback (`127.0.0.1:8080`), default mode changed to `agent`, sandbox enabled by default.
  - Public (non-loopback) listen now requires Bearer token auth unless `--unsafe` is passed.
  - Added symlink-safe `IsWithinPath` helper; work directory resolution and `allowedWorkDirs` checks reject symlink escape.
  - Persisted session working directories are re-validated on load to prevent stale-path escapes.
  - Sessions are pinned during request handling to prevent idle-eviction races; added locking to `Touch`/use counters to fix concurrent list/evict data races.

- **Sandbox Cleanup**
  - Added `CommandCleanupProvider` interface; macOS Seatbelt sandbox now cleans up temporary profiles after each command exits.
  - Bash tool invokes sandbox cleanup on both synchronous and asynchronous command paths.

- **Cron Scheduler Reliability**
  - Added atomic `ClaimDue` in the SQLite cron store to prevent duplicate job runs across scheduler instances.
  - Tracked in-memory claims to avoid overlapping ticks for the same job.

- **A2A Task Cancellation**
  - Registered per-run cancel so `tasks/cancel` propagates to the executor context.
  - Introduced `TaskStore.Finish`/`Cancel` helpers and added terminal-state tests.

- **Stats Dashboard Heatmap**
  - Replaced the 30-day bar chart with a 7-day / 2-hour bucket flame-style heatmap for a more intuitive view of usage intensity.

### 📚 Documentation

- Removed stale `docs/proposal/codex-goal-mode.md`.
- Added `docs/proposal/enable-supervisor-mode.md`.
- Updated configuration docs to reflect new `/compact` immediate execution and summary-only checkpoint behavior.

## v1.1.61

### ✨ Features

- **Per-Session Tool Capabilities**
  - Added `x_tools` extension to `/v1/chat/completions` for enabling webSearch, browser, a2aMaster, delegate, and multiAgent per session.
  - Added `GET /api/capabilities` and `GET/PATCH /api/sessions/{id}/capabilities` APIs for querying and updating session tool toggles.
  - Added `session_capabilities` persistence table (migration 007) in `sessions.db`.
  - Added CLI flags `--web-search`, `--browser`, `--enable-a2a-master` for serve mode.
  - Added `webSearch`, `browser`, `a2aMaster` fields to `serve.json` config.
  - Web UI session tool toggles in composer bar with PATCH to capabilities API.
  - Per-session settings injection for `webSearch` via `settingsForSession`.
  - Added `x_session_id` + `x_working_dir` cwd conflict detection (HTTP 409).
  - Added `/api/sessions?scope=all|active` and `/api/sessions/active` endpoints.
  - `/mode` and `/delegate` commands now persist capability changes per session.

- **Session Streaming & Stats Dashboard**
  - Added SSE-based session streaming for real-time chat updates (`session_stream.go`).
  - Added sequenced message/event replay for cursor-based streaming.
  - Added `/api/stats/` endpoints (summary, timeseries, by-provider, by-model, recent).
  - Track session running state and publish run/capability events to stream.
  - Added cache read/write tokens to usage tracking.
  - Web UI: SSE streaming in Chat view, Stats dashboard view, Channels/Logs settings.
  - Added `WorkingDir` → `DefaultWorkDir` rename in serve config.
  - Added legacy config dir normalization for session/skills dirs.

- **Run & Capability Event Tracking**
  - Added `session_run_events` and `session_capability_events` tables (migration 008).
  - Record run lifecycle events (started/finished/failed/canceled) for every chat completion.
  - Record capability change events for `/mode`, `/delegate`, `x_tools`, and PATCH API.
  - New endpoints: `GET /api/sessions/{id}/run-events`, `GET /api/sessions/{id}/capability-events`.
  - Web UI displays recent run and capability events.
  - Added `make serve` Makefile target.

- **Transcript SSE Events for Streaming**
  - Added `x_transcript` field to `ChatCompletionRequest` for enabling transcript-mode streaming.
  - When enabled, streaming handler emits `event: transcript` frames (`assistant_delta` and `message` types) instead of legacy `tool_status` events.
  - Web UI sends `x_transcript:true` and routes transcript events through shared `upsertTranscriptMessage` path.
  - Refactored streaming handler to branch on transcript flag with helper functions for building transcript toolCall/toolResult entries.

- **Embedded Web UI Assets**
  - Web UI assets are now embedded in the binary via `go:embed`, no longer requiring dist files on disk.
  - Added `ui` package with `DistFS()` and `fs.FS` abstraction for both embedded and override paths.
  - `--web-ui-dir` flag still supported for overriding embedded assets.

- **External Bind Address via --port**
  - `--port` now accepts full addresses (e.g. `0.0.0.0:8080`).
  - Removed `displayListenAddr` rewrite that overrode `0.0.0.0` to `127.0.0.1`.
  - Added `mothx serve --unsafe` to disable auth and expose loopback/default listens on all interfaces for the current process.

- **Web UI Keyboard Shortcuts & Pagination**
  - Sidebar: Cmd/Ctrl+K to focus search, Shift+Cmd/Ctrl+K for new chat.
  - Platform-aware shortcut labels (macOS vs other), Escape clears search.
  - Sessions view: paginated list (25 per page) with page navigation controls.

- **WeChat QR Login API & Channels Settings UI**
  - Added WeChat QR login API endpoints (login status, QR proxy, base64 mode).
  - Added `wechatLoginSession` for managing QR scan flow state.
  - Rewrote Channels.svelte with full WeChat QR login flow (polling, display, error handling).
  - Added Feishu config form (appId/appSecret/workspace/allowedUsers) and WebSocket channel toggle.
  - Added `ProviderSettings.svelte` wrapper and comprehensive i18n strings for channel settings.
  - Dispatcher nil-checks before starting platforms.

- **Sub-Agent Detach & Rule Guard Fix**
  - Added `DetachChild()` to `AgentManager` to remove child from parent's active list while retaining the child agent for later inspection.
  - Added `HasRunning()` to `AgentManager` to check if any agent is actively executing.
  - Changed `DelegateSubAgentTool` to use `DetachChild` instead of `Destroy` so completed delegated children remain inspectable via handle.
  - Delegate result now returns handle for tracking delegated child agents.
  - Fixed `/rule` command guard to use `HasRunning()` instead of `Count() > 0` so rule changes are allowed when only completed retained agents exist.

- **Cron Store Migration to SQLite**
  - Replaced `FileCronStore` (cron.json) with `SQLiteCronStore` (sessions.db) for reliable, transactional cron job persistence.
  - Added `SessionScopedStore` to bind cron jobs to sessions with per-session isolation and automatic workDir inheritance.
  - Added `--cron` CLI flag as independent option (separate from `--multi-agent`).
  - Cron now enabled by default in serve mode without requiring multi-agent.
  - Scheduler attaches scheduled local runs to existing sessions when `SessionID` is set.
  - Added `cron_jobs` table migration in sessions.go.
  - Cron tool supports `findJob` by name (with ambiguity detection) for enable, disable, delete, and run actions.
  - Dispatcher lazily initializes `AgentManager` for cron-only sessions.

- **Serve Settings Hot-Reload & Workflows Toggle**
  - Added `Server.ApplySettings()` to hot-reload provider/model after settings save.
  - Added `workflows` session tool option and feature flag in serve.
  - Added `nearestExistingBrowseDir` fallback when default work dir is missing.
  - Refactored truncation to `util.TruncateWithSuffix` (UTF-8 safe).

- **System Prompt Rename to MothX**
  - Renamed system prompt identity from VibeCoding to MothX.

### 🔧 Improvements

- **Web UI Settings Expansion**
  - Added `ListEditor` reusable component for editing string lists in settings.
  - Expanded `AppSettings` with full form-based editor for defaults, web search, context files, compaction, sandbox, retry, approval, and provider config.
  - Expanded `ServeConfig` with full form-based editor for features, API, cron, memory, security, agent, hooks, channels, and lobster mode settings.
  - Web UI: sessions table fixed columns with ellipsis layout.
  - Web UI: skill_ref and workflow_lint tool call/result display.
  - Web UI: simplified WeChat QR to open-in-new-tab instead of embed.
  - Web UI: `resetSelectedModelToDefault` on session switch and settings save.
  - Web UI: workdir settings refresh after save, cleaner restrict logic.
  - i18n: added zh/en strings for workflow and skill_ref tools.

- **Web UI Internationalization & Theme Support**
  - Added a complete i18n system for the Web UI with Chinese/English language switching.
  - Added a preference controls panel (`PreferenceControls`) for adjusting language and theme.
  - Replaced all hardcoded strings across views and components with translation keys.
  - Added a CSS variable-driven theme system supporting Dark/Light theme switching.

- **Web UI Tool Calls & Plan Card Rendering**
  - The Web UI chat interface now renders tool calls, tool results, and plan cards.
  - Tool calls are displayed as running/completed status chips; tool results are collapsible (summary shows first line, click to fetch full output on demand).
  - Plan tool invocations render as a live-updating todo checklist.
  - Backend added `/api/sessions/:id/tool-results/:callId` endpoint for lazy-loading full tool output.
  - `ListActiveSessions` now also returns historical sessions so the Sessions page shows all persisted conversations.

- **Richer Tool Status Streaming in Serve Mode**
  - SSE streaming events now carry richer tool status information, enabling the frontend to display real-time tool execution progress.
  - Tool status events include tool name, arguments, and execution results.

- **Debug Mode pprof Server**
  - Added `--debug` flag to start a local pprof profiling server across all entry points (CLI, TUI, Serve, ACP, A2A).

- **Speedtest Command**
  - Added `vibecoding speedtest` CLI subcommand for testing model response speed.

- **New StepFun Vendor Support**
  - Added `stepfun` vendor with Base URL `https://api.stepfun.com/step_plan/v1` using OpenAI-compatible protocol.
  - Added `step-3.7-flash` model with 256K context window and multimodal (text + image) input support.

- **Multimodal Image Input**
  - Serve mode now supports multimodal (image) input; images can be uploaded via API for conversations.
  - Improved session persistence to correctly store multimodal messages.

- **Serve init-config Subcommand**
  - Added `mothx serve init-config` subcommand for initializing global and project-level `serve.json` configuration.

- **TUI Input Queue On-Demand Start**
  - Input queue ticker now starts on demand and stops when idle, reducing unnecessary CPU usage.

- **TUI Backspace/Delete to Remove Auth Models**
  - Added Backspace/Delete shortcut in the auth dialog model list to delete a selected model entry.
  - Action rows like `+ Add Model` and `Done` are excluded from deletion.

- **Split-Paste Coalescing Configurable**
  - Split-paste event coalescing is now configurable via test parameters, facilitating unit test verification.

### 🔒 Security

- **Browse API Restriction**
  - Browse API is now restricted to `allowedWorkDirs` whitelist directories, rejecting out-of-bounds access.
  - Auth requests without tokens are now rejected to prevent unauthorized access.

- **Code Scanning Fix**
  - Fixed a potentially unsafe quoting issue (Code Scanning Alert #3).

### 🔄 Refactors

- **Merged Gateway & Hermes into Unified Serve Mode**
  - Removed `internal/gateway` and `internal/hermes` packages, merged into the unified `internal/serve/` architecture.
  - Removed `gateway`/`hermes` subcommands from CLI entry points.
  - Added `internal/serve/openaiapi` (OpenAI-compatible API runtime), `internal/serve/channels` (message channel dispatcher), `internal/serve/ws` (WebSocket channel runtime).
  - Removed Hermes built-in terminal WebSocket client.

- **WebUI Component Split**
  - Split monolithic `App.svelte` into separate components, views, and lib modules for improved maintainability.

### 📦 Dependencies

- Bumped `golang.org/x/image` from v0.36.0 to v0.41.0.

## v1.1.60

### ✨ Features

- **Unified Serve Mode with Web UI**
  - Added `mothx serve` CLI command to start a unified server exposing OpenAI-compatible APIs, a Web UI management panel, and messaging channels (WeChat/Feishu) simultaneously.
  - Added `internal/serve/` package to unify Serve, Channels channel, and Web UI configuration and runtime management.
  - Configuration via `serve.json` (global `~/.mothx/serve.json`, project `.mothx/serve.json`), supporting Serve, channels, Web UI, Cron, Memory, Security, Hooks, and Agent settings.
  - Built-in Svelte Web UI panel with Dark theme, health check, channel status, config editor, settings editor, and chat interface with SSE streaming.
  - Web UI now includes full management APIs: `/api/status`, `/api/sessions`, `/api/cron`, `/api/memory`, and `/ws/logs` for real-time log streaming.
  - WebSocket runtime mounted at `/ws`, reusing Channels event protocol for real-time communication.
  - Cron API supports CRUD operations with scheduler integration.
  - Serve SessionPool now has List/Delete management interfaces.
  - Added `--web-ui-dir` CLI flag to override the Web UI static assets directory.
  - Added Lobster mode (`--lobster`) that auto-enables yolo mode, disables sandbox, and turns on sub-agents.
  - Serve now supports an `ExtraRoutes` hook for Serve mode to inject custom API routes (`/api/serve/config`, `/api/settings`, `/api/channels`).

- **New Vendor Support**
  - Added Huawei Cloud vendor (`huawei`, `huawei-plan`) with 13 models total, including standard and Plan reasoning modes.
  - Added Moore Threads vendor (`mthreads-plan`) with GLM-4.7 model (1M context).
  - Added Tianyi Cloud vendor (`ctyun-plan`) with 3 models including GLM-5-Turbo.
  - Added JD Cloud vendor (`jd-plan`) with 10 models including JoyAI-LLM-Flash.
  - Added Kimi-K2.5 and MiMo-V2.5-Pro to Gitee/Moark providers; fixed JD Plan config with missing models.

### 🐛 Bug Fixes

- **TUI Split Paste Event Coalescing**
  - Some terminals split pasted text into separate key events. Added idle detection to wait for a quiet period before flushing the input queue.
  - Split paste events are now coalesced into a single paste when Enter appears within the stream followed by more text.
  - Extracted `handleInputSubmit()` for cleaner Enter key handling.

- **Legacy `VIBECODING_DIR` Handling**
  - `ConfigDir()` now falls back to the default `.mothx/` path when `VIBECODING_DIR` is set to the legacy default `~/.vibecoding`, avoiding an unintended override of the new config directory.
  - `ConfigDirOverridden()` no longer reports a custom override when `VIBECODING_DIR` equals the default legacy path.
  - The stats CLI now reads `sessionDir` from `config.LoadSettings()` instead of calling `platform.SessionDir()` directly, ensuring it respects the configured session directory.

### 🧪 Tests

- Added `TestConfigDirIgnoresLegacyDefaultEnvDir` and `TestConfigDirHonorsCustomLegacyEnvDir` to verify `ConfigDir` correctly ignores or honors `VIBECODING_DIR` based on whether it matches the legacy default.
- Added `TestLoadSettingsWithLegacyDefaultEnvCreatesMothXConfig` to verify settings migration creates `.mothx/` config when `VIBECODING_DIR` points to the legacy default.
- Added `TestOpenStatsDBUsesConfiguredSessionDir` to verify the stats command resolves the session database path from settings.

## v1.1.59

### ✨ Features

- **Tool Selection Rules in System Prompt**
  - Added a "Tool Selection Rules" section to the agent system prompt, instructing the model to prefer dedicated tools (`read`, `ls`, `grep`, `find`) over `bash` for file inspection and discovery.
  - Explicitly discourages running `cat`, `sed`, `awk`, `grep`, `find`, `ls`, `pwd` via `bash` when equivalent dedicated tools exist.

- **Directory Migration to `.mothx/`**
  - Install scripts (`install.sh`, `install.ps1`) now default to `~/.mothx/` (was `~/.vibecoding/`).
  - Added `MOTHX_INSTALL_DIR` env var; `VIBECODING_INSTALL_DIR` remains as a legacy fallback.
  - Uninstall checks both old (`~/.vibecoding/`, `./.vibe`) and new (`~/.mothx/`, `./.mothx`) directories for backward compatibility.
  - npm postinstall scripts and README updated to reference `~/.mothx/settings.json`.

### 🔧 Improvements

- **Bash Non-Interactive Subprocess & Process Group Kill**
  - Bash tool subprocesses now run in non-interactive mode: stdin is set to empty (`read` sees EOF instead of blocking), and non-interactive env defaults are injected (`GIT_TERMINAL_PROMPT=0`, `GIT_ASKPASS=true`, `SSH_ASKPASS=true`, `SSH_ASKPASS_REQUIRE=never`, `SUDO_ASKPASS=true`) unless the user has explicitly set them.
  - On Unix, `Setsid` gives the shell its own session; cancellation kills the entire process group via `kill(-pid)` so auth helpers and grandchildren do not linger.
  - Added `killCommandProcess` helper shared by `BashTool` and `JobManager` for unified process termination.

- **TUI Auth Dialog Refactor**
  - Auth input fields (API key, provider ID, model name, etc.) now use `SetMaxLines(1)` instead of `SetMaxLines(3)`, enforcing single-line input for credential and identifier fields.
  - Introduced `newAuthInput()` helper to centralize editor creation, reducing duplication across `auth_dialog.go`, `auth_model.go`, `auth_provider.go`, and `auth_settings_top.go`.
  - Removed redundant `editor` imports from files that now use the helper.

- **Tests**
  - Added `TestAuthAPIKeyInputStaysSingleLine` to verify auth input does not wrap into multiple lines.
  - Added `TestLoadSettingsCreatesMothXConfigDir` to verify settings creation uses `.mothx/` and does not create `.vibecoding/`.

## v1.1.58

### 🔧 Improvements

- Renamed npm platform binary packages from `mothx-*` to `mothx-installer-*` for consistent naming with the root `mothx-installer` package.
- Renamed the PyPI installer package from `vibecoding-installer` to `mothx-installer`; the Python wrapper now exposes `mothx` as the primary command while keeping `vibecoding` as a compatibility alias.

## v1.1.57

### ✨ Features

- **Image Preprocessing & Multimodal Enhancements**
  - Unified image preprocessing pipeline with metadata propagation across the tool chain.
  - Added image crop support, browser screenshot preprocessing, and OpenAI `detail` parameter passthrough for `auto`/`low`/`high` quality control.
  - Image output-size enforcement with provider-specific hints and coordinate mapping for bounding-box annotations.
  - Added Qwen-specific 28px patch image token estimation and multi-image accumulation for accurate token accounting.
  - Added `/paste-image` command with `Ctrl+R` preview support.

- **Stats Dashboard Improvements**
  - Added share button, token trend chart, and overall UI improvements.
  - Added 2.5h time bucket grouping and filtering on the recent requests page.

- **Auto-Open Auth Dialog on First Run**
  - The auth dialog now opens automatically on first run when no provider is configured.

- **MothX npm rename transition**
  - Added the new `mothx-installer` npm package as the forward-looking installer.
  - Kept `vibecoding-installer` as a compatibility package for this release, with migration notices pointing users to `npm install -g mothx-installer@latest`.
  - Renamed npm platform binary packages from `vibecoding-installer-*` to `mothx-installer-*`.

### 🔧 Improvements

- `MaxTokens` now resolves from model defaults and is clamped to the context window size.
- Added compaction timeout and summary token cap settings.
- Command suggestions and `/mode` / `/agent` descriptions clarified to avoid confusion.
- Added HTTP 500 to retryable status codes for provider requests.
- Updated `vibe-browser` to v0.1.3, removed local replace directive.
- Default settings file is now written more sparsely, omitting unset fields.

## v1.1.56

### ✨ Features

- **Interactive Sessions Dialog**
  - `/sessions` now opens an interactive picker dialog with Up/Down navigation, Enter-to-switch, `n` for a new session, and `d` for delete. Existing `/sessions ls`, `/sessions set <id>`, `/sessions clear`, and `/sessions del <id>` commands remain available.
  - TUI startup defers session creation until the first user message is sent, while `--continue`, `--resume`, `--session`, and `/sessions set` still bind to existing sessions.
  - Continuing or switching sessions in the TUI prints the loaded session history into normal terminal scrollback.

- **Stats Web Dashboard**
  - `mothx stats` starts a web dashboard on `127.0.0.1:7878` with charts and filtering.
  - Pure HTML/CSS/JS dashboard — no external dependencies. Charts drawn on `<canvas>`.
  - Displays overall summary (requests, tokens, cost, duration), time-series charts, per-provider/model breakdowns, and a paginated recent requests table.
  - Filters by time range (today/week/month/all), vendor, and protocol.
  - `mothx stats --cli` prints the same statistics directly in the terminal.
  - `mothx stats --db <path>` opens an alternate sessions.db.

- **Stats Dashboard: Protocol + Vendor Split**
  - The "Provider" column in the stats dashboard has been semantically split into **Vendor** (the company/provider name) and **Protocol** (the API protocol, e.g. `openai-chat`, `anthropic-messages`, `google-gemini`).
  - Added `Provider.API()` to the provider interface so the protocol type is recorded alongside the vendor name in `request_stats`.
  - New filter dropdowns for Vendor and Protocol; pie chart and table now show both dimensions.
  - Schema migration 006 adds the `protocol` column to `request_stats` (backfilled with empty string for existing rows).

- **LongCat Provider Support**
  - Added the `longcat` vendor adapter, supporting both OpenAI-compatible (`https://api.longcat.chat/openai`) and Anthropic-compatible (`https://api.longcat.chat/anthropic`) endpoints.
  - Registered two default providers in settings: `longcat` (OpenAI format, `LONGCAT_API_KEY`) and `longcat-anthropic` (Anthropic format, `LONGCAT_ANTHROPIC_API_KEY`).
  - Default model `LongCat-2.0`: 1M context window, 128K max output tokens.
  - TUI auth dialog offers selectable base URLs for OpenAI vs Anthropic format under the `longcat` provider.

- **Inline `<think>` Reasoning for OpenAI-Compatible Models**
  - Added a `parseReasoningInContent` model compat flag for OpenAI-compatible providers. When enabled, reasoning emitted inline in the content stream and wrapped in `<think>...</think>` tags is extracted and surfaced as thinking deltas instead of regular text.
  - The streaming parser correctly handles tags split across multiple SSE chunks and treats dangling partial tags as literal text at stream end.

- **Auth V2 Settings Tracking**
  - Added `fieldSet` tracking to `ProviderConfig` and `ModelConfig` via custom `UnmarshalJSON` implementations, enabling detection of explicitly set JSON fields for auth V2 merge behavior.
  - Custom `Settings.UnmarshalJSON` handles the map-style `providers` key without requiring struct field changes.

- **Project-Level Bash Auto-Approval Rules**
  - `allow.json` now supports `bashCommands` (exact match) and `bashPrefixes` (prefix match) for project-level bash auto-approval in agent mode.
  - The approval dialog offers "Always Allow Exact Command" and "Always Allow Command Prefix" options that persist rules to `.vibe/allow.json`.
  - Settings-level `bashBlacklist` takes precedence over project allow rules (blacklisted commands always require approval).
  - `autoEdit` in `allow.json` now defaults to `true` when no file exists, matching the typical developer workflow.

- **Full Settings Dialog via `/settings`**
  - `/settings` now opens a structured root menu instead of jumping directly into the provider list. Categories: Providers, Defaults, Behavior, Web Search, Context Files, Status Line, Compaction, Sandbox, Paths, Retry, and Approval.
  - Each top-level setting group has its own sub-menu with editable fields, boolean toggles, and list editors.
  - Top-level setting edits use `SaveGlobalSettingsPatch()` to update only the affected JSON key, preventing unrelated defaults from being expanded into `settings.json`.

- **Interactive Approval Dialog**
  - Replaced the inline "y/n" approval prompt with a dedicated dialog supporting ↑/↓ navigation, Enter to select, y/n shortcuts, and Esc to abort.
  - Approval dialog shows structured details per tool type: bash commands display with timeout/async metadata; edit/write show wrapped argument summaries.
  - The footer alert now reads "! APPROVAL REQUIRED: ↑/↓ Enter" to reflect the new interaction model.

### 🔧 Improvements

- Extracted ~1000 lines of embedded dashboard HTML from `internal/stats/dashboard.go` into `internal/stats/dashboard.html`, loaded via `go:embed`.
- Stats are recorded automatically by the agent loop after every LLM call. The stats server calls `session.ApplyMigrations()` on open to ensure the `request_stats` table exists.
- Updated Volcengine provider: added `agentplan` and `codingplan` vendors, unified gitee/moark adapters, and removed the `seed` vendor.
- Updated PyPI build with venv isolation (`.venv-build`) to decouple PyPI builds from system Python.
- Extracted `bashCommandArg()` helper to support both `command` and `cmd` argument keys consistently across the approval path.
- Refactored TUI Esc handling into `abortPendingRequest()` to properly clean up approval and question state.
- Fixed stale `ParamField` / `ParamFieldKey` carry-over when navigating auth dialog views; toggles and submenus no longer leave input mode active.
- Fixed indentation in default provider config model slices.
- Added tests for auth dialog and config field tracking.

## v1.1.54

### ✨ Features

- **Multi-Workspace Session Isolation in Serve**
  - Isolated default HTTP API sessions by work directory (`workDir`) rather than sharing a single global default. Multiple workspace clients no longer share fallback session history.
  - Added `OpenByIDExact` to load session metadata and reconstruction info directly by exact session UUID, ignoring current working directory constraints.
  - Serialized concurrent session creation inside the HTTP API server to safely handle rapid successive calls and prevent duplicates.
  - Improved `/sessions del` slash command to support prefix matching for session IDs and prevent deleting the currently active session.
  - Preserved the serve session slot on `/clear` while cleanly resetting all messages in the session manager.

- **PyPI Installer Packaging**
  - Added a PyPI package wrapper for `vibecoding-installer` that exposes the legacy `vibecoding` console command and ships platform-specific wheels with embedded native binaries.
  - Added `make pypi-*` release targets plus version-sync and wheel-build scripts, mirroring the npm release workflow while using pip's native platform wheel selection.
  - Updated installation and release documentation with `pipx install vibecoding-installer`.

### 💅 Improvements

- **Reliable Fallback Tool Call ID Generation**
  - Switched the fallback tool call ID generator to use a process-wide atomic counter combined with high-precision unique timestamps. This prevents Anthropic/OpenAI schema validation errors under heavy concurrent tool-calling loads.
  - Updated model lists and default configs for several providers, specifically resolving Gemini-specific tool-calling constraints.
  - Preserved customized model parameters in the TUI Auth Dialog on save instead of resetting them to vendor defaults.

### 🐛 Bug Fixes

- **Thinking Level Normalization**
  - Added a normalization step for provider `thinkingLevel`. If the value is empty or invalid, it gracefully falls back to `medium` instead of silently disabling thinking, ensuring reasoning models perform correctly by default.


## v1.1.53

### ✨ Features

- **Embeddable agent: host-provided external tools**
  - Added a public `agent.ExternalTool` interface so embedding applications can expose their own controlled capabilities to the agent alongside (or instead of) the built-in coding tools.
  - Added `ExternalToolResult` (text/error + optional rich `Contents` blocks) and the optional `ExternalToolPromptInfo` interface for contributing system-prompt hints (`PromptSnippet`, `PromptGuidelines`).
  - Added `Builder.WithExternalTools(...)` to register custom tools and `Builder.WithoutBuiltinTools()` to disable all built-in tools, enabling an agent that may only use host-provided tools.
  - External tools are wired through the internal factory via an `externalToolAdapter`, and the internal package now builds from public `Builder` config through `CreateFromPublicOptions`.
  - Added a `bootstrap` package: external modules blank-import `github.com/startvibecoding/mothx/bootstrap` once to register the internal builder and provider resolution hooks (since internal packages cannot be imported directly).

### 💅 Improvements

- **Configured provider models honored end-to-end**
  - Threaded a `ModelID` field through the public and internal `ChatParams` so the selected model flows all the way to the provider request.
  - OpenAI- and Anthropic-compatible providers now resolve their model list and `compat` flags from the provider config when present, falling back to built-in defaults otherwise.
  - Provider factories are now registered through `init` hooks so `ResolveProvider` can construct providers by name via the global registry, with a simplified fallback chain that errors on an unsupported `api`.

- **Provider guide documentation**
  - Added a provider guide (`docs/en/provider-guide.md`, `docs/zh/provider-guide.md`) covering provider/vendor configuration.

- **Bash execution ergonomics**
  - Reduced the default synchronous `bash` timeout to 45s, kept `async=true` for background jobs, and clarified `timeout=0` as an explicit no tool-level deadline.
  - Updated `bash` guidance to steer long-running services to `async=true` and call out network probes and other commands that often need an explicit timeout.
  - In the TUI, tool execution now shows a separate "running" line before the final result line, so long commands are visible while they are still in flight.

- **Internal module split**
  - Split agent, TUI, and command files into focused modules (agent approval/context, TUI paste/render, session/statusline commands) for maintainability, with no behavior change.

### 🐛 Fixes

- **Custom provider auth flow**
  - Fixed the custom provider authentication flow to advance correctly from the API key step to the models step.

## v1.1.52

### 💅 Improvements

- **Provider HTTP/1.1 fallback option**
  - Added `providers.<name>.forceHTTP11` to disable HTTP/2 for a provider HTTP client.
  - This can help with proxies or API gateways that occasionally reset HTTP/2 SSE streams with errors such as `stream ID ... INTERNAL_ERROR`.

- **Retry early provider SSE read failures**
  - OpenAI-compatible, Anthropic, and Google streams now honor the configured `retry` settings when a transient stream read error occurs before any visible output is emitted.
  - HTTP/2 `INTERNAL_ERROR` stream resets are now classified as retryable network errors.
  - Once text, thinking, tool calls, or usage have been emitted, stream read errors still fail immediately to avoid duplicate output.

- **Removed embedded rg/fd binaries — switched to pure-Go SDKs**
  - Replaced the embedded `rg` binary with the [`go-ripgrep`](https://github.com/startvibecoding/go-ripgrep) packages. The `grep` tool now runs ripgrep-compatible search in-process as pure Go, without system `grep` fallback.
  - Replaced the embedded `fd` binary with the [`go-fd`](https://github.com/startvibecoding/go-fd) SDK (`gofd.Find()`). The `find` tool now runs fd-compatible file discovery in-process as pure Go, without system `find` fallback.
  - Deleted the entire `internal/vendored/` package (embed files, binary extraction, `RgPath`/`FdPath`/`Ensure` helpers) and all 12 platform-specific `rg`/`fd` binaries (~42 MB).
  - Removed `scripts/prepare-vendored.sh`, `scripts/extract-vendored-tool.sh`, `scripts/download-ripgrep.sh`, `scripts/download-fd.sh`, and the `pkgs/` directory (cached tarballs).
  - Removed `prepare-vendored` and `test-vendored` Makefile targets; `build`, `build-all`, and `test` no longer depend on binary extraction.
  - The `bash` tool no longer injects `~/.vibecoding/bin` into `PATH`, since there are no extracted binaries to expose.
  - Output format remains line-oriented for `grep` and `find`; invalid roots and search setup errors are reported directly as tool errors.

- **FreeBSD Builds & Packaging**
  - Added FreeBSD `amd64` and `arm64` to the build matrix (`make build-freebsd`), tarball distribution (`make dist-freebsd`), and the full `make dist` / `make build-all` flows.
  - Added FreeBSD platform npm packages (`vibecoding-installer-freebsd-x64`, `vibecoding-installer-freebsd-arm64`) as optional dependencies, with platform detection in the npm wrapper and `install.sh`.
  - FreeBSD uses the pure-Go `grep`/`find` implementations and falls back to the no-op sandbox, since bwrap/seatbelt are Linux/macOS only.

- **Embedded BusyBox for Windows**
  - Embedded `busybox32u.exe` and `busybox64u.exe` assets for Windows, extracted at runtime and used as the default shell for the `bash` tool.
  - Falls back to PowerShell when BusyBox is unavailable.
  - Bash tool output now includes a runtime label indicating whether BusyBox or the system shell is in use.

- **Interactive Model Picker**
  - `/model` without arguments now opens an interactive picker dialog instead of listing models as plain text.
  - Supports search/filter, arrow-key navigation, current-model indicator, and Enter to switch.

- **Native ccstatusline Support**
  - Added `statusLine` configuration (`type`, `command`, `padding`, `refreshInterval`, `timeoutMs`, `fallback`) for external status line renderers.
  - Executes the status line command with a Claude-compatible JSON stdin payload; supports multi-line output, ANSI colors, and OSC 8 hyperlinks.
  - Added `/statusline` slash command (`on`/`off`/`status`/`test`/`refresh`) for runtime control.

## v1.1.51

### ✨ Features

- **New Provider: Volcengine (火山引擎)**
  - Added Volcengine provider with Doubao Seed models via the Ark API platform.
  - Models: Doubao Seed 2.1 Turbo (`doubao-seed-2-1-turbo-260628`, 256K context, text), Doubao Seed Evolving (`doubao-seed-evolving`, 256K context, text+image), Doubao Seed 2.1 Pro (`doubao-seed-2-1-pro-260628`, 256K context, text+image).
  - Uses OpenAI-compatible API endpoint `https://ark.cn-beijing.volces.com/api/v3`.
  - Automatic vendor detection via `ark.cn-beijing.volces.com` domain.

- **SQLite Session Storage**
  - Standardized new and resumed sessions on SQLite (`modernc.org/sqlite`) for improved query performance and metadata management.
  - For CLI and Serve, all session metadata and entry logs are stored in a single, unified `sessions.db` database file under `sessionDir`, using virtual `.db` paths as handles for listing, switching, and deleting. Only Channels writes physical handle files (like `active.db` and archived `*_corrupt.db` files) under per-user directories.
  - Added fast exact/prefix matching in `OpenByID` and `OpenByPathOrID`, including ambiguity detection and direct session reconstruction from the unified SQLite database.
  - ACP history replay now streams tool execution events (`toolCall`/`toolResult`) while loading stored conversation history.
  - `DeleteSession` purges session/entry rows from SQLite, and deletes the physical handle file if present (as in Channels), while refusing to treat the shared `sessions.db` database as a session handle.
  - Channels now uses `active.db` physical session handles, archives corrupt sessions as `*_corrupt.db`, and no longer falls back to legacy `active.jsonl` paths.
  - Removed legacy JSONL load/write paths so new and resumed sessions use SQLite only.

### 🐛 Bug Fixes

- **ACP Systeminit Plan Mode Write Access**
  - Fixed ACP systeminit to allow file writes in plan mode, enabling the TUI/ACP to use `/systeminit` for generating `AGENTS.md` without mode restriction errors.

### ✨ Features

- **`/systeminit` and `/reload` commands**
  - Added `/systeminit` to generate or refresh a project `AGENTS.md` for AI agents. Available in the TUI, ACP, and as the `mothx systeminit` CLI subcommand. In the TUI and ACP the agent heuristically uses the `question` tool to ask a few clarifying questions first, then writes a higher-quality `AGENTS.md`; the CLI runs non-interactively. Optional trailing guidance is supported, e.g. `/systeminit ask me in Chinese, write AGENTS.md in English`.
  - The `question` tool is now also available in `agent` mode (previously plan-only) and is registered for the ACP server, which surfaces questions via the `session/request_permission` channel.
  - Added `/reload` (TUI): restarts as a fresh process with a brand-new session, reloading config, context files, skills, and MCP — equivalent to relaunching the program.

- **Mode boundary enhancements: `/btw` side questions + editable-path whitelist + full auto-edit**
  - Added `/btw <question>`: answer a quick side question without interrupting the main task. It inherits the main conversation history (read-only) into a one-shot sub-agent. The answer is shown in a temporary floating overlay, never written back to the main session, and does not consume the main task's context window or token budget. The sub-agent is read-only (read/grep/find/ls/skill_ref). A long main history is automatically truncated when injected to keep the side query lightweight.
  - Added `/alloweditpath [add <glob>|remove <glob>|clear]`: an auto-edit path whitelist (supports `**`/`*` globs). In agent mode, `write`/`edit` whose path matches the whitelist auto-approve without prompting.
  - Added `/allowautoedit [on|off] [global]`: full auto-edit in agent mode (effectively only bash still needs approval).
  - The whitelist and the auto-edit flag persist to a dedicated `allow.json`: `/alloweditpath` and `/allowautoedit` (default) write the project-level `.vibe/allow.json`; `/allowautoedit on global` writes the global `allow.json`. Loading is global→project override (`editPaths` is project-only). It is auto-loaded on startup.
  - These only relax the approval layer; sandbox / allowedWorkDirs boundaries and plan / yolo semantics are unchanged.

- **Update notifications via npm registry**
  - MothX now checks the npm registry (`vibecoding-installer`) for newer releases and shows a non-blocking reminder at startup when an update is available.
  - Network checks run in the background (at most once per 24h) and only refresh a local cache (`update-check.json`); the foreground never blocks on the network.
  - The reminder appears in the TUI initial message and on stderr in `--print` mode, suggesting `npm install -g vibecoding-installer@latest`.
  - Disable via config file with `"updateCheck": false` in `settings.json`, or with `VIBECODING_NO_UPDATE_CHECK=1`; override the registry with `VIBECODING_NPM_REGISTRY`.

### 📚 Documentation

- Updated session documentation, CLI examples, FAQ cleanup guidance, architecture diagrams, Channels docs, and README feature summaries to describe SQLite-backed storage, `.db` handle files, and `active.db` Channels sessions.
- Added configuration docs for the built-in Volcengine/Doubao provider and refreshed provider-adapter lists to include Volcengine, Mistral, GitHub Copilot, Cloudflare, and Amazon Bedrock adapters.

### 💅 Improvements

- **TUI header and footer polish**
  - Enlarged the ASCII logo and vertically centered it within the header.
  - Dimmed the footer separator and unified the mode/model/path colors for a cleaner look.

## v1.1.50

### ✨ Features

- **Streaming Delta Builder Optimization**
  - Replaced string concatenation with `strings.Builder` for accumulating assistant and thinking text deltas during streaming, avoiding O(n²) memory growth on long responses.
  - Builders are finalized before printing on turn end, approval, and error events to ensure consistent output.

- **New Provider: Mistral**
  - Added Mistral AI provider with models: Mistral Large, Mistral Medium 3.5, Mistral Small, Codestral, Devstral, Magistral Medium/Small, and Pixtral Large.
  - Uses OpenAI-compatible API endpoint `https://api.mistral.ai/v1`.

- **New Provider: GitHub Copilot**
  - Added GitHub Copilot provider with Claude Sonnet 4.6/4.5, Claude Opus 4.8, Claude Haiku 4.5, Claude Fable 5, GPT-5.5/5.4/5.2, Gemini 2.5 Pro, and Gemini 3.5 Flash models.
  - Uses OpenAI-compatible API endpoint `https://api.individual.githubcopilot.com`.

- **New Provider: Cloudflare AI Gateway**
  - Added Cloudflare AI Gateway provider with Claude, GPT, Gemini, and Llama 4 Scout models.
  - Supports routing through Cloudflare's AI Gateway with models from Anthropic, OpenAI, Google, and Meta.

- **New Provider: Cloudflare Workers AI**
  - Added Cloudflare Workers AI provider with Llama 4 Scout 17B, Llama 3.3 70B, Gemma 4 26B, Mistral Small 3.1 24B, GPT OSS 120B/20B, Kimi K2.7 Code, and GLM 5.2 models.
  - Uses Cloudflare's Workers AI inference endpoints.

- **New Provider: Amazon Bedrock**
  - Added Amazon Bedrock provider with Claude Sonnet 4.6/4.5, Claude Opus 4.8, Claude Haiku 4.5, Claude Fable 5, Amazon Nova Pro/Micro/Lite, and DeepSeek V3.2/R1 models.
  - Uses OpenAI-compatible cross-region inference endpoints.

- **Compact TUI Footer and Input Divider**
  - Merged mode, model, and path onto a single footer line (was 3 lines).
  - Added half-block divider between transcript and input area for visual separation.
  - Applied background color to editor cursor and placeholder styles.
  - Added npm postinstall script with quick start info.

### 🐛 Bug Fixes

- **TUI Input Box Width Alignment**
  - Aligned input box width with the gap divider above for consistent layout.
  - Set editor width to full terminal width to match gap divider.
  - Fixed double padding subtraction in editor Width calculation by using `m.width` for the final render Width.

- **TUI `compactBashOutput` Trailing Whitespace**
  - Fixed `compactBashOutput` writing the original untrimmed line instead of the trimmed version after blank-line dedup, which could preserve trailing whitespace.

- **TUI Duplicate Transcript in Program-Backed Scrollback**
  - Cleared managed live content when a Bubble Tea program is active so completed transcript blocks printed to native scrollback via `Program.Println` are not duplicated in the live view.

- **Sandbox Info Label**
  - Removed redundant "YOLO mode" text from the "no sandbox" status display.

---

## v0.1.47

### ✨ Features

- **Expanded Model Catalog**
  - Added new Anthropic models: Claude Opus 4.8, Claude Opus 4.1, Claude Opus 4, Claude Sonnet 4.0, Claude Haiku 4.5, Claude Fable 5, and legacy Claude 3 series models.
  - Added new OpenAI models: GPT-5.5, GPT-5.5 Pro, GPT-5.4 series, GPT-5.3 Codex/Spark, GPT-5.2 Pro/Codex, GPT-5.1 Codex variants, GPT-4.1 series, o4-mini, o3/o3-pro/o3-deep-research, o1-pro, and legacy GPT-4 variants.
  - Added OpenRouter provider models: Claude Sonnet 4.6/4.5, Claude Opus 4.8, Claude Haiku 4.5, GPT-5.5/5.5 Pro/5.4, Gemini 3.5 Flash/2.5 Pro, DeepSeek V4 Flash/Pro, Qwen 3.7 Plus, Kimi K2.7 Code, MiniMax M3, Llama 4 Scout, GLM 5/5.2, Grok 4.3, and GPT-OSS-120B (free).
  - Added Vercel AI Gateway models: Claude Sonnet 4.6/4.5, Claude Opus 4.8, Claude Haiku 4.5, GPT-5.5/5.4, Gemini 3.5 Flash, DeepSeek V4 Flash/Pro, Qwen3.6 Plus, MiniMax M3, Kimi K2.7 Code, Grok 4.3, and GLM 5.2.
  - Reordered Anthropic and OpenAI model lists to show newest models first.

### 🐛 Bug Fixes

- **TUI Approval Details Visibility in Live View**
  - Fixed queued approval requests not showing details in the live transcript while waiting for user input.
  - The current approval message index is now tracked so it stays visible during the approval prompt.
  - Index is properly cleared after approval is answered and reset on state/clear paths.

- **TUI Tool Modal Performance and Display**
  - Added line-level caching for tool modal rendered output to avoid re-parsing the full transcript on every render.
  - Added per-entry caching for expanded tool results to avoid repeated formatting.
  - `invalidateToolModalCache()` is now called at all transcript state mutation points to keep the cache consistent.
  - Fixed edit tool results duplicating diff excerpts in the expanded view by extracting a dedicated edit header formatter.
  - Tool modals now open at the top (offset 0) instead of scrolling to the bottom.

### 🧪 Tests

- Added regression test verifying expanded edit output does not duplicate diff excerpts.

---

## v0.1.46

### ✨ Features

- **Workflow Agent Instance Keys**
  - Added `:key` for repeated logical workflow agents, so bounded `while` loops can keep literal agent names while storing per-round results as `phase.agent[key]`.
  - Added `result-key` and `result-latest`, plus `(result "phase.agent" :key "r0")`, for explicit keyed result lookup and latest-instance lookup.
  - Keyed workflow workers use instance-aware runtime IDs such as `agent-worker[r0]`, preventing repeated loop workers from colliding while preserving the logical agent name.

- **Workflow Lint Tool**
  - Added `workflow_lint` to validate workflow JavaScript DSL without running worker agents.
  - Linting checks JavaScript syntax, workflow/phase/agent forms, keyword arguments, required prompts, and result references.
  - Registered the lint tool alongside workflow run/status/cancel tools and updated workflow prompt guidance to lint non-trivial generated or edited workflows before execution.

- **Configurable Context Compaction**
  - Added compaction settings for `tokenizer`, `tokenizerModel`, and `template`, wired through CLI, print mode, ACP, Serve, Channels, TUI mode switches, and delegated agent factories.
  - Added built-in compression summary templates: `default`, `code`, and `conversation`, so long sessions can preserve task-appropriate checkpoints.
  - Introduced a token estimator abstraction while preserving the existing generic chars/4 estimator for `auto` and `generic`.
  - Compaction entries now record summary version, previous compaction ID, and last summarized entry ID for better session replay/debugging.

### 🐛 Bug Fixes

- **Context Compaction Replay**
  - Print mode now restores replayed session history before running the agent, preserving prior conversation context.
  - Manual and forced compaction now check for genuinely compactable older history instead of compacting only recent context.
  - Replayed compacted messages strip stale usage metadata from kept messages to avoid leaking obsolete token accounting into future runs.

- **Concurrent File Writes**
  - Added a process-wide in-memory file lock manager shared by default tool registries.
  - `write` and `edit` now acquire per-file locks before reading and modifying files, preventing concurrent agents from interleaving writes to the same target.
  - Lock waits honor context cancellation/deadlines and report the current owner when interrupted.

### 🔧 Refactoring

- **Pre-release Packaging**
  - `npm-publish-pre` now syncs and builds npm packages with a `-pre` version suffix before publishing pre-release packages.
  - Updated npm package metadata and optional platform dependency versions to the pre-release version.

- **Named Workflow Worker Agents**
  - Workflow worker agents now use deterministic IDs derived from DSL agent names (`agent-<name>`), improving event attribution and background agent visibility.
  - Workflow skill guidance now documents the ID mapping and recommends unique agent names within a workflow.

### 📚 Documentation

- Updated Workflow mode docs, tool reference, and the `workflow-javascript` skill to document `:key`, keyed result lookup, and bounded while-loop patterns.
- Documented context compaction `tokenizer`, `tokenizerModel`, and `template` settings, including the built-in template choices and the current reserved/deprecated status of idle compaction settings.
- Clarified Ctrl+O details modal key hints for target switching, paging, scrolling, and closing.
- Documented the TUI scrollback trade-off: completed transcript blocks are printed to native terminal scrollback for stable selection/history, while user input should remain block-printed rather than unbuffered streaming to avoid interfering with Bubble Tea live rendering.

### 🧪 Tests

- Added workflow runner, lint, integration, and skill coverage for keyed repeated agents and keyed result lookup.
- Added context compaction tests for custom token estimators, template resolution, configured summary prompts, compaction metadata, compactability checks, and session replay usage cleanup.
- Added Serve and Channels coverage for `/compact` when only recent context can be kept.
- Added workflow lint tests for valid source collection and missing result reference errors.
- Added workflow integration coverage verifying DSL agent names are reflected in runtime worker agent IDs.
- Added file lock tests for wait/cancel behavior, shared default managers, and `write`/`edit` context handling.

---

## v0.1.45

### ✨ Features

- **Workflow Skill with Progressive References**
  - Extracted workflow JavaScript/DSL documentation from the system prompt into a dedicated `workflow-javascript` skill, reducing system prompt size.
  - Introduced progressive reference structure: skill index page lists 9 reference files loaded on demand, with core rules loaded by default.
  - Eight pattern guides: research & investigation, serial & parallel composition, decision routing, bounded while loops, horizontal multi-agent collaboration, master-slave small teams, evaluator-optimizer review passes, and governance & human checkpoints.
  - Each reference file includes copy-ready JavaScript skeleton examples and pattern selection guidance.
  - `EnsureProjectSkill` automatically creates the skill and all reference files under `.skills/workflow-javascript/` without overwriting user-customized content.
- **Workflow Timeout Control**
  - Added optional `timeoutSeconds` support to `workflow_run`, allowing bounded long workflows to choose an appropriate timeout and intentional continuous workflows to set `0` to avoid the default agent-level deadline.

- **goja v0.0.2 Upgrade**
  - Upgraded `goja` dependency from v0.0.1 to v0.0.2 with expanded JavaScript surface.
  - Added support for backquote/comma, `let*`/`while`/`cond`/`catch`/`throw`/`lambda`/`defun`/`defmacro`/`with-current-buffer`/`save-current-buffer` special forms.
  - Added builtins: `cons`/`car`/`cdr`/`nth`/`append`/`reverse`/`member`/`assoc`/`funcall`/`apply`/`macroexpand`, arithmetic and predicate functions, and in-memory buffer + marker builtins.
  - Added comprehensive test coverage for v0.0.2 JavaScript features.

### 🔧 Refactoring

- **Serve Session-Level Skills Support**
  - Serve sessions now support independent `SkillsMgr` and `ExtraContext`, so delegate sub-agents inherit per-session state.
  - `/skill` and `/skills` commands now operate on session-level skills instead of global server-level skills.

- **System Prompt Streamlining**
  - Detailed workflow JavaScript VM syntax and DSL form descriptions removed from the system prompt, replaced by a reference to the `workflow-javascript` skill.
  - Only key constraints and usage notes remain in the system prompt, significantly reducing token usage.

- **Workflow Skill Reference Clarity**
  - Renamed reference file titles for clarity: "Continuous Loops and Iterative Tasks" → "Bounded While Loops", "Evaluator-Optimizer and Critic Loops" → "Evaluator-Optimizer Review Passes".
  - Split pattern selection guidance: bounded while loops for runtime repetition with stop conditions; evaluator-optimizer for one-pass draft/critique/revise pipelines.
  - Added constraint: do not simulate loops with numbered phases.
  - Unified progressive reference status labels to English ("loaded" / "load on demand") for consistency.

### 📚 Documentation

- Added Workflow mode usage guide and best practices documentation (EN/ZH) covering quick start, core concepts, common patterns, and pitfalls.
- Synced workflow references across docs pages: added Dynamic Workflows section to features overview, workflow orchestration scenario to use cases, and cross-links from tools references.
- Clarified workflow hidden defaults and limits in the `workflow-javascript` skill and docs: worker `:max-iterations` default/failure behavior, `workflow_run timeoutSeconds`, `concurrency` default, inherited `:mode`, default `:tools`, current work directory behavior, disabled nested orchestration, and unsupported per-worker options.

### 🧪 Tests

- Added workflow skill tests verifying skill file and 8 reference file creation, non-overwrite behavior, and missing reference auto-creation.
  - Expanded workflow runner and JavaScript test coverage.
  - Added tests for reference content clarity and non-overlap of loop vs evaluator-optimizer patterns.

---

## v0.1.44

### ✨ Features

- **Dynamic Workflows**
  - Added `--workflows` mode for CLI, ACP, and Serve, independent from `--multi-agent`.
  - Added JavaScript workflow tools: `workflow_run`, `workflow_status`, and `workflow_cancel`.
  - Added workflow runtime support for phases, series/parallel execution, concurrency limits, worker-agent tasks, result fan-in, and run logs.
  - Added persistent workflow run state under the MothX workflow store and `/workflows` status commands in TUI and Serve.
  - Added in-process active-run cancellation so `workflow_cancel` and `/workflows cancel <id>` can interrupt running workflows.

- **Z.AI Vendor Adapter**
  - Added `vendor_zai.go` with a dedicated `zai` vendor adapter, registering domains `api.z.ai` and `open.bigmodel.cn` with `thinkingFormat: zai`.
  - Updated `zai` and `zai-coding-cn` provider configs: set `Vendor: "zai"`, `ThinkingFormat: "zai"`, updated base URL to the coding endpoint, added `glm-5v-turbo` vision model.

- **Kimi Provider Updates**
  - Added `api.kimi.com` domain to the `kimi` vendor adapter for automatic vendor detection.
  - Added `User-Agent: KimiCLI/1.5` header to the `kimi-coding` provider config.
  - Added Kimi K2.7 Code and K2.7 Code HighSpeed models to `moonshotai`, `moonshotai-cn`, `fireworks`, and `opencode-go` providers.

- **New Models**
  - Added `GLM-5.2` model to the `opencode-go` provider (1M context, 262K max output).
  - Added Kimi K2.7 Code Fast model to `fireworks` provider.

### 🐛 Bug Fixes

- **TUI Agent Event Handling**
  - Fixed partial response text not being committed to terminal scrollback when an error event occurs mid-stream, ensuring partial content is not lost.
  - Added regression test verifying stream indices and print queue behavior on error.

- **Version Strings**
  - Fixed `Makefile` to use `--abbrev=0` with `git describe` for clean tag versions without commit count/hash suffix.
  - Fixed `sync-npm-version.sh` to strip commit count and hash suffix from version strings.
  - Updated `npm/bin/mothx` to use GitHub raw URL for install script fallback.

### 🔧 Refactoring

- **Agent Manager Deterministic Ordering**
  - `AgentManager.List` now sorts agents by start time then ID for stable, deterministic ordering.
  - Extracted `resetAgent`/`abortAndResetAgent` helpers to reduce code duplication in TUI commands.
  - Agent ID is now set in config when creating agents in TUI.

### 📦 Dependencies

- Added `github.com/startvibecoding/goja v0.0.1` as the embedded JavaScript subset evaluator used by workflow DSL execution.

### 📚 Documentation

- Added the dynamic workflows JavaScript proposal under `docs/proposal/`.
- Updated English and Chinese tool docs with workflow tool usage, JavaScript-only DSL guidance, and cancellation scope.

### 🧪 Tests

- Added workflow runner/store/tool tests covering JavaScript execution, parallel workers, result fan-in, persistence, tool registration isolation, and active-run cancellation.
- Added prompt and CLI flag tests ensuring workflow mode does not leak into multi-agent mode, delegate mode, or worker-agent prompts.
- Added `VendorFromBaseURL` test cases for `api.kimi.com`, `api.z.ai`, and `open.bigmodel.cn`.
- Added agent manager tests verifying deterministic list ordering.

---

## v0.1.43

### 🐛 Bug Fixes

- **TUI Input Flush**
  - Fixed `flushInputQueue` in TUI app to properly return the queued input as a `tea.Cmd`, ensuring queued keystrokes are flushed before processing key events (`Enter`, `Tab`, `Up`, `Down`). Previously the command was called but its return value was discarded.

### 🔧 Refactoring

- **Remove Unused `mergeSettings`**
  - Removed the unused `mergeSettings()` function and its related tests. Project settings merging is now handled directly by `LoadSettings`.
  - Rewrote `settings_zero_test` to test via actual file I/O with `LoadSettings()` instead of direct JSON unmarshaling.

### 📦 Dependencies

- **GoStreamingMarkdown Update**
  - Updated `github.com/startvibecoding/GoStreamingMarkdown` from `v0.0.2` to `v0.0.3`.

### 🧪 Tests

- Added test verifying that `Enter` flushes queued input before applying command suggestions.

---

## v0.1.42

### ✨ Features

- **TUI Multiline Input**
  - Replaced the prompt input with the reusable TUI editor component, enabling true multiline prompt composition.
  - `Alt+Enter` and `Ctrl+J` now insert newlines; `Enter` still submits the prompt.
  - Small multiline pastes are preserved as multiline text instead of flattening newlines to spaces, while large pastes still use paste markers.
  - `Up` / `Down` now move within multiline input first and only browse prompt history at input boundaries.

### 🐛 Bug Fixes

- **TUI Input Editing**
  - `Home` / `End` editing keys now reach the input editor correctly instead of being swallowed by top-level TUI handling.
  - Restored draft-preserving prompt history navigation when queued keystrokes have not yet flushed.
  - `/clear` now resets printed-message bookkeeping after emitting the clear confirmation, avoiding stale transcript print state after clearing.

### 📚 Documentation

- Updated TUI keyboard shortcut documentation for multiline input, newline insertion, history navigation, and tool modal behavior.

### 🧪 Tests

- Added tests for multiline prompt input, `Alt+Enter` / `Ctrl+J`, small multiline paste preservation, prompt history boundary navigation, Home/End input editing, and `/clear` transcript state reset.

---

## v0.1.41

### ✨ Features

- **TUI Redesign**
  - Added a startup header with the Vibe logo, version, provider/model, and current working directory.
  - Added a redesigned footer showing mode, model, cwd, elapsed/last request duration, sandbox, context window usage, cache metrics, and key hints.
  - Added an inline loading indicator while the agent is running, with spinner, elapsed time, and cancel hint.
  - Added a sticky todo list for active non-done `plan` tool steps, so long-running tasks remain visible while the transcript scrolls.
  - Added a multi-agent tab bar showing active agents and their states when more than one agent is running.
  - Added compact tool display mode, toggled with `Ctrl+G`, to collapse tool outputs into one-line summaries while keeping details available through `Ctrl+O`.

- **Terminal Scrollback Transcript**
  - Completed transcript blocks are now printed to native terminal scrollback via Bubble Tea `Program.Println`, leaving only live streaming content inside the managed TUI view.
  - This improves mouse scrolling, terminal selection/copying, and behavior for long transcripts.

- **TUI Component Foundation**
  - Added reusable editor, suggestion list, and vertical scroll components under `internal/tui/components/` with CJK-aware buffer and rendering behavior.

- **Response Formatting Guideline**
  - Updated the system prompt to discourage excessive bold text, headers, and bullet lists unless structure is needed or requested.

### 🐛 Bug Fixes

- **TUI Tool Result Printing**
  - Tool result updates now go through one-time transcript printing instead of only refreshing in-memory live content, preventing completed tool output from disappearing from terminal scrollback.

- **Viewport Cleanup**
  - Removed obsolete viewport state resets after the TUI moved transcript history to terminal-native scrollback.

### 📦 Packaging

- Updated npm installer package metadata and optional platform package versions to `0.1.40`.

### 🧪 Tests

- Added component tests for the new TUI editor, suggestion list, and vertical scroll models.
- Updated TUI cache/render tests for header, footer, native scrollback transcript printing, compact display mode, and simplified viewport behavior.

---

## v0.1.40

### ✨ Features

- **GoStreamingMarkdown Renderer**
  - Replaced Glamour-based Markdown rendering with `github.com/startvibecoding/GoStreamingMarkdown` (`gsm`) for print mode, local TUI, and Channels remote TUI.
  - Removed the local module replacement and now depend on the remote `github.com/startvibecoding/GoStreamingMarkdown` module directly.

- **Delegate Mode (Blocking Single Sub-Agent Delegation)**
  - New `delegate_subagent` tool: runs one sub-agent synchronously, returns a summarized result, enforces a single concurrent delegate limit.
  - `--delegate` CLI flag for root, ACP, and serve commands.
  - `/delegate [on|off|status]` slash command for TUI and serve.
  - `enableDelegate` configuration option in serve config (`serve.json`).
  - Dedicated **Delegation Mode** section in system prompt with context-cost heuristics, good/bad examples, and result interpretation guidance.
  - `AgentFactoryOptions.DelegateEnabled` factory support for programmatic use.
  - Sub-agent system prompt now uses a structured reporting format (`Result`, `Evidence`, `Changes`, `Risks`) with guidelines on negative results, test execution, and conciseness.
  - `delegate_subagent` result includes `tool_calls` count and `tool_breakdown` per-tool-name map.

- **Sub-Agent Execution Mode Inheritance**
  - Both `subagent_spawn` and `delegate_subagent` now inherit the parent agent's execution mode (`plan`/`agent`/`yolo`) instead of hardcoding `agent`.
  - Parent mode is injected via context in `executeSingleToolCall`.
  - Sub-agent policy `AllowedModes` expanded from `["agent"]` to `["plan", "agent", "yolo"]`.

- **Multi-Agent Approval Handling Improvement**
  - Removed the deadlock-prone `newApprovalForwarder` (synchronous approval handler with Mutex-based pending map).
  - Sub-agent approval requests are now forwarded via event channel (`sendParentEvent`) without blocking.
  - TUI tracks `agentID` in `pendingApproval` and dispatches approval responses to the correct sub-agent via `handleApprovalResponse`.

### 🐛 Bug Fixes

- **TUI Abort Reason in Errors**
  - When a TUI agent session is aborted (user presses Esc or mode changes), the abort reason is now included in the error message, e.g., `"Error: aborted (reason: user pressed Esc)"`.
  - `pendingAbortReason` tracked on the TUI `App` struct and cleared on `EventError`.

### 🧪 Tests

- Updated TUI Markdown rendering assertions to match `gsm` behavior while preserving coverage for content integrity and viewport width limits.
- Added `TestDelegateSubAgentTool` and `TestDelegateSubAgentToolMissingTask` to verify blocking sub-agent delegation.
- Updated `TestSubAgentPolicyDefault` to expect expanded allowed modes `["plan", "agent", "yolo"]`.
- Updated `TestAgentManagerEnforcesSubAgentPolicy` to allow `yolo` mode by default.
- Added `TestAgentErrorIncludesAbortReason` for TUI abort reason rendering.

---

## v0.1.39

### ✨ Features

- **Sub-Agent Skills & Plan Tool Inheritance**
  - `AgentFactory` now accepts a `skillsMgr` parameter so sub-agents automatically inherit the `skill_ref` tool from the parent session.
  - Added `EnablePlanTool` to `RegistryConfig`; the `enablePlanTool` setting from `settings.json` is now propagated to sub-agent registries, ensuring consistent tool availability across parent and child agents.

- **Session Compaction Replay State Persistence**
  - Sessions now persist compaction replay state (`ReplayState`) so that compacted sessions can be correctly resumed across restarts.
  - `LoadHistoryState` / `GetHistoryState` track per-message session entry IDs, enabling the compaction boundary (`firstKeptEntryID`) to survive session reload.
  - Serve and Channels use `LoadHistoryState` instead of `LoadHistoryMessages` for accurate post-compaction replay.
  - TUI blocks user input during manual compaction and merges `agentStartMsg`/`compactionStartMsg` into a unified `agentStreamStartMsg`.

- **TUI Viewport Rewrite & CJK Support**
  - Replaced custom ANSI parsing and line-wrapping with `bubbles/viewport` and `charmbracelet/x/ansi`, fixing character interleaving issues with mixed CJK/ASCII text during fast streaming.
  - Think messages now render on a separate path from assistant messages.
  - Long paths and URLs are now word-wrapped correctly without mid-token breaks.
  - Markdown rendering no longer overflows the viewport width.
  - Added `renderutil` package with comprehensive test coverage for mixed CJK/ASCII wrapping, ANSI sequence integrity, and full-file rendering.

- **Grep Tool Output Limiting**
  - The `grep` tool now limits output size during streaming to prevent excessive context consumption from large result sets.

### 🐛 Bug Fixes

- **Google Tool Result Grouping**
  - Fixed tool result grouping logic for Google/Gemini providers to correctly pair tool calls with their results.

- **TUI Compact Command**
  - The `/compact` command now triggers compaction immediately instead of waiting for the next agent turn.

- **Agent Exit Path Consistency**
  - Extracted `agentEndEvent()` helper to eliminate duplicate event emission code across 8 exit paths.
  - Fixed missing `EventAgentEnd` on session-save error paths.
  - Added missing `usage`/`contextUsage` metadata to `EventDone` in the `ShouldStopAfterTurn` path.

### 🧪 Tests

- Added `TestAgentFactorySubAgentsRespectPlanToolSetting` to verify the `enablePlanTool` setting propagates to sub-agent registries.
- Added `TestAgentFactorySubAgentsRegisterSkillRef` to verify sub-agents inherit the `skill_ref` tool when a skills manager is configured.
- Added tests for `agentEndEvent` consistency and `ShouldStopAfterTurn` metadata.
- Added comprehensive TUI tests for fixed-height rendering, CJK wrapping, and ANSI integrity.

---

## v0.1.38

### ✨ Features

- **Custom Provider Model Fallback**
  - When a provider is explicitly requested (via CLI, Serve, or Channels) but no model ID is specified, the factory now automatically falls back to the provider's **first available model** instead of using `settings.DefaultModel` (which belongs to the default provider).
  - This prevents configuration mismatches and "model not found" errors when using non-default providers without a specified model.

- **Channels Configuration Default Resolution**
  - Updated Channels default model resolution so that when `DefaultProvider` is specified in `serve.json` but `DefaultModel` is left blank, the system correctly falls back to the custom provider's first available model instead of propagating the global `DefaultModel` from `settings.json`.

- **Refined Exposed Agent SDK Package & Examples**
  - Completed the mapping for all missing events and fields inside the top-level `agent` bridge (including `Messages`, `TurnMessage`, `TurnToolResults`, `Message`, `ToolCall`, `ToolDiff`, `ToolError`, `PartialResult`, `Plan`, `Usage`, and `ContextUsage`).
  - Fixed a critical enum misalignment on `StreamEventType` caused by internal-only types (like `StreamThinkSignature`), implementing explicit, robust mapping helpers.
  - Implemented `PublicProviderAdapter` to seamlessly bridge internal providers back to the public `agent.Provider` interface, and wired them up automatically to avoid package import cycles.
  - Added two rich top-level examples in the `example/` directory (`simple_agent` and `custom_provider`) with dual-language READMEs, showing custom provider and tool loop execution.

### 🧪 Tests

- Added `TestCreateFallbackToFirstModel` in `internal/provider/factory_test.go` to cover fallback behavior for both custom and built-in providers when the model ID is blank.
- Added test coverage in `internal/serve/channels/config_test.go` for `GetDefaultModel` when `DefaultProvider` is specified in Channels config but `DefaultModel` is empty.

---

## v0.1.37

### ✨ Features

- **Vertex AI API Key Authentication**
  - Added support for authenticating directly with Google Vertex AI using API keys (`x-goog-api-key`) instead of requiring gcloud OAuth tokens (`ya29.`).
  - When an API key is used, the default base URL automatically routes to `https://aiplatform.googleapis.com/v1/publishers/google/models` (which doesn't require project/location parameters).
  - Keeps backward compatibility with existing OAuth bearer tokens.

### 🐛 Bug Fixes

- **Google Tool Thought Signatures**
  - Correctly extract and forward Gemini reasoning/thought signatures in tool calls, preventing any misalignment or signature mismatches.

- **TUI Raw Bash Output Preservation**
  - Preserved ANSI escape codes and raw spacing/newlines in Bubble Tea TUI rendering for `bash` tool results, avoiding accidental italic style inheritance from the TUI framework.

---

## v0.1.36

### ✨ Features

- **Doctor Subcommand** (`mothx doctor`)
  - New diagnostic command that checks environment, configuration, providers, sandbox, MCP servers, sessions, skills, and context files
  - Reports OS/arch, Go version, shell, home/working directory
  - Validates settings, serve, and MCP config files with parse checks
  - Lists configured providers with masked API keys, models with context window/max tokens/reasoning flags
  - Checks bwrap sandbox availability and version
  - Shows MCP servers, session counts, skills directories, and discovered context files
  - Unconfigured providers (no API key) are silently skipped

### 🐛 Bug Fixes

- **TUI Session State**
  - Fixed `/clear` to reset transcript rendering state, tool results, assistant markdown caches, active stream indices, plan panel, and tool modal state consistently with session switching
  - Extracted shared transcript/input reset helpers to reduce divergent cleanup paths

- **TUI Mode Switching**
  - Pressing Tab to cycle mode now aborts an active request before changing mode, matching `/mode` behavior and preventing approval/question responses from targeting a stale agent

- **TUI Question Tool**
  - Numbered question selections now resolve to the selected option text instead of sending raw numbers back to the model
  - Clearing question state now also clears the current question metadata

- **TUI Warnings and Details Modal**
  - Context-pressure and budget-pressure agent events are now displayed in the TUI
  - Ctrl+O now reports when there is no conversation detail to show instead of opening an empty modal

- **Context Pressure Threshold Comparison**
  - Fixed Context Pressure threshold unit mismatch: `Percent` (0-100) was compared directly against `threshold` (0-1), causing the warning to fire at ~0.5% usage instead of the intended 55%
  - Fixed `InitChannelsConfig` project template to include explicit `context_pressure_threshold` and `budget_pressure_threshold` defaults, preventing them from being serialized as 0 (disabled)

- **Live Message Rendering**
  - Live assistant messages render fenced code blocks as Markdown while keeping normal prose on the plain-text wrapping path to avoid awkward word splitting

- **Model Validation and Compaction**
  - Pass `ModelID` in `ChatParams` so providers know the active model
  - Forward model to compaction/summary generation, preventing silent fallback to default model
  - Return errors with available model list when model is not found instead of silently falling back
  - Consistent model error messages across factory, serve, and TUI

- **Google/OpenAI Tool Result Text Extraction**
  - Fixed Google and OpenAI providers sending empty content when tool results use rich `Contents` blocks instead of plain `Content` string
  - Added `googleToolResultText()` to extract text from `Contents` blocks in Google provider
  - Fixed `responseToolOutput()` usage in OpenAI rich tool result branch

### 🧪 Tests

- Added regression coverage for `/clear` transcript cleanup, question state tracking, empty details modal handling, pressure warnings, live code-block rendering, and prose wrapping
- Added agent-level integration test (`TestToolResultIsIncludedInNextProviderTurn`) verifying tool results with rich `Contents` blocks reach the next provider turn
- Added provider-level unit tests for Google and OpenAI tool result text extraction from `Contents` blocks

## v0.1.35

### 🐛 Bug Fixes

- **TUI Print Ordering**
  - Replaced ad-hoc `go program.Println(...)` goroutines with a single drain goroutine (`printCh`) to prevent message interleaving on Bubble Tea's unbuffered channel
  - `flushPendingPrints` now uses `tea.Sequence` instead of `tea.Batch` to preserve print order

- **Display Width Accuracy**
  - `truncate()` now uses `lipgloss.Width` instead of byte length, so CJK characters (2 cells) and ANSI escape sequences (0 cells) align correctly in the TUI grid
  - Tool modal title separator uses `lipgloss.Width` for correct line width

- **Tool Output Improvements**
  - `ls` tool result now shows a compact summary (blank-line removal) in collapsed view, matching `bash` output behavior
  - Tool result rendering handles multiline summaries with a newline separator instead of forcing everything onto one line

### 🧪 Tests

- Added `formatters_test.go` with display-width-aware truncation tests for ASCII, CJK, and mixed content

## v0.1.34

### ✨ Features

- **Channels Remote TUI Client**
  - Replaced the plain-text WebSocket client with a full Bubble Tea TUI for the serve WebSocket channel
  - Markdown rendering with syntax highlighting via Glamour
  - Scrollable tool details modal (Ctrl+O), approval prompts (Enter/Esc), and question tool support
  - Plan update display, context pressure/budget warnings, and request timers
  - Native terminal scrollback for completed messages
  - Slash command support (`/clear`, `/mode`, `/model`, `/compact`, `/help`, etc.)
  - New `internal/serve/channels/remotetui` package: `app.go`, `render.go`, `input.go`, `remote.go`, `agent_events.go`, `approval.go`, `commands.go`, `formatters.go`, `tool_modal.go`, `events.go`

- **WebSocket Protocol Enhancements**
  - Added `question_request` / `question_response` events for plan-mode question tool over WebSocket
  - Added `plan_update` events with structured plan step data
  - Added `compaction_start` / `compaction_end` events for context compaction progress
  - `connected` event now includes `model` and `work_dir` metadata
  - `approval_request` event now carries `approval_tool` and `approval_args` for richer client-side display

- **Dispatcher Refactor**
  - Extracted `buildAgent()` from `runAgent()` for agent creation and cleanup, improving reuse between messaging and WebSocket paths

- **Provider Custom Headers**
  - Added `providers.<name>.headers` for custom HTTP headers on provider requests
  - Header values support the same `${ENV}` and opt-in `!cmd` resolution as `apiKey`

### 🧪 Tests

- Added WebSocket runtime server tests for connection, auth, chat, approval, question, and command flows

## v0.1.33

### ✨ Features

- **Multiple Project Skill Directories**
  - Skills manager now supports loading from both `.skills/` and `skills/` directories in the project root
  - Priority order: `.skills/` > `skills/` > global skills directory
  - New `NewManagerWithProjectDirs` constructor accepts explicit project dirs in priority order
  - New `ProjectSkillDirs` helper returns the standard project skill directory list
  - Updated all call sites: CLI, ACP, and serve
  - Added tests for multi-directory priority and plain `skills/` directory loading

## v0.1.32

### ✨ Features

- **Tool System Completeness**
  - Added full documentation for all registered tools: `jobs`, `kill`, `question`, `memory`, `cron`, and MCP dynamic tools
  - `jobs` tool: list and inspect background jobs started with `bash async=true`, with optional cleanup
  - `kill` tool: terminate a running background job by ID
  - `question` tool: AI can ask users multiple-choice questions during plan mode to clarify requirements
  - `memory` tool (Channels): persistent memory via `memory.md` with read/add/update/delete actions across sessions
  - `cron` tool (Channels/multi-agent): scheduled background tasks via sub-agents with `@daily`, `@weekly`, `@every N` schedules and one-shot support
  - MCP dynamic tools: tools/resources/prompts from MCP servers are auto-discovered and registered per session

- **Plan Mode Question Tool**
  - Added `question` tool, registered only in TUI + plan mode
  - AI can ask users multiple-choice questions; users select a preset option or type a custom answer
  - Helps clarify requirements before forming a plan, producing higher-quality proposals
  - Exposed via `QuestionHandler` optional interface (type assertion); does not pollute the public `Agent` interface

### 🐛 Bug Fixes

- **Bash Tool Output Safety**
  - Synchronous bash mode now enforces a 1 GB output limit using `limitedBuffer`, preventing OOM from unbounded `bytes.Buffer` growth

- **Channels `/compact` Command**
  - Implemented the `/compact` slash command for Channels messaging mode (previously a TODO stub)
  - Sets a `ForceCompact` flag on the session, consumed by the next agent run to trigger context compaction

- **Session Durability**
  - `writeEntry` now calls `f.Sync()` after writing, guaranteeing data survives crash or power loss
  - Corrupt session lines are now logged as warnings and skipped instead of blocking session load

- **Channels Approval Race Condition**
  - `ResolveApproval` now uses `select` to avoid writing to an already-consumed channel when timeout and approval race

- **Agent Sub-agent Panic Logging**
  - `sendParentEvent` now logs the panic value before recovering, aiding diagnosis of closed-channel races

- **Atomic File Write Cleanup**
  - `writeFileAtomic` no longer uses `defer os.Remove(tmpPath)` which would attempt to delete an already-renamed file; cleanup is now explicit on each error path

- **Agent Loop Detection Configurability**
  - `MaxConsecutiveNoText` (stuck-detection threshold) is now configurable via `AgentLoopConfig` (default 95)
  - Fixed incorrect error message that added pre- and post-warning counters together

- **Job Manager Auto-cleanup**
  - `AddJob` now garbage-collects finished jobs older than 30 minutes (checked every 5 minutes)

- **Cron Scheduler Error Logging**
  - `checkAndRun` now logs store errors instead of silently swallowing them

- **TUI Bash Output Display**
  - Compressed bash tool output summary by removing blank lines to prevent excessive vertical height in the TUI collapsed view

- **Vendored Search Tools**
  - Added fallback to system `grep` / `find` when embedded `rg` / `fd` are unavailable for the current architecture

### 📦 Distribution

- Added Linux LoongArch64 (`loong64`) build and packaging targets, including tarball, Debian, and npm package metadata

### ✅ Tests

- Added unit tests for `limitedBuffer` truncation, `JobManager` GC, `writeFileAtomic` cleanup, `sendParentEvent` panic recovery, `MaxConsecutiveNoText` configurability, session fsync durability, corrupt-line tolerance, and `QuestionTool` metadata/mode-filtering/execution/error-handling


## v0.1.31

### 🐛 Bug Fixes

- **Terminal Input**
  - Added Home/End cursor movement support in the TUI input box
  - Fixed the first submitted input being swallowed after canceling an approval prompt with Esc
  - Added command history navigation with Up/Down, including repeated selection through previous inputs

- **A2A Security and Reliability**
  - Changed the default A2A host from `0.0.0.0` to `127.0.0.1`
  - Added Bearer token authentication for `/a2a`, REST A2A routes, and SSE events while keeping the Agent Card public
  - Replaced timestamp-based A2A task IDs with collision-resistant random IDs
  - Made A2A task store reads and writes use cloned task snapshots to avoid accidental shared mutation

- **Path and Session Safety**
  - Fixed path containment checks to use path-aware boundaries instead of string prefix checks
  - Prevented context `extraFiles` from escaping the working directory
  - Encoded unsafe Channels session path components and enforced `allowed_work_dirs` during session creation
  - Restricted session deletion to `.db` files under the configured session directory

- **Auth, Approval, and Resource Limits**
  - Switched Channels HTTP/WebSocket token checks to constant-time comparison
  - Changed the Channels WebSocket client to send auth via `Authorization: Bearer ...` instead of query strings
  - Cleaned up pending ACP permission requests on timeout and propagated ACP write errors
  - Added request/body size limits for ACP, read-tool image files, WeChat responses, and cron A2A responses
  - Added timeouts to cron A2A HTTP calls

- **Memory, Context, and Concurrency**
  - Added locking to memory store operations
  - Fixed `memory.WriteAll()` path handling and kept memory update/delete scoped to the requested section
  - Cloned API model settings before per-request `temperature`/`top_p` overrides
  - Passed agent callback context/message snapshots instead of shared references
  - Serialized cron job state transitions through the job store

- **Configuration and Serve Hardening**
  - Gated `!command` API key resolution behind `VIBECODING_ALLOW_SHELL_CONFIG=1`
  - Fixed Serve CORS to echo only the allowed request origin
  - Added a startup warning when Serve listens beyond loopback in `yolo` mode without authentication
  - Hardened platform home/shell fallback behavior

### 🧪 Tests

- Added regression coverage for A2A auth, task ID uniqueness, task snapshot isolation, and persisted working task messages
- Added coverage for path traversal, unsafe session IDs, memory section operations, ACP cleanup, CORS behavior, UTF-8 truncation, and shell-config opt-in
- Ran focused package tests plus race tests for A2A, agent, serve, and cron

### 📝 Docs

- Updated A2A, Channels, Serve, configuration, and security docs for the new authentication and hardening behavior

## v0.1.30

### ✨ Features

- **Per-Provider HTTP Proxy**
  - Added `providers.<name>.httpProxy` to route individual providers through different HTTP proxies
  - Kept default environment proxy behavior when a provider does not set `httpProxy`

- **Google Gemini and Vertex Vendor Adapters**
  - Added native `google-gemini` and `google-vertex` providers using Google `streamGenerateContent`
  - Enabled base URL detection for Gemini API and Vertex AI native Gemini endpoints
  - Added default Google provider templates for Gemini API keys and Vertex bearer tokens
  - Updated provider documentation and lookup coverage for Google vendor names

- **Hosted Web Search Tool**
  - Added `--web-search` for CLI and ACP runs
  - Added top-level `webSearch` settings with `enabled`, `provider`, `providerType`, and `model`
  - Registered hosted `web_search` tools only when enabled, keeping them separate from local function tools
  - Added OpenAI Responses API mapping to `web_search`
  - Updated Responses web search mapping to provider-neutral `web_search`, so compatible custom providers are not required to be named `openai`
  - Added Anthropic Messages API mapping to `web_search_20250305`
  - Preserved `webSearch.model` as provider-neutral metadata for future routing and cost display

- **Default Provider Templates**
  - Added built-in default provider entries for OpenAI, Anthropic, and Xiaomi MiMo
  - Kept DeepSeek providers and `deepseek-openai` as the default provider/model
  - First-run `settings.json` now includes disabled web search configuration plus OpenAI/Anthropic/Xiaomi provider templates

### 🧪 Tests

- Added coverage for hosted web search tool serialization across OpenAI Responses and Anthropic Messages
- Added coverage for web search configuration defaults, CLI flag parsing, and hosted tool metadata propagation
- Added coverage for macOS default config directory resolution

### 🐛 Bug Fixes

- **macOS Config Directory**
  - Unified the default macOS global config directory with Linux at `~/.vibecoding`

- **Release Versioning**
  - Removed the default `dirty` suffix from npm and distribution package version detection
  - Normalized npm package metadata to `0.1.30`

## v0.1.29

### 🐛 Bug Fixes

- **NPM Package Wrapper**
  - Fixed `npm/bin/mothx` entry script to ensure installer packages ship the correct executable wrapper
  - Adjusted `build-npm.sh` and `build-npm-packages.sh` to include the wrapper consistently

## v0.1.28

### ✨ Features

- **Per-Model Temperature/Top-P Configuration**
  - Added `temperature` and `top_p` fields to `ModelConfig` and `Model` for per-model parameter tuning
  - Wired through OpenAI and Anthropic providers with `omitempty` — `nil` means use API default
  - Wired through provider factory, agent loop, and ACP mode
  - Serve supports per-request `temperature`/`top_p` override via `ChatParams`
  - When not configured, parameters are omitted entirely (no zero-value sent to API)

- **OpenAI Responses API Support**
  - Added a dedicated OpenAI Responses provider path under `api: "openai-responses"`
  - Supports Responses streaming, tool calls, reasoning summaries, and prompt cache parameters
  - Responses configuration is exposed under provider `responses` settings with default prompt cache enabled
  - Added model compat flags for `supportsPromptCacheKey` and `supportsReasoningSummary`

### 🧪 Tests

- Improved provider test coverage for OpenAI Responses API and Anthropic request parsing
- Reworked Anthropic tests to use in-memory HTTP mocks instead of port-binding test servers

### 📝 Docs

- Updated `AGENTS.md` version to v0.1.28

## v0.1.27

### ✨ Features

- **Serve Mode** (`mothx serve`)
  - New messaging channel mode for WeChat, Feishu, and WebSocket
  - Persistent per-user sessions with auto-archiving on `/new`
  - Default `yolo` mode for unattended operation
  - Smart approvals with tiered risk classification (low/medium/high)
  - User whitelist for platform access control
  - WebSocket streaming: real-time text_delta/think_delta/tool_call/tool_result/tool_diff/usage/done events

- **A2A Protocol** (`mothx a2a`)
  - New Agent-to-Agent protocol server (JSON-RPC 2.0 over HTTP + SSE streaming)
  - Standalone mode: `mothx a2a start` (port 8093)
  - Agent Card at `/.well-known/agent.json`
  - Task lifecycle: submitted → working → completed/failed/canceled
  - REST endpoints: `/a2a/send`, `/a2a/task`, `/a2a/task/cancel`, `/a2a/events`
  - **A2A Client**: `mothx a2a send <message>` to send tasks to other A2A servers
  - **A2A Discovery**: `mothx a2a discover <url>` to fetch remote Agent Cards
  - **A2A Scheduling**: Cron jobs support `--a2a-target` to schedule tasks to A2A servers

- **A2A Master Mode** (`--enable-a2a-master`)
  - Configure multiple remote A2A agents via `a2a-list.json`
  - Registers `a2a_dispatch` tool for the LLM to automatically dispatch tasks to remote agents
  - Supports global (`~/.vibecoding/a2a-list.json`) and project-level (`.vibe/a2a-list.json`) config
  - `--init-a2a-master-config` generates a sample config file
  - Disabled by default, requires explicit opt-in

- **A2A Config Initialization**
  - `mothx a2a --init-a2a-config` generates `a2a.json` config template
  - `mothx --init-serve` generates `serve.json` config template (existing)
  - `mothx --init-a2a-master-config` generates `a2a-list.json` config template
  - All `--init-*` flags support `--force` to overwrite existing files

- **Scenarios & Walkthroughs Documentation**
  - New `docs/scenarios.md` (zh + en) covering 9 practical usage scenarios
  - Covers: daily coding, CI integration, multi-agent, VS Code ACP, A2A server,
    A2A Master cross-machine dispatch, Serve HTTP, Channels messaging, combined modes

- **Documentation Overhaul**
  - `architecture.md`: added all missing modules (a2a/acp/serve/mcp/memory/messaging/vendored)
  - `tools.md`: added `a2a_dispatch` and `skill_ref` tool docs
  - `cli-reference.md`: added `--enable-a2a-master`, `--init-a2a-master-config`,
    `--init-serve`, `--force`, `a2a` subcommand docs
  - `README.md`: updated architecture diagram, added running modes overview

- **Pressure System**
  - Context Pressure: `EventContextPressure` fired at 55% context usage (configurable via `context_pressure_threshold`)
  - Budget Pressure: `EventBudgetPressure` fired at 20% remaining iterations (configurable via `budget_pressure_threshold`)
  - One-shot events: fire once per threshold crossing, not every turn
  - Messaging platforms receive pressure warnings via progress callback

- **Smart Approvals (Tiered Strategy)**
  - Low risk: auto-approve
  - Medium risk: auto-approve + notify user
  - High risk (WebSocket): send `approval_request`, wait for user `approval_response` (5min timeout)
  - High risk (messaging): auto-reject + notify user
  - Command risk classification: low/medium/high based on bash command patterns

- **Provider/Model Configuration**
  - `default_provider` / `default_model` in `serve.json` (overrides `settings.json`)
  - CLI flags `-p`/`--provider` and `-m`/`--model` for `mothx serve`
  - Priority: CLI flags > `serve.json` > `settings.json`

- **Multi-Agent Mode** (`--multi-agent`)
  - Enables sub-agent tools (spawn/status/send/destroy) in serve channel sessions
  - Configurable via `serve.json` `multi_agent` field or `--multi-agent` CLI flag

- **Sandbox Mode** (`--sandbox`)
  - Optional bwrap sandbox isolation (disabled by default)
  - Configurable via `serve.json` `sandbox` field or `--sandbox` CLI flag

- **MCP Integration**
  - Channels automatically loads MCP servers from global/project `mcp.json`
  - MCP tools registered per-session, connections auto-closed on session removal

- **Progress Events for Messaging Platforms**
  - Real-time tool execution progress sent to WeChat/Feishu during agent runs
  - Format: `[tool]: args ✅/❌` for tools, `💭 ...` for thinking process
  - Final summary sent after agent completes

- **Memory Tool**
  - `memory` tool with read/add/update/delete actions
  - Section-level operations (User Profile, Working Memory, Lessons Learned)
  - Defaults to `.vibe/memory.md` (project directory)
  - Lookup priority: `memory.path` config → `.vibe/memory.md` → `<GLOBAL_DIR>/memory.md`
  - `/api/memory` HTTP endpoint (GET/PUT) for memory access

- **Serve Channel Management**
  - `mothx serve` — start the unified API, Web UI, and channel runtime
  - `mothx serve init-config` — create `serve.json`
  - Web UI and serve APIs manage channel status, sessions, memory, webhooks, and cron jobs
  - `a2a start/stop/status/card` — A2A server management

### 📝 Changes

- WeChat iLink implementation with zero external dependencies (5 files: types/protocol/auth/crypto/wechat)
- Feishu bot with official SDK and WebSocket long-connection
- Shell hooks for pre/post tool call external scripts (JSON stdin/stdout)
- Webhook inbound routing with HMAC-SHA256 signature verification
- WebSocket uses `golang.org/x/net/websocket` (stdlib compatible)
- Serve runtime process lifecycle management

### 🐛 Bug Fixes

- **NPM Installer Packaging**
  - Fixed release packaging flow so `vibecoding-installer` always ships executable entry `bin/mothx`.
  - Added `scripts/npm-installer-wrapper.js` as the single source of wrapper logic, reused by both
    `scripts/build-npm.sh` and `scripts/build-npm-packages.sh` to avoid drift.
  - Adjusted `npm/.npmignore` and `npm/bin` handling to avoid shipping accidental build artifacts and to keep
    package manifests (`files`) explicit.

- **Channels Webhook Delivery and Filtering**
  - Webhook routes now treat unknown event types as non-matching unless the route explicitly allows `*`.
  - Added `delivery_target` to webhook routes so WeChat/Feishu delivery has a concrete recipient.
  - Updated webhook route listing and config templates to show the delivery target when present.

- **OpenAI Responses Thinking Mapping**
  - Mapped `--thinking xhigh` to `reasoning.effort: "high"` for the OpenAI Responses API.

### 🧪 Tests

- Reworked webhook router tests to wait on handler completion instead of sleeping, removing a race/flakiness source.
- Added coverage for webhook event rejection when the event type cannot be inferred.
- Added coverage for webhook delivery target handling.

## v0.1.26

### ✨ Features

- **Serve Mode** (`mothx serve`)
  - New HTTP server exposing a standard OpenAI Chat Completions API (`/v1/chat/completions`, `/v1/models`, `/health`)
  - Any OpenAI-compatible client (Cursor, Continue, Open WebUI, Python SDK, etc.) can connect directly
  - Streaming (SSE) and non-streaming responses fully supported
  - Backend powered by MothX agent loop with tool execution transparent to the caller

- **Multi-Session Support**
  - Built-in `SessionPool` for concurrent sessions, each with isolated agent, tools, and message history
  - Session association via `x_session_id` in request body; auto-created when absent
  - Configurable idle timeout (`session.idleTimeoutSeconds`) and max session limit (`session.maxSessions`)

- **Sub-Agent Support in Serve**
  - Optional `enableSubAgents` config to enable multi-agent orchestration in serve mode
  - Reuses existing `AgentFactory` / `AgentManager` / sub-agent tools with no core agent changes

- **Bearer Token Authentication**
  - Configurable via `serve.json` with `auth.enabled` and `auth.tokens` list
  - Disabled by default; `/health` endpoint always unauthenticated

- **Slash Commands via API**
  - `/clear`, `/mode`, `/model`, `/models`, `/sessions`, `/compact`, `/status`, `/skill`, `/skills`, `/help`
  - Triggered when the last user message starts with `/`; processed at the serve HTTP layer without invoking LLM
  - Responses use standard OpenAI format with `x_command` extension field

- **Tool Visibility Configuration** (`toolVisibility.mode`)
  - `"content"` (default): tool status sent as text in `content` field during streaming
  - `"sse_event"`: tool status sent as extended SSE events for custom clients
  - `"none"`: fully transparent, client sees only final text

- **System Prompt Handling** (`systemPromptMode`)
  - `"append"` (default): client system messages appended to built-in system prompt
  - `"ignore"`: client system messages discarded entirely

- **Security: allowedWorkDirs**
  - Directory whitelist for `x_working_dir` request-level overrides with path-separator-aware prefix matching
  - Three-layer security model: L1 auth + L2 directory control + L3 sandbox (bwrap)

- **Sandbox Support in Serve**
  - Configurable via `serve.json` `sandbox.enabled` / `sandbox.level` or `--sandbox` flag
  - Inherits detailed sandbox settings (allowedRead, deniedPaths, etc.) from `settings.json`

- **Serve Configuration** (`serve.json`)
  - Independent config file at `~/.vibecoding/serve.json`
  - Covers: listen address, auth, mode, sandbox, workingDir, allowedWorkDirs, session management, CORS, tool visibility, system prompt mode, request timeout, concurrency limit, logging
  - `mothx --init-serve` to generate template; `--force` to overwrite

- **Request Timeout & Concurrency**
  - `requestTimeoutSeconds` (default 1800s); streaming keeps alive as long as data flows
  - `maxConcurrentRequests` (default 0 = unlimited)

### 📝 Docs

- Added an API server proposal with full architecture, API design, security model, and implementation plan
- Updated `AGENTS.md` version note

## v0.1.25

### ✨ Features

- **Multi-Agent Mode**
  - Added opt-in `--multi-agent` support across CLI, TUI, and ACP mode
  - Added `AgentManager`, `EventRouter`, and per-agent registries so agents have isolated tools, job managers, sessions, messages, and context
  - Added `subagent_spawn`, `subagent_status`, `subagent_send`, and `subagent_destroy` tools for delegated background work
  - Added multi-agent prompt guidance and safeguards that prevent nested sub-agent spawning

- **Cron Task Support**
  - Added `internal/cron` with persistent cron store and scheduler coverage
  - Added `/cron` command entry points in multi-agent TUI workflows

- **Provider Vendor Adapter Layer**
  - Added vendor adapter registration in `internal/provider/vendor*.go`
  - Centralized provider/model creation in `internal/provider/factory`
  - Added vendor detection for DeepSeek, Xiaomi, Kimi, MiniMax, Seed, Qianfan, Bailian, Gitee, OpenRouter, Together, Groq, Fireworks, OpenAI, and Anthropic
  - Preserved existing provider config format while allowing vendor-specific defaults and generic OpenAI/Anthropic-compatible fallback
  - Added model `compat` handling for thinking formats, reasoning effort support, max token field selection, adaptive Anthropic thinking, and DeepSeek/Xiaomi assistant `reasoning_content`

### 🐛 Bug Fixes

- Auto-initialized sessions on first append so sub-agents can write session entries without requiring explicit prior initialization
- Fixed sub-agent tests to wait for background runs and clean up spawned agents before temporary directory removal
- Preserved ACP Anthropic cache-control behavior while moving provider creation to the shared factory

### 📝 Docs

- Updated `AGENTS.md` with provider factory and vendor adapter guidance
- Replaced the multi-agent implementation checklist with a completed architecture/status document
- Removed the obsolete root `todo.md`

### 🧪 Testing

- Added coverage for provider vendor resolution, provider factory creation, OpenAI/Anthropic compat behavior, multi-agent manager/router/sub-agent flows, cron storage/scheduler behavior, and session auto-initialization
- Verified with `make test` (`go test -v -race ./...`)

---

## v0.1.24

### ✨ Features

- **API Retry with Exponential Backoff**
  - Automatic retry for transient errors (5xx, network failures, rate limits) on initial HTTP connection
  - Exponential backoff: `baseDelay × 2^attempt`, capped at 30 seconds
  - Does NOT retry on user abort (`context.Canceled`), 4xx client errors, or mid-stream failures
  - Configurable via `retry` settings (`maxRetries`, `baseDelay`, `maxDelay`)
  - Agent forwards retry events as status updates visible in TUI and print mode
  - ACP mode also receives retry configuration

### 🐛 Bug Fixes

- **Anthropic `cache_control` Now Opt-In**
  - Changed default `cache_control` behavior to off (was auto-enabled for official API base URL)
  - Require explicit `cacheControl: true` in provider config to enable prompt caching
  - ACP provider creation explicitly enables `cache_control` for Anthropic

- **Anthropic Tool Result Grouping**
  - Fixed consecutive `toolResult` messages to be grouped into a single `user` message
  - Anthropic API requires all `tool_result` blocks for preceding `tool_use` to appear together before other content
  - Image blocks from tool results are now appended after all result blocks in the same message
  
- **Agent Tool-Only Loop Warning Ordering**
  - Moved the no-text tool-loop warning to be injected after tool results are appended
  - Keeps assistant -> toolResult -> warning message ordering valid for provider and session transcripts
  - Warning messages are now also persisted to session storage

### 📝 Docs

- **Comprehensive Configuration Documentation Rewrite**
  - Added missing settings: `cacheControl`, idle compression, full sandbox fields (`bwrapPath`, `allowedRead`, `allowedWrite`, `deniedPaths`, `passEnv`, `tmpSize`), `shellPath`, `shellCommandPrefix`, `sessionDir`, `skillsDir`, `theme`, `retry`
  - Documented shell command `apiKey` format (`!cmd`) for password manager integration
  - Fixed key resolution order: config `apiKey` first, then derived env var
  - Updated macOS config path documentation
  - Added top-level fields reference table with all defaults
  - Added per-platform defaults for sandbox paths and env vars
  - Improved examples with Claude provider `cacheControl`, idle compression, project-level overrides, and custom sandbox paths

### 🧪 Testing

- Added retry tests covering `IsRetryable`, `RetryDelay`, and `FormatRetryMessage`
- Added Anthropic provider tests for consecutive tool result grouping
- Added a regression test covering tool-only warning placement after tool results


---

## v0.1.23

### 🛠 Improvements

- **DeepSeek Thinking Format**
  - Added `thinkingFormat: "deepseek"` for DeepSeek reasoning requests
  - OpenAI-compatible requests now send `thinking: {type: "enabled"}` with `reasoning_effort`
  - Anthropic-compatible requests now send `thinking: {type: "enabled"}` with `output_config.effort`
  - Kept `thinkingFormat: "xiaomi"` as the legacy thinking-only format

### 🧪 Testing

- Added provider tests covering the new `deepseek` thinking format for both OpenAI- and Anthropic-compatible requests

### 📝 Docs

- Updated `anthropic-api` skill and configuration docs for the new `thinkingFormat` option

---

## v0.1.22

### ✨ Features

- **CLI/TUI MCP Auto-Loading**
  - CLI/TUI startup now loads global and project `mcp.json`, connects configured MCP servers, and registers MCP tools before the agent tool list is frozen

### 🐛 Bug Fixes

- **Markdown Rendering Style**
  - Switched CLI print mode and TUI markdown rendering from Glamour auto-style detection to the fixed `dark` style for more consistent terminal output

### 🧪 Testing

- Added MCP config loader coverage for placeholder template filtering

### 🛠 Improvements

- **Shared MCP Runtime**
  - Moved MCP connection/tool registration out of ACP-only code into a shared runtime used by ACP and normal CLI/TUI sessions
  - Starter-template placeholder MCP servers are ignored during automatic startup loading

---

## v0.1.21

### ✨ Features

- **Plan/Apply Workflow**
  - Added a built-in `plan` tool for structured task plans with `pending`, `running`, `done`, and `failed` step statuses
  - TUI now shows the current task plan and records plan updates in the transcript
  - Print mode and ACP now surface plan updates for non-interactive and editor-client flows

- **Apply Confirmation**
  - Added `approval.confirmBeforeWrite` to require approval before `write` and `edit` in agent mode
  - Enabled write/edit confirmation by default in generated settings
  - TUI approval prompts summarize write content by byte size instead of dumping full file content

- **MCP Config Commands**
  - Added `/init_mcp` to create project/global `mcp.json` with `basic`/`full` templates and optional `--force`
  - Added `/mcps` to list MCP servers from global and project `mcp.json` files
  - MCP config is now maintained in standalone `mcp.json` (separate from `settings.json`)

### 🧪 Testing

- Added coverage for the `plan` tool and write/edit approval gating
- Added HTTP-based MCP integration tests for tool/resource/prompt registration and callback paths
- Added SSE-based MCP integration tests for stream callbacks and message endpoint request/response flow

### 🛠 Improvements

- **ACP MCP Hardening**
  - Added MCP transport support for `http` and `sse` (alongside existing `stdio`)
  - Added MCP initialize/tool-discovery timeouts to avoid hanging ACP sessions
  - Added paginated `tools/list` fetching with upper page bounds
  - Added MCP `resources/*` and `prompts/*` discovery and tool registration
  - Added duplicate MCP server-name detection and MCP tool-name de-duplication
  - Added MCP inbound request/notification handling (`ping`, progress/logging/cancel notifications)
  - Added bridge for inbound `sampling/createMessage` to the active ACP provider/model
  - Added stricter close/error propagation

---

## v0.1.20

### ✨ Features

- **Structured File Change Reporting**
  - `write` and `edit` now attach structured file diff metadata to tool results
  - TUI tool details show full unified diffs while collapsed tool rows keep a compact `+N -N` summary
  - Print mode now emits clear file change summaries for non-interactive runs
  - ACP tool updates include diff metadata in raw output for compatible clients

### 🧪 Testing

- Added coverage for structured diff metadata from `write` and `edit`

---

## v0.1.19

### ✨ Features

- **TUI Tool Details Modal**
  - Replaced `Ctrl+O` toggle-expand with a scrollable full-screen modal overlay showing all tool calls and results
  - Supports PgUp/PgDn, Up/Down, Home/End navigation; Esc/Ctrl+O/q to close
  - Tool headers now display file paths; removed content truncation in tool args display
  - Write tool results show diff summary in the one-line summary line
  - Key input is blocked while the modal is open to prevent accidental actions

- **Write Tool Diff Summary**
  - `write` tool now computes LCS-based line-level diff when overwriting files
  - Returns structured diff info (`+N -N` with line ranges) in the tool result
  - Skips diff computation for very large files (>200K line pairs) to avoid memory pressure

### 🛠 Improvements

- **Unified Shell Args Across Sandbox Backends**
  - All sandbox backends (`none`, `mac`, `windows`) now use `platform.ShellArgs()` for cmd.exe/PowerShell argument construction
  - Fixes Windows cmd.exe and PowerShell commands in sandboxed execution modes
  - `ShellArgs` now normalizes shell name to lowercase before matching

### 🧪 Testing

- Added `TestNoneSandboxWrapCommandUsesPlatformShellArgs` covering cmd.exe and PowerShell argument generation

---

## v0.1.18

### 🐛 Bug Fixes

- **TUI Nil Pointer Panic**
  - Fixed a nil pointer panic in `printMessageOnce` when `printedMessageIdx` map was not initialized
  - Added nil check before accessing the map in the message printing logic

- **Stream Commit Before Tool Execution**
  - Added `commitActiveStream()` method to flush streaming content (thinking and assistant messages) to output before tool execution
  - Now properly commits active stream before `EventToolCall` and `EventToolApprovalRequest` handling
  - Ensures thinking and partial assistant responses are visible when tools run or approval is requested

### 🧪 Testing

- Added `TestHandleAgentEventCommitsStreamBeforeApproval` regression test for stream commit ordering

---

## v0.1.17

### 🛠 Improvements

- **TUI Native Scrollback**
  - Reworked TUI history rendering so completed messages are printed into the terminal's native scrollback instead of a fixed-height viewport
  - Removed the virtual scrollbar and mouse-capture approach; mouse wheel scrolling now uses normal terminal history behavior
  - Kept live streaming content, input, footer, context/cache status, and tool output controls in the Bubble Tea view

- **TUI Request Timers**
  - Added per-request elapsed time display while a response is running
  - Footer now keeps the last request duration after completion

- **Event Loop Decoupling**
  - Added shared agent event consumption helpers
  - Split the TUI agent-event bridge out of the main app file and reused the event loop from CLI print mode

- **Windows Console Compatibility**
  - Enabled Windows virtual terminal console modes where available for better PowerShell rendering on Windows 10

### 🐛 Bug Fixes

- Fixed a TUI startup deadlock caused by printing initial/session history before Bubble Tea had started consuming program messages
- Fixed an agent message-history data race found by `go test -race`
- Fixed mock provider cancellation handling for already-canceled contexts

### 🧪 Testing

- Full `make test` now passes with race detection
- Added TUI regression coverage for startup history printing without blocking
- Hardened tests that depend on local HTTP listeners or default home-directory session paths in restricted environments

---

## v0.1.16

### 🛠 Improvements

- **Session Open by ID or Path**
  - New `OpenByPathOrID` function allows opening sessions by either file path or session ID
  - `OpenByID` now supports prefix matching with ambiguity detection
  - `ContinueRecent` initializes new sessions immediately so they are ready for messages

- **Session Save Error Handling**
  - `AppendMessage` and `AppendCompaction` now return errors to the caller
  - Agent loop surfaces session-save failures as `EventError` instead of silently dropping them

- **Vendored Tool Test Guard**
  - Makefile `test` target now depends on `prepare-vendored` and a new `test-vendored` check
  - Tests fail early with a clear message if `rg`/`fd` binaries are missing for the current platform

### 🧪 Testing

- Added CLI flag parsing tests for root and ACP subcommands
- Added settings merge tests covering project overrides and environment variables
- Added session tests for `OpenByPathOrID`, prefix ambiguity, corrupt lines, and parent chain tracking

---

## v0.1.15

### 🐛 Bug Fixes

- **Vendored Search Tool Availability**
  - Fixed `grep` and `find` so they prepare embedded `rg` / `fd` binaries on demand instead of failing when vendored tools have not been extracted yet
  - Restored executable permissions for already-extracted vendored binaries to avoid `permission denied` failures on reuse

- **Bash Tool Result Handling**
  - Fixed bash tool responses to report stdout, stderr, working directory, and exit code in a stable structured format
  - Preserved non-zero command exits as normal tool results with explicit `exit_code` output instead of mixing shell failures into transport-level errors
  - Standardized empty stdout/stderr rendering as `(no output)` for more predictable downstream handling

---

## v0.1.14

### 🐛 Bug Fixes

- **Session Continue Context Injection (`-c`)**
  - Fixed a TUI state coupling issue where continued sessions could display history but fail to inject that history into the model context for follow-up prompts
  - Split session history state into separate UI-display and agent-injection flags to ensure resumed conversations keep prior context
  - Reset agent history-injection state consistently when the agent is recreated (abort/mode/model/skill/session switches)
  - Added missing TUI handlers for `EventStatus` and `EventMessageStart` so status/warning messages are rendered reliably

### 🧪 Testing

- Added regressions that cover:
  - history injection when UI history is already loaded
  - real startup ordering (`Init()` history load, then follow-up input) for continued sessions

---

## v0.1.13

### 🐛 Bug Fixes

- **Streaming Event and Tool Call Robustness**
  - Preserved terminal agent events in the TUI event listener so done/error/status handling is not dropped during streaming
  - Added Anthropic thinking signature streaming and replay support, and surfaced SSE `error` events as proper stream errors
  - Generated fallback tool call IDs for OpenAI-compatible streamed tool calls when providers omit IDs, with an extra defensive fallback in the agent loop

- **Sandbox Environment Inheritance**
  - Fixed `none` sandbox execution so commands inherit the parent environment, including variables such as `$HOME`
  - Clarified bubblewrap environment override handling to match runtime behavior

### 🛠 Improvements

- **Vendored Tool Build Flow**
  - Unified build and distribution targets around `prepare-vendored`
  - Removed the old `vendored-tools` release step and deprecated the stale extract helper script

- **Documentation Site Layout**
  - Expanded the docs landing page content width for better large-screen readability

- **Package Metadata**
  - Updated npm package versions for installer packages

### 📖 Documentation

- Updated README and docs landing pages to highlight safer approval handling, unified cache metrics, and consistent provider debugging
- Simplified `AGENTS.md` guidance for repository agents

### 🧪 Testing

- Added bash tool output coverage for stdout-only, stderr-only, no-output, and non-zero exit cases
- Added TUI regression tests for status/warning rendering and done/error event passthrough
- Added OpenAI streaming regression coverage for tool calls with missing IDs

---

## v0.1.12

### 🐛 Bug Fixes

- **Unified Cache Hit Rate Semantics**
  - Restored cache hit rate calculation to use the full prompt footprint (`CacheRead / TotalInputTokens()`)
  - Aligned CLI print mode token display with TUI cache-aware totals
  - Updated Anthropic cache tests and shared provider usage tests to match the unified definition

- **Approval Safety in Non-Interactive and YOLO Flows**
  - Made `bashBlacklist` effective in approval checks with higher priority than `bashWhitelist`
  - Blacklisted bash commands now still require approval in `yolo` mode
  - `--print` mode now fails fast instead of auto-approving commands that would require user confirmation

### 🛠 Improvements

- **Debug Output Consistency**
  - `--debug` now also enables provider-level request/response debug output
  - Applied the same behavior to ACP mode

- **Cross-Platform Path Handling**
  - Replaced string-based `.skills` path construction with `filepath.Join(...)`

### 📖 Documentation

- Updated CLI reference to document stricter `--print` behavior and debug output behavior
- Updated configuration guide for approval precedence and `VIBECODING_DEBUG`
- Updated root README and documentation landing pages to highlight safer approval handling, unified cache metrics, and provider debug behavior

### 🧪 Testing

- Added approval behavior tests for whitelist/blacklist and `yolo` mode
- Added print mode regression test for approval-required tool calls
- Expanded cache-related provider tests to cover the unified cache hit rate definition

---

## v0.1.11

### 🛠 Improvements

- **Command Structure Refactoring**
  - Extracted root command creation into separate function for better testability
  - Added unit tests for command initialization and configuration
  - Improved code modularity and maintainability

### 📖 Documentation

- **License & Documentation Updates**
  - Added MIT license file
  - Added Chinese README (README_zh.md) for broader accessibility
  - Updated npm package versions

---

## v0.1.10

### ✨ Features

- **ACP Support Documentation**
  - Added ACP (Agent Client Protocol) support documentation to READMEs
  - MothX can run as an ACP stdio agent for editor integrations
  - Compatible with VS Code, Zed, and JetBrains IDEs (IntelliJ IDEA/WebStorm) via ACP plugins

### 📖 Documentation

- Updated main README.md with ACP support feature
- Updated English README with features section
- Updated Chinese README with features section

---

## v0.1.9

### 🐛 Bug Fixes

- **TUI Deferred Render Goroutine Safety**
  - Fixed `scheduleRender` calling `updateViewportContent` from background goroutine without marshalling back to Bubble Tea's UI goroutine
  - Added `renderRequestMsg` type and `program.Send()` to properly marshal UI updates
  - Added `program *tea.Program` field and `SetProgram()` method for deferred UI scheduling

### 🛠 Improvements

- **TUI Abort Clears Queued Input**
  - Clear input queue and reset input state on manual abort and mode change
  - Prevents buffered keystrokes from executing after abort

- **Assistant Slot Reservation**
  - Added `EventTurnStart` handling to reserve display slot before text deltas arrive
  - Prevents tool output from shifting assistant message index mid-stream
  - Added empty raw markdown check in `updateViewportContent`

- **Tool Prompt Snippets**
  - Added "(preferred for ...)" hints to `read`, `ls`, `grep`, `find` tool descriptions
  - Reordered tool registration: read-only tools registered before write/edit/bash

### 🧪 Testing

- Added `TestHandleAgentEventReservesAssistantSlotBeforeTextDelta` test
- Added `TestAbortClearsQueuedInput` test

---

## v0.1.8

### 🐛 Bug Fixes

- **Token Counting with Cache-Aware TotalTokens**
  - Fixed Anthropic `TotalTokens` calculation to include `CacheRead` and `CacheWrite` tokens
  - Added `PromptTokens()` and `TotalInputTokens()` helper methods to `Usage` struct
  - Updated `CacheInfo()` to use `TotalInputTokens()` as denominator for accurate cache hit rates
  - Updated TUI to display correct token counts including cache tokens

### 🧪 Testing

- Added comprehensive tests for `PromptTokens()` and `TotalInputTokens()` helper methods
- Updated Anthropic provider tests with `TotalTokens` validation

---

## v0.1.7

### 🐛 Bug Fixes

- **Anthropic Provider Tool Use Serialization**
  - Fixed `tool_use` content blocks missing `input` field when tool has no arguments
  - Changed `Input` field from `map[string]interface{}` to `*map[string]interface{}` so `omitempty` only checks nil pointer, not empty map
  - Fixes API errors when using models like Xiaomi MiMo with Anthropic-compatible endpoints

---

## v0.1.6

### ✨ Features

- **Session Management Command**
  - Added `/sessions` command for browsing and managing project sessions
  - Supports listing, switching, clearing, and deleting sessions
  - Shows session details including file path and message count

### 🐛 Bug Fixes

- **Sandbox Initialization**
  - Fixed sandbox initialization validation and bwrap multiarch compatibility
  - Improved error handling for sandbox setup

### 📖 Documentation

- Updated AGENTS.md with current version information
- Formatted Go code for consistency

---

## v0.1.5

### ✨ Features

- **DeepSeek V4 Default Models**
  - Updated default model specs to DeepSeek V4 (Flash and Pro)
  - 1M context window, up to 384K max output tokens
- **Install Script Improvements**
  - Install scripts now show config directory path on completion

### 🐛 Bug Fixes

- **Windows IME Support**
  - Fixed Windows IME (CJK input) support in terminal
  - Fixed shell command resolution on Windows
  - Added config loading diagnostics for troubleshooting
- **Musl Deb Packages**
  - Fixed invalid dpkg architecture names for musl deb packages

### 🛠 Improvements

- **Configuration Simplification**
  - Removed `auth.json` support — all credentials now in `settings.json` only
  - Cleaner config path with single source of truth

### 📖 Documentation

- Clarified that OpenAI/Anthropic API-compatible services are also supported
- Removed all `auth.json` references from docs and install scripts
- Added expanded Windows `%APPDATA%` path examples
- Clearly distinguished Windows vs Linux/macOS config paths

---

## v0.1.4

### ✨ Features

- **Linux musl Build Support**
  - Added `make build-linux-musl` target for statically linked musl binaries (amd64 + aarch64)
  - musl tarballs produced via `dist-tarball` and `dist` targets
  - musl Debian packages produced via `dist-deb` target (amd64-musl / arm64-musl)
  - npm packages: `vibecoding-installer-linux-musl-x64` and `vibecoding-installer-linux-musl-arm64`
  - npm uses `libc` field for proper musl/glibc resolution (npm >=9.4)
  - postinstall.js auto-detects musl vs glibc on Linux

---

## v0.1.3

### ✨ Features

- **Versioning Rules**
  - Added version number management rules with base-10 carry-over (e.g., v0.1.9 -> v0.2.0)
  - Documented changelog rules: only write in docs/en/changelog.md and docs/zh/changelog.md
  - No separate release notes files allowed

---

## v0.1.2

### ✨ Features

- **Prompt Cache Optimization**
  - Implemented prompt cache optimization following LLM_Agent_Cache.md strategy
  - Cache system prompts and static context across multiple turns
  - Reduces API costs by reusing cached tokens for repeated prefixes

- **TUI Markdown Syntax Highlighting**
  - Assistant messages in TUI now have markdown syntax highlighting
  - Code blocks, headers, and formatting are visually distinguished
  - Improves readability of LLM responses

### 🐛 Bug Fixes

- **Security & Correctness**
  - Resolved critical security, race condition, and correctness issues
  - Addressed high and medium severity correctness issues across codebase
  - Removed dead code and improved overall code correctness

- **TUI Stability**
  - Fixed TUI startup hang caused by `clearStdin` blocking on unsupported stdin
  - Fixed TUI assistant message rendering broken by ANSI escape codes in prefix check

### 🛠 Improvements

- **Code Quality**
  - Addressed remaining medium severity issues across codebase
  - npm package versions updated

---

## v0.1.1

### ✨ Features

- **Cache Hit Rate Display**
  - Footer now shows cumulative cache hit percentage across all turns
  - Cache percentage is highlighted when hit rate ≥ 50% for quick visibility
  - Per-turn cache read/write counts displayed in token usage line

- **Proxy Compatibility**
  - Handle proxies that send usage fields in `message_delta` instead of `message_start`
  - Handle OpenAI proxies that split usage across multiple SSE chunks (first-wins per field)
  - Fixed missing space before `$` in print-mode token summary line

### 🛠 Improvements

- **Code Quality**
  - Extracted `Usage.CacheInfo()` to eliminate 3× duplicated cache display logic
  - npm package versions now use `v`-prefixed format (e.g. `v0.1.1`)
  - Normalized JSON formatting across all npm package.json files

### 🧪 Testing

- Added 37 unit tests for `CacheInfo()`, `formatCachePercent()`, and `renderFooter()` cache section
- Added 12 httptest integration tests for Anthropic and OpenAI SSE cache token parsing

---

## v0.1.0

### ✨ Features

- **Xiaomi MiMo Thinking Format Support**
  - Added `thinkingFormat` configuration option for Xiaomi MiMo API
  - OpenAI provider: MiMo endpoints use `thinking: {type: "enabled"}` format
  - Anthropic provider: MiMo endpoints omit `budget_tokens`
  - URL auto-detection: auto-detects `xiaomimimo` endpoints when `thinkingFormat` is not set
  - Debug logging: enabled via `VIBECODING_DEBUG` environment variable

### 🛠 Improvements

- **Configuration Flexibility**
  - `thinkingFormat` passed from config to provider, no longer relies solely on URL detection
  - Anthropic `budget_tokens` changed from required to optional (pointer type + `omitempty`)

---

## v0.0.9

### ✨ Features

- **Image Support in Tools**
  - `read` tool now supports reading image files (PNG, JPEG, GIF, WebP)
  - Images are returned as base64-encoded data with MIME type information
  - LLMs can now analyze and understand image content
  - Supported formats: `.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`

- **Rich Content Tool Results**
  - New `ToolResult` struct supports both plain text and rich content blocks
  - Tools can now return text + images in a single result
  - New factory functions: `NewTextToolResult()` and `NewImageToolResult()`

- **Model Switching**
  - `/model <id>` command allows switching models in interactive mode
  - `/model` without arguments shows current model and available options
  - Agent resets automatically when model is switched

- **Enhanced Help System**
  - `/help` command now shows detailed command descriptions
  - Added keyboard shortcuts reference (Tab, Esc, Ctrl+O, PgUp/PgDn)

### 🛠 Improvements

- **Context Token Estimation**
  - Fixed double-counting issue when both `Content` and `Contents` are present
  - Image tokens estimated as ~1200 tokens per image

- **Provider Message Conversion**
  - OpenAI: Images in tool results sent as supplementary user messages
  - Anthropic: Images sent as separate user messages alongside tool_result

### 🧪 Testing

- Added `TestReadToolImage` test case for image reading functionality
- All tool tests updated for new `ToolResult` return type

---

## v0.0.8

### ✨ Features

- **NPM Multi-Architecture Split Packages**
  - Split the npm package from a single all-platform bundle (~60MB) into 6 platform-specific packages (~10MB each)
  - Users now only download the binary for their current platform, reducing install size by 83%
  - Uses npm `optionalDependencies` + `os`/`cpu` fields for automatic platform matching
  - Main package `vibecoding-installer` is only ~2KB, links the correct platform package via `postinstall`

### 🛠 Improvements

- **Build System**
  - Added `scripts/build-npm-packages.sh` to generate platform-specific npm packages
  - Added `make npm-packages`, `make npm-pack`, `make npm-publish-all` targets
  - `sync-npm-version.sh` now syncs versions across all platform packages

---

## v0.0.7

### ✨ Features

- **Cross-Platform Sandbox Support**
  - Sandbox now supports macOS and Windows in addition to Linux
  - macOS uses `sandbox-exec` for process isolation
  - Windows uses restricted process creation without network access
  - Platform-specific sandbox implementations selected automatically

- **Repository Rename**
  - Module path renamed to `github.com/startvibecoding/mothx`
  - All imports, documentation, and scripts updated accordingly

### 🛠 Improvements

- **Platform-Specific Process Handling**
  - Extracted `SysProcAttr` configuration into build-tagged files (`bash_unix.go`, `bash_windows.go`)
  - Background child process cleanup now works correctly on all platforms
  - `Setpgid` only set on Unix systems; Windows uses `CREATE_NEW_PROCESS_GROUP`

### 📖 Documentation

- Updated all GitHub URLs to new repository location
- Added v0.0.6 and v0.0.7 release notes

---

## v0.0.6

### 🛠 Improvements

- **Bash Tool Reliability**
  - Fixed background child process hanging issue
  - Added `WaitDelay` to prevent shell from waiting indefinitely on background children
  - Properly handle `exec.ErrWaitDelay` errors

- **NPM Installation**
  - Added npm package for installation via `npm install -g vibecoding-installer`
  - Automatic binary download during `postinstall`

### 📖 Documentation

- Added npm installation instructions
- Removed redundant markdown files from docs root
- Added v0.0.5 release notes

---

## v0.0.5

### ✨ Features

- **Non-root Installation**
  - `install.sh` now supports installation without root or sudo
  - Auto-detects writable install directory: uses `/usr/local/bin` if writable, otherwise falls back to `~/.vibecoding/bin`
  - Removes all `sudo` calls — user-level installation never requires elevated privileges

- **Automatic PATH Setup**
  - Auto-detects user's shell (bash, zsh, fish) and configures PATH in the appropriate config file
  - Supports `.bashrc`, `.bash_profile`, `.zshrc`, `.zshenv`, `config.fish`, and `.profile`
  - Skips configuration if PATH entry already exists (no duplicates)
  - Fish shell uses `set -gx PATH` syntax; bash/zsh use `export PATH=...`

### 🛠 Improvements

- **Environment Variables**
  - `INSTALL_DIR` — override the install directory (unchanged)
  - `AUTO_SETUP_PATH=0` — disable automatic PATH configuration
  - Better error messages for permission issues

- **Install Experience**
  - Shows install directory and PATH auto-setup status at the start
  - Cleaner output with colored status messages

### 📖 Documentation

- Added v0.0.5 release notes

---

## v0.0.4

### ✨ Features

- **Agent Mode Approval Mechanism**
  - Bash commands in Agent mode now require user approval
  - Configurable `bashWhitelist` for auto-approved command prefixes
  - Configurable `bashBlacklist` for commands always requiring approval
  - TUI displays approval prompt; user responds with `y`/`yes` or `n`/`no`
  - Approval requests can be cancelled via `abort`

- **Mode Permission Matrix**
  - Plan mode: Read-only tools (read, grep, find, ls)
  - Agent mode: Read/write auto-execute, bash requires approval
  - YOLO mode: All tools auto-execute
  - Updated system prompts with explicit permission matrix

### 🛠 Improvements

- **Default Approval Whitelist**
  - Default whitelist: `go`, `make`, `git`, `npm`, `yarn`, `node`, `python`, `pip`
  - Customizable in `settings.json`

- **Mode Switch Feedback**
  - Mode switching now shows detailed permission descriptions
  - `/mode` command displays full permission list for current mode

### 📖 Documentation

- Added approval configuration section
- Updated security docs with approval mechanism details
- Added v0.0.4 release notes

---

## v0.0.3

### ✨ Features

- **Session History Loading**
  - Display session info (file path and message count) when continuing or opening sessions
  - Load and display historical messages from previous sessions in TUI
  - Load history messages into agent context for continuity
  - Reset agent on abort to ensure clean state for next request

### 🛠 Improvements

- **Build & Distribution System**
  - Restructured Makefile with clear per-platform build and dist targets
  - Added `dist-linux`, `dist-darwin`, `dist-windows` targets
  - Added `build-zip.sh` for Windows zip packages
  - Added `checksums` target for release verification
  - Updated `build-deb.sh` and `build-tarball.sh` to support all platforms

### 📖 Documentation

- Added GitHub repository button in documentation site header
- Added v0.0.2 release notes

---

## v0.0.2

### ✨ Features

- **One-line Installation Scripts**
  - `install.sh` for Linux/macOS - downloads from GitHub Releases automatically
  - `install.ps1` for Windows PowerShell - supports custom install directory via `VIBECODING_INSTALL_DIR`
  - Both scripts detect platform/architecture, verify checksums, and configure PATH

- **Documentation Redesign**
  - Redesigned with Google Material Design style
  - Default language changed to English
  - Added hash routing for easy document sharing (e.g., `#/en/README`, `#/zh/configuration`)
  - Added logo to header and README

- **Brand Assets**
  - Added `docs/assets/icon.svg` (512×512) for packaging
  - Added `docs/assets/mothx.png` for README and small displays
  - Minimal, professional design with slate color palette

- **Build System**
  - Added `make build-windows` target (amd64 + arm64)
  - Added `make build-linux` and `make build-darwin` targets
  - Updated `make build-all` to use platform-specific targets

- **Documentation**
  - Added `docs/en/skills.md` for Skills system
  - Updated installation instructions in README and getting-started guides

### 🐛 Bug Fixes

- Moved assets to `docs/assets/` for proper GitHub Pages deployment

---

**Full Changelog**: https://github.com/startvibecoding/mothx/compare/v0.1.26...v0.1.27
