package history

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func dormantSmartlistWAL(t *testing.T) (string, string) {
	t.Helper()
	lib, database := smartlistHistoryLibrary(t)
	recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	if _, err := database.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lib, "Database2", "m.db")
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("cleanly closed WAL fixture retained %s: %v", suffix, err)
		}
	}
	if err := os.WriteFile(filepath.Join(lib, "retained-audio.bin"), []byte{0, 1, 0xff, 2}, 0600); err != nil {
		t.Fatal(err)
	}
	return lib, path
}

func TestSmartlistProjectionDormantWALPreservesSourceContent(t *testing.T) {
	lib, path := dormantSmartlistWAL(t)
	before := historyLeaseTree(t, lib)
	database, err := db.Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close Smartlist fixture database: %v", err)
		}
	})
	projection := projectionForTest(t, lib, database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if len(projection.Rows) != 1 || len(projection.Actions) != 1 || projection.Rows[0].Values["parentPlaylistPath"] != "Renamed;" {
		t.Fatalf("dormant WAL projection lost native/history evidence: %+v", projection)
	}
	after := historyLeaseTree(t, lib)
	// SQLite's mode=ro preserves database content but may create coordination
	// sidecars. Those arrivals also change the containing directory metadata.
	delete(before, filepath.Dir(path))
	delete(after, filepath.Dir(path))
	for _, suffix := range []string{"-wal", "-shm"} {
		if info, err := os.Stat(path + suffix); err == nil {
			t.Logf("SQLite read-only coordination file %s: %d bytes", suffix, info.Size())
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		delete(after, path+suffix)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("read-only projection changed native database, history, media, or unrelated files:\n%s", historyLeaseTreeDiff(before, after))
	}
}

func TestSmartlistProjectionDormantWALRefusesMissingCoordinationStorage(t *testing.T) {
	lib, path := dormantSmartlistWAL(t)
	smartlistRestrict(t, path, 0400)
	smartlistRestrict(t, filepath.Dir(path), 0500)
	before := historyLeaseTree(t, lib)
	database, err := db.Open(path, true)
	called := false
	if err == nil {
		_, err = ReadSmartlistProjection(t.Context(), lib, database, func(context.Context, *sql.Tx, *SmartlistProjection) (bool, error) {
			called = true
			return true, nil
		})
		err = errors.Join(err, database.Close())
	}
	if err == nil || called {
		t.Fatalf("unavailable WAL coordination storage accepted: callback=%v err=%v", called, err)
	}
	t.Logf("native read refusal: %v", err)
	if after := historyLeaseTree(t, lib); !reflect.DeepEqual(before, after) {
		t.Fatalf("failed read created or changed source storage:\n%s", historyLeaseTreeDiff(before, after))
	}
}
