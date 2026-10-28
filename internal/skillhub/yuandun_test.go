package skillhub

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

func TestYuandunSearchMapsCardsAndCursor(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != yuandunSearchPath {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Client-Source"); got != yuandunClientSource {
			t.Errorf("X-Client-Source = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != yuandunClientSource {
			t.Errorf("User-Agent = %q", got)
		}
		query := r.URL.Query()
		if got := query.Get("keyword"); got != "封面" {
			t.Errorf("keyword = %q", got)
		}
		if got := query.Get("category"); got != "内容与创作" {
			t.Errorf("category = %q", got)
		}
		if got := query.Get("sortBy"); got != "DOWNLOADS" {
			t.Errorf("sortBy = %q", got)
		}
		if got := query.Get("pageSize"); got != "30" {
			t.Errorf("pageSize = %q", got)
		}
		if got := query.Get("cursor"); got != "next-page" {
			t.Errorf("cursor = %q", got)
		}
		return jsonResponse(`{"result":true,"message":"OK","data":{"items":[{"name":"owner/douyin-cover","displayName":"抖音封面","nameZh":"抖音封面","logoUrl":null,"status":"ONLINE","securityStatus":"RISKY","securityStatusName":"有风险","description":"封面生成","category":"内容与创作","runtimeType":"LOCAL","starCount":7,"installs":7482,"currentVersion":"3.1.3"}],"nextCursor":"next-page-2","hasMore":true}}`), nil
	})}

	page, err := NewYuandunClient("https://yuandun.test", client).Search(context.Background(), SearchQuery{
		Query: "封面", Category: "内容与创作", Sort: "installs", Limit: 100, Cursor: "next-page",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %#v", page.Items)
	}
	item := page.Items[0]
	if item.Market != MarketYuandun || item.ID != "owner/douyin-cover" || item.Slug != "douyin-cover" || item.Author != "owner" {
		t.Fatalf("unexpected item identity: %#v", item)
	}
	if item.Name != "抖音封面" || item.Version != "3.1.3" || item.Downloads != 7482 || item.Stars != 7 || !item.Suspicious {
		t.Fatalf("unexpected item mapping: %#v", item)
	}
	if item.SecurityStatus != "RISKY" || item.SecurityStatusName != "有风险" {
		t.Fatalf("unexpected security status: %#v", item)
	}
	if page.NextCursor != "next-page-2" || page.PageSize != 30 {
		t.Fatalf("unexpected page: %#v", page)
	}
}

