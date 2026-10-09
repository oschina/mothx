# ACP 请求分派与取消语义方案（读循环 reactor + 请求级 context + 效果域车道）

> 状态：**第 1 步与第 2 步均已实施（2026-10-09）**。F2（prompt admission 阻塞读循环）与 F5（close 级联与子会话失序）均已修复；实现按 §6 的「效果域」原则落地（见 §4.1 实现注记）。
>
> 日期：2026-10-09
>
> 关联方案：[统一 Agent 核心与多入口 Runtime 方案](./agent-core-runtime-unification-proposal.md)、[跨进程 Session 执行归属、停止与恢复方案](./cross-process-session-execution-ownership-proposal.md)、[WebUI 多会话流式方案](./webui-multi-session-streaming-proposal.md)
>
> 关联代码：`internal/acp/acp.go`（`Run` 读循环、`dispatchRequest`、`handlePrompt`、`acquirePromptAdmission`、`handleCancel`/`handleCancelRequest`、`handleCloseSession`、`sessionCascadeIDs`、`closeSessionRuntime`/`shutdownSessionRuntime`、`s.pending`/`deliverResponse`）、`internal/acp/session_ops.go`（`sessionOpLanes`）、`internal/agentruntime/execution_admission.go`（`AcquireExecutionAdmission`、`AcquireSessionMutationGroup`）、`internal/agentruntime/execution_snapshot.go`（`localExecutionDraining`）
>
> 背景来源：`docs/notes/20261009.md` §12 的独立 review（F2、F5 两项低危观察）。

## 0. 决策记录

| # | 决策 | 结论 |
|---|---|---|
| D1 | 读循环的职责 | 读循环是**纯 demultiplexer（reactor）**：只做「信号的即时投递」与「命令的入 lane」，**永不执行会阻塞的命令**。 |
| D2 | 取消的载体 | 取消不再依赖「读循环里找 `rt.cancel`」。每个请求在**派发时**同步取得一个可取消的 `context.Context`；`session/cancel`/`$/cancel_request` 内联调用其 `CancelFunc`。 |
| D3 | 串行域的键 | lane 的键 = 命令的**效果范围**（被该命令改动的会话集合），不再是「请求里的单个 `sessionId`」。多会话命令按**确定排序**占用多键（沿用 `AcquireSessionMutationGroup` 的排序多锁纪律）。 |
| D4 | prompt 路径 | `session/prompt` **全程走 lane**（异步），取消句柄在派发前同步注册。 |
| D5 | 语义归属 | 本方案只改**适配器的派发与取消投影**。持久化、admission/recovery/terminalization、pending decision 的 owner 仍是 `ExecutionRuntime`/`DecisionService`；不新增 run 状态机、不新增所有权视图。 |
| D6 | 落地方式 | 分两步独立交付：第 1 步（消 F2）＝请求级 context + prompt 全程异步；第 2 步（消 F5）＝效果域串行（实现为「级联目标各在自身 lane 上关闭」，见 §6）。两步均已实施。 |

## 1. 背景与现状

ACP 以 NDJSON/JSON-RPC over stdio 通信。`Run` 的读循环串行读取请求并调用 `dispatchRequest`（`internal/acp/acp.go`）。读循环承担两类互相冲突的职责：

1. **分派「可阻塞的命令」**：`session/load|new|fork|resume` 会装配 Runtime 资源、连接 MCP；`session/prompt` 会做 admission、durable begin、`BuildAgent`。
2. **即时接收「必须立刻响应的信号」**：`session/cancel`、`$/cancel_request`、以及客户端对反向 `session/request_permission` / question 的响应——这些都必须被读循环读到才生效（「cancel/decision 可达性」）。

commit `4860f0b2` 引入 `internal/acp/session_ops.go` 的 `sessionOpLanes`：把 session 相关命令按 session 串行到异步车道，使 load/fork 不再阻塞读循环，是正确方向。`495bbc82` 让同进程后继 run 在 draining 时排队而非误报 busy。

## 2. 问题（本方案要消除的两个缺口）

### 2.1 F2：prompt 的 admission 阻塞仍发生在读循环

