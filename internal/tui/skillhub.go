package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/skillhub"
	"github.com/oschina/mothx/internal/skills"
	"github.com/oschina/mothx/internal/tools"
	"github.com/oschina/mothx/internal/tui/renderutil"
)

type skillHubView int

const (
	skillHubBrowse skillHubView = iota
	skillHubSearch
	skillHubOfficial
)

type skillHubLoadedMsg struct {
	page   skillhub.SearchPage
	market skillhub.Market
	view   skillHubView
	query  string
	pageNo int
	cursor string
	err    error
}

type skillHubDetailLoadedMsg struct {
	detail skillhub.SkillDetail
	id     string
	err    error
}

type skillHubCategoriesLoadedMsg struct {
	market     skillhub.Market
	categories []skillhub.Category
	err        error
}

type skillHubInstalledMsg struct {
	result   skillhub.InstallResult
	id       string
	activate bool
	update   bool
	err      error
}

func (a *App) openSkillHub() tea.Cmd {
	if a.isThinking {
		a.addCommandError("Cannot open /skillhub while the agent is running.")
		return nil
	}
	if a.skillHub == nil {
		globalDir := ""
		officialHandles := a.skillHubOfficialHandles()
		if a.settings != nil {
			globalDir = a.settings.GetGlobalSkillsDir()
		}
		a.skillHub = skillhub.NewServiceForWorkDir(globalDir, a.currentCwd(), officialHandles, skillhub.ClientsForSettings(func() config.SkillHubSettings {
			if a.settings == nil {
				return config.SkillHubSettings{}
			}
			return a.settings.SkillHub
		}())...)
	}
	a.skillHubOpen = true
	a.skillHubMarket = a.defaultSkillHubMarket()
	a.skillHubView = skillHubBrowse
	if a.skillHubMarket == skillhub.MarketSkillHub && len(a.skillHubOfficialHandles()) > 0 {
		a.skillHubView = skillHubOfficial
	}
	a.skillHubScope = a.defaultSkillHubScope()
	a.skillHubQuery = ""
	a.skillHubCategory = ""
	a.skillHubSort = "downloads"
	a.skillHubCategories = nil
	a.skillHubSearchFocused = false
	a.skillHubResults = nil
	a.skillHubSelected = 0
	a.resetSkillHubPaging()
	a.skillHubTotal = 0
	a.skillHubDetail = nil
	a.skillHubDetailExpanded = false
	a.skillHubDetailScroll = 0
	a.skillHubMessage = ""
	return tea.Batch(a.loadSkillHub(), a.loadSkillHubCategories())
}

