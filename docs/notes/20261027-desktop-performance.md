# 2026-10-27 Desktop 卡顿性能治理

> 症状：Desktop 界面卡顿、任务列表/长转录滚动掉帧、启动后任务列表出现很慢。
> 范围：`desktop/renderer`（React 投影）、`desktop/main`（Electron 壳 + ACP 传输）、
> `internal/session`（首次列表的 EventStore 展示投影）。不改 ACP/Runtime 语义。

## 1. 主要根因

### 1.1 Renderer：每个流式 chunk 重渲染整个壳层

- `core/state.ts` 的 `emit()` 原本同步递增版本并通知所有
  `useSyncExternalStore` 订阅者。provider 每个 chunk 都会更新 transcript，因此
  App、侧边栏、状态栏、标题栏、Composer、计划面板都会按 token 重渲染。
- `core/bootstrap.ts` 对 `session-update` / `session-event` / `reverse-request`
  在投影函数已经 `emit()` 后再次 `emit()`，等于每个事件双倍渲染。
- 转录条目全部就地 `Object.assign`/`+=`，React 无法区分变化项，所有 Markdown
  消息和工具卡在每个 chunk 都要重新解析/渲染。
- 侧边栏每次渲染都重新排序、过滤、拼 FLIP 签名，并对每个会话做 O(n) 的
  title/pin 查找。
- 长转录没有虚拟化或 `content-visibility`，滚动时所有历史 DOM 都参与布局。

### 1.2 Electron：Linux 打包版无条件软件渲染

- `main/index.ts` 对 Linux 打包版无条件 `appendSwitch('disable-gpu')` 和
  `disable-dev-shm-usage'`。正常桌面也因此失去 GPU 合成，滚动/动画明显掉帧。
- 每个 ACP session update 都是一次独立 `webContents.send`，流式任务会跨进程
  发送大量小 IPC 消息。
- 诊断日志默认对每条 ACP stderr 都向 renderer 推送 IPC，即使日志面板未打开。

### 1.3 首次任务列表：串行重放 EventStore

- `mothx/session/listAll` 首屏为每个会话调用
  `loadCommittedEventEntries` 完整重放 EventStore，再取标题/预览/绑定。
  50 个会话本机约 1–3 秒；期间 `Tab` 已可显示，但任务树要等它完成。

## 2. 已实现

### 2.1 Renderer

- `core/state.ts`：通知合并到 16ms 一次（约 60fps），state 变更仍同步；
  新增 `flushPendingEmits` 供测试/启动路径使用。
- `hooks/useAppState.ts`：新增 `useAppSelector` + `shallowEqual`。
  App、Sidebar、StatusBar、TitleBar、Composer、HomeView、ChatView、HistoryView
  以及设置页 16 个面板只订阅自己展示的切片。
- `core/bootstrap.ts`：移除重复 emit；store/appInfo 与 startup 列表并行加载。
- `core/transcript.ts`：条目改为不可变替换；`TranscriptEntry` 与 `Markdown`
  使用 `memo`，未变化条目跳过渲染。
- `ChatStream`：自动跟随滚动合并到 rAF；每个条目外包一层
  `.transcript-entry { content-visibility: auto }`，视口外跳过布局/绘制。
- `Sidebar`：排序/搜索/FLIP 签名改为 `useMemo`；子组件不再逐个订阅全局 store。
- `HomeView`：移除 hero 光晕/鼠标视差；快捷卡 spotlight 改为整个网格统一委托 + rAF 合并（每帧最多测量一次卡片），不再每次 pointermove 强制布局。
- 用户背景图全局生效时降低大面积 `backdrop-filter` 半径（surface veil 3px、控制/标题栏 5px、composer 6px、首页快捷卡 8px），减少 GPU 合成成本；无背景图时保持原效果。

### 2.2 Electron main

- `main/linux-flags.ts`：Linux 打包版仅在缺少 `DISPLAY`/`WAYLAND_DISPLAY`
  时关闭 GPU；`MOTHX_DESKTOP_DISABLE_GPU` 可显式覆盖。`/dev/shm` 仅在不可写、
  缺失或小于 128MB 时使用 `/tmp` 回退。
- `main/renderer-event-batcher.ts`：main→renderer 的 ACP 事件按 8ms/64 条合并为
  一次 IPC；preload 按原顺序展开为单个事件回调，renderer 协议不变。
- `main/diagnostic-logs.ts` + preload：诊断日志仍写入 console/文件/内存缓冲，
  但只有日志面板订阅时才跨 IPC 推送；renderer reload 时 main 自动复位。

