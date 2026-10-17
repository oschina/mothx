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
