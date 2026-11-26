package desktop

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

func (a *nativeApp) handleIncoming(client *acpClient, msg incoming) {
	switch msg.Method {
	case "session/update":
		var n sessionUpdateNotification
		if err := json.Unmarshal(msg.Params, &n); err != nil {
			return
		}
		a.dispatch(func() { a.applyUpdate(n) })
	case "_mothx/session_event":
		var ev sessionEvent
		if err := json.Unmarshal(msg.Params, &ev); err != nil {
			return
		}
		a.dispatch(func() { a.handleSessionEvent(ev) })
	case "mothx/worktree/status":
		var payload worktreeStatusPayload
		if err := json.Unmarshal(msg.Params, &payload); err != nil {
			return
		}
		a.dispatch(func() { a.applyWorktreeStatus(payload) })
	case "mothx/database/integrity":
		var payload databaseIntegrityPayload
		if err := json.Unmarshal(msg.Params, &payload); err != nil {
			return
		}
		a.dispatch(func() { a.applyDatabaseIntegrity(payload) })
	case "$/cancel_request":
		return
	default:
		if msg.HasID() {
			a.handleReverse(client, msg)
		}
	}
}

func (a *nativeApp) handleReverse(client *acpClient, msg incoming) {
	switch msg.Method {
	case "session/request_permission":
		var p permissionParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			if client != nil {
				_ = client.RespondError(msg.ID, -32602, "invalid permission params")
			}
			return
		}
		title := p.ToolCall.Title
		if title == "" {
			title = T("工具调用")
		}
		d := decision{
			ID:       append(json.RawMessage(nil), msg.ID...),
			Kind:     "permission",
			Session:  p.SessionID,
			Title:    title,
			Prompt:   permissionPrompt(p.ToolCall.RawInput),
			RawInput: p.ToolCall.RawInput,
		}
		for _, opt := range p.Options {
			d.Options = append(d.Options, decisionOption{ID: opt.OptionID, Label: permissionOptionLabel(opt.Name, opt.Kind), Kind: opt.Kind})
		}
		a.queueDecision(d)
	case "mothx/requestQuestion", "_mothx/request_question":
		var p questionParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			if client != nil {
				_ = client.RespondError(msg.ID, -32602, "invalid question params")
			}
			return
		}
		prompt := p.Prompt
		if prompt == "" {
			prompt = p.Question
		}
		title := p.Title
		if title == "" {
			title = "MothX 想确认一下"
		}
		d := decision{
			ID:      append(json.RawMessage(nil), msg.ID...),
			Kind:    "question",
			Session: p.SessionID,
			Title:   title,
			Prompt:  prompt,
			Multi:   p.Multi,
		}
		if p.TimeoutMs > 0 {
			d.Deadline = time.Now().Add(time.Duration(p.TimeoutMs) * time.Millisecond)
		}
		d.Options = parseQuestionOptions(p.Options)
		a.queueDecision(d)
	case "elicitation/create":
		var p elicitationParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			if client != nil {
				_ = client.RespondError(msg.ID, -32602, "invalid elicitation params")
			}
			return
		}
		d := buildElicitationDecision(msg.ID, p)
		a.queueDecision(d)
	default:
		if client != nil && msg.HasID() {
			_ = client.RespondError(msg.ID, -32601, "method not found: "+msg.Method)
		}
	}
}

