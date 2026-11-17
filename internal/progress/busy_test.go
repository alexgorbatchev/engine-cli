package progress

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func TestErrorCodeReportsBusyLibrariesFromEitherDriver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	holder, err := sqlite.OpenConn(path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := sqlitex.ExecuteTransient(holder, "CREATE TABLE t (v)", nil); err != nil {
		t.Fatal(err)
	}
	if err := sqlitex.ExecuteTransient(holder, "BEGIN IMMEDIATE", nil); err != nil {
		t.Fatal(err)
	}

	contender, err := sqlite.OpenConn(path)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	contender.SetBusyTimeout(0)
	zombiezenErr := sqlitex.ExecuteTransient(contender, "BEGIN IMMEDIATE", nil)
	if zombiezenErr == nil {
		t.Fatal("expected the held write lock to refuse a second writer")
	}

	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, moderncErr := db.ExecContext(context.Background(), "INSERT INTO t VALUES (1)")
	if moderncErr == nil {
		t.Fatal("expected the held write lock to refuse a second writer")
	}

	for name, err := range map[string]error{
		"zombiezen":         zombiezenErr,
		"modernc":           moderncErr,
		"wrapped zombiezen": fmt.Errorf("update tracks: %w", zombiezenErr),
		"wrapped modernc":   fmt.Errorf("update tracks: %w", moderncErr),
	} {
		if got := ErrorCode(err); got != LibraryBusyCode {
			t.Fatalf("%s: got %q want %q", name, got, LibraryBusyCode)
		}
	}
	if got := ErrorCode(errors.New("database is locked")); got != "operation_failed" {
		t.Fatalf("diagnostic text alone must not classify: got %q", got)
	}
}
