package knowledge

import "testing"

func TestSaveParamsKeepKnowledgeBaseWireKey(t *testing.T) {
	params := SaveParams("kb-1", &BaseDraft{Name: "Docs", RootDir: "/work/docs"})
	if _, ok := params["knowledgeBase"]; !ok {
		t.Fatalf("missing knowledgeBase wire key: %#v", params)
	}
	if _, ok := params["Base"]; ok {
		t.Fatalf("Go type casing leaked into the wire payload: %#v", params)
	}
	if params["id"] != "kb-1" {
		t.Fatalf("update id = %#v", params["id"])
	}
	if _, ok := SaveParams("", &BaseDraft{})["id"]; ok {
		t.Fatalf("create payload must not include an empty id")
	}
}
