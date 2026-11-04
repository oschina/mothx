# ACP Client External Sub-agent Proposal

> Status: Implemented Phases 0–4 (multi-session process reuse, multi-turn sessions with capability-gated `session/resume`/`session/load`, multi-profile target selection, read-only and isolated-write fan-out, capability-negotiated attachments, scoped filesystem and opt-in terminal bridges, durable child history, managed per-child worktrees with bounded change-set snapshots, a Runtime-owned exclusive change-set integration lane, opt-in parallel base-workspace writers with static scope-overlap refusal, and the ACP standard remote transport over WebSocket and streamable HTTP/SSE). Terminal/URL authentication is explicitly out of scope.
>
> Date: 2026-11-02 · Completed: 2026-11-04
>
> Related: `docs/proposal/agent-core-runtime-unification-proposal.md`,
> `docs/proposal/multi-agent-architecture-plan.md`,
> `docs/proposal/delegate-mode-proposal.md`,
> `docs/proposal/expert-team-mothx-proposal.md`,
> `docs/proposal/codex-lead-subagent-research.md`

## 1. Summary

MothX already acts as an ACP **Agent/Server** through `mothx acp`: Desktop and
other ACP clients create MothX sessions and send it prompts. Before this
proposal it did not act as an ACP **Client** that starts another ACP-compliant
coding agent and uses that agent as one of MothX's sub-agents.

This proposal added that missing direction without creating a second Agent
Runtime:

1. an administrator/user configures a named, local ACP Agent profile, such as
   `codex`, `claude-code`, or another stdio ACP executable;
2. the shared `SessionRuntime` resolves which configured profiles a session may
   use;
3. a lead Agent calls the existing `subagent_spawn` or `delegate_subagent`
   tool with an optional `agent` profile ID;
4. MothX becomes the ACP Client for that child only: it launches the configured
   process, performs `initialize -> session/new -> session/prompt`, consumes
   `session/update`, handles the mandatory reverse permission request, and
   cancels/closes the child correctly;
5. the remote child's output is adapted to the existing canonical child Agent
   events, `AgentManager` status graph, mailbox, UI projections, and parent
   tool result.

The model never supplies an executable path, command-line arguments, arbitrary
environment, endpoint, or an unapproved profile. It may select only a
Runtime-resolved profile ID. An external ACP Agent is therefore an explicit
user-configured execution dependency, not a provider endpoint and not an MCP
server.

## Implementation Status (2026-11-03)

Phases 0–3 are implemented, followed by asynchronous spawn and the first Phase
4 transport slices. Router now reuses one initialized ACP process for multiple
isolated remote sessions, admits concurrent read-only workers up to the profile
ceiling, and negotiates image/audio/embedded-resource delivery from the Agent's
advertised capabilities. It also advertises bounded bridges for
`fs/read_text_file` and scoped `fs/write_text_file` constrained to the shared
Runtime workspace roots, the task write scope, and the profile's declared write
roots. Profiles that explicitly opt in also receive the terminal bridge.
Remote sessions stay alive across turns, `subagent_send` drives follow-ups, and
an explicit reconnect prefers capability-gated `session/resume` and falls back
to `session/load`; child transcripts and remote session identity are persisted.
Both local and ACP children now use the same durable child-history record.
Read-only workers and isolated `implement` workers may share the profile
ceiling because the latter write only a managed per-child worktree. The lead
applies selected immutable snapshots through `subagent_integrate`, which uses
one Runtime-owned exclusive base-workspace lane instead of starting another
external Agent. An idle session also wakes into a new canonical lead Run when
worker completions arrive in its mailbox, coalescing a burst into one Run and
projecting it through the same Runtime event path. The ACP standard remote
transport is implemented over WebSocket and streamable HTTP/SSE. Parallel
base-workspace writers are available behind an explicit per-profile opt-in.

| Area | Status | Implemented boundary |
| --- | --- | --- |
| First-class Router mode | Implemented | `ModeRouter`, shared policy validation, Router-only frozen local tool surface and system prompt, CLI/TUI/ACP mode catalogs. |
| ACP profile configuration | Implemented | `settings.json` `acpAgents` schema; argv validation, duplicate ID, environment-name, enabled-state, and permission-policy validation. |
| Router session binding | Implemented | Replayable `router_binding` session entry for an ordered profile allowlist and one non-blocking strategy per profile, supported by legacy store, EventStore, display projection, fork, and Runtime resolution. |
| Multi-profile Router target | Implemented | The replayable binding stores an ordered allowlist plus one recorded strategy per profile. The first profile is the default; tools accept any bound ID and reject configured-but-unbound IDs. `ResolveRouterACPProfileByID` resolves the selected worker from the same Runtime-owned binding. |
| ACP stdio client | Implemented | Transport-only `internal/acp/client`: NDJSON JSON-RPC correlation, notifications, reverse requests, v1 initialize/session helpers (including `session/resume`), frame bounds, and real subprocess tests. |
| ACP standard remote transport | Implemented | The same transport-only client supports WebSocket and streamable HTTP/SSE from the ACP remote transport RFD: cookie affinity, HTTP/2 for `https://`, `initialize` 200 plus subsequent 202 POSTs, one connection-scoped SSE stream, one session-scoped SSE stream per isolated session, required `Acp-Connection-Id`/`Acp-Session-Id` routing, reverse-request responses on the session stream, JSON-RPC batch rejection, bounded frames, and DELETE/close lifecycle. Runtime selects it from `transport`/`url` profile policy and keys the connection pool by transport plus endpoint. |
| ACP Worker Bridge | Implemented | Runtime-owned bridge with `initialize -> session/new|resume|load -> session/prompt` turns, typed update decoding, bounded output, partial-output preservation, cancellation notification, and a caller-supplied process launcher. One prompt runs at a time per remote session. |
| Router `delegate_subagent` | Implemented | `AgentManager` reserves/binds an external child through a Runtime-installed backend; `delegate_subagent` requires a bound profile in Router and rejects local fallback, while non-Router `agent` calls are rejected. |
| Dynamic task contract | Implemented | `maxConcurrentChildren` remains profile policy. `role` is an optional per-delegation semantic hint defaulting to `investigate`; `write_scope` is optional task metadata used for prompting, audit, and overlap checks. Neither is profile authorization or a filesystem sandbox. Legacy `roles`/`writeRoots` values still parse but are ignored and are no longer projected by the TUI or management APIs. |
| Router activation (TUI/CLI/WebUI/Desktop) | Implemented | `/router <profile>[,<profile>...]`, `--router --acp-agent <profile>[,<profile>...]`, the WebUI's `POST /api/sessions/<id>/router`, and the Desktop's `mothx/manage/router/activate` all call the same Runtime `ActivateRouter`: validates every profile and the source policy, requires an idle session, then persists the ordered Router allowlist before the mode entry. The first profile is the default target. Router selection without a binding is rejected, and every attach path replays the persisted mode. The WebUI and Desktop project the worker catalog through the same Runtime catalog the TUI panel renders, so eligibility stays one fact. Desktop stays a pure ACP projection: the selection is a `mothx/manage/*` projection, gated on the `manageRouter` feature key, and never enters Electron-owned storage. |
| External delegation and child projection | Implemented | External workers are blocking or asynchronous; a Runtime-owned `agent.Agent` proxy projects ACP text/thought/tool/plan/usage updates into canonical child events, owns the single `EventRunFinished`, and returns a bounded `worker-reported` envelope. Cancellation answers pending decisions as cancelled and reaps the process tree. |
| Diagnostics and docs | Implemented | `mothx doctor` checks profile shape/executable availability without launching; bilingual configuration and tool-contract docs added; `mothx/manage/acp/profiles/list` projects the safe Runtime catalog. Executable/profile writes intentionally stay on the local settings path. |
| Decision and process containment | Implemented | Remote `session/request_permission` records a canonical decision and answers immediately with an offered one-time option; the Runtime process launcher contains the tree with a Unix process group / Windows Job Object and `internal/acp/client` owns no process. |
| Router target projection | Implemented | The Runtime publishes the session's bound worker allowlist into both `delegate_subagent` and `subagent_spawn`: a single bound worker is a `const`, multiple bound workers are an ordered `enum`, and the first ID is the default. Unbound configured profiles appear only as correction context. A wrong `agent` value fails with the accepted names instead of a generic rule. |
| Router async spawn | Implemented | `subagent_spawn` accepts the same bound `agent` allowlist and bounded task contract and returns a manager handle immediately. `subagent_status`, `subagent_wait(handle: ...)`, `subagent_send`, and `subagent_destroy` project/observe the existing child lifecycle. External completions also enter the session mailbox keyed by the child handle. Read-only roles fan out to the profile ceiling; a workspace writer remains exclusive. `subagent_answer` and local-only parameters are not exposed. |
| ACP multi-session transport | Implemented | One Runtime-owned connection pool starts and initializes at most one process per profile/workdir, advertises the union of client capabilities its per-session handlers can enforce, then creates isolated remote sessions with session-scoped notification/reverse-request routing. Read-only and write-role sessions share that connection; each session's request handler still enforces its own task role and non-empty write scope. Process lifetime is Runtime-owned and closed by `SessionRuntime.Close`; a dead transport is never silently replayed. |
| Multi-turn external child | Implemented | A spawned child keeps its remote session alive between turns. `subagent_send` sends one follow-up prompt on the same session; the local sub-agent wall-clock budget does not apply. `subagent_destroy` closes it. |
| Remote resume/reconnect | Implemented | The remote session ID is retained after a transport loss. A later turn prefers capability-gated `session/resume` (no history replay) and falls back to `session/load` when only that capability is advertised; a missing capability fails the turn rather than silently creating a fresh session. In-flight prompts are never replayed after output or side effects. |
| Durable child history | Implemented | Migration 55 creates `session_child_runs`; migration 56 adds worktree/change-set fields and migration 57 adds integration evidence. DAO/session owners persist contract, transcript, backend, profile, remote session ID, workdir, worktree identity, base revision, change-set snapshot/error, integration result, status, and timestamps. The Runtime installs one recorder for local children; the ACP proxy persists its remote identity through the same record. |
| Read-only fan-out | Implemented | Read-only roles share the configured `maxConcurrentChildren` ceiling. An `implement` role in a Runtime-managed per-child worktree may also share the ceiling because it cannot write the base workspace. A base-workspace writer (including integration) remains exclusive in both directions. The permit is Runtime-owned and counts live workers and base writers. |
| Managed child worktree | Implemented | When `AgentManager` resolves a per-child worktree (explicit request or `worktree.perChildSubagents`), the ACP backend now uses that directory instead of silently falling back to the session workspace. Path resolution, `fs/*`, and `terminal/*` use an isolated Runtime registry/sandbox view rooted at the worktree; the session's global roots are not widened. |
| Change-set snapshot | Implemented | On every external-child terminal path the Runtime captures a bounded, immutable snapshot from the registered worktree: base/head revision, changed-path statuses, unified/binary patch, and bounded untracked-file content with digests. Migration 56 persists `worktree_id`, `base_revision`, `change_set_json`, and `change_set_error` in `session_child_runs`; a truncated or failed snapshot is explicit and never silently integrated. |
| Change-set integration | Implemented | Router exposes `subagent_integrate(changes: [{handle, digest}])`. The Runtime loads only durable child evidence, re-verifies the digest, successful terminal status, truncation state, and managed-worktree identity, then acquires one exclusive base-workspace lane. It applies the tracked patch with `git apply --check` followed by apply and installs untracked bytes through a component-wise symlink fence and atomic rename. It never copies or mutates the child worktree and never starts another Agent. A multi-selection is transactional within the Runtime: if a later snapshot conflicts, snapshots applied by that same call are reversed and their evidence is updated. Migration 57 records `integration_id`, `integration_digest`, `integration_status`, `integration_error`, and `integrated_at`; repeated identical selections are idempotent, and a git conflict is returned as structured evidence. |
| Capability-negotiated attachments | Implemented | The tool passes only Runtime input resource IDs; the Runtime verifies ownership/size/hash and the bridge advertises/delivers ACP `image`, `audio`, or embedded `resource` content only when the Agent's `promptCapabilities` supports it. Embedded resources use an opaque Runtime URN rather than a `file://` URI, because a managed per-child worktree does not contain the session-workspace attachment path. Unsupported content is disclosed with its Runtime resource ID and session-relative path instead of being silently dropped. |
| Filesystem and terminal bridges | Implemented | The bridge advertises `fs.readTextFile` only when the Runtime has a Registry, advertises `fs.writeTextFile` as part of a Registry-backed connection's capability union, and permits a write only for a write-role task with a non-empty `write_scope`. Reads resolve through the shared workspace roots and symlink fence with line/limit slicing; writes must resolve inside the task scope, then use the shared file lock and atomic write path. This bridge is convenience, not containment: the external Agent's own tools remain outside it. `terminal` is advertised only when the profile opts in. Terminal commands run through the shared sandbox and process-group containment with a bounded, UTF-8-safe, non-interactive output buffer and the worker's restricted environment; terminal/URL authentication is not implemented. `session/new`/`session/load`/`session/resume` send `additionalDirectories` only when the Agent advertises that session capability. |
| Known-agent preset picker | Implemented | `agentruntime.ACPAgentPresets()` holds verified launch recipes; the TUI `/router` panel opens on that picker instead of a blank form, because the most common configuration mistake is assuming `codex acp` / `claude acp` start an ACP server. |

