package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/tui/i18n"
)

// --- Model group constants ---

const (
	modelGroupBasics       = "basics"
	modelGroupCapabilities = "capabilities"
	modelGroupSampling     = "sampling"
	modelGroupCost         = "cost"
	modelGroupCompat       = "compat"
)

// modelGroupInfo describes a model settings group.
type modelGroupInfo struct {
	ID          string
	Title       string
	Description string
}

var modelGroups = []modelGroupInfo{
	{modelGroupBasics, "A. Basics", "Name, Context window, Max tokens"},
	{modelGroupCapabilities, "B. Capabilities", "Reasoning, Input modalities"},
	{modelGroupSampling, "C. Sampling", "Temperature, Top P"},
	{modelGroupCost, "D. Cost", "Input/Output/CacheRead/CacheWrite pricing"},
	{modelGroupCompat, "E. Compatibility", "Thinking format, API flags"},
}

// currentModelEdit returns the modelEditState currently being edited.
func (a *App) currentModelEdit() *modelEditState {
	if a.auth.CurrentModelID == "" {
		return nil
	}
	return a.auth.Models[a.auth.CurrentModelID]
}

// prepareModelInput sets up the editor for a model field with placeholder
// explanation AND pre-filled default value.
func (a *App) prepareModelInput() {
	prompt := a.authModelInputPrompt()
	value := a.authModelInputValue()
	a.authInput = a.newAuthInput(prompt)
	if value != "" {
		a.authInput = a.authInput.SetValue(value)
	}
}

// authModelInputValue returns the current value for the active model field.
func (a *App) authModelInputValue() string {
	me := a.currentModelEdit()
	if me == nil {
		return ""
	}
	switch a.auth.ParamField {
	case "name":
		return me.Name
	case "contextWindow":
		if me.ContextWindow > 0 {
			return strconv.Itoa(me.ContextWindow)
		}
	case "maxTokens":
		if me.MaxTokens > 0 {
			return strconv.Itoa(me.MaxTokens)
		}
	case "input":
		if len(me.Input) > 0 {
			return strings.Join(me.Input, ",")
		}
	case "temperature":
		if me.Temperature != nil {
			return f64s(*me.Temperature)
		}
	case "topP":
		if me.TopP != nil {
			return f64s(*me.TopP)
		}
	case "costInput":
		if me.CostEnabled {
			return f64s(me.CostInput)
		}
	case "costOutput":
		if me.CostEnabled {
			return f64s(me.CostOutput)
		}
	case "cacheRead":
		if me.CostEnabled {
			return f64s(me.CacheRead)
		}
	case "cacheWrite":
		if me.CostEnabled {
			return f64s(me.CacheWrite)
		}
	case "thinkingFormat":
		return me.Compat.ThinkingFormat
	case "maxTokensField":
		return me.Compat.MaxTokensField
	}
	return ""
}

// --- Model list view ---

func (a *App) authModelListOptions() []authOption {
	opts := make([]authOption, 0, len(a.auth.ModelOrder)+2)
	// Add Model is always first
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthAddModel), Description: a.translator.Text(i18n.MsgAuthAddModel), Value: "add"})
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthFetchOnlineModels), Description: a.translator.Text(i18n.MsgAuthFetchOnlineModelsHint), Value: "fetchOnline"})
	for _, id := range a.auth.ModelOrder {
		if me, ok := a.auth.Models[id]; ok {
			opts = append(opts, authOption{
				Title:       id,
				Description: me.summaryShort(),
				Value:       "edit:" + id,
			})
		}
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthDone), Value: "done"})
	return opts
}

func (a *App) selectModelList(value string) tea.Cmd {
	if value == "done" {
		// All models done → go to default setting
		if a.isReviewEdit() {
			a.returnToReviewAfterEdit()
		} else {
			a.pushAuthView(authViewDefault)
		}
		return nil
	}
	if value == "add" {
		a.pushAuthView(authViewAddModelID)
		return nil
	}
	if value == "fetchOnline" {
		return a.startFetchOnlineModels()
	}
	if strings.HasPrefix(value, "edit:") {
		modelID := strings.TrimPrefix(value, "edit:")
		a.auth.CurrentModelID = modelID
		a.pushAuthView(authViewModelGroupList)
	}
	return nil
}

