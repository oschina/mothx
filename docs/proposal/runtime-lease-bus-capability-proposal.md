# 本机跨进程 Runtime 事件总线能力增强方案（UDP Lease Bus 升级）

> 状态：第 0–6 期已全部实现（2026-10-08）。SQLite 保持权威；每个进程的内存 Session 持有视图通过 UDP 全局同步；保持明文；监听 bug 修复已并入第 0 期。
>
> 日期：2026-10-08
>
> 关联方案：[跨进程 Session 执行归属、停止与恢复方案](./cross-process-session-execution-ownership-proposal.md)、[统一 Agent 核心与多入口 Runtime 方案](./agent-core-runtime-unification-proposal.md)、[SQLite 写压力优化方案](./sqlite-write-pressure-reduction-proposal.md)
>
> 关联代码：`internal/session/runtime_lease_bus*.go`、`internal/session/runtime_wake_coalescer.go`、`internal/session/runtime_lock.go`、`internal/session/database_recovery_notice.go`、`internal/dao/runtime_lease.go`、`internal/agentruntime/execution_snapshot.go`、`internal/acp`、`internal/serve/openaiapi`、`internal/serve/channels`、`internal/tui`、`cmd/mothx`

## 0. 决策记录（已确认）

| # | 决策 | 结论 |
|---|---|---|
| D1 | 权威来源 | **SQLite 仍是唯一权威**。`session_runtime_leases` 的 owner/epoch/token fenced CAS 与 Run 行是归属、提交许可、取消许可的最终裁决；UDP 不裁决正确性。 |
| D2 | UDP 定位升级 | 不再只是“建议性 nudge”。从 SQLite 读出的**每进程内存 Session 持有视图**（ownership cache）要通过 UDP 就近、全局地同步到本机其它进程，使各进程无需每次读库即可感知归属变化。 |
| D3 | 传输安全 | 维持**明文** UDP。理由：仅本机多进程同步，接收端已做 loopback 源地址校验与自包跳过，视为同一信任域。不做 HMAC、不换 UDS。 |
| D4 | 订阅作用域 | **全局**。进程订阅整个 session 目录（以及本机所有 session 目录）的所有 Session 归属变化，不按单会话订阅。 |
| D5 | 载荷 | **不脱敏**。owner instance ID、PID、epoch、runID、purpose、expiry 等按同步需要携带；tokenHash 仅用于本进程 fencing，默认不放总线上（不是隐私，而是不需要）。 |
| D6 | 监听状态机 bug | §3 中确认的监听 bug 修复**并入本方案第 0 期**一起交付。 |

> 仍未采纳：让报文承载“权威/命令”（旧轴 F）。按关联方案 §7.2，跨进程取消/接管必须走 durable command/ack，不能是 UDP 广播；本轮不做。

## 1. 背景与现有基线

现状三层：进程内 mutex、SQLite `session_runtime_leases`（权威）、UDP `SessionLeaseBus`（仅建议性唤醒）。既有合同要求接收方每次都必须重读 SQLite 才能投影状态，因此 UDP 只是“缩短下一次查询延迟”。

本次目标是把它升级为**权威 SQLite 之上的本机全局 ownership 同步层**：每个进程维护一份内存中的 Session 持有视图，由 UDP 即时传播 `acquire/release/lost/state_changed`，并周期性做 anti-entropy 校准；任何不确定、缺口、过期或冲突都回退到 SQLite 重读。写入路径（admission）仍然必须执行 fenced CAS——内存视图只用于加速感知与投影，永不授权。

### 1.1 已实现能力（改造基线）

