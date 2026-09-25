# Git Worktree 隔离工作区方案

> Date: 2026-09-25 · Status: Proposal · Owner: Agent Runtime / Session / Desktop
> Related: `docs/proposal/agent-core-runtime-unification-proposal.md`, `docs/proposal/webui-project-session-management-proposal.md`, `docs/proposal/runtime-workspace-input-materialization-proposal.md`, `AGENTS.md`
> Reference implementation: `/home/free/src/opencode`（`packages/opencode/src/worktree/index.ts`、`.../control-plane/adapters/worktree.ts`、`packages/app/src/utils/worktree.ts`）

## 实现进度

- **P0 已完成**：
  - `internal/worktree`：git 机制（Plan/Add/Populate/List/Remove/Reset/DefaultBranch + porcelain 解析 + slug/repoKey），纯机制、无 DB。
  - migration 44 `create_worktrees`；`internal/dao/worktrees.go`（SQL owner）；`internal/session/worktrees.go`（域 API + `Worktree` 结构）。
  - `internal/agentruntime/worktrees.go`：`WorktreeManager`（同步落盘+授权、异步填充、事件 sink、读时对账、external 只读、按仓库/目录锁）。
  - `cmd/mothx worktree list|create|remove|reset`（薄投影）。
  - 单测/集成测试通过；`go build ./...`、`go test ./internal/architecture` 通过。
- **P1 已完成**：
  - ACP additive 方法族 `mothx/worktree/list|create|remove|reset` + 能力键 `worktrees`/`worktreeReset` + `mothx/worktree/status` 事件投影。
  - `resolveWorkspace` 扩展为三类来源（协商 cwd + 客户端 additional + Runtime-granted worktree，worktree 不占 16 名额）；initialize 时从注册表在窗口内重新授权。
  - 运行中保护：worktree 上有活跃 run 时默认拒绝删除/重置，`force` 先 `CancelDurable`。
  - Desktop：主进程转发 `mothx/worktree/status`；`renderer/src/core/worktrees.ts`（能力门控 + 就绪等待 + 生命周期动作）；composer 增加「隔离工作区」按钮；i18n 双语。`tsc --noEmit`、renderer/main 单测（195）全通过。
- **P2 已完成**：
  - `internal/worktree` reset：`clean -ffdx` 的 failed-remove prune 重试 + `.gitmodules` 存在时的 submodule update/foreach reset/clean。
  - 启动脚本：经**沙箱策略**执行（`sandbox.NewManagerWithOptions` + 解析后的 level），不裸 `bash -lc`；创建与重置后执行；失败则 `status=failed`。默认无脚本即不执行。
  - `settings.json` 新增 `worktree` 段（`enabled`/`branchPrefix`/`startCommand`）+ 访问器；CLI/ACP 读取并门控创建。
- **P3 已完成（核心）**：
  - 子 agent 工作区继承修正：工具执行时注入工作目录，`subagent_spawn`/`delegate_subagent` 未传 `work_dir` 时继承父工作区（不再落到 `os.Getwd()`）。
  - 按需 worktree 三触发点：① 工具参数 `worktree: "true"|<name>`；② 成员声明 `worktree: true`（与固定 `work_dir` 冲突时在 bundle 校验和 spawn 两处报错）；③ `worktree.perChildSubagents` 策略。
  - 边界：`internal/agent` 不 import `agentruntime`；`WorktreeProvider` 接口 + `SetWorktreeProvider`，`AgentOptions.Worktree`（`WorktreeSpec{Name,Reuse}`），`AgentManager.Create` 在取锁前解析并替换 `WorkDir`；`agentruntime.WorktreeProviderAdapter` 实现并在 ACP 两个 `NewAgentManager` 处安装。
  - 共享 primitive：`WorktreeManager.ResolveOrCreate` + `WorktreeSpec.Reuse`，供一个工作流（如 ESM objective）复用同一 worktree。ESM 三角色本已共用 objective 的 `WorkDir`，已满足共享语义；适配器可选用 `Reuse` 显式物化。
- **P4 已完成**：
  - 跨进程文件锁：`internal/worktree/lock.go`（O_CREATE|O_EXCL + 过期回收），在填充/删除/重置时与进程内锁一起获取。
  - Desktop 列表/重置/删除：composer 的「隔离工作区」按钮改为弹出菜单（`WorktreeMenu`），列出仓库 worktree 并提供新建/重置/删除（二次确认）；external 项只读。
- **P4 可选跟进（已完成）**：ESM 适配器显式物化 per-objective worktree——serve（webui）与 tui 的 ESM role runner 请求 `WorktreeSpec{Name: "esm-<sessionID>", Reuse: true, Optional: true}`，由 `worktree.esm`（默认 false）开启；`WorktreeSpec.Optional` 保证非 git 目录降级到会话工作区。新增 `agentruntime.NewDefaultWorktreeManager(settings)` 单一构造入口，serve/tui/CLI 的 `NewAgentManager` 与 ACP 一致安装 provider。
- **review 收尾**：ACP 能力键 `worktrees`/`worktreeReset` 改为由 `worktree.enabled` 决定是否广播——`enabled=false` 时 Desktop 隐藏控件，而不是让客户端发现一个会被拒绝的方法；此时 list/remove/reset 仍可用。
- 已落地但与方案有出入的点：Desktop 的 worktree 列表在 composer 弹出菜单而非侧栏分组。

