package i18n

const (
	MsgSettingsCategoryProviders    MessageID = "settings.category.providers"
	MsgSettingsCategoryDefaults     MessageID = "settings.category.defaults"
	MsgSettingsCategoryBehavior     MessageID = "settings.category.behavior"
	MsgSettingsCategoryWebSearch    MessageID = "settings.category.web_search"
	MsgSettingsCategoryContextFiles MessageID = "settings.category.context_files"
	MsgSettingsCategoryStatusLine   MessageID = "settings.category.status_line"
	MsgSettingsCategoryCompaction   MessageID = "settings.category.compaction"
	MsgSettingsCategorySandbox      MessageID = "settings.category.sandbox"
	MsgSettingsCategoryPaths        MessageID = "settings.category.paths"
	MsgSettingsCategoryRetry        MessageID = "settings.category.retry"
	MsgSettingsCategoryApproval     MessageID = "settings.category.approval"

	MsgSettingsSummaryProviders    MessageID = "settings.summary.providers"
	MsgSettingsSummaryDefaults     MessageID = "settings.summary.defaults"
	MsgSettingsSummaryBehavior     MessageID = "settings.summary.behavior"
	MsgSettingsSummaryWebSearch    MessageID = "settings.summary.web_search"
	MsgSettingsSummaryContextFiles MessageID = "settings.summary.context_files"
	MsgSettingsSummaryStatusLine   MessageID = "settings.summary.status_line"
	MsgSettingsSummaryCompaction   MessageID = "settings.summary.compaction"
	MsgSettingsSummarySandbox      MessageID = "settings.summary.sandbox"
	MsgSettingsSummaryPaths        MessageID = "settings.summary.paths"
	MsgSettingsSummaryRetry        MessageID = "settings.summary.retry"
	MsgSettingsSummaryApproval     MessageID = "settings.summary.approval"

	MsgSettingsFieldDefaultModel       MessageID = "settings.field.default_model"
	MsgSettingsFieldDefaultThinking    MessageID = "settings.field.default_thinking"
	MsgSettingsFieldDefaultMode        MessageID = "settings.field.default_mode"
	MsgSettingsFieldTheme              MessageID = "settings.field.theme"
	MsgSettingsFieldEnablePlanTool     MessageID = "settings.field.enable_plan_tool"
	MsgSettingsFieldEnableArtifact     MessageID = "settings.field.enable_artifact"
	MsgSettingsFieldAuthored           MessageID = "settings.field.authored"
	MsgSettingsFieldMaxContextTokens   MessageID = "settings.field.max_context_tokens"
	MsgSettingsFieldUpdateCheck        MessageID = "settings.field.update_check"
	MsgSettingsFieldProvider           MessageID = "settings.field.provider"
	MsgSettingsFieldProviderType       MessageID = "settings.field.provider_type"
	MsgSettingsFieldModel              MessageID = "settings.field.model"
	MsgSettingsFieldToolExecutionMode  MessageID = "settings.field.tool_execution_mode"
	MsgSettingsFieldToolMaxConcurrency MessageID = "settings.field.tool_max_concurrency"
	MsgSettingsToolExecutionLocalOnly  MessageID = "settings.tool_execution.local_only"
	MsgSettingsFieldImageEnabled       MessageID = "settings.field.image_generation_enabled"
	MsgSettingsFieldImageProvider      MessageID = "settings.field.image_generation_provider"
	MsgSettingsFieldImageAPIType       MessageID = "settings.field.image_generation_api_type"
	MsgSettingsFieldImageBaseURL       MessageID = "settings.field.image_generation_base_url"
	MsgSettingsFieldImageToken         MessageID = "settings.field.image_generation_token"
	MsgSettingsFieldWebSearchBaseURL   MessageID = "settings.field.web_search_base_url"
	MsgSettingsFieldWebSearchToken     MessageID = "settings.field.web_search_token"
	MsgSettingsFieldWebSearchBackend   MessageID = "settings.field.web_search_backend"
	MsgSettingsFieldImageModel         MessageID = "settings.field.image_generation_model"
	MsgSettingsFieldExtraFiles         MessageID = "settings.field.extra_files"
	MsgSettingsFieldReserveTokens      MessageID = "settings.field.reserve_tokens"
	MsgSettingsFieldKeepRecentTokens   MessageID = "settings.field.keep_recent_tokens"
	MsgSettingsFieldTokenizer          MessageID = "settings.field.tokenizer"
	MsgSettingsFieldTokenizerModel     MessageID = "settings.field.tokenizer_model"
	MsgSettingsFieldTemplate           MessageID = "settings.field.template"
	MsgSettingsFieldLevel              MessageID = "settings.field.level"
	MsgSettingsFieldBwrapPath          MessageID = "settings.field.bwrap_path"
	MsgSettingsFieldAllowedRead        MessageID = "settings.field.allowed_read"
	MsgSettingsFieldAllowedWrite       MessageID = "settings.field.allowed_write"
	MsgSettingsFieldDeniedPaths        MessageID = "settings.field.denied_paths"
	MsgSettingsFieldPassEnv            MessageID = "settings.field.pass_env"
	MsgSettingsFieldTmpSize            MessageID = "settings.field.tmp_size"
	MsgSettingsFieldSessionDir         MessageID = "settings.field.session_dir"
	MsgSettingsFieldSkillsDir          MessageID = "settings.field.skills_dir"
	MsgSettingsFieldShellPath          MessageID = "settings.field.shell_path"
	MsgSettingsFieldShellCommandPrefix MessageID = "settings.field.shell_command_prefix"
	MsgSettingsFieldMaxRetries         MessageID = "settings.field.max_retries"
	MsgSettingsFieldBaseDelay          MessageID = "settings.field.base_delay"
	MsgSettingsFieldConfirmBeforeWrite MessageID = "settings.field.confirm_before_write"
	MsgSettingsFieldBashWhitelist      MessageID = "settings.field.bash_whitelist"
	MsgSettingsFieldBashBlacklist      MessageID = "settings.field.bash_blacklist"

	MsgSettingsPromptTheme              MessageID = "settings.prompt.theme"
	MsgSettingsPromptMaxContextTokens   MessageID = "settings.prompt.max_context_tokens"
	MsgSettingsPromptWebProvider        MessageID = "settings.prompt.web_provider"
	MsgSettingsPromptWebProviderType    MessageID = "settings.prompt.web_provider_type"
	MsgSettingsPromptWebModel           MessageID = "settings.prompt.web_model"
	MsgSettingsPromptToolMaxConcurrency MessageID = "settings.prompt.tool_max_concurrency"
	MsgSettingsPromptImageProvider      MessageID = "settings.prompt.image_provider"
	MsgSettingsPromptImageAPIType       MessageID = "settings.prompt.image_api_type"
	MsgSettingsPromptImageBaseURL       MessageID = "settings.prompt.image_base_url"
	MsgSettingsPromptImageToken         MessageID = "settings.prompt.image_token"
	MsgSettingsPromptWebSearchBaseURL   MessageID = "settings.prompt.web_search_base_url"
	MsgSettingsPromptWebSearchToken     MessageID = "settings.prompt.web_search_token"
	MsgSettingsPromptImageModel         MessageID = "settings.prompt.image_model"
	MsgSettingsPromptExtraFiles         MessageID = "settings.prompt.extra_files"
	MsgSettingsPromptStatusLineType     MessageID = "settings.prompt.status_line_type"
	MsgSettingsPromptStatusLineCommand  MessageID = "settings.prompt.status_line_command"
	MsgSettingsPromptStatusLinePadding  MessageID = "settings.prompt.status_line_padding"
	MsgSettingsPromptRefreshInterval    MessageID = "settings.prompt.refresh_interval"
	MsgSettingsPromptTimeoutMS          MessageID = "settings.prompt.timeout_ms"
	MsgSettingsPromptStatusLineFallback MessageID = "settings.prompt.status_line_fallback"
	MsgSettingsPromptReserveTokens      MessageID = "settings.prompt.reserve_tokens"
	MsgSettingsPromptKeepRecentTokens   MessageID = "settings.prompt.keep_recent_tokens"
	MsgSettingsPromptTokenizer          MessageID = "settings.prompt.tokenizer"
	MsgSettingsPromptTokenizerModel     MessageID = "settings.prompt.tokenizer_model"
	MsgSettingsPromptTemplate           MessageID = "settings.prompt.template"
	MsgSettingsPromptBwrapPath          MessageID = "settings.prompt.bwrap_path"
	MsgSettingsPromptListValues         MessageID = "settings.prompt.list_values"
	MsgSettingsPromptTmpSize            MessageID = "settings.prompt.tmp_size"
	MsgSettingsPromptSessionDir         MessageID = "settings.prompt.session_dir"
	MsgSettingsPromptSkillsDir          MessageID = "settings.prompt.skills_dir"
	MsgSettingsPromptShellPath          MessageID = "settings.prompt.shell_path"
	MsgSettingsPromptShellPrefix        MessageID = "settings.prompt.shell_prefix"
	MsgSettingsPromptMaxRetries         MessageID = "settings.prompt.max_retries"
	MsgSettingsPromptBaseDelay          MessageID = "settings.prompt.base_delay"
	MsgSettingsPromptApprovalPrefixes   MessageID = "settings.prompt.approval_prefixes"

	MsgSettingsErrorNonNegativeInteger MessageID = "settings.error.non_negative_integer"
	MsgSettingsRuntimeReloadNote       MessageID = "settings.runtime_reload_note"
)

