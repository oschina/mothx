package skillhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Yuandun (源盾) exposes a public, anonymous, read-only skill marketplace.
// Its document defines relative paths, so the deployment origin is centralized
// here and can still be overridden through skillHub.markets[].apiURL.
const (
	YuandunDefaultBaseURL = "https://yuandun.gitee.com"

	yuandunSearchPath      = "/yuandun/ui/skills"
	yuandunDetailPath      = "/yuandun/ui/skills/detail"
	yuandunFilesPath       = "/yuandun/ui/skills/files"
	yuandunFileContentPath = "/yuandun/ui/skills/file"
	yuandunAuditPath       = "/yuandun/ui/skills/security-audit"
	yuandunDownloadPath    = "/yuandun/ui/skill-downloads"

	yuandunClientSource = "MothX"

	maxYuandunContentBytes = 4 << 20
	maxYuandunPageSize     = 30
)

// ErrYuandunNotConfigured reports a zero-value client without a deployment
// origin. NewYuandunClient always applies YuandunDefaultBaseURL.
var ErrYuandunNotConfigured = errors.New("yuandun skill market client has no API URL")

type YuandunClient struct {
	baseURL    string
	httpClient *http.Client

	mu         sync.RWMutex
	packageSHA map[string]string
}

func NewYuandunClient(baseURL string, client *http.Client) *YuandunClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = YuandunDefaultBaseURL
	}
	base := newHTTPClient(client)
	wrapped := *base
	next := wrapped.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	wrapped.Transport = yuandunTransport{next: next}
	return &YuandunClient{
		baseURL:    baseURL,
		httpClient: &wrapped,
		packageSHA: map[string]string{},
	}
}

// yuandunTransport marks every 源盾 API request with the MothX source so
// server-side attribution is consistent even outside the download counter.
type yuandunTransport struct{ next http.RoundTripper }

func (t yuandunTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("X-Client-Source", yuandunClientSource)
	clone.Header.Set("User-Agent", yuandunClientSource)
	return t.next.RoundTrip(clone)
}

func (c *YuandunClient) Market() MarketInfo {
	return MarketInfo{
		ID:      MarketYuandun,
		Name:    "源盾",
		SiteURL: c.baseURL,
		Capabilities: MarketCapabilities{
			Search:           true,
			List:             true,
			CursorPagination: true,
			Categories:       true,
			FileList:         true,
			FileContent:      true,
			Evaluation:       true,
		},
	}
}

func (c *YuandunClient) Search(ctx context.Context, q SearchQuery) (SearchPage, error) {
	if err := c.requireBaseURL(); err != nil {
		return SearchPage{}, err
	}
	limit := yuandunPageSize(q.Limit)
	values := url.Values{"pageSize": {strconv.Itoa(limit)}, "sortBy": {yuandunSort(q.Sort)}}
	if q.Query != "" {
		values.Set("keyword", q.Query)
	}
	if q.Category != "" {
		values.Set("category", q.Category)
	}
	if q.Cursor != "" {
		values.Set("cursor", q.Cursor)
	}
	data, err := getYuandunData[yuandunSearchData](ctx, c.httpClient, endpoint(c.baseURL, yuandunSearchPath, values))
	if err != nil {
		return SearchPage{}, err
	}
	items := make([]SkillSummary, 0, len(data.Items))
	for i := range data.Items {
		items = append(items, data.Items[i].summary())
	}
	nextCursor := ""
	if data.NextCursor != nil {
		nextCursor = *data.NextCursor
	}
	return SearchPage{Items: items, NextCursor: nextCursor, PageSize: limit}, nil
}

// UserSkills is not exposed by the public 源盾 API. Author filtering is only
// available through the market's own search syntax.
func (c *YuandunClient) UserSkills(context.Context, string, UserSkillsQuery) (SearchPage, error) {
	return SearchPage{}, fmt.Errorf("yuandun does not expose user skill listings")
}

