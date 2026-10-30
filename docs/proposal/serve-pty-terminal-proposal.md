# Serve PTY / Terminal 后端方案

> Date: 2026-10-28 · Last revised: 2026-10-30 · Status: Implemented (T0–T4, Pdeathsig, and Windows kill-on-close Job Object implemented 2026-10-29; 2026-10-30 follow-up closed the 501 mapping and build-tag gaps; real-Windows runner execution outstanding) · Owner: Serve (Go)
> Related: `AGENTS.md`, `docs/proposal/webui-dynamic-workbench-layout-proposal.md`
> Scope: serve-only，独立于 WebUI 工作台 v1；不随布局方案一起实现。

> 优先级：**易用性 > 安全/隐私摩擦**，但不得越过 AGENTS.md 的硬边界。回环监听默认可用；非回环监听在 auth 已启用且配置了 token 时默认可用；只有“非回环且未认证”才拒绝。auth 是访问边界，共享 token 的风险放入部署文档和安全提示，不再叠加额外开关。同源、认证、不把未认证 shell 暴露到公网是不可关闭的安全底线。PTY 不创建 Run、不写 session transcript、不占 ExecutionRuntime lease。


> 本文回答“tty 面板如果要真做，Go 后端应该放在哪、怎么接、边界是什么”。它是**独立能力**，不属于 WebUI 布局 v1；不要因为前端加了 tab 就把 PTY 顺手塞进 `internal/tools/bash.go` 或 `internal/serve/openaiapi` 的 session pool。

## 1. 结论与边界

- **PTY 是 serve-only 的后端能力**：只有 `mothx serve` 的 WebUI 使用；TUI/CLI/ACP 不受影响，也不共享终端会话。
- **它不是 Agent 的 `bash` 工具**：`bash` 是每次工具调用启动一个非交互子进程、捕获 stdout/stderr；PTY 是长生命周期、可输入、可 resize、可重连的交互式 shell。
- **它不是 session/run 的一部分**：terminal 不创建 Run、不写 session transcript、不占 ExecutionRuntime lease；它只绑定一个已存在的 session 的 cwd。
- **回环或已认证的非回环默认可用**：`127.0.0.1`/`localhost` 且未显式关闭时可用；非回环监听只要 auth 已启用且配置了 token 就可用（`Enabled=nil` 即自动）。只有非回环 + auth 未启用/无 token 才拒绝创建（`terminal_unsafe`）。用户显式 `terminal.enabled=false` 可关闭。
- **不承诺沙箱，但不隐藏事实**：PTY 是用户 shell 的直接入口，等价于 serve 用户权限的本地 shell，**完整继承 serve 进程的环境变量**，能读取该用户可读的所有文件和凭据。沙箱开启时仍可用（易用性优先），但 UI 必须持续显示“终端不受沙箱保护”，不要对外宣称“终端在 sandbox 内”。
- **不持久化**：terminal id、输出、进程状态只在内存；serve 重启/进程退出即终止。需要历史请用 transcript/trajectory，不要把终端输出写 DB。

## 2. 代码归属（Go 包与文件）

新增：

```text
internal/terminal/
├── terminal.go          # Terminal 结构、状态机、ring buffer、resize、Close
├── manager.go           # 注册表：byID / bySession、创建/查找/关闭、上限、shutdown
├── buffer.go            # ring buffer / output limiter
├── pty_unix.go          # //go:build !windows：creack/pty
├── pty_windows.go       # //go:build windows：ConPTY（当前为 ErrUnsupported 占位）
├── pty_unsupported.go   # 显式平台集合：返回 ErrUnsupported
├── shell.go             # shell 解析、环境变量继承、TERM/COLORTERM
└── *_test.go

internal/serve/terminal.go   # HTTP/WS handler、auth/origin/token、协议编解码
internal/serve/terminal_config.go # TerminalConfig/EffectiveTerminalEnabled
internal/serve/terminal_test.go

ui/src/components/terminal/TerminalPane.svelte # xterm 前端（未接入工具面板）
```

修改：

```text
internal/serve/config.go       # Config 增加 Terminal TerminalConfig；DefaultConfig/normalize
internal/serve/config_schema.go # rawTerminalConfig（缺省/显式三态）
internal/serve/config_mapping.go # applyRawConfig 映射（如需要）
internal/serve/run.go          # channelRuntime 增加 terminals *terminal.Manager；初始化/stop/routes/featureStatus
internal/serve/run.go          # delete session 成功后 rt.terminals.CloseSession(sessionID)
internal/serve/openaiapi/auth.go # 复用 AuthMiddlewareForConfig（不新增认证模型）
internal/config/settings.go    # 复用 (*Settings).GetShell()/ShellPath；不新增字段（终端只读它）
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
    // nil = 自动：回环可用；非回环且 auth 已配置 token 可用。
    // true = 显式开启（仍要求非回环有 auth + token，不绕过 auth）；false = 关闭。
    // 不要把 nil 在 normalize 时解析成 bool，否则热更新 auth/listen 后会失真。
    Enabled *bool `json:"enabled,omitempty"`

    // Shell 为空时使用 (*Settings).GetShell()。
    Shell string `json:"shell,omitempty"`

    // 资源上限；0 表示默认值。
    MaxSessions    int `json:"maxSessions,omitempty"`         // 默认 8
    MaxPerSession  int `json:"maxPerSession,omitempty"`       // 默认 2
    ScrollbackBytes int `json:"scrollbackBytes,omitempty"`    // 默认 1048576
    MaxOutputBytesPerSec int `json:"maxOutputBytesPerSec,omitempty"` // 默认 1048576

    // 反代/自定义前端；空数组 = 严格同源。
    AllowedOrigins []string `json:"allowedOrigins,omitempty"`
}
```

`DefaultConfig()`：`Terminal.Enabled=nil`，其余给默认值。`normalize()` 只做资源夹紧和 shell 回退，**不把 Enabled 解析成具体 bool**：

