# 主循环迭代预算：感知与有界续期方案

> 状态：Proposal
> 日期：2026-09-17
> 范围：`internal/agent` 主循环 + `internal/agentruntime` 策略解析
> 目标：让模型在逼近 `MaxIterations` 时能感知剩余预算，并在 Runtime 有界放行的前提下续期，而不是数到上限就硬切 `incomplete`

## 1. 背景与问题

主循环的迭代上限是一个**隐藏的硬计数器**：

- 循环条件 `for i := 0; i < a.config.MaxIterations; i++`（`internal/agent/agent.go:1376`），run 内没有任何路径修改它。
- 默认值 200（`agent.go:619`、`agent.go:658`），由适配器按需覆盖（渠道 `MaxTurns` 默认 90，`internal/serve/channels/config.go:147`、`internal/serve/channels/dispatcher.go:2262`；子代理默认 50，`internal/agent/subagent.go:93`/`:325`；ESM worker 200 / critic·audit 80 / observer 40，`internal/esm/runtime_core.go:140`/`:146`/`:236`）。
- 耗尽后直接 `emitRunFinished(ch, TaskIncomplete, "max_iterations", ...)`（`agent.go:1986-1988`），TUI 显示 `会话结束：incomplete`（`internal/tui/agent_events.go:275-288`）。

由此产生两个问题：

1. **模型感知不到预算。** 剩余 ≤ 阈值（默认 20%）时确实会发 `EventBudgetPressure`（`agent.go:1900-1921`），但它只被适配器渲染——TUI 打印到转录区（`internal/tui/agent_events.go:450-457`），渠道走 progress（`internal/serve/channels/dispatcher.go:2573-2578`）——**从未注入到模型可见的 messages**。`[session context]` 注入消息（`internal/agent/agent_context.go:215-237`）也只含日期/模型/工作目录/mode。
2. **没有续期通道。** `PrepareNextTurn` 返回的 `TurnUpdate` 只有 `Context` / `Model` / `ThinkingLevel`（`agent.go:259-264`），改不了上限；`GetFollowUpMessages`（`agent.go:1759-1762`）只在“本轮无 tool call、即将正常结束”时注入消息并 `continue`，挂在循环**内部**的正常出口，循环计数耗尽时不会再被调用。

结果是：一个确实在推进、只是需要更多轮的长任务，会因为数到 200 而被判为 `incomplete`，用户看到的是“失败”语义，模型也没有机会收敛或说明。

## 2. 设计目标与非目标

### 2.1 目标

1. **模型可见**：逼近上限时，模型能在下一轮请求中看到剩余轮数与建议动作。
2. **放行在 Runtime**：模型只负责提出申请（工具调用），是否放行由 Runtime 确定性 clamp，不由适配器各自实现。
3. **有界**：续期受硬上限与续期次数上限约束，不允许无限延长。
4. **零缓存代价**：注入不得破坏 prompt cache 前缀（见 §6）。
5. **零新机制**：复用已有的 `SystemInjected` 注入、一次性阈值事件、软限恢复模式。
6. **终态语义不变**：续期不产生终态事件；每条退出路径仍恰好一个 `EventRunFinished`。

### 2.2 非目标

- 不取消 `MaxIterations`，也不把上限交给适配器任意修改。
- 不为子代理 / Expert 成员提供续期（它们受能力上限约束，只能收窄）。
- 不引入新的运行状态机、新的 run 记录、或与 `ExecutionRuntime` 平行的生命周期。
- 不做无约束的自动放行：任何续期都必须经过 Runtime clamp（`Hard` / `MaxRenewals` / `MinInterval`）。

## 3. 核心设计

> 决策（2026-09-17）：**续期由模型工具申请发起，Runtime 只做确定性 clamp**，不做“Runtime 自动按进展放行”。

把“轮数”从隐藏硬计数器降格为**模型可见、Runtime 校验、带硬上限的策略参数**。续期退化为循环里一次普通的 `continue`。

三层：

### 3.1 感知层：阈值触发的一次性 SystemInjected 注入

不改 `[session context]`（它是每轮重建的稳定前缀消息，见 §6）。而是在剩余轮数 ≤ 软阈值时，注入一条独立的 `SystemInjected` 消息，追加到 run 上下文的**尾部**：

