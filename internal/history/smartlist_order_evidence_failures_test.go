package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistOrderRejectsHistoryChangedAfterIdentityObservation(t *testing.T) {
	for _, kind := range []string{"missing entry", "changed operation", "missing before image", "changed native identity"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			entry := recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
			evidence, err := readSmartlistEvidence(ctx, lib)
			if err != nil {
				t.Fatal(err)
			}
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			entryPath := filepath.Join(dir, entry.ID+".json")
			switch kind {
			case "missing entry":
				err = os.Remove(entryPath)
			case "changed operation":
				entry.Operation = "playlist.move"
				err = writeEntry(dir, entry)
			case "missing before image":
				entry.Before = nil
				err = writeEntry(dir, entry)
			case "changed native identity":
				target := filepath.Join(lib, "Database2", "m.db")
				state := entry.Before.Databases[target]
				image, openErr := db.Open(state.Path)
				if openErr != nil {
					t.Fatal(openErr)
				}
				_, mutationErr := image.Exec(`UPDATE Playlist SET title='External historical identity' WHERE id=1`)
				closeErr := image.Close()
				if mutationErr != nil || closeErr != nil {
					t.Fatalf("external image mutation: %v %v", mutationErr, closeErr)
				}
				state.Hash, err = fingerprint(ctx, state.Path)
				if err == nil {
					entry.Before.Databases[target] = state
					err = writeEntry(dir, entry)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before, readErr := os.ReadFile(entryPath)
			if kind != "missing entry" && readErr != nil {
				t.Fatal(readErr)
			}
			live := filepath.Join(lib, "Database2", "m.db")
			hash, err := fingerprint(ctx, live)
			if err != nil {
				t.Fatal(err)
			}
			if orders, err := readSmartlistOrderEvidence(ctx, lib, evidence); err == nil || orders != nil {
				t.Fatalf("mixed-time history granted native order evidence: %+v %v", orders, err)
			}
			if actual, err := fingerprint(ctx, live); err != nil || actual != hash {
				t.Fatalf("refusal changed current native library: %s %v", actual, err)
			}
			after, err := os.ReadFile(entryPath)
			if kind == "missing entry" {
				if !os.IsNotExist(err) {
					t.Fatalf("reader recreated missing history: %v", err)
				}
			} else if err != nil || string(after) != string(before) {
				t.Fatalf("reader changed external history evidence: %v", err)
			}
		})
	}
}
