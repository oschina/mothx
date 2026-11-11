# MothX Native

A pure-native desktop client for MothX, written in Go with
[MyGo](https://github.com/egoist/mygo)'s `ui` package. It draws every
widget itself — no HTML, no JavaScript, and no webview — and talks to
`mothx acp` over ACP v1 (JSON-RPC 2.0, newline-delimited JSON on stdio).

The interface follows the desktop chat layout in `.mothx/tmp`: a
workspace/session sidebar, a centered transcript, and a rounded composer
with workspace and model selectors. Both light and dark palettes are
included; the first frame follows the system appearance and the sidebar's
“切换外观” row toggles it.

The sidebar, empty state, and app icon use the MothX brand mark from
`docs/assets/mothx.png` (a cleaned copy lives in `assets/mothx.png`). The
empty state uses the Desktop Chinese home slogan, “MothX用中国词元赋能你的任何工作”.

## Run

MothX Native needs a `mothx` binary. It looks at `MOTHX_BIN`, then `PATH`,
then `../bin/mothx` (the repository's development binary):

```sh
cd native
go run .

# or point it at another runtime
MOTHX_BIN=/path/to/mothx go run .
```

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
| `session/load` | Replay a session's transcript |
| `mothx/session/listAll` / `session/list` | Sidebar session library |
| `session/prompt` / `session/cancel` | Runs and stopping them |
| `session/set_config_option` | Model and thinking-level menus |
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
| `mothx/manage/mcp/list` | Global `mcp.json` server projection (when `manageMcp` is advertised) |
| `mothx/manage/mcp/set` | Full replacement of the global MCP server list, including stdio/http/sse, env, and headers |
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
- `model.go` — transcript reducer, session grouping, formatting.
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
- `view.go` — the native UI, palette, and components.
- `icons.go` — inline SVG icons.

Session and provider state remain Runtime-owned; this app only projects ACP
state and never reads `settings.json` or a database directly.
