# Adding a New Provider

This guide explains how to add support for a new LLM provider or vendor to MothX.

## Architecture Overview

MothX separates two concerns when adding provider support:

1. **Protocol Providers** — handle the wire protocol (OpenAI Chat Completions, Anthropic Messages, Google Gemini). These live in `internal/provider/openai/`, `internal/provider/anthropic/`, and `internal/provider/google/`.
2. **Vendor Adapters** — apply vendor-specific defaults (thinking format, cache control, default API) on top of a protocol provider. These live in `internal/provider/vendor_*.go`.

Most new providers only need a vendor adapter file. You only need a new protocol provider if the API wire format is fundamentally different from existing ones.

## Quick Start: Adding a Vendor Adapter

### Step 1: Create the vendor file

Create `internal/provider/vendor_<name>.go`:

```go
package provider

func init() {
    RegisterVendorAdapter(simpleVendorAdapter{
        name:           "my-vendor",
        domains:        []string{"api.myvendor.com"},
        thinkingFormat: "myvendor",  // optional
        defaultAPI:     "openai-chat", // optional
        cacheControl:   nil,          // optional: boolPtr(true) to enable
    })
}
```

### Step 2: Add tests (if needed)

Vendor adapters are tested through `vendor_test.go`. If your adapter has special behavior beyond the simple adapter, add a test case there.

That's it. The `init()` function registers the adapter automatically.

## Vendor Adapter Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | `string` | Unique vendor name (lowercase, e.g. `"deepseek"`, `"google-gemini"`) |
| `domains` | `[]string` | URL substrings for auto-detection from base URL |
| `thinkingFormat` | `string` | Thinking/reasoning format: `"deepseek"`, `"anthropic"`, `"openai"`, `"qwen"`, `"zai"`, `"xiaomi"`, etc. |
| `defaultAPI` | `string` | Default protocol: `"openai-chat"`, `"anthropic-messages"`, `"google-gemini"`, `"google-vertex"` |
| `cacheControl` | `*bool` | Enable/disable cache control. Use `boolPtr(true)` to enable. |

## How Vendor Detection Works

When a user configures a provider in `settings.json`:

```json
{
  "providers": {
    "my-provider": {
      "vendor": "my-vendor",
      "baseUrl": "https://api.myvendor.com/v1",
      "apiKey": "sk-...",
      "api": "openai-chat",
      "models": [
        { "id": "my-model", "name": "My Model", "contextWindow": 128000 }
      ]
    }
  },
  "defaultProvider": "my-provider",
  "defaultModel": "my-model"
}
```

`providers` is an object keyed by provider ID; use `baseUrl`/`apiKey`, not
`base_url`/`api_key`. A provider ID is the name you pass to `--provider`.

The resolution flow is:

1. **Explicit vendor field** — if `vendor` is set in config, use that adapter directly
2. **Base URL matching** — scan registered adapters' `domains` for a substring match
3. **Protocol fallback** — if the URL contains "anthropic", use `anthropic-messages`; otherwise `openai-chat`

### Example: Multiple Domain Aliases

Some vendors have multiple API domains:

```go
func init() {
    RegisterVendorAdapter(simpleVendorAdapter{
        name:    "my-vendor",
        domains: []string{"api.myvendor.com", "api.v2.myvendor.com"},
    })
}
```

### Example: Multiple Adapters per Vendor

When a vendor has distinct regional endpoints with different behavior:

```go
func init() {
    RegisterVendorAdapter(simpleVendorAdapter{
        name:           "myvendor-us",
        domains:        []string{"us.api.myvendor.com"},
        thinkingFormat: "myvendor",
    })
    RegisterVendorAdapter(simpleVendorAdapter{
        name:           "myvendor-eu",
        domains:        []string{"eu.api.myvendor.com"},
        thinkingFormat: "myvendor",
    })
}
```

## Protocol Provider Details

### OpenAI-Compatible (`openai-chat`)

Most vendors use this. The provider sends requests to `/chat/completions` with streaming SSE responses. Supports function calling, tool use, and structured outputs.

### OpenAI Responses (`openai-responses`)

For OpenAI's Responses API. Uses `POST /v1/responses` and supports streaming responses, native response-item replay, reasoning, structured output, hosted tools, and prompt-cache controls.

`responses.stateMode` controls how follow-up requests retain context: use `replay` for local history replay, `previous_response_id` to continue an upstream response lineage, or `conversation` with a provider conversation ID. With `responses.background: true`, `mothx serve` can submit supported work as a durable remote task, poll it, recover it after a server restart, and execute local function/custom tools as required.

### Anthropic-Compatible (`anthropic-messages`)

For Anthropic's Messages API. Uses `/v1/messages` with SSE streaming. Supports extended thinking and cache control.

### Google Gemini (`google-gemini`)

For Google's Generative Language API. Uses `streamGenerateContent` with SSE streaming.

### Google Vertex (`google-vertex`)

For Google Cloud Vertex AI. Similar to Gemini but with GCP authentication.

## When to Add a Protocol Provider

You need a new protocol provider only when:

- The API wire format is fundamentally different from OpenAI/Anthropic/Google
- The streaming protocol is not SSE-based
- Authentication requires a custom flow (e.g., OAuth2 with token refresh)

To add a protocol provider:

1. Create a new directory under `internal/provider/<name>/`
2. Implement the `Provider` interface:
   ```go
   type Provider interface {
       Chat(ctx context.Context, params ChatParams) <-chan StreamEvent
       Name() string
       Models() []*Model
       GetModel(id string) *Model
   }
   ```
3. Register a factory in `internal/provider/registry.go`:
   ```go
   func init() {
       Register("my-protocol", func(cfg *config.ProviderConfig) (Provider, error) {
           return myprovider.New(cfg)
       })
   }
   ```

## Testing

Run vendor adapter tests:

```bash
go test ./internal/provider/ -run TestVendor
```

Run the full provider test suite:

```bash
go test ./internal/provider/...
```

## Checklist

- [ ] Created `internal/provider/vendor_<name>.go`
- [ ] Vendor name is lowercase and unique
- [ ] `domains` list covers all known API endpoints
- [ ] `thinkingFormat` set if the vendor supports extended thinking
- [ ] `defaultAPI` set if the vendor uses a non-OpenAI protocol
- [ ] Tests pass: `go test ./internal/provider/...`
- [ ] No changes to `settings.json` schema needed (vendor adapters are transparent)
