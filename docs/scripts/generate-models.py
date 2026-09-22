#!/usr/bin/env python3
"""Generate docs/provider-model-list.md — the MothX built-in provider/model catalog.

The catalog is derived from the built-in provider presets in
`internal/config/settings.go` so it always reflects what ships in the binary.
"""

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SRC = ROOT / "internal/config/settings.go"
OUT = ROOT / "docs/provider-model-list.md"

MODEL_ARRAY = "Models: []ModelConfig{"

# Chinese display names for the built-in providers. Providers without an entry
# fall back to their raw ID.
PROVIDER_LABELS = {
    "anthropic": "Anthropic（官方）",
    "openai": "OpenAI（官方）",
    "codeok": "CodeOK",
    "yescode": "YesCode",
    "deepseek-openai": "DeepSeek（官方）",
    "deepseek-anthropic": "DeepSeek（官方 · Anthropic）",
    "google-gemini": "Google Gemini",
    "google-vertex": "Google Vertex AI",
    "xiaomi": "小米 MiMo",
    "xiaomi-token-plan-ams": "小米 MiMo Token Plan（AMS）",
    "xiaomi-token-plan-cn": "小米 MiMo Token Plan（CN）",
    "xiaomi-token-plan-sgp": "小米 MiMo Token Plan（SGP）",
    "volcengine": "火山引擎（Volcengine）",
    "volcengine-agentplan": "火山引擎 Agent Plan",
    "volcengine-codingplan": "火山引擎 Coding Plan",
    "openrouter": "OpenRouter",
    "openrouter-free-models": "OpenRouter 免费模型",
    "minimax": "MiniMax",
    "minimax-anthropic": "MiniMax（Anthropic）",
    "minimax-cn-anthropic": "MiniMax 国内（Anthropic）",
    "zai": "智谱 AI（Z.AI）",
    "zai-coding-cn": "智谱 AI Coding（国内）",
    "modelscope": "ModelScope（魔搭社区）",
    "alibaba-standard": "阿里云百炼（标准）",
    "alibaba-coding-plan": "阿里云百炼 Coding Plan",
    "alibaba-token-plan": "阿里云百炼 Token Plan",
    "huawei": "华为云（ModelArts）",
    "huawei-plan": "华为云 Coding Plan",
    "gitee": "Gitee AI",
    "moark": "Moark",
    "moonshotai": "月之暗面（Moonshot / Kimi）",
    "moonshotai-cn": "月之暗面（国内）",
    "kimi-coding": "Kimi Coding",
    "xai": "xAI（Grok）",
    "fireworks": "Fireworks AI",
    "together": "Together AI",
    "nvidia": "Nvidia NIM",
    "mistral": "Mistral",
    "huggingface": "HuggingFace",
    "groq": "Groq",
    "cerebras": "Cerebras",
    "ant-ling": "蚂蚁 Ling（Cerebras）",
    "opencode": "CodePlayz（Opencode）",
    "opencode-go": "CodePlayz（Opencode Go）",
    "vercel-ai-gateway": "Vercel AI Gateway",
    "github-copilot": "GitHub Copilot",
    "cloudflare-ai-gateway": "Cloudflare AI Gateway",
    "cloudflare-workers-ai": "Cloudflare Workers AI",
    "amazon-bedrock": "Amazon Bedrock",
    "longcat": "LongCat（龙猫）",
    "longcat-anthropic": "LongCat（Anthropic）",
    "qianfan-code-plan": "百度千帆 Code Plan",
    "qianfan-token-plan": "百度千帆 Token Plan",
    "mthreads-plan": "摩尔线程 Coding Plan",
    "ctyun-plan": "天翼云 Coding Plan",
    "jd-plan": "京东智联云 JD Plan",
    "tencent-hy-plan": "腾讯混元（Tencent Hunyuan）",
    "tencent-hy-plan-anthropic": "腾讯混元（Anthropic）",
    "stepfun": "阶跃星辰（StepFun）",
    "amd-radeon": "AMD Radeon",
    "agnes": "Agnes AI（国际版）",
    "agnes-cn": "Agnes AI（国内版）",
    "bai": "B.AI",
}

