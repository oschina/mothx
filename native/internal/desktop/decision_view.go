package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// decisionCard projects an Approval/Question reverse request inline in the
// transcript. Resolution stays first-response-wins on the Runtime side; this
// client only keeps the card's local resolved label and sends the canonical
// JSON-RPC response.
func (a *nativeApp) decisionCard(c *ui.Context, p palette, message *chatMessage) {
	d := message.Decision
	if d == nil {
		return
	}
	id := decisionIDString(d.ID)
	border := p.T.Warning
	if d.Resolved {
		border = p.T.Border
	}
	ui.Column(c).Key(message.ID).WidthPercent(100).MaxWidth(780).Margin(0, ui.Auto).Radius(10).
		Background(p.Thought).Border(1, border).Gap(8).Padding(10, 12).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			icon := iconAlert
			if d.Kind == "question" || d.Kind == "elicitation" {
				icon = iconSpark
			}
			ui.Icon(c, icon).FontSize(14).TextColor(border)
			ui.Text(c, T(d.Title)).FontSize(12).FontWeight(650).TextColor(p.T.Text).Grow(1).SingleLine()
			if label := decisionDeadlineLabel(d.Deadline); label != "" && !d.Resolved {
				c.After(time.Second)
				ui.Text(c, T(label)).FontSize(9.5).TextColor(p.T.Warning)
			}
		})
		if d.Prompt != "" {
			ui.Text(c, d.Prompt).FontSize(11.5).TextColor(p.T.TextMuted).Selectable()
		}
		if d.Kind == "permission" {
			if input := prettyToolInput(d.RawInput); input != "" {
				ui.Text(c, input).Font("monospace").FontSize(10).Padding(7, 9).Radius(7).
					Background(p.T.Background).Border(1, p.T.Border).MaxLines(12).Selectable()
			}
		}
		if d.Resolved {
			ui.Text(c, d.ResolvedValue).FontSize(10.5).TextColor(p.T.TextMuted)
			return
		}
		if d.Multi {
			for _, option := range d.Options {
				if ptr := d.Choices[option.ID]; ptr != nil {
					ui.Checkbox(c, ptr, option.Label).FontSize(11.5)
				}
			}
		} else if len(d.Fields) > 0 {
			// Multi-property elicitation form: one input or enum choice per
			// property; values are submitted as content keyed by name.
			for i := range d.Fields {
				field := &d.Fields[i]
				ui.Column(c).Gap(3).Children(func() {
					ui.Text(c, field.Title).FontSize(10.5).FontWeight(600).TextColor(p.T.TextMuted)
					if field.Description != "" {
						ui.Text(c, field.Description).FontSize(9.5).TextColor(p.T.TextMuted).LineHeight(1.3)
					}
					if len(field.Options) > 0 {
						ui.Row(c).Wrap().Gap(6).Children(func() {
							for _, option := range field.Options {
								option := option
								b := ui.Button(c.Key("decision:"+id+":field:"+field.Name+":"+option.ID), option.Label)
								if field.Value == option.ID {
									b.Background(p.Active)
								}
								if b.Clicked() {
									field.Value = option.ID
								}
							}
						})
					} else {
						input := ui.TextInput(c.Key("decision:"+id+":field:"+field.Name), &field.Value).
							Placeholder(T("输入回答…")).FontSize(11.5).Height(30).Padding(0, 8).
							Background(p.T.Background).Radius(7).Border(1, p.T.Border)
						input.Label(field.Title)
					}
				})
			}
		} else if d.Kind != "permission" {
			ui.Row(c).Wrap().Gap(6).Children(func() {
				for _, option := range d.Options {
					option := option
					if ui.Button(c.Key("decision:"+id+":option:"+option.ID), option.Label).Clicked() {
						a.answerDecision(d, option.Label)
					}
				}
			})
		}
		if d.Kind != "permission" && len(d.Fields) == 0 {
			input := ui.TextInput(c.Key("decision:"+id+":answer"), &d.Answer).
				Placeholder(T("输入回答…")).FontSize(11.5).Height(30).Padding(0, 8).
				Background(p.T.Background).Radius(7).Border(1, p.T.Border)
			input.Label(T("回答"))
		}
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			cancel := ui.Button(c.Key("decision:"+id+":cancel"), T("取消")).Label(T("取消此次确认"))
			if cancel.Clicked() {
				a.cancelDecision(d)
			}
			ui.Spacer(c)
			if d.Kind == "permission" {
				for _, option := range d.Options {
					option := option
					label := option.Label
					if option.Kind == "allow_once" || option.Kind == "allow" {
						if ui.PrimaryButton(c.Key("decision:"+id+":allow:"+option.ID), label).Clicked() {
							a.answerDecision(d, option.ID)
						}
					} else if ui.Button(c.Key("decision:"+id+":deny:"+option.ID), label).Clicked() {
						a.answerDecision(d, option.ID)
					}
				}
				return
			}
			if d.Multi || d.Kind == "elicitation" || len(d.Options) > 0 {
				if ui.PrimaryButton(c.Key("decision:"+id+":submit"), T("确认")).Clicked() {
					a.answerDecision(d, "")
				}
			}
		})
	})
}
