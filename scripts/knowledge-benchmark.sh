#!/usr/bin/env bash
# Controlled knowledge-base quality benchmark (proposal §9 Phase C4).
#
# Builds a deterministic fixture, indexes it without a provider, and replays a
# fixed query set through the Knowledge MCP handler. The Go test reports the
# documented baseline metrics (index duration, query p50/p95, tool-result size,
# citation/uncertainty coverage, token estimate) and enforces the regression
# thresholds in internal/agentruntime/knowledge_benchmark_test.go.
#
# Usage:
#   scripts/knowledge-benchmark.sh [report.json]
#
# The JSON report is written to the given path (default: knowledge-benchmark.json
# in the repository root) and always echoed to stderr for CI capture.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
report="${1:-${repo_root}/knowledge-benchmark.json}"

cd "${repo_root}"
MOTHX_KB_BENCH_REPORT="${report}" \
  go test ./internal/agentruntime/ -run TestKnowledgeBaseBenchmarkBaseline -count=1 -v

echo "knowledge-benchmark report: ${report}" >&2
