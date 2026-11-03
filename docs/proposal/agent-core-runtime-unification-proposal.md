# 统一 Agent 核心与多入口 Runtime 方案

> 状态：Phase 12 当前验收完成（2026-08-15）。核心 Agent Runtime 边界已经落地；明确命名的 adapter 兼容桥仍保留，并有 owner、删除条件和合同测试，不再作为第二套核心生命周期。
>
> 最近同步：2026-11-03
>
> 当前进度：`internal/agentruntime` 已提供 `ExecutionRuntime`、`DecisionService`、protocol-neutral `DecisionRecord`、`ReplayDecisions`、`DecisionService.Rehydrate`、`RunEvent/RunEventSink`、`RunStore`、`DurableRunStore`、durable transition API、orphan recovery policy/projection 和 delivery replay/event constructors。生产 Agent 构造已统一经 `SessionRuntime.BuildAgent`、`BuildTransientAgent` 或共享 `NewAgentManager`；CLI print、Responses background 和各主要入口都使用 Runtime build options。数据库访问已完成严格收敛：`internal/db` 统一管理 Bun 连接生命周期，`internal/dao` 统一承载所有 Bun 查询构造、SQL、表操作和行映射，业务/runtime 代码只通过 DAO 方法访问数据库，不保留 `internal/commondb` 兼容桥。Responses provider-specific polling、continuation 和 remote replay 仍保留在 Adapter driver。2026-08-15 已修复 Serve 子 Agent content/terminal 投影竞态、`DecisionService.Register` nil receiver、Channel high-risk command bypass、标题生成 shutdown race，以及 ACP prompt/ durable run identity 混用。
>
> 当前边界：Source binding、forced `yolo`、strict conflict rejection、child inheritance、Cron/ESM/recovery mode propagation、数据库 `db -> dao -> logic` 边界和主要入口的 bounded `SessionRuntime.Shutdown` 已统一。Shutdown 会等待 loop owner、为无 owner 的 durable run 补齐 terminal event，并在执行错误时仍清理 Decision；durable transition 具备串行化、失败可重试和确定性 recovery event。ACP 进程重启时不会伪造 Agent loop reattach：本地 ACP orphan 会被标记 failed，pending Decision 会 terminalize；同一进程 reconnect 才会复用 Runtime 并 replay。Responses 的 provider-specific driver、Channel/TUI capability hooks、协议 payload、RunManager 内存 fan-out，以及少量 adapter-owned Decision/Run 兼容 API 是有删除条件的迁移桥，不是新的核心实现。

> 目标：建立一个完整、前端中立的 Agent 核心运行时。TUI、WebUI、微信/飞书等 Channel、ACP 只负责自己的协议、交互和权限配置，不再分别实现 Agent 的装配、Session 恢复、工具/MCP/Skill 管理和运行生命周期。
>
> 关联代码：`internal/agent`、`internal/tools`、`internal/mcp`、`internal/skills`、`internal/session`、`internal/provider`、`internal/tui`、`internal/serve`、`internal/acp`

## 1. 摘要

本节第 1.1 小节之前的描述是方案提出时的初始基线，用于解释迁移动机；当前实现状态以第 1.2 小节和文首进度为准。

MothX 当前已经拥有较完整的 Agent 核心：Agent loop、Provider 抽象、Tools Registry、MCP、Skills、SQLite Session、Sandbox、Sub-agent、Workflow 和多厂商兼容能力均已存在，并且多数入口最终复用 `internal/agent`。

但当前的复用主要停留在“共享底层能力”层面。TUI、WebUI/API、Channel、ACP 仍分别负责：

- 创建和恢复 Session；
- 组装 `agent.Config`；
- 创建 Tools Registry；
- 加载 MCP 和 Skills；
- 选择 mode、sandbox、工具和能力开关；
- 处理 approval；
- 管理 run、cancel、事件和生命周期。

因此当前架构不是四套独立 Agent loop，而是“四套 Agent Runtime 装配和执行编排”。这会造成行为漂移。已经出现的典型问题是：微信/飞书 Channel 侧使用 `yolo`，WebUI 恢复同一 Session 时 `APISession.Mode` 为空，展示层显示为 `yolo`，但 run submit 又回退到 API 默认 `agent`。

本方案不重写 Agent loop，而是在现有 `AgentFactory`、`AgentManager`、`RunExecutor` 和 Session 持久化能力之上，增加一个前端中立的 Runtime 层，逐步把入口层变成薄适配器。

## 1.1 实施前基线（历史记录）

本节保留方案提出时的基线，不能作为当前代码状态使用。当前状态以文首和 1.2 为准。

本方案的总体判断成立，但实施前必须以以下事实为准：

1. **项目已经有 Channel binding/source 的持久化基础。** `internal/session` 已存在 `sessions.channel_type`、`sessions.channel_id`、`session.Header.ChannelType`、`session.Header.ChannelID`，并提供 `FindBindingBySessionID`、`ListBindings` 等 API。WebUI 也已经将 `ChannelType`、`ChannelID` 和 `Bound` 暴露给会话列表。因此 Phase 1 不应重新发明来源识别，而应把已有 binding 映射为统一 `RuntimeSource`。
2. **项目已经有部分 serve runtime 抽象。** `internal/serve/runtime` 当前包含 Responses background 的通用请求/driver 类型，但它还不是完整的 Agent Runtime；Agent 装配、Session resources、capabilities 和普通 run 生命周期仍主要位于 `internal/serve/openaiapi`。
3. **基线中的 Channel yolo 只是不变量缺口。** 当时 `ChannelSession` 的 `Mode: "yolo"` 仍可被 `/mode`、background request 或恢复路径覆盖；现在由 Runtime-resolved policy 强制 WeChat/Feishu 使用 yolo，并在工具执行前保留 high-risk guard。
4. **基线中的 WebUI source 漂移已修复。** 当时 WebUI 恢复 Channel session 主要依赖 capability 记录；现在 Session binding/header 是 authoritative source，WebUI、ACP、Cron、ESM、recovery 和 child mode 都通过同一 resolver。
5. **Approval 不是完全没有持久化模型。** WebUI 已有 `pendingSessionApproval`、approval request/resolution、事件和 Session 持久化；当前缺少的是跨入口统一的可挂起 decision service，而不是从零建立 approval persistence。

