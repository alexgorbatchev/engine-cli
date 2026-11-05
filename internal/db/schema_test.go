package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSchemaInitializationIsAtomic(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	// SQLite refuses an AFTER INSERT trigger on a view. This fails after the
	// Information table and its initial row would otherwise have committed.
	if _, err := d.Exec("CREATE VIEW Track AS SELECT 1 AS id"); err != nil {
		t.Fatal(err)
	}
	if err := InitSchema(t.Context(), d); err == nil {
		t.Fatal("schema conflict succeeded")
	}
	var count int
	if err := d.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name='Information'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed initialization retained partial schema: count=%d err=%v", count, err)
	}
	if _, err := d.Exec("DROP VIEW Track"); err != nil {
		t.Fatal(err)
	}
	if err := InitSchema(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO Track(id,title) VALUES(42,'Retained')"); err != nil {
		t.Fatal(err)
	}
	if err := InitSchema(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	var title, origin string
	var id int
	if err := d.QueryRow("SELECT title,originDatabaseUuid,originTrackId FROM Track WHERE id=42").Scan(&title, &origin, &id); err != nil || title != "Retained" || origin != "fixture" || id != 42 {
		t.Fatalf("schema retry damaged fixture semantics: %q %q %d %v", title, origin, id, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := InitSchema(ctx, d); err == nil {
		t.Fatal("cancelled initialization succeeded")
	}
}
