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

- **新增 Agnes AI 供应商（国际版 + 国内版）**
  - 通过新的 `agnes` OpenAI 兼容厂商适配器新增 `agnes`（`https://apihub.agnes-ai.com/v1`，`${AGNES_API_KEY}`）与 `agnes-cn`（`https://api.agnes-ai.cn/v1`，`${AGNES_CN_API_KEY}`）两个提供商，均提供 `agnes-2.5-flash`（200K 上下文）、`agnes-2.5-pro`（256K 上下文）和 `agnes-3.0-flash`（512K 上下文，最大输出 65535 tokens）。
  - 三个模型均标记为支持思考（reasoning）与多模态（`text,image`）。`agnes-2.5-flash` 和 `agnes-2.5-pro` 自身不发送默认 `max_tokens`，使用供应商默认值；`agnes-3.0-flash` 最大输出为 65535 tokens。

- **数据库被重建时通知其他 mothx 进程**
  - 某个进程因 schema 迁移失败而备份并重建数据库后，现在会通过已有的 advisory UDP 总线广播 `database_rebuilt` 通知。其他共享同一会话目录的 mothx 进程收到后会丢弃自己缓存的数据库连接（否则会继续通过旧句柄读写已被替换的文件）、记录日志，并在 TUI 中提示用户。通知只带被替换的文件路径，迁移原因和备份路径仍保留在重建进程侧。
  - 总线仍是仅限本机：`127.255.255.255` 定向广播且只接受 loopback 来源，报文不会离开本机。

- **迭代预算对模型可见，并可有界续期**
  - Agent 主循环的 `MaxIterations` 此前是一个隐藏的硬计数器：预算压力事件只被适配器渲染、从未到达模型，run 内也没有任何路径能提高上限，于是确实在推进的长任务仍会在恰好 200 轮被判为 `incomplete`，模型既没有机会收敛也无法说明。
  - 现在当剩余轮数跨过压力阈值时，预算会以一条尾部追加的瞬时系统消息注入 run 上下文，模型因此能看到倒计时。该消息不写入 transcript，缓存断点也会跳过它，因此不破坏 prompt cache 前缀。新增经 Runtime 确定性 clamp 的 `extend_budget` 工具，模型可附上具体理由申请更多轮数，由 Runtime 决定是否放行。
  - `IterationBudgetPolicy`（软/硬上限、放大幅度、最大续期次数、最小间隔、16 小时墙钟）在共享 Runtime 中统一规范化，且只对会话的对话 lead 生效：瞬时构建、辅助角色、子代理与专家团成员保持其能力上限，且不会拿到该工具。每条退出路径仍只产生一个终态事件；渠道 run 看门狗与后台轮询的默认时长同步提升到 57600s，与墙钟预算一致。

- **`mothx pure`：归档会话数据库并重新开始**
  - 此前要重新开始只能手动删除 `sessions.db`，既具破坏性又容易出错：残留的 `-wal` 或 `-shm` 附属文件会被下一个数据库继承。新增 `mothx pure` 子命令会把共享的 `sessions.db` 及其附属文件移走，并在原位置创建全新的、已完成迁移的数据库。不会删除任何内容：每个被移动的文件都会重命名为 `sessions.db.pure-<时间戳>.bak` 保存在新数据库旁边（附属文件保留自己的后缀），因此原有会话仍可恢复。`--session-dir` 可指定目录，默认使用已配置的会话目录。
  - 主数据库已丢失的孤立附属文件（重置被打断或被手工删除）也会被同样归档，因此新数据库不会继承残留的 WAL，启动结果也不再取决于 SQLite 如何处置这些残留。
  - 若目标目录仍有其他进程持有活跃的会话运行，命令会拒绝重置，并列出会话 ID、归属进程 pid 与运行 ID；`--force` 可跳过该检查。无法读取的数据库属于“所有权未知”，不能据此认定没有进程正在写入，因此同样会拒绝，需显式 `--force`。
  - `pure` 现在还会说明它留下了什么，而留下的原因各目录不同：`channels/` 是自带 `sessions.db` 的独立会话根目录，每个 `knowledge-bases/<id>.db` 就是该知识库自己的权威存储，只有 `artifacts/` 的内容会在记录被归档后变得无法寻址。因此仅最后一类可回收：它会连容量一起列出，其余两类是重置不得当作垃圾的数据，仅如实报告。
  - 若文件已归档但新数据库创建失败，错误会列出归档路径，重置失败不会再被误读为数据丢失。

