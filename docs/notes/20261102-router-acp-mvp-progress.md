# Router ACP MVP Progress (2026-11-02, completed 2026-11-03)

> Status: Phases 0–3 and Phase 4 transport/continuation slices of
> [`acp-client-external-subagent-proposal.md`](../proposal/acp-client-external-subagent-proposal.md)
> are implemented, including the ACP standard remote transport. Parallel
> base-workspace writers are available behind the explicit profile opt-in.
> The 2026-11-04 follow-up removed profile-level role/write-root authorization:
> roles and task scopes are per delegation, and legacy `roles`/`writeRoots`
> fields remain parse-compatible but are ignored.
>
> Validation: `go build ./...`, `go test ./...`, focused `-race` runs, and
> `GOOS=windows go build ./internal/agentruntime ./internal/acp/client` all
> pass (see the final section).

## Scope

This note is the implementation record for the whole MVP. It started with the
foundation slice:

- a first-class Router mode;
- configuration and persistence facts needed to select one external ACP worker;
- a transport-only ACP v1 stdio client; and
- a Runtime-owned one-turn worker bridge.

Later sections record activation, the external-child backend, delegation,
decisions, containment, diagnostics, and documentation. The MVP is complete:
`delegate_subagent` targets the activated external ACP worker and never falls
back to a local child in Router.

## Implemented

### 1. First-class Router mode

Files:

- `internal/agentruntime/policy.go`
- `internal/agentruntime/session_options.go`
- `internal/tools/tool.go`
- `internal/agent/system_prompt.go`
- `cmd/mothx/main.go`
- `internal/tui/`
- `internal/acp/acp.go`

Behavior:

- `ModeRouter = "router"` is accepted by the shared mode resolver and remains
  distinct from `yolo`.
- WeChat/Feishu source-forced yolo remains authoritative.
- Unattended Router-derived work preserves the Router tool surface instead of
  silently widening to yolo.
- Router's local frozen tool surface is `read`, `ls`, `grep`, `find`, `plan`,
  and `skill_ref`; write/edit/insert/bash/jobs/kill are absent from both tool
  advertisement and the Agent Core execution authorization snapshot.
- The Router system prompt describes a read-only lead/orchestrator and does not
  include the YOLO unrestricted-access claim.
- CLI accepts `-M router`; TUI supports `/mode router` and `/router <id>`,
  footer display, and English/Chinese permissions copy. ACP session mode
  catalogs also recognize Router so it is not treated as an unknown persisted
  mode.
- Router is deliberately **not** a cycleable mode. It is excluded from the Tab
  mode cycle and from the `defaultMode` settings cycle, because it is not a
  mode the user can switch into: it exists only for the session's bound
  external-worker allowlist. `/router` (panel or `/router <id>[,<id>...]`) is
  the only entry point.
- Activation is not the only fence. `SessionRuntime.buildAgent` fails closed
  when a Router-mode Agent would be constructed without an eligible
  `RouterBinding`, so a persisted default, stale adapter value, or protocol
  request cannot produce a Router lead whose `delegate_subagent` can only fail.
- Every TUI mode write goes through `SessionRuntime.SetMode`. `/mode`,
  the Tab cycle, `/systeminit`, a `defaultMode` settings change, and `/router`
  activation previously assigned `App.mode` locally, which made the adapter and
  the persisted replay entry diverge: leaving Router then reopened the session
  as Router and re-applied the read-only tool fence.

### 2. ACP worker profile configuration

Files:

- `internal/config/settings.go`
- `internal/config/settings_test.go`

`Settings.ACPAgents` now contains `ACPAgentProfile` entries:

```json
{
  "id": "codex",
  "title": "Codex",
  "command": ["codex-acp"],
  "enabled": true,
  "environment": ["OPENAI_API_KEY"],
  "permissionPolicy": "auto_allow_once"
}
```

Implemented constraints:

- profile IDs are lower-case ASCII identifiers;
- `command[0]` may be an absolute path or a bare command resolved through `PATH`
  at launch, exactly like any other `exec.Command` launch; launch never goes
  through a shell, so there is nothing extra to enforce;
- environment entries are variable names only, never inline secret values;
- duplicate IDs and unsupported permission policies fail validation;
- `ACPAgentProfile()` returns only enabled, valid profiles and copies slices;
- empty/unknown permission policy resolves conservatively to `auto_reject`.

Editing this schema is a deliberate act (profile configuration is
executable-code trust), and there are exactly two supported surfaces: the global
`settings.json`, and the TUI `/router` panel. The panel renders the Runtime's
read-only catalog (`agentruntime.ListACPWorkerProfiles`) for eligibility and
writes only through `config.SaveACPAgentProfiles`, which validates the complete
candidate set (non-empty `command[0]`, duplicate IDs, environment names,
concurrency, transport, and permission policy) before persisting. That keeps one validation
owner and one write owner; no adapter parses or writes `settings.json` itself.
`mothx/manage/acp/profiles/list` remains a read-only projection for ACP clients.

