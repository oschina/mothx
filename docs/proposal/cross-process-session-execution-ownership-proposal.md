# 跨进程 Session 执行归属、停止与恢复方案

> 状态：实施中（核心 admission、恢复、停止和副作用围栏已落地；Serve/ACP 的 durable 查询已收口到 Runtime，全入口合同、故障注入与兼容桥清理仍在进行）
>
> 日期：2026-08-27
>
> 关联方案：[Session 分叉与消息进度分叉方案](./session-fork-and-message-branch-proposal.md)、[统一 Agent 核心与多入口 Runtime 方案](./agent-core-runtime-unification-proposal.md)、[Runtime 提交队列与执行完成屏障方案](./runtime-submission-queue-completion-barrier-proposal.md)、[Runtime 工作区输入文件物化方案](./runtime-workspace-input-materialization-proposal.md)、[WebUI 会话终止与审批生命周期方案](./webui-session-stop-and-approval-lifecycle.md)

> 交接基线：当前分支 `lockandmedia` 的 `06dcf69` 已落地本方案的核心 Runtime/session/adapter 实现。下一次继续工作时，以本文件第 4、10、11.4 节为准，不要把已完成项重新设计成新方案。
>
> 当前工作树包含本方案对应的 Runtime/session/Serve/ACP/Channel/Agent Core 改动；交接时应以本节状态和第 11.4 节测试证据为准，不要把兼容桥误认为新的生命周期实现。

> 提交队列关联：同一进程的连续 submission 由 `internal/agentruntime.SubmissionCoordinator` 串行化；`SessionLeaseBus` 只作为 advisory wake，通知接收后必须重新读取 SQLite 并执行 fenced admission，不能用 UDP 报文直接授权、取消或终结 Run。Runtime completion barrier 关闭前，ownership snapshot 仍保持 busy/reserved，适配器不得把 `EventRunFinished` 当作可提交证明。

## 1. 背景与问题

当前可能出现如下用户可见的不一致：Session 列表或聊天页显示“正在运行”，但点击停止后接口返回 `session has no active run`。这并不必然说明列表错误；也可能是**另一个进程**持有该 Session 的有效执行权并仍在运行。

多进程可同时访问同一 Session 是已支持的场景，例如 Serve/WebUI、CLI、TUI、ACP 与 channel 入口。为避免两个进程同时写入同一 Session，系统已经有三层机制：

| 机制 | 已解决的问题 | 不能单独解决的问题 |
|---|---|---|
| 进程内 mutex | 同一 Go 进程中的快速串行化 | 其他进程的所有权、崩溃恢复 |
| SQLite `session_runtime_leases` | 跨进程 lease、TTL、epoch fencing 与持久化裁决 | 向已连接客户端即时推送变化 |
| UDP `SessionLeaseBus` | 提醒同机其他进程尽快重新读取状态 | 可靠所有权、取消命令与恢复裁决 |

`session_runs` 的非终态记录表示“存在尚未完成的 durable Run”；有效的 runtime lease 表示“哪个进程当前被允许继续执行和写入”。两者是不同维度，不能互相替代。

目前 Serve/WebUI 的 runtime snapshot 主要从 active Run 得出 `running`，而停止入口主要查看当前 Serve 进程的 `APISession` 内存对象及其 cancel function。因此：

1. 远端进程持有有效 lease 时，UI 正确显示运行中，但当前进程没有内存 cancel function；
2. orphan Run（进程崩溃、lease 过期、终态落库失败等）会留下非终态 durable Run，但没有本地执行实体，也无法证明存在可恢复的 provider 远端执行；
3. snapshot 没有把“运行事实”“执行归属”“本地可控制性”明确投影给适配器；
4. UDP 收到通知后虽然会触发刷新，却没有把 lease owner 的判定带入统一状态模型。

本方案不把上述情况简单改为“非本进程执行就不显示 running”。那会重新打开跨进程并发提交窗口，违背 lease 的设计目的。正确的产品语义是：**远端有效执行仍显示为运行中，但它不是本进程可停止的运行。**

进程意外退出不能让 Session 永久显示为正在执行。正常本地 Agent Run 的 owner 心跳停止后，只允许在 lease TTL 内继续投影为 `external`；lease 失效后必须立即投影为 `orphaned`，并由 Runtime-owned 恢复协调器在有界时间内终结为 `failed/owner_lost`。若数据库或恢复事务持续失败，状态必须显式变为 `recovery_failed`，而不是继续伪装为 `running`。

## 2. 目标与非目标

### 2.1 目标

1. 将 durable Run、SQLite lease 和本进程执行实体统一解析为一个 Runtime-owned 的 Session 执行快照。
2. 区分本地执行、外部执行、远端 provider 执行、非执行占用、孤儿 Run、恢复失败、空闲和暂不可判定状态；所有入口使用同一判定。
3. 保持其他进程持有有效 lease 的 Session 为 running，禁止任何入口在这种情况下提交新的 Run。
4. 将停止语义拆分为本地取消、外部归属冲突、孤儿终结/恢复和状态暂不可用，消除误导性的 `no active run`。
5. 将 lease 的 `run_id` 与 durable Run 建立受 fencing 保护的关联，方便诊断、恢复和状态投影。
6. 保证孤儿恢复先取得 lease 再写入；绝不因一个进程未看到本地内存 runtime 就终结另一个有效进程的 Run。
7. 让终态持久化失败保留为可观察、可重试的生命周期状态，而不是提前从内存和 UI 中“清空”。
8. 为 admission、execution、recovery、mutation、fork 建立明确的 lease purpose，避免把维护操作误报为 Agent 执行。
9. 保持一个 Agent Runtime、一个 Run 生命周期和薄适配器的架构边界。
10. 为 owner 进程崩溃建立不依赖 UDP、不依赖其他进程重启的有界恢复保证，并让恢复失败成为可见、可重试的独立状态。

### 2.2 非目标

1. 本期不实现“任意进程都能强制停止另一个进程”的跨进程取消协议。若将来需要，该能力必须使用 durable control command/ack，不得复用 UDP 广播充当命令通道。
2. 本期不通过 UDP 复制 Agent 内存、tool 状态、审批等待器或 provider stream。
3. 本期不承诺进程崩溃后能从任意 Agent 中间指令继续执行；不能安全 reattach 的 orphan Run 仍按既有恢复策略终结。
4. 本期不改变 mode、审批、sandbox、渠道绑定或 provider 的业务语义。
5. 本期不新增 adapter 私有 Run manager、锁、恢复循环或 SQLite schema 初始化路径。

## 3. 设计原则与不变量

### 3.1 权威性顺序

```text
SQLite durable Run + SQLite runtime lease  ── 事实与裁决
              ↑
       Runtime execution snapshot           ── 唯一业务解释
              ↑
Serve / WebUI / CLI / TUI / ACP / Channel   ── 只做投影与协议映射

UDP、进程内 mutex、内存 APISession           ── 加速、执行或通知，不单独裁决
```

- `session_runs` 是 durable 生命周期事实；非终态 Run 不可被当作可随意覆盖的陈旧 UI 位。
- `session_runtime_leases` 是跨进程执行权的事实；有效性必须由 SQLite 的时间与 fencing 字段裁决，不能由 UDP 是否到达裁决。
- 本地内存 `ExecutionRuntime`、`APISession` 和 cancel function 只证明“当前进程是否能够实际执行本地取消”。它们不能否定外部有效 lease。
- UDP 只用于唤醒订阅者、缩短下一次 SQLite 查询的延迟；丢包、重复、乱序和监听失败都不得改变正确性。
- active Run、lease 和 SQLite 当前时间必须来自同一个只读事务快照；跨多个独立查询拼接出的状态不能用于恢复或控制决策。

### 3.2 崩溃后的有界收敛

只要 Session DB 可读写且至少有一个长期 Runtime 进程存活，本地 Agent Run 在 owner 意外退出后必须满足：

