# Router Worker MCP Bridge 与 ACP Worker 适配层方案

> 状态：Implemented（Phase 0–3 代码完成；2026-11-10 已对 OpenCode 与 codex-acp 做真实 interop 验证，未发现 generic-acp 无法描述的协议缺口，因此未新增 flavor）
> 日期：2026-11-10
> 关联：[`acp-client-external-subagent-proposal.md`](./acp-client-external-subagent-proposal.md)、[`agent-core-runtime-unification-proposal.md`](./agent-core-runtime-unification-proposal.md)、[`acp-session-model-config-options-proposal.md`](./acp-session-model-config-options-proposal.md)、[`multi-agent-architecture-plan.md`](./multi-agent-architecture-plan.md)
> 参照实现：`/home/free/src/t3code`（Orchestration V2 的 MCP orchestrator、ProviderAdapter、capability/policy 降级）

> 实施状态（2026-11-10）：Phase 0/1/2/3 代码已完成并有自动化覆盖；`mcpBridge`/`capabilityOverrides`、`mothx doctor`、双语文档、TUI/WebUI/Desktop 只读 worker 视图与 profile 编辑均已完成。真实 interop 已用 OpenCode 2.0.16（`opencode acp`）和 codex-acp 0.16.0 验证：两者都完成 `initialize`、`session/new` 并连上 Runtime 的 loopback MCP bridge（`tools/list`）；OpenCode 还通过 `worker_mgr(action=report, kind=finding)` 写入了真实 finding。两者均未观察到 generic-acp 无法表达的协议差异，因此没有新增未经验证的 flavor，默认仍为 `generic-acp`。2026-11-11 补齐了三个边界：JSONL 按 `(sessionDir, sessionID)` 隔离并在 session 删除时级联清理；bridge 注册/listener 启动失败会把 resolved capability 与 child metadata 降级为 `mcpBridge=unavailable` 并记录 degradation；bridge HTTP handler 显式拒绝非 loopback `RemoteAddr`。同一轮还让 `delegate_worker_mgr` 的模型可见摘要由 resolved capability 投影生成（含 bridge 可用性、remote/off、`capabilityOverrides`、attachments/write/terminal/resume），并新增 bridge 准备失败、`DeleteSessionWithMutation` 级联与摘要派生测试；5.3/5.8 已把 token 生命周期（按 remote session 而非单 turn）与 `child_terminal` 的 MCP tool error 形状对齐实现。2026-11-11 第二轮补齐了跨入口闭环：worker `initialize` 完成后由 `acpChildProxy.recordCapabilities` 立即 republish Router target，且每次 `BuildAgent` 冻结 prompt 前都会按最新广告重算；摘要为附件/resume 的“未广告”和 write/terminal 的 `capabilityOverrides` 降级给出可操作原因；Serve/WebUI 会话在打开时启用 Runtime mailbox wake，并把唤醒 Run 的 canonical Run 事件、工具/transcript、审批/提问与 `done` 投影到既有 SSE，空闲 lead 可被 worker finding/question 唤醒。

### 真实 interop 验证记录（2026-11-10）

| agent | 版本 / 命令 | 结果 |
|---|---|---|
| OpenCode | 2.0.16 / `opencode acp` | `initialize`、`session/new` 成功；MCP `initialize`/`tools/list` 成功；`worker_mgr(context)` 与 `worker_mgr(report finding)` 成功，`worker_messages.jsonl` 记录到 `interop-ok`。 |
| codex-acp | 0.16.0 / `codex-acp`（`@zed-industries/codex-acp`） | `initialize`、`session/new` 成功；MCP `initialize`/`tools/list` 成功；该环境的上游模型传输超时，因此只验证到 bridge handshake，未验证完整 turn。 |

复现命令：

```bash
MOTHX_REAL_ACP_INTEROP=opencode \
  go test -run TestRealACPBridgeInterop -v ./internal/agentruntime

MOTHX_REAL_ACP_INTEROP=codex MOTHX_REAL_ACP_INTEROP_HANDSHAKE_ONLY=1 \
  MOTHX_CODEX_ACP=/path/to/codex-acp CODEX_HOME=/path/to/codex-home \
  go test -run TestRealACPBridgeInterop -v ./internal/agentruntime
```

OpenCode 的默认 provider 凭据在该测试环境不可用，因此测试通过 ACP `session/set_config_option` 选择一个本地可用的模型；这是测试夹具行为，不是 Runtime 的 model 选择策略。测试默认跳过，只有显式设置 `MOTHX_REAL_ACP_INTEROP` 才运行，符合“真实 interop 不进默认 CI”的约束。

测试从 bridge 请求体统计 `worker_mgr` action，并在 finding 到达后要求 `contextCalls>0`；因此 `worker_mgr(context)` 不再是人工旁证，而是与 `worker_mgr(report finding)` 一起被自动断言。2026-11-10 复跑 `MOTHX_REAL_ACP_INTEROP=opencode` 得到 `contextCalls=1 reportCalls=1`。Bridge 的 `ask` 只由 Runtime 的 `askWorkerQuestion` 写入 JSONL（含 `decisionId`），bridge handler 不再重复写一行；answered/timeout 各只产生一条 question 记录并折叠为 `delivered`/`expired`。

## 1. 摘要

Router 模式目前只有两种 worker → lead 的信息通道：

1. worker 的 ACP `session/update` 被投影成 canonical child event，只用于 UI/transcript；
2. worker 终止时生成一条 `MemberCompletion` 进入 session mailbox，lead 在迭代边界 drain，或在 idle 时由 mailbox wake 开新 Run。

这带来两个缺口：

- **运行中的 worker 无法主动向 lead 投递信息**：进度、关键发现、需要 lead 决策的问题，只能等 worker 结束时一次性返回；worker 也无法提问，因为 Router 故意不暴露 `subagent_answer`，ACP permission 又由 profile 策略自动答复。
- **worker 的能力差异没有统一建模**：`agentCapabilities` 目前以 `map[string]any` 在 `acp_worker_remote.go` / `acp_worker_bridge.go` 里零散读取，profile、initialize 协商、Runtime 策略三层没有一份显式的 resolved capability，新增一个 ACP Agent 只能靠读代码猜它支持什么。

本方案借鉴 t3code 的 MCP 注入、typed capability 与 policy 降级，但不引入它的服务端 command/event/outbox 编排层。最终形状是：

1. **Router 侧一套统一签名**：只注册 `delegate_worker_mgr`，用 `action` 覆盖 delegate/spawn/status/list/send/wait/destroy/integrate/answer；本地会话继续使用现有的 `delegate_subagent` / `subagent_*`。
2. **Worker 侧一个 MCP 工具**：`worker_mgr`，用 `action` 覆盖 context/report/ask；MCP server 只通过 loopback HTTP 暴露。
3. **能力层**：`WorkerCapabilities` + `WorkerAdapter`，把不同 ACP agent 的差异收敛为协商结果与显式降级，不在业务代码里按 agent 名字分支。
4. **注册模型**：借鉴 t3code 的 driver/instance 分层——`acpAgents` profile 是实例配置，`driver` 是 ACP flavor，`WorkerCapabilities` 是解析后的能力；只做 ACP，不做 Codex/Claude/Cursor 等原生协议 driver。
5. **消息持久化**：worker 消息与投递状态写入 session 目录下的 append-only JSONL，不新增 SQLite 消息表。

## 2. 现状与问题

### 2.1 当前 worker → lead 路径

```text
ACP session/update
  -> acp_worker_remote.go 解码
  -> acpChildProxy 投影为 child event（AgentID = child）
  -> 父 Run 只在工具结果 / mailbox completion 时看到 worker 的结论
```

相关位置：

- `internal/agentruntime/acp_child.go`：`acpChildProxy.Run` 缓存 `result.Output`，`finishRun` 只发一个 terminal event。
- `internal/agent/subagent.go`：`memberNotifier.notify` 在 spawn 监控 goroutine 结束时入 mailbox。
- `internal/agent/mailbox.go`：`MemberCompletion` 只有 completion / question 两种 kind。
- `internal/agentruntime/mailbox_wake.go`：任一 pending mailbox item 都可能唤醒空闲 lead。
- `internal/agentruntime/acp_worker_decisions.go`：`session/request_permission` 由 profile 策略即时答复，不进入 lead 上下文。

