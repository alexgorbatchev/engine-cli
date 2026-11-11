package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/smartlist"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

const createRulesBody = `{"title":"House picks","parent_id":0,"match":"all","rules":[{"filter":"title","condition":"contains","value":"house"}],"time_zone":"UTC"}`

func TestSmartlistRulesOperationsAndUndo(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('native-null','Existing',NULL,NULL,NULL,'opaque')`); err != nil {
		t.Fatal(err)
	}
	before := mixedNativeState(t, d)
	catalog := runAPIJob(t, s, "smartlist.catalog", "{}")["result"].(map[string]any)
	if len(catalog["filters"].([]any)) != 15 || catalog["max_rules"] != float64(8) {
		t.Fatalf("catalog %#v", catalog)
	}
	created := runAPIJob(t, s, "smartlist.create", createRulesBody)
	id := created["result"].(map[string]any)["uuid"].(string)
	saved := mixedNativeState(t, d)
	getBody, _ := json.Marshal(map[string]string{"uuid": id, "time_zone": "UTC"})
	got := runAPIJob(t, s, "smartlist.get", string(getBody))["result"].(map[string]any)
	if got["title"] != "House picks" || got["read_only"] != false || got["revision"] == "" {
		t.Fatalf("get %#v", got)
	}
	update := smartlist.UpdateOptions{UUID: id, Title: "Techno picks", Revision: got["revision"].(string), Document: smartlist.Document{Match: "one", TimeZone: "UTC", Rules: []smartlist.Rule{{Filter: "genre", Condition: "equal", Value: "Techno"}}}}
	body, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	updated := runAPIJob(t, s, "smartlist.update", string(body))
	if updated["result"].(map[string]any)["uuid"] != id {
		t.Fatal("update lost identity")
	}
	read := runAPIJob(t, s, "smartlist.get", string(getBody))["result"].(map[string]any)
	if read["title"] != "Techno picks" || read["match"] != "one" {
		t.Fatalf("update unreadable %#v", read)
	}
	_, _, err = s.RunOperation(t.Context(), "smartlist.update", body)
	if progress.ErrorCode(err) != "smartlist_changed" {
		t.Fatalf("stale update: %v", err)
	}
	runAPIJob(t, s, "library.undo", `{"id":"`+updated["history_id"].(string)+`"}`)
	if after := mixedNativeState(t, d); after != saved {
		t.Fatalf("update Undo mismatch %s != %s", after, saved)
	}
	runAPIJob(t, s, "library.undo", `{"id":"`+created["history_id"].(string)+`"}`)
	if after := mixedNativeState(t, d); after != before {
		t.Fatalf("create Undo mismatch %s != %s", after, before)
	}
	var preserved bool
	if err := d.QueryRow(`SELECT parentPlaylistPath IS NULL AND nextPlaylistPath IS NULL AND nextListUuid IS NULL AND rules='opaque' FROM Smartlist WHERE listUuid='native-null'`).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("Undo changed native NULL terminal: %v %v", preserved, err)
	}

}

func TestSmartlistRulesWritesRespectDesktopProtection(t *testing.T) {
	s, _ := testAPI(t)
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	for _, test := range []struct{ operation, body string }{{"smartlist.create", createRulesBody}, {"smartlist.update", `{"uuid":"saved","title":"Changed","revision":"rev","match":"all","rules":[{"filter":"title","condition":"contains","value":"house"}]}`}} {
		_, history, err := s.RunOperation(t.Context(), test.operation, json.RawMessage(test.body))
		if progress.ErrorCode(err) != "engine_desktop_running" || history != "" {
			t.Fatalf("%s bypassed protection: history=%q err=%v", test.operation, history, err)
		}
	}
}

func TestSmartlistTracksReadCurrentCollectionWithoutWriting(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := d.Exec(`UPDATE Track SET title='House music' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	id, err := smartlist.Create(t.Context(), d, smartlist.CreateOptions{Title: "House", Document: smartlist.Document{Match: "all", Rules: []smartlist.Rule{{Filter: "title", Condition: "contains", Value: "house"}}}})
	if err != nil {
		t.Fatal(err)
	}
	before := mixedNativeState(t, d)
	body, _ := json.Marshal(map[string]string{"uuid": id})
	got := runAPIJob(t, s, "smartlist.tracks", string(body))["result"].([]any)
	all := runAPIJob(t, s, "track.list", `{}`)["result"].([]any)
	if !reflect.DeepEqual(got, all) {
		t.Fatalf("listing fields differ: %v / %v", got, all)
	}
	if !reflect.DeepEqual(before, mixedNativeState(t, d)) {
		t.Fatal("read changed native rows")
	}
	runAPIJob(t, s, "track.update", `{"identifier":"1","title":"Techno"}`)
	got = runAPIJob(t, s, "smartlist.tracks", string(body))["result"].([]any)
	if len(got) != 0 {
		t.Fatalf("stale matches %v", got)
	}
	if _, err := d.Exec(`UPDATE Smartlist SET rules='{"rv":99}' WHERE listUuid=?`, id); err != nil {
		t.Fatal(err)
	}
	failed := awaitAPIJob(t, s, submitAPIJob(t, s, "smartlist.tracks", string(body)))
	if failed["state"] != "failed" || failed["error"].(map[string]any)["code"] != "smartlist_rules_unsupported" {
		t.Fatalf("unsupported result %v", failed)
	}
}
