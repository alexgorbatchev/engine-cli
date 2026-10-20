package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestAPIPlaylistMutations(t *testing.T) {
	s, lib := testAPI(t)
	runAPIJob(t, s, "playlist.create", `{"name":"Folder","folder":true}`)
	runAPIJob(t, s, "playlist.create", `{"name":"Second","parent":"2"}`)
	runAPIJob(t, s, "playlist.inspect", `{"ids":[3]}`)
	runAPIJob(t, s, "playlist.move", `{"identifier":"3","to":""}`)
	runAPIJob(t, s, "playlist.track.add", `{"playlist":"1","tracks":["1"]}`)
	runAPIJob(t, s, "playlist.track.move", `{"from":"1","to":"3","tracks":["1"]}`)
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	second, err := db.FindPlaylist(context.Background(), database, "3")
	if err != nil || second == nil || second.ParentListID != 0 {
		t.Fatalf("moved playlist=%+v error=%v", second, err)
	}
	tracks, err := db.GetPlaylistTracks(context.Background(), database, second.ID)
	if err != nil || len(tracks) != 1 || tracks[0].ID != 1 {
		t.Fatalf("moved membership=%+v error=%v", tracks, err)
	}
	runAPIJob(t, s, "playlist.track.rm", `{"playlist":"3","tracks":["1"]}`)
	runAPIJob(t, s, "playlist.rm", `{"identifier":"3","force":true}`)
	second, err = db.FindPlaylist(context.Background(), database, "3")
	if err != nil || second != nil {
		t.Fatalf("removed playlist=%+v error=%v", second, err)
	}
}

func TestAPIMutationSnapshotPrecedesChanges(t *testing.T) {
	initial, lib := testAPI(t)
	initial.Close()
	s, err := New(Config{LibraryDir: lib})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	runAPIJob(t, s, "playlist.create", `{"name":"After snapshot"}`)
	snapshots, err := backup.ListBackups(s.config.LibraryDir, "")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots=%+v error=%v", snapshots, err)
	}
	database, err := db.Open(filepath.Join(snapshots[0].Path, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	pl, err := db.FindPlaylist(context.Background(), database, "2")
	if err != nil || pl != nil {
		t.Fatalf("snapshot was taken after mutation: %+v %v", pl, err)
	}
}

func TestAPIDatabaseAndBackupFailures(t *testing.T) {
	for _, scenario := range []string{"missing database", "corrupt database", "backup destination"} {
		t.Run(scenario, func(t *testing.T) {
			initial, lib := testAPI(t)
			initial.Close()
			path := filepath.Join(lib, "Database2", "m.db")
			operation, body := "playlist.list", `{}`
			if scenario == "missing database" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "corrupt database" {
				if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "backup destination" {
				blocked := filepath.Join(lib, ".engine-cli")
				if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				operation, body = "playlist.create", `{"name":"Never created"}`
			}
			s, err := New(Config{LibraryDir: lib})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			id := submitAPIJob(t, s, operation, body)
			job := awaitAPIJob(t, s, id)
			if job["state"] != "failed" {
				t.Fatalf("expected failure=%+v", job)
			}
			if scenario == "missing database" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("missing database created: %v", err)
				}
			}
		})
	}
}

func TestAPITrackMutationsAndEmptyMaintenance(t *testing.T) {
	s, lib := testAPI(t)
	runAPIJob(t, s, "track.relocate", `{"from":"song.mp3","to":"relocated.mp3"}`)
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	track, err := db.GetTrackByID(context.Background(), database, 1)
	if err != nil || track.Path.String != "relocated.mp3" {
		t.Fatalf("relocation=%+v error=%v", track, err)
	}
	runAPIJob(t, s, "track.rm", `{"tracks":["1"]}`)
	track, err = db.GetTrackByID(context.Background(), database, 1)
	if err != nil || track != nil {
		t.Fatalf("removed track=%+v error=%v", track, err)
	}
	for _, operation := range []string{"library.verify", "library.refresh", "library.prune", "track.artwork.normalize"} {
		runAPIJob(t, s, operation, `{}`)
	}
}

func TestAPIBackupsAndRestore(t *testing.T) {
	s, lib := testAPI(t)
	out := t.TempDir()
	body, err := json.Marshal(map[string]string{"out_dir": out})
	if err != nil {
		t.Fatal(err)
	}
	job := runAPIJob(t, s, "library.backup", string(body))
	result, ok := job["result"].(map[string]any)
	if !ok {
		t.Fatalf("backup result=%+v", job)
	}
	path, ok := result["path"].(string)
	if !ok {
		t.Fatalf("backup path=%+v", result)
	}
	runAPIJob(t, s, "library.backups", string(body))
	runAPIJob(t, s, "playlist.rm", `{"identifier":"1","force":true}`)
	restore, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, s, "library.restore", string(restore))
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	pl, err := db.FindPlaylist(context.Background(), database, "1")
	if err != nil || pl == nil {
		t.Fatalf("restored playlist=%+v error=%v", pl, err)
	}
}

func TestAPIOperationFailuresBecomeJobs(t *testing.T) {
	s, _ := testAPI(t)
	for _, tt := range []struct{ name, body string }{
		{"playlist.create", `{"name":"Bad","parent":"missing"}`},
		{"playlist.rm", `{"identifier":"missing"}`},
		{"playlist.move", `{"identifier":"missing","to":"Favorites"}`},
		{"playlist.track.add", `{"playlist":"missing","tracks":["1"]}`},
		{"playlist.track.rm", `{"playlist":"missing","tracks":["1"]}`},
		{"playlist.track.move", `{"from":"missing","to":"Favorites","tracks":["1"]}`},
		{"track.add", `{"paths":["missing.mp3"],"move":true,"to_playlist":"Favorites"}`},
		{"track.upgrade", `{"identifier":"missing","query":"Artist - Title","keep_timing":true}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := requestAPI(t, s, http.MethodPost, "/v1/operations/"+tt.name, tt.body)
			if w.Code != http.StatusAccepted {
				t.Fatalf("submit status=%d body=%s", w.Code, w.Body)
			}
			var accepted struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
				t.Fatal(err)
			}
			job := awaitAPIJob(t, s, accepted.ID)
			if job["error"] == nil || job["error"] == "" {
				t.Fatalf("expected failure job: %+v", job)
			}
		})
	}
	for _, tt := range []struct {
		name   string
		params map[string]string
	}{
		{"track.move", map[string]string{"identifier": "1", "destination": filepath.Join(t.TempDir(), "destination.mp3")}},
		{"library.restore", map[string]string{"path": filepath.Join(t.TempDir(), "missing")}},
	} {
		body, err := json.Marshal(tt.params)
		if err != nil {
			t.Fatal(err)
		}
		w := requestAPI(t, s, http.MethodPost, "/v1/operations/"+tt.name, string(body))
		if w.Code != http.StatusAccepted {
			t.Fatalf("submit status=%d body=%s", w.Code, w.Body)
		}
		var accepted struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
		job := awaitAPIJob(t, s, accepted.ID)
		if job["error"] == nil {
			t.Fatalf("expected failure job: %+v", job)
		}
	}
}