### 2.2 当前工具面

`delegate_subagent` / `subagent_spawn` 同时服务本地子 Agent、专家团成员和外部 ACP worker，schema 在 Router 模式下被动态重写：

- `delegate_subagent` 的 Router schema 仍列出 `mode` / `work_dir` / `worktree` / `tools` / `max_iterations`，描述写着“Rejected for an external Router worker”，模型会看到注定失败的参数；
- 本地 schema 又列出 `agent` / `role` / `read_scope` / `write_scope` / `expected_deliverables` / `verification` 等 Router 专用字段；
- Router 的工具是 `delegate_subagent` + `subagent_spawn` + `subagent_status/send/wait/destroy/integrate` 的零散组合，没有一个完整的 worker 管理签名。

### 2.3 当前能力协商

`acp_worker_remote.go` / `acp_worker_bridge.go` 直接用 map key 探测：

```go
supportsSessionResume(conn.init.AgentCapabilities)
supportsSessionLoad(conn.init.AgentCapabilities)
acpPromptCapability(capabilities, "image")
supportsSessionClose(capabilities)
supportsAdditionalDirectories(capabilities)
```

问题：

- 没有一份可持久化、可测试、可展示的 resolved capability；
- profile 的静态信任（`terminal`、`permissionPolicy`）与 agent 实际广告的能力没有合并规则；
- 新 agent 的特殊行为（MCP transport 偏好、permission option 形状、configOptions 命名）没有落点；
- 模型看不到 worker 能力，容易在 attachment、write 任务上做错误假设。

## 3. 设计原则

1. **Runtime 唯一所有权**：bridge server、token、消息日志、问题决策、能力解析全部由 `internal/agentruntime` 拥有；`internal/acp` / `internal/serve/openaiapi` / `internal/tui` 只投影。
2. **adapter 只做协议与差异**：不读 `acpAgents`，不拥有 session/child 生命周期，不写文件/数据库。
3. **显式能力，不猜**：能力只来自 profile 静态声明、`initialize` 广告、Runtime 策略三者的交集；缺能力时走命名降级或明确拒绝，不静默假装支持。
4. **消息不是控制面**：worker 只能 context/report/ask，**不能** spawn、destroy、send 其他 worker、改 mode、写 Run 状态。
5. **完成语义不变**：ACP prompt 结束仍是唯一 terminal；report/ask 不结束 turn、不产生新的父 Run。
6. **可审计**：worker 发来的文本一律标记来源为 `worker-reported`，与现有 `docs/en/tools.md` 的 evidence 分类一致。
7. **不引入第二套编排**：不复制 t3code 的 thread/run/command/outbox；child 仍是 AgentManager child。
8. **一套签名**：同一入口的同类操作只通过一个工具名暴露，不用零散工具拼装；action 的必填组合由 Runtime 校验。

## 4. Router 工具面：`delegate_worker_mgr`

### 4.1 为什么收成一个工具

Router 的 worker 生命周期是一个完整闭环：

```text
delegate (blocking) ─┐
spawn (async) ───────┼─> status/list
                     ├─> send (follow-up)
                     ├─> wait
                     ├─> integrate (change set)
                     ├─> answer (worker question)
                     └─> destroy
```

这些操作共享同一个 child handle、同一套权限、同一份状态。拆成多个工具会让模型在“该用哪个”上做额外判断，也让 schema 分散。因此 Router 只注册一个 `delegate_worker_mgr`，用 `action` 区分。

本地会话不受影响：`delegate_subagent` / `subagent_spawn` / `subagent_status` / `subagent_answer` / `subagent_destroy` / `subagent_wait` 保持现有语义，且不再携带 Router 专用字段。

### 4.2 Schema

```json
{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": [
        "delegate", "spawn", "status", "list", "send",
        "wait", "destroy", "integrate", "answer"
      ],
      "description": "delegate=阻塞执行一个任务；spawn=异步创建；status/list=读取；send=追加一轮；wait=等待；destroy=关闭；integrate=应用变更集；answer=回答 worker 问题"
    },
    "task": { "type": "string", "description": "delegate/spawn 必填：目标、范围、期望产物、停止条件" },
    "agent": { "type": "string", "description": "delegate/spawn 必填：绑定的 ACP profile ID" },
    "role": { "type": "string", "enum": ["investigate", "implement", "test", "review", "integrate"] },
    "read_scope": { "type": "array", "items": { "type": "string" } },
    "write_scope": { "type": "array", "items": { "type": "string" } },
    "expected_deliverables": {
      "type": "array",
      "items": { "type": "string", "enum": ["findings", "changed-files", "test-evidence", "review"] }
    },
    "verification": { "type": "string" },
    "stop_conditions": { "type": "array", "items": { "type": "string" } },
    "system_prompt_extra": { "type": "string" },
    "attachments": { "type": "array", "items": { "type": "string" } },
    "handle": { "type": "string", "description": "status/send/destroy 必填；wait 可只传 handle" },
    "handles": { "type": "array", "items": { "type": "string" }, "description": "wait 可传多个 handle" },
    "message": { "type": "string", "description": "send 必填：追加给空闲 worker 的一轮消息" },
    "timeout_seconds": { "type": "integer", "description": "wait 可选；Runtime 有上限" },
    "changes": {
      "type": "array",
      "description": "integrate 必填：要应用的 worker 变更集",
      "items": {
        "type": "object",
        "properties": {
          "handle": { "type": "string" },
          "digest": { "type": "string" }
        },
        "required": ["handle", "digest"]
      }
    },
    "question_id": { "type": "string", "description": "answer 必填" },
    "answer": { "type": "string", "description": "answer 必填" }
  },
  "required": ["action"]
}
```

`agent` 由 Runtime 投影为 `const`（单 worker）或有序 `enum`（多 worker）；未绑定的 profile 只出现在纠错描述里。

### 4.3 Action 契约

| action | 必填 | 行为 | 返回 |
|---|---|---|---|
| `delegate` | `task`, `agent` | 阻塞一个 bounded worker turn，复用现有 `delegate_subagent` 执行路径 | `{status, handle, result, evidence}` |
| `spawn` | `task`, `agent` | 异步创建，立即返回 handle | `{handle, status}` |
| `status` | `handle` | 读一个 worker 状态 | 单个状态 |
| `list` | 无 | 列出本 session 的 worker（含已完成） | 列表 |
| `send` | `handle`, `message` | 向空闲 worker 的同一 remote session 追加一轮 | `{status, result}` |
| `wait` | `handle` 或 `handles` | 等待指定 worker 终态或活动 | 摘要，不返回全文 |
| `destroy` | `handle` | 关闭 remote session 并释放 permit | `{status}` |
| `integrate` | `changes` | 走现有不可变 change-set + 独占 lane | `{status, applied, evidence}` |
| `answer` | `question_id`, `answer` | 解析 worker question 的 DecisionService 记录 | `{status}` |

约束：

- `delegate` / `spawn` 不接受 `mode`、`work_dir`、`worktree`、`tools`、`max_iterations`；worker 的 mode、工具、工作目录由 profile 与 Runtime 决定。
- `spawn` / `delegate` 的 `agent` 必须属于本 session 的 bound allowlist；错误信息直接给出 accepted IDs 与默认值。
- `wait` 的 `timeout_seconds` 只影响等待，不取消 worker。
- `integrate` 的多选是 all-or-nothing，复用现有回滚语义。
- `answer` 只能由 lead 调用；不接受来自其他 session/run 的 question_id。

### 4.4 实现与注册

- 新增 `internal/agent/worker_mgr_tool.go`：`Name() = "delegate_worker_mgr"`，`Parameters()` 由 Runtime 注入 `agent` schema；`Execute` 按 action 分派到**现有内部方法**，不复制 child runner。`answer` 通过 Runtime 安装的 `WorkerQuestionResolver` 回调解析（与 `ChangeSetIntegrator` 相同的注入方式），不让 `internal/agent` 反向依赖 `agentruntime`。
- 本地工具移除 Router 分支：`delegate_subagent` 的 `Parameters` 删除 `agent/role/read_scope/write_scope/expected_deliverables/verification/attachments` 与“Rejected”参数；`subagent_spawn` 同步删除 `attachments`。
- `internal/agentruntime/tool_sync.go`：Router 模式移除全部本地 subagent 工具，只注册 `delegate_worker_mgr`；`publishRouterTarget` 只作用于该工具。
- `internal/tools/tool.go` 的 `ModeTools` `case "router"`：只保留只读工具 + `delegate_worker_mgr`。
- `RouterSubAgentToolNames()` / `RouterOnlySubAgentToolNames()` 收敛为一个名字；保留函数名以减少调用点改动，内容改为 `{"delegate_worker_mgr"}`。
- 工具结果统一带 `action` 与 `status`，便于投影与测试。

