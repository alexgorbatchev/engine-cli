package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestPlaylistImportCLIReviewApplyAndUndo(t *testing.T) {
	root := createTestLibrary(t)
	source := filepath.Join(root, "review.m3u8")
	if err := os.WriteFile(source, []byte("#EXTM3U\nMusic/track1.m4a\nMusic/track1.m4a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-l", root, "--no-backup", "playlist", "import", source, "--name", "Reviewed set", "--parent", "1"}
	preview, err := executeCommand(append(append([]string{}, args...), "--dry-run")...)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"New playlist: Reviewed set", "Matched tracks: 1", "Repeated entries omitted: 1", "line 2: matched", "line 3: duplicate", "Plan token:"} {
		if !strings.Contains(preview, text) {
			t.Fatalf("missing %q: %s", text, preview)
		}
	}
	if strings.Contains(preview, `"track_ids"`) {
		t.Fatal("human review printed raw JSON")
	}
	plan := runJSON(t, root, "playlist", "import", source, "--name", "Reviewed set", "--parent", "1", "--dry-run", "--json")
	token := plan["plan_token"].(string)
	if _, err := executeCommand(args...); err == nil {
		t.Fatal("import without review token succeeded")
	}
	output, err := executeCommand(append(args, "--plan-token", token)...)
	if err != nil || !strings.Contains(output, "tracks, 1 repeated entries omitted") {
		t.Fatalf("apply: %s %v", output, err)
	}
	entries, err := history.List(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("history: %+v %v", entries, err)
	}
	if _, err := executeCommand("-l", root, "--no-backup", "library", "undo"); err != nil {
		t.Fatal(err)
	}
	listing := runJSON(t, root, "playlist", "inspect", "1", "--json")
	if strings.Contains(fmt.Sprint(listing), "Reviewed set") {
		t.Fatal("undo retained imported child")
	}
}
