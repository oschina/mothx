package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// settingsSection is one leaf of the settings navigation tree. Feature is the
// ACP capability key that must be advertised for the section to be selectable;
// empty Feature means the page is rendered locally without calling ACP.
type settingsSection struct {
	ID          string
	Label       string
	Description string
	Icon        *ui.SVG
	Feature     string
}

// settingsCategory groups related sections in the left-most rail.
type settingsCategory struct {
	ID       string
	Label    string
	Icon     *ui.SVG
	Sections []settingsSection
}

// managedServiceName returns a stable ACP service path for the section, used in
// the capability-aware placeholder so users know which backend owns the panel.
func (s settingsSection) managedServiceName() string {
	switch s.ID {
	case "knowledge":
		return "mothx/manage/knowledge-bases"
	case "acpWorkers":
		return "mothx/manage/acp/profiles"
	default:
		return "mothx/manage/" + s.ID
	}
}

// settingsCategories mirrors the desktop/core/settings-nav.ts information
// architecture while keeping the native palette and typography.
func settingsCategories() []settingsCategory {
	return []settingsCategory{
		{
			ID: "appearance", Label: "外观", Icon: iconSun,
			Sections: []settingsSection{
				{ID: "appearance", Label: "外观", Description: "选择深色或浅色主题", Icon: iconSun},
			},
		},
		{
			ID: "agent", Label: "Agent", Icon: iconCpu,
			Sections: []settingsSection{
				{ID: "application", Label: "应用", Description: "全局应用设置与默认行为", Icon: iconSettings, Feature: "manageApplicationSettings"},
				{ID: "providers", Label: "供应商", Description: "模型供应商与连接配置", Icon: iconCpu, Feature: "manageProviders"},
				{ID: "knowledge", Label: "知识库", Description: "知识库索引与 MCP 接入", Icon: iconBook, Feature: "manageKnowledgeBases"},
				{ID: "skills", Label: "技能", Description: "本地与全局技能开关", Icon: iconSpark, Feature: "manageSkills"},
				{ID: "skillhub", Label: "SkillHub", Description: "技能市场与订阅", Icon: iconShop, Feature: "manageSkillHub"},
				{ID: "experts", Label: "专家团队", Description: "专家与团队绑定", Icon: iconUsers, Feature: "manageExperts"},
				{ID: "acpWorkers", Label: "ACP 工作器", Description: "ACP Worker 配置文件", Icon: iconCpu, Feature: "manageACPProfiles"},
				{ID: "env", Label: "环境变量", Description: "持久化环境变量", Icon: iconTerminal, Feature: "manageEnv"},
			},
		},
		{
			ID: "integrations", Label: "集成", Icon: iconGlobe,
			Sections: []settingsSection{
				{ID: "channels", Label: "消息通道", Description: "微信、飞书等消息通道", Icon: iconGlobe, Feature: "manageChannels"},
				{ID: "mcp", Label: "MCP", Description: "MCP 服务器配置", Icon: iconGlobe, Feature: "manageMcp"},
				{ID: "memory", Label: "记忆", Description: "全局记忆文本", Icon: iconBook, Feature: "manageMemory"},
			},
		},
		{
			ID: "automation", Label: "自动化", Icon: iconClock,
			Sections: []settingsSection{
				{ID: "cron", Label: "定时任务", Description: "Cron 任务与计划执行", Icon: iconClock, Feature: "manageCron"},
			},
		},
		{
			ID: "system", Label: "系统", Icon: iconServer,
			Sections: []settingsSection{
				{ID: "serve", Label: "服务", Description: "mothx serve 监听与接口配置", Icon: iconServer, Feature: "manageServeConfig"},
				{ID: "runtime", Label: "运行时", Description: "当前 Native 可执行文件解析状态", Icon: iconRefresh},
				{ID: "stats", Label: "统计", Description: "使用统计与指标", Icon: iconList, Feature: "manageStats"},
				{ID: "about", Label: "关于", Description: "版本与 ACP 连接信息", Icon: iconDoc},
			},
		},
	}
}

func (a *nativeApp) settingsCategoryByID(id string) (settingsCategory, bool) {
	for _, cat := range settingsCategories() {
		if cat.ID == id {
			return cat, true
		}
	}
	return settingsCategory{}, false
}

func (a *nativeApp) settingsSectionByID(id string) (settingsSection, bool) {
	for _, cat := range settingsCategories() {
		for _, sec := range cat.Sections {
			if sec.ID == id {
				return sec, true
			}
		}
	}
	return settingsSection{}, false
}

