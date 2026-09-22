# MothX 桌面客户端化打包方案调研

> Date: 2026-07-26
> Status: 路线已确认（2026-07-26）
> 参考: `/home/free/src/oschina/mothxwork`（OpenWork fork，代号 Moark）

> **决议（2026-07-26 最终）**：自研 Electron 壳 + `mothx serve` 单通道，窗口直接加载内嵌 Web UI（零前端改动）。**桌面版放弃 ACP**——ACP 仅作为第三方客户端的兼容协议保留。mothxwork 仅作为打包方案参考（vendored 运行时机制、electron-builder 出包配置、自动更新），不共建、不 fork。

## 背景

MothX 目前的分发形态都是 CLI：

- 单 Go binary（`zip` / `tar.gz` / `deb`）
- npm `mothx-installer`（按平台 optionalDependencies 分发预编译二进制）
- PyPI installer
- 自解压脚本安装器（见 `non-developer-desktop-installer-proposal.md`）

UI 入口有三个：TUI、`mothx serve` 内嵌的 Svelte Web UI、`mothx acp`（Agent Client Protocol，stdio JSON-RPC）。

本文调研的目标：**把 MothX 包装成普通用户可以双击安装、双击打开的桌面客户端**（macOS `.app` / dmg、Windows 安装器、Linux AppImage），并参考 mothxwork 仓库已有的做法给出推荐路线。

## mothxwork 调研结果

mothxwork 是 OpenWork（Qwen Code desktop）的 fork，已经完成了"Electron 桌面壳 + vendored MothX 二进制 + ACP 通信"的大部分打通工作。其架构和打包方式如下。

### 仓库结构

```text
mothxwork/
  apps/
    electron/     # Electron 壳（main / preload / renderer），React 渲染层
    cli/          # headless CLI 入口
    webui/        # 浏览器版 UI
    viewer/
  packages/
    shared/       # agent 抽象层，含 ACP 进程管理
    server/       # headless server
    core/ session-tools-core/ messaging-*/ ui/ ...
  scripts/
    vendor-qwen-code.ts        # 下载并 vendor mothx 二进制
    electron-build-*.ts        # main/preload/renderer/resources 构建
    electron-builder-config.ts # 生成 electron-builder.yml
```

### 关键机制 1：vendored MothX 二进制

`scripts/vendor-qwen-code.ts` 直接从 npm 安装 `mothx-installer`（即本仓库发布的包）到 `apps/electron/vendor/qwen-code/`：

- 通过 `npm install --include=optional --install-strategy=nested` 拉取 `mothx-installer`，利用其平台 optionalDependencies 自动得到当前平台的 `bin/mothx`（Windows 为 `mothx.exe`）。
- 支持三种来源：`QWEN_CODE_TARBALL`（本地 tgz）、`QWEN_CODE_VERSION`（npm 版本/dist-tag）、`QWEN_CODE_ROOT`（本地源码构建）。
- 默认版本固定在根 `package.json` 的 `qwenCodeRuntime.version`（当前为 `1.1.79`，与本仓库版本同步）。
- vendor 后会执行 `mothx --version` 做可运行验证。

**结论：本仓库的 npm 分发包天然就是桌面客户端的运行时载体，无需为此新增产物。**

### 关键机制 2：ACP 作为桌面↔代理协议

`packages/shared/src/agent/qwen-agent.ts`：

- 以 `mothx acp`（`args = ['acp']`）启动子进程，stdio 上跑 JSON-RPC。
- 采用**共享进程 + 租约（lease）**模式：一个 ACP 进程托管多个 session（`registerSession` / `unregisterSession`），不是每个会话起一个进程。
- `packages/shared/src/agent/backend/internal/runtime-resolver.ts` 按顺序解析二进制位置：vendored 目录 → node_modules → 系统 PATH 的 `mothx`。
- 模型列表通过 ACP 通道获取（`fetchQwenModelsViaSharedAcp`）。

