package session

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/oschina/mothx/internal/provider"
)

// normalizeSessionEntry repairs provider messages at the persistence boundary.
// This is intentionally a last line of defense: callers should normalize as
// soon as a streamed tool call is received, but Runtime-owned durable writes
// must remain safe even when a message arrived through another adapter.
func normalizeSessionEntry(entry any) any {
	switch value := entry.(type) {
	case MessageEntry:
		value.Message, _ = provider.NormalizeMessage(value.Message)
		return value
	case *MessageEntry:
		if value == nil {
			return entry
		}
		copyValue := *value
		copyValue.Message, _ = provider.NormalizeMessage(copyValue.Message)
		return &copyValue
	case ContentOverrideEntry:
		value.Message, _ = provider.NormalizeMessage(value.Message)
		return value
	case *ContentOverrideEntry:
		if value == nil {
			return entry
		}
		copyValue := *value
		copyValue.Message, _ = provider.NormalizeMessage(copyValue.Message)
		return &copyValue
	default:
		return entry
	}
}

// marshalSessionEntry retries a failed JSON-v2 marshal after applying the
// message repair above. The first marshal is deliberate: it preserves the
// original error for diagnostics and keeps this fallback focused on malformed
// streamed payloads rather than masking unrelated schema bugs.
func marshalSessionEntry(entry any) ([]byte, error) {
	data, err := json.Marshal(entry)
	if err == nil {
		return data, nil
	}

	repaired := normalizeSessionEntry(entry)
	if retryData, retryErr := json.Marshal(repaired); retryErr == nil {
		log.Printf("[session] repaired an invalid message payload after marshal failure: %v", err)
		return retryData, nil
	}
	return nil, fmt.Errorf("marshal entry: %w", err)
}