// sectionEnabled reports whether a section is currently selectable. Local pages
// are always enabled; managed tabs require their advertised ACP feature.
// SkillHub spans two feature keys: configuration is manageSkillHub, while the
// session-bound market catalogue is manageSkillHubCatalog. Either projection
// makes the section useful, so the panel renders the available half without
// pretending the other capability exists.
func (a *nativeApp) sectionEnabled(sec settingsSection) bool {
	if sec.ID == "skillhub" {
		return a.featureEnabled("manageSkillHub") || a.featureEnabled("manageSkillHubCatalog")
	}
	if sec.Feature == "" {
		return true
	}
	return a.featureEnabled(sec.Feature)
}

func (a *nativeApp) enabledSections(cat settingsCategory) []settingsSection {
	out := make([]settingsSection, 0, len(cat.Sections))
	for _, sec := range cat.Sections {
		if a.sectionEnabled(sec) {
			out = append(out, sec)
		}
	}
	return out
}

// ensureSettingsTabVisible falls back to the first visible section of the
// current category when the selected tab is hidden by capability gating. It
// never issues an ACP call.
func (a *nativeApp) ensureSettingsTabVisible() {
	cat, ok := a.settingsCategoryByID(a.settingsCategory)
	if !ok {
		cat = settingsCategories()[0]
		a.settingsCategory = cat.ID
	}
	enabled := a.enabledSections(cat)
	if len(enabled) == 0 {
		// Appearance is always visible; use it as a safe fallback.
		cat = settingsCategories()[0]
		a.settingsCategory = cat.ID
		enabled = cat.Sections
	}
	for _, sec := range enabled {
		if sec.ID == a.settingsTab {
			return
		}
	}
	previousTab := a.settingsTab
	a.settingsTab = enabled[0].ID
	if previousTab == "providers" && a.settingsTab != "providers" {
		a.invalidateProviderPanel()
	}
	if previousTab == "knowledge" && a.settingsTab != "knowledge" {
		a.invalidateKnowledgePanel()
	}
	if previousTab == "mcp" && a.settingsTab != "mcp" {
		a.invalidateMCPPanel()
	}
	if previousTab == "skills" && a.settingsTab != "skills" {
		a.invalidateSkillsPanel()
	}
	if previousTab == "skillhub" && a.settingsTab != "skillhub" {
		a.invalidateSkillHubPanel()
	}
	if previousTab == "experts" && a.settingsTab != "experts" {
		a.invalidateExpertsPanel()
	}
	if previousTab == "acpWorkers" && a.settingsTab != "acpWorkers" {
		a.invalidateACPWorkersPanel()
	}
	if previousTab == "env" && a.settingsTab != "env" {
		a.invalidateEnvPanel()
	}
	if previousTab == "stats" && a.settingsTab != "stats" {
		a.invalidateStatsPanel()
	}
	if previousTab == "memory" && a.settingsTab != "memory" {
		a.invalidateMemoryPanel()
	}
	if previousTab == "cron" && a.settingsTab != "cron" {
		a.invalidateCronPanel()
	}
	if previousTab == "channels" && a.settingsTab != "channels" {
		a.invalidateChannelsPanel()
	}
	if previousTab == "serve" && a.settingsTab != "serve" {
		a.invalidateServePanel()
	}
}

func (a *nativeApp) setSettingsCategory(id string) {
	cat, ok := a.settingsCategoryByID(id)
	if !ok {
		return
	}
	if a.settingsTab == "providers" {
		a.invalidateProviderPanel()
	}
	if a.settingsTab == "knowledge" {
		a.invalidateKnowledgePanel()
	}
	if a.settingsTab == "mcp" {
		a.invalidateMCPPanel()
	}
	if a.settingsTab == "skills" {
		a.invalidateSkillsPanel()
	}
	if a.settingsTab == "skillhub" {
		a.invalidateSkillHubPanel()
	}
	if a.settingsTab == "experts" {
		a.invalidateExpertsPanel()
	}
	if a.settingsTab == "acpWorkers" {
		a.invalidateACPWorkersPanel()
	}
	if a.settingsTab == "env" {
		a.invalidateEnvPanel()
	}
	if a.settingsTab == "stats" {
		a.invalidateStatsPanel()
	}
	if a.settingsTab == "memory" {
		a.invalidateMemoryPanel()
	}
	if a.settingsTab == "cron" {
		a.invalidateCronPanel()
	}
	if a.settingsTab == "channels" {
		a.invalidateChannelsPanel()
	}
	if a.settingsTab == "serve" {
		a.invalidateServePanel()
	}
	a.settingsCategory = cat.ID
	a.settingsTab = ""
	a.ensureSettingsTabVisible()
	a.maybeLoadActiveSettingsTab()
}

