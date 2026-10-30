package playlist

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func exportFixture(t *testing.T) (*sql.DB, string, ExportOptions) {
	t.Helper()
	root, _ := importFixture(t)
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	id, err := db.CreatePlaylist(context.Background(), d, "Café set", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, track := range []int64{1, 2} {
		if _, err := db.AddTrackToPlaylist(context.Background(), d, id, track); err != nil {
			t.Fatal(err)
		}
	}
	// Saved order is the reverse of insertion order; entity IDs are not positions.
	if _, err := d.Exec("UPDATE PlaylistEntity SET nextEntityId=CASE id WHEN 2 THEN 1 ELSE 0 END WHERE listId=?", id); err != nil {
		t.Fatal(err)
	}
	return d, root, ExportOptions{Identifier: "1", Path: filepath.Join(root, "export.m3u8"), PathMode: "relative"}
}

func TestExportSavedOrderAndUnchangedSource(t *testing.T) {
	d, root, opts := exportFixture(t)
	plan, err := PreviewExport(context.Background(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.CanApply || plan.ExportedCount != 2 || plan.Entries[0].TrackID != 2 || plan.Entries[1].TrackID != 1 {
		t.Fatalf("saved membership order: %+v", plan)
	}
	opts.PlanToken = plan.PlanToken
	result, err := ApplyExport(context.Background(), d, root, opts)
	if err != nil || result.ExportedCount != 2 || result.SkippedCount != 0 {
		t.Fatalf("apply: %+v %v", result, err)
	}
	raw, err := os.ReadFile(opts.Path)
	if err != nil || string(raw) != "#EXTM3U\n#EXTINF:-1,first.flac\nfirst.flac\n#EXTINF:-1,zéro.flac\nzéro.flac\n" {
		t.Fatalf("serialization: %q %v", raw, err)
	}
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM PlaylistEntity").Scan(&count); err != nil || count != 2 {
		t.Fatalf("source changed: %d %v", count, err)
	}
	if _, err := ApplyExport(context.Background(), d, root, opts); progress.ErrorCode(err) != "playlist_export_exists" {
		t.Fatalf("overwrite: %v", err)
	}
}

func TestExportDistinctOriginsWithRepeatedMediaPaths(t *testing.T) {
	d, root, opts := exportFixture(t)
	if _, err := d.Exec("INSERT INTO Track(path,title,originDatabaseUuid,originTrackId) VALUES('./first.flac','Another origin','foreign',99)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTrackToPlaylist(context.Background(), d, 1, 3); err != nil {
		t.Fatal(err)
	}
	plan, err := PreviewExport(context.Background(), d, root, opts)
	if err != nil || plan.ExportedCount != 3 {
		t.Fatalf("origins lost: %+v %v", plan, err)
	}
	ids := []int64{}
	for _, entry := range plan.Entries {
		ids = append(ids, entry.TrackID)
	}
	if !reflect.DeepEqual(ids, []int64{2, 1, 3}) {
		t.Fatal(ids)
	}
}

func TestExportMalformedSavedLinksRefused(t *testing.T) {
	for _, statement := range []string{
		"UPDATE PlaylistEntity SET nextEntityId=999 WHERE id=1",
		"UPDATE PlaylistEntity SET nextEntityId=2 WHERE id=1",
		"UPDATE PlaylistEntity SET nextEntityId=0",
		"UPDATE PlaylistEntity SET nextEntityId=1",
	} {
		t.Run(statement, func(t *testing.T) {
			d, root, opts := exportFixture(t)
			if _, err := d.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if _, err := PreviewExport(context.Background(), d, root, opts); progress.ErrorCode(err) != "playlist_export_order" {
				t.Fatalf("invalid saved order accepted: %v", err)
			}
		})
	}
}

func TestExportUnavailableRequiresReviewedPolicy(t *testing.T) {
	for _, mode := range []string{"missing file", "unavailable flag", "missing origin", "streaming", "no path", "directory"} {
		t.Run(mode, func(t *testing.T) {
			d, root, opts := exportFixture(t)
			statement := ""
			switch mode {
			case "missing file":
				if err := os.Remove(filepath.Join(root, "first.flac")); err != nil {
					t.Fatal(err)
				}
			case "unavailable flag":
				statement = "UPDATE Track SET isAvailable=0 WHERE id=2"
			case "missing origin":
				statement = "UPDATE PlaylistEntity SET databaseUuid='lost' WHERE trackId=2"
			case "streaming":
				statement = "UPDATE Track SET path='https://example.com/song.flac' WHERE id=2"
			case "no path":
				statement = "UPDATE Track SET path=NULL WHERE id=2"
			case "directory":
				statement = "UPDATE Track SET path='Database2' WHERE id=2"
			}
			if statement != "" {
				if _, err := d.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := PreviewExport(context.Background(), d, root, opts)
			if err != nil || plan.CanApply || plan.UnavailableCount != 1 || len(plan.Entries) != 2 {
				t.Fatalf("unavailable: %+v %v", plan, err)
			}
			opts.PlanToken = plan.PlanToken
			if _, err := ApplyExport(context.Background(), d, root, opts); progress.ErrorCode(err) != "playlist_export_unavailable" {
				t.Fatal(err)
			}
			opts.SkipUnavailable = true
			if _, err := ApplyExport(context.Background(), d, root, opts); progress.ErrorCode(err) != "playlist_export_stale" {
				t.Fatalf("unreviewed omission accepted: %v", err)
			}
			plan, err = PreviewExport(context.Background(), d, root, opts)
			if err != nil {
				t.Fatal(err)
			}
			opts.PlanToken = plan.PlanToken
			result, err := ApplyExport(context.Background(), d, root, opts)
			if err != nil || result.ExportedCount != 1 || result.SkippedCount != 1 {
				t.Fatalf("explicit omission: %+v %v", result, err)
			}
		})
	}
}

func TestExportReviewBindsSourceAndOptions(t *testing.T) {
	for _, mode := range []string{"metadata", "order", "origin", "option", "token", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			d, root, opts := exportFixture(t)
			plan, err := PreviewExport(context.Background(), d, root, opts)
			if err != nil {
				t.Fatal(err)
			}
			opts.PlanToken = plan.PlanToken
			ctx := context.Background()
			statement := ""
			switch mode {
			case "metadata":
				statement = "UPDATE Track SET title='Changed' WHERE id=1"
			case "order":
				statement = "UPDATE PlaylistEntity SET nextEntityId=CASE id WHEN 1 THEN 2 ELSE 0 END"
			case "origin":
				statement = "UPDATE PlaylistEntity SET databaseUuid='lost' WHERE id=1"
			case "option":
				opts.PathMode = exportAbsolute
			case "token":
				opts.PlanToken = ""
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if statement != "" {
				if _, err := d.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ApplyExport(ctx, d, root, opts); err == nil {
				t.Fatal("changed plan accepted")
			}
			if _, err := os.Stat(opts.Path); !os.IsNotExist(err) {
				t.Fatalf("rejected review created output: %v", err)
			}
		})
	}
}

func TestExportEncodingAndRepresentation(t *testing.T) {
	for _, tt := range []struct{ name, encoding, pathMode, title, extension, code string }{
		{"utf8", "", "", "東京", ".m3u8", ""},
		{"windows", "windows-1252", "relative", "Café", ".m3u", ""},
		{"latin1", "iso-8859-1", "relative", "Café", ".m3u", ""},
		{"unrepresentable", "windows-1252", "", "東京", ".m3u", "playlist_export_encoding"},
		{"m3u8 utf8 only", "windows-1252", "", "Café", ".m3u8", "playlist_export_encoding"},
		{"unknown encoding", "utf-16", "", "Café", ".m3u", "playlist_export_encoding"},
		{"unknown path mode", "", "volume", "Café", ".m3u", "playlist_export_format"},
		{"wrong extension", "", "", "Café", ".txt", "playlist_export_format"},
		{"metadata newlines", "", "", "New\r\n#INJECT\x00 title", ".m3u8", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, root, opts := exportFixture(t)
			opts.Path = filepath.Join(root, "other"+tt.extension)
			opts.Encoding = tt.encoding
			opts.PathMode = tt.pathMode
			if _, err := d.Exec("UPDATE Track SET title=?,artist='Artist',length=123 WHERE id=1", tt.title); err != nil {
				t.Fatal(err)
			}
			plan, err := PreviewExport(context.Background(), d, root, opts)
			if tt.code != "" {
				if progress.ErrorCode(err) != tt.code {
					t.Fatalf("want %s: %v", tt.code, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			opts.PlanToken = plan.PlanToken
			if _, err := ApplyExport(context.Background(), d, root, opts); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(opts.Path)
			if err != nil {
				t.Fatal(err)
			}
			if tt.name == "metadata newlines" && (strings.Contains(string(raw), "\n#INJECT") || strings.ContainsRune(string(raw), '\x00')) {
				t.Fatalf("metadata injected records: %q", raw)
			}
			if tt.name == "windows" || tt.name == "latin1" {
				if !strings.Contains(string(raw), "Caf\xe9") {
					t.Fatalf("encoding: %q", raw)
				}
			}
		})
	}
}
