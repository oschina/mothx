# MothX 供应商/模型完整配置表

> 由 `docs/scripts/generate-models.py` 依据 `internal/config/settings.go`（`defaultProviderConfigs`）与 `internal/provider/vendor_*.go` 生成，请勿手工编辑；运行 `make docs-models` 重新生成。

本文档是 MothX 内置供应商与模型的完整参考：共 **63 个供应商**、**589 个模型**。

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

## 按供应商分类的 Quick Reference

| 供应商 | Provider | Vendor | API 协议 | Thinking 格式 | 模型数 |
|--------|----------|--------|----------|--------------|--------|
| Moark | `moark` | gitee | openai-chat | - | 30 |
| DeepSeek（官方 · Anthropic） | `deepseek-anthropic` | - | anthropic-messages | - | 3 |
| DeepSeek（官方） | `deepseek-openai` | - | openai-chat | - | 3 |
| 小米 MiMo | `xiaomi` | - | openai-chat | xiaomi | 2 |
| 小米 MiMo Token Plan（AMS） | `xiaomi-token-plan-ams` | - | openai-chat | - | 2 |
| 小米 MiMo Token Plan（CN） | `xiaomi-token-plan-cn` | - | openai-chat | - | 2 |
| 小米 MiMo Token Plan（SGP） | `xiaomi-token-plan-sgp` | - | openai-chat | - | 2 |
| 火山引擎（Volcengine） | `volcengine` | volcengine | openai-chat | - | 6 |
| 火山引擎 Agent Plan | `volcengine-agentplan` | volcengine-agentplan | openai-chat | - | 15 |
| 火山引擎 Coding Plan | `volcengine-codingplan` | volcengine-codingplan | openai-chat | - | 12 |
| OpenAI（官方） | `openai` | - | openai-responses | - | 45 |
| Anthropic（官方） | `anthropic` | - | anthropic-messages | - | 25 |
| LongCat（Anthropic） | `longcat-anthropic` | longcat | anthropic-messages | - | 1 |
| MiniMax（Anthropic） | `minimax-anthropic` | - | anthropic-messages | - | 3 |
| MiniMax 国内（Anthropic） | `minimax-cn-anthropic` | - | anthropic-messages | - | 3 |
| 腾讯混元（Anthropic） | `tencent-hy-plan-anthropic` | tencent-hy-plan | anthropic-messages | - | 1 |
| Google Gemini | `google-gemini` | - | google-gemini | - | 16 |
| Google Vertex AI | `google-vertex` | - | google-vertex | - | 10 |
| Agnes AI（国际版） | `agnes` | agnes | openai-chat | - | 3 |
| Agnes AI（国内版） | `agnes-cn` | agnes | openai-chat | - | 3 |
| 阿里云百炼 Coding Plan | `alibaba-coding-plan` | bailian | openai-chat | - | 10 |
| 阿里云百炼（标准） | `alibaba-standard` | bailian | openai-chat | - | 6 |
| 阿里云百炼 Token Plan | `alibaba-token-plan` | bailian | openai-chat | - | 14 |
| Amazon Bedrock | `amazon-bedrock` | amazon-bedrock | openai-chat | - | 10 |
| AMD Radeon | `amd-radeon` | amd-radeon | openai-chat | - | 2 |
| 蚂蚁 Ling（Cerebras） | `ant-ling` | - | openai-chat | - | 3 |
| B.AI | `bai` | bai | openai-chat | - | 49 |
| Cerebras | `cerebras` | - | openai-chat | - | 2 |
| Cloudflare AI Gateway | `cloudflare-ai-gateway` | cloudflare-ai-gateway | openai-chat | - | 7 |
| Cloudflare Workers AI | `cloudflare-workers-ai` | cloudflare-workers-ai | openai-chat | - | 8 |
| CodeOK | `codeok` | codeok | openai-responses | - | 4 |
| 天翼云 Coding Plan | `ctyun-plan` | ctyun-plan | openai-chat | - | 3 |
| Fireworks AI | `fireworks` | - | anthropic-messages | - | 7 |
| Gitee AI | `gitee` | gitee | openai-chat | - | 30 |
| GitHub Copilot | `github-copilot` | github-copilot | openai-chat | - | 10 |
| Groq | `groq` | - | openai-chat | - | 7 |
| 华为云（ModelArts） | `huawei` | huawei | openai-chat | - | 8 |
| 华为云 Coding Plan | `huawei-plan` | huawei-plan | openai-chat | - | 5 |
| HuggingFace | `huggingface` | - | openai-chat | - | 5 |
| 京东智联云 JD Plan | `jd-plan` | jd-plan | openai-chat | - | 10 |
| Kimi Coding | `kimi-coding` | - | openai-chat | kimi | 4 |
| LongCat（龙猫） | `longcat` | longcat | openai-chat | - | 1 |
| MiniMax | `minimax` | minimax | anthropic-messages | - | 5 |
| Mistral | `mistral` | mistral | openai-chat | - | 30 |
| ModelScope（魔搭社区） | `modelscope` | - | openai-chat | - | 45 |
| 月之暗面（Moonshot / Kimi） | `moonshotai` | - | openai-chat | - | 7 |
| 月之暗面（国内） | `moonshotai-cn` | - | openai-chat | - | 7 |
| 摩尔线程 Coding Plan | `mthreads-plan` | mthreads-plan | openai-chat | - | 1 |
| Nvidia NIM | `nvidia` | - | openai-chat | - | 5 |
| CodePlayz（Opencode） | `opencode` | - | openai-chat | - | 5 |
| CodePlayz（Opencode Go） | `opencode-go` | - | openai-chat | - | 7 |
| OpenRouter | `openrouter` | openrouter | openai-chat | - | 18 |
| OpenRouter 免费模型 | `openrouter-free-models` | openrouter | openai-chat | - | 11 |
| 百度千帆 Code Plan | `qianfan-code-plan` | qianfan | openai-chat | - | 4 |
| 百度千帆 Token Plan | `qianfan-token-plan` | qianfan | openai-chat | - | 6 |
| 阶跃星辰（StepFun） | `stepfun` | - | openai-chat | - | 1 |
| 腾讯混元（Tencent Hunyuan） | `tencent-hy-plan` | tencent-hy-plan | openai-chat | - | 1 |
| Together AI | `together` | - | openai-chat | - | 5 |
| Vercel AI Gateway | `vercel-ai-gateway` | - | anthropic-messages | - | 14 |
| xAI（Grok） | `xai` | - | openai-chat | - | 7 |
| YesCode | `yescode` | yescode | openai-responses | - | 4 |
| 智谱 AI（Z.AI） | `zai` | zai | openai-chat | zai | 7 |
| 智谱 AI Coding（国内） | `zai-coding-cn` | zai | openai-chat | zai | 7 |

---

## 完整供应商列表

### Moark（`moark`） <a id="moark"></a>

Vendor `gitee` · BaseURL `https://api.moark.com/v1` · API `openai-chat` · API Key `${MOARK_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `deepseek-v4.1-flash` | 1M | - | **是** | text,image | - | - | - | - |
| `qwen3.8-flash` | 1M | - | **是** | text,image | - | - | - | - |
| `glm-5.3-flash` | 1M | 131K | **是** | text,image | - | - | - | - |
| `qwen3.8-max-0902` | 1M | 131K | **是** | text,image | - | - | - | - |
| `glm-5.3` | 1M | 131K | **是** | text | - | - | - | - |
| `kimi-k3` | 1M | 262K | **是** | text,image | - | - | - | - |
| `minimax-m3` | 1M | 128K | **是** | text,image | - | - | - | - |
| `mimo-v2.5-pro` | 1M | 131K | **是** | text,image | - | - | - | - |
| `deepseek-v4-flash` | 1M | 384K | **是** | text | - | - | - | - |
| `deepseek-v4-pro` | 1M | 384K | **是** | text | - | - | - | - |
| `qwen3.8-omni-flash` | 1M | 131K | **是** | text,image,audio,video | - | - | - | - |
| `qwen3.5-flash` | 1M | 66K | **是** | text,image | - | - | - | - |
| `qwen3.6-flash` | 1M | 66K | **是** | text,image | - | - | - | - |
| `qwen3.7-plus` | 1M | 66K | **是** | text,image | - | - | - | - |
| `qwen3.7-max` | 1M | 66K | **是** | text | - | - | - | - |
| `auto` | 1M | - | **是** | text,image | - | - | - | - |
| `qwen3.8-max` | 1M | - | **是** | text,image | - | - | - | - |
| `qwen3.8-27b` | 1M | - | **是** | text,image,video | - | - | - | - |
| `deepseek-v4-flash-0731` | 1M | - | **是** | text | - | - | - | - |
| `deepseek-v4-pro-0813` | 1M | - | **是** | text | - | - | - | - |
| `kimi-k2.5` | 262K | 262K | **是** | text,image,video | - | - | - | - |
| `kimi-k2.6` | 262K | 262K | **是** | text,image,video | - | - | - | - |
| `kimi-k2.7-code` | 262K | 262K | **是** | text | - | - | - | - |
| `minimax-m2.7` | 262K | 131K | **是** | text | - | - | - | - |
| `step-3.7-flash` | 262K | 16K | 否 | text,image | - | - | - | - |
| `glm-5.1` | 200K | 131K | **是** | text | - | - | - | - |
| `glm-5` | 200K | 33K | **是** | text | - | - | - | - |
| `ernie-5.0-thinking` | 131K | 66K | **是** | text | - | - | - | - |
| `gemma-4-26b-a4b-it` | 131K | 33K | **是** | text,image | - | - | - | - |
| `qwen3.6-plus` | 66K | 66K | **是** | text,image | - | - | - | - |

---

### DeepSeek（官方 · Anthropic）（`deepseek-anthropic`） <a id="deepseek-anthropic"></a>

BaseURL `https://api.deepseek.com/anthropic` · API `anthropic-messages` · API Key `${DEEPSEEK_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `deepseek-v4-flash` | 1M | 384K | **是** | text | 0.14 | 0.28 | 0.003 | 0 |
| `deepseek-v4-pro` | 1M | 384K | **是** | text | 0.435 | 0.87 | 0.004 | 0 |
| `deepseek-v4-flash-vision-exp` | 1M | - | **是** | text,image | - | - | - | - |

---

### DeepSeek（官方）（`deepseek-openai`） <a id="deepseek-openai"></a>

BaseURL `https://api.deepseek.com` · API `openai-chat` · API Key `${DEEPSEEK_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `deepseek-v4-flash` | 1M | 384K | **是** | text | 0.14 | 0.28 | 0.003 | 0 |
| `deepseek-v4-pro` | 1M | 384K | **是** | text | 0.435 | 0.87 | 0.004 | 0 |
| `deepseek-v4-flash-vision-exp` | 1M | - | **是** | text,image | - | - | - | - |

---

### 小米 MiMo（`xiaomi`） <a id="xiaomi"></a>

BaseURL `https://api.xiaomimimo.com/v1` · API `openai-chat` · Thinking `xiaomi` · API Key `${XIAOMI_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `mimo-v2.6-flash` | 1M | 131K | **是** | text,image | - | - | - | - |
| `mimo-v2.6-pro` | 1M | 131K | **是** | text,image | - | - | - | - |