- `MaxSessions` 夹紧 `[1, 32]`；`MaxPerSession` 夹紧 `[1, 4]`；
- `ScrollbackBytes` 夹紧 `[64KiB, 8MiB]`；
- `MaxOutputBytesPerSec` 夹紧 `[64KiB, 16MiB]`；
- `AllowedOrigins` 只接受 `scheme://host[:port]`，非法项丢弃；空数组保持严格同源；
- `Shell` 只接受绝对路径或可解析的 basename；不合法则回退当前 settings 的 `(*Settings).GetShell()`；
- `Enabled=nil` 保持 nil，`false` 保持 false，`true` 保持 true。

新增 `EffectiveTerminalEnabled(cfg *Config) (enabled bool, reason string)`，在创建 handler 和 `featureStatus` 计算时调用，而不是启动时冻结：

- `Enabled=false` → false；
- `terminalSupported()==false` → false；
- 监听地址为回环（`127.0.0.1`/`localhost`/`[::1]`）且 `Enabled!=false` → true；
- 非回环且 `cfg.API.Auth.Enabled && len(cfg.API.Auth.Tokens)>0` → true（`Enabled=nil` 或 true 均可；true 不绕过 auth）；
- 非回环且 auth 未启用或没有 token → false（`terminal_unsafe`）；
- 若 `UsesPlaceholderAuthToken(cfg)` 为 true，仍按 auth 放行，但返回 `warning=placeholder_token`；启动日志和 `/api/status` 必须持续强警告。

这样 `srv.ApplyServeConfig` 热更新 auth 时，下一次 `/api/status` 和创建请求会立即反映新策略，不会继续使用启动时的旧判断。

`featureStatus` 增加 `terminal: bool`，由 `EffectiveTerminalEnabled(cfg)` 与 `terminalSupported()` 决定；WebUI 只在为 true 时显示入口。`terminalSupported()` 由 build tags 提供。`/api/status` 还应暴露 `terminalWarning`（例如 `placeholder_token`），供 WebUI 显示持续横幅，而不是只写日志。

配置接入还必须覆盖：`Config` 增加 `Terminal TerminalConfig`；`DefaultConfig()` 初始化；`DecodeConfigBytes`/`handleServeConfig` 的 GET/PUT 往返；`serve.json` 文档与 `config_test`。现有 `rawConfig` 模式用于“缺省 vs 显式值”的字段，`Enabled *bool` 必须在往返测试中验证 nil/true/false 三态不丢失。`AllowedOrigins` 也要在 PUT 后立即应用于 handler。

## 4. 数据模型与状态机

```go
// internal/terminal/terminal.go
type State string
const (
    StateStarting State = "starting"
    StateRunning  State = "running"
    StateDetached State = "detached" // PTY 仍存活，但没有 attached client
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

- `Create(sessionID, cwd string, cols, rows uint16) (*Terminal, error)`：校验上限、shell、cwd、平台支持；启动 PTY；注册；启动 read loop。达到 `MaxSessions`/`MaxPerSession` 返回 409，不自动淘汰旧终端。
- `UpdateConfig(cfg TerminalConfig)`：热更新 limits/shell/scrollback/backpressure；新限制立即用于新建和 resize，已有终端尽量保留。
- `Get(sessionID, terminalID)`：HTTP 管理接口使用；必须同时匹配，防止跨 session 访问。
- `GetByID(terminalID)`：WebSocket attach 使用（URL 只有 terminalId）；返回 terminal 及其绑定的 sessionId。
- `Attach(terminalID, client)`：验证 terminal 未退出，停止旧 client（takeover），发送 `attached` + ring replay，再恢复 live；状态回 `running`。
- `Close(sessionID, terminalID)`：kill process group，标记 killed，通知 attached client，移出注册表。
- `CloseSession(sessionID)`：session 删除时调用。
- `Shutdown()`：serve 退出时关闭全部；`channelRuntime.stop()` 中调用。
- 状态：`starting -> running <-> detached -> exited|killed`；`exited` 可从 PTY EOF/`cmd.Wait` 得到 exit code；`killed` 由显式关闭/上限触发。
- attachment：单 attached client；新连接 takeover 旧连接。`Detach` 只清 client、进 `detached`，不 kill PTY；**detached 不自动超时关闭**，只在 serve 退出或显式 `DELETE`/`CloseSession` 时 kill。
- 终端 id：`crypto/rand` 128-bit，base64url；不要用 session id + 序号（可预测）。

## 5. HTTP / WebSocket API

路由注册在 `channelRuntime.routes`（`internal/serve/run.go`），与 `/ws/runs`、`/ws/logs` 同级；所有 handler 经过现有 `AuthMiddleware`（`internal/serve/openaiapi/auth.go`）：

```text
POST   /api/terminals                 创建
GET    /api/terminals?sessionId=...   列出该 session 的终端（可选）
DELETE /api/terminals/{id}            关闭
GET    /ws/terminals/{id}             WebSocket attach
```

`mux` 必须同时注册 `"/api/terminals"` 与 `"/api/terminals/"`（前者精确匹配，后者处理子路径），否则 `DELETE /api/terminals/{id}` 会被根 `/` handler 吞掉。**只要 `terminalSupported()` 就始终注册路由**；每个 handler 按当前 `EffectiveTerminalEnabled` 拒绝/放行，不能在启动时按 feature 条件注册，否则 auth 热更新不会生效。所有 handler 都必须在顶层 `AuthMiddlewareForConfig` 之内。

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
  "state": "running",
  "terminalToken": "tt_..."  // 仅非浏览器/Bearer 创建时返回；绑定 terminalId + 创建客户端直到终端关闭
}
```

错误码（JSON `{error, code}`）：

