# 更新日志
## v1.2.100

### ✨ 新功能

- **独立的制品发布开关**
  - TUI/CLI、WebUI/API、Desktop/ACP 与消息渠道现在分别拥有制品发布开关，且全部默认关闭。每个入口只启用共享 Runtime 托管的 `publish_artifact` 路径，不会联动其他入口。

- **TUI、WebUI 与 Desktop 全端主角团**
  - 会话可通过 `--expert <id>` 或 TUI `/expert list|show|bind|unbind|switch` 命令绑定可复用主角包。团队包会注入 lead 身份与名册并自动启用成员调度；单人包只改变 lead 身份。
  - 替换已绑定主角会创建分叉，而不会覆盖源会话。WebUI 主角面板和 Desktop ACP 的 **Expert** 选项复用同一条 Runtime 绑定与分叉路径。
  - 成员生命周期投影现在携带成员名称、emoji、角色和主角身份，供 TUI、WebUI 与 Desktop 卡片展示。成员完成会在 lead 的边界投递，绝不会自行启动新 run。
  - ESM 仅在目标可运行且会话真正空闲时续跑；待处理输入、决策或成员终态事件都不能绕过该 gate。
  - 新增内置 `expert-creater` Skill：通过 TUI/WebUI 的 `/skill expert-creater` 或 Desktop/ACP 的 `/expert-creater` 启用后，当前 Agent 会在项目 `.mothx/experts/` 中创建并安装经校验的主角团。

- **WebUI：聊天输入框斜杠命令建议**
  - 在聊天输入中键入 `/` 即弹出建议下拉框，覆盖全部支持的斜杠命令（`/clear`、`/mode`、`/model`、`/defaultModel`、`/models`、`/sessions`、`/status`、`/compact`、`/delegate`、`/alloweditpath`、`/allowautoedit`、`/workflows`、`/skill`、`/skills`、`/rule`、`/esm`、`/help`），并对 `/esm` 提供专门的子命令过滤（objective/edit/pause/resume/clear/guide）。
  - 使用 ↑/↓ 导航，Tab 或 Enter 补全（当输入与选中项完全一致时 Enter 直接发送），Esc 关闭，或点击选中；选中后光标定位到插入命令的末尾。输入框保持完整的 combobox/listbox 无障碍状态（`aria-expanded`、`aria-activedescendant`、`aria-selected`）。
  - 运行进行中、API 被禁用或输入包含换行时不显示建议。

- **WebUI：Runtime 托管的知识库管理**
  - 新增 **知识库** 工作区：通过与 ACP 和 Desktop 相同的 Runtime/session 服务完成目录知识库的列表、新建、编辑、扫描、查询和删除。原生目录选择器不可用时回退到内置目录浏览器；源文件与索引存储始终由服务端持有。

- **Desktop：精简首页预设，快捷操作一键填入提示词**
  - 首页预设标签精简为「办公 / 代码 / 创作」，中英文预设描述同步收紧。
  - 快捷操作改为将完整、可直接发送的提示词（含可替换的 `[主题]` 占位符）填入输入框，一键即可开始真实任务，而不再是空泛的标签文案。

- **Desktop：开发模式（`make desktop-dev` / `npm run dev`）**
  - 新增开发运行器：监听 `renderer/src/`、`renderer/index.html` 与 `renderer/styles.css`，变更后自动重建 `dist/renderer` 并无缓存刷新 Electron，ACP 子进程无需重启；`main/` 与 `preload/` 仅在启动时构建一次，修改后需手动重启。
  - 开发模式（`MOTHX_DESKTOP_DEV=1`）自动打开 DevTools（以独立外部窗口打开，不嵌入主窗口），Chrome DevTools Protocol 仅监听 `127.0.0.1:9223`（可用 `MOTHX_DESKTOP_DEBUG_PORT` 更换本地端口），renderer 产物变化时自动重载窗口；不启动 `mothx serve`，也不向 renderer 增加任何 HTTP/API 通道。
  - 开发实例使用独立的 `desktop/.dev-user-data/` 用户数据目录（可用 `MOTHX_DESKTOP_USER_DATA` 覆盖），不会与已安装的 Desktop 争夺单实例锁，也不会复用其本地展示状态。

- **Desktop：在线技能市场（SkillHub Catalog）**
  - 新增 ACP 特性键 `manageSkillHubCatalog` 与增量式 `mothx/manage/skillhub/*` 方法族（markets/categories/official/search/detail/targets/installed/install/activate/uninstall），为共享 SkillHub 服务的纯 ACP 投影；请求必须绑定活跃会话，Desktop 无法任意选择安装目录。
  - Desktop 技能页新增「在线市场」区块：市场/分类筛选、关键词搜索、官方推荐，以及安装/更新/启用到当前会话/卸载，可装入当前会话的项目或全局技能目录。
  - ACP 会话现在跟踪多个已激活技能（此前启用新技能会顶替上一个），同一会话可同时启用多个技能。

- **Desktop：应用背景图片扩展选项**
  - 背景图可应用于整个应用或仅首页，支持适配方式（填满裁切 / 完整显示 / 拉伸铺满 / 循环平铺）与对齐位置（中 / 左 / 右 / 上 / 下）。
  - 背景遮罩与表面毛玻璃随图片透明度自适应减弱；全局背景下的标题栏使用可读性更强的对比表面与文字阴影，保证窗口控制按钮清晰可辨。
  - 外观设置独立为单独的「外观」设置分类。

- **主角团成员提问直达 lead**
  - 成员需要决策时向 lead 提问，而不是向用户提问：问题进入会话邮箱，以 `[MEMBER_QUESTION]` steering 消息（或 `subagent_wait` 中 `status: question` 的待处理条目）投递，并由 lead 用 `subagent_answer(handle: "<成员>", question_id: "…", answer: "…")` 回答；用户只在 lead 事件流上看到该提问的投影。
  - 该邮箱通路在所有能派生成员的会话中生效 —— 多 Agent、delegate 与 workflow 模式 —— 不限于绑定主角团的会话；`subagent_wait` 在这些会话中同样能报告待处理的成员活动。只有绑定主角团的会话会在收尾轮为仍在运行的成员保持 run 打开；其他会话正常结束本轮，并在下一个迭代或下一次 run 时看到通知。
  - 回答一个已不在待处理的提问（已回答、已过期或属于其他成员）会返回错误而不是静默成功。阻塞式 `delegate_subagent` 子 Agent 不会提问，因为它的调用方正停在工具调用里，无人能作答。

- **在 Desktop 与 WebUI 重试失败的消息投递**
  - Desktop 的通道设置列出当前会话失败的持久化投递（平台、操作类型、状态、失败码、尝试次数与最后更新时间），并提供逐条重试；WebUI 在通道设置中通过 `GET /api/deliveries/failures` 与 `POST /api/deliveries/retry` 暴露同一份 Runtime 事实。
  - 两个入口都投影 Runtime 自己的判定：只有 failed 且传输级的操作才提供重试，进行中、已投递与永久失败的操作会被拒绝，而不会被打回重试队列（拒绝原因与持久化 fence 一致）。

### 🐛 问题修复

- **Browser：内置 Skill 不再写入项目目录**
  - Browser 指引现随内置 `vibe-browser` Skill 提供。在 TUI、WebUI、Desktop、ACP 或频道中启用 Browser 不再创建 `.skills/vibe-browser/SKILL.md`；用户主动创建的同名项目或全局 Skill 仍可覆盖内置指引。

- **频道：Browser 选择在 Runtime 重装配后保持有效**
  - 频道会话持久化的 Browser 选择现在同时驱动初始 Registry 构建和 Runtime 资源重装配。显式启用 Browser 后，Session Runtime 附着时不再将该工具移除。

- **TUI：运行期间提交的提示词排队执行，不再顶替当前运行**
  - 同一会话同一时刻只允许一个前台执行。此前在运行进行中提交输入会直接替换内存中的运行句柄，导致活跃运行的终态清理与运行时租约被孤立。现在此类提交会在 TUI 中排队，仅当前一个运行到达规范终态并释放租约后，才启动下一个排队提示词 —— 覆盖所有终态分支（成功、失败、未完成与取消）。
  - 排队的提示词保留 Runtime 预制的附件（`agentruntime.PreparedInput`），并通过同一输入契约重新提交，附件在延迟期间保持不变。

- **TUI：`/defaultModel` 与 `/model` 共用同一模型目录逻辑**
  - `/defaultModel` 选择器现在通过 `providerfactory.ResolvedModels` 解析每个 Provider 的模型列表 —— 与 `/model` 和 WebUI 选择器背后同一套工厂解析目录（内置预设与 settings 覆盖合并）—— 不再直接解析原始 `settings.json` 的 models。settings 条目仅配置凭据或部分模型的 Provider 不再丢失其余内置模型。

- **被取消的运行不再被记为成功**
  - 在等待成员期间、或最后一轮进行中被取消的运行，现在以 `aborted` 原因记为 cancelled，而不再被投影为正常完成；从终态事件推导状态的入口（ACP/Desktop）会显示用户请求的取消结果。
  - 因输出上限被截断、但已通过升级或续写恢复的那一轮，不再被判为 incomplete：截断标志现在只作用于自己那一轮，不会泄漏到下一轮。
  - TUI：已退役的事件流会继续被排空，让被取消的运行总能完成终态记账；被取消的缓存 Agent 会在下次提交前丢弃；因运行提前结束而残留的运行中工具行会被终态化。

- **消息频道：有界投递重试窗口**
  - 持久化投递操作改为在可配置窗口（默认 10 分钟）内重试，而不是在固定尝试次数后放弃。传输级失败会自动重试、由 serve 重连恢复路径重新打开，或由运维通过 ACP `mothx/manage/deliveries/retry` 重开。
  - 永久性失败（平台 4xx、不支持的媒体类型、投影损坏）保持 failed：失败投影返回 `retryable` 标记，重试入口会拒绝非 failed 或不可重开的操作而不是把它们打回 `retry_wait`，持久化层执行同样的 fence。仅因依赖失败而终止的操作会随依赖一同恢复。

- **频道：被显式关闭的 sub-agent 工具不再被重新打开**
  - 把 sub-agent 工具重新指向会话级 manager（它拥有会话邮箱）时，不再复活用户关闭的工具：普通多 Agent 选择保留逐工具开关，绑定主角团的会话仍以完整工具集为权威。

- **并行工具调用按声明顺序启动**
  - 并行工具批次现在严格按模型给出的顺序开始执行：参数解析、审批等待或持久化执行声明不再让后面的调用抢跑。调用之间仍然并发重叠、完成顺序不受限制；结果、transcript 顺序与 provider 续写消息维持原有的顺序保证。

### ✅ 测试

- TUI：新增测试断言运行期间提交的输入仅排队而不替换租约持有者，且排队提示词只有在取消流程完成持久化运行终态并释放租约之后才会启动。
- TUI：`/defaultModel` 新增覆盖，断言对话框模型列表与工厂创建的 Provider 列表（`/model` 路径）一致，覆盖部分模型覆盖与仅凭据两类 settings 条目。
- 主角团：覆盖 Runtime 绑定/分叉、命名成员事件、TUI 与 Serve 的“成员终态不直接开 run”守卫、ACP bind/fork 进程路径、Desktop 投影与跨入口 ESM 空闲 gate。
- 频道：全“可选工具”契约测试验证每个可用的持久化工具选择都会出现在解析后的会话 Registry 中。
- 主角团：成员提问 → lead → `subagent_answer` 闭环、已失效提问的拒绝、邮箱归属（会话 lead vs 辅助角色、定时任务、团队绑定的 ESM worker），以及非团队会话在不为成员等待的前提下投递成员通知。
- Agent 循环：成员等待期间取消、以及被后续轮次恢复的截断轮的终态覆盖。
- 频道：sub-agent 工具与 lead 共享同一会话邮箱；部分工具选择不会被重新注册复活。
- 投递：永久失败的重开拒绝，覆盖运维入口与持久化 fence 两层。
- 投递：WebUI 端点覆盖列表、重试成功以及永久失败/进行中/未知操作的拒绝；Desktop 投影覆盖同一判定与双语文案。
- Runtime：孤儿恢复并发用例改用同步屏障证明 worker 重叠，不再依赖时间窗口，因此不会在高负载下抖动。

## v1.2.99

### ✨ 新功能

- **WebUI 与 TUI 统一使用服务端解析的模型目录**
  - 新增 `GET /api/models/catalog` 端点，通过 `providerfactory.ResolvedModels`/`SortProviderIDs` 解析全部可选 Provider/模型 —— 与构建 TUI Provider 模型列表使用的是同一套共享逻辑 —— 并返回规范化默认值与排序后的 Provider 列表。即使当前活跃 Provider 来自内置预设或 serve 参数而非 settings 的 providers 映射，也依然可选。
  - `stores.js` 由 `models` 迁移为 `modelCatalog`；Chat 新会话选择器改为直接消费服务端目录，不再在客户端合并原始 settings JSON，移除本地 `buildModelCatalog`/settings 兜底推导。设置页各入口（默认 Provider/模型下拉、Provider 编辑器）消费同一 store，继承的内置预设模型新增专用中英文标签。
  - TUI 认证对话框的 Provider 排序委托给 `providerfactory.ProviderSortPriority`，TUI 对话框与 WebUI 目录共用同一套排序逻辑。

- **Enable Supervisor Mode（ESM）：斜杠命令控制、共享引导与证据追踪**
  - WebUI 的 ESM 控制改为与 TUI 相同的 `/esm` 斜杠命令（`/esm <objective>`、`/esm status|edit|pause|resume|clear|guide`），不再使用专用图形控件 —— 移除 500 行的 `ESMControls` 组件，聊天输入与 ESM REST API 共享同一条服务端 objective 路径。
  - 新增引导（guidance）模块：`/esm guide <text>` 将用户引导排队并打上 objective 当前版本戳；Supervisor 将待处理引导注入每个非恢复角色（role）的提示词，并在角色结果应用后恰好消费一次 —— 由核心统一持有生命周期，TUI 与 WebUI 适配器共享。
  - 新增证据（evidence）模块：共享的 `EvidenceTracker` 按角色运行累积工具调用证据（唯一工具调用 ID、按工具的计数/错误），使 `ApplyWorkerResult`/`ApplyReviewResult` 中“工具支撑证据”校验不会在适配器之间产生分歧。
  - ESM 不再强制 token/时间预算：移除 `budget_limited` 状态、`SetBudget`/预算提示词与 TUI `/esm budget` 子命令；`TokensUsed`/`TimeUsedMS` 仅作可观测性计数。阻塞审计阈值集中为 `BlockedAuditLimit`（连续 3 次运行报告相同 blocker 后置为 blocked）。
  - 无人值守派生运行通过 `agentruntime.ResolveUnattendedMode` 解析执行模式：仅 `os` 从会话模式继承，其余会话模式一律回退为 `yolo`，ESM 角色子代理不会因交互式审批而停摆（高危命令硬防护仍与模式无关）。
  - Supervisor 以基础 Run ID 执行 continuation（角色运行使用由其派生的带后缀 ID），由 Supervisor 统一持有两个适配器的终态 `FinishRun` 调用，并在 continuation 结束时清理更早 continuation 遗留的过期连续计数。

### 🐛 问题修复

- **ESM：失败 objective 转为暂停，杜绝静默重跑**
  - 不可重试的角色失败现在会将 objective 置为暂停，须显式 `/esm resume` 才能再次运行 —— 排队的引导或后续触发器不再静默重跑已失败的任务。超时与可重试的传输错误仍走受 `RecoveryLimit` 约束的恢复路径。
  - Serve 启动时不再重放历史上持久化为 "active" 的 ESM objective：角色可能在进程退出前刚刚失败，因此 `Create`/`Edit`/`ResumeESM` 成为仅有的显式执行入口。
  - `esmCoordinator` 新增带 done 通道与有界等待的 `stop`/`stopAll`；Serve 关闭时取消所有 ESM worker 并等待其释放会话/运行时引用（`SessionRuntime.Shutdown` 仍是最终资源边界），已关闭的协调器拒绝启动新 worker。

- **无桌面服务器上的原生目录选择器**
  - Unix 环境下缺少 `DISPLAY`/`WAYLAND_DISPLAY` 时，原生选择器明确报告不可用而非静默失败，让 Web UI 回退到内置目录浏览器。启动失败且 stderr 带诊断信息的场景现在会作为错误暴露，不再被误判为对话框取消。

### 🔧 改进

- **目录浏览器：Windows 盘符根目录与按路径解析的允许根**
  - `/api/browse` 的允许根（allowed-root）解析现在接收请求路径；Windows 各盘符根目录通过虚拟浏览根列出（盘符根之间没有可供导航的公共父目录）。`DirBrowser` 新增 `initialPath` 属性、一次性打开语义、服务端返回的 `selectable` 标志与刷新支持。

- **工具恢复审计记录**
  - `RequestToolExecutionRecoveryRecords` 记录用户的显式确认并仅返回匹配的中断调用；记录作为审计证据保留，恢复则以全新执行开始。新增 DAO 列表接口（`ListRequestedToolRecoveries`）向 Serve 暴露已请求的恢复记录。终态 Run 绝不会被重新激活来消费这些记录。

### ✅ 测试

- ESM：新增/扩充引导生命周期（版本戳、注入、一次性消费）、证据追踪、不可重试失败即暂停、基础 Run ID continuation、预算移除、`/esm` 斜杠命令一致性，以及协调器 `stop`/`stopAll`（含已关闭协调器拒绝启动）的覆盖。
- Serve：进程级测试断言启动绝不重放历史 ESM objective；browse-root 测试覆盖允许根解析与 Windows 盘符根列出；原生选择器测试覆盖无桌面不可用与 stderr 诊断场景。
- Provider factory：目录解析与 Provider 排序测试；settings：`qwen3.8-max-0902` 预设断言。
## v1.2.98

### ✨ 新功能

- **Gitee/Moark 新增模型：`qwen3.8-max-0902`**
  - `gitee` 和 `moark` 两个提供商均新增 `qwen3.8-max-0902`，支持 1M 上下文窗口、128K 最大输出与文本/图片输入。

### 🔧 改进

- **TUI：死代码清理与决策生命周期对齐**
  - 移除未使用的函数（`renderLiveAssistantMessage`、`renderPlanPanel`、`formatPlanForDisplay`、`normalizeHistoryLineEndings`、`resolveESMStoreDir`、`updateViewportContentWithFollow`），并精简对应的 plan/ESM 测试覆盖。
  - 问题请求现在通过 `DecisionService` 注册，挂起的问题与审批一样可持久化、可重放；重复的问题请求会被拒绝。
  - 终态决策状态根据实际运行状态映射，不再一律将挂起决策标记为 `cancelled` —— 只有显式取消才记录 `cancelled`，其它任何终态记录 `timed_out`。
  - 延迟打印循环新增 `stopPrintLoop` 退出路径，退出与重载前排空已排队的转录内容再清理。
  - 外部状态行刷新推迟到实际渲染时执行，将高密度事件突发合并为一次刷新。
  - `sessionsDel` 改为通过防御性的 `getSessionDir()` 辅助函数解析会话目录。

- **WebUI：设置页模型列表与新会话选择器保持一致**
  - 设置页的“默认 Provider / 默认模型”下拉改为复用新会话模型选择器背后的服务端解析目录（`GET /api/models/catalog`），因此内置预设 Provider 及其默认模型也可在此选择；表单中尚未保存的编辑会追加在目录之后，可在保存前直接选用。
  - 供应商设置同样复用该目录：左侧模型数量徽标显示解析后的模型数，模型列表额外以只读行展示未写入配置的内置预设模型（与新会话选择器一致，保存时不会持久化；添加相同 ID 的模型即可覆盖）。

### 🐛 问题修复

- **Serve Responses 恢复保持 Run 终态不变**
  - 恢复经用户确认的中断工具调用时，不再把已完成或失败的 durable Run 改回 `queued`，也不再重新接管其已终止的远端 Responses 任务。Serve 现在通过正常 Runtime 输入路径提交一条新的、幂等的恢复消息，并启动新的本地 AgentLoop Run；原 Run 保持不可变。
  - 移除仅供恢复使用的“终态转活跃”Run 存储 API，防止后续调用方绕过规范的单调生命周期。

### ✅ 测试

- TUI：新增问题决策注册（挂起类型、持久化、重复拒绝）与 ESM 存储目录解析测试。
- Serve：Responses 恢复测试覆盖原终态 Run 保持不变、新 AgentLoop 收到恢复消息，以及重复恢复请求幂等返回同一个新 Run。
## v1.2.97

### ✨ 新功能

- **Provider 在线模型发现**
  - 模型发现能力下沉到 `internal/provider`，提供共享辅助函数（`ModelsEndpoint`、`ResolveSecretRef`、`DiscoverModels`），可拉取并规范化 provider 的 `/models` 列表为 `DiscoveredModel`。OpenAI 兼容的 `/v1/provider/models` 与 model-test 接口改为调用这些共享实现，不再各自重复探测逻辑。

- **TUI：在认证对话框中拉取并搜索在线模型**
  - provider 模型列表与模型设置视图新增 "Fetch Online Models" 入口：以后台命令对草稿 provider 的 Base URL / API 类型执行发现，并打开 "Add Model · Online List" 视图，可将拉取到的模型加入或移出草稿。保存 provider 之前不会持久化任何内容，关闭对话框或切换 provider 后的过期结果会被丢弃，加载中/空/错误状态均有 zh/en 文案。
  - 在在线列表中输入即可过滤拉取到的模型，按模型 ID 与显示名以 精确 > 前缀 > 子串 排序，同分保持发现顺序；Esc 清空查询，无匹配时显示 "No models match." 提示。

### 🔧 改进

- **公共 SDK 边界：`agent/` 不再依赖 internal 包**
  - provider 桥接从公共 `agent/` 包移入 `bootstrap/`（外部模块本来就通过空白导入使用它），在 init 时注册 provider 解析钩子与具体工厂。
  - `agent.Builder` 不再预先解析平台会话目录（由内部 builder 在 Build 时解析默认值），钩子未注册时给出明确错误；示例改为空白导入 `bootstrap` 而非 internal 包。

- **会话存储完整性加固**
  - `DeleteSession` 现在通过其会话归属的父表级联清理没有 `session_id` 列的子表（`delivery_operations`、`attachment_deliveries`），删除会话后不再残留孤儿行。
  - schema 迁移按版本号升序执行，不再按切片顺序；前向建表引用不再依赖 FK 强制关闭。
  - Run 的非终态/终态状态集合集中在 `run_store.go`，作为唯一事实来源；SQL 字面量、部分唯一索引、fork、轨迹与恢复路径全部从中派生。
  - `EndConversationTurn` 对已关闭的回合幂等；会话条目 ID 扩展为 64 位；`IdentityLocks` 委托给引用计数的锁注册表；运行时租约总线在最后一个处理器退订后关闭 UDP 监听。

### 🐛 问题修复

- **TUI：单独回车即时提交**
  - 排队中的 Enter 曾被当作换行证据，导致每次快速输入后回车发送都要等满 120ms 的分片粘贴合并窗口。现在扩展空闲窗口仅在队列中存在真实粘贴证据（含换行的字符块或紧邻文本的 Enter）时启用；单独的延迟 Enter 保持正常 16ms 窗口并立即提交。

- **被放弃的 Durable 运行缺失错误原因**
  - 工具执行被中断后放弃的后台运行可能在进入终态时未持久化原因。现在通过专用注解边界（`RunDAO.UpdateErrorIfEmpty` → `session.AnnotateSessionRunError` → `agentruntime.AnnotateDurableRunError`）仅在错误仍为空时写入——不改变运行状态、不复活终态运行、不触碰活跃运行，首个记录的原因保持权威。Responses API 的放弃路径经该边界持久化原因。

- **后台运行协调器重复用户条目**
  - 准入阶段原子追加的用户条目在管理器重载后已存在于重放状态中；协调器现在按确定性 `RunUserEntryID` 匹配并复用该条目作为续接消息，不再向会话转录与 provider 请求追加重复条目。该检查在重试、恢复与进程重启间保持幂等。

- **通道轮换租约目标目录**
  - `AcquireRuntimeForRotate` 现在显式从生命周期属主获取会话目录（为空时回退到 dispatcher 配置的目录），使变更租约与强制释放等待作用于权威的 Session，而非 dispatcher 持有的任意目录。

### ✅ 测试

- 架构：新增 `public_sdk_boundary_test`，公共 `agent/` 包或 `example/` 模块再次导入 internal 包时失败。
- 新增会话测试：删除完整性（无孤儿行）、迁移升序执行、Run 状态集合一致性、引用计数锁注册表、租约总线监听器清理；`-race` 下放宽孤儿恢复的时序余量。
- 新增快速单独回车提交（分片粘贴续接仍受保护）与 durable 运行错误注解的回归测试。

## v1.2.96

### ✨ 新功能

- **运行时工作区输入物化**
  - 现在所有入口点（CLI、TUI、WebUI/API、ACP、微信、飞书）的用户文件统一由一个前端无关的输入契约负责。适配器提交源流，`internal/agentruntime` 将接收的文件物化到项目工作区，首条用户消息声明文件路径与元数据，由 Agent 决定是否以及如何读取每个文件。
  - 图片不再在摄入时就自动转换为厂商图片内容；输入资源通过 `input_resources` 表持久化，并拥有 Runtime 托管的完整生命周期（`PrepareInput`/`AttachPreparedInput`、丢弃/删除/清理、`input_resource_events`）。
  - TUI 的 `/paste-image` 现在提交由 Runtime 写入的流；Web UI 新增聊天附件上传/预览；ACP 的提示内容（文本/图片/文件/音频/视频）与微信/飞书入站媒体都统一走同一入口。

- **基于租约的执行准入与孤儿运行恢复**
  - 旧的 `TryLockRuntime`/`TryLockRuntimes` 路径被显式、按用途和运行绑定的运行时租约取代，覆盖 CLI、TUI、ACP、Serve、通道和定时任务：带引用计数的 durable 租约守卫（`AcquireExecutionAdmission`/`AcquireFork`/`AcquireMutations`、运行绑定）以及针对陈旧/孤儿运行的恢复与协调模式。
  - 新增 `RecoveryCoordinator`，通过启动扫描和周期/唤醒驱动的重试以租约优先方式收敛孤儿运行，并由 `session_run_recoveries` 表提供持久化状态、重试计数与幂等重放。
  - 会话运行时快照现在暴露准入/恢复事实（`reserved`、`local`、`external`、`detached_remote`、`orphaned`、`recovery_failed`、`inconsistent`）；Web UI 显示对应状态徽标，并在会话忙碌时禁用删除/fork。

- **Durable 投递发件箱**
  - 新增投递意图与有序操作（`delivery_intents`/`delivery_operations`），支持确定性的 `PlanDelivery` 序列（caption/上传/发送/回退）、Runtime 的 claim/fence/重试协调器、助手消息/Run/回合/事件/意图的终态原子提交，以及服务启动恢复。
  - 微信（图片/视频/文件）与飞书（图片/文件）出站媒体通过冻结的传输上下文原生投递；发布产物移动到工作目录之外的私有存储，打开时校验完整性（大小 + SHA-256）。

- **幂等的运行提交**
  - 新增 `runtime_submissions` 表，冲突时进行对账处理：submit-key 冲突复用已有提交而不是创建重复记录，使 Run 准入具备重试安全性。

- **火山引擎新增模型：`glm-5.3-flash`**
  - `volcengine`、`volcengine-agentplan` 和 `volcengine-codingplan` 三个提供商均新增 `glm-5.3-flash`，支持 1M 上下文窗口与文本/图片输入；与 `glm-5.3` 一致，默认不发送 max_tokens。

- **Gitee/Moark 新增模型：`glm-5.3-flash`**
  - `gitee` 和 `moark` 两个提供商均新增 `glm-5.3-flash`，支持 1M 上下文窗口、128K 最大输出与文本/图片输入。

- **新增 AMD Radeon 厂商支持**
  - 新增 `amd-radeon` 厂商，Base URL `https://developer.amd.com.cn/radeon/api/v1`，OpenAI 兼容协议。
  - 新增 `DeepSeek-V4-Flash`（1M 上下文）与 `Qwen3.8-Flash-Next`（1M 上下文）模型。

### 🔧 改进

- **仅 DAO 的 SQL 迁移**
  - `internal/db` 现在统一管理进程级 SQLite/Bun 连接生命周期与事务边界；会话、定时任务、用量统计、ESM 与投递相关 SQL 全部迁移到 `internal/dao` 持久化对象。
  - 移除 `internal/commondb` 兼容包与投递遗留桥接；架构守卫以最小迁移归属 allowlist 强制 DAO-only 边界。

- **Web UI 加载与状态稳定性**
  - 路由视图（Chat/Sessions/Stats/Cron/Skills/Settings/Login）改为懒加载，只拉取当前路由的 chunk；lucide/bits-ui/svelte 依赖合并为稳定的 vendor chunk。
  - 会话运行时状态（加载/PATCH/轮询/模式切换）抽成可单测的管理器；历史快照逐字段合并，陈旧的持久化投影不会覆盖实时助手文本。

### 🐛 问题修复

- **缓存输入 token 重复计费**
  - 用量统计现在通过 `UncachedInputTokens` 正确计算未缓存输入 token，避免在 Anthropic、OpenAI 兼容与 Google 三种线上格式中重复收取缓存读取费用。

- **ListSessionRuns 连接死锁**
  - `ListSessionRuns` 在 `session_runs` 行循环内查询 `input_resources`，在单连接池（MaxOpenConns(1)）下永久阻塞，导致带运行记录的会话在 TUI 启动时挂起。现在先耗尽外层行再一次性批量查询，并新增断言其正常完成的回归测试。

- **Durable 运行终态事件稳定性**
  - `RunExecutor.Finalize` 不再为 durable 运行发布终态流事件；`FinalizeRun` 在 `FinishDurable` 提交助手消息后保持唯一发布者，避免 WebUI 历史重载与数据库写入竞争。当内存标记已清除时从规范 Run 行恢复 durable 身份；终态化期间容忍已关闭的会话回合，幂等重试仍能提交最终条目与终态事件。

### ✅ 测试

- 架构：`input_contract_guard_test` 在 TUI、CLI、WebUI/API、ACP 与 Channel 入口点强制单一输入契约。
- 扩展准入/恢复测试：租约优先的孤儿收敛、执行快照、停止处理、幂等性与跨进程租约行为；投递进程集成测试覆盖 claim/fence/重试与协调器恢复。
- 新增缓存输入 token 计费与 `ListSessionRuns` 死锁的回归测试。

## v1.2.95

### ✨ 新功能

- **新增 Gitee/Moark 模型：`qwen3.8-flash`**
  - 支持 1M 上下文、文本/图片输入，默认不发送 max-token 上限。

