# 配置详解

## 配置文件位置

MothX 使用两个配置文件:

| 文件 | 平台 | 范围 | 优先级 |
|------|------|------|--------|
| `~/.mothx/settings.json` | Linux/macOS | 全局 (所有项目) | 低 |
| `%APPDATA%\mothx\settings.json` | Windows | 全局 (所有项目) | 低 |
| `.mothx/settings.json` | 全部 | 项目级 | 高 |

> **提示:** 可以通过 `MOTHX_DIR` 覆盖全局配置目录。

> **Windows 用户：** `%APPDATA%` 实际展开为 `C:\Users\<用户名>\AppData\Roaming`，所以完整路径通常是 `C:\Users\<用户名>\AppData\Roaming\mothx\settings.json`。

项目级配置会覆盖全局配置。`providers` 先按 provider ID 合并，再按项目文件中显式出现的 provider 字段覆盖；若显式提供 `models`，该数组会替换继承的模型列表。

### 旧目录

当前版本使用 `.mothx` 作为项目目录、`~/.mothx`（Windows 为 `%APPDATA%\mothx`）作为全局目录。旧版 `.vibe`、`.vibecoding` 目录既不会读取，也不会迁移；如需继续使用旧配置，请手动复制到对应的 `.mothx` 目录，并检查其中的路径与字段。


### `tuilang`（TUI 界面语言）

顶层 `tuilang` 支持 `"auto"`（默认）、`"zh"` 和 `"en"`。`zh`、`en` 强制使用对应语言；`auto` 在 TUI 启动时读取当前 UTC 偏移，恰好 UTC+08:00 使用中文，其他偏移使用英文。项目 `.mothx/settings.json` 中的值会覆盖全局设置。TUI 的 `/settings` 菜单支持切换语言、选择全局或项目保存范围，并在保存成功后立即生效；slash 命令语法始终保持英文。
## 配置结构

### 完整示例

```json
{
  "providers": {
    "deepseek-anthropic": {
      "baseUrl": "https://api.deepseek.com/anthropic",
      "apiKey": "${DEEPSEEK_API_KEY}",
      "api": "anthropic-messages",
      "thinkingFormat": "deepseek",
      "cacheControl": false,
      "models": [
        {
          "id": "deepseek-v4-flash",
          "name": "DeepSeek-V4-Flash",
          "contextWindow": 1000000,
          "maxTokens": 384000,
          "cost": { "input": 0.5, "output": 2.0 }
        },
        {
          "id": "deepseek-v4-pro",
          "name": "DeepSeek-V4-Pro",
          "reasoning": true,
          "contextWindow": 1000000,
          "maxTokens": 384000,
          "cost": { "input": 1, "output": 4 }
        }
      ]
    },
    "deepseek-openai": {
      "baseUrl": "https://api.deepseek.com",
      "apiKey": "${DEEPSEEK_API_KEY}",
      "api": "openai-chat",
      "models": [
        {
          "id": "deepseek-v4-flash",
          "name": "DeepSeek-V4-Flash",
          "contextWindow": 1000000,
          "maxTokens": 384000,
          "cost": { "input": 0.5, "output": 2.0 }
        },
        {
          "id": "deepseek-v4-pro",
          "name": "DeepSeek-V4-Pro",
          "reasoning": true,
          "contextWindow": 1000000,
          "maxTokens": 384000,
          "cost": { "input": 1, "output": 4 }
        }
      ]
    }
  },
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash",
  "defaultMode": "yolo",
  "defaultThinkingLevel": "medium",
  "toolExecution": {
    "mode": "parallel",
    "maxConcurrency": 10
  },
  "statusLine": {
    "enabled": false,
    "type": "command",
    "command": "ccstatusline",
    "padding": 0,
    "refreshInterval": 10,
    "timeoutMs": 800,
    "fallback": "builtin"
  },
  "enablePlanTool": true,
  "enableArtifact": false,
  "enableACPArtifact": false,
  "maxContextTokens": 1000000,
  "contextFiles": {
    "enabled": true,
    "extraFiles": ["/path/to/extra-context.md"]
  },
  "skillsDir": "~/.mothx/skills",
  "compaction": {
    "enabled": true,
    "reserveTokens": 16384,
    "keepRecentTokens": 20000,
    "tokenizer": "auto",
    "template": "default"
  },
  "sandbox": {
    "enabled": false,
    "level": "none",
    "bwrapPath": "",
    "allowNetwork": false,
    "allowedRead": ["/usr", "/lib", "/lib64", "/bin", "/sbin"],
    "allowedWrite": [],
    "deniedPaths": ["/etc/shadow", "/root", "/home"],
    "passEnv": ["PATH", "HOME", "USER", "LANG", "TERM", "SHELL"],
    "tmpSize": "100m"
  },
  "sessionDir": "~/.mothx/sessions",
  "shellPath": "/bin/bash",
  "shellCommandPrefix": "",
  "theme": "dark",
  "tuilang": "auto",
  "retry": {
    "enabled": true,
    "maxRetries": 5,
    "baseDelayMs": 3000
  },
  "approval": {
    "bashWhitelist": ["go ", "make ", "git ", "npm ", "yarn ", "node ", "python ", "pip "],
    "bashBlacklist": ["rm -rf", "sudo"],
    "confirmBeforeWrite": true
  }
}
```

## 所有配置字段

### 顶层字段速查表

| 字段 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `providers` | object | *(见下文)* | 提供商配置 (以名称为 key) |
| `defaultProvider` | string | `"deepseek-openai"` | 默认使用的提供商 |
| `defaultModel` | string | `"deepseek-v4-flash"` | 默认使用的模型 ID |
| `defaultMode` | string | `"yolo"` | 默认运行模式: `plan`、`agent`、`yolo` 或 `os` |
| `defaultThinkingLevel` | string | `"medium"` | 默认思考级别 |
| `toolExecution` | object | *(见下文)* | 本地 function/custom tool 执行模式与每批并发上限 |
| `statusLine` | object | *(见下文)* | 仅 TUI 生效的外部状态行命令设置 |
| `enablePlanTool` | bool | `true` | 是否注册内置 `plan` 工具 |
| `enableArtifact` | bool | `false` | 是否允许 TUI/CLI 会话发布生成制品 |
| `enableACPArtifact` | bool | `false` | 是否允许 Desktop/ACP 会话发布生成制品 |
| `maxContextTokens` | int | `0` (自动) | 覆盖最大上下文 token 数 |
| `contextFiles` | object | *(见下文)* | 上下文文件加载设置 |
| `skillsDir` | string | `"~/.mothx/skills"` | 全局技能目录路径 |
| `compaction` | object | *(见下文)* | 上下文压缩设置 |
| `sandbox` | object | *(见下文)* | 沙箱执行设置 |
| `sessionDir` | string | `"~/.mothx/sessions"` | SQLite 会话存储目录 |
| `shellPath` | string | `""` (自动) | 自定义 Bash 工具的 shell 路径 |
| `shellCommandPrefix` | string | `""` | 每条 shell 命令前自动追加的前缀 |
| `theme` | string | `"dark"` | UI 主题: `"dark"` 或 `"light"` |
| `tuilang` | string | `"auto"` | TUI 语言: `"auto"`、`"zh"` 或 `"en"` |
| `retry` | object | *(见下文)* | API 调用重试设置 |
| `approval` | object | *(见下文)* | Bash 命令审批设置 |
| `webSearch` | object | *(见下文)* | Hosted web search 设置 |
| `updateCheck` | bool | `true` | 启用 npm 版本更新检测通知 |

