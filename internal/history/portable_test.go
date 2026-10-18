package history

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPortableStorageErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, lib := range []string{"", file, file + "-missing"} {
		if _, err := List(lib); err == nil {
			t.Fatalf("accepted invalid library %q", lib)
		}
		if _, err := Undo(context.Background(), lib, "", UndoOptions{}); err == nil {
			t.Fatalf("undo accepted invalid library %q", lib)
		}
	}
	if _, err := os.Stat(".engine-cli"); !os.IsNotExist(err) {
		t.Fatalf("invalid input created local history: %v", err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove working directory: %v", err)
	}
	if _, err := List("."); err == nil {
		t.Fatal("accepted unavailable working directory")
	}
}

func TestPortableDeletedFiles(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "library audio", true: "external audio"}[external], func(t *testing.T) {
			lib, d, file := fixture(t)
			if external {
				file = filepath.Join(t.TempDir(), "external.mp3")
				if err := os.WriteFile(file, []byte("original audio"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			e, err := Run(ctx, lib, "delete", nil, func(ctx context.Context) error {
				if err := Preserve(ctx, file); err != nil {
					return err
				}
				return os.Remove(file)
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = d.Close(); err != nil {
				t.Fatal(err)
			}
			moved := filepath.Join(t.TempDir(), "Library")
			if err = os.Rename(lib, moved); err != nil {
				t.Fatal(err)
			}
			if !external {
				file = filepath.Join(moved, filepath.Base(file))
			}
			u, err := Undo(ctx, moved, e.ID, UndoOptions{})
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(file)
			if err != nil || string(data) != "original audio" {
				t.Fatalf("lost retained contents %q: %v", data, err)
			}
			if _, err = Undo(ctx, moved, u.ID, UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(file); !os.IsNotExist(err) {
				t.Fatalf("redo did not delete audio: %v", err)
			}
		})
	}
}

func TestPortableHistory(t *testing.T) {
	lib, d, file := fixture(t)
	ctx := context.Background()
	dest := filepath.Join(lib, "moved.mp3")
	e, err := Run(ctx, lib, "move", nil, func(ctx context.Context) error {
		if err := MoveFile(ctx, file, dest); err != nil {
			return err
		}
		_, err := d.Exec("UPDATE Track SET title='Edited'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(lib, ".engine-cli", "history", e.ID+".json")); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(filepath.Join(lib, ".engine-cli", "history", e.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(lib)) {
		t.Fatal("history contains absolute library paths")
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "Library")
	if err = os.Rename(lib, moved); err != nil {
		t.Fatal(err)
	}
	list, err := List(moved)
	if err != nil || len(list) != 1 || list[0].LibraryID != e.LibraryID {
		t.Fatalf("lost portable identity: %v %v", list, err)
	}
	u, err := Undo(ctx, moved, e.ID, UndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(moved, filepath.Base(file))
	data, err := os.ReadFile(restored)
	if err != nil || string(data) != "original audio" {
		t.Fatalf("restored %q: %v", data, err)
	}
	if _, err = os.Stat(lib); !os.IsNotExist(err) {
		t.Fatalf("wrote old library location: %v", err)
	}
	if _, err = Undo(ctx, moved, u.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(moved, "moved.mp3")); err != nil {
		t.Fatal(err)
	}
}

func TestUndoUsesOSTemporaryWorkspace(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	e, err := Run(ctx, lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title='Edited'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	p, cleanup, err := prepareUndo(ctx, lib, dir, e, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range p.databases {
		if filepath.Dir(filepath.Dir(path)) != root {
			cleanup()
			t.Fatalf("scratch database outside OS temp: %s", path)
		}
	}
	cleanup()
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 {
		t.Fatalf("scratch not cleaned: %v %v", files, err)
	}
	if err = os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if _, _, err = prepareUndo(ctx, lib, dir, e, false); err == nil {
		t.Fatal("accepted unavailable OS temp")
	}
}
