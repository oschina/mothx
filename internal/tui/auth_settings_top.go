package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/config"
	providerfactory "github.com/oschina/mothx/internal/provider/factory"
	"github.com/oschina/mothx/internal/tui/i18n"
)

func configuredTUILangScope() string {
	if project, err := config.LoadProjectSettingsSparse(); err == nil && project != nil && strings.TrimSpace(project.TUILang) != "" {
		return "project"
	}
	return "global"
}

func (a *App) projectLanguageScopeAvailable() bool {
	return config.IsProjectDir(a.currentCwd())
}

func (a *App) languageScopeLabel() string {
	if a.tuiLangScope == "project" {
		return a.translator.Text(i18n.MsgLanguageScopeProject)
	}
	return a.translator.Text(i18n.MsgLanguageScopeGlobal)
}

func (a *App) languageSourceLabel() string {
	if data, err := config.LoadProjectSettingsSparse(); err == nil && data != nil && strings.TrimSpace(data.TUILang) != "" {
		return a.translator.Text(i18n.MsgLanguageSourceProject)
	}
	return a.translator.Text(i18n.MsgLanguageSourceGlobal)
}

func (a *App) authSettingsRootOptions() []authOption {
	s := a.effectiveSettings()
	return []authOption{
		{Title: a.translator.Text(i18n.MsgSettingsCategoryProviders), Description: a.translator.Text(i18n.MsgSettingsSummaryProviders, len(s.Providers), valueOrDefault(s.DefaultProvider, a.translator.Text(i18n.MsgAuthValueUnset)), valueOrDefault(s.DefaultModel, a.translator.Text(i18n.MsgAuthValueUnset))), Value: "providers"},
		{Title: a.translator.Text(i18n.MsgSettingsCategoryDefaults), Description: a.translator.Text(i18n.MsgSettingsSummaryDefaults, valueOrDefault(s.DefaultMode, "yolo"), valueOrDefault(s.DefaultThinkingLevel, "medium")), Value: "defaults"},
		{Title: a.translator.Text(i18n.MsgSettingsCategoryBehavior), Description: a.translator.Text(i18n.MsgSettingsSummaryBehavior, valueOrDefault(s.Theme, "dark"), a.boolPtrSummary(s.EnablePlanTool, true)), Value: "behavior"},
		{Title: a.translator.Text(i18n.MsgSettingsCategoryWebSearch), Description: a.translator.Text(i18n.MsgSettingsSummaryWebSearch, a.boolPtrSummary(s.WebSearch.Enabled, false), valueOrDefault(s.WebSearch.Provider, "openai")), Value: "webSearch"},
		{Title: a.translator.Text(i18n.MsgSettingsCategoryContextFiles), Description: a.translator.Text(i18n.MsgSettingsSummaryContextFiles, a.boolYesNo(s.ContextFiles.Enabled), len(s.ContextFiles.ExtraFiles)), Value: "contextFiles"},
		{Title: a.translator.Text(i18n.MsgSettingsCategoryStatusLine), Description: a.translator.Text(i18n.MsgSettingsSummaryStatusLine, a.boolYesNo(s.StatusLine.Enabled), valueOrDefault(s.StatusLine.Type, "command")), Value: "statusLine"},
		{Title: a.translator.Text(i18n.MsgSettingsCategoryCompaction), Description: a.translator.Text(i18n.MsgSettingsSummaryCompaction, a.boolYesNo(s.Compaction.Enabled), authItoa(s.Compaction.ReserveTokens), authItoa(s.Compaction.KeepRecentTokens)), Value: "compaction"},
		{Title: a.translator.Text(i18n.MsgSettingsCategorySandbox), Description: a.translator.Text(i18n.MsgSettingsSummarySandbox, a.boolYesNo(s.Sandbox.Enabled), valueOrDefault(s.Sandbox.Level, "none")), Value: "sandbox"},
		{Title: a.translator.Text(i18n.MsgSettingsCategoryPaths), Description: a.translator.Text(i18n.MsgSettingsSummaryPaths, a.shortSettingValue(s.SessionDir)), Value: "paths"},
		{Title: a.translator.Text(i18n.MsgSettingsCategoryRetry), Description: a.translator.Text(i18n.MsgSettingsSummaryRetry, a.boolYesNo(s.Retry.Enabled), s.Retry.MaxRetries, s.Retry.BaseDelayMs), Value: "retry"},
		{Title: a.translator.Text(i18n.MsgSettingsCategoryApproval), Description: a.translator.Text(i18n.MsgSettingsSummaryApproval, a.boolPtrSummary(s.Approval.ConfirmBeforeWrite, true), len(s.Approval.BashWhitelist), len(s.Approval.BashBlacklist)), Value: "approval"},
		{Title: a.translator.Text(i18n.MsgSettingsLanguage), Description: a.translator.Text(i18n.MsgSettingsLanguageDescription, valueOrDefault(s.TUILang, "auto"), a.translator.Language(), a.tuiLangOffset, a.languageSourceLabel()), Value: "tuilang"},
		{Title: a.translator.Text(i18n.MsgSettingsLanguageScope), Description: a.translator.Text(i18n.MsgSettingsLanguageScopeDescription, a.languageScopeLabel()), Value: "tuilang.scope"},
		{Title: a.translator.Text(i18n.MsgSettingsLanguageSave), Description: a.translator.Text(i18n.MsgSettingsLanguageSaveDescription), Value: "tuilang.save"},
	}
}