---

## 配置项详解

### statusLine

TUI 专用状态行命令配置。启用后，MothX 会在交互式 TUI 中把当前状态 JSON 通过 stdin 传给外部命令，并用该命令的 stdout 替换输入框下方的默认 footer。

未启用、未配置、首次执行中、超时、失败或输出为空时，TUI 会继续显示当前默认 footer。

其他模式不会读取或执行这个配置，包括 Serve、Channels、ACP、print mode。

| 字段 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `enabled` | bool | `false` | 是否启用外部状态行 |
| `type` | string | `"command"` | 当前只支持 `"command"` |
| `command` | string | `""` | 要执行的状态行命令，例如 `ccstatusline` |
| `padding` | int | `0` | 在状态行输出后追加的空行数 |
| `refreshInterval` | int | `0` | 可选周期刷新秒数；`0` 表示仅事件触发 |
| `timeoutMs` | int | `800` | 单次命令超时时间（毫秒） |
| `fallback` | string | `"builtin"` | 失败时的回退策略；当前建议保持 `"builtin"` |

最小示例：

```json
{
  "statusLine": {
    "enabled": true,
    "type": "command",
    "command": "ccstatusline",
    "refreshInterval": 10,
    "timeoutMs": 800
  }
}
```

说明：

- 当前实现只支持 `stdin snapshot` 模式。
- 不输出 `transcript_path`。
- 不生成任何给 `ccstatusline` 读取的 transcript JSONL 文件。
- 因此依赖 transcript 历史扫描的 `ccstatusline` widgets 可能为空或精度受限。

### providers

多提供商配置。每个提供商是一个以用户自定义名称为 key 的对象:

