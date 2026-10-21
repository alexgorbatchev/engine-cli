package libraryops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func fixture(t *testing.T, media string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = db.InitSchema(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("INSERT INTO Track(id,path,title) VALUES(1,?,'Shared track')", media); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAggregateProtectsSharedFilesAndUndo(t *testing.T) {
	ctx := context.Background()
	media := filepath.Join(t.TempDir(), "song.mp3")
	if err := os.WriteFile(media, []byte("audio bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	source, peer := fixture(t, media), fixture(t, media)
	opts := Options{Path: source, Action: "aggregate", Mode: "move", SharedMode: "copy"}
	known := []Library{{Path: source}, {Path: peer, Name: "Other collection"}}
	plan, err := Preview(ctx, opts, known)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Applicable || len(plan.Files) != 1 || plan.Files[0].Action != "copy" || len(plan.AffectedLibraries) != 1 {
		t.Fatalf("unsafe plan: %+v", plan)
	}
	opts.PlanToken = plan.PlanToken
	entry, err := history.Run(ctx, source, "library.media.aggregate", nil, func(ctx context.Context) error { return Apply(ctx, opts, known) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(media); err != nil {
		t.Fatalf("shared source removed: %v", err)
	}
	if _, err = os.Stat(plan.Files[0].Destination); err != nil {
		t.Fatal(err)
	}
	if _, err = history.Undo(ctx, source, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(plan.Files[0].Destination); !os.IsNotExist(err) {
		t.Fatalf("undo retained installed file: %v", err)
	}
}

func TestPreviewDetectsStaleMediaAndUnreadablePeers(t *testing.T) {
	ctx := context.Background()
	media := filepath.Join(t.TempDir(), "song.mp3")
	if err := os.WriteFile(media, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	source := fixture(t, media)
	opts := Options{Path: source, Action: "aggregate", Mode: "move", SharedMode: "move"}
	plan, err := Preview(ctx, opts, []Library{{Path: source}, {Path: filepath.Join(t.TempDir(), "offline")}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Applicable || len(plan.UnavailableLibraries) != 1 {
		t.Fatalf("unreadable library hidden: %+v", plan)
	}
	plan, err = Preview(ctx, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts.PlanToken = plan.PlanToken
	if err = os.WriteFile(media, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = Apply(ctx, opts, nil); err == nil {
		t.Fatal("stale plan applied")
	}
	if _, err = os.Stat(plan.Files[0].Destination); !os.IsNotExist(err) {
		t.Fatalf("stale plan wrote file: %v", err)
	}
}

func TestMoveLibraryAndUndoPreserveIdentityAndExternalPaths(t *testing.T) {
	ctx := context.Background()
	media := filepath.Join(t.TempDir(), "song.mp3")
	if err := os.WriteFile(media, []byte("external audio"), 0600); err != nil {
		t.Fatal(err)
	}
	source := fixture(t, media)
	destination := filepath.Join(t.TempDir(), "Moved library")
	opts := Options{Path: source, Action: "move", Destination: destination}
	p, err := Preview(ctx, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts.PlanToken = p.PlanToken
	e, err := history.Run(ctx, source, "library.move", nil, func(ctx context.Context) error { return Apply(ctx, opts, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if e.Library != destination || e.RelocationFrom != source {
		t.Fatalf("journal not moved: %+v", e)
	}
	if _, err = os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err = history.Undo(ctx, destination, e.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(source, "Database2", "m.db")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("undo retained destination: %v", err)
	}
}
