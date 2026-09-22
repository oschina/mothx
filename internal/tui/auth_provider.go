package tui

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/oschina/mothx/internal/tui/i18n"
)

// --- Provider group constants ---

const (
	providerGroupCredentials = "credentials"
	providerGroupProtocol    = "protocol"
	providerGroupNetwork     = "network"
	providerGroupAdvanced    = "advanced"
)

// providerGroupInfo describes a provider settings group.
type providerGroupInfo struct {
	ID          string
	Title       string
	Description string
}

var providerGroups = []providerGroupInfo{
	{providerGroupCredentials, "A. Credentials", "API Key, Vendor"},
	{providerGroupProtocol, "B. Protocol", "Base URL, Responses"},
	{providerGroupNetwork, "C. Network", "HTTP Proxy, Force HTTP/1.1"},
	{providerGroupAdvanced, "D. Advanced", "Headers, Thinking Format, Cache Control, Image Limit"},
}

// --- authViewProviderGroupList ---

func (a *App) authProviderGroupOptions() []authOption {
	pe := &a.auth.Provider
	opts := make([]authOption, 0, len(providerGroups)+2)
	// API Type at top level (select, not text input)
	apiDesc := pe.API
	if apiDesc == "" {
		apiDesc = a.translator.Text(i18n.MsgAuthValueUnset)
	}
	opts = append(opts, authOption{
		Title:       a.translator.Text(i18n.MsgAuthLabelAPIType),
		Description: apiDesc,
		Value:       "api",
	})
	for _, g := range providerGroups {
		desc := ""
		switch g.ID {
		case providerGroupCredentials:
			desc = a.authProviderCredentialsSummary(pe)
		case providerGroupProtocol:
			desc = a.authProviderProtocolSummary(pe)
		case providerGroupNetwork:
			desc = a.authProviderNetworkSummary(pe)
		case providerGroupAdvanced:
			desc = a.authProviderAdvancedSummary(pe)
		}
		opts = append(opts, authOption{
			Title:       a.translator.Text(providerGroupMessageID(g.ID)),
			Description: desc,
			Value:       g.ID,
		})
	}
	// Add Models entry at the end
	modelCount := len(a.auth.ModelOrder)
	opts = append(opts, authOption{
		Title:       a.translator.Text(i18n.MsgAuthLabelModels),
		Description: a.translator.Text(i18n.MsgAuthModelsCount, modelCount),
		Value:       "models",
	})
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthSave), Value: "done"})
	return opts
}

func (a *App) authProviderCredentialsSummary(pe *providerEditState) string {
	parts := []string{}
	if pe.APIKey != "" {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryKey, maskAuthSecret(pe.APIKey)))
	} else {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryKey, a.translator.Text(i18n.MsgAuthValueEmpty)))
	}
	if pe.Vendor != "" {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryVendor, pe.Vendor))
	}
	return strings.Join(parts, "  ")
}

func (a *App) authProviderProtocolSummary(pe *providerEditState) string {
	parts := []string{a.translator.Text(i18n.MsgAuthSummaryURL, shortURL(pe.BaseURL))}
	if pe.Responses.ReasoningSummary != "" {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryResponses, pe.Responses.ReasoningSummary))
	}
	return strings.Join(parts, "  ")
}

func (a *App) authProviderNetworkSummary(pe *providerEditState) string {
	proxy := a.translator.Text(i18n.MsgAuthValueNone)
	if pe.HTTPProxy != "" {
		proxy = shortURL(pe.HTTPProxy)
	}
	parts := []string{a.translator.Text(i18n.MsgAuthSummaryProxy, proxy)}
	if pe.ForceHTTP11 {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryForceHTTP11))
	} else {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryHTTP2))
	}
	return strings.Join(parts, "  ")
}

func (a *App) authProviderAdvancedSummary(pe *providerEditState) string {
	parts := []string{}
	if len(pe.Headers) > 0 {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryHeaders, len(pe.Headers)))
	} else {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryHeaders, 0))
	}
	if pe.ThinkingFormat != "" {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryThinking, pe.ThinkingFormat))
	}
	cache := a.translator.Text(i18n.MsgAuthValueAuto)
	if pe.CacheControl != nil {
		cache = a.translator.Text(i18n.MsgAuthValueEnabled)
		if !*pe.CacheControl {
			cache = a.translator.Text(i18n.MsgAuthValueDisabled)
		}
	}
	parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryCache, cache))
	parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryImages, a.imageLimitSummary(pe.MaxImagesPerRequest)))
	return strings.Join(parts, "  ")
}

