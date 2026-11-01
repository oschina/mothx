# Session 内容存储一致性修复方案

> 状态：提案（P0、P1 已实施，P2 待实施）
>
> 日期：2026-11-01（P0 落地 2026-11-02；P1 落地 2026-11-02）
>
> 实施进展见 §4「P0 实施记录」与 [`docs/notes/20261101-session-content-store-split-brain.md`](../notes/20261101-session-content-store-split-brain.md)。
>
> 现场调查与证据：[`docs/notes/20261101-session-content-store-split-brain.md`](../notes/20261101-session-content-store-split-brain.md)
>
> 关联方案：[Session 控制面与事件存储分离方案](./session-control-plane-event-store-proposal.md)、[SQLite 写压力优化方案](./sqlite-write-pressure-reduction-proposal.md)、[跨进程 Session 执行归属方案](./cross-process-session-execution-ownership-proposal.md)、[统一 Agent 核心与多入口 Runtime 方案](./agent-core-runtime-unification-proposal.md)
>
> 结论一句话：`content_store` 的写入路由必须从「内存标志 / 持久化列 / 物理日志」三处裁判收敛为一处（持久化列为准），并且 legacy payload 行的删除必须从「每次 open 无条件删」改为「有因果证据 + 持写租约才删」；否则单进程也会出现 `session was modified by another process: expected leaf "X", current leaf ""` 的假报错与真实数据丢失。

## 1. 问题陈述

### 1.1 现场症状

任务在**只有一个应用进程在跑该 session** 的情况下仍偶发失败：

```
session was modified by another process: expected leaf "63131f3ccf0d0deb", current leaf "";
reopen the session before writing
```

该文案只由 legacy 写入路径产生（`internal/session/session.go`）：

- `writeEntry` 的乐观锁：`session.go:3429-3440`
- `writeEntries`（`AppendMessages` 批量，工具结果走这条）：`session.go:3505-3515`

两者的判据都是 `entries` 表的最新 payload 行 vs 内存 `m.leafID`。`current leaf ""` 的语义是**该 session 在 `entries` 表里已无 payload 行**，而不是「另一个写者写了更新的一条」（那会是非空且不同的 id）。所以这条错误名不副实：它真正的含义是**「legacy 行被删除，而某个活着的 handle 仍在按 legacy 表写」**。

### 1.2 两个层次的缺陷

**（A）写入路由权威分裂（代码缺陷，单进程可复现）**

| 决定「内容写到哪」的位置 | 依据 | 代码 |
|---|---|---|
| `Manager.AppendMessage/AppendMessages/Append*` | 内存 `m.eventStoreMode`，**从不重读持久化列** | `session.go:1090/1127/1198/1234/1268/1295/1345/1404/1476` |
| run 准入（intent/run 落盘） | 持久化 `content_store` 列 | `sessionUsesEventStore`（`session.go:2905`）调用点：`run_store.go:456`、`execution_intent.go:129`、`projects.go:142`、`session.go:2718/2784/2825/2872` |
| run 终态（assistant/turn_end） | **物理事件日志是否存在** | `event_turn.go:23-135`（`appendEventStoreRunTerminal`，`L28` 以 batches/intents 判定） |

读取/收敛路径恰好是唯一正确的一处——它明确重读持久化列，并在注释里写明了这个隐患（`event_store_append.go:216-227`）：

> *"The persisted content store, not this process's in-memory view, decides where canonical content lives. A Manager opened before another path flipped the session still reports the legacy mode, so re-read the column"*

**（B）legacy payload 行的删除没有风险判定（数据丢失缺陷）**

`convergeFlippedSession`（`event_store_append.go:498-537`）在**任意一次 open**（`loadLocked` → `convergeEventStoreLocked`，`session.go:2058-2084`）里探测并删除该 session 的全部 legacy payload 行：

```go
hasLegacy, _ := projectionDAO.HasLegacyEntries(ctx, db.Bun(), sessionID)   // event_store_append.go:521
if hasLegacy {
    WriteRootDatabase(... projectionDAO.DeleteLegacyEntries(ctx, tx, sessionID))  // :526-527
}
```