### 3. RouterBinding session persistence

Files:

- `internal/session/entry.go`
- `internal/session/store.go`
- `internal/session/session.go`
- `internal/session/memory_store.go`
- `internal/session/event_entry.go`
- `internal/session/event_store_display.go`
- `internal/session/display_projection.go`
- `internal/session/fork.go`
- `internal/agentruntime/router.go`

New replayable entry type:

```text
router_binding
  acpProfileId
  approvalStrategy
  policyRevision
```

It is handled by both legacy entries and EventStore decoding, batched display
projection, `LatestSessionBindings`, forks, and memory storage. The Runtime
exposes:

- `SessionRuntime.SetRouterBinding`;
- `SessionRuntime.RouterBinding`; and
- `SessionRuntime.ResolveRouterACPProfile`.

The binding is an opaque session-level profile selection. Only
`internal/agentruntime` combines it with global executable configuration.

### 4. ACP v1 transport client

Files:

- `internal/acp/client/client.go`
- `internal/acp/client/protocol.go`
- `internal/acp/client/client_test.go`

`internal/acp/client` owns only stdio JSON-RPC transport behavior:

- external subprocess stdin/stdout NDJSON framing;
- serialized writes and numeric request-ID correlation;
- bounded inbound/outbound frames (default 10 MiB);
- Agent notifications and reverse requests through a caller callback;
- request cancellation notification;
- typed helpers for `initialize`, `session/new`, `session/prompt`,
  `session/cancel`, and `session/close`.

It imports neither Runtime, Agent Core, database/session packages, UI, nor
providers. Package tests include a real subprocess helper that performs the
complete stdio handshake and prompt round-trip.

### 5. ACPWorkerBridge foundation

Files:

- `internal/agentruntime/acp_worker_bridge.go`
- `internal/agentruntime/acp_worker_bridge_test.go`

`ACPWorkerBridge.Run` accepts an already-authorized profile/workdir/text task,
builds a minimal truthful ACP v1 client capability payload, and executes:

```text
initialize -> session/new -> session/prompt -> optional session/close
```

It drains `session/update` notifications through a Runtime callback and sends
`session/cancel` on caller-context cancellation. It resolves only the declared
environment allowlist from the existing global `env.json` / process environment.
It does not own an Agent loop, Session, Run, DecisionService, queue, or UI.

## Self-iteration observation

The development process ran the current source through:

```text
mothx -P -M plan "Read only ... review the Router mode implementation ..."
```

The delegated plan-mode review did not modify the repository, but it kept
issuing read/grep operations for roughly two minutes and consumed about 120K
context tokens before it was interrupted. It did not produce its requested
concise final answer before cancellation.

This is direct evidence for the proposal's Router task-contract and delegation
budget requirements: prompt wording alone does not bound worker exploration.
That observation drove the task-contract, output-bound, one-worker permit, and
cancellation work that was completed before the bridge was exposed through
`delegate_subagent`; the final wiring is recorded in the last section of this
note.

## Second slice (2026-11-03)

### 1. ACP profile and dynamic task scope

`acpAgents` profiles declare launch and concurrency policy, not role authority:

```json
{
  "command": ["codex-acp"],
  "enabled": true,
  "maxConcurrentChildren": 1,
  "permissionPolicy": "auto_allow_once"
}
```

- `role` is an optional per-delegation semantic hint and defaults to
  `investigate` when omitted.
- `write_scope` is optional task metadata used for prompting, audit, and
  overlap checks. A write role without a scope remains valid but receives no
  MothX `fs/write_text_file` bridge.
- Legacy `roles` and `writeRoots` fields still load for settings compatibility,
  but they are ignored and are no longer projected by the TUI or management
  APIs. An external ACP Agent runs its own tools, so neither field can confine
  its writes.
- `maxConcurrentChildren` defaults to one concurrent read-only worker per
  session and is bounded at 8. Read-only roles may fan out up to this ceiling and share the
  stable worktree. A write role (`implement`/`integrate`) remains exclusive
  because two writers would race the same session worktree and the read-only
  lead cannot resolve that conflict. The Runtime permit consumes
  `EffectiveEnforcedChildren`; `mothx doctor` (`acp.profiles.concurrency`) and
  the `/router` panel report the configured and enforced values. Parallel writes
  stay gated on a Runtime-owned per-worker worktree and managed change sets.
- Profile lookup still copies legacy slices so a caller cannot mutate global
  configuration through the returned value.

### 2. `SubagentTaskContract` validation boundary

`internal/agentruntime/router_task_contract.go` defines the Runtime-owned
contract (`objective`, `role`, `readScope`, `writeScope`,
`expectedDeliverables`, `verification`, `stopConditions`, `targetProfile`) and
`Validate(profile)`:

- the target profile must be the selected one;
- `role` is optional and defaults to `investigate`; any known role is accepted;
- `write_scope` is optional for every role and is checked only for
  normalization (relative paths that do not escape the working directory);
