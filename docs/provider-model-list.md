# VibeCoding 供应商/模型完整配置表

> 数据来源：`internal/config/settings.go` (defaultProviderConfigs) + `internal/provider/vendor_*.go`

## API 类型说明

| API 协议 | 说明 |
|----------|------|
| `anthropic-messages` | Anthropic Messages API（原生协议） |
| `openai-chat` | OpenAI Chat Completions API（兼容协议） |
| `openai-responses` | OpenAI Responses API（o1/o3 等模型专用） |
| `google-gemini` | Google Gemini API（原生协议） |
| `google-vertex` | Google Vertex AI API（原生协议） |

## ThinkingFormat 说明

| Format | 适用供应商 |
|--------|-----------|
| `anthropic` | Anthropic（extended thinking） |
| `deepseek` | DeepSeek（reasoning_content 字段） |
| `openai` | OpenAI o1/o3（reasoning_effort） |
| `xiaomi` | 小米 MiMo（reasoning_content 格式） |
| `zai` | 智谱 GLM（思考模式格式） |
| `kimi` | Kimi Coding（reasoning_content 格式） |
| `qwen` | Qwen 3.6/3.7/3.8（enable_thinking + thinking_budget） |
| `doubao-seed` | 豆包 Seed 2.1 / Evolving（reasoning_effort：minimal/low/medium/high） |
| 空（默认） | 使用标准 OpenAI thinking 或原生协议 |

---

## 完整供应商列表

### 1. Anthropic（官方）

- **Vendor**: `anthropic`
- **BaseURL**: `https://api.anthropic.com`
- **API**: `anthropic-messages`
- **ThinkingFormat**: `anthropic`
- **API Key**: `${ANTHROPIC_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| claude-3-5-haiku-20241022 | 200K | 8K | 否 | text,image |
| claude-3-5-haiku-latest | 200K | 8K | 否 | text,image |
| claude-3-5-sonnet-20240620 | 200K | 8K | 否 | text,image |
| claude-3-5-sonnet-20241022 | 200K | 8K | 否 | text,image |
| claude-3-7-sonnet-20250219 | 200K | 64K | **是** | text,image |
| claude-3-haiku-20240307 | 200K | 4K | 否 | text,image |
| claude-3-opus-20240229 | 200K | 4K | 否 | text,image |
| claude-3-sonnet-20240229 | 200K | 4K | 否 | text,image |
| claude-fable-5 | 1M | 128K | **是** | text,image |
| claude-haiku-4-5 / claude-haiku-4-5-20251001 | 200K | 64K | **是** | text,image |
| claude-opus-4-0 / claude-opus-4-20250514 | 200K | 32K | **是** | text,image |
| claude-opus-4-1 / claude-opus-4-1-20250805 | 200K | 32K | **是** | text,image |
| claude-opus-4-5 / claude-opus-4-5-20251101 | 200K | 64K | **是** | text,image |
| claude-opus-4-6 | 1M | 128K | **是** | text,image |
| claude-opus-4-7 | 1M | 128K | **是** | text,image |
| claude-opus-4-8 | 1M | 128K | **是** | text,image |
| claude-sonnet-4-0 / claude-sonnet-4-20250514 | 200K | 64K | **是** | text,image |
| claude-sonnet-4-5 / claude-sonnet-4-5-20250929 | 200K | 64K | **是** | text,image |
| claude-sonnet-4-6 | 1M | 64K | **是** | text,image |

---

### 2. OpenAI（官方）

- **Vendor**: `openai`
- **BaseURL**: `https://api.openai.com/v1`
- **API**: `openai-responses`
- **ThinkingFormat**: `openai`
- **API Key**: `${OPENAI_API_KEY}`
- **Headers**: `User-Agent: codex_cli_rs/0.144.4`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| gpt-4 | 8K | 8K | 否 | text |
| gpt-4-turbo | 128K | 4K | 否 | text,image |
| gpt-4.1 | 1M | 32K | 否 | text,image |
| gpt-4.1-mini | 1M | 32K | 否 | text,image |
| gpt-4.1-nano | 1M | 32K | 否 | text,image |
| gpt-4o / gpt-4o-2024-08-06 / gpt-4o-2024-11-20 | 128K | 16K | 否 | text,image |
| gpt-4o-2024-05-13 | 128K | 4K | 否 | text,image |
| gpt-4o-mini | 128K | 16K | 否 | text,image |
| gpt-5 / gpt-5-codex | 400K | 128K | **是** | text,image |
| gpt-5-chat-latest | 128K | 16K | 否 | text,image |
| gpt-5-mini / gpt-5-nano | 400K | 128K | **是** | text,image |
| gpt-5-pro | 400K | 128K | **是** | text,image |
| gpt-5.1 / gpt-5.1-codex / gpt-5.1-codex-max / gpt-5.1-codex-mini | 400K | 128K | **是** | text,image |
| gpt-5.1-chat-latest | 128K | 16K | **是** | text,image |
| gpt-5.2 / gpt-5.2-codex | 400K | 128K | **是** | text,image |
| gpt-5.2-chat-latest | 128K | 16K | **是** | text,image |
| gpt-5.2-pro | 400K | 128K | **是** | text,image |
| gpt-5.3-chat-latest | 128K | 16K | 否 | text,image |
| gpt-5.3-codex | 400K | 128K | **是** | text,image |
| gpt-5.3-codex-spark | 128K | 32K | **是** | text,image |
| gpt-5.4 / gpt-5.4-mini / gpt-5.4-nano | 272K~400K | 128K | **是** | text,image |
| gpt-5.4-pro | 1.05M | 128K | **是** | text,image |
| gpt-5.5 | 272K | 128K | **是** | text,image |
| gpt-5.5-pro | 1.05M | 128K | **是** | text,image |
| gpt-5.6-sol / gpt-5.6-terra / gpt-5.6-luna | 未公开 | 未公开 | **是** | text,image |
| o1 / o1-pro | 200K | 100K | **是** | text,image |
| o3 / o3-deep-research / o3-pro | 200K | 100K | **是** | text,image |
| o3-mini | 200K | 100K | **是** | text |
| o4-mini / o4-mini-deep-research | 200K | 100K | **是** | text,image |

---

### 2a. CodeOK

- **Vendor**: `codeok`
- **BaseURL**: `https://www.codeok.cc/v1`
- **API**: `openai-responses`
- **API Key**: `${CODEOK_API_KEY}`
- **Headers**: `User-Agent: codex_cli_rs/0.144.4`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| gpt-5.5 | 272K | 128K | **是** | text,image |
| gpt-5.6-sol / gpt-5.6-terra / gpt-5.6-luna | 未公开 | 未公开 | **是** | text,image |

---

### 2b. YesCode

- **Vendor**: `yescode`
- **BaseURL**: `https://co.yes.vg/v1`
- **API**: `openai-responses`
- **API Key**: `${YESCODE_API_KEY}`
- **Headers**: `User-Agent: codex_cli_rs/0.144.4`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| gpt-5.5 | 272K | 128K | **是** | text,image |
| gpt-5.6-sol / gpt-5.6-terra / gpt-5.6-luna | 未公开 | 未公开 | **是** | text,image |

