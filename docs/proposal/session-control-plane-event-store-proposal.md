# Session 控制面与事件存储分离方案

> 状态：提案（待评审，不代表已实施）
>
> 日期：2026-10-18

> 实施进展（2026-10-18）：已落地第一阶段的 session-scoped append-only EventStore、尾部恢复/校验、目录同步、SQLite `session_event_intents` migration，以及 fenced `prepared -> appended -> committed` 基础协议；EventStore batch 以 64 entries 上限切分，限制一次 fsync 与 SQLite projection transaction 的锁持有时间；EventStore 拒绝跨 batch 重复 event ID；同进程 append cache 仅在日志文件大小未改变时免去全量 scan，任何 peer 写入、reset 或归档均会使其失效；`EventStoreWriteStats` 公开本进程 batch/event/byte 和 cache hit/miss 压力指标；`reconciling`/`blocked`/`corrupt`/`inconsistent` 状态拒绝新的 EventStore append；孤儿 Run recovery 已接入 intent 对账；现有 Session entry 已具备无损 EventRecord 编解码和 Runtime replay 读取边界；commit 会在 SQLite 写入不含 payload 的可重建 projection，并持久化 Session `content_store=event_store`；Runtime 新建且为空的 Session 已可通过 `Manager.AppendEventStoreEntries` 走 canonical append/replay；SessionRuntime BindSession 对 legacy root Session 在 mutation lease 下自动执行一次 export/migration；Session list 的 last-active/message-count、detailed preview、title 和 canonical replay search，以及 SSE/WebSocket 使用的 cursor/latest/before 分页读取，已能读取 EventStore projection/replay；conversation turn 的 leaf/run 查询已兼容 EventStore projection；EventStore Session 已具备 fork 的 committed-event 复制、ID/turn remap 和 child EventStore 创建路径；legacy Session 具备 mutation-lease 下的 export/migration 路径；`internal/architecture/event_store_guard_test.go` 禁止 Session/Runtime owner 以外的生产代码直连 EventStore/intent APIs；Run admission 的 user/turn-start payload、terminal assistant/turn-end payload 已在 EventStore commit 后移除 SQLite legacy payload rows。EventStore 达到单文件上限时按序号滚动切分 segment（最多 128 个活动文件），manifest 记录活动列表且缺失时由 scan 重建；单个 session 的 EventStore 损坏只污染该 session 的 replay，不影响邻居 session 或 `ListAll(sessionDir)`；epoch takeover 后旧 owner 的 `CommitEventIntent` 以 fenced CAS 拒绝，未提交 batch 不会进入 committed 读视图；projection 缺失可在多 segment 存储上完整重建。`sessions.content_store`/`sub_session.content_store` 由 migration 48 幂等收敛，避免历史半应用记录让既有数据库落入备份重建路径。直接调用旧 Session Manager 的 compatibility path 仍保留，直到所有入口完成 Runtime bind/migration 收敛。
>
> 实施进展（2026-10-19）：Session 内容的迁移与对账已收敛为一条 bind 路径上的不变量。EventStore 的 seq 以 1 为基（0 专用于「尚无 cursor」），legacy `entries.seq` 是数据库全局 AUTOINCREMENT，会与其他 Session 交错并可能在数值上落入 EventStore 区间，因此任何跨空间比较都必须按身份而非按范围判定，不能以数字大小推断它属于哪个存储。migration 依据持久化的 parent 链重建 transcript 顺序（而不是依赖进程内视图），并在同一个 fenced commit 事务里把 `conversation_turns` 的边界翻译到新序列，使 fork resolution 与 recovery 在迁移后仍指向真实存在的 turn marker；`TestMigrateSessionRemapsTurnBoundaries` 固定了这一行为并检查迁移后的 fork 仍能解析到该 turn。projection 的 `turn_id`/`run_id` 过去只取批量信封的值，而 migration 导出的 marker 记录两者为空，导致每个迁移过的 marker 都丢失 turn 身份，进而让 `conversation_turn.go` 的 COALESCE 回退、`recovery.go` 的 `ListOpenTurns` 全部失效；现在这两个列由解码后的 marker payload 自身推导（`exportMarkerTurnID`/`exportMarkerRunID`），`RebuildEventProjection` 共用同一函数，已翻转的 Session 在重建时自愈。对账不再只在「本次决定执行 migration」时才发生：`MigrateToEventStore` 在 Manager 已经以 EventStore 模式 load 成功的分支上同样调用 `reconcileControlPlane`，于是由更早构建翻转、边界仍指向已删除 legacy 序列的那批 Session，会在下一次 bind 时被 `repairUnmappedTurnBoundaries` 按 payload 中的 turn 身份重新定位（幂等、不取 lease、无需要新增 schema 版本），`TestConvergeFlippedSessionRepairsPreRemapTurnBoundaries` 覆盖了这条修复路径。迁移遇到不可解的 entry 类型仍然 fail closed：`custom`/`custom_message` 既没有结构定义、provider 投影，也没有声明的引用重写策略（fork 早已拒绝同一对类型），`DecodeEventRecord` 因而显式报错，使 export 整体失败而不是把它们从 transcript 中静默抹掉；`TestMigrateSessionRefusesUnsupportedEntryType` 证明拒绝后 legacy payload 与原 content store 均未被破坏。
>
> 实施进展（2026-10-19，读取与修复分离）：EventStore 的观测型读取被固定为绝不改动文件系统。此前 `InspectEventStore()` 复用 `Scan()`，而 `Scan()` 是会修复的追加路径原语：它在 `repairTail=true` 下截断崩溃尾部，并调用 `writeManifest()` 落盘那份可重建的 manifest。于是**一次只读的会话 load 就会写磁盘**。`Manager.Reload()` 在 `agent.RunWithUserMessage` 的 goroutine 里持 `m.mu.Lock()`，走到 `loadCommittedEventEntries -> InspectEventStore -> Scan -> writeManifest`，与 TempDir 清理相撞，会在 `internal/tui` 留下非确定的 `directory not empty` 泄漏；更关键的是它在语义上也不成立 —— 未持有 fenced writer lease 的路径没有资格决定哪些 bytes 应当作废。现在新增 `(*EventStore).Inspect()`：取同一把锁、跑同一套 `scanLayout()`/`scanState(layout, false)`，因此**损坏分类与 `Scan()` 完全一致**，只是既不截断、也不物化 manifest、更不填充 segment/append cache。路由后 `Scan()`→`scan(true)` 仍是唯一的修复入口，只由追加/commit/reconcile 一侧调用；`Inspect()`→`scanState(..., false)` 是唯一的只读入口，被 session load、fork、turn 终态与各入口投影共用。回归测试 `TestEventStoreInspectDoesNotMutateFilesystem` 注入真实崩溃尾巴（`batch_begin` + 无换行的半个 event），移除合法发布的 manifest，然后断言 `Inspect()` 只返回完整 batch、segment 的 bytes 与 mtime 逐字节不变、manifest 不被重建，最后再断言 `Scan()` 仍会截断该尾巴并重建 manifest；变异检验证明它非空洞（把 `scanState(layout, false)` 改成 `true` 即失败）。`Inspect()` 是内部方法，架构守卫 allowlist 无需新增条目。
>
> 同一轮实验还否证了一个看起来更「宽容」的设计，记录以免重做：让只读路径容忍一个未修复的尾部（引入 `tail_unrepaired` 状态，或在 `repairTail=false` 时直接 break）。三点结论。（1）带换行的孤立 `batch_begin` 是**格式良好的零事件 batch**，不是崩溃尾巴，因此常见的注入形状根本不触发截断；（2）真正残留的孤儿 bytes 必然撞上 `len(byID)!=0`，被判为 `blocked`（“event store contains bytes without SQLite intent”），宽容分支实际上不可达，而在已存在 committed intent 的真实会话上它会把状态报成 `healthy` —— 那是静默 masking，严格劣于原来的 `corrupt`；（3）任何新的非 `healthy` 状态都会硬失败掉 `session.go`、`event_turn.go`、`event_store_append.go` 里的 `!= EventStoreHealthy` 闸门，把良性的 load/append/终态一并打断。因此 §5.2「最后一行 JSON 不完整 → 只允许丢弃 segment 尾部未提交记录」在读路径上的含义是**原样保留并由分类暴露**，修复始终推迟给下一个持有 lease 的 writer。
>
> 实施进展（2026-10-19，对账下沉到 open）：对账不再是 `SessionRuntime.BindSession` 的副作用。此前 `convergeFlippedSession`/`reconcileControlPlane` 只能经由 bind→`MigrateToEventStore` 触发，而 serve/chat、background_run_coordinator、channels、ESM、ACP（`agentruntime.OpenSessionForWorkDir`→`session.OpenByID`）与 `internal/tui/commands_session.go` 都是用 `load()` 打开 Manager 且从不 bind：这些 Manager 回放的是 EventStore 已经拥有的那份 transcript 的 SQLite 副本，而被更早构建翻转的会话其 `conversation_turns` 边界永远指向已删除的 legacy 序列。这违反本方案自身的不变量「bind 过会话之后控制面必须与事件日志一致」，因此把一致性做成会话的属性而不是入口的属性：`MigrateToEventStore` 拆成薄壳——先调用新的导出方法 `ConvergeEventStore(ctx)`，只有在尚未收敛且非 sub-agent 时才调用 `exportLegacyTranscript(ctx)`（后者持有 `m.mu`，并在 `m.eventStoreMode` 时提前返回，即唯一不可逆的那一步）。`convergeEventStoreLocked` 挂在 `loadLocked` 的 `useEventStore` 分支末尾，于是 `New`/`Open`/`OpenByID`/`OpenByIDExact`/`ContinueRecent`/`Reload` 全部经过它；它按持久化的 `content_store` 列（而非本进程的内存视图）判定是否已翻转，用进程内的 `convergingEventStore` 标志终止「收敛后重载」的递归，并且全程不取 lease 也不取 reservation——真正内容变更的写围栏仍留在 `appendEventStoreEntriesLocked` 与 Runtime 终态事务里。`load()` 现在是取锁的薄壳，原体改为 `loadLocked(ctx)`；相应地 `Manager.Reload()` 必须调用 `loadLocked`，因为它自己持有 `m.mu`，而 `sync.RWMutex` 不可重入——这一点由变异检验固定：把 `Reload` 改回 `load()` 会让 `TestReloadConvergesFlippedSessionWithoutDeadlock` 直接撞上包级超时。回归测试是 `TestOpenConvergesFlippedSessionWithoutBind`（在一次真实 export 之后重新植入 legacy payload 副本并把控制行滚回 legacy 数字，随后**只**做一次 open 就断言副本被清除、回放来自事件日志、边界等于事件日志 marker，并断言再次 open 是不动点）与上面那个 Reload 测试；两条都被变异检验证明非空洞（移除 load 钩子分别得到 4 条重复消息与未清理的副本）。注意这两条断言刻意不调用 `MigrateToEventStore`，否则该方法自带的对账会让 load 钩子的缺失变得不可观察——第一版正是因此通过了变异体。
> 实施进展（2026-10-19，后台释放屏障）：serve 的 Run 准入与执行被拆成两层释放，目的是堵住「durable admit 已经写下、goroutine 还没交接」这段窗口。`SessionPool` 新增 `ReserveBackground()`/`LaunchBackground(release, fn)`：前者只占一个 drain slot 而不启动任何东西，后者把这份预留的所有权搬到新 goroutine 上（`go` 内 `defer release()`），因此 slot 从预留那一刻起连续持有到任务真正返回。此前 submit handler 是先做 durable admit、再 `Go(...)`，一旦 `Shutdown` 插在两步之间，就会出现一个没有任何人 drain 的活跃 Run，而进程已经开始关闭它的 session 数据库；现在 `Shutdown` 会等待调用方要么启动、要么归还。`LaunchBackground` 刻意**不**像 `GoCtx` 那样从 stop signal 派生取消上下文：durable Run 持有 fenced execution lease，必须走到自己的终态转换，在这里取消只会把它变成孤儿，交由 `SessionRuntime.Shutdown` 去取消才是正解。与之配套的是释放的两层划分：`runtimeRelease`（runtime pin + 并发槽 + 预启动期的 drain 折叠）与 `handOffRelease`（唯一的 drain 交接）。executor 必须把 `handOffRelease` 注册为**第一个** defer，从而成为最外层、最后执行的那个，这样 artifact collector 关闭、run context 取消、兜底终态写入与 `FinalizeRun` 全部落在 drain 的覆盖范围内。`Shutdown` 侧同时补上了有界等待：`waitBackground(ctx)` 用 `backgroundWG.Wait` 的 goroutine 加 `select` 实现，超时返回 `ctx.Err()` 而不是无限阻塞——一个永不返回的后台任务（等不到审批的 agent、无视 context 的 provider 流）是可用性问题，不该让进程变得杀不掉；归属仍由 fenced lease 决定，提前返回绝不会把一个活跃 Run 终态化。此外恢复路径也纳入同一屏障：孤儿 Responses Run 的 reattach 先 `ReserveBackground`，再把 slot 交给 `monitorRecoveredResponsesBackgroundRun` 作为最外层 defer，因为该 monitor 才是真正执行终态尾部的角色。
>
> 实施进展（2026-10-19，drain 顺序静态守卫）：上面那条「executor 必须先注册 hand-off release」是无法用类型表达的静默顺序不变量——写错照样编译、功能测试照样过，只有真实 shutdown 撞上活跃 Run 时才暴露。曾尝试用端到端测试固定它，并在此过程中得到一个**否证性结论**，记录以免重做：`approvalBlockingProvider` 这类 fixture 每次只产出一轮以 `StreamDone{tool_calls}` 结束的响应，agent loop 随后永久停在 `RequestToolApproval`；解析掉一个审批只会让它带着新的 pending id 重新发出同一个工具调用并再次挂住。也就是说，一个「全 deny」的 fixture 在构造上就永远到不了 executor 的延迟尾部，因而永远拿不到终态事件——用它断言 drain 顺序必然失败，且失败原因与被测不变量无关。反过来，一个能确定快速终态的 fixture 又会让断言变得空洞（drain 早已完成，顺序无从体现）。因此改为静态守卫 `internal/architecture/drain_release_guard_test.go`：`TestServeExecutorsRegisterHandOffReleaseFirst` 解析三个 serve 后台文件里的每一处 `LaunchBackground`，沿「wrapper 转发 → leaf 执行」的委托链走到真正的 executor，检查两件事——drain 参数落在由**被调方签名自身推导**的位置上（不是手写表，故新增前置参数无法悄悄把 slot 移出守卫视野），以及该 leaf 的第一个 defer 就是 hand-off release。四种变异均被证明非空洞：交换 monitor 的 defer 顺序、删掉 `executeBackgroundRun` 的首个 defer、两侧同时重命名 `handOffRelease`、让薄封装不再向下委托，各自都以精确信息失败。「两侧同时重命名」这一例尤其重要：它说明仅靠标识符匹配会被绕过，所以位置是从签名反推的，重命名会让守卫以「drain 参数消失」大声失败而不是静默通过。至于「drain slot 不得折进 `runtimeRelease`」这条同源不变量，最终**不由 AST 匹配强制**：预启动期把 drain 折进 `runtimeRelease` 是有意且正确的做法（好让既有的每条 admission 早退路径都顺手归还 slot），而启动后那份链上的副本是死代码，因为 executor 只调用单独穿线的 `handOffRelease`。它由结构保证——`LaunchBackground` 的第一参数与 executor 的独立形参必须是同一个值，折进去就等于丢掉这个形参，而这正是上面的位置检查所覆盖的。
>
> 关联方案：[跨进程 Session 执行归属方案](./cross-process-session-execution-ownership-proposal.md)、[Runtime 提交队列与执行完成屏障方案](./runtime-submission-queue-completion-barrier-proposal.md)、[SQLite 写压力优化方案](./sqlite-write-pressure-reduction-proposal.md)、[统一 Agent 核心与多入口 Runtime 方案](./agent-core-runtime-unification-proposal.md)、[Session 分叉与消息进度分叉方案](./session-fork-and-message-branch-proposal.md)
>
> 评审问题：Session 上下文是否应从共享 SQLite 的大字段表中分离为 Runtime-owned、session-scoped、append-only 的事件存储；如果采用 JSONL，如何在不削弱 SQLite lease/CAS、UDP advisory bus、SubmissionCoordinator 和 Runtime terminal 语义的前提下实现。

