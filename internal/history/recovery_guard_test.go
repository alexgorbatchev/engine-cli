package history

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// interruptedFixture records a completed title edit and then an operation that
// renames the playlist and is interrupted, so its entry stays pending.
func interruptedFixture(t *testing.T) (lib string, d *sql.DB, edit, interrupted *Entry) {
	t.Helper()
	lib, d, _ = fixture(t)
	ctx := context.Background()
	edit, err := Run(ctx, lib, "track.edit", ScopeCollection, nil, func(context.Context) error {
		_, err := d.Exec("UPDATE Track SET title='Edited' WHERE id=1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	interrupted, err = Run(ctx, lib, "playlist.rename", ScopeCollection, nil, func(ctx context.Context) error {
		if _, err := d.Exec("UPDATE Playlist SET title='Interrupted' WHERE id=1"); err != nil {
			return err
		}
		return RequireRecovery(ctx)
	})
	if progress.ErrorCode(err) != RecoveryRequiredCode || interrupted == nil || interrupted.After != nil {
		t.Fatalf("interrupted entry = %+v, err %v", interrupted, err)
	}
	return lib, d, edit, interrupted
}

func queryText(t *testing.T, d *sql.DB, query string) string {
	t.Helper()
	var text string
	if err := d.QueryRow(query).Scan(&text); err != nil {
		t.Fatal(err)
	}
	return text
}

func TestUndoWaitsForInterruptedEntryRecovery(t *testing.T) {
	lib, d, edit, interrupted := interruptedFixture(t)
	ctx := context.Background()
	refused := []struct {
		name string
		run  func() error
	}{
		{"undo of another entry", func() error { _, err := Undo(ctx, lib, edit.ID, UndoOptions{}); return err }},
		{"forced undo of another entry", func() error { _, err := Undo(ctx, lib, edit.ID, UndoOptions{Force: true}); return err }},
		{"undo kept beside later edits", func() error {
			_, err := Undo(ctx, lib, edit.ID, UndoOptions{KeepLater: true})
			return err
		}},
		{"review of another entry", func() error {
			_, err := ReviewUndo(ctx, lib, UndoRequest{IDs: []string{edit.ID}})
			return err
		}},
		{"forced review of another entry", func() error {
			_, err := ReviewUndo(ctx, lib, UndoRequest{IDs: []string{edit.ID}, Force: true})
			return err
		}},
		{"undo of several entries with the interrupted one", func() error {
			_, err := Undo(ctx, lib, "", UndoOptions{IDs: []string{interrupted.ID, edit.ID}, Force: true})
			return err
		}},
		{"review of several entries with the interrupted one", func() error {
			_, err := ReviewUndo(ctx, lib, UndoRequest{IDs: []string{interrupted.ID, edit.ID}})
			return err
		}},
		{"unforced recovery of the interrupted entry", func() error {
			_, err := Undo(ctx, lib, interrupted.ID, UndoOptions{})
			return err
		}},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); progress.ErrorCode(err) != RecoveryRequiredCode {
				t.Fatalf("refusal = %v, code %q", err, progress.ErrorCode(err))
			}
			if title := queryText(t, d, "SELECT title FROM Track WHERE id=1"); title != "Edited" {
				t.Fatalf("refused undo changed the track title to %q", title)
			}
			if list, err := List(lib); err != nil || len(list) != 2 {
				t.Fatalf("refused undo recorded history: %d entries, %v", len(list), err)
			}
		})
	}
}

