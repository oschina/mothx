# MothX Desktop

MothX Desktop 是一个**纯 ACP（Agent Client Protocol）客户端**：

- Electron 主进程启动打包内的 `mothx acp` 子进程，通过 stdio 上的
  newline-delimited JSON-RPC（ACP v1 + `mothx.dev` 扩展）通信。
- 主进程是唯一的 ACP client（`main/acp-client.ts`）；渲染进程只通过
  preload 暴露的 `window.mothx` IPC 桥访问协议，不直接接触子进程。
- 前端是**独立的单页应用**（`desktop/renderer/`，React 19 + shadcn/ui +
  Tailwind CSS，Vite 打包为 file:// 兼容的经典脚本），与 serve 模式的 Web UI
  （`ui/`）完全分离；桌面版不再启动 `mothx serve`，没有 HTTP/token 通道。
- 界面布局参考 workbuddy 桌面原型：38px 标题栏 / 220px 侧边栏 / 22px 状态栏，
  IDE Light / IDE Night 双主题，任务状态机（规划中/执行中/等待输入/已完成/失败），
  计划卡片、工具卡片（含 diff）、审批/提问卡片、制品卡片。

## 架构

```
renderer (desktop/renderer)          main (desktop/main)                runtime
┌──────────────────────────┐   IPC   ┌──────────────────────┐  stdio  ┌──────────────┐
│ React + shadcn/ui +      │◄───────►│ index.ts  窗口/生命周期 │◄───────►│ mothx acp    │
│ Tailwind (Vite 构建)      │ context │ acp-client.ts JSON-RPC│ NDJSON  │ (vendored)   │
│ core/  状态与 ACP 动作    │ Bridge  │ ipc.ts    通道注册     │         │ ACP v1 +     │
│ views/ 视图与设置面板     │         │ store.ts  UI 本地状态  │         │ mothx.dev ext│
└──────────────────────────┘         └──────────────────────┘         └──────────────┘
```

渲染进程分层：`core/` 保存与 DOM 无关的中央状态、ACP 动作与 i18n 字典
（`useSyncExternalStore` 桥接 React）；`components/ui/` 是 shadcn/ui 基础组件；
`components/` 与 `views/` 是界面投影；`index.css` 定义设计 token（shadcn 语义
变量映射 MothX 调色板）与少量 CSS 系统（应用背景图、自动隐藏滚动条、拖拽区）。

协议使用（全部经 `acp-client.ts`）：

- 生命周期：`initialize`（`_meta.mothx.workspace` 协商工作区窗口）；新建对话先选工作目录（原生选择器）：目录在窗口内直接用作会话 cwd，否则经 `mothx/workspace/extend` 免重启纳入窗口，老运行时回退重启子进程
- 会话：`session/new` `session/load`（重放历史）`session/resume` `session/fork` `session/list` `session/close` `session/delete` `mothx/session/setTitle`
- 运行：`session/prompt`（text / resource_link / resource content block）`session/cancel` `$/cancel_request`
- 配置：`session/set_config_option`（provider/model/mode/thinking_level/sandbox/browser/web_search）`session/set_mode`
- 通知：`session/update`（agent_message_chunk、agent_thought_chunk、tool_call(_update) 含 diff、plan、usage_update、available_commands_update、config_option_update、session_info_update、artifact*）与 `_mothx/session_event`（terminal/status/retry/compaction）
- 反向请求：`session/request_permission`（审批）`mothx/requestQuestion`（提问）
- 诊断：`mothx/doctor`；制品获取：`mothx/attachment/fetch`*

`*` 标记的制品投影/获取是 ACP 能力缺口补齐项（P0-1），见
`docs/proposal/desktop-acp-frontend-gap-proposal.md`；运行时未实现时前端自动降级
（从 `publish_artifact` 工具调用投影制品卡片）。审批/提问超时通过环境变量
`MOTHX_ACP_PERMISSION_TIMEOUT` / `MOTHX_ACP_QUESTION_TIMEOUT` 注入（P0-3）。

主题/语言/工作区历史等 ACP 不拥有的 UI 状态保存在 userData 的
`desktop-store.json`（`main/store.ts`），绝不复制会话权威数据（置顶/分组等会话
元数据是 ACP `mothx/session/setMeta` 的 canonical 状态，不落本地）。运行时二进制
选择（内置/自定义）也属于客户端外壳的启动状态，同样只存在于该文件。

## 开发与构建

打包的应用默认内置平台原生 MothX CLI 二进制：`scripts/build-runtime.cjs` 为
`electron-builder.yml` 里的每个目标架构构建一份到 `vendor/mothx/bin/<goos>-<goarch>/`
（macOS arm64+x64、Windows/Linux x64），打包后由 `scripts/after-pack.cjs` 把与目标
架构匹配的那份注入到 `<resources>/app/vendor/mothx/bin/`（macOS 位于 `MothX.app`
内，随签名一起封存），并用 `scripts/binary-arch.cjs` 校验 ELF/Mach-O/PE 头部
架构，不匹配直接构建失败。`ui/dist` 只在缺失时构建一次（Go 二进制 embed 需要），
桌面前端与其无关。

