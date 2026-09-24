# 知识库可用性完善方案

> Date: 2026-09-24 · Status: Proposed (product decisions locked 2026-09-24; Phase C0–C4 implemented, remaining gaps noted in §0) · Owner: Agent Runtime / MCP / Session / DAO / Desktop
> Related: `docs/proposal/desktop-knowledge-base-agent-proposal.md`, `docs/proposal/agent-core-runtime-unification-proposal.md`, `AGENTS.md`

## 0. 决策摘要

知识库的数据地基（一库一私有 SQLite、不可变快照、FTS 种子召回、有界图投影、canonical durable Run、Knowledge MCP 查询路径）已经落地，但离“用户能放心天天用”还差三段：**会被真实文档触发的阻断缺陷**、**索引覆盖与图谱/查询质量不足**、**管理面与调度不完整**。

本方案不新增 Runtime、不新增 Agent loop、不新增数据库方向。它只做三件事：

1. 修掉会让索引直接失败的确定性缺陷，并把配置校验前移到写入/扫描之前。
2. 补齐“可重建索引”的覆盖度与质量：格式发现、忽略规则、增量差异、实体归并与关系、二跳有界图查询、证据预算与不确定性。
3. 补齐“可运维”：索引运行历史与诊断、清除索引、来源浏览、预览查询、调度暂停/合并/失效，以及跨入口一致性。

所有改动仍落在既有 owner 上：`internal/agentruntime` 负责编排与角色、`internal/session` 负责领域 API 与事务、`internal/dao` 负责全部 SQL/FTS/图查询、`internal/acp`/`internal/serve`/Desktop/WebUI 只做投影。

### 当前实施进度