func (a *App) selectSettingsRoot(value string) {
	switch value {
	case "providers":
		a.pushAuthView(authViewExistingProvider)
	case "defaults":
		a.pushAuthView(authViewSettingsDefaults)
	case "behavior":
		a.pushAuthView(authViewSettingsBehavior)
	case "webSearch":
		a.pushAuthView(authViewSettingsWebSearch)
	case "contextFiles":
		a.pushAuthView(authViewSettingsContextFiles)
	case "statusLine":
		a.pushAuthView(authViewSettingsStatusLine)
	case "compaction":
		a.pushAuthView(authViewSettingsCompaction)
	case "sandbox":
		a.pushAuthView(authViewSettingsSandbox)
	case "paths":
		a.pushAuthView(authViewSettingsPaths)
	case "retry":
		a.pushAuthView(authViewSettingsRetry)
	case "approval":
		a.pushAuthView(authViewSettingsApproval)
	case "tuilang", "tuilang.scope", "tuilang.save":
		a.selectSettingsFieldValue(value)
	}
}

func (a *App) authSettingsTopLevelOptions(v authView) []authOption {
	s := a.effectiveSettings()
	tr := a.translator.Text
	var opts []authOption
	switch v {
	case authViewSettingsDefaults:
		opts = []authOption{
			{Title: tr(i18n.MsgSettingsFieldDefaultModel), Description: fmt.Sprintf("%s / %s", valueOrDefault(s.DefaultProvider, tr(i18n.MsgAuthValueUnset)), valueOrDefault(s.DefaultModel, tr(i18n.MsgAuthValueUnset))), Value: "defaults.modelPicker"},
			{Title: tr(i18n.MsgSettingsFieldDefaultThinking), Description: valueOrDefault(s.DefaultThinkingLevel, "medium"), Value: "defaultThinkingLevel"},
			{Title: tr(i18n.MsgSettingsFieldDefaultMode), Description: valueOrDefault(s.DefaultMode, "yolo"), Value: "defaultMode"},
		}
	case authViewSettingsBehavior:
		toolExecution := effectiveToolExecutionSettings(s)
		opts = []authOption{
			{Title: tr(i18n.MsgSettingsFieldTheme), Description: valueOrDefault(s.Theme, "dark"), Value: "theme"},
			{Title: tr(i18n.MsgSettingsFieldEnablePlanTool), Description: a.boolPtrSummary(s.EnablePlanTool, true), Value: "enablePlanTool"},
			{Title: tr(i18n.MsgSettingsFieldEnableArtifact), Description: a.boolPtrSummary(s.EnableArtifact, false), Value: "enableArtifact"},
			{Title: tr(i18n.MsgSettingsFieldAuthored), Description: a.boolYesNo(s.Authored), Value: "authored"},
			{Title: tr(i18n.MsgSettingsFieldMaxContextTokens), Description: a.zeroAsUnset(s.MaxContextTokens), Value: "maxContextTokens"},
			{Title: tr(i18n.MsgSettingsFieldUpdateCheck), Description: a.boolPtrSummary(s.UpdateCheck, true), Value: "updateCheck"},
			{Title: tr(i18n.MsgSettingsFieldToolExecutionMode), Description: fmt.Sprintf("%s (%s)", toolExecution.Mode, tr(i18n.MsgSettingsToolExecutionLocalOnly)), Value: "toolExecution.mode"},
			{Title: tr(i18n.MsgSettingsFieldToolMaxConcurrency), Description: authItoa(toolExecution.MaxConcurrency), Value: "toolExecution.maxConcurrency"},
		}
	case authViewSettingsWebSearch:
		opts = []authOption{
			{Title: tr(i18n.MsgAuthLabelEnabled), Description: a.boolPtrSummary(s.WebSearch.Enabled, false), Value: "webSearch.enabled"},
			{Title: tr(i18n.MsgSettingsFieldProvider), Description: valueOrDefault(s.WebSearch.Provider, "openai"), Value: "webSearch.provider"},
			{Title: tr(i18n.MsgSettingsFieldProviderType), Description: valueOrDefault(s.WebSearch.ProviderType, "responses"), Value: "webSearch.providerType"},
			{Title: tr(i18n.MsgSettingsFieldModel), Description: valueOrDefault(s.WebSearch.Model, tr(i18n.MsgAuthValueUnset)), Value: "webSearch.model"},
			{Title: tr(i18n.MsgSettingsFieldImageEnabled), Description: a.boolPtrSummary(s.ImageGeneration.Enabled, false), Value: "imageGeneration.enabled"},
			{Title: tr(i18n.MsgSettingsFieldImageProvider), Description: valueOrDefault(s.ImageGeneration.Provider, "openai"), Value: "imageGeneration.provider"},
			{Title: tr(i18n.MsgSettingsFieldImageAPIType), Description: valueOrDefault(s.ImageGeneration.APIType, "openai-images"), Value: "imageGeneration.apiType"},
			{Title: tr(i18n.MsgSettingsFieldImageBaseURL), Description: a.shortSettingValue(s.ImageGeneration.BaseURL), Value: "imageGeneration.baseUrl"},
			{Title: tr(i18n.MsgSettingsFieldImageToken), Description: tr(i18n.MsgAuthValueHidden), Value: "imageGeneration.token"},
			{Title: tr(i18n.MsgSettingsFieldImageModel), Description: valueOrDefault(s.ImageGeneration.Model, "gpt-image-1"), Value: "imageGeneration.model"},
		}
	case authViewSettingsContextFiles:
		opts = []authOption{
			{Title: tr(i18n.MsgAuthLabelEnabled), Description: a.boolYesNo(s.ContextFiles.Enabled), Value: "contextFiles.enabled"},
			{Title: tr(i18n.MsgSettingsFieldExtraFiles), Description: a.listSummary(s.ContextFiles.ExtraFiles), Value: "contextFiles.extraFiles"},
		}
	case authViewSettingsStatusLine:
		opts = []authOption{
			{Title: tr(i18n.MsgAuthLabelEnabled), Description: a.boolYesNo(s.StatusLine.Enabled), Value: "statusLine.enabled"},
			{Title: tr(i18n.MsgAuthLabelType), Description: valueOrDefault(s.StatusLine.Type, "command"), Value: "statusLine.type"},
			{Title: tr(i18n.MsgAuthLabelCommand), Description: a.shortSettingValue(valueOrDefault(s.StatusLine.Command, "ccstatusline")), Value: "statusLine.command"},
			{Title: tr(i18n.MsgAuthLabelPadding), Description: authItoa(s.StatusLine.Padding), Value: "statusLine.padding"},
			{Title: tr(i18n.MsgAuthLabelRefreshInterval), Description: fmt.Sprintf("%ds", s.StatusLine.RefreshInterval), Value: "statusLine.refreshInterval"},
			{Title: tr(i18n.MsgAuthLabelTimeout), Description: fmt.Sprintf("%dms", s.StatusLine.TimeoutMs), Value: "statusLine.timeoutMs"},
			{Title: tr(i18n.MsgAuthLabelFallback), Description: valueOrDefault(s.StatusLine.Fallback, "builtin"), Value: "statusLine.fallback"},
		}
	case authViewSettingsCompaction:
		opts = []authOption{
			{Title: tr(i18n.MsgAuthLabelEnabled), Description: a.boolYesNo(s.Compaction.Enabled), Value: "compaction.enabled"},
			{Title: tr(i18n.MsgSettingsFieldReserveTokens), Description: authItoa(s.Compaction.ReserveTokens), Value: "compaction.reserveTokens"},
			{Title: tr(i18n.MsgSettingsFieldKeepRecentTokens), Description: authItoa(s.Compaction.KeepRecentTokens), Value: "compaction.keepRecentTokens"},
			{Title: tr(i18n.MsgSettingsFieldTokenizer), Description: valueOrDefault(s.Compaction.Tokenizer, tr(i18n.MsgAuthValueAuto)), Value: "compaction.tokenizer"},
			{Title: tr(i18n.MsgSettingsFieldTokenizerModel), Description: valueOrDefault(s.Compaction.TokenizerModel, tr(i18n.MsgAuthValueAuto)), Value: "compaction.tokenizerModel"},
			{Title: tr(i18n.MsgSettingsFieldTemplate), Description: a.shortSettingValue(s.Compaction.Template), Value: "compaction.template"},
		}
	case authViewSettingsSandbox:
		opts = []authOption{
			{Title: tr(i18n.MsgAuthLabelEnabled), Description: a.boolYesNo(s.Sandbox.Enabled), Value: "sandbox.enabled"},
			{Title: tr(i18n.MsgSettingsFieldLevel), Description: valueOrDefault(s.Sandbox.Level, "none"), Value: "sandbox.level"},
			{Title: tr(i18n.MsgSettingsFieldBwrapPath), Description: valueOrDefault(s.Sandbox.BwrapPath, tr(i18n.MsgAuthValueAuto)), Value: "sandbox.bwrapPath"},
			{Title: tr(i18n.MsgSettingsFieldAllowedRead), Description: a.listSummary(s.Sandbox.AllowedRead), Value: "sandbox.allowedRead"},
			{Title: tr(i18n.MsgSettingsFieldAllowedWrite), Description: a.listSummary(s.Sandbox.AllowedWrite), Value: "sandbox.allowedWrite"},
			{Title: tr(i18n.MsgSettingsFieldDeniedPaths), Description: a.listSummary(s.Sandbox.DeniedPaths), Value: "sandbox.deniedPaths"},
			{Title: tr(i18n.MsgSettingsFieldPassEnv), Description: a.listSummary(s.Sandbox.PassEnv), Value: "sandbox.passEnv"},
			{Title: tr(i18n.MsgSettingsFieldTmpSize), Description: valueOrDefault(s.Sandbox.TmpSize, "100m"), Value: "sandbox.tmpSize"},
		}
	case authViewSettingsPaths:
		opts = []authOption{
			{Title: tr(i18n.MsgSettingsFieldSessionDir), Description: a.shortSettingValue(s.SessionDir), Value: "sessionDir"},
			{Title: tr(i18n.MsgSettingsFieldSkillsDir), Description: a.shortSettingValue(s.SkillsDir), Value: "skillsDir"},
			{Title: tr(i18n.MsgSettingsFieldShellPath), Description: valueOrDefault(s.ShellPath, tr(i18n.MsgAuthValueDefaultShell)), Value: "shellPath"},
			{Title: tr(i18n.MsgSettingsFieldShellCommandPrefix), Description: valueOrDefault(s.ShellCommandPrefix, tr(i18n.MsgAuthValueNone)), Value: "shellCommandPrefix"},
		}
	case authViewSettingsRetry:
		opts = []authOption{
			{Title: tr(i18n.MsgAuthLabelEnabled), Description: a.boolYesNo(s.Retry.Enabled), Value: "retry.enabled"},
			{Title: tr(i18n.MsgSettingsFieldMaxRetries), Description: authItoa(s.Retry.MaxRetries), Value: "retry.maxRetries"},
			{Title: tr(i18n.MsgSettingsFieldBaseDelay), Description: fmt.Sprintf("%dms", s.Retry.BaseDelayMs), Value: "retry.baseDelayMs"},
		}
	case authViewSettingsApproval:
		opts = []authOption{
			{Title: tr(i18n.MsgSettingsFieldConfirmBeforeWrite), Description: a.boolPtrSummary(s.Approval.ConfirmBeforeWrite, true), Value: "approval.confirmBeforeWrite"},
			{Title: tr(i18n.MsgSettingsFieldBashWhitelist), Description: a.listSummary(s.Approval.BashWhitelist), Value: "approval.bashWhitelist"},
			{Title: tr(i18n.MsgSettingsFieldBashBlacklist), Description: a.listSummary(s.Approval.BashBlacklist), Value: "approval.bashBlacklist"},
		}
	}
	opts = append(opts, authOption{Title: tr(i18n.MsgSettingsDone), Description: tr(i18n.MsgSettingsReturn), Value: "done"})
	return opts
}

