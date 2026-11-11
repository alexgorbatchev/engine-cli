package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestSmartlistRemovalChainAndExactUndo(t *testing.T) {
	for _, parent := range []string{"", "Favorites;"} {
		for _, tail := range []string{"", "After;"} {
			for _, count := range []int{1, 3} {
				for removed := 0; removed < count; removed++ {
					t.Run(fmt.Sprintf("%s/%s/%d/%d", parent, tail, count, removed), func(t *testing.T) {
						s, lib := testAPI(t)
						d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
						if err != nil {
							t.Fatal(err)
						}
						defer d.Close()
						parentID := int64(0)
						if parent != "" {
							parentID = 1
						}
						if tail != "" {
							if _, err := db.CreatePlaylist(t.Context(), d, "After", parentID, false); err != nil {
								t.Fatal(err)
							}
						}
						for i := 0; i < count; i++ {
							next, path := "", ""
							if i < count-1 {
								next = fmt.Sprint(i + 1)
							} else if tail != "" {
								path = parent + tail
							}
							if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime) VALUES(?,?,?,?,?,?,?)`, fmt.Sprint(i), fmt.Sprintf("Saved %d", i), parent, path, next, []string{`{"rv":99,"rules":[{"v":"99.0.0"}]}`, "malformed", "opaque"}[i], fmt.Sprintf("2026-01-%02d", i+1)); err != nil {
								t.Fatal(err)
							}
						}
						// Abort on any write to the unrelated tables, including same-value updates.
						for _, table := range []string{"Track", "Playlist", "PlaylistEntity"} {
							for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
								if _, err := d.Exec(fmt.Sprintf(`CREATE TRIGGER protect_%s_%s BEFORE %s ON %s BEGIN SELECT RAISE(ABORT,'unrelated write'); END`, table, op, op, table)); err != nil {
									t.Fatal(err)
								}
							}
						}
						before := mixedNativeState(t, d)
						job := runAPIJob(t, s, "smartlist.rm", fmt.Sprintf(`{"uuid":"%d"}`, removed))
						result, ok := job["result"].(map[string]any)
						if !ok || result["uuid"] != fmt.Sprint(removed) || result["parent_id"] != float64(parentID) {
							t.Fatalf("result=%#v", job["result"])
						}
						tree, err := db.ReadPlaylistTree(t.Context(), d)
						if err != nil || len(tree.Issues) != 0 {
							t.Fatalf("tree=%#v err=%v", tree, err)
						}
						var remaining int
						if err := d.QueryRow(`SELECT COUNT(*) FROM Smartlist`).Scan(&remaining); err != nil {
							t.Fatal(err)
						}
						if remaining != count-1 {
							t.Fatalf("remaining=%d", remaining)
						}
						for i := 0; i < count; i++ {
							if i == removed {
								continue
							}
							next, path := "", ""
							successor := i + 1
							if successor == removed {
								successor++
							}
							if successor < count {
								next = fmt.Sprint(successor)
							} else if tail != "" {
								path = parent + tail
							}
							var gotNext, gotPath string
							if err := d.QueryRow(`SELECT nextListUuid,nextPlaylistPath FROM Smartlist WHERE listUuid=?`, fmt.Sprint(i)).Scan(&gotNext, &gotPath); err != nil {
								t.Fatal(err)
							}
							if gotNext != next || gotPath != path {
								t.Fatalf("links=%q,%q want %q,%q", gotNext, gotPath, next, path)
							}
						}
						runAPIJob(t, s, "library.undo", `{"id":"`+job["history_id"].(string)+`"}`)
						if after := mixedNativeState(t, d); after != before {
							t.Fatalf("undo mismatch\n%s\n%s", before, after)
						}
					})
				}
			}
		}
	}
}

func TestSmartlistRemovalRefusalsAndRollback(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('a','A','','','b','opaque a'),('b','B','','','','opaque b')`); err != nil {
		t.Fatal(err)
	}
	before := mixedNativeState(t, d)
	response := requestAPI(t, s, http.MethodPost, "/v1/operations/smartlist.rm", `{"uuid":""}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_parameters"`) {
		t.Fatalf("empty UUID response=%s", response.Body)
	}
	for _, tc := range []struct{ uuid, code string }{{"missing", "smartlist_not_found"}} {
		_, _, err := s.RunOperation(t.Context(), "smartlist.rm", json.RawMessage(fmt.Sprintf(`{"uuid":%q}`, tc.uuid)))
		if progress.ErrorCode(err) != tc.code {
			t.Fatalf("err=%v want=%s", err, tc.code)
		}
	}
	if _, err := d.Exec(`CREATE TRIGGER reject_delete BEFORE DELETE ON Smartlist BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunOperation(t.Context(), "smartlist.rm", json.RawMessage(`{"uuid":"b"}`)); err == nil {
		t.Fatal("expected delete failure")
	}
	if after := mixedNativeState(t, d); after != before {
		t.Fatal("failed delete changed native links")
	}
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	_, id, err := s.RunOperation(t.Context(), "smartlist.rm", json.RawMessage(`{"uuid":"a"}`))
	if progress.ErrorCode(err) != "engine_desktop_running" || id != "" {
		t.Fatalf("guard id=%s err=%v", id, err)
	}
	if after := mixedNativeState(t, d); after != before {
		t.Fatal("refusal changed native rows")
	}
}
