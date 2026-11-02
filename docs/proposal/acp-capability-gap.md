# ACP 能力差距清单（vs TUI / WebUI）

> Date: 2026-07-26
> Status: 互操作性参考（2026-09-06 更新：desktop 已重构回纯 ACP 技术栈，新缺口清单见 `docs/proposal/desktop-acp-frontend-gap-proposal.md`，本文保留作历史对照）
> 关联: `docs/proposal/desktop-client-packaging-proposal.md`
> 方法：逐项对照 `internal/acp/acp.go`、`internal/tui/`、`internal/serve/openaiapi/` 源码

## ACP 现状（已有能力）

协议方法：

- `initialize`（能力声明 + mothx 扩展命名空间 `mothx.dev`：`requestQuestion`、`sessionEvent`）
- `session/new` / `session/load` / `session/resume` / `session/prompt` / `session/cancel`
- `session/close` / `session/delete` / `session/list`（非标准扩展，已声明 capability）

通知（`session/update`）：

- `agent_message_chunk`（文本流）、`agent_thought_chunk`（thinking 流）
- `tool_call` / `tool_call_update`（pending/in_progress/completed/failed，含 edit diff rawOutput）
- `plan`（计划步骤）
- `usage_update`（上下文 used/size + 累计 cost）
- `_mothx/session_event` 扩展（status、compaction_start/finished、turn_start/finished）

反向请求（agent → client）：

- `session/request_permission`（工具审批，含 allow_always 等选项）
- question 扩展（question 工具经 request_permission 通道）

其他：

- per-session MCP servers（`session/new` 传入，HTTP/SSE）
- `/systeminit` 展开（唯一支持的 slash command）
- 进程级启动 flag：mode、thinking、multiAgent、delegate、workflows、browser、websearch

## 差距清单

### A. Prompt 输入能力

| 能力 | TUI | WebUI | ACP | 备注 |
| --- | --- | --- | --- | --- |
| 文本 prompt | ✅ | ✅ | ✅ | |
| 图片 prompt | ✅ 剪贴板 | ✅ 上传 | ❌ `promptCaps.Image: false`，`promptToText` 直接拒绝 image/audio/resource | agent loop 本身已支持（`RunWithUserMessage` 接受 `provider.Message`），ACP 只需把 content block 转成 message，**补齐成本低** |
| 嵌入资源（resource/embeddedContext） | — | — | ❌ 拒绝 | ACP spec 的 resource_link 已支持（降级为文本） |

### B. 会话运行时控制

| 能力 | TUI | WebUI | ACP | 备注 |
| --- | --- | --- | --- | --- |
| per-session 工具开关（webSearch/browser/a2aMaster/delegate/multiAgent/workflows） | ✅ | ✅ 运行时切换 + 持久化到 session | ❌ 只能在进程启动时全局定死 | 第三方桌面客户端刚需 |
| mode 切换（plan/agent/yolo） | ✅ `/mode` | ✅ | ❌ 启动 flag 固定 | |
| thinking level 切换 | ✅ | ✅ | ❌ 启动 flag 固定 | |
| model / provider 切换 | ✅ `/model` `/defaultModel` | ✅ 模型选择器 | ❌ | 第三方客户端模型选择器需要 |
| 模型列表查询 | ✅ | ✅ `/v1/models` | ❌ | mothxwork 通过 ACP 拉模型列表，目前拿不到 |
| 手动 compact | ✅ `/compact` | ✅ | ❌ | |
| 清空会话（/clear） | ✅ | ✅ | ❌（只能 close + new） | |

### C. Slash commands

- WebUI 在 HTTP 层支持 16 个：`/clear` `/mode` `/model` `/defaultModel` `/models` `/sessions` `/status` `/compact` `/delegate` `/alloweditpath` `/allowautoedit` `/workflows` `/skill` `/skills` `/rule` `/help`
- ACP 只展开 `/systeminit`
- TUI 另有：`/auth` `/settings` `/mcpmgr` `/cron` `/esm` `/stats` `/skillhub` `/agent` `/btw` `/reload` `/statusline` `/quit`

