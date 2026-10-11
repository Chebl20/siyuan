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

package indexstore

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/siyuan-note/siyuan/kernel/search"
)

// SQLitePaths 是 workspace/temp 里可重建的明文索引。加密 SQLCipher 文件不在此列。
type SQLitePaths struct {
	Content      string
	BlockTree    string
	History      string
	AssetContent string
	TempDir      string
}

// TableReport 记录一张表的源、目标行数和样本哈希。
type TableReport struct {
	Table        string `json:"table"`
	Source       int64  `json:"source"`
	Dest         int64  `json:"dest"`
	SampleSHA256 string `json:"sampleSha256"`
}

// Report 是一次 sqlite → postgres 复制的结果。OK 仅在每张表行数一致时为真。
type Report struct {
	OK               bool          `json:"ok"`
	Tables           []TableReport `json:"tables"`
	Inconsistencies  []string      `json:"inconsistencies"`
	EncryptedSkipped []string      `json:"encryptedSkipped"`
}

type copySpec struct {
	fileKey     string
	sourceQuery string
	sourceTable string
	destTable   string
	columns     []string
	intCols     []string
	conflict    []string
	skipEmptyID bool
	searchKind  string
}

// CopySQLiteIndex 把明文 SQLite 索引幂等写入已经完成 migration 的 PostgreSQL。
// 加密笔记本的 siyuan-encrypted-*.db 只记入报告，不打开。
func CopySQLiteIndex(paths SQLitePaths, dest *sql.DB) (Report, error) {
	report := Report{Inconsistencies: []string{}, EncryptedSkipped: []string{}}
	report.EncryptedSkipped = encryptedIndexFiles(paths.TempDir)
	files := map[string]*sql.DB{}
	for key, path := range map[string]string{
		"content": paths.Content,
		"tree":    paths.BlockTree,
		"history": paths.History,
		"asset":   paths.AssetContent,
	} {
		db, err := openSQLiteRO(path)
		if err != nil {
			return report, err
		}
		if db != nil {
			defer db.Close()
		}
		files[key] = db
	}

	specs := []copySpec{
		{fileKey: "content", sourceTable: "blocks", destTable: "blocks", skipEmptyID: true, searchKind: "block",
			columns:  []string{"id", "parent_id", "root_id", "hash", "box", "path", "hpath", "name", "alias", "memo", "tag", "content", "fcontent", "markdown", "length", "type", "subtype", "ial", "sort", "created", "updated"},
			intCols:  []string{"length", "sort"},
			conflict: []string{"id"}},
		{fileKey: "content", sourceTable: "spans", destTable: "spans", skipEmptyID: true,
			columns: []string{"id", "block_id", "root_id", "box", "path", "content", "markdown", "type", "ial"}, conflict: []string{"id"}},
		{fileKey: "content", sourceTable: "assets", destTable: "assets", skipEmptyID: true,
			columns: []string{"id", "block_id", "root_id", "box", "docpath", "path", "name", "title", "hash"}, conflict: []string{"id"}},
		{fileKey: "content", sourceTable: "attributes", destTable: "attributes", skipEmptyID: true,
			columns: []string{"id", "name", "value", "type", "block_id", "root_id", "box", "path"}, conflict: []string{"id"}},
		{fileKey: "content", sourceTable: "refs", destTable: "refs", skipEmptyID: true,
			columns:  []string{"id", "def_block_id", "def_block_parent_id", "def_block_root_id", "def_block_path", "block_id", "root_id", "box", "path", "content", "markdown", "type"},
			conflict: []string{"id"}},
		{fileKey: "content", sourceTable: "file_annotation_refs", destTable: "file_annotation_refs", skipEmptyID: true,
			columns: []string{"id", "file_path", "annotation_id", "block_id", "root_id", "box", "path", "content", "type"}, conflict: []string{"id"}},
		{fileKey: "content", sourceTable: "block_embeddings", destTable: "block_embeddings", skipEmptyID: true,
			columns:  []string{"id", "root_id", "box", "path", "embedding", "model", "content_len", "updated", "fail_count", "last_tried", "ignored_type"},
			intCols:  []string{"content_len", "fail_count", "last_tried", "ignored_type"},
			conflict: []string{"id"}},
		{fileKey: "content", sourceTable: "stat", destTable: "stat",
			columns: []string{"key", "value"}, conflict: []string{"key"}},
		{fileKey: "tree", sourceTable: "blocktrees", destTable: "blocktrees_row", skipEmptyID: true,
			columns: []string{"id", "root_id", "parent_id", "box_id", "path", "hpath", "updated", "type"}, conflict: []string{"id"}},
		{fileKey: "history", sourceTable: "histories_fts_case_insensitive", destTable: "histories", searchKind: "history",
			columns: []string{"id", "type", "op", "title", "content", "path", "created"}, intCols: []string{"type"},
			conflict: []string{"path", "created", "op"}},
		{fileKey: "asset", sourceTable: "asset_contents_fts_case_insensitive", destTable: "asset_contents", searchKind: "asset",
			columns: []string{"id", "name", "ext", "path", "size", "updated", "content"}, intCols: []string{"size", "updated"},
			conflict: []string{"path"}},
	}
	for i := range specs {
		specs[i].sourceQuery = "SELECT " + strings.Join(specs[i].columns, ", ") + " FROM " + specs[i].sourceTable
	}
	for _, spec := range specs {
		table, notes, err := copyTable(files[spec.fileKey], dest, spec)
		if err != nil {
			return report, fmt.Errorf("copy %s: %w", spec.destTable, err)
		}
		report.Tables = append(report.Tables, table)
		report.Inconsistencies = append(report.Inconsistencies, notes...)
	}
	orphans, err := countOrphans(dest)
	if err != nil {
		return report, err
	}
	if orphans > 0 {
		report.Inconsistencies = append(report.Inconsistencies, fmt.Sprintf("spans.root_id missing from blocks: %d", orphans))
	}
	report.OK = true
	for _, table := range report.Tables {
		if table.Source != table.Dest {
			report.OK = false
			break
		}
	}
	return report, nil
}

