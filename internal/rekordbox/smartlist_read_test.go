package rekordbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestImportPreparationRefusesPendingDestination(t *testing.T) {
	root, database, options := preparedImportFixture(t)
	dir := filepath.Join(root, ".engine-cli", "smartlist-repair")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(dir, "pending.json")
	if err := os.WriteFile(pending, []byte("interrupted publication intent"), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(t.Context(), database, root, options)
	if prepared != nil || progress.ErrorCode(err) != "smartlist_repair_pending" {
		t.Fatalf("pending destination escaped preparation: %+v %v", prepared, err)
	}
	if err := os.Remove(pending); err != nil {
		t.Fatal(err)
	}
	prepared, err = Prepare(t.Context(), database, root, options)
	if err != nil || prepared == nil {
		t.Fatalf("released destination failed preparation: %+v %v", prepared, err)
	}
	if _, err := prepared.Review(); err != nil {
		t.Fatal(err)
	}
}
