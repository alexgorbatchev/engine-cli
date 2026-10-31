package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
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

// A queue poll must not spend a job slot, or a Staging page left open evicts finished jobs.
func TestAPIIntakeListReadDoesNotCreateJobs(t *testing.T) {
	s, _ := testAPI(t)
	finished := runAPIJob(t, s, "library.history", "{}")["id"].(string)
	for range maxJobs + 1 {
		if response := requestAPI(t, s, http.MethodGet, "/v1/intake", ""); response.Code != http.StatusOK {
			t.Fatalf("intake read: %d %s", response.Code, response.Body)
		}
	}
	if response := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+finished, ""); response.Code != http.StatusOK {
		t.Fatalf("polling evicted a finished job: %d", response.Code)
	}
}

// The queue's receipts deduplicate imports only while the queue is the only way in.
func TestAPIQueueOwnedCopiesStayOutOfStaging(t *testing.T) {
	s, lib := testAPI(t)
	s.intake.quiet = 0
	raw, err := json.Marshal(intakePaths{Paths: []string{intakeAPISource(t)}})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, s, "intake.enqueue", string(raw))
	id := readAPIIntake(t, s).Items[0].ID
	service, err := s.intake.open(lib, false)
	if err != nil {
		t.Fatal(err)
	}
	waitIntakeItem(t, service, id, "staged")
	stage := readAPIIntake(t, s).Items[0].StageID
	if listed := requestAPI(t, s, http.MethodGet, "/v1/staging", ""); !strings.Contains(listed.Body.String(), "[]") {
		t.Fatalf("queue-owned copy listed in Staging: %s", listed.Body)
	}
	for operation, parameters := range map[string]string{"staging.import": `{"ids":["` + stage + `"]}`, "staging.rm": `{"ids":["` + stage + `"]}`} {
		if _, _, err := s.RunOperation(t.Context(), operation, []byte(parameters)); progress.ErrorCode(err) != "staging_queue_owned" {
			t.Fatalf("%s: %v", operation, err)
		}
	}
	if _, err := staging.New(lib).Inspect(t.Context(), stage); err != nil {
		t.Fatalf("refusal lost the staged copy: %v", err)
	}
}

func TestAPIIntakeImportKeepsUntaggedSourceName(t *testing.T) {
	s, lib := testAPI(t)
	s.intake.quiet = 0
	source := filepath.Join(t.TempDir(), "Evening Set.wav")
	audio, err := os.ReadFile(intakeAPISource(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, audio, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(intakePaths{Paths: []string{source}})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, s, "intake.enqueue", string(raw))
	id := readAPIIntake(t, s).Items[0].ID
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
	value, _, err := s.RunOperation(t.Context(), "intake.import.review", raw)
	if err != nil {
		t.Fatal(err)
	}
	review := value.(*staging.ImportReview)
	if filepath.Base(review.Items[0].Destination) != "Evening Set.wav" {
		t.Fatalf("destination lost the source name: %+v", review.Items[0])
	}
	opts.PlanToken = review.PlanToken
	raw, err = json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	job := runAPIJob(t, s, "intake.import", string(raw))
	waitIntakeItem(t, service, id, "imported")
	trackID := int64(job["result"].(map[string]any)["track_id"].(float64))
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var title string
	if err := database.QueryRow("SELECT title FROM Track WHERE id=?", trackID).Scan(&title); err != nil || title != "Evening Set" {
		t.Fatalf("untagged import lost its name: %q %v", title, err)
	}
}

// Text is not audio; importing it would write a Track row Engine DJ cannot play.
func TestAPIIntakeReviewRefusesFilesWithoutAudio(t *testing.T) {
	s, lib := testAPI(t)
	s.intake.quiet = 0
	source := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(source, []byte("not audio\n"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(intakePaths{Paths: []string{source}})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, s, "intake.enqueue", string(raw))
	id := readAPIIntake(t, s).Items[0].ID
	service, err := s.intake.open(lib, false)
	if err != nil {
		t.Fatal(err)
	}
	waitIntakeItem(t, service, id, "staged")
	raw, err = json.Marshal(intakeImport{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunOperation(t.Context(), "intake.import.review", raw); progress.ErrorCode(err) != "track_not_audio" {
		t.Fatalf("text passed review: %v", err)
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
