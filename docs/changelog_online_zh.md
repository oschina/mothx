# 更新日志（当前版本）

本文件仅记录**当前版本**的变更。所有版本的完整历史见 [docs/zh/changelog.md](zh/changelog.md)。

## v1.3.103

### 💥 破坏性变更

- **移除入站 Webhook 支持。** Serve 的入站 webhook 功能（`channels.webhooks` 配置、`/webhook/*` 路由及其 agent 任务处理器）已彻底移除；不再接收 webhook 事件，现有配置文件中的 `webhooks` 段将被忽略。请改用 OpenAI 兼容 API 或微信/飞书消息通道。

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


### 🐛 问题修复

- **ACP 不再因默认供应商缺 key 而整体不可用**
  - 此前 `mothx acp` 在启动时把“默认供应商不可用（缺 API key / 未知供应商 / 无可用模型）”当作致命错误：进程直接以结构化启动错误退出，initialize、会话、管理面（`mothx/manage/*`）全部不可用，Desktop 等客户端连“配置供应商”这条路都走不通。
  - 现在 ACP 会正常启动并只提供一条 stderr 警告；管理、设置、会话历史、技能/知识库等全部可用，仅 `session/new`、`session/prompt` 等执行入口返回带 `code`/`fix` 的结构化 RPC 错误（如 `provider_unusable`：“default provider x has no API key”）。已绑定其它可用供应商的会话不受影响。
  - 管理面写入（`mothx/manage/providers/save`、`providers/delete`、涉及 `defaultProvider`/`defaultModel`/`providers` 的 `settings/patch`）会在运行中的进程内即时重建供应商目录：配好 key 后无需重启 ACP 即可开始执行。

- **Desktop 首页背景图大图不再静默不生效**
  - 选定背景此前以 base64 data URL 注入壳层 CSS 变量 `--app-user-image`，而 Chromium 会静默丢弃超过 2MiB 的 CSS 自定义属性值：大于约 1.5MB 的壁纸编码后超限，`background-image` 落空且无任何报错；首页 logo 走 `<img src>` 不受该限制，因此表现为“logo 生效、背景不生效”。
  - 渲染器现在把授权读取到的 data URL 转换为 `blob:` object URL 后再注入 CSS 变量（注入值始终是短字符串，图片字节以 Blob 驻留内存），并在背景替换或清除时 revoke 旧 object URL 避免泄漏；新增回归测试断言该转换与 revoke 路径。

- **上下文压缩不再以 "max iterations (1) exceeded" 失败**
  - 压缩摘要是通过一个 `MaxIterations: 1`（只允许一次 LLM 轮次）的子 Agent 生成的。agent loop 内的恢复重试（空响应重试、输出上限升级与续写、内容拒绝恢复、上下文溢出恢复、Responses 远端状态重放）每次都会重新发起一次供应商请求，却不消耗逻辑迭代计数，于是任何一次恢复都会用掉唯一的迭代，整个压缩以 `generate summary: max iterations (1) exceeded` 中止；而压缩失败后超大的上下文原样保留，错误便会在后续每一轮反复出现。
  - 这些恢复尝试现在不再消耗逻辑迭代预算——每条路径都保留自己的有界重试计数（空响应 2 次、输出续写 3 次、内容拒绝 2 个阶段）——因此 `MaxIterations` 现在表示"产出性 LLM 轮次"，摘要子 Agent 遇到空响应或截断也能正常恢复而不是报错终止。这与既有的传输层恢复规则以及"恢复不得消耗迭代预算"的共享原则保持一致。
  - 摘要子 Agent 的上限保持为 3，作为偶发"幽灵工具调用"轮次的安全余量（其工具集始终为空）；误导性的 `tool result summarization returned empty result` 错误文案也更名为 `summarization returned empty result`。
  - 新增回归测试覆盖：1 次迭代预算内的空响应与输出上限恢复、摘要子 Agent 在空响应后正常恢复。

- **Desktop 任务可并行运行与自由切换**
  - Desktop 之前用一个全局 `promptInFlight` 标志描述"有任务在跑"，因此任务 A 运行期间无法新建任务 B，新建任务输入区被禁用，点侧边栏其他任务只会得到"当前任务还在运行"，必须再点一次"新建任务"才恢复。Run 状态改为按会话投影（`runningSessions`）：后台任务不会让当前任务显示为忙碌、不再阻塞任务切换、也不会覆盖当前任务的运行状态；重新打开仍在运行的任务会保持"执行中"，而不会卡在加载态。
  - 流式内容的路由收敛为唯一规则——内容属于拥有该转录的会话——因此在另一任务流式输出期间新建的任务不会再把两段对话混进同一个视图，当前会话的失败也一定会写进自己的转录，而不是只变一下左上角状态点。
  - 发送改为单飞：回车连按（或按键重复）不再并发创建两个会话，也不会把同一条消息发两次。

