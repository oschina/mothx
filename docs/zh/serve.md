# Serve 模式

`mothx serve` 是唯一的服务端入口，用来启动统一运行时：

- OpenAI 兼容 `/v1/chat/completions` API
- 针对配置了 `api: "openai-responses"` 和 `responses.background: true` 的 provider，提供可持久恢复的 OpenAI Responses 后台运行管理
- Web UI 管理面板（聊天界面、会话管理、设置编辑）
- 微信、飞书、WebSocket 消息通道
- Cron 定时任务、Memory 持久记忆、Hooks 钩子
- Stats 使用统计仪表盘
- 多 Agent 子代理支持

```bash
# 启动 Serve（默认 127.0.0.1:7872）
mothx serve

# 指定端口和工作目录
mothx serve --port 8080 --work-dir /path/to/project

# 绑定到外部网卡（允许其他机器访问）
mothx serve --port 0.0.0.0:8080 --work-dir /path/to/project

# 关闭认证并绑定到所有网卡（仅限可信网络）
mothx serve --unsafe --work-dir /path/to/project

# 初始化配置文件
mothx serve init-config global   # 生成 ~/.mothx/serve.json
mothx serve init-config project  # 生成 .mothx/serve.json
`--unsafe` 是显式的高风险模式：它会关闭认证，并让当前进程监听所有网络接口。请勿在不受信任的网络中使用；应优先使用 loopback 加 Bearer token 认证。

`/v1/chat/completions` 仅接受标准 OpenAI Chat Completions 字段，并返回标准 JSON/SSE 数据帧。不再支持 VibeCoding 专用的 `x_*` 请求和响应字段。WebUI 的会话选择、运行时能力、技能、审批和运行事件改由结构化的 `/api/...` 接口及 WebSocket 流处理。

## 配置

配置统一放在 `serve.json`：

- 全局：`~/.mothx/serve.json`
- 项目：`.mothx/serve.json`
- 自定义路径：`mothx serve --config /path/to/serve.json`

项目配置会覆盖全局配置。

### 核心配置项

```json
{
  "api": {
    "listen": "127.0.0.1:7872",
    "auth": {
      "enabled": true,
      "tokens": ["your-secret-token"]
    },
    "defaultWorkDir": "/path/to/project",
  },
  "features": {
    "multiAgent": true,
    "workflows": true
  },
  "artifact": false,
  "sandbox": {
    "enabled": false
  },
  "channels": {
    "artifact": false,
    "wechat": { "enabled": false },
    "feishu": { "enabled": false }
  },
  "webUI": {
    "enabled": true
  },
  "cron": {
    "enabled": true
  },
  "memory": {
    "path": ".mothx/memory.md"
  },
  "allowedWorkDirs": ["/path/to/project"],
  "agent": {
    "mode": "yolo"
  }
}
```

制品发布默认关闭。顶层 `artifact` 控制 WebUI/API 会话，`channels.artifact` 独立控制微信与飞书会话。

### 配置热重载

通过 Web UI 保存设置后，Serve 会自动热重载 provider/model 配置，无需重启服务。

## 网络访问

如果需要允许其他机器访问，把 Serve 绑定到外部网卡，例如 `--port 0.0.0.0:8080`，或在 `serve.json` 中设置 `"listen": "0.0.0.0:8080"`。对外暴露前应开启 Bearer token 认证。仅在可信本地网络中，可以使用 `--unsafe` 临时关闭认证，并把 loopback/default 监听地址绑定到 `0.0.0.0`。

当 `api.auth.enabled` 为 `true` 时，Web UI 会显示登录页。输入 `api.auth.tokens` 中任意一个 Token 作为密码即可登录。登录成功后浏览器会保存 HttpOnly、SameSite=Strict 的认证 Cookie；API 客户端仍可照常使用 `Authorization: Bearer <token>`。

## 安全

安全配置由以下三层独立控制：

1. **Bearer Token 认证**：`api.token` 或 `security.token`
2. **工作目录白名单**：`api.allowedWorkDirs`
3. **沙箱隔离**：`sandbox.enabled`（bwrap）

## Web UI

访问 `http://127.0.0.1:7872` 打开 Web UI。启用认证时，先使用 `api.auth.tokens` 中的 Token 登录。Web UI 提供：