1. 在 `lease.expires_at` 之前保持 `external`，禁止其他进程提前终结或接管；
2. SQLite 当前时间达到 `expires_at` 后，下一次 snapshot 立即返回 `orphaned`，不得继续显示“正在另一进程执行”；
3. Runtime recovery coordinator 最迟在一个 `recoveryScanInterval` 内尝试取得 recovery lease；
4. 成功取得 recovery lease 后，在 `recoveryAttemptTimeout` 内完成 fenced terminalization，或转为显式 `recovery_failed`；
5. 当前基线 `leaseTTL=15s`、`recoveryScanInterval<=5s`、`recoveryAttemptTimeout<=10s`，因此健康 SQLite 下普通本地 orphan 从最后一次成功心跳到终态的目标上限为 30 秒；这些值由 Runtime 统一配置和投影，adapter/UI 不得另设计时器；
6. 没有任何 Runtime 进程存活时当然无法主动写库；下一个进程启动时必须先执行恢复扫描，受影响 Session 在恢复或明确进入 `recovery_failed` 前不得接受新 Run。

UDP、页面刷新和用户操作都只能缩短上述时间，不能成为最终收敛的必要条件。数据库永久不可用或数据关联损坏时不能为了“解锁”而伪造终态；此时 Session 保持不可提交，但必须显示 `unknown`、`inconsistent` 或 `recovery_failed` 及诊断，而不是显示执行中。

### 3.3 运行与控制必须分离

`running` 回答的是“Runtime 是否能证明这个 Session 仍有实际执行实体”：即有效 execution lease，或可验证的 `detached_remote` provider Run。仅有非终态 durable Run 只能证明生命周期尚未收敛；lease 已过期的 `orphaned` 和恢复失败的 `recovery_failed` 都是 `busy`，但不是 `running`。

`busy`/`CanSubmit` 回答是否允许新提交，`canCancelLocal` 回答当前 Runtime 能否对该 Run 执行 `ExecutionRuntime.CancelDurable`。三者不得复用同一个布尔字段或通过本地 map 推断；UI 也不得因为 `activeRun.status` 仍为 `running` 就覆盖 `execution.state=orphaned/recovery_failed` 的真实文案。

### 3.4 统一 Runtime 边界

新能力必须扩展 `internal/agentruntime` 与 `internal/session` 的既有 Run/lease 边界：

- `internal/session` 负责 lease 和 Run 的原子读取、fencing 校验与事务性关联；
- `internal/agentruntime` 负责判定、取消、重试终结与 orphan recovery 的编排；
- Serve/OpenAI API、WebUI、CLI、TUI、ACP、channel 只消费 snapshot 或调用 Runtime 控制操作；
- `RunManager` 可以继续做事件 fan-out，但不得成为状态、归属或取消结果的第二权威来源。

不得在 `internal/serve/openaiapi` 仅靠 `APISession.IsRunning()` 再实现一套跨进程判定，也不得让 UI 根据 UDP 报文自行决定 Session 是否可提交或可停止。

### 3.5 本机可观测性与授权边界

MothX 的 HTTP、SSE、WebSocket 和进程间 UDP 都属于本机服务的同一信任域。本方案不对这些本机协议设置 `owner_instance_id`、origin instance ID、PID、epoch、expiry、lease token 或 token hash 的隐私隐藏要求；是否投影某个字段由诊断价值、载荷大小和 API 兼容性决定，而不是由“网络协议类型”决定。UDP 继续只通过 `127.0.0.0/8` 回环网络广播，接收端必须拒绝非 loopback 来源。

可观测不等于授权。任何提交、停止、恢复、lease 续期、接管或状态写入都不得接受客户端或 UDP 报文携带的 owner/token/epoch 作为执行凭据；Runtime 必须从自己的受控 binding 取得预期身份，并重新读取 SQLite、执行 CAS/fencing 后才能操作。即使诊断接口完整展示 lease identity，调用方也不能通过回传这些字段获得、延长或释放 lease。若未来 Serve 支持非本机或多租户部署，再由独立认证/授权方案重新定义对外字段，而不是污染当前本机合同。

## 4. 当前实现状态与剩余缺口

本节取代旧的“当前实现缺口”清单。状态以当前代码和测试为准：`已完成` 表示核心路径已经进入基线，`部分完成` 表示主路径已落地但仍有明确验收或迁移工作，`待完成` 表示不能在交接时假设已经具备。

| 能力 | 当前状态 | 实现位置与说明 |
|---|---|---|
| 同一事务读取 Session、active Run、lease、recovery、Responses remote facts | 已完成 | `internal/session/execution_facts.go` 的 `ReadSessionExecutionFacts`；使用单次 SQLite 时钟和只读事务。 |
| 统一执行快照与状态判定 | 已完成 | `internal/agentruntime/execution_snapshot.go` 的 `InspectSessionExecution`；覆盖 `idle/reserved/local/external/detached_remote/orphaned/recovery_failed/inconsistent/unknown`。 |
| 显式 lease purpose | 已完成（保留兼容桥） | `admission/execution/recovery/mutation/fork` 已有明确 purpose；旧 `TryLockRuntime` 仅作为历史测试/兼容读取，生产新路径受架构守卫限制。 |
| lease 与 Run 的 run_id、epoch、token identity fencing | 已完成 | `internal/session/runtime_lock.go`、`internal/agentruntime/run_store.go`；包含 lease lost、tombstone 和本地 binding 引用保留。 |
| 原子 admission 与首次 Session 竞争 | 已完成核心路径（保留兼容桥） | `BeginIntentDurable` 和 `BeginDurable` 在生产 `RunStore` 上均把 Run/started event/lease binding 放入同一事务；真实双进程首次 Session 竞争已验证一个 winner、一个 duplicate/busy，且只留下一个 canonical Run；不实现扩展 store 接口的嵌入测试仍走明确的非原子兼容桥。 |
| 本地/外部/远端停止矩阵 | 已完成 | `internal/agentruntime/execution_stop.go`；Serve/OpenAI API 与 channel 已投影结构化结果，外部 owner 不被本地停止误杀；Run API 取消带 `ExpectedRunID`，local/remote/orphan 路径均复核目标，避免旧请求误停新 Run。 |
| terminal persistence 失败保持 active 并异步重试 | 已完成核心路径 | `FinishDurableWithRetry` 保留 lease/registration，并在前台 context 超时后由 Runtime 重试；已有回归测试。 |
| lease-first orphan recovery 与 durable recovery record | 已完成核心路径 | `internal/agentruntime/run_recovery.go`、`internal/session/run_recovery.go`；恢复前重新读取并校验 recovery lease，原子收敛 Run/event/Turn/Decision。 |
| 常驻 recovery coordinator | 部分完成 | `internal/agentruntime/recovery_coordinator.go` 已接入启动扫描、周期扫描和 wake；orphan 扫描与不同 Run 的恢复现使用 context deadline 和最多 8 个 Runtime worker 并行，recovery record 与终态收敛写入也受限；超时后用独立短 deadline 持久化 `recovery_failed`，并已用 SQLite 终态事件触发器验证写失败收敛、用慢/快双 Session 验证慢回调不会阻塞快 Run。provider/adapter 回调本身仍不承诺可强制中断，暂不宣称全流程固定 30 秒上限。 |
| `responses_background` detached remote | 已完成主路径 | 以 canonical `response_runs` 作为远端证据，snapshot 可返回 `detached_remote`，Serve 提供受 Runtime 控制的 reattach/cancel hook。 |
| Serve/WebUI/CLI/TUI/ACP/Channel 统一投影 | 部分完成 | 提交、chat-completions、Responses cancel/reconnect 已按 fresh Runtime snapshot 投影 external/reserved/orphaned/recovery_failed/unknown；Serve/ACP 的 durable Run 查询不再经 RunManager，RunManager 仅保留事件 fan-out 与明确兼容桥；仍需全入口合同测试和少量历史兼容分支清理。 |
| 副作用工具在 lease TTL 跨越后的二次 fencing、稳定 operation ID | 已完成 | Agent Core 在审批/阻塞结束、durable tool claim 之后统一调用 Runtime hook；hook 以当前 Run、execution lease epoch/token identity 和 SQLite 事务复核 binding，失败时不调用工具。`ToolExecutionRecord.ExecutionKey` 作为稳定 operation ID 注入 tool context，支持外部幂等目标复用；已补真实跨进程 lease takeover 后旧 owner 的副作用阻断、HTTP MCP operation ID header 验收，以及不支持幂等时明确提示无法保证 exactly-once 且不自动重放。 |
| 全真实进程崩溃、恢复者再次崩溃、恢复写失败验收 | 部分完成 | 已补真实子进程 `kill -9`：owner 崩溃后常驻 coordinator 在 lease 过期且无 UDP 通知时收敛，recovery owner 崩溃后可由下一协调器接管；SQLite 终态写失败注入、首次 Session 双进程 admission 已通过；仍需更多负载和 race 证据。 |
| 旧 `APISession.IsRunning`/无 purpose API 清理 | 待完成 | 仅可在迁移窗口结束且全入口合同测试通过后删除；当前不应提前删除兼容读取。 |