本仓库 `internal/acp/acp.go` 已实现的服务端能力：

- `initialize`（声明 `loadSession`、`sessionEvent` 等能力）
- `session/new` / `session/load` / `session/resume` / `session/prompt` / `session/cancel` / `session/close` / `session/delete` / `session/list`
- `session/update` 通知（文本流、工具调用、plan、usage）
- 客户端→服务端反向请求：权限确认（`session/request_permission`）、MCP 回调

mothxwork 根目录的 `acp-research*.mjs` 脚本说明对方仍在摸索 ACP 对接，主要未知点是消息发送/通知的确切形态。

### 关键机制 3：electron-builder 出包

`apps/electron/electron-builder.yml`（由 `scripts/electron-builder-config.ts` 生成）：

| 平台 |  target | 说明 |
| --- | --- | --- |
| macOS | dmg + zip（arm64/x64） | hardenedRuntime、entitlements、可选签名/notarize、`afterPack` hook 处理 macOS 26 图标 |
| Windows | nsis（x64） | per-user 安装、卸载删 appData；vendored 二进制走 `extraResources` 规避 electron-builder EBUSY bug |
| Linux | AppImage（x64） | |

其他值得借鉴的点：

- `asar: false`，避免解压开销和启动延迟。
- 按平台排除其他架构的 vendored 二进制（`files` 里的 `!**/vendor/.../<other-platform>` 规则）。
- electron-updater 自动更新（`main/auto-update.ts`），Windows 未签名渠道显式 `verifyUpdateCodeSignature: false`。
- 版本管理脚本（`bump-desktop-version.ts`、`check-release-version.ts`）保证桌面版本与运行时版本（`qwenCodeRuntime.version`）一致性。

### 对本仓库的依赖事实

- mothxwork 已经把 MothX 当作"运行时 artifact"：桌面源码不包含 agent 逻辑，只 vendor 二进制。
- 桌面 UI 是 mothxwork 自己的 React 工作台（多会话、文件预览、自动化、权限模式），**不是**本仓库的 Svelte Web UI。
- mothxwork 设定"桌面不存第三方 API key"，这与 MothX 多 provider + `settings.json` 存 key 的模式存在产品差异。

## 可选方案

### 方案 A：共建 mothxwork 作为官方桌面客户端（ACP 路线）

直接把 mothxwork 作为 MothX 的桌面客户端来共建/对齐，本仓库继续只做运行时。

架构：

```text
Electron shell (mothxwork)
  ├─ React 工作台 UI（多会话、权限、文件预览）
  ├─ packages/shared agent 层
  │     └─ spawn vendor/qwen-code/.../bin/mothx acp   ← 共享进程 + session 租约
  └─ electron-updater 自动更新
```

优点：

- 桌面 UI、多会话管理、权限交互、自动更新、三平台出包流水线**已经存在**，工作量最小。
- vendoring 已经指向 `mothx-installer`，版本同步机制（`qwenCodeRuntime.version`）已就位。
- ACP 是无状态 stdio 协议，天然隔离：桌面崩溃可以重连/重启代理进程；CLI 升级独立于桌面升级。

缺点/成本：

- UI 是另一套 React 代码库，与本仓库 Svelte Web UI 双轨并存，功能 parity 需要长期维护（skills、cron、ESM、stats 等 serve Web UI 已有的页面）。
- mothxwork 是体量很大的 fork（含 WhatsApp worker、MCP bridge 等我们不需要的部分），需要裁剪或接受冗余。
- ACP 能力有缺口（见下文"ACP 差距清单"），需要本仓库补齐协议。
- 签名 macOS dmg 需要 macOS runner + 证书；未签名可以 Linux 出 zip。

### 方案 B：自研轻量壳 + 复用 `mothx serve` Web UI

桌面壳只负责：启动 `mothx serve --addr 127.0.0.1:<随机端口>` 子进程、开一个指向它的窗口、托盘、自动更新。UI 完全复用 `ui/` 的 Svelte Web UI。

