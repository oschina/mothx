# Tool System Reference Guide

MothX provides a powerful and extensible set of built-in tools for file operations, codebase exploration, shell execution, multi-agent coordination, and workspace interaction.

---

## 1. Tool Execution Safety and Sandboxing

Before exploring individual tools, it is crucial to understand the three run modes under which MothX can execute tools:

| Run Mode | Tool Modification Privileges | Network Access | Typical Use |
|----------|------------------------------|----------------|-------------|
| **plan** | Read-only; writes and modifications are denied | Usually disabled | Analysis and planning |
| **agent** | Workspace reads/writes; risky operations require approval | Controlled by sandbox and tool configuration | Daily development |
| **yolo** | Relaxed approvals and full tool operations; blacklist still applies | Controlled by sandbox configuration | Explicitly authorized automation (default) |
| **os** | Bash only with YOLO-level approval behavior; high-risk protections still apply | Disabled | Shell-only automation |

### Sandbox Mechanics (`bwrap`)
When `sandbox.enabled` is `true` in `settings.json`, MothX isolates commands run via `bash` (or other external processes):
* **Allowed Paths**: The sandbox mounts system folders (`/usr`, `/lib`, `/bin`, etc.) as read-only, and mounts the active workspace directory as read-write (`standard`) or read-only (`strict`).
* **Denied Paths**: Sensitive directories (like `~/.ssh`, `/etc/shadow`, etc.) are completely hidden or blocked.
* **Network Isolation**: Direct networking is blocked by creating a separate network namespace (`--unshare-net`).


## 1a. Parallel Tool Execution

When an agent turn returns multiple local function or custom tool calls, MothX can execute them concurrently using a bounded worker pool. This is controlled by the `toolExecution` setting in `settings.json`:

| Setting | Type | Default | Description |
|---------|------|---------|-------------|
| `mode` | string | `"parallel"` | `"parallel"` uses bounded local workers; `"sequential"` executes returned calls one at a time locally (provider-side parallel output is unaffected) |
| `maxConcurrency` | int | `10` | Maximum concurrent tool calls per agent turn (set to `1` for serial execution) |

Tool completion events may stream out of order, while provider continuation messages are restored in the original call order. The setting is shared by TUI, WebUI, Serve, channels, ACP, and sub-agents built through the common runtime. `sequential` only changes local execution after a response arrives; a provider may still return multiple tool calls in one response. It does not control concurrency inside provider-hosted tools (e.g. OpenAI Responses `web_search`); those calls remain under the provider's own orchestration.


---

## 2. Comprehensive Tool Directory

