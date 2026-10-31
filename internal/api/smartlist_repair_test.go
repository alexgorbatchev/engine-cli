package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func smartlistRepairAPIFixture(t *testing.T) (*Server, *sql.DB, string) {
	t.Helper()
	lib := t.TempDir()
	if _, err := db.CreateLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
		('broken','Unavailable parent','Missing;','','','retain broken evidence'),
		('healthy','Independent saved list','','','','retain healthy rules')`); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{LibraryDir: lib, CheckSystem: desktopClosed})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, d, lib
}

func smartlistRepairBody(t *testing.T, token string) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(smartlistRepairApplyParams{PlanToken: token})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestSmartlistRepairAPIReviewAndApply(t *testing.T) {
	s, d, lib := smartlistRepairAPIFixture(t)
	checks := 0
	s.config.CheckSystem = func(ctx context.Context, tools []string) (*systemcheck.Report, error) {
		checks++
		return desktopClosed(ctx, tools)
	}
	value, id, err := s.RunOperation(context.Background(), "library.smartlist-repair.preview", json.RawMessage(`{}`))
	if err != nil || id != "" || checks != 0 {
		t.Fatalf("preview=%+v id=%q checks=%d error=%v", value, id, checks, err)
	}
	review := value.(*history.SmartlistRepairReview)
	if review.PlanToken == "" || review.Removed != 1 || review.Reconnected != 0 || review.AmendedDeletes != 0 || len(review.Rows) != 1 || review.Rows[0].UUID != "broken" || review.Rows[0].Action != "remove" {
		t.Fatalf("incorrect native review: %+v", review)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("preview enrolled repair storage: %v", err)
	}
	var rows int
	if err := d.QueryRow(`SELECT COUNT(*) FROM Smartlist`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("preview changed saved rows: %d %v", rows, err)
	}
	job := runAPIJob(t, s, "library.smartlist-repair.apply", string(smartlistRepairBody(t, review.PlanToken)))
	result := job["result"].(map[string]any)
	if result["applied"] != true || result["removed"] != float64(1) || result["plan_token"] != review.PlanToken || checks != 1 || job["history_id"] != nil {
		t.Fatalf("repair result/guard/history: %+v checks=%d", job, checks)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM Smartlist WHERE listUuid='broken'`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("broken row remains: %d %v", rows, err)
	}
	var rules string
	if err := d.QueryRow(`SELECT rules FROM Smartlist WHERE listUuid='healthy'`).Scan(&rules); err != nil || rules != "retain healthy rules" {
		t.Fatalf("unrelated saved row changed: %q %v", rules, err)
	}
	entries, err := history.List(lib)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unproven removal fabricated Undo: %+v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "backups")); !os.IsNotExist(err) {
		t.Fatalf("generic backup wrapped repair publisher: %v", err)
	}
	value, id, err = s.RunOperation(context.Background(), "library.smartlist-repair.apply", smartlistRepairBody(t, review.PlanToken))
	if progress.ErrorCode(err) != "smartlist_repair_stale" || value != nil || id != "" {
		t.Fatalf("stale repair replay: value=%+v id=%q error=%v", value, id, err)
	}
}

func TestSmartlistRepairAPIRequiresFreshDesktopProtection(t *testing.T) {
	for _, name := range []string{"library.smartlist-repair.apply", "library.smartlist-repair.recover"} {
		t.Run(name, func(t *testing.T) {
			s, d, lib := smartlistRepairAPIFixture(t)
			checks := 0
			s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
				checks++
				return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
			}
			body := json.RawMessage(`{}`)
			if name == "library.smartlist-repair.apply" {
				body = smartlistRepairBody(t, "unreviewed")
			}
			value, id, err := s.RunOperation(context.Background(), name, body)
			if progress.ErrorCode(err) != "engine_desktop_running" || value != nil || id != "" || checks != 1 {
				t.Fatalf("Desktop refusal: value=%+v id=%q checks=%d error=%v", value, id, checks, err)
			}
			var rows int
			if err := d.QueryRow(`SELECT COUNT(*) FROM Smartlist`).Scan(&rows); err != nil || rows != 2 {
				t.Fatalf("protected operation changed saved rows: %d %v", rows, err)
			}
			if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
				t.Fatalf("Desktop refusal created safety artifacts: %v", err)
			}
		})
	}
}

func TestSmartlistRepairPendingBlocksOrdinaryAPIBeforeBackup(t *testing.T) {
	s, d, lib := smartlistRepairAPIFixture(t)
	if _, err := d.Exec(`INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(1,'House',0,0)`); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	if err := os.MkdirAll(active, 0700); err != nil {
		t.Fatal(err)
	}
	// An unreadable interrupted intent must refuse writes, never auto-recover.
	pending := filepath.Join(active, "pending.json")
	const evidence = "incomplete interrupted publication"
	if err := os.WriteFile(pending, []byte(evidence), 0600); err != nil {
		t.Fatal(err)
	}
	value, id, err := s.RunOperation(t.Context(), "playlist.rename", json.RawMessage(`{"identifier":"1","title":"Changed"}`))
	if progress.ErrorCode(err) != "smartlist_repair_pending" || value != nil || id != "" {
		t.Fatalf("pending ordinary write: result=%+v id=%q error=%v", value, id, err)
	}
	var title string
	if err := d.QueryRow(`SELECT title FROM Playlist WHERE id=1`).Scan(&title); err != nil || title != "House" {
		t.Fatalf("pending operation changed native playlist: %q %v", title, err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "backups")); !os.IsNotExist(err) {
		t.Fatalf("generic backup ran before pending refusal: %v", err)
	}
	got, err := os.ReadFile(pending)
	if err != nil || string(got) != evidence {
		t.Fatalf("ordinary operation altered recovery evidence: %q %v", got, err)
	}
}

