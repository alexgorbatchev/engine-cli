package track

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// artworkImportRunner answers ffprobe with an attached cover for every file
// except those named in bare, and ffmpeg with square JPEGs of the size asked.
func artworkImportRunner(t *testing.T, bare ...string) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			path := args[len(args)-1]
			for _, file := range bare {
				if strings.HasSuffix(path, file) {
					return []byte(`{"streams":[{"index":0,"codec_type":"audio","codec_name":"mp3"}]}`), nil
				}
			}
			return []byte(`{"streams":[{"index":0,"codec_type":"audio","codec_name":"mp3"},{"index":1,"codec_type":"video","disposition":{"attached_pic":1}}]}`), nil
		}
		return fileTagsJPEG(t, args), nil
	}
}

func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func TestArtworkImportReviewsAndSetsLibraryCoversWithoutTouchingAudio(t *testing.T) {
	d, lib, _ := createTestDB(t)
	ctx := context.Background()
	for _, name := range []string{"covered.mp3", "replaced.mp3", "bare.mp3"} {
		if err := os.WriteFile(filepath.Join(lib, name), []byte("audio "+name), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(`INSERT INTO Track(id,title,path) VALUES(1,'Covered','covered.mp3'),(2,'Replaced','replaced.mp3'),(3,'Bare','bare.mp3'),(4,'Gone','gone.mp3')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE Track SET albumArtId=1, albumArtSourceHash='other' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	before := fileHash(t, filepath.Join(lib, "covered.mp3"))
	opts := ArtworkImportOptions{Library: lib, Runner: artworkImportRunner(t, "bare.mp3")}
	identifiers := []string{"1", "2", "3", "4"}
	plan, err := PreviewArtworkImport(ctx, d, identifiers, opts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changed != 2 || plan.Replaced != 1 || !plan.CanApply || plan.PlanToken == "" {
		t.Fatalf("plan %+v", plan)
	}
	if !plan.Tracks[1].Replaces || plan.Tracks[0].Replaces || plan.Tracks[2].ErrorCode != "file_no_artwork" || plan.Tracks[3].ErrorCode != "file_not_found" {
		t.Fatalf("tracks %+v", plan.Tracks)
	}
	// A review changes nothing.
	var hash any
	if err := d.QueryRow("SELECT albumArtSourceHash FROM Track WHERE id=1").Scan(&hash); err != nil || hash != nil {
		t.Fatalf("review wrote a cover: %v %v", hash, err)
	}
	if err := CheckArtworkImport(ctx, d, identifiers, "", opts); err == nil {
		t.Fatal("accepted an import without a token")
	}
	if err := CheckArtworkImport(ctx, d, identifiers, "stale", opts); progress.ErrorCode(err) != "artwork_import_plan_stale" {
		t.Fatalf("stale token: %v", err)
	}
	result, err := ApplyArtworkImport(ctx, d, identifiers, plan.PlanToken, opts)
	if err != nil || result.Changed != 2 || len(result.TrackIDs) != 2 {
		t.Fatalf("result %+v %v", result, err)
	}
	var art, source string
	if err := d.QueryRow("SELECT albumArt, albumArtSourceHash FROM Track WHERE id=1").Scan(&art, &source); err != nil || !strings.HasPrefix(art, "image://planck/") || source == "" {
		t.Fatalf("cover not set: %q %q %v", art, source, err)
	}
	if after := fileHash(t, filepath.Join(lib, "covered.mp3")); !bytes.Equal(before[:], after[:]) {
		t.Fatal("importing a cover changed the audio file")
	}
	// The library now shows the file's cover, so the same import changes nothing.
	again, err := PreviewArtworkImport(ctx, d, identifiers, opts)
	if err != nil || again.CanApply || !again.Tracks[0].Unchanged {
		t.Fatalf("second review %+v %v", again, err)
	}
	if _, err := ApplyArtworkImport(ctx, d, identifiers, again.PlanToken, opts); progress.ErrorCode(err) != "artwork_import_unchanged" {
		t.Fatalf("unchanged import: %v", err)
	}
	// The old review no longer matches the library.
	if err := CheckArtworkImport(ctx, d, identifiers, plan.PlanToken, opts); progress.ErrorCode(err) != "artwork_import_plan_stale" {
		t.Fatalf("stale after import: %v", err)
	}
}

func TestArtworkImportValidatesIdentifiers(t *testing.T) {
	many := make([]string, MaxArtworkImports+1)
	for i := range many {
		many[i] = string(rune('a'+i%26)) + strings.Repeat("x", i)
	}
	for name, identifiers := range map[string][]string{
		"none":     nil,
		"empty":    {" "},
		"repeated": {"1", "1"},
		"too many": many,
	} {
		if err := ValidateArtworkImport(identifiers); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if code := progress.ErrorCode(ValidateArtworkImport(many)); code != "batch_limit_exceeded" {
		t.Fatalf("limit code %q", code)
	}
}
