# 2026-11-01 Session 内容存储 split-brain 与 `ErrSessionModified` 假报错调查

> 状态：调查完成；P0、P1 已落地并通过回归（2026-11-02）；P2 已落地（2026-11-03）。P0 无 schema 变更；P1 追加 migration 54（`sessions.content_store_flipped_at`）与 `session_store_meta` 计数键。
>
> 关联方案：[`docs/proposal/session-content-store-consistency-remediation-proposal.md`](../proposal/session-content-store-consistency-remediation-proposal.md)
>
> 现场基线：HEAD `84637581`（工作区另有 webui/workbench 未提交改动，与本问题无关）；本机同时运行多个不同构建的 mothx 进程（见 §5）。

## 1. 现象

用户反馈：**只有一个进程在处理任务时**，任务仍偶发失败：

```
session was modified by another process: expected leaf "63131f3ccf0d0deb", current leaf "";
reopen the session before writing
```

- 该文案只由 legacy 写入路径产生：`internal/session/session.go:3438`（`writeEntry`）与 `:3514`（`writeEntries`）。
- `ErrSessionModified` 定义：`internal/session/session.go:28`。
- 判据是 `entries` 表最新 payload 行 vs 内存 `m.leafID`；`current leaf ""` 表示**该 session 的 legacy payload 行已被删除**（真人并发写会得到非空且不同的 id）。
- 失败会被 `internal/agent/agent.go:960-966` 当作 `session_save` 失败并 `TaskFailed` 结束任务。

## 2. 现场时间线（session `95399bdbf08e4403`，cwd `/home/free/src/mothx`）

| 时间 | 事件 |
|---|---|
| 11:18:33 → 11:33:43 | run `tui_2348d4eba27a43c4` 完成；该 run 的 222 条 transcript 全部写在 legacy `entries` 表 |
| 11:35:46-47 | 另一进程（intent `owner_instance_id = pid-123184-…`）把该 session **迁移导出**到 EventStore：4 个已提交 intent（seq 1-222）、单个 segment 文件 450737B、`content_store='event_store'`、run #1 的 `conversation_turns` 边界被翻译为事件 seq `1..222`，并删除 legacy payload 行 |
| 11:36:15 → 13:02 | 旧构建 TUI 继续按 legacy 表写：run #2-#8 的 turn 边界全在 legacy seq（`244-366/367-374/375-395/403-508/509-576/577-582/583(open)`），`entries` 行持续增长（截查时到 seq 648） |
| 12:37:31 | 新 `mothx serve`（PID 135577，`make ui-dev` 流程）启动 |
| 12:37:37.567 | TUI 写入 toolResult 行 `63131f3ccf0d0deb`（parent `e2889d87af1fd431`） |
| 12:37:37-43 | 新进程 open 该 session → 收敛 → `DeleteLegacyEntries` 删除 legacy payload 行 |
| 12:37:43.503 | run `tui_e44ab2444a6c3c46` 下一次 append 失败：`expected leaf "63131f3ccf0d0deb", current leaf ""` |

## 3. 证据（全部来自只读副本）

副本目录：`/tmp/opencode/session-investigate/`（`sessions.db` + `-wal` + `-shm`，2026-11-01 13:0x 拷贝）。

1. **报错的 leaf 是一条被删除的 legacy 行**：`63131f3ccf0d0deb` 在 `entries` / `session_event_projections` / `sub_entries` 中都查不到，但在 `sessions.db` 的空闲页里可按字节找到：
   ```
   …95399bdbf08e4403 63131f3ccf0d0deb message e2889d87af1fd431 2026-11-01T12:37:37.567420823+08:00
   {"type":"message","id":"63131f3ccf0d0deb","parentId":"e2889d87af1fd431",…,"message":{"role":"toolResult",…"toolName":"edit"…}}
   ```
   → 被 `DELETE` 掉的 legacy 行，与「删除发生在两次 append 之间」形状一致。
