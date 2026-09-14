package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/doctor"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/session"
)

func TestExtractSamplingInput(t *testing.T) {
	raw := json.RawMessage(`{"maxTokens":512,"messages":[{"role":"system","content":"sys"},{"role":"user","content":"hello"}]}`)
	prompt, systemPrompt, maxTokens := extractSamplingInput(raw)
	if prompt != "hello" {
		t.Errorf("prompt: got %q", prompt)
	}
	if systemPrompt != "sys" {
		t.Errorf("systemPrompt: got %q", systemPrompt)
	}
	if maxTokens != 512 {
		t.Errorf("maxTokens: got %d", maxTokens)
	}
}

func TestParseJSONRawToMap(t *testing.T) {
	raw := json.RawMessage("{}")
	m := parseJSONRawToMap(raw)
	if m == nil {
		t.Fatal("expected map")
	}
	m = parseJSONRawToMap(json.RawMessage("bad"))
	if m != nil {
		t.Error("expected nil")
	}
}

func TestRequestPermissionTimeoutCleansPending(t *testing.T) {
	s := &server{
		pending:           make(map[string]chan json.RawMessage),
		w:                 &bytes.Buffer{},
		permissionTimeout: time.Millisecond,
	}

	if s.requestPermission("session-1", "tool-1", "bash", map[string]any{"command": "date"}) {
		t.Fatal("requestPermission returned true, want false on timeout")
	}

	if len(s.pending) != 0 {
		t.Fatalf("pending len = %d, want 0", len(s.pending))
	}
}

func TestWriteMessageReturnsWriteError(t *testing.T) {
	s := &server{w: errWriter{}}

	if err := s.writeMessage(map[string]any{"jsonrpc": "2.0"}); err == nil {
		t.Fatal("writeMessage error = nil, want error")
	}
}

func TestReadRequestRejectsOversizedMessage(t *testing.T) {
	s := &server{r: bufio.NewReader(strings.NewReader(strings.Repeat("x", maxRequestBytes+1) + "\n"))}

	if _, err := s.readRequest(); err == nil {
		t.Fatal("readRequest error = nil, want oversized error")
	}
}

func TestValidRPCIDRejectsNonScalarIDs(t *testing.T) {
	for _, raw := range []string{`{"x":1}`, `[1]`, `true`, `1.5`, `1e3`} {
		if validRPCID(json.RawMessage(raw)) {
			t.Errorf("validRPCID(%s) = true, want false", raw)
		}
	}
	for _, raw := range []string{`"request-1"`, `1`, `-42`, `null`} {
		if !validRPCID(json.RawMessage(raw)) {
			t.Errorf("validRPCID(%s) = false, want true", raw)
		}
	}
}

func TestResolveACPModelSelection(t *testing.T) {
	providerName, modelID, err := resolveACPModelSelection(RunOptions{Provider: "", Model: ""}, "test-provider/model-two", true)
	if err != nil || providerName != "test-provider" || modelID != "model-two" {
		t.Fatalf("environment model selection = %q/%q, err=%v", providerName, modelID, err)
	}
	providerName, modelID, err = resolveACPModelSelection(RunOptions{Provider: "test-provider"}, "test-provider/model-two", true)
	if err != nil || providerName != "test-provider" || modelID != "model-two" {
		t.Fatalf("provider completion = %q/%q, err=%v", providerName, modelID, err)
	}
	if _, _, err = resolveACPModelSelection(RunOptions{Provider: "test-provider", Model: "model-one"}, "test-provider/model-two", true); err == nil {
		t.Fatal("conflicting explicit model accepted")
	}
	if _, _, err = resolveACPModelSelection(RunOptions{}, "", true); err == nil {
		t.Fatal("empty requested model accepted")
	}
}

func TestResolveACPProviderSelectionKeepsExplicitProviderModelDefault(t *testing.T) {
	settings := config.DefaultSettings()
	settings.DefaultProvider = "default-provider"
	settings.DefaultModel = "default-model"

	providerName, modelID, err := resolveACPProviderSelection(settings, RunOptions{Provider: "selected-provider"}, "", false)
	if err != nil || providerName != "selected-provider" || modelID != "" {
		t.Fatalf("explicit provider selection = %q/%q, err=%v", providerName, modelID, err)
	}

	providerName, modelID, err = resolveACPProviderSelection(settings, RunOptions{}, "", false)
	if err != nil || providerName != "default-provider" || modelID != "default-model" {
		t.Fatalf("default provider selection = %q/%q, err=%v", providerName, modelID, err)
	}
}

func TestWriteResponseSuppressesNotifications(t *testing.T) {
	var out bytes.Buffer
	s := &server{w: &out}
	if err := s.writeResponse(nil, map[string]any{"ok": true}, nil); err != nil {
		t.Fatalf("writeResponse(notification) error = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("notification response = %q, want empty output", out.String())
	}
	if err := s.writeResponse(json.RawMessage("null"), map[string]any{"ok": true}, nil); err != nil {
		t.Fatalf("writeResponse(null id) error = %v", err)
	}
	if len(jsonLines(t, &out)) != 1 {
		t.Fatalf("explicit null ID response missing: %q", out.String())
	}
}

func TestInitializeAdvertisesStandardSessionLifecycleCapabilities(t *testing.T) {
	var out bytes.Buffer
	s := &server{w: &out}
	s.handleInitialize(rpcRequest{ID: json.RawMessage("1")})

	message := jsonLines(t, &out)[0]
	result := message["result"].(map[string]any)
	caps := result["agentCapabilities"].(map[string]any)["sessionCapabilities"].(map[string]any)
	if _, ok := caps["close"].(map[string]any); !ok {
		t.Fatalf("close capability = %#v, want object", caps["close"])
	}
	if _, ok := caps["list"].(map[string]any); !ok {
		t.Fatalf("list capability = %#v, want object", caps["list"])
	}
	if _, ok := caps["delete"].(map[string]any); !ok {
		t.Fatalf("delete capability = %#v, want object", caps["delete"])
	}
	if _, ok := caps["resume"].(map[string]any); !ok {
		t.Fatalf("resume capability = %#v, want object", caps["resume"])
	}
	if _, ok := caps["configOptions"]; ok {
		t.Fatalf("configOptions is a client capability, not an agent session capability: %#v", caps["configOptions"])
	}
	mcpCaps := result["agentCapabilities"].(map[string]any)["mcpCapabilities"].(map[string]any)
	if _, ok := mcpCaps["stdio"]; ok {
		t.Fatalf("stdio must not be advertised as an MCP extension: %#v", mcpCaps)
	}
	meta := result["agentCapabilities"].(map[string]any)["_meta"].(map[string]any)
	if _, ok := meta[mothxExtensionNamespace]; !ok {
		t.Fatalf("missing MothX extension capability: %#v", meta)
	}
	agentInfo := result["agentInfo"].(map[string]any)
	if agentInfo["name"] != "mothx" || agentInfo["title"] != "MothX" || agentInfo["version"] == "" {
		t.Fatalf("agentInfo = %#v, want MothX identity and version", agentInfo)
	}
	extension := meta[mothxExtensionNamespace].(map[string]any)
	if extension["doctor"] != true {
		t.Fatalf("doctor capability = %#v, want true", extension)
	}
	features, _ := extension["features"].([]any)
	if !containsACPFeature(features, "sessionConfigProvider") {
		t.Fatalf("features = %#v, want sessionConfigProvider", features)
	}
	// The cumulative prompt-cache projection on usage_update is discoverable
	// only through this key; clients must not sniff the payload shape.
	if !containsACPFeature(features, "usageCacheProjection") {
		t.Fatalf("features = %#v, want usageCacheProjection", features)
	}
	rootMeta := result["_meta"].(map[string]any)
	if rootMeta[mothxExtensionNamespace].(map[string]any)["doctor"] != true {
		t.Fatalf("root MothX metadata = %#v, want doctor capability", rootMeta)
	}
}

func TestHandleDoctorDoesNotRequireSession(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	var out bytes.Buffer
	s := &server{w: &out}
	s.handleDoctor(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(`{}`)})

	result := jsonLines(t, &out)[0]["result"].(map[string]any)
	if _, ok := result["version"].(string); !ok {
		t.Fatalf("doctor result version = %#v", result["version"])
	}
	checks := result["checks"].([]any)
	foundCLI := false
	for _, raw := range checks {
		check := raw.(map[string]any)
		if check["id"] == "cli" {
			foundCLI = true
		}
	}
	if !foundCLI {
		t.Fatalf("doctor checks = %#v, missing cli", checks)
	}
}

