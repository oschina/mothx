package agentruntime

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/oschina/mothx/internal/session"
)

var (
	// knowledgeSupersedeKeyword marks a line as a supersession statement.
	knowledgeSupersedeKeyword = regexp.MustCompile(`\b(?:supersedes|obsoletes|replaces)\b`)
	// knowledgeQuotedRef captures a quoted document reference.
	knowledgeQuotedRef = regexp.MustCompile("[\"'`]([^\"'`]{1,120})[\"'`]")
	// knowledgeFileLikeRef captures a filename-like token (a name with an
	// extension) so a plain prose sentence produces no reference.
	knowledgeFileLikeRef = regexp.MustCompile(`[A-Za-z0-9_./-]+\.[A-Za-z0-9]{1,8}`)
)

// appendKnowledgeSupersedesEdges adds deterministic "supersedes" edges: a
// document whose text states that it supersedes/obsoletes/replaces another
// document is linked to that document's file node when the reference resolves to
// a file present in the same snapshot. Only resolvable file references are
// recorded, so the relation is locally verifiable and needs no external lookup.
func appendKnowledgeSupersedesEdges(graph *session.KnowledgeGraphSnapshot, profile string) {
	if graph == nil {
		return
	}
	switch profile {
	case "documents", "mixed":
	default:
		return
	}
	fileByPath := make(map[string]string)
	fileByBase := make(map[string]string)
	for _, node := range graph.Nodes {
		if node.Kind != "file" {
			continue
		}
		fileByPath[node.NormalizedLabel] = node.ID
		base := normalizeKnowledgeLabel(filepath.Base(node.Label))
		if _, exists := fileByBase[base]; !exists {
			fileByBase[base] = node.ID
		}
	}
	if len(fileByPath) == 0 && len(fileByBase) == 0 {
		return
	}
	chunkByID := make(map[string]session.KnowledgeChunk, len(graph.Chunks))
	for _, chunk := range graph.Chunks {
		chunkByID[chunk.ID] = chunk
	}
	nodeByID := make(map[string]session.KnowledgeNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodeByID[node.ID] = node
	}
	fileNodeByFileID := make(map[string]string)
	for _, evidence := range graph.Evidence {
		if evidence.NodeID == "" {
			continue
		}
		node, ok := nodeByID[evidence.NodeID]
		if !ok || node.Kind != "file" {
			continue
		}
		chunk, ok := chunkByID[evidence.ChunkID]
		if !ok {
			continue
		}
		if _, exists := fileNodeByFileID[chunk.FileID]; !exists {
			fileNodeByFileID[chunk.FileID] = node.ID
		}
	}
	existing := make(map[string]struct{}, len(graph.Edges))
	for _, edge := range graph.Edges {
		existing[knowledgeEdgeKey(edge.FromNodeID, edge.ToNodeID, edge.RelationType)] = struct{}{}
	}
	for _, chunk := range graph.Chunks {
		fromID := fileNodeByFileID[chunk.FileID]
		if fromID == "" {
			continue
		}
		for index, line := range strings.Split(chunk.Text, "\n") {
			if !knowledgeSupersedeKeyword.MatchString(line) {
				continue
			}
			lineNo := chunk.StartLine + index
			for _, token := range knowledgeSupersedeReferences(line) {
				toID := resolveKnowledgeLinkTarget(token, fileByPath, fileByBase)
				if toID == "" || toID == fromID {
					continue
				}
				key := knowledgeEdgeKey(fromID, toID, "supersedes")
				if _, duplicate := existing[key]; duplicate {
					continue
				}
				edge := session.KnowledgeEdge{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, FromNodeID: fromID,
					ToNodeID: toID, RelationType: "supersedes", Confidence: 1}
				graph.Edges = append(graph.Edges, edge)
				graph.Evidence = append(graph.Evidence, session.KnowledgeEvidence{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID,
					EdgeID: edge.ID, ChunkID: chunk.ID, StartLine: lineNo, EndLine: lineNo, Confidence: 1})
				existing[key] = struct{}{}
			}
		}
	}
}

// knowledgeSupersedeReferences extracts candidate document references from a
// supersession line: quoted strings first, then filename-like tokens.
func knowledgeSupersedeReferences(line string) []string {
	seen := make(map[string]struct{})
	refs := make([]string, 0, 2)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		refs = append(refs, value)
	}
	for _, match := range knowledgeQuotedRef.FindAllStringSubmatch(line, -1) {
		add(match[1])
	}
	for _, token := range knowledgeFileLikeRef.FindAllString(line, -1) {
		add(token)
	}
	return refs
}