func (c *YuandunClient) Detail(ctx context.Context, id SkillID) (SkillDetail, error) {
	if err := c.requireBaseURL(); err != nil {
		return SkillDetail{}, err
	}
	values := url.Values{"name": {id.ID}}
	data, err := getYuandunData[yuandunDetail](ctx, c.httpClient, endpoint(c.baseURL, yuandunDetailPath, values))
	if err != nil {
		return SkillDetail{}, err
	}
	version := yuandunString(data.Version)
	if data.PackageSHA256 != nil && *data.PackageSHA256 != "" {
		c.rememberPackageSHA(id.ID, version, *data.PackageSHA256)
	}
	summary := data.summary(id.ID)
	detail := SkillDetail{
		SkillSummary:    summary,
		SecurityReports: data.securityReports(),
	}
	return detail, nil
}

func (c *YuandunClient) Files(ctx context.Context, id SkillID, version string) ([]SkillFile, error) {
	if err := c.requireBaseURL(); err != nil {
		return nil, err
	}
	values := url.Values{"name": {id.ID}}
	if version != "" {
		values.Set("version", version)
	}
	data, err := getYuandunData[yuandunFileTree](ctx, c.httpClient, endpoint(c.baseURL, yuandunFilesPath, values))
	if err != nil {
		return nil, err
	}
	files := make([]SkillFile, 0, len(data.Files))
	for _, file := range data.Files {
		files = append(files, SkillFile{Path: file.Path, Size: file.Size})
	}
	return files, nil
}

func (c *YuandunClient) Evaluation(ctx context.Context, id SkillID) (any, error) {
	if err := c.requireBaseURL(); err != nil {
		return nil, err
	}
	values := url.Values{"name": {id.ID}}
	data, err := getYuandunData[yuandunSecurityAudit](ctx, c.httpClient, endpoint(c.baseURL, yuandunAuditPath, values))
	if err != nil {
		return nil, err
	}
	dimensions := make(map[string]any, len(data.Dimensions))
	for _, dimension := range data.Dimensions {
		entry := map[string]any{
			"dimensionName": dimension.DimensionName,
			"status":        dimension.Status,
			"statusName":    dimension.StatusName,
			"findings":      dimension.Findings,
		}
		if dimension.Summary != nil {
			entry["summary"] = *dimension.Summary
		}
		dimensions[dimension.Dimension] = entry
	}
	evaluation := map[string]any{
		"securityStatus":     data.SecurityStatus,
		"securityStatusName": data.SecurityStatusName,
		"dimensions":         dimensions,
	}
	if data.Summary != nil {
		evaluation["summary"] = *data.Summary
	}
	return evaluation, nil
}

func (c *YuandunClient) FileContent(ctx context.Context, id SkillID, version, path string) (string, error) {
	if err := c.requireBaseURL(); err != nil {
		return "", err
	}
	values := url.Values{"name": {id.ID}, "path": {path}}
	if version != "" {
		values.Set("version", version)
	}
	if sha := c.packageSHAFor(id.ID, version); sha != "" {
		values.Set("packageSha256", sha)
	}
	return getYuandunText(ctx, c.httpClient, endpoint(c.baseURL, yuandunFileContentPath, values), maxYuandunContentBytes)
}

func (c *YuandunClient) DownloadSources(id SkillID, version string) []DownloadSource {
	if c.baseURL == "" {
		return nil
	}
	values := url.Values{"name": {id.ID}}
	if version != "" {
		values.Set("version", version)
	}
	if sha := c.packageSHAFor(id.ID, version); sha != "" {
		values.Set("packageSha256", sha)
	}
	return []DownloadSource{{URL: endpoint(c.baseURL, yuandunDownloadPath, values), Kind: "api"}}
}

func (c *YuandunClient) Download(ctx context.Context, id SkillID, version string) (io.ReadCloser, DownloadMeta, error) {
	if err := c.requireBaseURL(); err != nil {
		return nil, DownloadMeta{}, err
	}
	values := url.Values{"name": {id.ID}}
	if version != "" {
		values.Set("version", version)
	}
	if sha := c.packageSHAFor(id.ID, version); sha != "" {
		values.Set("packageSha256", sha)
	}
	source := endpoint(c.baseURL, yuandunDownloadPath, values)
	body, sha, err := downloadYuandun(ctx, c.httpClient, source)
	if err != nil {
		return nil, DownloadMeta{}, err
	}
	return body, DownloadMeta{SourceURL: source, SHA256: sha}, nil
}

