package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/playlist"
	"github.com/spf13/cobra"
)

func reorderCommandLibrary(t *testing.T) (string, *sql.DB) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root := t.TempDir()
	if _, err := db.CreateLibrary(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, name := range []string{"Target", "Other"} {
		if _, err := db.CreatePlaylist(t.Context(), d, name, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	for id := int64(1); id <= 3; id++ {
		if _, err := d.Exec("INSERT INTO Track(id,path,title) VALUES(?,?,?)", id, fmt.Sprintf("%d.wav", id), fmt.Sprintf("Track %d", id)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.AddTrackToPlaylist(t.Context(), d, 1, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.AddTrackToPlaylist(t.Context(), d, 2, 2); err != nil {
		t.Fatal(err)
	}
	return root, d
}

func runReorderCommand(root string, args ...string) (string, string, error) {
	command := newRootCommand()
	var output, diagnostics bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&diagnostics)
	command.SetArgs(append([]string{"--lib-dir", root}, args...))
	err := command.Execute()
	return output.String(), diagnostics.String(), err
}

func assertReorderCommandOrder(t *testing.T, d *sql.DB, want []int64) {
	t.Helper()
	for _, expected := range []struct {
		playlist int64
		ids      []int64
	}{{1, want}, {2, []int64{2}}} {
		tracks, err := db.GetPlaylistTracks(t.Context(), d, expected.playlist)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]int64, 0, len(tracks))
		for _, track := range tracks {
			ids = append(ids, track.ID)
		}
		if !slices.Equal(ids, expected.ids) {
			t.Fatalf("playlist %d: got %v want %v", expected.playlist, ids, expected.ids)
		}
	}
}

func TestPlaylistReorderCommandReviewApplyUndo(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		t.Run("agent="+mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			root, d := reorderCommandLibrary(t)
			args := []string{"playlist", "reorder", "1", "--rows", "0", "--drop", "3"}
			output, diagnostics, err := runReorderCommand(root, append(slices.Clone(args), "--dry-run", "--json")...)
			var plan playlist.ReorderPlan
			if err != nil || json.Unmarshal([]byte(output), &plan) != nil || !plan.CanApply || plan.PlanToken == "" || !slices.Equal(plan.TrackIDs, []int64{2, 3, 1}) || strings.Contains(diagnostics, "operation_id:") {
				t.Fatalf("JSON preview: %q %q %v", output, diagnostics, err)
			}
			output, diagnostics, err = runReorderCommand(root, append(slices.Clone(args), "--dry-run")...)
			want := fmt.Sprintf("Playlist: 1\nResulting track order: [2 3 1]\nCan apply: true\nPlan token: %s\nApply with the same playlist, view, rows, gap and direction using --plan-token.\n", plan.PlanToken)
			if mode == "1" {
				want = fmt.Sprintf("playlist_id:1 track_ids:[2 3 1] can_apply:true plan_token:%s\n", plan.PlanToken)
			}
			if err != nil || output != want || strings.Contains(diagnostics, "operation_id:") {
				t.Fatalf("preview: %q %q %v want %q", output, diagnostics, err, want)
			}
			assertReorderCommandOrder(t, d, []int64{1, 2, 3})
			if _, err := os.Stat(filepath.Join(root, ".engine-cli")); !os.IsNotExist(err) {
				t.Fatalf("preview created safety artifacts: %v", err)
			}
			output, diagnostics, err = runReorderCommand(root, append(slices.Clone(args), "--plan-token", plan.PlanToken)...)
			want = "Reordered playlist 1. Saved track order: [2 3 1]\n"
			if mode == "1" {
				want = "playlist_id:1 track_ids:[2 3 1]\n"
			}
			if err != nil || output != want || !strings.Contains(diagnostics, "operation_id: ") {
				t.Fatalf("apply: %q %q %v", output, diagnostics, err)
			}
			assertReorderCommandOrder(t, d, []int64{2, 3, 1})
			snapshots, err := backup.ListBackups(root, "")
			if err != nil || len(snapshots) != 1 {
				t.Fatalf("snapshots: %+v %v", snapshots, err)
			}
			entries, err := history.List(root)
			if err != nil || len(entries) != 1 || !strings.Contains(diagnostics, "operation_id: "+entries[0].ID+"\n") {
				t.Fatalf("history: %+v %q %v", entries, diagnostics, err)
			}
			if output, diagnostics, err := runReorderCommand(root, "library", "undo", entries[0].ID); err != nil {
				t.Fatalf("undo: %q %q %v", output, diagnostics, err)
			}
			assertReorderCommandOrder(t, d, []int64{1, 2, 3})
		})
	}
}

