package desktop

import (
	"strings"
)

func (a *nativeApp) queueDecision(d decision) {
	a.dispatch(func() {
		add := func() {
			id := "decision:" + decisionIDString(d.ID)
			if id == "decision:" {
				id = a.nextLocalMessageID("decision")
			}
			message := a.upsertMessage(id, messageDecision)
			copy := d
			if copy.Multi {
				copy.Choices = make(map[string]*bool, len(copy.Options))
				for _, option := range copy.Options {
					copy.Choices[option.ID] = new(bool)
				}
			}
			message.Decision = &copy
			message.Done = false
		}
		if d.Session != "" && d.Session != a.currentSession && d.Session != a.loadingSession {
			a.withSessionState(d.Session, add)
			a.showErrorToast("后台会话需要确认，切换到该任务处理")
			return
		}
		add()
	})
}

func (a *nativeApp) decisionMessage(d *decision) *chatMessage {
	for _, message := range a.messages {
		if message.Decision == d {
			return message
		}
	}
	return nil
}

func (a *nativeApp) answerDecision(d *decision, optionID string) {
	if d == nil || d.Resolved {
		return
	}
	client := a.client()
	answer := strings.TrimSpace(d.Answer)
	if optionID != "" {
		answer = optionID
		if !d.Multi {
			d.Answer = optionID
		}
	}
	resolved := ""
	switch d.Kind {
	case "permission":
		if client != nil {
			if optionID == "" {
				_ = client.Respond(d.ID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
			} else {
				_ = client.Respond(d.ID, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}})
			}
		}
		resolved = T("已取消")
		if optionID != "" {
			for _, option := range d.Options {
				if option.ID == optionID {
					resolved = Tf("已选择：%s", option.Label)
					break
				}
			}
		}
	case "elicitation":
		content := elicitationContent(d)
		if client != nil {
			if len(content) == 0 {
				_ = client.Respond(d.ID, map[string]any{"action": "decline"})
			} else {
				_ = client.Respond(d.ID, map[string]any{"action": "accept", "content": content})
			}
		}
		resolved = T("已取消")
		if len(content) > 0 {
			resolved = Tf("已提交：%s", elicitationResolvedLabel(d, content))
		}
	default:
		if client != nil {
			if d.Multi {
				var answers []string
				for _, option := range d.Options {
					if ptr := d.Choices[option.ID]; ptr != nil && *ptr {
						answers = append(answers, option.Label)
					}
				}
				if answer != "" {
					answers = append(answers, answer)
				}
				if len(answers) == 0 {
					_ = client.Respond(d.ID, map[string]any{"cancelled": true})
				} else {
					_ = client.Respond(d.ID, map[string]any{"answers": answers})
				}
			} else if answer == "" {
				_ = client.Respond(d.ID, map[string]any{"cancelled": true})
			} else {
				_ = client.Respond(d.ID, map[string]any{"answer": answer})
			}
		}
		resolved = T("已取消")
		if answer != "" {
			resolved = Tf("已回答：%s", answer)
		}
	}
	d.Resolved = true
	d.ResolvedValue = resolved
	if message := a.decisionMessage(d); message != nil {
		message.Done = true
	}
}

func (a *nativeApp) cancelDecision(d *decision) {
	if d == nil || d.Resolved {
		return
	}
	client := a.client()
	switch d.Kind {
	case "permission":
		if client != nil {
			_ = client.Respond(d.ID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
		}
	case "elicitation":
		if client != nil {
			_ = client.Respond(d.ID, map[string]any{"action": "decline"})
		}
	default:
		if client != nil {
			_ = client.Respond(d.ID, map[string]any{"cancelled": true})
		}
	}
	d.Resolved = true
	d.ResolvedValue = T("已取消")
	if message := a.decisionMessage(d); message != nil {
		message.Done = true
	}
}
