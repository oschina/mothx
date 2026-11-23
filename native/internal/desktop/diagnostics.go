package desktop

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

// diagnosticLog is the Native main-process diagnostic ring. It is distinct
// from ACP stderr: it records shell launch/connection/request lifecycle facts
// that Electron's diagnostic-logs view also exposes on Desktop. It is local
// presentation state and never becomes Runtime/session truth.

const maxDiagnosticEntries = 200

// nativeStartupError is the structured failure the ACP child prints on stderr
// with the MOTHX_ACP_ERROR marker. Desktop parses the same marker so the user
// sees the Runtime's message and fix instead of a generic process exit.
type nativeStartupError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

func parseNativeStartupErrorLine(line string) (nativeStartupError, bool) {
	const marker = "MOTHX_ACP_ERROR "
	index := strings.Index(line, marker)
	if index < 0 {
		return nativeStartupError{}, false
	}
	var parsed nativeStartupError
	if err := json.Unmarshal([]byte(strings.TrimSpace(line[index+len(marker):])), &parsed); err != nil {
		return nativeStartupError{}, false
	}
	if strings.TrimSpace(parsed.Message) == "" {
		return nativeStartupError{}, false
	}
	if parsed.Code == "" {
		parsed.Code = "acp_startup"
	}
	return parsed, true
}

type diagnosticEntry struct {
	At    time.Time
	Level string
	Text  string
}

var nativeDiagnostics = struct {
	mu      sync.Mutex
	entries []diagnosticEntry
}{}

func addDiagnostic(level, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	nativeDiagnostics.mu.Lock()
	defer nativeDiagnostics.mu.Unlock()
	nativeDiagnostics.entries = append(nativeDiagnostics.entries, diagnosticEntry{At: time.Now(), Level: level, Text: text})
	if len(nativeDiagnostics.entries) > maxDiagnosticEntries {
		nativeDiagnostics.entries = nativeDiagnostics.entries[len(nativeDiagnostics.entries)-maxDiagnosticEntries:]
	}
}

func clearDiagnostics() {
	nativeDiagnostics.mu.Lock()
	nativeDiagnostics.entries = nil
	nativeDiagnostics.mu.Unlock()
}

func diagnosticSnapshot() []diagnosticEntry {
	nativeDiagnostics.mu.Lock()
	defer nativeDiagnostics.mu.Unlock()
	out := make([]diagnosticEntry, len(nativeDiagnostics.entries))
	copy(out, nativeDiagnostics.entries)
	return out
}

func filterDiagnostics(entries []diagnosticEntry, filter string) []diagnosticEntry {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return entries
	}
	out := make([]diagnosticEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Text), filter) || strings.Contains(strings.ToLower(entry.Level), filter) {
			out = append(out, entry)
		}
	}
	return out
}

func diagnosticLine(entry diagnosticEntry) string {
	return fmt.Sprintf("%s [%s] %s", entry.At.Format("15:04:05"), entry.Level, entry.Text)
}

func (a *nativeApp) diagnosticLogView(c *ui.Context, p palette) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.TextInput(c.Key("diagnostics-filter"), &a.diagnosticsFilter).FontSize(11).Height(30).
				Padding(0, 8).Placeholder(T("过滤诊断日志")).
				Background(p.T.Background).Radius(8).Border(1, p.T.Border).Grow(1)
			if ui.Button(c.Key("diagnostics-clear"), T("清空")).Clicked() {
				clearDiagnostics()
			}
			if ui.Button(c.Key("diagnostics-refresh"), T("刷新")).Clicked() {
				addDiagnostic("info", "diagnostics refreshed from the Runtime settings view")
			}
		})
		entries := filterDiagnostics(diagnosticSnapshot(), a.diagnosticsFilter)
		if len(entries) == 0 {
			ui.Text(c, T("没有诊断日志。")).FontSize(10.5).TextColor(p.T.TextMuted)
			return
		}
		if len(entries) > 60 {
			entries = entries[len(entries)-60:]
		}
		ui.Scroll(c).Height(180).Padding(8, 10).Radius(8).
			Background(p.T.Background).Border(1, p.T.Border).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				for _, entry := range entries {
					ui.Text(c, diagnosticLine(entry)).Font("monospace").FontSize(9).
						TextColor(p.T.TextMuted).Selectable()
				}
			})
		})
	})
}
