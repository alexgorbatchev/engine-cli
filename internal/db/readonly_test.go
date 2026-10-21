package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyOpenNeverCreatesOrWrites(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.db")
	d, err := Open(missing, true)
	if err == nil {
		d.Close()
		t.Fatal("read-only open accepted a missing database")
	}
	if _, err = os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("read-only open created a database: %v", err)
	}
	_, path := createTestDB(t)
	d, err = Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err = d.Exec("CREATE TABLE NeverWritten(id INTEGER)"); err == nil {
		t.Fatal("read-only connection accepted a write")
	}
}