// buildElicitationDecision projects one elicitation/create request into the
// inline decision card. The Runtime's form projection uses a single "answer"
// property, but a request replayed from a form-capable client may carry other
// shapes: prefer "answer", else the first property in sorted order, and accept
// both string and {id,label} enum entries instead of failing the whole request
// on a mixed schema.
func buildElicitationDecision(id json.RawMessage, p elicitationParams) decision {
	d := decision{
		ID:      append(json.RawMessage(nil), id...),
		Kind:    "elicitation",
		Session: p.SessionID,
		Title:   T("MothX 想确认一下"),
		Prompt:  p.Message,
		Elicit:  true,
	}
	names := make([]string, 0, len(p.Schema.Properties))
	for name := range p.Schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 1 {
		// A real multi-property form: one field per property, answered as
		// content keyed by property name instead of a single free text.
		for _, name := range names {
			prop := p.Schema.Properties[name]
			field := decisionField{Name: name, Title: prop.Title, Description: prop.Description}
			if field.Title == "" {
				field.Title = name
			}
			for _, raw := range prop.Enum {
				if option, ok := parseElicitationEnum(raw); ok {
					field.Options = append(field.Options, option)
				}
			}
			d.Fields = append(d.Fields, field)
		}
		return d
	}
	chosen := ""
	for _, name := range names {
		if name == "answer" {
			chosen = name
			break
		}
	}
	if chosen == "" && len(names) > 0 {
		chosen = names[0]
	}
	if chosen != "" {
		prop := p.Schema.Properties[chosen]
		if prop.Title != "" {
			d.Title = prop.Title
		}
		for _, raw := range prop.Enum {
			if option, ok := parseElicitationEnum(raw); ok {
				d.Options = append(d.Options, option)
			}
		}
	}
	return d
}

// elicitationContent builds the accept payload: a multi-property form answers
// as content keyed by property name; the single-property projection keeps the
// Runtime's "answer" key. Empty forms decline instead of accepting blank data.
func elicitationContent(d *decision) map[string]any {
	content := make(map[string]any)
	if len(d.Fields) > 0 {
		for _, field := range d.Fields {
			if value := strings.TrimSpace(field.Value); value != "" {
				content[field.Name] = value
			}
		}
		return content
	}
	if answer := strings.TrimSpace(d.Answer); answer != "" {
		content["answer"] = answer
	}
	return content
}

func elicitationResolvedLabel(d *decision, content map[string]any) string {
	if len(d.Fields) == 0 {
		if value, ok := content["answer"].(string); ok {
			return value
		}
		return ""
	}
	parts := make([]string, 0, len(d.Fields))
	for _, field := range d.Fields {
		if value, ok := content[field.Name].(string); ok {
			parts = append(parts, field.Name+"="+value)
		}
	}
	return strings.Join(parts, ", ")
}

// parseElicitationEnum accepts both enum shapes used by ACP form schemas:
// plain strings and the {id,label} option objects the Runtime embeds when it
// projects a legacy question into elicitation/create.
func parseElicitationEnum(raw json.RawMessage) (decisionOption, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil && strings.TrimSpace(text) != "" {
		return decisionOption{ID: text, Label: text}, true
	}
	var obj struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		id := obj.ID
		if id == "" {
			id = obj.Label
		}
		label := obj.Label
		if label == "" {
			label = id
		}
		if id != "" {
			return decisionOption{ID: id, Label: label}, true
		}
	}
	return decisionOption{}, false
}

// permissionOptionLabel keeps the Runtime-provided option name, which is the
// canonical user-facing label, and only falls back to a Native translation for
// older projections that omit it. Never overwrite an ACP name with a
// client-local string: the Runtime may already have localized it.
func permissionOptionLabel(name, kind string) string {
	if label := strings.TrimSpace(name); label != "" {
		return label
	}
	switch kind {
	case "allow_once", "allow":
		return T("允许一次")
	case "reject_once", "reject":
		return T("拒绝")
	default:
		return kind
	}
}

func permissionPrompt(input map[string]any) string {
	if input == nil {
		return ""
	}
	if command, ok := input["command"].(string); ok && command != "" {
		return command
	}
	if path, ok := input["path"].(string); ok && path != "" {
		return path
	}
	if query, ok := input["query"].(string); ok && query != "" {
		return query
	}
	return ""
}

func parseQuestionOptions(raw json.RawMessage) []decisionOption {
	if len(raw) == 0 {
		return nil
	}
	var objects []questionOption
	if err := json.Unmarshal(raw, &objects); err == nil {
		var out []decisionOption
		for _, opt := range objects {
			label := opt.Label
			if label == "" {
				label = opt.ID
			}
			out = append(out, decisionOption{ID: opt.ID, Label: label})
		}
		return out
	}
	var stringsOpt []string
	if err := json.Unmarshal(raw, &stringsOpt); err == nil {
		out := make([]decisionOption, 0, len(stringsOpt))
		for _, s := range stringsOpt {
			out = append(out, decisionOption{ID: s, Label: s})
		}
		return out
	}
	return nil
}
