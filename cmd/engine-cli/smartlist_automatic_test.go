package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestNativeMutationRepairsSmartlistsOnlyAfterFreshDesktopAbsence(t *testing.T) {
	original := querySystem
	t.Cleanup(func() { querySystem = original })
	for _, tc := range []struct {
		name    string
		running *bool
		allowed bool
	}{{"absent", new(false), true}, {"running", new(true), false}, {"unknown", nil, false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			lib := t.TempDir()
			if _, err := db.CreateLibrary(t.Context(), lib); err != nil {
				t.Fatal(err)
			}
			database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('broken','Saved','Missing;','','','opaque')`); err != nil {
				t.Fatal(err)
			}
			querySystem = func(context.Context, []string) (*systemcheck.Report, error) {
				return &systemcheck.Report{Checks: map[string]systemcheck.Result{
					systemcheck.EngineDesktop: {Status: "ok", Running: tc.running},
				}}, nil
			}
			_, err = executeCommand("-l", lib, "--no-backup", "playlist", "create", "Added")
			if (err == nil) != tc.allowed {
				t.Fatalf("fresh Desktop protection: allowed=%t error=%v", tc.allowed, err)
			}
			var saved, added int
			if err := database.QueryRow(`SELECT count(*) FROM Smartlist`).Scan(&saved); err != nil {
				t.Fatal(err)
			}
			if err := database.QueryRow(`SELECT count(*) FROM Playlist WHERE title='Added'`).Scan(&added); err != nil {
				t.Fatal(err)
			}
			if tc.allowed {
				if saved != 0 || added != 1 {
					t.Fatalf("ordinary command did not repair then mutate: saved=%d added=%d", saved, added)
				}
			} else {
				if saved != 1 || added != 0 {
					t.Fatalf("guarded command changed native state: saved=%d added=%d", saved, added)
				}
				if _, err := os.Lstat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
					t.Fatalf("guarded command created repair/history storage: %v", err)
				}
			}
		})
	}
}
