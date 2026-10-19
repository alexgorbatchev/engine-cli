package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEditCommands(t *testing.T) {
	lib := createTestLibrary(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"track", "update", "1", "--title", "New title", "--artist", "New artist", "--year", "2026", "--rating", "4", "--json"}, "New title"},
		{[]string{"playlist", "rename", "1", "Renamed", "--json"}, "Renamed"},
		{[]string{"track", "update", "1", "--clear-artwork", "--json"}, "New title"},
	} {
		var out bytes.Buffer
		c := newRootCommand()
		c.SetOut(&out)
		c.SetErr(&bytes.Buffer{})
		c.SetArgs(append([]string{"-l", lib, "--no-backup"}, tt.args...))
		if err := c.Execute(); err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result["title"] != tt.want {
			t.Fatal(result)
		}
	}
	for _, args := range [][]string{{"track", "update", "1", "--artwork", "missing.png"}, {"track", "update", "1", "--artwork", "missing.png", "--clear-artwork"}} {
		if _, err := executeCommand(append([]string{"-l", lib, "--no-backup"}, args...)...); err == nil {
			t.Fatal("invalid artwork edit accepted", args)
		}
	}
	for _, args := range [][]string{{"track", "update", "1", "--title", "Other"}, {"playlist", "rename", "1", "Again"}} {
		for _, mode := range []string{"0", "1"} {
			t.Setenv("AGENT", mode)
			_, err := executeCommand(append([]string{"-l", lib, "--no-backup"}, args...)...)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, args := range [][]string{{"track", "update", "1"}, {"track", "update", "1", "--rating", "9"}, {"playlist", "rename", "999", "Missing"}, {"track", "update", "999", "--title", "Missing"}} {
		if _, err := executeCommand(append([]string{"-l", lib, "--no-backup"}, args...)...); err == nil {
			t.Fatal(args)
		}
	}
}
