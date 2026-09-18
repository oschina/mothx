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
    "feishu": { "enabled": false },
    "webhooks": {
      "enabled": false,
      "secret": "生产环境请使用环境变量",
      "routes": []
    }
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
- **审批中心**：查看待处理工具审批，一次性批准或拒绝，持久化命令/路径放行规则，并查看会话审批审计历史。
- **会话管理**：分页浏览，键盘快捷键，历史会话，运行时快照，能力开关及可安全重连恢复的审批状态。历史会话按最后使用时间倒序排列，最新回复的会话位于最上方；会话管理页面会显示最后回复时间。页面刷新后，活动 run 直到进入终态前仍会保持会话忙碌。
- **Responses 后台运行**：当 `openai-responses` provider 启用 `responses.background` 时，Serve 会将支持的请求提交为远程后台任务，持久化 response lineage 与归档输出，轮询直到完成，在重启后恢复可恢复任务，并执行本地 function/custom tool 调用。经认证的 run API 支持 `GET /api/responses/runs/{localRunID}?session_id={sessionID}` 以及 `cancel`、`reconnect`、`abandon` 操作；详见[配置](configuration.md#responses-字段)。
- **设置编辑**：Provider/Model 配置、Defaults、Web 搜索、MCP、上下文文件、压缩、沙箱、重试、审批、Provider 配置。保存 provider、app 或 Serve 配置后，相关状态与工具可用性会立即刷新，无需重启服务。MCP 设置用于编辑全局 `mcp.json`；聊天工具栏可为当前会话编辑项目级 MCP 配置。
- **原生 Responses Web Search**：OpenAI Responses provider 会自动暴露上游 hosted `web_search` 能力；独立的 Web 搜索设置仅控制 MothX 本地搜索。
- **通道管理**：微信 QR 登录、飞书配置
- **WebSocket 流**：`/ws/runs` 和 `/ws/logs` 是 Web UI 使用的 Serve 事件流，不是一个可单独配置的消息通道
- **设置**：除 provider 和 agent 设置外，还可管理 Memory、WorkDir、Logs、SkillHub、环境变量和 MCP
- **服务配置**：Features，API，Cron，Memory，Security，Agent，Hooks，Channels，Lobster 模式

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

### Webhook

Serve 可以接收入站 webhook 事件，将其分派给 agent skill，然后把结果投递到已配置目标。配置 `channels.webhooks.enabled`、可选的 `secret`，以及包含 `path`、`events`、`skill`、`delivery` 和可选 `delivery_target` 的路由。将 webhook 对外暴露前，应保持 loopback 绑定或通过认证/secret 校验进行保护。

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
