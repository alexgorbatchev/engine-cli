package intake

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

const importChildLibrary = "ENGINE_INTAKE_IMPORT_CHILD_LIBRARY"

func TestImportCommitChild(t *testing.T) {
	lib := os.Getenv(importChildLibrary)
	if lib == "" {
		return
	}
	s, err := Open(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	items, err := s.List(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("child queue: %+v, %v", items, err)
	}
	opts := ImportOptions{ID: items[0].ID}
	review, err := s.ReviewImport(t.Context(), d, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.PlanToken = review.PlanToken
	entry, err := history.Run(t.Context(), lib, "intake.import", history.ScopeCollection, nil, func(ctx context.Context) error {
		_, err := s.ApplyImport(ctx, d, opts)
		return err
	})
	if err != nil || entry == nil || entry.State != "complete" {
		t.Fatalf("child import: %+v, %v", entry, err)
	}
	fmt.Println("collection-committed")
	select {}
}

func TestImportReconcilesCommittedProcessDeathAndUndo(t *testing.T) {
	s, lib := queueFixture(t)
	d := importCollectionFixture(t, lib)
	source := writeImportWAV(t)
	queued, err := s.Enqueue(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := s.PrepareNext(t.Context(), 0)
	if err != nil || staged == nil || staged.Status != "staged" {
		t.Fatalf("prepared: %+v, %v", staged, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestImportCommitChild$", "-test.v")
	child.Env = append(os.Environ(), importChildLibrary+"="+lib)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			child.Process.Kill()
			child.Wait()
		}
	})
	scanner := bufio.NewScanner(stdout)
	committed := false
	for scanner.Scan() {
		if scanner.Text() == "collection-committed" {
			committed = true
			break
		}
	}
	if !committed {
		t.Fatalf("child did not commit: %v", scanner.Err())
	}
	before, err := s.List(t.Context())
	if err != nil || len(before) != 1 || before[0].Status != "importing" || before[0].HistoryID == "" {
		t.Fatalf("durable importing state: %+v, %v", before, err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("killed child unexpectedly succeeded")
	}
	waited = true
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.ReconcileImports(t.Context()); err != nil {
		t.Fatal(err)
	}
	items, err := restarted.List(t.Context())
	if err != nil || len(items) != 1 || items[0].Status != "imported" || items[0].HistoryID != before[0].HistoryID {
		t.Fatalf("reconciled: %+v, %v", items, err)
	}
	var count, length int
	if err := d.QueryRow(`SELECT count(*),max(length) FROM Track`).Scan(&count, &length); err != nil || count != 1 || length != 1 {
		t.Fatalf("actual imported audio: count=%d length=%d, %v", count, length, err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	undo, err := history.Undo(t.Context(), lib, items[0].HistoryID, history.UndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err = Open(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.ReconcileImports(t.Context()); err != nil {
		t.Fatal(err)
	}
	again, err := restarted.Enqueue(t.Context(), source)
	if err != nil || again.ID != queued.ID || again.Status != "cancelled" || again.HistoryID == "" {
		t.Fatalf("undone version requeued: %+v, %v", again, err)
	}
	if item, err := restarted.PrepareNext(t.Context(), 0); err != nil || item != nil {
		t.Fatalf("undone version resumed: %+v, %v", item, err)
	}
	if item, err := restarted.Retry(t.Context(), queued.ID); item != nil || progress.ErrorCode(err) != "intake_state_changed" {
		t.Fatalf("committed version receipt reset: %+v, %v", item, err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM Track`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("Undo did not remove import: %d, %v", count, err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Undo(t.Context(), lib, undo.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	restarted, err = Open(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.ReconcileImports(t.Context()); err != nil {
		t.Fatal(err)
	}
	restored, err := restarted.Enqueue(t.Context(), source)
	if err != nil || restored.ID != queued.ID || restored.Status != "imported" || restored.HistoryID != items[0].HistoryID {
		t.Fatalf("restored import misreported or duplicated: %+v, %v", restored, err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM Track`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Undo-of-Undo did not restore import: %d, %v", count, err)
	}
	if item, err := restarted.PrepareNext(t.Context(), 0); err != nil || item != nil {
		t.Fatalf("restored version resumed: %+v, %v", item, err)
	}
	if data, err := os.ReadFile(source); err != nil || len(data) != 96044 {
		t.Fatalf("original audio lost: %d, %v", len(data), err)
	}
}

func TestImportRefusesUnreviewedOrOccupiedDestination(t *testing.T) {
	for _, change := range []string{"collection", "staged bytes", "existing destination", "outside history"} {
		t.Run(change, func(t *testing.T) {
			s, lib := queueFixture(t)
			d := importCollectionFixture(t, lib)
			if _, err := s.Enqueue(t.Context(), writeImportWAV(t)); err != nil {
				t.Fatal(err)
			}
			item, err := s.PrepareNext(t.Context(), 0)
			if err != nil || item == nil {
				t.Fatalf("prepare: %+v, %v", item, err)
			}
			opts := ImportOptions{ID: item.ID}
			review, err := s.ReviewImport(t.Context(), d, opts)
			if err != nil {
				t.Fatal(err)
			}
			var occupied string
			wantCode := "staging_import_stale"
			switch change {
			case "collection":
				if _, err := d.Exec(`INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Later',0,0)`); err != nil {
					t.Fatal(err)
				}
			case "staged bytes":
				path, err := staging.New(lib).Resolve(t.Context(), item.StageID)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("different audio"), 0600); err != nil {
					t.Fatal(err)
				}
				wantCode = "intake_staged_changed"
			case "existing destination":
				occupied = filepath.Join(lib, filepath.FromSlash(review.Items[0].Destination))
				if err := os.MkdirAll(filepath.Dir(occupied), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(occupied, []byte("unrelated existing media"), 0600); err != nil {
					t.Fatal(err)
				}
				// Even an explicitly reviewed occupied path is not a commit receipt.
				review, err = s.ReviewImport(t.Context(), d, opts)
				if err != nil {
					t.Fatal(err)
				}
				wantCode = "intake_destination_exists"
			}
			opts.PlanToken = review.PlanToken
			var receipt *ImportReceipt
			if change == "outside history" {
				receipt, err = s.ApplyImport(t.Context(), d, opts)
				if err == nil || receipt != nil {
					t.Fatalf("unowned import accepted: %+v, %v", receipt, err)
				}
			} else {
				_, err = history.Run(t.Context(), lib, "intake.import", history.ScopeCollection, nil, func(ctx context.Context) error {
					receipt, err = s.ApplyImport(ctx, d, opts)
					return err
				})
				if receipt != nil || progress.ErrorCode(err) != wantCode {
					t.Fatalf("changed import accepted: %+v, %v", receipt, err)
				}
			}
			var count int
			if err := d.QueryRow(`SELECT count(*) FROM Track`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("refusal changed collection: %d, %v", count, err)
			}
			if err := s.database.QueryRow(`SELECT count(*) FROM ImportIntent`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("preparation refusal left an importing intent: %d, %v", count, err)
			}
			if occupied != "" {
				data, err := os.ReadFile(occupied)
				if err != nil || !bytes.Equal(data, []byte("unrelated existing media")) {
					t.Fatalf("existing destination overwritten: %q, %v", data, err)
				}
			}
		})
	}
}

func TestImportRequiresMatchingCollectionOwner(t *testing.T) {
	for _, owner := range []struct {
		name      string
		operation string
		scope     history.Scope
	}{
		{"file operation", "intake.import", history.ScopeFiles},
		{"unrelated collection operation", "track.update", history.ScopeCollection},
	} {
		t.Run(owner.name, func(t *testing.T) {
			s, lib := queueFixture(t)
			d := importCollectionFixture(t, lib)
			if _, err := s.Enqueue(t.Context(), writeImportWAV(t)); err != nil {
				t.Fatal(err)
			}
			item, err := s.PrepareNext(t.Context(), 0)
			if err != nil || item == nil {
				t.Fatalf("prepare: %+v, %v", item, err)
			}
			opts := ImportOptions{ID: item.ID}
			review, err := s.ReviewImport(t.Context(), d, opts)
			if err != nil {
				t.Fatal(err)
			}
			opts.PlanToken = review.PlanToken
			_, err = history.Run(t.Context(), lib, owner.operation, owner.scope, nil, func(ctx context.Context) error {
				receipt, err := s.ApplyImport(ctx, d, opts)
				if receipt != nil || err == nil {
					t.Fatalf("wrong history owner authorized collection import: %+v, %v", receipt, err)
				}
				return err
			})
			if err == nil {
				t.Fatal("wrong history owner succeeded")
			}
			var count int
			if err := d.QueryRow(`SELECT count(*) FROM Track`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("wrong owner changed collection: %d, %v", count, err)
			}
			if err := s.database.QueryRow(`SELECT count(*) FROM ImportIntent`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("wrong owner created import intent: %d, %v", count, err)
			}
		})
	}
}

func TestImportReceiptFailureRequiresRecovery(t *testing.T) {
	s, lib := queueFixture(t)
	d := importCollectionFixture(t, lib)
	if _, err := s.Enqueue(t.Context(), writeImportWAV(t)); err != nil {
		t.Fatal(err)
	}
	item, err := s.PrepareNext(t.Context(), 0)
	if err != nil || item == nil {
		t.Fatalf("prepare: %+v, %v", item, err)
	}
	opts := ImportOptions{ID: item.ID}
	review, err := s.ReviewImport(t.Context(), d, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.PlanToken = review.PlanToken
	if _, err := s.database.Exec(`CREATE TRIGGER refuse_receipt BEFORE UPDATE OF track_id ON ImportIntent BEGIN SELECT RAISE(ABORT,'receipt unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	entry, err := history.Run(t.Context(), lib, "intake.import", history.ScopeCollection, nil, func(ctx context.Context) error {
		receipt, err := s.ApplyImport(ctx, d, opts)
		if receipt != nil {
			t.Errorf("failed receipt returned success: %+v", receipt)
		}
		return err
	})
	if progress.ErrorCode(err) != "history_recovery_required" || entry == nil || entry.State != "pending" {
		t.Fatalf("cross-file failure lost pending history: %+v, %v", entry, err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM Track`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("native committed track missing: %d, %v", count, err)
	}
	if err := s.ReconcileImports(t.Context()); progress.ErrorCode(err) != "intake_import_recovery_required" {
		t.Fatalf("incomplete receipt treated as committed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Undo(t.Context(), lib, entry.ID, history.UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReconcileImports(t.Context()); err != nil {
		t.Fatal(err)
	}
	items, err := s.List(t.Context())
	if err != nil || len(items) != 1 || items[0].Status != "cancelled" || items[0].HistoryID != entry.ID {
		t.Fatalf("recovered version resumed: %+v, %v", items, err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM Track`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("recovery left imported track: %d, %v", count, err)
	}
}

func importCollectionFixture(t *testing.T, lib string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(lib, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.InitSchema(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	return d
}

func writeImportWAV(t *testing.T) string {
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
