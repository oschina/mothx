package skillhub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/skills"
)

type Service struct {
	clients         map[Market]MarketClient
	globalDir       string
	projectDirs     []string
	officialHandles []string
	cache           *memoryCache
}

func NewService(globalDir string, projectDirs []string, officialHandles []string, clients ...MarketClient) *Service {
	if len(clients) == 0 {
		clients = []MarketClient{NewSkillHubClient("", nil), NewClawHubClient("", nil)}
	}
	indexed := make(map[Market]MarketClient, len(clients))
	for _, client := range clients {
		if client != nil {
			indexed[client.Market().ID] = client
		}
	}
	return &Service{clients: indexed, globalDir: globalDir, projectDirs: projectDirs, officialHandles: officialHandles, cache: newMemoryCache(30 * time.Second)}
}

func NewServiceForWorkDir(globalDir, workDir string, officialHandles []string, clients ...MarketClient) *Service {
	return NewService(globalDir, skills.ProjectSkillDirs(workDir), officialHandles, clients...)
}
func (s *Service) Markets() []MarketInfo {
	result := make([]MarketInfo, 0, len(s.clients))
	for _, client := range s.clients {
		result = append(result, client.Market())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func (s *Service) Search(ctx context.Context, market Market, query SearchQuery) (SearchPage, error) {
	client, err := s.client(market)
	if err != nil {
		return SearchPage{}, err
	}
	key := fmt.Sprintf("search:%s:%+v", market, query)
	if cached, ok := s.cache.getPage(key); ok {
		s.applyInstalled(cached.Items)
		return cached, nil
	}
	page, err := client.Search(ctx, query)
	if err != nil {
		return SearchPage{}, err
	}
	s.applyInstalled(page.Items)
	s.cache.setPage(key, page)
	return page, nil
}
func (s *Service) Categories(ctx context.Context, market Market) ([]Category, error) {
	client, err := s.client(market)
	if err != nil {
		return nil, err
	}
	if !client.Market().Capabilities.Categories {
		return nil, nil
	}
	key := "categories:" + string(market)
	if cached, ok := s.cache.getCategories(key); ok {
		return cached, nil
	}
	categories, err := client.Categories(ctx)
	if err == nil {
		s.cache.setCategories(key, categories)
	}
	return categories, err
}
func (s *Service) Detail(ctx context.Context, market Market, id string) (SkillDetail, error) {
	client, err := s.client(market)
	if err != nil {
		return SkillDetail{}, err
	}
	key := "detail:" + string(market) + ":" + id
	if cached, ok := s.cache.getDetail(key); ok {
		cached.Installed = s.state(market, id)
		return cached, nil
	}
	detail, err := client.Detail(ctx, SkillID{Market: market, ID: id})
	if err != nil {
		return SkillDetail{}, err
	}
	if client.Market().Capabilities.FileList {
		if files, filesErr := client.Files(ctx, SkillID{Market: market, ID: id}, detail.Version); filesErr == nil {
			detail.Files = files
		}
	}
	if client.Market().Capabilities.Evaluation {
		if evaluation, evaluationErr := client.Evaluation(ctx, SkillID{Market: market, ID: id}); evaluationErr == nil {
			detail.Evaluation = evaluation
		}
	}
	detail.DownloadSources = client.DownloadSources(SkillID{Market: market, ID: id}, detail.Version)
	detail.Installed = s.state(market, id)
	if detail.Installed != nil {
		detail.Installed.UpdateAvailable = versionsDiffer(detail.Installed.Version, detail.Version)
	}
	s.cache.setDetail(key, detail)
	return detail, nil
}

func versionsDiffer(installed, remote string) bool {
	return installed != "" && remote != "" && installed != remote
}
func (s *Service) Official(ctx context.Context, query UserSkillsQuery) (SearchPage, error) {
	client, err := s.client(MarketSkillHub)
	if err != nil {
		return SearchPage{}, err
	}
	pageSize := boundedLimit(query.Limit)
	pageNo := query.Page
	if pageNo < 1 {
		pageNo = 1
	}
	allItems := make([]SkillSummary, 0)
	seen := make(map[string]bool)
	for _, handle := range s.officialHandles {
		for remotePage := 1; ; remotePage++ {
			page, err := client.UserSkills(ctx, handle, UserSkillsQuery{Query: query.Query, Limit: 100, Page: remotePage})
			if err != nil {
				return SearchPage{}, fmt.Errorf("official handle %q: %w", handle, err)
			}
			for _, item := range page.Items {
				key := item.ID
				if !seen[key] {
					seen[key] = true
					allItems = append(allItems, item)
				}
			}
			if int64(remotePage*100) >= page.Total {
				break
			}
		}
	}
	sort.SliceStable(allItems, func(i, j int) bool { return allItems[i].Downloads > allItems[j].Downloads })
	start := (pageNo - 1) * pageSize
	if start > len(allItems) {
		start = len(allItems)
	}
	end := start + pageSize
	if end > len(allItems) {
		end = len(allItems)
	}
	result := SearchPage{Items: allItems[start:end], Total: int64(len(allItems)), Page: pageNo, PageSize: pageSize}
	s.applyInstalled(result.Items)
	return result, nil
}
func (s *Service) Install(ctx context.Context, request InstallRequest) (InstallResult, error) {
	if request.Market == "" {
		request.Market = MarketSkillHub
	}
	client, err := s.client(request.Market)
	if err != nil {
		return InstallResult{}, err
	}
	if request.TargetDir == "" {
		return InstallResult{}, errors.New("target directory is required")
	}
	if err := s.validateTargetDir(request.TargetDir, request.Scope); err != nil {
		return InstallResult{}, err
	}
	result, err := Install(ctx, client, request)
	if err == nil {
		s.cache.clear()
	}
	return result, err
}

func (s *Service) validateTargetDir(targetDir, scope string) error {
	target, err := filepath.Abs(filepath.Clean(targetDir))
	if err != nil {
		return err
	}
	if scope != "project" && scope != "global" {
		return fmt.Errorf("scope must be project or global")
	}
	if scope == "global" {
		if s.globalDir == "" {
			return errors.New("global skills directory is unavailable")
		}
		global, absErr := filepath.Abs(filepath.Clean(s.globalDir))
		if absErr != nil || target != global {
			return fmt.Errorf("global scope requires target directory %q", s.globalDir)
		}
		return nil
	}
	for _, root := range s.projectDirs {
		project, absErr := filepath.Abs(filepath.Clean(root))
		if absErr == nil && target == project {
			return nil
		}
	}
	return fmt.Errorf("target directory %q is not a configured project skills directory", targetDir)
}

func (s *Service) Uninstall(market Market, id, scope string) error {
	state := s.state(market, id)
	if state == nil || !state.Installed {
		return fmt.Errorf("skill %q is not installed", id)
	}
	if state.Local {
		return ErrLocalSkillExists
	}
	if scope != "" && scope != state.Scope {
		return fmt.Errorf("skill %q is not installed in %s scope", id, scope)
	}
	metadata, err := readMetadata(state.Dir)
	if err != nil || metadata.Market != market || metadata.ID != id {
		return fmt.Errorf("managed metadata for skill %q is invalid", id)
	}
	if err := s.validateSkillDir(state.Dir); err != nil {
		return err
	}
	info, err := os.Lstat(state.Dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to uninstall a symbolic link")
	}
	if err := os.RemoveAll(state.Dir); err != nil {
		return err
	}
	s.cache.clear()
	return nil
}

func (s *Service) validateSkillDir(dir string) error {
	dir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return err
	}
	roots := append([]string{}, s.projectDirs...)
	if s.globalDir != "" {
		roots = append(roots, s.globalDir)
	}
	for _, root := range roots {
		root, rootErr := filepath.Abs(filepath.Clean(root))
		if rootErr != nil {
			continue
		}
		rel, relErr := filepath.Rel(root, dir)
		if relErr == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("skill directory %q is outside configured skills directories", dir)
}
func (s *Service) Showcase(ctx context.Context, market Market, kind string, query SearchQuery) (SearchPage, error) {
	client, err := s.client(market)
	if err != nil {
		return SearchPage{}, err
	}
	showcase, ok := client.(ShowcaseClient)
	if !ok {
		return SearchPage{}, fmt.Errorf("market %q does not support showcase", market)
	}
	page, err := showcase.Showcase(ctx, kind, query)
	if err == nil {
		s.applyInstalled(page.Items)
	}
	return page, err
}

func (s *Service) FileContent(ctx context.Context, market Market, id, version, path string) (string, error) {
	client, err := s.client(market)
	if err != nil {
		return "", err
	}
	reader, ok := client.(FileContentClient)
	if !ok {
		return "", fmt.Errorf("market %q does not support file content", market)
	}
	return reader.FileContent(ctx, SkillID{Market: market, ID: id}, version, path)
}

func (s *Service) InstallSkillSet(ctx context.Context, requests []InstallRequest) ([]InstallResult, error) {
	results := make([]InstallResult, 0, len(requests))
	installed := make([]InstallResult, 0, len(requests))
	for _, request := range requests {
		result, err := s.Install(ctx, request)
		if err != nil {
			for i := len(installed) - 1; i >= 0; i-- {
				_ = s.Uninstall(installed[i].Market, installed[i].Name, installed[i].Scope)
			}
			return results, err
		}
		results = append(results, result)
		if !result.AlreadyInstalled {
			installed = append(installed, result)
		}
	}
	return results, nil
}
func (s *Service) client(market Market) (MarketClient, error) {
	client := s.clients[market]
	if client == nil {
		return nil, fmt.Errorf("unsupported skill market %q", market)
	}
	return client, nil
}
func (s *Service) state(market Market, id string) *InstalledState {
	index, err := NewLocalIndex(s.globalDir, s.projectDirs)
	if err != nil {
		return nil
	}
	return index.State(market, id)
}
func (s *Service) applyInstalled(items []SkillSummary) {
	index, err := NewLocalIndex(s.globalDir, s.projectDirs)
	if err == nil {
		index.Apply(items)
	}
}
