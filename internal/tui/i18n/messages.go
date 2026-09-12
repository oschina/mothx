package i18n

const (
	MsgInputPlaceholder                   MessageID = "input.placeholder"
	MsgCommandsTitle                      MessageID = "commands.title"
	MsgKeyboardShortcutsTitle             MessageID = "commands.keyboard_shortcuts"
	MsgShortcutSubmitInput                MessageID = "commands.shortcut.submit_input"
	MsgShortcutInsertNewline              MessageID = "commands.shortcut.insert_newline"
	MsgShortcutCycleMode                  MessageID = "commands.shortcut.cycle_mode"
	MsgShortcutAbort                      MessageID = "commands.shortcut.abort"
	MsgShortcutToolDetails                MessageID = "commands.shortcut.tool_details"
	MsgShortcutESMProgress                MessageID = "commands.shortcut.esm_progress"
	MsgShortcutPreviewImage               MessageID = "commands.shortcut.preview_image"
	MsgShortcutCompactTools               MessageID = "commands.shortcut.compact_tools"
	MsgShortcutMoveHistory                MessageID = "commands.shortcut.move_history"
	MsgShortcutSwitchDetailTarget         MessageID = "commands.shortcut.switch_detail_target"
	MsgShortcutPagePanel                  MessageID = "commands.shortcut.page_panel"
	MsgUnknownCommand                     MessageID = "commands.unknown"
	MsgCommandAgentUnknown                MessageID = "commands.agent.unknown"
	MsgCommandMultiAgentStatus            MessageID = "commands.agent.status"
	MsgCommandAgentManagerUnavailable     MessageID = "commands.agent.manager_unavailable"
	MsgCommandNoAgents                    MessageID = "commands.agent.no_agents"
	MsgCommandAgentNotFound               MessageID = "commands.agent.not_found"
	MsgCommandAgentFocused                MessageID = "commands.agent.focused"
	MsgCommandAgentInputHint              MessageID = "commands.agent.input_hint"
	MsgCommandDelegateStatus              MessageID = "commands.delegate.status"
	MsgCommandDelegateRunning             MessageID = "commands.delegate.running"
	MsgCommandDelegateChanged             MessageID = "commands.delegate.changed"
	MsgCommandBrowserStatus               MessageID = "commands.browser.status"
	MsgCommandBrowserRunning              MessageID = "commands.browser.running"
	MsgCommandToolRegistryUnavailable     MessageID = "commands.tool_registry.unavailable"
	MsgCommandBrowserSkillFailed          MessageID = "commands.browser.skill_failed"
	MsgCommandBrowserSkillLoadFailed      MessageID = "commands.browser.skill_load_failed"
	MsgSettingsTitle                      MessageID = "settings.title"
	MsgSettingsLanguage                   MessageID = "settings.language"
	MsgSettingsLanguageDescription        MessageID = "settings.language.description"
	MsgSettingsLanguageScope              MessageID = "settings.language.scope"
	MsgSettingsLanguageScopeDescription   MessageID = "settings.language.scope.description"
	MsgSettingsLanguageSave               MessageID = "settings.language.save"
	MsgSettingsLanguageSaveDescription    MessageID = "settings.language.save.description"
	MsgSettingsLanguageSaved              MessageID = "settings.language.saved"
	MsgSettingsLanguageSaveFailed         MessageID = "settings.language.save_failed"
	MsgSettingsLanguageProjectUnavailable MessageID = "settings.language.project_unavailable"
	MsgTUILangStatus                      MessageID = "commands.tuilang.status"
	MsgBTWTitle                           MessageID = "btw.title"
	MsgBTWError                           MessageID = "btw.error"
	MsgBTWThinking                        MessageID = "btw.thinking"
	MsgBTWNoAnswer                        MessageID = "btw.no_answer"
	MsgBTWStatus                          MessageID = "btw.status"
	MsgBackgroundRunStatus                MessageID = "background.run.status"
	MsgSettingsDone                       MessageID = "settings.done"
	MsgSettingsReturn                     MessageID = "settings.return"
	MsgEnterSelect                        MessageID = "dialog.enter_select"
	MsgEnterSubmit                        MessageID = "dialog.enter_submit"
	MsgEnterSave                          MessageID = "dialog.enter_save"
	MsgYouPrefix                          MessageID = "transcript.you_prefix"
	MsgErrorPrefix                        MessageID = "transcript.error_prefix"
	MsgSessionEndedPrefix                 MessageID = "transcript.session_ended_prefix"
	MsgCompacting                         MessageID = "agent.compacting"
	MsgContextCompacted                   MessageID = "agent.context_compacted"
	MsgCompactionFailed                   MessageID = "agent.compaction_failed"
	MsgAborted                            MessageID = "agent.aborted"
	MsgHostedItemTitle                    MessageID = "agent.hosted_item"
	MsgMemberQuestionToLead               MessageID = "agent.member_question_to_lead"
	MsgNoConversationDetails              MessageID = "conversation.no_details"
	MsgApprovalTitle                      MessageID = "approval.title"
	MsgApprovalRequestTitle               MessageID = "approval.request_title"
	MsgApprovalApproveOnce                MessageID = "approval.approve_once"
	MsgApprovalApproveOnceDescription     MessageID = "approval.approve_once.description"
	MsgApprovalDeny                       MessageID = "approval.deny"
	MsgApprovalDenyDescription            MessageID = "approval.deny.description"
	MsgApprovalRememberExact              MessageID = "approval.remember_exact"
	MsgApprovalRememberPrefix             MessageID = "approval.remember_prefix"
	MsgApprovalProjectRule                MessageID = "approval.project_rule"
	MsgApprovalHint                       MessageID = "approval.hint"
	MsgApprovalSavedExact                 MessageID = "approval.saved_exact"
	MsgApprovalSavedPrefix                MessageID = "approval.saved_prefix"
	MsgApprovalMissingCommand             MessageID = "approval.missing_command"
	MsgApprovalMissingPrefix              MessageID = "approval.missing_prefix"
	MsgApprovalSaveFailed                 MessageID = "approval.save_failed"
	MsgApprovalQueuedApproved             MessageID = "approval.queued_approved"
	MsgApprovalPendingCount               MessageID = "approval.pending_count"
	MsgApprovalCustomInput                MessageID = "approval.custom_input"
	MsgApprovalQuestionPrompt             MessageID = "approval.question_prompt"
	MsgApprovalChooseHint                 MessageID = "approval.choose_hint"
	MsgCommandLabel                       MessageID = "common.command"
	MsgTimeoutLabel                       MessageID = "common.timeout"
	MsgAsyncLabel                         MessageID = "common.async"
	MsgPathLabel                          MessageID = "common.path"
	MsgContentBytes                       MessageID = "common.content_bytes"
	MsgUnknownPath                        MessageID = "common.unknown_path"
	MsgToolModalNoOutput                  MessageID = "tool.modal.no_output"
	MsgToolModalNoConversation            MessageID = "tool.modal.no_conversation"
	MsgToolModalDiff                      MessageID = "tool.modal.diff"
	MsgToolModalTitle                     MessageID = "tool.modal.title"
	MsgToolModalPosition                  MessageID = "tool.modal.position"
	MsgToolModalPositionEmpty             MessageID = "tool.modal.position_empty"
	MsgToolModalSwitchTargetHint          MessageID = "tool.modal.switch_target_hint"
	MsgToolModalPageHint                  MessageID = "tool.modal.page_hint"
	MsgToolModalScrollHint                MessageID = "tool.modal.scroll_hint"
	MsgToolModalCloseHint                 MessageID = "tool.modal.close_hint"
	MsgToolModalEdited                    MessageID = "tool.modal.edited"
	MsgToolModalUnknownPath               MessageID = "tool.modal.unknown_path"
	MsgToolModalStateRunning              MessageID = "tool.modal.state.running"
	MsgToolModalStateReady                MessageID = "tool.modal.state.ready"
	MsgToolModalStateDone                 MessageID = "tool.modal.state.done"
	MsgToolModalStateError                MessageID = "tool.modal.state.error"
	MsgToolModalStateCanceled             MessageID = "tool.modal.state.canceled"
	MsgToolModalStateUnknown              MessageID = "tool.modal.state.unknown"
	MsgToolModalAgentTab                  MessageID = "tool.modal.agent_tab"
	MsgToolModalMain                      MessageID = "tool.modal.main"
	MsgToolArgsPath                       MessageID = "tool.args.path"
	MsgToolArgsContent                    MessageID = "tool.args.content"
	MsgToolArgsEdit                       MessageID = "tool.args.edit"
	MsgToolArgsCommand                    MessageID = "tool.args.command"
	MsgToolExecutionRunning               MessageID = "tool.execution.running"
	MsgToolCommandRunning                 MessageID = "tool.command.running"
	MsgToolCommandUnavailable             MessageID = "tool.command.unavailable"
	MsgToolCommandStarted                 MessageID = "tool.command.started"
	MsgToolCommandSucceeded               MessageID = "tool.command.succeeded"
	MsgToolCommandFailed                  MessageID = "tool.command.failed"
	MsgToolCommandFailedExit              MessageID = "tool.command.failed_exit"
	MsgToolSummaryEmpty                   MessageID = "tool.summary.empty"
	MsgToolResultWritten                  MessageID = "tool.result.written"
	MsgPlanUpdated                        MessageID = "plan.updated"
	MsgPlanTitle                          MessageID = "plan.title"
	MsgPlanNote                           MessageID = "plan.note"
	MsgToolEdited                         MessageID = "tool.edited"
	MsgApprovalCommandLabel               MessageID = "approval.command_label"
	MsgApprovalTimeoutLabel               MessageID = "approval.timeout_label"
	MsgApprovalAsyncLabel                 MessageID = "approval.async_label"
	MsgApprovalPathLabel                  MessageID = "approval.path_label"
	MsgApprovalContentBytes               MessageID = "approval.content_bytes"
	MsgApprovalDiffEmpty                  MessageID = "approval.diff_empty"
	MsgOutputTruncated                    MessageID = "agent.output_truncated"
	MsgOutputRetry                        MessageID = "agent.output_retry"
	MsgAutomaticRetry                     MessageID = "agent.automatic_retry"
	MsgAutomaticRetryWaiting              MessageID = "agent.automatic_retry_waiting"
	MsgAutomaticRetryUnknown              MessageID = "agent.automatic_retry_unknown"
	MsgUsageTokens                        MessageID = "agent.usage_tokens"
	MsgAttachmentsTitle                   MessageID = "attachments.title"
	MsgAttachmentFallback                 MessageID = "attachments.fallback"
	MsgReasonSuffix                       MessageID = "error.reason_suffix"
	MsgToolResultLines                    MessageID = "tool.result.lines"
	MsgToolResultApplied                  MessageID = "tool.result.applied"
	MsgBackgroundStartFailed              MessageID = "background.start_failed"
	MsgBackgroundSubmitted                MessageID = "background.submitted"
	MsgLoading                            MessageID = "common.loading"
	MsgCompactToolsOn                     MessageID = "tools.compact_on"
	MsgCompactToolsOff                    MessageID = "tools.compact_off"
	MsgStatsStartFailed                   MessageID = "stats.start_failed"
	MsgStatsServerAlreadyRunning          MessageID = "stats.server.already_running"
	MsgStatsStarting                      MessageID = "stats.server.starting"
	MsgStatsNotRunning                    MessageID = "stats.server.not_running"
	MsgStatsStopping                      MessageID = "stats.server.stopping"
	MsgStatsRunning                       MessageID = "stats.server.running"
	MsgStatsStopFailed                    MessageID = "stats.server.stop_failed"
	MsgStatsStopped                       MessageID = "stats.server.stopped"
	MsgStatsLoading                       MessageID = "stats.loading"
	MsgStatsNoData                        MessageID = "stats.no_data"
	MsgStatsTitle                         MessageID = "stats.title"
	MsgStatsRequests                      MessageID = "stats.requests"
	MsgStatsInputTokens                   MessageID = "stats.input_tokens"
	MsgStatsOutputTokens                  MessageID = "stats.output_tokens"
	MsgStatsTotalTokens                   MessageID = "stats.total_tokens"
	MsgStatsByProvider                    MessageID = "stats.by_provider"
	MsgStatsByModel                       MessageID = "stats.by_model"
	MsgStatsRecentRequests                MessageID = "stats.recent_requests"
	MsgStatsNoRows                        MessageID = "stats.no_rows"
	MsgStatsFooter                        MessageID = "stats.footer"
	MsgSessionsTitle                      MessageID = "sessions.title"
	MsgSessionsNoSessions                 MessageID = "sessions.no_sessions"
	MsgSessionsAlreadyCurrent             MessageID = "sessions.already_current"
	MsgSessionsCannotSwitchRunning        MessageID = "sessions.cannot_switch_running"
	MsgSessionsSwitched                   MessageID = "sessions.switched"
	MsgSessionsCannotDeleteCurrent        MessageID = "sessions.cannot_delete_current"
	MsgSessionsDeleted                    MessageID = "sessions.deleted"
	MsgSessionsUnknownSubcommand          MessageID = "sessions.unknown_subcommand"
	MsgSessionsErrorListing               MessageID = "sessions.error_listing"
	MsgSessionsDeleteFailed               MessageID = "sessions.delete_failed"
	MsgSessionsAmbiguousID                MessageID = "sessions.ambiguous_id"
	MsgSessionsNoMatch                    MessageID = "sessions.no_match"
	MsgSessionsNewOnNextMessage           MessageID = "sessions.new_on_next_message"
	MsgSessionsListTitle                  MessageID = "sessions.list_title"
	MsgSessionsListHint                   MessageID = "sessions.list_hint"
	MsgSessionsShowingRange               MessageID = "sessions.showing_range"
	MsgSessionsDialogHint                 MessageID = "sessions.dialog_hint"
	MsgSessionsAgeJustNow                 MessageID = "sessions.age.just_now"
	MsgSessionsAgeMinutes                 MessageID = "sessions.age.minutes"
	MsgSessionsAgeHours                   MessageID = "sessions.age.hours"
	MsgSessionsAgeDays                    MessageID = "sessions.age.days"
	MsgClipboardPasteFailed               MessageID = "clipboard.paste_failed"
	MsgClipboardNoPNG                     MessageID = "clipboard.no_png"
	MsgClipboardImagePath                 MessageID = "clipboard.image_path"
	MsgClipboardImagePasted               MessageID = "clipboard.image_pasted"
	MsgClipboardPreviewHint               MessageID = "clipboard.preview_hint"
	MsgClipboardNoImage                   MessageID = "clipboard.no_image"
	MsgClipboardOpenFailed                MessageID = "clipboard.open_failed"
	MsgClipboardOpened                    MessageID = "clipboard.opened"
	MsgDefaultModelTitle                  MessageID = "default_model.title"
	MsgDefaultModelNoMatch                MessageID = "default_model.no_match"
	MsgDefaultModelFooter                 MessageID = "default_model.footer"
	MsgDefaultModelLoadFailed             MessageID = "default_model.load_failed"
	MsgDefaultModelValidationFailed       MessageID = "default_model.validation_failed"
	MsgDefaultModelSaveFailed             MessageID = "default_model.save_failed"
	MsgDefaultModelSaved                  MessageID = "default_model.saved"
	MsgSettingsRunning                    MessageID = "settings.running"
	MsgMultiAgentConfigured               MessageID = "multi_agent.configured"
	MsgMultiAgentDisabled                 MessageID = "multi_agent.disabled"
	MsgAssistantPrefix                    MessageID = "transcript.assistant_prefix"
	MsgThinkingPrefix                     MessageID = "transcript.thinking_prefix"
	MsgThinkingStatus                     MessageID = "agent.thinking_status"
	MsgCancelHint                         MessageID = "agent.cancel_hint"
	MsgFooterLastDuration                 MessageID = "footer.last_duration"
	MsgFooterToolModalHints               MessageID = "footer.tool_modal_hints"
	MsgFooterMainHints                    MessageID = "footer.main_hints"
	MsgFooterApprovalAlert                MessageID = "footer.approval_alert"
	MsgLanguageConfiguredSource           MessageID = "settings.language.configured_source"
	MsgLanguageScopeGlobal                MessageID = "settings.language.scope.global"
	MsgLanguageScopeProject               MessageID = "settings.language.scope.project"
	MsgLanguageSourceGlobal               MessageID = "settings.language.source.global"
	MsgLanguageSourceProject              MessageID = "settings.language.source.project"
	MsgAuthExistingProviders              MessageID = "auth.existing_providers"
	MsgAuthCustomProvider                 MessageID = "auth.custom_provider"
	MsgAuthBack                           MessageID = "auth.back"
	MsgAuthYes                            MessageID = "auth.yes"
	MsgAuthNo                             MessageID = "auth.no"
	MsgAuthSave                           MessageID = "auth.save"
	MsgAuthEdit                           MessageID = "auth.edit"
	MsgAuthDone                           MessageID = "auth.done"
	MsgAuthModels                         MessageID = "auth.models"
	MsgAuthDefault                        MessageID = "auth.default"
	MsgAuthProviderSettings               MessageID = "auth.provider_settings"
	MsgAuthAddModel                       MessageID = "auth.add_model"
	MsgAuthReviewSave                     MessageID = "auth.review_save"
	MsgAuthSearch                         MessageID = "auth.search"
	MsgAuthEnterSelect                    MessageID = "auth.enter_select"
	MsgAuthEnterSubmit                    MessageID = "auth.enter_submit"
	MsgAuthEnterSave                      MessageID = "auth.enter_save"
	MsgAuthProviderIDInvalid              MessageID = "auth.provider_id_invalid"
	MsgAuthModelIDInvalid                 MessageID = "auth.model_id_invalid"
	MsgAuthModelIDExists                  MessageID = "auth.model_id_exists"
	MsgAuthProviderSaved                  MessageID = "auth.provider_saved"
	MsgAuthProviderSavedHint              MessageID = "auth.provider_saved_hint"
	MsgAuthSettingsSaved                  MessageID = "settings.saved"
	MsgAuthSettingsSaveFailed             MessageID = "settings.save_failed"
	MsgAuthSettingsReloadFailed           MessageID = "settings.reload_failed"
	MsgAuthSettingsUnknownField           MessageID = "settings.unknown_field"
	MsgAuthExistingProvidersDescription   MessageID = "auth.existing_providers.description"
	MsgAuthCustomProviderDescription      MessageID = "auth.custom_provider.description"
	MsgAuthBackDescription                MessageID = "auth.back.description"
	MsgAuthYesDescription                 MessageID = "auth.yes.description"
	MsgAuthNoDescription                  MessageID = "auth.no.description"
	MsgAuthSaveDescription                MessageID = "auth.save.description"
	MsgAuthEditDescription                MessageID = "auth.edit.description"
	MsgAuthModelsCount                    MessageID = "auth.models.count"
	MsgAuthDefaultDescription             MessageID = "auth.default.description"
	MsgAuthProviderIDLabel                MessageID = "auth.provider_id"
	MsgAuthLoadGlobalFailed               MessageID = "auth.load_global_failed"
	MsgAuthProviderValidationFailed       MessageID = "auth.provider_validation_failed"
	MsgAuthSaveFailed                     MessageID = "auth.save_failed"
	MsgAuthReloadFailed                   MessageID = "auth.reload_failed"
	MsgAuthEffectiveValidationFailed      MessageID = "auth.effective_validation_failed"
	MsgActivityHostedItem                 MessageID = "activity.hosted_item"
	MsgActivityToolStarted                MessageID = "activity.tool_started"
	MsgActivityToolResult                 MessageID = "activity.tool_result"
	MsgActivityDone                       MessageID = "activity.done"
	MsgActivityError                      MessageID = "activity.error"
	MsgActivityCanceled                   MessageID = "activity.canceled"
	MsgActivityNoActivity                 MessageID = "activity.no_activity"
	MsgActivityLatestTool                 MessageID = "activity.latest_tool"
	MsgActivityThinking                   MessageID = "activity.thinking"
	MsgActivityResponse                   MessageID = "activity.response"
	MsgActivityLatestResult               MessageID = "activity.latest_result"
	MsgActivityTimeline                   MessageID = "activity.timeline"
	MsgActivityUpdated                    MessageID = "activity.updated"
	MsgActivityAgoSeconds                 MessageID = "activity.ago_seconds"
	MsgActivityAgoMinutes                 MessageID = "activity.ago_minutes"
	MsgAuthSearchLabel                    MessageID = "auth.search.label"
	MsgAuthNoProvidersMatch               MessageID = "auth.no_providers_match"
	MsgAuthNoModelsMatch                  MessageID = "auth.no_models_match"
	MsgAuthShowingRange                   MessageID = "auth.showing_range"
	MsgAuthMoreLinesHidden                MessageID = "auth.more_lines_hidden"
	MsgAuthPlaceholderProviderID          MessageID = "auth.placeholder.provider_id"
	MsgAuthPlaceholderModelID             MessageID = "auth.placeholder.model_id"
	MsgAuthTitleConnectProvider           MessageID = "auth.title.connect_provider"
	MsgAuthTitleSettingsProviders         MessageID = "auth.title.settings_providers"
	MsgAuthTitleExistingProvider          MessageID = "auth.title.existing_provider"
	MsgAuthTitleSettingsDefaults          MessageID = "auth.title.settings_defaults"
	MsgAuthTitleSettingsBehavior          MessageID = "auth.title.settings_behavior"
	MsgAuthTitleSettingsWebSearch         MessageID = "auth.title.settings_web_search"
	MsgAuthTitleSettingsContextFiles      MessageID = "auth.title.settings_context_files"
	MsgAuthTitleSettingsStatusLine        MessageID = "auth.title.settings_status_line"
	MsgAuthTitleSettingsCompaction        MessageID = "auth.title.settings_compaction"
	MsgAuthTitleSettingsSandbox           MessageID = "auth.title.settings_sandbox"
	MsgAuthTitleSettingsPaths             MessageID = "auth.title.settings_paths"
	MsgAuthTitleSettingsRetry             MessageID = "auth.title.settings_retry"
	MsgAuthTitleSettingsApproval          MessageID = "auth.title.settings_approval"
	MsgAuthTitleCustomProviderID          MessageID = "auth.title.custom_provider_id"
	MsgAuthTitleSettingsProvider          MessageID = "auth.title.settings_provider"
	MsgAuthTitleProviderSettings          MessageID = "auth.title.provider_settings"
	MsgAuthTitleProviderCredentials       MessageID = "auth.title.provider_credentials"
	MsgAuthTitleProviderProtocol          MessageID = "auth.title.provider_protocol"
	MsgAuthTitleProviderNetwork           MessageID = "auth.title.provider_network"
	MsgAuthTitleProviderAdvanced          MessageID = "auth.title.provider_advanced"
	MsgAuthTitleProviderHeaders           MessageID = "auth.title.provider_headers"
	MsgAuthTitleProviderResponses         MessageID = "auth.title.provider_responses"
	MsgAuthTitleProviderModels            MessageID = "auth.title.provider_models"
	MsgAuthTitleModelParameters           MessageID = "auth.title.model_parameters"
	MsgAuthTitleModelBasics               MessageID = "auth.title.model_basics"
	MsgAuthTitleModelCapabilities         MessageID = "auth.title.model_capabilities"
	MsgAuthTitleModelSampling             MessageID = "auth.title.model_sampling"
	MsgAuthTitleModelCost                 MessageID = "auth.title.model_cost"
	MsgAuthTitleModelCompatibility        MessageID = "auth.title.model_compatibility"
	MsgAuthTitleAddModelID                MessageID = "auth.title.add_model_id"
	MsgAuthTitleAddModelName              MessageID = "auth.title.add_model_name"
	MsgAuthTitleSetupDefault              MessageID = "auth.title.setup_default"
	MsgAuthTitleSetupReview               MessageID = "auth.title.setup_review"
	MsgAuthTitleSetupEdit                 MessageID = "auth.title.setup_edit"
	MsgAuthTitleSetup                     MessageID = "auth.title.setup"
	MsgAuthPromptProviderID               MessageID = "auth.prompt.provider_id"
	MsgAuthPromptModelID                  MessageID = "auth.prompt.model_id"
	MsgAuthPromptModelName                MessageID = "auth.prompt.model_name"
	MsgAuthPromptHeaderName               MessageID = "auth.prompt.header_name"
	MsgAuthPromptHeaderValue              MessageID = "auth.prompt.header_value"
	MsgAuthPromptInput                    MessageID = "auth.prompt.input"
	MsgCommandMode                        MessageID = "commands.mode"
	MsgCommandCurrentMode                 MessageID = "commands.current_mode"
	MsgCommandPermissionsPlan             MessageID = "commands.permissions.plan"
	MsgCommandPermissionsAgent            MessageID = "commands.permissions.agent"
	MsgCommandPermissionsYolo             MessageID = "commands.permissions.yolo"
	MsgCommandPermissionsOS               MessageID = "commands.permissions.os"
	MsgCommandInvalidMode                 MessageID = "commands.invalid_mode"
	MsgCommandModelNotFound               MessageID = "commands.model.not_found"
	MsgCommandModelSwitched               MessageID = "commands.model.switched"
	MsgCommandRunningCannotOpen           MessageID = "commands.running.cannot_open"
	MsgCommandNothingToCompact            MessageID = "commands.compact.empty"
	MsgConversationCleared                MessageID = "conversation.cleared"
	MsgSkillsUnavailable                  MessageID = "skills.unavailable"
	MsgSkillsEmpty                        MessageID = "skills.empty"
	MsgSkillNotFound                      MessageID = "skills.not_found"
	MsgSkillAlreadyActive                 MessageID = "skills.already_active"
	MsgSkillActivated                     MessageID = "skills.activated"
	MsgAllowEditPathTitle                 MessageID = "alloweditpath.title"
	MsgAllowEditPathAlready               MessageID = "alloweditpath.already"
	MsgAllowEditPathNotFound              MessageID = "alloweditpath.not_found"
	MsgAllowEditPathSaved                 MessageID = "alloweditpath.saved"
	MsgAllowEditPathAdded                 MessageID = "alloweditpath.added"
	MsgAllowEditPathRemoved               MessageID = "alloweditpath.removed"
	MsgAllowEditPathCleared               MessageID = "alloweditpath.cleared"
	MsgAllowAutoEditStatus                MessageID = "allowautoedit.status"
	MsgAllowAutoEditSaved                 MessageID = "allowautoedit.saved"
	MsgAllowAutoEditEffective             MessageID = "allowautoedit.effective"
	MsgAgentCannotDestroyMain             MessageID = "agent.cannot_destroy_main"
	MsgAgentDestroyFailed                 MessageID = "agent.destroy_failed"
	MsgAgentDestroyed                     MessageID = "agent.destroyed"
	MsgCronRequiresMultiAgent             MessageID = "cron.requires_multi_agent"
	MsgCronStoreUnavailable               MessageID = "cron.store_unavailable"
	MsgCronCreated                        MessageID = "cron.created"
	MsgCronListEmpty                      MessageID = "cron.list_empty"
	MsgCronListTitle                      MessageID = "cron.list_title"
	MsgCronEntry                          MessageID = "cron.entry"
	MsgCronChanged                        MessageID = "cron.changed"
	MsgCronSchedulerUnavailable           MessageID = "cron.scheduler_unavailable"
	MsgCronTriggered                      MessageID = "cron.triggered"
	MsgCronUnknownCommand                 MessageID = "cron.unknown_command"
	MsgWorkflowListFailed                 MessageID = "workflow.list_failed"
	MsgWorkflowListEmpty                  MessageID = "workflow.list_empty"
	MsgWorkflowListTitle                  MessageID = "workflow.list_title"
	MsgWorkflowShowFailed                 MessageID = "workflow.show_failed"
	MsgWorkflowTitle                      MessageID = "workflow.title"
	MsgWorkflowName                       MessageID = "workflow.name"
	MsgWorkflowPhase                      MessageID = "workflow.phase"
	MsgWorkflowError                      MessageID = "workflow.error"
	MsgWorkflowNotActive                  MessageID = "workflow.not_active"
	MsgWorkflowCancelRequested            MessageID = "workflow.cancel_requested"
	MsgSystemInitRunning                  MessageID = "systeminit.running"
	MsgSystemInitCompacting               MessageID = "systeminit.compacting"
	MsgSystemInitSwitchedMode             MessageID = "systeminit.switched_mode"
	MsgSystemInitInteractive              MessageID = "systeminit.interactive"
	MsgSystemInitAutomatic                MessageID = "systeminit.automatic"
	MsgRuleRunning                        MessageID = "rule.running"
	MsgRuleWriteFailed                    MessageID = "rule.write_failed"
	MsgRuleCreated                        MessageID = "rule.created"
	MsgRuleOverwritten                    MessageID = "rule.overwritten"
	MsgRuleLoaded                         MessageID = "rule.loaded"
	MsgRuleExists                         MessageID = "rule.exists"
	MsgRuleNotOverwritten                 MessageID = "rule.not_overwritten"
	MsgRuleUseForce                       MessageID = "rule.use_force"
	MsgRuleLoadedExisting                 MessageID = "rule.loaded_existing"
	MsgReloading                          MessageID = "reload.reloading"
	MsgCompactRunning                     MessageID = "compact.running"
	MsgModeChangeAborted                  MessageID = "mode.change_aborted"
	MsgCommandAgentEntry                  MessageID = "commands.agent.entry"
	MsgCommandAgentParent                 MessageID = "commands.agent.parent"
	MsgCommandAgentChildren               MessageID = "commands.agent.children"
	MsgCommandBrowserSkillAction          MessageID = "commands.browser.skill_action"
	MsgAllowEditPathSaveFailed            MessageID = "alloweditpath.save_failed"
	MsgAllowAutoEditSavedEffective        MessageID = "allowautoedit.saved_effective"
	MsgCronCreateFailed                   MessageID = "cron.create_failed"
	MsgCronListFailed                     MessageID = "cron.list_failed"
	MsgCronChangedEnabled                 MessageID = "cron.changed.enabled"
	MsgCronChangedDisabled                MessageID = "cron.changed.disabled"
	MsgCronChangedRemoved                 MessageID = "cron.changed.removed"
	MsgWorkflowListEntry                  MessageID = "workflow.list_entry"
	MsgWorkflowResultEntry                MessageID = "workflow.result_entry"
	MsgESMProgressTitle                   MessageID = "esm.panel.progress_title"
	MsgESMPanelLoadFailed                 MessageID = "esm.panel.load_failed"
	MsgESMPanelNoObjective                MessageID = "esm.panel.no_objective"
	MsgESMPanelCreateHint                 MessageID = "esm.panel.create_hint"
	MsgESMPanelTitle                      MessageID = "esm.panel.title"
	MsgESMPanelNow                        MessageID = "esm.panel.now"
	MsgESMPanelNext                       MessageID = "esm.panel.next"
	MsgESMPanelStatus                     MessageID = "esm.panel.status"
	MsgESMPanelStage                      MessageID = "esm.panel.stage"
	MsgESMPanelPipeline                   MessageID = "esm.panel.pipeline"
	MsgESMPanelPaused                     MessageID = "esm.panel.paused"
	MsgESMPanelObjective                  MessageID = "esm.panel.objective"
	MsgESMPanelLatestWorkerProgress       MessageID = "esm.panel.latest_worker_progress"
	MsgESMPanelRemainingWork              MessageID = "esm.panel.remaining_work"
	MsgESMPanelBlocker                    MessageID = "esm.panel.blocker"
	MsgESMPanelRepeatedBlockerAudit       MessageID = "esm.panel.repeated_blocker_audit"
	MsgESMPanelLatestCompletionReview     MessageID = "esm.panel.latest_completion_review"
	MsgESMPanelCompletionRejections       MessageID = "esm.panel.completion_rejections"
	MsgESMPanelAutomaticRecoveries        MessageID = "esm.panel.automatic_recoveries"
	MsgESMPanelLatestRecoveryReason       MessageID = "esm.panel.latest_recovery_reason"
	MsgESMPanelCompletionCandidate        MessageID = "esm.panel.completion_candidate"
	MsgESMPanelLiveDetails                MessageID = "esm.panel.live_details"
	MsgESMPanelTokens                     MessageID = "esm.panel.tokens"
	MsgESMPanelTime                       MessageID = "esm.panel.time"
	MsgESMPanelLastSaved                  MessageID = "esm.panel.last_saved"
	MsgESMPanelSubagentStarting           MessageID = "esm.panel.subagent_starting"
	MsgESMPanelLatestTool                 MessageID = "esm.panel.latest_tool"
	MsgESMPanelLatestResult               MessageID = "esm.panel.latest_result"
	MsgESMPanelLatestResponse             MessageID = "esm.panel.latest_response"
	MsgESMPanelThinking                   MessageID = "esm.panel.thinking"
	MsgESMPanelNoFurtherWork              MessageID = "esm.panel.no_further_work"
	MsgESMPanelProgress                   MessageID = "esm.panel.progress"
	MsgESMPanelWorkRemaining              MessageID = "esm.panel.work_remaining"
	MsgESMPanelActivityStarting           MessageID = "esm.panel.activity_starting"
	MsgESMPanelActivityState              MessageID = "esm.panel.activity_state"
	MsgESMPanelTool                       MessageID = "esm.panel.tool"
	MsgESMPanelLatest                     MessageID = "esm.panel.latest"
	MsgESMPanelCriticReview               MessageID = "esm.panel.phase.critic_review"
	MsgESMPanelFinalAudit                 MessageID = "esm.panel.phase.final_audit"
	MsgESMPanelComplete                   MessageID = "esm.panel.phase.complete"
	MsgESMPanelWorkerExecution            MessageID = "esm.panel.phase.worker_execution"
	MsgESMPanelESMPaused                  MessageID = "esm.panel.activity.paused"
	MsgESMPanelESMBlocked                 MessageID = "esm.panel.activity.blocked"
	MsgESMPanelUsageLimited               MessageID = "esm.panel.activity.usage_limited"
	MsgESMPanelAuditPassed                MessageID = "esm.panel.activity.audit_passed"
	MsgESMPanelCriticReviewing            MessageID = "esm.panel.activity.critic_reviewing"
	MsgESMPanelAuditing                   MessageID = "esm.panel.activity.auditing"
	MsgESMPanelWorkerInvestigating        MessageID = "esm.panel.activity.worker_investigating"
	MsgESMPanelNextPaused                 MessageID = "esm.panel.next.paused"
	MsgESMPanelNextBlocked                MessageID = "esm.panel.next.blocked"
	MsgESMPanelNextUsageLimited           MessageID = "esm.panel.next.usage_limited"
	MsgESMPanelNextComplete               MessageID = "esm.panel.next.complete"
	MsgESMPanelNextCritic                 MessageID = "esm.panel.next.critic"
	MsgESMPanelNextAudit                  MessageID = "esm.panel.next.audit"
	MsgESMPanelNextWorker                 MessageID = "esm.panel.next.worker"
	MsgESMPanelShortcutHint               MessageID = "esm.panel.shortcut_hint"
	MsgESMPanelPosition                   MessageID = "esm.panel.position"
	MsgESMPanelPositionEmpty              MessageID = "esm.panel.position_empty"
)