The focused package suite covering these foundations passes; the detailed
record, including one self-delegation budget observation, is in
`docs/notes/20261102-router-acp-mvp-progress.md`.

### 2026-11-03 follow-up: preset picker and Router target projection

Two usability defects surfaced after the MVP and are now closed.

**1. The model could not know which worker it was bound to.** The
`delegate_subagent` tool advertised a free-form `agent` string, so a lead
model guessed a plausible product name (a user reported it asking for
`codex` while the session was bound to `opencode`) and every delegation
failed on a rule the model had no way to learn.

The fix is a Runtime-owned projection, not an adapter hint.
`SynchronizeToolGroups` calls `publishRouterTarget`, which reads the session's
persisted `RouterBinding` and pushes it into the tool
(`agent.DelegateSubAgentTool.SetRouterTarget`). The tool then:

- names the session's bound worker allowlist in its description, which is what
  the model reads first;
- pins a single bound worker with `const`; with multiple bound workers it uses
  an `enum` limited to that allowlist (with the first ID as the default), so a
  wrong name is a schema violation rather than a runtime surprise; configured
  but unbound profiles appear only in the correction text; and
- fails a wrong `agent` value with the accepted names (and the list of
  configured profiles) instead of restating the rule.

Because the target is republished on every tool-group synchronization,
activation, binding changes, and leaving Router mode are all reflected
without rebuilding the tool instance, and a stale profile ID can never
survive into a non-Router session. `agentRouterTarget` is a plain atomic
pointer, so the tool stays stateless in the AGENTS.md sense while remaining
safe to read from the model's view concurrently with an activation write.

**2. Configuring a worker required knowing its launch recipe.** Several
popular CLIs do not start an ACP server themselves: `codex acp` and
`claude acp` are not valid, and their official adapter packages
(`@zed-industries/codex-acp`, `@zed-industries/claude-agent-acp`) are the
actual entry points. `agentruntime.ACPAgentPresets()` records the verified
recipe for each well-known agent, and the TUI `/router` panel opens on that
picker rather than an empty form. Each preset carries the install caveat that
the recipe implies.

This relaxed one validation rule: `command[0]` no longer has to be an
absolute path, because a bare command resolved through `PATH` is exactly how
every other process launch works and the presets depend on it. Launch still
uses `exec.Command` and never a shell, so there is no injection surface to
close. `mothx doctor` was updated to resolve the command the way a real
launch would (`exec.LookPath`) instead of `os.Stat`-ing it, so a PATH-resolved
worker is diagnosed correctly.

Tests:

- `internal/agentruntime/router_target_projection_test.go`: the activated
  worker reaches the tool description and schema, and the target is cleared
  when the session leaves Router mode.
- `internal/agentruntime/router_presets_test.go`: every preset expands to a
  valid, Router-eligible profile; no preset reintroduces a non-ACP
  self-server command; the preset recipe is copied, not aliased.
- `internal/tui/router_presets_test.go`: the picker is the create flow's
  entry point, a chosen preset fills a usable profile and persists, argv
  arguments survive, and Esc leaves no half-open form.
- `internal/agent/delegate_router_target_test.go`: the generated schema is
  always valid JSON (including an ID that needs JSON escaping), a single bound
  worker is a `const`, multiple bound workers are a bound-only `enum`, the
  published target is defensively copied and reversible, and a wrong or missing
  `agent` value names the accepted workers.

### 2026-11-03 post-MVP hardening

Review closed four implementation gaps without widening the MVP:

- the ACP client leaves its notification channel open and drains it through
  `Done`, removing a close/send panic on worker exit; inbound frames are read
  in bounded fragments so the limit is enforced before allocation;
- the Router `agent` schema exposes only the session's bound allowlist: a
  single worker is a `const`, and multiple bound workers are an `enum`;
- Router rejects local-child parameters (`mode`, `work_dir`, `worktree`,
  `tools`, `max_iterations`) instead of silently ignoring them; and
- ACP permission cancellation uses `DecisionService`, failed resolution
  commits discard the in-memory pending entry after recording a terminal row,
  output truncation preserves UTF-8, and Windows workers start suspended until
  they join the Job Object.

### 2026-11-04 review hardening

A code-to-proposal audit closed four more gaps without widening the MVP:

- the isolated-writer admission check now compares physical paths (resolving
  symlinks) and fails closed for an empty base directory, so a worktree that
  resolves into the session workspace can never share the fan-out lane;
- streamable HTTP releases a request-id -> session mapping only when the client
  authoritatively correlates the outbound response. A colliding reverse-request
  ID no longer leaks the mapping or releases a live outbound call; cancellation
  still selects the correct `Acp-Session-Id`;
- embedded attachments use an opaque Runtime URN instead of a `file://` path
  that a managed worktree does not contain, and unsupported attachments name
  the Runtime resource ID plus the session-relative path; and
- the shared `AgentManager` refreshes backend-owned metadata on status
  transitions, so a terminal `changeSetDigest`, truncation flag, or capture
  error is visible through `subagent_status` and `subagent_wait` rather than
  only in the blocking delegate envelope.

## 1.1 MothX Architecture Gate

This feature is acceptable only as a policy-selected execution backend of the
existing front-end-neutral Runtime. It is not permission to add a second Agent
Core, a parallel session implementation, or an ACP-specific orchestration
path. The following invariants are hard requirements, not aspirations:

| MothX philosophy | Required interpretation for this feature |
| --- | --- |
| One Agent Core, one front-end-neutral Agent Runtime | `SessionRuntime` owns external-profile resolution, child resource assembly, prompt input conversion, process lifetime, decisions, event conversion, cleanup, and all policy. An ACP child is never constructed or driven by TUI, CLI, Serve, Channel, Desktop, or the ACP server adapter. |
| One construction path | No new adapter-local `agent.Config`, `agent.New`, AgentFactory, provider factory, registry, session replay, MCP bootstrap, or sandbox assembly is permitted. The remote executable is another product's Agent; MothX ownership stops at the Runtime-owned ACP-client boundary. |
| Policy, not forks | Source, effective mode, capability ceiling, sandbox/approval constraints, profile eligibility, worktree, and cancellation behavior are resolved once through `ResolveSource`/`ResolvePolicy` and Runtime-owned resolvers. A profile cannot weaken a parent/session forced mode or source policy. |
| One execution/lifecycle path | The parent remains a canonical `ExecutionRuntime` Run. Runtime-owned child execution joins the existing `AgentManager` child graph and its cancellation/status contracts. The ACP client cannot create a parallel run state machine, durable Run row, terminal result, or recovery policy. If child Runs later become durable, every child backend migrates together through `ExecutionRuntime`; ACP must not get a private store. |
| One decision model | An external Agent's `session/request_permission` is a projection into `DecisionService`/`DecisionRecord`; no request-local approval map, UI-local state, or direct callback owns the decision. |
| One event semantic model | ACP wire updates are decoded once by the Runtime adapter and projected as canonical `agent.Event` values with the parent/child identity. Adapters only render those events; they do not create a second external-child stream or terminal status. |
| One input/content and artifact lifecycle | Child task/context uses the Runtime input contract: bounded task text plus capability-negotiated image/audio/embedded-resource attachments. No adapter or ACP client directly manufactures provider content, reads attachment storage, uploads a mutable worktree file, or infers artifacts from remote text. |
| One database direction | The ACP client is transport-only and performs no SQL. Any profile, Run, decision, event, or future child-history persistence follows `config`/`session`/`dao` and Runtime services only. |

The design below is intentionally more restrictive than a generic ACP library:
being able to speak ACP is insufficient if the resulting child bypasses these
ownership boundaries.

## 2. Problem and Terminology

The word "ACP" appears in two independent MothX roles and must remain
unambiguous:

| Role | Current state | Direction |
| --- | --- | --- |
| MothX ACP server | Implemented in `internal/acp`; Desktop is a pure client | Desktop/IDE -> `mothx acp` |
| MothX ACP client | Proposed here | MothX parent Agent -> configured external ACP Agent |

There are also two kinds of tool call in this design:

| Term | Meaning |
| --- | --- |
| MothX tool call | The parent model invokes `subagent_spawn` or `delegate_subagent`. This is the entry point proposed here. |
| ACP remote tool call | The child Agent sends `session/update` with `tool_call` or asks MothX through `session/request_permission`. MothX displays, records, and answers the ACP request; it does not execute that remote Agent's private tool itself. |

The current in-process sub-agent implementation is valuable and stays the
default. It creates a child from the local `AgentFactory`, has isolated
registry/context, prohibits nested children, feeds child events through
`AgentManager`, and exposes the child through existing ACP/Desktop projections.
The new path must use those same Runtime-selected lifecycle and projection
contracts rather than introduce a separate "external-agent session manager" in
a CLI, UI, protocol adapter, or Agent Core utility.

## 3. Research Findings

Research was performed against the official ACP v1 documentation on
2026-11-02. The protocol facts that constrain the proposal are:

