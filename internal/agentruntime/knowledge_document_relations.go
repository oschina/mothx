package agentruntime

import (
	"regexp"
	"sort"
	"strings"

	"github.com/oschina/mothx/internal/session"
)

// Deterministic document node kinds. They are deliberately outside the model
// candidate kinds so a Runtime-derived fact can never collide with (or be
// shadowed by) a model assertion on the node unique key.
const (
	knowledgeDefinedTermKind  = "defined_term"
	knowledgeRequirementKind  = "requirement"
	maxKnowledgeDocumentNodes = 500
)

var (
	// knowledgeDefinitionLine matches a glossary-style definition: a bolded term
	// followed by a separator and definition text (for example
	// "**Idempotency** — repeating a request has no additional effect.").
	knowledgeDefinitionLine = regexp.MustCompile(`^\s*(?:\*\*|__)\s*([^*_\n]{2,80}?)\s*(?:\*\*|__)\s*[:\-–—]\s*(\S.*)$`)
	// knowledgeRequirementLine matches a normative RFC 2119 keyword as a whole
	// word, which marks the line as a stated requirement.
	knowledgeRequirementLine = regexp.MustCompile(`\b(?:MUST NOT|MUST|SHALL NOT|SHALL|REQUIRED|SHOULD NOT|SHOULD)\b`)
)

// appendKnowledgeDocumentEdges adds deterministic "defines" and "requires" edges
// for document knowledge bases: a bolded term definition produces a defined-term
// node, and a line stating an RFC 2119 requirement produces a requirement node.
// Both are parsed from the document's own lines, so the relation is locally
// verifiable and rebuilt over the whole assembled graph on every index.
func appendKnowledgeDocumentEdges(graph *session.KnowledgeGraphSnapshot, profile string) {
	if graph == nil {
		return
	}
	switch profile {
	case "documents", "mixed":
	default:
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
	sectionsByFile := make(map[string][]knowledgeSymbolDecl)
	for _, evidence := range graph.Evidence {
		if evidence.NodeID == "" {
			continue
		}
		node, ok := nodeByID[evidence.NodeID]
		if !ok {
			continue
		}
		chunk, ok := chunkByID[evidence.ChunkID]
		if !ok {
			continue
		}
		switch node.Kind {
		case "file":
			if _, exists := fileNodeByFileID[chunk.FileID]; !exists {
				fileNodeByFileID[chunk.FileID] = node.ID
			}
		case "section":
			sectionsByFile[chunk.FileID] = append(sectionsByFile[chunk.FileID], knowledgeSymbolDecl{id: node.ID, declLine: evidence.StartLine})
		}
	}
	for fileID := range sectionsByFile {
		sections := sectionsByFile[fileID]
		sort.SliceStable(sections, func(i, j int) bool { return sections[i].declLine < sections[j].declLine })
	}
	nodeIDs := make(map[string]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodeIDs[node.Kind+"\x00"+node.NormalizedLabel] = node.ID
	}
	existing := make(map[string]struct{}, len(graph.Edges))
	for _, edge := range graph.Edges {
		existing[knowledgeEdgeKey(edge.FromNodeID, edge.ToNodeID, edge.RelationType)] = struct{}{}
	}
	added := 0
	for _, chunk := range graph.Chunks {
		if added >= maxKnowledgeDocumentNodes {
			break
		}
		fileID := chunk.FileID
		fileNodeID := fileNodeByFileID[fileID]
		for index, line := range strings.Split(chunk.Text, "\n") {
			if added >= maxKnowledgeDocumentNodes {
				break
			}
			lineNo := chunk.StartLine + index
			fromID := knowledgeEnclosingContainer(sectionsByFile[fileID], fileNodeID, lineNo)
			if fromID == "" {
				continue
			}
			if match := knowledgeDefinitionLine.FindStringSubmatch(line); len(match) == 3 {
				if appendKnowledgeDocumentEdge(graph, fromID, knowledgeDefinedTermKind, "defines", match[1], chunk, lineNo, nodeIDs, existing) {
					added++
				}
			}
			if knowledgeRequirementLine.MatchString(line) {
				if appendKnowledgeDocumentEdge(graph, fromID, knowledgeRequirementKind, "requires", line, chunk, lineNo, nodeIDs, existing) {
					added++
				}
			}
		}
	}
}

// appendKnowledgeDocumentEdge creates (or reuses) the target node and links the
// enclosing container to it. It reports whether a new edge was added.
func appendKnowledgeDocumentEdge(graph *session.KnowledgeGraphSnapshot, fromID, kind, relation, rawLabel string, chunk session.KnowledgeChunk, lineNo int, nodeIDs map[string]string, existing map[string]struct{}) bool {
	label := normalizeKnowledgeCandidateLabel(rawLabel)
	normalized := normalizeKnowledgeLabel(label)
	if label == "" || normalized == "" {
		return false
	}
	key := kind + "\x00" + normalized
	toID, exists := nodeIDs[key]
	if !exists {
		node := session.KnowledgeNode{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, Kind: kind,
			Label: label, NormalizedLabel: normalized, Summary: label,
			Status: session.KnowledgeNodeStatusFact, Confidence: 1}
		graph.Nodes = append(graph.Nodes, node)
		toID = node.ID
		nodeIDs[key] = toID
		graph.Evidence = append(graph.Evidence, session.KnowledgeEvidence{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID,
			NodeID: toID, ChunkID: chunk.ID, StartLine: lineNo, EndLine: lineNo, Confidence: 1})
	}
	if toID == fromID {
		return false
	}
	edgeKey := knowledgeEdgeKey(fromID, toID, relation)
	if _, duplicate := existing[edgeKey]; duplicate {
		return false
	}
	edge := session.KnowledgeEdge{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, FromNodeID: fromID,
		ToNodeID: toID, RelationType: relation, Confidence: 1}
	graph.Edges = append(graph.Edges, edge)
	graph.Evidence = append(graph.Evidence, session.KnowledgeEvidence{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID,
		EdgeID: edge.ID, ChunkID: chunk.ID, StartLine: lineNo, EndLine: lineNo, Confidence: 1})
	existing[edgeKey] = struct{}{}
	return true
}

// knowledgeEnclosingContainer returns the nearest preceding section node, or the
// file node when the line precedes every section. sections must be sorted by
// declaration line.
func knowledgeEnclosingContainer(sections []knowledgeSymbolDecl, fileNodeID string, line int) string {
	container := ""
	for _, section := range sections {
		if section.declLine > line {
			break
		}
		container = section.id
	}
	if container == "" {
		return fileNodeID
	}
	return container
}