`runSessionOp` 对 `session/prompt`（及 `set_config_option`/`set_mode`/`set_*`/`history`）采用「lane 不忙则内联同步执行」的混合策略。原因是 `handlePrompt` 必须在返回前**同步**完成「admission + durable begin + 注册 `rt.cancel`/`rt.promptID`」，紧随其后的 `session/cancel` 才能读到 `rt.cancel` 真正取消本轮。

但 `handlePrompt` → `acquirePromptAdmission` → `agentruntime.AcquireExecutionAdmission(ctx=context.Background(), Wait=false)` 存在一个会**轮询等待**的分支（`execution_admission.go`）：当占用的租约由本进程持有、且该 execution 已选终态但终态持久化未完成（draining）时，`localExecutionDraining` 为真，于是以 50ms 轮询等待租约释放（`ctx` 为 Background，无超时）。此时 lane 不忙 → 内联执行 → **这段轮询发生在读循环 goroutine 内**，窗口内对**所有 session** 而言 cancel/`$/cancel_request`/反向 decision 响应都暂时不可达。

### 2.2 F5：lane 的键 ≠ close/delete 的效果范围

`session/close`/`session/delete` 本轮改为「始终异步到 lane」，但 lane 键 = 请求里的单个 `sessionId`。而 `handleCloseSession` 经 `sessionCascadeIDs` 会把**父 + 全部派生子会话**一起关闭。于是「关闭父会话 → 紧接着对某个子会话 prompt」时：close 在父的 lane 异步跑，子 prompt 查子自己的 lane（不忙）→ 内联执行，两者**并发**（改动前同步读循环天然保序）。

后果面：子 prompt 经 `sessionForPrompt` 拿到 `rt`（close 尚未将其从 `s.sessions` 删除；且 `handlePrompt` 只检查 `rt.runtime == nil`，**不检查 `rt.closed`**；`shutdownSessionRuntime` 会 `rt.closed = true` 并调 `rt.runtime.Shutdown()`），可能在**正在 Shutdown 的 runtime** 上继续 `BuildAgent`/`BeginIntentDurable`，与 shutdown 竞争。

### 2.3 共同根因

**「命令的串行域」与「命令的效果范围」不一致，且「阻塞的等待」没有被纳入可取消的信号域。**

## 3. 目标与非目标

### 3.1 目标

1. 读循环永不因命令而阻塞（消 F2 的读循环阻塞）。
2. cancel/`$/cancel_request` 对**任何阶段**的 prompt 都可达且有效——包括「已排队、尚未 admit」的 prompt（比现状更强）。
3. 多会话命令（close/delete 级联）与「其效果范围内的会话操作」严格串行（消 F5）。
4. 保住既有不变量：`rt.cancel`/`promptID` 注册的 prompt→cancel 顺序、durable run 生命周期、pending decision 的 owner、`RuntimeOwnsTurnEnd` 语义。

### 3.2 非目标

1. 不改 durable run 的生命周期、admission/recovery/terminalization 语义（仍归 `ExecutionRuntime`）。
2. 不改 pending approval/question 的持久化与 replay（仍归 `DecisionService`）。
3. 不引入第二套 run 状态机、所有权视图或事件流。
4. 不改 ACP v1 线协议；仅内部派发/取消机制。
5. 不给任何命令加「任意墙钟超时」来决定所有权（与长任务连续性口径一致）。

## 4. 设计总览

把读循环当作 **reactor**（类比 `net/http.Server`：accept 循环分派 handler，handler 各持自己的 `context.Context`，另有控制面取消它）：

- **信号（inline）**：`session/cancel`、`$/cancel_request`、反向请求的响应。只做「查句柄 → 触发」，不排队、不执行命令。
- **命令（lane）**：其余全部 session 作用域方法。进入「效果域车道」，按效果范围串行。
- **请求级 context**：每个请求在派发时同步创建 `ctx` 与 `CancelFunc`，登记到请求表中；命令的可阻塞阶段全部在该 `ctx` 下执行。

由此：