## 1. 决策摘要

本方案建议的目标架构是：

```text
SQLite control plane                         Session event store
-------------------                         -------------------
session catalog                             user/assistant messages
cwd / project / channel binding             tool call/result
run lifecycle                               reasoning / compaction
runtime lease / epoch / token               attachment/artifact references
decision / delivery                         branch and parent metadata
event intent / commit marker                append-only event segments
query/index metadata
```

结论不是“把 Session 改成一个普通 `session.jsonl` 文件”，而是：

> Session 的内容事实由一个独立的、可校验、可重放、按 Session 隔离的 Event Store 保存；SQLite 只保留需要事务、CAS、跨进程裁决和快速查询的 Control Plane。JSONL 是 Event Store 的首选物理编码，但不是对外 API，也不是适配器可以直接打开的文件。

本方案有五个不可妥协的判断：

1. `session_runtime_leases` 仍是唯一执行归属权威；Event Store 不产生 ownership。
2. UDP `SessionLeaseBus` 仍只是 advisory wake-up 和 projection hint，绝不变成 JSONL 锁或命令通道。
3. 每个 Session 只有一个 Runtime-owned writer；`SubmissionCoordinator` 仍是本地 admission authority。
4. Session 内容只有一份 canonical source。SQLite 中不再保留第二份 transcript；查询索引只能是可重建 projection。
5. Event Store 与 Control Plane 之间采用可恢复的 intent/commit 协议；不能用未经定义的“双写后祈祷一致”。

