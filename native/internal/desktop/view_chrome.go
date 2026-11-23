package desktop

import (
	"github.com/egoist/mygo/ui"
)

// titleBar draws the window chrome instead of the system title bar. It follows
// Desktop's projection: macOS keeps its native traffic lights and centers the
// tagline, while other platforms get app-drawn window controls.
func (a *nativeApp) titleBar(c *ui.Context, p palette, bar ui.TitleBar) {
	height := float32(nativeTitleBarHeightOther)
	left := float32(12)
	if nativeIsMac {
		height = nativeTitleBarHeightMac
		if bar.Height > height {
			height = bar.Height
		}
		left = bar.Left
		if left < 78 {
			left = 78
		}
	}
	ui.Row(c).Height(height).Padding(0, 0, 0, left).AlignItems(ui.Center).Gap(8).
		Background(p.Sidebar).BorderWidth(0, 0, 1, 0).BorderColor(p.T.Border).
		DragWindow().Children(func() {
		mothxLogoView(c, 20)
		ui.Text(c, "MothX").FontSize(12.5).FontWeight(600).TextColor(p.T.Text)
		ui.Box(c).Padding(1, 6).Radius(6).Background(p.Hover).Children(func() {
			ui.Text(c, "v"+a.version()).FontSize(10).TextColor(p.T.TextMuted)
		})
		ui.Spacer(c)
		if nativeIsMac {
			ui.Row(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).
				Justify(ui.Center).AlignItems(ui.Center).
				HitTest(func(x, y float32, r ui.Rect) bool { return false }).Children(func() {
				ui.Text(c, "Make OSCHINA Tokens Harness eXecution").
					FontSize(12).LetterSpacing(0.2).TextColor(p.T.TextMuted).SingleLine()
			})
			return
		}
		ui.Row(c).Height(height).Gap(0).AlignItems(ui.Center).Children(func() {
			a.windowControlButton(c, p, "titlebar-minimize", iconMinimize, "最小化", height, false, a.minimizeWindow)
			a.windowControlButton(c, p, "titlebar-maximize", iconMaximize, "最大化", height, false, a.toggleMaximizeWindow)
			a.windowControlButton(c, p, "titlebar-close", iconX, "关闭窗口", height, true, a.closeWindow)
		})
	})
}

func (a *nativeApp) windowControlButton(c *ui.Context, p palette, key string, icon *ui.SVG, label string, height float32, danger bool, action func()) {
	b := ui.ButtonBase(c.Key(key)).Width(46).Height(height).Justify(ui.Center).AlignItems(ui.Center)
	hovered := b.Hovered()
	if hovered {
		if danger {
			b.Background(ui.Hex("#e81123"))
		} else {
			b.Background(p.Hover)
		}
	}
	b.Children(func() {
		color := p.T.TextMuted
		if hovered {
			color = p.T.Text
			if danger {
				color = ui.Hex("#ffffff")
			}
		}
		ui.Icon(c, icon).FontSize(13).TextColor(color)
	})
	b.Label(T(label)).Tooltip(T(label))
	if b.Clicked() {
		action()
	}
}

func (a *nativeApp) minimizeWindow() {
	if a.win != nil {
		a.win.Minimize()
	}
}

func (a *nativeApp) toggleMaximizeWindow() {
	if a.win != nil {
		a.win.ToggleMaximize()
	}
}

func (a *nativeApp) closeWindow() {
	if a.win != nil {
		a.win.Close()
	}
}

func (a *nativeApp) toggleTheme() {
	a.dark = !a.dark
	if a.dark {
		a.themeMode = "dark"
	} else {
		a.themeMode = "light"
	}
	a.themeSet = true
	a.saveNativePreferences()
}

func (a *nativeApp) toggleLocale() {
	if CurrentLocale() == "en" {
		SetNativeLocale("zh")
	} else {
		SetNativeLocale("en")
	}
	a.saveNativePreferences()
}

func (a *nativeApp) appearanceIcon() *ui.SVG {
	if a.dark {
		return iconSun
	}
	return iconMoon
}