- **回收无引用的附件存储**
  - 此前附件字节只能通过“由数据库行驱动”的过期路径删除，因此删除会话、内容已提交但记录写入失败、或数据库被归档后留下的存储既无法寻址也永不会被回收。现在 Runtime 还会从目录侧对私有存储做一致性清扫：在附件写入路径上触发（每进程每小时至多一次），并由一个 Runtime 拥有的 cron 维护任务（`mothx-maintenance:artifact-storage`，每日一次；属主机维护而非用户自动化任务，因此不会出现在用户 cron 列表里）每天执行一次；只删除没有任何行引用、**且**最后写入时间早于附件保留期外加 24 小时缓冲的目录——正是这个下限保证回收不会删掉“恢复归档后仍可用”的数据。无法读取会话数据库时一律不删，不跟随符号链接，也只处理与自己生成的 ID 形状一致的目录，因此不可能波及无关数据。`mothx pure --prune-unreferenced` 可按需执行同一次清扫，并报告回收量与因太新而保留的数量；`channels/` 与 `knowledge-bases/` 不在其范围内。
  - 新增可选的 `settings.json` 子对象 `maintenance` 控制它：`reclaimAttachmentStorage`（默认 `true`，因此现有配置文件不会改变行为）与 `storageReconcileSchedule`（默认 `@daily`，由调度器校验，无法解析时回退默认值并输出日志）。关闭后会在下次调度器启动时移除该计划任务，即使旧进程留下的任务行被触发也不会删除任何东西；只改频率时保留任务的运行次数与状态。

### 🐛 问题修复

- **供应商流超时在各类运行中保持一致恢复**
  - 调用方 deadline（包括曾经包在 ESM 角色外层的 deadline）此前可能被显示成供应商响应超时，并误称“已重试 0 次”。现在调用方取消和 deadline 会保持为已取消的运行，不再被误归类为供应商传输故障。
  - ESM worker、critic 与 audit 角色不再施加 30 分钟 deadline：长任务目标会持续执行，直到完成或被显式取消；受限的 recovery observer 仍保留有界时长。
  - 供应商流真正停滞时，Agent Core 现在会以指数退避持续重试，直至取消。若已输出部分文本或思考，则先持久化安全的部分响应并携带续写指令；这些恢复重试不消耗 Agent 迭代预算。该共享恢复路径同样适用于普通运行、ESM 和其他适配器运行，不会重新执行已经发出的工具调用。

- **ESM 长任务不再被内部熔断器中断**
  - worker、critic 与 audit 角色现在使用 Agent Core 明确定义的无界迭代策略。角色以 `incomplete` 结束时会被标记为未完成并记录为可恢复工作，绝不会投影成成功。
  - 恢复次数和完成拒绝次数仍会展示以便诊断，但都不会暂停活跃 ESM 目标。完成声明被拒绝代表仍有工作，下一次续跑会从持久化状态继续。

- **会话删除由 Runtime 变更租约进行栅栏保护**
  - 删除会话（TUI `/sessions`、ACP `session/delete`、Serve `/clear` 与会话删除、CLI）此前会直接移除其数据行与句柄，不检查是否有其他进程仍持有该会话执行，因此并发运行可能在脚下失去会话。现在删除会先获取该会话的共享 Runtime 变更租约，并在删除事务内再次校验带栅栏的 `owner`/`epoch`/`token` 身份；多会话级联删除复用已持有的租约组，不再为每个子会话重复获取进程内锁。
  - `mothx pure` 使用的租约预检现在以只读方式打开会话数据库，不执行迁移、完整性修复或 WAL 设置，因此安全检查不会修改它所检查的数据库。

- **未对外声明的工具调用在执行前被拒绝**
  - 供应商响应属于不可信输入：此前只要供应商发出工具调用，agent 就会执行，即使当前模式并未对外声明该工具，因此幻觉或被注入的调用仍可能进入审批、持久化声明与注册表。现在执行前会先检查该运行的冻结注册快照，对未注册到该运行的工具调用直接返回工具错误，注册表变更只对后续构建的 Agent 生效。

- **会话身份只在准备工作全部成功后才提交**
  - `BindSession` 会在加载会话的专家绑定与上下文资源之前就发布新的会话身份，因此加载失败时 Runtime 已挂在一个初始化到一半的会话上，而调用方却认为绑定失败。现在先解析这些依赖会话的资源，全部成功后才发布身份；TUI 的 `activateSession` 也改为在决策恢复与 Runtime 绑定都成功之后才提交 `session`/`cwd`，失败的切换不会再让适配器与 Runtime 停留在不同会话上。
  - 绑定时会为新绑定的会话重建 Runtime 拥有的附件与输入服务。因此长生命周期的 Runtime（TUI 在 `/sessions` 切换时复用一个，CLI 的 `--shared-runtime` 也复用同一个）在切换会话后，不会再把新的上传写入第一个会话的附件目录。
  - Serve 的 `/clear` 会创建新的会话管理器，却仍把会话的 Runtime 绑定在已删除的旧管理器上，导致下一次提示在一个已不存在的会话上执行。现在清除会话后会在复用前把 Runtime 重新绑定到新的管理器。

- **SQLite：过期二级索引会被修复，而不是阻断启动**
  - 写入被中断后，即使所有表页完好，`PRAGMA quick_check` 也可能报出 `wrong # of entries in index`，此前完整性检查会直接让启动失败且无路可走。索引内容由表行派生，因此现在对这一精确情形执行 `REINDEX` 并复检后才继续；其他完整性失败仍会中止启动，因为它们可能影响规范数据。
  - 拿不到写锁的修复现在会被归为争用（另一进程仍持有该数据库，索引未被重建），而不再看起来像“修复一个损坏文件失败”。重试窗口复用托管连接本就携带的 busy 预算；锁释放后，未被改动的数据库仍能被正常修复。
  - 已完成的修复现在会“恰好一次”上报（CLI 与 TUI 的启动提示，serve/ACP/渠道的日志），因为需要修复索引意味着这个数据库已经经受过一次写入中断。