func (a *nativeApp) setSettingsTab(id string) {
	sec, ok := a.settingsSectionByID(id)
	if !ok || !a.sectionEnabled(sec) {
		return
	}
	if a.settingsTab == "providers" && id != "providers" {
		a.invalidateProviderPanel()
	}
	if a.settingsTab == "knowledge" && id != "knowledge" {
		a.invalidateKnowledgePanel()
	}
	if a.settingsTab == "mcp" && id != "mcp" {
		a.invalidateMCPPanel()
	}
	if a.settingsTab == "skills" && id != "skills" {
		a.invalidateSkillsPanel()
	}
	if a.settingsTab == "skillhub" && id != "skillhub" {
		a.invalidateSkillHubPanel()
	}
	if a.settingsTab == "experts" && id != "experts" {
		a.invalidateExpertsPanel()
	}
	if a.settingsTab == "acpWorkers" && id != "acpWorkers" {
		a.invalidateACPWorkersPanel()
	}
	if a.settingsTab == "env" && id != "env" {
		a.invalidateEnvPanel()
	}
	if a.settingsTab == "stats" && id != "stats" {
		a.invalidateStatsPanel()
	}
	if a.settingsTab == "memory" && id != "memory" {
		a.invalidateMemoryPanel()
	}
	if a.settingsTab == "cron" && id != "cron" {
		a.invalidateCronPanel()
	}
	if a.settingsTab == "channels" && id != "channels" {
		a.invalidateChannelsPanel()
	}
	if a.settingsTab == "serve" && id != "serve" {
		a.invalidateServePanel()
	}
	for _, cat := range settingsCategories() {
		for _, s := range cat.Sections {
			if s.ID == id {
				a.settingsCategory = cat.ID
				a.settingsTab = id
				a.maybeLoadActiveSettingsTab()
				return
			}
		}
	}
}

func (a *nativeApp) openSettingsTab(tabID string) {
	a.settingsOpen = true
	a.setSettingsTab(tabID)
	a.ensureSettingsTabVisible()
	a.maybeLoadActiveSettingsTab()
}

// panelVisible reports whether a managed panel is currently mounted, either
// through Settings or through the dedicated main view for that feature.
func (a *nativeApp) panelVisible(page, tab string) bool {
	return (a.settingsOpen && a.settingsTab == tab) || (a.page == page)
}

// activeSettingsSection returns the currently selected section after applying
// capability fallback. It is safe to call from the UI thread.
func (a *nativeApp) activeSettingsSection() settingsSection {
	a.ensureSettingsTabVisible()
	sec, _ := a.settingsSectionByID(a.settingsTab)
	return sec
}

// maybeLoadActiveSettingsTab starts loading the active managed tab. It only
// issues ACP calls for sections whose feature is advertised.
func (a *nativeApp) maybeLoadActiveSettingsTab() {
	if !a.settingsOpen {
		return
	}
	sec := a.activeSettingsSection()
	if !a.sectionEnabled(sec) {
		return
	}
	switch sec.ID {
	case "application":
		if a.settingsView != nil || a.settingsLoading {
			return
		}
		seq := atomic.AddInt64(&a.settingsSeq, 1)
		a.settingsLoading = true
		a.settingsErr = ""
		a.settingsForm = newApplicationSettingsForm()
		go a.loadApplicationSettings(seq)
	case "providers":
		if a.providers.Loaded || a.providers.Loading {
			return
		}
		a.startProviderPanelLoad()
	case "knowledge":
		if a.knowledge.Loaded || a.knowledge.Loading {
			return
		}
		a.startKnowledgePanelLoad()
	case "mcp":
		if a.mcp.Loaded || a.mcp.Loading {
			return
		}
		a.startMCPPanelLoad()
	case "skills":
		if a.skills.Loaded || a.skills.Loading {
			return
		}
		a.startSkillsPanelLoad()
	case "skillhub":
		a.startSkillHubPanelLoad()
	case "experts":
		if a.experts.Loaded || a.experts.Loading {
			return
		}
		a.startExpertsPanelLoad()
	case "acpWorkers":
		if a.acpWorkers.Loaded || a.acpWorkers.Loading {
			return
		}
		a.startACPWorkersPanelLoad()
	case "env":
		if a.env.Loaded || a.env.Loading {
			return
		}
		a.startEnvPanelLoad()
	case "stats":
		if a.stats.Loaded || a.stats.Loading {
			return
		}
		a.startStatsPanelLoad()
	case "memory":
		if a.memory.Loaded || a.memory.Loading {
			return
		}
		a.startMemoryPanelLoad()
	case "cron":
		if a.cron.Loaded || a.cron.Loading {
			return
		}
		a.startCronPanelLoad()
	case "channels":
		if a.channels.Loaded || a.channels.Loading {
			return
		}
		a.startChannelsPanelLoad()
	case "serve":
		if a.serve.Loaded || a.serve.Loading {
			return
		}
		a.startServePanelLoad()
	}
}

func (a *nativeApp) openSettings() {
	if a.settingsOpen {
		return
	}
	a.historyOpen = false
	a.history = false
	a.invalidateHistoryPanel()
	a.closeRouterPanel()
	a.closeSubagentPanel()
	a.closeWorktreePanel()
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "application"
	a.settingsErr = ""
	a.settingsStatus = ""
	a.settingsView = nil
	a.settingsSaving = false
	a.settingsLoading = false
	a.ensureSettingsTabVisible()
	a.maybeLoadActiveSettingsTab()
}

