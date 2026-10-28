# 在线 Skill 市场集成

MothX 兼容市面上的 Skill 市场（源盾 / SkillHub / ClawHub），可以直接使用这些平台发布的技能包。

| 平台 | 地址 | 区域 |
|------|------|------|
| **源盾（Yuandun）** | [https://yuandun.gitee.com](https://yuandun.gitee.com/) | 中国 |
| **SkillHub** | [https://skillhub.cn](https://skillhub.cn/) | 中国 |
| **ClawHub** | [https://clawhub.ai](https://clawhub.ai/) | 海外 |

MothX 内建了 TUI Skill 市场，可以浏览、搜索、查看详情并安装源盾 / SkillHub / ClawHub
公开技能包。使用 `/skillhub` 打开。源盾排在市场列表最前面，并作为默认市场。
MothX 会为源盾请求带上 `X-Client-Source: MothX` 与 `User-Agent: MothX`，源盾用该头统计下载来源。

本指南涵盖：

1. [从市场安装技能](#从市场安装技能) — TUI 与命令用法
2. [技能格式兼容](#技能格式兼容) — 标准格式说明
3. [本地技能系统](#本地技能系统) — 已实现的功能
4. [Cron 基础设施](#cron-基础设施) — 定时任务基础

---

## 从市场安装技能

使用 `/skillhub` 打开内建市场。

在 `mothx serve` Web UI 中，也可以从侧边栏打开「技能市场」。Web UI 与 TUI 使用同一套
SkillHub / ClawHub adapter 和安全安装器，支持 Official、Browse、Search、详情、安装、更新和
当前 session 激活。项目安装目录根据当前 session workDir 推导，并受 `allowedWorkDirs` 限制。

| 按键 | 行为 |
|------|------|
| `Tab` / `Shift+Tab` | 切换源盾 / SkillHub.cn / ClawHub.ai |
| `[` / `]` | 切换 Browse / Search / Official |
| `/` | 编辑搜索词，`Enter` 执行搜索 |
| `Up` / `Down` | 选择 Skill |
| `Left` / `Right` | 游标市场（源盾 / ClawHub）或 SkillHub 页码翻页 |
| `Enter` | 加载详情和文件清单 |
| `d` | 打开可滚动的文件、安全报告和 evaluation 明细 |
| `c` / `s` | 在源盾或 SkillHub.cn Browse 中切换分类 / 排序 |
| `i` | 安装到当前 scope |
| `u` | 更新有新版本的已安装 Skill |
| `a` | 安装并在当前 session 激活 |
| `g` / `p` | 选择全局 / 项目 scope |
| `r` | 刷新 |
| `Esc` | 关闭市场 |

列表使用 `DL` 显示下载量，SkillHub.cn Browse 按下载量降序排列。
源盾 Browse 使用市场游标分页，同样支持分类与排序切换。
`[official]` 表示由配置的 Official 账号发布；`[certified]` 表示发布方已认证；
`[verified]` 是市场返回的 Skill 认证字段；`[risk]` 表示存在可疑风险。这些标记会分开显示，
因为账号来源不等同于平台认证。
市场提供相应数据时，详情还会显示来源、分类、tags、安全扫描摘要、evaluation 状态和下载端点；
`d` 明细视图会区分主下载来源和回退来源。
SkillHub.cn Browse 支持分类过滤，以及下载量、star、安装量、score、更新时间降序切换。
源盾 Browse 通过同一组快捷支持固定分类以及 COMPREHENSIVE / DOWNLOADS / STARS / LATEST 排序。
已安装 Skill 有新版本时显示 `[update]`，按 `u` 更新；若该 Skill 已在当前 session 激活，
更新后会重新加载其上下文。

也可以使用命令形式：

```text
/skillhub search <query>
/skillhub detail <market>/<id>
/skillhub install <market>/<id> [--global|--project] [--activate]
/skillhub installed
```

安装和激活是两个操作。安装只写入技能包并刷新本地列表；激活还会重建当前
session 的 agent，使下一次请求包含该技能。

下载的压缩包有大小限制，并会检查路径穿越、绝对路径、Windows 盘符和符号链接。
同名的本地手写 Skill 不会被覆盖；更新也只允许覆盖同一市场条目安装并带 metadata 的目录。
也可以继续把兼容的 Skill 目录手动放入本地目录。

---

## 技能格式兼容

MothX 的技能格式与源盾 / SkillHub / ClawHub 标准完全一致：

```
skill-name/
├── SKILL.md              # 必需：技能定义文件
└── references/           # 可选：按需加载的参考文件
    ├── api-guide.md
    └── examples.md
```

### SKILL.md 标准格式

```markdown
# 技能名称

简短描述。

## 规则

- 规则 1
- 规则 2

## 示例

...
```

### 参考文件

技能可以包含 `references/` 目录下的参考文件，通过 `skill_ref` 工具按需加载：

```
> skill_ref(skill="go-expert", ref="references/api-guide.md")
→ 返回 api-guide.md 的内容
```

这允许技能包含大量参考资料而不占用系统提示词空间。

---

## 本地技能系统

除了从市场下载，你也可以直接创建本地技能。

### 技能目录

| 类型 | 位置 | 作用域 |
|------|------|--------|
| 全局 | `~/.mothx/skills/`（Linux/macOS）或 `%APPDATA%\mothx\skills\`（Windows） | 所有项目 |
| 项目 | `.mothx/skills/`、`.skills/` 或 `skills/` | 当前项目，按此顺序覆盖同名全局技能 |

### 创建技能

```bash
mkdir -p ~/.mothx/skills/go-expert
cat > ~/.mothx/skills/go-expert/SKILL.md << 'EOF'
# Go Expert

专家级 Go 编码规范。

## 规则

- 使用 `gofmt` 格式化代码
- 遵循 Effective Go 指南
- 返回错误，不要 panic
- 使用 `fmt.Errorf` 和 `%w` 包装错误

## 测试

- 编写表驱动测试
- 使用 `t.Run` 子测试
- 目标覆盖率 >80%
EOF
```

### 使用技能

```
> /skills
已加载 2 个技能:
  - go-expert (全局)
  - project-conventions (项目)

> /skill:go-expert
已加载技能: go-expert
```

### 配置

在 `settings.json` 中配置全局技能目录：

```json
{
  "skillsDir": "~/.mothx/skills",
  "skillHub": {
    "defaultMarket": "yuandun",
    "defaultInstallScope": "project",
    "officialHandles": ["user_0064faa7"],
    "markets": [
      { "id": "yuandun", "name": "源盾", "enabled": true, "apiURL": "https://yuandun.gitee.com" },
      { "id": "skillhub.cn", "name": "SkillHub.cn", "enabled": true },
      { "id": "clawhub.ai", "name": "ClawHub.ai", "enabled": true }
    ]
  }
}
```

`officialHandles` 控制 SkillHub.cn Official tab 聚合哪些账号。源盾默认使用
`https://yuandun.gitee.com` 作为 `apiURL`，只有使用镜像时才需要覆盖。源盾排在
SkillHub.cn 之前，并作为默认市场。项目技能目录会自动加载，无需额外配置。

---

## Cron 基础设施

MothX 已有内部 cron 基础设施（`internal/cron` 包）和 TUI 命令入口。Cron 存储将 session 绑定任务持久化到 `sessions.db` 的 `cron_jobs` 表，调度器每 30 秒检查一次到期任务。

### `/cron` TUI 命令

需要多 Agent 模式（`--multi-agent` 或 Ctrl+P 切换）：

```
> /cron add <描述>             — 添加定时任务
> /cron list                   — 列出定时任务
> /cron enable <id>            — 启用任务
> /cron disable <id>           — 禁用任务
> /cron remove <id>            — 删除任务
> /cron run <id>               — 立即运行任务
```

### Cron 任务数据模型

| 字段 | 描述 |
|------|------|
| `id` | 唯一任务 ID（如 `cron-1716883200`） |
| `name` | 任务简短描述 |
| `prompt` | 发送给子 Agent 的任务提示词 |
| `schedule` | 5 字段 cron 表达式 |
| `mode` | `agent` 或 `yolo` |
| `enabled` | 任务是否激活 |
| `last_run` | 上次执行时间戳 |
| `next_run` | 计算得出的下次执行时间 |
| `run_count` | 总执行次数 |
| `last_status` | `success`、`failed` 或 `running` |

### 调度器架构

```
调度器循环 (每 30 秒)
    │
    ├── 从存储列出所有已启用任务
    │
    ├── 检查每个任务：是否到期？
    │   ├── 从未运行 → 到期
    │   ├── NextRun 已过 → 到期
    │   └── 上次运行超过 1 小时 → 到期（兜底）
    │
    └── 到期任务 → 创建子 Agent
              │
              ├── 标记任务为 "running"
              ├── 通过 AgentManager 创建 Agent
              ├── 使用任务 prompt 运行 Agent
              ├── 收集结果
              └── 更新任务状态 (success/failed)
```

---

## 相关文档

- [技能系统](skills.md) — 本地技能格式和管理
- [配置详解](configuration.md) — 完整设置参考
- [安全与沙箱](security.md) — 沙箱和审批控制