```text
壳（Electron 或 Tauri）
  ├─ spawn mothx serve（sidecar，内嵌 Web UI + token 鉴权）
  └─ BrowserWindow / WebView 加载 http://127.0.0.1:<port>?token=...
```

优点：

- **UI 零新增代码**：chat、sessions、stats、cron、skills、settings 全部现成，且后续只维护一套 UI。
- serve 已有 Bearer token 鉴权，壳可以用随机 token + loopback 保证本机安全。
- 协议是 HTTP/WebSocket，调试简单，channel 架构已支持进度事件推送。

缺点：

- 壳要自己写（窗口管理、进程生命周期、端口冲突、更新器），Electron 方向约等于重写 mothxwork 的壳部分但 UI 不同。
- Tauri 方向体积小（~10 MB 级安装包），但：macOS 出包必须 macOS runner（签名/公证链），Linux 依赖系统 webkitgtk（发行版碎片化），Rust 工具链进入构建。
- Electron 方向安装包 80–200 MB，但出包配置可以直接抄 mothxwork 的 electron-builder.yml。
- serve 是会话单例语义，桌面多窗口/多会话并发的体验不如 ACP 多租约模式（需要验证 serve 对多 session 并发的支持程度）。

### 方案 C：不做壳，桌面快捷方式直达 serve（现状增强）

延续 `non-developer-desktop-installer-proposal.md`：安装器创建 `MothX Serve` 快捷方式，双击启动 `mothx serve` 并自动打开浏览器。

优点：几乎零成本，两个 proposal 可以合并交付。
缺点：不是"应用"体验——没有独立窗口、没有 Dock/任务栏图标、依赖默认浏览器、无自动更新。只能作为过渡。

### 对比汇总

| 维度 | A: mothxwork 共建 | B: 自研壳 + serve UI | C: 快捷方式 |
| --- | --- | --- | --- |
| 桌面 UI | React 工作台（现成） | Svelte Web UI（现成） | 浏览器 |
| 新增 UI 维护 | 双轨（React + Svelte） | 单轨（Svelte） | 单轨 |
| 通信协议 | ACP stdio | HTTP/WS loopback | HTTP/WS loopback |
| 壳/出包工作 | 基本现成 | Electron 可抄配置；Tauri 平台受限 | 无 |
| 安装包体积 | 大（Electron） | Electron 大 / Tauri 小 | 最小 |
| 多会话桌面体验 | 原生支持 | 取决于 serve 并发能力 | 取决于 serve |
| 对本仓库改动 | 补 ACP 能力 | 少量（serve 启动参数、桌面模式） | 仅安装器 |
| 产品契合 | 需解决 key 存储/品牌分叉 | 与 CLI/TUI/serve 完全一致 | 一致 |

## ACP 差距清单（方案 A 需要本仓库补齐）

> 2026-07-26 更新：曾考虑把 ACP 与 serve API 抽象到共享后端（`docs/proposal/acp-serve-shared-backend-proposal.md`），评估后放弃——抽象层会引入更多复杂性。ACP 保持独立实现，本节差距项如桌面客户端确实需要，再逐项单独补齐。

基于 `internal/acp/acp.go` 现状与 mothxwork 的用法对比：

1. **认证/Provider 配置**：ACP `AuthMethods` 目前为空，MothX 的 provider key 在 `settings.json`。桌面 onboarding 要么复用 `/auth` 流程落盘 settings，要么定义 ACP 扩展方法。这是最大的产品差异点（mothxwork 设定为不存第三方 key）。
2. **模型列表**：mothxwork 通过 ACP 拉模型列表（`fetchQwenModelsViaSharedAcp`）。需要确认我们的 `initialize` 响应或扩展方法能否提供 provider/model 清单及 thinking 能力标志。
3. **消息/通知形态对齐**：`acp-research*.mjs` 显示对方对 `session/prompt` 的 content block 格式和 `session/update` 通知序列仍在摸索，需要出一份我们的 ACP 协议文档（方法、参数、通知、错误码），避免双方靠逆向对接。
4. **Windows 细节**：stdio 子进程在 Windows 上的编码/换行、bwrap sandbox 不可用时的降级策略、`mothx.exe` 路径解析，需要端到端验证。
5. **能力协商**：`initialize` 已声明 `loadSession` / `sessionEvent`，建议补充 `session/list`、`session/delete` 等非标准方法的能力标志，便于桌面做特性检测。

