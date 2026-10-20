package track

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestUpgradeRejectsInvalidOriginal(t *testing.T) {
	for _, scenario := range []string{"unknown", "no path", "missing file", "directory", "missing metadata", "closed database"} {
		t.Run(scenario, func(t *testing.T) {
			database, libDir, _ := createTestDB(t)
			path := "original.mp3"
			if scenario == "no path" {
				path = ""
			}
			if _, err := database.Exec("INSERT INTO Track (id,path) VALUES (1,?)", path); err != nil {
				t.Fatal(err)
			}
			if scenario == "directory" {
				if err := os.Mkdir(filepath.Join(libDir, path), 0700); err != nil {
					t.Fatal(err)
				}
			} else if scenario == "missing metadata" {
				if err := os.WriteFile(filepath.Join(libDir, path), []byte("audio"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			id := "1"
			if scenario == "unknown" {
				id = "99"
			}
			if scenario == "closed database" {
				if err := database.Close(); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			_, err := UpgradeTrack(context.Background(), database, libDir, id, UpgradeOptions{Runner: func(context.Context, string, ...string) ([]byte, error) {
				called = true
				return nil, errors.New("unexpected acquisition")
			}})
			if err == nil || called {
				t.Fatalf("error = %v, acquisition called = %v", err, called)
			}
		})
	}
}

func TestUpgradeWorkspaceLifecycle(t *testing.T) {
	t.Chdir(t.TempDir())
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	if err := os.WriteFile(".tmp", []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	work, cleanup, err := upgradeWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(work) != canonical {
		t.Fatalf("workspace outside OS temp: %s", work)
	}
	cleanup()
	if _, err = os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("workspace retained: %v", err)
	}
	data, err := os.ReadFile(".tmp")
	if err != nil || string(data) != "keep" {
		t.Fatalf("modified working directory: %q %v", data, err)
	}
	if err = os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if _, _, err = upgradeWorkspace(); err == nil {
		t.Fatal("accepted missing OS temp directory")
	}
}

func TestFetchUpgradeRejectsUnusableDownloads(t *testing.T) {
	for _, scenario := range []string{"unverified", "low bandwidth", "silent", "missing peak", "missing", "empty", "directory", "symlink escape"} {
		t.Run(scenario, func(t *testing.T) {
			work := t.TempDir()
			file := filepath.Join(work, "candidate.m4a")
			verification := any(map[string]int{"bandwidth_hz": 20000, "peak_dbfs": -1})
			switch scenario {
			case "unverified":
				verification = nil
			case "low bandwidth":
				verification = map[string]int{"bandwidth_hz": 12000, "peak_dbfs": -1}
			case "silent":
				verification = map[string]int{"bandwidth_hz": 20000, "peak_dbfs": -180}
			case "missing peak":
				verification = map[string]int{"bandwidth_hz": 20000}
			case "directory":
				if err := os.Mkdir(file, 0700); err != nil {
					t.Fatal(err)
				}
			case "empty":
				if err := os.WriteFile(file, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink escape":
				outside := filepath.Join(t.TempDir(), "original.m4a")
				if err := os.WriteFile(outside, []byte("audio"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, file); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "silent" || scenario == "missing peak" {
				if err := os.WriteFile(file, []byte("synthetic audio"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runner := func(context.Context, string, ...string) ([]byte, error) {
				return json.Marshal(map[string]any{"status": "success", "output_file": file, "verification": verification})
			}
			_, err := fetchUpgrade(context.Background(), runner, "Artist - Title", work, acquire.TrackOptions{})
			if err == nil {
				t.Fatal("unusable audio accepted")
			}
			if (scenario == "silent" || scenario == "missing peak") && !strings.Contains(err.Error(), "signal measurement") {
				t.Fatalf("unexpected peak rejection: %v", err)
			}
		})
	}
}

func TestProbeUpgradeRejectsMalformedAudio(t *testing.T) {
	for _, data := range []string{"not json", `{"streams":[{"codec_type":"video","codec_name":"mjpeg"}]}`} {
		runner := func(context.Context, string, ...string) ([]byte, error) { return []byte(data), nil }
		if _, err := probeUpgrade(context.Background(), runner, "candidate.m4a"); err == nil {
			t.Fatalf("accepted probe %q", data)
		}
	}
}

func TestUpgradeAudioPropertiesValidation(t *testing.T) {
	for _, data := range []string{
		`{"format":{"duration":"NaN","bit_rate":"256000"}}`,
		`{"format":{"duration":"+Inf","bit_rate":"256000"}}`,
		`{"format":{"duration":"0","bit_rate":"256000"}}`,
		`{"format":{"duration":"300","bit_rate":"invalid"}}`,
	} {
		var probe upgradeProbe
		if err := json.Unmarshal([]byte(data), &probe); err != nil {
			t.Fatal(err)
		}
		if _, _, err := probe.audioProperties(); err == nil {
			t.Fatalf("accepted properties %s", data)
		}
	}
}

func TestCommitUpgradeRollsBackTimingFailure(t *testing.T) {
	database, _, _ := createTestDB(t)
	if _, err := database.Exec(`INSERT INTO Track (id,path,title) VALUES (1,'original.mp3','Curated'); CREATE TABLE PerformanceData(trackId INTEGER); INSERT INTO PerformanceData VALUES (1); CREATE TRIGGER refuse_clear BEFORE DELETE ON PerformanceData BEGIN SELECT RAISE(ABORT,'keep timing'); END;`); err != nil {
		t.Fatal(err)
	}
	original, err := db.GetTrackByID(context.Background(), database, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = commitUpgrade(context.Background(), database, original, upgradedFile{Path: "replacement.m4a", Bytes: 10, Duration: 300, Bitrate: 256}, UpgradeOptions{ClearTiming: true})
	if err == nil || !strings.Contains(err.Error(), "clearing old timing data") {
		t.Fatalf("error = %v", err)
	}
	got, err := db.GetTrackByID(context.Background(), database, 1)
	if err != nil || got.Path.String != "original.mp3" || got.Title.String != "Curated" {
		t.Fatalf("rollback = %+v, %v", got, err)
	}
	var count int
	if err := database.QueryRow("SELECT count(*) FROM PerformanceData").Scan(&count); err != nil || count != 1 {
		t.Fatalf("timing changed: %d %v", count, err)
	}
}

func TestCommitUpgradeDetectsConcurrentEdit(t *testing.T) {
	database, _, _ := createTestDB(t)
	if _, err := database.Exec(`INSERT INTO Track (id,path) VALUES (1,'original.mp3')`); err != nil {
		t.Fatal(err)
	}
	original, err := db.GetTrackByID(context.Background(), database, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE Track SET path='moved.mp3' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	_, err = commitUpgrade(context.Background(), database, original, upgradedFile{Path: "replacement.m4a"}, UpgradeOptions{KeepTiming: true})
	if err == nil || !strings.Contains(err.Error(), "changed during acquisition") {
		t.Fatalf("error = %v", err)
	}
	got, err := db.GetTrackByID(context.Background(), database, 1)
	if err != nil || got.Path.String != "moved.mp3" {
		t.Fatalf("concurrent edit overwritten: %+v %v", got, err)
	}
}

func TestInstallUpgradeFailuresRetainOriginal(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original.mp3")
	if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := installUpgrade(context.Background(), filepath.Join(root, "missing.m4a"), original); err == nil {
		t.Fatal("accepted missing source")
	}
	source := filepath.Join(root, "staged.m4a")
	if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := installUpgrade(context.Background(), source, filepath.Join(root, "missing", "original.mp3")); err == nil {
		t.Fatal("accepted missing destination directory")
	}
	b, err := os.ReadFile(original)
	if err != nil || string(b) != "original" {
		t.Fatalf("original = %q %v", b, err)
	}
}

func TestUpgradeTimingRequiresExplicitDecision(t *testing.T) {
	for _, scenario := range []string{"no table", "empty", "saved", "keep", "clear", "conflicting"} {
		t.Run(scenario, func(t *testing.T) {
			database, libDir, _ := createTestDB(t)
			if _, err := database.Exec(`INSERT INTO Track(id,path,title,artist) VALUES(1,'original.mp3','Title','Artist')`); err != nil {
				t.Fatal(err)
			}
			if scenario != "no table" {
				if _, err := database.Exec("CREATE TABLE PerformanceData(trackId INTEGER)"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "no table" && scenario != "empty" {
				if _, err := database.Exec("INSERT INTO PerformanceData VALUES(1)"); err != nil {
					t.Fatal(err)
				}
			}
			opts := UpgradeOptions{KeepTiming: scenario == "keep" || scenario == "conflicting", ClearTiming: scenario == "clear" || scenario == "conflicting"}
			err := checkUpgradeTiming(context.Background(), database, 1, opts)
			if (err != nil) != (scenario == "saved") {
				t.Fatalf("timing check error = %v", err)
			}
			if scenario == "conflicting" {
				_, err := UpgradeTrack(context.Background(), database, libDir, "1", opts)
				if err == nil || !strings.Contains(err.Error(), "--keep-timing") || !strings.Contains(err.Error(), "--clear-timing") {
					t.Fatalf("conflicting options error = %v", err)
				}
			}
		})
	}
}

func TestCommitUpgradeTimingModes(t *testing.T) {
	for _, mode := range []string{"clear", "keep", "no table"} {
		t.Run(mode, func(t *testing.T) {
			database, _, _ := createTestDB(t)
			if _, err := database.Exec(`INSERT INTO Track(id,path,title,isAnalyzed) VALUES(1,'original.mp3','Curated',1)`); err != nil {
				t.Fatal(err)
			}
			if mode != "no table" {
				if _, err := database.Exec("CREATE TABLE PerformanceData(trackId INTEGER); INSERT INTO PerformanceData VALUES(1),(2)"); err != nil {
					t.Fatal(err)
				}
			}
			original, err := db.GetTrackByID(context.Background(), database, 1)
			if err != nil {
				t.Fatal(err)
			}
			opts := UpgradeOptions{KeepTiming: mode == "keep", ClearTiming: mode == "clear"}
			cleared, err := commitUpgrade(context.Background(), database, original, upgradedFile{Path: "replacement.m4a", Bytes: 42, Duration: 360, Bitrate: 256}, opts)
			if err != nil || cleared != (mode == "clear") {
				t.Fatalf("cleared=%v error=%v", cleared, err)
			}
			got, err := db.GetTrackByID(context.Background(), database, 1)
			if err != nil || got.Path.String != "replacement.m4a" || got.Title.String != "Curated" || got.IsAnalyzed.Bool || got.FileBytes.Int64 != 42 {
				t.Fatalf("committed=%+v error=%v", got, err)
			}
			if mode != "no table" {
				var count int
				want := 1
				if mode == "keep" {
					want = 2
				}
				if err := database.QueryRow("SELECT count(*) FROM PerformanceData").Scan(&count); err != nil || count != want {
					t.Fatalf("timing count=%d error=%v", count, err)
				}
			}
		})
	}
}

func TestCommitUpgradePreservesTimingAddedDuringAcquisition(t *testing.T) {
	database, _, _ := createTestDB(t)
	if _, err := database.Exec(`INSERT INTO Track(id,path) VALUES(1,'original.mp3'); CREATE TABLE PerformanceData(trackId INTEGER,quickCues BLOB)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := checkUpgradeTiming(ctx, database, 1, UpgradeOptions{}); err != nil {
		t.Fatal(err)
	}
	original, err := db.GetTrackByID(ctx, database, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO PerformanceData VALUES(1,x'010203')"); err != nil {
		t.Fatal(err)
	}
	_, err = commitUpgrade(ctx, database, original, upgradedFile{Path: "replacement.m4a", Bytes: 42, Duration: 360, Bitrate: 256}, UpgradeOptions{})
	if err == nil || !strings.Contains(err.Error(), "--keep-timing") || !strings.Contains(err.Error(), "--clear-timing") {
		t.Fatalf("expected timing decision error, got %v", err)
	}
	got, err := db.GetTrackByID(ctx, database, 1)
	if err != nil || got.Path.String != "original.mp3" {
		t.Fatalf("track changed: %+v %v", got, err)
	}
	var cues []byte
	if err := database.QueryRow("SELECT quickCues FROM PerformanceData WHERE trackId=1").Scan(&cues); err != nil || string(cues) != string([]byte{1, 2, 3}) {
		t.Fatalf("new timing changed: %x %v", cues, err)
	}
}

func TestUpgradeTagsPreserveCuratedAndFillMissing(t *testing.T) {
	var old, replacement upgradeProbe
	if err := json.Unmarshal([]byte(`{"format":{"tags":{"DATE":"2001-02-03","ALBUM":"Original album","REPLAYGAIN_TRACK_GAIN":"-6 dB","encoder":"old encoder"}},"streams":[{"codec_type":"audio","tags":{"COMMENT":"Original comment","r128_track_gain":"42"}}]}`), &old); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"format":{"tags":{"title":"Replacement title","artist":"Replacement artist","genre":"House","compatible_brands":"mp42"}},"streams":[{"codec_type":"audio","tags":{"composer":"Replacement composer"}}]}`), &replacement); err != nil {
		t.Fatal(err)
	}
	track := &db.Track{Title: sql.NullString{String: "Curated title", Valid: true}, Year: sql.NullInt64{Int64: 2001, Valid: true}, BPM: sql.NullInt64{Int64: 125, Valid: true}, Rating: sql.NullInt64{Int64: 5, Valid: true}}
	tags := upgradeTags(track, &old, &replacement)
	for key, want := range map[string]string{"title": "Curated title", "artist": "Replacement artist", "album": "Original album", "comment": "Original comment", "genre": "House", "composer": "Replacement composer", "date": "2001-02-03", "bpm": "125", "rating": "5"} {
		if tags[key] != want {
			t.Fatalf("%s = %q, want %q", key, tags[key], want)
		}
	}
	for _, key := range []string{"replaygain_track_gain", "r128_track_gain", "encoder", "compatible_brands"} {
		if _, exists := tags[key]; exists {
			t.Fatalf("retained stale encoding tag %q", key)
		}
	}
	track.Year.Int64 = 2020
	if got := upgradeTags(track, &old, &replacement)["date"]; got != "2020" {
		t.Fatalf("curated year conflict = %q", got)
	}
}

func TestUpgradeLateFailuresLeaveLibraryUntouched(t *testing.T) {
	for _, stage := range []string{"replacement probe", "final probe", "duration", "missing staged file"} {
		t.Run(stage, func(t *testing.T) {
			database, libDir, _ := createTestDB(t)
			original := filepath.Join(libDir, "original.mp3")
			if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec("INSERT INTO Track(id,path) VALUES(1,?)", original); err != nil {
				t.Fatal(err)
			}
			probes := 0
			runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
				switch name {
				case "fetch-track":
					if len(args) > 0 && args[0] == "verify" {
						return []byte(`{"status":"success","verification":{"bandwidth_hz":16000}}`), nil
					}
					if args[len(args)-1] != "Artist - Explicit query" {
						t.Fatalf("query=%v", args)
					}
					var work string
					for i, arg := range args {
						if arg == "--out-dir" {
							work = args[i+1]
						}
					}
					candidate := filepath.Join(work, "candidate.m4a")
					if err := os.WriteFile(candidate, []byte("replacement"), 0600); err != nil {
						return nil, err
					}
					return json.Marshal(map[string]any{"status": "success", "output_file": candidate, "verification": map[string]int{"bandwidth_hz": 20000, "peak_dbfs": -1}})
				case "ffprobe":
					probes++
					if stage == "replacement probe" && probes == 2 || stage == "final probe" && probes == 3 {
						return nil, errors.New("unreadable audio")
					}
					duration := "300"
					if stage == "duration" && probes == 3 {
						duration = "invalid"
					}
					return []byte(`{"format":{"duration":"` + duration + `","bit_rate":"256000"},"streams":[{"codec_type":"audio","codec_name":"aac"}]}`), nil
				case "ffmpeg":
					if stage != "missing staged file" {
						return nil, os.WriteFile(args[len(args)-1], []byte("prepared"), 0600)
					}
					return nil, nil
				default:
					return nil, errors.New("unexpected command " + name)
				}
			}
			if _, err := UpgradeTrack(context.Background(), database, libDir, "1", UpgradeOptions{Query: "Artist - Explicit query", Runner: runner}); err == nil {
				t.Fatal("invalid replacement accepted")
			}
			got, err := db.GetTrackByID(context.Background(), database, 1)
			if err != nil || got.Path.String != original {
				t.Fatalf("library changed: %+v %v", got, err)
			}
			b, err := os.ReadFile(original)
			if err != nil || string(b) != "original" {
				t.Fatalf("original changed: %q %v", b, err)
			}
		})
	}
}

func TestVorbisCoverFileErrors(t *testing.T) {
	work := t.TempDir()
	if _, err := vorbisUpgradeCover(work, filepath.Join(work, "missing.jpg")); err == nil {
		t.Fatal("missing cover accepted")
	}
	cover := filepath.Join(work, "cover.jpg")
	if err := os.WriteFile(cover, []byte("cover"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := vorbisUpgradeCover(filepath.Join(work, "missing"), cover); err == nil {
		t.Fatal("missing metadata destination accepted")
	}
	got, err := os.ReadFile(cover)
	if err != nil || string(got) != "cover" {
		t.Fatalf("cover changed: %q %v", got, err)
	}
}

func TestUpgradeTagPreparationErrors(t *testing.T) {
	var coverProbe upgradeProbe
	if err := json.Unmarshal([]byte(`{"streams":[{"index":1,"codec_type":"video","disposition":{"attached_pic":1}}]}`), &coverProbe); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"missing extension", "cover extraction", "missing extracted cover"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := "candidate.ogg"
			if scenario == "missing extension" {
				candidate = "candidate"
			}
			input := upgradeTagInput{Candidate: candidate, Original: "original.mp3", OriginalProbe: &coverProbe, CandidateProbe: &upgradeProbe{}}
			runner := func(context.Context, string, ...string) ([]byte, error) {
				if scenario == "cover extraction" {
					return nil, errors.New("corrupt original artwork")
				}
				return nil, nil
			}
			if _, err := tagUpgrade(context.Background(), runner, t.TempDir(), input); err == nil {
				t.Fatal("invalid artwork preparation accepted")
			}
		})
	}
}

func TestUpgradeCompatibleNativeFormats(t *testing.T) {
	tests := []struct {
		name, ext, codec string
		wantErr          bool
	}{
		{"mp3", ".mp3", "mp3", false},
		{"alac", ".m4a", "alac", false},
		{"aac uppercase extension", ".MP4", "aac", false},
		{"wave pcm", ".wav", "pcm_s24le", false},
		{"aiff pcm", ".aiff", "pcm_s16be", false},
		{"unsupported compressed wave", ".wav", "adpcm_ms", true},
		{"wrong extension", ".mp3", "aac", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var probe upgradeProbe
			data, err := json.Marshal(map[string]any{"streams": []map[string]string{{"codec_type": "video", "codec_name": "mjpeg"}, {"codec_type": "audio", "codec_name": tt.codec}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &probe); err != nil {
				t.Fatal(err)
			}
			if err := probe.engineCompatible("candidate" + tt.ext); (err != nil) != tt.wantErr {
				t.Fatalf("compatibility error=%v", err)
			}
		})
	}
	t.Run("no audio", func(t *testing.T) {
		if err := (&upgradeProbe{}).engineCompatible("candidate.mp3"); err == nil {
			t.Fatal("accepted no audio")
		}
	})
}

func TestCachedUpgradeArtworkErrors(t *testing.T) {
	for _, scenario := range []string{"missing", "empty", "database", "destination", "success"} {
		t.Run(scenario, func(t *testing.T) {
			database, _, _ := createTestDB(t)
			work := t.TempDir()
			track := &db.Track{AlbumArtID: sql.NullInt64{Int64: 1, Valid: true}}
			data := []byte("curated artwork")
			if scenario == "empty" {
				data = nil
			}
			if scenario != "missing" {
				if _, err := database.Exec("INSERT INTO AlbumArt(id,albumArt) VALUES(1,?)", data); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "database" {
				if err := database.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "destination" {
				work = filepath.Join(work, "missing")
			}
			path, err := cachedUpgradeCover(context.Background(), database, work, track)
			wantErr := scenario == "database" || scenario == "destination"
			if (err != nil) != wantErr {
				t.Fatalf("path=%s error=%v", path, err)
			}
			if scenario == "missing" || scenario == "empty" {
				if path != "" {
					t.Fatalf("unexpected cover %s", path)
				}
			}
			if scenario == "success" {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(data) {
					t.Fatalf("curated cover=%q error=%v", got, err)
				}
			}
		})
	}
}