因此本方案的第一阶段应优先做“已有 binding/source → 统一 policy → 所有执行路径”的接通，而不是先新增一套独立的来源存储。

## 1.2 实施进度（2026-08-28）

> 本节记录截至 **2026 年 8 月 28 日** 的代码迁移状态。`DecisionService` 负责 pending decision identity、first-response-wins、Run 清理、可选 resolver callback 和 durable identity rehydration；协议 payload、callback 重绑、超时调度和外部交互仍由 Adapter 负责。数据库迁移已完成严格验收：生产 SQL/Bun 查询全部收口到 `internal/dao`，连接生命周期全部由 `internal/db` 控制，业务层不得越过 DAO。
>
> 2026-11-03 后续工作见 §1.3：Router 模式作为共享 Runtime 的一等模式落地，未新增第二套运行语义。

| 阶段 | 状态 | 已完成 | 仍未完成 |
|---|---|---|---|
| Phase 0 | ✅ 完成 | mode/source/capability/approval 关键不变量测试，以及共享 DecisionService 合同测试基础 | 继续扩展跨入口进程级覆盖 |
| Phase 1 | ✅ 完成 | `RuntimeSource`、mode resolver、authoritative Source/Policy resolver、Channel forced `yolo`，覆盖 WebUI/API/Channel/ACP/ESM/Cron recovery/background/approval；persisted binding/header/current source conflict 统一拒绝，未知 source fail closed | 完整 `ExecutionPolicy` schema 仍可继续扩展到更多 capability 字段，但不再存在入口级 mode fallback |
| Phase 2 | ✅ 当前边界完成 | `internal/agentruntime`、SessionRuntime、Builder、Context/Skills/Rule、Registry/MCP policy、Agent builder、Session lifecycle、ExecutionRuntime、DecisionService、DecisionRecord | RegistryHook、AgentBuildOptions 的 per-run inputs 和兼容 API 仍是命名迁移桥；Runtime 统一拥有资源和生命周期，Adapter 不再组装生产 `agent.Config` |
| Phase 3 | ✅ 当前边界完成 | Channel Session/Registry/MCP/Skills/Context/Agent/session lifecycle、ExecutionRuntime durable Begin/Cancel/Finish、Decision identity/resolver、Runtime-owned high-risk pre-tool guard 和 managed-child inheritance | Channel watchdog 判定、平台 delivery、协议 event mapping 仍由 adapter 负责；idle close 是无 active run 的兼容清理 |
| Phase 4 | ✅ 当前边界完成 | ACP Session/Registry/MCP/Skills/Context/Agent/session lifecycle、ExecutionRuntime durable Begin/Cancel/Finish、Permission/Question identity、ResolveWith persistence、超时/取消/关闭清理和 ACP-owned orphan recovery | ACP JSON-RPC framing/pending response channel 仍由 adapter 负责；进程重启不伪造本地 Agent reattach，只有同进程 Runtime reconnect 会 replay |
| Phase 5 | ✅ 当前边界完成 | TUI SessionRuntime 接入、Runtime.BuildAgent、mode 切换与配置重建、tuiRun Begin/Cancel/Finish、Approval/Question waiting/resume、TUI DecisionService ResolveWith、BTW transient Agent；基础 Context/Skills/Sandbox/Registry/MCP 和 AgentManager 已由 Runtime 构建 | Question/A2A/Sub-agent/Delegate/Workflow/Cron capability hooks 仍由 CLI/RegistryHook 注入，作为显式迁移桥 |
| Phase 6 | ✅ 当前边界完成 | `DecisionService` identity、Bind resolver、ClearRun/ClearRunWithValue、`Rehydrate`、deadline/replay、ResolveWith commit-before-consume、各入口持久化和合同测试 | ACP process restore 只做 orphan terminalization；可恢复执行栈的协议 callback 重绑仍是后续 adapter 工作 |
| Phase 7 | ✅ 当前边界完成 | `DecisionRecord`、`ReplayDecisions`、WebUI/Channel/ACP/TUI 的 DecisionRecord 持久化、旧 payload 兼容、跨入口 replay 单元测试 | 协议 callback/reconnect 仍由各 adapter 负责；不可恢复进程栈不会被错误 revive |
| Phase 11 | ✅ 当前边界完成 | `DeliveryRecord`、`ReplayDeliveries`、delivery event constructors、Channel background recovery 使用 Runtime delivery projection、pending payload 兼容、重复投递保护与 finalize 集成测试 | 平台 delivery payload 仍由 adapter 负责；进程级测试继续扩展 |
| Phase 12 | ✅ 当前验收完成 | CLI/TUI/Serve/Channel/ACP 使用共享 SessionRuntime；生产 Agent 构造统一经 Runtime；source/policy、Shutdown、durable transition、Decision commit/recovery、title task tracking、Cron job tracking、架构守卫和跨入口合同测试已收敛 | 长期债务仅限非数据库协议/能力迁移项：TUI capability hooks、Responses provider driver、RunManager 内存 fan-out；数据库 SQL、Bun 查询、连接生命周期已无迁移桥 |

| DB/DAO | ✅ 严格验收完成（2026-08-28） | `internal/db` 负责连接创建、缓存、SQLite 配置、迁移启动和关闭；`internal/dao` 负责全部 Bun 查询构造、raw SQL、表操作、持久化和行映射；Session、Run、Decision、Delivery、Response、Attachment、Project、Cron、Stats、ESM、Recovery 等数据访问均已迁移到 DAO；`internal/commondb` 已移除 | 无数据库兼容桥。新增业务代码不得导入 Bun/`database/sql`，不得调用 SQL 执行 API 或 Bun 查询构造器；所有访问必须遵循 `internal/db` → `internal/dao` → 业务逻辑 |

当前 `internal/agentruntime` 主要能力：

- `RuntimeSource`、`ExecutionPolicy`、`ModeResolver`；
- `SessionRuntime`、`Builder`、`AttachSessionResources`；
- `LoadContextResources`、`RefreshResources`；
- `RegistryPolicy`、`RegistryMutator`、`BuildRegistry`；
- `MCPPolicy`、严格/可选 MCP 连接和 Runtime-owned cleanup；
- `CreateSession`、`OpenSession`、`OpenSessionForWorkDir`、`DeleteSession`；
- `NewAgentManager`、`SessionRuntime.BuildAgent`、`BuildTransientAgent`、legacy `AgentBuildOptionsFromConfig` 迁移桥；
- `ExecutionRuntime` 的 Begin/Cancel/SetAgent/Finish/FinishWithState，以及 durable `BeginDurable`/`CancelDurable`/`FinishDurable`、approval/question waiting/resume 状态转换；