func (a *App) handleSkillHubCommand(parts []string) tea.Cmd {
	if len(parts) == 1 {
		return a.openSkillHub()
	}
	if a.isThinking {
		a.addCommandError("Cannot use /skillhub while the agent is running.")
		return nil
	}
	switch parts[1] {
	case "search":
		if len(parts) < 3 {
			a.addCommandError(commandUsage(a.translator, "/skillhub search <query>"))
			return nil
		}
		_ = a.openSkillHub()
		a.skillHubView = skillHubSearch
		a.skillHubQuery = strings.Join(parts[2:], " ")
		return a.loadSkillHub()
	case "skillset":
		if len(parts) < 3 {
			a.addCommandError(commandUsage(a.translator, "/skillhub skillset <market>/<id>... [--global|--project|--activate]"))
			return nil
		}
		if a.skillHub == nil {
			globalDir := ""
			if a.settings != nil {
				globalDir = a.settings.GetGlobalSkillsDir()
			}
			a.skillHub = skillhub.NewServiceForWorkDir(globalDir, a.currentCwd(), a.skillHubOfficialHandles(), skillhub.ClientsForSettings(func() config.SkillHubSettings {
				if a.settings == nil {
					return config.SkillHubSettings{}
				}
				return a.settings.SkillHub
			}())...)
		}
		scope, activate := a.defaultSkillHubScope(), false
		requests := make([]skillhub.InstallRequest, 0, len(parts)-2)
		for _, value := range parts[2:] {
			if value == "--global" {
				scope = "global"
				continue
			}
			if value == "--project" {
				scope = "project"
				continue
			}
			if value == "--activate" {
				activate = true
				continue
			}
			market, id, err := parseSkillHubID(value)
			if err != nil {
				a.addCommandError(err.Error())
				return nil
			}
			requests = append(requests, skillhub.InstallRequest{Market: market, ID: id, Scope: scope})
		}
		results, err := a.skillHub.InstallSkillSet(context.Background(), requests)
		if err != nil {
			a.addCommandError("SkillSet failed: " + err.Error())
			return nil
		}
		if a.skillsMgr != nil {
			if err := a.skillsMgr.Load(); err != nil {
				a.addCommandError("SkillSet installed, but skills reload failed: " + err.Error())
				return nil
			}
			for _, result := range results {
				if activate {
					a.activateSkill(result.Name)
				}
			}
		} else if activate {
			a.addCommandError("SkillSet installed, but no skills manager is available for activation")
			return nil
		}
		a.addCommandStatus(fmt.Sprintf("Installed %d skills%s.", len(results), func() string {
			if activate {
				return " and activated them in the current session"
			}
			return ""
		}()))
		return nil
	case "detail", "install", "uninstall":
		if len(parts) < 3 {
			a.addCommandError(commandUsage(a.translator, "/skillhub "+parts[1]+" <market>/<id>"))
			return nil
		}
		market, id, err := parseSkillHubID(parts[2])
		if err != nil {
			a.addCommandError(err.Error())
			return nil
		}
		if a.skillHub == nil {
			globalDir := ""
			officialHandles := a.skillHubOfficialHandles()
			if a.settings != nil {
				globalDir = a.settings.GetGlobalSkillsDir()
			}
			a.skillHub = skillhub.NewServiceForWorkDir(globalDir, a.currentCwd(), officialHandles, skillhub.ClientsForSettings(func() config.SkillHubSettings {
				if a.settings == nil {
					return config.SkillHubSettings{}
				}
				return a.settings.SkillHub
			}())...)
		}
		a.skillHubOpen = true
		a.skillHubMarket, a.skillHubScope = market, a.defaultSkillHubScope()
		a.skillHubResults = []skillhub.SkillSummary{{Market: market, ID: id, Slug: filepathBase(id), Name: filepathBase(id)}}
		a.skillHubSelected = 0
		a.resetSkillHubPaging()
		if parts[1] == "detail" {
			return a.loadSkillHubDetail()
		}
		activate := false
		for _, option := range parts[3:] {
			switch option {
			case "--global":
				a.skillHubScope = "global"
			case "--project":
				a.skillHubScope = "project"
			case "--activate":
				activate = true
			default:
				a.addCommandError(commandUsage(a.translator, "/skillhub install <market>/<id> [--global|--project] [--activate]"))
				return nil
			}
		}
		return a.installSelectedSkillHub(activate)
	case "installed":
		globalDir := ""
		if a.settings != nil {
			globalDir = a.settings.GetGlobalSkillsDir()
		}
		index, err := skillhub.NewLocalIndex(globalDir, skills.ProjectSkillDirs(a.currentCwd()))
		if err != nil {
			a.addCommandError("Failed to scan installed skills: " + err.Error())
			return nil
		}
		states := index.List()
		if len(states) == 0 {
			a.addCommandStatus("No marketplace skills installed.")
			return nil
		}
		lines := []string{"Installed marketplace skills:"}
		for _, state := range states {
			lines = append(lines, fmt.Sprintf("  %s (%s, %s)", state.Dir, state.Scope, state.Version))
		}
		a.addCommandStatus(strings.Join(lines, "\n"))
		return nil
	default:
		a.addCommandError(commandUsage(a.translator, "/skillhub [search <query>|detail <market>/<id>|install <market>/<id> [--global|--project] [--activate]|uninstall <market>/<id>|skillset <market>/<id>... [--global|--project|--activate]|installed]"))
		return nil
	}
}

func parseSkillHubID(value string) (skillhub.Market, string, error) {
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", "", fmt.Errorf("market skill must use skillhub.cn/<slug> or clawhub.ai/<id>")
	}
	market := skillhub.Market(parts[0])
	if market != skillhub.MarketSkillHub && market != skillhub.MarketClawHub {
		return "", "", fmt.Errorf("unsupported marketplace %q", parts[0])
	}
	return market, parts[1], nil
}

func filepathBase(id string) string {
	parts := strings.Split(id, "/")
	return parts[len(parts)-1]
}

func (a *App) defaultSkillHubMarket() skillhub.Market {
	if a.settings != nil && skillhub.Market(a.settings.SkillHub.DefaultMarket) == skillhub.MarketClawHub {
		return skillhub.MarketClawHub
	}
	return skillhub.MarketSkillHub
}

func (a *App) defaultSkillHubScope() string {
	if a.settings != nil && a.settings.SkillHub.DefaultInstallScope == "global" {
		return "global"
	}
	return "project"
}

func (a *App) skillHubOfficialHandles() []string {
	if a.settings != nil && len(a.settings.SkillHub.OfficialHandles) > 0 {
		return a.settings.SkillHub.OfficialHandles
	}
	return []string{config.DefaultSkillHubOfficialHandle}
}

