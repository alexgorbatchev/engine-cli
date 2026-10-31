package intake

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestPreparationRefusesUnusableWorkerOwner(t *testing.T) {
	for _, name := range []string{"directory", "symlink", "invalid database", "missing parent"} {
		t.Run(name, func(t *testing.T) {
			store, library := queueFixture(t)
			source := sourceFixture(t, "retained.wav")
			item, err := store.Enqueue(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(library, ".engine-cli", "intake-owner.db")
			switch name {
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				target := filepath.Join(library, "unrelated-owner.db")
				unrelated, openErr := db.Open(target)
				if openErr != nil {
					t.Fatal(openErr)
				}
				if _, err := unrelated.Exec("CREATE TABLE Retained(value TEXT)"); err != nil {
					t.Fatal(err)
				}
				if err := unrelated.Close(); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(target, path)
			case "invalid database":
				err = os.WriteFile(path, []byte("retained invalid worker database"), 0600)
			case "missing parent":
				store.library = filepath.Join(library, "absent")
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			got, err := store.PrepareNext(t.Context(), 0)
			if got != nil || err == nil {
				t.Fatalf("worker owner refusal lost: %+v, %v", got, err)
			}
			items, err := store.List(t.Context())
			if err != nil || len(items) != 1 || items[0].ID != item.ID || items[0].Status != "queued" || items[0].Attempt != 0 {
				t.Fatalf("refused worker changed durable queue: %+v, %v", items, err)
			}
			after, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("worker refusal changed source: %v", err)
			}
		})
	}
}
