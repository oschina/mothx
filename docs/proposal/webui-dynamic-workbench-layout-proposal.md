# Serve WebUI 动态工作台布局方案

> Date: 2026-10-28 · Last revised: 2026-10-30 · Status: Implemented (W0–W4, legacy-path cleanup, and the optional W3 Plan dock implemented 2026-10-29; 2026-10-30 page-polish commit verified against the same acceptance suite; touch/drag feel accepted 2026-10-29, 4-pane long-stream acceptance outstanding) · Owner: Serve WebUI (Svelte)
> Related: `AGENTS.md`, `docs/proposal/webui-multi-session-streaming-proposal.md`, `docs/proposal/webui-project-session-management-proposal.md`, `docs/proposal/serve-pty-terminal-proposal.md`
> Reference implementation: `/home/free/src/thinkrail`（`apps/web/src/shell/layout/`、`apps/web/src/shell/layoutState/`、`apps/web/src/shell/SPEC.md`）
> 目标代码：`ui/`（Svelte 5 + Vite，构建后嵌入 Go binary，由 `mothx serve` 提供）

## 0. 决策摘要

本方案只覆盖 **MothX serve WebUI**（`ui/`，Svelte 5），引入前端本地的动态工作台：`/chat` 从“一次只渲染一个 `Chat.svelte`”升级为“**会话标签页 + 桌面最多 4 个可调整大小的分屏**”，布局可记忆、可恢复，并支持键盘操作。**桌面分屏 + 移动端单 pane 标签条；不为 PC-only 牺牲手机聊天。**

核心决策：

- **只改 `ui/`，不改 Go/serve 后端。** 现有 `/ws/runs` 已经订阅 `sessions` 列表中所有会话，`/api/sessions/:id/messages`、`/api/sessions/:id/stream`、审批/提问接口都按 session 工作；多 pane 只是同一条数据流的多个渲染投影。**不新增 HTTP/WS 端点，不改 `settings.json`/`serve.json`。**
- **复用 `session-runs.js` + `session-view.js`，不新建第二套会话状态。** WebUI 已经按 `sessionId` 保存后台运行状态，并有纯 reducer（`viewFromSessionState`、`applySessionViewReducer` 等）。本方案的关键不是“再造投影”，而是把 `Chat.svelte` 里“只有当前会话能渲染”的壳拆成可复用的 `ChatPane`/`SessionSnapshot`，并让**一个**模块消费 `$runEvents`。
- **工作台只作用于 `/chat`。** Sidebar 保持现状（固定栏目；现有移动端抽屉代码保持原样，但不属于本方案）；Sessions/Skills/Knowledge/Stats/Cron/Settings 仍是整页视图。Tab 代表 session 或 draft，不把设置页做成可停靠面板。
- **v1 目标是所有 pane 都可交互：** `ChatPane`（带 composer/审批/轨迹）。`SessionSnapshot` 只作为 W0 延期时的实验降级形态，不进入 v1 验收/发布；因为 `Chat.svelte` 184KB、86 处 `$currentSession`，W0 必须分阶段合入并用单会话快照守住行为。
- **v1 只有 session/draft tab，不把 tty / todo / spec 做成可停靠面板。** 现有 WebUI 没有 PTY terminal API，也没有 MothX 的 spec-graph 工具；`plan`（TODO/计划）与 `trajectory` 的数据已存在，可作为 W3 的只读工具面板；真正的 interactive tty、spec 面板需要新的后端/工具契约。**如果要做 tty，Go 后端的包归属、HTTP/WS 协议、鉴权/沙箱/资源上限见 `docs/proposal/serve-pty-terminal-proposal.md`。**
- **纯 JS 模型 + Svelte 渲染器。** 不引入 React 生态（`react-resizable-panels`、`@dnd-kit/core` 不适用于 Svelte）；分屏用 CSS flex + 自写 pointer splitter，拖拽用原生 Pointer Events（鼠标/触控板）。不新增运行时依赖。
- **布局持久化用一份共享 `localStorage` 文档。** WebUI 没有 Electron main 进程；同一 origin 只保留一份 `mothx.workbench.v1`，刷新、重开浏览器、新 tab 都恢复最近布局。多 tab 采用 last-write-wins，不做 Web Locks/surface 抢锁，也不做实时镜像同步；布局不写服务端。
- **`currentSession` 从“可写全局源”改为“工作台焦点投影”。** Sidebar/Sessions/Experts 的“打开会话”统一走 `workbench.openSession(id)`；工作台更新焦点后再同步 `currentSession` 和 hash 路由，避免两套选中状态互相打架。
- **响应式，而不是 PC-only。** 桌面按 1024px+ 工作，提供最多 4 pane 分屏；`isMobile`（≤900px）下渲染单 pane + 标签条，不做分屏/拖拽，保留现有 Sidebar 抽屉与聊天输入体验。移动端交互使用 Svelte 条件渲染，CSS 媒体查询只负责布局，遵循 AGENTS.md。
- **每个 pane 的模型、provider、composer 草稿独立。** 模型选择不能继续做全局单值，否则两个 pane 会互相改模型/发送参数；草稿按 sessionId 保存在本地，切 tab 不丢。
- **审批不埋。** 每个 pane 内联显示自己的 pending approval/question；同时提供全局 attention 徽标（标签角标 + 顶栏入口），一键定位到对应 pane/tab，但不新增阻塞确认。
- **只回退布局，不保留旧路径。** 灰度期曾用 `?workbench=1` / 设置开关 opt-in；GA 后默认开启，并在 2026-10-29 的清理中移除了 `?workbench=0`、设置关闭项和单 pane 回退。**事件层始终不回退**：`session-stream.js` 仍是唯一 `$runEvents` 消费者；需要回退时只重置布局。

### 0.1 修订原则（2026-10-29）

优先级：**易用性 > 安全/隐私摩擦**，但不得越过 `AGENTS.md` 的硬边界。

**易用性优先的具体取舍：**

- 工作台 GA 后默认开启；旧 `?workbench=0` 与设置关闭项已在 2026-10-29 清理中移除，`/chat` 只有 Workbench 投影。
- 布局是“我的工作区”：同一 origin 共享一份 last-layout，刷新、重启浏览器、新 tab 都恢复最近布局；不做 per-surface 隔离与 Web Locks 抢锁。
- 移动端保留 Chat 可用性：`isMobile` 下渲染单 pane + 标签条，不做分屏/拖拽；不为“PC-only”牺牲手机聊天。
- pane 内直接审批/提问；后台 tab 的 pending decision 通过标签/标题徽标与全局 attention 入口暴露，不新增阻塞确认。
- 关闭运行中的标签不弹阻塞确认，按“后台继续”处理并给一次非阻塞提示。

**不可交换的硬边界（AGENTS.md）：**

- 只改 `ui/`，不新增 Go 端点、不改 `/ws/runs`、不改 session/run/decision 语义；不引入第二套会话投影或输入路径。
- 单一事件消费者（`session-stream.js`）与单一 `sessionRunStates` 源；`currentSession` 只是焦点投影。
- 布局只存表现状态（session id/draft id + 几何 + 未创建会话的本地选择），不写消息/附件/密钥；不把布局写进 `settings.json`/`serve.json`/数据库。
- 移动端交互用 Svelte 条件渲染（`isMobile`），CSS 媒体查询只做布局。
- 安全底线不因“易用性”让步：不泄露密钥/凭据、不绕过认证/同源、不把未认证 shell 暴露到公网；这些是 bug，不是 UX 摩擦。

### 0.2 与 AGENTS.md 的对应关系

| AGENTS.md 约束 | 本方案落点 |
| --- | --- |
| One core / one front-end-neutral Runtime；adapter 只做投影 | 只改 `ui/`；不新增 Go 端点、不改 session/run/decision 语义；不做第二套会话状态或输入路径 |
| One event semantic model | `session-stream.js` 是唯一 `$runEvents` 消费者；`session-view.js` 继续是唯一归约层；ChatPane/SSE/WS 只做渲染投影 |
| One source of truth | `sessionRunStates` 是 per-session 视图源；`currentSession` 只是焦点投影；布局 store 只存表现状态 |
| One input/content path | ChatPane 通过 `session-actions.js` 调现有 API；不构造 `provider.Message`/图片内容，不 fork prompt 组装 |
| Policy, not forks | 桌面/移动只做同一 Workbench 的两种投影；不做“移动版第二套 Chat”；Lite 不进入 v1，仅另立实验分支 |
| One database direction | 不碰 `internal/db`/`internal/dao`；不把布局写进数据库或 `settings.json`/`serve.json` |
| Web UI mobile 约定 | `isMobile` 条件渲染负责移动交互，CSS 媒体查询只负责布局 |
| 双语与门禁 | W4 同步 `docs/{en,zh}/serve.md` 与 changelog；`npm test`/`npm run build`/e2e 作为门禁 |

## 0.3 实施进度（2026-10-30）

已完成：

- W0 事件层：`ui/src/lib/session-stream.js` 成为 App 级唯一的 `/ws/runs` 消费者（`App.startApp()` 启动、`onDestroy` 停止），并接管 SSE fallback、observer 生命周期、done 后会话/统计刷新；`ChatPane` 不再 import `runEvents`/`readSSE`，不再在 `onDestroy` abort 任何 session observer。边界由 `ui/src/lib/session-stream-boundary.test.js` 守卫。
- W0 后台运行 owner：responses-run 重连/轮询从 pane 移到 `session-stream.js`，按 `sessionId` 注册；关闭 pane 不会停止后台 responses 运行恢复，单测覆盖无 mounted pane 的轮询。
- W0 per-session 状态：`sessionRunStates` 增加 `loading/status/loadedAt/earliestSeq/hasMore/effects/modelOverride/providerOverride`；`ChatPane` 从 `sessionStateStore(sessionId)` 渲染，动作写回同一 store；`session-store.js`、`composer-drafts.js`、`session-prefs.js` 已接入（文本草稿、模型/provider、历史窗口）。
- W0 组件：`views/Chat.svelte` 已拆为薄 controller；`components/chat/ChatPane.svelte` 接收 `sessionId/active/viewMode`，支持 per-pane 模型、草稿、审批、轨迹和显式 session actions；`DraftPane.svelte` 直接复用完整 ChatPane composer，首次发送后由 `replaceDraftInWorkbench` 原子替换为 session tab；不保留第二套 composer 实现。
- W1 布局模型：`model.js`（openSession/openDraft/replaceDraft/select/focus/close/move/split/remove/resize/setTabMode/setPreset/capturePreset/reconcile）、`normalize.js`（含顶层 `version` 往返修复）、`presets.js`、`store.js`、`persistence.js`、`index.js`；`moveTab` 跨 group 选择/移动缺陷已修复并有回归测试。
- W1/W2 组件：`Workbench.svelte`/`SplitView.svelte`/`SplitHandle.svelte`/`Pane.svelte`/`TabStrip.svelte`/`EmptyPane.svelte` 使用 Svelte 5 snippet 递归渲染，支持标签选择/关闭、菜单分屏（移动选中标签到新 pane）、拖拽标签到 pane 四边分屏或中间移动、pointer/键盘 resize、4 叶上限、桌面最小 380px pane 回退单 pane、移动端单 pane + 合并标签条。
- W1/W3 集成：`App.svelte` 不再在 section 内路由变化时重挂载视图；Sidebar 新对话打开 Draft 标签；`currentSession` 与 URL 焦点双向同步（焦点变化使用 `replaceNavigate`）；布局写入共享 `mothx.workbench.v1`，刷新恢复标签/焦点/比例；Settings 中的“动态工作台布局”开关与 `?workbench=0` 回退已在 2026-10-29 清理中移除。
- W3：每个 tab 显示 running 指示与 pending approval/question 数量；工具条全局 attention 入口可聚焦首个待处理 pane；预设 `single/two-column/two-row/quad` 已接入；trajectory 继续作为每个 tab 的 chat/trajectory 模式。
- W4 代码减法：`ChatPane` 不再维护第二份 transcript/run/runtime 局部状态，也不再在切 tab 时搬运；视图全部由 `sessionRunStates` 经 `viewFromSessionState` 派生，所有显式写入走 `applySessionViewReducer`/`updateSessionState`。`persistLocalSessionState`/`restoreLocalSessionState`/`applyView`/`currentView` 已删除，`session-stream-boundary.test.js` 增加静态守卫。
- W4 运行时清理：删除 `chat-runtime-state.js` 中无人调用的 responses-run 轮询（后台轮询已归 `session-stream.js`）和 `onPersist` 回调；`sessionStateStore` 的派生缓存随 pane 卸载清理。ChatPane 的命令建议/运行时面板 DOM id 增加 pane 前缀，lightbox 键盘监听只由焦点 pane 响应；e2e 断言分屏后 DOM id 全局唯一。
- W4 标签溢出：标签条溢出时显示“全部标签”菜单，列出每个 tab 的运行/待处理状态并可直接聚焦；底部横向滚动仍保留。标签运行/待处理徽标改为直接订阅 `sessionRunStates`，审批完成后即时消失。
- 测试：`npm test`（209 tests）通过；`npm run build` 通过；`ui/e2e/workbench.smoke.mjs` 覆盖已有会话发送、Draft 首次发送后原子替换为真实 session、双 session 标签、刷新恢复、切换标签保留未发送草稿、拖拽取消后清除落点预览、真实拖拽重排/跨 pane/右边缘分屏与预览一致性、双页面共享 last-layout 和 last-write-wins 恢复、分屏双 pane 同时渲染、关闭 pane、跨 pane 待处理徽标/attention/在所属 pane 完成审批、跨 pane 后台提问不抢占焦点且聚焦后可在所属 pane 回答、跨 pane DOM id 唯一性、键盘调分隔条与双击恢复 50/50、比例刷新恢复、per-tab chat/trajectory 模式刷新恢复、per-pane 模型独立选择与刷新恢复、四宫格预设与第 5 个拖拽分屏被拒绝并提示原因、移动端单 pane、标签溢出菜单、终端面板（安全/共享 cwd 提示、初始 resize、键盘输入回显、断线重连、页面刷新 reattach 且不新建 POST、宿主缩放 resize、关闭 DELETE）；清理后额外断言 `?workbench=0` 被忽略且不存在 `.chat-single-host`。
- 复盘修复（本轮）：composer 草稿改为按 `sessionId`/`draft:<draftId>` 保存文本、内存中的 dataUrl 附件和可持久化的 providerRef/attachmentId 引用。`DraftPane` 传入 `draftKey`，切换标签（组件卸载）不再丢输入；刷新后仅剩可恢复引用，dataUrl 附件以 `missing` 元数据提示重新选择，发送/清空会删除草稿。新增 `composer-drafts.test.js` 覆盖 LRU 上限、跨刷新、dataUrl 不落盘。
- 复盘修复（本轮）：后台到达的审批在所属 pane 通过 attention 获得焦点后现在会自动弹出审批中心（之前只在 pending 首次到达且 pane 已激活时弹出，`selectedApprovalID` 已设置会让后续 focus 永远不打开）；e2e 的审批流程可稳定复现该竞态。
- 复盘修复（本轮）：分隔条支持双击恢复 50/50 并持久化；拖拽被 Escape 取消时 `dragend` 会清除落点预览，避免预览残留；超限（>512KB）布局文档在 `JSON.parse` 前丢弃并回退单 pane；标签会话兜底文案与残留硬编码英文错误改用双语 i18n。
- 复盘修复（本轮）：拖拽落点判定优先识别标签条：指针位于 tab strip 内时始终视为同 pane 重排，不再因为标签条落在 pane 左上角 25% 边缘区而被误判为左/上分屏。`workbench.smoke.mjs` 新增真实 drop 断言：同 pane 前后重排、跨 pane 中间移动、右边缘水平分屏，落点结果与预览一致，drop 后预览清除。
- 自动化多 tab 复检（本轮）：e2e 使用同一 Chromium profile 打开第二个页面，验证新页面继承共享 `mothx.workbench.v1` 的分屏布局；随后两个页面先后写入，后写入方在对方 reload 后成为恢复结果，且文档版本/标签唯一性保持有效。
- 复盘修复（本轮）：`settings.app.workbench` 与 `settings.app.workbenchHint` 原先只在中文词典存在，英文界面会回退成原始 key。已补齐英文翻译，并新增 `ui/src/lib/i18n.test.js` 静态守卫：断言 zh/en 键集合一致，且 `ui/src` 中所有 `t('...')` 使用的键在两种语言都存在。
- 复盘修复（本轮）：终端状态栏持续显示“不受沙箱保护”安全提示，并在服务端报告占位认证 token 时显示本地化警告；e2e 对两条提示均有断言。
- 复盘修复（本轮）：工作台焦点切换时终端 dock 会释放旧会话终端并按新焦点 cwd 重建；e2e 覆盖旧终端 DELETE 与新会话 POST，避免出现“界面已切 pane、终端仍绑定旧会话”的错位。
- 复盘修复（本轮）：终端 dock 的关闭/重连路径现在受益于后端的即时关闭（向进程组发 `SIGHUP`，进程退出即返回，只有忽略 HUP/TERM 时才等待宽限期），因此关闭面板或切换焦点不再固定阻塞 2 秒；浏览器 cookie 会话也不会再从 `POST /api/terminals` 响应中拿到长期 `terminalToken`。
- 复盘修复（本轮）：补齐 §7.7 的 per-session 滚动位置。新增 `lib/scroll-positions.js`（运行时 Map，LRU 64，不落 localStorage/服务端），`ChatPane` 在滚动与卸载时保存 `{top, following}`，切回标签或重新加载历史后恢复：跟随输出的 pane 回到底部，用户上翻的 pane 保持精确偏移；`scroll-positions.test.js` 覆盖无效值、覆盖写、遗忘与容量上限。
- 复盘修复（本轮）：关闭运行中的标签/其他标签/右侧标签/整个窗格时不再静默，也不弹阻塞确认；操作后若确有运行中的会话不再打开，会通过一次全局 notice 说明任务已在后台继续，可从 Sidebar 重新打开（`workbench.backgroundNotice` zh/en）。
- 复盘修复（本轮）：工作台 hydration 改为应用启动时的一次性步骤（`App.startApp()`），不再在懒加载的 `Chat.svelte` 每次挂载时重读 `localStorage`。此前从 Sidebar/Sessions 在 `/chat` 之外新建 Draft 后，若 300ms 持久化防抖尚未触发就切回 `/chat`，旧的存储布局会覆盖内存中的新任务；现在启动后内存状态权威，新页面仍在启动时继承共享 last-layout。新增 `lib/workbench/store.test.js` 覆盖延迟 hydration、首次共享布局恢复与一次性语义，`workbench.smoke.mjs` 增加到“从 Sessions 新建任务后返回 /chat 仍保留 Draft”。
- 提交前复检（2026-10-29）：`go test ./...`、`cd ui && npm test`（209 tests）、`npm run build`、`node e2e/workbench.smoke.mjs` 均通过。剩余证据仅为真实 Windows runner 与人工手感/长时性能验收。
- 未完成（人工）：触摸板/鼠标拖拽手感与 4-pane 长时流式性能仍需人工验收。
- 复盘修复（本轮）：新增 §12.1.15 的固定种子 property test（1000 步操作，每步校验 §5.2 不变量并做持久化 round-trip）。该测试发现并修复了四个真实缺陷：Draft tab 的 trajectory mode 在 `normalize` 中被硬编码回 `chat`；`removeGroup`/`setPreset` 可能把 `lastFocusedGroupId` 留在已删除或新 frame 不存在的 group；`normalizeWeights` 在极端比例下因浮点减法返回 `0.0999…` 而不是 `0.1`；`setPreset` 没有把焦点跟随到原选中 tab 的新 leaf。均补了对应回归测试。
- 复盘修复（本轮）：工作台 tab 对账只使用覆盖服务端 `total` 的完整分页；`/api/sessions` 默认只取前 100 条，之前会把超出该页的非焦点会话 tab 误判为已删除。`stores.js` 现在记录 `sessionsTotal`，`Chat.svelte` 在分页不完整时不做自动删除。
- 复盘修复（本轮）：修复分屏后重复的 DOM id。命令建议项从全局 `chat-command-suggestion-${index}` 改为 `${paneId}-command-suggestion-${index}`，`aria-activedescendant` 同步使用 pane 前缀；移动端合并标签条的右键菜单现在按 tab 实际所属 group 执行“关闭其他/右侧”，不再错误作用于焦点 group；分隔条 `aria-valuenow` 改为真实权重而不是固定 50。

