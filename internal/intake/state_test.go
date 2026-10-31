package intake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
)

func TestQueueTransitionsWaitForPairedCaptureOwner(t *testing.T) {
	for _, action := range []string{"enqueue", "claim", "complete", "cancel", "retry", "pause", "resume", "prepare"} {
		t.Run(action, func(t *testing.T) {
			s, library := queueFixture(t)
			path := sourceFixture(t, "preserved.wav")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			item, err := s.Enqueue(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			if action == "complete" {
				item, err = s.Claim(t.Context())
			} else if action == "retry" {
				item, err = s.Cancel(t.Context(), item.ID)
			} else if action == "resume" {
				err = s.Pause(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			if action == "enqueue" {
				path = sourceFixture(t, "second-preserved.wav")
				original, err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.List(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			control, err := s.Control(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			err = intakestate.With(t.Context(), library, func(context.Context) error {
				ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
				defer cancel()
				var err error
				switch action {
				case "enqueue":
					_, err = s.Enqueue(ctx, path)
				case "claim":
					_, err = s.Claim(ctx)
				case "complete":
					_, err = s.CompleteStage(ctx, StageResult{ID: item.ID, Attempt: item.Attempt, ErrorCode: "operation_failed"})
				case "cancel":
					_, err = s.Cancel(ctx, item.ID)
				case "retry":
					_, err = s.Retry(ctx, item.ID)
				case "pause":
					err = s.Pause(ctx)
				case "resume":
					err = s.Resume(ctx)
				case "prepare":
					_, err = s.PrepareNext(ctx, 0)
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("transition crossed paired capture owner: %v", err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			after, err := s.List(t.Context())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("blocked transition changed queue: before=%+v after=%+v err=%v", before, after, err)
			}
			current, err := s.Control(t.Context())
			if err != nil || current != control {
				t.Fatalf("blocked transition changed control: %+v %v", current, err)
			}
			if err := s.Pause(t.Context()); err != nil {
				t.Fatal("transition leaked native ownership:", err)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
				t.Fatalf("transition changed source bytes: %q %v", got, err)
			}
		})
	}
}

func TestQueueEnrollmentWaitsForPairedCaptureOwner(t *testing.T) {
	library := t.TempDir()
	err := intakestate.With(t.Context(), library, func(context.Context) error {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		s, err := Open(ctx, library)
		if s != nil {
			_ = s.Close()
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queue enrolled during paired presence capture: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(library, ".engine-cli", companion.IntakeName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("blocked enrollment created queue database: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), library)
	if err != nil {
		t.Fatal("enrollment leaked native owner:", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