func (a *nativeApp) closeSettings() {
	a.settingsOpen = false
	a.settingsLoading = false
	a.settingsSaving = false
	atomic.AddInt64(&a.settingsSeq, 1)
	a.invalidateProviderPanel()
	a.invalidateKnowledgePanel()
	a.invalidateMCPPanel()
	a.invalidateSkillsPanel()
	a.invalidateSkillHubPanel()
	a.invalidateExpertsPanel()
	a.invalidateACPWorkersPanel()
	a.invalidateEnvPanel()
	a.invalidateStatsPanel()
	a.invalidateMemoryPanel()
	a.invalidateCronPanel()
	a.invalidateChannelsPanel()
	a.invalidateServePanel()
	switch a.page {
	case "skills":
		a.startSkillsPanelLoad()
		a.startSkillHubPanelLoad()
	case "automation":
		a.startCronPanelLoad()
	case "library":
		a.startKnowledgePanelLoad()
	}
}

func (a *nativeApp) retryLoadSettings() {
	if !a.settingsOpen || a.settingsLoading {
		return
	}
	sec := a.activeSettingsSection()
	if sec.ID != "application" || !a.sectionEnabled(sec) {
		return
	}
	seq := atomic.AddInt64(&a.settingsSeq, 1)
	a.settingsLoading = true
	a.settingsErr = ""
	go a.loadApplicationSettings(seq)
}

// settingsPage renders the full Settings View inside the native content area.
// The primary chat sidebar stays visible so the user can return to chats from
// there; an explicit back control is also provided in the page header.
func (a *nativeApp) settingsPage(c *ui.Context, p palette, bar ui.TitleBar) {
	right := float32(12 + bar.Right)
	left := float32(18 + bar.Left)
	ui.Column(c).Fill().Background(p.t.Background).Children(func() {
		a.settingsPageHeader(c, p, left, right)
		ui.Row(c).Grow(1).Children(func() {
			a.settingsCategoryRail(c, p)
			a.settingsSectionRail(c, p)
			a.settingsDetailPane(c, p)
		})
	})
}

func (a *nativeApp) settingsPageHeader(c *ui.Context, p palette, left, right float32) {
	ui.Row(c).Height(52).Padding(0, right, 0, left).AlignItems(ui.Center).Gap(10).
		BorderWidth(0, 0, 1, 0).BorderColor(p.t.Border).Background(p.t.Background).Children(func() {
		btn := ui.ButtonBase(c.Key("settings-back")).Height(30).Padding(6, 10).Radius(8).Gap(6).
			AlignItems(ui.Center).Background(p.chip).Border(1, p.t.Border)
		if btn.Hovered() {
			btn.Background(p.hover)
		}
		btn.Children(func() {
			ui.Icon(c, iconArrowLeft).FontSize(14).TextColor(p.t.TextMuted)
			ui.Text(c, T("返回对话")).FontSize(12).TextColor(p.t.Text)
		})
		btn.Label(T("返回对话")).Tooltip(T("返回当前对话"))
		if btn.Clicked() {
			a.closeSettings()
		}
		ui.Icon(c, iconSettings).FontSize(16).TextColor(p.t.Accent)
		ui.Text(c, T("应用设置")).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
	})
}

func (a *nativeApp) settingsCategoryRail(c *ui.Context, p palette) {
	a.ensureSettingsTabVisible()
	currentCat, _ := a.settingsCategoryByID(a.settingsCategory)
	ui.Column(c).Width(130).FillHeight().Background(p.sidebar).
		BorderWidth(0, 1, 0, 0).BorderColor(p.t.Border).Padding(8, 6).Children(func() {
		a.settingsRailHeader(c, p, "分类")
		ui.Column(c).Gap(3).Children(func() {
			for _, entry := range settingsCategories() {
				entry := entry
				a.settingsNavButton(c, p, "settings-cat:"+entry.ID, entry.Label, entry.Icon, entry.ID == currentCat.ID, func() {
					a.setSettingsCategory(entry.ID)
				})
			}
		})
	})
}

func (a *nativeApp) settingsSectionRail(c *ui.Context, p palette) {
	currentCat, _ := a.settingsCategoryByID(a.settingsCategory)
	active := a.activeSettingsSection()
	ui.Column(c).Width(150).FillHeight().Background(p.t.Background).
		BorderWidth(0, 1, 0, 0).BorderColor(p.t.Border).Padding(8, 6).Children(func() {
		a.settingsRailHeader(c, p, "条目")
		ui.Column(c).Gap(3).Children(func() {
			for _, sec := range a.enabledSections(currentCat) {
				sec := sec
				a.settingsNavButton(c, p, "settings-sec:"+sec.ID, sec.Label, sec.Icon, sec.ID == active.ID, func() {
					a.setSettingsTab(sec.ID)
				})
			}
		})
	})
}