func (c *YuandunClient) Categories(context.Context) ([]Category, error) {
	return []Category{
		{Key: "软件研发", Name: "软件研发", NameEn: "Software Development"},
		{Key: "数据与分析", Name: "数据与分析", NameEn: "Data & Analytics"},
		{Key: "内容与创作", Name: "内容与创作", NameEn: "Content & Creation"},
		{Key: "业务与管理", Name: "业务与管理", NameEn: "Business & Management"},
		{Key: "AI与自动化", Name: "AI与自动化", NameEn: "AI & Automation"},
		{Key: "其他", Name: "其他", NameEn: "Other"},
	}, nil
}

func (c *YuandunClient) requireBaseURL() error {
	if c.baseURL == "" {
		return ErrYuandunNotConfigured
	}
	return nil
}

func (c *YuandunClient) rememberPackageSHA(id, version, sha string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.packageSHA == nil {
		c.packageSHA = map[string]string{}
	}
	c.packageSHA[yuandunSHAKey(id, version)] = sha
	if version != "" {
		c.packageSHA[yuandunSHAKey(id, "")] = sha
	}
}

func (c *YuandunClient) packageSHAFor(id, version string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if sha := c.packageSHA[yuandunSHAKey(id, version)]; sha != "" {
		return sha
	}
	return c.packageSHA[yuandunSHAKey(id, "")]
}

func yuandunSHAKey(id, version string) string { return id + "\x00" + version }

func yuandunPageSize(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > maxYuandunPageSize {
		return maxYuandunPageSize
	}
	return limit
}

func yuandunSort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "downloads", "installs":
		return "DOWNLOADS"
	case "stars":
		return "STARS"
	case "latest", "updated", "updated_at", "recent":
		return "LATEST"
	default:
		return "COMPREHENSIVE"
	}
}

func yuandunSlug(name string) string {
	if index := strings.LastIndex(name, "/"); index >= 0 && index+1 < len(name) {
		return name[index+1:]
	}
	return name
}

func yuandunAuthor(name string) string {
	if index := strings.Index(name, "/"); index > 0 {
		return name[:index]
	}
	return ""
}

