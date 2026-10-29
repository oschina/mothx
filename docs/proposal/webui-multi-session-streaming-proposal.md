# WebUI 多 Session 并发执行与多流隔离方案设计

> 状态: Superseded（UI 布局与多 pane 部分由 `docs/proposal/webui-dynamic-workbench-layout-proposal.md` 承接；本文的 per-session 流隔离设计仍是其基础，保留作历史决策记录）
> 日期: 2026-07-19
> 目标版本: 待定

## 1. 概述

WebUI 当前可以浏览和切换多个 Session，但聊天执行路径仍以单个 `Chat.svelte` 组件内的一组全局运行状态为中心：`busy`、`chatAbort`、`chatStreamSessionID`、`messages`、`chatEvents` 等只允许一个主聊天 SSE 流被可靠管理。

这会导致一个明显的问题：Session A 正在执行时，用户切换到 Session B，前端会停止或替换当前页面持有的流订阅；来自 A 的后续事件又会因为“不是当前选中的 Session”而被忽略。用户感知为 A 被取消、输出消失，或返回 A 后状态不完整。与此同时，用户也无法自然地在 B 中再启动一个独立任务。

本提案的目标是让 WebUI 支持：

1. 多个**不同 Session** 同时执行；
2. 每个运行中的 Session 保持自己的 HTTP/SSE 流、`AbortController`、消息缓存和运行状态；
3. 切换 Session 只切换渲染视图，**绝不终止其他 Session 的流或任务**；
4. 用户点击“停止”时，仍然通过 Abort 终止**当前 Session 对应的任务**；
5. 多条流的事件按 `sessionId` / `runId` 路由到独立状态，绝不串到其他 Session；
6. 页面刷新、切回正在运行的 Session 或打开外部启动的 Session 时，能通过 Session Stream 进行恢复和补流。

本方案优先解决 WebUI 的多流状态管理问题。第一阶段不要求把 Agent 执行生命周期完全从 HTTP 请求生命周期中剥离：当前“浏览器主动 Abort 请求 = 终止该任务”的语义需要保留。

## 2. 背景与当前实现

### 2.1 相关目录与组件

| 层级 | 当前位置 | 职责 |
|------|----------|------|
| WebUI 路由与选中会话 | `ui/src/App.svelte` | URL `?session=` 与 `currentSession` 双向同步 |
| Session 列表 | `ui/src/components/Sidebar.svelte` | 选择历史 Session、显示 Session 列表 |
| 聊天页 | `ui/src/views/Chat.svelte` | 发起 completion、消费 SSE、渲染聊天和运行状态 |
| 前端共享状态 | `ui/src/lib/stores.js` | `currentSession`、Session 列表、runtime、工具状态等 |
| SSE 工具 | `ui/src/lib/api.js` | `readSSE()` 读取 fetch body 并派发 SSE frame |
| Completion 服务端 | `internal/serve/openaiapi/handler_chat.go` | 创建 Agent、执行、输出 OpenAI SSE / transcript |
| Session 管理 | `internal/serve/openaiapi/session_mgr.go` | `APISession`、每 Session 锁、SessionPool |
| Session SSE | `internal/serve/openaiapi/session_stream.go` | 历史 replay + 实时 Session 事件流 |
| 路由 | `internal/serve/run.go` | `/api/sessions/{id}/stream` 等管理 API |

### 2.2 服务端已有能力

服务端并非天然只能执行一个 Session：

- `SessionPool` 可保存多个 `APISession`；
- `APISession.mu` 用于串行化**同一个 Session** 的请求；不同 Session 不共享该锁；
- `APISession.running` 已可用于暴露单个 Session 是否执行中；
- `SessionPool.Pin/Unpin` 会保护正在使用的 Session，避免 idle cleanup 在执行期间回收；
- `sessionStreamHub` 按 `sessionID` 维护订阅者，能够向多个订阅者发布同一 Session 的事件；
- `GET /api/sessions/{id}/stream` 已支持 transcript、run event、capability event 的持久化 replay 与实时推送；
- Session 列表 API 已返回 `running` 字段。

因此本提案不需要把 SessionPool 改造成多 Session；核心工作是让 WebUI 正确利用已有的 Session 隔离和 Session Stream 能力。

### 2.3 当前前端单流模型

`ui/src/views/Chat.svelte` 当前以组件级变量维护主请求流：

```js
let busy = false;
let chatAbort = null;
let chatStreamSessionID = '';
let messages = [];
let chatEvents = [];
let sessionRunEvents = [];
let sessionCapabilityEvents = [];
let sessionStreamAbort = null;
let sessionStreamID = '';
let sessionStreamCursor = { entrySeq: 0, runSeq: 0, capabilitySeq: 0 };
```

发送消息时，聊天请求使用单个 `chatAbort`：

```js
chatAbort = new AbortController();
const res = await fetch('/v1/chat/completions', {
  method: 'POST',
  signal: chatAbort.signal,
  body
});
await readSSE(res.body, handleStreamEvent);
```

停止按钮直接调用：

```js
function stop() {
  if (chatAbort) chatAbort.abort();
}
```

这个 Abort 语义本身是正确的：当前服务端执行 Context 由 `r.Context()` 派生，浏览器 abort 该 fetch 后，请求 Context 取消，Agent 收到取消并结束。

问题是，组件状态同时又被用来表示“当前查看的 Session”。当用户切换 Session 时：

