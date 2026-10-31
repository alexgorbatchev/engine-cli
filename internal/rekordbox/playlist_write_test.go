package rekordbox

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestWriteHierarchyUsesNativeLinksAndExplicitMissingDispositions(t *testing.T) {
	ctx := context.Background()
	d := importDatabase(t)
	tracks := map[string]int64{"missing": 0}
	for _, name := range []string{"a", "b"} {
		track := db.Track{Path: text("/music/" + name + ".wav"), Title: text(name)}
		id, err := db.InsertTrack(ctx, d, &track)
		if err != nil {
			t.Fatal(err)
		}
		tracks[name] = id
	}
	nodes := []Playlist{{Name: "Folder", Folder: true, Children: []Playlist{{Name: "Ordered", Tracks: []string{"b", "missing", "a", "b", "a"}}, {Name: "Empty", Folder: true}}}}
	warnings := hierarchyWarnings(nodes)
	if len(warnings) != 3 || warnings[0].Field != "RepeatedMembership" || warnings[0].SourceID != "b" || warnings[1].SourceID != "a" || warnings[2].Field != "EmptyFolder" {
		t.Fatalf("native representation limits were not disclosed: %+v", warnings)
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := writeHierarchy(ctx, tx, 0, nodes, tracks); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var parent, child int64
	if err := d.QueryRow("SELECT id FROM Playlist WHERE title='Folder' AND parentListId=0").Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow("SELECT id FROM Playlist WHERE title='Ordered' AND parentListId=?", parent).Scan(&child); err != nil {
		t.Fatal(err)
	}
	got := savedMemberships(t, d, child)
	if !slices.Equal(got, []string{"b", "a"}) {
		t.Fatalf("native first-occurrence membership order changed: %v", got)
	}
	var empty int
	if err := d.QueryRow("SELECT COUNT(*) FROM Playlist WHERE title='Empty' AND parentListId=?", parent).Scan(&empty); err != nil || empty != 1 {
		t.Fatalf("empty source node lost: %d, %v", empty, err)
	}
}

func TestWriteHierarchyFailureRemainsWithinCallerTransaction(t *testing.T) {
	for _, name := range []string{"unreviewed selector", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			d := importDatabase(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tx, err := d.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if name == "cancelled" {
				cancel()
			}
			nodes := []Playlist{{Name: "Should roll back", Tracks: []string{"unreviewed"}}}
			if err := writeHierarchy(ctx, tx, 0, nodes, map[string]int64{}); err == nil {
				t.Fatal("unreviewed or cancelled hierarchy was accepted")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := d.QueryRow("SELECT COUNT(*) FROM Playlist").Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial hierarchy survived: %d, %v", count, err)
			}
		})
	}
}

func savedMemberships(t *testing.T, d *sql.DB, playlist int64) []string {
	t.Helper()
	rows, err := d.Query(`WITH RECURSIVE saved(id,next,origin,uuid,ordinal) AS (
	 SELECT pe.id,pe.nextEntityId,pe.trackId,pe.databaseUuid,0 FROM PlaylistEntity pe
	 WHERE pe.listId=? AND NOT EXISTS(SELECT 1 FROM PlaylistEntity prev WHERE prev.listId=pe.listId AND prev.nextEntityId=pe.id)
	 UNION ALL SELECT pe.id,pe.nextEntityId,pe.trackId,pe.databaseUuid,saved.ordinal+1 FROM PlaylistEntity pe JOIN saved ON pe.id=saved.next
	) SELECT t.title FROM saved JOIN Track t ON t.originTrackId=saved.origin AND t.originDatabaseUuid=saved.uuid ORDER BY saved.ordinal`, playlist)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}
