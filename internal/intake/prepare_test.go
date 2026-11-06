package intake

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

const preparationChildLibrary = "ENGINE_INTAKE_PREPARATION_CHILD_LIBRARY"

func TestPreparationPublicationChild(t *testing.T) {
	library := os.Getenv(preparationChildLibrary)
	if library == "" {
		return
	}
	s, err := Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.withWorker(t.Context(), func(ctx context.Context) error {
		item, err := s.Claim(ctx)
		if err != nil {
			return err
		}
		if item == nil {
			return fmt.Errorf("child has no queued source")
		}
		_, err = staging.New(library).CopyLocal(ctx, item.StageID, staging.LocalSource{
			Path: item.SourcePath, Identity: item.SourceIdentity, Size: item.SourceSize, Modified: item.SourceModified,
		})
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(os.Stdout, "staging-published"); err != nil {
			return err
		}
		// The process dies after real staging publication and before its queue
		// receipt. No synthetic journal or injected production callback is used.
		select {}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPreparationRecoversPublishedCopyAfterProcessDeath(t *testing.T) {
	s, library := queueFixture(t)
	source := sourceFixture(t, "recover.wav")
	queued, err := s.Enqueue(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPreparationPublicationChild$")
	cmd.Env = append(os.Environ(), preparationChildLibrary+"="+library)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Error(err)
		}
		if !waited {
			_ = cmd.Wait() // Reap the child on assertion failures too.
		}
	}()
	line := bufio.NewScanner(out)
	if !line.Scan() || line.Text() != "staging-published" {
		t.Fatalf("publication child: %q, %v", line.Text(), line.Err())
	}
	before, err := s.List(t.Context())
	if err != nil || len(before) != 1 || before[0].Status != "preparing" || before[0].ContentHash != "" {
		t.Fatalf("crash boundary queue: %+v, %v", before, err)
	}
	reserved := before[0].StageID
	stage := staging.New(library)
	published, err := stage.Inspect(t.Context(), reserved)
	if err != nil || published.Status != "ready" {
		t.Fatalf("actual published copy: %+v, %v", published, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err == nil {
		t.Fatal("killed child exited successfully")
	}
	recovered, err := s.PrepareNext(t.Context(), 0)
	if err != nil || recovered == nil || recovered.ID != queued.ID || recovered.Status != "staged" || recovered.Attempt != 2 || recovered.StageID != reserved || recovered.ContentHash != published.ContentHash {
		t.Fatalf("resumed receipt: %+v, %v", recovered, err)
	}
	after, err := stage.Inspect(t.Context(), reserved)
	if err != nil || after.AudioPath != published.AudioPath {
		t.Fatalf("recovery duplicated staged audio: %+v, %v", after, err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "source media retained unchanged" {
		t.Fatalf("source modified: %q, %v", data, err)
	}
	if next, err := s.PrepareNext(t.Context(), 0); err != nil || next != nil {
		t.Fatalf("completed source reprocessed: %+v, %v", next, err)
	}
}

func TestPreparationPersistsFailureAndContinuesFIFO(t *testing.T) {
	s, _ := queueFixture(t)
	source := sourceFixture(t, "changed.wav")
	first, err := s.Enqueue(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Enqueue(t.Context(), sourceFixture(t, "next.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("changed producer bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	failed, err := s.PrepareNext(t.Context(), 0)
	if err != nil || failed == nil || failed.ID != first.ID || failed.Status != "failed" || failed.ErrorCode != "intake_source_changed" {
		t.Fatalf("source change result: %+v, %v", failed, err)
	}
	prepared, err := s.PrepareNext(t.Context(), 0)
	if err != nil || prepared == nil || prepared.ID != second.ID || prepared.Status != "staged" {
		t.Fatalf("next source result: %+v, %v", prepared, err)
	}
}