---

### 小米 MiMo Token Plan（AMS）（`xiaomi-token-plan-ams`） <a id="xiaomi-token-plan-ams"></a>

BaseURL `https://token-plan-ams.xiaomimimo.com/v1` · API `openai-chat` · API Key `${XIAOMI_TOKEN_PLAN_AMS_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `mimo-v2.6-flash` | 1M | 131K | **是** | text,image | - | - | - | - |
| `mimo-v2.6-pro` | 1M | 131K | **是** | text,image | - | - | - | - |

---

### 小米 MiMo Token Plan（CN）（`xiaomi-token-plan-cn`） <a id="xiaomi-token-plan-cn"></a>

BaseURL `https://token-plan-cn.xiaomimimo.com/v1` · API `openai-chat` · API Key `${XIAOMI_TOKEN_PLAN_CN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `mimo-v2.6-flash` | 1M | 131K | **是** | text,image | - | - | - | - |
| `mimo-v2.6-pro` | 1M | 131K | **是** | text,image | - | - | - | - |

---

### 小米 MiMo Token Plan（SGP）（`xiaomi-token-plan-sgp`） <a id="xiaomi-token-plan-sgp"></a>

BaseURL `https://token-plan-sgp.xiaomimimo.com/v1` · API `openai-chat` · API Key `${XIAOMI_TOKEN_PLAN_SGP_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `mimo-v2.6-flash` | 1M | 131K | **是** | text,image | - | - | - | - |
| `mimo-v2.6-pro` | 1M | 131K | **是** | text,image | - | - | - | - |

---

### 火山引擎（Volcengine）（`volcengine`） <a id="volcengine"></a>

Vendor `volcengine` · BaseURL `https://ark.cn-beijing.volces.com/api/v3` · API `openai-chat` · API Key `${VOLCENGINE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `doubao-seed-2-1-turbo-260628` | 262K | 262K | 否 | text | - | - | - | - |
| `doubao-seed-evolving` | 262K | 262K | 否 | text,image | - | - | - | - |
| `doubao-seed-2-1-pro-260628` | 262K | 262K | 否 | text,image | - | - | - | - |
| `glm-5.3` | 1M | - | **是** | text | - | - | - | - |
| `glm-5.3-flash` | 1M | - | **是** | text,image | - | - | - | - |
| `deepseek-v4.1-flash` | 1M | 262K | **是** | text,image | - | - | - | - |

---

### 火山引擎 Agent Plan（`volcengine-agentplan`） <a id="volcengine-agentplan"></a>

Vendor `volcengine-agentplan` · BaseURL `https://ark.cn-beijing.volces.com/api/plan/v3` · API `openai-chat` · API Key `${VOLCENGINE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `ark-code-latest` | 262K | 100K | **是** | text | - | - | - | - |
| `doubao-seed-2.1-turbo` | 262K | 100K | **是** | text | - | - | - | - |
| `doubao-seed-evolving` | 1M | 100K | **是** | text,image | - | - | - | - |
| `doubao-seed-2-0-lite` | 262K | 100K | **是** | text | - | - | - | - |
| `doubao-seed-2-0-mini` | 262K | 100K | **是** | text | - | - | - | - |
| `glm-5.3` | 1M | - | **是** | text | - | - | - | - |
| `glm-5.3-flash` | 1M | - | **是** | text,image | - | - | - | - |
| `kimi-k2.7-code` | 262K | 100K | **是** | text | - | - | - | - |
| `deepseek-v4-pro` | 1M | 100K | **是** | text | - | - | - | - |
| `deepseek-v4-flash` | 1M | 100K | **是** | text,image | - | - | - | - |
| `deepseek-v4.1-flash` | 1M | 100K | **是** | text,image | - | - | - | - |
| `minimax-m3` | 1M | 100K | **是** | text,image | - | - | - | - |
| `minimax-m2.7` | 262K | 100K | **是** | text | - | - | - | - |
| `kimi-k2.6` | 262K | 100K | **是** | text,image | - | - | - | - |
| `kimi-k3` | 1M | 100K | **是** | text,image | - | - | - | - |

---

### 火山引擎 Coding Plan（`volcengine-codingplan`） <a id="volcengine-codingplan"></a>

Vendor `volcengine-codingplan` · BaseURL `https://ark.cn-beijing.volces.com/api/coding/v3` · API `openai-chat` · API Key `${VOLCENGINE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `ark-code-latest` | 262K | 100K | **是** | text | - | - | - | - |
| `doubao-seed-2.1-turbo` | 262K | 100K | **是** | text | - | - | - | - |
| `doubao-seed-evolving` | 1M | 100K | **是** | text,image | - | - | - | - |
| `doubao-seed-2-0-lite` | 262K | 100K | **是** | text | - | - | - | - |
| `doubao-seed-2-0-mini` | 262K | 100K | **是** | text | - | - | - | - |
| `glm-5.3` | 1M | - | **是** | text | - | - | - | - |
| `glm-5.3-flash` | 1M | - | **是** | text,image | - | - | - | - |
| `kimi-k2.7-code` | 262K | 100K | **是** | text | - | - | - | - |
| `deepseek-v4-pro` | 1M | 100K | **是** | text | - | - | - | - |
| `deepseek-v4-flash` | 1M | 100K | **是** | text,image | - | - | - | - |
| `deepseek-v4.1-flash` | 1M | 100K | **是** | text,image | - | - | - | - |
| `minimax-m3` | 1M | 100K | **是** | text,image | - | - | - | - |

---

### OpenAI（官方）（`openai`） <a id="openai"></a>

BaseURL `https://api.openai.com/v1` · API `openai-responses` · API Key `${OPENAI_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `gpt-4` | 8K | 8K | 否 | text | 30 | 60 | 0 | 0 |
| `gpt-4-turbo` | 128K | 4K | 否 | text,image | 10 | 30 | 0 | 0 |
| `gpt-4.1` | 1M | 33K | 否 | text,image | 2 | 8 | 0.5 | 0 |
| `gpt-4.1-mini` | 1M | 33K | 否 | text,image | 0.4 | 1.6 | 0.1 | 0 |
| `gpt-4.1-nano` | 1M | 33K | 否 | text,image | 0.1 | 0.4 | 0.025 | 0 |
| `gpt-4o` | 128K | 16K | 否 | text,image | 2.5 | 10 | 1.25 | 0 |
| `gpt-4o-2024-05-13` | 128K | 4K | 否 | text,image | 5 | 15 | 0 | 0 |
| `gpt-4o-2024-08-06` | 128K | 16K | 否 | text,image | 2.5 | 10 | 1.25 | 0 |
| `gpt-4o-2024-11-20` | 128K | 16K | 否 | text,image | 2.5 | 10 | 1.25 | 0 |
| `gpt-4o-mini` | 128K | 16K | 否 | text,image | 0.15 | 0.6 | 0.075 | 0 |
| `gpt-5` | 400K | 128K | **是** | text,image | 1.25 | 10 | 0.125 | 0 |
| `gpt-5-chat-latest` | 128K | 16K | 否 | text,image | 1.25 | 10 | 0.125 | 0 |
| `gpt-5-codex` | 400K | 128K | **是** | text,image | 1.25 | 10 | 0.125 | 0 |
| `gpt-5-mini` | 400K | 128K | **是** | text,image | 0.25 | 2 | 0.025 | 0 |
| `gpt-5-nano` | 400K | 128K | **是** | text,image | 0.05 | 0.4 | 0.005 | 0 |
| `gpt-5-pro` | 400K | 128K | **是** | text,image | 15 | 120 | 0 | 0 |
| `gpt-5.1` | 400K | 128K | **是** | text,image | 1.25 | 10 | 0.125 | 0 |
| `gpt-5.1-chat-latest` | 128K | 16K | **是** | text,image | 1.25 | 10 | 0.125 | 0 |
| `gpt-5.1-codex` | 400K | 128K | **是** | text,image | 1.25 | 10 | 0.125 | 0 |
| `gpt-5.1-codex-max` | 400K | 128K | **是** | text,image | 1.25 | 10 | 0.125 | 0 |
| `gpt-5.1-codex-mini` | 400K | 128K | **是** | text,image | 0.25 | 2 | 0.025 | 0 |
| `gpt-5.2` | 400K | 128K | **是** | text,image | 1.75 | 14 | 0.175 | 0 |
| `gpt-5.2-chat-latest` | 128K | 16K | **是** | text,image | 1.75 | 14 | 0.175 | 0 |
| `gpt-5.2-codex` | 400K | 128K | **是** | text,image | 1.75 | 14 | 0.175 | 0 |
| `gpt-5.2-pro` | 400K | 128K | **是** | text,image | 21 | 168 | 0 | 0 |
| `gpt-5.3-chat-latest` | 128K | 16K | 否 | text,image | 1.75 | 14 | 0.175 | 0 |
| `gpt-5.3-codex` | 400K | 128K | **是** | text,image | 1.75 | 14 | 0.175 | 0 |
| `gpt-5.3-codex-spark` | 128K | 32K | **是** | text,image | 1.75 | 14 | 0.175 | 0 |
| `gpt-5.4` | 272K | 128K | **是** | text,image | 2.5 | 15 | 0.25 | 0 |
| `gpt-5.4-mini` | 400K | 128K | **是** | text,image | 0.75 | 4.5 | 0.075 | 0 |
| `gpt-5.4-nano` | 400K | 128K | **是** | text,image | 0.2 | 1.25 | 0.02 | 0 |
| `gpt-5.4-pro` | 1M | 128K | **是** | text,image | 30 | 180 | 0 | 0 |
| `gpt-5.5` | 272K | 128K | **是** | text,image | 5 | 30 | 0.5 | 0 |
| `gpt-5.5-pro` | 1M | 128K | **是** | text,image | 30 | 180 | 0 | 0 |
| `gpt-5.6-sol` | - | - | **是** | text,image | - | - | - | - |
| `gpt-5.6-terra` | - | - | **是** | text,image | - | - | - | - |
| `gpt-5.6-luna` | - | - | **是** | text,image | - | - | - | - |
| `o1` | 200K | 100K | **是** | text,image | 15 | 60 | 7.5 | 0 |
| `o1-pro` | 200K | 100K | **是** | text,image | 150 | 600 | 0 | 0 |
| `o3` | 200K | 100K | **是** | text,image | 2 | 8 | 0.5 | 0 |
| `o3-deep-research` | 200K | 100K | **是** | text,image | 10 | 40 | 2.5 | 0 |
| `o3-mini` | 200K | 100K | **是** | text | 1.1 | 4.4 | 0.55 | 0 |
| `o3-pro` | 200K | 100K | **是** | text,image | 20 | 80 | 0 | 0 |
| `o4-mini` | 200K | 100K | **是** | text,image | 1.1 | 4.4 | 0.275 | 0 |
| `o4-mini-deep-research` | 200K | 100K | **是** | text,image | 2 | 8 | 0.5 | 0 |

---

### Anthropic（官方）（`anthropic`） <a id="anthropic"></a>

BaseURL `https://api.anthropic.com` · API `anthropic-messages` · API Key `${ANTHROPIC_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `claude-3-5-haiku-20241022` | 200K | 8K | 否 | text,image | 0.8 | 4 | 0.08 | 1 |
| `claude-3-5-haiku-latest` | 200K | 8K | 否 | text,image | 0.8 | 4 | 0.08 | 1 |
| `claude-3-5-sonnet-20240620` | 200K | 8K | 否 | text,image | 3 | 15 | 0.3 | 3.75 |
| `claude-3-5-sonnet-20241022` | 200K | 8K | 否 | text,image | 3 | 15 | 0.3 | 3.75 |
| `claude-3-7-sonnet-20250219` | 200K | 64K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |
| `claude-3-haiku-20240307` | 200K | 4K | 否 | text,image | 0.25 | 1.25 | 0.03 | 0.3 |
| `claude-3-opus-20240229` | 200K | 4K | 否 | text,image | 15 | 75 | 1.5 | 18.75 |
| `claude-3-sonnet-20240229` | 200K | 4K | 否 | text,image | 3 | 15 | 0.3 | 0.3 |
| `claude-fable-5` | 1M | 128K | **是** | text,image | 10 | 50 | 1 | 12.5 |
| `claude-haiku-4-5` | 200K | 64K | **是** | text,image | 1 | 5 | 0.1 | 1.25 |
| `claude-haiku-4-5-20251001` | 200K | 64K | **是** | text,image | 1 | 5 | 0.1 | 1.25 |
| `claude-opus-4-0` | 200K | 32K | **是** | text,image | 15 | 75 | 1.5 | 18.75 |
| `claude-opus-4-1` | 200K | 32K | **是** | text,image | 15 | 75 | 1.5 | 18.75 |
| `claude-opus-4-1-20250805` | 200K | 32K | **是** | text,image | 15 | 75 | 1.5 | 18.75 |
| `claude-opus-4-20250514` | 200K | 32K | **是** | text,image | 15 | 75 | 1.5 | 18.75 |
| `claude-opus-4-5` | 200K | 64K | **是** | text,image | 5 | 25 | 0.5 | 6.25 |
| `claude-opus-4-5-20251101` | 200K | 64K | **是** | text,image | 5 | 25 | 0.5 | 6.25 |
| `claude-opus-4-6` | 1M | 128K | **是** | text,image | 5 | 25 | 0.5 | 6.25 |
| `claude-opus-4-7` | 1M | 128K | **是** | text,image | 5 | 25 | 0.5 | 6.25 |
| `claude-opus-4-8` | 1M | 128K | **是** | text,image | 5 | 25 | 0.5 | 6.25 |
| `claude-sonnet-4-0` | 200K | 64K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |
| `claude-sonnet-4-20250514` | 200K | 64K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |
| `claude-sonnet-4-5` | 200K | 64K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |
| `claude-sonnet-4-5-20250929` | 200K | 64K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |
| `claude-sonnet-4-6` | 1M | 64K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |

---

### LongCat（Anthropic）（`longcat-anthropic`） <a id="longcat-anthropic"></a>

Vendor `longcat` · BaseURL `https://api.longcat.chat/anthropic` · API `anthropic-messages` · API Key `${LONGCAT_ANTHROPIC_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `LongCat-2.0` | 1M | 131K | **是** | text | - | - | - | - |

---

### MiniMax（Anthropic）（`minimax-anthropic`） <a id="minimax-anthropic"></a>

BaseURL `https://api.minimaxi.com/anthropic` · API `anthropic-messages` · API Key `${MINIMAX_ANTHROPIC_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `MiniMax-M2.7` | 205K | 131K | **是** | text | 0.3 | 1.2 | 0.06 | 0.375 |
| `MiniMax-M2.7-highspeed` | 205K | 131K | **是** | text | 0.6 | 2.4 | 0.06 | 0.375 |
| `MiniMax-M3` | 512K | 128K | **是** | text,image | 0.6 | 2.4 | 0.12 | 0 |

---

### MiniMax 国内（Anthropic）（`minimax-cn-anthropic`） <a id="minimax-cn-anthropic"></a>

BaseURL `https://api.minimaxi.com/anthropic` · API `anthropic-messages` · API Key `${MINIMAX_CN_ANTHROPIC_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `MiniMax-M2.7` | 205K | 131K | **是** | text | 0.3 | 1.2 | 0.06 | 0.375 |
| `MiniMax-M2.7-highspeed` | 205K | 131K | **是** | text | 0.6 | 2.4 | 0.06 | 0.375 |
| `MiniMax-M3` | 512K | 128K | **是** | text,image | 0.6 | 2.4 | 0.12 | 0 |

---

### 腾讯混元（Anthropic）（`tencent-hy-plan-anthropic`） <a id="tencent-hy-plan-anthropic"></a>

Vendor `tencent-hy-plan` · BaseURL `https://api.lkeap.cloud.tencent.com/plan/anthropic` · API `anthropic-messages` · API Key `${TENCENT_HY_PLAN_ANTHROPIC_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `hy3` | 262K | 66K | **是** | text | - | - | - | - |

---

### Google Gemini（`google-gemini`） <a id="google-gemini"></a>

BaseURL `https://generativelanguage.googleapis.com/v1beta/models` · API `google-gemini` · API Key `${GOOGLE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `gemini-2.0-flash` | 1M | 8K | 否 | text,image | 0.1 | 0.4 | 0.025 | 0 |
| `gemini-2.0-flash-lite` | 1M | 8K | 否 | text,image | 0.075 | 0.3 | 0 | 0 |
| `gemini-2.5-flash` | 1M | 66K | **是** | text,image | 0.3 | 2.5 | 0.03 | 0 |
| `gemini-2.5-flash-lite` | 1M | 66K | **是** | text,image | 0.1 | 0.4 | 0.01 | 0 |
| `gemini-2.5-pro` | 1M | 66K | **是** | text,image | 1.25 | 10 | 0.125 | 0 |
| `gemini-3-flash-preview` | 1M | 66K | **是** | text,image | 0.5 | 3 | 0.05 | 0 |
| `gemini-3-pro-preview` | 1M | 66K | **是** | text,image | 2 | 12 | 0.2 | 0 |
| `gemini-3.1-flash-lite` | 1M | 66K | **是** | text,image | 0.25 | 1.5 | 0.025 | 0 |
| `gemini-3.1-flash-lite-preview` | 1M | 66K | **是** | text,image | 0.25 | 1.5 | 0.025 | 0 |
| `gemini-3.1-pro-preview` | 1M | 66K | **是** | text,image | 2 | 12 | 0.2 | 0 |
| `gemini-3.1-pro-preview-customtools` | 1M | 66K | **是** | text,image | 2 | 12 | 0.2 | 0 |
| `gemini-3.5-flash` | 1M | 66K | **是** | text,image | 1.5 | 9 | 0.15 | 0 |
| `gemini-flash-latest` | 1M | 66K | **是** | text,image | 1.5 | 9 | 0.15 | 0 |
| `gemini-flash-lite-latest` | 1M | 66K | **是** | text,image | 0.25 | 1.5 | 0.025 | 0 |
| `gemma-4-26b-a4b-it` | 262K | 33K | **是** | text,image | - | - | - | - |
| `gemma-4-31b-it` | 262K | 33K | **是** | text,image | - | - | - | - |

---

### Google Vertex AI（`google-vertex`） <a id="google-vertex"></a>

BaseURL `https://aiplatform.googleapis.com/v1/publishers/google/models` · API `google-vertex` · API Key `${GOOGLE_CLOUD_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `gemini-2.5-flash` | 1M | 66K | **是** | text,image | 0.3 | 2.5 | 0.03 | 0 |
| `gemini-2.5-flash-lite` | 1M | 66K | **是** | text,image | 0.1 | 0.4 | 0.01 | 0 |
| `gemini-2.5-pro` | 1M | 66K | **是** | text,image | 1.25 | 10 | 0.125 | 0 |
| `gemini-3-flash-preview` | 1M | 66K | **是** | text,image | 0.5 | 3 | 0.05 | 0 |
| `gemini-3.1-flash-lite` | 1M | 66K | **是** | text,image | 0.25 | 1.5 | 0.025 | 0 |
| `gemini-3.1-pro-preview` | 1M | 66K | **是** | text,image | 2 | 12 | 0.2 | 0 |
| `gemini-3.1-pro-preview-customtools` | 1M | 66K | **是** | text,image | 2 | 12 | 0.2 | 0 |
| `gemini-3.5-flash` | 1M | 66K | **是** | text,image | 1.5 | 9 | 0.15 | 0 |
| `gemini-flash-latest` | 1M | 66K | **是** | text,image | 1.5 | 9 | 0.15 | 0 |
| `gemini-flash-lite-latest` | 1M | 66K | **是** | text,image | 0.25 | 1.5 | 0.025 | 0 |

---

### Agnes AI（国际版）（`agnes`） <a id="agnes"></a>

Vendor `agnes` · BaseURL `https://apihub.agnes-ai.com/v1` · API `openai-chat` · API Key `${AGNES_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `agnes-2.5-flash` | 200K | - | **是** | text,image | - | - | - | - |
| `agnes-2.5-pro` | 256K | - | **是** | text,image | - | - | - | - |
| `agnes-3.0-flash` | 512K | 66K | **是** | text,image | - | - | - | - |

---

### Agnes AI（国内版）（`agnes-cn`） <a id="agnes-cn"></a>

Vendor `agnes` · BaseURL `https://api.agnes-ai.cn/v1` · API `openai-chat` · API Key `${AGNES_CN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `agnes-2.5-flash` | 200K | - | **是** | text,image | - | - | - | - |
| `agnes-2.5-pro` | 256K | - | **是** | text,image | - | - | - | - |
| `agnes-3.0-flash` | 512K | 66K | **是** | text,image | - | - | - | - |

---

### 阿里云百炼 Coding Plan（`alibaba-coding-plan`） <a id="alibaba-coding-plan"></a>