`DeleteLegacyEntries` = `DELETE FROM entries WHERE session_id=? AND type<>'session'`（`dao/session_event_projection.go:114`）。删除不检查：删除时是否可能仍有 legacy writer、被删的行是否已被事件日志覆盖。

而当前构建里 open 无处不在：`appendEventStoreUserHandoff`（每次 run 准入，`run_store.go:594-627`，`OpenByIDExact` 在 `:595`）、`appendEventStoreRunTerminal`（每次 run 终态，`event_turn.go:34`）、`LatestSessionBindings` 兜底（`session.go:929`）、列表/详情/preview、ACP/serve 各管理 handler。因此**同一个进程自己这次 run 的准入/终态 open 就会删掉自己 handle 的 legacy 行**，下一次 append 即刻爆出假报错。

### 1.3 三个放大器

1. `CommitEventIntent` 把 `content_store` 改成 `event_store` 只是**提交第一个批次时的副作用**（`event_intents.go:315`），而 `flipSessionContentStore` 只更新**调用它的那一个** Manager 的内存标志（`event_store_append.go:662`），进程内其他 handle 不会被告知。
2. `MigrateToEventStore` 在租约被占/丢失时**静默返回 nil**（`event_store_append.go:106-115`），于是「本次没迁移」被当成成功，legacy 继续服务而没有任何状态记录。
3. **混合 build 没有任何能力门**。旧构建（不含 content store 代码）的写入永远是 legacy；新构建会迁移并删除 legacy 行。现场事故正是旧 TUI 与新 `serve` 并存（见 notes）。Desktop 会捆绑固定版本的 `mothx acp`，因此这不是一次性问题。

### 1.4 后果

- 假报错直接**打断任务**：`internal/agent/agent.go:960-966` 把它当 `session_save` 失败并 `emitRunFinished(TaskFailed, ...)`。
- 更严重的是**静默的 split 状态**：`content_store='event_store'` 但新内容只在 `entries`，replay/fork/search 使用旧事件日志；而新构建下一次 attach 会**优先事件日志并删除较新的 legacy 行**（现场 session `95399bdbf08e4403` 即为此状态）。

## 2. 目标与不变量

修复后必须成立：

1. **一个 resolver**：任何写入（Manager append、run 准入、run 终态、fork/repair）决定「写到哪个 store」时，只读**持久化** `content_store`（可带一次进程内 revision 缓存，但 flip 后必须失效），不得只依赖内存标志或物理日志存在性。
2. **删除必须因果安全**：legacy payload 行只有在「该 session 已翻转 + 事件日志健康 + 不存在未被事件日志覆盖的 legacy 行 + 持有该 session 的 content write lease」时才可删除。删除不再是 open 的副作用（沿用本仓既有原则「读取与修复分离」）。
3. **乐观锁语义正确**：`ErrSessionModified` 只表示「与另一个真实写者的并发追加冲突」。`currentLeaf == ""` 且内存 leaf 非空 → 判为 store 变更，必须自愈（有界 reload + 重判），不得直接判任务失败。
4. **不静默降级**：迁移被 defer 必须是显式状态（记录 + 可诊断），不能以 `nil` 成功掩盖。
5. **混合 build 安全**：不具备 content-store 能力的进程（老构建、旧 Desktop 捆绑）可能仍在写 legacy 时，新构建**不得翻转、不得删除** legacy 行；此时允许「两存储并存」并由显式修复入口收敛。
6. **fail closed 且可操作**：无法安全收敛时给出可操作的错误/诊断（会话级、可定位），而不是误导性的「another process」。
7. **架构不变量不放松**：仍是一个 Agent Core、一个 Runtime、DAO-only SQL；本方案不改 `settings.json`/`serve.json` 语义，不新增第二套 store。

## 3. 设计

### 3.1 单一 resolver：`resolveContentStoreLocked`

新增 `internal/session` 内的唯一判定（不导出到适配器）：

```go
// resolveContentStoreLocked 返回写入应使用的存储，并在持久化列已翻转时采纳提交日志。
// 依据持久化 sessions.content_store（EventStore 模式不再查询，因为会话不会回退）。
func (m *Manager) resolveContentStoreLocked(ctx context.Context) (usesEventStore bool, err error)
func (m *Manager) persistedContentStoreLocked(ctx context.Context) (usesEventStore bool, err error)
```