- **CLI 持久化运行**
  - CLI `runPrint` 现在通过 `agentruntime.ExecutionRuntime` 持久化规范的 durable run，与 WebUI、消息通道和 ACP 的运行生命周期保持一致。

- **UDP 运行时租约总线**
  - 新增尽力而为的 UDP `SessionLeaseBus`，在运行时租约和运行状态变化时唤醒本地进程。
  - 使用定向回环广播和去重；SQLite 租约与 durable 记录仍是唯一权威。

- **按运行选择厂商/模型（API 与 Web UI）**
  - `POST /v1/responses` 运行新增可选 `provider` 字段；支持解析并校验 `provider/model` 限定 ID，厂商与模型不匹配时返回结构化错误。
  - 运行使用请求厂商的 Agent，通过共享 `SessionRuntime` 构建；运行策略快照与请求指纹记录厂商。
  - `/v1/models` 现在返回每个模型的所属厂商。

### 🔧 改进

- **ClawHub 歧义技能 slug 自动解析**
  - 当 ClawHub 对无归属前缀的技能 slug 返回 `409 AMBIGUOUS_SKILL_SLUG` 时，客户端现在会自动解析唯一精确匹配的 slug，并使用解析出的归属者重试一次，安装 `clawhub.ai/custom-mail-fresh100` 这类技能不再需要手写 `@owner/slug` 形式。
  - 当匹配项无法确定唯一候选时，将返回列出所有候选 ref 的可读错误，而不是直接暴露原始 409 响应。

- **统一 Bun 数据库访问**
  - 新增共享的 `internal/db` SQLite/Bun 连接与事务层，并为定时任务、用量统计、ESM 目标和通道绑定增加 DAO 持久化对象。
  - 移除 `internal/commondb` 兼容包；共享连接与关闭统一由 `internal/db` 管理，已迁移表由 Bun DAO 负责访问，会话事务辅助统一使用 DAO 持有的 Bun 句柄。

- **事件代理重同步**
  - 事件代理新增 `SubscribeWithResync`；订阅者溢出时关闭 WebSocket，让客户端重连并重放 durable SQLite 游标。

- **运行时租约心跳**
  - 租约心跳现在对瞬态 SQLite 失败进行有界重试，并发布 `acquired`/`released`/`lost` 通知。

- **会话能力开关（沙箱/浏览器/联网搜索）**
  - `SessionRuntime` 新增 `CapabilitySnapshot`、`ConfigureCapabilities` 与 `SetCapabilityOption`；浏览器与联网搜索能力通过 `session_capabilities` 持久化并在加载时重放，同时同步核心工具。
  - ACP 会话在运行时租约下恢复持久化能力与额外目录；沙箱仍由进程策略拥有。

- **Web UI 厂商感知的模型选择器**
  - 新增可搜索的 `ModelPicker` 组件，带文本/图片/音频/视频/文件模态图标，替换原有模型菜单。
  - 聊天输入框基于 `/v1/models` 与已配置厂商构建级联模型目录，选择厂商后模型列表随之收窄，并在每次运行提交时携带厂商。

- **ACP 会话扩展方法**
  - 新增 `session/fork` 与 `mothx/session/setTitle` 处理、工作区窗口协商（`cwd`/额外目录）、fork 血缘级联删除以及 `available_commands_update` 通知。
  - 可选的编辑器上下文以有界的不可信上下文块注入；已释放运行时租约的历史会话加载时不再改写持久化绑定。

### 🐛 问题修复

- **后台运行乐观并发**
  - 在 durable admission 之后重新加载共享会话管理器，使后台协调器将用户消息附加到新叶子，而不是因乐观并发校验失败。

- **微信 iLink 入站连接恢复**
  - 微信适配器现在发送 iLink 上线/下线生命周期通知；仅在 `getupdates` 轮询成功后报告已连接；采用服务端建议的长轮询超时，并在重启后恢复 iLink 同步游标。
  - 请求现在携带 MothX 通道版本和 bot agent；iLink 返回畸形响应时会显式报错，不再静默当作空轮询处理。

### ✅ 测试

- 在 `TestResponsesRunAPIAbandonMarksInterruptedToolsWithoutRetry` 中检查废弃工具记录前获取运行时租约，与生产环境的 recovery caller 模式一致。
- 新增 ACP 测试：已释放租约的历史会话加载不得持久化默认值、运行时租约下的目录更新、历史会话标题修改。
- 新增 serve 测试：按运行选择厂商、厂商/模型不匹配、限定模型解析。

## v1.2.93

### 🐛 问题修复

- **GHCR 镜像构建改用 Go 1.27**
  - Docker 构建镜像现在使用 `golang:1.27.0-bookworm`，与 `go.mod` 一致，避免 GHCR 打包时出现 `go.mod requires go >= 1.27 (running go 1.26.1; GOTOOLCHAIN=local)`。

- **桌面端版本跟随 Git Tag**
  - 桌面端 `package.json` 只保留 `0.0.0` 占位符，不再写死发行号。
  - 打包时从 `MOTHX_VERSION`（如已设置）或当前 git tag（`git describe --tags --abbrev=0`）解析真实版本，并在构建时写入 `package.json`、`package-lock.json` 和 `mothxRuntime.version`。
  - 桌面端 CI 不再把分支名当作版本，而是使用显式 tag 覆盖或检出仓库的 git tag。

### 🔧 改进

- **ACP 安装诊断**
  - `initialize.agentInfo` 现在报告真实的 MothX 身份和构建版本。
  - 新增无需会话的 `mothx/doctor`、`mothx doctor --json` 和结构化 `MOTHX_ACP_ERROR` 启动诊断，检查结果不包含密钥原文。

- **清理 CI 测试工作流**
  - 移除了每次提交都会运行的 GitHub Actions 测试工作流，推送不再自动跑测试。

## v1.2.92

### ✨ 新功能

- **会话分叉与消息分支**
  - 会话现在可以从任意消息分叉并扩展出替代分支；执行意图通过带运行时锁的分叉路径传递。
  - 会话 schema 与迁移、运行/响应存储、条目处理、ESM 指引、后台运行协调器和 dispatcher 均支持分叉；TUI 会话命令/运行时与 WebUI 聊天/会话视图同步更新。

- **WebUI 轨迹视图与会话日志导出**
  - Serve WebUI 新增只读轨迹投影，将会话消息、工具事件和运行事件按运行/回合/步骤组织，不复制 Agent/Runtime 状态。
  - 服务端会话轨迹与导出端点位于现有会话路由下，会话头部新增日志下载操作（可选包含子会话）。

- **WebUI 现代化改造（shadcn-svelte 与 lucide）**
  - 引入 Tailwind CSS v4 和 shadcn-svelte 风格组件（button、badge、card、dialog、input、switch、tabs、tooltip），并新增 `$lib` 别名与 `cn()` 工具函数。
  - 用 lucide-svelte 图标替换侧边栏、会话、设置和列表编辑视图中的文本符号；新增品牌标志、工作区筛选菜单（全部/项目/未分组）以及 Ctrl+Shift+K 新建对话快捷键。

- **署名提交 Co-Author 设置**
  - 新增全局 "authored" 设置（默认关闭），在系统提示中附加 MothX 共同作者标记，引导模型创建 git 提交时包含 `Co-Authored-By: MothX <harness@mothx.net>`。
  - 已接入配置持久化、TUI 设置对话框和 WebUI 设置表单，并提供双语标签。

- **新增提供商模型**
  - DeepSeek（anthropic + openai）：新增 `deepseek-v4-flash-vision-exp`，支持 1M 上下文、文本+图片输入，默认不发送 max_tokens。
  - 火山引擎 codingplan：新增 `doubao-seed-evolving`，支持 1M 上下文和文本+图片输入。

### 🔧 改进

- **默认模式改为 YOLO**
  - 新安装和空 mode 回退现在使用 `yolo` 而不是 `agent`：包括 `settings.json` 的 `defaultMode`、Serve/API `DefaultMode`、CLI/TUI/ACP/WebUI、公共 SDK `Builder`，以及 `agentruntime` 的策略解析。
  - 显式 `--mode`、已持久化的会话 mode，以及微信/飞书强制 `yolo` 仍然优先。已有配置中的 `defaultMode: "agent"` 不会被改写。

- **Serve 原生目录选择器**
  - Serve 现在通过操作系统原生目录选择器（macOS、Windows、Unix）选择工作目录，无需手动输入路径。

- **统一设置组件**
  - 抽取共享的 `SettingsField`、`SettingsSection`、`SettingsSwitch` 和 `ProviderEditorDetail` 组件，统一 AppSettings、ServeConfig、Channels、Env、Logs、Memory、Overview、SkillHub 和 WorkDir 的设置视图布局与样式。

### 🐛 问题修复

- **WebUI 侧边栏与会话 ID 修复**
  - 侧边栏折叠状态现在持久化到 localStorage，并补充折叠/展开标签。
  - 移除未使用的轨迹时间线模式、状态、翻译和布局辅助函数。
  - 修复 `AllocateSessionID`：`sessions.db` 已存在但 ID 未注册时视为可用，并补充回归测试。

## v1.2.91

### ✨ 新功能

- **ACP 会话准入控制**
  - ACP prompt 现在先获取共享的会话运行时锁并检查持久化的活跃运行记录，再开始运行，使 ACP 入口与 TUI、WebUI 和消息通道串行化。
  - 运行时锁在已准入运行的整个生命周期内持有，其他适配器无法用新运行抢占其终态持久化；存在活跃运行的会话会被拒绝，返回 `session already has an active run`。

- **WebUI 服务端分配的会话 ID**
  - WebUI 现在通过 `POST /api/session-id` 从服务器请求会话 ID，而非在浏览器端生成，使会话身份保持规范统一，同时保留延迟创建的"新建对话"体验。
  - 服务端分配会对保留中及已存在的会话 ID 去重，并在 10 分钟后清理过期保留；浏览器端随机 ID 生成仅保留用于运行请求键。

- **会话重复 ID 拒绝**
  - 使用已存在的 ID 创建会话现在会失败并返回 `ErrSessionIDExists`，而不会将新头部静默合并进旧会话导致对话分叉。
  - 自动生成的 ID 在冲突时会重试（最多 8 次）；会话头部写入改用普通 INSERT，从而可靠地检测重复 ID。

### 🔧 改进

- **统一会话创建**
  - TUI、serve 和 CLI 现在通过 `agentruntime.CreateSession` 创建会话，取代直接的 `session.New(...).Init()`，将会话创建集中到共享运行时。

## v1.2.90

### ✨ 新功能

- **新增 Gitee/Moark 模型：`qwen3.8-27b`**
  - 支持 1M 上下文、文本/图片/视频输入，默认不发送 max-token 上限。

- **ACP 会话配置选项**
  - ACP 新增 `session/set_config_option` 和 `session/set_mode` RPC 方法，客户端可在不重建 agent 或 provider 的情况下更改每个会话的模型、模式和思考级别。
  - 每个会话在 `session/new`、`session/load` 和 `session/resume` 结果中携带一致的 `configOptions` 目录，`session/set_config_option` 会通知所有已连接的客户端更新后的选项。
  - 配置持久化到会话历史（`model_change`、`mode_change`、`thinking_level_change` 条目），在会话加载时回放，确保绑定在重启后和跨多个适配器共享同一会话时保持不变。
  - 稳定的流式消息 ID（`agent_message_chunk`、`agent_thought_chunk` 和 `user_message_chunk` 更新上的 `messageId`），将每个 prompt 回合的块分组为逻辑消息。

- **ACP 附加目录支持**
  - `session/new`、`session/load` 和 `session/resume` 接受 `additionalDirectories` 数组（绝对路径的工作区根目录）。工具注册表在这些根目录内解析路径，沙箱将其挂载为只读（严格模式）或可写路径。
  - 目录集作为 `additional_directories` 会话条目持久化，并在重新加载时恢复。
  - 会话列表端点（`session/list`）为每个会话暴露 `additionalDirectories`。

- **ACP 标准 Elicitation 表单协议**
  - 当客户端在能力声明中声明 `elicitation.form` 时，问题请求使用标准 ACP `elicitation/create` 方法，而非遗留的 `_mothx/request_question` 扩展，并带有类型化的 `requestedSchema` 信封。
  - 回放时，若重连客户端未声明表单支持，则优雅回退到旧扩展格式。

- **ACP 文件差异协议投射**
  - 工具调用更新现在包含 `diff` 内容类型，包含 `path`、`oldText` 和 `newText` 字段用于语义差异表示，同时保留现有文本内容。新建文件时 `oldText` 为 `null`。
  - 工具调用更新在存在差异时携带 `locations` 包含受影响文件路径。

### 🔧 改进

- **会话模式与思考级别持久化**
  - 新增 `EntryModeChange` 和 `EntryAdditionalDirectories` 会话条目类型，将模式切换和目录绑定记录到会话历史，实现完整回放和跨会话一致性。
  - `SessionRuntime` 拥有 `Model`、`Mode`、`ThinkingLevel` 和 `AdditionalDirectories` 作为会话级绑定，提供 `ConfigureSession`、`ConfigSnapshot`、`SetConfigOption` 和 `SetAdditionalDirectories` 方法实现原子读写。
  - `BuildAgent` 在适配器未提供覆盖时继承会话级模型、模式和思考级别，确保 ACP、TUI 和 WebUI 间一致的 Agent 构建。

- **ACP 严格 JSON-RPC 2.0 校验**
  - ACP 服务器现在强制要求：`initialize` 必须在任何其他方法之前调用、`initialize` 只能调用一次、空消息静默跳过、通知（空 ID）不发送响应、请求 ID 校验为 JSON-RPC 标量类型。
  - `cancel` 需要 `sessionId` 参数，对未知会话返回错误。

- **ACP 跨连接唯一运行 ID**
  - Prompt 运行 ID 现在包含随机后缀，防止 ACP SDK 请求 ID 在连接间重复时产生运行 ID 冲突。

- **CI 发布说明使用更新日志**
  - GitHub release 工作流现在使用 `docs/changelog_online_en.md` 作为发布正文，而非自动生成的发布说明，确保发布携带精心编写的更新日志。

- **FileDiff 新增 OldText/NewText**
  - `FileDiff` 现在保留完整的文件内容（`OldText` 和 `NewText`），用于需要语义差异而非展示补丁的协议投射。新建文件时 `OldText` 为 `nil`。

- **ContentBlock 文件字段扩展**
  - `FileContent` 新增 `Title`、`Description` 和 `Size` 字段，ACP `resource_link` prompt 类型将这些字段映射到 provider 中立的文件表示。

### 🐛 修复

- **ACP 空 Prompt 主体拒绝**
  - 仅包含空文本的 prompt 现在会被拒绝，返回 `empty prompt` 错误，而非进入 agent 循环。

- **ACP 不稳定测试与 CI 稳定性**
  - 修复了 wrapped thinking text 断言和 CI 中不稳定的 Go 测试。

## v1.2.89

### 🐛 修复

- **错误信息泄露防护**
  - Serve API 中的模型发现错误不再透传上游响应体，防止凭据、私有诊断信息或任意 HTML 泄露给客户端。仅 HTTP 状态码足以说明模型发现失败原因。
  - 运行提交中的预检错误信息现已清空 `Detail` 字段，确保原始解析/存储诊断不会通过 `DisplayErrorMessage` 投射到适配层。

### 🔧 改进

- **ACP 会话模型配置**
  - ACP 现在解析 `HARBOR_ACP_REQUESTED_MODEL`，声明会话级模型/模式/思考级别选项，并通过共享 Session Runtime 持久化配置和构建 Agent。标准 config/mode 更新、能力门控的 form elicitation 与 `session_info_update`、稳定流式消息 ID、多会话隔离与非法模型错误均由 Go stdio 进程测试覆盖。Harbor 兼容验证保持为显式外部检查，不进入默认测试或 CI。

- **CI 分支版本解析**
  - `Makefile` 现在优先使用 `GITEE_BRANCH` 环境变量进行版本字符串解析，回退到 `git describe` 再到 `dev`，确保 CI 构建携带正确的发布标签。

## v1.2.88

### ✨ 新功能

- **ModelScope 模型列表刷新**
  - `modelscope` 供应商扩展为 `https://api-inference.modelscope.cn/v1` 提供的完整 45 个模型目录：DeepSeek V4 Pro / Pro-0813 / Flash-0731、Qwen3.5-27B / 35B-A3B / 122B-A10B / 397B-A17B、Qwen3.8-27B、Qwen3 base / Instruct / Thinking / VL / Next / Coder 系列、Intern-S1 / S1-mini / S2-Preview、InternVL3.5-241B-A28B、MiniMax M1-80k / M3、GLM-4.7-Flash / GLM-5.2、Step 3.5 / 3.7 Flash、腾讯 Hy3、ERNIE-4.5 PT 系列等，并为每个模型配置了上下文、推理与输入（text/image/video）能力。

### 🔧 改进

- **运行失败中的 Provider 错误详情**
  - `ErrorInfo` 新增 `Detail` 字段，在安全回退的 `Message` 之外保留有界（最大 4KB）且已脱敏凭据的 provider 诊断信息。新增的 `DisplayErrorMessage()` 将两者合并供适配层使用，运行失败不再丢失 provider 上下文。
  - 该详情已贯通 ACP、频道、TUI、Serve API（事件、chat handler、run executor、run API 与 session stream）以及 Web UI，本地化消息会附加诊断输出。
  - `IsRetryable` 现在将所有 4xx/5xx HTTP 状态码视为可重试，并在错误字符串中做数字状态码匹配。

### 🐛 修复

- **TUI 紧凑视图隐藏思考内容**
  - 修复了紧凑事件显示将思考消息渲染为空字符串、导致推理内容在转录中丢失的问题。现在思考内容在紧凑与完整事件视图中都会渲染，推理不再丢失，切回完整视图时也不会重复显示。

- **Web UI 图片预览无障碍修复**
  - 消息中的图片缩略图改为带正确标签、可通过键盘激活的按钮，不再是无标签的可点击图片；灯箱浮层也支持键盘聚焦，可用 Enter/空格键关闭，解决无障碍告警并改善键盘导航。

- **全新检出时内嵌 Web UI 修复**
  - `ui/dist` 中现在跟踪一个占位文件，保证在构建生产 UI 之前 `//go:embed` 指令始终能匹配，同时锚定了 dist 忽略规则。

## v1.2.87

### ✨ 新功能

- **可配置并发数的并行工具执行**
  - 单个 agent 回合内的多个本地函数/自定义工具调用现在通过有界并行工作池执行。新增顶层 `toolExecution` 设置：`mode`（`"parallel"` 或 `"sequential"`）与 `maxConcurrency`（默认 `10`；`1` 为串行执行）。
  - 该设置在 Web UI 的 Settings > Tools 与 TUI 的 `/settings` > Behavior 中开放，适用于 TUI、Web UI/Serve、频道和 ACP 运行，并由子代理与临时代理继承。
  - 工具完成事件可能乱序到达，而回传给 provider 的续接消息会按原始调用顺序恢复。

- **TUI 紧凑事件视图**
  - 新建与已有会话现在默认启用紧凑事件显示。常规生命周期、usage、托管工具和压缩细节默认隐藏，可用 `Ctrl+G` 切换到完整事件视图；运行中的工具行会由完成结果替换。

- **OS 执行模式**
  - 在共享 Runtime、TUI、WebUI、频道、ACP 与子 Agent 中新增仅提供 bash、且不启用沙箱的 `os` 模式。普通 bash 调用会自动执行，配置的黑名单规则仍需审批，硬性高风险命令阻断也继续生效。

- **新增模型：`deepseek-v4-pro-0813`**
  - 在 Gitee 与 Moark 供应商中新增 `deepseek-v4-pro-0813` 快照模型，支持 1M 上下文窗口与推理，且默认不传 max-token（最大输出 token 上限由供应商发布）。

- **Web UI 图片灯箱**
  - 新增图片灯箱浮层，支持键盘导航（Esc/方向键）、上一张/下一张按钮、新标签页打开与图片计数。
  - 消息中的图片缩略图加大并带悬停放大提示，输入区图片预览卡片样式同步优化。

### 🔧 改进

- **Provider 并行工具调用支持**
  - Anthropic、Google 和 OpenAI 适配器现在在受支持时发送并行工具调用请求，并对拒绝该标志的网关遵守 `supportsParallelToolCalls: false`。

- **Anthropic tool-choice 兼容标志**
  - 新增 `supportsToolChoice` 兼容标志；当模型将其设为 `false` 时，Anthropic Messages 会整体省略 `tool_choice`（适用于拒绝该字段的网关），与并行工具调用控制相互独立。

- **设置 compat 深拷贝修复**
  - `cloneModelCompat` 现在深拷贝所有兼容字段（`SupportsToolChoice`、`SupportsParallelToolCalls`、托管工具、supported include、reasoning-effort/strict-mode 等标志），解析出的模型配置不再与原始设置共享引用。

- **频道 OS 模式审批对齐**
  - 频道中 `os` 模式的运行现在遵循与 `yolo` 相同的自动审批规则；高风险 bash 命令仍需审批。

- **TUI Bash 工具结果显示优化**
  - Bash 工具结果现在内联显示命令状态：`(执行中)`、`(执行成功)` 或 `(执行失败（退出码 N）)`。
  - 状态根据工具执行状态、错误和退出码推断，提供准确反馈。

- **系统提示词中的工具执行协议**
  - 系统提示词现在会声明工具执行协议（并行/串行以及每批并发上限），使模型的工具调用分组与本地执行器保持一致。
  - 有界并行工作池新增单项快速路径，跳过 goroutine/通道开销，覆盖常见的单工具回合。

- **审批/提问快照投递**
  - 审批与提问事件现在会附带运行时快照一起发布，错过实时帧的客户端（断开的 WebSocket、延迟订阅）仍能通过运行时投影看到待处理决策。
  - WebSocket 订阅/恢复时会发送重放后的运行时快照，弥合持久化重放与实时事件转发之间的空档。
  - 409 冲突响应会携带活动运行 ID，客户端可据此对齐视图并呈现停止控件。

- **跨端 CI**
  - 新增 GitHub Actions 工作流，覆盖 Go、Web UI、桌面端与安装包任务，并提供 `make test-all`/`test-ui`/`test-desktop`/`test-npm`/`test-pypi` 目标。

### 🐛 修复

- **审批记录自死锁**
  - 审批请求现在在 `approvalMu` 之外持久化，记录审批不再因自身互斥锁阻塞 Agent。

- **运行取消时审批自动拒绝**
  - 审批持久化期间发现运行已被取消时，请求会自动拒绝并留下决议记录，不再泄漏为待处理状态。

- **会话执行并发加固**
  - `APISession.Execution` 现在通过 `ensureExecution()`/`executionRuntime()` 访问器由 RWMutex 保护，后台工具进度与请求处理不再并发竞争会话执行。
  - ESM 协调器 `RunRole` 增加 agent manager 空值保护，并修复了 Web UI 中 SSE `readSSE` 的 CR/LF 分块边界处理。

## v1.2.86

### ✨ 新功能

- **OpenAI 后台运行重试**
  - OpenAI Responses 后台运行请求现在携带幂等键，并对可重试的失败按照配置的重试策略以指数退避自动重试。

- **频道运行时配置同步**
  - 频道（微信/飞书）运行和子代理现在使用与 Web UI 相同的 provider/模型/重试配置；通过 serve API 应用设置时会重建频道分发器运行时。

### 🔧 改进

- **重试设置即时生效**
  - TUI 在保存重试设置后会立即应用到当前 provider，无需重启。

- **Serve 设置 API 错误上报**
  - serve 设置 API 在应用设置失败时现在返回 HTTP 500 并携带错误详情，而不是仅记录日志。

- **GLM 5.3 替换 GLM 5.2**
  - `glm-5.2` 已被大部分厂商下线，现已在所有受影响供应商中替换为 `glm-5.3`：智谱 AI（`zai`、`zai-coding-cn`）、Gitee AI、Moark、火山引擎 AgentPlan/CodingPlan（仅保留已有的 `glm-5.3` 条目）、阿里百炼 Token Plan、华为云 ModelArts、京东智联云 JD Plan、百度千帆 Token Plan、CodePlayz（`opencode-go`）、OpenRouter（`z-ai/glm-5.3`）、Vercel AI Gateway（`zai/glm-5.3`）与 Cloudflare Workers AI（`@cf/zai-org/glm-5.3`）。均为纯文本输入模型。

### 🐛 修复

- **TUI Esc 中止并终结持久化运行**
  - 按 Esc 现在会在接受下一条输入之前取消并终结持久化运行；运行启动失败会以错误提示呈现，而不再静默卡住。
  - 已取消运行流中缓冲的过期事件会被忽略（新运行已安装新的事件通道），不再干扰或污染下一次运行。

- **Web UI 过期运行事件过滤**
  - 在接受替代运行或刷新后，被取代运行的延迟事件会被过滤，废弃流不再覆盖活动运行的对话记录或状态。
  - 运行生命周期版本号保护历史重新加载、运行时快照、响应运行轮询和停止处理，避免过时响应覆盖最新状态。

## v1.2.83

### ✨ 新功能

- **统一的 Agent Runtime**
  - 引入 `internal/agentruntime` 作为 TUI、Web UI/Serve、Channels、ACP 和 A2A 共享的权威运行时层。
  - 将 session 生命周期、持久化 run、决策/审批状态、run 事件和投递统一为同一条路径。
  - 各适配器现在将权威事件投影到各自的协议，而不是维护并行的状态机。

- **Web UI 项目会话管理**
  - 新增项目作用域的会话列表，支持搜索、重命名和删除。
  - 空或未命名的会话从历史中过滤；生成的会话标题会被持久化。
  - 会话元数据现在通过 serve API 存储和暴露。

- **ESM 后端任务系统**
  - 新增 Extended Streaming Mode 后端任务，并提供统一的运行时适配器。
  - ESM 目标、进度和恢复状态现在通过共享运行时流转。

- **Web UI Cookie 认证**
  - Serve 现在支持基于 Cookie 的 Web UI 认证。

### 🔧 改进

- **运行时控制**
  - 新增最大思考层级配置，并在重连后稳定运行时控制状态。
  - 任务启动期间现在显示工作进度。

- **错误处理**
  - 统一所有适配器的错误处理和重试逻辑。

### 🐛 修复

- **SkillHub 预检**
  - SkillHub 安装预检现在保持只读。
- **会话标题**
  - 最新生成的会话标题现在被正确保留。

## v1.1.82

### ✨ 新功能

- **交互式 TUI 国际化**
  - `settings.json` 新增 `tuilang` 配置，支持 `auto`、`zh` 和 `en`。`auto` 仅在 UTC+08:00 使用中文；`/settings` 菜单支持全局/项目保存范围，修改成功后立即生效。
  - 无论选择哪种界面语言，slash 命令语法始终保持英文。

### 🔧 改进

- **统一任务终态**
  - 每次 agent 运行都会发出唯一的规范事件 `EventRunFinished`，并携带 `success`、`incomplete`、`failed` 或 `canceled` 之一的 `TaskStatus`。
  - TUI、Web UI/Serve、channels、A2A、ACP、子代理和 workflow 现在都基于该事件判定结果；旧版 `EventDone`/`EventError` 事件仍保留以兼容现有消费者。
  - 用户中断、超时或 context 取消现在明确报告为 `canceled`；TUI 显示状态提示，不再显示红色错误。
  - 事件流在未发出终态事件时关闭，现在报告协议失败，不再被视为成功完成。

- **Provider 稳定性**
  - OpenAI Responses 流现在会对流事件报告的可重试失败进行重试。

- **调试体验**
  - 交互式 TUI 启用 `--debug` 时，会在启动阶段向终端打印一次 pprof 服务地址；持续的 provider 调试输出仍通过 `VIBECODING_DEBUG_LOG_ONLY` 写入 `debug.log`。

## v1.1.79

### 💥 不兼容变更

- **Workflow DSL 迁移到 JavaScript**
  - 将基于 Emacs Lisp 的 workflow DSL 替换为使用 goja 运行时的 JavaScript 实现。
  - 移除了 `vibeEmacsLispVm` 依赖和 Elisp 解释器。
  - 提供内置函数：`agent`、`phase`、`parallel`、`series` 用于 workflow 定义。
  - **不兼容变更**：Workflow DSL 语法从 Elisp 变为 JavaScript。用户需要相应地更新他们的 workflow 定义。
  - 保持了现有的 workflow 语义和 API 兼容性。

### 🐛 问题修复

- **Channel 运行状态同步**
  - 修复 channel（微信/飞书）运行状态不同步：取消 channel run 现在会同时 Abort agent（与 background 运行时对齐），不响应 context 取消的等待不再永久持有 session 运行时锁，`/new` 不再误报任务仍在执行。
  - 根治子代理终态事件丢失：`AgentManager` 新增终态生命周期监听器，channel dispatcher 将每次运行的根 agent 映射到所属会话；当子代理比父事件流活得更久（异步 `subagent_spawn`、运行取消、强制轮转）时，`done`/`error` 终态仍会送达观察者，子代理不再永远卡在 “运行中”；外部子代理接收端对事件流与监听器双路径投递的终态事件去重。
  - 修复测试基件竞态：channel 子代理测试 provider 原先按全局调用顺序分发响应，而父 agent 的后续调用与子的首个调用存在先后竞争；现改为按请求内容路由，子代理观察者相关测试转为确定性。

### ✨ 新功能

- **Channel 运行管理**
  - 新增 channel 运行看门狗：超过 `agent.run_stale_timeout_secs`（默认 600s）无 agent 事件、或总时长超过 `agent.run_max_duration_secs`（默认 4h）的 run 会被强制停止，持久化 run 状态收敛到终态。
  - 新增 channel `/stop` 命令、`/new force` 与 `/clear force`（先取消当前任务并等待宽限期再轮转）、非强制轮转时的占用提示、`/status` 展示运行状态，以及上一条消息仍在执行时的排队提示。
  - dispatcher 回退路径的会话轮转不再阻塞等待运行时锁；两条轮转路径统一为 try-lock + force 语义。
  - durable background 轮询新增全局上限（`api.backgroundRunMaxSeconds`，默认 6h），实时循环与重启恢复循环均生效；远端永不终态的 run 会以 `incomplete` 收尾并释放运行时锁。
  - agent 提问等待现在同时响应 context 取消，无人值守的运行时不会因等不到回答而永久挂起 run。

## v1.1.78

