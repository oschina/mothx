# 2026-10-27 session / DAO 性能治理进度

> 状态：第一批、第二批（搜索、列表分页、连接模型）、第三批（会话内容 FTS5 索引）、第四批（会话存储维护）、第五批（Serve 分页）、第六批（列表展示投影）、第七批（查询级结果缓存）以及 quick_check 可显式关闭 + doctor 按需检查均已实现并通过最终验证（第三批起的改动尚在工作区）。
> 基线提交为 `342b6e97`；会话/DAO 改动提交于 `cae95987`，Desktop 渲染与列表投影改动提交于 `a0aba5bc`、`e28047d4`。

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
  - `currentSchemaVersion = 53`。
  - 新增 v50 `converge_session_run_status_index`：重新声明 `idx_session_runs_session_status_started`。v49 的该索引是在版本已被部分数据库记录后才补进迁移体的；`applySchemaMigrations` 对已记录版本直接跳过，因此这些库永远缺该索引。v50 用 `CREATE INDEX IF NOT EXISTS` 让它们下次启动时收敛。
  - 新增 v51 会话内容 FTS 索引，见 2.9。
  - 新增 v52 `session_display_projection` 会话列表展示投影，见 2.12。
  - 新增 v53 `session_store_meta` 全库搜索写入版本与触发器，见 2.13。
- `internal/session/schema.go`：`busyRetryDatabase` 增加只读 `Query/QueryRow`，供迁移探测。
- 注意：本机真实库已记录 v49，但当时 v49 迁移体只包含前两个索引，导致 `idx_session_runs_session_status_started` 缺失；v50 会在新二进制**下次启动**时补建（本机 639MB 库副本实测三个索引各约 5–6ms 热缓存；冷盘会慢一些），期间短暂持有写锁。v51 创建 FTS5 虚拟表，不扫描存量内容；存量会话在 attach 时逐个会话回填。

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

### 2.4 搜索路径（第二批）

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

### 2.8 Desktop 首次列表的 EventStore 展示投影（后续优化，已验证）

`mothx/session/listAll` 首屏仍需为每页会话补齐标题/预览,原实现通过
`loadCommittedEventEntries` 重放整段 EventStore,50 个会话在本机约 1–3 秒。

- `internal/session/event_store.go`
  - `scanEventStore` 增加 `verifyDigest` 参数;新的 `inspectProjection()` 只校验记录框架与序列连续性,跳过每个 batch 的 SHA-256 重算,并且不写入 append cache。
  - 非修复扫描改用 `O_RDONLY`,只读列表不再要求 EventStore 可写。
- `internal/session/event_intents.go`
  - 新增 `InspectEventStoreProjection`,复用 intent 校验但使用结构扫描;canonical `InspectEventStore` 仍校验全部摘要并 fail-closed。
- `internal/session/event_store_display.go`
  - 不再构造 `Manager`/`GetMessages()` 重放;直接从 committed batches 解码首条 user 预览、最新标题与绑定元数据。
- `internal/session/session.go`
  - `FillSessionDetailPreviews` 与 `LatestSessionBindings` 先用有界 worker 池(`min(GOMAXPROCS,8)`)预取当前页展示投影,再顺序填充,保证结果确定性。
- Desktop renderer
  - 状态通知合并到每帧一次;转录条目不可变替换并对 `TranscriptEntry`/`Markdown` 做 memo;侧边栏/状态栏/输入框/计划面板改用 selector 订阅;视口外转录块启用 `content-visibility`;自动跟随滚动与帧合并。

本机 `mothx acp` 首次 `mothx/session/listAll` 从约 2.7s 降到约 1.2s。



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

Desktop 的 307 项测试全部通过。

搜索变异检验已补完：向 `search.go` 的 legacy 聚合路径注入 `OpenByIDExact` 后，
`TestSearchAllDetailedSemantics` 按预期失败（报告打开了 60 个 session，要求 0）；
临时备份已还原，工作区不含变异代码。

未串行运行整个 `./internal/serve/...` 时曾出现 3 个时序性失败，涉及异步 Run 终止、ESM 状态和异步临时目录清理；对应包单独重跑以及最终 `-p 1` 串行验证均通过。最终结论以串行包测试结果为准，避免高并发测试进程互相干扰。

新增测试文件：

