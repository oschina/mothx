# Session Management

MothX stores sessions in SQLite, supporting tree structure, branching, compaction, labels, and fast session lookup.

## Session Storage

### Storage Architecture

MothX's session architecture differs based on the execution mode:

1. **CLI / TUI / Serve Mode (Single Database + Virtual Handles)**
   All session metadata (session list, session IDs, CWD, timestamps) and all history messages/entries are stored entirely inside a single, unified SQLite database file `sessions.db` under `sessionDir`.
   In this mode, **no physical working directory subdirectories or per-session handle files are created on disk**. The `.db` paths displayed in the CLI/TUI (e.g., `~/.mothx/sessions/20260625-120000_abcd1234.db`) are **virtual paths** (handles) computed dynamically from the database metadata. They do not exist on the filesystem but are fully recognized, navigated, and deleted by the program.

2. **Serve Channels (Single Database + Physical Handles)**
   Unattended messaging channels store records in the single `sessions.db` database and additionally write physical subdirectory handle files containing the session ID (e.g., `20260625-120000_abcd1234.db`) under platform-specific or per-user paths on disk. This layout facilitates platform-specific mapping and lifecycle tracking.

### Storage Location Layout

```text
~/.mothx/sessions/
├── sessions.db                       # The unified SQLite database for all session entries and metadata
└── channels/                           # (Only present for messaging channels)
    └── wechat/user_123/active.db       # Channels platform-specific physical session handle
```

### Path Encoding

In scenarios requiring directory isolation (such as Channels), working directory paths are encoded with URL-safe base64 to avoid collisions and filesystem issues.

Examples:
- `/home/user/project` → `--L2hvbWUvdXNlci9wcm9qZWN0--`
- `/home/user/my.app` → a distinct encoded directory name

## SQLite Schema

Session state is stored in `sessions.db` using two core tables:

| Table | Purpose |
|-------|---------|
| `sessions` | Session metadata: ID, working directory, timestamp, parent session, schema version |
| `entries` | Ordered event log: messages, model changes, compactions, labels, and metadata entries |

Entries keep stable IDs and parent IDs so the conversation can be replayed as a tree and compacted safely.

### Entry Types

| Type | Description |
|------|-------------|
| `session` | Session metadata |
| `message` | User/assistant/tool messages |
| `model_change` | Model switch record |
| `thinking_level_change` | Thinking-level switch record |
| `compaction` | Context compression checkpoint |
| `custom` | Runtime-specific custom metadata entry |
| `custom_message` | Message payload created by an external or custom runtime |
| `session_info` | Display metadata such as session name |
| `branch_summary` | Branch switch summary |
| `label` | User-defined label |

### Durability and Write Performance

`sessions.db` runs in WAL mode with the durability level defaulting to the SQLite-recommended `synchronous(NORMAL)`: commits no longer fsync individually (syncing is concentrated at WAL checkpoints), which markedly lowers writer-lock contention and write latency when several processes (Serve, TUI, ACP, CLI) share one session directory. The semantics are:

- A process crash (kill -9, panic) loses nothing.
- An OS crash or power loss does not corrupt the database, but commits made since the last checkpoint (typically seconds of writes) may roll back; a missing run terminal state converges through the lease-expiry → orphan-recovery path, so a session never stays stuck as "running" forever.

Deployments sensitive to power loss (unstable power, some network drives) can restore the legacy per-commit fsync behavior with the `MOTHX_SQLITE_SYNCHRONOUS=FULL` environment variable; it takes effect per process, and mixed old/new processes sharing one database file is safe. With `--debug`, the `mothx_sqlite` metrics on the `/debug/vars` endpoint (busy-retry hits and backoff total, transaction begin count/total wait/slowest single wait) make cross-process writer contention observable. Full design and load-test matrix: `docs/proposal/sqlite-write-pressure-reduction-proposal.md`.

## Session Operations

### Create New Session

