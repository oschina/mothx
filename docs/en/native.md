# MothX Native Desktop Client

MothX Native is the **primary desktop client** for MothX. It is a pure-Go
application built with [MyGo](https://github.com/egoist/mygo)'s `ui` package:
every widget is drawn by the app itself — no HTML, no JavaScript, no webview,
no Electron. Like the deprecated Desktop client, it is a **pure ACP (Agent
Client Protocol) client**: it launches a `mothx acp` child over stdio and
speaks ACP v1 (newline-delimited JSON-RPC 2.0) plus the additive `mothx.dev`
extensions. Sessions, runs, settings, providers, skills, knowledge bases, and
every management surface stay Runtime-owned; Native only projects them.

> **Status:** Native replaces the Electron [Desktop client](desktop.md), which
> is deprecated. Release pages publish Native desktop artifacts automatically
> on version tags; Electron builds remain available only as manual
> compatibility builds during the migration window.

## Install

Download the asset for your platform from the release page
(`mothx-native-<version>-<platform>-<arch>`):

- **Linux amd64:** raw binary, `.deb`, `.tar.gz`, or `PKGBUILD`.
- **macOS universal:** `.dmg`.
- **Windows amd64:** portable `.exe` or `-setup.exe` installer.

Native needs a `mothx` runtime binary. It resolves, in order: the launch
argument `--mothx-bin`, the binary persisted in Settings → Runtime,
`MOTHX_BIN`, `PATH`, then the repository's `../bin/mothx` development
fallback. The persisted choice is client-shell launch state only; it is never
written to `settings.json`/`serve.json`. If a selected binary fails to start
or initialize, Native rolls back to the previous choice automatically.

Build from source instead:

```sh
make native-build        # bin-style stripped binary in native/build/
make native-dist         # package for the current platform
make native-dist-linux   # linux/amd64 (deb, tar.gz, PKGBUILD)
make native-dist-darwin  # darwin/universal
make native-dist-windows # windows/amd64
```

## What you get

- **Tasks:** project/recent/ungrouped sidebar with per-session menus, cursor
  pagination, pinning, project grouping, rename, fork, delete, and workdir
  changes; a paginated History view with search and scope filters.
- **Chat:** streaming markdown, reasoning with a live preview while collapsed,
  tool cards with input/diff/output, plan cards and a plan progress side panel
  (including the Runtime-provided plan title/note), inline approval/question
  cards with deadlines (multi-property elicitation forms render one field per
  schema property and answer as content keyed by property name), inline
  sub-agent lifecycle cards with a live running/total header count, structured
  error cards, inline images, artifact preview/save, full-transcript copy and
  export.
- **Composer:** mode, provider/model/thinking picker with capability badges,
  expert binding (replacement forks), sandbox/browser/web-search capabilities,
  attachments (workspace files as links, outside files and clipboard images
  embedded), slash commands, and a workspace menu backed by recent-directory
  history.
- **Pages:** Skills (command cards + SkillHub marketplace), Automation (cron),
  Library (knowledge-base status), each with page-level management dialogs.
- **Settings:** the full Desktop management surface — application defaults,
  providers, MCP, skills, SkillHub, experts, ACP workers, environment,
  channels + deliveries, memory, cron, serve, knowledge bases, stats — plus
  local Appearance (theme, language, home preset/logo/background with an
  app-wide or home-only scope), Runtime (connection, doctor, logs, binary
  choice), and About.
- **Collaboration:** isolated git worktrees, Router worker selection with
  read-only worker evidence, and the read-only sub-agent roster/transcript.
- **Shell:** light/dark/system theme, Chinese/English UI, toasts, background
  run caches per session, and background-approval attention badges.

Like Desktop, a fresh process gives the first draft a private
`~/MothX/mothx_projects_*` quick-start directory; explicitly picked directories
are remembered only as picker history. A **New Task** draft inherits no
directory: the shell asks for one when the task is created and asks again at
send time if it is still unset (cancelling keeps the draft). Until a directory
exists, the provider/model menu is projected from the provider catalog, exactly
as Desktop does before `draft-config-options` can resolve a cwd. Both are
presentation state in
`native-preferences.json` (theme, language, home appearance, runtime binary,
directory history) — never session or configuration authority.

## Migrating from Desktop

Nothing canonical moves: sessions, transcripts, providers, MCP servers,
skills, experts, knowledge bases, schedules, and channel configuration live in
the shared Runtime and appear in Native exactly as they did in Desktop (and in
the TUI/WebUI). Only Electron-local presentation state is left behind:

| Desktop (Electron) | Native |
|---|---|
| `desktop-store.json` theme/language | Settings → Appearance (re-pick once) |
| Home logo/background, opacity/blur/fit/position | Settings → Appearance, plus a new app-wide/home-only scope |
| Bundled vs. custom runtime binary | Settings → Runtime binary choice (with automatic rollback) |
| Last/recent workspace picker history | Remembered automatically in Native preferences |
| DevTools / local CDP debugging | Settings → Runtime diagnostics and ACP stderr log |

Feature-gated panels behave the same way: if a panel is missing, the runtime
did not advertise its feature key — update the runtime binary, not the shell.

## Troubleshooting

- **Linux:** Native draws its own window but still needs a display server
  (X11/Wayland). Headless hosts should use the TUI or `mothx serve` + Web UI.
- **Runtime does not start:** check Settings → Runtime for the resolved
  binary and origin; a failed custom-binary switch rolls back automatically.
- **Approval/question never resolves:** the Runtime decision timeouts
  (`MOTHX_ACP_PERMISSION_TIMEOUT` / `MOTHX_ACP_QUESTION_TIMEOUT`) apply
  unchanged; unrecoverable pending decisions are terminalized by the Runtime.

## Development

```sh
make native-dev    # run from source
make native-test   # transcript reducer + headless UI tests
make native-vet    # go vet + MyGo lifetime/goroutine checks
```

The headless tester renders every page offline; English scans fail on any
untranslated Chinese UI, and pixel probes pin the background layering.