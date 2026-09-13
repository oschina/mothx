# 更新日志（当前版本）

本文件仅记录**当前版本**的变更。所有版本的完整历史见 [docs/zh/changelog.md](zh/changelog.md)。

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

- **WebUI：Windows 原生目录选择器不再损坏中文/全角目录名**
  - `/api/select-directory` 在 Windows 上通过 PowerShell `FolderBrowserDialog` 输出所选路径。Windows PowerShell 5.1 重定向 stdout 默认使用 ANSI/OEM 代码页（中文系统为 GBK），Go 侧按 UTF-8 读取会得到乱码字节，中文或全角目录名返回后无法解析。选择器脚本现在在写出结果前强制 `[Console]::OutputEncoding` 为 UTF-8（pwsh 7 重定向时本就默认 UTF-8，两种宿主行为统一）。
  - 选择器输出只再去除末尾换行，不再做 Unicode 感知的 `TrimSpace`：以空格或全角空格（U+3000）开头/结尾的合法目录名不会被静默截断。

- **被取消的运行不再被记为成功**
  - 在等待成员期间、或最后一轮进行中被取消的运行，现在以 `aborted` 原因记为 cancelled，而不再被投影为正常完成；从终态事件推导状态的入口（ACP/Desktop）会显示用户请求的取消结果。
  - 因输出上限被截断、但已通过升级或续写恢复的那一轮，不再被判为 incomplete：截断标志现在只作用于自己那一轮，不会泄漏到下一轮。
  - TUI：已退役的事件流会继续被排空，让被取消的运行总能完成终态记账；被取消的缓存 Agent 会在下次提交前丢弃；因运行提前结束而残留的运行中工具行会被终态化。

- **消息频道：有界投递重试窗口**
  - 持久化投递操作改为在可配置窗口（默认 10 分钟）内重试，而不是在固定尝试次数后放弃。传输级失败会自动重试、由 serve 重连恢复路径重新打开，或由运维通过 ACP `mothx/manage/deliveries/retry` 重开。
  - 永久性失败（平台 4xx、不支持的媒体类型、投影损坏）保持 failed：失败投影返回 `retryable` 标记，重试入口会拒绝非 failed 或不可重开的操作而不是把它们打回 `retry_wait`，持久化层执行同样的 fence。仅因依赖失败而终止的操作会随依赖一同恢复。

- **频道：被显式关闭的 sub-agent 工具不再被重新打开**
  - 把 sub-agent 工具重新指向会话级 manager（它拥有会话邮箱）时，不再复活用户关闭的工具：普通多 Agent 选择保留逐工具开关，绑定主角团的会话仍以完整工具集为权威。

- **并行工具调用按声明顺序上报启动**
  - 并行工具批次现在按模型给出的顺序上报每个调用的启动，参数解析与 worker 调度不再让后面的调用看起来先启动。该顺序只约束启动、不阻塞执行：调用之间仍然并发运行，各自独立等待审批与持久化执行记录，完成顺序不受限制；结果、transcript 顺序与 provider 续写消息维持原有的顺序保证。Responses 后台运行也使用同一套顺序句柄。

### ✅ 测试

- TUI：新增测试断言运行期间提交的输入仅排队而不替换租约持有者，且排队提示词只有在取消流程完成持久化运行终态并释放租约之后才会启动。
- TUI：`/defaultModel` 新增覆盖，断言对话框模型列表与工厂创建的 Provider 列表（`/model` 路径）一致，覆盖部分模型覆盖与仅凭据两类 settings 条目。
- WebUI：新增 Windows 目录选择器回归测试，断言脚本在写出选择前强制 UTF-8 输出编码、默认路径经 UTF-16 环境变量传递，并覆盖以全角空格结尾路径的输出修剪。
- 主角团：覆盖 Runtime 绑定/分叉、命名成员事件、TUI 与 Serve 的“成员终态不直接开 run”守卫、ACP bind/fork 进程路径、Desktop 投影与跨入口 ESM 空闲 gate。
- 频道：全“可选工具”契约测试验证每个可用的持久化工具选择都会出现在解析后的会话 Registry 中。
- 主角团：成员提问 → lead → `subagent_answer` 闭环、已失效提问的拒绝、邮箱归属（会话 lead vs 辅助角色、定时任务、团队绑定的 ESM worker），以及非团队会话在不为成员等待的前提下投递成员通知。
- Agent 循环：成员等待期间取消、以及被后续轮次恢复的截断轮的终态覆盖。
- 频道：sub-agent 工具与 lead 共享同一会话邮箱；部分工具选择不会被重新注册复活。
- 投递：永久失败的重开拒绝，覆盖运维入口与持久化 fence 两层。
- 投递：WebUI 端点覆盖列表、重试成功以及永久失败/进行中/未知操作的拒绝；Desktop 投影覆盖同一判定与双语文案。
- Runtime：孤儿恢复并发用例改用同步屏障证明 worker 重叠，不再依赖时间窗口，因此不会在高负载下抖动。