- 实现要点（P0 已落地）：只有在 handle 仍处于 legacy 模式时才读取该列（一次 PK 读，无额外事务）；命中翻转时调用 `reloadLocked`（`Reload` 的无锁内核）采纳提交日志，条目按采纳后的 leaf 挂父。`session_store_meta` revision 缓存留给 P1（用于给「已 EventStore 模式」的 handle 也提供廉价校验）。
- Manager 侧所有 `Append*` 通过 `appendEntryLocked` 使用它；原 `m.eventStoreMode` 降级为「已知状态提示」，不再作为唯一判据。
- 调用点收敛：`sessionUsesEventStore`（`session.go:2905`）改为对 `dao.SessionDAO.ContentStore` 的薄封装，run 准入/fork/列表等 9 个调用点与写入路径共用同一列读取；`event_turn.go` 的终态判定在 P1 同样改读列（物理日志存在性只用于「事件日志是否健康/可回放」）。
- 守卫（P2）：新增 `internal/architecture` 测试，禁止 `internal/session` 之外出现 content store 判定，并禁止出现新的「只看物理日志决定写入 store」的调用（`eventStoreMode` 直接判定的写入分支需列入 allowlist 并附移除条件）。

### 3.2 叶子冲突语义修复

- legacy CAS 失败时由 `Manager.legacyLeafConflict` 细分（P0 已落地）：
  - `currentLeaf == expectedLeaf` → 正常；
  - 持久化列已是 `event_store` → 内部信号 `errContentStoreFlipped`：`appendEntryLocked`/`AppendMessages` 在**同一个调用内**采纳并重试一次（重试不消耗 Agent iteration budget，也不改变 run 终态）；
  - 列仍是 sqlite 但 `currentLeaf == ""` 且 `expectedLeaf != ""` → transcript 被外部清空（旧构建或人工），返回 `ErrSessionContentStoreChanged`，绝不静默重新生根；
  - 其余（`currentLeaf != ""` 且不同）→ 真并发/他写者，返回 `ErrSessionModified`（保留原文案）。
- `internal/agent` 的 `session_save` 分支保持失败语义，但错误类型可用于区分「store 迁移」与「并发冲突」。
- 事件存储模式侧的叶子检查（`event_store_append.go:736-738`）同样只在真实并发时返回 `ErrSessionModified`。
- 顺带收口（P1）：`serve/run.go:1886` 的错误码映射增加 `session_content_store_changed`/`session_store_split`，避免把两类问题都报成 `session_modified`。

### 3.3 删除 legacy payload 的因果安全条件

新增派生检测（**无需 schema 变更**，利用「导出保留 entry ID」这一事实）：

```sql
-- split probe：已覆盖判定 = legacy 行 ID 是否存在于事件投影
SELECT COUNT(*), MAX(e.timestamp) FROM entries e
WHERE e.session_id = ?
  AND e.type <> 'session'
  AND NOT EXISTS (
        SELECT 1 FROM session_event_projections p
        WHERE p.session_id = e.session_id AND p.id = e.id)
```

- `count == 0` → legacy 行全部已被事件日志覆盖，可安全回收；
- `count > 0` → **split 状态**（存在 flip 之后新写入的 legacy 行），禁止删除。

规则：

1. `convergeFlippedSession`（open 路径）**只读**：探测 + 记录诊断（进程内计数 + doctor 可见），不再执行删除；
2. 删除移到新方法 `RetireLegacyPayloadUnderLease(ctx, sessionID, evidence)`，仅在以下条件同时成立时执行：已翻转、`InspectEventStore` healthy、split probe `count==0`、调用者持有该 session 的 content write lease（`event_storeWriteBinding` 已提供）；
3. 调用者只有两处：迁移导出的尾部（`exportLegacyTranscript`，本身持 lease）与 `MigrateToEventStoreOutcome` 的已收敛分支；legacy append 路径不调用；
4. 删除失败/条件不满足 → 保持两存储并存，不 fail 会话（返回 `ErrLegacyPayloadSplit`，attach 侧记录后继续）；诊断里记录 split（含 count），修复入口见 P1。

