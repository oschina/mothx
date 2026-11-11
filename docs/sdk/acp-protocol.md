# MothX ACP 协议参考

本文描述 MothX 作为 ACP Agent（`mothx acp`）对外暴露的完整协议面，包括传输、握手、会话生命周期、流式事件、决策请求、MothX 扩展方法与 `mothx/manage/*` 管理投影。

- 协议基线：**Agent Client Protocol v1**
- 传输：**JSON-RPC 2.0 over stdio**（NDJSON，一行一个完整 JSON 对象）
- 标准方法前缀：无（`initialize`、`session/*`、`$/cancel_request`）
- MothX 扩展前缀：`mothx/*`、`_mothx/*`，以及 `_meta` 中的 `mothx.dev`
- 所有扩展都是**附加能力**；不声明/不使用扩展的 ACP 客户端仍可完成标准会话流程。

MothX 的产品边界是“一个 Agent Core + 一个前端中立的 Agent Runtime，ACP 只是薄投影”。因此本文中的字段要么来自标准 ACP，要么是 Runtime 既有状态的只读/受控投影；ACP 层不拥有第二套会话、运行、附件或决策状态。

---

## 目录

1. [传输与进程模型](#1-传输与进程模型)
2. [JSON-RPC 规则](#2-json-rpc-规则)
3. [启动与命令行参数](#3-启动与命令行参数)
4. [initialize 与能力协商](#4-initialize-与能力协商)
5. [工作区模型](#5-工作区模型)
6. [会话生命周期](#6-会话生命周期)
7. [发送提示与执行生命周期](#7-发送提示与执行生命周期)
8. [Prompt 内容块](#8-prompt-内容块)
9. [session/update 通知](#9-sessionupdate-通知)
10. [服务端发起的决策请求](#10-服务端发起的决策请求)
11. [_mothx/session_event 与扩展通知](#11-_mothxsession_event-与扩展通知)
12. [MothX 会话与资源扩展方法](#12-mothx-会话与资源扩展方法)
13. [管理投影 mothx/manage/*](#13-管理投影-mothxmanage)
14. [MCP 集成](#14-mcp-集成)
15. [错误模型](#15-错误模型)
16. [完整交互示例](#16-完整交互示例)
17. [客户端实现清单](#17-客户端实现清单)
18. [附录：MothX 作为 ACP 客户端](#18-附录mothx-作为-acp-客户端)

---

## 1. 传输与进程模型

### 1.1 stdio NDJSON

MothX 从 **stdin** 读取请求，向 **stdout** 写入响应和通知。

- 每条消息是一个 JSON 对象，后跟一个 `\n`。
- 消息内部不得包含未转义换行。
- stdout **只用于协议消息**；日志、警告、调试信息写入 stderr。
- 单条入站消息上限为 **10 MiB**（`10 << 20` 字节）。超过上限会返回解析错误并继续读取后续消息。
- 空行会被忽略。
- 进程读到 stdin EOF 时正常退出，并执行会话运行时清理。

### 1.2 并发与顺序

- 读循环负责接收请求、响应以及客户端对反向请求的回答。
- `session/new`、`session/load`、`session/fork`、`session/close`、`session/delete` 等会话生命周期操作按 `(连接, sessionId)` 串行执行，避免慢操作阻塞读循环。
- `session/prompt` 会先进入 Runtime 的每会话提交队列；同一会话同时只有一个 Run，后续 prompt 按 FIFO 等待。
- `session/cancel` 与 `$/cancel_request` 在读循环上同步处理，因此即使有会话加载或 provider 探测在执行，也能及时取消。
- 不同会话之间可以并行。

### 1.3 stdout 生命周期

客户端应在子进程退出前持续读取 stdout。服务端退出时：

1. 停止接受新的会话操作；
2. 取消活动 Run；
3. 等待终态并释放 MCP 等资源；
4. 关闭数据库与后台调度器。

---

## 2. JSON-RPC 规则

### 2.1 请求、响应、通知

请求：

```json
{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/home/me/project"}}
```

成功响应：

```json
{"jsonrpc":"2.0","id":1,"result":{"sessionId":"..."}}
```

错误响应：

```json
{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"cwd is required"}}
```

通知（无 `id`，不应答）：

```json
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"...","update":{"sessionUpdate":"agent_message_chunk"}}}
```

### 2.2 ID 规则

- `id` 可为字符串或整数。
- `null` 被视为合法请求 ID，并会被原样回显。
- 对象、数组、小数、指数形式的数值 ID 非法，返回 `-32600`。
- 通知不得带 `id`；服务端不会为通知发送响应。
- 服务端发起的反向请求使用自己的 ID 空间，格式为 `acp-1`、`acp-2`……

### 2.3 初始化顺序

- `initialize` 必须是第一条方法调用。
- 除 `initialize` 外，任何方法在初始化前调用都会得到 `-32600: initialize must be called first`。
- `initialize` 只能调用一次；第二次会得到 `-32600`。
- 初始化前的通知会被静默忽略（不会报错，也没有响应）。

### 2.4 未知方法

- 未知请求：`-32601 method not found`。
- 未知通知：静默忽略。
- 未知 `mothx/manage/*` 请求：`-32601`，并带结构化 `error.data.code = "manage_method_not_found"`。

---

## 3. 启动与命令行参数

### 3.1 启动命令

```bash
mothx acp
```

常见组合：

```bash
mothx acp --provider deepseek-openai --model deepseek-v4-flash
mothx acp --mode agent --sandbox
mothx acp --multi-agent --workflows
mothx acp --browser --web-search --artifact
```

### 3.2 `mothx acp` 参数

| 参数 | 简写 | 说明 |
|---|---|---|
| `--provider` | `-p` | Provider 名称（内置或自定义） |
| `--model` | `-m` | 模型 ID |
| `--mode` | `-M` | 运行模式：`plan`、`agent`、`yolo`、`os`、`router`；空值按产品规则回退 `yolo` |
| `--thinking` | `-t` | 思考级别：`off`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max` |
| `--sandbox` | | 启用沙箱执行 |
| `--verbose` | | 详细日志 |
| `--debug` | | 调试日志与本地 pprof |
| `--multi-agent` | | 启用子 Agent 工具与多 Agent 工作流 |
| `--delegate` | | 启用阻塞式单子 Agent 委托 |
| `--workflows` | | 启用 JavaScript workflow 工具 |
| `--web-search` | | 启用已配置的本地 Web 搜索工具 |
| `--browser` | | 启用浏览器自动化 |
| `--artifact` | | 启用生成制品发布 |
| `--permission-timeout` | | `session/request_permission` 决策超时，Go duration，默认 `5m` |
| `--question-timeout` | | 提问决策超时，Go duration，默认 `5m` |

对应环境变量：

- `MOTHX_ACP_PERMISSION_TIMEOUT`
- `MOTHX_ACP_QUESTION_TIMEOUT`
- `MOTHX_ACP_CRON_INTERVAL`（管理面 Cron 调度 tick，默认 30s，主要用于测试）
- `HARBOR_ACP_REQUESTED_MODEL`（外部宿主请求的 `provider/model`；与显式 CLI 选择冲突时启动失败）

显式 CLI 参数优先于环境变量；非法或非正 duration 会被忽略并回退默认值。

### 3.3 启动失败

如果 ACP 在初始化前失败，MothX 会向 stderr 写一行机器可读错误：

```text
MOTHX_ACP_ERROR {"code":"config_invalid","message":"settings could not be loaded","fix":"Fix settings.json syntax"}
```

已初始化后，ACP 不会因为 provider 不可用而退出：管理面和配置修正仍然可用，执行入口会返回结构化 RPC 错误，直到默认 provider 恢复可用。

---

## 4. initialize 与能力协商

### 4.1 请求

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": 1,
    "clientCapabilities": {
      "fs": {"readTextFile": true, "writeTextFile": true},
      "terminal": true,
      "elicitation": {"form": {}},
      "session": {"configOptions": {"boolean": {}}}
    },
    "clientInfo": {"name": "my-client", "title": "My Client", "version": "1.0.0"},
    "_meta": {
      "mothx": {
        "workspace": {
          "cwd": "/home/me/project",
          "additionalDirectories": ["/home/me/shared"]
        }
      }
    }
  }
}
```

字段说明：

| 字段 | 必填 | 说明 |
|---|---|---|
| `protocolVersion` | 是 | 必须为 `1`。省略时仅对嵌入式/单元 fixture 兼容，真实 stdio 客户端应始终发送 |
| `clientCapabilities` | 否 | 客户端能力，MothX 会保存并在需要时使用 |
| `clientInfo` | 否 | 客户端标识，仅用于展示/诊断 |
| `_meta.mothx.workspace` | 否 | 协商工作区窗口；也接受 `_meta["mothx.dev"].workspace` |

`editorContext`、`parentSessionId`、`surface` 等元数据属于具体请求参数，在 `session/prompt`、`session/fork` 等方法中使用，`initialize` 只消费 `workspace`。

### 4.2 clientCapabilities

MothX 识别的客户端能力如下：

```json
{
  "fs": {"readTextFile": true, "writeTextFile": true},
  "terminal": true,
  "auth": {"terminal": true},
  "elicitation": {"form": {}, "url": {}},
  "session": {"configOptions": {"boolean": {}}}
}
```

当前行为：

- `elicitation.form` 存在时，提问请求优先使用标准 `elicitation/create`；否则使用 `mothx/requestQuestion`。
- `fs` / `terminal` / `auth` 会被记录，但 **MothX 作为 ACP Agent 不会主动向客户端发起 `fs/*` 或 `terminal/*` 反向请求**。MothX 自己的文件、命令和沙箱工具由 Runtime 在服务端执行。
- `session.configOptions.boolean` 表示客户端能处理布尔类型 config option。

### 4.3 响应

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": 1,
    "agentCapabilities": {
      "loadSession": true,
      "promptCapabilities": {
        "image": true,
        "audio": true,
        "embeddedContext": true
      },
      "sessionCapabilities": {
        "close": {},
        "delete": {},
        "list": {},
        "resume": {},
        "fork": {},
        "additionalDirectories": {}
      },
      "mcpCapabilities": {"http": true, "sse": true},
      "_meta": {
        "mothx.dev": {
          "minClientProtocol": 1,
          "doctor": true,
          "requestQuestion": true,
          "sessionEvent": true,
          "artifactProjection": true,
          "attachmentFetch": true,
          "artifactEnabled": false,
          "features": ["sessionConfigProvider", "sessionDraftConfigOptions", "..."]
        }
      }
    },
    "agentInfo": {"name": "mothx", "title": "MothX", "version": "x.y.z"},
    "authMethods": [],
    "_meta": {
      "mothx.dev": {
        "minClientProtocol": 1,
        "doctor": true,
        "requestQuestion": true,
        "sessionEvent": true,
        "artifactProjection": true,
        "attachmentFetch": true,
        "artifactEnabled": false,
        "features": ["sessionConfigProvider", "sessionDraftConfigOptions", "..."]
      }
    }
  }
}
```

说明：

- `agentInfo.name` 固定为 `mothx`，`title` 为 `MothX`，`version` 为构建版本。
- `authMethods` 为空数组：MothX 不通过 ACP auth 方法认证。
- `promptCapabilities` 如实声明文本之外的支持：`image`、`audio`、`embeddedContext`。
- `mcpCapabilities` 中 `http`/`sse` 为 true；stdio 是基线能力。
- `_meta["mothx.dev"]` 同时出现在顶层和 `agentCapabilities._meta`，兼容只读取其中一个位置的客户端。
- `artifactEnabled` 反映当前通信 run 是否允许发布生成制品。

### 4.4 功能键（features）

客户端必须根据 `initialize` 响应里的 `_meta["mothx.dev"].features` gate 可选方法、面板和投影。未声明的能力不应被调用。

当前稳定功能键：

| 功能键 | 含义 |
|---|---|
| `sessionConfigProvider` | 会话配置项支持 provider 切换 |
| `sessionDraftConfigOptions` | 支持创建会话前读取草稿配置项 |
| `sessionDelete` | 支持 `session/delete` / `mothx/session/delete` |
| `sessionSetTitle` | 支持 `mothx/session/setTitle` |
| `sessionListCwd` | `session/list` 支持 cwd 过滤与分页 |
| `sessionListAll` | 支持全局会话库 `mothx/session/listAll` |
| `sessionWorkDir` | 支持 `mothx/session/setWorkDir` |
| `sessionHistoryPaging` | 支持 `mothx/session/history` 分页 |
| `sessionFork` | 支持 `session/fork` |
| `editorContext` | prompt `_meta.mothx.editorContext` 会被纳入上下文 |
| `doctor` | 支持 `mothx/doctor` |
| `requestQuestion` | 支持 `mothx/requestQuestion` 提问扩展 |
| `artifactProjection` | 支持 `sessionUpdate="artifact"` 投影 |
| `attachmentFetch` | 支持 `mothx/attachment/fetch` |
| `runStatus` | 支持 `_mothx/session_event` 的 `run_status` |
| `sessionExecutionState` | 会话响应/列表携带跨进程执行投影 |
| `sessionMeta` | 支持 pinned/projectId 会话元数据 |
| `projects` | 支持 `mothx/projects/*` |
| `workspaceExtend` | 支持 `mothx/workspace/extend` |
| `decisionDeadline` | 支持 `decision_deadline` 提醒事件 |
| `subagentEvents` | 支持子 Agent 生命周期 `subagent` 事件 |
| `subagentPanel` | 支持 `mothx/subagent/list` / `mothx/subagent/messages` |
| `toolResultImages` | 工具结果图片会投影到 `tool_call_update` |
| `usageCacheProjection` | `usage_update._meta["mothx.dev"]` 携带累计缓存统计 |
| `attachmentList` | 支持 `mothx/attachment/list` |
| `manageSettings` | 支持 settings 管理投影 |
| `manageACPProfiles` | 支持 ACP worker profile 管理投影 |
| `manageRouter` | 支持 Router 管理投影 |
| `workerMessages` | 支持 `mothx/worker/messages` |
| `manageApplicationSettings` | 支持 application settings 管理投影 |
| `manageServeConfig` | 支持 serve 配置管理投影 |
| `manageChannels` | 支持渠道配置管理投影 |
| `manageProviders` | 支持 provider 列表/保存/删除 |
| `manageProviderConfig` | 支持 provider 配置编辑与测试 |
| `manageSkills` | 支持技能启用/禁用 |
| `manageMcp` | 支持 MCP 配置管理 |
| `manageCron` | 支持定时任务管理 |
| `manageStats` | 支持统计投影 |
| `manageMemory` | 支持 memory.md 投影 |
| `manageSkillHub` | 支持 SkillHub 设置管理 |
| `manageSkillHubCatalog` | 支持 SkillHub 目录/搜索/安装 |
| `manageExperts` | 支持 Expert/Expert Team 管理 |
| `manageKnowledgeBases` | 支持知识库管理 |
| `manageEnv` | 支持 env.json 管理 |
| `manageDeliveries` | 支持投递失败列表/重试 |
| `knowledgeGraphIndex` | 知识图谱索引投影可用 |
| `knowledgeBaseContext` | prompt 可携带 `knowledgeBaseRefs` |
| `knowledgeRuns` | 知识库索引 Run 历史可用 |
| `knowledgeSources` | 知识库源文件浏览可用 |
| `worktrees` | 支持 worktree 创建/列表/删除 |
| `worktreeReset` | 支持 `mothx/worktree/reset` |

`worktrees` 与 `worktreeReset` 只有在 `settings.json` 的 worktree 开关启用时才会追加。

---

## 5. 工作区模型

### 5.1 协商窗口

工作区窗口可以在 `initialize._meta.mothx.workspace` 中协商：

```json
{
  "cwd": "/home/me/project",
  "additionalDirectories": ["/home/me/shared", "/home/me/vendor"]
}
```

也接受旧命名空间：

```json
{"_meta":{"mothx.dev":{"workspace":{"cwd":"/home/me/project"}}}}
```

同时，`session/new`、`session/load`、`session/resume`、`session/fork` 等请求还可以在顶层传 `cwd` 与 `additionalDirectories`。

### 5.2 授权规则

- `initialize` 中协商的 `cwd` 是本进程的**不可变主工作区根**。
- 后续请求如果通过 `_meta.mothx.workspace.cwd` 指定 cwd，必须与顶层 `cwd` 一致；不一致返回 `cwd does not match workspace cwd`。
- 会话的 `cwd` 必须是已协商的根之一（主 cwd 或 additional directory），不能只是其子目录；否则返回 `cwd is outside the negotiated workspace`。
- `additionalDirectories` 同样按精确根校验；越界返回 `additional directory is outside the negotiated workspace: ...`。
- 所有路径必须是绝对路径。
- 符号链接会通过 `EvalSymlinks` 规范化后比较。
- Runtime 管理的 worktree 目录是单独的授权来源，不消耗 additional-directory 配额。

### 5.3 动态扩展

`mothx/workspace/extend` 可以只增不减地扩展窗口：

```json
{"jsonrpc":"2.0","id":2,"method":"mothx/workspace/extend","params":{"additionalDirectories":["/home/me/shared"]}}
```

响应：

```json
{"jsonrpc":"2.0","id":2,"result":{"cwd":"/home/me/project","additionalDirectories":["/home/me/shared"]}}
```

规则：

- 只接受存在目录的绝对路径。
- symlink 会解析后再加入。
- 自动去重并合并。
- 合并后的附加目录最多 **16** 个，超过返回 `workspace_limit_exceeded`。
- `cwd` 不可变，窗口不会收缩。
- 成功后会向所有打开的会话同步新窗口，并广播：

```json
{
  "jsonrpc":"2.0",
  "method":"_mothx/session_event",
  "params":{
    "event":"workspace",
    "cwd":"/home/me/project",
    "additionalDirectories":["/home/me/shared"]
  }
}
```

---

## 6. 会话生命周期

### 6.1 方法总览

| 方法 | 类型 | 说明 |
|---|---|---|
| `session/new` | request | 新建会话 |
| `session/load` | request | 打开已有会话，默认重放历史 |
| `session/resume` | request | 重新连接已有会话，不重放历史 |
| `session/fork` | request | 在当前状态分叉会话 |
| `session/list` | request | 列出当前工作区根下的会话 |
| `mothx/session/listAll` | request | 列出全局会话库 |
| `mothx/session/history` | request | 分页读取历史 |
| `mothx/session/draft-config-options` | request | 新会话草稿配置项 |
| `session/close` | request | 取消活动运行并释放运行时资源 |
| `session/delete` / `mothx/session/delete` | request | 删除非活动会话 |
| `mothx/session/setTitle` | request | 设置会话标题 |
| `mothx/session/setWorkDir` | request | 移动空闲会话的工作目录 |
| `mothx/session/setMeta` | request | 设置 pinned/project 元数据 |
| `session/prompt` | request | 发送一轮提示 |
| `session/cancel` | request/notification | 取消当前提示 |
| `session/set_mode` | request | 切换模式 |
| `session/set_config_option` | request | 切换配置项 |
| `$/cancel_request` | notification | 按 JSON-RPC 请求 ID 取消 |

### 6.2 `session/new`

请求：

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "session/new",
  "params": {
    "cwd": "/home/me/project",
    "additionalDirectories": ["/home/me/shared"],
    "mcpServers": [
      {
        "name": "my-db",
        "type": "stdio",
        "command": "mcp-server",
        "args": ["--port", "8080"],
        "env": [{"name": "DB_URL", "value": "postgres://localhost/db"}]
      }
    ],
    "_meta": {
      "mothx": {
        "workspace": {"cwd": "/home/me/project"}
      }
    }
  }
}
```

响应：

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "sessionId": "20261111-...",
    "modes": {
      "currentModeId": "yolo",
      "availableModes": [
        {"id": "agent", "name": "Agent"},
        {"id": "plan", "name": "Plan"},
        {"id": "yolo", "name": "Yolo"},
        {"id": "os", "name": "OS"},
        {"id": "router", "name": "Router"}
      ]
    },
    "configOptions": [
      {
        "type": "select",
        "id": "provider",
        "name": "Provider",
        "category": "provider",
        "currentValue": "deepseek",
        "options": [{"value": "deepseek", "name": "Deepseek"}]
      },
      {
        "type": "select",
        "id": "model",
        "name": "Model",
        "category": "model",
        "currentValue": "deepseek/deepseek-v4-flash",
        "options": [{"value": "deepseek/deepseek-v4-flash", "name": "DeepSeek V4 Flash"}]
      },
      {
        "type": "select",
        "id": "mode",
        "name": "Mode",
        "category": "mode",
        "currentValue": "yolo",
        "options": [
          {"value": "agent", "name": "Agent"},
          {"value": "plan", "name": "Plan"},
          {"value": "yolo", "name": "Yolo"},
          {"value": "os", "name": "OS"},
          {"value": "router", "name": "Router"}
        ]
      },
      {
        "type": "boolean",
        "id": "browser",
        "name": "Browser",
        "category": "browser",
        "currentValue": "false"
      },
      {
        "type": "boolean",
        "id": "web_search",
        "name": "Web search",
        "category": "web_search",
        "currentValue": "false"
      }
    ]
  }
}
```

说明：

- `cwd` 必填，且必须在已协商窗口内。
- `mcpServers` 在会话创建时连接；连接发生在 Agent 冻结工具集之前。
- `provider/model` 配置项使用 `provider/model` 形式的限定模型值。
- 当模型支持 reasoning 时，还会出现 `id:"thinking_level"` 选项。
- 还可能包含 `id:"expert"` 选项。
- 创建成功后服务端会发送 `available_commands_update`。

### 6.3 `session/load`

请求：

```json
{
  "jsonrpc":"2.0",
  "id":3,
  "method":"session/load",
  "params":{
    "sessionId":"20261111-...",
    "cwd":"/home/me/project",
    "additionalDirectories":[],
    "mcpServers":[],
    "historyLimit":0
  }
}
```

行为：

- `historyLimit <= 0`：服务端在响应前通过 `session/update` 逐条重放完整历史（用户消息、助手文本、思考、工具调用、工具结果），然后响应。
- `historyLimit > 0`：不通过通知重放，而是在响应中返回分页历史。
- 会重放持久化的生成制品（`sessionUpdate:"artifact"`）。
- 会重新投影仍待处理的审批/提问请求。
- 返回结果：

```json
{
  "sessionId": "20261111-...",
  "modes": {"currentModeId":"yolo","availableModes":["..."]},
  "configOptions": ["..."],
  "history": {
    "sessionId": "20261111-...",
    "updates": [{"sessionUpdate":"user_message_chunk"}],
    "nextCursor": "YWNwLWhpc3RvcnktdjE6NDA"
  },
  "execution": {
    "sessionId":"...",
    "state":"running",
    "running":true,
    "busy":true,
    "canSubmit":false,
    "canCancelLocal":true,
    "source":"db"
  }
}
```

`execution` 是附加字段，仅在会话非 idle 时出现；idle 会话不携带该键。

### 6.4 `session/resume`

```json
{"jsonrpc":"2.0","id":4,"method":"session/resume","params":{"sessionId":"...","cwd":"/home/me/project"}}
```

- 与 `load` 相同地重新打开 Runtime 资源。
- 不重放会话历史。
- 会重放待处理决策请求与生成制品。
- 结果同样是 `newSessionResult`，通常不含 `history`。

### 6.5 `session/fork`

```json
{
  "jsonrpc":"2.0",
  "id":5,
  "method":"session/fork",
  "params":{
    "sessionId":"parent-id",
    "cwd":"/home/me/project",
    "atSeq":42,
    "requestId":"fork-req-1",
    "titleMode":"copy",
    "expertId":"team-a"
  }
}
```

字段：

| 字段 | 说明 |
|---|---|
| `sessionId` | 父会话，必填 |
| `cwd` | 必须等于父会话 cwd |
| `atSeq` | 可选，在指定消息序号处分叉 |
| `requestId` | 幂等键；省略时使用 JSON-RPC 请求 ID |
| `titleMode` | 子会话标题策略 |
| `expertId` | 附加字段。非空字符串绑定该 Expert；空字符串创建未绑定子会话；省略保留父绑定 |
| `mcpServers` | 可选，子会话的 MCP 配置 |
| `additionalDirectories` | 可选 |

响应为 `newSessionResult`，包含新 `sessionId` 和 `parentSessionId`。若 `expertId` 指向无效 bundle，返回 `-32602`。

### 6.6 `session/list`

请求：

```json
{"jsonrpc":"2.0","id":6,"method":"session/list","params":{"cwd":"/home/me/project","cursor":""}}
```

- `cwd` 必填，且必须在协商窗口内。
- 只返回 cwd 等于请求 cwd 或协商 additional roots 的会话。
- 每页默认 **50** 条。
- `cursor` 为不透明 base64 游标；非法游标返回 `-32602 invalid cursor`。

响应：

```json
{
  "sessions": [
    {
      "sessionId": "...",
      "cwd": "/home/me/project",
      "additionalDirectories": [],
      "title": "Fix login bug",
      "provider": "deepseek",
      "model": "deepseek-v4-flash",
      "mode": "yolo",
      "thoughtLevel": "medium",
      "parentSessionId": "",
      "updatedAt": "2026-11-11T10:00:00Z",
      "_meta": {
        "messageCount": 12,
        "pinned": false,
        "projectId": null,
        "lastRun": {
          "runId": "acp_1_...",
          "status": "completed",
          "startedAt": "2026-11-11T09:58:00Z",
          "finishedAt": "2026-11-11T09:59:00Z",
          "active": false
        },
        "execution": {
          "sessionId": "...",
          "state": "running",
          "busy": true,
          "canSubmit": false,
          "source": "db"
        }
      }
    }
  ],
  "nextCursor": "..."
}
```

### 6.7 `mothx/session/listAll`

列出跨工作区的全局会话库。

```json
{
  "jsonrpc":"2.0","id":7,"method":"mothx/session/listAll",
  "params":{"scope":"all","projectId":"","query":"login","cursor":""}
}
```

- `scope`: `all`（默认）、`project`、`ungrouped`。
- `projectId` 非空时隐式 `scope=project`。
- `query` 对 session id、标题/预览、cwd、项目名做包含匹配。
- 每页 50 条。
- 返回结构与 `session/list` 相同。
- 列出全局会话不会自动授权其工作目录；对具体会话执行 load/fork/prompt 时仍会做工作区校验。

### 6.8 `mothx/session/history`

```json
{"jsonrpc":"2.0","id":8,"method":"mothx/session/history","params":{"sessionId":"...","cursor":"","limit":40}}
```

- 仅对已打开会话有效；未知会话返回 `unknown session`。
- `limit` 默认 **40**，最大 **100**。
- 结果：

```json
{
  "sessionId":"...",
  "updates":[{"sessionUpdate":"agent_message_chunk","messageId":"...","content":{"type":"text","text":"..."}}],
  "nextCursor":"YWNwLWhpc3RvcnktdjE6MjA"
}
```

`updates` 使用与 `session/update` 相同的 update 结构。

### 6.9 `mothx/session/draft-config-options`

在创建会话前读取同一 Runtime 的初始选项。

```json
{"jsonrpc":"2.0","id":9,"method":"mothx/session/draft-config-options","params":{"cwd":"/home/me/project"}}
```

响应：

```json
{"configOptions":["..."]}
```

若默认 provider 不可用，返回空数组而不是失败。

### 6.10 `session/close`

```json
{"jsonrpc":"2.0","id":10,"method":"session/close","params":{"sessionId":"..."}}
```

行为：

- 取消该会话的活动 Run。
- 取消未决决策。
- 释放 MCP 客户端与 Runtime 资源。
- 返回 `{}`。
- 对未知/已关闭会话是幂等成功。

### 6.11 `session/delete` / `mothx/session/delete`

```json
{"jsonrpc":"2.0","id":11,"method":"session/delete","params":{"sessionId":"..."}}
```

行为：

- 删除会话及其持久化子会话。
- 活动会话拒绝删除：`cannot delete an active session`。
- 不存在或已删除的会话按幂等成功处理，返回 `{}`。
- 删除会获取会话 mutation lease。

### 6.12 `mothx/session/setTitle`

```json
{"jsonrpc":"2.0","id":12,"method":"mothx/session/setTitle","params":{"sessionId":"...","title":"New title"}}
```

- `title` 不能为空。
- 持久化后广播 `session_info_update`。
- 返回 `{}`。

### 6.13 `mothx/session/setWorkDir`

```json
{"jsonrpc":"2.0","id":13,"method":"mothx/session/setWorkDir","params":{"sessionId":"...","cwd":"/home/me/other"}}
```

- 目标 cwd 必须已授权且可用。
- 仅允许空闲会话；运行中返回 `cannot change the work directory while the session is running`。
- 成功后会关闭现有 Runtime，下一次 `session/load` 按新工作目录重建。
- 返回：

```json
{"cwd":"/home/me/other"}
```

### 6.14 `mothx/session/setMeta`

```json
{
  "jsonrpc":"2.0","id":14,"method":"mothx/session/setMeta",
  "params":{"sessionId":"...","pinned":true,"projectId":"project-1"}
}
```

`projectId` 三种语义：

- 省略：保留当前项目绑定。
- `null`：清除项目绑定。
- 字符串：绑定该项目。

响应：

```json
{"pinned":true,"projectId":"project-1","updatedAt":"2026-11-11T10:00:00.123456789Z"}
```

变更会广播带 `_meta.pinned` / `_meta.projectId` 的 `session_info_update`。

### 6.15 `session/set_mode`

```json
{"jsonrpc":"2.0","id":15,"method":"session/set_mode","params":{"sessionId":"...","modeId":"agent"}}
```

- `modeId` 必填；兼容旧客户端的 `mode` 别名字段。
- 允许值：`agent`、`plan`、`yolo`、`os`、`router`。
- `router` 需要已激活的 ACP worker binding；否则返回错误。
- 成功广播 `current_mode_update` 与 `config_option_update`。
- 返回 `{}`。

### 6.16 `session/set_config_option`

```json
{
  "jsonrpc":"2.0","id":16,"method":"session/set_config_option",
  "params":{"sessionId":"...","configId":"model","value":"deepseek/deepseek-v4-flash"}
}
```

支持的 config ID：

| ID | 别名 | 值类型 | 说明 |
|---|---|---|---|
| `provider` | | string | 切换 provider，并级联选择其可用模型 |
| `model` | | string | `provider/model` 或模型 ID；可跨 provider |
| `mode` | | string | 同上模式集合 |
| `thinking_level` | `thought_level`、`thinking` | string | 仅 reasoning 模型 |
| `expert` | | string | Expert bundle ID；允许空字符串解绑 |
| `browser` | | boolean | 浏览器工具开关 |
| `web_search` | `web-search`、`websearch` | boolean | Web 搜索开关 |
| `sandbox` | | boolean | 运行时能力开关；默认不对客户端广告 |

响应：

```json
{"configOptions":["..."]}
```

成功广播 `config_option_update` 与 `session_info_update`。

### 6.17 取消

`session/cancel` 是标准 ACP 取消入口：

```json
{"jsonrpc":"2.0","id":17,"method":"session/cancel","params":{"sessionId":"..."}}
```

- 如果带 `id`，服务端返回 `{}`。
- 取消目标会话当前 Run；若 prompt 仍在等待提交准入，也会中止。
- 正在运行的 prompt 最终返回 `{"stopReason":"cancelled"}`。

`$/cancel_request` 按 JSON-RPC 请求 ID 取消进行中的请求：

```json
{"jsonrpc":"2.0","method":"$/cancel_request","params":{"requestId":42}}
```

- 这是通知，不应答。
- `requestId` 可以是字符串或数字，按原始 JSON 标量精确匹配。
- 可用于取消 `session/prompt`，也可中止正在等待的审批/提问反向请求。
- 取消待处理审批/提问时，挂起中的请求会收到取消结果。

---

## 7. 发送提示与执行生命周期

### 7.1 `session/prompt` 请求

```json
{
  "jsonrpc":"2.0",
  "id":20,
  "method":"session/prompt",
  "params":{
    "sessionId":"...",
    "prompt":[
      {"type":"text","text":"帮我修复登录失败的问题"},
      {"type":"resource_link","name":"logs.txt","uri":"file:///home/me/project/logs.txt","mimeType":"text/plain"}
    ],
    "knowledgeBaseRefs":[
      {"knowledgeBaseId":"kb-1","required":false}
    ],
    "_meta":{
      "mothx":{
        "workspace":{"cwd":"/home/me/project"},
        "editorContext":{
          "uri":"file:///home/me/project/login.go",
          "path":"/home/me/project/login.go",
          "language":"go",
          "selection":{"startLine":10,"endLine":24,"text":"..."},
          "diagnostics":[{"severity":"error","line":18,"message":"undefined: session"}]
        }
      }
    }
  }
}
```

字段：

| 字段 | 必填 | 说明 |
|---|---|---|
| `sessionId` | 是 | 目标会话 |
| `prompt` | 是 | ContentBlock 数组；至少要有文本或可物化附件 |
| `knowledgeBaseRefs` | 否 | Runtime 知识库引用；内容由 Runtime 解析 |
| `_meta.mothx.workspace.cwd` | 否 | 必须与会话 cwd 一致 |
| `_meta.mothx.workspace.additionalDirectories` | 否 | 必须已授权 |
| `_meta.mothx.editorContext` | 否 | 当前编辑器选区/诊断会作为附加上下文 |
| `_meta.mothx.parentSessionId` | 否 | 若提供，必须与持久化 lineage 一致 |
| `_meta.mothx.surface` | 否 | 运行策略元数据 |

行为要点：

- 空 prompt（既无文本也无附件）返回 `-32602 empty prompt`。
- `/systeminit` 会展开为完整初始化提示；在 plan 模式下这一轮临时升级为 agent。
- `/skill <name>`、`/skill:<name>`、`/<skill>` 会激活技能并返回 `end_turn`，不会启动模型 Run。
- 同一会话的 prompt 按 FIFO 排队；`session/prompt` 可能因为等待准入而保持未响应。
- `session/cancel` 或 `$/cancel_request` 可以中止排队中和执行中的 prompt。

### 7.2 stopReason

成功响应：

```json
{"jsonrpc":"2.0","id":20,"result":{"stopReason":"end_turn"}}
```

| stopReason | 含义 |
|---|---|
| `end_turn` | 正常结束（包括工具使用后继续直到自然结束） |
| `max_tokens` | 达到模型/上下文限制 |
| `max_turn_requests` | 达到最大轮次请求限制 |
| `refusal` | 模型拒绝或未知停止原因 |
| `cancelled` | 客户端取消或运行被取消 |

运行失败通常返回 JSON-RPC error，而不是 `stopReason`。错误 `data` 中包含结构化 `errorInfo`。取消是例外：返回 `stopReason: "cancelled"`。

### 7.3 运行生命周期事件

一次 prompt 的典型顺序：

1. 服务端可能先发送 `_mothx/session_event {event:"run_status", status:"running"}`。
2. 发送 `session/update` 流：文本、思考、工具调用、计划、用量等。
3. 如需审批或提问，发送反向请求并等待客户端回答。
4. 运行结束时发送 `_mothx/session_event {event:"terminal", status:"completed|failed|cancelled|incomplete"}`。
5. `session/prompt` 返回 `stopReason` 或 JSON-RPC error。
6. 服务端发送 `_mothx/session_event {event:"run_status", status:"..."}` 的终态投影。

`run_status` 与 `terminal` 是附加投影，不替代标准 prompt 响应。

### 7.4 提交队列

- 每个 `(databaseIdentity, sessionID)` 有 Runtime 拥有的 FIFO 提交队列。
- 同一时刻只有一个 Run。
- 等待中的 prompt 可被取消。
- `ExecutionRuntime.Completion` 是下一个 prompt 可以继续的权威信号。

---

## 8. Prompt 内容块

`session/prompt.params.prompt` 是 `contentBlock` 数组。

### 8.1 `text`

```json
{"type":"text","text":"hello"}
```

多个 text 块按顺序用换行连接。

### 8.2 `resource_link`

```json
{
  "type":"resource_link",
  "name":"notes.txt",
  "uri":"file:///home/me/project/notes.txt",
  "mimeType":"text/plain",
  "size":1234
}
```

规则：

- `name` 与 `uri` 必填。
- 仅允许 `file://` 或裸路径；拒绝其他 scheme 和非 localhost host。
- 相对路径按会话 cwd 解析。
- 文件必须是 workspace/additional roots 内的普通文件。
- `size` 只是提示；Runtime 会重新 stat，不信任过期大小。

### 8.3 `resource`

```json
{
  "type":"resource",
  "name":"notes.md",
  "mimeType":"text/markdown",
  "data":"IyBub3Rlcw=="
}
```

- `data` 为 base64；也接受 `data:` URI 前缀。
- 若 `data` 为空但 `uri` 存在，按 `resource_link` 的本地文件规则物化。
- 内容会进入 Runtime 统一附件/输入契约。

### 8.4 `image`

```json
{"type":"image","mimeType":"image/png","data":"iVBORw0KGgo..."}
```

- `data` 为 base64。
- 省略 `mimeType` 时默认 `image/png`。
- 支持 `data:` URI 前缀。

### 8.5 `audio`

```json
{"type":"audio","mimeType":"audio/wav","data":"UklGR..."}
```

规则同 `image`。

### 8.6 附件与制品的边界

- Prompt 内容进入 Runtime 的附件/输入契约，由 Runtime 负责校验、存储、会话关联、回放和 provider 转换。
- 工作目录里的文件不会自动成为“制品”。
- 生成制品只通过 Runtime 的 `publish_artifact` 或授权的 provider attachment resolver 产生。
- 客户端通过 `mothx/attachment/list` 查看元数据，通过 `mothx/attachment/fetch` 取字节。

---

## 9. session/update 通知

### 9.1 信封

```json
{
  "jsonrpc":"2.0",
  "method":"session/update",
  "params":{
    "sessionId":"...",
    "update":{
      "sessionUpdate":"agent_message_chunk",
      "messageId":"...",
      "content":{"type":"text","text":"Hello"}
    }
  }
}
```

### 9.2 update 类型

| `sessionUpdate` | 说明 |
|---|---|
| `agent_message_chunk` | 助手文本增量 |
| `agent_thought_chunk` | 思考/推理增量 |
| `user_message_chunk` | 用户消息/历史用户消息 |
| `tool_call` | 新工具调用 |
| `tool_call_update` | 工具状态/结果更新 |
| `plan` | 任务计划 |
| `usage_update` | 上下文/用量/成本 |
| `available_commands_update` | 可用命令与技能 |
| `config_option_update` | 完整配置项目录刷新 |
| `current_mode_update` | 当前模式变化 |
| `session_info_update` | 标题/时间/附加元数据变化 |
| `artifact` | 生成制品投影（MothX 扩展变体） |

### 9.3 文本与思考

```json
{"sessionUpdate":"agent_message_chunk","messageId":"acp_<session>_<prompt>_message_0","content":{"type":"text","text":"部分文本"}}
```

```json
{"sessionUpdate":"agent_thought_chunk","messageId":"acp_<session>_<prompt>_thought_0","content":{"type":"text","text":"推理片段"}}
```

`messageId` 在同一模型 turn 内稳定；工具调用之后的新模型 turn 会获得新的 `messageId`，以便客户端正确插入 transcript 位置。

### 9.4 `tool_call`

```json
{
  "sessionUpdate":"tool_call",
  "toolCallId":"call_123",
  "title":"read: path=/home/me/project/login.go",
  "kind":"read",
  "status":"pending",
  "rawInput":{"path":"/home/me/project/login.go"}
}
```

`kind` 映射：

| 工具 | kind |
|---|---|
| `read`、`ls` | `read` |
| `write`、`edit` | `edit` |
| `grep`、`find` | `search` |
| `bash` | `execute` |
| `plan` | `think` |
| 其他/MCP | `other` |

### 9.5 `tool_call_update`

开始执行：

```json
{
  "sessionUpdate":"tool_call_update",
  "toolCallId":"call_123",
  "title":"read: path=...",
  "kind":"read",
  "status":"in_progress",
  "rawInput":{"path":"..."}
}
```

完成：

```json
{
  "sessionUpdate":"tool_call_update",
  "toolCallId":"call_123",
  "title":"read: path=...",
  "kind":"read",
  "status":"completed",
  "content":[
    {"type":"content","content":{"type":"text","text":"file contents..."}},
    {"type":"content","content":{"type":"image","mimeType":"image/png","data":"..."}},
    {"type":"diff","path":"/home/me/project/a.go","oldText":"...","newText":"..."}
  ],
  "locations":[{"path":"/home/me/project/a.go"}],
  "rawOutput":{
    "content":"...",
    "diff":{"path":"...","oldText":"...","newText":"..."}
  }
}
```

失败：

```json
{
  "sessionUpdate":"tool_call_update",
  "toolCallId":"call_123",
  "status":"failed",
  "content":[{"type":"content","content":{"type":"text","text":"错误摘要"}}],
  "rawOutput":{"content":"错误摘要","errorInfo":{"code":"...","...":"..."}}
}
```

状态值：

- `pending`
- `in_progress`
- `completed`
- `failed`
- 托管工具/外部状态还可能投影为 `cancelled`、`expired`、`incomplete`

工具结果图片限制：

- 单张图片解码后最大 **2 MiB**。
- 单条 `tool_call_update` 最多 **4** 张。
- 超限图片不会静默丢失，而是降级为文本说明。

### 9.6 `plan`

```json
{
  "sessionUpdate":"plan",
  "entries":[
    {"content":"定位登录失败原因","priority":"medium","status":"in_progress"},
    {"content":"修复并补充测试","priority":"medium","status":"pending"}
  ],
  "_meta":{"mothx.dev":{"title":"修复计划","note":"可选说明"}}
}
```

### 9.7 `usage_update`

```json
{
  "sessionUpdate":"usage_update",
  "used":12345,
  "size":200000,
  "cost":{"amount":0.0123,"currency":"USD"},
  "_meta":{"mothx.dev":{"cacheRead":8000,"cacheWrite":1200,"totalInputTokens":10000}}
}
```

- `cost` 仅在已知且大于 0 时出现。
- 当 `usageCacheProjection` 已声明且已观察到输入 token 时，`_meta["mothx.dev"]` 携带会话累计 `cacheRead`、`cacheWrite`、`totalInputTokens`。
- 缓存命中率口径：`cacheRead / totalInputTokens`。
- 累计成本与缓存量会在加载/恢复会话时从权威历史重建。

### 9.8 `available_commands_update`

```json
{
  "sessionUpdate":"available_commands_update",
  "availableCommands":[
    {"name":"/systeminit","description":"Initialize project guidance","_meta":{"mothx.dev":{"kind":"command"}}},
    {"name":"/my-skill","description":"...","_meta":{"mothx.dev":{"kind":"skill"}}}
  ]
}
```

### 9.9 `config_option_update`

```json
{"sessionUpdate":"config_option_update","configOptions":["..."]}
```

携带完整刷新后的配置项目录。

### 9.10 `current_mode_update`

```json
{"sessionUpdate":"current_mode_update","currentModeId":"agent"}
```

### 9.11 `session_info_update`

```json
{
  "sessionUpdate":"session_info_update",
  "title":"Fix login bug",
  "updatedAt":"2026-11-11T10:00:00.123456789Z",
  "_meta":{"pinned":true,"projectId":"project-1"}
}
```

### 9.12 `artifact`

MothX 附加投影，用标准 `session/update` 信封承载：

```json
{
  "sessionUpdate":"artifact",
  "artifactId":"att_...",
  "filename":"report.md",
  "kind":"file",
  "mediaType":"text/markdown",
  "size":2048,
  "runId":"acp_20_...",
  "status":"generated"
}
```

- 制品由 Runtime 持久化后才投影。
- 加载/恢复会话时会重放已有制品。
- 字节内容通过 `mothx/attachment/fetch` 获取，不放在通知里。

---

## 10. 服务端发起的决策请求

MothX 在执行中需要用户批准或回答问题时，会发送带 `id` 的 JSON-RPC 请求。客户端必须在同一个连接上返回响应。

### 10.1 工具审批：`session/request_permission`

请求：

```json
{
  "jsonrpc":"2.0",
  "id":"acp-1",
  "method":"session/request_permission",
  "params":{
    "sessionId":"...",
    "toolCall":{
      "toolCallId":"call_123",
      "title":"bash",
      "kind":"execute",
      "status":"pending",
      "rawInput":{"command":"rm -rf build"}
    },
    "options":[
      {"optionId":"allow-once","name":"Allow once","kind":"allow_once"},
      {"optionId":"reject-once","name":"Reject","kind":"reject_once"}
    ]
  }
}
```

允许：

```json
{"jsonrpc":"2.0","id":"acp-1","result":{"outcome":{"outcome":"selected","optionId":"allow-once"}}}
```

拒绝：

```json
{"jsonrpc":"2.0","id":"acp-1","result":{"outcome":{"outcome":"selected","optionId":"reject-once"}}}
```

取消：

```json
{"jsonrpc":"2.0","id":"acp-1","result":{"outcome":{"outcome":"cancelled"}}}
```

规则：

- MothX 只提供 `allow-once` 与 `reject-once`，不会发明新的 option ID。
- 未选择、超时、连接取消或 `$/cancel_request` 都会被视为拒绝/取消。
- 默认超时 **5 分钟**，可用 `--permission-timeout` 或 `MOTHX_ACP_PERMISSION_TIMEOUT` 配置。
- 待处理决策会持久化；重连/恢复时会重新投影尚未解析的请求。
- 已解析或已过期的决策不会复活。

### 10.2 提问：标准 elicitation 或 MothX 扩展

MothX 根据 `initialize.clientCapabilities.elicitation.form` 选择协议。

#### 10.2.1 标准 `elicitation/create`

```json
{
  "jsonrpc":"2.0",
  "id":"acp-2",
  "method":"elicitation/create",
  "params":{
    "sessionId":"...",
    "message":"选择要使用的数据库",
    "mode":"form",
    "requestedSchema":{
      "type":"object",
      "properties":{
        "answer":{
          "type":"string",
          "title":"Answer",
          "description":"可选解释",
          "enum":["PostgreSQL","SQLite"]
        }
      },
      "required":["answer"]
    }
  }
}
```

接受：

```json
{"jsonrpc":"2.0","id":"acp-2","result":{"action":"accept","content":{"answer":"PostgreSQL"}}}
```

拒绝/取消：

```json
{"jsonrpc":"2.0","id":"acp-2","result":{"action":"decline"}}
```

服务端只把 `action=="accept"` 且 `content.answer` 非空的结果视为有效回答；其他 action 按取消处理。

#### 10.2.2 MothX 扩展 `mothx/requestQuestion`

未声明 form elicitation 的客户端会收到：

```json
{
  "jsonrpc":"2.0",
  "id":"acp-2",
  "method":"mothx/requestQuestion",
  "params":{
    "prompt":"选择要使用的数据库",
    "options":[
      {"id":"PostgreSQL","label":"PostgreSQL"},
      {"id":"SQLite","label":"SQLite"}
    ],
    "multi":false,
    "title":"MothX",
    "placeholder":"可选解释"
  }
}
```

回答：

```json
{"jsonrpc":"2.0","id":"acp-2","result":{"answer":"PostgreSQL"}}
```

也接受：

```json
{"answer":"PostgreSQL"}
{"answers":["PostgreSQL"]}
{"ok":false}
{"cancelled":true}
```

若回答不是候选 option 之一，决策仍会被记录为已解析，但上层不会收到任何匹配选项（等价于空选择）。

> 兼容说明：初始化前的旧式重放可能使用 `_mothx/request_question` 和旧的 `questionRequest` 载荷。正常已初始化的线协议客户端应使用 `mothx/requestQuestion`。

### 10.3 超时与截止提醒

- 审批默认超时：**5 分钟**。
- 提问默认超时：**5 分钟**。
- 超时后分别记录 `timed_out`。
- 如果声明了 `decisionDeadline`，服务端会发送：

```json
{
  "jsonrpc":"2.0",
  "method":"_mothx/session_event",
  "params":{
    "sessionId":"...",
    "event":"decision_deadline",
    "requestId":"acp-1",
    "kind":"approval",
    "deadline":"2026-11-11T10:05:00Z",
    "remainingMs":120000
  }
}
```

提醒时机：

- 第一次：`min(60s, timeout/2)`。
- 第二次：`timeout - 60s`，且必须晚于第一次。

### 10.4 恢复与重放

- 待处理审批/提问会持久化为 DecisionRecord。
- 会话加载/恢复时会重新发送未解析请求。
- 过期决策会被终结为 `timed_out`。
- 无法恢复的执行相关决策会被终结为 `cancelled`，而不是永久挂起。

---

## 11. `_mothx/session_event` 与扩展通知

### 11.1 `_mothx/session_event`

这是 MothX Runtime 事件的附加通知，信封固定：

```json
{"jsonrpc":"2.0","method":"_mothx/session_event","params":{"sessionId":"...","event":"...","...":"..."}}
```

事件类型：

| `event` | 附加字段 | 说明 |
|---|---|---|
| `run_status` | `runId`、`status` | 运行开始/结束/外部运行同步 |
| `terminal` | `status`、可选 `error`、`errorInfo` | 规范化终态；每个 Run 最多一次 |
| `retrying` | `message`、`attempt`、`maxAttempts`、`retryAfterMs` | provider 流重试 |
| `status` | `message` | 普通状态文本 |
| `turn_started` | `message` | 模型 turn 开始 |
| `turn_finished` | `message` | 模型 turn 结束 |
| `compaction_started` | `message` | 上下文压缩开始 |
| `compaction_finished` | `message` | 上下文压缩结束 |
| `decision_deadline` | `requestId`、`kind`、`deadline`、`remainingMs` | 决策截止提醒 |
| `subagent` | `agentId`、`status`、可选 lineage/member 字段 | 子 Agent 生命周期 |
| `workspace` | `cwd`、`additionalDirectories` | 工作区扩展 |
| `cron_completed` | `jobId`、`status`、可选 `sessionId` | 管理面 cron 完成 |

`run_status.status` 取值：`running`、`completed`、`failed`、`cancelled`、`incomplete`。

`terminal` 示例：

```json
{
  "jsonrpc":"2.0",
  "method":"_mothx/session_event",
  "params":{
    "sessionId":"...",
    "event":"terminal",
    "status":"failed",
    "error":"The run could not be completed.",
    "errorInfo":{
      "code":"...",
      "type":"...",
      "phase":"model",
      "retryable":true,
      "retryMode":"user"
    }
  }
}
```

### 11.2 `first-response-wins` 决策语义

- 审批/提问请求解析后，后续响应会被忽略。
- 决策记录以 request ID 为唯一标识。
- 关闭会话会把该 Run 的未决决策终结为 `cancelled`。

### 11.3 子 Agent 事件

```json
{
  "jsonrpc":"2.0",
  "method":"_mothx/session_event",
  "params":{
    "sessionId":"...",
    "event":"subagent",
    "agentId":"agent-1",
    "parentAgentId":"lead",
    "status":"started",
    "memberId":"member-1",
    "expertId":"team-a",
    "memberDisplayName":"Researcher",
    "memberEmoji":"🔎",
    "memberRole":"researcher"
  }
}
```

- 每个 agent ID 最多一次 `started` 和一次终态；`subagent` 事件的终态是 `completed` 或 `failed`（取消也投影为 `failed`）。`mothx/subagent/list` 的 `status` 则可能显示 `running`、`completed`、`failed`、`cancelled`、`incomplete`。
- 子 Agent 的文本/工具事件仍会出现在父会话 `session/update` 流中。
- 子 Agent 终态不会改变父 Run 的终态。

### 11.4 `mothx/worktree/status`

```json
{
  "jsonrpc":"2.0",
  "method":"mothx/worktree/status",
  "params":{
    "worktree":{
      "id":"wt-1",
      "name":"feature-a",
      "branch":"mothx/feature-a",
      "directory":"/home/me/project/.mothx/worktrees/feature-a",
      "status":"created",
      "error":""
    }
  }
}
```

---

## 12. MothX 会话与资源扩展方法

### 12.1 `mothx/doctor`

无需会话，但必须在 `initialize` 之后调用。

请求：

```json
{"jsonrpc":"2.0","id":30,"method":"mothx/doctor","params":{}}
```

可选：

```json
{"cwd":"/home/me/project"}
```

响应：

```json
{
  "ok": true,
  "version": "x.y.z",
  "summary": "All checks passed",
  "checks": [
    {"id":"provider.default","status":"ok","title":"Default provider","detail":"...","fix":""}
  ]
}
```

稳定检查 ID 包括 provider/model/cwd 等；不会返回 API key 原文。

### 12.2 `mothx/projects/*`

| 方法 | 请求 | 响应 |
|---|---|---|
| `mothx/projects/list` | `{}` | `{"projects":[{"id","name","createdAt","updatedAt","sessionCount"}]}` |
| `mothx/projects/create` | `{"name":"..."}` | `{"id","name","createdAt","updatedAt"}` |
| `mothx/projects/rename` | `{"id":"...","name":"..."}` | 项目对象 |
| `mothx/projects/delete` | `{"id":"..."}` | `{}` |

删除项目会把会话的项目绑定清空，但不会删除会话。

### 12.3 附件

#### `mothx/attachment/list`

```json
{"sessionId":"...","status":"generated"}
```

`status` 可选：

- 省略：全部
- `generated`：生成制品
- `input`：prompt 输入附件（持久化为 `accepted`）

响应：

```json
{
  "attachments":[
    {
      "attachmentId":"att_...",
      "filename":"report.md",
      "kind":"file",
      "mediaType":"text/markdown",
      "size":2048,
      "status":"generated",
      "runId":"acp_20_...",
      "createdAt":"2026-11-11T10:00:00.123456789Z"
    }
  ]
}
```

只返回该会话拥有的附件。

#### `mothx/attachment/fetch`

```json
{"sessionId":"...","attachmentId":"att_..."}
```

响应：

```json
{"filename":"report.md","mediaType":"text/markdown","size":2048,"contentBase64":"..."}
```

错误码：

| code | 说明 |
|---|---|
| `attachment_not_found` | 不存在或不属于该会话 |
| `attachment_expired` | 已过期 |
| `attachment_too_large` | 超过 10 MiB 获取上限 |
| `attachment_unavailable` | 存储/读取失败 |

### 12.4 子 Agent 面板

#### `mothx/subagent/list`

```json
{"sessionId":"..."}
```

响应：

```json
{
  "agents":[
    {
      "agentId":"agent-1",
      "parentAgentId":"lead",
      "memberId":"member-1",
      "expertId":"team-a",
      "memberDisplayName":"Researcher",
      "memberEmoji":"🔎",
      "memberRole":"researcher",
      "status":"running",
      "active":true,
      "messageCount":4,
      "lastResponse":"...",
      "error":"",
      "startedAt":"...",
      "updatedAt":"..."
    }
  ]
}
```

状态：`running`、`completed`、`failed`、`cancelled`、`incomplete`。

#### `mothx/subagent/messages`

```json
{"sessionId":"...","agentId":"agent-1"}
```

响应：

```json
{"agentId":"agent-1","updates":[{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"..."}}]}
```

未知子 Agent 返回 `-32004` 与 `subagent_not_found`。

### 12.5 `mothx/worker/messages`

只读投影 Router worker 消息和待处理问题。

```json
{"sessionId":"..."}
```

响应：

```json
{
  "sessionId":"...",
  "messages":[
    {
      "id":"...",
      "sessionId":"...",
      "childId":"...",
      "parentRunId":"...",
      "direction":"worker_to_lead",
      "kind":"status",
      "body":"...",
      "status":"delivered",
      "runId":"...",
      "createdAt":"..."
    }
  ],
  "pendingQuestions":[
    {
      "id":"...",
      "sessionId":"...",
      "runId":"...",
      "childId":"...",
      "question":"...",
      "options":["A","B"],
      "createdAt":"...",
      "expiresAt":"..."
    }
  ]
}
```

该接口不返回 bridge token 或 worker MCP 端点；回答 worker 问题仍通过 Runtime 工具执行。

### 12.6 Worktree

只有在 `features` 包含 `worktrees` 时使用。

#### `mothx/worktree/list`

```json
{"repositoryRoot":"/home/me/project","projectId":""}
```

响应：

```json
{
  "worktrees":[
    {
      "id":"wt-1",
      "name":"feature-a",
      "branch":"mothx/feature-a",
      "directory":"/home/me/project/.mothx/worktrees/feature-a",
      "repositoryRoot":"/home/me/project",
      "projectId":"...",
      "startCommand":"...",
      "status":"ready",
      "external":false,
      "createdAt":"...",
      "updatedAt":"..."
    }
  ]
}
```

#### `mothx/worktree/create`

```json
{
  "baseCwd":"/home/me/project",
  "name":"feature-a",
  "detached":false,
  "startCommand":"npm run dev",
  "projectId":"..."
}
```

响应：

```json
{"worktree":{ "...": "..." }}
```

#### `mothx/worktree/remove`

```json
{"id":"wt-1","directory":"...","force":false}
```

- 有活动运行时拒绝，返回 `worktree_in_use`。
- `force:true` 会先取消该目录下活动 Run。
- 响应：`{"removed":true}`。

#### `mothx/worktree/reset`

```json
{"id":"wt-1"}
```

响应：`{"reset":true}`。

---

## 13. 管理投影 `mothx/manage/*`

`mothx/manage/*` 是共享 Runtime/Serve 服务的附加投影，主要供 Desktop 使用。客户端必须根据对应 `manage*` feature key gate。

通用规则：

- 所有管理调用都要求已完成 `initialize`。
- 未知管理方法返回 `-32601` + `data.code = "manage_method_not_found"`。
- API key、provider token、SkillHub token、渠道 secret、环境变量值不会以原文返回或写入日志。
- 写入使用严格白名单；未知字段返回 `*_field_not_allowed`。
- 管理调用不会让 Desktop 成为会话、运行、凭证或调度状态的 owner。

### 13.1 应用与设置

#### `mothx/manage/settings/get`

```json
{
  "defaultProvider":"deepseek",
  "defaultModel":"deepseek-v4-flash",
  "defaultMode":"yolo",
  "thinkingLevel":"medium",
  "providers":[
    {
      "name":"deepseek",
      "maskedKey":"sk-***abc",
      "baseUrl":"https://api.deepseek.com",
      "modelCount":3,
      "apiKeyConfigured":true,
      "isDefault":true,
      "usageCount":12,
      "lastUsedAt":"..."
    }
  ],
  "sandboxEnabled":false,
  "sandboxLevel":"standard",
  "webSearchEnabled":false,
  "skillsDisabled":[],
  "memoryEnabled":true
}
```

#### `mothx/manage/settings/patch`

```json
{
  "patch":{
    "defaultProvider":"deepseek",
    "defaultModel":"deepseek-v4-flash",
    "defaultMode":"agent",
    "thinkingLevel":"high",
    "providerKey":{"name":"deepseek","key":"sk-..."},
    "providerBaseUrl":{"name":"deepseek","url":"https://api.deepseek.com"},
    "sandboxEnabled":true,
    "webSearchEnabled":false
  }
}
```

可写字段白名单：

`defaultProvider`、`defaultModel`、`defaultMode`、`thinkingLevel`、`providerKey`、`providerBaseUrl`、`sandboxEnabled`、`webSearchEnabled`。

- `providerKey` / `providerBaseUrl` 的值是 `{name, key|url}` 对象；`key`/`url` 为 `null` 时拒绝，用空字符串清空。
- 响应为刷新后的 settings 视图。

#### `mothx/manage/application/get` / `patch`

`get` 返回 settings.json 中安全的应用子集：

```json
{
  "defaults":{
    "defaultMode":"yolo",
    "enablePlanTool":false,
    "enableArtifact":true,
    "enableACPArtifact":false,
    "authored":true,
    "updateCheck":true
  },
  "contextFiles":{"enabled":true,"extraFiles":[]},
  "compaction":{"enabled":true,"reserveTokens":0,"keepRecentTokens":0,"tokenizer":"","tokenizerModel":"","template":""},
  "toolExecution":{"mode":"parallel","maxConcurrency":4},
  "webSearch":{"enabled":false,"backend":"","provider":"","baseUrl":"","tokenConfigured":false},
  "imageGeneration":{"enabled":false,"provider":"","apiType":"","baseUrl":"","model":"","tokenConfigured":false},
  "retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1000},
  "statusLine":{"enabled":false,"type":"","command":"","padding":0,"refreshInterval":0,"timeoutMs":0,"fallback":false},
  "sandbox":{"enabled":false,"level":"none","bwrapPath":"","allowNetwork":false,"allowedRead":[],"allowedWrite":[],"deniedPaths":[],"tmpSize":"","protectGit":false},
  "approval":{"bashWhitelist":[],"bashBlacklist":[],"confirmBeforeWrite":false}
}
```

`patch`：

```json
{
  "patch":{
    "defaults":{"defaultMode":"agent"},
    "compaction":{"enabled":true},
    "sandbox":{"enabled":true,"level":"standard"},
    "webSearch":{"enabled":true,"backend":"deepseek","token":"..."}
  }
}
```

支持 section：`defaults`、`contextFiles`、`compaction`、`toolExecution`、`webSearch`、`imageGeneration`、`retry`、`statusLine`、`sandbox`、`approval`。

- 未列出的 section/字段拒绝。
- 响应为更新后的完整 application 视图。
- 会同步到当前运行的会话 Runtime，无需重启。

#### `mothx/manage/env/get` / `patch`

`get` 只返回变量名和是否配置：

```json
{"variables":[{"name":"OPENAI_API_KEY","valueConfigured":true}]}
```

`patch`：

```json
{
  "set":[{"name":"FOO","value":"bar"}],
  "unset":["OLD_VAR"]
}
```

- 同一名字不能同时出现在 `set` 和 `unset`。
- 响应为更新后的变量名列表。
- 值永远不会回显。

### 13.2 Serve 与渠道

#### `mothx/manage/serve/get` / `patch`

`get` 返回 serve.json 的安全视图：

- `api`: listen/defaultMode/defaultThinkingLevel/enableSubAgents/enableDelegate/enableWorkflows/enableWebSearch/enableBrowser/enableArtifact/enableA2AMaster/toolVisibility/systemPromptMode/requestTimeoutSeconds/backgroundRunMaxSeconds/maxConcurrentRequests/logLevel/session
- `features`: webUI/openAIAPI/multiAgent/cron/memory
- `webUI`: enabled/dir
- `cron`: enabled/interval
- `memory`: enabled/path
- `security`: smartApprovals
- `agent`: maxTurns/budgetPressure/contextPressure/thresholds/run timeouts
- `lobsterMode`

`patch` 信封：

```json
{"patch":{"api":{"defaultMode":"agent"},"features":{"cron":true}}}
```

可写顶层 section：`api`、`features`、`webUI`、`cron`、`memory`、`security`、`agent`、`lobsterMode`。

不会返回 auth token、CORS 来源、允许工作目录、hook 命令或 provider secret。

#### `mothx/manage/channels/get` / `patch`

`get`：

```json
{
  "artifact":true,
  "wechat":{"enabled":true,"workDir":"...","autoTyping":true,"credentialConfigured":true},
  "feishu":{"enabled":true,"workDir":"...","appIDConfigured":true,"appSecretConfigured":true}
}
```

`patch`：

```json
{
  "patch":{
    "artifact":true,
    "wechat":{"enabled":true,"workDir":"...","autoTyping":true,"credPath":"...","clearCredPath":false},
    "feishu":{"enabled":true,"workDir":"...","appId":"...","appSecret":"...","clearAppId":false,"clearAppSecret":false}
  }
}
```

- `credPath` 与 `clearCredPath` 互斥。
- `appId`/`appSecret` 与对应 clear 字段互斥。
- 响应只显示 `*Configured` 布尔值，不回显 secret。

### 13.3 Provider

#### `mothx/manage/providers/list`

```json
{
  "providers":["..."],
  "providerConfigs":[
    {
      "id":"deepseek",
      "provider":{"vendor":"...","baseUrl":"...","models":["..."],"api":"...","thinkingFormat":"...","cacheControl":"...","maxImagesPerRequest":0,"responses":{}},
      "maskedKey":"sk-***abc",
      "apiKeyConfigured":true,
      "isDefault":true,
      "globalOverride":true
    }
  ],
  "models":[
    {"id":"deepseek-v4-flash","name":"DeepSeek V4 Flash","provider":"deepseek","input":["text"],"reasoning":true,"contextWindow":200000,"maxTokens":8192}
  ],
  "defaultProvider":"deepseek",
  "defaultModel":"deepseek-v4-flash",
  "modelDefaults":{"reasoning":false,"contextWindow":0,"maxTokens":0,"input":["text"]}
}
```

#### `mothx/manage/providers/save`

```json
{
  "id":"deepseek",
  "previousId":"",
  "provider":{
    "vendor":"openai",
    "baseUrl":"https://api.deepseek.com",
    "api":"chat",
    "models":[{"id":"deepseek-v4-flash","name":"DeepSeek V4 Flash","reasoning":true,"contextWindow":200000,"maxTokens":8192,"input":["text"]}]
  },
  "apiKey":"sk-..."
}
```

- `provider` 可写字段：`vendor`、`baseUrl`、`httpProxy`、`forceHTTP11`、`headers`、`api`、`thinkingFormat`、`cacheControl`、`maxImagesPerRequest`、`responses`、`models`。
- `apiKey` 省略表示保留当前 secret；显式空字符串表示清空。
- `previousId` 用于重命名全局 provider overlay。
- 响应为刷新后的 provider 目录。

#### `mothx/manage/providers/delete`

```json
{"id":"deepseek"}
```

- 当前默认 provider 不允许删除：`provider_default_in_use`。
- 预设 provider 删除 overlay 后恢复内置默认；自定义 provider 会消失。
- 响应为刷新后的 provider 目录。

#### `mothx/manage/providers/discover`

```json
{
  "api":"openai",
  "baseUrl":"https://api.example.com",
  "apiKey":"...",
  "httpProxy":"",
  "forceHTTP11":false,
  "headers":{"X-Tenant":"a"}
}
```

- 网络请求超时 30s。
- 响应：`{"models":[...]}`。
- 仅返回草稿，必须再通过 `providers/save` 保存。
- 该方法是异步管理方法，不阻塞读循环。

#### `mothx/manage/providers/test`

```json
{"provider":"deepseek","model":"deepseek-v4-flash"}
```

- 执行 1-token ping，超时 5s。
- 成功：`{"ok":true,"provider":"...","model":"...","latencyMs":123}`。
- 失败：`{"ok":false,"provider":"...","model":"...","error":"..."}`。
- 不返回 key 材料。

### 13.4 技能与 MCP

#### `mothx/manage/skills/list`

```json
{"cwd":"/home/me/project"}
```

响应：

```json
{"cwd":"/home/me/project","skills":[{"name":"deploy","description":"...","source":"project","enabled":true}]}
```

#### `mothx/manage/skills/set`

```json
{"cwd":"/home/me/project","name":"deploy","enabled":false}
```

响应：

```json
{"name":"deploy","enabled":false,"skillsDisabled":["deploy"]}
```

实时应用到当前进程的技能管理器。

#### `mothx/manage/mcp/list`

```json
{"scope":"global","sessionId":""}
```

- `scope`: `global`（默认）或 `project`。
- `project` scope 必须提供活动会话 `sessionId`。
- 响应：

```json
{
  "scope":"global",
  "sessionId":"",
  "path":"/home/me/.mothx/mcp.json",
  "servers":[
    {
      "name":"my-db",
      "type":"stdio",
      "command":"mcp-server",
      "args":["--port","8080"],
      "env":[{"name":"DB_URL","value":"..."}],
      "enabled":true
    }
  ]
}
```

#### `mothx/manage/mcp/set`

```json
{
  "scope":"global",
  "sessionId":"",
  "servers":[
    {"name":"my-db","type":"stdio","command":"mcp-server","args":[],"env":[],"enabled":true}
  ]
}
```

- **整体替换**目标 scope 的 server 列表；未出现的 server 会被删除。
- 字段白名单：`name`、`type`、`command`、`args`、`url`、`messageUrl`、`enabled`、`headers`、`env`。
- `type` 必须是 `stdio`、`http`、`sse`。
- `stdio` 必须有 `command`；`http`/`sse` 必须有 `url`。
- 保存后会重连受影响的会话，响应包含可选的 `reloadedSessions`。

### 13.5 自动化、统计、记忆、投递

#### Cron

| 方法 | 请求 | 说明 |
|---|---|---|
| `mothx/manage/cron/list` | `{}` | 返回 `{enabled, running, jobs}` |
| `mothx/manage/cron/create` | `{name,schedule,prompt,mode?,enabled?}` | 返回 `{job}` |
| `mothx/manage/cron/update` | `{id,...部分字段}` | 返回 `{job}` |
| `mothx/manage/cron/remove` | `{id}` | 返回 `{id,deleted:true}` |
| `mothx/manage/cron/run` | `{id}` | 立即触发；返回 `{ok:true,jobId,triggered:true}` |

- 可写字段：`name`、`schedule`、`prompt`、`mode`、`enabled`。
- `mode` 只能是 `agent` 或 `yolo`。
- `schedule` 使用共享 cron 语法并归一化。
- 完成任务会广播 `_mothx/session_event {event:"cron_completed"}`。
- 维护任务不会出现在用户可见列表。

#### 统计

`mothx/manage/stats/summary`：

```json
{"from":"2026-10-01","to":"2026-11-11","group":"day"}
```

响应：

```json
{
  "sessions":12,
  "runs":34,
  "tokens":{"input":1000,"output":2000,"total":3000},
  "cost":0.0,
  "since":"...",
  "until":"..."
}
```

`mothx/manage/stats/timeseries`：

```json
{"from":"2026-10-01","group":"day"}
```

响应：

```json
{"group":"day","points":[{"date":"2026-10-01","runs":4,"tokens":1200,"cost":0.0}],"from":"...","to":"..."}
```

- `group`: `day`、`1h`、`week`、`month`。
- 时间可使用 `YYYY-MM-DD` 或 RFC3339。

#### 记忆

`mothx/manage/memory/get`：

```json
{"content":"...","path":"/home/me/.mothx/memory.md","source":"global","size":123,"updatedAt":"..."}
```

`mothx/manage/memory/put`：

```json
{"content":"# Memory\n..."}
```

- 内容上限 1 MiB。
- 响应：`{size,updatedAt,path,source}`。
- 路径来自 serve 配置或默认全局 memory.md。

#### 投递

`mothx/manage/deliveries/list`：

```json
{"sessionId":"","limit":50}
```

响应：

```json
{
  "deliveries":[
    {
      "operationId":"...",
      "intentId":"...",
      "sessionId":"...",
      "runId":"...",
      "platform":"wechat",
      "targetId":"...",
      "operationKind":"...",
      "status":"failed",
      "failureCode":"...",
      "attemptCount":3,
      "updatedAt":"...",
      "retryable":true
    }
  ],
  "count":1
}
```

`mothx/manage/deliveries/retry`：

```json
{"operationId":"..."}
```

- 仅 `failed` 且 `retryable=true` 的操作可重试。
- 响应：`{operationId,retried}`。

### 13.6 SkillHub

所有 SkillHub catalogue 方法都要求 `sessionId` 指向活动会话，因为安装目标必须由 Runtime 工作目录决定。

| 方法 | 请求关键字段 | 说明 |
|---|---|---|
| `mothx/manage/skillhub/get` | `{}` | 返回市场、默认市场、默认安装 scope、official handles（token 只显示是否配置） |
| `mothx/manage/skillhub/patch` | `{patch:{...}}` | 修改 SkillHub 全局设置 |
| `mothx/manage/skillhub/markets` | `{sessionId}` | 返回市场列表与默认市场 |
| `mothx/manage/skillhub/categories` | `{sessionId,market}` | 返回分类 |
| `mothx/manage/skillhub/official` | `{sessionId,market?,query,page,limit}` | SkillHub.cn 官方推荐 |
| `mothx/manage/skillhub/search` | `{sessionId,market?,query,page,limit,cursor,sort,order,category}` | 搜索技能 |
| `mothx/manage/skillhub/detail` | `{sessionId,market?,id}` | 技能详情 |
| `mothx/manage/skillhub/targets` | `{sessionId}` | 返回可安装目录 |
| `mothx/manage/skillhub/installed` | `{sessionId}` | 返回已安装技能与当前会话状态 |
| `mothx/manage/skillhub/install` | `{sessionId,market?,id,version?,scope,targetDir,overwrite?,activate?}` | 安装技能 |
| `mothx/manage/skillhub/activate` | `{sessionId,id}` | 激活技能 |
| `mothx/manage/skillhub/uninstall` | `{sessionId,market,id,scope}` | 卸载 |

`install` 要求：

- `id` 与 `targetDir` 必填。
- `targetDir` 必须是绝对路径。
- `scope` 必须是 `project` 或 `global`。
- `market` 默认为配置的默认市场。
- 成功响应包含 `install`、`activated`、`session`。

`search`/`categories`/`official`/`detail` 是读网络的管理方法，会异步执行以避免阻塞读循环。

### 13.7 专家（Expert / Expert Team）

| 方法 | 请求 | 响应 |
|---|---|---|
| `mothx/manage/experts/list` | `{cwd?,scope?}` | `{scope,cwd,experts,effectiveExperts}` |
| `mothx/manage/experts/get` | `{cwd?,scope?,name}` | `{scope,cwd,bundle}` |
| `mothx/manage/experts/create` | `{cwd?,scope,bundle}` | `{scope,cwd,bundle}` |
| `mothx/manage/experts/update` | `{cwd?,scope,bundle}` | `{scope,cwd,bundle}` |
| `mothx/manage/experts/delete` | `{cwd?,scope,name}` | `{deleted:true,scope,cwd,name}` |

- `scope`: `global`（默认）或 `project`。
- project scope 需要 cwd，且会在协商窗口内校验。
- bundle 结构由 Expert 域管理；写入不会绕过 bundle 校验。
- 切换一个非空 Expert 到另一个必须通过 fork 完成，而不是静默替换。

### 13.8 知识库

| 方法 | 请求 | 说明 |
|---|---|---|
| `mothx/manage/knowledge-bases/list` | `{}` | 返回 `{knowledgeBases}` |
| `mothx/manage/knowledge-bases/get` | `{id}` | 返回单个知识库视图 |
| `mothx/manage/knowledge-bases/create` | `{knowledgeBase}` | 创建 |
| `mothx/manage/knowledge-bases/update` | `{id,knowledgeBase}` | 更新 |
| `mothx/manage/knowledge-bases/delete` | `{id}` | 删除配置与索引，不删除源目录 |
| `mothx/manage/knowledge-bases/scan` | `{id}` | 后台启动索引 |
| `mothx/manage/knowledge-bases/status` | `{id}` | 等价于 get |
| `mothx/manage/knowledge-bases/query` | `{id,query,limit?}` | 有界证据查询 |
| `mothx/manage/knowledge-bases/runs/list` | `{id,limit?}` | 索引 Run 历史 |
| `mothx/manage/knowledge-bases/runs/get` | `{id,runId}` | 单个索引 Run |
| `mothx/manage/knowledge-bases/clear` | `{id}` | 清除活动索引但保留配置/源目录 |
| `mothx/manage/knowledge-bases/sources/list` | `{id}` | 活动快照的文件级来源 |
| `mothx/manage/knowledge-bases/schedule` | `{id,enabled?}` | 读取/暂停/恢复调度 |
| `mothx/manage/knowledge-bases/mcp/apply` | `{id,enabled?}` | 写入标准 Knowledge MCP server |

`knowledgeBase` 字段：

```json
{
  "name":"Docs",
  "rootDir":"/home/me/docs",
  "preprocessProfile":"default",
  "provider":"deepseek",
  "model":"deepseek-v4-flash",
  "mode":"agent",
  "thinkingLevel":"medium",
  "schedule":"0 3 * * *",
  "enabled":true,
  "ignoreGlobs":["node_modules/**"]
}
```

知识库视图：

```json
{
  "knowledgeBase":{ "...": "..." },
  "snapshot":{ "...": "..." },
  "status":"completed",
  "indexing":{
    "running":true,
    "phase":"scanning",
    "filesTotal":120,
    "filesDone":30,
    "chunks":200,
    "startedAt":"...",
    "runId":"...",
    "error":""
  }
}
```

`status` 直接投影活动快照的 `snapshot.status`；没有活动快照时为 `unindexed`。索引进行中的实时进度单独在 `indexing` 中返回。

`scan` 立即返回：

```json
{"started":true,"alreadyRunning":false,"id":"...","status":"indexing","indexing":{...}}
```

`query`：

```json
{"id":"...","query":"login error","limit":8}
```

- `limit` 默认 8，最大 20。
- 响应：`{"query":{...}}`，包含有界证据与引用。

`mothx/manage/knowledge-bases/mcp/apply` 写入全局 MCP server：

```json
{"id":"kb-1","enabled":true}
```

响应：

```json
{"id":"kb-1","name":"knowledge-kb-1","enabled":true}
```

服务端自动生成：

```json
{
  "name":"knowledge-kb-1",
  "type":"stdio",
  "command":"<mothx executable>",
  "args":["knowledge-mcp","serve","--knowledge-base","kb-1"],
  "enabled":true
}
```

### 13.9 Router 与 ACP profiles

#### Router

| 方法 | 请求 | 响应 |
|---|---|---|
| `mothx/manage/router/state` | `{sessionId}` | `{enabled,mode,primaryProfile?,profileIds?}` |
| `mothx/manage/router/activate` | `{sessionId,profileIds:[...]}` | Router binding 投影 |
| `mothx/manage/router/deactivate` | `{sessionId}` | Router binding 投影 |

- `activate` 把会话绑定到外部 ACP worker，并进入 Router 模式。
- 第一个 profile 是主 worker。
- 会话必须空闲；激活由共享 Runtime 执行 idle fence。
- `deactivate` 回到普通只读 plan surface，但保留持久化 binding。
- 成功会广播 `current_mode_update` 与 `config_option_update`。
- 投影不包含 argv、环境变量或凭证。

#### `mothx/manage/acp/profiles/list`

```json
{
  "profiles":[
    {
      "id":"codex",
      "title":"Codex",
      "transport":"stdio",
      "model":"",
      "endpoint":"",
      "enabled":true,
      "routerEligible":true,
      "permissionPolicy":"auto",
      "maxConcurrentChildren":1,
      "maxConcurrentChildrenEnforced":1,
      "driver":"generic-acp",
      "mcpBridge":"auto",
      "capabilityOverrides":[]
    }
  ],
  "capabilityNames":["..."]
}
```

不返回 command、环境变量名或本地路径；远程 profile 的 `endpoint` 会去掉 query/fragment/用户信息。

#### `mothx/manage/acp/profiles/patch`

```json
{
  "id":"codex",
  "driver":"...",
  "model":"...",
  "mcpBridge":"auto|off",
  "capabilityOverrides":{"someCapability":false}
}
```

- 只允许编辑行为元数据。
- 不能通过该接口修改 argv、URL、环境、trust flag 或授权。
- `capabilityOverrides` 只能收紧（值必须为 false）。
- 响应：`{"profiles":[...]}`。

---

## 14. MCP 集成

### 14.1 配置位置

- CLI/TUI/Serve：启动时读取 `mcp.json`。
- ACP：客户端可在 `session/new`、`session/load`、`session/resume`、`session/fork` 的 `mcpServers` 中传入。
- 管理面可读写全局 `mcp.json` 或项目级 `mcp.json`。

### 14.2 server 配置

```json
{
  "mcpServers": [
    {
      "name": "my-database",
      "type": "stdio",
      "command": "mcp-server",
      "args": ["--port", "8080"],
      "env": [{"name": "DB_URL", "value": "postgres://localhost/mydb"}],
      "enabled": true
    },
    {
      "name": "remote-tools",
      "type": "http",
      "url": "https://mcp.example.com",
      "headers": [{"name": "Authorization", "value": "Bearer ${TOKEN}"}]
    },
    {
      "name": "legacy-sse",
      "type": "sse",
      "url": "https://legacy.example.com/sse",
      "messageUrl": "https://legacy.example.com/messages"
    }
  ]
}
```

支持传输：

- `stdio`：`command` + `args`
- `http`：streamable HTTP，`url`
- `sse`：旧式 SSE，`url` + `messageUrl`

同一会话内 server `name` 必须唯一。

### 14.3 工具与资源注册

MCP server 连接后：

- `tools/*` 注册为 `mcp_<sanitized_server>_<tool>`。
- `resources/*` 注册为 MCP 资源读取工具。
- `prompts/*` 注册为 MCP Prompt 渲染工具。

注册发生在 Agent 冻结当前会话 system prompt 与工具定义之前。之后修改 MCP 配置，需要通过 `mothx/manage/mcp/set` 触发重连，或创建/加载新会话。

### 14.4 MCP 回调

- `sampling/createMessage`：桥接到当前会话 provider/model，返回 assistant 文本内容；超时 90s。
- `notifications/progress`、`notifications/message`、`logging/message`、`notifications/cancelled`：投影为 `tool_call_update`。
- 每个 MCP server 使用合成工具调用 ID `mcp-notify-<server>`，标题 `mcp_notification: <server>`，kind `other`。

### 14.5 MCP 失败语义

- 会话创建/加载时 MCP 连接失败会导致该生命周期操作失败。
- 管理面重连是 best-effort：失败不会让管理调用失败，会通过会话告警面投影。
- 管理面 `mcp/set` 采用整体替换，不允许重复 server 名。

---

## 15. 错误模型

### 15.1 标准 JSON-RPC 错误码

| code | 含义 |
|---|---|
| `-32700` | 解析错误 |
| `-32600` | 无效请求 / 未初始化 / 重复初始化 |
| `-32601` | 方法不存在 |
| `-32602` | 参数无效 |
| `-32000` | 通用服务端错误 |
| `-32002` | 会话不存在 / provider mismatch（按方法语义） |
| `-32004` | 子 Agent 不存在 |

### 15.2 结构化错误

MothX 结构化错误在 `error.data` 中携带稳定机器码：

```json
{
  "jsonrpc":"2.0",
  "id":1,
  "error":{
    "code":-32602,
    "message":"cwd is required",
    "data":{"code":"invalid_params","field":"cwd","allowed":["..."]}
  }
}
```

规则：

- `error.code` 是 JSON-RPC 层错误码。
- `error.data.code` 是稳定业务码，客户端应优先使用它。
- `error.data` 可能包含 `field`、`allowed`、`fix`、`errorInfo`、大小限制等附加信息。
- provider/模型错误还会带 `errorInfo`：`code`、`type`、`phase`、`retryable`、`retryMode`、`messageKey` 等。

### 15.3 常用业务码

| code | 说明 |
|---|---|
| `invalid_params` | 参数缺失/格式错误 |
| `settings_unavailable` | 设置读取失败 |
| `settings_field_not_allowed` | settings patch 字段不在白名单 |
| `settings_field_invalid` | settings 字段值非法 |
| `settings_save_failed` | 设置保存失败 |
| `provider_not_found` | provider 不存在 |
| `provider_default_in_use` | 默认 provider 不能删除 |
| `provider_not_custom` | provider 没有可删除的全局 overlay |
| `provider_field_invalid` | provider 字段非法 |
| `provider_discovery_failed` | provider 模型发现失败 |
| `provider_test_failed` | provider 探测失败 |
| `provider_unknown` / `provider_unusable` / `model_unknown` | 启动期 provider/model 不可用 |
| `config_invalid` / `cwd_invalid` | 启动配置/工作目录不可用 |
| `unknown_session` | 会话不在当前进程打开 |
| `session_not_found` | 持久化会话不存在 |
| `session_meta_unavailable` | 会话元数据不可用 |
| `projects_unavailable` / `project_not_found` | 项目服务/项目不存在 |
| `unauthorized` | 目标不在协商窗口内 |
| `workspace_directory_invalid` | 工作区扩展路径非法 |
| `workspace_directory_unavailable` | 工作区扩展目录不可用 |
| `workspace_forbidden` | 会话/目标工作区不允许 |
| `workspace_limit_exceeded` | 附加目录超过上限 |
| `attachment_not_found` / `attachment_expired` / `attachment_too_large` / `attachment_unavailable` | 附件错误 |
| `subagent_invalid_params` / `subagent_not_found` | 子 Agent 投影错误 |
| `worker_messages_unavailable` | worker 消息投影不可用 |
| `worktrees_disabled` / `worktrees_unavailable` / `worktree_not_found` / `worktree_in_use` / `worktree_failed` | worktree 错误 |
| `manage_method_not_found` | 未知管理方法 |
| `manage_unavailable` | 管理服务不可用 |
| `skills_unavailable` / `skill_not_found` | 技能错误 |
| `mcp_scope_invalid` / `mcp_server_invalid` / `mcp_field_not_allowed` / `mcp_unavailable` | MCP 管理错误 |
| `cron_unavailable` / `cron_job_not_found` / `cron_field_invalid` / `cron_mode_invalid` / `cron_schedule_invalid` / `cron_job_running` / `cron_run_failed` | Cron 错误 |
| `stats_unavailable` / `stats_group_invalid` / `stats_time_invalid` | 统计错误 |
| `memory_unavailable` / `memory_too_large` | 记忆错误 |
| `deliveries_unavailable` / `delivery_not_found` / `delivery_not_reopenable` / `delivery_not_retryable` / `delivery_retry_failed` | 投递错误 |
| `skillhub_*` | SkillHub 错误 |
| `expert_*` | Expert 错误 |
| `knowledge_base_*` | 知识库错误 |
| `env_*` | 环境变量管理错误 |
| `router_activation_failed` / `router_deactivation_failed` | Router 错误 |
| `serve_config_unavailable` / `serve_config_save_failed` / `serve_field_invalid` / `serve_field_not_allowed` | Serve 配置错误 |
| `application_section_not_allowed` / `application_field_invalid` / `application_field_not_allowed` | Application settings 错误 |

### 15.4 运行失败

模型/工具运行失败时：

```json
{
  "jsonrpc":"2.0",
  "id":20,
  "error":{
    "code":-32000,
    "message":"The run could not be completed.",
    "data":{
      "code":"...",
      "type":"...",
      "phase":"model",
      "retryable":true,
      "retryMode":"user",
      "errorInfo":{ "...": "..." }
    }
  }
}
```

安全边界：

- 错误消息经过脱敏，不回显 API key、provider 原始响应或敏感 header。
- 工具类错误会带阶段信息（如 `phase:"tool"`）。

---

## 16. 完整交互示例

下面展示一次从握手到工具审批、工具执行、最终回答的最小完整流。

### 16.1 initialize

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"elicitation":{"form":{}}},"_meta":{"mothx":{"workspace":{"cwd":"/home/me/project"}}}}}
```

```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"promptCapabilities":{"image":true,"audio":true,"embeddedContext":true},"sessionCapabilities":{"close":{},"delete":{},"list":{},"resume":{},"fork":{},"additionalDirectories":{}},"mcpCapabilities":{"http":true,"sse":true},"_meta":{"mothx.dev":{"features":["..."]}}},"agentInfo":{"name":"mothx","title":"MothX","version":"x.y.z"},"authMethods":[]}}
```

### 16.2 新建会话

```json
{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/home/me/project"}}
```

```json
{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s1","modes":{"currentModeId":"yolo","availableModes":[]},"configOptions":[]}}
```

随后：

```json
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"available_commands_update","availableCommands":[{"name":"/systeminit"}]}}}
```

### 16.3 prompt

```json
{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"s1","prompt":[{"type":"text","text":"删除 build 目录"}]}}
```

运行开始：

```json
{"jsonrpc":"2.0","method":"_mothx/session_event","params":{"sessionId":"s1","event":"run_status","runId":"acp_3_...","status":"running"}}
```

文本增量：

```json
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","messageId":"...","content":{"type":"text","text":"我将执行删除。"}}}}
```

工具调用：

```json
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"call_1","title":"bash: rm -rf build","kind":"execute","status":"pending","rawInput":{"command":"rm -rf build"}}}}
```

审批请求：

```json
{"jsonrpc":"2.0","id":"acp-1","method":"session/request_permission","params":{"sessionId":"s1","toolCall":{"toolCallId":"call_1","title":"bash","kind":"execute","status":"pending","rawInput":{"command":"rm -rf build"}},"options":[{"optionId":"allow-once","name":"Allow once","kind":"allow_once"},{"optionId":"reject-once","name":"Reject","kind":"reject_once"}]}}
```

客户端批准：

```json
{"jsonrpc":"2.0","id":"acp-1","result":{"outcome":{"outcome":"selected","optionId":"allow-once"}}}
```

工具开始/完成：

```json
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"call_1","title":"bash: rm -rf build","kind":"execute","status":"in_progress","rawInput":{"command":"rm -rf build"}}}}
```

```json
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"call_1","title":"bash: rm -rf build","kind":"execute","status":"completed","content":[{"type":"content","content":{"type":"text","text":"removed"}}],"rawOutput":{"content":"removed"}}}}
```

最终文本与用量：

```json
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","messageId":"...","content":{"type":"text","text":"已删除 build 目录。"}}}}
```

```json
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"usage_update","used":1234,"size":200000,"cost":{"amount":0.001,"currency":"USD"}}}}
```

终态事件：

```json
{"jsonrpc":"2.0","method":"_mothx/session_event","params":{"sessionId":"s1","event":"terminal","status":"completed"}}
```

prompt 响应：

```json
{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn"}}
```

```json
{"jsonrpc":"2.0","method":"_mothx/session_event","params":{"sessionId":"s1","event":"run_status","runId":"acp_3_...","status":"completed"}}
```

### 16.4 取消

```json
{"jsonrpc":"2.0","id":4,"method":"session/cancel","params":{"sessionId":"s1"}}
```

```json
{"jsonrpc":"2.0","id":3,"result":{"stopReason":"cancelled"}}
```

```json
{"jsonrpc":"2.0","id":4,"result":{}}
```

---

## 17. 客户端实现清单

### 17.1 必须实现

1. 以子进程方式启动 `mothx acp`，stdin/stdout 走 NDJSON。
2. 持续读取 stdout，按 `id` 关联响应；同时处理无 `id` 的通知和带 `id` 的反向请求。
3. 发送 `initialize`，校验 `protocolVersion == 1`，保存 `agentInfo` 与 `features`。
4. 通信前用绝对路径协商 `workspace.cwd`；后续会话 cwd 必须保持一致。
5. 使用 `session/new` 或 `session/load` 打开会话。
6. 发送 `session/prompt`，渲染 `session/update` 流。
7. 响应 `session/request_permission` 和提问请求（`elicitation/create` 或 `mothx/requestQuestion`）。
8. 支持 `session/cancel` 与 `$/cancel_request`。
9. 在退出前关闭/删除会话，或等待 stdin EOF 清理。

### 17.2 推荐实现

- 按 `features` 动态显示可选 UI，而不是假设扩展永远存在。
- 为 `agent_message_chunk` / `agent_thought_chunk` 按 `messageId` 分组，保持工具调用前后的文本顺序。
- 把 `tool_call` / `tool_call_update` 按 `toolCallId` 归并成一张卡片。
- 支持 `artifact` + `mothx/attachment/fetch` 的制品查看。
- 用 `_mothx/session_event` 的 `run_status`/`terminal` 维护左侧运行指示器，但以 prompt 响应/错误为最终状态。
- 处理 `decision_deadline`，在审批/提问上显示倒计时。
- 对 `session/load` 的完整历史重放去重，避免与本地缓存重复渲染。

### 17.3 不要做

- 不要把 stdout 当日志通道。
- 不要在未初始化时发送会话方法。
- 不要假设 `session/load` 永远有 `history` 字段；默认模式是通知重放。
- 不要凭 `session/list` 结果直接读取会话文件；通过 ACP 方法。
- 不要自行扫描工作目录推断“制品”；只使用 `artifact` 投影与附件接口。
- 不要发明权限 option ID；只回传服务端提供的 `optionId`。
- 不要用 `session/cancel` 的响应猜测 Run 终态；以 prompt 响应/`terminal` 为准。

### 17.4 幂等与重连

- `session/close` 对未知会话幂等。
- `session/delete` 对不存在会话幂等。
- `session/fork` 需要 `requestId` 保证重试幂等。
- 重连后重新 `session/load`/`session/resume`，服务端会重放待处理决策与已生成制品。
- 客户端重连后不应复用旧连接上的反向请求 ID；服务端会重新分配。

---

## 18. 附录：MothX 作为 ACP 客户端

MothX 自身也实现了 ACP 客户端，用于 Router 模式连接外部 ACP Agent（例如 Codex 或其他兼容 Agent）。

配置在 `settings.json` 的 `acpAgents` 中：

```json
{
  "acpAgents": [
    {
      "id": "codex",
      "title": "Codex",
      "command": ["codex", "acp"],
      "model": "",
      "transport": "stdio",
      "url": "",
      "enabled": true,
      "environment": ["CODEZ_HOME=/home/me/.codex"],
      "permissionPolicy": "auto",
      "terminal": false,
      "maxConcurrentChildren": 1,
      "allowParallelBaseWorkspaceWrites": false,
      "driver": "generic-acp",
      "mcpBridge": "auto",
      "capabilityOverrides": {}
    }
  ]
}
```

字段：

| 字段 | 说明 |
|---|---|
| `id` | 稳定 profile ID；模型只会看到这个 ID |
| `title` | 展示名 |
| `command` | stdio 子进程 argv |
| `model` | 外部 Agent 自己的模型 ID，不是 MothX provider/model |
| `transport` | 空/`stdio`，或 `websocket`/`http` |
| `url` | 远程 transport 必填 |
| `enabled` | 是否可用 |
| `environment` | 追加环境变量 |
| `permissionPolicy` | 外部 Agent 权限策略 |
| `terminal` | 显式允许客户端 terminal bridge |
| `maxConcurrentChildren` | 并发 worker 上限，默认 1 |
| `allowParallelBaseWorkspaceWrites` | 显式允许并行写同一基础工作区 |
| `driver` | ACP 方言驱动；只描述协议差异，不增加授权 |
| `mcpBridge` | `auto` / `off`；是否注入 loopback worker bridge |
| `capabilityOverrides` | 只能把能力收紧为 false |

客户端侧行为：

- 启动外部 Agent 子进程，完成 `initialize`，协商 ACP v1。
- 使用 `session/new`、`session/load`、`session/resume` 创建或恢复 worker 会话。
- 通过 `session/set_config_option` 或旧式 `session/set_model` 选择 worker 模型。
- 发送 `session/prompt` 并消费 `session/update`。
- 处理 worker 发起的 `session/request_permission`。
- 可选处理 `fs/read_text_file`、`fs/write_text_file`、`terminal/create`、`terminal/output`、`terminal/wait_for_exit`、`terminal/kill`、`terminal/release`。
- 支持远程 transport 的 streamable HTTP 与 WebSocket。
- 外部 worker 永远不是 MothX 会话/运行/凭证状态的 owner；它只是被委派执行的 ACP Agent。

Router 模式的会话级选择和激活通过 `mothx/manage/router/*` 投影；worker 只读消息通过 `mothx/worker/messages` 投影。

---

## 参考实现位置

- 服务端协议与 handler：`internal/acp/acp.go`、`internal/acp/extensions.go`、`internal/acp/manage*.go`、`internal/acp/worktrees.go`
- ACP 客户端与传输：`internal/acp/client/`
- 会话配置项：`internal/agentruntime/session_options.go`
- 共享 Runtime / Run / Decision / 附件 / 知识库：`internal/agentruntime/`
- MCP 协议服务：`internal/mcp/`
- 现有面向用户文档：`docs/zh/acp.md`、`docs/en/acp.md`

协议变更原则：新增能力必须通过 `initialize._meta["mothx.dev"].features` 可发现；保留标准 ACP v1 语义；不把适配器私有状态变成新的权威事实。
