package workflow

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// Workflow source evaluation runs inside a JavaScript VM. It only builds the
// node graph (worker agents run natively afterwards), so it must always be
// bounded: a runaway DSL script such as `while (true) {}` is a plausible
// authoring mistake, and the caller's context may carry no deadline at all.
const (
	// jsEvalTimeout caps one workflow-run evaluation.
	jsEvalTimeout = 30 * time.Second
	// lintEvalTimeout is the tighter cap for workflow_lint, which is an
	// interactive authoring check and must fail fast.
	lintEvalTimeout = 5 * time.Second
)

// ErrJSEvaluationTimeout reports a workflow source that outran the VM
// evaluation budget instead of completing or being cancelled by its caller.
var ErrJSEvaluationTimeout = errors.New("workflow source evaluation timed out")

type jsWorkflow struct {
	name        string
	concurrency int
	children    []*jsNode
}

type jsNode struct {
	kind     string
	name     string
	opts     map[string]any
	children []*jsNode
}

type jsExpr struct {
	kind string
	args []any
}

func jsBuiltin(vm *goja.Runtime, kind string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		args := make([]any, len(call.Arguments))
		for i, arg := range call.Arguments {
			args[i] = arg.Export()
		}
		return vm.ToValue(&jsExpr{kind: kind, args: args})
	}
}

func evalJSWorkflow(ctx context.Context, source string) (*jsWorkflow, error) {
	return evalJSWorkflowWithin(ctx, source, jsEvalTimeout)
}