### 3.4 增量迁移（legacy delta adoption）替代 reset + 全量重导

现状：`exportLegacyTranscript` 在 `content_store='sqlite'` 时会 `resetEventStoreExportAttempt`（删 intent + `RemoveEventStore`）后整段重导（`event_store_append.go:377-393`、`:280`）。这在 split 状态下会**丢掉 flip 之后的新内容**。

修复：新增增量路径 `Manager.adoptLegacyDeltaLocked` / `Manager.AdoptLegacyContent`（P1 已落地；只由修复入口与租约持有者调用）：

- 取 split probe 返回的 legacy 行（按持久化 parent 链排序，与现有一致）；
- 因为导出**保留 entry ID**，delta 行的 parent 若已被导出，可直接沿用原 parent ID 落到事件日志尾部；parent 在 delta 内的按链内序；若某祖先既不在事件日志也不在 delta（只可能由旧构建的删除造成）→ 判为 `session_store_broken`，fail closed 并保留全部 legacy 行；
- 按 `maxEventStoreEntriesPerBatch` 分片，直接 `AppendPreparedEventBatch` + `CommitEventIntent`（不 reset），最后翻译 `conversation_turns` 边界（复用 `translateConversationTurnBoundaries`）；
- `RemoveEventStore` 只允许保留在「`content_store='sqlite'` 且无 committed intent」的原始导出路径上，并在注释里写明理由。
- 入口：新增 `mothx doctor --repair-session-content[=all|<sessionID>]`（或复用现有 repair 入口，需与 session/agentruntime 现有一致性修复对齐），持 lease、可重复、幂等；禁止在 serve/TUI 启动路径里自动跑，避免再次制造隐式破坏。

### 3.5 迁移入口的显式 defer 状态

`MigrateToEventStore` 改为返回显式结果（保持单一入口，不改调用路径）：

```go
type MigrationOutcome int
const (
    MigrationAdopted  MigrationOutcome = iota // 已采用 EventStore
    MigrationDeferred                          // 有 owner 在跑/lease 丢失：本次无事可做
    MigrationNotNeeded                         // 已收敛
)
func (m *Manager) MigrateToEventStore(ctx context.Context) (MigrationOutcome, error)
```

- `MigrationDeferred` 必须记录（session 级诊断 + `session_store_meta` 计数），`BindSession`/`AttachSessionResources`/`Builder.Build` 三条装配路径（`agentruntime/session_runtime.go:374`、`attach.go:65`、`:1087`）保持「不阻塞 attach」，但不再把 defer 当成功；
- 由于 3.1 的 resolver 会在每次写前重读列，defer 后的写入自然留在 legacy，不再产生「准入走 A、写入走 B」的分裂。

### 3.6 混合 build 防护（修订：从"能力门"改为"延迟回收 + 覆盖判定"）

原计划是"进程能力登记 + 未声明能力则不翻转"。实现阶段确认该门**不可判定**：旧构建无法声明任何能力，因此"没有任何旧构建存活"无法由新构建观测。任何"翻转/删除安全"的证明都缺少一个必要事实。与其做一个看起来严格、实际只覆盖"已声明实例"的门，改为三条可验证的机制（P1 已落地）：

1. **翻转时间 + 冷静期**：`sessions.content_store_flipped_at`（migration 54）记录首次翻转时间；`retireLegacyPayload` 在冷静期（`legacyPayloadRetireGrace`，默认 10 分钟）内拒绝删除已覆盖行，返回 `ErrLegacyPayloadRetireGrace`（attach 侧记录后继续）。理由是可观测的：仍在写 legacy 的进程会持续追加，冷静期内的新行必然未覆盖 → 后续回收整体拒绝；本仓现场事故（翻转 11:35、旧 TUI 11:36 起持续追加、12:37 删除）在冷静期+覆盖判定下不会被删除。
2. **覆盖判定是唯一删除条件**：`DeleteCoveredLegacyEntries` 在 DELETE 内部校验投影覆盖（P0），不是先探测后删除；因此"冷静期结束后旧写者才追加"的那一行也永远删不到内容。
3. **显式修复**：split 由 `mothx doctor` 报告、`mothx doctor --repair-session-content` 增量采纳（§3.4），并要求操作者先退出所有 MothX 进程；修复属人工动作，不走启动路径自动执行。

