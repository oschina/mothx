# JavaScript 工作流

Workflow 模式使用原生 JavaScript DSL 编排 worker agent。通过 `--workflows` 启用；项目中的 `workflow-javascript` skill 提供完整语法和模式参考。

```javascript
workflow("quick audit", {
  concurrency: 2,
  phases: [
    phase("scan", parallel(
      agent("api", { mode: "plan", tools: ["read", "grep"], prompt: "审计 API 风险。" }),
      agent("channels", { mode: "plan", tools: ["read", "grep"], prompt: "审计渠道风险。" })
    )),
    phase("verify", agent("cross-check", {
      mode: "plan", prompt: "汇总发现：\n" + results("scan")
    }))
  ]
});
```

`workflow(name, options)` 支持 `concurrency` 和 `phases`。可使用 `phase`、`parallel`、`series`、`agent`。workflow、phase、agent 名称必须是字符串字面量。agent 选项为 `prompt`、`mode`、`workDir`、`tools`、`maxIterations`、`key` 和 `systemPromptExtra`；tools 使用普通 JavaScript 数组。

运行时表达式包括 `result("phase.agent")`、`resultKey("phase.agent", "r0")`、`resultLatest("phase.agent")`、`results("phase")` 和 `log("message", value)`。重复逻辑 agent 保持固定名称，并使用唯一 key，例如 `key: "r" + i`。

并发默认值为 5。worker 的 `maxIterations` 省略、为 0 或负数时默认为 100。`workflow_run` 的 `timeoutSeconds` 是独立的工具级超时。worker 不能启动嵌套编排。生成或修改复杂源码后，应先调用 `workflow_lint`。

CLI 和 Serve 模式提供 `/workflows` 用于列表、查看和取消；`workflow_cancel` 只取消当前进程中的 active run。