### 4.5 为什么不拆成多个工具

- **共享状态**：handle、权限、生命周期、evidence 是同一份；拆分会让描述与参数重复。
- **模型选择成本**：Router 的只读工具面已经很窄，一个管理工具比“delegate + status + send + wait + destroy + integrate + answer”更容易解释。
- **注册一致性**：`SynchronizeToolGroups`、`publishRouterTarget`、`ModeTools` 只需处理一个名字。
- **风险与缓解**：mega-schema 可能让模型填错 action 字段。缓解方式是 action 枚举 + 每个 action 的必填校验 + 结构化错误（错误信息直接点名缺少的字段），并在评测里覆盖 9 个 action。若实测准确率不足，再把 `delegate`/`spawn` 与 `status/send/wait/destroy/integrate/answer` 拆成两个工具；这是可回退的。

## 5. Worker MCP Bridge

### 5.1 拓扑

```text
SessionRuntime (lead)
  └── AgentManager
        └── acpChildProxy (child)
              └── ACP session
                    └── external agent process
                          └── MCP client
                                │  POST /mcp  (Authorization: Bearer <child token>)
                                ▼
                        WorkerBridgeServer (Runtime-owned, 127.0.0.1:0)
                          ├── token registry: token -> child scope
                          ├── worker_mgr(context|report|ask)
                          └── JSONL message log
```

`WorkerBridgeServer` 由顶层进程装配拥有（TUI/CLI 一个，`mothx serve` 一个共享给所有 session，`mothx acp` 一个共享给其 session），通过 `BuildOptions.WorkerBridge` 注入 `SessionRuntime`。不按 session 起 listener，也不使用包级全局状态。

### 5.2 传输、端口与多进程

**只做 loopback HTTP MCP，不做 stdio、不做 SSE、不做 public URL。**

- 监听：`net.Listen("tcp4", "127.0.0.1:0")`。端口由内核分配，**不使用固定端口**，因此同一台机器上的多个 mothx 进程（TUI、serve、ACP Desktop、多个测试实例）不会冲突；每个进程把自己的实际端口写进本进程 worker 的 `session/new|load|resume`。
- 地址固定为数字 `127.0.0.1`，不使用 `localhost`，避免解析到 `::1`；只接受 loopback `RemoteAddr`。
- 每个进程最多一个 listener；由顶层装配创建并在退出时关闭。`SessionRuntime.Shutdown` 只撤销自己 session 的 token，不关闭共享 listener。
- 懒启动：第一个 Router worker 创建时启动；最后一个 child 结束后进入 idle，超过 `workerBridgeIdleTimeout`（默认 5 分钟）关闭；再次需要时重新绑定新端口。
- 端口资源：`tcp4` 临时端口范围由内核管理；idle 关闭避免长期占用。`mothx doctor` 检查 listener 可绑定、可访问、只监听 loopback。
- 容器/网络命名空间：如果 worker 不在宿主 loopback（例如远程 ACP、独立 netns），bridge 不可达；这种情况直接标记 `mcpBridge=unreachable`，不注入、不降级到 stdio。
- HTTP 细节：`POST /mcp`，`Content-Type: application/json`，响应 `application/json`；支持 `initialize` / `tools/list` / `tools/call`，不支持 server-initiated notifications。`Accept: text/event-stream` 时返回 `406` 或普通 JSON（按实现选择，文档写明），不做 SSE。

### 5.3 注入与生命周期

`internal/acp/client/protocol.go` 的 `NewSessionParams` / `LoadSessionParams` / `ResumeSessionParams` 已有 `mcpServers []any`，但 `acp_worker_remote.go` 目前不填。方案：

```go
type ACPWorkerInvocation struct {
    ...
    MCPServers []any // Runtime-built, already authorized
}
```

三处调用都传入：

```go
conn.newSession(ctx, acpclient.NewSessionParams{
    Cwd: ..., AdditionalDirectories: ..., MCPServers: invocation.MCPServers,
}, handler, notify)
```

payload：

```json
{
  "type": "http",
  "name": "mothx-worker",
  "url": "http://127.0.0.1:<port>/mcp",
  "headers": [
    { "name": "Authorization", "value": "Bearer <opaque-token>" }
  ]
}
```

- 仅对本地 stdio profile 注入；remote transport（websocket/http）默认不注入。
- token 每 child 32 字节随机数，只存内存；绑定 `(sessionID, childID, parentRunID, profileID, capabilities)`。token 的生命周期是 child 的 remote session，而不是单个 turn：turn 结束后 token 保留，scope 进入 `terminal` 状态，后续 bridge 调用返回 `child_terminal`；下一轮 `send`/follow-up 开始时会重新解除 terminal 状态，因此同一 remote session 可以继续使用同一 token。`destroy`、child/worker close、session shutdown 或进程退出时撤销；remote session 重新 resume 时签发并注入新 token。**不写入 settings、session 文件、child_runs、日志、JSONL。**
- remote session 可 resume 时重新签发 token，并通过 `session/load|resume` 重新注入。
- MCP 的 `tools/call` 错误是应用层 tool error，不是 HTTP 状态：post-terminal 调用返回 HTTP `200` + JSON-RPC result 中 `isError=true`，文本以 `child_terminal` 开头；不投递、不写 JSONL。
- bridge 失败不影响任务：没启动、worker 不连、401、406 都只记录 `mcpBridge: unavailable`，prompt 继续。

### 5.4 Worker 侧工具：`worker_mgr`

只暴露一个 MCP 工具，与 Router 侧的“一套签名”对齐：

```json
{
  "name": "worker_mgr",
  "inputSchema": {
    "type": "object",
    "properties": {
      "action": { "type": "string", "enum": ["context", "report", "ask"] },
      "kind": { "type": "string", "enum": ["notice", "progress", "finding"] },
      "text": { "type": "string" },
      "dedupe_key": { "type": "string" },
      "options": { "type": "array", "items": { "type": "string" } },
      "timeout_seconds": { "type": "integer" }
    },
    "required": ["action"]
  }
}
```

| action | 必填 | 行为 |
|---|---|---|
| `context` | 无 | 返回任务合同、工作目录、bridge 能力（report/ask/wake 策略） |
| `report` | `kind`, `text` | 写入 JSONL；按 kind 决定投递与唤醒 |
| `ask` | `text` | 创建 DecisionService 问题；阻塞直到 lead 回答、超时或取消 |

约束：

- 作用域由 token 决定，参数里没有 `childId`，worker 无法指定其他 child。
- body 上限：`report` 8 KiB、`ask` 4 KiB；`options` ≤ 8；每 child 默认 20 条/分钟。
- `dedupe_key` 在 `(session, child, key)` 上唯一；重复返回同一 message id。
- 不提供 spawn/send/destroy/steer/run_* 控制面。

### 5.5 投递与唤醒

| kind | JSONL | 进入 lead 上下文 | 唤醒空闲 lead | 叫醒 `wait` |
|---|---|---|---|---|
| `notice` | 是 | 否 | 否 | 否 |
| `progress` | 是（合并/限流） | 否 | 否 | 否 |
| `finding` | 是 | 是（下个迭代边界 steering） | **是** | 是 |
| `question` | 是 | 是（`[WORKER_QUESTION]`） | 是 | 是 |
| completion | 是 | 是 | 是 | 是 |

实现：