- 当前渲染的 `messages`、工具事件、run event 被整体重置或重新加载；
- Session Stream 订阅会被 `stopSessionStream()` 停止；
- 主聊天流仍只有一个控制器与一套消息容器；
- 收到旧 Session 的事件时，`acceptsChatStreamSession()` / `applyTranscriptStreamEvent()` 等逻辑会以 `$currentSession` 判断并拒绝处理非当前 Session 事件。

结果是“执行流所属 Session”和“当前页面选中的 Session”被错误地当作同一个概念。

## 3. 问题定义

### 3.1 必须区分的三个概念

| 概念 | 含义 | 切换 Session 是否应改变它 |
|------|------|---------------------------|
| 选中 Session（selected session） | 用户当前正在看的聊天页 | 会改变 |
| 运行 Session（running session） | 有正在执行 Agent Run 的 Session | 不应因查看其他 Session 而改变 |
| 流订阅（stream subscription） | 浏览器正在消费的一条 completion SSE 或 Session SSE | 不应因选择其他 Session 自动取消 |

当前问题来自把三者折叠为全局 `busy/chatAbort/messages`。修复后必须独立管理。

### 3.2 正确的 Abort 语义

本提案明确保留以下行为：

- 用户在 Session A 点击“停止”：前端 abort A 的主 completion fetch，服务端感知 `r.Context()` 取消，A 的 Agent Run 终止；
- 用户从 Session A 切换到 Session B：前端**不得** abort A 的 fetch，也不得停止 A 的 SSE reader；
- 用户在 B 点击“停止”：只 abort B 的 controller，A 不受影响；
- 单个浏览器标签关闭、刷新或页面卸载：该标签持有的 fetch 请求会中断，当前阶段内相应任务会被取消。这是第一阶段有意保留的语义，而不是 bug；如以后要支持“关闭浏览器后继续跑”，再单独引入后台 Run API / 显式 cancel endpoint。

### 3.3 不同 Session 并行、同一 Session 串行

需要坚持以下并发边界：

- **不同 Session**：允许同时运行；
- **同一个 Session**：只能有一个前台 completion Run；第二次发送应保留现有服务端 Session lock 的串行语义，产品层建议明确反馈“该会话正在执行”，而不是让第二个请求静默等待；
- **同一 workDir 的不同 Session**：允许并行，但 WebUI 应提示潜在文件修改冲突；不在第一阶段引入按工作目录全局锁。

## 4. 设计目标

| 目标 | 说明 |
|------|------|
| 多流并存 | 同一 WebUI 标签页可同时持有多个运行中 Session 的 completion SSE 流 |
| 流与 Session 强绑定 | 每条流在创建时固定绑定 `sessionId`，可选再绑定 `runId` |
| 独立渲染状态 | 消息、工具、运行事件、能力事件、审批和游标均按 Session 隔离 |
| Abort 精确终止 | 停止按钮只 abort 所选 Session 的对应请求，不影响其他流 |
| 切换不取消 | 切换聊天 Session 不 abort、不丢弃、不关闭其他运行 Session 的流 |
| 事件不串流 | 非当前 Session 的事件仍会入库到对应前端状态，但不会渲染到当前窗口 |
| 可恢复 | 使用 `/api/sessions/{id}/stream` 补齐刷新、切回、旁观和主流异常后的事件 |
| 保持兼容 | 继续使用 `/v1/chat/completions`、`x_session_id`、`x_transcript` 和现有 Session Stream |
| 渐进改造 | 优先前端状态模型；服务端只做稳定事件标识、可观测性和测试补强 |

## 5. 非目标

- 第一阶段不把 Agent Run 生命周期从 HTTP request context 中独立出来。
- 第一阶段不引入一个新的 WebSocket 聊天协议。
- 第一阶段不支持同一 Session 多条用户消息并发进入同一 Agent history。
- 第一阶段不保证浏览器刷新、关闭标签页后任务继续运行。
- 第一阶段不引入按 workDir 的**粗粒度全局锁**。同进程内对同一目标文件的 `write` / `edit` 冲突，应继续复用现有的进程级文件锁；该锁不是按整个工作目录串行化。
- 不改变 `settings.json` 或 `serve.json` 现有字段语义。
- 不在每个未选中 Session 的 DOM 中持续渲染完整聊天内容；后台只维护数据状态，当前窗口只渲染 selected session。

## 6. 总体架构

### 6.1 前端 Session Run Store

新增前端运行态管理层，建议新建：

```text
ui/src/lib/session-runs.js
```

它是 `Chat.svelte` 的状态控制器，不替代服务端 Session 数据源。其职责是：

1. 按 Session 创建、保存和释放请求 controller；
2. 消费每条 SSE；
3. 用流绑定的 `sessionId` 将事件路由到目标 Session；
4. 保存未选中 Session 的增量状态；
5. 暴露当前 Session 的派生 ViewModel；
6. 在重连和切换时通过 Session Stream 进行补流。

建议基础数据模型：

```ts
/** 一个浏览器标签页内的 Session 视图与运行态。 */
type SessionRunState = {
  sessionId: string;

  // 当前前台 completion 请求。存在时 abort 即取消该 Agent 任务。
  completion?: {
    controller: AbortController;
    status: 'starting' | 'running' | 'completed' | 'failed' | 'canceled';
    startedAt: string;
    runId?: string;
  };

  // 用于恢复/旁观的 Session SSE。其 abort 只停止观察，不负责取消任务。
  observer?: {
    controller: AbortController;
    source: 'session_stream';
  };

  messages: SessionMessage[];
  toolEvents: ToolStatusEvent[];
  runEvents: SessionRunEvent[];
  capabilityEvents: SessionCapabilityEvent[];
  runtime: SessionRuntimeSnapshot | null;
  pendingApprovals: SessionApprovalRequest[];

  cursor: {
    entrySeq: number;
    runSeq: number;
    capabilitySeq: number;
  };

  historyLoaded: boolean;
  streamCompleted: boolean;
  lastError?: string;
};

type SessionRunMap = Record<string, SessionRunState>;
```