## 0. 决策摘要

参考 opencode 的 worktree 能力，MothX 增加**「Git Worktree 隔离工作区」**：以一个 Git 仓库的主工作树为基准，创建、列出、重置、删除相互隔离的 worktree 目录，并把某个 worktree 目录作为某个 session 的工作目录（cwd）使用。这样多个 session/任务可以在同一仓库上并行工作而互不覆盖对方的改动。

核心决策：

- **不是新语义，而是新资源。** worktree 不是新的 Agent mode、不是新的 session 类型、不是新的 run 状态机、不是第二套 workspace 授权机制，也不是新的 Agent Core 分支。它只是「一个被 Runtime 授权的工作目录」，完全复用现有 `cwd` + 工作区窗口、`SessionRuntime`、`ExecutionRuntime`、`RunStore`、`DecisionService` 与 DAO 边界。
- **worktree 是 Runtime-owned 一等资源，不是 cache。** 每个 worktree 拥有稳定 `id`、归属（repositoryRoot / project）、生命周期状态与授权身份，由 `sessions.db` 持久化。它**不是** git 的临时投影：**Runtime 注册表是「身份 / 归属 / 授权」的权威，git 是「内容」的权威**（存在性、分支、干净度）。二者分工，各自唯一，读时对账。
- **一个 owner 一处实现。** Git 机制在 `internal/worktree`，SQL 在 `internal/dao`，session 域 API 在 `internal/session`，编排/授权/事件在 `internal/agentruntime`（`WorktreeManager`）；ACP、CLI、TUI、WebUI、Desktop 只做协议投影与渲染。禁止任何 adapter 自行实现 git worktree 命令、目录授权或生命周期。
- **授权 resolver 只有一个，但来源分三类。** 授权判定为 `negotiatedCwd ∪ clientAdditionalDirectories(≤16) ∪ registeredWorktrees(无上限)`。worktree 是 **Runtime 授予**的授权根，独立成类，**不占用客户端请求的 `additionalDirectories` 名额**；重启后 Runtime 依据注册表**显式**重新授权，而不是靠路径启发式推断。
- **创建分两段：同步落盘+授权，异步填充。** 同步阶段 `git worktree add`（目录已存在）+ 注册 + 授权；异步阶段 `git reset --hard` 填充 + 启动脚本 + `ready`/`failed` 事件。原因是 ACP 分发循环单线程（慢操作会阻塞整个协议循环），而授权必须同步（`resolveWorkspace` 要求目录已存在，且客户端可能创建后立即 `session/new`）。
- **默认落在数据目录，不污染仓库。** worktree 目录固定位于 `platform.DataDir()/worktrees/<repo-name>-<hash8>/<name>`，不写进用户仓库工作树，避免被 `git status` 污染、避免递归 worktree。
- **多 agent 模式按需开启（可组合）。** 主角团 / 委托 / 多 agent / ESM 的子 agent **默认继承父工作区**；可在 spawn 参数、成员声明或执行策略层**按需**为某个工作流派生独立 worktree。它与 worktree 能力正交组合，**不新增表/实体/列**；ESM 的 worker/critic/audit 共享同一 objective 的 worktree。
- **删除必须可证明是 worktree。** 删除前必须验证目标目录是**该仓库已注册的 worktree**（`git worktree list --porcelain` + 规范化路径比对），绝不递归删除任意用户目录。

本方案与 opencode 的关键差异（MothX 特有约束）：

| 维度 | opencode | MothX 方案 |
| --- | --- | --- |
| project 语义 | project 就是 Git 仓库目录，worktree 是它的 sandbox | project 是**逻辑标签**（`projects` 表：id+name），session 才有 `cwd`；worktree 归属于**仓库根目录**，可关联到 project |
| 授权模型 | 无显式窗口，`addSandbox` 记录路径 | 复用 `resolveWorkspace` 协商窗口并扩展为「三类来源 union」，worktree 为 Runtime-granted 且不吃客户端名额 |
| 资源身份 | 目录即身份（git 为准） | Runtime-owned durable 资源：稳定 `id` + 归属 + 状态；git 只作内容权威 |
| 前端 | SolidStart 单体 | Desktop 是**纯 ACP**（`mothx/manage/*` + 能力键），不得直连文件系统/DB |
| 数据库边界 | 无强制 | DAO-only SQL，schema 走 migration 追加 |

## 1. 背景与问题定义

MothX 的 session 以一个 `cwd`（工作目录）为根：工具、沙箱、context/skills/rules、MCP、additional directories 都绑定到这个目录。当前一个仓库通常只有一个工作树，因此：

