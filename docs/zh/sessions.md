# 会话管理

MothX 使用 SQLite 存储会话数据，支持树状结构、分支、压缩、标签和快速会话查找。

## 会话存储

### 存储架构

MothX 的会话设计在不同运行模式下有所区别：

1. **CLI / TUI / Serve 模式（单一数据库 + 虚拟句柄）**
   所有会话元数据（如会话列表、会话ID、CWD、时间戳）和所有历史消息/条目均统一保存在单个 SQLite 数据库文件 `sessions.db` 中。
   在该模式下，**不需要且不会在磁盘上生成工作目录子目录或物理会话文件**。CLI/TUI 中展示的 `.db` 路径（如 `~/.mothx/sessions/20260625-120000_abcd1234.db`）是根据数据库元数据动态计算出的**虚拟路径**（句柄），无需真实存在即可被程序识别、定位和删除。

2. **Serve 消息通道（单一数据库 + 物理句柄）**
   无人值守的消息通道在单一 `sessions.db` 的基础上，还会为每个用户在磁盘上创建编码后的工作目录物理子目录，并在其中写入包含对应会话 ID 的真实物理句柄文件（如 `20260625-120000_abcd1234.db`），以便进行特定平台的会话关联与生命周期管理。

### 存储位置布局（以全局 session 目录为例）

```text
~/.mothx/sessions/
├── sessions.db                         # 统一存储所有会话和消息条目的唯一 SQLite 数据库
└── channels/                           # (仅消息通道启用时存在)
    └── wechat/user_123/active.db       # Channels 平台特定的物理会话句柄
```

### 路径编码

在需要编码工作目录以进行隔离的场景下（例如 Channels），工作目录路径使用 URL 安全的 base64 编码，避免冲突和文件系统兼容性问题。

示例：
- `/home/user/project` → `--L2hvbWUvdXNlci9wcm9qZWN0--`
- `/home/user/my.app` → 另一个互不冲突的编码目录名

## SQLite 结构

会话状态存储在 `sessions.db` 的两张核心表中：

| 表 | 用途 |
|----|------|
| `sessions` | 会话元数据：ID、工作目录、时间戳、父会话、版本 |
| `entries` | 有序事件日志：消息、模型切换、压缩、标签和元数据条目 |

条目会保留稳定的 ID 和父 ID，因此对话可以按树状结构重放并安全压缩。

### 条目类型

| 类型 | 描述 |
|------|------|
| `session` | 会话元数据 |
| `message` | 用户/助手/工具消息 |
| `model_change` | 模型切换记录 |
| `thinking_level_change` | 思考等级切换记录 |
| `compaction` | 上下文压缩检查点 |
| `custom` | 运行时特定的自定义元数据条目 |
| `custom_message` | 外部或自定义运行时创建的消息负载 |
| `session_info` | 会话名等展示元数据 |
| `branch_summary` | 分支切换摘要 |
| `label` | 用户自定义标签 |

### 持久性与写入性能

`sessions.db` 运行在 WAL 模式，持久性级别默认为 SQLite 推荐的 `synchronous(NORMAL)`：提交不再逐次 fsync（同步集中在 WAL checkpoint），多个进程（Serve、TUI、ACP、CLI）共享同一会话目录时的写锁竞争与写入延迟显著降低。其语义是：

- 进程崩溃（kill -9、panic）不丢失任何数据；
- 操作系统崩溃或断电时，数据库不会损坏，但最近一次 checkpoint 之后（通常为秒级）的提交可能回退；缺失的 run 终态由租约过期 → 孤儿恢复路径收敛，会话不会永久停留在“运行中”。

对断电敏感的部署（不稳定电源、部分网络盘等）可用环境变量 `MOTHX_SQLITE_SYNCHRONOUS=FULL` 恢复旧的每提交 fsync 行为；该变量按进程生效，新旧进程混布共享同一库文件是安全的。以 `--debug` 启动时，`/debug/vars` 端点的 `mothx_sqlite` 指标（busy 重试次数与退避总时长、事务 begin 次数/等待总时长/单次最大等待）可用于观测跨进程写锁竞争。完整设计与压测矩阵见 `docs/proposal/sqlite-write-pressure-reduction-proposal.md`。

## 会话操作

