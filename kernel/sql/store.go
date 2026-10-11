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
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/siyuan-note/siyuan/kernel/util"
)

// IndexStore 是明文索引的最小读写面。调用方继续使用 kernel/sql，不直接依赖 pgx。
type IndexStore interface {
	Begin(ctx context.Context) (*sql.Tx, error)
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// DBIndexStore 把 *sql.DB 适配成 IndexStore。SQLite 与 PostgreSQL 共用这一实现。
type DBIndexStore struct {
	DB *sql.DB
}

func (s DBIndexStore) Begin(ctx context.Context) (*sql.Tx, error) {
	if s.DB == nil {
		return nil, errors.New("database is nil")
	}
	return s.DB.BeginTx(ctx, nil)
}

func (s DBIndexStore) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if s.DB == nil {
		return nil, errors.New("database is nil")
	}
	return s.DB.ExecContext(ctx, query, args...)
}

func (s DBIndexStore) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if s.DB == nil {
		return nil, errors.New("database is nil")
	}
	return s.DB.QueryContext(ctx, query, args...)
}

// GlobalIndexStore 返回当前全局明文索引连接。
func GlobalIndexStore() IndexStore {
	return DBIndexStore{DB: db}
}

// UsePostgres 表示本次进程的明文索引在 PostgreSQL。缺省为假，测试和现有数据仍走 SQLite。
func UsePostgres() bool {
	return util.UsePostgresIndex()
}

// PostgresPlain 为真时，这个笔记本的明文索引在 PostgreSQL。
// 加密笔记本即使在 postgres 驱动下也返回假，读写留在已打开的 SQLCipher 连接上。
func PostgresPlain(boxID string) bool {
	if !UsePostgres() {
		return false
	}
	if boxID == "" {
		return true
	}
	if GetEncryptedDB(boxID) != nil {
		return false
	}
	if IsEncryptedBoxFn != nil && IsEncryptedBoxFn(boxID) {
		return false
	}
	return true
}

var postgresTxs sync.Map

func notePostgresTx(tx *sql.Tx) {
	if tx != nil && UsePostgres() {
		postgresTxs.Store(tx, struct{}{})
	}
}

func isPostgresTx(tx *sql.Tx) bool {
	if tx == nil {
		return false
	}
	_, ok := postgresTxs.Load(tx)
	return ok
}

func unnotePostgresTx(tx *sql.Tx) {
	if tx != nil {
		postgresTxs.Delete(tx)
	}
}