func init() {
	en := catalogs[LanguageEN]
	zh := catalogs[LanguageZH]
	entries := map[MessageID][2]string{
		MsgSettingsFieldDefaultModel:       {"Default Provider / Model", "默认 Provider / 模型"},
		MsgSettingsFieldDefaultThinking:    {"Default Thinking Level", "默认思考级别"},
		MsgSettingsFieldDefaultMode:        {"Default Mode", "默认模式"},
		MsgSettingsFieldTheme:              {"Theme", "主题"},
		MsgSettingsFieldEnablePlanTool:     {"Enable Plan Tool", "启用计划工具"},
		MsgSettingsFieldEnableArtifact:     {"Enable Artifact Publishing", "启用制品发布"},
		MsgSettingsFieldAuthored:           {"Add MothX co-author to commits", "提交时添加 MothX 署名"},
		MsgSettingsFieldMaxContextTokens:   {"Max Context Tokens", "最大上下文 Token"},
		MsgSettingsFieldUpdateCheck:        {"Update Check", "更新检查"},
		MsgSettingsFieldProvider:           {"Provider", "Provider"},
		MsgSettingsFieldProviderType:       {"Provider Type", "Provider 类型"},
		MsgSettingsFieldModel:              {"Model", "模型"},
		MsgSettingsFieldImageEnabled:       {"Image Generation Enabled", "启用图片生成"},
		MsgSettingsFieldImageProvider:      {"Image Generation Provider", "图片生成 Provider"},
		MsgSettingsFieldImageAPIType:       {"Image Generation API Type", "图片生成 API 类型"},
		MsgSettingsFieldImageBaseURL:       {"Image Generation Base URL", "图片生成 Base URL"},
		MsgSettingsFieldImageToken:         {"Image Generation Token", "图片生成 Token"},
		MsgSettingsFieldWebSearchBaseURL:   {"Web Search Base URL", "Web 搜索 Base URL"},
		MsgSettingsFieldWebSearchToken:     {"Web Search Token", "Web 搜索 Token"},
		MsgSettingsFieldWebSearchBackend:   {"Web Search Backend", "Web 搜索后端"},
		MsgSettingsFieldImageModel:         {"Image Generation Model", "图片生成模型"},
		MsgSettingsFieldExtraFiles:         {"Extra Files", "额外文件"},
		MsgSettingsFieldReserveTokens:      {"Reserve Tokens", "预留 Token"},
		MsgSettingsFieldKeepRecentTokens:   {"Keep Recent Tokens", "保留最近 Token"},
		MsgSettingsFieldTokenizer:          {"Tokenizer", "Tokenizer"},
		MsgSettingsFieldTokenizerModel:     {"Tokenizer Model", "Tokenizer 模型"},
		MsgSettingsFieldTemplate:           {"Template", "模板"},
		MsgSettingsFieldLevel:              {"Level", "级别"},
		MsgSettingsFieldBwrapPath:          {"Bwrap Path", "Bwrap 路径"},
		MsgSettingsFieldAllowedRead:        {"Allowed Read", "允许读取"},
		MsgSettingsFieldAllowedWrite:       {"Allowed Write", "允许写入"},
		MsgSettingsFieldDeniedPaths:        {"Denied Paths", "拒绝路径"},
		MsgSettingsFieldPassEnv:            {"Pass Env", "传递环境变量"},
		MsgSettingsFieldTmpSize:            {"Tmp Size", "临时目录大小"},
		MsgSettingsFieldSessionDir:         {"Session Dir", "会话目录"},
		MsgSettingsFieldSkillsDir:          {"Skills Dir", "Skill 目录"},
		MsgSettingsFieldShellPath:          {"Shell Path", "Shell 路径"},
		MsgSettingsFieldShellCommandPrefix: {"Shell Command Prefix", "Shell 命令前缀"},
		MsgSettingsFieldMaxRetries:         {"Max Retries", "最大重试次数"},
		MsgSettingsFieldBaseDelay:          {"Base Delay", "基础延迟"},
		MsgSettingsFieldConfirmBeforeWrite: {"Confirm Before Write", "写入前确认"},
		MsgSettingsFieldBashWhitelist:      {"Bash Whitelist", "Bash 白名单"},
		MsgSettingsFieldBashBlacklist:      {"Bash Blacklist", "Bash 黑名单"},
		MsgSettingsCategoryProviders:       {"Providers", "Provider"}, MsgSettingsCategoryDefaults: {"Defaults", "默认值"}, MsgSettingsCategoryBehavior: {"Behavior", "行为"}, MsgSettingsCategoryWebSearch: {"MothX Local Web Search", "MothX 本地 Web 搜索"}, MsgSettingsCategoryContextFiles: {"Context Files", "上下文文件"}, MsgSettingsCategoryStatusLine: {"Status Line", "状态栏"}, MsgSettingsCategoryCompaction: {"Compaction", "上下文压缩"}, MsgSettingsCategorySandbox: {"Sandbox", "沙箱"}, MsgSettingsCategoryPaths: {"Paths", "路径"}, MsgSettingsCategoryRetry: {"Retry", "重试"}, MsgSettingsCategoryApproval: {"Approval", "审批"},
		MsgSettingsSummaryProviders: {"%d provider(s), default %s / %s", "%d 个 Provider，默认 %s / %s"}, MsgSettingsSummaryDefaults: {"mode=%s  thinking=%s", "模式=%s  思考=%s"}, MsgSettingsSummaryBehavior: {"theme=%s  planTool=%s", "主题=%s  计划工具=%s"}, MsgSettingsSummaryWebSearch: {"enabled=%s  backend=%s", "启用=%s  后端=%s"}, MsgSettingsSummaryContextFiles: {"enabled=%s  extra=%d", "启用=%s  额外文件=%d"}, MsgSettingsSummaryStatusLine: {"enabled=%s  type=%s", "启用=%s  类型=%s"}, MsgSettingsSummaryCompaction: {"enabled=%s  reserve=%s  keep=%s", "启用=%s  预留=%s  保留=%s"}, MsgSettingsSummarySandbox: {"enabled=%s  level=%s", "启用=%s  级别=%s"}, MsgSettingsSummaryPaths: {"sessions=%s", "会话=%s"}, MsgSettingsSummaryRetry: {"enabled=%s  max=%d  base=%dms", "启用=%s  最大次数=%d  基础延迟=%dms"}, MsgSettingsSummaryApproval: {"write=%s  whitelist=%d  blacklist=%d", "写入=%s  白名单=%d  黑名单=%d"},
		MsgSettingsToolExecutionLocalOnly: {"local only; provider may batch", "仅本地；provider 仍可能批量返回"},
		MsgSettingsPromptTheme:            {"Enter theme:", "输入主题："}, MsgSettingsPromptMaxContextTokens: {"Enter max context tokens (0 = unset):", "输入最大上下文 Token（0 = 未设置）："}, MsgSettingsPromptWebProvider: {"Enter credential provider for web search:", "输入 Web 搜索凭证 Provider："}, MsgSettingsPromptWebProviderType: {"Enter web search provider type:", "输入 Web 搜索 Provider 类型："}, MsgSettingsPromptWebModel: {"Enter web search model (empty = unset):", "输入 Web 搜索模型（留空 = 未设置）："}, MsgSettingsPromptWebSearchBaseURL: {"Enter web search backend base URL:", "输入 Web 搜索后端 Base URL："}, MsgSettingsPromptWebSearchToken: {"Enter web search API token or ${ENV_VAR}:", "输入 Web 搜索 API Token 或 ${ENV_VAR}："}, MsgSettingsPromptImageProvider: {"Enter image generation provider:", "输入图片生成 Provider："}, MsgSettingsPromptImageAPIType: {"Enter image generation API type (openai-images/openai-responses):", "输入图片生成 API 类型（openai-images/openai-responses）："}, MsgSettingsPromptImageBaseURL: {"Enter image generation base URL:", "输入图片生成 Base URL："}, MsgSettingsPromptImageToken: {"Enter image generation token or ${ENV_VAR}:", "输入图片生成 Token 或 ${ENV_VAR}："}, MsgSettingsPromptImageModel: {"Enter image generation model:", "输入图片生成模型："}, MsgSettingsPromptExtraFiles: {"Enter extra context files, comma or newline separated:", "输入额外上下文文件，以逗号或换行分隔："}, MsgSettingsPromptStatusLineType: {"Enter status line type:", "输入状态栏类型："}, MsgSettingsPromptStatusLineCommand: {"Enter status line command:", "输入状态栏命令："}, MsgSettingsPromptStatusLinePadding: {"Enter status line padding:", "输入状态栏填充："}, MsgSettingsPromptRefreshInterval: {"Enter refresh interval seconds (0 = event-driven):", "输入刷新间隔秒数（0 = 事件驱动）："}, MsgSettingsPromptTimeoutMS: {"Enter timeout in milliseconds:", "输入超时毫秒数："}, MsgSettingsPromptStatusLineFallback: {"Enter status line fallback:", "输入状态栏回退方式："}, MsgSettingsPromptReserveTokens: {"Enter reserve tokens:", "输入预留 Token："}, MsgSettingsPromptKeepRecentTokens: {"Enter keep recent tokens:", "输入保留最近 Token 数："}, MsgSettingsPromptTokenizer: {"Enter tokenizer (empty = auto):", "输入 Tokenizer（留空 = 自动）："}, MsgSettingsPromptTokenizerModel: {"Enter tokenizer model (empty = auto):", "输入 Tokenizer 模型（留空 = 自动）："}, MsgSettingsPromptTemplate: {"Enter compaction template (empty = default):", "输入压缩模板（留空 = 默认）："}, MsgSettingsPromptBwrapPath: {"Enter bwrap path (empty = auto):", "输入 bwrap 路径（留空 = 自动）："}, MsgSettingsPromptListValues: {"Enter values, comma or newline separated:", "输入值，以逗号或换行分隔："}, MsgSettingsPromptTmpSize: {"Enter tmp size:", "输入临时目录大小："}, MsgSettingsPromptSessionDir: {"Enter session directory:", "输入会话目录："}, MsgSettingsPromptSkillsDir: {"Enter skills directory:", "输入 Skill 目录："}, MsgSettingsPromptShellPath: {"Enter shell path (empty = default shell):", "输入 Shell 路径（留空 = 默认 Shell）："}, MsgSettingsPromptShellPrefix: {"Enter shell command prefix (empty = none):", "输入 Shell 命令前缀（留空 = 无）："}, MsgSettingsPromptMaxRetries: {"Enter max retries:", "输入最大重试次数："}, MsgSettingsPromptBaseDelay: {"Enter base delay in milliseconds:", "输入基础延迟毫秒数："}, MsgSettingsPromptApprovalPrefixes: {"Enter one command prefix per line. Trailing spaces are significant:", "每行输入一个命令前缀。尾随空格具有实际意义："},
		MsgSettingsErrorNonNegativeInteger: {"Invalid non-negative integer", "无效的非负整数"}, MsgSettingsRuntimeReloadNote: {"Note: /reload may be needed for this setting to fully affect existing tools or sessions.", "注意：可能需要执行 /reload，此设置才能完全影响已有工具或会话。"},
	}
	entries[MsgSettingsFieldToolExecutionMode] = [2]string{"Tool Execution Mode", "工具执行模式"}
	entries[MsgSettingsFieldToolMaxConcurrency] = [2]string{"Max Concurrent Tools", "最大并行工具数"}
	entries[MsgSettingsPromptToolMaxConcurrency] = [2]string{"Enter maximum concurrent tools:", "输入最大并行工具数："}
	for id, values := range entries {
		en[id], zh[id] = values[0], values[1]
	}
}
