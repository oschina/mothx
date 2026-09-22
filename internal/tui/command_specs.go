package tui

import "github.com/oschina/mothx/internal/tui/i18n"

// CommandSpec keeps slash-command syntax stable while localizing user-facing
// descriptions. Syntax is protocol text and must remain English.
type CommandSpec struct {
	Name        string
	Value       string
	Usage       string
	Description i18n.MessageID
}

var commandSpecs = []CommandSpec{
	{Name: "/auth", Value: "/auth", Usage: "/auth", Description: i18n.MsgCommandAuthDescription},
	{Name: "/settings", Value: "/settings", Usage: "/settings", Description: i18n.MsgCommandSettingsDescription},
	{Name: "/tuilang", Value: "/tuilang ", Usage: "/tuilang [global|project] [auto|zh|en]", Description: i18n.MsgCommandTUILangDescription},
	{Name: "/mode", Value: "/mode ", Usage: "/mode [plan|agent|yolo|os]", Description: i18n.MsgCommandModeDescription},
	{Name: "/esm", Value: "/esm ", Usage: "/esm [objective|edit|pause|resume|clear|guide]", Description: i18n.MsgCommandESMDescription},
	{Name: "/model", Value: "/model ", Usage: "/model [model_id]", Description: i18n.MsgCommandModelDescription},
	{Name: "/defaultModel", Value: "/defaultModel ", Usage: "/defaultModel [project|global]", Description: i18n.MsgCommandDefaultModelDescription},
	{Name: "/env", Value: "/env ", Usage: "/env [list|set KEY VALUE|unset KEY|clear]", Description: i18n.MsgCommandEnvDescription},
	{Name: "/skillhub", Value: "/skillhub", Usage: "/skillhub [search <q>]", Description: i18n.MsgCommandSkillHubDescription},
	{Name: "/skills", Value: "/skills", Usage: "/skills", Description: i18n.MsgCommandSkillsDescription},
	{Name: "/skill", Value: "/skill ", Usage: "/skill <name>", Description: i18n.MsgCommandSkillDescription},
	{Name: "/paste-image", Value: "/paste-image", Usage: "/paste-image", Description: i18n.MsgCommandPasteImageDescription},
	{Name: "/clear", Value: "/clear", Usage: "/clear", Description: i18n.MsgCommandClearDescription},
	{Name: "/compact", Value: "/compact", Usage: "/compact", Description: i18n.MsgCommandCompactDescription},
	{Name: "/sessions", Value: "/sessions", Usage: "/sessions [ls|set <id>|clear|del <id>]", Description: i18n.MsgCommandSessionsDescription},
	{Name: "/expert", Value: "/expert ", Usage: "/expert [list|show <id>|bind <id>|unbind|switch <id>]", Description: i18n.MsgCommandExpertDescription},
	{Name: "/init_mcp", Value: "/init_mcp ", Usage: "/init_mcp [project|global] [basic|full] [--force]", Description: i18n.MsgCommandInitMCPDescription},
	{Name: "/mcps", Value: "/mcps", Usage: "/mcps", Description: i18n.MsgCommandMCPsDescription},
	{Name: "/delegate", Value: "/delegate ", Usage: "/delegate [on|off|status]", Description: i18n.MsgCommandDelegateDescription},
	{Name: "/browser", Value: "/browser ", Usage: "/browser [on|off|status]", Description: i18n.MsgCommandBrowserDescription},
	{Name: "/stats", Value: "/stats ", Usage: "/stats server|stop-server|tui", Description: i18n.MsgCommandStatsDescription},
	{Name: "/statusline", Value: "/statusline ", Usage: "/statusline [status|on|off|command|refresh] ...", Description: i18n.MsgCommandStatusLineDescription},
	{Name: "/alloweditpath", Value: "/alloweditpath ", Usage: "/alloweditpath [add <glob>|remove <glob>|clear]", Description: i18n.MsgCommandAllowEditPathDescription},
	{Name: "/allowautoedit", Value: "/allowautoedit ", Usage: "/allowautoedit [on|off] [global]", Description: i18n.MsgCommandAllowAutoEditDescription},
	{Name: "/btw", Value: "/btw ", Usage: "/btw <question>", Description: i18n.MsgCommandBTWDescription},
	{Name: "/systeminit", Value: "/systeminit ", Usage: "/systeminit [guidance]", Description: i18n.MsgCommandSystemInitDescription},
	{Name: "/rule", Value: "/rule", Usage: "/rule [force|--force]", Description: i18n.MsgCommandRuleDescription},
	{Name: "/reload", Value: "/reload", Usage: "/reload", Description: i18n.MsgCommandReloadDescription},
	{Name: "/workflows", Value: "/workflows ", Usage: "/workflows [list|show <id>|cancel <id>]", Description: i18n.MsgCommandWorkflowsDescription},
	{Name: "/agent", Value: "/agent ", Usage: "/agent list|switch <id>|destroy <id>", Description: i18n.MsgCommandAgentDescription},
	{Name: "/cron", Value: "/cron ", Usage: "/cron add|list|enable|disable|remove|run", Description: i18n.MsgCommandCronDescription},
	{Name: "/help", Value: "/help", Usage: "/help", Description: i18n.MsgCommandHelpDescription},
	{Name: "/quit", Value: "/quit", Usage: "/quit", Description: i18n.MsgCommandQuitDescription},
}