| HTTP | code | 场景 |
| --- | --- | --- |
| 403 | `terminal_disabled` | 配置显式关闭（`terminal.enabled=false`） |
| 403 | `terminal_unsafe` | 非回环且 auth 未启用/未配置 token |
| 404 | `session_not_found` | `GetSessionWorkDir` 失败 |
| 400 | `invalid_size` | cols/rows 越界 |
| 409 | `terminal_limit` | 全局或 per-session 上限 |
| 501 | `terminal_unsupported` | 平台没有 PTY 实现 |

**WebSocket：**

`terminalToken` 只用于非浏览器客户端；浏览器 WebUI 仍用 HttpOnly cookie。非浏览器没有浏览器 session，`terminalToken` 自身就是客户端会话标识：绑定 terminalId + 创建时的 auth principal（token hash），不能用于其他 terminal；优先通过 `Sec-WebSocket-Protocol` 传递，query 仅作兼容且日志必须脱敏。

- 握手：浏览器 WebUI 用同源 `Origin` + HttpOnly cookie；非浏览器用 `POST /api/terminals` 返回的短时 `terminalToken`（绑定 terminalId + 创建它的客户端会话，直到终端关闭），通过 query 或 `Sec-WebSocket-Protocol` 传入。`allowedOrigins` 为空时严格同源；非空时只允许列表中的完整 origin。`x/net/websocket` 的 `websocket.Handler` 会拒绝缺失/非法 Origin；要支持无 Origin 的非浏览器客户端，必须用 `websocket.Server` 自定义 handshake，不能用 `Handler`。URL 只带 terminalId，不带 cwd/shell/command。
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
[0x03][JSON control bytes]              error/replay-done/closed(reason)
```

- 服务端在 `cwd_changed` 时主动关闭旧终端，发送 `closed` 控制帧（reason=`cwd_changed`）；前端收到后按新 cwd 重新 `POST /api/terminals`，不静默保留旧 shell。**当前 serve 没有修改既有 session 工作目录的 HTTP 入口（`SetWorkDir` 仅由 ACP 使用），因此 serve 不会实际发出 `cwd_changed`；前端保留该帧处理以便未来接入。**

- 允许客户端发送文本 JSON 作为兼容/调试（`{"type":"resize","cols":...}`），但主路径用二进制，避免 base64 33% 开销。
- `x/net/websocket` 的 `websocket.Message.Receive/Send` 可收发 `[]byte`/`string`；用 `websocket.Message` 而不是 `websocket.JSON`。控制帧可以编码成 JSON 字节。
- 单连接单 writer goroutine；输入 reader 只写 PTY；输出 reader 只写 WebSocket。不要让多个 goroutine 并发 `Send` 同一连接。

**重连：**

- WebSocket 断开不立即 kill PTY；terminal 进入 `detached`，保留 ring buffer，不自动超时关闭。
- 新连接 attach 同一 terminalId：停止旧 client（如果还活着）、发送 ring replay、恢复 live。旧 client 收到 `terminal_takeover` 后关闭。
- 只有显式 `DELETE`/`CloseSession`、session 删除、cwd 变更或 serve 退出才 kill。
- 浏览器刷新/短暂断网通常可在 replay 窗口内恢复；serve 重启则全部丢失（进程死亡）。ring buffer 覆盖或慢客户端反压时，超出 replay 窗口的输出会丢失，这属于终端语义，不是持久化保证。

## 6. 进程、cwd、环境与安全

**cwd：**

- 只用 `sessions.GetSessionWorkDir(sessionID)`（`internal/serve/openaiapi/session_mgr.go:1275`）解析；不存在/不可读 → 404 `session_not_found`。
- 客户端不能传 cwd、shell、command；API body 只允许 `sessionId/cols/rows`。
- 不把 terminal 绑定到任意用户目录；session 删除时 `CloseSession`。
- 任何 Runtime 路径修改 session workDir 时：先 `CloseSession(sessionID)`，向已连接客户端发送 `closed(reason=cwd_changed)`，再由前端按新 cwd 重新创建；不尝试对运行中的 shell 做 chdir。

**shell：**

- `cfg.Terminal.Shell` 优先；为空用 `(*Settings).GetShell()`（`internal/config/settings.go:2116`），默认 `platform.DefaultShell()`。
- 只启动 shell 本身（交互模式），不执行客户端命令；shell 通过 TTY 自动进入交互。
- Windows 用 ConPTY + `powershell.exe`/`cmd.exe`；Unix 用 login/interactive shell（按平台）。
- 不允许 `sh -c <client string>`；终端输入由用户在 PTY 里键入。

**环境：**

- **完整继承 `os.Environ()`**：PTY 是用户自己的 shell，不做 allowlist/denylist、不裁剪凭据、不删除 `MOTHX_*` 或任何 provider key。否则代理、SSH agent、云 CLI、KUBECONFIG、自定义工具链都会神秘失效，而用户仍能从 `~/.aws`、`.env`、SSH 配置读取同样的凭据，过滤只是假安全。
- 仅做终端语义补充：若 `TERM` 为空设 `xterm-256color`，若 `COLORTERM` 为空设 `truecolor`；不要覆盖用户显式设置的值。
- Windows 额外继承 `SystemRoot`/`COMSPEC`/`PATHEXT` 等由父进程已有的变量，不做额外裁剪。
- 在日志/文档中明确：PTY 与 serve 用户同权限、同环境；需要隔离时请用独立 OS 用户/容器/VM 运行 serve，而不是在 PTY 启动时过滤 env。

**进程组/退出：**

- Unix：交给 `pty.StartWithAttrs` 的 `Setsid/Setctty` 建立会话（子进程成为 session/process-group leader），关闭时对该进程组发 `SIGHUP`（终端挂断语义；交互式 dash/bash 会忽略 `SIGTERM`），等待 2s 后 `SIGKILL`；PTY master 关闭。不要额外依赖 `Setpgid` 与 `Setsid` 的冲突配置。
- Windows：ConPTY handle + Job Object；关闭时 terminate job；`Wait` 取 exit code。
- Linux 可设 `Pdeathsig=SIGKILL`，serve 崩溃时尽量带走 shell；Windows 用 job object 的 kill-on-close。
- 不把 PTY 子进程挂到 Agent 的 `exec.CommandContext` 生命周期；terminal 有自己的 context/manager。

**安全：**

- **回环或已认证的非回环默认可用**：`EffectiveTerminalEnabled` 为真时注册路由/返回 feature。回环监听默认可用；非回环在 auth 已启用且有 token 时默认可用，不再叠加 `terminal.enabled=true`。
- **占位 token 只警告不阻断（产品决定）**：若 `UsesPlaceholderAuthToken(cfg)` 为 true，终端仍可用，但启动日志、`/api/status` 和 UI 必须持续显示“正在使用公开占位 token，任何知道默认值的人都能访问终端”；这是明确的风险提示，不是静默放行。
- **共享 token 的含义必须在部署文档中写明**：serve 的 `auth.tokens` 是单一共享凭据，没有用户/角色模型；任何持有 token 的 API 调用方都能获得 serve 用户权限的 shell。这是“谁能访问 serve”的授权结果，不是再加一个开关的理由；如果未来需要按用户区分 shell 权限，再引入独立授权模型。
- **auth**：复用 `AuthMiddlewareForConfig`；WebSocket 也要过同一 middleware（cookie/Bearer）。auth 开启但 `len(tokens)==0` 时 `EffectiveTerminalEnabled=false`（配置本身不完整）；不要为终端另开匿名通道。
- **Origin**：浏览器严格同源；`AllowedOrigins` 非空时只允许列表中的完整 `scheme://host[:port]`。非浏览器走短时 `terminalToken`；`websocket.Handler` 只做非空/可解析校验，同源比较必须自己实现。
- **沙箱**：若 `cfg.API.Sandbox.Enabled`（或 settings sandbox）为 true，终端仍可用，但 UI 必须持续显示“终端不受沙箱保护”；不要在日志/文档中宣称终端受沙箱约束。终端不使用 `AllowUnsafe` 之类的兼容开关（新功能无旧配置可兼容）。
- **资源上限**：`MaxSessions`/`MaxPerSession`、`cols/rows` 范围（如 20–500 / 5–300）、`MaxOutputBytesPerSec`、`ScrollbackBytes`。输出超过 `MaxOutputBytesPerSec` 时**暂停 PTY read loop 做反压**，不丢连接；子进程可能短暂阻塞。达到 `MaxSessions` 时创建返回 409 `terminal_limit`，提示用户关闭旧终端，不自动淘汰。ring buffer 仍可能覆盖旧输出，因此只承诺“ring 窗口内 replay”，不承诺零丢失。
- **审计**：serve log 只记录 `terminal_created/terminal_exited/terminal_killed`（terminalId、sessionId、cwd、exitCode），**不记录输入/输出内容**。
- **不落盘**：ring buffer 只在内存；不写 session DB、不写 transcript、不写文件。这样也避免把用户 shell 输出误当 agent 证据。