- **Phase C0（P0，已完成 2026-09-24）**：§2.1 同文件重复标签去重（`indexSourceFile`）+ 图投影去重（`ActiveGraphProjection`/`NodesForChunks` 改用 `IN (SELECT ...)` 消除重复行）；§2.2 WebUI 默认 thinkingLevel 改为 `off`，serve/acp 在 create/update 预校验 mode/thinking；§2.3 新增 `session.ErrKnowledgeBaseDisabled`/`ErrKnowledgeBaseRootUnavailable` 哨兵，适配层改用 `errors.Is` 映射。回归测试覆盖重复标题、重复代码注释、非法 mode/thinking、禁用库 409/`knowledge_base_disabled`。
- **Phase C1（索引覆盖，已完成 2026-09-24）**：§3.2 知识库级 `ignoreGlobs` + 根目录 `.gitignore`/`.mothxignore` 建议忽略（只读）+ `discovered/ignored/skipped` 发现诊断投影；§3.3 增量差异摘要（`added/modified/removed/unchanged`，写入 `knowledge_index_snapshots.diff_summary`，复用路径写入事件数据）；§3.1 跳过文件（超大/二进制/不可读）标记 `status=skipped` + 诊断，不交给模型；Indexer/Librarian 只读工具（`read`/`ls`/`grep`/`find`）绑定知识库根目录。存储 schema 升级至 v4。
- **Phase C2（查询与图谱质量，已完成 2026-09-24）**：§4.1 `knowledge_nodes.status ∈ {fact,candidate}` + `confidence`、`knowledge_entity_aliases` 归并文件 basename 别名、确定性可复核的 `references` 边（Markdown 链接）、`tested_by`（代码文件名约定）与 `imports`（Go/Python/JS import 语句）边、`declares`（代码符号声明，取代泛化的 `contains`）、`configured_by`（代码文件引用配置文件名）、`calls`（同文件内符号调用）、`defines`/`requires`（documents 术语定义与 RFC 2119 要求）、`supersedes`（documents 取代关系）、Indexer 产出 candidate 实体（标签需在引用 chunk 中本地可复核，无引用则落无证据 candidate，默认查询不返回）；§4.2 二跳有界查询（≤2 hops、≤64 节点、≤96 边）+ 关系权重/证据数/查询命中排序 + `uncertainties[]` + `truncated`，无证据 candidate 不入结果；§4.3 MCP 结果每条摘录补充 `relationType`/`confidence`，并投影 `uncertainties`/`truncated`。
- **Phase C3（管理与调度，已完成 2026-09-24）**：§5 ACP `mothx/manage/knowledge-bases/{runs/list,runs/get,clear,sources/list,schedule}` + HTTP `/api/knowledge-bases/:id/{runs,runs/:runId,clear,sources}`，新增能力键 `knowledgeRuns`/`knowledgeSources`；来源投影含每文件分块数（`chunkCount`），`schedule` 投影复用共享 Cron store（下次运行/上次结果/暂停恢复，暂停即持久化 `enabled=false` 并删除 cron job）；§6 根目录不可用时主动失效 cron 计划（禁用/暂停仍由 `enabled` 表达，即删除该库 cron job 并保留配置），同库并发触发复用同一后台 job；§7 WebUI 不再强制 `schedule=manual`/`thinkingLevel=none`（回显真实 cadence、保存保留 Desktop 计划、提交 ignoreGlobs），新增 `internal/architecture` 迁移桥护栏（`KnowledgeCapsule`/`WithKnowledgeContext`/`knowledgeBaseRefs` 仅允许 Runtime owner 与 `internal/acp/acp.go`）。
- **Phase C3 渲染层（已完成 2026-09-24）**：WebUI `Knowledge.svelte` 新增索引历史/来源浏览/清除索引面板与 `schedule`/`ignoreGlobs` 编辑；Desktop `KnowledgePanel.tsx` + `core/manage-api.ts` 接入 `runs/list`、`runs/get`、`sources/list`、`clear`、`schedule`，按 `knowledgeRuns`/`knowledgeSources` 能力键 gate，附双语文案与 Desktop 投影测试。
- **Phase C4（质量回归，已完成 2026-09-24）**：已更新 `docs/en|zh/changelog.md` 与 `docs/changelog_online_*.md`（v1.3.104）。新增受控基准：`internal/agentruntime/knowledge_benchmark_test.go` + `scripts/knowledge-benchmark.sh`（`make knowledge-benchmark`），在确定性 40 文件夹具上产出并断言基线指标（索引时长、MCP 查询 p50/p95、tool-result 字节数、引用覆盖率、不确定性覆盖率、token 估算），阈值见下。
- **剩余债务**：PDF/Office 提取器（首版明确不做，允许 Agent 只读工具读取）；计划状态 HTTP 投影复用现有 `/api/cron`（WebUI 不新增知识库专属 schedule 端点）。
- **未完成 / 与方案有偏差（已核对代码）**：
  - debounce/coalesce：已实现**合并（coalesce）**——扫描进行中到达的触发不再丢失，而是合并为恰好一次后续扫描（最新 source 胜出）；基于时间窗的 debounce 未做（§6）。
  - mtime 快路径**已决定不做**（§3.3）：安全实现无收益、不安全实现会提供过期知识；复用继续以 content hash 为权威。
  - §9 Phase C1 原计划的“可插拔提取器（PDF/Office/HTML）”已按 §3.1/§12 决策搁置；§9/§10 已据此更正。
- **补充修复（2026-09-24）**：索引期 provider/model 错误映射为稳定错误码 `knowledge_base_model_unavailable`（写入失败 Run 的 `ErrorInfo.code`，由 `KnowledgeIndexRun.errorCode` 投影；ACP/HTTP 同步路径亦映射）。

#### C4 基线指标与回归阈值

基线由 `make knowledge-benchmark`（或 `scripts/knowledge-benchmark.sh`）在 CI 或本地复现，报告写入 `knowledge-benchmark.json`。阈值刻意宽松以避免共享 CI 抖动，仅在记录新基线时收紧。

| 指标 | 阈值 |
| --- | --- |
| 索引时长 `indexDurationMs` | < 60000 |
| MCP 查询 `queryP95Ms` | < 5000 |
| tool-result 字节 `toolResultBytesMax` | ≤ 4800 + 4096 |
| 引用覆盖率 `citationCoverage` | = 1.0 |
| 不确定性覆盖率 `uncertaintyCoverage` | ≥ 0.1 |
| token 估算 `tokenEstimate` | < 200000 |

## 1. 现状盘点与可用性差距

> 说明：本节记录方案编写时的**修复前基线**，用于说明差距。其中“现状”描述已被 C0–C4 改变（如 schema 3→4、1 跳→≤2 跳、无 glob/无 run 历史→已具备等），请以“当前实施进度”为准。

