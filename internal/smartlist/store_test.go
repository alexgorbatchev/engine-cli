package smartlist

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/db/dbtest"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func library(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(dbtest.Library(t, "rules-test"), "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	return d
}
func document() Document {
	return Document{Match: "all", TimeZone: "UTC", Rules: []Rule{{Filter: "title", Condition: "contains", Value: "house"}}}
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var failure *progress.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("got %v; want %s", err, code)
	}
}
func create(t *testing.T, d *sql.DB, name string, parent int64) string {
	t.Helper()
	id, err := Create(t.Context(), d, CreateOptions{Title: name, ParentID: parent, Document: document()})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func details(t *testing.T, d *sql.DB, id string) *Details {
	t.Helper()
	got, err := Get(t.Context(), d, id, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func TestCreateMaintainsNativeSiblingChains(t *testing.T) {
	d := library(t)
	if _, err := db.CreatePlaylist(t.Context(), d, "Crates", 0, false); err != nil {
		t.Fatal(err)
	}
	first := create(t, d, "House", 0)
	second := create(t, d, "Techno", 0)
	child := create(t, d, "House", 1)
	if _, err := db.CreatePlaylist(t.Context(), d, "House", 0, false); err != nil {
		t.Fatal(err)
	}
	got := details(t, d, first)
	if got.Title != "House" || got.ReadOnly || got.Match != "all" || got.Rules[0].Value != "house" || got.Rules[0].OriginalIndex == nil || *got.Rules[0].OriginalIndex != 0 {
		t.Fatalf("unexpected details %#v", got)
	}
	for _, c := range []struct{ id, parent, next string }{{first, "", second}, {second, "", ""}, {child, "Crates;", ""}} {
		var parent, next, playlist string
		if err := d.QueryRow(`SELECT parentPlaylistPath,nextListUuid,nextPlaylistPath FROM Smartlist WHERE listUuid=?`, c.id).Scan(&parent, &next, &playlist); err != nil {
			t.Fatal(err)
		}
		if parent != c.parent || next != c.next || playlist != "" {
			t.Fatalf("broken placement %q: %q %q %q", c.id, parent, next, playlist)
		}
	}
	_, err := Create(t.Context(), d, CreateOptions{Title: "House", Document: document()})
	requireCode(t, err, "smartlist_name_conflict")
	var tail string
	if err := d.QueryRow(`SELECT nextListUuid FROM Smartlist WHERE listUuid=?`, second).Scan(&tail); err != nil {
		t.Fatal(err)
	}
	if tail != "" {
		t.Fatalf("failed insert altered tail %q", tail)
	}
}
func TestUpdatePreservesIdentityAndRefusesStaleWrites(t *testing.T) {
	d := library(t)
	id := create(t, d, "House", 0)
	before := details(t, d, id)
	var timestamp string
	if err := d.QueryRow(`SELECT lastEditTime FROM Smartlist WHERE listUuid=?`, id).Scan(&timestamp); err != nil {
		t.Fatal(err)
	}
	opts := UpdateOptions{UUID: id, Title: "  Deep house  ", Revision: before.Revision, Document: before.Document}
	opts.Rules = append([]Rule(nil), opts.Rules...)
	opts.Rules[0].Value = "deep"
	if err := Update(t.Context(), d, opts); err != nil {
		t.Fatal(err)
	}
	after := details(t, d, id)
	if after.Title != "Deep house" || after.Rules[0].Value != "deep" || after.Revision == before.Revision {
		t.Fatalf("bad update %#v", after)
	}
	var changed string
	if err := d.QueryRow(`SELECT lastEditTime FROM Smartlist WHERE listUuid=?`, id).Scan(&changed); err != nil {
		t.Fatal(err)
	}
	if changed != timestamp {
		t.Fatal("changed Desktop timestamp")
	}
	requireCode(t, Update(t.Context(), d, opts), "smartlist_changed")
	create(t, d, "Taken", 0)
	opts.Revision = after.Revision
	opts.Title = "Taken"
	requireCode(t, Update(t.Context(), d, opts), "smartlist_name_conflict")
	if !reflect.DeepEqual(details(t, d, id), after) {
		t.Fatal("failed update changed data")
	}
	opts.Title = " "
	requireCode(t, Update(t.Context(), d, opts), "smartlist_rules_invalid")
	opts.Title = "Valid"
	opts.UUID = "missing"
	requireCode(t, Update(t.Context(), d, opts), "smartlist_not_found")
}
func TestUnsupportedRulesCannotBeOverwritten(t *testing.T) {
	d := library(t)
	id := create(t, d, "Future", 0)
	for _, raw := range []any{nil, "opaque", `{"rv":99,"match":"all","rules":[]}`} {
		if _, err := d.Exec(`UPDATE Smartlist SET rules=? WHERE listUuid=?`, raw, id); err != nil {
			t.Fatal(err)
		}
		before := details(t, d, id)
		if !before.ReadOnly || before.Reason != "smartlist_rules_read_only" || len(before.Rules) != 0 {
			t.Fatalf("unsafe detail %#v", before)
		}
		requireCode(t, Update(t.Context(), d, UpdateOptions{UUID: id, Title: "Overwrite", Revision: before.Revision, Document: document()}), "smartlist_rules_read_only")
		if !reflect.DeepEqual(details(t, d, id), before) {
			t.Fatal("read-only data changed")
		}
	}
	_, err := Get(t.Context(), d, "missing", "UTC")
	requireCode(t, err, "smartlist_not_found")
	_, err = Get(t.Context(), d, id, "not/a-zone")
	requireCode(t, err, "smartlist_rules_invalid")
}
func TestCreateRefusesUnsafePlacement(t *testing.T) {
	for _, scenario := range []string{"missing-parent", "damaged", "old-schema", "blank", "invalid-rules"} {
		t.Run(scenario, func(t *testing.T) {
			d := library(t)
			opts := CreateOptions{Title: "New", Document: document()}
			code := "smartlist_rules_invalid"
			switch scenario {
			case "missing-parent":
				opts.ParentID = 99
				code = "smartlist_parent_not_found"
			case "damaged":
				id := create(t, d, "Existing", 0)
				if _, err := d.Exec(`UPDATE Smartlist SET parentPlaylistPath='Missing;' WHERE listUuid=?`, id); err != nil {
					t.Fatal(err)
				}
				code = "smartlist_paths_damaged"
			case "old-schema":
				if _, err := d.Exec(`DROP TABLE Smartlist`); err != nil {
					t.Fatal(err)
				}
				code = "smartlist_schema_unavailable"
			case "blank":
				opts.Title = " "
			case "invalid-rules":
				opts.Match = "invalid"
			}
			_, err := Create(t.Context(), d, opts)
			requireCode(t, err, code)
		})
	}
}

func TestCreateAfterNativeNullTerminal(t *testing.T) {
	d := library(t)
	if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('nullable','Existing',NULL,NULL,NULL,'opaque')`); err != nil {
		t.Fatal(err)
	}
	_, err := Create(t.Context(), d, CreateOptions{Title: "Existing", Document: document()})
	requireCode(t, err, "smartlist_name_conflict")
	id := create(t, d, "New", 0)
	var next string
	if err := d.QueryRow(`SELECT nextListUuid FROM Smartlist WHERE listUuid='nullable'`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next != id {
		t.Fatalf("nullable terminal not linked: %q != %q", next, id)
	}
	inventory, err := db.ReadSmartlistInventory(t.Context(), d)
	if err != nil || len(inventory.Issues) > 0 {
		t.Fatalf("damaged inventory %#v: %v", inventory, err)
	}
}