- `MemberCompletion` 增加 `Wake bool`（completion/question 默认 `true`，保持现有行为）。
- `MemberMailbox` 增加 `HasWakingPending()`；`mailbox_wake.go` 只在存在 `Wake=true` 的 item 时开 Run。
- `Enqueue` 的 activity signal 按 `Wake` 区分：`notice`/`progress` 不叫醒 `subagent_wait`；`finding`、`ask`、completion 叫醒。
- `finding` 唤醒空闲 lead 可能形成风暴：按 child 合并（同一 child 的 pending finding 只保留最新一段），并复用 mailbox wake 的 debounce（默认几百毫秒到 1 秒）。
- `HasPending()` 保持“有任意未消费消息”的语义，供 `status`/UI 读取；只有 wake 路径用 `HasWakingPending()`。

### 5.6 `worker_mgr(action=ask)` 与回答

1. Runtime 创建 `DecisionRequest{Kind: DecisionQuestion}`，`RunID` 绑定 worker 的 parentRunID，并写入 durable `DecisionRecord`（复用 replay/deadline/first-response-wins）。
2. 同时入 mailbox 一条 `MemberItemQuestion`（`MemberID = child handle`）。
3. MCP 调用阻塞，直到 lead 回答、决策过期或 worker 被取消。
4. lead 用 `delegate_worker_mgr(action=answer, question_id, answer)` 回答；`answer` 直接调用 `DecisionService.Resolve`。
5. 超时（默认 10 分钟，上限 30 分钟，Runtime 策略可调）返回 `{status:"cancelled", reason:"timeout"}`；worker 自行决定继续或结束。
6. 回答幂等：重复回答返回已提交的 resolution。
7. **只有 lead 能回答**；UI 只做只读投影，不提供回答入口。

> 不复用 `subagent_answer`：它属于本地成员问题注册表，权限与生命周期不同层。合并会让 `internal/agent` 反向依赖 Runtime。

### 5.7 持久化：session 目录下的 JSONL

worker 消息与投递状态写入 `<sessionDir>/worker-messages/<sessionID>/worker_messages.jsonl`，append-only，不新增 SQLite 表。每个 session 一个私有目录，因此不同 session 不会共享 dedupe 索引、append 句柄或 compaction 目标。

- 路径由 `SessionRuntime` 解析并持有；`internal/dao` 不参与（这不是 SQL 存储）。session ID 必须是单一路径组件，显式 ID 不能逃逸 Runtime 私有目录。
- 单写者：bridge handler 不直接写文件，统一交给 Runtime 的 `WorkerMessageLog`；同一 session 的并发 opener 复用同一个日志实例，内部用互斥/单 goroutine 串行 append，`O_APPEND|O_CREATE|O_WRONLY`，每条一行 JSON。
- 记录是事件式的，读时折叠出当前状态：

```json
{"op":"created","id":"wm_...","sessionId":"...","childId":"...","parentRunId":"...","direction":"worker_to_lead","kind":"finding","body":"...","dedupeKey":"...","decisionId":null,"createdAt":"..."}
{"op":"delivered","id":"wm_...","at":"...","runId":"..."}
{"op":"observed","id":"wm_...","runId":"..."}
{"op":"expired","id":"wm_...","at":"..."}
{"op":"cancelled","id":"wm_...","at":"...","reason":"..."}
```

- 读取：逐行扫描，忽略最后一行不完整记录（崩溃安全）；按 `id` 折叠状态；`(session, child, dedupeKey)` 去重；读取时再次按 `sessionId` 过滤，复制/损坏文件不能投影其他 session 的消息。
- 清理：session 删除时级联删除该 session 的 `worker-messages/<sessionID>` 目录；fork 默认不复制 worker 消息（新 session 从空开始）；文件超过阈值（如 8 MiB）时可做一次原子 compaction（临时文件 + rename），保留最后状态。
- `session_child_runs` 仍保存 child metadata（capabilities / degradations / mcpBridge），不把消息塞进 `transcript_json`。
- 阻塞问题的权威仍是 `DecisionRecord`（SQLite）：JSONL 存问题/回答投影与 evidence，`decisionId` 关联；不把 pending decision 的 replay 逻辑复制到 JSONL。若后续希望完全脱离 SQLite，再单独评估把 worker decision 也迁到 JSONL。

### 5.8 失败与降级

| 场景 | 行为 |
|---|---|
| worker 不支持/忽略 `mcpServers` | prompt 正常执行；child metadata `mcpBridge=unavailable` |
| bridge 启动失败/端口不可用 | 记录日志，按 unavailable 继续；不阻塞 spawn |
| token 过期/撤销 | `401`；worker 收到 tool error，可继续任务 |
| worker 在 terminal 之后调用 | MCP tool error `child_terminal`（HTTP `200` + `isError=true`）；不投递 |
| lead 已结束且 finding 到达 | 落 JSONL；唤醒空闲 lead（新 Run） |
| ask 时 lead 长时间不回答 | 超时取消；decision terminalize；worker 继续 |
| 进程重启 | token 全部失效；JSONL 可重放；child session 可 resume 时重新注入新 token |
| remote ACP profile | 不注入；能力显示 `mcpBridge=unreachable` |
| 容器/独立 netns | 同 remote，`unreachable` |

### 5.9 安全与限额

- loopback + bearer + 每 child 作用域；请求里没有可枚举的 session/child 参数。
- body、options、rate、并发 ask 数均有上限（默认每 child 1 个 pending ask）。
- 日志只记录 `sessionID/childID/tool/结果码/字节数`，不记录 token、question 原文、report 原文。
- bridge 不注册到 `mcp.json`、不出现在 lead 的 MCP server 列表、不能被其他 session 复用。
- `mothx doctor` 增加 bridge 自检：`tcp4` loopback 绑定、401/406 行为、token 不落盘、idle 关闭。

## 6. ACP Worker 适配层

### 6.1 从 t3code 拿什么、不拿什么

| t3code 做法 | 本方案 |
|---|---|
| `ProviderAdapterV2` 统一 open/start/steer/interrupt/respond/fork | 借鉴为 **WorkerAdapter**，但只覆盖 ACP worker 的差异面 |
| typed `OrchestrationV2ProviderCapabilities` | 借鉴为 typed `WorkerCapabilities` |
| capability → policy 降级（steering/fork/subagent） | 借鉴为 ACP 的 resume/load、MCP transport、attachment、fs/terminal、steering 降级 |
| driver + instance 分离 | 借鉴为 `driver`（行为）+ `acpAgents` profile（配置） |
| provider-testing / testkit / replay | 借鉴为 scripted ACP agent + update fixtures |
| 服务端 command/event/outbox、child thread 编排 | **不引入**；Router 仍是 lead agent 的工具委派 |
| MCP 注入与 per-session credential | 借鉴；token 生命周期与作用域按 Router child 设计 |

### 6.2 `WorkerCapabilities`

放在 `internal/agentruntime`（不是 config），由 profile + initialize + policy 解析，持久化到 child run metadata：

```go
type WorkerCapabilities struct {
    SessionResume          bool
    SessionLoad            bool
    SessionClose           bool
    SessionFork            bool
    AdditionalDirectories  bool

    PromptImage            bool
    PromptAudio            bool
    PromptEmbeddedContext  bool

    MCPHTTP                bool
    MCPSSE                 bool
    MCPStdio               bool

    FSReadText             bool
    FSWriteText            bool
    Terminal               bool

    ModelConfigOption      bool
    ThoughtStream          bool
    NativeSubagents        bool
    ActiveSteering         bool // 未来 driver 用；ACP 当前为 false

    TerminalStatusQuality  string // strong | weak | none
    Source                 string // negotiated | profile | degraded
}
```

规则：

- **advertised 是上限**：adapter 的 baseline 不能授予 `initialize` 未广告的能力。
- **profile 是信任下限**：`terminal=true` 仍需 agent 广告 terminal capability；两者都满足才启用。
- **Runtime policy 是最终裁决**：remote profile 永远 `MCPHTTP=false`（不可达）；本项目只做 loopback HTTP，因此 `MCPSSE/MCPStdio` 仅作为诊断字段，不用于注入。
- 解析结果与降级原因写入 `session_child_runs`，并发一条 canonical child notice（不进入 lead prompt）。
- `capabilityOverrides` 只允许收紧（把 `true` 改为 `false`），不允许放宽；override 记录进 metadata。

### 6.3 `WorkerAdapter`

该接口与实现放在新的 `internal/agentruntime/workerdriver/` 子包（见 7.7），`agentruntime` 只做调用。

