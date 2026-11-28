package desktop

import (
	"log"
	"os"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// nativeIsMac selects the title bar projection: macOS keeps its native
// traffic lights, every other platform gets app-drawn window controls.
var nativeIsMac = runtime.GOOS == "darwin"

// nativeIsLinux selects a frameless Linux window: no window manager or
// compositor decoration (SSD) and no GTK client-side decoration shadow (CSD).
var nativeIsLinux = runtime.GOOS == "linux"

const (
	nativeTitleBarHeightMac   = 42
	nativeTitleBarHeightOther = 40
)

func nativeWindowOptions() mygo.WindowOptions {
	opts := mygo.WindowOptions{
		Title:           "MothX Native",
		Width:           1180,
		Height:          760,
		MinWidth:        860,
		MinHeight:       560,
		StateKey:        "main",
		BackgroundColor: "#191a1f",
	}
	switch {
	case nativeIsMac:
		opts.TitleBarStyle = mygo.TitleBarHidden
		opts.TrafficLightPosition = &mygo.Point{X: 12, Y: 12}
	case nativeIsLinux:
		// A frameless window tells a Wayland compositor that the app decorates
		// itself, so it adds no server-side title bar. DisableShadow keeps GTK
		// from drawing its client-side decorations, the transparent shadow and
		// border ring around the page that resizes the window instead of the
		// app. The app draws its own title bar and window controls, and the
		// content edges still resize the window.
		opts.Frameless = true
		opts.DisableShadow = true
	default:
		opts.TitleBarStyle = mygo.TitleBarHidden
		opts.HideWindowButtons = true
		opts.TitleBarHeight = nativeTitleBarHeightOther
	}
	return opts
}

// Run is the Native entry point. It owns the MyGo application lifecycle, the
// startup preference load, the launch quick-start workspace, and connection to
// the ACP runtime. The root package main stays a thin launcher.
func Run() {
	if bin, ok := parseLaunchBinaryArg(os.Args[1:]); ok {
		setLaunchBinaryOverride(bin)
	}
	a := newNativeApp()
	activeNativeApp = a
	mygo.App.WhenReady(func() {
		mygo.App.SetLanguages("zh-Hans", "en")
		SetNativeLocale("zh")
		a.prefsPath = nativePreferencesPath()
		if prefs, ok := loadNativePreferences(a.prefsPath); ok {
			a.applyNativePreferences(prefs)
		}
		// Desktop's launch quick-start: the first draft gets a private scratch
		// directory instead of the process cwd. Every session still supplies
		// its own cwd through ACP afterwards.
		a.initQuickStartWorkspace()
		opts := nativeWindowOptions()
		opts.Content = ui.View(a.view)
		a.win = mygo.NewWindow(opts)
		a.win.OnClose(func(*mygo.CloseEvent) {
			a.shutdown()
		})
		go a.connect()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