现有 `session_runtime_leases` 列、epoch fencing 和 recovery record 是唯一基础设施；不要为剩余缺口另建“running session”表、adapter-owned recovery loop 或 UDP 命令通道。

## 5. 统一执行快照

### 5.1 Runtime contract

`internal/agentruntime` 已提供面向所有适配器的只读服务 `InspectSessionExecution`。其责任边界固定：必须在一次受控读取中解释 active Run、lease 与本地执行注册表，不能把这三项判断分散到各 adapter。

建议的概念模型如下：

```go
type SessionExecutionState string

const (
    SessionExecutionIdle           SessionExecutionState = "idle"
    SessionExecutionReserved       SessionExecutionState = "reserved"
    SessionExecutionLocal          SessionExecutionState = "local"
    SessionExecutionExternal       SessionExecutionState = "external"
    SessionExecutionDetached       SessionExecutionState = "detached_remote"
    SessionExecutionOrphaned       SessionExecutionState = "orphaned"
    SessionExecutionRecoveryFailed SessionExecutionState = "recovery_failed"
    SessionExecutionInconsistent   SessionExecutionState = "inconsistent"
    SessionExecutionUnknown        SessionExecutionState = "unknown"
)

type SessionExecutionSnapshot struct {
    SessionID          string
    ActiveRun          *RunSummary
    State              SessionExecutionState
    Phase              string // "reserved", "admitting", "executing", "terminal_persistence", "releasing", "recovering"
    LeaseExpiresAt     *time.Time
    LeasePurpose       string
    LeaseEpoch         int64 // 仅服务端控制操作使用；是否对客户端投影由 API 层决定
    LinkageState       string // "bound", "legacy_unbound", "mismatched", "none"
    CanSubmit          bool
    CanCancelLocal     bool
    CanCancelRemote    bool
    RecoveryAction     string // "none", "recover_orphan", "reattach_remote", "retry_terminal_persistence", "retry_recovery"
    RecoveryAttempt    int
    RecoveryLastError  string
    RecoveryNextAt     *time.Time
    DisplayOwnerScope  string // "local", "external", "remote", "none", "unknown"
}
```

`RunSummary` 必须来自 canonical Run store，至少含 run ID、durable status、source、mode、开始和更新时间。`DisplayOwnerScope` 是稳定的产品投影，不能替代内部 owner/token/epoch；本机诊断响应可同时展示 source、owner instance ID、PID、lease purpose、epoch、expiry 和 token identity。HTTP 是否默认返回这些字段由 API 契约和兼容性决定，不受隐私禁令限制；客户端也不能通过回传它们自行取得 lease。

`RecoveryAttempt`、`RecoveryLastError` 和 `RecoveryNextAt` 必须来自 Runtime-owned 的 durable recovery 记录或 canonical Run recovery metadata，不能只存在于某个 Serve 进程的内存 cache。若需要 schema 变更，只能追加到 `internal/session/migrations.go` 并通过共享 store 访问。

`CanSubmit` 在所有有 active Run、`reserved`、`detached_remote`、`orphaned`、`recovery_failed`、`inconsistent` 和 `unknown` 状态均为 `false`。这保证“看不到本地 runtime”不会被误认为可以创建新的 Run。`RecoveryLastError` 对本机诊断可完整返回，但面向普通对话 UI 时应使用稳定错误码和简短消息。

### 5.2 判定规则

数据层应提供类似 `ReadSessionExecutionFacts` 的操作，在**同一个 SQLite 只读事务**中使用一次 SQLite 当前时间并读取同一个 Session 的：

1. 该 Session 当前非终态 durable Run；
2. runtime lease 的 purpose、state、expiry、owner、epoch 与 `run_id`；
3. 对 `responses_background` 等可脱离本地进程继续执行的 Run，读取 Runtime-owned 的远端执行记录与可恢复能力。

数据库事务完成后，再将这份不可变事实快照与当前进程注册的 `ExecutionRuntime` binding 比较。本地内存不能加入 SQLite 事务，因此比较失败时必须保守返回非 local 状态；控制操作还要在真正写入前再次校验预期的 run ID、epoch 和 lease identity。

判定按表中顺序应用；`recovery` 等显式 purpose 优先于一般的 execution owner 判断。

| durable Run | SQLite lease | 本地注册执行体 | 快照状态 | UI 语义 | 可提交 / 可取消本地执行 |
|---|---|---|---|---|---|
| 无 | 无有效 run lease | 无 | `idle` | 空闲 | 可 / 不可 |
| 无 | 有效 admission/mutation/fork/recovery lease，或 execution lease 正处于 release 窗口 | 任意 | `reserved` | Session 正被其他操作占用 | 不可 / 不可 |
| 非终态 | 有效 recovery lease，且绑定待恢复 run ID | recovery coordinator | `reserved` / phase=`recovering` | 正在恢复或重新挂接 | 不可 / 不可 |
| 非终态 | 有效 admission/mutation/fork lease | 任意 | `inconsistent` | 非执行操作与 active Run 冲突 | 不可 / 不可 |
| 非终态且 run ID 已绑定 | 有效、owner 为当前进程 | 同一 run、epoch 和 lease identity 已注册 | `local` | 本进程正在执行 | 不可 / 可 |
| 非终态且 run ID 已绑定 | 有效、owner 为其他进程 | 任意 | `external` | 其他进程正在执行 | 不可 / 不可 |
| 非终态、且 Runtime 能证明 provider 远端执行仍可查询/恢复 | 无有效本地 execution lease | 无 | `detached_remote` | 远端 provider 仍在执行或等待重新挂接 | 不可 / 不可 |
| 非终态的本地 Agent Run | 无有效 execution/recovery lease，最近一次恢复已超时或失败 | 无 | `recovery_failed` | 遗留执行恢复失败，可重试/诊断 | 不可 / 不可 |
| 非终态的本地 Agent Run | lease 缺失、失效或 released/lost | 无或已丢失 | `orphaned` | 遗留执行等待恢复裁决 | 不可 / 不可 |
| 非终态 | 存在有效 lease，但 lease 绑定其他 run ID | 任意 | `inconsistent` | Run/lease 关联冲突 | 不可 / 不可 |
| 非终态且有效 lease 归属当前进程 | 同一 run ID | admission coordinator 证明正在注册 | `reserved` / phase=`admitting` | 正在准备本地执行 | 不可 / 不可 |
| 非终态且有效 lease 归属当前进程 | 同一 run ID | 无匹配 registration，且没有合法过渡记录 | `inconsistent` | 本地执行关联丢失 | 不可 / 不可 |
| 读取/解码/SQLite 暂时失败 | 无法裁决 | 任意 | `unknown` | 状态暂不可用 | 不可 / 不可 |

`cancelling`、`terminalizing`、`waiting_for_approval` 与 `waiting_for_question` 仍是 Run 的 durable status；它们不应另起一套 ownership state。有效外部 lease 加 `cancelling` 仍是 `external`，只是 UI 文案应显示“其他进程正在停止”。

`reserved` 是正常的安全占用，而不是数据损坏：它覆盖短时 Session mutation，也覆盖 acquire lease 到 Run admission、durable terminal commit 到 lease release 的合法过渡窗口。`inconsistent` 只用于无法由合法状态迁移解释的关联冲突。

### 5.3 远端 provider execution

`responses_background` 等 provider-native background Run 可以在 MothX owner 进程退出后继续存在。其状态不能只凭 `Run.Source` 推断；Runtime 必须通过 canonical `response_runs`、provider capability、远端 ID/polling 信息与 recovery policy 解析为以下结果之一：

- `attached`：当前进程持有 execution lease 并运行 monitor，按 `local` 处理；
- `detached_remote`：无本地 owner，但远端 Run 可查询、可取消或可重新挂接；
- `remote_terminal_pending`：远端已终态，本地 transcript/Run 终态尚待 fenced 落库，按 `reserved`/`recovering` 处理；
- `unrecoverable`：无法证明远端仍可恢复，取得 recovery lease 后按 orphan policy 终结。