已知残余（不可由代码消除）：旧构建在冷静期结束后**才开始**写 legacy 的会话会再次出现"预期 leaf X、当前 leaf ''"；它是旧构建自身的观测错误，重启到新构建即结束，且期间写入的内容不会被删除（会形成 split 并被采纳）。因此发布说明与 doctor 文案统一要求：**升级后重启每一个 MothX 进程**。

### 3.7 可诊断性

- `mothx doctor` 增加只读检查 `sessions.content_store`（P1 已落地，`internal/doctor/doctor.go` `checkSessionContentStore`）：输出 split 会话数、未覆盖 legacy 行数与最多 5 个会话 ID，并给出"退出所有进程 → 备份 → `--repair-session-content all`"的动作；
- `session_store_meta` 记录 `content_store_migration_adopted` / `content_store_migration_deferred` / `content_store_legacy_rows_retired`（P1 已落地；只由写路径 +1，open 保持零写事务）。split 计数保持进程内 `SessionStoreSplitDetections`，因为 open 不允许写库；
- 错误文案：把「reopen the session before writing」替换为可操作说明（session id、store 变更方向、是否 split、建议的修复命令），并保留 `errors.Is` 判定用的 sentinel。

## 4. 实施阶段

### P0 — 正确性（可独立发布，向后兼容，无 schema 破坏）

1. `3.1` 单一 resolver（Manager 写入 + run 准入/终态统一）。
2. `3.2` 叶子冲突细分 + 有界自愈 + typed error + 文案/错误码。
3. `3.3` open 只读化：删除改为 `RetireLegacyPayloadUnderLease`，条件含 split probe。
4. `3.5` `MigrateToEventStore` 显式 outcome + 记录。

验收：现场两个 Manager 场景（一个 flip、一个 append）不再产生 `ErrSessionModified`，且事件日志不丢内容、legacy 行不被误删。

#### P0 实施记录（2026-11-02）

已落地，`go build ./...` 与 `internal/{session,dao,architecture,agentruntime,agent,acp,cron,serve/...}` 全绿。

