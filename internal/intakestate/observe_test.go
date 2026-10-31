package intakestate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func TestObservationWithoutOwnerDoesNotEnroll(t *testing.T) {
	library := t.TempDir()
	called := false
	present, err := Observe(context.Background(), library, func(context.Context) error { called = true; return nil })
	if err != nil || present || called {
		t.Fatalf("absent observation: present=%v called=%v err=%v", present, called, err)
	}
	if _, err := os.Stat(filepath.Join(library, ".engine-cli")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("observation enrolled storage: %v", err)
	}
}

func TestNativeObservationAndMutationExcludeEachOther(t *testing.T) {
	for _, first := range []string{"observer", "writer"} {
		t.Run(first, func(t *testing.T) {
			library := t.TempDir()
			if err := With(context.Background(), library, func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			done := make(chan error, 1)
			hold := func(ctx context.Context) error { close(entered); <-release; return ctx.Err() }
			go func() {
				if first == "observer" {
					_, err := Observe(ctx, library, hold)
					done <- err
				} else {
					done <- With(ctx, library, hold)
				}
			}()
			<-entered
			cancel()
			wait, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer stop()
			called := false
			callback := func(context.Context) error { called = true; return nil }
			var err error
			if first == "observer" {
				err = With(wait, library, callback)
			} else {
				_, err = Observe(wait, library, callback)
			}
			unblock()
			if !errors.Is(err, context.DeadlineExceeded) || called {
				t.Fatalf("cancelled %s released ownership before callback joined: called=%v err=%v", first, called, err)
			}
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal("callback cancellation lost:", err)
			}
			want := errors.New("observation callback refused")
			present, err := Observe(context.Background(), library, func(context.Context) error { return want })
			if !present || !errors.Is(err, want) {
				t.Fatalf("callback refusal lost: present=%v err=%v", present, err)
			}
			if err := With(context.Background(), library, func(context.Context) error { return nil }); err != nil {
				t.Fatal("observation leaked owner:", err)
			}
		})
	}
}

func TestNativeStateRefusesWALBeforeCallback(t *testing.T) {
	library := t.TempDir()
	if err := With(context.Background(), library, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	d, err := sqlite.OpenConn(filepath.Join(library, ".engine-cli", ownerName), sqlite.OpenReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlitex.ExecuteTransient(d, "PRAGMA journal_mode=WAL", nil); err != nil {
		t.Fatal(err)
	}
	if err := sqlitex.ExecuteScript(d, "CREATE TABLE proof(value TEXT); INSERT INTO proof VALUES('preserve');", nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	called := false
	callback := func(context.Context) error { called = true; return nil }
	if err := With(context.Background(), library, callback); err == nil || called {
		t.Fatalf("WAL writer accepted: called=%v err=%v", called, err)
	}
	if _, err := Observe(context.Background(), library, callback); err == nil || called {
		t.Fatalf("WAL observation accepted: called=%v err=%v", called, err)
	}
	d, err = sqlite.OpenConn(filepath.Join(library, ".engine-cli", ownerName), sqlite.OpenReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var value string
	if err := sqlitex.ExecuteTransient(d, "SELECT value FROM proof", &sqlitex.ExecOptions{ResultFunc: func(stmt *sqlite.Stmt) error { value = stmt.ColumnText(0); return nil }}); err != nil || value != "preserve" {
		t.Fatalf("refusal changed contents: value=%q err=%v", value, err)
	}
}

func TestNativeModePreflightCanBecomeStaleBeforeAcquisition(t *testing.T) {
	for _, acquisition := range []string{"BEGIN EXCLUSIVE", "BEGIN; SELECT count(*) FROM sqlite_schema"} {
		t.Run(acquisition, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "owner.db")
			first, err := sqlite.OpenConn(path, sqlite.OpenReadWrite|sqlite.OpenCreate)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			if err := rollbackMode(first); err != nil {
				t.Fatal("initial rollback mode:", err)
			}
			second, err := sqlite.OpenConn(path, sqlite.OpenReadWrite)
			if err != nil {
				t.Fatal(err)
			}
			if err := sqlitex.ExecuteTransient(second, "PRAGMA journal_mode=WAL", nil); err != nil {
				t.Fatal("native concurrent mode switch:", err)
			}
			if err := second.Close(); err != nil {
				t.Fatal(err)
			}
			if acquisition == "BEGIN EXCLUSIVE" {
				err = sqlitex.ExecuteTransient(first, acquisition, nil)
			} else {
				err = sqlitex.ExecuteTransient(first, "BEGIN", nil)
				if err == nil {
					err = sqlitex.ExecuteTransient(first, "SELECT count(*) FROM sqlite_schema", nil)
				}
			}
			if err != nil {
				t.Fatal("native acquisition:", err)
			}
			if err := rollbackMode(first); err == nil {
				t.Fatal("post-acquisition validation accepted changed native mode")
			}
		})
	}
}
