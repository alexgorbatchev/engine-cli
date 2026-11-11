package smartlist

import (
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestConversionUsesNativeOriginsAndStableMembershipOrder(t *testing.T) {
	d := library(t)
	id := create(t, d, "House", 0)
	if _, err := d.Exec(`INSERT INTO Track(id,title,originTrackId,originDatabaseUuid) VALUES(9,'House C',44,'other'),(2,'House B',88,'source'),(1,'House A',99,'source'),(3,'Techno',22,'other')`); err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewConversion(t.Context(), d, id, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert(t.Context(), d, t.TempDir(), ConversionOptions{UUID: id, PlanToken: preview.PlanToken})
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := db.GetPlaylistTracks(t.Context(), d, result.PlaylistID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, track := range tracks {
		ids = append(ids, track.ID)
	}
	if !reflect.DeepEqual(ids, []int64{1, 2, 9}) {
		t.Fatalf("native order=%v", ids)
	}
	if result.TrackCount != 3 {
		t.Fatalf("count=%d", result.TrackCount)
	}
}

func TestConversionStaleNameAndRuleRevision(t *testing.T) {
	for _, change := range []string{"name", "rules"} {
		t.Run(change, func(t *testing.T) {
			d := library(t)
			id := create(t, d, "House", 0)
			if _, err := d.Exec(`INSERT INTO Track(id,title,originTrackId,originDatabaseUuid) VALUES(1,'House',1,'source')`); err != nil {
				t.Fatal(err)
			}
			preview, err := PreviewConversion(t.Context(), d, id, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if change == "name" {
				if _, err := db.CreatePlaylist(t.Context(), d, "House", 0, false); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := d.Exec(`UPDATE Smartlist SET rules=rules || ' ' WHERE listUuid=?`, id); err != nil {
					t.Fatal(err)
				}
			}
			_, err = Convert(t.Context(), d, t.TempDir(), ConversionOptions{UUID: id, PlanToken: preview.PlanToken})
			requireCode(t, err, "smartlist_conversion_changed")
		})
	}
}

func TestConversionAmbiguousPath(t *testing.T) {
	d := library(t)
	a, e := db.CreatePlaylist(t.Context(), d, "A", 0, false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.CreatePlaylist(t.Context(), d, "B", a, false)
	if e != nil {
		t.Fatal(e)
	}
	id := create(t, d, "A;B", 0)
	_, e = d.Exec(`INSERT INTO Track(id,title,originTrackId,originDatabaseUuid) VALUES(1,'House',1,'source')`)
	if e != nil {
		t.Fatal(e)
	}
	p, e := PreviewConversion(t.Context(), d, id, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	result, e := Convert(t.Context(), d, t.TempDir(), ConversionOptions{UUID: id, PlanToken: p.PlanToken})
	if e != nil {
		t.Fatal(e)
	}
	if result.Title != "A;B 2" {
		t.Fatalf("unsafe destination name: %q", result.Title)
	}
	inv, e := db.ReadSmartlistInventoryFrom(t.Context(), d)
	if e != nil {
		t.Fatal(e)
	}
	if len(inv.Issues) > 0 {
		t.Fatalf("successful conversion damaged source Smartlist: %+v", inv.Issues)
	}
}

func TestConversionRollsBackInvalidFinalLinks(t *testing.T) {
	d := library(t)
	id := create(t, d, "House", 0)
	if _, err := d.Exec(`INSERT INTO Track(id,title,originTrackId,originDatabaseUuid) VALUES(1,'House',1,'source')`); err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewConversion(t.Context(), d, id, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TRIGGER corrupt_conversion AFTER INSERT ON Playlist BEGIN UPDATE Smartlist SET parentPlaylistPath='missing;'; END`); err != nil {
		t.Fatal(err)
	}
	_, err = Convert(t.Context(), d, t.TempDir(), ConversionOptions{UUID: id, PlanToken: preview.PlanToken})
	requireCode(t, err, "smartlist_paths_damaged")
	inventory, err := db.ReadSmartlistInventoryFrom(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Playlists) != 0 || len(inventory.Issues) != 0 || len(inventory.Saved) != 1 {
		t.Fatalf("failed conversion escaped rollback: %+v", inventory)
	}
}
