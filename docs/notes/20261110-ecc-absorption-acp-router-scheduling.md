# ECC 仓库吸收笔记：ACP 对接、Router 预设与调度优化

> 日期：2026-11-10
> 来源：`/home/free/src/ECC`（版本 2.2.3，71 agents / 314 skills / 98 commands）
> 目的：把 ECC 中可复用的 agent 集成、预设与调度经验映射到 MothX Router 模式，
> 并明确哪些东西**不应**照搬。

## 0. 结论摘要

1. **ECC 没有 ACP 协议实现。** 全仓库检索 `acp` 只命中无关文本
   （Perl 技能中的 MetaCPAN、lockfile 中的 base64）。ECC 的“多 agent 接入”
   是按宿主分别投影配置文件（`.claude/`、`.codex/`、`.cursor/`、
   `.opencode/`、`.kiro/`、`.kimi/`、`.qwen/`、`.trae/`、`.zed/`、
   `.qoder-plugin/`、`.openclaw/`、`.hermes/` 等），不是 ACP 客户端/服务端。
   因此 **ACP 线协议没有可移植内容**；可借鉴的是“宿主矩阵”和“按角色预设”的方法。

2. **MothX Router 在 ACP 上已经领先 ECC。** MothX 已有：
   - 9 个经过验证的 ACP agent 启动预设（`internal/agentruntime/router_presets.go`）；
   - 多 profile 绑定、非阻塞审批、连接池复用、并发上限；
   - 任务契约、角色/写集、worktree 变更集、`subagent_integrate`；
   - Runtime 统一拥有的决策、事件、子会话历史、mailbox 唤醒。
   ECC 没有对应物，**不能反向“补”协议能力**。

3. **真正可吸收的是三件事：**
   - **角色预设**：ECC 的 71 个专用 agent 本质上是角色提示词分类；
     MothX Router 的五种 `SubagentRole` 可以吸收每个角色的工作纪律，
     让外部 worker 的提示词不再只有一句 “investigate/implement”。
   - **调度纪律**：ECC 的 `orch-review`、`continuous-agent-loop`、
     `loop-operator`、`team-agent-orchestration` 与 `ecc2` daemon
     提供了“先并行、去重后再验证、区分证据等级、按优先级/预算调度、
     停顿检测、合并队列”的成熟规则。
   - **预设/模板**：ECC2 的 `agent_profiles`（继承 + model + 工具白/黑名单 +
     预算 + 追加提示词）和 `orchestration_templates`（步骤、变量、
     handoff 链、槽位预检）是 Router 后续做“任务模板/角色预设”的好参照。

4. **不要照搬：** ECC 的 314 skills/hook 大礼包、ECC2 的独立 session 状态机与
   DB 所有权、制造业 `production-scheduling` 技能、任意 wall-clock 截止时间。

## 1. ECC 的真实结构

| 层 | 位置 | 本质 | 对 MothX 的价值 |
| --- | --- | --- | --- |
| Agent/Skill/Command 包 | `agents/` `skills/` `commands/` | Markdown 提示词 + 工具/模型声明 | 角色提示词与命令路由模式 |
| 宿主投影 | `.claude/` `.codex/` `.cursor/` `.opencode/` `.kiro/` `.kimi/` `.qwen/` `.trae/` `.zed/` `.qoder-plugin/` `.openclaw/` `.hermes/` | 各 IDE/CLI 的原生配置格式 | “一个能力，多端投影”的清单 |
| 编排命令 | `commands/multi-*.md` `commands/orch-*.md` `commands/astra-review.md` `commands/santa-loop.md` | 多模型/子 agent 协作流程 | 并行、后台、止损、门禁 |
| 原生 workflow | `workflows/orch-review.workflow.js` | 结构化 fan-out + 去重 + 对抗验证 | **最值得吸收的调度样本** |
| 控制平面 alpha | `ecc2/`（Rust） | SQLite session store、daemon、cron、远程派发、模板、预算 | 调度/队列/恢复的参考实现 |
| Provider 抽象 | `src/llm/`（Python） | provider/model 预设选择器 | 预设选择器的极简做法 |

关键文件：

- `workflows/README.md`、`workflows/orch-review.workflow.js`
- `ecc2/src/config/mod.rs`（`AgentProfileConfig`、`OrchestrationTemplateConfig`、预算/风险阈值）
- `ecc2/src/session/daemon.rs`、`ecc2/src/session/manager.rs`
- `skills/continuous-agent-loop/SKILL.md`、`agents/loop-operator.md`
- `skills/team-agent-orchestration/SKILL.md`
- `skills/council-multi-model/SKILL.md`
- `skills/plan-orchestrate/SKILL.md`
- `skills/agent-sort/SKILL.md`