`RecoveryKeepRemote` 不得只返回“保留 durable Run”然后结束；它必须生成 `detached_remote` snapshot，或在返回前成功建立新的 monitor/lease。否则同一 Run 会在每次 inspect 时重新被判为 orphan。

`detached_remote` 默认 `CanSubmit=false`、`CanCancelLocal=false`。若 provider driver 支持受认证的远端取消，则由 Runtime 投影 `CanCancelRemote=true`；适配器仍不能直接调用 provider driver。

### 5.4 历史未绑定 lease 的兼容判定

发布前已经创建的 lease 可能有 `run_id=''`。对于这类记录：

- 若同一 Session 存在唯一非终态 Run 且 legacy lease 仍有效，外部观察者必须保守投影为 `external`，并标记 `linkageState=legacy_unbound`，不得因为未绑定而恢复或终结它；
- 当前 owner 进程若能确认本地注册的 Run，应在下一次受 fencing 保护的 lifecycle 写入时回填绑定；
- lease 已失效且存在非终态 Run 时，按 `orphaned` 处理；
- legacy lease 有效、`run_id=''` 且没有 active Run 时，按 `reserved` 处理，因为它可能是旧版 mutation 或 admission 窗口；
- 出现有效 lease 绑定其他 run ID、多个异常 Run 或无法唯一匹配时，按 `inconsistent` 处理；读库失败时按 `unknown` 处理。两者均禁止提交新 Run。

兼容桥只在安全的 owner 或 recovery lease 内补齐关联，不能由任意观察者修改活跃 lease。

## 6. lease 与 Run 的原子关联

### 6.1 lease purpose 分类与迁移

历史版本的 `TryLockRuntime` 曾将不同调用统一写为 `purpose=run`，调用者同时包含完整 Agent 执行和 Session 删除、绑定、解绑、能力修改、Responses 恢复等短时维护操作。当前主路径已经使用显式 purpose；剩余 `TryLockRuntime` 仅是迁移期兼容桥，不能作为新代码的执行归属判定。

目标 purpose 至少分为：

| purpose | 允许绑定 active `run_id` | 典型使用方 | snapshot 语义 |
|---|---:|---|---|
| `admission` | 否；提交事务内转为 execution 并绑定 | 新 Run 的短时准入 | `reserved` / `admitting` |
| `execution` | 是，必须绑定 | Agent、ESM、Cron、Channel、Responses monitor | `local` / `external` |
| `recovery` | 是，恢复目标确认后绑定 | orphan terminalization、remote reattach | `reserved` / `recovering`，完成后转 execution 或 release |
| `mutation` | 否 | delete、bind、unbind、transfer、capability/config mutation | `reserved` |
| `fork` | 否 | Session fork | `reserved` |

应增加显式 purpose 的 lease API，并将所有 production 调用点迁移完成后，再把无 purpose 的 `TryLockRuntime` 降为有删除条件的兼容桥。新代码不得继续通过通用 `TryLockRuntime` 隐式获得 `purpose=run`。

显式 API 还必须把 active Run 检查放入 lease acquisition 的同一事务：

- `AcquireExecutionAdmission`：只有没有 active Run 时才取得 `purpose=admission`；若发现遗留 active Run，返回 `session_recovery_required`，由 Runtime 转入独立 recovery 操作；
- `AcquireRecovery(expectedRunID)`：只有目标 Run 仍非终态且没有其他有效 owner 时，才取得 `purpose=recovery` 并绑定该 run ID；
- `AcquireMutation` / `AcquireFork`：只有没有 active Run 时才成功，不能先取得 mutation lease 再在事务外检查；
- 所有 API 都以 SQLite 当前时间判断 expiry，并通过 owner/token/epoch CAS 完成接管。

客户端预留的新 Session ID 不能退化为进程内锁。`AcquireExecutionAdmission` 必须在同一数据库事务中确认 Session row 已存在；若产品允许首次提交隐式创建 Session，则该事务先以完整 canonical header 创建 Session，再取得 admission lease。创建冲突、重复 ID 或无法取得数据库 lease 时必须失败，禁止返回“无数据库 lease 但可继续执行”的兼容结果。

这样 active Run 与 admission/mutation/fork lease 同时存在只可能是历史异常或损坏状态，应投影为 `inconsistent`，而不是正常窗口。

数据库中历史 `purpose=run` 视为 `legacy_unspecified`：只有在 `run_id` 已绑定，或当前 owner 能以完整 lease identity 证明本地 Run 时，才按 execution 解释；否则按 `reserved` 或 `legacy_unbound` 保守处理。

purpose 变更只能由当前 lease owner 在同一 epoch/token 下通过 CAS 完成。`recovery -> execution` 必须在成功 reattach 与注册本地 runtime 的受控交接中发生；不得通过 release 后重新 acquire 制造无保护窗口。

### 6.2 关联不变量

对新建的可执行 durable Run，以下关系必须在 admission 成功前成立：

```text
sessions(id = S)
  + execution intent / conversation turn / input resource bindings / user entry
  + active session_runs(id = R, session_id = S)
  + started event
  + active session_runtime_leases(session_id = S, purpose = execution, run_id = R, epoch = E)
        ↕ 同一 SQLite 事务、同一 fencing 校验
```

具体实现必须扩展既有 Run admission 事务，或增加由 `ExecutionRuntime.BeginDurable` 调用的受控 store 方法；不得先创建 Run、返回给 adapter，再异步“尽力写入” lease `run_id`，也不得把 execution intent、用户 entry、输入资源绑定或 started event 拆到无 fencing 的后续事务。事务必须同时校验当前 owner、token、epoch、lease 未过期和允许转入 `purpose=execution` 的 admission purpose。

lease release 使用 tombstone 语义时可保留最后一个 `run_id` 作为审计关联，但 `state` 与 expiry 必须明确表明它已不再授予执行权。接管新的 lease 时只允许在 expiry/epoch CAS 成功后替换关联。

### 6.3 关键生命周期

```text
Acquire lease
  -> purpose=admission / state = reserved
  -> Begin durable Run + bind lease.run_id + purpose=execution（同一 fencing 事务）
  -> register local ExecutionRuntime with exact lease binding
  -> execute / heartbeat / persist events
  -> Finish durable Run（失败时保持 terminal-persistence-pending）
  -> unregister local runtime
  -> Release lease tombstone
```

以下顺序约束必须成立：

1. 启动前，先取得 `purpose=admission` lease；恢复现有 Run 时取得 `purpose=recovery` lease。若 lease 属于其他有效 owner，返回 `session_busy`，不得启动第二个 Agent。
2. durable admission 与 `run_id` binding 成功后，才允许将 Run 投影为可运行；若事务失败，释放刚获得的 lease，且 adapter 不得保留半初始化 active run。
3. 本地 execution registration 必须保存不可伪造的内部 binding：规范化数据库身份、session ID、run ID、owner instance ID、epoch 与 token identity。只有该 binding 与数据库快照完全一致时，snapshot 才可返回 `local` 和 `CanCancelLocal=true`。
4. heartbeat 或任一次 fenced 写返回 lease lost 时，当前 binding 立即失效并取消 `ExecutionRuntime`；旧 owner 绝不继续把事件、终态或工具结果写回该 Session。
5. 每次副作用工具执行前，以及 Approval/Question、限流等待或其他可能跨越 lease TTL 的阻塞结束后，Runtime 必须用当前 run ID、epoch 和 token identity 同步复核 lease。复核失败时不得调用工具。对于 HTTP/MCP/平台发送等无法由 SQLite 原子 fencing 的外部副作用，还必须使用稳定 operation ID 和目标系统幂等能力；不支持幂等的工具必须明确说明无法提供 exactly-once 保证，并禁止自动重放结果不明确的操作。不能把“结果写入被 fence”表述为“外部副作用绝不会重复”。
6. `FinishDurable` 的非 lease-lost 失败不可被吞掉。Run 必须进入可观察的“终态持久化待重试”路径，保留 active/blocked 投影与 lease，直到终态事务成功或本进程确定失去 lease。
7. 只有 durable 终态提交成功后，adapter 才能清除其本地 run 引用、pending projection 和本地 cancel function；之后才释放 lease。
8. 如果终态提交因 lease lost 失败，当前进程不得假装完成，也不得直接把 Session 置空；它应使旧 binding 失效、停止本地执行，并交由下一次 snapshot/recovery 裁决为 `external`、`detached_remote` 或 `orphaned`。

