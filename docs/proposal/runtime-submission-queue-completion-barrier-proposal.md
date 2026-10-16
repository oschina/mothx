# Runtime 提交队列与执行完成屏障方案

> 状态：提案（尚未实施）
>
> 日期：2026-10-16
>
> 关联方案：[统一 Agent 核心与多入口 Runtime 方案](./agent-core-runtime-unification-proposal.md)、[跨进程 Session 执行归属、停止与恢复方案](./cross-process-session-execution-ownership-proposal.md)

## 1. 背景

TUI 允许用户在一个 Run 执行期间继续输入，后续输入显示为排队消息，并在前一个 Run 结束后依次执行。当前队列由 TUI adapter 管理，而 Run 的 durable terminal transition、execution registration、retained lease 和跨进程 admission 由 `internal/agentruntime` 与 `internal/session` 管理。

这造成了两个不同的“完成”边界：

1. **Agent 事件完成**：Agent Core 发出 `EventRunFinished`，TUI 可以完成文本渲染；
2. **Runtime 执行完成**：终态和 conversation turn 已持久化，本地 execution registration 已注销，Runtime 持有的 lease 引用已释放，新的 submission 可以安全 admission。

`EventRunFinished` 不是 ownership release acknowledgement。Adapter 若在收到该事件后立即清空 `a.run`、切换为空闲并启动下一条输入，就可能在 Runtime 完成 lease handoff 之前申请新的 admission。当前错误文本 `session runtime lease is held by another process` 也不能表达这种同进程过渡状态：它同时被用于进程内 mutex 忙、本进程 retained lease 忙和真正的外部 owner。

对这一问题继续增加轮询、短延时、`localExecutionDraining` 瞬时检查或让所有 TUI 输入无界 `Wait: true`，只能掩盖部分时序，不能建立稳定的生命周期合同。

## 2. 目标与非目标

### 2.1 目标

1. 为一次执行定义唯一、Runtime-owned 的 **completion barrier**，明确区分“终态已选择”“终态已展示”和“执行权已完整释放”。
2. 让同一 Session 的本地 submissions 在 Runtime 内按 FIFO 串行 admission，不依赖 Adapter 对 lease 状态的轮询或猜测。
3. 保持 SQLite fenced lease 为跨进程唯一所有权裁决，不因本地排队削弱 external owner 保护。
4. 排队输入一旦被 Runtime 接受，就必须有稳定 submission ID、状态和确定的取消/失败结果，不能因瞬时 busy 被丢弃。
5. TUI、CLI、WebUI/API、ACP 和 Channel 使用同一个 submission/admission 生命周期；入口只选择提交策略并投影事件。
6. 保持 canonical Run 的语义：排队 submission 不是 Run，只有 admission 成功后才创建 Run/intent/start event。

### 2.2 非目标

1. 本方案不允许一个进程排队后自动抢占另一个进程的 Session lease。
2. 本方案不把 UDP ownership view 变成授权或唤醒队列的可靠消息总线。
3. 本方案不持久化普通交互队列；进程退出时尚未 admission 的内存 submission 明确终结为 interrupted。若未来需要 durable queue，应单独设计 schema、恢复和幂等合同。
4. 本方案不改变 Agent Core 的 provider/tool iteration，也不合并多个用户输入为一个 provider turn。
5. 本方案不让 Adapter 直接持有或释放 Runtime execution lease。

## 3. 核心不变量

### 3.1 一个 Session 一个 submission coordinator

`SessionRuntime` 为每个 Session 持有唯一的 `SubmissionCoordinator`。所有需要执行 Agent 的入口都通过它提交，不能在 TUI、ACP、Serve 或 Channel 中各自实现 admission 重试和前序 Run 等待。

同一进程、同一 Session 的 submission 状态机为：

```text
accepted -> queued -> admitting -> running -> completing -> completed
                                             \-> failed/cancelled/interrupted
```

- `accepted/queued` 尚未创建 canonical Run；
- `admitting` 取得 admission lease，并原子创建 intent/Run/start event；
- `running` 对应唯一 active execution；
- `completing` 表示 Agent 已选择终态，但 Runtime 仍在执行 fenced terminal persistence 或 release；
- `completed` 仅在 completion barrier 关闭后成立。

### 3.2 一个权威完成屏障

每次成功 admission 返回 Runtime-owned handle：

