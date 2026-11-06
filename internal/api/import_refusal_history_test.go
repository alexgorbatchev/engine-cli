package api

import (
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

func TestAPIImportRefusalsPreserveLatestUndo(t *testing.T) {
	for _, operation := range []string{"intake.import", "staging.import", "staging.rm"} {
		t.Run(operation, func(t *testing.T) {
			s, lib := testAPI(t)
			s.intake.quiet = 0
			runAPIJob(t, s, "intake.enqueue", string(mustJSON(t, intakePaths{Paths: []string{intakeAPISource(t)}})))
			item := readAPIIntake(t, s).Items[0]
			service, err := s.intake.open(lib, false)
			if err != nil {
				t.Fatal(err)
			}
			waitIntakeItem(t, service, item.ID, "staged")
			item = readAPIIntake(t, s).Items[0]
			created := runAPIJob(t, s, "playlist.create", `{"name":"Change to undo"}`)
			createdID := historyIdentity(t, created)
			body := mustJSON(t, stagedIDsParams{IDs: []string{item.StageID}})
			code := "staging_queue_owned"
			if operation == "intake.import" {
				review, _, err := s.RunOperation(t.Context(), "intake.import.review", mustJSON(t, intakeImport{ID: item.ID}))
				if err != nil {
					t.Fatal(err)
				}
				// A real reviewed plan cannot authorize a different destination.
				body = mustJSON(t, intakeImport{ID: item.ID, PlanToken: review.(*staging.ImportReview).PlanToken, ToPlaylist: "1"})
				code = "staging_import_stale"
			}
			job := awaitAPIJob(t, s, submitAPIJob(t, s, operation, string(body)))
			failure, ok := job["error"].(map[string]any)
			if job["state"] != "failed" || !ok || failure["code"] != code {
				t.Fatalf("wrong refusal: %+v", job)
			}
			if job["history_id"] != nil {
				t.Errorf("refusal returned an Undo identity: %+v", job)
			}
			entries, err := history.List(lib)
			if err != nil || len(entries) != 1 || entries[0].ID != createdID {
				t.Errorf("refusal polluted History: %+v error=%v", entries, err)
			}
			undone := runAPIJob(t, s, "library.undo", `{}`)
			result := undone["result"].(map[string]any)
			if result["undo_of"] != createdID {
				t.Errorf("latest Undo selected refusal instead of real change: %+v", undone)
			}
			assertUndoPlaylist(t, lib, "Change to undo", false)
		})
	}
}

func TestAPIPrechangeWriteRefusalsPreserveLatestUndo(t *testing.T) {
	for _, tt := range []struct{ operation, body string }{
		{"playlist.create", `{"name":"Refused","parent":"missing"}`},
		{"playlist.rm", `{"identifier":"missing"}`},
		{"playlist.track.add", `{"playlist":"missing","tracks":["1"]}`},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			s, lib := testAPI(t)
			createdID := historyIdentity(t, runAPIJob(t, s, "playlist.create", `{"name":"Change to undo"}`))
			job := awaitAPIJob(t, s, submitAPIJob(t, s, tt.operation, tt.body))
			if job["state"] != "failed" || job["history_id"] != nil {
				t.Fatalf("prechange refusal returned Undo: %+v", job)
			}
			entries, err := history.List(lib)
			if err != nil || len(entries) != 1 || entries[0].ID != createdID {
				t.Fatalf("prechange refusal polluted History: %+v %v", entries, err)
			}
			undone := runAPIJob(t, s, "library.undo", `{}`)
			if undone["result"].(map[string]any)["undo_of"] != createdID {
				t.Fatalf("Undo did not select real change: %+v", undone)
			}
			assertUndoPlaylist(t, lib, "Change to undo", false)
		})
	}
}
