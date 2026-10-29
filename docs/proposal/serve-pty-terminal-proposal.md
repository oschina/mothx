# Serve PTY / Terminal 后端方案

> Date: 2026-10-28 · Last revised: 2026-10-29 · Status: Proposal · Owner: Serve (Go)
> Related: `AGENTS.md`, `docs/proposal/webui-dynamic-workbench-layout-proposal.md`
> Scope: serve-only，独立于 WebUI 工作台 v1；不随布局方案一起实现。

> 优先级：**易用性 > 安全/隐私摩擦**，但不越过 AGENTS.md 的硬边界。回环且已认证（或仅回环）的常见本地场景默认可用；同源、认证、密钥环境过滤、不把未认证 shell 暴露到公网这些是 bug 边界，不是可关闭的 UX 摩擦。PTY 不创建 Run、不写 session transcript、不占 ExecutionRuntime lease。


> 本文回答“tty 面板如果要真做，Go 后端应该放在哪、怎么接、边界是什么”。它是**独立能力**，不属于 WebUI 布局 v1；不要因为前端加了 tab 就把 PTY 顺手塞进 `internal/tools/bash.go` 或 `internal/serve/openaiapi` 的 session pool。

## 1. 结论与边界

- **PTY 是 serve-only 的后端能力**：只有 `mothx serve` 的 WebUI 使用；TUI/CLI/ACP 不受影响，也不共享终端会话。
- **它不是 Agent 的 `bash` 工具**：`bash` 是每次工具调用启动一个非交互子进程、捕获 stdout/stderr；PTY 是长生命周期、可输入、可 resize、可重连的交互式 shell。
- **它不是 session/run 的一部分**：terminal 不创建 Run、不写 session transcript、不占 ExecutionRuntime lease；它只绑定一个已存在的 session 的 cwd。
- **默认可用，但不裸奔**：回环监听（`127.0.0.1`/`localhost`）默认可用；公开监听必须有 auth，未认证的公开监听拒绝创建并返回 `terminal_unsafe`。用户显式 `terminal.enabled=false` 可关闭。
- **不承诺沙箱，但不隐藏事实**：PTY 是用户 shell 的直接入口。沙箱开启时仍可用（易用性优先），但 UI 必须持续显示“终端不受沙箱保护”，不要对外宣称“终端在 sandbox 内”。
- **不持久化**：terminal id、输出、进程状态只在内存；serve 重启/进程退出即终止。需要历史请用 transcript/trajectory，不要把终端输出写 DB。

## 2. 代码归属（Go 包与文件）

新增：

```text
internal/terminal/
├── terminal.go          # Terminal 结构、状态机、ring buffer、resize、Close
├── manager.go           # 注册表：byID / bySession、创建/查找/关闭、上限、shutdown
├── pty_unix.go          # //go:build !windows：creack/pty 或 unix.Openpty
├── pty_windows.go       # //go:build windows：ConPTY
├── pty_unsupported.go   # //go:build 其他：返回 ErrUnsupported
├── shell.go             # shell 解析、环境变量、TERM/COLORTERM
└── *_test.go

internal/serve/terminal.go   # HTTP/WS handler、auth/origin、session 查找、协议编解码
internal/serve/terminal_test.go
```

修改：

```text
internal/serve/config.go       # Config 增加 Terminal TerminalConfig；DefaultConfig/normalize/featureStatus
internal/serve/run.go          # channelRuntime 增加 terminals *terminal.Manager；startChannels 初始化；stop 关闭；routes 注册
internal/serve/run.go          # 删除 session 时 rt.terminals.CloseSession(sessionID)
internal/serve/openaiapi/auth.go # 复用 AuthMiddleware（不新增认证模型）
internal/config/settings.go    # 复用 GetShell()/ShellPath；不新增字段（终端只读它）
```

**不要放在：**

- `internal/tools/bash.go`：它是非交互工具执行器，生命周期、输出、沙箱语义不同；把 PTY 塞进去会让 Agent 工具和用户终端互相污染。
- `internal/serve/openaiapi/server.go`：`Server` 是 OpenAI API/session API 的 owner，不是本地终端 owner；terminal 由 `channelRuntime` 持有一个 `terminal.Manager` 即可。
- `internal/session`/`internal/dao`：终端不需要数据库；session 只用于解析 cwd。
- `internal/agentruntime`：terminal 不是 Run，不参与 lease/queue/decision。