1. 多个 session/任务在同一仓库并行时，会互相覆盖工作树中的改动；Agent 的编辑、构建产物、`git` 操作彼此干扰。
2. 没有隔离的「试错空间」：想让 Agent 在独立分支上做一次实验，必须手动 `git worktree add`、切目录、再新建 session，体验割裂。
3. Desktop 的「新会话目录」选择器只能选主工作树或其它已存在目录，无法一键派生一个干净隔离工作区。

opencode 的做法是：把 worktree 作为项目的 sandbox，创建时 `git worktree add --no-checkout` 后 `git reset --hard` 填充，注册为可授权目录，新会话直接选它作为目录，并提供 reset/remove/启动脚本与 `worktree.ready`/`worktree.failed` 事件。MothX 需要同一类能力，但必须落在「一个 Agent Core、一个 front-end-neutral Runtime、薄 adapter」和 DAO-only 的既有边界内。

## 2. 目标、非目标与名词

### 2.1 目标

- 支持对任意已授权的 Git 仓库根创建 worktree：自动生成唯一名称与分支（默认 `<prefix>/<name>`，可 detached），在数据目录落地，填充为干净工作树。
- 支持列出（按仓库根、按 project 过滤）、删除、重置到默认分支。
- 把 worktree 目录作为 session 的正常 cwd：新会话可直接以 worktree 目录创建，已有 idle 会话可通过现有 `mothx/session/setWorkDir` 迁入。
- worktree 作为 Runtime-owned 一等资源：稳定 id、归属、生命周期、授权身份由 Runtime/DAO 拥有；创建/删除/重置并发安全、可恢复；对 Desktop/CLI/TUI/WebUI 只暴露协议投影。
- 创建/填充过程异步化并发出 canonical 事件（ready/failed），前端可等待就绪后再发送首条 prompt（对齐 opencode 的 `Worktree.wait`）。
- 支持多 agent 模式（主角团 / 委托 / 多 agent / ESM）按需为其工作流派生隔离 worktree：子 agent 默认继承父工作区，显式请求时独占 worktree（组合现有 worktree 能力，不新增持久化）。
- 可选：为 worktree 运行「启动脚本」（如 `bun install`），失败只影响 worktree 状态，不影响主流程。

### 2.2 非目标

- 不把 worktree 做成安全沙箱或权限边界；沙箱、allow 规则、高风险命令保护、git 保护仍是独立策略，worktree 只是普通目录。
- 不引入新的 Agent mode、session 类型、run 状态机、workspace 授权机制或事件语义。
- 不引入通用「Workspace」超类型或 materializer 插件框架；本方案只建模 git worktree（避免投机抽象）。
- 不做 worktree 之间的自动合并 / PR / rebase；worktree 产出分支，是否合并由用户或 lead 决定（合并能力不在本方案内）。
- 首版不支持：远程仓库 worktree、跨仓库共享、submodule/partial clone 的完整语义、worktree 的 PR/合并自动化。
- 不让 Desktop renderer、Electron 主进程或 CLI 直接执行 git worktree 命令、直接读写 `sessions.db`、或自行授权目录。
- 不把 project 与仓库目录强制绑定（沿用现有「project 是逻辑标签」的模型）。
- 不自动删除用户数据：删除 worktree 只删除由本方案创建并登记、且能被 git 证明的 worktree 目录与其分支。

### 2.3 名词

| 名词 | 含义 |
| --- | --- |
| Worktree | Runtime-owned 一等资源：一个由本方案在数据目录创建、隶属于某 Git 仓库的独立工作树目录，可被授权为 session 的 cwd。 |
| 仓库根（repositoryRoot） | 用户仓库的**主工作树**绝对路径；worktree 由它派生。 |
| 注册表（registry） | `sessions.db` 中 Runtime-owned 的 worktree 表；**身份/归属/授权**权威。 |
| 内容权威 | git：worktree 的存在性、分支、干净度以 `git worktree list --porcelain` 为准。 |
| 授权窗口（workspace window） | `resolveWorkspace` 的判定集合：协商 cwd + 客户端 additional directories + Runtime-granted registered worktrees。 |
| 启动脚本（startCommand） | 创建/重置 worktree 后可选执行的一条命令，用于安装依赖等；经既有 sandbox/审批路径执行。 |
| repoKey | 仓库根路径的可读前缀 + 短哈希，用于在数据目录下隔离不同仓库的 worktree。 |

## 3. 总体架构

### 3.1 一条数据流

```text
Desktop / CLI / TUI / WebUI
  └─ ACP mothx/worktree/*（或 CLI 子命令） ── 薄投影
                     │
        internal/agentruntime/worktrees.go   ← WorktreeManager：编排/授权/事件/会话绑定
             │                │
             │                └─ 授权：registered worktree 作为 Runtime-granted 根
             │                   （resolveWorkspace 三类来源 union，不吃客户端名额）
             │
        internal/session/worktrees.go        ← session 域 API（读写注册表）
             │                │
        internal/dao/worktrees.go            ← 唯一 SQL owner
             │
        internal/worktree                    ← 唯一 git worktree 机制 owner
             └─ git worktree add/list/remove + reset/clean + startCommand
                     │
              DataDir()/worktrees/<repo-key>/<name>   （git 为内容权威）
```