## 2. ACP 对接：ECC 没有，但宿主矩阵有参考价值

### 2.1 事实核验

- ECC 的安装器只复制 Markdown/JSON/TOML 配置到各宿主的原生目录；
  没有 `initialize` / `session/new` / `session/prompt` / `session/update`
  之类的 ACP JSON-RPC 实现。
- ECC 的“多模型”通过外部 CLI/包装器实现：
  `npx ccg-workflow`、`codeagent-wrapper`、`codex` CLI、Antigravity 等，
  走的是子进程/stdin，不是 ACP。
- 因此，**“吸收其他 agent 的 ACP 对接”在 ECC 里没有源码可抄**。
  能做的是核对 ECC 覆盖的宿主名单，检查 MothX 预设是否漏掉真实 ACP worker。

### 2.2 宿主矩阵对照

ECC 覆盖的宿主：Claude Code、Codex、Cursor、Gemini、OpenCode、Kiro、
Kimi Code、Qwen、Trae、Zed、Qoder、OpenClaw、Hermes、CodeBuddy、Pi、AdaL。

MothX 现有 ACP 预设：`opencode`、`claude-code`、`codex`、`gemini`、
`qwen-code`、`copilot`、`cursor`、`kiro`、`openclaw`。

结论：

- **不要**因为 ECC 有 Kimi/Trae/Zed/Qoder 就新增 ACP 预设。
  Kimi Code 的 ECC 适配是技能目录；Trae/Qoder 是 IDE/插件面；
  Zed 是 ACP 客户端而非 worker。把它们写成 `command` 会制造“看起来能连、
  实际起不来”的假预设，违反 `router_presets.go` “每条都经过验证”的契约。
- 新增预设的唯一充分条件：该工具自己的文档或 `--help` 明确提供
  启动 ACP server 的命令，并且能在 `mothx doctor` 中完成
  `initialize` 预检。

### 2.3 ECC 的“能力投影”原则

ECC 每个宿主目录只放该宿主真正能发现的内容，并在 README 写明
“不会触碰哪些配置”。这对应 MothX 的一条既有原则：
**一个 Runtime 语义，多个薄投影**。Router 的 profile/预设属于全局配置，
TUI/CLI/ACP/serve 只做投影，不各自维护命令模板。ECC 的做法可作为
新增 ACP 预设时的文档模板：命令、安装方式、已验证版本、不会触碰的配置。

## 3. 预设进入 Router 模式

### 3.1 ECC 的三类预设

1. **Agent Profile（`ecc2/src/config/mod.rs`）**
   - `inherits`、`agent`、`model`、`allowed_tools`、`disallowed_tools`、
     `permission_mode`、`add_dirs`、`max_budget_usd`、`token_budget`、
     `append_system_prompt`。
   - 支持继承链，检测循环；`append_system_prompt` 按层拼接。
   - 意义：预设不是“一条命令”，而是“命令 + 模型 + 权限 + 工具 + 预算 + 提示词”。

2. **Orchestration Template（同一文件）**
   - `steps[]`：`name`、`task`、`agent`、`profile`、`worktree`、
     `project`、`task_group`。
   - 变量插值、必填变量缺失即失败、步骤槽位不够即拒绝启动、
     首步作为锚点并给后续步骤发 handoff。
   - 意义：**任务模板是 Router 的“多步预设”**，而不是让模型自由发挥。

3. **DAILY / LIBRARY 分类（`skills/agent-sort`）**
   - 用仓库证据（扩展名、lockfile、框架配置、CI）决定哪些能力默认加载，
     哪些只保留可检索。
   - 意义：Router 的 profile/preset 也应有“默认绑定”和“可检索库”两层，
     避免一次绑定十几个 worker 造成选择困难和上下文噪声。

### 3.2 MothX 现状与差距

已有：

- `ACPAgentPreset` → `config.ACPAgentProfile` 的一键展开；
- `RouterBinding` 的有序 allowlist + 默认 profile；
- `delegate_subagent` 的 `agent` 参数在 Router 下用 schema 固定/枚举；
- profile 级 `permissionPolicy`、`environment`、`terminal`、
  `maxConcurrentChildren`、`allowParallelBaseWorkspaceWrites`；
- TUI `/router` 面板的 preset picker。

差距（按价值排序）：

