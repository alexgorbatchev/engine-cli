package staging

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
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
	if err := os.WriteFile(filepath.Join(dir, "song.mp3"), []byte("original audio"), 0600); err != nil {
		t.Fatal(err)
	}
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
	opts := ImportOptions{IDs: []string{item.ID}, Runner: func(context.Context, string, ...string) ([]byte, error) { return []byte(`{}`), nil }}
	report, err := s.Import(ctx, database, opts)
	if err != nil || len(report.Imported) != 1 || len(report.Failed) != 1 {
		t.Fatalf("installed track must survive cleanup failure in report: %+v %v", report, err)
	}
	if report.Imported[0].ID != item.ID || report.Failed[0].ID != item.ID || report.Failed[0].ErrorCode != "operation_failed" || !strings.Contains(report.Failed[0].Error, "imported but staged cleanup failed") {
		t.Fatalf("cleanup result %+v", report)
	}
	trackID := report.Imported[0].Track.ID
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
	if data, err := os.ReadFile(installed); err != nil || string(data) != "original audio" {
		t.Fatalf("installed audio %q %v", data, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	report, err = s.Import(ctx, database, opts)
	if err != nil || len(report.Imported) != 1 || len(report.Failed) != 0 || report.Imported[0].Track.ID != trackID {
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
	if err := os.WriteFile(path, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
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
		report, err = s.Import(ctx, database, ImportOptions{IDs: []string{mix.ID}, ToPlaylist: "7", Runner: func(context.Context, string, ...string) ([]byte, error) {
			return []byte(`{"format":{"tags":{"title":"Tagged"}}}`), nil
		}})
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
		Imported []struct {
			Track struct {
				Title       string
				Artist      *string
				PlaylistIDs []int64 `json:"playlistIds"`
			}
		}
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Imported[0].Track.Title != "Tagged" || decoded.Imported[0].Track.Artist != nil || !reflect.DeepEqual(decoded.Imported[0].Track.PlaylistIDs, []int64{7}) {
		t.Fatalf("import JSON must use track values and nulls: %s: %v", encoded, err)
	}
	if _, err := s.Inspect(ctx, child.ID); !os.IsNotExist(err) {
		t.Fatalf("imported staged item remains: %v", err)
	}
	if _, err := s.Inspect(ctx, missing.ID); err != nil {
		t.Fatal(err)
	}
	if report.Imported[0].Track.Title.String != "Tagged" {
		t.Fatal("tags not imported")
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

func TestImportReportsUnreadablePlaylistsAfterImporting(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "song.mp3"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	report, err := s.Import(ctx, database, ImportOptions{IDs: []string{item.ID}, Runner: func(context.Context, string, ...string) ([]byte, error) { return []byte(`{}`), nil }})
	if err == nil || !strings.Contains(err.Error(), "playlist memberships") || len(report.Imported) != 1 {
		t.Fatalf("unreadable memberships must fail after reporting the import: %+v %v", report, err)
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
	opts := ImportOptions{IDs: []string{item.ID}, Runner: func(context.Context, string, ...string) ([]byte, error) { return []byte(`{}`), nil }}
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
	if err := os.WriteFile(filepath.Join(dir, "audio.mp3"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(filepath.Join(dir, "audio.mp3"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
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
