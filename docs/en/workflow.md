# Workflow JavaScript

Workflow mode orchestrates worker agents with a raw JavaScript DSL. Enable it with `--workflows`; the project skill `workflow-javascript` contains the full reference patterns.

```javascript
workflow("quick audit", {
  concurrency: 2,
  phases: [
    phase("scan", parallel(
      agent("api", { mode: "plan", tools: ["read", "grep"], prompt: "Audit API risks." }),
      agent("channels", { mode: "plan", tools: ["read", "grep"], prompt: "Audit channel risks." })
    )),
    phase("verify", agent("cross-check", {
      mode: "plan", prompt: "Reconcile findings:\n" + results("scan")
    }))
  ]
});
```

`workflow(name, options)` accepts `concurrency` and `phases`. Use `phase`, `parallel`, `series`, and `agent` nodes. Agent names and phase names must be string literals. Agent options are `prompt`, `mode`, `workDir`, `tools`, `maxIterations`, `key`, and `systemPromptExtra`; tools are normal JavaScript arrays.

Runtime expressions: `result("phase.agent")`, `resultKey("phase.agent", "r0")`, `resultLatest("phase.agent")`, `results("phase")`, and `log("message", value)`. Repeated logical agents keep a literal name and use a unique key such as `key: "r" + i`.

Concurrency defaults to 5. Worker `maxIterations` defaults to 100 when omitted, zero, or negative. `workflow_run` `timeoutSeconds` is a separate tool-level timeout. Worker agents cannot start nested orchestration. Use `workflow_lint` before running non-trivial generated or edited source.

The CLI and Serve mode expose `/workflows` for listing, inspection, and cancellation. `workflow_cancel` only cancels active runs in the current process.