func (a *App) closeSkillHub() {
	a.skillHubOpen = false
	a.skillHubResults = nil
	a.skillHubDetail = nil
	a.skillHubLoading = false
	a.skillHubInstalling = false
	a.skillHubSearchFocused = false
	a.skillHubDetailExpanded = false
	a.skillHubDetailScroll = 0
	a.resetSkillHubPaging()
}

func (a *App) loadSkillHubCategories() tea.Cmd {
	if a.skillHub == nil || a.skillHubMarket != skillhub.MarketSkillHub {
		return nil
	}
	service, market := a.skillHub, a.skillHubMarket
	return func() tea.Msg {
		categories, err := service.Categories(context.Background(), market)
		return skillHubCategoriesLoadedMsg{market: market, categories: categories, err: err}
	}
}

func (a *App) resetSkillHubPaging() {
	a.skillHubPage = 1
	a.skillHubCursor = ""
	a.skillHubNextCursor = ""
	a.skillHubCursorHistory = nil
}

func (a *App) loadSkillHub() tea.Cmd {
	if a.skillHub == nil {
		return nil
	}
	a.skillHubLoading = true
	a.skillHubInstalling = false
	a.skillHubDetail = nil
	a.skillHubMessage = ""
	market, view, query, pageNo, cursor := a.skillHubMarket, a.skillHubView, a.skillHubQuery, a.skillHubPage, a.skillHubCursor
	category, sortBy := a.skillHubCategory, a.skillHubSort
	if pageNo < 1 {
		pageNo = 1
	}
	pageSize := a.skillHubPageSize()
	service := a.skillHub
	return func() tea.Msg {
		var page skillhub.SearchPage
		var err error
		switch view {
		case skillHubOfficial:
			if market != skillhub.MarketSkillHub {
				err = fmt.Errorf("official recommendations are available on SkillHub.cn only")
			} else {
				page, err = service.Official(context.Background(), skillhub.UserSkillsQuery{Query: query, Limit: pageSize, Page: pageNo})
			}
		case skillHubSearch:
			request := skillhub.SearchQuery{Query: query, Limit: pageSize}
			if market == skillhub.MarketClawHub {
				request.Cursor = cursor
			}
			page, err = service.Search(context.Background(), market, request)
		default:
			request := skillhub.SearchQuery{Limit: pageSize}
			if market == skillhub.MarketSkillHub {
				request.Page, request.Sort, request.Order, request.Category = pageNo, sortBy, "desc", category
			} else {
				request.Cursor = cursor
			}
			page, err = service.Search(context.Background(), market, request)
		}
		return skillHubLoadedMsg{page: page, market: market, view: view, query: query, pageNo: pageNo, cursor: cursor, err: err}
	}
}

func (a *App) loadSkillHubDetail() tea.Cmd {
	item, ok := a.selectedSkillHubItem()
	if !ok || a.skillHub == nil {
		return nil
	}
	a.skillHubLoading = true
	service := a.skillHub
	return func() tea.Msg {
		detail, err := service.Detail(context.Background(), item.Market, item.ID)
		return skillHubDetailLoadedMsg{detail: detail, id: item.ID, err: err}
	}
}

func (a *App) installSelectedSkillHub(activate bool) tea.Cmd {
	item, ok := a.selectedSkillHubItem()
	if !ok || a.skillHub == nil {
		a.skillHubMessage = "Select a skill first."
		return nil
	}
	a.skillHubInstalling = true
	a.skillHubMessage = ""
	service, scope := a.skillHub, a.skillHubScope
	return func() tea.Msg {
		result, err := service.Install(context.Background(), skillhub.InstallRequest{Market: item.Market, ID: item.ID, Version: item.Version, Scope: scope})
		return skillHubInstalledMsg{result: result, id: item.ID, activate: activate, err: err}
	}
}

func (a *App) updateSelectedSkillHub() tea.Cmd {
	item, ok := a.selectedSkillHubItem()
	if !ok || a.skillHub == nil {
		a.skillHubMessage = "Select a skill first."
		return nil
	}
	if item.Installed == nil || !item.Installed.Installed {
		a.skillHubMessage = "Install the skill before updating it."
		return nil
	}
	if !item.Installed.UpdateAvailable {
		a.skillHubMessage = "No update is available."
		return nil
	}
	if item.Installed.Dir == "" {
		a.skillHubMessage = "Installed skill directory is unavailable."
		return nil
	}
	a.skillHubInstalling = true
	a.skillHubMessage = ""
	service := a.skillHub
	installed := *item.Installed
	activate := installed.Active
	return func() tea.Msg {
		result, err := service.Install(context.Background(), skillhub.InstallRequest{
			Market:    item.Market,
			ID:        item.ID,
			Version:   item.Version,
			Scope:     installed.Scope,
			TargetDir: filepath.Dir(installed.Dir),
			Overwrite: true,
		})
		return skillHubInstalledMsg{result: result, id: item.ID, activate: activate, update: true, err: err}
	}
}

