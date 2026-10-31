package verify

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

func membershipLibrary(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.InitSchema(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "track.wav"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO Track(id,path,originTrackId,originDatabaseUuid) VALUES(10,'track.wav',10,'fixture')`,
		`INSERT INTO Playlist(id,title,parentListId) VALUES(1,'Set',0)`,
		`INSERT INTO PlaylistEntity(id,listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,10,'fixture',0)`,
	} {
		if _, err := d.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return d, dir
}

func TestVerifyLibraryRejectsIncompleteMembershipReads(t *testing.T) {
	for _, tc := range []struct {
		name string
		sql  string
	}{
		{"cyclic chain", `UPDATE PlaylistEntity SET nextEntityId=id`},
		{"missing membership table", `DROP TABLE PlaylistEntity`},
		{"missing playlist table", `DROP TABLE Playlist`},
		{"missing artwork table", `DROP TABLE AlbumArt`},
		{"unreadable track identity", `UPDATE PlaylistEntity SET trackId='invalid'`},
		{"unreadable playlist identity", `UPDATE PlaylistEntity SET listId='invalid'`},
		{"unreadable order", `ALTER TABLE PlaylistEntity RENAME COLUMN nextEntityId TO unreadableNext`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, dir := membershipLibrary(t)
			if _, err := d.Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`PRAGMA query_only=ON`); err != nil {
				t.Fatal(err)
			}
			report, err := VerifyLibrary(context.Background(), d, dir)
			if err == nil || report != nil {
				t.Fatalf("incomplete verification returned report=%+v error=%v", report, err)
			}
			if code := progress.ErrorCode(err); code != "library_verify_incomplete" {
				t.Fatalf("failure code=%q, want library_verify_incomplete", code)
			}
		})
	}
}

func TestVerifyLibraryUsesNativeMembershipOrigins(t *testing.T) {
	for _, tc := range []struct {
		name     string
		trackID  int64
		uuid     string
		orphaned int
	}{
		{"origin differs from local ID", 101, "imported-library", 0},
		{"same ID from a different library", 10, "different-library", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, dir := membershipLibrary(t)
			if _, err := d.Exec(`UPDATE Track SET originTrackId=101,originDatabaseUuid='imported-library'`); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`UPDATE PlaylistEntity SET trackId=?,databaseUuid=?`, tc.trackID, tc.uuid); err != nil {
				t.Fatal(err)
			}
			report, err := VerifyLibrary(context.Background(), d, dir)
			if err != nil {
				t.Fatal(err)
			}
			if report.OrphanedCount != tc.orphaned || report.IsHealthy != (tc.orphaned == 0) {
				t.Fatalf("incorrect origin resolution: %+v", report)
			}
		})
	}
}

func TestVerifyLibraryMembershipCancellation(t *testing.T) {
	d, dir := membershipLibrary(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = progress.WithReporter(ctx, func(event progress.Event) {
		if event.Phase == "verify.memberships" {
			cancel()
		}
	})
	report, err := VerifyLibrary(ctx, d, dir)
	if !errors.Is(err, context.Canceled) || report != nil {
		t.Fatalf("membership cancellation returned report=%+v error=%v", report, err)
	}
}

func TestVerifyLibraryReadCancellation(t *testing.T) {
	for _, phase := range []string{"verify", "verify.tracks", "verify.playlists"} {
		t.Run(phase, func(t *testing.T) {
			d, dir := membershipLibrary(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = progress.WithReporter(ctx, func(event progress.Event) {
				if event.Phase == phase {
					cancel()
				}
			})
			report, err := VerifyLibrary(ctx, d, dir)
			if report != nil || !errors.Is(err, context.Canceled) || progress.ErrorCode(err) != "cancelled" {
				t.Fatalf("read cancellation returned report=%+v error=%v code=%q", report, err, progress.ErrorCode(err))
			}
		})
	}
}