| 优先级 | 差距 | 建议 |
| --- | --- | --- |
| P0 | 外部 worker 只有角色名，没有角色工作纪律 | **已实现**：`router_role_playbook.go` 为五种角色注入有界 playbook |
| P0 | Router lead 缺少调度与证据分级纪律 | **已实现**：Router 系统提示词增加 Scheduling discipline / 证据四级分类 |
| P1 | profile 无“provider/模型家族”元数据，无法判断独立评审 | 增加可选 `providerFamily`（或 `vendor`）字段；只用于提示和 UI 标签，不参与授权 |
| P1 | 没有“任务模板”预设 | 借鉴 `OrchestrationTemplateConfig`，做 Runtime 拥有的 Router mission preset：步骤、目标 profile、角色、scope、验收、依赖 |
| P1 | 预设只有命令，没有默认预算/并发/环境提示 | 扩展 `ACPAgentPreset` 的可选默认字段（并发上限、环境变量名、安装提示）；不做隐式权限提升 |
| P2 | profile 没有继承 | 若未来 profile 数量增长，再引入 `inherits`；当前 9 个预设不值得引入复杂度 |
| P2 | 无“默认绑定组”概念 | 可提供 `read-only-review`、`implementation`、`full-stack` 等绑定预设，但必须仍是显式 allowlist |

### 3.3 从 ECC 学到的“路由即分类，未知即失败”

`skills/plan-orchestrate` 的核心不是“智能”，而是：

1. 先按标签分类步骤；
2. 只发射目标命令**确实能携带**的 scope；
3. 不能携带就 `BLOCKED`，绝不发一条看似编排、实际无 scope 的命令。

MothX Router 已经采用同样的失败关闭策略（`delegate_subagent` 的
`agent` schema、`ResolveRouterBinding` 的审批策略校验、
`internal/architecture` 的构建栅栏）。后续新增“自动选 profile”时，
必须保持这条原则：**无法证明匹配就不自动路由**，让 lead 显式选择。

### 3.4 从 ECC 学到的“同源评审不是独立评审”

`skills/council-multi-model` 明确区分：

- `cross-provider external critique`；
- `same-provider external critique`；
- `provider relationship unverified`。

MothX Router 的 `review` 角色目前只要求“独立检查”，但没有告诉 lead
同源模型不等于独立。已在 Router 系统提示词中加入 same-family 标注要求；
后续可在 profile 上增加 provider 元数据，把这个判断从“提示”升级为“可见事实”。

## 4. 调度与优化

### 4.1 ECC 的四个可复用调度模式

**(A) fan-out → dedup barrier → adversarial verify（`orch-review.workflow.js`）**

- 按维度并行评审：质量、语言、条件安全；
- 以规范化 `evidence` 为 key 去重（标题/行号会漂移，代码片段不会）；
- 只对去重后的 CRITICAL/HIGH 启动独立验证者；
- 验证者默认“无法确认即驳回”，不把“未验证”降级为 advisory；
- 任一维度失败 → 整体不得干净 `APPROVE`；
- 实测 11 条原始发现去重为 4 条，验证成本约减半。

对应 MothX：Router 已经有 read-only 并发和 `review` 角色，但**没有
“去重后再验证”的显式屏障**。建议把“同一 evidence 的重复发现合并”
写进 Router lead 纪律（已写入提示词），再评估是否做 Runtime 级统计。

**(B) 强制后台 + 关键路径才等待（`commands/multi-*.md`）**

- 外部模型调用一律 `run_in_background: true`；
- 只有被阻塞时才轮询，且超时要询问用户而不是杀进程；
- 外部模型零文件写权限，所有落盘由主 agent 完成。

对应 MothX：`subagent_spawn` + mailbox 唤醒已经是更优实现；
本次补充 lead 提示词“不要反射式 wait、不要重复 wait 收通知”。

**(C) 停顿/重试/成本三线告警（`loop-operator`、`continuous-agent-loop`）**

- 连续两个检查点无进展 → 升级；
- 相同堆栈重复失败 → 降低 scope；
- 成本漂移超出预算窗口 → 停；
- 合并队列停滞 → 冻结并审计。

对应 MothX：已有执行租约、长任务连续性、worker 结果截断；
缺少 per-Run 的“总 worker 数 / 总写 worker 数 / 总回传字节 / 可选成本”
预算与停顿检测。建议作为 P1，且**不使用 wall-clock 截止时间**。

**(D) 优先级 + 槽位预检 + 心跳恢复（`ecc2` daemon）**

`ecc2/src/session/daemon.rs` 每个心跳周期做：

- `check_sessions`（心跳/陈旧会话）；
- `run_due_schedules`（cron，最多 `max_parallel_sessions`）；
- `run_remote_dispatch_requests`（按 `TaskPriority`：low/normal/high/critical）；
- `coordinate_backlog`、`auto_merge_ready_worktrees`、
  `auto_prune_inactive_worktrees`、`activate_pending_worktree_sessions`；