```go
type WorkerDriver string

const (
    WorkerDriverGenericACP WorkerDriver = "generic-acp"
    WorkerDriverCodex      WorkerDriver = "codex-acp"
    WorkerDriverClaudeCode WorkerDriver = "claude-code-acp"
    // ...
)

type WorkerAdapter interface {
    Driver() WorkerDriver
    Preset() WorkerPreset
    Baseline() WorkerCapabilities
    Resolve(input WorkerResolveInput) WorkerCapabilities
    // 可选 quirk hooks；默认实现保持通用 ACP 行为。
    NormalizePermissionOptions([]acpclient.PermissionOption) []acpclient.PermissionOption
    NormalizeUpdate(acpclient.Message) []agentpkg.Event
}
```

- 注册表以 `driver` 为 key；未知/空 driver 回落 `generic-acp`。
- `ACPAgentProfile` 增加可选 `driver`（缺省 `generic-acp`）与 `mcpBridge`（`auto|off`）；旧 settings 无需迁移。
- 业务代码只读 `WorkerCapabilities`，不读 `driver` 名字；`driver` 只在注册与诊断中出现。

### 6.4 策略降级矩阵

| 需求 | 支持 | 不支持 |
|---|---|---|
| 重连 | `session/resume` | `session/load` → 都无则明确失败，不新建 session |
| MCP bridge | `MCPHTTP` → loopback HTTP | `off`/不可达，metadata `unavailable`；不降级 stdio |
| 附件 | image/audio/embedded 各自能力 | 文本中披露资源 ID 与相对路径（现有行为） |
| 写文件 | `FSWriteText` + 写角色 + 非空 `write_scope` | 只用 worker 自己的工具；`fs/write` 不广告 |
| terminal | profile opt-in + agent 广告 | 不广告；worker 自己决定是否运行命令 |
| 后续消息 | `send`（同一 remote session） | remote session 不可 resume 时明确失败 |
| 主动 steering | `ActiveSteering`（未来） | 当前不支持；只能等 turn 结束或 `destroy` |
| 嵌套委派 | `NativeSubagents` 仅用于观测/文档 | Router 不提供 worker→worker 工具 |

### 6.5 已知 agent flavor 的边界

- `ACPAgentPresets()` 继续提供经过验证的启动配方。
- flavor 只影响：候选 recipe、permission option 形状、update 归一化细节、已知能力缺口。
- **flavor 不得影响**：授权、sandbox、写权限、并发、approval 策略、是否允许委派。
- 任何 flavor 缺口必须以 `capability_degraded` 事件暴露，不能只在代码里静默分支。

### 6.6 模型可见的能力面

- `publishRouterTarget` 把每个 bound worker 的简洁能力摘要写进 `delegate_worker_mgr` 的描述：是否支持附件、write、terminal、ask/report、resume。
- worker `initialize` 完成后立即 republish；每次 Agent 构建冻结 prompt 前也按最新广告重算，适配器无需额外同步。
- 只发布短摘要，不把完整 capability JSON 放进 prompt。
- 能力不满足时给出可操作原因：bridge 说明 remote/off/no listener/degradation；attachments/resume 说明“worker 未广告”；write/terminal 说明具体 `capabilityOverrides` 条目（例如“attachments: worker did not advertise image/audio/embeddedContext”“write: fsWriteText: disabled by capabilityOverrides”）。

### 6.7 测试适配器

- 新增 scripted ACP agent helper（复用现有 `acp_worker_bridge_test.go` 的子进程模式），按脚本返回 initialize/session/new/updates/permission/stopReason。
- 每种 driver 一份 fixture：advertised capabilities、MCP transport、permission options、异常 update。
- 表驱动测试 `Resolve` 的三方交集与降级矩阵；禁止在测试里按名字走特殊路径。
- 真实 interop（Codex/Claude ACP 等）只作为显式运行的兼容验证，不进入默认 CI。

## 7. 第三方 Agent 的注册与对接（t3code 调研）

本节回答“每个第三方 agent 是怎么注册进来、怎么被调用”的问题。t3code 的做法可以拆成 **driver / instance / adapter / capability / client registration** 五层；MothX 只借用其中与 ACP 相关的部分。

### 7.1 两层身份：driver 与 instance

t3code 把“集成类型”和“一份配置/账号”分开：

- `ProviderDriverKind`（如 `codex`、`claudeAgent`、`cursor`、`grok`、`opencode`、`antigravity`、`kiro`、`pi`、`muse`、`fx`、`acpRegistry`）标识一种集成；
- `ProviderInstanceId` 标识一份具体配置和账号生命周期；同一 driver 可以有多个 instance；
- 会话、模型目录、凭据都属于 instance，不能在同 driver 的两个 instance 之间共享可变状态；
- orchestration 和客户端只读 capability，不读 driver kind，也不按 provider 名字分支。

MothX 的对应关系：

| t3code | MothX |
|---|---|
| `ProviderDriverKind` | `ACPAgentProfile.driver`（默认 `generic-acp`） |
| `ProviderInstance` | `settings.json` 的 `acpAgents[]` 条目 |
| `ProviderAdapterV2` | 共享 ACP 客户端 + `WorkerAdapter` flavor |
| `OrchestrationV2ProviderCapabilities` | `WorkerCapabilities` |
| ACP Registry 里的 agent | 用户显式配置的 ACP profile（MothX 不自动发现、不自动安装） |

### 7.2 Driver 契约

t3code 每个 provider 包导出一个 `ProviderDriver`：

```ts
type ProviderDriver<Config> = {
  driverKind: ProviderDriverKind;
  metadata: {
    displayName: string;
    supportsMultipleInstances?: boolean;
    hasDefaultInstance?: boolean;
  };
  configSchema: Schema<Config>;
  defaultConfig: () => Config;
  create: (input) => Effect<ProviderInstance, ProviderDriverError>;
  usage?: ProviderUsageReader;
};
```

`create` 返回的 `ProviderInstance` 是对外暴露面：

```ts
type ProviderInstance = {
  instanceId; driverKind; continuationIdentity;
  displayName?; accentColor?; enabled;
  snapshot;                 // ServerProvider：安装状态、版本、auth、模型、slash commands
  snapshotForCwd?; refreshModels?; invalidateCaches?;
  orchestrationAdapter;     // ProviderAdapterV2：openSession + 各 turn 控制
  textGeneration;           // commit/PR/标题等应用级生成
  auth?; acpSessionManagement?;
};
```

`ProviderAdapterV2` 的 session runtime 再暴露 `ensureThread / resumeThread / startTurn / steerTurn / interruptTurn / stopSubagent / respondToRuntimeRequest / readThreadSnapshot / forkThread / rollbackThread`；可选方法由 capability 守卫。

MothX 不需要 `textGeneration`、`auth`、`fork/rollback` 这些应用级能力；对应到 `WorkerAdapter` 的是更窄的一组钩子（baseline、resolve、MCP transport、permission option、update 归一化）。

### 7.3 服务端与客户端的注册清单

t3code 新增一个 provider 需要同时改这些位置（来自 `docs/internals/adding-a-provider.md` 与代码）：

| 层 | 位置 | 作用 |
|---|---|---|
| 服务端 driver 列表 | `apps/server/src/provider/builtInDrivers.ts` | `BUILT_IN_DRIVERS` 决定可创建的 driver；顺序影响去重 |
| 状态排序 | `apps/server/src/provider/providerStatusCache.ts` | `BUILT_IN_DRIVER_ORDER` |
| 模型兼容 | `apps/server/src/provider/model-manifest.json` | `t3CodeRange` / `recommendedRange` / 版本区间状态；改完 bump `updatedAt` |
| 默认模型 | `packages/contracts` | driver 的默认 model slug |
| 服务端包 | `packages/provider-<name>/src/server.ts` | 导出 `ProviderDriver` 与 adapter driver |
| 设置 schema | `packages/provider-<name>/src/settings.ts` | `makeProviderSettingsSchema`，浏览器安全 |
| 客户端定义 | `packages/provider-<name>/src/client.ts` | `defineProviderClient`：label、settings schema、icon、badge |
| 客户端注册表 | web / mobile provider registries | 设置页、模型选择器、搜索词 |
| 测试 | `packages/provider-<name>/src/testing.ts` + `apps/server/src/orchestration-v2/testkit` | replay fixture 与 testkit |
| 文档 | `docs/user/providers-<name>.md` | 安装、权限模式、差异 |

