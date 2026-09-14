# SQLite 写压力优化方案(synchronous 降级、事务合批、心跳合并)

> 状态:阶段 1–3 已实施(2026-09-14)。已落地:`synchronous(NORMAL)` 默认 + `MOTHX_SQLITE_SYNCHRONOUS=FULL` 逃生门、会话域 `AppendMessages` 事务合批(agent 工具结果批量落盘)、每目录租约心跳调度器 + DAO `RenewBatch`。阶段 0 大部分落地:busy 重试与 begin 等待指标(`internal/db.BusyRetryStats`/`BeginWaitStats`)经 `--debug` pprof 服务器的 `/debug/vars` 暴露(`mothx_sqlite`);压测形状 A/B/C 实现于 `internal/session/write_pressure_test.go`(`MOTHX_WRITE_PRESSURE_SCALE` 控制负载倍数),形状 D 由既有测试套覆盖(见 §4.2);WAL/checkpoint 上报与 p50/p99 直方图仍待建。以下为方案与多进程论证原文。
>
> 日期:2026-09-14
>
> 关联方案:[跨进程 Session 执行归属方案](./cross-process-session-execution-ownership-proposal.md)、[统一 Agent 核心与多入口 Runtime 方案](./agent-core-runtime-unification-proposal.md)、[Desktop 知识库方案](./desktop-knowledge-base-agent-proposal.md)
>
> 关联代码:`internal/db`、`internal/dao`、`internal/session`、`internal/agent`

## 1. 背景与问题

### 1.1 当前写入模型

MothX 的所有 canonical 会话数据写入同一个 `sessions.db`(每个 session 目录一个文件)。写入是**里程碑级**的,不是流式的:

- 流式 delta(`EventTextDelta`/`EventThinkDelta`)只做内存 fan-out(SSE/WebSocket/TUI)与内存 facts 累积,从不落库(`internal/serve/openaiapi/run_executor.go`、`internal/agentruntime/execution_observation.go` 的 delta 分支)。
- 落库发生在离散里程碑上。一次 agent 迭代(含 N 个工具调用)的稳态写入清单:

| 写入点 | 表 | 事务数/迭代 | 代码位置 |
|---|---|---|---|
| assistant 消息 entry | `entries` | 1 | `internal/agent/agent.go` 的 `AppendMessage(assistantMsg)` |
| usage 统计 | `request_stats` | 1 | `RecordUsageFromProviderUsage` |
| 每条工具结果 entry | `entries` | **N** | `internal/agent/agent.go` 的 `AppendMessage(result)` 循环 |
| run 行 usage/context 更新 | `session_runs` | 1 | `ExecutionRuntime.RecordUsage` → `UpdateSessionRunUsage` |
| 重试进度(仅重试时) | `session_runs` | 0~1 | `persistRetryProgress` → `UpdateSessionRunProgress` |
| 工具进度事件(channel/Responses background 路径) | `session_run_events` | 每工具 2 | `persistResponsesBackgroundToolProgress`(`tool_progress` started/ended) |
| 租约心跳(后台,与迭代无关) | `session_runtime_leases` | 每租约 1 tx / 3s | `leaseHeartbeat` → `Renew` |

admission(intent + run + started event + turn + lease binding)与 terminal(run update + assistant entry + finished event + turn end + delivery plan)已经是单事务原子提交(`CreateSessionRunAndEvent`、`FinishSessionRunAndConversationTurn`),不需要也不应该再拆。

### 1.2 压力的真实来源