建议使用 Svelte `writable`：

```js
export const sessionRunStates = writable({});
```

并由 helper 提供不可变更新，避免组件中直接修改嵌套对象：

```js
ensureSessionState(sessionID)
updateSessionState(sessionID, updater)
startCompletion(sessionID, request)
abortCompletion(sessionID)
startObserver(sessionID)
stopObserver(sessionID)
applyStreamEvent(sessionID, event, source)
releaseSessionState(sessionID)
```

### 6.2 两类流与职责边界

| 流 | 入口 | 主要用途 | Abort 后果 |
|----|------|----------|------------|
| 主 completion 流 | `POST /v1/chat/completions` | 用户从本标签发送消息后，实时接收该次 Agent 执行输出 | **取消 Agent 任务**（保留现有语义） |
| Session observer 流 | `GET /api/sessions/{id}/stream` | 切回运行中 Session、刷新恢复、旁观外部启动 Session、主流中断后的补流 | 只停止观察；不应主动取消服务端任务 |

约束：同一 Session 在主 completion 流健康时，原则上不再额外开启 observer 流，以避免两条流重复消费同一 transcript。若因恢复、旁观需要 observer，则必须使用 sequence cursor 去重。

### 6.3 流生命周期图

```text
Session A 发送消息
  │
  ├─ ensure SessionRunState(A)
  ├─ create AbortController(A.completion)
  ├─ POST /v1/chat/completions (x_session_id=A, x_transcript=true)
  └─ readSSE → applyStreamEvent(A, event, "completion")

用户切换到 Session B
  │
  ├─ 不调用 abortCompletion(A)
  ├─ 不停止 A 的 completion reader
  ├─ selectedSession = B
  ├─ B 的 UI 从 SessionRunState(B) 派生
  └─ 若 B 服务端显示 running 且本地没有健康 completion：startObserver(B)

用户点击 A 的 Stop
  │
  └─ abortCompletion(A)
       └─ fetch abort → request context canceled → A Agent 停止

用户返回 A
  │
  ├─ 渲染 SessionRunState(A) 中已持续累积的内容
  └─ 如本地流曾断开，按 cursor 发起 observer 补流
```

## 7. Session ID 与 Run ID 策略

### 7.1 新会话必须在请求前获得稳定 Session ID

当前新聊天可能在第一次 SSE frame 后才获知服务端创建的 Session ID，这使多流管理需要“根据流内容认领 Session”，容易发生竞态。

建议 WebUI 在首次发送前生成 ID：

```js
function newWebUISessionID() {
  return crypto.randomUUID();
}
```

首次发送逻辑：

```js
const sessionID = $currentSession || newWebUISessionID();
if (!$currentSession) {
  currentSession.set(sessionID);
}
```

请求中始终传入：

```json
{
  "x_session_id": "<sessionID>",
  "x_transcript": true
}
```

服务端 `getOrCreateSession()` 已支持 client-supplied ID 并通过 `InitWithID()` 创建 Session，因此此改造不需要改变存储模型。

收益：

- 请求创建前即可建立 `SessionRunState(sessionID)`；
- 流控制器、消息占位、运行状态从一开始即有明确归属；
- 不再需要 `chatStreamSessionID` 的“首帧认领”逻辑；
- 用户可在发送后立刻切到其他 Session，不依赖首帧到达时机。

### 7.2 Run ID 的使用

服务端在 `handleChatCompletions` 中已创建 `runID` 并将其持久化到 run event。建议在面向 WebUI 的事件中稳定携带该值。

`runId` 的作用：

- 区分同一 Session 的不同执行轮次；
- 防止上一次 Run 的迟到事件污染下一次 Run；
- 精确展示运行状态、取消原因和耗时；
- 将 approval request 与执行轮次绑定；
- 后续演进显式 Run cancel API 时可直接复用。

第一阶段不强制所有 OpenAI 标准 chunk 改协议；但 transcript、run_event、approval、tool_event 和 done 事件应包含或可推导出 `runId`。

## 8. 事件契约与路由规则

### 8.1 事件的最小身份字段

所有 WebUI 专用或扩展 SSE 事件建议满足：

```jsonc
{
  "sessionId": "sess-a",
  "runId": "run-a",        // 没有时允许为空，但运行态事件应尽量具备
  "seq": 42,                 // 持久化事件的序列号；即时 delta 可选
  "timestamp": "2026-07-19T10:00:00Z"
}
```

兼容字段规则：

- 现有 transcript 中的 `x_session_id` 继续保留；
- 前端内部统一解析为 `sessionId`；
- URL path 已明确 Session ID 的 Session Stream，可以以路径 `id` 作为保底归属；
- 收到 payload Session ID 与流绑定 Session ID 不一致时，视为协议异常：记录错误并忽略该 frame，不能把它写到任意 Session。

### 8.2 事件路由原则

错误模式：

```js
if (incomingSessionID !== $currentSession) return;
```

正确模式：

```js
function handleBoundStreamEvent(boundSessionID, event, source) {
  const payload = parseEvent(event);
  const eventSessionID = payload.sessionId || payload.x_session_id || boundSessionID;

  if (eventSessionID !== boundSessionID) {
    reportProtocolError(boundSessionID, eventSessionID, event);
    return;
  }

  applyStreamEvent(boundSessionID, payload, source);
}
```