func (a *nativeApp) settingsRailHeader(c *ui.Context, p palette, title string) {
	ui.Text(c, T(title)).FontSize(10).FontWeight(700).LetterSpacing(0.5).
		TextColor(p.t.TextMuted).Padding(6, 4, 8, 4)
}

func (a *nativeApp) settingsNavButton(c *ui.Context, p palette, key, label string, icon *ui.SVG, active bool, action func()) {
	b := ui.ButtonBase(c.Key(key)).Height(30).Padding(5, 7).Radius(8).Gap(6).
		AlignItems(ui.Center).Justify(ui.Start)
	if active {
		b.Background(p.active)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		color := p.t.TextMuted
		textColor := p.t.Text
		if active {
			color = p.t.Accent
			textColor = p.t.Accent
		}
		ui.Icon(c, icon).FontSize(15).TextColor(color)
		ui.Text(c, T(label)).FontSize(11.5).TextColor(textColor).SingleLine().Grow(1)
	})
	b.Label(T(label)).Tooltip(T(label))
	if b.Clicked() {
		action()
	}
}

func (a *nativeApp) settingsDetailPane(c *ui.Context, p palette) {
	sec := a.activeSettingsSection()
	ui.Column(c).Grow(1).FillHeight().Background(p.t.Background).Children(func() {
		ui.Column(c).Padding(16, 20, 14, 20).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, T(sec.Label)).FontSize(17).FontWeight(650).TextColor(p.t.Text)
				ui.Text(c, T(sec.Description)).FontSize(11.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			})
		})
		ui.Box(c).Height(1).Background(p.t.Border)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(16, 20, 24, 20).Gap(14).Children(func() {
				switch sec.ID {
				case "application":
					a.settingsApplicationPane(c, p)
				case "providers":
					a.settingsProvidersPane(c, p)
				case "knowledge":
					a.settingsKnowledgePane(c, p)
				case "mcp":
					a.settingsMCPPane(c, p)
				case "skills":
					a.settingsSkillsPane(c, p)
				case "skillhub":
					a.settingsSkillHubPane(c, p)
				case "experts":
					a.settingsExpertsPane(c, p)
				case "acpWorkers":
					a.settingsACPWorkersPane(c, p)
				case "env":
					a.settingsEnvPane(c, p)
				case "stats":
					a.settingsStatsPane(c, p)
				case "memory":
					a.settingsMemoryPane(c, p)
				case "cron":
					a.settingsCronPane(c, p)
				case "channels":
					a.settingsChannelsPane(c, p)
				case "serve":
					a.settingsServePane(c, p)
				case "appearance":
					a.settingsAppearancePane(c, p)
				case "runtime":
					a.settingsRuntimePane(c, p)
				case "about":
					a.settingsAboutPane(c, p)
				default:
					a.settingsManagedPane(c, p, sec)
				}
			})
		})
	})
}

func (a *nativeApp) settingsManagedPane(c *ui.Context, p palette, sec settingsSection) {
	a.settingsCard(c, p, sec.Label, "该设置项由 ACP-backed 服务管理。", func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, sec.Icon).FontSize(18).TextColor(p.t.Accent)
				ui.Text(c, sec.managedServiceName()).FontSize(12).FontWeight(600).TextColor(p.t.Text)
			})
			if sec.Feature != "" {
				a.settingsRuntimeRow(c, p, "已公开的 ACP 能力", sec.Feature)
			}
			ui.Text(c, Tf("%s 面板将在后续迁移中实现。当前版本仅保持导航架构完整，不会调用该服务。", T(sec.Label))).
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
	})
}

