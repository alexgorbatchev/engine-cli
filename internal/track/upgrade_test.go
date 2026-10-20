package track

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestUpgradeTrack(t *testing.T) {
	for _, fail := range []string{"", "fetch", "json", "warning", "outside", "probe", "tag", "database"} {
		t.Run(fail, func(t *testing.T) {
			database, libDir, _ := createTestDB(t)
			ctx := context.Background()
			original := filepath.Join(libDir, "original.mp3")
			if err := os.WriteFile(original, []byte("original audio"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO Track (id,path,filename,title,artist,album,comment,rating,isAnalyzed,length,bitrate,fileType) VALUES (1,'original.mp3','original.mp3','Curated title','Curated artist','Curated album','Keep this',5,1,180,128,'mp3'); INSERT INTO Playlist (id,title) VALUES (1,'Favorites'); INSERT INTO PlaylistEntity (id,listId,trackId,databaseUuid,nextEntityId) VALUES (1,1,1,'fixture',0); CREATE TABLE PerformanceData (trackId INTEGER, quickCues BLOB); INSERT INTO PerformanceData VALUES (1,x'0102');`); err != nil {
				t.Fatal(err)
			}
			var remuxArgs []string
			runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
				switch name {
				case "fetch-track":
					if args[0] == "verify" {
						return []byte(`{"status":"success","verification":{"bandwidth_hz":16000}}`), nil
					}
					if fail == "fetch" {
						return nil, errors.New("fetch failed")
					}
					if fail == "json" {
						return []byte("not JSON"), nil
					}
					if !strings.Contains(strings.Join(args, " "), "Curated artist - Curated title") {
						t.Fatalf("search ignores original metadata: %v", args)
					}
					var dir string
					for i, a := range args {
						if a == "--out-dir" {
							dir = args[i+1]
						}
					}
					candidate := filepath.Join(dir, "download.m4a")
					if fail == "outside" {
						candidate = original
					}
					if candidate != original {
						if err := os.WriteFile(candidate, []byte("better audio"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					status := "success"
					if fail == "warning" {
						status = "quality_warning"
					}
					return json.Marshal(map[string]any{"status": status, "output_file": candidate, "verification": map[string]any{"bandwidth_hz": 20000, "peak_dbfs": -1, "duration_seconds": 360}})
				case "ffprobe":
					if fail == "probe" {
						return nil, errors.New("probe failed")
					}
					if args[len(args)-1] == original {
						return []byte(`{"format":{"duration":"180","tags":{"title":"Old title","artist":"Old artist","comment":"Original comment","date":"2001-02-03"}},"streams":[{"codec_type":"audio","codec_name":"mp3"}]}`), nil
					}
					return []byte(`{"format":{"duration":"360","bit_rate":"256000","tags":{"title":"Bad title","artist":"Bad artist","album":"Bad album","genre":"House"}},"streams":[{"codec_type":"audio","codec_name":"aac","bit_rate":"256000"}]}`), nil
				case "ffmpeg":
					if fail == "tag" {
						return nil, errors.New("tag failed")
					}
					remuxArgs = args
					if err := os.WriteFile(args[len(args)-1], []byte("tagged better audio"), 0o600); err != nil {
						t.Fatal(err)
					}
					if fail == "database" {
						if _, err := database.Exec(`CREATE TRIGGER refuse_upgrade BEFORE UPDATE ON Track BEGIN SELECT RAISE(ABORT,'refused'); END;`); err != nil {
							t.Fatal(err)
						}
					}
					return nil, nil
				}
				t.Fatalf("unexpected command %s", name)
				return nil, nil
			}
			result, err := UpgradeTrack(ctx, database, libDir, "1", UpgradeOptions{Runner: runner, KeepTiming: true})
			if fail != "" {
				if err == nil {
					t.Fatal("expected upgrade failure")
				}
				got, lookupErr := db.GetTrackByID(ctx, database, 1)
				if lookupErr != nil || got.Path.String != "original.mp3" {
					t.Fatalf("failed upgrade changed track: %+v %v", got, lookupErr)
				}
				files, readErr := os.ReadDir(libDir)
				if readErr != nil {
					t.Fatal(readErr)
				}
				for _, file := range files {
					if strings.Contains(file.Name(), "upgraded") {
						t.Fatalf("failed upgrade left audio: %s", file.Name())
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				got, lookupErr := db.GetTrackByID(ctx, database, 1)
				if lookupErr != nil {
					t.Fatal(lookupErr)
				}
				if got.ID != 1 || got.Title.String != "Curated title" || got.Artist.String != "Curated artist" || got.Comment.String != "Keep this" || got.Rating.Int64 != 5 || got.Length.Int64 != 360 || got.Bitrate.Int64 != 256 || got.FileType.String != "m4a" || got.IsAnalyzed.Bool {
					t.Fatalf("incorrect upgrade: %+v", got)
				}
				if result.OriginalPath != original || result.Track.Path.String == "original.mp3" {
					t.Fatalf("incorrect result: %+v", result)
				}
				var trackID int
				if err := database.QueryRow("SELECT trackId FROM PlaylistEntity WHERE id=1").Scan(&trackID); err != nil || trackID != 1 {
					t.Fatalf("playlist membership lost: %d %v", trackID, err)
				}
				joined := strings.Join(remuxArgs, " ")
				for _, want := range []string{"-c:a copy", "title=Curated title", "artist=Curated artist", "comment=Keep this", "date=2001-02-03"} {
					if !strings.Contains(joined, want) {
						t.Errorf("missing %q in %s", want, joined)
					}
				}
			}
			data, readErr := os.ReadFile(original)
			if readErr != nil || string(data) != "original audio" {
				t.Fatalf("original changed: %s %v", data, readErr)
			}
		})
	}
}
