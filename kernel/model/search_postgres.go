// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package model

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/88250/gulu"
	"github.com/siyuan-note/siyuan/kernel/search"
	"github.com/siyuan-note/siyuan/kernel/sql"
)

// postgresBlockMatchSQL 用 tsvector 代替 FTS5。simple 词典不会生成思源分词器的 CJK 重叠二元组。
func postgresBlockMatchSQL(query string) string {
	return "WITH q AS (SELECT websearch_to_tsquery('simple', ?) AS tsq) SELECT " + postgresBlockProjection() +
		" FROM blocks b JOIN block_search s ON s.id = b.id CROSS JOIN q WHERE s.search_vector @@ q.tsq"
}

func postgresHeadline(expr string) string {
	opts := "StartSel=" + search.SearchMarkLeft + ",StopSel=" + search.SearchMarkRight + ",MaxWords=35,MinWords=15,MaxFragments=1"
	return "ts_headline('simple', " + expr + ", q.tsq, '" + opts + "')"
}

func postgresBlockProjection() string {
	return "b.id, b.parent_id, b.root_id, b.hash, b.box, b.path, " +
		postgresHeadline("b.hpath") + ", " +
		postgresHeadline("b.name") + ", " +
		postgresHeadline("b.alias") + ", " +
		postgresHeadline("b.memo") + ", " +
		postgresHeadline("b.tag") + ", " +
		postgresHeadline("b.content") + ", " +
		"b.fcontent, b.markdown, b.length, b.type, b.subtype, b.ial, b.sort, b.created, b.updated"
}

func postgresOrder(orderBy string) string {
	return strings.ReplaceAll(orderBy, "rank", "ts_rank(s.search_vector, q.tsq)")
}

func postgresFullTextSearchBlocks(query, boxFilter, pathFilter string, boxArgs, pathArgs []any, typeFilter, ignoreFilter, orderBy string, beforeLen, page, pageSize int, boxID string) (ret []*Block, matchedBlockCount, matchedRootCount int) {
	ret = []*Block{}
	if strings.TrimSpace(query) == "" {
		return
	}
	stmt := postgresBlockMatchSQL(query) + " AND " + typeFilter + boxFilter + pathFilter + ignoreFilter + " " + postgresOrder(orderBy)
	stmt += " LIMIT " + strconv.Itoa(pageSize) + " OFFSET " + strconv.Itoa((page-1)*pageSize)
	args := []any{query}
	args = append(args, boxArgs...)
	args = append(args, pathArgs...)
	blocks := sql.SelectBlocksRawStmtArgsInBox(stmt, args, pageSize, boxID)
	ret = fromSQLBlocks(&blocks, "", beforeLen)
	if 1 > len(ret) {
		ret = []*Block{}
	}
	matchedBlockCount, matchedRootCount = postgresFullTextSearchCount(query, boxFilter, pathFilter, boxArgs, pathArgs, typeFilter, ignoreFilter, boxID)
	return
}

func postgresFullTextSearchCount(query, boxFilter, pathFilter string, boxArgs, pathArgs []any, typeFilter, ignoreFilter, boxID string) (matchedBlockCount, matchedRootCount int) {
	stmt := "SELECT COUNT(b.id) AS matches, COUNT(DISTINCT b.root_id) AS docs FROM blocks b " +
		"JOIN block_search s ON s.id = b.id WHERE s.search_vector @@ websearch_to_tsquery('simple', ?) AND " +
		typeFilter + boxFilter + pathFilter + ignoreFilter
	args := []any{query}
	args = append(args, boxArgs...)
	args = append(args, pathArgs...)
	result, _ := sql.QueryNoLimitArgsInBox(stmt, boxID, args...)
	if 1 > len(result) {
		return
	}
	matchedBlockCount = int64Field(result[0]["matches"])
	matchedRootCount = int64Field(result[0]["docs"])
	return
}

