package main

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// SkillHub projects an optional remote icon URL. Native loads it through this
// small bounded cache instead of a browser image primitive: http/https only,
// one fetch per URL, a fixed timeout, and a hard body limit. Failure is cached
// so an unreachable marketplace cannot retry on every frame.
const nativeSkillIconMaxBytes = 2 << 20

func validSkillIconURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return nil, false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, false
	}
	return parsed, true
}

func (a *nativeApp) skillIcon(skill skillHubSkill) ui.ImageSource {
	raw := strings.TrimSpace(skill.IconURL)
	if raw == "" {
		return nil
	}
	if image, ok := a.skillIconImages[raw]; ok {
		return image
	}
	if a.skillIconLoading[raw] || a.skillIconFailed[raw] {
		return nil
	}
	if _, ok := validSkillIconURL(raw); !ok {
		if a.skillIconFailed == nil {
			a.skillIconFailed = make(map[string]bool)
		}
		a.skillIconFailed[raw] = true
		return nil
	}
	if a.skillIconLoading == nil {
		a.skillIconLoading = make(map[string]bool)
	}
	a.skillIconLoading[raw] = true
	go a.fetchSkillIcon(raw)
	return nil
}

func (a *nativeApp) fetchSkillIcon(raw string) {
	client := &http.Client{Timeout: 10 * time.Second}
	image, err := func() (ui.ImageSource, error) {
		resp, err := client.Get(raw)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, io.ErrUnexpectedEOF
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, nativeSkillIconMaxBytes+1))
		if err != nil {
			return nil, err
		}
		if len(data) > nativeSkillIconMaxBytes || len(data) == 0 {
			return nil, io.ErrShortBuffer
		}
		return ui.DecodeImage(data)
	}()
	a.dispatch(func() {
		if a.skillIconLoading == nil {
			a.skillIconLoading = make(map[string]bool)
		}
		delete(a.skillIconLoading, raw)
		if err != nil || image == nil {
			if a.skillIconFailed == nil {
				a.skillIconFailed = make(map[string]bool)
			}
			a.skillIconFailed[raw] = true
			return
		}
		if a.skillIconImages == nil {
			a.skillIconImages = make(map[string]ui.ImageSource)
		}
		a.skillIconImages[raw] = image
	})
}

// skillIconElement draws the cached remote icon or the existing spark fallback.
func (a *nativeApp) skillIconElement(c *ui.Context, p palette, skill skillHubSkill, size float32) {
	if image := a.skillIcon(skill); image != nil {
		ui.Image(c, image).Size(size, size).Fit(ui.Cover).Radius(size * 0.3)
		return
	}
	ui.Icon(c, iconSpark).FontSize(size * 0.5).TextColor(p.t.Accent)
}