2. **失败 run 的落盘记录**：`session_runs.id=tui_e44ab2444a6c3c46`，`status=failed`，`error` 即该文案，`error_info_json` 含 `failureClass=internal`、`phase=tool`、`partialOutput=true`、`assistantEntryId=0ebdc47c7917291b`。
3. **事件日志只有 run #1**：`session_event_intents` 4 条全部 `committed`、`updated_at=2026-11-01 11:35:47`；`event-store/95399bdbf08e4403/segments/*.jsonl` 一个 450737B、mtime `11:35:47`、`manifest.nextSeq=223`；段内不含 run #2 及以后的 run id。
4. **导出形态可判定**：段内 `turn_start` 的 id 是 legacy 随机 id（`05b1a597580ff15e`），而当前代码的所有事件侧 turn marker 都用 `stableTurnEntryID`（`turn_<hash>`，`conversation_turn.go:125`、`run_store.go:601`）→ 该内容来自**迁移导出**而非实时追加。
5. **旧构建 TUI 才是持续 legacy 写入的一方**（关键）：
   - 运行中的 TUI 是 `/home/free/go/bin/mothx`，**构建于 11:07:32**，进程 11:08 启动；
   - `git reflog`：`11:07:18 checkout: moving from jsonl_session to main`，`11:35:11 pull gitee main: Fast-forward`（`54c374be`）→ 该二进制早于 EventStore content store 引入；
   - 字符串核验（`strings -a | grep -c`）：

     | 字符串 | `/home/free/go/bin/mothx`(11:07) | `bin/mothx`(12:46) |
     |---|---|---|
     | `content_store` | 0 | 10 |
     | `session_event_intents` | 0 | 9 |
     | `session_event_projections` | 0 | 19 |
     | `migrate session content to EventStore` | 0 | 1 |
     | `remove legacy transcript payload after EventStore commit` | 0 | 1 |

     → 旧 TUI 完全没有 content-store 分支/迁移/收敛删除，只能按 legacy 表写。
6. **另一个进程仍在后台迁移会话**：本调查所用 session 自身 `496bc22b9be43f00` 的 event-store 目录 mtime 为 `12:56:29`，`content_store` 已从 `sqlite` 变为 `event_store`（12:53 时仍是 `sqlite`）——说明新构建进程在此期间 attach 并迁移了它。
7. **同类残留**：`092c54c219b20788`、`143b61c424135e47`、`75b0465b073a3eb1` 均为 `content_store='event_store'` + 无已提交事件 + legacy payload 行仍在。

## 4. 根因

**层次 A（现场事故）：混合构建 + 删除无风险判定。**
旧构建 TUI 只写 legacy `entries`；新构建进程按 `internal/session/event_store_append.go:498-537`（`convergeFlippedSession` → `HasLegacyEntries` → `DeleteLegacyEntries`，`dao/session_event_projection.go:104/114`）在**任意一次 open** 时删除 legacy payload 行。于是旧 TUI 两次 append 之间一旦有新构建 open 该 session，下一次 append 就报「另一个进程修改了会话」——从用户视角只有一个进程在跑任务，实际是两个构建。

**层次 B（代码缺陷，单进程同构建也可复现）：**
1. **写入路由三处裁判**：Manager 写入看内存 `m.eventStoreMode`（`session.go:1090` 等，**从不重读列**）；run 准入看持久化列（`sessionUsesEventStore`，`session.go:2905`）；run 终态看物理事件日志（`event_turn.go:28`）。只有读取/收敛路径（`event_store_append.go:216-227`）重读列并注明「早于 flip 打开的 Manager 会一直以为自己是 legacy」。
2. **open 会删数据**：删除由任何 open 触发，而当前构建每次 run 准入（`run_store.go:594-627`）与终态（`event_turn.go:34`）都会 `OpenByIDExact`，即**同一进程自己**就能删掉自己 handle 的 legacy 行。
3. **flip 不会通知活 handle**：`CommitEventIntent` 把列改成 `event_store` 只是首批次提交的副作用（`event_intents.go:315`），`flipSessionContentStore` 只改调用者自己的标志（`event_store_append.go:662`）；`MigrateToEventStore` 在租约被占/丢失时静默 `return nil`（`:106-115`）。
4. **无混合 build 能力门**：旧构建无法被约束，新构建却会翻转并删除；Desktop 捆绑的 `mothx acp` 会长期滞后，同类问题对终端用户同样成立。