Vendor `bailian` · BaseURL `https://coding.dashscope.aliyuncs.com/v1` · API `openai-chat` · API Key `${BAILIAN_CODING_PLAN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `qwen3.5-plus` | 1M | 66K | **是** | text,image,video | - | - | - | - |
| `qwen3.6-plus` | 1M | 66K | **是** | text,image,video | - | - | - | - |
| `qwen3.7-plus` | 1M | 66K | **是** | text,image | - | - | - | - |
| `glm-5` | 200K | 33K | **是** | text | - | - | - | - |
| `kimi-k2.5` | 262K | 262K | **是** | text,image,video | - | - | - | - |
| `MiniMax-M2.5` | 197K | 131K | **是** | text | - | - | - | - |
| `qwen3-coder-plus` | 1M | 66K | 否 | text | - | - | - | - |
| `qwen3-coder-next` | 262K | 66K | 否 | text | - | - | - | - |
| `qwen3-max-2026-01-23` | 262K | 66K | **是** | text | - | - | - | - |
| `glm-4.7` | 200K | 131K | **是** | text | - | - | - | - |

---

### 阿里云百炼（标准）（`alibaba-standard`） <a id="alibaba-standard"></a>

Vendor `bailian` · BaseURL `https://dashscope.aliyuncs.com/compatible-mode/v1` · API `openai-chat` · API Key `${DASHSCOPE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `qwen3.6-plus` | 1M | 66K | **是** | text,image | - | - | - | - |
| `qwen3.7-plus` | 1M | 66K | **是** | text,image | - | - | - | - |
| `qwen3.7-max` | 1M | 66K | **是** | text | - | - | - | - |
| `glm-5.1` | 200K | 131K | **是** | text | - | - | - | - |
| `deepseek-v4-pro` | 1M | 384K | **是** | text,image,video | - | - | - | - |
| `deepseek-v4-flash` | 1M | 384K | 否 | text | - | - | - | - |

---

### 阿里云百炼 Token Plan（`alibaba-token-plan`） <a id="alibaba-token-plan"></a>

Vendor `bailian` · BaseURL `https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1` · API `openai-chat` · API Key `${BAILIAN_TOKEN_PLAN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `qwen3.8-max-preview` | 1M | 66K | **是** | text | - | - | - | - |
| `qwen3.6-plus` | 1M | 66K | **是** | text,image | - | - | - | - |
| `qwen3.7-max` | 1M | 66K | **是** | text | - | - | - | - |
| `qwen3.7-plus` | 1M | 66K | **是** | text,image | - | - | - | - |
| `qwen3.6-flash` | 1M | 66K | **是** | text,image | - | - | - | - |
| `deepseek-v4-pro` | 1M | 384K | 否 | text | - | - | - | - |
| `deepseek-v4-flash` | 1M | 384K | 否 | text | - | - | - | - |
| `deepseek-v3.2` | 131K | 66K | **是** | text | - | - | - | - |
| `kimi-k2.6` | 262K | 262K | **是** | text,image,video | - | - | - | - |
| `kimi-k2.5` | 262K | 262K | **是** | text,image,video | - | - | - | - |
| `glm-5.3` | 1M | 131K | **是** | text | - | - | - | - |
| `glm-5.1` | 200K | 131K | **是** | text | - | - | - | - |
| `glm-5` | 200K | 33K | **是** | text | - | - | - | - |
| `MiniMax-M2.5` | 197K | 131K | 否 | text | - | - | - | - |

---

### Amazon Bedrock（`amazon-bedrock`） <a id="amazon-bedrock"></a>

Vendor `amazon-bedrock` · BaseURL `https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1` · API `openai-chat` · API Key `${AWS_BEARER_TOKEN_BEDROCK}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `anthropic.claude-sonnet-4-6-v1` | 1M | 64K | **是** | text,image | - | - | - | - |
| `anthropic.claude-opus-4-8` | 1M | 128K | **是** | text,image | - | - | - | - |
| `anthropic.claude-sonnet-4-5-20250929-v1:0` | 200K | 64K | **是** | text,image | - | - | - | - |
| `anthropic.claude-haiku-4-5-20251001-v1:0` | 200K | 64K | **是** | text,image | - | - | - | - |
| `anthropic.claude-fable-5` | 1M | 128K | **是** | text,image | - | - | - | - |
| `amazon.nova-pro-v1:0` | 300K | 5K | 否 | text,image | - | - | - | - |
| `amazon.nova-micro-v1:0` | 128K | 5K | 否 | text | - | - | - | - |
| `amazon.nova-lite-v1:0` | 300K | 5K | 否 | text,image | - | - | - | - |
| `deepseek.v3.2` | 131K | 16K | 否 | text | - | - | - | - |
| `deepseek.r1-v1:0` | 131K | 16K | **是** | text | - | - | - | - |

---

### AMD Radeon（`amd-radeon`） <a id="amd-radeon"></a>

Vendor `amd-radeon` · BaseURL `https://developer.amd.com.cn/radeon/api/v1` · API `openai-chat` · API Key `${AMD_RADEON_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `DeepSeek-V4-Flash` | 1M | 384K | **是** | text | - | - | - | - |
| `Qwen3.8-Flash-Next` | 1M | - | **是** | text | - | - | - | - |

---

### 蚂蚁 Ling（Cerebras）（`ant-ling`） <a id="ant-ling"></a>

BaseURL `https://api.ant-ling.com/v1` · API `openai-chat` · API Key `${ANT_LING_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `Ling-2.6-1T` | 262K | 66K | 否 | text | 0.06 | 0.25 | 0 | 0 |
| `Ling-2.6-flash` | 262K | 66K | 否 | text | 0.01 | 0.02 | 0 | 0 |
| `Ring-2.6-1T` | 262K | 66K | **是** | text | 0.06 | 0.25 | 0 | 0 |

---

### B.AI（`bai`） <a id="bai"></a>

Vendor `bai` · BaseURL `https://api.b.ai/v1` · API `openai-chat` · API Key `${BAI_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `minimax-m3` | 1M | 128K | **是** | text,image,video | - | - | - | - |
| `minimax-m2.7` | 262K | 131K | **是** | text | - | - | - | - |
| `glm-5.1` | 200K | 131K | **是** | text | - | - | - | - |
| `glm-5.2` | 1M | 131K | **是** | text | - | - | - | - |
| `gpt-5.6-sol` | - | - | **是** | text,image | - | - | - | - |
| `gpt-5.6-terra` | - | - | **是** | text,image | - | - | - | - |
| `gpt-5.6-luna` | - | - | **是** | text,image | - | - | - | - |
| `gpt-5.5` | 1M | 128K | **是** | text,image | - | - | - | - |
| `gpt-5.5-instant` | 128K | 16K | **是** | text,image | - | - | - | - |
| `gpt-5.4` | 1M | 128K | **是** | text,image | - | - | - | - |
| `gpt-5.2` | 400K | 128K | **是** | text,image | - | - | - | - |
| `gpt-5.4-pro` | 1M | 128K | **是** | text,image | - | - | - | - |
| `gpt-5.4-mini` | 400K | 128K | **是** | text,image | - | - | - | - |
| `gpt-5-mini` | 400K | 128K | **是** | text,image | - | - | - | - |
| `gpt-5.4-nano` | 400K | 128K | **是** | text,image | - | - | - | - |
| `gpt-5-nano` | 400K | 128K | **是** | text,image | - | - | - | - |
| `claude-fable-5.1` | 1M | 128K | **是** | text,image | - | - | - | - |
| `claude-fable-5` | 1M | 128K | **是** | text,image | - | - | - | - |
| `claude-opus-5` | 1M | 128K | **是** | text,image | - | - | - | - |
| `claude-opus-4.8` | 1M | 128K | **是** | text,image | - | - | - | - |
| `claude-opus-4.7` | 1M | 128K | **是** | text,image | - | - | - | - |
| `claude-opus-4.6` | 1M | 128K | **是** | text,image | - | - | - | - |
| `claude-opus-4.5` | 200K | 64K | **是** | text,image | - | - | - | - |
| `claude-sonnet-5` | 1M | 64K | **是** | text,image | - | - | - | - |
| `claude-sonnet-4.6` | 1M | 64K | **是** | text,image | - | - | - | - |
| `claude-sonnet-4.5` | 1M | 64K | **是** | text,image | - | - | - | - |
| `claude-haiku-4.5` | 200K | 64K | **是** | text,image | - | - | - | - |
| `gemini-3.1-pro` | 1M | 66K | **是** | text,image | - | - | - | - |
| `gemini-3-flash` | 1M | 66K | **是** | text,image | - | - | - | - |
| `gemini-3.5-flash-lite` | 1M | 66K | **是** | text,image | - | - | - | - |
| `gemini-3.6-flash` | 1M | 66K | **是** | text,image | - | - | - | - |
| `hy3` | 262K | 66K | **是** | text | - | - | - | - |
| `mimo-v2.5-pro` | 1M | 131K | **是** | text | - | - | - | - |
| `glm-5.3-flash` | 1M | 131K | **是** | text,image | - | - | - | - |
| `glm-5.3-flashx` | 1M | 131K | **是** | text,image | - | - | - | - |
| `kimi-k3` | 1M | 262K | **是** | text,image | - | - | - | - |
| `glm-5.3` | 1M | 131K | **是** | text | - | - | - | - |
| `kimi-k2.8-preview` | 262K | 262K | **是** | text,image | - | - | - | - |
| `kimi-k2.6` | 262K | 262K | **是** | text,image,video | - | - | - | - |
| `mimo-v2.5` | 1M | 131K | **是** | text,image | - | - | - | - |
| `hy4-preview` | 262K | 66K | **是** | text | - | - | - | - |
| `qwen3.8-flash` | 1M | - | **是** | text,image | - | - | - | - |
| `qwen3.8-max` | 1M | - | **是** | text,image | - | - | - | - |
| `qwen3.8-27b` | 1M | - | **是** | text,image,video | - | - | - | - |
| `deepseek-v4.1-flash` | 1M | - | **是** | text,image | - | - | - | - |
| `gpt-6-astra` | 400K | 128K | **是** | text,image | - | - | - | - |
| `gemini-3.8-flash` | 1M | 66K | **是** | text,image | - | - | - | - |
| `deepseek-v4-pro` | 1M | 384K | **是** | text | - | - | - | - |
| `gemini-3.5-flash` | 1M | 66K | **是** | text,image | - | - | - | - |

---

### Cerebras（`cerebras`） <a id="cerebras"></a>

BaseURL `https://api.cerebras.ai/v1` · API `openai-chat` · API Key `${CEREBRAS_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `gpt-oss-120b` | 131K | 41K | **是** | text | 0.35 | 0.75 | 0 | 0 |
| `zai-glm-4.7` | 131K | 41K | **是** | text | 2.25 | 2.75 | 0 | 0 |

---

### Cloudflare AI Gateway（`cloudflare-ai-gateway`） <a id="cloudflare-ai-gateway"></a>

