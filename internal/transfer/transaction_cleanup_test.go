package transfer

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestSyncFailedNativeTransactionReleasesWriter(t *testing.T) {
	for _, failure := range []string{"cancel", "commit busy"} {
		t.Run(failure, func(t *testing.T) {
			source, s := library(t, "source")
			destination, d := library(t, "destination")
			execSQL(t, s, `INSERT INTO Smartlist(listUuid,title,rules) VALUES('saved','Saved',x'007fff')`)
			execSQL(t, d, "PRAGMA journal_mode=DELETE")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var reader *sql.Tx
			defer func() {
				if reader != nil {
					if err := reader.Rollback(); err != nil {
						t.Error(err)
					}
				}
			}()
			reached := false
			ctx = progress.WithReporter(ctx, func(event progress.Event) {
				if event.Phase != "sync.finalize" {
					return
				}
				reached = true
				if failure == "cancel" {
					cancel()
					return
				}
				var err error
				reader, err = d.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				var count int
				if err := reader.QueryRow("SELECT count(*) FROM Smartlist").Scan(&count); err != nil || count != 0 {
					t.Fatalf("reader observed uncommitted rows: %d %v", count, err)
				}
			})
			o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, NoBackup: true}
			_, err := Run(ctx, o)
			if !reached || err == nil {
				t.Fatalf("transaction failure was not reached: reached=%t error=%v", reached, err)
			}
			if failure == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
			} else {
				var native *sqlite.Error
				if !errors.As(err, &native) || native.Code() != sqlite3.SQLITE_BUSY {
					t.Fatalf("failure did not reach native commit contention: %v", err)
				}
			}
			if reader != nil {
				if err := reader.Rollback(); err != nil {
					t.Fatal(err)
				}
				reader = nil
			}
			if actual := savedRows(t, d); len(actual) != 0 {
				t.Fatalf("failed transaction published rows: %#v", actual)
			}
			if _, err := os.Stat(statePath(destination)); !os.IsNotExist(err) {
				t.Fatalf("failed native commit published sync state: %v", err)
			}
			entries, err := history.List(destination)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.State == "pending" || entry.After == nil {
					t.Fatalf("cleanup left unresolved history: %+v", entry)
				}
			}
			// A subsequent protected mutation must acquire the native writer and
			// observe the rolled-back state without forced recovery.
			applied, err := Run(t.Context(), o)
			if err != nil || applied.OperationID == "" || len(savedRows(t, d)) != 1 {
				t.Fatalf("writer or recovery state leaked: %+v %v", applied, err)
			}
		})
	}
}