该顺序尤其禁止“`FinishDurable` 报错但仍调用 `FinalizeRun`”这一类只清内存、不收敛 durable 状态的路径。

## 7. 停止与恢复语义

### 7.1 统一 Runtime 控制操作

当前 Runtime-owned 操作为 `RequestSessionStop(sessionID)`。它先取得统一 snapshot，但 snapshot 只是控制操作的预期版本，不是永久授权。真正取消、远端控制或恢复时必须携带预期 `runID + lease epoch + linkage state`，在写入前重新执行 fencing/CAS；冲突时重新 inspect 并返回最新结果。HTTP handler、CLI、TUI、ACP 和 channel 不得各自查询 map 后决定取消。

| 快照状态 | 行为 | 结果代码/语义 |
|---|---|---|
| `local` | 调用对应 `ExecutionRuntime.CancelDurable`，持久化 `cancelling` 并保持 snapshot active，直到终态 | `stop_accepted` |
| `external` | 不调用本地 cancel，不改 lease、不伪造 Run 终态 | `session_run_owned_elsewhere` |
| `detached_remote` | 取得 recovery lease并复核远端记录；支持 provider cancel 时由 Runtime 请求远端取消，否则返回明确的不可控制结果 | `remote_stop_accepted`、`remote_stop_unsupported` 或 `session_busy` |
| `orphaned` | 先尝试取得 recovery lease；成功后按用户停止语义终结；失败则重新 inspect | `recovery_started`、`session_busy` 或 recovery error |
| `recovery_failed` | 以同一 run ID 发起一次显式 fenced recovery；不得绕过 lease 直接清除 active Run | `recovery_started` 或结构化 recovery error |
| `reserved` | 不抢占、不误报为运行；返回当前操作正在占用 Session | `session_reserved` |
| `idle` | 不做写入 | `no_active_run` |
| `inconsistent` / `unknown` | 不猜测、不清理、不提交新 Run | `session_execution_state_unavailable` |

这使 `no_active_run` 只在 durable 层确实没有非终态 Run 时返回。它不再被用于表达“本进程没有 APISession”。

对于 HTTP `POST /api/sessions/{sessionID}/stop`，建议映射为：

- `local`：接受取消，返回 `202 Accepted` 和 snapshot；客户端等待后续 Run/runtime event 确认终态；
- `external`：返回 `409 Conflict`，结构化错误码 `session_run_owned_elsewhere`，携带安全的 `runId`、durable status、可选 source 和 lease expiry；
- `detached_remote`：Runtime 成功提交 provider-side cancel 后返回 `202 Accepted`；provider 不支持取消时返回 `409 Conflict` 和 `remote_stop_unsupported`；
- `orphaned`：若成功开始用户触发的终结，返回 `202 Accepted` 与 `recovery_started`；恢复完成由事件或刷新确认；
- `recovery_failed`：允许用户使用同一 Stop 操作重试终结；成功启动返回 `202 Accepted`，再次失败返回包含 recovery reason/next retry 的 `503`，但不得返回 `session_run_active`；
- `reserved`：返回 `409 Conflict` 和 `session_reserved`，客户端根据 snapshot 刷新；
- `idle`：返回 `409 Conflict`，结构化错误码 `no_active_run`；
- `inconsistent` / `unknown`：返回 `503 Service Unavailable` 或等价 retryable 错误，不将未知状态折叠为空闲。

### 7.2 为什么外部 Run 不能由 UDP 停止

UDP 的可靠性、认证和顺序都不满足取消命令要求。把“stop”做成 UDP 广播会出现丢失、误杀、重放和无审计确认问题，也会让 lease fencing 失效。

这里的“外部 Run”指另一个 MothX 进程持有 lease；它与 `detached_remote` 的 provider-native 后台执行不同。后者可在 Runtime 取得 recovery lease 后使用既有 provider driver 控制。

若未来产品确实要求从任意进程停止另一个 MothX 进程持有的 Run，需另立方案并定义：持久化 `session_execution_commands`（或复用正式 control plane）、命令 ID、目标 run ID/epoch、owner 轮询与 ack、幂等性、超时、权限和审计。该协议的 owner 必须在自己的 fenced lease 下执行 `CancelDurable`。本方案明确不把这一能力作为修复当前不一致的前提。

### 7.3 orphan recovery

orphan 不是“看到 active Run 但本地 map 没有它”。只有在 SQLite 可证明没有仍有效的匹配 run lease 时，才可认为其没有 owner。

有效 lease 绑定其他 run ID 时属于 `inconsistent`，不得进入 orphan recovery；可验证的 provider-native 远端执行属于 `detached_remote`，也不得按普通本地 orphan 直接标记失败。

恢复过程必须如下：

```text
find non-terminal durable Run
  -> inspect current SQLite lease
  -> valid external lease: skip，仍投影 external
  -> no valid matching lease: acquire purpose=recovery lease by CAS
  -> re-read Run + lease under acquired epoch
  -> bind/verify run ID，解析 remote disposition
  -> detached remote: safe reattach / provider cancel / 保持 detached
  -> local orphan: 处理 pending Decision 并持久化 terminal state
  -> publish canonical Run state change，release lease
```

任何一次 acquire、复核或终结写入失败，都不能把 Run 直接标记成功/失败。CAS 未获胜表示另一个协调器正在处理，应重新投影为 `reserved/recovering`；本次恢复超过 `recoveryAttemptTimeout` 或确定失败时，本地遗留 Run 投影为 `recovery_failed`，数据库整体不可读时投影为 `unknown`，可验证的远端 Run 保持 `detached_remote`。恢复尝试必须记录 recovery reason、attempt、旧 epoch、last error、next retry 和时间；本机诊断需要时可附完整 lease identity，但恢复裁决仍只能读取 SQLite 和 Runtime 自持 binding。

orphan terminalization 的终态和原因必须区分触发源：

| 触发源 | Run 终态 | 原因 |
|---|---|---|
| 服务启动或周期性自动恢复 | `failed` | `owner_lost` / `process_terminated_while_run_active` |
| 用户对 orphan 点击停止 | `cancelled` | `cancelled_by_user_after_owner_loss` |
| provider 远端取消成功 | `cancelled` | `remote_run_cancelled_by_user` |
| provider 已自行失败或过期 | `failed` / `expired` | 采用 canonical provider terminal reason |

恢复终态操作必须复用 `DecisionService` 和 Runtime-owned store，在同一受 fencing 保护的收敛操作中完成：Run terminal row、terminal event、开放 ConversationTurn、pending Approval/Question 的终态 DecisionRecord。若现有 store 尚不能原子完成，应先扩展共享 Runtime transaction contract；不得由 adapter 先清内存 pending map 再单独终结 Run。

恢复调度不是可选优化。每个长期运行的 Runtime 必须按同一数据库 identity 启动共享 recovery coordinator，并满足：

1. 启动时扫描一次；对该 Session 完成恢复裁决或明确记录 `recovery_failed` 前，不接受它的新 Run；
2. 之后以不大于 `min(leaseTTL/3, 5s)` 的间隔扫描非终态 Run，不能依赖其他 owner 重启；
3. `InspectSessionExecution` 读到 `orphaned`、用户 Stop、用户提交以及 UDP lease notification 都应立即唤醒同一 coordinator；UDP 丢失不影响下一次周期扫描；
4. 用户提交遇到 orphan 时走 Runtime-owned `ReconcileSessionBeforeAdmission`：先用 recovery lease 收敛旧 Run，再以原 execution intent 重新执行 admission；若本次请求无法在恢复时限内完成，返回 `session_recovery_in_progress` 或 `session_recovery_failed`，不能笼统返回 `session_run_active`；
5. recovery lease 自身也必须 heartbeat、TTL 和 fencing；协调器在恢复中崩溃后，其他进程可在该 lease 过期后继续处理，不能产生第二个永久占用点；
6. 自动重试采用有上限的指数退避并持续保留下一次重试时间；进入 `recovery_failed` 后仍允许周期探测和用户显式重试，但 UI 不得将其显示成 Agent 正在执行。

