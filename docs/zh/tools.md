# 工具系统参考指南

MothX 提供了一套功能强大且可扩展的内置工具，用于文件操作、代码库探索、Shell 执行、多 Agent 协同以及工作区交互。

---

## 1. 工具执行安全性与沙箱机制

在探索各个工具之前，首先需要了解 MothX 执行工具时的三种安全级别：

| 运行模式 | 工具修改权限 | 网络访问 | 典型用途 |
|----------|--------------|----------|----------|
| **plan** | 只读，禁止写入和修改 | 通常关闭 | 分析与规划 |
| **agent** | 可在工作区读写；危险操作需审批 | 受沙箱与工具配置限制 | 日常开发 |
| **yolo** | 放宽审批并允许完整工具操作；黑名单仍有效 | 由沙箱配置决定 | 明确授权的自动化任务（默认） |
| **os** | 仅提供 Bash，使用 YOLO 级审批行为；高风险保护仍生效 | 关闭 | 仅 Shell 自动化 |

### 沙箱工作原理 (`bwrap`)
当 `settings.json` 中的 `sandbox.enabled` 设置为 `true` 时，MothX 会隔离通过 `bash`（或其他外部进程）运行的命令：
* **允许的路径**：沙箱将系统文件夹（`/usr`, `/lib`, `/bin` 等）挂载为只读，并将当前工作空间目录挂载为读写（`standard` 模式）或只读（`strict` 模式）。
* **拒绝的路径**：敏感目录（如 `~/.ssh`, `/etc/shadow` 等）会被完全隐藏或禁止访问。
* **网络隔离**：通过创建独立的网络命名空间（`--unshare-net`）来完全切断外部网络连接。


## 1a. 并行工具执行

当一个 agent 回合返回多个本地函数或自定义工具调用时，MothX 可以使用有界工作池并发执行它们。由 `settings.json` 中的 `toolExecution` 设置控制：

| 设置项 | 类型 | 默认值 | 描述 |
|--------|------|--------|------|
| `mode` | string | `"parallel"` | `"parallel"` 使用有界本地 worker 并行执行；`"sequential"` 在本地逐个执行已返回的调用（不影响 provider 侧并行出参） |
| `maxConcurrency` | int | `10` | 每轮 agent 允许的最大并发工具调用数（设为 `1` 则为串行） |

工具完成事件可能乱序输出，但 provider 的后续消息会按原始调用顺序还原。该设置由 TUI、WebUI、Serve、channels、ACP 以及通过共享 Runtime 创建的子 Agent 统一使用。`sequential` 只改变响应到达后的本地执行顺序；provider 仍可能在一条响应中返回多个 tool call。它不控制 provider 托管工具（如 OpenAI Responses `web_search`）内部的并发；这些调用仍由 provider 自身的编排负责。


---

## 2. 完整工具目录

