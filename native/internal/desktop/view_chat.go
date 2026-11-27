package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *nativeApp) mainView(c *ui.Context, p palette) {
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		a.header(c, p)
		if len(a.messages) == 0 {
			a.emptyState(c, p)
		} else {
			a.transcriptWithPlan(c, p)
		}
		a.composer(c, p)
		a.statusBar(c, p)
	})
}

func (a *nativeApp) header(c *ui.Context, p palette) {
	ui.Row(c).Height(46).Padding(0, 16, 0, 16).AlignItems(ui.Center).Gap(10).
		BorderWidth(0, 0, 1, 0).BorderColor(p.T.Border).Background(p.T.Background).Children(func() {
		if label := a.currentSessionStatusLabel(); label != "" {
			statusPill(c, p, label)
		}
		ui.Column(c).Gap(1).Children(func() {
			ui.Text(c, a.sessionTitle()).FontSize(12.5).FontWeight(600).SingleLine().MaxWidth(520)
			if a.currentSession != "" {
				ui.Text(c, a.workspaceName()).FontSize(9.5).TextColor(p.T.TextMuted).SingleLine()
			}
		})
		ui.Spacer(c)
		if a.usageLabel() != "" {
			ui.Text(c, a.usageLabel()).FontSize(10).TextColor(p.T.TextMuted).Margin(0, 8, 0, 4)
		}
		if a.worktreeCapable() && a.worktreeBaseDir() != "" {
			iconButton(c, p, iconBranch, "隔离工作树", 15, a.openWorktreePanel)
		}
		if a.routerCapable() && a.currentSession != "" && a.loadingSession == "" {
			iconButton(c, p, iconCpu, "Router 协作", 15, a.openRouterPanel)
		}
		if a.subagentCapable() && a.currentSession != "" && a.loadingSession == "" {
			a.subagentSummaryButton(c, p)
		}
		if a.mcpCapable() && a.currentSession != "" && a.loadingSession == "" {
			iconButton(c, p, iconServer, "项目 MCP", 15, a.openProjectMCPPanel)
		}
		if a.currentSession != "" {
			if session, ok := a.sessionByID(a.currentSession); ok {
				a.sessionMenu(c, p, session, "header")
			}
		}
		iconButton(c, p, iconGlobe, "切换语言", 15, func() { a.toggleLocale() })
		iconButton(c, p, a.appearanceIcon(), "切换外观", 15, func() { a.toggleTheme() })
		if a.sidebar {
			iconButton(c, p, iconPanelLeft, "收起侧栏", 15, func() { a.sidebar = false })
		} else {
			iconButton(c, p, iconPanelLeft, "展开侧栏", 15, func() { a.sidebar = true })
		}
	})
}

func (a *nativeApp) emptyState(c *ui.Context, p palette) {
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		if a.home.backgroundImage != nil && a.home.backgroundOpacity > 0 && !a.appBackgroundActive() {
			opacity := float32(a.home.backgroundOpacity)
			if opacity < 0 {
				opacity = 0
			}
			if opacity > 1 {
				opacity = 1
			}
			x, y := a.homeObjectPosition()
			ui.Image(c, a.home.backgroundImage).Absolute().Left(0).Top(0).Right(0).Bottom(0).
				Fit(a.homeFit()).ObjectPosition(x, y).Opacity(opacity).
				Blur(float32(a.home.backgroundBlur)).ZIndex(-2).
				HitTest(func(x, y float32, r ui.Rect) bool { return false })
			// The home veil keeps preset/logo text readable on any image, as
			// Desktop's home overlay does; it sits above the image and below
			// the flow content.
			ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).
				Background(p.T.Background.Alpha(nativeBackgroundVeilAlpha)).ZIndex(-1).
				HitTest(func(x, y float32, r ui.Rect) bool { return false })
		}
		ui.Column(c).Grow(1).Center().Padding(24).Children(func() {
			if a.conn == connFailed {
				a.connectionError(c, p)
				return
			}
			if a.home.logoVisible {
				if a.home.logoImage != nil {
					ui.Image(c, a.home.logoImage).Size(58, 58).Radius(14).Fit(ui.Contain)
				} else {
					mothxLogoView(c, 58)
				}
			}
			ui.Row(c).AlignItems(ui.Center).Gap(9).Margin(10, 0, 0, 0).Children(func() {
				ui.Text(c, "MothX").FontSize(26).FontWeight(650)
				ui.Text(c, T(" : Make OSCHINA Tokens Harness eXecution")).FontSize(14).
					FontWeight(500).TextColor(p.T.TextMuted)
			})
			ui.Text(c, T(nativeChineseSlogan)).FontSize(12.5).
				LetterSpacing(0.2).TextColor(p.T.TextMuted).Margin(4, 0, 0, 0)
			if slogan := a.homeConnectionSlogan(); slogan != "" {
				ui.Text(c, slogan).FontSize(11.5).TextColor(p.T.TextMuted).Margin(2, 0, 0, 0)
			}
			a.homePresetTabs(c, p)
			preset := homePresetByID(a.home.preset)
			locale := CurrentLocale()
			ui.Text(c, T(preset.sub(locale))).FontSize(11.5).
				FontWeight(600).TextColor(p.T.Text).Margin(10, 0, 0, 0)
			ui.Text(c, T(preset.desc(locale))).FontSize(10.5).
				TextColor(p.T.TextMuted).MaxWidth(620).LineHeight(1.4).Margin(2, 0, 0, 0)
			a.homePresetPrompts(c, p, preset)
			if a.workspaceName() != "" {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Margin(12, 0, 0, 0).Children(func() {
					ui.Icon(c, iconFolder).FontSize(12).TextColor(p.T.TextMuted)
					ui.Text(c, a.workspaceName()).FontSize(10.5).TextColor(p.T.TextMuted)
				})
			}
		})
	})
}

