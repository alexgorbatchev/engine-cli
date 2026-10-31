package history

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"zombiezen.com/go/sqlite"
)

func TestSmartlistPreparedMutationRepairsBeforeBackupAndSnapshot(t *testing.T) {
	for _, shape := range []string{"healthy", "unknown", "reconnect"} {
		t.Run(shape, func(t *testing.T) {
			lib, database := smartlistHistoryLibrary(t)
			wantPath, wantCount, wantEntries := "House;", 1, 1
			switch shape {
			case "unknown":
				if _, err := database.Exec(`UPDATE Smartlist SET parentPlaylistPath='Missing;'`); err != nil {
					t.Fatal(err)
				}
				wantCount = 0
			case "reconnect":
				recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
				wantPath, wantEntries = "Renamed;", 3
			}
			check := func(ctx context.Context, database *sql.DB) error {
				var count int
				if err := database.QueryRowContext(ctx, `SELECT count(*) FROM Smartlist`).Scan(&count); err != nil {
					return err
				}
				if count != wantCount {
					t.Errorf("repair count before ordinary operation: got %d want %d", count, wantCount)
				}
				if count != 0 {
					var path, rules string
					if err := database.QueryRowContext(ctx, `SELECT parentPlaylistPath,rules FROM Smartlist WHERE listUuid='saved'`).Scan(&path, &rules); err != nil {
						return err
					}
					if path != wantPath || rules != "opaque" {
						t.Errorf("repair did not preserve expected row: path=%q rules=%q", path, rules)
					}
				}
				return nil
			}
			prepared := false
			options := RunOptions{Operation: "playlist.rename", Scope: ScopeCollection, RepairSmartlists: true,
				Prepare: func(ctx context.Context) error {
					prepared = true
					if err := check(ctx, database); err != nil {
						return err
					}
					dir, err := storeDir(lib)
					if err != nil {
						return err
					}
					owner, err := lock(ctx, dir)
					if err == nil {
						return errors.Join(errors.New("publisher entered between repair and backup"), owner.Close())
					}
					if sqlite.ErrCode(err) != sqlite.ResultBusy {
						return err
					}
					return nil
				}}
			entry, err := RunPrepared(t.Context(), lib, options, func(ctx context.Context) error {
				if !prepared {
					t.Error("ordinary operation ran before preparation")
				}
				_, err := database.ExecContext(ctx, `UPDATE Playlist SET title='Edited' WHERE id=2`)
				return err
			})
			if err != nil || entry == nil {
				t.Fatalf("protected operation: %+v %v", entry, err)
			}
			image, err := db.Open(entry.Before.Databases[filepath.Join(lib, "Database2", "m.db")].Path, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := errors.Join(check(t.Context(), image), image.Close()); err != nil {
				t.Fatal(err)
			}
			entries, err := List(lib)
			if err != nil || len(entries) != wantEntries {
				t.Fatalf("repair/ordinary undo ownership: entries=%d want=%d %v", len(entries), wantEntries, err)
			}
			if _, err := Undo(t.Context(), lib, entry.ID, UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := check(t.Context(), database); err != nil {
				t.Fatal(err)
			}
			var title string
			if err := database.QueryRow(`SELECT title FROM Playlist WHERE id=2`).Scan(&title); err != nil || title != "Sets" {
				t.Fatalf("ordinary Undo did not restore only requested change: %q %v", title, err)
			}
			if shape == "healthy" {
				if _, err := os.Lstat(filepath.Join(lib, ".engine-cli", "smartlist-repair")); !os.IsNotExist(err) {
					t.Fatalf("healthy ordinary operation enrolled repair: %v", err)
				}
			}
		})
	}
}

func TestSmartlistPreparedMutationRefusesPendingBeforeBackup(t *testing.T) {
	lib, _, _ := fixture(t)
	active := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	if err := os.MkdirAll(active, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "pending.json"), []byte("pending"), 0600); err != nil {
		t.Fatal(err)
	}
	options := RunOptions{Operation: "track.update", Scope: ScopeCollection, RepairSmartlists: true,
		Prepare: func(context.Context) error { t.Error("backup ran before pending refusal"); return nil }}
	entry, err := RunPrepared(t.Context(), lib, options, func(context.Context) error {
		t.Error("ordinary mutation bypassed pending repair")
		return nil
	})
	if progress.ErrorCode(err) != "smartlist_repair_pending" || entry != nil {
		t.Fatalf("pending operation: %+v %v", entry, err)
	}
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := entries(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("pending operation captured history: %+v %v", entries, err)
	}
}

func TestSmartlistPreparedMutationRejectsAliasedStorage(t *testing.T) {
	for _, name := range []string{".engine-cli", ".engine-cli/history"} {
		t.Run(name, func(t *testing.T) {
			lib, _ := smartlistHistoryLibrary(t)
			outside := t.TempDir()
			alias := filepath.Join(lib, filepath.FromSlash(name))
			if name != ".engine-cli" {
				if err := os.Mkdir(filepath.Dir(alias), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(outside, alias); err != nil {
				t.Skipf("native symlink unavailable: %v", err)
			}
			options := RunOptions{Operation: "track.update", Scope: ScopeCollection, RepairSmartlists: true,
				Prepare: func(context.Context) error { t.Error("backup followed aliased history storage"); return nil }}
			entry, err := RunPrepared(t.Context(), lib, options, func(context.Context) error {
				t.Error("mutation followed aliased history storage")
				return nil
			})
			if err == nil || entry != nil {
				t.Fatalf("aliased storage accepted: %+v %v", entry, err)
			}
			contents, err := os.ReadDir(outside)
			if err != nil || len(contents) != 0 {
				t.Fatalf("aliased storage was enrolled: entries=%d error=%v", len(contents), err)
			}
		})
	}
}