1. **`synchronous(FULL)` 下每次提交都 fsync,且 fsync 在持有 WAL 写锁期间执行**(`internal/db/db.go` DSN)。这使单次提交的锁持有时间达到 fsync 级(消费级 SSD 约 0.5~5ms;慢盘、网络盘、Windows+AV 场景可达 10ms+)。
2. **跨进程共享写锁。** 同一 session 目录可能同时被多个进程打开:`mothx serve`(WebUI/channels/cron)、desktop spawn 的 `mothx acp` 子进程(`desktop/main/acp-client.ts`,vendored runtime,版本可能与全局 CLI 不一致)、TUI、CLI one-shot(`run`/`stats`/`doctor`/`knowledge-mcp`)。WAL 同一时刻只允许一个写者,`_txlock=immediate` 让每个写事务在 `BEGIN IMMEDIATE` 就排队。
3. **进程内单连接串行。** `SetMaxOpenConns(1)`(`internal/db/db.go`)使进程内所有写天然串行——不存在进程内 SQLITE_BUSY,但排队延迟随并发会话数增长;租约心跳是每个活跃租约一个独立 goroutine、一个独立事务(`internal/session/runtime_lock.go` 的 `go leaseHeartbeat(lease)`),serve 并发 N 个会话即恒定 N tx/3s 的后台写源。

这三点叠加的后果已有文档记录:`docs/zh/changelog.md`「SQLite:瞬时 busy 的事务开启会重试」条目与 `internal/db/busy.go` 注释描述了“多个进程共享会话目录、各自在 `synchronous(FULL)` 下持续提交时,某次 begin 的等待可能超过 busy_timeout(10s),让健康的数据库直接报 `database is locked (5)`”。当前的缓解是 90 秒预算的 begin 退避重试——它把硬失败变成了延迟,但没有消除 fsync 排队本身。

### 1.3 关键前提:多进程写入的既有语义(已查证)

- **同一 session 的内容写入永远是单进程的。** `validateRuntimeLeaseTx`(`internal/session/runtime_lock.go`)在租约行存在时拒绝非 owner 进程写入(`ErrRuntimeLeaseLost`),仅在无租约行时放行冷写入(标题、标签等)。因此不存在“两个进程并发写同一会话 entry”的竞争;跨进程冲突只发生在**不同 session 共享一个 DB 文件**的写锁获取层面。
- 无 `ATTACH`、无跨库事务;单文件单写锁,不存在锁顺序死锁。知识库派生库是独立文件、独立连接,不与 `sessions.db` 共享事务。
- cron 空闲轮询只读;`ClaimDue` 仅在任务到期时写(`internal/dao/cron.go`),不是稳态写源。
- SQLite 本身已经提供两层“单写者”保证:WAL 写锁(物理层,一次一个写者)与 `session_runtime_leases` 的 epoch fencing(逻辑层,每 session 一个 owner)。UDP `SessionLeaseBus` 仅是 advisory 唤醒,不裁决(见关联方案 §3.1、§8)。

### 1.4 已考虑并否决的替代方案

| 备选 | 否决理由 |
|---|---|
| 会话内容拆 JSONL 文件 | entry 写入与租约围栏、leaf 乐观检查在同一事务(`writeEntry`);admission/terminal 是多表单事务;`entries.data` 被跨会话 SQL 查询(列表计数、预览、`LIKE` 搜索、seq 分页、fork 边界)依赖;项目历史上刚从 JSONL 迁移到 SQLite-only(changelog:“移除旧版 JSONL 加载/写入路径”);JSONL 追加同样需要 fsync,单条 entry 反而可能从 1 次 fsync 变 2 次。违反 “Canonical persistence boundary” 与 “duplicate durable records” 架构规则。 |
| UDP 通道保证单写者 | UDP 丢包/乱序/无认证,不能承担裁决;关联方案明文禁止:“不得复用 UDP 广播充当命令通道”“任何状态写入都不得接受 UDP 报文携带的 owner/token/epoch 作为执行凭据”。且单写者已由 SQLite 写锁 + 租约 fencing 保证,层次恰好是“越不可靠的通道,权力越小”。 |
| 稳定写者进程(DB 代理 + group commit) | 选举必须落 SQLite 租约、通道必须 Unix socket;等于把 DAO 表面全部 RPC 化,需处理代理超时、幂等重试、failover 期间写可用性;TUI/CLI/ACP 形态没有常驻守护进程,写者生命周期无解。其收益(fsync 合并)由阶段 1+2 以近零成本获得。仅当 `synchronous=FULL` 是不可妥协的持久性要求且实测吞吐仍不足时重新立项。 |
| 每 session 独立 DB 文件分片 | 破坏跨会话列表/搜索/admission 与“active Run、lease、SQLite 当前时间来自同一只读事务快照”的恢复裁决;知识库 per-ID DB 的先例仅适用于私有、可重建的派生存储。 |
| `wal_autocheckpoint` 调优 | 暂缓:NORMAL 下 fsync 集中到 checkpoint,先由阶段 0 数据决定是否调整阈值。 |

