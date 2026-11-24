# 更新日志（当前版本）

本文件仅记录**当前版本**的变更。所有版本的完整历史见 [docs/zh/changelog.md](zh/changelog.md)。

## v1.3.109

### 💥 行为变更

- **Desktop（Electron）已弃用，MothX Native 成为主力桌面客户端。** `desktop-release.yml` 不再随版本 tag 自动运行，仅保留手动兼容构建桥，且不能再把 release 标记为 latest；`native-release.yml` 是桌面产物的唯一自动发布入口（Linux 二进制/deb/tar.gz/PKGBUILD、macOS dmg、Windows exe/setup）。根 Makefile help 将 Native 置前并把 Desktop targets 标记为弃用。新增双语指南（`docs/en/native.md`、`docs/zh/native.md`）覆盖安装、功能面与迁移；`desktop.md` 带弃用横幅与明确移除条件（完整一个发布周期不发布 Desktop 产物且无需手动兼容构建后删除 `desktop/` 及其入口）。没有任何权威数据需要搬移：会话、转录与配置都在共享 Runtime 中，Desktop 用户换壳即可，无需迁移。

### ✨ 新功能

- **Router 的 ACP profile 现在可以设置外部 worker 的默认模型。** `acpAgents` 新增可选 `model` 字段，由 Runtime 在创建远程 worker session 时应用。MothX 优先使用标准 ACP `configOptions` 的 model 选项（`category: "model"`）；只有当 worker 不提供现代选项时，才回退到已废弃的 `models`/`session/set_model` 协议。`model` 留空时保留第三方 Agent 自己的默认行为。若配置的模型不在 worker 提供列表中，委派会带着可用模型列表失败，而不是静默使用其他模型；恢复会话时若 Agent 不返回选项列表，则沿用其已保存的选择。该模型可从 TUI `/router` 面板、WebUI Router Worker 设置与 Desktop Router Workers 编辑，并投影在共享 Runtime 目录中。
- **Router 收敛为单一 worker 管理签名，并新增 loopback MCP bridge。** Router 模式只注册一个工具 `delegate_worker_mgr`，其 `delegate`/`spawn`/`status`/`list`/`send`/`wait`/`destroy`/`integrate`/`answer` action 覆盖完整的外部 ACP worker 生命周期；本地 `delegate_subagent` 与 `subagent_*` 工具不再出现在 Router，也不再广告 Router 专用字段。本地 stdio worker 可在 `session/new|load|resume` 收到一个名为 `mothx-worker` 的 MCP server，使用仅存内存的 per-child bearer token 鉴权；唯一工具 `worker_mgr` 支持 `context`、`report`（notice/progress/finding）与 `ask`（durable `DecisionQuestion`，由 lead 通过 `delegate_worker_mgr(action=answer)` 回答），并有 body 上限、每 child 限流、dedupe key 与会话私有的 JSONL 证据（`worker-messages/<sessionId>/worker_messages.jsonl`，随会话删除）。finding 以 `[WORKER_FINDING]` 进入 lead 上下文并可唤醒空闲 lead；notice/progress 永不进入。bridge 惰性绑定 `tcp4 127.0.0.1:0`，不使用 stdio/SSE/public URL，以 `mcpBridge=unavailable|unreachable|disabled` 投影且不阻塞任务（bridge 注册或 listener 启动失败时同样降级），并在会话 shutdown 时撤销 token。token 的生命周期是 worker 的 remote session，而不是单个 turn：turn 结束后的 bridge 调用返回 `child_terminal` MCP tool error，下一次 `send` 会重新启用同一凭证；destroy/close、会话 shutdown 或进程退出时撤销。`delegate_worker_mgr` 的描述会发布由同一解析状态生成的简短能力摘要（report/ask、attachments、write、terminal、resume），并说明 bridge 不可用的原因。 worker 活动在 TUI `/router` 面板、ACP `mothx/worker/messages`、WebUI `GET /api/sessions/<id>/router` 与 Desktop Router composer 中只读投影；只有 lead 能回答问题。Profile 的 `driver`、`mcpBridge` 与 `capabilityOverrides` 可在 TUI 面板、WebUI 的 Router Worker 设置（`PATCH /api/acp/profiles`）与 Desktop 的 Router Workers 设置（`mothx/manage/acp/profiles/patch`）中编辑。`mothx doctor` 现在还会验证 listener 在最后一个 token 撤销后关闭。详见 `docs/zh/tools.md` 与 `docs/zh/configuration.md`。

- **新增 Gitee/Moark 模型：`hy4`**
  - 在 `gitee` 与 `moark` 供应商中新增 `hy4`：推理模型，1M 上下文，仅文本输入，使用 `deepseek-v4.1` 推理强度档位，不发送默认 max_tokens。

