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

package sql

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/siyuan-note/logging"
	"github.com/siyuan-note/siyuan/kernel/indexstore/postgres"
	"github.com/siyuan-note/siyuan/kernel/search"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func initPostgresIndex(forceRebuild bool) {
	if err := openPostgresDB(&db, 20); err != nil {
		logging.LogFatalf(logging.ExitCodeUnavailableDatabase, "open postgres index failed: %s", err)
	}
	initIndexQueue()
	treenode.InitBlockTree(forceRebuild)
	if forceRebuild || util.DatabaseVer != getDatabaseVer() {
		logging.LogInfof("reinitializing postgres index")
		if err := postgres.TruncatePlainIndex(db); err != nil {
			logging.LogFatalf(logging.ExitCodeUnavailableDatabase, "truncate postgres index failed: %s", err)
		}
		treenode.InitBlockTree(true)
		setDatabaseVer()
		setStat("index_driver", "postgres")
		clearQueue()
		return
	}
	if err := cleanupInvalidRefs(db); err != nil {
		logging.LogErrorf("cleanup invalid refs failed: %s", err)
	}
	setStat("index_driver", "postgres")
	recoverIndexQueue()
}

func initPostgresHistory(forceRebuild bool) {
	if err := openPostgresDB(&historyDB, 3); err != nil {
		logging.LogFatalf(logging.ExitCodeUnavailableDatabase, "open postgres history index failed: %s", err)
	}
	if forceRebuild {
		if _, err := historyDB.Exec("TRUNCATE TABLE histories, history_search"); err != nil {
			logging.LogFatalf(logging.ExitCodeUnavailableDatabase, "truncate postgres history index failed: %s", err)
		}
	}
}

func initPostgresAssetContent(forceRebuild bool) {
	if err := openPostgresDB(&assetContentDB, 3); err != nil {
		logging.LogFatalf(logging.ExitCodeUnavailableDatabase, "open postgres asset index failed: %s", err)
	}
	if forceRebuild {
		if _, err := assetContentDB.Exec("TRUNCATE TABLE asset_contents, asset_content_search"); err != nil {
			logging.LogFatalf(logging.ExitCodeUnavailableDatabase, "truncate postgres asset index failed: %s", err)
		}
	}
}

func openPostgresDB(target **sql.DB, maxOpen int) error {
	if *target != nil {
		(*target).Close()
		*target = nil
	}
	opened, err := postgres.Open(util.PostgresDatabaseURL(), maxOpen)
	if err != nil {
		return err
	}
	if err = postgres.Migrate(opened); err != nil {
		opened.Close()
		return err
	}
	*target = opened
	return nil
}

func setStat(key, value string) {
	tx, err := beginTx()
	if err != nil {
		return
	}
	if err = putStat(tx, key, value); err != nil {
		return
	}
	commitTx(tx)
}

func deleteBlocksByIDsPostgres(tx *sql.Tx, ids []string) (err error) {
	for _, id := range ids {
		removeBlockCache(id)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if err = execStmtTx(tx, "DELETE FROM blocks WHERE id IN ("+placeholders+")", args...); err != nil {
		return
	}
	if _, embedErr := tx.Exec("DELETE FROM block_embeddings WHERE id IN ("+placeholders+")", args...); embedErr != nil {
		if !strings.Contains(embedErr.Error(), "no such table") && !strings.Contains(embedErr.Error(), "does not exist") {
			err = embedErr
		}
	}
	return
}

func blockSearchText(parts ...string) string {
	return search.NormalizeSearchText(strings.Join(parts, "\n"), caseSensitive, hanSensitive)
}

func refreshBlockSearchRows(tx *sql.Tx, blocks []*Block) (err error) {
	stmt := `INSERT INTO block_search (id, search_vector) VALUES (?, to_tsvector('simple', ?))
		ON CONFLICT (id) DO UPDATE SET search_vector = EXCLUDED.search_vector`
	for _, block := range blocks {
		text := blockSearchText(block.Name, block.Alias, block.Memo, block.Tag, block.Content, block.FContent, block.IAL)
		if err = execStmtTx(tx, stmt, block.ID, text); err != nil {
			return
		}
	}
	return
}

func refreshBlockSearchID(tx *sql.Tx, id string) (err error) {
	var name, alias, memo, tag, content, fcontent, ial string
	err = tx.QueryRow("SELECT name, alias, memo, tag, content, fcontent, ial FROM blocks WHERE id = ?", id).
		Scan(&name, &alias, &memo, &tag, &content, &fcontent, &ial)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return
	}
	text := blockSearchText(name, alias, memo, tag, content, fcontent, ial)
	stmt := `INSERT INTO block_search (id, search_vector) VALUES (?, to_tsvector('simple', ?))
		ON CONFLICT (id) DO UPDATE SET search_vector = EXCLUDED.search_vector`
	return execStmtTx(tx, stmt, id, text)
}