func (a *App) handleSkillHubLoaded(msg skillHubLoadedMsg) {
	if !a.skillHubOpen || msg.market != a.skillHubMarket || msg.view != a.skillHubView || msg.query != a.skillHubQuery || msg.pageNo != a.skillHubPage || msg.cursor != a.skillHubCursor {
		return
	}
	a.skillHubLoading = false
	if msg.err != nil {
		a.skillHubMessage = "Failed to load marketplace: " + msg.err.Error()
		return
	}
	a.skillHubResults = msg.page.Items
	for i := range a.skillHubResults {
		a.markSkillHubActive(&a.skillHubResults[i])
	}
	a.skillHubTotal = msg.page.Total
	a.skillHubNextCursor = msg.page.NextCursor
	a.skillHubSelected = 0
	if len(msg.page.Items) == 0 {
		a.skillHubMessage = "No skills found."
	}
}

func (a *App) handleSkillHubDetailLoaded(msg skillHubDetailLoadedMsg) {
	item, ok := a.selectedSkillHubItem()
	if !ok || item.ID != msg.id {
		return
	}
	a.skillHubLoading = false
	if msg.err != nil {
		a.skillHubMessage = "Failed to load detail: " + msg.err.Error()
		return
	}
	a.skillHubDetail = &msg.detail
	a.markSkillHubActive(&a.skillHubDetail.SkillSummary)
	a.skillHubDetailExpanded = false
	a.skillHubDetailScroll = 0
}

func (a *App) handleSkillHubCategoriesLoaded(msg skillHubCategoriesLoadedMsg) {
	if msg.market != a.skillHubMarket {
		return
	}
	if msg.err != nil {
		a.skillHubMessage = "Failed to load categories: " + msg.err.Error()
		return
	}
	a.skillHubCategories = msg.categories
}

func (a *App) handleSkillHubInstalled(msg skillHubInstalledMsg) {
	a.skillHubInstalling = false
	item, ok := a.selectedSkillHubItem()
	if !ok || item.ID != msg.id {
		return
	}
	if msg.err != nil {
		operation := "Installation"
		if msg.update {
			operation = "Update"
		}
		a.skillHubMessage = operation + " failed: " + msg.err.Error()
		return
	}
	if err := a.reloadSkillHubSkills(); err != nil {
		a.skillHubMessage = "Installed, but failed to refresh local skills: " + err.Error()
		return
	}
	if msg.activate {
		delete(a.activeSkills, msg.result.Name)
		a.activateSkill(msg.result.Name)
		if msg.update {
			a.skillHubMessage = "Updated and reactivated " + msg.result.Name + "."
		} else {
			a.skillHubMessage = "Installed and activated " + msg.result.Name + "."
		}
	} else if msg.result.AlreadyInstalled {
		a.skillHubMessage = msg.result.Name + " is already installed."
	} else if msg.update {
		a.skillHubMessage = "Updated " + msg.result.Name + "."
	} else {
		a.skillHubMessage = "Installed " + msg.result.Name + "."
	}
	item.Installed = &skillhub.InstalledState{Installed: true, Scope: msg.result.Scope, Dir: msg.result.Dir, Version: msg.result.Version, Active: msg.activate}
	a.skillHubResults[a.skillHubSelected] = item
	if a.skillHubDetail != nil && a.skillHubDetail.ID == item.ID {
		a.skillHubDetail.Version = msg.result.Version
		a.skillHubDetail.Installed = item.Installed
	}
}

func (a *App) markSkillHubActive(item *skillhub.SkillSummary) {
	if item == nil || item.Installed == nil || item.Installed.Dir == "" {
		return
	}
	_, item.Installed.Active = a.activeSkills[filepath.Base(item.Installed.Dir)]
}

func (a *App) reloadSkillHubSkills() error {
	globalDir := ""
	if a.settings != nil {
		globalDir = a.settings.GetGlobalSkillsDir()
	}
	manager := skills.NewManagerWithProjectDirs(globalDir, skills.ProjectSkillDirs(a.currentCwd()))
	if err := manager.Load(); err != nil {
		return err
	}
	a.skillsMgr = manager
	if a.registry != nil {
		a.registry.Register(tools.NewSkillRefTool(manager))
	}
	return nil
}

func (a *App) selectedSkillHubItem() (skillhub.SkillSummary, bool) {
	if a.skillHubSelected < 0 || a.skillHubSelected >= len(a.skillHubResults) {
		return skillhub.SkillSummary{}, false
	}
	return a.skillHubResults[a.skillHubSelected], true
}

