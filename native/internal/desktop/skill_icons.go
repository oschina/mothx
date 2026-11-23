package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
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
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return nil, false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, false
	}
	if !skillIconHostAllowed(parsed.Hostname()) {
		return nil, false
	}
	return parsed, true
}

// skillIconHostAllowed rejects syntax-level local/private targets. DNS names
// are validated again at dial time (including redirects), so a resolved name
// cannot bypass this through a private address.
func skillIconHostAllowed(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return skillIconIPAllowed(ip)
	}
	return true
}

func skillIconIPAllowed(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 100.64.0.0/10 carrier-grade NAT is not covered by IsPrivate.
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return false
		}
	}
	return true
}

// skillIconDialContext resolves the icon host and dials only an allowed
// public address. Checking at dial time covers redirects and DNS rebinding
// instead of trusting an earlier URL-level check.
func skillIconDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			ips = append(ips, addr.IP)
		}
	}
	var lastErr error
	for _, ip := range ips {
		if !skillIconIPAllowed(ip) {
			lastErr = fmt.Errorf("skill icon address blocked: %s", ip)
			continue
		}
		dialer := net.Dialer{}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("skill icon host has no allowed address")
}

func skillIconHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext:         skillIconDialContext,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many skill icon redirects")
			}
			if _, ok := validSkillIconURL(req.URL.String()); !ok {
				return errors.New("skill icon redirect target blocked")
			}
			return nil
		},
	}
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
	client := skillIconHTTPClient()
	image, err := func() (ui.ImageSource, error) {
		if _, ok := validSkillIconURL(raw); !ok {
			return nil, errors.New("skill icon URL blocked")
		}
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
	ui.Icon(c, iconSpark).FontSize(size * 0.5).TextColor(p.T.Accent)
}
