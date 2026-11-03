# Router ACP MVP Progress (2026-11-02, completed 2026-11-03)

> Status: Phases 0–3 of
> [`acp-client-external-subagent-proposal.md`](../proposal/acp-client-external-subagent-proposal.md)
> are implemented. Phase 4 follow-ups remain deliberate and are listed below.
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
- CLI accepts `-M router`; TUI supports `/mode router`, completion, mode
  cycling, footer display, default-mode cycling, and English/Chinese
  permissions copy. ACP session mode catalogs also recognize Router so it is
  not treated as an unknown persisted mode.

### 2. ACP worker profile configuration

Files:

- `internal/config/settings.go`
- `internal/config/settings_test.go`

`Settings.ACPAgents` now contains `ACPAgentProfile` entries:

```json
{
  "id": "codex",
  "title": "Codex",
  "command": ["/absolute/path/to/codex", "acp"],
  "enabled": true,
  "environment": ["OPENAI_API_KEY"],
  "permissionPolicy": "auto_allow_once"
}
```

Implemented constraints:

- profile IDs are lower-case ASCII identifiers;
- `command[0]` must be absolute, preventing PATH replacement at launch;
- environment entries are variable names only, never inline secret values;
- duplicate IDs and unsupported permission policies fail validation;
- `ACPAgentProfile()` returns only enabled, valid profiles and copies slices;
- empty/unknown permission policy resolves conservatively to `auto_reject`.

This schema is not exposed by an in-product configuration editor. It remains a
local, deliberate settings edit (profile configuration is executable-code
trust); `mothx/manage/acp/profiles/list` projects a read-only safe catalog and
no adapter reads `settings.json` directly.

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

### 1. ACP profile role ceiling

`acpAgents` profiles now declare:

```json
{
  "roles": ["investigate", "implement", "test", "review"],
  "writeRoots": ["src"],
  "maxConcurrentChildren": 2
}
```

- A profile with no `roles` is read-only (`investigate` only), so an omitted
  field cannot silently make a profile write-capable.
- Write roles (`implement`, `integrate`) require at least one `writeRoots`
  entry; roots must stay relative to, and inside, the Runtime-resolved child
  work directory.
- `maxConcurrentChildren` defaults to 1 (the serial MVP) and is bounded at 8.
- Profile lookup copies `Roles`/`WriteRoots` so a caller cannot mutate global
  configuration through the returned value.

### 2. `SubagentTaskContract` validation boundary

`internal/agentruntime/router_task_contract.go` defines the Runtime-owned
contract (`objective`, `role`, `readScope`, `writeScope`,
`expectedDeliverables`, `verification`, `stopConditions`, `targetProfile`) and
`Validate(profile)`:

- the target profile must be the selected one;
- the role must be inside the profile's declared ceiling;
- a read-only role may not declare a write scope, and a write role requires a
  non-empty one inside the declared write roots;
- deliverables are role-specific, deduplicated, and bounded;
- `implement`/`integrate`/`test` require verification evidence;
- objective, scope entries, verification, stop conditions, and the total
  contract are byte-bounded.

This is a contract/prompt ceiling, not a filesystem sandbox: the external
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
Raw subprocess ownership is still in the client and moves to the Runtime-owned
process-tree launcher in Phase 2 item 8.

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

### 6. Diagnostics, management projection, and docs

`mothx doctor` adds `acp.profiles` (shape, executable presence/executable bit,
no launch). `agentruntime.ListACPWorkerProfiles` and the read-only
`mothx/manage/acp/profiles/list` projection expose safe descriptors (ID, title,
enabled/eligible, roles, policy, concurrency) and never argv/environment/paths.
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

## Remaining (Phase 4 deliberate follow-ups)

Everything in Phases 0–3 is implemented as of 2026-11-03. The remaining items
are the proposal's deliberate follow-ups, not unfinished MVP work:

- capability-negotiated images/resources/attachments and client-owned
  filesystem/terminal bridges;
- terminal or URL authentication flows;
- remote session reuse/resume and reconnect;
- asynchronous `subagent_spawn(agent: ...)`, multi-profile fan-out, and mailbox
  wakeups;
- managed worktree change-set snapshots and an exclusive integration worker;
- durable historical child transcript/Run persistence for every child backend
  together.

The worktree/change-set item and parallel write workers still require their own
proposal before implementation.
