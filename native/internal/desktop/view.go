package desktop

import (
	"github.com/egoist/mygo/ui"
)

// This file owns the root view and theme projection. Chrome, sidebar, pages,
// chat, transcript, message cards, and composer render in view_*.go.

func (a *nativeApp) applyTheme(c *ui.Context) palette {
	a.services = c.Services()
	if !a.themeSet {
		switch a.themeMode {
		case "dark":
			a.dark = true
		case "light":
			a.dark = false
		default:
			a.dark = c.Theme().Dark
		}
		a.themeSet = true
	}
	t := ui.DarkTheme()
	if !a.dark {
		t = ui.LightTheme()
	}
	t.FontSize = 13
	t.Radius = 8
	t.Spacing = 4
	t.Accent = ui.Hex("#4d6bfe")
	t.AccentHover = ui.Hex("#6480ff")
	t.AccentPressed = ui.Hex("#3f5ce8")
	t.AccentText = ui.Hex("#ffffff")
	t.InactiveAccent = t.Accent
	t.Focus = ui.RGBA(77, 107, 254, 0.65)
	t.Selection = ui.RGBA(77, 107, 254, 0.30)
	t.Scrollbar = ui.RGBA(127, 133, 148, 0.45)
	if a.dark {
		t.Background = ui.Hex("#191a1f")
		t.Surface = ui.Hex("#24262d")
		t.SurfaceHover = ui.Hex("#2c2e36")
		t.SurfacePressed = ui.Hex("#343740")
		t.Border = ui.Hex("#30323b")
		t.Text = ui.Hex("#eceef4")
		t.TextMuted = ui.Hex("#8d929e")
		t.Danger = ui.Hex("#ff6b6b")
		t.Warning = ui.Hex("#f4b860")
		t.Success = ui.Hex("#5ad19a")
	} else {
		t.Background = ui.Hex("#ffffff")
		t.Surface = ui.Hex("#f4f4f6")
		t.SurfaceHover = ui.Hex("#ececef")
		t.SurfacePressed = ui.Hex("#e2e2e6")
		t.Border = ui.Hex("#e6e6ea")
		t.Text = ui.Hex("#1b1c20")
		t.TextMuted = ui.Hex("#6c707a")
		t.Danger = ui.Hex("#d92d20")
		t.Warning = ui.Hex("#c77700")
		t.Success = ui.Hex("#14875a")
	}
	c.SetTheme(t)
	p := palette{T: t}
	if a.dark {
		p.Sidebar = ui.Hex("#121318")
		p.Bubble = ui.Hex("#2a2c34")
		p.Hover = ui.Hex("#23252c")
		p.Composer = ui.Hex("#202127")
		p.Chip = ui.Hex("#2a2c34")
		p.Thought = ui.Hex("#1d1f25")
		p.Active = ui.RGBA(77, 107, 254, 0.18)
		p.ErrorBg = ui.RGBA(255, 107, 107, 0.10)
		p.WarnBg = ui.RGBA(244, 184, 96, 0.10)
		p.OkBg = ui.RGBA(90, 209, 154, 0.10)
	} else {
		p.Sidebar = ui.Hex("#f7f7f9")
		p.Bubble = ui.Hex("#f1f2f4")
		p.Hover = ui.Hex("#ececef")
		p.Composer = ui.Hex("#ffffff")
		p.Chip = ui.Hex("#f0f0f3")
		p.Thought = ui.Hex("#f7f7f9")
		p.Active = ui.RGBA(77, 107, 254, 0.10)
		p.ErrorBg = ui.RGBA(217, 45, 32, 0.07)
		p.WarnBg = ui.RGBA(199, 119, 0, 0.08)
		p.OkBg = ui.RGBA(20, 135, 90, 0.08)
	}
	return p
}

func (a *nativeApp) view(c *ui.Context) {
	p := a.applyTheme(c)
	appBackground := a.appBackgroundActive()
	var veil ui.Color
	if appBackground {
		veil = p.T.Background.Alpha(nativeBackgroundVeilAlpha)
		p = p.Veiled()
	}
	bar := c.TitleBar()
	rootBackground := p.T.Background
	if appBackground {
		// The image + veil layer below is the shell surface; an opaque root
		// would hide it entirely.
		rootBackground = ui.Color{}
	}
	ui.Column(c).Fill().Background(rootBackground).Children(func() {
		if appBackground {
			a.appBackgroundLayer(c, veil)
		}
		a.titleBar(c, p, bar)
		ui.Row(c).Grow(1).Children(func() {
			if a.sidebar {
				a.sidebarView(c, p)
			}
			if a.settingsOpen {
				a.settingsPage(c, p)
			} else if a.historyOpen {
				a.historyView(c, p)
			} else if a.page == "home" {
				a.homePage(c, p)
			} else if a.page == "skills" {
				a.skillsPage(c, p)
			} else if a.page == "automation" {
				a.automationPage(c, p)
			} else if a.page == "library" {
				a.libraryPage(c, p)
			} else {
				a.mainView(c, p)
			}
		})
	})
	if a.previewOpen {
		a.artifactPreviewDialog(c, p)
	}
	if a.settingsOpen {
		a.settingsProviderDialogs(c, p)
		a.settingsMCPDialogs(c, p)
		a.settingsExpertsDialogs(c, p)
	}
	if a.settingsOpen || a.page == "library" {
		a.settingsKnowledgeDialogs(c, p)
	}
	if a.settingsOpen || a.page == "skills" {
		a.settingsSkillHubDialogs(c, p)
	}
	if a.settingsOpen || a.page == "automation" {
		a.settingsCronDialogs(c, p)
	}
	if a.historyPanel.DialogOpen || a.historyPanel.ProjectDialogOpen {
		a.historyDialogs(c, p)
	}
	if a.worktree.Open {
		a.worktreeDialog(c, p)
	}
	if a.router.Open {
		a.routerDialog(c, p)
	}
	if a.subagent.Open {
		a.subagentDialog(c, p)
	}
	a.modelPickerDialog(c, p)
	a.toastView(c, p)
}

// appBackgroundLayer paints the user background across the whole shell with
// a theme-colored veil, mirroring Desktop's homeBackgroundScope=app. It is
// presentation only and never intercepts input.
func (a *nativeApp) appBackgroundLayer(c *ui.Context, veil ui.Color) {
	opacity := float32(a.home.backgroundOpacity)
	if opacity < 0 {
		opacity = 0
	}
	if opacity > 1 {
		opacity = 1
	}
	x, y := a.homeObjectPosition()
	// MyGo paints absolute children above flow siblings; a negative ZIndex
	// puts the image and veil under the shell while staying above the root
	// background, so the chrome renders on top of them.
	ui.Image(c, a.home.backgroundImage).Absolute().Left(0).Top(0).Right(0).Bottom(0).
		Fit(a.homeFit()).ObjectPosition(x, y).Opacity(opacity).
		Blur(float32(a.home.backgroundBlur)).ZIndex(-2).
		HitTest(func(x, y float32, r ui.Rect) bool { return false })
	ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(veil).ZIndex(-1).
		HitTest(func(x, y float32, r ui.Rect) bool { return false })
}
