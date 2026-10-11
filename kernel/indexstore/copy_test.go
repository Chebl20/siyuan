package indexstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeRowConvertsSQLiteBytes(t *testing.T) {
	spec := copySpec{columns: []string{"id", "length", "embedding"}, intCols: []string{"length"}}
	got := normalizeRow(spec, []any{[]byte("abc"), nil, nil})
	if got[0] != "abc" || got[1] != int64(0) || got[2] != nil {
		t.Fatalf("%#v", got)
	}
}

func TestEncryptedIndexFilesAreListedAndNotOpened(t *testing.T) {
	dir := t.TempDir()
	name := "siyuan-encrypted-box.db"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("cipher"), 0644); err != nil {
		t.Fatal(err)
	}
	got := encryptedIndexFiles(dir)
	if len(got) != 1 || got[0] != name {
		t.Fatalf("%v", got)
	}
}
