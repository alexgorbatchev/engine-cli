package intake

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
)

func TestWatchPreparationRechecksRestoredAuthorizationAfterSelection(t *testing.T) {
	s, _ := queueFixture(t)
	source, _ := watchSourceFixture(t, s)
	original, err := s.Enqueue(t.Context(), source.source.Path)
	if err != nil {
		t.Fatal(err)
	}
	alias := original.SourcePath + "-renamed.wav"
	if err := os.Rename(original.SourcePath, alias); err != nil {
		t.Fatal(err)
	}
	if scan, _, err := s.reconcileWatch(t.Context(), 0); err != nil || scan.Associated != 1 {
		t.Fatalf("alias association: %+v %v", scan, err)
	}
	claimed, err := s.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	selected, err := s.selectPreparationSource(t.Context(), claimed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := selected.close(); err != nil {
			t.Error(err)
		}
	})
	// This is the durable restored image consumed by the publication fence,
	// rather than a replacement callback or a simulated authorization result.
	err = intakestate.With(t.Context(), s.library, func(ctx context.Context) (err error) {
		d, err := openWatch(ctx, s.library)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, d.Close()) }()
		_, err = d.ExecContext(ctx, "UPDATE WatchControl SET reason='restored' WHERE id=1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	watchCode(t, intakestate.With(t.Context(), s.library, selected.authorize), "watch_suspended")
	refused, err := s.selectPreparationSource(t.Context(), claimed)
	if refused != nil {
		if err := refused.close(); err != nil {
			t.Error(err)
		}
		t.Fatal("restored alias selected before explicit activation")
	}
	watchCode(t, err, "watch_suspended")
	current, err := scanItem(s.database.QueryRowContext(t.Context(), "SELECT "+itemColumns+" FROM Item WHERE id=?", claimed.ID))
	if err != nil || !reflect.DeepEqual(claimed, current) {
		t.Fatalf("selection refusal changed queue attempt: %+v %v", current, err)
	}
	state, err := s.WatchList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WatchActivate(t.Context(), state.Revision); err != nil {
		t.Fatal(err)
	}
	if err := intakestate.With(t.Context(), s.library, selected.authorize); err != nil {
		t.Fatal("explicit activation did not restore authorization:", err)
	}
	if err := os.Rename(alias, alias+"-retained"); err != nil {
		t.Fatal(err)
	}
	if selected, err := s.selectPreparationSource(t.Context(), claimed); selected != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("vanished alias accepted: %v %v", selected, err)
	}
	if err := os.WriteFile(alias, []byte("unrelated native replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if selected, err := s.selectPreparationSource(t.Context(), claimed); selected != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("different native alias version accepted: %v %v", selected, err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(alias+"-retained", alias); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.selectPreparationSource(t.Context(), claimed)
	if err != nil {
		t.Fatal("selection did not recover:", err)
	}
	if err := recovered.close(); err != nil {
		t.Fatal(err)
	}
}

func TestManualPreparationRechecksOriginalNameAfterSelection(t *testing.T) {
	s, _ := queueFixture(t)
	path := sourceFixture(t, "original.wav")
	if _, err := s.Enqueue(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	selected, err := s.selectPreparationSource(t.Context(), claimed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := selected.close(); err != nil {
			t.Error(err)
		}
	})
	if err := os.Rename(path, path+"-retained"); err != nil {
		t.Fatal(err)
	}
	if err := selected.local.Check(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unlinked original name remained authorized: %v", err)
	}
	if absent, err := s.selectPreparationSource(t.Context(), claimed); absent != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing unconfigured source selected: %v %v", absent, err)
	}
	if err := os.Rename(path+"-retained", path); err != nil {
		t.Fatal(err)
	}
	if err := selected.local.Check(); err != nil {
		t.Fatal("original name did not recover:", err)
	}
	if err := s.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	if paused, err := s.selectPreparationSource(t.Context(), claimed); paused != nil || err == nil {
		t.Fatalf("paused queue selected preparation: %v %v", paused, err)
	}
}