## 出包平台策略（2026-07-26 已确认：三平台全量支持）

桌面客户端**必须完整支持 macOS / Linux / Windows**，因此放弃 CLI 安装器"Linux-only 出包"的约束，接受多平台 CI matrix：

| 平台 | runner | 产物 | 签名 |
| --- | --- | --- | --- |
| Windows x64 | `windows-latest` | **portable 单文件 exe**（双击即用，免安装） | 有证书用 signtool；无证书过渡期出未签名包 + checksums |
| macOS arm64 / x64 | `macos-latest` | dmg + zip | 签名 + notarization（hardenedRuntime + entitlements，配置参考 mothxwork）；证书机密（`CSC_LINK` / `APPLE_ID` / `APPLE_APP_SPECIFIC_PASSWORD` / `APPLE_TEAM_ID`）存 GitHub Secrets |
| Linux x64 | `ubuntu-latest` | AppImage | 不适用（Linux 无签名生态） |

要点：

- vendored mothx 二进制按平台 vendor：每个 runner 上 `desktop-vendor` 从 npm `mothx-installer` 拉对应平台二进制，天然支持跨平台 matrix。
- mothxwork 已验证该 matrix 可行（同样的 electron-builder + 三平台 runner 模式）。
- CLI / npm / PyPI 的 release 仍在 Linux 单 runner 上，不受影响。
- `non-developer-desktop-installer-proposal.md` 的 Linux-only 约束仅适用于 CLI 自解压安装器，不适用于本桌面方案。

## 最终方案（2026-07-26 已确认）

决策记录：

- 自研 Electron 壳；mothxwork 仅作打包参考，不共建、不 fork。
- **桌面端彻底放弃 ACP**：ACP 仅作为第三方客户端（mothxwork / Zed 类编辑器）的兼容支持继续维护，不参与桌面版。桌面版**采用 serve 单通道**。
- 壳放**本仓库 `desktop/` 子目录**。
- **WebUI 整体复用，零改动**：Electron 窗口直接加载 `mothx serve` 提供的内嵌 Web UI（`ui/dist` 已嵌入 Go 二进制），桌面壳**不包含任何前端代码、不做 renderer 构建**。UI 更新随 mothx 运行时发布，与绑定版本策略一致。
- **单进程**：唯一子进程是 `mothx serve`（`--addr 127.0.0.1:<随机端口>` + 随机 token，仅 loopback）。
- **不干扰 CLI / npm / PyPI 打包**：`desktop/` 完全独立，现有 `Makefile`、`npm/`、`pypi/`、release workflow 零改动。
- **Windows 不走 nsis 安装器**：出 electron-builder `portable` 单文件 exe，双击即用、免安装、可放任意目录。
- 不做临时方案：首期即包含自动更新、版本锁定、三平台出包。

### 总体架构