func TestYuandunDetailFilesAuditAndPinnedDownload(t *testing.T) {
	const sha = "662a263212770e81a201ea421e9f5db029a1040188604eec3c90bc52384294bd"
	var detailCalls, filesCalls, auditCalls, contentCalls, downloadCalls int
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		query := r.URL.Query()
		switch r.URL.Path {
		case yuandunDetailPath:
			detailCalls++
			if got := query.Get("name"); got != "owner/douyin-cover" {
				t.Errorf("detail name = %q", got)
			}
			return jsonResponse(`{"result":true,"message":"OK","data":{"name":"owner/douyin-cover","displayName":"抖音封面","nameZh":"抖音封面","status":"ONLINE","securityStatus":"SAFE","securityStatusName":"安全","lastAuditAt":"2026-10-10T23:19:58.000+00:00","version":"3.1.3","latestVersion":"3.1.3","releaseNotes":"修复比例","releasedAt":"2026-10-04T06:44:08.000+00:00","source":{"sourceType":"ClawHub","sourceUrl":"https://clawhub.ai/owner/douyin-cover","repositoryUrl":null,"author":"owner","organization":null},"starCount":7,"downloadCount":7482,"licenseName":"MIT-0","licenseStatus":"LOW","licenseStatusName":"低风险","packageSha256":"` + sha + `","packageSize":1999322,"fileCount":8,"totalFileSize":2084987,"downloadDecision":"READY","requiresConfirmation":false,"downloadUrl":"/yuandun/ui/skill-downloads?name=owner%2Fdouyin-cover","capability":{"name":"owner/douyin-cover","version":"3.1.3","capabilityStatus":"SUCCEEDED","skillDescription":"封面工具","functionSummary":"生成封面提示词","executionMode":"Prompt驱动","skillCategory":"内容与创作","capabilityTags":["提示词工程"],"usageScenarios":["封面"],"requiresApiKey":"no","runtimeDependencies":[],"analyzedAt":"2026-10-10T23:19:49.000+00:00"},"skillMd":{"path":"SKILL.md","size":6628,"streamUrl":"/yuandun/ui/skills/file?name=owner%2Fdouyin-cover&path=SKILL.md"}}}`), nil
		case yuandunFilesPath:
			filesCalls++
			if got := query.Get("version"); got != "3.1.3" {
				t.Errorf("files version = %q", got)
			}
			return jsonResponse(`{"result":true,"message":"OK","data":{"name":"owner/douyin-cover","version":"3.1.3","files":[{"path":"SKILL.md","size":6628},{"path":"README.md","size":3383}],"fileCount":2,"totalFileSize":10011}}`), nil
		case yuandunAuditPath:
			auditCalls++
			return jsonResponse(`{"result":true,"message":"OK","data":{"name":"owner/douyin-cover","version":"3.1.3","securityStatus":"SAFE","securityStatusName":"安全","summary":"未发现风险","dimensions":[{"dimension":"instruction_security","dimensionName":"指令安全","status":"SAFE","statusName":"未发现风险","summary":"ok","findings":[]},{"dimension":"code_security","dimensionName":"代码安全","status":"SAFE","statusName":"未发现风险","summary":"ok","findings":[]}]}}`), nil
		case yuandunFileContentPath:
			contentCalls++
			if got := query.Get("packageSha256"); got != sha {
				t.Errorf("content packageSha256 = %q", got)
			}
			if got := query.Get("path"); got != "SKILL.md" {
				t.Errorf("content path = %q", got)
			}
			return jsonResponse("# Skill\n"), nil
		case yuandunDownloadPath:
			downloadCalls++
			if got := r.Header.Get("X-Client-Source"); got != yuandunClientSource {
				t.Errorf("X-Client-Source = %q", got)
			}
			if got := query.Get("packageSha256"); got != sha {
				t.Errorf("download packageSha256 = %q", got)
			}
			if got := query.Get("version"); got != "3.1.3" {
				t.Errorf("download version = %q", got)
			}
			response := jsonResponse("PK\x03\x04zip")
			response.Header.Set("X-Skill-SHA256", sha)
			return response, nil
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header), Request: r}, nil
		}
	})}

	market := NewYuandunClient("https://yuandun.test", client)
	detail, err := market.Detail(context.Background(), SkillID{Market: MarketYuandun, ID: "owner/douyin-cover"})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Version != "3.1.3" || detail.Downloads != 7482 || detail.Category != "内容与创作" || detail.Author != "owner" || detail.Source != "ClawHub" {
		t.Fatalf("unexpected detail: %#v", detail)
	}
	if !strings.Contains(detail.Description, "生成封面提示词") {
		t.Fatalf("description = %q", detail.Description)
	}
	reports, ok := detail.SecurityReports.(map[string]any)
	if !ok || reports["yuandun"] == nil {
		t.Fatalf("security reports = %#v", detail.SecurityReports)
	}

	files, err := market.Files(context.Background(), SkillID{Market: MarketYuandun, ID: "owner/douyin-cover"}, detail.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "SKILL.md" {
		t.Fatalf("files = %#v", files)
	}

	evaluation, err := market.Evaluation(context.Background(), SkillID{Market: MarketYuandun, ID: "owner/douyin-cover"})
	if err != nil {
		t.Fatal(err)
	}
	dimensions, ok := evaluation.(map[string]any)["dimensions"].(map[string]any)
	if !ok || len(dimensions) != 2 {
		t.Fatalf("evaluation = %#v", evaluation)
	}

	content, err := market.FileContent(context.Background(), SkillID{Market: MarketYuandun, ID: "owner/douyin-cover"}, detail.Version, "SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if content != "# Skill\n" {
		t.Fatalf("content = %q", content)
	}

	body, meta, err := market.Download(context.Background(), SkillID{Market: MarketYuandun, ID: "owner/douyin-cover"}, detail.Version)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, _ := io.ReadAll(body)
	if string(data) != "PK\x03\x04zip" || !strings.Contains(meta.SourceURL, yuandunDownloadPath) || meta.SHA256 != sha {
		t.Fatalf("download = %q meta=%#v", data, meta)
	}
	if detailCalls != 1 || filesCalls != 1 || auditCalls != 1 || contentCalls != 1 || downloadCalls != 1 {
		t.Fatalf("unexpected call counts: detail=%d files=%d audit=%d content=%d download=%d", detailCalls, filesCalls, auditCalls, contentCalls, downloadCalls)
	}
}

