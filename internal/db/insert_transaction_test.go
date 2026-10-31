package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestInsertTrackSharesNativeTransaction(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	uuid, err := CreateLibrary(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, commit := range []bool{false, true} {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				t.Error(err)
			}
		})
		insertNativeTrack(t, ctx, tx, uuid)
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"Track", "PerformanceData"} {
			var count int
			if err := d.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if (commit && count != 1) || (!commit && count != 0) {
				t.Fatalf("commit %v leaked or lost %s: %d", commit, table, count)
			}
		}
	}
}

func insertNativeTrack(t *testing.T, ctx context.Context, tx *sql.Tx, uuid string) {
	t.Helper()
	track := &Track{Title: sql.NullString{String: "Imported", Valid: true}, Path: sql.NullString{String: "Music/import.wav", Valid: true}}
	id, err := InsertTrack(ctx, tx, track)
	if err != nil {
		t.Fatal(err)
	}
	var origin string
	var originID, performanceID int64
	if err := tx.QueryRowContext(ctx, `SELECT originDatabaseUuid,originTrackId FROM Track WHERE id=?`, id).Scan(&origin, &originID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT trackId FROM PerformanceData WHERE trackId=?`, id).Scan(&performanceID); err != nil {
		t.Fatal(err)
	}
	if id != track.ID || origin != uuid || originID != id || performanceID != id {
		t.Fatalf("native origin or performance trigger lost: %+v, %s:%d, performance %d", track, origin, originID, performanceID)
	}
}
