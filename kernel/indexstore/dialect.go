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
	"strconv"
	"strings"
	"unicode"
)

// Adapt 把发往 PostgreSQL 的 SQL 从 SQLite 占位符改成 $n，并把反引号标识符改成双引号。
// 不改写字符串字面量、注释、列名本身，也不把 MATCH 翻译成全文检索。
func Adapt(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	inSingle := false
	inDouble := false
	inIdent := false
	inLine := false
	inBlock := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case inLine:
			b.WriteByte(c)
			if c == '\n' {
				inLine = false
			}
		case inBlock:
			b.WriteByte(c)
			if c == '*' && i+1 < len(query) && query[i+1] == '/' {
				b.WriteByte('/')
				i++
				inBlock = false
			}
		case inSingle:
			b.WriteByte(c)
			if c == '\'' {
				if i+1 < len(query) && query[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				inSingle = false
			}
		case inDouble:
			b.WriteByte(c)
			if c == '"' {
				if i+1 < len(query) && query[i+1] == '"' {
					b.WriteByte('"')
					i++
					continue
				}
				inDouble = false
			}
		case inIdent:
			if c == '`' {
				b.WriteByte('"')
				inIdent = false
				continue
			}
			if c == '"' {
				b.WriteString(`""`)
				continue
			}
			b.WriteByte(c)
		default:
			if c == '-' && i+1 < len(query) && query[i+1] == '-' {
				b.WriteString("--")
				i++
				inLine = true
				continue
			}
			if c == '/' && i+1 < len(query) && query[i+1] == '*' {
				b.WriteString("/*")
				i++
				inBlock = true
				continue
			}
			if c == '\'' {
				inSingle = true
				b.WriteByte(c)
				continue
			}
			if c == '"' {
				inDouble = true
				b.WriteByte(c)
				continue
			}
			if c == '`' {
				inIdent = true
				b.WriteByte('"')
				continue
			}
			if c == '?' {
				n++
				b.WriteByte('$')
				b.WriteString(strconv.Itoa(n))
				continue
			}
			if word, ok := callWord(query, i, "IFNULL"); ok {
				b.WriteString("COALESCE")
				i += len(word) - 1
				continue
			}
			if word, ok := callWord(query, i, "instr"); ok {
				b.WriteString("strpos")
				i += len(word) - 1
				continue
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}

func callWord(query string, i int, word string) (string, bool) {
	if i+len(word) > len(query) {
		return "", false
	}
	if !strings.EqualFold(query[i:i+len(word)], word) {
		return "", false
	}
	if i > 0 && (unicode.IsLetter(rune(query[i-1])) || unicode.IsDigit(rune(query[i-1])) || query[i-1] == '_') {
		return "", false
	}
	j := i + len(word)
	for j < len(query) && (query[j] == ' ' || query[j] == '\n' || query[j] == '\t') {
		j++
	}
	if j >= len(query) || query[j] != '(' {
		return "", false
	}
	return query[i : i+len(word)], true
}

// IsEncryptedIndexFile 判断文件名是否属于加密笔记本的 SQLCipher 索引。这类文件不能进入 PostgreSQL。
func IsEncryptedIndexFile(name string) bool {
	base := name
	if slash := strings.LastIndexAny(name, `/\`); slash >= 0 {
		base = name[slash+1:]
	}
	return strings.Contains(base, "siyuan-encrypted-")
}

// ConflictUpdate 给 INSERT ... VALUES 追加 ON CONFLICT DO UPDATE。conflict 中的列不参与 SET。
func ConflictUpdate(stmt string, conflict []string) string {
	if strings.Contains(strings.ToUpper(stmt), "ON CONFLICT") {
		return stmt
	}
	cols := insertColumns(stmt)
	skip := map[string]bool{}
	for _, col := range conflict {
		skip[col] = true
	}
	sets := make([]string, 0, len(cols))
	for _, col := range cols {
		if skip[col] {
			continue
		}
		sets = append(sets, col+" = EXCLUDED."+col)
	}
	target := strings.Join(conflict, ", ")
	if len(sets) == 0 {
		return stmt + " ON CONFLICT (" + target + ") DO NOTHING"
	}
	return stmt + " ON CONFLICT (" + target + ") DO UPDATE SET " + strings.Join(sets, ", ")
}

func insertColumns(stmt string) []string {
	start := strings.Index(stmt, "(")
	end := strings.Index(stmt, ")")
	if start < 0 || end <= start {
		return nil
	}
	parts := strings.Split(stmt[start+1:end], ",")
	cols := make([]string, 0, len(parts))
	for _, part := range parts {
		col := strings.TrimSpace(part)
		if col != "" {
			cols = append(cols, col)
		}
	}
	return cols
}