- 启动时把 pid 已死的 running session 标记 failed。

`manager.rs` 的关键细节：

- 模板启动前计算 `available_slots = max_parallel_sessions - live_sessions`，
  不够就整体拒绝，不做半启动；
- 远程派发记录 success/failure，并按优先级保留；
- `auto_dispatch_limit_per_session` 限制单会话自动派发量；
- 预算/风险阈值触发 advisory/warning/critical。

对应 MothX：**不要引入第二个 session 调度器**。MothX 已有
`internal/serve` 的 cron、Run admission、mailbox 唤醒、lease fence。
可吸收的是“槽位预检”和“优先级”两个语义，放到 Router 的
delegation budget 内，而不是新建 daemon。

### 4.2 MothX Router 已有能力（避免重复建设）

- 连接池：同一 profile 复用 ACP 进程，多 remote session 隔离；
- 并发：profile `maxConcurrentChildren`，读 worker 与隔离 implement worker
  共享上限；base-workspace writer 默认独占，可显式 opt-in；
- 写隔离：implement worker 用 Runtime worktree，变更集不可变，
  `subagent_integrate` 独占集成；
- 上下文：worker 结果 16 KiB 截断，父级只收完成信封；
- 生命周期：`session_child_runs`、mailbox 唤醒、durable Run、lease fence；
- 审批：Router 只允许非阻塞策略，交互式 profile 在激活时拒绝。

### 4.3 建议的调度优化路线

**P0（本次已做）**

1. Router lead 提示词加入：
   - 异步优先、关键路径才 wait、不重复 wait；
   - fan-out 前去重；
   - 证据四级分类（verified / worker-reported / not-verified / failed）；
   - same-family 评审标注；
   - 结果有界，不回灌完整 transcript。
2. 五种 `SubagentRole` 注入有界工作纪律（investigate/implement/test/review/integrate）。

**P1（下一步，建议单独立项）**

3. **Delegation budget**：每父 Run 记录 `workersTotal`、`workersLive`、
   `writersLive`、`resultBytes`；超过上限时拒绝新 spawn 并给出明确原因。
   可选 per-profile token/cost 上限，只有在 worker 能可信上报 usage 时启用。
4. **去重屏障**：对 review 角色的发现按规范化 evidence 去重；
   同一 evidence 只启动一个验证者；把 `raw/unique/confirmed/refuted`
   写入 child history 统计。
5. **停顿检测**：不是 wall-clock kill，而是“连续两个检查点无进展”
   或“同一失败签名重复 N 次”时，要求 lead 降低 scope 或显式取消。
6. **优先级预约**：`critical/high/normal/low` 只影响同一会话内
   已排队 delegation 的启动顺序；不改变 Run 所有权与 lease。

**P2（观察后再做）**

7. **Router mission template**：Runtime 拥有的命名模板，
   步骤含 target profile、role、read/write scope、deliverable、
   verification、depends_on；启动前做槽位预检，失败整体拒绝。
8. **Profile 预设继承**：只在 profile 数量显著增长后引入；
   必须保留 settings.json 向后兼容。
9. **Router eval**：用 pass@k / pass^k 衡量不同 profile 组合的
   完成率、平均轮次、结果字节、成本；先做离线统计，不做自动改配置。

## 5. 明确不吸收的内容

| ECC 内容 | 不吸收的原因 |
| --- | --- |
| 314 skills / 大量 hooks 的默认装载 | 上下文成本高；MothX 用 skill_ref/按需发现更合适 |
| ECC2 的独立 session 状态机、SQLite 表、PID 心跳 | 与 MothX “Run/lease/session 单一所有者”冲突；会造出第二套真相 |
| `production-scheduling` 制造业调度 | 领域知识，不是 agent 调度；DBR/SMED 不能直接映射到 LLM worker |
| 任意 wall-clock 超时杀 worker | 违反 MothX 长任务连续性目标；只允许显式取消/安全边界 |
| 把 ECC agents 全量复制成 MothX experts | 角色提示词可借鉴，但全量复制会稀释 Router 的五角色模型 |
| 未经验证的 ACP 预设 | 会产生“配置成功、启动失败”的假能力，破坏 preset 契约 |

## 6. 本次落地的代码

1. `internal/agentruntime/router_role_playbook.go`
   - 新增五种角色的工作纪律；由 `SubagentTaskContract.RenderPrompt`
     统一渲染，ACP bridge 与未来 adapter 不各自拼字符串。
   - 角色仍是 per-delegation 提示，不是文件系统授权边界。

