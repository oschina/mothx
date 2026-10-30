# 更新日志（当前版本）

本文件仅记录**当前版本**的变更。所有版本的完整历史见 [docs/zh/changelog.md](zh/changelog.md)。

## v1.3.107

### ✨ 新功能

- **Serve Web UI 新增动态多会话工作台。** `/chat` 现支持会话标签、最多 4 个窗格分屏（菜单/拖拽）、每个 pane 独立的 composer 与模型/草稿状态、布局持久化和移动端单窗格投影。应用级唯一的 `session-stream` 调度器是 `/ws/runs` 的唯一消费者并接管 SSE 回退，因此关闭一个 pane 不会中断其他后台会话的流。各 pane 直接渲染 canonical `sessionRunStates`，溢出的标签可通过“全部标签”菜单访问；审批与提问在所属 pane 的浮层中完成，后台待处理只显示标签与全局 attention 徽标，不会抢占焦点；超过 4 个窗格或标签上限的布局操作会被拒绝并提示原因。旧的单会话回退及其 `?workbench=0`/设置开关已移除，`/chat` 始终是工作台；只读 Plan dock 从焦点会话的最新 plan 消息派生，不新增存储、工具或后端端点。composer 文本在切换标签时保留；带 providerRef 的附件可跨刷新恢复，仅带 dataUrl 的附件在刷新后会提示重新选择。标签条内拖拽现在始终按同窗格重排处理，不再被误判为边缘分屏；第二个浏览器标签页会恢复共享的最近布局，并按 last-write-wins 保持可恢复。每个会话的滚动位置在切换标签后保留；关闭仍在运行的会话标签时会给出一次后台继续提示，而不是静默丢弃。 工作台标签条与 icon-only 工具条已收紧并适配明暗主题；composer 对齐 Desktop：折叠的模型+模式入口可展开 plan/agent/yolo/os 模式、Work/Code 显示模式与 provider/model/主角团选择，会话目录移到聊天头部。

- **Serve 新增带认证的交互式终端。** `features.terminal` 通过 `/api/terminals` 与 `/ws/terminals/` 暴露由 `internal/terminal`（Unix 使用 creack/pty，Windows 使用 ConPTY）驱动的 xterm.js PTY 面板。终端绑定焦点会话的 cwd，继承 serve 进程环境，支持 resize/replay/takeover，并按“回环或已认证监听”策略授权；它不创建 Run、不写 transcript、不占 Runtime lease，也不在 sandbox 内运行。Linux 上 shell 还带有 `Pdeathsig`，不会在 serve 退出后残留；Windows 则提供仅在该平台运行的 ConPTY start/input/resize/exit 往返测试作为实机门禁。 终端配置（含显式 `enabled: false`、shell、资源上限与 allowedOrigins）在 `serve.json` 保存/重载后完整保留。状态栏会显示服务端输出反压，不再把它误判为断线；serve 只记录终端创建/退出/终止的生命周期元数据，不记录输入或输出。

- **源盾已成为默认 SkillHub 市场。** MothX 新增源盾原生客户端，支持搜索、详情、文件、安全审计与下载，并在市场顺序中优先于 SkillHub.cn 与 ClawHub.ai，同时迁移旧的 `skillhub.cn` 默认值。该市场通过 ACP、Serve、TUI、Web UI 与 Desktop 统一投影；Web UI 与 Desktop 还获得新的市场布局与右侧详情抽屉。下载时使用 `X-Client-Source: MothX` 标识 MothX，带 `X-Skill-SHA256` 的归档会在解压前校验。安全审计中的疑似结果改用琥珀色警告样式，与确认风险保持区分。

- **新增 `mothx maintenance` 检查与压缩会话数据库。** `status` 报告转录/工具记录计数、数据库大小与可回收空闲页；`vacuum` 执行 SQLite VACUUM 并报告结果，`--dry-run` 可预览。该命令不会删除消息，也不会按保留期清理工具执行记录；定时维护任务仍是唯一会限制终态工具记录并回填搜索索引的路径。