```text
desktop/                        # Electron 壳（本仓库子目录，独立 package.json）
  main/                         # 主进程（TypeScript）
    serve-manager.ts            # spawn mothx serve（127.0.0.1:随机端口 + 随机 token），健康检查、崩溃重启
    runtime-resolver.ts         # vendored → node_modules → PATH 解析 mothx 二进制
    window.ts                   # BrowserWindow 加载 http://127.0.0.1:<port>/，单实例锁、窗口状态记忆
    auth-inject.ts              # session.webRequest 给所有指向 serve 端口的请求注入 Authorization 头
    tray.ts / menu.ts / updater.ts
  preload/                      # 最小 preload：窗口控制、更新提示、外链拦截（window.open → 系统浏览器）
  scripts/
    vendor-mothx.ts             # 从 npm mothx-installer vendor 当前平台二进制
    electron-builder-config.ts
  vendor/mothx/                 # vendored 运行时（gitignore）
  package.json                  # name: mothx-desktop；mothxRuntime.version 锁版本

唯一子进程：mothx serve --addr 127.0.0.1:<随机端口>（内嵌 Web UI + 全部 API）
Electron 窗口 = 指向该地址的 BrowserWindow，UI 即 serve Web UI，零前端改动
```

### 进程模型

- 启动：主进程解析 mothx 二进制 → 生成随机端口与随机 token → spawn `mothx serve --addr 127.0.0.1:<port> --token <token>` → 轮询 `/health` 就绪 → 创建窗口加载首页。
- 鉴权：token 只存在于主进程；`session.webRequest.onBeforeSendHeaders` 为发往 serve 端口的请求统一注入 `Authorization: Bearer <token>`。Web UI 代码零改动，token 不进渲染层、不进 localStorage。
- 安全：仅监听 `127.0.0.1`；随机端口 + 随机 token 使其他本地进程无法猜测；token 每次启动重新生成。
- 退出/恢复：子进程随壳退出；崩溃自动重启 serve 并 reload 窗口（session 持久化在磁盘，不丢数据）。
- **无端口洁癖的说明**：端口仅绑定 loopback、随机分配、带随机 token，且未来可无缝切换为 `--addr unix://`（Linux/macOS）/ named pipe（Windows）而不影响任何 UI 代码——渲染层始终只走标准 HTTP fetch，地址由主进程决定。

### 渲染层复用方式（2026-07-26 最终定稿）

**整体复用，零改动**：Electron 窗口直接加载 `mothx serve` 提供的 Web UI（`ui/dist` 经 `ui/embed.go` 嵌入二进制）。

- `ui/` 代码**一行不改**：无 transport 抽象、无 chat-backend、无 baseUrl 注入点、无构建期 flag。
- 桌面壳**不包含前端代码**：没有 renderer 目录、没有 Vite 构建、没有 alias。桌面版 UI 与 serve 版 UI 是同一份产物，功能与体验永远一致，新功能自动同步（随运行时版本发布）。
- 桌面专属行为全部在主进程/preload 实现：原生标题栏与菜单、托盘、单实例、外链在系统浏览器打开、更新提示条。
- 如需区分桌面环境（例如隐藏 Channels/Logs 等服务器向页面），用 preload 注入 `window.__MOTHX_DESKTOP__ = true`，UI 按此条件渲染——按需再加，首期不做。

### 与 CLI/npm 打包的隔离保证

- `desktop/` 自带 `package.json` 与 lockfile，不进入根 `Makefile` 的任何现有 target；只新增独立的 `desktop-vendor` / `desktop-build` / `desktop-dist` target（互不依赖 `dist`）。
- Go 构建、`ui/embed.go`、`npm/`、`pypi/`、`install.sh` 零改动。
- release workflow 新增独立 job（或独立 workflow `desktop-release.yml`），与现有 CLI release 互不阻塞；desktop 产物单独上传 GitHub Release，命名 `MothX-<ver>-<platform>.<ext>`。
- `desktop/vendor/` gitignore；CI 里 `desktop-vendor` 从 npm `mothx-installer` 拉与 release tag 一致的版本，也可 `MOTHX_TARBALL` / `MOTHX_LOCAL=1`（用 `bin/` 本地构建）覆盖。

### 出包与更新（首期即完整方案）