多个进程可以同时扫描，但每个 Session 只有一个进程能通过 CAS 取得 recovery lease。扫描器只负责发现和调用共享 Runtime 操作，不拥有第二套 Run 状态机。

## 8. UDP 与本地内存的定位

### 8.1 UDP

现有 `LEASE_ACQUIRED`、`LEASE_RELEASED`、`LEASE_LOST` 与 `RUN_STATE_CHANGED` 通知仍保留。接收方处理改为：

1. 以 `sessionID` 使本地 execution snapshot cache 和事件 cursor 失效或唤醒刷新；
2. 用 `InspectSessionExecution` 重新读取 SQLite Run 和 lease；
3. 将新的 canonical snapshot 投影到 SSE/WebSocket/HTTP 读取者；
4. 对重复、乱序或来源为自身的通知保持幂等。

UDP 可以携带 origin/owner instance ID、PID、epoch、expiry、lease identity，并可在后续增加可选的 `runID`，用于忽略自身通知、诊断、刷新和去重。由于总线仅在本机回环网络广播，这些字段无需为隐私而脱敏或删除；具体载荷以实际诊断需要为准。接收方不得使用报文中的 owner、token、epoch 或 run ID 直接执行状态写入，必须重新读取 SQLite 并使用本进程已有的受控 binding 完成 fencing。

### 8.2 本地执行注册表

每个进程可以在 `internal/agentruntime` 内维护本地 execution registry，用于 `local` 判定和实际取消。注册键不能只有 run ID；必须使用规范化数据库身份、session ID、run ID 和 lease epoch，并在注册值中保留完整 owner/token identity。该 identity 可以投影到本机诊断接口，但控制操作只能使用 Runtime 自己持有的注册值，不能信任客户端回传值。注册表必须满足：

- 注册仅在 durable admission 与 lease/run binding 成功后发生；
- 注册的 lease identity 必须与 SQLite snapshot 的 owner、epoch 和 token hash 全部相同，才能投影 `CanCancelLocal=true`；
- 同一进程重新取得更高 epoch 时，旧 epoch 的注册项即使尚未清理也不能匹配为 local；
- lease `Lost` 通道关闭时立即使注册 binding 无效，再触发执行取消；
- deregister 仅在 durable terminal state 成功或确认 lease lost 后发生；
- 进程内记录丢失、启动重启或 adapter unload 不得把 durable Run 判定为空闲；
- 注册表不是跨进程锁、不是 durable recovery store，也不允许被 UI 直接查询为权威状态。

Serve 中的 `APISession` 可以成为该注册表的一个实现细节，但不再是 `CancelSessionRun` 的唯一事实来源。

## 9. 适配器与用户界面投影

### 9.1 API

现有 `SessionActiveRun` 应向后兼容地扩展，或由同一 runtime snapshot 新增字段。旧客户端仍可只读 `activeRun.runId` 与 `activeRun.status`；新客户端必须使用 `execution.state` 和 `execution.canCancelLocal` 决定操作。

建议的 JSON 投影：

```json
{
  "activeRun": {
    "runId": "run_...",
    "status": "running",
    "source": "webui"
  },
  "execution": {
    "state": "external",
    "phase": "executing",
    "ownerScope": "external",
    "canSubmit": false,
    "canCancelLocal": false,
    "canCancelRemote": false,
    "linkageState": "bound",
    "ownerInstanceId": "pid-1234-...",
    "ownerPid": 1234,
    "leaseEpoch": 7,
    "leaseExpiresAt": "2026-08-26T10:00:15Z"
  }
}
```

`ownerInstanceId`、`ownerPid`、`leaseEpoch` 和 `leaseExpiresAt` 都是本机诊断字段；需要时 API 也可投影 token identity。它们只用于状态解释、关联日志和刷新策略，浏览器回传这些值不能取得或接管 lease。对于 `unknown`，Run 与 owner 相关字段应保守返回，不可把上一份缓存伪装成事实。

### 9.2 WebUI

WebUI 的 `busy` 应继续对 `reserved`、`local`、`external`、`detached_remote`、`orphaned`、`recovery_failed`、`inconsistent` 和 `unknown` 保守为真，避免同一 Session 被再次提交。交互文案则按状态区分：

| execution state | 输入区 | 停止按钮 | 建议文案 |
|---|---|---|---|
| `local` | 禁止发送 | 可用；点击后显示 stopping | 正在此进程执行 |
| `external` | 禁止发送 | 禁用或替换为提示 | 正在另一进程执行 |
| `detached_remote` | 禁止发送 | provider 支持取消时可用 | 远端任务仍在执行，等待重新连接 |
| `reserved` | 禁止发送 | 禁用 | Session 正被维护或准备执行 |
| `orphaned` | 禁止发送 | 可用，标为“结束遗留执行” | 原执行进程已退出，正在自动恢复 |
| `recovery_failed` | 禁止发送 | 可用，标为“重试恢复” | 遗留执行恢复失败；显示原因和下次重试时间 |
| `inconsistent` | 禁止发送 | 禁用 | 正在确认执行状态 |
| `unknown` | 禁止发送 | 禁用并允许刷新/重试 | 无法确认执行状态 |
| `idle` | 可发送 | 隐藏 | 空闲 |

Session 列表、聊天页、审批视图和重连 SSE 应消费同一 snapshot。外部 Run 的 pending approval 不能被本地 UI 当成可直接响应的本地 waiter；是否跨进程响应审批属于独立 control-plane 问题。

### 9.3 其他入口

- CLI/TUI：显示“由另一进程执行”而不是允许新输入后因锁失败；本地取消只针对本进程的 Run。
- ACP：保持既有协议 projection，但其 run/cancel/recovery 决策改走共享 Runtime snapshot。
- Channel：继续用绑定 Session 与 runtime lease 防止污染；收到外部状态后不复制 Run 生命周期。
- OpenAI-compatible API：保留现有响应格式的兼容字段，并通过结构化 error code 表达 external ownership 和 unknown state。

## 10. 实施分期

### Phase 0：合同与观测基线（已完成）

1. [x] 为 active durable Run、external lease 和当前进程执行实体补充诊断字段与测试夹具。
2. [x] 枚举 lease 调用语义并引入 admission、execution、recovery、mutation、fork purpose；通用 `purpose=run` 已被限定为兼容桥。
3. [x] 固化 non-terminal Run、lease valid、admission/release 过渡窗口和 remote disposition 的判定。
4. [x] 架构测试禁止新增 adapter-local 的 Run/lease 状态判定。

### Phase 1：数据层与 Runtime snapshot（核心已完成）

1. [x] `internal/session` 提供单只读事务 execution facts，以及受控 lease/run binding helper；客户端或 UDP 回传值不参与控制授权。
2. [x] 显式 lease purpose 已接入 mutation/fork/recovery/admission/execution 主路径。
3. [~] `BeginIntentDurable` 与 `BeginDurable` 在生产 `RunStore` 上均走 fenced 原子 admission；真实双进程首次 Session ID 竞争已覆盖 Session 创建冲突与唯一 Run/event 结果；不实现扩展接口的嵌入 store 仍保留兼容桥，API/ACP/Channel 的跨入口原子事务合同仍需补齐。
4. [x] `internal/agentruntime` 已实现 `InspectSessionExecution`、带 lease identity 的本地注册和 detached remote 判定。
5. [x] 历史 `purpose=run`、`run_id=''` 租约有保守兼容路径。

### Phase 2：生命周期收敛（核心已完成）

1. [x] `BeginDurable`、`ReattachDurable`、`UpdateDurable`、`CancelDurable`、`FinishDurable` 和 shutdown 使用统一 binding/registration 顺序。
2. [x] terminal persistence 失败保留 active 状态、lease 和本地注册，并由 Runtime 异步重试。
3. [~] orphan recovery 已改为 lease-first、re-read、fenced terminalization；orphan 扫描、RecoveryCoordinator 启动/周期/wake 调度和不同 Run 的最多 8 个 worker 均接受 context deadline，超时后以独立短 deadline 写入 `recovery_failed`。真实进程 owner/recovery 崩溃接管测试已通过；仍需以真实负载验证慢/失败 orphan 不会推迟其他 Session，并补 SQLite 写失败验收。
4. [x] Responses background 的 keep/reattach/cancel 已进入 Runtime-owned `detached_remote` 主路径。
5. [~] 工具执行已有幂等记录和 side-effect 状态；Agent Core 已在副作用调用前通过 Runtime hook 重新校验 lease binding，并将 `ExecutionKey` 作为稳定 operation ID 注入 context。仍需补跨进程 TTL/恢复负载和外部幂等能力差异的专项验收。