func TestSmartlistRepairAPIReconnectExposesOwnedUndo(t *testing.T) {
	s, d, lib := smartlistRepairAPIFixture(t)
	ctx := context.Background()
	if _, err := db.CreatePlaylist(ctx, d, "House", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE Smartlist SET parentPlaylistPath='House;' WHERE listUuid='broken'`); err != nil {
		t.Fatal(err)
	}
	original, err := history.Run(ctx, lib, "playlist.rename", history.ScopeCollection, nil, func(ctx context.Context) error {
		_, err := d.ExecContext(ctx, `UPDATE Playlist SET title='Renamed' WHERE title='House'`)
		return err
	})
	if err != nil || original == nil {
		t.Fatalf("record original native rename: %+v %v", original, err)
	}
	value, _, err := s.RunOperation(ctx, "library.smartlist-repair.preview", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	review := value.(*history.SmartlistRepairReview)
	if review.Reconnected != 1 || review.Removed != 0 || review.AmendedDeletes != 0 {
		t.Fatalf("reconnect review: %+v", review)
	}
	job := runAPIJob(t, s, "library.smartlist-repair.apply", string(smartlistRepairBody(t, review.PlanToken)))
	result := job["result"].(map[string]any)
	undo, ok := result["undo_id"].(string)
	if !ok || undo == "" || job["history_id"] != undo || undo == original.ID {
		t.Fatalf("repair-owned Undo missing from job: %+v", job)
	}
	entries, err := history.List(lib)
	if err != nil || len(entries) != 2 {
		t.Fatalf("generic wrapper fabricated extra history: %+v %v", entries, err)
	}
	var parent string
	if err := d.QueryRow(`SELECT parentPlaylistPath FROM Smartlist WHERE listUuid='broken'`).Scan(&parent); err != nil || parent != "Renamed;" {
		t.Fatalf("reconnect not applied: %q %v", parent, err)
	}
	body, err := json.Marshal(map[string]string{"id": undo})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, s, "library.undo", string(body))
	if err := d.QueryRow(`SELECT parentPlaylistPath FROM Smartlist WHERE listUuid='broken'`).Scan(&parent); err != nil || parent != "House;" {
		t.Fatalf("repair Undo did not restore saved path: %q %v", parent, err)
	}
	var title string
	if err := d.QueryRow(`SELECT title FROM Playlist WHERE id=1`).Scan(&title); err != nil || title != "Renamed" {
		t.Fatalf("repair Undo reverted original rename: %q %v", title, err)
	}
}

func TestSmartlistRepairAPIStrictParameters(t *testing.T) {
	s, _, lib := smartlistRepairAPIFixture(t)
	for _, tc := range []struct{ operation, body string }{
		{"library.smartlist-repair.apply", `{}`},
		{"library.smartlist-repair.apply", `{"plan_token":null}`},
		{"library.smartlist-repair.apply", `{"plan_token":""}`},
		{"library.smartlist-repair.apply", `{"plan_token":1}`},
		{"library.smartlist-repair.apply", `{"plan_token":"x","force":true}`},
		{"library.smartlist-repair.preview", `{"recover":true}`},
		{"library.smartlist-repair.recover", `{"plan_token":"x"}`},
	} {
		t.Run(tc.operation+tc.body, func(t *testing.T) {
			value, id, err := s.RunOperation(context.Background(), tc.operation, json.RawMessage(tc.body))
			if err == nil || value != nil || id != "" {
				t.Fatalf("accepted invalid parameters: value=%+v id=%q error=%v", value, id, err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("invalid parameters enrolled storage: %v", err)
	}
}

func TestSmartlistRepairAPIScopedLibraryIsolation(t *testing.T) {
	s, first, _ := smartlistRepairAPIFixture(t)
	other, second, lib := smartlistRepairAPIFixture(t)
	other.Close()
	opened := requestAPI(t, s, http.MethodPost, "/v1/libraries", fmt.Sprintf(`{"paths":[%q]}`, lib))
	var libraries []openedLibrary
	if opened.Code != http.StatusOK || json.Unmarshal(opened.Body.Bytes(), &libraries) != nil || len(libraries) != 1 {
		t.Fatalf("open scoped library: %d %s", opened.Code, opened.Body)
	}
	prefix := libraries[0].URL
	scoped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = prefix + strings.TrimPrefix(r.URL.Path, "/v1")
		s.ServeHTTP(w, r)
	})
	token := ""
	for _, operation := range []string{"library.smartlist-repair.preview", "library.smartlist-repair.apply"} {
		body := "{}"
		if token != "" {
			body = string(smartlistRepairBody(t, token))
		}
		response := requestAPI(t, s, http.MethodPost, prefix+"/operations/"+operation, body)
		if response.Code != http.StatusAccepted || !strings.HasPrefix(response.Header().Get("Location"), prefix+"/") {
			t.Fatalf("scoped submit: %d %s", response.Code, response.Body)
		}
		var accepted Job
		if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
		job := awaitAPIJob(t, scoped, accepted.ID)
		if job["state"] != "succeeded" {
			t.Fatalf("scoped repair failed: %+v", job)
		}
		token = job["result"].(map[string]any)["plan_token"].(string)
	}
	for _, tc := range []struct {
		name string
		d    *sql.DB
		want int
	}{{"default unchanged", first, 1}, {"scoped repaired", second, 0}} {
		var count int
		if err := tc.d.QueryRow(`SELECT COUNT(*) FROM Smartlist WHERE listUuid='broken'`).Scan(&count); err != nil || count != tc.want {
			t.Fatalf("%s: count=%d error=%v", tc.name, count, err)
		}
	}
}