约束：Desktop 只投影状态与来源卡片；它不执行 git、不读注册表 SQL、不自行授权目录。CLI/TUI 与 ACP 复用同一个 `internal/agentruntime` 入口。

### 3.2 包与 owner 划分

| 包/文件 | 唯一职责 | 明确不做 |
| --- | --- | --- |
| `internal/worktree/*.go` | git worktree 机制：名称/slug 生成、`add --no-checkout` + `reset --hard` 填充、`list --porcelain` 解析、`remove --force` + 分支删除、`reset`（fetch 默认分支 + reset/clean）、prune/sweep、启动脚本执行 | 不碰 SQL、不碰 session/Runtime、不构造 provider、不授权目录 |
| `internal/dao/worktrees.go` | 注册表的全部 SQL（CRUD、按仓库/项目查询、状态与对账更新） | 不含业务编排、不执行 git |
| `internal/session/worktrees.go` | session 域 API：注册表读写、与 project/session 关联、对外结构体 | 不执行 git、不授权窗口 |
| `internal/agentruntime/worktrees.go` | `WorktreeManager`：解析仓库根、调用 `internal/worktree`、写注册表、授权（三类来源）、发出 canonical 事件、与会话 cwd 绑定；实现注入 `AgentManager` 的 worktree resolver | 不实现 git 命令细节、不直接写 SQL |
| `internal/agent`（`AgentManager` + subagent/delegate 工具） | 子 agent 工作区解析：worktree resolver 接口（定义于此、由 agentruntime 实现）、`AgentOptions` 的 worktree 请求、成员声明的 worktree 映射；默认继承父工作区 | 不执行 git、不物化 worktree、不拥有授权 |
| `internal/acp`（`mothx/worktree/*`） | 协议投影、能力键、参数校验、错误映射、事件投影 | 不拥有 worktree 生命周期 |
| `cmd/mothx` | CLI 子命令薄包装 | 不重复生命周期 |
| `desktop/` | renderer UI + 主进程目录选择 IPC | 不直连文件系统/DB，不自行授权 |

`internal/worktree` 定位类似 `internal/sandbox`/`internal/mcp`：一个专注、可单测的机制包，不引入新的 Runtime。编排仍集中在 `internal/agentruntime`（`WorktreeManager`），符合「复杂度只在一个共享 Runtime 增长」的不变量。

### 3.3 worktree 目录布局

```text
<DataDir>/worktrees/
  <repo-name>-<hash8>/            # repoKey：可读前缀 + 仓库根路径短哈希，可浏览、可判重、碰撞安全
    <name>/                       # worktree 工作树目录（git worktree add 目标）
```

- 选数据目录的原因：不污染用户仓库、避免 worktree 内再嵌套 worktree、便于统一清理与权限隔离。
- 目录固定，**不提供 `worktree.root` 覆盖**：可覆盖根会引入「授权根是任意路径」的特例，破坏「Runtime-granted 精确匹配」的干净授权模型。
- 目录名与分支名：`name` 默认由用户输入 slug 化，或由仓库名 + 短随机后缀生成；分支默认 `<prefix>/<name>`（前缀可配置，默认 `mothx`），也支持 detached（`--detach`）。
- 唯一性：名称与分支任一冲突即重新生成后缀，最多 26 次后报 `name_generation_failed`（对齐 opencode 的 `MAX_NAME_ATTEMPTS`）。

## 4. 数据模型

### 4.1 注册表（`sessions.db`，migration 追加）

新增一张表（version 44，追加在现有 migration 末尾，不改动既有表/字段语义）：

```sql
CREATE TABLE IF NOT EXISTS worktrees (
  id              TEXT PRIMARY KEY,       -- 稳定身份，对外引用一律用 id
  repository_root TEXT NOT NULL,          -- 主工作树绝对路径（规范化）
  directory       TEXT NOT NULL,          -- worktree 工作树绝对路径（规范化）
  name            TEXT NOT NULL,
  branch          TEXT,                   -- NULL 表示 detached
  project_id      TEXT REFERENCES projects(id) ON DELETE SET NULL,
  start_command   TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL DEFAULT 'pending',  -- pending|ready|failed|removed
  error           TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_worktrees_directory ON worktrees(directory);
CREATE INDEX IF NOT EXISTS idx_worktrees_repo ON worktrees(repository_root);
CREATE INDEX IF NOT EXISTS idx_worktrees_project ON worktrees(project_id, updated_at);
```

要点：

- 注册表是**身份/归属/授权权威**，不是 git 的 cache：`id`、`project_id`、`start_command`、`status`、`error` 都只有注册表知道；git 只提供内容事实。
- `directory` 唯一，防止同一目录被登记两次；对外引用一律用 `id`，不用路径（避免 Windows 大小写/移动/重命名带来的脆性）。
- **对账**（读时）：以 `git worktree list --porcelain` 为准修正内容状态——
  - 注册行在 git 中已消失 → 置 `removed`；
  - git 中存在但未注册（用户手工创建）→ 作为 **external** 只读项列出（无 MothX 元数据、无 id、不可被本方案管理）；
  - 仓库根失效（目录被删/移动/不再是 git 仓库）→ 该仓库的注册行置 stale（`removed`），但**不**让整次 list 失败。
