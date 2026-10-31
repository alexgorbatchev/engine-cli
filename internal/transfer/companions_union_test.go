package transfer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestSyncSelectedColorsAndImportEvidenceTogether(t *testing.T) {
	source, s := library(t, "source")
	destination, _ := library(t, "destination")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3'),('Two','two.mp3','two.mp3');
INSERT INTO Playlist(title,parentListId,nextListId,isPersisted) VALUES('Set',0,0,1);
INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,'source',0)`)
	for _, name := range []string{"one.mp3", "two.mp3"} {
		put(t, filepath.Join(source, name), name)
	}
	seedTransferImports(t, source)
	want := colors.State{Assignments: []colors.Assignment{{OriginUUID: "source", OriginTrackID: 1, Color: "#112233"}, {OriginUUID: "source", OriginTrackID: 2, Color: "#445566"}}}
	if _, err := colors.Replace(t.Context(), source, transferColors(t, source).Revision(), want); err != nil {
		t.Fatal(err)
	}
	opts := Options{Source: source, Destination: destination, Scope: Scope{PlaylistIDs: []int64{1}}, NoBackup: true, DryRun: true}
	preview, err := Run(t.Context(), opts)
	if err != nil || !preview.Applicable {
		t.Fatalf("preview=%+v error=%v", preview, err)
	}
	opts.DryRun, opts.PlanToken = false, preview.PlanToken
	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	assigned := transferColors(t, destination)
	archived := transferImports(t, destination)
	if len(assigned.Assignments) != 1 || assigned.Assignments[0] != want.Assignments[0] || len(archived.Documents) != 1 || len(archived.Tracks) != 1 || archived.Tracks[0].SourceID != "track:0" || archived.Tracks[0].OriginUUID != assigned.Assignments[0].OriginUUID || archived.Tracks[0].OriginTrackID != assigned.Assignments[0].OriginTrackID {
		t.Fatalf("selected companions diverged: colors=%+v imports=%+v", assigned, archived)
	}
	if _, err := history.Undo(t.Context(), destination, result.OperationID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{companion.ColorsPath(destination), companion.ImportsPath(destination), companion.TagsPath(destination)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("Undo did not restore independent absence at %s: %v", path, err)
		}
	}
}