- 复盘修复（本轮）：修复工作台/单 pane 宿主高度链路。`.wb-single-body`/`.wb-pane-body` 改为纵向 flex 容器，`.chat-view` 增加 `height: 100%`，`.chat-scroll` 增加 `min-height: 0`。新 Draft pane 现在撑满宿主；长历史只在 transcript 内滚动，composer 始终留在视口内。`workbench.smoke.mjs` 现在载入 30 轮历史，并断言初始加载、切回标签、分屏、Draft 四种状态下 chat view 高度等于宿主、composer 可见、长历史确实产生内部滚动。
- 界面打磨：工作台标签条改为主题 token 驱动的软 pill 样式，含 active 页面底色/内描边/轻阴影、hover 过渡、隐藏滚动条、SVG 关闭按钮、运行脉冲点、明暗主题一致的 pending 徽章，以及统一的溢出菜单；标签条收紧到 31px（26px 标签，上下各 2px）。顶部工作台工具条：桌面 30px 高、26px icon-only 按钮（预设布局用图标分段控件，分屏/关闭/重置/终端/待处理用带 tooltip 的图标）；移动端 42px 高、34px 触控按钮，隐藏预设/重置并增加侧栏菜单入口。桌面与 390px 移动端都不需要横向滚动；e2e 断言桌面工具条 ≤31px、按钮 ≤28px，移动端 ≤44px 且无横向溢出。
- 界面打磨：会话目录从 composer 移到 `chat-session-header` 内、`chat-session-headline` 之后，作为紧凑目录 chip；不新增按钮，因为左侧已有全局新建对话入口。目录文本自动截断，完整路径保留在 title；新 Draft 继续使用 composer 内的目录选择器，不在 header 重复展示。header 内的 headline、目录 chip、下载工具和 chat/trajectory tabs 统一垂直居中（e2e 断言各元素中线偏差 ≤1px）。e2e 同时断言会话目录位于 header、紧跟 headline、不在 composer 内且没有重复的新建会话按钮。
- 界面打磨（对齐 Desktop composer）：输入卡片不再常驻 provider/model/主角团选择行，也移除了独立的“模式”按钮。输入框位于卡片顶部，模型与模式合并进底部工具栏的 `composer-config-toggle`（显示当前模型 + mode 徽章），按需在输入框下方展开配置面板：面板顶部是 plan/agent/yolo/os 模式与 Work/Code 显示模式，下面是 provider/model/主角团选择；会话目录移到 `chat-session-header` 的 headline 后面，composer 内不再保留目录/新建会话按钮。桌面端折叠态卡片高度从 174px 降到 133px，移动端仍保持单卡片内换行布局；e2e 覆盖默认折叠、展开/收起、mode 徽章、面板内模式组以及折叠后仍可切换模型。
- 清理（2026-10-29）：按 §11.3 删除 `?workbench=0`、Settings 中的“动态工作台布局”开关、`mothx.webui.workbenchEnabled` 偏好、`chat-single-host` 单 pane 回退和 `Chat.svelte` 的 `workbenchEnabled` 分支；同时删除不再使用的 `sessionRuntime`/`pendingApprovals` stores 与 runtime panel/mode switcher/display mode CSS。`/chat` 现在只有 Workbench 投影。e2e 断言 `?workbench=0` 被忽略、Workbench 仍渲染且不存在 `.chat-single-host`。
- W3 可选 Plan 面板：新增 `components/workbench/PlanPanel.svelte`，从焦点会话 `sessionRunStates[sessionId].messages` 的最近一条 `role === 'plan'` 消息派生，无新存储/工具/后端端点。Workbench 工具条新增 Plan 图标按钮，桌面为右侧 320px dock，移动端为同区 overlay；空会话/无 plan 显示空态，Draft 不显示 Plan 入口。e2e 覆盖有 plan、空 plan、Draft 无入口，以及桌面 dock 不覆盖 pane 区域。
- 复检（2026-10-29）：`go test ./...` 与 `cd ui && npm run e2e`（209 单测、构建、channel/trajectory/workbench 浏览器 smoke）通过；workbench smoke 覆盖 `?workbench=0` 被忽略、Plan dock 内容/空态/Draft 无入口/桌面 dock 几何，以及标签条、icon-only 工具条、composer 配置面板和 header 中线对齐的紧凑性断言。
- 界面统一（2026-10-30）：`c6cf28fb` 统一 Settings 保存栏/页头、统计图表、聊天标题与工具条、技能筛选和日志样式；工作台标签改为展示会话标题/预览而不是 16 位 ID，移动端工具条新增侧栏菜单并按触控尺寸布局，e2e 已同步标题与移动端断言。两个方案的门禁复检通过：`go test -count=1 ./...`、`npm test`（209）、`npm run build`、三个 e2e smoke。ESM 偶发失败另由 `internal/esm` 的事务化写入和 orphaned 执行快照的控制放行修复，不属于本方案但影响同一条复检结论。

未完成 / 后续：

- 人工验收：触摸板/鼠标拖拽手感已于 2026-10-29 确认通过。仍需验收 4-pane 长时流式性能；自动化已覆盖拖拽重排/跨 pane/边缘分屏结果、Escape 取消、多 tab last-write-wins 恢复、宽度矩阵（1024/1280/1440/1920 与 390/768）与徽标断言。跨 pane 审批与提问流程已由 `workbench.smoke.mjs` 自动化覆盖（后台提问只打徽标，聚焦所属 pane 后才显示浮层并可直接回答）。
- 复盘修复（严重）：`ChatPane.sendPrompt()` 原先声明 `let sessionID = paneSessionID`，在同作用域内引用外层同名 prop 触发 TDZ `ReferenceError`，导致 WebUI 任何一次发送都失败。现改为局部 `runSessionID` 并在函数体内统一使用；e2e 覆盖已有会话发送与 Draft 首次发送。
- 复盘修复：路由焦点同步在 reset/close 后会保留陈旧的 `lastRouteSession`，使下一次显式打开同一 session 被当成重复路由而忽略；现无论 URL 是否已匹配都先更新 `lastRouteSession`。关闭 tab 后也总会把 `currentSession` 和路由投影到新的焦点。删除了 `currentSession -> openSession` 的重复副作用，避免关闭后又被陈旧状态重新打开；列表刷新只删除已确认消失的 session，焦点中或提交中的新 session 受保护。
- 复盘修复：`ModelPicker` 把 `bind:this` 的组件实例当成 DOM input 调用 `focus()`，打开模型菜单时会抛 `TypeError`；现改为 `root.querySelector('input')?.focus()`。`ChatPane` 还会在 catalog/default 后到达时把无显式偏好的 session 从占位 `default` 切到设置默认模型，修复刷新后模型显示停留在 `default` 的问题；e2e 断言 A/B 两个 pane 的模型互不影响且刷新后恢复。
- 复盘修复：提问浮层原先被错误嵌套在审批中心的 edit 分支中，导致待处理提问必须打开审批中心且存在编辑类审批时才渲染；现已移到顶层，并用 `active && selectedQuestion` 保证后台 pane 的提问不会遮挡焦点 pane。
- 复盘修复：工作台布局操作原先静默忽略 `center-limit`/`tab-limit` 失败；现统一通过全局 notice 提示原因，4-pane 上限时工具栏分屏按钮的 title 也说明限制。`Chat.svelte` 对新建 draft、菜单分屏、拖拽分屏/移动、预设应用都会报告失败原因（`same-position` 除外）。
- `ChatPane` 的 composer 仍内嵌在同一个组件中。进一步拆分需要把模型/provider、附件、草稿、技能与运行时开关全部改为显式 props；当前只有一份实现且 DraftPane 直接复用，拆分的收益低于状态所有权扩散的风险，因此保留在 W4 评估结论中。
- Lite `SessionSnapshot` 继续定位为非 v1 实验降级，不进入默认发布路径。

## 1. 背景与问题定义

### 1.1 当前 serve WebUI 的真实结构

`ui/src/App.svelte` 是外壳：

```text
App.svelte
├─ Sidebar.svelte（任务树 / 项目 / 导航；现有移动端抽屉不属于本方案）
├─ main.workbench
│  ├─ Topbar.svelte（非 chat 路由）
│  ├─ Banners.svelte
│  └─ .view-container
│     └─ lazy view：Chat / Sessions / Experts / Stats / Cron / Skills / Knowledge / Settings
```

`/chat` 只挂载一个 `views/Chat.svelte`：

- `currentSession`（`lib/stores.js`）是全局单值；`Sidebar.svelte`、`Sessions.svelte`、`Experts.svelte`、`Chat.svelte` 都直接 `currentSession.set(id)`。
- `Chat.svelte` 用大量局部变量保存**当前会话**的视图：`messages`、`chatEvents`、`sessionRunEvents`、`sessionRuntimeValue`、`busy`、`currentRunID`、`prompt`、`pendingAttachments`、滚动位置等；切换会话时通过 `persistLocalSessionState` / `restoreLocalSessionState` 在局部变量与 `sessionRunStates` 之间搬运。
- `lib/session-runs.js` 已经有按 `sessionId` 的状态表：`sessionRunStates = writable({})`，每个 session 有 `messages/toolEvents/runEvents/capabilityEvents/runtime/cursor/historyLoaded/subAgents/...`。
- `lib/session-view.js` 已经有纯 reducer：`viewFromSessionState`、`sessionStateWithView`、`reduceTranscriptEvent`、`reduceRunEvent`、`reduceApprovalRequest` 等；文件头明确写着“The chat view renders one session at a time, but run events arrive for every subscribed session... background sessions update their own entry in sessionRunStates directly.”
- `lib/stores.js` 的 `connectRuns()` 打开一个 `/ws/runs` WebSocket，并 `runSubscriptions()` 把 `sessions` 列表中的**所有**会话订阅上；`runEvents` 是全局追加数组，`sessionRunStates` 按 session 归约。也就是说，**后台会话的实时事件已经到达浏览器**，只是没有第二个 pane 渲染它；历史仍需按 session 懒加载。
- `Chat.svelte` 里 `$: { ... $runEvents ... handleSessionStreamEvent(item.sessionId, ...) }` 是唯一的全局事件消费者；`applySessionViewReducer` 已经区分“当前会话写局部变量、后台会话写 `sessionRunStates`”。

这个结构有一个非常重要的含义：**WebUI 已经具备做多 pane 的基础**。它不需要新建会话投影，只需要：

1. 把 `Chat.svelte` 中“当前会话”的渲染拆成 `ChatPane(sessionId)`；
2. 把 `$runEvents` 的消费移到唯一的 `session-stream.js`，不再由每个 pane 各消费一次；
3. 把 `currentSession` 从可写全局源改为工作台焦点投影；
4. 加布局模型、标签/分屏渲染、localStorage 持久化。

### 1.2 用户问题

- **无法并排看两个 agent。** 一个会话在跑长任务、一个在等审批、一个在写代码；现在只能切换，切换会触发 `Chat.svelte` 的 `$currentSession` 响应式块，重新加载/恢复转录。
- **后台运行不可见。** 侧边栏有 running 状态，`sessionRunStates` 也在更新，但 UI 一次只画一个。
- **审批/提问被埋。** 后台会话的 pending approvals 只在切过去时看到；没有“所有运行中会话”的全局视图。
- **轨迹和对话不能并排。** `SessionHeader` 只有 Chat/Trajectory 二选一；想看轨迹时对话消失。
- **布局不记忆。** 刷新/重新打开浏览器，回到默认单会话；无法固定“左会话、右轨迹”的工作方式。
- **窄窗口/手机不可用。** 小于约 1024px 的分屏不可用；移动端仍应能用单 pane + 标签条聊天，不能因为工作台把手机聊天做没。

### 1.3 为什么 WebUI 可以直接复用现有会话状态

WebUI 不需要像“单 transcript 架构”那样先做一次会话投影重构：

- `sessionRunStates` 已经以 `sessionId` 为键保存 `messages/toolEvents/runEvents/runtime/...`；
- `session-view.js` 已经把流式事件归约成纯函数；
- `/ws/runs` 已经订阅 `sessions` 列表中的所有会话，后台会话的**实时事件**已经到达浏览器（历史仍需按 session 懒加载）；
- `Chat.svelte` 的 `applySessionViewReducer` 已经在“当前会话”和“后台会话”之间分流。

所以本方案只做三件事：**把渲染拆成 `ChatPane(sessionId)`、把事件消费收敛到一个 `session-stream.js`、把布局/持久化加上**。不新增会话数据模型，也不改后端协议。注意：`sessionRunStates` 的后台条目只保证“已订阅事件”的实时投影，不自动包含完整历史；每个打开的 tab 仍需 `ensureSessionLoaded(sessionId)`。

## 2. 目标、非目标与名词

### 2.1 目标

1. `/chat` 支持会话标签页：Sidebar 点击会话时，已打开则聚焦，未打开则插入当前 pane；支持关闭、重排、溢出菜单。
2. 中央分屏：最多 4 个 pane，支持左右/上下二分、拖拽标签到边缘分屏、菜单分屏、分隔条拖动。
3. 每个 pane 有独立选中会话、独立滚动、独立 composer；v1 目标是所有 pane 都可交互。Lite 只读快照不是 v1 交付物；如 W0 严重延期，只能另立实验分支，不进入本方案验收。
4. 焦点模型：`focusedSessionId` 由聚焦 pane 的选中标签推导；Sidebar 高亮、路由、StatusBar 都跟随它。
5. 布局记忆：刷新、重启浏览器、新 tab 都恢复最近拓扑、每个 pane 的标签、焦点、比例；非法文档回退单 pane。
6. 响应式窗口：每个 pane 最小 380px；宽度足够时按 frame 分屏（900–1024px 也允许 2 pane），不够时整体回退单 pane + 标签条；`isMobile`（≤900px）始终单 pane；窗口缩放不重写持久化比例。
7. 不改变 serve 后端的会话/run 语义；不新增端点；不改变现有审批/提问/流式协议。
8. 不显著增加首屏包体：不引入 React/DnD 库；新增纯 JS 模型 + Svelte 组件，按路由懒加载。
9. 可回滚：GA 后默认开启；`?workbench=0`/设置关闭项已移除，回退只重置布局，事件消费者仍是 `session-stream.js`，不得恢复第二套消费路径。

### 2.2 非目标