// evalJSWorkflowWithin evaluates source with both defensive bounds the VM needs:
// the caller's context and a wall-clock budget. The first one to fire interrupts
// the script at its next safe point, so a runaway DSL cannot pin the process
// even when its caller passed a context without deadline.
func evalJSWorkflowWithin(ctx context.Context, source string, timeout time.Duration) (*jsWorkflow, error) {
	vm := goja.New()
	var timedOut atomic.Bool
	var timer *time.Timer
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}
	interruptDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt(ctx.Err())
		case <-timeoutCh:
			timedOut.Store(true)
			vm.Interrupt(ErrJSEvaluationTimeout)
		case <-interruptDone:
		}
	}()
	defer close(interruptDone)
	toNode := func(v goja.Value) (*jsNode, error) {
		if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
			return nil, fmt.Errorf("expected workflow node")
		}
		n, ok := v.Export().(*jsNode)
		if !ok || n == nil {
			return nil, fmt.Errorf("expected workflow node")
		}
		return n, nil
	}
	toNodes := func(args []goja.Value) ([]*jsNode, error) {
		nodes := make([]*jsNode, 0, len(args))
		for _, arg := range args {
			n, err := toNode(arg)
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, n)
		}
		return nodes, nil
	}
	vm.Set("agent", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.ToValue("agent expects a name"))
		}
		opts := map[string]any{}
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) {
			obj := call.Arguments[1].ToObject(vm)
			for _, key := range obj.Keys() {
				opts[key] = obj.Get(key).Export()
			}
		}
		return vm.ToValue(&jsNode{kind: "agent", name: call.Arguments[0].String(), opts: opts})
	})
	for _, name := range []string{"result", "resultKey", "resultLatest", "results", "log"} {
		vm.Set(name, jsBuiltin(vm, name))
	}
	vm.Set("concurrency", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) != 1 {
			panic(vm.ToValue("concurrency expects 1 argument"))
		}
		return vm.ToValue(&jsExpr{kind: "concurrency", args: []any{call.Arguments[0].Export()}})
	})
	vm.Set("parallel", func(call goja.FunctionCall) goja.Value {
		nodes, err := toNodes(call.Arguments)
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return vm.ToValue(&jsNode{kind: "parallel", children: nodes})
	})
	vm.Set("series", func(call goja.FunctionCall) goja.Value {
		nodes, err := toNodes(call.Arguments)
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return vm.ToValue(&jsNode{kind: "series", children: nodes})
	})
	vm.Set("phase", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.ToValue("phase expects a name and body"))
		}
		nodes, err := jsBodyNodes(vm, call.Arguments[1])
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return vm.ToValue(&jsNode{kind: "phase", name: call.Arguments[0].String(), children: nodes})
	})
	var workflow *jsWorkflow
	vm.Set("workflow", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.ToValue("workflow expects a name and body"))
		}
		nodes, err := jsBodyNodes(vm, call.Arguments[1])
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		workflow = &jsWorkflow{name: call.Arguments[0].String(), children: nodes}
		if obj := call.Arguments[1].ToObject(vm); obj != nil {
			if v := obj.Get("concurrency"); v != nil && !goja.IsUndefined(v) {
				workflow.concurrency = int(v.ToInteger())
			}
		}
		return vm.ToValue(workflow.name)
	})
	if _, err := vm.RunString(source); err != nil {
		// The timeout flag is unambiguous, so it wins over a caller context that
		// happens to expire at the same moment. A plain interrupt (the caller's
		// context) is reported as the context error, matching the pre-existing
		// contract for cancelled evaluations.
		if timedOut.Load() {
			return nil, ErrJSEvaluationTimeout
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		var interrupted *goja.InterruptedError
		if errors.As(err, &interrupted) {
			return nil, interrupted
		}
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if workflow == nil {
		return nil, fmt.Errorf("source must call workflow(name, body)")
	}
	return workflow, nil
}

func jsBodyNodes(vm *goja.Runtime, value goja.Value) ([]*jsNode, error) {
	if fn, ok := goja.AssertFunction(value); ok {
		v, err := fn(goja.Undefined())
		if err != nil {
			return nil, err
		}
		return jsBodyNodes(vm, v)
	}
	if n, ok := value.Export().(*jsNode); ok {
		return []*jsNode{n}, nil
	}
	obj := value.ToObject(vm)
	if obj == nil {
		return nil, fmt.Errorf("workflow body must be a node, array, function, or options object")
	}
	if arr := obj.Get("phases"); arr != nil && !goja.IsUndefined(arr) {
		return jsBodyNodes(vm, arr)
	}
	if obj.ClassName() == "Array" {
		var raw []any
		if err := vm.ExportTo(obj, &raw); err != nil {
			return nil, fmt.Errorf("workflow body array: %w", err)
		}
		out := []*jsNode{}
		for _, item := range raw {
			n, err := jsBodyNodes(vm, vm.ToValue(item))
			if err != nil {
				return nil, err
			}
			out = append(out, n...)
		}
		return out, nil
	}
	return nil, fmt.Errorf("workflow body must be a node, array, function, or options object")
}

func resolveJSValue(rt *runtime, ctx context.Context, v any) (any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if v == nil {
		return nil, nil
	}
	if e, ok := v.(*jsExpr); ok {
		str := func(i int) (string, error) {
			x, err := resolveJSValue(rt, ctx, e.args[i])
			if err != nil {
				return "", err
			}
			s, ok := x.(string)
			if !ok {
				return "", fmt.Errorf("%s expects a string", e.kind)
			}
			return s, nil
		}
		switch e.kind {
		case "result":
			s, err := str(0)
			if err != nil {
				return nil, err
			}
			r, ok := rt.lookupResult(s, "")
			if !ok {
				return nil, fmt.Errorf("workflow result %q not found", s)
			}
			return r.Result, nil
		case "resultKey":
			s, err := str(0)
			if err != nil {
				return nil, err
			}
			k, err := str(1)
			if err != nil {
				return nil, err
			}
			if err = validateInstanceKey(k); err != nil {
				return nil, err
			}
			r, ok := rt.lookupResult(s, k)
			if !ok {
				return nil, fmt.Errorf("workflow result %q with key %q not found", s, k)
			}
			return r.Result, nil
		case "resultLatest":
			s, err := str(0)
			if err != nil {
				return nil, err
			}
			r, ok := rt.latestResultForBase(s)
			if !ok {
				return nil, fmt.Errorf("workflow result %q not found", s)
			}
			return r.Result, nil
		case "results":
			s, err := str(0)
			if err != nil {
				return nil, err
			}
			return rt.resultsText(s), nil
		case "log":
			parts := make([]string, len(e.args))
			for i := range e.args {
				x, err := resolveJSValue(rt, ctx, e.args[i])
				if err != nil {
					return nil, err
				}
				parts[i] = fmt.Sprint(x)
			}
			s := strings.Join(parts, " ")
			rt.appendLog(s)
			return s, nil
		case "concurrency":
			return e.args[0], nil
		default:
			return nil, fmt.Errorf("unknown workflow expression %s", e.kind)
		}
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			resolved, err := resolveJSValue(rt, ctx, item)
			if err != nil {
				return nil, err
			}
			out[k] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			resolved, err := resolveJSValue(rt, ctx, item)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	default:
		return v, nil
	}
}

func (rt *runtime) lookupResult(baseKey string, instanceKey string) (AgentResult, bool) {
	if instanceKey != "" {
		return rt.resultByStorageKey(resultStorageKey(baseKey, instanceKey))
	}
	if result, ok := rt.resultByStorageKey(baseKey); ok {
		return result, true
	}
	return rt.latestResultForBase(baseKey)
}

func (rt *runtime) resultByStorageKey(key string) (AgentResult, bool) {
	rt.mu.Lock()
	result, ok := rt.state.Results[key]
	rt.mu.Unlock()
	return result, ok
}

func (rt *runtime) latestResultForBase(baseKey string) (AgentResult, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var latest AgentResult
	found := false
	for _, result := range rt.state.Results {
		if !resultMatchesBase(result, baseKey) {
			continue
		}
		if !found || result.StartedAt.After(latest.StartedAt) || result.FinishedAt.After(latest.FinishedAt) {
			latest = result
			found = true
		}
	}
	return latest, found
}
func (rt *runtime) resultsText(query string) string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	results := make([]AgentResult, 0, len(rt.state.Results))
	for _, res := range rt.state.Results {
		if resultMatchesBase(res, query) || res.Phase == query {
			results = append(results, res)
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].StartedAt.Equal(results[j].StartedAt) {
			return results[i].Key < results[j].Key
		}
		return results[i].StartedAt.Before(results[j].StartedAt)
	})
	out := ""
	for _, res := range results {
		if out != "" {
			out += "\n\n"
		}
		out += res.Key + ":\n" + res.Result
	}
	return out
}
func (rt *runtime) appendLog(msg string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.state.Logs = append(rt.state.Logs, WorkflowLog{Time: rt.runner.now(), Message: msg})
	rt.state.UpdatedAt = rt.runner.now()
	rt.emitProgressLocked(ProgressEvent{Status: StatusRunning, Message: msg})
}

