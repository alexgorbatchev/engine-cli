package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/libraryops"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/state"
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

func TestRejectedPlansWriteNoSnapshotOrHistory(t *testing.T) {
	_, lib := testAPI(t)
	s, err := New(Config{LibraryDir: lib})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw, _ := json.Marshal(libraryops.Options{Path: lib, DryRun: true})
	result, _, err := s.RunOperation(context.Background(), "library.delete", raw)
	if err != nil {
		t.Fatal(err)
	}
	token := result.(*libraryops.Plan).PlanToken
	for _, opts := range []libraryops.Options{
		{Path: lib, PlanToken: token, ConfirmPath: filepath.Join(lib, "elsewhere")},
		{Path: lib, PlanToken: "stale", ConfirmPath: lib},
	} {
		raw, _ = json.Marshal(opts)
		if _, _, err = s.RunOperation(context.Background(), "library.delete", raw); err == nil {
			t.Fatalf("unreviewed deletion accepted: %+v", opts)
		}
		if _, err = os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
			t.Fatalf("rejected plan wrote a snapshot or history: %v", err)
		}
	}
}

func TestMoveLibraryKeepsSidebarNameAndRetiresOldPath(t *testing.T) {
	s, lib := testAPI(t)
	ctx := context.Background()
	name, hidden := "Club set", true
	if err := s.state.Update(ctx, lib, &name, &hidden); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "Moved library")
	opts := libraryops.Options{Path: lib, Destination: destination, DryRun: true}
	raw, _ := json.Marshal(opts)
	result, _, err := s.RunOperation(ctx, "library.move", raw)
	if err != nil {
		t.Fatal(err)
	}
	opts.DryRun = false
	opts.PlanToken = result.(*libraryops.Plan).PlanToken
	raw, _ = json.Marshal(opts)
	if _, _, err = s.RunOperation(ctx, "library.move", raw); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(destination, "Database2", "m.db")); err != nil {
		t.Fatalf("library not moved: %v", err)
	}
	entries, err := s.state.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]state.Library{}
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	if old := byPath[lib]; !old.Retired {
		t.Fatalf("old path still active: %+v", entries)
	}
	if moved := byPath[destination]; moved.Name != name || moved.Hidden || moved.Retired {
		t.Fatalf("moved library lost its name or stayed hidden: %+v", entries)
	}
	// The retired old path no longer exists, but it must not be reported as an unavailable peer.
	raw, _ = json.Marshal(libraryops.Options{Path: destination, DryRun: true})
	result, _, err = s.RunOperation(ctx, "library.delete", raw)
	if err != nil {
		t.Fatal(err)
	}
	if unavailable := result.(*libraryops.Plan).UnavailableLibraries; len(unavailable) != 0 {
		t.Fatalf("retired library reported as unavailable: %+v", unavailable)
	}
}

func TestStateDatabaseReachedThroughSymlinkCannotBeManaged(t *testing.T) {
	_, lib := testAPI(t)
	link := filepath.Join(t.TempDir(), "library link")
	if err := os.Symlink(lib, link); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{LibraryDir: lib, NoBackup: true, StatePath: filepath.Join(link, "state.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, dryRun := range []bool{true, false} {
		raw, _ := json.Marshal(libraryops.Options{Path: lib, ConfirmPath: lib, DryRun: dryRun})
		_, _, err = s.RunOperation(context.Background(), "library.delete", raw)
		var failure *progress.Error
		if !errors.As(err, &failure) || failure.Code != "state_database_inside_library" {
			t.Fatalf("dry run %t: deletion allowed its own state database: %v", dryRun, err)
		}
	}
	if _, err = os.Stat(filepath.Join(lib, "Database2", "m.db")); err != nil {
		t.Fatalf("refused deletion changed the library: %v", err)
	}
}

func TestLifecycleOperationsRejectEmptyPathsBeforeStartingJobs(t *testing.T) {
	s, _ := testAPI(t)
	for _, name := range []string{"library.delete", "library.move", "library.media.aggregate"} {
		for _, body := range []string{`{"dry_run":true}`, `{"path":null,"dry_run":true}`, `{"path":"  ","dry_run":true}`} {
			response := requestAPI(t, s, http.MethodPost, "/v1/operations/"+name, body)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_parameters"`) {
				t.Fatalf("%s started a job for %s: %d %s", name, body, response.Code, response.Body)
			}
		}
	}
}