---

### 3. DeepSeek（官方）

#### 3a. DeepSeek Anthropic 模式

- **Vendor**: `deepseek`
- **BaseURL**: `https://api.deepseek.com/anthropic`
- **API**: `anthropic-messages`
- **ThinkingFormat**: `deepseek`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| deepseek-v4-flash | 1M | 384K | **是** | text |
| deepseek-v4-pro | 1M | 384K | **是** | text |
| deepseek-v4-flash-vision-exp | 1M | - | **是** | text,image |

#### 3b. DeepSeek OpenAI 模式

- **Vendor**: `deepseek`
- **BaseURL**: `https://api.deepseek.com`
- **API**: `openai-chat`
- **ThinkingFormat**: `deepseek`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| deepseek-v4-flash | 1M | 384K | **是** | text |
| deepseek-v4-pro | 1M | 384K | **是** | text |
| deepseek-v4-flash-vision-exp | 1M | - | **是** | text,image |

---

### 4. Google Gemini

- **Vendor**: `google-gemini`
- **BaseURL**: `https://generativelanguage.googleapis.com/v1beta/models`
- **API**: `google-gemini`
- **API Key**: `${GOOGLE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| gemini-2.0-flash | 1M | 8K | 否 | text,image |
| gemini-2.0-flash-lite | 1M | 8K | 否 | text,image |
| gemini-2.5-flash | 1M | 64K | **是** | text,image |
| gemini-2.5-flash-lite | 1M | 64K | **是** | text,image |
| gemini-2.5-pro | 1M | 64K | **是** | text,image |
| gemini-3-flash-preview | 1M | 64K | **是** | text,image |
| gemini-3-pro-preview | 1M | 64K | **是** | text,image |
| gemini-3.1-flash-lite / preview | 1M | 64K | **是** | text,image |
| gemini-3.1-pro-preview / customtools | 1M | 64K | **是** | text,image |
| gemini-3.5-flash | 1M | 64K | **是** | text,image |
| gemini-flash-latest | 1M | 64K | **是** | text,image |
| gemini-flash-lite-latest | 1M | 64K | **是** | text,image |
| gemma-4-26b-a4b-it | 262K | 32K | **是** | text,image |
| gemma-4-31b-it | 262K | 32K | **是** | text,image |

---

### 5. Google Vertex AI

- **Vendor**: `google-vertex`
- **BaseURL**: `https://aiplatform.googleapis.com/v1/publishers/google/models`
- **API**: `google-vertex`
- **API Key**: `${GOOGLE_CLOUD_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| gemini-2.5-flash | 1M | 64K | **是** | text,image |
| gemini-2.5-flash-lite | 1M | 64K | **是** | text,image |
| gemini-2.5-pro | 1M | 64K | **是** | text,image |
| gemini-3-flash-preview | 1M | 64K | **是** | text,image |
| gemini-3.1-flash-lite | 1M | 64K | **是** | text,image |
| gemini-3.1-pro-preview / customtools | 1M | 64K | **是** | text,image |
| gemini-3.5-flash | 1M | 64K | **是** | text,image |
| gemini-flash-latest | 1M | 64K | **是** | text,image |
| gemini-flash-lite-latest | 1M | 64K | **是** | text,image |

---

### 6. 小米 MiMo

- **Vendor**: `xiaomi`
- **BaseURL**: `https://api.xiaomimimo.com/v1`
- **API**: `openai-chat`
- **ThinkingFormat**: `xiaomi`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| mimo-v2.5 | 1M | 128K | **是** | text,image |
| mimo-v2.5-pro | 1M | 128K | **是** | text |
| mimo-v2.5-pro-ultraspeed | 1M | 128K | **是** | text |
| mimo-v2.6-pro | 1M | 128K | **是** | text,image |
| mimo-v2.6-flash | 1M | 128K | **是** | text,image |

#### MiMo Token Plan（AMS/CN/SGP）

- **BaseURL**: `https://token-plan-{ams,cn,sgp}.xiaomimimo.com/v1`
- **API**: `openai-chat`
- **ThinkingFormat**: `xiaomi`

> 与上方同款模型，按区域分为 AMS（亚太）、CN（中国）、SG（新加坡）

---

### 7. 火山引擎（Volcengine）

#### 7a. 标准版

- **Vendor**: `volcengine`
- **BaseURL**: `https://ark.cn-beijing.volces.com/api/v3`
- **API**: `openai-chat`
- **API Key**: `${VOLCENGINE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | reasoning_effort |
|------|---------|-----------|------|------|-----------------|
| doubao-seed-2-1-turbo-260628 | 262K | 262K | 否 | text | minimal/low/medium/high |
| doubao-seed-evolving | 262K | 262K | 否 | text,image | minimal/low/medium/high |
| doubao-seed-2-1-pro-260628 | 262K | 262K | 否 | text,image | minimal/low/medium/high |
| glm-5.3 | 1M | 不传 | **是** | text |
| glm-5.3-flash | 1M | 不传 | **是** | text,image |
| deepseek-v4.1-flash | 1M | 262K | **是** | text,image |

#### 7b. AgentPlan（规划模式）

- **Vendor**: `volcengine-agentplan`
- **BaseURL**: `https://ark.cn-beijing.volces.com/api/plan/v3`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 | reasoning_effort |
|------|---------|-----------|------|------|-----------------|
| ark-code-latest | 262K | 100K | **是** | text | |
| doubao-seed-2.1-turbo | 262K | 100K | **是** | text | minimal/low/medium/high |
| doubao-seed-2-0-lite/mini | 262K | 100K | **是** | text | |
| doubao-seed-evolving | 1M | 100K | **是** | text,image | minimal/low/medium/high |
| glm-5.3 | 1M | 不传 | **是** | text |
| glm-5.3-flash | 1M | 不传 | **是** | text,image |
| kimi-k2.7-code | 262K | 100K | **是** | text |
| deepseek-v4-pro | 1M | 100K | **是** | text |
| deepseek-v4-flash | 1M | 100K | **是** | text,image |
| deepseek-v4.1-flash | 1M | 100K | **是** | text,image |
| minimax-m3 | 1M | 100K | **是** | text,image |
| minimax-m2.7 | 262K | 100K | **是** | text |
| kimi-k3 | 1M | 100K | **是** | text,image |
| kimi-k2.6 | 262K | 100K | **是** | text,image |

#### 7c. CodingPlan（编码模式）

- **Vendor**: `volcengine-codingplan`
- **BaseURL**: `https://ark.cn-beijing.volces.com/api/coding/v3`
- **API**: `openai-chat`

> 模型列表同 AgentPlan（不含 kimi-k2.6 和 minimax-m2.7）；除 `glm-5.3` 与 `glm-5.3-flash` 外，其余模型 MaxTokens 均为 100K，`glm-5.3`/`glm-5.3-flash` 不传 MaxTokens。AgentPlan 与 CodingPlan 均提供 `deepseek-v4.1-flash`（1M 上下文、text+image 输入）。

---

### 8. OpenRouter

