# Changelog (Current Version)

This file contains the changes for the **current version only**. The full history of all versions lives in [docs/en/changelog.md](en/changelog.md).

## v1.3.107

### ✨ New Features

- **源盾 (Yuandun) is now the default SkillHub marketplace.** MothX adds a native 源盾 client for search, detail, files, security audit, and downloads, registers it ahead of SkillHub.cn and ClawHub.ai, and migrates the legacy `skillhub.cn` default. The market is projected through ACP, Serve, the TUI, the Web UI, and Desktop, which also gain a refreshed marketplace layout and a right-side detail drawer. Downloads identify MothX with `X-Client-Source: MothX`, and archives carrying `X-Skill-SHA256` are verified before extraction. Suspected (not confirmed) security findings use an amber warning tone so they stay distinct from risky results.

- **`mothx maintenance` inspects and compacts the session database.** `status` reports transcript/tool-record counts, database size, and reclaimable free pages; `vacuum` runs SQLite VACUUM and reports the result, with `--dry-run` to preview. The command never deletes messages or prunes tool-execution records; the scheduled maintenance job remains the only path that can bound terminal tool records and backfill the search index.

- **New Gitee/Moark Models: `kimi-k3.1-pro` and `kimi-k3.1-pro-highspeed`**
  - Added `kimi-k3.1-pro` and `kimi-k3.1-pro-highspeed` to the `gitee` and `moark` providers: thinking is enabled by default with Kimi's `low`/`high`/`max` effort scale (MothX's `minimal`/`low` map to `low`, `medium`/`high` to `high`, and `xhigh`/`max` to `max`), a 1M context window, and text+image input. Both use the verified `reasoning_effort` parameter through the `kimi` thinking format.

- **New Gitee/Moark Model: `MiniMax-M3.1-Flash`**
  - Added `MiniMax-M3.1-Flash` to the `gitee` and `moark` providers: thinking is enabled by default through the standard `reasoning_effort` parameter, with a 1M context window and text+image+video input; no default max_tokens is sent.

### 💥 Behavior Changes

- **Gitee/Moark model lists are reordered for discoverability.** The `gitee` and `moark` presets now lead with `deepseek-v4.1-flash-1017` and `deepseek-v4.1-flash`, followed by `qwen3.8-flash`, `qwen3.8-max`, `glm-5.3-flash`, `glm-5.3`, `MiniMax-M3.1-Flash`, `step-3.7-flash`, and `auto`; the remaining models follow in natural alphabetical order. Because preset order is also the fallback when no default model is configured, an unconfigured Gitee/Moark session now defaults to `deepseek-v4.1-flash-1017`.

- **The Serve session list is bounded by default.** `/api/sessions` without `limit` now returns the first 100 sessions plus `total`, `limit`, `offset`, and `hasMore`; pass `limit`/`offset` to page through history. `scope=active` and `/api/sessions/active` project only the in-memory session pool instead of loading all history, and per-session MCP configuration resolves its working directory by ID.

- **Session storage maintenance is scheduled without deleting transcript messages.** MothX now projects a daily `mothx-maintenance:session-storage` job that preserves every `entries`/`sub_entries` transcript row forever. It may prune only completed/abandoned `tool_execution_records` older than `maintenance.toolExecutionRetentionDays` (default `0`, keep forever), always keeps running/interrupted/retry-requested records so uncertain tool side effects stay fenced, runs `PRAGMA optimize`, and optionally runs `VACUUM` when `maintenance.vacuumSessionStorage` is enabled.

- **SQLite startup integrity checks are now explicitly opt-out.** MothX keeps running `PRAGMA quick_check` by default before using the session database. Very large databases can skip the fixed opening cost with `MOTHX_SQLITE_QUICK_CHECK=off`; `mothx doctor` still runs an on-demand read-only integrity check and reports `sessions.integrity`.

- **Non-hosted web search now uses DeepSeek Search/Fetch.** The old `webSearch` provider/type/model configuration that registered a provider-backed search tool was replaced by local `web_search` and `web_fetch` tools backed by DeepSeek's official `POST /v1/tools/search` and `POST /v1/tools/fetch` endpoints. Provider-native hosted search for OpenAI Responses is unchanged, and Anthropic Messages uses its native hosted search while `webSearch` is enabled; both take precedence over the local `web_search` function. `webSearch` now configures `provider`, `baseUrl`, and `token` (with the provider API key as fallback); both tools are read-only and available in plan mode.

- **Tencent Yuanbao search backend added.** `webSearch.backend` selects `deepseek` (official Search/Fetch APIs) or `yuanbao` (Tencent Yuanbao SearchPro at `POST /SearchPro`). The Yuanbao backend sends `{"Query": ...}`, normalizes the returned `Pages` into title/URL/site/date/score/passage results, authenticates with `webSearch.token` or `${TENCENTCLOUD_WSA_APIKEY}`, and currently provides search only, so `web_fetch` remains available only with the DeepSeek backend.

### ⚡ Performance

