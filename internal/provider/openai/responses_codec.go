package openai

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/oschina/mothx/internal/provider"
)

const (
	responsesMaxCanonicalItemBytes = 128 * 1024
	responsesMaxMetadataBytes      = 16 * 1024
)

var (
	errResponsesStop                   = errors.New("responses stream reached terminal event")
	errResponsesAbort                  = errors.New("responses stream aborted")
	errResponsesComputerUseUnsupported = errors.New("Responses computer use is not supported by this version")
)

type responsesSSEFrame struct {
	Event    string
	ID       string
	Data     string
	Sequence int64
}

// decodeResponsesSSE implements the SSE framing rules used by Responses API.
// It deliberately leaves JSON decoding to the schema-aware event normalizer.
func decodeResponsesSSE(r io.Reader, fn func(responsesSSEFrame) error) error {
	reader := bufio.NewReader(r)
	var (
		eventName string
		eventID   string
		data      []string
		sequence  int64
	)

	dispatch := func() error {
		if len(data) == 0 {
			eventName = ""
			eventID = ""
			return nil
		}
		sequence++
		frame := responsesSSEFrame{
			Event:    eventName,
			ID:       eventID,
			Data:     strings.Join(data, "\n"),
			Sequence: sequence,
		}
		eventName = ""
		eventID = ""
		data = nil
		return fn(frame)
	}

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				if dispatchErr := dispatch(); dispatchErr != nil {
					return dispatchErr
				}
			} else if strings.HasPrefix(line, ":") {
				// SSE comments are keep-alive frames.
				continue
			} else {
				field, value := line, ""
				if colon := strings.IndexByte(line, ':'); colon >= 0 {
					field, value = line[:colon], line[colon+1:]
					if strings.HasPrefix(value, " ") {
						value = value[1:]
					}
				}
				switch field {
				case "event":
					eventName = value
				case "id":
					eventID = value
				case "data":
					data = append(data, value)
					// A number of OpenAI-compatible gateways emit one
					// complete JSON event per line without the blank-line
					// separator required by SSE. Preserve that legacy
					// behavior while still supporting multi-line data frames.
					if eventName == "" && eventID == "" {
						joined := strings.Join(data, "\n")
						if joined == "[DONE]" || json.Valid([]byte(joined)) {
							if dispatchErr := dispatch(); dispatchErr != nil {
								return dispatchErr
							}
						}
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return dispatch()
			}
			return err
		}
	}
}

type responsesResponseItem struct {
	ID          string
	Type        string
	Status      string
	OutputIndex int
	CallID      string
	Name        string
	Arguments   json.RawMessage
	Input       string
	Canonical   json.RawMessage
}

type responsesNormalizedResponse struct {
	ID                   string
	Status               string
	PreviousResponseID   string
	ConversationID       string
	IncompleteReason     string
	Usage                *responsesUsage
	Error                *responsesError
	Items                []*responsesResponseItem
	UnknownItems         int
	UnknownEventTypes    []string
	UnsupportedItemTypes []string
}

type responsesNormalizer struct {
	items          map[string]*responsesResponseItem
	itemOrder      []string
	argumentBytes  map[string]*strings.Builder
	response       responsesNormalizedResponse
	hostedPolicies map[string]responsesHostedPolicy
}

func newResponsesNormalizer() *responsesNormalizer {
	return &responsesNormalizer{
		items:         make(map[string]*responsesResponseItem),
		argumentBytes: make(map[string]*strings.Builder),
	}
}

