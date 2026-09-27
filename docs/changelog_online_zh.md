# 更新日志（当前版本）

本文件仅记录**当前版本**的变更。所有版本的完整历史见 [docs/zh/changelog.md](zh/changelog.md)。

## v1.3.103

### ✨ 新功能

- **知识库可用性完善**
  - **忽略规则与发现诊断。** 知识库新增 `ignoreGlobs` 列表，与固定忽略目录以及从源目录根读取的 `.gitignore`/`.mothxignore` 建议规则（只读）合并生效。每次扫描会在快照上记录有界的发现投影（`discovered`/`ignored`/`skipped` 及逐路径原因），因此“静默未索引”变为可解释。超大、二进制/非 UTF-8 或不可读的文件标记为 `skipped` 并给出原因，且不会交给模型。
  - **增量差异摘要。** 每次重建的快照都会持久化 `added`/`modified`/`removed`/`unchanged` 投影（含有限路径样本）；完全未变化的扫描保留既有的快照复用事件，并在事件数据中携带全 0 差异。
  - **图谱与查询增强。** `knowledge_nodes` 新增 `fact`/`candidate` 状态与 `confidence`；新增 `knowledge_entity_aliases` 表把文件节点的 basename 与其路径标签归并为同一实体；Markdown 链接产生确定性、可本地复核的 `references` 边，代码文件的约定测试文件与其 import 语句分别产生确定性 `tested_by` 与 `imports` 边，代码符号声明则产生 `declares` 边（取代泛化的 `contains`），引用已索引配置文件的代码文件产生 `configured_by` 边，同一文件内调用已声明符号的符号产生 `calls` 边，文档中的术语定义与 RFC 2119 要求分别产生确定性 `defines`/`requires` 边，声明取代另一份已索引文档的文档产生 `supersedes` 边。查询现在最多做两跳有界遍历（≤64 节点、≤96 边），按关系类型权重、证据数、查询命中排序，剔除无证据的 candidate，并返回结构化 `uncertainties` 与 `truncated`。Indexer Agent 还可以提议 candidate 实体：标签若在引用的 chunk 中字面出现则落为有证据的 `candidate` 节点（永不成为 fact），否则落为无证据 candidate 并在默认结果中隐藏。Knowledge MCP `search_knowledge_base` 结果每条摘录新增 `relationType`/`confidence`，并投影 `uncertainties`/`truncated`。
  - **管理面。** 新增可加性的 ACP 方法与 HTTP 端点：canonical 索引 Run 历史（`runs/list`、`runs/get`）、文件级来源浏览（`sources/list`，不含正文，含每文件分块数）、清除索引（`clear`，保留配置与源目录）、计划状态投影（`schedule`，复用共享 Cron store 提供下次运行/上次结果与暂停恢复）。新增能力键 `knowledgeRuns`、`knowledgeSources` 用于 Desktop 面板 gate。当知识库根目录不可用时，旧计划会被主动失效。
  - **稳定的索引错误码。** 当索引无法构建所配置的 Indexer provider/model 时，失败 Run 的结构化错误信息会记录稳定错误码 `knowledge_base_model_unavailable`，并在 run 历史中以 `errorCode` 投影；ACP/HTTP 管理面也映射同一错误码，而非泛化的操作失败。
  - **扫描触发合并。** 扫描进行中到达的重索引触发不再被丢弃：它会被合并为恰好一次后续扫描（最新触发来源胜出），而不是启动并行扫描或每个触发排一次运行。基于时间窗的 debounce 未实现。
  - **Desktop 与 Web UI 面板。** Desktop 知识库面板与 Web UI 知识库视图新增索引历史、来源浏览与清除索引，按 `knowledgeRuns`/`knowledgeSources` 能力键 gate；Web UI 同时暴露 schedule 与忽略 glob 字段。
  - **跨入口一致性。** Web UI 不再强制 `schedule=manual` 或 `thinkingLevel=none`：它回显真实 cadence，保存时保留既有 Desktop 计划，并提交忽略 glob。新增架构护栏把遗留的知识上下文迁移桥（`KnowledgeCapsule`/`WithKnowledgeContext`/`knowledgeBaseRefs`）约束到 Runtime owner 与唯一的 ACP 调用方，并写明删除条件。
  - **质量基线。** 新增受控基准（`make knowledge-benchmark`）在确定性夹具上索引，并对索引时长、MCP 查询 p50/p95、tool-result 大小、引用覆盖率、不确定性覆盖率与 token 估算施加文档化回归阈值。
  - 知识库存储 schema 版本升级至 4；旧快照仍可读（新增列带安全默认值），并在下次扫描时重建。

- **Context guard 保留被省略的超大工具输出**
  - 当请求即将超出模型输入预算时，context guard 省略超大 tool result 不再永久丢弃内容：完整输出会先保存到 `<workDir>/.mothx/tmp/context-guard/`（私有 `0700`/`0600` 权限，沿用 `.mothx/tmp/inputs` 的暂存约定），guard 消息中附带文件路径与总行数/字节数，并明确引导模型用 `read` 的 offset/limit 分页读取保存的文件或用 `grep` 检索，而不是重跑可能非幂等的命令（部署、迁移、一次性脚本）。
  - 无法保存时（无工作目录、写入失败）回退为原有的“仅省略”文案；暂存区按最新 50 个文件尽力滚动清理；guard 的状态事件同时展示保存路径。

- **桌面客户端内置 MothX 运行时，并支持选择运行时二进制**
  - **所有桌面包都自带可运行的 `mothx` CLI。** 桌面发布构建会为每个打包架构（macOS arm64 与 x64、Windows/Linux x64）各构建一份源码版运行时，并在打包后、macOS 签名封存前把与目标架构匹配的二进制注入到 `<resources>/app/vendor/mothx/bin/`。此前 macOS x64 包里被塞进去的是构建机的 arm64 运行时，Intel Mac 客户端根本无法启动 ACP 运行时；现在打包步骤会校验内置二进制的 ELF/Mach-O/PE 架构（含小端与 fat Mach-O 头），不匹配就直接构建失败，而不是发布坏包。Windows/Linux 产物中也不再在应用目录旁重复附带一份运行时副本。
  - **运行时二进制设置。** 桌面设置（运行时 → MothX 运行时二进制）可在内置 `mothx` 可执行文件（默认）与自定义 `mothx` 二进制路径之间切换。选择与校验都在特权的 Electron 主进程完成，切换后自动重启 ACP 运行时；自定义路径不可用时回退到内置运行时并给出可见提示，自定义二进制无法启动时回滚选择，保证客户端始终可用。`MOTHX_BINARY` 环境变量仍是优先级最高的开发覆盖项。
