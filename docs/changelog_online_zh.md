# 更新日志（当前版本）

本文件仅记录**当前版本**的变更。所有版本的完整历史见 [docs/zh/changelog.md](zh/changelog.md)。

## v1.3.101

### ✨ 新功能

- **Serve：模板 API Token 的替换警告**
  - `mothx serve init-config` 生成的模板 token 是公开值，一旦启用 auth 而未替换，等同于公开 API key —— 此前对此没有任何提示。该 token 现在是具名常量（`serve.PlaceholderAuthToken`）并配有显式判定（`IsPlaceholderAuthToken`/`UsesPlaceholderAuthToken`）：创建模板时（`mothx serve init-config` 与 CLI 的 `--init-serve` 路径）打印替换警告，`mothx serve` 启动时若 `api.auth.enabled` 为真且 token 仍未替换，会再次打印同一条警告。
  - 启动检查只在 auth 启用时生效，默认模板（auth 关闭、仅监听本机）保持安静，已替换的 token 永不误报。`serve init-config` 改为写入命令自身的 stderr，警告随命令输出一起可见。

- **成员在交互式界面上会等待 lead**
  - 能派生成员、但未绑定专家团的会话，在交互式来源（TUI、Web UI、ACP）上会在收尾轮为仍在运行的成员保持 run 打开，让成员在同一次 run 内完成或提问，而不必等到 lead 的下一次 run。无头与异步来源（CLI、cron、微信/飞书）仍正常结束回合，在下一个迭代或下一次 run 投递成员通知；绑定专家团的会话始终等待。

- **绑定团队始终保留完整 sub-agent 工具集**
  - 绑定专家团后始终暴露完整的规范 sub-agent 工具集（`subagent_spawn`、`subagent_status`、`subagent_send`、`subagent_wait`、`subagent_answer`、`subagent_destroy`）。逐工具关闭只对非团队的多 Agent 会话生效；团队能力是权威的，不会因关闭单个工具而从团队会话移除工具。

- **Gitee/Moark 新增模型：`deepseek-v4.1-flash`**
  - `gitee` 和 `moark` 两个提供商均新增 `deepseek-v4.1-flash`，支持 1M 上下文窗口与文本/图片输入；默认不发送 max_tokens。

### 🐛 问题修复

- **被内容审核拒绝的图片不再让整个会话失效**
  - 供应商的内容策略拒绝——例如 DashScope/千问的 `InternalError.Algo.DataInspectionFailed: Input image data may contain inappropriate content`——以 HTTP 400 返回，但此前所有 4xx 都被当作可重试。同一张被拒的图片会在 provider 的退避重试与 Agent 的流失败重试中被反复发送（数分钟的 "Retrying…"），而拒绝是永久性的，最终 run 仍然失败；更糟的是，出问题的图片留在持久化历史里，之后的每一轮都会重发它，于是什么都无法继续，只有新建会话才能恢复——连 `/clear` 都不行，因为它会重新加载同一份历史。
  - `provider.IsContentRejectionError` 现在单独识别这一窄类文案（data inspection、content policy/moderation/filter、"inappropriate content"），并让 `IsRetryable` 对它返回 false，失败因此立即浮现而不再消耗重试预算。Agent Core 随后就地自愈：先剥离本轮新增的图片，若仍被拒再剥离整个对话中的图片，把每张替换为模型可见的说明（告知该图片被供应商内容过滤拦截、像素已不可用），并追加一条可重放的 `content_override` 会话记录，使重放（同进程或重新加载后）都不会再次发送该图片。run 会在不含该图片的情况下重试，会话得以继续；若该回合已经流出可见输出，则只做自愈不再重跑，避免输出重复。

- **Serve：Windows 上保存配置不再报 "Access is denied"**
  - 在 Windows（包括把数据放在 exFAT 移动磁盘上的便携部署）上，通过 Web UI 启用微信/飞书通道或保存任何 `serve.json` 变更都会失败，提示 `sync config directory: Access is denied`。原子配置写入在重命名后会对父目录执行 fsync——这是 POSIX 的持久化惯例——但在 Windows 上，`FlushFileBuffers` 作用于只读目录句柄时，在任何文件系统（包括 exFAT）上都必然返回 `ERROR_ACCESS_DENIED`。由于失败发生在配置文件已经替换到位之后，接口返回了错误，但新配置从未应用到运行时。
  - 现在在 Windows 上跳过重命名后的目录刷新（与 etcd/bolt 的做法一致）；配置文件本身仍在重命名前 fsync，持久性不受影响，Unix 平台行为不变。