func (a *App) selectSettingsFieldValue(value string) {
	a.auth.Error = ""
	if value == "done" {
		a.popAuthView()
		return
	}
	if value == "defaults.modelPicker" {
		a.closeAuthDialog()
		a.openDefaultModelDialog("global")
		return
	}

	next := a.cloneEffectiveSettings()
	switch value {
	case "tuilang":
		next.TUILang = cycleString(next.TUILang, []string{"auto", "zh", "en"}, "auto")
		if err := a.saveTUILang(next.TUILang); err != nil {
			return
		}
	case "tuilang.scope":
		if a.tuiLangScope == "project" {
			a.tuiLangScope = "global"
		} else if !a.projectLanguageScopeAvailable() {
			a.auth.Error = a.translator.Text(i18n.MsgSettingsLanguageProjectUnavailable)
		} else {
			a.tuiLangScope = "project"
		}
		a.scheduleRender()
	case "tuilang.save":
		_ = a.saveTUILang(valueOrDefault(next.TUILang, "auto"))
	case "defaultThinkingLevel":
		next.DefaultThinkingLevel = cycleString(next.DefaultThinkingLevel, []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}, "medium")
		a.saveAuthSettingsPatch("defaultThinkingLevel", map[string]any{"defaultThinkingLevel": next.DefaultThinkingLevel})
	case "defaultMode":
		next.DefaultMode = cycleString(next.DefaultMode, []string{"plan", "agent", "yolo", "os"}, "yolo")
		a.saveAuthSettingsPatch("defaultMode", map[string]any{"defaultMode": next.DefaultMode})
	case "enablePlanTool":
		next.EnablePlanTool = cycleSettingsBoolPtr(next.EnablePlanTool, true)
		a.saveAuthSettingsPatch("enablePlanTool", map[string]any{"enablePlanTool": next.EnablePlanTool})
	case "enableArtifact":
		next.EnableArtifact = cycleSettingsBoolPtr(next.EnableArtifact, false)
		a.saveAuthSettingsPatch("enableArtifact", map[string]any{"enableArtifact": next.EnableArtifact})
	case "authored":
		next.Authored = !next.Authored
		a.saveAuthSettingsPatch("authored", map[string]any{"authored": next.Authored})
	case "updateCheck":
		next.UpdateCheck = cycleSettingsBoolPtr(next.UpdateCheck, true)
		a.saveAuthSettingsPatch("updateCheck", map[string]any{"updateCheck": next.UpdateCheck})
	case "webSearch.enabled":
		next.WebSearch.Enabled = cycleSettingsBoolPtr(next.WebSearch.Enabled, false)
		a.saveAuthSettingsPatch("webSearch.enabled", map[string]any{"webSearch": next.WebSearch})
	case "toolExecution.mode":
		toolExecution := effectiveToolExecutionSettings(next)
		toolExecution.Mode = cycleString(toolExecution.Mode, []string{"parallel", "sequential"}, "parallel")
		a.saveAuthSettingsPatch("toolExecution.mode", map[string]any{"toolExecution": toolExecution})
	case "imageGeneration.enabled":
		next.ImageGeneration.Enabled = cycleSettingsBoolPtr(next.ImageGeneration.Enabled, false)
		a.saveAuthSettingsPatch("imageGeneration.enabled", map[string]any{"imageGeneration": next.ImageGeneration})
	case "contextFiles.enabled":
		next.ContextFiles.Enabled = !next.ContextFiles.Enabled
		a.saveAuthSettingsPatch("contextFiles.enabled", map[string]any{"contextFiles": next.ContextFiles})
	case "statusLine.enabled":
		next.StatusLine.Enabled = !next.StatusLine.Enabled
		normalizeStatusLineDefaults(&next.StatusLine)
		a.saveAuthSettingsPatch("statusLine.enabled", map[string]any{"statusLine": next.StatusLine})
	case "compaction.enabled":
		next.Compaction.Enabled = !next.Compaction.Enabled
		a.saveAuthSettingsPatch("compaction.enabled", map[string]any{"compaction": next.Compaction})
	case "sandbox.enabled":
		next.Sandbox.Enabled = !next.Sandbox.Enabled
		a.saveAuthSettingsPatch("sandbox.enabled", map[string]any{"sandbox": next.Sandbox})
	case "sandbox.level":
		next.Sandbox.Level = cycleString(next.Sandbox.Level, []string{"none", "standard", "strict"}, "none")
		a.saveAuthSettingsPatch("sandbox.level", map[string]any{"sandbox": next.Sandbox})
	case "retry.enabled":
		next.Retry.Enabled = !next.Retry.Enabled
		a.saveAuthSettingsPatch("retry.enabled", map[string]any{"retry": next.Retry})
	case "approval.confirmBeforeWrite":
		next.Approval.ConfirmBeforeWrite = cycleSettingsBoolPtr(next.Approval.ConfirmBeforeWrite, true)
		a.saveAuthSettingsPatch("approval.confirmBeforeWrite", map[string]any{"approval": next.Approval})
	default:
		a.auth.ParamField = value
		a.prepareAuthSettingsInput()
	}
}

