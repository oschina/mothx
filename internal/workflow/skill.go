package workflow

import (
	"fmt"
	"os"
	"path/filepath"
)

const SkillName = "workflow-javascript"

const defaultSkillContent = `# Workflow JavaScript

Use this skill when authoring workflow_run source. The source is raw JavaScript, not
Markdown, and must call workflow("name", body). Run workflow_lint before executing
non-trivial generated or edited source.

## Core rules

- Use JavaScript function calls and object literals; do not use the former Lisp-style syntax.
- workflow, phase, and agent names must be string literals.
- A workflow body is an options object with phases, or a node/array of nodes.
- Agent options are prompt, mode, workDir, tools, maxIterations, key, and
  systemPromptExtra. Tools is a normal JavaScript string array.
- Use result, resultKey, resultLatest, results, and log for runtime expressions.
- Use key for repeated logical agents; keyed results are stored as phase.agent[key].
- Keep workflows bounded and set timeoutSeconds for long workflow_run calls.

## Progressive references

- references/00-core-rules.md — syntax and defaults [loaded]
- references/01-research.md — research and investigation
- references/02-serial-parallel.md — serial and parallel composition
- references/03-decision-routing.md — JavaScript branching
- references/04-continuous-loops.md — bounded JavaScript loops
- references/05-horizontal-collaboration.md — peer collaboration
- references/06-master-slave-team.md — coordinator and specialists
- references/07-evaluator-optimizer.md — draft and critique
- references/08-governance-checkpoints.md — approval checkpoints

Use workflow_lint when source structure, result references, or option spelling is
uncertain. Worker agents cannot spawn subagents, delegate, or nested workflows.
`