- **新增 Gitee/Moark 模型：`kimi-k3.1-pro` 与 `kimi-k3.1-pro-highspeed`**
  - 为 `gitee` 与 `moark` 新增 `kimi-k3.1-pro` 与 `kimi-k3.1-pro-highspeed`：默认开启思考，使用 Kimi 的 `low`/`high`/`max` 推理强度档位（MothX 的 `minimal`/`low` 映射为 `low`，`medium`/`high` 映射为 `high`，`xhigh`/`max` 映射为 `max`），支持 1M 上下文与文本+图片输入。两者均通过 `kimi` 思考格式使用经过验证的 `reasoning_effort` 参数。

- **Gitee/Moark 新增模型：`MiniMax-M3.1-Flash`**
  - 为 `gitee` 与 `moark` 新增 `MiniMax-M3.1-Flash`：通过标准 `reasoning_effort` 参数默认开启思考，支持 1M 上下文与文本+图片+视频输入；默认不发送 max_tokens。

### 💥 行为变更

- **Gitee/Moark 模型列表已重新排序以便查找。** `gitee` 与 `moark` 预设现在优先列出 `deepseek-v4.1-flash-1017`、`deepseek-v4.1-flash`，随后是 `qwen3.8-flash`、`qwen3.8-max`、`glm-5.3-flash`、`glm-5.3`、`MiniMax-M3.1-Flash`、`step-3.7-flash` 与 `auto`，其余模型按自然字母顺序排列。由于预设顺序同时是未配置默认模型时的回退顺序，未显式配置的 Gitee/Moark 会话现在会默认使用 `deepseek-v4.1-flash-1017`。

- **Serve 会话列表默认有界。** 未指定 `limit` 的 `/api/sessions` 现在只返回前 100 个会话，并附带 `total`、`limit`、`offset`、`hasMore`；可用 `limit`/`offset` 继续翻页。`scope=active` 与 `/api/sessions/active` 改为只投影内存会话池，不再加载全部历史；会话级 MCP 配置也改为按 ID 单查工作目录。

- **会话存储维护已纳入定时任务，且不会删除转录消息。** MothX 现在会投影每日一次的 `mothx-maintenance:session-storage` 任务，永久保留所有 `entries`/`sub_entries` 转录行。该任务只可能清理早于 `maintenance.toolExecutionRetentionDays` 的已完成/已放弃 `tool_execution_records`（默认 `0`，永久保留），始终保留 running/interrupted/retry-requested 记录以免不确定的工具副作用失去围栏，每次运行 `PRAGMA optimize`，并可在开启 `maintenance.vacuumSessionStorage` 时执行 `VACUUM`。

- **SQLite 启动完整性检查现在可显式关闭。** MothX 默认仍会在使用会话库前执行 `PRAGMA quick_check`。超大数据库可用 `MOTHX_SQLITE_QUICK_CHECK=off` 跳过这项固定开销；`mothx doctor` 仍会执行按需的只读完整性检查，并报告 `sessions.integrity`。

- **非托管网页搜索改用 DeepSeek Search/Fetch。** 原先通过 `webSearch` 的 provider/type/model 配置注册的 provider 搜索工具已被替换为 DeepSeek 官方 `POST /v1/tools/search` 与 `POST /v1/tools/fetch` 支撑的本地 `web_search`、`web_fetch`。OpenAI Responses 的 provider 原生托管搜索保持不变；Anthropic Messages 在 `webSearch` 启用时使用其 provider 原生托管搜索。两者都优先于本地 `web_search`。`webSearch` 现在配置 `provider`、`baseUrl` 与 `token`（未设置 token 时回退到 provider API key）；两个工具均为只读，可在 plan 模式使用。

- **新增腾讯元宝搜索后端。** `webSearch.backend` 现在可选择 `deepseek`（官方 Search/Fetch API）或 `yuanbao`（腾讯元宝 SearchPro，`POST /SearchPro`）。元宝后端发送 `{"Query": ...}`，把返回的 `Pages` 归一化为标题、URL、站点、时间、分数与正文摘要，并使用 `webSearch.token` 或 `${TENCENTCLOUD_WSA_APIKEY}` 鉴权；元宝目前只提供搜索，因此 `web_fetch` 仅在 DeepSeek 后端可用。

### ⚡ 性能

