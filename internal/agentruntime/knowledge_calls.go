package agentruntime

import (
	"sort"
	"strings"

	"github.com/oschina/mothx/internal/session"
)

// knowledgeSymbolDecl is one symbol node declared in a file, with the 1-based
// line of its declaration.
type knowledgeSymbolDecl struct {
	id       string
	label    string
	declLine int
}

// appendKnowledgeCallEdges adds deterministic, within-file "calls" edges between
// two symbol nodes declared in the same file: when a line inside one symbol's
// body invokes another symbol declared in that file, the enclosing symbol is
// linked to the invoked one. Only local declarations are considered, so the
// relation is locally verifiable and needs no cross-file symbol resolution.
// Recursive self-calls are intentionally not recorded.
func appendKnowledgeCallEdges(graph *session.KnowledgeGraphSnapshot, profile string) {
	if graph == nil {
		return
	}
	switch profile {
	case "code", "mixed":
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
	symbolsByFile := make(map[string][]knowledgeSymbolDecl)
	declLinesByFile := make(map[string]map[int]struct{})
	for _, evidence := range graph.Evidence {
		if evidence.NodeID == "" {
			continue
		}
		node, ok := nodeByID[evidence.NodeID]
		if !ok || node.Kind != "symbol" {
			continue
		}
		chunk, ok := chunkByID[evidence.ChunkID]
		if !ok {
			continue
		}
		symbolsByFile[chunk.FileID] = append(symbolsByFile[chunk.FileID], knowledgeSymbolDecl{id: node.ID, label: node.Label, declLine: evidence.StartLine})
		if declLinesByFile[chunk.FileID] == nil {
			declLinesByFile[chunk.FileID] = make(map[int]struct{})
		}
		declLinesByFile[chunk.FileID][evidence.StartLine] = struct{}{}
	}
	if len(symbolsByFile) == 0 {
		return
	}
	for fileID := range symbolsByFile {
		decls := symbolsByFile[fileID]
		sort.SliceStable(decls, func(i, j int) bool { return decls[i].declLine < decls[j].declLine })
	}
	existing := make(map[string]struct{}, len(graph.Edges))
	for _, edge := range graph.Edges {
		existing[knowledgeEdgeKey(edge.FromNodeID, edge.ToNodeID, edge.RelationType)] = struct{}{}
	}
	for _, chunk := range graph.Chunks {
		symbols := symbolsByFile[chunk.FileID]
		if len(symbols) == 0 {
			continue
		}
		declLines := declLinesByFile[chunk.FileID]
		for index, line := range strings.Split(chunk.Text, "\n") {
			lineNo := chunk.StartLine + index
			if _, isDecl := declLines[lineNo]; isDecl {
				continue
			}
			trimmed := strings.TrimSpace(line)
			if knowledgeLineIsComment(trimmed) {
				continue
			}
			caller := knowledgeEnclosingSymbol(symbols, lineNo)
			if caller == "" {
				continue
			}
			for _, callee := range symbols {
				if callee.id == caller || !knowledgeLineCallsSymbol(line, callee.label) {
					continue
				}
				key := knowledgeEdgeKey(caller, callee.id, "calls")
				if _, duplicate := existing[key]; duplicate {
					continue
				}
				edge := session.KnowledgeEdge{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, FromNodeID: caller,
					ToNodeID: callee.id, RelationType: "calls", Confidence: 1}
				graph.Edges = append(graph.Edges, edge)
				graph.Evidence = append(graph.Evidence, session.KnowledgeEvidence{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID,
					EdgeID: edge.ID, ChunkID: chunk.ID, StartLine: lineNo, EndLine: lineNo, Confidence: 1})
				existing[key] = struct{}{}
			}
		}
	}
}

// knowledgeEnclosingSymbol returns the id of the symbol whose declaration is the
// closest one at or before line. symbols must be sorted by declaration line.
func knowledgeEnclosingSymbol(symbols []knowledgeSymbolDecl, line int) string {
	caller := ""
	for _, symbol := range symbols {
		if symbol.declLine > line {
			break
		}
		caller = symbol.id
	}
	return caller
}

// knowledgeLineCallsSymbol reports whether line invokes label as a call: label
// followed by optional spaces and "(", with a non-identifier character (or the
// start of the line) before it.
func knowledgeLineCallsSymbol(line, label string) bool {
	if label == "" {
		return false
	}
	for offset := 0; offset < len(line); {
		index := strings.Index(line[offset:], label)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(label)
		offset = start + 1
		if start > 0 && knowledgeIdentifierByte(line[start-1]) {
			continue
		}
		after := end
		for after < len(line) && line[after] == ' ' {
			after++
		}
		if after < len(line) && line[after] == '(' {
			return true
		}
	}
	return false
}

func knowledgeIdentifierByte(value byte) bool {
	return value == '_' || value >= '0' && value <= '9' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

// knowledgeLineIsComment reports whether a trimmed line is a whole-line comment
// in one of the supported languages, so a mention inside a comment is not
// mistaken for a call site.
func knowledgeLineIsComment(trimmed string) bool {
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*/")
}