## 2. 为什么要分离

### 2.1 当前共享数据库混合了两种不同的数据

当前 `sessions.db` 同时承担：

- 高频增长、长 payload、主要是追加写的 transcript/entry 数据；
- 需要原子更新、CAS、跨进程 fencing 的 Run、lease、decision、delivery 和恢复状态。

这两类数据的最佳存储性质不同。Transcript 更接近事件日志；lease 和 Run 更接近控制数据库。把它们放在同一个 SQLite B-tree 中，会让长内容增长、WAL、checkpoint、索引和跨 Session 写锁共同扩大损坏与恢复的 blast radius。

### 2.2 目标不是消灭 SQLite

SQLite 仍然是最合适的本机控制面，因为以下状态必须支持事务和 fenced CAS：

| 状态 | 必须留在 Control Plane 的原因 |
|---|---|
| session catalog / cwd / binding | 跨入口列表、精确打开和安全策略的权威事实 |
| Run lifecycle | admission、terminal、recovery 和 completion barrier |
| runtime lease | `owner`/`epoch`/`token` CAS，唯一 ownership authority |
| pending decision | first-response-wins、deadline、replay 和 terminalization |
| delivery intent/operation | 幂等投递、平台状态和重试检查点 |
| event intent/commit marker | 跨 Event Store 与 Control Plane 的恢复协议 |
| 可重建查询索引 | 列表、搜索、分页和状态读取的性能 |

