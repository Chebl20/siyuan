package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestPostgresRoundTripAndIdempotentUpsert(t *testing.T) {
	url := os.Getenv("SIYUAN_POSTGRES_TEST_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" || !strings.Contains(strings.ToLower(url), "test") {
		t.Skip("set SIYUAN_POSTGRES_TEST_URL to a database whose name contains test")
	}
	db, err := Open(url, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = TruncatePlainIndex(db); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO blocks (id, content) VALUES (?, ?) ON CONFLICT (id) DO UPDATE SET content = EXCLUDED.content`
	if _, err = db.Exec(insert, "block", "笔记"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(insert, "block", "笔记"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM blocks WHERE id = $1`, "block").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("upsert duplicated rows: %d", n)
	}
	var content string
	if err = db.QueryRow(`SELECT content FROM blocks WHERE id = ?`, "block").Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != "笔记" {
		t.Fatalf("content %s", content)
	}
}
