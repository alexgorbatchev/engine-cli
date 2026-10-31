package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/api"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/spf13/cobra"
)

func intakeCLILibrary(t *testing.T) string {
	t.Helper()
	t.Setenv("AGENT", "0")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	original := querySystem
	t.Cleanup(func() { querySystem = original })
	querySystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{
			systemcheck.EngineDesktop: {Status: "ok", Running: new(false)},
		}}, nil
	}
	lib := t.TempDir()
	if _, err := db.CreateLibrary(t.Context(), lib); err != nil {
		t.Fatal(err)
	}
	return lib
}

func executeIntakeCLI(args ...string) (string, error) {
	var out, diagnostics bytes.Buffer
	command := newRootCommand()
	command.SetOut(&out)
	command.SetErr(&diagnostics)
	command.SetArgs(args)
	err := command.Execute()
	return out.String(), err
}

func TestIntakeCLIControlsAndDurableSource(t *testing.T) {
	lib := intakeCLILibrary(t)
	source := filepath.Join(t.TempDir(), "incoming.wav")
	if err := os.WriteFile(source, []byte("original source"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		args []string
		want string
	}{
		{[]string{"pause"}, "Queue paused: manual"},
		{[]string{"enqueue", source}, "Item 1: queued"},
		{[]string{"cancel", "1"}, "Item 1: cancelled"},
		{[]string{"resume"}, "Queue active"},
	} {
		out, err := executeIntakeCLI(append([]string{"-l", lib, "intake"}, step.args...)...)
		if err != nil || !strings.Contains(out, step.want) {
			t.Fatalf("%v: %s %v", step.args, out, err)
		}
	}
	out, err := executeIntakeCLI("-l", lib, "intake", "list", "--json")
	var view api.IntakeView
	if err != nil || json.Unmarshal([]byte(out), &view) != nil || len(view.Items) != 1 || view.Items[0].Status != "cancelled" || view.Control.Paused {
		t.Fatalf("durable control/list: %s %v", out, err)
	}
	data, err := os.ReadFile(source)
	if err != nil || string(data) != "original source" {
		t.Fatalf("queue changed original: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "history")); !os.IsNotExist(err) {
		t.Fatalf("operational queue created collection History: %v", err)
	}
	for _, args := range [][]string{{"cancel", "0"}, {"retry", "bad"}, {"import", "1"}, {"enqueue"}} {
		if out, err := executeIntakeCLI(append([]string{"-l", lib, "intake"}, args...)...); err == nil {
			t.Fatalf("invalid arguments accepted: %v %s", args, out)
		}
	}
}

func TestIntakeCLIReviewedImportAndUndo(t *testing.T) {
	lib := intakeCLILibrary(t)
	source := filepath.Join(t.TempDir(), "incoming.wav")
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
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	queue, err := intake.Open(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	item, err := queue.PrepareNext(t.Context(), 0)
	if closeErr := queue.Close(); err != nil || closeErr != nil || item == nil || item.Status != "staged" {
		t.Fatalf("native preparation: %+v %v %v", item, err, closeErr)
	}
	out, err := executeIntakeCLI("-l", lib, "intake", "import-review", "1", "--json")
	var review staging.ImportReview
	if err != nil || json.Unmarshal([]byte(out), &review) != nil || review.Ready != 1 || review.PlanToken == "" {
		t.Fatalf("native review: %s %v", out, err)
	}
	if out, err := executeIntakeCLI("-l", lib, "intake", "import-review", "1"); err != nil || !strings.Contains(out, "Ready: 1\nSkipped: 0") || !strings.Contains(out, "Plan token: "+review.PlanToken) {
		t.Fatalf("human review: %s %v", out, err)
	}
	for _, mode := range []string{"0", "1"} {
		t.Setenv("AGENT", mode)
		for _, operation := range []string{"staging.import", "staging.import.review"} {
			var untouched bytes.Buffer
			command := &cobra.Command{}
			command.SetOut(&untouched)
			if handled, err := printIntake(command, operation, &review); handled || err != nil || untouched.Len() != 0 {
				t.Fatalf("intake formatter consumed %s in mode %s: handled=%v output=%q error=%v", operation, mode, handled, untouched.String(), err)
			}
		}
	}
	t.Setenv("AGENT", "1")
	if out, err := executeIntakeCLI("-l", lib, "intake", "import-review", "1"); err != nil || !strings.Contains(out, "ready:1 skipped:0") || !strings.Contains(out, "plan_token:"+review.PlanToken) {
		t.Fatalf("agent review: %s %v", out, err)
	}
	out, err = executeIntakeCLI("-l", lib, "--no-backup", "intake", "import", "1", "--plan-token", review.PlanToken, "--json")
	var receipt intake.ImportReceipt
	if err != nil || json.Unmarshal([]byte(out), &receipt) != nil || receipt.TrackID <= 0 || receipt.HistoryID == "" {
		t.Fatalf("native import: %s %v", out, err)
	}
	if out, err := executeIntakeCLI("-l", lib, "--no-backup", "library", "undo", receipt.HistoryID); err != nil {
		t.Fatalf("native import Undo: %s %v", out, err)
	}
	out, err = executeIntakeCLI("-l", lib, "intake", "list", "--json")
	var view api.IntakeView
	if err != nil || json.Unmarshal([]byte(out), &view) != nil || len(view.Items) != 1 || view.Items[0].Status != "cancelled" || view.Items[0].HistoryID != receipt.HistoryID {
		t.Fatalf("Undo receipt reconciliation: %s %v", out, err)
	}
	got, err := os.ReadFile(source)
	if err != nil || string(got) != string(data) {
		t.Fatalf("import/Undo changed original source: %v", err)
	}
}
