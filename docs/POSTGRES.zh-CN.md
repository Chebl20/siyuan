# PostgreSQL 索引

[English](POSTGRES.md)

文档、配置、历史归档、数据仓库、资源文件和属性视图仍在工作空间目录中。PostgreSQL 只保存可重建的明文索引。`STORAGE_DRIVER=sqlite`，以及未设置 `STORAGE_DRIVER` 时，继续使用 `temp/` 下的 SQLite，并且不会打开 `DATABASE_URL`。

## 迁移范围

| 工作空间路径 | 作用 |
|---|---|
| `data/**/*.sy`、`data/assets/`、`data/storage/`、`conf/conf.json`、`history/`、`repo/` | 数据源。不改变 |
| `temp/siyuan.db`、`temp/blocktree.db`、`temp/history.db`、`temp/asset_content.db` | 明文索引。`STORAGE_DRIVER=postgres` 时改由 PostgreSQL 读写 |
| `temp/siyuan-encrypted-*.db` | 加密笔记本索引。始终留在本地 SQLCipher，不会写入 PostgreSQL |

没有用户表或会话表。访问控制仍是 `conf/conf.json` 和访问授权码 cookie。

## 变量

| 变量 | 含义 |
|---|---|
| `STORAGE_DRIVER` | `sqlite`（默认）或 `postgres` |
| `DATABASE_TYPE` | 仅当 `STORAGE_DRIVER` 为空时生效。`postgres` 和 `postgresql` 选择 postgres 驱动 |
| `DATABASE_URL` | 只在 `STORAGE_DRIVER=postgres` 时必需。不会写入 `conf.json` 或迁移报告 |
| `SIYUAN_WORKSPACE_PATH` / `--workspace` | 工作空间卷。仍然必需 |
| `PUID`、`PGID`、`TZ`、`SIYUAN_ACCESS_AUTH_CODE` | 既有容器设置 |

每个工作空间只运行一个思源进程。PostgreSQL 不能让两个进程同时写同一批 `.sy` 文件。

## 搜索差异

明文搜索使用 `simple` 词典的 `tsvector`，并在块内容和名称上使用 `pg_trgm`。它不复现 SQLite 的 `siyuan` 分词器，因此不会生成中日韩重叠二元组。子串搜索仍由 `pg_trgm` 承担。`LIKE` 的大小写规则可能与 SQLite 的 `case_sensitive_like` 不同。

postgres 模式下，`POST /api/query/sql` 只接受只读的 `SELECT` 和 `WITH`。`MATCH`、`ATTACH`、`regexp()`、`search_normalize()` 和 FTS5 语法会被拒绝。这些语句在 `STORAGE_DRIVER=sqlite` 下仍然有效。

## 迁移

先停止思源，确认工作空间的 `.lock` 未被占用，然后执行：

```bash
kernel migrate --from sqlite --to postgres --workspace /siyuan/workspace --database-url "$DATABASE_URL"
```

该命令应用 schema 迁移，用 `ON CONFLICT` 复制明文行，跳过 `siyuan-encrypted-*.db`，并写入 `temp/migrate-report.json`。报告包含行数、样本哈希和不一致项，不包含数据库 URL。再次执行会按相同 id 更新。表行数不一致时退出码非零。

不复制 SQLite 的等价做法是清空 schema `siyuan`，让启动过程从 `.sy`、`history/` 和资源文件重建索引。重建时不要删除整个 PostgreSQL database。

## Coolify

`deploy/coolify/docker-compose.yml` 使用仓库 Dockerfile 构建，把命名卷挂到 `/siyuan/workspace`，并在内部网络运行 PostgreSQL 16。保持 `replicas: 1`。思源健康检查是 `kernel health`，它请求 `GET /api/system/version`。PostgreSQL 使用 `pg_isready`。

迁移之后，明文索引不再需要 `temp/siyuan.db`。卷仍然要保存文档、`conf.json`、历史、数据仓库、索引队列和加密笔记本数据库。

## 回滚

1. 停止容器。
2. 设置 `STORAGE_DRIVER=sqlite`，并去掉 `DATABASE_URL`。
3. 需要干净的 SQLite 索引时，删除 `temp/siyuan.db`、`temp/blocktree.db`、`temp/history.db` 和 `temp/asset_content.db`。
4. 启动思源。启动过程会从 `.sy`、`history/` 和资源文件重建索引。
5. PostgreSQL schema 可以保留。sqlite 模式不会读取它。

postgres 模式不会删除旧的 SQLite 文件。如果迁移在报告确认行数一致之前中断，把驱动改回 sqlite 即可继续使用这些文件，不必重建索引。

同时备份工作空间卷和 PostgreSQL 转储。只恢复卷并改回 `STORAGE_DRIVER=sqlite` 即可回到文件索引。
