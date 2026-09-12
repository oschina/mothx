# Expert Teams

Expert Teams let a session use a reusable expert identity or a coordinated
team. An expert bundle defines the lead persona, optional member personas,
and their declared capabilities. The Runtime resolves the bundle once for the
session; TUI, WebUI, Desktop/ACP, and channels only display that shared state.

## Start with an expert

Use `--expert` when starting MothX, or manage the binding from the TUI:

```bash
# Start a new session with the built-in software team
mothx --expert software-company

# Inspect the available expert bundles in an existing TUI session
/expert list
/expert show software-company

# Bind or remove an expert identity
/expert bind software-company
/expert unbind
```

`/expert bind` can bind an unbound session and `/expert unbind` removes the
identity from the current session. Neither operation rewrites previous
conversation history.

## Switch experts by forking

Changing from one non-empty expert to another creates a new session branch:

```text
/expert switch frontend-developer
```

The source session keeps its original expert, history, and identity prompt.
The fork receives the requested expert at the current conversation boundary.
This prevents two expert identities from being mixed into one history.

The WebUI expert panel and Desktop's **Expert** session option follow the
same rule. Initial binding and unbinding update the current idle session;
switching an existing expert creates and opens a fork.

## Team behavior

Team bundles automatically enable the session's multi-agent capability. You
do not need to add `--multi-agent` just to use a team. The lead receives the
team roster and may dispatch declared members by ID:

```text
subagent_spawn(member: "software-engineer", task: "Implement the focused fix and run its tests.")
subagent_wait(timeout_ms: 30000)
```

Members are still normal sub-agents: their tool/mode limits come from the
bundle and the session policy, nested member spawning is unavailable, and
high-risk command protection remains in force. A single-persona expert only
changes the lead identity; it does not force team tools.

Member lifecycle cards are projections of canonical child events. A member
completion updates its own status and is delivered to an active lead at an
agent-loop boundary. It never starts a new lead run by itself.

A member that needs a decision asks the lead instead of the human. The question
is queued for the lead (delivered as a `[MEMBER_QUESTION]` steering message, or
as a `subagent_wait` pending entry with status `question`) and is answered with
`subagent_answer(handle: "<member>", question_id: "…", answer: "…")`; the user
only sees the question projected on the lead's stream. A blocking
`delegate_subagent` child cannot ask questions, because its caller is parked
inside the tool call that would have to answer it.

The mailbox path is available in every session that can spawn members, not only
in team sessions; only a team-bound session also holds its run open for
still-running members at the wrap-up turn.

## ESM interaction

Expert Teams work with Enable Supervisor Mode (ESM) without creating a second
task scheduler. Only a user creates, edits, resumes, or clears an ESM
objective. When a session is truly idle and the objective is still runnable,
the normal ESM continuation path may start the next lead run; member terminal
events are not continuation triggers.

For a team-bound ESM worker, the lead identity and roster are retained,
including member scheduling: the worker continuation drains member
notifications and waits for members before its run may end. ESM critic, audit,
and recovery roles remain isolated: they do not receive member scheduling tools
and never drain or wait on the session's members.

## Add local bundles

Expert bundles are discovered lazily from the built-in catalog, the user
configuration directory's `experts/` folder, and the project folder:

```text
<config-dir>/experts/<bundle-name>/
<project>/.mothx/experts/<bundle-name>/
```

Project bundles override global bundles with the same name, which override
built-in bundles. A bundle contains `expert.json` and one or more persona
files under `agents/`; invalid bundles are shown as unavailable and cannot be
bound. See [the expert-team implementation proposal](../proposal/expert-team-mothx-proposal.md)
for the bundle schema and architecture rationale.

## Create and install a team with the built-in Skill

MothX includes the `expert-creater` Skill. Once enabled, the current Agent
creates and installs a validated project-level team at
`.mothx/experts/<team-id>/`, including `expert.json` and its member personas.
It never overwrites an existing team directory unless you explicitly ask to
update that team.

| Surface | Activation command |
| --- | --- |
| TUI and WebUI chat | `/skill expert-creater` |
| Desktop and ACP | `/expert-creater`; ACP also accepts `/skill expert-creater` and `/skill:expert-creater` |

After activation, describe the team's goal and the lead/member roles in your
next message. Bind the resulting team with the existing picker or
`/expert bind <team-id>`; switching an already-bound team still follows the
normal fork rule.
