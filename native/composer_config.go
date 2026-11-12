package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// This file owns the pre-session configuration surface. A selection in a draft
// shell stays local until session/new, then flushes through the canonical
// session/set_config_option contract. It never creates a session as a side
// effect of browsing a menu.

var draftConfigOrder = []string{
	"provider",
	"model",
	"mode",
	"thinking_level",
	"expert",
	"sandbox",
	"browser",
	"web_search",
}

func (a *nativeApp) cloneDraftConfig() map[string]string {
	if len(a.draftConfig) == 0 {
		return nil
	}
	out := make(map[string]string, len(a.draftConfig))
	for id, value := range a.draftConfig {
		out[id] = value
	}
	return out
}

func (a *nativeApp) configOptionByID(id string) *configOption {
	for i := range a.configOptions {
		if a.configOptions[i].ID == id {
			return &a.configOptions[i]
		}
	}
	return nil
}

func (a *nativeApp) applyDraftConfigOption(configID, value string) {
	if a.draftConfig == nil {
		a.draftConfig = make(map[string]string)
	}
	configID = strings.TrimSpace(configID)
	if configID == "" {
		return
	}
	a.draftConfig[configID] = value
	if option := a.configOptionByID(configID); option != nil {
		option.CurrentValue = value
	}
	switch configID {
	case "provider":
		// The old provider's model identity is not valid for the new one.
		delete(a.draftConfig, "model")
		a.refreshDraftProviderModels(value)
	case "mode":
		a.mode = value
	}
	a.syncModelState()
}

// refreshDraftProviderModels projects the provider catalog for a draft shell.
// It joins on the provider projection only; no local model registry is built.
func (a *nativeApp) refreshDraftProviderModels(provider string) {
	provider = strings.TrimSpace(provider)
	if provider == "" || a.currentSession != "" || !a.featureEnabled("manageProviders") {
		return
	}
	client := a.acp
	if client == nil {
		return
	}
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		raw, err := client.call("mothx/manage/providers/list", map[string]any{})
		a.dispatch(func() {
			if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) || a.currentSession != "" {
				return
			}
			if a.configOptionByID("provider") == nil || a.configOptionByID("provider").CurrentValue != provider {
				return
			}
			if err != nil {
				a.status = "读取供应商模型失败：" + err.Error()
				return
			}
			var catalog providerCatalogView
			if err := json.Unmarshal(raw, &catalog); err != nil {
				a.status = "供应商模型响应无效：" + err.Error()
				return
			}
			choices := make([]configChoice, 0, len(catalog.Models))
			for _, model := range catalog.Models {
				if !strings.EqualFold(model.Provider, provider) || strings.TrimSpace(model.ID) == "" {
					continue
				}
				name := model.Name
				if name == "" {
					name = model.ID
				}
				choices = append(choices, configChoice{Value: model.ID, Name: name})
			}
			option := a.configOptionByID("model")
			if option == nil {
				return
			}
			option.Options = choices
			selected := option.CurrentValue
			if len(choices) == 0 {
				selected = ""
			} else {
				found := false
				for _, choice := range choices {
					if choice.Value == selected {
						found = true
						break
					}
				}
				if !found {
					selected = choices[0].Value
				}
			}
			option.CurrentValue = selected
			if a.draftConfig == nil {
				a.draftConfig = make(map[string]string)
			}
			if selected != "" {
				a.draftConfig["model"] = selected
			} else {
				delete(a.draftConfig, "model")
			}
			a.syncModelState()
		})
	}()
}

// flushDraftConfig applies every pending selection in a stable order. The
// caller owns the session/create lifecycle; failures are reported without
// discarding the prompt, matching the shared Runtime's partial-config policy.
func (a *nativeApp) flushDraftConfig(client *acpClient, sessionID string, values map[string]string) string {
	if client == nil || sessionID == "" || len(values) == 0 {
		return ""
	}
	var failures []string
	for _, configID := range draftConfigOrder {
		value, ok := values[configID]
		if !ok {
			continue
		}
		raw, err := client.call("session/set_config_option", map[string]any{
			"sessionId": sessionID,
			"configId":  configID,
			"value":     value,
		})
		if err != nil {
			failures = append(failures, configID+"："+err.Error())
			continue
		}
		var result sessionResult
		if json.Unmarshal(raw, &result) == nil && len(result.ConfigOptions) > 0 {
			options := result.ConfigOptions
			a.dispatch(func() {
				if a.currentSession == sessionID {
					a.configOptions = options
				}
			})
		}
	}
	if len(failures) > 0 {
		return "部分配置未应用：" + strings.Join(failures, "；")
	}
	return ""
}

