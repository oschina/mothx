# 2026-10-27 session / DAO 性能治理进度

> 状态：第一批、第二批（搜索、列表分页、连接模型）以及 quick_check 可显式关闭 + doctor 按需检查均已实现并通过最终验证。
> 基线提交为 `342b6e97`；本文件描述的改动仍在工作区，尚未提交。

## 1. 背景

用户反馈 `internal/session` / `internal/dao` “各种操作有时候阻塞很慢”。审计后用真实库（`~/.mothx/sessions/sessions.db`，当时约 660MB / 381 会话 / 33.7k entries / 19.5k projections）实测得到的主要瓶颈：

1. 进程内 `SetMaxOpenConns(1)` + `BEGIN IMMEDIATE` + `busy_timeout=10s` + 90s 重试预算：一个写事务等锁会占住唯一连接，把同进程所有读一起堵住。
2. 打开已收敛 event-store 会话：全量扫描事件库 3 次 + 无条件写事务。
3. 每次打开 `sessions.db`：28 条迁移各开一个写事务（约 29 次 `BEGIN IMMEDIATE`），外加 `PRAGMA quick_check`（本机 660MB 约 463ms）。
4. 会话列表：`MAX(timestamp)` 相关子查询无匹配索引、计算列全量排序；`ListActiveSessions` 每会话一次 metadata + 一次执行事实读（后者每次全扫 `session_runs`）；`len(GetMessages())` 深拷贝整段 transcript。
5. 搜索：带 `search` 时对每个会话 `OpenByIDExact`（完整 load + 收敛写事务 + 全量重放），serve 端点还会再跑一遍 `CountWithMessages`，同一遍扫描做两次。
6. 数据库无保留策略，`entries` / `sub_entries` / `tool_execution_records` 持续膨胀。

## 2. 已完成

### 2.1 会话打开路径（第一批）

- `internal/session/session.go`
  - 新增 `committedEventView`：一次 `InspectEventStore` 同时产出 entries + committed batches。
  - `loadLocked` 把该 view 传给 convergence，不再二次/三次扫描。
  - 新增 `eventStoreConverged` + `eventStoreConvergedFingerprint`；`Reload` 清空。
- `internal/session/event_store_append.go`
  - `convergeFlippedSession` / `repairUnmappedTurnBoundaries` 复用 view 的 batches。
  - 删除 legacy payload 前先 `HasLegacyEntries` 探测，空则完全跳过写事务。
  - `MigrateToEventStore` 仅在“已收敛 + 物理指纹未变”时跳过重新收敛；指纹变化 / 为空 / stat 失败一律重新 inspect（保持 fail-closed）。
- `internal/session/event_store_display.go`：指纹函数改名 `eventStorePhysicalFingerprint`（仅 stat，不扫内容）。
- `internal/dao/session_event_projection.go`：`HasLegacyEntries`。
- `internal/session/database.go`：包内 `writeRootTransactions` 计数（仅测试观测，无业务含义）。

效果（确定性测试）：打开已收敛会话 3 次全量扫描 → 1 次；写事务 1–2 → 0；同一 Manager 的 bind 0 扫描 / 0 写事务。

### 2.2 schema 迁移检查（第一批）

- `internal/session/migrations.go`
  - `ensureSchemaMigrationsTable` 先只读探测；表/列已齐时不写。
  - `applySchemaMigrations` 一次读取 `schema_migrations`，只对“缺失迁移”或“legacy version IS NULL 回填”开事务；当前库 0 次写事务。
  - 新增 v49 `add_session_list_aggregate_indexes`：
    - `idx_entries_session_timestamp(session_id, timestamp)`
    - `idx_session_event_projections_session_timestamp(session_id, timestamp)`
    - `idx_session_runs_session_status_started(session_id, status, started_at)`
  - `currentSchemaVersion = 49`。