func encryptedIndexFiles(tempDir string) []string {
	if tempDir == "" {
		return []string{}
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return []string{}
	}
	var names []string
	for _, entry := range entries {
		if IsEncryptedIndexFile(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	if names == nil {
		return []string{}
	}
	return names
}

func openSQLiteRO(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	return db, nil
}

func copyTable(src, dest *sql.DB, spec copySpec) (TableReport, []string, error) {
	report := TableReport{Table: spec.destTable}
	notes := []string{}
	if src == nil {
		destCount, err := countTable(dest, spec.destTable)
		report.Dest = destCount
		return report, notes, err
	}
	sourceCount, err := countTable(src, spec.sourceTable)
	if err != nil {
		return report, notes, err
	}
	report.Source = sourceCount
	rows, err := src.Query(spec.sourceQuery)
	if err != nil {
		return report, notes, err
	}
	defer rows.Close()

	tx, err := dest.Begin()
	if err != nil {
		return report, notes, err
	}
	defer tx.Rollback()

	var batch [][]any
	var samples []string
	emptySkipped := 0
	const batchSize = 100
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := insertBatch(tx, spec, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	for rows.Next() {
		vals, err := scanLoose(rows, len(spec.columns))
		if err != nil {
			return report, notes, err
		}
		if spec.skipEmptyID && stringValue(vals[0]) == "" {
			emptySkipped++
			continue
		}
		if len(samples) < 20 {
			samples = append(samples, stringValue(vals[0]))
		}
		batch = append(batch, vals)
		if len(batch) >= batchSize {
			if err = flush(); err != nil {
				return report, notes, err
			}
		}
	}
	if err = rows.Err(); err != nil {
		return report, notes, err
	}
	if err = flush(); err != nil {
		return report, notes, err
	}
	if err = tx.Commit(); err != nil {
		return report, notes, err
	}
	if emptySkipped > 0 {
		notes = append(notes, fmt.Sprintf("%s: empty id skipped: %d", spec.destTable, emptySkipped))
	}
	report.Dest, err = countTable(dest, spec.destTable)
	if err != nil {
		return report, notes, err
	}
	sum := sha256.Sum256([]byte(strings.Join(samples, "\n")))
	report.SampleSHA256 = hex.EncodeToString(sum[:])
	return report, notes, nil
}

func insertBatch(tx *sql.Tx, spec copySpec, batch [][]any) error {
	groups := make([]string, 0, len(batch))
	args := make([]any, 0, len(batch)*len(spec.columns))
	placeholder := "(" + strings.TrimSuffix(strings.Repeat("?,", len(spec.columns)), ",") + ")"
	for _, row := range batch {
		groups = append(groups, placeholder)
		args = append(args, normalizeRow(spec, row)...)
	}
	stmt := "INSERT INTO " + spec.destTable + " (" + strings.Join(spec.columns, ", ") + ") VALUES " + strings.Join(groups, ",")
	stmt = ConflictUpdate(stmt, spec.conflict)
	if _, err := tx.Exec(stmt, args...); err != nil {
		return err
	}
	return insertSearchBatch(tx, spec, batch)
}

func insertSearchBatch(tx *sql.Tx, spec copySpec, batch [][]any) error {
	if spec.searchKind == "" {
		return nil
	}
	for _, row := range batch {
		values := map[string]any{}
		for i, col := range spec.columns {
			values[col] = row[i]
		}
		switch spec.searchKind {
		case "block":
			text := normalizeJoined(values, "name", "alias", "memo", "tag", "content", "fcontent", "ial")
			if _, err := tx.Exec(`INSERT INTO block_search (id, search_vector) VALUES (?, to_tsvector('simple', ?))
				ON CONFLICT (id) DO UPDATE SET search_vector = EXCLUDED.search_vector`, stringValue(values["id"]), text); err != nil {
				return err
			}
		case "history":
			text := normalizeJoined(values, "title", "content")
			if _, err := tx.Exec(`INSERT INTO history_search (path, created, op, search_vector) VALUES (?, ?, ?, to_tsvector('simple', ?))
				ON CONFLICT (path, created, op) DO UPDATE SET search_vector = EXCLUDED.search_vector`,
				stringValue(values["path"]), stringValue(values["created"]), stringValue(values["op"]), text); err != nil {
				return err
			}
		case "asset":
			text := normalizeJoined(values, "name", "content")
			if _, err := tx.Exec(`INSERT INTO asset_content_search (path, search_vector) VALUES (?, to_tsvector('simple', ?))
				ON CONFLICT (path) DO UPDATE SET search_vector = EXCLUDED.search_vector`, stringValue(values["path"]), text); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizeJoined(values map[string]any, cols ...string) string {
	parts := make([]string, 0, len(cols))
	for _, col := range cols {
		parts = append(parts, stringValue(values[col]))
	}
	return search.NormalizeSearchText(strings.Join(parts, "\n"), false, false)
}

func normalizeRow(spec copySpec, row []any) []any {
	ints := map[string]bool{}
	for _, col := range spec.intCols {
		ints[col] = true
	}
	out := make([]any, len(row))
	for i, val := range row {
		col := spec.columns[i]
		if val == nil {
			if ints[col] {
				out[i] = int64(0)
			} else if col == "embedding" {
				out[i] = nil
			} else {
				out[i] = ""
			}
			continue
		}
		if b, ok := val.([]byte); ok && col != "embedding" {
			out[i] = string(b)
			continue
		}
		out[i] = val
	}
	return out
}

func scanLoose(rows *sql.Rows, n int) ([]any, error) {
	vals := make([]any, n)
	ptrs := make([]any, n)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	return vals, nil
}

func stringValue(val any) string {
	switch v := val.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

func countTable(db *sql.DB, table string) (int64, error) {
	var n int64
	err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
	if err != nil && (strings.Contains(err.Error(), "no such table") || strings.Contains(err.Error(), "does not exist")) {
		return 0, nil
	}
	return n, err
}

func countOrphans(dest *sql.DB) (int64, error) {
	var n int64
	err := dest.QueryRow(`SELECT COUNT(*) FROM spans s WHERE s.root_id <> '' AND NOT EXISTS (
		SELECT 1 FROM blocks b WHERE b.id = s.root_id)`).Scan(&n)
	return n, err
}

// ReportPath 返回迁移报告路径。报告里不写连接串。
func ReportPath(tempDir string) string {
	return filepath.Join(tempDir, "migrate-report.json")
}