func (n *responsesNormalizer) apply(event responsesSSEEvent, raw []byte) error {
	if event.Response != nil {
		n.applyResponse(event.Response)
	}

	switch event.Type {
	case "response.created", "response.queued", "response.in_progress",
		"response.content_part.added", "response.content_part.done",
		"response.output_text.delta", "response.output_text.done",
		"response.refusal.delta", "response.refusal.done",
		"response.reasoning_summary_part.added", "response.reasoning_summary_text.delta",
		"response.reasoning_summary_part.done", "response.reasoning_summary_text.done", "response.reasoning_text.delta",
		"response.reasoning_text.done", "response.completed", "response.incomplete",
		"response.failed", "error":
		// These events are handled by the stream parser or response envelope;
		// keeping them explicit prevents them from being reported as unknown.
	case "response.output_item.added", "response.output_item.done":
		if event.Item == nil {
			return fmt.Errorf("responses event %q is missing item", event.Type)
		}
		item, err := n.upsertItem(event.Item, event.OutputIndex, responsesEventItemRaw(raw))
		if err != nil {
			return err
		}
		if isUnsupportedResponsesItemType(item.Type) {
			n.recordUnsupportedItemType(item.Type)
		}
		if event.Type == "response.output_item.done" && item.Type == "function_call" {
			if len(item.Arguments) == 0 {
				item.Arguments = n.arguments(event.Item.ID, event.OutputIndex)
			}
		}
	case "response.function_call_arguments.delta":
		key, err := responsesEventItemKey(event.ItemID, event.OutputIndex)
		if err != nil {
			return err
		}
		if event.Delta != "" {
			if n.argumentBytes[key] == nil {
				n.argumentBytes[key] = &strings.Builder{}
			}
			n.argumentBytes[key].WriteString(event.Delta)
		}
		item := n.ensureItem(event.ItemID, event.OutputIndex)
		if item.Type == "" {
			item.Type = "function_call"
		}
		item.Arguments = n.arguments(event.ItemID, event.OutputIndex)
	case "response.function_call_arguments.done":
		key, err := responsesEventItemKey(event.ItemID, event.OutputIndex)
		if err != nil {
			return err
		}
		if len(event.Arguments) > 0 {
			if n.argumentBytes[key] == nil {
				n.argumentBytes[key] = &strings.Builder{}
			}
			n.argumentBytes[key].Reset()
			n.argumentBytes[key].WriteString(responsesArgumentsText(event.Arguments))
		}
		item := n.ensureItem(event.ItemID, event.OutputIndex)
		if item.Type == "" {
			item.Type = "function_call"
		}
		item.Arguments = n.arguments(event.ItemID, event.OutputIndex)
	case "response.custom_tool_call_input.delta":
		key, err := responsesEventItemKey(event.ItemID, event.OutputIndex)
		if err != nil {
			return err
		}
		if event.Delta != "" {
			if n.argumentBytes[key] == nil {
				n.argumentBytes[key] = &strings.Builder{}
			}
			n.argumentBytes[key].WriteString(event.Delta)
		}
		item := n.ensureItem(event.ItemID, event.OutputIndex)
		if item.Type == "" {
			item.Type = "custom_tool_call"
		}
		item.Input = string(n.arguments(event.ItemID, event.OutputIndex))
	case "response.custom_tool_call_input.done":
		key, err := responsesEventItemKey(event.ItemID, event.OutputIndex)
		if err != nil {
			return err
		}
		if event.Input != "" {
			if n.argumentBytes[key] == nil {
				n.argumentBytes[key] = &strings.Builder{}
			}
			n.argumentBytes[key].Reset()
			n.argumentBytes[key].WriteString(event.Input)
		}
		item := n.ensureItem(event.ItemID, event.OutputIndex)
		if item.Type == "" {
			item.Type = "custom_tool_call"
		}
		item.Input = string(n.arguments(event.ItemID, event.OutputIndex))
	default:
		n.recordUnknownEventType(event.Type)
	}

	if event.Type == "response.completed" || event.Type == "response.incomplete" ||
		event.Type == "response.failed" {
		n.applyResponse(event.Response)
	}
	return nil
}

func (n *responsesNormalizer) recordUnknownEventType(eventType string) {
	if n == nil || eventType == "" {
		return
	}
	for _, existing := range n.response.UnknownEventTypes {
		if existing == eventType {
			return
		}
	}
	if len(n.response.UnknownEventTypes) >= 16 {
		return
	}
	n.response.UnknownEventTypes = append(n.response.UnknownEventTypes, eventType)
}