- **只做 serve WebUI。** 不修改、不共享其他 renderer。
- **不改 TUI/CLI/ACP。** 工作台是 WebUI 表现层。
- **不改 Go/serve API。** 不新增 `/api/layout`、不把布局写进 session 元数据、不改 `/ws/runs` 协议。
- **不做实时跨 tab 镜像。** 同一 origin 共享一份 last-layout，多 tab last-write-wins；不同 tab 同时打开同一 session 各自投影（后端 session/run 是权威）。
- **不做多用户共享布局。** 布局是浏览器本地表现状态，不跟随账号/设备。
- **不做通用 docking。** 只支持 session tab 与（未来）只读工具面板；不把任意 Svelte 组件注册成可停靠面板。
- **v1 不交付 Lite 只读 pane。** `SessionSnapshot` 仅作为 W0 延期时的实验降级形态，必须依赖 `session-stream.js`，且不进入验收/发布；主线始终是 `ChatPane`。
- **不做终端（v1）。** WebUI 没有 PTY；底部 dock 未来只放轨迹/日志。PTY 是独立后端能力，见 `docs/proposal/serve-pty-terminal-proposal.md`。
- **不做自定义预设编辑器（v1）。** 只提供 `single`/`two-column`/`two-row`/`quad`。
- **不做跨 pane 拖拽会话内容/消息。** 只拖标签。
- **不做移动端分屏/触摸拖拽。** 移动端只做单 pane + 标签条；不做 4 pane 分屏、拖拽或触摸手势。
- **v1 不做 tty/terminal 面板。** 现有 `bash` 工具输出只作为 transcript/trajectory 的一部分展示，不是交互式终端。PTY 后端方案见 `docs/proposal/serve-pty-terminal-proposal.md`。
- **v1 不做 spec-graph 面板。** MothX 当前没有 ThinkRail 的 `pi-spec-graph` 等价物；不把通用文件浏览器/知识库冒充 spec 面板。
- **不把手机当作分屏验收场景。** 分屏只在 1024px+ 验收；390/768px 验收的是单 pane + 标签条可用，不是分屏。

### 2.3 名词

| 名词 | 含义 |
| --- | --- |
| Workbench | `/chat` 区域内的动态布局容器：中心分屏树 + 标签。 |
| Frame | 布局形状：分屏树、group id、几何比例；不含会话。 |
| View | 每个 group 的 tabs、选中项；`mode` 存在每个 tab 上（chat/trajectory）；整个 origin 一份（last-write-wins）。 |
| Attention | 当前聚焦 group、最后聚焦 group、导航时钟（runtime-only）；另含 pending approval/question 的全局提醒。 |
| Pane / Group | 分屏树叶子；拥有标签条和内容区。 |
| ChatPane | 可交互的会话 pane：composer、审批、轨迹切换。 |
| SessionSnapshot | 非 v1 的实验降级只读 pane：从 `sessionRunStates` 渲染转录/轨迹，不提供发送；必须依赖 `session-stream.js`。 |
| Draft tab | 尚未创建 session 的新会话标签；首次发送后原子替换为 session tab。 |
| Surface | 一个浏览器 tab/window；v1 不再作为布局持久化作用域，仅用于运行时焦点/拖拽状态。 |
| Preset | 资源无关的 frame 模板；应用时重分配现有 tab。 |
| Projection epoch | shape 变化递增的运行时栅栏；取消旧拖拽/延迟焦点。 |
| Tool panel | 非 session 的只读工具面板（如 plan/trajectory/logs）；v1 不做 tty/spec。 |

### 2.4 成功标准

- 同一 `/chat` 页面可并排看到两个 session 同时流式输出，内容不串。
- Sidebar 点击已打开会话只聚焦；关闭标签不删除 session；关闭运行中标签不阻塞，后台继续并可再次打开。
- 刷新、重启浏览器、打开新 tab 后恢复最近布局；损坏布局回退单 pane。
- 1024/1280/1440/1920px 宽度下分屏可用；900–1024px 在能容纳 2 个 ≥380px pane 时分屏；390/768px 下单 pane + 标签条可用，无横向溢出。
- 4 pane 同时流式时页面可交互；滚动/输入无明显卡顿。
- 每个 pane 的模型/provider/草稿互不覆盖；后台 pending approval/question 有可见入口。
- 关闭开关后与今天完全一致。
- `cd ui && npm test && npm run build` 通过；新增纯模型测试覆盖所有操作与非法文档。

## 3. 借鉴清单（ThinkRail → serve WebUI）

| # | ThinkRail 做法 | WebUI 对应 |
| --- | --- | --- |
| 1 | 受控渲染器 + 纯函数 mutation | `Workbench.svelte` 只接收 `state + callbacks`；`lib/workbench/model.js` 返回完整 state 或 reason |
| 2 | Frame / View / Attention 分离 | 同构；View 额外支持 `mode: chat/trajectory` |
| 3 | document 是投影，不是权威 | 渲染 DTO 由 `project(frame, view)` 生成；store 只存 frame/view |
| 4 | 形状变化不 remount | Svelte 用 `{#key}`/稳定 id 保 DOM；分屏结构变化时接受必要的 DOM 重建，会话数据在 `sessionRunStates` 外部 |
| 5 | 渲染隔离 | 每个 pane 只订阅自己的 session state；布局 store 与 `sessionRunStates` 分离 |
| 6 | 版本化本地文档 + 严格校验 + 回退 | `mothx.workbench.v1` localStorage 文档；非法回退 `single` |
| 7 | 资源无关预设 | `single/two-column/two-row/quad`；应用时重排 session tab |
| 8 | 拖拽只画一个合法结果 | 原生 Pointer Events；`previewDrop` 纯函数；非法目标不画 |
| 9 | 键盘/ARIA 等价路径 | tablist/tab/tabpanel、splitter、菜单命令 |
| 10 | 手势失败回滚 | resize 草稿 runtime-only；epoch 取消 |

**不借：** React 组件、`react-resizable-panels` 的 hook、`@dnd-kit/core`、Electron `windowId`、ThinkRail 的 file/diff/terminal 资源。

## 4. 总体架构

### 4.1 分层

```text
┌──────────────────────────────────────────────────────────────────┐
│ serve 后端（权威，不改）                                         │
│  /ws/runs 订阅 sessions 列表 · /api/sessions/:id/messages        │
│  /api/sessions/:id/stream（SSE fallback）· 审批/提问接口         │
└───────────────────────────────┬──────────────────────────────────┘
                                │ WebSocket / SSE / REST
┌───────────────────────────────▼──────────────────────────────────┐
│ ui/src/lib/session-stream.js（新增，唯一事件消费者）             │
│  $runEvents → reduce → sessionRunStates[sessionId]               │
│  visibleSessionIds 只影响 UI 副作用（滚动/刷新/错误提示）        │
└───────────────────────────────┬──────────────────────────────────┘
                                │ sessionRunStates (Svelte store)
┌───────────────────────────────▼──────────────────────────────────┐
│ ui/src/lib/workbench/（纯 JS，无 Svelte 组件/无 API）            │
│  types · model · normalize · presets · decode/encode             │
└───────────────────────────────┬──────────────────────────────────┘
                                │ frame/view/attention
┌───────────────────────────────▼──────────────────────────────────┐
│ ui/src/lib/workbench/store.js（Svelte store + persistence）      │
│  commit · focus · hydrate · debounce localStorage                │
└───────────────────────────────┬──────────────────────────────────┘
                                │ $workbench
┌───────────────────────────────▼──────────────────────────────────┐
│ ui/src/components/workbench/（Svelte 渲染器）                    │
│  Workbench · SplitView · Pane · TabStrip · EmptyPane · DragLayer │
│  Pane body = ChatPane(sessionId) 或 SessionSnapshot(sessionId)   │
└───────────────────────────────┬──────────────────────────────────┘
                                │ actions
┌───────────────────────────────▼──────────────────────────────────┐
│ ui/src/lib/session-actions.js（显式 sessionId）                  │
│  sendPrompt/stop/approve/answer/loadOlder/loadTrajectory         │
└──────────────────────────────────────────────────────────────────┘
```

**关键边界：** `session-stream.js` 是唯一消费 `$runEvents` 的模块；`ChatPane` 不再各自消费全局事件。否则两个 pane 会把同一条事件归约两次，后台状态会被重复写。

### 4.2 目录与模块

新增：

```text
ui/src/lib/workbench/
├── model.js              # 纯操作：open/close/move/split/resize/focus/preset/draft
├── normalize.js          # decode/encode/normalize/reconcile
├── presets.js            # single/two-column/two-row/quad
├── store.js              # Svelte writable + hydrate/persist + focus
├── persistence.js        # localStorage 共享文档 / 校验 / 恢复最近布局
└── index.js              # 桶导出 + JSDoc 类型

ui/src/lib/session-stream.js      # 唯一 $runEvents 消费者（必须在 workbench 纯层之外）
ui/src/lib/session-store.js       # per-session derived store + forget
ui/src/lib/session-prefs.js       # per-session model/provider 选择持久化
ui/src/lib/composer-drafts.js     # text + 已上传附件引用；按 session/draft 保存

ui/src/components/chat/
├── ChatPane.svelte           # 可交互会话 pane（composer 仍在组件内）
└── DraftPane.svelte           # 新会话草稿；首次发送后 replaceDraft

ui/src/components/workbench/
├── Workbench.svelte      # 根：订阅 store、持有 drag/resize 草稿
├── SplitView.svelte      # 递归 frame.center
├── Pane.svelte           # pane 容器 + 标签条 + 内容
├── TabStrip.svelte       # 标签、溢出、拖拽源、pending 徽标
├── SplitHandle.svelte    # 分隔条（pointer + keyboard）
├── EmptyPane.svelte      # 空 pane：新建任务/打开会话提示
├── SessionSnapshot.svelte # W0 延期时的只读会话（降级形态，不默认发布）
└── PlanPanel.svelte      # 2026-10-29 新增的只读 Plan dock
```

实现收敛说明：计划中的 `types.js`（JSDoc typedef）并入 `index.js`/`model.js`；`session-actions.js` 的职责由 `ChatPane` 的显式 `sessionId` 动作承担；`attention.js` 由 `sessionRunStates` + 工具条 attention 入口投影。未创建独立的 `ComposerPane.svelte`（`ChatPane` 与 `DraftPane` 共用同一份 composer 实现）、`AttentionBell.svelte`（由工作台工具条的 attention 按钮替代）和 `DragLayer.svelte`（投放预览由 `Workbench` 本地状态管理）。

修改：

```text
ui/src/App.svelte                         # chat 路由渲染 Workbench（开关/移动端投影）
ui/src/views/Chat.svelte                  # 拆成 ChatPane；保留薄 wrapper 或删除
ui/src/views/Sessions.svelte              # 打开会话走 workbench.openSession
ui/src/views/Experts.svelte               # 子会话打开走 workbench.openSession
ui/src/components/Sidebar.svelte          # 打开会话走 workbench.openSession
ui/src/components/chat/SessionHeader.svelte # 已支持 sessionID/view/onViewChange props；只需改调用方
ui/src/lib/router.js                    # 增加 replace 导航语义（焦点切换不产生历史记录）
ui/src/lib/stores.js                      # currentSession 改为工作台焦点投影；selectedModel 改为按 session/pane 解析
ui/src/lib/chat-runtime-state.js          # manager 从 currentSession 闭包改为显式 sessionId；轮询按 session 注册而非 pane 挂载
ui/src/lib/session-runs.js                # 增加 status/loading/effects/history window/缓存淘汰
ui/src/lib/session-view.js                # 复用；可能新增 selectors
ui/src/style.css                          # workbench/pane/splitter/responsive 样式
ui/package.json                           # test 脚本显式覆盖 lib/workbench 子目录
```

### 4.3 依赖规则

- `lib/workbench/*` **禁止** import `views/Chat.svelte`、Svelte 组件、`lib/stores.js`、`lib/api.js`；只允许纯 JS 与自身类型。
- `lib/workbench/store.js` 可以 import `lib/workbench/*`、`lib/persistence.js`；不 import Chat/API。
- `lib/session-stream.js` 可以 import `lib/stores.js`（runEvents/sessions）、`lib/session-runs.js`、`lib/session-view.js`、`lib/api.js`；它是唯一把 socket 事件变成 session state 的地方，也是唯一允许消费 `$runEvents` 的模块。**W0 之后它应在 App 启动时始终运行**；`workbenchEnabled` 已移除，不再控制事件消费者；**不保留旧 Chat 的 `$runEvents` 消费路径**。
- SSE fallback 的 observer 生命周期、`stopObserver` 和 `runEvents` cursor 归 `session-stream.js` 所有；`ChatPane` 不得在 `onDestroy` 里 abort `sessionRunStates[*].observer`，否则关闭一个 pane 会中止其他会话的流。
- **隐藏 tab 也要有 runtime 轮询 owner。** responses-run 重连/轮询按 `sessionId` 注册在 `session-stream.js` 或共享 registry，不能只随 `ChatPane` 挂载启停；否则后台运行的重连会延迟到用户重新选中。
- `components/workbench/*` 可以 import store、纯模型、`ChatPane`/`SessionSnapshot`、现有 UI 组件；不直接 fetch。
- `components/workbench/*` 和 App 集成层在 pane 选择变化时调用 `session-stream.setVisibleSessions(ids)`；它只驱动 effects，不参与归约。
- `ChatPane` 只通过显式 `sessionId` 和 `session-actions.js` 工作；不读 `currentSession` 作为目标，也不直接拼 provider content。
- `ChatPane` 的模型/provider 从该 session 的 state/runtime 派生，选中变更写回该 session；禁止继续用全局 `selectedModel` 作为发送源。
- `currentSession` 是 workbench 焦点的共享投影。Sidebar/Sessions/Experts 仍以 `currentSession.set()` + `navigate()` 作为导航入口（不是工作台状态 owner），`Chat.svelte` 再把 route/currentSession 投影为 workbench focus；已删除 `currentSession -> openSession` 的重复副作用，避免关闭后又被陈旧状态重新打开。

### 4.4 数据流

**打开会话：**

```text
Sidebar click / Sessions / Experts / 深链
  → workbench.openSession(sessionId)
      → 已打开：聚焦并选中
      → 未打开：插入聚焦 pane（若达到 tab 上限则拒绝）
      → workbench store commit
      → currentSession = focusedSessionId；route 同步 ?session=
  → ensureSessionLoaded(sessionId)（懒加载消息/运行事件/历史窗口）
```

**打开新会话（draft）：**

```text
EmptyPane "新建会话" / resetSession
  → workbench.openDraft({ workDir, model, provider, expertId })
  → Draft tab 显示现有 composer（不创建 session）
  → 首次发送：POST /api/session-id → workbench.replaceDraft(draftId, sessionId)
  → 该 tab 变 session tab；消息/run 仍走统一 session 路径
```

**流式更新：**

```text
/ws/runs → stores.runEvents
  → session-stream.js（唯一消费者）
      → 对每个 event: reduce 到 sessionRunStates[event.sessionId]
      → 记录 effects（scroll/historyReload/statsRefresh/subagent/approval/question/error）到 per-session effect 标记
  → ChatPane 订阅 $sessionRunStates[id] → 只重渲染自己的 DOM
```

**布局提交：**

```text
拖拽/关闭/分屏/分隔条
  → 组件本地草稿（不写 store）
  → pointerup / 键盘确认
  → pure model 返回完整 state 或 reason
  → workbench store commit
  → 订阅者更新；debounce 300ms → localStorage
```

### 4.5 与路由/`currentSession` 的关系

- hash 路由的 `?session=` 只表示**焦点会话**，不编码整个布局（避免 URL 过长、分享布局）。`?view=trajectory` 只表示焦点 pane 的模式；其他 pane 的模式存在 workbench view 里。
- 深链打开时：**先** hydrate 布局，再判断 session 是否已打开；未打开则插入焦点 pane；最后同步 `currentSession`。避免 `App.svelte` 的 `currentSession.subscribe(syncRouteFromSession)` 在 hydrate 前把 `?session=` 覆盖成 `/chat`。
- 路由变化（前进/后退/地址栏）也走 `workbench.openSession`，不要直接 `currentSession.set()`；`App.svelte` 的 `syncSessionFromRoute` 在开关开启时必须委托给 workbench。
- **焦点切换用 replace，不用 push。** 当前 `router.navigate` 通过改 `window.location.hash` 会新增历史记录；多 pane 切换焦点、点击 pane 内部时不能每次污染历史。为 `router.js` 增加 `replaceNavigate`（`history.replaceState`）或 `navigate(path, { replace: true })`：Sidebar/深链/新建会话用 push，pane focus/路由回写用 replace。
- `App.svelte` 的 `currentSession.subscribe(syncRouteFromSession)` 保留，但 `currentSession` 只由 workbench 写；Sidebar 等调用 `openSession` 而不是直接 set。
- 当没有打开任何 tab 时，显示 EmptyPane；新建会话走 Draft tab，不依赖 `currentSession=''` 的单例路径。

## 5. 数据模型

### 5.1 类型（JSDoc）

WebUI 是 JS；用 JSDoc typedef 提供类型，不引入 TS 编译步骤。

```js
/**
 * @typedef {"session" | "draft"} LayoutTabKind
 * @typedef {{ kind: "session", id: string, sessionId: string, mode?: "chat" | "trajectory" }} LayoutSessionTab
 * @typedef {{
 *   kind: "draft",
 *   id: string,
 *   draftId: string,
 *   workDir?: string,
 *   model?: string,
 *   provider?: string,
 *   expertId?: string,
 *   mode?: "chat" | "trajectory"
 * }} LayoutDraftTab
 * @typedef {LayoutSessionTab | LayoutDraftTab} LayoutTab
 *
 * @typedef {{ kind: "group", id: string }} WorkbenchGroup
 * @typedef {{
 *   kind: "split",
 *   id: string,
 *   direction: "horizontal" | "vertical",
 *   weights: [number, number],
 *   children: [WorkbenchCenterNode, WorkbenchCenterNode]
 * }} WorkbenchSplit
 * @typedef {WorkbenchGroup | WorkbenchSplit} WorkbenchCenterNode
 *
 * @typedef {{ version: 1, center: WorkbenchCenterNode }} WorkbenchFrame
 *
 * @typedef {{
 *   tabs: LayoutTab[],
 *   selectedTabId?: string
 * }} WorkbenchGroupView
 *
 * @typedef {{ groups: Record<string, WorkbenchGroupView> }} WorkbenchView
 *
 * @typedef {{
 *   focusedGroupId: string,
 *   lastFocusedGroupId: string
 * }} WorkbenchAttention
 *
 * @typedef {{
 *   frame: WorkbenchFrame,
 *   view: WorkbenchView,
 *   attention: WorkbenchAttention
 * }} WorkbenchState
 *
 * @typedef {{
 *   version: 1,
 *   frame: WorkbenchFrame,
 *   view: WorkbenchView,
 *   attention: WorkbenchAttention
 * }} PersistedWorkbenchV1
 */
```