- **Vendor**: `openrouter`
- **BaseURL**: `https://openrouter.ai/api/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| anthropic/claude-sonnet-4.6 | 1M | 64K | **是** | text,image |
| anthropic/claude-opus-4.8 | 1M | 128K | **是** | text,image |
| anthropic/claude-sonnet-4.5 | 1M | 64K | **是** | text,image |
| anthropic/claude-haiku-4.5 | 200K | 64K | **是** | text,image |
| openai/gpt-5.5 | 1.05M | 128K | **是** | text,image |
| openai/gpt-5.5-pro | 1.05M | 128K | **是** | text,image |
| openai/gpt-5.4 | 1.05M | 128K | **是** | text,image |
| google/gemini-3.5-flash | 1M | 64K | **是** | text,image |
| google/gemini-2.5-pro | 1M | 64K | **是** | text,image |
| deepseek/deepseek-v4-flash | 1M | 64K | **是** | text |
| deepseek/deepseek-v4-pro | 1M | 384K | **是** | text |
| qwen/qwen3.7-plus | 1M | 64K | **是** | text,image |
| moonshotai/kimi-k2.7-code | 262K | 262K | **是** | text |
| minimax/minimax-m3 | 1M | 4K | **是** | text,image |
| meta-llama/llama-4-scout | 10M | 16K | 否 | text,image |
| z-ai/glm-5 | 200K | 4K | **是** | text |
| z-ai/glm-5.3 | 1M | 128K | **是** | text |
| x-ai/grok-4.3 | 1M | 4K | **是** | text,image |
| openai/gpt-oss-120b:free | 128K | 128K | **是** | text |

---

### 9. MiniMax

#### 9a. 通用版（OpenAI 兼容）

- **Vendor**: `minimax`
- **BaseURL**: `https://api.minimax.io/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| MiniMax-M3 | 1M | 128K | 否 | text,image,video |
| MiniMax-M2.7 | 204K | 128K | 否 | text |
| MiniMax-M2.7-highspeed | 204K | 128K | 否 | text |
| MiniMax-M2.5 | 196K | 128K | 否 | text |
| MiniMax-M2.5-highspeed | 196K | 128K | 否 | text |

#### 9b. Anthropic 模式（/minimax-anthropic）

- **BaseURL**: `https://api.minimax.io/anthropic`
- **API**: `anthropic-messages`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| MiniMax-M2.7 | 204K | 128K | **是** | text |
| MiniMax-M2.7-highspeed | 204K | 128K | **是** | text |
| MiniMax-M3 | 512K | 128K | **是** | text,image |

#### 9c. 国内 Anthropic 模式（/minimax-cn-anthropic）

- **BaseURL**: `https://api.minimaxi.com/anthropic`
- **API**: `anthropic-messages`

> 模型同上

---

### 10. 智谱 AI（Z.AI）

#### 10a. 国际版

- **Vendor**: `zai`
- **BaseURL**: `https://api.z.ai/api/coding/paas/v4`
- **API**: `openai-chat`
- **ThinkingFormat**: `zai`

#### 10b. 国内 Coding

- **Vendor**: `zai`
- **BaseURL**: `https://open.bigmodel.cn/api/coding/paas/v4`
- **API**: `openai-chat`
- **ThinkingFormat**: `zai`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| glm-4.5-air | 128K | 98K | **是** | text |
| glm-4.7 | 204K | 128K | **是** | text |
| glm-5-turbo | 200K | 128K | **是** | text |
| glm-5.1 | 200K | 128K | **是** | text |
| glm-5.3 | 1M | 128K | **是** | text |
| glm-5.3-flash | 1M | 128K | **是** | text,image |
| glm-5v-turbo | 200K | 128K | **是** | text,image |

---

### 11. ModelScope（魔搭社区）

- **BaseURL**: `https://api-inference.modelscope.cn/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| deepseek-ai/DeepSeek-V4-Flash-0731 | 1M | 384K | **是** | text |
| deepseek-ai/DeepSeek-V4-Pro | 1M | 384K | **是** | text |
| deepseek-ai/DeepSeek-V4-Pro-0813 | 1M | 384K | **是** | text |
| MedAIBase/AntAngelMed | 128K | 16K | 否 | text |
| meituan-longcat/LongCat-Flash-Lite | 256K | 32K | 否 | text |
| MiniMax/MiniMax-M1-80k | 1M | 80K | **是** | text |
| MiniMax/MiniMax-M3 | 1M | 128K | **是** | text,image,video |
| mistralai/Mistral-Large-Instruct-2407 | 128K | 32K | 否 | text |
| MusePublic/Qwen-Image-Edit | 32K | 16K | 否 | text,image |
| opencompass/CompassJudger-1-32B-Instruct | 16K | 4K | 否 | text |
| OpenGVLab/InternVL3_5-241B-A28B | 64K | 16K | 否 | text,image,video |
| PaddlePaddle/ERNIE-4.5-0.3B-PT | 128K | 64K | 否 | text |
| PaddlePaddle/ERNIE-4.5-21B-A3B-PT | 128K | 64K | 否 | text |
| PaddlePaddle/ERNIE-4.5-300B-A47B-PT | 128K | 64K | 否 | text |
| PaddlePaddle/ERNIE-4.5-VL-28B-A3B-PT | 128K | 64K | 否 | text,image |
| Qwen/Qwen-Image-Edit | 32K | 16K | 否 | text,image |
| Qwen/Qwen3-14B | 128K | 38K | 否 | text |
| Qwen/Qwen3-235B-A22B | 128K | 38K | 否 | text |
| Qwen/Qwen3-235B-A22B-Instruct-2507 | 256K | 64K | 否 | text |
| Qwen/Qwen3-235B-A22B-Thinking-2507 | 256K | 80K | **是** | text |
| Qwen/Qwen3-30B-A3B | 128K | 38K | 否 | text |
| Qwen/Qwen3-30B-A3B-Thinking-2507 | 256K | 80K | **是** | text |
| Qwen/Qwen3-4B | 128K | 38K | 否 | text |
| Qwen/Qwen3-8B | 128K | 38K | 否 | text |
| Qwen/Qwen3-Coder-30B-A3B-Instruct | 256K | 64K | 否 | text |
| Qwen/Qwen3-Next-80B-A3B-Instruct | 256K | 64K | 否 | text |
| Qwen/Qwen3-Next-80B-A3B-Thinking | 256K | 80K | **是** | text |
| Qwen/Qwen3-VL-235B-A22B-Instruct | 256K | 32K | 否 | text,image,video |
| Qwen/Qwen3-VL-8B-Instruct | 256K | 32K | 否 | text,image,video |
| Qwen/Qwen3-VL-8B-Thinking | 256K | 40K | **是** | text,image,video |
| Qwen/Qwen3.5-122B-A10B | 256K | 80K | **是** | text,image,video |
| Qwen/Qwen3.5-27B | 256K | 80K | **是** | text,image,video |
| Qwen/Qwen3.5-35B-A3B | 256K | 80K | **是** | text,image,video |
| Qwen/Qwen3.5-397B-A17B | 1M | 130K | **是** | text |
| Qwen/Qwen3.8-27B | 256K | 128K | **是** | text,image,video |
| Shanghai_AI_Laboratory/Intern-S1 | 128K | 32K | **是** | text,image,video |
| Shanghai_AI_Laboratory/Intern-S1-mini | 128K | 32K | **是** | text,image,video |
| Shanghai_AI_Laboratory/Intern-S2-Preview | 128K | 32K | **是** | text,image |
| stepfun-ai/Step-3.5-Flash | 256K | 32K | **是** | text |
| stepfun-ai/Step-3.7-Flash | 256K | 32K | **是** | text,image |
| Tencent-Hunyuan/Hy3 | 256K | 128K | **是** | text |
| XGenerationLab/XiYanSQL-QwenCoder-32B-2412 | 32K | 16K | 否 | text |
| XGenerationLab/XiYanSQL-QwenCoder-32B-2504 | 32K | 16K | 否 | text |
| ZhipuAI/GLM-4.7-Flash | 256K | 128K | **是** | text |
| ZhipuAI/GLM-5.2 | 1M | 128K | **是** | text |

---

### 12. 阿里云百炼（Alibaba Bailian）

#### 12a. Coding Plan（编码计划）

- **Vendor**: `bailian`
- **BaseURL**: `https://coding.dashscope.aliyuncs.com/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| qwen3.5-plus | 1M | 64K | **是** | text,image,video |
| qwen3.6-plus | 1M | 64K | **是** | text,image,video |
| qwen3.7-plus | 1M | 64K | **是** | text,image |
| glm-5 | 200K | 32K | **是** | text |
| kimi-k2.5 | 262K | 262K | **是** | text,image,video |
| MiniMax-M2.5 | 196K | 128K | **是** | text |
| qwen3-coder-plus | 1M | 64K | 否 | text |
| qwen3-coder-next | 262K | 64K | 否 | text |
| qwen3-max-2026-01-23 | 262K | 64K | **是** | text |
| glm-4.7 | 200K | 128K | **是** | text |