只有渲染层根据 `$currentSession` 选择数据：

```js
$: currentState = $sessionRunStates[$currentSession] || emptySessionState($currentSession);
$: messages = currentState.messages;
$: busy = currentState.completion?.status === 'starting'
  || currentState.completion?.status === 'running'
  || currentState.runtime?.activeRun?.status === 'running';
```

即使 Session A 不在当前视图中，A 的事件也必须持续更新 `sessionRunStates[A]`。

### 8.3 消息与事件去重

同一个 Session 可能出现以下重叠来源：

- completion SSE 的即时 transcript；
- Session Stream 的实时事件；
- Session Stream reconnect 的 persisted replay；
- `GET /messages` 的完整历史刷新。

前端必须按实体身份去重，而不能仅按文本去重：

| 内容 | 首选唯一键 |
|------|------------|
| 持久化消息 | `entry.id`，否则 `entry.seq + role + toolCallId` |
| tool call / tool result | `toolCallId` |
| run event | `event.id`，否则 `runId + eventType + seq` |
| capability event | `event.id` 或 `seq` |
| assistant delta | 主流内按顺序 append；恢复后以持久化 assistant message 为准合并 |
| approval | `approvalId` |

`cursor.entrySeq/runSeq/capabilitySeq` 必须是每个 Session 独立字段，绝不能复用当前 `Chat.svelte` 的单个全局 cursor。

### 8.4 当前 SSE 的事件处理映射

| SSE event | 更新目标 |
|-----------|----------|
| `transcript` | 目标 Session 的 `messages`、sub-agent transcript、cursor |
| `tool_event` | 目标 Session 的 `toolEvents` 与对应 tool message |
| `run_event` | 目标 Session 的 `runEvents`、`completion.status` / runtime 摘要 |
| `capability_event` | 目标 Session 的 `capabilityEvents` |
| `runtime_event` | 目标 Session 的 `runtime`、pending approvals |
| `approval_request` | 目标 Session 的 `pendingApprovals`，可仅在该 Session 被选中时弹窗 |
| `approval_resolved` | 删除目标 Session 对应 `approvalId` |
| `done` | 只标记目标 Session 的当前流完成；不影响其他 Session |
| `error` | 只写入目标 Session 的 `lastError` / 当前 Run 状态 |

## 9. WebUI 详细改造

### 9.1 新增 `session-runs.js`

推荐把 SSE、Abort 和按 Session 的缓存逻辑从 `Chat.svelte` 拆出。`Chat.svelte` 保留 UI 交互和 selected Session 的 ViewModel 组合。

建议 API：

```js
export function ensureSessionState(sessionID) {}
export function getSessionState(sessionID) {}
export function startCompletion(sessionID, request) {}
export function abortCompletion(sessionID) {}
export function startSessionObserver(sessionID) {}
export function stopSessionObserver(sessionID) {}
export function loadSessionSnapshot(sessionID) {}
export function hydrateRunningSessions() {}
export function removeSessionState(sessionID) {}
```

`startCompletion()` 的关键形态：

```js
async function startCompletion(sessionID, request) {
  const state = ensureSessionState(sessionID);
  if (isRunActive(state)) {
    throw new Error('This session already has an active run.');
  }

  const controller = new AbortController();
  updateSessionState(sessionID, (current) => ({
    ...current,
    completion: {
      controller,
      status: 'starting',
      startedAt: new Date().toISOString()
    },
    streamCompleted: false,
    lastError: ''
  }));

  try {
    const response = await fetch('/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      signal: controller.signal,
      body: JSON.stringify(request)
    });
    await readSSE(response.body, (event) => {
      handleBoundStreamEvent(sessionID, event, 'completion');
    });
    markCompletionEnded(sessionID, 'completed');
  } catch (err) {
    markCompletionEnded(
      sessionID,
      err?.name === 'AbortError' ? 'canceled' : 'failed',
      err
    );
  } finally {
    clearCompletionController(sessionID, controller);
  }
}
```

要求：`sessionID` 是函数入参和闭包绑定值，不能在回调里通过 `$currentSession` 推断归属。

### 9.2 改造 `Chat.svelte` 的发送逻辑

发送前：

1. 解析当前 selected session；
2. 若是新会话，生成 UUID 并写入 `currentSession`；
3. 从目标 Session state 构造请求历史；
4. 仅检查目标 Session 是否 running；
5. 对该 Session 创建 completion 流；
6. 不修改其他 Session 的 controller、messages 或状态。

伪代码：

```js
async function sendPrompt() {
  const sessionID = ensureCurrentSessionID();
  const state = ensureSessionState(sessionID);

  if (isRunActive(state)) {
    setError($t('chat.error.sessionAlreadyRunning'));
    return;
  }

  const outgoing = takePromptAndImages();
  appendOptimisticUserMessage(sessionID, outgoing);

  await startCompletion(sessionID, {
    model: $selectedModel || 'default',
    stream: true,
    x_session_id: sessionID,
    x_working_dir: workDirForSession(sessionID),
    x_mode: modeForNewSession(sessionID),
    x_tools: sessionToolsFor(...),
    x_transcript: true,
    messages: buildRequestMessages(sessionID)
  });
}
```

原先的 `busy` 不能继续作为组件单例；改为当前 Session 的派生状态。

### 9.3 改造停止按钮

保留 Abort 终止任务语义：

```js
function stop() {
  const sessionID = $currentSession;
  if (!sessionID) return;
  abortCompletion(sessionID);
}
```

注意：