`mode` 存在 tab 上，而不是 group 上：同一 pane 切换会话时，每个 tab 保留自己的 Chat/Trajectory 选择；拖拽/换布局/preset 重分配都不会丢失。新 tab 默认 `chat`，`mode` 缺失按 `chat` 处理。

Draft tab 只携带新会话所需的本地选择（workDir/model/provider/expertId/mode），不携带消息；首次发送成功后由 `replaceDraft` 原子换成 `session` tab，消息/run 永不走 draft 分支。Draft 元数据允许随布局一起持久化；composer 文本和已上传附件引用由 `composer-drafts.js` 单独保存，避免刷新丢输入。布局文档本身不写消息、附件、凭据或运行时状态；workDir/model/provider 只作为 draft 的本地展示/创建参数，敏感路径风险见 §9.2。

### 5.2 不变量

1. `frame.center` 至少一个 group 叶子；split 的两个子树都非空。
2. 叶子数 `[1, 4]`；超过返回 `center-limit`。
3. group id、split id 全局唯一且不冲突。
4. `weights` 有限、每项 `[0.1, 0.9]`、和约 1（容差 1e-6）。
5. `view.groups` 的 key 与叶子 group id 完全一致；缺失补空，孤儿删除。
6. 同一个 `sessionId` 在一份工作台文档内最多出现一次；tab id = `session:${sessionId}`。
7. 同一个 `draftId` 最多出现一次；`replaceDraft` 后 draft 不得残留。
8. `selectedTabId` 属于 group；空 group 无 selected。
9. `attention.focusedGroupId` 存在；被删时回退 `lastFocusedGroupId` 或第一个叶子。
10. 关闭最后一个 tab 不删除 group；只有显式 `removeGroup` 才改变拓扑。
11. 移动/合并/应用预设不得丢失 tab；关闭是唯一显式丢弃。
12. 持久化只含上述字段；未知字段丢弃，未知版本回退 `single`。
13. 每个 tab 的 `mode` 只能是 `chat`/`trajectory`；缺失或非法值回退 `chat`。

### 5.3 示例文档

```json
{
  "version": 1,
  "frame": {
    "version": 1,
    "center": {
      "kind": "split",
      "id": "s1",
      "direction": "horizontal",
      "weights": [0.5, 0.5],
      "children": [
        { "kind": "group", "id": "g1" },
        { "kind": "group", "id": "g2" }
      ]
    }
  },
  "view": {
    "groups": {
      "g1": {
        "tabs": [
          { "kind": "session", "id": "session:abc", "sessionId": "abc", "mode": "chat" },
          { "kind": "session", "id": "session:def", "sessionId": "def", "mode": "trajectory" }
        ],
        "selectedTabId": "session:abc"
      },
      "g2": {
        "tabs": [
          { "kind": "session", "id": "session:ghi", "sessionId": "ghi", "mode": "trajectory" }
        ],
        "selectedTabId": "session:ghi"
      }
    }
  },
  "attention": {
    "focusedGroupId": "g1",
    "lastFocusedGroupId": "g2"
  }
}
```

### 5.4 为什么不按 workspace/project 分 view

WebUI 的 Sidebar 项目只是任务目录分支，没有“活动项目”概念；同一 origin 一份 view 足够。若未来需要“每个项目不同布局”，把 `WorkbenchView` 提升为 `viewsByScope: Record<scopeId, WorkbenchView>` 并升 `version: 2`，不要把未使用的字段提前放进 v1。

## 6. 纯操作与状态机

### 6.1 结果类型

```js
/**
 * @typedef {(
 *   | { code: "center-limit", limit: number }
 *   | { code: "group-missing", groupId: string }
 *   | { code: "tab-missing", tabId: string }
 *   | { code: "same-position" }
 *   | { code: "last-group" }
 *   | { code: "unknown-preset", presetId: string }
 *   | { code: "tab-limit", limit: number }
 *   | { code: "draft-missing", draftId: string }
 * )} LayoutUnavailableReason
 *
 * @typedef {(
 *   | { ok: true, state: WorkbenchState, focus?: { groupId: string, tabId?: string }, shapeChanged: boolean }
 *   | { ok: false, reason: LayoutUnavailableReason }
 * )} LayoutResult
 */
```

纯层不返回本地化文案；UI 用 `reason.code` 映射到 `workbench.reason.*` 的 i18n key（`ui/src/lib/preferences.js` 的 `t`）。这样 `model.test.js` 不依赖语言。

### 6.2 操作清单

| 操作 | 签名 | 语义 | 失败原因 |
| --- | --- | --- | --- |
| `openSession` | `(state, sessionId, targetGroupId?)` | 已打开则选中/聚焦；否则插入目标 group 的 selected 之后 | group-missing、tab-limit |
| `openDraft` | `(state, draft, targetGroupId?)` | 插入/选中一个 Draft tab；不创建 session | group-missing、tab-limit |
| `replaceDraft` | `(state, draftId, sessionId)` | 把 Draft tab 原子替换为 session tab；若该 session 已打开则合并聚焦 | draft-missing |
| `selectTab` | `(state, groupId, tabId)` | 选中并聚焦 | group/tab missing |
| `focusGroup` | `(state, groupId)` | 只改 attention | group missing |
| `closeTab` | `(state, tabId)` | 移除；selected 回退前一个/后一个；空 group 保留 | tab missing |
| `closeOtherTabs` | `(state, groupId, keepTabId)` | 只留一个 | group/tab missing |
| `moveTab` | `(state, tabId, destGroupId, index)` | 同 group 重排或跨 group 移动 | group/tab missing、same-position |
| `splitGroup` | `(state, groupId, spec, tabId?)` | 把 group 换成 split；有 tabId 时移入新 group | group/tab missing、center-limit |
| `removeGroup` | `(state, groupId)` | 删除叶子并折叠父 split；tabs 移到最近兄弟 | group missing、last-group |
| `resizeSplit` | `(state, splitId, weights)` | 夹紧/归一化；shapeChanged=false | split missing |
| `setTabMode` | `(state, tabId, mode)` | `chat`/`trajectory`；不改变 topology | tab missing |
| `setPreset` | `(state, presetId)` | 替换 frame，重分配现有 tab | unknown-preset |
| `capturePreset` | `(state)` | 返回资源无关 frame 模板 | 无 |
| `reconcileSessions` | `(state, deletedSessionIds)` | 移除已删除 session 的 tab | 无 |
| `normalize` | `(raw)` | 解码/修复/回退 | 无（内部） |

### 6.3 关键语义

**openSession。** 目标 group 默认 `attention.focusedGroupId`；若 tab 已存在，只在那个 group 选中/聚焦，不复制。插入位置：selected 之后，否则末尾。每 group 16、全工作区 32 的上限在 `openSession`/`openDraft`/`replaceDraft` 都要检查；超出返回 `tab-limit`。

**openDraft / replaceDraft。** Draft tab 是新会话的唯一入口，不再用 `currentSession=''` 表达“新聊天”；Draft tab 初始 `mode=chat`。首次发送时先 `POST /api/session-id`，成功后 `replaceDraft` 把 draft tab 换成 session tab并保留 `mode`；失败则保留 draft 和草稿。若该 session 已被其他 tab 打开，合并到已有 tab 并关闭 draft，避免同一 session 两个 composer。

**closeTab。** 关闭 selected 时选前一个，没有再选后一个；group 空则 `selectedTabId=undefined`，pane 显示 EmptyPane。关闭非 selected 不影响 selected。**关闭 tab 不删除 session**，也不取消后台 run。运行中的 tab 关闭不弹阻塞确认，只给一次非阻塞提示“任务将在后台继续”；Sidebar 仍可重新打开。

**moveTab。** `index` 是移除源 tab 后的目标下标，夹紧到 `[0, dest.tabs.length]`；同 group 同位置或原位置+1 返回 `same-position`。源 group 变空保留。

**splitGroup。** `direction: horizontal` 左右，`vertical` 上下；`side: before|after`。新 group 先加入 frame/view（空 view），再移动 tab，避免 `moveTab` 目标 group 不存在。4 叶上限。

**removeGroup。** 优先前兄弟、再后兄弟；tabs 追加到目标 group 末尾，保留目标 selected；最后一个 leaf 返回 `last-group`；focused 指向被删 group 时改为目标。

**setPreset。** 按当前 frame 深度优先收集 tabs，再按新 frame 深度优先分配；每个 leaf 至少一个（若 tab 足够）；每个 tab 的 `mode` 随 tab 保留；焦点 session 所在 leaf 成为 focused；不关闭 session。

**setTabMode。** 只改目标 tab 的 `mode`；不触发 shapeChanged；用于 `SessionHeader` 的 Chat/Trajectory 切换。桌面与移动端都可用；空 group 没有 tab，不调用。

**normalize。** `null`/版本不符 → `defaultState()`；重复 id / 孤儿 view / 缺失 view / 重复 sessionId / selected 不存在 / 非法 weights / >4 叶 / 非法 tab.mode / 超长 id/workDir 逐项修复；不可修复回退默认。修复 warnings 只在测试/诊断可见。

### 6.4 运行时状态

```js
/**
 * @typedef {{
 *   state: WorkbenchState,
 *   projectionEpoch: number,
 *   navigationClock: Record<string, number>
 * }} WorkbenchRuntime
 */
```

- `projectionEpoch` 在 `shapeChanged` 时 +1；`resizeSplit`/`selectTab`/`focusGroup`/`setTabMode` 不增加。
- 拖拽开始时记录 epoch，commit 前比对；变了就取消并恢复。
- 延迟 `ensureSessionLoaded` 完成后，若目标 group 已删或 clock 变了，不抢焦点。
- runtime 字段不持久化。

### 6.5 伪代码：openSession

```js
export function openSession(state, sessionId, targetGroupId = state.attention.focusedGroupId) {
  const existing = findSessionTab(state.view, sessionId);
  if (existing) {
    return {
      ok: true,
      state: {
        ...state,
        view: selectInGroup(state.view, existing.groupId, existing.tab.id),
        attention: focus(state.attention, existing.groupId)
      },
      focus: { groupId: existing.groupId, tabId: existing.tab.id },
      shapeChanged: false
    };
  }
  if (!findGroup(state.frame.center, targetGroupId)) {
    return { ok: false, reason: { code: "group-missing", groupId: targetGroupId } };
  }
  const tabs = state.view.groups[targetGroupId]?.tabs ?? [];
  if (tabs.length >= MAX_TABS_PER_PANE) {
    return { ok: false, reason: { code: "tab-limit", limit: MAX_TABS_PER_PANE } };
  }
  const tab = { kind: "session", id: `session:${sessionId}`, sessionId, mode: "chat" };
  const group = state.view.groups[targetGroupId] ?? { tabs: [] };
  const at = group.selectedTabId
    ? group.tabs.findIndex((item) => item.id === group.selectedTabId) + 1
    : group.tabs.length;
  return {
    ok: true,
    state: {
      ...state,
      view: {
        ...state.view,
        groups: {
          ...state.view.groups,
          [targetGroupId]: {
            ...group,
            tabs: [...group.tabs.slice(0, at), tab, ...group.tabs.slice(at)],
            selectedTabId: tab.id
          }
        }
      },
      attention: focus(state.attention, targetGroupId)
    },
    focus: { groupId: targetGroupId, tabId: tab.id },
    shapeChanged: false
  };
}
```

### 6.6 伪代码：splitGroup

```js
export function splitGroup(state, groupId, spec, tabId) {
  if (countLeaves(state.frame.center) >= MAX_CENTER_GROUPS) {
    return { ok: false, reason: { code: "center-limit", limit: MAX_CENTER_GROUPS } };
  }
  const group = findGroup(state.frame.center, groupId);
  if (!group) return { ok: false, reason: { code: "group-missing", groupId } };

  const newGroup = { kind: "group", id: nextGroupId(state) };
  const split = {
    kind: "split",
    id: nextSplitId(state),
    direction: spec.direction,
    weights: [0.5, 0.5],
    children: spec.side === "before" ? [newGroup, group] : [group, newGroup]
  };
  const frame = {
    ...state.frame,
    center: replaceGroupWithSplit(state.frame.center, groupId, split)
  };
  const view = ensureGroupViews(state.view, collectLeaves(frame.center));
  const expanded = { ...state, frame, view, attention: focus(state.attention, newGroup.id) };

  if (tabId) {
    const moved = moveTab(expanded, tabId, newGroup.id, 0);
    if (!moved.ok) return moved;
    return { ...moved, shapeChanged: true, focus: { groupId: newGroup.id, tabId } };
  }
  return {
    ok: true,
    state: expanded,
    focus: { groupId: newGroup.id },
    shapeChanged: true
  };
}
```

`replaceGroupWithSplit` 递归复制路径，不修改入参；Svelte store 的引用比较依赖不可变更新。

## 7. 会话流与 ChatPane 重构（核心）

### 7.1 现状：一个消费者、一个可见会话

`Chat.svelte` 里的这段是全局事件入口：

```js
$: {
  const pendingEvents = $runEvents.filter((item) => Number(item?.wsSeq || 0) > runEventCursor);
  if (pendingEvents.length > 0) {
    runEventCursor = ...;
    for (const item of pendingEvents) {
      if (item?.type !== "session_event" || !item.sessionId) continue;
      handleSessionStreamEvent(item.sessionId, { event: normalizedEvent, data: ... });
    }
  }
}
```

`handleSessionStreamEvent(id, event)` 内部已经按 `id` 处理：

- 如果 `id === $currentSession`：`applySessionViewReducer` 把结果写进组件局部变量（`messages`、`chatEvents`、`sessionRuntimeValue`...）。
- 否则：写进 `sessionRunStates[id]`，不碰 DOM。

这意味着“后台会话的**实时事件**已经是完整的”，只是没有第二个 `Chat` 组件来画它；历史仍需 `ensureSessionLoaded`。多 pane 的最小改动不是重写 reducer，而是：

1. **把这段事件消费抽到 `lib/session-stream.js`**，由它统一更新 `sessionRunStates`；`ChatPane` 不再消费 `$runEvents`。
2. **让每个 `ChatPane` 都从 `$sessionRunStates[sessionId]` 读取视图**，而不是用局部 `messages` 作为唯一源。
3. 局部变量只保留真正属于 UI 的东西：`prompt`、`pendingAttachments`、滚动位置、弹窗开关、命令建议。

### 7.2 `session-stream.js` 设计

```js
// ui/src/lib/session-stream.js
// 伪代码：只负责“runEvents -> sessionRunStates + effects”，不碰 DOM。
import { get } from "svelte/store";
import { runEvents } from "./stores.js";
import { sessionRunStates, getSessionState, updateSessionState } from "./session-runs.js";
import { t } from "./preferences.js";

// 只在 App 启动时建立一次；首次从 0 归约当前缓冲区，之后严格按 wsSeq 去重。
let cursor = 0;
let visibleSessionIds = new Set();

export function setVisibleSessions(ids) {
  visibleSessionIds = new Set(ids);
}

export function startSessionStreamDispatcher() {
  return runEvents.subscribe((items) => {
    const pending = items.filter((item) => Number(item?.wsSeq || 0) > cursor);
    if (!pending.length) return;
    cursor = Number(pending[pending.length - 1].wsSeq) || cursor;
    // 首次启动从 0 开始；重复启动由 App 的单一 startSessionStreamDispatcher 保证。
    for (const item of pending) {
      if (item?.type !== "session_event" || !item.sessionId) continue;
      applySessionStreamEvent(item.sessionId, item, visibleSessionIds.has(item.sessionId), get(t));
    }
  });
}

function applySessionStreamEvent(sessionId, item, visible, tr) {
  // 每个分支：updateSessionState(sessionId, (state) => sessionStateWithView(
  //   state, reducer(viewFromSessionState(state), ...)))，并把副作用写成 state.effects。
  // 不直接调用 scroll/打开 modal/写 activeApproval；这些由 ChatPane 消费 effects。
}
```

**事件契约（必须逐项从 `Chat.svelte:2176` 迁移，不能只迁移 transcript）：**

| 事件 | state 写入 | effects / UI 归属 |
| --- | --- | --- |
| `transcript` / `tool_event` | `reduceTranscriptEvent` / `reduceToolStatusEvent` | `scrollEpoch`、`subAgentEpoch`；历史窗口过滤用该 session 的 `earliestSeq` |
| `run_event` / `status` / `error` | `reduceRunEvent` / `reduceStreamError` | `historyReloadEpoch`、`statsRefreshEpoch`、`errorNotice` |
| `done` | `reduceStreamDone` | `historyReloadEpoch`、`statsRefreshEpoch`、`refreshEpoch` |
| `runtime_event` | `reduceRuntimeSnapshot` | `runtimeDisplayMode` 由 pane 派生；`sessions` 列表更新仍由 `stores.js` 负责 |
| `capability_event` | `reduceCapabilityEvent` | 无 |
| `approval_request` / `approval_resolved` | `reduceApprovalRequest` / `reduceApprovalResolved` | `approvalEpoch` + `approvalId`；`recordApprovalResolution` 可在 dispatcher 侧做（非 DOM）；`activeApproval/showApprovalCenter/selectedApprovalID/dismissedApprovalIDs` 是 pane UI |
| `question_request` / `question_resolved` | `reduceQuestionRequest` / `reduceQuestionResolved` | `questionEpoch` + `questionId`；`selectedQuestionID` 是 pane UI |

关键点：

