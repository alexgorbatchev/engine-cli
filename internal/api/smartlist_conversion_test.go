package api

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/smartlist"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestSmartlistConversionSnapshotAndUndo(t *testing.T) {
	for _, parent := range []int64{0, 1} {
		t.Run(fmt.Sprint(parent), func(t *testing.T) {
			s, lib := testAPI(t)
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if _, err := d.Exec(`UPDATE Track SET title='House music' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"House", "House 2"} {
				if _, err := db.CreatePlaylist(t.Context(), d, name, parent, false); err != nil {
					t.Fatal(err)
				}
			}
			id, err := smartlist.Create(t.Context(), d, smartlist.CreateOptions{Title: "House", ParentID: parent, Document: smartlist.Document{Match: "all", Rules: []smartlist.Rule{{Filter: "title", Condition: "contains", Value: "house"}}}})
			if err != nil {
				t.Fatal(err)
			}
			before := mixedNativeState(t, d)
			preview := runAPIJob(t, s, "smartlist.convert.preview", fmt.Sprintf(`{"uuid":%q}`, id))["result"].(map[string]any)
			if preview["title"] != "House 3" || preview["track_count"] != float64(1) || preview["can_apply"] != true || preview["parent_id"] != float64(parent) {
				t.Fatalf("preview=%#v", preview)
			}
			if after := mixedNativeState(t, d); after != before {
				t.Fatal("preview wrote native rows")
			}
			job := runAPIJob(t, s, "smartlist.convert", fmt.Sprintf(`{"uuid":%q,"plan_token":%q}`, id, preview["plan_token"]))
			result := job["result"].(map[string]any)
			created := int64(result["playlist_id"].(float64))
			if result["uuid"] != id || result["title"] != "House 3" || result["track_count"] != float64(1) {
				t.Fatalf("result=%#v", result)
			}
			tree, err := db.ReadPlaylistTree(t.Context(), d)
			if err != nil || len(tree.Issues) > 0 {
				t.Fatalf("tree=%#v err=%v", tree, err)
			}
			nodes := tree.Nodes
			if parent != 0 {
				for _, node := range tree.Nodes {
					if node.ID == parent {
						nodes = node.Children
					}
				}
			}
			if nodes[len(nodes)-1].ID != created || nodes[len(nodes)-2].UUID != id {
				t.Fatalf("wrong tail: %#v", nodes)
			}
			if _, err := d.Exec(`UPDATE Track SET title='Techno music' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			ids, err := smartlist.MatchingIDs(t.Context(), d, id, lib)
			if err != nil || len(ids) != 0 {
				t.Fatalf("membership=%v err=%v", ids, err)
			}
			var count int
			if err := d.QueryRow(`SELECT COUNT(*) FROM PlaylistEntity WHERE listId=?`, created).Scan(&count); err != nil || count != 1 {
				t.Fatalf("snapshot count=%d err=%v", count, err)
			}
			// Restore the external test edit before exercising exact native Undo.
			if _, err := d.Exec(`UPDATE Track SET title='House music' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			runAPIJob(t, s, "library.undo", `{"id":"`+job["history_id"].(string)+`"}`)
			if after := mixedNativeState(t, d); after != before {
				t.Fatalf("Undo mismatch\n%s\n%s", before, after)
			}
		})
	}
}

func TestSmartlistConversionProtectionAndRollback(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`UPDATE Track SET title='House music' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	id, err := smartlist.Create(t.Context(), d, smartlist.CreateOptions{Title: "House", Document: smartlist.Document{Match: "all", Rules: []smartlist.Rule{{Filter: "title", Condition: "contains", Value: "house"}}}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := smartlist.PreviewConversion(t.Context(), d, id, lib)
	if err != nil {
		t.Fatal(err)
	}
	body := json.RawMessage(fmt.Sprintf(`{"uuid":%q,"plan_token":%q}`, id, preview.PlanToken))
	before := mixedNativeState(t, d)
	if _, err := d.Exec(`CREATE TRIGGER reject_conversion BEFORE INSERT ON PlaylistEntity BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunOperation(t.Context(), "smartlist.convert", body); err == nil {
		t.Fatal("expected membership insertion failure")
	}
	if after := mixedNativeState(t, d); after != before {
		t.Fatal("failed membership write left playlist or sibling link changes")
	}
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	_, history, err := s.RunOperation(t.Context(), "smartlist.convert", body)
	if progress.ErrorCode(err) != "engine_desktop_running" || history != "" {
		t.Fatalf("guard history=%q err=%v", history, err)
	}
	if _, _, err := s.RunOperation(t.Context(), "smartlist.convert.preview", json.RawMessage(fmt.Sprintf(`{"uuid":%q}`, id))); err != nil {
		t.Fatalf("protected preview failed: %v", err)
	}
	if _, err := d.Exec(`UPDATE Smartlist SET rules='{"rv":99,"rules":[]}' WHERE listUuid=?`, id); err != nil {
		t.Fatal(err)
	}
	_, err = smartlist.PreviewConversion(t.Context(), d, id, lib)
	if progress.ErrorCode(err) != "smartlist_rules_unsupported" {
		t.Fatalf("unknown rules guessed: %v", err)
	}
}

func TestSmartlistConversionRefusesChangedAndEmptyMembership(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	id, err := smartlist.Create(t.Context(), d, smartlist.CreateOptions{Title: "House", Document: smartlist.Document{Match: "all", Rules: []smartlist.Rule{{Filter: "title", Condition: "contains", Value: "house"}}}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := smartlist.PreviewConversion(t.Context(), d, id, lib)
	if err != nil || preview.CanApply {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	for _, code := range []string{"smartlist_conversion_empty", "smartlist_conversion_changed"} {
		if code == "smartlist_conversion_changed" {
			if _, err := d.Exec(`UPDATE Track SET title='House music' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
		}
		before := mixedNativeState(t, d)
		body := json.RawMessage(fmt.Sprintf(`{"uuid":%q,"plan_token":%q}`, id, preview.PlanToken))
		_, _, err := s.RunOperation(t.Context(), "smartlist.convert", body)
		if progress.ErrorCode(err) != code {
			t.Fatalf("err=%v want=%s", err, code)
		}
		if after := mixedNativeState(t, d); after != before {
			t.Fatal("refusal wrote native rows")
		}
	}
}