2. `internal/agentruntime/router_task_contract.go`
   - `RenderPrompt` 在 Role 之后插入 `## Role playbook`。

3. `internal/agent/system_prompt.go`
   - Router lead 增加 Scheduling discipline 与证据四级分类；
   - 明确“同模型/同 provider 家族的评审不是独立验证”。

4. 测试：
   - `TestSubagentTaskContractRendersRolePlaybook` 验证五种角色都渲染、
     未知角色不渲染、prompt 仍有界；
   - `TestBuildSystemPromptModes` 验证 Router 提示词包含调度纪律且
     不继承 yolo 权限契约。

验证命令：

```bash
gofmt -w internal/agentruntime/router_role_playbook.go \
  internal/agentruntime/router_task_contract.go \
  internal/agentruntime/router_task_contract_test.go \
  internal/agent/system_prompt.go internal/agent/agent_test.go
go test ./internal/agentruntime -run 'TestSubagentTaskContract' -count=1
go test ./internal/agent -run 'TestBuildSystemPrompt' -count=1
```

## 6.1 主角团落地结果（2026-11-10）

按“12 个默认团 / 58 个角色”的方案落地。角色命名统一使用职责制
（`前端负责人`、`数据库与迁移负责人`），不再使用真人姓名。

| 主角团 | 角色数 | 角色 |
| --- | --- | --- |
| backend-platform | 5 | 后端负责人、API 与契约负责人、数据库与迁移负责人、服务运行时负责人、后端验证负责人 |
| web-frontend | 5 | Web 前端负责人、React/Next.js 负责人、Vue/Angular 负责人、设计系统与可访问性负责人、前端性能负责人 |
| mobile-crossplatform | 5 | 移动负责人、Android/KMP 负责人、Flutter 负责人、React Native 负责人、iOS/Swift 负责人 |
| systems-lowlevel | 4 | 系统负责人、Rust 负责人、C++ 负责人、Go 系统负责人 |
| enterprise-jvm-dotnet | 5 | 企业级负责人、Java/Spring 负责人、Kotlin 负责人、C#/.NET 负责人、PHP/Ruby 负责人 |
| qa-testing | 5 | 质量负责人、TDD 负责人、端到端测试负责人、回归分析负责人、静默失败审计负责人 |
| security-compliance | 5 | 安全负责人、应用安全负责人、Agent 安全负责人、供应链审计负责人、合规评审负责人 |
| devops-sre | 5 | 平台运维负责人、Kubernetes 负责人、CI/CD 与发布负责人、可观测性负责人、配置与环境负责人 |
| data-ml-ai | 5 | 数据与模型负责人、MLOps 负责人、PyTorch 负责人、检索与推荐负责人、LLM 评测负责人 |
| product-architecture | 5 | 产品与架构负责人、产品规划负责人、架构设计负责人、代码考古负责人、规格挖掘负责人 |
| docs-research | 4 | 文档与研究负责人、文档治理负责人、代码库入门负责人、深度研究负责人 |
| agent-platform | 5 | Agent 平台负责人、Agent 架构负责人、评测与 Harness 负责人、循环运维负责人、评测负责人 |

加上原有 `software-company`（5 角色）和 `frontend-developer`（1 角色），
内置目录现在是 **14 个主角团 / 64 个角色**。

实际嵌入体积（按文件字节计，Go embed 不压缩）：

- 新增 12 个团：**142,363 字节（约 139 KiB）**，其中 12 个聚焦 `SKILL.md` 约 16.2 KiB
- 现有 2 个团：20,012 字节
- `experts/` 总计：**162,375 字节（约 159 KiB）**
- 磁盘占用 `du -sh` 为 448 KiB（块对齐开销，不是二进制增量）

验证：

```bash
go build ./...
go test ./internal/expert ./internal/agent ./internal/agentruntime -count=1
go test ./internal/tui ./internal/acp -run 'Expert|expert' -count=1
go test ./internal/serve/openaiapi -run 'Expert' -count=1
go test ./internal/agentruntime -run 'TestSeededRoleBasedTeam|TestBuilderLoadsRoleBasedTeamSkill' -count=1
make test-ui
```

## 7. 一句话总结

ECC 值得吸收的不是 ACP 代码（它没有），而是**“角色预设 + 去重后再验证 +
证据分级 + 槽位/预算/停顿调度”**这套编排纪律；MothX Router 的协议、
隔离和生命周期已经更完整，本轮把角色与调度纪律补进 Runtime 拥有的提示词，
其余按 P1/P2 单独立项，避免在 Router 里再造一个 ECC2。