## 3. 配置（serve.json）

终端是 serve-only 功能，配置放 `serve.json` 的 `terminal` 段，而不是 `settings.json`（后者跨所有适配器共享）。

```go
// internal/serve/config.go
type TerminalConfig struct {
    // nil = 默认策略：回环可用；公开监听要求 auth。显式 false 关闭。
    Enabled *bool `json:"enabled,omitempty"`

    // Shell 为空时使用 settings.GetShell()。
    Shell string `json:"shell,omitempty"`

    // 资源上限；0 表示默认值。
    MaxSessions    int `json:"maxSessions,omitempty"`    // 默认 8
    MaxPerSession  int `json:"maxPerSession,omitempty"`  // 默认 2
    ScrollbackBytes int `json:"scrollbackBytes,omitempty"` // 默认 262144
    IdleTimeoutSecs int `json:"idleTimeoutSecs,omitempty"` // 默认 600
    MaxOutputBytesPerSec int `json:"maxOutputBytesPerSec,omitempty"` // 默认 1048576

    // 兼容旧配置；易用性优先下沙箱开启不再默认拒绝，只影响 UI 警告强度。
    AllowUnsafe bool `json:"allowUnsafe,omitempty"`
}
```

`DefaultConfig()`：`Terminal.Enabled=nil`（按启动时的监听/auth 解析），其余给默认值。`normalize()`：

- `Enabled=nil` 且监听为回环 → 解析为 true；公开监听 + auth → true；公开监听 + 未启用 auth → false 并拒绝创建。
- `Enabled=false` → 保持 false（用户显式关闭）。
- `MaxSessions` 夹紧 `[1, 32]`；`MaxPerSession` 夹紧 `[1, 4]`；
- `ScrollbackBytes` 夹紧 `[16KiB, 4MiB]`；
- `IdleTimeoutSecs` 夹紧 `[60, 86400]`；
- `MaxOutputBytesPerSec` 夹紧 `[64KiB, 16MiB]`；
- `Shell` 只接受绝对路径或可解析的 basename；不合法则回退 `settings.GetShell()`；
- `Enabled=true` 但监听地址不是回环、且 auth 未启用时，启动日志警告并**拒绝创建**（见 6）。

`featureStatus` 增加 `terminal: bool`，由解析后的 `terminalEnabled`（`Enabled=nil`/true + 回环或 auth）与 `terminalSupported()` 决定；WebUI 只在为 true 时显示入口。`terminalSupported()` 由 build tags 提供。

## 4. 数据模型与状态机

```go
// internal/terminal/terminal.go
type State string
const (
    StateStarting State = "starting"
    StateRunning  State = "running"
    StateExited   State = "exited"
    StateKilled   State = "killed"
)

type Terminal struct {
    ID        string
    SessionID string
    Cwd       string
    Shell     string
    Cols      uint16
    Rows      uint16
    State     State

    cmd  *exec.Cmd
    ptmx *os.File
    ring *RingBuffer // 固定容量，按字节

    mu       sync.Mutex
    attached *client // 单控制端；重连可 takeover
    done     chan struct{}
}
```

`Manager`：

```go
type Manager struct {
    mu        sync.Mutex
    cfg       TerminalConfig
    shell     string
    byID      map[string]*Terminal
    bySession map[string]map[string]*Terminal
    log       func(format string, args ...any)
    closed    bool
}
```

- `Create(sessionID, cwd string, cols, rows uint16) (*Terminal, error)`：校验上限、shell、cwd、平台支持；启动 PTY；注册；启动 read loop。
- `Get(sessionID, terminalID)`：必须同时匹配；防止跨 session 访问。
- `Close(sessionID, terminalID)`：kill process group，标记 killed，通知 attached client，移出注册表。
- `CloseSession(sessionID)`：session 删除时调用。
- `Shutdown()`：serve 退出时关闭全部；`channelRuntime.stop()` 中调用。
- 状态：`starting -> running -> exited|killed`；`exited` 可从 PTY EOF/`cmd.Wait` 得到 exit code；`killed` 由显式关闭/超时/上限触发。
- 终端 id：`crypto/rand` 128-bit，base64url；不要用 session id + 序号（可预测）。

## 5. HTTP / WebSocket API

