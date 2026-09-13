# Changelog (Current Version)

This file contains the changes for the **current version only**. The full history of all versions lives in [docs/en/changelog.md](en/changelog.md).

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

- **WebUI: Windows Native Directory Picker No Longer Corrupts Chinese/Full-Width Directory Names**
  - `/api/select-directory` outputs the selected path from a PowerShell `FolderBrowserDialog` on Windows. Redirected stdout of Windows PowerShell 5.1 defaults to the ANSI/OEM code page (GBK on Chinese systems), so the Go side read non-UTF-8 bytes and Chinese or full-width directory names came back corrupted and failed path resolution. The picker script now forces `[Console]::OutputEncoding` to UTF-8 before writing the selection (pwsh 7 already defaults to UTF-8 when redirected, so both hosts now behave identically).
  - Picker output only strips the trailing newline instead of applying Unicode-aware `TrimSpace`: directory names that legitimately start or end with a space or a full-width space (U+3000) are no longer silently truncated.

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
- WebUI: new Windows directory-picker regression tests asserting the script forces UTF-8 output encoding before writing the selection and passes the default path through the UTF-16 environment block, plus output-trimming coverage for a path ending in a full-width space.
- Expert Teams: Runtime binding/fork, named-member events, TUI and Serve no-direct-run guards, ACP bind/fork process coverage, Desktop projection, and cross-entry ESM idle-gate coverage.
- Channels: an all-selectable-tools contract test verifies that every available persisted tool selection is present in the resolved session registry.
- Expert Teams: member question → lead → `subagent_answer` round trip, refusal of a question that is no longer pending, mailbox ownership (session lead vs. auxiliary roles, scheduled jobs, and a team-bound ESM worker), and non-team sessions delivering member notifications without holding the run open.
- Agent loop: terminal-state coverage for a run cancelled during the member wait, and for a truncated turn recovered by a follow-up turn.
- Channels: the session mailbox shared by the sub-agent tools and the lead, plus a partial tool selection that must not be resurrected by re-registration.
- Delivery: reopen refusal for permanent failures, verified through both the operator entry and the persistence fence.
- Delivery: the WebUI endpoints cover listing, a successful retry, and the permanent/in-flight/unknown refusals; the Desktop projection covers the same verdict and its bilingual strings.
- Runtime: the orphan-recovery concurrency test now proves overlapping workers with a rendezvous instead of a timing window, so it no longer flakes under load.