func (a *App) prepareAuthSettingsInput() {
	prompt := a.authSettingsInputPrompt()
	value := a.authSettingsInputValue()
	a.authInput = a.newAuthInput(prompt)
	if value != "" {
		a.authInput = a.authInput.SetValue(value)
	}
}

func (a *App) saveTUILang(value string) error {
	return a.saveTUILangForScope(value, a.tuiLangScope)
}

func (a *App) saveTUILangForScope(value, scope string) error {
	value = valueOrDefault(strings.TrimSpace(strings.ToLower(value)), "auto")
	var err error
	if scope == "project" {
		if !a.projectLanguageScopeAvailable() {
			err = errors.New(a.translator.Text(i18n.MsgSettingsLanguageProjectUnavailable))
		} else {
			err = config.SaveProjectSettingsPatch(map[string]any{"tuilang": value})
		}
	} else {
		scope = "global"
		err = config.SaveGlobalSettingsPatch(map[string]any{"tuilang": value})
	}
	if err != nil {
		a.auth.Error = a.translator.Text(i18n.MsgSettingsLanguageSaveFailed, err)
		a.scheduleRender()
		return err
	}

	a.tuiLangScope = scope
	effectiveValue := value
	if project, loadErr := config.LoadProjectSettingsSparse(); loadErr == nil && strings.TrimSpace(project.TUILang) != "" {
		effectiveValue = project.TUILang
	} else if global, loadErr := config.LoadGlobalSettingsSparse(); loadErr == nil && strings.TrimSpace(global.TUILang) != "" {
		effectiveValue = global.TUILang
	}
	if a.settings != nil {
		a.settings.TUILang = effectiveValue
	}
	configured, _ := i18n.ParseConfigured(effectiveValue)
	a.tuiLangConfigured = configured
	now := time.Now()
	a.translator = i18n.New(i18n.Resolve(configured, now, time.Local))
	a.tuiLangOffset = i18n.UTCOffset(now, time.Local)
	a.input = a.input.SetPlaceholder(a.translator.Text(i18n.MsgInputPlaceholder))
	a.suggest = a.suggest.SetItems(commandSuggestionItems(a.translator))
	a.invalidateToolModalCache()
	a.statusLineLastAttempt = ""
	a.statusLineLastSuccess = ""
	if a.auth.ParamField != "" {
		a.authInput = a.authInput.SetPlaceholder(a.authInputPrompt(a.auth.View))
	}
	a.auth.Error = ""
	a.addCommandStatus(a.translator.Text(i18n.MsgSettingsLanguageSaved, a.languageScopeLabel(), value, a.translator.Language()))
	a.scheduleRender()
	return nil
}