这里的“SQLite 只存元数据”应理解为“SQLite 不再保存长 transcript payload”，而不是把所有 Runtime 状态也搬到文件日志中。

### 2.3 写压力是本方案的一等目标

有考虑，而且这是拆分方案成立的必要条件：**不能只是把 transcript 从 SQLite 复制一份到 JSONL，再额外增加 SQLite intent 写入**。如果每条消息都执行一次 SQLite `prepare`、一次 JSONL `fsync`、一次 SQLite `commit`，写压力反而会上升，方案应被否决。

目标是把高频内容写入从 SQLite WAL/`BEGIN IMMEDIATE` 路径移出，同时让 Control Plane 只承载低频的生命周期里程碑：

| 写入类型 | 当前共享 SQLite | 目标方案 |
|---|---|---|
| 流式 text/reasoning delta | 内存，不落库 | 仍然只在内存，不落库 |
| 一轮内多个 tool result | 多个/批量 `entries` 事务 | 一个 Event Store batch；Control Plane 最多一个 intent/commit 组 |
| user/assistant turn | `entries` 长 payload + turn/run 更新 | Event Store payload；SQLite 只写 seq/摘要/intent/Run 状态 |
| lease heartbeat | SQLite fenced renewal | 保持现有按目录批量续期，不迁移到 JSONL |
| search/list/page index | 直接依赖 transcript 表 | 异步、可重建 projection，禁止每个 delta 更新 |
| terminal transaction | SQLite 多表原子事务 | 仍需 Control Plane 原子 terminal commit，并引用已 fsync 的 event batch |

因此，Event Store 的默认写策略是：

1. 一个 Agent 迭代内的多个事件组成一个 bounded batch，而不是每个 delta 一次写盘；
2. 普通内容 batch 只需一次 segment append + 一次 fsync；
3. SQLite 的 intent/commit 以 batch/turn 为单位，不以 event 为单位；
4. event index、全文搜索和预览 projection 使用受控异步批处理，丢失后从 Event Store 重建；
5. admission、lease heartbeat、Run progress 和 terminal 仍遵守现有 Runtime 事务边界，不为 Event Store 新增一套心跳或轮询写入；
6. batch 有大小、事件数和最长等待上限，长任务不能无限积压，也不能让一个大工具结果长时间占住 SQLite writer。

写压力的验收不是“SQLite 文件变小”，而是同一负载下同时满足：SQLite `BEGIN`/commit 次数下降、WAL 增长受控、写锁持有时间下降、lease heartbeat 不被内容写入拖延，且 `Event Store fsync + SQLite control commit` 的总延迟不高于当前 canonical entry 提交路径的预算。若 intent 协议使事务数或锁持有时间增加，必须优先合并 intent、扩大 bounded batch 或调整协议，而不是接受“功能正确但 SQLite 更忙”。

## 3. 目标数据布局

建议布局如下：

```text
<session-dir>/
  sessions.db
    sessions / projects / bindings
    session_runs / run_events
    session_runtime_leases
    decisions / deliveries
    event_intents / event_checkpoints
    rebuildable event index metadata

  sessions/
    <session-id>/
      manifest.json
      events/
        00000000000000000001-00000000000000001024.jsonl
        00000000000000001025-00000000000000002048.jsonl
      checkpoints/
        <seq>.json
      attachments/   # 仍由 Runtime attachment store 管理
```

实际目录名和分段大小可以改变，但以下语义固定：

- 一个 Session 一个独立 Event Store，不能让所有 Session 共用一个 JSONL 文件；
- Event Store 只追加，达到大小或事件数阈值后滚动 segment；
- 已封存 segment 不原地修改；压缩通过新 segment + manifest checkpoint 完成；
- `manifest.json` 不是唯一事实来源，损坏时可以从 segment 和 SQLite intent 重建；
- 适配器不得读写这些文件，必须通过 `internal/agentruntime` / `internal/session` 的 Runtime API 获取回放和追加结果。

## 4. Event Store 数据模型

每条记录是 Runtime event，而不是“当前消息快照”：

```json
{"schema":1,"seq":42,"event_id":"evt_...","session_id":"ses_...","run_id":"run_...","turn_id":"turn_...","kind":"assistant_message","parent_seq":41,"payload":{},"checksum":"sha256:..."}
```

最小字段：

- `seq`：Session 内单调递增，不依赖文件名或时间排序；
- `event_id`：全局唯一、重放和幂等使用；
- `session_id`、`run_id`、`turn_id`：防止跨 Session 或跨 Run 误合并；
- `kind` 与 schema version：由 Agent Core/Runtime 定义，不由协议适配器定义；
- `parent_seq`：保留消息分支和 fork 边界；
- `payload`：消息、工具调用、工具结果、推理、压缩和资源引用；
- `checksum`：至少覆盖 canonical JSON 的关键字段和 payload。

