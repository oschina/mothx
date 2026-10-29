# Serve WebUI 动态工作台布局方案

> Date: 2026-10-28 · Last revised: 2026-10-29 · Status: Proposal · Owner: Serve WebUI (Svelte)
> Related: `AGENTS.md`, `docs/proposal/webui-multi-session-streaming-proposal.md`, `docs/proposal/webui-project-session-management-proposal.md`, `docs/proposal/serve-pty-terminal-proposal.md`
> Reference implementation: `/home/free/src/thinkrail`（`apps/web/src/shell/layout/`、`apps/web/src/shell/layoutState/`、`apps/web/src/shell/SPEC.md`）
> 目标代码：`ui/`（Svelte 5 + Vite，构建后嵌入 Go binary，由 `mothx serve` 提供）

## 0. 决策摘要

本方案只覆盖 **MothX serve WebUI**（`ui/`，Svelte 5），引入前端本地的动态工作台：`/chat` 从“一次只渲染一个 `Chat.svelte`”升级为“**会话标签页 + 桌面最多 4 个可调整大小的分屏**”，布局可记忆、可恢复，并支持键盘操作。**桌面分屏 + 移动端单 pane 标签条；不为 PC-only 牺牲手机聊天。**

核心决策：

- **只改 `ui/`，不改 Go/serve 后端。** 现有 `/ws/runs` 已经订阅 `sessions` 列表中所有会话，`/api/sessions/:id/messages`、`/api/sessions/:id/stream`、审批/提问接口都按 session 工作；多 pane 只是同一条数据流的多个渲染消费者。**不新增 HTTP/WS 端点，不改 `settings.json`/`serve.json`。**
- **复用 `session-runs.js` + `session-view.js`，不新建第二套会话状态。** WebUI 已经按 `sessionId` 保存后台运行状态，并有纯 reducer（`viewFromSessionState`、`applySessionViewReducer` 等）。本方案的关键不是“再造投影”，而是把 `Chat.svelte` 里“只有当前会话能渲染”的壳拆成可复用的 `ChatPane`/`SessionSnapshot`，并让**一个**模块消费 `$runEvents`。
- **工作台只作用于 `/chat`。** Sidebar 保持现状（固定栏目；现有移动端抽屉代码保持原样，但不属于本方案）；Sessions/Skills/Knowledge/Stats/Cron/Settings 仍是整页视图。Tab 代表 session 或 draft，不把设置页做成可停靠面板。
- **v1 目标是所有 pane 都可交互：** `ChatPane`（带 composer/审批/轨迹）。`SessionSnapshot` 只作为 W0 延期时的降级形态，不作为默认发布路径；因为 `Chat.svelte` 184KB、86 处 `$currentSession`，W0 必须分阶段合入并用单会话快照守住行为。
- **v1 只有 session/draft tab，不把 tty / todo / spec 做成可停靠面板。** 现有 WebUI 没有 PTY terminal API，也没有 MothX 的 spec-graph 工具；`plan`（TODO/计划）与 `trajectory` 的数据已存在，可作为 W3 的只读工具面板；真正的 interactive tty、spec 面板需要新的后端/工具契约。**如果要做 tty，Go 后端的包归属、HTTP/WS 协议、鉴权/沙箱/资源上限见 `docs/proposal/serve-pty-terminal-proposal.md`。**
- **纯 JS 模型 + Svelte 渲染器。** 不引入 React 生态（`react-resizable-panels`、`@dnd-kit/core` 不适用于 Svelte）；分屏用 CSS flex + 自写 pointer splitter，拖拽用原生 Pointer Events（鼠标/触控板）。不新增运行时依赖。
- **布局持久化用一份共享 `localStorage` 文档。** WebUI 没有 Electron main 进程；同一 origin 只保留一份 `mothx.workbench.v1`，刷新、重开浏览器、新 tab 都恢复最近布局。多 tab 采用 last-write-wins，不做 Web Locks/surface 抢锁，也不做实时镜像同步；布局不写服务端。
- **`currentSession` 从“可写全局源”改为“工作台焦点投影”。** Sidebar/Sessions/Experts 的“打开会话”统一走 `workbench.openSession(id)`；工作台更新焦点后再同步 `currentSession` 和 hash 路由，避免两套选中状态互相打架。
- **响应式，而不是 PC-only。** 桌面按 1024px+ 工作，提供最多 4 pane 分屏；`isMobile`（≤900px）下渲染单 pane + 标签条，不做分屏/拖拽，保留现有 Sidebar 抽屉与聊天输入体验。移动端交互使用 Svelte 条件渲染，CSS 媒体查询只负责布局，遵循 AGENTS.md。
- **每个 pane 的模型、provider、composer 草稿独立。** 模型选择不能继续做全局单值，否则两个 pane 会互相改模型/发送参数；草稿按 sessionId 保存在本地，切 tab 不丢。
- **审批不埋。** 每个 pane 内联显示自己的 pending approval/question；同时提供全局 attention 徽标（标签角标 + 顶栏入口），一键定位到对应 pane/tab，但不新增阻塞确认。
- **特性开关 + 可回滚。** 灰度期用 `?workbench=1` / 设置开关 opt-in；GA 后默认开启，保留 `?workbench=0` 与设置里的关闭项一个版本。关闭时保留现有 `Chat.svelte` 单会话路径。

### 0.1 修订原则（2026-10-29）

优先级：**易用性 > 安全/隐私摩擦**，但不得越过 `AGENTS.md` 的硬边界。

**易用性优先的具体取舍：**

- 工作台 GA 后默认开启，提供 `?workbench=0` 与设置里的关闭项；灰度期才 opt-in。
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
| Policy, not forks | 桌面/移动只做同一 Workbench 的两种投影；不做“移动版第二套 Chat”；Lite 仅作为可删除的降级开关 |
| One database direction | 不碰 `internal/db`/`internal/dao`；不把布局写进数据库或 `settings.json`/`serve.json` |
| Web UI mobile 约定 | `isMobile` 条件渲染负责移动交互，CSS 媒体查询只负责布局 |
| 双语与门禁 | W4 同步 `docs/{en,zh}/serve.md` 与 changelog；`npm test`/`npm run build`/e2e 作为门禁 |

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
- `lib/stores.js` 的 `connectRuns()` 打开一个 `/ws/runs` WebSocket，并 `runSubscriptions()` 把 `sessions` 列表中的**所有**会话订阅上；`runEvents` 是全局追加数组，`sessionRunStates` 按 session 归约。也就是说，**后台会话的流式数据已经到达浏览器**，只是没有第二个 pane 渲染它。
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
- `/ws/runs` 已经订阅 `sessions` 列表中的所有会话，后台会话的数据已经到达浏览器；
- `Chat.svelte` 的 `applySessionViewReducer` 已经在“当前会话”和“后台会话”之间分流。

所以本方案只做三件事：**把渲染拆成 `ChatPane(sessionId)`、把事件消费收敛到一个 `session-stream.js`、把布局/持久化加上**。不新增会话数据模型，也不改后端协议。

## 2. 目标、非目标与名词

