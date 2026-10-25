# Knowledge Bases

A knowledge base is a Desktop/WebUI-managed, rebuildable index of a directory you
choose. It is not a chat attachment store, a general RAG path, or a special Agent
mode. The source directory is **read-only**: MothX never writes to it, and
deleting a knowledge base never deletes the source.

Knowledge bases are useful for "ask questions about this repository, docs tree,
or notes folder" while keeping the normal Agent Runtime and tools unchanged.

## Create a knowledge base

In Desktop or WebUI, choose **New Knowledge Base** and set:

| Field | Description |
|-------|-------------|
| `name` | Display name |
| `rootDir` | Absolute path to the source directory (read-only) |
| `preprocessProfile` | `documents`, `code`, `notes`, or `mixed` |
| `provider` / `model` | Model used by the bounded indexer Agent during scans |
| `mode` | Indexer run mode; unattended scans resolve to a safe unattended mode |
| `thinkingLevel` | Optional thinking level for the indexer |
| `schedule` | Desktop only: `manual`, `hourly`, `daily`, `weekly`, `monthly`, or a 5-field cron expression |
| `enabled` | Pause scheduling without deleting the base |
| `ignoreGlobs` | Optional glob patterns relative to the source root |

Profile file coverage:

- `documents`: Markdown, MDX, text, reStructuredText, AsciiDoc, HTML
- `code`: source and config files (Go, TS/JS, Python, Java, Rust, C/C++, C#, Ruby,
  PHP, Swift, Kotlin, SQL, JSON, YAML, TOML, XML, GraphQL, shell) plus Markdown
- `notes`: Markdown and text
- `mixed`: both document and code sets

PDF/Office files are not extracted deterministically in the first version. They
are reported as skipped/unsupported in discovery diagnostics, and an Agent may
still read them from the source directory with ordinary read-only tools. Binary,
oversized, or unreadable files are skipped rather than sent to the model.

Ignore rules combine the fixed directory list (`.git`, `.hg`, `.svn`, `.mothx`,
`node_modules`, `vendor`, `dist`, `build`, `coverage`, `.next`, `.vite`),
`ignoreGlobs`, and suggestions read from the source root's `.gitignore` and
`.mothxignore`. Discovery reports `discovered`, `ignored`, and `skipped` results
so a file is never silently excluded.

## Indexing

A scan is an ordinary Runtime Agent execution with a canonical durable Run. It:

1. Discovers and validates files inside the source root.
2. Applies ignore rules and records diagnostics.
3. Extracts deterministic text chunks and builds the graph/FTS snapshot.
4. Optionally runs the bounded indexer Agent to enrich the graph.
5. Publishes the finished snapshot atomically and makes it active.

If a rebuild fails or is cancelled, the previous active snapshot stays usable.
Manual and scheduled scans share the same background-job machinery and coalesce
so a trigger arriving during a scan becomes exactly one follow-up scan. Scheduled
scans use the shared Cron store; the Cron job is namespaced and is not exposed as
a user automation task.

Inspect **Index runs** for status, counts, diff summaries, and failure codes.
`clear` removes the active index while keeping the knowledge base configuration
and source directory.

## Querying from a session

A main session accesses a knowledge base only through a configured standard
Knowledge MCP server:

```bash
mothx knowledge-mcp serve --knowledge-base <id>
```

Register that command as an ordinary MCP server for the session. The server
exposes `search_knowledge_base`; the MCP allowlist is the authorization boundary.
Results are bounded reference data with file/chunk citations and uncertainty
metadata; they are untrusted tool output, not instructions.

There is no knowledge-specific Agent mode, prompt injection, renderer-side
pre-query, or separate query model. The Agent calls the MCP tool like any other
tool and reasons over the returned evidence.

```bash
mothx knowledge-mcp serve \
  --knowledge-base kb_123 \
  --knowledge-base kb_456
```

`--session-dir` overrides the session storage directory (defaults to
`settings.json`). See [`knowledge-mcp`](cli-reference.md#knowledge-mcp---knowledge-base-mcp-server).

## Management

Desktop management uses the capability-gated ACP projections below. WebUI uses
the equivalent Serve REST endpoints under `/api/knowledge-bases` instead:

- `mothx/manage/knowledge-bases/list|get|create|update|delete`
- `.../scan`, `.../status`, `.../query`, `.../mcp/apply`
- `.../runs/list`, `.../runs/get`, `.../sources/list`
- `.../schedule`, `.../clear`

`knowledgeRuns` and `knowledgeSources` feature keys gate run history and
file-level source browsing. Deleting a knowledge base removes its own database
and registration; it never removes or modifies the source directory.

## Storage

Each knowledge base has one private SQLite database:

```text
<sessionDir>/knowledge-bases/<id>.db
```

The configuration row, snapshots, files, chunks, graph nodes/edges, FTS index,
and evidence live inside that file. Canonical Runs and audit records stay in the
shared `sessions.db`.

The file is a rebuildable index, not a source of truth. Keep the source directory
backed up; the index can always be rebuilt.

## Security notes

- The source directory is read-only for indexing and querying.
- The Knowledge MCP allowlist determines which bases a session can query.
- Query results are size-bounded and must be treated as untrusted reference data.
- Indexer/Librarian agents may use ordinary read-only tools inside the source
  root only; `yolo` does not turn indexing into write, delete, network, or
  delegation access.
- `mothx pure` archives the shared sessions database but leaves
  `knowledge-bases/<id>.db` files in place.