- `session-stream.js` 是**唯一**消费 `runEvents` 的地方；`App.svelte` 在 `startApp()` 中、`connectRuns()` 之前启动一次，`onDestroy` 停止。**W0 之后始终启动**；`workbenchEnabled` 已移除，不再控制事件消费者。**不保留旧 Chat 的 `$runEvents` 消费路径**；回滚只回退布局。
- 启动顺序：首次启动从 0 归约当前 `runEvents` 缓冲区，之后严格按 `wsSeq` 去重；由 App 保证 dispatcher 只启动一次。
- `visibleSessionIds` 由 workbench 通过 `setVisibleSessions(ids)` 显式更新；它只影响 effects，不影响事件归约。
- SSE fallback（`/api/sessions/:id/stream`）仍由 `session-stream.js` 管理，按 session 注册 observer；不能由多个 `ChatPane` 各自开 SSE。**observer 的 abort/stopObserver 所有权也在这里**；`ChatPane` 的 `onDestroy` 不得清理全局 `sessionRunStates[*].observer`。
- `runEvents` 的 `wsSeq` 是单调的；dispatcher 用模块级 cursor，避免每个 pane 各自算。
- `stores.js` 里对 `runtime_event` 更新 `sessions` 列表（running/execution 徽标）是元数据投影，不是第二条 session view 归约；`session-stream.js` 仍是唯一把事件写进 `sessionRunStates` 的模块。
- 事件归一化（`started/finished/...` → `run_event`）沿用 `Chat.svelte` 现有逻辑。
- **隐藏 tab 的 runtime 轮询**由 dispatcher/共享 registry 按 sessionId 负责，不能只随 `ChatPane` 挂载启停；否则后台 responses-run 重连会延迟到重新选中。

### 7.3 `sessionRunStates` 扩展

现有字段已经足够渲染；增加少量控制字段：

```js
{
  ...emptySessionState(sessionId),
  status: "idle" | "loading" | "ready" | "stale" | "failed",
  loading: false,          // ensureSessionLoaded in-flight
  loadedAt: 0,
  lastVisibleAt: 0,        // LRU
  earliestSeq: null,       // 已加载历史窗口的最早 seq
  hasMore: false,          // 是否还有更早历史
  historyAutoLoadReady: false,
  pendingReload: false,     // force 在 loading 中到达，加载结束后补一次
  lastHandledHistoryReloadEpoch: 0, // 按 session 防止重挂载重复消费
  modelOverride: "",       // 该 session 的发送模型；空则回退 runtime/config
  providerOverride: "",
  effects: {                // UI-only, consumed by ChatPane
    scrollEpoch: 0,         // 新消息/强制滚动
    historyReloadEpoch: 0,  // done/重连后重新拉取历史
    statsRefreshEpoch: 0,   // done 后刷新列表/统计
    refreshEpoch: 0,        // 通用刷新
    subAgentEpoch: 0,       // 子代理变化
    approvalEpoch: 0,       // 新 approval；配合 approvalId
    questionEpoch: 0,       // 新 question；配合 questionId
    approvalId?: string,
    questionId?: string,
    errorNotice?: string
  }
}
```

`reduceTranscriptEvent` 等在 `session-view.js` 里返回 `{ view, effects }`；`session-stream.js` 把 `effects` 转成 `effects.scrollEpoch++` 等，不直接在全局 DOM 上操作。`ChatPane` 用 `$: if (state.effects.scrollEpoch !== lastScrollEpoch) ...` 调用自己的 `scrollChatToBottom()`。这样每个 pane 的滚动是独立的。

**pane 级决策状态仍留在 ChatPane，不进入 sessionRunStates：**

- `activeApproval` / `showApprovalCenter` / `selectedApprovalID` / `dismissedApprovalIDs`：只影响当前 pane 的审批 UI；`AttentionBell` 从所有 pane 的 `runtime.pendingApprovals` 汇总数量，不依赖全局 `activeApproval`。
- `selectedQuestionID`：同理，按 pane/session 保存。
- `runtimeDisplayMode`（code/work）：由该 pane 的 `state.runtime.displayMode` 派生；不要因为后台事件改所有 pane。
- `recordApprovalResolution` 是全局 approval 历史投影，可在 dispatcher 侧调用；但它不能决定哪个 pane 打开模态。
- `done` 后的历史重载：dispatcher 设置 `historyReloadEpoch`；`ChatPane` 消费后调用 `ensureSessionLoaded(id, { force: true })`，但**只有当前处于“跟随底部”时才自动滚动**；用户已向上滚动时静默合并并保持锚点。不要在 dispatcher 里直接调用 Chat 的局部 `loadSessionMessages`。
- `refreshSessions`/`refreshStatsSummary`：属于 stores 层，可由 dispatcher 在 `done` 时调用；不要阻塞 reducer。

历史窗口状态必须进 store，不能继续留在 `Chat.svelte` 局部：现有 `earliestSeq`/`hasMoreHistory`/`historyAutoLoadReady`/`historyLoadVersion` 与 `isOlderThanLoadedHistory` 是按“当前会话”写的；多 pane 后后台 session 的 replay 帧需要自己的窗口边界，否则会把旧消息插进分页区域。

### 7.4 `ensureSessionLoaded(sessionId, { force = false } = {})`

每个打开的 tab 首次成为可见/被选中时加载；`done`/重连后的 `historyReloadEpoch` 用 `force: true` 重新对齐历史：

```js
export async function ensureSessionLoaded(sessionId, { force = false } = {}) {
  const state = getSessionState(sessionId);
  if (!sessionId || state.loading || (!force && state.status === "ready")) return;
  updateSessionState(sessionId, (s) => ({ ...s, loading: true, status: "loading" }));
  try {
    const { messages, hasMore } = await getSessionMessagesLatest(sessionId, 50);
    // 归一化、merge、写 state.messages / hasMore / historyLoaded
    // 并行/串行加载 run-events、capability-events、runtime、subagents
    updateSessionState(sessionId, (s) => ({ ...s, loading: false, status: "ready", loadedAt: Date.now() }));
  } catch (err) {
    updateSessionState(sessionId, (s) => ({
      ...s, loading: false, status: "failed", lastError: normalizeErrorInfo(err)
    }));
  }
}
```

- **去重**：`loading`/`status` 防止多个 pane 同时对同一 session 发起加载；`force` 只跳过 `ready` 检查，不跳过 `loading`。若 `force` 在 `loading` 中到达，记 `pendingReload=true`，当前加载结束后再补一次，避免 done 恰好落在加载中时丢刷新。
- **epoch 消费**：`historyReloadEpoch` 的已处理值按 session 存在 `sessionRunStates`，不要只放在组件局部；否则 pane 重新挂载会把旧 epoch 再消费一次。
- **分页**：`loadOlderSessionMessages(sessionId)` 使用 `getSessionMessagesBefore`，按 session 写入；不再依赖 `$currentSession`。
- **stale**：非可见且非 running 的 session 若被淘汰，重开时 `ensureSessionLoaded` 重新拉取。
- **加载优先级**：可见 pane > running > 最近聚焦；一次最多并发 2–3 个，避免页面一开 20 个 tab 同时打满 API。
- **历史窗口**：沿用 50 条/页 + 顶部自动加载；每个 state 保存 `hasMore`/`earliestSeq`，`isOlderThanLoadedHistory` 改为读该 session 的 state，而不是组件局部变量。

### 7.5 `ChatPane` 抽取

目标：把 `views/Chat.svelte` 变成：

```text
views/Chat.svelte                 # 兼容路由的薄 wrapper：<ChatPane sessionId={$currentSession} />
components/chat/ChatPane.svelte   # 真正的内容；props: sessionId, active
components/chat/DraftPane.svelte  # 新会话；props: draftId, active
components/chat/ComposerPane.svelte # 两者共用：composer/附件/模型选择
```

`ChatPane` 的改动：

- 所有 `$currentSession` 目标改为 `sessionId` prop；所有 `$currentSession === id` 改为 `id === sessionId`。
- 视图来源从局部 `messages/chatEvents/sessionRunEvents/sessionRuntimeValue` 改为 `$sessionRunStates[sessionId]` 的派生选择（见 7.6）；局部只保留 `prompt`、`pendingAttachments`、`scrollTop`、弹窗、命令建议、`showRuntimePanel` 等 UI 状态。
- 模型/provider 改为 **per-pane**：`selectModel`/provider 变更写该 session 的 `modelOverride`/`providerOverride`，并持久化到独立的 `mothx.session-prefs.v1`（按 sessionId）；发送时按 `override → runtime → catalog default` 解析，禁止继续从全局 `$selectedModel` 取发送模型，否则两个 pane 会互相覆盖。刷新后恢复上次选择；session 删除时清理对应 key。
- `lib/chat-runtime-state.js` 的 `createSessionRuntimeManager` 不再依赖 `deps.currentSession()`：每个 `ChatPane` 传固定 `sessionId` 创建 manager，或改成按 session 注册的共享 registry；**轮询必须按 session 注册，不能只随 pane 挂载启停**，否则隐藏 tab 的 responses-run 重连会延迟。`onPersist` 回调删除。
- 新会话由 `DraftPane.svelte` 承载，`ChatPane` 与 `DraftPane` 共用 `ComposerPane.svelte`（composer、附件、模型/provider 选择、发送按钮）；`ChatPane` 的 props 是 `sessionId/active`，`DraftPane` 的 props 是 `draftId/active`。首次发送走 `session-actions.allocateSession()` + `workbench.replaceDraft(draftId, sessionId)`；替换后 DraftPane 卸载，ChatPane 接管同一 tab，不创建第二条执行路径。
- `applySessionViewReducer` 的“当前会话写局部”分支删除；所有 reducer 结果写 `sessionRunStates`。`session-stream.js` 做归约；`ChatPane` 只读。
- `persistLocalSessionState`/`restoreLocalSessionState` 删除；不再有“切走前搬运”。
- `busy/canStop` 从 `$sessionRunStates[sessionId].runtime` + `completion` 计算。
- `sendPrompt`、`stop`、`approval`、`question`、`applyConfig`、`attach` 等显式传 `sessionId` 或读取当前 pane 的 state；不读 `$currentSession`。
- `SessionHeader` 的 `sessionID`、`view`、`onViewChange` 已经是 props；只需调用方按 selected tab 传入。`view` 来自 `selectedTab.mode`，`onViewChange` 调 `workbench.setTabMode(tabId, mode)`。
- **DOM id 和全局监听必须 pane 级隔离。** 现有 `chat-command-suggestions`（3444）、`session-runtime-panel`（3496）是固定 id；`ChatPane` 要加 `paneId` 前缀并用 `aria-controls` 指向自己的元素。`<svelte:window on:keydown={handleLightboxKeydown}>`（3645）和 `document.addEventListener('pointerdown', ...)`（293）不能每个 pane 各自全局注册；改为元素级监听，或用 `active` prop 只让焦点 pane 响应。
- **`onDestroy` 不得清理共享 observer。** 当前 `Chat.svelte:301` 会遍历所有 `sessionRunStates` 并 abort observer；迁移后这段必须删除，observer 生命周期归 `session-stream.js`。
- `TrajectoryView` 的 `sessionID` 用 prop；其内部已按 session 存 `trajectoryState`/`trajectoryViewState`，多 pane 可并存。
- 每个 pane 的审批/提问 UI 读 `$sessionRunStates[sessionId].runtime.pendingApprovals` / pending question；全局 `activeApproval` 只保留给“焦点 pane 的模态/当前选中请求”，并由 `AttentionBell` 汇总所有 pane 的 pending 数量，避免后台审批被埋。
- `sessionRuntime` 全局 store 只保留给非工作台的单会话兼容路径；`ChatPane` 不依赖它，直接用 per-session state。

### 7.6 每个 pane 的派生订阅

Svelte legacy store 不能直接 `$sessionRunStates[sessionId]` 细粒度订阅；用 `derived` + 缓存：

```js
// ui/src/lib/session-store.js
import { derived } from "svelte/store";
import { sessionRunStates } from "./session-runs.js";

const cache = new Map(); // sessionId -> derived store

export function sessionStateStore(sessionId) {
  if (!cache.has(sessionId)) {
    cache.set(sessionId, derived(sessionRunStates, ($states) => $states[sessionId] || null));
  }
  return cache.get(sessionId);
}
```

`ChatPane`：

```svelte
<script>
  export let sessionId;
  const state = sessionStateStore(sessionId);
  $: view = $state ? viewFromSessionState($state) : emptySessionView();
</script>
```

- 只有该 session 的 `sessionRunStates[id]` 引用变化时，`derived` 才通知该 pane；其他 session 的流不会让这个 pane 重渲染。
- `sessionStateStore` 的 cache 必须在 tab 关闭/淘汰时删除（`forgetSessionStateStore(sessionId)`），否则长期打开大量 session 会留下空订阅。
- 布局 store 与 `sessionRunStates` 分开：拖拽/resize 不触发 ChatPane。
- `ChatPane` 内部对 `messages`/`toolEvents` 的渲染沿用现有 `{#each}` 和纯函数；不再把整个 `$runEvents` 传下去。

### 7.7 Composer 草稿、滚动与弹窗

- `prompt`/`pendingAttachments` 现在是 `ChatPane` 局部状态；**同一 pane 切换 tab 会卸载 ChatPane**，文本和附件都会丢。新增 `lib/composer-drafts.js`：
  - `Map<sessionId|draftId, { text, attachments, updatedAt }>`；文本可持久化；附件只保存**已上传的 providerRef 和展示元数据**（name/mediaType/size），不保存 `File`/blob/dataUrl；
  - 只有 `providerRef` 的附件能跨刷新恢复；仍停留在 dataUrl 的待发送附件只保留在内存 Map 中，切 tab 不丢、刷新后会提示重新选择；
  - `ChatPane`/`DraftPane` 挂载时读取，`oninput` 同步写 Map（不触发 store），发送/清空时删除；
  - LRU 上限 16、单条文本上限 16KB；持久化到独立的 `mothx.composer-drafts.v1`，不要混进布局文档；
  - 多用户同 origin 时这是用户输入内容，`logout` 可清理，但文档必须说明它不进入服务端。
- 滚动位置：`Map<sessionId, { scrollTop, following }>`；`ChatPane` 挂载恢复。SessionSnapshot 不进入 v1；不做额外的只读滚动精度。
- 弹窗（runtime panel、subagent modal、MCP config、attachment lightbox、approval center）是 pane 局部 UI；不要用全局单例，否则两个 pane 会互相打开。审批中心可以保留一个全局入口 `AttentionBell.svelte`，列出所有 pane/tab 的 pending decision 并一键聚焦；但具体审批卡片仍在所属 pane 内。
- 关闭运行中的标签不弹阻塞确认，只提示“任务将在后台继续”，并保留 Sidebar/tab 徽标；易用性优先，但后台 run 不被前端取消。
- `document.title`/StatusBar 只显示焦点 pane 的 session；后台 running/pending 数量用徽标表达，不改变焦点。

### 7.8 Lite 方案（非 v1 交付，实验降级）

Lite 不属于本方案验收；只有在 W0 严重延期且明确另立分支时才允许使用：

- `App.svelte` 在 chat 路由挂载 `Workbench.svelte`。
- 只有焦点 pane 渲染 `<Chat sessionId={focusedSessionId} />`（现有 Chat，少量改动接受 `sessionId` prop 或继续读 `currentSession`）。
- 其他 pane 渲染 `SessionSnapshot.svelte`：从 `$sessionRunStates[id]` 读取 `messages/toolEvents/runEvents`，用 `session-view.js` 的纯函数生成只读转录；顶部显示状态/审批数量，点击“聚焦”把该 session 设为 `currentSession`。
- `SessionSnapshot` 首次挂载时调用轻量 `ensureSnapshotLoaded(id)`（`getSessionMessagesLatest` + `getSessionRunEvents` + `getSessionSubAgents`），按 session 去重；否则刚打开的只读 pane 只能看到“页面打开之后”的事件，历史为空。
- Lite 不依赖“焦点 Chat 常驻挂载”：必须先落地 `session-stream.js`，再让只读快照从 store 渲染；否则切 tab 会停归约。
- 风险：只读 pane 不能直接发消息/审批；用户需先聚焦。适合“看两个 agent 并排跑”的临时形态。
- 升级路径：W0 完成后用 `ChatPane` 替换 `SessionSnapshot`，删除降级分支。

### 7.9 迁移与测试

**迁移顺序：**

1. 先加 `session-stream.js`，把 `Chat.svelte` 的 `$runEvents` 消费替换为对 `sessionRunStates` 的读取；单会话行为不变。此阶段必须覆盖 `assistant_delta` 只消费一次（重复消费会把文本追加两遍）。
2. 抽 `ChatPane.svelte`，`Chat.svelte` 变成 wrapper；把 `$currentSession` 替换为 `sessionId`，模型/provider/历史窗口改为 per-session。
3. 加 `Workbench.svelte` 与标签；先单 pane，再分屏；桌面和移动端分别投影。
4. 加分屏/持久化/窗口缩放/Draft tab/AttentionBell。
5. 删除旧局部搬运代码与 `sessionRuntime` 单例依赖。

**测试：**

- `session-stream.test.js`：两个 session 交错事件，各自 state 正确；重复 `wsSeq` 不重复归约；`assistant_delta` 不会双写；SSE fallback 只开一个；approval/question effects 不丢失；`done` 产生 `historyReloadEpoch`；隐藏 session 的 runtime 轮询仍运行。
- `session-state.test.js`：`ensureSessionLoaded` 去重、stale 重载、LRU 淘汰 running 不淘汰；历史窗口 `earliestSeq/hasMore` 按 session 隔离。
- `model.test.js`：`openDraft`/`replaceDraft`、per-pane model/provider、同 session 去重、运行中关闭 tab 不取消 run。
- **DOM/生命周期回归**（e2e 或源码边界检查，不依赖 jsdom）：关闭一个 `ChatPane` 不 abort 其他 session 的 observer；多 pane 下固定 id 带 pane 前缀；window keydown 只影响焦点 pane；workbench 开关切换不出现 `assistant_delta` 双写。
- `ChatPane` 不读 `$currentSession`、不直接消费 `$runEvents`、不从全局 `$selectedModel` 取发送模型的边界检查。仓库目前没有 source-scan 先例；建议用 import 边界测试或新增一个明确的 AST/文本扫描测试，而不是声称“沿用现有风格”。
- 现有 `session-view.test.js`/`stores.test.js`/`session-runs` 相关测试更新。

