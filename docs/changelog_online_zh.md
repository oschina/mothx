# 更新日志（当前版本）

本文件仅记录**当前版本**的变更。所有版本的完整历史见 [docs/zh/changelog.md](zh/changelog.md)。

## v1.3.108

### 💥 行为变更

- **主循环的迭代预算初始上限提高到 400 轮，续期后最高 600 轮。** Agent 主循环的默认初始迭代上限从 200 提高到 400，续期天花板为 600，因此在持续有进展的长任务会在预算压力提示出现前多跑一倍轮数。`extend_budget` 续期策略形状不变（最多 2 次，每次 +50% 软上限，最小间隔为软上限的 1/10，墙钟上限 16 小时）；不调用续期的 run 就在 400 轮停止。子代理、Expert 成员与渠道 run 各自保留原有上限（渠道 `maxTurns`、子代理 `max_iterations`），显式传入软上限的调用方仍使用惯例的 2 倍硬上限。

### ✨ 新功能

- **TUI 与 CLI 现在可以为已配置的外部 ACP worker 激活 Router 模式。** `/router` 打开 ACP worker 面板（列出/新增/编辑/删除/激活 profile）；`/router <profile>` 与 `mothx --router --acp-agent <profile>` 直接激活，三者都走同一条 Runtime 激活路径：profile 必须存在于 `acpAgents`、已启用且使用非交互权限策略；会话必须空闲且来源未被强制为 yolo；Router binding 先于 mode 落盘，因此激活失败会留在非 Router 状态。面板只通过唯一的 `config.SaveACPAgentProfiles` 校验与写入入口持久化，因此相对路径 `command[0]`、重复 ID、写角色缺少 `writeRoots` 都会在写入前被拒绝。激活只负责指定 worker，非交互审批策略由 Runtime 按 profile 记录，并拒绝不一致的显式值。手工编辑或旧版本遗留的 `defaultMode: router` 会在加载与解析时被归一化为只读的 `plan` 模式，而不是让每个新会话的 Agent 构建失败；该回退绝不会放大为完全访问。 没有 binding 时，通用模式切换不能选择 `router`：`router` 已从 Tab 模式循环与 `defaultMode` 循环中移除，且 Agent 构建在无可用 binding 时对 Router 失败关闭，因此持久化默认值或过期的适配器取值都不会造出「委派必然失败」的 Router 工具面。TUI 的所有模式写入（`/mode`、Tab 循环、`/systeminit`、修改 `defaultMode` 设置）现在都经过共享 Runtime，适配器不再只改本地状态、导致重开时回放旧模式。会话在其他适配器重新打开、或无模式参数恢复时，会回放持久化模式（包括 Router）。直接编辑 `settings.json` 依然受支持且同样需要谨慎；外部委派已通过 Router 可用。

- **Router 模式可以把任务委派给已配置的外部 ACP 编码 Agent。** Router 绑定的 `delegate_subagent` 通过共享 Runtime 启动一个阻塞式外部 worker：Agent Core 只拿到事件/输入代理，profile 策略、有界任务契约、唯一终态事件与进程 containment 都由 Runtime 负责。Router lead 会看到带 `agent` 身份的文本、思考、工具、计划与用量投影；阻塞返回值附带标记为 `worker-reported` 的有界 `worker` 元数据，完整远端 transcript 仍作为子级投影。远端 `session/request_permission` 会写入共享决策账本，并按 profile 的 `auto_allow_once`/`auto_reject` 立即作答，只选择对方实际提供的一次性选项。取消会发送 `session/cancel`、把所有待处理请求回答为 cancelled，并回收整个进程树（Unix 进程组、Windows Job Object）。外部子级传输不依赖持久化、provider 或适配器；`mothx doctor` 现在可在不启动 worker 的情况下校验 profile 形状与可执行文件可用性。 ACP profile 的 `maxConcurrentChildren` 现在真正被执行：Runtime 的 worker 许可每个会话只放行一个存活的外部 worker，因为两个 worker 会同时写入同一个会话工作目录，而只读的 Router lead 没有本地写入工具来化解该冲突。配置值大于 1 仍可正常加载（既有的 `settings.json` 必须保持有效），并由 `mothx doctor` 与 `/router` 面板报告为「尚未生效」，而不是被静默当作已生效；并行能力本身仍取决于 Runtime 拥有的「每 worker 独立 worktree」。


