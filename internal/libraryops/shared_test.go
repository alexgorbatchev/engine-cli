package libraryops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedWithFindsOtherLibrariesUsingAFile(t *testing.T) {
	ctx := context.Background()
	media := filepath.Join(t.TempDir(), "song.mp3")
	if err := os.WriteFile(media, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	self, peer, other := fixture(t, media), fixture(t, media), fixture(t, filepath.Join(t.TempDir(), "other.mp3"))
	broken := t.TempDir()
	known := []Library{{Path: self}, {Path: peer, Name: "Peer"}, {Path: other}, {Path: broken}, {Path: peer}}
	shared, unavailable, err := SharedWith(ctx, media, self, known)
	if err != nil || len(shared) != 1 || shared[0].Path != mediaPath("", peer) || len(unavailable) != 1 || unavailable[0].Path != mediaPath("", broken) {
		t.Fatalf("shared=%+v unavailable=%+v err=%v", shared, unavailable, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := SharedWith(cancelled, media, self, known); err == nil {
		t.Fatal("ignored cancellation")
	}
}
