package history

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedOperationsRetainOnlyRecoverableChanges(t *testing.T) {
	refusal := errors.New("review refused")
	for _, name := range []string{"unchanged collection", "unchanged files", "cancelled unchanged", "cancelled workspace", "partial database", "cancelled partial database", "partial file", "partial move", "required recovery", "workspace required recovery", "successful unchanged"} {
		t.Run(name, func(t *testing.T) {
			lib, d, song := fixture(t)
			original, err := os.ReadFile(song)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			scope := ScopeCollection
			if name == "unchanged files" || name == "partial file" || name == "partial move" {
				scope = ScopeFiles
			}
			entry, err := Run(ctx, lib, "reviewed.write", scope, nil, func(ctx context.Context) error {
				switch name {
				case "cancelled workspace", "workspace required recovery":
					workspace := filepath.Join(lib, ".engine-cli", "staging", "attempt")
					if err := os.MkdirAll(workspace, 0700); err != nil {
						return err
					}
					if err := Workspace(ctx, workspace); err != nil {
						return err
					}
					if err := writeWorkspaceFile(ctx, workspace, "manifest.json", "incomplete"); err != nil {
						return err
					}
					if name == "cancelled workspace" {
						cancel()
						return ctx.Err()
					}
					if err := RequireRecovery(ctx); err != nil {
						return err
					}
				case "cancelled unchanged":
					cancel()
					return ctx.Err()
				case "partial database", "cancelled partial database":
					if _, err := d.Exec("UPDATE Track SET title='Partial'"); err != nil {
						return err
					}
					if name == "cancelled partial database" {
						cancel()
						return ctx.Err()
					}
				case "partial file":
					if err := Preserve(ctx, song); err != nil {
						return err
					}
					if err := os.WriteFile(song, []byte("partial"), 0600); err != nil {
						return err
					}
				case "partial move":
					if err := MoveFile(ctx, song, filepath.Join(lib, "moved.mp3")); err != nil {
						return err
					}
				case "required recovery":
					if err := RequireRecovery(ctx); err != nil {
						return err
					}
				case "successful unchanged":
					return nil
				}
				return refusal
			})
			if name == "successful unchanged" {
				if err != nil {
					t.Fatal(err)
				}
			} else if name == "cancelled unchanged" || name == "cancelled partial database" || name == "cancelled workspace" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			} else if !errors.Is(err, refusal) {
				t.Fatalf("refusal lost: %v", err)
			}
			entries, listErr := List(lib)
			if listErr != nil {
				t.Fatal(listErr)
			}
			switch name {
			case "unchanged collection", "unchanged files", "cancelled unchanged", "cancelled workspace":
				if entry != nil || len(entries) != 0 {
					t.Fatalf("unchanged failure became undoable: entry=%+v history=%+v", entry, entries)
				}
				assertNoHistoryResidue(t, lib)
			default:
				if entry == nil || len(entries) != 1 || entries[0].ID != entry.ID {
					t.Fatalf("lost recoverable entry: entry=%+v history=%+v", entry, entries)
				}
				if name == "required recovery" || name == "workspace required recovery" {
					if entry.After != nil || entry.State != "pending" {
						t.Fatalf("lost pending recovery: %+v", entry)
					}
					return
				}
				if _, err := Undo(t.Context(), lib, "", UndoOptions{}); err != nil {
					t.Fatalf("retained change cannot be undone: %v", err)
				}
				var title string
				if err := d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Original" {
					t.Fatalf("partial database not restored: %q %v", title, err)
				}
				if restored, err := os.ReadFile(song); err != nil || !bytes.Equal(restored, original) {
					t.Fatalf("original audio bytes not restored: %v", err)
				}
			}
		})
	}
}

func TestFailedLibraryRelocationRemainsUndoableWithoutDatabaseSnapshots(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "destination")
	failure := errors.New("failed after relocation")
	entry, err := Run(t.Context(), source, "library.move", ScopeFiles, nil, func(ctx context.Context) error {
		if err := RelocateLibrary(ctx, source, destination); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) || entry == nil || entry.RelocationFrom != source {
		t.Fatalf("relocation failure lost recovery: %+v %v", entry, err)
	}
	if _, err := Undo(t.Context(), destination, "", UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("Undo did not restore source directory: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("Undo left relocated directory: %v", err)
	}
}