- `internal/session/schema.go`：`busyRetryDatabase` 增加只读 `Query/QueryRow`，供迁移探测。
- 注意：v49 尚未在任何真实库应用。新二进制**下次启动**会做一次性索引构建（本机 639MB 库副本实测三个索引各约 5–6ms 热缓存；冷盘会慢一些），期间短暂持有写锁。

### 2.3 会话列表 N+1（第一批）

- `internal/session/execution_facts.go`：`ReadSessionExecutionFactsBatch`（一个只读事务；批量 existence / active runs / leases；仅单活动 run 才读 recovery/remote）。单会话实现复用同一逻辑。
- `internal/dao/{session,run,runtime_lease}.go`：`ExistingIDs`、`FindListByID`、`ActiveBySessions`、`FindMany`。
- `internal/agentruntime/execution_snapshot.go`：`InspectSessionExecutions` 真正批量（cache 优先 + 一次 batch facts）。
- `internal/session/message_count.go`：`Manager.MessageCount()`，按 replay/compaction 语义计数，不 clone `provider.Message`。
- `internal/session/session_detail.go`：`SessionDetailByID`（单会话列表投影）。
- `internal/serve/openaiapi/session_mgr.go`
  - `ListActiveSessions`：批量 metadata（1 次）+ 批量 execution（1 次）；缺失 execution 用 `UnknownSessionExecution`（unknown，绝不当 idle）。
  - `SessionPool.listDetails` 使用 `MessageCount()`。
  - `SetSessionTitle` / `SetSessionMetadata` 改用 `activeSessionInfo(id)`，不再全量列表后查找。

### 2.4 搜索路径（第二批，已实现待 QA 最终签字）

- `internal/session/search.go`
  - `SearchAllDetailed(sessionDir, query, limit, offset) (details, total, err)`：单遍、只读。
  - cheap 字段（ID/cwd/channel/title/preview）命中则不读正文。
  - legacy：`StreamSearchContentEntries` 单条流式 SQL，按 `session_id, seq` 排序，逐会话 `buildReplayState` 后匹配 —— 与 `GetMessages()` 语义一致（compaction 丢弃内容不命中、override 替换原文不命中、summary 可命中）。
  - event-store：`InspectEventStore` + `committedEventBatches` + `decode` + `buildReplayState`，只读、不收敛、不取 lease；异常降级为“仅 cheap 字段匹配”。
  - total 与 page 来自同一次扫描。
- `internal/session/session.go`
  - `ListAll` / `ListAllDetailed` / `CountWithMessages(WithSearch)` 统一走同一实现。
  - 删除旧的逐会话 `OpenByIDExact` 搜索路径。
- `internal/dao/session.go`：`StreamSearchContentEntries`（流式，避免 214MB 一次性入内存）。
- `internal/serve/run.go`：`/sessions` 带 search 时只调用一次 `SearchAllDetailed`；`listPersistedSessions` 分页内 metadata/execution 改为批量。
- `internal/acp/acp.go` / `internal/acp/manage.go`
  - `sessionCascadeIDs` 改用 `ListSessionParents`（单查询 id/parent）。
  - stats 的 session 计数改用 `CountAll`。
- `internal/serve/openaiapi/handler_session_trajectory.go`：`exportSessionIDs` 改用 `ListSessionParents`，删除逐会话 `OpenByIDExact`；测试用 `listSessionParents` seam。

### 2.5 列表 SQL 过滤与分页（第二批）

- `internal/dao/session.go`
  - `SessionListFilter` 增加 `CWDs`、`ProjectID`、`Ungrouped`；`List` 与 `Count` 共用同一个 `applySessionListFilter`。
  - `DistinctCWDs` 提供已存储目录的精确拼写。
  - 列表按 `last_active_at DESC, id ASC` 排序，为 LIMIT/OFFSET 提供稳定分页次序。
- `internal/session/session.go`
  - 新增 `WithCWDRoots`：在 session 层用 `filepath.Clean` 匹配 `DISTINCT cwd`，只把精确命中的存储值交给 SQL；空 roots 表示无可见会话，避免前缀越权。
  - 新增 `WithProjectScope`：空 project ID 表示未分组；DAO 用 `EXISTS`/`NOT EXISTS` 在分页前过滤。
  - 搜索路径同样携带 CWD/项目条件，避免分页或搜索结果越过授权范围。
