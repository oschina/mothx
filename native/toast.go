package main

import (
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
)

// Toast is the Native shell's transient non-blocking feedback layer. Toasts
// never replace canonical run/session state: operations still write their
// durable outcome through ACP, and a toast only makes the result discoverable
// after the initiating action has left the composer.

const (
	maxToasts        = 3
	toastDisplayTime = 3200 * time.Millisecond
)

type toastItem struct {
	ID    int64
	Text  string
	Error bool
}

func (a *nativeApp) showToast(text string) {
	a.showToastKind(text, false)
}

func (a *nativeApp) showErrorToast(text string) {
	a.showToastKind(text, true)
}

func (a *nativeApp) showToastKind(text string, isError bool) {
	if text == "" {
		return
	}
	id := atomic.AddInt64(&a.toastSeq, 1)
	a.toasts = append(a.toasts, toastItem{ID: id, Text: text, Error: isError})
	if len(a.toasts) > maxToasts {
		a.toasts = a.toasts[len(a.toasts)-maxToasts:]
	}
	time.AfterFunc(toastDisplayTime, func() {
		a.dispatch(func() { a.dismissToast(id) })
	})
}

func (a *nativeApp) dismissToast(id int64) {
	if len(a.toasts) == 0 {
		return
	}
	out := a.toasts[:0]
	for _, toast := range a.toasts {
		if toast.ID != id {
			out = append(out, toast)
		}
	}
	a.toasts = out
}

func (a *nativeApp) toastView(c *ui.Context, p palette) {
	if len(a.toasts) == 0 {
		return
	}
	ui.Column(c).Absolute().Right(22).Bottom(22).Width(340).Gap(7).ZIndex(300).
		HitTest(func(x, y float32, r ui.Rect) bool { return false }).Children(func() {
		for _, toast := range a.toasts {
			toast := toast
			bg := p.t.Surface
			border := p.t.Border
			textColor := p.t.Text
			if toast.Error {
				bg = p.errorBg
				border = ui.RGBA(255, 107, 107, 0.35)
				textColor = p.t.Danger
			}
			ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(9, 11).Radius(9).
				Background(bg).Border(1, border).Children(func() {
				if toast.Error {
					ui.Icon(c, iconAlert).FontSize(13).TextColor(p.t.Danger)
				} else {
					ui.Icon(c, iconCheck).FontSize(13).TextColor(p.t.Success)
				}
				ui.Text(c, T(toast.Text)).FontSize(11).TextColor(textColor).
					LineHeight(1.35).Grow(1)
				if ui.ButtonBase(c).Size(20, 20).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconX).FontSize(10).TextColor(p.t.TextMuted)
				}).Clicked() {
					a.dismissToast(toast.ID)
				}
			})
		}
	})
}