// --- Provider sub-form options ---

func (a *App) authProviderCredentialsOptions() []authOption {
	pe := &a.auth.Provider
	opts := []authOption{
		{Title: a.translator.Text(i18n.MsgAuthLabelAPIKey), Description: maskAuthSecret(pe.APIKey), Value: "apiKey"},
		{Title: a.translator.Text(i18n.MsgAuthLabelVendor), Description: valueOrDefault(pe.Vendor, a.translator.Text(i18n.MsgAuthValueAuto)), Value: "vendor"},
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthLabelConfirm), Value: "done"})
	return opts
}

func (a *App) authProviderProtocolOptions() []authOption {
	pe := &a.auth.Provider
	opts := []authOption{
		{Title: a.translator.Text(i18n.MsgAuthLabelBaseURL), Description: pe.BaseURL, Value: "baseUrl"},
		{Title: a.translator.Text(i18n.MsgAuthLabelResponses), Description: a.authProviderResponsesSummary(&pe.Responses), Value: "responses"},
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthLabelConfirm), Value: "done"})
	return opts
}

func (a *App) authProviderNetworkOptions() []authOption {
	pe := &a.auth.Provider
	opts := []authOption{
		{Title: a.translator.Text(i18n.MsgAuthLabelHTTPProxy), Description: valueOrDefault(pe.HTTPProxy, a.translator.Text(i18n.MsgAuthValueNone)), Value: "httpProxy"},
		{Title: a.translator.Text(i18n.MsgAuthLabelForceHTTP11), Description: a.boolYesNo(pe.ForceHTTP11), Value: "forceHTTP11"},
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthLabelConfirm), Value: "done"})
	return opts
}

func (a *App) authProviderAdvancedOptions() []authOption {
	pe := &a.auth.Provider
	opts := []authOption{
		{Title: a.translator.Text(i18n.MsgAuthLabelHeaders), Description: a.translator.Text(i18n.MsgAuthValueHeaderCount, len(pe.Headers)), Value: "headers"},
		{Title: a.translator.Text(i18n.MsgAuthLabelThinkingFormat), Description: valueOrDefault(pe.ThinkingFormat, a.translator.Text(i18n.MsgAuthValueAuto)), Value: "thinkingFormat"},
		{Title: a.translator.Text(i18n.MsgAuthLabelCacheControl), Description: a.authCacheControlSummary(pe.CacheControl), Value: "cacheControl"},
		{Title: a.translator.Text(i18n.MsgAuthLabelMaxImages), Description: a.imageLimitSummary(pe.MaxImagesPerRequest), Value: "maxImagesPerRequest"},
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthLabelConfirm), Value: "done"})
	return opts
}

func (a *App) authProviderResponsesSummary(re *responsesEditState) string {
	parts := []string{}
	if re.ReasoningSummary != "" {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryResponses, re.ReasoningSummary))
	}
	cache := a.translator.Text(i18n.MsgAuthValueAuto)
	if re.PromptCacheEnabled != nil {
		cache = a.translator.Text(i18n.MsgAuthValueEnabled)
		if !*re.PromptCacheEnabled {
			cache = a.translator.Text(i18n.MsgAuthValueDisabled)
		}
	}
	parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryPromptCache, cache))
	if re.PromptCacheKey != "" {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryCacheKey))
	}
	if re.PromptCacheRetention != "" {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryRetention, re.PromptCacheRetention))
	}
	if re.ToolControl.Choice != "" {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryToolChoice, re.ToolControl.Choice))
	}
	if re.ToolControl.Parallel != nil {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryParallelTools, a.authBool(*re.ToolControl.Parallel)))
	}
	if re.ToolControl.MaxCalls > 0 {
		parts = append(parts, a.translator.Text(i18n.MsgAuthSummaryMaxToolCalls, re.ToolControl.MaxCalls))
	}
	if len(parts) == 0 {
		return a.translator.Text(i18n.MsgAuthSummaryDefaults)
	}
	return strings.Join(parts, "  ")
}

func (a *App) authCacheControlSummary(v *bool) string {
	if v == nil {
		return a.translator.Text(i18n.MsgAuthValueAuto)
	}
	return a.authBool(*v)
}

func (a *App) imageLimitSummary(limit int) string {
	switch {
	case limit < 0:
		return a.translator.Text(i18n.MsgAuthValueUnlimited)
	case limit == 0:
		return a.translator.Text(i18n.MsgAuthValueProviderDefault)
	default:
		return strconv.Itoa(limit)
	}
}

