package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSmartlistPreparationFailureNeverGrantsPublication(t *testing.T) {
	for _, damage := range []string{"archive is a file", "active is a file", "pending repair", "damaged entry", "changed native source", "missing native source", "cancelled"} {
		t.Run(damage, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			entry := recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := planSmartlistRepair(ctx, lib)
			if err != nil {
				t.Fatal(err)
			}
			active := filepath.Join(filepath.Dir(dir), "smartlist-repair")
			target := filepath.Join(lib, "Database2", "m.db")
			switch damage {
			case "archive is a file":
				err = os.WriteFile(filepath.Join(filepath.Dir(dir), "smartlist-repairs"), []byte("retained archive marker"), 0600)
			case "active is a file":
				err = os.WriteFile(active, []byte("retained active marker"), 0600)
			case "pending repair":
				err = os.Mkdir(active, 0700)
				if err == nil {
					err = os.WriteFile(filepath.Join(active, smartlistJournalName), []byte("retained pending evidence"), 0600)
				}
			case "damaged entry":
				err = os.WriteFile(filepath.Join(dir, entry.ID+".json"), []byte("{damaged history"), 0600)
			case "changed native source":
				_, err = database.Exec(`UPDATE Playlist SET title='External' WHERE id=2`)
			case "missing native source":
				err = database.Close()
				if err == nil {
					err = os.Rename(target, target+".retained")
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			entryPath := filepath.Join(dir, entry.ID+".json")
			before, err := os.ReadFile(entryPath)
			if err != nil {
				t.Fatal(err)
			}
			var nativeBefore string
			if damage != "missing native source" {
				nativeBefore, err = fingerprint(context.Background(), target)
				if err != nil {
					t.Fatal(err)
				}
			}
			if prepared, err := prepareSmartlistRepair(ctx, lib, dir, plan); err == nil || prepared != nil {
				t.Fatalf("unsafe preparation succeeded: %+v %v", prepared, err)
			}
			after, err := os.ReadFile(entryPath)
			if err != nil || string(before) != string(after) {
				t.Fatalf("preparation changed history target: %v", err)
			}
			if damage != "missing native source" {
				if got, err := fingerprint(context.Background(), target); err != nil || got != nativeBefore {
					t.Fatalf("preparation changed native target: %s %v", got, err)
				}
			} else if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("preparation recreated missing native source: %v", err)
			}
			if damage == "pending repair" {
				data, err := os.ReadFile(filepath.Join(active, smartlistJournalName))
				if err != nil || string(data) != "retained pending evidence" {
					t.Fatalf("preparation replaced pending evidence: %q %v", data, err)
				}
			} else if _, err := os.Lstat(filepath.Join(active, smartlistJournalName)); err == nil || !os.IsNotExist(err) && damage != "active is a file" {
				t.Fatalf("failed preparation enrolled pending intent: %v", err)
			}
		})
	}
}