- prompt 也走 lane（不再有 `runSessionOp` 的混合分支）；取消句柄在**派发前**同步注册（`server.inflight`），故 prompt→cancel 顺序仍有保证，且「尚未 admit」的 prompt 也可取消。
- close 的级联把每个派生子的关闭派发到**子会话自身的 lane** 并等待（`runSessionLaneJob`），子 prompt 与子 close 同 lane，天然保序。

```
读循环 ──┬─ 信号? ──► inflight[key] 查 CancelFunc ──► cancel()   （inline，永不阻塞）
         └─ 命令? ──► lane[key]（同 session 串行，跨 session 并发）
                          │
                          └─ job(ctx) 在 ctx 下执行 admission/assembly/begin
```

### 4.1 实现注记（2026-10-09）

- 请求级 context 存在 `server.inflight`（`map[requestKey]*promptInflight`，含 `sessionID` 与 `CancelFunc`）。prompt 派发前同步登记；lane 任务结束时 `cancel()` 并清除。`handleCancel`（按 sessionId）与 `handleCancelRequest`（按 requestId） 除既有的 `rt.cancel`/`rt.execution` 路径外，还触发 inflight 取消。
- `handlePrompt` 拆为 `handlePrompt`（Background，供测试/嵌入式直调）与 `handlePromptContext(ctx, req)`（读循环用）；`acquirePromptAdmission(ctx, rt)` 使用该 ctx，并在 admission 后、BeginIntentDurable 后、BeginArtifactCollection 后增加 ctx 检查，保证 admission 后到 Run 注册之间的窄窗口也能取消。
- F5 采用「逐目标 lane 执行」（§6），未采用多键 barrier。delete 无需改动（§6.4）。

## 5. 原则一：请求级可取消 context（消 F2）

### 5.1 机制

1. 读循环在 `dispatchRequest` 前/内，为每个带 id 的请求创建 `ctx, cancel := context.WithCancel(context.Background())`，存入 `map[string]cancelEntry`（键 = `mcp.RawIDKey(req.ID)`）；请求响应写出即 `cancel()` 并从表删除。
2. `session/cancel` / `$/cancel_request` 内联处理：定位对应请求（`$/cancel_request` 用 `requestId`；`session/cancel` 用 `sessionId` 找到该会话的当前请求句柄）→ 调其 `cancel()`；并保留既有 `rt.execution.Cancel()` 语义。
3. 命令在 lane 上以 `job(ctx)` 运行；`acquirePromptAdmission` 改为把该 `ctx` 传给 `AcquireExecutionAdmission`。

**关键 enabler**：`AcquireExecutionAdmission` 已 `select { case <-ctx.Done(): return nil, ctx.Err() }`（`execution_admission.go`）。因此把 Background 换成可取消 `ctx` 后，draining 的 50ms 轮询会被 cancel **立刻打断**，无需触碰 Runtime。

### 5.2 效果

- 读循环不再阻塞（F2 消除）。
- cancel 可作用于「尚未 admit」的 prompt：`AcquireExecutionAdmission` 返回 `ctx.Err()` → prompt 以 cancelled 终止，且不留下 durable run（此时尚未 begin）。这是**比现状更强**的性质。
- prompt→cancel 顺序保住：句柄在派发前同步注册，cancel 一定找得到。

### 5.3 与 `rt.cancel` 的关系

保留两套句柄、职责分开：

- **请求级 `ctx`**：取消「命令的 admission/装配阶段」（尚未有 run）。
- **`rt.cancel`/`rt.promptID`**：取消「已 admit 的 run」（现状语义）。

`acquirePromptAdmission` 中判定「本地已有活跃 run」的 `localActive := rt.cancel != nil` 检查保持不变；不得把请求级 cancel 写进 `rt.cancel`，否则会误判。

## 6. 原则二：效果域串行（消 F5）

### 6.1 机制（已实施）

一条命令的**效果范围**（被它改动的会话集合）决定它与谁必须串行：

- 单会话命令（prompt/load/set_*）：效果范围 = 该会话，按会话 lane 串行（已有）。
- 多会话命令（`session/close` 级联）：效果范围 = 父 + 全部派生子会话。实现上，**每个目标会话的关闭都运行在它自己的 lane 上**：
  - 被请求的根会话已在自己的 lane 上（close 就是在这条 lane 上执行的），直接内联关闭；
  - 每个派生子会话通过 `runSessionLaneJob(child, closeChild)` 派发到**子会话自己的 lane** 并等待完成（`s.runSessionLaneJob`）。

