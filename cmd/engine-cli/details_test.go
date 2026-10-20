package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"path/filepath"
	"testing"
)

func TestBatchInspect(t *testing.T) {
	lib := createTestLibrary(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(context.Background(), "UPDATE Track SET path=? WHERE id=1", filepath.Join(home, "song.mp3")); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"track", "playlist"} {
		var output bytes.Buffer
		c := newRootCommand()
		c.SetOut(&output)
		c.SetErr(&bytes.Buffer{})
		c.SetArgs([]string{"-l", lib, kind, "inspect", "1", "999", "1", "--json"})
		if err := c.Execute(); err != nil {
			t.Fatal(err)
		}
		var r details.Report
		if err := json.Unmarshal(output.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if len(r.Items) != 3 || r.Items[0].ID != 1 || r.Items[1].Error.Code != "not_found" || r.Items[2].ID != 1 {
			t.Fatalf("%+v", r)
		}
		if kind == "track" && r.Items[0].Data["path"] != "~/song.mp3" {
			t.Fatal(r.Items[0])
		}
	}
	for _, args := range [][]string{{"track", "inspect", "Song"}, {"track", "inspect", "0"}, {"track", "inspect"}} {
		if _, err := executeCommand(args...); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
