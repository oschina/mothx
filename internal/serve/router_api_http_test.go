package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/serve/openaiapi"
)

// fakeRouterSessions is the minimal Router projection fixture for the HTTP
// adapter. The adapter only forwards the request, so the fixture records the
// call and returns a canned projection.
type fakeRouterSessions struct {
	activeSessionManager
	catalog     *fakeRouterCatalog
	state       *openaiapi.RouterBindingSummary
	activated   []string
	deactivated bool
}

func (f *fakeRouterSessions) ListACPWorkerProfiles() []openaiapi.ACPWorkerProfileSummary {
	if f.catalog == nil {
		return nil
	}
	return f.catalog.profiles
}

func (f *fakeRouterSessions) PatchACPWorkerProfile(id string, patch config.ACPAgentProfilePatch) ([]openaiapi.ACPWorkerProfileSummary, error) {
	if f.catalog == nil {
		f.catalog = &fakeRouterCatalog{}
	}
	for index := range f.catalog.profiles {
		profile := &f.catalog.profiles[index]
		if profile.ID != id {
			continue
		}
		if patch.Model != nil {
			profile.Model = *patch.Model
		}
		if patch.Driver != nil {
			profile.Driver = *patch.Driver
		}
		if patch.MCPBridge != nil {
			profile.MCPBridge = *patch.MCPBridge
		}
		if patch.CapabilityOverrides != nil {
			profile.CapabilityOverrides = nil
			for name := range *patch.CapabilityOverrides {
				profile.CapabilityOverrides = append(profile.CapabilityOverrides, name)
			}
		}
	}
	return f.catalog.profiles, nil
}

func (f *fakeRouterSessions) GetSessionRouter(string) (*openaiapi.RouterBindingSummary, error) {
	return f.state, nil
}

func (f *fakeRouterSessions) ActivateSessionRouter(_ string, profileIDs []string) (*openaiapi.RouterBindingSummary, error) {
	f.activated = profileIDs
	f.state = &openaiapi.RouterBindingSummary{Enabled: true, ProfileIDs: profileIDs, PrimaryProfile: "codex", Mode: "router"}
	return f.state, nil
}

func (f *fakeRouterSessions) DeactivateSessionRouter(string) (*openaiapi.RouterBindingSummary, error) {
	f.deactivated = true
	f.state = &openaiapi.RouterBindingSummary{Mode: "plan"}
	return f.state, nil
}

type fakeRouterCatalog struct {
	profiles []openaiapi.ACPWorkerProfileSummary
}

func TestACPWorkerProfilesEndpointProjectsCatalog(t *testing.T) {
	rt := &channelRuntime{}
	sessions := &fakeRouterSessions{catalog: &fakeRouterCatalog{profiles: []openaiapi.ACPWorkerProfileSummary{
		{ID: "codex", Enabled: true, RouterEligible: true},
	}}}
	req := httptest.NewRequest(http.MethodGet, "/api/acp/profiles", nil)
	rec := httptest.NewRecorder()
	rt.handleACPWorkerProfiles(sessions)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Profiles []openaiapi.ACPWorkerProfileSummary `json:"profiles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Profiles) != 1 || body.Profiles[0].ID != "codex" {
		t.Fatalf("profiles = %#v", body.Profiles)
	}
}

func TestACPWorkerProfilesEndpointRejectsMutation(t *testing.T) {
	rt := &channelRuntime{}
	sessions := &fakeRouterSessions{catalog: &fakeRouterCatalog{}}
	req := httptest.NewRequest(http.MethodPost, "/api/acp/profiles", nil)
	rec := httptest.NewRecorder()
	rt.handleACPWorkerProfiles(sessions)(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestSessionRouterEndpointActivatesAndDeactivates(t *testing.T) {
	rt := &channelRuntime{}
	sessions := &fakeRouterSessions{state: &openaiapi.RouterBindingSummary{Mode: "plan"}}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/router",
		bytes.NewBufferString(`{"profileIds":["codex","reviewer"]}`))
	rec := httptest.NewRecorder()
	rt.handleSessionRouter(sessions, "s-1", nil, rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("activate status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(sessions.activated) != 2 || sessions.activated[0] != "codex" {
		t.Fatalf("activated profiles = %#v", sessions.activated)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/router",
		bytes.NewBufferString(`{"deactivate":true}`))
	rec = httptest.NewRecorder()
	rt.handleSessionRouter(sessions, "s-1", nil, rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deactivate status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !sessions.deactivated {
		t.Fatal("the deactivate request was not forwarded")
	}
}

func TestSessionRouterEndpointRejectsInvalidBody(t *testing.T) {
	rt := &channelRuntime{}
	sessions := &fakeRouterSessions{}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/router",
		bytes.NewBufferString(`{"profileIds":`))
	rec := httptest.NewRecorder()
	rt.handleSessionRouter(sessions, "s-1", nil, rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid JSON") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestACPWorkerProfilesEndpointPatchesOnlyBehaviorMetadata(t *testing.T) {
	rt := &channelRuntime{}
	sessions := &fakeRouterSessions{catalog: &fakeRouterCatalog{profiles: []openaiapi.ACPWorkerProfileSummary{
		{ID: "codex", Enabled: true, RouterEligible: true, Driver: "generic-acp", MCPBridge: "auto"},
	}}}
	request := httptest.NewRequest(http.MethodPatch, "/api/acp/profiles",
		bytes.NewBufferString(`{"id":"codex","model":"fast-model","driver":"codex-acp","mcpBridge":"off","capabilityOverrides":{"promptImage":false}}`))
	record := httptest.NewRecorder()
	rt.handleACPWorkerProfiles(sessions)(record, request)
	if record.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", record.Code, record.Body.String())
	}
	var body struct {
		Profiles []openaiapi.ACPWorkerProfileSummary `json:"profiles"`
	}
	if err := json.Unmarshal(record.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Profiles) != 1 || body.Profiles[0].Model != "fast-model" || body.Profiles[0].Driver != "codex-acp" || body.Profiles[0].MCPBridge != "off" {
		t.Fatalf("patched profiles = %#v", body.Profiles)
	}
	if len(body.Profiles[0].CapabilityOverrides) != 1 || body.Profiles[0].CapabilityOverrides[0] != "promptImage" {
		t.Fatalf("overrides = %#v", body.Profiles[0].CapabilityOverrides)
	}
}

func TestACPWorkerProfilesEndpointRejectsMissingPatchID(t *testing.T) {
	rt := &channelRuntime{}
	sessions := &fakeRouterSessions{catalog: &fakeRouterCatalog{}}
	request := httptest.NewRequest(http.MethodPatch, "/api/acp/profiles", bytes.NewBufferString(`{"driver":"codex-acp"}`))
	record := httptest.NewRecorder()
	rt.handleACPWorkerProfiles(sessions)(record, request)
	if record.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", record.Code, record.Body.String())
	}
}