func (a *App) handleSkillHubKey(msg tea.KeyMsg) tea.Cmd {
	if !a.skillHubOpen {
		return nil
	}
	if a.skillHubSearchFocused {
		switch msg.Type {
		case tea.KeyEsc:
			a.skillHubSearchFocused = false
			return nil
		case tea.KeyEnter:
			a.skillHubSearchFocused = false
			a.skillHubView = skillHubSearch
			a.resetSkillHubPaging()
			return a.loadSkillHub()
		case tea.KeyBackspace, tea.KeyDelete:
			runes := []rune(a.skillHubQuery)
			if len(runes) > 0 {
				a.skillHubQuery = string(runes[:len(runes)-1])
			}
			return nil
		case tea.KeyRunes:
			a.skillHubQuery += string(msg.Runes)
			return nil
		}
		return nil
	}
	if a.skillHubDetailExpanded {
		switch msg.Type {
		case tea.KeyEsc:
			a.skillHubDetailExpanded = false
			a.skillHubDetailScroll = 0
			return nil
		case tea.KeyUp:
			a.scrollSkillHubDetail(-1)
			return nil
		case tea.KeyDown:
			a.scrollSkillHubDetail(1)
			return nil
		case tea.KeyPgUp:
			a.scrollSkillHubDetail(-a.skillHubPageSize())
			return nil
		case tea.KeyPgDown:
			a.scrollSkillHubDetail(a.skillHubPageSize())
			return nil
		case tea.KeyRunes:
			if string(msg.Runes) == "d" || string(msg.Runes) == "q" {
				a.skillHubDetailExpanded = false
				a.skillHubDetailScroll = 0
			}
			return nil
		}
		return nil
	}
	if a.skillHubLoading || a.skillHubInstalling {
		if msg.Type == tea.KeyEsc {
			a.closeSkillHub()
		}
		return nil
	}

	switch {
	case msg.Type == tea.KeyEsc || (msg.Type == tea.KeyRunes && string(msg.Runes) == "q"):
		a.closeSkillHub()
		return nil
	case msg.Type == tea.KeyTab:
		if a.skillHubMarket == skillhub.MarketSkillHub {
			a.skillHubMarket = skillhub.MarketClawHub
		} else {
			a.skillHubMarket = skillhub.MarketSkillHub
		}
		if a.skillHubMarket == skillhub.MarketClawHub && a.skillHubView == skillHubOfficial {
			a.skillHubView = skillHubBrowse
		}
		a.resetSkillHubPaging()
		return tea.Batch(a.loadSkillHub(), a.loadSkillHubCategories())
	case msg.Type == tea.KeyShiftTab:
		if a.skillHubMarket == skillhub.MarketSkillHub {
			a.skillHubMarket = skillhub.MarketClawHub
		} else {
			a.skillHubMarket = skillhub.MarketSkillHub
		}
		if a.skillHubMarket == skillhub.MarketClawHub && a.skillHubView == skillHubOfficial {
			a.skillHubView = skillHubBrowse
		}
		a.resetSkillHubPaging()
		return tea.Batch(a.loadSkillHub(), a.loadSkillHubCategories())
	case msg.Type == tea.KeyUp:
		if a.skillHubSelected > 0 {
			a.skillHubSelected--
		}
		a.skillHubDetail = nil
		return nil
	case msg.Type == tea.KeyDown:
		if a.skillHubSelected < len(a.skillHubResults)-1 {
			a.skillHubSelected++
		}
		a.skillHubDetail = nil
		return nil
	case msg.Type == tea.KeyEnter:
		return a.loadSkillHubDetail()
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "d":
		if a.skillHubDetail != nil {
			a.skillHubDetailExpanded = true
			a.skillHubDetailScroll = 0
		}
		return nil
	case msg.Type == tea.KeyLeft && a.skillHubCanPage():
		if a.skillHubMarket == skillhub.MarketClawHub {
			if len(a.skillHubCursorHistory) == 0 {
				return nil
			}
			last := len(a.skillHubCursorHistory) - 1
			a.skillHubCursor = a.skillHubCursorHistory[last]
			a.skillHubCursorHistory = a.skillHubCursorHistory[:last]
			if a.skillHubPage > 1 {
				a.skillHubPage--
			}
			return a.loadSkillHub()
		}
		if a.skillHubPage > 1 {
			a.skillHubPage--
			return a.loadSkillHub()
		}
		return nil
	case msg.Type == tea.KeyRight && a.skillHubCanPage():
		if a.skillHubMarket == skillhub.MarketClawHub {
			if a.skillHubNextCursor == "" {
				return nil
			}
			a.skillHubCursorHistory = append(a.skillHubCursorHistory, a.skillHubCursor)
			a.skillHubCursor = a.skillHubNextCursor
			a.skillHubPage++
			return a.loadSkillHub()
		}
		if int64(a.skillHubPage*a.skillHubPageSize()) < a.skillHubTotal {
			a.skillHubPage++
			return a.loadSkillHub()
		}
		return nil
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "/":
		a.skillHubSearchFocused = true
		return nil
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "[":
		if a.skillHubView > skillHubBrowse {
			a.skillHubView--
		}
		a.resetSkillHubPaging()
		return a.loadSkillHub()
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "]":
		if a.skillHubView < skillHubOfficial && a.skillHubMarket == skillhub.MarketSkillHub {
			a.skillHubView++
		} else if a.skillHubView < skillHubSearch {
			a.skillHubView++
		}
		a.resetSkillHubPaging()
		return a.loadSkillHub()
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "g":
		a.skillHubScope = "global"
		return nil
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "p":
		a.skillHubScope = "project"
		return nil
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "r":
		return a.loadSkillHub()
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "c":
		return a.cycleSkillHubCategory()
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "s":
		return a.cycleSkillHubSort()
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "i":
		return a.installSelectedSkillHub(false)
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "a":
		return a.installSelectedSkillHub(true)
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "u":
		return a.updateSelectedSkillHub()
	}
	return nil
}

