# Configuration Guide

## Configuration File Locations

MothX uses two configuration files:

| File | Platform | Scope | Priority |
|------|----------|-------|----------|
| `~/.mothx/settings.json` | Linux/macOS | Global (all projects) | Low |
| `%APPDATA%\mothx\settings.json` | Windows | Global (all projects) | Low |
| `.mothx/settings.json` | All | Project-level | High |

> **Tip:** Override the global configuration directory with `MOTHX_DIR`.

> **Windows:** `%APPDATA%` resolves to `C:\Users\<Username>\AppData\Roaming`, so the full path is typically `C:\Users\<Username>\AppData\Roaming\mothx\settings.json`.

Project-level configuration overrides global configuration. Providers are merged by provider ID and then by fields explicitly present in the project file. An explicitly supplied `models` array replaces that provider's inherited model list.

### Legacy Directories

The current release uses `.mothx` for project files and `~/.mothx` (or `%APPDATA%\mothx` on Windows) for global files. Legacy `.vibe` and `.vibecoding` directories are neither read nor migrated. Copy any configuration you still need into the corresponding `.mothx` directory and review paths and fields.


### `tuilang` (TUI language)

The top-level `tuilang` setting accepts `"auto"` (the default), `"zh"`, or `"en"`. `zh` and `en` force the corresponding language. In `auto`, MothX checks the current UTC offset once at TUI startup: exactly UTC+08:00 selects Chinese; every other offset selects English. A project `.mothx/settings.json` value overrides the global value. The TUI `/settings` menu can switch the language, choose global or project scope, and apply a successful change immediately. Slash command syntax remains English in every language.
## Configuration Structure

### Complete Example

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

## All Configuration Fields

### Top-Level Fields Reference

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `providers` | object | *(see below)* | Provider configurations (keyed by name) |
| `defaultProvider` | string | `"deepseek-openai"` | Which provider to use by default |
| `defaultModel` | string | `"deepseek-v4-flash"` | Which model ID to use by default |
| `defaultMode` | string | `"yolo"` | Default run mode: `plan`, `agent`, `yolo`, or `os` |
| `defaultThinkingLevel` | string | `"medium"` | Default thinking level |
| `toolExecution` | object | *(see below)* | Local function/custom tool execution mode and per-batch concurrency |
| `statusLine` | object | *(see below)* | External status line command settings for TUI only |
| `enablePlanTool` | bool | `true` | Register the built-in `plan` tool |
| `enableArtifact` | bool | `false` | Allow TUI/CLI sessions to publish generated artifacts |
| `enableACPArtifact` | bool | `false` | Allow Desktop/ACP sessions to publish generated artifacts |
| `maxContextTokens` | int | `0` (auto) | Override maximum context token count |
| `contextFiles` | object | *(see below)* | Context file loading settings |
| `skillsDir` | string | `"~/.mothx/skills"` | Global skills directory path |
| `compaction` | object | *(see below)* | Context compaction settings |
| `sandbox` | object | *(see below)* | Sandbox execution settings |
| `sessionDir` | string | `"~/.mothx/sessions"` | SQLite session storage directory |
| `shellPath` | string | `""` (auto) | Custom shell path for Bash tool |
| `shellCommandPrefix` | string | `""` | Prefix prepended to every shell command |
| `theme` | string | `"dark"` | UI theme: `"dark"` or `"light"` |
| `tuilang` | string | `"auto"` | TUI language: `"auto"`, `"zh"`, or `"en"` |
| `retry` | object | *(see below)* | API call retry settings |
| `approval` | object | *(see below)* | Bash command approval settings |
| `webSearch` | object | *(see below)* | Hosted web search settings |
| `updateCheck` | bool | `true` | Enable npm-based update notifications |

---

## Configuration Details

### statusLine

TUI-only status line command configuration. When enabled, MothX sends the current status JSON to an external command via stdin and replaces the built-in footer below the input box with that command's stdout.

If it is disabled, not configured, still on its first run, times out, fails, or returns empty output, the TUI keeps the current built-in footer.

Other modes ignore this setting entirely, including Serve, Channels, ACP, and print mode.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable the external status line |
| `type` | string | `"command"` | Currently only `"command"` is supported |
| `command` | string | `""` | Status line command to execute, for example `ccstatusline` |
| `padding` | int | `0` | Blank lines appended after the status line output |
| `refreshInterval` | int | `0` | Optional periodic refresh in seconds; `0` means event-driven only |
| `timeoutMs` | int | `800` | Per-invocation timeout in milliseconds |
| `fallback` | string | `"builtin"` | Fallback strategy on failure; keep `"builtin"` |

Minimal example:

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

Notes:

- The current implementation supports `stdin snapshot` mode only.
- It does not emit `transcript_path`.
- It does not generate transcript JSONL files for `ccstatusline`.
- Widgets that depend on transcript history scanning may therefore be empty or less accurate.

### providers

Multi-provider configuration. Each provider is an object keyed by a user-chosen name:

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `baseUrl` | string | ✓ | — | API base URL |
| `vendor` | string | — | auto-detect | Optional vendor adapter name (see below) |
| `apiKey` | string | — | `""` | API key (see [Authentication](#authentication-configuration) below) |
| `api` | string | — | auto-detect | API protocol: `"openai-chat"`, `"openai-responses"`, `"anthropic-messages"`, `"google-gemini"`, or `"google-vertex"` |
| `httpProxy` | string | — | `""` | Optional per-provider HTTP proxy URL, e.g. `"http://127.0.0.1:7890"` |
| `forceHTTP11` | bool | — | `false` | Force HTTP/1.1 for this provider by disabling HTTP/2 on the provider HTTP client |
| `headers` | object | — | `{}` | Optional custom HTTP headers applied to every provider request; values support the same `${ENV}` and `!cmd` resolution as `apiKey` |
| `thinkingFormat` | string | — | auto-detect | Thinking parameter format (see below) |
| `cacheControl` | bool | — | `false` | Enable Anthropic prompt caching; set `true` when using Claude models |
| `models` | array | — | `[]` | List of available models |

#### vendor field

The `vendor` field selects a vendor adapter without changing the provider config schema. It is optional; when omitted, MothX tries to detect the vendor from `baseUrl`, then falls back to the generic protocol provider selected by `api`.

Selection order:

1. Explicit `vendor`
2. Base URL detection
3. Generic fallback: `openai-chat`, `openai-responses`, `anthropic-messages`, `google-gemini`, or `google-vertex`

Built-in vendor adapters include `openai`, `anthropic`, `claude`, `deepseek`, `google-gemini`, `google-vertex`, `xiaomi`, `xiaomi-token-plan-ams`, `xiaomi-token-plan-cn`, `xiaomi-token-plan-sgp`, `volcengine`, `kimi`, `minimax`, `qianfan`, `bailian`, `gitee`, `openrouter`, `together`, `groq`, `fireworks`, `mistral`, `github-copilot`, `cloudflare-ai-gateway`, `cloudflare-workers-ai`, and `amazon-bedrock`.

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

#### Volcengine / Doubao example

The built-in `volcengine` provider uses the Ark OpenAI-compatible endpoint and supports Doubao Seed models. It can also be detected automatically from `ark.cn-beijing.volces.com`.

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

Hosted web search settings. This is disabled by default.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | — | `false` | Enable hosted web search registration |
| `provider` | string | — | `defaultProvider` | Provider configuration name to use for hosted web search |
| `providerType` | string | — | auto | Hosted tool type, usually `responses` or `messages` |
| `model` | string | — | `""` | Optional metadata for routing, display, or future provider-specific handling |

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

When `provider` points to a configured provider name, MothX resolves that provider's `baseUrl`, `api`, and vendor behavior before registering the hosted search tool. OpenAI Responses providers also expose the native Responses `web_search` tool automatically; this native capability does not depend on `webSearch.enabled`.

#### api field

The `api` field specifies the **protocol format**, not the service provider. You can point any provider to any compatible endpoint:

- `openai-chat`: OpenAI Chat Completions API format
- `openai-responses`: OpenAI Responses API format (`POST /v1/responses`)
- `anthropic-messages`: Anthropic Messages API format
- `google-gemini`: Native Gemini API `streamGenerateContent` format
- `google-vertex`: Native Vertex AI Gemini `streamGenerateContent` format

For example, DeepSeek offers both formats at different endpoints, and you can also use these formats to connect to the actual OpenAI or Anthropic services.

If not specified, auto-detected based on `baseUrl`:
- Contains `generativelanguage.googleapis.com` → `google-gemini`
- Contains `aiplatform.googleapis.com` → `google-vertex`
- Contains "anthropic" → `anthropic-messages`
- Others → `openai-chat`

#### responses field

`responses` configures the advanced behavior used when `api` is `"openai-responses"`. All fields are optional; model compatibility flags may reject unsupported fields before an upstream request is made.

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

| Field | Values / Type | Description |
|-------|---------------|-------------|
| `stateMode` | `replay`, `previous_response_id`, `conversation` | Session state strategy. `replay` is local-history replay; `previous_response_id` continues the provider response lineage; `conversation` requires `conversation`. |
| `store` | bool | Request upstream response storage when supported. |
| `conversation` | string | Provider conversation ID; required with `stateMode: "conversation"`. |
| `truncation` | `auto`, `disabled` | Upstream Responses truncation behavior. |
| `background` | bool | In Serve, run supported Responses requests remotely as durable background jobs. MothX polls them, resumes recoverable jobs after a restart, and handles local tools. |
| `reasoningSummary` | `auto`, `concise`, `detailed`, `none`, `off` | Request a reasoning summary where supported. |
| `reasoningContext` | `auto`, `current_turn`, `all_turns` | Controls the reasoning context sent between turns. |
| `reasoningMode` | `standard`, `pro` | Provider-specific reasoning mode. |
| `promptCacheEnabled` | bool | Enabled by default for Responses providers; set `false` to disable prompt-cache fields. |
| `promptCacheKey`, `promptCacheRetention`, `promptCacheMode`, `promptCacheTTL` | string | Prompt-cache key and provider-specific retention/mode/TTL controls. |
| `safetyIdentifier` | string | Provider safety identifier. |
| `metadata` | object | Non-sensitive provider metadata (maximum 16 entries). |
| `include` | string array | Extra supported Response item fields to include. |
| `serviceTier` | string | Provider service tier. |
| `structuredOutput` | object | JSON Schema structured output: `name`, `description`, `strict`, and `schema`. |
| `toolControl` | object | Tool selection and execution controls: `choice`, `parallel`, `maxCalls`. |
| `hostedTools` | object | Provider-hosted tool options: `webSearch`, `fileSearch`, `codeInterpreter`, `imageGeneration`, and `remoteMCP`. |

`background` is only available through `mothx serve`; normal CLI/TUI/ACP requests continue to use the streaming agent loop. A background-capable provider must support the selected model and Responses features.

`toolControl.choice` selects the `tool_choice` sent to OpenAI Responses providers: `auto`, `none`, `required`, or the name of a tool to force that tool. The WebUI exposes `responses.toolControl` under Settings > Providers (Tool Choice, Parallel Tool Calls, Max Tool Calls), and the TUI exposes it under `/settings` > provider > B. Protocol > Responses. The TUI editor writes the global settings file; project-level overrides remain supported through `.mothx/settings.json`.

Google native providers can be configured directly:

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

For `google-vertex`, `${GOOGLE_CLOUD_API_KEY}` uses the Vertex AI API key path and does not require `gcloud`, project, or location in the URL. OAuth access tokens are still supported by using a project-scoped URL such as `https://aiplatform.googleapis.com/v1/projects/YOUR_PROJECT/locations/global/publishers/google/models` and an access token value such as `!gcloud auth print-access-token`. Set `VIBECODING_ALLOW_SHELL_CONFIG=1` before using `!command` values.

#### thinkingFormat field

Specifies how thinking/reasoning parameters are sent to the API:

| Value | Behavior |
|-------|----------|
| `""` (empty) | Auto-detect based on URL |
| `"openai"` | Use OpenAI `reasoning_effort` format |
| `"anthropic"` | Use Anthropic `thinking` with `budget_tokens` |
| `"deepseek"` | Use DeepSeek `thinking: {type: "enabled"}` + `reasoning_effort` (OpenAI) or `output_config.effort` (Anthropic) |
| `"xiaomi"` | Legacy thinking-only format: `thinking: {type: "enabled"}` |
| `"doubao-seed"` | Doubao Seed 2.1 / Evolving `reasoning_effort`: `minimal` (no thinking), `low`, `medium`, `high` (default) |

When not set, automatically detects:
- URL contains `deepseek` → `"deepseek"`
- URL contains `xiaomimimo` → `"xiaomi"`
- Model ID matches `doubao-seed-2.1*` or `doubao-seed-evolving` → `"doubao-seed"`

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

#### cacheControl field

Enable Anthropic-style prompt caching. When set to `true`, MothX adds cache control headers to requests. **You should enable this when using Claude models through the Anthropic API** to reduce cost and latency.

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

#### models array

Each model in the `models` array:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `id` | string | — | Model ID sent to the API |
| `name` | string | — | Human-readable display name |
| `reasoning` | bool | `false` | Whether the model supports thinking/reasoning |
| `contextWindow` | int | `0` | Context window size (tokens) |
| `maxTokens` | int | `0` | Maximum output tokens per response. An explicit `0` disables the output-token limit on OpenAI/Google-style endpoints (the field is omitted from the request); Anthropic endpoints require `max_tokens`, so an explicit `0` falls back to the default of 16384 there |
| `input` | []string | `[]` | Supported input modalities: `"text"`, `"image"` |
| `cost` | object | `null` | Pricing per million tokens |
| `compat` | object | `null` | Model-specific compatibility flags for provider quirks |

The `cost` object:

| Field | Type | Description |
|-------|------|-------------|
| `input` | float | Cost per million input tokens |
| `output` | float | Cost per million output tokens |
| `cacheRead` | float | Cost per million cached read tokens (Anthropic) |
| `cacheWrite` | float | Cost per million cached write tokens (Anthropic) |

The `compat` object is optional and should only be set when a model needs protocol-specific adjustments:

| Field | Type | Description |
|-------|------|-------------|
| `thinkingFormat` | string | Override model thinking format (`openai`, `deepseek`, `xiaomi`, `anthropic`, etc.) |
| `requiresReasoningContentOnAssistant` | bool | Send empty `reasoning_content` on replayed assistant messages |
| `requiresReasoningContentOnAssistantMessages` | bool | Alias used by the reference implementation; treated the same as above |
| `forceAdaptiveThinking` | bool | Force Anthropic adaptive thinking format |
| `supportsReasoningEffort` | bool | Whether the model accepts `reasoning_effort` |
| `maxTokensField` | string | Use `max_tokens` or `max_completion_tokens` |
| `supportsDeveloperRole` | bool | Whether developer-role messages are supported |
| `supportsStore` | bool | Whether OpenAI `store` is supported |
| `supportsStrictMode` | bool | Whether strict tool schemas are supported |
| `supportsParallelToolCalls` | bool | Whether the provider/model accepts explicit parallel tool-call controls; set `false` for gateways that reject them. Anthropic Messages then omits the entire `tool_choice` object, not only `disable_parallel_tool_use` |
| `supportsToolChoice` | bool | Whether the provider/model accepts explicit tool-choice controls; Anthropic Messages omits `tool_choice` when this is `false` |
| `supportsCacheControlOnTools` | bool | Whether cache control can be applied to tool definitions |
| `supportsLongCacheRetention` | bool | Whether long prompt-cache retention is supported |
| `sendSessionAffinityHeaders` | bool | Whether session affinity headers should be sent |
| `supportsEagerToolInputStreaming` | bool | Whether Anthropic eager tool input streaming is supported |

`supportsToolChoice` and `supportsParallelToolCalls` are also editable in the WebUI model row and in the TUI under `/settings` > model > Compatibility; the remaining flags stay file-only.

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

This is a local execution setting only. `sequential` limits MothX's local workers after a response arrives; it does not ask the model to emit one tool call at a time, so providers may still return multiple tool calls in a single response. A `parallel` batch begins its calls in the model's declared order (a later call never starts before an earlier one has begun, even while that earlier call waits for approval) and then runs them concurrently, so only the completion order is unrestricted. The setting is shared by TUI, WebUI, Serve, channels, ACP, and sub-agents built through the common runtime. It does not control concurrency inside provider-hosted tools such as OpenAI Responses `web_search`; those calls remain under the provider's own orchestration and quota rules.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `mode` | string | `"parallel"` | `"parallel"` uses bounded local workers; `"sequential"` executes returned calls one at a time locally (provider-side parallel output is unaffected) |
| `maxConcurrency` | int | `10` | Maximum local tool calls in flight for one Agent/tool-call batch; `1` is serial; omitted or non-positive values use `10` |

Example:

```json
{
  "toolExecution": {
    "mode": "parallel",
    "maxConcurrency": 10
  }
}
```

The WebUI exposes these fields in Settings > Tools, and the TUI exposes them under `/settings` > Behavior. The TUI editor writes the global settings file; project-level overrides remain supported through `.mothx/settings.json`. Existing sessions use the setting when a new Agent is built; an already running turn keeps its resolved limit.

### defaultProvider

Default provider name. Must match a key in `providers`.

```json
{ "defaultProvider": "deepseek-openai" }
```

### defaultModel

Default model ID. Must match an `id` in the chosen provider's `models` list.

```json
{ "defaultModel": "deepseek-v4-flash" }
```

### defaultMode

Default run mode:

| Value | Description |
|-------|-------------|
| `plan` | Read-only analysis mode — no file writes, sandboxed |
| `agent` | Standard read/write mode — Bash requires approval |
| `yolo` | Full access mode — all tools auto-execute |
| `os` | Shell-only mode — exposes Bash with YOLO-level approval behavior and no sandbox; high-risk protections still apply |

```json
{ "defaultMode": "yolo" }
```

### defaultThinkingLevel

Default thinking level for reasoning models:

| Value | Description |
|-------|-------------|
| `off` | Disable thinking |
| `minimal` | Minimal thinking |
| `low` | Low level |
| `medium` | Medium level (default) |
| `high` | High level |
| `xhigh` | Highest level |

```json
{ "defaultThinkingLevel": "medium" }
```

### enablePlanTool

Whether to register the built-in `plan` tool that allows the agent to create and track structured task plans.

```json
{ "enablePlanTool": true }
```

Set to `false` to disable it (e.g., if you prefer the agent not to use structured plans).

### enableArtifact / enableACPArtifact

Artifact publishing is opt-in and independently configured for terminal and ACP/Desktop sessions. Enabling one does not enable the other.

```json
{
  "enableArtifact": false,
  "enableACPArtifact": false
}
```

### maxContextTokens

Override the maximum context token count. When set to `0` (default), the value is derived from the model's `contextWindow`.

```json
{ "maxContextTokens": 200000 }
```

### contextFiles

Context file loading settings.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Whether to automatically load context files |
| `extraFiles` | []string | `[]` | Additional context file paths to load |

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

#### Auto-loaded Context Files

MothX automatically searches for and loads the following files:

1. **Global files** (in the global config directory):
   - `AGENTS.md`
   - `CLAUDE.md`

2. **Project files** (searched upward from current directory):
   - `AGENTS.md`
   - `CLAUDE.md`
   - `.mothx/AGENTS.md`
   - `.mothx/CLAUDE.md`
   - `.vibe/AGENTS.md` (legacy compatibility)
   - `.vibe/CLAUDE.md` (legacy compatibility)

---

### skillsDir

Path to the global skills directory. Supports `~` expansion.

| Platform | Default |
|----------|---------|
| Linux/macOS | `~/.mothx/skills` |
| Windows | `%APPDATA%\mothx\skills` |

```json
{ "skillsDir": "~/.mothx/skills" }
```

Skills are loaded from:
- **Global skills**: `<skillsDir>/<name>/SKILL.md`
- **Project skills**: `.skills/<name>/SKILL.md` (override global)

---

### compaction

Context compaction (compression) configuration for managing long conversations. MothX automatically summarizes older messages when the estimated request reaches about 80% of the model context window.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable automatic context compaction |
| `reserveTokens` | int | `16384` | Compatibility setting also used to cap compaction summary output |
| `keepRecentTokens` | int | `20000` | Recent message tokens to keep uncompacted |
| `tokenizer` | string | `"auto"` | Token estimator selector. Currently `auto` and `generic` use the built-in chars/4 estimator |
| `tokenizerModel` | string | `""` | Optional model hint for future model-specific token estimators |
| `template` | string | `"default"` | Compression summary template: `default`, `code`, or `conversation` |

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

#### Compression Templates

- **`default`**: General structured checkpoint with goal, progress, decisions, next steps, and critical context.
- **`code`**: Coding-oriented checkpoint that emphasizes file paths, code changes, verification, and technical decisions.
- **`conversation`**: Concise discussion checkpoint for non-code conversations.

---

### sandbox

Sandbox configuration for secure command execution. Uses [bubblewrap (bwrap)](https://github.com/containers/bubblewrap) on Linux.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable sandboxed execution |
| `level` | string | `"none"` | Sandbox level: `"none"`, `"standard"`, `"strict"` |
| `bwrapPath` | string | `""` (auto) | Custom path to the `bwrap` binary |
| `allowNetwork` | bool | `false` | Allow network access inside sandbox |
| `allowedRead` | []string | *(platform-specific)* | Paths readable inside the sandbox |
| `allowedWrite` | []string | `[]` | Additional paths writable inside the sandbox |
| `deniedPaths` | []string | *(platform-specific)* | Paths explicitly denied inside the sandbox |
| `passEnv` | []string | *(platform-specific)* | Environment variables passed into the sandbox |
| `tmpSize` | string | `"100m"` | Size limit for the sandbox's `/tmp` tmpfs mount |

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

#### Sandbox Levels

| Level | File System | Network | Use Case |
|-------|------------|---------|----------|
| `none` | Full access | ✓ | No sandboxing (YOLO mode default) |
| `standard` | Project read-write | ✗ | Everyday development (Agent mode) |
| `strict` | Project read-only | ✗ | Code review / analysis (Plan mode) |

#### Platform Defaults for allowedRead

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

#### Platform Defaults for deniedPaths

**Linux / macOS:**
```json
["/etc/shadow", "/etc/gshadow", "/etc/passwd", "/root", "/home"]
```

**Windows:**
```json
["C:\\Users\\<Username>\\Documents", "C:\\Users\\<Username>\\Desktop"]
```

#### Platform Defaults for passEnv

**All platforms:** `PATH`, `HOME`, `USER`, `LANG`, `LC_ALL`, `TERM`

**Linux additionally:** `SHELL`, `GOPATH`, `GOROOT`, `GOPROXY`, `GOMODCACHE`, `NODE_PATH`

**macOS additionally:** `SHELL`, `TMPDIR`

**Windows additionally:** `APPDATA`, `LOCALAPPDATA`, `COMPUTERNAME`, `USERPROFILE`, `SYSTEMROOT`

---

### sessionDir

Directory for SQLite session storage. Supports `~` expansion.

MothX stores all session metadata and entries in a single, unified `sessions.db` database file under `sessionDir` (using virtual handles for CLI/TUI, and creating physical handles as needed for Channels). See [Session Management](sessions.md) for the layout.

| Platform | Default |
|----------|---------|
| Linux/macOS | `~/.mothx/sessions` |
| Windows | `%APPDATA%\mothx\sessions` |

```json
{ "sessionDir": "~/.mothx/sessions" }
```

---

### shellPath

Custom shell path for the Bash tool. When empty (default), MothX uses the platform default:

| Platform | Default |
|----------|---------|
| Linux | `$SHELL` or `/bin/bash` |
| macOS | `$SHELL` or `/bin/zsh` |
| Windows | `powershell.exe` or `cmd.exe` |

```json
{ "shellPath": "/usr/bin/fish" }
```

### shellCommandPrefix

A string prepended to every shell command before execution. Useful for setting up environment or activating virtualenvs.

```json
{ "shellCommandPrefix": "source ~/.venv/bin/activate && " }
```

When empty (default), commands are executed directly.

---

### theme

UI color theme for the terminal interface.

| Value | Description |
|-------|-------------|
| `"dark"` | Dark background theme (default) |
| `"light"` | Light background theme |

```json
{ "theme": "dark" }
```

---

### retry

API call retry configuration with exponential backoff. Retries apply to transient initial HTTP failures and early SSE read failures before any visible output has been emitted. Once text, thinking, tool calls, or usage have streamed to the client, stream read failures are not retried to avoid duplicate output.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable automatic retries on transient API errors |
| `maxRetries` | int | `5` | Maximum number of retry attempts |
| `baseDelayMs` | int | `3000` | Base delay in milliseconds (doubles on each retry) |

```json
{
  "retry": {
    "enabled": true,
    "maxRetries": 5,
    "baseDelayMs": 3000
  }
}
```

#### Retryable Errors

The following errors trigger automatic retries:

| Category | Examples |
|----------|----------|
| Rate limiting | HTTP 429 |
| Server errors | HTTP 502, 503, 504 |
| Network errors | connection refused, connection reset, DNS errors, HTTP/2 `INTERNAL_ERROR` stream resets |
| Timeouts | HTTP client timeout, TCP timeout |

The following are **not** retried:
- Context cancellation (user pressed Ctrl+C)
- HTTP 4xx client errors (except 429): 400, 401, 403, 404
- Stream read failures after visible output has already been emitted

#### Backoff Strategy

Each retry waits `baseDelayMs × 2^attempt` milliseconds, capped at 30 seconds:

| Attempt | Delay (base=2000ms) |
|---------|--------------------|
| 1st | 2s |
| 2nd | 4s |
| 3rd | 8s |

When a retry occurs, MothX displays a status message in the TUI:
```
Retrying (1/3): request timed out — waiting 2.0s...
Retrying (2/3): rate limited (HTTP 429) — waiting 4.0s...
```

#### Disabling Retries

```json
{
  "retry": {
    "enabled": false
  }
}
```

---

### approval

Agent mode approval configuration. Controls which Bash commands auto-execute and which require user confirmation.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `bashWhitelist` | []string | *(see below)* | Command prefixes that auto-approve in agent mode |
| `bashBlacklist` | []string | `[]` | Command prefixes that **always** require approval |
| `confirmBeforeWrite` | bool | `true` | Require user approval before `Write`/`Edit` tools run in agent mode |

#### Default Whitelist

```json
["go ", "make ", "git ", "npm ", "yarn ", "node ", "python ", "pip "]
```

#### Approval Flow

```
Agent requests tool execution
│
▼
Check mode
├─ Plan mode → Deny (read-only)
├─ Agent mode → Continue checking
└─ YOLO mode → Auto-approve unless blacklisted
│
▼
Blacklist check (highest priority):
├─ Command matches blacklist → Require user approval
└─ Otherwise continue
│
▼
In Agent mode:
├─ Project allow.json matches → Auto-approve (see below)
├─ Command matches settings whitelist → Auto-approve
├─ Write/Edit tool + confirmBeforeWrite=true → Require user approval
├─ Non-Bash tool → Auto-approve
└─ Otherwise → Require user approval
│
▼
In --print mode:
  Commands that would need approval → Fail immediately
```

#### Example Configurations

**Only allow git and npm:**
```json
{
  "approval": {
    "bashWhitelist": ["git ", "npm "]
  }
}
```

**Custom blacklist:**
```json
{
  "approval": {
    "bashWhitelist": ["go ", "make ", "git "],
    "bashBlacklist": ["rm -rf", "sudo", "dd "]
  }
}
```

**Disable write confirmation (trust the agent):**
```json
{
  "approval": {
    "confirmBeforeWrite": false
  }
}
```

### Project-Level Allow Rules (`allow.json`)

In addition to the global `settings.json` approval configuration, MothX supports project-level allow rules in `allow.json` (`.mothx/allow.json`). These rules enable auto-approval of specific bash commands **per project** without modifying global settings.

| File | Scope | Priority |
|------|-------|----------|
| `.mothx/allow.json` | Current project | High |
| `~/.mothx/allow.json` | Global fallback | Low |

#### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `autoEdit` | bool | `true` | Auto-approve `write`/`edit` tools in agent mode. When no `allow.json` exists, defaults to `true`. A file with `"autoEdit": false` explicitly disables it. |
| `editPaths` | []string | `[]` | Glob patterns for paths whose `write`/`edit` auto-approve in agent mode. Supports `**` (cross-directory) and `*` (single segment). Project-level only. |
| `bashCommands` | []string | `[]` | Exact bash command strings that auto-approve in agent mode. Project-level only. |
| `bashPrefixes` | []string | `[]` | Bash command prefixes that auto-approve in agent mode. Trailing spaces are significant (e.g. `"go test "` matches `go test ./...`). Project-level only. |

#### Interaction with Settings

- **Blacklist overrides allow rules**: A command matching `bashBlacklist` in `settings.json` will always require approval, even if it matches a project-level `bashCommands` or `bashPrefixes` entry.
- **`autoEdit` inheritance**: Global `autoEdit` is inherited unless the project file explicitly sets it. Writing a project `allow.json` without `autoEdit` will **not** persist the inherited global value.

#### Example `.mothx/allow.json`

```json
{
  "autoEdit": true,
  "editPaths": ["docs/**", "*.md"],
  "bashCommands": ["make test", "make build"],
  "bashPrefixes": ["go test ", "go vet "]
}
```

#### Managing from the TUI

- The approval dialog offers **"Always Allow Exact Command"** and **"Always Allow Command Prefix"** options when a bash command is pending. Selecting one persists the rule to `.mothx/allow.json`.
- Use `/alloweditpath add <glob>` to manage `editPaths`.
- Use `/allowautoedit [on|off]` to toggle `autoEdit`.

---

## MCP Configuration

MCP servers are configured in standalone `mcp.json` files, not in `settings.json`.

MothX loads MCP configuration at startup from:

1. Global config: `~/.mothx/mcp.json` on Linux/macOS, or `%APPDATA%\mothx\mcp.json` on Windows
2. Project config: `.mothx/mcp.json`

Serve's Web UI provides an MCP editor in **Settings → MCP** for the global file, plus a project-level editor from the active session's chat toolbar. Saved MCP servers are applied to newly created sessions; existing sessions keep their registered tools until recreated.

Create a template from the TUI:

```text
/init_mcp project full
/init_mcp global basic
/mcps
```

Example:

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

Supported transports:

- `stdio`: `command` may be an executable name resolved through `PATH`, or a direct path; put arguments in `args`
- `http`: streamable HTTP endpoint via `url`
- `sse`: legacy SSE stream via `url` plus `messageUrl`

MCP tools are registered after built-in tools and `skill_ref`, but before the agent is created. The agent freezes its system prompt and tool definitions for the session, so changes to `mcp.json` require restarting the client or creating a new Serve session.

Tool names use `mcp_<server_name>_<tool_name>`. If a name already exists, MothX appends a numeric suffix instead of replacing an existing tool. Starter-template placeholders such as `/absolute/path/to/mcp-server`, `example.com`, and `replace-me` are ignored during automatic startup loading.

---

## Authentication Configuration

MothX supports multiple ways to provide API keys, with flexible resolution logic.

### Key Resolution Order

When MothX needs the API key for a provider, it checks in this order:

1. **Provider `apiKey` field** in `settings.json` — if set, resolved using the rules below
2. **Derived environment variable** — provider name is converted to an env var: e.g., `deepseek-openai` → `DEEPSEEK_OPENAI_API_KEY`

### apiKey Field Formats

The `apiKey` field in a provider config supports three formats:

| Format | Example | Behavior |
|--------|---------|----------|
| `${VAR}` | `"${DEEPSEEK_API_KEY}"` | Reads the value of environment variable `VAR` |
| `!command` | `"!pass show deepseek-key"` | Executes a shell command and uses its stdout only when `VIBECODING_ALLOW_SHELL_CONFIG=1` |
| Plain string | `"sk-abc123..."` | Used as-is (⚠️ not recommended for shared configs) |

#### Environment Variable Reference

```json
{
  "providers": {
    "deepseek-openai": {
      "apiKey": "${DEEPSEEK_API_KEY}"
    }
  }
}
```

Then set the environment variable:

```bash
export DEEPSEEK_API_KEY=sk-...
```

#### Shell Command (Password Manager Integration)

Prefix with `!` to run a shell command. MothX uses `sh -c` on Linux/macOS and `powershell.exe` on Windows.

Shell command resolution is disabled by default. To enable it for trusted local configuration, set:

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

This is useful for integrating with password managers like `pass`, `1password-cli`, macOS Keychain, or any other secret store.

#### Derived Environment Variable Fallback

If no `apiKey` is configured for a provider, MothX derives an environment variable name from the provider name:

| Provider Name | Derived Env Var |
|---------------|-----------------|
| `deepseek-openai` | `DEEPSEEK_OPENAI_API_KEY` |
| `deepseek-anthropic` | `DEEPSEEK_ANTHROPIC_API_KEY` |
| `my-custom-provider` | `MY_CUSTOM_PROVIDER_API_KEY` |
| `anthropic` | `ANTHROPIC_API_KEY` |
| `openai` | `OPENAI_API_KEY` |

The rule: replace `-` with `_`, uppercase everything, append `_API_KEY`.

### Authentication Examples

**Option 1: Environment Variables (simplest)**

```bash
export DEEPSEEK_API_KEY=sk-...
```

With default config, MothX will look for `DEEPSEEK_OPENAI_API_KEY` for the `deepseek-openai` provider. But if the provider's `apiKey` is set to `${DEEPSEEK_API_KEY}`, it reads that env var instead.

**Option 2: Inline in Configuration File**

```json
{
  "providers": {
    "deepseek-openai": {
      "apiKey": "sk-..."
    }
  }
}
```

**Option 3: Password Manager**

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

## Invalid Settings Recovery

When a global or project-level `settings.json` cannot be parsed during startup, MothX attempts to rename it to a timestamped backup in the same directory, for example:

```text
settings.json.bak_20260715-143000
```

If that name already exists, MothX appends a numeric suffix such as `_1`. The warning printed to stderr includes the platform-native absolute backup path.

After a successful backup, an invalid global file is replaced by built-in defaults for the current run, while an invalid project file is ignored and the valid global settings remain active. The default global `settings.json` is recreated on the next startup. If the backup cannot be created—for example because the directory is read-only—configuration loading returns an error.

---

## Environment Variable Overrides

These environment variables override settings at runtime:

| Environment Variable | Overrides | Example |
|---------------------|-----------|---------|
| `MOTHX_DIR` | Global configuration directory | `export MOTHX_DIR=/custom/config` |
| `VIBECODING_PROVIDER` | `defaultProvider` | `export VIBECODING_PROVIDER=anthropic` |
| `VIBECODING_MODEL` | `defaultModel` | `export VIBECODING_MODEL=claude-sonnet-4-20250514` |
| `VIBECODING_MODE` | `defaultMode` | `export VIBECODING_MODE=yolo` |
| `VIBECODING_THINKING` | `defaultThinkingLevel` | `export VIBECODING_THINKING=high` |
| `VIBECODING_DEBUG` | Enable provider-level request/response debug output | `export VIBECODING_DEBUG=1` |
| `VIBECODING_PPROF_ADDR` | pprof listen address when `--debug` is set | `export VIBECODING_PPROF_ADDR=127.0.0.1:6061` |
| `VIBECODING_NO_UPDATE_CHECK` | Disable update check | `export VIBECODING_NO_UPDATE_CHECK=1` |
| `VIBECODING_NPM_REGISTRY` | Override npm registry for update checks | `export VIBECODING_NPM_REGISTRY=https://registry.npmjs.org` |

---

## Configuration Examples

### Minimal Configuration

Only need to set the default provider and model. Everything else uses sensible defaults.

```json
{
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash"
}
```

### Multi-Provider Configuration

Switch between providers at runtime using `/provider` or `--provider`:

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

### Custom API Endpoint / HTTP Proxy / Headers

`baseUrl` points to an API endpoint or API gateway. `httpProxy` configures the network proxy used only by that provider's HTTP client. When `httpProxy` is empty, the provider keeps Go's default `HTTP_PROXY` / `HTTPS_PROXY` environment behavior. Set `forceHTTP11` to `true` when a proxy or gateway has unstable HTTP/2 streaming behavior.

Use `headers` to attach custom HTTP headers to every request for a provider. Header values support the same resolution rules as `apiKey`, including `${ENV_VAR}` and opt-in `!cmd` shell commands. Custom headers are applied after MothX's default provider headers, so they can also override defaults such as `Authorization`, `x-api-key`, or gateway-specific headers when needed.

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

### Enable Sandbox with Custom Paths

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

### Use the Code Compaction Template

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

### Project-Level Override

Place in `.mothx/settings.json` to override specific settings for a project:

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

This merges with your global settings — only the fields you specify are overridden.