追加成功不等于事件已提交。记录还需要一个明确的 commit 状态来源，见下一节。

## 5. 跨存储提交协议

SQLite 事务不能原子覆盖普通文件系统写入，因此不能继续假设“entry、Run、turn end 在一个 SQLite transaction 中同时完成”。本方案用 Runtime-owned event intent 将两边连接起来。

### 5.1 提交流程

一次输入或 terminal turn 的逻辑提交如下：

```text
1. SubmissionCoordinator 获得本地 admission；Runtime 持有当前 fenced execution lease。
2. SQLite 事务写入 event_intent：session/run/turn、owner/epoch/token、event_id/seq 范围、intent 状态=prepared。
3. Event Store 在同一 Session writer 中追加完整 event batch，并 fsync segment。
4. SQLite 事务重新读取并校验 intent、当前 lease fencing 和 event batch checksum，写入 commit marker。
5. SQLite 事务同时更新 Run/turn/decision/delivery 的 Control Plane 状态。
6. Runtime 发布 canonical event projection；UDP 只发送 advisory wake-up。
```

`prepared` 不是用户可见的 Session 内容。只有带有有效 commit marker 的 event 才能进入回放和 adapter projection。

### 5.2 失败时的语义

| 失败点 | 恢复结果 |
|---|---|
| intent 成功，Event Store 未完整追加 | intent 超时后废弃；不进入回放 |
| Event Store 完整，SQLite commit 未完成 | 新 owner/恢复协调器校验 checksum 后幂等补交或废弃 |
| SQLite commit 完成，进程在响应前退出 | 事件已 canonical；回放和 Run 查询均可恢复 |
| owner 在追加后失去 lease | 旧 owner 不得继续 commit；新 owner 通过 SQLite recovery lease 处理 intent |
| 最后一行 JSON 不完整 | 只允许丢弃 segment 尾部未提交记录，并留下 recovery fact；中间损坏不能静默跳过 |
| SQLite control plane 不可用 | Event Store 不得自行宣布提交；Runtime 返回不可提交/待恢复状态 |

恢复必须是幂等的，判断依据是 `event_id + seq range + checksum`，不能依赖“文件里看起来有这一行”。

### 5.3 为什么不把 Event Store 的提交状态也只放 JSONL

如果 `committed` 只由 JSONL 自己声明，旧 owner 失去 lease 后可能把一个未完成的 side effect 或 terminal turn 自行标记为完成。提交状态必须回到 SQLite 的 fenced Control Plane，才能让接管者依据当前 epoch/token 重新裁决。

### 5.4 不同步时的唯一处理：对账，不猜测

Event Store 与 SQLite 不同步不是一个可以由适配器“尽量显示”的普通异常，而是一个 Runtime storage state。恢复协调器必须把它投影为 `healthy`、`reconciling`、`blocked` 或 `corrupt`，在裁决完成前禁止新的 side-effecting submission；不能把不确定状态伪装成 idle，也不能直接删除其中一边的数据。

#### Intent 状态机

`event_intents` 至少需要以下状态：

```text
prepared -> appended -> committed
    |          |
    v          v
  aborted   recovery_required
```

- `prepared`：SQLite 已预留 `intent_id`、`seq` 范围、checksum、Run/turn 和 fenced owner identity，但 Event Store 尚未证明完整写入；
- `appended`：Event Store 已 fsync 完整 batch，SQLite 仍未提交 canonical control state；这是可恢复的 in-doubt 状态，不是已完成；
- `committed`：SQLite 已在当前或 recovery owner 的 fenced 事务中确认 batch，并更新关联 Run/turn/decision；只有此状态进入 replay；
- `aborted`：明确证明 batch 没有完整写入，或已由 recovery owner 安全放弃；对应事件永远不能进入 replay；
- `recovery_required`：发现无法证明完整性、归属或 fencing，保留原始 bytes 和诊断，阻止写入，等待显式/Runtime-owned recovery；

状态只允许由当前 fenced owner 或 recovery lease 推进。旧 owner 即使持有 `intent_id` 也不能在失去 epoch/token 后把 `appended` 推进为 `committed`。

#### 对账规则

恢复时按 `intent_id + seq range + checksum` 对照 SQLite intent、segment manifest 和实际 JSONL bytes：

| 观察结果 | 处理 |
|---|---|
| `prepared` 且没有完整 bytes | 标记 `aborted`；释放未使用的 seq，或保留明确的不可见 gap |
| `prepared` 且 bytes 完整 | 在取得 recovery lease 后推进到 `appended`，再由 fenced 事务决定 commit 或 abort |
| `appended` 且 bytes 完整 | 幂等补写 SQLite commit；不得重复追加 bytes |
| `appended` 且 bytes 缺失/校验失败 | 标记 `recovery_required`，不猜测、不从中间行恢复 |
| `committed` 且 bytes 完整 | 正常 replay |
| `committed` 但 bytes 缺失/校验失败 | Control Plane 进入 `inconsistent`；禁止该 Session 提交，保留 Run/turn 事实并报警 |
| segment 有 bytes 但没有 SQLite intent | 不纳入 canonical replay，移动到 quarantine 或标记 orphaned bytes；不得扫描成用户消息 |
| 同一 intent 出现多个不同 checksum | `corrupt`；不自动选择“最新”或“最长”的版本 |

其中最重要的是最后三条：SQLite 已提交而 Event Store 缺失，不能为了恢复可用性伪造空事件；Event Store 有孤儿 bytes，也不能因为 JSONL 看起来合法就把它们提升为 canonical。两种情况都需要显式的不一致状态和可追踪的恢复记录。

#### “没有完整写入”的证明边界

只允许自动丢弃 segment 尾部的未完成记录：文件长度、JSON 解码边界、checksum 和 segment footer 能共同证明它没有成为完整 batch。中间记录损坏、seq 重复、checksum 不匹配或 manifest 与内容矛盾，都不能自动修剪，因为这可能删除已经提交的 canonical 内容。

