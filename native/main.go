package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	if bin, ok := parseLaunchBinaryArg(os.Args[1:]); ok {
		setLaunchBinaryOverride(bin)
	}
	a := newNativeApp()
	activeNativeApp = a
	mygo.App.WhenReady(func() {
		mygo.App.SetLanguages("zh-Hans", "en")
		setLocaleFromTag(mygo.App.Locale())
		a.prefsPath = nativePreferencesPath()
		if prefs, ok := loadNativePreferences(a.prefsPath); ok {
			a.applyNativePreferences(prefs)
		}
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "MothX Native",
			Width:           1180,
			Height:          760,
			MinWidth:        860,
			MinHeight:       560,
			StateKey:        "main",
			BackgroundColor: "#191a1f",
			Content:         ui.View(a.view),
		})
		a.win.OnClose(func(*mygo.CloseEvent) {
			a.shutdown()
		})
		go a.connect()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