var catalogs = map[Language]map[MessageID]string{
	LanguageEN: {
		MsgInputPlaceholder:                   "Type a message...",
		MsgCommandsTitle:                      "Commands:",
		MsgCommandModeDescription:             "Switch or show execution mode (plan/agent/yolo/os)",
		MsgCommandESMDescription:              "Enable or inspect Supervisor Mode",
		MsgCommandModelDescription:            "Switch or show model",
		MsgCommandDefaultModelDescription:     "Set the default provider/model",
		MsgCommandAuthDescription:             "Configure provider token, base URL and models",
		MsgCommandSettingsDescription:         "Configure settings.json groups, including providers",
		MsgCommandTUILangDescription:          "Set the TUI language (global by default)",
		MsgCommandSkillsDescription:           "List available skills",
		MsgCommandSkillHubDescription:         "Browse, search and install marketplace skills",
		MsgCommandEnvDescription:              "Manage extra environment variables",
		MsgCommandSkillDescription:            "Activate a skill",
		MsgCommandPasteImageDescription:       "Save a clipboard image and insert its local path",
		MsgCommandClearDescription:            "Clear conversation",
		MsgCommandCompactDescription:          "Trigger context compaction",
		MsgCommandSessionsDescription:         "List, switch, create or delete sessions",
		MsgCommandExpertDescription:           "List, inspect, bind or fork-switch expert personas",
		MsgCommandInitMCPDescription:          "Initialize mcp.json",
		MsgCommandMCPsDescription:             "List MCP servers",
		MsgCommandDelegateDescription:         "Toggle delegation mode",
		MsgCommandBrowserDescription:          "Toggle browser automation",
		MsgCommandStatsDescription:            "Manage or display usage statistics",
		MsgCommandStatusLineDescription:       "Inspect or toggle the TUI status line",
		MsgCommandAllowEditPathDescription:    "Manage the auto-edit path whitelist",
		MsgCommandAllowAutoEditDescription:    "Toggle full auto-edit in agent mode",
		MsgCommandBTWDescription:              "Ask a side question without touching the main task",
		MsgCommandSystemInitDescription:       "Generate or refresh AGENTS.md",
		MsgCommandRuleDescription:             "Create safe default project rules",
		MsgCommandReloadDescription:           "Restart as a fresh process with a new session",
		MsgCommandWorkflowsDescription:        "Inspect workflow runs",
		MsgCommandAgentDescription:            "Manage multi-agent workers",
		MsgCommandCronDescription:             "Manage scheduled tasks",
		MsgCommandQuitDescription:             "Exit",
		MsgCommandHelpDescription:             "Show this help",
		MsgCommandUsage:                       "Usage: %s",
		MsgKeyboardShortcutsTitle:             "Keyboard shortcuts:",
		MsgShortcutSubmitInput:                "Submit input",
		MsgShortcutInsertNewline:              "Insert newline in input",
		MsgShortcutCycleMode:                  "Cycle mode (plan/agent/yolo/os)",
		MsgShortcutAbort:                      "Abort current operation",
		MsgShortcutToolDetails:                "Open latest tool details",
		MsgShortcutESMProgress:                "Open Supervisor Mode progress",
		MsgShortcutPreviewImage:               "Preview latest pasted image",
		MsgShortcutCompactTools:               "Toggle simple/full event display",
		MsgShortcutMoveHistory:                "Move in multiline input; history at boundaries",
		MsgShortcutSwitchDetailTarget:         "Switch detail target when Ctrl+O modal is open",
		MsgShortcutPagePanel:                  "Page an open details or ESM progress panel",
		MsgUnknownCommand:                     "Unknown: %s",
		MsgCommandAgentUnknown:                "Unknown agent command: %s",
		MsgCommandMultiAgentStatus:            "Multi-agent mode: ON (active: %s)",
		MsgCommandAgentManagerUnavailable:     "Agent manager is not initialized.",
		MsgCommandNoAgents:                    "No agents running.",
		MsgCommandAgentNotFound:               "Agent %s not found.",
		MsgCommandAgentFocused:                "Focused agent tab: %s",
		MsgCommandAgentInputHint:              "Input still goes to the main agent; use subagent_send for follow-up instructions.",
		MsgCommandDelegateStatus:              "Delegation mode: %s",
		MsgCommandDelegateRunning:             "Cannot change delegation mode while the agent is running.",
		MsgCommandDelegateChanged:             "Delegation mode: %s",
		MsgCommandBrowserStatus:               "Browser tool: %s",
		MsgCommandBrowserRunning:              "Cannot change browser tool while the agent is running.",
		MsgCommandToolRegistryUnavailable:     "Tool registry is not initialized.",
		MsgCommandBrowserSkillFailed:          "Failed to create browser skill: %v",
		MsgCommandBrowserSkillLoadFailed:      "Failed to load skills: %v",
		MsgCommandMode:                        "Mode: %s",
		MsgCommandCurrentMode:                 "Current mode: %s",
		MsgCommandPermissionsPlan:             "  Permissions: READ only (no modifications)",
		MsgCommandPermissionsAgent:            "  Permissions: READ/WRITE/EDIT auto | BASH requires approval",
		MsgCommandPermissionsYolo:             "  Permissions: ALL tools auto-execute",
		MsgCommandPermissionsOS:               "  Permissions: BASH only, auto-execute (no sandbox; blacklisted commands still require approval)",
		MsgCommandInvalidMode:                 "Invalid mode",
		MsgCommandModelNotFound:               "Model %q not found — available: %s",
		MsgCommandModelSwitched:               "✅ Model switched to: %s (%s)",
		MsgCommandRunningCannotOpen:           "Cannot open %s while the agent is running.",
		MsgCommandNothingToCompact:            "Nothing to compact: no active conversation.",
		MsgConversationCleared:                "✅ Conversation cleared",
		MsgSkillsUnavailable:                  "No skills manager available.",
		MsgSkillsEmpty:                        "No skills found.",
		MsgSkillNotFound:                      "Skill not found: %s",
		MsgSkillAlreadyActive:                 "Skill '%s' is already active.",
		MsgSkillActivated:                     "✅ Skill '%s' activated (%s): %s",
		MsgAllowEditPathTitle:                 "Auto-edit path whitelist (agent mode):",
		MsgAllowEditPathAlready:               "Already in whitelist: %s",
		MsgAllowEditPathNotFound:              "Not in whitelist: %s",
		MsgAllowEditPathSaved:                 "✅ %s auto-edit whitelist: %s",
		MsgAllowEditPathCleared:               "✅ Auto-edit path whitelist cleared",
		MsgAllowAutoEditStatus:                "Auto-edit (agent mode): %s",
		MsgAllowAutoEditSaved:                 "✅ Auto-edit (agent mode): %s [%s]",
		MsgAllowAutoEditEffective:             " (effective here: %s due to project override)",
		MsgAgentCannotDestroyMain:             "Cannot destroy the main agent",
		MsgAgentDestroyFailed:                 "Failed to destroy agent %s: %v",
		MsgAgentDestroyed:                     "Agent %s destroyed",
		MsgCronRequiresMultiAgent:             "Cron commands require multi-agent mode. Restart with --multi-agent to enable.",
		MsgCronStoreUnavailable:               "Cron store not initialized.",
		MsgCronCreated:                        "✅ Cron task created: %s (id: %s)",
		MsgCronListEmpty:                      "Cron tasks: (none configured)",
		MsgCronListTitle:                      "Cron tasks (%d):",
		MsgCronEntry:                          "  %s [%s] %s (runs: %d)",
		MsgCronChanged:                        "Cron task %s %s",
		MsgCronSchedulerUnavailable:           "Scheduler not running.",
		MsgCronTriggered:                      "▶ Cron task %s triggered (will run on next scheduler tick)",
		MsgCronUnknownCommand:                 "Unknown cron command: %s",
		MsgWorkflowListFailed:                 "Failed to list workflows: %v",
		MsgWorkflowListEmpty:                  "Workflow runs: (none)",
		MsgWorkflowListTitle:                  "Workflow runs (%d):",
		MsgWorkflowShowFailed:                 "Failed to load workflow: %v",
		MsgWorkflowTitle:                      "Workflow %s: %s",
		MsgWorkflowName:                       "Name: %s",
		MsgWorkflowPhase:                      "Phase [%s] %s tasks=%d",
		MsgWorkflowError:                      "Error: %s",
		MsgWorkflowNotActive:                  "Workflow run %s is not active.",
		MsgWorkflowCancelRequested:            "Workflow run %s cancellation requested.",
		MsgSystemInitRunning:                  "Cannot run /systeminit while the agent is running.",
		MsgSystemInitCompacting:               "Cannot run /systeminit while context compaction is running.",
		MsgSystemInitSwitchedMode:             "Switched to AGENT mode for /systeminit (AGENTS.md needs write access).",
		MsgSystemInitInteractive:              "🛠 /systeminit: analyzing the project; I'll ask a few questions, then write AGENTS.md.",
		MsgSystemInitAutomatic:                "🛠 /systeminit: analyzing the project and writing AGENTS.md...",
		MsgRuleRunning:                        "Cannot change /rule while the agent is running.",
		MsgRuleWriteFailed:                    "Failed to write rule file: %v",
		MsgRuleCreated:                        "%s rule file: %s",
		MsgRuleOverwritten:                    "Overwrote",
		MsgRuleLoaded:                         "Loaded into the current session.",
		MsgRuleExists:                         "Rule file already exists: %s",
		MsgReloading:                          "↻ Reloading: starting a fresh process with a new session...",
		MsgCompactRunning:                     "Cannot compact while the agent is running.",
		MsgModeChangeAborted:                  "⏹ Aborted (mode change)",
		MsgCommandAgentEntry:                  "  %s [%s]",
		MsgCommandAgentParent:                 " parent=%s",
		MsgCommandAgentChildren:               " children=%d",
		MsgCommandBrowserSkillAction:          "%s: %s",
		MsgAllowEditPathSaveFailed:            "Failed to save allow.json: %v",
		MsgAllowAutoEditSavedEffective:        " (effective here: %s due to project override)",
		MsgCronCreateFailed:                   "Failed to create cron task: %v",
		MsgCronListFailed:                     "Failed to list cron tasks: %v",
		MsgCronChangedEnabled:                 "enabled",
		MsgCronChangedDisabled:                "disabled",
		MsgCronChangedRemoved:                 "removed",
		MsgWorkflowListEntry:                  "  [%s] %s %s (%s)",
		MsgWorkflowResultEntry:                "\n%s [%s]\n%s\n",
		MsgSettingsTitle:                      "Settings",
		MsgSettingsLanguage:                   "TUI Language",
		MsgSettingsLanguageDescription:        "configured=%s  effective=%s  %s  source=%s",
		MsgSettingsLanguageScope:              "Save Scope",
		MsgSettingsLanguageScopeDescription:   "%s",
		MsgSettingsLanguageSave:               "Save Language",
		MsgSettingsLanguageSaveDescription:    "Persist the language and apply it immediately",
		MsgSettingsLanguageSaved:              "TUI language saved to %s: %s (effective: %s)",
		MsgSettingsLanguageSaveFailed:         "Failed to save TUI language: %v",
		MsgSettingsLanguageProjectUnavailable: "Project scope is unavailable outside a project directory.",
		MsgTUILangStatus:                      "TUI language: configured=%s  effective=%s  %s  source=%s",
		MsgBTWTitle:                           "💬 /btw: %s",
		MsgBTWError:                           "Error: %v",
		MsgBTWThinking:                        "Thinking...",
		MsgBTWNoAnswer:                        "(no answer)",
		MsgBTWStatus:                          "[%s] lines %d-%d/%d  Up/Down:scroll  Esc:close (not saved to main task)",
		MsgBackgroundRunStatus:                "Background run %s %s%s",
		MsgSettingsDone:                       "Done",
		MsgSettingsReturn:                     "Return to Settings",
		MsgEnterSelect:                        "Enter to select, ↑↓ to navigate, Esc to go back",
		MsgEnterSubmit:                        "Enter to submit, Esc to go back",
		MsgEnterSave:                          "Enter to save, Esc to go back",
		MsgYouPrefix:                          "You: ",
		MsgErrorPrefix:                        "Error: ",
		MsgSessionEndedPrefix:                 "Session ended: ",
		MsgCompacting:                         "⏳ Compacting context...",
		MsgContextCompacted:                   "✅ Context compacted",
		MsgCompactionFailed:                   "Compaction failed: ",
		MsgAborted:                            "⏹ Aborted",
		MsgHostedItemTitle:                    "Hosted item",
		MsgMemberQuestionToLead:               "ℹ️ Member %s asked the lead (answered by the main agent): %s",
		MsgNoConversationDetails:              "No conversation details yet.",
		MsgApprovalTitle:                      "Approval Required: %s",
		MsgApprovalRequestTitle:               "Approval required: %s",
		MsgApprovalApproveOnce:                "Approve Once",
		MsgApprovalApproveOnceDescription:     "Run only this pending tool call",
		MsgApprovalDeny:                       "Deny",
		MsgApprovalDenyDescription:            "Reject this pending tool call",
		MsgApprovalRememberExact:              "Always Allow Exact Command",
		MsgApprovalRememberPrefix:             "Always Allow Command Prefix",
		MsgApprovalProjectRule:                "Project rule: %s",
		MsgApprovalHint:                       "Enter select · ↑/↓ move · y approve · n deny · Esc abort",
		MsgApprovalSavedExact:                 "Approved and remembered this command for this project",
		MsgApprovalSavedPrefix:                "Approved and remembered project command prefix: %s",
		MsgApprovalMissingCommand:             "Cannot save approval rule: missing bash command.",
		MsgApprovalMissingPrefix:              "Cannot save approval rule: missing bash command prefix.",
		MsgApprovalSaveFailed:                 "Failed to save allow.json: %v",
		MsgApprovalQueuedApproved:             "Approved %d queued matching command(s)",
		MsgApprovalPendingCount:               " (%d more pending)",
		MsgApprovalCustomInput:                "Custom input",
		MsgApprovalQuestionPrompt:             "Enter number or custom text: ",
		MsgApprovalChooseHint:                 "Choose in the approval dialog (↑/↓, Enter, y/n).",
		MsgCommandLabel:                       "Command:",
		MsgTimeoutLabel:                       "Timeout: %v",
		MsgAsyncLabel:                         "Async: %v",
		MsgPathLabel:                          "Path: %s",
		MsgContentBytes:                       "Content: (%d bytes)",
		MsgToolModalNoOutput:                  "(no output)",
		MsgToolModalNoConversation:            "(no conversation yet)",
		MsgToolModalDiff:                      "--- diff",
		MsgToolModalTitle:                     "Agent details",
		MsgToolModalPosition:                  "lines %d-%d/%d",
		MsgToolModalPositionEmpty:             "lines 0-0/0",
		MsgToolModalSwitchTargetHint:          "Left/Right:switch target",
		MsgToolModalPageHint:                  "PgUp/PgDn:page",
		MsgToolModalScrollHint:                "Up/Down:scroll",
		MsgToolModalCloseHint:                 "Esc:close",
		MsgToolModalEdited:                    "• Edited %s",
		MsgToolModalUnknownPath:               "(unknown)",
		MsgToolModalStateRunning:              "running",
		MsgToolModalStateReady:                "ready",
		MsgToolModalStateDone:                 "done",
		MsgToolModalStateError:                "error",
		MsgToolModalStateCanceled:             "canceled",
		MsgToolModalStateUnknown:              "unknown",
		MsgToolModalAgentTab:                  "[ %s %s ]",
		MsgToolModalMain:                      "Main",
		MsgToolArgsPath:                       "path: %v", MsgToolArgsContent: "content:\n%s", MsgToolArgsEdit: "edit[%d]:\n  old: %s\n  new: %s", MsgToolArgsCommand: "command: %v", MsgToolExecutionRunning: "%s running: %v", MsgToolCommandRunning: "running", MsgToolCommandUnavailable: "command unavailable", MsgToolCommandStarted: "started", MsgToolCommandSucceeded: "succeeded", MsgToolCommandFailed: "failed", MsgToolCommandFailedExit: "failed (exit code %d)", MsgToolSummaryEmpty: "...", MsgToolResultWritten: "Written", MsgPlanUpdated: "Plan updated.", MsgPlanTitle: "Plan", MsgPlanNote: "note: %s", MsgToolEdited: "• Edited %s",

		MsgApprovalCommandLabel:             "command:",
		MsgApprovalTimeoutLabel:             "timeout: %v",
		MsgApprovalAsyncLabel:               "async: %v",
		MsgApprovalPathLabel:                "path: %s",
		MsgApprovalContentBytes:             "content: (%d bytes)",
		MsgApprovalDiffEmpty:                "diff: (empty)",
		MsgOutputTruncated:                  "⚠ Output was truncated because the output token limit was reached.",
		MsgOutputRetry:                      "⚠ Output limit reached; retrying with %d max tokens...",
		MsgAutomaticRetry:                   "↻ Retrying (attempt %d/%d)...",
		MsgAutomaticRetryWaiting:            "↻ Retrying (attempt %d/%d); waiting %s...",
		MsgAutomaticRetryUnknown:            "↻ Retrying...",
		MsgUsageTokens:                      "Tokens: %d↓/%d↑ $%.4f%s",
		MsgAttachmentsTitle:                 "Attachments:",
		MsgAttachmentFallback:               "attachment",
		MsgReasonSuffix:                     " (reason: %s)",
		MsgToolResultLines:                  "%d lines",
		MsgToolResultApplied:                "Applied",
		MsgBackgroundStartFailed:            "Background run failed to start: ",
		MsgBackgroundSubmitted:              "Background run submitted: %s",
		MsgLoading:                          "Loading...",
		MsgCompactToolsOn:                   "Simple event display: ON",
		MsgCompactToolsOff:                  "Full event display: ON",
		MsgStatsServerAlreadyRunning:        "Stats server already running: %s",
		MsgStatsStarting:                    "Starting stats server...",
		MsgStatsNotRunning:                  "Stats server is not running.",
		MsgStatsStopping:                    "Stopping stats server...",
		MsgStatsRunning:                     "Stats server running: %s",
		MsgStatsStopFailed:                  "Failed to stop stats server: %v",
		MsgStatsStopped:                     "Stats server stopped.",
		MsgStatsLoading:                     "Loading stats...",
		MsgStatsNoData:                      "No stats data.",
		MsgStatsTitle:                       "VibeCoding Stats",
		MsgStatsRequests:                    "Requests:      %d",
		MsgStatsInputTokens:                 "Input tokens:  %d",
		MsgStatsOutputTokens:                "Output tokens: %d",
		MsgStatsTotalTokens:                 "Total tokens:  %d",
		MsgStatsByProvider:                  "By Provider",
		MsgStatsByModel:                     "By Model",
		MsgStatsRecentRequests:              "Recent Requests",
		MsgStatsNoRows:                      "  No data",
		MsgStatsFooter:                      "Stats  Up/Down:scroll  PgUp/PgDn:page  Esc:close",
		MsgSessionsTitle:                    "Sessions",
		MsgSessionsNoSessions:               "No sessions found for this project.",
		MsgSessionsAlreadyCurrent:           "Already on this session.",
		MsgSessionsCannotSwitchRunning:      "Cannot switch sessions while the agent is running.",
		MsgSessionsSwitched:                 "✅ Switched to session %s (%d msgs)",
		MsgSessionsCannotDeleteCurrent:      "Cannot delete the current session. Switch to another session first.",
		MsgSessionsDeleted:                  "✅ Deleted session %s.",
		MsgSessionsUnknownSubcommand:        "Unknown subcommand: %s. Use /sessions, or ls, set, clear, del.",
		MsgSessionsErrorListing:             "Error listing sessions: %v",
		MsgSessionsDeleteFailed:             "Error deleting session: %v",
		MsgSessionsAmbiguousID:              "Ambiguous ID '%s'. Be more specific.",
		MsgSessionsNoMatch:                  "No session found matching '%s'.",
		MsgSessionsNewOnNextMessage:         "✅ New session will be created when you send the next message.",
		MsgSessionsListTitle:                "Sessions for this project:",
		MsgSessionsListHint:                 "Use /sessions set <id> to switch. * = current session.",
		MsgSessionsShowingRange:             "Showing %d-%d of %d",
		MsgSessionsDialogHint:               "Enter switch  Up/Down select  n new  d delete  Esc close",
		MsgSessionsAgeJustNow:               "just now",
		MsgSessionsAgeMinutes:               "%d min ago",
		MsgSessionsAgeHours:                 "%d hour ago",
		MsgSessionsAgeDays:                  "%d day ago",
		MsgClipboardPasteFailed:             "Paste image failed: %v",
		MsgClipboardNoPNG:                   "Clipboard does not contain a PNG image. Copy an image, then run /paste-image again.",
		MsgClipboardImagePath:               "Image Path : %s",
		MsgClipboardImagePasted:             "Image pasted: %s",
		MsgClipboardPreviewHint:             "Press Ctrl+R to preview.",
		MsgClipboardNoImage:                 "No pasted image to preview. Run /paste-image first.",
		MsgClipboardOpenFailed:              "Could not open pasted image: %v. Path: %s",
		MsgClipboardOpened:                  "Opened preview: %s",
		MsgDefaultModelTitle:                "Set Default Model (%s)",
		MsgDefaultModelNoMatch:              "No options match.",
		MsgDefaultModelFooter:               "Enter to select, ↑↓ to navigate, Esc to go back",
		MsgDefaultModelLoadFailed:           "Failed to load %s settings: %v",
		MsgDefaultModelValidationFailed:     "Provider validation failed: %v",
		MsgDefaultModelSaveFailed:           "Failed to save %s settings: %v",
		MsgDefaultModelSaved:                "✅ Default model saved (%s): %s / %s",
		MsgSettingsRunning:                  "Cannot open /settings while the agent is running.",
		MsgMultiAgentConfigured:             "Multi-agent mode is configured at startup. Run with --multi-agent to enable sub-agent tools.",
		MsgMultiAgentDisabled:               "Multi-agent mode is not enabled. Restart with --multi-agent to enable sub-agent tools.",
		MsgAssistantPrefix:                  "Assistant: ",
		MsgThinkingPrefix:                   "think: ",
		MsgThinkingStatus:                   "Thinking...",
		MsgCancelHint:                       "esc to cancel",
		MsgFooterLastDuration:               "last %s",
		MsgFooterToolModalHints:             "Left/Right:switch PgUp/PgDn:page Up/Down:scroll Esc/Ctrl+O:close",
		MsgFooterMainHints:                  "Tab:mode Esc:abort Ctrl+O:details Ctrl+E:ESM Ctrl+R:preview Ctrl+G:events",
		MsgFooterApprovalAlert:              "! APPROVAL REQUIRED: ↑/↓ Enter",
		MsgLanguageConfiguredSource:         "%s",
		MsgLanguageScopeGlobal:              "Global default",
		MsgLanguageScopeProject:             "Current project",
		MsgLanguageSourceGlobal:             "global",
		MsgLanguageSourceProject:            "project override",
		MsgAuthExistingProviders:            "Existing Providers",
		MsgAuthCustomProvider:               "Custom Provider",
		MsgAuthBack:                         "← Back",
		MsgAuthYes:                          "Yes",
		MsgAuthNo:                           "No",
		MsgAuthSave:                         "Save",
		MsgAuthEdit:                         "Edit",
		MsgAuthDone:                         "Done",
		MsgAuthModels:                       "Models",
		MsgAuthDefault:                      "Default setting",
		MsgAuthProviderSettings:             "Provider Settings",
		MsgAuthAddModel:                     "+ Add Model",
		MsgAuthReviewSave:                   "Review & Save",
		MsgAuthSearch:                       "type to search",
		MsgAuthEnterSelect:                  "Enter to select, ↑↓ to navigate, Esc to go back",
		MsgAuthEnterSubmit:                  "Enter to submit, Esc to go back",
		MsgAuthEnterSave:                    "Enter to save, Esc to go back",
		MsgAuthProviderIDInvalid:            "Provider ID must be non-empty and contain no spaces or slashes.",
		MsgAuthModelIDInvalid:               "Model ID must be non-empty and contain no whitespace.",
		MsgAuthModelIDExists:                "Model ID already exists.",
		MsgAuthProviderSaved:                "✅ Provider saved: %s / %s",
		MsgAuthProviderSavedHint:            "Next message will use the new provider/model.",
		MsgAuthSettingsSaved:                "Settings saved: %s",
		MsgAuthSettingsSaveFailed:           "Failed to save settings: %v",
		MsgAuthSettingsReloadFailed:         "Failed to reload settings: %v",
		MsgAuthExistingProvidersDescription: "Add or update token/model under an existing provider",
		MsgAuthCustomProviderDescription:    "Add provider by API type, base URL, token and models",
		MsgAuthBackDescription:              "Return to main menu",
		MsgAuthYesDescription:               "Use this provider/model for future requests",
		MsgAuthNoDescription:                "Save provider without changing defaults",
		MsgAuthSaveDescription:              "Write settings.json and switch current TUI provider",
		MsgAuthEditDescription:              "Go back and modify provider setup",
		MsgAuthModelsCount:                  "%d model(s)",
		MsgAuthDefaultDescription:           "set default: %v",
		MsgAuthProviderIDLabel:              "Provider ID",
		MsgAuthLoadGlobalFailed:             "Load global settings failed: %v",
		MsgAuthProviderValidationFailed:     "Provider validation failed: %v",
		MsgAuthSaveFailed:                   "Save failed: %v",
		MsgAuthReloadFailed:                 "Saved global settings, but reload failed: %v",
		MsgAuthEffectiveValidationFailed:    "Saved global settings, but effective provider validation failed: %v",
		MsgActivityHostedItem:               "hosted item",
		MsgActivityToolStarted:              "tool started: %s",
		MsgActivityToolResult:               "tool result",
		MsgActivityDone:                     "done",
		MsgActivityError:                    "error: %s",
		MsgActivityCanceled:                 "canceled",
		MsgActivityNoActivity:               "no activity captured yet",
		MsgActivityLatestTool:               "Latest tool:",
		MsgActivityThinking:                 "Thinking:",
		MsgActivityResponse:                 "Response:",
		MsgActivityLatestResult:             "Latest result:",
		MsgActivityTimeline:                 "Activity timeline:",
		MsgActivityUpdated:                  "updated %s",
		MsgActivityAgoSeconds:               "%ds ago",
		MsgActivityAgoMinutes:               "%dm ago",
		MsgAuthSearchLabel:                  "Search: %s",
		MsgAuthNoProvidersMatch:             "No providers match.",
		MsgAuthNoModelsMatch:                "No models match.",
		MsgAuthShowingRange:                 "Showing %d-%d of %d",
		MsgAuthMoreLinesHidden:              "… %d more lines hidden",
		MsgAuthPlaceholderProviderID:        "provider-id (e.g. openrouter)",
		MsgAuthPlaceholderModelID:           "model-id",
		MsgAuthTitleConnectProvider:         "Connect a Provider",
		MsgAuthTitleSettingsProviders:       "Settings · Providers",
		MsgAuthTitleExistingProvider:        "Existing Providers · Provider",
		MsgAuthTitleSettingsDefaults:        "Settings · Defaults",
		MsgAuthTitleSettingsBehavior:        "Settings · Behavior",
		MsgAuthTitleSettingsWebSearch:       "Settings · Web Search",
		MsgAuthTitleSettingsContextFiles:    "Settings · Context Files",
		MsgAuthTitleSettingsStatusLine:      "Settings · Status Line",
		MsgAuthTitleSettingsCompaction:      "Settings · Compaction",
		MsgAuthTitleSettingsSandbox:         "Settings · Sandbox",
		MsgAuthTitleSettingsPaths:           "Settings · Paths",
		MsgAuthTitleSettingsRetry:           "Settings · Retry",
		MsgAuthTitleSettingsApproval:        "Settings · Approval",
		MsgAuthTitleCustomProviderID:        "Custom Provider · Provider ID",
		MsgAuthTitleSettingsProvider:        "Settings · %s",
		MsgAuthTitleProviderSettings:        "Provider · %s · Settings",
		MsgAuthTitleProviderCredentials:     "Provider · Credentials",
		MsgAuthTitleProviderProtocol:        "Provider · Protocol",
		MsgAuthTitleProviderNetwork:         "Provider · Network",
		MsgAuthTitleProviderAdvanced:        "Provider · Advanced",
		MsgAuthTitleProviderHeaders:         "Provider · Headers",
		MsgAuthTitleProviderResponses:       "Provider · Responses",
		MsgAuthTitleProviderModels:          "Provider · Models",
		MsgAuthTitleModelParameters:         "Model · %s · Parameters",
		MsgAuthTitleModelBasics:             "Model · Basics",
		MsgAuthTitleModelCapabilities:       "Model · Capabilities",
		MsgAuthTitleModelSampling:           "Model · Sampling",
		MsgAuthTitleModelCost:               "Model · Cost",
		MsgAuthTitleModelCompatibility:      "Model · Compatibility",
		MsgAuthTitleAddModelID:              "Add Model · ID",
		MsgAuthTitleAddModelName:            "Add Model · Name",
		MsgAuthTitleSetupDefault:            "Provider Setup · Default",
		MsgAuthTitleSetupReview:             "Provider Setup · Review",
		MsgAuthTitleSetupEdit:               "Provider Setup · Edit",
		MsgAuthTitleSetup:                   "Provider Setup",
		MsgAuthPromptProviderID:             "Enter provider ID:",
		MsgAuthPromptModelID:                "Enter model ID:",
		MsgAuthPromptModelName:              "Enter display name for '%s' (empty = use ID):",
		MsgAuthPromptHeaderName:             "Enter header name:",
		MsgAuthPromptHeaderValue:            "Enter value for header '%s':",
		MsgAuthPromptInput:                  "Input:",
		MsgAuthSettingsUnknownField:         "Unknown settings field",
	},
	LanguageZH: {
		MsgInputPlaceholder:                   "输入消息...",
		MsgCommandsTitle:                      "命令：",
		MsgCommandModeDescription:             "切换或显示执行模式（plan/agent/yolo/os）",
		MsgCommandESMDescription:              "启用或查看 Supervisor Mode",
		MsgCommandModelDescription:            "切换或显示模型",
		MsgCommandDefaultModelDescription:     "设置默认 Provider/模型",
		MsgCommandAuthDescription:             "配置 Provider token、Base URL 和模型",
		MsgCommandSettingsDescription:         "配置 settings.json 设置组，包括 Provider",
		MsgCommandTUILangDescription:          "设置 TUI 语言（默认全局）",
		MsgCommandSkillsDescription:           "列出可用 Skill",
		MsgCommandSkillHubDescription:         "浏览、搜索并安装市场 Skill",
		MsgCommandEnvDescription:              "管理额外环境变量",
		MsgCommandSkillDescription:            "启用 Skill",
		MsgCommandPasteImageDescription:       "保存剪贴板图片并插入本地路径",
		MsgCommandClearDescription:            "清空会话",
		MsgCommandCompactDescription:          "触发上下文压缩",
		MsgCommandSessionsDescription:         "列出、切换、新建或删除会话",
		MsgCommandExpertDescription:           "列出、查看、绑定或通过分叉切换主角人设",
		MsgCommandInitMCPDescription:          "初始化 mcp.json",
		MsgCommandMCPsDescription:             "列出 MCP 服务器",
		MsgCommandDelegateDescription:         "切换委派模式",
		MsgCommandBrowserDescription:          "切换浏览器自动化工具",
		MsgCommandStatsDescription:            "管理或显示用量统计",
		MsgCommandStatusLineDescription:       "查看或切换 TUI 状态栏",
		MsgCommandAllowEditPathDescription:    "管理自动编辑路径白名单",
		MsgCommandAllowAutoEditDescription:    "切换 agent 模式完整自动编辑",
		MsgCommandBTWDescription:              "提问旁支问题，不影响主任务",
		MsgCommandSystemInitDescription:       "生成或刷新 AGENTS.md",
		MsgCommandRuleDescription:             "创建安全的默认项目规则",
		MsgCommandReloadDescription:           "以新会话重启进程",
		MsgCommandWorkflowsDescription:        "查看 Workflow 运行",
		MsgCommandAgentDescription:            "管理多 Agent 工作器",
		MsgCommandCronDescription:             "管理定时任务",
		MsgCommandQuitDescription:             "退出",
		MsgCommandHelpDescription:             "显示此帮助",
		MsgCommandUsage:                       "用法：%s",
		MsgKeyboardShortcutsTitle:             "键盘快捷键：",
		MsgShortcutSubmitInput:                "提交输入",
		MsgShortcutInsertNewline:              "在输入中插入换行",
		MsgShortcutCycleMode:                  "循环切换模式（plan/agent/yolo/os）",
		MsgShortcutAbort:                      "中止当前操作",
		MsgShortcutToolDetails:                "打开最新工具详情",
		MsgShortcutESMProgress:                "打开 Supervisor Mode 进度",
		MsgShortcutPreviewImage:               "预览最近粘贴的图片",
		MsgShortcutCompactTools:               "切换简洁/完整事件显示",
		MsgShortcutMoveHistory:                "在多行输入内移动；边界处浏览历史",
		MsgShortcutSwitchDetailTarget:         "在 Ctrl+O 详情弹窗中切换目标",
		MsgShortcutPagePanel:                  "翻阅详情或 ESM 进度面板",
		MsgUnknownCommand:                     "未知命令：%s",
		MsgCommandAgentUnknown:                "未知 Agent 命令：%s",
		MsgCommandMultiAgentStatus:            "多 Agent 模式：开启（当前：%s）",
		MsgCommandAgentManagerUnavailable:     "Agent 管理器尚未初始化。",
		MsgCommandNoAgents:                    "没有正在运行的 Agent。",
		MsgCommandAgentNotFound:               "未找到 Agent %s。",
		MsgCommandAgentFocused:                "已聚焦 Agent 标签：%s",
		MsgCommandAgentInputHint:              "输入仍会发送给主 Agent；后续指令请使用 subagent_send。",
		MsgCommandDelegateStatus:              "委派模式：%s",
		MsgCommandDelegateRunning:             "Agent 运行时不能切换委派模式。",
		MsgCommandDelegateChanged:             "委派模式：%s",
		MsgCommandBrowserStatus:               "浏览器工具：%s",
		MsgCommandBrowserRunning:              "Agent 运行时不能切换浏览器工具。",
		MsgCommandToolRegistryUnavailable:     "工具注册表尚未初始化。",
		MsgCommandBrowserSkillFailed:          "创建浏览器 Skill 失败：%v",
		MsgCommandBrowserSkillLoadFailed:      "加载 Skill 失败：%v",
		MsgCommandMode:                        "模式：%s",
		MsgCommandCurrentMode:                 "当前模式：%s",
		MsgCommandPermissionsPlan:             "  权限：仅 READ（不修改）",
		MsgCommandPermissionsAgent:            "  权限：READ/WRITE/EDIT 自动执行 | BASH 需要审批",
		MsgCommandPermissionsYolo:             "  权限：所有工具自动执行",
		MsgCommandPermissionsOS:               "  权限：仅 BASH，自动执行（无沙箱；黑名单命令仍需审批）",
		MsgCommandInvalidMode:                 "无效模式",
		MsgCommandModelNotFound:               "未找到模型 %q——可用模型：%s",
		MsgCommandModelSwitched:               "✅ 模型已切换到：%s（%s）",
		MsgCommandRunningCannotOpen:           "Agent 运行时不能打开 %s。",
		MsgCommandNothingToCompact:            "没有可压缩内容：当前没有活动会话。",
		MsgConversationCleared:                "✅ 会话已清空",
		MsgSkillsUnavailable:                  "没有可用的 Skill 管理器。",
		MsgSkillsEmpty:                        "未找到 Skill。",
		MsgSkillNotFound:                      "未找到 Skill：%s",
		MsgSkillAlreadyActive:                 "Skill '%s' 已经启用。",
		MsgSkillActivated:                     "✅ Skill '%s' 已启用（%s）：%s",
		MsgAllowEditPathTitle:                 "自动编辑路径白名单（agent 模式）：",
		MsgAllowEditPathAlready:               "白名单中已经存在：%s",
		MsgAllowEditPathNotFound:              "白名单中不存在：%s",
		MsgAllowEditPathSaved:                 "✅ %s 自动编辑白名单：%s",
		MsgAllowEditPathCleared:               "✅ 自动编辑路径白名单已清空",
		MsgAllowAutoEditStatus:                "自动编辑（agent 模式）：%s",
		MsgAllowAutoEditSaved:                 "✅ 自动编辑（agent 模式）：%s [%s]",
		MsgAllowAutoEditEffective:             "（当前实际生效：%s，因项目配置覆盖）",
		MsgAgentCannotDestroyMain:             "不能销毁主 Agent",
		MsgAgentDestroyFailed:                 "销毁 Agent %s 失败：%v",
		MsgAgentDestroyed:                     "Agent %s 已销毁",
		MsgCronRequiresMultiAgent:             "Cron 命令需要多 Agent 模式。请使用 --multi-agent 重启以启用。",
		MsgCronStoreUnavailable:               "Cron 存储尚未初始化。",
		MsgCronCreated:                        "✅ Cron 任务已创建：%s（ID：%s）",
		MsgCronListEmpty:                      "Cron 任务：（未配置）",
		MsgCronListTitle:                      "Cron 任务（%d）：",
		MsgCronEntry:                          "  %s [%s] %s（运行次数：%d）",
		MsgCronChanged:                        "Cron 任务 %s %s",
		MsgCronSchedulerUnavailable:           "调度器未运行。",
		MsgCronTriggered:                      "▶ Cron 任务 %s 已触发（将在下一次调度器 tick 执行）",
		MsgCronUnknownCommand:                 "未知 Cron 命令：%s",
		MsgWorkflowListFailed:                 "列出 Workflow 失败：%v",
		MsgWorkflowListEmpty:                  "Workflow 运行：（无）",
		MsgWorkflowListTitle:                  "Workflow 运行（%d）：",
		MsgWorkflowShowFailed:                 "加载 Workflow 失败：%v",
		MsgWorkflowTitle:                      "Workflow %s：%s",
		MsgWorkflowName:                       "名称：%s",
		MsgWorkflowPhase:                      "阶段 [%s] %s，任务数=%d",
		MsgWorkflowError:                      "错误：%s",
		MsgWorkflowNotActive:                  "Workflow 运行 %s 未处于活动状态。",
		MsgWorkflowCancelRequested:            "已请求取消 Workflow 运行 %s。",
		MsgSystemInitRunning:                  "Agent 运行时不能执行 /systeminit。",
		MsgSystemInitCompacting:               "上下文压缩运行时不能执行 /systeminit。",
		MsgSystemInitSwitchedMode:             "已切换到 AGENT 模式执行 /systeminit（AGENTS.md 需要写权限）。",
		MsgSystemInitInteractive:              "🛠 /systeminit：正在分析项目；我会先询问几个问题，然后写入 AGENTS.md。",
		MsgSystemInitAutomatic:                "🛠 /systeminit：正在分析项目并写入 AGENTS.md……",
		MsgRuleRunning:                        "Agent 运行时不能修改 /rule。",
		MsgRuleWriteFailed:                    "写入规则文件失败：%v",
		MsgRuleCreated:                        "%s 规则文件：%s",
		MsgRuleOverwritten:                    "已覆盖",
		MsgRuleLoaded:                         "已加载到当前会话。",
		MsgRuleExists:                         "规则文件已存在：%s",
		MsgReloading:                          "↻ 正在重新加载：以新会话启动全新进程……",
		MsgCompactRunning:                     "Agent 运行时不能压缩上下文。",
		MsgModeChangeAborted:                  "⏹ 已中止（模式切换）",
		MsgCommandAgentEntry:                  "  %s [%s]",
		MsgCommandAgentParent:                 " 父级=%s",
		MsgCommandAgentChildren:               " 子级=%d",
		MsgCommandBrowserSkillAction:          "%s：%s",
		MsgAllowEditPathSaveFailed:            "保存 allow.json 失败：%v",
		MsgAllowAutoEditSavedEffective:        "（当前实际生效：%s，因项目配置覆盖）",
		MsgCronCreateFailed:                   "创建 Cron 任务失败：%v",
		MsgCronListFailed:                     "列出 Cron 任务失败：%v",
		MsgCronChangedEnabled:                 "已启用",
		MsgCronChangedDisabled:                "已禁用",
		MsgCronChangedRemoved:                 "已移除",
		MsgWorkflowListEntry:                  "  [%s] %s %s（%s）",
		MsgWorkflowResultEntry:                "\n%s [%s]\n%s\n",
		MsgSettingsTitle:                      "设置",
		MsgSettingsLanguage:                   "TUI 语言",
		MsgSettingsLanguageDescription:        "配置=%s  生效=%s  %s  来源=%s",
		MsgSettingsLanguageScope:              "保存范围",
		MsgSettingsLanguageScopeDescription:   "%s",
		MsgSettingsLanguageSave:               "保存语言设置",
		MsgSettingsLanguageSaveDescription:    "保存语言并立即应用",
		MsgSettingsLanguageSaved:              "TUI 语言已保存到%s：%s（当前生效：%s）",
		MsgSettingsLanguageSaveFailed:         "保存 TUI 语言失败：%v",
		MsgSettingsLanguageProjectUnavailable: "当前目录不是可识别的项目目录，无法使用项目范围。",
		MsgTUILangStatus:                      "TUI 语言：配置=%s  生效=%s  %s  来源=%s",
		MsgBTWTitle:                           "💬 /btw：%s",
		MsgBTWError:                           "错误：%v",
		MsgBTWThinking:                        "正在思考…",
		MsgBTWNoAnswer:                        "（暂无回答）",
		MsgBTWStatus:                          "[%s] 第 %d-%d/%d 行  ↑/↓：滚动  Esc：关闭（不会保存到主任务）",
		MsgBackgroundRunStatus:                "后台任务 %s 状态：%s%s",
		MsgSettingsDone:                       "完成",
		MsgSettingsReturn:                     "返回设置",
		MsgEnterSelect:                        "Enter 选择，↑↓ 移动，Esc 返回",
		MsgEnterSubmit:                        "Enter 提交，Esc 返回",
		MsgEnterSave:                          "Enter 保存，Esc 返回",
		MsgYouPrefix:                          "你：",
		MsgErrorPrefix:                        "错误：",
		MsgSessionEndedPrefix:                 "会话结束：",
		MsgCompacting:                         "⏳ 正在压缩上下文...",
		MsgContextCompacted:                   "✅ 上下文已压缩",
		MsgCompactionFailed:                   "上下文压缩失败：",
		MsgAborted:                            "⏹ 已中止",
		MsgHostedItemTitle:                    "托管项目",
		MsgMemberQuestionToLead:               "ℹ️ 成员 %s 向主 agent 提问（由主 agent 回答）：%s",
		MsgNoConversationDetails:              "暂无会话详情。",
		MsgApprovalTitle:                      "需要审批：%s",
		MsgApprovalRequestTitle:               "需要审批：%s",
		MsgApprovalApproveOnce:                "仅批准本次",
		MsgApprovalApproveOnceDescription:     "仅运行当前待处理工具调用",
		MsgApprovalDeny:                       "拒绝",
		MsgApprovalDenyDescription:            "拒绝当前待处理工具调用",
		MsgApprovalRememberExact:              "始终允许此完整命令",
		MsgApprovalRememberPrefix:             "始终允许此命令前缀",
		MsgApprovalProjectRule:                "项目规则：%s",
		MsgApprovalHint:                       "Enter 选择 · ↑/↓ 移动 · y 批准 · n 拒绝 · Esc 中止",
		MsgApprovalSavedExact:                 "已批准并为当前项目记住此完整命令",
		MsgApprovalSavedPrefix:                "已批准并记住项目命令前缀：%s",
		MsgAuthExistingProvidersDescription:   "在已有 Provider 下新增或更新 token/model",
		MsgAuthCustomProviderDescription:      "按 API 类型、Base URL、token 和模型新增 Provider",
		MsgAuthBackDescription:                "返回主菜单",
		MsgAuthYesDescription:                 "今后的请求使用此 Provider/模型",
		MsgAuthNoDescription:                  "保存 Provider，但不修改默认设置",
		MsgAuthSaveDescription:                "写入 settings.json 并切换当前 TUI Provider",
		MsgAuthEditDescription:                "返回修改 Provider 配置",
		MsgAuthModelsCount:                    "%d 个模型",
		MsgAuthDefaultDescription:             "设为默认：%v",
		MsgAuthLoadGlobalFailed:               "加载全局设置失败：%v",
		MsgAuthProviderValidationFailed:       "Provider 校验失败：%v",
		MsgAuthSaveFailed:                     "保存失败：%v",
		MsgAuthReloadFailed:                   "全局设置已保存，但重新加载失败：%v",
		MsgAuthEffectiveValidationFailed:      "全局设置已保存，但有效 Provider 校验失败：%v",
		MsgActivityHostedItem:                 "托管项目",
		MsgActivityToolStarted:                "工具已开始：%s",
		MsgActivityToolResult:                 "工具结果",
		MsgActivityDone:                       "完成",
		MsgActivityError:                      "错误：%s",
		MsgActivityCanceled:                   "已取消",
		MsgActivityNoActivity:                 "暂无活动记录",
		MsgActivityLatestTool:                 "最新工具：",
		MsgActivityThinking:                   "思考：",
		MsgActivityResponse:                   "回复：",
		MsgActivityLatestResult:               "最新结果：",
		MsgActivityTimeline:                   "活动时间线：",
		MsgActivityUpdated:                    "更新于 %s",
		MsgActivityAgoSeconds:                 "%d 秒前",
		MsgActivityAgoMinutes:                 "%d 分钟前",
		MsgAuthSearchLabel:                    "搜索：%s",
		MsgAuthNoProvidersMatch:               "没有匹配的 Provider。",
		MsgAuthNoModelsMatch:                  "没有匹配的模型。",
		MsgAuthShowingRange:                   "显示第 %d-%d 项，共 %d 项",
		MsgAuthMoreLinesHidden:                "… 另有 %d 行已隐藏",
		MsgAuthPlaceholderProviderID:          "Provider ID（例如 openrouter）",
		MsgAuthPlaceholderModelID:             "模型 ID",
		MsgAuthTitleConnectProvider:           "连接 Provider",
		MsgAuthTitleSettingsProviders:         "设置 · Provider",
		MsgAuthTitleExistingProvider:          "已有 Provider · Provider",
		MsgAuthTitleSettingsDefaults:          "设置 · 默认值",
		MsgAuthTitleSettingsBehavior:          "设置 · 行为",
		MsgAuthTitleSettingsWebSearch:         "设置 · Web 搜索",
		MsgAuthTitleSettingsContextFiles:      "设置 · 上下文文件",
		MsgAuthTitleSettingsStatusLine:        "设置 · 状态栏",
		MsgAuthTitleSettingsCompaction:        "设置 · 上下文压缩",
		MsgAuthTitleSettingsSandbox:           "设置 · 沙箱",
		MsgAuthTitleSettingsPaths:             "设置 · 路径",
		MsgAuthTitleSettingsRetry:             "设置 · 重试",
		MsgAuthTitleSettingsApproval:          "设置 · 审批",
		MsgAuthTitleCustomProviderID:          "自定义 Provider · Provider ID",
		MsgAuthTitleSettingsProvider:          "设置 · %s",
		MsgAuthTitleProviderSettings:          "Provider · %s · 设置",
		MsgAuthTitleProviderCredentials:       "Provider · 凭据",
		MsgAuthTitleProviderProtocol:          "Provider · 协议",
		MsgAuthTitleProviderNetwork:           "Provider · 网络",
		MsgAuthTitleProviderAdvanced:          "Provider · 高级",
		MsgAuthTitleProviderHeaders:           "Provider · 请求头",
		MsgAuthTitleProviderResponses:         "Provider · Responses",
		MsgAuthTitleProviderModels:            "Provider · 模型",
		MsgAuthTitleModelParameters:           "模型 · %s · 参数",
		MsgAuthTitleModelBasics:               "模型 · 基础",
		MsgAuthTitleModelCapabilities:         "模型 · 能力",
		MsgAuthTitleModelSampling:             "模型 · 采样",
		MsgAuthTitleModelCost:                 "模型 · 成本",
		MsgAuthTitleModelCompatibility:        "模型 · 兼容性",
		MsgAuthTitleAddModelID:                "添加模型 · ID",
		MsgAuthTitleAddModelName:              "添加模型 · 名称",
		MsgAuthTitleSetupDefault:              "Provider 配置 · 默认设置",
		MsgAuthTitleSetupReview:               "Provider 配置 · 预览",
		MsgAuthTitleSetupEdit:                 "Provider 配置 · 编辑",
		MsgAuthTitleSetup:                     "Provider 配置",
		MsgAuthPromptProviderID:               "输入 Provider ID：",
		MsgAuthPromptModelID:                  "输入模型 ID：",
		MsgAuthPromptModelName:                "输入“%s”的显示名称（留空则使用 ID）：",
		MsgAuthPromptHeaderName:               "输入请求头名称：",
		MsgAuthPromptHeaderValue:              "输入请求头“%s”的值：",
		MsgAuthPromptInput:                    "输入：",
		MsgApprovalMissingCommand:             "无法保存审批规则：缺少 bash 命令。",
		MsgApprovalMissingPrefix:              "无法保存审批规则：缺少 bash 命令前缀。",
		MsgApprovalSaveFailed:                 "保存 allow.json 失败：%v",
		MsgApprovalQueuedApproved:             "已批准 %d 个队列中匹配的命令",
		MsgApprovalPendingCount:               "（另有 %d 项待处理）",
		MsgApprovalCustomInput:                "自定义输入",
		MsgApprovalQuestionPrompt:             "请输入编号或自定义文本：",
		MsgApprovalChooseHint:                 "请在审批对话框中选择（↑/↓、Enter、y/n）。",
		MsgCommandLabel:                       "命令：",
		MsgTimeoutLabel:                       "超时：%v",
		MsgAsyncLabel:                         "异步：%v",
		MsgPathLabel:                          "路径：%s",
		MsgContentBytes:                       "内容：（%d 字节）",
		MsgToolModalNoOutput:                  "（无输出）",
		MsgToolModalNoConversation:            "（暂无会话）",
		MsgToolModalDiff:                      "--- 差异",
		MsgToolModalTitle:                     "Agent 详情",
		MsgToolModalPosition:                  "第 %d-%d/%d 行",
		MsgToolModalPositionEmpty:             "第 0-0/0 行",
		MsgToolModalSwitchTargetHint:          "←/→：切换目标",
		MsgToolModalPageHint:                  "PgUp/PgDn：翻页",
		MsgToolModalScrollHint:                "↑/↓：滚动",
		MsgToolModalCloseHint:                 "Esc：关闭",
		MsgToolModalEdited:                    "• 已编辑 %s",
		MsgToolModalUnknownPath:               "（未知路径）",
		MsgToolModalStateRunning:              "运行中",
		MsgToolModalStateReady:                "就绪",
		MsgToolModalStateDone:                 "完成",
		MsgToolModalStateError:                "错误",
		MsgToolModalStateCanceled:             "已取消",
		MsgToolModalStateUnknown:              "未知",
		MsgToolModalAgentTab:                  "[ %s %s ]",
		MsgToolModalMain:                      "主界面",
		MsgToolArgsPath:                       "路径：%v", MsgToolArgsContent: "内容：\n%s", MsgToolArgsEdit: "编辑[%d]：\n  旧：%s\n  新：%s", MsgToolArgsCommand: "命令：%v", MsgToolExecutionRunning: "%s 执行中：%v", MsgToolCommandRunning: "执行中", MsgToolCommandUnavailable: "命令不可用", MsgToolCommandStarted: "已启动", MsgToolCommandSucceeded: "执行成功", MsgToolCommandFailed: "执行失败", MsgToolCommandFailedExit: "执行失败（退出码 %d）", MsgToolSummaryEmpty: "...", MsgToolResultWritten: "已写入", MsgPlanUpdated: "计划已更新。", MsgPlanTitle: "计划", MsgPlanNote: "备注：%s", MsgToolEdited: "• 已编辑 %s",

		MsgApprovalCommandLabel:         "命令：",
		MsgApprovalTimeoutLabel:         "超时：%v",
		MsgApprovalAsyncLabel:           "异步：%v",
		MsgApprovalPathLabel:            "路径：%s",
		MsgApprovalContentBytes:         "内容：（%d 字节）",
		MsgApprovalDiffEmpty:            "差异：（空）",
		MsgOutputTruncated:              "⚠ 输出因达到最大 token 限制而被截断。",
		MsgOutputRetry:                  "⚠ 已达到输出限制；正以最大 %d token 重试...",
		MsgAutomaticRetry:               "↻ 正在重试（第 %d/%d 次）...",
		MsgAutomaticRetryWaiting:        "↻ 正在重试（第 %d/%d 次）；等待 %s...",
		MsgAutomaticRetryUnknown:        "↻ 正在重试...",
		MsgUsageTokens:                  "Token：%d↓/%d↑ $%.4f%s",
		MsgAttachmentsTitle:             "附件：",
		MsgAttachmentFallback:           "附件",
		MsgReasonSuffix:                 "（原因：%s）",
		MsgToolResultLines:              "%d 行",
		MsgToolResultApplied:            "已应用",
		MsgBackgroundStartFailed:        "后台任务启动失败：",
		MsgBackgroundSubmitted:          "后台任务已提交：%s",
		MsgLoading:                      "加载中...",
		MsgCompactToolsOn:               "简洁事件显示：开启",
		MsgCompactToolsOff:              "完整事件显示：开启",
		MsgStatsServerAlreadyRunning:    "统计服务器已在运行：%s",
		MsgStatsStarting:                "正在启动统计服务器……",
		MsgStatsNotRunning:              "统计服务器未运行。",
		MsgStatsStopping:                "正在停止统计服务器……",
		MsgStatsRunning:                 "统计服务器运行中：%s",
		MsgStatsStopFailed:              "停止统计服务器失败：%v",
		MsgStatsStopped:                 "统计服务器已停止。",
		MsgStatsLoading:                 "正在加载统计数据……",
		MsgStatsNoData:                  "暂无统计数据。",
		MsgStatsTitle:                   "VibeCoding 统计",
		MsgStatsRequests:                "请求数：      %d",
		MsgStatsInputTokens:             "输入 Token：  %d",
		MsgStatsOutputTokens:            "输出 Token：  %d",
		MsgStatsTotalTokens:             "Token 总数：  %d",
		MsgStatsByProvider:              "按 Provider",
		MsgStatsByModel:                 "按模型",
		MsgStatsRecentRequests:          "最近请求",
		MsgStatsNoRows:                  "  暂无数据",
		MsgStatsFooter:                  "统计  ↑↓：滚动  PgUp/PgDn：翻页  Esc：关闭",
		MsgSessionsTitle:                "会话",
		MsgSessionsNoSessions:           "当前项目暂无会话。",
		MsgSessionsAlreadyCurrent:       "当前已经是此会话。",
		MsgSessionsCannotSwitchRunning:  "Agent 运行时无法切换会话。",
		MsgSessionsSwitched:             "✅ 已切换到会话 %s（%d 条消息）",
		MsgSessionsCannotDeleteCurrent:  "无法删除当前会话，请先切换到其他会话。",
		MsgSessionsDeleted:              "✅ 已删除会话 %s。",
		MsgSessionsUnknownSubcommand:    "未知子命令：%s。可使用 /sessions，或 ls、set、clear、del。",
		MsgSessionsErrorListing:         "列出会话失败：%v",
		MsgSessionsDeleteFailed:         "删除会话失败：%v",
		MsgSessionsAmbiguousID:          "会话 ID “%s” 匹配了多个会话，请提供更具体的前缀。",
		MsgSessionsNoMatch:              "未找到与 “%s” 匹配的会话。",
		MsgSessionsNewOnNextMessage:     "✅ 发送下一条消息时将创建新会话。",
		MsgSessionsListTitle:            "当前项目的会话：",
		MsgSessionsListHint:             "使用 /sessions set <id> 切换。* 表示当前会话。",
		MsgSessionsShowingRange:         "显示第 %d-%d 项，共 %d 项",
		MsgSessionsDialogHint:           "Enter 切换  上/下选择  n 新建  d 删除  Esc 关闭",
		MsgSessionsAgeJustNow:           "刚刚",
		MsgSessionsAgeMinutes:           "%d 分钟前",
		MsgSessionsAgeHours:             "%d 小时前",
		MsgSessionsAgeDays:              "%d 天前",
		MsgClipboardPasteFailed:         "粘贴图片失败：%v",
		MsgClipboardNoPNG:               "剪贴板不包含 PNG 图片，请复制图片后再次运行 /paste-image。",
		MsgClipboardImagePath:           "图片路径：%s",
		MsgClipboardImagePasted:         "已粘贴图片：%s",
		MsgClipboardPreviewHint:         "按 Ctrl+R 预览。",
		MsgClipboardNoImage:             "没有可预览的粘贴图片，请先运行 /paste-image。",
		MsgClipboardOpenFailed:          "无法打开粘贴的图片：%v。路径：%s",
		MsgClipboardOpened:              "已打开预览：%s",
		MsgDefaultModelTitle:            "设置默认模型（%s）",
		MsgDefaultModelNoMatch:          "没有匹配的选项。",
		MsgDefaultModelFooter:           "Enter 选择，↑↓ 导航，Esc 返回",
		MsgDefaultModelLoadFailed:       "加载 %s 设置失败：%v",
		MsgDefaultModelValidationFailed: "Provider 校验失败：%v",
		MsgDefaultModelSaveFailed:       "保存 %s 设置失败：%v",
		MsgDefaultModelSaved:            "✅ 默认模型已保存（%s）：%s / %s",
		MsgSettingsRunning:              "Agent 运行时无法打开 /settings。",
		MsgMultiAgentConfigured:         "多 Agent 模式在启动时配置。请使用 --multi-agent 启用子 Agent 工具。",
		MsgMultiAgentDisabled:           "多 Agent 模式未启用。请使用 --multi-agent 重新启动以启用子 Agent 工具。",
		MsgAssistantPrefix:              "助手：",
		MsgThinkingPrefix:               "思考：",
		MsgThinkingStatus:               "思考中...",
		MsgCancelHint:                   "按 Esc 取消",
		MsgFooterLastDuration:           "上次用时 %s",
		MsgFooterToolModalHints:         "左/右:切换 PgUp/PgDn:翻页 上/下:滚动 Esc/Ctrl+O:关闭",
		MsgFooterMainHints:              "Tab:模式 Esc:中止 Ctrl+O:详情 Ctrl+E:ESM Ctrl+R:预览 Ctrl+G:事件",
		MsgFooterApprovalAlert:          "! 需要审批：↑/↓ Enter",
		MsgLanguageConfiguredSource:     "%s",
		MsgLanguageScopeGlobal:          "全局默认",
		MsgLanguageScopeProject:         "当前项目",
		MsgLanguageSourceGlobal:         "全局",
		MsgLanguageSourceProject:        "项目覆盖",
		MsgAuthExistingProviders:        "已有 Provider",
		MsgAuthCustomProvider:           "自定义 Provider",
		MsgAuthBack:                     "← 返回",
		MsgAuthYes:                      "是",
		MsgAuthNo:                       "否",
		MsgAuthSave:                     "保存",
		MsgAuthEdit:                     "编辑",
		MsgAuthDone:                     "完成",
		MsgAuthModels:                   "模型",
		MsgAuthDefault:                  "默认设置",
		MsgAuthProviderSettings:         "Provider 设置",
		MsgAuthAddModel:                 "+ 添加模型",
		MsgAuthReviewSave:               "预览并保存",
		MsgAuthSearch:                   "输入内容搜索",
		MsgAuthEnterSelect:              "Enter 选择，↑↓ 移动，Esc 返回",
		MsgAuthEnterSubmit:              "Enter 提交，Esc 返回",
		MsgAuthEnterSave:                "Enter 保存，Esc 返回",
		MsgAuthProviderIDInvalid:        "Provider ID 不能为空，且不能包含空格或斜杠。",
		MsgAuthModelIDInvalid:           "模型 ID 不能为空，且不能包含空白字符。",
		MsgAuthModelIDExists:            "模型 ID 已存在。",
		MsgAuthProviderSaved:            "✅ Provider 已保存：%s / %s",
		MsgAuthProviderSavedHint:        "下一条消息将使用新的 Provider/模型。",
		MsgAuthSettingsSaved:            "设置已保存：%s",
		MsgAuthSettingsSaveFailed:       "保存设置失败：%v",
		MsgAuthSettingsReloadFailed:     "重新加载设置失败：%v",
		MsgAuthProviderIDLabel:          "Provider ID",
		MsgAuthSettingsUnknownField:     "未知设置字段",
	},
}
