# 更新日志（当前版本）

本文件仅记录**当前版本**的变更。所有版本的完整历史见 [docs/zh/changelog.md](zh/changelog.md)。

## v1.3.101

### ✨ 新功能

- **Serve：模板 API Token 的替换警告**
  - `mothx serve init-config` 生成的模板 token 是公开值，一旦启用 auth 而未替换，等同于公开 API key —— 此前对此没有任何提示。该 token 现在是具名常量（`serve.PlaceholderAuthToken`）并配有显式判定（`IsPlaceholderAuthToken`/`UsesPlaceholderAuthToken`）：创建模板时（`mothx serve init-config` 与 CLI 的 `--init-serve` 路径）打印替换警告，`mothx serve` 启动时若 `api.auth.enabled` 为真且 token 仍未替换，会再次打印同一条警告。
  - 启动检查只在 auth 启用时生效，默认模板（auth 关闭、仅监听本机）保持安静，已替换的 token 永不误报。`serve init-config` 改为写入命令自身的 stderr，警告随命令输出一起可见。

### 🐛 问题修复

- **SQLite：瞬时 busy 的事务开启会重试**
  - 多个进程打开同一个会话目录时会争抢唯一的写锁。DSN 对所有非只读事务使用 `BEGIN IMMEDIATE`，因此当其他进程在 `synchronous(FULL)` 下持续提交时，某次 begin 的等待可能超过连接的 `busy_timeout`，让健康的数据库直接报 `database is locked (5)`。
  - 重试策略现在与 DSN 归属一起放在 `internal/db`：`BeginTx`（Bun）、`BeginSQLTx`（原生 `*sql.DB`）与 `RunInTx` 只对 `SQLITE_BUSY`/`SQLITE_LOCKED` 在有限预算（90 秒）内退避重试（200ms 起、上限 2s）；非瞬时错误原样返回，调用方的 context deadline 优先。
  - DAO 的 `Begin`/`BeginTx`/`RunInTx`（含 bindings 助手）、`internal/db.Write` 以及 session 的建表与迁移边界全部改走该策略，并发启动与普通写入不再把瞬时写锁冲突变成硬失败。

- **Workflow：失控 DSL 脚本受墙钟预算约束**
  - 当调用方的 context 不带 deadline 时，`while (true) {}` 这类 workflow 源码会让评估一直跑下去、把进程挂死。源码评估（只构建节点图，worker agent 之后原生执行）现在同时受两道边界约束：调用方 context 与墙钟预算，谁先触发谁中断 VM。
  - 预算为：workflow 运行 30 秒，交互式创作检查 `workflow_lint` 5 秒（必须快速失败）；超时映射为 sentinel `ErrJSEvaluationTimeout`（lint 结果是稳定可读的错误），调用方取消仍按原契约返回 context 错误。`Runner.EvalTimeout` 允许调用方收紧预算，零值保持上述默认常量。

### ✅ 测试

- 数据库：`internal/db` 固化 begin 重试策略 —— 只有 SQLITE_BUSY/SQLITE_LOCKED 会重试，其他驱动错误与到期 context 原样上抛，驱动码经错误自身的 `Code()` 分类，`RunInTx` 保持提交/回滚语义。
- Workflow：失控脚本在 50ms 预算下被中断（修复前该用例会挂死），正常源码评估行为不变，lint 路径返回 invalid 与超时文案。
- Serve：生成的模板与判定使用同一常量，带首尾空白的占位符仍被识别而真实/空 token 不会误报，`serve init-config` 的输出必须包含警告。
- Agent 循环：十个真实 `bash echo` 调用经由同一个并行批次执行（同步屏障只在十个 worker 同时存活时放行），并以顺序启动用例固化启动语义 —— 按模型声明顺序上报启动、进行中的调用不被更早的审批等待阻塞、更早调用失败时释放排队调用、后台工具调用复用同一套顺序句柄。
- Runtime：跨进程接管用例改为在有限预算内重试「过期 + 接管」这一对操作，并给 helper 启动留出容忍负载的窗口，因此慢机器上的失败会停在有明确诊断的 setup 阶段，而不是误判被测不变量。