包边界很硬：server 代码只能 import `@t3tools/provider-core/server/*`、contracts、shared；通过 `ProviderHost` 访问路径、设置、后台任务开关、附件、凭据，不能 import `apps/server`。

### 7.4 实例配置与生命周期

`ServerSettings.providerInstances` 是 `Record<ProviderInstanceId, ProviderInstanceConfig>`：

```ts
type ProviderInstanceConfig = {
  driver: ProviderDriverKind;
  displayName?: string;
  accentColor?: string;
  environment?: ProviderInstanceEnvironment; // 支持 secret reference
  enabled?: boolean;
  config?: unknown;                          // driver.configSchema 解码
};
```

`ProviderInstanceRegistry` 的职责：

1. 用 `driver.configSchema` 解码 `config`，缺省用 `driver.defaultConfig()`；
2. 解析 `environment` 与 config 里的 secret reference；读不到 secret 时生成 unavailable snapshot，**不回落到进程继承的凭据**；
3. 在独立 `Scope` 里调用 `driver.create`；配置变更/删除时先关闭旧 scope，再创建新实例；
4. 创建失败或配置非法时保留 unavailable snapshot，UI 可显示原因；
5. `listInstances` 只返回 live 实例；`listUnavailable` 单独投影。

实例通过 `ProviderInstanceMutation` 的 `create` / `upsert` / `remove` 修改；新 provider 默认关闭，设置页用 `client.ts` 的 settings schema 渲染表单。MothX 对应的是 `/router` 面板与 WebUI/Desktop 的 profile 管理，写入仍走 `config.SaveGlobalSettingsPatch()` 或 Runtime 的 profile 写入路径。

### 7.5 状态、模型、认证与维护

- `status.ts` 做安装/版本/auth/模型探测；**后台探测不打开会启动 MCP、hooks 或登录的 session**；`providerProbeEnvironment` 在后台探测时设 `MISE_OFFLINE=1`，避免 mise 下载缺失的 CLI。
- 探测结果写入 `ServerProvider` snapshot；`providerStatusCache.ts` 只 hydrate 成功的已认证探测；模型合并保留用户 custom model，避免缓存复活已删除的模型。
- `model-manifest.json` 记录 provider 版本兼容区间；`providerMaintenanceRunner.ts` 只在能证明拥有该安装时执行更新，否则交给 provider 自己的 updater 或标记 manual。
- 凭据按 instance 隔离，经 `ProviderHost.credentials` 绑定；Antigravity 等还会强制文件凭据、移除环境里的 ambient credentials，避免两个 instance 共用账号/计费。

### 7.6 ACP 类 Agent 的注册路径（与 MothX 最相关）

t3code 的 ACP 支持有两条入口：

1. **官方 ACP Registry**：`acpRegistry` driver（`packages/provider-acp-registry`）。settings 里选 `source=registry|local`、`agentId`、可选 `commandPath` override、`authMethodId`、distribution 偏好、custom models。
2. **专用 ACP driver**：当某个 agent 需要 spec 之外的行为时，新增一个 `provider-<name>` 包，但内部复用 `@t3tools/provider-acp/server/adapter`，只写一个 flavor。

`AcpRegistrySupport` 负责：

- 拉取 `https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json`（大小上限、HTTPS、30s 超时）；
- `search` 复用短期索引（5 分钟），`prepare` 总是取最新；
- 支持 binary archive（HTTPS、可选 sha256、版本目录缓存）、npx（必须精确版本）、uvx（必须精确版本）、本地可执行覆盖；
- 安装/解压/校验有超时与字节上限；版本目录名有字符约束。

`probe.ts` 通过 ACP `initialize` / session setup 读取模型、configOptions、slash commands、auth 状态；背景探测不触发登录。`AcpRegistryRuntimeCoordinator` 串行化启动与 session mutation，串行化后台探测，并发布 live config/commands 更新。

flavor 的选择在 `packages/provider-acp-registry/src/server/adapter.ts`：按 `agentId`（或本地可执行名）匹配，示例：

- `claudeAcp.ts`：识别 Claude 的 `Agent`/`Task` tool call，抽取 subagent update；
- `copilotAcp.ts`：注入扩展 meta、识别 lost-session 错误并重启进程；
- `devinAcp.ts` / `droidAcp.ts`：更新归一化与 elicitation；
- `provider-grok` / `provider-fx` / `provider-kiro` / `Antigravity`：各自一个 ACP driver + flavor，处理扩展方法、权限 option、config id、进程隔离等。

t3code 的硬规则：**generic registry adapter 里不能加 agent-id 判断**；只有 spec 覆盖不了时才新增 flavor，而且 flavor 只做协议差异，不做授权或 sandbox 决策。

### 7.7 MothX 的注册与对接设计

MothX 只讲 ACP，因此不复制 t3code 的原生协议 driver。一个第三方 agent 的接入分四步：

1. **Profile（实例）**：在 `settings.json` 的 `acpAgents[]` 中登记 `id/title/model/transport/command|url/environment/permissionPolicy/maxConcurrentChildren/terminal/driver/mcpBridge/capabilityOverrides`。这是唯一授权来源；模型工具调用不能提供命令、URL 或环境变量。`model` 是外部 Agent 自己的模型 ID，留空表示使用第三方 Agent 的默认行为，由 Runtime 在 `session/new|load|resume` 后通过标准 `session/set_config_option`（或仅对无现代选项的 worker 回退到 `session/set_model`）应用。
2. **Preset（可选）**：在 `ACPAgentPresets()` 增加经过验证的启动配方（命令、参数、默认 driver）。TUI `/router` 面板和 CLI `--acp-agent` 只使用 preset 生成 profile，不自动安装、不自动发现。
3. **Driver / flavor（按需）**：默认 `generic-acp`。只有观察到 ACP spec 覆盖不了的行为时才新增 `workerdriver` 实现，例如：permission option 形状、configOption 命名、扩展通知、session 恢复怪癖、subagent 事件抽取。flavor **不能**声明 agent 未广告的能力，也不能改变授权/沙箱/并发。
4. **能力解析**：`Resolve(baseline, advertised, profile, policy) -> WorkerCapabilities`；结果写入 child metadata，并在 `delegate_worker_mgr` 描述与 `mothx doctor` 中展示。

代码落点（新增 `internal/agentruntime/workerdriver/` 子包，避免继续膨胀 `agentruntime`）：

```text
internal/agentruntime/workerdriver/
  types.go        // WorkerDriver, WorkerCapabilities, ResolveInput
  registry.go     // Builtins() []Adapter; Lookup(id)
  generic.go      // generic-acp：只读 advertised，不猜
  codex.go        // 仅放经过 interop 验证的 ACP 差异
  claude_code.go
  opencode.go
  ...
```

注册用显式 `Builtins()` 列表（类似 `BUILT_IN_DRIVERS`），不用 `init()` 隐式注册；`agentruntime` 只通过 `Lookup` 取 adapter。`internal/architecture` 增加 guard：业务代码不得直接按 driver id 分支，adapter 不得读 `acpAgents`。

已知 ACP agent 的初始策略（能力一律以 `initialize` 广告为准，flavor 只补已验证缺口）：

| agent | 初始 driver | 何时需要 flavor |
|---|---|---|
| 任意 ACP agent | `generic-acp` | 不需要；只用 advertised capability |
| Codex（preset `codex`，命令 `codex-acp`） | `generic-acp` | 出现 Codex 特有的 permission/扩展/恢复差异时 |
| Claude Code（preset `claude-code`，命令 `claude-agent-acp`） | `generic-acp` | 需要把 `Agent`/`Task` 投影成 native subagent 时 |
| OpenCode（preset `opencode`） | `generic-acp` | configOptions/model/session 差异被验证时 |
| Gemini CLI（preset `gemini`） | `generic-acp` | 仅真实 interop 发现差异时 |
| Qwen Code（preset `qwen-code`） | `generic-acp` | 仅真实 interop 发现差异时 |
| GitHub Copilot（preset `copilot`） | `generic-acp` | 扩展 meta 或 lost-session 重启被验证时 |
| Cursor Agent（preset `cursor`） | `generic-acp` | 仅真实 interop 发现差异时 |
| Kiro（preset `kiro`） | `generic-acp` | `_kiro/*` 扩展或 config id 被验证时 |
| OpenClaw（preset `openclaw`） | `generic-acp` | 仅真实 interop 发现差异时 |