## 7. 并发与生命周期

- `Manager.mu` 保护 `byID`/`bySession`/`cfg`；创建/删除/查找/更新配置都在锁内完成，PTY I/O 不在锁内。
- `applyConfigUpdate` 调用 `terminals.UpdateConfig(next.Terminal)`；新的 limits/shell/scrollback 立即生效，已有终端尽量保留。
- 每个 Terminal 一个 read loop（PTY → ring + attached client）和一个 wait goroutine（`cmd.Wait` → exit code/state）。
- 单 attached client；新连接 takeover 旧连接。不要允许多个控制端同时输入（会造成不可预测的 shell 状态）；如果未来要“旁观”，加只读 watcher 模式，不写 PTY。
- `channelRuntime.stop()` 调用 `terminals.Shutdown()`；`Run` 的 defer 链保证 serve 退出/信号取消时关闭所有 PTY。
- 删除 session 的钩子在 `internal/serve/run.go` 的 `handleSessionByID`：`lifecycle.Delete` 成功且 `deleted=true` 后调用 `rt.terminals.CloseSession(id)`（当前约 2389 行）；`CloseSession` 必须幂等。
- session 的 cwd 变更/fork 不自动迁移已有终端：fork 生成新 session，旧 terminal 只属于原 session，不自动搬到新 session。serve 目前没有 cwd 变更入口；一旦新增，必须在变更成功后调用 `CloseSession` 并以 `cwd_changed` 关闭旧终端。
- terminal 不参与 Runtime lease：session 在别的进程运行时，本进程仍可开终端（同一个 cwd），这是产品选择；UI 必须在创建前提示“Agent 可能同时在此目录工作”，并在 session 被其他进程持有时显示更强警告。不要用 lease 阻塞用户终端；如果产品后续要求硬互斥，应在共享 Runtime policy 中定义，而不是在终端 manager 里另写一套。

## 8. 前端契约（供布局方案引用）

- 前端新增 `@xterm/xterm`、`@xterm/addon-fit`（桌面优先；不引入 React 生态；移动端不在 v1 验收范围），版本必须 pin。终端入口只在 `/api/status.features.terminal===true` 时显示；关闭后不保留 xterm 实例。
- `TerminalPane.svelte`：
  - `POST /api/terminals` → 拿 terminalId；非浏览器还需要响应里的短时 `terminalToken`；
  - `new WebSocket(`/ws/terminals/${id}`)`，浏览器用 cookie + Origin；非浏览器带 `terminalToken`，`binaryType='arraybuffer'`；
  - `term.onData` → 发送 `0x01 + TextEncoder.encode(data)`；
  - `FitAddon` resize → 发送 `0x02 + cols/rows`；
  - 收到 `0x01` → `term.write(new Uint8Array(payload))`；
  - 收到 `0x02` → 显示“进程已退出（code）”，关闭/重开；
  - 收到 `0x03` → JSON 控制（attached/replay-done/closed/error）；`closed(reason=cwd_changed)` 时按新 cwd 自动重建一次；
  - 服务端反压时显示“终端输出受限”状态，不要把暂停误判为断线。
