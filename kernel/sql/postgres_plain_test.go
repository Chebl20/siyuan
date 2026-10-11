package sql

import "testing"

func TestPostgresPlainStaysOffForSQLiteAndEncryptedBoxes(t *testing.T) {
	t.Setenv("STORAGE_DRIVER", "")
	t.Setenv("DATABASE_TYPE", "")
	if UsePostgres() || PostgresPlain("box") {
		t.Fatal("default driver must stay sqlite")
	}

	t.Setenv("STORAGE_DRIVER", "postgres")
	if !PostgresPlain("") || !PostgresPlain("plain") {
		t.Fatal("plain notebooks use postgres")
	}
	previous := IsEncryptedBoxFn
	IsEncryptedBoxFn = func(boxID string) bool { return boxID == "enc" }
	defer func() { IsEncryptedBoxFn = previous }()
	if PostgresPlain("enc") {
		t.Fatal("encrypted notebook indexes must stay on SQLCipher")
	}
}