### 2.1 目标

1. `/chat` 支持会话标签页：Sidebar 点击会话时，已打开则聚焦，未打开则插入当前 pane；支持关闭、重排、溢出菜单。
2. 中央分屏：最多 4 个 pane，支持左右/上下二分、拖拽标签到边缘分屏、菜单分屏、分隔条拖动。
3. 每个 pane 有独立选中会话、独立滚动、独立 composer；v1 目标是所有 pane 都可交互（Lite 只读快照仅作 W0 延期时的降级）。
4. 焦点模型：`focusedSessionId` 由聚焦 pane 的选中标签推导；Sidebar 高亮、路由、StatusBar 都跟随它。
5. 布局记忆：刷新、重启浏览器、新 tab 都恢复最近拓扑、每个 pane 的标签、焦点、比例；非法文档回退单 pane。
6. 响应式窗口：桌面 1024px+ 支持最多 4 pane；`isMobile`（≤900px）渲染单 pane + 标签条，不做分屏/拖拽；窗口缩放不重写持久化比例。
7. 不改变 serve 后端的会话/run 语义；不新增端点；不改变现有审批/提问/流式协议。
8. 不显著增加首屏包体：不引入 React/DnD 库；新增纯 JS 模型 + Svelte 组件，按路由懒加载。
9. 可回滚：GA 后默认开启，保留 `?workbench=0`/设置关闭项一个版本；关闭时恢复现有 `Chat.svelte` 单会话路径。

### 2.2 非目标

- **只做 serve WebUI。** 不修改、不共享其他 renderer。
- **不改 TUI/CLI/ACP。** 工作台是 WebUI 表现层。
- **不改 Go/serve API。** 不新增 `/api/layout`、不把布局写进 session 元数据、不改 `/ws/runs` 协议。
- **不做实时跨 tab 镜像。** 同一 origin 共享一份 last-layout，多 tab last-write-wins；不同 tab 同时打开同一 session 各自投影（后端 session/run 是权威）。
- **不做多用户共享布局。** 布局是浏览器本地表现状态，不跟随账号/设备。
- **不做通用 docking。** 只支持 session tab 与（未来）只读工具面板；不把任意 Svelte 组件注册成可停靠面板。
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
| View | 每个 group 的 tabs、选中项、pane 模式（chat/trajectory）；整个 origin 一份（last-write-wins）。 |
| Attention | 当前聚焦 group、最后聚焦 group、导航时钟（runtime-only）；另含 pending approval/question 的全局提醒。 |
| Pane / Group | 分屏树叶子；拥有标签条和内容区。 |
| ChatPane | 可交互的会话 pane：composer、审批、轨迹切换。 |
| SessionSnapshot | W0 延期时的只读会话 pane：从 `sessionRunStates` 渲染转录/轨迹，不提供发送。 |
| Draft tab | 尚未创建 session 的新会话标签；首次发送后原子替换为 session tab。 |
| Surface | 一个浏览器 tab/window；v1 不再作为布局持久化作用域，仅用于运行时焦点/拖拽状态。 |
| Preset | 资源无关的 frame 模板；应用时重分配现有 tab。 |
| Projection epoch | shape 变化递增的运行时栅栏；取消旧拖拽/延迟焦点。 |
| Tool panel | 非 session 的只读工具面板（如 plan/trajectory/logs）；v1 不做 tty/spec。 |

### 2.4 成功标准

- 同一 `/chat` 页面可并排看到两个 session 同时流式输出，内容不串。
- Sidebar 点击已打开会话只聚焦；关闭标签不删除 session；关闭运行中标签不阻塞，后台继续并可再次打开。
- 刷新、重启浏览器、打开新 tab 后恢复最近布局；损坏布局回退单 pane。
- 1024/1280/1440/1920px 宽度下分屏可用；390/768px 下单 pane + 标签条可用，无横向溢出。
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
├── types.js              # JSDoc typedef：Frame/View/Attention/Tab/Draft/Preset/Result
├── model.js              # 纯操作：open/close/move/split/resize/focus/preset/draft
├── normalize.js          # decode/encode/normalize/reconcile
├── presets.js            # single/two-column/two-row/quad
├── store.js              # Svelte writable + hydrate/persist + focus
├── persistence.js        # localStorage 共享文档 / 校验 / 恢复最近布局
└── index.js              # 桶导出

ui/src/lib/session-stream.js      # 唯一 $runEvents 消费者（必须在 workbench 纯层之外）