#### 12b. Token Plan（Token 计划）

- **Vendor**: `bailian`
- **BaseURL**: `https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| qwen3.8-max-preview | 1M | 64K | **是** | text |
| qwen3.6-plus | 1M | 64K | **是** | text,image |
| qwen3.7-plus | 1M | 64K | **是** | text,image |
| qwen3.7-max | 1M | 64K | **是** | text |
| qwen3.6-flash | 1M | 64K | **是** | text,image |
| deepseek-v4-pro | 1M | 384K | 否 | text |
| deepseek-v4-flash | 1M | 384K | 否 | text |
| deepseek-v3.2 | 128K | 64K | **是** | text |
| kimi-k2.6 | 262K | 262K | **是** | text,image,video |
| kimi-k2.5 | 262K | 262K | **是** | text,image,video |
| glm-5.1 | 200K | 128K | **是** | text |
| glm-5.3 | 1M | 128K | **是** | text |
| glm-5 | 200K | 32K | **是** | text |
| MiniMax-M2.5 | 196K | 128K | 否 | text |

#### 12c. 标准版

- **Vendor**: `bailian`
- **BaseURL**: `https://dashscope.aliyuncs.com/compatible-mode/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| qwen3.6-plus | 1M | 64K | **是** | text,image |
| qwen3.7-plus | 1M | 64K | **是** | text,image |
| qwen3.7-max | 1M | 64K | **是** | text |
| glm-5.1 | 200K | 128K | **是** | text |
| deepseek-v4-pro | 1M | 384K | **是** | text,image,video |
| deepseek-v4-flash | 1M | 384K | 否 | text |

---

### 13. 华为云（Huawei ModelArts）

#### 13a. 标准版

- **Vendor**: `huawei`
- **BaseURL**: `https://api.modelarts-maas.com/openai/v1`
- **API**: `openai-chat`
- **API Key**: `${HUAWEI_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| openpangu-2.0-flash | 512K | 128K | **是** | text,image |
| glm-5.3 | 198K | 128K | **是** | text |
| glm-5.1 | 198K | 128K | **是** | text,image |
| kimi-k2.6 | 256K | 96K | **是** | text,image |
| glm-5 | 198K | 64K | **是** | text,image |
| deepseek-v4-pro | 1M | 128K | **是** | text |
| deepseek-v4-flash | 1M | 128K | **是** | text |
| qwen3-235b-a22b | 128K | 32K | **是** | text,image |

#### 13b. Plan 模式

- **Vendor**: `huawei-plan`
- **BaseURL**: `https://api.modelarts-maas.com/plan/v2`
- **API**: `openai-chat`
- **API Key**: `${HUAWEI_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| glm-5 | 198K | 64K | **是** | text,image |
| glm-5.1 | 198K | 128K | **是** | text,image |
| kimi-k2.6 | 256K | 96K | **是** | text,image |
| deepseek-v3.2 | 128K | 64K | **是** | text |
| deepseek-v4-flash | 1M | 128K | **是** | text |

### 14. Gitee AI / Moark

#### 14a. Gitee AI

- **Vendor**: `gitee`
- **BaseURL**: `https://ai.gitee.com/v1`
- **API**: `openai-chat`
- **API Key**: `${GITEE_API_KEY}`

#### 14b. Moark

- **Vendor**: `gitee`
- **BaseURL**: `https://api.moark.com/v1`
- **API**: `openai-chat`
- **API Key**: `${MOARK_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| auto | 1M | - | **是** | text,image |
| glm-5 | 200K | 32K | **是** | text |
| glm-5.1 | 200K | 128K | **是** | text |
| glm-5.3 | 1M | 128K | **是** | text |
| glm-5.3-flash | 1M | 128K | **是** | text,image |
| ernie-5.0-thinking | 128K | 64K | **是** | text |
| qwen3.5-flash | 1M | 64K | **是** | text,image |
| qwen3.6-flash | 1M | 64K | **是** | text,image |
| qwen3.6-plus | 64K | 64K | **是** | text,image |
| qwen3.7-plus | 1M | 64K | **是** | text,image |
| step-3.7-flash | 256K | 16K | 否 | text,image |
| qwen3.7-max | 1M | 64K | **是** | text |
| qwen3.8-max | 1M | - | **是** | text,image |
| qwen3.8-max-0902 | 1M | 128K | **是** | text,image |
| qwen3.8-flash | 1M | - | **是** | text,image |
| qwen3.8-27b | 1M | - | **是** | text,image,video |
| qwen3.8-omni-flash | 1M | 128K | **是** | text,image,audio,video |
| deepseek-v4-flash | 1M | 384K | **是** | text |
| deepseek-v4-flash-0731 | 1M | - | **是** | text |
| deepseek-v4.1-flash | 1M | - | **是** | text,image |
| deepseek-v4-pro | 1M | 384K | **是** | text |
| deepseek-v4-pro-0813 | 1M | - | **是** | text |
| kimi-k2.5 | 262K | 262K | **是** | text,image,video |
| kimi-k2.6 | 262K | 262K | **是** | text,image,video |
| kimi-k2.7-code | 262K | 262K | **是** | text |
| kimi-k3 | 1M | 262K | **是** | text,image |
| minimax-m2.7 | 262K | 128K | **是** | text |
| minimax-m3 | 1M | 128K | **是** | text,image |
| mimo-v2.5-pro | 1M | 128K | **是** | text,image |
| gemma-4-26b-a4b-it | 128K | 32K | **是** | text,image |

---

### 15. 月之暗面（Moonshot / Kimi）

#### 15a. 国际版

- **BaseURL**: `https://api.moonshot.ai/v1`
- **API**: `openai-chat`

