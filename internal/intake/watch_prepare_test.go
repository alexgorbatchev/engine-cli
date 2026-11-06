package intake

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

func TestWatchPreparationCopiesRenamedSourceWithoutRebindingManualItem(t *testing.T) {
	s, library := queueFixture(t)
	source, _ := watchSourceFixture(t, s)
	original, err := s.Enqueue(t.Context(), source.source.Path)
	if err != nil {
		t.Fatal(err)
	}
	alias := original.SourcePath + "-renamed.wav"
	if err := os.Rename(original.SourcePath, alias); err != nil {
		t.Fatal(err)
	}
	scan, _, err := s.reconcileWatch(t.Context(), 0)
	if err != nil || scan.Associated != 1 || scan.Queued != 0 {
		t.Fatalf("renamed manual source association: %+v %v", scan, err)
	}
	prepared, err := s.PrepareNext(t.Context(), 0)
	if err != nil || prepared == nil || prepared.Status != "staged" || prepared.ContentHash == "" || !watchItemMatches(prepared, itemSource(original)) {
		t.Fatalf("renamed original request not prepared: %+v %v", prepared, err)
	}
	file, err := staging.New(library).Open(t.Context(), prepared.StageID, false)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := io.ReadAll(file)
	if err := errors.Join(err, file.Close()); err != nil || string(bytes) != "native source bytes stay intact" {
		t.Fatalf("staged alias bytes: %q %v", bytes, err)
	}
	if _, err := os.Stat(original.SourcePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original source path was recreated: %v", err)
	}
	bytes, err = os.ReadFile(alias)
	if err != nil || string(bytes) != "native source bytes stay intact" {
		t.Fatalf("source alias changed: %q %v", bytes, err)
	}
}

func TestWatchPreparationSelectsLinkedRenamedNativeVersion(t *testing.T) {
	s, _ := queueFixture(t)
	source, version := watchSourceFixture(t, s)
	original, err := s.Enqueue(t.Context(), source.source.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, provenance, _, err := s.publishWatchHandoff(t.Context(), version, source.source, 0, source.Check); err != nil || provenance != "associated" {
		t.Fatalf("manual association: %s %v", provenance, err)
	}
	claimed, err := s.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	alias := original.SourcePath + "-renamed.wav"
	if err := os.Rename(original.SourcePath, alias); err != nil {
		t.Fatal(err)
	}
	if err := intakestate.With(t.Context(), s.library, func(ctx context.Context) (err error) {
		d, err := openWatch(ctx, s.library)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, d.Close()) }()
		_, err = d.ExecContext(ctx, "UPDATE WatchAlias SET path=? WHERE version_id=?", alias, version)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	selected, err := s.selectPreparationSource(t.Context(), claimed)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.close()
	if err := selected.local.Check(); err != nil {
		t.Fatal(err)
	}
	bytes, err := io.ReadAll(selected.local.File)
	if err != nil || string(bytes) != "native source bytes stay intact" {
		t.Fatalf("retained alias bytes: %q %v", bytes, err)
	}
	current, err := scanItem(s.database.QueryRowContext(t.Context(), "SELECT "+itemColumns+" FROM Item WHERE id=?", original.ID))
	if err != nil || !watchItemMatches(current, itemSource(original)) || current.Attempt != claimed.Attempt || current.Status != "preparing" {
		t.Fatalf("selection mutated queue request: %+v %v", current, err)
	}
	state, err := s.WatchList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WatchSetEnabled(t.Context(), WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision}, false); err != nil {
		t.Fatal(err)
	}
	if err := intakestate.With(t.Context(), s.library, selected.authorize); progress.ErrorCode(err) != "watch_root_changed" {
		t.Fatalf("retained descriptor outlived revoked publication authorization: %v", err)
	}
	if selected, err := s.selectPreparationSource(t.Context(), claimed); selected != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revoked root still authorized alias: %v %v", selected, err)
	}
	if _, err := s.Cancel(t.Context(), claimed.ID); err != nil {
		t.Fatal(err)
	}
	if selected, err := s.selectPreparationSource(t.Context(), claimed); selected != nil || progress.ErrorCode(err) != "intake_state_changed" {
		t.Fatalf("cancelled attempt selected source: %v %v", selected, err)
	}
}