- `abortCompletion(sessionID)` 只能 abort `state.completion.controller`；
- 不应 abort `state.observer.controller` 来表示“停止生成”；observer 只是观察流；
- 停止后 UI 可先乐观标记 `canceled`，但最终状态应以服务端 `run_event` / completion 结束为准；
- 如果当前 Session 的运行不是由本标签发起，没有 completion controller，则第一阶段 UI 不显示“可停止”或明确展示“此运行由其他客户端发起，当前页面只能旁观”。后续引入显式 cancel API 后再支持跨客户端取消。

### 9.4 改造 Session 切换逻辑

当前切换逻辑中不得再把“切换视图”理解为“结束旧 Session 的实时工作”。

切换时允许：

- 停止**当前可见视图专用**的 observer（如果采用只观察 selected session 的策略）；
- 读取新 Session 的历史、runtime、run events；
- 如果新 Session 服务端为 running 且本地没有 active completion，则为其启动 observer；
- 更新 scroll、输入框、选中审批等纯 UI 状态。

切换时禁止：

- abort 任一 `completion.controller`；
- 清空非目标 Session 的消息缓存；
- 根据 `$currentSession` 忽略或 return 旧 Session 的 completion SSE frame；
- 用单一 `sessionStreamCursor` 覆盖其他 Session cursor。

推荐策略：

- **completion stream**：对每个由本标签发起的运行始终保留，直到该 Run 结束；
- **observer stream**：第一阶段只维护当前 selected 且服务端 running、但没有本地 completion 的 Session；切换时可关闭旧 observer，因为它不控制任务；
- **缓存**：所有 completion 流都持续更新各自 SessionRunState；未选中 Session 不需要 observer 才能保持本地发起 Run 的输出。

### 9.5 渲染层改造

`Chat.svelte` 不再拥有全局的 `messages`、`chatEvents` 和 `sessionRunEvents` 真相来源。可以先以最小风险方式保留局部变量作为 selected-session projection：

```js
$: selectedState = $sessionRunStates[$currentSession] || emptySessionState($currentSession);
$: messages = selectedState.messages;
$: chatEvents = selectedState.toolEvents;
$: sessionRunEvents = selectedState.runEvents;
$: sessionCapabilityEvents = selectedState.capabilityEvents;
$: sessionRuntimeValue = selectedState.runtime;
$: busy = isRunActive(selectedState);
```

后续可将组件局部变量完全删去，直接用 store 数据渲染。

审批 UI 也必须按 Session 隔离：

- 收到 A 的 `approval_request` 时写入 A 的 `pendingApprovals`；
- 若用户正在看 A，可打开 approval center；
- 若用户正在看 B，不应弹出覆盖 B 的审批弹窗，但侧边栏/运行标记应显示 A 有待处理审批；
- 用户进入 A 后可在审批中心处理 A 的请求；
- approval response 必须使用 approval payload 所属 Session ID，不能默认 `$currentSession`。

### 9.6 Sidebar 与运行可见性

`Sidebar.svelte` 可直接使用 `/api/sessions` 中已有的 `running` 字段，增强展示：

- running Session 使用动画状态点；
- 显示总运行数；
- Session 标题下显示模型、当前工具或耗时摘要（可渐进加入）；
- 同一 `workDir` 内多个 running Session 时显示 warning badge；
- 选择 running Session 仅表示“查看”，不触发停止。

UI 文案必须避免暗示切换会暂停或中断任务。

## 10. 服务端配合改造

### 10.1 第一阶段原则

第一阶段不改写 Agent 主执行模型。`handleChatCompletions()` 仍然可使用：

```go
ctx, cancel := context.WithTimeout(r.Context(), timeout)
```

因为该设计确保浏览器对对应 completion 请求执行 Abort 时，会终止对应 Agent Run。前端多流保持即可解决切换误取消的问题。

### 10.2 事件身份字段补齐

需要检查并补齐面向 WebUI 的扩展事件，避免前端只能靠当前 Session 猜测归属。

建议：

1. `TranscriptStreamEvent` 始终有 `x_session_id`，并新增/统一 `run_id`；
2. `ToolStatusEvent` 增加 `SessionID`、`RunID`（或确保外层 event 带上）；
3. `SessionRunEventEntry` 必须带 `sessionId`、`runId`、`seq`；
4. `approval_request` / `approval_resolved` 带 `sessionId`、`runId`；
5. `done` data 至少带 `sessionId`，建议带 `runId` 与终态 status；
6. Session Stream replay 与即时 publish 的 JSON 字段保持一致。

如果 event 的 Session ID 与订阅 URL / completion request 绑定的 Session ID 不匹配，服务端应记录日志；前端也应防御性忽略。

### 10.3 运行状态一致性

当前 `APISession` 使用 `running bool` 与 `activeRunID`。需要确认以下约束通过测试：

- 设置 `running=true` 与写入 `started/running` event 在同一次 Run 初始化阶段完成；
- 所有结束路径（completed、failed、context canceled、异常 channel close）都会设置 `running=false` 并写入终态 run event；
- `publishSessionStreamDone()` 在最终持久化事件后调用；
- Session Stream 的 `isSessionRunActive()` 判断与 runtime snapshot 的 `activeRun` 一致；
- 同一 Session 上第二个请求不能覆盖仍在运行的 `activeRunID`。

### 10.4 同一 Session 的冲突响应

当前服务端会在 `sess.Lock()` 处串行等待。WebUI 若已按 `running` 禁用当前 Session 的发送，大部分情况不会触发第二请求；但 API 层仍应有明确行为。

建议在拿到 Session 后、进入耗时执行前增加显式检查：

```go
if sess.IsRunning() {
    writeError(w, http.StatusConflict,
        "session already has an active run", "session_run_active")
    return
}
```