路由注册在 `channelRuntime.routes`（`internal/serve/run.go`），与 `/ws/runs`、`/ws/logs` 同级；所有 handler 经过现有 `AuthMiddleware`（`internal/serve/openaiapi/auth.go`）：

```text
POST   /api/terminals                 创建
GET    /api/terminals?sessionId=...   列出该 session 的终端（可选）
DELETE /api/terminals/{id}            关闭
GET    /ws/terminals/{id}             WebSocket attach
```

**创建：**

```http
POST /api/terminals
Content-Type: application/json

{"sessionId":"s-123","cols":120,"rows":32}
```

响应：

```json
{
  "terminalId": "t_...",
  "sessionId": "s-123",
  "cwd": "/path/to/session/workdir",
  "shell": "/bin/zsh",
  "cols": 120,
  "rows": 32,
  "state": "running"
}
```

错误码（JSON `{error, code}`）：

| HTTP | code | 场景 |
| --- | --- | --- |
| 403 | `terminal_disabled` | 配置未开启 |
| 403 | `terminal_unsafe` | 监听非回环且 auth 未启用 |
| 404 | `session_not_found` | `GetSessionWorkDir` 失败 |
| 400 | `invalid_size` | cols/rows 越界 |
| 409 | `terminal_limit` | 全局或 per-session 上限 |
| 501 | `terminal_unsupported` | 平台没有 PTY 实现 |

**WebSocket：**

- 握手：同源校验 `Origin`（`x/net/websocket` 不会替你校验）；必须已通过 auth（cookie 或 Bearer）；URL 只带 terminalId，不带 cwd/shell/command。
- 第一帧服务端发送 JSON control：

```json
{"type":"attached","terminalId":"t_...","cols":120,"rows":32,"state":"running","replayBytes":4096}
```

- 随后发送 ring buffer 的 replay（二进制 output 帧），再发送当前 live 输出。
- 客户端 → 服务端二进制帧：

```text
[0x01][raw input bytes]                 输入
[0x02][cols:uint16 BE][rows:uint16 BE]  resize
[0x03]                                  ping
```

- 服务端 → 客户端二进制帧：

```text
[0x01][raw output bytes]                输出
[0x02][exitCode:uint32 BE]              进程退出
[0x03][JSON control bytes]              error/replay-done/...
```

- 允许客户端发送文本 JSON 作为兼容/调试（`{"type":"resize","cols":...}`），但主路径用二进制，避免 base64 33% 开销。
- `x/net/websocket` 的 `websocket.Message.Receive/Send` 可收发 `[]byte`/`string`；用 `websocket.Message` 而不是 `websocket.JSON`。控制帧可以编码成 JSON 字节。
- 单连接单 writer goroutine；输入 reader 只写 PTY；输出 reader 只写 WebSocket。不要让多个 goroutine 并发 `Send` 同一连接。

**重连：**

- WebSocket 断开不立即 kill PTY；terminal 进入 `detached`，保留 ring buffer，等待 `IdleTimeoutSecs`。
- 新连接 attach 同一 terminalId：停止旧 client（如果还活着）、发送 ring replay、恢复 live。旧 client 收到 `terminal_takeover` 后关闭。
- 超过 idle timeout 或显式 `DELETE` 才 kill。
- 浏览器刷新/短暂断网不会丢命令输出；serve 重启则全部丢失（进程死亡）。

## 6. 进程、cwd、环境与安全

**cwd：**

- 只用 `sessions.GetSessionWorkDir(sessionID)`（`internal/serve/openaiapi/session_mgr.go:1275`）解析；不存在/不可读 → 404 `session_not_found`。
- 客户端不能传 cwd、shell、command；API body 只允许 `sessionId/cols/rows`。
- 不把 terminal 绑定到任意用户目录；session 删除时 `CloseSession`。

**shell：**

- `cfg.Terminal.Shell` 优先；为空用 `settings.GetShell()`（`internal/config/settings.go:2115`），默认 `platform.DefaultShell()`。
- 只启动 shell 本身（交互模式），不执行客户端命令；shell 通过 TTY 自动进入交互。
- Windows 用 ConPTY + `powershell.exe`/`cmd.exe`；Unix 用 login/interactive shell（按平台）。
- 不允许 `sh -c <client string>`；终端输入由用户在 PTY 里键入。

**环境：**

