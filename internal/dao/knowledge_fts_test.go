package dao

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestKnowledgeFTSIndexTextSplitsCJKRunsIntoBigrams(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"latin unchanged", "The runtime owns durable runs.", "The runtime owns durable runs."},
		{"cjk run becomes bigrams", "知识库", " 知识 识库 "},
		{"isolated cjk character is kept", "猫", " 猫 "},
		{"mixed run is separated", "go语言模型", "go 语言 言模 模型 "},
		{"cjk with punctuation", "知识。库", " 知识 。 库 "},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := KnowledgeFTSIndexText(testCase.input); got != testCase.want {
				t.Fatalf("KnowledgeFTSIndexText(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestKnowledgeFTSQueryBuildsCJKPhrases(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{"知识库", `"知识 识库"`},
		{"go语言", `"go 语言"`},
		{"knowledge base", `"knowledge" OR "base"`},
		{"durable_run", `"durable_run"`},
		{"...", ""},
		{"", ""},
	}
	for _, testCase := range cases {
		if got := knowledgeFTSQuery(testCase.query); got != testCase.want {
			t.Fatalf("knowledgeFTSQuery(%q) = %q, want %q", testCase.query, got, testCase.want)
		}
	}
}

// TestKnowledgeFTSCJKRoundTrip proves the index/query rewrite pair against a
// real FTS5 table with the same definition as knowledge_chunk_fts: Chinese
// phrases must match, and Latin behavior must not regress.
func TestKnowledgeFTSCJKRoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE VIRTUAL TABLE knowledge_chunk_fts USING fts5(chunk_id UNINDEXED, snapshot_id UNINDEXED, text)`); err != nil {
		t.Fatal(err)
	}
	rows := []struct{ id, text string }{
		{"c1", "知识库是一个可重建的图谱索引系统"},
		{"c2", "The knowledge base is a rebuildable graph index"},
		{"c3", "go语言 bindings expose the same API"},
	}
	for _, row := range rows {
		if _, err := db.ExecContext(ctx, `INSERT INTO knowledge_chunk_fts(chunk_id, snapshot_id, text) VALUES (?, 's1', ?)`, row.id, KnowledgeFTSIndexText(row.text)); err != nil {
			t.Fatal(err)
		}
	}
	match := func(query string) []string {
		terms := knowledgeFTSQuery(query)
		if terms == "" {
			return nil
		}
		rows, err := db.QueryContext(ctx, `SELECT chunk_id FROM knowledge_chunk_fts WHERE knowledge_chunk_fts MATCH ? ORDER BY bm25(knowledge_chunk_fts)`, terms)
		if err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		defer rows.Close()
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return ids
	}
	cases := []struct {
		query string
		want  string
	}{
		{"知识库", "c1"},
		{"图谱索引", "c1"},
		{"索引系统", "c1"},
		{"知识", "c1"},
		{"knowledge base", "c2"},
		{"rebuildable", "c2"},
		{"go语言", "c3"},
		{"bindings", "c3"},
	}
	for _, testCase := range cases {
		ids := match(testCase.query)
		if len(ids) == 0 || ids[0] != testCase.want {
			t.Fatalf("query %q matched %v, want %q first", testCase.query, ids, testCase.want)
		}
	}
	if ids := match("不存在的词组"); len(ids) != 0 {
		t.Fatalf("absent phrase matched %v", ids)
	}
	if !strings.Contains(knowledgeFTSQuery("知识库"), " ") {
		t.Fatal("CJK query must expand into a bigram phrase")
	}
}
