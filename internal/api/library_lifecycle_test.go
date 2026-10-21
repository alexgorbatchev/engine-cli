package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/libraryops"
)

func TestDeleteLibraryPreviewProtectionAndUndo(t *testing.T) {
	s, lib := testAPI(t)
	_, peer := testAPI(t)
	media := filepath.Join(lib, "song.mp3")
	if err := os.WriteFile(media, []byte("audio bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(peer, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("UPDATE Track SET path=?", media); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if _, err = s.openLibraryPaths([]string{peer}); err != nil {
		t.Fatal(err)
	}
	opts := libraryops.Options{Path: lib, DeleteMedia: true, KeepShared: true, DryRun: true}
	raw, _ := json.Marshal(opts)
	result, id, err := s.RunOperation(context.Background(), "library.delete", raw)
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatal("preview wrote history")
	}
	plan := result.(*libraryops.Plan)
	if _, err = os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote library data: %v", err)
	}
	if _, err = s.state.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	opts.DryRun = false
	opts.PlanToken = plan.PlanToken
	opts.ConfirmPath = "wrong"
	raw, _ = json.Marshal(opts)
	if _, _, err = s.RunOperation(context.Background(), "library.delete", raw); err == nil {
		t.Fatal("incorrect path accepted")
	}
	opts.ConfirmPath = lib
	raw, _ = json.Marshal(opts)
	_, id, err = s.RunOperation(context.Background(), "library.delete", raw)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("delete has no undo record")
	}
	if _, err = os.Stat(filepath.Join(lib, "Database2", "m.db")); !os.IsNotExist(err) {
		t.Fatalf("database not deleted: %v", err)
	}
	if _, err = os.Stat(media); err != nil {
		t.Fatalf("shared media removed: %v", err)
	}
	raw = []byte(fmt.Sprintf(`{"library":%q,"id":%q}`, lib, id))
	if _, _, err = s.RunOperation(context.Background(), "library.undo", raw); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(lib, "Database2", "m.db")); err != nil {
		t.Fatal(err)
	}
}
