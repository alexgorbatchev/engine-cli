package db

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

// treeShape renders titles in tree order: smart playlists are prefixed with ~ and
// read-only ones suffixed with !, folders list their children in braces.
func treeShape(nodes []TreeNode) string {
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		label := node.Title
		if node.Kind == TreeNodeSmartlist {
			label = "~" + label
			if node.ReadOnly {
				label += "!"
			}
		}
		if len(node.Children) > 0 {
			label += "{" + treeShape(node.Children) + "}"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, " ")
}

func treeIssues(issues []TreeIssue) map[string]string {
	result := map[string]string{}
	for _, issue := range issues {
		result[issue.UUID] = issue.Code
	}
	return result
}

const editableRules = `{"match":"all","rules":[{"col":"title","con":"LIKE","param":"'%house%'","v":"2.20.0"}],"rv":2}`

// sqlEditableRules is an editable document without quotes, for inline SQL fixtures.
const sqlEditableRules = `{"match":"all","rules":[{"col":"year","con":">","param":"2000","v":"2.20.0"}],"rv":2}`

func TestReadPlaylistTreePlacesSmartlistsInSavedOrder(t *testing.T) {
	for _, tc := range []struct {
		name, mutation, want string
		issues               map[string]string
	}{
		{"nested chain before an ordinary sibling", "", "House{~Saved A! ~Saved B! Deep} Household", map[string]string{}},
		{"top-level before an ordinary sibling",
			`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES ('x','Top','','Household;','','` + sqlEditableRules + `')`,
			"House{~Saved A! ~Saved B! Deep} ~Top Household", map[string]string{}},
		{"top-level at the end of the folder",
			`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES ('y','Last','','','','` + sqlEditableRules + `')`,
			"House{~Saved A! ~Saved B! Deep} Household ~Last", map[string]string{}},
		{"folder whose only children are smart playlists",
			`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES ('z','Only','Household;','','','` + sqlEditableRules + `')`,
			"House{~Saved A! ~Saved B! Deep} Household{~Only}", map[string]string{}},
		{"stale next path breaks the chain", `UPDATE Smartlist SET nextPlaylistPath='House;Gone;' WHERE listUuid='b'`,
			"House{Deep} Household", map[string]string{"a": "sibling_link_invalid", "b": "next_path_unresolved"}},
		{"broken saved link keeps the healthy tail", `UPDATE Smartlist SET nextListUuid='gone' WHERE listUuid='a'`,
			"House{~Saved B! Deep} Household", map[string]string{"a": "sibling_link_invalid"}},
		{"cycle", `UPDATE Smartlist SET nextPlaylistPath='',nextListUuid='a' WHERE listUuid='b'`,
			"House{Deep} Household", map[string]string{"a": "sibling_cycle", "b": "sibling_cycle"}},
		{"renamed parent", `UPDATE Playlist SET title='Renamed' WHERE id=1`,
			"Renamed{Deep} Household", map[string]string{"a": "parent_path_unresolved", "b": "parent_path_unresolved"}},
		{"deleted parent", `DELETE FROM Playlist WHERE id=1`,
			"Household", map[string]string{"a": "parent_path_unresolved", "b": "parent_path_unresolved"}},
		{"library without smart playlists", `DROP TABLE Smartlist`, "House{Deep} Household", map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := smartlistInspectionLibrary(t)
			if tc.mutation != "" {
				if _, err := d.Exec(tc.mutation); err != nil {
					t.Fatal(err)
				}
			}
			before := inspectionRowsIfPresent(t, d)
			tree, err := ReadPlaylistTree(context.Background(), d)
			if err != nil {
				t.Fatal(err)
			}
			if got := treeShape(tree.Nodes); got != tc.want {
				t.Fatalf("tree=%q want %q", got, tc.want)
			}
			if got := treeIssues(tree.Issues); !reflect.DeepEqual(got, tc.issues) {
				t.Fatalf("issues=%v want %v", got, tc.issues)
			}
			if after := inspectionRowsIfPresent(t, d); !reflect.DeepEqual(after, before) {
				t.Fatalf("read changed saved rows: %v -> %v", before, after)
			}
		})
	}
}

