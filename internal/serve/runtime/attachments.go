package runtime

import (
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/provider"
)

// FormatAttachmentSummary renders provider-neutral attachment references.
func FormatAttachmentSummary(items []provider.Attachment) string {
	if len(items) == 0 {
		return ""
	}
	lines := []string{"Attachments:"}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		label := strings.TrimSpace(item.Name)
		if label == "" {
			label = strings.TrimSpace(item.Kind)
		}
		if label == "" {
			label = "attachment"
		}
		target := strings.TrimSpace(item.URL)
		if target == "" {
			target = strings.TrimSpace(item.ProviderRef)
		}
		if target == "" {
			continue
		}
		key := label + "\x00" + target
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		lines = append(lines, fmt.Sprintf("- %s: %s", label, target))
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}