#### 15b. 国内版

- **BaseURL**: `https://api.moonshot.cn/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| kimi-k2-0711-preview | 128K | 16K | 否 | text |
| kimi-k2-0905-preview | 262K | 262K | 否 | text |
| kimi-k2-thinking | 262K | 262K | **是** | text |
| kimi-k2-thinking-turbo | 262K | 262K | **是** | text |
| kimi-k2-turbo-preview | 262K | 262K | 否 | text |
| kimi-k2.7-code | 262K | 262K | **是** | text |
| kimi-k2.7-code-highspeed | 262K | 262K | **是** | text |

---

### 16. Kimi Coding

- **BaseURL**: `https://api.kimi.com/coding/v1`
- **API**: `openai-chat`
- **ThinkingFormat**: `kimi`
- **Headers**: `User-Agent: opencode/1.17.18`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| k3 | 1M | 128K | **是** | text,image |
| k3-256k | 256K | - | **是** | text,image |
| kimi-for-coding | 256K | 32K | **是** | text,image |
| kimi-k2-thinking | 262K | 32K | **是** | text |

---

### 17. xAI（Grok）

- **Vendor**: `xai`
- **BaseURL**: `https://api.x.ai/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| grok-3 | 128K | 8K | 否 | text |
| grok-3-fast | 128K | 8K | 否 | text |
| grok-4.20-0309-non-reasoning | 1M | 30K | 否 | text,image |
| grok-4.20-0309-reasoning | 1M | 30K | **是** | text,image |
| grok-4.3 | 1M | 30K | **是** | text,image |
| grok-build-0.1 | 256K | 256K | **是** | text,image |
| grok-code-fast-1 | 32K | 8K | 否 | text |

---

### 18. Fireworks AI

- **Vendor**: `fireworks`
- **BaseURL**: `https://api.fireworks.ai/inference`
- **API**: `anthropic-messages`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| accounts/fireworks/models/deepseek-v4-flash | 1M | 384K | **是** | text |
| accounts/fireworks/models/deepseek-v4-pro | 1M | 384K | **是** | text |
| accounts/fireworks/models/glm-5p1 | 202K | 128K | **是** | text |
| accounts/fireworks/models/kimi-k2p7-code | 262K | 262K | **是** | text |
| accounts/fireworks/routers/kimi-k2p7-code-fast | 262K | 262K | **是** | text |
| accounts/fireworks/models/gpt-oss-120b | 128K | 32K | **是** | text |
| accounts/fireworks/models/gpt-oss-20b | 128K | 32K | **是** | text |

---

### 19. Together AI

- **Vendor**: `together`
- **BaseURL**: `https://api.together.ai/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| MiniMaxAI/MiniMax-M2.7 | 202K | 128K | **是** | text |
| MiniMaxAI/MiniMax-M3 | 524K | 250K | **是** | text,image |
| Qwen/Qwen2.5-7B-Instruct-Turbo | 32K | 32K | 否 | text |
| Qwen/Qwen3-235B-A22B-Instruct-2507-tput | 262K | 262K | 否 | text |
| Qwen/Qwen3.5-397B-A17B | 262K | 130K | **是** | text,image |

---

### 20. Nvidia NIM

- **Vendor**: `nvidia`
- **BaseURL**: `https://integrate.api.nvidia.com/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| meta/llama-3.1-70b-instruct | 128K | 4K | 否 | text |
| meta/llama-3.1-8b-instruct | 16K | 4K | 否 | text |
| meta/llama-3.2-11b-vision-instruct | 128K | 4K | 否 | text,image |
| meta/llama-3.2-90b-vision-instruct | 128K | 8K | 否 | text,image |
| meta/llama-3.3-70b-instruct | 128K | 4K | 否 | text |

---

### 21. Mistral

- **Vendor**: `mistral`
- **BaseURL**: `https://api.mistral.ai/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| codestral-latest | 256K | 4K | 否 | text |
| devstral-2512 / devstral-latest | 262K | 262K | 否 | text |
| devstral-medium-2507 | 128K | 128K | 否 | text |
| devstral-medium-latest | 262K | 262K | 否 | text |
| devstral-small-2505 / 2507 | 128K | 128K | 否 | text |
| labs-devstral-small-2512 | 256K | 256K | 否 | text,image |
| magistral-medium-latest | 128K | 16K | **是** | text |
| magistral-small | 128K | 128K | **是** | text |
| ministral-3b-latest | 128K | 128K | 否 | text |
| ministral-8b-latest | 128K | 128K | 否 | text |
| mistral-large-2411 | 128K | 16K | 否 | text |
| mistral-large-2512 / latest | 262K | 262K | 否 | text,image |
| mistral-medium-2505 | 128K | 128K | 否 | text,image |
| mistral-medium-2508 | 262K | 262K | 否 | text,image |
| mistral-medium-2604 / 3.5 | 262K | 262K | **是** | text,image |
| mistral-medium-latest | 262K | 262K | 否 | text,image |
| mistral-nemo | 128K | 128K | 否 | text |
| mistral-small-2506 | 128K | 16K | 否 | text,image |
| mistral-small-2603 / latest | 256K | 256K | **是** | text,image |
| open-mistral-7b | 8K | 8K | 否 | text |
| open-mistral-nemo | 128K | 128K | 否 | text |
| open-mixtral-8x22b | 64K | 64K | 否 | text |
| open-mixtral-8x7b | 32K | 32K | 否 | text |
| pixtral-12b | 128K | 128K | 否 | text,image |
| pixtral-large-latest | 128K | 128K | 否 | text,image |

---

### 22. HuggingFace

- **Vendor**: `huggingface`
- **BaseURL**: `https://router.huggingface.co/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| MiniMaxAI/MiniMax-M2.1 | 204K | 128K | **是** | text |
| MiniMaxAI/MiniMax-M2.5 | 204K | 128K | **是** | text |
| MiniMaxAI/MiniMax-M2.7 | 204K | 128K | **是** | text |
| Qwen/Qwen3-235B-A22B-Thinking-2507 | 262K | 128K | **是** | text |
| Qwen/Qwen3-Coder-480B-A35B-Instruct | 262K | 66K | 否 | text |

---

### 23. Groq

- **Vendor**: `groq`
- **BaseURL**: `https://api.groq.com/openai/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| llama-3.1-8b-instant | 128K | 128K | 否 | text |
| llama-3.3-70b-versatile | 128K | 32K | 否 | text |
| meta-llama/llama-4-scout-17b-16e-instruct | 128K | 8K | 否 | text,image |
| openai/gpt-oss-120b | 128K | 64K | **是** | text |
| openai/gpt-oss-20b | 128K | 64K | **是** | text |
| openai/gpt-oss-safeguard-20b | 128K | 64K | **是** | text |
| qwen/qwen3-32b | 128K | 40K | **是** | text |

---

### 24. Cerebras

- **Vendor**: `cerebras`
- **BaseURL**: `https://api.cerebras.ai/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| gpt-oss-120b | 128K | 40K | **是** | text |
| zai-glm-4.7 | 128K | 40K | **是** | text |

