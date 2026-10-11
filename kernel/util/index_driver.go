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

package util

import (
	"os"
	"strings"
)

// IndexDriver 返回明文索引的驱动。缺省为 sqlite，此时不读取 DATABASE_URL。
// STORAGE_DRIVER 优先；仅当它为空时，DATABASE_TYPE 为 postgres 或 postgresql 才选择 postgres。
func IndexDriver() string {
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_DRIVER")))
	if driver == "" {
		driver = strings.ToLower(strings.TrimSpace(os.Getenv("DATABASE_TYPE")))
	}
	switch driver {
	case "postgres", "postgresql":
		return "postgres"
	default:
		return "sqlite"
	}
}

// UsePostgresIndex 表示明文索引读写 PostgreSQL。加密笔记本索引始终留在本地 SQLCipher。
func UsePostgresIndex() bool {
	return IndexDriver() == "postgres"
}

// PostgresDatabaseURL 返回 PostgreSQL 连接串。只在 UsePostgresIndex 为真时被使用。
func PostgresDatabaseURL() string {
	return strings.TrimSpace(os.Getenv("DATABASE_URL"))
}