**不做**：原生 Codex app-server / Claude SDK / Cursor SDK / OpenCode server / Pi RPC / Muse SDK driver；provider 切换与 context handoff；按 provider 的 text generation/usage；自动安装或自动信任 ACP Registry。

可选的后续能力（不在本方案范围）：把官方 ACP Registry 作为 preset 目录导入（search/prepare/校验），但仍然要求管理员显式启用，不自动启动。

### 7.8 新 agent 接入验收清单

- [x] profile 通过 `Validate()`，`mothx doctor` 能检查命令/URL/环境名，不启动进程。
- [x] `initialize` 成功，resolved capabilities 与 advertised 一致；没有未广告却被启用的能力。
- [x] `session/new`、`session/prompt`、取消、关闭全路径通过 scripted ACP fixture。
- [x] MCP bridge：本地 + `MCPHTTP` 时注入并可用；不可达时标记 unavailable，不影响 prompt。
- [x] resume/load：按 capability 选择；都不支持时明确失败，不新建 session。
- [x] 写角色/terminal/附件按能力降级，不静默假装支持。
- [x] 至少一次真实 interop（显式运行，不进默认 CI），记录 agent 版本与命令。
- [x] 文档：`docs/en|zh/tools.md`、`docs/en|zh/configuration.md`、preset 面板文案。

## 8. 与现有 Runtime 的集成点

| 文件 | 改动 |
|---|---|
| `internal/mcp/server.go` | 抽出 handler 分发；新增 `NewHTTPHandler(handler)`，实现最小 MCP HTTP（POST JSON）；不改 stdio 行为 |
| `internal/agentruntime/worker_bridge.go`（新） | `WorkerBridgeServer`、token registry、HTTP handler、`worker_mgr` 实现 |
| `internal/agentruntime/worker_messages.go`（新） | JSONL append-only 日志、折叠读取、去重、compaction |
| `internal/agentruntime/workerdriver/`（新包） | `types.go`（WorkerDriver/WorkerCapabilities/ResolveInput）、`registry.go`（显式 `Builtins()` + `Lookup`）、`generic.go`、按需的 `codex.go`/`claude_code.go`/`opencode.go` |
| `internal/agentruntime/worker_capabilities.go`（新） | 由 `workerdriver` 调用；把 resolved caps 写入 child metadata，不重复实现解析 |
| `internal/agentruntime/session_runtime.go` | `BuildOptions.WorkerBridge`；`SessionRuntime` 持有 bridge/log；`Shutdown` 撤销 token |
| `internal/agentruntime/acp_child.go` | 创建 child 时注册 scope；turn 结束置 terminal、下一轮解除；destroy/close/session shutdown 时撤销；metadata 写 capability |
| `internal/agentruntime/acp_worker_remote.go` | 把 `invocation.MCPServers` 传入 new/load/resume |
| `internal/agentruntime/acp_worker_pool.go` | 不需要改：MCP 是 session 级，连接池仍按 profile/workdir 复用 |
| `internal/agentruntime/acp_worker_decisions.go` | worker question 的 DecisionService 注册/解析辅助 |
| `internal/agentruntime/tool_sync.go` | Router 只注册 `delegate_worker_mgr`；`publishRouterTarget` 只投影该工具 |
| `internal/agent/worker_mgr_tool.go`（新） | `delegate_worker_mgr` 的 schema 与 action 分派，复用现有内部方法；`answer` 走 `WorkerQuestionResolver` |
| `internal/agent/manager.go` | 新增 `SetWorkerQuestionResolver`（Runtime 安装），与 `SetChangeSetIntegrator` 同层 |
| `internal/agent/subagent.go` | 本地工具删除 Router 字段；不再动态改写 schema |
| `internal/agent/subagent_tools.go` | `RouterSubAgentToolNames()` 收敛为 `{"delegate_worker_mgr"}` |
| `internal/tools/tool.go` | `ModeTools` 的 `case "router"` 只保留只读工具 + `delegate_worker_mgr` |
| `internal/agent/mailbox.go` | `MemberCompletion.Wake`、`HasWakingPending`；finding 唤醒 |
| `internal/config/settings.go` | `driver`、`mcpBridge` 可选字段；`capabilityOverrides`；校验与默认值 |
| `internal/session/migrations.go` + `internal/dao/child_runs.go` | child_runs 增加 resolved capabilities / degradations / mcpBridge 列 |
| `internal/architecture` | guard：adapter 不得读 `acpAgents`；bridge 不得调用 `agent.New`；不得新增第二套 Run |
| `internal/serve/openaiapi`、`internal/acp`、`internal/tui` | 只读投影 worker 消息/问题/能力；不新增控制 API |
| `docs/en/tools.md`、`docs/zh/tools.md`、`docs/en/configuration.md`、`docs/zh/configuration.md` | 工具、profile 字段、bridge 限制、remote/container 限制 |

## 9. 分阶段

### Phase 0：能力层与工具面（无新增能力）

- 新增 `internal/agentruntime/workerdriver/`：`WorkerCapabilities`、`WorkerAdapter`、`Builtins()`、`generic-acp`。
- `ACPAgentProfile` 增加 `driver` 字段（默认 `generic-acp`）；`ACPAgentPresets()` 补默认 driver。
- 迁移现有 map 探测到 `Resolve`；resolved caps 写入 child metadata。
- Router 只注册 `delegate_worker_mgr`；本地工具删除 Router 字段。
- 表驱动测试覆盖三方交集、降级矩阵、9 个 action 的必填校验。
- 验收：现有 ACP 测试全绿；本地多 Agent/专家团行为不变；Router 工具不再出现“Rejected”参数。

### Phase 1：Bridge + report/context

- `WorkerBridgeServer`（`tcp4` loopback `:0`）、token、MCP HTTP handler。
- `worker_mgr(context|report)`；JSONL 日志。
- `acp_worker_remote.go` 注入 `mcpServers`（仅本地 stdio、`mcpBridge=auto`）。
- mailbox `Wake` 语义；finding 唤醒、progress 不唤醒。
- child metadata / UI 显示 `mcpBridge`。
- 验收：scripted worker 调用 context/report；dedupe；不连 MCP 时任务正常完成；token 不落盘。

### Phase 2：ask/answer

- `worker_mgr(ask)` + DecisionService + durable record + 超时。
- `delegate_worker_mgr(action=answer)`；mailbox question 投影。
- 空闲 lead 被 question 唤醒并开 Run。
- 验收：worker 提问 → lead 回答 → MCP 返回；超时自动取消；重复回答幂等；进程重启后 pending decision 按现有 replay 规则处理。

### Phase 3：flavor、overrides 与文档

- 按需新增已知 flavor（codex/claude-code/opencode/copilot 等），每个 flavor 必须有真实 interop 依据。
- `ACPAgentProfile` 增加 `mcpBridge`、`capabilityOverrides`；校验与默认值。
- `capabilityOverrides` 只能收紧；metadata 与 UI 可见。
- `mothx doctor`：listener、401/406、token、idle、remote/container、profile 诊断。
- 双语文档、preset 面板文案、每个新 agent 的接入验收清单。
- 验收：至少两种 ACP agent 的 real interop；override 生效且可见；remote/container 明确 `unreachable`。generic-acp 已覆盖已验证 agent；只有真实 interop 暴露 spec 缺口时才新增 flavor。

## 10. 测试与验收矩阵

