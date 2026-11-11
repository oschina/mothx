package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"

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
	a.invalidateMemoryPanel()
	a.invalidateCronPanel()
	a.invalidateChannelsPanel()
	a.invalidateServePanel()
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
			ui.Text(c, "返回对话").FontSize(12).TextColor(p.t.Text)
		})
		btn.Label("返回对话").Tooltip("返回当前对话")
		if btn.Clicked() {
			a.closeSettings()
		}
		ui.Icon(c, iconSettings).FontSize(16).TextColor(p.t.Accent)
		ui.Text(c, "应用设置").FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
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
	ui.Text(c, title).FontSize(10).FontWeight(700).LetterSpacing(0.5).
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
		ui.Text(c, label).FontSize(11.5).TextColor(textColor).SingleLine().Grow(1)
	})
	b.Label(label).Tooltip(label)
	if b.Clicked() {
		action()
	}
}

func (a *nativeApp) settingsDetailPane(c *ui.Context, p palette) {
	sec := a.activeSettingsSection()
	ui.Column(c).Grow(1).FillHeight().Background(p.t.Background).Children(func() {
		ui.Column(c).Padding(16, 20, 14, 20).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, sec.Label).FontSize(17).FontWeight(650).TextColor(p.t.Text)
				ui.Text(c, sec.Description).FontSize(11.5).TextColor(p.t.TextMuted).LineHeight(1.4)
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
			ui.Text(c, fmt.Sprintf("%s 面板将在后续迁移中实现。当前版本仅保持导航架构完整，不会调用该服务。", sec.Label)).
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
	})
}

func (a *nativeApp) settingsAppearancePane(c *ui.Context, p palette) {
	a.settingsCard(c, p, "主题", "选择 Native 界面的配色方案。", func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				if a.dark {
					ui.Icon(c, iconMoon).FontSize(16).TextColor(p.t.Accent)
				} else {
					ui.Icon(c, iconSun).FontSize(16).TextColor(p.t.Accent)
				}
				ui.Text(c, map[bool]string{true: "深色", false: "浅色"}[a.dark]).FontSize(12).TextColor(p.t.Text).Grow(1)
			})
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				a.appearanceThemeButton(c, p, "appearance-dark", "深色", true)
				a.appearanceThemeButton(c, p, "appearance-light", "浅色", false)
			})
		})
	})
}

func (a *nativeApp) appearanceThemeButton(c *ui.Context, p palette, key, label string, dark bool) {
	b := ui.ButtonBase(c.Key(key)).Height(32).Padding(0, 14).Radius(8).Gap(6).
		AlignItems(ui.Center).Justify(ui.Center).Border(1, p.t.Border)
	if a.dark == dark {
		b.Background(p.active)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(p.t.Text)
	})
	b.Label(label).Tooltip(label)
	if b.Clicked() {
		a.dark = dark
	}
}

func (a *nativeApp) settingsRuntimePane(c *ui.Context, p palette) {
	bin, origin := resolveMothxBinarySource()
	a.settingsCard(c, p, "当前解析的运行时", "这是启动 Native 时使用的 mothx 可执行文件；它由启动环境决定，不能在此持久化。", func() {
		ui.Column(c).Gap(10).Children(func() {
			a.settingsRuntimeRow(c, p, "解析来源", origin)
			a.settingsRuntimeRow(c, p, "可执行文件", bin)
			if pathBin, err := exec.LookPath("mothx"); err == nil {
				a.settingsRuntimeRow(c, p, "PATH 中的 mothx", pathBin)
			}
			if envBin := os.Getenv("MOTHX_BIN"); envBin != "" {
				a.settingsRuntimeRow(c, p, "MOTHX_BIN", envBin)
			}
			ui.Text(c, "该选择在启动时确定；重启 Native 并设置 MOTHX_BIN 或在 PATH 中提供 mothx 才能改变它。").
				FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
		})
	})
}

func (a *nativeApp) settingsRuntimeRow(c *ui.Context, p palette, label, value string) {
	ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, label).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		if value != "" {
			ui.Text(c, value).FontSize(11.5).TextColor(p.t.Text).Selectable()
		} else {
			ui.Text(c, "未解析").FontSize(11.5).TextColor(p.t.TextMuted)
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
		})
	})
}