- electron-builder 三平台 matrix：
  - **Windows**：`portable` 单文件 exe（`windows-latest`）。双击即用，vendored mothx 运行时打在包内。注意两点并给出对策：
    - portable exe 每次启动会解包到 `%TEMP%`，冷启动略慢于安装版——可接受，换来零安装、零卸载残留。
    - electron-updater **不支持 portable 目标的自动替换**。Windows 更新策略：应用内检查新版本 → 后台下载新单文件 → 提示用户“重启完成更新”，退出时用新文件替换旧文件（download-and-replace-on-exit），体验等同自动更新但不依赖安装器。
  - **macOS**：dmg + zip（arm64/x64，签名 + notarization，`macos-latest`），electron-updater 原生自动更新。
  - **Linux**：AppImage（`ubuntu-latest`），electron-updater 原生自动更新。
- `asar: false`；按平台排除异构 vendored 二进制；Windows portable 单文件内嵌全部资源。
- **版本策略：壳与运行时绑定发布**。`desktop/package.json` 的 `mothxRuntime.version` 锁定 mothx 版本，桌面 release 时 bump + check 脚本保证一致。理由：ACP 扩展方法在演进，绑定发布消除协议版本错配；运行时独立升级通道在 ACP 协议稳定后再评估。

### ACP 的定位（2026-07-26 重新界定）

桌面版**不再依赖 ACP**。ACP 仅作为第三方客户端（mothxwork、Zed 类编辑器等）的兼容协议继续存在：

- `internal/acp` 维持现状，接受缺陷修复；`docs/proposal/acp-capability-gap.md` 中的补齐项（图片 prompt、`session/configure`、`models/list`、协议文档）降级为**互操作性增强**，按第三方需求排期，不再阻塞任何桌面工作。
- 桌面版需要的一切能力由 serve API 提供（本来就是 WebUI 的完整后端）。

### 最终实施计划（一次性交付，无渐进阶段）

目标态一次成型：不做 MVP、不做过渡形态。以下为全部工作项，按工作流分组，每一项可独立勾选验收。

#### A. 壳工程脚手架

- [ ] A1. `desktop/package.json`：`name: mothx-desktop`，声明 `mothxRuntime.version`（与 mothx release tag 一致），依赖 electron / electron-builder / electron-updater / esbuild，独立 lockfile
- [ ] A2. `desktop/tsconfig.json` + esbuild 主进程/preload 构建脚本（`desktop/build/*`）
- [ ] A3. `desktop/.gitignore`：`vendor/`、`release/`、`dist/`
- [ ] A4. 根 `Makefile` 新增隔离 target：`desktop-vendor`、`desktop-build`、`desktop-dist`（不依赖、不影响任何现有 target）

#### B. 运行时 vendoring 与版本

- [ ] B1. `desktop/scripts/vendor-mothx.ts`：从 npm `mothx-installer` 安装（`--include=optional --install-strategy=nested`）→解析嵌套的当前平台包（如 `mothx-installer-linux-x64`）→将真实 CLI 二进制规范化复制到 `desktop/vendor/mothx/bin/mothx`（Windows 为 `mothx.exe`）→校验 `mothx --version` 可运行。桌面客户端最终必须内嵌该 CLI，不能依赖用户全局安装。
- [ ] B2. 覆盖源支持：`MOTHX_TARBALL=<tgz>`、`MOTHX_LOCAL=1`（用本仓库 `bin/` 构建产物）、`MOTHX_VERSION=<ver|tag>`
- [ ] B3. `main/runtime-resolver.ts`：vendored 目录 → node_modules → 系统 PATH 的顺序解析，找不到时给出可操作错误
- [ ] B4. 版本一致性脚本：`bump-desktop-version.ts`（壳版本 + mothxRuntime.version 同步 bump）、`check-release-version.ts`（CI 校验 tag ↔ mothxRuntime.version 一致）

#### C. serve 进程管理

