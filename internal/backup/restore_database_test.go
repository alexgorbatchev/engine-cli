package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestRestoreCorruptSourcePreservesDestinationAndJournals(t *testing.T) {
	for _, corruptDestination := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid destination", true: "corrupt destination"}[corruptDestination], func(t *testing.T) {
			root := t.TempDir()
			source, destination := filepath.Join(root, "source.db"), filepath.Join(root, "destination.db")
			if err := os.WriteFile(source, []byte("not a database"), 0600); err != nil {
				t.Fatal(err)
			}
			if corruptDestination {
				if err := os.WriteFile(destination, []byte("damaged destination"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				createSnapshotFixture(t, destination, "live value")
			}
			before, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			journal := destination + "-journal"
			if err := os.WriteFile(journal, []byte("journal owner"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := restoreDatabase(context.Background(), source, destination); err == nil {
				t.Fatal("accepted corrupt source")
			}
			after, err := os.ReadFile(destination)
			if err != nil || string(after) != string(before) {
				t.Fatalf("corrupt source changed destination: %v", err)
			}
			data, err := os.ReadFile(journal)
			if err != nil || string(data) != "journal owner" {
				t.Fatalf("corrupt source changed journal: %q %v", data, err)
			}
		})
	}
}

func TestRestoreValidOpenWALDatabaseUsesNativePublication(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source, destination := filepath.Join(root, "source.db"), filepath.Join(root, "destination.db")
	createSnapshotFixture(t, source, "restored value")
	createSnapshotFixture(t, destination, "old value")
	d, err := db.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; UPDATE fixture SET value='committed live value'"); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := restoreDatabase(ctx, source, destination); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(destination)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("native backup replaced live database inode: %v", err)
	}
	var value string
	if err := d.QueryRow("SELECT value FROM fixture").Scan(&value); err != nil || value != "restored value" {
		t.Fatalf("open connection sees %q: %v", value, err)
	}
}

func TestCorruptDestinationRepairRefusesSidecars(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			root := t.TempDir()
			source, destination := filepath.Join(root, "source.db"), filepath.Join(root, "destination.db")
			createSnapshotFixture(t, source, "restored value")
			if err := os.WriteFile(destination, []byte("damaged destination"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(destination+suffix, []byte("owned sidecar"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := restoreDatabase(context.Background(), source, destination); err == nil {
				t.Fatal("repaired sidecar-bearing corruption")
			}
			for path, want := range map[string]string{destination: "damaged destination", destination + suffix: "owned sidecar"} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != want {
					t.Fatalf("failed repair changed %s: %q %v", path, got, err)
				}
			}
		})
	}
}

func TestRepairPublicationFailurePreservesBothPaths(t *testing.T) {
	root := t.TempDir()
	image, destination := filepath.Join(root, "image.db"), filepath.Join(root, "destination.db")
	createSnapshotFixture(t, image, "validated image")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(destination, "owned")
	if err := os.WriteFile(marker, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishRepair(image, destination); err == nil {
		t.Fatal("published file over nonempty directory")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("failed publication changed destination: %q %v", data, err)
	}
	d, err := db.Open(image, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var value string
	if err := d.QueryRow("SELECT value FROM fixture").Scan(&value); err != nil || value != "validated image" {
		t.Fatalf("failed publication changed staged image: %q %v", value, err)
	}
}
