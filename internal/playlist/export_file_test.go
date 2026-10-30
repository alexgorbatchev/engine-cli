package playlist

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestExportExclusivePublicationAndCancellation(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "set.m3u8")
			switch kind {
			case "file":
				if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			default:
				target := filepath.Join(parent, "target")
				if kind == "symlink" {
					if err := os.WriteFile(target, []byte("existing"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := publishExport(context.Background(), path, []byte("replacement")); progress.ErrorCode(err) != "playlist_export_exists" {
				t.Fatalf("destination conflict: %v", err)
			}
			if kind == "file" || kind == "symlink" {
				if raw, err := os.ReadFile(path); err != nil || string(raw) != "existing" {
					t.Fatalf("existing target changed: %q %v", raw, err)
				}
			}
		})
	}
	path := filepath.Join(t.TempDir(), "cancelled.m3u8")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := publishExport(ctx, path, []byte("unwanted")); err == nil {
		t.Fatal("cancelled export created file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := publishExport(context.Background(), filepath.Join(path, "missing.m3u8"), nil); progress.ErrorCode(err) != "playlist_export_write" {
		t.Fatal(err)
	}
	large := []byte(strings.Repeat("music.flac\n", 20000))
	if err := publishExport(context.Background(), path, large); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != string(large) {
		t.Fatalf("chunked output: %d %v", len(raw), err)
	}
}

func TestExportDestinationAndEmptyPlaylist(t *testing.T) {
	d, root, opts := exportFixture(t)
	for _, path := range []string{"", filepath.Join(root, "missing", "set.m3u8"), filepath.Join(root, "first.flac")} {
		changed := opts
		changed.Path = path
		if _, err := PreviewExport(context.Background(), d, root, changed); err == nil {
			t.Fatalf("invalid destination %q accepted", path)
		}
	}
	opts.Identifier = "999"
	if _, err := PreviewExport(context.Background(), d, root, opts); progress.ErrorCode(err) != "playlist_export_target" {
		t.Fatal(err)
	}
	opts.Identifier = "invalid"
	if _, err := PreviewExport(context.Background(), d, root, opts); err == nil {
		t.Fatal("invalid identity accepted")
	}
	opts.Identifier = "1"
	if _, err := d.Exec("DELETE FROM PlaylistEntity"); err != nil {
		t.Fatal(err)
	}
	plan, err := PreviewExport(context.Background(), d, root, opts)
	if err != nil || !plan.CanApply || len(plan.Entries) != 0 {
		t.Fatalf("empty review: %+v %v", plan, err)
	}
	opts.PlanToken = plan.PlanToken
	if _, err := ApplyExport(context.Background(), d, root, opts); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(opts.Path); err != nil || string(raw) != "#EXTM3U\n" {
		t.Fatalf("empty export: %q %v", raw, err)
	}
}

func TestExportExplicitRelativePathsAndInvalidPath(t *testing.T) {
	for _, name := range []string{"#track.flac", "spotify:track.flac", "percent%track.flac", "bad\ntrack.flac", "trailing.flac "} {
		t.Run(name, func(t *testing.T) {
			d, root, opts := exportFixture(t)
			if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec("UPDATE Track SET path=?,title='',artist='' WHERE id=2", "./"+name); err != nil {
				t.Fatal(err)
			}
			plan, err := PreviewExport(context.Background(), d, root, opts)
			if strings.Contains(name, "\n") || strings.HasSuffix(name, " ") {
				if progress.ErrorCode(err) != "playlist_export_format" {
					t.Fatalf("unsafe path accepted: %+v %v", plan, err)
				}
				return
			}
			if err != nil || plan.Entries[0].Status != "ready" {
				t.Fatalf("valid explicit path: %+v %v", plan, err)
			}
			resolved, err := importPath(root, plan.Entries[0].Path)
			if err != nil || resolved != filepath.Join(root, name) {
				t.Fatalf("relative round trip: %q %v", resolved, err)
			}
		})
	}
}

func TestExportFailedCollectionReadDoesNotProducePartialOutput(t *testing.T) {
	d, root, opts := exportFixture(t)
	if _, err := d.Exec("DROP TABLE Track"); err != nil {
		t.Fatal(err)
	}
	if plan, err := PreviewExport(context.Background(), d, root, opts); err == nil || plan != nil {
		t.Fatalf("partial review after failed query: %+v %v", plan, err)
	}
}

func TestExportFailureCleanupPreservesExternalReplacement(t *testing.T) {
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := root.OpenFile("set.m3u8", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	created, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Rename("set.m3u8", "original.m3u8"); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("set.m3u8", []byte("external replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	removeOwnedExport(root, "set.m3u8", created)
	if raw, err := root.ReadFile("set.m3u8"); err != nil || string(raw) != "external replacement" {
		t.Fatalf("cleanup removed replacement: %q %v", raw, err)
	}
	removeOwnedExport(root, "original.m3u8", created)
	if _, err := root.Stat("original.m3u8"); !os.IsNotExist(err) {
		t.Fatalf("owned failed output survived cleanup: %v", err)
	}
}

func TestExportRejectsUnsafeReviewPathsEvenWhenUnavailable(t *testing.T) {
	d, root, opts := exportFixture(t)
	opts.Path = filepath.Join(root, "bad\nset.m3u8")
	if _, err := PreviewExport(context.Background(), d, root, opts); progress.ErrorCode(err) != "playlist_export_format" {
		t.Fatalf("unsafe destination accepted: %v", err)
	}
	opts.Path = filepath.Join(root, "safe-export.m3u8")
	if _, err := d.Exec("UPDATE Track SET path=?,isAvailable=0 WHERE id=2", "./missing\ntrack.flac"); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewExport(context.Background(), d, root, opts); progress.ErrorCode(err) != "playlist_export_format" {
		t.Fatalf("unsafe unavailable path accepted: %v", err)
	}
}
