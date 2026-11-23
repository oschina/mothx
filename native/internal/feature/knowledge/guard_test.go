package knowledge

import (
	"testing"

	"github.com/oschina/mothx/native/internal/feature/hosttest"
)

func TestPanelCurrentGuardsVisibilityAndGeneration(t *testing.T) {
	panel := NewPanel()
	h := hosttest.New()
	h.Panels["library|knowledge"] = true
	h.Reload = 7
	panel.Seq = 5

	if !panel.Current(h, 5, 7) {
		t.Fatal("fresh response rejected")
	}
	if panel.Current(h, 4, 7) || panel.Current(h, 5, 6) {
		t.Fatal("stale response accepted")
	}
	h.Panels["library|knowledge"] = false
	if panel.Current(h, 5, 7) {
		t.Fatal("hidden library page accepted")
	}
}

func TestPanelInvalidateBumpsSequences(t *testing.T) {
	panel := NewPanel()
	panel.Loaded = true
	panel.Seq = 3
	panel.PollSeq = 4

	panel.Invalidate()

	if panel.Seq != 4 || panel.Loaded || panel.PollSeq != 5 {
		t.Fatalf("invalidate did not reset projections: seq=%d loaded=%v pollSeq=%d", panel.Seq, panel.Loaded, panel.PollSeq)
	}
}

func TestCapabilityFlagsFollowFeatureKeys(t *testing.T) {
	panel := NewPanel()
	cases := []struct {
		key   string
		check func(*hosttest.Host) bool
	}{
		{"manageKnowledgeBases", func(h *hosttest.Host) bool { return panel.Capable(h) }},
		{"knowledgeRuns", func(h *hosttest.Host) bool { return panel.RunsCapable(h) }},
		{"knowledgeSources", func(h *hosttest.Host) bool { return panel.SourcesCapable(h) }},
		{"knowledgeGraphIndex", func(h *hosttest.Host) bool { return panel.GraphCapable(h) }},
		{"manageMcp", func(h *hosttest.Host) bool { return panel.MCPCapable(h) }},
	}
	for _, tc := range cases {
		h := hosttest.New()
		if tc.check(h) {
			t.Fatalf("%s: capability true without feature key", tc.key)
		}
		h.Features[tc.key] = true
		if !tc.check(h) {
			t.Fatalf("%s: capability false with feature key", tc.key)
		}
	}
}
