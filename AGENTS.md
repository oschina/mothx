# AGENTS.md

Guidance for AI coding agents working in this repository. Read this file before exploring or editing code. Keep changes focused, preserve existing behavior and APIs, and validate the smallest relevant scope.

## Core philosophy

**One Agent Core, one front-end-neutral Agent Runtime; every entry point (TUI, CLI, WebUI/API, WeChat/Feishu, ACP) is a thin projection of them.** Complexity may grow in exactly one place — the shared Runtime — while adapters may only get thinner. “Reuse” means reusing the same runtime path and lifecycle, not merely calling the same low-level Agent loop from several orchestrators.

- **One core, many projections.** The unique Agent Core and `internal/agentruntime` own prompts, events, tools, sessions, attachments, decisions, and run lifecycle. Adapters map protocols and render; they do not own semantics or state.
- **One source of truth, one owner each.** Construction, execution/lifecycle, source/policy resolution, session resources, decisions, event semantics, input/content, attachment/artifact lifecycle, and database access each have exactly one owner. Canonical IDs, Run ownership, ordering, and terminal semantics are preserved across every projection; only wire format and rendering differ.
- **Policy, not forks.** Entry-point differences are expressed as `RuntimeSource`/`ExecutionPolicy`/capabilities/explicit hooks. No parallel implementations, no “temporary” adapter defaults, no copied Session Runtime. If the shared abstraction is genuinely insufficient, extend it once in `internal/agentruntime` and migrate all adapters.
- **Boundaries are enforced, not aspirational.** `internal/architecture` statically rejects bypasses, and every migration bridge needs a named owner and a removal condition. Change a boundary and its cross-entry contract tests move with it; never weaken a guard to make one entry point work.
- **One database direction.** `internal/db` owns connection/transaction lifecycle, `internal/dao` owns all SQL, query construction, and row mapping, and business/runtime code only calls DAOs. Schema changes go through migrations, and `settings.json`/`serve.json` field meanings are compatibility surface. The public SDK boundary is `agent/` and must not import `internal/`.
- **Reliability favors long-task continuity.** A held execution lease keeps being renewed for as long as the process holds it; a transient renewal failure is an availability problem to retry, never ownership loss. Ownership is decided by the fenced `session_runtime_leases` CAS (`owner`/`epoch`/`token`), so wall-clock expiry or a single renewal timeout must not cancel a live run. Provider stream stalls and other safely recoverable transport failures are availability problems too: Agent Core must retry with bounded backoff until explicit cancellation, fenced ownership loss, or a genuine safety boundary. If text or reasoning has already streamed, preserve the safe partial turn and continue it rather than aborting the task; these recovery attempts must not consume the logical iteration budget. Do not blindly replay emitted tool calls or other uncertain side effects. ESM is a long-task objective mode: worker, critic, and audit roles must not impose arbitrary wall-clock deadlines; only explicitly scoped auxiliary observers may be bounded.
- **Disciplined, minimal change.** Read before editing, keep changes focused, prefer policy over duplication, add deterministic tests when behavior changes, keep bilingual docs in sync, and never hand-edit generated artifacts.

## Project snapshot

- **Primary language:** Go 1.27 (`go.mod`), with a Cobra CLI and Bubble Tea/Lipgloss TUI.
- **Frontend:** Svelte 5 + Vite in `ui/`; the built UI is embedded into the Go binary.
- **Desktop:** Electron + TypeScript in `desktop/`; this is a first-class, **pure ACP** client. It packages a source-built `mothx acp` runtime and has its own React 19 + shadcn/ui + Tailwind CSS renderer (Vite-built classic scripts for `file://`); it neither starts `mothx serve` nor embeds/reuses the Svelte Web UI.
- **Packaging:** npm installer packages under `npm/` and a Python installer package under `pypi/`.
- **Purpose:** MothX (`mothx`) is a terminal AI coding assistant with provider adapters, streaming agent execution, tools, sessions, sandboxing, skills, workflows, serve/API mode, messaging channels, and SDK support.

## Important directories

