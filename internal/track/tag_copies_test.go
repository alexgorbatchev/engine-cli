package track

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// tagCopyRunner hashes a file's "audio" as its first line, so files whose first
// lines match hold the same audio and differ only in their tags.
func tagCopyRunner(t *testing.T) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "ffmpeg" {
			t.Fatalf("unexpected %s", name)
		}
		data, err := os.ReadFile(args[4])
		if err != nil {
			return nil, err
		}
		audio, _, _ := strings.Cut(string(data), "\n")
		return []byte("SHA256=" + audio), nil
	}
}

func writeTagCopyFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestTagCopiesFoldOnlyCopiesOfTheSameAudio(t *testing.T) {
	d, lib, _ := createTestDB(t)
	music := filepath.Join(lib, "Music")
	// Track 1: a copy whose original is still beside it.
	writeTagCopyFile(t, filepath.Join(music, "one.mp3"), "A\nold tags", 0o644)
	writeTagCopyFile(t, filepath.Join(music, "one-upgraded-123.mp3"), "A\nnew tags", 0o600)
	// Track 2: stacked edits; the intermediate copy is unused and goes too.
	writeTagCopyFile(t, filepath.Join(music, "two-upgraded-1.mp3"), "B\nfirst edit", 0o600)
	writeTagCopyFile(t, filepath.Join(music, "two-upgraded-1-upgraded-2.mp3"), "B\nsecond edit", 0o600)
	// Track 3: the original is gone; the copy takes its name.
	writeTagCopyFile(t, filepath.Join(music, "three-upgraded-9.mp3"), "C\ntags", 0o600)
	// Track 4: an Upgrade audio replacement; different audio, left alone.
	writeTagCopyFile(t, filepath.Join(music, "four.mp3"), "D\nold", 0o644)
	writeTagCopyFile(t, filepath.Join(music, "four-upgraded-5.mp3"), "E\nnew", 0o600)
	// Tracks 5 and 6: the original belongs to another track.
	writeTagCopyFile(t, filepath.Join(music, "five.mp3"), "F\n", 0o644)
	writeTagCopyFile(t, filepath.Join(music, "five-upgraded-7.mp3"), "F\nedited", 0o600)
	// Track 7: the copy is missing from disk.
	if _, err := d.Exec(`INSERT INTO Track(id,title,path) VALUES
		(1,'One','Music/one-upgraded-123.mp3'),
		(2,'Two','Music/two-upgraded-1-upgraded-2.mp3'),
		(3,'Three','Music/three-upgraded-9.mp3'),
		(4,'Four','Music/four-upgraded-5.mp3'),
		(5,'Five','Music/five-upgraded-7.mp3'),
		(6,'Five original','Music/five.mp3'),
		(7,'Seven','Music/seven-upgraded-3.mp3'),
		(8,'Plain','Music/plain.mp3')`); err != nil {
		t.Fatal(err)
	}
	ctx, runner := context.Background(), tagCopyRunner(t)
	plan, err := PreviewTagCopies(ctx, d, lib, runner)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.CanApply || len(plan.Folds) != 3 || plan.PlanToken == "" {
		t.Fatalf("plan %+v", plan)
	}
	want := map[int64]TagCopyFold{
		1: {From: "Music/one-upgraded-123.mp3", To: "Music/one.mp3", ReplacesOriginal: true},
		2: {From: "Music/two-upgraded-1-upgraded-2.mp3", To: "Music/two.mp3", Removes: []string{"Music/two-upgraded-1.mp3"}},
		3: {From: "Music/three-upgraded-9.mp3", To: "Music/three.mp3"},
	}
	for _, fold := range plan.Folds {
		w := want[fold.TrackID]
		if fold.From != w.From || fold.To != w.To || fold.ReplacesOriginal != w.ReplacesOriginal || strings.Join(fold.Removes, ",") != strings.Join(w.Removes, ",") {
			t.Fatalf("fold %+v want %+v", fold, w)
		}
	}
	reasons := map[int64]string{}
	for _, skip := range plan.Skipped {
		reasons[skip.TrackID] = skip.Reason
	}
	if reasons[4] != "tag_copy_audio_differs" || reasons[5] != "tag_copy_original_in_use" || reasons[7] != "tag_copy_missing" || len(reasons) != 3 {
		t.Fatalf("skipped %+v", plan.Skipped)
	}
	// The dry run changed nothing.
	if _, err := os.Stat(filepath.Join(music, "one-upgraded-123.mp3")); err != nil {
		t.Fatal(err)
	}
	var code *progress.Error
	if _, err := FoldTagCopies(ctx, d, lib, "stale", runner); !errors.As(err, &code) || code.Code != "tag_copies_stale" {
		t.Fatalf("stale token: %v", err)
	}
	result, err := FoldTagCopies(ctx, d, lib, plan.PlanToken, runner)
	if err != nil || result.Folded != 3 || result.Removed != 1 {
		t.Fatalf("result %+v %v", result, err)
	}
	for id, path := range map[int64]string{1: "Music/one.mp3", 2: "Music/two.mp3", 3: "Music/three.mp3", 4: "Music/four-upgraded-5.mp3"} {
		var stored, filename string
		if err := d.QueryRow(`SELECT path,COALESCE(filename,'') FROM Track WHERE id=?`, id).Scan(&stored, &filename); err != nil || stored != path || (id != 4 && filename != filepath.Base(path)) {
			t.Fatalf("track %d path=%q filename=%q %v", id, stored, filename, err)
		}
	}
	files, err := os.ReadDir(music)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range files {
		names = append(names, file.Name())
	}
	if got := strings.Join(names, ","); got != "five-upgraded-7.mp3,five.mp3,four-upgraded-5.mp3,four.mp3,one.mp3,three.mp3,two.mp3" {
		t.Fatalf("files %s", got)
	}
	data, _ := os.ReadFile(filepath.Join(music, "one.mp3"))
	info, _ := os.Stat(filepath.Join(music, "one.mp3"))
	if string(data) != "A\nnew tags" || info.Mode().Perm() != 0o644 {
		t.Fatalf("one.mp3 %q %v", data, info.Mode())
	}
	again, err := PreviewTagCopies(ctx, d, lib, runner)
	if err != nil || again.CanApply || len(again.Skipped) != 3 {
		t.Fatalf("second review %+v %v", again, err)
	}
	if _, err := FoldTagCopies(ctx, d, lib, again.PlanToken, runner); !errors.As(err, &code) || code.Code != "tag_copies_none" {
		t.Fatalf("empty plan: %v", err)
	}
}