- 终端作为工具面板（Phase W3+），绑定 session cwd；不是 v1 的 session tab。
- 断线重连：显示“重新连接”按钮，attach 同一 terminalId；不要自动新建终端（`cwd_changed` 的自动重建是唯一例外）。

## 9. 测试

**单元（`internal/terminal`）：**

- ring buffer 回绕/容量/并发写读；
- manager 上限（全局/per-session）、达到上限返回 409 且不淘汰旧终端、重复关闭、幂等 shutdown；
- `UpdateConfig`：新 limits/shell/scrollback 立即用于新建，已有终端保留；
- resize 夹紧、cols/rows 边界；
- shell 解析：`(*Settings).GetShell()`、空值、非法路径；
- env 继承：`os.Environ()` 完整进入子进程；`TERM/COLORTERM` 只在缺失时补充；不修改用户变量。
- build tags：不支持平台返回 `ErrUnsupported`。

**HTTP/WS（`internal/serve`）：**

- `terminal_disabled`、`terminal_unsafe`、`session_not_found`、`invalid_size`、`terminal_limit`；
- auth 开启时无 cookie/Bearer → 401；跨 Origin → 403；缺失 Origin → 403；同源通过；`allowedOrigins` 命中通过，未命中拒绝；
- 非浏览器 `terminalToken`：绑定 terminalId + 创建客户端，terminal 关闭后失效；伪造/过期 token 拒绝；
- 创建/attach/输入/resize/输出/exit；
- 断开重连 replay；takeover 后旧连接收到 `terminal_takeover`；detached 不自动超时关闭；
- `DELETE` 关闭；`DELETE session` 关闭该 session 的所有终端；cwd 变更后旧终端关闭并发送 `cwd_changed`（当前 serve 无 cwd 变更入口，该断言待入口存在后补）；
- `Shutdown` 关闭所有 PTY，等待子进程；
- 达到 `MaxSessions` 时创建返回 409，不自动 LRU 淘汰；
- 输出超过 `MaxOutputBytesPerSec` 时暂停 PTY read loop，不丢连接；
- 并发 create/close 不 panic/不泄漏 goroutine；
- `EffectiveTerminalEnabled`：回环 nil → true；非回环 nil + auth/token → true；非回环 nil + 无 auth → false；非回环 `Enabled=false` → false；运行时关闭 auth 后下一个请求立即 false；占位 token 返回 `terminalWarning=placeholder_token` 但仍可用；
- `serve.json` / `PUT /api/serve/config` 往返保留 `enabled` 的 nil/true/false 三态与 `allowedOrigins`；路由始终注册，feature 动态变化；
- env 继承：父进程的自定义变量、代理、SSH/云 CLI 变量都仍在子进程中；`TERM/COLORTERM` 存在。

**平台矩阵：**

- Linux（`creack/pty`）、macOS（`creack/pty`）、Windows（ConPTY，要求 Windows 10 1809+ / Windows 11）。Windows 是必测项，MothX 有 Windows 用户；更老或不支持 ConPTY 的环境返回 `terminal_unsupported`，不能静默退化成 pipe。
- CI 至少增加 `GOOS=windows go build ./...` 与 `GOOS=darwin go build ./...` 的交叉编译检查；Unix 专属的 `Setpgid`/`Pdeathsig` 不能出现在共享文件。
- Windows ConPTY 必须在真实 Windows runner（或手工矩阵）验证 start/input/resize/exit；不能只靠交叉编译。

**前端 e2e（后续）：**

- 打开 terminal、`echo hello`、resize、刷新重连、关闭；
- 与 Agent `bash` 工具同时运行互不影响（不同进程）。

## 10. 依赖与实现阶段

**依赖：**
- Unix：`github.com/creack/pty`（成熟、小；只用于 Unix）。这是新增直接依赖，需要显式批准、pin 版本、检查 license，并在 `go.mod`/`go.sum` 留 diff；不要为了省依赖自己实现 ioctl。
- Windows：`github.com/UserExistsError/conpty` 或等价的 ConPTY 封装；必须支持 resize/exit。若该库不可维护，改用 `golang.org/x/sys/windows` 直接封装 ConPTY，不要引入第二套终端抽象。
- WebSocket：沿用 `golang.org/x/net/websocket`（`internal/serve/logs.go` 已用），不引入 gorilla 直接依赖。
- 依赖必须 pin 到 `go.mod` 的精确版本；`pty_unix.go`/`pty_windows.go` 用 build tags 隔离，避免 Windows 构建拉 Unix 代码。`pty_unsupported.go` 的 tag 写成显式平台集合（如 `//go:build !linux && !darwin && !freebsd && !windows`），不要写“其他”。
- `(*Settings).GetShell()` 是方法，不是包级函数；调用方从当前 settings snapshot 取值。

**阶段：**

| 阶段 | 内容 |
| --- | --- |
| T0 | `internal/terminal` manager + Unix PTY + ring/resize/kill + 单测 |
| T1 | Windows ConPTY + build tags + 交叉编译/真实 Windows 测试 |
| T2 | `internal/serve/terminal.go` HTTP/WS + auth/origin/token + effective 配置/上限 + `UpdateConfig` + 集成测试 |
| T3 | 前端 `TerminalPane.svelte` + xterm + 重连/反压/cwd 重建 + 工具面板接入 |
| T4 | 安全审计（非回环未认证拒绝/占位 token 警告/Origin/资源上限）、e2e、`docs/{en,zh}/serve.md` 与 changelog |

**范围：** 后端与前端分开交付；**不包含在 WebUI 布局 v1 内**；它是独立后端能力。