| ACP v1 fact | Design consequence |
| --- | --- |
| ACP uses bidirectional JSON-RPC 2.0. Requests need a result/error; notifications do not. | The client needs one reader, serialized writer, request-ID correlation, and a reverse-request dispatcher. It cannot be a one-way `exec.Command` wrapper. |
| ACP v1 core defines newline-delimited UTF-8 JSON-RPC over stdio. The published remote-transport RFD defines WebSocket and streamable HTTP/SSE on one `/acp` endpoint. | The client supports a contained local stdio process and the RFD's WebSocket/streamable-HTTP profiles. SSH and shell-string transports remain unsupported, and no endpoint is ever supplied by model output. |
| The normal sequence is `initialize`, then `session/new` or capability-gated `session/load`, then `session/prompt`; updates stream before the prompt response returns a stop reason. | Every external child owns one ephemeral remote ACP session and follows this handshake. MothX does not infer completion from stdout closing. |
| Client and Agent negotiate a major `protocolVersion`; omitted capabilities are unsupported. | MothX sends v1 and rejects a response version it cannot implement. It advertises only the reverse capabilities it actually implements. |
| `session/update` has standard message, thought, tool-call, plan, usage, command/config/mode, notice, and compaction variants. | Known variants map to canonical child events. Unknown/future variants remain bounded, untrusted child diagnostics rather than fabricated MothX semantics. |
| `session/request_permission` is the baseline Agent-to-Client request. On cancellation the client must answer outstanding requests with `{ outcome: { outcome: "cancelled" } }`. | The shared Runtime `DecisionService` owns the pending approval; cancellation answers every pending remote request before process teardown. |
| Filesystem, terminal, elicitation, authentication-terminal, notices, and compaction are capability-gated. | MothX advertises only the bridges it actually enforces: bounded reads, write-role scoped writes, and an explicitly opted-in terminal. It does not advertise terminal authentication, elicitation, notices, or compaction. |
| ACP describes Agent content, paths, IDs, tool labels, and permission labels as untrusted; permission is advisory for operations the remote Agent performs itself. | A profile is a trusted local executable chosen by the user. MothX must still render/record content defensively and cannot pretend its sandbox or allow list controls an Agent that acts without requesting permission. |

Primary references:

- ACP v1 overview: <https://agentclientprotocol.com/protocol/v1/overview>
- Initialization and capability negotiation: <https://agentclientprotocol.com/protocol/v1/initialization>
- Session setup: <https://agentclientprotocol.com/protocol/v1/session-setup>
- Prompt turn and updates: <https://agentclientprotocol.com/protocol/v1/prompt-turn>
- Tool calls and permissions: <https://agentclientprotocol.com/protocol/v1/tool-calls>
- Cancellation: <https://agentclientprotocol.com/protocol/v1/cancellation>
- Stdio transport: <https://agentclientprotocol.com/protocol/v1/transports>
- Content and security: <https://agentclientprotocol.com/protocol/v1/content>, <https://agentclientprotocol.com/protocol/v1/security>
- Extension rules: <https://agentclientprotocol.com/protocol/v1/extensibility>

There is no official Go SDK listed by ACP at the time of research. MothX
should implement the small v1 stdio client itself behind an internal package;
it must not make a third-party SDK a new public or Runtime boundary.

## 4. Goals

1. Let a parent Agent use a configured external ACP Agent as an asynchronous
   or blocking sub-agent through ordinary MothX tool calls.
2. Preserve one parent-child graph, one status model, one mailbox, one decision
   model, and one event projection for local and ACP-backed children.
3. Support ACP v1 stdio correctly, including initialization, session creation,
   prompt streaming, reverse permission requests, request cancellation, and
   process shutdown.
4. Make external execution opt-in, profile allowlisted, observable, bounded at
   protocol/resource boundaries, and truthful about its security limits.
5. Keep adapter behavior thin: TUI, CLI, WebUI/API, Channel, ACP server, and
   Desktop receive the same Runtime-owned tools/events; none owns an ACP child
   connection or a second sub-agent lifecycle.

## 5. Non-goals

- Replacing or changing default in-process `subagent_spawn` behavior.
- Turning `mothx acp` into a proxy for arbitrary remote ACP Agents.
- Supporting arbitrary model-provided commands, shell fragments, URLs,
  environment variables, SSH targets, or remote transports.
- Claiming that MothX sandbox/allow rules sandbox an independently launched
  remote Agent process.
- Sending the parent's full transcript, system prompt, secrets, session DB, or
  private attachments to another Agent by default.
- Adding nested delegation. ACP-backed children receive no MothX sub-agent or
  delegate tools from this feature.
- Implementing terminal or URL authentication, elicitation, custom extension
  methods, or automatic replay of an in-flight prompt.
- Creating a Desktop-only or ACP-server-only configuration store.

## 6. Product Contract

### 6.1 Profile configuration

External Agents are configured globally, owned by `internal/config`, and
resolved by `internal/agentruntime`. The exact JSON field name should be
reviewed with the existing settings schema, but its intended shape is:

```json
{
  "acpAgents": [
    {
      "id": "codex",
      "title": "Codex ACP",
      "command": ["codex-acp"],
      "enabled": true,
      "environment": ["OPENAI_API_KEY"],
      "maxConcurrentChildren": 1,
      "permissionPolicy": "auto_allow_once"
    }
  ]
}
```

Rules:

- `id` is a stable, lower-case identifier. It is the only value exposed to a
  model tool call.
- `command` is an argv array, not a shell string. `command[0]` may be an
  absolute path or a bare command resolved through `PATH` at launch; settings
  save/load validates that it is non-empty, and `mothx doctor` checks the
  executable the same way a launch would. Launch uses `exec.Command`, never
  `sh -c`, `cmd /c`, or interpolation.
- `environment` is an allowlist of variable *names*. Values are read at launch
  from MothX's existing secret/environment mechanism and are never persisted in
  this profile, displayed to the model, copied into events, or returned by ACP
  management APIs.
- `roles` and `writeRoots` are deprecated compatibility fields. Older settings
  continue to load, but the values are ignored and are not exposed by the TUI
  or management projections. A task's optional `role` and `write_scope` are
  supplied per delegation; neither can confine an external Agent that runs its
  own tools.
- The child process works in the resolved child work directory. Phase 1 does
  not accept a profile-owned or model-supplied directory override.
- `enabled`, profile limits, and the permission policy are authoritative
  Runtime policy. A disabled or unknown profile is absent from the tool schema
  and fails closed if a stale model call names it. `maxConcurrentChildren` is
  part of that policy: the Runtime worker permit admits concurrent read-only
  workers and isolated `implement` workers up to the profile's configured
  ceiling, while a base-workspace writer remains exclusive. The configured
  value stays schema-validated and forward-compatible, so an existing
  `settings.json` keeps loading; `mothx doctor` and the `/router` panel report
  both the configured and enforced ceilings.
- `permissionPolicy` selects the Runtime strategy for a cooperating ACP
  Agent's permission requests. It is not an assertion that the external Agent
  cannot perform an operation without asking; profile process isolation remains
  the actual containment boundary.
- The profile configuration itself is executable-code trust. Settings UI/CLI
  must show its complete argv, executable path, inherited workspace policy,
  and that the child is not covered by MothX's sandbox. Editing it requires the
  same deliberate local configuration path as other executable integrations.

Profiles are global configuration, not session history. A session stores only
the canonical child events/status already used by current sub-agents. A session
must resolve a profile afresh before every spawn through the same source and
policy resolver that governs its parent Run, so disabling/removing a profile
stops new work without corrupting historic transcripts. A profile is a
capability ceiling, never an adapter default or a way to raise the child's mode
above its parent/session policy.

### 6.2 Tool-call surface

Extend the existing tools; do not add parallel orchestration tools. The first
blocking slice used `delegate_subagent`:

```json
{
  "name": "delegate_subagent",
  "arguments": {
    "task": "Review the proposed change and return concrete risks.",
    "agent": "codex"
  }
}
```

`agent` is an optional profile ID in the tool schema, but Router requires it:

| Combination | Behavior |
| --- | --- |
| no `agent` | Exact existing local child behavior. |
| `agent: <enabled-profile>` on `subagent_spawn` | Router-only asynchronous external child. Returns a handle immediately; read-only children may run concurrently up to the profile ceiling, while writers remain exclusive. |
| `agent: <enabled-profile>` on `delegate_subagent` | Run a blocking ACP-backed child and return the usual compact result after its terminal state. |
| `member` plus `agent` | Reject. An Expert Team member is a Runtime-resolved local member definition in Phase 1; it cannot silently become an arbitrary remote implementation. |
| profile unavailable, disabled, saturated, or forbidden by policy | Reject before process launch. |

`mode`, `work_dir`, `worktree`, `tools`, and `max_iterations` are local-child
parameters. Router rejects them explicitly instead of silently ignoring them:
the external worker's mode, tools, and lifetime belong to its own profile and
ACP implementation, and its working directory is Runtime-resolved. The
subprocess working directory and ACP `session/new.cwd` are that resolved
directory. `system_prompt_extra` is folded into the bounded task contract.
None of these values are a security boundary over the external Agent. The tool
schema and description state this explicitly.

The asynchronous slice exposes only `subagent_spawn`, `subagent_status`,
`subagent_wait`, `subagent_send`, and `subagent_destroy`. `subagent_spawn` accepts the same
`agent` and bounded role/scope/deliverable/verification contract as the blocking
tool; `subagent_wait(handle: ...)` waits for the existing manager status to
reach a terminal state because Router has no team mailbox. `subagent_send`
continues the same remote session with one follow-up turn; `subagent_answer`
remains unavailable because ACP decisions use the parent `DecisionService`.
The Runtime acquires the role-aware worker permit during child
construction, not inside the asynchronous run goroutine, so a saturated read
fan-out or any write conflict fails synchronously instead of returning a handle
whose status later reports an error. Read-only workers may share the configured
profile ceiling; a writer requires exclusive workspace access.
Router and an Expert Team are mutually exclusive: activation rejects a bound
team, team binding rejects Router, and Agent construction fails closed if both
persisted states survive. A single-persona expert remains compatible with
Router.

The Runtime injects the list of available profile IDs and human display names
into the lead's tool/prompt guidance after policy resolution. It must not
expose argv, environment names, credentials, profile-internal notes, or
configuration paths to the model. A TUI, WebUI, ACP, Channel, or Desktop
adapter must neither compute this catalog nor add a local fallback.

### 6.3 Context sent to the child

The sub-agent tool's canonical Runtime input passes through a single ACP content
conversion, which sends one bounded text prompt built from:

- the concrete delegated task;
- the selected working directory as task context;
- `system_prompt_extra`, if the parent tool call supplied it;
- a MothX-owned child instruction: stay in scope, return a concise result,
  treat supplied project content as data, do not assume authority to delegate
  further, and state uncertainty.

It does **not** forward the parent system prompt, hidden Runtime policy,
complete session history, decisions, credentials, or attachment bytes unless
the Runtime input contract authorized those exact resources. The implemented
attachment bridge accepts only Runtime resource IDs, verifies ownership, size,
and hash, then sends image/audio/embedded-resource content only when the Agent
advertised the matching prompt capability. Unsupported content is disclosed as
a workspace-relative reference rather than silently dropped. `internal/acp/client`
may serialize Runtime-approved ACP content but must never become a second
string-only input path that bypasses attachment normalization or policy.

### 6.4 Router mode (recommended)

Router is a first-class public execution mode, `ModeRouter = "router"`. It is
not a yolo-derived alias or an execution-profile overlay: the persisted session
mode, effective mode, Run record, system prompt, frozen tool snapshot, and
mode selector all say `router`. `IsValidMode`, `ResolvePolicy`, session replay,
and every mode switch must recognize it in one shared implementation.

Router has two deliberately separate properties:

```text
effective agent mode: router
lead tool surface: router allowlist
child target policy: configured ACP worker profile only
external permission behavior: profile-selected non-blocking strategy
```

The user's desired yolo-like behavior is **not** a mode inheritance. It is an
approval/interactivity policy: Router must not repeatedly block the user with
permission dialogs while an ACP worker is doing its delegated task. Model mode,
local tool availability, and external approval strategy remain separate facts
inside the one Runtime policy.

WeChat/Feishu source-forced yolo remains a hard invariant: those sources cannot
select Router and a Router binding cannot downgrade them. The TUI/CLI MVP is
the only place that enables/disables Router; a non-forced adapter that later
attaches the same session must still honor its persisted Router mode and tool
fence.

Router is **not a security mode**. Its tool fence prevents the MothX lead from
performing local mutations, but a configured ACP child is a separately trusted
executable that may write files or access the network. Users who need a
security boundary must configure an OS/shared Runtime sandbox policy for the
ACP worker profile; they must not mistake the Router tool surface for one.

#### Lead tool surface

Router must use an allowlist, not a denylist. The Runtime owns the following
initial lead surface:

| Group | Allowed in Router | Notes |
| --- | --- | --- |
| Local inspection | `read`, `ls`, `grep`, `find` | Lets the lead investigate, inspect child changes in the shared worktree, and verify stated file-level facts. |
| Planning/context | `plan`, `skill_ref` | `plan` changes canonical task-plan state, not the worktree. |
| Orchestration | `delegate_subagent`, `subagent_spawn`, `subagent_status`, `subagent_wait`, `subagent_send`, `subagent_destroy` | Every external path is bound to the activated ACP profile. `subagent_send` continues the same remote session; Router never silently falls back to a local in-process child. Local-child parameters (`mode`, `work_dir`, `worktree`, `tools`, `max_iterations`) are rejected, not ignored. |
| Local mutation/process | none | `write`, `edit`, `insert`, `bash`, `jobs`, `kill`, and any future workspace-mutating/process-launching tool are absent from the frozen tool snapshot and rejected if a provider hallucinates them. |
| Other optional tools | off by default | Browser, web search, workflow, publishing, attachment mutation, and provider-hosted side-effect tools require an explicit future Router policy decision. |

The allowlist is applied in two places from one Runtime policy value: tool
definitions sent to the provider and the Agent Core's frozen execution
authorization list. Prompt text alone is never relied upon. A provider call to
`bash`, `edit`, or another excluded tool must fail before approval or tool
execution, exactly like an unadvertised tool does today.

#### ACP delegation policy

Router enables only the configured ACP child target backend. In the MVP the
target is selected by the TUI/CLI session policy; a tool call such as the
following is valid only when `codex` is in the session's bound allowlist:

```json
{
  "task": "Implement the parser fix in pkg/parser, run its focused tests, and return changed files, commands, results, and remaining risks.",
  "agent": "codex"
}
```

The same call without `agent`, which would select a local in-process child, is
rejected in Router rather than silently using the local child path. The MVP
also rejects any `agent` value outside the session's bound allowlist. The
profile resolver checks that the selected profile is Router-eligible and that
its declared worker capability may modify the selected worktree. This makes the
delegation boundary explicit in canonical tool arguments and Run policy, rather
than merely suggesting it in a system prompt.

An ACP worker does not inherit Router's lead tool surface. Its authority is
the selected profile's Runtime policy and the external Agent's own ACP
implementation. This is a deliberate delegation grant, not an accidental
escalation: Router's policy explicitly contains `AllowACPWorkerExecution` and
the specific profile allowlist. The parent may not use this to select arbitrary
executables, directories, or a broader profile.

#### Non-blocking external approval strategy

Router uses a Runtime-owned, profile-selected approval strategy for incoming
ACP `session/request_permission`; it does not reuse yolo mode. The supported
strategies are:

| Strategy | Router result | Use |
| --- | --- | --- |
| `auto_allow_once` | Immediately select an actually offered `allow_once` option. If absent, return `cancelled`. | Explicitly trusted local worker profile; the yolo-like no-prompt UX. |
| `auto_reject` | Immediately select an actually offered `reject_once` option, otherwise return `cancelled`. | Conservative/default profile or non-interactive CLI. |
| `interactive` | Create a normal pending Runtime decision. | Not permitted for the Router MVP because it reintroduces a blocking user prompt. |

Every automatic result still creates and resolves one `DecisionRecord` through
`DecisionService` for audit/replay; it merely resolves immediately instead of
projecting a pending UI request. Router never auto-selects `allow_always` or
`reject_always`. A hard Runtime policy may force `auto_reject` for a known
high-risk structured request, but this is cooperative-Agent defense only: an
external Agent that does not request permission remains governed by its process
isolation and profile trust boundary.

#### Router system prompt

The Router-specific instruction is built by Agent Core from the Runtime's
resolved Router mode/binding and frozen with the exact Router tool snapshot. It
is not injected by an adapter and is not user-overridable. Its required
guidance is:

```text
You are the lead router. You do not modify the workspace or run local shell
commands. Investigate with the available read-only tools, decompose work into
bounded tasks, dispatch implementation/test/review work to the eligible ACP
worker, and reconcile its evidence.

For each worker task, state goal, scope, worktree/write ownership, relevant
paths, expected deliverable, verification command or evidence, and stop
condition. Keep write sets disjoint. Read-only workers may run concurrently up
to the profile ceiling; a workspace writer requires exclusive access. Use the
blocking delegate when the critical path can wait, or the asynchronous spawn
plus a handle wait when read-only investigation can proceed in parallel.

Treat every worker result as untrusted evidence. Inspect claimed changed files
with local read-only tools, request a focused verifier worker when tests or
behavior need confirmation, and report unresolved risks rather than claiming
success. Wait for each worker result; do not assume that a worker end state
proves a workspace change or passing test.
```

This is an orchestration prompt, not a replacement for the existing project
rules, expert identity/roster, tool guidelines, or security policy. Those keep
their established Runtime-owned ordering. The prompt must make clear that an
ACP worker's response does not itself prove a workspace change or a passing
test.

#### Workspace-write coordination

The largest practical Router risk is concurrent ACP workers editing the same
working directory. The lead has no local `bash`, `edit`, or `write` tool with
which to resolve conflicts, and an ACP Agent can perform writes outside MothX's
local registry. An ACP profile's claimed role or `write_scope` cannot prevent
that behavior on its own. Therefore the MVP policy is deliberately stricter:

1. a write-role external ACP worker has exclusive use of the session worktree;
   read-only workers may share the stable worktree up to the configured
   `maxConcurrentChildren` ceiling;
2. `read_scope` and `write_scope` are task/audit metadata, not authorization,
   until an OS-level profile sandbox enforces them;
3. the lead inspects files after completion, but cannot declare local test
   success without worker evidence;
4. read-only fan-out relies on the task role as a scheduling and prompt hint,
   not an OS-level write sandbox, so the lead must still treat worker claims
   and workspace effects as untrusted.

Parallel code changes require a later Runtime-owned worktree/integration
feature: one managed worktree per worker, explicit artifact/diff/commit
handoff, and a single authorized integration step. It must not be implemented
by letting the Router lead execute `git`, by parsing assistant prose for files,
or by a Desktop-local merge workflow.

#### Required Runtime representation

Add `ModeRouter` to the front-end-neutral mode resolver and make the Router
policy an explicit companion of the resolved mode. `ResolvePolicy` resolves the
mode, Router binding, restrictive tool/child-target overlay, and approval
strategy from the same session identity and source facts. Router must be
present in:

- `SessionRuntime` configuration and its canonical option projection;
- agent construction/build options and frozen prompt/tool snapshot;
- the durable Run policy snapshot and event metadata;
- `SynchronizeToolGroups` and the Runtime-owned ACP profile resolver; and
- cross-entry contract tests.

It must not be recomputed in `Registry.ModeTools`, `internal/acp`, Desktop,
TUI, Serve, Channel, or an individual tool. `Registry` receives the already
resolved tool surface; it does not decide whether a session is a Router.

The persisted Router-specific session fact is a Runtime-owned `RouterBinding`,
not a sparse settings object and not an adapter preference. Its minimum fields
are `acpProfileID`, `approvalStrategy`, `policyRevision`, and `updatedAt`; the
mode itself is the canonical persisted `router` mode entry. Add it
through the session domain's normal migration/DAO/event-replay path, load it on
every Runtime attach, and include its resolved immutable values in the
ExecutionIntent/Run policy snapshot. Enabling, disabling, or changing a
binding is allowed only while the session is idle; a disabled/deleted global
profile leaves historic Runs intact but makes the binding unavailable for new
work until an eligible profile is explicitly selected. This gives TUI/CLI the
only initial controls without allowing another adapter to lose the restriction
when it reloads the session.

### 6.5 Router optimization and guardrails

Router deliberately trades a single model's direct execution for delegation
quality, cross-agent coordination, and external-worker capability. Cost and
ACP Agent heterogeneity cannot be removed; the Runtime should make those costs
explicit, bounded, and worth paying.

#### A. Use an explicit task contract, not free-form delegation

Before a Router child starts, Runtime normalizes the tool arguments into a
`SubagentTaskContract`. The lead supplies the values, and Runtime validates
them without inventing task semantics:

```text
objective              required, bounded outcome
role                   optional; investigate | implement | test | review | integrate (default investigate)
read_scope             relevant paths or search targets
write_scope            optional task metadata; non-empty enables MothX's write bridge for write roles
expected_deliverables  findings | changed-files | test-evidence | review
verification           command/evidence the worker must return, not execute locally
stop_conditions        explicit completion and non-goals
target_profile         resolved ACP profile ID
```

This contract improves prompt quality, enables overlap/admission checks, and
gives UI/event projections structured context without parsing assistant prose.
It does not make untrusted worker output true. The full worker prompt is a
bounded rendering of the contract plus the existing worker instruction, not an
independent string-building path in an adapter.

Recommended role discipline:

| Role | Contract intent | Recommended use |
| --- | --- | --- |
| `investigate` | expected not to write; not enforced without a sandbox | Locate code paths, explain behavior, propose bounded implementation plan. |
| `implement` | expected to modify only declared scope; not enforced without a sandbox | Make the change and report exact changed files plus verification evidence. |
| `test` | expected not to modify source; build/cache policy is profile-owned | Run focused checks and report command, exit status, relevant output, and coverage gap. |
| `review` | expected not to write; not enforced without a sandbox | Independently inspect a diff/files and identify correctness, security, and missing-test risks. |
| `integrate` | future exclusive base-worktree write lane | Apply/reconcile approved worker output in a later worktree-based flow. |

#### B. Make write work transactional before making it parallel

The Phase 1 one-write-lane rule is the correct default. Do not attempt
parallel implementation merely by asking agents to touch different files: code
often shares generated files, interfaces, tests, lockfiles, or implicit build
state.

For the later parallel-write phase, use a Runtime-owned change-set protocol:

1. Runtime materializes a managed worktree at a recorded base revision for each
   implementation worker.
2. The ACP worker receives that worktree as its only working directory and a
   declared write scope.
3. After terminal completion, Runtime records an immutable change-set snapshot
   (base revision, changed paths, bounded diff/patch reference, test evidence,
   and worker identity). It never treats an assistant text claim as a patch.