func (a *App) authSettingsInputPrompt() string {
	var id i18n.MessageID
	switch a.auth.ParamField {
	case "theme":
		id = i18n.MsgSettingsPromptTheme
	case "maxContextTokens":
		id = i18n.MsgSettingsPromptMaxContextTokens
	case "webSearch.provider":
		id = i18n.MsgSettingsPromptWebProvider
	case "webSearch.providerType":
		id = i18n.MsgSettingsPromptWebProviderType
	case "webSearch.model":
		id = i18n.MsgSettingsPromptWebModel
	case "toolExecution.maxConcurrency":
		id = i18n.MsgSettingsPromptToolMaxConcurrency
	case "imageGeneration.provider":
		id = i18n.MsgSettingsPromptImageProvider
	case "imageGeneration.apiType":
		id = i18n.MsgSettingsPromptImageAPIType
	case "imageGeneration.baseUrl":
		id = i18n.MsgSettingsPromptImageBaseURL
	case "imageGeneration.token":
		id = i18n.MsgSettingsPromptImageToken
	case "imageGeneration.model":
		id = i18n.MsgSettingsPromptImageModel
	case "contextFiles.extraFiles":
		id = i18n.MsgSettingsPromptExtraFiles
	case "statusLine.type":
		id = i18n.MsgSettingsPromptStatusLineType
	case "statusLine.command":
		id = i18n.MsgSettingsPromptStatusLineCommand
	case "statusLine.padding":
		id = i18n.MsgSettingsPromptStatusLinePadding
	case "statusLine.refreshInterval":
		id = i18n.MsgSettingsPromptRefreshInterval
	case "statusLine.timeoutMs":
		id = i18n.MsgSettingsPromptTimeoutMS
	case "statusLine.fallback":
		id = i18n.MsgSettingsPromptStatusLineFallback
	case "compaction.reserveTokens":
		id = i18n.MsgSettingsPromptReserveTokens
	case "compaction.keepRecentTokens":
		id = i18n.MsgSettingsPromptKeepRecentTokens
	case "compaction.tokenizer":
		id = i18n.MsgSettingsPromptTokenizer
	case "compaction.tokenizerModel":
		id = i18n.MsgSettingsPromptTokenizerModel
	case "compaction.template":
		id = i18n.MsgSettingsPromptTemplate
	case "sandbox.bwrapPath":
		id = i18n.MsgSettingsPromptBwrapPath
	case "sandbox.allowedRead", "sandbox.allowedWrite", "sandbox.deniedPaths", "sandbox.passEnv":
		id = i18n.MsgSettingsPromptListValues
	case "sandbox.tmpSize":
		id = i18n.MsgSettingsPromptTmpSize
	case "sessionDir":
		id = i18n.MsgSettingsPromptSessionDir
	case "skillsDir":
		id = i18n.MsgSettingsPromptSkillsDir
	case "shellPath":
		id = i18n.MsgSettingsPromptShellPath
	case "shellCommandPrefix":
		id = i18n.MsgSettingsPromptShellPrefix
	case "retry.maxRetries":
		id = i18n.MsgSettingsPromptMaxRetries
	case "retry.baseDelayMs":
		id = i18n.MsgSettingsPromptBaseDelay
	case "approval.bashWhitelist", "approval.bashBlacklist":
		id = i18n.MsgSettingsPromptApprovalPrefixes
	default:
		id = i18n.MsgAuthPromptInput
	}
	return a.translator.Text(id)
}