由于子 prompt 与「子 close」共享子会话 lane，二者严格 FIFO，不会再出现「在正在 Shutdown 的 runtime 上 BuildAgent」的竞态。级联是树形，等待关系无环（父→子→孙，不会互为等待），因此无死锁。

### 6.2 效果

- 子 close 与子 prompt 落在同一条（子的）lane 上 → 严格 FIFO（F5 消除）。
- 不牺牲跨会话并发：不同会话仍在各自 lane 上并发。
- 读循环保持纯净：级联集合仍在 lane 任务内计算（`sessionCascadeIDs`），不在读循环上做 DB 读。

### 6.3 备选（未采用）

- **多键 barrier**：预先算出级联集合，用一个 `dispatchSessionOps(keys, job)` 同时在多条 lane 上占位（排序避免死锁）。它更通用，但需要**在读循环上先算出级联集合**（一次 `ListAllDetailed` DB 读），且要新增多队头调度的复杂度。已实施的「逐目标 lane 执行」在行为上等价且不污染读循环。
- **`rt.closed` 拒绝**：让 `handlePrompt` 对 `rt.closed` 直接拒绝。仅能把竞态变为确定性拒绝，不闭合窗口，作为过渡备选。

### 6.4 说明：delete 无需此改动

`session/delete` 已经在删除期间用 `agentruntime.AcquireSessionMutationGroup`（排序多租约）锁住所有级联目标，与 prompt 的 execution admission 互斥，并对已加载会话直接拒绝；因此其效果域串行已由 Runtime 租约保证，无需 lane 层改动。

## 7. 语义与不变式

1. **单一仲裁点**：读循环 = reactor + demux；cancel/decision 仍只经 `ctx`/`DecisionService`，不新增 run 状态机。
2. **Policy 而非 fork**：lane 键由效果范围推导，规则一处定义；不新增并行 runtime。
3. **可达性方向**：所有变更只会让 cancel 更可达、更早生效，绝不把「可达」变「不可达」。
4. **顺序**：同效果域内 FIFO；prompt→cancel 顺序由「句柄同步注册」保证。
5. **无墙钟裁决**：请求级 `ctx` 只由显式 cancel/进程 shutdown 触发；不引入超时决定所有权（与 AGENTS.md 长任务连续性一致）。
6. **shutdown**：`shutdownAllSessionRuntimes` 先 `ops.shutdown()`（拒新任务 + 等待在途）再拆 runtime 的现行顺序保留；请求级 ctx 在 shutdown 时应被 cancel。

## 8. 改造点（文件级，已实施）

- `internal/acp/session_ops.go`：
  - 新增 `promptInflight` 与 `trackPromptInflight`/`clearPromptInflight`/`cancelPromptInflight`/`cancelPromptInflightForSession`；新增 `runSessionLaneJob`（在目标 lane 上运行并等待）。
  - `dispatch`/`busy`/`shutdown` 语义保留；`runSessionOp` 仍供 set_* 等方法使用（prompt 不再走它）。
- `internal/acp/acp.go`：
  - `server` 增加 `inflightMu`/`inflight` 并在 `Run` 初始化。
  - `dispatchRequest`：`session/prompt` 改为「始终异步」，并在派发前创建/登记请求级 `ctx`；信号方法（cancel、decision 响应）继续内联。
  - `session/prompt` 的处理拆为 `handlePrompt`（Background）与 `handlePromptContext(ctx, req)`；`acquirePromptAdmission(ctx, rt)` 增加 ctx 参数，并在 admission 后 / `BeginIntentDurable` 后 / `BeginArtifactCollection` 后增加取消检查。
  - `handleCancel`/`handleCancelRequest`：保留 `rt.execution.Cancel()`/`rt.cancel`，并新增请求级 inflight 取消。
  - `handleCloseSession`：级联循环中，根会话内联关闭，派生会话经 `runSessionLaneJob` 在其自身 lane 上关闭。`handleDeleteSession` 不变（§6.4）。