### D. 管理/配置面

| 能力 | TUI | WebUI | ACP | 备注 |
| --- | --- | --- | --- | --- |
| Provider / API key onboarding | ✅ `/auth` | ✅ settings 页面 | ❌ `AuthMethods: []` | 桌面首次启动必须解决；最务实的路径是桌面读写 `~/.mothx/settings.json` 或复用 serve 的 settings API，不一定要走 ACP |
| settings 读写 | ✅ `/settings` | ✅ | ❌ | |
| skills 列表 / 启用 | ✅ | ✅ | ❌（`skill_ref` 工具在，但客户端无法查看/切换） | |
| cron 任务 | ✅ `/cron` | ✅ | ❌ | |
| stats 用量统计 | ✅ `/stats` | ✅ | ❌（仅 per-session usage_update） | |
| memory.md | ✅ | ✅ | ❌ | |
| skillhub | ✅ | ✅ | ❌ | |

### E. 事件/通知

| 能力 | TUI | WebUI | ACP | 备注 |
| --- | --- | --- | --- | --- |
| 文本/thinking 流 | ✅ | ✅ | ✅ | |
| 工具调用 + diff | ✅ | ✅ | ✅ | |
| plan | ✅ | ✅ | ✅ | |
| usage / cost | ✅ | ✅ | ✅ | |
| compaction/turn/status | ✅ | ✅ | ✅（扩展通知） | |
| sub-agent 状态 | ✅ | ✅ | ❌ | 多 agent 开启时客户端需要 |
| ESM 状态 | ✅ | 部分 | ❌ | |
| 工具结果中的图片 | ✅ | ✅ | ❌（`textToolContent` 只发文本） | imageproc 图片结果应映射为 ACP image content block |

### F. 进程/会话模型

- ACP 已支持单进程多 session（与 mothxwork 的共享 lease 模型匹配）。✅
- session pool / idle 淘汰：ACP 无（桌面壳场景用不到）。➖
- 会话标题：list 有 title。✅

## 补齐优先级建议（面向第三方客户端互操作）

> 注：桌面版已改用 serve 单通道，本节重新定位为第三方 ACP 客户端（mothxwork、Zed 类编辑器）对接时的补齐依据，按第三方实际需求排期。

### P0 — 第三方客户端可用性阻塞项

1. **图片 prompt**：content block → `provider.Message`，打开 `promptCaps.Image`。
2. **模型列表 + 切换**：扩展方法（如 `mothx/models/list`、`mothx/session/set_model`），或经 `_meta` 协商。
3. **per-session 运行时控制**：mode、thinking、工具开关（扩展方法 `mothx/session/configure`，映射到现有 capability 语义）。
4. **ACP 协议文档**：方法/参数/通知/错误码/扩展命名空间，落到 `docs/`，作为与第三方客户端对接的契约。

### P1 — 第三方客户端完整体验

5. 核心 slash commands 子集（`/clear` `/compact` `/mode` `/model` `/skills`）在 ACP 层处理或映射。
6. Provider/auth onboarding 方案定型（读写 settings.json vs ACP 扩展方法）。
7. 工具结果图片 → ACP image content block。
8. sub-agent 状态事件。

### P2 — 与 WebUI 全量对齐

9. skills/cron/stats/memory/skillhub 管理面（如需，可评估 `mothx/http` 进程内隧道方式复用 serve API mux，避免逐端点扩展）。
10. ESM 状态事件。

## 关键结论

- ACP 的**会话核心链路是完整的**（多 session、流式、审批、plan、usage、MCP），缺的主要是**运行时控制面**和**管理面**。
- 管理面（settings/auth/cron/stats/skills）如需对第三方客户端暴露，优先考虑 `mothx/http` 进程内隧道复用 serve API mux，而不是逐端点扩展 ACP 方法。
- P0 四项是第三方客户端对接的最小补齐集，预估改动集中在 `internal/acp/acp.go` + 协议文档，不触碰其他子系统。
