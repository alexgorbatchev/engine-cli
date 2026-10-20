package track

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestUnmanagedTagsRetainOriginalUntilVerified(t *testing.T) {
	for _, mode := range []string{"success", "artwork", "preview", "native failure", "missing output", "escaped output", "changed format", "changed audio", "changed original", "cancelled", "invalid hash", "hash failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lib := t.TempDir()
			original := filepath.Join(t.TempDir(), "audio.mp3")
			if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			opts := FileTagOptions{Options: acquire.TagOptions{Title: new("After"), DryRun: mode == "preview", Artwork: "https://example.test/image.png"}, ArtworkOnly: mode == "artwork"}
			opts.Runner = func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "ffmpeg" {
					if mode == "hash failure" {
						return nil, errors.New("cannot hash")
					}
					if mode == "invalid hash" {
						return []byte("not a hash"), nil
					}
					if mode == "changed audio" && filepath.Base(args[4]) != "audio.mp3" {
						return []byte("SHA256=different"), nil
					}
					return []byte("SHA256=same"), nil
				}
				if mode == "artwork" && !slices.Contains(args, "embed") {
					t.Fatalf("native artwork command: %v", args)
				}
				if mode == "native failure" {
					return nil, errors.New("native failure")
				}
				index := slices.Index(args, "--out-dir")
				if index < 0 {
					t.Fatalf("missing workspace: %v", args)
				}
				output := filepath.Join(args[index+1], "tagged.mp3")
				if mode == "changed format" {
					output = filepath.Join(args[index+1], "tagged.m4a")
				}
				if mode == "escaped output" {
					output = filepath.Join(t.TempDir(), "escaped.mp3")
				}
				if mode != "missing output" {
					if err := os.WriteFile(output, []byte("tagged"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "changed original" {
					if err := os.WriteFile(original, []byte("externally changed"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "cancelled" {
					cancel()
				}
				return json.Marshal(acquire.TagResult{Status: "success", Results: []acquire.TagFile{{Status: "success", OutputFile: output}}})
			}
			var result *acquire.TagResult
			entry, err := history.Run(ctx, lib, "track.tags.update", nil, func(ctx context.Context) error {
				var e error
				result, e = UpdateUnmanagedFileTags(ctx, original, opts)
				return e
			})
			wantErr := !slices.Contains([]string{"success", "artwork", "preview"}, mode)
			if (err != nil) != wantErr {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			data, err := os.ReadFile(original)
			if err != nil {
				t.Fatal(err)
			}
			want := "original"
			if mode == "success" || mode == "artwork" {
				want = "tagged"
			}
			if mode == "changed original" {
				want = "externally changed"
			}
			if string(data) != want {
				t.Fatalf("original changed before validation: %q", data)
			}
			if mode == "success" || mode == "artwork" {
				if _, err := history.Undo(context.Background(), lib, entry.ID, history.UndoOptions{}); err != nil {
					t.Fatal(err)
				}
				data, err = os.ReadFile(original)
				if err != nil || string(data) != "original" {
					t.Fatalf("undo %q %v", data, err)
				}
			}
		})
	}
}

func TestUnmanagedTagsRejectNonFiles(t *testing.T) {
	for _, path := range []string{filepath.Join(t.TempDir(), "missing.mp3"), t.TempDir()} {
		if _, err := UpdateUnmanagedFileTags(context.Background(), path, FileTagOptions{}); err == nil {
			t.Fatal("accepted nonfile")
		}
	}
	// A broken link must never become a new file at the selector's location.
	link := filepath.Join(t.TempDir(), "broken.mp3")
	if err := os.Symlink("missing.mp3", link); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateUnmanagedFileTags(context.Background(), link, FileTagOptions{}); err == nil || !strings.Contains(err.Error(), "missing.mp3") {
		t.Fatalf("broken link: %v", err)
	}
}