- `cmd/mothx/` — Cobra CLI entry point and subcommands (`serve`, `stats`, `a2a`, `doctor`, etc.).
- `agent/` — public Go SDK types and interfaces. Treat changes here as public API changes.
- `bootstrap/` — blank-import wiring that connects public SDK types to internal implementations.
- `example/` — public SDK examples.
- `internal/agent/` — core agent loop, events, context handling, tool execution, sub-agents, and system prompts.
- `internal/agentruntime/` — the authoritative front-end-neutral runtime layer: shared `SessionRuntime`/`Builder` resource assembly, `ResolveSource`/`ResolvePolicy` mode wiring, `ExecutionRuntime` durable run lifecycle, `DecisionService`/`DecisionRecord` replay, MCP lifecycle, and coordinated shutdown for TUI, CLI, WebUI/API, channels, and ACP. See `docs/proposal/agent-core-runtime-unification-proposal.md`.
- `internal/expert/` — discovery and validation of built-in, global, and project Expert/Expert Team bundles. A session's resolved identity and team capability remain Runtime-owned in `internal/agentruntime`.
- `internal/provider/` — provider abstraction and implementations; `anthropic/`, `google/`, and `openai/` contain full providers, while `vendor_*.go` contains vendor detection/defaults.
- `internal/provider/factory/` — shared provider/model construction. Use this from CLI, ACP, serve, and other runtimes.
- `internal/tools/` — built-in tools and tool registration.
- `internal/tui/` — Bubble Tea terminal UI.
- `internal/serve/` — unified server runtime: OpenAI-compatible API, Web UI, channels, hooks, cron, memory, and settings APIs.
- `internal/serve/openaiapi/` — HTTP API handlers, slash commands, and tool-output formatting.
- `internal/architecture/` — static architecture guard tests that forbid production code from reintroducing direct `agent.New`/`agent.NewWithLoopConfig`, bypassing canonical Run persistence, or bypassing the DB-to-DAO boundary. Keep the allowlist minimal and documented; run `go test ./internal/architecture` after touching production call sites.
- `internal/db/` — process-wide Bun connection ownership, SQLite configuration, migration startup, and database shutdown.
- `internal/dao/` — the only production owner of Bun query construction, SQL statements, table persistence, and row mapping.
- `internal/session/` — Session domain APIs and replay logic; it uses `internal/db` for managed handles and `internal/dao` for every database operation.
- `internal/config/` — `settings.json` schema, defaults, and configuration persistence.
- `internal/contextfiles/`, `internal/skills/`, `internal/workflow/` — project context discovery, reusable skills, and workflow execution. Built-in skill guidance lives under `internal/skills/builtin/`; project/global skills remain user-owned overrides.
- `internal/sandbox/`, `internal/mcp/`, `internal/acp/`, `internal/a2a/` — sandboxing and protocol integrations. `internal/mcp/server.go` owns reusable stdio MCP protocol serving; domain packages provide handlers rather than implementing their own JSON-RPC loop.
- `internal/stats/` — usage statistics dashboard and queries.
- `ui/src/` — Svelte application; `App.svelte` routes views, `lib/stores.js` owns shared stores, `lib/preferences.js` owns `zh`/`en` translations, and `style.css` contains global styles.
- `desktop/` — the primary desktop product: Electron main process (`main/`), restricted preload bridge (`preload/`), separate React/shadcn renderer (`renderer/`: `core/` DOM-free state + ACP actions bridged to React via `useSyncExternalStore`, `components/ui/` shadcn primitives, `views/` screens and settings panels), tests, runtime-vendoring/build scripts, and packaging configuration. `main/acp-client.ts` is the only ACP client; renderer code only uses `window.mothx` through the preload bridge.
- `internal/session/knowledge_*.go`, `internal/dao/knowledge_bases.go`, and `internal/agentruntime/knowledge_*.go` — Runtime-owned managed knowledge bases: one private SQLite graph/FTS snapshot store per knowledge-base ID, DAO-only persistence, index orchestration, and bounded MCP evidence querying. See `docs/proposal/desktop-knowledge-base-agent-proposal.md`.
- `docs/en/` and `docs/zh/` — bilingual documentation; `docs/en/changelog.md` and `docs/zh/changelog.md` accumulate release notes for all versions, while `docs/changelog_online_en.md` and `docs/changelog_online_zh.md` hold only the current version's changes.
- `scripts/`, `npm/`, `pypi/`, `packaging/` — build and distribution tooling.
- `bin/`, `dist/`, `ui/dist/`, `ui/node_modules/`, `desktop/node_modules/`, and generated package artifacts are build output; do not hand-edit them.
- `internal/platform/busybox_assets/` and `internal/context/tokenizerdata/` — vendored third-party assets embedded into the binary (`busybox{32,64}u.exe` from [`rmyorston/busybox-w32`](https://github.com/rmyorston/busybox-w32); the DeepSeek V3 tokenizer JSON/conf from DeepSeek's official download). Each directory has a `README.md` describing its source, use, and update procedure; refresh both files of a pair together and rerun the owning package's tests (`./internal/platform`, `./internal/context`). Do not hand-edit the binary or JSON.

## Architecture notes

- The agent loop constructs prompts, streams provider events, executes tools, records usage, handles compaction, and continues until completion. Reuse it rather than creating parallel agent logic.
- **Target architecture:** one complete Agent Core plus one front-end-neutral Agent Runtime; TUI, CLI, WebUI/API, WeChat/Feishu channels, and ACP are thin adapters. Adapters may map protocols, render events, and supply scenario-specific policy/interaction hooks, but must not grow separate input/content, Agent, session, tool, MCP, skill, attachment/artifact, delivery, or run implementations. This is the implemented boundary, not a future plan; see `docs/proposal/agent-core-runtime-unification-proposal.md` for the migration record and remaining debt.
- Production Agent construction must go through `SessionRuntime.BuildAgent`, `BuildTransientAgent`, or `agentruntime.NewAgentManager`; **do not** add adapter-level complete `agent.Config` assembly or direct `agent.New`/`agent.NewWithLoopConfig`. Registry bootstrap, MCP connection lifecycle, Skill/context bootstrap, Session replay, canonical Run row persistence, and run state machines belong in `internal/agentruntime`. `internal/architecture` enforces this statically.
- Resolve source, mode, capabilities, tools, sandbox, approval, and run policy once in the shared runtime (`ResolveSource`/`ResolvePolicy`). UI display, run records/events, approvals, background/recovery paths, session bind/unbind, and `agent.Config` must use the same resolved values; do not add local fallback/default logic.
- Product default execution mode is `yolo` (`settings.json` `defaultMode`, Serve/API `DefaultMode`, CLI/TUI/ACP/WebUI empty-mode fallback, public SDK `Builder`, and `agentruntime.Policy.ResolveMode` when `DefaultMode` is empty). Explicit request, persisted session mode, and source-forced WeChat/Feishu `yolo` still win. Empty-mode fallbacks in adapters must use `yolo`, not `agent`.
- Durable run lifecycle is canonical in `internal/agentruntime`: `ExecutionRuntime.BeginDurable`/`ReattachDurable`/`UpdateDurable`/`CancelDurable`/`FinishDurable` and `RunStore` own run rows, terminal transitions, and start/finish events. Adapters keep protocol/SSE/WebSocket/JSON-RPC projection; use `RunManager.Register` only for in-memory event fan-out on migrated runs.
- `SessionRuntime.Shutdown` cancels the active `ExecutionRuntime` run, waits for terminal state, and releases MCP clients; it must stay idempotent. Adapters should not bypass this for cleanup.
- **Long-task continuity is a product priority.** A session's execution lease must keep being renewed for as long as the process holds it, so treat a transient failure to renew (a busy or unreachable session database, a stalled heartbeat) as an availability problem to retry — never as ownership loss. Ownership is decided only by the fenced `session_runtime_leases` CAS (`owner`/`epoch`/`token`): a competing process takes over by bumping the epoch, so an expired row that still carries our identity is still ours. Wall-clock expiry or a renewal timeout alone must not cancel a live run, reject an execution-path write, or block a side-effecting tool; only a genuine fenced takeover, an explicit release, or a gone in-memory lease may. Keep exactly one live heartbeat scheduler per session directory — the scheduler registry must never retain a stopped scheduler.
- **Provider-stream continuity follows the same rule.** A provider idle/response timeout or other retry-safe transport interruption is not task completion and must not terminalize a live run after a small fixed retry count. Agent Core owns retry classification, exponential/backoff scheduling, partial-response continuation, and canonical retry events for every projection. For a turn with text or reasoning already shown and no emitted tool call, preserve the safe partial response and retry with a continuation instruction; recovery retries do not consume Agent iteration budget. Stop only for explicit cancellation, a fenced ownership/safety decision, or an unrecoverable error. Never retry an emitted tool call blindly: resolve or terminalize uncertain side effects explicitly. ESM worker, critic, and audit roles are long-running by design and may not add a role-level hard deadline; a narrowly read-only recovery observer may have its own bounded timeout, after which the objective resumes its normal recovery path.
- Pending Approval/Question decisions use `DecisionService`/`DecisionRecord`: persist a request/resolution deadline, and on session load fully replay request/resolution so resolved or expired decisions are not revived; terminalize unrecoverable pending decisions. ACP may re-emit pending request projections after reconnect.
- Reuse persisted session channel bindings (`channel_type`, `channel_id`, and session headers) as the authoritative source for WeChat/Feishu identity. A session bound to WeChat or Feishu has a forced effective mode of `yolo`: request mode, session capability mode, API defaults, `/mode`, WebUI reloads, external/background/recovery paths, sub-agent inheritance, run records, and approval events must not downgrade it to `agent` or `plan`.
- Forced `yolo` controls effective agent mode only. It does not bypass sandbox, allow rules, channel security, or hard high-risk-command protections; model those separately in execution policy.
- Providers stream through the shared provider abstraction. Create providers through `internal/provider/factory`; put vendor-specific behavior in `internal/provider/vendor_*.go` and model compatibility flags, not in CLI/ACP wiring.
- All user input, including text, images, files, and protocol content parts, must normalize into one front-end-neutral Runtime input contract owned by `internal/agentruntime`. TUI, CLI, WebUI/API, ACP, and messaging channels may decode their wire/UI format, but must not build provider content or retain a parallel string-only execution path.
- Attachments and generated artifacts are Runtime-owned resources. Intake, normalization, storage, session linkage, replay, expiry, tool exposure, provider conversion, canonical events, and durable delivery state belong to `internal/agentruntime` backed by `internal/session`; adapters may only provide protocol fetch/upload/send hooks and render/project canonical state.
- A work-directory path becomes a deliverable artifact only through Runtime-owned `publish_artifact` (which copies a completed regular file into private attachment storage) or an authorized provider attachment resolver. Never infer an artifact from assistant text, scan adapter-local output paths, or send a mutable worktree file directly.
- The public SDK boundary is `agent/`. Public packages must not import `internal/`; implementation wiring belongs in `bootstrap/`. Update `example/` when public APIs change.
- Tools should be stateless where practical. Put shared runtime state in managers/registries and pass `context.Context` through execution paths.
- SQLite access has one mandatory direction: `internal/db` owns connection lifecycle, `internal/dao` owns all queries and persistence, and business/runtime code calls DAO methods. Business code may use the managed `internal/db`/`internal/session` transaction boundary to invoke a DAO, but must not execute SQL or use Bun query builders itself. Schema changes belong as appended entries in `internal/session/migrations.go`; do not add new direct `CREATE TABLE IF NOT EXISTS` schema setup.
- `settings.json` and `serve.json` are distinct schemas. Preserve existing field meanings. For sparse global settings edits, use `config.SaveGlobalSettingsPatch()` rather than saving a sparse `Settings` struct.
- Serve API and channels reuse the provider factory, agent loop, sessions, tools, sandbox, skills, and MCP. Serve-only configuration belongs in `internal/serve/config.go`.
- In the TUI, completed transcript blocks go to terminal scrollback with `Program.Println`; keep only active streaming content in the managed view. Keep provider/model state synchronized across `App`, settings, and `AgentManager`.
- In the Web UI, use Svelte conditional rendering for interactive mobile behavior (`isMobile`/`sidebarOpen`); reserve CSS media queries for layout. Add translations to both `zh` and `en` maps.
- **Desktop is an ACP projection, not a second backend:** Electron main starts the packaged `mothx acp` child over stdio NDJSON JSON-RPC; `main/acp-client.ts` owns request correlation, notifications, reverse approval/question requests, restart/backoff, and lifecycle. The renderer must communicate only through preload IPC, must use `initialize._meta.mothx.dev.features` to gate optional UI, and must not add an HTTP/token tunnel, direct child-process access, direct settings/session/database reads, or a second provider/session/run implementation.
- Desktop-local `desktop-store.json` may contain only presentation state that ACP does not own (for example theme, language, and the new-session default-directory/history). Session history, project membership, pinning, persisted session cwd, provider/model configuration, expert binding, run state, MCP configuration, and management data are canonical ACP/Runtime state. Renderer-only tree expansion, loaded pages, and similar transient projections stay in memory rather than becoming new persisted facts.
- Desktop's selected folder is the default cwd for a new session, not a process-wide workspace security boundary. Existing sessions use their persisted cwd. If product policy ever restricts directories, enforce it in the shared Runtime policy rather than ACP startup cwd, a renderer check, or Electron file grants.
- ACP management methods are additive `mothx/manage/*` projections of shared internal services. Keep keys capability-gated and preserve ACP v1; never copy Serve handlers, let Desktop read `settings.json`, or persist a Desktop-only management model. Provider responses and logs must keep keys and sensitive headers masked.
- **Expert Teams:** an expert is a persisted session binding resolved once by `SessionRuntime`. Bundle precedence is project `.mothx/experts/` over global `experts/` over built-ins. A team forces the shared multi-agent capability; member cards only project canonical child events. Binding/unbinding is allowed on an idle session, but switching one non-empty expert to another must fork, preserving the source identity and history. Do not let an adapter silently downgrade a team or turn a member completion into a new lead run.
- **Knowledge bases:** a knowledge base is a Desktop-managed, rebuildable index of a user-selected directory, not a chat attachment store, a general RAG path, or a special Agent mode. Its source directory is read-only; configuration, snapshots, FTS, graph nodes/edges, and evidence live in `sessionDir/knowledge-bases/<knowledgeBaseId>.db`, while canonical Runs and audit remain in `sessions.db`. Never delete the source directory when deleting a knowledge base.
- Indexing is a restricted ordinary Runtime Agent execution with a canonical durable Run; manual and scheduled scans reuse `ExecutionRuntime` and `internal/cron`, not Electron timers or a custom index state machine. Publish only fully validated snapshots atomically; queries must read the active snapshot, return bounded evidence with citations, and leave a prior active snapshot usable after a failed/cancelled rebuild.
- Main sessions access knowledge only through a configured standard Knowledge MCP server (`mothx knowledge-mcp serve --knowledge-base <id>`) and its ordinary `search_knowledge_base` tool. The MCP allowlist is authorization, and tool output is untrusted, bounded reference data. Do not construct provider content, inject `KnowledgeCapsule`/Librarian strings, run a renderer-side pre-query, or add a knowledge-specific branch to the Agent loop. The legacy capsule/Librarian query path is a migration bridge only; new callers must use MCP.

## Anti-fragmentation rules (hard architectural invariants)

The repository must maintain **one Agent Core, one front-end-neutral Agent Runtime, and thin adapters**. “Reuse” means reusing the same runtime path and lifecycle, not merely calling the same low-level Agent loop from multiple independent orchestrators.

- **One construction path:** production code must construct Agents only through `internal/agentruntime` (`SessionRuntime.BuildAgent`, `BuildTransientAgent`, or `agentruntime.NewAgentManager`). Never add a new adapter-local `agent.Config` assembler, `agent.New`, `agent.NewWithLoopConfig`, provider factory, registry builder, MCP/Skills loader, or session replay path.
- **One execution/lifecycle path:** all durable runs must use `ExecutionRuntime`/`RunStore` and its canonical begin, reattach, update, cancel, finish, recovery, and terminalization operations. Adapters may project events and protocol payloads, but may not create a competing run state machine, persistence path, cancellation path, or recovery policy.
- **One source-of-truth resolver:** source, effective mode, capabilities, tools, sandbox, approval/question policy, MCP policy, and run policy are resolved once by the shared Runtime (`ResolveSource`/`ResolvePolicy` and related resolvers). Do not compute display defaults, request defaults, background defaults, recovery defaults, or approval behavior independently in TUI, WebUI, ACP, Channel, CLI, or tests.
- **One session/resource owner:** session binding, replay, Context/Skills/Rules, Registry, MCP clients, Agent resources, and shutdown ownership belong to `SessionRuntime`/`Builder`. Adapters may supply policy and protocol hooks; they must not duplicate resource ownership or cleanup. `SessionRuntime.Shutdown` is the coordinated, idempotent shutdown boundary.
- **One database access path:** all production database access follows `internal/db` → `internal/dao` → business/runtime logic. `internal/db` is the sole owner of connection creation, caching, SQLite setup, migrations, and closing; `internal/dao` is the sole owner of Bun query builders, raw SQL, table names, persistence statements, and row mapping. Business/runtime packages may only call DAO methods and the managed transaction boundary; they must not bypass DAO to issue SQL, inspect rows, or perform table operations.
- **One decision model:** Approval/Question pending state, identity, deadlines, first-response-wins, replay, rehydration, expiry, and terminalization use `DecisionService`/`DecisionRecord`. Protocol callbacks and UI rendering remain adapter concerns, but an adapter must not invent a second decision store or revive resolved/expired decisions.
- **One event semantic model:** Agent/Runtime events and terminal states are canonical. SSE, WebSocket, JSON-RPC, Bubble Tea, and platform messages may use different wire formats, but must be projections of the same event/run semantics rather than parallel event producers.
- **One input/content path:** TUI, CLI, WebUI/API, ACP, and Channel must map text, images, files, and other supported content into the same `internal/agentruntime` input contract and call the same `SessionRuntime` execution entry. Adapters must not keep a separate text-only runner, construct `provider.Message`/`ImageContent` directly, invoke the provider, or fork prompt/content assembly.
- **One attachment/artifact lifecycle:** attachment intake, validation, storage, IDs, hashes, session/run ownership, replay, expiry, document extraction, tool access, provider conversion, generated-artifact registration, and canonical attachment/artifact events have one Runtime-owned implementation backed by `internal/session`. An adapter may decode a platform reference, provide authenticated fetch/upload/send operations, and render results; it must not own canonical attachment rows, upload storage, scan tool output paths, infer artifacts from text, or implement cleanup/recovery.
- **Thin output projections:** TUI/Bubble Tea, CLI text/NDJSON, WebUI/SSE/WebSocket, ACP/JSON-RPC, and WeChat/Feishu messages may format the same canonical text/tool/usage/decision/attachment/artifact/terminal events differently. They must preserve canonical IDs, Run ownership, ordering, and terminal semantics and must not synthesize a second success/failure or artifact stream.
- **Policy, not forks:** entry-point differences must be expressed through `RuntimeSource`, `ExecutionPolicy`, capabilities, and explicit adapter hooks. Do not fork the Agent loop, copy a Session Runtime, add “temporary” adapter defaults, or create a parallel package that will later become a second runtime.
- **No compatibility bypasses:** legacy code may remain only behind a named, documented migration bridge with a clear owner and an architecture test/allowlist entry. New callers must not use the bridge. Every bridge must have a removal condition; “temporary” duplication without an exit condition is prohibited.
- **Canonical persistence boundary:** use `internal/session`/`internal/db` for session/database lifecycle and `internal/dao` for all SQL-backed records; use the Runtime-owned Run/Decision/Delivery stores for their respective records. Do not add direct schema setup, adapter-owned canonical Run rows, duplicate durable records, or any database compatibility wrapper to make one entry point work.
- **DAO-only SQL rule:** outside `internal/db`, `internal/dao`, `internal/session/schema.go`, and `internal/session/migrations.go`, production code must not import `database/sql` or `github.com/uptrace/bun`, call `Query`/`Exec`, or call Bun query constructors such as `NewSelect`, `NewInsert`, `NewUpdate`, `NewDelete`, or `NewRaw`. New exceptions are prohibited; the architecture guard must reject them.
- **Required guardrails:** when moving or adding production construction, Runtime input/content handling, attachment/artifact ownership, run/delivery persistence, mode/source resolution, decision handling, or shutdown code, update/run `go test ./internal/architecture` and add focused cross-entry contract tests. For input or artifact changes, the contract tests must cover TUI, CLI, WebUI/API, ACP, and Channel and prove that only protocol/rendering projections differ while canonical session entries, attachment/artifact IDs, Run ownership, events, and terminal state remain aligned. Keep architecture allowlists minimal and explain every exception inline.

When a proposed change appears to require a new runtime, manager, lifecycle, resolver, or durable store, stop and first extend the existing `internal/agentruntime` abstraction. If the shared abstraction is genuinely insufficient, change it once and migrate all affected adapters; do not solve the problem separately per entry point.

## Build, test, run, and lint

```bash
make build                         # bin/mothx for the current platform
make run                           # build and run the TUI
make serve                         # build ui/dist, build binary, start serve mode
make install                       # go install the CLI
make test                          # go test -v -race ./...
go test ./internal/tools/...       # focused package tests
go test -run TestName ./path/...   # focused test
make lint                          # golangci-lint run ./...
make fmt                           # gofmt and goimports
make fuzz                          # internal/esm, internal/mcp, internal/util
```

Web UI:

```bash
make ui-install                    # cd ui && npm ci
make ui-build                      # cd ui && npm run build
make ui-dev                        # backend 127.0.0.1:7872 + Vite dev server
make ui-preview                    # preview ui/dist
(cd ui && npm run e2e)              # channel/settings smoke test
```

Desktop:

```bash
make desktop-vendor                # source-build and vendor the runtime
make desktop-build                 # build Electron shell
cd desktop && npm run typecheck     # TypeScript boundary check
cd desktop && npm test              # main/preload/renderer unit and protocol tests
cd desktop && npm run e2e           # optional Electron ACP smoke test (skips headless CI)
cd desktop && npm run start         # build/start locally
make desktop-dist-dev-linux        # analogous mac/win targets exist
```

Use focused tests first, then `make test` when the change crosses packages or affects concurrency. Run `make ui-build` for Svelte Web UI changes and Desktop's `typecheck` plus focused `npm test` for Electron/renderer changes; use the Electron smoke test when modifying an ACP end-to-end flow. Run provider tests (`go test ./internal/provider/...`) after provider/vendor changes. Run `go test ./internal/architecture` after moving production call sites of Agent construction or Run persistence. Knowledge-base changes require the focused `internal/session`, `internal/agentruntime`, `internal/acp`, and `internal/mcp` tests, plus the architecture guard when persistence/runtime boundaries move. Real process-boundary tests live with their packages (e.g. `internal/agentruntime`, `internal/acp`, `internal/serve`) and use the subprocess-helper pattern; keep them isolated with temp dirs and localhost addresses.

Release and publishing targets (`make dist*`, `make build-all`, npm/PyPI publish targets, checksums) are not normal development commands; run them only when explicitly requested.

## Coding conventions and working rules

- Read relevant files and nearby tests before editing; preserve unrelated user changes.
- Prefer small, maintainable changes over broad refactors. Follow nearby naming, layout, and error-handling patterns.
- In Go, return errors instead of panicking for normal control flow, pass contexts, keep interfaces stable, and format with `gofmt`/`goimports`.
- Add or update tests when changing behavior. Keep tests deterministic and scoped to the affected package.
- Preserve meaningful trailing spaces in approval command prefixes such as `go `; do not normalize them as comma-separated values.
- When adding a provider/model, update the `internal/config/settings.go` defaults, then regenerate the catalog with `make docs-models` (which writes `docs/provider-model-list.md`); do not hand-edit that file.
- When adding a Web UI view, register it in `ui/src/App.svelte` and add navigation in `ui/src/components/Sidebar.svelte` as appropriate.
- When changing Desktop UI, keep the renderer independent of `ui/`: build screens from the shadcn/ui primitives in `renderer/src/components/ui/` plus Tailwind utilities and the design tokens in `renderer/src/index.css`, keep DOM-free state/ACP actions in `renderer/src/core/`, use the `core/i18n` dictionaries for both Chinese and English strings, and make optional controls unavailable until their ACP feature key is advertised. Put privileged local operations behind a narrow, typed preload IPC method; do not expose Electron/Node primitives to the renderer.
- When changing ACP behavior used by Desktop, keep each extension additive and feature-discoverable, update the ACP wire/process tests and Desktop projection tests together, and preserve standard ACP event/run semantics. The main process remains the single ACP client.
- When changing Expert Teams, resolve bundles and decide forced multi-agent capability in `internal/agentruntime`; persist only the session expert binding, test the bind/unbind/fork transition, and preserve the child-event/lead-run boundary.
- When changing a knowledge base, keep all SQL and graph/FTS queries in `internal/dao`, session APIs and per-base database ownership in `internal/session`, and orchestration/MCP handlers in `internal/agentruntime`. Add evidence, snapshot isolation, failure/cancellation, and bounded-result tests; update the ACP/Desktop capability projection only after the shared service exists.
- Keep bilingual user-facing docs synchronized. Append changelog entries for all versions to `docs/en/changelog.md` and `docs/zh/changelog.md`; keep `docs/changelog_online_en.md` and `docs/changelog_online_zh.md` holding only the current version's changes (replace their content with each new release).
- Do not add license headers unless the surrounding file/project already uses them.
- Do not create commits, tags, or pushes unless explicitly requested.

## Agents must not

- Do not expose, print, or commit secrets from `.env`, credentials, keys, tokens, or private configuration.
- Do not rewrite shared remote history or use force-push equivalents.
- Do not use privilege escalation (`sudo`, `su`, `doas`, `pkexec`).
- Do not run destructive cleanup, resets, database drops, or bulk deletion without explicit approval.
- Do not hand-edit generated output under `bin/`, `dist/`, `ui/dist/`, `node_modules/`, `npm/packages/`, or `pypi/.venv-build/`.
- Do not change the `settings.json`/`serve.json` schema or existing field semantics without a deliberate compatibility change.
- Do not bypass `internal/provider/factory` or put vendor behavior in CLI/ACP glue.
- Do not open raw SQLite connections in new code; use the shared DB/session helpers.
- Do not access SQLite or Bun directly from business/runtime/adapters. The only valid path is `internal/db` lifecycle/transaction boundary -> `internal/dao` method -> caller logic. All SQL, query builders, table names, persistence statements, and row mapping belong in `internal/dao`; schema/migration SQL is limited to the existing migration owner files.
- Do not import `database/sql` or `github.com/uptrace/bun` outside the DB/DAO and schema migration owners, and do not call database `Query`/`Exec` or Bun `NewSelect`/`NewInsert`/`NewUpdate`/`NewDelete`/`NewRaw` outside those owners. Do not introduce a compatibility bridge, wrapper, or allowlist entry to evade this rule.
- Do not introduce an external HTTP framework into serve code; use the standard `net/http` stack.
- Do not use CSS media queries to toggle interactive Web UI elements.
- Do not import `internal/` packages from the public `agent/` package.
- Do not add adapter-local empty-mode fallbacks to `agent`; product default and empty-mode resolution are `yolo` via settings, Serve/API config, and `agentruntime.ResolvePolicy`.
- Do not add adapter-local user-input models, attachment stores, upload lifecycles, provider-content builders, artifact inference/scanning, delivery persistence, cleanup jobs, or recovery loops in TUI, CLI, WebUI/API, ACP, WeChat, or Feishu code. Extend the shared `internal/agentruntime` contract once and project it through every affected adapter.
- Do not make Desktop a Serve/WebUI wrapper: do not start or proxy `mothx serve`, add renderer HTTP fetches to local APIs, expose ACP stdio or Node/Electron directly to the renderer, or let Electron/renderer own sessions, runs, credentials, MCP clients, schedules, or security policy.
- Do not store canonical Desktop facts in `desktop-store.json`, localStorage, renderer caches, or Electron file grants. Do not treat the ACP child startup directory, a default new-session directory, or a renderer directory picker as a workspace authorization mechanism.
- Do not add Desktop-specific Agent configuration, direct provider calls, prompt/content assembly, expert-team capability logic, or a parallel approval/question/run event model. Use the existing ACP projection and shared Runtime resolver.
- Do not turn artifacts/attachments into knowledge sources implicitly, scan a Desktop/worktree output directory to infer knowledge, or read a knowledge-base SQLite file from an adapter. Do not implement a knowledge-specific prompt injection, Agent mode, agent loop, Electron timer, scheduler, query store, or unbounded/full-text result path; extend the Runtime/DAO/MCP path instead.