## 2. 目标与非目标

### 2.1 目标

1. 消除“每次提交持锁 fsync”这一跨进程排队的放大器,把 commit 成本降到 page-cache 级。
2. 减少稳态写事务数量:工具密集迭代从 N+3 个事务降到约 3 个;心跳从每租约每 3 秒一个事务降到每进程每库每 3 秒一个事务。
3. 全程保持多进程正确性不变量:谁允许写、如何裁决、如何恢复,一律不动;只改变“每次写多贵 × 写多少次”。
4. 建立可复跑的度量基线与多进程压测矩阵,让每个阶段的收益和回归可验证。

### 2.2 非目标

1. 不改 schema、不改任何行的格式与含义;不新增表、不删表。
2. 不改 `leaseTTL=15s`、心跳 3s、`runtimeHeartbeatRetry=12s`、busy_timeout 10s、begin 重试预算 90s 等常数——关联方案 §3.2 的有界恢复保证(普通本地 orphan 从心跳停止到终态 ≤30s)依赖它们。
3. 不给 UDP 总线增加任何裁决、命令或凭据职责。
4. 不引入 JSONL、sidecar blob、写代理守护进程或 per-session DB 分片。
5. 不动 admission/terminal 的既有原子事务结构。
6. 不改 `settings.json`/`serve.json` schema(阶段 1 的逃生门用环境变量,先例:`MOTHX_RUNTIME_BUS_PORT`)。

## 3. 设计原则与不变量

**核心不变量(所有阶段共同遵守):**

1. **裁决模型不变。** lease/epoch/token fencing、`BEGIN IMMEDIATE`、busy 重试、孤儿恢复协调器、`ErrRuntimeLeaseLost`/`ErrSessionModified` 语义原样保留。任何阶段不得以“减少跨进程冲突”为名放宽它们。
2. **持久化边界不变。** 所有改动收敛在 `internal/db`、`internal/dao`、`internal/session`、`internal/agent`;SQL 仍全部在 `internal/dao`;适配器零改动;收尾必须通过 `go test ./internal/architecture`。
3. **同 session 单写者不变。** entry 批量写入仍在租约围栏与 leaf 检查的同一事务内完成。
4. **恢复时序不变。** 心跳合并不改变“续期失败预算耗尽 → 标 lost → ExecutionRuntime 取消 agent”的链条与时间上限。
5. **事件/回放语义不变。** entries 的 seq 单调、parent 链、类型词汇表、run 事件流顺序与终态语义不因合批改变。

## 4. 阶段 0:度量基线与多进程压测矩阵(前置)

没有基线就无法验证收益,也无法发现回归。本阶段只加观测,不改行为。

### 4.1 指标

1. **busy 重试**:命中次数、累计等待时间、按进程实例打标签(已落地:`internal/db/busy.go` 的 atomic 计数器 + `BusyRetryStats()` 快照,经 `internal/db/stats.go` 发布到 expvar,`--debug` pprof 服务器的 `/debug/vars` 提供;按进程标签即每进程暴露自己的端点)。
2. **begin 等待分布**:p50/p99/max,按进程打标签——这是跨进程排队的直接度量(部分落地:`BeginWaitStats()` 提供次数/累计/单次最大等待;直方图分位数待基线数据证明需要后再加)。
3. **事务计数与 commit 延迟**:`RunInTx` 计时,按调用方标签分类(entry/run/lease/stats/event)。
4. **WAL 大小与 checkpoint**:`PRAGMA wal_checkpoint(PASSIVE)` 读数与频率(`CloseAll` 已执行该语句,补上报)。
5. **围栏健康**:owner 进程的 `ErrRuntimeLeaseLost` 计数(应恒为 0,出现即围栏被破坏)。