var defaultReferenceFiles = map[string]string{
	"references/00-core-rules.md": `# Core Rules and Skeletons

Workflow source is ordinary JavaScript. The canonical shape is:

    workflow("auth audit", {
      concurrency: 2,
      phases: [
        phase("scan", parallel(
          agent("api", {mode: "plan", tools: ["read", "grep"], maxIterations: 100,
            prompt: "Audit API authentication risks and return file:line evidence."}),
          agent("channels", {mode: "plan", tools: ["read", "grep"], maxIterations: 100,
            prompt: "Audit channel authentication risks and return file:line evidence."})
        )),
        phase("verify", agent("cross-check", {mode: "plan", tools: ["read", "grep"],
          prompt: "Verify these findings:\n" + results("scan")}))
      ]
    });

Supported constructors are workflow, phase, parallel, series, and agent. A workflow
options object may contain concurrency and phases. Use normal JavaScript arrays and
objects; do not use parentheses, quoted lists, symbols, defun, defmacro, concat,
  or other legacy Lisp-style forms. Runtime helpers include resultKey, resultLatest,
  results, and log.

Agent names and phase names must be string literals. Each agent requires prompt.
Supported agent options are prompt, mode, workDir, tools, maxIterations, key, and
systemPromptExtra. Unknown options are rejected. A repeated worker keeps a literal
name and uses a unique key, for example key: "r0". The result key is
phase.agent[r0]. A JavaScript loop can compute the key with "r" + i.

Runtime expressions:

- result("phase.agent") returns one result.
- resultKey("phase.agent", "r0") returns a keyed result.
- resultLatest("phase.agent") returns the newest keyed result.
- results("phase") returns deterministic phase fan-in text.
- log("message", value) appends a workflow log entry.

Defaults: concurrency is 5; maxIterations defaults to 100 when omitted, zero, or
negative; mode and workDir inherit surrounding settings. Worker tools cannot
start nested orchestration. workflow_run timeoutSeconds is separate from maxIterations.
Use workflow_lint before workflow_run for non-trivial source.
`,
	"references/01-research.md": `# Research and Investigation Workflows

Split independent research lanes with parallel, then fan in with a verification
agent. Use explicit read-only tools and bounded prompts.

    workflow("security research", {
      concurrency: 3,
      phases: [
        phase("scan", parallel(
          agent("entrypoints", {mode: "plan", tools: ["read", "grep", "find"],
            prompt: "Find public entrypoints and return file:line evidence."}),
          agent("storage", {mode: "plan", tools: ["read", "grep", "find"],
            prompt: "Inspect persistence trust boundaries and return evidence."}),
          agent("tools", {mode: "plan", tools: ["read", "grep", "find"],
            prompt: "Inspect tool execution and sandbox boundaries."})
        )),
        phase("verify", agent("cross-check", {mode: "plan", tools: ["read", "grep"],
          prompt: "Reject weak claims and prioritize:\n" + results("scan")}))
      ]
    });
`,
	"references/02-serial-parallel.md": `# Serial and Parallel Composition

Use series or ordered phase arrays when later work depends on earlier output. Use
parallel for independent branches.

    workflow("design then implement", {
      phases: [
        phase("design", agent("designer", {mode: "plan", tools: ["read", "grep"],
          prompt: "Design the minimal change and list tests."})),
        phase("implement", agent("builder", {mode: "agent", tools: ["read", "grep", "edit", "write"],
          prompt: "Implement this plan exactly:\n" + result("design.designer")})),
        phase("verify", agent("verifier", {mode: "plan", tools: ["read", "grep"],
          prompt: "Review the implementation:\n" + results("implement")}))
      ]
    });
`,
	"references/03-decision-routing.md": `# Decision Routing and Branching

JavaScript control flow may construct nodes dynamically. Names passed to workflow,
phase, and agent must remain string literals in source. Use result text to select a
bounded branch before building the workflow body.

    var route = "standard";
    workflow("risk routed task", {
      phases: [
        phase("classify", agent("classifier", {mode: "plan",
          prompt: "Return exactly HIGH or STANDARD."})),
        phase("review", route === "high"
          ? agent("high-risk-review", {mode: "plan", tools: ["read", "grep", "find"],
              prompt: "Perform conservative high-risk analysis."})
          : agent("standard-review", {mode: "plan", tools: ["read", "grep"],
              prompt: "Perform standard bounded review."}))
      ]
    });

Prefer exact classifier tokens and keep every branch bounded and auditable.
`,
	"references/04-continuous-loops.md": `# Bounded JavaScript Loops

Use a real bounded JavaScript loop only when repeated execution needs runtime state.
Every loop needs a hard limit, an updated state variable, a stop condition, and a
unique key for each repeated agent.

    var status = "NEEDS_WORK";
    var lastWorker = "";
    for (var i = 0; i < 3 && status !== "DONE"; i++) {
      var key = "r" + i;
      // Build repeated phase/agent nodes with key: key in the workflow body.
      lastWorker = resultKey("iteration.worker", key);
      status = resultLatest("iteration.checker");
    }

The runtime evaluates the resulting node graph. Do not simulate loops by writing
many numbered phases. Repeated agents keep a literal name and use key: "r" + i;
so results are stored as phase.agent[r0], phase.agent[r1], and so on. Status workers
should return exactly DONE or NEEDS_WORK. Use resultLatest for the newest instance.
`,
	"references/05-horizontal-collaboration.md": `# Horizontal Multi-Agent Collaboration

Use parallel peer agents for independent opinions, then a reconciliation phase.

    workflow("expert panel", {
      concurrency: 4,
      phases: [
        phase("positions", parallel(
          agent("security", {mode: "plan", tools: ["read", "grep"], prompt: "Analyze security risks."}),
          agent("maintainability", {mode: "plan", tools: ["read", "grep"], prompt: "Analyze ownership and maintenance."}),
          agent("performance", {mode: "plan", tools: ["read", "grep"], prompt: "Analyze runtime and scaling."})
        )),
        phase("reconcile", agent("triage", {mode: "plan", tools: ["read", "grep"],
          prompt: "Deduplicate and prioritize:\n" + results("positions")}))
      ]
    });
`,
	"references/06-master-slave-team.md": `# Master-Slave Small Team Workflows

Use a coordinator phase to define narrow specialist tasks, then execute independent
specialists in parallel and finish with a master review. Keep ownership boundaries
and tool scopes explicit.

    workflow("small team change", {
      concurrency: 3,
      phases: [
        phase("execute", parallel(
          agent("backend", {mode: "agent", tools: ["read", "grep", "edit"], prompt: "Implement backend scope."}),
          agent("frontend", {mode: "agent", tools: ["read", "grep", "edit"], prompt: "Implement frontend scope."})
        )),
        phase("master-review", agent("master-review", {mode: "plan", tools: ["read", "grep"],
          prompt: "Review integration boundaries:\n" + results("execute")}))
      ]
    });
`,
	"references/07-evaluator-optimizer.md": `# Evaluator-Optimizer Review Passes

Use a fixed one-pass draft, critique, and revision pipeline. This reference does
not define loop control.

    workflow("proposal refinement", {
      phases: [
        phase("draft", agent("writer", {mode: "plan", tools: ["read", "grep"],
          prompt: "Draft the proposal with assumptions and tradeoffs."})),
        phase("critique", agent("critic", {mode: "plan", tools: ["read", "grep"],
          prompt: "Critique for correctness and risk:\n" + result("draft.writer")})),
        phase("revise", agent("reviser", {mode: "plan", tools: ["read", "grep"],
          prompt: "Revise the draft using:\n" + result("critique.critic")}))
      ]
    });
`,
	"references/08-governance-checkpoints.md": `# Governance and Human Checkpoints

Workers cannot ask the user mid-run. For high-impact changes, first produce a
Decision Packet, ask for approval in the parent conversation, and only then run an
execution workflow.

    workflow("migration decision packet", {
      phases: [
        phase("assess", parallel(
          agent("benefits", {mode: "plan", tools: ["read", "grep"], prompt: "List concrete benefits."}),
          agent("risks", {mode: "plan", tools: ["read", "grep"], prompt: "List rollback and security risks."}),
          agent("costs", {mode: "plan", tools: ["read", "grep"], prompt: "Estimate implementation and test cost."})
        )),
        phase("packet", agent("decision-packet", {mode: "plan", tools: ["read", "grep"],
          prompt: "Produce recommendation and approval question:\n" + results("assess")}))
      ]
    });

Prefer plan mode before edits, explicit rollback, and a final validation phase.
`,
}

