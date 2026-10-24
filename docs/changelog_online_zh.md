# 更新日志（当前版本）

本文件仅记录**当前版本**的变更。所有版本的完整历史见 [docs/zh/changelog.md](zh/changelog.md)。

## v1.3.105-pre-4

### ✨ 新功能

- **新增 Gitee/Moark 模型：`kimi-k3.1-pro` 与 `kimi-k3.1-pro-highspeed`**
  - 为 `gitee` 与 `moark` 新增 `kimi-k3.1-pro` 与 `kimi-k3.1-pro-highspeed`：默认开启思考，使用 Kimi 的 `low`/`high`/`max` 推理强度档位（MothX 的 `minimal`/`low` 映射为 `low`，`medium`/`high` 映射为 `high`，`xhigh`/`max` 映射为 `max`），支持 1M 上下文与文本+图片输入。两者均通过 `kimi` 思考格式使用经过验证的 `reasoning_effort` 参数。

### 💥 行为变更

- **非托管网页搜索改用 DeepSeek Search/Fetch。** 原先通过 `webSearch` 的 provider/type/model 配置注册的 provider 搜索工具已被替换为 DeepSeek 官方 `POST /v1/tools/search` 与 `POST /v1/tools/fetch` 支撑的本地 `web_search`、`web_fetch`。OpenAI Responses 的 provider 原生托管搜索保持不变；Anthropic Messages 在 `webSearch` 启用时使用其 provider 原生托管搜索。两者都优先于本地 `web_search`。`webSearch` 现在配置 `provider`、`baseUrl` 与 `token`（未设置 token 时回退到 provider API key）；两个工具均为只读，可在 plan 模式使用。

- **新增腾讯元宝搜索后端。** `webSearch.backend` 现在可选择 `deepseek`（官方 Search/Fetch API）或 `yuanbao`（腾讯元宝 SearchPro，`POST /SearchPro`）。元宝后端发送 `{"Query": ...}`，把返回的 `Pages` 归一化为标题、URL、站点、时间、分数与正文摘要，并使用 `webSearch.token` 或 `${TENCENTCLOUD_WSA_APIKEY}` 鉴权；元宝目前只提供搜索，因此 `web_fetch` 仅在 DeepSeek 后端可用。

- **内置的 Windows BusyBox 现在要求 Windows 10 及以上。** `busybox-windows-x86.exe` 与 `busybox-windows-x86_64.exe` 改用该 fork 的 UTF-8 配置重新构建（x86_64 用 `mingw64u_defconfig`，x86 用 `mingw32w_defconfig`），带有 UTF-8 应用程序清单（`activeCodePage=UTF-8`）以及 `FEATURE_UTF8_INPUT`、`FEATURE_UTF8_CONSOLE`、`FEATURE_UTF8_AUTODETECT`、`FEATURE_UTF8_CHILD_ENV` 与 `UNICODE_SUPPORT`。非 ASCII 的 `sh -c` 命令文本现在以 UTF-8 传入 argv，不再被降级为系统 ANSI 代码页（简体中文 Windows 上为 GBK），子进程也保持 UTF-8 环境基线。由于构建时设置了 `FEATURE_FAIL_IF_UTF8_MANIFEST_UNSUPPORTED=y`，在 Windows 7/8 上 BusyBox 会拒绝启动；此时 `bash` 工具会像「BusyBox 不可用」一样自动回退到 PowerShell，命令仍可执行（只是没有 UTF-8 基线）。MothX 的支持范围仍标注为 Windows 10/11。

### 🐛 问题修复

- **Windows 捕获输出在捕获边界统一归一化。** 子进程各自决定管道编码——Python、Java、C 运行库与 shell 内建命令常用 ANSI 代码页（简体中文 Windows 上为 GBK），而 Go、Rust、Node.js 及多数 CLI 写 UTF-8，同一条命令还可能混用两者。现在不再注入针对单一运行时的环境变量，而是由 `bash` 工具通过 `platform.DecodeProcessOutput` 归一化捕获到的 stdout/stderr：非合法 UTF-8 的字节用当前代码页解码，合法 UTF-8 直接信任，混合输出则按「最大非 ASCII 连续段」分别判定。`MOTHX_WIN_OUTPUT_ENCODING=utf-8` 可强制 UTF-8，设为数字（如 `936`）可强制该代码页。

- **真正的 UTF-8 文本不再被误当作 GBK 重解码。** 两种编码在双字节序列上存在重叠，此前的「整块」启发式会对整段捕获只做一次判断，把真正的 UTF-8 文本重解码成无关的汉字（`Привет` → `袩褉懈胁械褌`，混有中文的行则变成 `涓枃`）。现在改为按非 ASCII 连续段分别判定，且仅当整条流没有更强的 UTF-8 证据时才可能翻转：只要 UTF-8 读法中存在 CJK 字符或任何 3/4 字节序列，就绝不再二次猜测；只有短的双字节片段才有资格翻转。字节本身与旧编码一致的情形（例如单个 GBK `写`）仍然照旧解码。