```text
[Budget Pressure] 18/200 turns remaining (9%).
Complete the current task and summarize progress. If the task is genuinely
unfinished, call extend_budget with a concrete reason.
```

注入位置选在现有的预算检查处（`agent.go:1900-1921`）——那时本轮 tool 结果已 append（`agent.go:1806-1820`）、`EventTurnEnd` 已发（`agent.go:1872`），下一轮请求尚未构建，所以消息正好落在尾部，模型下一轮立即看到。与 `stuck` 检测注入 warning（`agent.go:1836-1868`）是同一模式。

实现要点：

- **一次性**：复用 `budgetPressureFired` 语义，只在跨阈值那一刻注入一条；续期后进入下一个阈值再注入一条。一个 run 约 2–4 条。
- **append-only**：绝不原地覆写旧注入（否则每轮失效，见 §6）。
- **不持久化**：写入 `a.messages` / `a.context.Messages`（run 上下文），不调用 `Session.AppendMessage`。这样 durable replay / 重连不会带过期计数污染历史；重连后剩余量重算，低于阈值会再次注入。注意这与 `stuck` warning 不同——那个是持久化的，因为它解释了一次真实干预。

### 3.2 决策层：模型经工具申请，Runtime 只做 clamp

**续期由模型主动发起**，通道是一个普通工具调用，而不是 Runtime 的自动放行：

```text
extend_budget(reason: string, additional_turns?: int)
  -> { granted, remaining, soft_limit, hard_limit, renewals_used }
```

模型在 §3.1 的预算通知里被告知可以调用它；调用后走正常工具管线（`BeforeToolCall` / 审批 / 执行），Runtime 侧只做确定性 clamp：

- 不超过硬上限 `Hard`；
- 每 run 不超过 `MaxRenewals` 次；
- 两次续期之间满足最小轮数间隔（避免连续刷）；
- 仅主 run 可用（子代理 / Expert 成员禁用）；
- 模式与 allow / 审批策略允许时才暴露该工具。

`reason` 必填，作为可审计依据写入 transcript 与 run 事件。

这样**申请是显式工具调用，放行是确定性 clamp**，不依赖“进展判定”，也不依赖模型自述之外的推断。防跑飞仍由既有机制承担：`stuck` 检测（`agent.go:1836-1868`）拦无进展 run，`MaxRenewals` + `Hard` 拦刷预算。整体与输出截断的“升级 + 续写、带上限”同构（`escalatedMaxTokens = 65536`、`maxOutputRecoveryAttempts = 3`，`agent.go:1243-1244`；恢复流程 `agent.go:1611-1650`）。

### 3.3 工具的实现形态（无状态、可审计）

- **定义在 `internal/agent`**，与 `delegate_subagent` / `subagent_spawn` 同层（`internal/agent/subagent.go:38` / `:229`）。原因：`internal/agent` 已依赖 `internal/tools`，工具不能反向导入 agent 包；而 agent 包内工具可以直接接触循环运行态。
- **无状态**：工具实例不持有 per-run 状态（符合 “tools should be stateless where practical”）。每 run 的预算句柄通过 run context 传递，沿用既有模式（`ContextWithParentRunContext` / `ParentRunContextFromContext`，`agent.go:123-132`）：循环把 `*iterationBudget` 挂到 `runCtx`，工具从 `ctx` 取出并调用 `Request(additional, reason)`。
- **循环同步**：工具执行后，循环从预算句柄同步局部 `limit`（见 §3.4）；放行时发一条普通状态事件，适配器只做投影，不新造事件语义。
- **非副作用工具**：不触碰文件系统，无需 durable 幂等 claim；是否需要审批由 allow / 模式策略决定，与其它工具一致。
- **成员禁用**：成员 / 子代理的工具集是能力上限，`extend_budget` 不得出现在其注册表（`subagent.go:318-322` 只收窄、不放宽）。

### 3.4 策略对象与解析位置

按 AGENTS.md 的 “one source-of-truth resolver”，预算与续期策略必须由 `internal/agentruntime` 解析一次并下发，不能在 TUI/ACP/渠道各写一套：