```go
type ExecutionHandle struct {
    SubmissionID string
    RunID        string
    Events       <-chan RuntimeEvent
    Completed    <-chan ExecutionCompletion
}

type ExecutionCompletion struct {
    RunID  string
    State  RunState
    Error  *ErrorInfo
}
```

`Completed` 只能在以下步骤全部完成后发布一次：

1. Agent terminal outcome 已由 Runtime 接收；
2. canonical terminal event、Run、conversation turn、assistant entry 和 delivery plan 已按既有原子边界持久化；
3. pending Decision 已按终态收敛；
4. local execution registration 已注销；
5. Runtime retained lease reference 已释放；
6. adapter admission guard 已转交 Runtime 并完成释放；
7. 进程内 Session execution slot 已释放。

如果 terminal persistence 因瞬时数据库错误在后台重试，handle 保持 `completing`，completion barrier 不得提前关闭，后继 submission 也不得自行申请 lease。

### 3.3 Agent terminal event 只用于投影

`EventRunFinished` 继续是 Agent Core 的 canonical terminal outcome，但 Adapter 收到它时只能：

- 完成文本、工具和 usage 的 UI 渲染；
- 显示“正在完成”状态；
- 等待 Runtime completion。

Adapter 不得据此：

- 宣告 Session 可提交；
- 释放 execution lease；
- 清除 Runtime active execution；
- 直接启动下一条 queued submission；
- 合成第二个成功或失败终态。

### 3.4 本地排队与外部占用必须区分

Runtime coordinator 知道本进程是否已有 active/completing execution，因此不需要通过错误字符串或 SQLite lease owner 猜测“是否是自己的前序 Run”。

提交策略建议为：

```go
type SubmitPolicy struct {
    QueueBehindLocal bool
    WaitForExternal  bool
}
```

默认交互策略：

| 当前状态 | `QueueBehindLocal` | 行为 |
|---|---:|---|
| 本进程 `running/completing` | `true` | 接受并进入本地 FIFO |
| 本进程 `running/completing` | `false` | 返回结构化 `local_busy` |
| 外部有效 execution lease | 任意 | 返回结构化 `external_busy`；除非调用方显式设置 `WaitForExternal` |
| `orphaned/recovery_failed/inconsistent/unknown` | 任意 | 不排队，返回对应结构化状态 |
| idle | 任意 | 立即 admission |

TUI 使用 `QueueBehindLocal=true`、`WaitForExternal=false`。因此本地连续输入自然排队，真正的外部 owner 仍立即显示明确冲突，而不是无界等待。

## 4. 组件设计

### 4.1 `SubmissionCoordinator`

新增或扩展 `internal/agentruntime` 中的共享组件，职责包括：

- 接受 normalized Runtime input；
- 分配 submission ID；
- 维护每 Session FIFO 和唯一 active handle；
- 在前序 completion barrier 后执行下一次 admission；
- 调用现有 `ExecutionRuntime` durable Begin/Finish 路径；
- 投影 `submission_queued/admitting/started/completing/completed` 事件；
- 在 session shutdown 时取消 active execution，并终结尚未 admission 的 submissions；
- 对 queued submission 提供按 submission ID 取消能力。

它不得重新实现 Agent loop、RunStore、DecisionService、input materialization 或 attachment lifecycle。

### 4.2 lease 所有权转交

当前 Adapter 取得 `RuntimeLeaseGuard` 后再通过 `releaseRuntime` 释放，容易形成双 owner 和释放时序分裂。目标合同为：

1. coordinator 取得 admission guard；
2. durable Begin 将 admission lease 原子绑定到 Run；
3. `ExecutionRuntime` 成功 retain 后，coordinator 将 guard 所有权转交给 execution；
4. execution 的单一 terminal/release 路径释放 retained reference 和 guard；
5. Adapter 永远不直接调用 guard release。

转交必须是显式、一次性的 owner move，失败路径也由 Runtime 统一回滚，不能同时保留 `tuiRun.releaseRuntime` 和 Runtime retention 两个独立释放责任。

### 4.3 事件模型

新增的 submission 事件属于 Runtime canonical event，而非 TUI 私有 Bubble Tea message：

