package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestAPIIntakeReviewedImportAndUndo(t *testing.T) {
	s, lib := testAPI(t)
	testAPIIntakeReviewedImportAndUndo(t, s, lib)
}

func testAPIIntakeReviewedImportAndUndo(t *testing.T, s *Server, lib string) {
	t.Helper()
	s.intake.quiet = 0
	source := intakeAPISource(t)
	raw, err := json.Marshal(intakePaths{Paths: []string{source}})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, s, "intake.enqueue", string(raw))
	view := readAPIIntake(t, s)
	if len(view.Items) != 1 {
		t.Fatalf("enqueued source missing: %+v", view)
	}
	id := view.Items[0].ID
	service, err := s.intake.open(lib, false)
	if err != nil {
		t.Fatal(err)
	}
	waitIntakeItem(t, service, id, "staged")
	opts := intakeImport{ID: id}
	raw, err = json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	value, historyID, err := s.RunOperation(t.Context(), "intake.import.review", raw)
	if err != nil || historyID != "" {
		t.Fatalf("review: %+v %q %v", value, historyID, err)
	}
	review := value.(*staging.ImportReview)
	if review.Ready != 1 || review.PlanToken == "" {
		t.Fatalf("incomplete review: %+v", review)
	}
	opts.PlanToken = review.PlanToken
	raw, err = json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	if value, historyID, err := s.RunOperation(t.Context(), "intake.import", raw); err == nil || value != nil || historyID != "" {
		t.Fatalf("Desktop permitted import: %+v %q %v", value, historyID, err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "history")); !os.IsNotExist(err) {
		t.Fatalf("protected refusal enrolled history: %v", err)
	}
	s.config.CheckSystem = desktopClosed
	job := runAPIJob(t, s, "intake.import", string(raw))
	historyID, ok := job["history_id"].(string)
	if !ok || historyID == "" {
		t.Fatalf("import lost Undo receipt: %+v", job)
	}
	waitIntakeItem(t, service, id, "imported")
	view = readAPIIntake(t, s)
	if view.Items[0].HistoryID != historyID || view.WorkerErrorCode != "" {
		t.Fatalf("import not reconciled after History: %+v", view)
	}
	undoRaw, err := json.Marshal(undoParams{ID: historyID})
	if err != nil {
		t.Fatal(err)
	}
	undoValue, _, err := s.RunOperation(t.Context(), "library.undo", undoRaw)
	if err != nil || undoValue.(*history.Entry).UndoOf != historyID {
		t.Fatalf("import Undo: %+v %v", undoValue, err)
	}
	reopened, err := s.intake.open(lib, false)
	if err != nil || reopened == service {
		t.Fatalf("Undo reused old queue owner: %v", err)
	}
	waitIntakeItem(t, reopened, id, "cancelled")
	runAPIJob(t, s, "intake.enqueue", string(mustJSON(t, intakePaths{Paths: []string{source}})))
	view = readAPIIntake(t, s)
	if len(view.Items) != 1 || view.Items[0].Status != "cancelled" || view.Items[0].HistoryID != historyID {
		t.Fatalf("Undo requeued an imported source version: %+v", view)
	}
	if data, err := os.ReadFile(source); err != nil || len(data) != 96044 {
		t.Fatalf("import/Undo changed source: %d %v", len(data), err)
	}
}

func intakeAPISource(t *testing.T) string {
	t.Helper()
	const frames = 48000
	data := make([]byte, 44+frames*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], frames)
	binary.LittleEndian.PutUint32(data[28:], frames*2)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], frames*2)
	path := filepath.Join(t.TempDir(), "incoming.wav")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
