package watchclock

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
)

func TestAuthorityObservationDoesNotEnroll(t *testing.T) {
	library := t.TempDir()
	state, err := Read(t.Context(), library)
	if err != nil || state.Revision != 0 || state.Restored != 0 {
		t.Fatalf("absent authority: %+v %v", state, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Advance(ctx, library, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled allocation:", err)
	}
	if _, err := os.Stat(filepath.Join(library, ".engine-cli")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("observation enrolled authority:", err)
	}
}

func TestAuthoritySurvivesUnpublishedImageAndRefusesExhaustion(t *testing.T) {
	library := t.TempDir()
	want := errors.New("portable publication refused")
	err := intakestate.With(t.Context(), library, func(ctx context.Context) error {
		first, err := Advance(ctx, library, 0)
		if err != nil || first != 1 {
			t.Fatalf("initial allocation: %d %v", first, err)
		}
		restored, err := Restore(ctx, library, 40)
		if err != nil || restored != 41 {
			t.Fatalf("foreign image fence: %d %v", restored, err)
		}
		next, err := Advance(ctx, library, 1)
		if err != nil || next != 42 {
			t.Fatalf("ordinary mutation reused generation: %d %v", next, err)
		}
		return want
	})
	if !errors.Is(err, want) {
		t.Fatal("publication error lost:", err)
	}
	err = intakestate.With(t.Context(), library, func(ctx context.Context) error {
		state, err := Read(ctx, library)
		if err != nil || state.Revision != 42 || state.Restored != 41 {
			t.Fatalf("failed publication rolled back authority: %+v %v", state, err)
		}
		if _, err := Advance(ctx, library, math.MaxInt64); err == nil {
			t.Fatal("exhausted authority wrapped generation")
		}
		after, err := Read(ctx, library)
		if err != nil || after != state {
			t.Fatalf("refused allocation changed authority: %+v %v", after, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