```go
// internal/agentruntime
type IterationBudgetPolicy struct {
    Soft        int           // 软上限（现状 MaxIterations）
    Hard        int           // 硬上限（不可突破）
    RenewFactor float64       // 每次续期的放大幅度
    MaxRenewals int           // 每 run 最大续期次数
    MinInterval int           // 两次续期之间的最小轮数间隔
    MaxWallClock time.Duration // 单 run 最长时长（默认 16h）
}
```

- 扩展 `AgentBuildOptions`（`internal/agentruntime/agent_build.go:41-43`）承载该策略，`agent_build.go:238` 传入循环。
- 现有 `BudgetPressureThreshold`（`serve.json` 的 `agent.budgetPressureThreshold`，`internal/serve/channels/config.go:94`）就是本策略的雏形，**扩展而非新增平行旋钮**。
- 终态与日志必须报一致的有效上限，因此循环内用显式局部 `limit`（初值 = Soft，工具放行后抬高，封顶 Hard），而不是直接改 `a.config.MaxIterations`；`agent.go:1986` 的消息同时报 Soft/Hard 与实际消耗。

### 3.5 终态语义不变

续期只 `continue`，不发终态。`EventRunFinished` 仍每条退出路径恰好一次（契约见 `internal/agent/terminal_contract_test.go`），run 状态机、`RunStore`、租约、审批/问题决策全不用动。`RunStateIncomplete`（`internal/agentruntime/execution.go:25`）与 ACP 的 `incomplete` 投影（`internal/acp/acp.go:3846-3859`）语义保持原样。

## 4. 注入时点（SystemInjected）

框架现有的 `SystemInjected` 注入点（均在 `internal/agent`）：

| 位置 | 时机 | 持久化 |
|---|---|---|
| `agent_context.go:215` `buildSessionContextMessage` | 每次请求构建（`agent.go:1419`），插到最前 | 否 |
| `agent_context.go:783` compaction 摘要 | 压缩后 | 是 |
| `agent.go:1839` stuck warning | 连续无文本达阈值，工具结果之后 | 是 |
| `agent.go:1638` / `agent_context.go:1059` 输出/流恢复 | 截断或流失败恢复时 | 是 |
| `agent.go:1966` steering（`GetSteeringMessages`） | 每轮尾部、`continue` 之前 | 是 |
| `agent.go:1760` follow-up（`GetFollowUpMessages`） | 无 tool call 即将结束时 | 是 |
| `mailbox.go:149` 成员完成通知 | 子代理入邮箱后 | 是 |

预算通知落在 **`agent.go:1900` 的预算检查处**，与 `stuck` warning 同层，但**不持久化**（§3.1）。

## 5. 参数建议

不拍孤立数字，按“软限倍数 + 有限续期次数”定义，并配时间护栏：

| 场景 | 软限 | 硬上限 | wall-clock | 续期 |
|---|---|---|---|---|
| 主 run（TUI/CLI/ACP/API/WebUI） | 200 | **400（2×）** | **16h** | 最多 2 次，每次 +50%（或 1 次 +100%），每次需模型调用 `extend_budget` 且带 `reason` |
| 渠道 | 90 | **180（2×）** | **16h** | 同上；渠道现有 `run_max_duration_secs`（14400s）提升到 57600s |
| 子代理 / Expert 成员 | 50 / frontmatter | **= 软限** | 继承父 run | 不续期（`subagent.go:318-322` 只能收窄） |
| 后台 run | 200 | 400 | 6h（`backgroundRunMaxSeconds`） | 另受后台轮询上限约束 |

理由：

- 2× 是有界且与输出侧“升到固定 escalated 上限”一致的最坏情况放大；`MaxRenewals = 2` 避免“续期变成常态”，`MinInterval` 避免连续刷。
- 轮数只是代理指标，真正的硬边界是成本与时间。渠道侧已有时间护栏（stale 600s、run max 14400s = 4h，`channels/config.go:102-103`），但 TUI/CLI/ACP 目前**没有**总时长上限。
- **wall-clock 上限统一取 16 小时（57600s）**：前台（TUI/CLI/ACP/API/WebUI）与渠道共用同一策略值；渠道现有 `run_max_duration_secs`（14400s = 4h）随之提升到 57600s。（注：相对 4h 是 4×，按 16h 记录。）
- 后台 durable 轮询仍由 `backgroundRunMaxSeconds`（6h）单独约束——那是轮询生命周期，不是 run 时长，保持不变。
- 最优雅的形态是让硬边界统一由策略表达为 `{软轮数, 硬轮数, 最长时长, 最大续期次数}`，而不是散落的魔法数字。