### Phase 3：适配器投影和停止接口（核心已完成）

1. [x] Serve runtime snapshot、Session 列表和外部 session 更新消费 shared Runtime snapshot。
2. [x] `CancelSessionRun` 已降为 Runtime stop 的兼容薄包装，HTTP/Channel 使用结构化停止结果；Run API 对 external owner 返回 `409 session_run_owned_elsewhere`，不再写入外部 Run。
3. [~] WebUI、CLI/TUI、ACP、Channel 已迁移主要投影；Serve/ACP 的 Run 查询、取消和流状态已走 Runtime 边界，RunManager 不再参与生产状态裁决；需要完成全入口合同测试和最后的历史兼容回退清理。
4. [~] 旧响应字段保持兼容；停止和主要提交接口已有结构化结果。Serve 的 Run API 已覆盖“无本进程 RunManager 条目仍从 canonical store 查询”；仍需验证所有协议入口均按 fresh Runtime snapshot 区分 external、reserved、orphaned、recovery_failed 与 unknown，并清理残余 `session_run_active` 兼容投影。

### Phase 4：清理与强化（进行中）

1. [ ] 在迁移窗口结束后删除仅靠 `APISession.IsRunning()` 的生产兼容分支。
2. [ ] 在所有新入口确认迁移后删除 production 对无显式 purpose 的 `TryLockRuntime` 依赖；历史测试/读取兼容暂保留。
3. [~] 已有单进程、恢复竞争、架构守卫、UI 及真实子进程 `kill -9`/无 UDP 通知/恢复者再次崩溃测试；仍需 SQLite 写失败、多个 orphan 堆积与 race/全入口合同测试。

每个阶段完成后都必须保持：没有任何 adapter 能绕过 `ExecutionRuntime` 创建、取消、终结或恢复 durable Run。

## 11. 测试与验收标准

### 11.1 数据层和 Runtime 测试

1. **原子 admission**：Session row、execution intent、ConversationTurn、输入资源绑定、user entry、Run、started event、lease `run_id` 与 epoch fencing 要么按同一提交可见，要么全部不可见。
2. **外部 owner 投影**：子进程持有有效 lease 并运行时，父进程 snapshot 为 `external`，且 `CanSubmit=false`、`CanCancelLocal=false`。
3. **本地 owner 投影**：同一进程匹配 lease/run/registered runtime 时 snapshot 为 `local`，停止进入 `cancelling`，最终终结。
4. **epoch 精确绑定**：同一进程丢失 epoch N 并取得 epoch N+1 后，epoch N 的旧 ExecutionRuntime 不得投影为 local，也不能取消新 Run。
5. **purpose 隔离**：delete/bind/unbind/config mutation/fork 的有效 lease 投影为 `reserved`，不显示为 running 或 `inconsistent`；新 execution 才使用并绑定 `purpose=execution`。
6. **合法过渡窗口**：lease acquire 到 Run admission、Run terminal 到 lease release 的窗口显示 `reserved/admitting/releasing`，不触发告警或 orphan recovery。
7. **lease/run mismatch**：有效 lease 绑定其他 run ID 时返回 `inconsistent`，任何 observer 都不能自动 terminalize。
8. **事务快照**：在 Begin/Finish/release 并发点反复 inspect，结果只能是事务中真实存在的组合，不能拼接出伪 orphan。
9. **detached remote**：可恢复 Responses background Run 在无本地 lease 时投影为 `detached_remote`；reattach、provider cancel 和不可恢复降级分别收敛到规定状态。
10. **lease expiry**：持有者被杀死或心跳停止后，在 TTL 前仍显示 external；TTL 后只允许一个竞争者获得 recovery lease。
11. **fencing**：旧 owner 在被接管后不能写 Run event、终态、Decision 或 transcript；其本地执行收到 lease lost 后取消。
12. **副作用 fencing**：旧 owner 在 Approval/Question 或人工暂停期间跨过 TTL、由新 owner 接管后恢复时，必须在调用副作用工具前停止；支持外部幂等键的操作复用同一 operation ID。
13. **首次 Session 竞争**：两个进程同时以同一尚不存在的 Session ID 提交，只允许一个原子创建 Session 并取得 admission；另一个得到重复或 busy 结果，不能使用进程内 fallback 启动 Agent。
14. **legacy unbound lease**：有效外部空 `run_id` lease 不会被错误终结；失效后才可进入 orphan recovery。
15. **finish persistence failure**：模拟 terminal event/store 失败时，不能提前 deregister 或让 snapshot 变 idle；重试成功后才清理。
16. **recovery race**：两个进程同时发现 orphan，只允许一个写终态/attach；另一个重新读取并停止操作。
17. **orphan 终态语义**：自动恢复写 `failed/owner_lost`，用户停止写 `cancelled/cancelled_by_user_after_owner_loss`，且 Decision、Approval/Question、ConversationTurn 与 Run event 完整收敛。
18. **常驻观察者收敛**：进程 A 持有 execution lease 后被 `kill -9`，进程 B 在整个测试中不重启，并丢弃全部 UDP 通知；B 必须仅凭 SQLite 周期扫描在 `leaseTTL + recoveryScanInterval + recoveryAttemptTimeout` 内使本地 Run 进入终态、Session 变为 `idle`。
19. **恢复者再次崩溃**：进程 B 取得 recovery lease 后被杀死，进程 C 在 recovery lease TTL 后接管并完成同一 Run 的终结，不留下永久 `recovering/reserved`。
20. **恢复持久化失败**：注入超过单次恢复时限的 SQLite 写失败后，snapshot 变为 `recovery_failed` 而非 `running`，包含 attempt/next retry；解除故障后自动重试收敛。

### 11.2 API 与 WebUI 测试

1. Session 列表与聊天 runtime snapshot 对同一 external Run 都显示 running，且不提供错误可用的 Stop。
2. external Run 的停止请求返回 `session_run_owned_elsewhere`，而不是 `no_active_run`；Run、lease 和外部执行均不受影响。
3. local Run 的停止请求返回 `202`，在 durable terminal event 后才恢复输入。
4. detached remote Run 显示为远端执行；支持 provider cancel 时 Stop 返回 `202`，不支持时返回 `remote_stop_unsupported`。
5. mutation lease 与 admission/release 窗口显示 reserved，不显示“另一进程正在执行 Agent”。
6. orphan Session 在尚未取得 recovery lease 时显示自动恢复并允许“结束遗留执行”；取得 recovery lease 后显示 recovering。恢复成功后变 idle，或安全 reattach 后继续显示 active；超过单次时限则显示 `recovery_failed` 和可用的重试操作，绝不继续显示“正在执行”。
7. SQLite 读失败、lease 解码失败等 unknown 状态不应把 UI 解锁；刷新恢复后应回到正确状态。
8. Stop inspect 后发生 epoch takeover 或 Run 替换时，旧请求的目标 Run fencing/CAS 失败并重新投影最新 owner，不能取消新 Run。
9. UDP 通知丢失时，轮询/重连后的 SQLite snapshot 仍能收敛；重复和乱序通知不改变最终状态。
10. 同一 Session 的 WebUI、CLI/TUI、ACP、channel 投影具有相同 Run ID、状态、归属范围与提交禁止语义，仅协议/UI 格式不同。
11. 提交请求遇到已过期 lease 与本地 orphan 时会唤醒共享恢复；旧 Run 收敛后可用同一 execution intent 安全重试，不会永久返回 `session_run_active`。

### 11.3 架构验证

除 `internal/session`、`internal/agentruntime` 的明确 allowlist 外，静态架构测试应拒绝：

- 适配器直接读取 `session_runtime_leases` 后自行决定 local/external/orphaned；
- 适配器通过本地 `IsRunning()` 直接把 durable Run 解释为空闲；
- production 调用点继续用无显式 purpose 的 lease API 创建新的隐式 `purpose=run`；
- 新增 adapter-owned recovery loop、run store 或取消 state machine；
- UDP handler 直接改写 Run、lease 或执行取消。