func TestYuandunUsesDefaultOrigin(t *testing.T) {
	market := NewYuandunClient("", nil)
	if market.baseURL != YuandunDefaultBaseURL {
		t.Fatalf("baseURL = %q", market.baseURL)
	}
	if market.Market().SiteURL != YuandunDefaultBaseURL {
		t.Fatalf("siteURL = %q", market.Market().SiteURL)
	}
	if sources := market.DownloadSources(SkillID{Market: MarketYuandun, ID: "owner/skill"}, "1.0.0"); len(sources) != 1 || !strings.HasPrefix(sources[0].URL, YuandunDefaultBaseURL) {
		t.Fatalf("download sources = %#v", sources)
	}
	custom := NewYuandunClient("https://custom.test/", nil)
	if custom.baseURL != "https://custom.test" {
		t.Fatalf("custom baseURL = %q", custom.baseURL)
	}
}

func TestMarketsProjectYuandunBeforeSkillHub(t *testing.T) {
	service := NewService("", nil, nil)
	markets := service.Markets()
	if len(markets) < 3 {
		t.Fatalf("markets = %#v", markets)
	}
	if markets[0].ID != MarketYuandun || markets[1].ID != MarketSkillHub || markets[2].ID != MarketClawHub {
		t.Fatalf("market order = %#v", markets)
	}

	clients := ClientsForSettings(config.SkillHubSettings{})
	if len(clients) != 3 || clients[0].Market().ID != MarketYuandun {
		t.Fatalf("default clients = %#v", clients)
	}
}

func TestResolveDefaultMarketPrefersYuandunByDefault(t *testing.T) {
	if got := ResolveDefaultMarket(config.SkillHubSettings{DefaultMarket: "yuandun"}); got != MarketYuandun {
		t.Fatalf("default = %q", got)
	}
	if got := ResolveDefaultMarket(config.SkillHubSettings{DefaultMarket: "skillhub.cn"}); got != MarketYuandun {
		t.Fatalf("legacy default = %q", got)
	}
	custom := config.SkillHubSettings{
		DefaultMarket: "skillhub.cn",
		Markets:       []config.SkillHubMarketSettings{{ID: string(MarketSkillHub), Enabled: true}},
	}
	if got := ResolveDefaultMarket(custom); got != MarketSkillHub {
		t.Fatalf("custom legacy default = %q", got)
	}
	settings := config.SkillHubSettings{
		DefaultMarket: "yuandun",
		Markets: []config.SkillHubMarketSettings{
			{ID: string(MarketYuandun), Enabled: false, APIURL: YuandunDefaultBaseURL},
			{ID: string(MarketSkillHub), Enabled: true},
		},
	}
	if got := ResolveDefaultMarket(settings); got != MarketSkillHub {
		t.Fatalf("disabled default = %q", got)
	}
}

func TestVerifyArchiveSHA256RejectsMismatch(t *testing.T) {
	path := t.TempDir() + "/skill.zip"
	if err := os.WriteFile(path, []byte("zip"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyArchiveSHA256(path, "deadbeef"); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestYuandunRejectsFailedEnvelope(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(`{"result":false,"message":"访问过于频繁，请稍后重试","data":null,"traceId":null}`), nil
	})}
	_, err := NewYuandunClient("https://yuandun.test", client).Search(context.Background(), SearchQuery{Limit: 10})
	if err == nil || !strings.Contains(err.Error(), "访问过于频繁") {
		t.Fatalf("error = %v", err)
	}
}