## 6. 缓存分析（关键）

机制：`selectCacheMarkers`（`agent_context.go:480-499`）从尾部往前找两条**非** `SystemInjected` 消息作为缓存断点，`applyCacheMarkers`（`:501`）在它们最后一个 content block 上打 `cache_control: ephemeral`。供应商 prompt cache 是**前缀精确匹配**——断点之前的任何 token 变化都会让整段前缀失效。

结论：

- **注入头部会破坏缓存。** 若在 `[session context]`（index 0）放每轮变化的倒计时，等于每轮作废断点之前的整段前缀。`[session context]` 现在不伤缓存，正因为它在 run 内稳定。
- **追加尾部零影响。** `SystemInjected` 被 marker 选择跳过，最新断点仍落在最后一条真实消息上；注入消息位于断点**之后**，天然在缓存区之外。且 append-only 保证旧注入原封不动留在前缀里，后续每轮照常命中。
- **必须一次性或 append-only，禁止原地覆写。** 想要“实时倒计时”而覆写同一位置，会从该位置起每轮失效——用一次性注入替代。
- **不持久化**可避免 replay 携带过期计数（§3.1）。

即：按“尾部 + 一次性 + 不持久化”实现，**对 prompt cache 零影响**。

## 7. 代码落点清单

| 文件 | 改动 |
|---|---|
| `internal/agent/agent.go` | `AgentLoopConfig` 增 `IterationBudgetPolicy`；`:1900` 预算检查改为一次性注入预算通知；循环用局部 `limit`（封顶 Hard），并在每轮工具执行后从预算句柄同步；`:1986` 终态消息报 Soft/Hard/实际消耗 |
| `internal/agent/`（新增） | `extend_budget` 工具定义、`iterationBudget` 句柄与 `Request()` clamp、`ContextWithIterationBudget` / `FromContext` |
| `internal/agent/agent_context.go` | 新增构造预算通知 `SystemInjected` 消息的 helper（与 `buildSessionContextMessage` 并列） |
| `internal/agentruntime/agent_build.go` | `AgentBuildOptions` 增策略字段并透传到循环配置 |
| `internal/agentruntime`（ResolvePolicy 附近） | 解析默认值（200/400/续期次数），按 `RuntimeSource` 差异化 |
| `internal/serve/channels/config.go` | `run_max_duration_secs` 默认从 14400s 提升到 57600s（16h）。**不新增** serve.json 字段：硬上限/续期次数/间隔/时长由 `IterationBudgetPolicy.Normalize` 统一给出，渠道 soft 仍用 `agent.maxTurns` |
| `internal/tui/agent_events.go` / `internal/acp/acp.go` | 仅投影：把“已续期”作为普通状态事件渲染，不新造事件语义 |

## 8. 边界与约束

- **仅主 run 续期。** 子代理 / Expert 成员的 `MaxIterations` 是能力上限，`subagent.go:318-322` 规定只能收窄，且其注册表不得包含 `extend_budget`。
- **硬上限不可突破**，并配 16h（57600s）wall-clock 上限；续期不能突破两者。
- **审计**：每次续期进 transcript / run 事件，工具路径可被审批，`reason` 必填；放行结果（granted/remaining/renewals_used）回给模型。
- **模式与沙箱**：`extend_budget` 的可用性由模式与 allow 策略决定，不得绕过 sandbox / 审批 / 高风险命令保护。
- **不做适配器层自动重发**：TUI 队列续跑（`scheduleNextQueuedPrompt`）是新 run，会丢 canonical run 身份、租约、审批/问题状态、附件与交付状态，且模型无感知——违反 AGENTS.md 的反碎片化规则。
- **不把上限直接交给适配器**：违反 one-resolver，且无上限约束。

## 9. 测试计划