func (a *App) deleteSelectedAuthModel() bool {
	opts := a.authModelListOptions()
	if a.auth.Cursor < 0 || a.auth.Cursor >= len(opts) {
		return false
	}
	value := opts[a.auth.Cursor].Value
	if !strings.HasPrefix(value, "edit:") {
		return false
	}
	modelID := strings.TrimPrefix(value, "edit:")
	if !a.removeAuthModel(modelID) {
		return false
	}

	maxCursor := len(a.authModelListOptions()) - 1
	if a.auth.Cursor > maxCursor {
		a.auth.Cursor = max(0, maxCursor)
	}
	return true
}

// --- authViewModelGroupList ---

func (a *App) authModelGroupOptions() []authOption {
	me := a.currentModelEdit()
	if me == nil {
		return []authOption{}
	}
	opts := make([]authOption, 0, len(modelGroups))
	for _, g := range modelGroups {
		desc := ""
		switch g.ID {
		case modelGroupBasics:
			desc = a.authModelBasicsSummary(me)
		case modelGroupCapabilities:
			desc = a.authModelCapabilitiesSummary(me)
		case modelGroupSampling:
			desc = a.authModelSamplingSummary(me)
		case modelGroupCost:
			desc = a.authModelCostSummary(me)
		case modelGroupCompat:
			desc = a.authModelCompatSummary(&me.Compat)
		}
		opts = append(opts, authOption{
			Title:       a.translator.Text(modelGroupMessageID(g.ID)),
			Description: desc,
			Value:       g.ID,
		})
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthLabelConfirm), Value: "done"})
	return opts
}

func (a *App) authModelBasicsSummary(me *modelEditState) string {
	parts := []string{}
	if me.Name != me.ID && me.Name != "" {
		parts = append(parts, "name="+me.Name)
	}
	parts = append(parts, "ctx="+authItoa(me.ContextWindow))
	parts = append(parts, "max="+authItoa(me.MaxTokens))
	return strings.Join(parts, "  ")
}

func (a *App) authModelCapabilitiesSummary(me *modelEditState) string {
	parts := []string{}
	if me.Reasoning {
		parts = append(parts, "reasoning")
	}
	parts = append(parts, "in="+strings.Join(me.Input, ","))
	return strings.Join(parts, "  ")
}

func (a *App) authModelSamplingSummary(me *modelEditState) string {
	parts := []string{}
	if me.Temperature != nil {
		parts = append(parts, "t="+f64s(*me.Temperature))
	} else {
		parts = append(parts, "t=auto")
	}
	if me.TopP != nil {
		parts = append(parts, "p="+f64s(*me.TopP))
	} else {
		parts = append(parts, "p=auto")
	}
	if me.Compat.DisableSamplingParams == nil || *me.Compat.DisableSamplingParams {
		parts = append(parts, "sampling=off")
	}
	return strings.Join(parts, "  ")
}

func (a *App) authModelCostSummary(me *modelEditState) string {
	if !me.CostEnabled {
		return "(disabled)"
	}
	parts := []string{}
	parts = append(parts, "in="+f64s(me.CostInput))
	parts = append(parts, "out="+f64s(me.CostOutput))
	if me.CacheRead > 0 {
		parts = append(parts, "cr="+f64s(me.CacheRead))
	}
	if me.CacheWrite > 0 {
		parts = append(parts, "cw="+f64s(me.CacheWrite))
	}
	return strings.Join(parts, "  ")
}

func (a *App) authModelCompatSummary(ce *compatEditState) string {
	if !ce.Active || ce.activeCount() == 0 {
		return "(none active)"
	}
	return fmt.Sprintf("%d flag(s) active", ce.activeCount())
}

// --- Model sub-form options ---

func (a *App) authModelBasicsOptions() []authOption {
	me := a.currentModelEdit()
	if me == nil {
		return nil
	}
	opts := []authOption{
		{Title: a.translator.Text(i18n.MsgAuthLabelDisplayName), Description: valueOrDefault(me.Name, me.ID), Value: "name"},
		{Title: a.translator.Text(i18n.MsgAuthLabelContextWindow), Description: authItoa(me.ContextWindow), Value: "contextWindow"},
		{Title: a.translator.Text(i18n.MsgAuthLabelMaxOutputTokens), Description: authItoa(me.MaxTokens), Value: "maxTokens"},
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthLabelConfirm), Value: "done"})
	return opts
}