当前明确保留在适配层的内容：

- Channel 的平台鉴权、Security、Hooks、ProgressFunc、watchdog、消息和 run event 映射；Channel Question observer 仅作为可选适配边界，无正式外部协议时不得虚构回答协议；
- ACP 的 JSON-RPC framing、permission/question、MCP callback、protocol error 和 replay 输出；
- WebUI/API 的 HTTP/SSE/WebSocket、协议 payload、RunEvent 协议投影和 Responses background driver；剩余 adapter-owned canonical Run/Decision persistence 是有明确删除条件的迁移债务，不是目标职责；
- TUI 的 Bubble Tea state、终端交互和显示映射；TUI 的 Run/Approval/Question 以及 Context/Skills/Sandbox/Registry/MCP 已接入 Runtime，Question/A2A/Sub-agent/Delegate/Workflow/Cron hooks 仍由 CLI 注入；

目标结构如下：

```text
TUI adapter ───────────────┐
WebUI/API adapter ─────────┤
WeChat/Feishu adapter ─────┼── Agent Runtime ── AgentFactory ── Agent Core
ACP adapter ───────────────┘          │
                                      ├── Session
                                      ├── Tools Registry
                                      ├── MCP lifecycle
                                      ├── Skills/context
                                      ├── Provider/model
                                      ├── Sandbox
                                      ├── Approval/Question
                                      └── Run/events/cancel
```

职责边界：

### Agent Core

`internal/agent` 负责一次 Agent 执行本身：

- Provider 请求和流式事件；
- 工具调用与结果处理；
- Context、历史和 compaction；
- Agent mode 对工具执行的核心语义；
- 子 Agent、Delegate、Workflow；
- 事件生成、abort 和 terminal result。

Agent Core 不应知道 HTTP、WebSocket、Bubble Tea、微信、飞书或 ACP。

### Agent Runtime

新增 `internal/agentruntime`，作为与 `internal/agent`、`internal/session` 并列的前端中立 Runtime 包，负责跨入口共享的运行时能力。现有 `internal/serve/runtime` 保留为 Serve/Responses 的专用 background driver 支撑；不得把 ACP、TUI 或 Channel 的通用运行时依赖继续放入 `internal/serve/runtime`：

- Session Runtime；
- Agent/AgentFactory 组装；
- Tools Registry 构建；
- MCP 连接和清理；
- Skills/context 构建；
- Provider/model 运行时绑定；
- Sandbox 和工具策略；
- Mode/policy 解析；
- Approval/Question 抽象；
- Run 生命周期、取消、事件和锁；
- Session capability 持久化和恢复。

### Adapters

入口层只负责：

- 解析自己的输入协议；
- 创建入口特有的 Policy 和 Hook；
- 调用 Runtime；
- 将统一 Agent Event 映射为自己的输出格式；
- 处理 UI/协议交互。

适配层不应再直接拼接完整 `agent.Config`，也不应分别实现 MCP、Skill、Session 恢复或 Run 核心逻辑。

### Review 后验收与残余迁移债务（2026-08-15）

本轮代码核对和回归修复确认：Serve 子 Agent projection 的 terminal listener 竞态已消除；`DecisionService.Register` nil receiver、Channel high-risk pre-tool guard、标题生成 shutdown race、Cron active-job shutdown race 和 ACP prompt/durable Run identity 混用均已修复。`ResolvePolicy` 对 persisted binding/header/current 冲突和未知 source fail closed；forced `yolo` 在 Channel root、WebUI 恢复、ACP、ESM、Cron、recovery 和 managed child 路径保持一致。`SessionRuntime.Shutdown` 在有 loop owner 时 bounded wait，在无 owner 的 durable run 上补齐 terminal event/store transition，执行错误时仍清理 pending Decision；所有 active adapter 的 Decision resolution 都通过 `ResolveWith` 在 durable commit 成功后消费 pending identity。

已完成的大项：

1. **P0：Serve projection、DecisionService 和 Channel security。** child content/terminal 投影只发布一次终态；Runtime policy 先于 adapter hook，managed child 继承 hard guard，compound/path/flag/indirection 变体均有合同覆盖。
2. **P1：Source/Mode 和生命周期边界。** persisted binding/header 是 authoritative source；strict conflict rejection、forced mode、CLI/ACP/WebUI/Channel/TUI shutdown boundary、Cron/title task tracking 已落地。
3. **P1：Durable Run/Decision 一致性。** Begin/Update/Finish 串行化并支持 retry/idempotence；recovery event 使用确定性 ID；Decision callback/commit failure 保留 pending identity；ACP orphan recovery 不会伪造本地 Agent reattach。

保留的迁移债务（均有明确 owner 和删除条件，不得扩展为新的 Runtime）：

1. `internal/serve/openaiapi/run_manager.go` 只保留内存 event fan-out 和查询兼容；当所有 API/SSE 订阅改为直接消费 Runtime event broker 后删除 `Create/Finish/Cancel` 写入桥。
2. TUI 的 A2A/Sub-agent/Delegate/Workflow/Cron registry hooks 仍由 CLI 注入；当 capability registry contract 能表达这些依赖并有跨入口测试后，迁移到 `agentruntime.Builder`。
3. Responses provider-specific polling/continuation 和 ACP/Channel/WebUI payload projection 仍由 adapter 负责；它们必须继续使用 canonical Run/Decision/Source/Policy，不得新增本地状态机。
4. ACP 进程重启不恢复内存 Agent/回调；只有同进程 Runtime reconnect 重新投影 pending Decision。若未来 provider/agent loop 提供可恢复执行句柄，再增加显式 `Reattach` contract 和进程级测试。
5. Serve 的只读工作区投影（`/api/git/*`、`/api/files/*`）目前住在 adapter 层：root 解析在 `internal/serve/workspace_target.go`（session 走 canonical `GetSessionWorkDir`，draft 允许显式 path），containment/read 在 `internal/workspacefiles`，外部拉起在 `internal/workspaceopen`。owner: serve/WebUI workbench panels。删除条件: ACP/Desktop 落地对应的 `mothx/files/*`、`mothx/git/*` 投影时，root 决策迁入 `internal/agentruntime`（与 session identity 同源），两个共享包保持中立并被两侧复用；届时删除 `workspace_target.go` 桥。在此之前禁止任何入口复制第二套路径清洗/包含逻辑或 launcher 表。