---

### 25. Cerebras（蚂蚁 Ling）

- **BaseURL**: `https://api.ant-ling.com/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| Ling-2.6-1T | 262K | 64K | 否 | text |
| Ling-2.6-flash | 262K | 64K | 否 | text |
| Ring-2.6-1T | 262K | 64K | **是** | text |

---

### 26. CodePlayz（Opencode）

#### 26a. Claude 代理

- **Vendor**: `opencode`
- **BaseURL**: `https://opencode.ai/zen/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| big-pickle | 200K | 32K | **是** | text |
| claude-haiku-4-5 | 200K | 64K | **是** | text,image |
| claude-opus-4-1 | 200K | 32K | **是** | text,image |
| claude-opus-4-5 | 200K | 64K | **是** | text,image |
| claude-opus-4-6 | 1M | 128K | **是** | text,image |

#### 26b. OpenAI 代理

- **BaseURL**: `https://opencode.ai/zen/go/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| deepseek-v4-flash | 1M | 384K | **是** | text |
| deepseek-v4-pro | 1M | 384K | **是** | text |
| glm-5 / glm-5.1 | 200K | 32K | **是** | text |
| glm-5.3 | 1M | 128K | **是** | text |
| kimi-k2.6 | 262K | 64K | **是** | text,image |
| kimi-k2.7-code | 262K | 262K | **是** | text |

---

### 27. Vercel AI Gateway

- **Vendor**: `vercel-ai-gateway`
- **BaseURL**: `https://ai-gateway.vercel.sh`
- **API**: `anthropic-messages`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| anthropic/claude-sonnet-4.6 | 1M | 64K | **是** | text,image |
| anthropic/claude-opus-4.8 | 1M | 128K | **是** | text,image |
| anthropic/claude-sonnet-4.5 | 1M | 64K | **是** | text,image |
| anthropic/claude-haiku-4.5 | 200K | 64K | **是** | text,image |
| openai/gpt-5.5 / gpt-5.4 | 1.05M~400K | 128K | **是** | text,image |
| google/gemini-3.5-flash | 1M | 64K | **是** | text,image |
| deepseek/deepseek-v4-flash / pro | 1M | 64K~384K | **是** | text |
| alibaba/qwen3.6-plus | 64K | 64K | **是** | text,image |
| minimax/minimax-m3 | 1M | 4K | **是** | text,image |
| moonshotai/kimi-k2.7-code | 262K | 262K | **是** | text |
| xai/grok-4.3 | 1M | 4K | **是** | text,image |
| zai/glm-5.3 | 1M | 128K | **是** | text |

---

### 28. Mistral（GitHub Copilot 代理）

- **Vendor**: `github-copilot`
- **BaseURL**: `https://api.individual.githubcopilot.com`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| claude-sonnet-4.6 | 1M | 32K | **是** | text,image |
| claude-opus-4.8 | 200K | 64K | **是** | text,image |
| claude-sonnet-4.5 | 200K | 32K | **是** | text,image |
| claude-haiku-4.5 | 200K | 64K | **是** | text,image |
| claude-fable-5 | 1M | 128K | **是** | text,image |
| gpt-5.5 | 400K | 128K | **是** | text,image |
| gpt-5.4 | 400K | 128K | **是** | text,image |
| gpt-5.2 | 400K | 128K | **是** | text,image |
| gemini-2.5-pro | 128K | 64K | **是** | text,image |
| gemini-3.5-flash | 200K | 64K | **是** | text,image |

---

### 29. Cloudflare AI Gateway

- **Vendor**: `cloudflare-ai-gateway`
- **BaseURL**: `https://gateway.ai.cloudflare.com/v1/{ACCOUNT_ID}/{GATEWAY_ID}`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| anthropic/claude-sonnet-4.6 | 1M | 64K | **是** | text,image |
| anthropic/claude-opus-4.8 | 1M | 128K | **是** | text,image |
| openai/gpt-5.4 | 400K | 128K | **是** | text,image |
| openai/gpt-5.2 | 400K | 128K | **是** | text,image |
| google/gemini-2.5-pro | 1M | 64K | **是** | text,image |
| google/gemini-3.5-flash | 1M | 64K | **是** | text,image |
| meta-llama/llama-4-scout | 10M | 16K | 否 | text,image |

---

### 30. Cloudflare Workers AI

- **Vendor**: `cloudflare-workers-ai`
- **BaseURL**: `https://api.cloudflare.com/client/v4/accounts/{ACCOUNT_ID}/ai/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| @cf/meta/llama-4-scout-17b-16e-instruct | 131K | 16K | 否 | text,image |
| @cf/meta/llama-3.3-70b-instruct-fp8-fast | 24K | 24K | 否 | text |
| @cf/google/gemma-4-26b-a4b-it | 256K | 16K | **是** | text,image |
| @cf/mistralai/mistral-small-3.1-24b-instruct | 128K | 128K | 否 | text |
| @cf/openai/gpt-oss-120b | 128K | 16K | **是** | text |
| @cf/openai/gpt-oss-20b | 128K | 16K | **是** | text |
| @cf/moonshotai/kimi-k2.7-code | 262K | 262K | **是** | text |
| @cf/zai-org/glm-5.3 | 1M | 128K | **是** | text |

---

### 31. Amazon Bedrock

- **Vendor**: `amazon-bedrock`
- **BaseURL**: `https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1`
- **API**: `openai-chat`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| anthropic.claude-sonnet-4-6-v1 | 1M | 64K | **是** | text,image |
| anthropic.claude-opus-4-8 | 1M | 128K | **是** | text,image |
| anthropic.claude-sonnet-4-5-20250929-v1:0 | 200K | 64K | **是** | text,image |
| anthropic.claude-haiku-4-5-20251001-v1:0 | 200K | 64K | **是** | text,image |
| anthropic.claude-fable-5 | 1M | 128K | **是** | text,image |
| amazon.nova-pro-v1:0 | 300K | 5K | 否 | text,image |
| amazon.nova-micro-v1:0 | 128K | 5K | 否 | text |
| amazon.nova-lite-v1:0 | 300K | 5K | 否 | text,image |
| deepseek.v3.2 | 128K | 16K | 否 | text |
| deepseek.r1-v1:0 | 128K | 16K | **是** | text |

---

### 32. LongCat（龙猫/长毛象）

#### 32a. OpenAI 模式

- **Vendor**: `longcat`
- **BaseURL**: `https://api.longcat.chat/openai`
- **API**: `openai-chat`

#### 32b. Anthropic 模式

- **Vendor**: `longcat`
- **BaseURL**: `https://api.longcat.chat/anthropic`
- **API**: `anthropic-messages`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| LongCat-2.0 | 1M | 128K | **是** | text |

---

### 33. 百度千帆（Qianfan）

#### 33a. Coding Plan