func (a *nativeApp) settingsAppearancePane(c *ui.Context, p palette) {
	a.settingsCard(c, p, "语言", "切换 Native 界面语言；选择由 shell 保存，重启后保持。", func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			a.appearanceLanguageButton(c, p, "appearance-lang-zh", "中文", "zh")
			a.appearanceLanguageButton(c, p, "appearance-lang-en", "English", "en")
		})
	})
	a.settingsCard(c, p, "主题", "跟随系统或固定深色/浅色；选择会写入本机 Native 外观偏好，重启后保持。", func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				switch a.themeMode {
				case "light":
					ui.Icon(c, iconSun).FontSize(16).TextColor(p.t.Accent)
				case "dark":
					ui.Icon(c, iconMoon).FontSize(16).TextColor(p.t.Accent)
				default:
					ui.Icon(c, iconSettings).FontSize(16).TextColor(p.t.Accent)
				}
				label := "跟随系统"
				switch a.themeMode {
				case "light":
					label = "浅色"
				case "dark":
					label = "深色"
				}
				ui.Text(c, T(label)).FontSize(12).TextColor(p.t.Text).Grow(1)
			})
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				a.appearanceThemeModeButton(c, p, "appearance-system", "跟随系统", "system")
				a.appearanceThemeModeButton(c, p, "appearance-dark", "深色", "dark")
				a.appearanceThemeModeButton(c, p, "appearance-light", "浅色", "light")
			})
		})
	})
	a.settingsCard(c, p, "首页 Logo", "使用自定义图片替换首页默认 MothX 标志。", func() {
		ui.Column(c).Gap(9).Children(func() {
			ui.Checkbox(c, &a.home.logoVisible, T("显示首页 Logo")).FontSize(11.5)
			if a.home.logoPath != "" {
				a.settingsRuntimeRow(c, p, "Logo 文件", a.home.logoPath)
			} else {
				ui.Text(c, T("当前使用默认 MothX 标志。")).FontSize(10.5).TextColor(p.t.TextMuted)
			}
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				if ui.Button(c.Key("appearance-logo-choose"), T("选择图片…")).Clicked() {
					a.chooseHomeLogo()
				}
				if a.home.logoPath != "" {
					if ui.Button(c.Key("appearance-logo-clear"), T("恢复默认")).Clicked() {
						a.clearHomeLogo()
					}
				}
			})
		})
	})
	a.settingsCard(c, p, "首页背景", "仅作用于新会话首页，不改变转录或侧栏背景。", func() {
		ui.Column(c).Gap(9).Children(func() {
			if a.home.backgroundPath != "" {
				a.settingsRuntimeRow(c, p, "背景文件", a.home.backgroundPath)
			} else {
				ui.Text(c, T("当前没有首页背景图。")).FontSize(10.5).TextColor(p.t.TextMuted)
			}
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				if ui.Button(c.Key("appearance-bg-choose"), T("选择背景…")).Clicked() {
					a.chooseHomeBackground()
				}
				if a.home.backgroundPath != "" {
					if ui.Button(c.Key("appearance-bg-clear"), T("移除背景")).Clicked() {
						a.clearHomeBackground()
					}
				}
			})
			ui.Column(c).Gap(3).Children(func() {
				ui.Textf(c, "%s %d%%", T("不透明度"), int(a.home.backgroundOpacity*100)).
					FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				slider := ui.Slider(c, &a.home.backgroundOpacity, 0, 1).Width(260)
				if slider.Changed() {
					a.saveNativePreferences()
				}
			})
			ui.Column(c).Gap(3).Children(func() {
				ui.Textf(c, "%s %.1f", T("模糊"), a.home.backgroundBlur).
					FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				slider := ui.Slider(c, &a.home.backgroundBlur, 0, 20).Width(260)
				if slider.Changed() {
					a.saveNativePreferences()
				}
			})
			ui.Row(c).AlignItems(ui.Center).Gap(6).Wrap().Children(func() {
				ui.Text(c, T("缩放")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				for _, option := range []struct{ label, value string }{
					{"覆盖", "cover"}, {"完整显示", "contain"}, {"拉伸", "fill"}, {"原始尺寸", "scaledown"},
				} {
					option := option
					a.appearanceChoiceButton(c, p, "bg-fit:"+option.value, option.label, a.home.backgroundFit == option.value, func() {
						a.home.backgroundFit = option.value
						a.saveNativePreferences()
					})
				}
			})
			ui.Row(c).AlignItems(ui.Center).Gap(6).Wrap().Children(func() {
				ui.Text(c, T("位置")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				for _, option := range []struct{ label, value string }{
					{"居中", "center"}, {"顶部", "top"}, {"底部", "bottom"}, {"左侧", "left"}, {"右侧", "right"},
					{"左上", "topleft"}, {"右上", "topright"}, {"左下", "bottomleft"}, {"右下", "bottomright"},
				} {
					option := option
					a.appearanceChoiceButton(c, p, "bg-pos:"+option.value, option.label, a.home.backgroundPosition == option.value, func() {
						a.home.backgroundPosition = option.value
						a.saveNativePreferences()
					})
				}
			})
			if ui.PrimaryButton(c.Key("appearance-save"), T("保存首页外观")).Clicked() {
				a.saveNativePreferences()
				a.showToast("外观偏好已保存")
			}
		})
	})
}

// appearanceChoiceButton is a compact single-select control whose click action
// writes the underlying presentation field immediately.
func (a *nativeApp) appearanceChoiceButton(c *ui.Context, p palette, key, label string, active bool, action func()) {
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 10).Radius(7).
		AlignItems(ui.Center).Justify(ui.Center).Border(1, p.t.Border)
	if active {
		b.Background(p.active).Border(1, p.t.Accent)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		color := p.t.Text
		if active {
			color = p.t.Accent
		}
		ui.Text(c, T(label)).FontSize(10.5).TextColor(color)
	})
	b.Label(T(label))
	if b.Clicked() {
		action()
	}
}