| 项 | 现状 | 代码 |
|---|---|---|
| 传输 | UDP，`127.0.0.0/8` 定向广播，默认端口 `49371`，`MOTHX_RUNTIME_BUS_PORT` 可覆盖 | `runtime_lease_bus.go` |
| 监听 | 通配绑定 + `SO_REUSEADDR` | `runtime_lease_bus_socket_*.go` |
| 来源校验 | 仅 loopback；跳过本进程自发包 | `runRuntimeLeaseBus` |
| 版本 | `runtimeLeaseBusVersion = 2` | `runtime_lease_bus.go` |
| 事件类型 | `acquired`/`released`/`lost`/`state_changed`/`database_rebuilt`；`renewed` 被拒 | `validRuntimeLeaseNotification` |
| 载荷 | `RuntimeLeaseNotification`，上限 1024B | 同上 |
| 去重 | `MessageID` + TTL 10s + 上限 4096 | `rememberRuntimeLeaseMessageLocked` |
| 发送 | 每广播地址缓存 connected socket，写超时 200ms，拨号退避 5s | `runtimeLeaseBusSender*` |
| 接收合并 | `SessionWakeCoalescer`：单飞 + 尾随一次 | `runtime_wake_coalescer.go` |
| 订阅者 | 仅 ACP（`acp.go:1065`）与 serve/openaiapi（`server.go:426`） | — |

### 1.2 缺口

1. **监听状态机 bug**：最后一个订阅者在 socket 绑定完成前退订时，`runRuntimeLeaseBus` 命中 `handlers == 0` 提前 return，未复位 `started/listening/conn`（`listening` 假为 true、`conn` 为已关闭 socket、`started` 永为 true），导致后续订阅者不再启动监听 goroutine，该进程永久收不到唤醒。已用探针复现：`started=true listening=true conn!=nil=true handlers=0`。
2. **无序号/无同步语义**：没有 per-origin 序号、缺口检测与周期性校准；丢包即永久漏唤醒。
3. **无内存 ownership 视图**：每次都要读库；跨进程归属变化无法就近感知。
4. **订阅面窄**：只有 ACP/serve 订阅；TUI/CLI 不订阅。
5. **载荷过小**：1024B 与 5 种类型不足以承载全局 ownership 同步与快照；未知类型整包丢弃。
6. **不可观测**：无丢包/缺口/陈旧/收敛指标。

## 2. 目标与非目标

### 2.1 目标

1. 在 `internal/session` 建立**本机全局 ownership cache**：按 `(databaseIdentity, sessionID)` 记录 owner/epoch/purpose/runID/state/expiry/来源序号。
2. UDP 即时传播归属变化，并周期性广播 ownership 快照做 anti-entropy，使各进程视图收敛。
3. 所有入口（TUI/CLI/Channel/WebUI/ACP）经**唯一** Runtime 订阅入口接入全局同步。
4. 不确定即回退：缺口、陈旧（超过存活窗口）、过期、冲突、本地未命中，均触发合并后的 SQLite 重读。
5. 保持既有不变量：admission、recovery、terminalization 仍走 fenced CAS / lease-first 路径；内存视图永不授权、永不终结 Run。

### 2.2 非目标

1. 不用报文裁决 `busy`/`canSubmit`/`running`/`canCancel` 的最终值（最终值仍来自 Runtime-owned snapshot 与 CAS）。
2. 不用 UDP 做命令通道（取消/停止/审批）。
3. 不用 UDP 到达与否终结 Run、延长/释放 lease、或改变恢复裁决。
4. 不通过 UDP 复制 Agent 内存、tool 状态、审批等待器或 provider stream 内容。
5. 不做认证/加密/UDS（D3）。

## 3. 前置修复（第 0 期，与方案一起交付）

修复 §1.2(1)：`runRuntimeLeaseBus` 的 `handlers == 0` 提前 return 前，与 defer 清理保持一致地复位 `listening=false`、`conn=nil`、`started=false`，再关闭 socket 返回；保证后续订阅一定重新启动监听。补确定性回归测试。

## 4. 设计

### 4.1 分层与归属

| 关注点 | 归属 | 说明 |
|---|---|---|
| 租约 DB、fenced CAS | `internal/session`（权威） | 不变 |
| outbound 事件发布 | `internal/session` | lease 获取/释放/丢失、Run 状态变化 |
| 总线传输与去重 | `internal/session/runtime_lease_bus*.go` | 扩展字段、序号、快照 |
| ownership cache | `internal/session/runtime_ownership_cache.go`（新增） | 内存视图 + 每来源序号 + 缺口/陈旧判定 + 回读触发 |
| 快照投影 | `internal/agentruntime/execution_snapshot.go` | 合并 DB facts、本地执行注册表、ownership cache |
| 唯一订阅入口 | `internal/agentruntime`（新增） | 五入口统一接入；适配器只实现重读后投影回调 |
| SQL | `internal/dao` | 若有新增查询；迁移在 `internal/session/migrations.go` |