func (a *nativeApp) modeChoices() []configChoice {
	if option := a.configOptionByID("mode"); option != nil && len(option.Options) > 0 {
		return option.Options
	}
	if len(a.availableModes) > 0 {
		out := make([]configChoice, 0, len(a.availableModes))
		for _, mode := range a.availableModes {
			name := mode.Name
			if name == "" {
				name = mode.ID
			}
			out = append(out, configChoice{Value: mode.ID, Name: name})
		}
		return out
	}
	return []configChoice{
		{Value: "agent", Name: "Agent"},
		{Value: "plan", Name: "Plan"},
		{Value: "yolo", Name: "Yolo"},
		{Value: "os", Name: "OS"},
	}
}

func (a *nativeApp) currentModeValue() string {
	if a.mode != "" {
		return a.mode
	}
	if option := a.configOptionByID("mode"); option != nil && option.CurrentValue != "" {
		return option.CurrentValue
	}
	return "yolo"
}

func (a *nativeApp) modeLabel() string {
	current := a.currentModeValue()
	for _, choice := range a.modeChoices() {
		if choice.Value == current {
			if choice.Name != "" {
				return choice.Name
			}
			return current
		}
	}
	return current
}

func (a *nativeApp) modelMenu(c *ui.Context, p palette) {
	label := a.modelDisplay()
	btn := ui.PopoverMenuButton(c.Key("model-menu"), label, func(m *ui.Menu) {
		model := a.currentModelOption()
		provider := a.configOptionByID("provider")
		added := false
		if a.featureEnabled("manageProviders") {
			if m.Item(T("搜索模型与供应商…")).ID("model:search").Chosen() {
				a.openModelPicker()
			}
			m.Separator()
		}
		if provider != nil && len(provider.Options) > 1 {
			providerName := provider.CurrentValue
			if providerName == "" {
				providerName = T("供应商")
			}
			m.Submenu(Tf("供应商：%s", providerName), func(sub *ui.Menu) {
				for _, choice := range provider.Options {
					choice := choice
					if sub.Item(choice.Name).ID("provider:" + choice.Value).
						Radio(choice.Value == provider.CurrentValue).Chosen() {
						a.setConfigOption("provider", choice.Value)
					}
				}
			})
			m.Separator()
			added = true
		}
		if model != nil && len(model.Options) > 0 {
			for _, opt := range model.Options {
				opt := opt
				if m.Item(opt.Name).Checked(model.CurrentValue == opt.Value).Chosen() {
					a.setConfigOption("model", opt.Value)
				}
			}
			added = true
		}
		if thinking := a.currentThinkingOption(); thinking != nil && len(thinking.Options) > 0 {
			if added {
				m.Separator()
			}
			for _, opt := range thinking.Options {
				opt := opt
				if m.Item(Tf("思考：%s", opt.Name)).Checked(thinking.CurrentValue == opt.Value).Chosen() {
					a.setConfigOption(thinking.ID, opt.Value)
				}
			}
			added = true
		}
		if !added {
			m.Item(T("使用默认模型")).Disabled(true)
		}
	}, composerMenuOptions...).FontSize(10.5).TextColor(p.t.TextMuted).Padding(4, 6).Radius(7).Disabled(a.running || a.creatingSession)
	btn.Tooltip(T("切换供应商、模型与思考级别"))
}

func (a *nativeApp) modeMenu(c *ui.Context, p palette) {
	btn := ui.PopoverMenuButton(c.Key("mode-menu"), a.modeLabel(), func(m *ui.Menu) {
		current := a.currentModeValue()
		for _, choice := range a.modeChoices() {
			choice := choice
			item := m.Item(choice.Name).ID("mode:" + choice.Value).Radio(choice.Value == current)
			if choice.Value == "router" {
				item.Disabled(a.currentSession == "")
			}
			if item.Chosen() {
				if choice.Value == "router" {
					a.openRouterPanel()
					continue
				}
				a.setConfigOption("mode", choice.Value)
			}
		}
	}, composerMenuOptions...).FontSize(10.5).TextColor(p.t.TextMuted).Padding(4, 6).Radius(7).Disabled(a.running || a.creatingSession)
	btn.Tooltip(T("切换 Agent / Plan / Yolo / OS 模式"))
}

