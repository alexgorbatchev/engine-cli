package rekordbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

func TestObserveDestinationReadsNativeAndCompanionWithoutEnrollment(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if _, err := db.CreateLibrary(ctx, root); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///a.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.InsertTrack(ctx, d, &db.Track{Path: text("/moved/a.wav")})
	if err != nil {
		t.Fatal(err)
	}
	request := destinationRequest{library: root, name: "Reviewed root", source: src}
	got, err := observeDestination(ctx, d, request)
	if err != nil || got.uuid == "" || len(got.tracks) != 1 || got.tracks[0].id != id || got.review.complete || len(got.archive.Documents) != 0 {
		t.Fatalf("wrong native/passive observation: %+v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".engine-cli", "imports.db")); !os.IsNotExist(err) {
		t.Fatalf("read enrolled companion: %v", err)
	}
	state := importsource.State{Documents: []importsource.Document{src.document}, Tracks: []importsource.Track{archivedMapping(t, d, src.document.ID, "id:1", id)}}
	if err := importsource.Replace(ctx, root, got.archive.Revision(), state); err != nil {
		t.Fatal(err)
	}
	got, err = observeDestination(ctx, d, request)
	if err != nil || !got.review.complete || got.review.tracks["id:1"].path != "/moved/a.wav" || len(got.archive.Tracks) != 1 {
		t.Fatalf("archived origin/retained path lost: %+v, %v", got, err)
	}
	if _, err := db.CreatePlaylist(ctx, d, "Reviewed root", 0, true); err != nil {
		t.Fatal(err)
	}
	got, err = observeDestination(ctx, d, request)
	if err == nil || got.uuid != "" || got.tracks != nil {
		t.Fatalf("root collision returned partial observation: %+v, %v", got, err)
	}
}