- [ ] C1. `main/serve-manager.ts`：随机端口 + 随机 token → 写入桌面专用临时 `serve.json`（loopback listen + bearer auth token + WebUI/API enabled）→ spawn `mothx serve --config <path>` → 轮询 `/health` 就绪（超时上限）→ 暴露 baseUrl；运行时使用打包内的 `vendor/mothx/bin/mothx[.exe]`，不依赖系统 PATH。
- [ ] C2. 崩溃守护：进程退出码监控，非预期退出自动重启（指数退避，上限 N 次）；重启后窗口自动 reload
- [ ] C3. 诊断：子进程 stderr 落盘到用户数据目录 `logs/serve.log`；启动失败/端口冲突时展示原生错误页（含日志路径与重试按钮）
- [ ] C4. 退出秩序：壳退出时优雅终止 serve（先 SIGTERM，超时 SIGKILL）；Windows 上处理控制台进程树

#### D. 窗口与安全

- [ ] D1. `main/window.ts`：BrowserWindow 加载 serve 首页；`contextIsolation: true`、`nodeIntegration: false`、`sandbox: true`；DevTools 仅开发模式可开
- [ ] D2. `main/auth-inject.ts`：`session.webRequest.onBeforeSendHeaders` 仅对 `127.0.0.1:<port>` 目标注入 `Authorization: Bearer <token>`；token 不进渲染层、不进 storage
- [ ] D3. 单实例锁：第二实例聚焦已有窗口
- [ ] D4. 窗口状态记忆（尺寸/位置/最大化）落用户数据目录
- [ ] D5. 原生应用菜单（含 reload/devtools 开发项）+ 系统托盘（显示/隐藏/退出）
- [ ] D6. 外链拦截：`window.open` 与新窗口请求一律 `shell.openExternal` 走系统浏览器
- [ ] D7. preload 最小化：仅暴露更新提示事件与窗口控制；预留 `window.__MOTHX_DESKTOP__` 标记（首期不消费）

#### E. 自动更新

- [ ] E1. macOS/Linux：electron-updater 接 GitHub Releases，启动时 + 定时检查，下载完成后原生 dialog 提示重启
- [ ] E2. Windows portable：应用内检查（读 GitHub Releases API）→ 后台下载新单文件到同目录 → 提示“重启完成更新” → 退出时换包并 relaunch（含失败回滚：保留旧文件直到新文件校验通过）
- [ ] E3. 全部更新渠道校验 SHA256（与 release checksums 比对）

#### F. 出包与 CI

- [ ] F1. `desktop/scripts/electron-builder-config.ts`：生成三平台配置——`asar: false`、明确只打包 `vendor/mothx/bin/mothx[.exe]` CLI、按平台排除异构 vendored 二进制、artifactName 统一 `MothX-<ver>-<platform>.<ext>`
- [ ] F2. Windows：`portable` 单文件 exe target（`windows-latest` 构建）
- [ ] F3. macOS：dmg + zip（arm64/x64），hardenedRuntime + entitlements + 签名 + notarization（`macos-latest` 构建，secrets：`CSC_LINK`/`APPLE_ID`/`APPLE_APP_SPECIFIC_PASSWORD`/`APPLE_TEAM_ID`）
- [ ] F4. Linux：AppImage（`ubuntu-latest` 构建）
- [ ] F5. `.github/workflows/desktop-release.yml`：三平台 matrix，各自执行 vendor → build → dist → checksums → 上传 GitHub Release；与 CLI release workflow 互不触发、互不阻塞
- [ ] F6. 图标与品牌资源：`desktop/resources/`（icns/ico/png，沿用 MothX 品牌）

#### G. 文档与验收

- [ ] G1. `desktop/README.md`：开发（`desktop-vendor` + `desktop-build` + 启动）、出包、更新机制、目录结构
- [ ] G2. 用户文档：下载页增加桌面版入口（Windows portable / macOS dmg / Linux AppImage）
- [ ] G3. 执行本文件"验收标准"全部条目并记录结果；特别验证安装包解压后包含并可执行 `vendor/mothx/bin/mothx[.exe]`，且启动不依赖系统 PATH 或全局 npm 安装。

#### 外部依赖（不阻塞开发，阻塞首个正式 release）

