package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func testRuntime() *runtime {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	return &runtime{
		runner: &Runner{Now: func() time.Time { return now }},
		state:  &RunState{Results: make(map[string]AgentResult)},
	}
}

func TestResolveJSValueNestedValuesAndNumbers(t *testing.T) {
	rt := testRuntime()
	value := map[string]any{
		"object": map[string]any{
			"text":  &jsExpr{kind: "result", args: []any{"scan.worker"}},
			"items": []any{int64(3), float64(2.5), nil},
		},
		"null": nil,
	}
	rt.state.Results["scan.worker"] = AgentResult{Key: "scan.worker", Phase: "scan", Name: "worker", Result: "findings"}
	got, err := resolveJSValue(rt, context.Background(), value)
	if err != nil {
		t.Fatal(err)
	}
	obj := got.(map[string]any)
	nested := obj["object"].(map[string]any)
	if nested["text"] != "findings" {
		t.Fatalf("nested result = %#v", nested["text"])
	}
	items := nested["items"].([]any)
	if items[0] != int64(3) || items[1] != 2.5 || items[2] != nil {
		t.Fatalf("items = %#v", items)
	}
}

func TestResolveJSValueErrorsPropagate(t *testing.T) {
	rt := testRuntime()
	_, err := resolveJSValue(rt, context.Background(), &jsExpr{kind: "resultKey", args: []any{"scan.worker", "bad[]"}})
	if err == nil || !strings.Contains(err.Error(), "must not contain") {
		t.Fatalf("error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = resolveJSValue(rt, ctx, "value")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestEvalJSWorkflowCancellationInterruptsRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := evalJSWorkflow(ctx, `workflow("hang", {phases:[phase("loop", function(){ while (true) {} })]});`)
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("workflow evaluation did not stop after cancellation")
	}
}

func TestGojaUndefinedAndNullExportAsNil(t *testing.T) {
	vm := goja.New()
	for _, value := range []goja.Value{goja.Undefined(), goja.Null()} {
		if exported := value.Export(); exported != nil {
			t.Fatalf("exported %#v, want nil", exported)
		}
	}
	_ = vm
}

// TestEvalJSWorkflowTimesOutRunawaySource guards the VM-level evaluation
// budget: a runaway DSL script must be interrupted even when its caller passed
// a context without a deadline.
func TestEvalJSWorkflowTimesOutRunawaySource(t *testing.T) {
	started := time.Now()
	_, err := evalJSWorkflowWithin(context.Background(), `while (true) {}`, 50*time.Millisecond)
	if !errors.Is(err, ErrJSEvaluationTimeout) {
		t.Fatalf("error = %v, want ErrJSEvaluationTimeout", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("evaluation ran for %s despite the 50ms budget", elapsed)
	}
}

// TestEvalJSWorkflowWithinKeepsCompletionBehavior guards that the added budget
// does not change a successful evaluation.
func TestEvalJSWorkflowWithinKeepsCompletionBehavior(t *testing.T) {
	source := `workflow("ok", {phases:[phase("scan", agent("worker", {prompt:"look"})), phase("verify", agent("checker", {prompt: result("scan.worker")}))]});`
	wf, err := evalJSWorkflowWithin(context.Background(), source, time.Second)
	if err != nil {
		t.Fatalf("evaluation failed: %v", err)
	}
	if wf == nil || wf.name != "ok" || len(wf.children) != 2 {
		t.Fatalf("workflow = %#v, want two nodes", wf)
	}
}
