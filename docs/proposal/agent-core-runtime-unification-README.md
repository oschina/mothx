# Agent 核心统一架构方案

本目录记录 MothX 从“共享 Agent 底层能力”向“统一 Agent Runtime、多个薄适配层”演进的架构方案。

- 主方案：[统一 Agent 核心与多入口 Runtime 方案](./agent-core-runtime-unification-proposal.md)
- 后续方案：[Runtime 提交队列与执行完成屏障方案](./runtime-submission-queue-completion-barrier-proposal.md)
- 状态：Phase 12 当前验收完成（2026-08-15）。核心 Runtime boundary 已落地；剩余内容是有 owner、合同测试和删除条件的命名迁移桥。
- 最近同步：2026-08-15
- 当前完成：Source/Policy 与 strict conflict/unknown-source rejection、forced `yolo`、SessionRuntime/Builder、统一 Agent 构造、ExecutionRuntime durable transition/recovery、DecisionService `ResolveWith` commit-before-consume、ACP prompt/run identity、ACP-owned orphan recovery、Cron/SessionPool shutdown tracking、架构守卫和跨入口合同测试。
- 当前边界：Responses provider driver、协议 payload/callback、RunManager 内存 fan-out/query、TUI capability hooks 仍由 Adapter 或 CLI 注入；它们不能创建 Agent、Run 或 Decision 的第二套核心生命周期。
- ACP 恢复语义：同一进程 reconnect 会复用 Runtime 并 replay pending projections；进程重启无法重建本地 Agent/callback 栈，因此 ACP-owned orphan Run 会 terminalize，不会伪造 reattach。

## 残余迁移债务

1. 删除 `internal/serve/openaiapi/run_manager.go` 的 in-memory fan-out/query 兼容写入，完成 event broker 迁移后移除对应 helper。
2. 在 capability contract 稳定并有覆盖测试后，把 TUI capability hooks 从 CLI/RegistryHook 下沉到 Builder。
3. 保持 Responses provider-specific driver 与协议 projection 使用 Runtime 的 source/policy/run/decision 语义，并逐步删除旧 persistence/query wrapper。
4. 若未来有可恢复的跨进程 Agent execution contract，再为 ACP 增加真正的 Reattach；在此之前维持显式 orphan terminalization。
5. 按 Runtime submission coordinator/completion barrier 方案收敛同 Session 连续提交，删除 TUI 对 Agent terminal event、lease 轮询和 adapter-owned queue 调度的正确性依赖。