| Tool | Category | Action | Execution Security | Availability |
|------|----------|--------|-------------------|--------------|
| [`read`](#read---file-reading) | File System | Read file content (supports images and pagination) | Read-only access allowed | All modes |
| [`write`](#write---file-writing) | File System | Create or overwrite files | Requires Standard/Yolo | CLI, ACP, Serve, Channels |
| [`edit`](#edit---precise-file-modification) | File System | Atomic text replacements | Requires Standard/Yolo | CLI, ACP, Serve, Channels |
| [`ls`](#ls---directory-listing) | Exploration | List directory contents | Read-only access allowed | All modes |
| [`find`](#find---file-search) | Exploration | Search for files by pattern | Read-only access allowed | All modes |
| [`grep`](#grep---text-content-search) | Exploration | Regex search within files | Read-only access allowed | All modes |
| [`bash`](#bash---command-execution) | Execution | Run shell commands (supports sync & async) | Subject to Sandbox Level | CLI, ACP, Serve, Channels |
| [`jobs`](#jobs---background-job-management) | Execution | List or check status of background jobs | Read-only access allowed | All modes |
| [`kill`](#kill---stop-background-job) | Execution | Stop a running background job | Requires Standard/Yolo | CLI, ACP, Serve, Channels |
| [`plan`](#plan---task-planning) | Session | Update a visible progress/task plan | Read-only access allowed | All modes |
| [`question`](#question---user-clarification) | Session | Prompt user for multiple-choice input | Plan & Agent mode | TUI & ACP |
| [`memory`](#memory---persistent-memory) | Workflow | Read/write to persistent `memory.md` | Session-scoped read/write | Serve Mode |
| [`cron`](#cron---scheduled-background-tasks) | Workflow | Schedule background tasks via sub-agents | Session-scoped scheduling | Channels & Multi-Agent |
| [`subagent_spawn`](#subagent_---delegated-work) | Multi-Agent | Spawn isolated sub-agents | Sub-agent scoped limits | Multi-Agent Mode |
| [`subagent_status`](#subagent_---delegated-work) | Multi-Agent | Query sub-agent status | Read-only | Multi-Agent Mode |
| [`subagent_send`](#subagent_---delegated-work) | Multi-Agent | Send commands to sub-agents | Send message | Multi-Agent Mode |
| [`subagent_wait`](#subagent_---delegated-work) | Multi-Agent | Wait for member activity when the critical path is blocked | Bounded wait (2.5s-120s) | Multi-Agent Mode |
| [`subagent_answer`](#subagent_---delegated-work) | Multi-Agent | Answer a member's blocking question so it can continue | Question scoped | Multi-Agent Mode |
| [`subagent_destroy`](#subagent_---delegated-work) | Multi-Agent | Remove sub-agents & clean up | Destroy | Multi-Agent Mode |
| [`delegate_subagent`](#delegate_subagent---blocking-single-sub-agent-delegation) | Delegate | Run one synchronous sub-agent task | Sub-agent scoped limits | Delegate Mode |
| [`workflow_run`](#workflow_run---dynamic-javascript-workflows) | Workflow | Run a JavaScript workflow with worker agents | Worker-agent scoped limits | Workflow Mode |
| [`workflow_status`](#workflow_status---workflow-run-status) | Workflow | Inspect workflow runs and results | Read-only | Workflow Mode |
| [`workflow_cancel`](#workflow_cancel---workflow-cancellation) | Workflow | Cancel an active workflow run | In-process active run only | Workflow Mode |
| [`a2a_dispatch`](#a2a_dispatch---remote-agent-dispatch) | Multi-Agent | Dispatch tasks to a remote A2A agent | Network request | A2A Master Mode |
| [`skill_ref`](#skill_ref---skill-reference-loading) | Skills | Load external skill documentation | Read-only | All modes |

---

## 3. Tool Details & Parameters

### read - File Reading

Read file contents with support for pagination (vital for handling large source files without blowing up the context window) and image rendering.

#### Parameters:

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `path` | string | ✓ | Path to the file. |
| `offset` | integer | - | Starting line number (1-indexed). |
| `limit` | integer | - | Maximum number of lines to read. |

#### Image File Support:
If the file path ends with a supported image extension (`.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`), the tool automatically encodes the image content in Base64 and returns a rich content block with its MIME type, which enables multimodal models to "see" the image.

#### Example Payload:
```json
{
  "path": "src/main.go",
  "offset": 120,
  "limit": 50
}
```

---

### write - File Writing

Create a new file or completely overwrite an existing file. This operation is atomic (uses a temporary file first, then renames it) to prevent corruption.

#### Parameters:

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `path` | string | ✓ | Path to write. |
| `content` | string | ✓ | Full text content of the file. |

#### Example Payload:
```json
{
  "path": "config.json",
  "content": "{\n  \"port\": 8080,\n  \"debug\": true\n}"
}
```

---

### edit - Precise File Modification

The `edit` tool performs highly precise, **atomic multi-replacement operations** on a single file. Understanding how this tool operates under the hood is critical:

#### Edit Lifecycle & Safety Rules:
1. **Pre-Flight Validation**: Before any modification is made, MothX reads the file and searches for each `oldText` block in `edits[]`.
2. **Uniqueness Check**: Every `oldText` pattern **must match exactly once** in the target file. If a pattern matches 0 times or matches multiple times, the **entire** batch of edits fails immediately with an error, and the file is untouched.
3. **Overlap Resolution**: MothX sorts the matches by their start index and checks if any edit overlaps with another. If an overlap is detected, the operation aborts to prevent corrupt edits.
4. **Atomic Application**: Edits are applied in sorted order based on their indices in the original file. No partial edits are written. The file is modified atomically via a temporary file write.

#### Parameters:

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `path` | string | ✓ | File to edit. |
| `edits` | array of objects | ✓ | List of replacement blocks. |

**Edit Object Structure:**

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `oldText` | string | ✓ | Exact block of text to replace. |
| `newText` | string | ✓ | The replacement text. |

#### Example Payload:
```json
{
  "path": "server.go",
  "edits": [
    {
      "oldText": "func Start() {\n\tlog.Println(\"Starting server...\")",
      "newText": "func Start() {\n\tlog.Println(\"Booting microservice...\")"
    }
  ]
}
```

---

### bash - Command Execution

Executes a command using the shell (e.g., `/bin/bash` on Linux/macOS, BusyBox on Windows, or PowerShell as fallback). It supports both synchronous (blocking) and asynchronous (background) execution modes.

#### Parameters:

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `command` | string | ✓ | - | Shell command string to execute. |
| `timeout` | integer | - | 45 | Maximum execution time in seconds (max 600). Use `async=true` for long-running processes; `timeout=0` means no tool-level deadline. |
| `async` | boolean | - | false | If `true` (or if command ends with `&`), the process starts in the background and returns a `jobId` immediately. |

#### Example Payload (Synchronous):
```json
{
  "command": "go test -v ./internal/provider/...",
  "timeout": 60
}
```

#### Example Payload (Asynchronous):
```json
{
  "command": "npm run dev",
  "async": true
}
```

---

### jobs - Background Job Management

List, query, or clean up asynchronous shell processes spawned with `bash` (`async=true`).

#### Parameters:

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `jobId` | integer | - | Query detailed logs, start time, CPU elapsed time, and status for a specific job. |
| `cleanup` | boolean | - | Remove finished jobs from the manager list. |

#### Example Payload (List All):
```json
{}
```

#### Example Payload (Query Specific Job):
```json
{
  "jobId": 1
}
```

---

### kill - Stop Background Job

Forcefully stop (send SIGTERM / SIGKILL) an asynchronous background job started via the `bash` tool.

#### Parameters:

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `jobId` | integer | ✓ | ID of the job to terminate. |

#### Example Payload:
```json
{
  "jobId": 1
}
```

---

### ls - Directory Listing

List files and directories inside a target path, giving exact file sizes, modification statuses, and types.

#### Parameters:

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `path` | string | - | `.` | Directory to list. |

#### Example Payload:
```json
{
  "path": "./internal"
}
```

---

### find - File Search

Find file paths matching glob patterns. This tool runs the pure-Go `go-fd` engine in-process and respects ignore files.

#### Parameters:

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `pattern` | string | ✓ | - | Glob pattern to search (e.g. `*.go`, `*test*`). |
| `path` | string | - | `.` | Root directory for search. |
| `maxDepth` | integer | - | - | Maximum search depth. |
| `maxResults`| integer | - | 100 | Limit on number of results. |

#### Example Payload:
```json
{
  "pattern": "*_test.go",
  "path": "internal/provider",
  "maxDepth": 3
}
```

---

### grep - Text Content Search

Perform fast regex-based searches across codebase files. This tool runs the pure-Go `go-ripgrep` engine in-process and respects ignore files. If the pattern is an invalid regex, the search automatically falls back to a literal match and reports that fallback in the result.

#### Parameters:

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `pattern` | string | ✓ | - | Regular expression pattern. Invalid regex patterns automatically fall back to literal search. |
| `path` | string | - | `.` | Directory or file path to search. |
| `include` | string | - | - | Glob pattern for files to include (e.g., `*.go`). |
| `maxResults`| integer | - | 100 | Limit on returned matches. |

#### Example Payload:
```json
{
  "pattern": "type ProviderConfig struct",
  "include": "*.go"
}
```

---

### plan - Task Planning

Renders a multi-step checklist panel in the TUI and persists the current plan state. The TUI also shows active non-done steps in a sticky todo list so users can keep track of progress while the transcript continues streaming.

#### Parameters:

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `steps` | array of objects | ✓ | Ordered steps of the task. |
| `title` | string | - | Short descriptive title of the plan. |
| `note` | string | - | Current risk warning, blocker, or status note. |

**Step Object Structure:**

| Property | Type | Required | Allowed Values | Description |
|----------|------|----------|----------------|-------------|
| `title` | string | ✓ | - | Concise description of the step. |
| `status` | string | ✓ | `pending`, `running`, `done`, `failed` | Execution state. |

#### Example Payload:
```json
{
  "title": "Database Migration",
  "steps": [
    { "title": "Check current schema", "status": "done" },
    { "title": "Run migration script", "status": "running" },
    { "title": "Verify data consistency", "status": "pending" }
  ]
}
```

---

### question - User Clarification

The assistant can halt execution and ask the user a multiple-choice question to clarify requirements. Available in `plan` and `agent` modes inside the TUI and ACP sessions. The interactive TUI lets the user navigate options and select the correct path.

#### Parameters:

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `question` | string | ✓ | The question being asked. |
| `options` | array of strings | ✓ | List of choices for the user. |

#### Example Payload:
```json
{
  "question": "Which styling library would you prefer for the frontend components?",
  "options": [
    "Tailwind CSS",
    "Styled Components",
    "Vanilla CSS / CSS Modules"
  ]
}
```

---

### memory - Persistent Memory

In Channels messaging mode, the assistant can record user preferences, decisions, and system rules in `.mothx/memory.md`. This memory is loaded at the beginning of each session.

#### Parameters:

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `action` | string | ✓ | Action to take: `read`, `add`, `update`, `delete`. |
| `section` | string | - | Heading section (e.g. `Lessons Learned`, `Project Rules`). |
| `content` | string | - | Text block to write or match. |
| `old` | string | - | Target text to replace (used in `update`). |
| `new` | string | - | New replacement text (used in `update`). |

#### Example Payload (Add Memory):
```json
{
  "action": "add",
  "section": "Project Rules",
  "content": "All API endpoints must be documented in OpenAPI 3.0 format."
}
```

---

### cron - Scheduled Background Tasks

Enables creating and managing cron-like scheduled tasks that execute in the background via sub-agents. 

#### Parameters:

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `action` | string | ✓ | - | Action: `list`, `create`, `enable`, `disable`, `remove`, `run`. |
| `id` | string | - | - | Job ID (required for edit/remove actions). |
| `name` | string | - | - | Short mnemonic name. |
| `prompt` | string | - | - | The prompt instruction sent to the sub-agent. |
| `schedule` | string | - | - | Cron notation or interval (e.g. `@daily`, `@every 15m`). |
| `oneshot` | boolean | - | false | Run only once and then disable itself. |
| `mode` | string | - | `yolo` | Sub-agent execution mode (`agent` or `yolo`). |

#### Example Payload:
```json
{
  "action": "create",
  "name": "daily-git-pull",
  "prompt": "Pull the latest upstream branch and verify compilation.",
  "schedule": "@daily",
  "mode": "yolo"
}
```

---

### subagent_* - Delegated Work

When MothX is launched in multi-agent mode (`--multi-agent`), the main agent can spin up completely separate sub-agents to parallelize work. Sub-agents run in their own session contexts, have separate memories, separate temporary files, and isolated logs.

Member questions and completions are delivered to an active lead through the session mailbox (as steering messages) in every session that can spawn members; only a session bound to an expert **team** also holds its run open for still-running members at the wrap-up turn. Other sessions end the turn normally and see the notification at their next iteration or next run.

#### `subagent_spawn`
Launches a sub-agent to handle a focused prompt task asynchronously.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `task` | string | ✓ | Prompt describing the precise task to fulfill. |
| `mode` | string | - | Run mode (`plan`, `agent`, or `yolo`; defaults to the parent mode, then `yolo`). |
| `work_dir` | string | - | Specific subdirectory to run the sub-agent in. |
| `tools` | array of strings | - | Whitelist of allowed tool names (empty for all). |
| `max_iterations` | integer | - | Maximum agent loop cycles. |

#### `subagent_status`
Queries progress and returns the result once complete.
```json
{ "handle": "subagent-job-1" }
```

#### `subagent_send`
Appends additional guidance or questions to an active sub-agent.
```json
{ "handle": "subagent-job-1", "message": "Also fix imports in helper_test.go." }
```

#### `subagent_wait`
Blocks until a spawned member or sub-agent completion lands in the session mailbox, or the bounded timeout elapses. It returns only a pending summary (member id, status, and a `question_id` for members waiting on an answer); completion content is delivered automatically at the next iteration boundary.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `timeout_ms` | integer | - | Maximum wait in milliseconds, clamped to 2500-120000 (default 30000). |

#### `subagent_answer`
Answers a blocking question a running member asked the lead instead of the human. The question arrives as a `[MEMBER_QUESTION]` steering message or as a `subagent_wait` pending entry with status `question`; the member stays blocked until this tool resolves it.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `handle` | string | ✓ | Member handle that asked the question. |
| `question_id` | string | ✓ | Question ID from the `[MEMBER_QUESTION]` message or the wait summary. |
| `answer` | string | ✓ | Answer the member continues with. |

A question that is no longer pending (already answered, expired, or belonging to another member) is reported as an error instead of a silent success.

#### `subagent_destroy`
Cleans up logs and releases the sub-agent container/process context.
```json
{ "handle": "subagent-job-1" }
```

---

### delegate_subagent - Blocking Single Sub-Agent Delegation

When MothX is launched with delegate mode (`--delegate`) or enabled at runtime with `/delegate on`, the main agent gets a single blocking `delegate_subagent` tool. It is useful when a bounded subtask requires several exploration steps, but the parent only needs the final summarized result.

Unlike `subagent_*` multi-agent tools, `delegate_subagent` runs synchronously: the parent waits until the child agent completes, then receives a JSON result. Only one delegated sub-agent can run at a time.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `task` | string | ✓ | - | Specific bounded task with goal, relevant paths/context, expected output, and stop conditions. |
| `mode` | string | - | Parent mode, then `yolo` | Sub-agent execution mode: `plan`, `agent`, or `yolo`. |
| `work_dir` | string | - | Current working directory | Working directory for the sub-agent. |
| `tools` | array of strings | - | All tools except nested sub-agent/delegate | Optional tool allowlist, e.g. `["read", "grep", "find"]`. |
| `max_iterations` | integer | - | `50` | Maximum tool-call iterations. |
| `system_prompt_extra` | string | - | - | Extra instructions or constraints for the worker. |

Example payload:
```json
{
  "task": "Find all Go files in internal/serve/openaiapi/ that import net/http but do not call http.Error. Return file paths with line numbers.",
  "mode": "plan",
  "tools": ["grep", "read", "find"],
  "max_iterations": 20
}
```

Result shape:
```json
{
  "status": "done",
  "result": "...summarized worker response...",
  "duration": "1.234s",
  "tool_calls": 7,
  "tool_breakdown": {"grep": 4, "read": 3}
}
```

Use delegate mode for broad searches, multi-step investigations, focused implementation, or verification tasks. Avoid it for one-tool operations, tasks needing user clarification, or highly stateful work that depends on the full conversation.

---

### workflow_run - Dynamic JavaScript Workflows

When MothX is launched with workflow mode (`--workflows`), the main agent can run a plain JavaScript workflow script that coordinates multiple worker agents across phases. Workflow mode is independent from `--multi-agent`: enabling workflows exposes `workflow_*` tools, not `subagent_*` tools.

Workflow scripts must use the supported JavaScript DSL. Do not describe workflow structure with a JSON DSL.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `source` | string | ✓ | JavaScript workflow source. The top-level form should be `workflow("name", body)`. |
| `timeoutSeconds` | integer | - | Tool-level timeout for the whole workflow run. Omit to use the default tool timeout, set a positive number for bounded long workflows, or set `0` only for intentional continuous workflows with no agent-level deadline. |

Example payload:
```json
{
  "source": "workflow(\"auth audit\", {concurrency: 2, phases: [phase(\"scan\", parallel(agent(\"api\", {mode: \"plan\", tools: [\"read\", \"grep\"], maxIterations: 100, prompt: \"Audit internal/serve/openaiapi auth risks\"}), agent(\"channels\", {mode: \"plan\", tools: [\"read\", \"grep\"], maxIterations: 100, prompt: \"Audit internal/serve/channels auth risks\"}))), phase(\"verify\", agent(\"cross-check\", {mode: \"plan\", tools: [\"read\", \"grep\"], maxIterations: 80, prompt: results(\"scan\") + \"\\nReconcile the findings and list concrete risks.\"}))]});",
  "timeoutSeconds": 900
}
```

Supported workflow builtins include `workflow`, `phase`, `parallel`, `series`, `agent`, `concurrency`, `result`, `resultKey`, `resultLatest`, `results`, and `log`. Worker agents receive dynamic workflow context through their task prompt, so the parent system prompt and tool definitions remain frozen after agent construction.

Important defaults: `concurrency` defaults to 5, `mode` inherits the parent agent mode, omitted `tools` uses the default tool set for the worker mode, and omitted/zero/negative `maxIterations` defaults to 50 worker-agent loop iterations. Use `key` for repeated logical agents inside loops; keyed results are stored as `phase.agent[key]` and can be read with `resultKey` or `resultLatest`. Worker agents cannot spawn subagents, delegate, or start nested workflows, and the DSL has no per-worker `:timeout`, `model`, `:thinking-level`, or `:max-tokens` options.

For detailed usage and best practices of workflow mode, see the [Workflow Mode](workflow.md) documentation.

### workflow_status - Workflow Run Status

Lists recent workflow runs, or returns the full persisted state for one run.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | - | Workflow run ID. Omit to list recent runs. |

### workflow_cancel - Workflow Cancellation

Cancels an active workflow run in the current MothX process. Completed runs, runs from another process, and runs from before a restart are not active and cannot be canceled by ID.

---

### a2a_dispatch - Remote Agent Dispatch

Only available in A2A Master Mode. Dispatches a task request over network to a remote agent endpoint registered in the local configuration files.

#### Parameters:

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `agent_name` | string | ✓ | Remote agent alias registered in `a2a-list.json`. |
| `message` | string | ✓ | Full instruction to send to the remote agent. |

#### Example Payload:
```json
{
  "agent_name": "kubernetes-deployer",
  "message": "Deploy image tag v1.2.3 to development namespace."
}
```

---

### skill_ref - Skill Reference Loading

Load reference context from a localized skill. This keeps large reference schemas, api endpoints, and documentation out of the main agent prompt until specifically requested.

#### Parameters:

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `skill` | string | ✓ | Skill directory name. |
| `ref` | string | ✓ | Path of the reference file inside the skill folder. |

#### Example Payload:
```json
{
  "skill": "kubernetes-skill",
  "ref": "references/helm-deployment.md"
}
```

---

## 4. Best Practices for Developers & Users

To get the absolute best out of MothX's tool loop, keep the following principles in mind:

1. **The Read-Before-Modify Standard**: Always ensure you (or the model) call `read` on files before attempting to modify them with `edit`. Having the exact, complete block in context ensures the `oldText` parameter matches perfectly and prevents atomic editing failures.
2. **Minimize `bash` usage for exploration**: Avoid running shell commands like `grep`, `find`, or `cat` inside a bash tool. MothX's dedicated `grep`, `find`, and `read` tools are heavily optimized, parsed directly into structured data, and do not suffer from sub-shell spawn latencies or sandbox restrictions.
3. **Handle Long-Running Jobs with Care**: When spinning up servers or continuous builders using `bash` in async mode (`async=true`), regularly call `jobs` to clean up exited jobs (`cleanup=true`) to keep the system overhead down, and use `kill` to shut down background servers once they are no longer required.
