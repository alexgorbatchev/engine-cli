package api

import (
	"context"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestBulkMetadataAndUndo(t *testing.T) {
	s, lib := testAPI(t)
	s.config.NoBackup = false
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("INSERT INTO Track(id,title,artist,path) VALUES(2,'Second','Other','second.mp3')"); err != nil {
		t.Fatal(err)
	}
	job := runAPIJob(t, s, "track.batch.update", `{"identifiers":["2","1","2"],"artist":"Together","year":2026,"rating_stars":4}`)
	snapshots, err := backup.ListBackups(lib, "")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots=%v error=%v", snapshots, err)
	}
	before, err := db.Open(filepath.Join(snapshots[0].Path, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer before.Close()
	var unchanged int
	if err := before.QueryRow("SELECT COUNT(*) FROM Track WHERE artist IN ('Artist','Other')").Scan(&unchanged); err != nil || unchanged != 2 {
		t.Fatalf("snapshot unchanged=%d error=%v", unchanged, err)
	}
	entries, err := history.List(lib)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v error=%v", entries, err)
	}
	if !reflect.DeepEqual(job["result"], decodeJSON(t, `{"track_ids":[2,1]}`)) {
		t.Fatalf("result=%+v", job)
	}
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM Track WHERE artist='Together' AND year=2026 AND rating=80").Scan(&count); err != nil || count != 2 {
		t.Fatalf("count=%d error=%v", count, err)
	}
	id, ok := job["history_id"].(string)
	if !ok || id == "" {
		t.Fatalf("missing history: %+v", job)
	}
	runAPIJob(t, s, "library.undo", `{"id":"`+id+`"}`)
	if err := d.QueryRow("SELECT COUNT(*) FROM Track WHERE (id=1 AND artist='Artist') OR (id=2 AND artist='Other')").Scan(&count); err != nil || count != 2 {
		t.Fatalf("undo count=%d error=%v", count, err)
	}
}

func TestBulkMetadataRollsBack(t *testing.T) {
	for _, tc := range []struct{ name, selectors, trigger, code string }{
		{"missing", `["1","999"]`, "", "not_found"},
		{"write_failure", `["1","2"]`, "CREATE TRIGGER reject_second BEFORE UPDATE ON Track WHEN old.id=2 BEGIN SELECT RAISE(ABORT,'private diagnostics'); END", "operation_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, lib := testAPI(t)
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if _, err := d.Exec("INSERT INTO Track(id,title,artist,path) VALUES(2,'Second','Other','second.mp3')"); err != nil {
				t.Fatal(err)
			}
			if tc.trigger != "" {
				if _, err := d.Exec(tc.trigger); err != nil {
					t.Fatal(err)
				}
			}
			job := awaitAPIJob(t, s, submitAPIJob(t, s, "track.batch.update", `{"identifiers":`+tc.selectors+`,"artist":"Together"}`))
			if job["state"] != "failed" || job["error"].(map[string]any)["code"] != tc.code {
				t.Fatal(job)
			}
			var count int
			if err := d.QueryRow("SELECT COUNT(*) FROM Track WHERE artist='Together'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial writes=%d error=%v", count, err)
			}
		})
	}
}

func TestBulkMetadataDesktopProtection(t *testing.T) {
	s, lib := testAPI(t)
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "track.batch.update", `{"identifiers":["1"],"artist":"Blocked"}`))
	if job["error"].(map[string]any)["code"] != "engine_desktop_running" || job["history_id"] != nil {
		t.Fatal(job)
	}
	entries, err := history.List(lib)
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries=%v error=%v", entries, err)
	}
}

func TestBulkMetadataValidation(t *testing.T) {
	s, _ := testAPI(t)
	for _, body := range []string{
		`{"identifiers":[],"artist":"New"}`,
		`{"identifiers":["1"]}`,
		`{"identifiers":["1"],"title":" "}`,
		`{"identifiers":["1"],"year":10000}`,
		`{"identifiers":["1"],"rating_stars":6}`,
		`{"identifiers":["1"],"key":24}`,
	} {
		w := requestAPI(t, s, http.MethodPost, "/v1/operations/track.batch.update", body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, w.Code, w.Body)
		}
	}
}