| 层 | 用例 |
|---|---|
| 单元 | token 只映射一个 child；跨 child 访问不可能；body/rate/option 上限；dedupe 唯一；`HasWakingPending` 只对 Wake=true 返回 true |
| 工具 | `delegate_worker_mgr` 9 个 action 的必填组合、错误信息、agent const/enum；本地工具不再接受 Router 字段 |
| 协议 | MCP initialize/tools/list/tools/call；401；非法 JSON；`text/event-stream` 的明确响应 |
| 端口 | 同机多进程各自 `:0` 不冲突；idle 关闭；listener 仅 `127.0.0.1`；`tcp4` |
| Runtime | bridge 不可用不阻塞 spawn；terminal 后调用返回 MCP tool error `child_terminal`；shutdown 撤销 token；resume 重新签发 |
| 决策 | ask 的 request/resolution/deadline/replay/first-response-wins；wake Run 回答也生效 |
| 能力 | baseline ∩ advertised ∩ policy；adapter 不能授予未广告能力；override 只能收紧；降级事件可观测 |
| JSONL | append-only；最后一行损坏可恢复；折叠状态正确；session 删除级联；fork 不复制 |
| 跨入口 | TUI/CLI/serve/ACP 下同样的 report/ask 语义；wake Run 走 canonical Run 事件与投影 |
| 证据 | report/question 文本在 lead 上下文标记 `worker-reported`；不自动升级为 verified |
| 安全 | token/Authorization 不出现于日志、JSONL、session 文件、UI；listener 仅 loopback |
| 架构 | `internal/architecture` 新增 guard；`go test ./internal/architecture` |

## 11. 风险与开放问题

1. **mega-tool 的填写准确率**：一个 `delegate_worker_mgr` 覆盖 9 个 action，模型可能漏必填字段。缓解：枚举、结构化错误、评测覆盖；若仍不足，回退为两个工具（创建 + 控制）。
2. **finding 唤醒风暴**：finding 唤醒空闲 lead，可能频繁开 Run。缓解：按 child 合并、mailbox debounce、限流；必要时只允许“高重要性 finding”唤醒。
3. **JSONL 并发与 compaction**：单写者、`O_APPEND`、最后一行容错；compaction 需原子替换。跨进程读只容忍 append，不并发写。
4. **决策与消息的持久化分裂**：消息在 JSONL，pending decision 在 `DecisionRecord`。两者用 `decisionId` 关联；评审时确认这比“全部 JSONL”更简单，避免复制 replay。
5. **loopback 可达性**：远程/容器/独立 netns 的 worker 无法访问宿主 loopback。明确标记 `unreachable`，不引入 stdio 或 public URL 作为补救。
6. **多进程端口**：使用 `:0` 从根上避免固定端口冲突；但每个进程一个 listener 需要顶层装配保证单例，不能按 session 创建。
7. **能力误报**：agent 可能广告了但行为不符。`capabilityOverrides` 只能收紧，并在 metadata 中记录。
8. **worker 消息污染 lead 上下文**：finding 限流与 body 上限必须严格执行；progress/notice 永不注入。
9. **工具面变更**：Router 只保留一个管理工具，需要同步 `docs/en/tools.md` / `docs/zh/tools.md`、评测 prompt 与快照测试；Router 仍是 beta，不承诺旧名兼容。
10. **flavor 蔓延**：每个 agent 都要求一个 driver 会重新制造 t3code 的维护面。默认必须 `generic-acp`，flavor 只在真实 interop 暴露 spec 缺口时新增，并且只做协议差异。
11. **执行路径重复**：`delegate_worker_mgr` 必须复用现有 child 创建/运行/集成方法；评审时确认没有复制 runner。

## 12. 实施 TODO

> 按顺序执行；每一项都应能单独编译、测试、回滚。Phase 之间不交叉提交。

### Phase 0：能力层与工具面

- [x] 新增 `WorkerCapabilities` 与解析函数（profile ∩ advertised ∩ policy）。
- [x] 新增 `internal/agentruntime/workerdriver/`：`types.go`、`registry.go`、`generic.go`。
- [x] `ACPAgentProfile` 增加 `driver`；`ACPAgentPresets()` 补默认 driver；`ListACPWorkerProfiles` 投影 driver。
- [x] 把 `acp_worker_remote.go` / `acp_worker_bridge.go` 的 map 探测迁移到 `WorkerCapabilities`。
- [x] `session_child_runs` 增加 `resolved_capabilities_json`、`capability_degradations_json`、`mcp_bridge`（migration + DAO + 记录点）。
- [x] 新增 `delegate_worker_mgr` 工具骨架：schema、action 枚举、必填校验、agent const/enum 投影。
- [x] action 分派复用现有内部方法（delegate/spawn/status/send/wait/destroy/integrate/answer）。
- [x] 本地 `delegate_subagent` / `subagent_spawn` 删除 Router 字段与动态 schema 分支。
- [x] `tool_sync.go` / `ModeTools` / `RouterSubAgentToolNames` 收敛为单一 `delegate_worker_mgr`。
- [x] 表驱动测试：能力交集、降级矩阵、9 个 action、 本地工具拒绝 Router 字段。
- [x] `internal/architecture` 增加 guard；跑 `go test ./internal/architecture`。

### Phase 1：Bridge + report/context

- [x] 新增 `WorkerBridgeServer`：`tcp4` `127.0.0.1:0`、懒启动、idle 关闭、进程退出关闭。
- [x] 顶层装配单例：TUI/CLI、serve、ACP 各一个，通过 `BuildOptions.WorkerBridge` 注入。
- [x] 每 child token 生成、绑定、撤销；401 路径；不落盘。
- [x] `internal/mcp` 增加最小 HTTP handler（initialize/tools/list/tools/call）；stdio 行为不变。
- [x] `worker_mgr(context|report)`：schema、限额、dedupe、JSONL append。
- [x] `acp_worker_remote.go` 在 new/load/resume 注入 `mcpServers`（仅本地 stdio、auto）。
- [x] `MemberCompletion.Wake` + `HasWakingPending`；finding 唤醒、progress 不唤醒；`subagent_wait` 行为同步。
- [x] child metadata / TUI / ACP / WebUI 只读显示 `mcpBridge` 与 worker 消息。
- [x] 测试：scripted ACP agent、dedupe、no-bridge fallback、token 泄漏、多进程端口。

### Phase 2：ask/answer

- [x] `worker_mgr(ask)`：DecisionService 注册、deadline、超时取消、JSONL 记录。
- [x] `delegate_worker_mgr(action=answer)`：Resolve、幂等、越权校验。
- [x] mailbox question 投影 + 空闲 lead wake 新 Run。
- [x] UI 只读展示 pending question（不提供回答入口）。
- [x] 测试：ask/answer/timeout/replay/first-response-wins/跨 Run 回答。

### Phase 3：flavor、overrides、文档

- [x] `ACPAgentProfile` 增加 `mcpBridge`、`capabilityOverrides`；校验与默认值。
- [x] 按需新增已知 flavor：已验证 OpenCode 2.0.16 与 codex-acp 0.16.0 均可由 `generic-acp` 描述，未发现需要新增 flavor 的协议缺口，因此不新增未经验证的 flavor。
- [x] `capabilityOverrides` 只能收紧；metadata 与 UI 可见。
- [x] `mothx doctor`：listener、401/406、token、idle、remote/container、profile 诊断。
- [x] 每个新 agent 跑 7.8 的接入清单，并记录 agent 版本/命令。
- [x] 更新 `docs/en|zh/tools.md`、`docs/en|zh/configuration.md`、changelog。
- [x] 真实 interop：OpenCode 2.0.16 完成 MCP bridge context/report；codex-acp 0.16.0 完成 bridge handshake；版本与命令见上文记录。

## 13. 明确不做

- 不把 Router 改造成 t3code 式的服务端 thread/run/command/outbox 编排。
- 不让 worker 调用 spawn/send/destroy/steer 等控制面工具。
- 不做 worker→worker 直接通信或嵌套委派。
- 不做 stdio MCP、SSE、固定端口或 public URL；只做 loopback HTTP。
- 不默认给 remote/容器 worker 注入 bridge。
- 不把 token、MCP URL、question/report 原文持久化到 settings/child_runs。
- 不用 MCP 消息替代 ACP `session/update` 的 transcript 投影；两者是不同层的事实。
- 不新增 SQLite worker 消息表；消息走 session 目录 JSONL。
- 不实现原生 Codex app-server / Claude SDK / Cursor SDK / OpenCode server / Pi RPC / Muse SDK driver；第三方 agent 只通过 ACP profile 接入。
- 不做 provider 切换与 context handoff、按 provider 的 text generation/usage。
- 不自动发现、自动安装或自动信任 ACP Registry；preset 必须由用户/管理员显式选择。