实现阶段至少运行相关 `internal/session`、`internal/agentruntime`、`internal/serve/openaiapi`、`internal/acp`、`internal/serve/channels` 与 `internal/architecture` 测试；跨进程场景使用真实子进程和独立 temp Session DB，不以 mock UDP 代替 lease/fencing 验证。

### 11.4 2026-08-27 交接验证与下一步

已验证通过：

- `go test ./internal/agentruntime -count=1 -timeout=3m`
- `go test ./internal/session -count=1 -timeout=3m`
- `go test ./internal/serve/openaiapi -count=1 -timeout=4m`
- `go test ./internal/acp -count=1 -timeout=3m`
- `go test ./internal/architecture -count=1 -timeout=3m`
- `go test ./internal/tui -count=1 -timeout=10m`
- `go test ./internal/serve/channels -count=1 -timeout=12m`
- `go test ./internal/serve/channels -run '^TestHandleCommandStatusShowsRunState$' -count=1 -timeout=2m`
- UI 单元测试 13 个 suite、UI production build（此前已通过）。

本次实现后再次验证通过：

- `go test ./internal/agentruntime ./internal/session ./internal/serve/openaiapi ./internal/cron ./internal/architecture -count=1 -timeout=6m`
- `go test ./internal/session ./internal/agentruntime ./internal/serve/openaiapi -count=1 -timeout=6m`

本次入口边界改造后新增并通过：

- `TestGetRunReadsCanonicalStoreWithoutRunManager`：模拟新 Serve 进程读取其他进程创建的 canonical Run；查询不依赖本地 RunManager 内存条目。
- `TestRunAPICancelExternalOwnerReturnsStructuredConflict`：外部 execution lease 返回 `409 session_run_owned_elsewhere`，且 Run 保持 running。
- `TestRequestSessionStopRejectsStaleExpectedRunID`：旧 Run 的停止请求在新 Run 已接管时返回 target changed，不会误停新 Run。
- `go test ./internal/architecture` 新增 canonical Run 查询守卫，防止适配器直接读取 `session_runs`。
- `go test ./internal/serve/openaiapi ./internal/acp ./internal/agentruntime -run "^$" -count=1 -timeout=8m`：入口包编译检查。

新增回归覆盖：生产 `RunStore` 的 `BeginDurable` 原子 started-event admission、恢复 context 取消以及取消后 `recovery_failed` 标记仍可持久化；并发恢复结果按扫描顺序稳定汇总。

本次环境中 `internal/serve/channels` 全包、`internal/tui` 全包、`internal/acp`、`internal/cron` 和 `internal/agentruntime` 均通过；此前的 IPv6 loopback 限制和 `TestBuildAgentLoadsReplayState` compacted-summary 失败本次单包重跑未重现。标准 `/tmp` 临时目录下的全量 `go test ./...` 已完成，除 `internal/serve` 的 `TestSessionLifecycleRotateForcePastBusyRun` 外其余包均通过；该失败已在未修改的 `HEAD` 上复现，属于本轮之前的基线测试问题，不能作为本轮回归。UI 构建因当前环境未安装 `npm` 未执行。新增 `internal/agentruntime` 真实 `kill -9` owner/recovery 接管测试已通过。

换机后按以下顺序继续：

1. 在允许 localhost 双栈监听的环境重跑 `internal/serve/channels` 全包和跨入口合同；当前环境已通过，不再把 compacted-summary 场景列为已知失败。
2. `BeginDurable` 生产 `RunStore` 路径已使用原子 Run/event admission；已补两个真实进程同时竞争首次 Session ID 的验收，确认不会留下缺少 started event 的 active Run。仍需补进程在提交前后退出及多个入口同时提交的真实合同。
3. orphan 扫描、恢复快照、lease acquisition、active-run 查询及 recovery record/终态收敛写入均已传递 context deadline，并以最多 8 个 worker 隔离不同 Run；已补多个真实进程 `kill -9`、无 UDP 通知、恢复者再次崩溃接管、SQLite 终态写失败和慢/快 Session 负载隔离验收；仍需更大负载覆盖第 11.1 节第 18--20 项。
4. 已让 runs、chat-completions、Responses cancel/reconnect 基于 fresh Runtime snapshot 返回结构化错误；Serve/ACP 的 durable Run 查询和 Serve 取消入口已脱离 RunManager 状态裁决，Run API 已覆盖 external owner 冲突和 stale Run target fencing；仍需补齐其他入口的跨入口合同测试，并清理残余 `session_run_active` 兼容投影。
5. 已将副作用工具调用前的 lease recheck 和稳定 operation ID 收口到 Runtime hook；已补过期 lease 阻断、yolo 副作用分类、真实跨进程 TTL/恢复接管阻断、外部 HTTP header 投影和不支持幂等外部工具的 exactly-once 限制提示。第 11.1 节第 12 项专项验收已完成。
6. 全入口合同测试通过后，再删除 `APISession.IsRunning` 与无 purpose lease 的生产兼容分支；不要提前清理第 4 节标记为兼容桥的代码。

## 12. 失败模式与可观测性

应为每次 snapshot 判定和状态转移提供结构化诊断。本机诊断可以按需要包含完整 lease identity；字段是否默认展示由日志噪声和 API 兼容性决定，而不是隐私禁令：

```text
session_id, run_id, run_status,
execution_state, owner_scope, owner_instance_id, owner_pid,
lease_purpose, lease_epoch, lease_expires_at, lease_token_identity,
linkage_state, remote_disposition,
transition_reason, recovery_attempt, recovery_started_at,
recovery_deadline, recovery_next_at, recovery_last_error
```

建议增加计数器：

- `session_execution_snapshot_total{state}`；
- `session_execution_stop_total{result}`；
- `session_execution_orphan_recovery_total{result}`；
- `session_execution_orphan_recovery_seconds`；
- `session_execution_recovery_failed_total`；
- `session_execution_terminal_persistence_retry_total`；
- `session_execution_lease_run_linkage_total{state}`；
- `session_execution_remote_total{disposition,result}`。

告警和日志应重点关注：超过收敛上限的 `orphaned`、任何 `recovery_failed`、排除合法 admission/release 窗口后仍存在的 `inconsistent`、terminal persistence retry 持续失败、lease lost 后仍有旧 epoch 写入尝试，以及 external 状态被错误映射成 `no_active_run`。`reserved` 和有进展的 `detached_remote` 是正常业务状态，不应直接作为异常计数。

## 13. 开放问题

1. `owner_kind` 是否应由当前泛化的 `process` 提升为稳定的 Runtime source 分类，需要与 source/policy resolver 的既有枚举对齐后再决定；不应让 UI 依赖进程类型字符串。
2. 是否提供管理员级跨进程停止能力，应单独评审其认证、审计、幂等与 owner ack 合同；在此之前 external stop 明确返回冲突。
3. snapshot 缓存的时长和 SQLite 读压策略可在实现时压测决定，但缓存失效只能导致重新读取，不能在安全上把 external、detached_remote、orphaned 或 recovery_failed 降级为 idle。
4. detached remote 的具体 reattach/cancel capability 由各 provider driver 实现，但 capability 判定、状态机和持久化结果必须进入共享 Runtime contract；不得在 Serve adapter 留下第二套判断。

## 14. 结论

本问题的根因不是 UDP、SQLite 或内存锁中某一个单点“失效”，而是已有三层机制没有被投影为同一个执行语义：durable Run 表示运行事实，SQLite lease 表示跨进程执行归属，本地内存表示本进程控制能力。把它们压缩成单一 `running` 或单一 `APISession.IsRunning()` 都会产生误判。

本方案通过 Runtime-owned 事务快照、显式 lease purpose、带 epoch/token identity 的 lease/run 关联、detached remote 状态、lease-first orphan recovery 和按归属分类的停止结果，保留“外部有效执行仍是 running”这一正确并发保护。完成本文件第 4、10、11 节的剩余工作后，正常本地 owner 崩溃时 Session 最多只在 lease TTL 内继续显示 external；随后由不依赖 UDP 或进程重启的协调器在明确的调度上限内收敛。恢复基础设施故障也必须显式显示为 `recovery_failed`/`unknown`，不能把 Session 永久伪装成正在执行。
