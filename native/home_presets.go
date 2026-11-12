package main

// This file ports the Desktop Home presets verbatim. Home presets are shell
// presentation data: they inject text into the Runtime-owned composer but never
// become a second prompt or execution path.

type homePreset struct {
	ID                   string
	SubZh, SubEn         string
	DescZh, DescEn       string
	QuickZh, QuickEn     []string
	TagZh, TagEn         []string
	PromptsZh, PromptsEn []string
}

const homeExecutionProtocolZh = `

【执行协议】你是一位对结果负责的领域专家。开始前，请先提炼本任务的业务背景、目标用户、成功标准、已有事实和未知信息；只能使用我提供的材料或明确标注的假设，不能把推测、示例或常识伪装成事实。若缺少会实质影响结论、实现路径、时间、成本、权限或合规判断的信息，请先按优先级列出不超过 5 个澄清问题并暂停关键结论；若可以继续，请显式写出假设、它们的影响以及后续验证方式。

请将工作拆成有序步骤：先分析现状与约束，再提出可选方案和取舍，选择推荐方案后执行或形成可执行计划。每一步都要说明产出、依赖、负责人角色或所需输入、完成判据；把范围内工作与明确不做的事项分开。对涉及数据、代码、外部信息、成本、时间表或指标的内容，说明来源、计算口径或验证方法；对隐私、安全、版权、无障碍、兼容性、性能、预算和交付风险，按本任务相关性进行检查，并给出可操作的缓解措施。不要编造引用、链接、测试结果、用户反馈、实现状态或已获批准的决策。

请只用 Markdown 交付，至少包含：1. 执行摘要；2. 已知输入、待确认项与假设；3. 目标、范围与非目标；4. 分步骤的工作方案或结果；5. 方案取舍、风险和依赖；6. 可验证的验收清单；7. 下一步行动（按优先级、责任角色和建议时间排序）。需要比较时使用表格，需要示例时给出可直接使用且与输入一致的示例。完成前进行一次自检：每项结论均可追溯、每个行动可执行、每条验收标准可观察或测试，并明确说明仍无法确认的部分。`

const homeExecutionProtocolEn = `

## Execution protocol

Act as a domain expert accountable for the result. Before beginning, extract the business context, target users, success criteria, established facts, and unknowns. Use only material I provide or assumptions you label explicitly; never present inference, examples, or general knowledge as verified fact. If missing information would materially affect the conclusion, implementation path, schedule, cost, permissions, or compliance decision, list at most five prioritized clarification questions and defer the affected conclusion. If work can proceed, state every assumption, its impact, and how it should be validated.

Break the work into an ordered process: analyze the current state and constraints, compare viable options and trade-offs, then execute or produce an implementation-ready recommendation. For every step, name its output, dependencies, required input or accountable role, and completion criterion. Separate in-scope work from explicit non-goals. For data, code, external information, costs, schedules, or metrics, state the source, definition, calculation, or validation method. Review privacy, security, copyright, accessibility, compatibility, performance, budget, and delivery risk when relevant, and give concrete mitigations. Do not invent citations, links, test results, customer feedback, implementation status, or approved decisions.

Deliver only Markdown with at least: 1. an executive summary; 2. known inputs, open questions, and assumptions; 3. objectives, scope, and non-goals; 4. the step-by-step plan or result; 5. trade-offs, risks, and dependencies; 6. a verifiable acceptance checklist; and 7. next actions ordered by priority, accountable role, and suggested timing. Use tables for comparisons and provide examples that are directly usable and consistent with the supplied input. Before finishing, self-review: every conclusion must be traceable, every action executable, every acceptance criterion observable or testable, and every remaining uncertainty explicit.`