## 8. 渲染器设计（Svelte）

### 8.1 组件树

```text
App.svelte
└─ .view-container
   └─ Workbench.svelte（仅 /chat 且开关开启）
      ├─ WorkbenchToolbar.svelte（可选：预设、布局菜单）
      ├─ SplitView.svelte (node = frame.center)
      │  ├─ SplitView (child 0)
      │  │  ├─ Pane.svelte (g1)
      │  │  │  ├─ TabStrip.svelte
      │  │  │  ├─ SplitHandle.svelte（与兄弟 pane 之间）
      │  │  │  └─ PaneBody:
      │  │  │     ├─ ChatPane.svelte (sessionId)  ─┐
      │  │  │     ├─ DraftPane.svelte (draftId)     ├─ 共用 ComposerPane.svelte
      │  │  │     └─ 或 SessionSnapshot.svelte   ─┘
      │  │  └─ ...
      │  └─ SplitView (child 1)
      ├─ EmptyPane.svelte
      └─ DragLayer.svelte（拖拽预览/投放提示）
```

`Workbench.svelte` 不直接 import `Chat.svelte`；它接收 `renderPane`/`renderSnapshot` 回调（或在 integration 层做 `{#if}`），避免布局组件与 184KB 聊天组件耦合。

### 8.2 `Workbench` props

```svelte
<script>
  export let state;                    // WorkbenchState（只读）
  export let focusedGroupId = "";
  export let maxCenterGroups = 4;
  export let renderPane;               // (tab, group, active) => SvelteComponent
  export let renderEmptyPane;          // (group) => SvelteComponent
  export let onCommit;                 // (nextState, reason) => void
  export let onFocusGroup;
  export let onSelectTab;
  export let onOpenSession;
  export let onCloseTab;
  export let onSplit;
  export let onResize;
  export let onSetMode;
  export let onGestureCanceled;
</script>
```

- `state` 是纯数据；`Workbench` 自己不保存 committed layout，只保存 drag/resize 草稿。
- `renderPane` 由 `App.svelte` 或 `ChatWorkbenchHost.svelte` 传入，内部决定 `ChatPane` 还是 `SessionSnapshot`。
- 所有布局操作调用 `model.js`，成功后 `onCommit`；失败只 toast/tooltip，不改 state。

### 8.3 分屏渲染与 resize

不使用第三方 split pane 库；用 CSS flex + 自写 `SplitHandle`：

```svelte
<!-- SplitView.svelte -->
<script>
  export let node;
  export let onResize;
  export let onSplit;
  let dragging = false;
  let draft = node.kind === "split" ? [...node.weights] : [0.5, 0.5];
  let start = null;
  let host;

  // 外部提交/回退后同步草稿；拖拽中不覆盖用户当前手势。
  $: if (!dragging && node.kind === "split") draft = [...node.weights];

  function pointerdown(event) {
    if (node.kind !== "split") return;
    dragging = true;
    start = { x: event.clientX, y: event.clientY, weights: [...node.weights], size: host.getBoundingClientRect() };
    event.currentTarget.setPointerCapture(event.pointerId);
  }

  function pointermove(event) {
    if (!dragging || !start) return;
    const horizontal = node.direction === "horizontal";
    const total = horizontal ? start.size.width : start.size.height;
    const delta = horizontal ? (event.clientX - start.x) / total : (event.clientY - start.y) / total;
    draft = clampWeights([start.weights[0] + delta, start.weights[1] - delta]);
  }

  function pointerup(event) {
    if (!dragging) return;
    dragging = false;
    onResize(node.id, draft);
    event.currentTarget.releasePointerCapture?.(event.pointerId);
  }
</script>

{#if node.kind === "group"}
  <slot name="pane" group={node} />
{:else}
  <div class="wb-split wb-split-{node.direction}" bind:this={host}>
    <div class="wb-split-child" style={`flex: ${draft[0]} 1 0%`}>
      <svelte:self node={node.children[0]} {onResize} {onSplit}>
        <slot name="pane" slot="pane" />
      </svelte:self>
    </div>
    <SplitHandle
      direction={node.direction}
      {dragging}
      on:pointerdown={pointerdown}
      on:pointermove={pointermove}
      on:pointerup={pointerup}
      on:dblclick={() => onResize(node.id, [0.5, 0.5])}
      on:keydown={(event) => keyboardResize(event, draft, (next) => onResize(node.id, next))}
    />
    <div class="wb-split-child" style={`flex: ${draft[1]} 1 0%`}>
      <svelte:self node={node.children[1]} {onResize} {onSplit}>
        <slot name="pane" slot="pane" />
      </svelte:self>
    </div>
  </div>
{/if}
```

- `draft` 只在本组件内；pointerup 才 `onResize`，不写全局 store。
- `flex: weight 1 0%` 让两个 pane 按比例分配；`min-width/min-height: 0` 允许收缩。
- 分隔条 8–10px 命中区，视觉 1px；`touch-action: none`。
- 键盘：左右/上下箭头按 5%/10% 调整，Home/End 到 20%/80%；`role="separator"`、`aria-orientation`、`aria-valuenow/min/max`。
- 双击恢复 50/50。
- 成员变化（split/merge）时，`{#key node.id}` 只保证同一节点不重建；分屏树变化会重建受影响子树，但会话数据在 `sessionRunStates`，不会丢消息。**不要**把 Svelte 组件实例当作会话状态容器。
- 移动端投影：`isMobile` 下不渲染分屏结构，只渲染单 pane + 标签条；桌面不因为移动端开关改变 frame 渲染逻辑，具体见 8.6。

### 8.4 标签条与拖拽

`TabStrip.svelte`：

- 标签为 `role="tab"`，`aria-selected`，`aria-controls` 指向 pane body；`tabindex` 采用 roving focus（只有 active 标签 `tabindex=0`）。
- 标签宽度 120–220px，超出省略；溢出用“更多”按钮 + 可搜索列表（复用现有 `SearchSelect` 或简单 popover）。
- 关闭按钮：hover/active 显示；中键关闭；右键菜单（关闭、关闭其他、关闭右侧、在 Sidebar 显示、复制 id、向右/向下分屏）。
- 状态点：复用 Sidebar 的 running/pending 颜色语义；`ChatPane`/snapshot 只读 `sessionRunStates[id].runtime`。

拖拽：**原生 Pointer Events**，不引入依赖：

```js
// ui/src/lib/workbench/drag.js
export function beginTabDrag(event, { tab, sourceGroupId, state, onPreview, onCommit, onCancel }) {
  const pointerId = event.pointerId;
  const start = { x: event.clientX, y: event.clientY };
  let active = false;
  let target = null;

  const move = (e) => {
    if (!active && Math.hypot(e.clientX - start.x, e.clientY - start.y) < 6) return;
    active = true;
    target = hitTestDropTarget(e, { tab, sourceGroupId, state });
    onPreview(target); // 纯函数 previewDrop 的结果；非法则不画
  };
  const up = (e) => {
    cleanup();
    if (!active || !target) return onCancel();
    onCommit(target);
  };
  const cancel = () => { cleanup(); onCancel(); };
  // pointermove/up/cancel 挂在 window；Escape 取消
}
```

- 投放目标：标签条插入位置、pane 中心（移动）、pane 四边（分屏）、空 pane。
- `previewDrop(state, drag, target)` 返回 `LayoutResult`；UI 只在 `ok` 时画提示，drop 也只提交这个结果。
- 拖拽期间 `Workbench` 根持有 `dragState`，不写 store；源标签加半透明/拖拽副本。
- 不做触摸拖拽；PC 鼠标/触控板使用 Pointer Events。
- 不做跨浏览器窗口拖拽。

### 8.5 标签/pane 操作的 UI 映射

| UI | 纯操作 |
| --- | --- |
| Sidebar 点击会话 | `openSession(sessionId)` |
| 点击标签 | `selectTab(groupId, tabId)` |
| 关闭标签 | `closeTab(tabId)` |
| 右键“关闭其他” | `closeOtherTabs(groupId, tabId)` |
| 拖拽重排 | `moveTab(tabId, sameGroup, index)` |
| 拖拽到另一 pane | `moveTab(tabId, destGroup, end)` |
| 拖拽到 pane 边缘 | `splitGroup(groupId, {direction, side}, tabId)` |
| pane 菜单“向右/向下分屏” | `splitGroup(groupId, {...})` |
| pane 菜单“关闭窗格” | `removeGroup(groupId)`（有 tab 时确认移到兄弟） |
| 分隔条拖动 | `resizeSplit(splitId, weights)` |
| Chat/Trajectory | `setTabMode(tabId, mode)` |
| 应用预设 | `setPreset(presetId)` |
| 删除 session 成功 | `reconcileSessions(state, [id])` |

### 8.6 响应式：桌面分屏 / 移动单 pane

- `Workbench` 用 `$isMobile`（≤900px）和容器宽度共同决定投影；不要只靠 CSS 把分屏隐藏掉。
- **canSplit 规则：** 每个 pane 最小 380px，分隔条按实际宽度计入；2 pane 需要约 770px，3 pane 约 1160px，4 pane 约 1550px。容器宽度足够当前 frame 的全部叶子时才渲染分屏；否则**整体回退单 pane + 标签条**，不渲染“部分分屏”。
- 900–1024px 只要宽度足够 2 个 pane 就允许分屏；窗口缩放只改变运行时投影，不重写持久化 `weights`。回到足够宽度时恢复原 frame 和比例。
- ≤900px：单 pane + 标签条（可切换会话），不渲染 SplitHandle、不支持拖拽标签；composer 沿用现有移动端样式。
- CSS 媒体查询只负责 shell/pane 布局；移动端交互（侧栏抽屉、单 pane 切换）继续用 `isMobile`/`sidebarOpen` 条件渲染，遵循 AGENTS.md。
- `ChatPane` 的 composer/附件在移动端复用现有样式，不新增第二套输入组件。

### 8.7 键盘与 ARIA

- `role="tablist"`、`role="tab"`、`role="tabpanel"`；方向键在标签间移动，Home/End 首尾，Enter/Space 激活，Delete 关闭（若产品确认）。
- `Ctrl+F6` / `Ctrl+Shift+F6` 在 pane 间切换（IDE 习惯；WebUI 在浏览器中也会捕获，需 `preventDefault`；如果与浏览器/屏幕阅读器冲突，可改为菜单命令）。
- 分隔条 `role="separator"` + 箭头键；`aria-valuenow/min/max`。
- 所有指针操作都有菜单/键盘替代：标签右键、pane 菜单、预设菜单。
- 焦点恢复：关闭标签后焦点到新 selected；关闭 pane 后到重定位目标；Sidebar 打开已存在会话时聚焦其 pane。
- 不劫持浏览器 `Ctrl+W`/`Ctrl+T` 等；标签关闭用 `Ctrl+F4` 或菜单（WebUI 是浏览器页面，不能可靠拦截所有系统快捷键）。

### 8.8 性能

- 每个 pane 只挂载一个 `ChatPane`/`SessionSnapshot`；不要把所有 tab 都保持挂载（流式 DOM 成本爆炸）。移动端只挂载当前标签。
- `sessionStateStore(sessionId)` 的 `derived` 只在对应 session 引用变化时通知；布局 store 变化不触发 ChatPane。
- `session-stream.js` 一次归约，避免 N 个 pane 重复处理 `$runEvents`。
- 拖拽/resize 只改根组件本地变量；`Pane`/`ChatPane` 的 props 不变。
- 4 pane 同时流式：测量输入延迟/帧率；必要时非焦点 pane 降低 Markdown 重解析（流式纯文本、完成后再 Markdown），但先以现有 `Chat.svelte` 的渲染路径为基线。
- 不引入 `content-visibility` 之外的虚拟化；现有长转录滚动逻辑保留。
- 新增代码懒加载：`Workbench.svelte` 随 `/chat` chunk 加载；纯模型很小。

### 8.9 工具面板：tty / todo(plan) / spec / logs

先说结论：**v1 不包含 tty / todo / spec 可停靠面板**；本方案只做 session tab + 分屏。2026-10-29 已单独实现只读 Plan dock（非 v1 验收项）；tty/spec/logs 仍不在范围内。原因不是布局，而是数据/后端契约：

| 面板 | 当前 WebUI 是否已有数据/能力 | 本方案处理 |
| --- | --- | --- |
| `tty` / interactive terminal | **没有。** WebUI 没有 PTY 端点；`bash` 工具的 stdout 只出现在 transcript / trajectory 里，不能输入、不能交互 | **v1 不做**。Go 后端的落点、API、WebSocket 协议与安全边界见 `docs/proposal/serve-pty-terminal-proposal.md` |
| `todo` / `plan` | **有。** `internal/tools/plan.go` 产生 plan 消息；`sessionRunStates[id].messages` 里有 `kind: "plan"`；Chat 已有 TODO/plan 卡片；TUI 也有 sticky todo | **已实现（2026-10-29）只读 Plan dock**：`components/workbench/PlanPanel.svelte` 从焦点会话的最新 plan 消息派生；桌面右侧 320px dock，移动端 overlay；不新增存储、工具或端点 |
| `spec` | **没有 MothX 等价物。** 没有 `pi-spec-graph`、spec frontmatter 索引或 spec 查询工具；`Library`/`Knowledge` 不是 spec graph | **v1 不做**。若需要，先定义 MothX 的 spec 工具/API，再谈面板；不要用通用文件树冒充 |
| `trajectory` | **有。** `TrajectoryView.svelte` + `/api/sessions/:id/trajectory` + per-session `trajectoryState` | **W3 可做只读 Trajectory 面板**；也可以保持现在的 pane mode（Chat/Trajectory 切换） |
| `logs` | **有。** `/ws/logs` + 诊断日志面板 | 可作为全局底部面板（W3）；不属于会话资源 |

如果以后要加工具面板，建议的模型（**不在 v1 实现**）：

```js
/** @typedef {{ kind: "tool", id: string, toolId: string, scope: "session"|"global", sessionId?: string }} LayoutToolTab */
```

- `scope: "session"` 的工具绑定一个 session（例如 plan/trajectory），切 session 时它跟着焦点 session 或保留绑定；
- `scope: "global"` 的工具（logs）不绑定 session；
- 工具面板只存 `toolId + scope + sessionId`，不存内容；内容由数据源投影；
- 工具 registry 的 `available()` 决定是否显示；没有后端契约的工具不注册；
- 工具面板的布局仍复用同一 Frame/View/Attention；可以先只在右侧/底部 dock，不进入中心分屏。

**建议优先级：**

1. v1：session tab + 分屏（本方案主体）。
2. W3：`plan`（TODO）只读 dock 已实现；`trajectory` 继续使用现有 pane mode，不另做 dock。
3. 另立方案：interactive terminal（需要 PTY 后端）与 spec-graph（需要 MothX spec 工具/索引）。
4. 不要把 tty/spec 作为布局阶段的“顺手加一个面板”塞进 v1；否则会把一个前端布局改造变成后端能力建设。

## 9. 浏览器持久化与“窗口模块”

WebUI 没有 Electron 的 `BrowserWindow`/main 进程。这里的“窗口模块”只指浏览器 tab/window 的运行时焦点，不再是布局持久化作用域：**同一 origin 一份共享布局**，符合“易用性优先”的目标。

### 9.1 一份工作区文档

- 同一 `mothx serve` origin 只保留一份 `mothx.workbench.v1`。
- 刷新、重启浏览器、新 tab 都读取同一份文档并恢复最近布局；这是“我的工作区跟着我走”的默认心智模型。
- 多 tab 同时打开时，布局写入 last-write-wins；不抢锁、不做实时镜像；后写入的 tab 成为最近布局。
- 如果产品后续确实需要“每个 tab 独立布局”，再引入 surfaceId + Web Locks，并升 version；v1 不做。
- `sessionStorage` 只允许放纯运行时状态（如拖拽草稿、临时滚动位置），不作为布局权威。

### 9.2 Storage key 与作用域

```text
key = `mothx.workbench.v1`
```

- 不加 origin 前缀：`localStorage` 本身按 origin 隔离。
- 不包含 token、用户标识、消息、附件或运行时状态；布局含 session id、draft id、几何、焦点、tab.mode，以及 draft 的本地创建参数（workDir/model/provider/expertId/mode）。
- `workDir` 是本机路径，属于轻量本地信息；文档明确它只用于恢复 draft，不作为工作区授权边界。多用户共享同一 origin 时可能看到彼此路径，若不可接受，draft 元数据应改为 `sessionStorage`（刷新保留、关 tab 丢失）。
- 另有两个独立 key：`mothx.composer-drafts.v1`（文本 + 已上传附件引用）和 `mothx.session-prefs.v1`（per-session model/provider）。它们不进入布局文档，也不作为会话事实。
- 不做 auth scope hash：布局不含凭据；多用户同 origin 最多看到彼此的不透明 session id 和本机路径，打开无权限/已删除 session 时显示空 tab 并允许关闭。
- 不监听 `storage` 事件；不同 tab 不实时互相覆盖视图，只在下一次读取/写入时 last-write-wins。
- 退出登录时可选择清理 `mothx.workbench.*`，但不是必需（易用性优先，不把清理做成登录门槛）。

### 9.3 文档与校验