因此 Phase 12 已完成当前 proposal 的可执行边界；上述债务属于后续小步迁移，不再阻塞核心 Runtime 统一验收。

## 1.3 后续演进：Router 模式与外部 ACP 子代理（2026-11-03）

Phase 12 之后新增的 Router 工作没有引入第二套运行语义，而是把「外部 ACP worker」表达为共享 Runtime 的一等模式与能力。详见 [Router / 外部 ACP 子代理方案](./acp-client-external-subagent-proposal.md)。

对核心边界的验证：

- **Source/Policy 复用**：`ModeRouter` 进入共享 mode resolver，`ResolvePolicy` 解析出 Router 专属的工具围栏与能力；WeChat/Feishu 的 source-forced `yolo` 仍然优先，Router 不会覆盖它。
- **一个构造路径**：Router lead 仍然经 `SessionRuntime.BuildAgent` 构造；没有 eligible `RouterBinding` 时对 Router 失败关闭，因此不会造出「委派必然失败」的 Agent。
- **一个执行/生命周期路径**：外部 worker 是 Runtime 拥有的 child backend，共享 canonical child event、单一 `EventRunFinished`、决策账本与进程树收敛；`internal/acp/client` 只做传输，不持有进程、不持有持久化、不依赖 provider。
- **一个 session 事实所有者**：`RouterBinding` 是可回放的 session 条目（legacy store、EventStore、display projection、fork、Runtime 解析均支持），不是适配器本地布尔值。
- **一个模式写入路径**：TUI 的 `/mode`、Tab 循环、`/systeminit`、`defaultMode` 变更与 `/router` 激活现在都经过 `SessionRuntime.SetMode` / `ActivateRouter`，适配器不再只改本地状态导致重开时回放旧模式。

新增的 Runtime 侧投影：

- `publishRouterTarget` 在每次工具组同步时把已激活的 worker ID 推送到 `delegate_subagent` 工具：工具描述点名该 worker，`agent` schema 始终用 `const` 钉死已激活值（即使配置了多个 profile），其他 profile 只出现在纠错文本中，传错名字时错误消息直接给出正确值。这是 Runtime 拥有的投影，不是适配层提示。
- `agentruntime.ACPAgentPresets()` 记录已验证的启动配方；TUI `/router` 面板以预设选择器作为新建入口，避免用户必须知道 `codex acp` / `claude acp` 并不启动 ACP server。

Phase 4 剩余项（异步 `subagent_spawn`、多 profile fan-out、mailbox wakeup、client-owned fs/terminal bridge、URL 认证、远程 session 复用、managed worktree change-set）仍为刻意延后项，需要各自的前置方案。

## 2. 方案范围与术语

### 过渡期与目标职责边界

迁移期间允许 Adapter 暂时保留部分 Runtime 编排，但必须明确区分“协议适配”和“核心生命周期”。最终边界如下：

| 职责 | 当前/过渡期负责方 | 目标负责方 |
|---|---|---|
| Agent Event 生成 | Agent Core | Agent Core |
| Run 状态机、锁和 terminal state | Runtime 与 Adapter 混合 | ExecutionRuntime |
| 协议事件映射 | Adapter | Adapter |
| Run/Event 核心持久化 | WebUI/Channel/ACP 部分自有 | ExecutionRuntime |
| Approval/Question 传输 | Adapter | Adapter |
| Approval/Question 状态、决议和恢复 | Runtime 与 Adapter 混合 | DecisionService/DecisionRecord |
| HTTP、SSE、WebSocket、JSON-RPC、平台消息 | Adapter | Adapter |

因此，Channel 的 `ProgressFunc`、平台消息格式和 ACP 的 JSON-RPC framing 可以长期保留在 Adapter；但 Run 的核心状态、决议关联、取消和恢复不能继续形成入口专属实现。

## 3. 当前状态评估

| 区域 | 当前复用度 | 与目标偏离 | 结论 |
|---|---:|---:|---|
| Agent loop | 75%～85% | 较小 | 已是共享核心，不应重写 |
| Provider/factory | 80%～90% | 较小 | 方向正确，继续作为唯一创建入口 |
| Tools 实现 | 80%～90% | 较小 | 实现集中，Registry 装配仍重复 |
| MCP | 70%～85% | 中等 | 底层共享，连接生命周期分散 |
| Skills/context | 70%～80% | 中等 | Manager 共享，加载时机和上下文拼接分散 |
| Session 持久化 | 75%～85% | 中等 | 数据层统一，内存 Runtime 类型分裂 |
| Session Runtime | 35%～50% | 明显 | `APISession`、`ChannelSession`、ACP runtime、TUI state 各自维护 |
| Mode/policy | 35%～50% | 明显 | 默认值、强制策略和恢复逻辑不统一 |
| Approval | 40%～55% | 明显 | 入口回调存在，但缺少统一 pending/decision 模型 |
| Run lifecycle | 40%～55% | 明显 | WebUI、Channel、ACP、TUI 各有编排逻辑 |
| 入口薄封装程度 | 40%～55% | 明显 | 入口仍承担大量 Runtime 职责 |

总体判断：项目整体距离目标架构约偏离 35%～45%，但核心能力已经具备，不需要推倒重写。

## 4. 必须保持的架构原则

1. **只有一个 Agent 核心执行语义。** 所有入口都调用同一个 Agent loop。
2. **只有一个 Agent Runtime 装配路径。** Provider、Session、Tools、MCP、Skills、Sandbox 和 Policy 必须由统一 Runtime 构造。
3. **入口差异通过 Policy 和 Adapter 表达。** 不能通过复制 Agent loop 或复制 Session Runtime 表达。
4. **Provider 行为继续经过 `internal/provider/factory`。** 入口不得自行判断 vendor 或绕过 factory。
5. **数据库访问严格遵循 `internal/db` → `internal/dao` → 业务逻辑。** `internal/db` 独占连接创建、缓存、SQLite 配置、迁移启动和关闭；`internal/dao` 独占所有 SQL、Bun 查询构造、表操作、持久化和行映射；`internal/session` 及其他业务/runtime 包只能调用 DAO 方法和受控事务边界，不得越过 DAO。
6. **事件内容可以适配，事件语义不能分裂。** ACP、WebUI、Channel、TUI 可以有不同线格式，但统一来自 Agent/Runtime Event。
7. **配置 schema 保持兼容。** 不改变 `settings.json`、`serve.json` 现有字段语义，新增字段需单独评审。
8. **能力默认值必须在一个 resolver 中决定。** 展示、run、agent、approval、event 不得各自 fallback。