- `internal/session/event_store_open_cost_test.go`
- `internal/session/migrations_transaction_test.go`
- `internal/session/migrations_index_test.go`
- `internal/session/migrations_search_index_test.go`
- `internal/session/migrations_display_projection_test.go`
- `internal/session/display_projection_test.go`
- `internal/session/search_index_test.go`
- `internal/session/storage_maintenance_test.go`
- `internal/serve/openaiapi/server_test.go`（扩展分页/内存池）
- `internal/serve/config_test.go`（扩展默认分页）
- `cmd/mothx/main_maintenance_test.go`
- `internal/session/execution_facts_batch_test.go`
- `internal/session/message_count_test.go`
- `internal/session/search_test.go`
- `internal/session/search_result_cache_test.go`
- `internal/session/migrations_write_version_test.go`
- `internal/agentruntime/execution_snapshot_test.go`（扩展）
- `internal/serve/session_search_test.go`
- `internal/serve/openaiapi/session_export_parents_test.go`
- `internal/serve/openaiapi/server_test.go`（扩展）
- `internal/acp/session_search_cleanup_test.go`

### 2.9 会话内容 FTS5 索引（第三批）

搜索此前对每个未命中 cheap 字段的会话重放整段内容。现在新增派生 FTS5
（trigram）索引，把“哪些会话可能命中”和“最终是否命中”拆开：

- `internal/session/migrations.go`：v51 创建 `session_content_fts`
  （`session_id`/`entry_id` UNINDEXED，`text` 使用 trigram tokenizer）与
  `session_content_index_state`；`currentSchemaVersion = 51`。trigram 支持任意子串
  （含 CJK）且大小写不敏感；不足 3 个字符的查询继续走全量重放，保持原语义。
- `internal/dao/session_search_index.go`：唯一直接读写 FTS 的 DAO，负责插入、
  按 `MATCH` 取候选 session、查询已索引集合、按会话删除，以及 FTS 短语转义。
- 双写：legacy `writeEntry`/`writeEntries` 与 EventStore `CommitEventIntent`
  在持久化事务内同步写索引；legacy fork 复制行时为子会话写索引；
  `RebuildEventProjection` 同时重建索引；删除会话级联清理索引行与状态行。
- 回填：`MigrateToEventStore` 在 attach 时对尚未建立索引的老会话做一次
  best-effort 回填；新增 `BackfillSessionSearchIndex` 在每日会话存储维护任务中
  按最新会话优先补建，单次最多 512 个会话（每个会话一个事务），
  legacy 与 EventStore 分别从 canonical 内容读取；读不到内容的会话只计入
  skipped 并留待下次，不会把不完整索引标记为完成。未回填或回填失败的会话
  继续走原有重放路径，不会漏匹配。搜索本身仍然只读，不触发回填、不写事务。
- 候选校验：FTS 只负责缩小候选集，最终匹配仍由 canonical replay 决定，因此
  compaction 丢弃内容、content override 替换文本的语义完全不变；索引中出现
  “已失效文本”只会多一次候选校验，不会产生假命中。
- 降级：查询不足 3 个字符、FTS 出错、会话未索引时自动退回原全量扫描。
- 投影缓存：EventStore 搜索投影按物理日志指纹缓存（stat-only，append/轮转即失效），
  最多 128 条 / 32MB、单条不超过 4MB；重复搜索不再重放未变化会话。

本机实测：6 个会话、仅 1 个含目标子串时，单次搜索只 replay 1 个候选会话；
2 字符查询按预期 replay 全部 6 个。

### 2.10 会话存储维护（第四批）

产品约束：**旧消息永久保留**。因此本轮不实现任何针对 `entries` /
`sub_entries` 的保留策略；它们只被计数，永不被删除。

- `internal/config/settings.go`：`maintenance` 新增
  `reclaimSessionStorage`（默认 true）、`sessionStorageSchedule`（默认 `@daily`）、
  `toolExecutionRetentionDays`（默认 0 = 永久保留）与
  `vacuumSessionStorage`（默认 false）。
- `internal/agentruntime/maintenance_cron.go`：新增 Runtime 维护任务
  `mothx-maintenance:session-storage`，复用共享 cron 的 claim/status/next-run
  生命周期；任务仍从所有用户可见 cron 列表过滤。