#### 对用户和入口的表现

- `reconciling`：继续展示已有已提交历史，但不接受新的 side-effecting Run；只允许安全的只读诊断；
- `blocked` / `recovery_required`：明确显示 Session 需要恢复，保留 Session ID、Run ID、intent ID 和备份路径；
- `corrupt` / `inconsistent`：停止该 Session 的 Runtime 写入，不影响其他 Session 的 Control Plane 使用；
- 不允许 TUI、ACP、WebUI 或 Channel 各自决定回滚、跳过或继续；它们只投影 Runtime 的统一状态。

恢复失败时，原始 Event Store segment、SQLite intent 和诊断信息必须保留。`mothx pure` 之类的显式归档操作可以创建新 Session DB，但不能静默删除旧数据或声称已修复内容一致性。

## 6. Lease、UDP 和文件锁的关系

### 6.1 权威性顺序

```text
SQLite lease/CAS + durable Run
            |
            v
SessionRuntime / ExecutionRuntime
            |
            +-- SubmissionCoordinator
            +-- Event Store writer
            +-- terminal/recovery protocol
            |
            v
TUI / WebUI / ACP / Channel projections

UDP bus、进程内 cache、文件锁 = advisory / defensive mechanisms
```

只有 SQLite 中当前 owner 的 fenced binding 能授权追加和 commit。以下事件都不能单独改变状态：

- UDP `acquired`、`released`、`lost`、`ownership_snapshot`；
- UDP `event_appended` 或 `database_rebuilt`；
- 文件锁获取成功或超时；
- 本地缓存显示 idle 或 busy；
- wall-clock lease expiry，但尚未完成 SQLite fenced re-read。

据此派生出一条实现期才显现的裁决：**短预约（reservation）lease 的 released 墓碑不构成归属丢失**。`mutation`/`fork`/`legacy` 三种 purpose 只为串行化一次管理性变更而存在，从不携带 Run 执行权，也不在任何 execution/admission/recovery allowlist 内；它们释放后会话即回到 idle，下一个合法 writer 必须能够重新预约。若沿用 execution 语义让墓碑持续生效，任何一次管理员调用（删除兄弟会话、bind、migration）之后该会话会被永久冻结：后续每一次 fenced 写入都会观察到 “released”，而进程内又没有一个活着的 lease 可以自证。因此 `validateRuntimeLeaseTxContext` 对 reservation purpose 的 released 行直接放行，execution/admission/recovery/legacy run 则保留墓碑——对那些 purpose 而言，release 决定性地结束了 Run 的权威，迟到的旧 owner 写入必须继续被拒。两种情况下跨进程接管都不受影响：`Acquire` 仍然 bump epoch 并把行改回 active。

`AcquireContentWriteReservation`（purpose `legacy`，legacy 获取模式，无 active-Run 闸门）是这条顺序上唯一的**具名迁移桥**，供尚未走 Runtime bind 而直连 Session Manager 的调用者使用：它没有 Run binding 可供 fence，因此行为必须与旧的 SQLite transcript writer 一致（被动检查 lease、依赖 leaf CAS）。它不是执行授权——不带 Run ID、不能被 bind 到 Run、被排除在全部 execution purpose 白名单之外，单 writer 依旧由 epoch bump 保证。**Owner**：`internal/agentruntime` 的 EventStore convergence。**移除条件**：每个入口（TUI、CLI、serve/openaiapi background runs、channels、ACP）都先在 Runtime bound lease 下 bind 再写 canonical content，届时该函数与 `eventStoreWriteBinding` 中的调用点一并删除。

### 6.2 UDP bus 的新职责

UDP 继续用于：

- 提醒其他进程重新读取 Control Plane；
- 提醒 SessionRuntime 刷新 event projection；
- 通知数据库重建或 Session event recovery 需要关注。

它不用于：

- 传输 event payload；
- 传递 owner/token/epoch 作为执行凭据；
- 授权 JSONL 写入；
- 取消 Run、终结 Run 或宣布 terminal state；
- 代替 `session_runtime_leases` 或 `SubmissionCoordinator`。

UDP 丢包、重复、乱序和完全不可用时，正确性必须不变，只是其他进程晚一点重新读取。

### 6.3 文件锁

Event Store 可以使用 session-scoped 文件锁作为防御性保护，避免两个错误 writer 同时 append；但它不是业务锁。文件锁失败只能表示 `busy/unknown`，不能表示“另一个 owner 已失效”，也不能允许接管。文件锁的 owner 信息不参与 fencing，无法取代 SQLite lease 的 epoch/token。

## 7. 回放、索引和查询

Event Store 是内容 canonical source：

- `LoadSessionReplay` 只消费已 commit 的事件；
- fork 通过 `seq`、`parent_seq` 和新 Event Store checkpoint 表达，不复制第二份 SQLite transcript；
- 搜索、列表预览和分页可以维护 SQLite projection，但 projection 丢失时必须能从 Event Store 重建；
- projection 落后时只能显示明确的 `rebuilding/stale`，不能伪造空 Session；
- projection 中不得保存一份可被误认为 canonical 的完整 message payload；
- attachment/artifact 仍由 Runtime-owned attachment store 管理，Event Store 只保存不可变引用和必要的生命周期事件。

Event Store 的健康状态至少区分：`healthy`、`tail_recovered`、`needs_recovery`、`corrupt`、`unavailable`。`needs_recovery` 和 `corrupt` 都不能被适配器投影成 idle，也不能允许新的 side-effecting Run。

## 8. 并发、崩溃和长任务不变量

