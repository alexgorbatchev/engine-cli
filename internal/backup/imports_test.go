package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func archiveState(t *testing.T, lib string) importsource.State {
	t.Helper()
	s, err := importsource.Read(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func publishArchive(t *testing.T, lib, title string) importsource.State {
	t.Helper()
	xml := []byte(`<?xml version="1.0"?><DJ_PLAYLISTS Unknown="preserved"><COLLECTION><TRACK TrackID="0" Name="` + title + `"/></COLLECTION></DJ_PLAYLISTS>`)
	id := importsource.DocumentID(xml)
	s := importsource.State{Documents: []importsource.Document{{ID: id, XML: xml}}, Tracks: []importsource.Track{{DocumentID: id, SourceID: "track:0", OriginUUID: "original", OriginTrackID: 7}}}
	if err := importsource.Replace(t.Context(), lib, archiveState(t, lib).Revision(), s); err != nil {
		t.Fatal(err)
	}
	return archiveState(t, lib)
}

func TestRestoreRejectsInvalidLiveImportPathBeforeEngineWrites(t *testing.T) {
	lib := createDummyEngineLibrary(t)
	info, err := CreateBackup(t.Context(), lib, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lib, "Database2", "m.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("UPDATE fixture SET value='newer live content'"); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(companion.ImportsPath(lib), 0700); err != nil {
		t.Fatal(err)
	}
	if err := RestoreBackup(t.Context(), info.Path, lib); err == nil {
		t.Fatal("accepted nonregular live import archive")
	}
	d, err = db.Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var value string
	if err := d.QueryRow("SELECT value FROM fixture").Scan(&value); err != nil || value != "newer live content" {
		t.Fatalf("restore changed Engine before rejecting live companion path: value=%q error=%v", value, err)
	}
}

func TestIndependentImportSnapshotAndRestore(t *testing.T) {
	for _, withTags := range []bool{false, true} {
		t.Run(map[bool]string{false: "imports only", true: "imports and tags"}[withTags], func(t *testing.T) {
			lib := createDummyEngineLibrary(t)
			before := publishArchive(t, lib, "Original")
			if withTags {
				if _, err := tags.EditCategory(t.Context(), lib, tags.Edit{Name: "Mood", Revision: tagRevision(t, lib)}); err != nil {
					t.Fatal(err)
				}
			}
			info, err := CreateBackup(t.Context(), lib, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(info.Path, companionFolder, companion.ImportsName)
			if err := importsource.ValidateDatabase(t.Context(), path); err != nil {
				t.Fatalf("independent archive missing from backup: %v", err)
			}
			publishArchive(t, lib, "Later")
			entry, err := history.Run(t.Context(), lib, "library.restore", history.ScopeCollection, nil, func(ctx context.Context) error {
				return RestoreBackup(ctx, info.Path, lib)
			})
			if err != nil || archiveState(t, lib).Revision() != before.Revision() {
				t.Fatalf("source XML and native mapping not restored: entry=%+v err=%v", entry, err)
			}
			if withTags {
				listed, err := tags.List(t.Context(), lib)
				if err != nil || len(listed.Categories) != 1 || listed.Categories[0].Name != "Mood" {
					t.Fatalf("independent tags changed: %+v %v", listed, err)
				}
			}
			if _, err := history.Undo(t.Context(), lib, entry.ID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(archiveState(t, lib).Documents[0].XML, before.Documents[0].XML) {
				t.Fatal("undo did not restore later source document")
			}
		})
	}
}

func TestRestoreAbsenceRemovesFutureImportsAndUndoRecovers(t *testing.T) {
	for _, kind := range []string{"explicit absence", "legacy tags-only manifest", "old snapshot"} {
		t.Run(kind, func(t *testing.T) {
			lib := createDummyEngineLibrary(t)
			info, err := CreateBackup(t.Context(), lib, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(info.Path, companionFolder)
			if kind == "old snapshot" {
				err = os.RemoveAll(dir)
			} else if kind == "legacy tags-only manifest" {
				err = os.WriteFile(filepath.Join(dir, companionManifest), []byte(`{"version":1,"tags":false}`), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := publishArchive(t, lib, "Future")
			entry, err := history.Run(t.Context(), lib, "library.restore", history.ScopeCollection, nil, func(ctx context.Context) error {
				return RestoreBackup(ctx, info.Path, lib)
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(companion.ImportsPath(lib)); !os.IsNotExist(err) {
				t.Fatalf("future archive survived old absence: %v", err)
			}
			if _, err := history.Undo(t.Context(), lib, entry.ID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			if archiveState(t, lib).Revision() != before.Revision() {
				t.Fatal("undo lost source archive")
			}
		})
	}
}

func TestInvalidImportSnapshotRefusedBeforeLiveChanges(t *testing.T) {
	for _, damage := range []string{"missing database", "unrecorded database", "missing presence", "invalid XML fingerprint", "invalid mapping", "symlink"} {
		t.Run(damage, func(t *testing.T) {
			lib := createDummyEngineLibrary(t)
			publishArchive(t, lib, "Original")
			info, err := CreateBackup(t.Context(), lib, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(info.Path, companionFolder)
			path := filepath.Join(dir, companion.ImportsName)
			switch damage {
			case "missing database":
				err = os.Remove(path)
			case "unrecorded database":
				err = os.WriteFile(filepath.Join(dir, companionManifest), []byte(`{"version":2,"tags":false,"imports":false}`), 0600)
			case "missing presence":
				err = os.WriteFile(filepath.Join(dir, companionManifest), []byte(`{"version":2,"tags":false}`), 0600)
			case "symlink":
				if err := os.Rename(path, path+"-saved"); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(path+"-saved", path)
			default:
				d, openErr := db.Open(path)
				if openErr != nil {
					t.Fatal(openErr)
				}
				query := "UPDATE Document SET xml='<DJ_PLAYLISTS/>'"
				if damage == "invalid mapping" {
					query = "UPDATE Track SET documentId='missing'"
				}
				_, err = d.Exec(query)
				if closeErr := d.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before := publishArchive(t, lib, "Live newer")
			enginePath := filepath.Join(lib, "Database2", "m.db")
			engine, err := os.ReadFile(enginePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := RestoreBackup(t.Context(), info.Path, lib); err == nil {
				t.Fatal("accepted damaged source archive")
			}
			after, err := os.ReadFile(enginePath)
			if err != nil || !bytes.Equal(engine, after) || archiveState(t, lib).Revision() != before.Revision() {
				t.Fatalf("live data changed before rejection: %v", err)
			}
		})
	}
}
