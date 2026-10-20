package duplicate

import (
	"context"
	"testing"
)

func TestMergeOriginReferencesAndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "merge", true: "rollback"}[fail], func(t *testing.T) {
			d := library(t)
			seed(t, d)
			execSQL(t, d, `INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Shared',0,0),('Other',0,1);
		INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,101,'first',2),(1,202,'second',3),(1,5,'local',0),(2,202,'second',0),(2,2,'foreign',0);
		INSERT INTO PreparelistEntity(trackId,trackNumber) VALUES(2,1);
		UPDATE PerformanceData SET quickCues=x'0102',loops=x'03' WHERE trackId=2;`)
			if fail {
				execSQL(t, d, `CREATE TRIGGER refuse_delete BEFORE DELETE ON Track BEGIN SELECT RAISE(ABORT,'refused'); END`)
			}
			_, err := Merge(context.Background(), d, MergeOptions{Keep: 1, Remove: []int64{2}, PerformanceFrom: 2})
			if fail {
				if err == nil {
					t.Fatal("accepted failing write")
				}
				var n int
				if err = d.QueryRow("SELECT count(*) FROM PlaylistEntity WHERE trackId=202").Scan(&n); err != nil || n != 2 {
					t.Fatalf("partial writes n=%d err=%v", n, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for query, want := range map[string]int{
				"SELECT count(*) FROM Track WHERE id=2":                                          0,
				"SELECT count(*) FROM PlaylistEntity WHERE trackId=101 AND databaseUuid='first'": 2,
				"SELECT count(*) FROM PlaylistEntity WHERE trackId=2 AND databaseUuid='foreign'": 1,
				"SELECT nextEntityId FROM PlaylistEntity WHERE id=1":                             3,
				"SELECT trackId FROM PreparelistEntity":                                          1,
				"SELECT timeLastPlayed FROM Track WHERE id=1":                                    200,
				"SELECT count(*) FROM PerformanceData WHERE trackId=2":                           0,
				"SELECT length(quickCues) FROM PerformanceData WHERE trackId=1":                  2,
			} {
				var got int
				if err = d.QueryRow(query).Scan(&got); err != nil || got != want {
					t.Fatalf("%s got=%d want=%d err=%v", query, got, want, err)
				}
			}
		})
	}
}

func TestMergeRejectsInvalidSelection(t *testing.T) {
	for _, opts := range []MergeOptions{{Keep: 1}, {Keep: 1, Remove: []int64{1}}, {Keep: 1, Remove: []int64{2, 2}}, {Keep: 999, Remove: []int64{2}}, {Keep: 1, Remove: []int64{999}}, {Keep: 1, Remove: []int64{5}}, {Keep: 1, Remove: []int64{2}, PerformanceFrom: 3}} {
		d := library(t)
		seed(t, d)
		if _, err := Merge(context.Background(), d, opts); err == nil {
			t.Fatalf("accepted %+v", opts)
		}
	}
}

func TestMergePerformanceMustExist(t *testing.T) {
	for _, id := range []int{1, 2} {
		d := library(t)
		seed(t, d)
		if _, err := d.Exec("DELETE FROM PerformanceData WHERE trackId=?", id); err != nil {
			t.Fatal(err)
		}
		if _, err := Merge(context.Background(), d, MergeOptions{Keep: 1, Remove: []int64{2}, PerformanceFrom: 2}); err == nil {
			t.Fatal("accepted missing performance data")
		}
		var n int
		if err := d.QueryRow("SELECT count(*) FROM Track WHERE id IN (1,2)").Scan(&n); err != nil || n != 2 {
			t.Fatalf("lost tracks: %d %v", n, err)
		}
	}
}

func TestMergeResultReflectsChanges(t *testing.T) {
	d := library(t)
	seed(t, d)
	r, err := Merge(context.Background(), d, MergeOptions{Keep: 1, Remove: []int64{2}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Kept.TimeLastPlayed != 200 {
		t.Fatalf("stale result: %+v", r.Kept)
	}
}