- deliverables are role-specific, deduplicated, and bounded;
- `implement`/`integrate`/`test` require verification evidence;
- objective, scope entries, verification, stop conditions, and the total
  contract are byte-bounded.

This is task metadata and bounded prompting, not a filesystem sandbox: the external
Agent's process policy remains the real containment boundary.

### 3. Router rejects interactive profiles

`ResolveRouterACPProfile` now rejects a profile whose effective permission
policy is `interactive`. A Router worker that blocks on a UI decision would
stall the lead with no adapter able to answer it; auto-allow-once/auto-reject
remain the only Router strategies.

### 4. External ACP client ownership guard

`internal/architecture/acp_client_guard_test.go` proves:

- only `internal/agentruntime` imports `internal/acp/client` in production
  code; and
- the client package stays transport-only (no imports of Agent Core, Runtime,
  DAO/DB, provider, session, serve, or TUI).

The guard was mutation-checked by adding a `session` import to the client.
Raw subprocess ownership is now the Runtime-owned process-tree launcher; the
client only consumes the supplied `Launcher` and stdio pipes.

## Validation

Executed with loopback/UDP access enabled:

```text
go test ./internal/config ./internal/acp/client ./internal/session \
  ./internal/agentruntime ./internal/tools ./internal/agent ./internal/tui \
  ./internal/acp ./cmd/mothx ./internal/architecture
```

Result: all packages passed.

`git diff --check` also passed at the time of this note.

### 5. Router activation (TUI/CLI)

`SessionRuntime.ActivateRouter(ctx, sessionDir, settings, binding)` is the only
activation path:

- the profile must exist in `acpAgents`, be enabled, and use a non-interactive
  permission policy;
- the source must not be forced to yolo (WeChat/Feishu cannot select Router);
- the session must project as `SessionExecutionIdle`;
- the binding is persisted first and the mode entry second, so a crash between
  the two leaves a non-Router session (fail closed rather than a Router tool
  surface without an eligible worker).

The activator only names a profile; Runtime fills the non-blocking approval
strategy from the profile policy and rejects a mismatched explicit value.
`ResolveRouterACPProfile` re-checks that recorded strategy against the current
profile policy, so a policy change requires reactivation instead of silently
keeping the old approval behavior.

Router is not an independent mode: `applyModeOption` rejects `router` without a
persisted binding. This closes `/mode router`, ACP `session/set_mode`, and
`-M router` as ways to obtain the Router tool fence with no eligible worker.

The same slice makes persisted mode authoritative on attach: `BindSession`,
`AttachSessionResources`, and `Builder.Build` all replay the newest
`GetLatestModeChange` entry through the shared resolver. A resumed session
therefore reopens in Router (or any other persisted mode) even when the adapter
has no startup flag; CLI `-M` and `/systeminit` still win explicitly.

TUI: `/router <acp-profile-id>` (added to command specs/i18n). It refuses while
a run is active, then updates the local mode and resets the agent exactly like a
mode switch.

CLI: `mothx --router --acp-agent codex "..."` (or `-M router --acp-agent
codex`). `--router`/`-M router` without `--acp-agent`, `--acp-agent` without
Router intent, and `--acp-agent` combined with a non-Router `--mode` are
rejected before any session is activated. Interactive runs now create a session
when Router activation is requested. After activation the selected mode is
`router` for both print and interactive runs.

Tests: `TestActivateRouterPersistsModeAndBinding`,
`TestActivateRouterRequiresIdleAndEligibleProfile` (agentruntime),
`TestRouterCommandActivatesRuntimeBinding`,
`TestRouterCommandRejectsUnknownProfileAndBusySession` (TUI), and
`TestRouterActivationProfileValidation` (CLI).

## External delegation slice (2026-11-03)

### 1. AgentManager external child port

`internal/agent.ChildBackend` lets the Runtime install a child constructor.
`AgentManager.Create` keeps all existing worktree, parent, mode, policy, status,
cancellation, and listener behavior, then asks the backend before the local
factory. `ACPChildBackend` returns `handled=true` for every `ExternalAgent`
request (including errors), so a Router delegation can never fall back to a
local child. Agent Core carries only opaque `External*` fields.

### 2. Router delegation contract