- 后台 run 的 `Idempotency-Key` 现在会在已有 run event 中记录非敏感请求指纹；同一 key 搭配不同消息会明确返回冲突，兼容重试仍复用原 run，不新增表结构。
- 工具执行记录复用前新增 session/turn/provider/tool/args 一致性校验；execution key 碰撞时拒绝复用，避免损坏记录导致错误的副作用结果。
- 工具结果写回增加状态条件保护；旧进程在 abandon 或新恢复后迟到的结果不会覆盖当前记录。
- TUI 现在会从共享 session 数据库发现未终态的 durable background run，并在终态回放提交后的 assistant 文本，重启后不再丢失后台结果。
- Channels background 完成事件现在记录 canonical assistant entry 和待投递标记；dispatcher 重启后收到下一条入站消息时会补投最终文本/附件，并通过 run event 防止重复投递。
- Channels background 的受限工具开始/结束状态也会写入 run event，重启补投时按事件顺序恢复后再发送最终结果。
- Channels background 运行期间也会通过原有 `ProgressFunc` 实时收到受限工具状态，断线后仍以 run event 补投。
- Responses attachment 的 provider 归一化与 WebUI 现在都会拒绝 localhost、私网、loopback 和 link-local HTTPS URL，同时保留 provider reference 供审计。
- 新增可选 provider-specific file resolver：Serve 仅允许 session 已归档的 file ref 下载，WebUI 提供文件附件下载入口，不把任意 URL 或 ref 变成代理。
- 附件下载入口增加非 OpenAI provider 的可选 resolver 回归验证，公共 Provider 接口仍保持不变。
- OpenAI Responses hosted item 现在由包内 descriptor registry 统一声明 capability、恢复策略和 attachment policy；未知上游类型仍保留归档但不会被猜测执行或下载。
- Background Responses 的 `incomplete` 终态现在保留已生成的部分文本、附件和 `incomplete_reason`，不会再被错误转换为 `failed`。
- Background poll/recovery 遇到明确的远端状态失效（权限、404/410、lineage 或 expired）现在也会复用本地 replay；普通 429/5xx 不会误触发回退，单个本地 run 最多自动 replay 一次。
- Code Interpreter 支持现有 hosted 配置 map 内的 MothX 私有 `mothx.maxCalls`/`mothx.timeoutSecs`；这些字段不会发送给上游，超额后台结果保留为 `incomplete`，超时会取消 runtime。
- Remote MCP hostname 增加确认式 DNS 私网预检；明确解析到私网/loopback/link-local 才拒绝，DNS 失败或超时保持放行，不把本地预检冒充上游 egress 控制。
- Code Interpreter file citation 现在保留 `container_id` provenance，并在 OpenAI resolver 中使用容器文件下载接口；普通 file ref 行为不变。
- WebUI 现在读取 `/api/capabilities` 的 Responses attachment download 能力；只有服务端明确报告不支持时才隐藏下载入口，未知 provider 不会被误判为不支持。
- WebUI capability 请求现在独立降级；旧服务或 capability API 不可用时不会阻断会话列表和聊天数据加载。
- `/api/capabilities` 现在同时暴露 provider-neutral `attachmentDownload`；非 OpenAI provider 只要实现可选 resolver 也能正确声明下载能力。
### 🔧 改进

- **Responses hosted 生命周期与恢复审计**
  - hosted item 的 added/done 生命周期现在贯通 provider、agent、Serve、TUI、channels、WebUI 和 public SDK；状态可通过 SSE/transcript 观察，并在 run event 中重连重放。
  - hosted 状态投影使用统一的字段白名单、标量限制和长度限制；工具执行中断会明确显示为 `interrupted`，不会伪装成普通失败或隐式重试。
  - 工具恢复会记录自动只读恢复或用户确认恢复原因；channels 平台提供稳定消息 ID 时支持作用域幂等键。

### 🧪 测试

- 增加 hosted lifecycle fixture、capability profile、恢复审计、跨入口事件 bridge 和 race 测试覆盖。

### ✨ 新功能

- **Web UI MCP 配置**
  - 在设置页面新增全局 MCP 配置编辑，并可从当前会话打开项目级 MCP 配置；两者共用现有 `mcp.json` schema。
  - 支持 `stdio`、streamable HTTP 和 legacy SSE 传输方式，以及命令参数、请求头、环境变量、配置模板、校验和原子持久化。
  - 新建 Serve 会话会加载保存的全局/项目 MCP 配置，并在 agent prompt 冻结前注册 MCP 工具。

- **原生 OpenAI Responses Web Search**
  - OpenAI Responses provider 现在会自动暴露原生 hosted `web_search` 能力，不再要求启用 MothX 本地 web search 设置。
  - 原生 provider 工具与可配置的本地搜索开关保持区分；当两者最终映射到同一上游 hosted tool 时会自动去重。

- **大工具结果预压缩摘要**
  - 超过大结果阈值的工具输出会在整段对话压缩前分别生成摘要，保留文件路径、标识符、命令、错误、决策及其他继续任务所需的关键信息。
  - 摘要请求采用有界并发，并会对临时限流错误重试；同时保持原有工具结果的顺序和身份，降低上下文压力。

### 🔧 改进

- **Provider 流式响应空闲超时与自动重试**
  - 流式 HTTP client 不再设置固定的 30 分钟总时长限制，只有上游长时间没有发送数据时才会超时，因此持续输出的长 SSE 流可以继续运行。
  - 在产生可见输出前发生流式超时的 agent 回合会自动重试最多两次，并向消息 channel 展示进度；重试耗尽后返回友好的错误信息。

- **上下文压缩超时与配置清理**
  - 压缩不再使用独立的五分钟截止时间；改由 provider 流式超时和调用方取消信号统一控制。
  - 移除未使用的空闲压缩配置和 UI 控件。上下文压缩现在仅在上下文压力达到阈值或显式请求时触发。

### 🧪 测试

- 新增流式空闲超时处理、自动重试与恢复、大工具结果压缩、限流重试及更新后上下文压缩行为的测试覆盖。

### ✨ 新功能

- **Web UI 中展示 Channel 子 Agent**
  - Channel 自有的子 Agent 事件现在会桥接到 Web UI 会话运行时；现有子 Agent 页面和 API 可以查看实时进度、工具调用、结果、完成状态及 transcript。
  - Channel 命令新增 `/help`，并同步子 Agent 生命周期更新，同时不转移 channel dispatcher 对运行时的所有权。

### 🔧 改进

- **微信消息投递**
  - 长回复现在会在安全的 UTF-8 边界拆分，并显示剩余投递次数。达到单条消息推送上限后，额外内容会排队，可通过 `/more` 继续接收。
  - 进度消息也会共用同一投递额度，并保留入站微信消息 ID，用于 channel 作用域的幂等与投递跟踪。
  - 截断的 HTTP/SSE 响应（`io.ErrUnexpectedEOF`）现在会被视为可重试的 provider 失败。

### 🧪 测试

- 增加 channel 子 Agent 集成、外部子 Agent HTTP、dispatcher 生命周期、MCP editor、session view、微信分片/投递及截断流重试测试覆盖。

## v1.1.77

### ✨ 新功能