- **Desktop 任务列表不再残留已删除的会话**
  - 删除任务后会先把该会话从所有已缓存投影中驱逐，再执行刷新；当新的列表请求正在进行时，排队中的那次请求会被串接执行，而不是复用仍在飞行中的旧响应，因此已删除（或已改组/重命名）的会话不会再留在展开的项目分支里。任务列表刷新失败现在会弹出提示，而不是悄悄把列表冻结在旧数据上。
  - `session/delete` 与 `mothx/session/delete` 变为幂等：删除一个已被移除的会话会成功返回，而不是报 `session "…" not registered in DB`。

- **任务输入区的供应商选择器只显示可用供应商并按使用排序**
  - Desktop 任务输入区此前忽略了 `mothx/manage/providers/list` 已经投影的 `apiKeyConfigured`，于是没有配置 API Key 的供应商与可用的供应商并列出现在新建任务与会话内的选择器里。现在两个选择器都只渲染 Runtime 报告为已配置的供应商；当投影里没有任何已配置供应商（或旧运行时没有该字段）时，菜单仍保留可选项而不会被清空。
  - 供应商顺序改为单一权威投影，取代原本写死的厂商优先级列表：`mothx/manage/providers/list` 按"默认供应商 → 最近一次实际请求 → 从未使用者的稳定目录优先级"排序，并为每个条目附加 `usageCount`/`lastUsedAt`。以最近活跃为主序，是因为刚添加的供应商通常也正是刚开始使用的那一个，而纯按请求次数会把新供应商永远埋在长期老 favourite 之后。Desktop 选择器直接消费该顺序，会话内选择器与新建任务选择器展示相同的优先供应商。

- **`mothx --continue` 能恢复最近真正使用过的会话**
  - `sessions.cwd` 此前按字节精确比较，因此通过 Desktop 目录选择器创建（记录磁盘上的真实大小写）的会话，在 shell 里用另一种大小写书写同一目录时就查不到，`mothx --continue` 会静默地续接了另一个会话。现在会话列表在 Windows 与 macOS 上按大小写不敏感匹配工作目录，在大小写敏感的文件系统上仍保持精确匹配。
  - "最近"此前指"最近创建"：`SessionInfo.ModTime`（所有列表都按"最后使用"语义消费它——TUI 的时间列、Serve 的 `LastUsed`、Desktop 的 `updatedAt`）填的却是创建时间。它现在承载最新一条持久化 entry 的时间，`CreatedAt` 单独保留创建时间，虚拟会话句柄仍以创建时间命名，会话身份保持不变。于是 `--continue` 会续接真正最近使用过的会话。
  - **新增 `mothx --list-sessions`。** 跨全部工作目录列举最近会话（ID、最后使用时间、消息数、工作目录、标题），用于找回从 Desktop、Serve 或消息渠道发起的对话。`--list-sessions-limit` 控制输出条数（默认 20），`--list-sessions-cwd` 可只看某个目录。

- **文档：说明 ChatGPT/Codex 订阅能否在 MothX 中使用**
  - 新增 FAQ 条目与配置章节：ChatGPT Plus/Pro 订阅**不能**直接接到 MothX。它由 Codex 内部后端（`chatgpt.com/backend-api/codex/responses`）提供服务、使用 ChatGPT OAuth access token 并由 OpenAI 控制刷新节奏，两者都与 MothX 的 provider 凭据模型（明文 / `${ENV}` / `!command`）不兼容，且把订阅接入第三方客户端不符合其服务条款。文档同时说明了为什么 Codex CLI 自身可以把 ChatGPT 登录态发往自定义 `base_url`，以及推荐的两条替代路径：使用按 token 计费的 OpenAI API（内置 `openai` provider 已按 Codex 模型系列配置好，含 Responses API 与 Codex User-Agent），或把 MothX 指向任意兼容 OpenAI Responses API 的自建/企业网关。
  - 配置文档新增「指向自建或网关端点」小节，说明 `api: "openai-responses"` + `baseUrl` 的网关用法、显式声明 `models`、凭据形式与 `openai-chat` 回退，以及用 `mothx doctor`/`mothx speedtest` 验证网关。