var homePresets = map[string]homePreset{
	"working": {
		SubZh:     "办公事务与知识生产",
		SubEn:     "Office tasks & knowledge work",
		DescZh:    "将整理、写作、规划与沟通交给 AI 智能体推进，获得结构化、可落地的工作成果。",
		DescEn:    "Delegate organizing, writing, planning, and communication to the agent and get structured, actionable results.",
		QuickZh:   []string{`会议纪要`, `邮件起草`, `竞品调研`, `项目计划`, `数据报告`, `方案文档`},
		QuickEn:   []string{`Meeting notes`, `Email draft`, `Market research`, `Project plan`, `Data report`, `Proposal doc`},
		TagZh:     []string{`记录`, `文书`, `调研`, `规划`, `分析`, `文档`},
		TagEn:     []string{`Notes`, `Draft`, `Research`, `Plan`, `Analyze`, `Doc`},
		PromptsZh: []string{`请根据本次会议录音或记录，整理出一份结构化会议纪要。输入为 [会议主题/参会人/时间/原始记录/附件]；目标是准确还原讨论要点、决策结论、待办事项及责任人。工作范围包括：按议程梳理关键发言、合并重复观点、标记需要跟进的行动项。交付物为 Markdown 格式，包含会议信息、议程摘要、决议事项、待办清单（含负责人与截止日）。验收要点：要点完整无遗漏、决策表述明确、待办可执行。`, `请帮我起草一封专业商务邮件。背景与输入为 [收件人/关系/目的/关键信息/期望行动]；目标是获得清晰、得体的沟通效果。工作范围包括：确定主题行、撰写称呼与正文、列出附件/链接、提供落款与抄送建议。交付物为可直接发送的邮件全文（含主题）。约束与验收要点：语气匹配收件人身份、正文三段以内说明白目的、无歧义与错别字。`, `请针对 [目标产品/市场/竞品名称清单] 开展一次竞品调研。目标是梳理竞争格局、核心差异与可落地方案。工作范围包括：收集产品定位、功能对比、定价策略、用户评价与渠道信息，制作 SWOT 分析。交付物为 Markdown 报告，包含执行摘要、对比表格、优劣势分析、机会/威胁与下一步行动建议。验收要点：信息可追溯、对比维度一致、结论有数据或案例支撑。`, `请为 [项目名称/目标/范围] 制定一份可执行项目计划。输入包括背景、关键干系人、约束条件与预期成果；目标是将目标拆解为可跟踪的任务。工作范围包括：定义里程碑、WBS 任务拆分、排期、资源与风险点。交付物为 Markdown 文档，含项目目标、里程碑时间表、任务清单（含负责人）、风险登记册与沟通机制。验收要点：任务可执行、里程碑可衡量、风险有应对措施。`, `请基于 [数据来源/指标/时间范围] 生成一份数据分析报告。目标是提炼关键发现、趋势与业务建议。工作范围包括：清洗口径说明、核心指标计算、趋势对比、异常点标注与可视化建议。交付物为 Markdown 报告，含分析背景、指标定义、数据摘要、趋势分析、结论与行动建议。验收要点：口径一致、结论有数据支撑、建议可落地。`, `请为 [方案/项目名称] 编写一份完整的方案文档。背景与输入包括现状痛点、目标用户/场景、约束条件与成功标准。工作范围包括：问题定义、目标与范围、方案选型、实施路径、资源与排期、风险评估。交付物为 Markdown 格式方案文档，含摘要、背景、方案详述、实施计划、验收标准与附录。验收要点：逻辑自洽、方案可执行、风险与约束考虑周全。`},
		PromptsEn: []string{`Please produce structured meeting notes from this meeting transcript or recording. Input: [meeting title/attendees/date/raw notes/attachments]. Goal: accurately capture discussion points, decisions, action items, and owners. Scope: organize key remarks by agenda, merge duplicate points, and flag follow-up tasks. Deliverable: a Markdown document with meeting info, agenda summary, decisions, and a TODO list with owners and due dates. Acceptance: complete coverage, clear decisions, and actionable tasks.`, `Draft a professional business email for me. Context/input: [recipient/relationship/purpose/key points/desired action]. Goal: achieve clear, appropriate communication. Scope: craft a subject line, salutation, body, list attachments/links, and provide sign-off and CC suggestions. Deliverable: a ready-to-send email including subject line. Acceptance: tone matches the recipient, body states the purpose in three paragraphs or fewer, and there are no ambiguities or typos.`, `Conduct competitive research for [target product/market/list of competitors]. Goal: map the competitive landscape, key differentiators, and actionable options. Scope: gather positioning, feature comparisons, pricing, user feedback, and channel information; produce a SWOT analysis. Deliverable: a Markdown report with executive summary, comparison table, strengths/weaknesses, opportunities/threats, and next-step recommendations. Acceptance: traceable information, consistent comparison dimensions, and conclusions backed by data or examples.`, `Create an actionable project plan for [project name/goals/scope]. Input: background, stakeholders, constraints, and expected outcomes. Goal: decompose objectives into trackable tasks. Scope: define milestones, WBS task breakdown, schedule, resources, and risks. Deliverable: a Markdown document with project goals, milestone timeline, task list with owners, risk register, and communication cadence. Acceptance: tasks are executable, milestones are measurable, and risks have mitigation plans.`, `Generate a data analysis report based on [data source/metrics/time range]. Goal: extract key findings, trends, and business recommendations. Scope: define data definitions, normalize inputs, calculate core metrics, compare trends, flag anomalies, and suggest visualizations. Deliverable: a Markdown report with background, metric definitions, data summary, trend analysis, conclusions, and action items. Acceptance: consistent definitions, data-backed conclusions, and actionable recommendations.`, `Write a complete proposal document for [initiative/project name]. Context/input: current pain points, target users/scenarios, constraints, and success criteria. Scope: define the problem, goals, solution options, implementation path, resources/schedule, and risks. Deliverable: a Markdown proposal with summary, background, detailed solution, rollout plan, acceptance criteria, and appendix. Acceptance: internally consistent logic, executable plan, and thorough risk/constraint coverage.`},
	},
	"coding": {
		SubZh:     "软件开发与工程交付",
		SubEn:     "Software development & delivery",
		DescZh:    "将需求拆解、实现、重构与测试委派给 AI 智能体执行，在对话中集中审阅代码变更与工程产出。",
		DescEn:    "Delegate requirements breakdown, implementation, refactoring, and testing to the agent, and review code changes and engineering output in the conversation.",
		QuickZh:   []string{`需求拆解`, `缺陷诊断`, `代码审查`, `重构优化`, `单元测试`, `API 文档`},
		QuickEn:   []string{`Spec breakdown`, `Bug diagnosis`, `Code review`, `Refactor & optimize`, `Unit tests`, `API docs`},
		TagZh:     []string{`分析`, `调试`, `审查`, `重构`, `测试`, `文档`},
		TagEn:     []string{`Analyze`, `Debug`, `Review`, `Refactor`, `Test`, `Docs`},
		PromptsZh: []string{`请将以下需求拆解为清晰的开发任务。输入为 [业务需求/用户故事/PRD 链接/约束条件]；目标是输出可排期、可验收的开发计划。工作范围包括：识别参与角色、拆分功能点与非功能点、定义验收标准、梳理依赖与优先级。交付物为 Markdown 文档，含需求摘要、任务清单（含优先级/估点/依赖/验收标准）和风险提示。验收要点：任务粒度适中、验收标准明确、依赖无遗漏。`, `请分析以下代码或报错信息，定位根本原因并给出修复方案。输入为 [代码片段/日志/复现步骤/环境信息]；目标是快速定位并修复缺陷。工作范围包括：复现问题、定位根因、评估影响面、给出修复代码与验证步骤。交付物为 Markdown 报告，含问题描述、根因分析、修复方案、影响范围与验证建议。验收要点：根因可复现、修复方案最小化、回归风险可控。`, `请审查以下代码变更，指出潜在问题、性能风险与改进建议。输入为 [变更 diff/PR 描述/上下文/编码规范]；目标是提升代码质量与可维护性。工作范围包括：检查正确性、可读性、安全、性能与可测试性，按严重程度排序问题。交付物为 Markdown 审查意见，含总体评估、逐条问题（位置/等级/描述/建议）和优先修复清单。验收要点：问题具体可定位、建议可执行、优先级合理。`, `请重构以下代码，提升可读性、可维护性与性能。输入为 [目标代码/重构范围/约束条件/测试覆盖]；目标是在保持行为一致的前提下改善代码结构。工作范围包括：识别坏味道、拆分职责、提取抽象、优化算法与命名，并说明主要改动与原因。交付物为重构后的代码与 Markdown 改动说明，含改动点、风险与验证建议。验收要点：功能等价、测试通过、代码更易维护。`, `请为以下代码编写单元测试，覆盖主要分支与边界情况。输入为 [待测代码/语言框架/测试策略/覆盖率目标]；目标是建立可运行的测试用例集。工作范围包括：识别被测单元、设计正常/异常/边界用例、使用合适断言与 Mock、说明运行命令。交付物为测试代码文件与 Markdown 测试说明，含测试范围、用例表、运行方式和覆盖率说明。验收要点：用例覆盖核心逻辑、断言有效、Mock 合理、可独立运行。`, `请基于以下接口或模块生成 API 文档。输入为 [代码定义/接口清单/协议类型/使用场景]；目标是让调用方能快速理解与集成。工作范围包括：梳理端点/方法、描述参数与返回值、给出请求/响应示例、标注错误码与鉴权。交付物为 Markdown 格式 API 文档，含简介、认证、端点列表、示例和变更日志占位。验收要点：参数可枚举、示例可运行、错误场景完整。`},
		PromptsEn: []string{`Break down the following requirements into clear development tasks. Input: [business requirements/user stories/PRD link/constraints]. Goal: produce a schedulable, verifiable development plan. Scope: identify stakeholders, split functional and non-functional items, define acceptance criteria, and map dependencies and priorities. Deliverable: a Markdown document with a requirement summary, task list with priority/estimate/dependency/acceptance, and risk notes. Acceptance: appropriately sized tasks, clear acceptance criteria, and no missing dependencies.`, `Analyze the following code or error message, identify the root cause, and provide a fix. Input: [code snippet/logs/reproduction steps/environment]. Goal: quickly locate and fix the defect. Scope: reproduce the issue, identify the root cause, assess impact, provide fixed code, and outline verification steps. Deliverable: a Markdown report with problem description, root-cause analysis, fix, impact scope, and verification advice. Acceptance: reproducible root cause, minimal fix, and manageable regression risk.`, `Review the following code changes for potential issues, performance risks, and improvement suggestions. Input: [diff/PR description/context/coding standards]. Goal: improve code quality and maintainability. Scope: check correctness, readability, security, performance, and testability; rank issues by severity. Deliverable: a Markdown review with overall assessment, per-issue entries with location/severity/description/suggestion, and a prioritized fix list. Acceptance: issues are locatable, suggestions are actionable, and priorities are reasonable.`, `Refactor the following code to improve readability, maintainability, and performance. Input: [target code/refactor scope/constraints/test coverage]. Goal: improve structure while preserving behavior. Scope: identify code smells, split responsibilities, extract abstractions, optimize algorithms and naming, and explain major changes. Deliverable: refactored code and a Markdown change note covering changes, risks, and verification. Acceptance: functional equivalence, tests pass, and improved maintainability.`, `Write unit tests for the following code, covering main branches and edge cases. Input: [code under test/language/framework/test strategy/coverage target]. Goal: build a runnable test suite. Scope: identify units, design normal/error/boundary cases, use appropriate assertions and mocks, and document run commands. Deliverable: test code files and a Markdown test note with scope, case table, run instructions, and coverage notes. Acceptance: core logic is covered, assertions are meaningful, mocks are appropriate, and tests run independently.`, `Generate API documentation for the following interface or module. Input: [code definitions/endpoint list/protocol/usage scenarios]. Goal: help consumers understand and integrate quickly. Scope: list endpoints/methods, describe parameters and return values, provide request/response examples, and note error codes and auth. Deliverable: Markdown API docs with overview, authentication, endpoint list, examples, and a changelog placeholder. Acceptance: parameters are enumerable, examples are runnable, and error scenarios are complete.`},
	},
	"design": {
		SubZh:     "内容创意与视觉方案",
		SubEn:     "Content & visual creation",
		DescZh:    "将演示、原型、视觉与文案构思交给 AI 智能体协助完成，获得高质量的创意灵感与设计方案。",
		DescEn:    "Delegate presentations, prototypes, visuals, and copywriting to the agent for high-quality creative ideas and design concepts.",
		QuickZh:   []string{`演示大纲`, `界面原型`, `品牌文案`, `视觉规范`, `社媒内容`, `视频脚本`},
		QuickEn:   []string{`Slide outline`, `UI prototype`, `Brand copy`, `Visual guide`, `Social content`, `Video script`},
		TagZh:     []string{`演示`, `原型`, `文案`, `设计`, `内容`, `脚本`},
		TagEn:     []string{`Slides`, `Prototype`, `Copy`, `Design`, `Content`, `Script`},
		PromptsZh: []string{`请为 [主题/产品/受众] 设计一份演示文稿大纲。输入包括演讲目标、核心信息、时长限制与参考资料；目标是输出可直接进入制作的演示结构。工作范围包括：确定核心观点、设计章节与过渡、分配每页要点、建议视觉风格。交付物为 Markdown 大纲，含封面、章节结构、每页标题/要点/视觉建议和时间分配。验收要点：逻辑递进、重点突出、时长可控。`, `请为 [应用/页面/功能] 绘制低保真原型方案。输入包括用户目标、信息架构、核心流程与约束；目标是输出可评审的界面结构与交互说明。工作范围包括：梳理页面层级、定义关键界面元素、描述主流程与异常分支、给出布局建议。交付物为 Markdown 文档，含用户目标、页面结构、流程图/线框图描述、组件清单和交互说明。验收要点：信息架构清晰、主流程完整、异常分支可说明。`, `请为 [品牌/产品/活动] 撰写品牌文案。输入包括目标受众、品牌调性、核心卖点与传播渠道；目标是输出一套一致的品牌表达。工作范围包括：提炼品牌定位、撰写价值主张、创作主标语与辅助文案、适配 1-2 个渠道。交付物为 Markdown 文案套件，含定位说明、主标语、副文案、渠道版本和语气说明。验收要点：定位清晰、语言有辨识度、适配目标渠道。`, `请为 [产品/活动/品牌] 制定视觉规范。输入包括品牌调性、应用场景、技术约束与参考案例；目标是输出可直接指导设计的规范文档。工作范围包括：定义色彩系统、字体层级、图标/图形语言、间距与布局规则。交付物为 Markdown 视觉规范，含设计原则、色彩表、字体样例、组件样式和禁用示例。验收要点：规则可量化、示例具体、应用场景覆盖完整。`, `请为 [渠道/活动/产品] 创作社媒内容。输入包括目标受众、发布平台、传播目标与关键信息；目标是输出可直接发布的帖子组合。工作范围包括：撰写主标题与正文、设计话题标签、提供配图/视频建议和互动引导。交付物为 Markdown 内容套件，含版本 A/B、话题标签、发布建议和合规提示。验收要点：符合平台调性、信息完整、行动号召明确。`, `请为 [主题/产品/受众] 编写视频脚本。输入包括视频时长、平台、风格定位与核心信息；目标是输出可用于拍摄/制作的脚本。工作范围包括：拆分场景、撰写旁白/对白、标注画面与音效/字幕、控制时长。交付物为 Markdown 脚本表格，含场景序号、时间轴、画面描述、旁白/对白、音效和字幕备注。验收要点：时长符合要求、信息传递清晰、画面与声音可执行。`},
		PromptsEn: []string{`Design a slide deck outline for [topic/product/audience]. Input: presentation goal, core message, time limit, and reference materials. Goal: produce a ready-to-build presentation structure. Scope: define core message, design sections and transitions, allocate key points per slide, and suggest visual style. Deliverable: a Markdown outline with title slide, section structure, per-slide titles/bullet points/visual suggestions, and time allocation. Acceptance: logical flow, clear emphasis, and time budget respected.`, `Create a low-fidelity prototype plan for [app/page/feature]. Input: user goals, information architecture, core flows, and constraints. Goal: produce a reviewable structure and interaction spec. Scope: map page hierarchy, define key UI elements, describe main and exceptional flows, and provide layout suggestions. Deliverable: a Markdown document with user goals, page structure, flow/wireframe descriptions, component list, and interaction notes. Acceptance: clear IA, complete main flow, and documented edge cases.`, `Write brand copy for [brand/product/campaign]. Input: target audience, brand tone, key selling points, and channels. Goal: produce a consistent set of brand expressions. Scope: distill positioning, craft value proposition, create tagline and supporting copy, and adapt for one or two channels. Deliverable: a Markdown copy kit with positioning, tagline, body copy, channel variants, and tone notes. Acceptance: clear positioning, distinctive language, and channel-appropriate adaptation.`, `Define a visual guide for [product/campaign/brand]. Input: brand tone, usage scenarios, technical constraints, and references. Goal: produce a design-ready specification. Scope: define color system, typography hierarchy, icon/graphic language, and spacing/layout rules. Deliverable: a Markdown visual guide with design principles, color swatches, type samples, component styles, and do/don't examples. Acceptance: quantifiable rules, concrete examples, and complete scenario coverage.`, `Create social media content for [channel/campaign/product]. Input: target audience, platform, communication goal, and key messages. Goal: produce ready-to-publish post variants. Scope: write headline and body, design hashtags, suggest images/video, and include engagement prompts. Deliverable: a Markdown content kit with A/B variants, hashtags, posting recommendations, and compliance tips. Acceptance: matches platform tone, complete information, and clear call to action.`, `Write a video script for [topic/product/audience]. Input: duration, platform, style, and core message. Goal: produce a production-ready script. Scope: break down scenes, write narration/dialogue, describe visuals, and note sound/subtitle cues while keeping to time. Deliverable: a Markdown script table with scene number, timeline, visual description, narration/dialogue, sound, and subtitle notes. Acceptance: fits duration, clear message delivery, and actionable visual/audio directions.`},
	},
}

func homePresetByID(id string) homePreset {
	if preset, ok := homePresets[id]; ok {
		return preset
	}
	return homePresets["working"]
}

func (p homePreset) sub(locale string) string {
	if locale == "en" {
		return p.SubEn
	}
	return p.SubZh
}

func (p homePreset) desc(locale string) string {
	if locale == "en" {
		return p.DescEn
	}
	return p.DescZh
}

func (p homePreset) quick(locale string) []string {
	if locale == "en" {
		return p.QuickEn
	}
	return p.QuickZh
}

func (p homePreset) tags(locale string) []string {
	if locale == "en" {
		return p.TagEn
	}
	return p.TagZh
}

func (p homePreset) prompts(locale string) []string {
	if locale == "en" {
		return p.PromptsEn
	}
	return p.PromptsZh
}

func executionProtocol(locale string) string {
	if locale == "en" {
		return homeExecutionProtocolEn
	}
	return homeExecutionProtocolZh
}
