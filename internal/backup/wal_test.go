package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestBackupIncludesCommittedWAL(t *testing.T) {
	for _, dir := range []string{"Database2", "."} {
		t.Run(dir, func(t *testing.T) {
			library := t.TempDir()
			if err := os.MkdirAll(filepath.Join(library, dir), 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(library, dir, "m.db")
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			database.SetMaxOpenConns(1)
			if _, err := database.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE tracks(title TEXT); INSERT INTO tracks(rowid,title) VALUES(42,'Committed in WAL');`); err != nil {
				t.Fatal(err)
			}
			wal, err := os.Stat(path + "-wal")
			if err != nil || wal.Size() == 0 {
				t.Fatalf("fixture has no WAL: %v", err)
			}
			// Keep an uncommitted change open: the snapshot must include only committed data.
			tx, err := database.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec("INSERT INTO tracks VALUES('Uncommitted')"); err != nil {
				t.Fatal(err)
			}
			info, err := CreateBackup(context.Background(), library, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			folder := dir
			if dir == "." {
				folder = "Root"
			}
			// Move only the database into isolation: no journal sidecar may be needed.
			data, err := os.ReadFile(filepath.Join(info.Path, folder, "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			isolated := filepath.Join(t.TempDir(), "snapshot.db")
			if err := os.WriteFile(isolated, data, 0o600); err != nil {
				t.Fatal(err)
			}
			snapshot, err := sql.Open("sqlite", isolated)
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Close()
			var count, id int
			var title string
			if err := snapshot.QueryRow("SELECT count(*),rowid,title FROM tracks").Scan(&count, &id, &title); err != nil {
				t.Fatalf("reading standalone backup while source WAL is open: %v", err)
			}
			if count != 1 || id != 42 || title != "Committed in WAL" {
				t.Fatalf("incorrect snapshot: count=%d rowid=%d title=%q", count, id, title)
			}
			var integrity string
			if err := snapshot.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("backup integrity=%q error=%v", integrity, err)
			}
			if _, err := os.Stat(path + "-wal"); err != nil {
				t.Fatalf("backup disturbed live WAL: %v", err)
			}
		})
	}
}

func TestSnapshotCopiesMultipleBatches(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "large.db")
	database, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("CREATE TABLE payload(id INTEGER PRIMARY KEY, data BLOB)"); err != nil {
		t.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	const rows = 300
	const bytesPerRow = 4096
	data := make([]byte, bytesPerRow)
	for i := 0; i < rows; i++ {
		if _, err := tx.Exec("INSERT INTO payload VALUES(?,?)", i, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "snapshot.db")
	size, err := snapshotDatabase(context.Background(), source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if size <= 256*4096 {
		t.Fatalf("fixture failed to exercise multiple backup batches: %d bytes", size)
	}
	snapshot, err := sql.Open("sqlite", destination)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var count, bytes int
	if err := snapshot.QueryRow("SELECT count(*),sum(length(data)) FROM payload").Scan(&count, &bytes); err != nil {
		t.Fatal(err)
	}
	if count != rows || bytes != rows*bytesPerRow {
		t.Fatalf("incomplete backup: rows=%d bytes=%d", count, bytes)
	}
}
