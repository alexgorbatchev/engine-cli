package db

import (
	"context"
	"reflect"
	"slices"
	"testing"
)

func TestSmartlistCausesSeparateFailuresBehindSamePublicIssue(t *testing.T) {
	database := smartlistInspectionLibrary(t)
	if _, err := database.Exec(`UPDATE Smartlist SET nextPlaylistPath='House;Deep;' WHERE listUuid='a'`); err != nil {
		t.Fatal(err)
	}
	before, err := ReadSmartlistInventory(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DELETE FROM Smartlist WHERE listUuid='b'`); err != nil {
		t.Fatal(err)
	}
	after, err := ReadSmartlistInventory(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Issues, after.Issues) {
		t.Fatalf("fixture did not retain the same public issue: %+v -> %+v", before.Issues, after.Issues)
	}
	if len(before.Causes) != 1 || before.Causes[0].Reason != "dual_successors" || len(after.Causes) != 2 {
		t.Fatalf("distinct relationship failures collapsed: %+v -> %+v", before.Causes, after.Causes)
	}
	for _, cause := range after.Causes {
		if cause.UUID != "a" || cause.NextUUID != "b" || cause.NextPath != "House;Deep;" {
			t.Fatalf("failed edge lost native operands: %+v", cause)
		}
	}
}

func TestSmartlistCauseCollisionPreservesAllPredecessorIdentities(t *testing.T) {
	database := smartlistInspectionLibrary(t)
	if _, err := database.Exec(`CREATE TABLE copy AS SELECT * FROM Smartlist; DROP TABLE Smartlist; ALTER TABLE copy RENAME TO Smartlist;
		INSERT INTO Smartlist SELECT 'c','Saved C',parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime FROM Smartlist WHERE listUuid='a';
		INSERT INTO Smartlist SELECT 'd','Saved D',parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime FROM Smartlist WHERE listUuid='a'`); err != nil {
		t.Fatal(err)
	}
	inventory, err := ReadSmartlistInventory(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Causes) != 1 || inventory.Causes[0].Reason != "predecessor_collision" || !slices.Equal(inventory.Causes[0].Members, []string{"a", "c", "d"}) {
		t.Fatalf("collision witness lost identities/multiplicity: %+v", inventory.Causes)
	}
	if inventory.Causes[0].ParentPath != "House;" || inventory.Causes[0].NextUUID != "b" {
		t.Fatalf("collision witness lost anchor: %+v", inventory.Causes[0])
	}
}

func TestSmartlistCauseCyclesRetainExactDirectedEdges(t *testing.T) {
	database := smartlistInspectionLibrary(t)
	if _, err := database.Exec(`UPDATE Smartlist SET nextPlaylistPath='',nextListUuid='c' WHERE listUuid='b';
		INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules)
		VALUES ('c','Saved C','House;','','a','opaque-c')`); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]string {
		t.Helper()
		inventory, err := ReadSmartlistInventory(context.Background(), database)
		if err != nil {
			t.Fatal(err)
		}
		edges := make(map[string]string)
		for _, cause := range inventory.Causes {
			if cause.Reason == "cycle_edge" {
				edges[cause.UUID] = cause.NextUUID
			}
		}
		return edges
	}
	before := read()
	if !reflect.DeepEqual(before, map[string]string{"a": "b", "b": "c", "c": "a"}) {
		t.Fatalf("wrong cycle witness: %+v", before)
	}
	if _, err := database.Exec(`UPDATE Smartlist SET nextListUuid='temporary-'||listUuid;
		UPDATE Smartlist SET nextListUuid=CASE listUuid WHEN 'a' THEN 'c' WHEN 'b' THEN 'a' ELSE 'b' END`); err != nil {
		t.Fatal(err)
	}
	after := read()
	if !reflect.DeepEqual(after, map[string]string{"a": "c", "b": "a", "c": "b"}) || reflect.DeepEqual(before, after) {
		t.Fatalf("same cycle membership concealed changed directed edges: %+v -> %+v", before, after)
	}
}
