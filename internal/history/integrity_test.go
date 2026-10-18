package history

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestUndoPreservesExactRealValues(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	if _, err := d.Exec("CREATE TABLE Analysis(id INTEGER PRIMARY KEY,bpm REAL);INSERT INTO Analysis VALUES(1,120.0)"); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "analysis", nil, func(context.Context) error {
		_, err := d.Exec("UPDATE Analysis SET bpm=?", math.Nextafter(120, 121))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var got float64
	if err = d.QueryRow("SELECT bpm FROM Analysis").Scan(&got); err != nil || got != 120 {
		t.Fatalf("lost precision: %.17g %v", got, err)
	}
}

func TestUndoPreservesNonUTF8Text(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	if _, err := d.Exec("CREATE TABLE Imported(id INTEGER PRIMARY KEY,value TEXT);INSERT INTO Imported VALUES(1,CAST(x'ff' AS TEXT))"); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "edit", nil, func(context.Context) error {
		_, err := d.Exec("UPDATE Imported SET value=CAST(x'fe' AS TEXT)")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var got string
	if err = d.QueryRow("SELECT hex(value) FROM Imported").Scan(&got); err != nil || got != "FF" {
		t.Fatalf("changed bytes: %s %v", got, err)
	}
}

func TestUndoRefusesDamagedDatabaseSnapshot(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	e, err := Run(ctx, lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title='Edited'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	for path, state := range e.Before.Databases {
		if err = copyDatabase(ctx, e.After.Databases[path].Path, state.Path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true}); err == nil {
		t.Fatal("accepted damaged snapshot")
	}
	var title string
	if err = d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Edited" {
		t.Fatalf("changed source: %s %v", title, err)
	}
}

func TestCaptureRepairsDamagedRetainedContent(t *testing.T) {
	lib, _, file := fixture(t)
	ctx := context.Background()
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	state, err := saveFile(ctx, file, filepath.Join(dir, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "objects", state.Hash), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "edit", nil, func(context.Context) error { return os.WriteFile(file, []byte("modified"), 0600) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != "original audio" {
		t.Fatalf("lost original %q %v", got, err)
	}
}

func TestHistoryRejectsIncompleteEntry(t *testing.T) {
	lib, _, _ := fixture(t)
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"id":"bad","state":"pending"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = List(lib); err == nil {
		t.Fatal("accepted incomplete entry")
	}
}

func TestHistoryRejectsDamagedLocationRecord(t *testing.T) {
	lib, _, _ := fixture(t)
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	path := locationPath(lib, dir)
	if err = os.WriteFile(path, []byte("../invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = List(lib); err == nil {
		t.Fatal("accepted damaged location record")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = List(lib); err == nil {
		t.Fatal("accepted unreadable location record")
	}
}

func TestInterruptedOperationRecovery(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	e, err := Run(ctx, lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title='Edited'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	e.After = nil
	e.State = "pending"
	if err = writeEntry(dir, e); err != nil {
		t.Fatal(err)
	}
	if _, err = Run(ctx, lib, "blocked", nil, func(context.Context) error { t.Fatal("ran pending operation"); return nil }); err == nil {
		t.Fatal("accepted pending")
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{}); err == nil {
		t.Fatal("recovered without force")
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = Run(ctx, lib, "next", nil, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
