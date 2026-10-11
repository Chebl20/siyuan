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

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/siyuan-note/siyuan/kernel/indexstore"
	"github.com/siyuan-note/siyuan/kernel/indexstore/postgres"
	"github.com/siyuan-note/siyuan/kernel/util"
	"github.com/spf13/cobra"
)

var (
	migrateFrom        string
	migrateTo          string
	migrateDatabaseURL string
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Copy the rebuildable SQLite index into PostgreSQL",
	Long:  "Copy temp/siyuan.db, blocktree.db, history.db and asset_content.db into PostgreSQL. Encrypted notebook indexes are skipped. Stop SiYuan first. Running again updates rows in place.",
	RunE:  runMigrate,
}

func init() {
	migrateCmd.Flags().StringVar(&migrateFrom, "from", "sqlite", "source index driver")
	migrateCmd.Flags().StringVar(&migrateTo, "to", "postgres", "destination index driver")
	migrateCmd.Flags().StringVar(&migrateDatabaseURL, "database-url", "", "postgres URL; defaults to DATABASE_URL")
	rootCmd.AddCommand(migrateCmd)
}

func runMigrate(cmd *cobra.Command, args []string) error {
	if migrateFrom != "sqlite" || migrateTo != "postgres" {
		return fmt.Errorf("only --from sqlite --to postgres is supported")
	}
	if migrateDatabaseURL == "" {
		migrateDatabaseURL = os.Getenv("DATABASE_URL")
	}
	if migrateDatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if workspacePath == "" {
		workspacePath = os.Getenv("SIYUAN_WORKSPACE_PATH")
	}
	if workspacePath == "" {
		return fmt.Errorf("workspace is required")
	}
	if _, err := os.Stat(workspacePath); err != nil {
		return fmt.Errorf("directory not found: %s", workspacePath)
	}
	if !util.IsWorkspaceDir(workspacePath) {
		return fmt.Errorf("not a valid workspace: %s", workspacePath)
	}
	if util.IsWorkspaceLocked(workspacePath) {
		return fmt.Errorf("workspace is locked; stop SiYuan before migrating")
	}
	util.InitWorkspace(workspacePath, "")
	if err := os.MkdirAll(util.TempDir, 0755); err != nil {
		return err
	}

	db, err := postgres.Open(migrateDatabaseURL, 4)
	if err != nil {
		return err
	}
	defer db.Close()
	if err = postgres.Migrate(db); err != nil {
		return err
	}
	report, err := indexstore.CopySQLiteIndex(indexstore.SQLitePaths{
		Content:      util.DBPath,
		BlockTree:    util.BlockTreeDBPath,
		History:      util.HistoryDBPath,
		AssetContent: util.AssetContentDBPath,
		TempDir:      util.TempDir,
	}, db)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	reportPath := indexstore.ReportPath(util.TempDir)
	if err = os.WriteFile(reportPath, append(body, '\n'), 0644); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "migration report: %s\n", reportPath)
	if !report.OK {
		return fmt.Errorf("migration row counts diverged")
	}
	return nil
}