### 2.3 Session 首次列表

- `EventStore.inspectProjection()`：只校验记录框架/序列连续性，跳过每 batch
  SHA-256 重算，且不写入 append cache；只读扫描使用 `O_RDONLY`。
- `InspectEventStoreProjection`：复用 intent/committed 校验，不做 canonical
  replay；真正打开会话仍走完整 `InspectEventStore` 并 fail-closed。
- `eventStoreDisplayFromBatches`：只解码首条 user 预览、最新标题与绑定元数据。
- `FillSessionDetailPreviews` / `LatestSessionBindings`：有界 worker 池
  `min(GOMAXPROCS, 8)` 预取当前页，再顺序填充。

## 3. Trace 复核（`Trace-20261027T171144.json.gz`）

24.3s DevTools trace（新 bundle，17:11 构建）显示：

- `BeginFrame` 稳定 16.6ms（无 >33ms 间隔），compositor 帧节奏本身正常；卡顿主要来自主线程长任务/输入延迟。
- 26 个主线程 `RunTask` ≥50ms，最大 305ms；19–20s 的两段 250ms `PageAnimator::serviceScriptedAnimations` 合计启动 **774 个 transition**，一次 `UpdateLayoutTree` 重算 **629 个元素/120ms**。属性为 color / border-*-color / background-color / scrollbar-color，58 个会话行同时参与，符合切换主题。
- 根因：`animate-in ... duration-200/300` 同时设置了 `transition-duration`，而 CSS 初始 `transition-property` 是 `all`。会话行/卡片在 hover、主题切换、继承 `scrollbar-color` 变化时会过渡所有属性。
- 根因：转录条目同时使用 `content-visibility: auto` 和 `animate-in`；`contentvisibilityautostatechange` 与 `enter` 动画时间点完全重合（14.26s / 15.38s / 16.82s），滚动露出条目时重新播放入场动画并触发布局。
- 侧边栏 `.autohide-scrollbar` 在 `scrollbar-width:none/thin` 与 `::-webkit-scrollbar` 0/8px 间切换；`ScrollLayer` 与 85.7ms `Layout` 相邻。
- 大量 `v8::Debugger::AsyncTaskRun` / transition EventDispatch 是 DevTools Performance 面板自身的观测开销，真实成本应低于 trace 数值。

对应修复：

- 转录条目移除入场动画（保留 `content-visibility`），避免滚动露出时重播。
- 移除 `animate-in` 旁的 `duration-*`；会话行保留无 duration 的入场动画。
- 主题切换时给 `<html>` 加 `theme-switching`，两帧内禁用 `transition`。
- 侧边栏滚动条保持恒定 8px 宽度，只切换 thumb 透明度/颜色，不再改变布局。

## 4. 实测

- 本机 `mothx acp` 首次 `mothx/session/listAll`：约 2.7s → 约 1.2s。
- Desktop renderer：`npm run typecheck`、`npm test`、`npm run build` 全通过。
- Go：`go test ./internal/session/... ./internal/acp ./internal/architecture` 通过；
  预览/绑定路径额外跑过 `-race`。

## 5. 长时间运行稳定性

- 转录渲染改为 300 条尾部窗口；滚离底部时冻结窗口，新流式内容不会把视口顶走；「加载更早」先在本地展开，再走 ACP 分页。
- `status` / `retry` / `compaction_start` 使用稳定 key，就地更新，不再为每次重试/压缩追加一条永远不删的状态行。
- `runningSessions` 的终态投影上限 200（只淘汰终态，live Run 永不淘汰）；renderer `sessionStatus` 与主进程一致上限 400。
- `sessionPages` 只保留当前历史搜索页，项目页缓存上限 32，删除项目会清理对应分支；已知 session 缓存上限 1000。
- 子代理列表每个会话上限 200（优先保留 running），删除会话时清理子代理/待决决策。
- 子代理面板、资料库、知识库设置轮询增加 in-flight guard，慢 ACP 响应不会每 2–4s 叠加请求。
- 转录条目查找改用 key → index 映射，流式 chunk 不再每次全表扫描；批量插入/删除后重建，索引失效时自愈。
- `useFlipList` 在元素卸载时清理 ref callback；`desktop.log` 启动时超过 8MB 轮转为 `.1`。

## 6. 后续可选优化

- 当前窗口化只限制 DOM/React 成本；极长任务的 `state.transcript` 字符串仍会增长。渲染层已把工具文本限制在 20k/40k；进一步截断 ACP/Runtime 展示副本需要先提供按需取全量内容的契约，否则会丢失可查看内容，暂不实施。