- **聊天界面**：SSE 流式输出，工具调用/结果渲染，计划卡片，以及 `plan`、`agent`、`yolo` 模式的会话运行时菜单。提交的运行会保留持久化会话历史；支持在 run 中传入图片、会话工具开关、技能和显式模式。运行时模式和能力修改会立即采用服务端返回的权威状态，会话列表刷新则作为尽力而为的后台同步；重连后输入区会正确反映服务端排队/运行状态。
- **动态工作台**：`/chat` 支持会话标签、最多 4 个窗格分屏、菜单/拖拽分屏、布局记忆与移动端单窗格投影；每个 pane 拥有独立 composer、模型选择、草稿、审批和轨迹视图。各 pane 直接渲染 canonical `sessionRunStates` 投影，标签溢出时可通过“全部标签”菜单访问所有会话；使用 `?workbench=0` 或设置中的“动态工作台布局”开关可回到单会话布局；事件流始终由同一个 `session-stream` 调度器消费。
- **交互终端**：当 `/api/status` 的 `features.terminal` 为 true 时，工作台工具条可为当前会话 cwd 打开交互式 PTY（xterm.js + WebSocket）。终端等价于 serve 进程用户权限的本地 shell，不受 sandbox 保护，并完整继承 serve 进程环境变量；它不创建 Run、不写 transcript、不占 Runtime lease。只有回环监听、或已启用 auth 且配置了 token 的非回环监听可用；未认证的非回环监听返回 `terminal_unsafe`。
- **审批中心**：查看待处理工具审批，一次性批准或拒绝，持久化命令/路径放行规则，并查看会话审批审计历史。
- **会话管理**：分页浏览，键盘快捷键，历史会话，运行时快照，能力开关及可安全重连恢复的审批状态。历史会话按最后使用时间倒序排列，最新回复的会话位于最上方；会话管理页面会显示最后回复时间。页面刷新后，活动 run 直到进入终态前仍会保持会话忙碌。`GET /api/sessions` 默认有界：未指定 `limit` 时返回前 100 个会话以及 `total`、`limit`、`offset`、`hasMore`；`scope=active` 与 `/api/sessions/active` 只投影内存中的会话池，不再加载全部历史。
- **Responses 后台运行**：当 `openai-responses` provider 启用 `responses.background` 时，Serve 会将支持的请求提交为远程后台任务，持久化 response lineage 与归档输出，轮询直到完成，在重启后恢复可恢复任务，并执行本地 function/custom tool 调用。经认证的 run API 支持 `GET /api/responses/runs/{localRunID}?session_id={sessionID}` 以及 `cancel`、`reconnect`、`abandon` 操作；详见[配置](configuration.md#responses-字段)。
- **设置编辑**：Provider/Model 配置、Defaults、Web 搜索、MCP、上下文文件、压缩、沙箱、重试、审批、Provider 配置。保存 provider、app 或 Serve 配置后，相关状态与工具可用性会立即刷新，无需重启服务。MCP 设置用于编辑全局 `mcp.json`；聊天工具栏可为当前会话编辑项目级 MCP 配置。
- **DeepSeek Web Search 回退**：启用后，共享 Runtime 会为没有原生托管搜索的 provider 注册本地 `web_search` 工具，后端可通过 `webSearch.backend` 选择 DeepSeek 官方 Search API 或腾讯元宝 SearchPro；DeepSeek 后端还会注册 `web_fetch`。OpenAI Responses 继续保持 provider 原生托管搜索；Anthropic Messages 在 `webSearch` 启用时使用其 provider 原生托管搜索。两者都优先于本地 `web_search` 函数。
- **通道管理**：微信 QR 登录、飞书配置
- **WebSocket 流**：`/ws/runs` 和 `/ws/logs` 是 Web UI 使用的 Serve 事件流，不是一个可单独配置的消息通道
- **设置**：除 provider 和 agent 设置外，还可管理 Memory、WorkDir、Logs、SkillHub、环境变量和 MCP
- **服务配置**：Features，API，Cron，Memory，Security，Agent，Hooks，Channels，Lobster 模式

### 终端配置

`serve.json` 可选的 `terminal` 段控制交互终端；缺省即自动策略（回环可用；非回环但已配置 auth token 可用）：

```json
{
  "terminal": {
    "enabled": null,
    "shell": "",
    "maxSessions": 8,
    "maxPerSession": 2,
    "scrollbackBytes": 1048576,
    "maxOutputBytesPerSec": 1048576,
    "allowedOrigins": []
  }
}
```

`allowedOrigins` 为空表示严格同源；反向代理或自定义前端需要显式列出 origin。终端状态栏会持续显示 shell 不受沙箱保护、继承 serve 进程用户与环境变量，提示它与可能正在运行的 Agent 共享焦点会话 cwd（canonical execution 快照为本地或另一进程执行时显示更强警告），展示 `/api/status` 上报的占位认证 token 警告，并显示服务端输出反压，而不是把它当作断线。页面刷新会重新 attach 该标签页记录的 detached shell，而不是泄漏旧终端再新建一个；shell 已退出或 id 失效后，“重新连接”会创建新终端。浏览器 cookie 会话不会在创建响应中收到 `terminalToken`；只有 auth 关闭或使用 Bearer 认证的客户端才会拿到 token，用于非浏览器 WebSocket attach。serve 只记录终端生命周期元数据（`terminal_created`/`terminal_exited`/`terminal_killed`，含 id、session、cwd 与 exit code），不记录输入或输出。关闭终端功能会终止已有 shell 并拒绝新的 WebSocket attach。Linux 上 shell 带有 `Pdeathsig`，serve 进程退出时会被终止；Windows 使用 ConPTY（要求 Windows 10 1809+ 或 Windows 11），更老系统返回 `terminal_unsupported`。终端共享 serve 的认证 token：持有 token 即等同拥有该用户的 shell 权限，这是访问边界而不是 UI 开关；需要更强隔离时应使用独立 OS 用户或容器，而不是继续裁剪终端环境变量或依赖沙箱。

### 界面截图

**聊天界面** — SSE 流式输出、工具调用/结果渲染、计划卡片与模式菜单：

![Web UI 聊天界面](assets/image/webui-chat.webp)

**会话管理** — 分页历史、运行时快照、能力开关：

![Web UI 会话管理](assets/image/webui-sessions.webp)

**设置编辑** — Provider/Model、Defaults、Web 搜索、压缩、沙箱、审批：

![Web UI 设置](assets/image/webui-settings.webp)

**技能** — 浏览并加载项目/全局技能：

![Web UI 技能](assets/image/webui-skill-zh.webp)

**定时任务** — Cron 调度管理：

![Web UI 定时任务](assets/image/webui-cron.webp)

该列表只展示用户创建的任务。同一个调度器还会运行一个 Runtime 拥有的维护任务（`mothx-maintenance:artifact-storage`，每日一次），用于回收已无引用的附件存储；它不会出现在该视图与名称查找中，并在调度器启动时自动创建。详见 [`mothx pure`](cli-reference.md#pure---归档会话数据库)。


## 消息通道

### 微信

- 支持 QR 码登录（扫码认证）
- 登录状态轮询，错误处理
- API 端点：`/api/channels/wechat/login`

### 飞书

- 支持 appId/appSecret/workspace/allowedUsers 配置
- 自动消息路由和会话持久化

### WebSocket 事件流

Web UI 使用经认证的 `/ws/runs` 和 `/ws/logs` 流获取 run 事件和日志；这些端点不是独立的消息通道配置。

## Stats 仪表盘

Serve Web UI 在 `/stats` 提供使用统计（例如 `http://127.0.0.1:7872/stats`）。独立的 `mothx stats` 命令是另一套服务，默认监听 `127.0.0.1:7878`。

![Web UI 统计仪表盘](assets/image/webui-stats-zh.webp)

## CLI 标志

| 标志 | 描述 | 默认值 |
|------|------|--------|
| `--port` | 监听地址（host:port） | `127.0.0.1:7872` |
| `--work-dir` | 工作目录 | 当前目录 |
| `--config` | 配置文件路径 | `~/.mothx/serve.json` 或 `.mothx/serve.json` |
| `--web-ui-dir` | Web UI 静态资源目录 | 内置路径 |
| `--unsafe` | 关闭认证并绑定到所有网卡 | 关闭 |
| `--debug` | 启用 pprof 性能分析服务器 | 关闭 |