- **MCP：图片类工具结果不再退化为占位符，模型能看到真实图像**
  - 返回图片内容的 MCP 工具，到达模型时只剩字面量 `[image content: image/png]`。base64 载荷其实已经在 MCP 响应里，但客户端把所有内容块一律解码成文本，工具也只返回文本结果，于是截图类 MCP server 能报告坐标、模型却看不到画面。`resources/read` 的二进制资源更彻底：`blob` 字段没有对应的结构体字段，反序列化时整个载荷被丢弃，连占位符都不会出现。
  - 现在 `tools/call` 与 `resources/read` 会把 image 块投影为 `tools.ToolResult.Contents` 中真实的 provider 图片内容，复用与 `read`、`browser` 截图工具完全相同的 provider 感知预处理，并补上 `blob`/`uri` 资源字段的解码。无图片的结果保持历史文本形态，现有文本类 MCP 工具行为不变；解码失败、超限或超量（单次上限 4 张，与 ACP 投影上限一致）的图片降级为文本说明而非让调用失败。非视觉模型是否接收图片仍由 Agent Core 的图片能力闸门决定。

- **SQLite：瞬时 busy 的事务开启会重试**
  - 多个进程打开同一个会话目录时会争抢唯一的写锁。DSN 对所有非只读事务使用 `BEGIN IMMEDIATE`，因此当其他进程在 `synchronous(FULL)` 下持续提交时，某次 begin 的等待可能超过连接的 `busy_timeout`，让健康的数据库直接报 `database is locked (5)`。
  - 重试策略现在与 DSN 归属一起放在 `internal/db`：`BeginTx`（Bun）、`BeginSQLTx`（原生 `*sql.DB`）与 `RunInTx` 只对 `SQLITE_BUSY`/`SQLITE_LOCKED` 在有限预算（90 秒）内退避重试（200ms 起、上限 2s）；非瞬时错误原样返回，调用方的 context deadline 优先。
  - DAO 的 `Begin`/`BeginTx`/`RunInTx`（含 bindings 助手）、`internal/db.Write` 以及 session 的建表与迁移边界全部改走该策略，并发启动与普通写入不再把瞬时写锁冲突变成硬失败。

- **Workflow：失控 DSL 脚本受墙钟预算约束**
  - 当调用方的 context 不带 deadline 时，`while (true) {}` 这类 workflow 源码会让评估一直跑下去、把进程挂死。源码评估（只构建节点图，worker agent 之后原生执行）现在同时受两道边界约束：调用方 context 与墙钟预算，谁先触发谁中断 VM。
  - 预算为：workflow 运行 30 秒，交互式创作检查 `workflow_lint` 5 秒（必须快速失败）；超时映射为 sentinel `ErrJSEvaluationTimeout`（lint 结果是稳定可读的错误），调用方取消仍按原契约返回 context 错误。`Runner.EvalTimeout` 允许调用方收紧预算，零值保持上述默认常量。

- **已取消/已过期的决策不再阻塞分叉**
  - 若会话唯一的决策其实已被取消或超时，此前仍被当作存在待处理决策，导致分叉被以 `source session is active` 拒绝。现在所有决策账本读者共享同一套词汇表，取消/超时决策（以及旧的渠道请求事件名）都能正确清除，分叉得以继续。决策事件名与 `{"decision": …}` 信封各自有了单一属主，因此无论哪个界面写入，跨入口的决策恢复都读取同一批记录。

- **流中途网络中断自动重试，不再直接终止回复**
  - 供应商流在已输出正文/思考内容之后遭遇瞬时传输错误（`connection reset by peer`、意外 EOF、网关 5xx 等）时，此前整个 Run 直接以 `stream read error: ...` 失败：供应商级重试只覆盖尚未出现可见输出的流，Agent 级重试只覆盖空闲流超时。
  - 现在 Agent 循环会对这类瞬时错误做有限续写重试（最多 2 次）：已输出的部分内容被持久化进历史，并注入引用精确后缀的续写指令，模型从中断点直接继续生成，不会重复用户已经看到的内容；尚无可见输出时则直接重跑整轮。已发出工具调用的回合、上下文溢出（有专门的压缩恢复路径）与空闲流超时（有专门的重试路径）保持原有行为，Responses 远端状态回合也继续沿用既有的 failover 路径。

### 🔧 改进