## 5. 本机进程与数据状态（13:0x）

- 进程：`mothx` PID 100644（11:08 启动，二进制 11:07:32）＝唯一 TUI；`mothx serve` PID 135577（12:37:31 启动）；另有 UI dev server。
- `session_runtime_leases` 中 `95399bdbf08e4403` 的 owner 为 `pid-100644-…`，epoch 9，`active`（13:07-13:09 仍在续期）。
- 调查结束时该 session 仍有 run `tui_aa662bf5d46a4f69`（13:02:27 起）在按 legacy 写入（turn `start_seq=583`，`entries` seq 到 648），而 `content_store='event_store'`、事件日志 seq 上限仍为 222。

## 6. 风险与立即动作

⚠️ **数据风险**：`95399bdbf08e4403` 的 canonical 事件日志只到 run #1（11:33），其后的全部内容只存在于 `entries`。用新构建 attach 该会话时，`content_store='event_store'` 会让它优先事件日志并**删除较新的 legacy 行**（即真实内容丢失）。同类会话还有 §3.7 列出的三个。

建议立即执行（在跑新构建/做修复之前）：

1. 备份 `~/.mothx/sessions/sessions.db*` 与 `~/.mothx/sessions/event-store/`（不要只备份 `.db`，WAL 里有最新帧）。
2. 把所有 mothx 进程重启为**同一构建**（旧 TUI 与 11:35 之后的代码不可能互操作）；重启前先完成备份。
3. 用方案 P1 的 `doctor --repair-session-content`（待实现）做增量收敛；在此之前不要手工执行 `entries` 删除或用新构建反复打开这些会话。

复现/核验命令（只读，副本上执行）：

```bash
# split probe：未被事件日志覆盖的 legacy payload 行
SELECT COUNT(*), MAX(timestamp) FROM entries e
WHERE e.session_id = '<id>' AND e.type <> 'session'
  AND NOT EXISTS (SELECT 1 FROM session_event_projections p
                  WHERE p.session_id = e.session_id AND p.id = e.id);

# 事件日志是否只剩旧内容
SELECT count(*), max(seq_to) FROM session_event_intents WHERE session_id='<id>';
SELECT max(seq), count(*) FROM session_event_projections WHERE session_id='<id>';
```

## 7. 进度记录

- [x] 现象复现与时间线还原（session `95399bdbf08e4403`，12:25-12:37 失败 run）
- [x] 代码路径定位：`writeEntry`/`writeEntries` 叶子 CAS、`convergeFlippedSession` 删除、`sessionUsesEventStore` 调用点、`MigrateToEventStore` 静默 defer、`CommitEventIntent` 翻列
- [x] 数据证据固定（副本 + 字节级证据 + 事件日志/边界比对）
- [x] 混合构建证据（`git reflog` + 二进制字符串门 + `content_store` 变化时序）
- [x] 方案立项：`docs/proposal/session-content-store-consistency-remediation-proposal.md`
- [x] **P0（2026-11-02 落地）**
  - [x] 单一 resolver：`Manager.resolveContentStoreLocked`/`persistedContentStoreLocked`；`Reload` 抽出 `reloadLocked`
  - [x] 写入路径统一：`appendEntryLocked`（8 个 `Append*` 改工厂）+ `AppendMessages` 拆分（仅首分片未提交时自愈）
  - [x] 叶子冲突细分：`legacyLeafConflict` + 内部重试信号 `errContentStoreFlipped` + 导出错误 `ErrSessionContentStoreChanged`
  - [x] open 只读化：`convergeFlippedSession` 只探测与诊断（`SessionStoreSplitDetections`，每会话每进程一次日志）
  - [x] 围栏回收：`retireLegacyPayload`/`retireLegacyPayloadRows` + `dao.DeleteCoveredLegacyEntries`（DELETE 内覆盖过滤）+ `ErrLegacyPayloadSplit`
  - [x] 显式 defer：`MigrateToEventStoreOutcome`（`NotNeeded/Adopted/Deferred`）+ defer 日志；原签名保留
  - [x] DAO：`SessionDAO.ContentStore`、`SessionEventProjectionDAO.UncoveredLegacyEntryCount`
  - [x] 回归：`content_store_routing_test.go` 5 条 + `TestMigrateToEventStoreDefersWhileAnotherOwnerRuns` outcome 断言；两条旧「open 删除副本」测试改为「open 保留 / attach 回收」
  - [x] 验证：`go build ./...`、`go vet`、`gofmt` 干净；`internal/{session,dao,architecture,agentruntime,agent,acp,cron,tui,serve/...}` 全绿（session 19s / tui 3s / agentruntime 14s / acp 8s / openaiapi 15s）
