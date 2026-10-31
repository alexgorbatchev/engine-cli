package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestAPIIntakeLifetimeChild(t *testing.T) {
	library := os.Getenv("ENGINE_API_INTAKE_LIFETIME_CHILD")
	if library == "" {
		return
	}
	service, err := intake.OpenService(t.Context(), library, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := service.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "service-ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func TestAPIIntakeRestoreRefusesForeignServiceBeforeHistory(t *testing.T) {
	s, library := testAPI(t)
	s.config.NoBackup = false
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	queue, err := intake.Open(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := backup.CreateBackup(ctx, library, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAPIIntakeLifetimeChild$")
	child.Env = append(os.Environ(), "ENGINE_API_INTAKE_LIFETIME_CHILD="+library)
	child.Stderr = os.Stderr
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	t.Cleanup(func() {
		if !joined {
			if err := child.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
			if err := child.Wait(); err == nil {
				t.Error("killed child unexpectedly succeeded")
			}
		}
	})
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "service-ready" {
		t.Fatalf("foreign intake readiness = %q %v", scanner.Text(), scanner.Err())
	}
	before, err := os.ReadFile(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, map[string]string{"path": snapshot.Path})
	result, id, err := s.RunOperation(ctx, "library.restore", body)
	if progress.ErrorCode(err) != "intake_replacing" || result != nil || id != "" {
		t.Fatalf("restore bypassed foreign service: result=%v history=%s error=%v", result, id, err)
	}
	if after, err := os.ReadFile(filepath.Join(library, "Database2", "m.db")); err != nil || string(after) != string(before) {
		t.Fatalf("refused replacement changed native collection: %v", err)
	}
	entries, err := history.List(library)
	if err != nil || len(entries) != 0 {
		t.Fatalf("refused replacement enrolled History: %v %v", entries, err)
	}
	backups, err := backup.ListBackups(library, "")
	if err != nil || len(backups) != 0 {
		t.Fatalf("refused replacement created safety backup: %v %v", backups, err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	err = child.Wait()
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunOperation(ctx, "library.restore", body); err != nil {
		t.Fatalf("replacement stayed blocked after foreign service joined: %v", err)
	}
	view := readAPIIntake(t, s)
	if view.RecoveryErrorCode != "" || !view.Control.Paused || view.Control.Reason != "restored" {
		t.Fatalf("replacement did not reopen suspended queue: %+v", view)
	}
}