func TestInterruptedEntryRecoveryReviewIsARead(t *testing.T) {
	lib, d, _, interrupted := interruptedFixture(t)
	ctx := context.Background()
	review, err := ReviewUndo(ctx, lib, UndoRequest{IDs: []string{interrupted.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Records) != 1 || review.Records[0].ID != interrupted.ID || review.Records[0].State != "pending" {
		t.Fatalf("records = %+v", review.Records)
	}
	found := false
	for _, change := range review.Changes {
		for _, field := range change.Fields {
			if change.Table == "Playlist" && field.Field == "title" && field.From == "Interrupted" && field.To == "Favorites" {
				found = true
			}
		}
	}
	if !found || review.PlanToken == "" || review.Blocked || len(review.Conflicts) != 0 {
		t.Fatalf("recovery review = %+v", review)
	}
	if title := queryText(t, d, "SELECT title FROM Playlist WHERE id=1"); title != "Interrupted" {
		t.Fatalf("review changed the playlist title to %q", title)
	}
	list, err := List(lib)
	if err != nil || len(list) != 2 || list[0].ID != interrupted.ID || list[0].State != "pending" || list[0].After != nil {
		t.Fatalf("review changed history: %+v %v", list, err)
	}
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, interrupted.ID, "recovered")); !os.IsNotExist(err) {
		t.Fatalf("review captured recovery into history: %v", err)
	}
}

func TestInterruptedEntryRecoveryAppliesItsReview(t *testing.T) {
	lib, d, edit, interrupted := interruptedFixture(t)
	ctx := context.Background()
	stale, err := ReviewUndo(ctx, lib, UndoRequest{IDs: []string{interrupted.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// Engine DJ Desktop or another tool changes the library after the review.
	if _, err := d.Exec("UPDATE Playlist SET title='Elsewhere' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(ctx, lib, interrupted.ID, UndoOptions{Force: true, PlanToken: stale.PlanToken}); progress.ErrorCode(err) != UndoPlanStaleCode {
		t.Fatalf("stale recovery = %v, code %q", err, progress.ErrorCode(err))
	}
	review, err := ReviewUndo(ctx, lib, UndoRequest{IDs: []string{interrupted.ID}})
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := Undo(ctx, lib, interrupted.ID, UndoOptions{Force: true, PlanToken: review.PlanToken})
	if err != nil {
		t.Fatal(err)
	}
	if title := queryText(t, d, "SELECT title FROM Playlist WHERE id=1"); title != "Favorites" {
		t.Fatalf("recovery restored playlist title %q", title)
	}
	status, err := UndoStatus(lib, review.PlanToken)
	if err != nil || status.State != "applied" || status.HistoryID != recovery.ID {
		t.Fatalf("recovery status = %+v, %v", status, err)
	}
	// Once recovered, other undos run again.
	if _, err := Undo(ctx, lib, edit.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if title := queryText(t, d, "SELECT title FROM Track WHERE id=1"); title != "Original" {
		t.Fatalf("undo after recovery left title %q", title)
	}
}

func TestUndoRecoversTheNewestInterruptedEntryFirst(t *testing.T) {
	lib, d, older, newer := interruptedFixture(t)
	ctx := context.Background()
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	// An older entry left pending, as history written before undos were refused can hold.
	older.After = nil
	older.State = "pending"
	if err := writeEntry(dir, older); err != nil {
		t.Fatal(err)
	}
	if _, err := ReviewUndo(ctx, lib, UndoRequest{IDs: []string{older.ID}}); progress.ErrorCode(err) != RecoveryRequiredCode {
		t.Fatalf("older review = %v, code %q", err, progress.ErrorCode(err))
	}
	if _, err := Undo(ctx, lib, older.ID, UndoOptions{Force: true}); progress.ErrorCode(err) != RecoveryRequiredCode {
		t.Fatalf("older recovery = %v, code %q", err, progress.ErrorCode(err))
	}
	if title := queryText(t, d, "SELECT title FROM Track WHERE id=1"); title != "Edited" {
		t.Fatalf("refused recovery changed the track title to %q", title)
	}
	if _, err := Undo(ctx, lib, newer.ID, UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(ctx, lib, older.ID, UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if title := queryText(t, d, "SELECT title FROM Track WHERE id=1"); title != "Original" {
		t.Fatalf("recovery left track title %q", title)
	}
	if title := queryText(t, d, "SELECT title FROM Playlist WHERE id=1"); title != "Favorites" {
		t.Fatalf("recovery left playlist title %q", title)
	}
}
