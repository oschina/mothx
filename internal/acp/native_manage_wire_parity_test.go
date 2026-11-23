package acp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
)

// Native cannot import internal/acp, so this fixture guard executes the real
// management handlers and checks the raw response keys against the source of
// the Native feature projection types. It catches a server rename/removal that
// would arrive as a zero value in the desktop client, and a new top-level
// response key that Native does not model.
type nativeManageWireCase struct {
	method   string
	params   any
	file     string
	typeName string
	// ignoredTopLevel lists server-only top-level keys this projection
	// intentionally does not model. Keep it explicit so a new key still fails.
	ignoredTopLevel []string
}

func TestNativeManageResponseKeysMatchFixtures(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	settings := writeManageSettings(t, configDir, nil)
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, workDir)
	// cron/list needs a non-nil configuration plane. The scheduler is only
	// listed here, so a zero manager/runtime is sufficient; we stop it below.
	srv.settings = settings
	srv.runtime = &agentruntime.SessionRuntime{}
	srv.p = provider.NewMockProvider("mock", []*provider.Model{{ID: "mock-model"}}, nil)
	srv.m = &provider.Model{ID: "mock-model", Provider: "mock"}
	srv.providerName = "mock"
	srv.cronAgentMgr = &agent.AgentManager{}
	defer srv.stopManageCron()

	cases := []nativeManageWireCase{
		{method: "mothx/manage/providers/list", params: map[string]any{}, file: "internal/feature/providers/model.go", typeName: "CatalogView"},
		{
			method: "mothx/manage/settings/get", params: map[string]any{}, file: "internal/feature/providers/model.go", typeName: "SettingsView",
			// The full settings view also carries default mode, sandbox, and
			// feature flags; the provider panel only reads its default pair.
			ignoredTopLevel: []string{"defaultMode", "sandboxEnabled", "sandboxLevel", "webSearchEnabled", "skillsDisabled", "memoryEnabled"},
		},
		{method: "mothx/manage/skills/list", params: map[string]any{"cwd": workDir}, file: "internal/feature/skills/model.go", typeName: "ListView"},
		{method: "mothx/manage/mcp/list", params: map[string]any{}, file: "internal/feature/mcp/model.go", typeName: "ListView"},
		{method: "mothx/manage/memory/get", params: map[string]any{}, file: "internal/feature/memory/model.go", typeName: "View"},
		{method: "mothx/manage/cron/list", params: map[string]any{}, file: "internal/feature/cron/model.go", typeName: "ListView"},
		{method: "mothx/manage/channels/get", params: map[string]any{}, file: "internal/feature/channels/model.go", typeName: "ConfigView"},
		{method: "mothx/manage/serve/get", params: map[string]any{}, file: "internal/feature/serve/model.go", typeName: "ConfigView"},
		{method: "mothx/manage/knowledge-bases/list", params: map[string]any{}, file: "internal/feature/knowledge/model.go", typeName: "ListView"},
		{method: "mothx/manage/application/get", params: map[string]any{}, file: "internal/acp/types.go", typeName: "ApplicationSettingsView"},
		{method: "mothx/manage/env/get", params: map[string]any{}, file: "internal/feature/env/model.go", typeName: "View"},
		{method: "mothx/manage/stats/summary", params: map[string]any{}, file: "internal/feature/stats/model.go", typeName: "SummaryView"},
		{method: "mothx/manage/stats/timeseries", params: map[string]any{"group": "day"}, file: "internal/feature/stats/model.go", typeName: "TimeseriesView"},
		{method: "mothx/manage/deliveries/list", params: map[string]any{}, file: "internal/feature/channels/delivery.go", typeName: "DeliveryListResult"},
		{method: "mothx/manage/acp/profiles/list", params: map[string]any{}, file: "internal/feature/acpworkers/model.go", typeName: "CatalogView"},
	}

	nativeRoot := filepath.Join("..", "..", "native")
	registryCache := map[string]map[string]*nativeWireStruct{}

	for index, item := range cases {
		result := manageFixtureResult(t, callManageFixture(t, srv, output, index+1, item.method, item.params))
		registry := loadNativeWireRegistry(t, filepath.Join(nativeRoot, item.file), registryCache)
		root, ok := registry[item.typeName]
		if !ok {
			t.Fatalf("%s: native type %s is missing from %s", item.method, item.typeName, item.file)
		}
		ctx := item.method + " " + item.typeName
		checkNativeWireObject(t, ctx, registry, root.fields, result, true, item.ignoredTopLevel, 0)
	}
}