- **打开与列出会话不再重扫整库。** 当前数据库只读取一次迁移账本，打开时不再开启写事务或持有写锁；每次打开复用 EventStore 检查结果；执行事实、列表元数据与消息计数改为批量读取；级联、导出与统计投影不再逐个打开会话；搜索改为一次只读扫描，同时保持 compaction 与 content override 的语义。

- **用户背景图模式降低大面积毛玻璃成本。** 背景图生效时，应用表面遮罩、控制/标题栏、输入框与首页快捷卡使用更小的模糊半径，在保持半透明可读性的同时降低持续的 GPU 合成成本。

- **重复搜索复用未变化的 EventStore 投影。** 按物理日志指纹索引、带字节上限的缓存避免每次搜索都重放同一会话；追加或分段轮转会使缓存失效。

- **重复搜索直接复用已算好的结果页。** 查询/分页结果缓存位于候选扫描之前，因此重复的 Desktop/WebUI 历史查询不再执行列表查询，也不重放任何会话内容。缓存条目按持久化的全库写入版本失效：SQLite 触发器会在会话行、legacy payload、EventStore 投影与项目归属写入时递增版本；追加、标题/绑定/工作目录变更、删除或另一进程的写入都会使结果页失效，而不会返回过期内容。

- **会话列表不再在启动时扫描 EventStore。** 首条用户预览、最新标题与绑定元数据现在持久化到小型 SQLite 展示投影，随每个提交批次更新、随分叉复制、随事件投影重建，并由维护任务回填。列表与绑定读取优先使用投影行，缺失时仍回退到 canonical EventStore 扫描。

- **Desktop 设置页不再因后台流式 chunk 整体重渲染。** 16 个设置面板现在只订阅自己展示的连接/偏好字段，不再订阅整个应用状态。

- **会话内容搜索改为先通过派生 FTS5 索引缩小范围。** 搜索先向 trigram 全文索引查询候选会话，再只对候选做 canonical 重放校验；最终命中仍由重放决定，因此 compaction 丢弃的内容与 content override 的替换语义完全不变。索引与每次写入在同一事务内落库，随事件投影重建、随分叉复制，并在旧会话 attach 时回填；短查询、索引异常与尚未建索引的会话自动退回原全量扫描，搜索本身仍然只读。每日会话存储维护还会按最新优先回填最多 512 个尚未建索引的会话，因此历史会话不需要逐个打开也能被搜索到。

- **Desktop 流式输出与长列表滚动不再重渲染整个外壳。** 渲染进程序列化状态通知,每帧最多提交一次;转录条目改为不可变替换,未变化的 Markdown/工具卡直接跳过渲染;侧边栏、状态栏、输入框与计划面板只订阅自己展示的状态切片;视口外的转录块可跳过布局与绘制;聊天自动跟随滚动与绘制 chunk 的提交合并。

- **Desktop 首次任务列表不再逐个串行扫描会话。** ACP 列表行改用只做结构校验的 EventStore 投影,仅解析首条用户预览与最新标题/绑定,并用有界 worker 池预取当前页;真正打开会话时仍会校验全部 batch 摘要并 fail-closed。

- **Electron 不再强制所有 Linux 打包桌面走软件渲染。** 存在 `DISPLAY`/`WAYLAND_DISPLAY` 时保留 GPU 合成(可用 `MOTHX_DESKTOP_DISABLE_GPU` 显式关闭),仅当 `/dev/shm` 缺失、不可写或小于 128MB 时才启用 `/tmp` 回退。ACP 事件也会按帧合并为一次 IPC 投递,诊断日志仅在日志面板打开时才跨 IPC 推送。

- **Desktop 不再在悬停、切换主题或滚动露出内容时启动成片的过渡/动画。** Tailwind `duration-*` 不再和 `animate-in` 同时出现(此前任意悬停都会让会话行过渡所有继承属性);转录卡片不再在 `content-visibility` 滚动露出时重播入场动画;侧边栏滚动条保持恒定 8px 宽度;切换主题时会临时禁用继承颜色的过渡。

- **长时间运行的 Desktop 窗口保持有界。** 实时转录只渲染末尾 300 条(可在本地继续展开),status/retry/compaction 通知就地更新而不再累积,会话/搜索/子代理/Run 状态缓存都有上限,进度轮询不会重叠,转录查找改用索引避免每个 chunk 全表扫描,超大工具输出在展示副本中截断,`desktop.log` 在启动间按 8MB 轮转。

