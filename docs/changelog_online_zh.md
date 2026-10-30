# 更新日志（当前版本）

本文件仅记录**当前版本**的变更。所有版本的完整历史见 [docs/zh/changelog.md](zh/changelog.md)。

## v1.3.108

### ✨ 新功能

- **Serve Web UI 新增动态多会话工作台。** `/chat` 现支持会话标签、最多 4 个窗格分屏（菜单/拖拽）、每个 pane 独立的 composer 与模型/草稿状态、布局持久化和移动端单窗格投影。应用级唯一的 `session-stream` 调度器是 `/ws/runs` 的唯一消费者并接管 SSE 回退，因此关闭一个 pane 不会中断其他后台会话的流。各 pane 直接渲染 canonical `sessionRunStates`，溢出的标签可通过“全部标签”菜单访问；审批与提问在所属 pane 的浮层中完成，后台待处理只显示标签与全局 attention 徽标，不会抢占焦点；超过 4 个窗格或标签上限的布局操作会被拒绝并提示原因。旧的单会话回退及其 `?workbench=0`/设置开关已移除，`/chat` 始终是工作台；只读 Plan dock 从焦点会话的最新 plan 消息派生，不新增存储、工具或后端端点。composer 文本在切换标签时保留；带 providerRef 的附件可跨刷新恢复，仅带 dataUrl 的附件在刷新后会提示重新选择。标签条内拖拽现在始终按同窗格重排处理，不再被误判为边缘分屏；第二个浏览器标签页会恢复共享的最近布局，并按 last-write-wins 保持可恢复。每个会话的滚动位置在切换标签后保留；关闭仍在运行的会话标签时会给出一次后台继续提示，而不是静默丢弃。 工作台标签条与 icon-only 工具条已收紧并适配明暗主题；composer 对齐 Desktop：折叠的模型+模式入口可展开 plan/agent/yolo/os 模式、Work/Code 显示模式与 provider/model/主角团选择，会话目录移到聊天头部。

- **Serve 新增带认证的交互式终端。** `features.terminal` 通过 `/api/terminals` 与 `/ws/terminals/` 暴露由 `internal/terminal`（Unix 使用 creack/pty，Windows 使用 ConPTY）驱动的 xterm.js PTY 面板。终端绑定焦点会话的 cwd，继承 serve 进程环境，支持 resize/replay/takeover，并按“回环或已认证监听”策略授权；它不创建 Run、不写 transcript、不占 Runtime lease，也不在 sandbox 内运行。Linux 上 shell 还带有 `Pdeathsig`，不会在 serve 退出后残留；Windows 则提供仅在该平台运行的 ConPTY start/input/resize/exit 往返测试作为实机门禁。 终端配置（含显式 `enabled: false`、shell、资源上限与 allowedOrigins）在 `serve.json` 保存/重载后完整保留。状态栏会显示服务端输出反压，不再把它误判为断线；serve 只记录终端创建/退出/终止的生命周期元数据，不记录输入或输出。

### 🐛 问题修复

- **ESM 目标编辑不会被并发协调器写入覆盖，孤立 Run 也不再阻塞控制。** ESM 目标的所有读改写路径现在都在 `BEGIN IMMEDIATE` 事务内执行，worker 进度写入不会再把它在用户编辑提交前读到的旧 objective 写回。pause/resume/clear 同时允许一个没有有效 lease、等待恢复的 canonical Run，因此中断的本地准入不会让目标永久不可控制。终端控制还会把不支持平台报告为 `501 terminal_unsupported` 而不是 `terminal_disabled`，Unix PTY 的 build tag 也与受支持平台集合对齐，使 `aix` 等目标编译到显式的 `ErrUnsupported` 实现。

- **交互终端生命周期无数据竞争，会跟随焦点，并持续显示安全边界。** PTY 读取/等待循环现在在创建时直接接收句柄，结束路径在锁内清空终端句柄，既消除了数据竞争，也消除了短命 shell 在启动期间退出时可能触发的空指针窗口；列出/附加终端时通过加锁的 `Size()` 读取尺寸，而不是访问未同步字段。切换工作台焦点会话时，现在会释放旧会话的 shell 并为新 cwd 创建终端，不再留下绑定到上一个会话的旧终端；终端 id 失效（如 serve 重启）后会停止重试，由“重新连接”按钮创建新 shell。终端状态栏现在持续标明 shell 不受沙箱保护、继承 serve 进程用户与环境变量，并展示 `/api/status` 上报的占位认证 token 警告；浏览器冒烟测试对提示与焦点重绑定均有断言。关闭活动 shell 时现在会在进程退出后立即返回，仅在宽限期后升级为 SIGKILL，不再每次都等待 2 秒；浏览器 cookie 会话不再收到长期 terminal token；被拒绝的配置更新在回滚前不会先应用终端限制或杀掉 shell；Windows ConPTY 包装现在保证句柄只关闭一次，并把 shell 加入 kill-on-close Job Object，终端关闭后遗留的后台子进程不会继续运行。

- **交互终端的恢复、焦点与安全提示已补齐。** 终端 dock 会始终提示它与可能正在运行的 Agent 共享焦点会话 cwd，并在 canonical execution 快照为本地或另一进程执行时升级警告。页面刷新会重新 attach 该标签页记录的 detached shell，而不是泄漏旧终端再新建一个；shell 已退出或终端 id 失效后，“重新连接”会创建新终端。xterm 样式已加载，`@xterm/xterm`/`@xterm/addon-fit` 改为精确 pin；PTY read loop 在发布 exit 帧前等待 `cmd.Wait`，非零退出码不再被误报为 0。工作台新增 1000 步 property test，并据此修复 Draft trajectory 模式持久化、删除 group/应用预设后的焦点修复、分隔权重精确夹紧、分页会话标签对账、命令建议重复 DOM id 与移动端右键菜单目标。

- **在 `/chat` 之外创建的新任务不会再因工作台重新挂载而丢失。** 工作台 hydration 改为应用启动时的一次性步骤，不再在懒加载的 Chat 视图每次挂载时重新读取 `localStorage`。此前从 Sidebar/Sessions 新建 Draft 后，若 300ms 持久化防抖尚未触发就切回 `/chat`，旧布局会覆盖内存中的新任务；现在应用启动后内存状态即权威，新开的页面仍会继承共享的最近布局。浏览器与单测均覆盖重新挂载和延迟 hydration 路径。
