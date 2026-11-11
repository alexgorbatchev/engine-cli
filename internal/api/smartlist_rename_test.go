package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestSmartlistRenameOnlyTitleAndUndo(t *testing.T) {
	for _, rules := range []string{`{"rv":2,"rules":[{"v":"2.20.0"}]}`, `{"rv":99,"rules":[{"v":"99.0.0"}]}`, "opaque malformed rules"} {
		t.Run(rules, func(t *testing.T) {
			s, lib := testAPI(t)
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := d.Close(); err != nil {
					t.Errorf("close fixture: %v", err)
				}
			})
			if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime) VALUES('saved','Saved','','','',?,'2026-01-01')`, rules); err != nil {
				t.Fatal(err)
			}
			before := mixedNativeState(t, d)
			job := runAPIJob(t, s, "smartlist.rename", `{"uuid":"saved","title":"Favorites"}`)
			if result, ok := job["result"].(map[string]any); !ok || result["uuid"] != "saved" {
				t.Fatalf("missing affected Smartlist identity: %#v", job["result"])
			}
			var title, actualRules, parent, nextPath, nextUUID, edited string
			if err := d.QueryRow(`SELECT title,rules,parentPlaylistPath,nextPlaylistPath,nextListUuid,CAST(lastEditTime AS TEXT) FROM Smartlist WHERE listUuid='saved'`).Scan(&title, &actualRules, &parent, &nextPath, &nextUUID, &edited); err != nil {
				t.Fatal(err)
			}
			if title != "Favorites" || actualRules != rules || parent != "" || nextPath != "" || nextUUID != "" || edited != "2026-01-01" {
				t.Fatalf("unexpected row: %q %q %q %q %q %q", title, actualRules, parent, nextPath, nextUUID, edited)
			}
			runAPIJob(t, s, "library.undo", `{"id":"`+job["history_id"].(string)+`"}`)
			if after := mixedNativeState(t, d); after != before {
				t.Fatalf("Undo mismatch: %s != %s", after, before)
			}
		})
	}
}

func TestSmartlistRenameRefusalsPreserveRows(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	})
	if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
	('first','First','','','second','opaque first'),('second','Second','','','','opaque second')`); err != nil {
		t.Fatal(err)
	}
	before := mixedNativeState(t, d)
	for _, tc := range []struct{ body, code string }{
		{`{"uuid":"first","title":"Second"}`, "smartlist_name_conflict"},
		{`{"uuid":"gone","title":"Title"}`, "smartlist_not_found"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			_, _, err := s.RunOperation(t.Context(), "smartlist.rename", json.RawMessage(tc.body))
			if progress.ErrorCode(err) != tc.code {
				t.Fatalf("error=%v, want %s", err, tc.code)
			}
			if after := mixedNativeState(t, d); after != before {
				t.Fatalf("refusal changed rows: %s", after)
			}
		})
	}
	for _, body := range []string{`{"uuid":"first","title":""}`, `{"uuid":"first","title":"  "}`} {
		response := requestAPI(t, s, http.MethodPost, "/v1/operations/smartlist.rename", body)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_parameters"`) {
			t.Fatalf("empty title: status=%d body=%s", response.Code, response.Body)
		}
		if after := mixedNativeState(t, d); after != before {
			t.Fatalf("empty title changed rows: %s", after)
		}
	}
	checks := 0
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		checks++
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	_, id, err := s.RunOperation(t.Context(), "smartlist.rename", json.RawMessage(`{"uuid":"first","title":"Renamed"}`))
	if progress.ErrorCode(err) != "engine_desktop_running" || id != "" || checks != 1 {
		t.Fatalf("Desktop guard: id=%q checks=%d err=%v", id, checks, err)
	}
	if after := mixedNativeState(t, d); after != before {
		t.Fatalf("Desktop refusal changed rows: %s", after)
	}
}
