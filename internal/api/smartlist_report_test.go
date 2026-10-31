package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestAutomaticSmartlistJobRetainsRepairOutcome(t *testing.T) {
	for _, shape := range []string{"remove success", "remove backup failure", "reconnect operation failure"} {
		t.Run(shape, func(t *testing.T) {
			s, database, lib := smartlistRepairAPIFixture(t)
			body := `{"name":"Requested"}`
			if shape == "reconnect operation failure" {
				if _, err := db.CreatePlaylist(t.Context(), database, "House", 0, false); err != nil {
					t.Fatal(err)
				}
				if _, err := database.Exec(`UPDATE Smartlist SET parentPlaylistPath='House;' WHERE listUuid='broken'`); err != nil {
					t.Fatal(err)
				}
				if _, err := history.Run(t.Context(), lib, "playlist.rename", history.ScopeCollection, nil, func(ctx context.Context) error {
					_, err := database.ExecContext(ctx, `UPDATE Playlist SET title='Renamed' WHERE title='House'`)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				body = `{"name":"Requested","parent":"missing"}`
			}
			if shape == "remove backup failure" {
				if err := os.MkdirAll(filepath.Join(lib, ".engine-cli"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(lib, ".engine-cli", "backups"), []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			id := submitAPIJob(t, s, "playlist.create", body)
			job := awaitAPIJob(t, s, id)
			r, ok := job["smartlist_repair"].(map[string]any)
			if !ok || r["applied"] != true {
				t.Fatalf("committed repair missing from terminal job: %+v", job)
			}
			rows, ok := r["rows"].([]any)
			if !ok || len(rows) != 1 {
				t.Fatalf("repair rows lost: %+v", r)
			}
			row := rows[0].(map[string]any)
			if row["name"] != "Unavailable parent" {
				t.Fatalf("named repair lost: %+v", r)
			}
			if shape == "reconnect operation failure" {
				if r["reconnected"] != float64(1) || r["undo_id"] == nil || r["undo_id"] == job["history_id"] {
					t.Fatalf("repair Undo ownership: %+v", job)
				}
			} else if r["removed"] != float64(1) || r["undo_id"] != nil {
				t.Fatalf("unknown removal borrowed Undo: %+v", r)
			}
			if shape == "remove success" {
				if job["state"] != "succeeded" || job["result"].(map[string]any)["title"] != "Requested" || job["history_id"] == nil {
					t.Fatalf("ordinary result overwritten: %+v", job)
				}
			} else if job["state"] != "failed" || job["error"] == nil {
				t.Fatalf("ordinary failure hidden: %+v", job)
			}
			events := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+id+"/events", "")
			if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), `"type":"smartlist_repair"`) || !strings.Contains(events.Body.String(), `"name":"Unavailable parent"`) {
				t.Fatalf("repair missing from replay: %d %s", events.Code, events.Body)
			}
			var broken int
			if err := database.QueryRow(`SELECT count(*) FROM Smartlist WHERE parentPlaylistPath='Missing;' OR parentPlaylistPath='House;'`).Scan(&broken); err != nil || broken != 0 {
				t.Fatalf("repair not durable: %d %v", broken, err)
			}
		})
	}
}

func TestAutomaticSmartlistJobRetainsRepairAfterCancellation(t *testing.T) {
	s, database, _ := smartlistRepairAPIFixture(t)
	op := s.operations["playlist.create"]
	bound, err := op.bind(json.RawMessage(`{"name":"Requested"}`))
	if err != nil {
		t.Fatal(err)
	}
	run := bound.run
	entered := make(chan struct{})
	bound.run = func(ctx context.Context) (any, error) {
		close(entered)
		<-ctx.Done()
		return run(ctx)
	}
	accepted, err := s.start(op, bound)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("requested operation did not reach its post-repair boundary")
	}
	response := requestAPI(t, s, http.MethodPost, "/v1/jobs/"+accepted.ID+"/cancel", "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("cancel: %d %s", response.Code, response.Body)
	}
	job := awaitAPIJob(t, s, accepted.ID)
	repair, ok := job["smartlist_repair"].(map[string]any)
	if job["state"] != "cancelled" || !ok || repair["applied"] != true || repair["removed"] != float64(1) || repair["undo_id"] != nil {
		t.Fatalf("cancelled job lost independent committed repair: %+v", job)
	}
	var remaining, requested int
	if err := database.QueryRow(`SELECT count(*) FROM Smartlist WHERE listUuid='broken'`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("repair did not remain committed: %d %v", remaining, err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM Playlist WHERE title='Requested'`).Scan(&requested); err != nil || requested != 0 {
		t.Fatalf("cancelled requested operation mutated library: %d %v", requested, err)
	}
}

func TestAutomaticSmartlistHealthyJobOmitsRepair(t *testing.T) {
	s, database, _ := smartlistRepairAPIFixture(t)
	if _, err := database.Exec(`DELETE FROM Smartlist WHERE listUuid='broken'`); err != nil {
		t.Fatal(err)
	}
	id := submitAPIJob(t, s, "playlist.create", `{"name":"Requested"}`)
	job := awaitAPIJob(t, s, id)
	if job["state"] != "succeeded" || job["smartlist_repair"] != nil || job["result"].(map[string]any)["title"] != "Requested" {
		t.Fatalf("healthy requested result: %+v", job)
	}
	events := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+id+"/events", "")
	if events.Code != http.StatusOK || strings.Contains(events.Body.String(), `"type":"smartlist_repair"`) {
		t.Fatalf("healthy job emitted repair: %d %s", events.Code, events.Body)
	}
}