func yuandunString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func yuandunTime(value *string) time.Time {
	if value == nil || *value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func yuandunSuspicious(status string) bool {
	return status == "RISKY" || status == "SUSPECTED"
}

type yuandunSearchData struct {
	Items      []yuandunCard `json:"items"`
	NextCursor *string       `json:"nextCursor"`
	HasMore    bool          `json:"hasMore"`
}

type yuandunCard struct {
	Name               string  `json:"name"`
	DisplayName        string  `json:"displayName"`
	NameZh             *string `json:"nameZh"`
	LogoURL            *string `json:"logoUrl"`
	Status             string  `json:"status"`
	SecurityStatus     string  `json:"securityStatus"`
	SecurityStatusName string  `json:"securityStatusName"`
	Description        string  `json:"description"`
	Category           string  `json:"category"`
	RuntimeType        *string `json:"runtimeType"`
	StarCount          int64   `json:"starCount"`
	Installs           int64   `json:"installs"`
	CurrentVersion     *string `json:"currentVersion"`
}

func (c yuandunCard) summary() SkillSummary {
	displayName := c.DisplayName
	if displayName == "" {
		displayName = yuandunString(c.NameZh)
	}
	if displayName == "" {
		displayName = yuandunSlug(c.Name)
	}
	return SkillSummary{
		Market:             MarketYuandun,
		ID:                 c.Name,
		Slug:               yuandunSlug(c.Name),
		Name:               displayName,
		DisplayName:        displayName,
		Description:        c.Description,
		Version:            yuandunString(c.CurrentVersion),
		Author:             yuandunAuthor(c.Name),
		Category:           c.Category,
		IconURL:            yuandunString(c.LogoURL),
		Source:             "源盾",
		Downloads:          c.Installs,
		Installs:           c.Installs,
		Stars:              c.StarCount,
		Suspicious:         yuandunSuspicious(c.SecurityStatus),
		SecurityStatus:     c.SecurityStatus,
		SecurityStatusName: c.SecurityStatusName,
	}
}

type yuandunDetail struct {
	Name                 string             `json:"name"`
	DisplayName          string             `json:"displayName"`
	NameZh               *string            `json:"nameZh"`
	LogoURL              *string            `json:"logoUrl"`
	Status               string             `json:"status"`
	SecurityStatus       string             `json:"securityStatus"`
	SecurityStatusName   string             `json:"securityStatusName"`
	LastAuditAt          *string            `json:"lastAuditAt"`
	Version              *string            `json:"version"`
	LatestVersion        string             `json:"latestVersion"`
	ReleaseNotes         *string            `json:"releaseNotes"`
	ReleasedAt           *string            `json:"releasedAt"`
	Source               *yuandunSource     `json:"source"`
	StarCount            int64              `json:"starCount"`
	DownloadCount        int64              `json:"downloadCount"`
	LicenseName          *string            `json:"licenseName"`
	LicenseStatus        *string            `json:"licenseStatus"`
	LicenseStatusName    *string            `json:"licenseStatusName"`
	PackageSHA256        *string            `json:"packageSha256"`
	PackageSize          *int64             `json:"packageSize"`
	FileCount            *int64             `json:"fileCount"`
	TotalFileSize        *int64             `json:"totalFileSize"`
	DownloadDecision     *string            `json:"downloadDecision"`
	RequiresConfirmation bool               `json:"requiresConfirmation"`
	DownloadURL          *string            `json:"downloadUrl"`
	Capability           *yuandunCapability `json:"capability"`
	SkillMD              *yuandunSkillMD    `json:"skillMd"`
}

func (d yuandunDetail) summary(id string) SkillSummary {
	displayName := d.DisplayName
	if displayName == "" {
		displayName = yuandunString(d.NameZh)
	}
	if displayName == "" {
		displayName = yuandunSlug(d.Name)
	}
	description := ""
	category := ""
	var tags []string
	if d.Capability != nil {
		description = yuandunString(d.Capability.FunctionSummary)
		if description == "" {
			description = yuandunString(d.Capability.SkillDescription)
		}
		category = yuandunString(d.Capability.SkillCategory)
		tags = append([]string(nil), d.Capability.CapabilityTags...)
	}
	if description == "" {
		description = yuandunString(d.ReleaseNotes)
	}
	author, sourceURL, sourceType := "", "", ""
	if d.Source != nil {
		author = yuandunString(d.Source.Author)
		sourceType = yuandunString(d.Source.SourceType)
		sourceURL = yuandunString(d.Source.RepositoryURL)
		if sourceURL == "" {
			sourceURL = yuandunString(d.Source.SourceURL)
		}
	}
	if author == "" {
		author = yuandunAuthor(d.Name)
	}
	return SkillSummary{
		Market:             MarketYuandun,
		ID:                 d.Name,
		Slug:               yuandunSlug(d.Name),
		Name:               displayName,
		DisplayName:        displayName,
		Description:        description,
		Version:            yuandunString(d.Version),
		Author:             author,
		Category:           category,
		Tags:               tags,
		IconURL:            yuandunString(d.LogoURL),
		SourceURL:          sourceURL,
		Source:             sourceType,
		Downloads:          d.DownloadCount,
		Installs:           d.DownloadCount,
		Stars:              d.StarCount,
		Suspicious:         yuandunSuspicious(d.SecurityStatus),
		SecurityStatus:     d.SecurityStatus,
		SecurityStatusName: d.SecurityStatusName,
		UpdatedAt:          yuandunTime(d.ReleasedAt),
	}
}

func (d yuandunDetail) securityReports() any {
	if d.SecurityStatus == "" {
		return nil
	}
	report := map[string]any{"status": d.SecurityStatus}
	if d.SecurityStatusName != "" {
		report["statusText"] = d.SecurityStatusName
	}
	if d.LastAuditAt != nil {
		report["lastAuditAt"] = *d.LastAuditAt
	}
	return map[string]any{"yuandun": report}
}

type yuandunSource struct {
	SourceType    *string `json:"sourceType"`
	SourceURL     *string `json:"sourceUrl"`
	RepositoryURL *string `json:"repositoryUrl"`
	Author        *string `json:"author"`
	Organization  *string `json:"organization"`
}

type yuandunCapability struct {
	Name                     string              `json:"name"`
	Version                  string              `json:"version"`
	CapabilityStatus         string              `json:"capabilityStatus"`
	SkillDescription         *string             `json:"skillDescription"`
	FunctionSummary          *string             `json:"functionSummary"`
	ExecutionMode            *string             `json:"executionMode"`
	ExecutionModeDescription *string             `json:"executionModeDescription"`
	SkillCategory            *string             `json:"skillCategory"`
	CapabilityTags           []string            `json:"capabilityTags"`
	UsageScenarios           []string            `json:"usageScenarios"`
	SkillWorkflow            []string            `json:"skillWorkflow"`
	RequiresAPIKey           *string             `json:"requiresApiKey"`
	RuntimeDependencies      []yuandunDependency `json:"runtimeDependencies"`
	InputTypes               []string            `json:"inputTypes"`
	InputContents            []string            `json:"inputContents"`
	OutputTypes              []string            `json:"outputTypes"`
	OutputContents           []string            `json:"outputContents"`
	AnalyzedAt               *string             `json:"analyzedAt"`
}

type yuandunDependency struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type yuandunSkillMD struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	StreamURL string `json:"streamUrl"`
}

