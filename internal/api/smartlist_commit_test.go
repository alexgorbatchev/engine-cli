package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestSmartlistAPICommittedRepairWinsLateCancellation(t *testing.T) {
	s, d, lib := smartlistRepairAPIFixture(t)
	review, err := history.PreviewSmartlistRepair(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	op := s.operations["library.smartlist-repair.apply"]
	bound, err := op.bind(smartlistRepairBody(t, review.PlanToken))
	if err != nil {
		t.Fatal(err)
	}
	run := bound.run
	committed := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	bound.run = func(ctx context.Context) (any, error) {
		result, err := run(ctx)
		if err == nil {
			close(committed)
			<-release
		}
		return result, err
	}
	job, err := s.start(op, bound)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-committed:
	case <-time.After(10 * time.Second):
		t.Fatal("native repair did not durably commit")
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM Smartlist WHERE listUuid='broken'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("native repair was not committed before cancellation: %d %v", count, err)
	}
	response := requestAPI(t, s, http.MethodPost, "/v1/jobs/"+job.ID+"/cancel", "")
	if response.Code != http.StatusAccepted && response.Code != http.StatusOK {
		t.Fatalf("late cancellation status=%d body=%s", response.Code, response.Body)
	}
	// The callback returns its actual committed result only after cancellation.
	release <- struct{}{}
	finished := awaitAPIJob(t, s, job.ID)
	if finished["state"] != "succeeded" || finished["error"] != nil {
		t.Fatalf("late cancellation hid committed repair: %+v", finished)
	}
	encoded, err := json.Marshal(finished["result"])
	if err != nil {
		t.Fatal(err)
	}
	var result history.SmartlistRepairResult
	if err := json.Unmarshal(encoded, &result); err != nil || !result.Applied || result.Removed != 1 || result.PlanToken != review.PlanToken {
		t.Fatalf("committed result lost: %+v %v", result, err)
	}
}