- 基础：`os.Environ()` + `TERM=xterm-256color` + `COLORTERM=truecolor` + `LANG`/`LC_ALL`（若存在）。
- **不要无条件继承所有服务端 secret。** 启动前过滤：
  - 删除 `MOTHX_*`、`OPENAI_API_KEY`、`ANTHROPIC_API_KEY`、`GOOGLE_*_KEY`、`*_TOKEN`、`*_SECRET`、`*_PASSWORD` 等（用显式 denylist；不要只靠前缀猜）。
  - 保留 `PATH`、`HOME`、`USER`、`SHELL`、`TMPDIR`、`LANG`、`LC_*`、`TERM`、`COLORTERM`。
  - 如果用户明确需要某个变量，后续可加 `terminal.envPassthrough` 白名单；v1 不开放。
- 不继承 `MOTHX_SERVE_*` 之类内部开关。

**进程组/退出：**

- Unix：`Setpgid: true`；关闭时 `kill(-pgid, SIGTERM)`，等待 2s 后 `SIGKILL`；PTY master 关闭。
- Windows：ConPTY handle + Job Object；关闭时 terminate job；`Wait` 取 exit code。
- Linux 可设 `Pdeathsig=SIGTERM`，serve 崩溃时尽量带走 shell；Windows 用 job object 的 kill-on-close。
- 不把 PTY 子进程挂到 Agent 的 `exec.CommandContext` 生命周期；terminal 有自己的 context/manager。

**安全：**

- **默认可用，但不裸奔**：解析后的 `terminalEnabled` 为真时注册路由/返回 feature；回环默认可用，公开监听无 auth 拒绝。
- **默认只允许回环监听**；如果 `listen` 非 `127.0.0.1`/`localhost` 且 `auth.enabled=false`，创建返回 `terminal_unsafe`（即使 enabled）。
- **auth**：复用 `AuthMiddleware`；WebSocket 也要过同一 middleware（cookie/Bearer）。不要为终端另开匿名通道。
- **Origin**：只允许同源 `Origin`；缺失/跨域拒绝。
- **沙箱**：若 `cfg.API.Sandbox.Enabled`（或 settings sandbox）为 true，终端仍可用，但 UI 必须持续显示“终端不受沙箱保护”；不要在日志/文档中宣称终端受沙箱约束。`AllowUnsafe` 仅保留兼容，不再作为启用门槛。
- **资源上限**：`MaxSessions`/`MaxPerSession`、`cols/rows` 范围（如 20–500 / 5–300）、`MaxOutputBytesPerSec`、`ScrollbackBytes`、`IdleTimeoutSecs`；超过输出速率时对 PTY 读取做短暂 sleep/丢弃？终端不能丢字节，正确做法是反压（暂停读取）或断开慢客户端；v1 选择断开并让客户端重连 replay。
- **审计**：serve log 只记录 `terminal_created/terminal_exited/terminal_killed`（terminalId、sessionId、cwd、exitCode），**不记录输入/输出内容**。
- **不落盘**：ring buffer 只在内存；不写 session DB、不写 transcript、不写文件。这样也避免把用户 shell 输出误当 agent 证据。

## 7. 并发与生命周期

- `Manager.mu` 保护 `byID`/`bySession`；创建/删除/查找都在锁内完成，PTY I/O 不在锁内。
- 每个 Terminal 一个 read loop（PTY → ring + attached client）和一个 wait goroutine（`cmd.Wait` → exit code/state）。
- 单 attached client；新连接 takeover 旧连接。不要允许多个控制端同时输入（会造成不可预测的 shell 状态）；如果未来要“旁观”，加只读 watcher 模式，不写 PTY。
- `channelRuntime.stop()` 调用 `terminals.Shutdown()`；`Run` 的 defer 链保证 serve 退出/信号取消时关闭所有 PTY。
- `DELETE /api/sessions/{id}` 成功路径调用 `rt.terminals.CloseSession(id)`；如果 session 已在别处删除，manager 的 `CloseSession` 可幂等。
- terminal 不参与 Runtime lease：session 在别的进程运行时，本进程仍可开终端（同一个 cwd），这是产品选择；如果担心并发写，UI 提示“Agent 可能同时在此目录工作”。不要用 lease 阻塞用户终端。

## 8. 前端契约（供布局方案引用）

