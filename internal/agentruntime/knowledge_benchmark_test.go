package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

// Controlled knowledge-base quality benchmark (proposal §9 Phase C4).
//
// The fixture is fully deterministic: it builds a fixed directory of Markdown
// files, indexes it without a provider, and replays a fixed query set through
// the Knowledge MCP handler. The reported metrics are the documented baseline:
//
//   - indexDurationMs: one full deterministic index pass of the fixture.
//   - queryP50Ms / queryP95Ms: MCP search latency over the fixed query set.
//   - toolResultBytesP50 / toolResultBytesMax: encoded MCP tool-result size.
//   - citationCoverage: share of returned evidence items carrying a citation.
//   - uncertaintyCoverage: share of query results exposing a structured
//     uncertainty or the truncated flag.
//   - tokenEstimate: coarse (bytes/4) estimate of the tool-result payload.
//
// Regression thresholds are intentionally loose so the check catches gross
// regressions without being flaky on a shared CI host. Tighten them only with a
// recorded baseline. Run with `go test -run TestKnowledgeBaseBenchmarkBaseline
// ./internal/agentruntime -v`, or `scripts/knowledge-benchmark.sh` which also
// captures the JSON report.
const (
	knowledgeBenchmarkFiles           = 40
	knowledgeBenchmarkQueryRepeats    = 5
	knowledgeBenchmarkMaxIndexMs      = 60_000
	knowledgeBenchmarkMaxP95Ms        = 5_000
	knowledgeBenchmarkMaxToolBytes    = maxKnowledgeMCPResultChars + 4_096
	knowledgeBenchmarkMinCitationCov  = 1.0
	knowledgeBenchmarkMinUncertainty  = 0.1
	knowledgeBenchmarkMaxTokenEstimat = 200_000
)

type knowledgeBenchmarkReport struct {
	Files               int     `json:"files"`
	Chunks              int     `json:"chunks"`
	Nodes               int     `json:"nodes"`
	Edges               int     `json:"edges"`
	IndexDurationMs     float64 `json:"indexDurationMs"`
	Queries             int     `json:"queries"`
	QueryP50Ms          float64 `json:"queryP50Ms"`
	QueryP95Ms          float64 `json:"queryP95Ms"`
	ToolResultBytesP50  int     `json:"toolResultBytesP50"`
	ToolResultBytesMax  int     `json:"toolResultBytesMax"`
	CitationCoverage    float64 `json:"citationCoverage"`
	UncertaintyCoverage float64 `json:"uncertaintyCoverage"`
	TokenEstimate       int     `json:"tokenEstimate"`
}

func TestKnowledgeBaseBenchmarkBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("knowledge base benchmark is skipped in -short mode")
	}
	sessionDir := t.TempDir()
	source := t.TempDir()
	writeKnowledgeBenchmarkFixture(t, source, knowledgeBenchmarkFiles)

	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Benchmark", RootDir: source, PreprocessProfile: "documents",
		Mode: ModeYolo, Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	snapshot, err := service.Index(t.Context(), base.ID)
	if err != nil {
		t.Fatal(err)
	}
	indexDuration := time.Since(start)

	handler, err := NewKnowledgeMCPHandler(sessionDir, []string{base.ID})
	if err != nil {
		t.Fatal(err)
	}

	queries := knowledgeBenchmarkQueries()
	latencies := make([]float64, 0, len(queries)*knowledgeBenchmarkQueryRepeats)
	resultBytes := make([]int, 0, len(queries)*knowledgeBenchmarkQueryRepeats)
	citations, citedEvidence, uncertaintySignals := 0, 0, 0
	tokenEstimate := 0
	for _, query := range queries {
		for repeat := 0; repeat < knowledgeBenchmarkQueryRepeats; repeat++ {
			args, _ := json.Marshal(map[string]any{"knowledgeBaseId": base.ID, "query": query, "limit": 8})
			callStart := time.Now()
			callResult, callErr := handler.CallTool(context.Background(), knowledgeMCPToolName, args)
			latencies = append(latencies, float64(time.Since(callStart).Microseconds())/1000.0)
			if callErr != nil {
				t.Fatalf("query %q failed: %v", query, callErr)
			}
			if len(callResult.Content) == 0 {
				t.Fatalf("query %q returned no content", query)
			}
			payload := callResult.Content[0].Text
			resultBytes = append(resultBytes, len(payload))
			tokenEstimate += len(payload) / 4
			var decoded struct {
				Evidence []struct {
					Citations []struct {
						ChunkID string `json:"chunkId"`
						Path    string `json:"path"`
					} `json:"citations"`
				} `json:"evidence"`
				Uncertainties []struct {
					Kind string `json:"kind"`
				} `json:"uncertainties"`
				Truncated bool `json:"truncated"`
			}
			if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
				t.Fatalf("decode query %q result: %v", query, err)
			}
			if len(decoded.Uncertainties) > 0 || decoded.Truncated {
				uncertaintySignals++
			}
			for _, item := range decoded.Evidence {
				citations++
				if len(item.Citations) > 0 && strings.TrimSpace(item.Citations[0].Path) != "" {
					citedEvidence++
				}
			}
		}
	}

	report := knowledgeBenchmarkReport{
		Files:               snapshot.FileCount,
		Chunks:              snapshot.ChunkCount,
		Nodes:               snapshot.NodeCount,
		Edges:               snapshot.EdgeCount,
		IndexDurationMs:     float64(indexDuration.Microseconds()) / 1000.0,
		Queries:             len(queries) * knowledgeBenchmarkQueryRepeats,
		QueryP50Ms:          percentile(latencies, 50),
		QueryP95Ms:          percentile(latencies, 95),
		ToolResultBytesP50:  int(percentile(intToFloat(resultBytes), 50)),
		ToolResultBytesMax:  maxInt(resultBytes),
		CitationCoverage:    ratio(citedEvidence, citations),
		UncertaintyCoverage: ratio(uncertaintySignals, len(queries)*knowledgeBenchmarkQueryRepeats),
		TokenEstimate:       tokenEstimate,
	}
	encoded, _ := json.Marshal(report)
	t.Logf("knowledge-benchmark: %s", encoded)
	if path := strings.TrimSpace(os.Getenv("MOTHX_KB_BENCH_REPORT")); path != "" {
		if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
			t.Fatalf("write benchmark report: %v", err)
		}
	}

	if report.Files != knowledgeBenchmarkFiles {
		t.Fatalf("indexed %d files, want %d", report.Files, knowledgeBenchmarkFiles)
	}
	if report.IndexDurationMs > knowledgeBenchmarkMaxIndexMs {
		t.Fatalf("index duration %.1fms exceeds threshold %dms", report.IndexDurationMs, knowledgeBenchmarkMaxIndexMs)
	}
	if report.QueryP95Ms > knowledgeBenchmarkMaxP95Ms {
		t.Fatalf("query p95 %.1fms exceeds threshold %dms", report.QueryP95Ms, knowledgeBenchmarkMaxP95Ms)
	}
	if report.ToolResultBytesMax > knowledgeBenchmarkMaxToolBytes {
		t.Fatalf("tool result %d bytes exceeds threshold %d", report.ToolResultBytesMax, knowledgeBenchmarkMaxToolBytes)
	}
	if report.CitationCoverage < knowledgeBenchmarkMinCitationCov {
		t.Fatalf("citation coverage %.3f below threshold %.3f", report.CitationCoverage, knowledgeBenchmarkMinCitationCov)
	}
	if report.UncertaintyCoverage < knowledgeBenchmarkMinUncertainty {
		t.Fatalf("uncertainty coverage %.3f below threshold %.3f", report.UncertaintyCoverage, knowledgeBenchmarkMinUncertainty)
	}
	if report.TokenEstimate > knowledgeBenchmarkMaxTokenEstimat {
		t.Fatalf("token estimate %d exceeds threshold %d", report.TokenEstimate, knowledgeBenchmarkMaxTokenEstimat)
	}
}

// writeKnowledgeBenchmarkFixture builds a deterministic directory whose files
// interlink, share vocabulary, and each carry a heading, a code declaration, and
// a reference link so the index produces files, sections, symbols, contains and
// references edges without a model.
func writeKnowledgeBenchmarkFixture(t *testing.T, root string, files int) {
	t.Helper()
	topics := []string{"retrieval", "pipeline", "indexing", "snapshot", "evidence"}
	for i := 0; i < files; i++ {
		next := (i + 1) % files
		topic := topics[i%len(topics)]
		content := fmt.Sprintf(`# Topic %02d

Topic %02d documents the %s subsystem and how it cooperates with the pipeline.

## Details

The %s module links to [next](doc-%02d.md) and describes the snapshot workflow.

`+"```go\nfunc Handler%02d() error { return nil }\n```\n\n## Notes\n\nThe evidence component mentions %s and indexing repeatedly so retrieval queries match this document.\n",
			i, i, topic, topic, next, i, topic)
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("doc-%02d.md", i)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// knowledgeBenchmarkQueries is the fixed query set replayed by the benchmark.
// The final broad query deliberately exceeds the tool-result budget so the
// truncated/uncertainty surface is exercised.
func knowledgeBenchmarkQueries() []string {
	return []string{
		"retrieval pipeline",
		"snapshot workflow",
		"evidence indexing",
		"Handler07",
		"doc-12",
		"subsystem cooperation",
		"topic",
	}
}

func percentile(values []float64, p int) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	index := (p * (len(sorted) - 1)) / 100
	lower := sorted[index]
	upper := sorted[index+1]
	if upper == lower {
		return lower
	}
	fraction := float64(p*(len(sorted)-1)) / 100.0
	return lower + (upper-lower)*(fraction-float64(index))
}

func intToFloat(values []int) []float64 {
	out := make([]float64, len(values))
	for i, value := range values {
		out[i] = float64(value)
	}
	return out
}

func maxInt(values []int) int {
	max := 0
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func ratio(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total)
}
