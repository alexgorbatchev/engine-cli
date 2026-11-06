package staging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/testaudio"
)

func TestImportCleanupFailureReportsInstalledTrackAndRetries(t *testing.T) {
	ctx := context.Background()
	lib := t.TempDir()
	if err := os.MkdirAll(filepath.Join(lib, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.InitSchema(ctx, database); err != nil {
		t.Fatal(err)
	}
	s := New(lib)
	item, err := s.Create(ctx, Item{Kind: "track", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.Directory(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	item.AudioPath = item.ID + "/song.mp3"
	original := testaudio.Write(t, filepath.Join(dir, "song.mp3"))
	if err := s.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "unsafe-link")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	opts := ImportOptions{IDs: []string{item.ID}, Runner: testaudio.Metadata(`{}`)}
	report, err := s.Import(ctx, database, opts)
	if err != nil || len(report.Imported) != 1 || len(report.Failed) != 1 {
		t.Fatalf("installed track must survive cleanup failure in report: %+v %v", report, err)
	}
	if report.Imported[0].ID != item.ID || report.Failed[0].ID != item.ID || report.Failed[0].ErrorCode != "operation_failed" || !strings.Contains(report.Failed[0].Error, "imported but staged cleanup failed") {
		t.Fatalf("cleanup result %+v", report)
	}
	trackID := report.Imported[0].TrackID
	tracks, err := db.GetAllTracks(ctx, database)
	if err != nil || len(tracks) != 1 || tracks[0].ID != trackID {
		t.Fatalf("collection %+v %v", tracks, err)
	}
	staged, err := s.Open(ctx, item.ID, false)
	if err != nil {
		t.Fatalf("cleanup failure lost retryable staging: %v", err)
	}
	if err := staged.Close(); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(lib, "Music", "staging", item.ID, "song.mp3")
	if data, err := os.ReadFile(installed); err != nil || !bytes.Equal(data, original) {
		t.Fatalf("installed audio %q %v", data, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	report, err = s.Import(ctx, database, opts)
	if err != nil || len(report.Imported) != 1 || len(report.Failed) != 0 || report.Imported[0].TrackID != trackID {
		t.Fatalf("retry duplicated track or failed cleanup: %+v %v", report, err)
	}
	tracks, err = db.GetAllTracks(ctx, database)
	if err != nil || len(tracks) != 1 {
		t.Fatalf("retry collection %+v %v", tracks, err)
	}
	if _, err := s.Inspect(ctx, item.ID); !os.IsNotExist(err) {
		t.Fatalf("successful retry retained staging: %v", err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "untouched" {
		t.Fatalf("external file changed: %q %v", data, err)
	}
}

func TestImportAndUndo(t *testing.T) {
	ctx := context.Background()
	lib := t.TempDir()
	if err := os.MkdirAll(filepath.Join(lib, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.InitSchema(ctx, database); err != nil {
		t.Fatal(err)
	}
	s := New(lib)
	mix, err := s.Create(ctx, Item{Kind: "mix", Title: "Mix"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.Create(ctx, Item{Kind: "track", ParentID: mix.ID, Title: "Song", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	missing, err := s.Create(ctx, Item{Kind: "track", ParentID: mix.ID, Title: "Missing", Status: "failed", Error: "not found"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.root(), child.ID, "song.mp3")
	testaudio.Write(t, path)
	child.AudioPath = child.ID + "/song.mp3"
	if err := s.Update(ctx, *child); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO Playlist(id,title,parentListId) VALUES(7,'Imports',0)"); err != nil {
		t.Fatal(err)
	}
	var report *ImportReport
	entry, err := history.Run(ctx, lib, "staging.import", history.ScopeCollection, nil, func(ctx context.Context) error {
		var err error
		report, err = s.Import(ctx, database, ImportOptions{IDs: []string{mix.ID}, ToPlaylist: "7", Runner: testaudio.Metadata(`{"format":{"tags":{"title":"Tagged"}}}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Imported) != 1 || len(report.Failed) != 1 || report.Failed[0].ID != missing.ID {
		t.Fatalf("report %+v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Imported []map[string]any `json:"imported"`
	}
	trackID := report.Imported[0].TrackID
	if err := json.Unmarshal(encoded, &decoded); err != nil || !reflect.DeepEqual(decoded.Imported, []map[string]any{{"id": child.ID, "track_id": float64(trackID)}}) {
		t.Fatalf("import JSON must pair staged and track IDs: %s: %v", encoded, err)
	}
	imported, err := db.GetTrackByID(ctx, database, trackID)
	if err != nil || imported.Title.String != "Tagged" || imported.Artist.Valid {
		t.Fatalf("tags not imported: %+v %v", imported, err)
	}
	memberships, err := db.PlaylistMemberships(ctx, database, []int64{trackID})
	if err != nil || len(memberships[trackID]) != 1 || memberships[trackID][0].ID != 7 {
		t.Fatalf("import playlist not assigned: %+v %v", memberships, err)
	}
	if _, err := s.Inspect(ctx, child.ID); !os.IsNotExist(err) {
		t.Fatalf("imported staged item remains: %v", err)
	}
	if _, err := s.Inspect(ctx, missing.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Undo(ctx, lib, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, child.ID, false); err != nil {
		t.Fatalf("undo lost staged audio: %v", err)
	}
	tracks, err := db.GetAllTracks(ctx, database)
	if err != nil || len(tracks) != 0 {
		t.Fatalf("undo collection %v %v", tracks, err)
	}
}

func TestImportFailureRetainsStaging(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	item, err := s.Create(ctx, Item{Kind: "track", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.Import(ctx, nil, ImportOptions{IDs: []string{item.ID}})
	if err == nil || report != nil {
		t.Fatalf("nil database accepted: %+v %v", report, err)
	}
	if _, err := s.Inspect(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
}

// An import outside any playlist completes without reading playlist memberships.
func TestImportDoesNotReadPlaylistMemberships(t *testing.T) {
	ctx := context.Background()
	lib := t.TempDir()
	if err := os.MkdirAll(filepath.Join(lib, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.InitSchema(ctx, database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("DROP TABLE PlaylistEntity"); err != nil {
		t.Fatal(err)
	}
	s := New(lib)
	item, err := s.Create(ctx, Item{Kind: "track", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.Directory(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	item.AudioPath = item.ID + "/song.mp3"
	testaudio.Write(t, filepath.Join(dir, "song.mp3"))
	if err := s.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	report, err := s.Import(ctx, database, ImportOptions{IDs: []string{item.ID}, Runner: testaudio.Metadata(`{}`)})
	if err != nil || len(report.Imported) != 1 || report.Imported[0].ID != item.ID || len(report.Failed) != 0 {
		t.Fatalf("import must succeed without reading memberships: %+v %v", report, err)
	}
}

func TestImportRetriesAndMixCompletion(t *testing.T) {
	ctx := context.Background()
	lib := t.TempDir()
	s := New(lib)
	if err := os.MkdirAll(filepath.Join(lib, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.InitSchema(ctx, database); err != nil {
		t.Fatal(err)
	}
	mix, err := s.Create(ctx, Item{Kind: "mix"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, Item{Kind: "track", ParentID: mix.ID, Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	opts := ImportOptions{IDs: []string{item.ID}, Runner: testaudio.Metadata(`{}`)}
	report, err := s.Import(ctx, database, opts)
	if err != nil || len(report.Failed) != 1 {
		t.Fatalf("missing audio %+v %v", report, err)
	}
	dir, err := s.Directory(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	item.AudioPath = item.ID + "/audio.mp3"
	if err := s.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	report, err = s.Import(ctx, database, opts)
	if err != nil || len(report.Failed) != 1 {
		t.Fatalf("missing file %+v %v", report, err)
	}
	testaudio.Write(t, filepath.Join(dir, "audio.mp3"))
	opts.ToPlaylist = "999"
	report, err = s.Import(ctx, database, opts)
	if err != nil || len(report.Failed) != 1 {
		t.Fatalf("missing playlist %+v %v", report, err)
	}
	if _, err := s.Open(ctx, item.ID, false); err != nil {
		t.Fatal("failed import removed source", err)
	}
	opts.ToPlaylist = ""
	opts.IDs = []string{mix.ID, item.ID}
	report, err = s.Import(ctx, database, opts)
	if err != nil || len(report.Imported) != 1 || len(report.Failed) != 0 {
		t.Fatalf("retry %+v %v", report, err)
	}
	if all, err := s.List(ctx); err != nil || len(all) != 0 {
		t.Fatalf("mix not completed %v %v", all, err)
	}
	if _, err := s.Import(ctx, database, ImportOptions{}); err == nil {
		t.Fatal("accepted empty import")
	}
}

func TestImportInstallationFailure(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	item, err := s.Create(ctx, Item{Kind: "track", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.Directory(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	testaudio.Write(t, filepath.Join(dir, "audio.mp3"))
	item.AudioPath = item.ID + "/audio.mp3"
	if err := s.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.lib, "Music"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	report, err := s.Import(ctx, d, ImportOptions{IDs: []string{item.ID}})
	if err != nil || len(report.Failed) != 1 {
		t.Fatalf("installation failure %+v %v", report, err)
	}
	if _, err := s.Open(ctx, item.ID, false); err != nil {
		t.Fatalf("failed installation lost source %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Import(cancelled, d, ImportOptions{IDs: []string{item.ID}}); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestImportRejectsNonAudioBeforeInstallation(t *testing.T) {
	for _, cancelProbe := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid audio", true: "cancelled probe"}[cancelProbe], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			lib := t.TempDir()
			database, err := db.Open(filepath.Join(lib, "collection.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if err := db.InitSchema(ctx, database); err != nil {
				t.Fatal(err)
			}
			s := New(lib)
			item, err := s.Create(ctx, Item{Kind: "track", Status: "ready"})
			if err != nil {
				t.Fatal(err)
			}
			dir, err := s.Directory(ctx, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "not-a-song.wav")
			original := []byte("not readable audio")
			if cancelProbe {
				original = testaudio.Write(t, path)
			} else if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			item.AudioPath = item.ID + "/not-a-song.wav"
			if err := s.Update(ctx, *item); err != nil {
				t.Fatal(err)
			}
			opts := ImportOptions{IDs: []string{item.ID}}
			if cancelProbe {
				opts.Runner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
					cancel()
					return exec.CommandContext(ctx, name, args...).Output()
				}
			}
			report, err := s.Import(ctx, database, opts)
			if cancelProbe {
				if !errors.Is(err, context.Canceled) || report == nil || len(report.Imported) != 0 || len(report.Failed) != 0 {
					t.Fatalf("probe cancellation swallowed: %+v %v", report, err)
				}
			} else if err != nil || len(report.Imported) != 0 || len(report.Failed) != 1 || report.Failed[0].ErrorCode != "track_not_audio" {
				t.Fatalf("non-audio import: %+v %v", report, err)
			}
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, original) {
				t.Fatalf("refusal changed staged source: %q %v", data, err)
			}
			if current, err := s.Inspect(t.Context(), item.ID); err != nil || current.AudioPath != item.AudioPath || current.Status != "ready" {
				t.Fatalf("refusal changed staged item: %+v %v", current, err)
			}
			var count int
			if err := database.QueryRowContext(t.Context(), "SELECT count(*) FROM Track").Scan(&count); err != nil || count != 0 {
				t.Fatalf("refusal changed collection: %d %v", count, err)
			}
			if _, err := os.Stat(filepath.Join(lib, "Music")); !os.IsNotExist(err) {
				t.Fatalf("non-audio installed into library: %v", err)
			}
		})
	}
}