- **`make desktop-dev` 现在能被 Ctrl+C 彻底关闭**
  - dev runner 原本只在 Electron「先退出」的情况下关闭 Vite renderer 监听；Ctrl+C 时它已把自己标记为退出中，于是该分支被跳过 —— 监听一直开着，`npm run dev` 以及等待它的 `make` 进程永远不退出。现在所有退出路径（包含中断路径）都会关闭监听，并在退出前等待关闭完成，避免 `process.exit` 把关闭过程截断。
  - 关闭逻辑改为显式的、有界状态机，取代单次 `child.kill()`：第一次中断终止 Electron 并启动 4 秒宽限期，超时仍未退出则强制结束，再次按 Ctrl+C 则跳过等待立刻退出。spawn 失败也走同一条路径上报，而不是抛出未处理的 `error` 事件。退出码遵循 128 + 信号约定（130/143/129），让 `make` 正确报告中断。
  - POSIX 下 Electron 以独立进程组启动并按整棵树终止（向进程组发 `SIGTERM`/`SIGKILL`，Windows 用 `taskkill /T` 与 `taskkill /T /F`），因此 renderer/GPU/utility 子进程不再残留为无窗口后台进程。`SIGHUP`（关闭终端窗口）与其他中断一视同仁处理。

- **Desktop 开发运行器在缺少显示服务器时给出说明，而不是白屏**
  - 在没有可用显示服务器的 Linux 主机上（SSH 会话、X 服务已退出、X11 转发未连通），Chromium 会在平台初始化阶段直接退出并因 SIGSEGV 崩溃。由于窗口是无边框且背景为白色，用户能看到的现象只有一个白色窗口加一段段错误提示，而且还要先等完整构建。`npm run dev` 现在会先检查显示服务器，1 秒内打印可执行的说明并以退出码 1 结束（完全不做构建）；当 `DISPLAY` 有值但连不上时（环境检查无法预判的情况），改为识别 Chromium 自己的 stderr 报错并附加同一段说明。Chromium 原始诊断信息仍原样输出，说明中列出可行方案：本机真实桌面会话、`ssh -X` 转发、虚拟显示器（`Xvfb`，可再叠 VNC/x11vnc），或在无显示器主机上改用 TUI / `mothx serve` Web UI。

- **技能引用不再能逃逸技能目录**
  - 从 SKILL.md 内容解析的引用路径（`### 标签 (路径) [已加载]` 标题或 Markdown 链接）此前直接拼接到技能目录、没有围栏检查，第三方或 skillhub 安装的技能可以通过 `../../../…` 之类的引用把技能目录之外的任意 `.md`/`.txt` 文件自动注入系统提示词。现在解析出的引用与 `skill_ref` 按需加载执行相同的目录围栏检查，绝对路径与逃逸路径会被拒绝。

- **文件工具路径解析增加符号链接围栏**
  - `read`/`write`/`edit`/`insert`/`grep`/`find`/`ls` 的工作区围栏检查此前只做词法校验，工作区内指向会话根目录之外的符号链接可以通过检查，让未沙箱化的宿主进程读写围栏之外的文件。`ResolvePath` 现在会对目标路径与会话根目录做符号链接规范化（新建文件尚不存在的尾部路径也能正确处理），并在规范路径上重新做围栏检查。

- **SSE 流内错误事件现在纳入 provider 重试**
  - provider 在流中途发出带内错误事件/分片时（Anthropic 的 `event: error`（如 `overloaded_error`）、Google 的 `error` 分片（如 `UNAVAILABLE`、`RESOURCE_EXHAUSTED`）、OpenAI 兼容网关的 `{"error":…}` 分片），此前会立即终止 run——OpenAI chat 流甚至会静默忽略错误分片、把被截断的流当正常完成上报。现在这些错误会返回给调用方的重试分类逻辑：在尚未产生可见输出时按有界退避策略重试，否则作为普通流错误呈现。`IsRetryable` 同时新增了 `unavailable`、`resource_exhausted`、`internal error` 子串匹配。

- **修复 TUI 排队提示词误报“lease is held by another process”**
  - run 进入终态后，如果带围栏的终态持久化写入需要重试，Runtime 会（按设计）继续持有执行租约并在后台重试；此时 TUI 中排队的提示词会立即尝试下一次 admission，并被 `session runtime lease is held by another process` 拒绝——这实际是同进程的收尾窗口，却被误报为跨进程占用错误，排队的输入还会被丢弃。
  - 共享执行 admission 现在能区分“正在收尾”与“被占用”：如果 busy 的租约仍由当前进程持有、且该执行已选定终态，排队的后继请求（admission 与会话 mutation）会等待 Runtime 自行释放租约，会话因此保留在本进程内，排队输入正常执行。对同进程内真正活跃的执行或其他进程持有的租约，未开启等待的调用方仍然立即失败。