- 不为「哪个子 agent / objective 用了它」建列：worktree 与工作流的归属是**瞬态**的（由 cwd 推导，或 adapter 内存投影），不引入新实体。
- `status` 是**资源生命周期**状态，不是 run 状态机；不参与 `ExecutionRuntime` 的 run 生命周期。
- 项目关联沿用现有 `session_metadata.project_id` 语义：`ON DELETE SET NULL`，删除 project 不删除 worktree。

### 4.2 对外结构体

```go
type Worktree struct {
    ID             string `json:"id"`
    Name           string `json:"name"`
    Branch         string `json:"branch,omitempty"`   // 空 = detached
    Directory      string `json:"directory"`
    RepositoryRoot string `json:"repositoryRoot"`
    ProjectID      string `json:"projectId,omitempty"`
    Status         string `json:"status"`             // pending|ready|failed|removed
    Error          string `json:"error,omitempty"`
    External       bool   `json:"external,omitempty"` // 未注册的 git worktree，只读
    CreatedAt      time.Time `json:"createdAt"`
    UpdatedAt      time.Time `json:"updatedAt"`
}
```

### 4.3 配置（`settings.json` 可选新增，语义为新增而非修改）

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `worktree.enabled` | `true` | 是否允许创建 worktree（关闭后仅可列出/删除已有）。 |
| `worktree.branchPrefix` | `"mothx"` | 默认分支前缀。 |
| `worktree.startCommand` | `""` | 全局默认启动脚本；worktree 级 `startCommand` 覆盖它。默认无脚本即不执行。 |
| `worktree.perChildSubagents` | `false` | 每个子 agent 都独占一个 worktree（策略驱动触发点）。 |
| `worktree.esm` | `false` | ESM 按目标共享一个 worktree；非 git 目录自动回退到会话工作区。 |

所有字段缺省即用默认值，不改变任何既有字段含义；旧 `settings.json` 可直接加载。**不提供 `worktree.root`**（见 3.3）。

## 5. 生命周期与状态机

### 5.1 创建（同步落盘+授权，异步填充）

```text
create(baseCwd, name?, detached?, startCommand?, projectId?)
  同步阶段（必须在 ACP 单线程循环内快速返回，可回滚）：
    1. 解析 repositoryRoot：git -C baseCwd rev-parse --show-toplevel；失败 → not_git
    2. 校验 repositoryRoot 在授权窗口内（否则 unauthorized）
    3. 生成唯一 name/directory/branch（数据目录下）
    4. git worktree add --no-checkout (-b branch | --detach) <directory>  → 目录落地
    5. 写注册表行 status=pending；把 directory 登记为 Runtime-granted 授权根
    6. 返回 Worktree 投影（pending）
  异步阶段（goroutine，避免阻塞协议循环）：
    7. git reset --hard 填充工作树（失败 → status=failed + worktree.failed 事件）
    8. 运行 startCommand（可选，经 sandbox 策略执行；失败则 status=failed + failed 事件）
    9. status=ready，发 worktree.ready 事件
```

- 授权在**同步阶段**完成：`resolveWorkspace` 要求目录已存在（`EvalSymlinks`+`Stat`），且客户端可能创建后立刻 `session/new`。异步阶段只做填充与启动脚本。
- 填充失败时目录可能残留：置 `failed` 并保留目录供人工检查/重置/删除。
- 创建按**仓库根**串行（每仓库一把进程内锁，复用 `internal/session` 的 `lockRegistry` 思路），避免 git index/refs 竞争。

### 5.2 重置（reset）

```text
reset(id | directory)
  1. 定位注册行并校验为已登记 worktree 且非主工作树
  2. 解析默认分支（origin/HEAD → main/master 回退），必要时 fetch
  3. git reset --hard <base>
  4. 清理：git clean -ffdx（含 failed-remove 目录的 prune 重试）
  5. 子模块：存在 .gitmodules 时 submodule update --init --recursive --force + foreach reset/clean
  6. git status --porcelain 必须为空，否则 reset_failed（不静默丢改动）
  7. 可选重跑 startCommand（经 sandbox），发事件
```

- 重置会**丢弃** worktree 内的本地改动，前端必须二次确认。
- 重置拒绝主工作树、拒绝非本仓库登记的目录。

### 5.3 删除（remove）

```text
remove(id | directory, force?)
  1. 定位注册行；无则按 directory 直接查 git（external 项亦可删）
  2. 校验目录属于该仓库的已注册 worktree（git worktree list 规范化比对）
  3. 若该目录上有运行中的 session/durable run：
       - 默认拒绝（worktree_in_use）
       - force=true 时先取消/关闭相关 run 再删
  4. 关闭绑定该目录的 SessionRuntime（释放资源）
  5. git worktree remove --force <directory>
  6. 目录残留则安全清理（仅限已证明的 worktree 路径）
  7. 删除分支 git branch -D <branch>（detached 跳过）
  8. 注册行置 removed，发 worktree.removed 事件
```