## 10.1 实施进度（2026-10-29）

已完成：

- T0 `internal/terminal`：Unix PTY manager、ring buffer、output limiter、attach/takeover/detach、process group close、完整 `os.Environ()` 继承、`err`/limit/state 单测。
- T1 Windows ConPTY：新增 `github.com/UserExistsError/conpty v0.1.4`，把平台层抽象为 `PTY` 接口（Read/Write/Resize/Wait/Pid），Unix 用 `creack/pty` 包装，Windows 用 ConPTY（尺寸、cwd、环境变量、退出码）；`Supported()` 按 `IsConPtyAvailable()` 返回。`GOOS=windows`、`darwin`、`freebsd` 交叉编译通过。
- T2 `internal/serve`：`terminal` 配置段、`EffectiveTerminalEnabled`、`featureStatus.terminal/terminalWarning`、始终注册的 `/api/terminals` 与 `/ws/terminals/`、auth/origin/token handshake、HTTP 创建/列表/删除、`Manager.UpdateConfig` 热配置、session 删除时 `CloseSession`。
- T3 `ui/src/components/terminal/TerminalPane.svelte`：xterm + FitAddon、create/attach/input/resize/replay、重连、cwd_changed 重建；已接入 Workbench 工具条底部终端 dock（`features.terminal===true` 时显示），绑定焦点会话 cwd；不做成 session tab。
- T4：`docs/{en,zh}/serve.md` 增加动态工作台、交互终端、`terminal` 配置与共享 token 安全说明；四份 changelog 已同步；`go test ./internal/terminal ./internal/serve ./internal/architecture` 通过。
- T4 Pdeathsig：Linux 通过 `procattr_linux.go` 给 PTY 子进程设置 `Pdeathsig=SIGKILL`，同时保留 creack/pty 的 `Setsid` 与 process-group kill；其他平台为空实现。单测验证 `Pdeathsig` 已设置且不会清除既有 `SysProcAttr`，并验证 Close 会关闭进程组。
- T1 Windows 验证入口：新增 `terminal_windows_test.go`，在真实 Windows 10 1809+/Windows 11 runner 上通过 `Manager` 覆盖 ConPTY start/input/resize/输出/exit，并覆盖重复 `Close` 的幂等性；ConPTY 不可用的旧系统会 skip（skip 不算通过证据）。`scripts/verify-conpty.ps1` 先断言当前主机是 Windows，再运行这两个测试，并在非零退出、出现 SKIP 或缺少任一 PASS 行时以非零退出码失败，因此不会把 skip 误报成通过。
- 测试：`go test ./internal/terminal ./internal/serve` 通过；覆盖 ring、env、per-session/global limits（不淘汰旧终端）、shutdown 幂等、resize 边界、takeover/replay、echo、Pdeathsig/进程组、effective 开关、origin、配置往返、create/list/delete、WebSocket attach/input/output 往返；`GOOS=windows/darwin/freebsd` 交叉编译通过（含测试二进制）。
- 前端 e2e：`ui/e2e/workbench.smoke.mjs` 覆盖工作台分屏/标签/预设/移动端/关闭开关，以及终端面板打开、初始 resize 帧、键盘输入（服务端收到输入帧并回显）、服务端断开后 1s 自动重连并收到新输出、宿主尺寸变化触发新 resize 帧、关闭后 xterm 实例销毁并发出 DELETE。
- 复盘修复（本轮）：`Config.MarshalJSON` 的 `rawConfig` 未包含 `terminal` 段，导致 WebUI/ACP 保存 `serve.json` 时静默丢失显式终端配置（`enabled: false`、shell、上限、allowedOrigins）。新增 `rawTerminalConfig` 映射与 Marshal 输出，并补充 Marshal/Decode 与 `SaveConfig`/`LoadConfigFrom` 往返测试（显式关闭、shell、上限、allowedOrigins）。
- 复盘修复（本轮）：终端关闭时的 `DELETE /api/terminals/{id}` 使用 `fetch(..., {keepalive:true})`，提高页面刷新/关闭时清理 detached 终端的概率；正常关闭路径仍由 e2e 断言收到 DELETE。
- 复检（2026-10-29）：`go test -count=1 ./internal/terminal ./internal/serve ./internal/architecture` 通过；`GOOS=windows GOARCH=amd64 go build ./...` 与 `GOOS=windows GOARCH=amd64 go test -c ./internal/terminal` 通过（仅证明可编译，不替代 ConPTY 实机）；`cd ui && npm test && npm run build && node e2e/workbench.smoke.mjs` 通过。
- 复盘修复（本轮）：修复 `internal/terminal` 的数据竞争与启动空指针窗口。`readLoop`/`waitLoop` 现在在创建时直接接收 PTY 句柄，`finish()` 仍在锁内清空 `t.pty`；新增 `Terminal.Size()` 并在列表、创建响应和 `attached` 控制帧中通过它读取几何尺寸，避免 HTTP 列表与并发 resize 直接读写 `Cols`/`Rows`；新增 `TestTerminalShortLivedExit` 反复覆盖 shell 立即退出的启动/结束路径。`go test -race -cpu=8 ./internal/terminal` 连续 8 次通过，`go test -race ./internal/terminal ./internal/serve` 通过。
- 复盘修复（本轮）：终端状态栏持续显示“终端不受沙箱保护，继承 serve 进程用户权限与环境变量”，并在 `features.terminalWarning=placeholder_token` 时显示占位 token 警告；`workbench.smoke.mjs` 对两条提示都有断言。
- 复盘修复（本轮）：`terminal.enabled=false`（或非回环认证被移除）现在会通过 `Manager.CloseAll()` 终止已有 shell，`terminalHandshake` 也会拒绝新的 WebSocket attach；POST 在创建 PTY 后会二次检查 effective 配置，避免配置切换的竞态窗口留下孤儿 shell。新增 `TestManagerCloseAllKeepsManagerUsable` 与 `TestTerminalHandshakeRejectsDisabledFeature`。
- 复盘修复（本轮）：工作台切换焦点会话时，`TerminalPane` 会释放旧会话的终端并为新焦点 cwd 重建；POST 在途的旧请求会自我关闭，陈旧 socket 不会覆盖新连接。终端 id 因 serve 重启失效后停止重试，由“重新连接”按钮创建新终端。e2e 新增焦点切换断言（旧终端 DELETE + 新会话 POST）。
- 复检（2026-10-29）：`go test -count=1 -race ./internal/terminal ./internal/serve`、`go test -count=1 ./internal/terminal ./internal/serve ./internal/architecture`、`GOOS=windows GOARCH=amd64 go build ./...` 与 `GOOS=windows GOARCH=amd64 go test -c ./internal/terminal` 通过；`cd ui && npm test && npm run build && node e2e/workbench.smoke.mjs` 通过；`go test ./...` 全仓库通过。
- 复盘修复（本轮）：关闭活动终端不再无条件等待 2 秒。Unix 现在先向进程组发 `SIGHUP`，通过 `Done()` 感知进程退出后立即返回，只有宽限期（2s）内未退出才升级为 `SIGKILL`；Windows 仍以关闭 ConPTY 作为终止手段。新增 `TestTerminalCloseIsPromptForIdleShell`（空闲 shell 必须远快于宽限期）与 `TestTerminalCloseEscalatesToKill`（忽略 HUP/TERM 的脚本必须被 SIGKILL），`DELETE` 与 `Shutdown` 不再逐个终端串行阻塞。
- 复盘修复（本轮）：`POST /api/terminals` 仅在 auth 关闭或请求携带 Bearer 凭据时返回 `terminalToken`；浏览器 HttpOnly cookie 会话不再在响应体里拿到长期 token（WebSocket 仍由 cookie + 同源 Origin 授权），并新增 `TestTerminalTokenExposure`。
- 复盘修复（本轮）：终端策略与配置回滚解耦。可逆的 limits/shell 立即应用（失败时恢复旧值），不可逆的 `CloseAll()` 只在 dispatcher/cron/platform 全部提交成功后再执行；此前若 `syncPlatformRuntime` 失败触发回滚，`CloseAll()` 已经杀掉了旧配置下的 shell。现在回滚路径不会产生不可逆的终端副作用。
- 复盘修复（本轮）：补齐 §6 生命周期审计。`Manager.SetLogger` 安装审计 sink，`terminal_created`/`terminal_exited`/`terminal_killed` 只记录 id/session/cwd/shell/exitCode/reason，不记录输入或输出；terminal 在创建时复制 sink，避免热更新丢失。新增 `TestManagerAuditLogsLifecycleWithoutOutput` 断言 created/exited 事件存在且输出文本未进入日志。
- 复盘修复（本轮）：补齐 §8 反压投影。`outputLimiter.wait` 返回是否真正等待，PTY read loop 在状态变化时通过 `Subscription.Control()` 发送 `backpressure` 控制消息，WebSocket 映射为 `{"type":"backpressure","active":...}`，`attached` 帧也携带当前 `throttled`；`TerminalPane` 状态栏显示本地化“终端输出受限（服务端反压）”，恢复后自动消失。新增 limiter 单测与 e2e 断言（出现/恢复），确保反压不会被误判为断线。
- 复盘修复（本轮）：补齐 §3/§6 的 shell 优先级。新增 `effectiveTerminalShell`：显式 `terminal.shell` 仅在绝对可执行文件或可解析 basename 时生效，否则回退 `(*Settings).GetShell()`，为空再回退平台默认；`startChannels` 与 `applyConfigUpdate` 共用该解析（不修改持久化配置），并新增 `TestEffectiveTerminalShell` 覆盖空值、绝对可执行/不可执行、缺失路径、相对路径与 PATH basename。
- 复盘修复（本轮）：WebSocket 二进制 resize 帧此前只校验非零，可绕过 HTTP 的尺寸上限。`Terminal.Resize`/`Manager.Create` 现在统一夹紧到 500x300（`MaxCols`/`MaxRows`），并扩展 resize 单测覆盖超大尺寸。
- 复盘修复（本轮）：补齐 §6/§7 的“与 Agent 共享 cwd”提示。`TerminalPane` 从 canonical `sessionRunStates.runtime.execution` 读取执行状态，始终显示“终端绑定当前会话目录，Agent 可能同时在此目录工作”，并在 `local`/`external`/`detached_remote`/`reserved`/恢复中等状态下升级为更强的本地或跨进程警告；严重程度由纯函数 `terminalAgentWarningKey` 判定并有单测。
- 复盘修复（本轮）：补上 xterm 样式导入，并将 `@xterm/xterm` 5.5.0、`@xterm/addon-fit` 0.10.0 在 `package.json` 与 lockfile 中改为精确 pin，满足 §8 的版本 pin 要求。
- 复盘修复（本轮）：刷新页面后终端不再泄漏并新建。`TerminalPane` 在 `sessionStorage` 记录 terminalId，重开面板时通过 `GET /api/terminals?sessionId=` 确认仍为 starting/running/detached 后 attach 同一终端；serve 重启或已退出则丢弃陈旧 id。e2e 新增页面刷新后重连且不新增 POST 的断言。
- 复盘修复（本轮）：进程已退出或终端已失效时，“重新连接”不再 attach 已死亡的 terminalId，而是创建新终端；收到 exit/closed 帧或 `terminal has exited` 错误会清理存储的 id。
- 复盘修复（本轮）：PTY read loop 在 EIO 早于 `cmd.Wait` 记录退出码时最多等待 500ms（`exitDone`），避免 WebSocket exit 帧把非零退出码误报为 0；新增 `TestTerminalReportsExitCode` 断言 `exit 7` 返回 7。
- 复盘修复（本轮）：补齐 §5 的文本 JSON 兼容帧：WebSocket 现在接受 `{"type":"input|resize|ping",...}`，浏览器主路径仍用二进制帧；`TestTerminalWebSocketRoundTrip` 增加文本 input/resize 往返与尺寸断言。
- 复盘修复（本轮）：Unix 关闭改为先发 `SIGHUP` 而不是 `SIGTERM`。交互式 dash/bash 在 steady state 会忽略 `SIGTERM`，原实现会让每次关闭空闲 shell 都耗尽 2s 宽限并触发 `TestTerminalCloseIsPromptForIdleShell` 抖动（在 CPU 负载下必现）。`SIGHUP` 是终端挂断的标准信号，空闲 shell 立即退出；忽略 HUP/TERM 的脚本仍在宽限期后升级 `SIGKILL`。测试现在先等待 shell 真正解析一条命令再计时，避免把启动期信号处置竞态误当作稳定行为。`go test -race -count=3 ./internal/terminal` 与 `go test -count=10 -run 'TestTerminalClose' ./internal/terminal` 通过。
- 复盘修复（本轮）：补齐 §6 的 Windows 终止语义。`conpty.Close()` 会关闭原始句柄且不可重复调用，而终端关闭路径会从 `requestStop`、`CloseWithReason` 和 `finish` 多次调用 `PTY.Close()`；Windows 包装现在用 `sync.Once` 保证只关闭一次，避免二次 `CloseHandle` 误关已被复用的句柄。创建 shell 时还会尝试把它加入 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 的 Job Object，使关闭终端时根 shell 遗留的后台子进程也一并终止；若 `OpenProcess`/`AssignProcessToJobObject` 被宿主 job 拒绝则安全降级为仅关闭 ConPTY，不影响终端创建。上述改动已通过 `GOOS=windows` 编译与测试二进制交叉编译，但 Job Object 的实际终止行为仍需 Windows 实机验证。