### 1.1 已具备

- 配置对象（名称/根目录/profile/provider/model/mode/thinking/schedule/enabled）与活动快照 ID。
- 每知识库私有 SQLite：`sessionDir/knowledge-bases/<id>.db`，含配置、快照、文件、chunk、节点、边、evidence、FTS5；schema 版本 3，`config_revision` 围栏。
- 确定性索引：文件发现、路径/符号链接逃逸防护、稳定分块、标题/代码声明节点、`contains` 边、无变化快照复用、按文件子图克隆的增量重建、活动快照原子切换 + 旧快照清理。
- 受验证 `co_mentions` 边：Indexer 只提议、Runtime 二次校验（ID/类型/同现文本/span 范围/去重）。
- 查询：FTS 种子 → 命中 chunk → 邻接节点 → 邻接边（1 跳），结果有界。
- Knowledge MCP：`search_knowledge_base`，allowlist 授权，输出带 citation、有硬预算。
- 管理面：ACP `mothx/manage/knowledge-bases/*`（list/get/create/update/delete/scan/status/query/mcp/apply）、Desktop 设置面板、WebUI 知识库视图；手动 + Cron 扫描复用 canonical Run。

### 1.2 差距清单

| 维度 | 现状 | 可用性缺口 | 优先级 |
| --- | --- | --- | --- |
| 索引正确性 | 同名节点唯一键冲突会使整次索引失败 | 重复标题/重复 `#` 注释即可触发；无回归测试 | P0 |
| 配置校验 | thinkingLevel 只在扫描时才校验 | WebUI 默认 `none` 非法；坏配置可落库 | P0 |
| 查询质量 | 仅 1 跳、按 bm25+ordinal 排序 | 无 2 跳、无按关系类型/证据数/新鲜度排序、无不确定性字段 | P1 |
| 图谱语义 | 仅 file/section/symbol + contains/co_mentions | 无实体归并、别名、语义关系、candidate/status | P1 |
| 文件覆盖 | 仅白名单文本扩展名 | PDF/Office/HTML 正文不可用；改为允许 Agent 用本地只读工具读取（§3.1） | P1 |
| 忽略规则 | 固定目录 + 扩展名白名单 | 无用户 glob、不读 `.gitignore`、忽略结果无诊断 | P1 |
| 增量 | 全量 hash 比较 | 无 mtime 快路径、无差异诊断、无变更清单投影 | P2 |
| 管理面 | 配置 + 聚合统计 + 实时进度 | 无 run 历史、无失败诊断、无清除索引、无来源浏览、无预览查询 | P1 |
| 调度 | manual/hourly/daily/weekly/monthly/cron | 无 debounce/coalesce、无暂停/恢复、根目录失效不主动失效计划 | P2 |
| 跨入口一致性 | WebUI 强制 schedule=manual、thinkingLevel=none | 从 WebUI 保存会静默降级 Desktop 计划；默认值非法 | P0/P1 |
| 迁移桥 | `KnowledgeCapsule`/Librarian/`knowledgeBaseRefs` 仍可调用 | 无架构护栏、无删除进度 | P1 |
| 观测/回归 | 无 | 无 p50/p95、token、引用覆盖率、不确定性覆盖率基线 | P2 |

## 2. P0：阻断性缺陷修复（必须先做）

### 2.1 同文件重复标签导致索引失败

`internal/agentruntime/knowledgebase.go` 的 `indexSourceFile` 对每个 marker 生成一个节点，`NormalizedLabel = normalizeKnowledgeLabel(path + "\x00" + label)`；而 `knowledge_nodes` 有 `UNIQUE(snapshot_id, kind, normalized_label)`（`internal/session/migrations.go`）。同一文件里两个同名标题（如两段 `## Example`）或 `code` profile 下重复的 `# TODO` 注释会产生两条相同唯一键节点，`InsertNodes` 触发约束错误，整个快照事务回滚，索引 Run 失败。

修复（Runtime 侧，无 schema 变更）：

- 在 `indexSourceFile` 内维护 `map[(kind, normalizedLabel)]nodeID`，同一文件同标签只建一个节点；每个出现位置仍生成自己的 evidence（`node` 级 + `edge` 级），全部指向该唯一节点。
- `contains` 边去重：`(fromNodeID,toNodeID,relationType)` 已由唯一键约束，去重后再 append。
- 不改变 `knowledge_nodes` 唯一键语义（一个快照内同 kind 同规范化标签就是一个实体）。