Vendor `cloudflare-ai-gateway` · BaseURL `https://gateway.ai.cloudflare.com/v1/{ACCOUNT_ID}/{GATEWAY_ID}` · API `openai-chat` · API Key `${CLOUDFLARE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `anthropic/claude-sonnet-4.6` | 1M | 64K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |
| `anthropic/claude-opus-4.8` | 1M | 128K | **是** | text,image | 5 | 25 | 0.5 | 6.25 |
| `openai/gpt-5.4` | 400K | 128K | **是** | text,image | 2.5 | 15 | 0.25 | 0 |
| `openai/gpt-5.2` | 400K | 128K | **是** | text,image | 1.75 | 14 | 0.175 | 0 |
| `google/gemini-2.5-pro` | 1M | 66K | **是** | text,image | 1.25 | 10 | 0.125 | 0 |
| `google/gemini-3.5-flash` | 1M | 66K | **是** | text,image | 1.5 | 9 | 0.15 | 0 |
| `meta-llama/llama-4-scout` | 10M | 16K | 否 | text,image | 0.1 | 0.3 | 0 | 0 |

---

### Cloudflare Workers AI（`cloudflare-workers-ai`） <a id="cloudflare-workers-ai"></a>

Vendor `cloudflare-workers-ai` · BaseURL `https://api.cloudflare.com/client/v4/accounts/{ACCOUNT_ID}/ai/v1` · API `openai-chat` · API Key `${CLOUDFLARE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `@cf/meta/llama-4-scout-17b-16e-instruct` | 131K | 16K | 否 | text,image | 0.27 | 0.85 | 0 | 0 |
| `@cf/meta/llama-3.3-70b-instruct-fp8-fast` | 24K | 24K | 否 | text | 0.293 | 2.253 | 0 | 0 |
| `@cf/google/gemma-4-26b-a4b-it` | 256K | 16K | **是** | text,image | 0.1 | 0.3 | 0 | 0 |
| `@cf/mistralai/mistral-small-3.1-24b-instruct` | 128K | 128K | 否 | text | 0.351 | 0.555 | 0 | 0 |
| `@cf/openai/gpt-oss-120b` | 128K | 16K | **是** | text | 0.35 | 0.75 | 0 | 0 |
| `@cf/openai/gpt-oss-20b` | 128K | 16K | **是** | text | 0.2 | 0.3 | 0 | 0 |
| `@cf/moonshotai/kimi-k2.7-code` | 262K | 262K | **是** | text | 0.95 | 4 | 0.19 | 0 |
| `@cf/zai-org/glm-5.3` | 1M | 131K | **是** | text | 1.4 | 4.4 | 0.26 | 0 |

---

### CodeOK（`codeok`） <a id="codeok"></a>

Vendor `codeok` · BaseURL `https://www.codeok.cc/v1` · API `openai-responses` · API Key `${CODEOK_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `gpt-5.5` | 272K | 128K | **是** | text,image | - | - | - | - |
| `gpt-5.6-sol` | - | - | **是** | text,image | - | - | - | - |
| `gpt-5.6-terra` | - | - | **是** | text,image | - | - | - | - |
| `gpt-5.6-luna` | - | - | **是** | text,image | - | - | - | - |

---

### 天翼云 Coding Plan（`ctyun-plan`） <a id="ctyun-plan"></a>

Vendor `ctyun-plan` · BaseURL `https://wishub-x6.ctyun.cn/coding/v1` · API `openai-chat` · API Key `${CTYUN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `glm-5-turbo` | 200K | 131K | **是** | text,image | - | - | - | - |
| `glm-5-pro` | 200K | 131K | **是** | text,image | - | - | - | - |
| `deepseek-v3.2-pro` | 131K | 66K | **是** | text | - | - | - | - |

---

### Fireworks AI（`fireworks`） <a id="fireworks"></a>

BaseURL `https://api.fireworks.ai/inference` · API `anthropic-messages` · API Key `${FIREWORKS_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `accounts/fireworks/models/deepseek-v4-flash` | 1M | 384K | **是** | text | 0.14 | 0.28 | 0.03 | 0 |
| `accounts/fireworks/models/deepseek-v4-pro` | 1M | 384K | **是** | text | 1.74 | 3.48 | 0.145 | 0 |
| `accounts/fireworks/models/glm-5p1` | 203K | 131K | **是** | text | 1.4 | 4.4 | 0.26 | 0 |
| `accounts/fireworks/models/kimi-k2p7-code` | 262K | 262K | **是** | text | 0.95 | 4 | 0.19 | 0 |
| `accounts/fireworks/routers/kimi-k2p7-code-fast` | 262K | 262K | **是** | text | 2 | 8 | 0.38 | 0 |
| `accounts/fireworks/models/gpt-oss-120b` | 131K | 33K | **是** | text | 0.15 | 0.6 | 0.01 | 0 |
| `accounts/fireworks/models/gpt-oss-20b` | 131K | 33K | **是** | text | 0.07 | 0.3 | 0.035 | 0 |

---

### Gitee AI（`gitee`） <a id="gitee"></a>

Vendor `gitee` · BaseURL `https://ai.gitee.com/v1` · API `openai-chat` · API Key `${GITEE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `deepseek-v4.1-flash` | 1M | - | **是** | text,image | - | - | - | - |
| `qwen3.8-flash` | 1M | - | **是** | text,image | - | - | - | - |
| `glm-5.3-flash` | 1M | 131K | **是** | text,image | - | - | - | - |
| `qwen3.8-max-0902` | 1M | 131K | **是** | text,image | - | - | - | - |
| `glm-5.3` | 1M | 131K | **是** | text | - | - | - | - |
| `kimi-k3` | 1M | 262K | **是** | text,image | - | - | - | - |
| `minimax-m3` | 1M | 128K | **是** | text,image | - | - | - | - |
| `mimo-v2.5-pro` | 1M | 131K | **是** | text,image | - | - | - | - |
| `deepseek-v4-flash` | 1M | 384K | **是** | text | - | - | - | - |
| `deepseek-v4-pro` | 1M | 384K | **是** | text | - | - | - | - |
| `qwen3.8-omni-flash` | 1M | 131K | **是** | text,image,audio,video | - | - | - | - |
| `qwen3.5-flash` | 1M | 66K | **是** | text,image | - | - | - | - |
| `qwen3.6-flash` | 1M | 66K | **是** | text,image | - | - | - | - |
| `qwen3.7-plus` | 1M | 66K | **是** | text,image | - | - | - | - |
| `qwen3.7-max` | 1M | 66K | **是** | text | - | - | - | - |
| `auto` | 1M | - | **是** | text,image | - | - | - | - |
| `qwen3.8-max` | 1M | - | **是** | text,image | - | - | - | - |
| `qwen3.8-27b` | 1M | - | **是** | text,image,video | - | - | - | - |
| `deepseek-v4-flash-0731` | 1M | - | **是** | text | - | - | - | - |
| `deepseek-v4-pro-0813` | 1M | - | **是** | text | - | - | - | - |
| `kimi-k2.5` | 262K | 262K | **是** | text,image,video | - | - | - | - |
| `kimi-k2.6` | 262K | 262K | **是** | text,image,video | - | - | - | - |
| `kimi-k2.7-code` | 262K | 262K | **是** | text | - | - | - | - |
| `minimax-m2.7` | 262K | 131K | **是** | text | - | - | - | - |
| `step-3.7-flash` | 262K | 16K | 否 | text,image | - | - | - | - |
| `glm-5.1` | 200K | 131K | **是** | text | - | - | - | - |
| `glm-5` | 200K | 33K | **是** | text | - | - | - | - |
| `ernie-5.0-thinking` | 131K | 66K | **是** | text | - | - | - | - |
| `gemma-4-26b-a4b-it` | 131K | 33K | **是** | text,image | - | - | - | - |
| `qwen3.6-plus` | 66K | 66K | **是** | text,image | - | - | - | - |

---

### GitHub Copilot（`github-copilot`） <a id="github-copilot"></a>

Vendor `github-copilot` · BaseURL `https://api.individual.githubcopilot.com` · API `openai-chat` · API Key `${COPILOT_GITHUB_TOKEN}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `claude-sonnet-4.6` | 1M | 32K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |
| `claude-opus-4.8` | 200K | 64K | **是** | text,image | 5 | 25 | 0.5 | 6.25 |
| `claude-sonnet-4.5` | 200K | 32K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |
| `claude-haiku-4.5` | 200K | 64K | **是** | text,image | 1 | 5 | 0.1 | 1.25 |
| `claude-fable-5` | 1M | 128K | **是** | text,image | 10 | 50 | 1 | 12.5 |
| `gpt-5.5` | 400K | 128K | **是** | text,image | 5 | 30 | 0.5 | 0 |
| `gpt-5.4` | 400K | 128K | **是** | text,image | 2.5 | 15 | 0.25 | 0 |
| `gpt-5.2` | 400K | 128K | **是** | text,image | 1.75 | 14 | 0.175 | 0 |
| `gemini-2.5-pro` | 128K | 64K | **是** | text,image | 1.25 | 10 | 0.125 | 0 |
| `gemini-3.5-flash` | 200K | 64K | **是** | text,image | 1.5 | 9 | 0.15 | 0 |

---

### Groq（`groq`） <a id="groq"></a>

BaseURL `https://api.groq.com/openai/v1` · API `openai-chat` · API Key `${GROQ_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `llama-3.1-8b-instant` | 131K | 131K | 否 | text | 0.05 | 0.08 | 0 | 0 |
| `llama-3.3-70b-versatile` | 131K | 33K | 否 | text | 0.59 | 0.79 | 0 | 0 |
| `meta-llama/llama-4-scout-17b-16e-instruct` | 131K | 8K | 否 | text,image | 0.11 | 0.34 | 0 | 0 |
| `openai/gpt-oss-120b` | 131K | 66K | **是** | text | 0.15 | 0.6 | 0.075 | 0 |
| `openai/gpt-oss-20b` | 131K | 66K | **是** | text | 0.075 | 0.3 | 0.037 | 0 |
| `openai/gpt-oss-safeguard-20b` | 131K | 66K | **是** | text | 0.075 | 0.3 | 0.037 | 0 |
| `qwen/qwen3-32b` | 131K | 41K | **是** | text | 0.29 | 0.59 | 0 | 0 |

---

### 华为云（ModelArts）（`huawei`） <a id="huawei"></a>

Vendor `huawei` · BaseURL `https://api.modelarts-maas.com/openai/v1` · API `openai-chat` · API Key `${HUAWEI_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `openpangu-2.0-flash` | 524K | 131K | **是** | text,image | - | - | - | - |
| `glm-5.3` | 203K | 131K | **是** | text | - | - | - | - |
| `glm-5.1` | 203K | 131K | **是** | text,image | - | - | - | - |
| `kimi-k2.6` | 262K | 98K | **是** | text,image | - | - | - | - |
| `glm-5` | 203K | 66K | **是** | text,image | - | - | - | - |
| `deepseek-v4-pro` | 1M | 131K | **是** | text | - | - | - | - |
| `deepseek-v4-flash` | 1M | 131K | **是** | text | - | - | - | - |
| `qwen3-235b-a22b` | 131K | 33K | **是** | text,image | - | - | - | - |

---

### 华为云 Coding Plan（`huawei-plan`） <a id="huawei-plan"></a>

Vendor `huawei-plan` · BaseURL `https://api.modelarts-maas.com/plan/v2` · API `openai-chat` · API Key `${HUAWEI_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `glm-5` | 203K | 66K | **是** | text,image | - | - | - | - |
| `glm-5.1` | 203K | 131K | **是** | text,image | - | - | - | - |
| `kimi-k2.6` | 262K | 98K | **是** | text,image | - | - | - | - |
| `deepseek-v3.2` | 131K | 66K | **是** | text | - | - | - | - |
| `deepseek-v4-flash` | 1M | 131K | **是** | text | - | - | - | - |