4. A single Runtime-owned integration operation, admitted through the
   base-workspace write lane, receives selected change-set references and
   resolves/validates their merge. It is a tool projection
   (`subagent_integrate`) over the shared Runtime, not a second Agent and not a
   fourth external worker.
5. Runtime records integration evidence and preserves rejected/conflicting
   change sets for review; it never silently copies a mutable child worktree
   into the base directory. A multi-selection is all-or-nothing within one
   call: a later conflict reverses the selections this call applied.

The snapshot/artifact design reuses the Runtime worktree ownership contract and
the durable child-history record. It does not require a separate Agent
implementation: `internal/worktree.ApplyChangeSet` and
`agentruntime.RuntimeChangeSetIntegrator` own verification and application.
Parallel base-workspace writers still require separate conflict-resolution
policy because a worktree path is not itself a deliverable artifact.

#### C. Require evidence and independent verification for completion

Router should use a lightweight completion gate rather than trusting a worker's
final paragraph:

- an `implement` worker returns changed-path evidence and the exact checks it
  attempted;
- the lead uses read-only tools to inspect claimed paths and compare the task
  contract against the observed workspace;
- for behavior, build, or test claims, the lead dispatches a fresh `test` or
  `review` worker when the claim is material, high-risk, or contradictory;
- the final response distinguishes `verified`, `worker-reported`,
  `not-verified`, and `failed` evidence. It must not upgrade a worker-reported
  success to verified merely because the child reached `end_turn`.

Phase 1 presents this gate as a frozen Router instruction and canonical event
metadata. A future stricter product mode may prevent a Router run from being
marked complete after implementation unless the Runtime sees a matching
verification record. That change must be evaluated across all entry points and
must not be implemented as a Desktop-only UI check.

#### D. Bound fan-out and context cost without task wall-clock deadlines

Use a Runtime-owned delegation budget per parent Run/profile, separate from
the Agent iteration budget and never expressed as an arbitrary execution time
limit:

- maximum live workers, maximum total workers, and maximum live write workers;
- maximum task-contract/context bytes sent to each worker and maximum result
  bytes injected back into the lead context;
- per-profile concurrency permit and optional user-configured monetary/token
  ceiling when the profile can report trustworthy usage;
- admission preference for a reusable read-only investigation or verifier
  before launching duplicate implementation tasks;
- cancellation of queued/not-started work when the parent Run is cancelled or
  its objective is superseded.

Worker transcripts remain available as child projections, but the parent sees
only a bounded completion envelope by default. This preserves the context-saving
reason for delegation and prevents a verbose external Agent from consuming the
lead's entire context. No limit above terminates a live long-running worker
solely for elapsed wall-clock time; explicit cancellation and existing Runtime
safety boundaries remain the termination authority.

#### E. Treat ACP profile interoperability as an observed capability contract

Profiles should declare only policies MothX controls, such as Router
eligibility, concurrency, environment names, and sandbox wrapper.
They must not blindly trust a vendor claim that an Agent is "safe" or
read-only. At launch, the Runtime observes standard ACP facts from
`initialize` and records a bounded compatibility result:

| Compatibility level | Requirement | Router behavior |
| --- | --- | --- |
| `v1-basic` | ACP v1 negotiation, `session/new`, `session/prompt`, and prompt response work | Eligible for read-only fan-out; it receives no enforced read-only security claim. |
| `v1-streaming` | Also sends valid ordered `session/update` events | Eligible for normal Router progress/transcript projection. |
| `v1-permission` | Also issues/handles valid `session/request_permission` when it needs approval | Eligible where the profile policy requires interactive approval. |
| `router-worker` | Operator explicitly enables the profile and the observed ACP baseline succeeds | Eligible for any per-delegation role hint; the role does not grant filesystem authority. |

The profile may pin expected executable identity/version or an operator-managed
binary digest for diagnostics. A changed identity/version causes a visible
preflight warning or policy rejection according to configuration; it never
causes MothX to enable vendor-specific extension methods automatically.

`mothx doctor` performs an opt-in non-mutating preflight (`initialize` and
capability validation only) using the same client package. Its result is a
diagnostic observation, not an alternate connection/session owner and not a
guarantee that the Agent will behave consistently on a later task.

#### F. Isolate process privilege and worker output

Each profile should use a Runtime-owned process launch policy that can provide
an OS-level sandbox wrapper, explicit working-directory/root set, minimal
environment, network policy, CPU/memory/process limits, and stdout/stderr
bounds. This is the meaningful hardening point for a worker that might perform
unannounced side effects. It must be shared Runtime execution policy, not a
renderer file permission or an ACP client special case.

All worker completion content enters the parent as an explicitly labeled,
untrusted completion envelope. It cannot change the parent objective, profile,
mode, approval policy, or task contract, and it must not be rendered as a user
instruction. This reduces prompt-injection and control-plane confusion when a
worker reports text such as "ignore the previous policy".

### 6.6 MVP delivery: TUI and CLI first

The first product surface may be TUI and interactive CLI, but its semantics
must still be implemented once in the shared Runtime. "TUI/CLI first" means
only those surfaces initially expose configuration/activation controls; it does
not permit a TUI subprocess runner, a CLI-only settings format, or a separate
Router policy. If another adapter later opens the same persisted session, it
must honor the resolved Router profile and its restrictive tool surface even if
that adapter has no control for creating new Router sessions yet.

The WebUI now exposes activation too (`POST /api/sessions/<id>/router` plus the
`GET /api/acp/profiles` catalog), and Desktop does the same through the additive
`mothx/manage/router/*` projection, both still delegating to the same Runtime
activation path. Channel sessions remain excluded by design: WeChat and Feishu
are source-forced to yolo, so they can never select Router.

The deliberately narrow MVP is:

| Area | MVP behavior | Explicitly deferred |
| --- | --- | --- |
| Configuration | User manually configures one or more global ACP profiles. TUI chooses one eligible profile; CLI supplies one profile ID with Router activation. | Desktop/WebUI/ACP management screens and profile editing UI. |
| Session policy | Runtime persists `mode: router` plus one selected/allowed ACP profile ID and non-blocking approval strategy for the session. | Multi-profile routing, cost-class routing, dynamic worker selection. |
| Lead tools | Read-only inspection/planning plus blocking `delegate_subagent`, `subagent_spawn`/`subagent_status`/`subagent_wait`/`subagent_send`/`subagent_destroy`, and Router-only `subagent_integrate`; every external call targets one of the session's bound profiles. | `subagent_answer` and local-child fallback. |
| Child lifecycle | Read-only external children and isolated `implement` workers may run concurrently up to the profile ceiling and share one initialized ACP process; a base-workspace writer is exclusive. A spawned session survives across `subagent_send` turns and may reconnect through `session/resume` or `session/load`. The caller may block on `delegate_subagent` or poll a `subagent_spawn` handle; an idle session is also woken by pending mailbox completions. | Parallel base-workspace writers. |
| Workspace | An external child honors a Runtime-resolved managed worktree, receives an isolated filesystem/sandbox view, and records a bounded change-set snapshot with its durable history. The lead selects a snapshot by digest through `subagent_integrate`, which applies it under the Runtime's exclusive base-workspace lane and records canonical integration evidence. A write-role child without a worktree still owns the session workspace exclusively. | Parallel base-workspace writers; a second external integration Agent. |
| Decisions | Resolve every remote permission through `DecisionService` using the selected non-blocking strategy; non-interactive CLI never hangs. | Interactive Router approval UI or profile-local approval persistence. |
| ACP support | ACP v1 stdio plus standard WebSocket/streamable-HTTP remote transport, `initialize`, `session/new`, connection reuse with isolated remote sessions, multi-turn `session/prompt`, `session/resume`/`session/load` reconnect, capability-gated additional roots, negotiated image/audio/embedded resources, `fs/read_text_file`, scoped `fs/write_text_file`, opt-in terminal, text/thought/tool progress, standard permission request, cancellation, close, and durable ACP child history. | Terminal/URL auth, elicitation, extensions, local-child history adoption. |

Recommended controls are:

```text
# CLI shape; exact Cobra flag names are subject to existing flag conventions.
mothx --router --acp-agent codex "implement the requested change"
# Bind an ordered allowlist; the first profile remains the default target.
mothx --router --acp-agent codex,claude-code "implement the requested change"

# TUI shape; choose only from Runtime-resolved configured profiles.
/router codex
/router codex,claude-code
```

Internally these controls set the same persisted Runtime policy; they must not
set a TUI/CLI-local boolean. The selected allowlist is the capability ceiling for
the session, and each worker keeps its own profile policy. A
`delegate_subagent` call may choose only a bound `agent` value; an unbound
configured profile is rejected. This keeps heterogeneity and audit/cost
attribution deterministic while retaining the normal tool-call workflow.

Concurrent delegation is deliberately split by workspace safety: multiple
read-only workers and isolated `implement` workers are allowed, while a
base-workspace writer remains exclusive. The lead applies selected immutable
snapshots through `subagent_integrate`, which owns the one base-workspace lane
and records integration evidence; no external integration worker is started.
The caller may block on one delegate or poll multiple spawn handles, inspect
returned evidence/files, then integrate the desired snapshots and produce the
parent answer.

## 7. Architecture

### 7.0 ACP Worker Bridge: the required thin cushion

An ACP child has protocol concerns that do not belong in the lead Agent or in
the generic stdio client: profile-bound process launch, handshake ordering,
one remote session, reverse permission requests, cancellation ordering, frame
validation, bounded output, and translation into canonical child events.
Putting these details directly in `DelegateSubAgentTool` would turn an Agent
Core tool into an ACP session runner; putting them in TUI/CLI would create two
adapter-owned runtimes.

Introduce one thin Runtime-owned mediation boundary, named here
`ACPWorkerBridge`:

```text
lead Agent tool call
        |
        v
SessionRuntime Router delegation policy
  - resolve mode/profile/task contract/permit
        |
        v
ACPWorkerBridge (internal/agentruntime)
  - one child process + ACP v1 session
  - protocol-to-canonical-event conversion
  - DecisionService/cancel/output-bound callbacks
        |
        v
internal/acp/client (transport + typed ACP v1 JSON-RPC only)
        |
        v
configured external ACP Agent
```

The bridge receives an already resolved invocation: parent/child identity,
selected profile, work directory, bounded `SubagentTaskContract`, execution
policy, and Runtime callback handles. It may not re-resolve source/mode,
select profiles, build an `agent.Config`, create a MothX session, access a DAO,
invent a Run, own a decision store, or apply UI policy. Its only outputs are
canonical child events, a bounded untrusted completion envelope, and one
terminal outcome for the existing `AgentManager` lifecycle.

This is intentionally a bridge, not another Agent:

- no provider/model call, LLM loop, system prompt, registry, or tool set;
- no independent mailbox, task queue, session persistence, Run persistence,
  retry policy, or recovery state machine;
- no business decision about whether to delegate, whether a profile is safe,
  or whether worker evidence is sufficient for user-facing success;
- no adapter import or direct TUI/CLI/stdout rendering.

The bridge separates one-time connection/session setup from one prompt turn:

```go
Open(ctx, resolvedInvocation) (*acpRemoteWorker, error)
Prompt(ctx, content, hooks) (result, error)
```

The Runtime-owned connection pool may reuse one initialized process for several
isolated remote sessions. The bridge still owns no Agent loop, mailbox, run
state machine, or background health worker; it resolves remote permission
through Runtime callbacks and exposes only protocol results and canonical
events. Session identity and process lifetime stay under `SessionRuntime`.