func (n *responsesNormalizer) applyResponse(response *responsesCompletedObject) {
	if response == nil {
		return
	}
	n.response.ID = response.ID
	n.response.Status = response.Status
	n.response.PreviousResponseID = response.PreviousResponseID
	n.response.ConversationID = responsesConversationID(response)
	n.response.Usage = response.Usage
	n.response.Error = response.Error
	if response.IncompleteDetails != nil {
		n.response.IncompleteReason = response.IncompleteDetails.Reason
	}
	for index, rawItem := range response.Output {
		item, err := decodeResponsesOutputItem(rawItem, index)
		if err != nil {
			continue
		}
		n.upsertDecodedItem(item)
		if isUnsupportedResponsesItemType(item.Type) {
			n.recordUnsupportedItemType(item.Type)
		}
	}
}

func responsesConversationID(response *responsesCompletedObject) string {
	if response == nil {
		return ""
	}
	if response.ConversationID != "" {
		return response.ConversationID
	}
	if len(response.ConversationRaw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(response.ConversationRaw, &text) == nil {
		return text
	}
	var value struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(response.ConversationRaw, &value) == nil {
		return value.ID
	}
	return ""
}

func (n *responsesNormalizer) upsertItem(item *responsesOutputItem, outputIndex int, raw []byte) (*responsesResponseItem, error) {
	decoded := &responsesResponseItem{
		ID:          item.ID,
		Type:        item.Type,
		Status:      item.Status,
		OutputIndex: outputIndex,
		CallID:      item.CallID,
		Name:        item.Name,
		Arguments:   responsesArgumentsRaw(item.Arguments),
		Input:       item.Input,
		Canonical:   canonicalResponsesJSON(raw, responsesMaxCanonicalItemBytes),
	}
	n.upsertDecodedItem(decoded)
	return decoded, nil
}

func (n *responsesNormalizer) upsertDecodedItem(item *responsesResponseItem) {
	key := responsesToolKey(item.ID, item.OutputIndex)
	// Some gateways omit item.id in response.completed.output even though the
	// streaming output_item.added event included it. Output indexes are unique
	// within a response, so merge that completion snapshot into the streamed
	// item instead of archiving/replaying it as a second item.
	if item.ID == "" {
		for existingKey, existing := range n.items {
			if existing != nil && existing.OutputIndex == item.OutputIndex {
				key = existingKey
				break
			}
		}
	}
	if existing := n.items[key]; existing != nil {
		if item.ID != "" {
			existing.ID = item.ID
		}
		if item.Type != "" {
			existing.Type = item.Type
		}
		if item.Status != "" {
			existing.Status = item.Status
		}
		if item.CallID != "" {
			existing.CallID = item.CallID
		}
		if item.Name != "" {
			existing.Name = item.Name
		}
		if len(item.Arguments) > 0 {
			existing.Arguments = cloneRawMessage(item.Arguments)
		}
		if item.Input != "" {
			existing.Input = item.Input
		}
		if len(item.Canonical) > 0 {
			existing.Canonical = cloneRawMessage(item.Canonical)
		}
		return
	}
	n.items[key] = item
	n.itemOrder = append(n.itemOrder, key)
	n.response.Items = append(n.response.Items, item)
	if !isKnownResponsesItemType(item.Type) {
		n.response.UnknownItems++
	}
	if isUnsupportedResponsesItemType(item.Type) {
		n.recordUnsupportedItemType(item.Type)
	}
}

func (n *responsesNormalizer) recordUnsupportedItemType(itemType string) {
	if itemType == "" {
		return
	}
	for _, existing := range n.response.UnsupportedItemTypes {
		if existing == itemType {
			return
		}
	}
	n.response.UnsupportedItemTypes = append(n.response.UnsupportedItemTypes, itemType)
}

func (n *responsesNormalizer) ensureItem(itemID string, outputIndex int) *responsesResponseItem {
	key := responsesToolKey(itemID, outputIndex)
	if item := n.items[key]; item != nil {
		return item
	}
	item := &responsesResponseItem{ID: itemID, OutputIndex: outputIndex}
	n.upsertDecodedItem(item)
	return item
}

func (n *responsesNormalizer) arguments(itemID string, outputIndex int) json.RawMessage {
	key := responsesToolKey(itemID, outputIndex)
	if buffer := n.argumentBytes[key]; buffer != nil {
		return json.RawMessage(buffer.String())
	}
	return nil
}

func (n *responsesNormalizer) toolCalls() []providerToolCall {
	calls := make([]providerToolCall, 0)
	for _, key := range n.itemOrder {
		item := n.items[key]
		if item == nil || (item.Type != "function_call" && item.Type != "custom_tool_call") {
			continue
		}
		arguments := cloneRawMessage(item.Arguments)
		input := item.Input
		kind := "function"
		if item.Type == "custom_tool_call" {
			kind = "custom"
			if input == "" {
				input = string(n.arguments(item.ID, item.OutputIndex))
			}
			arguments, _ = json.Marshal(map[string]string{"input": input})
		} else if len(arguments) == 0 {
			arguments = n.arguments(item.ID, item.OutputIndex)
		}
		calls = append(calls, providerToolCall{
			Key:       key,
			ID:        item.CallID,
			ItemID:    item.ID,
			Name:      item.Name,
			Kind:      kind,
			Input:     input,
			Arguments: arguments,
		})
	}
	return calls
}

type providerToolCall struct {
	Key       string
	ID        string
	ItemID    string
	Name      string
	Kind      string
	Input     string
	Arguments json.RawMessage
}

func (n *responsesNormalizer) metadata() map[string]any {
	metadata := map[string]any{
		"itemCount": len(n.response.Items),
	}
	if n.response.UnknownItems > 0 {
		metadata["unknownItemCount"] = n.response.UnknownItems
	}
	if len(n.response.UnknownEventTypes) > 0 {
		metadata["unknownEventTypes"] = append([]string(nil), n.response.UnknownEventTypes...)
	}
	if len(n.response.UnsupportedItemTypes) > 0 {
		metadata["unsupportedItemTypes"] = append([]string(nil), n.response.UnsupportedItemTypes...)
		metadata["computerUseRejected"] = true
	}
	if n.response.ID != "" {
		metadata["responseId"] = n.response.ID
	}
	if n.response.Status != "" {
		metadata["responseStatus"] = n.response.Status
	}
	if n.response.PreviousResponseID != "" {
		metadata["previousResponseId"] = n.response.PreviousResponseID
	}
	if n.response.ConversationID != "" {
		metadata["conversationId"] = n.response.ConversationID
	}
	if n.response.IncompleteReason != "" {
		metadata["incompleteReason"] = n.response.IncompleteReason
	}
	return limitResponsesMetadata(metadata)
}

func (n *responsesNormalizer) unsupportedError() error {
	if n == nil || len(n.response.UnsupportedItemTypes) == 0 {
		return nil
	}
	return fmt.Errorf("%w: item type %q", errResponsesComputerUseUnsupported, n.response.UnsupportedItemTypes[0])
}

func (n *responsesNormalizer) hostedPolicyError() error {
	if n == nil {
		return nil
	}
	policy := n.hostedPolicies["code_interpreter"]
	if !policy.Configured || !policy.MaxCallsSet {
		return nil
	}
	count := 0
	for _, item := range n.response.Items {
		if item != nil && item.Type == "code_interpreter_call" {
			count++
		}
	}
	if count > policy.MaxCalls {
		return fmt.Errorf("code interpreter call quota exceeded: %d calls (limit %d)", count, policy.MaxCalls)
	}
	return nil
}

func (n *responsesNormalizer) attachments() []provider.Attachment {
	seen := make(map[string]struct{})
	var attachments []provider.Attachment
	for _, item := range n.response.Items {
		if item == nil || len(item.Canonical) == 0 {
			continue
		}
		var value any
		if json.Unmarshal(item.Canonical, &value) != nil {
			continue
		}
		// The registry is consulted here as an observation boundary. It does
		// not reject unknown types; canonical archive preservation remains
		// forward-compatible with newer upstream hosted items.
		collectResponsesAttachments(value, item.ID, item.Type, item.Status, "", "", seen, &attachments)
	}
	return attachments
}

// collectResponsesAttachments only exposes references from known output
// contexts. In particular, it does not turn arbitrary URLs (for example a
// remote MCP server URL) into clickable UI attachments.
func collectResponsesAttachments(value any, itemID, rootType, status, parentType, inheritedContainerID string, seen map[string]struct{}, out *[]provider.Attachment) {
	// A remote MCP server controls its own output payload. Do not convert its
	// nested URLs into browser-visible attachments before a dedicated MCP
	// lifecycle has applied its egress and provenance policies. The registry is
	// intentionally advisory: unknown future item types remain archivable.
	if descriptor, ok := hostedToolDescriptorForType(rootType); ok && len(descriptor.AttachmentKinds) == 0 {
		return
	}
	switch typed := value.(type) {
	case []any:
		for _, nested := range typed {
			collectResponsesAttachments(nested, itemID, rootType, status, parentType, inheritedContainerID, seen, out)
		}
	case map[string]any:
		containerID := inheritedContainerID
		if current, ok := typed["container_id"].(string); ok && current != "" {
			containerID = current
		}
		itemType, _ := typed["type"].(string)
		if itemType == "" {
			itemType = parentType
		}
		lowerType := strings.ToLower(itemType)
		if lowerType == "code_interpreter_call" {
			if containerID, ok := typed["container_id"].(string); ok && containerID != "" && itemID != "" {
				key := "artifact\x00" + itemID + "\x00" + containerID
				if _, ok := seen[key]; !ok {
					seen[key] = struct{}{}
					*out = append(*out, provider.Attachment{
						Kind: "artifact", Name: "Code Interpreter container", ProviderRef: containerID,
						Metadata: responsesAttachmentMetadata(typed, itemID, rootType, status, map[string]any{
							"tool": "code_interpreter",
						}),
					})
				}
			}
		}
		if lowerType == "image_generation_call" {
			if encoded, ok := typed["result"].(string); ok && encoded != "" && itemID != "" {
				sum := sha256.Sum256([]byte(encoded))
				key := "image\x00" + itemID + "\x00" + hex.EncodeToString(sum[:])
				if _, ok := seen[key]; !ok {
					seen[key] = struct{}{}
					*out = append(*out, provider.Attachment{
						Kind: "image", Name: "generated image", MediaType: "image/png", ProviderRef: itemID,
						Metadata: responsesAttachmentMetadata(typed, itemID, rootType, status, map[string]any{
							"sha256": hex.EncodeToString(sum[:]), "encodedBytes": len(encoded),
						}),
					})
				}
			}
		}
		kind := ""
		switch {
		case strings.Contains(lowerType, "file") || strings.Contains(lowerType, "container"):
			kind = "file"
		case strings.Contains(lowerType, "citation") || strings.Contains(lowerType, "search"):
			kind = "citation"
		case strings.Contains(lowerType, "image"):
			kind = "image"
		}
		if kind != "" {
			attachmentURL, _ := typed["url"].(string)
			if attachmentURL == "" {
				attachmentURL, _ = typed["file_url"].(string)
			}
			attachmentURL = safeResponsesAttachmentURL(attachmentURL)
			name, _ := typed["title"].(string)
			if name == "" {
				name, _ = typed["filename"].(string)
			}
			ref, _ := typed["file_id"].(string)
			if ref == "" {
				ref, _ = typed["container_id"].(string)
			}
			// Keep text annotations even when a gateway omits the URL or file
			// reference. Their offsets/title/type remain useful provenance and
			// allow a later provider-specific resolver to enrich the citation.
			_, hasStart := typed["start_index"]
			_, hasEnd := typed["end_index"]
			if attachmentURL != "" || ref != "" || (kind == "citation" && (hasStart || hasEnd)) {
				key := kind + "\x00" + attachmentURL + "\x00" + ref
				if _, ok := seen[key]; !ok {
					seen[key] = struct{}{}
					extra := map[string]any{
						"annotationType": itemType,
						"title":          name,
					}
					if containerID != "" {
						extra["containerId"] = containerID
					}
					metadata := responsesAttachmentMetadata(typed, itemID, rootType, status, extra)
					*out = append(*out, provider.Attachment{
						Kind: kind, Name: name, URL: attachmentURL, ProviderRef: ref,
						Metadata: metadata,
					})
				}
			}
		}
		for _, nested := range typed {
			collectResponsesAttachments(nested, itemID, rootType, status, itemType, containerID, seen, out)
		}
	}
}

// safeResponsesAttachmentURL keeps provider output from becoming an active
// browser target unless it is a credential-free HTTPS URL. References without
// a safe URL are still archived through ProviderRef for audit and future
// provider-specific retrieval.
func safeResponsesAttachmentURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return ""
	}
	return parsed.String()
}