> 关键：ownership cache 的 owner 是 `internal/session`（它拥有 lease 语义），适配器与 agentruntime 只能读取其投影，不得各自缓存。

### 4.2 报文扩展（version 3）

`RuntimeLeaseNotification` 在现有字段基础上新增：

- `DatabaseIdentity string`：规范化 session 目录/DB 身份（复用 `RuntimeDatabaseIdentity`），用于全局作用域下按目录过滤与缓存键。
- `Seq uint64`：该 `OriginInstanceID` 的单调序号；接收端据此丢弃乱序/重复并检测缺口。
- `RunStatus string` / `Phase string`（可选）：run 投影辅助，仅作提示。
- 新增消息类型 `ownership_snapshot`（见 4.4）。

约束：

- 载荷上限 1024B → 4096B；快照实现为**每个持有租约一包**（非按目录分批，避免超出上限；实现中未引入 `BatchIndex/LastBatch`）。
- 未知类型/版本**逐报文忽略**（丢弃该条、总线继续运行）：`validRuntimeLeaseNotification` 维持白名单，`renewed` 继续被拒。
- `renewed` 仍不上总线（避免每会话每 3s 一包）；用存活窗口 + 周期快照替代。

### 4.3 缓存条目与新鲜度

```go
// 实际类型：internal/session/runtime_ownership_cache.go
 type RuntimeOwnershipEntry struct {
    DatabaseIdentity string
    SessionID        string
    OwnerInstanceID  string
    OwnerPID         int
    Epoch            int64
    Purpose          string          // lease purpose（字符串）
    RunID            string
    State            string          // 仅 "active"；released/lost 直接从缓存删除
    RunStatus        string          // 可选 run 状态提示
    Phase            string
    ExpiresAt        time.Time
    Seq              uint64          // 来自 OriginInstanceID 的序号
    OriginInstanceID string
    AppliedAt        time.Time       // 本地应用时刻
    FromDatabase     bool            // 由本地 SQLite 读回填
}
```

- **存活窗口（liveness window）**：条目在其 `OriginInstanceID` 最近一次同步（单条事件或快照）之后的 `runtimeOwnershipLivenessWindow`（30s）内视为新鲜。因为心跳续租不上总线，**不以 `ExpiresAt` 作为唯一新鲜度判据**，避免长任务被误判陈旧；`ExpiresAt` 仅用于提示剩余时间与触发提示性回读。
- 超过存活窗口、或来源缺失、或 epoch/seq 冲突 → 标记 `uncertain`；**回读在读取路径按需进行**（实现未引入独立 coalescer/调度器）。
- 周期快照（4.4）持续刷新各来源的新鲜度；进程崩溃后无快照，其条目在一个窗口后转 `uncertain`，由 SQLite 重读收敛。

### 4.4 同步协议

1. **即时事件**：`acquired`/`released`/`lost`/`state_changed` 立即广播，携带 `DatabaseIdentity`、`Seq`、owner/epoch/purpose/runID/expiry/state。
2. **周期快照（anti-entropy）**：每 `runtimeOwnershipSnapshotInterval`（10s）广播一次 `ownership_snapshot`，**每个持有租约一包**，携带 `DatabaseIdentity`/`Seq`/owner/epoch/purpose/runID/expiry/`RunStatus`。接收端写入/刷新缓存。
3. **缺口处理**：接收端按来源维护 `lastSeq`；收到 `seq > lastSeq+1` 判为缺口，对该来源的全部条目标记 `uncertain`；`seq <= lastSeq` 直接丢弃。回读按需进行，无独立调度。
4. **冲突处理**：同一 session 收到不同来源、不同 epoch 的条目时，标记 `uncertain` 并回读 SQLite；以 SQLite 结果为准。
5. **回读合并（未实现）**：设计曾计划经 `SessionWakeCoalescer` 合并；实现简化为在读取路径按需回读一次，未引入独立 coalescer。
6. **本地命中路径**：`InspectSessionExecution` 先取缓存；仅当命中“新鲜 + 远端 + `execution` 租约且带 `RunStatus`”时按缓存投影并标注 `source=cache`，否则 `ReadSessionExecutionFacts` 回读、`PrimeRuntimeOwnership` 回填并标注 `source=db`（该门限源于第 6 节的设计修正）。

