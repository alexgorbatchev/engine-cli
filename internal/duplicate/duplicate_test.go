package duplicate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func library(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	schema, err := os.ReadFile("testdata/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, d, string(schema))
	execSQL(t, d, "INSERT INTO Information(uuid) VALUES('local'); PRAGMA foreign_keys=ON")
	d.SetMaxOpenConns(1)
	return d
}

func execSQL(t *testing.T, d *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := d.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func seed(t *testing.T, d *sql.DB) {
	t.Helper()
	execSQL(t, d, `INSERT INTO Track(title,artist,filename,path,length,originDatabaseUuid,originTrackId,timeLastPlayed,isPlayed) VALUES
	(' Song ','Artist','one.mp3','one.mp3',180,'first',101,100,1),
	('song',' ARTIST ','two.mp3','two.mp3',180,'second',202,200,1),
	('Other','Someone',' TWO.MP3 ','three.mp3',180,'third',303,300,0),
	(NULL,NULL,NULL,'four.mp3',NULL,'local',4,NULL,NULL),
	('Solo','Artist','solo.mp3','solo.mp3',180,'local',5,NULL,NULL)`)
}

func TestList(t *testing.T) {
	d := library(t)
	seed(t, d)
	for _, tt := range []struct {
		mode string
		want [][]int64
	}{
		{"all", [][]int64{{1, 2, 3}}}, {"metadata", [][]int64{{1, 2}}}, {"filename-len", [][]int64{{2, 3}}},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			groups, err := List(context.Background(), d, tt.mode)
			if err != nil {
				t.Fatal(err)
			}
			var got [][]int64
			for _, g := range groups {
				var ids []int64
				for _, tr := range g.Tracks {
					ids = append(ids, tr.ID)
				}
				got = append(got, ids)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("groups=%v want=%v", got, tt.want)
			}
		})
	}
	if _, err := List(context.Background(), d, "bad"); err == nil {
		t.Fatal("accepted bad mode")
	}
}

func TestInspectOriginIdentity(t *testing.T) {
	d := library(t)
	seed(t, d)
	execSQL(t, d, `INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Favorites',0,0);
	INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,101,'first',0),(1,1,'unrelated',0);
	UPDATE PerformanceData SET quickCues=x'01',loops=x'02' WHERE trackId=1;`)
	got, err := Inspect(context.Background(), d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Playlists) != 1 || got.Playlists[0] != "Favorites" || !got.HasCues || !got.HasLoops || got.TimeLastPlayed != 100 {
		t.Fatalf("detail=%+v", got)
	}
	if _, err := Inspect(context.Background(), d, 999); err == nil {
		t.Fatal("missing track accepted")
	}
}

func TestListIncludesPlaylistMemberships(t *testing.T) {
	d := library(t)
	seed(t, d)
	execSQL(t, d, `INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Favorites',0,0);
	INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,101,'first',0),(1,2,'unrelated',0);`)
	groups, err := List(context.Background(), d, "all")
	if err != nil {
		t.Fatal(err)
	}
	if got := groups[0].Tracks[0].Playlists; !reflect.DeepEqual(got, []string{"Favorites"}) {
		t.Fatalf("playlists=%v", got)
	}
	if got := groups[0].Tracks[1].Playlists; got == nil || len(got) != 0 {
		t.Fatalf("unrelated memberships=%v", got)
	}
	execSQL(t, d, "DROP TABLE PlaylistEntity")
	if _, err := List(context.Background(), d, "all"); err == nil {
		t.Fatal("accepted unreadable memberships")
	}
}