- 删除**绝不**递归删除未通过 git 证明的任意目录。
- 删除分支失败不阻断目录删除，但要回报 warning。

### 5.4 状态模型

`pending | ready | failed | removed` 是 **worktree 资源生命周期**，与 run 状态机无关：

| 状态 | 含义 | 转移 |
| --- | --- | --- |
| pending | 目录已落地并授权，填充进行中 | → ready / failed |
| ready | 填充完成，可正常作为 session cwd | → removed |
| failed | 填充/启动脚本失败，目录可能残留 | → ready（重试/reset）/ removed |
| removed | 已删除（或 git 中消失被对账） | 终态 |

### 5.5 事件

canonical 事件由 `internal/agentruntime` 拥有的事件 sink 发出（对齐 `run_event.go` 的 `RunEventSink` 模式），adapter 只投影：

| 事件 | 载荷 | 说明 |
| --- | --- | --- |
| `worktree.pending` | id, name, directory | 创建开始（已落盘+授权） |
| `worktree.ready` | id, name, branch?, directory | 填充完成、可正常使用 |
| `worktree.failed` | id, directory, message | 创建/填充/启动脚本失败 |
| `worktree.removed` | id, directory | 删除完成 |

- ACP 侧投影为 `mothx/worktree/status` 通知，并保留 capability 键 `worktrees`。
- 异步 goroutine 通过线程安全的 notify 路径发送事件。
- 事件不进入 run 终态语义；worktree 状态与 run 状态互不耦合。

## 6. 与 Runtime / Session 的集成

### 6.1 授权窗口（三类来源）

授权 resolver 只有一个（延续 `resolveWorkspace`），但判定来源分三类：

```text
authorized(cwd) = negotiatedCwd
                ∪ clientAdditionalDirectories   (客户端请求，≤16，语义与上限不变)
                ∪ registeredWorktrees           (Runtime 授予，无上限，精确匹配)
```

- worktree 是 **Runtime-granted** 的授权根：由 `WorktreeManager` 在创建时登记，**不占用客户端 `additionalDirectories` 名额**，也不会出现「窗口满所以不能建 worktree」的失败模式。
- 采用**精确匹配**（与现有 additional 语义一致），不做前缀包含：因为目录固定但语义上仍以「精确的、被证明的 worktree 目录」为授权单位，避免误放行子目录。
- **重启后重新授权**：Runtime 依据注册表**显式**重新授权 registered worktree（不是反推 `.git` 的启发式推断）。加载一个 cwd 指向 worktree 的持久化 session 时，该 cwd 因已注册而被放行。
- 之后：
  - 新会话：`session/new { cwd: worktree.Directory }`（已授权，`resolveWorkspace` 放行）。
  - 已有 idle 会话：`mothx/session/setWorkDir { sessionId, cwd: worktree.Directory }`，由现有逻辑关闭并重建 SessionRuntime。
- **Desktop 不得**把未注册目录直接塞进 `session/new`；否则 `resolveWorkspace` 以 `cwd is outside the negotiated workspace` 拒绝。

### 6.2 session 绑定

- session 与 worktree 的关系**只通过 `cwd` 表达**，不新增 session 字段、不改 `sessions`/`sub_session` schema。
- 需要「某会话是否在 worktree 中」时，用 `cwd` 反查注册表。
- project 关联：worktree 可绑定 `project_id`，与 session 的 `project_id` 相互独立；Desktop 按 project 分组时，worktree 作为 project 下的「工作区」展示。

### 6.3 沙箱与 git 保护

- worktree 目录是普通 cwd，沙箱的 `allowedRead/allowedWrite/deniedPaths` 与 `protectGit` 照常生效。
- worktree 的 `.git` 是**文件**（`gitdir: <repo>/.git/worktrees/<name>`），`internal/sandbox` 已有 `protectedGitPaths` 处理该情形；worktree 创建/删除涉及的 git 操作由 Runtime 以受控方式执行，不走工具沙箱。
- 启动脚本经既有 sandbox/审批路径执行，不裸 `bash -lc`；默认关闭。
- worktree 创建不绕过任何 allow 规则或高风险命令保护。

### 6.4 多 agent 模式的按需 worktree（可组合，不新增实体）

worktree 与「带 subagent 的多 agent 模式」是**正交、可组合**的：不为多 agent 新增表、实体或列，也不新增第二套生命周期。当某个子 agent 需要隔离时，Runtime 复用它已有的 worktree 能力——同一个 `WorktreeManager`、同一张 `worktrees` 表、同一套授权与事件——只把产出的目录作为该子 agent 的 WorkDir。

- **默认继承（先修一个既有缺口）**：子 agent 默认继承父工作区（父 session 的 cwd，本身可能就是一个 worktree）。这也修正了当前 `subagent_spawn` / `delegate_subagent` 不传 `work_dir` 时落到 `os.Getwd()` 的问题；需兼容说明与测试覆盖。
- **按需开启（三个触发点，Runtime 解析一次）**：
  1. 模型驱动：`subagent_spawn(..., worktree: true|<name>)` / `delegate_subagent(..., worktree: true)`；
  2. 成员声明：expert-team 成员 bundle 可声明 `worktree: true`（与固定 `WorkDir` 同时声明视为冲突并报错）；
  3. 策略驱动：`settings.json` 的 `worktree.perChildSubagents`（默认 false）使每个子 agent 独占一个 worktree。
