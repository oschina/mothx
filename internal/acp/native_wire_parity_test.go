package acp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Native is a separate Go module and cannot import internal/acp, so it
// hand-copies the ACP v1 wire types. This guard keeps that copy honest: every
// JSON key Native expects must still exist in the shared server type. A server
// rename or removal therefore fails here instead of silently arriving as a
// zero value in the desktop client.
func TestNativeWireTypesMatchSharedContract(t *testing.T) {
	path := filepath.Join("..", "..", "native", "internal", "acp", "types.go")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("native module source is not available: %v", err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse native wire types: %v", err)
	}

	nativeTags := make(map[string][]string)
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if !ok {
			return true
		}
		structType, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		nativeTags[spec.Name.Name] = structJSONTags(structType.Fields)
		return false
	})

	pairs := []struct {
		server any
		native string
	}{
		{sessionUpdate{}, "SessionUpdate"},
		{newSessionResult{}, "SessionResult"},
		{listedSession{}, "ListedSession"},
		{listSessionsResult{}, "SessionListResult"},
		{transcriptPageResult{}, "TranscriptPageResult"},
		{sessionModeState{}, "SessionModeState"},
		{sessionMode{}, "SessionMode"},
		{contentBlock{}, "ContentBlock"},
		{availableCommand{}, "Command"},
		{toolCallLocation{}, "ToolLocation"},
		{planEntry{}, "PlanEntry"},
		{usageCost{}, "UsageCost"},
		{promptResult{}, "PromptResult"},
		{permissionOption{}, "PermissionOption"},
		{permissionToolCall{}, "PermissionToolCall"},
		{requestPermissionRequest{}, "PermissionParams"},
	}

	for _, pair := range pairs {
		serverTags := reflectJSONTags(reflect.TypeOf(pair.server))
		native, ok := nativeTags[pair.native]
		if !ok {
			t.Errorf("native acp type %s is missing from native/internal/acp/types.go", pair.native)
			continue
		}
		var extra []string
		for _, tag := range native {
			if _, found := serverTags[tag]; !found {
				extra = append(extra, tag)
			}
		}
		if len(extra) > 0 {
			t.Errorf("native %s expects JSON keys the shared ACP contract does not send: %s", pair.native, strings.Join(extra, ", "))
		}
	}
}

func structJSONTags(fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}
	tags := make([]string, 0, len(fields.List))
	for _, field := range fields.List {
		if field.Tag == nil {
			continue
		}
		raw, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			continue
		}
		name := jsonTagName(reflect.StructTag(raw).Get("json"))
		if name == "" {
			continue
		}
		tags = append(tags, name)
	}
	sort.Strings(tags)
	return tags
}

func reflectJSONTags(typ reflect.Type) map[string]struct{} {
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	tags := make(map[string]struct{}, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name := jsonTagName(typ.Field(i).Tag.Get("json"))
		if name == "" {
			continue
		}
		tags[name] = struct{}{}
	}
	return tags
}

func jsonTagName(tag string) string {
	if tag == "" || tag == "-" {
		return ""
	}
	return strings.Split(tag, ",")[0]
}