type yuandunFileTree struct {
	Name          string        `json:"name"`
	Version       string        `json:"version"`
	Files         []yuandunFile `json:"files"`
	FileCount     int64         `json:"fileCount"`
	TotalFileSize int64         `json:"totalFileSize"`
}

type yuandunFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type yuandunSecurityAudit struct {
	Name               string                  `json:"name"`
	Version            string                  `json:"version"`
	SecurityStatus     string                  `json:"securityStatus"`
	SecurityStatusName string                  `json:"securityStatusName"`
	Summary            *string                 `json:"summary"`
	Dimensions         []yuandunAuditDimension `json:"dimensions"`
}

type yuandunAuditDimension struct {
	Dimension     string  `json:"dimension"`
	DimensionName string  `json:"dimensionName"`
	Status        string  `json:"status"`
	StatusName    string  `json:"statusName"`
	Summary       *string `json:"summary"`
	Findings      []any   `json:"findings"`
}

func getYuandunData[T any](ctx context.Context, client *http.Client, endpoint string) (T, error) {
	var zero T
	if _, body, err := getWithStatus(ctx, client, endpoint, defaultMaxJSONBytes); err != nil {
		return zero, err
	} else {
		var envelope struct {
			Result  *bool           `json:"result"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return zero, fmt.Errorf("decode %s: %w", endpoint, err)
		}
		if envelope.Result != nil && !*envelope.Result {
			if envelope.Message == "" {
				envelope.Message = "request failed"
			}
			return zero, fmt.Errorf("yuandun: %s", envelope.Message)
		}
		if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
			return zero, nil
		}
		if err := json.Unmarshal(envelope.Data, &zero); err != nil {
			return zero, fmt.Errorf("decode %s data: %w", endpoint, err)
		}
		return zero, nil
	}
}

func getYuandunText(ctx context.Context, client *http.Client, endpoint string, maxBytes int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/plain, */*")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GET %s: %s: %s", endpoint, resp.Status, yuandunErrorMessage(body))
	}
	if readErr != nil {
		return "", readErr
	}
	if int64(len(body)) > maxBytes {
		return "", fmt.Errorf("response from %s exceeds %d byte limit", endpoint, maxBytes)
	}
	if !utf8.Valid(body) {
		return "", fmt.Errorf("response from %s is not valid UTF-8 text", endpoint)
	}
	return string(body), nil
}

func downloadYuandun(ctx context.Context, client *http.Client, endpoint string) (io.ReadCloser, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/octet-stream, application/zip, */*")
	req.Header.Set("X-Client-Source", yuandunClientSource)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyBytes))
		return nil, "", fmt.Errorf("GET %s: %s: %s", endpoint, resp.Status, yuandunErrorMessage(body))
	}
	return resp.Body, strings.TrimSpace(resp.Header.Get("X-Skill-SHA256")), nil
}

func yuandunErrorMessage(body []byte) string {
	var envelope struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) == nil && strings.TrimSpace(envelope.Message) != "" {
		return strings.TrimSpace(envelope.Message)
	}
	return strings.TrimSpace(string(body))
}