- **Vendor**: `qianfan`
- **BaseURL**: `https://qianfan.baidubce.com/v2`
- **API**: `openai-chat`
- **API Key**: `${QIANFAN_CODE_PLAN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| qianfan-code-latest | 1M | 64K | **是** | text |
| deepseek-v4-flash | 1M | 384K | **是** | text,image |
| glm-5.1 | 200K | 128K | **是** | text,image |
| deepseek-v4-pro | 1M | 384K | **是** | text,image |

#### 33b. Token Plan

- **Vendor**: `qianfan`
- **BaseURL**: `https://qianfan.baidubce.com/v2/tokenplan/personal`
- **API**: `openai-chat`
- **API Key**: `${QIANFAN_TOKEN_PLAN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| deepseek-v4-pro | 1M | 384K | **是** | text,image |
| deepseek-v4-flash | 1M | 384K | **是** | text,image |
| glm-5.3 | 1M | 128K | **是** | text |
| glm-5.1 | 200K | 128K | **是** | text,image |
| kimi-k2.6 | 262K | 262K | **是** | text,image,video |
| ernie-5.1 | 128K | 64K | **是** | text |

---

### 34. 摩尔线程（Mthreads Coding Plan）

- **Vendor**: `mthreads-plan`
- **BaseURL**: `https://coding-plan-endpoint.kuaecloud.net/v1`
- **API**: `openai-chat`
- **API Key**: `${MTHREADS_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| glm-4.7 | 200K | 128K | **是** | text,image |

---

### 35. 天翼云（Ctyun Coding Plan）

- **Vendor**: `ctyun-plan`
- **BaseURL**: `https://wishub-x6.ctyun.cn/coding/v1`
- **API**: `openai-chat`
- **API Key**: `${CTYUN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| glm-5-turbo | 200K | 128K | **是** | text,image |
| glm-5-pro | 200K | 128K | **是** | text,image |
| deepseek-v3.2-pro | 128K | 64K | **是** | text |

---

### 36. 京东智联云（JD Plan）

- **Vendor**: `jd-plan`
- **BaseURL**: `https://agentrs.jd.com/api/saas/openai-u/v1`
- **API**: `openai-chat`
- **API Key**: `${JD_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| glm-5 | 200K | 64K | **是** | text,image |
| glm-5.1 | 200K | 128K | **是** | text,image |
| glm-5.3 | 1M | 128K | **是** | text |
| qwen3.7-plus | 1M | 64K | **是** | text,image |
| qwen3.7-max | 1M | 64K | **是** | text |
| deepseek-v4-flash | 1M | 128K | **是** | text |
| deepseek-v4-pro | 1M | 128K | **是** | text |
| kimi-k2.6 | 256K | 96K | **是** | text,image |
| minimax-m2.7 | 200K | 128K | **是** | text |
| joyai-llm-flash | 128K | 32K | 否 | text |

---

### 37. 腾讯混元（Tencent Hunyuan）

#### 37a. OpenAI 模式

- **Vendor**: `tencent-hy-plan`
- **BaseURL**: `https://api.lkeap.cloud.tencent.com/plan/v3`
- **API**: `openai-chat`
- **API Key**: `${TENCENT_HY_PLAN_API_KEY}`

#### 37b. Anthropic 模式

- **Vendor**: `tencent-hy-plan`
- **BaseURL**: `https://api.lkeap.cloud.tencent.com/plan/anthropic`
- **API**: `anthropic-messages`
- **API Key**: `${TENCENT_HY_PLAN_ANTHROPIC_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| hy3 | 262K | 64K | **是** | text |

### 38. 阶跃星辰（StepFun）

- **BaseURL**: `https://api.stepfun.com/step_plan/v1`
- **API**: `openai-chat`
- **API Key**: `${STEPFUN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| step-3.7-flash | 256K | 16K | 否 | text,image |

---

### 39. AMD Radeon（AMD AI 开发者计划）

- **Vendor**: `amd-radeon`
- **BaseURL**: `https://developer.amd.com.cn/radeon/api/v1`
- **API**: `openai-chat`
- **API Key**: `${AMD_RADEON_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| DeepSeek-V4-Flash | 1M | 384K | **是** | text |
| Qwen3.8-Flash-Next | 1M | 未公开 | **是** | text |

---

### 40. Agnes AI（国际版 / 国内版）

- **Vendor**: `agnes`
- **BaseURL**: `https://apihub.agnes-ai.com/v1`（国际版 `agnes`）/ `https://api.agnes-ai.cn/v1`（国内版 `agnes-cn`）
- **API**: `openai-chat`
- **API Key**: `${AGNES_API_KEY}`（国际版）/ `${AGNES_CN_API_KEY}`（国内版）

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| agnes-2.5-flash | 200K | 未设置（默认） | **是** | text,image |
| agnes-2.5-pro | 256K | 未设置（默认） | **是** | text,image |
| agnes-3.0-flash | 512K | 65535 | **是** | text,image |

---

### 41. B.AI（`bai`）

- **Vendor**: `bai`
- **BaseURL**: `https://api.b.ai/v1`
- **API**: `openai-chat`
- **API Key**: `${BAI_API_KEY}`

> 模型列表来自 `https://api.b.ai/v1/models` 实时接口(共 49 个 chat 模型,已排除仅用于图像生成的 `gpt-image-2`);上下文窗口 / MaxTokens / 输入能力字段参考各模型公开规格填写,可通过 TUI/WebUI 的模型发现功能校正。