func TestPlaylistReorderCommandRepeatedRowsDescendingAndJSONApply(t *testing.T) {
	root, d := reorderCommandLibrary(t)
	args := []string{"playlist", "reorder", "1", "--view", "3,2", "--view", "1", "--descending", "--rows", "0", "--rows", "1", "--drop", "3", "--json"}
	output, diagnostics, err := runReorderCommand(root, append(slices.Clone(args), "--dry-run")...)
	var plan playlist.ReorderPlan
	if err != nil || json.Unmarshal([]byte(output), &plan) != nil || !slices.Equal(plan.TrackIDs, []int64{2, 3, 1}) {
		t.Fatalf("repeated descending preview: %q %q %v", output, diagnostics, err)
	}
	output, diagnostics, err = runReorderCommand(root, append(slices.Clone(args), "--plan-token", plan.PlanToken)...)
	var result playlist.ReorderResult
	if err != nil || json.Unmarshal([]byte(output), &result) != nil || result.PlaylistID != 1 || !slices.Equal(result.TrackIDs, plan.TrackIDs) {
		t.Fatalf("JSON apply: %q %q %v", output, diagnostics, err)
	}
	assertReorderCommandOrder(t, d, plan.TrackIDs)
}

func TestPlaylistReorderCommandRejectsWithoutWriting(t *testing.T) {
	root, d := reorderCommandLibrary(t)
	for _, args := range [][]string{
		{"1", "--drop", "0", "--dry-run"},
		{"1", "--rows", "0", "--dry-run"},
		{"1", "extra", "--rows", "0", "--drop", "3", "--dry-run"},
		{"1", "--rows", "bad", "--drop", "3", "--dry-run"},
		{"1", "--rows", "0", "--drop", "3"},
		{"1", "--rows", "0", "--drop", "3", "--plan-token", "unreviewed"},
		{"1", "--rows", "5", "--drop", "0", "--dry-run"},
		{"1", "--view=", "--rows", "0", "--drop", "0", "--dry-run"},
	} {
		output, diagnostics, err := runReorderCommand(root, append([]string{"playlist", "reorder"}, args...)...)
		if err == nil || output != "" || strings.Contains(diagnostics, "operation_id:") {
			t.Fatalf("accepted %v: %q %q %v", args, output, diagnostics, err)
		}
		assertReorderCommandOrder(t, d, []int64{1, 2, 3})
		if _, err := os.Stat(filepath.Join(root, ".engine-cli")); !os.IsNotExist(err) {
			t.Fatalf("rejected command created safety artifacts: %v", err)
		}
	}
}

func TestPlaylistReorderOutputFailures(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		t.Setenv("AGENT", mode)
		for _, result := range []any{&playlist.ReorderPlan{PlaylistID: 1, TrackIDs: []int64{2, 1}, CanApply: true, PlanToken: "reviewed"}, &playlist.ReorderResult{PlaylistID: 1, TrackIDs: []int64{2, 1}}} {
			command := &cobra.Command{}
			command.SetOut(&collectionFailWriter{failAt: 1})
			handled, err := printPlaylistReorder(command, result, false)
			if !handled || !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("mode %s result %T: handled=%t err=%v", mode, result, handled, err)
			}
		}
	}
}