func (a *App) authModelCapabilitiesOptions() []authOption {
	me := a.currentModelEdit()
	if me == nil {
		return nil
	}
	opts := []authOption{
		{Title: a.translator.Text(i18n.MsgAuthLabelReasoning), Description: a.boolYesNo(me.Reasoning), Value: "reasoning"},
		{Title: a.translator.Text(i18n.MsgAuthLabelInputModalities), Description: strings.Join(me.Input, ","), Value: "input"},
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthLabelConfirm), Value: "done"})
	return opts
}

func (a *App) authModelSamplingOptions() []authOption {
	me := a.currentModelEdit()
	if me == nil {
		return nil
	}
	tempStr := "auto"
	if me.Temperature != nil {
		tempStr = f64s(*me.Temperature)
	}
	toppStr := "auto"
	if me.TopP != nil {
		toppStr = f64s(*me.TopP)
	}
	opts := []authOption{
		{Title: a.translator.Text(i18n.MsgAuthLabelTemperature), Description: tempStr, Value: "temperature"},
		{Title: a.translator.Text(i18n.MsgAuthLabelTopP), Description: toppStr, Value: "topP"},
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthLabelConfirm), Value: "done"})
	return opts
}

func (a *App) authModelCostOptions() []authOption {
	me := a.currentModelEdit()
	if me == nil {
		return nil
	}
	opts := []authOption{
		{Title: a.translator.Text(i18n.MsgAuthLabelEnableCostTracking), Description: a.authBool(me.CostEnabled), Value: "costEnabled"},
	}
	if me.CostEnabled {
		opts = append(opts,
			authOption{Title: a.translator.Text(i18n.MsgAuthLabelInputCost), Description: f64s(me.CostInput), Value: "costInput"},
			authOption{Title: a.translator.Text(i18n.MsgAuthLabelOutputCost), Description: f64s(me.CostOutput), Value: "costOutput"},
			authOption{Title: a.translator.Text(i18n.MsgAuthLabelCacheReadCost), Description: f64s(me.CacheRead), Value: "cacheRead"},
			authOption{Title: a.translator.Text(i18n.MsgAuthLabelCacheWriteCost), Description: f64s(me.CacheWrite), Value: "cacheWrite"},
		)
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthLabelConfirm), Value: "done"})
	return opts
}

func (a *App) authModelCompatOptions() []authOption {
	me := a.currentModelEdit()
	if me == nil {
		return nil
	}
	ce := &me.Compat
	opts := []authOption{
		{Title: a.translator.Text(i18n.MsgAuthLabelThinkingFormat), Description: valueOrDefault(ce.ThinkingFormat, a.translator.Text(i18n.MsgAuthValueAuto)), Value: "thinkingFormat"},
		{Title: a.translator.Text(i18n.MsgAuthLabelRequiresReasoningAsst), Description: a.boolYesNo(ce.RequiresReasoningContentOnAssistant), Value: "reqReasoningAsst"},
		{Title: a.translator.Text(i18n.MsgAuthLabelRequiresReasoningMsgs), Description: a.boolYesNo(ce.RequiresReasoningContentOnAssistantMessages), Value: "reqReasoningAsstMsgs"},
		{Title: a.translator.Text(i18n.MsgAuthLabelForceAdaptiveThinking), Description: a.boolYesNo(ce.ForceAdaptiveThinking), Value: "forceAdaptiveThinking"},
		{Title: a.translator.Text(i18n.MsgAuthLabelParseReasoningContent), Description: a.boolYesNo(ce.ParseReasoningInContent), Value: "parseReasoningInContent"},
	}
	// API Params
	opts = append(opts,
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelSupportsDeveloperRole), Description: a.triStateStr(ce.SupportsDeveloperRole), Value: "supportsDeveloperRole"},
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelSupportsStore), Description: a.triStateStr(ce.SupportsStore), Value: "supportsStore"},
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelSupportsReasoning), Description: a.triStateStr(ce.SupportsReasoningEffort), Value: "supportsReasoningEffort"},
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelSupportsStrictMode), Description: a.triStateStr(ce.SupportsStrictMode), Value: "supportsStrictMode"},
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelMaxTokensField), Description: valueOrDefault(ce.MaxTokensField, a.translator.Text(i18n.MsgAuthValueProviderDefault)), Value: "maxTokensField"},
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelDisableSamplingParams), Description: a.samplingParamsStr(ce.DisableSamplingParams), Value: "disableSamplingParams"},
	)
	// Cache
	opts = append(opts,
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelCacheControlOnTools), Description: a.triStateStr(ce.SupportsCacheControlOnTools), Value: "cacheControlOnTools"},
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelLongCacheRetention), Description: a.triStateStr(ce.SupportsLongCacheRetention), Value: "longCacheRetention"},
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelSupportsPromptCache), Description: a.triStateStr(ce.SupportsPromptCacheKey), Value: "promptCacheKey"},
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelSupportsReasoningSum), Description: a.triStateStr(ce.SupportsReasoningSummary), Value: "reasoningSummary"},
	)
	// Streaming
	opts = append(opts,
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelSessionAffinity), Description: a.boolYesNo(ce.SendSessionAffinityHeaders), Value: "sessionAffinityHeaders"},
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelEagerToolStreaming), Description: a.triStateStr(ce.SupportsEagerToolInputStreaming), Value: "eagerToolStreaming"},
	)
	// Tool choice controls
	opts = append(opts,
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelSupportsToolChoice), Description: a.triStateStr(ce.SupportsToolChoice), Value: "supportsToolChoice"},
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelSupportsParallelTools), Description: a.triStateStr(ce.SupportsParallelToolCalls), Value: "supportsParallelToolCalls"},
	)
	opts = append(opts,
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelResetCompat), Description: a.translator.Text(i18n.MsgAuthLabelResetCompat), Value: "resetAll"},
		authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthLabelConfirm), Value: "done"},
	)
	return opts
}