- 循环契约：续期只 `continue`，每条退出路径仍恰好一个 `EventRunFinished`（扩 `terminal_contract_test.go`）。
- 感知注入：跨阈值时尾部出现一条 `SystemInjected` 消息且仅一次；不持久化；下一轮请求包含它。
- 工具申请：模型调用 `extend_budget` 后有效上限抬高、返回 granted/remaining；未调用则不续期。
- 上限：超过 `MaxRenewals` / 到达 Hard / 超过 16h wall-clock 后必终态，不再延长。
- 缓存：续期注入前后 `selectCacheMarkers` 结果对真实消息稳定（扩 `internal/agent/cache_test.go`）。
- 子代理：成员 run 不因续期策略获得更高上限。
- 工具路径：`extend_budget` 的 clamp（Hard / MaxRenewals / MinInterval）、`reason` 必填、模式/allow 门控。
- 成员隔离：子代理 / Expert 成员注册表中不出现 `extend_budget`，且无法通过任何路径抬高自身上限。

## 10. 风险与未决问题

- **成本**：续期使单 run 上限提高，必须与时长/用量护栏配套；否则等于取消保险。
- **模型滥用**：模型可能把续期当默认动作；`MaxRenewals` + `MinInterval` 与 `reason` 审计是主要缓解。
- **可见性 vs 缓存**：若产品坚持“每轮实时倒计时”，需评估缓存损失，或改为固定间隔的一次性注入。
- **已定**：续期由模型经 `extend_budget` 工具申请（§3.2 / §3.3），Runtime 只做 clamp；不再提供纯 Runtime 自动放行路径。
- **已定**：wall-clock 上限统一 16h（57600s），前台与渠道共用（§5）；渠道 `run_max_duration_secs` 提升到 57600s。

## 11. 实现状态（2026-09-17）

已落地：

- `internal/agent/iteration_budget.go`：`IterationBudgetPolicy` + `Normalize` + 每 run 的 `iterationBudget` 句柄（`Request` clamp：`reason` 必填、`MaxRenewals`、`MinInterval`、`Hard`）+ run context 传递。
- `internal/agent/iteration_budget_tool.go`：`extend_budget` 工具（无状态，从 context 取句柄）。
- `internal/agent/agent.go`：`AgentLoopConfig.IterationBudget`；循环改用动态 `limit`（读预算句柄）；预算通知一次性 `SystemInjected` 尾部注入（不持久化，cache marker 跳过）；续期后重置通知并投影一条 `EventStatus`；`max_iterations` 终态消息报 soft/hard/续期次数；wall-clock 到点以 `TaskIncomplete` + `wall_clock_limit` 收尾。
- `internal/agentruntime/agent_build.go`：`AgentBuildOptions.IterationBudget`；`buildAgent` 对 lead（非 transient、非 auxiliary）规范化策略并把 `extend_budget` 注册到共享 registry；transient / auxiliary / 子代理不注册。
- `internal/serve/channels/config.go`：`run_max_duration_secs` 默认 14400s → 57600s（16h）。

实现说明与决策：

- 硬上限/续期次数/最小间隔/wall-clock 默认值由 `IterationBudgetPolicy.Normalize` 给出（hard = 2× soft、RenewFactor 0.5、MaxRenewals 2、MinInterval = soft/10、MaxWallClock 16h）。**决策：不新增 `serve.json` 字段**，硬边界统一由 Runtime 策略代码默认值给出；渠道仍以 `agent.maxTurns` 作为 soft。
- 续期默认放大量 = `round(soft × RenewFactor)`；模型可用 `additional_turns` 覆盖，但始终被 `Hard` 截断。
- wall-clock 命中按“预算耗尽”处理（`TaskIncomplete` / `wall_clock_limit`），与 `max_iterations` 同语义。
- 续期不产生终态事件，`EventRunFinished` 契约不变。

测试：

- `internal/agent/iteration_budget_test.go`：策略规范化、`Request` clamp、context 往返、工具、循环注入一次、经工具续期越过 soft 上限、cache marker 不受尾部注入影响。
- `internal/agentruntime/iteration_budget_build_test.go`：lead 才注册 `extend_budget`，transient 不注册，子代理工具集不含它。
