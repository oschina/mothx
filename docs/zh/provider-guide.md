# 添加新的 Provider

本指南介绍如何为 MothX 添加新的 LLM 供应商（vendor）支持。

## 架构概览

MothX 在添加 Provider 支持时区分两个层面：

1. **协议 Provider** — 处理底层通信协议（OpenAI Chat Completions、Anthropic Messages、Google Gemini）。位于 `internal/provider/openai/`、`internal/provider/anthropic/`、`internal/provider/google/`。
2. **供应商适配器** — 在协议 Provider 之上应用供应商特定的默认值（思考格式、缓存控制、默认 API）。位于 `internal/provider/vendor_*.go`。

大多数新供应商只需要一个适配器文件。只有当 API 协议与现有的根本不同时，才需要新建协议 Provider。

## 快速开始：添加供应商适配器

### 第一步：创建适配器文件

创建 `internal/provider/vendor_<name>.go`：

```go
package provider

func init() {
    RegisterVendorAdapter(simpleVendorAdapter{
        name:           "my-vendor",
        domains:        []string{"api.myvendor.com"},
        thinkingFormat: "myvendor",    // 可选
        defaultAPI:     "openai-chat", // 可选
        cacheControl:   nil,           // 可选：boolPtr(true) 启用
    })
}
```

### 第二步：添加测试（如需要）

供应商适配器通过 `vendor_test.go` 测试。如果适配器有超出简单适配器的特殊行为，在那里添加测试用例。

完成。`init()` 函数会自动注册适配器。

## 适配器字段说明

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | `string` | 唯一的供应商标识（小写，如 `"deepseek"`、`"google-gemini"`） |
| `domains` | `[]string` | 用于从 base URL 自动检测的子串匹配列表 |
| `thinkingFormat` | `string` | 思考/推理格式：`"deepseek"`、`"anthropic"`、`"openai"`、`"qwen"`、`"zai"` 等 |
| `defaultAPI` | `string` | 默认协议：`"openai-chat"`、`"anthropic-messages"`、`"google-gemini"`、`"google-vertex"` |
| `cacheControl` | `*bool` | 启用/禁用缓存控制。使用 `boolPtr(true)` 启用 |

## 供应商检测流程

当用户在 `settings.json` 中配置 Provider 时：

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

`providers` 是以 provider ID 为键的对象；字段名是 `baseUrl`/`apiKey`，不是
`base_url`/`api_key`。provider ID 就是传给 `--provider` 的名称。

解析流程为：

1. **显式 vendor 字段** — 如果配置中设置了 `vendor`，直接使用该适配器
2. **Base URL 匹配** — 扫描已注册适配器的 `domains` 进行子串匹配
3. **协议回退** — 如果 URL 包含 "anthropic"，使用 `anthropic-messages`；否则使用 `openai-chat`

### 示例：多域名别名

一些供应商有多个 API 域名：

```go
func init() {
    RegisterVendorAdapter(simpleVendorAdapter{
        name:    "my-vendor",
        domains: []string{"api.myvendor.com", "api.v2.myvendor.com"},
    })
}
```

### 示例：单供应商多适配器

当供应商的不同区域端点有不同行为时：

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

## 协议 Provider 详解

### OpenAI 兼容（`openai-chat`）

大多数供应商使用此协议。向 `/chat/completions` 发送请求，SSE 流式响应。支持函数调用、工具使用和结构化输出。

### OpenAI Responses（`openai-responses`）

用于 OpenAI 的 Responses API。使用 `POST /v1/responses`，支持流式响应、原生 response item 回放、reasoning、结构化输出、hosted tools 和 prompt cache 控制。

`responses.stateMode` 决定后续请求如何保留上下文：`replay` 使用本地历史回放，`previous_response_id` 延续上游 response lineage，`conversation` 则使用 provider conversation ID。设置 `responses.background: true` 后，`mothx serve` 可将支持的工作提交为可持久恢复的远程任务，轮询任务状态、在服务重启后恢复，并按需执行本地 function/custom tool。

### Anthropic 兼容（`anthropic-messages`）

用于 Anthropic 的 Messages API。使用 `/v1/messages` 和 SSE 流式传输。支持扩展思考和缓存控制。

### Google Gemini（`google-gemini`）

用于 Google 的 Generative Language API。使用 `streamGenerateContent` 和 SSE 流式传输。

### Google Vertex（`google-vertex`）

用于 Google Cloud Vertex AI。与 Gemini 类似但使用 GCP 认证。

## 何时需要新建协议 Provider

仅在以下情况需要：

- API 协议与 OpenAI/Anthropic/Google 根本不同
- 流式协议不是基于 SSE
- 认证需要自定义流程（如带 token 刷新的 OAuth2）

添加协议 Provider 的步骤：

1. 在 `internal/provider/<name>/` 下创建新目录
2. 实现 `Provider` 接口：
   ```go
   type Provider interface {
       Chat(ctx context.Context, params ChatParams) <-chan StreamEvent
       Name() string
       Models() []*Model
       GetModel(id string) *Model
   }
   ```
3. 在 `internal/provider/registry.go` 中注册工厂：
   ```go
   func init() {
       Register("my-protocol", func(cfg *config.ProviderConfig) (Provider, error) {
           return myprovider.New(cfg)
       })
   }
   ```

## 测试

运行供应商适配器测试：

```bash
go test ./internal/provider/ -run TestVendor
```

运行完整的 Provider 测试套件：

```bash
go test ./internal/provider/...
```

## 提交检查清单

- [ ] 已创建 `internal/provider/vendor_<name>.go`
- [ ] 供应商名称为小写且唯一
- [ ] `domains` 列表覆盖所有已知的 API 端点
- [ ] 如果供应商支持扩展思考，已设置 `thinkingFormat`
- [ ] 如果供应商使用非 OpenAI 协议，已设置 `defaultAPI`
- [ ] 测试通过：`go test ./internal/provider/...`
- [ ] 无需修改 `settings.json` schema（供应商适配器是透明的）
