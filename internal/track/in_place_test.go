package track

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublishInPlaceRefusesAChangedOriginalAndCancellation(t *testing.T) {
	for _, scenario := range []string{"changed", "cancelled", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			original, output := filepath.Join(dir, "song.mp3"), filepath.Join(t.TempDir(), "tagged.mp3")
			if err := os.WriteFile(original, []byte("original"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(output, []byte("tagged"), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(original)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := "original"
			switch scenario {
			case "changed":
				if err := os.WriteFile(original, []byte("edited elsewhere"), 0644); err != nil {
					t.Fatal(err)
				}
				want = "edited elsewhere"
			case "cancelled":
				// A cancellation after the copy is written still publishes nothing.
				ctx = cancelAfterCopy{ctx, cancel, original}
			case "missing":
				if err := os.Remove(original); err != nil {
					t.Fatal(err)
				}
				want = ""
			}
			if err := publishInPlace(ctx, output, original, before); err == nil {
				t.Fatal("published over a file that changed, a cancelled write or a missing file")
			}
			data, _ := os.ReadFile(original)
			if string(data) != want {
				t.Fatalf("original=%q want %q", data, want)
			}
			if entries, _ := os.ReadDir(dir); len(entries) > 1 {
				t.Fatalf("left a temporary copy: %v", entries)
			}
		})
	}
}

// cancelAfterCopy reports cancellation once the replacement exists beside the original.
type cancelAfterCopy struct {
	context.Context
	cancel   func()
	original string
}

func (c cancelAfterCopy) Err() error {
	if entries, _ := os.ReadDir(filepath.Dir(c.original)); len(entries) > 1 {
		c.cancel()
	}
	return c.Context.Err()
}

func (c cancelAfterCopy) Deadline() (time.Time, bool) { return time.Time{}, false }