从仓库根目录：

```bash
make desktop-vendor     # npm ci + version:set + 源码构建 vendor 运行时
make desktop-build      # esbuild 打包 main/preload + Vite 打包 renderer 到 desktop/dist
make desktop-dev        # 监听 renderer、自动刷新 Electron，并打开 DevTools / 本地 CDP
```

desktop 目录内：

```bash
npm run build           # esbuild（main.cjs / preload.cjs）+ Vite（renderer/*）
npm run dev             # renderer 热更新；DevTools 以独立外部窗口打开 + 127.0.0.1:9223 Chrome DevTools Protocol（先执行 make desktop-vendor）
npm run typecheck       # tsc --noEmit（main + preload + renderer + scripts）
npm test                # node --test + tsx --test（协议分帧/本地 store）
npm run start           # version:set + ensure:electron + build:runtime + build + electron .
```

运行时二进制的解析优先级（`main/runtime-binary.ts`）：开发覆盖
`MOTHX_BINARY=/path/to/mothx` > 桌面设置里的自定义二进制（运行时设置页可切换，
默认使用内置）> 内置 vendor 目录 > 仓库 `bin/`。自定义二进制不可用时回退到内置；
`npm run build:runtime` 默认构建全部打包架构，加 `--arch x64|arm64` 只构建一种。

`make desktop-dev` 会先准备 Desktop 运行时，再启动 `npm run dev`。直接在
`desktop/` 中执行 `npm run dev` 时，请先执行一次 `make desktop-vendor`。它以 Vite
watch 模式监听 `renderer/`：修改后会重建 `dist/renderer` 并让 Electron 无缓存刷新，
ACP 子进程无需重启。`main/` 与
`preload/` 只在启动时构建一次；修改后需要手动重启 Electron。开发模式自动打开
DevTools（以独立外部窗口打开，不嵌入主窗口），并将 Chrome DevTools Protocol 限制为 `127.0.0.1:9223`，可供本机自动化
工具连接、截图和界面审阅；可用 `MOTHX_DESKTOP_DEBUG_PORT=9333 make desktop-dev`
换用其他本地端口。开发实例使用 `desktop/.dev-user-data/`，不会与已安装 Desktop
争夺单实例锁或复用其本地展示状态；可用 `MOTHX_DESKTOP_USER_DATA=/tmp/mothx-dev`
覆盖该目录。它不启动 `mothx serve`，也不会向 renderer 增加 HTTP/API
通道。

退出方式：终端里按一次 Ctrl+C 即可结束。dev runner 会关闭 Vite 监听、终止
Electron 的整个进程树（主进程与 renderer/GPU 子进程），并在 4 秒宽限期后对
未退出的进程强制结束，因此不会残留后台 Electron，也不会留下挂住的
`npm run dev`。重复按 Ctrl+C 可以立即强制退出；关闭终端窗口（`SIGHUP`）
同样会触发完整的清理流程。

Desktop 是图形客户端，Linux 主机必须有可用显示服务器，否则 Chromium 在
平台初始化阶段直接退出（`Missing X server or $DISPLAY` /
`The platform failed to initialize` / SIGSEGV），现象是一个白色无边框窗口后
接一段段错误。此时 `npm run dev` 会在 1 秒内打印可执行的说明并以退出码 1
结束（完全没有 `DISPLAY` 时连构建都不会跑）；`DISPLAY` 存在但连不上
（`ssh` 未开 X11 转发、X 服务已退出等）时，Chromium 自己的报错会被原样保留
并附加同一段说明。可行方案：本机真实桌面会话、`ssh -X` 转发、Xvfb
（`Xvfb :99 & export DISPLAY=:99`，可再叠 VNC/x11vnc），或者在无显示器的
主机上改用 TUI（`mothx`）/ `mothx serve` + 本地浏览器 Web UI。

## 发布打包

Desktop `package.json` 保持占位版本；`npm run version:set` 与打包脚本从
`MOTHX_VERSION`（如设置）或当前 git tag 解析真实版本。

- `npm run dist:dev:mac` — 当前机器构建 macOS 开发包（`MothX-Desktop-macos-{arch}.dmg` + `.zip`）
- `npm run dist:dev:win` — 当前机器构建 Windows 开发包（`MothX-Desktop-windows-x64.exe` portable + `.zip`）
- `npm run dist:dev:linux` — 当前机器构建 Linux 开发包（`MothX-Desktop-linux-amd64.AppImage` + `.deb` + `.tar.gz`）
- `npm run dist:mac` / `dist:win` / `dist:linux` — 对应发布构建，允许配置 publish

等价的仓库根目录快捷命令：

```bash
make desktop-dist-dev-mac
make desktop-dist-dev-win
make desktop-dist-dev-linux
```

`dist:dev:*` 强制 `--publish never`，不会创建或上传 GitHub Release。跨平台构建
仍建议在对应 runner 上执行。macOS 构建为单架构（`--arch $(node -p process.arch)`）；
`after-pack.cjs` 会校验打包进应用的 CLI 二进制架构与应用架构一致。
