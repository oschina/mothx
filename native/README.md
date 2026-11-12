# MothX Native

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
approval/question cards, a 300-message render window, plan progress side panel,
inline status lines, structured error cards, inline images, publish_artifact
fallback cards, full-transcript copy, and file export. The composer
has mode, provider, model, expert, capabilities, attachment, and command
controls plus a searchable provider/model picker with capability badges and a
bottom StatusBar with context, cost, prompt-cache hit rate, mode, model, and
workspace.

Files inside the session workspace stay `resource_link`s; files outside it (and
images from the composer/clipboard, including direct Ctrl/Cmd+V paste) are
embedded as base64 resources. Attachments show thumbnails, mime type, and size;
generated image artifacts can be fetched by canonical attachment ID and
previewed in-window, while other attachments can be saved. Runs are cached per
session, so switching tasks or starting a new one does not stop a background
session; background approvals surface a toast and a sidebar attention count.
Both light and dark palettes are included. Appearance can follow the system or
pin a theme, and it persists theme, Home preset/logo/background appearance in a
Native-local preferences file. The interface switches between Chinese and
English using the shared Desktop dictionary plus Native-only labels, including
dynamic status and error text; an English-page scan test renders every settings
entry and main page offline and fails on untranslated Chinese UI leaking through.

The Home view ports the Desktop presets (Office/Code/Create), their execution
protocols, quick cards, custom logo, and background image with opacity, blur,
fit, and position. The sidebar, empty state, and app icon use the MothX brand
mark from `docs/assets/mothx.png` (a cleaned copy lives in `assets/mothx.png`).

## Run

MothX Native needs a `mothx` binary. It resolves, in order, the launch
argument `--mothx-bin`, `MOTHX_BIN`, `PATH`, then `../bin/mothx` (the
repository's development binary):

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
| `mothx/requestQuestion` / `elicitation/create` | Question dialog |

`session/update` notifications build the transcript (text, reasoning, tool
calls and output, plans, usage, titles, artifacts), and
`_mothx/session_event` drives run status. The server echoes accepted user
content as `user_message_chunk`; the optimistic bubble is replaced by that
echo instead of duplicated.

## Files

- `main.go` — window and lifecycle.
- `app.go` — ACP connection, session/run operations, decisions.
- `acp.go` — stdio JSON-RPC transport and process management.
- `acp_types.go` — ACP wire payloads.
- `model.go` — transcript reducer, message formatting, tool input/diff projection, and decision deadlines.
- `task_tree.go` — shared project/recent/ungrouped task-tree projection and project catalog refresh.
- `session_menu.go` — compact per-session and per-project action menus.
- `session_state.go` — per-session transcript/run/config cache for background runs.
- `decision_view.go` — inline approval/question cards with deadlines and resolved state.
- `artifact_preview.go` — canonical artifact fetch, image preview, and attachment save.
- `i18n.go` / `i18n_extra.go` — native zh/en locale projection and translation table.
- `composer_config.go` — draft-config selection, provider model projection, menu controls, and post-create flush.
- `transcript_paging.go` — bounded transcript pages and older-message prepend.
- `runtime_model.go` — Runtime connection/diagnostics projection.
- `providers_model.go` — ACP provider wire types, draft validation, and panel actions.
- `providers_view.go` — native provider defaults/catalog/editor and dialogs.
- `skills_model.go` — ACP skills wire types, selected-workspace cwd resolution, and panel actions.
- `skills_view.go` — native skill list with enable/disable toggles, loading, and error states.
- `skillhub_model.go` — ACP SkillHub config/catalogue wire types, token-safe patch builders, session-bound market actions, and dual-sequence invalidation.
- `skillhub_view.go` — native SkillHub browser, detail/add-market/uninstall dialogs, and token-free market editor.
- `mcp_model.go` — ACP MCP wire types, full-replacement request builders, validation, and panel actions.
- `knowledge_model.go` — ACP knowledge-base wire types, defaults, draft normalization, polling, schedule/MCP actions, and async invalidation.
- `knowledge_view.go` — Native knowledge-base cards, directory picker, run/source insights, query preview, and confirmations.
- `mcp_view.go` — native MCP server card list, add/edit dialog, and delete confirmation.
- `serve_model.go` — ACP Serve wire types, grouped editor validation, full patch builder, and async panel actions.
- `serve_view.go` — native grouped Serve configuration editor with load/save states.
- `acp_workers_model.go` — ACP worker profile wire types, draft projections, gated list/patch actions, and async invalidation.
- `acp_workers_view.go` — native ACP worker profile editor for model, driver, MCP bridge, and capability overrides.
- `env_model.go` / `env_view.go` — write-only environment variable drafts and the atomic set/unset patch surface.
- `stats_model.go` / `stats_view.go` — usage summary windows, bucketed trend projection, and the bar chart.
- `history_model.go` / `history_view.go` — paginated task library, project/scope filters, compact session menus, session mutations, and dialogs.
- `worktree_model.go` / `worktree_view.go` — isolated worktree list/create/remove/reset with readiness notifications.
- `collab_model.go` / `collab_view.go` — Router worker selection and the read-only subagent roster/transcript.
- `delivery_model.go` / `delivery_view.go` — delivery-failure projection on the Channels panel.
- `view.go` — the native UI, palette, task tree, transcript, composer, and components.
- `icons.go` — inline SVG icons.

Session and provider state remain Runtime-owned; this app only projects ACP
state and never reads `settings.json` or a database directly.
