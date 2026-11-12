# Native Desktop 功能迁移进度

更新时间：2026-11-12

## 目标与边界

将 `desktop/` 的 ACP 设置与管理能力逐项迁移至纯 Go 的 `native/` 客户端。

- Native 只能作为 ACP 投影：所有运行时、会话、设置、知识库和管理数据经 `mothx acp` 访问。
- 不允许 Native 直接读写 `settings.json`、`serve.json` 或数据库。
- 设置使用 Native 的独立三级视图（分类 / 条目 / 内容），不使用聊天页小弹窗。
- 凭据、token、API key、App Secret 等敏感值仅提交，不从 ACP 响应或客户端状态回显。

## 已完成的基础结构

- Native Settings 已从弹窗改为独立主视图，保留聊天状态，并支持返回聊天。
- 设置导航对齐 Desktop 的分类和条目结构；按 ACP capability 隐藏未公开的管理能力。
- 会话历史是独立视图，展示全会话、搜索、项目/scope 筛选、分页与 `lastRun` 状态投影。
- 历史视图与 Settings 视图互斥；会话/项目弹窗现在也能从侧栏菜单打开。
- Native 侧栏已改为 Desktop 的“项目 / 最近 / 未归类”任务树，每个会话行使用紧凑 `⋯` 菜单，不再在行内铺文字按钮。

## 已实现的 Settings 面板

下列面板已有 Native ACP 状态模型、视图、生命周期失效处理和对应测试文件：

| 面板 | 主要 ACP 方法 / 能力 | Native 文件 |
|---|---|---|
| Application | `mothx/manage/application/get`, `patch` | `settings_model.go`, `settings_view.go`, `app.go` |
| Providers | `manage/settings/*`, `manage/providers/*` | `providers_model.go`, `providers_view.go` |
| MCP | `mothx/manage/mcp/list`, `set`；global + 当前会话 project scope | `mcp_model.go`, `mcp_view.go` |
| Skills | `mothx/manage/skills/list`, `set` | `skills_model.go`, `skills_view.go` |
| Memory | `mothx/manage/memory/get`, `put` | `memory_model.go`, `memory_view.go` |
| Cron | `mothx/manage/cron/*` | `cron_model.go`, `cron_view.go` |
| Channels | `mothx/manage/channels/get`, `patch` + deliveries | `channels_model.go`, `channels_view.go`, `delivery_*` |
| Serve | `mothx/manage/serve/get`, `patch` | `serve_model.go`, `serve_view.go` |
| Knowledge | `mothx/manage/knowledge-bases/*` 及附加能力 | `knowledge_model.go`, `knowledge_view.go` |
| SkillHub | `mothx/manage/skillhub/*` | `skillhub_model.go`, `skillhub_view.go` |
| Experts | `mothx/manage/experts/*` | `experts_model.go`, `experts_view.go` |
| ACP Workers | `mothx/manage/acp/profiles/list`, `patch` | `acp_workers_model.go`, `acp_workers_view.go` |
| Env | `mothx/manage/env/get`, `patch`（值只写不回显） | `env_model.go`, `env_view.go` |
| Stats | `mothx/manage/stats/summary`, `timeseries` | `stats_model.go`, `stats_view.go` |

本地页面也已提供：Appearance（跟随系统/深色/浅色）、Runtime（连接、重启、`mothx/doctor`、ACP stderr、启动二进制解析）、About。

## 本轮补齐的聊天/任务能力

- 侧栏项目树、项目菜单、最近/未归类分组；会话状态点、pin 标记和 `⋯` 菜单复用。
- 统一会话动作：置顶、重命名、移动项目、新建项目并移动、移出项目、设置工作目录、Fork、删除。
- Composer：模式、Provider/模型/思考级别、Expert、沙箱/浏览器/Web 搜索能力、附件与斜杠命令注入。
- 新会话的配置选择只保存在 `draftConfig`，`session/new` 后通过 `session/set_config_option` 统一提交；浏览模型不再创建空会话。
- 工作目录语义：活动会话变更会话 cwd，草稿壳层只变更下一次新会话目录。
- 转录：工具 rawInput、diff 内容、locations、`mothx/session/history` 旧消息分页。
- 决策：permission 完整工具参数、question timeout / `decision_deadline` 截止信息。
- 子代理：子会话事件复用主转录 reducer 渲染，运行中子代理按 2 秒轮询。
- MCP：Settings 内切换全局/当前会话项目 scope，聊天头部提供“项目 MCP”入口。
- Runtime：连接状态/重启、doctor 检查、ACP stderr 日志。
- 使用量展示补充上下文百分比与成本；模式/模型在 Composer 中可见。
- 附件：工作区内 `resource_link`，工作区外按上限内嵌；图片制品可通过 canonical attachment ID 预览，非图片制品可保存。
- 多会话并发：运行中的会话会进入独立 snapshot；切换/新建/发送不再被其他会话的 Run 阻塞，事件继续写入各自缓存。
- Skills / Automation / Library 独立主视图已接入侧栏；复用共享设置面板，生命周期按主视图可见性加载。
- SkillHub 分类树与 SkillHub.cn 官方推荐已接入。
- 首页空态提供场景化快捷提示，点击即可注入 Composer。
- 剪贴板 PNG 图片可直接内嵌为附件。
- 审批/提问改为转录内联卡片，保留已解决状态、完整工具参数和秒级 deadline 刷新。
- i18n：Native 增加 zh/en 语言切换（MyGo SetLocale 持久化），共享 Desktop 字典 + Native 专用补充表；未命中字符串回退中文。