- **Opening and listing sessions no longer rescans the whole store.** A current database reads its migration ledger once and opens without a write transaction or writer lock; EventStore inspection is reused per open; execution facts, list metadata, and message counts are batched; cascade, export, and stats projections stop opening every session; and search uses one read-only scan that preserves compaction and content-override semantics.

- **User background mode lowers large-area backdrop blur.** When a user background image is active, the app surface veil, control/titlebar surfaces, composer, and home quick cards use smaller blur radii, reducing continuous GPU compositing cost while keeping the semi-transparent surfaces readable.

- **Repeated searches reuse unchanged EventStore projections.** A fingerprint-keyed, byte-bounded cache avoids replaying the same EventStore session on every search; an append or segment rotation invalidates it.

- **Repeated session searches reuse the completed page.** A query/page cache sits in front of the candidate scan, so a repeated Desktop/WebUI history query performs no list query and no content replay. Cache entries are keyed by the durable store-wide write version, which SQLite triggers bump on session, legacy payload, EventStore projection, and project-membership writes; an append, title/binding/cwd change, delete, or write from another process invalidates the page instead of serving stale results.

- **Session list rows no longer scan the EventStore at startup.** First-user preview, latest title, and binding metadata are persisted in a small SQLite display projection, written with each committed batch, copied on fork, rebuilt with the event projection, and backfilled by maintenance. List and bindings reads use the row when present and fall back to the canonical EventStore scan when it is missing.

- **Desktop settings panels no longer re-render on every background chunk.** All 16 settings panels now select only the connection/store fields they display instead of subscribing to the whole app state.

- **Session content search now narrows work through a derived FTS5 index.** Searches ask a trigram full-text index for candidate sessions before replaying anything; the canonical replay still decides the final match, so compaction-discarded content and content overrides keep their exact semantics. The index is written in the same transaction as each append, rebuilt with the event projection, copied on fork, and backfilled when a pre-index session is attached. Short queries, index errors, and not-yet-indexed sessions fall back to the previous full scan, and search itself remains read-only. The daily session-storage maintenance pass also backfills up to 512 unindexed sessions, newest first, so existing history becomes searchable without opening every session.

- **Desktop streaming and long-list scrolling no longer re-render the whole shell.** Renderer state notifications are coalesced into one commit per frame, transcript items are replaced immutably so unchanged Markdown/tool cards skip rendering, the sidebar/status bar/composer/plan panel subscribe only to the state slices they display, offscreen transcript blocks can skip layout/paint, and chat auto-scroll is coalesced with the commit that paints the chunk.

- **The initial Desktop task list no longer scans every session sequentially.** ACP list rows now use a structural-only EventStore projection that decodes just the first user preview and latest title/bindings, and the page prefetches those projections with a bounded worker pool. Canonical session open still verifies every batch digest and fails closed.

- **Electron no longer forces software rendering on every packaged Linux desktop.** GPU compositing is kept when `DISPLAY`/`WAYLAND_DISPLAY` exists (with an explicit `MOTHX_DESKTOP_DISABLE_GPU` escape hatch), and the `/dev/shm` fallback is applied only when that mount is missing, unwritable, or smaller than 128 MB. ACP events are also coalesced into one IPC delivery per frame, and live diagnostic logs cross IPC only while the log viewer is open.

- **Desktop no longer starts transition/animation storms on hover, theme changes, or scroll reveals.** Tailwind `duration-*` no longer accompanies `animate-in` (which made every hovered sidebar row transition all inherited properties), transcript cards no longer replay entrance animations when `content-visibility` reveals them during scrolling, the sidebar scrollbar keeps a constant 8 px width, and theme switches temporarily suppress transitions on inherited colors.

- **Long-lived Desktop windows stay bounded.** The live transcript renders a 300-item tail window (expandable locally), status/retry/compaction notices update in place instead of accumulating, session/search/subagent/run-status caches are capped, progress polling cannot overlap, transcript lookup uses an index instead of scanning every chunk, oversized tool output is capped in the display copy, and `desktop.log` rotates at 8 MB between launches.

### 🐛 Bug Fixes

- **Stuck detection no longer stops reasoning-only progress, and the terminal error keeps its reason.** Turns that produce reasoning output now count as progress, so a reasoning model that keeps calling tools without user-facing text is no longer treated as stuck; the silent-turn guard still stops a loop with neither text nor reasoning. `ErrAgentStuck` now carries the silent-turn count and is projected through the Run result and error event, so Runtime `ErrorInfo` and every adapter can report why the loop guard stopped.

- **A partially applied session-list migration now converges.** Some databases recorded schema version 49 before the Run index was added to that migration body, so `idx_session_runs_session_status_started` stayed missing forever: recorded versions are skipped, not re-inspected. Version 50 re-asserts the index with `CREATE INDEX IF NOT EXISTS`, converging those databases on the next start while leaving fresh ones unchanged.

- **Stopping a Responses background task during attachment now stays cancelled.** Concurrent WAL reads can let a stop mark the Run cancelling before the background coordinator attaches its Agent. That path previously fell through to the default `failed` terminal status and could orphan the provider-side task. It now preserves `cancelled` and explicitly cancels the remote Responses task.
