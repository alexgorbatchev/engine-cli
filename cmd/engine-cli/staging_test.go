package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

func TestStagingCommands(t *testing.T) {
	lib := createTestLibrary(t)
	item, err := staging.New(lib).Create(context.Background(), staging.Item{Kind: "track", Title: "Staged Song", Status: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := executeCommand("-l", lib, "staging", "list", "--json")
	if err != nil || !strings.Contains(out, "Staged Song") {
		t.Fatalf("list %s %v", out, err)
	}
	out, err = executeCommand("-l", lib, "staging", "inspect", item.ID, "--json")
	var got staging.Item
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || got.ID != item.ID {
		t.Fatalf("inspect %s %v", out, err)
	}
	out, err = executeCommand("-l", lib, "staging", "rm", item.ID, "--json")
	if err != nil || !strings.Contains(out, item.ID) {
		t.Fatalf("remove %s %v", out, err)
	}
}