- **Serve Web UI 新增动态多会话工作台。** `/chat` 现支持会话标签、最多 4 个窗格分屏（菜单/拖拽）、每个 pane 独立的 composer 与模型/草稿状态、布局持久化和移动端单窗格投影。应用级唯一的 `session-stream` 调度器是 `/ws/runs` 的唯一消费者并接管 SSE 回退，因此关闭一个 pane 不会中断其他后台会话的流。各 pane 直接渲染 canonical `sessionRunStates`，溢出的标签可通过“全部标签”菜单访问；审批与提问在所属 pane 的浮层中完成，后台待处理只显示标签与全局 attention 徽标，不会抢占焦点；超过 4 个窗格或标签上限的布局操作会被拒绝并提示原因。旧的单会话回退及其 `?workbench=0`/设置开关已移除，`/chat` 始终是工作台；只读 Plan dock 从焦点会话的最新 plan 消息派生，不新增存储、工具或后端端点。composer 文本在切换标签时保留；带 providerRef 的附件可跨刷新恢复，仅带 dataUrl 的附件在刷新后会提示重新选择。标签条内拖拽现在始终按同窗格重排处理，不再被误判为边缘分屏；第二个浏览器标签页会恢复共享的最近布局，并按 last-write-wins 保持可恢复。每个会话的滚动位置在切换标签后保留；关闭仍在运行的会话标签时会给出一次后台继续提示，而不是静默丢弃。 工作台标签条与 icon-only 工具条已收紧并适配明暗主题；composer 对齐 Desktop：折叠的模型+模式入口可展开 plan/agent/yolo/os 模式、Work/Code 显示模式与 provider/model/主角团选择，会话目录移到聊天头部。

- **Serve 新增带认证的交互式终端。** `features.terminal` 通过 `/api/terminals` 与 `/ws/terminals/` 暴露由 `internal/terminal`（Unix 使用 creack/pty，Windows 使用 ConPTY）驱动的 xterm.js PTY 面板。终端绑定焦点会话的 cwd，继承 serve 进程环境，支持 resize/replay/takeover，并按“回环或已认证监听”策略授权；它不创建 Run、不写 transcript、不占 Runtime lease，也不在 sandbox 内运行。Linux 上 shell 还带有 `Pdeathsig`，不会在 serve 退出后残留；Windows 则提供仅在该平台运行的 ConPTY start/input/resize/exit 往返测试作为实机门禁。 终端配置（含显式 `enabled: false`、shell、资源上限与 allowedOrigins）在 `serve.json` 保存/重载后完整保留。状态栏会显示服务端输出反压，不再把它误判为断线；serve 只记录终端创建/退出/终止的生命周期元数据，不记录输入或输出。

- **TUI 状态栏新增实时生成速度。** Agent 流式输出期间，底部状态栏会在耗时旁显示估算的 `tok/s` 速度；该值基于最近一个短窗口内的流式输出增量计算，在没有新 Token 到达时（例如执行工具或等待审批期间）自动隐藏。加载提示行同时显示本次运行的估算生成 Token 总数。

- **TUI 新增就地 MCP 管理面板，远端 MCP 遵循当前 Streamable HTTP 版本。** `/mcpmgr [project|global]` 在当前作用域的 `mcp.json` 上打开面板，可逐字段新增、编辑、删除、启用或关闭服务器（先选传输类型，再填写名称、端点或命令、参数、环境变量、请求头与启用状态），取代旧的 `/init_mcp` 与 `/mcps`；变更仅修改配置，执行 `/reload` 后生效。HTTP MCP 客户端现在会在 `initialize` 阶段协商协议版本，并在 `MCP-Protocol-Version` 中发送该版本，声明 `Accept: application/json, text/event-stream`，拒绝既非 JSON 也非 `text/event-stream` 的响应，且不再跟随已配置端点的重定向，避免凭据与请求体泄漏到其他来源。`tools/call` 结果会保留 `structuredContent` 以及标准 `resource`/`resource_link` 块并转为模型可读文本，而不再丢弃。

### 🐛 问题修复

- **ESM 目标编辑不会被并发协调器写入覆盖，孤立 Run 也不再阻塞控制。** ESM 目标的所有读改写路径现在都在 `BEGIN IMMEDIATE` 事务内执行，worker 进度写入不会再把它在用户编辑提交前读到的旧 objective 写回。pause/resume/clear 同时允许一个没有有效 lease、等待恢复的 canonical Run，因此中断的本地准入不会让目标永久不可控制。终端控制还会把不支持平台报告为 `501 terminal_unsupported` 而不是 `terminal_disabled`，Unix PTY 的 build tag 也与受支持平台集合对齐，使 `aix` 等目标编译到显式的 `ErrUnsupported` 实现。

