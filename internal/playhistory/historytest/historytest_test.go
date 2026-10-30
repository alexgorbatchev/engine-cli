package historytest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestWriteCreatesTheLibraryHistoryDatabase(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	Write(t, root, Schema+"INSERT INTO Historylist(title) VALUES('Set');")
	history, err := db.Open(filepath.Join(root, "Database2", "hm.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	var title string
	if err := history.QueryRow("SELECT title FROM Historylist").Scan(&title); err != nil || title != "Set" {
		t.Fatalf("title = %q, %v", title, err)
	}
}

func TestWriteReportsUnwritableHistory(t *testing.T) {
	if err := write(t.TempDir(), Schema); err == nil {
		t.Fatal("wrote history without a Database2 directory")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := write(root, "NOT SQL"); err == nil {
		t.Fatal("accepted invalid statements")
	}
}
