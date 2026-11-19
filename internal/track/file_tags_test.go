package track

import (
	"context"
	"encoding/json"
	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateFileTags(t *testing.T) {
	for _, changedAudio := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserved audio", true: "reject changed audio"}[changedAudio], func(t *testing.T) {
			d, lib, _ := createTestDB(t)
			original := filepath.Join(lib, "original.mp3")
			if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			// The rewritten file keeps the original's mode, here one the temp file would not get.
			if err := os.Chmod(original, 0640); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`INSERT INTO Track(id,title,artist,path,rating,isAnalyzed) VALUES(1,'Old','Artist','original.mp3',80,1); CREATE TABLE PerformanceData(trackId INTEGER,quickCues BLOB); INSERT INTO PerformanceData VALUES(1,x'1234')`); err != nil {
				t.Fatal(err)
			}
			runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
				switch name {
				case "tag-track":
					var work string
					for i, a := range args {
						if a == "--out-dir" {
							work = args[i+1]
						}
					}
					output := filepath.Join(work, "tagged.mp3")
					if err := os.WriteFile(output, []byte("tagged"), 0600); err != nil {
						t.Fatal(err)
					}
					return json.Marshal(acquire.TagResult{Status: "success", Results: []acquire.TagFile{{Status: "success", OutputFile: output}}})
				case "ffmpeg":
					if changedAudio && args[4] != original {
						return []byte("SHA256=changed"), nil
					}
					return []byte("SHA256=same"), nil
				case "ffprobe":
					return []byte(`{"format":{"duration":"180","bit_rate":"320000","tags":{"title":"New","artist":"Artist","album":"Album","date":"2024-01-01"}},"streams":[{"codec_type":"audio","codec_name":"mp3"}]}`), nil
				default:
					t.Fatalf("unexpected command %s", name)
					return nil, nil
				}
			}
			// An online lookup rewrites every field, so the album follows the file.
			r, err := UpdateFileTags(context.Background(), d, lib, "1", FileTagOptions{Options: acquire.TagOptions{Lookup: acquire.LookupReplace, Title: new("New")}, Runner: runner})
			if changedAudio {
				if err == nil {
					t.Fatal("changed audio accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tagged, err := db.GetTrackByID(context.Background(), d, r.TrackID)
			if err != nil || tagged.Title.String != "New" || tagged.Album.String != "Album" || tagged.Rating.Int64 != 80 || !tagged.IsAnalyzed.Bool || tagged.PathString() != "original.mp3" || r.Path != "original.mp3" || tagged.FileBytes.Int64 != int64(len("tagged")) {
				t.Fatalf("result=%+v track=%+v error=%v", r, tagged, err)
			}
			// The write lands in place: same path, same mode, and no second copy beside it.
			data, err := os.ReadFile(original)
			if err != nil || string(data) != "tagged" {
				t.Fatalf("original=%q error=%v", data, err)
			}
			info, err := os.Stat(original)
			if err != nil || info.Mode().Perm() != 0640 {
				t.Fatalf("mode=%v error=%v", info, err)
			}
			audio, err := filepath.Glob(filepath.Join(lib, "*.mp3"))
			if err != nil || len(audio) != 1 {
				t.Fatalf("audio files=%v error=%v", audio, err)
			}
			var cues []byte
			if err := d.QueryRow(`SELECT quickCues FROM PerformanceData WHERE trackId=1`).Scan(&cues); err != nil || len(cues) != 2 {
				t.Fatalf("cues=%x error=%v", cues, err)
			}
		})
	}
}