func (a *nativeApp) expertMenu(c *ui.Context, p palette) {
	option := a.configOptionByID("expert")
	if option == nil || len(option.Options) == 0 {
		return
	}
	label := T("无主角团")
	for _, choice := range option.Options {
		if choice.Value == option.CurrentValue {
			label = localizedChoiceName(choice)
			break
		}
	}
	btn := ui.PopoverMenuButton(c.Key("expert-menu"), label, func(m *ui.Menu) {
		m.MaxWidth(300)
		if m.Item(T("无主角团")).ID("expert:none").Radio(option.CurrentValue == "").Chosen() {
			a.chooseSessionExpert("")
		}
		m.Separator()
		for _, choice := range option.Options {
			choice := choice
			item := m.Item(localizedChoiceName(choice)).ID("expert:" + choice.Value).Radio(choice.Value == option.CurrentValue)
			if description := localizedChoiceDescription(choice); description != "" {
				item.Subtitle(description)
			}
			if item.Chosen() {
				a.chooseSessionExpert(choice.Value)
			}
		}
	}, composerMenuOptions...).FontSize(10.5).TextColor(p.t.TextMuted).Padding(4, 6).Radius(7).Disabled(a.running || a.creatingSession)
	btn.Tooltip(T("选择当前会话的主角团"))
}

// chooseSessionExpert preserves Runtime-owned identity semantics: an initial
// bind and an unbind mutate the current idle session, while replacing one
// non-empty expert on an existing session must fork. This mirrors Desktop's
// chooseSessionExpert and never silently rewrites history.
func (a *nativeApp) chooseSessionExpert(value string) {
	option := a.configOptionByID("expert")
	if option == nil {
		return
	}
	if value == option.CurrentValue {
		return
	}
	if a.currentSession == "" || option.CurrentValue == "" || value == "" {
		a.setConfigOption("expert", value)
		return
	}
	session, ok := a.sessionByID(a.currentSession)
	if !ok {
		session = listedSession{SessionID: a.currentSession, Cwd: a.workspace}
	}
	a.forkSessionWithExpert(session, value)
}

func (a *nativeApp) forkSessionWithExpert(session listedSession, expertID string) {
	client := a.acp
	if client == nil {
		return
	}
	cwd := session.Cwd
	if cwd == "" {
		cwd = a.workspace
	}
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		raw, err := client.call("session/fork", buildSessionForkRequest(session, expertID))
		a.dispatch(func() {
			if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			if err != nil {
				a.showErrorToast("Fork 失败：" + err.Error())
				a.status = "Fork 失败：" + err.Error()
				return
			}
			var result sessionResult
			if err := json.Unmarshal(raw, &result); err != nil || result.SessionID == "" {
				a.status = "Fork 响应无效"
				return
			}
			a.showToast("已带新 Expert 分叉会话")
			a.selectSession(listedSession{
				SessionID: result.SessionID,
				Cwd:       cwd,
			})
			go a.refreshSessions(client, cwd, a.featureSnapshot())
		})
	}()
}

// composerMenuOptions keeps every composer popover above its button. The
// composer sits at the bottom edge of the window; a short menu (for example a
// single capability) would otherwise be placed below the anchor, behind the
// composer hint/status bar and effectively invisible.
var composerMenuOptions = []ui.PopoverMenuButtonOptions{{At: ui.AnchorTopEnd, Self: ui.AnchorBottomEnd}}

// localizedChoiceName and localizedChoiceDescription project the optional
// bilingual labels carried by runtime catalogs (currently the expert catalog)
// for the active UI locale. They fall back to the canonical fields so older
// runtimes and non-expert options keep rendering unchanged.
func localizedChoiceName(choice configChoice) string {
	name := ""
	if currentLocale() == "en" {
		name = strings.TrimSpace(choice.NameEn)
	} else {
		name = strings.TrimSpace(choice.NameZh)
	}
	if name == "" {
		name = strings.TrimSpace(choice.Name)
	}
	if name == "" {
		name = strings.TrimSpace(choice.Value)
	}
	return name
}

func localizedChoiceDescription(choice configChoice) string {
	description := ""
	if currentLocale() == "en" {
		description = strings.TrimSpace(choice.DescriptionEn)
	} else {
		description = strings.TrimSpace(choice.DescriptionZh)
	}
	if description == "" {
		description = strings.TrimSpace(choice.Description)
	}
	return description
}