func (a *nativeApp) homePresetTabs(c *ui.Context, p palette) {
	ui.Row(c).AlignItems(ui.Center).Justify(ui.Center).Gap(8).Margin(12, 0, 0, 0).Children(func() {
		for _, preset := range []struct{ ID, Label string }{
			{"working", "办公"},
			{"coding", "代码"},
			{"design", "创作"},
		} {
			preset := preset
			active := a.home.preset == preset.ID
			b := ui.ButtonBase(c.Key("home-preset:"+preset.ID)).Height(32).Padding(0, 18).Radius(15).
				AlignItems(ui.Center).Justify(ui.Center).Border(1, p.T.Border).Background(p.Chip)
			if active {
				b.Background(p.Active).Border(1, p.T.Accent)
			} else if b.Hovered() {
				b.Background(p.Hover)
			}
			b.Children(func() {
				color := p.T.Text
				if active {
					color = p.T.Accent
				}
				ui.Text(c, T(preset.Label)).FontSize(12).FontWeight(550).TextColor(color)
			})
			b.Label(T(preset.Label))
			if b.Clicked() {
				a.home.preset = preset.ID
				a.saveNativePreferences()
			}
		}
	})
}

func (a *nativeApp) homePresetPrompts(c *ui.Context, p palette, preset homePreset) {
	locale := CurrentLocale()
	labels := preset.quick(locale)
	tags := preset.tags(locale)
	prompts := preset.prompts(locale)
	protocol := executionProtocol(locale)
	ui.Row(c).Wrap().Gap(8).MaxWidth(640).Justify(ui.Center).Margin(14, 0, 0, 0).Children(func() {
		for i, label := range labels {
			i, label := i, label
			tag := ""
			if i < len(tags) {
				tag = tags[i]
			}
			prompt := label
			if i < len(prompts) {
				prompt = prompts[i] + protocol
			}
			b := ui.ButtonBase(c.Key("home-prompt:"+preset.ID+":"+label)).Width(206).Padding(8, 11).Radius(10).
				Border(1, p.T.Border).Background(p.Chip)
			if b.Hovered() {
				b.Background(p.Hover).Border(1, p.T.Accent)
			}
			b.Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, strings.ToUpper(tag)).FontSize(8.5).FontWeight(650).
						LetterSpacing(0.5).TextColor(p.T.TextMuted)
					ui.Text(c, T(label)).FontSize(11).FontWeight(500).TextColor(p.T.Text).SingleLine()
				})
			})
			b.Label(label)
			if b.Clicked() {
				a.insertComposerText(prompt)
			}
		}
	})
}

func (a *nativeApp) homeConnectionSlogan() string {
	switch a.conn {
	case connReady:
		version := strings.TrimSpace(a.runtimeVersion)
		if version == "" {
			version = a.version()
		}
		if version != "" && version != "unknown" {
			return Tf("已连接 · mothx %s · ACP v1", version)
		}
		return T("已连接 · ACP v1")
	case connStarting:
		return T("正在连接 ACP…")
	case connFailed:
		return a.connErr
	default:
		return T("未连接")
	}
}

func (a *nativeApp) connectionError(c *ui.Context, p palette) {
	ui.Column(c).Width(460).Padding(18).Radius(14).Background(p.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(18).TextColor(p.T.Danger)
			ui.Text(c, T("未连接到 MothX ACP")).FontSize(14).FontWeight(650)
		})
		if a.connErr != "" {
			ui.Text(c, T(a.connErr)).FontSize(11.5).TextColor(p.T.TextMuted).Selectable()
		}
		if a.connFix != "" {
			ui.Text(c, T("修复：")+a.connFix).FontSize(11).TextColor(p.T.Accent).LineHeight(1.4).Selectable()
		}
		if len(a.stderr) > 0 {
			ui.Text(c, strings.Join(a.stderr, "\n")).Font("monospace").FontSize(9.5).
				TextColor(p.T.TextMuted).MaxLines(5).Selectable()
		}
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			retry := ui.PrimaryButton(c.Key("retry-connection"), T("重试连接"))
			if retry.Clicked() {
				go a.connect()
			}
			openSettings := ui.Button(c.Key("connection-open-settings"), T("打开设置"))
			openSettings.Label(T("打开设置"))
			if openSettings.Clicked() {
				a.openSettings()
			}
		})
	})
}

// transcriptRenderWindow bounds the live DOM/build cost of very long sessions.
// Full canonical history remains in a.messages and is reachable through older
// message paging; only the rendered tail is capped, matching Desktop.
const transcriptRenderWindow = 300
