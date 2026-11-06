package history

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func smartlistHistoryLibrary(t *testing.T) (string, *sql.DB) {
	t.Helper()
	lib := t.TempDir()
	if _, err := db.CreateLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	for _, title := range []string{"House", "Sets"} {
		if _, err := db.CreatePlaylist(context.Background(), d, title, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES ('saved','Saved','House;','','','opaque')`); err != nil {
		t.Fatal(err)
	}
	return lib, d
}

func recordSmartlistMutation(t *testing.T, lib string, d *sql.DB, operation, statement string) *Entry {
	t.Helper()
	entry, err := Run(context.Background(), lib, operation, ScopeCollection, nil, func(ctx context.Context) error {
		_, err := d.ExecContext(ctx, statement)
		return err
	})
	if err != nil || entry == nil {
		t.Fatalf("record historical mutation: entry=%v error=%v", entry, err)
	}
	return entry
}

func TestSmartlistHistoryResolvesNativeIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, operation, statement, wantPath string
		deleted                              bool
	}{
		{"renamed", "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`, "Renamed;", false},
		{"moved", "playlist.move", `UPDATE Playlist SET parentListId=2 WHERE id=1`, "Sets;House;", false},
		{"removed", "playlist.rm", `DELETE FROM Playlist WHERE id=1`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib, d := smartlistHistoryLibrary(t)
			entry := recordSmartlistMutation(t, lib, d, tc.operation, tc.statement)
			evidence, err := readSmartlistEvidence(context.Background(), lib)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := evidence.resolvePath(context.Background(), "House;")
			if err != nil || resolved == nil || resolved.id != 1 || resolved.present == tc.deleted || resolved.currentPath != tc.wantPath {
				t.Fatalf("resolved=%+v error=%v", resolved, err)
			}
			if tc.deleted && resolved.deleteEntry != entry.ID {
				t.Fatalf("original deletion identity lost: %+v", resolved)
			}
			if unknown, err := evidence.resolvePath(context.Background(), "Unrecorded;"); err != nil || unknown != nil {
				t.Fatalf("invented identity for unrecorded path: %+v %v", unknown, err)
			}
			if root, err := evidence.resolvePath(context.Background(), ""); err != nil || root.id != 0 || !root.present {
				t.Fatalf("root identity: %+v %v", root, err)
			}
			var parent, rules string
			if err := d.QueryRow(`SELECT parentPlaylistPath,rules FROM Smartlist WHERE listUuid='saved'`).Scan(&parent, &rules); err != nil || parent != "House;" || rules != "opaque" {
				t.Fatalf("planning mutated saved data: parent=%q rules=%q error=%v", parent, rules, err)
			}
			if entries, err := List(lib); err != nil || len(entries) != 1 {
				t.Fatalf("planning enrolled history: %+v %v", entries, err)
			}
		})
	}
}