回归测试：一个 markdown 含两段 `## Example`、一个 shell 含两行 `# TODO`，索引必须成功，节点数不重复，evidence 数等于出现次数。

### 2.2 配置校验前移，修 WebUI 默认 thinkingLevel

- `ui/src/lib/knowledge-base.js` 的 `DEFAULT_THINKING_LEVEL` 由 `'none'` 改为 `'off'`（或空串，空串由 Runtime 归一为 `ThinkingMedium`）。
- `internal/serve/knowledge_bases.go` 的 `validateWebKnowledgeBaseSpec` 与 `internal/acp/manage_knowledge_bases.go` 的校验补上：`provider/model` 成对、`mode ∈ {yolo,agent,plan,os}`、`thinkingLevel` 可用 `agentruntime.ValidateThinkingLevel` 预校验（空串合法）。
- Runtime 侧 `resolveKnowledgeIndexer` 在索引时校验 provider/model/thinking；创建/更新期由适配层预校验并返回 `knowledge_base_model_invalid`/`knowledge_base_thinking_invalid`；索引期 provider/model 不可用映射为稳定错误码 `knowledge_base_model_unavailable`（写入失败 Run 的 `ErrorInfo.code`，由 `KnowledgeIndexRun.errorCode` 投影，ACP/HTTP 同步路径亦映射）。

回归测试：WebUI 默认 payload 创建 + 配置 provider/model 后扫描成功；非法 thinkingLevel 在 create/update 时即被拒绝。

### 2.3 错误分类改为哨兵错误

`internal/serve/knowledge_bases.go`、`internal/acp/manage_knowledge_bases.go` 目前用 `strings.Contains(err.Error(), "is disabled")` 判码。改为 `internal/session` 暴露 `ErrKnowledgeBaseDisabled`、`ErrKnowledgeBaseRootUnavailable` 等哨兵，适配层用 `errors.Is` 映射到结构化错误码。

## 3. 索引覆盖与预处理

### 3.1 文档读取：允许 Agent 使用本地只读工具（已确认）

现状只读取 UTF-8 文本，`documents` profile 对 PDF/Office 不可用。**产品决策：首版不内置 PDF/Office 提取器，允许索引/查询 Agent 调用本地只读工具读取源文件。**

- 保留 Runtime 侧的 UTF-8 直读作为确定性基线，保证文件/chunk 快照不依赖模型。
- 在只读策略内允许 Indexer/Librarian 使用**既有**本地只读工具（`read`/`ls`/`grep`/`find`）读取知识库根目录内的文件。工具根始终绑定知识库根目录，`yolo` 不穿透只读策略，不允许写/删/网络/委派。
- 新增本地文本转换工具（如 PDF/Office 转换）**暂搁置**，不在首版范围。
- 无法用现有工具解析的文件（加密/二进制/超大）：文件级 `status=skipped` + 诊断，**不**交给模型，不使旧快照失效。
- 若后续确需确定性全文索引 PDF/Office，再以**可选** Runtime 提取器形式追加，并把提取器版本纳入快照复用比较；首版不做（见 §11）。

### 3.2 忽略规则与发现诊断

- 支持知识库级 `ignoreGlobs`（spec 字段），与固定忽略目录合并；默认仍忽略 `.git/.mothx/node_modules/vendor/dist/build/coverage` 等。
- 读取根目录 `.gitignore`/`.mothxignore` 作为建议忽略（首版只读不写，命中项进诊断）。
- 发现阶段产出 `discovered / ignored / skipped` 三类清单，随快照存最小投影（相对路径 + 原因），管理面可查，避免“静默不索引”。

### 3.3 增量差异诊断

- 扫描先与活动快照比较，产出 `added/modified/removed/unchanged` 四类计数与清单，写入 `knowledge_index_snapshots`（新增 `diff_summary` JSON 列）。
- 全未变化时保持现有“复用快照 + `knowledge_snapshot_reused` 事件”，但补充差异摘要（全 0）。
- mtime 快路径**已决定不做**：安全实现（mtime+size 命中仍需 content hash 复核）不会减少任何 I/O；不安全实现（凭 mtime 跳过读取）会在 mtime 被保留（`cp -p`、`git checkout` 等）时把陈旧内容当作未变，违背“不提供过期知识”。因此复用判定继续以 content hash 为唯一权威。