实现时必须防止 check-then-act race：应在 Session 锁保护范围内检查、设置 `activeRunID` 和 `running=true`。

好处：

- 同一 Session 的第二个请求不会长时间挂在锁上；
- 前端、外部 API 调用方可得到可识别的 `409`；
- 多 Session 并行和单 Session 串行的边界更明确。

### 10.5 全局并发上限

`openaiapi.Config` 已有 `maxConcurrentRequests` 字段。实施本方案时应确认它真的限制 Agent Run 数，而非只作为无效配置。

建议使用 server 级 semaphore：

```go
runSlots chan struct{}
```

规则：

- 不同 Session 的运行可并发；
- 全部运行共享 `runSlots`；
- `maxConcurrentRequests <= 0` 时保持当前不限流行为，或在后续版本定义安全默认值；
- 达到上限返回明确的 `429` / `503`，第一阶段不引入服务端排队；
- 前端显示“达到并发上限，请稍后重试”，而不是将其误报成 Session 冲突。

### 10.6 同工作目录并发写入与现有文件锁

不同 Session 同时使用同一 `workDir` 是允许的。它们不应因为工作目录相同而被整体串行化；例如两个 Session 分别修改不同文件时，应仍可并行执行。

仓库已经存在可复用的同进程内存文件锁，不能把这里的问题仅描述为“提示风险”：

- 实现在 `internal/tools/file_lock.go` 的 `FileLockManager`；
- 默认实例 `DefaultFileLockManager()` 为进程级单例；
- 每个默认 `tools.Registry` 都会使用该实例，因此主 Agent、sub-agent，以及 serve 中不同 API Session 创建的独立 Registry，都共享同一把锁；
- `write` 与 `edit` 会先通过 `Registry.ResolvePath()` 得到规范化目标路径，再调用 `acquireFileLock(ctx, path, toolName)`；
- 对同一个已解析路径，后到达的 `write` / `edit` 会等待持锁操作完成；等待受 Agent request context 控制，用户 Stop/Abort 时会取消等待而不是永久阻塞；
- 锁在单次文件读取、计算和原子写入完成后立即释放，因此它保护的是“同一文件的写入临界区”，不是整个 Run，也不是整个 workDir。

因此，多 Session 并发的基线策略应为：

| 场景 | 行为 |
|------|------|
| 不同 Session 修改不同文件 | 并行执行 |
| 不同 Session 同时通过 `write` / `edit` 修改同一解析路径 | 使用现有进程级 `FileLockManager` 串行化写入 |
| 等待文件锁期间用户停止该 Session | 等待因 context canceled 退出，该 Session 按取消路径结束 |
| 不同 mothx 进程修改同一文件 | 当前内存锁无法覆盖；属于跨进程竞争 |
| 通过 `bash`、外部工具或未接入锁的工具修改文件 | 当前文件锁无法覆盖；属于旁路写入竞争 |

本提案的实施要求：

1. 不为 WebUI 多 Session 另造第二套 file lock，也不按 workDir 加一把大锁；
2. 验证 `buildSessionResources()` 创建的每个 Session Registry 使用默认的共享 `FileLockManager`，不得意外注入各自独立的 manager；
3. 为日志、tool event 或 run event 增加可观测性时，建议把文件锁等待/获取记录为状态信息，并将 owner 从仅工具名增强为可定位的 `sessionId/runId/agentId/toolName`；这不是正确性前提，但能排查等待原因；
4. WebUI 可在同 workDir 多 Run 时显示“并行修改”提示，但文案应准确：同一文件的 `write` / `edit` 已由同进程文件锁串行化，其他文件写入和未受管写入仍可能并行；
5. 后续如需覆盖多进程或 `bash` 写入，需要单独设计 OS 文件锁、工作区事务/调度或更严格 sandbox 策略，不能假设当前内存锁已覆盖这些路径。

测试补充：

- 两个不同 Session 的 Registry 同时对同一路径执行 `write` / `edit`：第二个操作必须等待第一个释放，最终文件内容符合串行顺序；
- 第一个 Session 持锁时取消第二个 Session：第二个操作应因 context canceled 退出，且锁状态不被污染；
- 两个 Session 修改不同路径：不得因文件锁相互阻塞；
- 确认服务端多 Session Registry 实例共享 `DefaultFileLockManager()`，而不是只覆盖 parent/sub-agent 场景。

## 11. 状态机

### 11.1 单 Session Run 状态

```text
idle
  └─ send → starting
               ├─ first stream event / started event → running
               ├─ AbortController.abort() → cancel_requested
               ├─ fetch / server error → failed
               └─ normal done → completed

cancel_requested
  ├─ EventError(context canceled) / terminal run_event → canceled
  └─ unexpected terminal success → completed (以服务端终态为准)
```

前端的 `AbortError` 只能作为本地即时反馈；最终 `completed/failed/canceled` 应优先服从服务端 run event，避免网络时序导致错误标记。

### 11.2 选中 Session 状态

```text
select A
  ├─ A 有本地 completion → 直接显示 A 的缓存，流继续
  ├─ A running 且无本地 completion → load snapshot + start observer(A)
  └─ A idle → load persisted history/runtime，不启 observer

select B
  ├─ 不动 A completion
  ├─ 可停止 A observer（如存在；observer 不控制 A 任务）
  └─ 按同样规则装载 B
```

### 11.3 页面刷新与恢复

在当前阶段，刷新会 abort 本标签发起的主 completion，服务端任务随 request context 取消。因此恢复重点是：

