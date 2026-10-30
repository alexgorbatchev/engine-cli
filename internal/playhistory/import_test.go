package playhistory

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
)

// importFixture records a set that plays One, Two, One again, and a track the library no longer has.
func importFixture(t *testing.T) (string, *ImportOptions, func() *ImportPlan) {
	t.Helper()
	root, library := fixture(t, `INSERT INTO Historylist(id,title,startTime,timezone,isDeleted) VALUES(1,'Friday',1700000000,'UTC',0);
		INSERT INTO HistorylistEntity(listId,trackId,startTime) VALUES(1,10,100),(1,11,200),(1,10,300),(1,12,400);`)
	opts := &ImportOptions{Session: 1, DryRun: true}
	preview := func() *ImportPlan {
		t.Helper()
		plan, err := PreviewImport(context.Background(), root, library, *opts)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	t.Cleanup(func() { library.Close() })
	return root, opts, preview
}

func openLibrary(t *testing.T, root string) *sql.DB {
	t.Helper()
	library, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { library.Close() })
	return library
}

func TestImportReviewKeepsFirstPlaysAndBlocksMissingTracksUntilSkipped(t *testing.T) {
	_, opts, preview := importFixture(t)
	plan := preview()
	var statuses []string
	for _, e := range plan.Entries {
		statuses = append(statuses, e.Status)
	}
	if plan.Name != "Friday" || plan.SessionTitle != "Friday" || plan.ParentID != 0 ||
		!reflect.DeepEqual(plan.TrackIDs, []int64{1, 2}) || plan.DuplicateCount != 1 || plan.MissingCount != 1 ||
		!reflect.DeepEqual(statuses, []string{statusAvailable, statusAvailable, importDuplicate, statusMissing}) ||
		plan.CanApply || plan.PlanToken == "" {
		t.Fatalf("plan = %+v", plan)
	}
	opts.SkipMissing = true
	if skipped := preview(); !skipped.CanApply || skipped.PlanToken == plan.PlanToken {
		t.Fatalf("skipping plan = %+v", skipped)
	}
}

func TestImportCreatesThePlaylistInPlayOrderAndLeavesHistoryUnchanged(t *testing.T) {
	root, opts, preview := importFixture(t)
	library := openLibrary(t, root)
	historyPath := filepath.Join(root, "Database2", File)
	before, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	opts.SkipMissing = true
	opts.Name = "  Friday set "
	plan := preview()
	opts.DryRun, opts.PlanToken = false, plan.PlanToken
	if err := CheckImport(context.Background(), root, library, *opts); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyImport(context.Background(), root, library, *opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "Friday set" || result.ImportedCount != 2 || result.DuplicateCount != 1 || result.SkippedCount != 1 {
		t.Fatalf("result = %+v", result)
	}
	tracks, err := db.GetPlaylistTracks(context.Background(), library, result.PlaylistID)
	if err != nil || len(tracks) != 2 || tracks[0].ID != 1 || tracks[1].ID != 2 {
		t.Fatalf("playlist tracks = %+v, %v", tracks, err)
	}
	after, err := os.ReadFile(historyPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("history database changed: %v", err)
	}
	// The applied review is spent: the same name is now taken.
	if _, err := ApplyImport(context.Background(), root, library, *opts); code(err) != "session_import_target" {
		t.Fatalf("repeated import err = %v", err)
	}
}

func TestImportRefusesStaleUnresolvedAndCancelledApplications(t *testing.T) {
	root, opts, preview := importFixture(t)
	library := openLibrary(t, root)
	plan := preview()
	opts.DryRun, opts.PlanToken = false, plan.PlanToken
	if _, err := ApplyImport(context.Background(), root, library, *opts); code(err) != "session_import_unresolved" {
		t.Fatalf("unresolved err = %v", err)
	}
	opts.SkipMissing = true
	opts.DryRun = true
	opts.PlanToken = preview().PlanToken
	opts.DryRun = false
	writeHistory(t, root, "INSERT INTO HistorylistEntity(listId,trackId,startTime) VALUES(1,11,500)")
	if err := CheckImport(context.Background(), root, library, *opts); code(err) != "session_import_stale" {
		t.Fatalf("stale err = %v", err)
	}
	opts.PlanToken = ""
	if _, err := ApplyImport(context.Background(), root, library, *opts); code(err) != "session_import_stale" {
		t.Fatalf("missing token err = %v", err)
	}
	opts.DryRun = true
	opts.PlanToken = preview().PlanToken
	opts.DryRun = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ApplyImport(ctx, root, library, *opts); err == nil {
		t.Fatal("cancelled import succeeded")
	}
	var playlists int
	if err := library.QueryRow("SELECT COUNT(*) FROM Playlist").Scan(&playlists); err != nil || playlists != 0 {
		t.Fatalf("playlists after refused imports = %d, %v", playlists, err)
	}
}

func TestImportNamesDefaultLikeEngineAndRejectInvalidTargets(t *testing.T) {
	root, opts, preview := importFixture(t)
	library := openLibrary(t, root)
	for _, title := range []string{"Friday", "Friday 2"} {
		if _, err := db.CreatePlaylist(context.Background(), library, title, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	if plan := preview(); plan.Name != "Friday 3" {
		t.Fatalf("default name = %q", plan.Name)
	}
	parent, err := db.CreatePlaylist(context.Background(), library, "Sets", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	opts.Parent = "eng:missing"
	if _, err := PreviewImport(context.Background(), root, library, *opts); code(err) == "" {
		t.Fatalf("unknown reference accepted")
	}
	for _, bad := range []ImportOptions{
		{Session: 1, Name: "Friday", DryRun: true},
		{Session: 1, Parent: "404", DryRun: true},
		{Session: 1, Parent: "x", DryRun: true},
	} {
		if _, err := PreviewImport(context.Background(), root, library, bad); code(err) != "session_import_target" {
			t.Fatalf("%+v err = %v", bad, err)
		}
	}
	nested, err := PreviewImport(context.Background(), root, library, ImportOptions{Session: 1, Parent: "0", DryRun: true})
	if err != nil || nested.ParentID != 0 {
		t.Fatalf("root parent = %+v, %v", nested, err)
	}
	inside, err := PreviewImport(context.Background(), root, library, ImportOptions{Session: 1, Name: "Friday", Parent: " " + strconv.FormatInt(parent, 10) + " ", DryRun: true})
	if err != nil || inside.ParentID != parent || inside.Name != "Friday" {
		t.Fatalf("nested plan = %+v, %v", inside, err)
	}
	if _, err := PreviewImport(context.Background(), root, library, ImportOptions{Session: 0}); code(err) != "session_not_found" {
		t.Fatalf("invalid session err = %v", err)
	}
}

func TestImportResolvesOpaqueParentReferences(t *testing.T) {
	root, opts, _ := importFixture(t)
	library := openLibrary(t, root)
	parent, err := db.CreatePlaylist(context.Background(), library, "Sets", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Register(context.Background(), library, root, false); err != nil {
		t.Fatal(err)
	}
	entities, err := identity.List(context.Background(), library, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entities {
		if e.Kind == "playlist" && e.ID == parent {
			opts.Parent = e.Ref
		}
	}
	plan, err := PreviewImport(context.Background(), root, library, *opts)
	if err != nil || plan.ParentID != parent {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
}
