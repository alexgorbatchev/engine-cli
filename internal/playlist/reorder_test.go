package playlist

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func reorderFixture(t *testing.T) (string, *sql.DB, ReorderOptions) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	if _, err := db.CreateLibrary(ctx, root); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	statements := []string{
		`INSERT INTO Playlist(id,title,parentListId,lastEditTime) VALUES(9,'Parent',0,'2001-01-01'),(1,'Target',9,'2001-01-01'),(2,'Other',0,'2001-01-01')`,
		`INSERT INTO Track(id,originTrackId,originDatabaseUuid,title) VALUES(10,101,'source','First'),(20,102,'source','Second'),(30,103,'source','Last'),(40,101,'foreign','Other')`,
		`INSERT INTO PlaylistEntity(id,listId,trackId,databaseUuid,nextEntityId,membershipReference) VALUES(300,1,101,'source',200,13),(200,1,999,'source',100,12),(100,1,102,'source',500,11),(500,1,103,'source',0,15),(400,2,101,'foreign',0,14)`,
	}
	for _, statement := range statements {
		if _, err := d.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	return root, d, ReorderOptions{Playlist: "1", View: []int64{10, 20, 30}, Rows: []int{0, 1}, Drop: 3}
}

type reorderState struct {
	Entities []string
	Lists    []string
	Tracks   []string
}

func readReorderState(t *testing.T, d *sql.DB) reorderState {
	t.Helper()
	queries := []string{
		`SELECT id,listId,trackId,databaseUuid,nextEntityId,membershipReference FROM PlaylistEntity ORDER BY id`,
		`SELECT id,title,parentListId,nextListId,lastEditTime,isPersisted,isExplicitlyExported FROM Playlist ORDER BY id`,
		`SELECT id,originTrackId,originDatabaseUuid,title,lastEditTime FROM Track ORDER BY id`,
	}
	var state reorderState
	for i, query := range queries {
		rows, err := d.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var data []string
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(values))
			for j := range values {
				pointers[j] = &values[j]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			data = append(data, string(encoded))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		switch i {
		case 0:
			state.Entities = data
		case 1:
			state.Lists = data
		case 2:
			state.Tracks = data
		}
	}
	return state
}