- 刷新前可能已正常完成但 UI 未处理完的 Session；
- 外部 API、其他浏览器标签页、channel 等来源启动的运行；
- Session Stream 临时断开后继续观察仍在运行的外部任务。

页面启动时：

1. 调用 `/api/sessions`；
2. 对 selected session 加载 messages/runtime/run events；
3. 对 `running=true` 的 selected session，若本地没有 completion，启动 observer；
4. 后续可选择对所有 running sessions 建立 observer，但第一阶段为控制连接数，仅观察 selected session 即可。

## 12. 实施步骤

### Phase 1：建立前端按 Session 的运行状态容器

文件：

- 新增 `ui/src/lib/session-runs.js`
- 修改 `ui/src/lib/stores.js`
- 修改 `ui/src/views/Chat.svelte`

工作：

1. 增加 `sessionRunStates` store 与 `ensure/update/remove` helper；
2. 将 `messages`、tool events、run events、capability events、cursor、runtime 的真相来源迁移为按 Session 数据；
3. 每个 Session 保存独立 completion controller；
4. 仍保留现有渲染组件，先通过 selected-state projection 兼容，减少一次性 UI 重写风险；
5. 为 store helper 添加纯 JavaScript 单元测试。

完成标准：运行 A 后切到 B，A 的流 controller 仍存在，A 的 event 仍写入 A 的 state。

### Phase 2：改造发送、停止和事件路由

文件：

- 修改 `ui/src/views/Chat.svelte`
- 必要时修改 `ui/src/lib/api.js`

工作：

1. 新会话首次发送前用 `crypto.randomUUID()` 创建 `x_session_id`；
2. 将 `sendPrompt()` 改为 `startCompletion(sessionID, request)`；
3. 将 `stop()` 改为 `abortCompletion($currentSession)`；
4. 删除基于 `$currentSession` 丢弃 completion stream event 的逻辑；
5. 以 `boundSessionID` 路由 transcript/tool/run/approval/done；
6. 当前 UI 的 `busy` 只从 selected session 的状态派生；
7. 用户从 A 切换到 B 后允许在 B 发送，A/B 可同时 running。

完成标准：A/B 同时运行，A 的文本或工具事件不会出现在 B，B 的事件不会出现在 A，Stop A 不影响 B。

### Phase 3：Session Stream 恢复与 observer 管理

文件：

- 修改 `ui/src/views/Chat.svelte`
- 新增或扩展 `ui/src/lib/session-runs.js`
- 视需要修改 `ui/src/lib/stores.js`

工作：

1. 将当前单例 `sessionStreamAbort/sessionStreamID/sessionStreamCursor` 迁移到 per-session observer/cursor；
2. 选择 running Session 且无本地 completion 时启动 observer；
3. observer 使用 `after_entry_seq/after_run_seq/after_capability_seq`；
4. observer 事件复用同一 `applyStreamEvent`，按实体 ID/seq 去重；
5. observer abort 只停止观察，不改变 Session 的 running 状态；
6. 主 completion 流结束后刷新 Session 信息与最终持久化历史。

完成标准：打开正在由外部来源执行的 Session，能看到持续输出；切换离开再回来能补齐遗漏；不会重复显示消息或工具事件。

### Phase 4：服务端事件与冲突处理补强

文件：

- `internal/serve/openaiapi/handler_chat.go`
- `internal/serve/openaiapi/session_stream.go`
- `internal/serve/openaiapi/events.go`
- `internal/serve/openaiapi/types.go`
- `internal/serve/openaiapi/session_mgr.go`
- `internal/serve/openaiapi/server_test.go`

工作：

1. 统一/补齐 Session ID、Run ID、终态 status；
2. 在同 Session active run 下返回明确 `409 session_run_active`；
3. 校验全部结束路径的 `running` 与 `activeRunID` 清理；
4. 使 `maxConcurrentRequests` 真正限制运行数量；
5. 增加多 Session 并行和流断连相关测试。

完成标准：事件身份一致；同 Session 不并发；不同 Session 可并发；服务端状态可由 WebUI 正确恢复。

### Phase 5：运行可见性与受控文件写入提示

文件：

- `ui/src/components/Sidebar.svelte`
- `ui/src/views/Chat.svelte`
- `ui/src/style.css`
- 相关 i18n 资源（如存在）

工作：

1. Session list 展示 running badge；
2. 展示当前并行运行数量；
3. 同 workDir 多 Run 时展示 non-blocking 提示，并明确同一文件的 `write` / `edit` 已由共享进程内文件锁串行化；
4. 对“本页面可 Abort”和“外部运行只可旁观”的状态给出明确提示；
5. 展示或记录文件锁等待状态（如服务端事件协议补齐该信息）。

## 13. 测试计划

### 13.1 服务端测试

在 `internal/serve/openaiapi/server_test.go` 或职责更清晰的新测试文件中覆盖：

1. **不同 Session 并行执行**
   - 创建可控、阻塞的 Agent A；
   - 启动 Session A；
   - 启动 Session B；
   - 验证 B 不因 A 的 Session lock 阻塞，二者均处于 running。

2. **同一 Session 拒绝第二 Run**
   - A 已经 running；
   - 第二请求使用相同 `x_session_id`；
   - 验证返回 `409` 与 `session_run_active`；
   - 验证原 Run 的 `activeRunID` 未被覆盖。

3. **Abort 终止对应请求任务**
   - 为 Session A 发起长执行请求；
   - 取消 request context；
   - 验证 A Agent 终止、run event 为 canceled、`running=false`；
   - 同时运行 B，验证 B 不受影响。

