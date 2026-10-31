package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestAPIVerifyRejectsIncompleteRead(t *testing.T) {
	for _, statement := range []string{
		`UPDATE PlaylistEntity SET nextEntityId=id`,
		`DROP TABLE PlaylistEntity`,
	} {
		t.Run(statement, func(t *testing.T) {
			s, lib := testAPI(t)
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`INSERT INTO PlaylistEntity(id,listId,trackId,databaseUuid,nextEntityId)
				SELECT 1,1,originTrackId,originDatabaseUuid,0 FROM Track WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			job := awaitAPIJob(t, s, submitAPIJob(t, s, "library.verify", `{}`))
			failure, ok := job["error"].(map[string]any)
			if job["state"] != "failed" || !ok || failure["code"] != "library_verify_incomplete" || job["result"] != nil {
				t.Fatalf("incomplete verification job=%+v", job)
			}
			for _, name := range []string{"backups", "history"} {
				entries, err := os.ReadDir(filepath.Join(lib, ".engine-cli", name))
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if len(entries) != 0 {
					t.Fatalf("read-only verification created %s: %+v", name, entries)
				}
			}
		})
	}
}
