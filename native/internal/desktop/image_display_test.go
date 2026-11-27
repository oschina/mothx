package desktop

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// reviewImage builds a solid picture so headless renders can exercise the
// real Bitmap path instead of a stub ImageSource.
func reviewImage(t *testing.T, width, height int, c color.RGBA) ui.ImageSource {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	return ui.NewBitmap(img)
}

func TestBoundedImageSizeNeverUpscales(t *testing.T) {
	cases := []struct {
		name       string
		w, h       float32
		maxW, maxH float32
		wantW      float32
	}{
		{"tiny icon", 16, 16, 360, 260, 16},
		{"wide", 600, 200, 360, 260, 360},
		{"tall", 120, 480, 360, 260, 65},
		{"huge", 4000, 2000, 360, 260, 360},
		{"narrow parent", 600, 240, 200, 260, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotW, gotH := boundedImageSize(tc.w, tc.h, tc.maxW, tc.maxH)
			if math.Abs(float64(gotW-tc.wantW)) > 0.5 {
				t.Fatalf("display width = %v, want %v", gotW, tc.wantW)
			}
			if tc.w > 0 && gotH > tc.maxH {
				t.Fatalf("display height = %v exceeds max %v", gotH, tc.maxH)
			}
			if gotW > tc.w+0.5 || gotH > tc.h+0.5 {
				t.Fatalf("image upscaled from %v×%v to %v×%v", tc.w, tc.h, gotW, gotH)
			}
		})
	}
}

func TestChatInlineImageKeepsIntrinsicSize(t *testing.T) {
	a := testApp()
	a.themeMode = "light"
	a.themeSet = false
	a.conn = connReady
	a.currentSession = "s1"
	a.messages = []*chatMessage{{
		ID: "u1", Kind: messageUser, Text: "tiny", Done: true,
		Images: []chatImage{{MimeType: "image/png", Image: reviewImage(t, 16, 16, color.RGBA{220, 40, 40, 255})}},
	}}
	a.messageIndex = map[string]*chatMessage{"u1": a.messages[0]}

	tt := ui.NewTester(a.view, 1180, 760)
	rect, ok := tt.Find("图片")
	if !ok {
		t.Fatalf("inline image was not projected: %q", tt.Texts())
	}
	if rect.W > 20 || rect.H > 20 {
		t.Fatalf("16×16 inline image rendered at %v×%v (upscaled)", rect.W, rect.H)
	}
}

func TestChatInlineImageFitsNarrowTranscript(t *testing.T) {
	a := testApp()
	a.themeMode = "light"
	a.themeSet = false
	a.conn = connReady
	a.currentSession = "s1"
	a.messages = []*chatMessage{
		{ID: "u1", Kind: messageUser, Text: "wide", Done: true,
			Images: []chatImage{{MimeType: "image/png", Image: reviewImage(t, 600, 200, color.RGBA{40, 180, 90, 255})}}},
		{ID: "p1", Kind: messagePlan, Plan: []planEntry{{Content: "step", Status: "in_progress"}}, Done: true},
	}
	a.planMessageID = "p1"
	a.messageIndex = map[string]*chatMessage{"u1": a.messages[0], "p1": a.messages[1]}

	// 860 DIP plus the 288-DIP plan panel leaves ~211 DIP of bubble content.
	tt := ui.NewTester(a.view, 860, 560)
	rect, ok := tt.Find("图片")
	if !ok {
		t.Fatalf("inline image was not projected: %q", tt.Texts())
	}
	if rect.W > 240 {
		t.Fatalf("inline image width = %v overflows the narrow bubble", rect.W)
	}
	if ratio := rect.W / rect.H; math.Abs(float64(ratio-3)) > 0.1 {
		t.Fatalf("inline image aspect = %v, want 3", ratio)
	}
}

func TestAssistantInlineImageRenders(t *testing.T) {
	a := testApp()
	a.themeMode = "light"
	a.themeSet = false
	a.conn = connReady
	a.currentSession = "s1"
	a.messages = []*chatMessage{{
		ID: "a1", Kind: messageAssistant, Text: "image follows", Done: true,
		Images: []chatImage{{MimeType: "image/png", Image: reviewImage(t, 600, 200, color.RGBA{40, 90, 220, 255})}},
	}}
	a.messageIndex = map[string]*chatMessage{"a1": a.messages[0]}

	tt := ui.NewTester(a.view, 1180, 760)
	rect, ok := tt.Find("图片")
	if !ok {
		t.Fatalf("assistant inline image was not rendered: %q", tt.Texts())
	}
	if rect.W > 521 {
		t.Fatalf("assistant image width = %v exceeds its bound", rect.W)
	}
	if ratio := rect.W / rect.H; math.Abs(float64(ratio-3)) > 0.1 {
		t.Fatalf("assistant image aspect = %v, want 3", ratio)
	}
}

func TestArtifactPreviewImageFitsWindow(t *testing.T) {
	for _, tc := range []struct {
		name    string
		w, h    int
		windowW int
		windowH int
	}{
		{"tall", 200, 1600, 1180, 760},
		{"tall-min-window", 200, 1600, 860, 560},
		{"tiny", 16, 16, 1180, 760},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp()
			a.themeMode = "light"
			a.themeSet = false
			a.conn = connReady
			a.previewOpen = true
			a.previewTitle = "artifact.png"
			a.previewMediaType = "image/png"
			a.previewImage = reviewImage(t, tc.w, tc.h, color.RGBA{40, 120, 200, 255})

			tt := ui.NewTester(a.view, tc.windowW, tc.windowH)
			rect, ok := tt.Find("图片")
			if !ok {
				t.Fatalf("artifact preview image was not rendered: %q", tt.Texts())
			}
			if rect.W > 680 || rect.H > 560 {
				t.Fatalf("preview image = %v×%v exceeds its bound", rect.W, rect.H)
			}
			if rect.Y+rect.H > float32(tc.windowH) {
				t.Fatalf("preview image bottom = %v escapes the window", rect.Y+rect.H)
			}
			if ratio := rect.W / rect.H; math.Abs(float64(ratio-float32(tc.w)/float32(tc.h))) > 0.02 {
				t.Fatalf("preview aspect = %v, want %v", ratio, float32(tc.w)/float32(tc.h))
			}
			if tc.w <= 16 && (rect.W > 20 || rect.H > 20) {
				t.Fatalf("tiny preview image upscaled to %v×%v", rect.W, rect.H)
			}
		})
	}
}

func TestZeroOpacityBackgroundStaysInactive(t *testing.T) {
	a := testApp()
	a.home.backgroundImage = reviewImage(t, 4, 4, color.RGBA{255, 0, 255, 255})
	a.home.backgroundScope = "app"
	a.home.backgroundOpacity = 0
	if a.appBackgroundActive() {
		t.Fatal("a zero-opacity background must not veil the app shell")
	}
	a.home.backgroundOpacity = 0.5
	if !a.appBackgroundActive() {
		t.Fatal("a visible background must stay active")
	}
}