- `internal/acp/acp.go`
  - 标准 `session/list` 先解码游标，再按 `limit+1` 读取当前页；allowed roots 下沉 SQL，不再先加载全量目录后在 Go 里过滤。
  - `mothx/session/listAll` 无 query 时把 project/ungrouped 与分页下推 SQL；有 query 时保留 Go 侧标题/预览搜索，避免改变搜索语义。
  - `writeSessionListPage` 只投影当前页，下一游标由读取结果直接给出。
- 新增/扩展测试：
  - `internal/session/cwd_roots_test.go`
  - `internal/acp/acp_mcp_test.go`
  - `SessionListRowsRead()` 进程内测试遥测：ACP 标准列表首屏断言只读取 51 行、`listAll` 项目首屏只读取 51 行，防止退回“全量加载后切片”。
  - `internal/session/search_test.go` 的变异验证：恢复 `OpenByIDExact` 注入后测试按预期失败，代码已还原。

### 2.6 SQLite 读并发（第二批）

- `internal/db/db.go`：托管连接池改为 `SetMaxOpenConns(4)` / `SetMaxIdleConns(4)`。WAL 读可在另一连接持有写事务时继续；SQLite 仍只有单写者。
- `internal/db/wal_test.go`
  - 池统计测试改为期望 4 连接。
  - 新增 `TestReadProceedsWhileWriteTransactionHoldsLock`：持有未提交写事务时，另一连接在 2s deadline 内读取最后一个已提交快照。
- 更新了 run-store、恢复测试中仍描述“单连接池”的过期注释。
- 4 连接并发暴露并修复了一个后台 Responses 停止竞态：停止可能在 coordinator 安装 Agent 前把 Run 标为 cancelling，原来会落入默认 `failed` 且不取消远端任务。现在该分支保留 `cancelled`，并显式调用 provider 侧 cancel；`TestRequestSessionStopCancelsResponsesBackgroundPolling` 连续多轮通过。

### 2.7 quick_check 可显式关闭 + doctor 按需检查（后续优化）

- `internal/db/db.go`
  - 启动时默认仍执行 `PRAGMA quick_check`，保持 fail-closed；这是相对最初“默认后台化”方案的收敛策略，并不消除默认启动成本。
  - `MOTHX_SQLITE_QUICK_CHECK=off|false|0|skip|disabled` 可显式跳过启动检查；其他值（包括未知值）仍保持检查。
  - 新增 `StartupQuickCheckEnabled()`，统一暴露启动检查策略，避免 doctor 复制环境变量解析。
  - 新增 `VerifyIntegrity(path)`：不经连接缓存、只读执行 quick_check，不执行修复或迁移。
  - 新增 `IsIndexOnlyIntegrityError(err)`：区分可用 `REINDEX` 修复的 stale index 与其他数据损坏。
- `internal/doctor/doctor.go`
  - 新增 `sessions.integrity` 检查；数据库存在时调用只读 `VerifyIntegrity`。
  - 修复建议按实际情况区分：检查开启时重启可修复 stale index；检查关闭时先 unset `MOTHX_SQLITE_QUICK_CHECK` 再重启；非索引损坏则提示备份/恢复，不再承诺重启修复。
  - 设置 `MOTHX_SQLITE_QUICK_CHECK` 时，doctor 的环境覆盖检查会显示该覆盖。
- 新增测试：db 的环境开关、显式关闭后不执行修复、只读完整性检查、索引损坏分类；doctor 的缺失/健康/损坏数据库检查及条件化修复建议。
- 中英文配置、会话文档和 changelog 已同步。

## 3. 验证状态

本轮新增代码已通过下列测试：

```bash
go test ./internal/db ./internal/doctor -count=1
```