---

### HuggingFace（`huggingface`） <a id="huggingface"></a>

BaseURL `https://router.huggingface.co/v1` · API `openai-chat` · API Key `${HUGGINGFACE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `MiniMaxAI/MiniMax-M2.1` | 205K | 131K | **是** | text | 0.3 | 1.2 | 0 | 0 |
| `MiniMaxAI/MiniMax-M2.5` | 205K | 131K | **是** | text | 0.3 | 1.2 | 0.03 | 0 |
| `MiniMaxAI/MiniMax-M2.7` | 205K | 131K | **是** | text | 0.3 | 1.2 | 0.06 | 0 |
| `Qwen/Qwen3-235B-A22B-Thinking-2507` | 262K | 131K | **是** | text | 0.3 | 3 | 0 | 0 |
| `Qwen/Qwen3-Coder-480B-A35B-Instruct` | 262K | 67K | 否 | text | 2 | 2 | 0 | 0 |

---

### 京东智联云 JD Plan（`jd-plan`） <a id="jd-plan"></a>

Vendor `jd-plan` · BaseURL `https://agentrs.jd.com/api/saas/openai-u/v1` · API `openai-chat` · API Key `${JD_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `glm-5` | 200K | 66K | **是** | text,image | - | - | - | - |
| `glm-5.1` | 200K | 131K | **是** | text,image | - | - | - | - |
| `glm-5.3` | 1M | 131K | **是** | text | - | - | - | - |
| `qwen3.7-plus` | 1M | 66K | **是** | text,image | - | - | - | - |
| `qwen3.7-max` | 1M | 66K | **是** | text | - | - | - | - |
| `deepseek-v4-flash` | 1M | 131K | **是** | text | - | - | - | - |
| `deepseek-v4-pro` | 1M | 131K | **是** | text | - | - | - | - |
| `kimi-k2.6` | 262K | 98K | **是** | text,image | - | - | - | - |
| `minimax-m2.7` | 205K | 131K | **是** | text | - | - | - | - |
| `joyai-llm-flash` | 128K | 33K | 否 | text | - | - | - | - |

---

### Kimi Coding（`kimi-coding`） <a id="kimi-coding"></a>

BaseURL `https://api.kimi.com/coding/v1` · API `openai-chat` · Thinking `kimi` · API Key `${KIMI_CODING_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `kimi-for-coding` | 262K | 33K | **是** | text,image | 0 | 0 | 0 | 0 |
| `kimi-k2-thinking` | 262K | 33K | **是** | text | 0 | 0 | 0 | 0 |
| `k3` | 1M | 131K | **是** | text,image | 0 | 0 | 0 | 0 |
| `k3-256k` | 262K | - | **是** | text,image | 0 | 0 | 0 | 0 |

---

### LongCat（龙猫）（`longcat`） <a id="longcat"></a>

Vendor `longcat` · BaseURL `https://api.longcat.chat/openai` · API `openai-chat` · API Key `${LONGCAT_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `LongCat-2.0` | 1M | 131K | **是** | text | - | - | - | - |

---

### MiniMax（`minimax`） <a id="minimax"></a>

Vendor `minimax` · BaseURL `https://api.minimaxi.com/anthropic` · API `anthropic-messages` · API Key `${MINIMAX_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `MiniMax-M3` | 1M | 128K | **是** | text,image,video | - | - | - | - |
| `MiniMax-M2.7` | 205K | 131K | **是** | text | - | - | - | - |
| `MiniMax-M2.7-highspeed` | 205K | 131K | **是** | text | - | - | - | - |
| `MiniMax-M2.5` | 197K | 131K | **是** | text | - | - | - | - |
| `MiniMax-M2.5-highspeed` | 197K | 131K | **是** | text | - | - | - | - |

---

### Mistral（`mistral`） <a id="mistral"></a>

Vendor `mistral` · BaseURL `https://api.mistral.ai/v1` · API `openai-chat` · API Key `${MISTRAL_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `codestral-latest` | 256K | 4K | 否 | text | 0.3 | 0.9 | 0.03 | 0 |
| `devstral-2512` | 262K | 262K | 否 | text | 0.4 | 2 | 0.04 | 0 |
| `devstral-latest` | 262K | 262K | 否 | text | 0.4 | 2 | 0.04 | 0 |
| `devstral-medium-2507` | 128K | 128K | 否 | text | 0.4 | 2 | 0.04 | 0 |
| `devstral-medium-latest` | 262K | 262K | 否 | text | 0.4 | 2 | 0.04 | 0 |
| `devstral-small-2505` | 128K | 128K | 否 | text | 0.1 | 0.3 | 0.01 | 0 |
| `devstral-small-2507` | 128K | 128K | 否 | text | 0.1 | 0.3 | 0.01 | 0 |
| `labs-devstral-small-2512` | 256K | 256K | 否 | text,image | - | - | - | - |
| `magistral-medium-latest` | 128K | 16K | **是** | text | 2 | 5 | 0.2 | 0 |
| `magistral-small` | 128K | 128K | **是** | text | 0.5 | 1.5 | 0.05 | 0 |
| `ministral-3b-latest` | 128K | 128K | 否 | text | 0.04 | 0.04 | 0.004 | 0 |
| `ministral-8b-latest` | 128K | 128K | 否 | text | 0.1 | 0.1 | 0.01 | 0 |
| `mistral-large-2411` | 131K | 16K | 否 | text | 2 | 6 | 0.2 | 0 |
| `mistral-large-2512` | 262K | 262K | 否 | text,image | 0.5 | 1.5 | 0.05 | 0 |
| `mistral-large-latest` | 262K | 262K | 否 | text,image | 0.5 | 1.5 | 0.05 | 0 |
| `mistral-medium-2505` | 131K | 131K | 否 | text,image | 0.4 | 2 | 0.04 | 0 |
| `mistral-medium-2508` | 262K | 262K | 否 | text,image | 0.4 | 2 | 0.04 | 0 |
| `mistral-medium-2604` | 262K | 262K | **是** | text,image | 1.5 | 7.5 | 0.15 | 0 |
| `mistral-medium-3.5` | 262K | 262K | **是** | text,image | 1.5 | 7.5 | 0 | 0 |
| `mistral-medium-latest` | 262K | 262K | 否 | text,image | 0.4 | 2 | 0.04 | 0 |
| `mistral-nemo` | 128K | 128K | 否 | text | 0.15 | 0.15 | 0.015 | 0 |
| `mistral-small-2506` | 128K | 16K | 否 | text,image | 0.1 | 0.3 | 0.01 | 0 |
| `mistral-small-2603` | 256K | 256K | **是** | text,image | 0.15 | 0.6 | 0.015 | 0 |
| `mistral-small-latest` | 256K | 256K | **是** | text,image | 0.15 | 0.6 | 0.015 | 0 |
| `open-mistral-7b` | 8K | 8K | 否 | text | 0.25 | 0.25 | 0.025 | 0 |
| `open-mistral-nemo` | 128K | 128K | 否 | text | 0.15 | 0.15 | 0.015 | 0 |
| `open-mixtral-8x22b` | 64K | 64K | 否 | text | 2 | 6 | 0.2 | 0 |
| `open-mixtral-8x7b` | 32K | 32K | 否 | text | 0.7 | 0.7 | 0.07 | 0 |
| `pixtral-12b` | 128K | 128K | 否 | text,image | 0.15 | 0.15 | 0.015 | 0 |
| `pixtral-large-latest` | 128K | 128K | 否 | text,image | 2 | 6 | 0.2 | 0 |

---

### ModelScope（魔搭社区）（`modelscope`） <a id="modelscope"></a>

