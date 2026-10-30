package playlist

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func importFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if _, err := db.CreateLibrary(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, path := range []string{"zéro.flac", "first.flac"} {
		if err := os.WriteFile(filepath.Join(root, path), nil, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec("INSERT INTO Track(path,title) VALUES(?,?)", path, path); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(root, "set.m3u8")
	if err := os.WriteFile(source, []byte("\xef\xbb\xbf#EXTM3U\r\n#EXTINF:12,ignored title\r\nfirst.flac\r\nzéro.flac\r\nfirst.flac\r\nmissing.flac\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, source
}

func TestImportReviewAndAtomicOrderedApply(t *testing.T) {
	root, source := importFixture(t)
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	opts := ImportOptions{Path: source, Name: "Reviewed set"}
	plan, err := PreviewImport(context.Background(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CanApply || plan.DuplicateCount != 1 || plan.UnresolvedCount != 1 || !reflect.DeepEqual(plan.TrackIDs, []int64{2, 1}) {
		t.Fatalf("unexpected review: %+v", plan)
	}
	opts.PlanToken = plan.PlanToken
	if _, err := ApplyImport(context.Background(), d, root, opts); err == nil {
		t.Fatal("unresolved media was silently skipped")
	}
	opts.SkipMissing = true
	plan, err = PreviewImport(context.Background(), d, root, opts)
	if err != nil || !plan.CanApply {
		t.Fatalf("explicit skip review: %+v %v", plan, err)
	}
	opts.PlanToken = plan.PlanToken
	result, err := ApplyImport(context.Background(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := db.GetPlaylistTracks(context.Background(), d, result.PlaylistID)
	if err != nil || len(tracks) != 2 || tracks[0].ID != 2 || tracks[1].ID != 1 {
		t.Fatalf("order: %+v %v", tracks, err)
	}
	var next int64
	if err := d.QueryRow("SELECT nextEntityId FROM PlaylistEntity WHERE listId=? ORDER BY id LIMIT 1", result.PlaylistID).Scan(&next); err != nil || next == 0 {
		t.Fatalf("broken native membership chain: %d %v", next, err)
	}
	if _, err := ApplyImport(context.Background(), d, root, opts); err == nil {
		t.Fatal("replayed plan accepted")
	}
}

func TestImportChangedSourceAndTransactionalFailure(t *testing.T) {
	for _, mode := range []string{"changed source", "failed insert", "failed order update", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			root, source := importFixture(t)
			d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			opts := ImportOptions{Path: source, Name: "Atomic", SkipMissing: true}
			plan, err := PreviewImport(context.Background(), d, root, opts)
			if err != nil {
				t.Fatal(err)
			}
			opts.PlanToken = plan.PlanToken
			ctx := context.Background()
			switch mode {
			case "changed source":
				if err := os.WriteFile(source, []byte("zéro.flac\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "failed insert":
				if _, err := d.Exec("CREATE TRIGGER fail_import BEFORE INSERT ON PlaylistEntity WHEN NEW.trackId=1 BEGIN SELECT RAISE(ABORT,'fixture failure'); END"); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "failed order update":
				if _, err := d.Exec("CREATE TRIGGER fail_import_order BEFORE UPDATE OF nextEntityId ON PlaylistEntity BEGIN SELECT RAISE(ABORT,'fixture order failure'); END"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ApplyImport(ctx, d, root, opts); err == nil {
				t.Fatal("invalid import succeeded")
			}
			var count int
			if err := d.QueryRow("SELECT COUNT(*) FROM Playlist").Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial playlist survived: %d %v", count, err)
			}
		})
	}
}

func TestImportEncodingAndPaths(t *testing.T) {
	for _, tt := range []struct{ name, extension, encoding, text, code string }{
		{"legacy explicit", ".m3u", "windows-1252", "z\xe9ro.flac\n", ""},
		{"invalid utf8", ".m3u8", "", "z\xe9ro.flac\n", "playlist_import_encoding"},
		{"m3u8 legacy refused", ".m3u8", "windows-1252", "first.flac\n", "playlist_import_encoding"},
		{"hls refused", ".m3u8", "", "#EXTM3U\n#EXT-X-TARGETDURATION:10\nfirst.flac", "playlist_import_format"},
		{"remote refused", ".m3u", "", "https://example.com/music.flac", "playlist_import_format"},
		{"local uri", ".m3u", "", "", ""},
		{"latin1 explicit", ".m3u", "iso-8859-1", "z\xe9ro.flac\n", ""},
		{"unsupported encoding", ".m3u", "utf-16", "first.flac", "playlist_import_encoding"},
		{"empty", ".m3u", "", "#EXTM3U\n\n", "playlist_import_empty"},
		{"wrong extension", ".txt", "", "first.flac", "playlist_import_format"},
		{"opaque uri", ".m3u", "", "file:first.flac", "playlist_import_format"},
		{"opaque remote uri", ".m3u", "", "spotify:track:123", "playlist_import_format"},
		{"opaque http uri", ".m3u", "", "https:track.mp3", "playlist_import_format"},
		{"remote file uri", ".m3u", "", "file://foreign/music.flac", "playlist_import_format"},
		{"uri query", ".m3u", "", "file:///music.flac?download=1", "playlist_import_format"},
		{"control character", ".m3u", "", "first\x00.flac", "playlist_import_format"},
		{"encoded control character", ".m3u", "", "file:///first%0a.flac", "playlist_import_format"},
		{"long line", ".m3u", "", strings.Repeat("a", maxImportLine), "playlist_import_format"},
		{"many entries", ".m3u", "", strings.Repeat("first.flac\n", maxImportEntries+1), "playlist_import_format"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root, _ := importFixture(t)
			source := filepath.Join(root, "other"+tt.extension)
			text := tt.text
			if tt.name == "local uri" {
				text = "file://" + filepath.ToSlash(filepath.Join(root, "zéro.flac")) + "\n"
			}
			if err := os.WriteFile(source, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			plan, err := PreviewImport(context.Background(), d, root, ImportOptions{Path: source, Name: "Encoding", Encoding: tt.encoding})
			if tt.code != "" {
				if err == nil || progress.ErrorCode(err) != tt.code {
					t.Fatalf("want %s got %v", tt.code, err)
				}
				return
			}
			if err != nil || len(plan.TrackIDs) != 1 || plan.TrackIDs[0] != 1 {
				t.Fatalf("path decode: %+v %v", plan, err)
			}
		})
	}
}

func TestImportNativeURIClassification(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "song.flac")
	got, err := importPath(root, "FILE:"+filepath.ToSlash(local))
	if err != nil || got != local {
		t.Fatalf("case-insensitive local URI: %q %v", got, err)
	}
	got, err = importPath(root, "./spotify:track:123")
	if err != nil || got != filepath.Join(root, "spotify:track:123") {
		t.Fatalf("explicit relative colon filename: %q %v", got, err)
	}
}

func TestImportResolutionAndPlanOwnership(t *testing.T) {
	root, source := importFixture(t)
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := os.WriteFile(filepath.Join(root, "unknown.flac"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO Track(path,title) VALUES('./first.flac','Ambiguous copy')"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("first.flac\nunknown.flac\nzéro.flac\n"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := ImportOptions{Path: source, Name: "Reviewed", SkipMissing: true}
	plan, err := PreviewImport(context.Background(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Entries[0].Status != "ambiguous" || plan.Entries[1].Status != "unregistered" || !reflect.DeepEqual(plan.TrackIDs, []int64{1}) || !plan.CanApply {
		t.Fatalf("resolution: %+v", plan)
	}
	opts.PlanToken = plan.PlanToken
	for _, change := range []ImportOptions{
		{Path: source, Name: "Different", SkipMissing: true, PlanToken: opts.PlanToken},
		{Path: source, Name: opts.Name, SkipMissing: false, PlanToken: opts.PlanToken},
	} {
		if _, err := CheckImport(context.Background(), d, root, change); progress.ErrorCode(err) != "playlist_import_stale" {
			t.Fatalf("changed options accepted: %v", err)
		}
	}
	if _, err := d.Exec("UPDATE Track SET originTrackId=123 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyImport(context.Background(), d, root, opts); progress.ErrorCode(err) != "playlist_import_stale" {
		t.Fatalf("changed identity accepted: %v", err)
	}
	if _, err := PreviewImport(context.Background(), d, root, ImportOptions{Path: source, Name: " "}); progress.ErrorCode(err) != "playlist_import_target" {
		t.Fatalf("empty name: %v", err)
	}
	for _, parent := range []string{"-1", "missing", "999"} {
		if _, err := PreviewImport(context.Background(), d, root, ImportOptions{Path: source, Name: "Child", Parent: parent}); progress.ErrorCode(err) != "playlist_import_target" {
			t.Fatalf("invalid parent %s: %v", parent, err)
		}
	}
	parent, err := db.CreatePlaylist(context.Background(), d, "Folder", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	opts.Parent = fmt.Sprint(parent)
	plan, err = PreviewImport(context.Background(), d, root, opts)
	if err != nil || plan.ParentID != parent {
		t.Fatalf("child review: %+v %v", plan, err)
	}
	opts.PlanToken = plan.PlanToken
	result, err := ApplyImport(context.Background(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	var parentID int64
	if err := d.QueryRow("SELECT parentListId FROM Playlist WHERE id=?", result.PlaylistID).Scan(&parentID); err != nil || parentID != parent {
		t.Fatalf("child destination: %d %v", parentID, err)
	}
}

func TestImportUnavailableSourceAndBounds(t *testing.T) {
	root, _ := importFixture(t)
	for _, mode := range []string{"missing", "directory", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.m3u")
			want := "playlist_import_format"
			switch mode {
			case "missing":
				want = "playlist_import_unavailable"
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(path, make([]byte, maxImportBytes+1), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := readImport(context.Background(), ImportOptions{Path: path}); progress.ErrorCode(err) != want {
				t.Fatalf("want %s: %v", want, err)
			}
		})
	}
	if _, err := importPath(root, "C:\\music\\song.flac"); runtime.GOOS != "windows" && progress.ErrorCode(err) != "playlist_import_format" {
		t.Fatal(err)
	}
	if _, err := importPath(root, "music\\song.flac"); runtime.GOOS != "windows" && progress.ErrorCode(err) != "playlist_import_format" {
		t.Fatal(err)
	}
}

func TestImportOpaqueParentAndUnavailableCollection(t *testing.T) {
	ctx := context.Background()
	root, source := importFixture(t)
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	parent, err := db.CreatePlaylist(ctx, d, "Parent", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Register(ctx, d, root, false); err != nil {
		t.Fatal(err)
	}
	items, err := identity.List(ctx, d, root)
	if err != nil {
		t.Fatal(err)
	}
	var reference string
	for _, item := range items {
		if item.Kind == "playlist" && item.ID == parent {
			reference = item.Ref
		}
	}
	if reference == "" {
		t.Fatal("parent has no registered identity")
	}
	opts := ImportOptions{Path: source, Name: "Imported child", Parent: reference, SkipMissing: true}
	plan, err := PreviewImport(ctx, d, root, opts)
	if err != nil || plan.ParentID != parent {
		t.Fatalf("opaque parent preview: %+v %v", plan, err)
	}
	opts.PlanToken = plan.PlanToken
	result, err := ApplyImport(ctx, d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	var actualParent int64
	if err := d.QueryRow("SELECT parentListId FROM Playlist WHERE id=?", result.PlaylistID).Scan(&actualParent); err != nil || actualParent != parent {
		t.Fatalf("opaque parent apply: %d %v", actualParent, err)
	}
	opts.Parent = "eng:missing:playlist:missing"
	if _, err := PreviewImport(ctx, d, root, opts); err == nil {
		t.Fatal("invalid opaque parent accepted in preview")
	}
	if _, err := ApplyImport(ctx, d, root, opts); err == nil {
		t.Fatal("invalid opaque parent accepted in apply")
	}
	opts.Parent = ""
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewImport(ctx, d, root, opts); err == nil {
		t.Fatal("closed collection preview succeeded")
	}
	if _, err := CheckImport(ctx, d, root, opts); err == nil {
		t.Fatal("closed collection validation succeeded")
	}
	if _, err := ApplyImport(ctx, d, root, opts); err == nil {
		t.Fatal("closed collection apply succeeded")
	}
}