- 文档：`PersistedWorkbenchV1`（见 5.1），只含 `version/frame/view/attention`。
- 读取：`JSON.parse` → `normalize()` → 不变量；不可修复回退 `defaultState()`（`single`）。
- 写入：只写持久化字段（剥离 epoch/clock/drag 草稿）；`JSON.stringify` 后 ≤ 64 KiB；超过则丢弃本次写入并 `console.warn`（不弹窗）。
- debounce 300ms；`pagehide`/`visibilitychange` 时 flush 一次；新 tab 启动时先 hydrate 再挂载 ChatPane。
- 版本高于当前：回退默认，不部分读取；在 `console.info` 记录一次（不含用户数据）。
- 损坏/字段缺失：局部修复（补 view、去孤儿、夹紧 weights、去重复 session/draft）；只有结构性损坏才整份回退。
- 不写任何消息、附件、标题、run 状态或凭据；draft tab 可存 workDir/model/provider/expertId/mode；composer 文本和已上传附件引用由 `composer-drafts.js` 另存到独立的 `mothx.composer-drafts.v1`；model/provider 选择由 `session-prefs.js` 存到 `mothx.session-prefs.v1`，都不要混入布局文档。

### 9.4 多 tab 行为

- 共享一份布局；A tab 打开/关闭 session 会成为下一次读取/写入的最近布局，B tab 不实时重排（避免编辑中被抢焦点）。
- 同一 session 可以在多个浏览器 tab 同时打开；后端 run/审批是权威，两个 tab 各自投影。审批 first-response-wins（后端 `DecisionService`）；前端不要加第二套“谁先点谁赢”。
- `runEvents` WebSocket 每 tab 一条；后端已有多个客户端的并发路径，不需要改动。
- 关闭最后一个 tab 不影响其他 tab；浏览器关闭所有 tab 后再打开，使用最后一次写入的布局。
- 两个 tab 并发写入时 last-write-wins；接受这个取舍，换取“新 tab 自动恢复工作区”的易用性。

### 9.5 响应式投影

- 布局文档与投影解耦：按容器宽度和 frame 叶子数决定是否分屏；每个 pane 最小 380px，不满足就整体回退单 pane + 标签条，不渲染部分分屏。
- 900–1024px 在宽度足够 2 个 pane 时允许分屏；≤900px 始终单 pane。
- 移动端不做 4 pane、不做拖拽/触摸手势；标签切换是唯一的跨会话入口。
- CSS 媒体查询只做布局；移动端交互用 `isMobile`/`sidebarOpen` 条件渲染（AGENTS.md）。

### 9.6 为什么不用服务端/IndexedDB

- 服务端：布局不是会话事实，跨设备同步没有需求；写进 `settings.json`/session 会扩大兼容面并违反“表现状态本地”的边界。
- IndexedDB：localStorage 的 64KB 文档足够；同步 API 更简单；不需要异步 hydrate 的复杂度。
- localStorage 的多 tab 共享从“风险”改为“默认共享工作区”；如果确实需要隔离，再用显式开关而不是默认复杂化。

## 10. serve/后端边界

- **不新增 API/WS 端点。** 使用现有：
  - `GET /api/sessions/:id/messages`（分页）
  - `GET /api/sessions/:id/stream`（SSE fallback）
  - `/ws/runs`（已订阅 `sessions` 列表中的所有 session）
  - 审批/提问/停止/发送现有端点
- **多 pane 并发发送**：每个 `ChatPane` 调用现有 `sendPrompt`/`stop`，后端 `SessionRuntime.SubmissionCoordinator` 已按 session 串行化；前端不要加全局“当前 run”锁。Draft tab 首次发送复用现有 `POST /api/session-id`，不新增端点。
- **附件：** 现有代码把待发送附件作为 dataUrl 保存在内存，`/api/attachments/{providerRef}` 只用于已归档/已上传引用；本方案不新增上传端点。因此 `composer-drafts.js` 只能跨刷新恢复已有 `providerRef` 的附件；dataUrl 附件切 tab 保留在内存，刷新后提示重选。若产品要求 dataUrl 也能跨刷新恢复，必须另立附件上传端点变更，不能在本方案里偷偷加 Go API。
- **模型/provider**：发送参数来自该 pane 的 `modelOverride`/`providerOverride`，最终回退到 session runtime 与服务端默认；选择按 sessionId 持久化到 `mothx.session-prefs.v1`，不要继续把全局 `selectedModel` 当发送源。
- **事件订阅**：`connectRuns()` 的 `runSubscriptions()` 目前基于 `sessions` 列表（最多 100）；工作台打开但不在列表前 100 的 session 需要确保它在 `sessions` 中（`refreshSessions`/`upsertSession` 已处理）。如果未来 session 列表分页很大，需把 workbench 打开的 session 加进订阅集合；这是**前端**的 `runSubscriptions` 调整，不需要改协议。
- **审批/提问**：每个 pane 使用 `sessionRunStates[id].runtime.pendingApprovals` 与 per-session pending question；全局 `activeApproval` 只作为“焦点 pane 当前选中”的镜像，`AttentionBell` 汇总所有 pane 的 pending 数量。后端的 first-response-wins 不变。
- **`sessionRuntime` 全局 store**：当前 `Chat.svelte` 把可见会话的 runtime 写进全局 `sessionRuntime`；多 pane 下不能代表所有 pane。改为 per-session `sessionRunStates[id].runtime`；`sessionRuntime` 仅保留给非工作台/兼容路径。
- **认证**：布局不存 token/密码；只含 session/draft id、几何和 draft 本地参数。不做 auth scope hash，不把布局当权限边界；打开无权限/已删除 session 时显示空 tab，后端接口照常拒绝。
- **CSP/安全**：布局只含不透明 id；渲染时按文本转义；不把 localStorage 内容插入 `{@html}`。

## 11. 迁移与发布

> 2026-10-29 清理执行：§11.3 条件已满足，旧 `?workbench` 开关、设置项与 `chat-single-host` 回退已移除。以下保留为发布记录。

### 11.1 特性开关（已移除）

- 灰度期曾使用 URL `?workbench=1` opt-in 或 Settings > Appearance 的本地开关；GA 后默认开启。
- 2026-10-29 清理移除了 `?workbench=0`/`?workbench=1`、Settings 中的“动态工作台布局”开关、`mothx.webui.workbenchEnabled` 偏好和 `workbenchEnabled` 分支；`/chat` 只有 Workbench 投影。
- **事件消费者始终唯一。** `session-stream.js` 是唯一 `$runEvents` 消费者；回退只重置布局，不恢复旧 Chat 消费路径。

### 11.2 路由与 Sidebar

- `Sidebar.openSession(id)` 统一走 `workbench.openSession(id)`，不再有 `currentSession.set(id)` 单 pane 回退。
- `Sessions.svelte`/`Experts.svelte` 的打开会话同样走统一入口；避免一个页面直接 set `currentSession`。
- `App.svelte` 的 `syncSessionFromRoute` 始终调用 `workbench.openSession(routeSession)`（或 `focusSession`）；不存在则打开，不能直接 `currentSession.set`。
- `syncRouteFromSession` 保留：焦点变化写 `?session=`；避免循环（比较当前值）。
- 启动顺序：先 hydrate 布局，再订阅路由/焦点同步；前进/后退必须可逆地打开/聚焦 tab。

### 11.3 旧路径移除（已执行 2026-10-29）

- `Chat.svelte` 已拆成 `ChatPane`，所有 `$currentSession` 作为“操作目标”的用法已替换为 `sessionId`。
- `session-stream.js` 是唯一 `$runEvents` 消费者。
- 每个 pane 的模型/provider/草稿/历史窗口都已 per-session；Draft tab 与 AttentionBell 已覆盖。
- 桌面分屏与移动单 pane 通过 `npm test`/`npm run e2e`。
- 已删除 `?workbench=0`/设置开关/`mothx.webui.workbenchEnabled`/`chat-single-host` 单 pane 回退；`workbench.smoke.mjs` 断言 `?workbench=0` 被忽略且不存在 `.chat-single-host`。

### 11.4 发布节奏（已完成到 Cleanup）

1. **内部**：W0 完成（所有 pane 可交互）后 dogfood；Lite 不进入内部发布。
2. **Beta**：收集多 tab/审批/移动端/窗口缩放反馈。
3. **Stable**：桌面默认开启。
4. **Cleanup（2026-10-29 已完成）**：删除旧路径/开关/未使用 CSS，更新 `docs/{en,zh}/serve.md` 与 changelog。

### 11.5 回滚

- 用户：重置布局或清空 `mothx.workbench.v1`；不再有单 pane 布局回退。
- 开发：只回退 `Workbench`/布局层；`session-stream.js` 继续作为唯一事件消费者，不能恢复旧 Chat 消费路径。
- 数据：localStorage key 保留；没有服务端迁移。

## 12. 测试计划

### 12.1 纯模型单测（`ui/src/lib/workbench/*.test.js`）

现有 `ui/package.json` 的 `test` 是 `node --test src/lib/*.test.js`，不会递归子目录。新增测试时二选一：

- 把测试放在 `ui/src/lib/workbench.test.js`（单文件，最省事）；或
- 把脚本改为显式包含：`node --test src/lib/*.test.js src/lib/workbench/*.test.js src/lib/trajectory/*.test.js`。

建议用目录 + 显式脚本，测试文件按模块拆：

1. `openSession`：新建/已存在/指定 group/group 缺失/每 group 上限/全工作区 32 上限。
2. `openDraft`/`replaceDraft`：插入/选中、替换后不残留、同 session 已打开时合并、失败保留草稿。
3. `closeTab`：selected/非 selected/最后一个/空 group 保留；运行中关闭不取消 run（状态层断言）。
4. `closeOtherTabs`。
5. `moveTab`：同 group 前后/跨 group/末尾/非法 index/same-position。
6. `splitGroup`：水平/垂直、before/after、带 tab/不带 tab、4 叶上限。
7. `removeGroup`：有/无 tabs、重定位前/后兄弟、最后一个 leaf 拒绝。
8. `resizeSplit`：夹紧/归一化/非法输入、shapeChanged=false。
9. `focusGroup`/`selectTab`/`setTabMode` 非法 id/mode。
10. `setPreset`：1/2/3/4 叶、0/1/5 tab、焦点保持/回退；每个 tab 的 `mode` 随 tab 保留。
11. `capturePreset` 不含 session。
12. `normalize`：null、错误版本、重复 group/split id、孤儿/缺失 view、重复 sessionId/draftId、selected 不存在、非法 weights、>4 叶、空 split、非法 tab.mode、超长 id/workDir。
13. `reconcileSessions`：删除聚焦/非聚焦/最后一个 tab。
14. per-pane model/provider：两个 session 各自选择不互相覆盖；刷新后从 `mothx.session-prefs.v1` 恢复；发送解析顺序 override → runtime → default。
15. property test：固定 seed 生成 1000 个操作序列，每步断言 5.2 的 13 条不变量。
16. 序列化 round-trip：`encode(decode(doc))` 稳定；runtime 字段不出现；重启恢复 last-layout。

### 12.2 会话流/状态单测

`ui/src/lib/session-stream.test.js`（Node `node:test`，用假 store/假 fetch）：

1. 两个 session 交错 `session_event`，各自 `sessionRunStates[id]` 的 messages/runEvents 正确，互不污染。
2. 同一 `wsSeq` 重复/乱序不重复归约；`assistant_delta` 只追加一次；trim 后 cursor 不 stall。
3. `done`/`error` 的 run identity 检查：旧 run 的延迟帧不结束新 run。
4. SSE fallback：每个 session 只开一个 observer；切后台/关闭后停止。
5. `visibleSessionIds` 只影响 effects（scrollEpoch/historyReloadEpoch/statsRefreshEpoch/approvalEpoch/questionEpoch），不影响归约。
6. `ensureSessionLoaded` 去重、stale 重载、失败状态、running 不淘汰。
7. 历史窗口：两个 session 的 `earliestSeq/hasMore` 隔离；旧 replay 帧不会插入分页区域。
8. Composer 草稿：切换 tab 保留文本和已上传附件引用；无 providerRef 的 dataUrl 附件刷新后提示重选；发送清空；LRU 上限。
9. Attention：两个 pane 同时 pending approval/question 时，`AttentionBell` 数量正确，响应一个不影响另一个。

### 12.3 组件与 source-scan

现有 WebUI 没有组件测试基建；不引入 jsdom/testing-library 大依赖。建议：

- 把 `SplitView` 的权重计算、`TabStrip` 的溢出计算、`previewDrop` 的目标判定抽成纯函数并单测。
- **导入边界检查**（仓库目前没有 source-scan 先例，不要声称沿用现有风格；可新建 `boundary.test.js` 或用 AST/文本扫描）：
  - `components/workbench/*` 不 import `views/Chat.svelte`；
  - `ChatPane` 不以 `$currentSession` 作为操作目标，不直接消费 `$runEvents`，不从全局 `$selectedModel` 取发送模型；
  - `lib/workbench/*` 不 import Svelte/`stores.js`/`api.js`；
  - `App.svelte` 只在开关开启时挂载 Workbench；移动端走单 pane 投影；
  - `session-stream.js` 是唯一 import `runEvents` 的非 stores 模块。
- **DOM 隔离检查**：`ChatPane` 的固定 id 必须带 pane 前缀；扫描 `id="chat-command-suggestions"`/`session-runtime-panel` 不应出现在组件内；`<svelte:window>` 不得在 `ChatPane` 内无条件注册。
- **生命周期检查**：`ChatPane` 的 `onDestroy` 不得调用 `abort()` 清理 `sessionRunStates[*].observer`；该路径只归 `session-stream.js`。
- Svelte 编译期：`npm run build` 会捕获模板/响应式错误；`svelte-check` 目前不在脚本里，若引入需另评（本方案不新增）。

### 12.4 e2e smoke

扩展 `ui/e2e/`：

- `workbench.smoke.mjs`：启动 `mothx serve`（或现有测试 fixture），用浏览器自动化/现有 e2e 工具：
  - 打开两个会话（可用本地 fixture session）；
  - 拖拽/菜单分屏；
  - 断言两个 pane 都出现各自消息；
  - 一个 pane 触发 pending approval/question，断言所属 pane 和 AttentionBell 都可见；关闭另一个 pane 后仍在；
  - 刷新/重启页面，断言标签/比例/焦点恢复；
  - 1024px/1440px viewport 下布局不溢出；900–1024px 在能容纳 2 个 ≥380px pane 时分屏；390/768px 下只出现单 pane + 标签条；窗口缩放不丢标签/焦点，也不渲染部分分屏。
  - 新建 Draft tab 后发送，断言 tab 变成真实 session 且消息不会重复；
  - 前进/后退只在显式打开会话时产生历史，pane focus 不堆积历史。
- 现有 `trajectory.smoke.mjs` 覆盖 Trajectory；新增 workbench 后仍要跑。
- `npm run e2e` 的脚本加上新 smoke；如果 CI 没有浏览器，至少跑模型/构建。

### 12.5 手工矩阵

| 场景 | Chrome | Firefox | Safari |
| --- | --- | --- | --- |
| 单 pane 聊天 | ✓ | ✓ | ✓ |
| 2/3/4 pane 分屏 | ✓ | ✓ | ✓ |
| 标签切换/关闭/溢出 | ✓ | ✓ | ✓ |
| 拖拽重排/跨 pane/边缘分屏 | ✓ | ✓ | ✓ |
| 后台运行 + 审批 | ✓ | ✓ | ✓ |
| 刷新/重启恢复布局 | ✓ | ✓ | ✓ |
| 多浏览器 tab 共享 last-layout | ✓ | ✓ | ✓ |
| 每个 pane 不同模型/provider | ✓ | ✓ | ✓ |
| Draft tab 首次发送 | ✓ | ✓ | ✓ |
| 1024/1280/1440/1920 宽度分屏 | ✓ | ✓ | ✓ |
| 390/768 宽度单 pane + 标签条 | ✓ | ✓ | ✓ |
| 深色/浅色/自定义主题 | ✓ | ✓ | ✓ |
| 中文输入法/composer | ✓ | ✓ | ✓ |

### 12.6 门禁

```bash
cd ui
npm test
npm run build
# 有浏览器环境时
npm run e2e
```

`ui/package.json` 的 `test` 脚本需要显式包含 `src/lib/workbench/*.test.js`（当前 `node --test src/lib/*.test.js` 不递归）。

无 Go 改动；不需要 `go test`。如果实现中意外改了 `internal/serve`（本方案不允许），需另立变更并跑对应 Go 测试。PTY 是独立提案，不随本方案实现。

## 13. 实施阶段

### Phase W0：单事件消费者 + ChatPane 抽取（前置，风险最高）

**交付：**

- `lib/session-stream.js`：唯一 `$runEvents` 消费者；SSE fallback；完整 effects（scroll/historyReload/statsRefresh/subAgent/approval/question/error）；observer 生命周期；dispatcher 始终启动。
- `sessionRunStates` 增加 `loading/status/loadedAt/effects/earliestSeq/hasMore/modelOverride/providerOverride`。
- `components/chat/ChatPane.svelte` / `DraftPane.svelte` / `ComposerPane.svelte`：ChatPane props `sessionId/active`，DraftPane props `draftId/active`，ComposerPane 共用；读 per-session state；动作显式 sessionId；固定 DOM id 加 pane 前缀；全局键盘/pointer 监听按 `active` 限定。
- `views/Chat.svelte` 变成薄 wrapper；单会话行为不变。
- `chat-runtime-state.js` 改为显式 sessionId，轮询按 session 注册；`composer-drafts.js`（文本 + 已上传附件引用）、滚动/历史窗口恢复；`session-prefs.js` 恢复 model/provider。
- 删除 `persistLocalSessionState`/`restoreLocalSessionState` 的搬运路径（或先保留但不再作为唯一源），删除 `onDestroy` 对全局 observer 的 abort。

