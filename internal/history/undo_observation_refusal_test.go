//go:build linux || darwin

package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Unix ENOTDIR distinguishes an obstructed parent from an absent database.
// Windows can classify this shape as not-exist, so it needs a different fixture.
func TestUndoRefusesUnobservableDatabaseParent(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		for _, force := range []bool{false, true} {
			t.Run(map[bool]string{false: "apply", true: "preview"}[dryRun]+map[bool]string{false: "/checked", true: "/force"}[force], func(t *testing.T) {
				lib, d, media := fixture(t)
				entry, err := Run(t.Context(), lib, "track.edit", ScopeCollection, nil, func(context.Context) error {
					_, err := d.Exec("UPDATE Track SET title='Changed'")
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := d.Close(); err != nil {
					t.Fatal(err)
				}
				parent := filepath.Join(lib, "Database2")
				retained := filepath.Join(lib, "retained-databases")
				if err := os.Rename(parent, retained); err != nil {
					t.Fatal(err)
				}
				const obstruction = "retain this ordinary file"
				if err := os.WriteFile(parent, []byte(obstruction), 0600); err != nil {
					t.Fatal(err)
				}
				_, nativeErr := os.Stat(filepath.Join(parent, "m.db"))
				if nativeErr == nil || os.IsNotExist(nativeErr) {
					t.Fatalf("fixture did not produce a distinct native observation refusal: %v", nativeErr)
				}
				var pathErr *os.PathError
				if !errors.As(nativeErr, &pathErr) {
					t.Fatalf("native Stat omitted path cause: %v", nativeErr)
				}
				prepared := false
				result, err := Undo(t.Context(), lib, entry.ID, UndoOptions{DryRun: dryRun, Force: force, Prepare: func(context.Context) error {
					prepared = true
					return nil
				}})
				if result != nil || !errors.Is(err, pathErr.Err) || prepared {
					t.Fatalf("unobservable database reached Undo publication: result=%+v error=%v prepared=%t native=%v", result, err, prepared, nativeErr)
				}
				if data, err := os.ReadFile(parent); err != nil || string(data) != obstruction {
					t.Fatalf("refused Undo changed obstruction: %q %v", data, err)
				}
				if data, err := os.ReadFile(media); err != nil || string(data) != "original audio" {
					t.Fatalf("refused Undo changed media: %q %v", data, err)
				}
				all, err := List(lib)
				if err != nil || len(all) != 1 || all[0].ID != entry.ID {
					t.Fatalf("refusal changed history: %+v %v", all, err)
				}
				if err := os.Remove(parent); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(retained, parent); err != nil {
					t.Fatal(err)
				}
				if restored, err := Undo(t.Context(), lib, entry.ID, UndoOptions{}); err != nil || restored == nil || restored.UndoOf != entry.ID {
					t.Fatalf("refused Undo leaked ownership or damaged its target: %+v %v", restored, err)
				}
			})
		}
	}
}