### 4.2 压测矩阵(真实多进程形状)

| 形状 | 构成 | 验证目标 |
|---|---|---|
| A | serve(N 并发 run)+ TUI(1 run)同目录 | 跨进程 busy/begin 等待基线;三阶段后对比 |
| B | serve + `mothx acp` 子进程 + 周期性 CLI one-shot(stats/doctor) | desktop 形状;**故意混布 synchronous 设置**(一个进程 `MOTHX_SQLITE_SYNCHRONOUS=FULL`)验证混布安全 |
| C | 单 serve 进程 N 会话 + 多 agent 团队 | 进程内并发;验证阶段 2/3 的合批收益 |
| D | 双进程同 session 提交竞争 + 负载下 `kill -9` owner | 一个 winner 一个 `session_busy`;恢复收敛仍 ≤30s——证明围栏与恢复时序不受三阶段影响 |

实现方式:扩展 `internal/session/sqlite_robustness_test.go` 已有的 subprocess-helper 模式;每个形状跑 60s,输出 §4.1 指标。三个阶段各合入后重跑同一矩阵。

> 落地记录(2026-09-14):形状 A(多进程写不同会话 + 指标报告)、形状 B(一个 helper 设 `MOTHX_SQLITE_SYNCHRONOUS=FULL` 的混布双进程)、形状 C(单进程多会话多租约,验证单调度器与同拍批量续期)实现于 `internal/session/write_pressure_test.go`,默认轻量、`MOTHX_WRITE_PRESSURE_SCALE` 放大负载供基线对比(60s 持续负载由该倍数近似)。形状 D 由既有套件覆盖,不重复建设:`internal/agentruntime/process_integration_test.go`(双进程 admission 竞争,一个 winner 一个 duplicate/busy)、`TestSQLiteTwoProcessesCompetingForSameSession`、`TestRuntimeLeaseSurvivesProcessFailureUntilExpiry`(kill -9 + 过期)、`TestLeaseHeartbeatSchedulerBatchRenewDisplaceAndRetire`(顶替只 lost 自己 + 调度器退休)。

### 4.3 验收

- 指标可采集、可对比;形状 D 在当前代码上通过(作为不变量的回归基线)。

## 5. 阶段 1:`synchronous(FULL)` → `NORMAL`

### 5.1 改动

1. `internal/db/db.go` 的 `dsnForOS`:`q.Add("_pragma", "synchronous(FULL)")` → `synchronous(NORMAL)`;更新注释写明持久性语义与本节风险分析。
2. 环境变量逃生门:`MOTHX_SQLITE_SYNCHRONOUS=FULL` 恢复旧行为,默认 `NORMAL`(逐进程生效;不动 settings schema)。
3. 测试更新:`internal/session/session_test.go` 的 `PRAGMA synchronous` 断言 `!= 2` → `!= 1`,并补 env 覆盖回 FULL 的用例。
4. `wal_autocheckpoint` 暂不动,由阶段 0 的 WAL 指标决定是否调整。

### 5.2 语义与风险评估(WAL + NORMAL)

- **进程崩溃(kill -9):零丢失。** 已提交数据在 WAL/OS page cache 中,文件完好,重开即恢复。现有 `crash-transaction` helper 场景不变。
- **OS 崩溃/断电:** 可能丢最近一次 checkpoint 之后的提交(秒级窗口);数据库不损坏,自动回滚到一致点。
- **丢失窗口落在既有恢复语义内:** run 终态未落盘 → lease 过期 → `orphaned` → recovery coordinator 在关联方案 §3.2 的 30s 上限内终结为 `failed/owner_lost`;entry 丢失 → 会话回放退到较早一致点,与“终态持久化失败保留为可观察、可重试状态”的既有模型同类。知识库派生库可重建,无额外风险。
- **混布安全(desktop vendored runtime 版本偏斜):** `synchronous` 是 per-connection pragma。FULL 旧进程与 NORMAL 新进程并存时,各自的持久性语义只影响自己的提交;WAL 共享内存索引保证跨进程可见性不变;无磁盘格式变化。

