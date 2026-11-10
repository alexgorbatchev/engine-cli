package api

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestPlaylistTreeOperationReadsSmartlists(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
		('lead','Lead','','Favorites;','','{"rules":[],"rv":2}'),
		('lost','Lost','Gone;','','','{"rules":[],"rv":2}')`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if !s.operations["playlist.tree"].collectionRead {
		t.Fatal("playlist.tree is not a rerunnable collection read")
	}
	job := runAPIJob(t, s, "playlist.tree", `{}`)
	result := job["result"].(map[string]any)
	want := map[string]any{
		"nodes": []any{
			map[string]any{"kind": "smartlist", "uuid": "lead", "title": "Lead", "parentId": float64(0), "readOnly": false},
			map[string]any{"kind": "playlist", "id": float64(1), "title": "Favorites", "parentId": float64(0), "trackCount": float64(0)},
		},
		"issues": []any{map[string]any{"uuid": "lost", "title": "Lost", "code": "parent_path_unresolved"}},
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result=%#v\nwant  %#v", result, want)
	}
	if job["history_id"] != nil {
		t.Fatalf("read recorded history: %+v", job)
	}
}