ui/src/components/workbench/
├── Workbench.svelte      # 根：订阅 store、持有 drag/resize 草稿
├── SplitView.svelte      # 递归 frame.center
├── Pane.svelte           # pane 容器 + 标签条 + 内容
├── TabStrip.svelte       # 标签、溢出、拖拽源、pending 徽标
├── SplitHandle.svelte    # 分隔条（pointer + keyboard）
├── EmptyPane.svelte      # 空 pane：打开会话/新建草稿
├── SessionSnapshot.svelte# W0 延期时的只读会话（降级形态，不默认发布）
├── AttentionBell.svelte  # 全局 pending approval/question 入口
└── DragLayer.svelte      # 拖拽预览/投放提示
```

修改：

```text
ui/src/App.svelte                         # chat 路由渲染 Workbench（开关/移动端投影）
ui/src/views/Chat.svelte                  # 拆成 ChatPane；保留薄 wrapper 或删除
ui/src/views/Sessions.svelte              # 打开会话走 workbench.openSession
ui/src/views/Experts.svelte               # 子会话打开走 workbench.openSession
ui/src/components/Sidebar.svelte          # 打开会话走 workbench.openSession
ui/src/components/chat/SessionHeader.svelte # 支持 pane 内 chat/trajectory 切换
ui/src/lib/stores.js                      # currentSession 改为工作台焦点投影；selectedModel 改为按 session/pane 解析
ui/src/lib/chat-runtime-state.js          # manager 从 currentSession 闭包改为显式 sessionId（每个 pane 一个或按 session 注册）
ui/src/lib/session-runs.js                # 增加 status/loading/effects/history window/缓存淘汰
ui/src/lib/session-view.js                # 复用；可能新增 selectors
ui/src/lib/session-actions.js             # 新增：显式 sessionId 的 send/stop/approve/answer/loadOlder
ui/src/lib/session-store.js               # 新增：per-session derived store + forget
ui/src/lib/composer-drafts.js             # 新增：按 sessionId 保存 composer 草稿/附件
ui/src/lib/attention.js                   # 新增：跨 pane 的 pending decision 投影
ui/src/style.css                          # workbench/pane/splitter/responsive 样式
ui/package.json                           # test 脚本显式覆盖 lib/workbench 子目录
```

### 4.3 依赖规则

- `lib/workbench/*` **禁止** import `views/Chat.svelte`、Svelte 组件、`lib/stores.js`、`lib/api.js`；只允许纯 JS 与自身类型。
- `lib/workbench/store.js` 可以 import `lib/workbench/*`、`lib/persistence.js`；不 import Chat/API。
- `lib/session-stream.js` 可以 import `lib/stores.js`（runEvents/sessions）、`lib/session-runs.js`、`lib/session-view.js`、`lib/api.js`；它是唯一把 socket 事件变成 session state 的地方，也是唯一允许消费 `$runEvents` 的模块。
- `components/workbench/*` 可以 import store、纯模型、`ChatPane`/`SessionSnapshot`、现有 UI 组件；不直接 fetch。
- `ChatPane` 只通过显式 `sessionId` 和 `session-actions.js` 工作；不读 `currentSession` 作为目标，也不直接拼 provider content。
- `ChatPane` 的模型/provider 从该 session 的 state/runtime 派生，选中变更写回该 session；禁止继续用全局 `selectedModel` 作为发送源。
- `currentSession` 由 workbench store 派生/同步；Sidebar/Sessions/Experts/App 路由不再直接 `currentSession.set()`（除 workbench 内部）。

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
      → 记录 effects（scroll/refresh/subagent）到 per-session effect 标记
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
- `App.svelte` 的 `currentSession.subscribe(syncRouteFromSession)` 保留，但 `currentSession` 只由 workbench 写；Sidebar 等调用 `openSession` 而不是直接 set。
- 当没有打开任何 tab 时，显示 EmptyPane；新建会话走 Draft tab，不依赖 `currentSession=''` 的单例路径。

## 5. 数据模型

### 5.1 类型（JSDoc）

WebUI 是 JS；用 JSDoc typedef 提供类型，不引入 TS 编译步骤。

```js
/**
 * @typedef {"session" | "draft"} LayoutTabKind
 * @typedef {{ kind: "session", id: string, sessionId: string }} LayoutSessionTab
 * @typedef {{
 *   kind: "draft",
 *   id: string,
 *   draftId: string,
 *   workDir?: string,
 *   model?: string,
 *   provider?: string,
 *   expertId?: string
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
 *   selectedTabId?: string,
 *   mode?: "chat" | "trajectory"
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

`mode` 存在 group view 上（每个 pane 一个），而不是 tab 上：同一个 pane 切换会话时继续显示用户当前的 Chat/Trajectory 选择；如果产品希望每个会话记住自己的模式，再把它移到 tab 上（schema 升级）。

Draft tab 只携带新会话所需的本地选择（workDir/model/provider/expertId），不携带消息；首次发送成功后由 `replaceDraft` 原子换成 `session` tab，消息/run 永不走 draft 分支。Draft 允许随布局一起持久化，composer 文本另由 `composer-drafts.js` 保存，避免刷新丢输入。

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
13. `mode` 只能是 `chat`/`trajectory`；非法值回退 `chat`。

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
          { "kind": "session", "id": "session:abc", "sessionId": "abc" },
          { "kind": "session", "id": "session:def", "sessionId": "def" }
        ],
        "selectedTabId": "session:abc",
        "mode": "chat"
      },
      "g2": {
        "tabs": [
          { "kind": "session", "id": "session:ghi", "sessionId": "ghi" }
        ],
        "selectedTabId": "session:ghi",
        "mode": "trajectory"
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
 *   | { code: "session-running", sessionId: string }
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
| `setPaneMode` | `(state, groupId, mode)` | `chat`/`trajectory`；不改变 topology | group missing |
| `setPreset` | `(state, presetId)` | 替换 frame，重分配现有 tab | unknown-preset |
| `capturePreset` | `(state)` | 返回资源无关 frame 模板 | 无 |
| `reconcileSessions` | `(state, deletedSessionIds)` | 移除已删除 session 的 tab | 无 |
| `normalize` | `(raw)` | 解码/修复/回退 | 无（内部） |

### 6.3 关键语义

**openSession。** 目标 group 默认 `attention.focusedGroupId`；若 tab 已存在，只在那个 group 选中/聚焦，不复制。插入位置：selected 之后，否则末尾。软上限建议每 group 16、全工作区 32；超出返回 `tab-limit`。

**openDraft / replaceDraft。** Draft tab 是新会话的唯一入口，不再用 `currentSession=''` 表达“新聊天”。首次发送时先 `POST /api/session-id`，成功后 `replaceDraft` 把 draft tab 换成 session tab；失败则保留 draft 和草稿。若该 session 已被其他 tab 打开，合并到已有 tab 并关闭 draft，避免同一 session 两个 composer。

**closeTab。** 关闭 selected 时选前一个，没有再选后一个；group 空则 `selectedTabId=undefined`，pane 显示 EmptyPane。关闭非 selected 不影响 selected。**关闭 tab 不删除 session**，也不取消后台 run。运行中的 tab 关闭不弹阻塞确认，只给一次非阻塞提示“任务将在后台继续”；Sidebar 仍可重新打开。

**moveTab。** `index` 是移除源 tab 后的目标下标，夹紧到 `[0, dest.tabs.length]`；同 group 同位置或原位置+1 返回 `same-position`。源 group 变空保留。

**splitGroup。** `direction: horizontal` 左右，`vertical` 上下；`side: before|after`。新 group 先加入 frame/view（空 view），再移动 tab，避免 `moveTab` 目标 group 不存在。4 叶上限。

**removeGroup。** 优先前兄弟、再后兄弟；tabs 追加到目标 group 末尾，保留目标 selected；最后一个 leaf 返回 `last-group`；focused 指向被删 group 时改为目标。

**setPreset。** 按当前 frame 深度优先收集 tabs，再按新 frame 深度优先分配；每个 leaf 至少一个（若 tab 足够）；焦点 session 所在 leaf 成为 focused；不关闭 session。

**setPaneMode。** 只改 `view.groups[groupId].mode`；不触发 shapeChanged；用于 `SessionHeader` 的 Chat/Trajectory 切换。桌面与移动端都可用。

**normalize。** `null`/版本不符 → `defaultState()`；重复 id / 孤儿 view / 缺失 view / 重复 sessionId / selected 不存在 / 非法 weights / >4 叶 / 非法 mode 逐项修复；不可修复回退默认。修复 warnings 只在测试/诊断可见。

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

- `projectionEpoch` 在 `shapeChanged` 时 +1；`resizeSplit`/`selectTab`/`focusGroup`/`setPaneMode` 不增加。
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
  const tab = { kind: "session", id: `session:${sessionId}`, sessionId };
  const group = state.view.groups[targetGroupId] ?? { tabs: [], mode: "chat" };
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

这意味着“后台会话的状态已经是完整的”，只是没有第二个 `Chat` 组件来画它。多 pane 的最小改动不是重写 reducer，而是：

1. **把这段事件消费抽到 `lib/session-stream.js`**，由它统一更新 `sessionRunStates`；`ChatPane` 不再消费 `$runEvents`。
2. **让每个 `ChatPane` 都从 `$sessionRunStates[sessionId]` 读取视图**，而不是用局部 `messages` 作为唯一源。
3. 局部变量只保留真正属于 UI 的东西：`prompt`、`pendingAttachments`、滚动位置、弹窗开关、命令建议。

### 7.2 `session-stream.js` 设计

```js
// ui/src/lib/session-stream.js
import { get } from "svelte/store";
import { runEvents } from "./stores.js";
import { sessionRunStates, getSessionState, updateSessionState } from "./session-runs.js";
import { viewFromSessionState, sessionStateWithView, reduce... } from "./session-view.js";

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
    for (const item of pending) {
      if (item?.type !== "session_event" || !item.sessionId) continue;
      applySessionStreamEvent(item.sessionId, item, {
        visible: visibleSessionIds.has(item.sessionId)
      });
    }
  });
}

function applySessionStreamEvent(sessionId, item, { visible }) {
  const event = normalizeStreamEvent(item);
  if (event.event === "transcript") {
    updateSessionState(sessionId, (state) => {
      const { view, effects } = reduceTranscriptEvent(viewFromSessionState(state), event.data, t);
      return withEffects(sessionStateWithView(state, view), effects);
    });
    ...
  }
  // run_event / status / done / error / capability / approval / question ...
  // 这里复用 Chat.svelte 现有的 handleSessionStreamEvent 分支，
  // 但把 “visible ? 写局部 : 写 store” 改为“总是写 store”，
  // 把 UI 副作用写成 state.effects（见 7.3）。
}
```

关键点：

- `session-stream.js` 是**唯一**消费 `runEvents` 的地方；`App.svelte` 在 `onMount` 启动一次，`onDestroy` 停止。**只有 Workbench 路径启动它**；Lite/旧 `Chat.svelte` 路径继续由 Chat 自己消费，二者由 `workbenchEnabled` 互斥选择，避免 `assistant_delta` 双写。
- 启动顺序：在 `connectRuns()` 之前启动 dispatcher；首次订阅时从当前 `runEvents` 的最后一个 `wsSeq` 之后开始（或明确从 0 归约缓冲），并保证 HMR/重复启动不会把同一事件归约两次。
- `visibleSessionIds` 来自 workbench：所有 pane 的 selected session 集合。它只影响“是否刷新列表/是否弹错误/是否触发子代理副作用”，不影响是否归约事件。
- SSE fallback（`/api/sessions/:id/stream`）仍由 `session-stream.js` 管理，按 session 注册 observer；不能由多个 `ChatPane` 各自开 SSE。
- `runEvents` 的 `wsSeq` 是单调的；dispatcher 用模块级 cursor，避免每个 pane 各自算。
- `stores.js` 里对 `runtime_event` 更新 `sessions` 列表（running/execution 徽标）是元数据投影，不是第二条 session view 归约；`session-stream.js` 仍是唯一把事件写进 `sessionRunStates` 的模块。
- 事件归一化（`started/finished/...` → `run_event`）沿用 `Chat.svelte` 现有逻辑。

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
  modelOverride: "",       // 该 pane 的发送模型；空则回退 runtime/config
  providerOverride: "",
  effects: {                // UI-only, consumed by ChatPane
    scrollEpoch: 0,         // 新消息/强制滚动
    refreshEpoch: 0,        // done 后刷新列表/统计
    subAgentEpoch: 0,       // 子代理变化
    errorNotice?: string
  }
}
```

`reduceTranscriptEvent` 等在 `session-view.js` 里返回 `{ view, effects }`；`session-stream.js` 把 `effects` 转成 `effects.scrollEpoch++` 等，不直接在全局 DOM 上操作。`ChatPane` 用 `$: if (state.effects.scrollEpoch !== lastScrollEpoch) ...` 调用自己的 `scrollChatToBottom()`。这样每个 pane 的滚动是独立的。

历史窗口状态必须进 store，不能继续留在 `Chat.svelte` 局部：现有 `earliestSeq`/`hasMoreHistory`/`historyAutoLoadReady`/`historyLoadVersion` 与 `isOlderThanLoadedHistory` 是按“当前会话”写的；多 pane 后后台 session 的 replay 帧需要自己的窗口边界，否则会把旧消息插进分页区域。

### 7.4 `ensureSessionLoaded(sessionId)`

每个打开的 tab 首次成为可见/被选中时加载：

```js
export async function ensureSessionLoaded(sessionId) {
  const state = getSessionState(sessionId);
  if (!sessionId || state.loading || state.status === "ready") return;
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

- **去重**：`loading`/`status` 防止多个 pane 同时对同一 session 发起加载。
- **分页**：`loadOlderSessionMessages(sessionId)` 使用 `getSessionMessagesBefore`，按 session 写入；不再依赖 `$currentSession`。
- **stale**：非可见且非 running 的 session 若被淘汰，重开时 `ensureSessionLoaded` 重新拉取。
- **加载优先级**：可见 pane > running > 最近聚焦；一次最多并发 2–3 个，避免页面一开 20 个 tab 同时打满 API。
- **历史窗口**：沿用 50 条/页 + 顶部自动加载；每个 state 保存 `hasMore`/`earliestSeq`，`isOlderThanLoadedHistory` 改为读该 session 的 state，而不是组件局部变量。

### 7.5 `ChatPane` 抽取

目标：把 `views/Chat.svelte` 变成：

```text
views/Chat.svelte                 # 兼容路由的薄 wrapper：<ChatPane sessionId={$currentSession} />
components/chat/ChatPane.svelte   # 真正的内容；props: sessionId, active, mode, onModeChange
```

`ChatPane` 的改动：

- 所有 `$currentSession` 目标改为 `sessionId` prop；所有 `$currentSession === id` 改为 `id === sessionId`。
- 视图来源从局部 `messages/chatEvents/sessionRunEvents/sessionRuntimeValue` 改为 `$sessionRunStates[sessionId]` 的派生选择（见 7.6）；局部只保留 `prompt`、`pendingAttachments`、`scrollTop`、弹窗、命令建议、`showRuntimePanel` 等 UI 状态。
- 模型/provider 改为 **per-pane**：`selectModel`/provider 变更写该 session 的 `modelOverride`/`providerOverride`；发送时按 `override → runtime → catalog default` 解析，禁止继续从全局 `$selectedModel` 取发送模型，否则两个 pane 会互相覆盖。
- `lib/chat-runtime-state.js` 的 `createSessionRuntimeManager` 不再依赖 `deps.currentSession()`：每个 `ChatPane` 传固定 `sessionId` 创建自己的 manager，或改成按 session 注册的共享 registry；轮询只在该 pane 挂载且存在 active run 时开启，`onPersist` 回调删除。
- Draft tab 由 `DraftPane.svelte`（复用 composer/附件/模型选择）承载，或让 `ChatPane` 支持 `draftId` 模式；首次发送走 `session-actions.allocateSession()` + `workbench.replaceDraft(draftId, sessionId)`，不创建第二条执行路径。
- `applySessionViewReducer` 的“当前会话写局部”分支删除；所有 reducer 结果写 `sessionRunStates`。`session-stream.js` 做归约；`ChatPane` 只读。
- `persistLocalSessionState`/`restoreLocalSessionState` 删除；不再有“切走前搬运”。
- `busy/canStop` 从 `$sessionRunStates[sessionId].runtime` + `completion` 计算。
- `sendPrompt`、`stop`、`approval`、`question`、`applyConfig`、`attach` 等显式传 `sessionId` 或读取当前 pane 的 state；不读 `$currentSession`。
- `SessionHeader` 的 `sessionID`、`view`、`onViewChange` 改为 props；`view` 来自 `group.mode`，`onViewChange` 调 `workbench.setPaneMode(groupId, mode)`。
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

- `prompt`/`pendingAttachments` 现在是 `ChatPane` 局部状态；**同一 pane 切换 tab 会卸载 ChatPane**，文本会丢。新增 `lib/composer-drafts.js`：
  - `Map<sessionId, { text, attachments, updatedAt }>`；
  - `ChatPane` 挂载时读取，`oninput` 同步写 Map（不触发 store），发送/清空时删除；
  - LRU 上限 16，非可见/非 running 的草稿可淘汰。
- 滚动位置：`Map<sessionId, { scrollTop, following }>`；`ChatPane` 挂载恢复。降级/Lite 形态可先不做精确像素，只保证消息不丢。
- 弹窗（runtime panel、subagent modal、MCP config、attachment lightbox、approval center）是 pane 局部 UI；不要用全局单例，否则两个 pane 会互相打开。审批中心可以保留一个全局入口 `AttentionBell.svelte`，列出所有 pane/tab 的 pending decision 并一键聚焦；但具体审批卡片仍在所属 pane 内。
- 关闭运行中的标签不弹阻塞确认，只提示“任务将在后台继续”，并保留 Sidebar/tab 徽标；易用性优先，但后台 run 不被前端取消。
- `document.title`/StatusBar 只显示焦点 pane 的 session；后台 running/pending 数量用徽标表达，不改变焦点。

### 7.8 Lite 方案（W0 延期时的降级）

如果 W0 完整 `ChatPane` 无法按期合入，Lite 作为降级形态，不默认发布：

- `App.svelte` 在 chat 路由挂载 `Workbench.svelte`。
- 只有焦点 pane 渲染 `<Chat sessionId={focusedSessionId} />`（现有 Chat，少量改动接受 `sessionId` prop 或继续读 `currentSession`）。
- 其他 pane 渲染 `SessionSnapshot.svelte`：从 `$sessionRunStates[id]` 读取 `messages/toolEvents/runEvents`，用 `session-view.js` 的纯函数生成只读转录；顶部显示状态/审批数量，点击“聚焦”把该 session 设为 `currentSession`。
- `SessionSnapshot` 首次挂载时调用轻量 `ensureSnapshotLoaded(id)`（`getSessionMessagesLatest` + `getSessionRunEvents` + `getSessionSubAgents`），按 session 去重；否则刚打开的只读 pane 只能看到“页面打开之后”的事件，历史为空。
- Lite 的前提是 **焦点 Chat 常驻挂载**：现有 Chat 已经消费 `$runEvents` 并更新所有后台 session 的 state（`applySessionViewReducer` 的非当前分支）。如果 Workbench 在切 tab 时卸载焦点 Chat，后台归约会停；此时必须先落地 `session-stream.js`，不能靠 Lite 绕过。
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

- `session-stream.test.js`：两个 session 交错事件，各自 state 正确；重复 `wsSeq` 不重复归约；`assistant_delta` 不会双写；SSE fallback 只开一个。
- `session-state.test.js`：`ensureSessionLoaded` 去重、stale 重载、LRU 淘汰 running 不淘汰；历史窗口 `earliestSeq/hasMore` 按 session 隔离。
- `model.test.js`：`openDraft`/`replaceDraft`、per-pane model/provider、同 session 去重、运行中关闭 tab 不取消 run。
- `ChatPane` 不读 `$currentSession`、不直接消费 `$runEvents` 的 source-scan 测试（沿用仓库现有 `.test.js` 风格）。
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
      │  │  │     ├─ ChatPane.svelte (sessionId)
      │  │  │     └─ 或 SessionSnapshot.svelte (sessionId)
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
| Chat/Trajectory | `setPaneMode(groupId, mode)` |
| 应用预设 | `setPreset(presetId)` |
| 删除 session 成功 | `reconcileSessions(state, [id])` |

### 8.6 响应式：桌面分屏 / 移动单 pane

- `Workbench` 用 `$isMobile`（≤900px）做 Svelte 条件渲染，切换两套投影；不要只靠 CSS 把分屏隐藏掉。
- 桌面 ≥1024px：按 frame 渲染分屏，SplitView 的 `min-width`/`min-height` 保证 pane 不被压到 0；900–1024px 仍按单 pane + 标签条工作，不分屏。
- ≤900px：单 pane + 标签条（可切换会话），不渲染 SplitHandle、不支持拖拽标签；composer 沿用现有移动端样式。
- 窗口缩放只改变运行时投影；不重写持久化 `weights`。从窄屏回到宽屏时恢复原分屏比例。
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

先说结论：**v1 不包含 tty / todo / spec 可停靠面板**；本方案只做 session tab + 分屏。原因不是布局，而是数据/后端契约：

| 面板 | 当前 WebUI 是否已有数据/能力 | 本方案处理 |
| --- | --- | --- |
| `tty` / interactive terminal | **没有。** WebUI 没有 PTY 端点；`bash` 工具的 stdout 只出现在 transcript / trajectory 里，不能输入、不能交互 | **v1 不做**。Go 后端的落点、API、WebSocket 协议与安全边界见 `docs/proposal/serve-pty-terminal-proposal.md` |
| `todo` / `plan` | **有。** `internal/tools/plan.go` 产生 plan 消息；`sessionRunStates[id].messages` 里有 `kind: "plan"`；Chat 已有 TODO/plan 卡片；TUI 也有 sticky todo | **W3 可做只读 Plan 面板**：从当前 session 的最新 plan 消息派生；不新增存储、不新增工具 |
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
2. W3：`plan`（TODO）与 `trajectory` 的只读侧栏/底部面板，因为它们已有数据。
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
- 不包含 token、用户标识、消息、附件、cwd；布局只含 session id、draft id、几何与焦点。
- 不做 auth scope hash：布局不含敏感内容；多用户同 origin 最多看到彼此的不透明 session id，打开无权限/已删除 session 时显示空 tab 并允许关闭。
- 不监听 `storage` 事件；不同 tab 不实时互相覆盖视图，只在下一次读取/写入时 last-write-wins。
- 退出登录时可选择清理 `mothx.workbench.*`，但不是必需（易用性优先，不把清理做成登录门槛）。

### 9.3 文档与校验

- 文档：`PersistedWorkbenchV1`（见 5.1），只含 `version/frame/view/attention`。
- 读取：`JSON.parse` → `normalize()` → 不变量；不可修复回退 `defaultState()`（`single`）。
- 写入：只写持久化字段（剥离 epoch/clock/drag 草稿）；`JSON.stringify` 后 ≤ 64 KiB；超过则丢弃本次写入并 `console.warn`（不弹窗）。
- debounce 300ms；`pagehide`/`visibilitychange` 时 flush 一次；新 tab 启动时先 hydrate 再挂载 ChatPane。
- 版本高于当前：回退默认，不部分读取；在 `console.info` 记录一次（不含用户数据）。
- 损坏/字段缺失：局部修复（补 view、去孤儿、夹紧 weights、去重复 session/draft）；只有结构性损坏才整份回退。
- 不写任何消息、附件、cwd、标题、model、run 状态；draft tab 只存 workDir/model/provider/expertId，composer 文本由 `composer-drafts.js` 另存。

### 9.4 多 tab 行为

- 共享一份布局；A tab 打开/关闭 session 会成为下一次读取/写入的最近布局，B tab 不实时重排（避免编辑中被抢焦点）。
- 同一 session 可以在多个浏览器 tab 同时打开；后端 run/审批是权威，两个 tab 各自投影。审批 first-response-wins（后端 `DecisionService`）；前端不要加第二套“谁先点谁赢”。
- `runEvents` WebSocket 每 tab 一条；后端已有多个客户端的并发路径，不需要改动。
- 关闭最后一个 tab 不影响其他 tab；浏览器关闭所有 tab 后再打开，使用最后一次写入的布局。
- 两个 tab 并发写入时 last-write-wins；接受这个取舍，换取“新 tab 自动恢复工作区”的易用性。

### 9.5 响应式投影

- 布局文档与投影解耦：桌面 ≥1024px 渲染分屏；900–1024px 与 `isMobile`（≤900px）渲染单 pane + 标签条，不渲染 SplitHandle。
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
- **模型/provider**：发送参数来自该 pane 的 `modelOverride`/`providerOverride`，最终回退到 session runtime 与服务端默认；不要继续把全局 `selectedModel` 当发送源。
- **事件订阅**：`connectRuns()` 的 `runSubscriptions()` 目前基于 `sessions` 列表（最多 100）；工作台打开但不在列表前 100 的 session 需要确保它在 `sessions` 中（`refreshSessions`/`upsertSession` 已处理）。如果未来 session 列表分页很大，需把 workbench 打开的 session 加进订阅集合；这是**前端**的 `runSubscriptions` 调整，不需要改协议。
- **审批/提问**：每个 pane 使用 `sessionRunStates[id].runtime.pendingApprovals` 与 per-session pending question；全局 `activeApproval` 只作为“焦点 pane 当前选中”的镜像，`AttentionBell` 汇总所有 pane 的 pending 数量。后端的 first-response-wins 不变。
- **`sessionRuntime` 全局 store**：当前 `Chat.svelte` 把可见会话的 runtime 写进全局 `sessionRuntime`；多 pane 下不能代表所有 pane。改为 per-session `sessionRunStates[id].runtime`；`sessionRuntime` 仅保留给非工作台/兼容路径。
- **认证**：布局只存不透明 session/draft id 与几何，不存用户数据；不做 auth scope hash，不把布局当权限边界。打开无权限/已删除 session 时显示空 tab，后端接口照常拒绝。
- **CSP/安全**：布局只含不透明 id；渲染时按文本转义；不把 localStorage 内容插入 `{@html}`。

## 11. 迁移与发布

### 11.1 特性开关

- 灰度期：URL `?workbench=1` opt-in，或 Settings > Appearance 的本地开关（不写服务端 settings）。
- GA：默认开启；保留 `?workbench=0` 与设置关闭项一个版本；关闭时 `App.svelte` 继续渲染现有 `Chat.svelte`（移动端也回退到现有 Chat）。
- `localStorage` 的 `mothx.workbench.enabled` 只作为本地偏好/诊断，不作为唯一灰度通道。
- 开关切换不需要刷新；切换时把当前 `currentSession` 作为焦点 session 插入/聚焦。

### 11.2 路由与 Sidebar

- `Sidebar.openSession(id)` 改为 `workbench.openSession(id)`；若开关关闭，回退 `currentSession.set(id)` + `navigate`。
- `Sessions.svelte`/`Experts.svelte` 的打开会话同样走统一入口；避免一个页面直接 set `currentSession`。
- `App.svelte` 的 `syncSessionFromRoute` 在开关开启时调用 `workbench.openSession(routeSession)`（或 `focusSession`）；不存在则打开，不能直接 `currentSession.set`。
- `syncRouteFromSession` 保留：焦点变化写 `?session=`；避免循环（比较当前值）。
- 启动顺序：先 hydrate 布局，再订阅路由/焦点同步；否则初始 `/chat?session=...` 会被空焦点覆盖。前进/后退必须可逆地打开/聚焦 tab。

### 11.3 旧路径移除条件

- `Chat.svelte` 已拆成 `ChatPane`，且所有 `$currentSession` 作为“操作目标”的用法已替换为 `sessionId`；
- `session-stream.js` 是唯一 `$runEvents` 消费者（grep `runEventCursor`/`handleSessionStreamEvent` 只剩它）；
- 每个 pane 的模型/provider/草稿/历史窗口都已 per-session；Draft tab 与 AttentionBell 已覆盖；
- 桌面分屏与移动单 pane 都通过 `npm test`/`npm run e2e`；
- 开关开启时行为验收通过；删除开关与旧分支，更新本提案状态。

### 11.4 发布节奏

1. **内部**：W0 完成（所有 pane 可交互）后，用 `?workbench=1` dogfood；Lite 只作为 W0 延期时的降级开关。
2. **Beta**：设置开关默认关闭；收集多 tab/审批/移动端/窗口缩放反馈。移动端先验证单 pane + 标签条，不开放分屏。
3. **Stable**：桌面默认开启；保留一个版本的旧路径与 `?workbench=0`。
4. **Cleanup**：移动端单 pane 验收通过后删除旧路径/开关/`sessionRuntime` 兼容/未使用 CSS；更新 `docs/{en,zh}/serve.md` 的 WebUI 章节与 changelog。

### 11.5 回滚

- 用户：设置关闭或 `?workbench=0`，布局文档保留；移动端自动回退现有 Chat。
- 开发：revert `App.svelte` 的 chat 分支；`session-stream.js`/`ChatPane` 可独立保留（不改变单会话行为）。
- 数据：localStorage key 保留；没有服务端迁移。

## 12. 测试计划

### 12.1 纯模型单测（`ui/src/lib/workbench/*.test.js`）

现有 `ui/package.json` 的 `test` 是 `node --test src/lib/*.test.js`，不会递归子目录。新增测试时二选一：

- 把测试放在 `ui/src/lib/workbench.test.js`（单文件，最省事）；或
- 把脚本改为显式包含：`node --test src/lib/*.test.js src/lib/workbench/*.test.js src/lib/trajectory/*.test.js`。

建议用目录 + 显式脚本，测试文件按模块拆：

1. `openSession`：新建/已存在/指定 group/group 缺失/tab 上限。
2. `openDraft`/`replaceDraft`：插入/选中、替换后不残留、同 session 已打开时合并、失败保留草稿。
3. `closeTab`：selected/非 selected/最后一个/空 group 保留；运行中关闭不取消 run（状态层断言）。
4. `closeOtherTabs`。
5. `moveTab`：同 group 前后/跨 group/末尾/非法 index/same-position。
6. `splitGroup`：水平/垂直、before/after、带 tab/不带 tab、4 叶上限。
7. `removeGroup`：有/无 tabs、重定位前/后兄弟、最后一个 leaf 拒绝。
8. `resizeSplit`：夹紧/归一化/非法输入、shapeChanged=false。
9. `focusGroup`/`selectTab`/`setPaneMode` 非法 id/mode。
10. `setPreset`：1/2/3/4 叶、0/1/5 tab、焦点保持/回退。
11. `capturePreset` 不含 session。
12. `normalize`：null、错误版本、重复 group/split id、孤儿/缺失 view、重复 sessionId/draftId、selected 不存在、非法 weights、>4 叶、空 split、非法 mode、超长 id。
13. `reconcileSessions`：删除聚焦/非聚焦/最后一个 tab。
14. per-pane model/provider：两个 session 各自选择不互相覆盖；发送解析顺序 override → runtime → default。
15. property test：固定 seed 生成 1000 个操作序列，每步断言 5.2 的 13 条不变量。
16. 序列化 round-trip：`encode(decode(doc))` 稳定；runtime 字段不出现；重启恢复 last-layout。

### 12.2 会话流/状态单测

`ui/src/lib/session-stream.test.js`（Node `node:test`，用假 store/假 fetch）：

1. 两个 session 交错 `session_event`，各自 `sessionRunStates[id]` 的 messages/runEvents 正确，互不污染。
2. 同一 `wsSeq` 重复/乱序不重复归约；`assistant_delta` 只追加一次；trim 后 cursor 不 stall。
3. `done`/`error` 的 run identity 检查：旧 run 的延迟帧不结束新 run。
4. SSE fallback：每个 session 只开一个 observer；切后台/关闭后停止。
5. `visibleSessionIds` 只影响 effects（scrollEpoch/refreshEpoch），不影响归约。
6. `ensureSessionLoaded` 去重、stale 重载、失败状态、running 不淘汰。
7. 历史窗口：两个 session 的 `earliestSeq/hasMore` 隔离；旧 replay 帧不会插入分页区域。
8. Composer 草稿：切换 tab 保留文本/附件；发送清空；LRU 上限；Draft tab 刷新不丢。
9. Attention：两个 pane 同时 pending approval/question 时，`AttentionBell` 数量正确，响应一个不影响另一个。

### 12.3 组件与 source-scan

现有 WebUI 没有组件测试基建；不引入 jsdom/testing-library 大依赖。建议：

- 把 `SplitView` 的权重计算、`TabStrip` 的溢出计算、`previewDrop` 的目标判定抽成纯函数并单测。
- source-scan（沿用仓库现有 `.test.js` 风格）：
  - `components/workbench/*` 不 import `views/Chat.svelte`；
  - `ChatPane` 不以 `$currentSession` 作为操作目标，不直接消费 `$runEvents`，不从全局 `$selectedModel` 取发送模型；
  - `lib/workbench/*` 不 import Svelte/`stores.js`/`api.js`；
  - `App.svelte` 只在开关开启时挂载 Workbench；移动端走单 pane 投影；
  - `session-stream.js` 是唯一 import `runEvents` 的非 stores 模块。
- Svelte 编译期：`npm run build` 会捕获模板/响应式错误；`svelte-check` 目前不在脚本里，若引入需另评（本方案不新增）。

### 12.4 e2e smoke

扩展 `ui/e2e/`：

- `workbench.smoke.mjs`：启动 `mothx serve`（或现有测试 fixture），用浏览器自动化/现有 e2e 工具：
  - 打开两个会话（可用本地 fixture session）；
  - 拖拽/菜单分屏；
  - 断言两个 pane 都出现各自消息；
  - 刷新/重启页面，断言标签/比例/焦点恢复；
  - 1024px/1440px viewport 下布局不溢出；390/768px 下只出现单 pane + 标签条；窗口缩放不丢标签/焦点；
  - 新建 Draft tab 后发送，断言 tab 变成真实 session 且消息不会重复。
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

- `lib/session-stream.js`：唯一 `$runEvents` 消费者；SSE fallback；effects。
- `sessionRunStates` 增加 `loading/status/loadedAt/effects/earliestSeq/hasMore/modelOverride/providerOverride`。
- `components/chat/ChatPane.svelte`：props `sessionId/active/mode`；读 per-session state；动作显式 sessionId。
- `views/Chat.svelte` 变成薄 wrapper；单会话行为不变。
- `chat-runtime-state.js` 改为显式 sessionId；`composer-drafts.js`、滚动/历史窗口恢复。
- 删除 `persistLocalSessionState`/`restoreLocalSessionState` 的搬运路径（或先保留但不再作为唯一源）。

**退出标准：**

- 单会话功能与今天一致；两个 session 交错流式测试通过。
- `$runEvents` 只有一个消费者（source-scan）。
- `npm test && npm run build` 通过。


### Phase W1：标签页（单 pane）

**交付：**

- `lib/workbench/{types,model,normalize,presets,store,persistence}.js` + 测试。
- `Workbench.svelte`/`Pane.svelte`/`TabStrip.svelte`/`EmptyPane.svelte`。
- Sidebar/Sessions/Experts 接入 `openSession`；`currentSession` 焦点投影。
- 标签关闭/重排/溢出/右键菜单；`setPaneMode`；Draft tab（`openDraft`/`replaceDraft`）。
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

W0 是主路径；Lite 只作为 W0 延期时的降级开关，不默认发布。

### 13.2 Lite 方案（降级，不默认发布）

- 跳过 W0 的完整 ChatPane 抽取：保留现有 `Chat.svelte` 作为焦点 pane，新增 `SessionSnapshot` 只读 pane。
- 前提：焦点 Chat 必须常驻挂载；若切 tab 会卸载 Chat，则必须先落地 `session-stream.js`。
- 需要：workbench 标签/分屏、共享 localStorage、`ensureSnapshotLoaded`。
- 交付：能并排看，但非焦点 pane 不能直接发消息/审批。
- 升级路径：W0 完成后立即用 `ChatPane` 替换 `SessionSnapshot`，删除降级分支。

## 14. 风险与对策

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| `Chat.svelte` 184KB、86 处 `$currentSession`，一次性拆完回归多 | 高 | W0 的“单消费者 + per-session state”分阶段合入；每步保持单会话行为快照 |
| 多个 pane 重复消费 `$runEvents` 导致重复归约（`assistant_delta` 会双写文本） | 高 | `session-stream.js` 唯一消费者；source-scan + 交错流测试 |
| `sessionRuntime`/`activeApproval`/`selectedModel` 全局单值导致 pane 串台 | 高 | pane 读 `sessionRunStates[id]`；模型/provider/草稿/历史窗口 per-session；全局只做焦点镜像 |
| `chat-runtime-state.js` 仍以 `currentSession()` 为闭包，单轮询器 | 高 | 每个 ChatPane 固定 sessionId 创建 manager，或按 session 注册；轮询按 pane 生命周期停止 |
| Draft tab 首次发送的 session 分配/替换失败 | 中 | `openDraft`/`replaceDraft` 纯操作 + 失败保留草稿；测试同 session 合并 |
| 历史窗口仍留在组件局部（`earliestSeq`/`hasMore`） | 中 | 迁入 `sessionRunStates`；后台 replay 帧按 session 窗口过滤 |
| 共享 localStorage 多 tab last-write-wins | 中 | 接受该取舍；布局只存不透明 id/几何；提供重置/恢复默认 |
| 刷新后布局引用已删除 session | 中 | `reconcileSessions` 只删“确认删除”的 id；未知 id 保留为失败/空 tab，打开时再报错 |
| `runEvents` 只订阅 sessions 前 100 条 | 中 | workbench 打开的 session 调 `upsertSession`/`refreshSessions`；`runSubscriptions` 加入打开的 session |
| 移动端被工作台挤坏 | 中 | `isMobile` 下只渲染单 pane + 标签条；不渲染 SplitHandle；390/768 纳入验收 |
| 4 pane 同时流式卡顿 | 中 | per-session derived；只挂载 selected body；measure；必要时降低非焦点 Markdown |
| 特性开关双路径腐烂 | 中 | 明确移除条件与版本；开关只包壳；W4 删除 |
| 路由/currentSession 循环更新/启动覆盖深链 | 中 | 单一写者（workbench）；先 hydrate 再订阅；路由变化走 `openSession`；测试前进/后退 |
| Svelte 5 legacy `$:`/store 语法与 runes 混用 | 低 | 沿用现有 legacy 语法；不引入 runes，保持文件一致 |
| 测试脚本不覆盖子目录 | 低 | 更新 `ui/package.json` test glob，或测试放 `src/lib/workbench.test.js` |

## 15. 开放问题与建议

| 问题 | 选项 | 建议 |
| --- | --- | --- |
| 完整交互 pane 还是 Lite 只读？ | 完整 / Lite | **完整 W0 为主路径**；Lite 仅在 W0 延期时作为降级开关，不默认发布 |
| 默认预设？ | single / two-column | **single**；首次不突然分屏 |
| 每个 pane 的 Chat/Trajectory 模式存哪？ | group / tab | **group**（v1）；切会话保留用户当前模式 |
| 关闭标签是否提示后台运行？ | 阻塞确认 / 非阻塞提示 | **非阻塞提示一次**“任务将在后台继续”；易用性优先，不打断操作 |
| 新浏览器 tab 是否继承最近布局？ | 是 / 否 | **是**；同一 origin 共享 last-layout，刷新/重启/新 tab 都恢复 |
| 审批显示在哪个 pane？ | 所属 pane / 全局中心 / 两者 | **所属 pane + AttentionBell 汇总**；不另做全局模态，避免两个模态打架 |
| 最小支持宽度？ | 1024px / 1280px | **桌面分屏 1024px**；900–1024px 单 pane；≤900px 移动单 pane + 标签条 |
| 是否持久化 pane mode？ | 是 / 否 | **是**（group.mode），刷新后保持 Trajectory |
| 是否允许同一 session 在多个 pane 打开？ | 允许 / 禁止 | **工作台内禁止**（同一文档唯一），避免两个 composer 同时发送；跨浏览器 tab 允许各自投影，后端是权威 |
| 是否做自定义布局预设？ | 是 / 否 | **v2**；v1 只内置 4 个 |
| 是否在 v1 做 tty/todo/spec 面板？ | 做 / 不做 | **不做 tty/spec；todo(plan) 可在 W3 做只读面板**。tty 需要 PTY 后端，见 `docs/proposal/serve-pty-terminal-proposal.md` |

## 16. 验收清单

- [ ] 同一 `/chat` 页面可并排看到两个 session 同时流式输出，内容不串。
- [ ] Sidebar 点击已打开会话只聚焦；关闭标签不删除 session。
- [ ] 关闭运行中标签不阻塞，后台继续；侧边栏状态正确，可重新打开。
- [ ] 新建 Draft tab，首次发送后变成真实 session；文本/附件不丢、不重复。
- [ ] 每个 pane 可独立选择模型/provider，互不覆盖；发送参数来自所属 pane。
- [ ] 4 pane 上限生效；第 5 个分屏/拖拽边缘被拒绝并给出原因。
- [ ] 拖拽重排/跨 pane/边缘分屏与预览一致；Escape 取消。
- [ ] 分隔条拖动/双击/键盘调整；刷新后比例恢复。
- [ ] 刷新、重启浏览器、新 tab 后标签/焦点/pane mode 恢复；损坏/超大/未来版本回退 single。
- [ ] 多浏览器 tab 共享 last-layout；last-write-wins 不破坏可恢复性。
- [ ] 桌面 1024/1280/1440/1920px 分屏可用；390/768px 单 pane + 标签条可用、无横向溢出。
- [ ] 审批/提问在所属 pane 显示；后台 pending 有 AttentionBell 入口；不会因另一个 pane 的 runtime 被覆盖。
- [ ] 关闭特性开关后行为与旧版一致；移动端回退现有 Chat。
- [ ] `cd ui && npm test && npm run build` 通过；有浏览器时 `npm run e2e` 通过。
- [ ] 双语 i18n 完整；无硬编码用户可见文案。
- [ ] 文档/changelog 更新；本提案状态改为 Implemented。

## 17. PTY / Terminal 后端方案

v1 不做 interactive terminal。PTY 是独立的 serve-only 后端能力，不属于布局方案；包归属、HTTP/WS 协议、配置、安全与测试见 `docs/proposal/serve-pty-terminal-proposal.md`。布局方案只在 W3 之后按该提案的 feature 投影 `TerminalPane`，不把 PTY 塞进 `internal/tools/bash.go` 或 session pool。

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
    view: { groups: { g1: { tabs: [], mode: "chat" } } },
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
- 所有 id/sessionId/draftId 字符串 ≤ 256；draft tab 必须带 `draftId`。
- `weights` 长度 2、有限、`[0.1, 0.9]`、和约 1。
- `mode` ∈ `{chat, trajectory}`，否则 `chat`。
- `JSON.stringify(doc).length <= 65536`。
- 未知字段丢弃；返回 sanitized clone。
- 不写消息/附件/cwd/标题/model/run 状态。

## 附录 E：实现顺序速查

1. W0：`session-stream.js` + `ChatPane` 抽取；接真实 session，单会话行为不变。
2. `workbench/types.js` + `defaultState` + 不变量断言。
3. `normalize.js` + 非法文档测试。
4. `model.js` 的 open/close/draft/move/split/resize/setMode，逐操作测试。
5. `presets.js` + 分配测试。
6. `workbench/store.js`（内存 commit + focus）。
7. `Workbench.svelte`/`Pane.svelte`/`TabStrip.svelte` + `ChatPane` 跑通单 pane。
8. Sidebar/Sessions/Experts/路由/`currentSession` 焦点同步 + 共享 localStorage 恢复。
9. `SplitView`/`SplitHandle`/pointer 拖拽/响应式投影。
10. Draft tab、AttentionBell、预设、e2e、性能、清理旧路径。
