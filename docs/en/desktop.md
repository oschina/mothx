# MothX Desktop

MothX Desktop is the graphical MothX client. It is a **pure ACP (Agent Client
Protocol) client**: the Electron main process launches the bundled `mothx acp`
runtime over stdio and speaks newline-delimited JSON-RPC. It does not start
`mothx serve`, does not embed the Svelte Web UI, and does not expose an HTTP or
token tunnel.

## What it is

- **One ACP client.** `desktop/main/acp-client.ts` owns request correlation,
  notifications, reverse approval/question requests, restart/backoff, and
  lifecycle. The renderer only talks to `window.mothx` through the preload IPC
  bridge.
- **A separate frontend.** The renderer is React 19 + shadcn/ui + Tailwind CSS
  under `desktop/renderer/`; it is not the Svelte Web UI in `ui/`.
- **A thin projection.** Sessions, runs, messages, tools, approvals, questions,
  attachments, artifacts, Expert Teams, knowledge bases, worktrees, providers,
  MCP configuration, and schedules are canonical ACP/Runtime state. Desktop only
  renders and manages that state through ACP methods.
- **Local presentation state only.** Theme, language, and new-session directory
  history live in `desktop-store.json`. The runtime binary choice (bundled vs. a
  user-picked path) is client-shell launch state in the Electron main process,
  not a workspace or security boundary.

## Features

- Chat with streaming text, reasoning, tool cards, diffs, plan cards, and
  usage/cache projections.
- Approval and question dialogs backed by the shared Runtime decision model.
- Session list, history paging, fork, resume, rename/metadata, project grouping,
  pinning, and delete.
- Attachments and generated artifacts, including artifact cards and preview.
- Expert Teams: bind an expert or team for a session; switching an existing
  expert forks the session.
- Knowledge bases: create, scan, inspect runs/sources, query, schedule, and
  clear the index.
- Isolated git worktrees shared with the CLI and Runtime.
- Provider/model, mode, thinking, sandbox, browser, and web-search settings.
- Skills and the online Skill Hub, MCP servers, memory, cron automation, and
  usage statistics.
- Sub-agent status and read-only child transcript panels.

Optional controls are gated by the feature keys advertised in
`initialize._meta["mothx.dev"].features`. If a key is absent, the matching panel
or action is unavailable rather than silently emulated.

## Build and run

From the repository root:

```bash
make desktop-vendor    # npm ci + build/vendor the platform runtime
make desktop-build     # bundle main/preload and renderer into desktop/dist
make desktop-dev       # renderer watch + Electron + DevTools/CDP
```

From `desktop/`:

```bash
npm run build          # build main/preload/renderer
npm run typecheck      # TypeScript boundary check
npm test               # main/preload/renderer and protocol tests
npm run e2e            # optional Electron ACP smoke test
npm start              # version:set + ensure:electron + build + launch
```

`make desktop-dev` prepares the runtime first and uses `desktop/.dev-user-data/`
so a development instance does not fight an installed app for the single-instance
lock. Linux requires a working display server; use a real desktop session,
`ssh -X`, or Xvfb. Without `DISPLAY`, Electron exits during Chromium startup.

Development packages:

```bash
make desktop-dist-dev-linux
make desktop-dist-dev-mac
make desktop-dist-dev-win
```

The packaged app vendors a source-built `mothx` runtime. The Electron main
process resolves it in this order: `MOTHX_BINARY` (development override) → the
custom binary selected in Desktop settings → the bundled vendor directory → the
repository `bin/` directory. A missing custom binary falls back to the bundled
one.

## Runtime and protocol

Desktop uses the same ACP v1 surface documented in [ACP Protocol](acp.md), plus
`mothx.dev` extensions. The main process starts one shared ACP child process and
routes multiple sessions through it. It never reads `settings.json`, the
sessions database, or Serve configuration directly; management operations use
`mothx/manage/*` and are capability-gated.

Typical method groups:

- Lifecycle: `initialize`, `mothx/doctor`
- Sessions: `session/new`, `session/load`, `session/resume`, `session/fork`,
  `session/list`, `session/close`, `session/delete`
- Runs: `session/prompt`, `session/cancel`, `$/cancel_request`
- Configuration: `session/set_config_option`, `session/set_mode`
- Notifications: `session/update`, `_mothx/session_event`
- Reverse requests: `session/request_permission`, `mothx/requestQuestion`
- Attachments/artifacts: `mothx/attachment/list|fetch`
- Management: `mothx/manage/*`

## Troubleshooting

- **Blank window or crash on Linux:** confirm `DISPLAY` points at a live X
  server, or run under Xvfb.
- **Custom runtime does not start:** verify the path is executable and reports a
  compatible `mothx --version`; Desktop falls back to the bundled runtime.
- **Optional panel missing:** the runtime did not advertise its feature key.
  Update the bundled runtime or the Desktop release together.
- **Approval or question never resolves:** check the ACP decision timeout
  (`--permission-timeout`, `--question-timeout`, or the
  `MOTHX_ACP_PERMISSION_TIMEOUT` / `MOTHX_ACP_QUESTION_TIMEOUT` environment
  variables). The Runtime terminalizes unrecoverable pending decisions.
