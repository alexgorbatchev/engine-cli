package sync

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func refreshFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.InitSchema(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "media")
	if err := os.Mkdir(media, 0700); err != nil {
		t.Fatal(err)
	}
	return database, media
}

func addRefreshTrack(t *testing.T, database *sql.DB, media string, id int, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(media, name), []byte("synthetic audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), "INSERT INTO Track(id,path,title,fileBytes) VALUES(?,?,?,0)", id, name, "Original"); err != nil {
		t.Fatal(err)
	}
}

func refreshTags(context.Context, string, ...string) ([]byte, error) {
	return []byte(`{"format":{"tags":{"title":"Refreshed"}}}`), nil
}

func TestRefreshWriteFailureRetainsPartialReport(t *testing.T) {
	database, media := refreshFixture(t)
	addRefreshTrack(t, database, media, 1, "first.wav")
	addRefreshTrack(t, database, media, 2, "second.wav")
	if _, err := database.ExecContext(t.Context(), "CREATE TRIGGER reject_second BEFORE UPDATE ON Track WHEN NEW.id=2 BEGIN SELECT RAISE(ABORT,'synthetic write failure'); END"); err != nil {
		t.Fatal(err)
	}
	report, err := SyncLibrary(t.Context(), database, media, refreshTags)
	if progress.ErrorCode(err) != "library_refresh_write_failed" {
		t.Fatalf("write failure was not reported: report=%+v error=%v", report, err)
	}
	if report == nil || report.UpdatedCount != 1 || report.TotalDBTracks != 2 {
		t.Fatalf("lost committed partial outcome: %+v", report)
	}
	for id, want := range map[int64]string{1: "Refreshed", 2: "Original"} {
		track, err := db.GetTrackByID(t.Context(), database, id)
		if err != nil || track.Title.String != want {
			t.Fatalf("track %d title=%q error=%v", id, track.Title.String, err)
		}
	}
}

func TestRefreshScanFailureRetainsCommittedUpdate(t *testing.T) {
	database, media := refreshFixture(t)
	addRefreshTrack(t, database, media, 1, "first.wav")
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if err := os.RemoveAll(media); err != nil {
			t.Fatal(err)
		}
		return refreshTags(ctx, name, args...)
	}
	report, err := SyncLibrary(t.Context(), database, media, runner)
	if progress.ErrorCode(err) != "library_refresh_scan_failed" || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scan failure was not reported: report=%+v error=%v", report, err)
	}
	if report == nil || report.UpdatedCount != 1 {
		t.Fatalf("lost committed update: %+v", report)
	}
}

func TestRefreshUnexpectedStatFailureIsNotMissing(t *testing.T) {
	database, media := refreshFixture(t)
	path := filepath.Join(media, "loop.wav")
	if err := os.Symlink("loop.wav", path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := database.ExecContext(t.Context(), "INSERT INTO Track(id,path) VALUES(1,'loop.wav')"); err != nil {
		t.Fatal(err)
	}
	report, err := SyncLibrary(t.Context(), database, media, refreshTags)
	if progress.ErrorCode(err) != "library_refresh_stat_failed" || report == nil || report.MissingCount != 0 {
		t.Fatalf("unexpected stat failure counted as missing: report=%+v error=%v", report, err)
	}
}

func TestRefreshCancellationRetainsEarlierWrites(t *testing.T) {
	database, media := refreshFixture(t)
	addRefreshTrack(t, database, media, 1, "first.wav")
	addRefreshTrack(t, database, media, 2, "second.wav")
	addRefreshTrack(t, database, media, 3, "third.wav")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls++
		if calls == 2 {
			cancel()
			return nil, ctx.Err()
		}
		return refreshTags(ctx, name, args...)
	}
	report, err := SyncLibrary(ctx, database, media, runner)
	if !errors.Is(err, context.Canceled) || progress.ErrorCode(err) != "cancelled" || report == nil || report.UpdatedCount != 1 {
		t.Fatalf("cancelled partial refresh: report=%+v error=%v", report, err)
	}
	track, err := db.GetTrackByID(t.Context(), database, 2)
	if err != nil || track.Title.String != "Original" || track.FileBytes.Int64 != 0 {
		t.Fatalf("cancelled track changed: track=%+v error=%v", track, err)
	}
}
