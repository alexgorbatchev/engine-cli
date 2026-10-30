package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestRefreshAbsolutePaths(t *testing.T) {
	for _, location := range []string{"external", "inside library", "relative library base", "drive casing"} {
		t.Run(location, func(t *testing.T) {
			database, media := refreshFixture(t)
			dir := media
			if location == "external" {
				dir = t.TempDir()
			}
			path := filepath.Join(dir, "absolute.wav")
			if location == "drive casing" {
				volume := filepath.VolumeName(path)
				if len(volume) != 2 || volume[1] != ':' {
					t.Skip("requires a Windows drive path")
				}
				variant := strings.ToLower(volume)
				if variant == volume {
					variant = strings.ToUpper(volume)
				}
				path = variant + strings.TrimPrefix(path, volume)
			}
			data := []byte("changed external audio")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := database.ExecContext(t.Context(), "INSERT INTO Track(id,path,title,fileBytes) VALUES(1,?,'Original',0)", path); err != nil {
				t.Fatal(err)
			}
			addRefreshTrack(t, database, media, 2, "relative.wav")
			calls := 0
			runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
				want := path
				if calls > 0 {
					want = filepath.Join(media, "relative.wav")
				}
				calls++
				if args[len(args)-1] != want {
					t.Fatalf("probe path=%q, want %q", args[len(args)-1], want)
				}
				return refreshTags(ctx, name, args...)
			}
			base := media
			if location == "relative library base" {
				t.Chdir(filepath.Dir(media))
				base = filepath.Base(media)
			}
			report, err := SyncLibrary(t.Context(), database, base, runner)
			if err != nil || calls != 2 || report == nil || report.TotalDBTracks != 2 || report.UpdatedCount != 2 || report.MissingCount != 0 || report.UnindexedCount != 0 {
				t.Fatalf("refresh: report=%+v calls=%d error=%v", report, calls, err)
			}
			track, err := db.GetTrackByID(t.Context(), database, 1)
			if err != nil || track.Title.String != "Refreshed" || track.FileBytes.Int64 != int64(len(data)) || track.PathString() != path {
				t.Fatalf("external metadata/path: track=%+v error=%v", track, err)
			}
		})
	}
}

func TestRefreshAbsoluteMissingAndStatFailure(t *testing.T) {
	for _, broken := range []bool{false, true} {
		name := "missing"
		if broken {
			name = "symlink loop"
		}
		t.Run(name, func(t *testing.T) {
			database, media := refreshFixture(t)
			addRefreshTrack(t, database, media, 1, "first.wav")
			path := filepath.Join(t.TempDir(), "external.wav")
			if broken {
				if err := os.Symlink(filepath.Base(path), path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if _, err := database.ExecContext(t.Context(), "INSERT INTO Track(id,path,title,fileBytes) VALUES(2,?,'Original',0)", path); err != nil {
				t.Fatal(err)
			}
			report, err := SyncLibrary(t.Context(), database, media, refreshTags)
			if report == nil || report.TotalDBTracks != 2 || report.UpdatedCount != 1 {
				t.Fatalf("partial outcome lost: %+v error=%v", report, err)
			}
			if broken {
				if progress.ErrorCode(err) != "library_refresh_stat_failed" || report.MissingCount != 0 {
					t.Fatalf("unexpected stat failure: report=%+v error=%v", report, err)
				}
			} else if err != nil || report.MissingCount != 1 {
				t.Fatalf("expected missing file: report=%+v error=%v", report, err)
			}
		})
	}
}
