package transfer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intakelease"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func savedRename(t *testing.T, lib string, database *sql.DB, uuid, title string) {
	t.Helper()
	execSQL(t, database, `INSERT INTO Playlist(id,title,parentListId,nextListId,isPersisted) VALUES(1,'Original',0,0,1)`)
	execSQL(t, database, `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES(?,?,'Original;','','',?)`, uuid, title, []byte{0, 255, 10})
	if _, err := history.Run(t.Context(), lib, "playlist.rename", history.ScopeCollection, nil, func(ctx context.Context) error {
		_, err := database.ExecContext(ctx, `UPDATE Playlist SET title=? WHERE id=1`, title)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func syncFileManifest(t *testing.T, path string) map[string][32]byte {
	t.Helper()
	files := map[string][32]byte{}
	if err := filepath.WalkDir(path, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = sha256.Sum256(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestSmartlistSyncProjectionPreservesSourceAndDestinationUndo(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	savedRename(t, source, s, "source-saved", "Source")
	savedRename(t, destination, d, "dest-saved", "Destination")
	before := syncFileManifest(t, source)
	o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, DryRun: true, NoBackup: true}
	preview, err := Run(t.Context(), o)
	if err != nil || !preview.Applicable || preview.Summary.SourceSmartlistsReconnected != 1 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	o.DryRun, o.PlanToken = false, preview.PlanToken
	applied, err := Run(t.Context(), o)
	if err != nil || applied.OperationID == "" {
		t.Fatalf("apply: %+v %v", applied, err)
	}
	for uuid, path := range map[string]string{"source-saved": "Source;", "dest-saved": "Destination;"} {
		var actual string
		var rules []byte
		if err := d.QueryRow(`SELECT parentPlaylistPath,rules FROM Smartlist WHERE listUuid=?`, uuid).Scan(&actual, &rules); err != nil || actual != path || !reflect.DeepEqual(rules, []byte{0, 255, 10}) {
			t.Fatalf("saved row %s: %q %x %v", uuid, actual, rules, err)
		}
	}
	if after := syncFileManifest(t, source); !reflect.DeepEqual(before, after) {
		t.Fatal("sync changed source files or enrolled source storage")
	}
	entries, err := history.List(destination)
	if err != nil {
		t.Fatal(err)
	}
	o.PlanToken = ""
	again, err := Run(t.Context(), o)
	if err != nil || again.OperationID != "" {
		t.Fatalf("source projection created repeat operation: %+v %v", again, err)
	}
	if now, err := history.List(destination); err != nil || len(now) != len(entries) {
		t.Fatalf("no-op enrolled history: %d %v", len(now), err)
	}
	if _, err := history.Undo(t.Context(), destination, applied.OperationID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var path string
	if err := d.QueryRow(`SELECT parentPlaylistPath FROM Smartlist WHERE listUuid='dest-saved'`).Scan(&path); err != nil || path != "Destination;" {
		t.Fatalf("sync Undo reversed prerequisite repair: %q %v", path, err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM Smartlist WHERE listUuid='source-saved'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("sync Undo retained copied row: %d %v", count, err)
	}
}

func TestSmartlistSyncStaleEvidenceRefusesBeforeRepairAndBackup(t *testing.T) {
	for _, changed := range []string{"source", "destination"} {
		t.Run(changed, func(t *testing.T) {
			source, s := library(t, "source")
			destination, d := library(t, "destination")
			savedRename(t, source, s, "source-saved", "Source")
			savedRename(t, destination, d, "dest-saved", "Destination")
			o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, DryRun: true}
			preview, err := Run(t.Context(), o)
			if err != nil {
				t.Fatal(err)
			}
			path := source
			if changed == "destination" {
				path = destination
			}
			entries, err := history.List(path)
			if err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(path, ".engine-cli", "history", entries[0].ID+".json")
			data, err := os.ReadFile(entry)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(entry, append(data, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			o.DryRun, o.PlanToken = false, preview.PlanToken
			r, err := Run(t.Context(), o)
			if err == nil || progress.ErrorCode(err) != "sync_preview_stale" || r == nil || r.OperationID != "" {
				t.Fatalf("stale accepted: %+v %v", r, err)
			}
			var raw string
			if err := d.QueryRow(`SELECT parentPlaylistPath FROM Smartlist WHERE listUuid='dest-saved'`).Scan(&raw); err != nil || raw != "Original;" {
				t.Fatalf("stale operation repaired destination: %q %v", raw, err)
			}
			if _, err := os.Stat(filepath.Join(destination, ".engine-cli", "backups")); !os.IsNotExist(err) {
				t.Fatalf("stale operation created backup: %v", err)
			}
		})
	}
}

func TestSyncSourceRecoveryCheckedBeforeDestinationOwner(t *testing.T) {
	source, _ := library(t, "source")
	destination, _ := library(t, "destination")
	_, err := history.Run(t.Context(), source, "interrupted", history.ScopeCollection, nil, func(ctx context.Context) error {
		return errors.Join(errors.New("interrupted"), history.RequireRecovery(ctx))
	})
	if err == nil {
		t.Fatal("fixture did not retain interrupted history")
	}
	owner, err := intakelease.Observe(t.Context(), destination)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	_, err = Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, NoBackup: true})
	if progress.ErrorCode(err) != "history_recovery_required" {
		t.Fatalf("destination owner preceded source observation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("source refusal enrolled destination history: %v", err)
	}
}
