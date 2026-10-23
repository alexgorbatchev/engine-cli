package db

import (
	"context"
	"database/sql"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlaylistSavedOrder(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if _, err := CreateLibrary(ctx, root); err != nil {
		t.Fatal(err)
	}
	d, err := Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, name := range []string{"First", "Second", "Third"} {
		if _, err := CreatePlaylist(ctx, d, name, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(`UPDATE Playlist SET nextListId=NULL; UPDATE Playlist SET nextListId=CASE id WHEN 3 THEN 1 WHEN 1 THEN 2 ELSE 0 END`); err != nil {
		t.Fatal(err)
	}
	got, err := GetAllPlaylists(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, len(got))
	for i, p := range got {
		ids[i] = p.ID
	}
	if !reflect.DeepEqual(ids, []int64{3, 1, 2}) {
		t.Fatalf("saved order=%v", ids)
	}
}

func TestMovePlaylistNativeOrder(t *testing.T) {
	for _, tc := range []struct {
		name               string
		id, parent, before int64
		root, children     []int64
		code               string
	}{
		{"before first", 3, 0, 1, []int64{3, 1, 2}, []int64{4, 5}, ""},
		{"after last", 1, 0, 0, []int64{2, 3, 1}, []int64{4, 5}, ""},
		{"before middle", 3, 0, 2, []int64{1, 3, 2}, []int64{4, 5}, ""},
		{"same position", 1, 0, 2, []int64{1, 2, 3}, []int64{4, 5}, ""},
		{"into folder", 2, 1, 5, []int64{1, 3}, []int64{4, 2, 5}, ""},
		{"out of folder", 4, 0, 2, []int64{1, 4, 2, 3}, []int64{5}, ""},
		{"append child", 2, 1, 0, []int64{1, 3}, []int64{4, 5, 2}, ""},
		{"self", 1, 1, 0, nil, nil, "playlist_move_invalid"},
		{"descendant", 1, 4, 0, nil, nil, "playlist_move_invalid"},
		{"wrong sibling", 2, 0, 4, nil, nil, "playlist_move_invalid"},
		{"missing sibling", 2, 0, 999, nil, nil, "playlist_move_invalid"},
		{"missing source", 999, 0, 0, nil, nil, "playlist_move_invalid"},
		{"missing parent", 2, 999, 0, nil, nil, "playlist_move_invalid"},
		{"self sibling", 2, 0, 2, nil, nil, "playlist_move_invalid"},
		{"negative", 2, -1, 0, nil, nil, "playlist_move_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			if _, err := CreateLibrary(ctx, root); err != nil {
				t.Fatal(err)
			}
			d, err := Open(filepath.Join(root, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			for _, p := range []struct {
				name   string
				parent int64
			}{{"Folder", 0}, {"Second", 0}, {"Third", 0}, {"Child", 1}, {"Other", 1}} {
				if _, err := CreatePlaylist(ctx, d, p.name, p.parent, false); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := d.Exec("INSERT INTO Track(title,path) VALUES('Keep','keep.mp3'); INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(2,1,'fixture',0)"); err != nil {
				t.Fatal(err)
			}
			before, err := playlistRows(ctx, d)
			if err != nil {
				t.Fatal(err)
			}
			err = MovePlaylist(ctx, d, tc.id, tc.parent, tc.before)
			if progress.ErrorCode(err) != tc.code {
				t.Fatalf("error=%v code=%s want %s", err, progress.ErrorCode(err), tc.code)
			}
			after, err := playlistRows(ctx, d)
			if err != nil {
				t.Fatal(err)
			}
			if tc.code != "" {
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("rejected move changed rows: %v -> %v", before, after)
				}
			} else {
				for parent, want := range map[int64][]int64{0: tc.root, 1: tc.children} {
					got, err := siblingOrder(after, parent)
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("parent=%d order=%v want=%v err=%v", parent, got, want, err)
					}
					for i, id := range got {
						var next sql.NullInt64
						if err := d.QueryRow("SELECT nextListId FROM Playlist WHERE id=?", id).Scan(&next); err != nil {
							t.Fatal(err)
						}
						var wantNext int64
						if i+1 < len(got) {
							wantNext = got[i+1]
						}
						if !next.Valid || next.Int64 != wantNext {
							t.Fatalf("id=%d next=%v want=%d", id, next, wantNext)
						}
					}
				}
			}
			var count int
			if err := d.QueryRow("SELECT COUNT(*) FROM PlaylistEntity WHERE listId=2 AND trackId=1").Scan(&count); err != nil || count != 1 {
				t.Fatalf("membership changed count=%d err=%v", count, err)
			}
		})
	}
}

func TestMovePlaylistRollsBackFailedRelink(t *testing.T) {
	d, _ := createTestDB(t)
	if _, err := d.Exec(`INSERT INTO Playlist(title,parentListId,nextListId) VALUES('One',0,2),('Two',0,0);
CREATE TRIGGER fail_relink BEFORE UPDATE ON Playlist WHEN NEW.nextListId=1 BEGIN SELECT RAISE(ABORT,'forced relink failure'); END;`); err != nil {
		t.Fatal(err)
	}
	before, err := playlistRows(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	if err := MovePlaylist(t.Context(), d, 2, 0, 1); err == nil {
		t.Fatal("expected failed relink")
	}
	after, err := playlistRows(t.Context(), d)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("non-atomic move before=%v after=%v err=%v", before, after, err)
	}
}