func TestHandleDoctorUsesServerCWDWhenRequestOmitsIt(t *testing.T) {
	configDir := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	var out bytes.Buffer
	s := &server{w: &out, cwd: cwd, version: "test-version"}
	s.handleDoctor(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(`{}`)})

	result := jsonLines(t, &out)[0]["result"].(map[string]any)
	for _, raw := range result["checks"].([]any) {
		check := raw.(map[string]any)
		if check["id"] == "cwd" && check["detail"] != cwd {
			t.Fatalf("doctor cwd = %#v, want %q", check["detail"], cwd)
		}
	}
}

func TestInitializeAndDoctorUseConfiguredRunVersion(t *testing.T) {
	var out bytes.Buffer
	s := &server{w: &out, version: "0.3.1"}
	s.handleInitialize(rpcRequest{ID: json.RawMessage("1")})
	s.handleDoctor(rpcRequest{ID: json.RawMessage("2"), Params: json.RawMessage(`{}`)})

	messages := jsonLines(t, &out)
	initialize := messages[0]["result"].(map[string]any)
	if got := initialize["agentInfo"].(map[string]any)["version"]; got != "0.3.1" {
		t.Fatalf("agentInfo version = %#v, want 0.3.1", got)
	}
	doctorResult := messages[1]["result"].(map[string]any)
	if got := doctorResult["version"]; got != "0.3.1" {
		t.Fatalf("doctor version = %#v, want 0.3.1", got)
	}
}

func TestDoctorMatchesSharedDoctorResponse(t *testing.T) {
	configDir := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	var out bytes.Buffer
	s := &server{w: &out, cwd: cwd, version: "0.3.1"}
	s.handleDoctor(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(`{}`)})

	var wire struct {
		Result struct {
			OK      bool   `json:"ok"`
			Version string `json:"version"`
			Summary string `json:"summary"`
			Checks  []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"checks"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	shared := doctor.Run(cwd, "0.3.1")
	if wire.Result.OK != shared.OK || wire.Result.Version != shared.Version || wire.Result.Summary != shared.Summary || len(wire.Result.Checks) != len(shared.Checks) {
		t.Fatalf("ACP doctor = %#v, shared doctor = %#v", wire.Result, shared)
	}
	for i, check := range shared.Checks {
		if wire.Result.Checks[i].ID != check.ID || wire.Result.Checks[i].Status != check.Status {
			t.Fatalf("check %d = %#v, want %#v", i, wire.Result.Checks[i], check)
		}
	}
}

func TestClassifyACPStartupErrorDoesNotExposeCause(t *testing.T) {
	secret := "do-not-expose-this-value"
	startup := classifyACPStartupError(fmt.Errorf("invalid provider config api key=%s", secret))
	if startup.Code != "provider_unusable" {
		t.Fatalf("startup code = %q, want provider_unusable", startup.Code)
	}
	if strings.Contains(startup.Message, secret) || strings.Contains(startup.Fix, secret) {
		t.Fatalf("startup payload exposes secret: %#v", startup)
	}
}

func containsACPFeature(features []any, want string) bool {
	for _, feature := range features {
		if feature == want {
			return true
		}
	}
	return false
}

func TestInitializeParsesTypedClientCapabilities(t *testing.T) {
	var out bytes.Buffer
	s := &server{w: &out}
	s.handleInitialize(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(`{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":true,"writeTextFile":true},"terminal":true,"auth":{"terminal":false},"elicitation":{"form":{},"url":{}},"session":{"configOptions":{"boolean":{}}}}}`),
	})
	if !s.clientCaps.FS.ReadTextFile || !s.clientCaps.FS.WriteTextFile || !s.clientCaps.Terminal {
		t.Fatalf("typed fs/terminal capabilities = %#v", s.clientCaps)
	}
	if s.clientCaps.Auth == nil || s.clientCaps.Auth.Terminal {
		t.Fatalf("typed auth capabilities = %#v", s.clientCaps.Auth)
	}
	if s.clientCaps.Elicitation == nil || s.clientCaps.Elicitation.Form == nil || s.clientCaps.Elicitation.URL == nil {
		t.Fatalf("typed elicitation capabilities = %#v", s.clientCaps.Elicitation)
	}
	if s.clientCaps.Session == nil || s.clientCaps.Session.ConfigOptions == nil || s.clientCaps.Session.ConfigOptions.Boolean == nil {
		t.Fatalf("typed session capabilities = %#v", s.clientCaps.Session)
	}
	message := jsonLines(t, &out)[0]
	caps := message["result"].(map[string]any)["agentCapabilities"].(map[string]any)["sessionCapabilities"].(map[string]any)
	if _, ok := caps["additionalDirectories"].(map[string]any); !ok {
		t.Fatalf("additionalDirectories capability = %#v, want object", caps["additionalDirectories"])
	}
}