- `internal/agentruntime`：策略解析、禁用时拒绝陈旧任务。
- `internal/session/storage_maintenance.go`：`MaintainSessionStorage` 只清理
  `completed` / `abandoned` 且早于保留期的 `tool_execution_records`；
  `running` / `interrupted` / `retry_requested` 永远保留，避免不确定的工具副作用
  失去围栏。每次运行执行 `PRAGMA optimize` 并报告 transcript/tool 行数与
  freelist 页数；`vacuumSessionStorage` 为 true 且存在空闲页时才执行 `VACUUM`。
- `internal/dao/session_storage.go`：唯一 SQL 位置（清理、计数、PRAGMA、
  VACUUM）。
- 默认策略下任务只做 optimize 与统计，不删除任何消息或工具记录；要真正回收
  空间需显式配置保留天数或开启 `VACUUM`。`VACUUM` 会长时间持有写锁，因此
  默认关闭，且只在 cron 维护任务中执行，不在启动路径上运行。
- 新增一次性入口 `mothx maintenance status` 与 `mothx maintenance vacuum [--dry-run]`：
  status 只读统计数据库大小、转录/工具记录与空闲页；vacuum 只执行 VACUUM，
  不按保留期删除工具记录，也绝不删除消息。

测试覆盖：只清理到期的终态工具记录、保留所有 in-flight 状态、transcript
行数与消息内容不变、retention 0 时全部保留、禁用策略拒绝执行、两个维护任务
在共享 store 中各自只投影一次。

### 2.11 Serve 会话列表分页（第五批）

`ListActiveSessions` 此前每次调用都会加载全部历史详情，再用来返回一个小结果（`scope=active` 只需内存池），是最后一个随会话总数无界增长的请求路径。

- `internal/serve/openaiapi`：新增
  - `ListActiveSessionsPage(limit, offset)`：SQL LIMIT/OFFSET 取当前页，metadata/execution 仅批量读取该页 ID，并返回 total；
  - `ListActivePoolSessions()`：只投影本进程内存池；
  - `GetSessionWorkDir(id)`：按 ID 单查工作目录，供 MCP 配置等管理端点使用。
- `internal/serve`：
  - `/api/sessions` 未指定 `limit` 时默认 `DefaultSessionListLimit = 100`，响应增加 `total`、`limit`、`offset`、`hasMore`；Web UI 原本就使用 `limit=100`，外部调用方可继续用 `limit`/`offset` 翻页。
  - `scope=active` 与 `/api/sessions/active` 改用内存池投影，不再加载全部历史。
  - 会话级 MCP 配置改用 `GetSessionWorkDir(id)`，不再为了找一条会话列出全部历史。
  - `status` 的兜底计数改用分页 total。
- 测试：分页边界与 total/hasMore、active 池排除持久化历史、默认首页上限 100、最后一页不足一页。

### 2.12 会话列表展示投影（第六批）

Desktop 首次任务列表此前为每个 EventStore 会话重放结构投影，只为取首条用户预览、最新标题与绑定元数据。现在把这些字段持久化为派生 SQLite 投影，启动列表不再扫描 EventStore。

- `internal/session/migrations.go`：v52 创建 `session_display_projection`
  （`session_id` 主键，`preview`、`preview_seen`、`title`、`bindings_json`、
  `updated_at`）；`currentSchemaVersion = 52`。
- `internal/dao/session_display_projection.go`：批量读取、upsert、按会话删除、
  查询尚未投影的 EventStore 会话。
- 写入：
  - `CommitEventIntent` 在提交事务内更新已存在的投影行；不会为缺少行的老会话
    创建不完整投影。
  - 新会话在第一条内容提交后由 `appendEventStoreEntriesLocked` 触发一次完整
    投影；非空迁移导出完成后由 `MigrateToEventStore` 建行。
  - EventStore fork 在复制完成后建行；`RebuildEventProjection` 同步重建；
    删除会话级联清理。
  - 每日会话存储维护按最新优先最多回填 512 个未投影会话。
- 读取：
  - `FillSessionDetailPreviews` 与 `LatestSessionBindings` 优先批量读取投影行；
    缺失行仍回退到原 EventStore 扫描，因此旧会话在回填前保持正确。
  - `preview_seen` 记录首条 user 消息是否已出现（即使文本为空），后续 user
    消息不会错误替换预览。
- 测试：迁移表结构、写入/更新/预览不覆盖、无 EventStore 字节时列表与绑定仍可
  读取、fork、删除、回填幂等、维护任务报告。

### 2.13 查询级结果缓存（第七批）