// responsesAttachmentMetadata retains only stable output provenance. It is
// deliberately derived from canonical, redacted items rather than arbitrary
// remote MCP fields or raw tool payloads.
func responsesAttachmentMetadata(value map[string]any, itemID, itemType, status string, extra map[string]any) map[string]any {
	metadata := make(map[string]any, 6+len(extra))
	if itemID != "" {
		metadata["responseItemId"] = itemID
	}
	if itemType != "" {
		metadata["responseItemType"] = itemType
	}
	if status != "" {
		metadata["status"] = status
	}
	if containerID, ok := value["container_id"].(string); ok && containerID != "" {
		metadata["containerId"] = containerID
	}
	for _, field := range []string{"score", "start_index", "end_index"} {
		if fieldValue, ok := value[field]; ok {
			metadata[field] = fieldValue
		}
	}
	for key, fieldValue := range extra {
		metadata[key] = fieldValue
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func responsesEventItemKey(itemID string, outputIndex int) (string, error) {
	if itemID == "" && outputIndex < 0 {
		return "", fmt.Errorf("responses event is missing item identity")
	}
	return responsesToolKey(itemID, outputIndex), nil
}

func decodeResponsesOutputItem(raw json.RawMessage, outputIndex int) (*responsesResponseItem, error) {
	var item struct {
		ID        string          `json:"id"`
		Type      string          `json:"type"`
		Status    string          `json:"status"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Input     string          `json:"input"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, err
	}
	if item.Type == "" {
		return &responsesResponseItem{
			ID:          item.ID,
			OutputIndex: outputIndex,
			Canonical:   canonicalResponsesJSON(raw, responsesMaxCanonicalItemBytes),
		}, nil
	}
	return &responsesResponseItem{
		ID:          item.ID,
		Type:        item.Type,
		Status:      item.Status,
		OutputIndex: outputIndex,
		CallID:      item.CallID,
		Name:        item.Name,
		Arguments:   responsesArgumentsRaw(item.Arguments),
		Input:       item.Input,
		Canonical:   canonicalResponsesJSON(raw, responsesMaxCanonicalItemBytes),
	}, nil
}

func responsesArgumentsRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return json.RawMessage(text)
	}
	return cloneRawMessage(raw)
}