func TestQuestionUsesStandardElicitationForm(t *testing.T) {
	lines := make(chan string, 1)
	s := &server{
		w:       acpLineWriter{lines: lines},
		pending: make(map[string]chan json.RawMessage),
		clientCaps: clientCapabilities{Elicitation: &clientElicitationCapabilities{
			Form: &struct{}{},
		}},
	}
	answer := make(chan string, 1)
	go func() {
		answer <- s.requestQuestion(context.Background(), "session-1", "Continue?", []string{"yes", "no"}, "Choose one")
	}()

	var request map[string]any
	select {
	case line := <-lines:
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("standard elicitation request was not sent")
	}
	if request["method"] != "elicitation/create" {
		t.Fatalf("request method = %#v, want elicitation/create", request["method"])
	}
	id, ok := request["id"].(string)
	if !ok || id == "" {
		t.Fatalf("request id = %#v, want string", request["id"])
	}
	params := request["params"].(map[string]any)
	if params["sessionId"] != "session-1" || params["message"] != "Continue?" || params["mode"] != "form" {
		t.Fatalf("elicitation params = %#v", params)
	}
	schema := params["requestedSchema"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	answerSchema := properties["answer"].(map[string]any)
	if answerSchema["type"] != "string" {
		t.Fatalf("answer schema = %#v", answerSchema)
	}

	rawID, _ := json.Marshal(id)
	s.deliverResponse(rawID, json.RawMessage(`{"action":"accept","content":{"answer":"yes"}}`), nil)
	select {
	case got := <-answer:
		if got != "yes" {
			t.Fatalf("answer = %q, want yes", got)
		}
	case <-time.After(time.Second):
		t.Fatal("standard elicitation answer was not delivered")
	}
}

type acpLineWriter struct {
	lines chan<- string
}

func (w acpLineWriter) Write(p []byte) (int, error) {
	w.lines <- string(p)
	return len(p), nil
}

func TestCloseSessionCancelsAndRemovesRuntime(t *testing.T) {
	var out bytes.Buffer
	cancelled := make(chan struct{})
	s := &server{
		w: &out,
		sessions: map[string]*sessionRuntime{
			"session-1": {cancel: func() { close(cancelled) }},
		},
	}
	s.handleCloseSession(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(`{"sessionId":"session-1"}`),
	})

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel active session")
	}
	if _, ok := s.sessions["session-1"]; ok {
		t.Fatal("closed session remains in runtime map")
	}
	message := jsonLines(t, &out)[0]
	if _, ok := message["result"].(map[string]any); !ok {
		t.Fatalf("close result = %#v, want empty object", message["result"])
	}
}

func TestListSessionsReturnsPersistedSessions(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	newTestSession(t, cwd, dir, "session-one", 1)
	newTestSession(t, cwd, dir, "session-two", 2)
	for id, providerName := range map[string]string{"session-one": "moark", "session-two": "volcengine-agentplan"} {
		mgr, err := session.OpenByIDExact(dir, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.AppendModelChange(providerName, "model"); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	s := &server{settings: &config.Settings{SessionDir: dir}, w: &out}
	s.handleListSessions(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(fmt.Sprintf(`{"cwd":%q}`, cwd)),
	})

	message := jsonLines(t, &out)[0]
	result := message["result"].(map[string]any)
	sessions := result["sessions"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("listed sessions = %d, want 2", len(sessions))
	}
	for _, item := range sessions {
		listed := item.(map[string]any)
		if listed["cwd"] != cwd {
			t.Fatalf("listed cwd = %q, want %q", listed["cwd"], cwd)
		}
		if listed["sessionId"] == "" {
			t.Fatalf("missing session ID: %#v", listed)
		}
		if listed["provider"] == "" || listed["model"] == "" {
			t.Fatalf("missing provider/model: %#v", listed)
		}
	}
}

func TestListSessionsUsesOpaqueCursor(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	for i := 0; i < sessionListPageSize+1; i++ {
		newTestSession(t, cwd, dir, fmt.Sprintf("page-%03d", i), 0)
	}
	var out bytes.Buffer
	s := &server{settings: &config.Settings{SessionDir: dir}, w: &out}
	s.handleListSessions(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(fmt.Sprintf(`{"cwd":%q}`, cwd))})
	first := jsonLines(t, &out)[0]["result"].(map[string]any)
	if len(first["sessions"].([]any)) != sessionListPageSize {
		t.Fatalf("first page size = %d, want %d", len(first["sessions"].([]any)), sessionListPageSize)
	}
	cursor, ok := first["nextCursor"].(string)
	if !ok || cursor == "" || strings.Trim(cursor, "0123456789") == "" {
		t.Fatalf("nextCursor = %#v, want opaque non-numeric token", first["nextCursor"])
	}
	out.Reset()
	s.handleListSessions(rpcRequest{ID: json.RawMessage("2"), Params: json.RawMessage(fmt.Sprintf(`{"cwd":%q,"cursor":%q}`, cwd, cursor))})
	second := jsonLines(t, &out)[0]["result"].(map[string]any)
	if len(second["sessions"].([]any)) != 1 {
		t.Fatalf("second page size = %d, want 1", len(second["sessions"].([]any)))
	}
	out.Reset()
	s.handleListSessions(rpcRequest{ID: json.RawMessage("3"), Params: json.RawMessage(fmt.Sprintf(`{"cwd":%q,"cursor":"bad"}`, cwd))})
	if code := jsonLines(t, &out)[0]["error"].(map[string]any)["code"]; code != float64(-32602) {
		t.Fatalf("invalid cursor error code = %#v, want -32602", code)
	}
}

func TestListAllSessionsReturnsTheGlobalProjectCatalog(t *testing.T) {
	dir := t.TempDir()
	firstCwd := t.TempDir()
	secondCwd := t.TempDir()
	newTestSession(t, firstCwd, dir, "first-project-session", 1)
	newTestSession(t, secondCwd, dir, "second-project-session", 1)

	var out bytes.Buffer
	s := &server{settings: &config.Settings{SessionDir: dir}, w: &out}
	s.handleListAllSessions(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(`{}`)})

	result := jsonLines(t, &out)[0]["result"].(map[string]any)
	sessions := result["sessions"].([]any)
	seen := map[string]string{}
	for _, item := range sessions {
		listed := item.(map[string]any)
		seen[listed["sessionId"].(string)] = listed["cwd"].(string)
	}
	if seen["first-project-session"] != firstCwd || seen["second-project-session"] != secondCwd {
		t.Fatalf("global session catalog = %#v, want both project directories", seen)
	}
}

func TestListAllSessionsFiltersProjectSearchBeforePagination(t *testing.T) {
	dir := t.TempDir()
	targetCwd := t.TempDir()
	target := session.New(targetCwd, dir)
	if err := target.InitWithID("needle-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := target.AppendSessionTitle("Needle title", "manual"); err != nil {
		t.Fatal(err)
	}
	project, err := session.CreateProject(dir, "Desktop history")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SetSessionMetadata(dir, "needle-session", session.SessionMetadata{ProjectID: project.ID}); err != nil {
		t.Fatal(err)
	}

	// The matching session is created before a full first page of unrelated
	// sessions. A client-side filter after pagination would miss it.
	for i := 0; i < sessionListPageSize+1; i++ {
		newTestSession(t, t.TempDir(), dir, fmt.Sprintf("other-%03d", i), 1)
	}
	var out bytes.Buffer
	s := &server{settings: &config.Settings{SessionDir: dir}, w: &out}

	s.handleListAllSessions(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(`{"query":"desktop history"}`)})
	result := jsonLines(t, &out)[0]["result"].(map[string]any)
	items := result["sessions"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["sessionId"] != "needle-session" {
		t.Fatalf("project-name search result = %#v, want only needle-session", items)
	}
	if result["nextCursor"] != nil {
		t.Fatalf("filtered one-item search returned cursor = %#v", result["nextCursor"])
	}

	out.Reset()
	s.handleListAllSessions(rpcRequest{ID: json.RawMessage("2"), Params: json.RawMessage(fmt.Sprintf(`{"scope":"project","projectId":%q}`, project.ID))})
	projectResult := jsonLines(t, &out)[0]["result"].(map[string]any)
	projectItems := projectResult["sessions"].([]any)
	if len(projectItems) != 1 || projectItems[0].(map[string]any)["sessionId"] != "needle-session" {
		t.Fatalf("project scope result = %#v, want only needle-session", projectItems)
	}

	out.Reset()
	s.handleProjectsDelete(rpcRequest{ID: json.RawMessage("3"), Params: json.RawMessage(fmt.Sprintf(`{"id":%q}`, project.ID))})
	if response := jsonLines(t, &out)[0]; response["error"] != nil {
		t.Fatalf("delete project response = %#v", response)
	}
	out.Reset()
	s.handleListAllSessions(rpcRequest{ID: json.RawMessage("4"), Params: json.RawMessage(`{"scope":"ungrouped","query":"needle title"}`)})
	ungroupedResult := jsonLines(t, &out)[0]["result"].(map[string]any)
	ungroupedItems := ungroupedResult["sessions"].([]any)
	if len(ungroupedItems) != 1 || ungroupedItems[0].(map[string]any)["sessionId"] != "needle-session" {
		t.Fatalf("ungrouped result after project deletion = %#v, want retained needle-session", ungroupedItems)
	}
}

