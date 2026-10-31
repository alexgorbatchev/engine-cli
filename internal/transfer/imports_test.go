package transfer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func transferImports(t *testing.T, path string) importsource.State {
	t.Helper()
	s, err := importsource.Read(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSyncImportMappingReusesActualDestinationOrigin(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3'),('Two','two.mp3','two.mp3')`)
	execSQL(t, d, `INSERT INTO Track(title,path,filename) VALUES('Existing','existing.mp3','existing.mp3')`)
	put(t, filepath.Join(source, "one.mp3"), "same audio")
	put(t, filepath.Join(source, "two.mp3"), "other audio")
	put(t, filepath.Join(destination, "existing.mp3"), "same audio")
	seedTransferImports(t, source)
	opts := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, NoBackup: true}
	result, err := Run(t.Context(), opts)
	if err != nil || result.Summary.TracksReused != 1 {
		t.Fatalf("reuse=%+v error=%v", result, err)
	}
	state := transferImports(t, destination)
	if len(state.Tracks) != 2 || state.Tracks[0].SourceID != "track:0" || state.Tracks[0].OriginUUID != "destination" || state.Tracks[0].OriginTrackID != 1 {
		t.Fatalf("mapping retained source origin instead of actual reused track: %+v", state)
	}
	var title string
	if err := d.QueryRow("SELECT title FROM Track WHERE id=1").Scan(&title); err != nil || title != "Existing" {
		t.Fatalf("reuse replaced destination metadata: title=%q error=%v", title, err)
	}
	unchanged, err := Run(t.Context(), opts)
	if err != nil || unchanged.OperationID != "" {
		t.Fatalf("unchanged evidence created another history entry: %+v error=%v", unchanged, err)
	}
}

func TestSyncImportEvidenceRetainedByMirrorAndRemovedTrack(t *testing.T) {
	source, _ := library(t, "source")
	destination, d := library(t, "source-archive")
	execSQL(t, d, `INSERT INTO Track(title,path,filename,originDatabaseUuid,originTrackId) VALUES('One','one.mp3','one.mp3','source',1),('Two','two.mp3','two.mp3','source',2)`)
	put(t, filepath.Join(destination, "one.mp3"), "one")
	put(t, filepath.Join(destination, "two.mp3"), "two")
	before := seedTransferImports(t, destination)
	execSQL(t, d, "DELETE FROM Track WHERE id=2")
	result, err := Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, Mirror: true, NoBackup: true})
	if err != nil {
		t.Fatal(err)
	}
	if after := transferImports(t, destination); after.Revision() != before.Revision() {
		t.Fatalf("mirror destroyed historical import evidence: %+v", after)
	}
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM Track").Scan(&count); err != nil || count != 0 || result.Summary.TracksRemoved != 1 {
		t.Fatalf("mirror did not actually remove extant track: count=%d result=%+v error=%v", count, result, err)
	}
}

func TestSyncImportRevisionAndMappingConflict(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3'),('Two','two.mp3','two.mp3')`)
	put(t, filepath.Join(source, "one.mp3"), "one")
	put(t, filepath.Join(source, "two.mp3"), "two")
	before := seedTransferImports(t, source)
	opts := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, DryRun: true, NoBackup: true}
	preview, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	changed := before
	changed.Tracks = append([]importsource.Track{}, before.Tracks[:1]...)
	if err := importsource.Replace(t.Context(), source, before.Revision(), changed); err != nil {
		t.Fatal(err)
	}
	opts.DryRun, opts.PlanToken = false, preview.PlanToken
	if _, err := Run(t.Context(), opts); err == nil {
		t.Fatal("accepted stale import-evidence plan")
	}
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM Track").Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale plan changed Engine: count=%d error=%v", count, err)
	}
	changed.Tracks[0].OriginUUID = "unavailable-origin"
	if err := importsource.Replace(t.Context(), destination, transferImports(t, destination).Revision(), changed); err != nil {
		t.Fatal(err)
	}
	opts.DryRun, opts.PlanToken, opts.Conflict = true, "", "source"
	conflict, err := Run(t.Context(), opts)
	if err != nil || conflict.Applicable {
		t.Fatalf("source policy silently repointed historical selector: %+v error=%v", conflict, err)
	}
	found := false
	for _, item := range conflict.Conflicts {
		found = found || item.Code == "sync_import_mapping_conflict"
	}
	if !found {
		t.Fatalf("missing import mapping conflict: %+v", conflict.Conflicts)
	}
}

