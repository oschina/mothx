package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	a := newNativeApp()
	mygo.App.WhenReady(func() {
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