The parent-child registration sequence is explicit. `AgentManager` first
validates parent policy and reserves a child ID/parent relation/cancel slot
without invoking the local `AgentFactory`; Runtime binds an ACP child proxy to
that reservation; the bridge supplies only non-terminal protocol events and an
`ACPChildOutcome`; the proxy emits the one canonical `EventRunFinished`; and
the existing delegate path updates the manager status from that same outcome.
The bridge never emits a second terminal event or directly marks a manager
status. This reservation/bind API is required because the current manager's
`Create` path constructs every child through the local factory.

### 7.1 Ownership

| Concern | Sole owner |
| --- | --- |
| Profile schema, validation, and secret lookup | `internal/config` |
| Profile/capability resolution, source/policy intersection, concurrency admission, and child resource lifecycle | `internal/agentruntime` |
| ACP v1 JSON-RPC/stdio client and wire validation | new `internal/acp/client` package |
| ACP Worker Bridge: canonical input conversion, prompt shaping, protocol/event bridge, decision bridge, lifecycle, and cleanup | `internal/agentruntime` |
| Parent-child graph, statuses, mailbox, and child event identity | existing `internal/agent.AgentManager` |
| Core event semantics and parent tool execution | existing `internal/agent` |
| Durable parent Run, decisions, events, and terminal transaction | existing `ExecutionRuntime` / `DecisionService` / session stores |
| TUI/CLI/WebUI/Channel/ACP/Desktop rendering | existing adapters, projection only |

`internal/acp/client` is intentionally protocol-only. It does not import
`agentruntime`, `agent`, `config`, UI packages, session storage, a provider, or
the database. It owns no authoritative session fact after its connection is
closed. This prevents the new client from becoming a second Runtime.
`agentruntime` provides the canonical input, decisions, event conversion,
limits, process policy, and shutdown callbacks.

The current ACP server package may share JSON-RPC envelope and ACP v1 wire
types with the client through a small `internal/acp/protocol` package. That
extraction is allowed only for protocol representation and codec helpers;
server session assembly remains in `internal/acp`, and no client code may call
server handlers or inspect server state.

### 7.2 One child lifecycle

The local and ACP-backed branches differ only in the Runtime-selected child
execution backend. They share the same `AgentManager` registration, status
transitions, parent ID, event forwarding, terminal handling, mailbox delivery,
`subagent_status`, `subagent_wait`, `subagent_destroy`, and adapter
projections.

Introduce a narrow child-execution port in `internal/agent`, implemented and
installed only by the Runtime, rather than importing ACP into Agent Core.
Conceptually:

```go
// Interface location and final names are illustrative.
type ExternalChildResolver interface {
    ResolveACPChild(profileID string, parent AgentID, opts AgentOptions) (agent.Agent, error)
}
```

`AgentManager` remains the graph/status authority. The Runtime creates an
ACP-backed implementation of the existing public `agent.Agent` interface,
registers it through the manager's validated child-registration path, and the
normal sub-agent tool drives `Run`. The proxy exposes the remote child
transcript through `GetMessages`, so existing ACP/Desktop child transcript
views continue to work. The proxy is an event/input adapter, not a second
agent loop: it must not assemble a MothX `agent.Config`, own a local session,
or persist an independent conversation.

The core interface must be deliberately narrow:

- it accepts only a profile ID already resolved by Runtime policy;
- it cannot construct a local `agent.Config`, provider, registry, or session;
- it cannot change parent mode, approval policy, or work directory;
- its cancellation and terminal result flow through the existing child status
  contract;
- child-of-child creation remains prohibited.

Do not implement an ACP-specific status map, a second child mailbox, or a
second event stream in `internal/acp`, Desktop, Serve, or Channel.

### 7.3 ACP child state machine

Each spawned child owns exactly one isolated remote ACP session. The Runtime
may share one initialized process/connection across sessions with the same
profile, work directory, and enforced capability set. Sessions are closed
independently, and an explicit reconnect uses `session/resume` or
`session/load`; the connection is never silently replayed.

```text
new
  -> process_started
  -> initialized
  -> remote_session_created
  -> prompting
  -> terminal(success | incomplete | cancelled | error)
  -> closing
  -> closed
```

Required behavior:

1. Resolve profile, Runtime policy, worktree, and per-profile concurrency
   permit before process start.
2. Launch the configured argv with the Runtime-resolved work directory; capture
   bounded stderr for diagnostics, but never parse it as protocol data.
3. Start one stdout reader and one stdin writer. Enforce UTF-8 NDJSON framing,
   a shared maximum frame size, and serialized writes.
4. Send `initialize` once with `protocolVersion: 1`, truthful `clientInfo`, and
   the Phase 1 client capability set in section 8. Validate the returned major
   version before any session operation.
5. Reject an authentication-required startup deterministically in Phase 1. The
   Agent must be pre-authenticated by its own supported local setup; MothX does
   not advertise terminal authentication or relay secrets.
6. Send `session/new` with an absolute Runtime-resolved `cwd`. Send
   `additionalDirectories` only when the Agent advertised
   `sessionCapabilities.additionalDirectories`, and only with roots the Runtime
   has already normalized and authorized. The same rule applies to
   `session/load` and `session/resume`.
7. Send one `session/prompt` request per turn. Process notifications and reverse
   requests until its corresponding JSON-RPC response arrives; a follow-up turn
   continues the same remote session.
8. Translate its `stopReason` plus local cancellation/error facts to one
   canonical `EventRunFinished`, then legacy terminal compatibility events only
   where the existing child contract requires them.
9. If the Agent advertises `sessionCapabilities.close`, call `session/close`
   after terminalization. Closing must never change an already recorded child
   terminal state. Close pipes and wait for the child process in all cases.

An EOF, malformed frame, non-ACP stdout, request-ID protocol violation, or
process exit before the `session/prompt` response is a child error, not an
implicit successful stop. MothX must not replay `session/prompt` after any
output or tool event: repeating it may repeat remote side effects. A later
explicit turn may reconnect, but the failed prompt itself is never retried.

## 8. ACP v1 Capability and Reverse-request Policy

### 8.1 Initialize request

MothX sends a minimal truthful capability set:

```json
{
  "protocolVersion": 1,
  "clientInfo": {
    "name": "mothx",
    "title": "MothX external sub-agent client",
    "version": "<mothx-version>"
  },
  "clientCapabilities": {
    "fs": {"readTextFile": true, "writeTextFile": false},
    "terminal": false,
    "auth": {"terminal": false}
  }
}
```

Do not include `elicitation`, `session.notices`, `session.compaction`, or
`session.configOptions` until their respective client-side handling and
projection contracts are implemented. Omission means unsupported in ACP v1.
The values above are the base shape: `fs.readTextFile` is `true` only with an
attached Registry, and a Registry-backed connection advertises
`fs.writeTextFile` as part of its capability union. Per-session handlers still
require a write-role task with a non-empty `write_scope`; the profile no longer
has a role ceiling or write-root list. `terminal` is `true` only when the
profile sets `"terminal": true`. No `mothx.dev` extension is required or
assumed between MothX and a third-party Agent.

### 8.2 Incoming Agent requests and notifications

| Incoming message | Current handling |
| --- | --- |
| `session/update` notification | Validate that it belongs to the active remote session; map supported variants to canonical child events in receive order. |
| `session/request_permission` request | Persist/replay through the parent Runtime `DecisionService`, project a canonical approval event with child identity, then return the selected ACP option ID or `cancelled`. |
| `$/cancel_request` notification | Cancel only the matching outstanding reverse request; stale IDs are harmless. The original request receives a valid result or JSON-RPC `-32800` (Request cancelled). |
| `fs/read_text_file` request | Resolve the absolute path through the shared Runtime workspace roots and symlink fence — the same `cwd` plus `additionalDirectories` sent in `session/new` — enforce the text-size/UTF-8 bound, apply ACP `line`/`limit`, and return only the requested content. |
| `fs/write_text_file` request | Available only to a write-role task with a non-empty `write_scope`. Resolve the symlink target through the shared workspace fence, require it inside the task `writeScope`, then write through the shared file lock and atomic-write path. This is an optional MothX-mediated bridge, not containment of the Agent's own tools. |
| `terminal/*` request | Available only when the profile opts in with `"terminal": true`. Run the argv through the shared workspace/sandbox command path with process-group containment, a bounded output buffer, and no interactive stdin. Terminal authentication and URL authentication are not implemented. |
| `elicitation/create`, terminal authentication, unknown standard request | Respond with `-32601` (or `-32602` for invalid parameters) because MothX did not advertise the capability. Never silently emulate it. |
| Unknown notification | Ignore after bounded diagnostic logging, as ACP permits. Do not error the child merely because a newer Agent sent a notification MothX does not understand. |

For an ACP permission request, MothX stores the remote `sessionId`, remote
`toolCallId`, remote request ID, profile ID, remote tool-call title/kind/input,
and the offered option IDs as external/untrusted decision context. The
`DecisionService` record is the only pending-decision owner. It binds to the
parent canonical Run and is replayed/expired using existing semantics.

When the user resolves it, MothX responds with exactly one of:

```json
{"outcome":{"outcome":"selected","optionId":"<offered-option-id>"}}
```

or, on cancellation/expiry/teardown:

```json
{"outcome":{"outcome":"cancelled"}}
```

The client never invents an option ID. `allow_always` and `reject_always` are
owned by the remote Agent according to ACP; MothX does not turn them into a
global MothX allow rule. Router resolves its profile-selected non-blocking
strategy before the request arrives: it may select only an actually offered
one-time option and must be based on structured tool-call fields, never the
Agent-authored title or label alone. It must never auto-select `allow_always`.
The ordinary interactive `ask` behavior remains available outside Router; it is
not silently selected by a Router session.

### 8.3 Event mapping

| ACP update | Canonical child projection |
| --- | --- |
| `agent_message_chunk` | `EventTextDelta`; append to the child proxy's assistant transcript. |
| `agent_thought_chunk` | `EventThinkDelta`; present only through existing thought policy, never as a parent user message. |
| `tool_call` | `EventToolCall` with a namespaced remote tool-call ID and untrusted title/input. |
| `tool_call_update` | Existing tool execution start/update/end events; map only ACP terminal tool statuses, retain remote raw output under existing size/redaction policy. |
| `plan` | `EventPlanUpdate` with normalized entries. |
| `usage_update` | `EventUsage`/context projection only for fields that map truthfully; never synthesize provider token pricing. |
| `available_commands_update`, mode/config/session info | Child status/transcript metadata only; they do not mutate MothX session mode, tools, model, title, settings, or capabilities. |
| `notice`, compaction, unknown future updates | Not advertised in Phase 1; preserve at most bounded untrusted diagnostic metadata and never translate them into a MothX terminal event. |

Remote IDs are opaque and scoped by the MothX child Agent ID. MothX must not
reuse a remote `toolCallId`, `messageId`, or session ID as a canonical MothX
Run/decision/attachment ID.

## 9. Cancellation, Shutdown, and Reliability

Cancellation is an ordered protocol operation, not just process termination:

1. parent Run cancellation, `subagent_destroy`, Runtime shutdown, or
   `context.Context` cancellation marks the child as cancelling;
2. write one ACP `session/cancel` notification if a remote session exists;
3. answer every outstanding `session/request_permission` with ACP's
   `cancelled` outcome;
