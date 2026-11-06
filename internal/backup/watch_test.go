package backup

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
)

func TestWatchSnapshotRestoreUndoAndAbsenceFence(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "prewatch absence", true: "configured watch"}[present], func(t *testing.T) {
			library := createDummyEngineLibrary(t)
			root := t.TempDir()
			var queue *intake.Store
			var state intake.WatchState
			open := func() {
				var err error
				queue, err = intake.Open(t.Context(), library)
				if err != nil {
					t.Fatal(err)
				}
			}
			closeQueue := func() {
				if err := queue.Close(); err != nil {
					t.Fatal(err)
				}
			}
			add := func() {
				var err error
				state, err = queue.WatchAdd(t.Context(), intake.WatchAddOptions{Path: root, Recursive: true, Revision: state.Revision})
				if err != nil {
					t.Fatal(err)
				}
			}
			open()
			if present {
				add()
			}
			closeQueue()
			snapshot, err := CreateBackup(t.Context(), library, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			open()
			if !present {
				add()
			}
			stale := state.Revision
			state, err = queue.WatchSetEnabled(t.Context(), intake.WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision}, false)
			if err != nil {
				t.Fatal(err)
			}
			closeQueue()
			entry, err := history.Run(t.Context(), library, "library.restore", history.ScopeCollection, nil, func(ctx context.Context) error {
				return RestoreBackup(ctx, snapshot.Path, library)
			})
			if err != nil || entry == nil {
				t.Fatalf("restoring pair: %+v %v", entry, err)
			}
			check := func(roots int, enabled bool) {
				open()
				defer closeQueue()
				state, err = queue.WatchList(t.Context())
				if err != nil || !state.Suspended || state.Reason != "restored" || len(state.Roots) != roots || state.Revision <= stale {
					t.Fatalf("restored authority: %+v %v", state, err)
				}
				if roots != 0 && state.Roots[0].Enabled != enabled {
					t.Fatalf("restored root differs: %+v", state.Roots)
				}
				if _, err := queue.WatchAdd(t.Context(), intake.WatchAddOptions{Path: t.TempDir(), Revision: stale}); err == nil {
					t.Fatal("replacement reused stale authorization")
				}
				stale = state.Revision
			}
			if present {
				check(1, true)
			} else {
				check(0, false)
				if path, err := companion.Database(library, companion.WatchName); err != nil || path != "" {
					t.Fatalf("prewatch absence enrolled portable image: %q %v", path, err)
				}
			}
			undo, err := history.Undo(t.Context(), library, entry.ID, history.UndoOptions{})
			if err != nil || undo == nil {
				t.Fatalf("undo restored pair: %+v %v", undo, err)
			}
			check(1, false)
			if _, err := history.Undo(t.Context(), library, undo.ID, history.UndoOptions{}); err != nil {
				t.Fatal("undo of restore Undo:", err)
			}
			if present {
				check(1, true)
			} else {
				check(0, false)
			}
			for _, name := range []string{"watch-authority.db", "intake-state.db", "watch-owner.db"} {
				if _, err := os.Stat(filepath.Join(snapshot.Path, companionFolder, name)); !os.IsNotExist(err) {
					t.Fatalf("operational authority entered portable image: %s %v", name, err)
				}
			}
		})
	}
}

func TestWatchSnapshotRejectsMismatchedPairBeforeReplacement(t *testing.T) {
	for _, damage := range []string{"watch tuple", "queue tuple", "missing queue item", "watch tuple after planning"} {
		t.Run(damage, func(t *testing.T) {
			library := createDummyEngineLibrary(t)
			root := t.TempDir()
			source := filepath.Join(root, "incoming.wav")
			original := []byte("preserved native source version")
			if err := os.WriteFile(source, original, 0600); err != nil {
				t.Fatal(err)
			}
			service, err := intake.OpenService(t.Context(), library, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			if err := service.Ready(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := service.WatchAdd(t.Context(), intake.WatchAddOptions{Path: root}); err != nil {
				t.Fatal(err)
			}
			scan, err := service.WatchReconcile(t.Context())
			if err != nil || len(scan.State.Roots) != 1 || scan.State.Roots[0].Linked != 1 {
				t.Fatalf("public discovery did not create linked pair: %+v %v", scan, err)
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			snapshot, err := CreateBackup(t.Context(), library, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			restore := func() error { return RestoreBackup(t.Context(), snapshot.Path, library) }
			if damage == "watch tuple after planning" {
				sections, err := planRestore(snapshot.Path, library)
				if err != nil {
					t.Fatal(err)
				}
				plans, err := planCompanion(t.Context(), snapshot.Path)
				if err != nil {
					t.Fatal(err)
				}
				// Exercise the actual post-acquisition recheck with a formerly
				// valid selection; no scheduler timing claim is needed.
				restore = func() error {
					return intakestate.With(t.Context(), library, func(ctx context.Context) error {
						return restoreHeld(ctx, snapshot.Path, library, sections, plans)
					})
				}
			}
			image := filepath.Join(snapshot.Path, companionFolder, companion.WatchName)
			statement := "UPDATE WatchHandoff SET source_identity='different native version'"
			if damage == "queue tuple" || damage == "missing queue item" {
				image = filepath.Join(snapshot.Path, companionFolder, companion.IntakeName)
				statement = "UPDATE Item SET source_identity='different native version'"
				if damage == "missing queue item" {
					statement = "DELETE FROM Item"
				}
			}
			d, err := db.Open(image)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			paths := []string{filepath.Join(library, "Database2", "m.db"), filepath.Join(library, "Artwork", "sample_art.jpg"), filepath.Join(library, ".engine-cli", companion.IntakeName), filepath.Join(library, ".engine-cli", companion.WatchName), filepath.Join(library, ".engine-cli", "watch-authority.db"), source}
			before := make(map[string][sha256.Size]byte)
			for _, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				before[path] = sha256.Sum256(data)
			}
			if err := restore(); err == nil {
				t.Fatal("damaged pair accepted")
			}
			for _, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil || sha256.Sum256(data) != before[path] {
					t.Fatalf("pair refusal changed live state %s: %v", path, err)
				}
			}
		})
	}
}