## 5. 核心设计

### 5.1 Runtime Source

Session 必须使用现有 binding/header 或新增兼容字段，记录或可靠推导其来源。当前项目已有 Channel binding 基础，优先复用它：

```go
type RuntimeSource string

const (
    SourceTUI     RuntimeSource = "tui"
    SourceWebUI   RuntimeSource = "webui"
    SourceWeChat  RuntimeSource = "wechat"
    SourceFeishu  RuntimeSource = "feishu"
    SourceACP     RuntimeSource = "acp"
    SourceCLI     RuntimeSource = "cli"
)
```

来源不是展示字段，而是影响默认 mode、approval、sandbox、工具和能力边界的运行时输入。`channel_type=wechat/feishu` 应映射为强制 yolo 的 Runtime Policy；不能只把它作为会话列表的展示字段。

### 5.2 ExecutionPolicy

入口通过 Policy 描述场景差异：

```go
type ExecutionPolicy struct {
    Source                  RuntimeSource
    ForcedMode              *Mode
    DefaultMode             Mode
    Approval                ApprovalPolicy
    Question                QuestionPolicy
    Tools                   ToolPolicy
    MCP                     MCPPolicy
    Skills                  SkillPolicy
    Sandbox                 SandboxPolicy
    MultiAgent              MultiAgentPolicy
    AllowInteractiveInput   bool
}
```

Policy 只描述限制和默认值。最终有效配置必须由 Runtime 统一计算，不能由入口直接修改 Agent config。

### 5.3 ModeResolver

统一提供：

```go
func ResolveMode(policy ExecutionPolicy, sessionMode, requestedMode string) (Mode, error)
```

规则：

```text
1. ForcedMode 存在时，始终使用 ForcedMode。
2. 否则使用合法的显式 requestedMode。
3. 否则使用合法的 sessionMode。
4. 否则使用 policy.DefaultMode。
5. 所有结果必须是 plan、agent、yolo 之一。
```

微信和飞书必须使用强制策略：

```text
SourceWeChat / SourceFeishu
    ForcedMode = yolo
```

因此微信/飞书的以下路径全部必须得到 `yolo`：

- Channel 直接执行；
- WebUI 恢复后执行；
- chat completion；
- submit run；
- background run；
- retry/recovery；
- compaction；
- sub-agent 的继承策略；
- run record 和 run event；
- approval event；
- agent.Config。

普通 API Session 仍可保持现有语义：明确 mode 优先，空 mode 使用 API `DefaultMode`。不能把所有空 mode 全局变成 yolo。

### Source 的可信来源与冲突处理

Source 不是请求方可以任意覆盖的展示字段。对于已有绑定的 Session，Runtime 必须按以下顺序解析来源：

```text
1. 持久化 binding record（channel_type/channel_id）
2. 持久化 Session header
3. 当前 Runtime 已绑定的 source
4. request source（仅适用于尚未绑定的新 Session）
5. unknown
```

当多个持久化来源冲突时，不得静默采用较宽松的策略；应记录冲突并采用更严格的有效 Policy，必要时拒绝运行。已绑定的 WeChat/Feishu Session 不能通过 request source、requested mode 或 WebUI 恢复路径降级为普通 API Session。

对于其他 Policy，统一采用以下优先级：

```text
hard policy limits  >  session capabilities  >  request options  >  runtime defaults
```

`hard policy limits` 包括 Channel Security、Sandbox 和高风险命令保护，不能被 Session 或 request 降低；request 只能在允许范围内选择工具、MCP、Skill 和交互能力。

### 5.4 SessionRuntime

建议引入统一的前端中立结构：

```go
type SessionRuntime struct {
    ID          string
    Source      RuntimeSource
    WorkDir     string
    Manager     *session.Manager
    Registry    *tools.Registry
    SandboxMgr  *sandbox.Manager
    Skills      *skills.Manager
    MCPClients  []*mcp.Client
    AgentMgr    *agent.AgentManager
    Capabilities Capabilities
    Policy      ExecutionPolicy
    LastUsed    time.Time
}
```

它是 `APISession`、`ChannelSession`、ACP `sessionRuntime` 和 TUI session runtime 的共享语义模型。入口可以持有自己的包装类型，但不能重新定义一套互不兼容的核心状态。

当前 `SessionRuntime` 的资源所有权和并发不变量必须明确：

- 一个 `SessionRuntime` 只有一个资源清理所有者，`Close` 必须幂等；
- 开始关闭后不得创建新 Run，关闭前应先取消 active Run；
- MCP client 只能关闭一次；
- active Run 期间不得修改影响该 Run 的 Registry 基础结构；
- `SessionRuntime` 关闭后不得继续被 Agent 或 Adapter 使用；
- Session 是否允许并发 Run 必须由 Runtime 统一决定，而不能由入口自行实现。

### 5.5 RuntimeBuilder

RuntimeBuilder 负责：

1. 校验和打开 Session；
2. 加载 Session capabilities；
3. 应用 Source/Policy 默认值；
4. 创建 Sandbox；
5. 加载 context files、rule 和 Skills；
6. 构建 Registry；
7. 注册 builtin/optional tools；
8. 连接 MCP；
9. 创建 AgentFactory/AgentManager；
10. 恢复历史；
11. 提供统一的 Agent 和 Run 创建入口。

建议接口形态：