API_TYPES = """## API 类型说明

| API 协议 | 说明 |
|----------|------|
| `anthropic-messages` | Anthropic Messages API（原生协议） |
| `openai-chat` | OpenAI Chat Completions API（兼容协议） |
| `openai-responses` | OpenAI Responses API（o1/o3 等模型专用） |
| `google-gemini` | Google Gemini API（原生协议） |
| `google-vertex` | Google Vertex AI API（原生协议） |"""

THINKING_FORMATS = """## ThinkingFormat 说明

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
| 空（默认） | 使用标准 OpenAI thinking 或原生协议 |"""

CONFIG_FIELDS = """## 通用配置字段说明

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

内置默认值目前为 `gitee`/`moark` 5 张、官方 OpenAI 1500 张。OpenAI 文档给出单请求最多 1500 个图片输入；xAI 文档标为不限制；Gemini 文档给出 3600 个图片文件；Anthropic 文档按上下文模型给出 100/600 张；Mistral 明确说明上限取决于模型和总 token 预算。因此没有稳定固定上限的 provider 默认保持 `0`，可按实际网关在 `settings.json` 中覆盖。"""

THINKING_LEVELS = """## Thinking Levels

```go
off      // 关闭推理
minimal  // 最少推理
low      // 低
medium   // 中（默认）
high     // 高
xhigh    // 最高
```"""


def match_brace(text, index):
    """Return the index of the brace matching text[index] (must be '{')."""
    assert text[index] == "{", text[index]
    depth = 0
    i = index
    while i < len(text):
        ch = text[i]
        if ch == '"':
            i += 1
            while i < len(text):
                if text[i] == "\\":
                    i += 2
                    continue
                if text[i] == '"':
                    break
                i += 1
        elif ch == "`":
            i += 1
            while i < len(text) and text[i] != "`":
                i += 1
        elif ch == "{":
            depth += 1
        elif ch == "}":
            depth -= 1
            if depth == 0:
                return i
        i += 1
    raise ValueError("unbalanced braces")


def read_string(text, index):
    """Return (value, next_index) for a quoted Go string at text[index]."""
    assert text[index] == '"'
    i = index + 1
    out = []
    while i < len(text):
        if text[i] == "\\":
            out.append(text[i : i + 2])
            i += 2
            continue
        if text[i] == '"':
            return "".join(out), i + 1
        out.append(text[i])
        i += 1
    raise ValueError("unterminated string")


def split_top_level(body, open_ch, close_ch):
    """Split `body` (contents between open_ch/close_ch) into element texts."""
    elements = []
    i = 0
    n = len(body)
    while i < n:
        ch = body[i]
        if ch in " \t\r\n,":
            i += 1
            continue
        if ch == open_ch:
            end = match_brace(body, i)
            elements.append(body[i : end + 1])
            i = end + 1
            continue
        i += 1
    return elements


def parse_providers(source):
    anchor = "defaultProviderConfigs = map[string]*ProviderConfig{"
    start = source.index(anchor)
    open_index = source.index("{", start + len(anchor) - 1)
    close_index = match_brace(source, open_index)
    body = source[open_index + 1 : close_index]

    providers = []
    i = 0
    n = len(body)
    while i < n:
        ch = body[i]
        if ch in " \t\r\n,":
            i += 1
            continue
        if ch != '"':
            i += 1
            continue
        name, i = read_string(body, i)
        while i < n and body[i] in " \t\r\n":
            i += 1
        if i >= n or body[i] != ":":
            continue
        i += 1
        while i < n and body[i] in " \t\r\n":
            i += 1
        if body.startswith("&ProviderConfig", i):
            i += len("&ProviderConfig")
            while i < n and body[i] in " \t\r\n":
                i += 1
        if i >= n or body[i] != "{":
            continue
        end = match_brace(body, i)
        providers.append(parse_provider(name, body[i + 1 : end]))
        i = end + 1
    return providers


def first(pattern, text, default=""):
    m = re.search(pattern, text)
    return m.group(1) if m else default


