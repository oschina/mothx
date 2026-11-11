package main

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

// The brand mark is the same moth used by the MothX desktop app and docs.
//
//go:embed assets/mothx.png
var mothxLogoPNG []byte

var mothxLogo = func() *ui.Bitmap {
	b, err := ui.DecodeBitmap(mothxLogoPNG)
	if err != nil {
		return nil
	}
	return b
}()

func svg(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

var (
	iconLogo         = svg(`<path d="M12 3.5c-1.9 1.2-3.1 3.3-3.1 5.6 0 1.2.4 2.3 1 3.2-1.9.3-3.4 1.6-4.1 3.4-.9 2.4.4 5 2.9 5.7 2 .6 4.1-.4 4.6-2.4"/><path d="M12 3.5c1.9 1.2 3.1 3.3 3.1 5.6 0 1.2-.4 2.3-1 3.2 1.9.3 3.4 1.6 4.1 3.4.9 2.4-.4 5-2.9 5.7-2 .6-4.1-.4-4.6-2.4"/><circle cx="12" cy="16.5" r="1.1" fill="currentColor" stroke="none"/>`)
	iconPlus         = svg(`<path d="M12 5v14M5 12h14"/>`)
	iconSearch       = svg(`<circle cx="11" cy="11" r="7"/><path d="m20 20-3.6-3.6"/>`)
	iconFolder       = svg(`<path d="M3.5 7.5a2 2 0 0 1 2-2h3l2 2h8a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z"/>`)
	iconChevronRight = svg(`<path d="m9 6 6 6-6 6"/>`)
	iconChevronDown  = svg(`<path d="m6 9 6 6 6-6"/>`)
	iconChat         = svg(`<path d="M20 12a8 8 0 0 1-11.6 7.1L4 20l1-4.2A8 8 0 1 1 20 12z"/>`)
	iconSun          = svg(`<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>`)
	iconMoon         = svg(`<path d="M20 14.2A8 8 0 0 1 9.8 4a8 8 0 1 0 10.2 10.2z"/>`)
	iconPanelLeft    = svg(`<rect x="3.5" y="4.5" width="17" height="15" rx="2.5"/><path d="M9 4.5v15"/>`)
	iconArrowUp      = svg(`<path d="M12 19V5M6 11l6-6 6 6"/>`)
	iconStop         = svg(`<rect x="7" y="7" width="10" height="10" rx="2" fill="currentColor" stroke="none"/>`)
	iconPaperclip    = svg(`<path d="m20 11-8.5 8.5a5 5 0 0 1-7-7L13 4a3.4 3.4 0 0 1 4.8 4.8l-8.5 8.5a1.8 1.8 0 0 1-2.5-2.5l8-8"/>`)
	iconX            = svg(`<path d="M6 6l12 12M18 6 6 18"/>`)
	iconWrench       = svg(`<path d="M14.5 6.5a4 4 0 0 0-5.2 5.2L4 17l3 3 5.3-5.3a4 4 0 0 0 5.2-5.2l-2.4 2.4-2.9-.6-.6-2.9z"/>`)
	iconTerminal     = svg(`<path d="m5 7 5 5-5 5M13 17h6"/>`)
	iconFile         = svg(`<path d="M14 3.5H7a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8.5z"/><path d="M14 3.5v5h5"/>`)
	iconCheck        = svg(`<path d="m5 12.5 4.5 4.5L19 7.5"/>`)
	iconAlert        = svg(`<path d="M12 4 3 20h18z"/><path d="M12 10v4M12 17.5v.1"/>`)
	iconSpark        = svg(`<path d="M12 3.5 13.8 9l5.5 1.8-5.5 1.8L12 18l-1.8-5.4L4.7 10.8 10.2 9z"/>`)
	iconGlobe        = svg(`<circle cx="12" cy="12" r="8"/><path d="M4 12h16M12 4c2.2 2.2 3.3 5 3.3 8S14.2 17.8 12 20c-2.2-2.2-3.3-5-3.3-8S9.8 6.2 12 4z"/>`)
	iconSettings     = svg(`<path d="M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.07a1.99 1.99 0 0 1-2.82 2.82l-.07-.06a1.65 1.65 0 0 0-1.82.33 1.65 1.65 0 0 0-.67 1.61v.17a2 2 0 0 1-4 0v-.17a1.65 1.65 0 0 0-.67-1.61 1.65 1.65 0 0 0-1.82-.33l-.07.06a1.99 1.99 0 0 1-2.82-2.82l.06-.07a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.61-.67h-.17a2 2 0 0 1 0-4h.17a1.65 1.65 0 0 0 1.61-.67c.39-.49.5-1.12.33-1.82l-.06-.07a1.99 1.99 0 0 1 2.82-2.82l.07.06a1.65 1.65 0 0 0 1.82.33h.17c.49.39 1.12.5 1.61.67a1.65 1.65 0 0 0 .67-1.61v-.17a2 2 0 0 1 4 0v.17c0 .7.18 1.32.67 1.61.49.28 1.12.18 1.61-.67h.17a2 2 0 0 1 2.82 2.82l-.06.07a1.65 1.65 0 0 0-.33 1.82 1.65 1.65 0 0 0 1.61.67h.17a2 2 0 0 1 0 4h-.17a1.65 1.65 0 0 0-1.61.67z"/>`)
	iconArrowLeft    = svg(`<path d="M19 12H5M12 19l-7-7 7-7"/>`)
	iconCpu          = svg(`<rect x="7" y="7" width="10" height="10" rx="2"/><path d="M2 10h2M2 14h2M20 10h2M20 14h2M10 2v2M14 2v2M10 20v2M14 20v2"/><rect x="9" y="9" width="6" height="6" rx="1"/>`)
	iconBook         = svg(`<path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M4 4.5A2.5 2.5 0 0 1 6.5 7H20v13H6.5A2.5 2.5 0 0 1 4 17.5v-13z"/>`)
	iconShop         = svg(`<path d="M3 3h18v4H3zM3 7l2 11h14l2-11"/><path d="M9 14a2 2 0 0 0 4 0"/>`)
	iconUsers        = svg(`<path d="M17 21v-2a4 4 0 0 0-3-3.87M9 21v-2a4 4 0 0 1 3-3.87M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8z"/><circle cx="18" cy="8" r="2"/><circle cx="6" cy="8" r="2"/>`)
	iconClock        = svg(`<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 3"/>`)
	iconServer       = svg(`<rect x="4" y="3" width="16" height="6" rx="2"/><rect x="4" y="15" width="16" height="6" rx="2"/><circle cx="8" cy="6" r="1"/><circle cx="8" cy="18" r="1"/>`)
	iconRefresh      = svg(`<path d="M21 12a9 9 0 1 1-2.64-6.36"/><path d="M21 3v9h-9"/>`)
	iconList         = svg(`<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>`)
	iconDoc          = svg(`<path d="M14 3.5H7a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8.5z"/><path d="M14 3.5v5h5"/>`)
)

// mothxLogoView shows the official MothX mark, with the drawn icon as a
// fallback for a platform where decoding the embedded bitmap fails.
func mothxLogoView(c *ui.Context, size float32) {
	if mothxLogo != nil {
		ui.Image(c, mothxLogo).Size(size, size).Fit(ui.Contain)
		return
	}
	ui.Icon(c, iconLogo).FontSize(size).TextColor(c.Theme().Accent)
}
