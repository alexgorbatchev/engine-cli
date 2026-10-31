package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestUndoRepairPendingRefusesBeforeIntakeReservation(t *testing.T) {
	ctx := t.Context()
	lib, d, _ := fixture(t)
	entry, err := Run(ctx, lib, "track.edit", ScopeCollection, nil, func(context.Context) error {
		_, err := d.Exec("UPDATE Track SET title='Changed'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	if err := os.Mkdir(active, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(active, "pending.json")
	proof := []byte("retained interrupted repair evidence")
	if err := os.WriteFile(marker, proof, 0600); err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		result, err := Undo(ctx, lib, entry.ID, UndoOptions{Force: force, Prepare: func(context.Context) error {
			t.Error("pending repair reached backup preparation")
			return nil
		}})
		if result != nil || progress.ErrorCode(err) != "smartlist_repair_pending" {
			t.Fatalf("pending repair refusal: result=%v error=%v", result, err)
		}
	}
	anchor := filepath.Join(filepath.Dir(lib), ".engine-cli-intake", filepath.Base(lib))
	if _, err := os.Lstat(anchor); !os.IsNotExist(err) {
		t.Fatalf("pending repair enrolled replacement reservation: %v", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != string(proof) {
		t.Fatalf("pending evidence changed: %q %v", got, err)
	}
	if err := os.Rename(marker, filepath.Join(active, "committed.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(ctx, lib, entry.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Original" {
		t.Fatalf("released repair did not permit native Undo: %q %v", title, err)
	}
}
