package track

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestPlanMoveReviewsTargetsAndConflicts(t *testing.T) {
	d, lib, _ := createTestDB(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(lib, "Sorted"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"song.mp3", "Sorted/taken.mp3"} {
		if err := os.WriteFile(filepath.Join(lib, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(`INSERT INTO Track(id,title,path) VALUES(1,'Song','song.mp3'),(2,'Gone','gone.mp3')`); err != nil {
		t.Fatal(err)
	}
	noShare := func(context.Context, string) ([]string, []string, error) { return nil, nil, nil }
	plan, err := PlanMove(ctx, d, lib, "1", filepath.Join(lib, "Sorted"), noShare)
	if err != nil || !plan.CanApply || plan.From != "song.mp3" || plan.To != "Sorted/song.mp3" || plan.Target != filepath.Join(lib, "Sorted", "song.mp3") || plan.PlanToken == "" {
		t.Fatalf("folder move %+v %v", plan, err)
	}
	if again, _ := PlanMove(ctx, d, lib, "1", filepath.Join(lib, "Sorted"), noShare); again.PlanToken != plan.PlanToken {
		t.Fatal("an unchanged review produced another token")
	}
	if plan.Source != filepath.Join(lib, "song.mp3") {
		t.Fatalf("source %q", plan.Source)
	}
	// A relative destination is inside the library folder, as stored paths are.
	if relative, _ := PlanMove(ctx, d, lib, "1", "Sorted", noShare); relative.Target != plan.Target || relative.PlanToken != plan.PlanToken {
		t.Fatalf("relative folder %+v", relative)
	}
	if relative, _ := PlanMove(ctx, d, lib, "1", "Renamed/new.mp3", noShare); relative.Target != filepath.Join(lib, "Renamed", "new.mp3") || relative.To != "Renamed/new.mp3" {
		t.Fatalf("relative file %+v", relative)
	}
	if relative, _ := PlanMove(ctx, d, lib, "1", "Fresh/", noShare); relative.Target != filepath.Join(lib, "Fresh", "song.mp3") {
		t.Fatalf("relative new folder %+v", relative)
	}
	// A path ending in a separator names a folder that does not exist yet.
	if next, _ := PlanMove(ctx, d, lib, "1", filepath.Join(lib, "New")+"/", noShare); next.To != "New/song.mp3" || !next.CanApply {
		t.Fatalf("new folder %+v", next)
	}
	cases := []struct {
		identifier, destination string
		shared                  SharedLookup
		want                    string
	}{
		{"1", filepath.Join(lib, "Sorted", "taken.mp3"), noShare, "move_target_exists"},
		{"1", filepath.Join(lib, "song.mp3"), noShare, "move_unchanged"},
		{"2", filepath.Join(lib, "Sorted"), noShare, "move_source_missing"},
		{"1", filepath.Join(lib, "Sorted"), func(context.Context, string) ([]string, []string, error) {
			return []string{"/Other Library"}, nil, nil
		}, "move_shared_file"},
		{"1", filepath.Join(lib, "Sorted"), func(context.Context, string) ([]string, []string, error) {
			return nil, []string{"/Unreadable"}, nil
		}, "move_shared_unknown"},
	}
	for _, c := range cases {
		got, err := PlanMove(ctx, d, lib, c.identifier, c.destination, c.shared)
		if err != nil || got.CanApply || strings.Join(got.Conflicts, ",") != c.want {
			t.Fatalf("%s: %+v %v", c.want, got, err)
		}
	}
	var code *progress.Error
	if err := CheckMove(ctx, d, lib, "1", filepath.Join(lib, "Sorted"), "stale", noShare); !errors.As(err, &code) || code.Code != "move_plan_stale" {
		t.Fatalf("stale %v", err)
	}
	if err := CheckMove(ctx, d, lib, "1", filepath.Join(lib, "Sorted"), plan.PlanToken, noShare); err != nil {
		t.Fatalf("reviewed %v", err)
	}
	taken, _ := PlanMove(ctx, d, lib, "1", filepath.Join(lib, "Sorted", "taken.mp3"), noShare)
	if err := CheckMove(ctx, d, lib, "1", filepath.Join(lib, "Sorted", "taken.mp3"), taken.PlanToken, noShare); !errors.As(err, &code) || code.Code != "move_target_exists" {
		t.Fatalf("conflict %v", err)
	}
	failing := func(context.Context, string) ([]string, []string, error) { return nil, nil, errors.New("fixture") }
	for _, bad := range []struct{ id, dest string }{{"1", " "}, {"99", lib}} {
		if _, err := PlanMove(ctx, d, lib, bad.id, bad.dest, noShare); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if _, err := PlanMove(ctx, d, lib, "1", filepath.Join(lib, "Sorted"), failing); err == nil {
		t.Fatal("ignored a failed shared lookup")
	}
	// A file that changes after review makes the token stale.
	if err := os.WriteFile(filepath.Join(lib, "song.mp3"), []byte("edited elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckMove(ctx, d, lib, "1", filepath.Join(lib, "Sorted"), plan.PlanToken, noShare); !errors.As(err, &code) || code.Code != "move_plan_stale" {
		t.Fatalf("changed file %v", err)
	}
}

func TestListFoldersStaysInsideItsRoots(t *testing.T) {
	lib, music := t.TempDir(), t.TempDir()
	for _, dir := range []string{"Sorted/Deep", "b-folder", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(lib, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(lib, "song.mp3"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	real := func(path string) string {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	roots := []FolderRoot{{Label: "Library", Path: real(lib)}, {Label: "Music", Path: real(music)}}
	listing, err := ListFolders(roots, "")
	if err != nil || listing.Path != real(lib) || listing.Parent != "" || len(listing.Folders) != 2 || listing.Folders[0].Name != "b-folder" || listing.Folders[1].Name != "Sorted" {
		t.Fatalf("root listing %+v %v", listing, err)
	}
	inner, err := ListFolders(roots, filepath.Join(lib, "Sorted"))
	if err != nil || inner.Parent != real(lib) || len(inner.Folders) != 1 || inner.Folders[0].Name != "Deep" {
		t.Fatalf("inner listing %+v %v", inner, err)
	}
	var code *progress.Error
	if _, err := ListFolders(roots, t.TempDir()); !errors.As(err, &code) || code.Code != "folder_outside_roots" {
		t.Fatalf("outside %v", err)
	}
	if _, err := ListFolders(nil, ""); !errors.As(err, &code) || code.Code != "folder_outside_roots" {
		t.Fatalf("no roots %v", err)
	}
	if _, err := ListFolders(roots, filepath.Join(lib, "missing")); err == nil {
		t.Fatal("listed a missing folder")
	}
	if got := MoveRoots(lib); len(got) == 0 || got[0].Label != "Library" || got[0].Path != real(lib) {
		t.Fatalf("roots %+v", got)
	}
	// A missing library folder offers only the roots that exist.
	t.Setenv("HOME", music)
	if got := MoveRoots(filepath.Join(lib, "missing")); len(got) != 0 {
		t.Fatalf("roots without folders %+v", got)
	}
}