// toolMaxCallsSummary describes responses.toolControl.maxCalls, where zero or
// a negative value leaves the limit to the provider.
func (a *App) toolMaxCallsSummary(calls int) string {
	if calls <= 0 {
		return a.translator.Text(i18n.MsgAuthValueProviderDefault)
	}
	return strconv.Itoa(calls)
}

// toolParallelState describes the Responses parallel_tool_calls tri-state.
// Unlike other compat flags, parallel tool calls default to enabled for
// OpenAI Responses, so nil shows "auto (enabled)" rather than just "auto".
func (a *App) toolParallelState(v *bool) string {
	if v == nil {
		return a.translator.Text(i18n.MsgAuthValueAutoEnabled)
	}
	return a.authBool(*v)
}

// --- Responses sub-form ---

func (a *App) authResponsesOptions() []authOption {
	re := &a.auth.Provider.Responses
	opts := []authOption{
		{Title: a.translator.Text(i18n.MsgAuthLabelReasoningSummary), Description: valueOrDefault(re.ReasoningSummary, a.translator.Text(i18n.MsgAuthValueAuto)), Value: "reasoningSummary"},
		{Title: a.translator.Text(i18n.MsgAuthLabelPromptCacheEnabled), Description: a.authCacheControlSummary(re.PromptCacheEnabled), Value: "promptCacheEnabled"},
		{Title: a.translator.Text(i18n.MsgAuthLabelPromptCacheKey), Description: valueOrDefault(re.PromptCacheKey, a.translator.Text(i18n.MsgAuthValueAuto)), Value: "promptCacheKey"},
		{Title: a.translator.Text(i18n.MsgAuthLabelPromptCacheRetention), Description: valueOrDefault(re.PromptCacheRetention, a.translator.Text(i18n.MsgAuthValueProviderDefault)), Value: "promptCacheRetention"},
		{Title: a.translator.Text(i18n.MsgAuthLabelToolChoice), Description: valueOrDefault(re.ToolControl.Choice, a.translator.Text(i18n.MsgAuthValueProviderDefault)), Value: "toolChoice"},
		{Title: a.translator.Text(i18n.MsgAuthLabelToolParallel), Description: a.toolParallelState(re.ToolControl.Parallel), Value: "toolParallel"},
		{Title: a.translator.Text(i18n.MsgAuthLabelToolMaxCalls), Description: a.toolMaxCallsSummary(re.ToolControl.MaxCalls), Value: "toolMaxCalls"},
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthLabelConfirm), Value: "done"})
	return opts
}

// --- Headers editor ---

func (a *App) authHeadersOptions() []authOption {
	pe := &a.auth.Provider
	var keys []string
	for k := range pe.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	opts := make([]authOption, 0, len(keys)+2)
	for _, k := range keys {
		opts = append(opts, authOption{
			Title:       k,
			Description: pe.Headers[k],
			Value:       "edit:" + k,
		})
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthLabelAddHeader), Description: a.translator.Text(i18n.MsgAuthLabelAddHeader), Value: "add"})
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthLabelConfirm), Value: "done"})
	return opts
}

func (a *App) authViewAPIChoiceOptions() []authOption {
	return []authOption{
		{Title: a.translator.Text(i18n.MsgAuthAPIChoiceOpenAIChat), Description: "api: openai-chat", Value: "openai-chat"},
		{Title: a.translator.Text(i18n.MsgAuthAPIChoiceOpenAIResponses), Description: "api: openai-responses", Value: "openai-responses"},
		{Title: a.translator.Text(i18n.MsgAuthAPIChoiceAnthropic), Description: "api: anthropic-messages", Value: "anthropic-messages"},
		{Title: a.translator.Text(i18n.MsgAuthAPIChoiceGemini), Description: "api: google-gemini", Value: "google-gemini"},
		{Title: a.translator.Text(i18n.MsgAuthAPIChoiceVertex), Description: "api: google-vertex", Value: "google-vertex"},
	}
}

func (a *App) selectAPIChoice(api string) {
	if api == "" {
		return
	}
	oldAPI := a.auth.Provider.API
	oldDefaultURL := defaultBaseURLForAPI(oldAPI)
	a.auth.Provider.API = api
	if strings.TrimSpace(a.auth.Provider.BaseURL) == "" || a.auth.Provider.BaseURL == oldDefaultURL {
		a.auth.Provider.BaseURL = defaultBaseURLForAPI(api)
	}
	a.popAuthView()
}

// --- Input handling for provider sub-forms ---

