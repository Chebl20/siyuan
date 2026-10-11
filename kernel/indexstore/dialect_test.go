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

import "testing"

func TestAdaptRebindsPlaceholdersAndBackticks(t *testing.T) {
	got := Adapt("SELECT * FROM stat WHERE `key` = ? AND name = ?")
	want := `SELECT * FROM stat WHERE "key" = $1 AND name = $2`
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestAdaptKeepsQuestionMarksInsideStrings(t *testing.T) {
	got := Adapt("SELECT '?', `matches` FROM blocks WHERE id = ?")
	want := `SELECT '?', "matches" FROM blocks WHERE id = $1`
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestAdaptRewritesInstrAndIfNullButNotMatch(t *testing.T) {
	got := Adapt("SELECT IFNULL(name, '') FROM blocks WHERE instr(hpath, ?) > 0 AND blocks_fts MATCH ?")
	want := "SELECT COALESCE(name, '') FROM blocks WHERE strpos(hpath, $1) > 0 AND blocks_fts MATCH $2"
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestConflictUpdateUsesExcludedColumns(t *testing.T) {
	stmt := "INSERT INTO blocks (id, name, content) VALUES (?, ?, ?)"
	got := ConflictUpdate(stmt, []string{"id"})
	want := stmt + " ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, content = EXCLUDED.content"
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestIsEncryptedIndexFile(t *testing.T) {
	if !IsEncryptedIndexFile(`temp/siyuan-encrypted-box.db`) {
		t.Fatal("encrypted content index must be skipped")
	}
	if !IsEncryptedIndexFile("siyuan-encrypted-box-blocktree.db") {
		t.Fatal("encrypted block tree index must be skipped")
	}
	if IsEncryptedIndexFile("siyuan.db") || IsEncryptedIndexFile("blocktree.db") {
		t.Fatal("plain index files are copied")
	}
}
