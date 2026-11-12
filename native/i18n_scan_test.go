package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The English shell must not only translate the static navigation; every
// reachable settings page uses capability-gated panels whose labels, dynamic
// status text, placeholders, and empty states must also stay English. These
// scans render the real view tree with a controlled, offline ACP state so a
// regression fails deterministically instead of relying on screenshots.
func assertNoChineseUI(t *testing.T, scope string, texts []string) {
	t.Helper()
	han := regexp.MustCompile(`\p{Han}`)
	var leaked []string
	for _, text := range texts {
		if han.MatchString(text) {
			leaked = append(leaked, text)
		}
	}
	if len(leaked) > 0 {
		t.Fatalf("%s leaked Chinese UI strings: %s", scope, strings.Join(leaked, " | "))
	}
}

func TestEnglishSettingsHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.themeSet = true
	a.settingsOpen = true
	a.setFeatures(map[string]bool{
		"manageApplicationSettings": true,
		"manageProviders":           true,
		"manageKnowledgeBases":      true,
		"manageSkills":              true,
		"manageSkillHub":            true,
		"manageSkillHubCatalog":     true,
		"manageExperts":             true,
		"manageACPProfiles":         true,
		"manageEnv":                 true,
		"manageChannels":            true,
		"manageMcp":                 true,
		"manageMemory":              true,
		"manageCron":                true,
		"manageServeConfig":         true,
		"manageStats":               true,
	})
	for _, cat := range settingsCategories() {
		a.settingsCategory = cat.ID
		for _, sec := range cat.Sections {
			a.settingsTab = sec.ID
			a.ensureSettingsTabVisible()
			tt := ui.NewTester(a.view, 1180, 900)
			assertNoChineseUI(t, "settings "+cat.ID+"/"+sec.ID, tt.Texts())
		}
	}
}

func TestEnglishMainPagesHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.themeSet = true
	a.setFeatures(map[string]bool{
		"manageSkills":          true,
		"manageSkillHub":        true,
		"manageSkillHubCatalog": true,
		"manageCron":            true,
		"manageKnowledgeBases":  true,
	})
	a.currentSession = "s1"
	a.skillhub.ConfigLoaded = true
	a.skillhub.CatalogLoaded = true
	a.skillhub.SessionID = "s1"
	a.skillhub.SelectedMarket = "skillhub.cn"
	a.skillhub.MarketInfos = []skillHubMarketInfo{{ID: "skillhub.cn", Name: "SkillHub"}}
	a.skillhub.ResultsLoaded = true
	a.skillhub.Results = []skillHubSkill{{Market: "skillhub.cn", ID: "review", Name: "review", Description: "Review changes"}}
	a.cron.Loaded = true
	a.knowledge.Loaded = true
	a.knowledge.Views = []knowledgeBaseView{{
		KnowledgeBase: knowledgeBase{ID: "kb1", Name: "Docs", Enabled: true},
		Snapshot:      &knowledgeSnapshot{ID: "snap1", Status: "ready", FileCount: 2},
	}}
	for _, page := range []string{"", "skills", "automation", "library"} {
		a.page = page
		tt := ui.NewTester(a.view, 1180, 900)
		assertNoChineseUI(t, "page "+page, tt.Texts())
	}
}

// Error and dialog surfaces are rendered lazily and were previously excluded
// from the page-level English scan. This exercises the real error cards with
// dynamic prefix+detail messages so a missing prefix mapping or a raw dialog
// error fails deterministically.
func TestEnglishSettingsErrorStatesHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.themeSet = true
	a.settingsOpen = true
	a.setFeatures(map[string]bool{
		"manageApplicationSettings": true,
		"manageProviders":           true,
		"manageKnowledgeBases":      true,
		"manageSkills":              true,
		"manageSkillHub":            true,
		"manageSkillHubCatalog":     true,
		"manageExperts":             true,
		"manageACPProfiles":         true,
		"manageEnv":                 true,
		"manageChannels":            true,
		"manageMcp":                 true,
		"manageMemory":              true,
		"manageCron":                true,
		"manageServeConfig":         true,
		"manageStats":               true,
		"manageDeliveries":          true,
	})
	a.settingsErr = "读取应用设置失败：boom"
	a.settingsStatus = "保存失败：尚未连接到 MothX ACP。"
	a.skills.Loaded = true
	a.skills.Err = "读取技能列表失败：boom"
	a.mcp.Loaded = true
	a.mcp.Err = "读取 MCP 服务器配置失败：boom"
	a.mcp.EditorOpen = true
	a.mcp.DialogErr = "保存 MCP 服务器失败：boom"
	a.cron.Loaded = true
	a.cron.Err = "读取定时任务失败：boom"
	a.cron.EditorOpen = true
	a.cron.DialogErr = "保存定时任务失败：boom"
	a.env.Loaded = true
	a.env.Err = "读取环境变量失败：boom"
	a.memory.Loaded = true
	a.memory.Err = "读取记忆失败：boom"
	a.stats.Loaded = true
	a.stats.Err = "读取使用统计失败：boom"
	a.knowledge.Loaded = true
	a.knowledge.Err = "读取知识库列表失败：boom"
	a.channels.Loaded = true
	a.channels.Err = "读取消息通道失败：boom"
	a.acpWorkers.Loaded = true
	a.acpWorkers.Err = "读取 ACP 工作器配置失败：boom"
	a.experts.Loaded = true
	a.experts.Err = "读取团队失败：boom"
	a.providers.Loaded = true
	a.providers.Err = "读取供应商目录失败：boom"
	a.serve.Loaded = true
	a.serve.Err = "读取 Serve 配置失败：boom"
	a.skillhub.ConfigLoaded = true
	a.skillhub.ConfigErr = "读取 SkillHub 配置失败：boom"
	a.skillhub.CatalogLoaded = true
	a.skillhub.CatalogErr = "读取市场列表失败：boom"

	for _, cat := range settingsCategories() {
		a.settingsCategory = cat.ID
		for _, sec := range cat.Sections {
			a.settingsTab = sec.ID
			a.ensureSettingsTabVisible()
			tt := ui.NewTester(a.view, 1180, 900)
			assertNoChineseUI(t, "settings error "+cat.ID+"/"+sec.ID, tt.Texts())
		}
	}
}

func TestEnglishHistoryErrorAndDialogsHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := sidebarTestApp(t)
	setLocale("en")
	a.historyOpen = true
	a.historyPanel.Loaded = true
	a.historyPanel.Err = "读取会话分页失败：boom"
	a.historyPanel.Status = "加载更早消息失败：boom"
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "history error", tt.Texts())

	a.historyPanel.DialogOpen = true
	a.historyPanel.DialogKind = historyDialogRename
	a.historyPanel.DialogSession = "s1"
	a.historyPanel.DialogTitle = "Open Task"
	a.historyPanel.DialogValue = "Open Task"
	a.historyPanel.DialogErr = "重命名失败：boom"
	tt = ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "history rename dialog error", tt.Texts())

	a.historyPanel.ProjectDialogOpen = true
	a.historyPanel.ProjectDialogKind = projectDialogCreate
	a.historyPanel.ProjectDialogName = "Alpha"
	a.historyPanel.ProjectDialogErr = "项目名称不能为空。"
	tt = ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "history project dialog error", tt.Texts())
}