func TestSmartlistHistoryRefusesAmbiguousIdentity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statements []string
	}{
		{"reused path", []string{`UPDATE Playlist SET title='Renamed' WHERE id=1`, `INSERT INTO Playlist(title,parentListId,nextListId) VALUES ('House',0,0)`, `UPDATE Playlist SET title='Other' WHERE id=3`}},
		{"reappeared identity", []string{`DELETE FROM Playlist WHERE id=1`, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES (1,'Renamed',0,0)`}},
		{"foreign library", []string{`UPDATE Playlist SET title='Renamed' WHERE id=1; UPDATE Information SET uuid='different-library'`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib, d := smartlistHistoryLibrary(t)
			for _, statement := range tc.statements {
				recordSmartlistMutation(t, lib, d, "playlist.rename", statement)
			}
			evidence, err := readSmartlistEvidence(context.Background(), lib)
			if err == nil {
				_, err = evidence.resolvePath(context.Background(), "House;")
			}
			if progress.ErrorCode(err) != "smartlist_repair_ambiguous" {
				t.Fatalf("accepted ambiguous native identity: %v", err)
			}
		})
	}
}

func TestSmartlistHistoryHealthyReadDoesNotEnroll(t *testing.T) {
	lib, _ := smartlistHistoryLibrary(t)
	evidence, err := readSmartlistEvidence(context.Background(), lib)
	if err != nil || evidence == nil || evidence.hash == "" || len(evidence.versions) != 0 {
		t.Fatalf("healthy evidence: %+v %v", evidence, err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "history")); !os.IsNotExist(err) {
		t.Fatalf("healthy read enrolled history: %v", err)
	}
}

func TestSmartlistHistoryRejectsDamagedSnapshot(t *testing.T) {
	lib, d := smartlistHistoryLibrary(t)
	entry := recordSmartlistMutation(t, lib, d, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	saved := entry.Before.Databases[filepath.Join(lib, "Database2", "m.db")]
	corrupt, err := db.Open(saved.Path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = corrupt.Exec(`UPDATE Playlist SET title='Tampered' WHERE id=1`)
	closeErr := corrupt.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("alter synthetic saved evidence: %v %v", err, closeErr)
	}
	if evidence, err := readSmartlistEvidence(context.Background(), lib); err == nil || evidence != nil {
		t.Fatalf("accepted damaged saved evidence: %+v %v", evidence, err)
	}
}

func TestSmartlistHistoryOriginalDeletionProvenance(t *testing.T) {
	lib, d := smartlistHistoryLibrary(t)
	original := recordSmartlistMutation(t, lib, d, "playlist.rm", `DELETE FROM Playlist WHERE id=1`)
	recordSmartlistMutation(t, lib, d, "playlist.rm", `DELETE FROM Playlist WHERE id=2`)
	evidence, err := readSmartlistEvidence(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := evidence.resolvePath(context.Background(), "House;")
	if err != nil || resolved == nil || resolved.present || resolved.deleteEntry != original.ID {
		t.Fatalf("later unrelated delete stole repair provenance: %+v %v", resolved, err)
	}
}

func TestSmartlistHistoryRejectsIncompleteSnapshotIdentity(t *testing.T) {
	for _, missing := range []string{"path", "hash"} {
		t.Run(missing, func(t *testing.T) {
			lib, d := smartlistHistoryLibrary(t)
			entry := recordSmartlistMutation(t, lib, d, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
			path := filepath.Join(lib, "Database2", "m.db")
			state := entry.Before.Databases[path]
			if missing == "path" {
				state.Path = ""
			} else {
				state.Hash = ""
			}
			entry.Before.Databases[path] = state
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeEntry(dir, entry); err != nil {
				t.Fatal(err)
			}
			if evidence, err := readSmartlistEvidence(context.Background(), lib); err == nil || evidence != nil {
				t.Fatalf("accepted persisted snapshot without %s: %+v %v", missing, evidence, err)
			}
		})
	}
}

func TestSmartlistHistoryPendingAndCancellation(t *testing.T) {
	lib, d := smartlistHistoryLibrary(t)
	entry := recordSmartlistMutation(t, lib, d, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if evidence, err := readSmartlistEvidence(ctx, lib); err == nil || evidence != nil {
		t.Fatalf("accepted cancelled evidence read: %+v %v", evidence, err)
	}
	entry.After = nil
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEntry(dir, entry); err != nil {
		t.Fatal(err)
	}
	if evidence, err := readSmartlistEvidence(context.Background(), lib); progress.ErrorCode(err) != "history_recovery_required" || evidence != nil {
		t.Fatalf("used pending history as identity evidence: %+v %v", evidence, err)
	}
}

func TestSmartlistHistoryRejectsChangedPlaylistIdentitySchema(t *testing.T) {
	lib, d := smartlistHistoryLibrary(t)
	var definition string
	if err := d.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='Playlist'`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, "AUTOINCREMENT") {
		t.Fatal("native fixture does not establish AUTOINCREMENT identity")
	}
	replacement := strings.Replace(definition, "CREATE TABLE Playlist", "CREATE TABLE replacement", 1)
	replacement = strings.Replace(replacement, "AUTOINCREMENT", "", 1)
	// Recreate the native dependent objects as well; ALTER TABLE correctly
	// rejects dangling views during the deliberately malformed table rebuild.
	rows, err := d.Query(`SELECT type,name,sql FROM sqlite_schema WHERE type='view' OR (type='trigger' AND tbl_name='Playlist') ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	var drops, restore []string
	for rows.Next() {
		var kind, name, statement string
		if err := rows.Scan(&kind, &name, &statement); err != nil {
			t.Fatal(err)
		}
		if kind == "view" {
			drops = append(drops, "DROP VIEW "+quote(name))
		}
		restore = append(restore, statement)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	recordSmartlistMutation(t, lib, d, "playlist.rename", strings.Join(drops, ";")+";"+replacement+`; INSERT INTO replacement SELECT * FROM Playlist; DROP TABLE Playlist; ALTER TABLE replacement RENAME TO Playlist; `+strings.Join(restore, ";")+`; UPDATE Playlist SET title='Renamed' WHERE id=1`)
	evidence, err := readSmartlistEvidence(context.Background(), lib)
	if err == nil {
		_, err = evidence.resolvePath(context.Background(), "House;")
	}
	if progress.ErrorCode(err) != "smartlist_repair_ambiguous" {
		t.Fatalf("accepted changed native identity schema: %v", err)
	}
}