func (a *nativeApp) capabilitiesMenu(c *ui.Context, p palette) {
	type capability struct {
		id   string
		name string
	}
	var available []capability
	for _, candidate := range []capability{
		{id: "sandbox", name: "沙箱"},
		{id: "browser", name: "浏览器"},
		{id: "web_search", name: "Web 搜索"},
	} {
		if option := a.configOptionByID(candidate.id); option != nil {
			available = append(available, candidate)
		}
	}
	if len(available) == 0 {
		return
	}
	btn := ui.PopoverMenuButton(c.Key("capabilities-menu"), T("能力"), func(m *ui.Menu) {
		for _, candidate := range available {
			option := a.configOptionByID(candidate.id)
			on := option != nil && option.CurrentValue == "true"
			name := T(candidate.name)
			if option != nil && option.Name != "" {
				name = T(option.Name)
			}
			if m.Item(name).ID("capability:" + candidate.id).Checked(on).Chosen() {
				next := "true"
				if on {
					next = "false"
				}
				a.setConfigOption(candidate.id, next)
			}
		}
	}, composerMenuOptions...).FontSize(10.5).TextColor(p.t.TextMuted).Padding(4, 6).Radius(7).Disabled(a.running || a.creatingSession)
	btn.Tooltip(T("切换沙箱、浏览器与 Web 搜索能力"))
}

// attachmentMenu keeps file intake and slash-command insertion in one compact
// menu instead of adding another toolbar button.
func (a *nativeApp) attachmentMenu(c *ui.Context, p palette) {
	btn := ui.PopoverMenuButton(c.Key("composer-add"), "+", func(m *ui.Menu) {
		m.MaxWidth(340)
		if m.Item(T("附加文件…")).ID("attach:files").Chosen() {
			a.attachFiles()
		}
		if m.Item(T("粘贴剪贴板图片")).ID("attach:clipboard").Chosen() {
			a.attachClipboardImage()
		}
		if len(a.commands) == 0 {
			return
		}
		m.Separator()
		for _, command := range a.commands {
			command := command
			name := strings.TrimSpace(command.Name)
			if name == "" {
				continue
			}
			item := m.Item("/" + name).ID("command:" + name)
			if command.Description != "" {
				item.Subtitle(command.Description)
			}
			if item.Chosen() {
				a.insertComposerText("/" + name + " ")
			}
		}
	}, composerMenuOptions...).FontSize(13).TextColor(p.t.TextMuted).Padding(4, 7).Radius(7).Disabled(a.running || a.creatingSession)
	btn.Tooltip(T("附加文件或插入技能命令"))
}

// attachClipboardImage reads one PNG image from the system clipboard and
// embeds it as a resource. No temporary file is written. It reports whether the
// clipboard produced an image so a text paste can fall through unchanged.
func (a *nativeApp) attachClipboardImage() bool {
	data, err := mygo.Clipboard.Read(transfer.PNG)
	if err != nil {
		a.showErrorToast("读取剪贴板图片失败：" + err.Error())
		return false
	}
	payload, err := data.Read(transfer.PNG)
	if err != nil || len(payload) == 0 {
		return false
	}
	if len(payload) > nativeMaxEmbeddedAttachment {
		a.showErrorToast("剪贴板图片过大，未附加")
		return true
	}
	preview, _ := ui.DecodeImage(payload)
	name := "clipboard.png"
	a.attachments = append(a.attachments, attachment{
		Name:     name,
		MimeType: "image/png",
		Data:     base64.StdEncoding.EncodeToString(payload),
		Size:     int64(len(payload)),
		Embedded: true,
		Preview:  preview,
	})
	a.showToast("已粘贴剪贴板图片")
	return true
}

func (a *nativeApp) insertComposerText(text string) {
	if text == "" {
		return
	}
	if strings.HasSuffix(a.draft, text) {
		return
	}
	a.draft = text + a.draft
}

