package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/db"
)

func duplicateLibrary(t *testing.T) string {
	t.Helper()
	lib := t.TempDir()
	if err := os.MkdirAll(filepath.Join(lib, "Database2"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	schema, err := os.ReadFile("../../internal/duplicate/testdata/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(`INSERT INTO Information(uuid) VALUES('test');
	INSERT INTO Track(title,artist,path,filename,length,bitrate) VALUES('Song','Artist','one.mp3','one.mp3',180,320),(' song ','artist','two.mp3','two.mp3',180,128)`); err != nil {
		t.Fatal(err)
	}
	return lib
}

func TestDuplicateCommands(t *testing.T) {
	lib := duplicateLibrary(t)
	for _, mode := range []string{"0", "1"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			for _, args := range [][]string{{"list"}, {"inspect", "1", "2"}} {
				argv := append([]string{"-l", lib, "track", "duplicate"}, args...)
				out, err := executeCommand(argv...)
				if err != nil || !strings.Contains(out, "Song") || !strings.Contains(out, "320") || !strings.Contains(out, "128") {
					t.Fatalf("output=%s error=%v", out, err)
				}
			}
		})
	}
	for _, args := range [][]string{{"list", "--type", "bad"}, {"list", "extra"}, {"inspect"}, {"inspect", "bad"}, {"inspect", "0"}, {"inspect", "999"}} {
		argv := append([]string{"-l", lib, "track", "duplicate"}, args...)
		if _, err := executeCommand(argv...); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := executeCommand("-l", missing, "track", "duplicate", "list"); err == nil {
		t.Fatal("accepted missing library")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("created missing library: %v", err)
	}
}

func TestDuplicateMergeUndo(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	lib := duplicateLibrary(t)
	out, err := executeCommand("-l", lib, "--no-backup", "track", "duplicate", "merge", "2", "--keep", "1")
	if err != nil || !strings.Contains(out, "merged: 1") || !strings.Contains(out, "operation_id:") {
		t.Fatalf("merge=%s err=%v", out, err)
	}
	if out, err = executeCommand("-l", lib, "--no-backup", "library", "undo"); err != nil {
		t.Fatalf("undo=%s err=%v", out, err)
	}
	out, err = executeCommand("-l", lib, "track", "duplicate", "list")
	if err != nil || !strings.Contains(out, "groups: 1") {
		t.Fatalf("restored=%s err=%v", out, err)
	}
}
