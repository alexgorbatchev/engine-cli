package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestKeptAudioCountsAudioCopiesHistoryKeepsForUndo(t *testing.T) {
	lib, _, p := fixture(t)
	ctx := context.Background()
	if kept, err := ListKeptAudio(lib); err != nil || kept != (KeptAudio{}) {
		t.Fatalf("empty history kept=%+v err=%v", kept, err)
	}
	cover := filepath.Join(lib, "cover.jpg")
	if err := os.WriteFile(cover, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := Run(ctx, lib, "track.tags.update", ScopeFiles, []string{p, cover}, func(context.Context) error {
			if err := os.WriteFile(cover, []byte("new image"), 0o600); err != nil {
				return err
			}
			return os.WriteFile(p, []byte("rewritten audio"), 0o600)
		}); err != nil {
			t.Fatal(err)
		}
	}
	kept, err := ListKeptAudio(lib)
	// The first write kept the original audio and the second kept the first rewrite;
	// the cover image is not audio.
	if err != nil || kept.Files != 2 || kept.Bytes != int64(len("original audio")+len("rewritten audio")) {
		t.Fatalf("kept=%+v err=%v", kept, err)
	}
}
