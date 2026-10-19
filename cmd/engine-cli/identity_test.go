package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/identity"
)

func TestLibraryRegistrationCLI(t *testing.T) {
	lib := duplicateLibrary(t)
	for _, asJSON := range []bool{false, true} {
		for _, command := range []string{"register", "entities"} {
			var output bytes.Buffer
			c := newRootCommand()
			c.SetOut(&output)
			c.SetErr(&bytes.Buffer{})
			args := []string{"-l", lib, "--no-backup", "library", command}
			if command == "entities" {
				args[len(args)-1] = "entity"
				args = append(args, "list")
			}
			if asJSON {
				args = append(args, "--json")
			}
			c.SetArgs(args)
			if err := c.Execute(); err != nil {
				t.Fatal(err)
			}
			if asJSON && !json.Valid(output.Bytes()) {
				t.Fatalf("invalid JSON: %s", output.String())
			}
		}
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	entities, err := identity.List(context.Background(), d, lib)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeCommand("-l", lib, "track", "inspect", entities[0].Ref, "--json"); err != nil {
		t.Fatal(err)
	}
	r, err := identity.Load(lib)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeCommand("-l", lib, "--no-backup", "library", "register", "--new-copy"); err != nil {
		t.Fatal(err)
	}
	changed, err := identity.Load(lib)
	if err != nil || changed.ID == r.ID {
		t.Fatalf("copy registration: %v", err)
	}
	if _, err := executeCommand("-l", lib, "--no-backup", "library", "undo"); err != nil {
		t.Fatal(err)
	}
	restored, err := identity.Load(lib)
	if err != nil || restored.ID != r.ID {
		t.Fatalf("registration undo: %v", err)
	}
	if _, err := os.Stat(identity.Path(lib)); err != nil {
		t.Fatal(err)
	}
}