## 本轮补齐的 Desktop 对齐项

- 侧栏 `mothx/session/listAll` 分页：all 首页 + 未归类首页自动加载，项目分支展开时加载第一页，分支/未归类都带 cursor “加载更多”；会话目录由已加载页面合并，51 条以后不再消失。
- i18n 动态状态与错误：`T` 增加动态前缀翻译，`Tf` 支持格式化；转录状态、设置面板 Status/Err、动态错误拼接、Appearance 动态标签均经过翻译。英文设置页/错误态新增回归测试。Provider fallback 行、SkillHub 作者/下载/结果数与详情、Knowledge 来源/停用/无快照状态、技能/定时任务/SkillHub 进行中状态、Composer 内嵌附件标签也全部走 T/Tf，并由 `i18n_nonvacuous_test.go` 填充真实数据状态后断言英文零汉字。
- i18n 收口第二轮：Runtime 二进制来源（Native 设置/仓库 bin/）、页面能力缺口、项目空态、SkillHub/Expert/子代理 fallback 标签、保存/切换/工作树/知识库/Provider/Cron 动态状态、文件选择与保存对话框标题、`localizedError`/`localizedErrorf` 校验错误（Provider/MCP/Cron/Knowledge/Serve/SkillHub/Env/Experts/Channels）全部经过 T/Tf；Composer 能力菜单 fallback 标签与制品可访问标签也走 T。新增非空状态与校验错误英文零汉字回归。
- Home：逐字移植 Desktop 办公/代码/创作预设、快捷卡片、标签和长执行协议；支持自定义 Home Logo、背景图、透明度、模糊、fit、position，并按语言切换预设文案。
- Appearance：主题、语言、Home 预设/Logo/背景偏好写入 `native-preferences.json`（PathUserData），重启保持；语言仍由 MyGo `SetLocale` 持久化。
- StatusBar：底部连接/版本/ACP、context used/size/百分比、cost、prompt-cache 命中率、mode/model/workspace；解析 `usage_update._meta["mothx.dev"]` 的 `usageCacheProjection`。
- 转录：最近 300 条渲染窗口、PlanProgress 右侧栏、内联 status line、结构化错误卡（code/retryable/detail）、用户/助手/工具结果图片、完整转录复制。
- 工具调用产物：`publish_artifact` 完成但 P0 `artifact` 事件缺席时，投影只读 fallback 卡片；无 `artifactId` 时不读取工作树路径，而是给出与 Desktop 一致的制品获取不可用提示；canonical artifact 到达后自动去重。
- 转录导出：活动会话菜单提供“复制完整转录”和“导出转录…”；导出走系统保存对话框，只写出已投影到 Native 的 canonical 转录，不扫描工作区。
- 附件：Ctrl/Cmd+V 直接粘贴剪贴板图片、Composer 缩略图/mime/size、乐观用户消息图片预览。
- Runtime：可持久化选择本地 mothx 二进制（`--mothx-bin` 优先）、主进程诊断日志过滤/清空、About 平台/架构/运行时详情。
- Composer：可搜索模型/Provider 弹层、Provider 过滤、模型能力徽标（reasoning/text/image/audio/video）；Expert 替换非空绑定时通过 `session/fork` 携带 `expertId`。
- 反馈：Toast 层（成功/错误、自动消失、可手动关闭），后台审批到达时提示并在侧栏显示待处理数量。
- Skills / Automation / Library 已改为页面级 IA，不再把 Settings 面板整块铺进主视图：
  - Skills 保留指令卡片网格，新增 SkillHub 市场 hero、市场/分类 chips、在线结果卡片网格、已安装技能卡片，以及“管理本地技能 / 市场配置”入口；设置页仍持有本地开关与市场配置表单。
  - Automation 使用 Desktop 风格的页面 action row、统计卡和 canonical Cron 行列表；创建/编辑/删除对话框在页面级也能渲染，不再只在 Settings 内生效。
  - Library 为 Desktop 风格的只读知识库状态行，显示启用状态、索引进度与快照统计；“管理知识库”跳转 Settings 完成创建/扫描/删除/MCP 接入。
