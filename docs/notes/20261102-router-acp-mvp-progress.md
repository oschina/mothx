# Router ACP MVP Progress (2026-11-02)

## Scope

This note records the first implementation slice of
`docs/proposal/acp-client-external-subagent-proposal.md`:

- a first-class Router mode;
- configuration and persistence facts needed to select one external ACP worker;
- a transport-only ACP v1 stdio client; and
- a Runtime-owned one-turn worker bridge.

This is foundation work. It does **not** yet expose a working Router ACP
delegation tool to end users. In particular, `delegate_subagent` still follows
the existing local-child path; wiring it to the external bridge is the next
implementation slice.

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

This schema is not yet exposed by a TUI/CLI configuration editor. It is a
Runtime configuration primitive; no UI reads `settings.json` directly.

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
The MVP bridge is therefore intentionally not exposed through a lead tool yet;
the wiring must include Runtime-owned task/result bounds and cancellation
semantics rather than just call the client transport.

## Not implemented

The following proposal requirements remain open:

1. Router activation command / UI that atomically sets Router mode and its
   RouterBinding only on an idle session.
2. `AgentManager` reserve/bind path for an external child proxy.
3. Router-specific replacement of `delegate_subagent`, with local child
   fallback rejected.
4. ACP `session/update` -> canonical child `agent.Event` conversion and a
   single `EventRunFinished` owner.
5. `DecisionService` persistence and immediate `auto_allow_once` /
   `auto_reject` responses to external `session/request_permission`.
6. Runtime-owned platform process-tree containment (Unix process group,
   Windows Job Object) replacing direct subprocess ownership.
7. Router task contract, parent-visible output limit, one-worker admission
   permit, and later managed worktree/change-set integration.

## Validation

Executed with loopback/UDP access enabled:

```text
go test ./internal/config ./internal/acp/client ./internal/session \
  ./internal/agentruntime ./internal/tools ./internal/agent ./internal/tui \
  ./internal/acp ./cmd/mothx ./internal/architecture
```

Result: all packages passed.

`git diff --check` also passed at the time of this note.