- **隔离单位**：委托 / 主角团 / 多 agent 默认继承、按需独占；ESM 按 objective 共享一个 worktree（worker/critic/audit 必须读到同一份改动），由 `worktree.esm` 开启，非 git 目录自动回退。
- **无新持久化**：不记录 worktree↔agent/objective 绑定，归属是**瞬态**的——由子 agent 的 WorkDir/cwd 推导，或由 adapter 在内存里投影到 UI；清理策略是运行期策略，不落列。
- **边界**：`internal/agent` 不 import `internal/agentruntime`，通过注入的 worktree resolver 接口（`SetWorktreeProvider`，对齐 `SetMemberContext`）解析；`AgentOptions` 带 worktree 请求，`AgentManager.Create` 在 `factory.Create` 前解析它并设置 `opts.WorkDir`；spawn 工具只传请求、绝不碰 git；授权仍走 Runtime-granted 根；ESM 不引入角色级硬超时。

## 7. ACP 协议投影

新增 additive 方法族 `mothx/worktree/*`（保持 ACP v1，能力键 `worktrees`）：

| 方法 | 参数 | 结果 |
| --- | --- | --- |
| `mothx/worktree/list` | `{ repositoryRoot?, projectId? }` | `{ worktrees: Worktree[] }`（含 external 只读项） |
| `mothx/worktree/create` | `{ baseCwd, name?, detached?, startCommand?, projectId? }` | `{ worktree: Worktree }`（初始多为 pending） |
| `mothx/worktree/remove` | `{ id? , directory?, force? }` | `{ removed: bool }` |
| `mothx/worktree/reset` | `{ id?, directory? }` | `{ reset: bool }` |
| 通知 `mothx/worktree/status` | `{ worktree: Worktree }` | 进度投影（pending/ready/failed/removed） |

- 错误映射沿用结构化错误码：`not_git`、`unauthorized`、`name_generation_failed`、`create_failed`、`start_command_failed`、`remove_failed`、`reset_failed`、`list_failed`、`worktree_in_use`。
- 参数校验与路径规范化（`filepath.Clean` + `EvalSymlinks`）在 ACP 层做**输入合法性**检查；真正的授权与生命周期在 `internal/agentruntime`。
- `initialize._meta.mothx.dev.features` 增加 `worktrees`（以及 `worktreeReset`、`worktreeStartCommand` 等细粒度键，便于 Desktop 渐进启用）。

## 8. CLI

新增 `mothx worktree` 子命令（薄包装，复用 Runtime 入口）：

```bash
mothx worktree list [--repo DIR] [--project ID] [--json]
mothx worktree create [--repo DIR] [--name NAME] [--detach] [--start CMD] [--project ID]
mothx worktree remove <id|name|directory> [--force]
mothx worktree reset  <id|name|directory>
```

并可选增加 `mothx --worktree [NAME]`：在（新建或已有）worktree 中启动 TUI，等价于 `--cwd`。CLI 只调用共享入口，不自己拼 git 命令。

## 9. WebUI 与 TUI

- 首版不强制。WebUI 复用 Serve 的 HTTP 端点（新增 `/api/worktrees*` 投影），TUI 复用 CLI 子命令/内部 API。
- 两者都只投影 Runtime 状态，不实现生命周期。

## 10. Desktop 交互

Desktop 是首个完整界面，保持纯 ACP：

1. **新会话工作区选择器**（composer）：在现有「目录选择」旁增加「主分支 / 已有 worktree / 新建 worktree」。选择 worktree 时把其目录作为 `session/new` 的 cwd。
2. **等待就绪**：创建后 renderer 维护 pending 状态，收到 `mothx/worktree/status` 的 ready/failed 后再发送首条 prompt（对齐 opencode `Worktree.wait`），并在失败时 toast。
3. **侧栏分组**：在 project/仓库下展示 worktree 列表（名称 + 分支 + 状态），支持删除（二次确认）与重置；external 项标为只读。
4. **设置**：worktree 启动脚本、分支前缀。
5. **目录授权**：worktree 目录由 Runtime 授权；Desktop 主进程只负责在「选择已有目录」时通过既有 IPC 让用户挑选，不把目录选择当作授权。

边界：renderer 不直连文件系统/DB，不执行 git；所有动作经 `desktop`/`invoke` → ACP。

## 11. 安全、并发与恢复