- Worktree / Router / Subagent 弹层补充 Desktop 风格的状态徽标、成员/工作器计数与只读标识。
- 英文设置页与主页面收口：设置分类/条目、Provider 编辑器、Channels、Memory、Stats、Serve、Runtime、Skills/SkillHub/Knowledge 的空态、动态状态、占位符与按钮统一经过 T/Tf；Home 场景副标题/描述不再显示 `preset.*` 原始键；断连时侧栏不再永久停留在 “Loading tasks…”。
- 新增 `i18n_scan_test.go`：在受控 ACP 状态下逐页渲染全部 Settings 分类/条目与 Skills/Automation/Library/Home，英文模式出现任何汉字即失败。
- `i18n_nonvacuous_test.go` 继续覆盖页面扫描触达不到的动态状态：转录分页按钮（加载中/非加载中）、doctor 检查缺省标题、Settings 编辑器与删除确认（Cron/MCP/Knowledge/Provider/Experts）、填充后的转录/状态行/Toast、Home 三个场景、侧栏/Worktree/Router 弹层；这些用例均在恢复原始中文标签时失败，证明不是空测。

## 人工/真实运行时验收记录

2026-11-12 使用真实 `mothx acp` 子进程与 X11 截图逐页检查：

- Home、Skills、Automation、Library；Appearance、Application、Provider、Knowledge、Skills、SkillHub、Experts、ACP workers、Env、Channels、MCP、Memory、Serve、Runtime、Stats、About。
- 覆盖英文模式、1180×760 与 900×600 窄窗口、空态、ACP 启动失败态；Channels/Provider 截图确认凭据只显示配置布尔值/掩码，不回显明文。
- 截图检查发现并修复：Home `preset.*` 原始键、设置分类/条目未翻译、Provider 编辑区标题挤压、Channels 占位符/保存按钮未翻译、Memory 动态路径与更新时间、Stats 窗口/趋势文案、SkillHub 无会话错误态、Sidebar 断连后永久加载等。
- 跨 tab 请求失效由各面板既有序列守卫单测覆盖；本轮未重新做真实进程中的手工跨点击验收。

## 本轮真实运行时端到端验收

2026-11-12 使用真实 Provider（默认 provider/model）跑通并修复：

- `mothx acp` 协议层：发送消息 → `bash` 工具调用 → 审批（`session/request_permission`，自动选择 `allow-once`）→ 提问（`mothx/requestQuestion`，选择 `Red`）→ 图片输入（模型正确回答 Red）→ `publish_artifact` 发布并通过 `mothx/attachment/fetch` 取回 `artifact-ok`。事件包含 `run_status/running/turn_started/turn_finished/terminal:completed`、`usage_update`、工具卡片与 artifact。
- Native 真实 UI（X11 + 真实 `mothx acp`）：从 Home 发送提示词，观察到 `bash` 工具卡片、转录、内联 `Run completed` 状态行、底部 `ctx used/size (%) · cache %`（例如 `ctx 13.6k/1.0m (1%) · cache 49%`）与 usage 头部。
- 真实审批卡：Agent 模式下工具运行前出现 transcript 内联审批卡；按钮使用 Runtime 名称 `Allow once` / `Reject`，点击后工具执行，结果为 `NATIVE_APPROVE_OK`。
- 本轮发现并修复的真实 UI 问题：
  - Home 三个场景标签被纵向堆叠，已改为桌面同款横向一行。
  - StatusBar 在 cache 存在时重复渲染一次 context 投影（短菜单/缓存）并显示 `cache0%`，已改为单一投影、命中率 >0 时才追加且有空格。
  - Composer 底部短菜单（例如只有 Browser 的 Capabilities）默认向下展开后落在 hint/status bar 后面，点击像无响应；所有 composer popover 固定向上展开，并新增无头布局回归。
  - 英文模式动态文案继续收口：会话/项目菜单、附件菜单、模型/专家菜单、审批选项与截止倒计时、审批结果、Runtime 默认会话标题、History 未命名会话、Worktree/Router/Subagent 弹层。
  - 连接初始化期间立即打开 History 会永久缓存 “sessionListAll not advertised” 空态；现在先显示加载，initialize 完成后自动重载。

本轮真实 UI 截图覆盖 Home（英/中）、加载后的真实转录、审批卡、Capabilities/附件/专家菜单、History、Worktree、Router、Subagent，以及 1920×1097 与窄窗口。测试会话与临时目录均在验收后清理。

## 尚未完成

1. **P1 逐像素视觉对照**：Worktree/Router/Subagent 与主要页面已做真实截图与交互检查，状态徽标、计数、空态与英文文案正常；与 Desktop 的逐像素排版对照仍建议人工验收（非功能阻塞）。

## 验证

```sh
cd native
gofmt -l .
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go tool mygo vet .
make build
```

最后验证：2026-11-12，上述检查全部通过（含 `go test -race`、`go vet`、`go tool mygo vet`、`make build`）。新增回归覆盖：publish_artifact fallback 去重、fallback 不可读路径、完整转录导出/文件名、活动会话菜单导出入口、英文 Settings/主页面零汉字扫描、页面级对话框渲染、Home 预设元数据、断连侧栏状态；本轮新增动态菜单/审批/截止时间/默认标题/History 空态/Worktree 弹层英文扫描、Home 标签横向布局、StatusBar 单一 context 投影、Composer 菜单向上展开、History 初始化等待能力。

## 工作树提示

`native/` 当前仍有未提交修改和新增文件（包括本轮与上一轮迁移）。提交前应逐项审查，不要把 `build/`、本地二进制或临时截图纳入版本控制。