```go
type Service struct { ... }

func NewService(cfg ServiceConfig) (*Service, error)
func (s *Service) CreateSession(ctx context.Context, opts SessionOptions) (*SessionRuntime, error)
func (s *Service) OpenSession(ctx context.Context, id string, opts OpenSessionOptions) (*SessionRuntime, error)
func (s *Service) CloseSession(ctx context.Context, id string) error
func (s *Service) DeleteSession(ctx context.Context, id string) error
func (s *Service) GetCapabilities(ctx context.Context, id string) (*Capabilities, error)
func (s *Service) PatchCapabilities(ctx context.Context, id string, patch CapabilityPatch) (*Capabilities, error)
func (s *Service) RunPrompt(ctx context.Context, id string, input PromptInput, hooks RunHooks) (*Run, error)
func (s *Service) CancelRun(ctx context.Context, sessionID, runID string) error
```

接口名称可以调整，但职责必须保持前端中立。

## 6. Approval 和 Question 统一模型

当前 `func(...) bool` callback 不足以表达挂起、拒绝、取消、超时和持久化决议。建议升级为：

```go
type ApprovalDecision string

const (
    ApprovalAllow   ApprovalDecision = "allow"
    ApprovalDeny    ApprovalDecision = "deny"
    ApprovalCancel  ApprovalDecision = "cancel"
    ApprovalTimeout ApprovalDecision = "timeout"
)

type ApprovalService interface {
    Create(context.Context, ApprovalRequest) (ApprovalTicket, error)
    Get(context.Context, string) (ApprovalTicket, error)
    Resolve(context.Context, string, ApprovalDecision) error
    Cancel(context.Context, string) error
}
```

`Request(...) (ApprovalDecision, error)` 可以作为纯同步 Adapter 的内部辅助接口，但不能作为跨入口 durable decision service 的唯一模型。Runtime 必须能够在没有立即决议时将 Run 置为 `waiting_for_approval`，并在进程重启、客户端重连、超时、取消或重复决议时保持一致状态。

各入口实现自己的 adapter：

- TUI：终端交互；
- WebUI：pending approval + HTTP/WebSocket resolution；
- WeChat/Feishu：根据强制 yolo 和 Channel security 自动决策；
- ACP：映射到 `session/request_permission`。

共享层负责：

- approval ID；
- run/session/tool call 关联；
- context cancel；
- timeout；
- event persistence；
- terminal state。

`QuestionService` 采用同样方向，但允许入口决定是否支持交互式提问。Channel 默认不应依赖无法送达的交互式 question。

## 7. Tool/MCP/Skill 统一装配

新增统一 RegistryBuilder，集中处理：

- builtin tools；
- plan tool；
- question tool；
- skill_ref；
- browser；
- image generation；
- cron；
- A2A；
- sub-agent/delegate/workflow；
- external tools；
- MCP tools；
- tool filters；
- per-session capability 开关。

入口只提供 `ToolPolicy` 和 `MCPPolicy`，不再分别复制：

```go
registry := tools.NewRegistry(...)
mcp.LoadConfiguredServers(...)
mcp.ConnectServers(...)
registry.Register(...)
```

MCP client 的连接和关闭必须绑定到 `SessionRuntime` 生命周期，避免 ACP、WebUI、Channel 分别泄漏或采用不同清理规则。

Skills 的全局/项目目录发现、Load、context 构建也归入 RuntimeBuilder；入口只决定激活哪些 Skill 以及如何展示结果。

## 8. AgentFactory 使用规则

现有 `internal/agent.AgentFactory` 是目标架构的重要基础，应提升为所有入口的标准 Agent 创建入口。

目标规则：

```text
adapter 不直接构造完整 agent.Config
adapter 不直接调用 agent.New / agent.NewWithLoopConfig
adapter 只调用 Runtime/AgentFactory 的 Create
```

允许的入口特有参数包括：

- Agent ID；
- parent ID；
- 用户请求文本；
- 入口专属 Approval/Question service；
- 事件 sink；
- 明确的 per-run options。

不应由入口自行决定：

- provider/model 绑定方式；
- mode fallback；
- Registry 基础工具；
- MCP/Skills 初始化；
- Session replay；
- Sandbox 基础策略；
- 子 Agent 默认继承规则。

## 9. Run/Execution Runtime

### 9.1 Run 状态与 ExecutionRuntime 边界

`RunExecutor` 当前主要服务于 WebUI，应演进为跨入口的 Execution Runtime。ExecutionRuntime 的目标不是只记录 `Begin/Cancel/SetAgent/Finish`，而是统一 Run 的状态机和生命周期。建议至少定义以下状态：

```text
created
running
waiting_for_approval
waiting_for_question
cancelling
completed
failed
cancelled
timed_out
```

当配置了 Runtime-owned store/sink 时，ExecutionRuntime 负责创建 Run ID、解析最终 Policy/Mode、建立锁、创建或接收由 Runtime/AgentFactory 构建的 Agent、启动 Agent loop、转发统一 Event、处理 decision、取消、terminalization、usage、canonical Run transition 和通用 recovery/replay。Adapter 仍可提供兼容 sink、admission lock、协议输出、交互传输、事件映射和 provider-specific driver；这些是有删除条件的迁移桥，不得形成第二套状态机。

`SetAgent` 仅允许作为迁移期兼容接口；目标状态下 Agent 创建应由 Runtime/AgentFactory 完成并在 Run 创建过程中完成绑定。

### 9.2 共享职责

- 创建 run ID；
- 建立 session/run 锁；
- 解析最终 Policy 和 Mode；
- 创建 Agent；
- 启动 Agent loop；
- 转发统一 Event；
- 处理 approval/question；
- cancel/abort；
- terminalization；
- usage；
- run、run event 和 approval event 持久化；
- recovery/replay 的通用部分。

以下内容留在适配层：

- WebUI 的 HTTP 状态码、SSE、WebSocket；
- TUI 的 Bubble Tea message；
- Channel 的 ProgressFunc 和平台消息；
- ACP 的 JSON-RPC notification；
- OpenAI-compatible response JSON。

Responses API 的 provider-specific background driver 可以继续保留在 serve/runtime，但应通过通用 Run/Execution 接口接入，不应复制普通 Agent 的生命周期语义。

## 10. 四类适配层的目标职责

### TUI

保留：

- Bubble Tea model/update/view；
- 输入历史和终端渲染；
- terminal approval/question；
- Agent Event 到终端内容的映射；
- TUI 特有命令和显示状态。

下沉：

- Agent 装配；
- Session replay；
- Registry/MCP/Skills 构建；
- mode/policy resolution；
- 通用 run/cancel 生命周期。