func TestReorderNativeReviewedApply(t *testing.T) {
	root, d, opts := reorderFixture(t)
	ctx := context.Background()
	before := readReorderState(t, d)
	plan, err := PreviewReorder(ctx, d, root, opts)
	if err != nil || !plan.CanApply || !reflect.DeepEqual(plan.TrackIDs, []int64{30, 10, 20}) || !reflect.DeepEqual(plan.entities, []int64{500, 300, 200, 100}) {
		t.Fatalf("review = %+v, %v", plan, err)
	}
	if after := readReorderState(t, d); !reflect.DeepEqual(before, after) {
		t.Fatal("preview changed collection")
	}
	opts.PlanToken = plan.PlanToken
	if _, err := CheckReorder(ctx, d, root, opts); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyReorder(ctx, d, root, opts)
	if err != nil || result.PlaylistID != 1 || !reflect.DeepEqual(result.TrackIDs, plan.TrackIDs) {
		t.Fatalf("apply = %+v, %v", result, err)
	}
	after := readReorderState(t, d)
	if !reflect.DeepEqual(before.Tracks, after.Tracks) || !reflect.DeepEqual(before.Lists[1:], after.Lists[1:]) || before.Entities[3] != after.Entities[3] || before.Lists[0] == after.Lists[0] {
		t.Fatalf("unrelated rows changed or timestamp unchanged: before=%+v after=%+v", before, after)
	}
	// Entity IDs, origin pairs and membership references are preserved, including
	// the unresolved entity inside the moved visible run.
	for _, row := range []struct {
		id, next, origin, reference int64
	}{{500, 300, 103, 15}, {300, 200, 101, 13}, {200, 100, 999, 12}, {100, 0, 102, 11}} {
		var next, origin, reference int64
		var uuid string
		if err := d.QueryRow(`SELECT nextEntityId,trackId,databaseUuid,membershipReference FROM PlaylistEntity WHERE id=? AND listId=1`, row.id).Scan(&next, &origin, &uuid, &reference); err != nil || next != row.next || origin != row.origin || reference != row.reference || uuid != "source" {
			t.Fatalf("entity %+v became %d/%d/%s/%d: %v", row, next, origin, uuid, reference, err)
		}
	}
	if _, err := ApplyReorder(ctx, d, root, opts); err == nil {
		t.Fatal("old review replayed")
	}
	if _, err := d.Exec(`SELECT 1`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	tracks, err := db.GetPlaylistTracks(ctx, reopened, 1)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, track := range tracks {
		ids = append(ids, track.ID)
	}
	if !reflect.DeepEqual(ids, result.TrackIDs) {
		t.Fatalf("reopened order = %v, want %v", ids, result.TrackIDs)
	}
}

func TestReorderRollback(t *testing.T) {
	for _, tc := range []struct {
		name, trigger string
	}{
		{"later membership", `CREATE TRIGGER reject_reorder BEFORE UPDATE OF nextEntityId ON PlaylistEntity WHEN OLD.id=100 BEGIN SELECT RAISE(ABORT,'injected later membership failure'); END`},
		{"timestamp", `CREATE TRIGGER reject_reorder BEFORE UPDATE OF lastEditTime ON Playlist WHEN OLD.id=1 BEGIN SELECT RAISE(ABORT,'injected timestamp failure'); END`},
		{"ignored membership", `CREATE TRIGGER reject_reorder BEFORE UPDATE OF nextEntityId ON PlaylistEntity WHEN OLD.id=100 BEGIN SELECT RAISE(IGNORE); END`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, d, opts := reorderFixture(t)
			ctx := context.Background()
			plan, err := PreviewReorder(ctx, d, root, opts)
			if err != nil {
				t.Fatal(err)
			}
			opts.PlanToken = plan.PlanToken
			if _, err := d.Exec(tc.trigger); err != nil {
				t.Fatal(err)
			}
			before := readReorderState(t, d)
			if result, err := ApplyReorder(ctx, d, root, opts); err == nil || result != nil {
				t.Fatalf("injected failure = %+v, %v", result, err)
			}
			if after := readReorderState(t, d); !reflect.DeepEqual(before, after) {
				t.Fatalf("failed move partially committed: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestReorderConsentLibraryAndCancellation(t *testing.T) {
	root, d, opts := reorderFixture(t)
	ctx := context.Background()
	before := readReorderState(t, d)
	plan, err := PreviewReorder(ctx, d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := ApplyReorder(ctx, d, root, opts); progress.ErrorCode(err) != "playlist_reorder_stale" || result != nil {
		t.Fatalf("unreviewed apply = %+v, %v", result, err)
	}
	opts.PlanToken = plan.PlanToken
	other := t.TempDir()
	if result, err := ApplyReorder(ctx, d, other, opts); progress.ErrorCode(err) != "playlist_reorder_stale" || result != nil {
		t.Fatalf("different library root accepted = %+v, %v", result, err)
	}
	changed := opts
	changed.Drop = 2
	if result, err := ApplyReorder(ctx, d, root, changed); progress.ErrorCode(err) != "playlist_reorder_stale" || result != nil {
		t.Fatalf("changed intent accepted = %+v, %v", result, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if result, err := ApplyReorder(cancelled, d, root, opts); err == nil || result != nil {
		t.Fatalf("cancelled apply = %+v, %v", result, err)
	}
	if after := readReorderState(t, d); !reflect.DeepEqual(before, after) {
		t.Fatal("unreviewed, cross-library, changed intent or cancelled apply changed collection")
	}
}

func TestReorderRejectsChangedReview(t *testing.T) {
	for _, tc := range []struct {
		name, change string
	}{
		{"membership reference", `UPDATE PlaylistEntity SET membershipReference=99 WHERE id=200`},
		{"unresolved origin", `UPDATE PlaylistEntity SET trackId=998 WHERE id=200`},
		{"library uuid", `UPDATE Information SET uuid='changed-library'`},
		{"playlist title", `UPDATE Playlist SET title='Changed' WHERE id=1`},
		{"hidden membership", `UPDATE PlaylistEntity SET nextEntityId=600 WHERE id=500; INSERT INTO PlaylistEntity(id,listId,trackId,databaseUuid,nextEntityId) VALUES(600,1,888,'source',0)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, d, opts := reorderFixture(t)
			ctx := context.Background()
			plan, err := PreviewReorder(ctx, d, root, opts)
			if err != nil {
				t.Fatal(err)
			}
			opts.PlanToken = plan.PlanToken
			if _, err := d.Exec(tc.change); err != nil {
				t.Fatal(err)
			}
			before := readReorderState(t, d)
			for _, apply := range []func(context.Context, *sql.DB, string, ReorderOptions) (any, error){
				func(ctx context.Context, d *sql.DB, root string, opts ReorderOptions) (any, error) {
					return CheckReorder(ctx, d, root, opts)
				},
				func(ctx context.Context, d *sql.DB, root string, opts ReorderOptions) (any, error) {
					return ApplyReorder(ctx, d, root, opts)
				},
			} {
				if _, err := apply(ctx, d, root, opts); progress.ErrorCode(err) != "playlist_reorder_stale" {
					t.Fatalf("changed review error = %v", err)
				}
			}
			if after := readReorderState(t, d); !reflect.DeepEqual(before, after) {
				t.Fatal("stale review changed collection")
			}
		})
	}
}

func TestReorderViewAndNoop(t *testing.T) {
	for _, tc := range []struct {
		name string
		view []int64
		rows []int
		drop int
		desc bool
		code string
		want []int64
	}{
		{"default", nil, []int{0}, 3, false, "", []int64{20, 30, 10}},
		{"descending", []int64{30, 20, 10}, []int{0}, 3, true, "", []int64{30, 10, 20}},
		{"filtered", []int64{10, 30}, []int{0}, 2, false, "", []int64{20, 30, 10}},
		{"foreign", []int64{40}, []int{0}, 1, false, "playlist_reorder_view", nil},
		{"unsorted", []int64{20, 10}, []int{0}, 2, false, "playlist_reorder_view", nil},
		{"duplicate", []int64{10, 10}, []int{0}, 2, false, "playlist_reorder_view", nil},
		{"selection", []int64{10, 20}, []int{2}, 2, false, "playlist_reorder_view", nil},
		{"noop", []int64{10, 20, 30}, []int{0}, 0, false, "playlist_reorder_unchanged", []int64{10, 20, 30}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, d, opts := reorderFixture(t)
			opts.View, opts.Rows, opts.Drop, opts.Descending = tc.view, tc.rows, tc.drop, tc.desc
			ctx := context.Background()
			before := readReorderState(t, d)
			plan, err := PreviewReorder(ctx, d, root, opts)
			if tc.code == "playlist_reorder_view" {
				if progress.ErrorCode(err) != tc.code {
					t.Fatalf("view error = %v", err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(plan.TrackIDs, tc.want) {
				t.Fatalf("review = %+v, %v; want %v", plan, err, tc.want)
			}
			opts.PlanToken = plan.PlanToken
			_, err = CheckReorder(ctx, d, root, opts)
			if progress.ErrorCode(err) != tc.code {
				t.Fatalf("check error = %v, want %s", err, tc.code)
			}
			if after := readReorderState(t, d); !reflect.DeepEqual(before, after) {
				t.Fatal("review or validation changed collection")
			}
		})
	}
}