func (a *App) authSettingsInputValue() string {
	s := a.effectiveSettings()
	switch a.auth.ParamField {
	case "theme":
		return s.Theme
	case "maxContextTokens":
		return intInputValue(s.MaxContextTokens)
	case "webSearch.provider":
		return s.WebSearch.Provider
	case "webSearch.providerType":
		return s.WebSearch.ProviderType
	case "webSearch.model":
		return s.WebSearch.Model
	case "toolExecution.maxConcurrency":
		return intInputValue(effectiveToolExecutionSettings(s).MaxConcurrency)
	case "imageGeneration.provider":
		return s.ImageGeneration.Provider
	case "imageGeneration.apiType":
		return s.ImageGeneration.APIType
	case "imageGeneration.baseUrl":
		return s.ImageGeneration.BaseURL
	case "imageGeneration.token":
		return s.ImageGeneration.Token
	case "imageGeneration.model":
		return s.ImageGeneration.Model
	case "contextFiles.extraFiles":
		return strings.Join(s.ContextFiles.ExtraFiles, ", ")
	case "statusLine.type":
		return s.StatusLine.Type
	case "statusLine.command":
		return s.StatusLine.Command
	case "statusLine.padding":
		return intInputValue(s.StatusLine.Padding)
	case "statusLine.refreshInterval":
		return intInputValue(s.StatusLine.RefreshInterval)
	case "statusLine.timeoutMs":
		return intInputValue(s.StatusLine.TimeoutMs)
	case "statusLine.fallback":
		return s.StatusLine.Fallback
	case "compaction.reserveTokens":
		return intInputValue(s.Compaction.ReserveTokens)
	case "compaction.keepRecentTokens":
		return intInputValue(s.Compaction.KeepRecentTokens)
	case "compaction.tokenizer":
		return s.Compaction.Tokenizer
	case "compaction.tokenizerModel":
		return s.Compaction.TokenizerModel
	case "compaction.template":
		return s.Compaction.Template
	case "sandbox.bwrapPath":
		return s.Sandbox.BwrapPath
	case "sandbox.allowedRead":
		return strings.Join(s.Sandbox.AllowedRead, ", ")
	case "sandbox.allowedWrite":
		return strings.Join(s.Sandbox.AllowedWrite, ", ")
	case "sandbox.deniedPaths":
		return strings.Join(s.Sandbox.DeniedPaths, ", ")
	case "sandbox.passEnv":
		return strings.Join(s.Sandbox.PassEnv, ", ")
	case "sandbox.tmpSize":
		return s.Sandbox.TmpSize
	case "sessionDir":
		return s.SessionDir
	case "skillsDir":
		return s.SkillsDir
	case "shellPath":
		return s.ShellPath
	case "shellCommandPrefix":
		return s.ShellCommandPrefix
	case "retry.maxRetries":
		return intInputValue(s.Retry.MaxRetries)
	case "retry.baseDelayMs":
		return intInputValue(s.Retry.BaseDelayMs)
	case "approval.bashWhitelist":
		return strings.Join(s.Approval.BashWhitelist, "\n")
	case "approval.bashBlacklist":
		return strings.Join(s.Approval.BashBlacklist, "\n")
	default:
		return ""
	}
}

