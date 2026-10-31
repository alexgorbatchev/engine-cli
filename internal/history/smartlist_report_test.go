package history

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestAutomaticSmartlistReportPrecedesLaterFailure(t *testing.T) {
	for _, outcome := range []string{"success", "prepare failure", "run failure", "cancellation"} {
		t.Run(outcome, func(t *testing.T) {
			lib, database := smartlistHistoryLibrary(t)
			recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var repairs []SmartlistRepairResult
			ctx = progress.WithReporter(ctx, func(event progress.Event) {
				if event.Type == "smartlist_repair" {
					var repair SmartlistRepairResult
					if err := json.Unmarshal(event.Result, &repair); err != nil {
						t.Fatal(err)
					}
					repairs = append(repairs, repair)
				}
			})
			failure := errors.New("requested operation refused")
			entry, err := RunPrepared(ctx, lib, RunOptions{Operation: "playlist.rename", Scope: ScopeCollection, RepairSmartlists: true, Prepare: func(context.Context) error {
				if len(repairs) != 1 {
					t.Errorf("committed repair not reported before Prepare: %+v", repairs)
				}
				if outcome == "prepare failure" {
					return failure
				}
				if outcome == "cancellation" {
					cancel()
					return ctx.Err()
				}
				return nil
			}}, func(ctx context.Context) error {
				if outcome == "run failure" {
					return failure
				}
				_, err := database.ExecContext(ctx, `UPDATE Playlist SET title='Edited' WHERE id=2`)
				return err
			})
			if outcome == "success" && (err != nil || entry == nil) {
				t.Fatalf("ordinary success: %+v %v", entry, err)
			}
			if (outcome == "prepare failure" || outcome == "run failure") && !errors.Is(err, failure) {
				t.Fatalf("lost original error: %v", err)
			}
			if outcome == "cancellation" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if len(repairs) != 1 {
				t.Fatalf("repair reports: %+v", repairs)
			}
			r := repairs[0]
			if !r.Applied || r.Reconnected != 1 || r.Removed != 0 || r.UndoID == "" || len(r.Rows) != 1 || r.Rows[0].Name == "" || r.Rows[0].Action != "reconnect" {
				t.Fatalf("incomplete repair outcome: %+v", r)
			}
			if entry != nil && entry.ID == r.UndoID {
				t.Fatal("repair borrowed ordinary Undo identity")
			}
			var path string
			if err := database.QueryRow(`SELECT parentPlaylistPath FROM Smartlist WHERE listUuid='saved'`).Scan(&path); err != nil || path != "Renamed;" {
				t.Fatalf("committed repair lost after later failure: %q %v", path, err)
			}
		})
	}
}

func TestAutomaticSmartlistHealthyMutationHasNoRepairReport(t *testing.T) {
	lib, database := smartlistHistoryLibrary(t)
	reports := 0
	ctx := progress.WithReporter(t.Context(), func(event progress.Event) {
		if event.Type == "smartlist_repair" {
			reports++
		}
	})
	entry, err := RunPrepared(ctx, lib, RunOptions{Operation: "playlist.rename", Scope: ScopeCollection, RepairSmartlists: true}, func(ctx context.Context) error {
		_, err := database.ExecContext(ctx, `UPDATE Playlist SET title='Edited' WHERE id=2`)
		return err
	})
	if err != nil || entry == nil || reports != 0 {
		t.Fatalf("healthy ordinary mutation: entry=%+v reports=%d err=%v", entry, reports, err)
	}
	var title, parent, rules string
	if err := database.QueryRow(`SELECT title,parentPlaylistPath,rules FROM Smartlist WHERE listUuid='saved'`).Scan(&title, &parent, &rules); err != nil || title != "Saved" || parent != "House;" || rules != "opaque" {
		t.Fatalf("healthy saved row changed: %q %q %q %v", title, parent, rules, err)
	}
}
