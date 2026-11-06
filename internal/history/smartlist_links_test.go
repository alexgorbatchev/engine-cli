package history

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistCompleteChainPlanning(t *testing.T) {
	for _, tc := range []struct {
		name, setup, operation, mutation string
		want                             map[string]db.SmartlistRecord
		deleted                          bool
	}{
		{
			name: "both ordinary target and parent rename",
			setup: `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
			('a','A','','','b','rules-a'),('b','B','','Sets;','','rules-b')`,
			operation: "playlist.rename",
			mutation:  `UPDATE Playlist SET title=CASE id WHEN 1 THEN 'Renamed' ELSE 'New Sets' END`,
			want: map[string]db.SmartlistRecord{
				"saved": {UUID: "saved", Name: "Saved", ParentPath: "Renamed;"},
				"a":     {UUID: "a", Name: "A", NextUUID: "b"},
				"b":     {UUID: "b", Name: "B", NextPath: "New Sets;"},
			},
		},
		{
			name:      "nested parent move",
			operation: "playlist.move",
			mutation:  `UPDATE Playlist SET parentListId=2,nextListId=0 WHERE id=1; UPDATE Playlist SET nextListId=0 WHERE id=2`,
			want: map[string]db.SmartlistRecord{
				"saved": {UUID: "saved", Name: "Saved", ParentPath: "Sets;House;"},
			},
		},
		{
			name:      "proven deletion stays assigned to original undo",
			setup:     `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('healthy','Healthy','Sets;','','','keep')`,
			operation: "playlist.rm",
			mutation:  `DELETE FROM Playlist WHERE id=1`,
			want: map[string]db.SmartlistRecord{
				"healthy": {UUID: "healthy", Name: "Healthy", ParentPath: "Sets;"},
			},
			deleted: true,
		},
		{
			name: "missing terminal removes only its damaged component",
			setup: `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
			('a','A','','','b','rules-a'),('b','B','','Unknown;','','rules-b')`,
			want: map[string]db.SmartlistRecord{
				"saved": {UUID: "saved", Name: "Saved", ParentPath: "House;"},
			},
		},
		{
			name: "cycle with predecessor removes no independent healthy chain",
			setup: `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
			('a','A','','','b','rules-a'),('b','B','','','a','rules-b'),('c','C','Sets;','','a','rules-c')`,
			want: map[string]db.SmartlistRecord{
				"saved": {UUID: "saved", Name: "Saved", ParentPath: "House;"},
			},
		},
		{
			name:      "ordinary target move preserves proven old sibling position",
			setup:     `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('a','A','','House;','','rules-a')`,
			operation: "playlist.move",
			mutation:  `UPDATE Playlist SET parentListId=2,nextListId=0 WHERE id=1; UPDATE Playlist SET nextListId=0 WHERE id=2`,
			want: map[string]db.SmartlistRecord{
				"saved": {UUID: "saved", Name: "Saved", ParentPath: "Sets;House;"},
				"a":     {UUID: "a", Name: "A", NextPath: "Sets;"},
			},
		},
		{
			name: "ordinary deletion joins proven successor saved chain",
			setup: `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
			('a','A','','House;','','rules-a'),('tail','Tail','','Sets;','','rules-tail'),('head','Head','','','tail','rules-head')`,
			operation: "playlist.rm",
			mutation:  `DELETE FROM Playlist WHERE id=1`,
			want: map[string]db.SmartlistRecord{
				"a":    {UUID: "a", Name: "A", NextUUID: "head"},
				"head": {UUID: "head", Name: "Head", NextUUID: "tail"},
				"tail": {UUID: "tail", Name: "Tail", NextPath: "Sets;"},
			},
			deleted: true,
		},
		{
			name: "ordinary tail deletion joins native terminal saved chain",
			setup: `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
			('a','A','','Sets;','','rules-a'),('tail','Tail','','','','rules-tail'),('head','Head','','','tail','rules-head')`,
			operation: "playlist.rm",
			mutation:  `DELETE FROM Playlist WHERE id=2`,
			want: map[string]db.SmartlistRecord{
				"saved": {UUID: "saved", Name: "Saved", ParentPath: "House;"},
				"a":     {UUID: "a", Name: "A", NextUUID: "head"},
				"head":  {UUID: "head", Name: "Head", NextUUID: "tail"},
				"tail":  {UUID: "tail", Name: "Tail"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			if tc.setup != "" {
				if _, err := database.Exec(tc.setup); err != nil {
					t.Fatal(err)
				}
			}
			var entry *Entry
			if tc.mutation != "" {
				entry = recordSmartlistMutation(t, lib, database, tc.operation, tc.mutation)
			}
			evidence, err := readSmartlistEvidence(ctx, lib)
			if err != nil {
				t.Fatal(err)
			}
			orders, err := readSmartlistOrderEvidence(ctx, lib, evidence)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := planSmartlistLinks(ctx, evidence, orders)
			if err != nil || plan == nil || len(plan.patches) == 0 {
				t.Fatalf("plan=%+v error=%v", plan, err)
			}
			if tc.deleted {
				if len(plan.deletions) != 1 || len(plan.deletions[entry.ID]) != 1 || plan.deletions[entry.ID][0].UUID != "saved" {
					t.Fatalf("lost original deletion Undo: %+v", plan.deletions)
				}
			} else if len(plan.deletions) != 0 {
				t.Fatalf("unproven removal fabricated Undo: %+v", plan.deletions)
			}
			source := filepath.Join(lib, "Database2", "m.db")
			destination := filepath.Join(t.TempDir(), "complete.db")
			if _, err := stageSmartlistImage(ctx, source, destination, evidence.hash, plan.patches); err != nil {
				t.Fatal(err)
			}
			staged, _, err := readSmartlistVersion(ctx, destination, "")
			if err != nil {
				t.Fatal(err)
			}
			actual := make(map[string]db.SmartlistRecord)
			for _, row := range staged.Saved {
				actual[row.UUID] = row
			}
			if !reflect.DeepEqual(actual, tc.want) || len(staged.Issues) != 0 {
				t.Fatalf("saved rows=%+v issues=%+v expected=%+v", actual, staged.Issues, tc.want)
			}
			if current, err := fingerprint(ctx, source); err != nil || current != evidence.hash {
				t.Fatalf("planning/staging changed live source: %q %v", current, err)
			}
		})
	}
}

func TestSmartlistCompleteChainHealthyNoEnrollment(t *testing.T) {
	lib, _ := smartlistHistoryLibrary(t)
	evidence, err := readSmartlistEvidence(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planSmartlistLinks(context.Background(), evidence, nil)
	if err != nil || len(plan.patches) != 0 || len(plan.deletions) != 0 {
		t.Fatalf("healthy plan=%+v error=%v", plan, err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "history")); !os.IsNotExist(err) {
		t.Fatalf("healthy plan created history: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if plan, err := planSmartlistLinks(ctx, evidence, nil); err == nil || plan != nil {
		t.Fatalf("cancelled plan=%+v error=%v", plan, err)
	}
}