func (a *App) authSettingsSubmitInput() error {
	field := a.auth.ParamField
	rawValue := a.authInput.Value()
	value := strings.TrimSpace(rawValue)
	next := a.cloneEffectiveSettings()
	updates := map[string]any{}

	switch field {
	case "theme":
		next.Theme = value
		updates["theme"] = next.Theme
	case "maxContextTokens":
		v, err := a.parseNonNegativeInt(value)
		if err != nil {
			return err
		}
		next.MaxContextTokens = v
		updates["maxContextTokens"] = next.MaxContextTokens
	case "webSearch.provider":
		next.WebSearch.Provider = value
		updates["webSearch"] = next.WebSearch
	case "webSearch.providerType":
		next.WebSearch.ProviderType = value
		updates["webSearch"] = next.WebSearch
	case "webSearch.model":
		next.WebSearch.Model = value
		updates["webSearch"] = next.WebSearch
	case "toolExecution.maxConcurrency":
		v, err := a.parsePositiveInt(value)
		if err != nil {
			return err
		}
		toolExecution := effectiveToolExecutionSettings(next)
		toolExecution.MaxConcurrency = v
		updates["toolExecution"] = toolExecution
	case "imageGeneration.provider":
		next.ImageGeneration.Provider = value
		updates["imageGeneration"] = next.ImageGeneration
	case "imageGeneration.apiType":
		next.ImageGeneration.APIType = value
		updates["imageGeneration"] = next.ImageGeneration
	case "imageGeneration.baseUrl":
		next.ImageGeneration.BaseURL = value
		updates["imageGeneration"] = next.ImageGeneration
	case "imageGeneration.token":
		next.ImageGeneration.Token = value
		updates["imageGeneration"] = next.ImageGeneration
	case "imageGeneration.model":
		next.ImageGeneration.Model = value
		updates["imageGeneration"] = next.ImageGeneration
	case "contextFiles.extraFiles":
		next.ContextFiles.ExtraFiles = parseSettingsList(value)
		updates["contextFiles"] = next.ContextFiles
	case "statusLine.type":
		next.StatusLine.Type = value
		normalizeStatusLineDefaults(&next.StatusLine)
		updates["statusLine"] = next.StatusLine
	case "statusLine.command":
		next.StatusLine.Command = value
		normalizeStatusLineDefaults(&next.StatusLine)
		updates["statusLine"] = next.StatusLine
	case "statusLine.padding":
		v, err := a.parseNonNegativeInt(value)
		if err != nil {
			return err
		}
		next.StatusLine.Padding = v
		updates["statusLine"] = next.StatusLine
	case "statusLine.refreshInterval":
		v, err := a.parseNonNegativeInt(value)
		if err != nil {
			return err
		}
		next.StatusLine.RefreshInterval = v
		updates["statusLine"] = next.StatusLine
	case "statusLine.timeoutMs":
		v, err := a.parsePositiveInt(value)
		if err != nil {
			return err
		}
		next.StatusLine.TimeoutMs = v
		updates["statusLine"] = next.StatusLine
	case "statusLine.fallback":
		next.StatusLine.Fallback = value
		normalizeStatusLineDefaults(&next.StatusLine)
		updates["statusLine"] = next.StatusLine
	case "compaction.reserveTokens":
		v, err := a.parseNonNegativeInt(value)
		if err != nil {
			return err
		}
		next.Compaction.ReserveTokens = v
		updates["compaction"] = next.Compaction
	case "compaction.keepRecentTokens":
		v, err := a.parseNonNegativeInt(value)
		if err != nil {
			return err
		}
		next.Compaction.KeepRecentTokens = v
		updates["compaction"] = next.Compaction
	case "compaction.tokenizer":
		next.Compaction.Tokenizer = value
		updates["compaction"] = next.Compaction
	case "compaction.tokenizerModel":
		next.Compaction.TokenizerModel = value
		updates["compaction"] = next.Compaction
	case "compaction.template":
		next.Compaction.Template = value
		updates["compaction"] = next.Compaction
	case "sandbox.bwrapPath":
		next.Sandbox.BwrapPath = value
		updates["sandbox"] = next.Sandbox
	case "sandbox.allowedRead":
		next.Sandbox.AllowedRead = parseSettingsList(value)
		updates["sandbox"] = next.Sandbox
	case "sandbox.allowedWrite":
		next.Sandbox.AllowedWrite = parseSettingsList(value)
		updates["sandbox"] = next.Sandbox
	case "sandbox.deniedPaths":
		next.Sandbox.DeniedPaths = parseSettingsList(value)
		updates["sandbox"] = next.Sandbox
	case "sandbox.passEnv":
		next.Sandbox.PassEnv = parseSettingsList(value)
		updates["sandbox"] = next.Sandbox
	case "sandbox.tmpSize":
		next.Sandbox.TmpSize = value
		updates["sandbox"] = next.Sandbox
	case "sessionDir":
		next.SessionDir = value
		updates["sessionDir"] = next.SessionDir
	case "skillsDir":
		next.SkillsDir = value
		updates["skillsDir"] = next.SkillsDir
	case "shellPath":
		next.ShellPath = value
		updates["shellPath"] = next.ShellPath
	case "shellCommandPrefix":
		next.ShellCommandPrefix = value
		updates["shellCommandPrefix"] = next.ShellCommandPrefix
	case "retry.maxRetries":
		v, err := a.parseNonNegativeInt(value)
		if err != nil {
			return err
		}
		next.Retry.MaxRetries = v
		updates["retry"] = next.Retry
	case "retry.baseDelayMs":
		v, err := a.parseNonNegativeInt(value)
		if err != nil {
			return err
		}
		next.Retry.BaseDelayMs = v
		updates["retry"] = next.Retry
	case "approval.bashWhitelist":
		next.Approval.BashWhitelist = parseApprovalPrefixes(rawValue)
		updates["approval"] = next.Approval
	case "approval.bashBlacklist":
		next.Approval.BashBlacklist = parseApprovalPrefixes(rawValue)
		updates["approval"] = next.Approval
	default:
		return errors.New(a.translator.Text(i18n.MsgAuthSettingsUnknownField))
	}

	if err := a.saveAuthSettingsPatch(field, updates); err != nil {
		return err
	}
	a.clearAuthParamField()
	return nil
}

func (a *App) saveAuthSettingsPatch(label string, updates map[string]any) error {
	if err := config.SaveGlobalSettingsPatch(updates); err != nil {
		a.auth.Error = a.translator.Text(i18n.MsgAuthSettingsSaveFailed, err)
		a.scheduleRender()
		return err
	}
	effective, err := config.LoadSettings()
	if err != nil {
		a.auth.Error = a.translator.Text(i18n.MsgAuthSettingsReloadFailed, err)
		a.scheduleRender()
		return err
	}
	a.settings = effective
	a.applyRuntimeSettingsAfterSave(label, effective)
	a.auth.Error = ""
	a.addCommandStatus(a.translator.Text(i18n.MsgAuthSettingsSaved, label))
	return nil
}