- **会话运行时租约不再被短暂的数据库抖动打断**
  - 长任务偶尔会被 `session runtime lease was lost` 中断，即使并没有其他进程占用该会话。“每租约一个心跳”被合并为“每个会话目录一个调度器”时引入了一个竞态：只要某个 tick 观察到该目录为空，调度器就退出循环，即便片刻前刚申请到的租约仍然存活；它在调度器注册表里留下一个“已停止但仍被登记”的条目，后续申请再也无法替换它，于是该租约永远得不到续期，之后某次执行期写入便发现它已过期。
  - 现在调度器只有在真正把自己从空闲目录的注册表中注销后才退出；而仅因数据库繁忙/不可达导致的续期超时会在下一个心跳 tick 继续重试，不再被当作归属丢失。归属由 `owner`/`epoch`/`token` 的 fenced CAS 判定，而非墙钟过期：抢占必然 bump epoch，所以“过期但仍带自己身份”的行依然属于自己。只有真正的 fenced 抢占或已 release 才判定丢失。心跳重试预算改由共享的 `busy_timeout`（`db.BusyTimeout`）推导，使单个 tick 能完整消化一次被争用的 begin，并且租约丢失现在会带原因打日志。

- **渠道子代理的终态事件在父 run 结束后仍能送达观察者**
  - 渠道会话派生的子代理可能比父 run 的事件流活得更久（例如随 run 结束被取消）。此前 dispatcher 在父 run 一结束就删除 root-agent → session 映射，因为它检查的是 dispatcher 级别的 manager，而不是真正持有子代理的会话级 manager；于是迟到的终态事件无法再路由，观察者会永远停在 “running”，已完成或失败的成员也永远不会被上报。现在只要 root 仍在运行或还有子 agent 存活，映射就会保留；当 root 已结束且最后一个子代理进入终态后才释放。

- **Web UI：知识库扫描状态在刷新后不再丢失**
  - 在 Web UI 知识库页面点击重新扫描时，HTTP 请求会一直阻塞到整个索引完成，且没有记录任何进行中的作业信息，于是该知识库只会显示为「未索引」，刷新页面后扫描状态就完全消失了。
  - 现在 serve 处理器通过进程级缓存的 Runtime service 把扫描作为后台索引作业提交并立即返回运行中的作业投影，list/get 接口使用与 ACP 相同的投影暴露实时的 `indexing` 进度（阶段、已完成/总文件数）。知识库页面会渲染当前阶段，并在扫描进行时轮询，因此刷新页面会继续显示状态，而不是把它丢掉。
  - serve 端缓存的 service 现在会在 settings.json 变更时刷新自身的 settings 快照，因此后续扫描会使用更新后的 Indexer provider/model，同时保留进行中的后台作业注册表。

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

- **Desktop：技能市场默认选中 SkillHub.cn 而非 ClawHub**
  - Desktop 技能页的市场此前取 ACP 市场列表的第一个条目，而该列表按字母序排列，`clawhub.ai` 排在 `skillhub.cn` 之前，于是即使全局配置的 `skillHub.defaultMarket`（产品默认即 SkillHub.cn）另有指定，目录也会默认落在 ClawHub。默认市场是规范配置状态，适配器不应按列表顺序猜测。
  - `mothx/manage/skillhub/markets` 现在增量投影按 settings 解析的 `defaultMarket`（留空时回落产品默认 `skillhub.cn`），Desktop 目录引导按「用户已选 → ACP 投影的默认市场 → 首个市场」解析；categories/search/detail/install 的兑底市场也改走同一解析器，显式留空的配置不再报 `unsupported skill market`。

- **数据库迁移失败时改为备份并重建，不再阻塞启动**
  - 当 `sessions.db` 的 schema 无法被当前版本升级时（迁移不可应用，或表缺少必需列），此前每条命令都会以 `database schema is incompatible` 失败，用户除了手动删库没有别的出路。现在 `internal/db` 会对每个数据库做一次恢复：把无法迁移的库快照到原文件旁边（`sessions.db.migration-failed-<时间戳>.bak`，优先使用 SQLite `VACUUM INTO`，VACUUM 本身失败时回退为 checkpoint 加原始文件拷贝），删除旧文件集（含 `-wal`/`-shm`/`-journal`），再在其位置新建空库。
  - 恢复会明确告知用户而非静默处理：`internal/db` 记录日志，CLI/TUI 在启动时打印 “Database migration error” 提示并给出备份文件路径（旧会话都还在里面）。只有 schema 拥有者（`internal/session`）能把失败标记为可重建；写锁竞争、迁移被取消、只读文件等情况一律保持数据库原样，并把原因附在错误信息里，因此健康数据库在外部压力下永远不会被替换。

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
