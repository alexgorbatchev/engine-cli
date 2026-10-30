package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

// runJSON runs a command against lib and decodes its JSON object output.
func runJSON(t *testing.T, lib string, args ...string) map[string]any {
	t.Helper()
	var out bytes.Buffer
	c := newRootCommand()
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	c.SetArgs(append([]string{"-l", lib, "--no-backup"}, args...))
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestEditCommands(t *testing.T) {
	lib := createTestLibrary(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, tt := range []struct {
		args  []string
		field string
		want  any
	}{
		{[]string{"track", "update", "1", "--title", "New title", "--artist", "New artist", "--year", "2026", "--rating", "4", "--json"}, "title", "New title"},
		{[]string{"playlist", "rename", "1", "Renamed", "--json"}, "title", "Renamed"},
		{[]string{"track", "update", "1", "--clear-artwork", "--json"}, "title", "New title"},
		{[]string{"track", "update", "1", "--key", "6B", "--json"}, "key", float64(20)},
		{[]string{"track", "update", "1", "--key", "Am", "--json"}, "key", float64(1)},
		{[]string{"track", "update", "1", "--key", "B♭ minor", "--json"}, "key", float64(15)},
		{[]string{"track", "update", "1", "--key", "F♯ Major", "--json"}, "key", float64(12)},
		{[]string{"track", "update", "1", "--key", "12M", "--json"}, "key", float64(23)},
		{[]string{"track", "update", "1", "--key", "none", "--json"}, "key", nil},
	} {
		result := runJSON(t, lib, tt.args...)
		// Track edits report only the edited ID, so read the track back to check the change.
		if tt.args[0] == "track" {
			if result["track_id"] != float64(1) || len(result) != 1 {
				t.Fatalf("edit result %v", result)
			}
			result = runJSON(t, lib, "track", "inspect", "1", "--json")["items"].([]any)[0].(map[string]any)["data"].(map[string]any)
		}
		if result[tt.field] != tt.want {
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
	for _, args := range [][]string{{"track", "update", "1"}, {"track", "update", "1", "--rating", "9"}, {"track", "update", "1", "--key", "13A"}, {"playlist", "rename", "999", "Missing"}, {"track", "update", "999", "--title", "Missing"}} {
		if _, err := executeCommand(append([]string{"-l", lib, "--no-backup"}, args...)...); err == nil {
			t.Fatal(args)
		}
	}
}