- [x] **P1（2026-11-02 落地）**
  - [x] 增量采纳：`internal/session/content_store_adoption.go`（`Manager.AdoptLegacyContent` / `AdoptLegacyContentByID`）；只追加未覆盖行、按 ID 父链校验（缺失祖先 → `ErrSessionStoreBroken` 且不删）、分片提交、按 marker 身份翻译 turn 边界、最后围栏回收
  - [x] DAO：`UncoveredLegacyEntryIDs`、`SplitContentStoreSessions`、`ContentStoreFlippedAt`、`SetContentStoreFlipped`、`FlipContentStoreOnLease(..., now)`、`SessionStoreMetaDAO.IncrementCounter/Counter`
  - [x] 修复入口：`doctor.RepairSessionContent`（`all`/单会话、逐会话结果、5 分钟上限）+ `mothx doctor --repair-session-content`（文本与 `--json` 的 `repairs`）
  - [x] 混合 build 防护（修订为「冷静期 + 覆盖判定」，理由见方案 §3.6）：migration 54 `sessions.content_store_flipped_at`；`retireLegacyPayload` 在 `legacyPayloadRetireGrace`（默认 10 分钟）内返回 `ErrLegacyPayloadRetireGrace`
  - [x] 诊断：doctor 只读检查 `sessions.content_store`（split 会话数/未覆盖行数/前 5 个 ID/修复命令）；`session_store_meta` 记录 `content_store_migration_adopted|deferred|legacy_rows_retired`
  - [x] 回归：`content_store_adoption_test.go`（4 条）、`content_store_grace_test.go`（2 条）、`internal/doctor/session_content_repair_test.go`（2 条）；既有 attach 测试改为「attach 迁移 → 冷静期后回收」两阶段，`TestReloadConvergesFlippedSessionWithoutDeadlock` 改为断言 load 不删除
  - [x] 验证：`go build ./...`、`go vet`、`gofmt` 干净；`internal/{session,dao,architecture,agentruntime,agent,acp,cron,tui,doctor,db,serve/...}` + `cmd/mothx` 全绿
- [x] **P2（2026-11-03 落地）**
  - [x] turn 写入路由：`Manager.StartConversationTurn`/`EndConversationTurn` 改用 `resolveContentStoreLocked`；新增 `TestStaleConversationTurnHandleAdoptsEventStoreAfterFlip`（改回内存标志即失败）
  - [x] 架构守卫：`internal/architecture/content_store_guard_test.go`——resolver 单点、只读投影调用者白名单、open 不写内容存储（含跨包 DAO 方法）、`eventStoreMode` 不得路由写入；均通过变异检验
  - [x] allowlist/注释：`convergeFlippedSession` 无残留删除；`eventStoreMode` 合法读取点逐条注明理由
  - [x] changelog：`docs/{en,zh}/changelog.md` 与 `docs/changelog_online_{en,zh}.md`（含"重启每个进程"与 `doctor --repair-session-content` 用法）
  - [x] 验证：`go test ./internal/session ./internal/architecture` 全绿
- [x] 验证（现场，2026-11-02 02:25）：所有 MothX 进程退出后，备份 `~/.mothx/session-backups/20261102T022402/`，用当前构建执行
      `mothx doctor --repair-session-content 95399bdbf08e4403`（adopted 213 / retired 213）与
      `mothx doctor --repair-session-content all`（092c 4/4、75b 4/4、143b 3/3）。
      结果：全库 legacy payload 行 = 0，`doctor` 的 `sessions.content_store` = ok，`PRAGMA quick_check` = ok；
      `95399…` 投影 222 → 435（seq 1-222 为原 run #1，223-228 为 run #7，229-435 为 run #8），段文件 451 行（435 events + 16 batch markers）。