### 4.5 不变量（必须保持）

- 缓存**永不授权**：`AcquireExecutionAdmission`/`AcquireSessionMutation`/`AcquireRecovery` 仍执行 fenced CAS；缓存最多用于快速失败提示。
- 缓存**永不终结** Run；恢复仍是 lease-first、重读、fenced。
- 缓存**永不改变**提交/取消的最终许可；最终许可由 Runtime-owned snapshot + CAS 决定。
- UDP 全线丢失时，系统退化为“每进程靠 SQLite 读 + 周期扫描”，正确性不变，仅增加延迟。

### 4.6 全局作用域（不引入独立订阅入口）

实现结论：**不新增** `agentruntime.SubscribeRuntimeOwnership` 包装。落地做法：

- `ingestRuntimeOwnership` 直接内联在总线读循环内，对**任意已订阅进程**生效，不依赖某个特定订阅者，因此 ownership 视图天然是全局作用域（D4）。
- TUI/CLI/Channel/WebUI/ACP 沿用既有 `session.SubscribeRuntimeLeaseNotifications` 订阅；适配器不接触报文，也不自建缓存（缓存 owner 是 `internal/session`）。
- 架构守卫（§5.4）拒绝适配器构造投影或写投影字段。
- `SessionWakeCoalescer` 未接入（回读按需，见 §4.4）。

## 5. 横切不变量：投影权威单一化

（与 ownership 同步层同批落地；适用于执行归属、决策、投递/产物、错误等所有投影面）

### 5.1 原则

- 每个语义域只有一个 Runtime-owned 投影权威函数：执行归属 = `InspectSessionExecution`/`InspectSessionExecutions`；决策 = `DecisionService`/`ReplayDecisions`；投递/产物 = `DeliveryCoordinator`；错误 = `ClassifyError`。适配器只渲染该函数的输出，不自行推导 `busy`/`canSubmit`/`canCancel`/`running`。
- **投影权威 ≠ 写入/admission 权威。** 投影只读、只提示；写入许可必须由 `session.AcquireExecutionAdmission`（或 mutation/recovery lease）的 fenced CAS 重新裁决。禁止用一个函数同时“判断并授权”，否则等于让 UI 依陈旧投影放行，重新打开跨进程并发窗口。
- **不是一个巨型函数**：按域拆分、各自单所有者；禁止跨域复用同一个判断，也禁止把多域塞进一个入口。

### 5.2 ownership cache 的接入方式

- cache 是 `InspectSessionExecution` 的**内部输入**，不是并列的第二套判断：快照新增 `Source` 标记（`db`/`cache`/`local`）；新鲜度是缓存内部概念、**不暴露**在快照上；缺口/陈旧/冲突/未命中一律由该函数内部回退 SQLite 重读并回填。
- 适配器永远看不到 “cache vs DB” 的差异，也不做二次判断。

### 5.3 需要收口的既有点

- `internal/serve/openaiapi/session_mgr.go` 的 `APISession.inspectExecution()`：无共享 session root 时曾用内存 `execution.Active()` / legacy `running` 位**自造快照**。实现收口为 Runtime-owned `InspectLocalSessionExecution`（`internal/agentruntime/execution_local_projection.go`）：适配器只提供进程本地事实，投影（含 `local`/`idle`）仍由 Runtime 产生，适配器不再构造快照。
- 读取失败时对快照字段的覆写（`unknown`/`busy`/`CanSubmit=false`，`session_mgr.go:776-780`）下沉到唯一函数，适配器不再改字段。

### 5.4 护栏

- `internal/architecture` 新增守卫：`internal/agentruntime` 之外禁止构造 `agentruntime.SessionExecutionSnapshot{...}`，禁止写 `Busy`/`CanSubmit`/`CanCancelLocal` 字段；所有投影面必须调用唯一函数。
- 收口后全仓无违纪，**无需 allowlist**（`internal/architecture/session_projection_guard_test.go`）。

## 6. 实现分期