func TestSyncImportPublicationFailureRetainsRecovery(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3'),('Two','two.mp3','two.mp3')`)
	put(t, filepath.Join(source, "one.mp3"), "one")
	put(t, filepath.Join(source, "two.mp3"), "two")
	seedTransferImports(t, source)
	xml := []byte(`<DJ_PLAYLISTS Name="previous"/>`)
	previous := importsource.State{Documents: []importsource.Document{{ID: importsource.DocumentID(xml), XML: xml}}, Tracks: []importsource.Track{}}
	if err := importsource.Replace(t.Context(), destination, transferImports(t, destination).Revision(), previous); err != nil {
		t.Fatal(err)
	}
	previous = transferImports(t, destination)
	archive, err := db.Open(companion.ImportsPath(destination))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	execSQL(t, archive, "CREATE TRIGGER reject_import_publication BEFORE INSERT ON Document BEGIN SELECT RAISE(ABORT,'publication refused'); END")
	_, err = Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, NoBackup: true})
	if progress.ErrorCode(err) != "history_recovery_required" {
		t.Fatalf("failed publication lost pending recovery: %v", err)
	}
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM Track").Scan(&count); err != nil || count != 2 {
		t.Fatalf("test missed committed Engine half: count=%d error=%v", count, err)
	}
	if got := transferImports(t, destination); got.Revision() != previous.Revision() {
		t.Fatal("failed SQLite publication changed prior archive")
	}
	entries, err := history.List(destination)
	if err != nil || len(entries) != 1 || entries[0].After != nil {
		t.Fatalf("pending history=%+v error=%v", entries, err)
	}
	if _, err := history.Undo(t.Context(), destination, entries[0].ID, history.UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow("SELECT COUNT(*) FROM Track").Scan(&count); err != nil || count != 0 {
		t.Fatalf("recovery left Engine half: count=%d error=%v", count, err)
	}
}

func seedTransferImports(t *testing.T, path string) importsource.State {
	t.Helper()
	xml := []byte("<?xml version=\"1.0\"?>\n<DJ_PLAYLISTS Unknown=\"preserved\"><COLLECTION/></DJ_PLAYLISTS>\n")
	id := importsource.DocumentID(xml)
	s := importsource.State{Documents: []importsource.Document{{ID: id, XML: xml}}, Tracks: []importsource.Track{
		{DocumentID: id, SourceID: "track:0", OriginUUID: "source", OriginTrackID: 1},
		{DocumentID: id, SourceID: "track:9", OriginUUID: "source", OriginTrackID: 2},
	}}
	if err := importsource.Replace(t.Context(), path, transferImports(t, path).Revision(), s); err != nil {
		t.Fatal(err)
	}
	return transferImports(t, path)
}

func TestSyncImportSourceScopeAndUndo(t *testing.T) {
	for _, root := range []bool{false, true} {
		t.Run(map[bool]string{false: "playlist", true: "root"}[root], func(t *testing.T) {
			source, s := library(t, "source")
			destination, _ := library(t, "destination")
			execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3'),('Two','two.mp3','two.mp3'); INSERT INTO Playlist(title,parentListId,nextListId,isPersisted) VALUES('Set',0,0,1); INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,'source',0)`)
			for _, name := range []string{"one.mp3", "two.mp3"} {
				put(t, filepath.Join(source, name), name)
			}
			before := seedTransferImports(t, source)
			o := Options{Source: source, Destination: destination, Scope: Scope{PlaylistIDs: []int64{1}}, DryRun: true, NoBackup: true}
			if root {
				o.Scope = Scope{Root: true}
			}
			preview, err := Run(t.Context(), o)
			if err != nil || !preview.Applicable {
				t.Fatalf("preview=%+v error=%v", preview, err)
			}
			if _, err := os.Stat(companion.ImportsPath(destination)); !os.IsNotExist(err) {
				t.Fatalf("preview enrolled archive: %v", err)
			}
			o.DryRun, o.PlanToken = false, preview.PlanToken
			applied, err := Run(t.Context(), o)
			if err != nil {
				t.Fatal(err)
			}
			got := transferImports(t, destination)
			wantTracks := 1
			if root {
				wantTracks = 2
			}
			if len(got.Documents) != 1 || !bytes.Equal(got.Documents[0].XML, before.Documents[0].XML) || len(got.Tracks) != wantTracks || got.Tracks[0] != before.Tracks[0] {
				t.Fatalf("lost exact XML or selected native mapping: %+v", got)
			}
			if _, err := history.Undo(t.Context(), destination, applied.OperationID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(companion.ImportsPath(destination)); !os.IsNotExist(err) {
				t.Fatalf("undo retained newly enrolled archive: %v", err)
			}
		})
	}
}