- [x] 现场修复中发现并修复：`dao.SplitContentStoreSessions` 的 `Table` + `TableExpr` 造成 `FROM entries, entries AS e` 笛卡尔积，doctor 把 224 行报成 52864 行；已改为单次 `TableExpr("entries AS e")`，并新增 `TestListSplitContentStoreSessionsReportsExactCounts`（同一库两个 split 会话的精确计数）固定。

### 7.1 P0 行为变更清单（便于 review/revert）

| 位置 | 变更 |
|---|---|
| `session.AppendMessage` 等 8 个写入 | 由「内存 `eventStoreMode` 分支」改为「resolver + 一次自愈重试」；legacy 写前多一次 indexed 读（仅当 handle 仍在 legacy 模式） |
| `session.writeEntry`/`writeEntries` | 叶子 mismatch 细分：event_store 列 → 内部重试；sqlite 列但 transcript 被清空 → `ErrSessionContentStoreChanged`；其余 → 原 `ErrSessionModified` |
| `session.convergeFlippedSession` | **不再删除** legacy payload（只探测 + 诊断）；turn 边界修复保持原样 |
| `event_store_append.exportLegacyTranscript` | 翻转 + 收敛后调用围栏回收（只删已覆盖行）；split 时保留全部行并返回成功 |
| `event_store_append.MigrateToEventStore` | 变成 `MigrateToEventStoreOutcome` 的薄包装；已收敛分支尝试围栏回收（无副本时不写、不扫描） |
| `dao.SessionEventProjectionDAO.DeleteLegacyEntries` | 仅保留给复现旧构建破坏行为的测试；生产删除走 `DeleteCoveredLegacyEntries` |

已知取舍：混合 build（旧二进制仍在写 legacy）下，`content_store` 翻转/回收仍可能发生在旧写者活跃期间；P0 只保证**新的**新构建不再自伤、且 split 不再丢内容。彻底防护依赖 P1 的能力门。

### 7.2 P1 行为变更清单

| 位置 | 变更 |
|---|---|
| `session.AdoptLegacyContent` | 新增：split 会话的增量采纳（只追加未覆盖行 → 翻译 turn 边界 → 围栏回收），幂等、可 defer |
| `session.adoptLegacyDeltaLocked` | 已翻转会话只走增量，绝不 `RemoveEventStore`/reset；缺失祖先 fail closed |
| `session.retireLegacyPayloadRows` | 新增 `force` 参数与冷静期判定；冷静期内返回 `ErrLegacyPayloadRetireGrace` |
| `session.flipSessionContentStore` | 首次翻转写 `content_store_flipped_at`（migration 54） |
| `session.MigrateToEventStoreOutcome` | 采纳/延迟分别 +1 `session_store_meta` 计数 |
| `doctor.checkSessionContentStore` | 新增只读检查（split 报告 + 修复命令） |
| `doctor.RepairSessionContent` / `cmd/mothx --repair-session-content` | 新增修复入口（`all`/单会话，逐条结果，`--json` 中 `repairs`） |

## 8. 现场修复步骤（新构建可用后）

1. **退出所有 MothX 进程**（TUI / serve / Desktop 的 ACP 子进程），确认 `pgrep -af mothx` 为空。
2. 备份：`~/.mothx/sessions/sessions.db*`、`~/.mothx/sessions/event-store/`（保留 WAL）。
3. 用新构建查看现状：`./bin/mothx doctor`（或 `go run ./cmd/mothx doctor`）→ 应出现 `sessions.content_store` 警告，列出 `95399bdbf08e4403` 等会话。
4. 修复：`./bin/mothx doctor --repair-session-content all`（或 `--repair-session-content 95399bdbf08e4403` 单条）。逐条输出 `adopted N row(s), retired M duplicate row(s)` / `[skip]`（他进程占用）/ `[error]`。
5. 复检：`./bin/mothx doctor` → `sessions.content_store` 应为 ok；用 TUI/ACP 打开该会话确认历史完整、可继续。
6. 之后正常启动；任何新老构建混跑都会重新引入风险，务必统一构建。