1. 同一 Session 同一时刻最多一个 execution writer；其依据是 SQLite fenced lease，不是 UDP 或文件锁。
2. lease 续期失败是 availability problem；只要没有 fenced takeover，不能仅因 wall-clock expiry 或单次 heartbeat timeout 取消活跃 Run。
3. 旧 owner 的所有 event commit 都必须重新验证 epoch/token；失去 fencing 后不能写入 canonical terminal state。
4. `ExecutionRuntime.Completion` 仍是后续 submission 可以开始的唯一屏障；`EventRunFinished` 或 JSONL 尾部出现一行都不是屏障。
5. terminal turn 的 Control Plane 状态只有在 Event Store batch 完整、校验通过且 commit marker 成功后才能关闭。
6. provider stream 的 partial response、retry、tool call uncertainty 和 approval/question 仍由 Agent Core/Runtime 管理，不由 Event Store 自行推断。
7. 不得盲目重放已经发出的 tool call；Event Store 只能记录事实和恢复 intent，副作用重试仍需 Runtime 的 operation fencing/idempotency policy。
8. event log 损坏不得让其他 Session 的 Control Plane 失效；单个 Session 进入 `needs_recovery`，其他 Session 继续可用。
9. UDP 缺失不得阻塞恢复；恢复扫描必须由存活 Runtime 定期执行。
10. 所有入口仍只能调用同一 SessionRuntime/Event Store API；不能在 TUI、ACP、Channel 或 WebUI 复制 append、replay 或 recovery 逻辑。

## 9. 方案比较

| 方案 | 结论 | 原因 |
|---|---|---|
| 继续全部放共享 SQLite | 不作为长期目标 | 控制面和长内容共享 B-tree/WAL，单库损坏影响面大；可继续作为当前稳定基线 |
| 一个全局 `sessions.jsonl` | 否决 | 所有 Session 互相扩大损坏面，跨进程 append 和恢复困难 |
| 每 Session 一个普通 JSONL | 不足 | 没有 checksum、commit intent、fencing 和 segment recovery，不足以承担 canonical store |
| 每 Session 独立 Event Store，JSONL segment + SQLite control plane | **推荐** | 故障半径小，追加模型自然，控制状态仍保留事务/CAS，能够通过 intent 协议恢复 |
| 每 Session 独立 SQLite | 可作为备选 | 隔离性好，但仍有 B-tree page corruption，且控制面/内容面边界不够清晰；不能解决全部目标 |
| WAL/UDP 代理写者 | 否决 | UDP 不可靠；常驻代理会成为新的 ownership、failover 和生命周期复杂度中心 |

## 10. 不变量与验收标准

方案被接受前，必须证明：

- 五类入口（TUI、CLI、WebUI/API、ACP、Channel）对同一 Event Store 产生相同 canonical event、seq、run/turn ownership 和 terminal 结果，只允许协议投影不同；
- 两个进程竞争同一 Session 时只有一个能取得 execution lease 和 Event Store writer；UDP 禁用时结果仍正确；
- owner 在 `prepared`、append、commit 各窗口 `kill -9` 后，下一 Runtime 能幂等恢复，不产生重复消息、不伪造完成、不绕过 fencing；
- 旧 owner 在 epoch 被接管后不能追加 canonical event、不能 commit intent、不能完成 side effect；
- Event Store 尾部截断可安全恢复，中间 checksum 损坏被显式标记，不能静默跳过；
- 删除、fork、attachment/artifact replay、decision replay、compaction 和 session listing 不依赖一份不可重建的 transcript SQLite 副本；
- 单个 Session event store 损坏时，其他 Session 的 Run、lease、decision 和列表仍可用；
- `ExecutionRuntime.Completion`、terminal transaction 语义和 session admission queue 不因存储拆分而改变；
- 架构守卫继续拒绝 adapter 直接访问 JSONL、直接构造 provider content、直接操作 lease 或绕过 DAO/Runtime。

每条标准的落地位置（便于回归时定位，而不是新增要求）：五入口 canonical 一致性由 `internal/agentruntime/session_event_store_cross_entry_test.go` 的跨入口契约测试固定；「删除、fork、attachment/artifact replay、decision replay、compaction 和 session listing 不依赖一份不可重建的 transcript SQLite 副本」还额外由对账下沉固定——`TestOpenConvergesFlippedSessionWithoutBind` 证明仅仅 open 一个会话就能清除残留副本并让控制面对齐事件日志，因此该性质不再取决于某个入口是否恰好调用过 `BindSession`；旧 owner 在 epoch 接管后不能追加/commit 由 fenced CAS 测试固定；尾部截断与中间损坏显式化由 EventStore 修复测试加上 `TestEventStoreInspectDoesNotMutateFilesystem`（只读路径不得截断、不得物化 manifest）固定；fork/delete/attachment/decision/compaction/listing 不依赖 transcript 副本由 cursor 奇偶性、migration 的 turn 重映射与 projection marker 身份推导固定；单会话隔离由 per-session store 目录与 `ListAll(sessionDir)` 测试固定；`ExecutionRuntime.Completion`、terminal transaction 与 admission queue 未被存储拆分改变（本轮没有触碰它们）；架构守卫是 `internal/architecture/event_store_guard_test.go`。

尚未自动化、需要在评审时明确为后续项的三条：**两进程真实争抢**目前只有 fenced CAS 的逻辑证明，没有跨进程压测夹具；**`prepared`/append/`commit` 三个窗口各自的 `kill -9`** 目前是逻辑覆盖（每个窗口的恢复分支都有单测），没有 SIGKILL harness；**后台 drain 的顺序**（executor 必须先注册 hand-off release）目前只由 `internal/architecture/drain_release_guard_test.go` 静态固定，没有端到端夹具——这不是偷懒：任何「挂得住」的 fixture（例如审批阻塞型 provider）在构造上永远到不了延迟尾部，因而拿不到终态事件，而任何能快速终态的 fixture 又让顺序断言变得空洞。该守卫本身用四种变异证明了非空洞。三者都属于测试基础设施缺口，不是已知缺陷。

## 11. 主要代价与剩余问题

该方案不是“把 SQLite 换成文本文件”，而是把复杂度从 SQLite 单体持久化移动到一个可恢复的跨存储提交协议。主要代价包括：