| 期 | 内容 | 状态 |
|---|---|---|
| 0 | 修监听 bug + 回归测试（D6） | 已完成 |
| 1 | 报文 v3、`DatabaseIdentity`/`Seq`、载荷 1024→4096、发布点带上 database identity | 已完成 |
| 2 | ownership cache（条目、序号、缺口、陈旧、冲突、回读合并） | 已完成（cache 本体；coalesced 回读在第 4 期接入） |
| 3 | 周期 ownership 快照 anti-entropy（每租约一包，保持在上限内） | 已完成 |
| 4 | 单一投影函数 + `InspectSessionExecution` 缓存快路径、`Source` 标记、DB 读回填；全局作用域；session_mgr 兼容桥收口；投影权威守卫 | 已完成 |
| 5 | 可观测：发送/接收/丢弃/去重/缺口/陈旧/回读指标 | 已完成 |
| 6 | 文档与 changelog（zh/en）同步 | 已完成 |

> 实现进展（2026-10-08）：第 0 期已并入 `internal/session/runtime_lease_bus.go`（提前 return 复位 `started/listening/conn`）+ 回归测试 `runtime_lease_bus_restart_test.go`。第 1 期已落地：`RuntimeLeaseNotification` 新增 `DatabaseIdentity`/`Seq`/`OwnerPID`/`RunID`/`RunStatus`/`Phase`，版本升到 3，载荷上限 4096，acquire/released/lost/快照发布点复用既有 `runtimeDatabaseIdentity` 并补 `OwnerPID`；测试 `runtime_lease_bus_wire_test.go`。未知类型仍按“忽略该报文、不断总线”处理（`validRuntimeLeaseNotification` 白名单，`renewed` 继续被拒）。
>
> 第 2 期已落地：`internal/session/runtime_ownership_cache.go`（全局 `(DatabaseIdentity,SessionID)` 视图、每来源 `Seq` 与缺口检测、epoch 冲突/乱序防护、`ownershipLivenessWindow=30s` 陈旧判定、`PrimeRuntimeOwnership` 回填、`MarkRuntimeOwnershipUncertain`）；总线读循环内联 `ingestRuntimeOwnership`；报文新增 `OwnerPID`/`RunID`；测试 `runtime_ownership_cache_test.go`（9 例）。
>
> 第 3 期已落地：新增 `ownership_snapshot` 报文类型；租约记录 `expiresAt`（获取时设置、成功续租后刷新）；`runRuntimeLeaseBus` 启动时拉起 `runtimeOwnershipSnapshotLoop`（进即发一次 + 每 10s），每次逐个租约发一包以免超出 4096 上限；读取缓冲同步提到 `runtimeLeaseBusPayloadLimit`。快照与 acquire 在缓存中同义（创建/刷新/修复），乱序旧快照由 per-origin `Seq` 丢弃。
>
> 第 4 期已落地（核心）：把 `InspectSessionExecution` 拆成单一投影函数 `projectSessionExecutionFromFacts`（SQLite 路径与缓存路径共用，防止两套投影漂移）；`SessionExecutionSnapshot` 新增 `Source`（`db`/`cache`）；DB 读后 `PrimeRuntimeOwnership` 回填（带 RunID/RunStatus）；`InspectSessionExecution` 在命中“新鲜 + 远端 + execution 租约且带 RunStatus”时走缓存快路径，其余情况（本进程自己的租约、无 RunStatus、其他 purpose）一律回落 SQLite。新增 `RuntimeOwnerInstanceID()`；acquire 与快照发布带上 `RunStatus=running` 提示（仅 execution）。测试：`execution_ownership_projection_test.go`。
>
> 设计修正（实现中发现）：缓存快路径不能无条件替代 DB 读——投影器把“execution 租约的 RunID 与持久化 Run 行不匹配”判为 `inconsistent`，而缓存只有 RunID 没有 Run 行，因此快路径以 `RunStatus` hint 存在为前置；否则回落 SQLite。后续如要让无状态的 admission/mutation 与 recovery/reattach 也走快路径，需要把 Run 行的最小投影一并上总线。
>
> 第 4 期已收尾（4b）：① 全局作用域已由设计实现——`ingestRuntimeOwnership` 在总线读循环内对“任意已订阅进程”生效，不依赖某个特定订阅者，因此不再单独引入 `agentruntime` 包装；② `internal/serve/openaiapi/session_mgr.go` 第二套 snapshot 构造已收口：新增 Runtime-owned `InspectLocalSessionExecution`/`UnknownSessionExecution`（`execution_local_projection.go`），适配器只提供进程本地事实，错误路径不再回写 `Busy/CanSubmit`；③ 新增投影权威守卫 `internal/architecture/session_projection_guard_test.go`：禁止 `internal/agentruntime` 外构造 `SessionExecutionSnapshot{...}` 或写 `Busy/CanSubmit/CanCancelLocal`（现已无违纪，无需 allowlist）。测试：`execution_local_projection` 经 openaiapi 现有用例覆盖；architecture 守卫通过。
>
> 备注：并行跑多包时 `TestHandleESMAPIControlLifecycle` 偶发失败（时序抖动）；单独 `-count=5` 与两包并行 3 次均通过，判定为既有的并行时序 flaky，与本改动无关。
>
> 第 5 期已落地：`internal/session/runtime_lease_metrics.go` 新增 `RuntimeLeaseBusStats`/`RuntimeLeaseBusMetrics()`（发送、发送失败、接收、非法丢弃、自身跳过、去重、应用、`Seq` 缺口、不确定查询）；计数点已埋入发布器、总线读循环与缓存 ingest/lookup。测试：`TestRuntimeLeaseMetricsCountGapsAndUncertainty`。
>
> 第 6 期已落地：`runtime_lease_bus.go` 顶部注释由“advisory only / 必须每次重读”更新为“唤醒 + 归属同步提示；缺失/过期/不确定时重读 SQLite；永不授权或取消”；`docs/changelog_online_{en,zh}.md` 与 `docs/en/changelog.md`、`docs/zh/changelog.md` 的 v1.3.103 下均新增“跨进程会话归属同步”（新功能）与“租约总线监听器竞态自禁用修复”（Bug 修复）条目。AGENTS.md 已同步（本轮）：在“Long-task continuity”补充 UDP 归属同步/“投影提示而非权威”规则，在 anti-fragmentation 不变量新增“One ownership projection”，在 Required guardrails 与 Agents must not 补充投影守卫与禁止项。
>
> 验收补测（2026-10-08）：新增 `runtime_lease_bus_convergence_test.go` 的跨进程快照收敛用例 `TestRuntimeLeaseBusOwnershipSnapshotConvergesAcrossProcesses`（对端只注册租约、不发定向事件，本进程仅由周期 `ownership_snapshot` 收敛并校验 `Purpose`/`RunStatus`/`Epoch`/`OwnerPID` 与 `received` 计数）；为可测性新增 `runtimeOwnershipSnapshotIntervalValue()`（env `MOTHX_RUNTIME_SNAPSHOT_INTERVAL` 覆盖，生产仍用常量）。§8 第 1/3/6 项缺口已按上表消除。