TUI 可以是第一阶段暂不迁移的适配层，但长期目标仍是使用同一个 Runtime。

### WebUI/API

保留：

- HTTP handlers；
- JSON/OpenAI API compatibility；
- WebSocket/SSE；
- WebUI 专属响应和事件格式。

下沉：

- `APISession` 的核心 Runtime 状态；
- `buildSessionResources`；
- `syncSessionTools`；
- capability persistence；
- Agent config 组装；
- run 核心生命周期。

### WeChat/Feishu Channel

保留：

- 平台鉴权和消息收发；
- 用户/平台 identity 映射；
- ProgressFunc；
- 平台错误和消息格式；
- Channel-specific Policy 创建。

必须下沉或统一：

- Session 创建/恢复；
- Agent 组装；
- MCP/Skills/Tools；
- run lifecycle；
- mode resolver。

必须强制：

```text
WeChat/Feishu policy.ForcedMode = yolo
```

无论 WebUI 是否打开过该 Session、是否有 capability 记录、请求是否省略 mode，实际执行都必须是 yolo。

### ACP

保留：

- stdio JSON-RPC framing；
- ACP method dispatch；
- ACP request/notification 映射；
- ACP approval/question 输出；
- ACP protocol error mapping。

下沉：

- `sessionRuntime` 的核心实现；
- Registry/MCP/Skills setup；
- Agent 创建；
- Session replay；
- cancel 和 run lifecycle。

## 11. 分阶段实施计划

### Phase 0：建立不变量和测试 ✅ 已完成

已建立并验证跨入口不变量：

- WeChat/Feishu session 的 effective mode 永远是 yolo；
- runtime 展示 mode 与 run/agent/event mode 一致；
- 普通 API 空 mode 仍使用 API DefaultMode；
- Session 重启后 capability 和 source 可恢复；
- approval payload 的 mode、run ID、tool call ID 保持一致。

跨入口完整 durable Approval/Question service 仍属于后续收敛工作；当前已完成 `DecisionService` identity、清理、合同测试以及部分 resolver 接入。

### Phase 1：统一 Mode/Source/Policy ✅ 已完成

已完成：

- `RuntimeSource` 到现有 channel binding/header 的映射；
- `ExecutionPolicy`；
- `ModeResolver`；
- Channel forced `yolo`；
- run/agent/approval/event mode 统一解析；
- `/mode`、external background、WebUI submit、chat completion、recovery 等入口的 policy 检查。

后续可继续扩展统一 policy schema，使 sandbox/tools/MCP/approval policy 也由同一策略对象表达。

### Phase 2：抽取 Session Runtime 和资源构建 ✅ 核心完成

已从 WebUI/Serve 资源装配中抽取并接入 `internal/agentruntime`：

- `SessionRuntime`、`Builder`、`AttachSessionResources`；
- Context/Skills/Rule/Sandbox 初始化与刷新；
- `RegistryPolicy`、`RegistryMutator`、`BuildRegistry`；
- 严格/可选 `MCPPolicy` 和 Runtime-owned MCP cleanup；
- `CreateSession`、`OpenSession`、`DeleteSession`；
- `NewAgentManager`、`SessionRuntime.BuildAgent`；
- 基础 `ExecutionRuntime`。

OpenAI API 的 HTTP/approval/run orchestration 仍部分保留在 adapter，后续需要继续收敛。

### Phase 3：迁移 Channel ✅ 已完成当前方案边界

Channel 已使用共享 Runtime：

- Session 创建/恢复通过 `agentruntime`；
- Registry/MCP/Skills/Context/Rule 通过 Runtime policy；
- 普通 Agent 创建通过 `SessionRuntime.BuildAgent`；
- active run 的 Begin/Cancel/SetAgent/Finish 通过 `ExecutionRuntime`；
- 保留 Channel adapter 的 forced yolo、Security、Hooks、ProgressFunc、watchdog、消息和 run event 映射。

Channel 的平台持久化和协议事件映射仍属于 adapter 预期职责。

### Phase 4：迁移 ACP ✅ 已完成当前方案边界

ACP 已收敛为协议适配层：

- Session 创建/恢复通过 `agentruntime`；
- Registry/MCP/Skills/Context/Rule 通过 Runtime policy；
- 普通 Agent 创建通过 `SessionRuntime.BuildAgent`；
- prompt active state/cancel/agent abort/finish 通过 `ExecutionRuntime`；
- 保留 ACP JSON-RPC framing、permission/question、MCP callbacks、protocol error、replay 和 rollback。

跨入口 Run/Approval/Question 的 identity、状态转换和 Runtime 持久化边界已经统一；JSON-RPC/HTTP/Channel/TUI 的 payload、callback 和事件投影仍由各 adapter 负责。

### DecisionService 迁移进度

当前已落地的共享决策边界：

- `DecisionService` 管理 Approval/Question 的 pending identity、重复注册、first-response-wins、按 Run 清理、取消/超时清理和可选 resolver callback；
- WebUI/API 已接入 Approval/Question identity、resolver 和 Runtime snapshot identity 校验；pending map 仅保留协议 payload，不再持有 Agent 指针；Decision request 持久化 deadline，startup orphan recovery 会完整 replay 并同时取消 pending Approval/Question；
- TUI 已接入 Approval/Question identity、resolver 和 waiting/resume；Run 正常结束或 reset 会 durable 取消未决 Decision，Session 启动/切换会 replay durable records，并将过期 decision 标记 `timed_out`、执行栈不可恢复的 pending decision 标记 `cancelled`，同时终结 orphan TUI Run；
- ACP 已接入 Permission/Question identity、超时、取消、Session 关闭清理；同一进程 reconnect 会 replay durable request/resolution，并对仍存活的 execution 重新发送 `_mothx/request_question` 或 `session/request_permission` projection。进程重启后本地 Agent/callback 栈不可恢复，因此 ACP-owned orphan Run 标记 `failed`，pending Decision terminalize 为 `cancelled`，不会错误 revive；
- Channel 已接入 Approval/Question identity、resolver 和 Question observer 边界；无正式外部协议时安全取消并将 durable Question 记录为立即过期。

当前仍保留在 Adapter 的显式迁移桥：

