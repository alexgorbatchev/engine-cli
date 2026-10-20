package transfer

import (
	"context"
	"database/sql"
	_ "embed"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"os"
	"path/filepath"
	"testing"
)

//go:embed testdata/schema.sql
var schema string

func library(t *testing.T, uuid string) (string, *sql.DB) {
	t.Helper()
	p := t.TempDir()
	if err := os.Mkdir(filepath.Join(p, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(p, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err = d.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("INSERT INTO Information(uuid,schemaVersionMajor,schemaVersionMinor,schemaVersionPatch) VALUES(?,3,0,2)", uuid); err != nil {
		t.Fatal(err)
	}
	return p, d
}
func execSQL(t *testing.T, d *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func TestSyncPreviewApplyUndo(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('Same','song.mp3','song.mp3'); INSERT INTO Playlist(title,parentListId,nextListId,isPersisted) VALUES('Set',0,0,1); INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,'source',0); UPDATE PerformanceData SET quickCues=x'010203' WHERE trackId=1`)
	if err := os.WriteFile(filepath.Join(source, "song.mp3"), []byte("audio bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	o := Options{Source: source, Destination: dest, Scope: Scope{PlaylistIDs: []int64{1}}, DryRun: true}
	r, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Applicable || r.Summary.TracksAdded != 1 || r.Summary.FilesCopied != 1 {
		t.Fatalf("preview: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dest, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote state: %v", err)
	}
	o.DryRun = false
	o.PlanToken = r.PlanToken
	o.NoBackup = true
	r, err = Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if r.OperationID == "" {
		t.Fatal("missing undo ID")
	}
	var count int
	if err = d.QueryRow("SELECT count(*) FROM PlaylistEntity WHERE databaseUuid='source' AND trackId=1").Scan(&count); err != nil || count != 1 {
		t.Fatalf("membership: %d %v", count, err)
	}
	o.PlanToken = ""
	o.DryRun = true
	again, err := Run(context.Background(), o)
	if err != nil || again.Summary.TracksUpdated != 0 || again.Summary.FilesCopied != 0 {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	if _, err := history.Undo(context.Background(), dest, r.OperationID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = d.QueryRow("SELECT count(*) FROM Track").Scan(&count); err != nil || count != 0 {
		t.Fatalf("undo: %d %v", count, err)
	}
}
