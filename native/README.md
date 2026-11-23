# MothX Native

<a href="https://awesomemygo.dev/apps/mothx-native"><img src="https://awesomemygo.dev/badge.svg" alt="Listed on Awesome MyGo"></a>

A pure-native desktop client for MothX, written in Go with
[MyGo](https://github.com/egoist/mygo)'s `ui` package. It draws every
widget itself — no HTML, no JavaScript, and no webview — and talks to
`mothx acp` over ACP v1 (JSON-RPC 2.0, newline-delimited JSON on stdio).

The interface follows the Desktop task layout: a project/recent/ungrouped
sidebar with compact per-session `⋯` menus, cursor pagination and per-branch
“load more”; dedicated Skills/Automation/Library pages with Desktop page-level
IA (Skills command cards plus a card-grid skill marketplace, Automation rows,
read-only Library status); and a centered transcript with the same
markdown/tool/diff/decision language as Desktop, including inline
approval/question cards, inline sub-agent lifecycle cards with a live
running/total header count, a 300-message render window, plan progress side
panel with the Runtime-provided plan title/note, inline status lines,
structured error cards, inline images, a compact live preview of the thought
that is still streaming, publish_artifact fallback cards, full-transcript
copy, and file export. The composer
has mode, provider, model, expert, capabilities, attachment, and command
controls plus a searchable provider/model picker with capability badges, a
workspace picker menu backed by the shell's recent-directory history, and a
bottom StatusBar with context, cost, prompt-cache hit rate, mode, model, and
workspace.

Files inside the session workspace stay `resource_link`s; files outside it (and
images from the composer/clipboard, including direct Ctrl/Cmd+V paste) are
embedded as base64 resources. Attachments show thumbnails, mime type, and size;
generated image artifacts can be fetched by canonical attachment ID and
previewed in-window, while other attachments can be saved. Runs are cached per
session, so switching tasks or starting a new one does not stop a background
session; background approvals surface a toast and a sidebar attention count,
and background sub-agent events keep updating their own session's roster.
Both light and dark palettes are included. Appearance can follow the system or
pin a theme, and it persists theme, Home preset/logo/background appearance
(including whether the background paints the whole app or only Home) in a
Native-local preferences file. Like Desktop, a fresh process gives the first
draft a private `~/MothX/mothx_projects_*` quick-start scratch directory, and
explicitly picked directories are remembered as picker history only — never
as a session cwd fallback. A New Task draft inherits no directory: the shell
asks at creation and again at send time if still unset, and until a directory
exists the provider/model menu is projected from the provider catalog. A
user-selected runtime binary that fails to start
or initialize rolls back to the previous choice automatically. The interface
switches between Chinese and
English using the shared Desktop dictionary plus Native-only labels, including
dynamic status and error text; an English-page scan test renders every settings
entry and main page offline and fails on untranslated Chinese UI leaking through.

The Home view ports the Desktop presets (Office/Code/Create), their execution
protocols, quick cards, custom logo, and background image with opacity, blur,
fit, and position. The sidebar, empty state, and app icon use the MothX brand
mark from `docs/assets/mothx.png` (a cleaned copy lives in `internal/icons/assets/mothx.png`).

## Run

MothX Native needs a `mothx` binary. It resolves, in order, the launch
argument `--mothx-bin`, the binary persisted in Settings → Runtime,
`MOTHX_BIN`, `PATH`, then `../bin/mothx` (the repository's development
binary):

```sh
cd native
go run .

# or point it at another runtime at launch time
go run . --mothx-bin /path/to/mothx
MOTHX_BIN=/path/to/mothx go run .
```

The choice is Native shell launch state. It is not an ACP setting and is never
stored in `settings.json`/`serve.json`; a selection made in Settings → Runtime
is persisted in the Native-local preferences file and applied on the next
launch. `--mothx-bin` still outranks it.

```sh
make dev         # or: go run .
make test        # transcript reducer and headless UI tests
make vet         # go vet plus MyGo lifetime/goroutine checks
make dist        # package the app for the current platform
```

The same commands are available from the repository root as
`make native-dev`, `make native-test`, `make native-vet`,
`make native-build`, and `make native-dist*`.

## What it talks to

| ACP method | Use |
|---|---|
| `initialize` | Handshake, capability/feature discovery |
| `mothx/session/draft-config-options` | Model/thinking choices before a session exists |
| `session/new` | Create a session for the selected workspace |
| `session/load` | Replay a bounded initial transcript page when paging is advertised |
| `mothx/session/history` | Load older transcript pages by cursor (when `sessionHistoryPaging` is advertised) |
| `mothx/session/listAll` / `session/list` | Sidebar session library |
| `mothx/session/listAll` scope/project/query/cursor | Paginated task library with project and ungrouped filters |
| `mothx/session/setTitle` / `session/delete` | Rename and delete an idle session |
| `mothx/session/setMeta` | Pinned and project assignment metadata |
| `mothx/session/setWorkDir` | Move an idle session to an authorized directory |
| `session/fork` | Fork a session into a new child task |
| `mothx/projects/*` | Project grouping management |
| `mothx/worktree/*` | Isolated git worktree management (when `worktrees` is advertised) |
| `mothx/manage/router/*` | Session Router worker selection (when `manageRouter` is advertised) |
| `mothx/subagent/*` | Read-only child-agent roster and transcript (when `subagentPanel` is advertised) |
| `mothx/manage/deliveries/*` | Delivery failures and transport-level retry (when `manageDeliveries` is advertised) |
| `mothx/attachment/fetch` | Canonical artifact/attachment fetch for Native image preview (when `attachmentFetch` is advertised) |
| `session/prompt` / `session/cancel` | Runs and stopping them |
| `session/set_config_option` | Draft-session flush and live mode/provider/model/expert/capability changes |
| `mothx/manage/application/get` | Application defaults projection (capability-gated) |
| `mothx/manage/application/patch` | Save application defaults patch |
| `mothx/manage/settings/get` / `patch` | Default provider/model/thinking projection (when `manageSettings` is advertised) |
| `mothx/manage/providers/list` | Provider catalog plus masked key state (when `manageProviders` is advertised) |
| `mothx/manage/providers/save` / `delete` | Provider create/update/rename and reset/remove |
| `mothx/manage/providers/test` / `discover` | Connection probe and model discovery |
| `mothx/manage/skills/list` | Built-in, global, and workspace skill projection for the selected workspace (when `manageSkills` is advertised) |
| `mothx/manage/skills/set` | Enable/disable one skill through the global disabled list, then refresh the projection |
| `mothx/manage/skillhub/get` / `patch` | Token-free SkillHub defaults, markets, and write-only market token updates (when `manageSkillHub` is advertised) |
| `mothx/manage/skillhub/markets` / `targets` / `installed` | Session-bound market catalogue, install targets, and installed-skill projection (when `manageSkillHubCatalog` is advertised) |
| `mothx/manage/skillhub/search` / `detail` | Online skill search and detail projection |
| `mothx/manage/skillhub/install` / `activate` / `uninstall` | Explicit marketplace install, session activation, and confirmed removal |
| `mothx/manage/mcp/list` | Global or active-session project MCP projection (when `manageMcp` is advertised) |
| `mothx/manage/mcp/set` | Full replacement of the selected scope's MCP list, including stdio/http/sse, env, and headers |
| `mothx/manage/memory/get` / `put` | Global memory text projection and save (when `manageMemory` is advertised) |
| `mothx/manage/cron/list` / `create` / `update` / `remove` / `run` | Canonical cron automation rows and mutations (when `manageCron` is advertised) |
| `mothx/manage/channels/get` / `patch` | WeChat/Feishu channel configuration (when `manageChannels` is advertised) |
| `mothx/manage/experts/list` / `get` / `create` / `update` / `delete` | Expert and Expert Team bundle management (when `manageExperts` is advertised) |
| `mothx/manage/knowledge-bases/list` / `get` / `status` | Knowledge-base list, single status, and indexing progress projection (when `manageKnowledgeBases` is advertised) |
| `mothx/manage/knowledge-bases/create` / `update` / `delete` | Directory knowledge-base configuration lifecycle; deleting never removes the source directory |
| `mothx/manage/knowledge-bases/scan` | Starts a background Runtime-owned index scan; the panel polls progress |
| `mothx/manage/knowledge-bases/query` | Bounded debug/preview query against the active snapshot |
| `mothx/manage/knowledge-bases/runs/list` / `runs/get` | Canonical index Run history and diagnostics (when `knowledgeRuns` is advertised) |
| `mothx/manage/knowledge-bases/sources/list` | Active-snapshot file provenance without contents (when `knowledgeSources` is advertised) |
| `mothx/manage/knowledge-bases/clear` | Clears the active index while keeping configuration and the source directory |
| `mothx/manage/knowledge-bases/schedule` | Reads and pauses/resumes the Runtime-owned reindex schedule |
| `mothx/manage/knowledge-bases/mcp/apply` | Quick-adds the standard Knowledge MCP server through ACP |
| `mothx/manage/serve/get` | Safe projection of the global `serve.json` configuration (when `manageServeConfig` is advertised) |
| `mothx/manage/serve/patch` | Grouped Serve settings save through the ACP management projection |
| `mothx/manage/acp/profiles/list` | Safe ACP worker profile catalog (when `manageACPProfiles` is advertised) |
| `mothx/manage/acp/profiles/patch` | Narrow model/driver/MCP-bridge/capability-override profile edit |
| `mothx/manage/env/get` / `patch` | Write-only environment variable names plus atomic set/unset updates (when `manageEnv` is advertised) |
| `mothx/manage/stats/summary` / `timeseries` | Session/run/token summary and bucketed usage trend (when `manageStats` is advertised) |
| `mothx/doctor` | Shared Runtime diagnostics shown in Settings → Runtime (when `doctor` is advertised) |
| `session/request_permission` | Approval dialog |
| `mothx/requestQuestion` / `elicitation/create` | Question dialog; multi-property elicitation forms render one field per schema property |

`session/update` notifications build the transcript (text, reasoning, tool
calls and output, plans with their `mothx.dev` title/note annotation, usage,
titles, artifacts), and
`_mothx/session_event` drives run status and the live sub-agent projection
(`event=subagent` upserts one inline card per child and updates the session
roster; a terminal event re-reads the canonical `mothx/subagent/list`). The
server echoes accepted user
content as `user_message_chunk`; the optimistic bubble is replaced by that
echo instead of duplicated.

## Layout

The root package is only the desktop entry point. All implementation lives in
private `internal/` packages, split by business domain, so a single folder never
holds the whole application.

- `main.go` — thin launcher: injects the release version and calls `desktop.Run`.
- `internal/desktop/` — the private composition shell. It owns the `nativeApp`
  lifecycle, ACP connection/session/run state, the self-drawn page layout,
  transcript/composer core, project/recent task tree, History, worktree, and
  collaboration projections. It composes business panels; it does not own their
  wire contracts or actions.
- `internal/desktop/*_alias.go` / `host.go` — narrow aliases and exported host
  methods that let the shell call private feature packages without leaking the
  old flat-package vocabulary.
- `internal/acp/` — ACP v1 stdio transport plus canonical wire payload types.
- `internal/i18n/` — locale state and the zh/en dictionaries.
- `internal/icons/` — embedded brand asset and the inline SVG icon set.
- `internal/kit/` — shared palette, settings widgets, status pills, and
  formatting helpers used by every panel.
- `internal/feature/` — one private package per Runtime-managed business domain:
  - `acpworkers/` — ACP worker profile editor.
  - `channels/` — WeChat/Feishu configuration and delivery failures.
  - `cron/` — cron job list/editor and run status.
  - `env/` — write-only environment variables and atomic set/unset patches.
  - `experts/` — Expert/Expert Team bundles and editors.
  - `host/` — the shared narrow host interface implemented by the shell.
  - `knowledge/` — knowledge-base configuration, runs, sources, schedules, and
    Knowledge MCP wiring.
  - `mcp/` — MCP server list/editor and full-replacement patches.
  - `memory/` — global memory text projection.
  - `providers/` — provider catalog, defaults, editor, discovery, and tests.
  - `serve/` — grouped Serve configuration editor.
  - `skillhub/` — SkillHub config, catalogue, search, install, and activation.
  - `skills/` — workspace skill list and enable/disable toggles.
  - `stats/` — usage summary and bucketed trend projection.

Business packages may import `acp`, `i18n`, `icons`, `kit`, and other feature
packages, but never the shell. The shell implements the small `Host` interface
each feature declares (`Dispatch`, `FeatureEnabled`, `ACP`, settings state,
current session/page, dialogs, and panel-specific lookups).

Session/provider state remain Runtime-owned; this app only projects ACP state
and never reads `settings.json` or a database directly.