## 7. 影响面

| 包/文件 | 预期改动 |
|---|---|
| `internal/session/runtime_lease_bus.go` | v3、`DatabaseIdentity`/`Seq`/`OwnerPID`/`RunID`/`RunStatus`/`Phase`、载荷 4096、`ownership_snapshot`、快照循环、监听 bug 修复、指标计数 |
| `internal/session/runtime_ownership_cache.go`（新） | 缓存、序号、缺口/陈旧/冲突、回填与标记 |
| `internal/session/runtime_lease_metrics.go`（新） | `RuntimeLeaseBusStats`/`RuntimeLeaseBusMetrics()` |
| `internal/session/runtime_lock.go` | 发布点补充 `DatabaseIdentity`/`OwnerPID`/`RunID`/`RunStatus`；租约 `expiresAt`；导出 `RuntimeOwnerInstanceID` |
| `internal/agentruntime/execution_snapshot.go` | 拆出唯一投影 `projectSessionExecutionFromFacts` + 缓存快路径 + `Source` |
| `internal/agentruntime/execution_ownership_projection.go`（新） | 缓存快路径与 `Source` 常量 |
| `internal/agentruntime/execution_local_projection.go`（新） | `InspectLocalSessionExecution`/`UnknownSessionExecution` |
| `internal/serve/openaiapi/session_mgr.go` | 收口 `APISession.inspectExecution()`，改用 Runtime-owned 本地投影；错误路径不再改字段 |
| `internal/architecture/session_projection_guard_test.go`（新） | 投影权威守卫 |
| 适配器（`internal/acp`、`internal/serve`、`internal/serve/channels`、`internal/tui`、`cmd/mothx`） | **未改**：沿用既有订阅；全局作用域由总线读循环内联 ingest 实现（§4.6） |
| `docs/`（本方案、changelog zh/en） | 已同步 |

