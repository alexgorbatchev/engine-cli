package transfer

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestSmartlistNativeNameConstraints(t *testing.T) {
	cases := []struct {
		name                      string
		sourceTitle, localTitle   any
		sourceParent, localParent any
		collision                 bool
	}{
		{"same name", "Filters", "Filters", "", "", true},
		{"source NULL parent", "Filters", "Filters", nil, "", false},
		{"local NULL parent", "Filters", "Filters", "", nil, false},
		{"both NULL parents", "Filters", "Filters", nil, nil, false},
		{"source NULL title", nil, "Filters", "", "", false},
		{"local NULL title", "Filters", nil, "", "", false},
		{"case differs", "Filters", "filters", "", "", false},
		{"Unicode differs", "Café", "Cafe\u0301", "", "", false},
		{"BLOB versus text", []byte("Filters"), "Filters", "", "", false},
		{"equal BLOBs", []byte("Filters"), []byte("Filters"), "", "", true},
		{"numeric TEXT affinity", int64(123), "123", "", "", true},
	}
	for _, tc := range cases {
		for _, mirror := range []bool{false, true} {
			for _, policy := range []string{"error", "source", "destination"} {
				t.Run(tc.name+"/"+policy+map[bool]string{false: "/merge", true: "/mirror"}[mirror], func(t *testing.T) {
					source, s := library(t, "source")
					destination, d := library(t, "destination")
					execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'A',0,13),(13,'B',0,0)`)
					execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'A',0,49),(49,'B',0,0)`)
					insert := `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES(?,?,?,?,'',?)`
					execSQL(t, s, insert, "source-saved", tc.sourceTitle, tc.sourceParent, "A;", []byte{0, 1, 255})
					execSQL(t, d, insert, "local-saved", tc.localTitle, tc.localParent, "B;", " { \"local\": true } ")
					expected := savedRows(t, s)
					if !mirror {
						expected["local-saved"] = savedRows(t, d)["local-saved"]
					}
					beforeSource, beforeDestination := syncFileManifest(t, source), syncFileManifest(t, destination)
					o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, PlaylistMap: []PlaylistMapping{{SourceID: 12, DestinationID: 48}, {SourceID: 13, DestinationID: 49}}, Conflict: policy, Mirror: mirror, NoBackup: true, DryRun: true}
					preview, err := Run(t.Context(), o)
					blocked := tc.collision && !mirror
					if err != nil || preview == nil || preview.Applicable == blocked {
						t.Fatalf("preview collision=%v: %+v, %v", blocked, preview, err)
					}
					if !reflect.DeepEqual(beforeSource, syncFileManifest(t, source)) || !reflect.DeepEqual(beforeDestination, syncFileManifest(t, destination)) {
						t.Fatal("constraint preview changed library files")
					}
					o.DryRun, o.PlanToken = false, preview.PlanToken
					applied, err := Run(t.Context(), o)
					if blocked {
						if !slices.ContainsFunc(preview.Conflicts, func(c Change) bool { return c.Code == "sync_smartlist_constraint_conflict" }) || progress.ErrorCode(err) != "sync_conflicts" || applied == nil || applied.OperationID != "" {
							t.Fatalf("constraint was not refused before apply: %+v, %v", applied, err)
						}
						if !reflect.DeepEqual(beforeDestination, syncFileManifest(t, destination)) {
							t.Fatal("rejected apply changed destination files or enrolled history")
						}
					} else {
						if err != nil || applied == nil || !reflect.DeepEqual(preview.Changes, applied.Changes) {
							t.Fatalf("valid constraint plan failed: %+v, %v", applied, err)
						}
						if actual := savedRows(t, d); !reflect.DeepEqual(actual, expected) {
							t.Fatalf("native values changed: got %#v, want %#v", actual, expected)
						}
						requireSavedGraph(t, d)
					}
					if !reflect.DeepEqual(beforeSource, syncFileManifest(t, source)) {
						t.Fatal("constraint handling modified source files")
					}
				})
			}
		}
	}
}