- **模型级 `disableSamplingParams` 兼容标志（默认开启）**
  - 模型配置新增 `compat.disableSamplingParams`（三态）。默认开启：除非模型显式设置 `"disableSamplingParams": false` 选择启用，否则不再向任何模型发送 `temperature`/`top_p`。
  - 可在 TUI(`/auth` → 模型 → E. Compatibility → Disable Sampling Params，循环 auto → enabled → disabled）和 Web UI 设置的模型表格（允许采样列，勾选 = 发送参数）中配置。
  - 注意：通过 serve API 传递 `temperature`/`top_p` 的 OpenAI 兼容客户端，现在需要模型显式 opt-in 后这些值才会传给 provider。

### 🔧 改进

- **Web UI 历史会话排序与时间显示**
  - 会话列表现在按最后使用时间倒序排列，最近回复的会话显示在最上方。
  - Web UI 会话管理页面显示每个会话的最后回复时间；左侧历史会话仅保留最新优先的排序，不额外显示时间。

- **思考/推理模型自动抑制采样参数**
  - Anthropic：开启 extended thinking 时自动丢弃 `temperature`/`top_p`，符合 API 对 thinking 与采样参数互斥的要求。
  - OpenAI:chat completions 使用 OpenAI 风格 `reasoning_effort`、以及 Responses API 携带 `reasoning` 块时，自动省略 `temperature`/`top_p`(OpenAI 推理模型会拒绝这些参数）。

### 🧪 测试

- 为 Anthropic、OpenAI(chat completions + Responses)、Google 三个 provider 增加采样参数抑制测试，覆盖默认丢弃、显式 opt-in 透传与 deepseek 格式保留场景。

### 🐛 修复

- **Web UI 会话模式更新即时生效**
  - 修改会话运行时模式或能力后，Web UI 现在会立即应用服务端返回的权威运行时状态，不再等待会话列表刷新完成。与此同时会更新本地会话缓存；即使后台刷新暂时失败，模式控件也不会保持禁用或显示过期状态。

- **长会话上下文超限后永久卡死（微信/飞书 channel 尤为明显）**
  - 当 provider 因上下文窗口超限拒绝请求时（token 估算低估真实用量时可能发生，中文聊天场景更常见），agent 现在会先尝试 LLM 压缩后重试一次；如果压缩请求本身也超限，则降级为确定性截断（丢弃最旧的消息、仅在用户/助手轮次边界切割）再重试，而不是每条后续消息都报 `responses stream failed` 直到 `/new`。
  - 本地估算超过输入预算时的 context guard 错误路径同样接入该恢复逻辑。
  - 截断结果会作为 compaction 条目持久化，channel 会话（每条消息重建 agent）重载后不会再次加载超限的历史。
  - OpenAI Responses API 的 `response.failed` 事件现在会解析嵌套在 `response.error` 中的错误详情（Kimi 等服务端将失败原因放在此处），不再只显示笼统的 `responses stream failed`；channel 会话也会把压缩/恢复进度推送到消息平台。

- **Web UI 聊天丢失会话上下文**
  - Web UI 聊天使用的 submit-run API 从未将持久化的会话历史回放到 agent 中，导致每轮都以空上下文开始，模型表现得像全新会话（在切换 mode 后尤其明显）。后台 run 现在会加载会话 replay 状态，与 chat-completions 路径行为一致。
  - submit-run 请求体中的 `images`、`tools`、`skills` 字段此前被解析但静默忽略，现已生效：图片会按模型输入能力校验并作为图像内容块发送；`tools` 数组作为会话权威的能力开关集合应用；`skills` 用于激活会话技能（未知的 tool/skill 名称返回 400）。
  - 请求体中显式传入的 `mode` 现在会持久化到会话，后续 run 继续生效。

### ✨ 新功能

- **完整的 OpenAI Responses 运行时**
  - `api: "openai-responses"` 从基础流式支持扩展为完整的 Responses 运行时：支持原生 response item 回放、`previous_response_id` 与 conversation 状态模式、reasoning context/mode、prompt cache 控制、service tier、结构化输出、hosted tools 及工具调用控制；请求发送前会按当前模型的兼容能力进行校验。
  - Responses 会以原生 item 形式归档并关联到本地 turn，在 session 回放中保留 reasoning、引用/产物、工具输入和 response lineage。工具执行以 response turn 为幂等范围，provider 重复发送 function-call item 时不会重复执行工具。
  - Provider 配置 `responses.background: true` 可在 Serve 中启用可持久恢复的远程 Responses 后台运行。MothX 会轮询任务、在服务重启后恢复、执行本地 function/custom tool、持久化生命周期事件和结果，并通过 Responses run API 支持查询、取消、重连和放弃任务。

- **`-P --json` 打印模式流式 NDJSON 输出**
  - 在 `-P`（打印模式）下新增 `--json` 标志，将响应以 JSON Lines / NDJSON 流式输出到 stdout——每个事件一行 JSON，消费方可在事件到达时逐行读取，无需等待运行结束。
  - 每个 agent 事件各占一行：`start`（provider/model/mode）、`text_delta`、`think_delta`、`tool_call`、`tool_execution_start`、`tool_execution_end`、`tool_result`（含 `name`/`arguments`/`result`/`error`/`diff`）、`plan_update`、`usage`（token 与费用）、`context_usage`、`compaction_start`/`compaction_end`，以及作为流结束信号的 `done` 或 `error` 事件。所有进度、调试与诊断信息仍走 stderr，stdout 始终为纯 NDJSON。
  - 适用于 CI、自动化脚本和与外部程序集成场景。拼接助手文本：`mothx -P --json "总结" | jq -r 'select(.type=="text_delta").text' | tr -d '\n'`。以类型化对象查看整条流：`mothx -P --json "总结" | jq -s '.'`。

- **Serve 状态与能力中的 Web Search 可用性**
  - serve API 现在在 `/api/status`、session capabilities 和默认 session 创建中同时反映 `serve.json` 与 app-level `settings.json` 的 web search 配置。
  - Web UI 工具开关和状态快照在保存设置或 serve 配置后保持同步刷新。

- **桌面端 Release Workflow**
  - 新增 `.github/workflows/desktop-release.yml`，在推送 tag 时自动构建并发布桌面端安装包。
  - 支持 macOS 代码签名（`CSC_LINK` 支持 base64 或文件路径）、Windows 和 Linux 构建，使用 `electron-builder`。
  - 自动生成 SHA-256 checksums，并根据 tag 是否包含 `pre` 自动设置 prerelease 与 release notes，上传到 GitHub Releases。

- **新增模型：`deepseek-v4-flash-0731`**
  - 在 Gitee 与 Moark 供应商中新增 `deepseek-v4-flash-0731` 快照模型，支持 1M 上下文窗口与推理（最大输出 token 上限由供应商发布）。

### 🔧 改进

- **Responses 可靠性与重试处理**
  - Responses 流错误现在会纳入共享 provider 重试策略，而非立即失败。Cloudflare HTTP `524` 源站超时同样可重试，提升了短暂上游或网关异常时的稳定性。

- **Web UI 设置体验优化**
  - 保存 provider、app 和 serve 配置后立即刷新相关设置视图，使功能标志和运行时状态即时更新。
  - 聊天 Composer 的工具开关增加 title 提示，提升可发现性。

- **桌面端构建加固**
  - 提升 Electron 安装重试次数，并增加镜像源 fallback（`ELECTRON_MIRROR` → `npmmirror` → 默认源），提高 CI 稳定性。
  - 修复构建脚本中的 ESM 路径解析，并修正 macOS Electron 可执行文件路径检测。
  - 新增 `version:set` 脚本，从 release tag 同步更新 `package.json`、`package-lock.json` 和 runtime 版本元数据。

- **Serve Session 创建一致性**
  - 统一 `getOrCreateSession`、`defaultSessionCapabilities` 和 channel runtime status snapshot 中的 web search 能力解析逻辑。

- **Channels 会话选择器改为原生下拉框**
  - 将 Channels 设置页面的自定义会话下拉菜单替换为原生 `<select>` 控件，提升可访问性、键盘导航和移动端体验。

### 🧪 测试

- 新增 `settings.json` 和 serve config 对 `getOrCreateSession` 及 status handler 中 web search 行为的覆盖测试。

### 🐛 修复

- **Responses 工具调用与 Channel 会话选择器**
  - 在每个 response turn 内去重原生 Responses function/custom tool 调用，避免上游重复 item 导致工具重复执行，同时不同 turn 中合理的相同调用仍会执行。
  - 修复切换 channel 配置时 Channels 设置页面的会话选择器可见性问题。

- **Web UI 聊天与后台运行**
  - 修复 Web UI 后台运行不加载持久化会话历史的问题。提交的 run 现在会在调用 provider 前回放 session，包括切换运行模式后的下一轮对话。
  - submit-run 请求现在支持可选的 `images`、`tools` 和 `skills`：图片会按模型输入能力校验，`tools` 作为会话能力的权威集合，未知 tool 或 skill 返回 `400`；显式 `mode` 会持久化到后续运行。
  - 刷新页面后，如果服务端 run 处于 queued、running、cancelling 或 terminalizing 状态，会话仍保持忙碌。回放和实时 WebSocket 流中的生命周期事件统一处理，重连后停止按钮和运行状态保持正确。
  - 移除聊天界面重复的工具流卡片；工具活动仍可通过会话事件和工具结果视图查看。运行期间输入框会显示运行中提示。

- **Web UI 设置与模型选择器**
  - 移除设置概览中重复的功能开关卡片；功能配置仍可在 Serve 配置页面中使用。
  - 修复默认模型搜索选择器选择模型后重新展开的问题。现在会在更新选择值前可靠收起，并阻止焦点转移导致选择器重新打开。

## v1.1.76

### ✨ 新功能

- **空响应自动检测与重试**
  - 当 provider 返回空响应时，Agent 会自动重试请求，避免因网络抖动或上游短暂异常导致对话中断。

- **`vibe-browser` 升级至 v0.1.5**
  - 更新浏览器自动化技能，带来更好的页面交互和截图能力。

### 🔧 改进

- **Web UI 与 serve API 渲染 `insert` 工具**
  - Web UI 现在以专用调用视图渲染 `insert` 工具调用，展示目标路径、结构化位置（head/tail/before_line/after_line + 行号）、内容大小以及 `dry_run`/`dedupe`/`create_if_missing` 标志。
  - serve API 折叠模式工具格式化现在会渲染 `insert` 的 diff（此前仅 `edit`/`write`）；messaging channel 的进度行也会带上 `insert` 的路径。

- **Provider 预设回退统一**
  - 统一 `ResolveKey`、`ResolveProviderHeaders` 和 factory 中的预设回退逻辑，确保不同 entry point 使用相同的 provider 检测与 header 注入行为。

- **腾讯混元套餐移除 `hy3-preview`**
  - 从 `tencent-hy-plan` 与 `tencent-hy-plan-anthropic` 中移除 `hy3-preview`；腾讯云套餐目前仅提供 `hy3`。

- **文档图片优化**
  - 将架构、对比、模式等宣传图片转换为 webp 格式，减少文档体积并改善加载速度。

### 🐛 修复

- **GLM-5.2 与 Kimi K2.7 Code 为纯文本模型**
  - 修正了 `glm-5.2` 与 `kimi-k2.7-code`（含 `kimi-k2.7-code-highspeed` 变体、Fireworks 的 `accounts/fireworks/models/kimi-k2p7-code` 与 `accounts/fireworks/routers/kimi-k2p7-code-fast` 路由，以及路由 ID `moonshotai/kimi-k2.7-code`、`zai/glm-5.2`、`@cf/moonshotai/kimi-k2.7-code`、`@cf/zai-org/glm-5.2`）在多个供应商下被错误标记为多模态的问题。这些模型仅支持文本输入，`Input` 能力现改为仅 `text`，不再向其投递图片/附件载荷。已同步更新 `internal/config/settings.go`、`docs/provider-model-list.md` 与 `docs/models.md`。`glm-5v-turbo` 等真正的多模态模型保持不变。

## v1.1.75

### ✨ 新功能

- **输出 Token 截断自动恢复**
  - 当模型触达输出 token 上限时，Agent 会自动提升 max_tokens 上限（最高到模型原生输出限制）并重试；若仍截断，则进入「从中断处续写」恢复循环（最多 3 次），指示模型从中断位置继续输出，不重复已生成的内容。
  - 所有已知模型默认使用保守的 8192 token 输出上限，避免过度消耗 token；用户显式设置的 max_tokens 值始终原样保留。重试和截断状态通过事件推送到 TUI、serve API（SSE）和 Web UI。

- **内置模型与自定义模型列表合并**
  - 服务商模型列表现在会合并内置预设与用户自定义模型，而不是完全替换内置列表。自定义模型按 ID 优先覆盖；未被显式配置的内置模型仍会保留其完整预设参数。

### 🔧 改进

- **移除旧版配置目录迁移**
  - 删除了 `.vibe` → `.mothx` 和 `.vibecoding` → `.mothx` 的一次性自动迁移逻辑。项目路径工具函数现在位于 `internal/config/paths.go`，不再携带迁移副作用。

- **TUI 与 Web UI 截断状态提示**
  - TUI 在响应因输出 token 上限被截断时显示警告，并在自动重试进行中显示状态行。
  - Web UI 在聊天运行事件列表中显示来自 SSE 流的重试/状态事件。
  - serve API 在输出截断时返回 `finish_reason: "length"`，而不是一律返回 `"stop"`。

- **`insert` 工具在 Web UI 与 serve 侧的渲染适配**
  - Web UI 现在以专用调用视图渲染 `insert` 工具调用，展示目标路径、结构化位置（head/tail/before_line/after_line + 行号）、内容大小以及 `dry_run`/`dedupe`/`create_if_missing` 标志，并提供与 `write` 一致的内容预览。
  - serve API 折叠模式工具格式化现在会渲染 `insert` 的 diff（此前仅 `edit`/`write`）；messaging channel 的进度行也会带上 `insert` 的路径。

- **腾讯混元套餐模型列表**
  - 从 `tencent-hy-plan` 与 `tencent-hy-plan-anthropic` 中移除 `hy3-preview`；腾讯云套餐目前仅提供 `hy3`。

## v1.1.74

### ✨ 新功能

- **结构化文本插入工具 `insert`**
  - 新增专用文本插入工具，用于在已有 UTF-8 文本文件的头部、尾部、指定行之前或之后插入内容。
  - 支持自动换行、`exact`/`trimmed`/`line` 去重、`dry_run` 预览、结构化插入结果和 diff。
  - 对大于 32 MiB 的文件使用流式插入，配合文件锁、UTF-8/二进制校验、并发修改检测、`fsync` 和原子 rename，避免破坏原文件。
  - 与 `edit` 保持明确边界：文本匹配、替换和删除继续使用 `edit`；`insert` 不提供 `match`、正则或 occurrence 模式。

### 🔧 改进

- **火山引擎 Doubao 模型更新**
  - AgentPlan / CodingPlan 下线 `doubao-seed-2-0-code` 和 `doubao-seed-2-0-pro`，新增 `doubao-seed-2.1-turbo`。
  - 新增 `doubao-seed` 思考格式：`reasoning_effort` 支持 `minimal`（不思考）、`low`、`medium`、`high`（默认）四种模式，适用于 Doubao Seed 2.1 / Evolving 模型，按模型 ID 自动检测。

- **npm 发布代理可见性**
  - npm 按需发布脚本使用 `all_proxy`、`https_proxy` 或 `http_proxy`（包括大写变体）时，现在会输出 `use [proxy ...]`；日志会脱敏代理凭据。

### 🐛 修复

- **Windows SQLite 会话路径**
  - 修复 Windows 盘符路径被序列化为错误 SQLite file URI authority 的问题，避免创建会话时报 `invalid uri authority: C:`。

## v1.1.73

### ✨ 新功能

- **MCP PATH 命令解析与健壮的客户端生命周期**
  - stdio MCP 服务器命令不再需要绝对路径；命令通过 `PATH` 解析，并正确合并环境变量（Windows 下大小写不敏感，且无重复项）。
  - 新增入站请求队列，为 sampling 和 notification 消息提供背压机制。
  - 现在会校验 JSON-RPC 响应 ID 是否与请求 ID 匹配；不匹配的响应将被拒绝。
  - HTTP 响应体解析上限为 16 MiB；SSE 多行 data 负载以换行符连接。
  - 客户端生命周期使用按客户端隔离的 context 与 cancel；`Close` 会取消进行中的请求并关闭空闲 HTTP 连接。
  - 资源和 prompt 发现错误（非 method-not-found）现在会导致 `ConnectServers` 失败，不再被静默忽略。
  - MCP 配置文件现在通过临时文件 + rename 原子写入，权限为 `0600`。
  - ACP 新建会话时 MCP 失败会回滚已持久化的会话。
  - SSE `messageUrl` 被校验为 `http`/`https`；任意 scheme 均被拒绝。

### 🔧 改进

- **统一会话 Schema 管理**
  - 用 `schema.go` 中的统一完整 schema 定义替换了增量迁移逻辑（`migrations.go`），使用 `EnsureCurrentSchema()` 实现幂等初始化。
  - 实现了健壮的 SQLite 连接处理，启用 WAL 模式和 busy timeout。
  - 新增 `CloseDatabases()` 用于退出和重载时的正确清理，以及 `OpenStandaloneDB()` 供调用方管理数据库连接。
  - 修复了 cron 调度器 goroutine 生命周期管理，使用 `WaitGroup` 防止提前退出。

- **Web UI 审批与会话取消**
  - 取消运行中的 Web UI 会话时，现在会中止该会话的活动 Agent，包括尚未进入待处理队列的审批等待，避免会话一直停留在运行状态。
  - 审批请求与终态决议严格归属于对应 session 和 run，并持久化到 `session.db`；run 结束时，所有未决审批都会记录为已取消。
  - 审批面板现在会自动选中队列中的第一个待处理审批；当审批队列清空时，也会清理过期的审批状态。
  - 刷新页面或重新进入运行中的 session 后，仍可通过发送按钮旁的“停止”按钮取消服务端运行；终止完成后可继续发起新对话。

- **安装包版本管理**
  - npm、PyPI 及所有平台专用 npm 安装包版本统一升级至 `1.1.72`。

- **Web UI 移动端响应式适配**
  - 侧边栏在 ≤ 900px 屏幕下折叠为移动端抽屉（汉堡菜单模式），采用 Svelte 原生 `matchMedia` store + `{#if}` 条件渲染 + `transition:fly`/`fade` 动画；遮罩层和抽屉仅在展开时存在于 DOM 中，避免点击穿透问题。
  - 统计页面移动端适配：KPI 网格切换为 2 列，趋势图/排行网格纵向堆叠，图表内边距适配窄屏，最近请求表格渲染为卡片列表。
  - 历史对话页面在移动端从表格转换为卡片列表，包含紧凑的元信息行和内联操作按钮。
  - 输入框区域移动端适配：控件自然换行不使用滚动容器（避免 select 下拉被裁切），textarea 使用 16px 字号防止 iOS 自动缩放，runtime/skill 弹出面板使用 `position: fixed` 底部弹层模式防止溢出屏幕。
  - 更新 AGENTS.md，新增 WebUI 相关约定文档，说明 Svelte 原生移动端适配方案。

## v1.1.72

### ✨ 新功能

- **扩展项目技能目录**
  - 项目本地技能现在支持从 `.agents/skills/<name>/SKILL.md` 加载，并继续兼容已有的 `.mothx/skills`、`.skills` 和 `skills` 目录。

- **Web UI 聊天与技能控制**
  - 聊天界面新增按会话启用技能的选择器；选中的技能会随 completion 请求发送，并立即刷新会话上下文。
  - 新增服务端会话运行取消功能，Web UI 的停止操作在重连或刷新页面后仍可生效。
  - 流式请求失败时现在会发送结构化 SSE 错误事件，并在聊天记录中显示失败状态和原因。
  - Markdown 消息、技能引用和编辑预览新增可折叠的语法高亮代码块及复制按钮。

- **SkillHub 安装目标选择**
  - Web UI 现在支持在安装单个技能或技能集前，明确选择项目技能目录或全局技能目录。
  - 新增 SkillHub API，用于列出可安装目标并替换会话的启用技能集合。

### 🔧 改进

- **运行时 Sandbox 配置**
  - 保存 Serve 配置后会立即刷新 API 服务和所有存活会话的 sandbox manager 与工具注册表，无需重启服务。
  - API 会话现在按各自工作目录隔离 sandbox manager，确保已允许的非默认工作目录及其子 Agent 使用正确的策略。

### 🐛 修复

- **Sandbox Git 与设备兼容性**
  - 启用 Git 保护时，sandbox 内的 Git 元数据保持可见，既保留正常 Git 操作，也继续支持一次性 Git 访问处理。
  - 不再将主机 `/dev` 路径重新绑定到 Bubblewrap，避免 Git 等工具拿到不可用的只读设备文件。
  - 修正 Linux `/home` 拒绝规则的规范化逻辑，避免默认 sandbox 策略错误拒绝位于 `/home` 下的项目。

## v1.1.71

### ✨ 新功能

- **扩充 Provider 模型列表**
  - 同步 `docs/provider-model-list.md` 与 `internal/config/settings.go`，反映最新模型可用性。
  - 新增多个 provider 的大量模型支持，包括：
    - **OpenAI**：新增 `gpt-4o-2024-05-13` 变体。
    - **Google Gemini**：更新模型列表与数量。
    - **Gitee / Moark**：新增独立章节并扩充模型列表，包含 `glm-5` 系列、`qwen3.x` 系列与 `kimi-k2.x` 系列。
    - **阿里百炼**：在 Token Plan 中新增 `qwen3.8-max-preview`、`qwen3.7-plus` 与 `glm-5.2`。
    - **腾讯混元**：新增 `hy3` 与 `hy3-preview` 模型。
    - **百度千帆**：新增 `Token Plan` 子章节并扩充模型支持（`deepseek-v4`、`glm-5.2`、`kimi-k2.6`、`ernie-5.1`）。
    - **Kimi Coding**：显式新增 `ThinkingFormat: kimi` 支持。
  - 更新快速参考表，反映准确的模型数量与新增的 `kimi` 思考格式。

### 🐛 修复

- **显式零值 maxTokens 支持**
  - 修复了模型配置中 `maxTokens: 0` 与未设置值无法区分的问题，此前用户无法通过设为零来禁用输出 token 限制。
  - 为 `ModelConfig` 新增 `fieldSet` 追踪机制，确保显式设置的零值在 JSON 序列化/反序列化过程中被保留，并正确传递给 provider。
  - Anthropic provider 的 `max_tokens` 字段改为 `*int` + `omitempty`。由于 Anthropic Messages API 强制要求 `max_tokens` 且会拒绝超过模型输出上限的值，显式零值会回退到默认值 16384，而不是省略该字段；OpenAI/Google 风格端点则会省略字段并遵循禁用限制的语义。
  - 更新 `ResolveMaxTokens` 和 serve API handler，识别显式零值并跳过 fallback 默认值；客户端传入的负数 `max_tokens` 会先归一化为零。
  - TUI 模型编辑器现在能在编辑状态往返中正确保留显式零值 maxTokens。

- **Web UI 会话历史与失败原因展示**
  - 修复从默认会话发起新 Web UI 聊天时，新建 session 不会立即出现在侧边栏/历史列表中的问题。
  - 新增 Web UI 新会话启动阶段的乐观会话列表更新，并在后续自动与服务端持久化会话列表对齐。
  - Web UI 任务失败时现在会直接在聊天记录中显示失败原因，包括 completion 请求失败、会话流错误以及失败的运行事件。
  - 为失败的 assistant 消息新增错误样式，便于识别已停止或失败的任务。

## v1.1.69

### ✨ 新功能

- **Web UI 会话运行时控制与审批**
  - 新增按会话控制运行模式，可在不重启 Serve 的情况下切换 `plan`、`agent` 和 `yolo` 模式。
  - 新增浏览器、Web 搜索、delegate、多 Agent、workflow 和 A2A master 工具的实时能力状态，显示可用性及禁用原因。
  - 新增 Web UI 审批中心，支持处理 bash、文件写入/编辑、删除和 Git 访问请求，并提供一次性批准/拒绝以及持久化命令/路径放行规则。
  - 新增审批和工具执行会话流事件、运行时快照、运行事件审计记录，以及重连/会话恢复支持。

### 🔧 改进

- **Kimi K3 支持**
  - 在内置 Kimi 和 Kimi Coding provider 模型列表中加入 Kimi K3，支持 1M 上下文。
  - 新增 Kimi 推理等级映射，支持 `low`、`high` 和 `max` reasoning effort 值。

- **Web UI 与 Stats 品牌资源**
  - 为 Web UI、Stats 仪表盘和文档资源新增 MothX small favicon。
  - 从持久化会话运行事件恢复 Web UI 审批审计历史。

## v1.1.68

### 🐛 修复

- **Sandbox 隔离与策略一致性**
  - 将配置的 sandbox 策略应用到 Linux Bubblewrap，包括自定义二进制路径、网络访问、额外读写路径、拒绝路径、环境变量透传及临时文件系统大小。
  - OpenAI 兼容 API 改为按会话工作目录创建 sandbox manager，确保允许的非默认工作目录能够正确挂载，子 Agent 也会继承相同限制。
  - 修复 Channels 中已启用 sandbox 时子 Agent 未复用会话 sandbox manager、可能绕过隔离的问题。
  - 阻止通过额外 sandbox 路径选项重新绑定已拒绝的路径，并改进 sandbox 可用性错误信息。

### ✨ 新功能

- **SkillHub / ClawHub 技能市场集成**
  - 新增内置技能市场，支持在 TUI `/skillhub` 和 `mothx serve` Web UI 中浏览、搜索、查看详情、查看文件与安全评估、安装、更新、卸载及激活 SkillHub.cn / ClawHub.ai 技能。
  - 新增技能市场缓存与 Registry 工厂，可扩展额外市场；serve 模式提供市场、搜索、详情、安装、技能集和会话激活等 API。
  - 安装器校验压缩包大小、路径穿越、绝对路径、Windows 驱动器路径和符号链接，并保护手写技能目录不被覆盖。

### 🔧 改进

- **Sandbox 策略与 Git 保护**
  - 增加 sandbox 策略规范化、路径冲突校验、临时文件系统大小解析以及 Bubblewrap 能力探测；严格 sandbox 无法执行时会明确报告原因，不再静默降级。
  - sandbox 配置现在统一应用于 CLI、ACP、A2A、Channels、serve API 和子 Agent；serve API 默认不启用 sandbox，启用后按会话工作目录隔离。
  - 启用 Git 保护时默认拒绝访问 `.git` 元数据；需要 Git 操作时通过一次性审批临时放行，避免普通命令修改仓库内部状态。
  - 简化 sandbox 网络配置和 TUI 设置项，并修正 Linux 项目目录及 `/proc` 挂载规则。

### 🐛 修复

- **SkillHub 会话与渠道支持**
  - 修复 serve Web UI / Channels 中技能安装和激活的工作目录、会话状态及子 Agent 复用问题，确保当前会话能够及时加载已激活技能。

## v1.1.67

### ✨ 新功能

- **统一在线安装入口**
  - Unix 类系统在线安装统一使用 `https://mothx.net/install.sh`，Windows 使用 `https://mothx.net/install.bat`。
  - 脚本会复用已有 Node.js；缺少 Node.js 时先安装 Node.js LTS，再执行 `npm install -g mothx-installer` 安装最新版。

### 🐛 修复

- **损坏配置自动恢复**
  - 全局或项目级 `settings.json` 解析失败时，只要损坏文件能够成功备份，就不再阻止 MothX 启动。
  - 自动将损坏文件重命名为带时间戳的备份，例如 `settings.json.bak_20260715-143000`；文件名冲突时会追加数字序号。
  - Warning 会显示适配当前操作系统格式的真实绝对备份路径。全局配置损坏时回退到默认设置，项目配置损坏时忽略该配置并保留有效的全局设置。

- **TUI 托管 Agent 状态**
  - 修复 TUI 中托管主 Agent 的状态同步：multi-agent、delegate 和 workflow 模式下，回合开始会标记为 `running`，结束时会标记为 `done` 或 `error`。
  - workflow 模式失败时现在会把托管 Agent 状态更新为 `error`，避免标签页/状态 UI 一直停留在 `running`。

- **Delegate 子 Agent 完成等待**
  - 新增回归测试，确保 delegate 子 Agent 工具会阻塞等待子 Agent 完成，并返回子 Agent 的最终结果，而不是提前返回。

## v1.1.66

### ✨ 新功能

- **Fuzz 测试目标与 Make 目标**
  - 为 ESM 报告解析（`internal/esm/report_fuzz_test.go`）、MCP JSON-RPC 消息处理（`internal/mcp/mcp_fuzz_test.go`）以及工具类截断函数（`internal/util/truncate_fuzz_test.go`）新增 fuzz 测试。
  - 新增 `make fuzz` 目标，按可配置时长（`FUZZTIME`，默认 `10s`）依次运行所有已注册的 fuzz 目标，适配 Go fuzzing 每次只能跑一个包的限制。

### 🔧 改进

- **按模型配置输出 Token 上限**
  - 从 `settings.json` 中移除全局 `maxOutputTokens` 设置。输出上限现在完全由当前模型的 `maxTokens` 决定，避免因一个全局值在不同 provider 上造成截断或超长输出。
  - 简化 `agent.ResolveMaxTokens`，仅接收 model 参数；去掉 `MaxTokensSet` 门限，内建模型默认值自动生效，无需用户额外配置。
  - 更新 CLI/print、ACP 服务、TUI（`/model` 切换、`ensureAgent`、`?`/BTW 帮助）、OpenAI 兼容 API（`/compact` 与 chat completions）、AgentFactory 以及 ESM 子代理路径以使用新签名。
  - 从 TUI `/settings` 的 Behavior 面板移除 Max Output Tokens 字段；`SaveGlobalSettingsPatch` 会在重写稀疏配置时自动清理遗留的 `maxOutputTokens` 键。
  - 同步更新 `docs/en/configuration.md`、`docs/en/faq.md`、`README_zh.md`，移除该已废弃设置；FAQ 改为指引用户在对应模型上配置 `maxTokens`。

- **火山方舟 Plan 默认 Max Tokens 调整**
  - 将 `volcengine-agentplan` 与 `volcengine-codingplan` 下所有模型（Ark Code、Doubao Seed 2.0 系列、GLM-5.2、Kimi K2.x、DeepSeek V4 Pro/Flash、MiniMax M3/M2.7）的默认 `MaxTokens` 下调至 100K，对齐当前上游限制并统一 CodingPlan 的模型说明。
  - 修正 CodingPlan 文档备注（排除的是 MiniMax M2.7 而非 M3），新增 `TestVolcenginePlanModelsUseSharedMaxTokens` 回归测试。

- **TUI Debug 输出清理**
  - 启用 `--debug` 时，交互式 TUI 不再把流式 JSON 调试行混入 Bubble Tea 视图：调试信息仍然写入 `debug.log`，pprof 服务也照常启动，但通过新增的 `VIBECODING_DEBUG_LOG_ONLY` 环境变量抑制 stderr 输出。
  - CLI `--print` 模式（及其他非 TUI 入口）继续在 stderr 输出 `[DEBUG]` 行和 pprof 状态，启动时的 “Debug logging enabled” 横幅也仅限 print 模式。
  - `DebugCompleteResponse` 在 `json.Marshal` 失败时（例如工具调用在流式过程中累积了非法的 `json.RawMessage` 参数）会回退到结构化字符串 dump，确保畸形负载不会从 `debug.log` 中消失。
  - `debugpprof.Start` 支持传入日志写入 writer；新增 marshal 失败调试路径的测试覆盖。

- **测试套件维护**
  - 将 stats 面板的迁移期望值更新为 15（ESM 恢复列之后）。
  - 重写 TUI auth-dialog、settings-sparse 以及 zero-override 测试，改用 `maxContextTokens` 替代已移除的 `maxOutputTokens` 字段。

## v1.1.65

### 🔧 改进

- **GitHub Release 自动化**
  - 新增 `.github/workflows/release.yml`，在推送 Git tag 时自动构建并发布发布产物。
  - 工作流会先构建 Web UI（Node.js 22）和 Go 二进制文件（通过 `make dist`），然后创建 GitHub Release，并附带上 tar.gz 压缩包、`.deb` 安装包、zip 压缩包以及 SHA-256 校验和文件。
  - 包含 `-` 的预发布 tag（例如 `v1.1.65-pre`）会在 GitHub 上自动标记为 prerelease；release notes 由合并的 PR 自动生成。
- **GitHub npm 包发布工作流**
  - 新增 `.github/workflows/github-npm-publish.yml`，在每次 tag 推送时构建并发布 npm 安装包（各平台二进制包装器 + `@scope/mothx` 元包）到 GitHub Packages，并提供 `workflow_dispatch` 输入用于手动覆盖版本号。
  - 加入幂等的 `publish_if_needed` 检查，对已发布版本重复运行工作流会跳过而不是报错失败。

### 🐛 修复

- **CI npm 包构建路径**
  - 修复 GitHub npm 发布工作流在打包各平台二进制 tarball 时 `package.json` 路径解析错误的问题，避免因工作目录不正确导致发布失败。

## v1.1.63

### ✨ 新功能

- **ESM 自动恢复中断角色**
  - 新增 `RecoveryObserver` 只读子代理，在 ESM 角色（worker、critic 或 audit）因超时或传输故障中断后检查仓库状态。
  - 新增 `RecoveryObserverTaskPrompt`、`RecoveryReport` 和 `ParseRecoveryReport`，用于结构化的恢复决策（`resume` / `blocked`）。
  - TUI `recoverInterruptedESMRole` 将有限的可恢复角色失败转化为干净的 supervisor 完成，使下次 ESM 续跑可以启动新 worker。
  - 超时触发的恢复会启动 RecoveryObserver（5 分钟超时）来验证仓库状态并列出具体剩余任务。
  - 传输故障（provider 内建重试后的错误）自动恢复而无需启动 observer；新 worker 从当前状态重试。
  - DB 迁移 015 为 `session_esm_objectives` 表新增 `recovery_count` 和 `recovery_reason` 列。
  - `RecoveryLimit = 2` 允许的连续自动恢复次数；超过后暂停续跑，需 `/esm resume` 恢复。
  - 恢复状态显示在 TUI 底部栏（`recover N/2`）并包含在 steering/worker prompt 中。
  - Worker 进度成功续跑时重置恢复计数器。
  - Recovery observer 使用和 critic/audit 子代理相同的只读工具限制。

### 🔧 改进

- **TUI ESM 面板与工具弹窗增强**
  - 在 agent activity 中追踪 `FullThink`、`FullText`、`FullResult`、`LastToolName`、`LastToolArgs`，便于在 ESM 面板中完整查看子代理活动。
  - ESM 面板新增 `Now / Progress / Next` 状态，展示流水线阶段计数和剩余任务。
  - 工具弹窗直接渲染主代理的原始 assistant/thinking 内容；活动时间线条目不截断保留。
  - 工具参数键按字母序排序，确保显示结果稳定。

### 🐛 修复

- **OpenAI 兼容 Provider 工具参数解析**
  - 修复部分 OpenAI 兼容 provider（如火山引擎 Ark）以原始 JSON 对象而非 OpenAI 约定的转义字符串流式返回工具参数时，参数缓冲不是合法 JSON 导致工具执行失败的问题。
  - 引入 `openAIToolArguments`，通过自定义 `UnmarshalJSON` 同时接受字符串编码和原始对象两种形式，统一归一为原始 JSON。
  - 直接将原始参数字节流写入工具调用缓冲；在出站消息中回 Marshal 为 OpenAI 字符串形式，保持会话历史的 wire 兼容。
  - 新增覆盖字符串、对象、null 以及流式工具调用往返场景的单元测试。
  - 将火山引擎 Ark 注册为已知 provider 默认项，并加入 provider/model 文档列表。

## v1.1.62

### ✨ 新功能

- **ESM（Supervisor Mode）**
  - 新增 `internal/esm` 包，提供 Event State Memory 用于长期目标的持久化状态管理。
  - 新增 `/esm` 命令，支持 `edit`、`pause`、`resume`、`clear`、`budget` 子命令。
  - ESM 目标激活时注册 `get_esm` 和 `update_esm` 工具。
  - 通过 `AgentLoopConfig.GetSteeringMessages` 注入 ESM 引导消息。
  - TUI 底部栏显示 ESM 状态。
  - SQLite 持久化存储，新增 `session_esm_objectives` 表（迁移 010）。

- **ESM 完成审查工作流**
  - 新增 worker → critic → audit 审查流水线，用于验证完成候选。
  - 新增 `StatusCompleteCandidate`，支持结构化的 `WorkerReport`/`AuditReport` 解析与校验。
  - Objective schema 新增 `completion_review`、`completion_run_id`、`completion_reason`、`blocked_run_id` 字段（迁移 011/012）。
  - TUI ESM 编排完整审查流水线，仅审计通过才标记目标完成。
  - 限制 critic/audit 子代理仅使用只读工具，并要求报告中包含具体的阻塞项。
  - 抑制父 TUI 流中冗余的角色代理生命周期事件，输出更清爽。
  - AgentFactory 追踪 `providerName`/`Vendor`，确保子 Agent 运行时同步。
  - 新增 `withRuntimeConfig` 用于灵活的工厂克隆。
  - Worker 报告同时兼容 `remaining_work` 与 `missing_work`；存在任何遗留任务或 blocker 时，会在进入 Critic 前拒绝完成候选。
  - 持久化当前 ESM 阶段、最新 Worker 进度、结构化遗留任务和连续完成驳回状态。
  - 新增连续 3 次驳回熔断，自动暂停续跑，用户执行 `/esm resume` 后可恢复。
  - 新增 `Ctrl+E` 打开的实时可滚动 ESM 进度面板，展示流水线阶段、遗留任务、阻塞点、审查详情、用量和当前子 Agent 活动。

- **上下文压缩改进**
  - `/compact` 现在在 TUI、Channels 和 OpenAI API 运行时中立即执行（此前为延迟执行）。
  - 新增 `CompactForced`，支持用户显式请求的压缩，当近期保留窗口外无旧历史时允许仅生成摘要检查点。
  - 自动压缩触发点移至构建下一个请求之前，确保纯文本轮次不会错过触发点。
  - 自动压缩阈值改为基于百分比（上下文窗口的 80%），通过 `ShouldCompactPercent` 控制。
  - 修复会话回放，正确处理 `FirstKeptEntry` 为空的纯摘要压缩条目。

- **Docker 支持**
  - 新增 `Dockerfile`（默认 Ubuntu，支持 Debian/Fedora/Alpine 变体），多阶段构建。
  - 新增 `.github/workflows/ghcr-publish.yml`，支持 CI/CD 发布到 GHCR。
  - 新增 `.dockerignore`，确保干净构建。
  - README 和入门指南新增 Docker 安装文档（中英文）。

- **子代理会话隔离存储**
  - 新增 `sub_session` 和 `sub_entries` 专用表，用于子代理、cron、webhook 和 ESM worker 会话（迁移 013）。
  - `AgentOptions` 新增 `IsSubAgent` 标志；factory 自动将子代理路由到隔离存储，避免污染主会话列表。
  - Cron 调度器、webhook 分发器和 ESM worker 流水线均启用子会话存储。

### 🔧 改进

- **TUI 粘贴处理优化**
  - 默认禁用括号粘贴，防止终端丢弃结束标记时 TUI 假死。
  - 改进分割粘贴的 Enter 延迟：仅在最近有文本输入时（空闲延迟窗口内）才排队 Enter，而非缓冲区非空时始终排队。
  - 新增 macOS 专属 `console_darwin.go`，在括号粘贴结束标记不可靠的终端中保持 `WithoutBracketedPaste`。
  - 从通用 Unix（`console_unix.go`）移除 `WithoutBracketedPaste`，输入队列现在无需禁用括号粘贴即可处理分割粘贴。
  - 新增分割粘贴合并和延迟 Enter 提交的测试。

- **Web UI 侧边栏修复**
  - 修复侧边栏：历史列表自动隐藏滚动条，flex-shrink 修复提升布局稳定性。

- **AgentFactory 增强**
  - AgentFactory 现在追踪 `providerName` 和 `Vendor`，确保子 Agent 运行时同步。
  - 修复 `Vendor` 为空时使用量统计中 provider 名称提取的问题。

- **Serve API 安全加固**
  - 默认监听地址改为回环地址（`127.0.0.1:8080`），默认模式改为 `agent`，默认启用沙箱。
  - 公网（非回环）监听现在需要 Bearer token 认证，除非传入 `--unsafe`。
  - 新增符号链接安全的 `IsWithinPath` 辅助函数；工作目录解析和 `allowedWorkDirs` 检查拒绝符号链接逃逸。
  - 持久化的会话工作目录在加载时重新校验，防止过期路径逃逸。
  - 请求处理期间固定会话，防止空闲淘汰竞争；为 `Touch`/使用计数器添加锁，修复并发 list/evict 数据竞争。

- **沙箱清理**
  - 新增 `CommandCleanupProvider` 接口；macOS Seatbelt 沙箱现在在每条命令退出后清理临时配置文件。
  - Bash 工具在同步和异步命令路径上都会调用沙箱清理。

- **Cron 调度器可靠性**
  - SQLite cron 存储新增原子 `ClaimDue`，防止多调度器实例重复执行同一任务。
  - 内存中跟踪运行中的 claim，避免同一任务重叠触发。

- **A2A 任务取消**
  - 注册每次运行的 cancel，使 `tasks/cancel` 能传播到执行器 context。
  - 新增 `TaskStore.Finish`/`Cancel` 辅助函数，并补充终态测试。

- **统计面板热力图**
  - 将 30 天柱状图替换为 7 天 / 2 小时分桶的火焰式热力图，更直观展示使用强度。

### 📚 文档

- 移除过时的 `docs/proposal/codex-goal-mode.md`。
- 新增 `docs/proposal/enable-supervisor-mode.md`。
- 更新配置文档，反映 `/compact` 立即执行和纯摘要检查点行为。

## v1.1.61

### ✨ 新功能

- **按会话工具能力**
  - `/v1/chat/completions` 新增 `x_tools` 扩展，支持按会话启用 webSearch、browser、a2aMaster、delegate 和 multiAgent。
  - 新增 `GET /api/capabilities` 和 `GET/PATCH /api/sessions/{id}/capabilities` API，用于查询和更新会话工具开关。
  - 新增 `session_capabilities` 持久化表（迁移 007），存储在 `sessions.db` 中。
  - 新增 CLI 标志 `--web-search`、`--browser`、`--enable-a2a-master`。
  - `serve.json` 配置新增 `webSearch`、`browser`、`a2aMaster` 字段。
  - Web UI 作曲家栏新增会话工具开关，通过 PATCH 更新能力 API。
  - 通过 `settingsForSession` 为会话注入 `webSearch` 设置。
  - 新增 `x_session_id` + `x_working_dir` 工作目录冲突检测（HTTP 409）。
  - 新增 `/api/sessions?scope=all|active` 和 `/api/sessions/active` 端点。
  - `/mode` 和 `/delegate` 命令现在按会话持久化能力变更。

- **会话流式传输与统计面板**
  - 新增基于 SSE 的会话流式传输，支持实时聊天更新（`session_stream.go`）。
  - 新增基于游标的消息/事件有序回放。
  - 新增 `/api/stats/` 端点（摘要、时间序列、按厂商/模型、最近请求）。
  - 跟踪会话运行状态，向流发布运行/能力事件。
  - 使用统计新增缓存读取/写入 token 计数。
  - Web UI：Chat 视图 SSE 流式传输、Stats 面板、Channels/Logs 设置。
  - Serve 配置中 `WorkingDir` 重命名为 `DefaultWorkDir`。
  - 新增旧版配置目录规范化（会话/skills 目录）。

- **运行与能力事件追踪**
  - 新增 `session_run_events` 和 `session_capability_events` 表（迁移 008）。
  - 记录每次聊天完成的运行生命周期事件（started/finished/failed/canceled）。
  - 记录 `/mode`、`/delegate`、`x_tools` 和 PATCH API 的能力变更事件。
  - 新增端点：`GET /api/sessions/{id}/run-events`、`GET /api/sessions/{id}/capability-events`。
  - Web UI 显示最近的运行和能力事件。
  - 新增 `make serve` Makefile 目标。

- **聊天 Transcript SSE 事件**
  - `ChatCompletionRequest` 新增 `x_transcript` 字段，启用 transcript 模式流式传输。
  - 启用后，流式处理器发送 `event: transcript` 帧（`assistant_delta` 和 `message` 类型），替代旧的 `tool_status` 事件。
  - Web UI 发送 `x_transcript:true`，通过共享的 `upsertTranscriptMessage` 路径路由 transcript 事件。
  - 重构流式处理器以根据 transcript 标志分支，新增构建 transcript toolCall/toolResult 条目的辅助函数。

- **Web UI 资源嵌入二进制**
  - Web UI 资源现在通过 `go:embed` 嵌入二进制文件，不再需要磁盘上的 dist 文件。
  - 新增 `ui` 包，提供 `DistFS()` 和 `fs.FS` 抽象，支持嵌入和覆盖路径。
  - `--web-ui-dir` 标志仍可用于覆盖嵌入资源。

- **--port 支持外部绑定地址**
  - `--port` 现在接受完整地址（如 `0.0.0.0:8080`）。
  - 移除了将 `0.0.0.0` 重写为 `127.0.0.1` 的 `displayListenAddr` 逻辑。
  - 新增 `mothx serve --unsafe`，用于本次进程关闭认证，并把 loopback/default 监听地址暴露到所有网卡。

- **Web UI 键盘快捷键与会话分页**
  - 侧边栏：Cmd/Ctrl+K 聚焦搜索，Shift+Cmd/Ctrl+K 新建聊天。
  - 平台感知的快捷键标签（macOS 与其他），Escape 清除搜索。
  - Sessions 视图：分页列表（每页 25 条），带页面导航控件。

- **微信二维码登录 API 与通道设置 UI**
  - 新增微信二维码登录 API 端点（登录状态、二维码代理、base64 模式）。
  - 新增 `wechatLoginSession` 管理二维码扫描流程状态。
  - 重写 Channels.svelte，实现完整的微信二维码登录流程（轮询、显示、错误处理）。
  - 新增飞书配置表单（appId/appSecret/workspace/allowedUsers）和 WebSocket 通道开关。
  - 新增 `ProviderSettings.svelte` 包装器和通道设置的完整 i18n 字符串。
  - 启动平台前增加 dispatcher nil 检查。

- **子 Agent 分离与规则守卫修复**
  - `AgentManager` 新增 `DetachChild()`，从父级活动列表移除子级但保留子 Agent 供后续查看。
  - `AgentManager` 新增 `HasRunning()`，检查是否有 Agent 正在执行。
  - `DelegateSubAgentTool` 改用 `DetachChild` 替代 `Destroy`，使已完成的委派子 Agent 仍可通过句柄查看。
  - 委派结果现在返回句柄，用于跟踪委派子 Agent。
  - 修复 `/rule` 命令守卫，使用 `HasRunning()` 替代 `Count() > 0`，仅保留已完成的子 Agent 时允许规则变更。

- **Cron 存储迁移到 SQLite**
  - 用 `SQLiteCronStore`（sessions.db）替换 `FileCronStore`（cron.json），实现可靠的事务性 Cron 任务持久化。
  - 新增 `SessionScopedStore`，将 Cron 任务绑定到会话，实现按会话隔离和自动继承 workDir。
  - 新增 `--cron` CLI 标志，作为独立选项（与 `--multi-agent` 分离）。
  - Cron 现在在 Serve 模式下默认启用，无需 multi-agent。
  - 调度器在 `SessionID` 设置时将定时本地运行附加到已有会话。
  - sessions.go 新增 `cron_jobs` 表迁移。
  - Cron 工具支持按名称查找任务（含歧义检测）进行启用、禁用、删除和运行。
  - Dispatcher 为仅 Cron 的会话延迟初始化 `AgentManager`。

- **Serve 设置热重载与工作流开关**
  - 新增 `Server.ApplySettings()`，保存设置后热重载 provider/model。
  - Serve 新增 `workflows` 会话工具选项和功能标志。
  - 默认工作目录不存在时，新增 `nearestExistingBrowseDir` 回退。
  - 截断逻辑重构为 `util.TruncateWithSuffix`（UTF-8 安全）。

- **系统提示重命名为 MothX**
  - 系统提示中的身份标识从 VibeCoding 更改为 MothX。

### 🔧 改进

- **Web UI 设置扩展**
  - 新增 `ListEditor` 可复用组件，用于编辑设置中的字符串列表。
  - `AppSettings` 扩展为完整的表单编辑器，覆盖 defaults、web search、context files、compaction、sandbox、retry、approval 和 provider 配置。
  - `ServeConfig` 扩展为完整的表单编辑器，覆盖 features、API、cron、memory、security、agent、hooks、channels 和 lobster mode。
  - Web UI：Sessions 表格固定列和省略布局。
  - Web UI：skill_ref 和 workflow_lint 工具调用/结果显示。
  - Web UI：微信二维码简化为新标签页打开，不再内嵌。
  - Web UI：会话切换和设置保存时重置 `resetSelectedModelToDefault`。
  - Web UI：工作目录设置保存后刷新，更清晰的限制逻辑。
  - i18n：新增 workflow 和 skill_ref 工具的中英文字符串。

- **Web UI 国际化与主题支持**
  - 新增完整的 Web UI 国际化（i18n）系统，支持中英文切换。
  - 新增偏好设置面板（`PreferenceControls`），可调整语言和主题。
  - 全面替换所有视图和组件中的硬编码字符串为翻译键。
  - 新增 CSS 变量驱动的主题系统，支持 Dark/Light 主题切换。

- **Web UI 工具调用与计划卡片渲染**
  - Web UI 聊天界面现在可以渲染工具调用、工具结果和计划（plan）卡片。
  - 工具调用以运行中/已完成状态标签展示，工具结果支持折叠/展开（摘要显示首行，点击按需加载完整输出）。
  - 计划工具调用渲染为可实时更新的待办清单。
  - 后端新增 `/api/sessions/:id/tool-results/:callId` 端点，支持按需加载完整工具输出。
  - `ListActiveSessions` 现在同时返回历史会话，Sessions 页面可展示所有持久化对话。

- **Serve 模式更丰富的工具状态流**
  - SSE 流式事件现在携带更丰富的工具状态信息，前端可实时展示工具执行进度。
  - 工具状态事件包含工具名称、参数和执行结果。

- **调试模式 pprof 服务器**
  - 新增 `--debug` 标志，在所有入口（CLI、TUI、Serve、ACP、A2A）上启动本地 pprof 性能分析服务器。

- **Speedtest 命令**
  - 新增 `vibecoding speedtest` CLI 子命令，用于测试模型响应速度。

- **新增阶跃星辰厂商支持**
  - 新增 `stepfun` 厂商，Base URL `https://api.stepfun.com/step_plan/v1`，OpenAI 兼容协议。
  - 新增 `step-3.7-flash` 模型，256K 上下文，支持多模态（text + image）输入。

- **多模态图片输入**
  - Serve 模式现在支持多模态（图片）输入，可通过 API 上传图片进行对话。
  - 改进了会话持久化，确保多模态消息正确存储。

- **Serve init-config 子命令**
  - 新增 `mothx serve init-config` 子命令，支持初始化全局和项目级 `serve.json` 配置。

- **TUI 输入队列按需启动**
  - 输入队列定时器改为按需启动，空闲时自动停止，减少不必要的 CPU 占用。

- **TUI Backspace/Delete 删除认证模型**
  - 认证对话框模型列表中新增 Backspace/Delete 快捷键，可删除选中的模型条目。
  - `+ Add Model` 和 `Done` 等操作行不会被误删。

- **粘贴合并可配置化**
  - 分割粘贴事件合并（split-paste coalescing）现在可通过测试参数配置，便于单元测试验证。

### 🔒 安全

- **Browse API 限制**
  - Browse API 现在限制在 `allowedWorkDirs` 白名单目录内，拒绝越界访问。
  - 无 token 时拒绝认证请求，避免未授权访问。

- **代码扫描修复**
  - 修复了潜在的不安全引号问题（Code Scanning Alert #3）。

### 🔄 重构

- **Gateway 与 Hermes 合并为统一 Serve 模式**
  - 移除 `internal/gateway` 和 `internal/hermes` 包，合并到统一的 `internal/serve/` 架构中。
  - CLI 入口点移除 `gateway`/`hermes` 子命令。
  - 新增 `internal/serve/openaiapi`（OpenAI 兼容 API 运行时）、`internal/serve/channels`（消息通道调度）、`internal/serve/ws`（WebSocket 通道运行时）。
  - 移除 Hermes 内置终端 WebSocket 客户端。

- **WebUI 组件拆分**
  - 将单体 `App.svelte` 拆分为独立的组件（components）、视图（views）和工具库（lib），提升可维护性。

### 📦 依赖

- 升级 `golang.org/x/image` 从 v0.36.0 到 v0.41.0。

## v1.1.60

### ✨ 新功能

- **统一 Serve 模式与 Web UI**
  - 新增 `mothx serve` CLI 命令，启动统一服务器，同时提供 OpenAI 兼容 API、Web UI 管理面板和消息通道（微信/飞书）。
  - 新增 `internal/serve/` 包，统一管理 Serve、Channels 通道和 Web UI 的配置与运行时。
  - 配置文件 `serve.json`（全局 `~/.mothx/serve.json`，项目 `.mothx/serve.json`），支持 Serve、通道、Web UI、Cron、Memory、Security、Hooks 和 Agent 配置。
  - 内置 Svelte Web UI 面板，采用 Dark 主题，提供健康检查、通道状态、配置编辑、设置编辑和聊天界面（支持 SSE 流式输出）。
  - Web UI 新增完整管理 API：`/api/status`、`/api/sessions`、`/api/cron`、`/api/memory` 和 `/ws/logs` 实时日志流。
  - WebSocket 网关挂载到 `/ws`，复用 Channels 事件协议实现实时通信。
  - Cron API 支持 CRUD 操作并与调度器联动。
  - Serve SessionPool 新增 List/Delete 管理接口。
  - 新增 `--web-ui-dir` CLI 标志，可覆盖 Web UI 静态资源目录。
  - 新增 Lobster 模式（`--lobster`），自动启用 yolo 模式、禁用沙箱、开启子 Agent。
  - Serve 新增 `ExtraRoutes` 钩子，支持 Serve 模式注入自定义 API 路由（`/api/serve/config`、`/api/settings`、`/api/channels`）。

- **新增厂商支持**
  - 新增华为云厂商（`huawei`、`huawei-plan`），共 13 个模型，包含标准版和 Plan 推理模式。
  - 新增摩尔线程厂商（`mthreads-plan`），提供 GLM-4.7 模型（1M 上下文）。
  - 新增天翼云厂商（`ctyun-plan`），3 个模型含 GLM-5-Turbo。
  - 新增京东智联云厂商（`jd-plan`），10 个模型含 JoyAI-LLM-Flash。
  - Gitee/Moark 新增 Kimi-K2.5 和 MiMo-V2.5-Pro；修复 JD Plan 配置，补充缺失模型。

### 🐛 Bug 修复

- **TUI 分割粘贴事件合并**
  - 部分终端会将粘贴文本拆分为多个独立按键事件。新增空闲检测，在输入队列刷新前等待静默期。
  - 当流中出现 Enter 且后跟更多文本时，分割的粘贴事件现在会被合并为单次粘贴。
  - 提取 `handleInputSubmit()` 辅助函数，使 Enter 键处理更清晰。

- **旧版 `VIBECODING_DIR` 环境变量处理**
  - `ConfigDir()` 当 `VIBECODING_DIR` 设为旧版默认值 `~/.vibecoding` 时，现在会回退到默认的 `.mothx/` 路径，避免意外覆盖新配置目录。
  - `ConfigDirOverridden()` 当 `VIBECODING_DIR` 等于旧版默认路径时不再报告为自定义覆盖。
  - stats CLI 现在从 `config.LoadSettings()` 读取 `sessionDir`，而非直接调用 `platform.SessionDir()`，确保尊重配置中的会话目录。

### 🧪 测试

- 新增 `TestConfigDirIgnoresLegacyDefaultEnvDir` 和 `TestConfigDirHonorsCustomLegacyEnvDir`，验证 `ConfigDir` 在 `VIBECODING_DIR` 为旧版默认值或自定义路径时的正确行为。
- 新增 `TestLoadSettingsWithLegacyDefaultEnvCreatesMothXConfig`，验证当 `VIBECODING_DIR` 指向旧版默认值时，设置迁移会创建 `.mothx/` 配置。
- 新增 `TestOpenStatsDBUsesConfiguredSessionDir`，验证 stats 命令从设置中解析会话数据库路径。

## v1.1.59

### ✨ 新功能

- **系统提示新增工具选择规则**
  - 在 agent 系统提示中新增"工具选择规则"章节，指导模型优先使用专用工具（`read`、`ls`、`grep`、`find`）进行文件检查与发现，而非 `bash`。
  - 明确不鼓励通过 `bash` 运行 `cat`、`sed`、`awk`、`grep`、`find`、`ls`、`pwd` 等命令（当存在等效专用工具时）。

- **目录迁移到 `.mothx/`**
  - 安装脚本（`install.sh`、`install.ps1`）默认目录改为 `~/.mothx/`（原为 `~/.vibecoding/`）。
  - 新增 `MOTHX_INSTALL_DIR` 环境变量，`VIBECODING_INSTALL_DIR` 作为旧版兼容保留。
  - 卸载时同时检查旧目录（`~/.vibecoding/`、`./.vibe`）与新目录（`~/.mothx/`、`./.mothx`），确保向后兼容。
  - npm postinstall 脚本与 README 更新为引用 `~/.mothx/settings.json`。

### 🔧 改进

- **Bash 子进程非交互式与进程组终止**
  - bash 工具的子进程现在以非交互模式运行：stdin 设为空（`read` 看到 EOF 而非阻塞），并注入非交互环境变量默认值（`GIT_TERMINAL_PROMPT=0`、`GIT_ASKPASS=true`、`SSH_ASKPASS=true`、`SSH_ASKPASS_REQUIRE=never`、`SUDO_ASKPASS=true`），除非用户已显式设置。
  - Unix 平台使用 `Setsid` 让 shell 拥有独立会话；取消时通过 `kill(-pid)` 终止整个进程组，确保认证助手和孙进程不会残留。
  - 新增 `killCommandProcess` 辅助函数，`BashTool` 和 `JobManager` 共享统一的进程终止逻辑。

- **TUI 认证对话框重构**
  - 认证输入字段（API key、provider ID、模型名等）从 `SetMaxLines(3)` 改为 `SetMaxLines(1)`，强制单行输入。
  - 新增 `newAuthInput()` 辅助函数，统一编辑器创建逻辑，减少 `auth_dialog.go`、`auth_model.go`、`auth_provider.go`、`auth_settings_top.go` 中的重复代码。
  - 移除不再直接使用 `editor` 包的冗余导入。

- **测试**
  - 新增 `TestAuthAPIKeyInputStaysSingleLine`，验证认证输入不会换行成多行。
  - 新增 `TestLoadSettingsCreatesMothXConfigDir`，验证设置创建使用 `.mothx/` 而非 `.vibecoding/`。

## v1.1.58

### 🔧 改进

- npm 平台二进制包从 `mothx-*` 改名为 `mothx-installer-*`，与根包 `mothx-installer` 命名保持一致。
- PyPI 安装包从 `vibecoding-installer` 改名为 `mothx-installer`；Python 包装器现在以 `mothx` 作为主命令，并保留 `vibecoding` 兼容别名。

## v1.1.57

### ✨ 新功能

- **图片预处理与多模态增强**
  - 统一的图片预处理流水线，元数据在整个工具链中传递。
  - 新增图片裁剪、浏览器截图预处理，以及 OpenAI `detail` 参数透传，支持 `auto`/`low`/`high` 质量控制。
  - 图片输出尺寸校验，带厂商特定的坐标映射提示，用于边界框标注。
  - 新增 Qwen 专用的 28px patch 图片 token 估算和多图片累加，确保 token 统计准确。
  - 新增 `/paste-image` 命令，支持 `Ctrl+R` 预览。

- **统计面板改进**
  - 新增分享按钮、Token 趋势图和整体 UI 优化。
  - 新增 2.5 小时时间桶分组和最近请求页的筛选功能。

- **首次运行自动打开认证对话框**
  - 未配置任何 provider 时，首次运行自动弹出认证对话框。

- **MothX npm 改名过渡**
  - 新增面向后续更新的 npm 包 `mothx-installer`。
  - 本版本保留 `vibecoding-installer` 作为兼容包，并提示用户后续使用 `npm install -g mothx-installer@latest` 更新。
  - npm 平台二进制包从 `vibecoding-installer-*` 改名为 `mothx-installer-*`。

### 🔧 改进

- `MaxTokens` 现在从模型默认值解析，并限制在上下文窗口大小范围内。
- 新增压缩超时和摘要 token 上限设置。
- 命令建议和 `/mode` / `/agent` 描述更清晰，避免混淆。
- 将 HTTP 500 加入可重试状态码列表。
- 更新 `vibe-browser` 到 v0.1.3，移除本地 replace 指令。
- 默认设置文件现在以更精简的方式写入，省略未设置的字段。

## v1.1.56

### ✨ 新功能

- **交互式会话选择对话框**
  - `/sessions` 现在打开交互式选择对话框，支持方向键上下导航、回车切换、`n` 新建会话、`d` 删除会话。原有 `/sessions ls`、`/sessions set <id>`、`/sessions clear`、`/sessions del <id>` 命令仍然保留。
  - TUI 启动时延迟创建会话，直到用户发送第一条消息时才初始化。`--continue`、`--resume`、`--session` 和 `/sessions set` 仍会绑定已有会话。
  - 在 TUI 中继续或切换会话时，会把加载到的会话历史打印到终端 scrollback 中。

- **统计 Web 面板**
  - `mothx stats` 启动 Web 面板，默认监听 `127.0.0.1:7878`，含图表与筛选功能。
  - 纯 HTML/CSS/JS 面板，无外部依赖，图表通过 `<canvas>` 绘制。
  - 显示总体概览（请求数、token、费用、时长）、时间序列图、按厂商/模型分类统计，以及分页的最近请求列表。
  - 支持按时间范围（今日/本周/本月/全部）、厂商和协议筛选。
  - `mothx stats --cli` 直接在终端打印统计信息。
  - `mothx stats --db <path>` 可打开指定的 sessions.db 文件。

- **统计面板：协议与厂商分离**
  - 统计面板中的「Provider」列已按语义拆分为**厂商**（公司名称）和**协议**（API 协议类型，如 `openai-chat`、`anthropic-messages`、`google-gemini`）。
  - 新增 `Provider.API()` 接口方法，在 `request_stats` 中同时记录协议类型与厂商名称。
  - 新增厂商与协议筛选下拉框；饼图与表格现在同时展示两个维度。
  - 数据库迁移 006 为 `request_stats` 表添加 `protocol` 列（已有数据回填为空字符串）。

- **LongCat 厂商支持**
  - 新增 `longcat` 厂商适配器，支持 OpenAI 兼容协议（`https://api.longcat.chat/openai`）与 Anthropic 兼容协议（`https://api.longcat.chat/anthropic`）两种接入方式。
  - 默认设置中注册了两个内置 provider：`longcat`（OpenAI 协议，`LONGCAT_API_KEY`）与 `longcat-anthropic`（Anthropic 协议，`LONGCAT_ANTHROPIC_API_KEY`）。
  - 默认模型 `LongCat-2.0`：上下文长度 1M，最大输出长度 128K Tokens。
  - TUI 授权对话框中，在 `longcat` 厂商下提供 OpenAI / Anthropic 两种 BaseURL 的选择。

- **OpenAI 兼容模型的内联 `<think>` 推理**
  - 为 OpenAI 兼容供应商新增 `parseReasoningInContent` 模型兼容标志。启用后，正文流中以 `<think>...</think>` 包裹的推理内容会被提取并作为思考增量输出，而不再作为普通文本。
  - 流式解析器能正确处理跨多个 SSE 分块的标签，并在流结束时将残留的不完整标签按字面文本处理。

- **Auth V2 设置追踪**
  - 为 `ProviderConfig` 和 `ModelConfig` 新增 `fieldSet` 字段追踪，通过自定义 `UnmarshalJSON` 实现对 JSON 显式设置字段的检测，支持 auth V2 合并逻辑。
  - 自定义 `Settings.UnmarshalJSON` 处理映射风格的 `providers` 键，无需修改结构体字段。

- **项目级 Bash 自动审批规则**
  - `allow.json` 新增 `bashCommands`（精确匹配）和 `bashPrefixes`（前缀匹配），支持在 agent 模式下为项目配置 bash 自动审批。
  - 审批对话框新增「始终允许此命令」和「始终允许命令前缀」选项，规则持久化到 `.vibe/allow.json`。
  - 设置级 `bashBlacklist` 优先级高于项目允许规则（黑名单命令始终需要审批）。
  - `allow.json` 中 `autoEdit` 默认值改为 `true`（文件不存在时），更贴合开发者日常工作流。

- **完整的 `/settings` 设置对话框**
  - `/settings` 现在打开结构化根菜单，而非直接跳入 provider 列表。分类包括：Providers、Defaults、Behavior、Web Search、Context Files、Status Line、Compaction、Sandbox、Paths、Retry 和 Approval。
  - 每个顶层设置分组拥有独立的子菜单，支持字段编辑、布尔切换和列表编辑。
  - 顶层设置编辑使用 `SaveGlobalSettingsPatch()` 仅更新受影响的 JSON 键，防止无关默认值被展开写入 `settings.json`。

- **交互式审批对话框**
  - 用专用对话框替代了行内「y/n」审批提示，支持 ↑/↓ 导航、Enter 确认、y/n 快捷操作和 Esc 中止。
  - 审批对话框按工具类型展示结构化详情：bash 命令附带 timeout/async 元数据；edit/write 展示参数摘要。
  - 底部提示改为「! APPROVAL REQUIRED: ↑/↓ Enter」以反映新的交互方式。

### 🔧 改进

- 将约 1000 行内嵌仪表板 HTML 从 `internal/stats/dashboard.go` 提取至独立的 `internal/stats/dashboard.html` 文件，启动时通过 `go:embed` 加载。
- 每次 LLM 调用后 agent loop 自动记录统计数据。stats 服务器启动时调用 `session.ApplyMigrations()` 确保 `request_stats` 表存在。
- 更新火山引擎 provider：新增 `agentplan` 和 `codingplan` 供应商，统一 gitee/moark 适配器，移除 `seed` 供应商。
- PyPI 构建新增 venv 隔离（`.venv-build`），使构建脱离系统 Python。
- 抽取 `bashCommandArg()` 辅助函数，在审批路径中统一支持 `command` 和 `cmd` 两种参数键名。
- 将 TUI Esc 处理重构为 `abortPendingRequest()`，正确清理审批和问题状态。
- 修复 auth 对话框切换视图时 `ParamField` / `ParamFieldKey` 残留问题；切换和子菜单不再残留输入模式。
- 修复默认 provider 配置中模型切片的缩进问题。
- 新增 auth 对话框和配置字段追踪的测试。

## v1.1.54

### ✨ 新功能

- **Serve 多工作区会话隔离**
  - HTTP 网关的默认会话（x_session_id 为空时）改为按工作目录（`workDir`）进行隔离，不再共享全局唯一的默认会话，从而防止不同工作区的客户端混用会话上下文。
  - 新增 `OpenByIDExact` 接口，支持忽略当前工作目录限制、直接通过精确会话 UUID 加载并重建会话元数据。
  - 网关内增加了并发会话创建序列化锁，防止客户端并发高频调用时创建出重复的会话。
  - 优化 `/sessions del` 斜杠命令，支持对会话 ID 的前缀模糊匹配，并防止误删当前正在使用的活跃会话。
  - `/clear` 斜杠命令改为清空会话历史消息，但保持会话卡槽（Session Slot）不变，无需重建。

- **PyPI 安装包**
  - 新增 `vibecoding-installer` 的 PyPI 包装器，提供兼容用的 `vibecoding` 命令入口，并通过内嵌原生二进制的平台 wheel 分发。
  - 新增 `make pypi-*` 发布目标，以及版本同步和 wheel 构建脚本，在流程上对齐 npm 发布方式，同时使用 pip 原生的平台 wheel 选择机制。
  - 更新安装与发布文档，补充 `pipx install vibecoding-installer`。

### 💅 优化

- **更可靠的后备工具调用 ID 生成机制**
  - 后备工具调用 ID（Tool Call ID）生成机制改为“进程级原子计数器 + 高精度时间戳”组合，彻底杜绝高并发多工具调用场景下因 ID 重复而触发的 Anthropic/OpenAI Schema 校验报错。
  - 更新了部分默认模型及配置，并解决了 Gemini 特定的工具调用 ID 唯一性要求。
  - TUI 授权对话框（Auth Dialog）在保存时，现在会完整保留用户自定义的模型参数，而不是重置为厂商默认值。

### 🐛 修复

- **思维深度（Thinking Level）归一化**
  - 引入了 `thinkingLevel` 归一化步骤。当配置值为空或非法时，自动优雅回退至 `medium` 而不是静默禁用思维，从而保证推理模型默认行为符合预期。


## v1.1.53

### ✨ 新功能

- **可嵌入 agent：宿主提供的外部工具**
  - 新增公开的 `agent.ExternalTool` 接口，嵌入方应用可将自身受控能力暴露给 agent，与内置编码工具并存（或完全替代）。
  - 新增 `ExternalToolResult`（文本/错误 + 可选的富 `Contents` 内容块）以及可选的 `ExternalToolPromptInfo` 接口，用于贡献系统提示词信息（`PromptSnippet`、`PromptGuidelines`）。
  - 新增 `Builder.WithExternalTools(...)` 用于注册自定义工具，`Builder.WithoutBuiltinTools()` 用于禁用全部内置工具，从而构建只能使用宿主工具的 agent。
  - 外部工具通过内部 factory 的 `externalToolAdapter` 接入，内部包现在通过 `CreateFromPublicOptions` 从公开 `Builder` 配置构建 agent。
  - 新增 `bootstrap` 包：外部模块只需空白导入 `github.com/startvibecoding/mothx/bootstrap` 一次即可注册内部 builder 与 provider 解析 hook（因为内部包无法被直接导入）。

### 💅 优化

- **端到端遵循已配置的 provider 模型**
  - 在公开与内部的 `ChatParams` 中贯穿 `ModelID` 字段，使所选模型能一路传递到 provider 请求。
  - OpenAI 与 Anthropic 兼容 provider 现在会在存在配置时从 provider 配置解析模型列表与 `compat` 标志，否则回退到内置默认值。
  - provider 工厂改为通过 `init` hook 注册，使 `ResolveProvider` 能通过全局 registry 按名称构建 provider，并简化了回退链，对不支持的 `api` 直接报错。

- **Provider 指南文档**
  - 新增 provider 指南（`docs/en/provider-guide.md`、`docs/zh/provider-guide.md`），介绍 provider/vendor 配置。

- **bash 执行体验优化**
  - 将同步执行的 `bash` 默认超时收紧到 45 秒，保留 `async=true` 作为后台任务模式，并将 `timeout=0` 明确为“不设置工具层 deadline”。
  - 更新了 `bash` 的提示语，强调长驻服务应使用 `async=true`，网络探测和其他容易挂住的命令应显式设置超时。
  - TUI 现在会把工具执行拆成“即将运行”和“运行结果”两条独立消息，长命令执行中也能直接看见状态。

- **内部模块拆分**
  - 将 agent、TUI 与命令文件拆分为更聚焦的模块（agent 审批/上下文、TUI 粘贴/渲染、会话/状态行命令），便于维护，行为不变。

### 🐛 修复

- **自定义 provider 认证流程**
  - 修复自定义 provider 认证流程，使其能正确从 API key 步骤推进到模型选择步骤。

## v1.1.52

### 💅 优化

- **Provider HTTP/1.1 fallback 配置**
  - 新增 `providers.<name>.forceHTTP11`，可为单个 provider HTTP client 禁用 HTTP/2。
  - 当代理或 API 网关偶发将 HTTP/2 SSE 流重置并报出 `stream ID ... INTERNAL_ERROR` 时，可用该配置提升稳定性。

- **早期 provider SSE 读流失败遵守 retry 配置**
  - OpenAI 兼容、Anthropic 与 Google 流在尚未输出任何可见内容前遇到暂时性读流错误时，会按已配置的 `retry` 规则自动重试。
  - HTTP/2 `INTERNAL_ERROR` stream reset 现在会被归类为可重试网络错误。
  - 一旦文本、思考、工具调用或 usage 已输出，读流错误仍会立即失败，以避免重复输出。

- **移除内嵌 rg/fd 二进制，切换为纯 Go SDK**
  - 将内嵌的 `rg` 二进制替换为 [`go-ripgrep`](https://github.com/startvibecoding/go-ripgrep) 包。`grep` 工具现在以纯 Go 方式在进程内执行 ripgrep 兼容搜索，不再回退到系统 `grep`。
  - 将内嵌的 `fd` 二进制替换为 [`go-fd`](https://github.com/startvibecoding/go-fd) SDK（`gofd.Find()`）。`find` 工具现在以纯 Go 方式在进程内执行 fd 兼容的文件发现，不再回退到系统 `find`。
  - 删除整个 `internal/vendored/` 包（embed 文件、二进制提取逻辑、`RgPath`/`FdPath`/`Ensure` 辅助函数）以及全部 12 个平台的 `rg`/`fd` 二进制文件（约 42 MB）。
  - 移除 `scripts/prepare-vendored.sh`、`scripts/extract-vendored-tool.sh`、`scripts/download-ripgrep.sh`、`scripts/download-fd.sh` 以及 `pkgs/` 目录（缓存的压缩包）。
  - 移除 Makefile 中的 `prepare-vendored` 和 `test-vendored` 目标；`build`、`build-all`、`test` 不再依赖二进制提取。
  - `bash` 工具不再将 `~/.vibecoding/bin` 注入 `PATH`，因为已无提取的二进制需要暴露。
  - `grep` 和 `find` 仍保持按行输出；无效根路径和搜索初始化错误会直接作为工具错误返回。

- **FreeBSD 编译与打包**
  - 在构建矩阵中新增 FreeBSD `amd64` 和 `arm64`（`make build-freebsd`）、tarball 分发（`make dist-freebsd`），并接入完整的 `make dist` / `make build-all` 流程。
  - 新增 FreeBSD 平台 npm 包（`vibecoding-installer-freebsd-x64`、`vibecoding-installer-freebsd-arm64`）作为可选依赖，并在 npm wrapper 和 `install.sh` 中加入平台识别。
  - FreeBSD 使用纯 Go 的 `grep`/`find` 实现，并回退到 no-op 沙箱，因为 bwrap/seatbelt 仅支持 Linux/macOS。

- **Windows 内嵌 BusyBox 支持**
  - 为 Windows 平台内嵌 `busybox32u.exe` 和 `busybox64u.exe` 资产，运行时解压后作为 `bash` 工具的默认 shell。
  - BusyBox 不可用时回退到 PowerShell。
  - bash 工具输出现在包含运行时标签，指示当前使用的是 BusyBox 还是系统 shell。

- **交互式 Model 选择器**
  - `/model` 不带参数时现在会打开交互式选择对话框，而非以纯文本列出模型。
  - 支持搜索过滤、方向键导航、当前模型指示，以及回车切换。

- **ccstatusline 原生支持**
  - 新增 `statusLine` 配置（`type`、`command`、`padding`、`refreshInterval`、`timeoutMs`、`fallback`），用于外部状态行渲染器。
  - 以 Claude 兼容的 JSON stdin payload 执行状态行命令；支持多行输出、ANSI 颜色和 OSC 8 超链接。
  - 新增 `/statusline` 斜杠命令（`on`/`off`/`status`/`test`/`refresh`），可在运行时控制状态行。

## v1.1.51

### ✨ 新功能

- **新增 Provider: 火山引擎 (Volcengine)**
  - 新增火山引擎 Provider，通过方舟 API 平台接入豆包 Seed 系列模型。
  - 支持模型：豆包 Seed 2.1 Turbo（`doubao-seed-2-1-turbo-260628`，256K 上下文，纯文本）、豆包 Seed Evolving（`doubao-seed-evolving`，256K 上下文，文本+图片）、豆包 Seed 2.1 Pro（`doubao-seed-2-1-pro-260628`，256K 上下文，文本+图片）。
  - 使用 OpenAI 兼容 API 端点 `https://ark.cn-beijing.volces.com/api/v3`。
  - 通过 `ark.cn-beijing.volces.com` 域名自动识别供应商。

- **SQLite 会话存储**
  - 新增和恢复会话统一使用 SQLite（`modernc.org/sqlite`），提升查询性能和元数据管理能力。
  - 对于 CLI 和 Serve，所有会话的元数据和条目日志均存储在单个统一的 `sessions.db` 数据库文件中，列表/切换/删除时使用虚拟的 `.db` 路径句柄；只有 Channels 会在用户目录下写入物理的会话句柄文件（如 `active.db` 与归档的 `*_corrupt.db`）。
  - `OpenByID` 和 `OpenByPathOrID` 新增快速精确/前缀匹配，支持歧义检测，并可直接基于统一 SQLite 数据库还原会话结构。
  - ACP 历史重放现在会在加载存储对话历史时流式传输工具执行事件（`toolCall`/`toolResult`）。
  - `DeleteSession` 清理 SQLite 中的会话与条目记录，并在物理句柄文件存在时（如 Channels）将其删除，同时拒绝将共享的 `sessions.db` 作为会话句柄删除。
  - Channels 现在使用 `active.db` 会话物理句柄，损坏会话归档为 `*_corrupt.db`，并移除旧版 `active.jsonl` fallback。
  - 移除旧版 JSONL 加载/写入路径，新增和恢复会话仅使用 SQLite。

### 🐛 Bug 修复

- **ACP Systeminit Plan Mode 写权限**
  - 修复 ACP systeminit 在 plan 模式下允许文件写入，使 TUI/ACP 可在使用 `/systeminit` 生成 `AGENTS.md` 时不受模式限制错误影响。

### ✨ 新功能

- **`/systeminit` 与 `/reload` 指令**
  - 新增 `/systeminit`：生成或刷新项目级 `AGENTS.md`。在 TUI、ACP 以及 `mothx systeminit` CLI 子命令中均可用。TUI 与 ACP 下会启发式地使用 `question` 工具先向用户提问几个关键问题，再生成更优质的 `AGENTS.md`；CLI 为非交互式直接生成。支持传入附加说明，例如 `/systeminit 用中文提问我，用英文写 AGENTS.md`。
  - `question` 工具现在在 `agent` 模式下也可用（以前仅 plan），并为 ACP 服务器注册，ACP 通过 `session/request_permission` 通道呈现问题。
  - 新增 `/reload`（TUI）：以全新进程重启并开启新 session，重新加载配置、上下文文件、skills 与 MCP，等同于重新启动程序。

- **Mode 边界增强：`/btw` 旁路问答 + 可编辑路径白名单 + 全自动编辑**
  - 新增 `/btw <问题>`：在不中断主任务的前提下，继承主任务对话历史（只读）快速启动一个一次性 sub-agent 回答临时问题。答案显示在临时浮动层，不写回主 session，不增加主任务上下文窗口占用与 token 统计；sub-agent 仅拥有只读工具（read/grep/find/ls/skill_ref）。主历史过长时会自动裁剪注入快照以控制旁路开销。
  - 新增 `/alloweditpath [add <glob>|remove <glob>|clear]`：维护可编辑路径白名单（支持 `**`/`*` 通配符），agent 模式下命中白名单的 `write`/`edit` 无需逐次申请、自动放行。
  - 新增 `/allowautoedit [on|off] [global]`：打开 agent 模式下的全自动编辑（相当于只有 bash 需要申请权限）。
  - 白名单与全自动开关落盘到独立的 `allow.json`：`/alloweditpath` 与默认的 `/allowautoedit` 写项目级 `.vibe/allow.json`；`/allowautoedit on global` 写全局 `allow.json`。加载顺序为全局→项目覆盖（`editPaths` 仅项目级）。新会话启动时自动载入。
  - 仅放宽审批层，不改变 sandbox / allowedWorkDirs 物理边界，也不改变 plan / yolo 语义。

- **基于 npm 接口的版本更新检测**
  - MothX 现在会通过 npm registry（`vibecoding-installer`）检测是否有新版本，并在启动时给出非阻塞的更新提醒。
  - 网络检测在后台进行（最多每 24 小时一次），仅刷新本地缓存（`update-check.json`），前台不会因网络请求而阻塞。
  - 提醒会显示在 TUI 启动信息中，`--print` 模式下输出到 stderr，并提示执行 `npm install -g vibecoding-installer@latest`。
  - 可在配置文件 `settings.json` 中设置 `"updateCheck": false` 关闭，也可通过 `VIBECODING_NO_UPDATE_CHECK=1` 关闭；通过 `VIBECODING_NPM_REGISTRY` 覆盖 registry 地址。

### 📚 文档

- 更新会话文档、CLI 示例、FAQ 清理建议、架构图、Channels 文档和 README 功能摘要，说明 SQLite 存储、`.db` 句柄文件和 Channels `active.db` 会话。
- 新增内置火山引擎/豆包 provider 配置文档，并刷新 provider 适配器列表，加入火山引擎、Mistral、GitHub Copilot、Cloudflare 和 Amazon Bedrock。

### 💅 优化

- **TUI 头部与底部美化**
  - 放大 ASCII logo 并在头部区域垂直居中显示。
  - 弱化底部分隔线，并统一模式/模型/路径的配色，界面更清爽。

## v1.1.50

### ✨ 新功能

- **流式 Delta Builder 优化**
  - 用 `strings.Builder` 替代字符串拼接来累积助手和思考文本 delta，避免长回复时 O(n²) 的内存增长。
  - Builder 在轮次结束、审批和错误事件时先 finalize 再打印，确保输出一致性。

- **新增 Provider: Mistral**
  - 新增 Mistral AI Provider，支持模型包括：Mistral Large、Mistral Medium 3.5、Mistral Small、Codestral、Devstral、Magistral Medium/Small 和 Pixtral Large。
  - 使用 OpenAI 兼容 API 端点 `https://api.mistral.ai/v1`。

- **新增 Provider: GitHub Copilot**
  - 新增 GitHub Copilot Provider，支持 Claude Sonnet 4.6/4.5、Claude Opus 4.8、Claude Haiku 4.5、Claude Fable 5、GPT-5.5/5.4/5.2、Gemini 2.5 Pro 和 Gemini 3.5 Flash 模型。
  - 使用 OpenAI 兼容 API 端点 `https://api.individual.githubcopilot.com`。

- **新增 Provider: Cloudflare AI Gateway**
  - 新增 Cloudflare AI Gateway Provider，支持 Claude、GPT、Gemini 和 Llama 4 Scout 模型。
  - 支持通过 Cloudflare AI Gateway 路由来自 Anthropic、OpenAI、Google 和 Meta 的模型。

- **新增 Provider: Cloudflare Workers AI**
  - 新增 Cloudflare Workers AI Provider，支持 Llama 4 Scout 17B、Llama 3.3 70B、Gemma 4 26B、Mistral Small 3.1 24B、GPT OSS 120B/20B、Kimi K2.7 Code 和 GLM 5.2 模型。
  - 使用 Cloudflare Workers AI 推理端点。

- **新增 Provider: Amazon Bedrock**
  - 新增 Amazon Bedrock Provider，支持 Claude Sonnet 4.6/4.5、Claude Opus 4.8、Claude Haiku 4.5、Claude Fable 5、Amazon Nova Pro/Micro/Lite 以及 DeepSeek V3.2/R1 模型。
  - 使用 OpenAI 兼容跨区域推理端点。

- **紧凑 TUI 底栏与输入分隔线**
  - 将 mode、model 和 path 合并为单行底栏（原来 3 行）。
  - 在 transcript 和输入区域之间新增半块分隔线，增强视觉区分。
  - 编辑器光标和 placeholder 样式新增背景色。
  - npm 新增 postinstall 脚本，安装后显示快速开始信息。

### 🐛 Bug 修复

- **TUI 输入框宽度对齐**
  - 修复输入框宽度与上方分隔线不对齐的问题，布局更一致。
  - 编辑器宽度设为完整终端宽度以匹配分隔线。
  - 修复编辑器 Width 计算中双重 padding 扣减的问题，改用 `m.width` 作为最终渲染宽度。

- **TUI `compactBashOutput` 尾部空白**
  - 修复 `compactBashOutput` 在空行去重后写入原始未 trim 行而非 trim 后行的问题，避免保留尾部空白字符。

- **TUI Program 模式下转录内容重复**
  - 当 Bubble Tea program 活跃时清空受管 liveContent，避免通过 `Program.Println` 打印到原生 scrollback 的已完成转录块在 live 视图中重复显示。

- **Sandbox 状态标签**
  - 移除"无沙箱"状态显示中多余的 "YOLO mode" 文本。

---

## v0.1.47

### ✨ 新功能

- **扩展模型目录**
  - Anthropic 新增模型：Claude Opus 4.8、Claude Opus 4.1、Claude Opus 4、Claude Sonnet 4.0、Claude Haiku 4.5、Claude Fable 5，以及 Claude 3 系列遗留模型。
  - OpenAI 新增模型：GPT-5.5、GPT-5.5 Pro、GPT-5.4 系列、GPT-5.3 Codex/Spark、GPT-5.2 Pro/Codex、GPT-5.1 Codex 系列、GPT-4.1 系列、o4-mini、o3/o3-pro/o3-deep-research、o1-pro，以及 GPT-4 系列遗留模型。
  - OpenRouter 新增模型：Claude Sonnet 4.6/4.5、Claude Opus 4.8、Claude Haiku 4.5、GPT-5.5/5.5 Pro/5.4、Gemini 3.5 Flash/2.5 Pro、DeepSeek V4 Flash/Pro、Qwen 3.7 Plus、Kimi K2.7 Code、MiniMax M3、Llama 4 Scout、GLM 5/5.2、Grok 4.3、GPT-OSS-120B（免费）。
  - Vercel AI Gateway 新增模型：Claude Sonnet 4.6/4.5、Claude Opus 4.8、Claude Haiku 4.5、GPT-5.5/5.4、Gemini 3.5 Flash、DeepSeek V4 Flash/Pro、Qwen3.6 Plus、MiniMax M3、Kimi K2.7 Code、Grok 4.3、GLM 5.2。
  - Anthropic 和 OpenAI 模型列表重新排序，最新模型排在前面。

### 🐛 Bug 修复

- **TUI 审批详情在实时视图中可见性**
  - 修复排队审批请求在等待用户输入时不在实时 transcript 中显示详情的问题。
  - 现在跟踪当前审批消息索引，确保审批提示期间保持可见。
  - 审批完成后正确清除索引，并在状态重置/清除路径中重置。

- **TUI 工具弹窗性能与显示**
  - 工具弹窗渲染输出新增行级缓存，避免每次渲染时重新解析完整 transcript。
  - 新增按条目缓存展开的工具结果，避免重复格式化。
  - 所有 transcript 状态变更点现在都会调用 `invalidateToolModalCache()` 保持缓存一致。
  - 修复展开视图中 edit 工具结果重复显示 diff 片段的问题，提取了专用的 edit header 格式化函数。
  - 工具弹窗现在默认在顶部（offset 0）打开，而不是滚动到底部。

### 🧪 测试

- 新增回归测试，验证展开的 edit 输出不会重复 diff 片段。

---

## v0.1.46

### ✨ 新功能

- **Workflow Agent 实例 Key**
  - 新增重复逻辑 workflow agent 的 `:key`，有界 `while` 循环可以保持 agent 名称为字面量，同时将每轮结果保存为 `phase.agent[key]`。
  - 新增 `result-key`、`result-latest`，并支持 `(result "phase.agent" :key "r0")`，用于显式读取 keyed 结果或最新实例结果。
  - Keyed workflow worker 使用带实例的运行时 ID，例如 `agent-worker[r0]`，避免循环中的重复 worker 碰撞，同时保留稳定的逻辑 agent 名称。

- **Workflow Lint 工具**
  - 新增 `workflow_lint`，可在不运行 worker agents 的情况下验证 workflow JavaScript DSL。
  - Lint 会检查 JavaScript 语法、workflow/phase/agent 表单、关键字参数、必需 prompt，以及 result 引用。
  - 将 lint 工具与 workflow run/status/cancel 工具一起注册，并更新 workflow prompt 指引：非平凡的生成或修改后 workflow 应先 lint 再执行。

- **可配置上下文压缩**
  - 新增 `tokenizer`、`tokenizerModel` 和 `template` 压缩配置，并贯通 CLI、print 模式、ACP、Serve、Channels、TUI 模式切换和 delegate agent factory。
  - 新增内置压缩摘要模板：`default`、`code` 和 `conversation`，长会话可按任务类型保留更合适的 checkpoint。
  - 引入 token 估算器抽象，同时保持 `auto` 和 `generic` 使用现有 chars/4 通用估算器。
  - Compaction entry 现在记录 summary version、previous compaction ID 和 last summarized entry ID，便于 session replay 与调试。

### 🐛 Bug 修复

- **Context Compaction Replay**
  - Print 模式现在会在运行 agent 前恢复 session replay 历史，保留此前对话上下文。
  - 手动和强制 compaction 现在会检查是否真的存在可压缩的旧历史，避免只剩近期上下文时仍触发压缩。
  - Replay 已压缩消息时会移除保留消息中的旧 usage metadata，避免过期 token 统计泄漏到后续运行。

- **并发文件写入**
  - 新增进程级内存文件锁管理器，默认 tool registry 共享同一个管理器。
  - `write` 和 `edit` 在读取和修改文件前会获取按文件粒度的锁，避免多个 agent 并发写同一目标文件时互相交错覆盖。
  - 等待锁时支持 context 取消和 deadline；等待被中断时会报告当前锁持有者。

### 🔧 重构

- **预发布包发布**
  - `npm-publish-pre` 现在会先同步并使用 `-pre` 版本后缀构建 npm packages，再发布预发布包。
  - 更新 npm package metadata 和各平台 optional dependency 版本为预发布版本。

- **命名 Workflow Worker Agents**
  - Workflow worker agent 现在使用由 DSL agent 名称派生的确定性 ID（`agent-<name>`），改善事件归属和后台 agent 可见性。
  - Workflow skill 指引已记录该 ID 映射，并建议在同一个 workflow 内保持 agent 名称唯一。

### 📚 文档

- 更新 Workflow 模式文档、工具参考和 `workflow-javascript` skill，记录 `:key`、keyed 结果读取，以及有界 while 循环写法。
- 记录 context compaction 的 `tokenizer`、`tokenizerModel` 和 `template` 配置，包括内置模板选项，以及 idle compaction 设置当前为预留/弃用字段的状态。
- 澄清 Ctrl+O 详情弹窗中的按键提示，包括切换目标、翻页、滚动和关闭。
- 记录 TUI scrollback 的取舍：已完成 transcript block 会打印到原生终端 scrollback，以保证选择和历史滚动稳定；用户输入仍应按 block 打印，而不是无缓存流式输出，避免干扰 Bubble Tea 的 live view 重绘。

### 🧪 测试

- 新增 workflow runner、lint、集成和 skill 覆盖，验证 keyed 重复 agent 和 keyed result 查询。
- 新增 context compaction 测试，覆盖自定义 token estimator、模板解析、配置化摘要 prompt、compaction metadata、可压缩性检查，以及 session replay usage 清理。
- 新增 Serve 和 Channels 测试覆盖 `/compact` 在只剩近期上下文可保留时的行为。
- 新增 workflow lint 测试，覆盖有效 source 收集和缺失 result 引用错误。
- 新增 workflow 集成测试，验证 DSL agent 名称会反映到运行时 worker agent ID。
- 新增文件锁测试，覆盖等待/取消行为、默认管理器共享，以及 `write`/`edit` 的 context 处理。

---

## v0.1.45

### ✨ 新功能

- **Workflow Skill 渐进式参考文档**
  - 将 workflow JavaScript/DSL 文档从 system prompt 中提取为独立的 `workflow-javascript` skill，减少 system prompt 体积。
  - 引入渐进式参考结构：skill 索引页列出 9 个参考文件，按需加载，核心规则默认加载。
  - 8 个模式指南：研究与调研、串行与并行组合、决策路由、有界 While 循环、水平多 Agent 协作、主从小团队、评估优化器评审轮次、治理与人审检查点。
  - 每个参考文件包含可直接复制的 JavaScript 骨架示例和模式选择指引。
  - `EnsureProjectSkill` 自动在项目 `.skills/workflow-javascript/` 下创建 skill 和所有参考文件，不覆盖用户已有的自定义内容。
- **Workflow 超时控制**
  - `workflow_run` 新增可选 `timeoutSeconds` 参数，有明确上限的长 workflow 可设置合适的超时时间；需要持续运行的 workflow 可设置为 `0`，避免触发默认 agent 级 deadline。

- **goja v0.0.2 升级**
  - `goja` 依赖从 v0.0.1 升级到 v0.0.2，扩展了 JavaScript 支持范围。
  - 新增 backquote/comma、`let*`/`while`/`cond`/`catch`/`throw`/`lambda`/`defun`/`defmacro`/`with-current-buffer`/`save-current-buffer` 等特殊形式支持。
  - 新增内置函数：`cons`/`car`/`cdr`/`nth`/`append`/`reverse`/`member`/`assoc`/`funcall`/`apply`/`macroexpand`、算术与谓词函数、以及内存缓冲区 + marker 内置函数。
  - 新增 v0.0.2 JavaScript 特性的全面测试覆盖。

### 🔧 重构

- **Serve Session 级 Skills 支持**
  - Serve session 现在支持独立的 `SkillsMgr` 和 `ExtraContext`，使 delegate 子 Agent 继承 session 级状态。
  - `/skill` 和 `/skills` 命令改为操作 session 级 skills，而非全局 server 级。

- **System Prompt 精简**
  - Workflow JavaScript VM 语法和 DSL 表单的详细说明从 system prompt 移除，改为引用 `workflow-javascript` skill。
  - system prompt 中仅保留关键约束和调用说明，显著减少 token 占用。

- **Workflow Skill 参考文件职责澄清**
  - 重命名参考文件标题以更清晰："连续循环与迭代任务" → "有界 While 循环"，"评估优化器与评论家循环" → "评估优化器评审轮次"。
  - 拆分模式选择指引：有界 while 循环用于带停止条件的运行时重复；评估优化器用于单轮草稿/评审/修订流水线。
  - 新增约束：不要用编号 phase 模拟循环。
  - 渐进式参考状态标签统一为英文（"loaded" / "load on demand"），保持与 skill 其余内容一致。

### 📚 文档

- 新增 Workflow 模式使用指南和最佳实践文档（中英文），覆盖快速入门、核心概念、常见模式和避坑指南。
- 同步各文档页面的 workflow 引用：在功能概览中新增动态 Workflow 章节，在使用场景中新增 workflow 编排场景，并从工具参考文档添加交叉链接。
- 在 `workflow-javascript` skill 和文档中澄清 workflow 隐式默认值与限制：worker `:max-iterations` 默认值和失败行为、`workflow_run timeoutSeconds`、`concurrency` 默认值、继承的 `:mode`、默认 `:tools`、当前工作目录行为、禁用嵌套编排，以及不支持的 per-worker 选项。

### 🧪 测试

- 新增 workflow skill 测试，验证 skill 文件和 8 个参考文件的创建、不覆盖已有文件、缺失引用自动补全。
- 扩展 workflow runner 和 JavaScript 测试覆盖。
- 新增参考内容清晰度测试，验证循环与评估优化器模式不重叠。

---

## v0.1.44

### ✨ 新功能

- **Dynamic Workflows**
  - 新增独立 `--workflows` 模式，支持 CLI、ACP 和 Serve，不依赖 `--multi-agent`。
  - 新增 JavaScript workflow 工具：`workflow_run`、`workflow_status` 和 `workflow_cancel`。
  - Workflow runtime 支持 phase、series/parallel 执行、并发限制、worker agent 任务、结果汇总和运行日志。
  - 新增 workflow run 状态持久化，并在 TUI 与 Serve 中提供 `/workflows` 状态命令。
  - 新增进程内 active run 取消能力，`workflow_cancel` 和 `/workflows cancel <id>` 可中断运行中的 workflow。

- **Z.AI 供应商适配器**
  - 新增 `vendor_zai.go`，注册 `zai` 供应商适配器，域名 `api.z.ai` 和 `open.bigmodel.cn`，设置 `thinkingFormat: zai`。
  - 更新 `zai` 和 `zai-coding-cn` 供应商配置：设置 `Vendor: "zai"`、`ThinkingFormat: "zai"`，更新 base URL 为 coding 端点，新增 `glm-5v-turbo` 视觉模型。

- **Kimi 供应商更新**
  - `kimi` 供应商适配器新增 `api.kimi.com` 域名，支持自动供应商检测。
  - `kimi-coding` 供应商配置新增 `User-Agent: KimiCLI/1.5` 请求头。
  - `moonshotai`、`moonshotai-cn`、`fireworks`、`opencode-go` 供应商新增 Kimi K2.7 Code 和 K2.7 Code HighSpeed 模型。

- **新模型**
  - `opencode-go` 供应商新增 `GLM-5.2` 模型（1M 上下文窗口，262K 最大输出）。
  - `fireworks` 供应商新增 Kimi K2.7 Code Fast 模型。

### 🐛 Bug 修复

- **TUI Agent 事件处理**
  - 修复了在流式响应中途发生错误事件时，部分响应文本未提交到终端 scrollback 的问题，确保部分内容不会丢失。
  - 新增回归测试，验证错误发生时流索引和打印队列行为正确。

- **版本号字符串**
  - 修复 `Makefile` 中 `git describe` 使用 `--abbrev=0`，确保生成干净的标签版本号，不附带 commit 数量和 hash 后缀。
  - 修复 `sync-npm-version.sh`，去除版本号中的 commit 数量和 hash 后缀。
  - 更新 `npm/bin/mothx`，使用 GitHub raw URL 作为安装脚本的 fallback。

### 🔧 重构

- **Agent Manager 确定性排序**
  - `AgentManager.List` 现在按启动时间和 ID 排序，确保列表顺序稳定可预测。
  - 提取 `resetAgent`/`abortAndResetAgent` 辅助函数，减少 TUI commands 中的代码重复。
  - TUI 创建 Agent 时现在会在 config 中设置 Agent ID。

### 📦 依赖

- 新增 `github.com/startvibecoding/goja v0.0.1`，作为 workflow DSL 执行使用的内嵌 JavaScript 子集解释器。

### 📚 文档

- 在 `docs/proposal/` 下新增 dynamic workflows JavaScript 方案文档。
- 更新中英文工具文档，补充 workflow 工具用法、仅支持 JavaScript DSL 的约束和取消范围说明。

### 🧪 测试

- 新增 workflow runner/store/tool 测试，覆盖 JavaScript 执行、并行 worker、结果汇总、持久化、工具注册隔离和 active run 取消。
- 新增 prompt 与 CLI flag 测试，确认 workflow 模式不会污染 multi-agent、delegate 或 worker agent prompt。
- 新增 `VendorFromBaseURL` 测试用例：`api.kimi.com`、`api.z.ai`、`open.bigmodel.cn`。
- 新增 agent manager 测试，验证列表排序的确定性。

---

## v0.1.43

### 🐛 Bug 修复

- **TUI 输入 Flush**
  - 修复 TUI 中 `flushInputQueue` 未将其返回值作为 `tea.Cmd` 返回的问题，确保排队的按键在处理 `Enter`、`Tab`、`Up`、`Down` 等事件前正确刷新。

### 🔧 重构

- **移除未使用的 `mergeSettings`**
  - 移除未使用的 `mergeSettings()` 函数及相关测试；项目 settings 合现已由 `LoadSettings` 直接处理。
  - 重写 `settings_zero_test`，改为通过实际文件 I/O 调用 `LoadSettings()` 进行测试，而非直接 JSON 反序列化。

### 📦 依赖

- **GoStreamingMarkdown 更新**
  - 将 `github.com/startvibecoding/GoStreamingMarkdown` 从 `v0.0.2` 更新到 `v0.0.3`。

### 🧪 测试

- 新增测试验证 `Enter` 键在应用命令建议前先刷新排队输入。

---

## v0.1.42

### ✨ 新功能

- **TUI 多行输入**
  - 将 prompt 输入框替换为可复用的 TUI editor 组件，支持真正的多行 prompt 编写。
  - `Alt+Enter` 和 `Ctrl+J` 现在用于插入换行；`Enter` 仍用于提交 prompt。
  - 小型多行粘贴现在会保留换行，不再压平成空格；大型粘贴仍使用 paste marker。
  - `Up` / `Down` 会优先在多行输入内移动光标，只在输入边界处浏览 prompt 历史。

### 🐛 Bug 修复

- **TUI 输入编辑**
  - `Home` / `End` 编辑键现在能正确传递到输入 editor，不再被顶层 TUI 处理吞掉。
  - 修复在排队按键尚未 flush 时浏览 prompt 历史会丢失当前草稿的问题。
  - `/clear` 在输出清空确认后会重置 printed-message 记录，避免清空对话后复用陈旧的 transcript 打印状态。

### 📚 文档

- 更新 TUI 键盘快捷键文档，补充多行输入、插入换行、历史导航和工具 Modal 行为说明。

### 🧪 测试

- 新增多行 prompt 输入、`Alt+Enter` / `Ctrl+J`、小型多行粘贴保留、prompt 历史边界导航、Home/End 输入编辑和 `/clear` transcript 状态重置相关测试。

---

## v0.1.41

### ✨ 新功能

- **TUI 界面重设计**
  - 新增启动 Header，展示 Vibe Logo、版本、Provider/Model 和当前工作目录。
  - 重设计 Footer，展示模式、模型、cwd、当前/上次请求耗时、沙箱、上下文窗口用量、缓存指标和快捷键提示。
  - Agent 运行时新增内联 Loading 指示器，包含 spinner、耗时和取消提示。
  - `plan` 工具新增 sticky todo list，只展示未完成步骤，长任务执行时仍能持续看到当前计划。
  - 多 Agent 场景新增 Agent Tab Bar，当存在多个 Agent 时展示活跃 Agent 和状态。
  - 新增紧凑工具显示模式，可用 `Ctrl+G` 切换；工具输出折叠为单行摘要，详细内容仍可通过 `Ctrl+O` 查看。

- **终端原生 Scrollback Transcript**
  - 已完成的 transcript block 现在通过 Bubble Tea `Program.Println` 打印到终端原生 scrollback，只把实时流式内容保留在受管理的 TUI 视图中。
  - 改善长对话中的鼠标滚动、终端选择/复制和历史查看体验。

- **TUI 组件基础设施**
  - 在 `internal/tui/components/` 下新增可复用的 editor、suggestion list 和 vertical scroll 组件，包含 CJK 感知的 buffer 与渲染行为。

- **回复格式指南**
  - System prompt 新增格式约束，减少不必要的粗体、标题和列表，除非用户明确要求或内容确实需要结构化表达。

### 🐛 Bug 修复

- **TUI 工具结果打印**
  - 工具结果更新现在通过一次性 transcript 打印路径输出，而不是只刷新内存中的 live content，避免已完成工具输出从终端 scrollback 中消失。

- **Viewport 清理**
  - 在 TUI 历史迁移到终端原生 scrollback 后，移除过时的 viewport 状态重置逻辑。

### 📦 打包

- 更新 npm installer 包元数据及各平台 optional package 版本到 `0.1.40`。

### 🧪 测试

- 新增 TUI editor、suggestion list 和 vertical scroll 模型的组件测试。
- 更新 TUI cache/render 测试，覆盖 Header、Footer、终端原生 scrollback transcript 打印、紧凑显示模式和简化后的 viewport 行为。

---

## v0.1.40

### ✨ 新功能

- **GoStreamingMarkdown 渲染器**
  - 将 print 模式、本地 TUI 和 Channels 远程 TUI 的 Markdown 渲染从 Glamour 替换为 `github.com/startvibecoding/GoStreamingMarkdown`（`gsm`）。
  - 移除本地模块替换，改为直接依赖远程 `github.com/startvibecoding/GoStreamingMarkdown` 模块。

- **Delegate 模式（阻塞式单子 Agent 委托）**
  - 新增 `delegate_subagent` 工具：同步运行一个子 Agent，等待其完成并返回摘要结果，同时限制同一时间只能运行一个 delegate。
  - Root、ACP、Serve 命令新增 `--delegate` CLI 参数。
  - TUI 与 Serve 新增 `/delegate [on|off|status]` 斜杠命令。
  - Serve 配置（`serve.json`）新增 `enableDelegate` 选项。
  - System prompt 新增专门的 **Delegation Mode** 章节，包含上下文成本启发式、正反示例和结果解读指南。
  - 新增 `AgentFactoryOptions.DelegateEnabled`，便于程序化启用。
  - 子 Agent system prompt 现在使用结构化汇报格式（`Result`、`Evidence`、`Changes`、`Risks`），并补充负向搜索结果、测试执行和简洁性的说明。
  - `delegate_subagent` 结果中新增 `tool_calls` 计数和按工具名统计的 `tool_breakdown`。

- **子 Agent 执行模式继承**
  - `subagent_spawn` 与 `delegate_subagent` 现在会继承父 Agent 的执行模式（`plan`/`agent`/`yolo`），不再硬编码为 `agent`。
  - `executeSingleToolCall` 通过 context 注入父 Agent 模式。
  - 子 Agent policy 的 `AllowedModes` 从 `["agent"]` 扩展为 `["plan", "agent", "yolo"]`。

- **多 Agent 审批处理改进**
  - 移除了容易造成死锁的 `newApprovalForwarder`（基于 Mutex pending map 的同步审批 handler）。
  - 子 Agent 审批请求现在通过事件通道（`sendParentEvent`）转发，不阻塞工具执行。
  - TUI 在 `pendingApproval` 中跟踪 `agentID`，并通过 `handleApprovalResponse` 将审批响应分发给正确的子 Agent。

### 🐛 Bug 修复

- **TUI 中止原因显示**
  - 当 TUI Agent 会话被中止（用户按 Esc 或切换模式）时，错误信息现在会包含中止原因，例如：`"Error: aborted (reason: user pressed Esc)"`。
  - TUI `App` 结构中新增 `pendingAbortReason`，并在 `EventError` 后清理。

### 🧪 测试

- 更新 TUI Markdown 渲染断言以匹配 `gsm` 行为，同时保留内容完整性和视口宽度限制的覆盖。
- 新增 `TestDelegateSubAgentTool` 和 `TestDelegateSubAgentToolMissingTask`，验证阻塞式子 Agent 委托。
- 更新 `TestSubAgentPolicyDefault`，期望扩展后的允许模式 `["plan", "agent", "yolo"]`。
- 更新 `TestAgentManagerEnforcesSubAgentPolicy`，默认允许 `yolo` 模式。
- 新增 `TestAgentErrorIncludesAbortReason`，覆盖 TUI 中止原因渲染。

---

## v0.1.39

### ✨ 新功能

- **子 Agent 继承 Skills 与 Plan 工具设置**
  - `AgentFactory` 新增 `skillsMgr` 参数，子 Agent 现在自动继承父会话的 `skill_ref` 工具。
  - `RegistryConfig` 新增 `EnablePlanTool` 字段，`settings.json` 中的 `enablePlanTool` 设置现在会正确传播到子 Agent 注册表，确保父子 Agent 之间工具可用性一致。

- **会话压缩重放状态持久化**
  - 会话现在会持久化压缩重放状态（`ReplayState`），压缩后的会话可以在重启后正确恢复。
  - `LoadHistoryState` / `GetHistoryState` 跟踪每条消息的 Session Entry ID，使压缩边界（`firstKeptEntryID`）在会话重载后不会丢失。
  - Serve 和 Channels 现在使用 `LoadHistoryState` 替代 `LoadHistoryMessages`，确保压缩后的会话重放准确。
  - TUI 在手动压缩期间阻塞用户输入，将 `agentStartMsg`/`compactionStartMsg` 合并为统一的 `agentStreamStartMsg`。


- **TUI 视口重写与 CJK 支持**
  - 使用 `bubbles/viewport` 和 `charmbracelet/x/ansi` 替换自定义 ANSI 解析和换行逻辑，修复快速流式输出时 CJK/ASCII 混合文本的字符交错问题。
  - Think 消息与 Assistant 消息现在使用独立的渲染路径。
  - 长路径和 URL 现在能正确按词换行，不会在 token 中间截断。
  - Markdown 渲染不再溢出视口宽度。
  - 新增 `renderutil` 工具包，包含混合 CJK/ASCII 换行、ANSI 序列完整性和完整文件渲染的全面测试覆盖。

- **Grep 工具输出限制**
  - `grep` 工具在流式输出时现在会限制输出大小，防止大型结果集消耗过多上下文。

### 🐛 Bug 修复

- **Google 工具结果分组**
  - 修复 Google/Gemini Provider 的工具结果分组逻辑，正确配对工具调用与其结果。

- **TUI Compact 命令**
  - `/compact` 命令现在立即触发压缩，不再等待下一个 Agent 轮次。

- **Agent 退出路径一致性**
  - 抽取 `agentEndEvent()` 辅助函数，消除 8 个退出路径中的重复事件发射代码。
  - 修复 Session 保存错误路径上缺失的 `EventAgentEnd` 事件。
  - 在 `ShouldStopAfterTurn` 路径中补充缺失的 `usage`/`contextUsage` 元数据。

### 🧪 测试

- 新增 `TestAgentFactorySubAgentsRespectPlanToolSetting` 测试，验证 `enablePlanTool` 设置正确传播到子 Agent 注册表。
- 新增 `TestAgentFactorySubAgentsRegisterSkillRef` 测试，验证配置了 Skills Manager 时子 Agent 继承 `skill_ref` 工具。
- 新增 `agentEndEvent` 一致性和 `ShouldStopAfterTurn` 元数据的测试。
- 新增 TUI 固定高度渲染、CJK 换行和 ANSI 完整性的全面测试。

---

## v0.1.38

### ✨ 新功能

- **自定义 Provider 模型自动回退（Fallback）**
  - 当显式指定了自定义 Provider（通过 CLI、Serve 或 Channels）但没有指定 Model ID 时，Factory 现在会自动回退并使用该 Provider 下的**首个可用模型**，而不是错误地采用 `settings.DefaultModel`（默认模型通常属于默认 Provider）。
  - 避免了在使用非默认 Provider 且未指定具体模型时，因加载了不匹配的全局默认模型而导致“未找到模型”的错误。

- **Channels 默认配置解析优化**
  - 优化了 Channels 的 `GetDefaultModel` 逻辑：当 `serve.json` 中配置了 `DefaultProvider` 但 `DefaultModel` 留空时，系统现在能正确返回空字符串，从而触发上述自定义 Provider 首选模型的回退逻辑，不再强行透传 `settings.json` 中的全局默认模型。

- **完善公开的 Agent SDK 包与示例代码**
  - 完整补全了公开 `agent` 包与底层实现之间的流事件字段桥接映射（支持 `Messages`、`TurnMessage`、`TurnToolResults`、`Message`、`ToolCall`、`ToolDiff`、`ToolError`、`PartialResult`、`Plan`、`Usage` 和 `ContextUsage`）。
  - 修复了因内部流事件特有类型（如 `StreamThinkSignature`）导致的公开 `StreamEventType` 枚举下标错位问题，实现了健壮的显式双向转换。
  - 实现了 `PublicProviderAdapter` 适配器，将内部 Provider 无缝桥接到公开 `agent.Provider` 接口，并在初始化时自动关联，完美规避了 Go 包循环导入问题。
  - 在顶层新增了 `example/` 目录，设计并编写了两个极具代表性的高阶演示程序（`simple_agent` 和 `custom_provider`）以及详尽的中英文双语 `README` 文档，直观演示了如何自定义 LLM 后端、挂载内置工具框架并启动流式 Agent。

### 🧪 测试

- 在 `internal/provider/factory_test.go` 中新增 `TestCreateFallbackToFirstModel` 测试，覆盖当模型 ID 为空时，自定义 Provider 和内置 Provider 自动回退到其首选模型的行为。
- 在 `internal/serve/channels/config_test.go` 中新增针对 `GetDefaultModel` 方法的测试用例，覆盖 Channels 配置中仅指定 `DefaultProvider` 时的行为。

---

## v0.1.37

### ✨ 新功能

- **Vertex AI API Key 认证支持**
  - 新增对 Google Vertex AI 使用 API key（通过 `x-goog-api-key` 请求头）直接认证的支持，无需强制要求使用 gcloud OAuth 凭证（`ya29.`）。
  - 当配置了 API key 时，默认 baseUrl 会自动路由到 `https://aiplatform.googleapis.com/v1/publishers/google/models`（无需传入 project/location 参数）。
  - 完美保持对现有 OAuth bearer token 的向下兼容。

### 🐛 Bug 修复

- **Google 工具思维签名（Thought Signatures）**
  - 正确提取并在工具调用中传递 Gemini 的思维/推理签名，避免签名丢失或不匹配的问题。

- **TUI 原始 Bash 输出保留**
  - 在 Bubble Tea TUI 中渲染 `bash` 工具执行结果时，完美保留 ANSI 转义着色和原始的空格/换行符，避免被 TUI 框架误附加斜体等全局样式。

---

## v0.1.36

### ✨ 新功能

- **Doctor 子命令** (`mothx doctor`)
  - 新增诊断命令，检查环境、配置、Provider、沙箱、MCP 服务器、Session、技能和上下文文件
  - 报告 OS/架构、Go 版本、Shell、Home/工作目录
  - 校验 settings、serve 和 MCP 配置文件，带解析检查
  - 列出已配置的 Provider（API key 脱敏显示）、模型及其上下文窗口/最大 token/推理标志
  - 检查 bwrap 沙箱可用性和版本
  - 展示 MCP 服务器、Session 数量、技能目录和已发现的上下文文件
  - 未配置的 Provider（无 API key）静默跳过

### 🐛 Bug 修复

- **TUI 会话状态**
  - 修复 `/clear` 未清理 transcript 渲染状态、工具结果、助手 Markdown 缓存、活动流索引、Plan 面板和工具详情 Modal 状态的问题，与会话切换保持一致
  - 抽取共享的 transcript/input 重置辅助函数，减少不同清理路径的行为差异

- **TUI 模式切换**
  - 使用 Tab 循环切换模式时，现在会先中止正在运行的请求，与 `/mode` 行为一致，避免审批/提问响应发送到旧 agent

- **TUI Question 工具**
  - 提问工具的数字选项现在会解析为对应的选项文本，不再把原始数字发回给模型
  - 清理提问状态时也会清空当前问题元数据

- **TUI 警告与详情 Modal**
  - TUI 现在会显示 Context 压力和预算压力事件
  - Ctrl+O 在没有可展示的会话详情时会提示用户，而不是打开空 Modal

- **Context Pressure 阈值比较**
  - 修复 Context Pressure 阈值比较时单位不一致的 bug：`Percent`（0-100）与 `threshold`（0-1）直接比较，导致仅 ~0.5% 使用率就触发警告而非预期的 55%
  - 修复 `InitChannelsConfig` 项目模板，显式写入 `context_pressure_threshold` 和 `budget_pressure_threshold` 默认值，避免序列化为 0 导致禁用

- **实时消息渲染**
  - 实时助手消息会将 fenced code block 按 Markdown 渲染，同时普通文本保留 plain-text wrapping 路径，避免中英文被异常拆词换行

- **模型校验与 Compaction 修复**
  - 在 `ChatParams` 中传递 `ModelID`，让 Provider 知晓当前活跃模型
  - 将模型信息透传到 compaction/summary 生成，避免静默回退到默认模型
  - 模型未找到时返回错误并列出可用模型列表，不再静默回退
  - 统一 factory、serve 和 TUI 的模型错误提示

- **Google/OpenAI 工具结果文本提取**
  - 修复 Google 和 OpenAI Provider 在工具结果使用富 `Contents` 块而非纯 `Content` 字符串时发送空内容的问题
  - Google Provider 新增 `googleToolResultText()` 从 `Contents` 块提取文本
  - 修复 OpenAI 富工具结果分支中 `responseToolOutput()` 的使用

### 🧪 测试

- 新增 `/clear` transcript 清理、提问状态跟踪、空详情 Modal、压力警告、实时代码块渲染和普通文本换行的回归测试
- 新增 agent 级集成测试（`TestToolResultIsIncludedInNextProviderTurn`），验证含富 `Contents` 块的工具结果能正确传递到下一轮 provider 调用
- 新增 Google 和 OpenAI Provider 从 `Contents` 块提取工具结果文本的单元测试

## v0.1.35

### 🐛 Bug 修复

- **TUI 打印顺序修复**
  - 将分散的 `go program.Println(...)` 协程替换为单一 drain 协程（`printCh`），防止在 Bubble Tea 无缓冲 channel 上发生消息交错
  - `flushPendingPrints` 改用 `tea.Sequence` 替代 `tea.Batch`，保持打印顺序

- **显示宽度准确性**
  - `truncate()` 改用 `lipgloss.Width` 替代字节长度计算，CJK 字符（2 格）和 ANSI 转义序列（0 格）在 TUI 网格中正确对齐
  - 工具详情 Modal 标题分隔线使用 `lipgloss.Width` 计算正确行宽

- **工具输出改进**
  - `ls` 工具结果在折叠视图中显示 compact 摘要（去除空行），与 `bash` 输出行为一致
  - 工具结果渲染对多行摘要使用换行分隔符，不再强制压缩到单行

### 🧪 测试

- 新增 `formatters_test.go`，覆盖 ASCII、CJK 及混合内容的显示宽度截断测试

## v0.1.34

### ✨ 新功能

- **Channels 远程 TUI 客户端**
  - 将纯文本 WebSocket 客户端替换为 serve WebSocket 通道的 Bubble Tea TUI
  - 通过 Glamour 实现 Markdown 渲染与语法高亮
  - 可滚动的工具详情 Modal（Ctrl+O）、审批提示（Enter/Esc）和 question 工具支持
  - Plan 更新展示、Context 压力/预算警告和请求计时器
  - 已完成消息输出到终端原生 scrollback
  - 支持斜杠命令（`/clear`、`/mode`、`/model`、`/compact`、`/help` 等）
  - 新增 `internal/serve/channels/remotetui` 包：`app.go`、`render.go`、`input.go`、`remote.go`、`agent_events.go`、`approval.go`、`commands.go`、`formatters.go`、`tool_modal.go`、`events.go`

- **WebSocket 协议增强**
  - 新增 `question_request` / `question_response` 事件，支持 Plan 模式下通过 WebSocket 使用 question 工具
  - 新增 `plan_update` 事件，携带结构化 plan 步骤数据
  - 新增 `compaction_start` / `compaction_end` 事件，展示上下文压缩进度
  - `connected` 事件新增 `model` 和 `work_dir` 元数据
  - `approval_request` 事件新增 `approval_tool` 和 `approval_args`，丰富客户端展示

- **Dispatcher 重构**
  - 将 `buildAgent()` 从 `runAgent()` 中提取，改善消息平台与 WebSocket 路径间的 agent 创建复用

- **Provider 自定义 Header**
  - 新增 `providers.<name>.headers`，支持为 provider 请求携带自定义 HTTP header
  - Header 值支持与 `apiKey` 相同的 `${ENV}` 和需显式开启的 `!cmd` 解析

### 🧪 测试

- 新增 WebSocket Serve 服务器测试，覆盖连接、认证、聊天、审批、提问和命令流程

## v0.1.33

### ✨ 新功能

- **多项目技能目录支持**
  - Skills 管理器现支持从项目根目录下的 `.skills/` 和 `skills/` 两个目录加载技能
  - 优先级顺序：`.skills/` > `skills/` > 全局技能目录
  - 新增 `NewManagerWithProjectDirs` 构造函数，接受按优先级排列的项目目录列表
  - 新增 `ProjectSkillDirs` 辅助函数，返回标准项目技能目录列表
  - 更新所有调用点：CLI、ACP、Serve、Channels
  - 新增多目录优先级和普通 `skills/` 目录加载的测试

## v0.1.32

### ✨ 新功能

- **工具系统完整性**
  - 补充所有已注册工具的完整文档：`jobs`、`kill`、`question`、`memory`、`cron` 及 MCP 动态工具
  - `jobs` 工具：列出并查看通过 `bash async=true` 启动的后台任务，支持清理已完成任务
  - `kill` 工具：通过 Job ID 终止正在运行的后台任务
  - `question` 工具：Plan 模式下 AI 可向用户提出多选问题以澄清需求
  - `memory` 工具（Channels）：通过 `memory.md` 实现跨会话持久记忆，支持 read/add/update/delete 操作
  - `cron` 工具（Channels/多 Agent）：通过子 Agent 执行定时后台任务，支持 `@daily`、`@weekly`、`@every N` 调度及单次执行
  - MCP 动态工具：来自 MCP 服务器的 tools/resources/prompts 在会话中自动发现和注册

- **Plan 模式提问工具**
  - 新增 `question` 工具，仅在 TUI + plan 模式下注册
  - AI 可向用户提出多选问题，用户选择预设选项或输入自定义答案
  - 用于在制定方案前澄清需求，形成更优质的计划
  - 通过 `QuestionHandler` 可选接口暴露（类型断言），不污染公共 `Agent` 接口

### 🐛 Bug 修复

- **Bash 工具输出安全**
  - 同步 bash 模式新增 1GB 输出限制，使用 `limitedBuffer` 防止无界 `bytes.Buffer` 导致 OOM

- **Channels `/compact` 命令**
  - 实现 Channels 消息模式下的 `/compact` 斜杠命令（之前是 TODO 桩）
  - 在 session 上设置 `ForceCompact` 标志，下次 agent 运行时消费以触发上下文压缩

- **Session 持久性**
  - `writeEntry` 写入后调用 `f.Sync()`，保证崩溃或断电后数据不丢失
  - 损坏的 session 行现在记录为 warning 并跳过，不再阻止 session 加载

- **Channels 审批竞态修复**
  - `ResolveApproval` 使用 `select` 发送，避免超时与审批竞态时写入已消费的 channel

- **子代理 Panic 日志**
  - `sendParentEvent` 在 recover 前记录 panic 值，便于诊断关闭 channel 的竞态

- **原子文件写入清理**
  - `writeFileAtomic` 移除 `defer os.Remove(tmpPath)`，改为各错误路径显式清理，避免成功后尝试删除已重命名的文件

- **Agent 循环检测可配置化**
  - `MaxConsecutiveNoText`（卡住检测阈值）可通过 `AgentLoopConfig` 配置（默认 95）
  - 修复错误消息中错误地将前后警告计数器相加的问题

- **Job Manager 自动清理**
  - `AddJob` 时自动 GC 30 分钟前完成的 job（每 5 分钟检查一次）

- **Cron 调度器错误日志**
  - `checkAndRun` 现在记录 store 错误，不再静默吞掉

- **TUI Bash 输出显示**
  - 压缩 bash 工具输出摘要，去除空行，避免 TUI 折叠视图中占用过高垂直空间

- **内嵌搜索工具**
  - 当当前架构没有内嵌 `rg` / `fd` 时，退回使用系统 `grep` / `find`

### 📦 分发

- 新增 Linux LoongArch64 (`loong64`) 构建与打包目标，包括 tarball、Debian 和 npm 包元数据

### ✅ 测试

- 新增 `limitedBuffer` 截断、`JobManager` GC、`writeFileAtomic` 清理、`sendParentEvent` panic 恢复、`MaxConsecutiveNoText` 可配置性、session fsync 持久性、损坏行容忍、`QuestionTool` 元数据/模式过滤/执行/错误处理的单元测试


## v0.1.31

### 🐛 Bug 修复

- **终端输入**
  - 输入框支持 Home/End 光标移动
  - 修复在权限审批提示中按 Esc 取消后，第一次回车提交的输入被吞掉的问题
  - 输入框支持 Up/Down 历史记录导航，并可反复上下选择历史输入

- **A2A 安全与可靠性**
  - A2A 默认监听地址从 `0.0.0.0` 改为 `127.0.0.1`
  - 为 `/a2a`、REST A2A 路由和 SSE 事件添加 Bearer token 认证，同时保持 Agent Card 公开
  - 将基于时间戳的 A2A task ID 替换为抗碰撞的随机 ID
  - A2A task store 读写改为使用 task 快照，避免外部意外修改共享状态

- **路径与 Session 安全**
  - 路径包含校验改为使用路径边界，而不是字符串前缀匹配
  - 禁止 context `extraFiles` 逃逸工作目录
  - 对 Channels session 路径组件进行安全编码，并在创建 session 时强制校验 `allowed_work_dirs`
  - 限制 session 删除只能删除配置 session 目录下的 `.db` 文件

- **认证、审批与资源限制**
  - Channels HTTP/WebSocket token 校验改为常量时间比较
  - Channels WebSocket 客户端改为通过 `Authorization: Bearer ...` 发送认证信息，不再放入 query string
  - ACP 权限请求超时后清理 pending 状态，并向调用方传播写入错误
  - 为 ACP、read 工具图片文件、微信响应和 cron A2A 响应增加大小限制
  - 为 cron A2A HTTP 请求增加超时

- **Memory、Context 与并发**
  - 为 memory store 操作增加锁
  - 修复 `memory.WriteAll()` 路径处理，并将 memory update/delete 限制在指定 section 内
  - Serve 在请求级 `temperature`/`top_p` 覆盖前克隆模型配置
  - Agent callback 使用 context/message 快照，避免共享引用
  - Cron job 状态变更通过 job store 串行化

- **配置与 Serve 加固**
  - `!command` API key 解析现在必须显式设置 `VIBECODING_ALLOW_SHELL_CONFIG=1`
  - 修复 Serve CORS，使其只回显被允许的请求 origin
  - Serve 在非 loopback 监听、`yolo` 模式且未开启认证时输出启动警告
  - 加固 platform home/shell fallback 行为

### 🧪 测试

- 增加 A2A 认证、task ID 唯一性、task 快照隔离和 working task message 持久化回归测试
- 增加路径逃逸、危险 session ID、memory section 操作、ACP 清理、CORS、UTF-8 截断和 shell-config opt-in 测试
- 已运行聚焦包测试，以及 A2A、agent、serve、cron 的 race 测试

### 📝 文档

- 更新 A2A、Channels、Serve、配置和安全文档，说明新的认证和加固行为

## v0.1.30

### ✨ 新功能

- **Provider 级 HTTP 代理**
  - 新增 `providers.<name>.httpProxy`，支持为不同 provider 配置不同 HTTP 代理
  - 未配置 `httpProxy` 时继续保留默认环境变量代理行为

- **Google Gemini 和 Vertex 厂商适配器**
  - 新增原生 `google-gemini` 和 `google-vertex` provider，使用 Google `streamGenerateContent`
  - 支持 Gemini API 和 Vertex AI 原生 Gemini 端点的 baseUrl 自动识别
  - 新增 Gemini API key 和 Vertex bearer token 的默认 Google provider 模板
  - 更新 provider 文档与识别测试覆盖

- **Hosted Web Search 工具**
  - 为 CLI 和 ACP 运行新增 `--web-search`
  - 新增顶层 `webSearch` 配置，包含 `enabled`、`provider`、`providerType` 和 `model`
  - 仅在启用时注册 hosted `web_search`，并与本地 function tools 保持隔离
  - 新增 OpenAI Responses API 映射到 `web_search`
  - 将 Responses web search 映射改为 provider-neutral 的 `web_search`，兼容 provider 不必命名为 `openai`
  - 新增 Anthropic Messages API 映射到 `web_search_20250305`
  - 将 `webSearch.model` 保留为 provider-neutral metadata，用于后续路由和成本展示扩展

- **默认 Provider 模板**
  - 新增 OpenAI、Anthropic 和 Xiaomi MiMo 默认 provider 配置
  - 保留 DeepSeek providers，并继续使用 `deepseek-openai` 作为默认 provider/model
  - 首次生成的 `settings.json` 现在包含默认关闭的 web search 配置，以及 OpenAI/Anthropic/Xiaomi provider 模板

### 🧪 测试

- 增加 OpenAI Responses 和 Anthropic Messages hosted web search 序列化测试
- 增加 web search 配置默认值、CLI flag 解析和 hosted tool metadata 传递测试
- 增加 macOS 默认配置目录解析测试

### 🐛 Bug 修复

- **macOS 配置目录**
  - 将 macOS 默认全局配置目录与 Linux 统一为 `~/.vibecoding`

- **发布版本号**
  - npm 和发行包版本检测默认不再附加 `dirty` 后缀
  - 将 npm package metadata 规范化为 `0.1.30`

## v0.1.29

### 🐛 Bug 修复

- **NPM 包装修复**
  - 修复 `npm/bin/mothx` 入口脚本，确保安装包正确附带可执行包装器
  - 调整 `build-npm.sh` 和 `build-npm-packages.sh` 保证包装器一致性

## v0.1.28

### ✨ 新功能

- **Per-Model 温度/Top-P 配置**
  - 为 `ModelConfig` 和 `Model` 新增 `temperature` 和 `top_p` 字段，支持逐模型参数调优
  - 在 OpenAI 和 Anthropic 提供商中打通，使用 `omitempty` — `nil` 表示使用 API 默认值
  - 在 provider factory、agent loop、ACP 模式中打通
  - Serve 模式支持请求级 `temperature`/`top_p` 覆盖（通过 `ChatParams`）
  - 未配置时完全省略参数（不会向 API 发送零值）

- **OpenAI Responses API 支持**
  - 新增独立的 OpenAI Responses provider 路径，通过 `api: "openai-responses"` 启用
  - 支持 Responses 流式输出、工具调用、reasoning summary 和 prompt cache 参数
  - 在 provider `responses` 配置中暴露 Responses 专用设置，默认启用 prompt cache
  - 新增模型兼容标志 `supportsPromptCacheKey` 和 `supportsReasoningSummary`

### 🧪 测试

- 提升 OpenAI Responses API 和 Anthropic 请求解析相关测试覆盖
- 将 Anthropic 测试改为内存 HTTP mock，避免依赖本地端口监听

### 📝 文档

- 更新 `AGENTS.md` 版本至 v0.1.28

## v0.1.27

### ✨ 新功能

- **Serve 模式** (`mothx serve`)
  - 新增消息平台网关模式，支持微信、飞书和 WebSocket
  - 持久化 per-user session，`/new` 时自动归档
  - 默认 `yolo` 模式，适合无人值守场景
  - 智能审批分级策略（low/medium/high 风险等级）
  - 用户白名单访问控制
  - WebSocket 流式推送：text_delta/think_delta/tool_call/tool_result/tool_diff/usage/done

- **A2A 协议** (`mothx a2a`)
  - 新增 Agent-to-Agent 协议服务器（JSON-RPC 2.0 over HTTP + SSE 流式）
  - 独立模式：`mothx a2a start`（端口 8093）
  - Agent Card：`/.well-known/agent.json`
  - Task 生命周期：submitted → working → completed/failed/canceled
  - REST 端点：`/a2a/send`、`/a2a/task`、`/a2a/task/cancel`、`/a2a/events`
  - **A2A Client**：`mothx a2a send <message>` 向其他 A2A Server 发送任务
  - **A2A 发现**：`mothx a2a discover <url>` 获取远程 Agent Card
  - **A2A 调度**：Cron 任务支持 `--a2a-target` 参数，定时向 A2A Server 发送任务

- **A2A Master 模式** (`--enable-a2a-master`)
  - 通过 `a2a-list.json` 配置多个远程 A2A Agent
  - 注册 `a2a_dispatch` tool，LLM 可自动向远程 agent 分发任务
  - 支持全局（`~/.vibecoding/a2a-list.json`）和项目级（`.vibe/a2a-list.json`）配置
  - `--init-a2a-master-config` 生成示例配置文件
  - 默认关闭，需显式启用

- **A2A 配置初始化**
  - `mothx a2a --init-a2a-config` 生成 `a2a.json` 配置模板
  - `mothx --init-serve` 生成 `serve.json` 配置模板（已有）
  - `mothx --init-a2a-master-config` 生成 `a2a-list.json` 配置模板
  - 所有 `--init-*` 支持 `--force` 覆盖已存在的文件

- **场景演示文档**
  - 新增 `docs/scenarios.md`（中英文），覆盖 9 种实际使用场景
  - 涵盖：日常编码、CI 集成、多 Agent、VS Code ACP、A2A 服务器、
    A2A Master 跨机器调度、Serve HTTP 网关、Channels 消息平台、组合模式

- **文档全面更新**
  - `architecture.md`：补全全部模块（a2a/acp/serve/mcp/memory/messaging/vendored）
  - `tools.md`：新增 `a2a_dispatch` 和 `skill_ref` 工具文档
  - `cli-reference.md`：新增 `--enable-a2a-master`、`--init-a2a-master-config`、
    `--init-serve`、`--force`、`a2a` 子命令文档
  - `README.md`：架构图补全、新增运行模式总览

- **压力系统**
  - Context Pressure：55% context 使用率时触发 `EventContextPressure`（可通过 `context_pressure_threshold` 配置）
  - Budget Pressure：剩余 20% 迭代时触发 `EventBudgetPressure`（可通过 `budget_pressure_threshold` 配置）
  - 一次性触发：每个阈值越界只触发一次，非每轮触发
  - 消息平台通过进度回调接收压力警告

- **智能审批（分级策略）**
  - low 风险：自动批准
  - medium 风险：自动批准 + 通知用户
  - high 风险（WebSocket）：发送 `approval_request`，等待用户 `approval_response`（5 分钟超时）
  - high 风险（消息平台）：自动拒绝 + 通知用户
  - 命令风险分类：基于 bash 命令模式的 low/medium/high 分级

- **Provider/Model 配置**
  - `serve.json` 新增 `default_provider` / `default_model`（覆盖 `settings.json`）
  - `mothx serve` 新增 `-p`/`--provider` 和 `-m`/`--model` CLI 标志
  - 优先级：CLI 标志 > `serve.json` > `settings.json`

- **多 Agent 模式** (`--multi-agent`)
  - 启用子 Agent 工具（spawn/status/send/destroy）
  - 通过 `serve.json` 的 `multi_agent` 字段或 `--multi-agent` CLI 标志配置

- **Sandbox 模式** (`--sandbox`)
  - 可选 bwrap 沙箱隔离（默认关闭）
  - 通过 `serve.json` 的 `sandbox` 字段或 `--sandbox` CLI 标志配置

- **MCP 工具继承**
  - Channels 自动加载全局/项目 `mcp.json` 中的 MCP 服务器
  - MCP 工具按 session 注册，session 移除时自动关闭连接

- **消息平台进度事件推送**
  - agent 执行过程中实时向微信/飞书推送工具执行进度
  - 格式：`[tool]: args ✅/❌`（工具）、`💭 ...`（思考过程）
  - agent 完成后发送完整总结

- **memory 工具**
  - `memory` 工具支持 read/add/update/delete 操作
  - section 级操作（User Profile、Working Memory、Lessons Learned）
  - 默认写入 `.vibe/memory.md`（项目目录）
  - 查找优先级：`memory.path` 配置 → `.vibe/memory.md` → `<GLOBAL_DIR>/memory.md`
  - `/api/memory` HTTP 端点（GET/PUT）用于 memory 访问

- **Serve 通道管理**
  - `mothx serve` — 启动统一 API、Web UI 和消息通道运行时
  - `mothx serve init-config` — 生成 `serve.json`
  - Web UI 和 serve API 管理通道状态、会话、memory、webhook 和 cron 任务
  - `a2a start/stop/status/card` — A2A 服务器管理

### 📝 变更

- 微信 iLink 协议实现，零外部依赖（5 个文件：types/protocol/auth/crypto/wechat）
- 飞书 Bot 使用官方 SDK + WebSocket 长连接
- Shell Hooks 支持 pre/post tool call 外部脚本（JSON stdin/stdout）
- Webhook 入站路由，支持 HMAC-SHA256 签名验证
- WebSocket 使用 `golang.org/x/net/websocket`（标准库兼容）
- Serve 运行时进程生命周期管理

### 🐛 问题修复

- **NPM 安装包修复**
  - 修复发布流水线，确保 `vibecoding-installer` 始终包含可执行入口 `bin/mothx`。
  - 新增 `scripts/npm-installer-wrapper.js` 作为统一的 wrapper 逻辑源，并被 `scripts/build-npm.sh`
    与 `scripts/build-npm-packages.sh` 复用，避免实现分叉。
  - 调整 `npm/.npmignore` 与 `npm/bin` 的处理方式，避免误打包非发布文件，并通过 `files` 字段显式声明要发布内容。

- **Channels Webhook 投递与过滤**
  - 当 webhook 路由无法识别事件类型时，除非显式允许 `*`，否则按不匹配处理。
  - 为 webhook 路由新增 `delivery_target`，让微信/飞书投递拥有明确接收者。
  - 路由列表和配置模板会在存在投递目标时一并展示。

- **OpenAI Responses thinking 映射**
  - 将 `--thinking xhigh` 在 OpenAI Responses API 中映射为 `reasoning.effort: "high"`。

### 🧪 测试

- 将 webhook router 测试改为等待 handler 完成，去掉 `time.Sleep` 带来的竞态和不稳定。
- 增加无法推断事件类型时的 webhook 拒收测试。
- 增加 webhook delivery target 相关测试覆盖。

## v0.1.26

### ✨ 新功能

- **Serve 模式** (`mothx serve`)
  - 新增 HTTP 服务，对外暴露标准 OpenAI Chat Completions API (`/v1/chat/completions`、`/v1/models`、`/health`)
  - 任何兼容 OpenAI SDK 的客户端（Cursor、Continue、Open WebUI、Python SDK 等）可直接接入
  - 完整支持 Streaming (SSE) 和 Non-streaming 响应
  - 后端由 MothX agent 循环驱动，tool 执行对调用方透明

- **多 Session 支持**
  - 内置 `SessionPool` 支持并发 session，每个 session 拥有独立的 agent、工具和消息历史
  - 通过请求体中的 `x_session_id` 关联 session，未指定时自动创建
  - 可配置空闲超时 (`session.idleTimeoutSeconds`) 和最大 session 数 (`session.maxSessions`)

- **Serve Sub-Agent 支持**
  - 可选 `enableSubAgents` 配置，在 serve 模式下启用多 Agent 编排
  - 复用现有 `AgentFactory` / `AgentManager` / 子Agent 工具，无需改动核心 agent 逻辑

- **Bearer Token 认证**
  - 通过 `serve.json` 的 `auth.enabled` 和 `auth.tokens` 列表配置
  - 默认关闭；`/health` 端点始终不需认证

- **API 指令系统 (Slash Commands)**
  - `/clear`、`/mode`、`/model`、`/models`、`/sessions`、`/compact`、`/status`、`/skill`、`/skills`、`/help`
  - 当最后一条用户消息以 `/` 开头时触发，在 serve HTTP 层直接处理，不调用 LLM
  - 响应使用标准 OpenAI 格式，附加 `x_command` 扩展字段

- **Tool 可见性配置** (`toolVisibility.mode`)
  - `"content"` (默认): streaming 时通过 `content` 字段发送 tool 状态文本
  - `"sse_event"`: 通过扩展 SSE event 发送，适合自定义客户端
  - `"none"`: 完全透明，客户端只见最终文本

- **System Prompt 处理策略** (`systemPromptMode`)
  - `"append"` (默认): 客户端 system message 追加到内置 system prompt 末尾
  - `"ignore"`: 完全忽略客户端 system message

- **安全: allowedWorkDirs 白名单**
  - 请求级 `x_working_dir` 的目录白名单，支持路径分隔符感知的前缀匹配
  - 三层安全模型: L1 认证 + L2 目录管控 + L3 沙箱 (bwrap)

- **Serve Sandbox 支持**
  - 通过 `serve.json` 的 `sandbox.enabled` / `sandbox.level` 或 `--sandbox` flag 配置
  - 细节配置（allowedRead、deniedPaths 等）继承 `settings.json`

- **Serve 配置文件** (`serve.json`)
  - 独立配置文件，位于 `~/.vibecoding/serve.json`
  - 覆盖: 监听地址、认证、模式、沙箱、工作目录、目录白名单、session 管理、CORS、tool 可见性、system prompt 策略、请求超时、并发限制、日志
  - `mothx --init-serve` 生成配置模板；`--force` 强制覆盖

- **请求超时与并发控制**
  - `requestTimeoutSeconds` (默认 1800s)；streaming 有数据流动不超时
  - `maxConcurrentRequests` (默认 0 = 不限制)

### 📝 文档

- 新增 API 服务设计方案，包含完整架构、API 设计、安全模型和实现计划
- 更新 `AGENTS.md` 版本标注

## v0.1.25

### ✨ 新功能

- **多 Agent 模式**
  - 在 CLI、TUI、ACP 模式中新增可选的 `--multi-agent` 支持
  - 新增 `AgentManager`、`EventRouter` 和每个 Agent 独立的 registry，隔离工具、job manager、session、messages 与 context
  - 新增 `subagent_spawn`、`subagent_status`、`subagent_send`、`subagent_destroy` 工具，用于派生后台子任务
  - 新增多 Agent system prompt 指引，并限制子 Agent 继续派生子 Agent

- **Cron 定时任务**
  - 新增 `internal/cron`，支持 cron store 持久化与调度器测试覆盖
  - 在多 Agent TUI 工作流中新增 `/cron` 命令入口

- **Provider 厂商适配层**
  - 新增 `internal/provider/vendor*.go` 厂商适配注册机制
  - 将 provider/model 创建逻辑统一到 `internal/provider/factory`
  - 新增 DeepSeek、Xiaomi、Kimi、MiniMax、Seed、Qianfan、Bailian、Gitee、OpenRouter、Together、Groq、Fireworks、OpenAI、Anthropic 等厂商识别
  - 保持现有 provider 配置格式不变，同时支持厂商默认值和通用 OpenAI/Anthropic 兼容 fallback
  - 新增模型 `compat` 处理，覆盖 thinking 格式、reasoning effort、max token 字段、自适应 Anthropic thinking，以及 DeepSeek/Xiaomi assistant `reasoning_content`

### 🐛 问题修复

- session 首次 append 时自动初始化，避免子 Agent 写入 session 前必须显式初始化
- 修复子 Agent 测试中的后台运行清理顺序，确保临时目录删除前已等待并销毁派生 Agent
- 在 provider 创建逻辑迁移到共享 factory 后，保留 ACP Anthropic cache-control 行为

### 📝 文档

- 更新 `AGENTS.md`，补充 provider factory 与 vendor adapter 工作约定
- 将多 Agent 实施 checklist 更新为已落地架构/状态说明
- 删除已过时的根目录 `todo.md`

### 🧪 测试

- 新增 provider vendor 解析、provider factory 创建、OpenAI/Anthropic compat、多 Agent manager/router/sub-agent 流程、cron 存储/调度、session 自动初始化等测试覆盖
- 已通过 `make test`（`go test -v -race ./...`）

---

## v0.1.24

### ✨ 新功能

- **API 重试与指数退避**
  - 对暂时性错误（5xx、网络故障、速率限制）在初始 HTTP 连接阶段自动重试
  - 指数退避策略：`baseDelay × 2^attempt`，上限 30 秒
  - 不会重试：用户中止（`context.Canceled`）、4xx 客户端错误、流传输中途失败
  - 通过 `retry` 配置项（`maxRetries`、`baseDelay`、`maxDelay`）灵活调整
  - Agent 将重试事件作为状态更新透出到 TUI 和 print 模式
  - ACP 模式同样接收重试配置

### 🐛 问题修复

- **Anthropic `cache_control` 改为显式启用**
  - 默认关闭 `cache_control`（此前会根据官方 API base URL 自动启用）
  - 需在 provider 配置中显式设置 `cacheControl: true` 才能启用 prompt 缓存
  - ACP provider 创建时显式为 Anthropic 启用 `cache_control`

- **Anthropic Tool Result 分组**
  - 修复连续 `toolResult` 消息未合并为单条 `user` 消息的问题
  - Anthropic API 要求前一轮 `tool_use` 对应的所有 `tool_result` 块在后续内容之前集中出现
  - 工具结果中的图片块现在会在同一消息中追加到所有结果块之后
  
- **Agent 纯工具循环告警顺序**
  - 将无文本输出的工具循环告警改为在 tool result 追加之后再注入
  - 保持 assistant -> toolResult -> warning 的消息顺序，确保 provider 与 session transcript 都合法
  - 告警消息现在也会持久化写入 session 存储

### 📝 文档

- **配置文档全面重写**
  - 补充缺失配置项：`cacheControl`、空闲压缩、完整沙箱字段（`bwrapPath`、`allowedRead`、`allowedWrite`、`deniedPaths`、`passEnv`、`tmpSize`）、`shellPath`、`shellCommandPrefix`、`sessionDir`、`skillsDir`、`theme`、`retry`
  - 记录 shell 命令格式的 `apiKey`（`!cmd`），支持密码管理器集成
  - 修正密钥解析顺序：优先使用配置中的 `apiKey`，其次使用推导的环境变量
  - 更新 macOS 配置路径文档
  - 新增顶层字段参考表及所有默认值
  - 新增各平台沙箱路径与环境变量默认值
  - 改进示例：Claude provider `cacheControl`、空闲压缩、项目级覆盖、自定义沙箱路径

### 🧪 测试

- 新增重试测试，覆盖 `IsRetryable`、`RetryDelay` 和 `FormatRetryMessage`
- 新增 Anthropic provider 测试，覆盖连续 tool result 分组
- 新增回归测试，覆盖 tool result 之后的纯工具循环告警插入位置

---

## v0.1.23

### 🛠 改进

- **DeepSeek Thinking 格式**
  - 新增 `thinkingFormat: "deepseek"`，用于 DeepSeek 推理请求
  - OpenAI 兼容请求现在会发送 `thinking: {type: "enabled"}` 和 `reasoning_effort`
  - Anthropic 兼容请求现在会发送 `thinking: {type: "enabled"}` 和 `output_config.effort`
  - 保留 `thinkingFormat: "xiaomi"` 作为旧的 thinking-only 格式

### 🧪 测试

- 新增 provider 测试，覆盖 OpenAI 与 Anthropic 兼容请求下的 `deepseek` thinking 格式

### 📝 文档

- 更新 `anthropic-api` skill 与配置文档中关于 `thinkingFormat` 选项的说明

---

## v0.1.22

### ✨ 新功能

- **CLI/TUI MCP 自动加载**
  - CLI/TUI 启动时现在会加载全局与项目 `mcp.json`，连接已配置的 MCP 服务器，并在 agent 工具列表冻结前注册 MCP 工具

### 🐛 问题修复

- **Markdown 渲染样式**
  - 将 CLI print 模式和 TUI 的 Markdown 渲染从 Glamour 自动样式检测改为固定 `dark` 样式，提升不同终端中的显示一致性

### 🧪 测试

- 新增 MCP 配置加载测试，覆盖模板占位服务器过滤

### 🛠 改进

- **共享 MCP 运行时**
  - 将 MCP 连接与工具注册从 ACP 私有实现提取为共享运行时，ACP 与普通 CLI/TUI 会话复用同一套逻辑
  - 自动启动加载时会忽略 starter 模板中的占位 MCP 服务器

---

## v0.1.21

### ✨ 新功能

- **Plan/Apply 工作流**
  - 新增内置 `plan` 工具，用结构化任务计划表达 `pending`、`running`、`done` 和 `failed` 步骤状态
  - TUI 现在会展示当前任务计划，并把计划更新记录到对话历史中
  - Print 模式和 ACP 现在也会透出计划更新，支持非交互和编辑器客户端流程

- **Apply 确认**
  - 新增 `approval.confirmBeforeWrite`，用于在 Agent 模式下要求 `write` 和 `edit` 执行前审批
  - 新生成的默认配置会启用写入/编辑确认
  - TUI 审批提示会用字节数摘要写入内容，避免直接展示完整文件内容

- **MCP 配置命令**
  - 新增 `/init_mcp`，支持创建项目/全局 `mcp.json`，并提供 `basic`/`full` 模板及 `--force` 覆盖
  - 新增 `/mcps`，用于列出全局与项目 `mcp.json` 中的 MCP 服务器
  - MCP 配置改为独立 `mcp.json`（不与 `settings.json` 混用）

### 🧪 测试

- 新增 `plan` 工具和 write/edit 审批门控测试覆盖
- 新增基于 HTTP 的 MCP 集成测试，覆盖 tool/resource/prompt 注册与回调链路
- 新增基于 SSE 的 MCP 集成测试，覆盖流通知回调与 message endpoint 请求/响应链路

### 🛠 改进

- **ACP MCP 健壮性增强**
  - 新增 `http` 和 `sse` MCP 传输支持（保留现有 `stdio`）
  - 为 MCP 初始化与工具发现增加超时控制，避免 ACP 会话长时间挂起
  - 为 `tools/list` 增加分页拉取与页数上限保护
  - 新增 MCP `resources/*` 与 `prompts/*` 发现和工具注册
  - 增加 MCP 服务器重名检测与 MCP 工具名去重注册
  - 增加 MCP 入站请求/通知处理（`ping`、progress/logging/cancel 通知）
  - 新增入站 `sampling/createMessage` 到当前 ACP provider/model 的桥接
  - 收紧关闭/错误传播行为

---

## v0.1.20

### ✨ 新功能

- **结构化文件变更报告**
  - `write` 和 `edit` 现在会在工具结果中附带结构化文件 diff 元数据
  - TUI 工具详情中展示完整 unified diff，折叠工具行保留简洁的 `+N -N` 摘要
  - Print 模式现在会为非交互运行输出清晰的文件变更摘要
  - ACP 工具更新会在 raw output 中包含 diff 元数据，方便兼容客户端使用

### 🧪 测试

- 新增 `write` 和 `edit` 结构化 diff 元数据测试覆盖

---

## v0.1.19

### ✨ 新功能

- **TUI 工具详情 Modal**
  - 将 `Ctrl+O` 切换展开替换为可滚动的全屏 modal overlay，展示所有工具调用及结果
  - 支持 PgUp/PgDn、Up/Down、Home/End 导航；Esc/Ctrl+O/q 关闭
  - 工具标题现在显示文件路径；移除了工具参数中的内容截断
  - Write 工具结果在摘要行显示 diff 信息
  - Modal 打开时屏蔽键盘输入，防止误操作

- **Write 工具 Diff 摘要**
  - `write` 工具现在在覆盖文件时基于 LCS 算法计算行级 diff
  - 在工具结果中返回结构化 diff 信息（`+N -N` 及行范围）
  - 对超大文件（>20 万行对）跳过 diff 计算，避免内存压力

### 🛠 改进

- **沙箱后端统一 Shell 参数**
  - 所有沙箱后端（`none`、`mac`、`windows`）现在统一使用 `platform.ShellArgs()` 构造 cmd.exe/PowerShell 参数
  - 修复沙箱模式下 Windows cmd.exe 和 PowerShell 命令执行问题
  - `ShellArgs` 现在在匹配前将 shell 名称转为小写

### 🧪 测试

- 新增 `TestNoneSandboxWrapCommandUsesPlatformShellArgs`，覆盖 cmd.exe 和 PowerShell 参数生成

---

## v0.1.18

### 🐛 问题修复

- **TUI Nil 指针 panic**
  - 修复 `printMessageOnce` 在 `printedMessageIdx` map 未初始化时导致的 nil 指针 panic
  - 添加 nil 检查，确保在消息打印逻辑中安全访问 map

- **工具执行前提交流**
  - 添加 `commitActiveStream()` 方法，用于在工具执行前将流式内容（thinking 和 assistant 消息）刷新到输出
  - 现在在 `EventToolCall` 和 `EventToolApprovalRequest` 处理前正确提交活跃的流
  - 确保在工具运行或请求审批时能看到 thinking 和部分 assistant 响应

### 🧪 测试

- 新增 `TestHandleAgentEventCommitsStreamBeforeApproval` 回归测试，覆盖流提交顺序

---

## v0.1.17

### 🛠 改进

- **TUI 原生滚动历史**
  - 重构 TUI 历史渲染：已完成消息会输出到终端原生 scrollback，而不是固定高度 viewport
  - 移除虚拟滚动条与鼠标捕获方案，鼠标滚轮现在使用终端自身的历史滚动行为
  - 保留实时流式内容、输入框、footer、上下文/缓存状态以及工具输出控制

- **TUI 请求计时器**
  - 响应运行期间显示本次请求耗时
  - 请求完成后在 footer 保留上一次请求耗时

- **事件循环解耦**
  - 新增共享的 agent event 消费辅助逻辑
  - 将 TUI 的 agent event bridge 从主 app 文件拆出，并让 CLI print 模式复用同一套事件消费逻辑

- **Windows 控制台兼容性**
  - 在可用时启用 Windows Virtual Terminal 控制台模式，改善 Windows 10 PowerShell 下的显示兼容性

### 🐛 问题修复

- 修复 TUI 启动时在 Bubble Tea 开始消费消息前打印初始/会话历史导致的卡死问题
- 修复 `go test -race` 发现的 agent 消息历史数据竞争
- 修复 mock provider 在 context 已取消时未稳定返回取消错误的问题

### 🧪 测试

- 全量 `make test` 已通过 race detection
- 新增 TUI 启动历史打印不阻塞的回归测试
- 增强受限环境下依赖本地 HTTP listener 或默认 home 目录会话路径的测试稳定性

---

## v0.1.16

### 🛠 改进

- **通过 ID 或路径打开会话**
  - 新增 `OpenByPathOrID` 函数，支持通过文件路径或会话 ID 打开会话
  - `OpenByID` 现在支持前缀匹配，并具备歧义检测
  - `ContinueRecent` 在创建新会话时立即初始化，确保可直接写入消息

- **会话保存错误处理**
  - `AppendMessage` 和 `AppendCompaction` 现在会向调用方返回错误
  - Agent 循环将会话保存失败作为 `EventError` 上报，不再静默丢弃

- **内嵌工具测试守卫**
  - Makefile `test` 目标现在依赖 `prepare-vendored` 和新增的 `test-vendored` 检查
  - 若当前平台缺少 `rg`/`fd` 二进制文件，测试会提前失败并给出明确提示

### 🧪 测试

- 新增 CLI flag 解析测试，覆盖 root 和 ACP 子命令
- 新增配置合并测试，覆盖项目级覆盖和环境变量
- 新增会话测试，覆盖 `OpenByPathOrID`、前缀歧义、损坏行和父链追踪

---

## v0.1.15

### 🐛 问题修复

- **内嵌搜索工具可用性**
  - 修复 `grep` 和 `find`：当内嵌的 `rg` / `fd` 尚未释放到本地时，会按需准备二进制文件，而不是直接失败
  - 为已释放的内嵌二进制补齐可执行权限，避免复用时出现 `permission denied` 错误

- **Bash 工具结果处理**
  - 修复 bash 工具返回内容，稳定输出 stdout、stderr、工作目录和退出码等结构化信息
  - 将命令非零退出保留为正常工具结果，并通过明确的 `exit_code` 字段表达，而不是混入传输级错误
  - 统一将空 stdout/stderr 渲染为 `(no output)`，便于下游稳定处理

---

## v0.1.14

### 🐛 问题修复

- **继续会话上下文注入（`-c`）**
  - 修复 TUI 状态耦合问题：继续会话时可能只显示历史记录，但后续提问未将历史真正注入模型上下文
  - 将会话历史状态拆分为“UI 展示标记”和“Agent 注入标记”，确保恢复会话后可持续携带上下文
  - 在 agent 重建场景（中止/模式切换/模型切换/技能切换/会话切换）统一重置历史注入状态
  - 补充 `EventStatus` 与 `EventMessageStart` 的 TUI 事件处理，确保状态/警告消息稳定渲染

### 🧪 测试

- 新增回归测试覆盖：
  - UI 历史已加载时的历史注入
  - 继续会话真实启动时序（`Init()` 先加载历史，再处理后续输入）

---

## v0.1.13

### 🐛 问题修复

- **流式事件与工具调用健壮性**
  - 保留 TUI 事件监听器中的 agent 事件，避免流式过程中丢失 done/error/status 处理
  - 为 Anthropic 增加 thinking signature 的流式接收与多轮回放支持，并将 SSE `error` 事件正确上报为流错误
  - 当 OpenAI 兼容 provider 在流式工具调用中省略 ID 时，自动生成回退 ID，并在 agent 循环中增加额外防御性回退

- **沙箱环境继承**
  - 修复 `none` 沙箱执行未继承父进程环境的问题，包括 `$HOME` 等环境变量
  - 明确 bubblewrap 环境变量覆盖逻辑，使实现与实际运行行为一致

### 🛠 改进

- **内嵌工具构建流程**
  - 围绕 `prepare-vendored` 统一构建与发包流程
  - 移除旧的 `vendored-tools` 发布步骤，并废弃过时的提取辅助脚本

- **文档站点布局**
  - 扩大文档首页内容区宽度，提升大屏阅读体验

- **包元数据**
  - 更新 npm 安装器相关包版本

### 📖 文档

- 更新 README 与文档首页，突出更安全的审批处理、统一缓存指标和一致的 provider 调试行为
- 精简仓库内 agent 使用说明 `AGENTS.md`

### 🧪 测试

- 为 bash 工具补充仅 stdout、仅 stderr、无输出、非零退出码等输出场景覆盖
- 为 TUI 增加状态/警告渲染与 done/error 事件透传的回归测试
- 为缺失 ID 的 OpenAI 流式工具调用增加回归测试

---

## v0.1.12

### 🐛 问题修复

- **统一缓存命中率语义**
  - 将缓存命中率计算恢复为基于完整 prompt 输入足迹（`CacheRead / TotalInputTokens()`）
  - 让 CLI print 模式的 token 显示与 TUI 的缓存感知总量保持一致
  - 更新 Anthropic 缓存测试与通用 provider usage 测试，使其与统一定义对齐

- **非交互与 YOLO 流程中的审批安全性**
  - 让 `bashBlacklist` 在审批检查中真正生效，且优先级高于 `bashWhitelist`
  - 在 `yolo` 模式下，命中黑名单的 bash 命令仍然要求审批
  - `--print` 模式遇到本应需要用户确认的命令时，改为直接报错退出，而不是自动批准

### 🛠 改进

- **调试输出一致性**
  - `--debug` 现在会同时启用 provider 级请求/响应调试输出
  - ACP 模式下也采用相同行为

- **跨平台路径处理**
  - 将 `.skills` 路径构造从字符串拼接改为 `filepath.Join(...)`

### 📖 文档

- 更新 CLI 参考文档，说明更严格的 `--print` 行为与 debug 输出行为
- 更新配置文档，说明审批优先级与 `VIBECODING_DEBUG`
- 更新根 README 与文档首页，突出更安全的审批处理、统一缓存指标和 provider 调试行为

### 🧪 测试

- 新增白名单/黑名单及 `yolo` 模式下的审批行为测试
- 新增 print 模式中需审批工具调用的回归测试
- 扩展 cache 相关 provider 测试，覆盖统一后的缓存命中率定义

---

## v0.1.11

### 🛠 改进

- **命令结构重构**
  - 将根命令创建提取为独立函数，提升可测试性
  - 新增命令初始化和配置的单元测试
  - 提高代码模块化和可维护性

### 📖 文档

- **许可证与文档更新**
  - 新增 MIT 许可证文件
  - 新增中文 README（README_zh.md），提升中文用户体验
  - 更新 npm 包版本

---

## v0.1.10

### ✨ 新功能

- **ACP 支持文档**
  - 在 README 中添加 ACP（Agent Client Protocol）支持文档
  - MothX 可作为 ACP stdio Agent 运行，用于编辑器集成
  - 兼容 VS Code、Zed 和 JetBrains IDE（IntelliJ IDEA/WebStorm），通过 ACP 兼容插件接入

### 📖 文档

- 更新主 README.md 添加 ACP 支持特性
- 更新英文 README 添加功能特性部分
- 更新中文 README 添加功能特性部分

---

## v0.1.9

### 🐛 问题修复

- **TUI 延迟渲染协程安全**
  - 修复 `scheduleRender` 从后台协程直接调用 `updateViewportContent` 而未归队到 Bubble Tea UI 协程的问题
  - 新增 `renderRequestMsg` 类型和 `program.Send()` 方法，确保 UI 更新正确归队
  - 新增 `program *tea.Program` 字段和 `SetProgram()` 方法支持延迟 UI 调度

### 🛠 改进

- **TUI 中止时清空输入队列**
  - 手动中止和模式切换时清空输入队列并重置输入状态
  - 防止缓冲按键在中止后继续执行

- **助手消息槽位预留**
  - 新增 `EventTurnStart` 处理，在文本增量到达前预留显示槽位
  - 防止工具输出在流式传输过程中改变助手消息索引
  - 在 `updateViewportContent` 中增加空原始 markdown 检查

- **工具提示片段优化**
  - 为 `read`、`ls`、`grep`、`find` 工具描述添加 "(preferred for ...)" 提示
  - 调整工具注册顺序：只读工具优先注册在 write/edit/bash 之前

### 🧪 测试

- 新增 `TestHandleAgentEventReservesAssistantSlotBeforeTextDelta` 测试
- 新增 `TestAbortClearsQueuedInput` 测试

---

## v0.1.8

### 🐛 问题修复

- **缓存感知的 Token 计算修复**
  - 修复 Anthropic `TotalTokens` 计算未包含 `CacheRead` 和 `CacheWrite` 的问题
  - 为 `Usage` 结构体添加 `PromptTokens()` 和 `TotalInputTokens()` 辅助方法
  - 更新 `CacheInfo()` 使用 `TotalInputTokens()` 作为分母，确保缓存命中率准确
  - 更新 TUI 显示正确的 token 计数（包含缓存 token）

### 🧪 测试

- 添加 `PromptTokens()` 和 `TotalInputTokens()` 辅助方法的综合测试
- 更新 Anthropic provider 测试以验证 `TotalTokens`

---

## v0.1.7

### 🐛 问题修复

- **Anthropic Provider Tool Use 序列化**
  - 修复 `tool_use` 内容块在 tool 无参数时缺少 `input` 字段的问题
  - 将 `Input` 字段从 `map[string]interface{}` 改为 `*map[string]interface{}`，使 `omitempty` 仅检查指针是否为 nil，而非空 map
  - 修复使用小米 MiMo 等 Anthropic 兼容端点时的 API 错误

---

## v0.1.6

### ✨ 新功能

- **会话管理命令**
  - 新增 `/sessions` 命令，用于浏览和管理项目会话
  - 支持列出、切换、清除和删除会话
  - 显示会话详情，包括文件路径和消息数量

### 🐛 问题修复

- **沙箱初始化**
  - 修复沙箱初始化验证和 bwrap 多架构兼容性问题
  - 改进沙箱设置的错误处理

### 📖 文档

- 更新 AGENTS.md 中的当前版本信息
- 格式化 Go 代码以保持一致性

---

## v0.1.5

### ✨ 新功能

- **DeepSeek V4 默认模型**
  - 更新默认模型规格为 DeepSeek V4（Flash 和 Pro）
  - 100 万上下文窗口，最高 38.4 万最大输出 token
- **安装脚本改进**
  - 安装完成后显示配置目录路径

### 🐛 问题修复

- **Windows IME 支持**
  - 修复 Windows 终端的 IME（中日韩输入法）支持
  - 修复 Windows 上的 shell 命令解析
  - 新增配置加载诊断信息，便于排查问题
- **Musl Deb 包**
  - 修复 musl deb 包使用无效 dpkg 架构名的问题

### 🛠 改进

- **配置简化**
  - 移除 `auth.json` 支持 — 所有凭据统一使用 `settings.json`
  - 更简洁的配置路径，单一数据源

### 📖 文档

- 明确说明 OpenAI/Anthropic 兼容 API 服务也受支持
- 从文档和安装脚本中移除所有 `auth.json` 引用
- 新增 Windows `%APPDATA%` 路径的详细示例
- 清晰区分 Windows 与 Linux/macOS 的配置路径

---

## v0.1.4

### ✨ 新功能

- **Linux musl 构建支持**
  - 新增 `make build-linux-musl` 目标，静态链接 musl 二进制文件（amd64 + aarch64）
  - 通过 `dist-tarball` 和 `dist` 目标生成 musl tarball 包
  - 通过 `dist-deb` 目标生成 musl Debian 包（amd64-musl / arm64-musl）
  - npm 包：`vibecoding-installer-linux-musl-x64` 和 `vibecoding-installer-linux-musl-arm64`
  - npm 使用 `libc` 字段实现 musl/glibc 正确解析（npm >=9.4）
  - postinstall.js 自动检测 Linux 上的 musl 与 glibc

---

## v0.1.3

### ✨ 新功能

- **版本规则**
  - 新增版本号管理规则：版本号采用十进制进位（如 v0.1.9 -> v0.2.0）
  - 明确 changelog 编写规则：只在 docs/en/changelog.md 和 docs/zh/changelog.md 中编写
  - 不创建单独的 release notes 文件

---

## v0.1.2

### ✨ 新功能

- **Prompt Cache 优化**
  - 实现了基于 LLM_Agent_Cache.md 策略的提示缓存优化
  - 跨多轮对话缓存系统提示和静态上下文
  - 通过重用缓存 token 减少 API 成本

- **TUI Markdown 语法高亮**
  - TUI 中的助手消息现在支持 markdown 语法高亮
  - 代码块、标题和格式化内容有视觉区分
  - 提升 LLM 响应的可读性

### 🐛 问题修复

- **安全与正确性**
  - 解决了关键的安全、竞态条件和正确性问题
  - 修复了代码库中的高、中严重性正确性问题
  - 移除了死代码，提高了整体代码正确性

- **TUI 稳定性**
  - 修复了在不支持的 stdin 上 `clearStdin` 阻塞导致的 TUI 启动挂起
  - 修复了 ANSI 转义码在前缀检查中导致的 TUI 助手消息渲染损坏

### 🛠 改进

- **代码质量**
  - 修复了代码库中剩余的中等严重性问题
  - 更新了 npm 包版本

---

## v0.1.1

### ✨ 新功能

- **缓存命中率显示**
  - 状态栏现在显示所有轮次的累计缓存命中百分比
  - 缓存命中率 ≥ 50% 时高亮显示，便于快速识别
  - 每轮 token 使用行新增缓存读写数量显示

- **代理兼容性**
  - 支持在 `message_delta` 而非 `message_start` 中发送 usage 字段的代理
  - 支持将 usage 拆分到多个 SSE chunk 的 OpenAI 代理（每个字段取首次出现的值）
  - 修复 print 模式 token 汇总行 `$` 前缺少空格的问题

### 🛠 改进

- **代码质量**
  - 提取 `Usage.CacheInfo()` 消除 3 处重复的缓存显示逻辑
  - npm 包版本号改为 `v` 前缀格式（如 `v0.1.1`）
  - 统一所有 npm package.json 的 JSON 格式

### 🧪 测试

- 新增 37 个单元测试覆盖 `CacheInfo()`、`formatCachePercent()` 和 `renderFooter()` 缓存部分
- 新增 12 个 httptest 集成测试覆盖 Anthropic 和 OpenAI SSE 缓存 token 解析

---

## v0.1.0

### ✨ 新功能

- **小米 MiMo thinking 格式支持**
  - 新增 `thinkingFormat` 配置选项，支持小米 MiMo API 格式
  - OpenAI provider: 小米端点使用 `thinking: {type: "enabled"}` 格式
  - Anthropic provider: 小米端点省略 `budget_tokens`
  - URL 自动检测：未设置 `thinkingFormat` 时自动检测 `xiaomimimo` 端点
  - 调试日志：通过 `VIBECODING_DEBUG` 环境变量启用

### 🛠 改进

- **配置灵活性**
  - `thinkingFormat` 从配置传递到 provider，不再仅依赖 URL 检测
  - Anthropic `budget_tokens` 从必需改为可选（指针类型 + `omitempty`）

---

## v0.0.9

### ✨ 新功能

- **工具图像支持**
  - `read` 工具现在支持读取图像文件（PNG、JPEG、GIF、WebP）
  - 图像以 base64 编码数据和 MIME 类型信息返回
  - LLM 现在可以分析和理解图像内容
  - 支持格式：`.png`、`.jpg`、`.jpeg`、`.gif`、`.webp`

- **富内容工具结果**
  - 新的 `ToolResult` 结构体支持纯文本和富内容块
  - 工具现在可以在单个结果中返回文本 + 图像
  - 新增工厂函数：`NewTextToolResult()` 和 `NewImageToolResult()`

- **模型切换**
  - `/model <id>` 命令允许在交互模式下切换模型
  - `/model` 不带参数显示当前模型和可用选项
  - 切换模型时自动重置 Agent

- **增强的帮助系统**
  - `/help` 命令现在显示详细的命令说明
  - 新增键盘快捷键参考（Tab、Esc、Ctrl+O、PgUp/PgDn）

### 🛠 改进

- **上下文 Token 估算**
  - 修复了同时存在 `Content` 和 `Contents` 时的重复计算问题
  - 图像 token 估算为每张图约 1200 token

- **提供商消息转换**
  - OpenAI：工具结果中的图像作为补充用户消息发送
  - Anthropic：图像作为单独的用户消息与 tool_result 一起发送

### 🧪 测试

- 新增 `TestReadToolImage` 测试用例验证图像读取功能
- 所有工具测试已更新为新的 `ToolResult` 返回类型

---

## v0.0.8

### ✨ 新功能

- **NPM 多架构分包优化**
  - 将 npm 包从单包全平台（~60MB）拆分为 6 个平台独立包（每个 ~10MB）
  - 用户安装时只下载当前平台的二进制文件，体积减少 83%
  - 利用 npm `optionalDependencies` + `os`/`cpu` 字段自动匹配平台
  - 主包 `vibecoding-installer` 仅 ~2KB，通过 `postinstall` 链接正确的平台包

### 🛠 改进

- **构建系统**
  - 新增 `scripts/build-npm-packages.sh` 生成平台独立 npm 包
  - 新增 `make npm-packages`、`make npm-pack`、`make npm-publish-all` 目标
  - `sync-npm-version.sh` 同步更新所有平台包版本

---

## v0.0.7

### ✨ 新功能

- **跨平台沙箱支持**
  - 沙箱现在除 Linux 外还支持 macOS 和 Windows
  - macOS 使用 `sandbox-exec` 进行进程隔离
  - Windows 使用受限进程创建，禁止网络访问
  - 自动选择平台特定的沙箱实现

- **仓库重命名**
  - 模块路径更名为 `github.com/startvibecoding/mothx`
  - 所有导入、文档和脚本已同步更新

### 🛠 改进

- **平台特定进程处理**
  - 将 `SysProcAttr` 配置提取到构建标签文件（`bash_unix.go`、`bash_windows.go`）
  - 后台子进程清理现在在所有平台上正常工作
  - `Setpgid` 仅在 Unix 系统上设置；Windows 使用 `CREATE_NEW_PROCESS_GROUP`

### 📖 文档

- 更新所有 GitHub URL 至新仓库地址
- 新增 v0.0.6 和 v0.0.7 发布说明

---

## v0.0.6

### 🛠 改进

- **Bash 工具可靠性**
  - 修复后台子进程挂起问题
  - 添加 `WaitDelay` 防止 shell 无限等待后台子进程
  - 正确处理 `exec.ErrWaitDelay` 错误

- **NPM 安装**
  - 新增 npm 包，支持通过 `npm install -g vibecoding-installer` 安装
  - `postinstall` 时自动下载二进制文件

### 📖 文档

- 新增 npm 安装说明
- 移除 docs 根目录下冗余的 markdown 文件
- 新增 v0.0.5 更新日志

---

## v0.0.5

### ✨ 新功能

- **非 root 安装**
  - `install.sh` 现在支持无需 root 或 sudo 权限安装
  - 自动检测可写安装目录：优先使用 `/usr/local/bin`，若不可写则回退到 `~/.vibecoding/bin`
  - 移除所有 `sudo` 调用 — 用户级安装不再需要提升权限

- **自动 PATH 配置**
  - 自动检测用户 shell（bash、zsh、fish）并在相应配置文件中配置 PATH
  - 支持 `.bashrc`、`.bash_profile`、`.zshrc`、`.zshenv`、`config.fish` 和 `.profile`
  - 若 PATH 条目已存在则跳过配置（避免重复）
  - Fish shell 使用 `set -gx PATH` 语法；bash/zsh 使用 `export PATH=...`

### 🛠 改进

- **环境变量**
  - `INSTALL_DIR` — 覆盖安装目录（不变）
  - `AUTO_SETUP_PATH=0` — 禁用自动 PATH 配置
  - 更好的权限问题错误提示

- **安装体验**
  - 开始时显示安装目录和 PATH 自动配置状态
  - 更清晰的彩色状态消息输出

### 📖 文档

- 新增 v0.0.5 发布说明

---

## v0.0.4

### ✨ 新功能

- **Agent 模式审批机制**
  - Agent 模式下执行 bash 命令需要用户审批
  - 支持 `bashWhitelist` 配置，白名单中的命令自动批准
  - 支持 `bashBlacklist` 配置，黑名单中的命令始终需要审批
  - TUI 中显示审批提示，用户输入 `y`/`yes` 或 `n`/`no` 响应
  - 审批请求支持 `abort` 取消

- **模式权限矩阵**
  - Plan 模式: 只读工具 (read, grep, find, ls)
  - Agent 模式: 读写自动执行，bash 需审批
  - YOLO 模式: 所有工具自动执行
  - 更新系统提示词，明确每个模式的权限

### 🛠 改进

- **默认审批白名单**
  - 默认白名单: `go`, `make`, `git`, `npm`, `yarn`, `node`, `python`, `pip`
  - 可在 `settings.json` 中自定义

- **模式切换反馈**
  - 切换模式时显示详细权限说明
  - `/mode` 命令显示当前模式的完整权限列表

### 📖 文档

- 新增审批配置章节
- 更新安全文档，说明审批机制
- 新增 v0.0.4 发布说明

---

## v0.0.3

### ✨ 新功能

- **会话历史加载**
  - 继续或打开会话时显示会话信息（文件路径和消息数量）
  - 在 TUI 中加载并显示历史会话消息
  - 将历史消息加载到 Agent 上下文中以保持连续性
  - 中止时重置 Agent 以确保下次请求状态干净

### 🛠 改进

- **构建与分发系统**
  - 重构 Makefile，按平台划分构建和分发目标
  - 新增 `dist-linux`、`dist-darwin`、`dist-windows` 目标
  - 新增 `build-zip.sh` 用于 Windows zip 打包
  - 新增 `checksums` 目标用于发布校验
  - 更新 `build-deb.sh` 和 `build-tarball.sh` 支持全平台

### 📖 文档

- 文档网站右上角新增 GitHub 仓库跳转按钮
- 新增 v0.0.2 更新日志

---

## v0.0.2

### ✨ 新功能

- **一键安装脚本**
  - `install.sh` 适用于 Linux/macOS，自动从 GitHub Releases 下载
  - `install.ps1` 适用于 Windows PowerShell，支持通过 `VIBECODING_INSTALL_DIR` 自定义安装目录
  - 两个脚本均可自动检测平台/架构、校验完整性并配置 PATH

- **文档站重新设计**
  - 采用 Google Material Design 风格重新设计
  - 默认语言改为英文
  - 新增 Hash 路由，方便文档分享（如 `#/en/README`、`#/zh/configuration`）
  - 头部和 README 新增 Logo

- **品牌素材**
  - 新增 `docs/assets/icon.svg`（512×512）用于打包
  - 新增 `docs/assets/mothx.png` 用于 README 和小尺寸显示
  - 简洁专业的石板色调设计

- **构建系统**
  - 新增 `make build-windows` 目标（amd64 + arm64）
  - 新增 `make build-linux` 和 `make build-darwin` 目标
  - 更新 `make build-all` 使用平台专用目标

- **文档**
  - 新增 `docs/en/skills.md` 技能系统文档
  - 更新 README 和快速入门中的安装说明

### 🐛 问题修复

- 将素材移至 `docs/assets/` 以支持 GitHub Pages 部署

---

**完整变更日志**: https://gitee.com/startvibecoding/mothx/compare/v0.1.26...v0.1.27