- `internal/agentruntime/execution_admission.go`：无改动（`ctx` 语义已具备）。

## 9. 落地与回滚（已实施）

| 阶段 | 内容 | 消除 | 状态 | 回滚 |
|---|---|---|---|---|
| 第 1 步 | 请求级可取消 `ctx` + `session/prompt` 全程异步 + 取消经请求级 ctx 触发 | F2 | 已实施 | 还原 `dispatchRequest` 路由、`handlePromptContext`、`promptInflight` 辅助与 `acquirePromptAdmission` 的 ctx 参数 |
| 第 2 步 | `session/close` 把每个派生子的关闭派发到子会话自身 lane 并等待（`runSessionLaneJob`） | F5 | 已实施 | 还原 `handleCloseSession` 的级联循环为直接 `closeSessionRuntime` |

两步独立；均已通过 `go test -race ./internal/acp/` 与 `go test ./internal/architecture/`。

## 10. 测试（已实施）

- 单元（`internal/acp/prompt_cancel_test.go`）：`TestCancelRequestAbortsPromptAdmissionContext`、`TestHandleCancelRequestCancelsInflightPrompt`、`TestHandleCancelAbortsPromptAdmissionForSession`、`TestCancelPromptInflightForSessionScopesBySession`、`TestDispatchRequestPromptRunsOffReadLoop`。
- 级联（`internal/acp/close_cascade_test.go`）：`TestRunSessionLaneJobSerializesOnTargetLane`（目标 lane 串行）、`TestRunSessionLaneJobRunsInlineWithoutLanes`。
- 既有回归：`TestDispatchRequestKeepsCancelReachableDuringSlowLoad`（load 占 lane 时 cancel 仍可达）继续通过；`TestACPStdioProcessRunStatusProjectionAndEvents` 改为在列会话前等 `run_status` begin 事件（§11.5）。
- 命令：`go test -race -count=1 ./internal/acp/`、`go test -count=1 ./internal/architecture/` 通过。
- 仍有待补（未来，需真实 draining 夹具）：直接断言 `acquirePromptAdmission` 在本地 draining 时被 cancel 立刻打断，而非轮询到租约释放。

## 11. 风险与开放问题

1. 请求表与 ctx 的**生命周期/清理**（响应写出、错误早退、shutdown 各路径），避免泄漏或提前 cancel 误伤。
2. 多键 barrier 的实现细节（饥饿、公平、与 `busy()` 的交互）；先只用于 close/delete 可降低面。
3. prompt 全程异步后，`rt.cancel`/`rt.promptID`/reverse-request 的注册时点需重新核对，确保与 decision replay 不冲突。
4. `session/cancel` 以 `sessionId` 定位句柄时，若同会话同时存在多个请求，需明确「取消哪一个」（建议：取消该会话当前的命令/lane 头任务，语义与现状一致）。
5. Desktop/ACP 客户端可观察行为：错误码/时序可能更早返回 cancelled；需确认客户端可接受（属增强而非破坏）。**已观察到的一处语义变化**：`session/prompt` 改为异步后，「prompt 被接受 → 紧接着的下一个请求（含全局的 `session/list`）一定能看到该 Run」不再成立；依赖运行时状态应通过 `run_status` 事件或重试列表。已同步调整 `TestACPStdioProcessRunStatusProjectionAndEvents`：先等 `run_status` begin 事件再列会话。

## 12. 与 AGENTS.md 对齐

- **一个 Agent Core / 一个 Runtime / 薄适配器**：本方案只改 ACP 的**派发与取消投影**；命令执行仍走 `SessionRuntime`/`ExecutionRuntime`。
- **Policy 而非 fork**：lane 键 = 效果范围，是派发策略的一处声明；不新增并行运行器。
- **单一决策/事件模型**：cancel/decision 仍经 `ctx`/`DecisionService`，不新增状态机或事件流。
- **长任务连续性**：不引入墙钟超时裁决；请求级 `ctx` 仅由显式 cancel/shutdown 触发。
- **无新 DB/SQL 边界**：不触碰 `internal/db`/`internal/dao`。