| 字段 | 类型 | 必填 | 默认值 | 描述 |
|------|------|------|--------|------|
| `baseUrl` | string | ✓ | — | API 基础 URL |
| `vendor` | string | — | 自动检测 | 可选厂商适配器名称 (见下文) |
| `apiKey` | string | — | `""` | API 密钥 (见[认证配置](#认证配置)) |
| `api` | string | — | 自动检测 | API 协议: `"openai-chat"`、`"openai-responses"`、`"anthropic-messages"`、`"google-gemini"` 或 `"google-vertex"` |
| `httpProxy` | string | — | `""` | 可选的 provider 级 HTTP 代理 URL，例如 `"http://127.0.0.1:7890"` |
| `forceHTTP11` | bool | — | `false` | 为该 provider 强制使用 HTTP/1.1，即禁用 provider HTTP client 的 HTTP/2 |
| `headers` | object | — | `{}` | 可选自定义 HTTP 请求头，会附加到每次 provider 请求；值支持与 `apiKey` 相同的 `${ENV}` 和 `!cmd` 解析 |
| `thinkingFormat` | string | — | 自动检测 | 思考参数格式 (见下文) |
| `cacheControl` | bool | — | `false` | 启用 Anthropic 提示缓存；使用 Claude 模型时设为 `true` |
| `models` | array | — | `[]` | 可用模型列表 |

#### vendor 字段

`vendor` 字段用于选择厂商适配器，不改变现有 provider 配置 schema。该字段可选；未设置时，MothX 会先根据 `baseUrl` 自动识别厂商，再根据 `api` 回退到通用协议 provider。

选择顺序：

1. 显式 `vendor`
2. `baseUrl` 自动识别
3. 通用 fallback：`openai-chat`、`openai-responses`、`anthropic-messages`、`google-gemini` 或 `google-vertex`

内置厂商适配器包括 `openai`、`anthropic`、`claude`、`deepseek`、`google-gemini`、`google-vertex`、`xiaomi`、`xiaomi-token-plan-ams`、`xiaomi-token-plan-cn`、`xiaomi-token-plan-sgp`、`volcengine`、`kimi`、`minimax`、`qianfan`、`bailian`、`gitee`、`openrouter`、`together`、`groq`、`fireworks`、`mistral`、`github-copilot`、`cloudflare-ai-gateway`、`cloudflare-workers-ai` 和 `amazon-bedrock`。

```json
{
  "providers": {
    "custom-deepseek": {
      "vendor": "deepseek",
      "baseUrl": "https://api.deepseek.com",
      "apiKey": "${DEEPSEEK_API_KEY}",
      "api": "openai-chat",
      "headers": {
        "X-Request-Source": "mothx",
        "X-Gateway-Token": "${MY_GATEWAY_TOKEN}"
      },
      "models": [
        { "id": "deepseek-v4-flash", "name": "DeepSeek-V4-Flash", "contextWindow": 1000000 }
      ]
    }
  }
}
```

#### 火山引擎 / 豆包示例

内置 `volcengine` provider 使用方舟 OpenAI 兼容端点，并支持豆包 Seed 系列模型。也可以通过 `ark.cn-beijing.volces.com` 自动识别。

```json
{
  "providers": {
    "volcengine": {
      "vendor": "volcengine",
      "baseUrl": "https://ark.cn-beijing.volces.com/api/v3",
      "apiKey": "${VOLCENGINE_API_KEY}",
      "api": "openai-chat",
      "models": [
        { "id": "doubao-seed-2-1-turbo-260628", "name": "Doubao Seed 2.1 Turbo", "contextWindow": 262144, "maxTokens": 262144, "input": ["text"] },
        { "id": "doubao-seed-evolving", "name": "Doubao Seed Evolving", "contextWindow": 262144, "maxTokens": 262144, "input": ["text", "image"] },
        { "id": "doubao-seed-2-1-pro-260628", "name": "Doubao Seed 2.1 Pro", "contextWindow": 262144, "maxTokens": 262144, "input": ["text", "image"] }
      ]
    }
  }
}
```

### webSearch

Hosted web search 设置。默认关闭。

| 字段 | 类型 | 必填 | 默认值 | 描述 |
|------|------|------|--------|------|
| `enabled` | bool | — | `false` | 启用 hosted web search 注册 |
| `provider` | string | — | `defaultProvider` | 用于 web search 的 provider 配置名称 |
| `providerType` | string | — | 自动 | Hosted tool 类型，通常是 `responses` 或 `messages` |
| `model` | string | — | `""` | 可选 metadata，用于路由、展示或未来 provider-specific 处理 |

```json
{
  "webSearch": {
    "enabled": true,
    "provider": "gpt",
    "providerType": "responses",
    "model": "gpt-5.4"
  }
}
```

当 `provider` 指向一个已配置的 provider 名称时，MothX 会先解析该 provider 的 `baseUrl`、`api` 和 vendor 行为，再注册 hosted search tool。OpenAI Responses provider 还会自动暴露原生 Responses `web_search` 工具；该原生能力不依赖 `webSearch.enabled`。

#### api 字段

`api` 字段指定的是**协议格式**，而非服务商。你可以将任意提供商指向任意兼容的端点：

- `openai-chat`: OpenAI Chat Completions API 格式
- `openai-responses`: OpenAI Responses API 格式 (`POST /v1/responses`)
- `anthropic-messages`: Anthropic Messages API 格式
- `google-gemini`: 原生 Gemini API `streamGenerateContent` 格式
- `google-vertex`: 原生 Vertex AI Gemini `streamGenerateContent` 格式

例如，DeepSeek 在不同端点提供两种格式，你也可以用这些格式去连接真正的 OpenAI 或 Anthropic 服务。

如果未指定，会根据 `baseUrl` 自动检测：
- 包含 `generativelanguage.googleapis.com` → `google-gemini`
- 包含 `aiplatform.googleapis.com` → `google-vertex`
- 包含 "anthropic" → `anthropic-messages`
- 其他 → `openai-chat`

#### responses 字段

当 `api` 为 `"openai-responses"` 时，使用 `responses` 配置高级行为。所有字段均为可选；模型兼容标志会在请求发送到上游前拒绝不受支持的字段。

```json
{
  "providers": {
    "openai": {
      "baseUrl": "https://api.openai.com/v1",
      "apiKey": "${OPENAI_API_KEY}",
      "api": "openai-responses",
      "responses": {
        "stateMode": "previous_response_id",
        "reasoningSummary": "auto",
        "reasoningContext": "all_turns",
        "promptCacheEnabled": true,
        "promptCacheMode": "implicit"
      }
    }
  }
}
```

| 字段 | 值 / 类型 | 描述 |
|------|-----------|------|
| `stateMode` | `replay`、`previous_response_id`、`conversation` | 会话状态策略。`replay` 使用本地历史回放；`previous_response_id` 延续 provider response lineage；`conversation` 需要同时设置 `conversation`。 |
| `store` | bool | 在支持时请求上游存储 response。 |
| `conversation` | string | Provider conversation ID；使用 `stateMode: "conversation"` 时必填。 |
| `truncation` | `auto`、`disabled` | 上游 Responses 截断行为。 |
| `background` | bool | 在 Serve 中将支持的 Responses 请求作为可持久恢复的远程后台任务运行。MothX 会轮询任务、在重启后恢复可恢复任务，并处理本地工具。 |
| `reasoningSummary` | `auto`、`concise`、`detailed`、`none`、`off` | 在支持时请求 reasoning summary。 |
| `reasoningContext` | `auto`、`current_turn`、`all_turns` | 控制轮次间传递的 reasoning context。 |
| `reasoningMode` | `standard`、`pro` | Provider 特定的 reasoning mode。 |
| `promptCacheEnabled` | bool | Responses provider 默认启用；设置为 `false` 可禁用 prompt cache 字段。 |
| `promptCacheKey`、`promptCacheRetention`、`promptCacheMode`、`promptCacheTTL` | string | Prompt cache 的 key，以及 provider 特定的 retention/mode/TTL 控制。 |
| `safetyIdentifier` | string | Provider safety identifier。 |
| `metadata` | object | 非敏感 provider metadata（最多 16 项）。 |
| `include` | string 数组 | 要附加返回的受支持 Response item 字段。 |
| `serviceTier` | string | Provider service tier。 |
| `structuredOutput` | object | JSON Schema 结构化输出：`name`、`description`、`strict` 和 `schema`。 |
| `toolControl` | object | 工具选择和执行控制：`choice`、`parallel`、`maxCalls`。 |
| `hostedTools` | object | Provider 托管工具选项：`webSearch`、`fileSearch`、`codeInterpreter`、`imageGeneration` 和 `remoteMCP`。 |

`background` 仅可通过 `mothx serve` 使用；普通 CLI/TUI/ACP 请求仍使用流式 agent loop。启用后台运行的 provider 必须同时支持所选模型和 Responses 功能。

`toolControl.choice` 决定发送给 OpenAI Responses provider 的 `tool_choice`：`auto`、`none`、`required`，或填写工具名以强制调用该工具。WebUI 在“设置 > Providers”中提供 Tool Choice、并行工具调用和最大工具调用数三个字段，TUI 在 `/settings` > provider > B. 协议 > Responses 中提供同名字段。TUI 编辑器写入全局 settings 文件；项目级覆盖仍可通过 `.mothx/settings.json` 配置。

Google 原生 provider 可以直接配置：

```json
{
  "providers": {
    "google-gemini": {
      "baseUrl": "https://generativelanguage.googleapis.com/v1beta/models",
      "apiKey": "${GOOGLE_API_KEY}",
      "api": "google-gemini",
      "models": [
        { "id": "gemini-2.5-flash", "name": "Gemini 2.5 Flash", "reasoning": true, "contextWindow": 1000000, "maxTokens": 65536 }
      ]
    },
    "google-vertex": {
      "baseUrl": "https://aiplatform.googleapis.com/v1/publishers/google/models",
      "apiKey": "${GOOGLE_CLOUD_API_KEY}",
      "api": "google-vertex",
      "models": [
        { "id": "gemini-2.5-flash", "name": "Gemini 2.5 Flash", "reasoning": true, "contextWindow": 1000000, "maxTokens": 65536 }
      ]
    }
  }
}
```

对 `google-vertex`，`${GOOGLE_CLOUD_API_KEY}` 会走 Vertex AI API key 路径，不需要 `gcloud`，URL 里也不需要 project/location。OAuth access token 仍然支持：使用 `https://aiplatform.googleapis.com/v1/projects/YOUR_PROJECT/locations/global/publishers/google/models` 这样的 project-scoped URL，并把 token 配成 `!gcloud auth print-access-token`。使用 `!command` 值前需要设置 `VIBECODING_ALLOW_SHELL_CONFIG=1`。

#### thinkingFormat 字段

指定思考/推理参数如何发送到 API：

| 值 | 行为 |
|----|------|
| `""` (空) | 根据 URL 自动检测 |
| `"openai"` | 使用 OpenAI `reasoning_effort` 格式 |
| `"anthropic"` | 使用 Anthropic `thinking` 带 `budget_tokens` |
| `"deepseek"` | 使用 DeepSeek `thinking: {type: "enabled"}` + `reasoning_effort` (OpenAI) 或 `output_config.effort` (Anthropic) |
| `"xiaomi"` | 旧的 thinking-only 格式: `thinking: {type: "enabled"}` |
| `"doubao-seed"` | 豆包 Seed 2.1 / Evolving `reasoning_effort`: `minimal`（不思考）、`low`、`medium`、`high`（默认） |

未设置时自动检测：
- URL 包含 `deepseek` → `"deepseek"`
- URL 包含 `xiaomimimo` → `"xiaomi"`
- 模型 ID 匹配 `doubao-seed-2.1*` 或 `doubao-seed-evolving` → `"doubao-seed"`

```json
{
  "providers": {
    "deepseek-openai": {
      "baseUrl": "https://api.deepseek.com",
      "apiKey": "sk-...",
      "api": "openai-chat",
      "thinkingFormat": "deepseek"
    }
  }
}
```

#### cacheControl 字段

启用 Anthropic 风格的提示缓存 (Prompt Caching)。设为 `true` 时，MothX 会在请求中添加缓存控制头。**使用 Claude 模型接入 Anthropic API 时应启用此选项**，可降低费用和延迟。

```json
{
  "providers": {
    "anthropic": {
      "baseUrl": "https://api.anthropic.com",
      "apiKey": "${ANTHROPIC_API_KEY}",
      "api": "anthropic-messages",
      "cacheControl": true,
      "models": [
        {
          "id": "claude-sonnet-4-20250514",
          "name": "Claude Sonnet 4",
          "contextWindow": 200000,
          "maxTokens": 8192,
          "cost": {
            "input": 3,
            "output": 15,
            "cacheRead": 0.3,
            "cacheWrite": 3.75
          }
        }
      ]
    }
  }
}
```

#### models 数组

每个模型字段:

| 字段 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `id` | string | — | 发送到 API 的模型 ID |
| `name` | string | — | 人类可读的显示名称 |
| `reasoning` | bool | `false` | 是否支持思考/推理 |
| `contextWindow` | int | `0` | 上下文窗口大小 (token) |
| `maxTokens` | int | `0` | 每次响应的最大输出 token。显式设为 `0` 可在 OpenAI/Google 风格端点上禁用输出 token 限制（请求中省略该字段）；Anthropic 端点强制要求 `max_tokens`，显式 `0` 会回退到默认值 16384 |
| `input` | []string | `[]` | 支持的输入模态: `"text"`, `"image"` |
| `cost` | object | `null` | 每百万 token 定价 |
| `compat` | object | `null` | 模型级兼容标志，用于处理 provider 差异 |

`cost` 对象:

| 字段 | 类型 | 描述 |
|------|------|------|
| `input` | float | 每百万输入 token 费用 |
| `output` | float | 每百万输出 token 费用 |
| `cacheRead` | float | 每百万缓存读取 token 费用 (Anthropic) |
| `cacheWrite` | float | 每百万缓存写入 token 费用 (Anthropic) |

`compat` 对象可选，仅在某个模型需要协议兼容调整时设置：

| 字段 | 类型 | 描述 |
|------|------|------|
| `thinkingFormat` | string | 覆盖模型 thinking 格式（`openai`、`deepseek`、`xiaomi`、`anthropic` 等） |
| `requiresReasoningContentOnAssistant` | bool | 回放 assistant 消息时发送空 `reasoning_content` |
| `requiresReasoningContentOnAssistantMessages` | bool | 参考实现中的别名，与上一项等价 |
| `forceAdaptiveThinking` | bool | 强制使用 Anthropic adaptive thinking 格式 |
| `supportsReasoningEffort` | bool | 模型是否接受 `reasoning_effort` |
| `maxTokensField` | string | 使用 `max_tokens` 或 `max_completion_tokens` |
| `supportsDeveloperRole` | bool | 是否支持 developer role 消息 |
| `supportsStore` | bool | 是否支持 OpenAI `store` |
| `supportsStrictMode` | bool | 是否支持严格工具 schema |
| `supportsParallelToolCalls` | bool | 是否接受显式并行 tool call 控制；拒绝该控制的网关设置为 `false`。Anthropic Messages 此时会省略整个 `tool_choice` 对象，而不只是 `disable_parallel_tool_use` |
| `supportsToolChoice` | bool | 是否接受显式 tool-choice 控制；设为 `false` 时 Anthropic Messages 会省略 `tool_choice` |
| `supportsCacheControlOnTools` | bool | 是否支持在工具定义上使用 cache control |
| `supportsLongCacheRetention` | bool | 是否支持长 prompt cache retention |
| `sendSessionAffinityHeaders` | bool | 是否发送 session affinity headers |
| `supportsEagerToolInputStreaming` | bool | 是否支持 Anthropic eager tool input streaming |

`supportsToolChoice` 和 `supportsParallelToolCalls` 也可在 WebUI 的模型行以及 TUI 的 `/settings` > 模型 > Compatibility 中编辑；其余兼容标志仍需直接编辑配置文件。

```json
{
  "id": "deepseek-v4-flash",
  "name": "DeepSeek-V4-Flash",
  "contextWindow": 1000000,
  "maxTokens": 384000,
  "reasoning": false,
  "input": ["text"],
  "cost": {
    "input": 0.5,
    "output": 2.0
  }
}
```

---

### toolExecution

该设置只控制本地执行。`sequential` 只会限制响应到达后的 MothX 本地 worker，不会要求模型一次只生成一个 tool call；provider 仍可能在一次响应中返回多个 tool call。`parallel` 批次按模型声明的顺序上报每个调用的启动，该顺序不阻塞执行：调用之间仍然并发运行，各自独立等待审批与持久化执行记录，只有完成顺序不受限制。TUI、WebUI、Serve、channels、ACP 以及通过共享 Runtime 创建的子 Agent 都使用同一配置。它不控制 OpenAI Responses `web_search` 等 provider 托管工具内部的并发；托管工具仍由 provider 自己的编排和配额规则负责。

| 字段 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `mode` | string | `"parallel"` | `"parallel"` 使用有界本地 worker；`"sequential"` 在本地逐个执行已返回的调用（不影响 provider 侧并行出参） |
| `maxConcurrency` | int | `10` | 单个 Agent 的一批 tool call 同时执行上限；`1` 等价于串行；省略或非正数使用 `10` |

示例：

```json
{
  "toolExecution": {
    "mode": "parallel",
    "maxConcurrency": 10
  }
}
```

WebUI 在“设置 > Tools”中提供这两个字段，TUI 在 `/settings` > Behavior 中提供这两个字段。TUI 编辑器写入全局 settings 文件；项目级覆盖仍可通过 `.mothx/settings.json` 配置。已有运行不会中途改变上限；新建 Agent 时会读取最新配置。

### defaultProvider

默认使用的提供商名称。必须对应 `providers` 中的一个 key。

```json
{ "defaultProvider": "deepseek-openai" }
```

### defaultModel

默认使用的模型 ID。必须对应所选提供商 `models` 列表中的一个 `id`。

```json
{ "defaultModel": "deepseek-v4-flash" }
```

### defaultMode

默认运行模式:

| 值 | 描述 |
|----|------|
| `plan` | 只读分析模式 — 无文件写入，有沙箱 |
| `agent` | 标准读写模式 — Bash 需要审批 |
| `yolo` | 完全访问模式 — 所有工具自动执行 |
| `os` | 仅 Shell 模式 — 只提供 Bash，使用 YOLO 级审批行为且不启用沙箱；高风险保护仍然生效 |

```json
{ "defaultMode": "yolo" }
```

### defaultThinkingLevel

默认思考级别:

| 值 | 描述 |
|----|------|
| `off` | 关闭思考 |
| `minimal` | 最小思考 |
| `low` | 低级别 |
| `medium` | 中等级别 (默认) |
| `high` | 高级别 |
| `xhigh` | 最高级别 |

```json
{ "defaultThinkingLevel": "medium" }
```

### enablePlanTool

是否注册内置 `plan` 工具，允许 agent 创建和跟踪结构化任务计划。

```json
{ "enablePlanTool": true }
```

设为 `false` 可禁用（例如不希望 agent 使用结构化计划）。

### enableArtifact / enableACPArtifact

制品发布默认关闭，并分别为终端与 ACP/Desktop 会话配置；启用其中一个不会启用另一个。

```json
{
  "enableArtifact": false,
  "enableACPArtifact": false
}
```

### maxContextTokens

覆盖最大上下文 token 数。设为 `0` (默认) 时，根据模型的 `contextWindow` 自动确定。

```json
{ "maxContextTokens": 200000 }
```

### contextFiles

上下文文件加载设置。

| 字段 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `enabled` | bool | `true` | 是否自动加载上下文文件 |
| `extraFiles` | []string | `[]` | 额外的上下文文件路径 |

```json
{
  "contextFiles": {
    "enabled": true,
    "extraFiles": [
      "/path/to/extra-context.md",
      "~/.mothx/global-context.md"
    ]
  }
}
```

#### 自动加载的上下文文件

MothX 会自动搜索并加载以下文件:

1. **全局文件** (在全局配置目录中):
   - `AGENTS.md`
   - `CLAUDE.md`

2. **项目文件** (从当前目录向上搜索):
   - `AGENTS.md`
   - `CLAUDE.md`
   - `.mothx/AGENTS.md`
   - `.mothx/CLAUDE.md`
   - `.vibe/AGENTS.md`（旧版兼容）
   - `.vibe/CLAUDE.md`（旧版兼容）

---

### skillsDir

全局技能目录路径。支持 `~` 展开。

| 平台 | 默认值 |
|------|--------|
| Linux/macOS | `~/.mothx/skills` |
| Windows | `%APPDATA%\mothx\skills` |

```json
{ "skillsDir": "~/.mothx/skills" }
```

技能加载位置：
- **全局技能**: `<skillsDir>/<name>/SKILL.md`
- **项目技能**: `.skills/<name>/SKILL.md` (覆盖全局)

---

### compaction

上下文压缩配置，用于管理长对话。当估算请求达到模型上下文窗口约 80% 时，MothX 会自动总结较旧的消息以继续对话。

| 字段 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `enabled` | bool | `true` | 启用自动上下文压缩 |
| `reserveTokens` | int | `16384` | 兼容配置，同时用于限制压缩摘要输出上限 |
| `keepRecentTokens` | int | `20000` | 保留的最近消息 token 数 |
| `tokenizer` | string | `"auto"` | token 估算器选择器。目前 `auto` 和 `generic` 都使用内置 chars/4 估算器 |
| `tokenizerModel` | string | `""` | 预留的模型提示，用于后续模型专用 token 估算器 |
| `template` | string | `"default"` | 压缩摘要模板：`default`、`code` 或 `conversation` |

```json
{
  "compaction": {
    "enabled": true,
    "reserveTokens": 16384,
    "keepRecentTokens": 20000,
    "tokenizer": "auto",
    "template": "code"
  }
}
```

#### 压缩模板

- **`default`**: 通用结构化 checkpoint，包含目标、进度、决策、下一步和关键上下文。
- **`code`**: 面向编码任务，强调文件路径、代码变更、验证命令和技术决策。
- **`conversation`**: 面向非代码对话的简洁讨论 checkpoint。

---

### sandbox

沙箱执行配置。在 Linux 上使用 [bubblewrap (bwrap)](https://github.com/containers/bubblewrap)。

| 字段 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `enabled` | bool | `false` | 启用沙箱执行 |
| `level` | string | `"none"` | 沙箱级别: `"none"`, `"standard"`, `"strict"` |
| `bwrapPath` | string | `""` (自动) | 自定义 `bwrap` 二进制文件路径 |
| `allowNetwork` | bool | `false` | 沙箱内是否允许网络访问 |
| `allowedRead` | []string | *(平台默认)* | 沙箱内可读路径 |
| `allowedWrite` | []string | `[]` | 沙箱内额外可写路径 |
| `deniedPaths` | []string | *(平台默认)* | 沙箱内明确禁止访问的路径 |
| `passEnv` | []string | *(平台默认)* | 传入沙箱的环境变量 |
| `tmpSize` | string | `"100m"` | 沙箱 `/tmp` tmpfs 挂载的大小限制 |

```json
{
  "sandbox": {
    "enabled": true,
    "level": "standard",
    "bwrapPath": "/usr/bin/bwrap",
    "allowNetwork": false,
    "allowedRead": ["/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc/ssl"],
    "allowedWrite": ["/tmp/my-build"],
    "deniedPaths": ["/etc/shadow", "/root"],
    "passEnv": ["PATH", "HOME", "USER", "LANG", "TERM", "SHELL", "GOPATH"],
    "tmpSize": "200m"
  }
}
```

#### 沙箱级别

| 级别 | 文件系统 | 网络 | 用途 |
|------|---------|------|------|
| `none` | 完全访问 | ✓ | 无沙箱 (YOLO 模式默认) |
| `standard` | 项目可读写 | ✗ | 日常开发 (Agent 模式) |
| `strict` | 项目只读 | ✗ | 代码审查/分析 (Plan 模式) |

#### allowedRead 平台默认值

**Linux:**
```json
["/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc/ld.so.cache", "/etc/ssl", "/etc/ca-certificates", "/dev/null", "/dev/urandom", "/dev/zero", "/proc/self", "/proc/meminfo", "/proc/cpuinfo"]
```

**macOS:**
```json
["/usr", "/lib", "/bin", "/sbin", "/System", "/Library"]
```

**Windows:**
```json
["C:\\Windows", "C:\\Program Files", "C:\\Program Files (x86)"]
```

#### deniedPaths 平台默认值

**Linux / macOS:**
```json
["/etc/shadow", "/etc/gshadow", "/etc/passwd", "/root", "/home"]
```

**Windows:**
```json
["C:\\Users\\<用户名>\\Documents", "C:\\Users\\<用户名>\\Desktop"]
```

#### passEnv 平台默认值

**所有平台:** `PATH`, `HOME`, `USER`, `LANG`, `LC_ALL`, `TERM`

**Linux 额外:** `SHELL`, `GOPATH`, `GOROOT`, `GOPROXY`, `GOMODCACHE`, `NODE_PATH`

**macOS 额外:** `SHELL`, `TMPDIR`

**Windows 额外:** `APPDATA`, `LOCALAPPDATA`, `COMPUTERNAME`, `USERPROFILE`, `SYSTEMROOT`

---

### sessionDir

SQLite 会话存储目录。支持 `~` 展开。

MothX 会把所有会话元数据和条目统一存入单个 `sessions.db` 数据库文件中（CLI/TUI 模式下使用虚拟句柄，Serve 模式下则会按需创建物理句柄）。布局详见[会话管理](sessions.md)。

| 平台 | 默认值 |
|------|--------|
| Linux/macOS | `~/.mothx/sessions` |
| Windows | `%APPDATA%\mothx\sessions` |

```json
{ "sessionDir": "~/.mothx/sessions" }
```

---

### shellPath

自定义 Bash 工具使用的 shell 路径。为空 (默认) 时使用平台默认值：

| 平台 | 默认值 |
|------|--------|
| Linux | `$SHELL` 或 `/bin/bash` |
| macOS | `$SHELL` 或 `/bin/zsh` |
| Windows | `powershell.exe` 或 `cmd.exe` |

```json
{ "shellPath": "/usr/bin/fish" }
```

### shellCommandPrefix

每条 shell 命令执行前自动追加的前缀字符串。适用于设置环境或激活虚拟环境。

```json
{ "shellCommandPrefix": "source ~/.venv/bin/activate && " }
```

为空 (默认) 时直接执行命令。

---

### theme

终端界面的 UI 颜色主题。

| 值 | 描述 |
|----|------|
| `"dark"` | 深色背景主题 (默认) |
| `"light"` | 浅色背景主题 |

```json
{ "theme": "dark" }
```

---

### retry

API 调用重试配置，使用指数退避策略。重试适用于暂时性的初始 HTTP 失败，以及尚未输出任何可见内容时发生的早期 SSE 读流失败。一旦文本、思考、工具调用或 usage 已经流式输出给客户端，读流失败将不再重试，以避免重复输出。

| 字段 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `enabled` | bool | `true` | 遇到瞬态 API 错误时自动重试 |
| `maxRetries` | int | `5` | 最大重试次数 |
| `baseDelayMs` | int | `3000` | 基础延迟 (毫秒)，每次重试翻倍 |

```json
{
  "retry": {
    "enabled": true,
    "maxRetries": 5,
    "baseDelayMs": 3000
  }
}
```

#### 可重试的错误

以下错误会触发自动重试：

| 类别 | 示例 |
|------|------|
| 速率限制 | HTTP 429 |
| 服务器错误 | HTTP 502, 503, 504 |
| 网络错误 | 连接被拒绝、连接重置、DNS 错误、HTTP/2 `INTERNAL_ERROR` stream reset |
| 超时 | HTTP 客户端超时、TCP 超时 |

以下情况**不会**重试：
- 上下文取消（用户按了 Ctrl+C）
- HTTP 4xx 客户端错误（除 429 外）：400、401、403、404
- 已经输出可见内容之后发生的读流失败

#### 退避策略

每次重试等待 `baseDelayMs × 2^attempt` 毫秒，上限 30 秒：

| 次数 | 延迟 (base=2000ms) |
|------|--------------------|
| 第 1 次 | 2 秒 |
| 第 2 次 | 4 秒 |
| 第 3 次 | 8 秒 |

发生重试时，MothX 会在 TUI 中显示状态消息：
```
Retrying (1/3): request timed out — waiting 2.0s...
Retrying (2/3): rate limited (HTTP 429) — waiting 4.0s...
```

#### 禁用重试

```json
{
  "retry": {
    "enabled": false
  }
}
```

---

### approval

Agent 模式审批配置。控制哪些 Bash 命令自动执行，哪些需要用户确认。

| 字段 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `bashWhitelist` | []string | *(见下文)* | agent 模式下自动批准的命令前缀列表 |
| `bashBlacklist` | []string | `[]` | **始终**需要审批的命令前缀列表 |
| `confirmBeforeWrite` | bool | `true` | agent 模式下 `Write`/`Edit` 工具执行前需要用户确认 |

#### 默认白名单

```json
["go ", "make ", "git ", "npm ", "yarn ", "node ", "python ", "pip "]
```

#### 审批流程

```
Agent 请求执行工具
│
▼
检查模式
├─ Plan 模式 → 拒绝 (只读)
├─ Agent 模式 → 继续检查
└─ YOLO 模式 → 自动批准（除非命中黑名单）
│
▼
黑名单检查（最高优先级）：
├─ 命令匹配黑名单 → 需要用户审批
└─ 否则继续
│
▼
Agent 模式下：
├─ 匹配项目 allow.json → 自动批准（见下文）
├─ 命令匹配设置白名单 → 自动批准
├─ Write/Edit 工具 + confirmBeforeWrite=true → 需要用户审批
├─ 非 Bash 工具 → 自动批准
└─ 其他 → 需要用户审批
│
▼
在 --print 模式下：
  本应触发审批的命令 → 直接报错退出
```

#### 示例配置

**仅允许 git 和 npm:**
```json
{
  "approval": {
    "bashWhitelist": ["git ", "npm "]
  }
}
```

**自定义黑名单:**
```json
{
  "approval": {
    "bashWhitelist": ["go ", "make ", "git "],
    "bashBlacklist": ["rm -rf", "sudo", "dd "]
  }
}
```

**禁用写入确认 (信任 agent):**
```json
{
  "approval": {
    "confirmBeforeWrite": false
  }
}
```

### 项目级允许规则（`allow.json`）

除全局 `settings.json` 审批配置外，MothX 还支持项目级允许规则，保存在 `allow.json`（`.mothx/allow.json`）中。这些规则支持**按项目**自动批准特定 bash 命令，而无需修改全局设置。

| 文件 | 范围 | 优先级 |
|------|------|----------|
| `.mothx/allow.json` | 当前项目 | 高 |
| `~/.mothx/allow.json` | 全局回退 | 低 |

#### 字段

| 字段 | 类型 | 默认值 | 描述 |
|-------|------|---------|-------------|
| `autoEdit` | bool | `true` | agent 模式下自动批准 `write`/`edit` 工具。文件不存在时默认为 `true`，文件中显式设置 `"autoEdit": false` 则禁用。 |
| `editPaths` | []string | `[]` | 匹配 `write`/`edit` 自动批准路径的 glob 模式。支持 `**`（跨目录）和 `*`（单段）。仅项目级。 |
| `bashCommands` | []string | `[]` | 在 agent 模式下自动批准的精确 bash 命令字符串。仅项目级。 |
| `bashPrefixes` | []string | `[]` | 在 agent 模式下自动批准的 bash 命令前缀。尾部空格有效（例如 `"go test "` 匹配 `go test ./...`）。仅项目级。 |

#### 与设置的交互

- **黑名单优先级高于允许规则**：匹配 `settings.json` 中 `bashBlacklist` 的命令始终需要审批，即使匹配项目级 `bashCommands` 或 `bashPrefixes` 条目。
- **`autoEdit` 继承**：全局 `autoEdit` 会被继承，除非项目文件显式设置。编写不包含 `autoEdit` 的项目 `allow.json` **不会**持久化继承的全局值。

#### 示例 `.mothx/allow.json`

```json
{
  "autoEdit": true,
  "editPaths": ["docs/**", "*.md"],
  "bashCommands": ["make test", "make build"],
  "bashPrefixes": ["go test ", "go vet "]
}
```

#### TUI 管理

- 当 bash 命令等待审批时，审批对话框提供**「始终允许此命令」**和**「始终允许命令前缀」**选项。选择后将规则持久化到 `.mothx/allow.json`。
- 使用 `/alloweditpath add <glob>` 管理 `editPaths`。
- 使用 `/allowautoedit [on|off]` 切换 `autoEdit`。

---

## MCP 配置

MCP 服务器配置保存在独立的 `mcp.json` 文件中，不写入 `settings.json`。

MothX 启动时会从以下位置加载 MCP 配置：

1. 全局配置：Linux/macOS 为 `~/.mothx/mcp.json`，Windows 为 `%APPDATA%\mothx\mcp.json`
2. 项目配置：`.mothx/mcp.json`

Serve 的 Web UI 在**设置 → MCP**中提供全局文件编辑器，也可从当前会话聊天工具栏打开项目级编辑器。保存后的 MCP 服务器会应用到新建会话；已有会话会保留已注册的工具，直到重新创建会话。

```text
/init_mcp project full
/init_mcp global basic
/mcps
```

示例：

```json
{
  "mcpServers": [
    {
      "name": "local-tools",
      "type": "stdio",
      "command": "mcp-server",
      "args": ["--port", "8080"],
      "env": [
        {"name": "API_KEY", "value": "sk-..."}
      ]
    },
    {
      "name": "remote-tools",
      "type": "http",
      "url": "https://mcp.example.com",
      "headers": [
        {"name": "Authorization", "value": "Bearer token"}
      ]
    }
  ]
}
```

支持的传输类型：

- `stdio`：`command` 可以是通过 `PATH` 查找的命令名，也可以是直接路径；参数请放在 `args` 中
- `http`：通过 `url` 连接 streamable HTTP 端点
- `sse`：通过 `url` 连接 legacy SSE 流，并通过 `messageUrl` 发送请求

MCP 工具会在内置工具和 `skill_ref` 之后、agent 创建之前注册。agent 会冻结当前会话的 system prompt 和工具定义，因此修改 `mcp.json` 后需要重启客户端，或创建新的 Serve 会话。

工具名称采用 `mcp_<server_name>_<tool_name>`。如果名称冲突，MothX 会追加数字后缀，不会覆盖已有工具。自动启动加载会忽略 starter 模板里的占位项，例如 `/absolute/path/to/mcp-server`、`example.com` 和 `replace-me`。

---

## 认证配置

MothX 支持多种方式提供 API 密钥，解析逻辑灵活。

### 密钥解析顺序

MothX 需要某个提供商的 API 密钥时，按以下顺序查找：

1. **提供商 `apiKey` 字段** — 如果在 `settings.json` 中设置了，按下方规则解析
2. **派生的环境变量** — 将提供商名称转换为环境变量：例如 `deepseek-openai` → `DEEPSEEK_OPENAI_API_KEY`

### apiKey 字段格式

`apiKey` 字段支持三种格式：

| 格式 | 示例 | 行为 |
|------|------|------|
| `${VAR}` | `"${DEEPSEEK_API_KEY}"` | 读取环境变量 `VAR` 的值 |
| `!command` | `"!pass show deepseek-key"` | 仅当 `VIBECODING_ALLOW_SHELL_CONFIG=1` 时执行 shell 命令，并使用其标准输出 |
| 纯字符串 | `"sk-abc123..."` | 直接使用 (⚠️ 不建议用于共享配置) |

#### 环境变量引用

```json
{
  "providers": {
    "deepseek-openai": {
      "apiKey": "${DEEPSEEK_API_KEY}"
    }
  }
}
```

然后设置环境变量：

```bash
export DEEPSEEK_API_KEY=sk-...
```

#### Shell 命令 (密码管理器集成)

前缀加 `!` 可执行 shell 命令。MothX 在 Linux/macOS 上使用 `sh -c`，在 Windows 上使用 `powershell.exe`。

Shell 命令解析默认关闭。如需在可信本地配置中启用，设置：

```bash
export VIBECODING_ALLOW_SHELL_CONFIG=1
```

```json
{
  "providers": {
    "anthropic": {
      "apiKey": "!pass show api/anthropic"
    },
    "openai": {
      "apiKey": "!security find-generic-password -s openai-api -w"
    }
  }
}
```

适用于集成 `pass`、`1password-cli`、macOS 钥匙串或其他密钥管理工具。

#### 派生环境变量回退

如果某个提供商未配置 `apiKey`，MothX 会从提供商名称派生环境变量名：

| 提供商名称 | 派生的环境变量 |
|-----------|---------------|
| `deepseek-openai` | `DEEPSEEK_OPENAI_API_KEY` |
| `deepseek-anthropic` | `DEEPSEEK_ANTHROPIC_API_KEY` |
| `my-custom-provider` | `MY_CUSTOM_PROVIDER_API_KEY` |
| `anthropic` | `ANTHROPIC_API_KEY` |
| `openai` | `OPENAI_API_KEY` |

规则：`-` 替换为 `_`，全部大写，末尾追加 `_API_KEY`。

### 认证示例

**方式一：环境变量 (最简单)**

```bash
export DEEPSEEK_API_KEY=sk-...
```

使用默认配置时，MothX 会为 `deepseek-openai` 提供商查找 `DEEPSEEK_OPENAI_API_KEY`。但如果提供商的 `apiKey` 设置为 `${DEEPSEEK_API_KEY}`，则读取该环境变量。

**方式二：配置文件内嵌**

```json
{
  "providers": {
    "deepseek-openai": {
      "apiKey": "sk-..."
    }
  }
}
```

**方式三：密码管理器**

```json
{
  "providers": {
    "deepseek-openai": {
      "apiKey": "!pass show deepseek"
    }
  }
}
```

---

## 损坏配置自动恢复

启动时，如果全局或项目级 `settings.json` 无法解析，MothX 会尝试将其重命名为同一目录下带时间戳的备份，例如：

```text
settings.json.bak_20260715-143000
```

如果该名称已存在，会追加 `_1` 等数字序号。stderr 中的 Warning 会显示适配当前操作系统格式的绝对备份路径。

备份成功后，全局配置损坏时本次运行使用内建默认设置；项目配置损坏时忽略该文件并保留已加载的有效全局设置。默认的全局 `settings.json` 会在下一次启动时重新创建。如果无法创建备份（例如目录为只读），配置加载会返回错误。

---

## 环境变量覆盖

以下环境变量在运行时覆盖设置：

| 环境变量 | 覆盖的设置 | 示例 |
|---------|-----------|------|
| `MOTHX_DIR` | 全局配置目录 | `export MOTHX_DIR=/custom/config` |
| `VIBECODING_PROVIDER` | `defaultProvider` | `export VIBECODING_PROVIDER=anthropic` |
| `VIBECODING_MODEL` | `defaultModel` | `export VIBECODING_MODEL=claude-sonnet-4-20250514` |
| `VIBECODING_MODE` | `defaultMode` | `export VIBECODING_MODE=yolo` |
| `VIBECODING_THINKING` | `defaultThinkingLevel` | `export VIBECODING_THINKING=high` |
| `VIBECODING_DEBUG` | 启用 provider 级请求/响应调试输出 | `export VIBECODING_DEBUG=1` |
| `VIBECODING_PPROF_ADDR` | 设置 `--debug` 启动的 pprof 监听地址 | `export VIBECODING_PPROF_ADDR=127.0.0.1:6061` |
| `VIBECODING_NO_UPDATE_CHECK` | 禁用版本更新检测 | `export VIBECODING_NO_UPDATE_CHECK=1` |
| `VIBECODING_NPM_REGISTRY` | 覆盖 npm registry 地址 | `export VIBECODING_NPM_REGISTRY=https://registry.npmjs.org` |

---

## 配置示例

### 最小配置

只需设置默认提供商和模型，其余使用合理的默认值。

```json
{
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash"
}
```

### 多提供商配置

可在运行时通过 `/provider` 或 `--provider` 切换提供商：

```json
{
  "providers": {
    "deepseek-anthropic": {
      "vendor": "deepseek",
      "baseUrl": "https://api.deepseek.com/anthropic",
      "apiKey": "${DEEPSEEK_API_KEY}",
      "api": "anthropic-messages"
    },
    "deepseek-openai": {
      "vendor": "deepseek",
      "baseUrl": "https://api.deepseek.com",
      "apiKey": "${DEEPSEEK_API_KEY}",
      "api": "openai-chat"
    },
    "anthropic": {
      "vendor": "anthropic",
      "baseUrl": "https://api.anthropic.com",
      "apiKey": "${ANTHROPIC_API_KEY}",
      "api": "anthropic-messages",
      "cacheControl": true,
      "models": [
        {
          "id": "claude-sonnet-4-20250514",
          "name": "Claude Sonnet 4",
          "contextWindow": 200000,
          "maxTokens": 8192,
          "cost": { "input": 3, "output": 15, "cacheRead": 0.3, "cacheWrite": 3.75 }
        }
      ]
    }
  },
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash"
}
```

### 自定义 API 端点 / HTTP 代理 / Headers

`baseUrl` 指向 API 端点或 API 网关；`httpProxy` 只配置该 provider 的网络代理。`httpProxy` 为空时，会保留 Go 默认的 `HTTP_PROXY` / `HTTPS_PROXY` 环境变量行为。当代理或网关的 HTTP/2 流式传输不稳定时，可以把 `forceHTTP11` 设为 `true`。

使用 `headers` 可以为某个 provider 的每次请求附加自定义 HTTP header。Header 值支持与 `apiKey` 相同的解析规则，包括 `${ENV_VAR}` 和需显式开启的 `!cmd` shell 命令。自定义 header 会在 MothX 默认 provider header 之后应用，因此必要时也可以覆盖 `Authorization`、`x-api-key` 或网关要求的特定 header。

```json
{
  "providers": {
    "my-proxy": {
      "baseUrl": "https://my-proxy.example.com/v1",
      "api": "openai-chat",
      "apiKey": "${MY_PROXY_API_KEY}",
      "httpProxy": "http://127.0.0.1:7890",
      "forceHTTP11": true,
      "headers": {
        "X-Gateway-Token": "${MY_GATEWAY_TOKEN}",
        "X-Request-Source": "mothx"
      },
      "models": [
        {
          "id": "gpt-4o",
          "name": "GPT-4o (via proxy)",
          "contextWindow": 128000,
          "maxTokens": 16384
        }
      ]
    }
  },
  "defaultProvider": "my-proxy",
  "defaultModel": "gpt-4o"
}
```

### 启用沙箱并自定义路径

```json
{
  "sandbox": {
    "enabled": true,
    "level": "standard",
    "allowNetwork": false,
    "allowedRead": ["/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc/ssl", "/opt/go"],
    "passEnv": ["PATH", "HOME", "USER", "LANG", "TERM", "SHELL", "GOPATH", "GOROOT"],
    "tmpSize": "200m"
  }
}
```

### 使用代码压缩模板

```json
{
  "compaction": {
    "enabled": true,
    "reserveTokens": 16384,
    "keepRecentTokens": 20000,
    "tokenizer": "auto",
    "template": "code"
  }
}
```

### 项目级覆盖

放在 `.mothx/settings.json` 中可覆盖特定项目的设置：

```json
{
  "defaultMode": "yolo",
  "defaultThinkingLevel": "high",
  "shellCommandPrefix": "source .venv/bin/activate && ",
  "approval": {
    "bashWhitelist": ["python ", "pytest ", "pip ", "make "],
    "confirmBeforeWrite": false
  }
}
```

这会与全局设置合并 — 只有你指定的字段会被覆盖。