func postgresFullTextSearchWithHPath(rawQuery, query, boxFilter, pathFilter string, boxArgs, pathArgs []any, typeFilter, ignoreFilter string, orderBy, beforeLen, page, pageSize int, boxID string) (ret []*Block, matchedBlockCount, matchedRootCount int) {
	ret = []*Block{}
	hpath := strings.ToLower(search.NormalizeSearchText(rawQuery, Conf.Search.CaseSensitive, Conf.Search.HanSensitiveVal()))
	stmt := "WITH q AS (SELECT websearch_to_tsquery('simple', ?) AS tsq), matches AS (" +
		"SELECT b.id, 0 AS match_source FROM blocks b JOIN block_search s ON s.id = b.id CROSS JOIN q " +
		"WHERE s.search_vector @@ q.tsq AND " + typeFilter + boxFilter + pathFilter + ignoreFilter +
		" UNION SELECT id, 1 AS match_source FROM blocks WHERE type = 'd' AND strpos(lower(hpath), ?) > 0" +
		boxFilter + pathFilter + ignoreFilter +
		" AND id NOT IN (SELECT id FROM blocks b JOIN block_search s ON s.id = b.id CROSS JOIN q WHERE s.search_vector @@ q.tsq)" +
		") SELECT " + postgresBlockProjection() + " FROM blocks b JOIN matches m ON m.id = b.id " +
		"LEFT JOIN block_search s ON s.id = b.id CROSS JOIN q ORDER BY m.match_source ASC, b.updated DESC"
	stmt += " LIMIT " + strconv.Itoa(pageSize) + " OFFSET " + strconv.Itoa((page-1)*pageSize)
	args := []any{query}
	args = append(args, boxArgs...)
	args = append(args, pathArgs...)
	args = append(args, hpath)
	args = append(args, boxArgs...)
	args = append(args, pathArgs...)
	blocks := sql.SelectBlocksRawStmtArgsInBox(stmt, args, pageSize, boxID)
	ret = fromSQLBlocks(&blocks, "", beforeLen)
	if 1 > len(ret) {
		ret = []*Block{}
	}
	matchedBlockCount, matchedRootCount = postgresFullTextSearchCount(query, boxFilter, pathFilter, boxArgs, pathArgs, typeFilter, ignoreFilter, boxID)
	return
}

func postgresRefSearch(keyword string, beforeLen int, onlyDoc bool, boxID string) (ret []*Block) {
	typeClause := "b.type IN " + Conf.Search.TypeFilter()
	if onlyDoc {
		typeClause = "b.type = 'd'"
	}
	stmt := postgresBlockMatchSQL(keyword) + " AND " + typeClause
	if ignoreLines := getRefSearchIgnoreLines(); 0 < len(ignoreLines) {
		for _, line := range ignoreLines {
			stmt += " AND " + line
		}
	}
	orderBy := " ORDER BY CASE WHEN b.name = ? THEN 10 WHEN b.alias = ? THEN 20 WHEN b.content = ? AND b.type = 'd' THEN 40 ELSE 65535 END ASC, b.sort ASC, b.length ASC"
	stmt += orderBy + " LIMIT " + strconv.Itoa(Conf.Search.Limit)
	args := []any{keyword, keyword, keyword, keyword}
	blocks := sql.SelectBlocksRawStmtArgsInBox(stmt, args, Conf.Search.Limit, boxID)
	ret = fromSQLBlocks(&blocks, "", beforeLen)
	if 1 > len(ret) {
		ret = []*Block{}
	}
	return
}

func postgresHighlight(query, typeFilter, id, boxID string) (ret []string) {
	stmt := postgresBlockMatchSQL(query) + " AND " + typeFilter + " AND b.root_id = ? LIMIT 256"
	blocks := sql.SelectBlocksRawStmtArgsInBox(stmt, []any{query, id}, 256, boxID)
	for _, block := range blocks {
		for _, field := range []string{block.HPath, block.Name, block.Alias, block.Memo, block.Tag, block.Content, block.IAL} {
			keyword := gulu.Str.SubstringsBetween(field, search.SearchMarkLeft, search.SearchMarkRight)
			if 0 < len(keyword) {
				ret = append(ret, keyword...)
			}
		}
	}
	ret = gulu.Str.RemoveDuplicatedElem(ret)
	return
}

func int64Field(val any) int {
	switch n := val.(type) {
	case int64:
		return int(n)
	case int32:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	default:
		return 0
	}
}

func searchFieldFilter(expr, boxID string) string {
	if sql.PostgresPlain(boxID) {
		return fieldCompare(expr, "~")
	}
	return fieldRegexp(expr)
}

func adaptDocumentSearchPostgres(stmt string) string {
	stmt = groupConcatPattern.ReplaceAllString(stmt, "string_agg(($1)::text, ',')")
	stmt = searchNormalizePattern.ReplaceAllString(stmt, "lower($1)")
	return stmt
}

var (
	groupConcatPattern     = regexp.MustCompile(`GROUP_CONCAT\(([^()]*)\)`)
	searchNormalizePattern = regexp.MustCompile(`search_normalize\(([^,]+),[^)]+\)`)
)
