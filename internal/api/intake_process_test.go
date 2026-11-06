package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAPIIntakeProcessCancellationKeepsDurablePreparation(t *testing.T) {
	s, lib := testAPI(t)
	s.intake.quiet = time.Hour
	source := filepath.Join(t.TempDir(), "incoming.wav")
	if err := os.WriteFile(source, []byte("source retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunOperation(t.Context(), "intake.enqueue", mustJSON(t, intakePaths{Paths: []string{source}})); err != nil {
		t.Fatal(err)
	}
	service, err := s.intake.open(lib, false)
	if err != nil || service == nil {
		t.Fatalf("durable service absent: %v", err)
	}
	waitIntakeItem(t, service, 1, "preparing")
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	parameters := mustJSON(t, emptyParams{})
	go func() {
		_, _, err := s.RunOperation(ctx, "intake.process", parameters)
		result <- err
	}()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("process ignored request cancellation: %v", err)
	}
	view, err := s.intakeView(t.Context())
	if err != nil || len(view.Items) != 1 || view.Items[0].Status != "preparing" {
		t.Fatalf("request cancellation discarded background queue: %+v %v", view, err)
	}
	if err := s.intake.close(); err != nil {
		t.Fatal(err)
	}
	s.intake = newIntakeLibraries()
	s.intake.quiet = 0
	value, historyID, err := s.RunOperation(t.Context(), "intake.process", parameters)
	if err != nil {
		t.Fatal(err)
	}
	view, ok := value.(*IntakeView)
	if !ok || historyID != "" || len(view.Items) != 1 || view.Items[0].Status != "staged" || view.Items[0].Attempt != 2 || view.Items[0].HistoryID != "" {
		t.Fatalf("explicit process did not resume interrupted preparation: %+v", value)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "history")); !os.IsNotExist(err) {
		t.Fatalf("operational process created History: %v", err)
	}
}