| 模型 | Context | MaxTokens | 推理 | 输入 |
|------|---------|-----------|------|------|
| minimax-m3 | 1M | 128K | **是** | text,image,video |
| minimax-m2.7 | 256K | 128K | **是** | text |
| glm-5.1 | 200K | 128K | **是** | text |
| glm-5.2 | 1M | 128K | **是** | text |
| gpt-5.6-sol | 未设置（默认） | 未设置（默认） | **是** | text,image |
| gpt-5.6-terra | 未设置（默认） | 未设置（默认） | **是** | text,image |
| gpt-5.6-luna | 未设置（默认） | 未设置（默认） | **是** | text,image |
| gpt-5.5 | 1050K | 128K | **是** | text,image |
| gpt-5.5-instant | 128K | 16K | **是** | text,image |
| gpt-5.4 | 1050K | 128K | **是** | text,image |
| gpt-5.2 | 400K | 128K | **是** | text,image |
| gpt-5.4-pro | 1050K | 128K | **是** | text,image |
| gpt-5.4-mini | 400K | 128K | **是** | text,image |
| gpt-5-mini | 400K | 128K | **是** | text,image |
| gpt-5.4-nano | 400K | 128K | **是** | text,image |
| gpt-5-nano | 400K | 128K | **是** | text,image |
| claude-fable-5.1 | 1M | 128K | **是** | text,image |
| claude-fable-5 | 1M | 128K | **是** | text,image |
| claude-opus-5 | 1M | 128K | **是** | text,image |
| claude-opus-4.8 | 1M | 128K | **是** | text,image |
| claude-opus-4.7 | 1M | 128K | **是** | text,image |
| claude-opus-4.6 | 1M | 128K | **是** | text,image |
| claude-opus-4.5 | 200K | 64K | **是** | text,image |
| claude-sonnet-5 | 1M | 64K | **是** | text,image |
| claude-sonnet-4.6 | 1M | 64K | **是** | text,image |
| claude-sonnet-4.5 | 1M | 64K | **是** | text,image |
| claude-haiku-4.5 | 200K | 64K | **是** | text,image |
| gemini-3.1-pro | 1M | 64K | **是** | text,image |
| gemini-3-flash | 1M | 64K | **是** | text,image |
| gemini-3.5-flash-lite | 1M | 64K | **是** | text,image |
| gemini-3.6-flash | 1M | 64K | **是** | text,image |
| hy3 | 256K | 64K | **是** | text |
| mimo-v2.5-pro | 1M | 128K | **是** | text |
| glm-5.3-flash | 1M | 128K | **是** | text,image |
| glm-5.3-flashx | 1M | 128K | **是** | text,image |
| kimi-k3 | 1M | 256K | **是** | text,image |
| glm-5.3 | 1M | 128K | **是** | text |
| kimi-k2.8-preview | 256K | 256K | **是** | text,image |
| kimi-k2.6 | 256K | 256K | **是** | text,image,video |
| mimo-v2.5 | 1M | 128K | **是** | text,image |
| hy4-preview | 256K | 64K | **是** | text |
| qwen3.8-flash | 1M | 未设置（默认） | **是** | text,image |
| qwen3.8-max | 1M | 未设置（默认） | **是** | text,image |
| qwen3.8-27b | 1M | 未设置（默认） | **是** | text,image,video |
| deepseek-v4.1-flash | 1M | 未设置（默认） | **是** | text,image |
| gpt-6-astra | 400K | 128K | **是** | text,image |
| gemini-3.8-flash | 1M | 64K | **是** | text,image |
| deepseek-v4-pro | 1M | 384K | **是** | text |
| gemini-3.5-flash | 1M | 64K | **是** | text,image |

---

## 按供应商分类的 Quick Reference

| 供应商 | Vendor 名 | API 协议 | Thinking 格式 | 模型数 |
|--------|-----------|----------|--------------|--------|
| Anthropic | `anthropic` | anthropic-messages | anthropic | 25 |
| OpenAI | `openai` | openai-responses | openai | 45 |
| CodeOK | `codeok` | openai-responses | - | 4 |
| YesCode | `yescode` | openai-responses | - | 4 |
| DeepSeek | `deepseek` | anthropic/openai-chat | deepseek | 2×2 |
| Google Gemini | `google-gemini` | google-gemini | - | 18 |
| Google Vertex | `google-vertex` | google-vertex | - | 11 |
| 小米 MiMo | `xiaomi` | openai-chat | xiaomi | 3 |
| 火山引擎 | `volcengine`×3 | openai-chat | - | 5~14 |
| OpenRouter | `openrouter` | openai-chat | - | 20 |
| MiniMax | `minimax`×3 | openai-chat/anthropic | - | 3~5 |
| 智谱 AI | `zai` | openai-chat | zai | 6 |
| ModelScope | - | openai-chat | - | 45 |
| 阿里云百炼 | `bailian`×3 | openai-chat | - | 6~14 |
| 华为云 | `huawei`/`huawei-plan` | openai-chat | - | 8/5 |
| Gitee/Moark | `gitee` | openai-chat | - | 30 |
| 摩尔线程 | `mthreads-plan` | openai-chat | - | 1 |
| 天翼云 | `ctyun-plan` | openai-chat | - | 3 |
| 京东智联云 | `jd-plan` | openai-chat | - | 10 |
| Moonshot/Kimi | `moonshotai`/`kimi-coding` | openai-chat | kimi | 7+3 |
| xAI Grok | `xai` | openai-chat | - | 7 |
| Fireworks | `fireworks` | anthropic-messages | - | 7 |
| Together | `together` | openai-chat | - | 5 |
| Nvidia | `nvidia` | openai-chat | - | 5 |
| Mistral | `mistral` | openai-chat | - | 27 |
| HuggingFace | `huggingface` | openai-chat | - | 5 |
| Groq | `groq` | openai-chat | - | 7 |
| Cerebras | `cerebras` | openai-chat | - | 2 |
| 蚂蚁 Ling | `ant-ling` | openai-chat | - | 3 |
| Opencode | `opencode` | openai-chat | - | 5~6 |
| Vercel Gateway | `vercel-ai-gateway` | anthropic-messages | - | 14 |
| GitHub Copilot | `github-copilot` | openai-chat | - | 10 |
| Cloudflare Gateway | `cloudflare-ai-gateway` | openai-chat | - | 7 |
| Cloudflare Workers | `cloudflare-workers-ai` | openai-chat | - | 8 |
| Amazon Bedrock | `amazon-bedrock` | openai-chat | - | 10 |
| LongCat | `longcat` | openai-chat/anthropic | - | 1 |
| 腾讯混元 | `tencent-hy-plan`×2 | openai-chat/anthropic | - | 1 |
| 百度千帆 | `qianfan`×2 | openai-chat | - | 4/6 |
| 阶跃星辰 | `stepfun` | openai-chat | - | 1 |
| AMD Radeon | `amd-radeon` | openai-chat | - | 2 |
| Agnes AI | `agnes`/`agnes-cn` | openai-chat | - | 3 |
| B.AI | `bai` | openai-chat | - | 49 |

---

## 通用配置字段说明

| 字段 | 说明 | 可选值 |
|------|------|--------|
| `api` | API 协议 | `openai-chat`, `openai-responses`, `anthropic-messages`, `google-gemini`, `google-vertex`, 空（自动检测） |
| `thinkingFormat` | 推理格式 | `anthropic`, `deepseek`, `openai`, `xiaomi`, `zai`, `kimi`, `qwen`, `""`（默认） |
| `cacheControl` | Prompt 缓存 | `true`（启用）/ `false`（禁用）/ `nil`（默认） |
| `maxImagesPerRequest` | OpenAI-compatible 请求最多保留的图片数 | 正数限制最新图片；`0` 使用 provider 默认值；`-1` 不限制 |
| `vendor` | 显式供应商 | 见上方 vendor 名列表 |
| `maxTokens` | 最大输出 tokens | 整数 |
| `contextWindow` | 上下文窗口 | 整数 |
| `temperature` | 温度 | 浮点数（0~2） |
| `topP` | Top-P 采样 | 浮点数（0~1） |
| `reasoning` | 是否支持推理 | `true`/`false` |

内置默认值目前为 `gitee`/`moark` 5 张、官方 OpenAI 1500 张。OpenAI 文档给出单请求最多 1500 个图片输入；xAI 文档标为不限制；Gemini 文档给出 3600 个图片文件；Anthropic 文档按上下文模型给出 100/600 张；Mistral 明确说明上限取决于模型和总 token 预算。因此没有稳定固定上限的 provider 默认保持 `0`，可按实际网关在 `settings.json` 中覆盖。

## Thinking Levels

```go
off      // 关闭推理
minimal  // 最少推理
low      // 低
medium   // 中（默认）
high     // 高
xhigh    // 最高
```