### 5.3 为什么这是多进程问题的直接修复

WAL 模式下 FULL 的 commit fsync 发生在持有写锁期间——这正是“其他进程持续提交导致本进程 begin 超过 busy_timeout”的机制。NORMAL 把 fsync 从“每次提交”移到“checkpoint 时”,**所有进程的写锁持有时间同时收缩到亚毫秒级**,跨进程排队基本消失;90s begin 重试退化为纯保险。

### 5.4 验证

- 阶段 0 形状 A/B 对比:commit p99 与 begin 等待分布数量级下降;busy 重试命中趋零。
- kill -9 用例:提交后立即杀进程,重开库数据完整(FULL/NORMAL 语义一致的部分)。
- `go test ./internal/db ./internal/session`;混布形状 B 通过。

## 6. 阶段 2:事务合批——agent 每轮工具结果一次落盘

### 6.1 改动

1. **DAO**:实现未新增方法——批量事务在同一 `dao.Tx` 内循环复用现有 `InsertEntry` 语句(与 `FinishSessionRunAndConversationTurn` 的事务内多次 DAO 调用同构),不引入新 SQL 形态。
2. **Session 域**(`internal/session/session.go`):新增 `func (m *Manager) AppendMessages(msgs []provider.Message) ([]string, error)`,单锁单事务:`ensureInitializedLocked` → 权限/句柄检查(沿用 `writeEntry` 现有前置)→ `validateRuntimeLeaseTx`(一次)→ `CurrentLeaf`(一次)→ 内存构造 entry 链(第 i 条 parent = 第 i−1 条 id,首条 parent = 当前 leaf)→ 同事务循环 `InsertEntry` → commit → 成功后统一更新 `m.entries`/`m.leafID`。与 `AppendMessage` 共用 locked helper,避免逻辑分叉;空切片直接返回;`m.entriesTable()` 保证主表/`sub_entries` 同路径复用。
3. **Agent 循环**(`internal/agent/agent.go`):工具结果循环改为一次 `AppendMessages(toolResults)`,按返回 id 切片逐个 `setMessageID`;失败沿用现有 `session_save` → `TaskFailed` 分支(整批回滚,与今天单条失败即中止语义一致)。

### 6.2 保持的不变量

- **assistant 消息仍在工具执行之前独立落盘**(崩溃时 tool_calls 有记录,副作用可归因)——不参与合批。
- 租约围栏 + leaf 乐观检查仍与写入同一事务;`BEGIN IMMEDIATE` 持写锁后,事务内单次 `CurrentLeaf` 与逐条检查等价。
- entries 的 seq 单事务内 AUTOINCREMENT 递增、parent 链正确、回放顺序不变。
- `deferAssistantEntry`(RuntimeOwnsTurnEnd)终态合批路径不受影响;`FinishSessionRunAndConversationTurn` 已证明“一个事务串多条 entry + run + event + turn”模式可行(`appendTurnAssistantMessageTx`/`appendTurnEntryTx` 先例)。
- 警告消息注入(连续无文本)是低频条件路径,保持单条 `AppendMessage`。

### 6.3 多进程分析

- 同 session 并发写已被围栏排除,合批不改变任何跨进程竞争语义;竞争面仍是不同 session/进程之间的锁获取。
- 权衡:单次持锁时间变长(一批 insert),但总持锁时间下降(省掉 N−1 次 begin/commit 开销;若 FULL 未退场,还省 N−1 次持锁 fsync——**阶段 2 在阶段 1 之前也独立成立**)。
- 护栏 1:**批大小上限**。一轮工具结果本身有界(工具输出有截断),另加每事务 K 条(初始 K=64)分批提交的兜底,超过即拆多事务。
- 护栏 2:压测加 worst-case 用例(并行多个大 payload 工具结果),量化最长持锁时间并纳入验收阈值。