- **小米 MiMo Token Plan 新增模型：`mimo-v2.6-pro-ultraspeed`**
  - 三个 MiMo Token Plan 渠道（`xiaomi-token-plan-ams`、`xiaomi-token-plan-cn`、`xiaomi-token-plan-sgp`）均新增 `mimo-v2.6-pro-ultraspeed`，作为 `mimo-v2.6-pro` 的极速版本，保持 1M tokens 上下文窗口、128K tokens 最大输出、思考（reasoning）与多模态（`text,image`）输入。

- **火山引擎新增模型：`deepseek-v4.1-flash-economy`**
  - `volcengine`、`volcengine-agentplan`、`volcengine-codingplan` 三个火山引擎渠道均新增 `deepseek-v4.1-flash-economy`，支持思考、1M 上下文窗口与文本/图片输入；标准版 MaxTokens 为 262K，两个套餐版为 100K。

- **火山引擎新增模型：`glm-5.4`**
  - `volcengine`、`volcengine-agentplan`、`volcengine-codingplan` 三个火山引擎渠道均新增 `glm-5.4`，支持思考、1M 上下文窗口与文本/图片输入；默认不发送 max_tokens，使用供应商默认值。

- **Gitee/Moark 新增模型：`glm-5.4`**
  - `gitee` 与 `moark` 供应商均新增 `glm-5.4`：推理模型，1M 上下文窗口，支持文本与图片输入，最大输出 131,072 tokens。

- **腾讯云套餐现在只提供 `hy4` 与 `hy4-flash`。**
  - `tencent-hy-plan` 与 `tencent-hy-plan-anthropic` 预设现在列出 `hy4` 与 `hy4-flash`，不再提供 `hy3`。`hy4` 仅支持文本，`hy4-flash` 支持文本与图片；两者均支持推理，上下文为 1M，并保留 65,536 输出上限。

- **阿里云百炼 Token Plan 预设改用千问 AI 端点并刷新模型目录。**
  - `alibaba-token-plan` 现在使用 `https://token-plan.maas.qianwenaiapi.com/compatible-mode/v1`，提供 `auto`、`qwen4-max`、`qwen3.8-max`、`qwen3.8-flash`、`deepseek-v4.1-flash-1017`、`deepseek-v4.1-flash` 与 `glm-5.3`，替换原先的 Qwen/Kimi/DeepSeek/GLM/MiniMax 列表。Qwen 与 DeepSeek 条目支持文本与图片输入、1M 上下文；`qwen4-max` 使用原生 Qwen 思考开关；`glm-5.3` 保留 131,072 输出上限。内置 `bailian` 厂商适配器也会自动识别新域名。

- **DeepSeek 会话可以使用原生会话标题。** 当解析后的供应商指向 `api.deepseek.com` 时，尚无标题的会话在第一次模型请求中会启用 DeepSeek 的内联 `title` 扩展，覆盖 Chat Completions、Responses 与 Anthropic Messages 三种协议。Agent Core 捕获响应中的标题；若拒答或出错导致没有标题，则回退到首条用户消息。共享 Runtime 只会将它持久化为一次 `auto` 会话标题。Serve 会发布 `title_updated`；TUI、CLI、WebUI/API、ACP/Desktop、渠道与公共 SDK 都复用同一条 Runtime 路径，因此没有任何适配器新增第二套标题请求或生成器。对于不支持该扩展的供应商，Serve 仍使用原有的独立标题请求。

- **MothX Native 补齐剩余 Desktop parity 缺口并修复背景图层顺序。** Native 现在把 canonical `subagent` 生命周期事件投影为内联子代理卡、按会话的实时名册（上限 200、保留运行中子代理）与头部运行中/总数计数和失败红点，并在 terminal 事件与会话加载后重读权威 `mothx/subagent/list`；后台团队继续写入各自缓存会话。计划携带 Runtime 的 `mothx.dev` 标题/备注进入侧栏、卡片与导出；折叠的流式思考显示紧凑 live 尾部预览；工具文本/diff 入库按 Desktop 口径截断（20k/40k 字符）。外观新增全应用/仅首页背景作用域与可读的主题 veil、启动 quick-start 私有草稿目录与 Composer 工作区菜单中的最近目录历史、用户所选运行时二进制启动/初始化失败时自动回滚。在繁忙照片感背景上的验收渲染发现并修复了 paint-order bug（MyGo 默认把 absolute 子节点画在 flow 兄弟之上，背景层曾遮住整个外壳）；负 ZIndex 分层加像素探针与无头回归现在固定两种主题、两种作用域下图片/veil/外壳的顺序。
- **MothX Native 对齐 Desktop 的新任务目录契约并补全 elicitation form。** 新任务草稿不再继承上一个目录：保持未设置直到原生选择器（或 worktree 动作）提供目录；创建时询问一次、发送时若仍未设置再问一次，取消则保留草稿而不是发往未知 cwd；启动 quick-start 私有目录仍是唯一免询问默认。草稿没有目录期间，供应商/模型菜单从 `mothx/manage/providers/list` 投影（已配置供应商优先、保持目录顺序、草稿 > 现有 > 默认偏好），因为 `draft-config-options` 需要 cwd。多 property 的 `elicitation/create` schema 现在按 property 渲染输入/枚举字段，并以 property 名 keyed content 应答；单 property 与 replay 的对象 enum schema 继续可用，空 form 走 decline。