| 事件 | 含义 |
|---|---|
| `submission_accepted` | Runtime 接受输入并分配 ID |
| `submission_queued` | 正在等待本进程前序 execution completion |
| `submission_admitting` | 正在取得 lease 并创建 canonical Run |
| `submission_started` | Run/start event 已持久化 |
| `execution_completing` | Agent 已选择终态，Runtime 正在持久化或释放 |
| `execution_completed` | completion barrier 已完成，可调度后继 submission |
| `submission_rejected` | 外部 owner、unknown/inconsistent 或策略拒绝 |
| `submission_cancelled` | queued submission 在 admission 前被取消 |

这些事件必须携带 submission ID；从 `submission_started` 起同时携带 canonical Run ID。Adapter 可以改变线格式和显示文本，但不能改变顺序与终态语义。

### 4.4 错误类型

不得继续依赖 `ErrRuntimeLeaseBusy` 的文本判断。Runtime admission 返回结构化分类：

```go
type AdmissionConflictKind string

const (
    ConflictLocalTransition AdmissionConflictKind = "local_transition"
    ConflictExternalOwner  AdmissionConflictKind = "external_owner"
    ConflictRecovering     AdmissionConflictKind = "recovering"
    ConflictUnknown        AdmissionConflictKind = "unknown"
)
```

DAO/session 层可以继续使用低层 fenced CAS 错误；`internal/agentruntime` 结合 canonical execution snapshot 和 coordinator state，将其解释成稳定的业务错误。显示文本不得声称“another process”，除非 Runtime 已确认 `external_owner`。

## 5. TUI 投影

迁移后 TUI：

1. 输入经现有 Runtime input contract normalize；
2. 调用 `SessionRuntime.Submit`，不创建 `tuiRun` admission owner；
3. 根据 `submission_queued` 更新队列预览；
4. 根据 `submission_started` 渲染 canonical `You:` transcript；
5. 根据 Agent/Runtime events 渲染流内容；
6. `EventRunFinished` 到达后显示 terminal outcome，但保持 submission 为 `completing`；
7. 只有 `execution_completed` 才从 live queue 中移除该 submission 并显示 Session idle；
8. 下一条 submission 由 coordinator 调度，TUI 不再发送 `queuedPromptStartMsg`。

`queuedPrompts`、`scheduleNextQueuedPrompt`、`startNextQueuedPrompt` 和 `tuiRun.releaseRuntime` 都是迁移桥。删除条件是 Runtime coordinator 覆盖普通完成、失败、取消、stream 无 terminal event、terminal persistence 重试、Session 切换和 shutdown 路径，并通过跨入口合同测试。

## 6. 生命周期时序

### 6.1 正常连续输入

```text
TUI              SubmissionCoordinator      ExecutionRuntime       SQLite lease/Run
 | submit A               |                         |                       |
 |----------------------->| admit A                 |                       |
 |                        |------------------------>| bind/create A         |
 | submit B               |                         |---------------------->|
 |----------------------->| queue B                 |                       |
 |  queued(B)             |                         |                       |
 |<-----------------------|                         |                       |
 |  EventRunFinished(A)   |                         |                       |
 |<-------------------------------------------------|                       |
 |                        | completing A            | terminal transaction  |
 |                        |                         |---------------------->|
 |                        |                         | unregister/release     |
 |                        |<------------------------|                       |
 | completed(A)           |                         |                       |
 |<-----------------------| admit B                 |                       |
 |                        |------------------------>| bind/create B         |
```

不存在“结束事件后立即重新探测 lease”的步骤，因此事件与新输入距离再近也不会产生 ownership 误判。

### 6.2 terminal persistence 重试

前序 execution 保持 `completing` 和 lease authority，后继 submissions 保持 `queued`。后台 retry 成功后关闭 completion barrier，再调度下一条。retry 失败但 ownership 真正丢失时，Runtime 以结构化失败完成 barrier；后继 submission 必须重新 inspect，不能假定 Session idle。

### 6.3 外部 owner

本地 coordinator 没有 active/completing handle，而 SQLite snapshot 确认 external owner 时，TUI submission 立即得到 `external_busy`。它不能进入“等待本地前序 Run”的 FIFO，也不能显示成普通 queued prompt。

## 7. 迁移计划

### Phase 0：复现与观测

1. 为 submission、Run、lease binding 增加关联日志：submission ID、Run ID、owner instance、epoch、purpose、phase。
2. 加入确定性测试 hook，分别阻塞 terminal persistence、local unregister、retained lease release 和 guard unlock。
3. 建立真实 TUI 连续输入测试，证明现有路径可在任一 handoff 点触发错误，而不是只测试手工构造的 `localExecutionRegistry`。