4. stop accepting new remote updates except those needed to drain the prompt
   response and safely close the connection;
5. wait a short, explicitly scoped shutdown grace for the child to respond and
   exit, then close pipes/terminate and reap the profile-owned process tree if
   it remains alive;
6. emit exactly one canonical cancelled terminal event and release the profile
   concurrency permit.

The grace limits process cleanup only. It is not a task-level wall-clock
timeout. In particular, ACP children used by ESM worker/critic/audit paths must
not receive an arbitrary new execution deadline. Existing shared cancellation,
lease, and ESM policy remains authoritative.

The Runtime process launcher owns the entire external Agent process tree:
Unix uses a dedicated process group and Windows uses a Job Object (or the
platform-equivalent containment primitive). It must reap descendants after the
grace expires, not merely kill the ACP parent while a shell/test/server it
started continues to modify the worktree. This process-tree handle is part of
the resolved profile resource and is never owned by an adapter or by
`internal/acp/client`.

Startup/handshake failures are safe to fail once because no prompt was sent.
After `session/prompt` is written, no automatic reconnect or replay occurs in
Phase 1. A failed process with streamed output produces an error/incomplete
child with its safe partial transcript retained. This follows the Runtime rule
not to blindly repeat uncertain side effects.

`SessionRuntime.Shutdown` owns the final cascade: it cancels active external
children through their registered child cancel functions, waits for their
terminal state, resolves pending decisions as cancelled, and releases all
process/pipe resources. No adapter may call `os.Process.Kill` as a competing
cleanup path.

## 10. Security Model

An ACP profile runs a program chosen by the user. Unlike MothX's own internal
tool process, that program may use its own filesystem/network tools and may
act without sending `session/request_permission`. MothX cannot make an
untrusted executable safe by forwarding ACP permission dialogs.

Controls in this proposal:

- Profile allowlist, argv execution without a shell, validated executable, and
  secret-name allowlist prevent a model from choosing arbitrary local code.
- The Runtime decides profile availability, working directory, worktree, child
  concurrency, task-size limits, and whether the MothX tool itself is usable
  in the parent mode/source.
- MothX advertises `fs/read_text_file` constrained to the same workspace roots
  and symlink fence as local read tools, plus `fs/write_text_file` as a
  Registry-backed capability union. A write request is accepted only for a
  write-role task with a non-empty declared write scope; the profile has no
  role/write-root authority. `terminal` is opt-in per profile; it runs argv
  through the shared sandbox path and never implements terminal or URL
  authentication.
- All remote content (including title, command text, paths, diffs, URLs,
  plans, and permission labels) is untrusted. Renderers escape terminal
  controls, sanitize markup, do not auto-open links/fetch URIs, and use existing
  output-size/redaction limits.
- The task is a least-context text envelope. Secrets and attachments do not
  cross the boundary by accident.
- Every remote permission choice is visible in canonical events/decisions with
  the child/profile identity. Cancellation always responds `cancelled`.
- Logs include profile ID and opaque MothX child ID, never environment values,
  full prompts, authorization headers, or unredacted remote frame dumps.

The settings UI and documentation must make residual risk explicit: choosing
an ACP profile means trusting that executable with the working directory and
with any environment variable deliberately allowlisted for it. A future
hardening project may add OS sandboxing around profile processes, but it must
be a shared Runtime execution policy, not a Desktop or ACP-client exception.

## 11. Implementation Plan

### Phase 0: Contract and configuration (implemented 2026-11-03)

1. [x] Add the profile schema/validation in `internal/config`, including stable ID,
   argv validation, enabled state, limits, environment variable-name allowlist,
   and safe display redaction.
2. [x] Add Runtime-owned profile resolution in `internal/agentruntime`. It combines
   global settings, source/mode policy, session capability, profile enabled
   state, and per-profile concurrency before exposing a profile to Agent Core.
   It must use the same `ResolveSource`/`ResolvePolicy` result as the parent
   Run, with no ACP-specific mode, approval, or source fallback.
3. [x] Add first-class `ModeRouter` to the Runtime mode resolver, session option
   catalog, session replay, Agent construction, and Run records. Its policy has
   the Router lead tool allowlist, ACP-only child-target policy, one-external-
   worker session permit, and a profile-selected non-blocking approval strategy.
   It must render a dedicated Router prompt and frozen tool surface, never the
   ordinary YOLO prompt or tool set.
4. [x] Define the Runtime-owned `SubagentTaskContract` validation boundary.
   `role` is an optional per-delegation hint and `write_scope` is optional task
   metadata; neither is profile authorization. Model arguments still cannot
   select an unconfigured target or escape the Runtime workspace.
5. [x] Implement the TUI/CLI activation projection first: both persist the same
   selected Router profile on the session and neither owns an ACP process,
   decision, or policy fallback. Non-Router adapters still honor that persisted
   restrictive profile on attach. (2026-11-03: `SessionRuntime.ActivateRouter`
   plus `/router <profile>` and `--router --acp-agent <profile>`.)
6. [x] Add the `RouterBinding` session domain entry, canonical replay,
   replay, idle-only mutation fence, and immutable Run policy snapshot before
   exposing the TUI/CLI control.
7. [x] Add focused settings compatibility tests. Sparse settings changes use
   `config.SaveGlobalSettingsPatch()`; no adapter reads/writes `settings.json`
   directly.
8. [x] Update architecture guard tests to ensure only Runtime/config code imports
   the ACP child client and that no adapter grows process launch logic.
   `internal/architecture/acp_client_guard_test.go` now enforces the single
   Runtime owner and the transport-only import ceiling; raw process launch moves
   to the Runtime-owned launcher in Phase 2 item 8.

### Phase 1: Protocol client (implemented 2026-11-03)

1. [x] Extract only reusable ACP v1 envelope/types/NDJSON codec into
   `internal/acp/protocol` if that avoids server/client wire duplication. Keep
   MothX ACP server behavior unchanged. (2026-11-03: typed update/permission
   decoding lives in `internal/acp/client`; the server wire types stay private
   to `internal/acp`, so no shared package was needed.)
2. [x] Add `internal/acp/client` with a testable stdio transport constructor,
   one reader loop, serialized writer, request correlation, reverse-request
   dispatch, cancellation tracking, bounded stderr capture, and no process or
   session ownership beyond its supplied transport.
3. [x] Implement the strict sequence `initialize -> session/new -> session/prompt
   -> optional session/close`, v1 negotiation, stop-reason completion, and
   fail-closed protocol violations.
4. [x] Implement the Phase 1 capability set and the decision callback interface;
   the package itself remains unaware of `DecisionService`.
5. [x] Build a fake ACP Agent integration harness that speaks actual NDJSON on
   stdin/stdout. Do not test this with in-memory direct server method calls
   alone.
6. [x] Add the ACP remote transport RFD's WebSocket and streamable HTTP/SSE
   profiles behind the same transport-only client: cookie affinity, connection
   and session SSE streams, HTTP/2 validation for `https://`, reverse-response
   session headers, batch rejection, frame bounds, and close lifecycle.

### Phase 2: Runtime child adapter (implemented 2026-11-03)

1. [x] Implement the synchronous MVP `ACPWorkerBridge` in
   `internal/agentruntime`, then use it from the ACP-backed `agent.Agent`
   proxy. The bridge owns canonical input conversion, prompt shaping,
   remote-event conversion, transcript snapshots, decision bridge,
   cancellation, and profile permit release. It does not create a second MothX
   session, provider, registry, Run-store row, decision store, queue, or
   background child manager.
2. [x] Add the narrow Runtime-installed external child resolver to
   `internal/agent.AgentManager` as a reserve/bind path; preserve its parent
   validation, policy cap, status state machine, cancellation registry, and
   terminal listeners while avoiding its local-factory-only `Create` path.
   (2026-11-03: `agent.ChildBackend` + `ACPChildBackend`; `Create` keeps every
   existing validation and registration step.)
3. [x] Start the TUI/CLI MVP with `delegate_subagent` and the Runtime-bound ACP
   target, rejecting a missing/mismatched `agent` and keeping local delegation
   unchanged outside Router. (2026-11-03 follow-up: added the role-aware
   async `subagent_spawn`/`status`/`wait`/`destroy` surface; send/answer remain
   absent, and read-only fan-out is admitted up to the profile ceiling.)
4. [x] Register the feature exclusively through
   `agentruntime.SynchronizeToolGroups`/the shared agent-manager construction
   path so every entry point receives the same availability and prompt
   guidance. No adapter may register a special ACP-child tool instance.
5. [x] Project external child events through the existing `ForwardChildAgentEvent`,
   mailbox, status, ACP subagent list/messages, and Desktop panel paths. Do not
   create an `mothx/acp-child/*` UI protocol.
6. [x] Apply Router's frozen lead tool allowlist at both provider advertisement and
   Agent Core execution authorization. In Router, require the selected
   Router-eligible ACP profile and reject local/mismatched child targets before
   launching the one blocking worker.
7. [x] Persist/project the bounded task contract and evidence classification as
   canonical child metadata. Keep the live streamed worker text as a child
   event projection; inject only a bounded, explicitly untrusted completion
   envelope into the lead context.
8. [x] Add the Runtime-owned process-tree launcher/reaper before enabling ACP
   profiles. Cancellation must terminate descendants through the platform
   containment handle after the ACP cancellation grace.

### Phase 3: Decisions, operational UI, and docs

1. [x] Wire remote ACP permission requests into `DecisionService` and each
   existing adapter's canonical decision projection. Test resolve, expiry,
   reconnect/replay, parent cancellation, and shutdown.
2. [x] Add configuration management through the shared configuration service and
   additive ACP management projections only after the backing Runtime service
   exists. Desktop remains an ACP projection and does not read settings files.
   (2026-11-03: `agentruntime.ListACPWorkerProfiles` plus the read-only
   `mothx/manage/acp/profiles/list` projection. 2026-11-03 follow-up: the TUI
   `/router` panel is the in-product editor; it renders that Runtime catalog for
   eligibility and writes through the single `config.SaveACPAgentProfiles`
   validator/owner, so no adapter parses or writes `settings.json` itself. The
   write path stays deliberate because the profile is executable-code trust.)
3. [x] Document profile setup, trust boundary, profile diagnostics, and the exact
   tool-call contract in both English and Chinese user documentation.
4. [x] Provide `mothx doctor` checks for executable availability and invalid
   profile shape without launching an Agent or exposing secret values.

### Phase 3.5: Single-worker async spawn (implemented 2026-11-03)

1. [x] Publish the activated Router target into `subagent_spawn` as well as
   `delegate_subagent`, using the same atomic projection and `const` schema.
2. [x] Accept the bounded task contract on `subagent_spawn` and reject
   `member`, `mode`, `work_dir`, `worktree`, `tools`, and `max_iterations`.
3. [x] Install only `subagent_spawn`, `subagent_status`, `subagent_wait`,
   `subagent_send`, and `subagent_destroy` in Router; do not expose
   `subagent_answer`, because ACP decisions use the parent `DecisionService`.
4. [x] Add handle-aware `subagent_wait` over the existing manager status so
   Router can wait without a team mailbox.
5. [x] Reserve the role-aware external-worker permit during child construction
   so a conflicting spawn fails synchronously; release it on terminal cleanup
   or destroy. (2026-11-03 follow-up: read-only roles and isolated `implement`
   workers fan out to the profile ceiling; a base-workspace writer remains
   exclusive.)