// refreshModelCatalog fetches the safe provider/model projection used by the
// searchable picker and composer capability badges. It is a projection of the
// shared Runtime registry, never a local model catalog.
func (a *nativeApp) refreshModelCatalog(client *acpClient, features map[string]bool) {
	if client == nil || !(features["manageProviders"] || a.featureEnabled("manageProviders")) {
		return
	}
	raw, err := client.call("mothx/manage/providers/list", map[string]any{})
	if err != nil {
		addDiagnostic("warn", "model catalog: "+err.Error())
		return
	}
	var catalog providerCatalogView
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return
	}
	a.dispatch(func() { a.modelCatalog = catalog.Models })
}

func (a *nativeApp) openModelPicker() {
	a.modelPickerOpen = true
	a.modelPickerQuery = ""
	a.modelPickerProvider = ""
	if len(a.modelCatalog) == 0 && a.acp != nil {
		a.modelPickerLoading = true
		a.modelPickerErr = ""
		client := a.acp
		go func() {
			raw, err := client.call("mothx/manage/providers/list", map[string]any{})
			a.dispatch(func() {
				a.modelPickerLoading = false
				if err != nil {
					a.modelPickerErr = "读取供应商模型失败：" + err.Error()
					return
				}
				var catalog providerCatalogView
				if err := json.Unmarshal(raw, &catalog); err != nil {
					a.modelPickerErr = "供应商模型响应无效：" + err.Error()
					return
				}
				a.modelCatalog = catalog.Models
				a.modelPickerModels = catalog.Models
			})
		}()
	}
	a.modelPickerModels = a.modelCatalog
}

func (a *nativeApp) modelPickerProviders() []string {
	seen := map[string]bool{}
	var providers []string
	for _, model := range a.modelCatalog {
		if model.Provider == "" || seen[model.Provider] {
			continue
		}
		seen[model.Provider] = true
		providers = append(providers, model.Provider)
	}
	return providers
}

func (a *nativeApp) filteredPickerModels() []providerCatalogModel {
	query := strings.ToLower(strings.TrimSpace(a.modelPickerQuery))
	out := make([]providerCatalogModel, 0, len(a.modelPickerModels))
	for _, model := range a.modelPickerModels {
		if a.modelPickerProvider != "" && !strings.EqualFold(model.Provider, a.modelPickerProvider) {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(model.ID + " " + model.Name + " " + model.Provider)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, model)
	}
	return out
}

func (a *nativeApp) currentModelMeta() *providerCatalogModel {
	provider := ""
	if option := a.configOptionByID("provider"); option != nil {
		provider = option.CurrentValue
	}
	model := a.modelValue
	if model == "" {
		if option := a.configOptionByID("model"); option != nil {
			model = option.CurrentValue
		}
	}
	for i := range a.modelCatalog {
		candidate := &a.modelCatalog[i]
		if candidate.ID == model && (provider == "" || strings.EqualFold(candidate.Provider, provider)) {
			return candidate
		}
	}
	return nil
}

func modelCapabilityBadgeList(model *providerCatalogModel) []string {
	if model == nil {
		return nil
	}
	var badges []string
	if model.Reasoning {
		badges = append(badges, "reasoning")
	}
	for _, input := range model.Input {
		input = strings.ToLower(strings.TrimSpace(input))
		switch input {
		case "text", "image", "audio", "video":
			badges = append(badges, input)
		}
	}
	return badges
}

func (a *nativeApp) modelCapabilityBadges(c *ui.Context, p palette) {
	badges := modelCapabilityBadgeList(a.currentModelMeta())
	if len(badges) == 0 {
		return
	}
	ui.Row(c).AlignItems(ui.Center).Gap(3).Children(func() {
		for _, label := range badges {
			ui.Box(c).Padding(0, 4).Radius(4).Background(p.chip).Border(1, p.t.Border).Children(func() {
				ui.Text(c, T(label)).FontSize(8).TextColor(p.t.TextMuted)
			})
		}
	})
}