- 提交前复检（2026-10-29）：`go test ./...`、`cd ui && npm test`（209 tests）、`npm run build`、`node e2e/workbench.smoke.mjs` 均通过；本地无真实 Windows runner，ConPTY 实机结果仍待外部证据。
- 复盘修复（2026-10-30）：不支持平台现在返回文档规定的 `501 terminal_unsupported`，而不是误报 `403 terminal_disabled`；映射由 `terminalUnavailableStatus` 统一，并新增 `TestTerminalUnavailableStatus`。`pty_unix.go` 的 build tag 从 `!windows` 收敛为 creack/pty 支持的显式平台集合，与 `pty_unsupported.go` 互补；`GOOS=aix` 现在走 unsupported 实现（`Supported()==false`）而不是在依赖编译阶段失败，`GOOS=linux/windows/darwin/freebsd` 均通过。
- 复盘修复（2026-10-30）：澄清 `cwd_changed` 契约。serve 没有 session cwd 变更入口，后端不会主动发送该帧；前端保留处理，未来新增 cwd 变更时必须调用 `CloseSession` 并以 `cwd_changed` 关闭旧终端。
- 复检（2026-10-30）：`go test -count=1 ./internal/terminal ./internal/serve ./internal/architecture`、`GOOS=windows/darwin/freebsd go build ./...`、`npm test`（209）、`npm run build`、三个 e2e smoke 通过；并联手修复了 `TestCmdESMLifecycle` 的偶发失败（ESM 写入事务化 + orphaned 执行快照的控制放行），使全仓 `go test -count=1 ./...` 稳定通过。

