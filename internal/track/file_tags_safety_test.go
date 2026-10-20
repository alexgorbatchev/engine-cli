package track

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/acquire"
)

func TestFileTagsReconcilesClearedDateAndRemainingMetadata(t *testing.T) {
	d, lib, _ := createTestDB(t)
	original := filepath.Join(lib, "original.mp3")
	if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO Track(id,title,path,year,label,composer,remixer) VALUES(1,'Old','original.mp3',2000,'Old label','Old composer','Old remixer')`); err != nil {
		t.Fatal(err)
	}
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "tag-track":
			for i, arg := range args {
				if arg == "--out-dir" {
					path := filepath.Join(args[i+1], "tagged.mp3")
					if err := os.WriteFile(path, []byte("tagged"), 0600); err != nil {
						t.Fatal(err)
					}
					return json.Marshal(acquire.TagResult{Status: "success", Results: []acquire.TagFile{{Status: "success", OutputFile: path}}})
				}
			}
		case "ffmpeg":
			return []byte("SHA256=same"), nil
		case "ffprobe":
			return []byte(`{"format":{"tags":{"title":"New","label":"Actual label","composer":"Actual composer","remixer":"Actual remixer"}},"streams":[{"codec_type":"audio","codec_name":"mp3"}]}`), nil
		}
		t.Fatalf("unexpected %s", name)
		return nil, nil
	}
	result, err := UpdateFileTags(context.Background(), d, lib, "1", FileTagOptions{Options: acquire.TagOptions{Date: new("")}, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if result.Track.Year.Int64 != 0 || result.Track.Label.String != "Actual label" || result.Track.Composer.String != "Actual composer" || result.Track.Remixer.String != "Actual remixer" {
		t.Fatalf("stale database metadata: %+v", result.Track)
	}
}

func TestFileTagsFailuresRetainOriginalAndDatabase(t *testing.T) {
	for _, scenario := range []string{"missing track", "closed database", "missing audio", "workspace", "tag error", "missing output", "outside workspace", "hash failure", "probe failure", "unsupported codec", "artwork failure", "changed database", "write failure", "closed before commit", "dry run", "artwork only", "artwork success", "artwork store failure"} {
		t.Run(scenario, func(t *testing.T) {
			d, lib, _ := createTestDB(t)
			original := filepath.Join(lib, "original.mp3")
			if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`INSERT INTO Track(id,title,path,year) VALUES(1,'Old','original.mp3',2000)`); err != nil {
				t.Fatal(err)
			}
			identifier := "1"
			switch scenario {
			case "missing track":
				identifier = "999"
			case "closed database":
				_ = d.Close()
			case "missing audio":
				_ = os.Remove(original)
			case "workspace":
				t.Setenv("TMPDIR", filepath.Join(lib, "missing"))
			case "write failure":
				if _, err := d.Exec(`CREATE TRIGGER reject_tag BEFORE UPDATE ON Track BEGIN SELECT RAISE(FAIL,'fixture failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "artwork store failure":
				if _, err := d.Exec(`DROP TABLE AlbumArt`); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
				calls++
				switch name {
				case "tag-track":
					if scenario == "tag error" {
						return nil, errors.New("fixture tag failure")
					}
					var work string
					for i, arg := range args {
						if arg == "--out-dir" {
							work = args[i+1]
						}
					}
					path := filepath.Join(work, "tagged.mp3")
					if scenario == "outside workspace" {
						path = original
					}
					if scenario != "missing output" && scenario != "outside workspace" {
						if err := os.WriteFile(path, []byte("tagged"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					return json.Marshal(acquire.TagResult{Status: "success", Results: []acquire.TagFile{{Status: "success", OutputFile: path, DryRun: scenario == "dry run"}}})
				case "ffmpeg":
					if scenario == "hash failure" || scenario == "artwork failure" && strings.Contains(strings.Join(args, " "), "image2pipe") {
						return nil, errors.New("fixture ffmpeg failure")
					}
					if strings.Contains(strings.Join(args, " "), "image2pipe") {
						return fileTagsJPEG(t, args), nil
					}
					return []byte("SHA256=same"), nil
				case "ffprobe":
					if scenario == "probe failure" {
						return nil, errors.New("fixture probe failure")
					}
					if scenario == "changed database" {
						if _, err := d.Exec(`UPDATE Track SET lastEditTime='concurrent' WHERE id=1`); err != nil {
							t.Fatal(err)
						}
					}
					if scenario == "closed before commit" {
						_ = d.Close()
					}
					if scenario == "unsupported codec" {
						return []byte(`{"streams":[{"codec_type":"audio","codec_name":"opus"}]}`), nil
					}
					if strings.HasPrefix(scenario, "artwork ") && scenario != "artwork only" {
						return []byte(`{"streams":[{"codec_type":"audio","codec_name":"mp3"},{"codec_type":"video","index":1,"disposition":{"attached_pic":1}}]}`), nil
					}
					return []byte(`{"format":{"tags":{"title":"Actual"}},"streams":[{"codec_type":"audio","codec_name":"mp3"}]}`), nil
				}
				t.Fatalf("unexpected command %s", name)
				return nil, nil
			}
			opts := FileTagOptions{Runner: runner, Options: acquire.TagOptions{DryRun: scenario == "dry run"}, ArtworkOnly: strings.HasPrefix(scenario, "artwork ")}
			if opts.ArtworkOnly {
				opts.Options.Artwork = "cover.jpg"
			}
			result, err := UpdateFileTags(context.Background(), d, lib, identifier, opts)
			wantSuccess := scenario == "dry run" || scenario == "artwork only" || scenario == "artwork success"
			if (err == nil) != wantSuccess {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if scenario == "dry run" && calls != 1 {
				t.Fatalf("dry run inspected or installed audio: %d calls", calls)
			}
			if scenario == "artwork success" && (!result.Track.AlbumArtID.Valid || result.Track.AlbumArtSourceHash.String == "") {
				t.Fatalf("artwork not reconciled: %+v", result.Track)
			}
			if scenario != "missing audio" {
				data, err := os.ReadFile(original)
				if err != nil || string(data) != "original" {
					t.Fatalf("original altered: %q %v", data, err)
				}
			}
			if scenario != "closed database" && scenario != "closed before commit" {
				var title, path string
				if err := d.QueryRow(`SELECT title,path FROM Track WHERE id=1`).Scan(&title, &path); err != nil {
					t.Fatal(err)
				}
				if title != "Old" {
					t.Fatalf("curated title changed: %q", title)
				}
				if scenario != "artwork only" && scenario != "artwork success" && path != "original.mp3" {
					t.Fatalf("failed/preview operation changed path: %q", path)
				}
			}
		})
	}
}

func TestAudioPreservationRejectsMissingHashesAndRunnerFailures(t *testing.T) {
	for _, output := range []string{"", "not a hash", "SHA256="} {
		err := VerifyAudioPreserved(context.Background(), func(context.Context, string, ...string) ([]byte, error) { return []byte(output), nil }, "original", "tagged")
		if err == nil {
			t.Fatalf("accepted invalid hash %q", output)
		}
	}
	if err := VerifyAudioPreserved(context.Background(), func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("fixture error") }, "original", "tagged"); err == nil {
		t.Fatal("ignored ffmpeg failure")
	}
}

func TestTaggedArtworkReadsAndPreparesAttachedImage(t *testing.T) {
	var probe upgradeProbe
	if err := json.Unmarshal([]byte(`{"streams":[{"index":2,"codec_type":"video","disposition":{"attached_pic":1}}]}`), &probe); err != nil {
		t.Fatal(err)
	}
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return fileTagsJPEG(t, args), nil
	}
	prepared, err := taggedArtwork(context.Background(), runner, "tagged.mp3", &probe)
	if err != nil || prepared == nil || len(prepared.Image) == 0 || len(prepared.Thumbnail) == 0 {
		t.Fatalf("prepared=%v error=%v", prepared, err)
	}
}

func fileTagsJPEG(t *testing.T, args []string) []byte {
	t.Helper()
	size := 16
	for _, arg := range args {
		if strings.Contains(arg, "scale=1400:1400") {
			size = 1400
		}
		if strings.Contains(arg, "scale=256:256") {
			size = 256
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, image.NewRGBA(image.Rect(0, 0, size, size)), nil); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