EventStore 投影缓存只消除了“同一会话的重复重放”；每次搜索仍要执行完整的列表
查询、cheap match、FTS 候选查询与聚合读取。历史列表的重复分页、WebUI 轮询以及
用户来回切换过滤器都会重复付出这笔成本。现在把“查询 → 已算好的结果页”也缓存：

- `internal/session/migrations.go`：v53 创建 `session_store_meta`
  （`key`/`value`，`currentSchemaVersion = 53`），并创建 SQLite 触发器，在
  `sessions`、`entries`、`session_event_projections`、`session_metadata` 的
  INSERT/UPDATE/DELETE 中递增 `search_write_version`。选择触发器而不是调用点
  计数器，是因为它同样覆盖另一进程的写入以及未来新增的 DAO 调用；只监听会改变
  搜索结果的表，避免无关写入（例如运行状态、lease、附件）使缓存失效。
- `internal/dao/session_store_meta.go`：唯一读取该版本的 DAO。触发器 SQL 属于
  migration owner，runtime 只读版本值。
- `internal/session/search.go`：
  - 结果缓存键包含 sessionDir、query、limit/offset、CWD 授权集合、项目作用域、
    messagesOnly 等过滤条件；CWD 集合排序后以长度前缀编码，避免不同顺序或分隔符
    造成碰撞。缓存上限 64 页 / 8MB，按插入顺序淘汰。
  - 命中时直接返回结果页，不再执行列表查询、FTS 候选查询或内容重放；命中结果
    会复制一份，调用方修改返回值不会污染缓存。
  - 未命中时先记录版本，扫描完成后再读一次；只有版本未变化才写入缓存。扫描期间
    并发提交的写入会让结果处于“可能包含也可能不包含”的模糊状态，因此不缓存。
  - 空结果页同样缓存；空页通常意味着“所有会话都重放过仍未命中”，是最值得复用
    的结果。
  - 版本行缺失、查询失败或旧库尚未迁移时自动退回无缓存路径，搜索正确性不依赖
    缓存。
- 测试：重复查询命中且内容重放为 0、空页命中、legacy 内容追加、标题写入、
  工作目录更新、EventStore 追加、项目归属变更均会使缓存失效、不同 offset 不
  共用缓存、迁移 v53 对每张受监听表的写入与父会话删除都递增版本。

## 3. 后续可选优化

本轮计划项已全部完成。以下不属于本轮验收条件：

1. **更激进的启动 quick_check 跳过策略**：默认仍开启（fail-closed）；“按干净关闭
   标记跳过”或后台检查可在未来评估。显式关闭后，`mothx doctor` 是推荐的按需
   完整性检查入口。
2. **极长转录的展示副本截断**：当前只限制 DOM/React 成本；进一步截断
   ACP/Runtime 展示副本需要先提供按需取全量内容的契约，暂不实施。

由于当前机器数据库不再是原始 660MB，本轮不以 wall-clock 基准作为完成条件，而以
SQL LIMIT 读取行数测试作为分页性能回归门槛。

## 4. 风险与注意事项

- 列表分页与连接池改动已提交到 `cae95987`；后续提交仍应避免混入无关的 `npm/`、`pypi/` 版本同步改动。
- 当前运行的 `mothx` 进程就是本助手自身；库大小/mtime 变化不代表测试写入。
- v49–v53 迁移只在**新二进制启动**时应用；应用前不要在同目录并发跑多个写入进程做大规模迁移。v53 的触发器会随每次会话写入递增一行元数据，首次在超大库上启用时请避免同时进行批量导出。
- 开启 `vacuumSessionStorage` 后，VACUUM 会重写整个 `sessions.db` 并长时间持有写锁；不要在高峰期或磁盘空间不足时启用。
- 4 连接是进程内 WAL 读并发配置，不改变 SQLite 单写者和跨进程写锁语义。
- 修改 `internal/session` 的持久化/入口路径后按 AGENTS.md 跑 `go test ./internal/architecture`。

## 5. 后续工作的第一步

本轮计划项（含查询级结果缓存）已完成。继续前可复核：

```bash
cd /home/free/src/mothx
git status --porcelain | grep -E "internal/|docs/notes"
git diff --check
go build ./...
go test ./internal/session ./internal/dao ./internal/architecture -count=1
```

第 3 节只保留不属于本轮验收条件的可选优化。
