package dbtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestLibraryFailures(t *testing.T) {
	existing := Library(t, "existing")
	if err := copyLibrary("existing", existing); err == nil {
		t.Fatal("copied over an existing library")
	}
	if _, err := createLibrary(existing, "existing"); err == nil {
		t.Fatal("created a library over an existing database")
	}
	temp := os.TempDir()
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if err := copyLibrary("uncached", t.TempDir()); err == nil {
		t.Fatal("copied a library without a temporary directory")
	}
	t.Setenv("TMPDIR", temp)
	if _, err := template("uncached"); err != nil {
		t.Fatalf("a failed template was cached: %v", err)
	}
}

func TestLibraryCopiesIndependentLibraries(t *testing.T) {
	first := Library(t, "source")
	second := Library(t, "source")
	other := Library(t, "dest")
	if first == second {
		t.Fatal("libraries share a directory")
	}
	d, err := db.Open(filepath.Join(first, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("INSERT INTO Track(title,path) VALUES('Only here','a.mp3')"); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		root, uuid string
		tracks     int
	}{{first, "source", 1}, {second, "source", 0}, {other, "dest", 0}} {
		d, err := db.Open(filepath.Join(tt.root, "Database2", "m.db"))
		if err != nil {
			t.Fatal(err)
		}
		var uuid string
		var tracks int
		if err := d.QueryRow("SELECT uuid, (SELECT count(*) FROM Track) FROM Information").Scan(&uuid, &tracks); err != nil {
			t.Fatal(err)
		}
		d.Close()
		if uuid != tt.uuid || tracks != tt.tracks {
			t.Fatalf("%s: uuid %q with %d tracks, want %q with %d", tt.root, uuid, tracks, tt.uuid, tt.tracks)
		}
	}
}