未完成 / 后续：

- T1 还需要在真实 Windows runner 上执行 `pwsh -File scripts/verify-conpty.ps1`（内部运行 `go test ./internal/terminal -run '^(TestConPTYRoundTrip|TestConPTYCloseIsIdempotent)$' -count=1 -v`，并在 skip、非零退出或缺少 PASS 行时失败）；本仓库当前只有该测试与交叉编译证据，不能替代 Windows 实机结果。skip 不算通过证据。
- 非浏览器 token 已实现 query/`Sec-WebSocket-Protocol` 校验；独立 CLI 客户端不是 T4 验收项，暂不新增。


## 11. 明确拒绝的做法

- 不要用 `bash` 工具 + `exec.Command` + pipes 假装 PTY：`isatty` 为 false，vim/top/ssh 等交互程序不可用，resize 无效。
- 不要在 `openaiapi.Server` 里直接 `exec.Command`，绕过 `channelRuntime` 的配置/鉴权/上限。
- 不要把 terminal id 写进 session DB 或 transcript；不要持久化输出。
- 不要允许客户端传 cwd/shell/command；不要为终端开匿名 WebSocket。
- 不要把“已配置 auth”误解为“任何人都无限制”；auth 是访问边界，但共享 token 意味着持有 token 即拥有 shell。把这个事实写进部署文档，不要因此阻止已认证用户使用终端。
- 不要在 auth 已配置的情况下再加第二道 `terminal.enabled=true` 门槛；只有未认证的非回环监听必须拒绝。
- 不要用 allowlist/denylist 裁剪 PTY 环境：筛不掉用户可读的凭据文件，只会破坏代理/SSH/云 CLI；需要隔离就用独立 OS 用户/容器。
- 不要为 detached 终端设置自动 idle kill；只在 serve 退出、显式关闭、session 删除或 cwd 变更时终止。
- 不要在 `MaxSessions` 满时静默 LRU 淘汰终端；返回 409，让用户显式关闭旧终端。
- 不要在 `normalize()` 里把 `Enabled=nil` 冻结成 bool；effective 判断必须按当前 listen/auth 动态计算。
- 不要在沙箱开启时静默允许终端；必须持续在 UI 标明“终端不受沙箱保护”，不要用 PTY 输出冒充 sandbox 内结果。
- 不要用 PTY 输出推断 Agent 工具结果；两者是不同数据源。