func authModelGroupFromID(id string) authView {
	switch id {
	case modelGroupBasics:
		return authViewModelBasics
	case modelGroupCapabilities:
		return authViewModelCapabilities
	case modelGroupSampling:
		return authViewModelSampling
	case modelGroupCost:
		return authViewModelCost
	case modelGroupCompat:
		return authViewModelCompat
	}
	return authViewModelGroupList
}

// --- Model field selection ---

func (a *App) selectModelFieldValue(value string) {
	if value == "done" {
		a.popAuthView()
		return
	}
	me := a.currentModelEdit()
	if me == nil {
		return
	}

	// Toggle fields
	switch value {
	case "reasoning":
		me.Reasoning = !me.Reasoning
		a.scheduleRender()
		return
	case "costEnabled":
		me.CostEnabled = !me.CostEnabled
		a.scheduleRender()
		return
	case "reqReasoningAsst":
		me.Compat.RequiresReasoningContentOnAssistant = !me.Compat.RequiresReasoningContentOnAssistant
		me.Compat.Active = true
		a.scheduleRender()
		return
	case "reqReasoningAsstMsgs":
		me.Compat.RequiresReasoningContentOnAssistantMessages = !me.Compat.RequiresReasoningContentOnAssistantMessages
		me.Compat.Active = true
		a.scheduleRender()
		return
	case "forceAdaptiveThinking":
		me.Compat.ForceAdaptiveThinking = !me.Compat.ForceAdaptiveThinking
		me.Compat.Active = true
		a.scheduleRender()
		return
	case "parseReasoningInContent":
		me.Compat.ParseReasoningInContent = !me.Compat.ParseReasoningInContent
		me.Compat.Active = true
		a.scheduleRender()
		return
	case "sessionAffinityHeaders":
		me.Compat.SendSessionAffinityHeaders = !me.Compat.SendSessionAffinityHeaders
		me.Compat.Active = true
		a.scheduleRender()
		return
	case "resetAll":
		me.Compat = compatEditState{}
		a.scheduleRender()
		return
	}

	// Tri-state pointer fields
	switch value {
	case "supportsDeveloperRole", "supportsStore", "supportsReasoningEffort", "supportsStrictMode",
		"disableSamplingParams", "cacheControlOnTools", "longCacheRetention", "promptCacheKey", "reasoningSummary", "eagerToolStreaming",
		"supportsToolChoice", "supportsParallelToolCalls":
		a.toggleModelTriState(value)
		a.scheduleRender()
		return
	}

	// Text input fields
	a.auth.ParamField = value
	a.auth.ParamFieldKey = ""
	a.prepareModelInput()
	a.scheduleRender()
}

