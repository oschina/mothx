# tokenizerdata

Vendored DeepSeek tokenizer data, embedded into the binary.

| File | Purpose |
|---|---|
| `deepseek_v3_tokenizer.json` | The DeepSeek tokenizer definition (the large embedded asset). |
| `tokenizer_config.json` | The tokenizer configuration the loader expects alongside it. |
| `deepseek_tokenizer.py` | Upstream sample script kept for reference only; never executed at runtime. |

## Source

Downloaded from DeepSeek's official tokenizer download. Keep the DeepSeek V3
tokenizer and its config in sync (they are versioned together).

## Use

`internal/context/deepseek_tokenizer.go` embeds `deepseek_v3_tokenizer.json`
with `go:embed` and uses it for accurate token counting and compaction when a
DeepSeek model is selected. `internal/context` keeps the tokenizer data in the
source tree so token accounting does not depend on a network fetch at runtime.

## Update

1. Download the current tokenizer (`deepseek_v3_tokenizer.json` and
   `tokenizer_config.json`) from DeepSeek's official distribution.
2. Replace both files here (keep the existing names).
3. Run `go test ./internal/context/` and `go test -bench . -run '^$' ./internal/context/`.

These files are third-party data; do not hand-edit the JSON.