func (a *nativeApp) appearanceLanguageButton(c *ui.Context, p palette, key, label, locale string) {
	b := ui.ButtonBase(c.Key(key)).Height(32).Padding(0, 14).Radius(8).Gap(6).
		AlignItems(ui.Center).Justify(ui.Center).Border(1, p.t.Border)
	if currentLocale() == locale {
		b.Background(p.active)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		color := p.t.Text
		if currentLocale() == locale {
			color = p.t.Accent
		}
		ui.Text(c, T(label)).FontSize(12).TextColor(color)
	})
	b.Label(T(label)).Tooltip(T(label))
	if b.Clicked() && currentLocale() != locale {
		setLocale(locale)
		if locale == "en" {
			mygo.App.SetLocale("en")
		} else {
			mygo.App.SetLocale("zh-Hans")
		}
	}
}

func (a *nativeApp) appearanceThemeModeButton(c *ui.Context, p palette, key, label, mode string) {
	b := ui.ButtonBase(c.Key(key)).Height(32).Padding(0, 14).Radius(8).Gap(6).
		AlignItems(ui.Center).Justify(ui.Center).Border(1, p.t.Border)
	if a.themeMode == mode {
		b.Background(p.active)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		color := p.t.Text
		if a.themeMode == mode {
			color = p.t.Accent
		}
		ui.Text(c, T(label)).FontSize(12).TextColor(color)
	})
	b.Label(T(label)).Tooltip(T(label))
	if b.Clicked() {
		a.themeMode = mode
		a.themeSet = false
		if mode == "dark" {
			a.dark = true
		} else if mode == "light" {
			a.dark = false
		}
		a.saveNativePreferences()
	}
}

func (a *nativeApp) settingsRuntimePane(c *ui.Context, p palette) {
	bin, origin := resolveMothxBinarySource()
	a.settingsCard(c, p, "当前解析的运行时", "Native 可以持久化选择一个本地 mothx 二进制；--mothx-bin 启动参数和 MOTHX_BIN 仍可覆盖它。ACP 与 settings.json 都不拥有这个选择。", func() {
		ui.Column(c).Gap(10).Children(func() {
			a.settingsRuntimeRow(c, p, "解析来源", origin)
			a.settingsRuntimeRow(c, p, "可执行文件", bin)
			if preference := getRuntimeBinaryPreference(); preference != "" {
				a.settingsRuntimeRow(c, p, "持久化选择", preference)
			}
			if pathBin, err := exec.LookPath("mothx"); err == nil {
				a.settingsRuntimeRow(c, p, "PATH 中的 mothx", pathBin)
			}
			if envBin := os.Getenv("MOTHX_BIN"); envBin != "" {
				a.settingsRuntimeRow(c, p, "MOTHX_BIN", envBin)
			}
			if launchBinaryOverride != "" {
				a.settingsRuntimeRow(c, p, "启动参数 --mothx-bin", launchBinaryOverride)
			}
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				if ui.Button(c.Key("runtime-binary-choose"), T("选择 mothx 二进制…")).Clicked() {
					a.chooseRuntimeBinary()
				}
				if getRuntimeBinaryPreference() != "" {
					if ui.Button(c.Key("runtime-binary-clear"), T("清除选择")).Clicked() {
						setRuntimeBinaryPreference("", true)
						addDiagnostic("info", "runtime binary preference cleared")
						a.reconnectACP()
					}
				}
			})
			ui.Text(c, T("优先级：--mothx-bin 启动参数 > Native 持久化选择 > MOTHX_BIN > PATH > 仓库 bin/。更改后会重新连接 ACP。")).
				FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
		})
	})
	a.settingsCard(c, p, "连接", "ACP 子进程状态。重启会关闭当前会话运行时；会话历史仍由 Runtime 持有。", func() {
		ui.Column(c).Gap(8).Children(func() {
			a.settingsRuntimeRow(c, p, "状态", a.connectionLabel())
			if a.connErr != "" {
				ui.Text(c, T(a.connErr)).FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}
			label := "重启连接"
			if a.conn == connFailed || a.conn == connIdle {
				label = "重试连接"
			}
			b := ui.Button(c.Key("runtime-reconnect"), T(label)).Label(T(label))
			b.Disabled(a.conn == connStarting)
			if b.Clicked() && a.conn != connStarting {
				a.reconnectACP()
			}
		})
	})
	a.settingsCard(c, p, "诊断", "通过共享 Runtime 的 mothx/doctor 检查配置、Provider、沙箱与会话数据库。", func() {
		ui.Column(c).Gap(8).Children(func() {
			run := ui.Button(c.Key("runtime-doctor"), T("运行诊断")).Label(T("运行 mothx/doctor 诊断"))
			run.Disabled(!a.doctorCapable() || a.runtime.DoctorLoading)
			if run.Clicked() && a.doctorCapable() && !a.runtime.DoctorLoading {
				a.runDoctor()
			}
			if !a.doctorCapable() {
				ui.Text(c, T("当前运行时未公开 doctor 能力。")).FontSize(10.5).TextColor(p.t.TextMuted)
			}
			if a.runtime.DoctorStatus != "" {
				ui.Text(c, T(a.runtime.DoctorStatus)).FontSize(10.5).TextColor(p.t.TextMuted)
			}
			if a.runtime.DoctorErr != "" {
				ui.Text(c, T(a.runtime.DoctorErr)).FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}
			if a.runtime.Doctor != nil {
				for _, check := range a.runtime.Doctor.Checks {
					check := check
					ui.Row(c).AlignItems(ui.Start).Gap(8).Padding(7, 9).Radius(8).
						Background(p.t.Background).Border(1, p.t.Border).Children(func() {
						color := p.t.TextMuted
						switch strings.ToLower(strings.TrimSpace(check.Status)) {
						case "ok", "pass", "passed":
							color = p.t.Success
						case "warn", "warning":
							color = p.t.Warning
						case "error", "fail", "failed":
							color = p.t.Danger
						}
						ui.Box(c).Size(6, 6).Radius(3).Background(color).Margin(4, 0, 0, 0)
						ui.Column(c).Grow(1).Gap(2).Children(func() {
							title := check.Title
							if title == "" {
								title = check.ID
							}
							if title == "" {
								title = T("检查")
							}
							ui.Textf(c, "%s · %s", title, T(doctorStatusLabel(check.Status))).
								FontSize(11).FontWeight(600).TextColor(p.t.Text)
							if check.Detail != "" {
								ui.Text(c, check.Detail).FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.35)
							}
							if check.Fix != "" {
								ui.Text(c, T("修复：")+check.Fix).FontSize(10.5).TextColor(p.t.Accent).LineHeight(1.35)
							}
						})
					})
				}
			}
		})
	})
	a.settingsCard(c, p, "ACP 日志", "最近捕获的 ACP stderr；仅保存在当前窗口内存中。", func() {
		if len(a.stderr) == 0 {
			ui.Text(c, T("没有捕获到 stderr。")).FontSize(10.5).TextColor(p.t.TextMuted)
			return
		}
		ui.Text(c, strings.Join(a.stderr, "\n")).Font("monospace").FontSize(9.5).
			TextColor(p.t.TextMuted).LineHeight(1.35).Selectable()
	})
	a.settingsCard(c, p, "主进程诊断日志", "Native shell 的连接、请求与错误生命周期；可过滤和清空。", func() {
		a.diagnosticLogView(c, p)
	})
}