1. Event intent、commit marker 和 recovery coordinator 需要新的 durable 状态和测试矩阵；
2. 文件系统 fsync、rename、目录 durability 和 Windows 文件锁语义需要单独定义；
3. fork、compaction、搜索、分页和 projection rebuild 需要从事件重放而不是直接查 `entries`；
4. 备份工具必须同时理解 Control Plane、Event Store segment 和 attachment store，不能只复制 `sessions.db`；
5. 旧版数据库兼容和事件 schema 演进需要明确版本策略；
6. SQLite 结构损坏不会完全消失，lease/Run/decision 控制面仍可能损坏；但损坏影响面更小，且 Session 内容不会与控制面共享同一个 B-tree。

已命名的剩余债务（有 owner 与移除条件，不是“暂时不管”）：

- **`custom`/`custom_message` 阻塞 migration**。`EntryType` 声明了这两个类型，但它们没有结构定义、没有 provider 投影，fork 侧也显式拒绝（“没有声明的引用重写策略”），今天没有任何生产代码写入它们。为了不把它们从 transcript 中静默抹掉，`DecodeEventRecord` 对它们报错，`orderedSessionEntries` 因而让整个 export fail closed，legacy payload 与 `content_store` 保持不变。**Owner**：`internal/session` 的 EventStore codec（`event_entry.go`）。**移除条件**：要么这两个类型从 schema 中删除，要么为它们补齐结构、provider 投影与 fork 的引用重写策略并在 `DecodeEventRecord`/`load()` 两处接入；届时 `TestMigrateSessionRefusesUnsupportedEntryType` 必须改写成正向 round-trip 测试。在此之前，携带这类行的 Session 永远留在 legacy store，这是有意行为，不是缺陷。

- **Runtime 持有的 terminal 持久化重试不受 pool drain 跟踪**。`internal/agentruntime/execution_persistence.go` 的 `startTerminalPersistenceRetry` 在前台有界尝试因 ctx 到期而失败后另起一个 goroutine，继续把 fenced 终态事务写完。它不属于 `SessionPool` 的 `backgroundWG`，因此 `Shutdown` 的有界 drain 不等待它。这不是所有权漏洞：该循环只在三种情况下退出——`FinishDurable` 成功、`ErrRuntimeLeaseLost`（fenced 接管）、或 `r.done` 被关闭，且 `canRetryTerminalPersistence` 每轮都重新校验 execution 仍 active、终态未变；前台那次有界尝试也只是响应性上限，Runtime 在此期间保持 execution lease 存活。真正的缺口是**可观测性**而非正确性：进程退出与这次后台重试之间没有任何显式的完成屏障，目前只靠 lease fencing 与 `done` 关闭兜底，没有测试证明「Shutdown 返回后不再有终态写落盘」。**Owner**：`internal/agentruntime` 的 ExecutionRuntime 终态路径。**移除条件**：要么给该重试一个 Runtime 侧可等待的句柄并让 `SessionRuntime.Shutdown` 等它（注意不能像 `LaunchBackground` 那样参与取消，否则会把活跃 Run 变孤儿），要么用测试固定「Shutdown 之后终态写入必已停止或已成功」这一性质并将其写入 §10。

- **`runtimeRelease` 之后才注册的延迟尾部今天不做任何 durable 写**。drain slot 之所以仍要跨越这段尾部（artifact collector 关闭、run context 取消），是防御性的：现行代码在这些步骤里没有数据库写入，所以把它们排除在 drain 之外暂时不会造成可见的错误。一旦将来有人往这段尾部加进任何 durable 副作用，「hand-off release 必须是第一个 defer」就从加固变成正确性要求。**Owner**：`internal/serve/openaiapi` 的三个 executor。**移除条件**：无需移除；这条记录的作用是说明为什么当前该不变量只有静态守卫而没有功能测试，并提醒后来者不要往尾部塞 durable 写而不回头检查守卫。

- **`AcquireContentWriteReservation` 与其 `legacy` reservation purpose**。见 §6.1：它是直连 Session Manager 的调用者唯一的 canonical content 写入口，owner 与移除条件同 §6.1。
- **manifest 只是诊断产物**。它由修复型 `Scan()` 发布，缺失时重建，今天没有任何生产代码读取它（只有 `event_store_test.go` 断言其内容）。因此只读路径不物化它不会损失任何能力；反过来说，将来若真要让 manifest 承担读取职责，必须先让它成为受 fencing 保护的权威，而不是反过来给读路径开一个写盘口子。

因此，本方案的成功标准不是“永不损坏”，而是：

> 高增长的 Session 内容不再成为共享 Control Plane 的 B-tree 负载；任何单点损坏都具有明确边界、可检测状态、可重放来源和不绕过 lease fencing 的恢复路径。

## 12. 明确不采用的简化

以下做法即使实现成本较低，也不属于本方案：

- 让适配器直接 `os.OpenFile` 追加 JSONL；
- 用 UDP 广播的 owner/token/epoch 授权写入；
- 用文件锁成功替代 SQLite lease；
- 在 SQLite 和 JSONL 中各保存一份完整 transcript；
- 通过“JSONL 最后一行存在”判断 Run 已完成；
- 发现 JSONL 损坏就静默跳到下一行；
- 为绕过控制面损坏而自动删除或覆盖原 Session 文件；
- 让旧 owner 在 lease 丢失后继续完成 pending intent；
- 在 adapter 中实现第二套 replay、checkpoint、恢复或提交队列。

## 13. 评审结论请求

本提案请求确认的不是具体文件名，而是以下架构决策：

1. 是否接受“SQLite Control Plane + per-session Event Store”作为长期方向；
2. 是否接受 JSONL 仅作为 Event Store 的物理格式，而不是公共 Session API；
3. 是否接受 `session_runtime_leases` 继续作为唯一 ownership authority；
4. 是否接受 UDP 永远只做 advisory wake-up，不承担锁、命令或提交裁决；
5. 是否接受 Event Store 与 Control Plane 必须通过 intent/commit/recovery 协议连接，而不能依赖普通双写。

在这五项未确认前，不应开始 schema、文件布局或迁移实现；否则很容易先做出一个看似简单、实际绕过 Runtime fencing 的 JSONL 兼容层，最终形成第二套 Session 生命周期。
