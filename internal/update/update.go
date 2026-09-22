// Package update provides non-blocking version update detection based on the
// npm registry. It never blocks the user: network checks run in the background
// and failures only affect the update notification.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/ua"
	"golang.org/x/mod/semver"
)

// PackageName is the npm package used for update detection.
const PackageName = "mothx"

// checkInterval is the minimum time between background network checks.
const checkInterval = 24 * time.Hour

var (
	fetchLatestVersion = fetchLatest
	now                = time.Now
)

// registryURL returns the npm registry endpoint for the latest dist-tag.
func registryURL() string {
	if u := os.Getenv("VIBECODING_NPM_REGISTRY"); u != "" {
		return strings.TrimRight(u, "/") + "/" + PackageName + "/latest"
	}
	return "https://registry.npmjs.org/" + PackageName + "/latest"
}

// cacheEntry is the on-disk cache for update checks.
type cacheEntry struct {
	CheckedAt time.Time `json:"checked_at"`
}

func cachePath() string {
	return filepath.Join(config.ConfigDir(), "update-check.json")
}

func readCache() cacheEntry {
	var c cacheEntry
	data, err := os.ReadFile(cachePath())
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, &c)
	return c
}

func writeCache(c cacheEntry) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Dir(cachePath())
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(cachePath(), data, 0o644)
}

// Notice returns the update reminder text for current and latest.
func Notice(current, latest string) string {
	return fmt.Sprintf(
		"✨ Update available: %s → %s\n   Run: npm install -g %s@latest",
		normalize(current), normalize(latest), PackageName,
	)
}

// CheckInBackground refreshes the cache in a background goroutine if it is
// stale. It returns immediately and never blocks the caller. When the fetched
// npm latest version is newer than current, notify is called from the background
// goroutine. Disable with the VIBECODING_NO_UPDATE_CHECK environment variable.
func CheckInBackground(current string, notify func(string)) {
	if !isCheckable(current) || checksDisabled() {
		return
	}
	c := readCache()
	if !c.CheckedAt.IsZero() && time.Since(c.CheckedAt) < checkInterval {
		return
	}
	go refreshCache(current, notify)
}

func refreshCache(current string, notify func(string)) {
	latest, err := fetchLatestVersion(context.Background())
	writeCache(cacheEntry{CheckedAt: now()})
	if err != nil {
		return
	}
	if compareVersions(latest, current) <= 0 {
		return
	}
	if notify != nil {
		notify(Notice(current, latest))
	}
}

// fetchLatest queries the npm registry for the latest published version.
func fetchLatest(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registryURL(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua.UserAgent())
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("npm registry: status %d", resp.StatusCode)
	}

	var payload struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Version == "" {
		return "", fmt.Errorf("npm registry: empty version")
	}
	return payload.Version, nil
}

func checksDisabled() bool {
	return os.Getenv("VIBECODING_NO_UPDATE_CHECK") != ""
}

// isCheckable reports whether current is a real release version worth checking.
func isCheckable(current string) bool {
	return semverVersion(current) != ""
}

// normalize strips a leading "v" and surrounding whitespace.
func normalize(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// compareVersions compares two semantic version strings. It returns -1 if a < b,
// 0 if equal, and 1 if a > b.
func compareVersions(a, b string) int {
	return semver.Compare(semverVersion(a), semverVersion(b))
}

func semverVersion(v string) string {
	v = normalize(v)
	if v == "" || strings.EqualFold(v, "dev") {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return semver.Canonical(v)
}