**退出标准：**

- 单会话功能与今天一致；两个 session 交错流式测试通过。
- `$runEvents` 只有一个消费者（边界检查）；关闭一个 pane 不中止其他 session observer；后台 approval/question 仍产生 effects。
- `npm test && npm run build` 通过。


### Phase W1：标签页（单 pane）

**交付：**

- `lib/workbench/{types,model,normalize,presets,store,persistence}.js` + 测试。
- `Workbench.svelte`/`Pane.svelte`/`TabStrip.svelte`/`EmptyPane.svelte`。
- Sidebar/Sessions/Experts 接入 `openSession`；`currentSession` 焦点投影；pane focus 用 `replaceNavigate` 不新增历史。
- 标签关闭/重排/溢出/右键菜单；`setTabMode`；Draft tab（`openDraft`/`replaceDraft`）。
- 特性开关 + 共享 localStorage 布局持久化（刷新/重启恢复）。
- 移动端单 pane + 标签条投影（`isMobile` 条件渲染）。

**退出标准：**

- 多会话标签切换不丢消息/草稿；刷新恢复标签/焦点。
- 非法文档回退 single。
- 单 pane 性能不退化。


### Phase W2：分屏与拖拽

**交付：**

- `SplitView.svelte`/`SplitHandle.svelte`；`splitGroup/moveTab/removeGroup/resizeSplit`。
- 原生 pointer 拖拽 + 投放提示；移动端不渲染拖拽。
- 4 叶上限、桌面最小窗口宽度提示。
- 键盘/ARIA。

**退出标准：**

- 4 pane 同时流式可交互；resize/拖拽不触发无关 ChatPane 重渲染。
- 拖拽结果与 `previewDrop` 一致；Escape 取消。
- 关闭 pane 的 tab 重定位正确。


### Phase W3：预设、审批与工具面板

**交付：**

- `single/two-column/two-row/quad` 预设；Settings > Appearance 的布局/重置。
- 审批/提问按 pane 显示；全局 `activeApproval` 只做焦点镜像；`AttentionBell` 汇总所有 pane 的 pending 数量并支持一键聚焦。
- 后台 running 徽标/状态；焦点 pane 的标题/文档标题。
- 空 pane 的“打开会话”搜索。
- **可选：** `plan`（TODO）与 `trajectory` 的只读侧栏/底部工具面板；`logs` 全局底部面板。**不含 tty/spec。**


### Phase W4：e2e、性能、清理

**交付：**

- workbench e2e smoke；桌面分屏/移动单 pane/刷新重启/窗口缩放手工矩阵。
- 性能测量与优化；移动端单 pane 验收后删除旧单会话分支/开关/未用 CSS。
- 更新 `docs/{en,zh}/serve.md` 的 WebUI 章节与 changelog。
- 代码减法：删除 `persistLocalSessionState`/`restoreLocalSessionState`、未用 `sessionRuntime` 兼容、重复 selector。


### 13.1 关键路径

```text
W0（事件/ ChatPane）──▶ W1（标签/Draft/移动单 pane）──▶ W2（分屏）──▶ W3（预设/审批）──▶ W4（清理）
```

W0 是主路径；Lite 不属于 v1 交付，只作为另立实验分支的降级选项。

### 13.2 Lite 方案（非 v1 交付，实验降级）

Lite 不进入本方案验收，也不默认发布；仅当 W0 严重延期且另立实验分支时使用：

- 跳过 W0 的完整 ChatPane 抽取：保留现有 `Chat.svelte` 作为 store-only 焦点 pane，新增 `SessionSnapshot` 只读 pane。
- 前提：必须先落地 `session-stream.js` 并把 `Chat.svelte`/ChatPane 改成 store-only；Lite 不能再让旧 Chat 消费 `$runEvents`。
- 需要：workbench 标签/分屏、共享 localStorage、`ensureSnapshotLoaded`。
- 交付：能并排看，但非焦点 pane 不能直接发消息/审批。
- 升级路径：W0 完成后立即用 `ChatPane` 替换 `SessionSnapshot`，删除降级分支。

## 14. 风险与对策

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| `Chat.svelte` 184KB、86 处 `$currentSession`，一次性拆完回归多 | 高 | W0 的“单消费者 + per-session state”分阶段合入；每步保持单会话行为快照 |
| 多个 pane 重复消费 `$runEvents` 导致重复归约（`assistant_delta` 会双写文本） | 高 | `session-stream.js` 唯一消费者；import/文本边界检查 + 交错流测试 |
| `sessionRuntime`/`activeApproval`/`selectedModel` 全局单值导致 pane 串台 | 高 | pane 读 `sessionRunStates[id]`；模型/provider/草稿/历史窗口 per-session；全局只做焦点镜像 |
| `chat-runtime-state.js` 仍以 `currentSession()` 为闭包，单轮询器 | 高 | 按 session 注册 manager/polling，不随 pane 挂载停止；隐藏 tab 也要能重连 |
| 多 pane 重复固定 DOM id / 全局 window/document 监听 | 高 | `ChatPane` 加 pane 前缀；`<svelte:window>`/pointer 监听按 `active` 限定或元素级注册 |
| 一个 pane 卸载时 abort 其他 session 的 observer | 高 | observer 生命周期归 `session-stream.js`；`ChatPane.onDestroy` 只清理自己的 UI |
| 开关切换时双事件消费者/`assistant_delta` 双写 | 高 | dispatcher 始终运行；回滚时显式停 dispatcher 并重建 store，不能两边同时消费 |
| draft/composer 持久化边界 | 中 | 布局只存 draft 元数据；composer 文本/附件引用独立 key；model/provider 独立 `session-prefs` key |
| Draft tab 首次发送的 session 分配/替换失败 | 中 | `openDraft`/`replaceDraft` 纯操作 + 失败保留草稿；测试同 session 合并 |
| 历史窗口仍留在组件局部（`earliestSeq`/`hasMore`） | 中 | 迁入 `sessionRunStates`；后台 replay 帧按 session 窗口过滤 |
| 共享 localStorage 多 tab last-write-wins | 中 | 接受该取舍；布局只存 id/几何/draft 本地参数；提供重置/恢复默认 |
| 刷新后布局引用已删除 session | 中 | `reconcileSessions` 只删“确认删除”的 id；未知 id 保留为失败/空 tab，打开时再报错 |
| `runEvents` 只订阅 sessions 前 100 条 | 中 | workbench 打开的 session 调 `upsertSession`/`refreshSessions`；`runSubscriptions` 加入打开的 session |
| 移动端被工作台挤坏 | 中 | `isMobile` 下只渲染单 pane + 标签条；不渲染 SplitHandle；390/768 纳入验收 |
| 4 pane 同时流式卡顿 | 中 | per-session derived；只挂载 selected body；measure；必要时降低非焦点 Markdown |
| 特性开关双路径腐烂 | 中 | 明确移除条件与版本；开关只包壳；W4 删除 |
| 路由/currentSession 循环更新/启动覆盖深链 | 中 | 单一写者（workbench）；先 hydrate 再订阅；路由变化走 `openSession`；测试前进/后退 |
| Svelte 5 legacy `$:`/store 语法与 runes 混用 | 低 | 沿用现有 legacy 语法；不引入 runes，保持文件一致 |
| 测试脚本不覆盖子目录 | 低 | 更新 `ui/package.json` test glob，或测试放 `src/lib/workbench.test.js` |
| source-scan 没有现成基建，文本扫描易脆 | 低 | 优先 import 边界/AST 检查；明确新增测试文件，不声称沿用现有风格 |

## 15. 开放问题与建议

| 问题 | 选项 | 建议 |
| --- | --- | --- |
| 完整交互 pane 还是 Lite 只读？ | 完整 / Lite | **完整 W0 为唯一交付**；Lite 不进入 v1 验收，如延期另立实验分支 |
| 默认预设？ | single / two-column | **single**；首次不突然分屏 |
| 每个 pane 的 Chat/Trajectory 模式存哪？ | group / tab | **group**（v1）；切会话保留用户当前模式 |
| 关闭标签是否提示后台运行？ | 阻塞确认 / 非阻塞提示 | **非阻塞提示一次**“任务将在后台继续”；易用性优先，不打断操作 |
| 新浏览器 tab 是否继承最近布局？ | 是 / 否 | **是**；同一 origin 共享 last-layout，刷新/重启/新 tab 都恢复 |
| 审批显示在哪个 pane？ | 所属 pane / 全局中心 / 两者 | **所属 pane + AttentionBell 汇总**；不另做全局模态，避免两个模态打架 |
| 最小支持宽度？ | 1024px / 1280px | **每个 pane 最小 380px**；宽度够当前 frame 的全部叶子才分屏，不够就整体回退单 pane + 标签条；900–1024px 可 2 pane，≤900px 单 pane |
| 是否持久化 pane mode？ | 是 / 否 | **是**（tab.mode），刷新后每个 tab 保持自己的 Chat/Trajectory |
| 是否允许同一 session 在多个 pane 打开？ | 允许 / 禁止 | **工作台内禁止**（同一文档唯一），避免两个 composer 同时发送；跨浏览器 tab 允许各自投影，后端是权威 |
| 是否做自定义布局预设？ | 是 / 否 | **v2**；v1 只内置 4 个 |
| 是否在 v1 做 tty/todo/spec 面板？ | 做 / 不做 | **不做 tty/spec；todo(plan) 可在 W3 做只读面板**。tty 需要 PTY 后端，见 `docs/proposal/serve-pty-terminal-proposal.md` |

## 16. 验收清单

状态（2026-10-29 复检）：`[x]` 由 `npm test` / `npm run build` / `ui/e2e/workbench.smoke.mjs` 自动覆盖；除 Windows 实机与本机无法替代的人工手感/长时性能外，验收项均有自动化证据。

- [x] 同一 `/chat` 页面可并排看到两个 session 同时流式输出，内容不串。
- [x] Sidebar 点击已打开会话只聚焦；关闭标签不删除 session。
- [x] 关闭运行中标签不阻塞，后台继续；侧边栏状态正确，可重新打开。
- [x] 新建 Draft tab，首次发送后变成真实 session；文本和已上传附件引用保留且不重复；无 providerRef 的附件刷新后提示重新选择。
- [x] 每个 tab 的 Chat/Trajectory 模式独立；换布局/应用预设后不丢失。
- [x] 每个 pane 可独立选择模型/provider，互不覆盖；发送参数来自所属 pane；刷新后 model/provider 选择恢复。
- [x] 4 pane 上限生效；第 5 个分屏/拖拽边缘被拒绝并给出原因。
- [x] 拖拽重排/跨 pane/边缘分屏与预览一致；Escape 取消。
- [x] 分隔条拖动/双击/键盘调整；刷新后比例恢复。
- [x] 刷新、重启浏览器、新 tab 后标签/焦点/tab mode 恢复；损坏/超大/未来版本回退 single。
- [x] 多浏览器 tab 共享 last-layout；last-write-wins 不破坏可恢复性。
- [x] 900–1024px 在能容纳 2 个 ≥380px pane 时分屏；不足时整体回退单 pane + 标签条；390/768px 下单 pane + 标签条可用、无横向溢出。
- [x] 审批/提问在所属 pane 显示；后台 pending 有 AttentionBell 入口；不会因另一个 pane 的 runtime 被覆盖。
- [x] 关闭一个 pane 不中止其他 session 的 observer；同一 DOM 无重复固定 id；window 键盘只影响焦点 pane。
- [x] 旧特性开关、`?workbench=0` 与 `chat-single-host` 单 pane 回退已删除；`/chat` 始终渲染 Workbench，移动端仍为单 pane 布局，且不存在双事件消费者。
- [x] `cd ui && npm test && npm run build` 通过；有浏览器时 `npm run e2e` 通过。
- [x] 双语 i18n 完整；无硬编码用户可见文案。
- [x] 文档/changelog 更新；本提案状态改为 Implemented。

## 17. PTY / Terminal 后端方案

v1 不做 interactive terminal。PTY 是独立的 serve-only 后端能力，不属于布局方案；包归属、HTTP/WS 协议、配置、安全与测试见 `docs/proposal/serve-pty-terminal-proposal.md`。该提案默认允许回环和已认证的非回环；只有未认证的非回环监听才拒绝。布局方案只在 W3 之后按该提案的 feature 投影 `TerminalPane`，不把 PTY 塞进 `internal/tools/bash.go` 或 session pool。

## 附录 A：ThinkRail 参考文件地图

| 主题 | ThinkRail 文件 | 读什么 |
| --- | --- | --- |
| 边界与职责 | `apps/web/src/shell/layout/SPEC.md` | Responsibility / Boundary / State contract |
| Frame/View 归一化 | `apps/web/src/shell/layout/normalized.ts` | `WorkbenchFrame`、`WorkspaceViewState`、`projectWorkspaceLayout` |
| 纯操作 | `apps/web/src/shell/layout/model.ts` | `openCenterTab`、`closeLayoutTab`、`moveTabToGroup`、`splitCenterGroup`、`resizeCenterSplit` |
| 预设 | `apps/web/src/shell/layout/presets.ts` | `BUILTIN_LAYOUT_PRESETS`、`instantiateWorkbenchFrame`、`applyWorkbenchPreset` |
| 持久化 | `apps/web/src/shell/layoutState/layoutState.ts` | `validateLayoutDocument`、回退；其 surface id/Web Locks 是本方案**不照抄**的部分 |
| 渲染隔离 | `apps/web/src/shell/SPEC.md` | 「Render isolation」与 memo 边界（Svelte 用 derived store 实现类似目标） |

阅读顺序：`layout/SPEC.md` 的 State contract → `normalized.ts`/`model.ts` 纯函数 → `layoutState.ts` 的校验/回退 → 最后看渲染。**不要**照抄 React 组件、`react-resizable-panels` 的 hook，或 ThinkRail 的 per-surface 持久化；本方案用共享 last-layout。

## 附录 B：默认状态与预设

```js
export const MAX_CENTER_GROUPS = 4;
export const MAX_TABS_PER_PANE = 16;
export const MAX_TABS_PER_SURFACE = 32;
export const MIN_WEIGHT = 0.1;
export const MAX_WEIGHT = 0.9;

export function defaultState() {
  return {
    frame: { version: 1, center: { kind: "group", id: "g1" } },
    view: { groups: { g1: { tabs: [] } } },
    attention: { focusedGroupId: "g1", lastFocusedGroupId: "g1" }
  };
}

export const BUILTIN_PRESETS = [
  { id: "single", name: "workbench.preset.single", frame: { version: 1, center: group("g1") } },
  { id: "two-column", name: "workbench.preset.twoColumn", frame: { version: 1, center: split("s1", "horizontal", group("g1"), group("g2")) } },
  { id: "two-row", name: "workbench.preset.twoRow", frame: { version: 1, center: split("s1", "vertical", group("g1"), group("g2")) } },
  { id: "quad", name: "workbench.preset.quad", frame: { version: 1, center: split("s1", "horizontal",
      split("s2", "vertical", group("g1"), group("g2")),
      split("s3", "vertical", group("g3"), group("g4"))) } }
];
```

`name` 是 i18n key，不是用户文案；`LayoutPreset` 只含 `id/name/frame`，不含 view/attention/session。

## 附录 C：i18n key 草案

`ui/src/lib/preferences.js` 的 `zh`/`en` 同步新增：

```text
workbench.tab.close
workbench.tab.closeOthers
workbench.tab.closeRight
workbench.tab.copySessionId
workbench.tab.revealInSidebar
workbench.pane.splitRight
workbench.pane.splitDown
workbench.pane.close
workbench.pane.moveTabsConfirm
workbench.empty.title
workbench.empty.openSession
workbench.empty.newTask
workbench.preset.single
workbench.preset.twoColumn
workbench.preset.twoRow
workbench.preset.quad
workbench.settings.title
workbench.settings.reset
workbench.settings.enabled
workbench.reason.centerLimit
workbench.reason.groupMissing
workbench.reason.tabMissing
workbench.reason.lastGroup
workbench.reason.sessionRunning
workbench.reason.unknownPreset
workbench.reason.tabLimit
workbench.reason.invalidState
```

## 附录 D：持久化校验清单

- 顶层对象、`version === 1`。
- `frame.version === 1`；递归深度 ≤ 8；节点数 ≤ 15。
- 叶子数 1–4；group/split id 唯一且不冲突。
- `view.groups` 键数 ≤ 8；每 group tabs ≤ 16；全工作区 tabs ≤ 32。
- 所有 id/sessionId/draftId/workDir 字符串 ≤ 256；draft tab 必须带 `draftId`。
- `weights` 长度 2、有限、`[0.1, 0.9]`、和约 1。
- 每个 tab 的 `mode` ∈ `{chat, trajectory}`，否则 `chat`。
- `JSON.stringify(doc).length <= 65536`。
- 未知字段丢弃；返回 sanitized clone。
- 布局不写消息/附件/标题/run 状态；draft tab 可含 workDir/model/provider/expertId/mode。

## 附录 E：实现顺序速查

1. W0：`session-stream.js` + `ChatPane` 抽取；接真实 session，单会话行为不变。
2. `workbench/types.js` + `defaultState` + 不变量断言。
3. `normalize.js` + 非法文档测试。
4. `model.js` 的 open/close/draft/move/split/resize/setTabMode，逐操作测试。
5. `presets.js` + 分配测试。
6. `workbench/store.js`（内存 commit + focus）。
7. `Workbench.svelte`/`Pane.svelte`/`TabStrip.svelte` + `ChatPane` 跑通单 pane。
8. Sidebar/Sessions/Experts/路由/`currentSession` 焦点同步 + 共享 localStorage 恢复。
9. `SplitView`/`SplitHandle`/pointer 拖拽/响应式投影。
10. Draft tab、AttentionBell、预设、e2e、性能、清理旧路径。