### 🐛 问题修复

- **交互终端生命周期无数据竞争，会跟随焦点，并持续显示安全边界。** PTY 读取/等待循环现在在创建时直接接收句柄，结束路径在锁内清空终端句柄，既消除了数据竞争，也消除了短命 shell 在启动期间退出时可能触发的空指针窗口；列出/附加终端时通过加锁的 `Size()` 读取尺寸，而不是访问未同步字段。切换工作台焦点会话时，现在会释放旧会话的 shell 并为新 cwd 创建终端，不再留下绑定到上一个会话的旧终端；终端 id 失效（如 serve 重启）后会停止重试，由“重新连接”按钮创建新 shell。终端状态栏现在持续标明 shell 不受沙箱保护、继承 serve 进程用户与环境变量，并展示 `/api/status` 上报的占位认证 token 警告；浏览器冒烟测试对提示与焦点重绑定均有断言。关闭活动 shell 时现在会在进程退出后立即返回，仅在宽限期后升级为 SIGKILL，不再每次都等待 2 秒；浏览器 cookie 会话不再收到长期 terminal token；被拒绝的配置更新在回滚前不会先应用终端限制或杀掉 shell；Windows ConPTY 包装现在保证句柄只关闭一次，并把 shell 加入 kill-on-close Job Object，终端关闭后遗留的后台子进程不会继续运行。

- **交互终端的恢复、焦点与安全提示已补齐。** 终端 dock 会始终提示它与可能正在运行的 Agent 共享焦点会话 cwd，并在 canonical execution 快照为本地或另一进程执行时升级警告。页面刷新会重新 attach 该标签页记录的 detached shell，而不是泄漏旧终端再新建一个；shell 已退出或终端 id 失效后，“重新连接”会创建新终端。xterm 样式已加载，`@xterm/xterm`/`@xterm/addon-fit` 改为精确 pin；PTY read loop 在发布 exit 帧前等待 `cmd.Wait`，非零退出码不再被误报为 0。工作台新增 1000 步 property test，并据此修复 Draft trajectory 模式持久化、删除 group/应用预设后的焦点修复、分隔权重精确夹紧、分页会话标签对账、命令建议重复 DOM id 与移动端右键菜单目标。

- **在 `/chat` 之外创建的新任务不会再因工作台重新挂载而丢失。** 工作台 hydration 改为应用启动时的一次性步骤，不再在懒加载的 Chat 视图每次挂载时重新读取 `localStorage`。此前从 Sidebar/Sessions 新建 Draft 后，若 300ms 持久化防抖尚未触发就切回 `/chat`，旧布局会覆盖内存中的新任务；现在应用启动后内存状态即权威，新开的页面仍会继承共享的最近布局。浏览器与单测均覆盖重新挂载和延迟 hydration 路径。

- **卡住检测不再误判仅有推理输出的进展，并保留终止原因。** 产生推理输出的轮次现在也算进展，因此持续调用工具、但没有面向用户文本的推理模型不会再被判为卡住；既无文本也无推理的连续轮次仍会被静默循环守卫终止。`ErrAgentStuck` 现在携带静默轮次数，并通过 Run 结果与错误事件投影，Runtime `ErrorInfo` 与各适配器都可以说明循环守卫为何终止。

- **部分应用过的会话列表迁移现在会自愈。** 有些数据库在 Run 索引被加入 v49 迁移体之前就记录了 schema 版本 49，此后已记录版本会被直接跳过，`idx_session_runs_session_status_started` 永远缺失。版本 50 用 `CREATE INDEX IF NOT EXISTS` 重新声明该索引，这些数据库下次启动时收敛，新库保持不变。

- **后台 Responses 任务在挂接期间收到停止时不再记为失败。** 并发 WAL 读可能让停止操作在后台 coordinator 挂接 Agent 之前把 Run 标为 cancelling。此前该分支会落入默认 `failed` 终态，并可能遗留 provider 侧任务；现在保留 `cancelled`，并显式取消远端 Responses 任务。