func (a *nativeApp) selectModelFromPicker(provider, model string) {
	a.modelPickerOpen = false
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	if a.currentSession == "" {
		if provider != "" {
			a.applyDraftConfigOption("provider", provider)
		}
		a.applyDraftConfigOption("model", model)
		return
	}
	client := a.acp
	sessionID := a.currentSession
	if client == nil {
		return
	}
	currentProvider := ""
	if option := a.configOptionByID("provider"); option != nil {
		currentProvider = option.CurrentValue
	}
	if provider == "" || provider == currentProvider {
		a.setConfigOption("model", model)
		return
	}
	go func() {
		_, err := client.call("session/set_config_option", map[string]any{
			"sessionId": sessionID,
			"configId":  "provider",
			"value":     provider,
		})
		if err != nil {
			a.dispatch(func() { a.showErrorToast("切换失败：" + err.Error()) })
			return
		}
		raw, err := client.call("session/set_config_option", map[string]any{
			"sessionId": sessionID,
			"configId":  "model",
			"value":     model,
		})
		if err != nil {
			a.dispatch(func() { a.showErrorToast("切换失败：" + err.Error()) })
			return
		}
		var result sessionResult
		if json.Unmarshal(raw, &result) == nil && len(result.ConfigOptions) > 0 {
			a.dispatch(func() {
				if a.currentSession != sessionID {
					return
				}
				a.configOptions = result.ConfigOptions
				a.syncModelState()
			})
		}
	}()
}

func (a *nativeApp) modelPickerDialog(c *ui.Context, p palette) {
	if !a.modelPickerOpen {
		return
	}
	dialog := ui.DialogBase(c, &a.modelPickerOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(640).MaxWidthPercent(94).MaxHeightPercent(86).Padding(14).Radius(14).
			Background(p.t.Background).Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconCpu).FontSize(15).TextColor(p.t.Accent)
				ui.Text(c, T("选择模型")).FontSize(13).FontWeight(650).Grow(1)
				if ui.Button(c.Key("model-picker-close"), T("关闭")).Clicked() {
					a.modelPickerOpen = false
				}
			})
			ui.TextInput(c.Key("model-picker-search"), &a.modelPickerQuery).FontSize(12).Height(34).
				Padding(0, 10).Placeholder(T("搜索模型或供应商…")).
				Background(p.t.Surface).Radius(8).Border(1, p.t.Border)
			ui.Row(c).Wrap().Gap(5).Children(func() {
				a.modelProviderChip(c, p, "", "全部")
				for _, provider := range a.modelPickerProviders() {
					a.modelProviderChip(c, p, provider, provider)
				}
			})
			if a.modelPickerLoading {
				ui.Text(c, T("正在读取供应商模型…")).FontSize(11).TextColor(p.t.TextMuted)
				return
			}
			if a.modelPickerErr != "" {
				ui.Text(c, T(a.modelPickerErr)).FontSize(11).TextColor(p.t.Danger)
			}
			models := a.filteredPickerModels()
			if len(models) == 0 {
				ui.Text(c, T("没有匹配的模型")).FontSize(11).TextColor(p.t.TextMuted)
				return
			}
			ui.Scroll(c).Grow(1).Height(420).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					for _, model := range models {
						model := model
						name := model.Name
						if name == "" {
							name = model.ID
						}
						b := ui.ButtonBase(c.Key("model-pick:"+model.Provider+":"+model.ID)).
							Padding(7, 9).Radius(8).AlignItems(ui.Center).Gap(8)
						if b.Hovered() {
							b.Background(p.hover)
						}
						b.Children(func() {
							ui.Column(c).Gap(1).Grow(1).Children(func() {
								ui.Text(c, name).FontSize(11.5).TextColor(p.t.Text).SingleLine()
								ui.Text(c, model.ID+" · "+model.Provider).FontSize(9).TextColor(p.t.TextMuted).SingleLine()
							})
							for _, badge := range modelCapabilityBadgeList(&model) {
								ui.Box(c).Padding(0, 4).Radius(4).Background(p.chip).Children(func() {
									ui.Text(c, T(badge)).FontSize(8).TextColor(p.t.TextMuted)
								})
							}
						})
						b.Label(name)
						if b.Clicked() {
							a.selectModelFromPicker(model.Provider, model.ID)
						}
					}
				})
			})
		})
	})
	if dialog.Dismissed() {
		a.modelPickerOpen = false
	}
}

func (a *nativeApp) modelProviderChip(c *ui.Context, p palette, provider, label string) {
	active := a.modelPickerProvider == provider
	b := ui.ButtonBase(c.Key("model-provider:"+provider)).Height(26).Padding(0, 9).Radius(999).
		AlignItems(ui.Center).Justify(ui.Center).Border(1, p.t.Border)
	if active {
		b.Background(p.active).Border(1, p.t.Accent)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		color := p.t.Text
		if active {
			color = p.t.Accent
		}
		ui.Text(c, T(label)).FontSize(10).TextColor(color)
	})
	b.Label(T(label))
	if b.Clicked() {
		a.modelPickerProvider = provider
	}
}