### 6.4 测试与验证

- `session_test`:`AppendMessages` 链正确性(parent/seq/顺序)、leaf 冲突返回 `ErrSessionModified`、租约失效返回 `ErrRuntimeLeaseLost`、批原子性(中途失败全回滚)、K 上限分批、sub-agent 表路径。
- `agent_test`:N 工具一轮全部落盘且顺序正确;批量写失败 → run 以 `session_save` 失败终结。
- 事务计数:用阶段 0 计数器断言一轮从 N+3 降到 ~3。
- `go test ./internal/architecture`(触碰持久化实现,验证不越界)。

## 7. 阶段 3:心跳写合并(每进程每库一个事务)

### 7.1 改动

1. **DAO**(`internal/dao/runtime_lease.go`):新增 `RenewBatch(ctx, tx, records []RuntimeLeaseRecord, ttl int64) (map[string]int64, error)`——单事务内逐条执行与现有 `Renew` 完全相同的 fenced UPDATE(owner/epoch/token CAS),返回每 session 受影响行数。
2. **心跳调度**(`internal/session/runtime_lock.go`):改为每 sessionDir 一个进程级调度器(singleton):3s ticker 从现有 `activeRuntimeLeases` 注册表快照该库的活跃租约,一个事务批量续期。移除 per-lease goroutine;`lease.stop`/`release`/`lost` 通道语义不变。
3. **结果处理(逐租约判定)**:`affected==0` → 仅该租约 `markRuntimeLeaseLost`(已 released 的租约由现有 released 标志短路,天然安全);事务级失败 → 整批在现有 `runtimeHeartbeatRetry`(12s)预算内退避重试,预算耗尽全部标 lost——与今天各租约独立耗尽预算的结果一致。
4. 新租约注册后由下一拍接管,首拍延迟 ≤3s;acquire 已写 `expires=now+15s`,安全余量不变。

### 7.2 最坏时间线等价性论证

今天:每租约独立经历“begin 最多阻塞 busy_timeout(10s)+ 12s 重试预算”,阻塞互相独立。合并后:一次 begin 等待覆盖全部租约(批内 M 条 fenced UPDATE 亚毫秒级),**busy 暴露面更小**(M 次 begin 排队 → 1 次)。唯一新风险是“一次 begin 阻塞延迟本进程所有租约的续期”,但阻塞点只在 begin,且 12s 预算 < 15s TTL 的余量与逐租约时代相同。关联方案 §3.2 的 30s 恢复上限不变。

### 7.3 明确不做

- **跨进程心跳合并**:不可能也不应该——每个进程必须自证存活,下限 = 进程数 P × 1 tx/3s(典型 P=2~4,稳态约 1 tx/s,阶段 1 之后可忽略)。再合并需要写代理,已在 §1.4 否决。
- **usage/progress 写合并**:`UpdateSessionRunUsage` 每 provider 迭代仅 1 次,progress 只在重试时写,已是里程碑级;且 `usage_json` 被 runtime snapshot 读取,节流会引入投影新鲜度问题。等阶段 0 数据说话,默认不做。
- **`tool_progress` run 事件合并**:是 channel/background 回放的合法投影记录,保持现状。

### 7.4 测试与验证

- `runtime_lock_test`:多租约合并为单事务;单租约 `affected=0` 只 lost 自己;批失败整批重试、预算耗尽全 lost;released 租约不被复活。
- 跨进程:形状 D 扩展——A 进程持 2 租约被 `kill -9` 后,B 的恢复收敛仍 ≤30s(合并未改变恢复时序);owner 进程 `ErrRuntimeLeaseLost` 计数恒 0。
- 形状 C 对比:心跳事务数从 N tx/3s 降到 1 tx/3s。

