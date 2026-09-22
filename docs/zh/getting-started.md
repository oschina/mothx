# 快速入门

本指南帮助你在 5 分钟内开始使用 MothX。

## 系统要求

- **操作系统**: Linux、macOS、Windows 10/11
- **Go**: 1.24+ (从源码构建时)
- **可选**: bubblewrap (用于沙箱功能)

## 安装

### 方法一：一键安装（推荐）

脚本会优先使用已有 Node.js。如果没有 Node.js，则先自动安装对应系统的最新 Node.js LTS，再通过 npm 安装最新版 MothX。

**Linux/macOS：**

```bash
curl -fsSL https://mothx.net/install.sh | bash
```

**Windows（命令提示符）：**

```bat
curl.exe -fsSL https://mothx.net/install.bat -o install.bat && install.bat
```

请在命令提示符（`cmd.exe`）中运行。安装脚本会检查 Node.js；如果尚未安装，则自动安装最新的 Node.js LTS，然后通过 npm 全局安装 MothX。

**卸载:**

```bash
# npm
npm uninstall -g mothx-installer

# PyPI
pipx uninstall mothx-installer

# Linux/macOS
curl -fsSL https://mothx.net/install.sh | bash -s -- --uninstall

# Windows
npm uninstall -g mothx-installer
```

在线安装脚本最终会执行 `npm install -g mothx-installer`。

### 方法四: Docker (GHCR)

```bash
# 默认 Ubuntu 镜像，容器以 root 运行
docker run --rm -it --entrypoint bash ghcr.io/oschina/mothx:latest

# 在当前工作区运行 MothX
docker run --rm -it -v "$PWD:/workspace" -w /workspace ghcr.io/oschina/mothx:latest
```

可用标签：

- `latest` / `ubuntu` - 默认 Ubuntu 镜像
- `debian`、`fedora`、`alpine` - 不同发行版镜像
- `vX.Y.Z`、`vX.Y.Z-ubuntu`、`vX.Y.Z-debian`、`vX.Y.Z-fedora`、`vX.Y.Z-alpine` - 发布标签

支持架构：`linux/amd64`、`linux/arm64`

容器默认以 `root` 运行，并内置 `bash`、`git`、`curl`、`ca-certificates` 和 `openssh-client`。

### 方法五: Go Install

```bash
go install github.com/oschina/mothx/cmd/mothx@latest
```

### 方法六: 从源码构建

```bash
# 克隆仓库
git clone https://gitee.com/oschina/mothx.git
cd mothx

# 构建
make build

# 二进制文件位于 bin/mothx
```

### 方法七: 安装到系统

```bash
# 从源码构建后
make install
```

## 配置 API 密钥

### 方式一: 环境变量

```bash
# DeepSeek
export DEEPSEEK_API_KEY=sk-...
```

### 方式二: 配置文件

或在 settings.json 中直接配置:

```json
{
  "providers": {
    "deepseek-openai": {
      "vendor": "deepseek",
      "api": "openai-chat",
      "baseUrl": "https://api.deepseek.com",
      "apiKey": "sk-..."
    }
  }
}
```

可选的 `vendor` 字段用于选择厂商适配器。未设置时，MothX 会尽量根据 `baseUrl` 自动识别厂商，否则根据 `api` 回退到通用协议 provider。详见 [配置详解](configuration.md)。

## 首次运行

### 交互模式

```bash
# 启动交互式会话
mothx
```

### 非交互模式

```bash
# 单次提问
mothx -p "解释这段代码的作用"

# 从 stdin 读取
echo "写一个 Hello World" | mothx -P
```

### 指定模型

```bash
# 使用 DeepSeek-V4-Flash
mothx --provider deepseek-openai --model deepseek-v4-flash

# 使用 DeepSeek-V4-Pro
mothx --provider deepseek-openai --model deepseek-v4-pro
```

### 多 Agent 模式

```bash
# 启用子 Agent 工具和多 Agent 命令
mothx --multi-agent

# ACP 会话也可以启用
mothx acp --multi-agent
```

多 Agent 模式会注册 `subagent_*` 工具，用于委托边界清晰的任务。TUI 多 Agent 工作流中也提供 cron 命令入口。

### 主角团

可将可复用的单主角或协作团队绑定到会话：

```bash
# 使用内置的软件公司团队启动
mothx --expert software-company
```

TUI 中可使用 `/expert list`、`/expert show <id>`、`/expert bind <id>`、`/expert unbind` 与 `/expert switch <id>`。在两个主角之间切换会创建会话分叉，原会话的身份与历史保持不变。团队型主角会自动启用成员调度能力。生命周期、ESM 和本地主角包说明见[主角团](expert-teams.md)。

### Delegate 模式

```bash
# 启用阻塞式单子 Agent 委托
mothx --delegate

# 在 TUI 中运行时切换
/delegate on
/delegate status
/delegate off
```