def parse_provider(name, entry):
    models_open = entry.find(MODEL_ARRAY)
    header = entry[:models_open] if models_open >= 0 else entry
    provider = {
        "id": name,
        "vendor": first(r'Vendor:\s*"((?:[^"\\]|\\.)*)"', header),
        "base_url": first(r'BaseURL:\s*"((?:[^"\\]|\\.)*)"', header),
        "api": first(r'\bAPI:\s*"((?:[^"\\]|\\.)*)"', header),
        "thinking": first(r'ThinkingFormat:\s*"((?:[^"\\]|\\.)*)"', header),
        "api_key": first(r'APIKey:\s*"((?:[^"\\]|\\.)*)"', header),
        "models": [],
    }
    if models_open >= 0:
        array_open = entry.index("{", models_open + len(MODEL_ARRAY) - 1)
        array_close = match_brace(entry, array_open)
        for element in split_top_level(entry[array_open + 1 : array_close], "{", "}"):
            provider["models"].append(parse_model(element))
    return provider


def parse_model(text):
    cost = {"input": 0.0, "output": 0.0, "cache_read": 0.0, "cache_write": 0.0}
    cost_match = re.search(r"Cost:\s*&CostConfig\{([^}]*)\}", text)
    if cost_match:
        for key, field in (("input", "Input"), ("output", "Output"), ("cache_read", "CacheRead"), ("cache_write", "CacheWrite")):
            value = first(rf"{field}:\s*([0-9.]+)", cost_match.group(1), "0")
            cost[key] = float(value)
    inputs = first(r"Input:\s*\[\]string\{([^}]*)\}", text)
    modalities = [m.strip().strip('"') for m in inputs.split(",") if m.strip()]
    return {
        "id": first(r'\{ID:\s*"((?:[^"\\]|\\.)*)"', text),
        "name": first(r'Name:\s*"((?:[^"\\]|\\.)*)"', text),
        "reasoning": "Reasoning: true" in text,
        "context": int(first(r"ContextWindow:\s*(\d+)", text, "0")),
        "max_tokens": int(first(r"MaxTokens:\s*(\d+)", text, "0")),
        "input": modalities,
        "cost": cost if cost_match else None,
    }


def provider_sort_key(provider_id):
    name = provider_id.lower()

    def priority():
        if "moark" in name:
            return 10
        if "deepseek" in name:
            return 20
        if "xiaomi" in name or "mimo" in name:
            return 30
        if "doubao" in name or "volc" in name or "ark" in name:
            return 40
        if "openai" in name:
            return 50
        if "anthropic" in name or "claude" in name:
            return 60
        if "google" in name or "gemini" in name or "vertex" in name:
            return 70
        return 100

    return (priority(), provider_id)


def fmt_size(value):
    if not value:
        return "-"
    if value >= 1_000_000:
        return f"{value / 1_000_000:.0f}M"
    if value >= 1_000:
        return f"{value / 1_000:.0f}K"
    return str(value)


def fmt_cost(value):
    if value is None:
        return "-"
    text = f"{value:.3f}".rstrip("0").rstrip(".")
    return text if text else "0"