### Phase 4: Follow-ups

Implemented in the first Phase 4 slices:

- capability-negotiated images/resources/attachments with explicit disclosure;
- one initialized ACP process reused across isolated remote sessions,
  including read-only and write-role sessions from the same profile/workdir
  (the connection advertises the capability union; per-session handlers
  enforce the actual task scope);
- read-only concurrent workers and isolated `implement` workers up to the
  configured profile ceiling;
- bounded `fs/read_text_file`, scoped `fs/write_text_file`, and opt-in terminal
  bridges using the shared Runtime workspace roots, symlink fence, file lock,
  atomic-write path, and sandbox command path;
- multi-turn remote sessions with `subagent_send` and capability-gated
  `session/resume`/`session/load` reconnect;
- multi-profile Router activation and target selection from one persisted
  ordered allowlist, with the first profile as the default;
- external-worker completion notifications delivered through the session
  mailbox with the canonical child handle as their identity;
- durable child history (`session_child_runs`) with contract, transcript,
  backend, remote session identity, status, profile, workdir, worktree identity,
  base revision, and bounded change-set snapshot for ACP children (local
  children use the same record shape); and
- managed per-child worktrees with isolated path/sandbox projections and
  deterministic change-set capture; and
- Runtime-owned exclusive change-set integration through `subagent_integrate`,
  with digest/truncation/status/worktree verification and persisted evidence.
- Runtime-owned automatic mailbox wakeups. Completions observed while a
  session is idle are coalesced (25ms debounce, 500ms cap) into one new lead
  Run through `SubmissionCoordinator` and `ExecutionRuntime.BeginDurable`; the
  seed is a system-injected continuation rather than a fake user entry. TUI and
  ACP attach their existing renderers through Runtime callbacks, skip a second
  event observation/terminal write, and share the same cancellation and
  decision paths. No adapter-local queue, Run state machine, or timer exists.
- ACP standard remote transport. Profiles may select `transport: "websocket"`
  or `"http"` with one administrator-configured `url`; stdio remains the
  default and keeps its contained-process launcher. The transport-only client
  owns cookie affinity, HTTP/2 validation for `https://`, connection/session
  SSE routing, reverse-response session headers, bounded frames, and close
  semantics. The Runtime connection pool keys by transport and endpoint and
  still owns profile policy, decisions, events, child lifecycle, and canonical
  terminalization. URL/terminal authentication remains explicitly out of scope.

Still deliberate:

- terminal or URL authentication flows are explicitly not planned;
- parallel base-workspace writers. Change-set capture and the exclusive
  Runtime-owned integration lane are implemented.

## 12. Test Matrix and Acceptance Criteria

### Protocol tests

- `initialize` is first and exactly once; version mismatch rejects before
  session creation.
- Requests use correlated IDs while notifications remain unordered only where
  ACP permits; concurrent reverse requests receive the correct response.
- The happy path receives text, thought, plan, tool call/update, usage, and a
  prompt response; emitted MothX child events retain order and have one
  terminal event.
- Unknown notifications are harmless; malformed/oversized/non-UTF-8/stdout
  protocol frames fail the child safely.
- `session/request_permission` preserves offered option IDs, maps an approved
  choice exactly once, rejects invalid/stale decision resolution, and returns
  `cancelled` during parent cancellation.
- A child that exits before the prompt response is an error; an Agent that
  sends output then fails retains a safe partial transcript; no prompt replay
  occurs.
- `session/cancel`, close-if-advertised, process wait, pipe closure, and forced
  cleanup are idempotent and release profile permits exactly once.

### Runtime and cross-entry tests

- Local child calls remain byte/behavior compatible when `agent` is omitted.
- Runtime policy hides disabled/unavailable profiles and rejects stale IDs;
  parent mode/source and expert-team restrictions cannot be bypassed.
- `member` + `agent` rejects; ACP children cannot create nested MothX children.
- Router runs with effective mode `router`, advertises and authorizes only its
  exact local allowlist, and renders its dedicated Router prompt. A hallucinated
  `bash`, `write`, `edit`, `insert`, `jobs`, or `kill` call is rejected before
  approval/execution on every entry point; no ordinary YOLO prompt/tool set may
  appear in the frozen Agent configuration.
- Router exposes blocking `delegate_subagent` or asynchronous
  `subagent_spawn`/`status`/`wait`/`destroy`, always bound to the session's
  eligible ACP profile allowlist (the first ID is the default). Read-only
  workers may fan out to the configured ceiling; writers are exclusive. It
  exposes no local child fallback or
  `subagent_answer`; `subagent_send` continues the same remote session.
  Source-forced yolo
  sources cannot select Router; non-forced adapters honor a persisted Router
  binding. Its prompt is frozen from Runtime policy and contains the routing
  discipline, not an adapter-injected prompt fragment.
- Router resolves `session/request_permission` immediately through its selected
  `auto_allow_once` or `auto_reject` strategy and records the resolution via
  `DecisionService`; it never emits a pending Router approval dialog, selects
  an `*_always` option, or leaves a non-interactive CLI blocked.
- The Runtime admits read-only ACP workers up to the profile ceiling and keeps
  write-role execution exclusive. Cancellation, process failure, and
  terminalization release each permit exactly once.
- Runtime validates every Router `SubagentTaskContract`, bounds parent-visible
  completion output, and labels worker conclusions as untrusted evidence rather
  than user input. The task role is a prompt/admission hint, never a profile
  ceiling or filesystem sandbox.
- ACP profile preflight accepts only observed standard v1 capabilities; a
  changed external Agent identity/version cannot silently enable an extension,
  broader worker role, or automatic permission policy.
- The read-only filesystem bridge resolves absolute paths through the shared
  workspace roots and symlink fence, honors ACP `line`/`limit`, rejects
  oversized or non-UTF-8 files. The write bridge is advertised in a
  Registry-backed connection's capability union and accepts only a write-role
  task with a non-empty write scope; terminal remains profile-opted-in. Both
  are enforced per session.
- The blocking ACP delegate returns the existing delegate result envelope shape.
  The asynchronous spawn returns the existing manager handle and uses the same
  status/list projection. Its completion enters the existing session mailbox
  with the canonical child handle and may wake an idle session through the one
  Runtime-owned continuation path.
- A burst of mailbox completions coalesces into one canonical lead Run, whose
  seed is system-injected rather than a user transcript entry. Both TUI and ACP
  project the same Run, observe each event exactly once, and do not write a
  second terminal transition; cancellation still reaches the same
  `ExecutionRuntime` and `DecisionService`.
- Decision persistence/replay and cancellation use the same `DecisionService`
  record path as local approvals; no ACP-client-local decision map exists.
- TUI, CLI, WebUI/API, ACP server, Channel, and Desktop receive the same
  Runtime-owned tool availability and canonical child IDs/events; only display
  wire format differs.
- `go test ./internal/architecture` proves no new direct Agent construction,
  adapter-owned process lifecycle, SQL bypass, or settings-file bypass.
- A static/contract guard proves `internal/acp/client` remains transport-only:
  it has no dependency on Runtime persistence, UI adapters, providers, Bun, or
  direct session/database access; `internal/agentruntime` is the sole owner of
  its construction and callback wiring.
- A focused bridge test proves the lead sees canonical events and one terminal
  outcome while ACP handshake/permission/cancel/frame details remain behind
  `ACPWorkerBridge`; the bridge does not acquire a second session, Run,
  decision, queue, or adapter dependency.
- Reservation/bind tests prove an external child cannot take the local factory
  creation path, parent completion/cancellation reaches its ACP cancellation
  callback, and exactly one proxy-owned `EventRunFinished` updates its manager
  state.
- Process-launch tests prove cancellation reaps a child process tree rather
  than only its ACP parent on every supported platform abstraction.

### Completion criteria

The TUI/CLI MVP is ready only when a configured compliant fake Agent and at
least one real ACP Agent can be launched through a Router-bound blocking
`delegate_subagent`, show streamed tool progress, complete/cancel
deterministically, resolve a remote permission through the selected
non-blocking `DecisionService` strategy without leaving a pending UI decision,
and leave no process tree, pending decision, or profile permit behind. A
non-interactive CLI must apply the same strategy instead of hanging. Other
adapters must honor an attached session's
persisted Router fence. Async spawn and the shared child-history record are
implemented; adapter child panels remain projection work. The MVP must also pass the
architecture gate in section 1.1: a review must be able to point to exactly one
Runtime owner for profile policy, input conversion, decisions, event semantics,
process cleanup, and any persistence.

## 13. Alternatives Rejected

| Alternative | Why rejected |
| --- | --- |
| Add a new Desktop-only ACP subprocess client | Violates Desktop's pure ACP projection and fragments lifecycle/decisions. |
| Let the model pass `command`, URL, or shell text to a tool | Gives model output arbitrary local code execution and defeats profile trust boundaries. |
| Treat an ACP Agent as an MCP server | MCP tool calls and ACP sessions have different direction, lifecycle, streaming, and approval semantics. |
| Put the client under a provider adapter | An ACP Agent is a full autonomous agent/session, not a model provider. |
| Parse stderr or process exit as completion | ACP completion is the `session/prompt` response; process output/exit is not a safe terminal semantic. |
| Automatically reconnect/replay a failed prompt | The remote Agent may have already executed side effects. |
| Implement a second external-child status/event/decision store | Breaks the one Runtime and one event/decision model; existing AgentManager and DecisionService already own those facts. |
| Advertise write-filesystem/terminal support without shared bounds | MothX would promise a security-sensitive mutation or command-execution capability without the Runtime workspace fence, task write-scope check, process containment, and profile terminal opt-in now used by the bridge. Profile roles or write roots are not used because they cannot confine an external Agent's own tools. |

## 14. Resolved and Remaining Decisions

Resolved:

1. The settings field is `acpAgents`; the TUI panel and CLI activation use the
   shared `config` validator/owner.
2. ACP-backed profiles are globally configured and Runtime-resolved; no
   adapter-owned per-session capability toggle is needed.
3. Durable child history uses one `session_child_runs` record for local and ACP
   children, owned by the Runtime/session/DAO path.
4. The first real interoperability target is OpenCode's native
   `opencode acp` stdio server. The opt-in
   `TestRealOpenCodeACPInterop` fixture pins that launch contract and runs the
   production `ACPWorkerBridge` against it; the hermetic suite keeps using the
   fake ACP Agent.
5. A Router session may bind an ordered allowlist of eligible workers. The
   first profile is the default target, every bound profile records its own
   non-blocking strategy, and the model may choose only IDs in that allowlist.

Remaining:

1. Parallel base-workspace writers beyond the current exclusive Runtime
   integration lane is now available behind an explicit per-profile opt-in
   (`allowParallelBaseWorkspaceWrites`); per-child worktrees, immutable
   snapshots, and `subagent_integrate` remain the default.
   The gate was deliberate rather than a missing mechanism: an external
   Agent's own tools are not confined to the Runtime filesystem bridge, so
   disjoint `write_scope` values cannot prove that two base-workspace writers
   will not touch the same file. The opt-in is therefore explicit operator
   trust, it keeps the configured `maxConcurrentChildren` ceiling, and the
   Runtime still refuses a parallel writer whose declared `write_scope`
   overlaps a live base writer, because that overlap is statically visible.
   `mothx doctor` and the `/router` panel report the opt-in so the residual
   risk stays visible.