func TestSmartlistNameCollisionAfterParentMapping(t *testing.T) {
	for _, policy := range []string{"source", "destination"} {
		t.Run(policy, func(t *testing.T) {
			source, s := library(t, "source")
			destination, d := library(t, "destination")
			execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Source',0,0),(13,'A',12,14),(14,'B',12,0); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('source-saved','Filters','Source;','Source;A;','','{}')`)
			execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'Local',0,0),(49,'A',48,50),(50,'B',48,0); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('local-saved','Filters','Local;','Local;B;','','{}')`)
			beforeSource, beforeDestination := syncFileManifest(t, source), syncFileManifest(t, destination)
			o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, PlaylistMap: []PlaylistMapping{{SourceID: 12, DestinationID: 48}, {SourceID: 13, DestinationID: 49}, {SourceID: 14, DestinationID: 50}}, Conflict: policy, NoBackup: true, DryRun: true}
			preview, err := Run(t.Context(), o)
			if err != nil || preview == nil || preview.Applicable || !slices.ContainsFunc(preview.Conflicts, func(c Change) bool { return c.Code == "sync_smartlist_constraint_conflict" }) {
				t.Fatalf("mapped collision escaped preview: %+v, %v", preview, err)
			}
			o.DryRun, o.PlanToken = false, preview.PlanToken
			if _, err := Run(t.Context(), o); progress.ErrorCode(err) != "sync_conflicts" {
				t.Fatalf("mapped collision apply: %v", err)
			}
			if !reflect.DeepEqual(beforeSource, syncFileManifest(t, source)) || !reflect.DeepEqual(beforeDestination, syncFileManifest(t, destination)) {
				t.Fatal("mapped conflict changed library files")
			}
		})
	}
}

func TestSmartlistObservedConstraintPolicy(t *testing.T) {
	for _, policy := range []string{"IGNORE", "REPLACE"} {
		t.Run(policy, func(t *testing.T) {
			source, s := library(t, "source")
			destination, d := library(t, "destination")
			var schema string
			if err := d.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='Smartlist'`).Scan(&schema); err != nil {
				t.Fatal(err)
			}
			execSQL(t, d, `DROP TABLE Smartlist`)
			execSQL(t, d, strings.Replace(schema, "UNIQUE (title, parentPlaylistPath)", "UNIQUE (title, parentPlaylistPath) ON CONFLICT "+policy, 1))
			execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'A',0,13),(13,'B',0,0); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('source-saved','Filters','','A;','','{}')`)
			execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'A',0,49),(49,'B',0,0); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('local-saved','Filters','','B;','','local')`)
			o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, PlaylistMap: []PlaylistMapping{{SourceID: 12, DestinationID: 48}, {SourceID: 13, DestinationID: 49}}, NoBackup: true, DryRun: true}
			preview, err := Run(t.Context(), o)
			if err != nil || preview == nil || preview.Applicable || !slices.ContainsFunc(preview.Conflicts, func(c Change) bool { return c.Code == "sync_smartlist_constraint_conflict" }) {
				t.Fatalf("native conflict policy silently discarded a planned row: %+v, %v", preview, err)
			}
		})
	}
}

func TestSmartlistConstraintCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p := &plan{expectedSmartlists: map[string]*entity{"smartlist:saved": {Key: "smartlist:saved"}}}
	if err := p.checkSmartlistConstraints(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled constraint check: %v", err)
	}
}

func TestSmartlistConstraintSchemaChangeInvalidatesObservation(t *testing.T) {
	path, database := library(t, "destination")
	before, err := readObservedCollection(t.Context(), database, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := observationDigest(before)
	if err := checkDestinationObservation(t.Context(), database, path, expected); err != nil {
		t.Fatalf("unchanged observation: %v", err)
	}
	execSQL(t, database, `DROP TABLE Smartlist`)
	execSQL(t, database, strings.Replace(before.SmartlistSchema, "UNIQUE (title, parentPlaylistPath)", "UNIQUE (title, parentPlaylistPath) ON CONFLICT IGNORE", 1))
	after, err := readObservedCollection(t.Context(), database, path, nil)
	if err != nil || !reflect.DeepEqual(before.Schema, after.Schema) || !reflect.DeepEqual(before.Entities, after.Entities) {
		t.Fatalf("DDL-only fixture changed columns or rows: %v", err)
	}
	files := syncFileManifest(t, path)
	if err := checkDestinationObservation(t.Context(), database, path, expected); progress.ErrorCode(err) != "sync_preview_stale" {
		t.Fatalf("changed native constraint escaped observation check: %v", err)
	}
	if !reflect.DeepEqual(files, syncFileManifest(t, path)) {
		t.Fatal("stale observation check changed destination files")
	}
}