- **单一 resolver**：新增 `Manager.resolveContentStoreLocked` / `persistedContentStoreLocked`（`internal/session/session.go`）。每次 legacy 写前以持久化 `sessions.content_store` 为准；已处于 EventStore 模式的 handle 不再额外查询（会话不会回退到 legacy）。命中翻转时用新的 `reloadLocked`（`Reload` 的无锁内核）采纳提交日志，条目按采纳后的 leaf 重新挂父。
- **写入路径统一**：新增 `appendEntryLocked`，`AppendMessage`/`AppendModelChange`/`AppendModeChange`/`AppendThinkingLevelChange`/`AppendAdditionalDirectories`/`AppendCompaction`/`AppendContentOverride`/`AppendSessionTitle` 改为「工厂函数 + resolver + 一次自愈重试」；`AppendMessages` 拆成 `appendEventStoreMessagesLocked`/`appendLegacyMessageChunksLocked`，仅在**首个分片未提交**时自愈（避免跨存储重放已提交分片）。
- **叶子冲突语义**：`writeEntry`/`writeEntries` 的 mismatch 交给 `Manager.legacyLeafConflict`：列已是 `event_store` → 内部信号 `errContentStoreFlipped`（采纳后重试一次）；列仍是 sqlite 但 transcript 被外部清空 → 新的 `ErrSessionContentStoreChanged`；其余才是 `ErrSessionModified`。原文案只保留给真正的并发冲突。
- **open 只读化**：`convergeFlippedSession` 不再删除 legacy payload，改为 `reportLegacyPayloadStateLocked`（只读探测 + `recordSplitStore` 诊断，进程级计数 `SessionStoreSplitDetections`，每会话每进程只记一次日志）。
- **围栏回收**：新增 `retireLegacyPayload`（`Manager` 包装）/`retireLegacyPayloadRows`（无锁内核），要求 content write lease（复用 `eventStoreWriteBinding`，优先使用已持有的执行租约）并在 **DELETE 语句内部**重新校验覆盖关系（`dao.DeleteCoveredLegacyEntries`）；调用点只有迁移导出尾部与 `MigrateToEventStoreOutcome` 的已收敛分支。发现未覆盖行时返回 `ErrLegacyPayloadSplit` 且**一行不删**。
- **DAO**：`SessionDAO.ContentStore`（唯一列读取）、`SessionEventProjectionDAO.UncoveredLegacyEntryCount`（split probe，利用「导出保留 entry ID」）、`DeleteCoveredLegacyEntries`（原子覆盖过滤）。`DeleteLegacyEntries` 仅保留给复现旧构建破坏行为的契约测试。
- **显式 defer**：`MigrateToEventStore` 保留原签名（3 条 attach 路径与既有调用不变），新增 `MigrateToEventStoreOutcome` 返回 `MigrationNotNeeded/MigrationAdopted/MigrationDeferred`，并在 defer 时记录日志；`MigrateToEventStore` 变为薄包装。
- **行为变更（有意）**：`TestOpenConvergesFlippedSessionWithoutBind` 与 `TestOpenRemovesLegacyDuplicateAndRepairsTurnBoundsInOneScan` 由「open 删除副本」改为「open 保留副本 + attach 回收」——这是本方案 §3.3 的核心取舍，父方案 `session-control-plane-event-store-proposal.md` 的进展记录已同步。
- **新增回归**：`content_store_routing_test.go`（stale handle 采纳、批量采纳、外部清空 transcript 的 typed error、split 保留与回收拒绝、覆盖过滤语句）与 `TestMigrateToEventStoreDefersWhileAnotherOwnerRuns` 的 outcome 断言。

遗留（P1/P2）：增量采纳（`adoptLegacyDelta`）与 `doctor --repair-session-content`、混合 build 能力门、`doctor` 检查与 `session_store_meta` 计数、架构守卫（resolver 单点 / open 不写 DB）、changelog。

### P1 — 数据修复与混合 build 门

5. `3.4` 增量迁移 + `doctor --repair-session-content`。
6. `3.6` 能力门与持久化门槛。
7. `3.7` doctor 检查 + `session_store_meta` 计数。

验收：`95399...` 类会话可由修复入口收敛为「事件日志包含全部历史」；老构建在场时不发生 flip/删除。

### P2 — 收口与守卫

8. 移除 `convergeFlippedSession` 内残留的删除调用与相关 allowlist 条目；更新父方案（`session-control-plane-event-store-proposal.md`）的进展记录。
9. 架构守卫：resolver 单点 + 禁止 open 写 DB + 禁止「物理日志决定写入 store」。
10. 文档与 changelog（`docs/en|zh/changelog.md`，含本会话事故的恢复说明与 `doctor` 用法）。

#### P1 实施记录（2026-11-02）

已落地，`go build ./...`、`go vet`、`gofmt` 干净；`internal/{session,dao,architecture,agentruntime,agent,acp,cron,tui,doctor,db,serve/...}` 与 `cmd/mothx` 全绿。

- **增量采纳（§3.4）**：`internal/session/content_store_adoption.go`
  - `Manager.AdoptLegacyContent(ctx)` / `AdoptLegacyContentByID(ctx, sessionDir, sessionID)`：读持久化列确认已翻转 → 用 `orderedSessionEntries` 的父链拓扑序取"未覆盖"子集（`dao.UncoveredLegacyEntryIDs`）→ 逐行校验父引用（覆盖集合或 delta 内，否则 `ErrSessionStoreBroken` 且一行不删）→ 在 content lease 下按 `maxEventStoreEntriesPerBatch` 分片 `AppendPreparedEventBatch` + `CommitEventIntent`（**不 reset**，追加到日志尾部）→ `repairUnmappedTurnBoundaries` 按 marker 身份把 turn 边界翻译到新序列 → `retireLegacyPayloadRows(..., force=true)` 收尾。
  - 幂等：崩溃后重跑时已提交的行已"覆盖"，自动从剩余 delta 继续；`Deferred`（他 owner 占用）不改变任何数据。
  - 与导出路径的分工：`exportLegacyTranscript` 只用于未翻转会话（可 reset）；已翻转会话只走增量采纳，绝不 `RemoveEventStore`。
