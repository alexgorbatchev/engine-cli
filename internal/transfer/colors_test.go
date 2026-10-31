package transfer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func transferColors(t *testing.T, library string) colors.State {
	t.Helper()
	state, err := colors.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestSyncColorsScopeClearAndUndo(t *testing.T) {
	for _, root := range []bool{false, true} {
		t.Run(map[bool]string{false: "playlist", true: "root"}[root], func(t *testing.T) {
			source, s := library(t, "source")
			destination, _ := library(t, "destination")
			execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3'),('Two','two.mp3','two.mp3'); INSERT INTO Playlist(title,parentListId,nextListId,isPersisted) VALUES('Set',0,0,1); INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,'source',0)`)
			for _, name := range []string{"one.mp3", "two.mp3"} {
				put(t, filepath.Join(source, name), name)
			}
			before := colors.State{Assignments: []colors.Assignment{{OriginUUID: "source", OriginTrackID: 1, Color: "#112233"}, {OriginUUID: "source", OriginTrackID: 2, Color: "#445566"}}}
			if _, err := colors.Replace(t.Context(), source, transferColors(t, source).Revision(), before); err != nil {
				t.Fatal(err)
			}
			o := Options{Source: source, Destination: destination, Scope: Scope{PlaylistIDs: []int64{1}}, DryRun: true, NoBackup: true}
			wantCount := 1
			if root {
				o.Scope, wantCount = Scope{Root: true}, 2
			}
			preview, err := Run(t.Context(), o)
			if err != nil || !preview.Applicable {
				t.Fatalf("preview=%+v error=%v", preview, err)
			}
			if _, err := os.Stat(companion.ColorsPath(destination)); !os.IsNotExist(err) {
				t.Fatalf("preview enrolled colors: %v", err)
			}
			o.DryRun, o.PlanToken = false, preview.PlanToken
			applied, err := Run(t.Context(), o)
			if err != nil {
				t.Fatal(err)
			}
			state := transferColors(t, destination)
			if len(state.Assignments) != wantCount || state.Assignments[0] != before.Assignments[0] {
				t.Fatalf("scoped colors=%+v", state)
			}
			if _, err := os.Stat(companion.TagsPath(destination)); !os.IsNotExist(err) {
				t.Fatalf("color-only sync enrolled taxonomy: %v", err)
			}
			if _, err := history.Undo(t.Context(), destination, applied.OperationID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(companion.ColorsPath(destination)); !os.IsNotExist(err) {
				t.Fatalf("undo retained new companion: %v", err)
			}
			o.PlanToken = ""
			if _, err := Run(t.Context(), o); err != nil {
				t.Fatal(err)
			}
			if _, err := colors.Assign(t.Context(), s, source, colors.Edit{Track: "1", Color: "", Revision: before.Revision()}); err != nil {
				t.Fatal(err)
			}
			o.DryRun = true
			clearPreview, err := Run(t.Context(), o)
			if err != nil || !clearPreview.Applicable {
				t.Fatalf("clear preview=%+v error=%v", clearPreview, err)
			}
			o.DryRun, o.PlanToken = false, clearPreview.PlanToken
			cleared, err := Run(t.Context(), o)
			if err != nil {
				t.Fatal(err)
			}
			if state := transferColors(t, destination); len(state.Assignments) != wantCount-1 {
				t.Fatalf("clear not transferred: %+v", state)
			}
			if _, err := history.Undo(t.Context(), destination, cleared.OperationID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			if state := transferColors(t, destination); len(state.Assignments) != wantCount || state.Assignments[0] != before.Assignments[0] {
				t.Fatalf("undo did not restore colors: %+v", state)
			}
		})
	}
}

func TestSyncColorRevisionInvalidatesPlan(t *testing.T) {
	source, s := library(t, "source")
	destination, _ := library(t, "destination")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3')`)
	put(t, filepath.Join(source, "one.mp3"), "audio")
	o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, DryRun: true, NoBackup: true}
	preview, err := Run(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := colors.Assign(t.Context(), s, source, colors.Edit{Track: "1", Color: "#112233", Revision: transferColors(t, source).Revision()}); err != nil {
		t.Fatal(err)
	}
	o.DryRun, o.PlanToken = false, preview.PlanToken
	if _, err := Run(t.Context(), o); err == nil {
		t.Fatal("accepted preview after source color changed")
	}
	if _, err := os.Stat(companion.ColorsPath(destination)); !os.IsNotExist(err) {
		t.Fatalf("stale plan enrolled destination colors: %v", err)
	}
}

func TestSyncColorPublicationFailureRetainsRecovery(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3')`)
	put(t, filepath.Join(source, "one.mp3"), "audio")
	if _, err := colors.Assign(t.Context(), s, source, colors.Edit{Track: "1", Color: "#112233", Revision: transferColors(t, source).Revision()}); err != nil {
		t.Fatal(err)
	}
	// Keep a valid empty enrolled destination, then reject native publication.
	stored, err := colors.Replace(t.Context(), destination, transferColors(t, destination).Revision(), colors.State{Assignments: []colors.Assignment{{OriginUUID: "source", OriginTrackID: 1, Color: "#445566"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := colors.Replace(t.Context(), destination, stored.Revision(), colors.State{}); err != nil {
		t.Fatal(err)
	}
	c, err := db.Open(companion.ColorsPath(destination))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	execSQL(t, c, `CREATE TRIGGER deny_color BEFORE INSERT ON TrackColor BEGIN SELECT RAISE(ABORT,'publication denied'); END`)
	_, err = Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, NoBackup: true})
	if progress.ErrorCode(err) != "history_recovery_required" {
		t.Fatalf("publication failure lost pending recovery: %v", err)
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM Track`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("did not reach Engine commit: count=%d error=%v", count, err)
	}
	entries, err := history.List(destination)
	if err != nil || len(entries) != 1 || entries[0].After != nil {
		t.Fatalf("pending entries=%+v error=%v", entries, err)
	}
	if _, err := history.Undo(t.Context(), destination, entries[0].ID, history.UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM Track`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("recovery left Engine commit: count=%d error=%v", count, err)
	}
}
