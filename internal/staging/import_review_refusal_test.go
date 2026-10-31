package staging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestImportReviewRefusesUnusableInputsWithoutInstalling(t *testing.T) {
	for _, name := range []string{"no collection", "missing selection", "missing playlist", "unreadable playlists", "missing stage", "nonregular destination"} {
		t.Run(name, func(t *testing.T) {
			store, database, item, audio := importReviewFixture(t)
			original, err := os.ReadFile(audio)
			if err != nil {
				t.Fatal(err)
			}
			opts := ImportOptions{IDs: []string{item.ID}}
			observedDatabase := database
			switch name {
			case "no collection":
				observedDatabase = nil
			case "missing selection":
				opts.IDs = []string{"missing-stage"}
			case "missing playlist":
				opts.ToPlaylist = "Missing"
			case "unreadable playlists":
				opts.ToPlaylist = "Missing"
				if _, err := database.ExecContext(t.Context(), "ALTER TABLE Playlist RENAME TO RetainedPlaylist"); err != nil {
					t.Fatal(err)
				}
			case "missing stage":
				if err := os.Remove(audio); err != nil {
					t.Fatal(err)
				}
			case "nonregular destination":
				path := filepath.Join(store.lib, filepath.FromSlash(importDestination(*item)))
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			review, err := store.ReviewImport(t.Context(), observedDatabase, opts)
			if err == nil || review != nil {
				t.Fatalf("unusable import received a review token: %+v, %v", review, err)
			}
			var count int
			if err := database.QueryRowContext(t.Context(), "SELECT count(*) FROM Track").Scan(&count); err != nil || count != 0 {
				t.Fatalf("refused review installed collection media: count=%d error=%v", count, err)
			}
			if name != "missing stage" {
				after, err := os.ReadFile(audio)
				if err != nil || !bytes.Equal(original, after) {
					t.Fatalf("refused review changed staged evidence: %v", err)
				}
			}
		})
	}
}
