# Native Desktop 功能迁移进度

更新时间：2026-11-11

## 目标与边界

将 `desktop/` 的 ACP 设置与管理能力逐项迁移至纯 Go 的 `native/` 客户端。

- Native 只能作为 ACP 投影：所有运行时、会话、设置、知识库和管理数据经 `mothx acp` 访问。
- 不允许 Native 直接读写 `settings.json`、`serve.json` 或数据库。
- 设置使用 Native 的独立三级视图（分类 / 条目 / 内容），不使用聊天页小弹窗。
- 凭据、token、API key、App Secret 等敏感值仅提交，不从 ACP 响应或客户端状态回显。

## 已完成的基础结构

- Native Settings 已从弹窗改为独立主视图，保留聊天状态，并支持返回聊天。
- 设置导航对齐 Desktop 的分类和条目结构；按 ACP capability 隐藏未公开的管理能力。
- 会话历史是独立视图，展示全会话、搜索、工作区/模型/更新时间和 `lastRun` 状态投影。
- 历史视图与 Settings 视图互斥。
- Native 侧栏按 Desktop 层级包含搜索、会话历史、设置；外观切换位于左下连接区。

## 已实现的 Settings 面板

下列面板已有 Native ACP 状态模型、视图、生命周期失效处理和对应测试文件：

| 面板 | 主要 ACP 方法 / 能力 | Native 文件 |
|---|---|---|
| Application | `mothx/manage/application/get`, `patch`, `manageApplicationSettings` | `settings_model.go`, `settings_view.go` |
| Providers | `manage/settings/*`, `manage/providers/*`, `manageProviders` | `providers_model.go`, `providers_view.go` |
| MCP | `mothx/manage/mcp/list`, `set`, `manageMcp` | `mcp_model.go`, `mcp_view.go` |
| Skills | `mothx/manage/skills/list`, `set`, `manageSkills` | `skills_model.go`, `skills_view.go` |
| Memory | `mothx/manage/memory/get`, `put`, `manageMemory` | `memory_model.go`, `memory_view.go` |
| Cron | `mothx/manage/cron/*`, `manageCron` | `cron_model.go`, `cron_view.go` |
| Channels | `mothx/manage/channels/get`, `patch`, `manageChannels` | `channels_model.go`, `channels_view.go` |
| Serve | `mothx/manage/serve/get`, `patch`, `manageServeConfig` | `serve_model.go`, `serve_view.go` |
| Knowledge | `mothx/manage/knowledge-bases/*`, `manageKnowledgeBases` 及附加能力 | `knowledge_model.go`, `knowledge_view.go` |
| SkillHub | `mothx/manage/skillhub/*`, `manageSkillHub` / catalog 能力 | `skillhub_model.go`, `skillhub_view.go` |
| Experts | `mothx/manage/experts/*`, `manageExperts` | `experts_model.go`, `experts_view.go` |

本地页面也已提供：Appearance、Runtime（只显示启动时二进制解析来源）、About。

## 暂停时的在途工作

- **ACP Workers**：`manageACPProfiles` 的迁移刚开始，委派任务在暂停前可能已产生部分未审查改动。恢复时先检查工作树、编译和测试，再继续。计划范围只允许编辑 driver、model、MCP bridge 和 capability override；不得展示或编辑 argv、URL、环境变量或授权标志。

## 尚未迁移 / 待完成项

1. ACP Workers（当前在途）
2. Env（`mothx/manage/env/*`）
3. Stats（`mothx/manage/stats/*`）
4. Desktop 运行时选择的 Native 壳层等价方案：需保持“启动时本地状态”边界，不能由 ACP 或工作区设置冒充。
5. 会话历史的分页、scope/project 筛选，以及 session 的改标题、删除、fork、项目归属和工作目录操作。
6. Desktop 的 Projects、Worktrees、Subagent/Router、Deliveries 等非 Settings 管理/工作流投影。
7. 全量人工 UI 验收：真实 ACP 运行时、空态、失败态、窄窗口、敏感值不回显和跨 tab 请求失效。

## 恢复建议

1. 先运行 Native 模块检查：

   ```sh
   cd native
   /home/free/usr/go/bin/gofmt -l .
   /home/free/usr/go/bin/go test ./...
   /home/free/usr/go/bin/go test -race ./...
   /home/free/usr/go/bin/go vet ./...
   /home/free/usr/go/bin/go tool mygo vet .
   ```

2. 审查 ACP Workers 的在途改动，确认只走 `mothx/manage/acp/profiles/list|patch` 且 capability-gated。
3. 按“ACP Workers → Env → Stats → 会话/工作流投影”的顺序继续；每个面板完成后再开始下一个，以保持可验证的边界。

## 工作树提示

`native/` 当前整体为未跟踪目录，包含源文件、测试、嵌入 PNG 资源和本地构建忽略项。提交前需显式审查 `native/.gitignore`，确保 `assets/mothx.png` 与 `resources/icon.png` 被纳入版本控制，而 `build/` 与本地二进制继续忽略。