type nativeWireField struct {
	name     string
	optional bool
	expr     ast.Expr
}

type nativeWireStruct struct {
	fields []nativeWireField
}

func loadNativeWireRegistry(t *testing.T, path string, cache map[string]map[string]*nativeWireStruct) map[string]*nativeWireStruct {
	t.Helper()
	if registry, ok := cache[path]; ok {
		return registry
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("native module source is not available: %v", err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse native wire file %s: %v", path, err)
	}
	registry := make(map[string]*nativeWireStruct)
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if !ok {
			return true
		}
		structType, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		registry[spec.Name.Name] = &nativeWireStruct{fields: nativeWireFields(structType.Fields)}
		return false
	})
	cache[path] = registry
	return registry
}

func nativeWireFields(list *ast.FieldList) []nativeWireField {
	if list == nil {
		return nil
	}
	fields := make([]nativeWireField, 0, len(list.List))
	for _, field := range list.List {
		if field.Tag == nil {
			continue
		}
		raw, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			continue
		}
		tag := reflect.StructTag(raw).Get("json")
		name := jsonTagName(tag)
		if name == "" {
			continue
		}
		fields = append(fields, nativeWireField{
			name:     name,
			optional: strings.Contains(tag, ",omitempty"),
			expr:     field.Type,
		})
	}
	return fields
}

// checkNativeWireObject validates native fields against a decoded JSON object.
// strictKeys marks the top-level envelope: unknown server keys there mean the
// response envelope grew without a Native projection. Nested projections may
// intentionally model a safety-reduced subset (for example provider configs),
// so nested unknown keys are tolerated while required native keys are checked
// at every depth.
func checkNativeWireObject(t *testing.T, ctx string, registry map[string]*nativeWireStruct, fields []nativeWireField, value map[string]any, strictKeys bool, ignored []string, depth int) {
	t.Helper()
	known := make(map[string]nativeWireField, len(fields))
	for _, field := range fields {
		known[field.name] = field
	}
	if strictKeys {
		for key := range value {
			if _, ok := known[key]; ok {
				continue
			}
			if containsString(ignored, key) {
				continue
			}
			t.Fatalf("%s: server key %q has no Native projection field", ctx, key)
		}
	}
	for _, field := range fields {
		item, present := value[field.name]
		if !present {
			if !field.optional {
				t.Fatalf("%s: Native requires key %q but the server response omitted it", ctx, field.name)
			}
			continue
		}
		checkNativeWireValue(t, ctx+"."+field.name, registry, field.expr, item, depth+1)
	}
}

func checkNativeWireValue(t *testing.T, ctx string, registry map[string]*nativeWireStruct, expr ast.Expr, value any, depth int) {
	t.Helper()
	if value == nil || depth > 16 {
		return
	}
	switch typed := expr.(type) {
	case *ast.ParenExpr:
		checkNativeWireValue(t, ctx, registry, typed.X, value, depth)
	case *ast.StarExpr:
		checkNativeWireValue(t, ctx, registry, typed.X, value, depth)
	case *ast.ArrayType:
		list, ok := value.([]any)
		if !ok {
			t.Fatalf("%s: expected an array, got %T", ctx, value)
		}
		for _, item := range list {
			checkNativeWireValue(t, ctx+"[]", registry, typed.Elt, item, depth+1)
		}
	case *ast.Ident:
		structure, ok := registry[typed.Name]
		if !ok {
			// Imported or otherwise opaque type: the envelope check above still
			// guarantees the key exists.
			return
		}
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("%s: %s expects an object, got %T", ctx, typed.Name, value)
		}
		checkNativeWireObject(t, ctx, registry, structure.fields, object, false, nil, depth)
	case *ast.MapType, *ast.SelectorExpr, *ast.InterfaceType:
		return
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