### Phase 1：completion barrier

1. 为 `ExecutionRuntime` 增加只读 completion handle；完成信号在完整 release 后关闭。
2. 将 guard/retained lease ownership 收敛到 Runtime 单一释放路径。
3. 现有 Adapter 暂不迁移队列，但不得再把 `EventRunFinished` 当作可 admission 证明。

### Phase 2：Runtime submission coordinator

1. 在 `SessionRuntime` 接入 FIFO coordinator 和结构化 SubmitPolicy。
2. 复用现有 input、ExecutionRuntime、DecisionService 与 durable Run API。
3. 增加 queued submission cancel、shutdown 和 failure convergence。

### Phase 3：入口迁移

1. 先迁移 TUI，删除 TUI admission polling和主动启动下一条逻辑。
2. 迁移 ACP/Channel 中已有的本地串行等待路径。
3. CLI/API 保留 fail-fast 或显式 wait policy，但必须调用同一 coordinator/admission contract。

### Phase 4：删除兼容桥

删除以下临时或重复逻辑：

- `tuiRun.releaseRuntime`；
- TUI `queuedPrompts` 的执行调度职责（可保留纯展示 projection）；
- `queuedPromptStartMsg`；
- admission 对 `localExecutionDraining` 的轮询式正确性依赖；
- TUI 全局 `ExecutionAdmissionOptions{Wait: true}` 临时规避；
- 对 `session runtime lease is held by another process` 文本的业务判断。

## 8. 测试与验收

### 8.1 确定性并发测试

必须用 channel/barrier 控制时序，覆盖：

1. 新输入发生在 `EventRunFinished` 之前、同时和之后；
2. terminal transaction 成功但 local registration 尚未注销；
3. registration 已注销但 retained lease 尚未释放；
4. retained lease 已释放但 admission mutex 尚未解锁；
5. terminal persistence 首次失败、后台重试成功；
6. execution lease 在 completing 期间被 fenced takeover；
7. queued submission 在 admission 前取消；
8. SessionRuntime shutdown 时 active 与 queued submission 同时存在。

测试不得依赖 `time.Sleep` 证明正确性；时间只可作为死锁上限。

### 8.2 跨入口合同

TUI、CLI、WebUI/API、ACP 和 Channel 必须证明：

- 相同 SubmitPolicy 得到相同 submission/Run 状态序列；
- canonical Run ID、terminal state、session entries 与事件顺序一致；
- 只有投影格式不同；
- external owner 不会被本地队列抢占；
- queued input 不会因瞬时 local transition busy 丢失。

### 8.3 验收标准

1. 连续快速提交至少 1,000 轮，在 race detector 下无 busy 误报、输入丢失、重复 Run 或 goroutine 泄漏。
2. 在每个 release 子阶段注入阻塞，后继 submission 均保持 queued，且只在 completion 后启动。
3. 真正双进程竞争仍只有一个 owner；另一个入口得到结构化 `external_busy`。
4. `go test -race ./internal/agentruntime ./internal/tui ./internal/acp ./internal/serve/...` 和 `go test ./internal/architecture` 通过。

## 9. 实施约束

本方案必须遵循一个 Agent Runtime 的架构边界：

- submission coordinator 属于 `internal/agentruntime`，不能新建 TUI queue runtime；
- canonical persistence 继续通过 `internal/db -> internal/dao -> internal/session/agentruntime`；
- 不新增 Adapter 私有 durable queue 表；
- 不弱化 fenced lease CAS、terminal persistence retry 或 ownership snapshot guard；
- 不用固定 sleep、延迟启动或错误文本匹配作为正确性机制；
- 变更 production lifecycle 后必须同步 architecture guard 和跨入口 contract tests。

## 10. 决策摘要

本问题不应定义为“给 TUI 排队加一点延迟”，而应定义为：**Adapter 使用 Agent terminal event 代替 Runtime ownership completion，缺少共享 submission serialization contract。**

最终设计选择是：

1. `EventRunFinished` 负责结果语义；
2. Runtime completion barrier 负责生命周期完成；
3. `SubmissionCoordinator` 负责同 Session 本地 FIFO；
4. SQLite fenced lease 继续负责跨进程 ownership；
5. Adapter 只投影 queue/run/completion，不再推断何时可以启动下一条。
