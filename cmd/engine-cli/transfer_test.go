package main

import (
	"bytes"
	"encoding/json"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func transferLibrary(t *testing.T, uuid string) string {
	t.Helper()
	path := t.TempDir()
	if err := os.Mkdir(filepath.Join(path, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(path, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	schema, err := os.ReadFile("../../internal/transfer/testdata/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("INSERT INTO Information(uuid,schemaVersionMajor,schemaVersionMinor,schemaVersionPatch) VALUES(?,3,0,2); INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Set',0,0)", uuid); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestSyncCLI(t *testing.T) {
	source := transferLibrary(t, "source")
	dest := transferLibrary(t, "dest")
	args := []string{"library", "sync", "--from", source, "--to", dest, "--playlist-id", "1", "--map-playlist", "1:1", "--dry-run", "--json"}
	c := newRootCommand()
	var stdout bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(io.Discard)
	c.SetArgs(args)
	err := c.Execute()
	out := stdout.String()
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Applicable bool   `json:"applicable"`
		PlanToken  string `json:"plan_token"`
	}
	if err = json.Unmarshal([]byte(out), &r); err != nil || !r.Applicable || r.PlanToken == "" {
		t.Fatalf("preview: %s %v", out, err)
	}
	for _, path := range []string{source, dest} {
		if _, err = os.Stat(filepath.Join(path, ".engine-cli")); !os.IsNotExist(err) {
			t.Fatal("preview changed library")
		}
	}
	out, err = executeCommand("library", "sync", "--from", source, "--to", dest, "--root", "--map-playlist", "1:1", "--no-backup")
	if err != nil || !strings.Contains(out, "operation_id:") {
		t.Fatalf("apply %s %v", out, err)
	}
	for _, extra := range [][]string{{"--root", "--playlist-id", "1"}, {"--playlist-id", "Set"}, {"--root", "--map-playlist", "Set:1"}, {"--root", "--media", "invalid"}, {"--root", "--conflict", "invalid"}} {
		args := append([]string{"library", "sync", "--from", source, "--to", dest}, extra...)
		if _, err = executeCommand(args...); err == nil {
			t.Fatalf("accepted %v", extra)
		}
	}
}