`delegate_subagent` gained `agent`, `role`, `read_scope`, `write_scope`,
`expected_deliverables`, `verification`, and `stop_conditions`. In Router the
target profile is required and must match the activated binding; outside Router
an `agent` argument is rejected. `SynchronizeToolGroups` exposes the tool in
Router regardless of the adapter's delegate flag. `SubagentTaskContract
.RenderPrompt` produces the bounded worker prompt (contract sections plus
MothX-owned worker rules) and never forwards parent prompts or history.

### 3. External child proxy and event mapping

`acpChildProxy` implements `agentpkg.Agent`. It runs the bridge, maps typed ACP
updates to canonical public events (text, thought, namespaced tool calls and
updates, plan, context usage), accumulates a 16 KiB-bounded result, appends the
remote transcript to `GetMessages`, and emits exactly one `EventRunFinished`
plus the legacy done/error compatibility event. The existing delegate monitor
still owns status transitions, `ForwardChildAgentEvent`, and the parent tool
result. `ChildMetadata`/`CompletionMetadata` add bounded `worker-reported`
metadata to the manager status and delegate result.

### 4. Decisions and cancellation

Remote `session/request_permission` is parsed in
`acp_worker_decisions.go`. The Runtime records a pending/resolved decision in
the canonical ledger, resolves it through `DecisionService`, and answers with
the offered `allow_once`/`reject_once` option; missing options become
`cancelled`, and `allow_always`/`reject_always` are never auto-selected.
Only a small base environment (`PATH`, `HOME`, locale, temporary directories,
and Windows process basics) plus the profile's explicit variable names reaches
the worker; text/thought/tool/plan projections are byte-bounded, and the worker
output is capped at 16 KiB. Cancellation still sends `session/cancel`, the client cancels outstanding
calls, and the proxy releases the per-session worker permit exactly once. A
permission request observed after the run context is cancelled is answered
`cancelled`, never `allow_once`; a failed decision registration/resolution
records a terminal cancelled row instead of leaving a pending one. Partial
worker output is preserved on prompt/connection failure, following the shared
"safe partial turn" rule.

### 5. Process containment and transport purity

`internal/acp/client` now accepts a `Launcher` and owns no process; the
Runtime's `acpProcessLauncher` uses a Unix process group or a Windows
kill-on-close Job Object. A Unix regression test starts a shell with a
background descendant, terminates the launcher, and proves the descendant is
reaped. A `Wait`/`Terminate` race is closed with process state synchronization,
and `Terminate` never signals after a known exit. The architecture guard now
forbids `os/exec` in the client package.

Worker text beyond the 16 KiB parent completion bound continues to stream as
canonical child text deltas; only the accumulator copied into the lead's
completion envelope is bounded.

### 6. Diagnostics, management projection, and docs

`mothx doctor` adds `acp.profiles` (shape, executable presence/executable bit,
no launch). `agentruntime.ListACPWorkerProfiles` and the read-only
`mothx/manage/acp/profiles/list` projection expose safe descriptors (ID, title,
enabled/eligible, policy, concurrency) and never argv/environment/paths.
Executable-profile writes remain on the deliberate local settings path.
Bilingual configuration and `delegate_subagent` documentation describe setup,
the trust boundary, diagnostics, and the exact contract.

### 7. Validation

```
go build ./...
GOOS=windows go build ./internal/agentruntime ./internal/acp/client
go test ./internal/agent ./internal/agentruntime ./internal/acp/client   ./internal/acp ./internal/tui ./cmd/mothx ./internal/doctor   ./internal/architecture ./internal/serve/...
```

All packages passed at the time of this note.

## Follow-up closed 2026-11-03: preset picker and Router target projection

Two usability defects surfaced after the MVP landed. Both are closed and
recorded in the proposal's Implementation Status section.

### The model could not learn which worker it was bound to

`delegate_subagent` advertised a free-form `agent` string, so a lead model
guessed a plausible product name — a user reported it asking for `codex`
while the session was bound to `opencode` — and every delegation failed on a
rule the model had no way to learn.

The fix is a Runtime-owned projection, not an adapter hint:
`SynchronizeToolGroups` calls `publishRouterTarget`, which reads the session's
persisted `RouterBinding` and publishes it into the tool through
`agent.DelegateSubAgentTool.SetRouterTarget`. The tool then names the
activated worker in its description, pins the `agent` schema to that ID with
`const` (even when several profiles are configured), and fails a wrong value with
the accepted name instead of restating the rule.

Because the target is republished on every tool-group synchronization,
activation, binding changes, and leaving Router mode are all reflected
without rebuilding the tool instance, and a stale profile ID cannot survive
into a non-Router session.

### Configuring a worker required knowing its launch recipe

Several popular CLIs do not start an ACP server themselves: `codex acp` and
`claude acp` are not valid, and the official adapter packages
(`@zed-industries/codex-acp`, `@zed-industries/claude-agent-acp`) are the real
entry points. `agentruntime.ACPAgentPresets()` records the verified recipe
for each well-known agent, and the TUI `/router` panel opens on that picker
rather than an empty form. Every preset carries the install caveat it
implies.

This relaxed one validation rule: `command[0]` no longer has to be an
absolute path, because a bare command resolved through `PATH` is exactly how
every other process launch works and the presets depend on it. Launch still
uses `exec.Command` and never a shell, so there is no injection surface to
close. `mothx doctor` was updated to resolve the command the way a real
launch would (`exec.LookPath`) rather than `os.Stat`-ing it, so a
PATH-resolved worker is diagnosed correctly.

### Tests

- `internal/agent/delegate_router_target_test.go`: schema validity, schema
  pinning, target copying and clearing, and the correction messages.
- `internal/agentruntime/router_target_projection_test.go`: the activated
  worker reaches the tool description and schema, and the target is cleared
  when the session leaves Router mode.
- `internal/agentruntime/router_presets_test.go`: every preset expands to a
  valid, Router-eligible profile; no preset reintroduces a non-ACP
  self-server command; the preset recipe is copied, not aliased.
- `internal/tui/router_presets_test.go`: the picker is the create flow's
  entry point, a chosen preset fills a usable profile and persists, argv
  arguments survive, and Esc leaves no half-open form.

## Post-MVP hardening (2026-11-03)

The MVP review found four implementation-level gaps and closed them:

- `internal/acp/client` no longer closes its notification channel while a
  reader may be sending. Consumers stop on `Done` and drain already-buffered
  notifications. Inbound frames use fragmented bounded reads, so an oversized
  line is rejected before the whole payload is allocated.
- The Router `agent` schema is always a `const` for the activated worker.
  Other configured profiles appear only as correction context, never as
  provider-visible enum values.
- `delegate_subagent` explicitly rejects local-child parameters in Router
  (`mode`, `work_dir`, `worktree`, `tools`, `max_iterations`) instead of
  silently ignoring them. The proposal and bilingual tool docs describe the
  blocking path and, below, the later role-aware async slice.
- ACP permission cancellation also goes through `DecisionService`, and a
  failed resolution commit discards the in-memory pending entry after writing
  the terminal ledger row. ACP output truncation now preserves UTF-8 runes.
- Windows workers start suspended, join the Job Object, and are resumed only
  after assignment, closing the window in which a descendant could escape.

## Role-aware async spawn (2026-11-03)

Router now exposes `subagent_spawn`, `subagent_status`, `subagent_wait`, and
`subagent_destroy` alongside blocking `delegate_subagent`. Read-only workers may
fan out; writers remain exclusive:

- `subagent_spawn` accepts the activated `agent` and the same bounded task
  contract as `delegate_subagent`, returns the existing manager handle, and
  rejects `member`, `mode`, `work_dir`, `worktree`, `tools`, and
  `max_iterations`.
- `SynchronizeToolGroups` reconciles the Router surface from the canonical
  subset, so entering Router cannot leave `subagent_send`/`subagent_answer`
  alive from an earlier multi-agent configuration.
- `subagent_wait(handle: ...)` polls the existing `AgentManager` status until
  that child reaches a terminal state. It adds no second child state machine,
  and Router does not need a team mailbox to wait.
- `ACPChildBackend` acquires the role-aware worker permit while constructing the
  child, not inside the asynchronous run goroutine. A second conflicting spawn
  therefore fails synchronously; the proxy releases the permit on terminal
  cleanup or destroy when the worker never started. Read-only roles and
  isolated `implement` workers fan out to the profile ceiling, while a
  base-workspace writer is exclusive in both directions.
- The external proxy keeps the remote session alive across prompts.
  `subagent_send` drives follow-up turns on that session; `subagent_answer`
  remains absent because ACP decisions use the parent `DecisionService`.
- Router and an Expert Team are mutually exclusive. Activation rejects a bound
  team, `SetExpert` rejects a team while Router is active, and Agent
  construction fails closed if both persisted states survive. A solo expert is
  still allowed.

Tests cover Router schema pinning, contract forwarding, local-parameter and
member rejection, non-Router target rejection, the canonical Router tool
surface, handle-only waiting without a mailbox, and synchronous permit refusal.

## Phase 4 transport slices (2026-11-03)

- The Runtime now owns an ACP connection pool keyed by profile/workdir. One
  initialized process hosts multiple isolated remote sessions; notifications
  and reverse permission requests route by `sessionId`, and every child still
  emits one canonical terminal event.
- `maxConcurrentChildren` is effective for read-only roles. The permit tracks
  readers and writers separately: readers may share the stable worktree up to
  the configured ceiling, while a writer requires exclusive access.
- Router tools accept Runtime input resource IDs in `attachments`. The Runtime
  verifies ownership, size, and hash; the bridge sends ACP `image`, `audio`, or
  embedded `resource` blocks only when the Agent advertised the corresponding
  `promptCapabilities`. Unsupported content stays visible as a workspace-
  relative fallback instead of being silently dropped.
- The client advertises a read-only `fs/read_text_file` bridge only when the
  Runtime has a Registry. Requests resolve absolute paths through the shared
  workspace roots and symlink fence, return at most 1 MiB of valid UTF-8 text,
  and honor ACP's 1-based `line`/`limit`. `session/new` forwards the same
  Runtime-authorized session roots as `additionalDirectories`, so an Agent that
  honors ACP's session root set and the MothX bridge agree on what is readable.
  A Registry-backed connection advertises `fs/write_text_file`; a write-role
  task with a non-empty `writeScope` may use it. Each request must resolve
  inside that task scope and uses the shared file lock and atomic write path.
  Profiles with `"terminal": true` get the terminal bridge; commands
  run through the shared sandbox, process-group containment, and a bounded
  non-interactive output buffer. Terminal/URL authentication remains unimplemented.
- The client gained a synchronous notification handler so a following JSON-RPC
  response cannot overtake earlier session updates, plus typed
  `session/resume`/`session/load` helpers for reconnect policy.

Tests cover concurrent sessions sharing one fake process, per-session update
routing, read-only fan-out versus writer exclusivity, capability-specific
content blocks, explicit unsupported-attachment disclosure, and Runtime-owned
attachment reads. Filesystem bridge tests cover line windows, workspace-root
escape, oversized input, and non-UTF-8 rejection.

## Phase 4 session continuity (2026-11-03)

- One remote ACP session now survives across child turns. `subagent_send`
  drives a follow-up prompt on the same session; the child stays idle between
  turns and is closed by `subagent_destroy`. The local sub-agent wall-clock
  budget does not apply to the external follow-up path.
- A dead transport is not replayed. The next turn prefers `session/resume`
  when the Agent advertises it and otherwise explicitly reconnects through
  `session/load` when the Agent advertises `loadSession`; without either
  capability the turn fails instead of silently creating a fresh remote
  conversation. The remote session ID is retained across reconnects.
- Migration 55 adds `session_child_runs`; DAO/session owners persist the child
  transcript, contract, profile, remote session ID, worktree, status, and
  timestamps. ACP children write their remote identity; local children use the
  Runtime-installed recorder, and both write after each terminal transition.
- `subagent_answer` remains absent for Router because ACP decisions continue to
  use the parent `DecisionService`; there is no second child question mailbox.

## Final protocol and history review (2026-11-03)

The final code/doc cross-check closed the remaining implementation gaps:

- The ACP client tracks in-flight reverse requests by canonical JSON-RPC ID.
  An inbound `$/cancel_request` cancels only the matching request and returns
  `-32800` (`Request cancelled`), while stale or differently typed IDs are
  harmless. `session/close` treats ACP `-32002` (unknown/already-closed
  session) as success, matching ACP's idempotent-close rule.
- New remote sessions no longer start their notification projector until the
  first prompt has installed the turn. `session/load` history replay is drained
  during `Open`; `session/resume` does not replay history by design.
- `session/new`, `session/load`, and `session/resume` send
  `additionalDirectories` only after the Agent advertises
  `sessionCapabilities.additionalDirectories`.
- Terminal output truncation preserves UTF-8 rune boundaries, reports exact
  limit-sized output as not truncated, and the no-sandbox terminal path uses
  the worker's restricted environment instead of inheriting MothX secrets.
- The TUI `/router` profile form exposes the profile `terminal` opt-in.
- Local child backends now persist to `session_child_runs` through a
  Runtime-installed recorder. The Runtime owns the DB/session path; Agent Core
  supplies only child identity, parent Run, transcript, workdir, and status.
- A first external turn that fails before `session/new` yields a remote session
  must not turn a later `subagent_send` retry into a history-less follow-up.
  The proxy now treats "no remote session ID" as a first turn, re-sends the
  full task contract, and appends the retry instruction; a failure after a
  remote session exists still resumes/loads that session.

## Managed change-set integration (2026-11-04)

The per-child worktree and bounded snapshot foundation now has its application
path, without adding a second Agent or a parallel run model:

- `internal/worktree.ChangeSet.Verify` re-checks the immutable snapshot digest,
  path safety, and untracked-file content hashes. A truncated patch or
  untracked snapshot is explicit and can never be applied.
- `internal/worktree.ApplyChangeSet` dry-runs the tracked patch with
  `git apply --check`, applies it, then installs untracked bytes through a
  component-wise symlink fence and atomic rename. It never reads the mutable
  child worktree.
- Capture marks the snapshot truncated when an untracked status entry cannot be
  represented safely (for example a symlink, special file, or unreadable
  entry), so integration refuses an incomplete snapshot instead of silently
  omitting the path.
- `agentruntime.RuntimeChangeSetIntegrator` loads only durable child history,
  requires a successful terminal status, no snapshot error, a matching digest,
  no truncation, and a managed-worktree identity. It applies selections in
  order under the Runtime's exclusive base-workspace permit lane and records
  `applied`/`conflict`/`failed` evidence with `integration_id`,
  `integration_digest`, `integration_status`, `integration_error`, and
  `integrated_at` (migration 57).
- A multi-selection is transactional within one request. If a later snapshot
  conflicts or fails, `worktree.RollbackChangeSet` reverses the tracked patch
  and removes only untracked files created by that request (after re-checking
  their digest), and the earlier children's durable evidence is updated from
  `applied` to `failed`. Selections that were already applied before the
  request are not touched.
- `subagent_integrate` is the Router tool projection. The lead supplies
  `[{handle, digest}]`; malformed, duplicate, missing, or stale selections are
  rejected before mutation. A repeated identical selection is idempotent, and
  a git conflict returns structured evidence with a nil tool error.
- The Router tool catalog includes `subagent_integrate` in the frozen mode
  surface, and leaving Router removes it through the same canonical
  `RouterOnlySubAgentToolNames` cleanup.

Tests cover tracked and untracked application, repeated application,
digest/truncation rejection, untracked-file conflict preservation, symlink
escape rejection, conflict evidence, and the tool-selection projection.

## Remaining (Phase 4 deliberate follow-ups)

Phases 0–3 and the transport/attachment/fan-out/integration/continuation
slices above are implemented as of 2026-11-04. Terminal or URL authentication
flows remain explicitly out of scope.

Change-set capture, selection, and application are implemented.

### Parallel base-workspace writers (2026-11-04)

Parallel base-workspace writers are available behind an explicit per-profile
opt-in, `allowParallelBaseWorkspaceWrites`. The default stays exclusive, so
every existing `settings.json` profile keeps its current behavior.

- The opt-in relaxes only the bidirectional exclusivity of the base-workspace
  write lane. A parallel writer still consumes the ordinary
  `maxConcurrentChildren` ceiling, and a ceiling of one still serializes.
- The Runtime still refuses a parallel writer whose declared `write_scope`
  overlaps a live base writer. The scope is the contract the worker was given,
  so this is the one part of the risk that can be checked without confining the
  worker's own tools.
- The residual risk is real and stated in the product: an external ACP Agent
  runs its own tools, so MothX cannot confine its writes to the declared
  `write_scope`. Two workers can still touch the same file. The opt-in is
  therefore explicit operator trust for operators who partition the workspace
  by module.
- `mothx doctor` reports the opt-in as a warning, and the `/router` panel
  exposes it as "Parallel workspace writes", so the risk stays visible rather
  than being a silent behavior change.
- Per-child worktrees plus the exclusive `subagent_integrate` lane remain the
  safer default and are unchanged.

### Automatic mailbox wakeups (2026-11-04)

The mailbox completion path is now Runtime-owned:

- `internal/agentruntime/mailbox_wake.go` exposes
  `SessionRuntime.EnableMailboxWake`/`DisableMailboxWake` and uses
  `MemberMailbox.AddActivityListener` as an advisory trigger. A 25ms debounce
  (capped at 500ms) coalesces a completion burst into one admission attempt.
- The wake re-checks `InspectSessionExecution`; only idle/local sessions
  proceed. Remote/unknown ownership is left to its owner. Admission is through
  `SubmissionCoordinator.Submit` (`QueueBehindLocal`) and the canonical
  `ExecutionRuntime.BeginDurable`/`FinishDurableWithRetry` transaction.
- The lead seed is `provider.NewSystemInjectedUserMessage`; a synthetic
  continuation does not reserve `RunUserEntryID`, so no fake user transcript
  entry is written. Canonical events are observed once by the Runtime and then
  projected to each adapter.
- TUI and ACP attach through `OnStarted`/`OnEvent`/`OnArtifact`/`OnAgent`/
  `OnFinished`. Their renderers never call a second terminal finish for the
  wake Run; cancellation reaches the same `ExecutionRuntime`, and ACP's
  permission/question bridge reuses the session `DecisionService`.
- Tests cover burst coalescing into one Run, a single provider call, the
  system-injected seed, no pending-work Run, TUI terminal projection without a
  foreground finish, and ACP `run_status`/state cleanup.

The standard remote transport is also implemented behind the same
transport-only client; it remains opt-in per profile because ACP v1 stdio is
still the default.

## Managed child worktrees and change-set snapshots (2026-11-03)

The ACP backend now consumes the worktree identity that AgentManager already
resolves instead of ignoring it:

- `AgentOptions.WorktreeResolved` carries the trusted directory, registry ID,
  repository root, and base revision; a model-supplied `work_dir` remains
  ignored.
- `ACPChildBackend` uses that directory, and `acpWorkspaceFor` builds an
  isolated `tools.Registry`/sandbox view rooted there. `fs/read_text_file`,
  scoped `fs/write_text_file`, and `terminal/*` therefore cannot read or write
  the main session workspace while a worktree is active. The shared file-lock
  manager and atomic write path are preserved.
- `worktree.Manager.CaptureChangeSet` records base/head revisions, changed-path
  statuses, a bounded unified/binary patch, and bounded untracked content with
  SHA-256 digests. Capture is limited to directories under the managed worktree
  root.
- Migration 56 adds `worktree_id`, `base_revision`, `change_set_json`, and
  `change_set_error` to `session_child_runs`; ACP terminal paths capture before
  persisting history, and completion metadata exposes the digest without
  treating it as verification.

## Multi-profile target selection and external mailbox identity (2026-11-03)

The Router binding now persists an ordered worker allowlist instead of a single
profile:

- `RouterBindingEntry.Profiles` records each bound profile with the
  non-blocking strategy captured at activation; the legacy singular
  `acpProfileId`/`approvalStrategy` fields remain the first/default entry.
- `SessionRuntime.ActivateRouter` validates every profile, fills each strategy
  from that profile's policy, and requires all of them to be eligible.
  `ResolveRouterACPProfileByID` resolves the selected worker from the same
  persisted allowlist.
- CLI `--acp-agent id1,id2` and TUI `/router id1,id2` bind an ordered allowlist;
  the first ID is the default. The panel remains a single-profile editor, and
  re-activation through it replaces the allowlist with that one profile.
- `RouterTarget` carries the bound allowlist separately from the configured
  correction list. A single bound worker is still a schema `const`; multiple
  bound workers are a schema `enum` limited to the bound IDs.
- `ACPChildBackend` resolves the requested `agent` ID rather than assuming the
  primary profile, and stamps the task contract with the actually selected
  profile.

The session mailbox is now consistent for external workers too. A spawned ACP
worker completion is keyed by its canonical child handle (not an empty member
ID), is marked `External`, and its error hint points at
`subagent_status`/`subagent_send` instead of a local member respawn. This does
not add a second child state machine: the payload still comes from the existing
manager monitor and is delivered by the same mailbox drain.

## Real interoperability fixture (2026-11-03)

The first real ACP interoperability target is OpenCode's native
`opencode acp` stdio server. `TestRealOpenCodeACPInterop` drives the production
`ACPWorkerBridge` (initialize, session/new, prompt, session/close) against that
launch contract and is opt-in:

```bash
MOTHX_ACP_INTEROP=1 go test ./internal/agentruntime \
  -run TestRealOpenCodeACPInterop -v
```

`MOTHX_ACP_INTEROP_OPENCODE_BIN` selects a non-PATH executable and
`MOTHX_ACP_INTEROP_ENV` is a comma-separated credential allowlist. The default
suite remains hermetic and uses the fake ACP Agent, so no vendor extension is
encoded as generic ACP v1 behavior.

## ACP standard remote transport (2026-11-04)

The transport-only client now implements the published ACP remote transport
RFD without adding a second Runtime:

- `internal/acp/client` owns stdio, WebSocket, and streamable HTTP/SSE behind
  one `frameTransport` contract. The JSON-RPC reader, pending-request
  correlation, reverse-request dispatch, cancellation, frame bounds, and
  notification ordering are unchanged.
- WebSocket uses one upgraded `/acp` connection, one JSON-RPC message per text
  frame, ignores binary frames, and rejects inbound JSON-RPC batches with
  `-32600` without closing the connection.
- Streamable HTTP sends `initialize` as a synchronous 200 response carrying
  `Acp-Connection-Id`, sends every later message as a POST expecting 202, and
  reads responses/notifications from one connection-scoped plus one
  session-scoped SSE GET stream. The client forwards cookies, requires HTTP/2
  for `https://` endpoints, sends `Acp-Session-Id` on session-scoped POSTs and
  reverse-request responses, and terminates with `DELETE`.
- `acpAgents` gains `transport` (`stdio` default, `websocket`, `http`) and one
  `url` for remote profiles. Remote profiles reject `command`, `environment`,
  embedded credentials, and mismatched URL schemes. The Runtime pool keys its
  reusable connection by transport and endpoint, so stdio and remote profiles
  never share a connection.
- The connection key no longer includes the per-task write flag. ACP negotiates
  client capabilities once per connection, so a profile that can ever write
  advertises the write bridge on its shared connection; each session handler
  still checks that session's role, write scope, profile roots, and workspace
  fence before writing. Read-only and write-role sessions for the same
  profile/workdir therefore share one initialized process or remote
  connection.
- `mothx doctor` and the TUI `/router` panel validate/project the same profile
  shape; doctor still never launches a process or connects to an endpoint.

Hermetic tests cover WebSocket lifecycle, binary-frame tolerance, batch
rejection, HTTP/2 streamable-HTTP initialization, cookie affinity, connection
and session SSE routing, reverse permission responses with session headers,
DELETE shutdown, and Runtime pool reuse across isolated remote sessions.

## 2026-11-04 review hardening

A review of the implemented proposal against the code closed four gaps:

- `acpWorkDirIsIsolated` now resolves symlinks on both sides, rejects an empty
  base directory, and only admits an `implement` writer as isolated when its
  physical directory is outside the session workspace. Everything else remains
  in the exclusive base-writer lane.
- Streamable HTTP no longer guesses response direction from frame shape.
  `Client` calls `CompleteRequest` only after correlating an inbound response
  with its pending outbound call, so a reverse request that reuses the same
  JSON-RPC ID cannot leak or prematurely release the request -> session map.
  Cancellation releases the map after the `$/cancel_request` is written.
- Embedded ACP resources now use `urn:mothx:attachment:<id>`; a worktree does
  not contain the session-workspace `.mothx/tmp/inputs` file, so a `file://`
  URI was false. Unsupported attachments disclose the Runtime resource ID and
  session-relative path as provenance instead of promising worker access.
- `AgentManager.updateStatus` refreshes `ChildMetadataProvider` metadata, so
  the terminal change-set digest/truncation/error appears in
  `subagent_status`/`subagent_wait` after the spawn monitor records completion.

Validation for this pass: focused `-race` runs for `internal/acp/client`,
`internal/agentruntime`, and `internal/worktree`, plus the full `go test ./...`,
`go build ./...`, `go vet ./...`, and `internal/architecture` suites.