| 工具名 | 分类 | 行为 | 执行安全性 | 可用范围 |
|--------|------|------|------------|----------|
| [`read`](#read---文件读取) | 文件系统 | 读取文件内容（支持图像和分页） | 允许只读访问 | 所有模式 |
| [`write`](#write---文件写入) | 文件系统 | 创建或覆盖文件 | 需要 Standard/Yolo | CLI, ACP, Serve, Channels |
| [`edit`](#edit---精确文件修改) | 文件系统 | 原子文本替换修改 | 需要 Standard/Yolo | CLI, ACP, Serve, Channels |
| [`ls`](#ls---列出目录内容) | 代码探索 | 列出目录下的文件和子目录 | 允许只读访问 | 所有模式 |
| [`find`](#find---文件搜索) | 代码探索 | 根据通配符匹配搜索文件 | 允许只读访问 | 所有模式 |
| [`grep`](#grep---文本内容搜索) | 代码探索 | 正则表达式搜索文件内容 | 允许只读访问 | 所有模式 |
| [`bash`](#bash---命令执行) | 进程执行 | 执行 Shell 命令（支持同步与异步） | 受沙箱级别限制 | CLI, ACP, Serve, Channels |
| [`jobs`](#jobs---后台任务管理) | 进程执行 | 列出或检查后台运行任务的状态 | 允许只读访问 | 所有模式 |
| [`kill`](#kill---终止后台任务) | 进程执行 | 终止正在运行的后台任务 | 需要 Standard/Yolo | CLI, ACP, Serve, Channels |
| [`plan`](#plan---任务计划) | 会话交互 | 更新并展示可见的阶段性任务计划 | 允许只读访问 | 所有模式 |
| [`question`](#question---用户问题澄清) | 会话交互 | 提示用户并等待多选问题输入 | Plan 与 Agent 模式 | TUI 与 ACP |
| [`memory`](#memory---持久记忆) | 工作流 | 读写持久化记忆文件 `memory.md` | 会话级读写 | 仅限 Serve 模式 |
| [`cron`](#cron---定时后台任务) | 工作流 | 通过子 Agent 调度定时后台任务 | 会话级调度 | Channels 与多 Agent 模式 |
| [`subagent_spawn`](#subagent_---子-agent-委托) | 多 Agent | 派生出完全隔离的子 Agent | 子 Agent 级权限限制 | 仅多 Agent 模式 |
| [`subagent_status`](#subagent_---子-agent-委托) | 多 Agent | 查询子 Agent 执行状态与结果 | 只读 | 仅多 Agent 模式 |
| [`subagent_send`](#subagent_---子-agent-委托) | 多 Agent | 向子 Agent 发送后续追问或指令 | 发送消息 | 仅多 Agent 模式 |
| [`subagent_wait`](#subagent_---子-agent-委托) | 多 Agent | 关键路径被阻塞时等待成员活动 | 有界等待（2.5s-120s） | 仅多 Agent 模式 |
| [`subagent_answer`](#subagent_---子-agent-委托) | 多 Agent | 回答成员的阻塞提问，让其继续执行 | 提问作用域 | 仅多 Agent 模式 |
| [`subagent_destroy`](#subagent_---子-agent-委托) | 多 Agent | 销毁子 Agent 释放其上下文资源 | 销毁释放 | 仅多 Agent 模式 |
| [`delegate_subagent`](#delegate_subagent---阻塞式单子-agent-委托) | 委托模式 | 同步执行一个子 Agent 任务 | 子 Agent 级权限限制 | 仅 Delegate 模式 |
| [`workflow_run`](#workflow_run---动态-javascript-workflow) | Workflow | 执行 JavaScript workflow 并编排 worker agent | 子 Agent 级权限限制 | 仅 Workflow 模式 |
| [`workflow_status`](#workflow_status---workflow-运行状态) | Workflow | 查询 workflow 运行记录与结果 | 只读 | 仅 Workflow 模式 |
| [`workflow_cancel`](#workflow_cancel---workflow-取消) | Workflow | 取消运行中的 workflow | 仅当前进程 active run | 仅 Workflow 模式 |
| [`a2a_dispatch`](#a2a_dispatch---远程-agent-分发) | 多 Agent | 向配置的远程 A2A Agent 节点发送任务 | 发起网络请求 | 仅 A2A Master 模式 |
| [`skill_ref`](#skill_ref---加载技能引用) | 技能系统 | 动态加载外部技能定义的参考文档 | 只读 | 所有模式 |

---

## 3. 工具详细说明与参数

### read - 文件读取

读取指定路径的文件内容，内置分页功能（非常适合在不撑爆大模型上下文窗口的前提下读取超大文件），并提供对图像多模态读取的支持。

#### 参数：

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `path` | string | ✓ | 目标文件路径。 |
| `offset` | integer | - | 起始行号（从 1 开始）。 |
| `limit` | integer | - | 最大读取的行数。 |

#### 图像文件支持：
若读取的文件路径具有常见的图像后缀名（如 `.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`），该工具会自动将图像转为 Base64 编码，并返回包含对应 MIME 类型的富文本块，以便支持多模态的大模型直接进行视觉分析。

#### 示例载荷：
```json
{
  "path": "src/main.go",
  "offset": 120,
  "limit": 50
}
```

---

### write - 文件写入

创建一个新文件，或者完全覆盖一个已有的文件。此操作是原子性的（即先写入临时文件，成功后再重命名覆盖），能有效防止因进程突然中断导致的文件损坏。

#### 参数：

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `path` | string | ✓ | 写入的文件路径。 |
| `content` | string | ✓ | 写入的完整文本内容。 |

#### 示例载荷：
```json
{
  "path": "config.json",
  "content": "{\n  \"port\": 8080,\n  \"debug\": true\n}"
}
```

---

### edit - 精确文件修改

`edit` 工具在目标文件上执行极其精准的 **原子化批量文本替换**。深刻理解该工具底层的执行机制对正确使用它非常重要：

#### 修改生命周期与安全校验规则：
1. **预检校验**：在进行任何修改前，MothX 首先读取文件，并在文件中查找 `edits[]` 中定义的每一个 `oldText` 文本块。
2. **唯一性检查**：每一个 `oldText` 匹配模式在目标文件中 **必须且仅出现一次**。如果发现某个匹配模式不存在，或者匹配到了多处（非唯一），**整批修改会立即被全部中止并报错**，文件保持完全不变。
3. **冲突重叠检查**：MothX 会按照原文件中的索引位置对修改进行排序，检查多个修改之间是否发生交叉或重叠。一旦发现重叠，立刻报错拦截，防止生成混乱的修改。
4. **原子应用**：所有校验通过后，按顺序在内存中替换完毕，最终通过原子写入机制更新到宿主机文件。没有半完成的中间状态。

#### 参数：

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `path` | string | ✓ | 要修改的文件路径。 |
| `edits` | array of objects | ✓ | 替换操作块列表。 |

**Edit 对象结构：**

| 属性名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `oldText` | string | ✓ | 文件中等待被替换的精准原文内容。 |
| `newText` | string | ✓ | 替换后的新文本内容。 |

#### 示例载荷：
```json
{
  "path": "server.go",
  "edits": [
    {
      "oldText": "func Start() {\n\tlog.Println(\"Starting server...\")",
      "newText": "func Start() {\n\tlog.Println(\"Booting microservice...\")"
    }
  ]
}
```

---

### bash - 命令执行

执行指定的 Shell 命令（如 Linux/macOS 上的 `/bin/bash`、Windows 上的 BusyBox，或回退到 PowerShell）。该工具完美支持同步（阻塞）和异步（后台运行）两种模式。

#### 参数：

| 参数名 | 类型 | 必填 | 默认值 | 描述 |
|--------|------|------|--------|------|
| `command` | string | ✓ | - | 要执行的完整 Shell 命令。 |
| `timeout` | integer | - | 45 | 最大执行超时时间，单位秒（最大 600 秒）。长驻服务应使用 `async=true`；`timeout=0` 表示不设置工具层 deadline。 |
| `async` | boolean | - | false | 如果设为 `true`（或者命令以 `&` 结尾），进程将进入后台异步执行，并立即返回一个 `jobId`。 |

#### 同步调用示例：
```json
{
  "command": "go test -v ./internal/provider/...",
  "timeout": 60
}
```

#### 异步调用示例（例如启动长驻服务）：
```json
{
  "command": "npm run dev",
  "async": true
}
```

---

### jobs - 后台任务管理

列出、查询或清理通过 `bash` (开启 `async=true`) 启动的后台异步进程。

#### 参数：

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `jobId` | integer | - | 可选：查询特定任务的详细输出日志、启动时间、CPU 运行耗时及实时状态。 |
| `cleanup` | boolean | - | 清理所有已执行完毕的任务记录。 |

#### 示例载荷（列出所有任务）：
```json
{}
```

#### 示例载荷（查询具体任务）：
```json
{
  "jobId": 1
}
```

---

### kill - 终止后台任务

强行终止（发送 SIGTERM 甚至 SIGKILL）通过 `bash` 启动的特定后台运行进程。

#### 参数：

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `jobId` | integer | ✓ | 需要被强制终止的目标任务 ID。 |

#### 示例载荷：
```json
{
  "jobId": 1
}
```

---

### ls - 列出目录内容

列出目标目录下的子文件与目录，提供精准的文件大小、修改状态及类型区分。

#### 参数：

| 参数名 | 类型 | 必填 | 默认值 | 描述 |
|--------|------|------|--------|------|
| `path` | string | - | `.` | 待列出的目录路径。 |

#### 示例载荷：
```json
{
  "path": "./internal"
}
```

---

### find - 文件搜索

根据通配符模式搜索匹配的文件。该工具在进程内运行纯 Go 的 `go-fd` 引擎，并遵守 ignore 文件。

#### 参数：

| 参数名 | 类型 | 必填 | 默认值 | 描述 |
|--------|------|------|--------|------|
| `pattern` | string | ✓ | - | 文件名匹配通配符（如 `*.go`, `*test*`）。 |
| `path` | string | - | `.` | 搜索的根目录起点。 |
| `maxDepth` | integer | - | - | 搜索的最大目录深度限制。 |
| `maxResults`| integer | - | 100 | 返回的最大结果数量。 |

#### 示例载荷：
```json
{
  "pattern": "*_test.go",
  "path": "internal/provider",
  "maxDepth": 3
}
```

---

### grep - 文本内容搜索

使用正则表达式搜索代码库文件内容。该工具在进程内运行纯 Go 的 `go-ripgrep` 引擎，并遵守 ignore 文件。如果模式不是合法正则表达式，搜索会自动回退为字面量匹配，并在结果中注明。

#### 参数：

| 参数名 | 类型 | 必填 | 默认值 | 描述 |
|--------|------|------|--------|------|
| `pattern` | string | ✓ | - | 正则表达式匹配模式。非法正则表达式会自动回退为字面量搜索。 |
| `path` | string | - | `.` | 搜索的目录或文件路径。 |
| `include` | string | - | - | 限制匹配的文件后缀通配符（如 `*.go`）。 |
| `maxResults`| integer | - | 100 | 返回的匹配行数限制。 |

#### 示例载荷：
```json
{
  "pattern": "type ProviderConfig struct",
  "include": "*.go"
}
```

---

### plan - 任务计划

在 TUI 中渲染多步骤任务清单面板，并持久化当前计划状态。TUI 还会把未完成步骤显示为 sticky todo list，让用户在 transcript 持续流式输出时仍能跟踪当前进度。

#### 参数：

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `steps` | array of objects | ✓ | 有序任务步骤。 |
| `title` | string | - | 计划任务的标题。 |
| `note` | string | - | 可选：当前的风险提示、阻塞条件或备注。 |

**Step 对象的具体属性：**

| 属性名 | 类型 | 必填 | 允许的值 | 描述 |
|--------|------|------|----------|------|
| `title` | string | ✓ | - | 简洁的步骤描述。 |
| `status` | string | ✓ | `pending`, `running`, `done`, `failed` | 执行状态。 |

#### 示例载荷：
```json
{
  "title": "数据库迁移校验",
  "steps": [
    { "title": "检查当前数据库 Schema", "status": "done" },
    { "title": "执行数据结构变更脚本", "status": "running" },
    { "title": "校验数据一致性与回滚验证", "status": "pending" }
  ]
}
```

---

### question - 用户问题澄清

助手可以暂停工作，并弹出一个多选交互菜单向用户提出提问，以便精确澄清需求细节。在 TUI 和 ACP 会话的 `plan` 和 `agent` 模式下可用。用户可以直接在 TUI 终端中使用上下键导航并选中特定选项，极其便捷。

#### 参数：

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `question` | string | ✓ | 具体的提问问题文本。 |
| `options` | array of strings | ✓ | 呈现给用户选择的候选项列表。 |

#### 示例载荷：
```json
{
  "question": "您希望为前端组件使用哪种样式方案？",
  "options": [
    "Tailwind CSS",
    "Styled Components",
    "原生 CSS / CSS Modules"
  ]
}
```

---

### memory - 持久记忆

在 Channels 聊天机器人模式下，助手可以在 `.mothx/memory.md` 中记录用户的个人偏好、专属工程实践规定或通用教训。该文件在每次新会话启动时都会被自动读入作为前置提示。

#### 参数：

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `action` | string | ✓ | 执行的操作：`read`（读取）, `add`（新增）, `update`（更新）, `delete`（删除）。 |
| `section` | string | - | 标题分组（如 `User Profile`, `Project Rules`）。 |
| `content` | string | - | 写入或匹配的文本内容。 |
| `old` | string | - | 供更新替换的原文（在 `update` 时使用）。 |
| `new` | string | - | 新替换的新内容（在 `update` 时使用）。 |

#### 示例载荷：
```json
{
  "action": "add",
  "section": "Project Rules",
  "content": "所有的 API 接口都必须提供 OpenAPI 3.0 格式的文档描述。"
}
```

---

### cron - 定时后台任务

允许主会话创建并配置一类定时任务。这类任务将在后台由子 Agent 在特定的时刻定时触发并自动运行。

#### 参数：

| 参数名 | 类型 | 必填 | 默认值 | 描述 |
|--------|------|------|--------|------|
| `action` | string | ✓ | - | 任务操作：`list`, `create`, `enable`, `disable`, `remove`, `run`。 |
| `id` | string | - | - | 任务 ID（对编辑/启用/禁用/移除等行为必填）。 |
| `name` | string | - | - | 定时任务的简短代号。 |
| `prompt` | string | - | - | 定时触发后，发送给子 Agent 调度的提示词。 |
| `schedule` | string | - | - | Cron 标准表达式或定期间隔（如 `@daily`, `@every 15m`）。 |
| `oneshot` | boolean | - | false | 若设为 `true`，触发并成功执行一次后该任务会自动禁用。 |
| `mode` | string | - | `yolo` | 子 Agent 运行的模式选择（`agent` 或 `yolo`）。 |

#### 示例载荷：
```json
{
  "action": "create",
  "name": "daily-git-pull",
  "prompt": "同步拉取最新的上游主分支代码，并验证整个项目能否成功编译编译。",
  "schedule": "@daily",
  "mode": "yolo"
}
```

---

### subagent_* - 子 Agent 委托

当 MothX 启用多 Agent 运行模式（使用 `--multi-agent` 参数启动）时，主 Agent 可以动态派生出完全隔离的子 Agent 来并行承担不同的开发或审查工作。子 Agent 拥有完全独立的消息上下行、临时环境、独立日志和 Session。

#### `subagent_spawn`
异步派生一个全新的子 Agent，并接收一个独立的作业 ID：

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `task` | string | ✓ | 描述让子 Agent 具体去完成的任务。 |
| `mode` | string | - | 运行模式选择（`plan`, `agent` 或 `yolo`，默认继承父模式，否则回退到 `yolo`）。 |
| `work_dir` | string | - | 限定子 Agent 的运行工作子目录。 |
| `tools` | array of strings | - | 允许子 Agent 调用的工具白名单。 |
| `max_iterations` | integer | - | 大模型循环周期的最大限制次数。 |

#### `subagent_status`
查询子任务的执行进度并返回最终成果报告。
```json
{ "handle": "subagent-job-1" }
```

#### `subagent_send`
向运行中的子 Agent 注入追加信息或补充指令。
```json
{ "handle": "subagent-job-1", "message": "同时请把 helper_test.go 里的包引入路径修复一下。" }
```

#### `subagent_wait`
阻塞等待，直到有成员/子 Agent 的完成通知进入会话邮箱，或到达有界超时。只返回待处理摘要（成员 ID、状态，以及等待回答的成员的 `question_id`）；完成内容会在下一个迭代边界自动投递。

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `timeout_ms` | integer | - | 最长等待毫秒数，限制在 2500-120000（默认 30000）。 |

#### `subagent_answer`
回答运行中成员向 lead（而不是向用户）提出的阻塞提问。提问以 `[MEMBER_QUESTION]` steering 消息或 `subagent_wait` 中 `status: question` 的待处理条目送达；在回答之前该成员一直阻塞。

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `handle` | string | ✓ | 提问成员的处理句柄。 |
| `question_id` | string | ✓ | 来自 `[MEMBER_QUESTION]` 消息或等待摘要的提问 ID。 |
| `answer` | string | ✓ | 成员继续执行所需的回答。 |

若提问已不在待处理状态（已回答、已过期或属于其他成员），工具会返回错误而不是静默成功。

#### `subagent_destroy`
销毁该子 Agent 的运行时上下文并彻底回收一切临时资源。
```json
{ "handle": "subagent-job-1" }
```

---

### delegate_subagent - 阻塞式单子 Agent 委托

当 MothX 以 Delegate 模式启动（`--delegate`）或在运行时通过 `/delegate on` 启用后，主 Agent 可获得一个同步阻塞的 `delegate_subagent` 工具。适合那些需要多步探索但父 Agent 仅需最终摘要结果的边界清晰子任务。

与 `subagent_*` 多 Agent 工具不同，`delegate_subagent` 是同步执行的：父 Agent 等待子 Agent 完成后才收到 JSON 格式的结果。同一时间只能运行一个 delegate。

| 参数名 | 类型 | 必填 | 默认值 | 描述 |
|--------|------|------|--------|------|
| `task` | string | ✓ | - | 具体且边界明确的任务描述，需包含目标、相关路径/上下文、预期输出格式和停止条件。 |
| `mode` | string | - | 父模式，然后 `yolo` | 子 Agent 执行模式：`plan`、`agent` 或 `yolo`。 |
| `work_dir` | string | - | 当前工作目录 | 子 Agent 的工作目录。 |
| `tools` | array of strings | - | 除嵌套子 Agent/delegate 外的所有工具 | 可选的工具白名单，例如 `["read", "grep", "find"]`。 |
| `max_iterations` | integer | - | `50` | 最大工具调用迭代次数。 |
| `system_prompt_extra` | string | - | - | 传给子 Agent 的额外上下文或约束。 |

请求示例：
```json
{
  "task": "查找 internal/serve/openaiapi/ 下导入 net/http 但未调用 http.Error 的 Go 文件，返回文件路径和行号。",
  "mode": "plan",
  "tools": ["grep", "read", "find"],
  "max_iterations": 20
}
```

返回结果结构：
```json
{
  "status": "done",
  "result": "...摘要结果...",
  "duration": "1.234s",
  "tool_calls": 7,
  "tool_breakdown": {"grep": 4, "read": 3}
}
```

Delegate 模式适用于大范围代码搜索、多步调查、聚焦实现或验证任务。不适用于单步操作、需要向用户澄清的任务或强依赖完整会话历史的有状态工作。

---

### workflow_run - 动态 JavaScript Workflow

当 MothX 使用 Workflow 模式启动（`--workflows`）后，主 Agent 可以运行一段普通 JavaScript workflow 脚本，将任务拆成多个 phase，并在 phase 内调度 worker agent。Workflow 模式与 `--multi-agent` 独立：启用 workflow 只暴露 `workflow_*` 工具，不会暴露 `subagent_*` 工具。

Workflow 脚本必须使用受支持的 JavaScript DSL。不要用 JSON DSL 描述 workflow 结构。

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `source` | string | ✓ | JavaScript workflow 源码。顶层表单应为 `workflow("name", body)`。 |
| `timeoutSeconds` | integer | - | 整个 workflow run 的工具级超时。省略时使用默认工具超时；有明确上限的长 workflow 可设置正数秒数；只有确实需要持续运行、且不希望有 agent 级 deadline 时才设置为 `0`。 |

请求示例：
```json
{
  "source": "workflow(\"auth audit\", {concurrency: 2, phases: [phase(\"scan\", parallel(agent(\"api\", {mode: \"plan\", tools: [\"read\", \"grep\"], maxIterations: 100, prompt: \"审计 internal/serve/openaiapi 的认证风险\"}), agent(\"channels\", {mode: \"plan\", tools: [\"read\", \"grep\"], maxIterations: 100, prompt: \"审计 internal/serve/channels 的认证风险\"}))), phase(\"verify\", agent(\"cross-check\", {mode: \"plan\", tools: [\"read\", \"grep\"], maxIterations: 80, prompt: results(\"scan\") + \"\\n交叉验证结论并列出具体风险。\"}))]});",
  "timeoutSeconds": 900
}
```

当前支持的 workflow builtin 包括 `workflow`、`phase`、`parallel`、`series`、`agent`、`concurrency`、`result`、`resultKey`、`resultLatest`、`results` 和 `log`。Worker agent 通过任务 prompt 接收动态 workflow 上下文，因此父 Agent 的 system prompt 和 tool definitions 在构造后保持冻结。

重要默认值：`concurrency` 默认 5；`mode` 默认继承父 agent mode；省略 `tools` 时使用该 worker mode 的默认工具集；省略、`0` 或负数的 `maxIterations` 默认 50 次 worker-agent 循环。循环中重复的逻辑 agent 应使用 `key`，keyed 结果会保存为 `phase.agent[key]`，可用 `resultKey` 或 `resultLatest` 读取。Worker agent 不能 spawn 子 agent、delegate 或启动嵌套 workflow；DSL 也没有 per-worker `:timeout`、`model`、`:thinking-level` 或 `:max-tokens` 选项。

更多 workflow 模式的详细用法和最佳实践请参考 [Workflow 模式](workflow.md) 文档。

### workflow_status - Workflow 运行状态

列出最近的 workflow run，或返回指定 run 的完整持久化状态。

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `id` | string | - | Workflow run ID。省略时列出最近运行记录。 |

### workflow_cancel - Workflow 取消

取消当前 MothX 进程内仍在运行的 workflow run。已经完成的 run、其他进程中的 run，以及重启前遗留的 run 不再是 active 状态，不能按 ID 取消。

---

### a2a_dispatch - 远程 Agent 分发

此工具仅在 A2A Master Mode 模式下注册生效。它用于通过标准 HTTP A2A 协议向远程注册列表中的协同 Agent 分发并接收特定指令成果。

#### 参数：

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `agent_name` | string | ✓ | 注册于配置文件中的远程 Agent 别名。 |
| `message` | string | ✓ | 发送给目标 Agent 运行处理的详尽提示词或任务。 |

#### 示例载荷：
```json
{
  "agent_name": "kubernetes-deployer",
  "message": "Deploy image tag v1.2.3 to development namespace."
}
```

---

### skill_ref - 加载技能引用

从加载的特定本地技能（Skill）中提取其静态说明、公共契约或 API 原型参考。这能有效保证大量的开发规范和多行的格式模式不会在前置提示词中常驻，而是直到 Agent 具体需要时才被精准加载检索。

#### 参数：

| 参数名 | 类型 | 必填 | 描述 |
|--------|------|------|------|
| `skill` | string | ✓ | 本地加载的技能名（技能对应的目录名称）。 |
| `ref` | string | ✓ | 技能目录下的具体参考规范文件相对路径。 |

#### 示例载荷：
```json
{
  "skill": "kubernetes-skill",
  "ref": "references/helm-deployment.md"
}
```

---

## 4. 最佳开发实践与原则

为了使 MothX 在自动化开发生命周期中发挥出最完美的威力，请确保遵守以下工程指导：

1. **先读后改（Read-Before-Modify）原则**：在指挥或让模型使用 `edit` 修改目标文件前，务必保证之前运行过 `read` 读入该文件。只有完全掌握原文件的准确文本排版与格式，`edit` 工具的 `oldText` 唯一匹配逻辑才能够百分之百吻合，防止任何因格式或换行差异导致的修改报错。
2. **减少用 Bash 工具来进行静态信息检索**：绝大多数静态查询指令（如使用 Bash 运行 `cat`, `grep`, `find`, `ls`）均可用 MothX 内置的高性能工具直接完成。内置的 `read` / `grep` / `find` 会对结果进行专门的内存分页、截断优化，具有极高的处理速度和极低的模型上下文开销，且不受系统沙箱的外部隔离限制。
3. **妥善打理长驻进程**：若通过 `bash` 的 `async=true` 启动了编译服务器、实时监控器或开发后台，请定时在会话中指示执行 `jobs` 工具（并开启 `cleanup=true` 参数）来关闭并打理那些已经崩溃或执行完毕的子任务记录，以释放系统内存开销；若对应长驻服务已不再有用，直接使用 `kill` 任务彻底终止它们。
