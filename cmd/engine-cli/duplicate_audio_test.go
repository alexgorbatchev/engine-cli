package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/fingerprint"
	"github.com/dj/engine-cli/internal/identity"
)

func TestAudioScanCLIAndReferenceMerge(t *testing.T) {
	lib := duplicateLibrary(t)
	ctx := context.Background()
	a := fingerprint.Audio{Profile: fingerprint.Profile, Duration: 180, Raw: make([]uint32, 1000)}
	x := uint32(123)
	for i := range a.Raw {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		a.Raw[i] = x
	}
	index := fingerprint.Index{}
	for _, name := range []string{"one.mp3", "two.mp3"} {
		path := filepath.Join(lib, name)
		if err := os.WriteFile(path, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		hash, err := fingerprint.FileHash(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		index[hash] = a
	}
	if err := fingerprint.Save(ctx, lib, index); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := identity.Register(ctx, d, lib, false); err != nil {
		t.Fatal(err)
	}
	items, err := identity.List(ctx, d, lib)
	if err != nil {
		t.Fatal(err)
	}
	for _, asJSON := range []bool{false, true} {
		args := []string{"-l", lib, "--no-backup", "track", "duplicate", "scan", "--refs", items[0].Ref + "," + items[1].Ref}
		if asJSON {
			args = append(args, "--json")
		}
		var out, stderr bytes.Buffer
		c := newRootCommand()
		c.SetOut(&out)
		c.SetErr(&stderr)
		c.SetArgs(args)
		if err := c.Execute(); err != nil {
			t.Fatal(err)
		}
		if asJSON {
			if !json.Valid(out.Bytes()) {
				t.Fatalf("invalid JSON: %s", out.String())
			}
		} else if !strings.Contains(out.String(), "groups: 1") || !strings.Contains(out.String(), "Song") {
			t.Fatalf("missing audio group: %s", out.String())
		}
	}
	if _, err := executeCommand("-l", lib, "track", "duplicate", "inspect", items[0].Ref); err != nil {
		t.Fatal(err)
	}
	if _, err := executeCommand("-l", lib, "track", "inspect", items[0].Ref, "1"); err == nil {
		t.Fatal("accepted mixed selectors")
	}
	if _, err := executeCommand("-l", lib, "--no-backup", "track", "duplicate", "merge", items[1].Ref, "--keep-ref", items[0].Ref, "--performance-from-ref", items[1].Ref); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(lib, "one.mp3")); err != nil {
		t.Fatal(err)
	}
	out, err := executeCommand("-l", lib, "--no-backup", "track", "duplicate", "scan")
	if err != nil || !strings.Contains(out, "audio_unavailable") {
		t.Fatalf("missing audio reporting: %s %v", out, err)
	}
}