- **修复入口（§3.4/§3.7）**：`internal/doctor/doctor.go` 新增 `RepairSessionContent`（`all` 或单 session，单次 5 分钟上限，逐会话记录 adopted/retired/deferred/error，互不阻断）；`cmd/mothx/main_doctor.go` 新增 `--repair-session-content`，`--json` 输出新增可选 `repairs` 字段；文本输出逐条打印 `[ok]/[skip]/[error]`。
- **混合 build 防护（§3.6 修订）**：migration 54 `add_content_store_flip_time` 新增 `sessions.content_store_flipped_at`；`flipSessionContentStore` 通过 `SetContentStoreFlipped` / `FlipContentStoreOnLease(..., now)` 只在首次翻转时记录时间；`retireLegacyPayloadRows` 在冷静期内返回 `ErrLegacyPayloadRetireGrace`（attach/导出侧记录后继续，修复入口 `force=true` 跳过）。
- **可诊断性（§3.7）**：doctor 只读检查 `sessions.content_store`（split 列表来自 `dao.SplitContentStoreSessions`，按未覆盖行数排序）；`session_store_meta` 新增 `content_store_migration_adopted/deferred/legacy_rows_retired` 计数（写路径 +1，open 零写）。
- **现场修复中发现并修复的诊断缺陷**：`dao.SplitContentStoreSessions` 原来同时使用 `Table("entries")` 与 `TableExpr("entries AS e")`，bun 会渲染成 `FROM entries, entries AS e` 的笛卡尔积，doctor 把 224 行报成 52,864 行。改为只声明 `TableExpr("entries AS e")`，并新增 `TestListSplitContentStoreSessionsReportsExactCounts`（同库两个 split 会话的精确计数）固定该查询。
- **测试**：`content_store_adoption_test.go`（收敛/拒绝缺失祖先/他 owner defer/边界翻译/精确计数）、`content_store_grace_test.go`（冷静期与计数）、`internal/doctor/session_content_repair_test.go`（检查 → 修复 → 幂等 → `RunWithOptions` 先修后检）；既有 attach 测试改为两阶段（attach 迁移 → 冷静期后 attach 回收），`TestReloadConvergesFlippedSessionWithoutDeadlock` 改为断言"load 不删除"。

遗留（P2）：架构守卫（resolver 单点 / open 不写 DB / 禁止物理日志决定写入）、`changelog` 与发布说明（含"升级后重启每个进程"）、清理 allowlist 与旧注释。

## 5. 测试与验收