// --- Model input submit ---

func (a *App) authModelInputPrompt() string {
	switch a.auth.ParamField {
	case "name":
		return a.translator.Text(i18n.MsgAuthPromptModelDisplayName)
	case "contextWindow":
		return a.translator.Text(i18n.MsgAuthPromptContextWindow)
	case "maxTokens":
		return a.translator.Text(i18n.MsgAuthPromptMaxOutputTokens)
	case "input":
		return a.translator.Text(i18n.MsgAuthPromptInputModalities)
	case "temperature":
		return a.translator.Text(i18n.MsgAuthPromptTemperature)
	case "topP":
		return a.translator.Text(i18n.MsgAuthPromptTopP)
	case "costInput":
		return a.translator.Text(i18n.MsgAuthPromptInputCost)
	case "costOutput":
		return a.translator.Text(i18n.MsgAuthPromptOutputCost)
	case "cacheRead":
		return a.translator.Text(i18n.MsgAuthPromptCacheReadCost)
	case "cacheWrite":
		return a.translator.Text(i18n.MsgAuthPromptCacheWriteCost)
	case "thinkingFormat":
		return a.translator.Text(i18n.MsgAuthPromptThinkingFormat)
	case "maxTokensField":
		return a.translator.Text(i18n.MsgAuthPromptMaxTokensField)
	}
	if strings.HasPrefix(a.auth.ParamField, "req") || a.auth.ParamField == "forceAdaptiveThinking" ||
		a.auth.ParamField == "parseReasoningInContent" || a.auth.ParamField == "sessionAffinityHeaders" {
		return a.translator.Text(i18n.MsgAuthPromptToggle)
	}
	if strings.HasPrefix(a.auth.ParamField, "supports") || a.auth.ParamField == "eagerToolStreaming" {
		return a.translator.Text(i18n.MsgAuthPromptCycleTriState)
	}
	return a.translator.Text(i18n.MsgAuthPromptInput)
}

func (a *App) authModelSubmitInput() error {
	value := strings.TrimSpace(a.authInput.Value())
	me := a.currentModelEdit()
	if me == nil {
		return errors.New(a.translator.Text(i18n.MsgAuthErrorNoModelSelected))
	}

	switch a.auth.ParamField {
	case "name":
		me.Name = value
		if me.Name == "" {
			me.Name = me.ID
		}
	case "contextWindow":
		if value != "" {
			v, err := a.parsePositiveInt(value)
			if err != nil {
				return errors.New(a.translator.Text(i18n.MsgAuthErrorContextWindowInvalid))
			}
			me.ContextWindow = v
		}
	case "maxTokens":
		if value != "" {
			v, err := a.parsePositiveInt(value)
			if err != nil {
				return errors.New(a.translator.Text(i18n.MsgAuthErrorMaxTokensInvalid))
			}
			me.MaxTokens = v
			me.MaxTokensEdited = true
		}
	case "input":
		ids := normalizeAuthModelIDs(value)
		if len(ids) == 0 {
			ids = []string{"text"}
		}
		me.Input = ids
	case "temperature":
		if value != "" {
			v, err := parseFloatRange(value, 0, 2)
			if err != nil {
				return errors.New(a.translator.Text(i18n.MsgAuthErrorTemperatureInvalid))
			}
			me.Temperature = &v
		} else {
			me.Temperature = nil
		}
	case "topP":
		if value != "" {
			v, err := parseFloatRange(value, 0, 1)
			if err != nil {
				return errors.New(a.translator.Text(i18n.MsgAuthErrorTopPInvalid))
			}
			me.TopP = &v
		} else {
			me.TopP = nil
		}
	case "costInput":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return errors.New(a.translator.Text(i18n.MsgAuthErrorInvalidNumber))
		}
		me.CostInput = v
	case "costOutput":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return errors.New(a.translator.Text(i18n.MsgAuthErrorInvalidNumber))
		}
		me.CostOutput = v
	case "cacheRead":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return errors.New(a.translator.Text(i18n.MsgAuthErrorInvalidNumber))
		}
		me.CacheRead = v
	case "cacheWrite":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return errors.New(a.translator.Text(i18n.MsgAuthErrorInvalidNumber))
		}
		me.CacheWrite = v
	case "thinkingFormat":
		me.Compat.ThinkingFormat = value
		me.Compat.Active = true
	case "maxTokensField":
		me.Compat.MaxTokensField = value
		me.Compat.Active = true
	}

	a.clearAuthParamField()
	return nil
}

