# MothX 桌面客户端

MothX Desktop 是 MothX 的图形客户端，也是一个**纯 ACP（Agent Client
Protocol）客户端**：Electron 主进程启动内置的 `mothx acp` 运行时，通过 stdio
上的 newline-delimited JSON-RPC 通信。它不会启动 `mothx serve`，不会嵌入
Svelte Web UI，也不提供 HTTP 或 token 隧道。

## 它是什么

- **唯一的 ACP client。** `desktop/main/acp-client.ts` 负责请求关联、通知、
  反向审批/提问请求、重启/退避与生命周期。渲染进程只通过 preload IPC 桥访问
  `window.mothx`。
- **独立前端。** 渲染层位于 `desktop/renderer/`，使用 React 19 + shadcn/ui +
  Tailwind CSS，与 `ui/` 中的 Svelte Web UI 完全分离。
- **轻量投影。** 会话、运行、消息、工具、审批、提问、附件、制品、主角团、
  知识库、worktree、provider、MCP 配置与调度都是权威的 ACP/Runtime 状态；
  Desktop 只通过 ACP 方法渲染和管理这些状态。
- **只保存展示状态。** 主题、语言与新会话目录历史保存在
  `desktop-store.json`。运行时二进制选择（内置或用户指定路径）属于 Electron
  主进程的客户端启动状态，不是工作区或安全边界。

## 功能

- 流式文本、推理、工具卡片、diff、计划卡片以及 usage/缓存投影。
- 基于共享 Runtime 决策模型的审批与提问对话框。
- 会话列表、历史分页、分叉、恢复、重命名/元数据、项目分组、置顶与删除。
- 附件与生成制品，包括制品卡片与预览。
- 主角团：为会话绑定主角或团队；替换已绑定主角会创建会话分叉。
- 知识库：创建、扫描、查看运行记录/来源、查询、调度与清除索引。
- 与 CLI 和 Runtime 共享的隔离 git worktree。
- provider/model、模式、思考级别、沙箱、浏览器与 Web 搜索设置。
- 技能与在线 Skill Hub、MCP 服务器、记忆、定时任务与用量统计。
- 子 Agent 状态与只读子 Agent 执行记录面板。

可选控件由 `initialize._meta["mothx.dev"].features` 中声明的能力键 gate。
如果缺少某个键，对应面板或操作应不可用，而不是被静默模拟。

## 构建与运行

在仓库根目录：

```bash
make desktop-vendor    # npm ci + 构建并 vendor 当前平台运行时
make desktop-build     # 将 main/preload 与 renderer 打包到 desktop/dist
make desktop-dev       # renderer watch + Electron + DevTools/CDP
```

在 `desktop/` 目录：

```bash
npm run build          # 构建 main/preload/renderer
npm run typecheck      # TypeScript 边界检查
npm test               # main/preload/renderer 与协议测试
npm run e2e            # 可选的 Electron ACP 冒烟测试
npm start              # version:set + ensure:electron + build + 启动
```

`make desktop-dev` 会先准备运行时，并使用 `desktop/.dev-user-data/`，避免开发实例
与已安装应用争夺单实例锁。Linux 需要可用的显示服务器；请使用真实桌面会话、
`ssh -X` 或 Xvfb。没有 `DISPLAY` 时 Electron 会在 Chromium 启动阶段退出。

开发包：

```bash
make desktop-dist-dev-linux
make desktop-dist-dev-mac
make desktop-dist-dev-win
```

打包应用会内置源码构建的 `mothx` 运行时。Electron 主进程按以下顺序解析：
`MOTHX_BINARY`（开发覆盖）→ Desktop 设置中选择的自定义二进制 → 内置 vendor
目录 → 仓库 `bin/` 目录。自定义二进制不可用时回退到内置运行时。

## 运行时与协议

Desktop 使用 [ACP 协议](acp.md) 中记录的 ACP v1 接口以及 `mothx.dev` 扩展。
主进程启动一个共享 ACP 子进程，并通过它路由多个会话。它不会直接读取
`settings.json`、sessions 数据库或 Serve 配置；管理操作使用 `mothx/manage/*`
并按能力键 gate。

常见方法分组：

- 生命周期：`initialize`、`mothx/doctor`
- 会话：`session/new`、`session/load`、`session/resume`、`session/fork`、
  `session/list`、`session/close`、`session/delete`
- 运行：`session/prompt`、`session/cancel`、`$/cancel_request`
- 配置：`session/set_config_option`、`session/set_mode`
- 通知：`session/update`、`_mothx/session_event`
- 反向请求：`session/request_permission`、`mothx/requestQuestion`
- 附件/制品：`mothx/attachment/list|fetch`
- 管理：`mothx/manage/*`

## 故障排查

- **Linux 白屏或崩溃**：确认 `DISPLAY` 指向可用的 X 服务器，或在 Xvfb 下运行。
- **自定义运行时无法启动**：确认路径可执行，并且 `mothx --version` 输出兼容版本；
  Desktop 会回退到内置运行时。
- **可选面板缺失**：运行时没有声明对应能力键。请同时更新内置运行时与 Desktop
  版本。
- **审批或提问一直不结束**：检查 ACP 决策超时（`--permission-timeout`、
  `--question-timeout`，或 `MOTHX_ACP_PERMISSION_TIMEOUT` /
  `MOTHX_ACP_QUESTION_TIMEOUT`）。Runtime 会把无法恢复的 pending decision
  终止化。