## 4. 图谱与查询质量

### 4.1 实体归并、别名与关系（扩展现有验证框架）

- 引入 `knowledge_nodes.status ∈ {fact, candidate}` 与 `confidence`；无 evidence 的模型断言只能落 `candidate`，默认查询不返回。
- 增加 `knowledge_entity_aliases(snapshot_id, normalized_alias, node_id)`（proposal 已规划），用于把同义标签归并到同一实体；归并需有 evidence 支持，冲突保留多条。
- 在 `co_mentions` 之外，按“先定义 schema、再加样本回归”的顺序逐步开放 `defines/requires/references`（documents）与 `declares/calls/imports/configured_by/tested_by`（code）；每种关系都必须有 Runtime 侧可本地复核的判定规则（同 chunk/同文件/span 范围），不能直接信任模型 JSON。**§4.1 列出的关系已全部落地：`references`、`tested_by`、`imports`、`declares`、`configured_by`、`calls`（code）与 `defines`（术语定义）、`requires`（RFC 2119 要求）、`supersedes`（取代）（documents），以及 candidate 实体。§5.1 档案表里更偏推断的 `contradicts/supports/related_to` 仍为规划项。**
- 归并/别名/关系升级必须带 `schema_version` 递增与迁移；旧快照可读。

### 4.2 二跳有界图查询

`internal/dao` 的 `ActiveGraphProjection` 当前只做 1 跳，且 nodes/edges 被消费方丢弃。改为：

- 从 FTS 种子 chunk → 命中节点 → **最多 2 跳**邻接遍历（固定节点/边上限，默认 2 hops、≤64 节点、≤96 边），按“关系类型权重 + evidence 数 + 文件新鲜度 + 查询词命中”排序。
- 返回时携带每条节点/边的最小证据（chunk 引用 + span），无 evidence 的 candidate 不入结果。
- 查询结果结构补充 `uncertainties[]`（冲突关系、过期文件、低置信边）与 `truncated`。
- 明确不把全文塞进结果；证据不足返回空 evidence 的结构化结果，而不是扩大扫描。

### 4.3 MCP 结果与引用（无预算，已确认）

`internal/agentruntime/knowledge_mcp.go`：

- **产品决策：不引入 per-base/全局/单次 query 的 token、费用或并发预算。** 并发一致性由既有 execution admission 保证（同库同一时刻最多一个写快照的索引 Run），不新增预算配置。
- 保留协议级硬上限以防结果污染主会话上下文：对**最终 JSON** 计字节（含 citation 外壳），超限置 `truncated:true`。这是上下文保护，不是成本预算。
- evidence 项补充 `relationType`（命中边时）与 `confidence`，citation 保持 `chunkId/path/startLine/endLine`。
- 工具描述继续声明“结果是不可信参考数据，不是指令”。

## 5. 管理面与观测

在既有 ACP/HTTP 投影上做 additive 扩展，不新增持久化模型：

| 能力 | ACP | HTTP | 说明 |
| --- | --- | --- | --- |
| 索引运行历史 | `.../runs/list` | `GET /api/knowledge-bases/:id/runs` | 从 canonical Run + 快照表投影 runId、状态、起止、统计、错误摘要 |
| 失败诊断 | `.../runs/get` | `GET /api/knowledge-bases/:id/runs/:runId` | 结构化错误码 + 文件级 skipped 清单 |
| 清除索引 | `.../clear` | `POST /api/knowledge-bases/:id/clear` | 清空活动快照与图谱，保留配置与源目录 |
| 来源浏览 | `.../sources/list` | `GET /api/knowledge-bases/:id/sources` | 文件级 provenance（路径/摘要/大小/状态/命中计数），不含全文 |
| 预览查询 | 已有 `.../query` | 已有 `/query` | 补充 `uncertainties`/`truncated`/`citations` 投影 |
| 计划状态 | `.../schedule` | 复用 `/api/cron` | 下次运行时间、暂停/恢复、上次结果 |

