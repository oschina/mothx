# 更新日志（当前版本）

本文件仅记录**当前版本**的变更。所有版本的完整历史见 [docs/zh/changelog.md](zh/changelog.md)。

## v1.3.105-pre-1

### ✨ 新功能

- **会话更早拒绝不可用的工作目录。** `internal/session` 成为工作目录校验的唯一 owner，Runtime API 仅做委托。创建、重开、重绑定会话，ACP `mothx/session/setWorkDir` 与消息通道解析会话时，都必须先确认目标是存在的绝对目录，才会持久化或组装沙箱及工具资源。ACP 对同目录请求不再因目录已消失而拆掉可用 Runtime；通道运行也会校验已绑定会话持久化的 cwd，而不是平台默认目录。
- **Desktop 使用显式的草稿目录模型。** 移除了遗留的 workspace 默认回退及其设置面板。Quick Start 草稿获得一个进程内 `mkdtemp` 临时目录；New Task 草稿保持未设置，直到原生目录选择器或 worktree 操作提供目录。选择历史只为下一次对话框定位便利；草稿状态携带 generation，迟到的 quick-start、picker 或 ACP 回调不会覆盖更新的 New Task。
- **Runtime 统一协调本地会话提交。** `SessionRuntime` 为每个 `(database identity, session ID)` 持有一个 FIFO `SubmissionCoordinator`，`ExecutionRuntime.Completion` 成为权威完成屏障。TUI 排队输入、ACP prompt admission、Channel/Cron/ESM/Knowledge 的准入交接、关闭、取消以及本地/外部忙碌的结构化分类共享同一生命周期，不再轮询 lease 错误或以 Agent 结束事件启动后继提交。

### 🐛 问题修复

- **轮换已绑定频道会话时保留其持久目录。** 轮换继承既有会话记录的 cwd，而非平台默认目录；fenced CAS 比较规范化后的路径，因此末尾分隔符等等价存储形式仍可成功轮换，真实并发变更仍会被检测并拒绝。
- **飞书工具图片清理具备并发安全性。** 每次投递都在唯一的私有临时目录下物化图片，并在所有退出路径清理整个目录，避免并行运行互相收集文件，也不会把图片字节残留在工作目录。
- **会话事件存储迁移入口统一。** 旧会话迁移此前只在 `SessionRuntime.BindSession` 触发，因此 ACP/Desktop、Channel、ESM、后台运行等直接装配会话的入口会把老会话留在 legacy SQLite transcript 上，新建会话也停在 `sqlite` 内容存储。现在 `BindSession`、`AttachSessionResources` 与 `Builder.Build` 三条装配路径调用同一个迁移入口 `Manager.MigrateToEventStore`，不再各自新增包装方法或特殊处理：老会话被导出到按会话隔离的 EventStore、新会话被翻转到 EventStore，入口之间不再产生内容归属分歧。该入口现在可无条件调用——当会话正被其他 owner 运行时它是成功的 no-op（不阻塞 attach，legacy store 继续自洽服务，稍后空闲的 attach 再完成迁移），只有损坏或不可回放的老数据才 fail closed。
- **运行处于 terminalizing 时终止会话不再失败。** 当 durable Run 已进入已提交的 `terminalizing` 窗口后一次终态写入失败，它会把 run 行留在只有终态转换才能推进的状态，而 Web UI 的停止路径仍试图强制 `terminalizing` -> `cancelling`——该转换会被 run store 拒绝，于是请求返回 `persist run cancellation: invalid session run transition "terminalizing" -> "cancelling"`，且该 run 看起来永远无法取消。现在 `ExecutionRuntime.CancelDurable` 与关闭时的取消路径会识别已准备好的 terminalizing 终态事务，跳过非法的 cancelling 写入，接受取消请求（仍会取消 loop），并在其重试已停止时重启已准备好的终态写入，使被中断的运行仍能到达终态。
- **向会话 EventStore 追加不再随 transcript 大小平方增长。** EventStore 追加此前每个 batch 都要多次重读整段日志——一次健康检查、一次幂等扫描、一次 next-seq 扫描，以及一次 commit 期有效性扫描——于是追加成本随会话累计内容增长（512 entries：每 batch 3.8ms → 178ms，累计 5.7s），而 legacy SQLite 路径保持恒定。现在健康门在进程内 append-state 缓存对当前物理 revision 新鲜时直接信任它，只需确认每个 SQLite intent 均已 committed；`NextSequence` 读取缓存的 cursor；幂等探测走 projection 索引，只有真正重试时才读全量 committed transcript；`CommitEventIntent` 接收刚 fsync 过的 batch 而不再重扫。追加成本现为 transcript 大小的常数级（同探针：每 batch 约 3ms，累计 0.30s，约 19×），而 intent/lease 协议、围栏与恢复校验保持不变。一个确定性回归测试固定「向已暖机会话追加不做整段扫描」。
- **新建 Web UI 会话不再报 `session_not_found`。** 服务端分配的 Web UI 会话要到首次运行开始才会落盘，但 run event、capability event 与 sub-agent 三个查询会对这个尚未落盘的 id 返回 `session_not_found`，于是新建会话时界面弹出错误。现在这三个查询对已分配的 id 投影空结果——与 trajectory、capabilities、runtime、messages 一致——而任意未知 id 仍然被拒绝。