| 用例 | 断言 |
|---|---|
| `TestStaleLegacyHandleAdoptsEventStoreAfterFlip`（已实现） | 两个 Manager 打开同一 legacy session；A 触发迁移导出；B 继续 `AppendMessage`：**不返回** `ErrSessionModified`，内容进入事件日志，replay 包含两条消息且只出现一次 |
| `TestAppendMessagesAdoptsEventStoreAfterFlip`（已实现） | 批量（工具结果）路径同样自愈，事件日志 replay 完整 |
| `TestAppendReportsRetiredLegacyTranscript`（已实现） | 列仍为 sqlite 但 transcript 被外部清空 → `ErrSessionContentStoreChanged`，且**不是** `ErrSessionModified`（不静默重新生根） |
| `TestOpenPreservesSplitLegacyRowsAndRetireRefuses`（已实现） | 已翻转 session 植入 flip 后 legacy 行 → open/attach 均不减少行数、`SessionStoreSplitDetections` 增加、`retireLegacyPayloadRows` 返回 `ErrLegacyPayloadSplit` |
| `TestDeleteCoveredLegacyEntriesSparesUncoveredRows`（已实现） | DELETE 内部覆盖过滤：只删已覆盖行，未覆盖行保留 |
| `TestMigrateToEventStoreDefersWhileAnotherOwnerRuns`（已扩展） | 他 owner 持 lease 时返回 `MigrationDeferred`（非错误），attach 成功且 legacy 继续自洽 |
| `TestAdoptLegacyContentReconcilesSplitStore` / `...RepairsTurnBounds` / `...RejectsMissingAncestor` / `...DefersWhileAnotherOwnerRuns`（已实现） | delta 行 ID 保留、parent 链正确、`conversation_turns` 边界翻译后 fork 可解析；祖先缺失 → fail closed 且 legacy 行完整 |
| 跨入口契约（`internal/{tui,agentruntime,acp,serve}`） | TUI/CLI/WebUI/ACP/Channel 在 flip 前后追加均只反映协议差异，canonical entry/Run/事件一致 |
| `TestRetireWaitsForPostFlipGrace`（已实现） | 冷静期内 attach 不删已覆盖行、计数为 0；冷静期后 attach 删除并计数；split 计数不增长 |
| `TestCheckSessionContentStoreReportsAndRepairsSplitStore`（`internal/doctor`，已实现） | 检查报 warn + 会话 ID；`--repair-session-content all` 采纳并回收；再检查为 ok；重复修复为 no-op |
| 混合 build（残余，见 §3.6） | 冷静期后旧构建才开始写 → 旧构建自身报错并形成 split；内容不被删除，可被修复入口采纳 |
| 架构守卫（P2） | resolver 单点、open 不写 DB、无第二套 store 判定 |

变异检验：至少对「probe 强制置 0」「resolver 退回读内存标志」「open 恢复删除」三个变异体证明测试会以精确信息失败（沿用本仓习惯）。P0 阶段的等价验证是 `TestOpenPreservesSplitLegacyRowsAndRetireRefuses`（把删除放回 open 即失败）与 `TestStaleLegacyHandleAdoptsEventStoreAfterFlip`（把 resolver 改回读内存标志即失败）。

## 6. 发布与回滚

- 发布顺序：P0（不删、不丢）→ P1（增量修复 + 冷静期）→ 观察 → 用 `doctor --repair-session-content all` 收敛存量 split 会话（先退出所有进程并备份）。
- 升级说明必须包含"重启每一个 MothX 进程"：旧构建与新构建共存时，旧构建仍会按 legacy 写并可能自报 `ErrSessionModified`；其写入不会被删除，可由修复入口采纳。
- 回滚条件：任何阶段只要发现「事件日志缺失但 legacy 行被删」或「split probe 报错」，回滚到 P0 状态（两存储并存是安全态，不丢数据）。
- 兼容性：不新增 `settings.json`/`serve.json` 字段语义；schema 变更只追加 migration，DAO-only。
- 运维前置（当前机器，立即执行）：把所有 mothx 进程重启为同一构建；备份 `sessions.db*` 与 `event-store/`；再做修复（详见 notes §6）。

## 7. 明确不做

- 不引入第二套 session/内容运行期或独立的「迁移守护进程」。
- 不让适配器（TUI/CLI/WebUI/ACP/Channel/Desktop）各自判断 store 或自行修复。
- 不在 serve/TUI 启动路径里自动删除或自动 reset 事件日志。
- 不把 UDP 租约总线/缓存当作 store 判定或删除授权（仍只是提示）。
- 不为了「消除报错」而放宽 CAS 或跳过 lease 校验。

## 8. 风险与取舍

- **保留 legacy 行有成本**：split 会话会同时保留两份内容直到修复；`entries` 的体积与写压力短期不变（可接受，正确性优先；长期由 P1 修复收敛）。
- **能力门是保守启发式**：老构建不写登记，因此可能把「无其他进程」的情况也判为不确定，从而延迟迁移。取舍是「延迟迁移」优于「丢内容」；`doctor` 提供显式推进手段。
- **增量迁移需要按 ID 拼父链**：若旧构建已删除未导出的祖先，只能 fail closed + 人工决策；这是既有破坏造成的结果，方案只能保证不再产生新的此类破坏。
- **resolver 缓存**：必须由 flip/defer/repair 显式失效，否则会退回「内存标志决定写入」的老问题；守卫与变异测试覆盖。