func responsesArgumentsText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}

func responsesEventItemRaw(raw []byte) []byte {
	var envelope struct {
		Item json.RawMessage `json:"item"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Item) == 0 {
		return raw
	}
	return envelope.Item
}

func isKnownResponsesItemType(itemType string) bool {
	switch itemType {
	case "message", "reasoning", "function_call", "function_call_output",
		"custom_tool_call", "custom_tool_call_output", "item_reference",
		"web_search_call", "file_search_call", "code_interpreter_call", "image_generation_call",
		"mcp_call", "mcp_call_output":
		return true
	default:
		return false
	}
}

func isUnsupportedResponsesItemType(itemType string) bool {
	switch itemType {
	case "computer_call", "computer_call_output":
		return true
	default:
		return false
	}
}

func canonicalResponsesJSON(raw []byte, maxBytes int) json.RawMessage {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil
	}
	value = redactResponsesValue(value)
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	if len(canonical) <= maxBytes {
		return canonical
	}
	sum := sha256.Sum256(canonical)
	truncated, _ := json.Marshal(map[string]any{
		"_truncated": true,
		"sha256":     hex.EncodeToString(sum[:]),
		"bytes":      len(canonical),
	})
	return truncated
}

func redactResponsesValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, nested := range typed {
			if isSensitiveResponsesKey(key) {
				result[key] = "[REDACTED]"
				continue
			}
			result[key] = redactResponsesValue(nested)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, nested := range typed {
			result[i] = redactResponsesValue(nested)
		}
		return result
	default:
		return value
	}
}

func isSensitiveResponsesKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	for _, marker := range []string{"authorization", "api_key", "apikey", "access_token", "refresh_token", "secret", "password", "cookie", "credential"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func limitResponsesMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	encoded, err := json.Marshal(metadata)
	if err == nil && len(encoded) <= responsesMaxMetadataBytes {
		return metadata
	}
	return map[string]any{"metadataTruncated": true}
}
