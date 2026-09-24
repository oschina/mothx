# 更新日志（当前版本）

本文件仅记录**当前版本**的变更。所有版本的完整历史见 [docs/zh/changelog.md](zh/changelog.md)。

## v1.3.103

### 🐛 问题修复

- **上下文压缩不再以 "max iterations (1) exceeded" 失败**
  - 压缩摘要是通过一个 `MaxIterations: 1`（只允许一次 LLM 轮次）的子 Agent 生成的。agent loop 内的恢复重试（空响应重试、输出上限升级与续写、内容拒绝恢复、上下文溢出恢复、Responses 远端状态重放）每次都会重新发起一次供应商请求，却不消耗逻辑迭代计数，于是任何一次恢复都会用掉唯一的迭代，整个压缩以 `generate summary: max iterations (1) exceeded` 中止；而压缩失败后超大的上下文原样保留，错误便会在后续每一轮反复出现。
  - 这些恢复尝试现在不再消耗逻辑迭代预算——每条路径都保留自己的有界重试计数（空响应 2 次、输出续写 3 次、内容拒绝 2 个阶段）——因此 `MaxIterations` 现在表示"产出性 LLM 轮次"，摘要子 Agent 遇到空响应或截断也能正常恢复而不是报错终止。这与既有的传输层恢复规则以及"恢复不得消耗迭代预算"的共享原则保持一致。
  - 摘要子 Agent 的上限保持为 3，作为偶发"幽灵工具调用"轮次的安全余量（其工具集始终为空）；误导性的 `tool result summarization returned empty result` 错误文案也更名为 `summarization returned empty result`。
  - 新增回归测试覆盖：1 次迭代预算内的空响应与输出上限恢复、摘要子 Agent 在空响应后正常恢复。