func (a *App) skillHubPageSize() int {
	height := a.height - lipgloss.Height(a.renderFooter()) - 5
	pageSize := height - 8
	if pageSize < 1 {
		return 1
	}
	if pageSize > 20 {
		return 20
	}
	return pageSize
}

func (a *App) cycleSkillHubCategory() tea.Cmd {
	if a.skillHubMarket != skillhub.MarketSkillHub || a.skillHubView != skillHubBrowse {
		a.skillHubMessage = "Category filtering is available in SkillHub.cn Browse."
		return nil
	}
	if len(a.skillHubCategories) == 0 {
		a.skillHubMessage = "Categories are not available yet."
		return a.loadSkillHubCategories()
	}
	current := 0
	for i, category := range a.skillHubCategories {
		if category.Key == a.skillHubCategory {
			current = i + 1
			break
		}
	}
	next := (current + 1) % (len(a.skillHubCategories) + 1)
	if next == 0 {
		a.skillHubCategory = ""
	} else {
		a.skillHubCategory = a.skillHubCategories[next-1].Key
	}
	a.resetSkillHubPaging()
	return a.loadSkillHub()
}

type skillHubSortOption struct{ key, label string }

var skillHubSortOptions = []skillHubSortOption{
	{key: "downloads", label: "Downloads"},
	{key: "stars", label: "Stars"},
	{key: "installs", label: "Installs"},
	{key: "score", label: "Score"},
	{key: "updated_at", label: "Updated"},
}

func (a *App) cycleSkillHubSort() tea.Cmd {
	if a.skillHubMarket != skillhub.MarketSkillHub || a.skillHubView != skillHubBrowse {
		a.skillHubMessage = "Sorting is available in SkillHub.cn Browse."
		return nil
	}
	current := 0
	for i, option := range skillHubSortOptions {
		if option.key == a.skillHubSort {
			current = i
			break
		}
	}
	a.skillHubSort = skillHubSortOptions[(current+1)%len(skillHubSortOptions)].key
	a.resetSkillHubPaging()
	return a.loadSkillHub()
}

func (a *App) skillHubSortLabel() string {
	for _, option := range skillHubSortOptions {
		if option.key == a.skillHubSort {
			return option.label
		}
	}
	return "Downloads"
}

func (a *App) skillHubCategoryLabel() string {
	if a.skillHubCategory == "" {
		return "All"
	}
	for _, category := range a.skillHubCategories {
		if category.Key == a.skillHubCategory {
			if category.NameEn != "" {
				return category.NameEn
			}
			if category.Name != "" {
				return category.Name
			}
		}
	}
	return a.skillHubCategory
}

func (a *App) skillHubCanPage() bool {
	if a.skillHubMarket == skillhub.MarketClawHub {
		return a.skillHubView == skillHubBrowse || a.skillHubView == skillHubSearch
	}
	return a.skillHubView == skillHubOfficial || a.skillHubView == skillHubBrowse
}