- **内置资产变化时会刷新已解压的 Windows BusyBox。** 已解压的副本会按 SHA-256 与内置资产比对，不同则以原子方式替换，因此升级后的 MothX 不会继续运行旧 shell。若正在运行的 BusyBox 锁住可执行映像，则本次运行继续使用旧副本，并在下次启动时重试刷新。

- **停止路径在 TUI/ACP 统一为共享 Runtime 决策，并修复竞态与后台终态。** ACP 的 `session/cancel` 现在在 `cancelMu` 下快照 Run ID，避免与 prompt 安装身份竞争；共享停止投影受有界 context 约束，不再阻塞 JSON-RPC 读循环。TUI 的中止/重置把 canonical stop 与终态写入移出 Bubble Tea 更新循环（退出时等待完成），避免 UI 卡顿。已被其他 owner 终态化的 canonical Run 会被本地执行收养（即使终态不同），并走完整终态广播，不再让会话永久 busy。恢复中的 Responses 后台任务被用户停止时现在记为 `cancelled` 而不是 `failed`。

- **Desktop 显示并查看子代理。** 会话头部现在显示当前会话的子代理数量与运行状态；点击可打开只读面板，列出全部子代理及其状态，并使用聊天窗口同一套消息、工具卡与思考块样式查看单个子代理的转录。ACP 新增 `mothx/subagent/list` 与 `mothx/subagent/messages` 只读投影及 `subagentPanel` 能力键，Desktop 不再只能依赖零散的 started/terminal 卡片。

## v1.3.105-pre-1

### ✨ 新功能

- **Gitee/Moark 新增模型：`deepseek-v4.1-flash-1017`**
  - `gitee` 和 `moark` 两个提供商均新增 `deepseek-v4.1-flash-1017`，紧随 `deepseek-v4.1-flash` 之后且参数完全一致：1M 上下文窗口、支持思考、文本/图片输入；默认不发送 max_tokens。

- **MiniMax 新增模型：`MiniMax-M3.1-Flash`**
  - `minimax` 提供商新增 `MiniMax-M3.1-Flash`：支持思考，并经 Anthropic 兼容协议的 `output_config.effort`（adaptive thinking 格式；`minimal` 与 `low` 映射为 `low`，其余档位一一映射为 `medium`/`high`/`xhigh`/`max`）支持思考强度；1M 上下文窗口，文本/图片/视频输入。预设声明 1M 的输出上限（即整个上下文窗口），请求仍使用 MothX 常规输出 token 默认值，除非用户显式调高。

- **火山引擎下线模型移除。** 火山引擎已下线 `kimi-k2.7-code`、`doubao-seed-2.1-turbo` 与 `minimax-m3`，`volcengine`、`volcengine-agentplan`、`volcengine-codingplan` 预设不再列出这些模型（含标准版的 `doubao-seed-2-1-turbo-260628` 快照）。同名模型在 Gitee/Moark 等其他 provider 仍可使用。由于预设顺序也是未配置默认模型时的回退顺序，未配置的标准版 `volcengine` 会话现在默认使用 `doubao-seed-evolving`，而非已下线的快照模型。

- **DeepSeek V4.1 思考强度。** Gitee/Moark 与火山引擎预设中的 DeepSeek V4.1 模型现改用已经验证的 `reasoning_effort` 参数，不再只发送思考开关。MothX 的 `minimal`、`low`、`medium`、`high`、`xhigh`、`max` 与网关刻度一一对应；`off` 会发送 `none`，关闭这些网关默认开启的思考。Gitee/Moark 与火山引擎 Agent Plan 已对完整合法枚举进行实测。所有 DeepSeek V4.1 模型 ID 默认使用该格式（包括第三方网关）；仅接受旧思考开关的部署可将 `thinkingFormat` 设为 `thinking-only` 退回。

- **不支持的 `reasoning_effort` 自动降级。** 当 OpenAI 兼容端点拒绝 `reasoning_effort` 时，MothX 会立即去掉该参数重试（保留 DeepSeek V4.1 的思考开关），在重试提示中说明原因，并把该模型的兼容配置记录为 `thinkingFormat: "thinking-only"` 与 `supportsReasoningEffort: false`，后续运行不再发送该参数。此降级不消耗重试次数。

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
- **EventStore 迁移有界且可自愈。** 旧会话 transcript 的一次性导出现在按 64 条一批写入，而不是单个无界 batch，因此无论 transcript 多大，单次 fsync 与单个 SQLite projection 事务都有界。导出也不再被残留或中断的状态卡死：在翻转之前内容存储仍是 SQLite，所以上一次中断导出留下的 EventStore 字节与 intent 会被丢弃并重新导出完整 transcript，而不是拒绝迁移、让 attach 失败。
