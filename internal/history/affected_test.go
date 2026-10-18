package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseOnlyDoesNotCaptureFiles(t *testing.T) {
	lib, d, file := fixture(t)
	if err := os.Symlink(file, filepath.Join(lib, "unrelated-link")); err != nil {
		t.Fatal(err)
	}
	e, err := Run(context.Background(), lib, "playlist.edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE Playlist SET title='Changed'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Before.Files) != 0 || len(e.After.Files) != 0 {
		t.Fatal("database edit captured unrelated files")
	}
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "objects")); !os.IsNotExist(err) && (err != nil || len(entries) > 0) {
		t.Fatalf("audio copied: %v %v", entries, err)
	}
}

func TestPreserveOnlySelectedFile(t *testing.T) {
	lib, _, file := fixture(t)
	ctx := context.Background()
	unrelated := filepath.Join(lib, "untouched.mp3")
	if err := os.WriteFile(unrelated, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "delete", nil, func(ctx context.Context) error {
		if err := Preserve(ctx, file); err != nil {
			return err
		}
		return os.Remove(file)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Before.Files) != 1 {
		t.Fatalf("captured %v", e.Before.Files)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "original audio" {
		t.Fatalf("restored %q %v", data, err)
	}
}

func TestMoveUndoWithoutAudioCopies(t *testing.T) {
	lib, _, file := fixture(t)
	ctx := context.Background()
	dest := filepath.Join(lib, "moved.mp3")
	e, err := Run(ctx, lib, "move", nil, func(ctx context.Context) error { return MoveFile(ctx, file, dest) })
	if err != nil {
		t.Fatal(err)
	}
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	if objects, err := os.ReadDir(filepath.Join(dir, "objects")); !os.IsNotExist(err) && (err != nil || len(objects) != 0) {
		t.Fatalf("move copied audio: %v %v", objects, err)
	}
	u, err := Undo(ctx, lib, e.ID, UndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, u.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dest); err != nil {
		t.Fatal(err)
	}
	if objects, err := os.ReadDir(filepath.Join(dir, "objects")); !os.IsNotExist(err) && (err != nil || len(objects) != 0) {
		t.Fatalf("undo/redo copied audio: %v %v", objects, err)
	}
}

func TestMoveOverwrittenDestinationRoundTrip(t *testing.T) {
	lib, _, file := fixture(t)
	ctx := context.Background()
	dest := filepath.Join(lib, "destination.mp3")
	if err := os.WriteFile(dest, []byte("overwritten"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "move", nil, func(ctx context.Context) error { return MoveFile(ctx, file, dest) })
	if err != nil {
		t.Fatal(err)
	}
	u, err := Undo(ctx, lib, e.ID, UndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "overwritten" {
		t.Fatalf("lost destination %q %v", got, err)
	}
	if _, err = Undo(ctx, lib, u.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(dest)
	if err != nil || string(got) != "original audio" {
		t.Fatalf("redo lost moved file %q %v", got, err)
	}
}
