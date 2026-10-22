package track

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestTrackFileHistory(t *testing.T) {
	for _, operation := range []string{"remove record", "delete audio", "move", "import", "upgrade install"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			d, lib, _ := createTestDB(t)
			ctx := context.Background()
			original := filepath.Join(lib, "song.mp3")
			dest := filepath.Join(lib, "moved.mp3")
			if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec("INSERT INTO Track(id,path,title) VALUES(1,'song.mp3','Song')"); err != nil {
				t.Fatal(err)
			}
			unrelated := filepath.Join(lib, "unrelated.mp3")
			if err := os.Symlink(original, unrelated); err != nil {
				t.Fatal(err)
			}
			entry, err := history.Run(ctx, lib, operation, history.ScopeCollection, nil, func(ctx context.Context) error {
				switch operation {
				case "remove record", "delete audio":
					_, err := RemoveTrack(ctx, d, lib, "1", operation == "delete audio")
					return err
				case "move":
					_, err := MoveTrackFile(ctx, d, lib, "1", dest)
					return err
				case "import":
					_, err := AddTrack(ctx, d, lib, original, "", true, fakeRunner(`{}`))
					dest = filepath.Join(lib, "Music", "song.mp3")
					return err
				default:
					staged := filepath.Join(t.TempDir(), "better.mp3")
					if err := os.WriteFile(staged, []byte("better"), 0600); err != nil {
						return err
					}
					var err error
					dest, err = installUpgrade(ctx, staged, original)
					return err
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := entry.Before.Files[unrelated]; ok {
				t.Fatal("captured unrelated file")
			}
			if operation == "remove record" && len(entry.Before.Files) != 0 {
				t.Fatal("database-only deletion copied audio")
			}
			if _, err = history.Undo(ctx, lib, entry.ID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(original)
			if err != nil || string(data) != "original" {
				t.Fatalf("undo original %q %v", data, err)
			}
			if operation == "move" || operation == "import" || operation == "upgrade install" {
				if _, err = os.Stat(dest); !os.IsNotExist(err) {
					t.Fatalf("undo left destination: %v", err)
				}
			}
		})
	}
}
