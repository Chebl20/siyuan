package model

import (
	"strings"
	"testing"

	"github.com/siyuan-note/siyuan/kernel/search"
)

func TestPostgresSearchSQLDoesNotUseSiyuanTokenizer(t *testing.T) {
	stmt := postgresBlockMatchSQL("笔记")
	if strings.Contains(stmt, "MATCH") || strings.Contains(stmt, "blocks_fts") {
		t.Fatalf("postgres search must not use FTS5: %s", stmt)
	}
	if !strings.Contains(stmt, "websearch_to_tsquery('simple'") {
		t.Fatalf("missing tsquery: %s", stmt)
	}
	// simple 配置不会把「笔记」拆成思源分词器的重叠二元组。
	if strings.Contains(search.NormalizeSearchText("笔记", false, false), " ") {
		t.Fatal("normalization must not invent siyuan bigrams")
	}
}

func TestAdaptDocumentSearchReplacesSQLiteFunctions(t *testing.T) {
	got := adaptDocumentSearchPostgres("GROUP_CONCAT(content||name) AND search_normalize(hpath, 0, 1)")
	if strings.Contains(got, "GROUP_CONCAT") || strings.Contains(got, "search_normalize") {
		t.Fatalf("got %s", got)
	}
	if !strings.Contains(got, "string_agg((content||name)::text, ',')") || !strings.Contains(got, "lower(hpath)") {
		t.Fatalf("got %s", got)
	}
}