### ♻️ Runtime 与架构

- **ACP worker 能力改为类型化解析模型。** `internal/agentruntime/workerdriver` 拥有 `WorkerCapabilities`、显式 `generic-acp` adapter 与 profile ∩ advertised ∩ policy 解析器；业务代码读取能力而不是探测原始 `agentCapabilities` map。Profile 新增可选 `driver`、`mcpBridge` 与只能收紧的 `capabilityOverrides`。解析结果、降级原因与 bridge 状态写入 `session_child_runs`（migration 58）并投影到 child metadata。`mothx doctor` 检查 loopback `tcp4` 可绑定性与 profile driver/bridge/override 合法性；`internal/architecture` 守护 workerdriver 叶子边界并禁止业务代码按 driver 身份分支。2026-11-10 的真实 interop 使用 OpenCode 2.0.16 与 codex-acp 0.16.0 验证了 loopback bridge（OpenCode 写入了真实 `worker_mgr` finding，codex-acp 完成了 MCP handshake）；两者均可由 `generic-acp` 描述，因此没有新增未经验证的 flavor。

### 🐛 问题修复

- **聚合商提供的 MiMo 模型会随 `tool_calls` 回传 `reasoning_content`。** MiMo 要求携带 `tool_calls` 的 assistant 消息在下一轮请求中原样回传 `reasoning_content`。此前仅根据 `xiaomimimo` base URL 判定，因此 B.AI、Gitee/Moark、OpenRouter 等聚合商提供的 `mimo-*` 模型在会话没有记录到 reasoning 时可能缺少该字段。现在也会按模型 ID 识别 MiMo（包括网关目录中未列出的模型），并在没有记录到 reasoning 的回放 assistant 消息上补空的 `reasoning_content` 占位。

- **思考开关与 reasoning effort 现在严格按各厂商的文档契约发送。** 显式关闭思考时，DeepSeek Chat Completions/Anthropic Messages 现在发送 `thinking: {type: "disabled"}`，Qwen 混合思考模型发送 `enable_thinking: false`，不再静默保持思考开启；DeepSeek V4.1 在 Responses API 上使用 `reasoning_effort: "none"`；GLM 的 effort 映射为 `low`/`high`/`max`，不会再发送 `medium`。DeepSeek 的 effort 遵循官方映射（`minimal`/`low` → `low`，`medium`/`high`/`xhigh` → `high`，`max` → `max`），聚合商提供的 Qwen3.5+ 与 GLM 模型 ID 会被识别用于 thinking 格式解析，配置的模型列表也不再按相同下标继承内置预设的 `compat`。

- **MothX Native 的运行与传输可靠性修复。** 会话加载与会话列表响应现在带连接代际：用户已离开的会话即使加载较慢或失败，也不会清空当前会话或复用其缓存 transcript；在途刷新期间请求的会话列表刷新会重新排队而不是静默丢弃。ACP 客户端以原子指针保存子进程，普通请求默认 5 分钟超时（仅 `session/prompt` 保持无界），并通过持有的 `os.Process` 句柄延迟关闭子进程，避免 PID 复用误杀。重连后过期供应商/草稿配置响应会被忽略；SkillHub 图标抓取在 URL 校验与拨号时都拒绝 loopback/私网/link-local 目标。Native 各 feature 包补齐 guard 测试，并新增跨模块 wire 测试：真实执行管理 handler，校验 Native 期望的每个响应 key 仍与 Runtime 契约一致。

- **TUI 排队消息现在只有一个调度者和稳定的会话绑定。** TUI 不再在 Runtime 的 `SubmissionCoordinator` 之外维护第二套队列/调度：每条交互输入都会在 run 启动前同步注册；首会话创建期间暂存的输入会在会话激活后统一送入 coordinator；运行中的 slash command 会被拒绝，而不是入队后当作普通 prompt 发给模型；暂存附件也不会被后续消息抢走。会话切换会退役旧 coordinator 并清空投影，排队消息不会再跑到另一个会话里。