func (rt *runtime) executeJSNodes(ctx context.Context, nodes []*jsNode, wf *jsWorkflow, phase string, phaseIndex int) error {
	for _, node := range nodes {
		if node == nil {
			continue
		}
		switch node.kind {
		case "phase":
			idx := rt.startPhase(node.name)
			if err := rt.executeJSNodes(ctx, node.children, wf, node.name, idx); err != nil {
				rt.finishPhase(idx, statusForError(err), err.Error())
				return err
			}
			rt.finishPhase(idx, StatusDone, "")
		case "parallel":
			parallelCtx, cancel := context.WithCancel(ctx)
			var wg sync.WaitGroup
			errs := make(chan error, len(node.children))
			for _, child := range node.children {
				if child == nil {
					continue
				}
				wg.Add(1)
				go func(n *jsNode) {
					defer wg.Done()
					if err := rt.executeJSNodes(parallelCtx, []*jsNode{n}, wf, phase, phaseIndex); err != nil {
						select {
						case errs <- err:
							cancel()
						default:
						}
					}
				}(child)
			}
			wg.Wait()
			cancel()
			close(errs)
			var parallelErrs []error
			for err := range errs {
				if err != nil {
					parallelErrs = append(parallelErrs, err)
				}
			}
			if len(parallelErrs) > 1 {
				filtered := parallelErrs[:0]
				for _, err := range parallelErrs {
					if !errors.Is(err, context.Canceled) {
						filtered = append(filtered, err)
					}
				}
				if len(filtered) > 0 {
					parallelErrs = filtered
				}
			}
			if len(parallelErrs) > 0 {
				return errors.Join(parallelErrs...)
			}
		case "series":
			if err := rt.executeJSNodes(ctx, node.children, wf, phase, phaseIndex); err != nil {
				return err
			}
		case "agent":
			task := AgentTask{Name: node.name, Phase: phase}
			for k, v := range node.opts {
				x, err := resolveJSValue(rt, ctx, v)
				if err != nil {
					return err
				}
				if err = applyJSAgentOption(&task, k, x); err != nil {
					return err
				}
			}
			if task.Prompt == "" {
				return fmt.Errorf("agent %q requires prompt", task.Name)
			}
			if _, err := rt.runAgent(ctx, task, phaseIndex); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyJSAgentOption(task *AgentTask, key string, value any) error {
	switch key {
	case "prompt":
		task.Prompt = fmt.Sprint(value)
	case "mode":
		task.Mode = fmt.Sprint(value)
	case "workDir":
		task.WorkDir = fmt.Sprint(value)
	case "tools":
		if list, ok := value.([]any); ok {
			for _, x := range list {
				task.Tools = append(task.Tools, fmt.Sprint(x))
			}
		} else {
			return fmt.Errorf("tools expects an array")
		}
	case "maxIterations":
		task.MaxIterations = int(toNumber(value))
	case "key":
		task.InstanceKey = fmt.Sprint(value)
		if err := validateInstanceKey(task.InstanceKey); err != nil {
			return err
		}
	case "systemPromptExtra":
		task.SystemPromptExtra = fmt.Sprint(value)
	default:
		return fmt.Errorf("unknown agent option %s", key)
	}
	return nil
}
func toNumber(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	case float32:
		return float64(n)
	default:
		return 0
	}
}