- **交互终端生命周期无数据竞争，会跟随焦点，并持续显示安全边界。** PTY 读取/等待循环现在在创建时直接接收句柄，结束路径在锁内清空终端句柄，既消除了数据竞争，也消除了短命 shell 在启动期间退出时可能触发的空指针窗口；列出/附加终端时通过加锁的 `Size()` 读取尺寸，而不是访问未同步字段。切换工作台焦点会话时，现在会释放旧会话的 shell 并为新 cwd 创建终端，不再留下绑定到上一个会话的旧终端；终端 id 失效（如 serve 重启）后会停止重试，由“重新连接”按钮创建新 shell。终端状态栏现在持续标明 shell 不受沙箱保护、继承 serve 进程用户与环境变量，并展示 `/api/status` 上报的占位认证 token 警告；浏览器冒烟测试对提示与焦点重绑定均有断言。关闭活动 shell 时现在会在进程退出后立即返回，仅在宽限期后升级为 SIGKILL，不再每次都等待 2 秒；浏览器 cookie 会话不再收到长期 terminal token；被拒绝的配置更新在回滚前不会先应用终端限制或杀掉 shell；Windows ConPTY 包装现在保证句柄只关闭一次，并把 shell 加入 kill-on-close Job Object，终端关闭后遗留的后台子进程不会继续运行。

- **交互终端的恢复、焦点与安全提示已补齐。** 终端 dock 会始终提示它与可能正在运行的 Agent 共享焦点会话 cwd，并在 canonical execution 快照为本地或另一进程执行时升级警告。页面刷新会重新 attach 该标签页记录的 detached shell，而不是泄漏旧终端再新建一个；shell 已退出或终端 id 失效后，“重新连接”会创建新终端。xterm 样式已加载，`@xterm/xterm`/`@xterm/addon-fit` 改为精确 pin；PTY read loop 在发布 exit 帧前等待 `cmd.Wait`，非零退出码不再被误报为 0。工作台新增 1000 步 property test，并据此修复 Draft trajectory 模式持久化、删除 group/应用预设后的焦点修复、分隔权重精确夹紧、分页会话标签对账、命令建议重复 DOM id 与移动端右键菜单目标。

- **在 `/chat` 之外创建的新任务不会再因工作台重新挂载而丢失。** 工作台 hydration 改为应用启动时的一次性步骤，不再在懒加载的 Chat 视图每次挂载时重新读取 `localStorage`。此前从 Sidebar/Sessions 新建 Draft 后，若 300ms 持久化防抖尚未触发就切回 `/chat`，旧布局会覆盖内存中的新任务；现在应用启动后内存状态即权威，新开的页面仍会继承共享的最近布局。浏览器与单测均覆盖重新挂载和延迟 hydration 路径。

- **会话内容写入收敛为单一裁决，split 存储可无损修复。** 消息、工具结果、模式/思考层级/标题变更以及对话 turn 边界现在都以持久化的 `sessions.content_store` 为准，不再信任当前 handle 的内存状态；在别的进程迁移之前打开的进程会采纳已提交的事件日志，而不是继续写入已被回收的 SQLite transcript，也不会再误报 `session was modified by another process`。打开会话现在是只读的：不再回收 legacy payload 行、不翻转存储、不重置日志；legacy 重复行只能由持有内容租约的围栏回收删除，且在翻转后的冷静期之后进行，并在 DELETE 内重新校验覆盖关系。旧构建在翻转后追加的行会被识别为 split 存储并完整保留。`mothx doctor` 会报告 split 会话，并单独报告「事件日志侧 turn 边界找不到对应 marker」的会话（内容在事件日志接管之前就已丢失：这些 turn 仍可读取但无法 fork，doctor 会如实报告而不是显示一切正常）。`mothx doctor --repair-session-content [all|SESSION_ID]` 会增量采纳未覆盖内容、翻译 turn 边界并幂等回收已覆盖的重复行，`--json` 输出逐会话结果。新增架构守卫会拒绝第二处 content store 路由、会写入存储的 open 路径，以及任何按内存模式选择写入分支的代码。**升级时请统一所有 MothX 二进制，并在修复前退出并重启每一个正在运行的进程（TUI、serve、Desktop 捆绑的 `mothx acp`）。** 新构建无法阻止混合构建窗口，但不会再丢内容：它会同时保留两份存储，直到修复入口采纳差异。