func inspectionRowsIfPresent(t *testing.T, d *sql.DB) [][6]string {
	t.Helper()
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name='Smartlist'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		return nil
	}
	return inspectionRows(t, d)
}

func TestReadPlaylistTreeNodeDetails(t *testing.T) {
	d := smartlistInspectionLibrary(t)
	if _, err := d.Exec(`UPDATE Smartlist SET rules=? WHERE listUuid='a'`, editableRules); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId,membershipReference) VALUES (2,1,'db',0,0),(2,2,'db',0,0),(3,1,'db',0,0)`); err != nil {
		t.Fatal(err)
	}
	tree, err := ReadPlaylistTree(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	want := []TreeNode{
		{Kind: TreeNodePlaylist, ID: 1, Title: "House", Children: []TreeNode{
			{Kind: TreeNodeSmartlist, UUID: "a", Title: "Saved A", ParentID: 1},
			{Kind: TreeNodeSmartlist, UUID: "b", Title: "Saved B", ParentID: 1, ReadOnly: true},
			{Kind: TreeNodePlaylist, ID: 2, Title: "Deep", ParentID: 1, TrackCount: 2, Children: []TreeNode{}},
		}},
		{Kind: TreeNodePlaylist, ID: 3, Title: "Household", TrackCount: 1, Children: []TreeNode{}},
	}
	if !reflect.DeepEqual(tree.Nodes, want) {
		t.Fatalf("nodes=%+v\nwant  %+v", tree.Nodes, want)
	}
}

func TestReadPlaylistTreeEmptyLibrary(t *testing.T) {
	d := smartlistInspectionLibrary(t)
	if _, err := d.Exec(`DELETE FROM Smartlist; DELETE FROM Playlist`); err != nil {
		t.Fatal(err)
	}
	tree, err := ReadPlaylistTree(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Nodes == nil || len(tree.Nodes) != 0 || tree.Issues == nil || len(tree.Issues) != 0 {
		t.Fatalf("tree=%+v, want empty non-nil nodes and issues", tree)
	}
}

func TestReadPlaylistTreeFailures(t *testing.T) {
	if _, err := ReadPlaylistTree(context.Background(), nil); err == nil {
		t.Fatal("nil database accepted")
	}
	for name, mutation := range map[string]string{
		"duplicate identity":       `CREATE TABLE copy AS SELECT * FROM Smartlist; DROP TABLE Smartlist; ALTER TABLE copy RENAME TO Smartlist; INSERT INTO Smartlist SELECT * FROM Smartlist WHERE listUuid='a'`,
		"Smartlist is not a table": `DROP TABLE Smartlist; CREATE VIEW Smartlist AS SELECT 1`,
		"cyclic playlist order":    `UPDATE Playlist SET nextListId=1 WHERE id=3; UPDATE Playlist SET nextListId=3 WHERE id=1`,
		"unreadable Smartlist":     `DROP TABLE Smartlist; CREATE TABLE Smartlist(listUuid TEXT)`,
	} {
		t.Run(name, func(t *testing.T) {
			d := smartlistInspectionLibrary(t)
			if _, err := d.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadPlaylistTree(context.Background(), d); err == nil {
				t.Fatal("damaged library read without an error")
			}
		})
	}
}

func TestSmartlistRulesEditable(t *testing.T) {
	for rules, want := range map[string]bool{
		editableRules:                         true,
		`{"match":"all","rules":[],"rv":1}`:   true,
		`{"rules":[{"v":"2.21.0"}],"rv":2}`:   true,
		`{"rules":[{"v":"2.22.0"}],"rv":2}`:   false,
		`{"rules":[{"v":"3.0.0"}],"rv":2}`:    false,
		`{"rules":[{"v":"latest"}],"rv":2}`:   false,
		`{"rules":[{"v":"2.20.0"}],"rv":3}`:   false,
		`{"rules":[{"v":"2.20.0"}]}`:          false,
		`{"rules":[{"v":"2.20.0"}],"rv":"2"}`: false,
		`not json`:                            false,
		``:                                    false,
	} {
		if got := smartlistRulesEditable(rules); got != want {
			t.Errorf("smartlistRulesEditable(%q)=%t want %t", rules, got, want)
		}
	}
}