func EnsureProjectSkill(projectRoot string) (path string, created bool, err error) {
	if projectRoot == "" {
		return "", false, fmt.Errorf("project root is required")
	}
	skillDir := filepath.Join(projectRoot, ".skills", SkillName)
	upperPath := filepath.Join(skillDir, "SKILL.md")
	lowerPath := filepath.Join(skillDir, "skill.md")
	for _, candidate := range []string{upperPath, lowerPath} {
		if _, statErr := os.Stat(candidate); statErr == nil {
			if err := ensureReferenceFiles(skillDir); err != nil {
				return "", false, err
			}
			return candidate, false, nil
		} else if !os.IsNotExist(statErr) {
			return "", false, statErr
		}
	}
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", false, err
	}
	f, err := os.OpenFile(upperPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return upperPath, false, nil
		}
		return "", false, err
	}
	if _, err := f.WriteString(defaultSkillContent); err != nil {
		_ = f.Close()
		return "", false, err
	}
	if err := f.Close(); err != nil {
		return "", false, err
	}
	if err := ensureReferenceFiles(skillDir); err != nil {
		return "", false, err
	}
	return upperPath, true, nil
}

func ensureReferenceFiles(skillDir string) error {
	for relPath, content := range defaultReferenceFiles {
		path := filepath.Join(skillDir, relPath)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return err
		}
		if _, err := f.WriteString(content); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}