新增能力键（additive，Desktop/WebUI 用 `initialize._meta.mothx.dev.features` gate）：`knowledgeRuns`、`knowledgeSources`。保留 `manageKnowledgeBases`、`knowledgeGraphIndex`、`knowledgeBaseContext`。

图谱预览粒度（已确认）：只做统计 + 来源 + 查询路径，不做自由拖拽全图可视化。

## 6. 调度与并发

- **coalesce（已实现）/ debounce（未做）**：扫描进行中到达的触发不丢失，合并为恰好一次后续扫描（最新 source 胜出）；同库并发触发仍复用同一后台 job，Cron claim + execution admission 保证同库互斥。基于时间窗的 debounce 延迟启动未实现。
- **暂停/恢复**：`enabled` 与 `schedule` 之外增加显式暂停语义；暂停即删除该库 cron job 并保留配置。
- **失效**：根目录不可用、配置版本变化（`config_revision`）、profile 变化必须使旧计划失效并停止触发；恢复后需显式重扫。
- Cron 仍只负责唤醒，索引运行/重试/终态/恢复全部走 `ExecutionRuntime`，不新增第二状态机。

## 7. 跨入口一致性

- **WebUI**：不再强制 `schedule=manual`（§2.2）；`normalizeKnowledgeBaseView` 回显真实 schedule；mode/thinking 选项与 Runtime 允许集一致；若 WebUI 暂不拥有调度器，则更新时保留原 schedule 而不是覆盖。
- **ACP/Desktop**：capability 新增键按 §5 gate；配置表单不保存密钥。
- **迁移桥移除**：`KnowledgeCapsule` / `WithKnowledgeContext` / `knowledgeBaseRefs` / Librarian 仅作兼容。删除条件：Desktop 已发布 MCP 配置路径、ACP 跨进程 MCP contract test 覆盖创建/加载会话、受支持客户端均不再发送该字段。满足后在 `internal/architecture` 增加护栏禁止新调用方，并移除该路径与字段。
- 所有入口只通过标准 `mcp.json`/ACP `mcpServers` 连接同一个 Knowledge MCP，不复制查询实现。

## 8. 安全与隐私（保持并强化）

- 文档内容始终是不可信数据；Indexer/Librarian 角色说明、MCP evidence schema、主 Agent tool-result 防护三者共同阻断 prompt injection。
- 工具能力保持只读且绑定知识库根目录；`yolo` 不穿透只读策略。
- 发现/忽略/跳过结果对用户可见；不把源文件复制进附件库，不把整库文本写入 transcript。
- 诊断只保留错误摘要与脱敏信息，不落模型原始 chain-of-thought。

## 9. 里程碑与验收

### Phase C0（P0，阻断修复）

1. §2.1 重复标签去重 + 回归测试。
2. §2.2 校验前移 + WebUI 默认 thinkingLevel 修正。
3. §2.3 哨兵错误分类。

验收：含重复标题/注释的真实目录可成功索引；WebUI 默认配置 + provider/model 可成功扫描；非法配置在 create/update 即被拒。

### Phase C1（索引覆盖，已完成 2026-09-24）

1. §3.1 文档读取：首版不内置提取器，允许 Agent 用既有本地只读工具读取；不可解析文件标记 `skipped` + 诊断（PDF/Office 提取器已搁置，见 §3.1/§11）。
2. §3.2 忽略规则 + 发现诊断。
3. §3.3 增量差异摘要（mtime 快路径已决定不做）。

验收：一个含 md/代码/不可解析文件的目录可建立带 provenance 的快照；忽略/跳过项可见；二次扫描对未变文件不重建。

### Phase C2（查询与图谱质量，已完成 2026-09-24）

1. §4.1 candidate/status + 别名归并 + `references` 关系（其余关系仍为规划项）。
2. §4.2 二跳有界查询 + 排序 + `uncertainties`。
3. §4.3 MCP 预算与引用增强。

验收：语义问题可经 2 跳取到带 citation 的证据；伪造节点/越界 span/无同现文本的模型断言被拒；结果有硬预算且标 `truncated`。

### Phase C3（管理与调度，已完成 2026-09-24）

1. §5 run 历史/诊断/清除索引/来源浏览/预览查询 + 能力键。
2. §6 暂停/失效 + coalesce（时间窗 debounce 未做）。
3. §7 跨入口一致性 + 迁移桥护栏。