func TestSetSessionWorkDirPersistsTarget(t *testing.T) {
	dir := t.TempDir()
	oldCwd := t.TempDir()
	newCwd := t.TempDir()
	newTestSession(t, oldCwd, dir, "move-session", 1)

	var out bytes.Buffer
	s := testSessionServer(oldCwd, dir, &out)
	s.handleSetSessionWorkDir(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(fmt.Sprintf(`{"sessionId":"move-session","cwd":%q,"_meta":{"mothx":{"workspace":{"cwd":%q}}}}`, newCwd, newCwd)),
	})

	messages := jsonLines(t, &out)
	message := messages[len(messages)-1]
	if _, ok := message["error"]; ok {
		t.Fatalf("set work directory failed: %#v", message)
	}
	result := message["result"].(map[string]any)
	if result["cwd"] != newCwd {
		t.Fatalf("result cwd = %q, want %q", result["cwd"], newCwd)
	}
	mgr, err := session.OpenByIDExact(dir, "move-session")
	if err != nil {
		t.Fatal(err)
	}
	if mgr.GetHeader().Cwd != newCwd {
		t.Fatalf("persisted cwd = %q, want %q", mgr.GetHeader().Cwd, newCwd)
	}
}

func TestLoadSessionReplaysAllMessages(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	newTestSession(t, cwd, dir, "full-history", 41)

	var out bytes.Buffer
	s := &server{
		settings:   &config.Settings{SessionDir: dir},
		sbMgr:      sandbox.NewManager(cwd),
		sessions:   make(map[string]*sessionRuntime),
		toolTitles: make(map[string]string),
		mcpNotify:  make(map[string]bool),
		w:          &out,
	}
	s.handleLoadSession(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(fmt.Sprintf(`{"sessionId":"full-history","cwd":%q}`, cwd)),
	})

	messages := jsonLines(t, &out)
	updates := 0
	for _, message := range messages {
		if message["method"] == "session/update" {
			updates++
		}
	}
	if updates != 41 {
		t.Fatalf("replayed updates = %d, want 41", updates)
	}
	rt := s.sessions["full-history"]
	if rt == nil || rt.runtime == nil || rt.runtime.Manager != rt.mgr || rt.runtime.Registry != rt.registry {
		t.Fatalf("ACP session is not backed by shared runtime: %#v", rt)
	}
}

func TestLoadSessionHistoryPagingProjectsNewestMessagesFirst(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	newTestSession(t, cwd, dir, "paged-history", 5)

	var out bytes.Buffer
	s := &server{
		settings:   &config.Settings{SessionDir: dir},
		sbMgr:      sandbox.NewManager(cwd),
		sessions:   make(map[string]*sessionRuntime),
		toolTitles: make(map[string]string),
		mcpNotify:  make(map[string]bool),
		w:          &out,
	}
	s.handleLoadSession(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(fmt.Sprintf(`{"sessionId":"paged-history","cwd":%q,"historyLimit":2}`, cwd)),
	})

	messages := jsonLines(t, &out)
	if len(messages) != 1 {
		t.Fatalf("initial lazy load emitted %#v, want only its response", messages)
	}
	result := messages[0]["result"].(map[string]any)
	history := result["history"].(map[string]any)
	updates := history["updates"].([]any)
	if len(updates) != 2 || history["nextCursor"] == "" {
		t.Fatalf("initial history = %#v, want two newest updates and a cursor", history)
	}

	out.Reset()
	s.handleSessionHistory(rpcRequest{
		ID:     json.RawMessage("2"),
		Params: json.RawMessage(fmt.Sprintf(`{"sessionId":"paged-history","cursor":%q,"limit":2}`, history["nextCursor"])),
	})
	page := jsonLines(t, &out)[0]["result"].(map[string]any)
	if got := len(page["updates"].([]any)); got != 2 || page["nextCursor"] == "" {
		t.Fatalf("older history page = %#v, want two updates and one final cursor", page)
	}
}

