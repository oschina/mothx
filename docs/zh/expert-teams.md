# 主角团

主角团让一个会话使用可复用的主角身份或协作团队。主角包定义 lead 人设、可选成员人设及其声明的能力。Runtime 会为会话统一解析一次；TUI、WebUI、Desktop/ACP 与渠道入口只投影这一份共享状态。

## 用主角启动

可以在启动时使用 `--expert`，也可以在 TUI 中管理绑定：

```bash
# 使用内置的软件公司团队启动新会话
mothx --expert software-company

# 在已有 TUI 会话中查看可用主角包
/expert list
/expert show software-company

# 绑定或移除主角身份
/expert bind software-company
/expert unbind
```

`/expert bind` 可为尚未绑定主角的会话建立绑定；`/expert unbind` 会移除当前会话的身份。两者都不会改写既有对话历史。

## 通过分叉切换主角

从一个非空主角切换到另一个主角时，会创建新的会话分支：

```text
/expert switch frontend-developer
```

源会话保留原来的主角、历史和身份提示词；新分支在当前对话边界绑定请求的主角。这样不会把两个主角身份混进同一段历史。

WebUI 主角面板和 Desktop 的 **Expert** 会话选项遵循相同规则：首次绑定和解绑更新当前空闲会话；替换已绑定主角会创建并打开分叉会话。

## 团队行为

团队型主角包会自动启用会话的多 Agent 能力，无需只为使用团队再额外添加 `--multi-agent`。lead 会获得团队名册，并可以按成员 ID 派发：

```text
subagent_spawn(member: "software-engineer", task: "实现这个聚焦修复，并运行相关测试。")
subagent_wait(timeout_ms: 30000)
```

成员仍是普通子 Agent：工具和模式限制来自主角包与会话策略；成员不能嵌套派发成员；高风险命令保护仍然生效。单人主角只改变 lead 身份，不会强制启用团队工具。

成员生命周期卡片来自 canonical child event 的投影。成员完成只更新自己的状态，并在活跃 lead 的 agent-loop 边界投递；它不会自行启动新的 lead run。

成员需要决策时向 lead 提问，而不是向用户提问：问题会进入会话邮箱（以 `[MEMBER_QUESTION]` steering 消息，或 `subagent_wait` 中 `status: question` 的待处理条目投递），由 lead 用 `subagent_answer(handle: "<成员>", question_id: "…", answer: "…")` 回答；用户只看到该提问在 lead 事件流上的投影。阻塞式 `delegate_subagent` 子 Agent 不会提问，因为它的调用方正停在工具调用里，无人能作答。

该邮箱通路在所有能派生成员的会话中都可用，不限于团队会话；只有绑定团队的会话才会在收尾轮为仍在运行的成员保持 run 打开。

## 与 ESM 的关系

主角团与 Enable Supervisor Mode（ESM）组合使用时不会创建第二套任务调度器。只有用户能创建、编辑、恢复或清除 ESM 目标。只有会话确实空闲且目标仍可自动运行时，既有 ESM continuation 才会启动下一次 lead run；成员终态事件不是续跑触发器。

团队绑定的 ESM worker 会保留 lead 身份与名册，也保留成员调度：该 worker continuation 会消费成员通知，并在收尾前等待成员。ESM 的 critic、audit 和 recovery 角色仍保持隔离：既不获得成员调度工具，也不会消费或等待会话成员。

## 添加本地主角包

主角包会从内置目录、用户配置目录的 `experts/` 文件夹以及项目目录延迟发现：

```text
<config-dir>/experts/<bundle-name>/
<project>/.mothx/experts/<bundle-name>/
```

同名时，项目包覆盖全局包，全局包覆盖内置包。一个主角包包含 `expert.json` 与 `agents/` 下的一个或多个 persona 文件；无效包会显示为不可用，且不能被绑定。包格式和架构设计请参阅[主角团实施方案](../proposal/expert-team-mothx-proposal.md)。

## 用内置 Skill 创建并安装主角团

MothX 内置了 `expert-creater` Skill。启用后，由当前会话的 Agent 根据你的描述创建并安装一个项目级主角团到 `.mothx/experts/<team-id>/`，其中包含校验过的 `expert.json` 与成员人设文件。它不会覆盖已有同名目录；需要更新已有团队时，请明确说明。

| 入口 | 启用指令 |
| --- | --- |
| TUI、WebUI 聊天 | `/skill expert-creater` |
| Desktop、ACP | `/expert-creater`；ACP 也接受 `/skill expert-creater` 或 `/skill:expert-creater` |

启用后，在下一条消息说明团队的用途、领队和成员职责，例如“创建一个移动端发布主角团，包含领队、Android 工程师和测试审查员”。创建完成后用主角团选择器或 `/expert bind <team-id>` 绑定；若当前会话已经绑定其他主角团，则沿用既有分叉切换规则。