Delegate 模式会注册 `delegate_subagent`，这是一个一次只处理一个边界清晰子任务的同步工具。适合多步调查或聚焦验证场景，主 Agent 只需接收摘要结果。

### A2A Master 模式

```bash
# 生成示例配置
mothx --init-a2a-master-config

# 启用 master 模式
mothx --enable-a2a-master
```

A2A Master 模式让你管理多个远程 A2A Agent，LLM 可自动通过 `a2a_dispatch` tool 分发任务。详见 [A2A 协议](a2a.md)。

## 选择模式

MothX 提供三种模式:

```bash
# Plan 模式 - 只读分析
mothx --mode plan

# Agent 模式 - 标准读写
mothx --mode agent

# YOLO 模式 - 完全访问 (默认)
mothx --mode yolo
```

| 模式 | 文件系统 | 网络 | 用途 |
|------|---------|------|------|
| **Plan** | 只读 | ✗ | 分析、规划 |
| **Agent** | 读写 | ✗ | 日常开发 |
| **YOLO** | 完全 | ✓ | 系统级操作 |

## 基本交互

### 常用命令

```bash
/mode plan      # 切换到 Plan 模式
/mode agent     # 切换到 Agent 模式
/model          # 查看当前模型
/think          # 切换思考级别
/clear          # 清空对话
/help           # 显示帮助
/quit           # 退出
```

### 键盘快捷键

| 快捷键 | 功能 |
|--------|------|
| `Enter` | 提交当前 prompt |
| `Alt+Enter` / `Ctrl+J` | 在 prompt 编辑器中插入换行 |
| `Tab` | 循环切换模式（`plan` → `agent` → `yolo`） |
| `Esc` | 中止当前操作、审批或问题提示 |
| `Ctrl+O` | 打开最新工具/详情 Modal；再次按下、`Esc` 或 `q` 可关闭 |
| `Ctrl+E` | 打开 ESM 进度面板；再次按下、`Esc` 或 `q` 可关闭 |
| `Ctrl+G` | 切换简洁/完整事件显示；默认使用简洁模式 |
| `Up` / `Down` | 在多行输入内移动；位于首/末行时浏览 prompt 历史；详情/进度面板打开时滚动内容 |
| `PgUp` / `PgDn` | 详情/进度面板打开时按页滚动 |
| `Home` / `End` | 移动到当前输入行首/行尾；详情/进度面板打开时跳到顶部/底部 |

已完成的对话块会打印到终端原生 scrollback，因此可以使用终端自带的鼠标滚轮、触控板或滚动条查看更早的 transcript 输出。

## 使用示例

### 代码解释

```bash
mothx -P "解释 main.go 的作用"
```

### 代码生成

```bash
mothx -P "写一个 Go HTTP 服务器"
```

### 文件操作

```bash
mothx -P "在当前目录创建一个 README.md"
```

### 继续会话

```bash
# 继续最近的会话
mothx --continue

# 恢复特定会话
mothx --resume <session-id>
```

## 技能系统

技能是可复用的提示片段，帮助强制执行项目约定：

```bash
# 列出可用技能
> /skills

# 激活技能
> /skill my-conventions
```

创建技能的方式是添加 `SKILL.md` 文件：
- **全局**: `~/.mothx/skills/<name>/SKILL.md`（所有项目可用）
- **项目**: `.skills/<name>/SKILL.md`（项目特定，覆盖全局）

详见 [技能系统](skills.md) 文档。

## IDE 集成

MothX 可以通过 Agent Client Protocol (ACP) 集成到你的 IDE：

### VS Code

在 `settings.json` 中添加：
```json
{
  "acp.agents": {
    "mothx": {
      "command": "mothx",
      "args": ["acp", "--mode", "agent", "--multi-agent"]
    }
  }
}
```

### JetBrains IDE

导航到 `Settings → Tools → ACP Agents` 并添加：
- **Name**: MothX
- **Command**: `mothx`
- **Arguments**: `acp --mode agent`

详见 [ACP 协议](acp.md) 文档。

## 故障排查

如果遇到问题，可以运行内置诊断命令：

```bash
mothx doctor
```

这会检查你的环境、配置文件、Provider、沙箱、MCP、会话、技能和上下文文件，并报告任何问题。

## 下一步

- 阅读 [配置详解](configuration.md) 自定义设置
- 查看 [工具参考](tools.md) 了解可用工具
- 尝试 [多 Agent 模式](cli-reference.md#多-agent-模式) 进行委托调查和 cron 命令入口
- 尝试 [Delegate 模式](cli-reference.md#delegate-模式) 进行阻塞式单子 Agent 调查任务
- 了解 [安全模型](security.md) 保护你的系统
- 探索 [技能系统](skills.md) 创建可复用提示片段
- 设置 [IDE 集成](acp.md) 在 VS Code 或 JetBrains 中使用
- 查看 [场景演示](scenarios.md) 了解各模式的实际用法