### 8.1 修复时发现的历史损失（不可恢复）

修复前 `entries` 中仅剩 seq 577-789（13:01-13:23），说明 12:37-12:47（run #5 尾段 + run #6）的行在本次修复之前就已被**旧构建的收敛删除**（旧二进制 `DeleteLegacyEntries` 会在任何 open 时清空该会话全部 payload），更早的 run #2-#4（11:36-12:18）同样早已丢失。因此：

- 事件日志现有内容 = run #1（11:18-11:33，原导出）+ run #7（13:01-13:02）+ run #8（13:02-13:23，本次采纳）；
- run #2-#6 的**消息正文**在 legacy 与事件两侧都不存在，无法从任何备份恢复（当时唯一的副本在调查过程中被 13:10 的再次拷贝覆盖）；
- 这些 run 的 `session_runs` 与 `conversation_turns` 行仍存在，边界仍指向已删除的 legacy seq；`repairUnmappedTurnBoundaries` 找不到对应 marker 时会跳过（不伪造边界），因此在那些 turn 上 fork 会 fail closed。**已补（2026-11-03）：** `mothx doctor` 新增 `sessions.turn_boundaries` 检查，通过 `session.ListUnmappedTurnBoundarySessions` 只读报告这类 turn；该检查只针对 `content_store = 'event_store'` 的会话（legacy 会话的边界是 `entries.seq`，本就没有 projection marker，不应报警）。检测按 turn 身份匹配，而不是按 seq 范围，因为一个 legacy seq 可能恰好等于某个无关的事件位置。命中的 turn 无法自动修复（内容已不存在），因此报告状态为 warn 并说明「仍可读取、但无法 fork」，而不是假装健康。

### 8.2 本机二进制更新（2026-11-02 02:32）

- `make install` → `/home/free/go/bin/mothx`（`v1.3.108-pre-1-1-g84637581-dirty`，构建于 02:32，含 P0+P1）。
- 校验：`strings` 命中 `content_store_flipped_at` / `legacy payload retirement is inside the post-flip grace window` / `content_store_legacy_rows_retired`；`mothx doctor --help` 含 `--repair-session-content`；从 PATH 运行 `mothx doctor` → `All checks passed`（`sessions.content_store` = ok）；`schema_migrations` 最高版本 54。
- Desktop 捆绑运行时已刷新（2026-11-02 02:44）：`desktop/vendor/mothx/bin/linux-amd64/mothx` 由当前源码重建，`strings` 命中同一组 P0/P1 标记，`doctor` 从该二进制运行返回 `All checks passed`。
- `make desktop-vendor` 的 `npm ci` 步骤在本机失败于 Electron 二进制下载（`electron/install.js` socket hang up）。已改为 `npm ci --ignore-scripts` 还原 623 个依赖，再用 `ELECTRON_MIRROR=https://npmmirror.com/mirrors/electron/ node node_modules/electron/install.js` 装上 Electron 39.8.10；`npm run typecheck` 通过。下次直接跑 `make desktop-vendor` 仍会 `npm ci`（会清空 node_modules），若 GitHub 不可达建议先 `ELECTRON_MIRROR=https://npmmirror.com/mirrors/electron/ npm ci`。
- `desktop/vendor/mothx/bin/mothx`（2026-09-10，旧扁平布局）已不被当前解析器使用（运行时按 `bin/<goos>-<goarch>/mothx` 探测），属陈旧构建产物；可按需删除。

## 9. 备注

- 调查阶段未修改生产代码；P0 修复（2026-11-02）只改 `internal/session`、`internal/dao`、对应测试与 `AGENTS.md` 的内容路由不变式，无 schema 变更，未改 `settings.json`/`serve.json`。
- 调查副本位于 `/tmp/opencode/session-investigate/`（含 `sessions.db*`、`wechat-sessions.db`、`seg.jsonl`），可安全删除但建议保留到 P1 修复完成。
- 后续若继续排查，请始终操作副本；live 库仍可能被旧构建 TUI 写入。
