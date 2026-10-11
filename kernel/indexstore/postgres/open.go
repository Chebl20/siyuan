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

package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/siyuan-note/siyuan/kernel/indexstore"
)

//go:embed migrations/*.sql
var migrations embed.FS

const schemaVersion = "0001"

// Open 打开 PostgreSQL，并把 SQLite 占位符在驱动层改成 PostgreSQL 占位符。
func Open(databaseURL string, maxOpen int) (*sql.DB, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("DATABASE_URL is required when STORAGE_DRIVER=postgres")
	}
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	base := stdlib.GetConnector(*cfg, stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
		_, execErr := conn.Exec(ctx, "SET search_path TO siyuan")
		return execErr
	}))
	db := sql.OpenDB(rewriteConnector{base: base})
	if maxOpen < 1 {
		maxOpen = 4
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxOpen)
	db.SetConnMaxLifetime(365 * 24 * time.Hour)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate 创建 schema siyuan。已应用的版本不会重复执行。
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE SCHEMA IF NOT EXISTS siyuan`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS siyuan.schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM siyuan.schema_migrations WHERE version = $1`, schemaVersion).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	body, err := fs.ReadFile(migrations, "migrations/0001_index.sql")
	if err != nil {
		return err
	}
	if _, err = db.Exec(string(body)); err != nil {
		return fmt.Errorf("apply postgres index migration %s: %w", schemaVersion, err)
	}
	_, err = db.Exec(`INSERT INTO siyuan.schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, schemaVersion)
	return err
}

// TruncatePlainIndex 清空可重建的明文索引，不删除 database，也不触碰加密笔记本。
func TruncatePlainIndex(db *sql.DB) error {
	_, err := db.Exec(`TRUNCATE TABLE
		blocks, block_search, spans, assets, attributes, refs, file_annotation_refs, block_embeddings,
		blocktrees_row, histories, history_search, asset_contents, asset_content_search
		RESTART IDENTITY`)
	return err
}

type rewriteConnector struct {
	base driver.Connector
}

func (c rewriteConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &rewriteConn{inner: conn}, nil
}

func (c rewriteConnector) Driver() driver.Driver {
	return c.base.Driver()
}

type rewriteConn struct {
	inner driver.Conn
}

func (c *rewriteConn) Prepare(query string) (driver.Stmt, error) {
	return c.inner.Prepare(indexstore.Adapt(query))
}

func (c *rewriteConn) Close() error {
	return c.inner.Close()
}

func (c *rewriteConn) Begin() (driver.Tx, error) {
	return c.inner.Begin()
}

func (c *rewriteConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if preparer, ok := c.inner.(driver.ConnPrepareContext); ok {
		return preparer.PrepareContext(ctx, indexstore.Adapt(query))
	}
	return c.Prepare(query)
}

func (c *rewriteConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := c.inner.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return execer.ExecContext(ctx, indexstore.Adapt(query), args)
}

func (c *rewriteConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.inner.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return queryer.QueryContext(ctx, indexstore.Adapt(query), args)
}

func (c *rewriteConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if beginner, ok := c.inner.(driver.ConnBeginTx); ok {
		return beginner.BeginTx(ctx, opts)
	}
	return c.inner.Begin()
}

func (c *rewriteConn) Ping(ctx context.Context) error {
	if pinger, ok := c.inner.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (c *rewriteConn) ResetSession(ctx context.Context) error {
	if resetter, ok := c.inner.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (c *rewriteConn) IsValid() bool {
	if validator, ok := c.inner.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

func (c *rewriteConn) CheckNamedValue(nv *driver.NamedValue) error {
	if checker, ok := c.inner.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}