- WebUI、ACP、Channel、TUI 各自的协议 payload、response channel、UI 队列和事件映射；
- ACP 请求响应 channel、同进程 callback 重绑、超时调度和协议 projection；canonical Decision/Run transition 已通过 Runtime `ResolveWith`/ExecutionRuntime 接入，仍有少量旧 persistence/query wrapper 待删除；
- Responses background 的专用恢复逻辑。

DecisionService 收敛大项已完成当前边界。后续删除债务转入：

1. 在存在可恢复执行栈的 adapter 中继续扩展协议 callback 重绑和客户端重连重发；不可恢复的本地进程栈保持显式 terminalization；
2. 清理 WebUI Responses background/ESM/recovery 中剩余旧 RunManager 持久化/query wrapper；
3. 继续下沉 TUI capability hooks，并在 capability contract 稳定后移除 CLI/RegistryHook 注入桥；
4. 增加跨入口真实集成测试，覆盖重启、取消、超时、重复 resolve 和客户端重连。

## 12. 兼容性和迁移要求

- 不修改既有 `settings.json`、`serve.json` 字段语义；
- 不改变 OpenAI-compatible API 形状；
- 不改变 ACP 外部协议，除非增加可选扩展；
- 不改变 Channel 对外消息格式；
- 现有 Session 数据可继续打开；
- 旧的空 capability mode 必须按照来源 policy 恢复；
- 对无法确认来源的旧 Session，不得静默覆盖用户明确持久化的合法 mode；
- Channel Session 优先通过现有 `channel_type`/`channel_id`、Session header 和 binding API 识别，不能只依赖 WebUI 侧推测；
- 对现有已绑定的微信/飞书 Session，Policy 必须覆盖空 mode、显式 agent/plan、外部 background 请求和 recovery 路径，最终强制为 yolo；
- 迁移期间允许 adapter wrapper 存在，但不能新增第二套核心规则。

## 13. 验收标准

### 核心一致性

- 四类入口使用同一个 Agent loop；
- 所有 Agent 创建最终经过统一 Factory/Runtime；
- 新增工具、MCP transport 或 Skill 能力时，核心实现只需增加一份；
- 新增能力后，各入口只需增加协议映射或 Policy 配置。

### Channel 强制策略

- WeChat/Feishu 所有执行路径最终 mode 都是 `yolo`；
- WebUI 恢复同一 Channel Session 后仍是 `yolo`；
- run、run event、approval event、agent config 的 mode 全部为 `yolo`；
- 请求传入 `agent` 或 `plan` 时，Channel forced policy 仍不会被覆盖。

### 普通 API 行为

- 普通 API session 的显式 mode 仍然优先；
- 普通 API session 的空 mode 仍按 API `DefaultMode`；
- runtime snapshot 不再使用与执行路径不同的展示专用 fallback。

### 资源和生命周期

- MCP clients 在 Session Runtime 关闭时可靠清理；
- Skills/context 只由共享 Runtime 构建；
- Session replay 在不同入口使用相同规则；
- 已迁移路径的 Run/Approval/Question 核心 identity、状态转换和 canonical 持久化由 Runtime service 负责；命名兼容桥仍可保留 adapter sink/query，但必须有 owner、合同测试和删除条件；
- 数据库没有兼容桥：不得以 wrapper、allowlist 或“旧会话”名义在 DAO 外执行 SQL。所有新增数据库代码必须先进入 `internal/dao`，并由架构守卫拒绝业务层的 `database/sql`、`uptrace/bun`、`Query`/`Exec` 和 Bun 查询构造器。
- Adapter 负责协议和交互传输，并可保留协议专属事件映射、provider driver 和 admission 适配；
- 新增 `internal/agentruntime` 的 focused tests，并覆盖 Session 恢复、Source 冲突、Channel forced `yolo` 和 terminal state；
- 典型验证命令：

```bash
go test ./internal/agentruntime/... ./internal/agent/... ./internal/provider/... ./internal/session/...
go test ./internal/serve/... ./internal/acp/... ./internal/tui/...
```

具体入口集成测试还应覆盖 Channel 包及 WebUI/API 的 approval/recovery 路径。

## 14. 风险与控制措施

### 抽取边界过大

先从 Mode/Policy 和资源构建开始，不一次性重写 WebUI、Channel、ACP。每一阶段保持旧 adapter 行为不变。

### Approval 挂起模型复杂

先把现有 WebUI approval 收敛成明确接口，再迁移到共享 Runtime。不要直接把当前 `bool` callback 原样扩大到所有入口。

### Channel 安全回归

Channel forced yolo 不是“无条件放开所有高风险操作”。mode、sandbox、allow policy 和高风险命令拦截仍然是不同维度。必须保留 Channel security 的高风险保护规则。

### Session 来源丢失

新增 source metadata 时要兼容旧 Session。对旧 Channel session，优先从 channel metadata、session header 或稳定关联信息恢复来源；无法确认时不能把普通 API session 误判为 Channel。

### Provider 特殊后台任务

Responses background run、A2A 和外部子 Agent 可以有专属 driver，但必须通过统一的 session/run/policy 语义接入，不能重新定义另一套 mode 和 approval 规则。

## 15. 本次 Review 的最终结论

本次迁移已将 MothX 的生产 Agent 构造、Source/Policy、Session 资源、durable Run transition、Decision identity/commit、恢复和关闭边界收敛到 `internal/agentruntime`。当前代码满足“一个 Agent Core、一个前端中立 Runtime、多个薄 Adapter”的主要合同；剩余工作是删除已命名的兼容桥，而不是再创建第二套 Runtime。

当前结构是：

```text
Source → Policy → SessionRuntime → AgentFactory → ExecutionRuntime → Adapter
```

其中：

- `Source` 表示 TUI/WebUI/WeChat/Feishu/ACP 等场景；
- `Policy` 表示该场景允许和强制的行为；
- `SessionRuntime` 统一 Session、Tools、MCP、Skills、Sandbox 和 capabilities；
- `AgentFactory` 统一 Agent 创建；
- `ExecutionRuntime` 统一 run、approval、cancel、event 和 terminal state；
- `Adapter` 只负责协议、交互和输出映射。

它实现目标：

> 一个完整的 Agent 核心，拥有完整的 Tools/MCP/Skill/Session/多厂商兼容能力；TUI、WebUI、Channel、ACP 是不同的薄封装，只根据场景注入不同权限和配置，而不是各自实现自己的 Agent 功能。