// --- Tri-state pointer toggle from key handler ---

func (a *App) toggleModelTriState(field string) {
	me := a.currentModelEdit()
	if me == nil {
		return
	}
	ce := &me.Compat
	var p **bool
	switch field {
	case "supportsDeveloperRole":
		p = &ce.SupportsDeveloperRole
	case "supportsStore":
		p = &ce.SupportsStore
	case "supportsReasoningEffort":
		p = &ce.SupportsReasoningEffort
	case "supportsStrictMode":
		p = &ce.SupportsStrictMode
	case "disableSamplingParams":
		p = &ce.DisableSamplingParams
	case "cacheControlOnTools":
		p = &ce.SupportsCacheControlOnTools
	case "longCacheRetention":
		p = &ce.SupportsLongCacheRetention
	case "promptCacheKey":
		p = &ce.SupportsPromptCacheKey
	case "reasoningSummary":
		p = &ce.SupportsReasoningSummary
	case "eagerToolStreaming":
		p = &ce.SupportsEagerToolInputStreaming
	case "supportsToolChoice":
		p = &ce.SupportsToolChoice
	case "supportsParallelToolCalls":
		p = &ce.SupportsParallelToolCalls
	default:
		return
	}
	*p = cycleTriState(*p)
	ce.Active = true
}

// --- Settings detail ---

func (a *App) authSettingsDetailOptions() []authOption {
	opts := []authOption{
		{Title: a.translator.Text(i18n.MsgAuthProviderSettings), Description: a.auth.Provider.summaryShort(), Value: "providerGroups"},
	}
	for _, id := range a.auth.ModelOrder {
		if me, ok := a.auth.Models[id]; ok {
			opts = append(opts, authOption{
				Title:       "Model: " + id,
				Description: me.summaryShort(),
				Value:       "model:" + id,
			})
		}
	}
	opts = append(opts,
		authOption{Title: a.translator.Text(i18n.MsgAuthAddModel), Description: a.translator.Text(i18n.MsgAuthAddModel), Value: "addModel"},
		authOption{Title: a.translator.Text(i18n.MsgAuthFetchOnlineModels), Description: a.translator.Text(i18n.MsgAuthFetchOnlineModelsHint), Value: "fetchOnline"},
		authOption{Title: a.translator.Text(i18n.MsgAuthLabelSetAsDefault), Description: a.translator.Text(i18n.MsgAuthValueCurrent, a.boolYesNo(a.auth.SetDefault)), Value: "setDefault"},
		authOption{Title: a.translator.Text(i18n.MsgAuthReviewSave), Description: a.translator.Text(i18n.MsgAuthReviewSave), Value: "review"},
	)
	return opts
}

func (a *App) selectSettingsDetail(value string) tea.Cmd {
	switch value {
	case "providerGroups":
		a.pushAuthView(authViewProviderGroupList)
	case "addModel":
		a.pushAuthView(authViewAddModelID)
	case "fetchOnline":
		return a.startFetchOnlineModels()
	case "setDefault":
		a.auth.SetDefault = !a.auth.SetDefault
		a.scheduleRender()
	case "review":
		a.prepareAuthPreview()
		a.pushAuthView(authViewReview)
	default:
		if strings.HasPrefix(value, "model:") {
			modelID := strings.TrimPrefix(value, "model:")
			a.auth.CurrentModelID = modelID
			a.pushAuthView(authViewModelGroupList)
		}
	}
	return nil
}

// --- Utility ---

func (a *App) triStateStr(v *bool) string {
	if v == nil {
		return a.translator.Text(i18n.MsgAuthValueAuto)
	}
	return a.authBool(*v)
}

// samplingParamsStr describes the DisableSamplingParams tri-state, whose
// default (nil) disables sampling params.
func (a *App) samplingParamsStr(v *bool) string {
	if v == nil {
		return a.translator.Text(i18n.MsgAuthValueAutoDisabled)
	}
	if *v {
		return a.translator.Text(i18n.MsgAuthValueDisabled)
	}
	return a.translator.Text(i18n.MsgAuthValueEnabledSamplingSent)
}