4. **Session Stream 多订阅者**
   - 对同一 Session 建立两个 `StreamSession` 订阅；
   - 发布 transcript/tool/run event；
   - 验证两个订阅者均收到事件且无死锁。

5. **断线 replay**
   - 记录多条消息、run event、capability event；
   - 用不同 cursor 建立 stream；
   - 验证只回放 cursor 之后的事件，顺序正确。

6. **事件身份完整性**
   - 检查 transcript、tool、approval、run、done 的 sessionId/runId；
   - 验证 observer 和 completion 流均能解析同一结构。

7. **并发上限**
   - `maxConcurrentRequests=1`；
   - 启动 A 后启动 B；
   - 验证 B 收到约定的限流错误；A 不受影响。

### 13.2 前端单元测试

新增纯逻辑测试，优先覆盖 `ui/src/lib/session-runs.js`：

1. `ensureSessionState(A/B)` 创建独立对象；
2. `abortCompletion(A)` 只调用 A 的 controller；
3. completion A 的 transcript 只进入 A messages；
4. A 与 B 的 tool event 使用各自 toolCallId 缓存；
5. 完成 A 不改变 B 的 running 状态；
6. cursor 按 Session 独立递增；
7. replay event 与即时 event 去重；
8. payload sessionId 与 bound sessionId 不一致时拒绝写入。

如果当前前端尚未配套测试框架，Phase 1 仍应至少将状态处理设计为不依赖 Svelte 组件的纯函数，方便后续接入 Vitest。

### 13.3 手工验收场景

1. 新建 A，执行一个会持续输出工具结果的任务。
2. A 运行中切到已有 B：确认 A 未停止。
3. 在 B 发起另一个任务：确认 A/B 同时显示 running。
4. 在 B 页面停留期间，A 持续完成工具和文本输出。
5. 切回 A：确认 A 的全部新输出都存在，且 B 内容没有混入。
6. 点击 A Stop：确认 A 被 canceled，B 仍继续。
7. 点击 B Stop：确认只 B 被 canceled。
8. 打开外部/API 启动的 running Session：确认通过 observer 持续显示其事件。
9. 切换多个运行 Session：确认无重复 transcript、无工具结果错配、无错误 banner 泄露到错误 Session。
10. 启动两个写同目录、同一目标文件的受管 `write` / `edit` 任务：确认后一个任务等待共享文件锁，前一个完成后按串行顺序继续；再验证修改不同文件的两个任务可并行。
11. 在等待文件锁的 Session 点击 Stop：确认仅该等待任务取消，持锁 Session 和其他 Session 不受影响。

## 14. 风险与缓解

| 风险 | 说明 | 缓解 |
|------|------|------|
| 内存增长 | 同时缓存多个 Session 的 transcript 可能增长 | 每 Session 限制未持久化 delta/cache 数量；完成后以持久化历史为准；提供 LRU 清理但不可清理 running Session |
| 重复事件 | completion 与 observer/replay 可能重叠 | per-session cursor + 实体 ID 去重；同一健康 completion 优先不并行 observer |
| 迟到事件 | 前一 Run 事件在下一 Run 开始后抵达 | event 必须携带 runId；状态更新校验 active runId |
| 非当前审批丢失 | 未选中 Session 收到 approval | 按 Session 保存 pending approvals，Sidebar 提示；回到该 Session 再处理 |
| 同目录文件竞争 | 多 Agent 可能竞争同一文件 | 复用现有进程级 `FileLockManager`，使同一解析路径的 `write` / `edit` 串行；UI 提示锁仅覆盖同进程受管写入，`bash` 和跨进程写入仍是旁路风险 |
| 浏览器刷新取消任务 | 当前 request context 与任务绑定 | 明确为 Phase 1 既有语义；后续单独做后台 Run 生命周期 |
| 前端改动范围大 | Chat 页面状态复杂 | 先引入 store 和 projection，逐步迁移，保留现有渲染结构 |

## 15. 后续演进：持久化后台 Run（不在本提案第一阶段）

如果产品以后要求“用户刷新、关闭浏览器或网络断开后任务仍继续执行”，需要另立提案，将执行与 completion HTTP request 解耦：

```text
POST /api/sessions/{id}/runs            创建后台 Run
GET  /api/sessions/{id}/runs/{runId}    查询 Run
POST /api/sessions/{id}/runs/{runId}/cancel  显式取消
GET  /api/sessions/{id}/stream          纯观察
```

那时 Abort 不再直接终止任务，而是前端收到 Stop 操作后调用 cancel endpoint。该模型与本提案不同，不能在当前修复中混用，否则会破坏“前端 Abort 仍应终止任务”的既定语义。

## 16. 决策摘要

本提案采用以下决策：

1. **前端 Abort 仍然终止任务。** `AbortController` 与发起该 completion 的 Session 强绑定。
2. **Session 切换不再 Abort 流。** 切换只改变 selected session 和渲染内容。
3. **允许多条 completion SSE 同时存在。** 每条流在创建时绑定唯一 sessionId，并持续消费直到结束或用户停止。
4. **所有事件按所属 Session 路由。** 非当前 Session 的事件必须保存到自己的状态，不能丢弃，也不能渲染到当前 Session。
5. **同 Session 单 Run、跨 Session 并行。** 服务端补充明确冲突响应和全局并发控制。
6. **Session Stream 用于恢复与旁观。** observer 流的停止只停止观察，不等价于停止任务。

最终用户体验应为：Session A 正在干活时，用户可以查看、操作或启动 Session B；A 继续执行并独立累积输出。用户回到 A 时看到完整进展；只有点击 A 的“停止”才会取消 A，且不会影响 B。