func TestLoadSessionSwitchesToPersistedProvider(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	mgr := session.New(cwd, dir)
	if err := mgr.InitWithID("foreign-provider-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AppendModelChange("volcengine-agentplan", "ark-code-latest"); err != nil {
		t.Fatal(err)
	}
	moarkModel := &provider.Model{ID: "moark-model", Name: "Moark Model"}
	volcModel := &provider.Model{ID: "ark-code-latest", Name: "Ark Code Latest"}
	moark := provider.NewMockProvider("moark", []*provider.Model{moarkModel}, nil)
	volc := provider.NewMockProvider("volcengine-agentplan", []*provider.Model{volcModel}, nil)
	var out bytes.Buffer
	s := &server{
		settings:     &config.Settings{SessionDir: dir},
		p:            moark,
		providerName: "moark",
		m:            moarkModel,
		providers:    map[string]provider.Provider{"moark": moark, "volcengine-agentplan": volc},
		sbMgr:        sandbox.NewManager(cwd),
		sessions:     make(map[string]*sessionRuntime),
		toolTitles:   make(map[string]string),
		mcpNotify:    make(map[string]bool),
		w:            &out,
	}
	s.handleLoadSession(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(fmt.Sprintf(`{"sessionId":"foreign-provider-session","cwd":%q}`, cwd))})
	message := jsonLines(t, &out)[0]
	result := message["result"].(map[string]any)
	options := result["configOptions"].([]any)
	if got := configOptionCurrent(options, "provider"); got != "volcengine-agentplan" {
		t.Fatalf("loaded provider = %q", got)
	}
	if got := configOptionCurrent(options, "model"); got != "volcengine-agentplan/ark-code-latest" {
		t.Fatalf("loaded model = %q", got)
	}
	if _, err := s.closeSessionRuntime("foreign-provider-session"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadHistoricalSessionWithReleasedLeaseDoesNotPersistDefaults(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	newTestSession(t, cwd, dir, "historical-session", 1)
	release, ok := session.TryLockRuntime(dir, "historical-session")
	if !ok {
		t.Fatal("acquire historical session lease")
	}
	release()

	model := &provider.Model{ID: "current-model", Name: "Current Model", Reasoning: true}
	p := provider.NewMockProvider("current-provider", []*provider.Model{model}, nil)
	var out bytes.Buffer
	s := testSessionServer(cwd, dir, &out)
	s.p = p
	s.providerName = "current-provider"
	s.m = model
	s.providers = map[string]provider.Provider{"current-provider": p}
	s.mode = agentruntime.ModeYolo
	s.thinkingLevel = provider.ThinkingMedium

	s.handleLoadSession(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(fmt.Sprintf(`{"sessionId":"historical-session","cwd":%q}`, cwd)),
	})

	messages := jsonLines(t, &out)
	var response map[string]any
	for _, message := range messages {
		if message["id"] == float64(1) {
			response = message
			break
		}
	}
	if response == nil || response["error"] != nil || response["result"] == nil {
		t.Fatalf("historical session load response = %#v", response)
	}
	reopened, err := session.OpenByIDExact(dir, "historical-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.GetLatestModelChange(); ok {
		t.Fatal("historical load persisted a default model binding")
	}
	if _, ok := reopened.GetLatestModeChange(); ok {
		t.Fatal("historical load persisted a default mode binding")
	}
	if _, ok := reopened.GetLatestThinkingLevelChange(); ok {
		t.Fatal("historical load persisted a default thinking binding")
	}
	if _, err := s.closeSessionRuntime("historical-session"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadHistoricalSessionUpdatesDirectoriesUnderRuntimeLease(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	mgr := session.New(cwd, dir)
	if err := mgr.InitWithID("historical-directories"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AppendAdditionalDirectories([]string{oldRoot}); err != nil {
		t.Fatal(err)
	}
	release, ok := session.TryLockRuntime(dir, "historical-directories")
	if !ok {
		t.Fatal("acquire historical directory session lease")
	}
	release()

	var out bytes.Buffer
	s := testSessionServer(cwd, dir, &out)
	s.handleLoadSession(rpcRequest{
		ID: json.RawMessage("1"),
		Params: json.RawMessage(fmt.Sprintf(
			`{"sessionId":"historical-directories","cwd":%q,"additionalDirectories":[%q]}`,
			cwd, newRoot,
		)),
	})

	response := jsonLines(t, &out)[0]
	if response["error"] != nil || response["result"] == nil {
		t.Fatalf("historical directory load response = %#v", response)
	}
	reopened, err := session.OpenByIDExact(dir, "historical-directories")
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := reopened.GetLatestAdditionalDirectories()
	if !ok || len(entry.Directories) != 1 || entry.Directories[0] != newRoot {
		t.Fatalf("persisted additional directories = %#v, ok=%v", entry.Directories, ok)
	}
	if _, err := s.closeSessionRuntime("historical-directories"); err != nil {
		t.Fatal(err)
	}
}

func TestSetTitleHistoricalSessionWithReleasedLease(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	newTestSession(t, cwd, dir, "historical-title", 1)
	release, ok := session.TryLockRuntime(dir, "historical-title")
	if !ok {
		t.Fatal("acquire historical title session lease")
	}
	release()

	var out bytes.Buffer
	s := &server{settings: &config.Settings{SessionDir: dir}, sessions: make(map[string]*sessionRuntime), w: &out}
	s.handleSetSessionTitle(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(`{"sessionId":"historical-title","title":"Renamed history"}`),
	})

	messages := jsonLines(t, &out)
	response := messages[len(messages)-1]
	if response["error"] != nil || response["result"] == nil {
		t.Fatalf("historical title response = %#v", response)
	}
	title, _, err := session.LatestSessionTitle(dir, "historical-title")
	if err != nil {
		t.Fatal(err)
	}
	if title != "Renamed history" {
		t.Fatalf("historical title = %q, want Renamed history", title)
	}
}

func TestLoadSessionProviderMismatchIsStructured(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	mgr := session.New(cwd, dir)
	if err := mgr.InitWithID("missing-provider-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AppendModelChange("missing-provider", "model"); err != nil {
		t.Fatal(err)
	}
	model := &provider.Model{ID: "moark-model"}
	moark := provider.NewMockProvider("moark", []*provider.Model{model}, nil)
	var out bytes.Buffer
	s := &server{
		settings:     &config.Settings{SessionDir: dir},
		p:            moark,
		providerName: "moark",
		m:            model,
		providers:    map[string]provider.Provider{"moark": moark},
		sbMgr:        sandbox.NewManager(cwd),
		sessions:     make(map[string]*sessionRuntime),
		toolTitles:   make(map[string]string),
		mcpNotify:    make(map[string]bool),
		w:            &out,
	}
	s.handleLoadSession(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(fmt.Sprintf(`{"sessionId":"missing-provider-session","cwd":%q}`, cwd))})
	message := jsonLines(t, &out)[0]
	errPayload := message["error"].(map[string]any)
	if errPayload["code"] != float64(-32002) || errPayload["message"] != "session provider mismatch" {
		t.Fatalf("provider mismatch error = %#v", errPayload)
	}
	data := errPayload["data"].(map[string]any)
	if data["sessionProvider"] != "missing-provider" || data["sessionModel"] != "model" || data["currentProvider"] != "moark" {
		t.Fatalf("provider mismatch data = %#v", data)
	}
}

func TestLoadBoundSessionUsesPersistedRuntimePolicy(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	bound, err := session.CreateBound(cwd, dir, "wechat", "acp-policy-user")
	if err != nil {
		t.Fatalf("CreateBound: %v", err)
	}
	var out bytes.Buffer
	s := testSessionServer(cwd, dir, &out)
	s.handleLoadSession(rpcRequest{
		ID: json.RawMessage("1"),
		Params: json.RawMessage(fmt.Sprintf(
			`{"sessionId":%q,"cwd":%q}`,
			bound.GetHeader().ID,
			cwd,
		)),
	})

	rt := s.sessions[bound.GetHeader().ID]
	if rt == nil || rt.runtime == nil {
		t.Fatalf("bound ACP runtime = %#v", rt)
	}
	if rt.runtime.Source != agentruntime.SourceWeChat || rt.runtime.EntrySource != agentruntime.SourceACP {
		t.Fatalf("runtime source/entry = %q/%q, want wechat/acp", rt.runtime.Source, rt.runtime.EntrySource)
	}
	_, mode, err := rt.runtime.ResolvePolicy("", agentruntime.ModeAgent, agentruntime.ModeAgent)
	if err != nil {
		t.Fatalf("ResolvePolicy: %v", err)
	}
	if mode != agentruntime.ModeYolo {
		t.Fatalf("ACP bound mode = %q, want yolo", mode)
	}
}

func TestResumeSessionDoesNotReplayMessages(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	newTestSession(t, cwd, dir, "resume-history", 2)

	var out bytes.Buffer
	s := testSessionServer(cwd, dir, &out)
	s.handleResumeSession(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(fmt.Sprintf(`{"sessionId":"resume-history","cwd":%q}`, cwd)),
	})

	messages := jsonLines(t, &out)
	if len(messages) != 1 || messages[0]["result"] == nil {
		t.Fatalf("resume messages = %#v, want one response without replay", messages)
	}
}

