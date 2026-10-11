# PostgreSQL index

[中文](POSTGRES.zh-CN.md)

Documents, configuration, history archives, the data repo, assets, and attribute views stay in the workspace directory. PostgreSQL stores only the rebuildable plaintext index. `STORAGE_DRIVER=sqlite`, and an empty `STORAGE_DRIVER`, keep the current SQLite files under `temp/` and do not open `DATABASE_URL`.

## What moves

| Workspace path | Role |
|---|---|
| `data/**/*.sy`, `data/assets/`, `data/storage/`, `conf/conf.json`, `history/`, `repo/` | Source of truth. Unchanged |
| `temp/siyuan.db`, `temp/blocktree.db`, `temp/history.db`, `temp/asset_content.db` | Plaintext index. Read and written in PostgreSQL when `STORAGE_DRIVER=postgres` |
| `temp/siyuan-encrypted-*.db` | Encrypted notebook index. Always local SQLCipher, never copied to PostgreSQL |

There is no user or session table. Access control remains `conf/conf.json` and the access-auth cookie.

## Variables

| Variable | Meaning |
|---|---|
| `STORAGE_DRIVER` | `sqlite` (default) or `postgres` |
| `DATABASE_TYPE` | Used only when `STORAGE_DRIVER` is empty. `postgres` and `postgresql` select the postgres driver |
| `DATABASE_URL` | Required only for `STORAGE_DRIVER=postgres`. Not written to `conf.json` or the migration report |
| `SIYUAN_WORKSPACE_PATH` / `--workspace` | Workspace volume. Still required |
| `PUID`, `PGID`, `TZ`, `SIYUAN_ACCESS_AUTH_CODE` | Existing container settings |

One SiYuan process per workspace. PostgreSQL does not allow a second writer on the same files.

## Search difference

Plaintext search uses `tsvector` with the `simple` dictionary, plus `pg_trgm` on block content and name. It does not reproduce the SQLite `siyuan` tokenizer, so CJK overlapping bigrams are not generated. Substring search still uses `pg_trgm`. `LIKE` case sensitivity can differ from SQLite `case_sensitive_like`.

`POST /api/query/sql` in postgres mode accepts read-only `SELECT` and `WITH`. `MATCH`, `ATTACH`, `regexp()`, `search_normalize()`, and FTS5 syntax are rejected. Those statements remain valid with `STORAGE_DRIVER=sqlite`.

## Migration

Stop SiYuan so the workspace `.lock` is free, then:

```bash
kernel migrate --from sqlite --to postgres --workspace /siyuan/workspace --database-url "$DATABASE_URL"
```

The command applies `kernel/sql` schema migrations, copies plaintext rows with `ON CONFLICT`, skips `siyuan-encrypted-*.db`, and writes `temp/migrate-report.json`. The report contains counts, sample hashes, and inconsistencies. It does not contain the database URL. A second run updates the same ids. A non-zero exit means a table count differs.

The equivalent without copying SQLite is to truncate the `siyuan` schema and let boot reindex from `.sy`, `history/`, and assets. Do not drop the PostgreSQL database as a rebuild.

## Coolify

`deploy/coolify/docker-compose.yml` builds the repository Dockerfile, mounts a named volume at `/siyuan/workspace`, and runs PostgreSQL 16 on an internal network. Keep `replicas: 1`. The SiYuan health check is `kernel health`, which requests `GET /api/system/version`. PostgreSQL uses `pg_isready`.

After migration, `temp/siyuan.db` is unused for the plaintext index. The volume is still required for documents, `conf.json`, history, the repo, the index queue, and encrypted notebook databases.

## Rollback

1. Stop the container.
2. Set `STORAGE_DRIVER=sqlite` and remove `DATABASE_URL`.
3. Delete `temp/siyuan.db`, `temp/blocktree.db`, `temp/history.db`, and `temp/asset_content.db` when a clean SQLite index is wanted.
4. Start SiYuan. Boot rebuilds the index from `.sy`, `history/`, and assets.
5. The PostgreSQL schema can remain. It is not read in sqlite mode.

Postgres mode does not delete the old SQLite files. If migration is interrupted before the report says counts match, switching the driver back uses those files without a reindex.

Back up the workspace volume and a PostgreSQL dump together. Restoring only the volume is enough when returning to `STORAGE_DRIVER=sqlite`.
