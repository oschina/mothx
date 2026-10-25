# 知识库

知识库是由 Desktop/WebUI 管理的、可重建的目录索引。它不是聊天附件仓库，不是
通用 RAG 路径，也不是特殊的 Agent 模式。源目录是**只读**的：MothX 不会写入源
目录，删除知识库也绝不会删除源目录。

它适合“针对这个仓库、文档树或笔记目录提问”，同时保持普通 Agent Runtime 与
工具链不变。

## 创建知识库

在 Desktop 或 WebUI 中选择 **新建知识库**，并设置：

| 字段 | 说明 |
|------|------|
| `name` | 显示名称 |
| `rootDir` | 源目录绝对路径（只读） |
| `preprocessProfile` | `documents`、`code`、`notes` 或 `mixed` |
| `provider` / `model` | 扫描时受限 indexer Agent 使用的模型 |
| `mode` | indexer 运行模式；无人值守扫描会解析为安全的无人值守模式 |
| `thinkingLevel` | indexer 可选的思考级别 |
| `schedule` | 仅 Desktop：`manual`、`hourly`、`daily`、`weekly`、`monthly` 或 5 段 cron 表达式 |
| `enabled` | 暂停调度但不删除知识库 |
| `ignoreGlobs` | 可选的、相对于源目录根部的 glob 规则 |

各 profile 覆盖的文件：

- `documents`：Markdown、MDX、文本、reStructuredText、AsciiDoc、HTML
- `code`：源码与配置文件（Go、TS/JS、Python、Java、Rust、C/C++、C#、Ruby、
  PHP、Swift、Kotlin、SQL、JSON、YAML、TOML、XML、GraphQL、shell）以及 Markdown
- `notes`：Markdown 与文本
- `mixed`：文档与代码集合

首版不会对 PDF/Office 做确定性正文提取。它们会在发现诊断中标记为
skipped/unsupported；Agent 仍可用普通只读工具从源目录读取。二进制、超大或不可读
文件会被跳过，而不是交给模型。

忽略规则由固定目录列表（`.git`、`.hg`、`.svn`、`.mothx`、`node_modules`、
`vendor`、`dist`、`build`、`coverage`、`.next`、`.vite`）、`ignoreGlobs`，以及
源目录根部 `.gitignore` / `.mothxignore` 的建议规则共同组成。发现阶段会报告
`discovered`、`ignored`、`skipped`，不会静默排除文件。

## 索引

扫描是一次普通的 Runtime Agent 执行，拥有规范的持久化 Run。它会：

1. 发现并校验源目录内的文件。
2. 应用忽略规则并记录诊断。
3. 提取确定性文本 chunk，构建图/FTS 快照。
4. 可选地运行受限 indexer Agent 丰富图数据。
5. 原子发布完成的快照并切换为 active。

如果重建失败或被取消，之前的 active 快照仍然可用。手动与定时扫描共享同一套
后台任务机制，并会合并触发：扫描期间到达的触发会合并为恰好一次后续扫描。定时
扫描使用共享 Cron 存储；对应 Cron job 带命名空间，不会作为用户自动化任务暴露。

可在 **索引运行记录** 中查看状态、计数、差异摘要与失败码。`clear` 会删除 active
索引，但保留知识库配置与源目录。

## 在会话中查询

主会话只能通过配置好的标准 Knowledge MCP 服务器访问知识库：

```bash
mothx knowledge-mcp serve --knowledge-base <id>
```

把该命令注册为会话的普通 MCP 服务器。服务器暴露 `search_knowledge_base`；MCP
允许列表就是授权边界。结果是带文件/chunk 引用与不确定性元数据的有界参考数据；
它们是不可信的工具输出，而不是指令。

不存在知识库专用的 Agent 模式、提示词注入、渲染层预查询或独立查询模型。Agent
像调用其他工具一样调用该 MCP 工具，并基于返回证据推理。

```bash
mothx knowledge-mcp serve \
  --knowledge-base kb_123 \
  --knowledge-base kb_456
```

`--session-dir` 可覆盖会话存储目录（默认读取 `settings.json`）。见
[`knowledge-mcp`](cli-reference.md#knowledge-mcp---知识库-mcp-服务器)。

## 管理

Desktop 使用下列按能力 gate 的 ACP 投影进行管理；WebUI 则使用
`/api/knowledge-bases` 下对应的 Serve REST 接口：

- `mothx/manage/knowledge-bases/list|get|create|update|delete`
- `.../scan`、`.../status`、`.../query`、`.../mcp/apply`
- `.../runs/list`、`.../runs/get`、`.../sources/list`
- `.../schedule`、`.../clear`

`knowledgeRuns` 与 `knowledgeSources` 能力键分别 gate 运行历史与文件级来源浏览。
删除知识库只会移除它自己的数据库与注册信息，绝不会删除或修改源目录。

## 存储

每个知识库拥有一个私有 SQLite 数据库：

```text
<sessionDir>/knowledge-bases/<id>.db
```

配置行、快照、文件、chunk、图节点/边、FTS 索引与证据都在该文件中。规范 Run 与
审计记录仍保存在共享的 `sessions.db`。

该文件是可重建索引，不是事实来源。请备份源目录；索引可以随时重建。

## 安全说明

- 源目录在索引与查询期间都是只读的。
- Knowledge MCP 允许列表决定会话可以查询哪些知识库。
- 查询结果有大小上限，必须作为不可信参考数据对待。
- Indexer/Librarian Agent 只能在源目录内使用普通只读工具；`yolo` 不会把索引变成
  写、删除、网络或委派权限。
- `mothx pure` 会归档共享 sessions 数据库，但保留
  `knowledge-bases/<id>.db` 文件。