func TestDeleteSessionRemovesPersistedSession(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	newTestSession(t, cwd, dir, "delete-me", 1)

	var out bytes.Buffer
	s := &server{settings: &config.Settings{SessionDir: dir}, w: &out}
	s.handleDeleteSession(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(`{"sessionId":"delete-me"}`),
	})
	if _, err := session.OpenByID(cwd, dir, "delete-me"); err == nil {
		t.Fatal("deleted session is still available")
	}
}

func TestNewSessionMCPFailureRollsBackPersistedSession(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	var out bytes.Buffer
	s := testSessionServer(cwd, dir, &out)

	s.handleNewSession(rpcRequest{
		ID: json.RawMessage("1"),
		Params: json.RawMessage(fmt.Sprintf(
			`{"cwd":%q,"mcpServers":[{"name":"broken","type":"stdio"}]}`,
			cwd,
		)),
	})

	messages := jsonLines(t, &out)
	if len(messages) != 1 || messages[0]["error"] == nil {
		t.Fatalf("new session response = %#v, want MCP error", messages)
	}
	if len(s.sessions) != 0 {
		t.Fatalf("runtime sessions = %d, want 0", len(s.sessions))
	}
	persisted, err := session.ListForDir(cwd, dir)
	if err != nil {
		t.Fatalf("list persisted sessions: %v", err)
	}
	if len(persisted) != 0 {
		t.Fatalf("persisted sessions = %#v, want none after MCP failure", persisted)
	}
}

func TestNewSessionLoadsEnabledConfiguredMCPServers(t *testing.T) {
	configDir := t.TempDir()
	sessionDir := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	command := filepath.Join(t.TempDir(), "configured-mcp")
	fixture := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  method=$(printf '%s\n' "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
  case "$method" in
    initialize)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-11-25"}}\n' "$id"
      ;;
    tools/list)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"configured_probe","inputSchema":{"type":"object"}}]}}\n' "$id"
      ;;
    resources/list|prompts/list)
      printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
      ;;
  esac