func (a *App) authProviderInputPrompt() string {
	switch a.auth.ParamField {
	case "apiKey":
		return a.translator.Text(i18n.MsgAuthPromptAPIKey)
	case "vendor":
		return a.translator.Text(i18n.MsgAuthPromptVendor)
	case "api":
		return a.translator.Text(i18n.MsgAuthPromptAPIType)
	case "baseUrl":
		return a.translator.Text(i18n.MsgAuthPromptBaseURL)
	case "httpProxy":
		return a.translator.Text(i18n.MsgAuthPromptHTTPProxy)
	case "thinkingFormat":
		return a.translator.Text(i18n.MsgAuthPromptThinkingFormat)
	case "maxImagesPerRequest":
		return a.translator.Text(i18n.MsgAuthPromptMaxImages)
	case "reasoningSummary":
		return a.translator.Text(i18n.MsgAuthPromptReasoningSummary)
	case "promptCacheKey":
		return a.translator.Text(i18n.MsgAuthPromptPromptCacheKey)
	case "promptCacheRetention":
		return a.translator.Text(i18n.MsgAuthPromptPromptCacheRetention)
	case "toolChoice":
		return a.translator.Text(i18n.MsgAuthPromptToolChoice)
	case "toolMaxCalls":
		return a.translator.Text(i18n.MsgAuthPromptToolMaxCalls)
	case "headerKey":
		return a.translator.Text(i18n.MsgAuthPromptHeaderName)
	case "headerValue":
		return a.translator.Text(i18n.MsgAuthPromptHeaderValue, a.auth.ParamFieldKey)
	case "newModelID":
		return a.translator.Text(i18n.MsgAuthPromptModelID)
	case "newModelName":
		return a.translator.Text(i18n.MsgAuthPromptModelName, a.auth.CurrentModelID)
	default:
		return a.translator.Text(i18n.MsgAuthPromptInput)
	}
}

func (a *App) authProviderSubmitInput() error {
	value := strings.TrimSpace(a.authInput.Value())
	pe := &a.auth.Provider

	switch a.auth.ParamField {
	case "apiKey":
		pe.APIKey = value
	case "vendor":
		pe.Vendor = value
	case "api":
		if value == "" {
			return errors.New(a.translator.Text(i18n.MsgAuthErrorAPITypeRequired))
		}
		pe.API = value
	case "baseUrl":
		if value == "" {
			return errors.New(a.translator.Text(i18n.MsgAuthErrorBaseURLRequired))
		}
		pe.BaseURL = value
	case "httpProxy":
		pe.HTTPProxy = value
	case "thinkingFormat":
		pe.ThinkingFormat = value
	case "maxImagesPerRequest":
		if value == "" {
			pe.MaxImagesPerRequest = 0
			break
		}
		v, err := strconv.Atoi(value)
		if err != nil || v < -1 {
			return errors.New(a.translator.Text(i18n.MsgAuthErrorMaxImagesInvalid))
		}
		pe.MaxImagesPerRequest = v
	case "reasoningSummary":
		pe.Responses.ReasoningSummary = value
	case "promptCacheKey":
		pe.Responses.PromptCacheKey = value
	case "promptCacheRetention":
		pe.Responses.PromptCacheRetention = value
	case "toolChoice":
		if strings.ContainsAny(value, " \t\n\"'") {
			return errors.New(a.translator.Text(i18n.MsgAuthErrorToolChoiceInvalid))
		}
		pe.Responses.ToolControl.Choice = value
	case "toolMaxCalls":
		if value == "" {
			pe.Responses.ToolControl.MaxCalls = 0
			break
		}
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			return errors.New(a.translator.Text(i18n.MsgAuthErrorToolMaxCallsInvalid))
		}
		pe.Responses.ToolControl.MaxCalls = v
	case "headerKey":
		if value == "" {
			return errors.New(a.translator.Text(i18n.MsgAuthErrorHeaderRequired))
		}
		a.auth.ParamFieldKey = value
		a.auth.ParamField = "headerValue"
		a.prepareAuthProviderInput()
		return errStayInInput
	case "headerValue":
		if pe.Headers == nil {
			pe.Headers = map[string]string{}
		}
		pe.Headers[a.auth.ParamFieldKey] = value
		a.clearAuthParamField()
		return nil
	}
	a.clearAuthParamField()
	return nil
}

// errStayInInput is a sentinel error indicating the input handler should not
// advance to the next view (used when a flow requires two sequential inputs).
var errStayInInput = fmt.Errorf("stay in input")

// prepareAuthProviderInput sets up the editor for the current ParamField.
func (a *App) prepareAuthProviderInput() {
	prompt := a.authProviderInputPrompt()
	value := a.authProviderInputValue()
	a.authInput = a.newAuthInput(prompt)
	if value != "" {
		a.authInput = a.authInput.SetValue(value)
	}
}