func (a *App) renderSkillHub() string {
	width := a.width - 4
	if width < 30 {
		width = 30
	}
	height := a.height - lipgloss.Height(a.renderFooter()) - 5
	if height < 5 {
		height = 5
	}
	if a.skillHubDetailExpanded {
		return a.renderExpandedSkillHubDetail(width, height)
	}
	marketTabs := "[ SkillHub.cn ]  ClawHub.ai"
	if a.skillHubMarket == skillhub.MarketClawHub {
		marketTabs = "SkillHub.cn  [ ClawHub.ai ]"
	}
	views := "[ Browse ]  Search  Official"
	switch a.skillHubView {
	case skillHubSearch:
		views = "Browse  [ Search ]  Official"
	case skillHubOfficial:
		views = "Browse  Search  [ Official ]"
	}
	if a.skillHubMarket == skillhub.MarketClawHub {
		views = strings.ReplaceAll(views, "  Official", "")
	}
	searchLabel := "Search: " + a.skillHubQuery
	if a.skillHubSearchFocused {
		searchLabel += "_"
	}
	status := fmt.Sprintf("Scope: %s   /:search  Tab:market  []:view  Enter:detail  d:details  i:install  u:update  a:install+activate", a.skillHubScope)
	if a.skillHubCanPage() {
		status += "   Left/Right:page"
		status += fmt.Sprintf("   Page %d", a.skillHubPage)
	}
	filter := ""
	if a.skillHubMarket == skillhub.MarketSkillHub && a.skillHubView == skillHubBrowse {
		filter = "Category: " + a.skillHubCategoryLabel() + " (c)   Sort: " + a.skillHubSortLabel() + " desc (s)"
	}
	lines := []string{"SkillHub", marketTabs, views, searchLabel, xansi.Truncate(filter, width, "..."), xansi.Truncate(status, width, "..."), strings.Repeat("-", width)}
	notice := ""
	if a.skillHubLoading || a.skillHubInstalling {
		notice = "Loading..."
	} else if a.skillHubMessage != "" {
		notice = a.skillHubMessage
	}
	lines = append(lines, notice)
	if len(a.skillHubResults) == 0 && !a.skillHubLoading {
		lines = append(lines, "No results.")
	}
	detailLines := []string{}
	if a.skillHubDetail != nil {
		detail := a.skillHubDetail
		detailLines = append(detailLines, strings.Repeat("-", width), "Detail: "+detail.Name, "Version: "+detail.Version+"  Author: "+detail.Author+"  Downloads: "+formatSkillHubCount(detail.Downloads), xansi.Truncate(detail.Description, width, "..."))
		metadata := make([]string, 0, 2)
		if detail.Source != "" {
			metadata = append(metadata, "Source: "+detail.Source)
		}
		if detail.Category != "" {
			metadata = append(metadata, "Category: "+detail.Category)
		}
		if len(metadata) > 0 {
			detailLines = append(detailLines, strings.Join(metadata, "  "))
		}
		if len(detail.Tags) > 0 {
			detailLines = append(detailLines, "Tags: "+xansi.Truncate(strings.Join(detail.Tags, ", "), max(1, width-6), "..."))
		}
		if detail.PublisherVerified {
			detailLines = append(detailLines, "Certified publisher: "+detail.PublisherName+"  "+detail.CertifiedName)
		}
		detailLines = append(detailLines, skillHubSecuritySummary(detail.SecurityReports)...)
		if dimensions := skillHubEvaluationDimensions(detail.Evaluation); dimensions > 0 {
			detailLines = append(detailLines, fmt.Sprintf("Evaluation: available (%d dimensions)", dimensions))
		}
		if len(detail.DownloadSources) > 0 {
			source := detail.DownloadSources[0]
			download := "Download: " + source.Kind
			if len(detail.DownloadSources) > 1 {
				download += fmt.Sprintf(" (+%d fallback)", len(detail.DownloadSources)-1)
			}
			detailLines = append(detailLines, download)
		}
		for i, file := range detail.Files {
			if i >= 3 {
				detailLines = append(detailLines, fmt.Sprintf("... and %d more files", len(detail.Files)-i))
				break
			}
			detailLines = append(detailLines, "  "+xansi.Truncate(file.Path, max(1, width-2), "..."))
		}
	}
	resultCapacity := height - len(lines) - len(detailLines)
	if resultCapacity < 1 {
		resultCapacity = 1
	}
	start := 0
	if a.skillHubSelected >= resultCapacity {
		start = a.skillHubSelected - resultCapacity + 1
	}
	end := min(len(a.skillHubResults), start+resultCapacity)
	for i := start; i < end; i++ {
		item := a.skillHubResults[i]
		marker := " "
		if i == a.skillHubSelected {
			marker = ">"
		}
		badges := skillHubBadges(item, a.skillHubView == skillHubOfficial)
		badgeText := ""
		if len(badges) > 0 {
			badgeText = " [" + strings.Join(badges, "][") + "]"
		}
		label := fmt.Sprintf("%s %s%s  DL %s  %s  %s", marker, item.Name, badgeText, formatSkillHubCount(item.Downloads), item.Version, item.Author)
		lines = append(lines, xansi.Truncate(label, width, "..."))
	}
	lines = append(lines, detailLines...)
	maxLines := height
	if len(lines) > maxLines {
		lines = append(lines[:maxLines-1], "...")
	}
	return toolModalStyle.Width(width).Height(height + 2).Render(strings.Join(lines, "\n"))
}

