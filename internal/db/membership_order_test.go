package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func nativeMembershipDB(t *testing.T) *sql.DB {
	t.Helper()
	root := t.TempDir()
	if _, err := CreateLibrary(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	d, err := Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestGetPlaylistTracksNativeOrder(t *testing.T) {
	d := nativeMembershipDB(t)
	ctx := context.Background()
	statements := []string{
		`INSERT INTO Playlist(id,title,parentListId) VALUES(1,'Ordered',0),(2,'Other',0)`,
		`INSERT INTO Track(id,originTrackId,originDatabaseUuid,title) VALUES(10,101,'source','First'),(20,102,'source','Last'),(30,101,'foreign','Foreign')`,
		// Native entity order is 300 -> 200 (unresolved origin) -> 100.
		`INSERT INTO PlaylistEntity(id,listId,trackId,databaseUuid,nextEntityId) VALUES(100,1,102,'source',0),(200,1,999,'source',100),(300,1,101,'source',200),(400,2,101,'foreign',0)`,
	}
	for _, statement := range statements {
		if _, err := d.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		list int64
		want []int64
	}{{1, []int64{10, 20}}, {2, []int64{30}}, {99, nil}} {
		tracks, err := GetPlaylistTracks(ctx, d, tc.list)
		if err != nil {
			t.Fatal(err)
		}
		var got []int64
		for _, track := range tracks {
			got = append(got, track.ID)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("playlist %d tracks = %v, want %v", tc.list, got, tc.want)
		}
	}
}

func TestGetPlaylistTracksRefusesInvalidOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows string
	}{
		{"dangling", `(1,1,1,'fixture',999)`},
		{"branch", `(1,1,1,'fixture',3),(2,1,2,'fixture',3),(3,1,3,'fixture',0)`},
		{"multiple heads", `(1,1,1,'fixture',0),(2,1,2,'fixture',0)`},
		{"cycle", `(1,1,1,'fixture',2),(2,1,2,'fixture',1)`},
		{"disconnected cycle", `(1,1,1,'fixture',0),(2,1,2,'fixture',3),(3,1,3,'fixture',2)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := nativeMembershipDB(t)
			ctx := context.Background()
			if _, err := d.ExecContext(ctx, `INSERT INTO Playlist(id,title,parentListId) VALUES(1,'Invalid',0); INSERT INTO Track(id,title) VALUES(1,'One'),(2,'Two'),(3,'Three')`); err != nil {
				t.Fatal(err)
			}
			if _, err := d.ExecContext(ctx, `INSERT INTO PlaylistEntity(id,listId,trackId,databaseUuid,nextEntityId) VALUES`+tc.rows); err != nil {
				t.Fatal(err)
			}
			if _, err := GetPlaylistTracks(ctx, d, 1); progress.ErrorCode(err) != "playlist_membership_order_invalid" {
				t.Fatalf("invalid chain error = %v", err)
			}
		})
	}
}

func TestGetPlaylistTracksRefusesAmbiguousOrigin(t *testing.T) {
	// InitSchema deliberately permits this damaged state; the native schema's
	// origin uniqueness constraint normally prevents it.
	d, _ := createTestDB(t)
	ctx := context.Background()
	if _, err := d.ExecContext(ctx, `INSERT INTO Track(id,originTrackId,originDatabaseUuid) VALUES(1,10,'duplicate'),(2,10,'duplicate'); INSERT INTO PlaylistEntity(id,listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,10,'duplicate',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := GetPlaylistTracks(ctx, d, 1); progress.ErrorCode(err) != "playlist_membership_order_invalid" {
		t.Fatalf("ambiguous origin error = %v", err)
	}
}

func TestOrderPlaylistMemberships(t *testing.T) {
	for _, tc := range []struct {
		name  string
		links map[int64]int64
		want  []int64
		bad   bool
	}{
		{"empty", nil, []int64{}, false},
		{"native order", map[int64]int64{3: 1, 1: 2, 2: 0}, []int64{3, 1, 2}, false},
		{"zero identity", map[int64]int64{0: 0}, nil, true},
		{"negative identity", map[int64]int64{-1: 0}, nil, true},
		{"negative next", map[int64]int64{1: -1}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := OrderPlaylistMemberships(tc.links)
			if tc.bad {
				if progress.ErrorCode(err) != "playlist_membership_order_invalid" {
					t.Fatalf("invalid membership error = %v", err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("order = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