- **路径安全**：所有目录先 `filepath.Clean` 再 `EvalSymlinks`；拒绝非绝对路径、拒绝符号链接逃逸；删除前必须 `git worktree list` 证明。
- **授权不可绕过**：worktree 目录只有经 Runtime 创建/注册/授权后才可用；外部手工创建的 worktree 仅只读列出，使用前仍需用户显式授权。
- **并发**：创建/删除/重置按仓库根串行（进程内锁）；同一 worktree 目录额外加**跨进程 advisory 文件锁**（`<root>/.locks/<hash>.lock`，O_CREATE|O_EXCL + 过期回收），覆盖填充/删除/重置。
- **运行中保护**：worktree 上有活跃 run 时默认拒绝删除/重置；`force` 先走 `ExecutionRuntime.CancelDurable` 再操作。
- **崩溃恢复**：`pending` 行在进程重启后与 git 对账——git 中不存在对应 worktree 则置 `failed`/`removed`，允许重试创建；不残留半成品状态。
- **不越界**：worktree 不是安全沙箱，不降低任何既有保护；删除只针对已证明路径。

## 12. 迁移与兼容

- **migration**：在 `internal/session/migrations.go` 末尾追加 version 44 `create_worktrees`；不改动既有表。
- **settings.json**：新增可选 `worktree` 段，缺省即默认值，向后兼容。
- **ACP**：纯 additive，能力键门控；旧客户端不受影响。
- **架构守卫**：任何新增 Agent 构造/Run 持久化调用点都要过 `go test ./internal/architecture`；worktree 不新增构造路径，因此守卫应保持通过（如需 allowlist，必须内联说明）。
- **文档**：`docs/en/`、`docs/zh/` 同步更新；changelog 追加。

## 13. 测试计划

- `internal/worktree` 单测：slug/名称唯一性、`list --porcelain` 解析、路径规范化、reset/remove 的拒绝路径（非 git、主工作树、未证明目录）、默认分支解析回退。
- `internal/session` / `internal/dao`：注册表 CRUD、`directory` 唯一约束、状态转移、对账清理、project `ON DELETE SET NULL`。
- `internal/agentruntime`：同步登记+授权、异步 ready/failed 事件序列、授权三类来源 union、重启后从注册表重新授权、超限不误伤（不占 additional 名额）、运行中删除拒绝、崩溃恢复对账。
- 多 agent worktree：子 agent 默认继承父工作区、spawn/成员/策略三触发点、ESM 按 objective 共享、per-child 隔离（复用同一 worktree 资源）、成员 `worktree` + 固定 `WorkDir` 冲突报错。
- `internal/acp`：`mothx/worktree/*` 参数校验、错误映射、能力键、事件投影；使用 subprocess-helper 模式的进程级测试，隔离临时目录与本地仓库。
- Desktop：`npm run typecheck` + 针对选择器/wait 语义的 `npm test`；必要时 Electron ACP smoke。
- `go test ./internal/architecture`：确认未新增绕过路径。

## 14. 实施阶段

| 阶段 | 内容 | 交付 |
| --- | --- | --- |
| P0 | `internal/worktree` 机制 + `internal/dao`/`internal/session` 注册表 + `WorktreeManager` 编排/授权/事件 + CLI 子命令 | 可创建/列出/删除/重置 worktree |
| P1 | ACP `mothx/worktree/*` + 能力键 + 事件投影 + Desktop 新会话选择器/等待就绪/侧栏 | Desktop 可用 |
| P2 | reset 完整语义（submodule/clean）、启动脚本（经 sandbox）、设置项、WebUI/TUI 投影 | 完整产品能力 |
| P3 | 多 agent 按需 worktree：子 agent 工作区继承修正、spawn/成员/策略三触发点、ESM 按 objective 共享、per-child 隔离（复用同一 worktree 资源） | 多 agent 隔离能力 |
| P4 | external worktree 只读列出、跨进程文件锁、更多边界测试 | 健壮性收敛 |

## 15. 风险与开放问题

- **仓库根发现**：多仓库/子模块嵌套时如何确定「主仓库」？首版以 `rev-parse --show-toplevel` 为准，嵌套场景留待后续。
- **启动脚本安全**：启动脚本是任意命令，需与沙箱/审批策略对齐；首版默认关闭，需显式配置。
- **Windows 路径大小写**：需规范化比较（对齐 opencode 的 lower-case 处理），避免重复 worktree；对外引用统一用 `id` 以降低脆性。
- **磁盘占用**：每个 worktree 是完整工作树，需在 UI 提示体积；可考虑后续 shallow/partial。
- **external worktree 的归属**：用户手工创建的 worktree 是否要「收养」为注册资源？首版只读列出，不自动收养。

## 16. 待确认问题（已全部落地）

1. 启动脚本：P2 落地，经 sandbox 策略执行，默认无脚本即不执行。
2. 新会话工作区选择器：Desktop composer 提供「隔离工作区」菜单（新建/列表/重置/删除）。
3. 外部手工 worktree 只读列出（`External` 标记），不自动收养。
4. worktree 在 Desktop 按**仓库**分组（composer 弹出菜单），未按 project 分组——project 与仓库不绑定。
5. 多 agent 默认隔离级别：子 agent 一律继承父工作区，显式请求才独占。
6. ESM 是否默认给每个 objective 开 worktree：**不默认**，由 `worktree.esm` 显式开启（避免静默行为变更）。
