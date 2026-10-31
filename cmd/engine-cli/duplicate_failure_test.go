package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestDuplicateMergeFailureHasNoOutput(t *testing.T) {
	for _, mode := range []string{"human", "json"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", "0")
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			library := duplicateLibrary(t)
			d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if _, err := d.Exec(`CREATE TRIGGER deny_merge BEFORE DELETE ON Track BEGIN SELECT RAISE(ABORT,'merge denied'); END`); err != nil {
				t.Fatal(err)
			}
			args := []string{"-l", library, "--no-backup", "track", "duplicate", "merge", "2", "--keep", "1"}
			if mode == "json" {
				args = append(args, "--json")
			}
			var stdout, stderr bytes.Buffer
			command := newRootCommand()
			command.SetArgs(args)
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			err = command.Execute()
			if err == nil || stdout.Len() != 0 {
				t.Fatalf("failed merge stdout=%q stderr=%q error=%v", stdout.String(), stderr.String(), err)
			}
			var count int
			if err := d.QueryRow(`SELECT COUNT(*) FROM Track`).Scan(&count); err != nil || count != 2 {
				t.Fatalf("failed merge changed collection: count=%d error=%v", count, err)
			}
		})
	}
}