- [ ] X1. Apple Developer 证书申请（notarization 必需）
- [ ] X2. Windows 代码签名证书（可选；无证书期 portable 出未签名包 + checksums，更新渠道自首个版本固定为未签名）

### 验收标准

- `make build && make dist`、npm/PyPI 发布流程与现状完全一致（desktop 不介入）。
- 桌面端功能与 `mothx serve` 浏览器访问完全一致（同一 UI 产物）：聊天、审批、skills、cron、stats、settings 全部可用。
- serve 仅监听 `127.0.0.1` 随机端口（`ss -lnt` 验证无非 loopback 绑定）；token 不出现在渲染层（DevTools 中不可见）。
- 三平台产物（Windows portable 单文件 exe、macOS 签名 dmg、Linux AppImage）在各自 runner 的 CI matrix 产出；应用内更新可用（macOS/Linux 自动更新，Windows 换包式更新）。

### 从 mothxwork 借鉴的打包资产（已并入上述方案）

1. **运行时 vendoring**：npm 安装 `mothx-installer`（`--include=optional --install-strategy=nested`），平台 optionalDeps 自动带出正确二进制；版本锁在壳 `package.json` 的 `mothxRuntime.version`；vendor 后 `mothx --version` 验证。对应 `scripts/vendor-qwen-code.ts`。
2. **二进制解析顺序**：vendored 目录 → node_modules → 系统 PATH。对应 `runtime-resolver.ts`。
3. **electron-builder.yml 要点**：`asar: false`；按平台排除其他架构 vendored 二进制；mac hardenedRuntime + entitlements；artifactName 统一命名。
4. **自动更新**：electron-updater（macOS/Linux）；Windows portable 走应用内换包更新。
5. **版本一致性脚本**：壳版本与 `mothxRuntime.version` 的 bump/check 脚本（`bump-desktop-version.ts`、`check-release-version.ts`）。


## 待讨论问题

已决：

- ~~与 mothxwork 的关系~~ → 不共建、不 fork，仅参考其打包方案。
- ~~通信协议~~ → **serve 单通道**；ACP 彻底退出桌面版，仅作第三方客户端兼容支持。
- ~~共享后端抽象~~ → 否决。
- ~~壳位置~~ → 本仓库 `desktop/` 子目录。
- ~~UI 复用~~ → WebUI 整体复用零改动：窗口直接加载 serve 内嵌 UI，壳不含前端代码。
- ~~进程模型~~ → 单进程 `mothx serve`（loopback 随机端口 + 随机 token，主进程注入 Authorization 头）。
- ~~打包隔离~~ → `desktop/` 独立，CLI/npm/PyPI 流程零改动。
- ~~版本策略~~ → 壳与 vendored 运行时绑定发布（`mothxRuntime.version`）。
- ~~出包平台~~ → 三平台全量支持，CI matrix（windows/macos/ubuntu runner）；macOS 签名 + notarization。
- ~~Windows 安装器~~ → 不走 nsis，portable 单文件 exe 双击即用；更新走应用内下载换包。

待决：

无。onboarding 复用 WebUI settings 页（与零改动原则一致）；证书采购已列入 X1/X2。

## 参考

- mothxwork vendoring: `scripts/vendor-qwen-code.ts`
- mothxwork ACP 进程管理: `packages/shared/src/agent/qwen-agent.ts`（`args = ['acp']`，共享 lease）
- mothxwork 二进制解析: `packages/shared/src/agent/backend/internal/runtime-resolver.ts`
- mothxwork 出包: `apps/electron/electron-builder.yml`、`scripts/electron-builder-config.ts`
- 本仓库 ACP 实现: `internal/acp/acp.go`
- 本仓库 serve Web UI: `internal/serve/`、`ui/`
- 相关 proposal: `docs/proposal/non-developer-desktop-installer-proposal.md`
- ACP 能力差距（仅第三方互操作参考）: `docs/proposal/acp-capability-gap.md`