- 前端新增 `@xterm/xterm`、`@xterm/addon-fit`（桌面优先；不引入 React 生态；移动端不在 v1 验收范围）。
- `TerminalPane.svelte`：
  - `POST /api/terminals` → 拿 terminalId；
  - `new WebSocket(`/ws/terminals/${id}`)`，`binaryType='arraybuffer'`；
  - `term.onData` → 发送 `0x01 + TextEncoder.encode(data)`；
  - `FitAddon` resize → 发送 `0x02 + cols/rows`；
  - 收到 `0x01` → `term.write(new Uint8Array(payload))`；
  - 收到 `0x02` → 显示“进程已退出（code）”，关闭/重开；
  - 收到 `0x03` → JSON 控制（attached/replay-done/error）。
- 终端作为工具面板（Phase W3+），绑定 session cwd；不是 v1 的 session tab。
- 断线重连：显示“重新连接”按钮，attach 同一 terminalId；不要自动新建终端（会丢状态）。

## 9. 测试

**单元（`internal/terminal`）：**

- ring buffer 回绕/容量/并发写读；
- resize 夹紧、cols/rows 边界；
- manager 上限（全局/per-session）、重复关闭、幂等 shutdown；
- shell 解析：`settings.GetShell()`、空值、非法路径；
- env 过滤：secret 不进入子进程，`TERM/COLORTERM` 存在；
- build tags：不支持平台返回 `ErrUnsupported`。

**HTTP/WS（`internal/serve`）：**

- `terminal_disabled`、`terminal_unsafe`、`session_not_found`、`invalid_size`、`terminal_limit`；
- auth 开启时无 cookie/Bearer → 401；跨 Origin → 403；
- 创建/attach/输入/resize/输出/exit；
- 断开重连 replay；idle timeout kill；
- `DELETE` 关闭；`DELETE session` 关闭该 session 的所有终端；
- `Shutdown` 关闭所有 PTY，等待子进程；
- 并发 create/close 不 panic/不泄漏 goroutine。

**平台矩阵：**

- Linux（`creack/pty`）、macOS（`creack/pty`）、Windows（ConPTY，要求 Windows 10 1809+ / Windows 11）。Windows 是必测项，MothX 有 Windows 用户；更老或不支持 ConPTY 的环境返回 `terminal_unsupported`，不能静默退化成 pipe。

**前端 e2e（后续）：**

- 打开 terminal、`echo hello`、resize、刷新重连、关闭；
- 与 Agent `bash` 工具同时运行互不影响（不同进程）。

## 10. 依赖与实现阶段

**依赖：**

- Unix：`github.com/creack/pty`（成熟、小；只用于 Unix）。
- Windows：`github.com/UserExistsError/conpty` 或等价的 ConPTY 封装；必须支持 resize/exit。
- WebSocket：沿用 `golang.org/x/net/websocket`（`internal/serve/logs.go` 已用），不引入 gorilla 直接依赖。
- 依赖必须 pin 到 `go.mod` 的精确版本；`pty_unix.go`/`pty_windows.go` 用 build tags 隔离，避免 Windows 构建拉 Unix 代码。

**阶段：**

| 阶段 | 内容 |
| --- | --- |
| T0 | `internal/terminal` manager + Unix PTY + ring/resize/kill + 单测 |
| T1 | Windows ConPTY + build tags + 平台单测 |
| T2 | `internal/serve/terminal.go` HTTP/WS + auth/origin + 配置/上限 + 集成测试 |
| T3 | 前端 `TerminalPane.svelte` + xterm + 重连 + 工具面板接入 |
| T4 | 安全审计（env/沙箱/资源上限）、e2e、文档 |

**范围：** 后端与前端分开交付；**不包含在 WebUI 布局 v1 内**；它是独立后端能力。

## 11. 明确拒绝的做法

- 不要用 `bash` 工具 + `exec.Command` + pipes 假装 PTY：`isatty` 为 false，vim/top/ssh 等交互程序不可用，resize 无效。
- 不要在 `openaiapi.Server` 里直接 `exec.Command`，绕过 `channelRuntime` 的配置/鉴权/上限。
- 不要把 terminal id 写进 session DB 或 transcript；不要持久化输出。
- 不要允许客户端传 cwd/shell/command；不要为终端开匿名 WebSocket。
- 不要在沙箱开启时静默允许终端；必须持续在 UI 标明“终端不受沙箱保护”，不要用 PTY 输出冒充 sandbox 内结果。
- 不要用 PTY 输出推断 Agent 工具结果；两者是不同数据源。