BaseURL `https://api-inference.modelscope.cn/v1` · API `openai-chat` · API Key `${MODELSCOPE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `deepseek-ai/DeepSeek-V4-Flash-0731` | 1M | 384K | **是** | text | - | - | - | - |
| `deepseek-ai/DeepSeek-V4-Pro` | 1M | 384K | **是** | text | - | - | - | - |
| `deepseek-ai/DeepSeek-V4-Pro-0813` | 1M | 384K | **是** | text | - | - | - | - |
| `MedAIBase/AntAngelMed` | 131K | 16K | 否 | text | - | - | - | - |
| `meituan-longcat/LongCat-Flash-Lite` | 262K | 33K | 否 | text | - | - | - | - |
| `MiniMax/MiniMax-M1-80k` | 1M | 80K | **是** | text | - | - | - | - |
| `MiniMax/MiniMax-M3` | 1M | 128K | **是** | text,image,video | - | - | - | - |
| `mistralai/Mistral-Large-Instruct-2407` | 131K | 33K | 否 | text | - | - | - | - |
| `MusePublic/Qwen-Image-Edit` | 33K | 16K | 否 | text,image | - | - | - | - |
| `opencompass/CompassJudger-1-32B-Instruct` | 16K | 4K | 否 | text | - | - | - | - |
| `OpenGVLab/InternVL3_5-241B-A28B` | 66K | 16K | 否 | text,image,video | - | - | - | - |
| `PaddlePaddle/ERNIE-4.5-0.3B-PT` | 131K | 66K | 否 | text | - | - | - | - |
| `PaddlePaddle/ERNIE-4.5-21B-A3B-PT` | 131K | 66K | 否 | text | - | - | - | - |
| `PaddlePaddle/ERNIE-4.5-300B-A47B-PT` | 131K | 66K | 否 | text | - | - | - | - |
| `PaddlePaddle/ERNIE-4.5-VL-28B-A3B-PT` | 131K | 66K | 否 | text,image | - | - | - | - |
| `Qwen/Qwen-Image-Edit` | 33K | 16K | 否 | text,image | - | - | - | - |
| `Qwen/Qwen3-14B` | 131K | 39K | 否 | text | - | - | - | - |
| `Qwen/Qwen3-235B-A22B` | 131K | 39K | 否 | text | - | - | - | - |
| `Qwen/Qwen3-235B-A22B-Instruct-2507` | 262K | 66K | 否 | text | - | - | - | - |
| `Qwen/Qwen3-235B-A22B-Thinking-2507` | 262K | 82K | **是** | text | - | - | - | - |
| `Qwen/Qwen3-30B-A3B` | 131K | 39K | 否 | text | - | - | - | - |
| `Qwen/Qwen3-30B-A3B-Thinking-2507` | 262K | 82K | **是** | text | - | - | - | - |
| `Qwen/Qwen3-4B` | 131K | 39K | 否 | text | - | - | - | - |
| `Qwen/Qwen3-8B` | 131K | 39K | 否 | text | - | - | - | - |
| `Qwen/Qwen3-Coder-30B-A3B-Instruct` | 262K | 66K | 否 | text | - | - | - | - |
| `Qwen/Qwen3-Next-80B-A3B-Instruct` | 262K | 66K | 否 | text | - | - | - | - |
| `Qwen/Qwen3-Next-80B-A3B-Thinking` | 262K | 82K | **是** | text | - | - | - | - |
| `Qwen/Qwen3-VL-235B-A22B-Instruct` | 262K | 33K | 否 | text,image,video | - | - | - | - |
| `Qwen/Qwen3-VL-8B-Instruct` | 262K | 33K | 否 | text,image,video | - | - | - | - |
| `Qwen/Qwen3-VL-8B-Thinking` | 262K | 41K | **是** | text,image,video | - | - | - | - |
| `Qwen/Qwen3.5-122B-A10B` | 262K | 82K | **是** | text,image,video | - | - | - | - |
| `Qwen/Qwen3.5-27B` | 262K | 82K | **是** | text,image,video | - | - | - | - |
| `Qwen/Qwen3.5-35B-A3B` | 262K | 82K | **是** | text,image,video | - | - | - | - |
| `Qwen/Qwen3.5-397B-A17B` | 1M | 130K | **是** | text | - | - | - | - |
| `Qwen/Qwen3.8-27B` | 262K | 131K | **是** | text,image,video | - | - | - | - |
| `Shanghai_AI_Laboratory/Intern-S1` | 131K | 33K | **是** | text,image,video | - | - | - | - |
| `Shanghai_AI_Laboratory/Intern-S1-mini` | 131K | 33K | **是** | text,image,video | - | - | - | - |
| `Shanghai_AI_Laboratory/Intern-S2-Preview` | 131K | 33K | **是** | text,image | - | - | - | - |
| `stepfun-ai/Step-3.5-Flash` | 262K | 33K | **是** | text | - | - | - | - |
| `stepfun-ai/Step-3.7-Flash` | 262K | 33K | **是** | text,image | - | - | - | - |
| `Tencent-Hunyuan/Hy3` | 262K | 131K | **是** | text | - | - | - | - |
| `XGenerationLab/XiYanSQL-QwenCoder-32B-2412` | 33K | 16K | 否 | text | - | - | - | - |
| `XGenerationLab/XiYanSQL-QwenCoder-32B-2504` | 33K | 16K | 否 | text | - | - | - | - |
| `ZhipuAI/GLM-4.7-Flash` | 262K | 131K | **是** | text | - | - | - | - |
| `ZhipuAI/GLM-5.2` | 1M | 131K | **是** | text | - | - | - | - |

---

### 月之暗面（Moonshot / Kimi）（`moonshotai`） <a id="moonshotai"></a>

BaseURL `https://api.moonshot.ai/v1` · API `openai-chat` · API Key `${MOONSHOTAI_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `kimi-k2-0711-preview` | 131K | 16K | 否 | text | 0.6 | 2.5 | 0.15 | 0 |
| `kimi-k2-0905-preview` | 262K | 262K | 否 | text | 0.6 | 2.5 | 0.15 | 0 |
| `kimi-k2-thinking` | 262K | 262K | **是** | text | 0.6 | 2.5 | 0.15 | 0 |
| `kimi-k2-thinking-turbo` | 262K | 262K | **是** | text | 1.15 | 8 | 0.15 | 0 |
| `kimi-k2-turbo-preview` | 262K | 262K | 否 | text | 2.4 | 10 | 0.6 | 0 |
| `kimi-k2.7-code` | 262K | 262K | **是** | text | 0.95 | 4 | 0.19 | 0 |
| `kimi-k2.7-code-highspeed` | 262K | 262K | **是** | text | 1.9 | 8 | 0.38 | 0 |

---

### 月之暗面（国内）（`moonshotai-cn`） <a id="moonshotai-cn"></a>

BaseURL `https://api.moonshot.cn/v1` · API `openai-chat` · API Key `${MOONSHOTAI_CN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `kimi-k2-0711-preview` | 131K | 16K | 否 | text | 0.6 | 2.5 | 0.15 | 0 |
| `kimi-k2-0905-preview` | 262K | 262K | 否 | text | 0.6 | 2.5 | 0.15 | 0 |
| `kimi-k2-thinking` | 262K | 262K | **是** | text | 0.6 | 2.5 | 0.15 | 0 |
| `kimi-k2-thinking-turbo` | 262K | 262K | **是** | text | 1.15 | 8 | 0.15 | 0 |
| `kimi-k2-turbo-preview` | 262K | 262K | 否 | text | 2.4 | 10 | 0.6 | 0 |
| `kimi-k2.7-code` | 262K | 262K | **是** | text | 0.95 | 4 | 0.19 | 0 |
| `kimi-k2.7-code-highspeed` | 262K | 262K | **是** | text | 1.9 | 8 | 0.38 | 0 |

---

### 摩尔线程 Coding Plan（`mthreads-plan`） <a id="mthreads-plan"></a>

Vendor `mthreads-plan` · BaseURL `https://coding-plan-endpoint.kuaecloud.net/v1` · API `openai-chat` · API Key `${MTHREADS_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `glm-4.7` | 200K | 131K | **是** | text,image | - | - | - | - |

---

### Nvidia NIM（`nvidia`） <a id="nvidia"></a>

BaseURL `https://integrate.api.nvidia.com/v1` · API `openai-chat` · API Key `${NVIDIA_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `meta/llama-3.1-70b-instruct` | 128K | 4K | 否 | text | 0 | 0 | 0 | 0 |
| `meta/llama-3.1-8b-instruct` | 16K | 4K | 否 | text | 0 | 0 | 0 | 0 |
| `meta/llama-3.2-11b-vision-instruct` | 128K | 4K | 否 | text,image | 0 | 0 | 0 | 0 |
| `meta/llama-3.2-90b-vision-instruct` | 128K | 8K | 否 | text,image | 0 | 0 | 0 | 0 |
| `meta/llama-3.3-70b-instruct` | 128K | 4K | 否 | text | 0 | 0 | 0 | 0 |

---

### CodePlayz（Opencode）（`opencode`） <a id="opencode"></a>

BaseURL `https://opencode.ai/zen/v1` · API `openai-chat` · API Key `${OPENCODE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `big-pickle` | 200K | 32K | **是** | text | 0 | 0 | 0 | 0 |
| `claude-haiku-4-5` | 200K | 64K | **是** | text,image | 1 | 5 | 0.1 | 1.25 |
| `claude-opus-4-1` | 200K | 32K | **是** | text,image | 15 | 75 | 1.5 | 18.75 |
| `claude-opus-4-5` | 200K | 64K | **是** | text,image | 5 | 25 | 0.5 | 6.25 |
| `claude-opus-4-6` | 1M | 128K | **是** | text,image | 5 | 25 | 0.5 | 6.25 |

---

### CodePlayz（Opencode Go）（`opencode-go`） <a id="opencode-go"></a>

BaseURL `https://opencode.ai/zen/go/v1` · API `openai-chat` · API Key `${OPENCODE_GO_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `deepseek-v4-flash` | 1M | 384K | **是** | text | 0.14 | 0.28 | 0.003 | 0 |
| `deepseek-v4-pro` | 1M | 384K | **是** | text | 1.74 | 3.48 | 0.015 | 0 |
| `glm-5` | 200K | 33K | **是** | text | 1 | 3.2 | 0.2 | 0 |
| `glm-5.1` | 200K | 33K | **是** | text | 1.4 | 4.4 | 0.26 | 0 |
| `glm-5.3` | 1M | 131K | **是** | text | 1.4 | 4.4 | 0.26 | 0 |
| `kimi-k2.6` | 262K | 66K | **是** | text,image | 0.95 | 4 | 0.16 | 0 |
| `kimi-k2.7-code` | 262K | 262K | **是** | text | 0.95 | 4 | 0.19 | 0 |

---

### OpenRouter（`openrouter`） <a id="openrouter"></a>

Vendor `openrouter` · BaseURL `https://openrouter.ai/api/v1` · API `openai-chat` · API Key `${OPENROUTER_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `anthropic/claude-sonnet-4.6` | 1M | 64K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |
| `anthropic/claude-opus-4.8` | 1M | 128K | **是** | text,image | 5 | 25 | 0.5 | 6.25 |
| `anthropic/claude-sonnet-4.5` | 1M | 64K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |
| `anthropic/claude-haiku-4.5` | 200K | 64K | **是** | text,image | 1 | 5 | 0.1 | 1.25 |
| `openai/gpt-5.5` | 1M | 128K | **是** | text,image | 5 | 30 | 0.5 | 0 |
| `openai/gpt-5.5-pro` | 1M | 128K | **是** | text,image | 30 | 180 | 0 | 0 |
| `openai/gpt-5.4` | 1M | 128K | **是** | text,image | 2.5 | 15 | 0.25 | 0 |
| `google/gemini-3.5-flash` | 1M | 66K | **是** | text,image | 1.5 | 9 | 0.15 | 0.083 |
| `google/gemini-2.5-pro` | 1M | 66K | **是** | text,image | 1.25 | 10 | 0.125 | 0.375 |
| `deepseek/deepseek-v4-flash` | 1M | 66K | **是** | text | 0.09 | 0.18 | 0.02 | 0 |
| `deepseek/deepseek-v4-pro` | 1M | 384K | **是** | text | 0.435 | 0.87 | 0.004 | 0 |
| `qwen/qwen3.7-plus` | 1M | 66K | **是** | text,image | 0.32 | 1.28 | 0.064 | 0.4 |
| `moonshotai/kimi-k2.7-code` | 262K | 262K | **是** | text | 0.612 | 3.069 | 0.13 | 0 |
| `minimax/minimax-m3` | 1M | 4K | **是** | text,image | 0.3 | 1.2 | 0.06 | 0 |
| `meta-llama/llama-4-scout` | 10M | 16K | 否 | text,image | 0.1 | 0.3 | 0 | 0 |
| `z-ai/glm-5` | 200K | 4K | **是** | text | 0.6 | 1.9 | 0.119 | 0 |
| `z-ai/glm-5.3` | 1M | 131K | **是** | text | 1.2 | 4.1 | 0.2 | 0 |
| `x-ai/grok-4.3` | 1M | 4K | **是** | text,image | 1.25 | 2.5 | 0.2 | 0 |

---

### OpenRouter 免费模型（`openrouter-free-models`） <a id="openrouter-free-models"></a>