## 8. 实施顺序、兼容性与回滚

- **顺序**:阶段 0 → 阶段 1(改动最小、收益最大、独立回滚)→ 阶段 2 → 阶段 3(并发语义最微妙,最后)。阶段 2 与 3 可并行开发,合入顺序建议 2 先。
- **兼容性**:三阶段均为纯内部实现改动;无 schema/行格式/wire 格式变化;新旧版本进程混布安全(阶段 1 是 per-connection pragma;阶段 2/3 写出的行与今天完全相同)。desktop vendored runtime 与全局 CLI 版本偏斜不需要协同升级。
- **回滚**:任意阶段独立 revert;阶段 1 另有 `MOTHX_SQLITE_SYNCHRONOUS=FULL` 一键回退,无需发版。
- **文档**:每阶段合入后追加双语 changelog(`docs/en/changelog.md`、`docs/zh/changelog.md`);阶段 1 的持久性语义变化需要在用户文档中明确说明(含 env 逃生门)。

## 9. 预期收益(量级估计,以阶段 0 实测为准)

| 阶段 | 机制 | 预期 |
|---|---|---|
| 1 | commit fsync → checkpoint fsync | commit p99 从 fsync 级(慢盘/AV 下 10ms+)降到 <0.1ms;跨进程 busy 重试趋零;changelog 记录的 “begin 超 busy_timeout” 场景基本消失 |
| 2 | N+3 事务/迭代 → ~3 | 写锁获取次数按比例下降;工具密集轮次收益最大 |
| 3 | N tx/3s → 1 tx/3s/进程/库 | 稳态后台写源基本消失;serve 多会话场景收益最大 |

## 10. 验收清单

1. 形状 A~D 压测全部通过,且与阶段 0 基线对比:begin 等待 p99、busy 重试命中、事务/秒均显著下降(阶段 1 后 busy 重试趋零;阶段 2 后迭代事务数 ~3;阶段 3 后心跳 1 tx/3s/进程)。
2. 形状 D 不变量:双进程同 session 竞争仍是一个 winner 一个 `session_busy`;负载下 kill -9 owner 后恢复收敛 ≤30s;owner 进程 `ErrRuntimeLeaseLost` 恒 0。
3. 混布形状 B:FULL 与 NORMAL 进程并存时双方读写、恢复、投影全部正常。
4. `go test ./internal/db ./internal/dao ./internal/session ./internal/agent ./internal/agentruntime ./internal/architecture` 通过;跨包改动跑 `make test`。
5. kill -9 后重开库:已提交数据完整(FULL/NORMAL 一致部分);NORMAL 的断电丢失窗口在用户文档中如实说明。
6. 无任何租约/围栏/恢复常数被修改;无 schema 变化;无 adapter 改动;changelog(双语)已追加。

## 11. 风险与开放问题

1. **NORMAL 的断电丢失窗口**(秒级、已提交事务可能回退):接受;env 逃生门 + 用户文档说明;若未来出现“断电后会话损坏”的真实报告,评估对 terminal/lease 关键事务单独提高持久性的可行性(SQLite 不支持 per-transaction synchronous,届时只能在关键提交后显式 `wal_checkpoint(FULL)`,作为后续开放问题)。
2. **批事务持锁时间上限**:K=64 + 工具输出截断已双重限界;压测 worst-case 用例给出实测值后确认阈值。
3. **心跳合并的批内耦合**:时间线等价性已论证(§7.2),形状 D 回归兜底。
4. **modernc.org/sqlite 驱动行为**:NORMAL pragma 经 `_pragma` 参数下发与 FULL 同机制;阶段 1 用例断言 `PRAGMA synchronous` 实际生效值。
5. **checkpoint 频率与 WAL 增长**:NORMAL 下 fsync 集中到 checkpoint,checkpoint 由越过阈值的提交方执行(现状行为);阶段 0 的 WAL 指标异常时再评估 `wal_autocheckpoint` 调整,作为独立小改动。
