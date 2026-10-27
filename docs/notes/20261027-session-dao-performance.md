# 2026-10-27 session / DAO 性能治理进度

> 状态：第一批 + 第二批（搜索）已实现并通过本地测试；第二批分页、连接模型未开始。
> 代码未提交（工作区改动），换机后从本文件继续即可。

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

## 3. 验证状态

在 QA 注入变异之前，以下全部通过：

```bash
go build ./...
go test ./internal/session/... ./internal/dao/... ./internal/architecture -count=1
go test ./internal/agentruntime/... -count=1
go test ./internal/serve/... ./internal/acp/... -count=1
go test -race ./internal/session -run 'TestOpenConvergedEventStoreSession|TestMigrateToEventStoreReinspects|TestApplySchemaMigrations|TestReadSessionExecutionFactsBatch|TestManagerMessageCount' -count=1
```

第一轮 QA 结论：通过；变异检验（去掉 legacy 探测 / MessageCount 退回 GetMessages）都能让对应测试失败，且已恢复。第二轮搜索 QA 在变异检验时被取消；**其注入的 `flush()` 内 `OpenByIDExact` 变异已还原**，当前 `search.go` 回到已通过测试的版本，`go build ./...` 通过。搜索语义的独立 QA 签字仍待补。

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

## 4. 尚未完成（换机后继续）

按建议顺序：

1. **搜索 QA 收尾**：补跑语义等价 + 变异检验（不要在生产代码里留变异；上次挂起就是变异导致）。
2. **列表 SQL 分页**（原计划第 2 项）
   - ACP 标准 `session/list`：把 allowed roots 下沉为 SQL CWD 过滤 + `LIMIT/OFFSET`，用 `limit+1` 判断 `hasMore`；`writeSessionList` 改为消费已分页结果。
   - `mothx/session/listAll`（Desktop 历史库）：`scope=project/ungrouped` 下沉 SQL `session_metadata` 过滤 + 分页；`query` 分支暂保留 Go 过滤（需要 event-store title/preview）。
   - session 层新增选项：`WithCWDRoots`（用 `filepath.Clean` 匹配 `SELECT DISTINCT cwd` 后传精确值，保持现有安全边界）、`WithProjectScope`。
   - DAO：`SessionListFilter` 增加 `CWDs` / `ProjectID` / `Ungrouped`，`Count` 同步。
3. **读并发 / 队头阻塞**（原计划第 3 项）
   - `internal/db/db.go` `openOnce`：`SetMaxOpenConns(4)`、`SetMaxIdleConns(4)`；更新 `TestPoolStatsReportsSingleConnectionModel` 与相关注释。
   - 加确定性测试：一个连接持有写事务时，另一连接读查询仍能在短 deadline 内完成。
   - 保留 `busy_timeout` / 90s 重试语义；写入仍可能被跨进程锁拖慢，但不再冻结全进程读。
4. **后续（未开工）**
   - FTS5 会话内容索引（搜索不再依赖内容重放；需要 append 双写 + 回填策略）。
   - `PRAGMA quick_check` 按需执行 / 后台化。
   - `entries`、`sub_entries`、`tool_execution_records` 保留策略与一次性 `VACUUM`（自动 vacuum 可选）。

## 5. 风险与注意事项

- **不要提交/推送**：所有改动仍在工作区；`npm/`、`pypi/` 下的 package.json/pyproject 改动是用户本机既有改动，不要一起提交。
- 当前机器上有一个 `mothx` 进程（本次会话）在写 `~/.mothx/sessions/sessions.db`，库大小/mtime 变化不代表测试写入。
- v49 迁移只在**新二进制启动**时应用；应用前不要在同目录并发跑多个写入进程做大规模迁移。
- 搜索缓存放最后再做（当前每次搜索仍是一次全量内容扫描，但无写事务、无逐会话 open、total 与 page 同源）。
- 修改 `internal/session` 的持久化/入口路径后按 AGENTS.md 跑 `go test ./internal/architecture`。

## 6. 恢复工作时的第一步

```bash
cd /home/free/src/mothx
git status --porcelain | grep -E "internal/|docs/notes"
go build ./...
go test ./internal/session/... ./internal/dao/... ./internal/architecture -count=1
go test ./internal/serve/... ./internal/acp/... -count=1
```

然后从“尚未完成”第 1 项开始。