Vendor `openrouter` · BaseURL `https://openrouter.ai/api/v1` · API `openai-chat` · API Key `${OPENROUTER_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `google/gemma-4-26b-a4b-it` | 262K | 33K | **是** | text,image,video | 0 | 0 | 0 | 0 |
| `google/gemma-4-31b-it` | 262K | 33K | **是** | text,image,video | 0 | 0 | 0 | 0 |
| `nvidia/nemotron-3-nano-omni-30b-a3b-reasoning` | 256K | 66K | **是** | text,image,video | 0 | 0 | 0 | 0 |
| `nvidia/nemotron-3-super-120b-a12b` | 1M | 262K | **是** | text | 0 | 0 | 0 | 0 |
| `nvidia/nemotron-3-ultra-550b-a55b` | 1M | 10K | **是** | text | 0 | 0 | 0 | 0 |
| `nvidia/nemotron-3.5-content-safety` | 128K | 8K | **是** | text,image | 0 | 0 | 0 | 0 |
| `nvidia/nemotron-nano-12b-v2-vl` | 128K | 128K | **是** | text,image,video | 0 | 0 | 0 | 0 |
| `openai/gpt-oss-20b` | 131K | 33K | **是** | text | 0 | 0 | 0 | 0 |
| `poolside/laguna-m.1` | 262K | 33K | **是** | text | 0 | 0 | 0 | 0 |
| `poolside/laguna-xs-2.1` | 262K | 33K | **是** | text | 0 | 0 | 0 | 0 |
| `tencent/hy3` | 262K | 38K | **是** | text | 0 | 0 | 0 | 0 |

---

### 百度千帆 Code Plan（`qianfan-code-plan`） <a id="qianfan-code-plan"></a>

Vendor `qianfan` · BaseURL `https://qianfan.baidubce.com/v2` · API `openai-chat` · API Key `${QIANFAN_CODE_PLAN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `qianfan-code-latest` | 1M | 66K | **是** | text | - | - | - | - |
| `deepseek-v4-flash` | 1M | 384K | **是** | text,image | - | - | - | - |
| `glm-5.1` | 200K | 131K | **是** | text,image | - | - | - | - |
| `deepseek-v4-pro` | 1M | 384K | **是** | text,image | - | - | - | - |

---

### 百度千帆 Token Plan（`qianfan-token-plan`） <a id="qianfan-token-plan"></a>

Vendor `qianfan` · BaseURL `https://qianfan.baidubce.com/v2/tokenplan/personal` · API `openai-chat` · API Key `${QIANFAN_TOKEN_PLAN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `deepseek-v4-pro` | 1M | 384K | **是** | text,image | - | - | - | - |
| `deepseek-v4-flash` | 1M | 384K | **是** | text,image | - | - | - | - |
| `glm-5.3` | 1M | 131K | **是** | text | - | - | - | - |
| `glm-5.1` | 200K | 131K | **是** | text,image | - | - | - | - |
| `kimi-k2.6` | 262K | 262K | **是** | text,image,video | - | - | - | - |
| `ernie-5.1` | 131K | 66K | **是** | text | - | - | - | - |

---

### 阶跃星辰（StepFun）（`stepfun`） <a id="stepfun"></a>

BaseURL `https://api.stepfun.com/step_plan/v1` · API `openai-chat` · API Key `${STEPFUN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `step-3.7-flash` | 262K | 16K | 否 | text,image | - | - | - | - |

---

### 腾讯混元（Tencent Hunyuan）（`tencent-hy-plan`） <a id="tencent-hy-plan"></a>

Vendor `tencent-hy-plan` · BaseURL `https://api.lkeap.cloud.tencent.com/plan/v3` · API `openai-chat` · API Key `${TENCENT_HY_PLAN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `hy3` | 262K | 66K | **是** | text | - | - | - | - |

---

### Together AI（`together`） <a id="together"></a>

BaseURL `https://api.together.ai/v1` · API `openai-chat` · API Key `${TOGETHER_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `MiniMaxAI/MiniMax-M2.7` | 203K | 131K | **是** | text | 0.3 | 1.2 | 0.06 | 0 |
| `MiniMaxAI/MiniMax-M3` | 524K | 250K | **是** | text,image | 0.3 | 1.2 | 0.06 | 0 |
| `Qwen/Qwen2.5-7B-Instruct-Turbo` | 33K | 33K | 否 | text | 0.3 | 0.3 | 0 | 0 |
| `Qwen/Qwen3-235B-A22B-Instruct-2507-tput` | 262K | 262K | 否 | text | 0.2 | 0.6 | 0 | 0 |
| `Qwen/Qwen3.5-397B-A17B` | 262K | 130K | **是** | text,image | 0.6 | 3.6 | 0 | 0 |

---

### Vercel AI Gateway（`vercel-ai-gateway`） <a id="vercel-ai-gateway"></a>

BaseURL `https://ai-gateway.vercel.sh` · API `anthropic-messages` · API Key `${VERCEL_AI_GATEWAY_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `anthropic/claude-sonnet-4.6` | 1M | 64K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |
| `anthropic/claude-opus-4.8` | 1M | 128K | **是** | text,image | 5 | 25 | 0.5 | 6.25 |
| `anthropic/claude-sonnet-4.5` | 1M | 64K | **是** | text,image | 3 | 15 | 0.3 | 3.75 |
| `anthropic/claude-haiku-4.5` | 200K | 64K | **是** | text,image | 1 | 5 | 0.1 | 1.25 |
| `openai/gpt-5.5` | 1M | 128K | **是** | text,image | 5 | 30 | 0.5 | 0 |
| `openai/gpt-5.4` | 1M | 128K | **是** | text,image | 2.5 | 15 | 0.25 | 0 |
| `google/gemini-3.5-flash` | 1M | 66K | **是** | text,image | 1.5 | 9 | 0.15 | 0.083 |
| `deepseek/deepseek-v4-flash` | 1M | 66K | **是** | text | 0.09 | 0.18 | 0.02 | 0 |
| `deepseek/deepseek-v4-pro` | 1M | 384K | **是** | text | 0.435 | 0.87 | 0.004 | 0 |
| `alibaba/qwen3.6-plus` | 66K | 66K | **是** | text,image | 0.325 | 1.95 | 0 | 0.406 |
| `minimax/minimax-m3` | 1M | 4K | **是** | text,image | 0.3 | 1.2 | 0.06 | 0 |
| `moonshotai/kimi-k2.7-code` | 262K | 262K | **是** | text | 0.612 | 3.069 | 0.13 | 0 |
| `xai/grok-4.3` | 1M | 4K | **是** | text,image | 1.25 | 2.5 | 0.2 | 0 |
| `zai/glm-5.3` | 1M | 131K | **是** | text | 1.2 | 4.1 | 0.2 | 0 |

---

### xAI（Grok）（`xai`） <a id="xai"></a>

BaseURL `https://api.x.ai/v1` · API `openai-chat` · API Key `${XAI_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `grok-3` | 131K | 8K | 否 | text | 3 | 15 | 0.75 | 0 |
| `grok-3-fast` | 131K | 8K | 否 | text | 5 | 25 | 1.25 | 0 |
| `grok-4.20-0309-non-reasoning` | 1M | 30K | 否 | text,image | 1.25 | 2.5 | 0.2 | 0 |
| `grok-4.20-0309-reasoning` | 1M | 30K | **是** | text,image | 1.25 | 2.5 | 0.2 | 0 |
| `grok-4.3` | 1M | 30K | **是** | text,image | 1.25 | 2.5 | 0.2 | 0 |
| `grok-build-0.1` | 256K | 256K | **是** | text,image | 1 | 2 | 0.2 | 0 |
| `grok-code-fast-1` | 33K | 8K | 否 | text | 0.2 | 1.5 | 0.02 | 0 |

---

### YesCode（`yescode`） <a id="yescode"></a>

Vendor `yescode` · BaseURL `https://co.yes.vg/v1` · API `openai-responses` · API Key `${YESCODE_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `gpt-5.5` | 272K | 128K | **是** | text,image | - | - | - | - |
| `gpt-5.6-sol` | - | - | **是** | text,image | - | - | - | - |
| `gpt-5.6-terra` | - | - | **是** | text,image | - | - | - | - |
| `gpt-5.6-luna` | - | - | **是** | text,image | - | - | - | - |

---

### 智谱 AI（Z.AI）（`zai`） <a id="zai"></a>

Vendor `zai` · BaseURL `https://api.z.ai/api/coding/paas/v4` · API `openai-chat` · Thinking `zai` · API Key `${ZAI_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `glm-4.5-air` | 131K | 98K | **是** | text | 0 | 0 | 0 | 0 |
| `glm-4.7` | 205K | 131K | **是** | text | 0 | 0 | 0 | 0 |
| `glm-5-turbo` | 200K | 131K | **是** | text | 0 | 0 | 0 | 0 |
| `glm-5.1` | 200K | 131K | **是** | text | 0 | 0 | 0 | 0 |
| `glm-5.3` | 1M | 131K | **是** | text | 0 | 0 | 0 | 0 |
| `glm-5.3-flash` | 1M | 131K | **是** | text,image | 0 | 0 | 0 | 0 |
| `glm-5v-turbo` | 200K | 131K | **是** | text,image | 0 | 0 | 0 | 0 |

---

### 智谱 AI Coding（国内）（`zai-coding-cn`） <a id="zai-coding-cn"></a>

Vendor `zai` · BaseURL `https://open.bigmodel.cn/api/coding/paas/v4` · API `openai-chat` · Thinking `zai` · API Key `${ZAI_CODING_CN_API_KEY}`

| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |
|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|
| `glm-4.5-air` | 131K | 98K | **是** | text | 0 | 0 | 0 | 0 |
| `glm-4.7` | 205K | 131K | **是** | text | 0 | 0 | 0 | 0 |
| `glm-5-turbo` | 200K | 131K | **是** | text | 0 | 0 | 0 | 0 |
| `glm-5.1` | 200K | 131K | **是** | text | 0 | 0 | 0 | 0 |
| `glm-5.3` | 1M | 131K | **是** | text | 0 | 0 | 0 | 0 |
| `glm-5.3-flash` | 1M | 131K | **是** | text,image | 0 | 0 | 0 | 0 |
| `glm-5v-turbo` | 200K | 131K | **是** | text,image | 0 | 0 | 0 | 0 |

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

## 汇总统计

- 支持推理（reasoning）的模型：**476**

### 上下文长度分布

| 区间 | 数量 |
|------|------|
| 1M+ | 233 |
| 500K-1M | 6 |
| 200K-500K | 235 |
| 100K-200K | 86 |
| 32K-100K | 12 |
| <32K | 5 |
| 未设置 | 12 |

### 输入模态分布

| 模态 | 数量 |
|------|------|
| text/image | 321 |
| text | 234 |
| text/image/video | 32 |
| text/image/audio/video | 2 |
