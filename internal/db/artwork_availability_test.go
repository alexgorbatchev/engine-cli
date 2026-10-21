package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/artworkcache"
)

func TestArtworkAvailability(t *testing.T) {
	ctx := context.Background()
	library := t.TempDir()
	database, err := Open(filepath.Join(library, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := InitSchema(ctx, database); err != nil {
		t.Fatal(err)
	}
	hash := []byte("0000000000000000000000000000000000000000")
	if _, err := database.Exec("INSERT INTO AlbumArt(id,albumArt,hash) VALUES(1,?,NULL),(2,NULL,?),(3,NULL,NULL)", []byte("inline"), hash); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(library, "Artwork"), 0700); err != nil {
		t.Fatal(err)
	}
	paths := artworkcache.Paths(library, hash)
	if err := os.WriteFile(paths[1], []byte("cached"), 0600); err != nil {
		t.Fatal(err)
	}
	tracks := []Track{
		{AlbumArtID: sql.NullInt64{Int64: 517, Valid: true}},
		{AlbumArtID: sql.NullInt64{Int64: 1, Valid: true}},
		{AlbumArtID: sql.NullInt64{Int64: 2, Valid: true}},
		{AlbumArtID: sql.NullInt64{Int64: 2, Valid: true}},
		{AlbumArtSourceHash: sql.NullString{String: string(hash), Valid: true}},
		{AlbumArtID: sql.NullInt64{Int64: 3, Valid: true}},
	}
	populateArtworkAvailability(ctx, database, tracks)
	for index, want := range []bool{false, true, true, true, true, false} {
		if got := tracks[index].HasArtwork; !got.Valid || got.Bool != want {
			t.Fatalf("track %d artwork=%v want=%v", index, got, want)
		}
	}
	if err := os.Remove(paths[1]); err != nil {
		t.Fatal(err)
	}
	populateArtworkAvailability(ctx, database, tracks)
	if got := tracks[2].HasArtwork; !got.Valid || got.Bool {
		t.Fatalf("missing cached image=%v", got)
	}
	if err := os.Mkdir(paths[0], 0700); err != nil {
		t.Fatal(err)
	}
	populateArtworkAvailability(ctx, database, tracks)
	if tracks[2].HasArtwork.Bool {
		t.Fatal("directory mistaken for artwork")
	}
	// A filesystem error must preserve unknown rather than suppress the request.
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(paths[0]), paths[0]); err != nil {
		t.Fatal(err)
	}
	populateArtworkAvailability(ctx, database, tracks)
	if tracks[2].HasArtwork.Valid {
		t.Fatal("unreadable cache reported as missing")
	}
}

func TestArtworkAvailabilityUnknownCatalogue(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	tracks := []Track{{AlbumArtID: sql.NullInt64{Int64: 1, Valid: true}}}
	populateArtworkAvailability(context.Background(), database, nil)
	populateArtworkAvailability(context.Background(), database, tracks)
	if tracks[0].HasArtwork.Valid {
		t.Fatal("missing catalogue reported as missing artwork")
	}
	memory, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer memory.Close()
	if err := InitSchema(context.Background(), memory); err != nil {
		t.Fatal(err)
	}
	tracks[0].AlbumArtSourceHash = sql.NullString{String: "0000000000000000000000000000000000000000", Valid: true}
	populateArtworkAvailability(context.Background(), memory, tracks)
	if tracks[0].HasArtwork.Valid {
		t.Fatal("unknown library path reported as missing artwork")
	}
}

func TestArtworkAvailabilityPreservesTrackReadErrors(t *testing.T) {
	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := InitSchema(ctx, database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO Track(id,path) VALUES(1,'song.mp3'); INSERT INTO Playlist(id,title) VALUES(1,'Set')"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddTrackToPlaylist(ctx, database, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE Track SET year='damaged value' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetAllTracks(ctx, database); err == nil {
		t.Fatal("collection read accepted damaged metadata")
	}
	if _, err := GetTrackByID(ctx, database, 1); err == nil {
		t.Fatal("single track read accepted damaged metadata")
	}
	if _, err := GetPlaylistTracks(ctx, database, 1); err == nil {
		t.Fatal("playlist read accepted damaged metadata")
	}
	if _, err := FindTrack(ctx, database, "song.mp3"); err == nil {
		t.Fatal("path read accepted damaged metadata")
	}
}