### 创建新会话

```go
sess := session.New(cwd, sessionDir)
if err := sess.Init(); err != nil {
    return err
}
```

### 继续最近会话

```bash
mothx --continue
mothx -c
```

```go
sess, err := session.ContinueRecent(cwd, sessionDir)
```

### 恢复特定会话

```bash
# 通过 session ID 或唯一前缀
mothx --resume abcd1234

# 通过会话句柄路径
mothx --resume ~/.mothx/sessions/--encoded-working-directory--/20260625-120000_abcd1234.db
```

```go
sess, err := session.OpenByPathOrID(cwd, sessionDir, "abcd1234")
```

### TUI 会话选择框

在交互式 TUI 模式中，如果没有传入 `--continue`、`--resume` 或 `--session`，启动时不会立即创建空 session；第一条用户消息发送时才会创建。

使用 `/sessions` 可以打开交互式会话选择框。它支持方向键上下选择、回车切换、`n` 开始新会话、`d` 删除选中会话、Esc 关闭。文本命令仍然可用：

```bash
/sessions ls
/sessions set abcd1234
/sessions clear
/sessions del abcd1234
```

继续、恢复或选择已有 session 时，会话历史会打印到正常终端 scrollback 中。

### Web UI 会话历史

Serve Web UI 的历史会话列表按最后使用时间倒序排列，最近回复的会话显示在最上方。Web UI 会话管理页面同时显示每个会话的最后回复时间，便于快速定位最近的对话。

### 添加消息

```go
_, err := sess.AppendMessage(provider.NewUserMessage("Hello"))
```

## 树状结构

会话条目形成父子链接的树：

```text
session-abcd1234
├── msg-001 (user: "Hello")
│   └── msg-002 (assistant: "Hi!")
│       └── msg-003 (user: "Tell me more")
└── msg-004 (user: "Different question")  # 分支点
    └── msg-005 (assistant: "...")
```

这支持探索不同方向、回到之前的节点重新开始，并保留多个解决方案。

## 会话压缩

MothX 会把压缩检查点记录到 SQLite。压缩后，重放状态会保留每条消息的 Entry ID 和压缩边界（`firstKeptEntryID`）。当会话被重新加载时：

- 消息会被裁剪到正确的压缩边界
- 摘要消息会自动前置
- 后续消息保持原始 Entry ID

压缩配置示例：

```json
{
  "compaction": {
    "enabled": true,
    "reserveTokens": 16384,
    "keepRecentTokens": 20000
  }
}
```

## 最佳实践

### 定期清理

删除普通 CLI、TUI 或 Serve 会话时，优先使用内置 `/sessions del <id>` 命令或经认证的会话 API。这些接口会从共享 `sessions.db` 中删除会话记录；虚拟句柄不需要手动删除文件。

```bash
/sessions ls
/sessions del abcd1234
```

手动清理物理句柄通常只适用于通道专用会话绑定。请先检查，再仅删除编码工作目录子目录中的旧绑定文件。不要删除根目录的 `sessions.db`，除非你确实要移除所有持久化会话数据。

```bash
# 示例：先查看旧通道绑定文件，再谨慎删除
find ~/.mothx/sessions -path '*/channels/*/*.db' -mtime +30 -print
```

### 备份重要会话

备份 session 根目录，尤其是 `sessions.db` 和编码后的工作目录句柄目录：

```bash
cp -a ~/.mothx/sessions ~/backups/
```

## 故障排除

### 会话数据库错误

```text
Error: session "..." not registered in DB
```

可能原因：
- 会话句柄文件存在，但 SQLite 记录已被删除
- `sessions.db` 被删除，或从较旧备份恢复
- session 根目录只被部分复制

解决方案：
1. 从备份恢复完整的 session 根目录
2. 使用 `/sessions list` 显示的有效 session ID 恢复
3. 如果 SQLite 记录已不存在，删除失效的句柄文件

### 会话丢失

可能原因：
- 工作目录发生变化
- 会话句柄文件或数据库被删除
- 编码后的工作目录不同

解决方案：
1. 检查 `~/.mothx/sessions/` 或 `%APPDATA%\mothx\sessions\`
2. 在原工作目录下使用 `--resume <session-id>`
3. 确认 `sessionDir` 配置正确
