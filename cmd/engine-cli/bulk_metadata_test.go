package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestBulkMetadataCommand(t *testing.T) {
	lib := createTestLibrary(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("INSERT INTO Track(id,title,path) VALUES(2,'Second','second.mp3')"); err != nil {
		t.Fatal(err)
	}
	result := runJSON(t, lib, "track", "batch", "update", "1", "2", "--artist", "Together", "--key", "8A", "--json")
	if !reflect.DeepEqual(result["track_ids"], []any{float64(1), float64(2)}) {
		t.Fatal(result)
	}
	entries, err := history.List(lib)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v error=%v", entries, err)
	}
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM Track WHERE artist='Together' AND key=1").Scan(&count); err != nil || count != 2 {
		t.Fatalf("count=%d error=%v", count, err)
	}
	for _, mode := range []string{"0", "1"} {
		t.Setenv("AGENT", mode)
		if _, err := executeCommand("-l", lib, "--no-backup", "track", "batch", "update", "1", "2", "--genre", "House"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := executeCommand("-l", lib, "--no-backup", "track", "batch", "update", "1", "999", "--title", "Missing"); err == nil {
		t.Fatal("accepted missing track")
	}
}