def render(providers):
    providers = sorted(providers, key=lambda p: provider_sort_key(p["id"]))
    total_models = sum(len(p["models"]) for p in providers)

    def label(provider_id):
        return PROVIDER_LABELS.get(provider_id, provider_id)

    lines = [
        "# MothX 供应商/模型完整配置表",
        "",
        "> 由 `docs/scripts/generate-models.py` 依据 `internal/config/settings.go`（`defaultProviderConfigs`）与 "
        "`internal/provider/vendor_*.go` 生成，请勿手工编辑；运行 `make docs-models` 重新生成。",
        "",
        f"本文档是 MothX 内置供应商与模型的完整参考：共 **{len(providers)} 个供应商**、**{total_models} 个模型**。",
        "",
        API_TYPES,
        "",
        THINKING_FORMATS,
        "",
        "## 按供应商分类的 Quick Reference",
        "",
        "| 供应商 | Provider | Vendor | API 协议 | Thinking 格式 | 模型数 |",
        "|--------|----------|--------|----------|--------------|--------|",
    ]
    for p in providers:
        lines.append(
            f"| {label(p['id'])} | `{p['id']}` | {p['vendor'] or '-'} | {p['api'] or '-'} | {p['thinking'] or '-'} | {len(p['models'])} |"
        )
    lines.extend(["", "---", "", "## 完整供应商列表", ""])

    for p in providers:
        lines.append(f"### {label(p['id'])}（`{p['id']}`） <a id=\"{p['id']}\"></a>")
        lines.append("")
        meta = []
        if p["vendor"]:
            meta.append(f"Vendor `{p['vendor']}`")
        if p["base_url"]:
            meta.append(f"BaseURL `{p['base_url']}`")
        if p["api"]:
            meta.append(f"API `{p['api']}`")
        if p["thinking"]:
            meta.append(f"Thinking `{p['thinking']}`")
        if p["api_key"]:
            meta.append(f"API Key `{p['api_key']}`")
        if meta:
            lines.append(" · ".join(meta))
            lines.append("")
        lines.append("| 模型 | Context | MaxTokens | 推理 | 输入 | Prompt $/M | Completion $/M | Cache Read $/M | Cache Write $/M |")
        lines.append("|------|---------|-----------|------|------|-----------|----------------|----------------|-----------------|")
        for m in p["models"]:
            cost = m["cost"] or {}
            lines.append(
                "| `{id}` | {ctx} | {out} | {reason} | {mods} | {pin} | {pout} | {pread} | {pwrite} |".format(
                    id=m["id"],
                    ctx=fmt_size(m["context"]),
                    out=fmt_size(m["max_tokens"]),
                    reason="**是**" if m["reasoning"] else "否",
                    mods=",".join(m["input"]) or "-",
                    pin=fmt_cost(cost.get("input")) if m["cost"] else "-",
                    pout=fmt_cost(cost.get("output")) if m["cost"] else "-",
                    pread=fmt_cost(cost.get("cache_read")) if m["cost"] else "-",
                    pwrite=fmt_cost(cost.get("cache_write")) if m["cost"] else "-",
                )
            )
        lines.extend(["", "---", ""])

    lines.extend([CONFIG_FIELDS, "", THINKING_LEVELS, "", "## 汇总统计", ""])
    reasoning = sum(1 for p in providers for m in p["models"] if m["reasoning"])
    lines.append(f"- 支持推理（reasoning）的模型：**{reasoning}**")
    lines.append("")
    modality_counts = {}
    for p in providers:
        for m in p["models"]:
            key = "/".join(m["input"]) or "-"
            modality_counts[key] = modality_counts.get(key, 0) + 1
    context_buckets = [
        ("1M+", lambda c: c >= 1_000_000),
        ("500K-1M", lambda c: 500_000 <= c < 1_000_000),
        ("200K-500K", lambda c: 200_000 <= c < 500_000),
        ("100K-200K", lambda c: 100_000 <= c < 200_000),
        ("32K-100K", lambda c: 32_000 <= c < 100_000),
        ("<32K", lambda c: 0 < c < 32_000),
        ("未设置", lambda c: c == 0),
    ]
    lines.extend(["### 上下文长度分布", "", "| 区间 | 数量 |", "|------|------|"])
    for bucket, predicate in context_buckets:
        count = sum(1 for p in providers for m in p["models"] if predicate(m["context"]))
        lines.append(f"| {bucket} | {count} |")
    lines.extend(["", "### 输入模态分布", "", "| 模态 | 数量 |", "|------|------|"])
    for key in sorted(modality_counts, key=lambda k: (-modality_counts[k], k)):
        lines.append(f"| {key} | {modality_counts[key]} |")
    lines.append("")

    return "\n".join(lines)


def main():
    providers = parse_providers(SRC.read_text(encoding="utf-8"))
    OUT.write_text(render(providers), encoding="utf-8")
    print(f"wrote {OUT.relative_to(ROOT)}: {len(providers)} providers")


if __name__ == "__main__":
    main()