此前分页与连接模型验证全部通过：

```bash
go build ./...
go test ./internal/session/... ./internal/dao/... ./internal/db/... ./internal/architecture -count=1
go test ./internal/agentruntime/... -count=1
go test -p 1 ./internal/serve/... ./internal/acp/... -count=1
go test -race ./internal/db ./internal/session -run 'TestReadProceedsWhileWriteTransactionHoldsLock|TestPoolStatsReportsReadConcurrentConnectionModel|TestListWithCWDRootsMatchesOnlyAuthorizedDirectories|TestListWithProjectScopeFiltersBeforePagination|TestSearchAllDetailedSemantics' -count=1
go test -race ./internal/acp -run 'TestListSessionsFiltersAllowedRootsBeforePaging|TestListAllSessionsFiltersProjectScopeBeforePagination|TestListSessionsUsesOpaqueCursor' -count=1
(cd desktop && npm test -- --runInBand)
```

Desktop 的 286 项测试全部通过。

搜索变异检验已补完：向 `search.go` 的 legacy 聚合路径注入 `OpenByIDExact` 后，
`TestSearchAllDetailedSemantics` 按预期失败（报告打开了 60 个 session，要求 0）；
临时备份已还原，工作区不含变异代码。

未串行运行整个 `./internal/serve/...` 时曾出现 3 个时序性失败，涉及异步 Run 终止、ESM 状态和异步临时目录清理；对应包单独重跑以及最终 `-p 1` 串行验证均通过。最终结论以串行包测试结果为准，避免高并发测试进程互相干扰。

新增测试文件：

- `internal/session/event_store_open_cost_test.go`
- `internal/session/migrations_transaction_test.go`
- `internal/session/migrations_index_test.go`
- `internal/session/execution_facts_batch_test.go`
- `internal/session/message_count_test.go`
- `internal/session/search_test.go`
- `internal/agentruntime/execution_snapshot_test.go`（扩展）
- `internal/serve/session_search_test.go`
- `internal/serve/openaiapi/session_export_parents_test.go`
- `internal/serve/openaiapi/server_test.go`（扩展）
- `internal/acp/session_search_cleanup_test.go`

## 4. 尚未完成（后续优化）

本轮计划项已完成，以下属于下一阶段：

1. **FTS5 会话内容索引**：搜索不再依赖内容重放；需要 append 双写 + 回填策略。
2. **保留策略与空间回收**：`entries`、`sub_entries`、`tool_execution_records` 保留策略与一次性 `VACUUM`（自动 vacuum 可选）。
3. **搜索缓存**：当前搜索仍是一次只读全量内容扫描，但已无逐会话 open、无写事务，且 total 与 page 同源。

启动 quick_check 默认开启；更激进的“按干净关闭标记跳过”或后台检查仍可在未来评估。当前显式关闭后，`mothx doctor` 是推荐的按需完整性检查入口。由于当前机器数据库不再是原始 660MB，本轮不以 wall-clock 基准作为完成条件，而以 SQL LIMIT 读取行数测试作为分页性能回归门槛。

## 5. 风险与注意事项

- 列表分页与连接池改动尚未提交；不要未经确认把无关的 `npm/`、`pypi/` 改动一起提交。
- 当前运行的 `mothx` 进程就是本助手自身；库大小/mtime 变化不代表测试写入。
- v49 迁移只在**新二进制启动**时应用；应用前不要在同目录并发跑多个写入进程做大规模迁移。
- 4 连接是进程内 WAL 读并发配置，不改变 SQLite 单写者和跨进程写锁语义。
- 修改 `internal/session` 的持久化/入口路径后按 AGENTS.md 跑 `go test ./internal/architecture`。

## 6. 恢复工作时的第一步

本轮计划项已完成。提交前可复核：

```bash
cd /home/free/src/mothx
git status --porcelain | grep -E "internal/|docs/notes"
git diff --check
go build ./...
```

下一步从第 4 节“尚未完成（后续优化）”开始。
