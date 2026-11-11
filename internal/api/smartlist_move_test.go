package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func smartlistMoveFixture(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	_, err = d.Exec(`UPDATE Playlist SET nextListId=2 WHERE id=1;
 INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(2,'Last',0,0),(3,'Child',1,0);
 INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime) VALUES
 ('p','Preceding','','Favorites;','','opaque p','2026-01-01'),
 ('a','A','','','b','malformed','2026-01-02'),
 ('b','B','','Last;','','{"rv":99,"rules":[{"v":"99.0.0"}]}','2026-01-03'),
 ('c','Trailing','','','','opaque c','2026-01-04'),
 ('n','Nested','Favorites;','Favorites;Child;','','opaque n','2026-01-05')`)
	if err != nil {
		t.Fatal(err)
	}
	return s, d
}

func TestSmartlistMoveOrderAndExactUndo(t *testing.T) {
	for _, tc := range []struct {
		uuid, to, before string
		root, nested     []string
	}{
		{"b", "", "smart:p", []string{"B", "Preceding", "Favorites", "A", "Last", "Trailing"}, []string{"Nested", "Child"}},
		{"b", "", "1", []string{"Preceding", "B", "Favorites", "A", "Last", "Trailing"}, []string{"Nested", "Child"}},
		{"b", "", "smart:a", []string{"Preceding", "Favorites", "B", "A", "Last", "Trailing"}, []string{"Nested", "Child"}},
		{"a", "", "smart:c", []string{"Preceding", "Favorites", "B", "Last", "A", "Trailing"}, []string{"Nested", "Child"}},
		{"a", "", "", []string{"Preceding", "Favorites", "B", "Last", "Trailing", "A"}, []string{"Nested", "Child"}},
		{"a", "1", "smart:n", []string{"Preceding", "Favorites", "B", "Last", "Trailing"}, []string{"A", "Nested", "Child"}},
		{"a", "1", "3", []string{"Preceding", "Favorites", "B", "Last", "Trailing"}, []string{"Nested", "A", "Child"}},
		{"a", "1", "", []string{"Preceding", "Favorites", "B", "Last", "Trailing"}, []string{"Nested", "Child", "A"}},
		{"n", "", "smart:b", []string{"Preceding", "Favorites", "A", "Nested", "B", "Last", "Trailing"}, []string{"Child"}},
	} {
		t.Run(fmt.Sprintf("%s/%s/%s", tc.uuid, tc.to, tc.before), func(t *testing.T) {
			s, d := smartlistMoveFixture(t)
			original := mixedNativeState(t, d)
			var opaque string
			if err := d.QueryRow(`SELECT group_concat(rules||lastEditTime,'|') FROM (SELECT * FROM Smartlist ORDER BY listUuid)`).Scan(&opaque); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"Playlist", "PlaylistEntity", "Track"} {
				for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
					if _, err := d.Exec(fmt.Sprintf(`CREATE TRIGGER protect_%s_%s BEFORE %s ON %s BEGIN SELECT RAISE(ABORT,'unrelated write'); END`, table, op, op, table)); err != nil {
						t.Fatal(err)
					}
				}
			}
			body, err := json.Marshal(map[string]string{"uuid": tc.uuid, "to": tc.to, "before": tc.before})
			if err != nil {
				t.Fatal(err)
			}
			job := runAPIJob(t, s, "smartlist.move", string(body))
			tree, err := db.ReadPlaylistTree(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			var root, nested []string
			for _, node := range tree.Nodes {
				root = append(root, node.Title)
				if node.ID == 1 {
					for _, child := range node.Children {
						nested = append(nested, child.Title)
					}
				}
			}
			if len(tree.Issues) != 0 || !reflect.DeepEqual(root, tc.root) || !reflect.DeepEqual(nested, tc.nested) {
				t.Fatalf("root=%v nested=%v issues=%v", root, nested, tree.Issues)
			}
			var after string
			if err := d.QueryRow(`SELECT group_concat(rules||lastEditTime,'|') FROM (SELECT * FROM Smartlist ORDER BY listUuid)`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != opaque {
				t.Fatal("rules or edit times changed")
			}
			runAPIJob(t, s, "library.undo", `{"id":"`+job["history_id"].(string)+`"}`)
			if got := mixedNativeState(t, d); got != original {
				t.Fatalf("Undo changed native rows\n%s\n%s", original, got)
			}
		})
	}
}

func TestSmartlistMoveRefusalsAndRollback(t *testing.T) {
	for _, tc := range []struct{ uuid, to, before, setup, code string }{
		{"missing", "", "", "", "smartlist_not_found"},
		{"a", "smart:b", "", "", "smartlist_move_parent"},
		{"a", "999", "", "", "smartlist_move_parent"},
		{"a", "", "smart:a", "", "smartlist_move_self"},
		{"a", "", "smart:missing", "", "smartlist_move_anchor"},
		{"a", "", "smart:n", "", "smartlist_move_anchor"},
		{"a", "", "3", "", "smartlist_move_anchor"},
		{"a", "", "999", "", "smartlist_move_anchor"},
		{"a", "", "smart:b", "", "smartlist_move_unchanged"},
		{"a", "1", "", `UPDATE Smartlist SET title='A' WHERE listUuid='n'`, "smartlist_name_conflict"},
		{"a", "1", "", `CREATE TRIGGER corrupt_move AFTER UPDATE OF parentPlaylistPath ON Smartlist WHEN NEW.parentPlaylistPath='Favorites;' BEGIN UPDATE Smartlist SET nextPlaylistPath='missing;' WHERE listUuid=NEW.listUuid; END`, "playlist_order_invalid"},
	} {
		t.Run(tc.code+tc.before+tc.to, func(t *testing.T) {
			s, d := smartlistMoveFixture(t)
			if tc.setup != "" {
				if _, err := d.Exec(tc.setup); err != nil {
					t.Fatal(err)
				}
			}
			before := mixedNativeState(t, d)
			body, err := json.Marshal(map[string]string{"uuid": tc.uuid, "to": tc.to, "before": tc.before})
			if err != nil {
				t.Fatal(err)
			}
			job := awaitAPIJob(t, s, submitAPIJob(t, s, "smartlist.move", string(body)))
			failure, ok := job["error"].(map[string]any)
			if !ok || failure["code"] != tc.code {
				t.Fatalf("job=%v", job)
			}
			if got := mixedNativeState(t, d); got != before {
				t.Fatalf("failed move changed rows\n%s\n%s", before, got)
			}
		})
	}
}

func TestSmartlistMoveFreshDesktopGuard(t *testing.T) {
	s, d := smartlistMoveFixture(t)
	before := mixedNativeState(t, d)
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	_, id, err := s.RunOperation(t.Context(), "smartlist.move", json.RawMessage(`{"uuid":"a","to":"1"}`))
	if progress.ErrorCode(err) != "engine_desktop_running" || id != "" {
		t.Fatalf("guard id=%s err=%v", id, err)
	}
	if after := mixedNativeState(t, d); after != before {
		t.Fatal("guard refusal changed native rows")
	}
}