func (a *App) applyRuntimeSettingsAfterSave(label string, effective *config.Settings) {
	if strings.HasPrefix(label, "retry.") && effective != nil {
		providerfactory.ConfigureRetry(a.provider, effective)
	}
	a.syncAgentManagerRuntime()
	if label == "defaultMode" && effective != nil && strings.TrimSpace(effective.DefaultMode) != "" {
		a.mode = effective.DefaultMode
	}
	if label == "enableArtifact" && effective != nil && a.runtime != nil {
		_ = a.runtime.SetArtifactEnabled(effective.IsArtifactEnabled())
	}
	if strings.HasPrefix(label, "statusLine.") {
		a.invalidateStatusLineRequests()
		a.statusLineIntervalInit = false
		if !a.statusLineEnabled() {
			a.statusLineOutput = ""
			a.statusLineLastError = ""
			a.statusLineLastSuccess = ""
			a.statusLineLastAttempt = ""
			a.statusLinePending = nil
			a.statusLineInFlight = false
		} else if a.ready && a.width > 0 {
			a.requestStatusLineRefresh(true)
		}
	}
	if strings.HasPrefix(label, "sandbox.") || label == "sessionDir" || label == "skillsDir" || label == "shellPath" || label == "shellCommandPrefix" {
		a.addCommandStatus(a.translator.Text(i18n.MsgSettingsRuntimeReloadNote))
	}
	a.rebuildAgentWithCurrentConfig(fmt.Errorf("settings changed"))
	a.scheduleRender()
}

func (a *App) effectiveSettings() *config.Settings {
	if a.settings != nil {
		return a.settings
	}
	return config.DefaultSettings()
}

type toolExecutionSettingsView struct {
	Mode           string
	MaxConcurrency int
}

// effectiveToolExecutionSettings keeps the settings UI on the same normalized
// values used by the agent runtime, including defaults for older files.
func effectiveToolExecutionSettings(s *config.Settings) toolExecutionSettingsView {
	out := toolExecutionSettingsView{
		Mode:           "parallel",
		MaxConcurrency: config.DefaultToolExecutionMaxConcurrency,
	}
	if s == nil {
		return out
	}
	out.Mode = s.ToolExecution.EffectiveMode()
	out.MaxConcurrency = s.ToolExecution.EffectiveMaxConcurrency()
	return out
}

func (a *App) cloneEffectiveSettings() *config.Settings {
	src := a.effectiveSettings()
	data, err := json.Marshal(src)
	if err != nil {
		cp := *src
		return &cp
	}
	var out config.Settings
	if err := json.Unmarshal(data, &out); err != nil {
		cp := *src
		return &cp
	}
	return &out
}

func normalizeStatusLineDefaults(s *config.StatusLineSettings) {
	if s == nil {
		return
	}
	if s.Type == "" {
		s.Type = "command"
	}
	if s.Enabled && strings.TrimSpace(s.Command) == "" {
		s.Command = "ccstatusline"
	}
	if s.TimeoutMs == 0 {
		s.TimeoutMs = 800
	}
	if s.Fallback == "" {
		s.Fallback = "builtin"
	}
}

func (a *App) parseNonNegativeInt(s string) (int, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v < 0 {
		return 0, errors.New(a.translator.Text(i18n.MsgSettingsErrorNonNegativeInteger))
	}
	return v, nil
}

func parseSettingsList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\t' })
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		value := strings.TrimSpace(f)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func parseApprovalPrefixes(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	seen := map[string]bool{}
	var out []string
	for _, line := range lines {
		value := strings.TrimSpace(line)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cycleString(current string, values []string, fallback string) string {
	current = strings.TrimSpace(current)
	for i, value := range values {
		if current == value {
			return values[(i+1)%len(values)]
		}
	}
	return fallback
}

func cycleSettingsBoolPtr(v *bool, defaultValue bool) *bool {
	if v == nil {
		b := !defaultValue
		return &b
	}
	if *v != defaultValue {
		b := defaultValue
		return &b
	}
	return nil
}

func (a *App) boolPtrSummary(v *bool, defaultValue bool) string {
	if v == nil {
		if defaultValue {
			return a.translator.Text(i18n.MsgAuthValueAutoEnabled)
		}
		return a.translator.Text(i18n.MsgAuthValueAutoDisabled)
	}
	if *v {
		return a.translator.Text(i18n.MsgAuthValueEnabled)
	}
	return a.translator.Text(i18n.MsgAuthValueDisabled)
}

func (a *App) zeroAsUnset(v int) string {
	if v == 0 {
		return a.translator.Text(i18n.MsgAuthValueUnset)
	}
	return strconv.Itoa(v)
}

func intInputValue(v int) string {
	return strconv.Itoa(v)
}

func (a *App) listSummary(values []string) string {
	if len(values) == 0 {
		return a.translator.Text(i18n.MsgAuthValueEmpty)
	}
	if len(values) == 1 {
		return a.shortSettingValue(values[0])
	}
	return a.translator.Text(i18n.MsgAuthValueEntries, len(values))
}

func (a *App) shortSettingValue(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return a.translator.Text(i18n.MsgAuthValueUnset)
	}
	if len([]rune(s)) <= 42 {
		return s
	}
	r := []rune(s)
	return string(r[:39]) + "..."
}
