package main

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
		TitleBarStyle:   mygo.TitleBarHidden,
	}
	if nativeIsMac {
		opts.TrafficLightPosition = &mygo.Point{X: 12, Y: 12}
	} else {
		opts.HideWindowButtons = true
		opts.TitleBarHeight = nativeTitleBarHeightOther
	}
	return opts
}

func main() {
	if bin, ok := parseLaunchBinaryArg(os.Args[1:]); ok {
		setLaunchBinaryOverride(bin)
	}
	a := newNativeApp()
	activeNativeApp = a
	mygo.App.WhenReady(func() {
		mygo.App.SetLanguages("zh-Hans", "en")
		setNativeLocale("zh")
		a.prefsPath = nativePreferencesPath()
		if prefs, ok := loadNativePreferences(a.prefsPath); ok {
			a.applyNativePreferences(prefs)
		}
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