验收：Desktop/WebUI 可查看索引历史与失败诊断、清除索引、浏览来源；暂停后不再触发；从 WebUI 保存不破坏 Desktop 计划。

### Phase C4（质量回归，已完成 2026-09-24）

1. 受控基准库 + 回归测试：索引时长、MCP 查询 p50/p95、tool-result 大小、引用覆盖率、不确定性覆盖率、token 使用。
2. 更新 `docs/en/changelog.md`、`docs/zh/changelog.md`（全量）与 `docs/changelog_online_*.md`（当前版本）。

验收：指标可在 CI 或本地基准脚本复现；回归阈值写入文档。

## 10. 测试矩阵

| 范围 | 必测行为 |
| --- | --- |
| DAO / session | 重复标签去重、别名/邻接索引、快照隔离、差异摘要、清除索引不删源目录、迁移（schema v3→v4）可重试 |
| Agent Runtime / MCP | 只从 Runtime 构建 Indexer/Librarian；无变化复用、部分变化子图克隆；MCP allowlist、`truncated`、`uncertainties`、candidate 过滤 |
| 文档读取 | 不可解析/加密/超大文件标记 `skipped` + 诊断（首版无内置 PDF/Office 提取器，已搁置） |
| Run / recovery | 索引/查询 run 经 `ExecutionRuntime` 终态化；取消/崩溃/恢复；同库互斥；旧快照可读 |
| ACP wire | 新能力键 gate、runs/sources/clear 方法、结构化错误码、标准 `mcpServers` 连接 |
| Desktop / WebUI | 表单不存密钥、不覆盖 schedule、能力未支持时占位、不读取索引/不拼 prompt |
| 跨入口回归 | ACP 可显式调用；TUI/CLI/Channel 无知识库分支；通用输入/Run/附件契约不变 |
| 安全 | 路径逃逸、符号链接、隐藏凭据、恶意文档指令、超大文件、过期快照、跨库访问 |

实现阶段至少运行：`go test ./internal/agentruntime ./internal/session ./internal/dao ./internal/acp ./internal/serve ./internal/architecture`，并为 Desktop 运行 typecheck/build 与 ACP smoke；涉及生产构造/输入/持久化/shutdown 调用点时补充跨入口 contract tests。

## 11. 明确不做

- 不新增 `knowledge` Agent mode、知识库专用 Agent loop、TUI/CLI/Channel 隐式检索。
- 不引入外部向量库/云端索引；若未来需要语义召回，只能作为可选、可替换的种子召回器，不成为事实权威。
- 首版不内置 PDF/Office 提取器；文档读取允许 Agent 调用本地只读工具（§3.1）。
- 不把整库文本/源文件复制进附件或 transcript；不通过扫描工作目录推断制品。
- 不新增 adapter-local SQL、run 状态机、prompt 拼接或第二份配置模型。
- 不以“文档提到过”推断事实；无 evidence 的模型断言只能落 candidate。

## 12. 产品决策（已确认 2026-09-24）

1. **文档读取**：允许 Agent 调用本地只读工具读取源文件；首版不强制内置 PDF/Office 提取器（§3.1）。新增本地文本转换工具暂搁置。
2. **预算**：无预算。不引入 per-base/全局/单次 query 的 token、费用或并发预算；并发一致性由 execution admission 保证。MCP 结果的协议级硬上限（`truncated`）**保留**，作为上下文保护而非成本预算（§4.3）。
3. **provider/model**：索引与查询继续共用同一配置，且每个知识库可各自使用不同渠道与模型；暂不引入 `indexProvider/indexModel` 覆盖。
4. **引用语义**：`required` 保持软依赖，不硬阻断主 Run；失败以结构化结果或不注入呈现。
5. **图谱预览**：统计 + 来源 + 查询路径即可，不做全图可视化（§5）。

## 13. 与既有方案的关系

本方案是 `desktop-knowledge-base-agent-proposal.md` 的收敛与补全：该文档定义边界与数据模型（K0–K3），本方案给出把“已实现”推进到“可用”的具体缺陷修复、覆盖度、质量、管理面与验收。两者不冲突；若冲突，以本方案的“不新增 Runtime/不新增数据库方向”约束为准，并在实施时回写更新原方案的状态段。