func (a *nativeApp) settingsRuntimeRow(c *ui.Context, p palette, label, value string) {
	ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		if value != "" {
			ui.Text(c, T(value)).FontSize(11.5).TextColor(p.t.Text).Selectable()
		} else {
			ui.Text(c, T("未解析")).FontSize(11.5).TextColor(p.t.TextMuted)
		}
	})
}

func (a *nativeApp) settingsAboutPane(c *ui.Context, p palette) {
	featureCount := 0
	for _, enabled := range a.featureSnapshot() {
		if enabled {
			featureCount++
		}
	}
	a.settingsCard(c, p, "关于 MothX Native", fmt.Sprintf("版本 %s", nativeVersion), func() {
		ui.Column(c).Gap(10).Children(func() {
			a.settingsRuntimeRow(c, p, "Native 版本", nativeVersion)
			a.settingsRuntimeRow(c, p, "ACP 状态", a.connectionLabel())
			a.settingsRuntimeRow(c, p, "可用能力数", fmt.Sprintf("%d", featureCount))
			a.settingsRuntimeRow(c, p, "平台", runtime.GOOS+"/"+runtime.GOARCH)
			bin, origin := resolveMothxBinarySource()
			a.settingsRuntimeRow(c, p, "运行时二进制", bin)
			a.settingsRuntimeRow(c, p, "运行时来源", origin)
		})
	})
}

// chooseRuntimeBinary lets the shell persist a local runtime executable. It is
// client launch state only; validation is executable-bit/path existence and the
// actual Runtime decides whether the binary can serve ACP.
func (a *nativeApp) chooseRuntimeBinary() {
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: T("选择 mothx 可执行文件")})
		if err != nil || len(paths) == 0 {
			return
		}
		path := strings.TrimSpace(paths[0])
		info, statErr := os.Stat(path)
		if statErr != nil || info.IsDir() {
			a.dispatch(func() { a.showErrorToast("所选运行时不是可执行文件") })
			return
		}
		a.dispatch(func() {
			setRuntimeBinaryPreference(path, true)
			addDiagnostic("info", "runtime binary preference set: "+path)
			a.showToast("运行时已切换，正在重新连接")
			a.reconnectACP()
		})
	}()
}