done
`
	if err := os.WriteFile(command, []byte(fixture), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveMCPConfig(config.GlobalMCPPath(), &config.MCPConfig{MCPServers: []config.MCPServer{{
		Name: "configured", Type: "stdio", Command: command,
	}}}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	s := testSessionServer(cwd, sessionDir, &out)
	s.handleNewSession(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(fmt.Sprintf(`{"cwd":%q}`, cwd))})
	messages := jsonLines(t, &out)
	if len(messages) != 1 || messages[0]["error"] != nil {
		t.Fatalf("new session response = %#v, want configured MCP session", messages)
	}
	result := messages[0]["result"].(map[string]any)
	sessionID := result["sessionId"].(string)
	rt := s.sessionRuntime(sessionID)
	if rt == nil || len(rt.mcp) != 1 {
		t.Fatalf("MCP clients = %#v, want configured server", rt)
	}
	found := false
	for _, tool := range rt.registry.All() {
		if tool.Name() == "mcp_configured_configured_probe" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("configured MCP tool was not registered: %#v", rt.registry.All())
	}
	rt.closeResources()
}

func TestCancelRequestCancelsMatchingPrompt(t *testing.T) {
	cancelled := make(chan struct{})
	s := &server{sessions: map[string]*sessionRuntime{
		"session-1": {promptID: "prompt-1", cancel: func() { close(cancelled) }},
	}}
	s.handleCancelRequest(rpcRequest{Params: json.RawMessage(`{"requestId":"prompt-1"}`)})
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("$/cancel_request did not cancel matching prompt")
	}
}

func TestCancelRejectsInvalidOrUnknownSession(t *testing.T) {
	var out bytes.Buffer
	s := &server{w: &out, sessions: map[string]*sessionRuntime{}}
	s.handleCancel(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(`{}`)})
	message := jsonLines(t, &out)[0]
	if code := message["error"].(map[string]any)["code"]; code != float64(-32602) {
		t.Fatalf("invalid cancel error code = %#v, want -32602", code)
	}
	out.Reset()
	s.handleCancel(rpcRequest{ID: json.RawMessage("2"), Params: json.RawMessage(`{"sessionId":"missing"}`)})
	message = jsonLines(t, &out)[0]
	if code := message["error"].(map[string]any)["code"]; code != float64(-32000) {
		t.Fatalf("unknown cancel error code = %#v, want -32000", code)
	}
}

func TestPlanUpdateUsesStandardPlanVariant(t *testing.T) {
	var out bytes.Buffer
	s := &server{w: &out}
	s.handleAgentEvent("session-1", agentpkg.Event{
		Type: agentpkg.EventPlanUpdate,
		Plan: &agentpkg.TaskPlan{
			Title: "Implementation",
			Steps: []agentpkg.PlanStep{{Title: "Inspect", Status: "running"}},
		},
	})
	update := jsonLines(t, &out)[0]["params"].(map[string]any)["update"].(map[string]any)
	if update["sessionUpdate"] != "plan" {
		t.Fatalf("plan update type = %#v", update)
	}
	entry := update["entries"].([]any)[0].(map[string]any)
	if entry["content"] != "Inspect" || entry["priority"] != "medium" || entry["status"] != "in_progress" {
		t.Fatalf("plan entry = %#v", entry)
	}
}

func TestMothxStatusUsesExtensionNotification(t *testing.T) {
	var out bytes.Buffer
	s := &server{w: &out}
	s.handleAgentEvent("session-1", agentpkg.Event{Type: agentpkg.EventStatus, StatusMessage: "working"})
	message := jsonLines(t, &out)[0]
	if message["method"] != "_mothx/session_event" {
		t.Fatalf("status method = %#v, want extension notification", message["method"])
	}
}

func TestMothxRetryUsesStructuredExtensionNotification(t *testing.T) {
	var out bytes.Buffer
	s := &server{w: &out}
	s.handleAgentEvent("session-1", agentpkg.Event{
		Type:             agentpkg.EventRetry,
		RetryAttempt:     2,
		RetryMaxAttempts: 4,
		RetryAfterMS:     1500,
		RetryReason:      "provider diagnostic that must not be rendered",
	})

	params := jsonLines(t, &out)[0]["params"].(map[string]any)
	if params["event"] != "retrying" || params["attempt"] != float64(2) || params["maxAttempts"] != float64(4) || params["retryAfterMs"] != float64(1500) {
		t.Fatalf("retry params = %#v, want structured retry metadata", params)
	}
	message, _ := params["message"].(string)
	if !strings.Contains(message, "Retrying (attempt 2/4); waiting 1.5s...") || strings.Contains(message, "provider diagnostic") {
		t.Fatalf("retry message = %q, want safe structured retry status", message)
	}
}

func TestACPFailureRPCErrorPreservesProviderDiagnostic(t *testing.T) {
	rpcErr := acpFailureRPCError(errors.New("upstream HTTP 503 request_id=provider-secret"), nil, agentruntime.PhaseModel)
	if rpcErr.Message != "upstream HTTP 503 request_id=provider-secret" {
		t.Fatalf("RPC error = %#v, want provider diagnostic", rpcErr)
	}
	data, ok := rpcErr.Data.(map[string]any)
	if !ok {
		t.Fatalf("RPC error data = %#v, want structured data", rpcErr.Data)
	}
	if detail, ok := data["detail"].(string); !ok || detail != rpcErr.Message {
		t.Fatalf("RPC error data detail = %#v, want provider diagnostic", data["detail"])
	}

	observed := &agentruntime.ErrorInfo{Code: "event_stream_interrupted", Message: "The run stopped before it could finish."}
	rpcErr = acpFailureRPCError(errors.New("raw stream diagnostic"), observed, agentruntime.PhaseTransport)
	if rpcErr.Message != observed.Message {
		t.Fatalf("observed RPC error = %#v, want Runtime observation message", rpcErr)
	}
}

func TestHostedItemUsesNonExecutableToolUpdate(t *testing.T) {
	var out bytes.Buffer
	s := &server{w: &out}
	s.handleAgentEvent("session-1", agentpkg.Event{Type: agentpkg.EventHostedItem, HostedItem: &agentpkg.HostedItem{
		ID: "search-1", Type: "web_search_call", Status: "completed",
	}})
	update := jsonLines(t, &out)[0]["params"].(map[string]any)["update"].(map[string]any)
	if update["sessionUpdate"] != "tool_call_update" || update["toolCallId"] != "search-1" || update["kind"] != "other" || update["status"] != "completed" {
		t.Fatalf("hosted ACP update = %#v", update)
	}
}

func TestToolDiffUsesACPStructuredContentAndLocations(t *testing.T) {
	var out bytes.Buffer
	oldText := "before\n"
	path := "/tmp/acp-diff.txt"
	s := &server{w: &out}
	s.handleAgentEvent("session-1", agentpkg.Event{
		Type:       agentpkg.EventToolExecutionEnd,
		ToolCallID: "write-1",
		ToolName:   "write_file",
		ToolDiff:   &agentpkg.FileDiff{Path: path, OldText: &oldText, NewText: "after\n"},
	})
	update := jsonLines(t, &out)[0]["params"].(map[string]any)["update"].(map[string]any)
	contents := update["content"].([]any)
	if len(contents) != 1 {
		t.Fatalf("tool content = %#v, want one diff item", contents)
	}
	diff := contents[0].(map[string]any)
	if diff["type"] != "diff" || diff["path"] != path || diff["oldText"] != oldText || diff["newText"] != "after\n" {
		t.Fatalf("ACP diff = %#v", diff)
	}
	locations := update["locations"].([]any)
	if len(locations) != 1 || locations[0].(map[string]any)["path"] != path {
		t.Fatalf("ACP locations = %#v", locations)
	}
}

func TestToolDiffIncludesNullOldTextForCreatedFile(t *testing.T) {
	var out bytes.Buffer
	s := &server{w: &out}
	s.handleAgentEvent("session-1", agentpkg.Event{
		Type:       agentpkg.EventToolExecutionEnd,
		ToolCallID: "write-1",
		ToolName:   "write_file",
		ToolDiff:   &agentpkg.FileDiff{Path: "/tmp/new.txt", NewText: "new"},
	})
	diff := jsonLines(t, &out)[0]["params"].(map[string]any)["update"].(map[string]any)["content"].([]any)[0].(map[string]any)
	if value, ok := diff["oldText"]; !ok || value != nil {
		t.Fatalf("created-file oldText = %#v, want explicit null", diff["oldText"])
	}
}

func TestStreamedContentChunksShareMessageID(t *testing.T) {
	var out bytes.Buffer
	s := &server{w: &out, sessions: map[string]*sessionRuntime{}}
	s.handleAgentEvent("session-1", agentpkg.Event{Type: agentpkg.EventTextDelta, TextDelta: "hello"})
	s.handleAgentEvent("session-1", agentpkg.Event{Type: agentpkg.EventTextDelta, TextDelta: " world"})
	lines := jsonLines(t, &out)
	if len(lines) != 2 {
		t.Fatalf("updates = %d, want 2", len(lines))
	}
	first := lines[0]["params"].(map[string]any)["update"].(map[string]any)
	second := lines[1]["params"].(map[string]any)["update"].(map[string]any)
	if first["messageId"] == "" || first["messageId"] != second["messageId"] {
		t.Fatalf("message IDs = %#v and %#v, want stable non-empty ID", first["messageId"], second["messageId"])
	}
}

func TestPromptSupportsResourceLinksAndRejectsUnadvertisedContent(t *testing.T) {
	if _, err := promptToText([]contentBlock{{Type: "resource_link", Name: "notes", URI: "file:///notes.md"}}); err == nil {
		t.Fatal("resource link was converted into prompt text")
	}
	if _, err := promptToText([]contentBlock{{Type: "image"}}); err == nil {
		t.Fatal("unadvertised image content was accepted")
	}
	size := 12
	if _, err := promptToRunInput([]contentBlock{{Type: "text", Text: "read this"}, {Type: "resource_link", Name: "notes", Title: "Notes", Description: "A note", URI: "file:///notes.md", MimeType: "text/markdown", Size: &size}}); err == nil {
		t.Fatal("text-only projection accepted a resource link")
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "notes.md")
	if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	text, ingresses, err := promptToIngresses([]contentBlock{{Type: "text", Text: "read this"}, {Type: "resource_link", Name: "notes", URI: "file://" + path, MimeType: "text/markdown"}}, tmp, nil, "acp:test")
	if err != nil || text != "read this" || len(ingresses) != 1 {
		t.Fatalf("resource link ingress = %q %#v, %v", text, ingresses, err)
	}
	stream, err := ingresses[0].Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(stream.Reader)
	stream.Reader.Close()
	if err != nil || string(data) != "hello" {
		t.Fatalf("resource bytes = %q, %v", data, err)
	}
	if _, _, err := promptToIngresses([]contentBlock{{Type: "resource_link", Name: "remote", URI: "https://example.com/a"}}, tmp, nil, "acp:test"); err == nil {
		t.Fatal("remote resource URI was accepted")
	}
}

func TestNormalizeAdditionalDirectories(t *testing.T) {
	directories, err := agentruntime.NormalizeAdditionalDirectories([]string{"/tmp/extra/..", "/tmp/other", "/tmp/other"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(directories), "[/tmp /tmp/other]"; got != want {
		t.Fatalf("directories = %s, want %s", got, want)
	}
	if _, err := agentruntime.NormalizeAdditionalDirectories([]string{"relative"}); err == nil {
		t.Fatal("relative additional directory was accepted")
	}
}

func TestUsageEventEmitsUsageUpdate(t *testing.T) {
	var out bytes.Buffer
	s := &server{
		m: &provider.Model{ContextWindow: 100, Cost: provider.ModelPricing{Input: 1, Output: 2}},
		sessions: map[string]*sessionRuntime{
			"session-1": {},
		},
		w: &out,
	}
	// A turn that finishes before any usage event must keep the standard ACP
	// payload: the additive cache extension is omitted, never zero-filled.
	s.handleAgentEvent("session-1", agentpkg.Event{Type: agentpkg.EventDone})
	if meta := mothxUsageMeta(t, lastUsageUpdate(t, &out)); meta != nil {
		t.Fatalf("usage_update without usage carried _meta %#v", meta)
	}

	s.handleAgentEvent("session-1", agentpkg.Event{
		Type: agentpkg.EventUsage,
		Usage: &agentpkg.Usage{
			InputTokens:  10,
			OutputTokens: 5,
			CacheRead:    40,
			CacheWrite:   8,
			TotalTokens:  63,
		},
		ContextUsage: &agentpkg.ContextUsage{Tokens: 20, ContextWindow: 100},
	})

	update := lastUsageUpdate(t, &out)
	if update["used"] != float64(20) || update["size"] != float64(100) {
		t.Fatalf("usage update = %#v", update)
	}
	cost := update["cost"].(map[string]any)
	if cost["currency"] != "USD" || cost["amount"] != 0.00002 {
		t.Fatalf("usage cost = %#v", cost)
	}
	// The cache denominator is the full input footprint (TotalTokens-Output =
	// 58), so Anthropic-style separated cache counts and OpenAI-style folded
	// ones share one hit-ratio basis with the TUI.
	if meta := mothxUsageMeta(t, update); meta["cacheRead"] != float64(40) || meta["cacheWrite"] != float64(8) || meta["totalInputTokens"] != float64(58) {
		t.Fatalf("usage cache = %#v", update)
	}

	// The next model turn accumulates on the same session baseline.
	s.handleAgentEvent("session-1", agentpkg.Event{
		Type: agentpkg.EventUsage,
		Usage: &agentpkg.Usage{
			InputTokens:  4,
			OutputTokens: 2,
			CacheRead:    60,
			TotalTokens:  66,
		},
		ContextUsage: &agentpkg.ContextUsage{Tokens: 24, ContextWindow: 100},
	})
	if meta := mothxUsageMeta(t, lastUsageUpdate(t, &out)); meta["cacheRead"] != float64(100) || meta["cacheWrite"] != float64(8) || meta["totalInputTokens"] != float64(122) {
		t.Fatalf("accumulated usage cache = %#v, want 100/8/122", lastUsageUpdate(t, &out))
	}
}

// TestPersistedSessionUsageSharesUsageUpdateBaseline covers session/load and
// ACP restarts: cost and the prompt-cache projection are both rebuilt from the
// canonical history in one pass, so a single usage_update never mixes a
// whole-session cost with a since-this-connection cache baseline.
func TestPersistedSessionUsageSharesUsageUpdateBaseline(t *testing.T) {
	dir := t.TempDir()
	mgr := session.New(dir, dir)
	if err := mgr.InitWithID("seeded-session"); err != nil {
		t.Fatalf("initialize session: %v", err)
	}
	history := []*provider.Usage{
		// Exact binary totals keep this baseline assertion free of float noise.
		{Input: 10, Output: 5, CacheRead: 40, CacheWrite: 8, TotalTokens: 63, Cost: provider.Cost{Total: 0.25}},
		{Input: 4, Output: 2, CacheRead: 60, TotalTokens: 66, Cost: provider.Cost{Total: 0.5}},
	}
	for _, usage := range history {
		if _, err := mgr.AppendMessage(provider.Message{Role: "assistant", Content: "done", Usage: usage}); err != nil {
			t.Fatalf("append usage message: %v", err)
		}
	}

	cost, usage := persistedSessionUsage(mgr, nil)
	if got, want := usage.inputTotal, 122; got != want {
		t.Fatalf("seeded input total = %d, want %d", got, want)
	}
	if usage.cacheRead != 100 || usage.cacheWrite != 8 {
		t.Fatalf("seeded cache = %d/%d, want 100/8", usage.cacheRead, usage.cacheWrite)
	}

	var out bytes.Buffer
	s := &server{
		m:        &provider.Model{ContextWindow: 100},
		sessions: map[string]*sessionRuntime{"session-1": {cost: cost, usageCache: usage}},
		w:        &out,
	}
	s.handleAgentEvent("session-1", agentpkg.Event{
		Type:         agentpkg.EventUsage,
		Usage:        &agentpkg.Usage{InputTokens: 6, OutputTokens: 4, CacheRead: 0, TotalTokens: 10},
		ContextUsage: &agentpkg.ContextUsage{Tokens: 30, ContextWindow: 100},
	})
	reloaded := lastUsageUpdate(t, &out)
	meta := mothxUsageMeta(t, reloaded)
	// 122 persisted input tokens plus the live turn's 6 (TotalTokens-Output),
	// with the persisted cache reads carried forward.
	if meta["totalInputTokens"] != float64(128) || meta["cacheRead"] != float64(100) || meta["cacheWrite"] != float64(8) {
		t.Fatalf("reloaded usage projection = %#v, want the persisted baseline plus the live turn", reloaded)
	}
	if amount := reloaded["cost"].(map[string]any)["amount"]; amount != 0.75 {
		t.Fatalf("reloaded cost = %v, want the seeded 0.25+0.5", amount)
	}
}

// lastUsageUpdate decodes the most recent usage_update notification in out.
func lastUsageUpdate(t *testing.T, out *bytes.Buffer) map[string]any {
	t.Helper()
	messages := jsonLines(t, out)
	last := messages[len(messages)-1]
	params, ok := last["params"].(map[string]any)
	if !ok {
		t.Fatalf("notification has no params: %#v", last)
	}
	update, ok := params["update"].(map[string]any)
	if !ok {
		t.Fatalf("params has no update: %#v", last)
	}
	if update["sessionUpdate"] != "usage_update" {
		t.Fatalf("update type = %v, want usage_update", update["sessionUpdate"])
	}
	return update
}

// mothxUsageMeta returns the additive mothx.dev projection on a usage_update,
// or nil when the server omitted the extension.
func mothxUsageMeta(t *testing.T, update map[string]any) map[string]any {
	t.Helper()
	meta, ok := update["_meta"].(map[string]any)
	if !ok {
		return nil
	}
	dev, ok := meta[mothxExtensionNamespace].(map[string]any)
	if !ok {
		t.Fatalf("_meta has no %q extension: %#v", mothxExtensionNamespace, update)
	}
	return dev
}

func newTestSession(t *testing.T, cwd, dir, id string, messages int) {
	t.Helper()
	mgr := session.New(cwd, dir)
	if err := mgr.InitWithID(id); err != nil {
		t.Fatalf("initialize session: %v", err)
	}
	for i := 0; i < messages; i++ {
		if _, err := mgr.AppendMessage(provider.NewUserMessage(fmt.Sprintf("message %d", i))); err != nil {
			t.Fatalf("append message %d: %v", i, err)
		}
	}
}

func testSessionServer(cwd, dir string, out *bytes.Buffer) *server {
	return &server{
		settings:   &config.Settings{SessionDir: dir},
		sbMgr:      sandbox.NewManager(cwd),
		sessions:   make(map[string]*sessionRuntime),
		toolTitles: make(map[string]string),
		mcpNotify:  make(map[string]bool),
		w:          out,
	}
}

func jsonLines(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var messages []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var message map[string]any
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatalf("decode JSON-RPC message %q: %v", line, err)
		}
		messages = append(messages, message)
	}
	return messages
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
