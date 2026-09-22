package serve

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/messaging/wechat"
	"golang.org/x/net/html"
)

type wechatLoginSession struct {
	mu        sync.Mutex
	cancel    context.CancelFunc
	state     string
	qrURL     string
	err       string
	userID    string
	startedAt time.Time
	updatedAt time.Time
}

type wechatLoginStatus struct {
	State     string `json:"state"`
	QRURL     string `json:"qrUrl,omitempty"`
	QROpenURL string `json:"qrOpenUrl,omitempty"`
	Error     string `json:"error,omitempty"`
	UserID    string `json:"userId,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	Enabled   bool   `json:"enabled"`
	LoggedIn  bool   `json:"loggedIn"`
}

type wechatLoginQRResponse struct {
	DataURL     string `json:"dataUrl"`
	Base64      string `json:"base64"`
	ContentType string `json:"contentType"`
}

func newWechatLoginSession() (*wechatLoginSession, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UTC()
	return &wechatLoginSession{
		cancel:    cancel,
		state:     "starting",
		startedAt: now,
		updatedAt: now,
	}, ctx
}

func (s *wechatLoginSession) update(state string, fn func()) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if state != "" {
		s.state = state
	}
	if fn != nil {
		fn()
	}
	s.updatedAt = time.Now().UTC()
}

func (s *wechatLoginSession) snapshot(enabled bool) wechatLoginStatus {
	if s == nil {
		return wechatLoginStatus{State: "idle", Enabled: enabled}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := wechatLoginStatus{
		State:    s.state,
		Error:    s.err,
		UserID:   s.userID,
		Enabled:  enabled,
		LoggedIn: s.state == "confirmed",
	}
	if s.qrURL != "" {
		proxyURL := "/api/channels/wechat/login/qr?ts=" + strconv.FormatInt(s.updatedAt.UnixNano(), 10)
		out.QRURL = proxyURL
		out.QROpenURL = qrOpenURL(s.qrURL, proxyURL)
	}
	if !s.startedAt.IsZero() {
		out.StartedAt = s.startedAt.Format(time.RFC3339)
	}
	if !s.updatedAt.IsZero() {
		out.UpdatedAt = s.updatedAt.Format(time.RFC3339)
	}
	return out
}

func (s *wechatLoginSession) active() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case "confirmed", "error", "cancelled":
		return false
	default:
		return true
	}
}

func (s *wechatLoginSession) cancelLogin() {
	if s == nil {
		return
	}
	s.cancel()
	s.update("cancelled", nil)
}

func (rt *channelRuntime) handleWechatLogin(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, rt.wechatLoginSnapshot())
		case http.MethodPost:
			cfg := rt.configSnapshot()
			if rt == nil || cfg == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "channel runtime unavailable"})
				return
			}
			if current := rt.currentWechatLogin(); current != nil && current.active() {
				current.cancelLogin()
			}
			sess, ctx := newWechatLoginSession()
			rt.setWechatLogin(sess)
			credPath := rt.wechatCredPath()
			go rt.runWechatLogin(ctx, sess, configPath, credPath)
			writeJSON(w, http.StatusAccepted, sess.snapshot(cfg.Channels.Wechat.Enabled))
		case http.MethodDelete:
			if current := rt.currentWechatLogin(); current != nil {
				current.cancelLogin()
			}
			writeJSON(w, http.StatusOK, rt.wechatLoginSnapshot())
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (rt *channelRuntime) handleWechatLoginQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	source := ""
	if current := rt.currentWechatLogin(); current != nil {
		current.mu.Lock()
		source = current.qrURL
		current.mu.Unlock()
	}
	source = strings.TrimSpace(source)
	if source == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "QR code is not available"})
		return
	}
	if strings.HasPrefix(source, "//") {
		source = "https:" + source
	}
	if r.URL.Query().Get("format") == "base64" {
		serveWechatQRBase64(w, r, source)
		return
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		proxyWechatQR(w, r, source)
		return
	}
	serveInlineQR(w, source)
}

func serveWechatQRBase64(w http.ResponseWriter, r *http.Request, source string) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	var (
		data        []byte
		contentType string
		err         error
	)
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		data, contentType, err = fetchWechatQRImage(ctx, source)
	} else {
		data, contentType, err = decodeInlineQR(source)
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	if strings.HasPrefix(strings.TrimSpace(string(data)), "<svg") {
		contentType = "image/svg+xml"
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	writeJSON(w, http.StatusOK, wechatLoginQRResponse{
		DataURL:     "data:" + contentType + ";base64," + encoded,
		Base64:      encoded,
		ContentType: contentType,
	})
}

func proxyWechatQR(w http.ResponseWriter, r *http.Request, source string) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	data, contentType, err := fetchWechatQRImage(ctx, source)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	if strings.HasPrefix(strings.TrimSpace(string(data)), "<svg") {
		contentType = "image/svg+xml"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func fetchWechatQRImage(ctx context.Context, source string) ([]byte, string, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	data, contentType, finalURL, err := fetchWechatQRURL(ctx, client, source, wechat.DefaultBaseURL+"/")
	if err != nil {
		return nil, "", err
	}
	if !isHTMLResponse(contentType, data) {
		return data, contentType, nil
	}
	imageURL, err := extractQRCodeImageURL(data, finalURL)
	if err != nil {
		return nil, "", fmt.Errorf("extract QR image from ilink page: %w", err)
	}
	if strings.HasPrefix(imageURL, "data:image/") {
		return decodeQRDataURL(imageURL)
	}
	data, contentType, _, err = fetchWechatQRURL(ctx, client, imageURL, finalURL)
	return data, contentType, err
}

func fetchWechatQRURL(ctx context.Context, client *http.Client, source, referer string) ([]byte, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, "", "", err
	}
	for k, values := range wechat.CommonHeaders() {
		for _, value := range values {
			req.Header.Add(k, value)
		}
	}
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,text/html,*/*;q=0.8")
	req.Header.Set("Referer", referer)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) MothX-Serve Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", "", fmt.Errorf("QR upstream returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, "", "", err
	}
	contentType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	finalURL := source
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return data, contentType, finalURL, nil
}

func isHTMLResponse(contentType string, data []byte) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if contentType == "text/html" || contentType == "application/xhtml+xml" {
		return true
	}
	sample := strings.ToLower(strings.TrimSpace(string(data[:min(len(data), 512)])))
	return strings.HasPrefix(sample, "<!doctype html") || strings.HasPrefix(sample, "<html")
}

func extractQRCodeImageURL(data []byte, baseURL string) (string, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	candidates := make([]string, 0, 4)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "img", "source":
				for _, key := range []string{"src", "data-src", "data-original", "data-url"} {
					if value := attrValue(n, key); value != "" {
						candidates = append(candidates, value)
					}
				}
			case "meta":
				property := strings.ToLower(attrValue(n, "property"))
				name := strings.ToLower(attrValue(n, "name"))
				if property == "og:image" || name == "twitter:image" {
					if value := attrValue(n, "content"); value != "" {
						candidates = append(candidates, value)
					}
				}
			case "link":
				if strings.Contains(strings.ToLower(attrValue(n, "rel")), "image") {
					if value := attrValue(n, "href"); value != "" {
						candidates = append(candidates, value)
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	for _, candidate := range candidates {
		resolved, ok := resolveQRCodeImageURL(candidate, baseURL)
		if ok {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("no QR image found")
}

func attrValue(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func resolveQRCodeImageURL(raw, baseURL string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(strings.ToLower(raw), "javascript:") {
		return "", false
	}
	if strings.HasPrefix(raw, "data:image/") {
		return raw, true
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	return base.ResolveReference(u).String(), true
}

func decodeQRDataURL(source string) ([]byte, string, error) {
	header, body, ok := strings.Cut(source, ",")
	if !ok {
		return nil, "", fmt.Errorf("invalid QR data URL")
	}
	contentType := strings.TrimPrefix(strings.Split(header, ";")[0], "data:")
	if contentType == "" {
		contentType = "image/png"
	}
	data, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(body)
	}
	if err != nil {
		return nil, "", err
	}
	return data, contentType, nil
}

func serveInlineQR(w http.ResponseWriter, source string) {
	data, contentType, err := decodeInlineQR(source)
	if err != nil {
		data = []byte(source)
		contentType = http.DetectContentType(data)
		if strings.HasPrefix(strings.TrimSpace(source), "<svg") {
			contentType = "image/svg+xml"
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func decodeInlineQR(source string) ([]byte, string, error) {
	contentType := "image/png"
	payload := source
	if strings.HasPrefix(source, "data:") {
		header, body, ok := strings.Cut(source, ",")
		if !ok {
			return nil, "", fmt.Errorf("invalid QR data URL")
		}
		payload = body
		if media := strings.TrimPrefix(strings.Split(header, ";")[0], "data:"); media != "" {
			contentType = media
		}
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(payload)
	}
	return data, contentType, err
}

func qrOpenURL(source, fallback string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return fallback
	}
	if strings.HasPrefix(source, "//") {
		return "https:" + source
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "data:") {
		return source
	}
	if _, err := base64.StdEncoding.DecodeString(source); err == nil {
		return "data:image/png;base64," + source
	}
	return fallback
}

func (rt *channelRuntime) wechatLoginSnapshot() wechatLoginStatus {
	cfg := rt.configSnapshot()
	enabled := cfg != nil && cfg.Channels.Wechat.Enabled
	if current := rt.currentWechatLogin(); current != nil {
		return current.snapshot(enabled)
	}
	credPath := ""
	if rt != nil {
		credPath = rt.wechatCredPath()
	}
	if credPath != "" {
		if creds, err := wechat.LoadCredentials(credPath); err == nil && creds != nil {
			return wechatLoginStatus{
				State:    "confirmed",
				UserID:   creds.UserID,
				Enabled:  enabled,
				LoggedIn: true,
			}
		}
	}
	return wechatLoginStatus{State: "idle", Enabled: enabled}
}

func (rt *channelRuntime) runWechatLogin(ctx context.Context, sess *wechatLoginSession, configPath, credPath string) {
	client := wechat.NewClient()
	creds, err := wechat.Login(ctx, client, wechat.LoginOptions{
		BaseURL:  wechat.DefaultBaseURL,
		CredPath: credPath,
		Force:    true,
		OnQRURL: func(url string) {
			sess.update("pending", func() {
				sess.qrURL = url
				sess.err = ""
			})
		},
		OnScanned: func() {
			sess.update("scanned", nil)
		},
		OnExpired: func() {
			sess.update("expired", nil)
		},
	})
	if err != nil {
		state := "error"
		if ctx.Err() != nil {
			state = "cancelled"
		}
		sess.update(state, func() {
			sess.err = err.Error()
		})
		return
	}
	if creds == nil {
		sess.update("error", func() {
			sess.err = "login returned empty credentials"
		})
		return
	}
	if err := rt.enableWechatAfterLogin(configPath, credPath); err != nil {
		sess.update("error", func() {
			sess.userID = creds.UserID
			sess.err = err.Error()
		})
		return
	}
	sess.update("confirmed", func() {
		sess.userID = creds.UserID
		sess.err = ""
	})
}

func (rt *channelRuntime) enableWechatAfterLogin(configPath, credPath string) error {
	cfg := rt.configSnapshot()
	if rt == nil || cfg == nil {
		return nil
	}
	path := configPath
	state := rt.configStateSnapshot(path, ConfigLayerExplicit)
	if state != nil {
		path = state.WritablePath
	}
	configuredCredPath := cfg.Channels.Wechat.CredPath
	if configuredCredPath == "" && credPath != defaultWechatCredPath() {
		configuredCredPath = credPath
	}
	body, err := json.Marshal(map[string]any{
		"enabled": true, "credPath": configuredCredPath, "workDir": cfg.Channels.Wechat.WorkDir,
		"autoTyping": cfg.Channels.Wechat.AutoTyping,
	})
	if err != nil {
		return err
	}
	result, err := state.UpdateChannel("wechat", body, rt.applyConfigUpdate)
	if err == nil {
		rt.publishManagementEvent("channel_config_changed", map[string]any{
			"platform": "wechat", "layer": result.Layer, "path": result.Path,
			"source": "wechat_login", "restart": result.Restart,
		})
	}
	return err
}

func (rt *channelRuntime) wechatCredPath() string {
	if cfg := rt.configSnapshot(); cfg != nil && cfg.Channels.Wechat.CredPath != "" {
		return cfg.Channels.Wechat.CredPath
	}
	return defaultWechatCredPath()
}

func defaultWechatCredPath() string {
	return filepath.Join(config.ConfigDir(), "wechat-credentials.json")
}

func (rt *channelRuntime) syncPlatformRuntime(previous, next *Config) error {
	if rt == nil {
		return nil
	}
	for _, name := range []string{"wechat", "feishu"} {
		if platformTransportChanged(previous, next, name) {
			if err := rt.restartPlatform(name, next); err != nil {
				return err
			}
		}
	}
	return nil
}