- **SQLite：会话库写压力三阶段优化**
  - 连接持久化策略从 `synchronous(FULL)` 调整为 WAL 推荐的 `synchronous(NORMAL)`：提交不再在持有唯一写锁期间 fsync（fsync 集中到 checkpoint），多进程共享同一会话目录时的写锁占用从 fsync 级收缩到 page-cache 级，此前记录的「其他进程持续提交导致 begin 等待超过 busy_timeout 而报 database is locked」的场景基本消除。进程崩溃仍然零丢失；OS 崩溃/断电可能回退最近一次 checkpoint 之后的秒级提交（数据库不损坏，缺失的 run 终态由既有的租约过期 → orphan → 有界恢复路径收敛）。`MOTHX_SQLITE_SYNCHRONOUS=FULL` 可按进程一键恢复旧持久性，新旧版本进程混布共享同一库文件是安全的。
  - 工具结果合批落盘：会话域新增 `AppendMessages`，agent 一轮迭代的多条工具结果以父子链单事务写入（每事务上限 64 条，超出自动分批），租约围栏与叶子乐观检查仍在写入同一事务内完成；工具密集轮次的写事务从 N+3 降到约 3。assistant 消息先于工具副作用落盘、批量失败即 `session_save` 失败的语义保持不变。
  - 租约心跳合并：从每个活跃租约独立 goroutine 每 3 秒一次续期事务，改为每会话目录一个调度器把本进程在该库的全部租约放进单事务批量续期（每租约 owner/epoch/token CAS 围栏不变，被顶替或已释放的租约仍只影响自己）；稳态后台心跳写从 N 事务/3s 降为 1 事务/3s/进程。TTL、心跳间隔、重试预算与 30 秒有界恢复保证全部不变，最后一个租约释放后调度器自动退出。
  - `internal/db` 新增进程级 busy 重试与事务 begin 等待指标（`BusyRetryStats`/`BeginWaitStats`），经 expvar 以 `mothx_sqlite` 发布，`--debug` 启动时可在 pprof 服务器的 `/debug/vars` 直接读取，跨进程写锁竞争从此可观测。
  - 完整方案、多进程论证与压测矩阵见 `docs/proposal/sqlite-write-pressure-reduction-proposal.md`。

### ✅ 测试

- 数据库：`internal/db` 固化 begin 重试策略 —— 只有 SQLITE_BUSY/SQLITE_LOCKED 会重试，其他驱动错误与到期 context 原样上抛，驱动码经错误自身的 `Code()` 分类，`RunInTx` 保持提交/回滚语义。
- Workflow：失控脚本在 50ms 预算下被中断（修复前该用例会挂死），正常源码评估行为不变，lint 路径返回 invalid 与超时文案。
- Serve：生成的模板与判定使用同一常量，带首尾空白的占位符仍被识别而真实/空 token 不会误报，`serve init-config` 的输出必须包含警告。
- Agent 循环：十个真实 `bash echo` 调用经由同一个并行批次执行（同步屏障只在十个 worker 同时存活时放行），并以顺序启动用例固化启动语义 —— 按模型声明顺序上报启动、进行中的调用不被更早的审批等待阻塞、更早调用失败时释放排队调用、后台工具调用复用同一套顺序句柄。
- Runtime：跨进程接管用例改为在有限预算内重试「过期 + 接管」这一对操作，并给 helper 启动留出容忍负载的窗口，因此慢机器上的失败会停在有明确诊断的 setup 阶段，而不是误判被测不变量。
- 决策账本：覆盖共享事件名、信封与读取器的往返与重放，遗留渠道事件名仍可解码，且已取消决策不再阻塞分叉。
- 成员等待：交互式（TUI）非团队 lead 会为运行中的成员保持 run 打开，无头（CLI）则正常结束。
- 架构：新增守卫拒绝适配器测试中新增使用 legacy session run/lease API 或低层 `agent.New`，其余夹具由带原因的 allowlist 冻结。
- systeminit：固化共享 `/systeminit` 提示词 —— 交互式才有的 question 指引、去空白的附加指令置于 finalNote 之前、空白输入忽略、确定性。
- 流失败恢复：流中途 connection reset 经由续写重试恢复 —— 已输出部分被持久化并从精确后缀继续，无可见输出时整轮重跑，预算耗尽后上报原始错误 —— 而已经发出工具调用或错误不可重试的回合不会重试。
- SQLite 写压力：`internal/db` 固化 synchronous 默认 NORMAL、`MOTHX_SQLITE_SYNCHRONOUS=FULL` 覆盖与 busy 重试计数（永久错误不计数）；会话域覆盖 `AppendMessages` 的父链与重放顺序、stale writer 整批拒绝且不落任何行、超过事务上限自动分批、子代理表隔离；租约心跳调度器覆盖单目录单调度器、同批续期、epoch 被顶替的租约单独 lost 而幸存租约照常续期、最后一个租约释放后调度器退出。另新增写压力压测形状 A/B/C（多进程写不同会话、FULL/NORMAL 混布、单进程多会话多租约）并报告 busy/begin 竞争指标，`MOTHX_WRITE_PRESSURE_SCALE` 可放大负载用于基线对比。