```go
sess := session.New(cwd, sessionDir)
if err := sess.Init(); err != nil {
    return err
}
```

### Continue Recent Session

```bash
mothx --continue
mothx -c
```

```go
sess, err := session.ContinueRecent(cwd, sessionDir)
```

### Resume Specific Session

```bash
# By session ID or unique prefix
mothx --resume abcd1234

# By session handle path
mothx --resume ~/.mothx/sessions/--encoded-working-directory--/20260625-120000_abcd1234.db
```

```go
sess, err := session.OpenByPathOrID(cwd, sessionDir, "abcd1234")
```

### TUI Session Picker

In interactive TUI mode, starting without `--continue`, `--resume`, or `--session` does not create an empty session immediately. The session is created when the first user message is sent.

Use `/sessions` to open the interactive session picker. It supports Up/Down navigation, Enter to switch, `n` to start fresh, `d` to delete the selected session, and Esc to close. Text commands are still available:

```bash
/sessions ls
/sessions set abcd1234
/sessions clear
/sessions del abcd1234
```

When an existing session is continued, resumed, or selected, its history is printed into the normal terminal scrollback.

### Web UI Session History

The Serve Web UI history list is sorted by last-used time in descending order, with the most recently replied-to session at the top. The Web UI session-management page also shows each session's last reply time for quickly locating recent conversations.

### Add Message

```go
_, err := sess.AppendMessage(provider.NewUserMessage("Hello"))
```

## Tree Structure

Session entries form a parent-linked tree:

```text
session-abcd1234
├── msg-001 (user: "Hello")
│   └── msg-002 (assistant: "Hi!")
│       └── msg-003 (user: "Tell me more")
└── msg-004 (user: "Different question")  # Branch point
    └── msg-005 (assistant: "...")
```

This supports exploring different directions, returning to previous points, and preserving alternate solutions.

## Session Compression

MothX records compaction checkpoints in SQLite. After compaction, replay state preserves per-message entry IDs and the compaction boundary (`firstKeptEntryID`). When a session is reloaded:

- Messages are trimmed to the correct compaction boundary
- The summary message is prepended automatically
- Subsequent messages keep their original entry IDs

Configure compaction in settings:

```json
{
  "compaction": {
    "enabled": true,
    "reserveTokens": 16384,
    "keepRecentTokens": 20000
  }
}
```

## Best Practices

### Regular Cleanup

Prefer the built-in `/sessions del <id>` command or the authenticated session API when deleting normal CLI, TUI, or Serve sessions. These interfaces remove the session records from the shared `sessions.db`; virtual handles do not require manual file deletion.

```bash
/sessions ls
/sessions del abcd1234
```

Manual physical-handle cleanup is normally relevant only to channel-specific session bindings. Inspect first and remove only dated binding files under encoded working-directory subdirectories. Do not delete the root `sessions.db` unless you intend to remove all persisted session data.

```bash
# Example: inspect old channel binding files first, then delete carefully
find ~/.mothx/sessions -path '*/channels/*/*.db' -mtime +30 -print
```

### Backup Important Sessions

Back up the session root directory, especially `sessions.db` and the encoded working-directory handle directories:

```bash
cp -a ~/.mothx/sessions ~/backups/
```

## Troubleshooting

### Session Database Error

```text
Error: session "..." not registered in DB
```

Possible causes:
- The session handle file exists but the SQLite record was removed
- `sessions.db` was deleted or restored from an older backup
- The session root was partially copied

Solutions:
1. Restore the full session root from backup
2. Resume by a valid session ID shown in `/sessions list`
3. Delete stale handle files if the SQLite record no longer exists

### Session Lost

Possible causes:
- Working directory changed
- Session handle file or database was deleted
- The encoded working-directory directory is different

Solutions:
1. Check `~/.mothx/sessions/` or `%APPDATA%\mothx\sessions\`
2. Use `--resume <session-id>` from the original working directory
3. Confirm the configured `sessionDir` is correct
