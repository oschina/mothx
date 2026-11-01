package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newFilesFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFileForTest(t, filepath.Join(root, "zeta.txt"), "hello world\n")
	writeFileForTest(t, filepath.Join(root, "alpha.txt"), "alpha\n")
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileForTest(t, filepath.Join(root, "docs", "guide.md"), "# Guide\n")
	writeFileForTest(t, filepath.Join(root, "blob.bin"), "\x7fELF\x00\x01")
	return root
}

func writeFileForTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFilesListHandlerProjection(t *testing.T) {
	rt := &channelRuntime{}
	root := newFilesFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/files/list?path="+root, nil)
	rec := httptest.NewRecorder()
	rt.handleFilesList(nil)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Root    string `json:"root"`
		Dir     string `json:"dir"`
		Entries []struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Dir  bool   `json:"dir"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Dir != "" || len(out.Entries) != 4 {
		t.Fatalf("listing = %+v", out)
	}
	if out.Entries[0].Name != "docs" || !out.Entries[0].Dir {
		t.Errorf("first entry should be the directory: %+v", out.Entries[0])
	}

	req = httptest.NewRequest(http.MethodGet, "/api/files/list?path="+root+"&dir=../etc", nil)
	rec = httptest.NewRecorder()
	rt.handleFilesList(nil)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("traversal listing status = %d", rec.Code)
	}
}

func TestFilesContentHandlerProjection(t *testing.T) {
	rt := &channelRuntime{}
	root := newFilesFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/files/content?path="+root+"&file=docs/guide.md", nil)
	rec := httptest.NewRecorder()
	rt.handleFilesContent(nil)(rec, req)
	var out struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Lang    string `json:"lang"`
		Binary  bool   `json:"binary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || out.Path != "docs/guide.md" || out.Content != "# Guide\n" || out.Lang != "markdown" {
		t.Errorf("content = %+v status=%d", out, rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/files/content?path="+root+"&file=blob.bin", nil)
	rec = httptest.NewRecorder()
	rt.handleFilesContent(nil)(rec, req)
	binOut := struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Lang    string `json:"lang"`
		Binary  bool   `json:"binary"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &binOut); err != nil {
		t.Fatal(err)
	}
	if !binOut.Binary || binOut.Content != "" {
		t.Errorf("binary file should not return content: %+v", binOut)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/files/content?path="+root+"&file=docs", nil)
	rec = httptest.NewRecorder()
	rt.handleFilesContent(nil)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("directory content status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/files/content?path="+root+"&file=../../etc/passwd", nil)
	rec = httptest.NewRecorder()
	rt.handleFilesContent(nil)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("traversal content status = %d", rec.Code)
	}
}

func TestFilesOpenRequiresTerminalGate(t *testing.T) {
	// Without a config snapshot the gate mirrors the terminal policy and must
	// reject launches instead of executing them (unsafe/disabled).
	rt := &channelRuntime{}
	root := newFilesFixture(t)
	body := `{"path":"` + filepath.ToSlash(root) + `","target":"zeta.txt","action":"reveal"}`
	req := httptest.NewRequest(http.MethodPost, "/api/files/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	rt.handleFilesOpen(nil)(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotImplemented {
		t.Errorf("ungated open status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestFilesOpenRejectsTraversalBeforeLaunch(t *testing.T) {
	rt := &channelRuntime{testOpenersEnabled: true}
	root := newFilesFixture(t)
	sessions := &fakeActiveSessions{dirs: map[string]string{"s1": root}}
	body := `{"sessionId":"s1","target":"../../etc","action":"reveal"}`
	req := httptest.NewRequest(http.MethodPost, "/api/files/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	rt.handleFilesOpen(sessions)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("traversal target status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestFilesOpenRejectsUnknownAction(t *testing.T) {
	rt := &channelRuntime{testOpenersEnabled: true}
	root := newFilesFixture(t)
	sessions := &fakeActiveSessions{dirs: map[string]string{"s1": root}}
	body := `{"sessionId":"s1","target":"zeta.txt","action":"launch-nuke"}`
	req := httptest.NewRequest(http.MethodPost, "/api/files/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	rt.handleFilesOpen(sessions)(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("unknown action status = %d body = %s", rec.Code, rec.Body.String())
	}
}

// fakeActiveSessions is the minimal open-path fixture for workspaceopen's
// RootResolver contract; full manager behavior lives in other tests.
type fakeActiveSessions struct {
	activeSessionManager
	dirs map[string]string
}

func (f *fakeActiveSessions) GetSessionWorkDir(id string) (string, error) {
	if dir, ok := f.dirs[id]; ok {
		return dir, nil
	}
	return "", os.ErrNotExist
}

func TestFilesListNeedsTarget(t *testing.T) {
	rt := &channelRuntime{}
	req := httptest.NewRequest(http.MethodGet, "/api/files/list", nil)
	rec := httptest.NewRecorder()
	rt.handleFilesList(nil)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing target status = %d", rec.Code)
	}
}