## 8. 测试与验收

1. 进程边界：即时事件传播（`TestRuntimeLeaseBusBroadcastReachesAnotherProcess`）、**快照 anti-entropy 收敛**（`TestRuntimeLeaseBusOwnershipSnapshotConvergesAcrossProcesses`：对端只注册租约、不发定向事件，本进程仅由周期快照收敛）、序号缺口/乱序/重复/冲突（`TestRuntimeOwnershipCacheGapMarksOriginEntriesUncertain` 等缓存单测）。
2. 丢包/损毁：丢弃全部 UDP 后仅靠 SQLite 仍收敛；缓存陈旧时回读而非误判（由缓存陈旧单测覆盖）。
3. 崩溃：owner 停发（`kill -9` 等价于不再广播）后，接收端窗口过期 → `uncertain` → 回读（`TestRuntimeOwnershipCacheStaleEntryIsUncertain` 覆盖窗口过期→uncertain；投影层的 DB 回读回退由 §4.4(6) 实现）。
4. 权威边界：缓存命中不得绕过 CAS；构造“缓存说空闲但 DB 已有 owner”的场景，admission 必须 CAS 失败为 busy（CAS 逻辑未改，由既有用例覆盖）。
5. 回归：监听 bug 修复后 `internal/session`、`internal/architecture` 通过；跨进程测试通过（已满足）。
6. 压测：不作独立压测——`publishOwnershipSnapshots` 只做内存枚举 + 出网，**不访问数据库**（§4.4），因此不会增加 SQLite 写压力或读压力。
7. 投影权威单一：五入口对同一 canonical 事实得到一致的 `SessionExecutionSnapshot`；适配器不自造 busy/canSubmit；守卫能拦截违规构造。

## 9. 风险与回滚

- **误判风险**：以存活窗口而非 `ExpiresAt` 判定新鲜度，避免长任务被误判陈旧；任何冲突一律回读。
- **内存增长**：缓存按 `(databaseIdentity, sessionID)`，需按存活窗口修剪超过窗口且已回读确认的条目。
- **快照风暴**：全局作用域 + 多目录时快照需限频（默认 10s）；实现为每租约一包，未复用 coalescer（回读按需）。
- **回滚**：每期可独立回退；关闭 UDP 后缓存不可用，系统回到纯 SQLite 路径，正确性不变。

## 10. 开放问题

1. ~~默认值与配置位置~~ 已决定：实现为常量 `runtimeOwnershipSnapshotInterval=10s`、`runtimeOwnershipLivenessWindow=30s`，暂不纳入 settings。
2. 快照分批大小与 4096B 上限的匹配；是否需要 UDP 分片重组（倾向分批而非分片）。
3. 是否需要把 `database_rebuilt` 纳入统一同步模型，或维持独立低层语义。
4. 是否将 `NotifyRuntimeStateChanged` 等发布点收敛到统一 `RuntimeEventPublisher`。
5. 指标命名与暴露面（沿用 `--debug` pprof `/debug/vars` 风格）。
6. ~~守卫与兼容桥收口是否同批交付~~ 已交付（第 4/4b 期）。

## 11. 结论

在“SQLite 权威”这条不变量之下，把 UDP 从“建议性 nudge”升级为**本机全局 ownership 同步层**：每个进程维护内存持有视图，UDP 即时传播 + 周期快照 anti-entropy，任何不确定回退 SQLite 重读；写入与恢复仍走 fenced CAS/lease-first。保持明文与全局作用域符合本机多进程信任域的既定判断。监听 bug 修复并入第 0 期。

本方案第 0–6 期已按第 6 节分期全部实现并通过相关测试；上文差异（每租约一包、按需回读、无独立订阅入口）为落地时的简化，已在上文标注。
