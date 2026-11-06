package transfer

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func savedRows(t *testing.T, database *sql.DB) map[string]row {
	t.Helper()
	values, _, err := rows(context.Background(), database, "Smartlist")
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]row{}
	for _, value := range values {
		result[str(value["listUuid"])] = without(value, "lastEditTime")
	}
	return result
}

func requireSavedGraph(t *testing.T, database *sql.DB) {
	t.Helper()
	issues, err := db.InspectSmartlists(context.Background(), database)
	if err != nil || len(issues) != 0 {
		t.Fatalf("invalid destination graph: %+v, %v", issues, err)
	}
}

func TestSmartlistMappedFinalPaths(t *testing.T) {
	for _, mirror := range []bool{false, true} {
		for _, policy := range []string{"error", "source", "destination"} {
			t.Run(policy+map[bool]string{false: "/merge", true: "/mirror"}[mirror], func(t *testing.T) {
				source, s := library(t, "source")
				destination, d := library(t, "destination")
				execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Source',0,0); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('saved','Saved','Source;',NULL,NULL,x'0001ff')`)
				execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'Local',0,0)`)
				before := savedRows(t, s)
				o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, PlaylistMap: []PlaylistMapping{{SourceID: 12, DestinationID: 48}}, Conflict: policy, Mirror: mirror, NoBackup: true, DryRun: true}
				preview, err := Run(t.Context(), o)
				if err != nil {
					t.Fatal(err)
				}
				o.DryRun, o.PlanToken = false, preview.PlanToken
				applied, err := Run(t.Context(), o)
				if policy == "error" {
					if err == nil || preview.Applicable || len(savedRows(t, d)) != 0 {
						t.Fatalf("conflicted graph applied: %+v, %v", applied, err)
					}
					return
				}
				if err != nil || !reflect.DeepEqual(preview.Changes, applied.Changes) {
					t.Fatalf("review/apply mismatch: %+v, %v", applied, err)
				}
				expected := "Source;"
				if policy == "destination" {
					expected = "Local;"
				}
				actual := savedRows(t, d)["saved"]
				if actual["parentPlaylistPath"] != expected || actual["nextPlaylistPath"] != nil || actual["nextListUuid"] != nil || !reflect.DeepEqual(actual["rules"], []byte{0, 1, 255}) {
					t.Fatalf("mapped full row: %#v", actual)
				}
				requireSavedGraph(t, d)
				if !reflect.DeepEqual(before, savedRows(t, s)) {
					t.Fatal("sync modified source saved rows")
				}
				o.PlanToken = ""
				again, err := Run(t.Context(), o)
				if err != nil || again.OperationID != "" {
					t.Fatalf("unchanged mapped sync created history: %+v, %v", again, err)
				}
			})
		}
	}
}

func TestSmartlistDestinationOnlyFollowsMappedAncestor(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Renamed',0,0)`)
	execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'Old',0,0),(49,'Child',48,0); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('kept','Kept','Old;Child;',NULL,NULL,' { "opaque": true } ')`)
	o := Options{Source: source, Destination: destination, Scope: Scope{PlaylistIDs: []int64{12}}, PlaylistMap: []PlaylistMapping{{SourceID: 12, DestinationID: 48}}, Conflict: "source", NoBackup: true}
	if _, err := Run(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	actual := savedRows(t, d)["kept"]
	if actual["parentPlaylistPath"] != "Renamed;Child;" || actual["nextPlaylistPath"] != nil || actual["nextListUuid"] != nil || actual["rules"] != " { \"opaque\": true } " {
		t.Fatalf("retained descendant row: %#v", actual)
	}
	requireSavedGraph(t, d)
}

func TestSmartlistMergedAnchorCollisionRefuses(t *testing.T) {
	for _, policy := range []string{"error", "source", "destination"} {
		t.Run(policy, func(t *testing.T) {
			source, s := library(t, "source")
			destination, d := library(t, "destination")
			execSQL(t, s, `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('source-saved','Source','','','','{}')`)
			execSQL(t, d, `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('local-saved','Local','','','','local')`)
			before := savedRows(t, d)
			r, err := Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, Conflict: policy, NoBackup: true})
			if err == nil || r == nil || r.Applicable || len(r.Conflicts) == 0 || !reflect.DeepEqual(before, savedRows(t, d)) {
				t.Fatalf("ambiguous merged chain was accepted: %+v, %v", r, err)
			}
		})
	}
}
