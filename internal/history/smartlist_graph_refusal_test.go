package history

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistRepairRemovesUnprovableNativeGraph(t *testing.T) {
	for _, tc := range []struct {
		name, setup, mutation string
		removed, amended      int
		want                  map[string]db.SmartlistRecord
	}{
		{
			name: "cross parent saved edge",
			setup: `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
			('a','A','','','b','rules-a'),('b','B','Sets;','','','rules-b')`,
			removed: 2,
			want: map[string]db.SmartlistRecord{
				"saved": {UUID: "saved", Name: "Saved", ParentPath: "House;"},
			},
		},
		{
			name: "duplicate ordinary anchors",
			setup: `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
			('a','A','','Sets;',NULL,'rules-a'),('b','B','','Sets;',NULL,'rules-b')`,
			removed: 2,
			want: map[string]db.SmartlistRecord{
				"saved": {UUID: "saved", Name: "Saved", ParentPath: "House;"},
			},
		},
		{
			name: "ambiguous successor heads after ordinary deletion",
			setup: `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
			('preceding','Preceding','','House;','','rules-preceding'),
			('a','A','','Sets;',NULL,'rules-a'),('b','B','','Sets;',NULL,'rules-b'),
			('healthy','Healthy','Sets;','','','keep')`,
			mutation: `DELETE FROM Playlist WHERE id=1`,
			removed:  4,
			amended:  1,
			want: map[string]db.SmartlistRecord{
				"healthy": {UUID: "healthy", Name: "Healthy", ParentPath: "Sets;"},
			},
		},
		{
			name: "duplicate predecessors after ordinary deletion",
			setup: `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
			('a','A','','House;',NULL,'rules-a'),('b','B','','House;',NULL,'rules-b'),
			('healthy','Healthy','Sets;','','','keep')`,
			mutation: `DELETE FROM Playlist WHERE id=1`,
			removed:  3,
			amended:  1,
			want: map[string]db.SmartlistRecord{
				"healthy": {UUID: "healthy", Name: "Healthy", ParentPath: "Sets;"},
			},
		},
		{
			name: "multiple ordinary departures have no invented chronology",
			setup: `INSERT INTO Playlist(title,parentListId,nextListId) VALUES ('Third',0,0);
			INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
			('a','A','','House;',NULL,'rules-a'),('b','B','','Sets;',NULL,'rules-b'),
			('healthy','Healthy','Third;','','','keep')`,
			mutation: `DELETE FROM Playlist WHERE id IN (1,2)`,
			removed:  3,
			amended:  1,
			want: map[string]db.SmartlistRecord{
				"healthy": {UUID: "healthy", Name: "Healthy", ParentPath: "Third;"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			if _, err := database.ExecContext(ctx, tc.setup); err != nil {
				t.Fatal(err)
			}
			var original *Entry
			if tc.mutation != "" {
				original = recordSmartlistMutation(t, lib, database, "playlist.rm", tc.mutation)
			}
			source := filepath.Join(lib, "Database2", "m.db")
			before, err := fingerprint(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			review, err := PreviewSmartlistRepair(ctx, lib)
			if err != nil || review.Removed != tc.removed || review.AmendedDeletes != tc.amended || review.Reconnected != 0 {
				t.Fatalf("unprovable graph review=%+v error=%v", review, err)
			}
			if after, err := fingerprint(ctx, source); err != nil || after != before {
				t.Fatalf("preview changed native source: %s %v", after, err)
			}
			for _, row := range review.Rows {
				if row.UUID != "saved" && row.DeleteEntryID != "" {
					t.Fatalf("unproven chain borrowed original deletion Undo: %+v", row)
				}
			}
			result, err := ApplySmartlistRepair(ctx, lib, review.PlanToken)
			if err != nil || result.Removed != tc.removed || result.AmendedDeletes != tc.amended || result.Reconnected != 0 || result.UndoID != "" {
				t.Fatalf("unprovable graph apply=%+v error=%v", result, err)
			}
			current, _, err := readSmartlistVersion(ctx, source, "")
			if err != nil {
				t.Fatal(err)
			}
			actual := make(map[string]db.SmartlistRecord)
			for _, row := range current.Saved {
				actual[row.UUID] = row
				var rules string
				if err := database.QueryRowContext(ctx, `SELECT rules FROM Smartlist WHERE listUuid=?`, row.UUID).Scan(&rules); err != nil {
					t.Fatal(err)
				}
				wantRules := "keep"
				if row.UUID == "saved" {
					wantRules = "opaque"
				}
				if rules != wantRules {
					t.Fatalf("surviving rules changed: %s=%q", row.UUID, rules)
				}
			}
			if !reflect.DeepEqual(actual, tc.want) || len(current.Issues) != 0 {
				t.Fatalf("saved=%+v issues=%+v want=%+v", actual, current.Issues, tc.want)
			}
			entries, err := List(lib)
			if err != nil {
				t.Fatal(err)
			}
			if original == nil {
				if len(entries) != 0 {
					t.Fatalf("unproven removal fabricated history: %+v", entries)
				}
			} else {
				if len(entries) != 1 || entries[0].ID != original.ID {
					t.Fatalf("removal invented separate Undo entry: %+v", entries)
				}
				for _, side := range []*snapshot{original.Before, original.After} {
					for _, image := range side.Databases {
						if actual, err := fingerprint(ctx, image.Path); err != nil || actual != image.Hash {
							t.Fatalf("immutable original image changed: %s %v", image.Path, err)
						}
					}
				}
			}
			again, err := PreviewSmartlistRepair(ctx, lib)
			if err != nil || again.Removed != 0 || again.Reconnected != 0 || again.AmendedDeletes != 0 || len(again.Rows) != 0 {
				t.Fatalf("repair is not idempotent: %+v %v", again, err)
			}
		})
	}
}