func (a *App) renderExpandedSkillHubDetail(width, height int) string {
	lines := a.skillHubExpandedDetailLines(max(1, width-2))
	maxOffset := len(lines) - height + 2
	if maxOffset < 0 {
		maxOffset = 0
	}
	if a.skillHubDetailScroll > maxOffset {
		a.skillHubDetailScroll = maxOffset
	}
	end := min(len(lines), a.skillHubDetailScroll+height-2)
	visible := ""
	if a.skillHubDetailScroll < len(lines) {
		visible = strings.Join(lines[a.skillHubDetailScroll:end], "\n")
	}
	title := fmt.Sprintf("Skill Detail  lines %d-%d/%d  Up/Down/PgUp/PgDn:scroll  d/Esc:back", min(len(lines), a.skillHubDetailScroll+1), end, len(lines))
	content := xansi.Truncate(title, width, "...") + "\n" + strings.Repeat("-", width) + "\n" + visible
	return toolModalStyle.Width(width).Height(height + 2).Render(content)
}

func (a *App) skillHubExpandedDetailLines(width int) []string {
	if a.skillHubDetail == nil {
		return []string{"No detail loaded."}
	}
	detail := a.skillHubDetail
	parts := []string{
		detail.Name,
		"Market: " + string(detail.Market),
		"ID: " + detail.ID,
		"Version: " + detail.Version,
		"Author: " + detail.Author,
		"Downloads: " + formatSkillHubCount(detail.Downloads),
		"Source: " + detail.Source,
		"Category: " + detail.Category,
		"Tags: " + strings.Join(detail.Tags, ", "),
		"",
		"Description:", detail.Description,
		"",
		fmt.Sprintf("Files (%d):", len(detail.Files)),
	}
	for _, file := range detail.Files {
		parts = append(parts, fmt.Sprintf("  %s  %d bytes  %s", file.Path, file.Size, file.SHA256))
	}
	parts = append(parts, "", "Download sources:")
	if len(detail.DownloadSources) == 0 {
		parts = append(parts, "(not available)")
	}
	for _, source := range detail.DownloadSources {
		label := source.Kind
		if source.Fallback {
			label += " fallback"
		}
		parts = append(parts, fmt.Sprintf("  [%s] %s", label, source.URL))
	}
	parts = append(parts, "", "Security reports:", formatSkillHubJSON(detail.SecurityReports), "", "Evaluation:", formatSkillHubJSON(detail.Evaluation))
	return strings.Split(renderutil.WrapANSI(strings.Join(parts, "\n"), width), "\n")
}

func formatSkillHubJSON(value any) string {
	if value == nil {
		return "(not available)"
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func (a *App) scrollSkillHubDetail(delta int) {
	a.skillHubDetailScroll += delta
	if a.skillHubDetailScroll < 0 {
		a.skillHubDetailScroll = 0
	}
}

func skillHubBadges(item skillhub.SkillSummary, officialView bool) []string {
	badges := make([]string, 0, 6)
	if officialView && item.Market == skillhub.MarketSkillHub {
		badges = append(badges, "official")
	}
	if item.PublisherVerified {
		badges = append(badges, "certified")
	}
	if item.Verified {
		badges = append(badges, "verified")
	}
	if item.Source == "enterprise" && !item.PublisherVerified {
		badges = append(badges, "enterprise")
	}
	if item.Suspicious {
		badges = append(badges, "risk")
	}
	if item.Installed != nil && item.Installed.Installed {
		badges = append(badges, "installed")
		if item.Installed.UpdateAvailable {
			badges = append(badges, "update")
		}
	}
	return badges
}

func formatSkillHubCount(value int64) string {
	digits := fmt.Sprintf("%d", value)
	start := 0
	if strings.HasPrefix(digits, "-") {
		start = 1
	}
	for i := len(digits) - 3; i > start; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return digits
}

func skillHubSecuritySummary(reports any) []string {
	values, ok := reports.(map[string]any)
	if !ok || len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		status := "available"
		if report, ok := values[key].(map[string]any); ok {
			if text, ok := report["status"].(string); ok && text != "" {
				status = text
			}
			if text, ok := report["statusText"].(string); ok && text != "" {
				status += " (" + text + ")"
			}
		}
		lines = append(lines, "Security "+key+": "+status)
	}
	return lines
}

func skillHubEvaluationDimensions(evaluation any) int {
	value, ok := evaluation.(map[string]any)
	if !ok {
		return 0
	}
	dimensions, ok := value["dimensions"].(map[string]any)
	if !ok {
		return 0
	}
	return len(dimensions)
}