func (a *App) authProviderInputValue() string {
	pe := &a.auth.Provider
	switch a.auth.ParamField {
	case "apiKey":
		return pe.APIKey
	case "vendor":
		return pe.Vendor
	case "api":
		return pe.API
	case "baseUrl":
		return pe.BaseURL
	case "httpProxy":
		return pe.HTTPProxy
	case "thinkingFormat":
		return pe.ThinkingFormat
	case "maxImagesPerRequest":
		if pe.MaxImagesPerRequest != 0 {
			return strconv.Itoa(pe.MaxImagesPerRequest)
		}
	case "reasoningSummary":
		return pe.Responses.ReasoningSummary
	case "promptCacheKey":
		return pe.Responses.PromptCacheKey
	case "promptCacheRetention":
		return pe.Responses.PromptCacheRetention
	case "toolChoice":
		return pe.Responses.ToolControl.Choice
	case "toolMaxCalls":
		if pe.Responses.ToolControl.MaxCalls > 0 {
			return strconv.Itoa(pe.Responses.ToolControl.MaxCalls)
		}
	case "headerValue":
		if v, ok := pe.Headers[a.auth.ParamFieldKey]; ok {
			return v
		}
	}
	return ""
}

// --- Provider field selection dispatcher ---

func (a *App) selectProviderFieldValue(value string) {
	switch value {
	case "done":
		a.popAuthView()
		return
	}

	// Special handling for toggle fields
	switch value {
	case "forceHTTP11":
		a.auth.Provider.ForceHTTP11 = !a.auth.Provider.ForceHTTP11
		a.scheduleRender()
		return
	case "cacheControl":
		a.auth.Provider.CacheControl = cycleTriState(a.auth.Provider.CacheControl)
		a.scheduleRender()
		return
	case "promptCacheEnabled":
		a.auth.Provider.Responses.PromptCacheEnabled = cycleTriState(a.auth.Provider.Responses.PromptCacheEnabled)
		a.scheduleRender()
		return
	case "toolParallel":
		a.auth.Provider.Responses.ToolControl.Parallel = cycleTriState(a.auth.Provider.Responses.ToolControl.Parallel)
		a.scheduleRender()
		return
	}

	// For "responses" jump to responses sub-form
	if value == "responses" {
		a.pushAuthView(authViewResponsesEdit)
		return
	}
	// For "headers" jump to headers editor
	if value == "headers" {
		a.pushAuthView(authViewHeadersEdit)
		return
	}
	// For "api" jump to API type choice list
	if value == "api" {
		a.pushAuthView(authViewAPIChoice)
		return
	}

	a.auth.ParamField = value
	a.auth.ParamFieldKey = ""
	a.prepareAuthProviderInput()
	a.scheduleRender()
}

// --- Headers editor selection ---

func (a *App) selectHeaderValue(value string) {
	if value == "done" {
		a.popAuthView()
		return
	}
	if value == "add" {
		a.auth.ParamField = "headerKey"
		a.auth.ParamFieldKey = ""
		a.prepareAuthProviderInput()
		a.scheduleRender()
		return
	}
	if strings.HasPrefix(value, "edit:") {
		key := strings.TrimPrefix(value, "edit:")
		a.auth.ParamField = "headerValue"
		a.auth.ParamFieldKey = key
		a.prepareAuthProviderInput()
		a.scheduleRender()
		return
	}
}

// --- Tri-state helper ---

func cycleTriState(v *bool) *bool {
	if v == nil {
		b := true
		return &b
	}
	if *v {
		b := false
		return &b
	}
	return nil // was false → back to auto (nil)
}

// --- Utility ---

func (a *App) authBool(v bool) string {
	if v {
		return a.translator.Text(i18n.MsgAuthValueEnabled)
	}
	return a.translator.Text(i18n.MsgAuthValueDisabled)
}

func (a *App) boolYesNo(v bool) string {
	if v {
		return a.translator.Text(i18n.MsgAuthValueYes)
	}
	return a.translator.Text(i18n.MsgAuthValueNo)
}

func valueOrDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func authViewProviderFromID(id string) authView {
	switch id {
	case providerGroupCredentials:
		return authViewProviderCredentials
	case providerGroupProtocol:
		return authViewProviderProtocol
	case providerGroupNetwork:
		return authViewProviderNetwork
	case providerGroupAdvanced:
		return authViewProviderAdvanced
	}
	return authViewProviderGroupList
}
