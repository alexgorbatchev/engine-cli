package track

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// fileTagsLookupRunner tags into the workspace and reports file tags that differ from the curated Engine row.
func fileTagsLookupRunner(t *testing.T, wantLookup bool, extracted *bool) func(context.Context, string, ...string) ([]byte, error) {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "tag-track":
			if slices.Contains(args, "--no-lookup") == wantLookup {
				t.Fatalf("lookup=%v args=%q", wantLookup, args)
			}
			i := slices.Index(args, "--out-dir")
			output := filepath.Join(args[i+1], "original.mp3")
			if err := os.WriteFile(output, []byte("tagged"), 0600); err != nil {
				t.Fatal(err)
			}
			return json.Marshal(acquire.TagResult{Status: "success", Results: []acquire.TagFile{{Status: "success", OutputFile: output}}})
		case "ffmpeg":
			if slices.Contains(args, "image2pipe") || slices.Contains(args, "-vf") {
				*extracted = true
				return fileTagsJPEG(t, args), nil
			}
			return []byte("SHA256=same"), nil
		case "ffprobe":
			return []byte(`{"format":{"duration":"180","bit_rate":"320000","tags":{"title":"New","artist":"File Artist","album":"File Album","genre":"File Genre","comment":"File comment","label":"File label","composer":"File composer","remixer":"File remixer","date":"2024-05-10"}},"streams":[{"index":0,"codec_type":"audio","codec_name":"mp3"},{"index":1,"codec_type":"video","disposition":{"attached_pic":1}}]}`), nil
		}
		t.Fatalf("unexpected command %s", name)
		return nil, nil
	}
}

func TestUpdateFileTagsWithoutLookupChangesOnlyEditedColumns(t *testing.T) {
	d, lib, _ := createTestDB(t)
	if err := os.WriteFile(filepath.Join(lib, "original.mp3"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO AlbumArt(id,hash,albumArt) VALUES(7,'curated',x'01');
		INSERT INTO Track(id,title,artist,album,genre,comment,label,composer,remixer,year,path,albumArtId,albumArt)
		VALUES(1,'Old','Engine Artist','Engine Album','Engine Genre','Engine comment','Engine label','Engine composer','Engine remixer',2015,'original.mp3',7,'image://planck/7')`); err != nil {
		t.Fatal(err)
	}
	extracted := false
	result, err := UpdateFileTags(context.Background(), d, lib, "1", FileTagOptions{Options: acquire.TagOptions{Title: new("New")}, Runner: fileTagsLookupRunner(t, false, &extracted)})
	if err != nil {
		t.Fatal(err)
	}
	tagged, err := db.GetTrackByID(context.Background(), d, result.TrackID)
	if err != nil {
		t.Fatal(err)
	}
	if tagged.Title.String != "New" || tagged.Artist.String != "Engine Artist" || tagged.Album.String != "Engine Album" || tagged.Genre.String != "Engine Genre" ||
		tagged.Comment.String != "Engine comment" || tagged.Label.String != "Engine label" || tagged.Composer.String != "Engine composer" ||
		tagged.Remixer.String != "Engine remixer" || tagged.Year.Int64 != 2015 || tagged.AlbumArtID.Int64 != 7 || result.Path != tagged.PathString() {
		t.Fatalf("untouched Engine metadata changed: result=%+v track=%+v", result, tagged)
	}
	if extracted {
		t.Fatal("untouched artwork was re-imported from the file")
	}

	extracted = false
	result, err = UpdateFileTags(context.Background(), d, lib, "1", FileTagOptions{Options: acquire.TagOptions{Date: new("2024-05-10"), Genre: new("File Genre"), Comment: new("")}, Runner: fileTagsLookupRunner(t, false, &extracted)})
	if err != nil {
		t.Fatal(err)
	}
	tagged, err = db.GetTrackByID(context.Background(), d, result.TrackID)
	if err != nil {
		t.Fatal(err)
	}
	if tagged.Year.Int64 != 2024 || tagged.Genre.String != "File Genre" || tagged.Comment.String != "File comment" || tagged.Title.String != "New" ||
		tagged.Artist.String != "Engine Artist" || tagged.Label.String != "Engine label" || tagged.AlbumArtID.Int64 != 7 || extracted {
		t.Fatalf("edited columns not reconciled from the file: track=%+v extracted=%v", tagged, extracted)
	}
}

func TestUpdateFileTagsArtworkAndLookupReconcileFromTheFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options acquire.TagOptions
		lookup  bool
		synced  bool
	}{
		{"explicit artwork without lookup", acquire.TagOptions{Artwork: "/covers/new.jpg"}, false, false},
		{"online lookup", acquire.TagOptions{Lookup: acquire.LookupReplace, Title: new("New")}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, lib, _ := createTestDB(t)
			if err := os.WriteFile(filepath.Join(lib, "original.mp3"), []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`INSERT INTO AlbumArt(id,hash,albumArt) VALUES(7,'curated',x'01');
				INSERT INTO Track(id,title,artist,label,year,path,albumArtId,albumArt) VALUES(1,'Old','Engine Artist','Engine label',2015,'original.mp3',7,'image://planck/7')`); err != nil {
				t.Fatal(err)
			}
			extracted := false
			result, err := UpdateFileTags(context.Background(), d, lib, "1", FileTagOptions{Options: tc.options, Runner: fileTagsLookupRunner(t, tc.lookup, &extracted)})
			if err != nil {
				t.Fatal(err)
			}
			tagged, err := db.GetTrackByID(context.Background(), d, result.TrackID)
			if err != nil {
				t.Fatal(err)
			}
			if !extracted || tagged.AlbumArtID.Int64 == 7 {
				t.Fatalf("file artwork not stored: track=%+v extracted=%v", tagged, extracted)
			}
			if synced := tagged.Artist.String == "File Artist" && tagged.Label.String == "File label" && tagged.Year.Int64 == 2024; synced != tc.synced {
				t.Fatalf("metadata reconciliation=%v want %v: %+v", synced, tc.synced, tagged)
			}
		})
	}
}
