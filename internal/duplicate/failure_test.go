package duplicate

import (
	"context"
	"fmt"
	"testing"
)

func TestMergeWriteFailuresRollback(t *testing.T) {
	for _, tt := range []struct{ table, event string }{
		{"PerformanceData", "UPDATE"}, {"Track", "UPDATE"}, {"PerformanceData", "DELETE"}, {"Track", "DELETE"},
		{"PlaylistEntity", "UPDATE"}, {"PlaylistEntity", "DELETE"}, {"PreparelistEntity", "DELETE"}, {"PreparelistEntity", "UPDATE"},
	} {
		t.Run(tt.table+tt.event, func(t *testing.T) {
			d := library(t)
			seed(t, d)
			execSQL(t, d, `INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Shared',0,0);
    INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,101,'first',2),(1,202,'second',0);
    INSERT INTO PreparelistEntity(trackId,trackNumber) VALUES(2,1);`)
			if tt.table == "PreparelistEntity" && tt.event == "DELETE" {
				execSQL(t, d, "INSERT INTO PreparelistEntity(trackId,trackNumber) VALUES(1,2)")
			}
			execSQL(t, d, fmt.Sprintf("CREATE TRIGGER stop_write BEFORE %s ON %s BEGIN SELECT RAISE(ABORT,'write refused'); END", tt.event, tt.table))
			if _, err := Merge(context.Background(), d, MergeOptions{Keep: 1, Remove: []int64{2}, PerformanceFrom: 2}); err == nil {
				t.Fatal("accepted refused write")
			}
			var n int
			if err := d.QueryRow("SELECT count(*) FROM Track WHERE id IN (1,2)").Scan(&n); err != nil || n != 2 {
				t.Fatalf("partial merge %d %v", n, err)
			}
		})
	}
}

func TestDuplicateMalformedLibraries(t *testing.T) {
	for _, q := range []string{"DROP TABLE PlaylistEntity", "UPDATE Track SET length='invalid' WHERE id=1", "DROP TABLE PerformanceData"} {
		d := library(t)
		seed(t, d)
		execSQL(t, d, q)
		if _, err := Inspect(context.Background(), d, 1); err == nil {
			t.Fatalf("accepted %s", q)
		}
		if _, err := Merge(context.Background(), d, MergeOptions{Keep: 1, Remove: []int64{2}}); err == nil {
			t.Fatalf("merged %s", q)
		}
	}
	d := library(t)
	seed(t, d)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := List(context.Background(), d, "all"); err == nil {
		t.Fatal("read closed database")
	}
	if _, err := Merge(context.Background(), d, MergeOptions{Keep: 1, Remove: []int64{2}}); err == nil {
		t.Fatal("merged closed database")
	}
}

func TestDuplicateMissingIdentity(t *testing.T) {
	for _, id := range []int{1, 2} {
		d := library(t)
		seed(t, d)
		execSQL(t, d, "UPDATE Track SET originTrackId=-1 WHERE id=?", id)
		if _, err := Merge(context.Background(), d, MergeOptions{Keep: 1, Remove: []int64{2}}); err == nil {
			t.Fatal("merged without identity")
		}
	}
}

func TestDuplicateMultipleGroups(t *testing.T) {
	d := library(t)
	seed(t, d)
	execSQL(t, d, `INSERT INTO Track(title,artist,path,originDatabaseUuid,originTrackId) VALUES('Solo','Artist','six.mp3','local',6)`)
	groups, err := List(context.Background(), d, "all")
	if err != nil || len(groups) != 2 || groups[0].Tracks[0].ID != 1 || groups[1].Tracks[0].ID != 5 {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
}
