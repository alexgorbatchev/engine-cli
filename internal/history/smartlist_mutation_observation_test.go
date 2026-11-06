package history

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSmartlistObservationRefusesBeforeRepairOrPreparation(t *testing.T) {
	lib, database := smartlistHistoryLibrary(t)
	recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	refused := errors.New("review refused")
	prepared, ran := false, false
	entry, err := RunPrepared(t.Context(), lib, RunOptions{
		Operation: "library.sync", Scope: ScopeCollection, RepairSmartlists: true,
		ObserveSmartlists: func(ctx context.Context, conn *sql.Tx, projection *SmartlistProjection) (bool, error) {
			if len(projection.Actions) != 1 || projection.Rows[0].Values["parentPlaylistPath"] != "Renamed;" {
				t.Fatalf("unrepaired projection: %+v", projection)
			}
			var raw string
			if err := conn.QueryRowContext(ctx, `SELECT parentPlaylistPath FROM Smartlist`).Scan(&raw); err != nil || raw != "House;" {
				t.Fatalf("observation changed native rows: %q %v", raw, err)
			}
			return false, refused
		},
		Prepare: func(context.Context) error { prepared = true; return nil },
	}, func(context.Context) error { ran = true; return nil })
	if !errors.Is(err, refused) || entry != nil || prepared || ran {
		t.Fatalf("rejection escaped preflight: entry=%v err=%v prepared=%t ran=%t", entry, err, prepared, ran)
	}
	var raw string
	if err := database.QueryRow(`SELECT parentPlaylistPath FROM Smartlist`).Scan(&raw); err != nil || raw != "House;" {
		t.Fatalf("rejected observation published repair: %q %v", raw, err)
	}
	if entries, err := List(lib); err != nil || len(entries) != 1 {
		t.Fatalf("rejection created history: %+v %v", entries, err)
	}
}

func TestSmartlistObservationBindsRepairEvidence(t *testing.T) {
	lib, database := smartlistHistoryLibrary(t)
	previous := recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	prepared := false
	entry, err := RunPrepared(t.Context(), lib, RunOptions{
		Operation: "library.sync", Scope: ScopeCollection, RepairSmartlists: true,
		ObserveSmartlists: func(context.Context, *sql.Tx, *SmartlistProjection) (bool, error) {
			path := filepath.Join(lib, ".engine-cli", "history", previous.ID+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				return false, err
			}
			// Simulate a noncooperating evidence editor after plan acceptance.
			return true, os.WriteFile(path, append(data, '\n'), 0600)
		},
		Prepare: func(context.Context) error { prepared = true; return nil },
	}, func(context.Context) error { t.Error("stale operation ran"); return nil })
	if err == nil || entry != nil || prepared {
		t.Fatalf("changed accepted evidence passed: entry=%v err=%v prepared=%t", entry, err, prepared)
	}
	var path string
	if err := database.QueryRow(`SELECT parentPlaylistPath FROM Smartlist`).Scan(&path); err != nil || path != "House;" {
		t.Fatalf("stale repair changed source: %q %v", path, err)
	}
}

func TestSmartlistObservationReservesNativeGeneration(t *testing.T) {
	lib, database := smartlistHistoryLibrary(t)
	writer, err := database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.ExecContext(t.Context(), "PRAGMA busy_timeout=0"); err != nil {
		t.Fatal(err)
	}
	refused := errors.New("observation finished")
	_, err = RunPrepared(t.Context(), lib, RunOptions{
		Operation: "library.sync", Scope: ScopeCollection, RepairSmartlists: true,
		ObserveSmartlists: func(ctx context.Context, _ *sql.Tx, _ *SmartlistProjection) (bool, error) {
			if _, err := writer.ExecContext(ctx, `UPDATE Playlist SET title='Concurrent' WHERE id=1`); err == nil {
				t.Fatal("native writer entered the reserved observation")
			}
			return false, refused
		},
	}, func(context.Context) error { t.Error("refused operation ran"); return nil })
	if !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(t.Context(), `UPDATE Playlist SET title='After' WHERE id=1`); err != nil {
		t.Fatalf("observation leaked native reservation: %v", err)
	}
}

func TestSmartlistObservationCanFinishWithoutRepairOrPreparation(t *testing.T) {
	lib, database := smartlistHistoryLibrary(t)
	recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	entry, err := RunPrepared(t.Context(), lib, RunOptions{
		Operation: "library.sync", Scope: ScopeCollection, RepairSmartlists: true,
		ObserveSmartlists: func(context.Context, *sql.Tx, *SmartlistProjection) (bool, error) {
			return false, nil
		},
		Prepare: func(context.Context) error { t.Error("finished observation prepared mutation"); return nil },
	}, func(context.Context) error { t.Error("finished observation ran mutation"); return nil })
	if err != nil || entry != nil {
		t.Fatalf("finished observation: entry=%v err=%v", entry, err)
	}
	var raw string
	if err := database.QueryRow(`SELECT parentPlaylistPath FROM Smartlist`).Scan(&raw); err != nil || raw != "House;" {
		t.Fatalf("finished observation published repair: %q %v", raw, err)
	}
	if entries, err := List(lib); err != nil || len(entries) != 1 {
		t.Fatalf("finished observation created history: %+v %v", entries, err)
	}
	if _, err := database.Exec(`UPDATE Playlist SET title='After' WHERE id=1`); err != nil {
		t.Fatalf("finished observation retained reservation: %v", err)
	}
}

func TestSmartlistObservationCancellationReleasesNativeReservation(t *testing.T) {
	lib, database := smartlistHistoryLibrary(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entry, err := RunPrepared(ctx, lib, RunOptions{
		Operation: "library.sync", Scope: ScopeCollection, RepairSmartlists: true,
		ObserveSmartlists: func(ctx context.Context, tx *sql.Tx, _ *SmartlistProjection) (bool, error) {
			var count int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM Smartlist").Scan(&count); err != nil {
				return false, err
			}
			cancel()
			return false, ctx.Err()
		},
		Prepare: func(context.Context) error { t.Error("cancelled observation prepared mutation"); return nil },
	}, func(context.Context) error { t.Error("cancelled observation ran mutation"); return nil })
	if !errors.Is(err, context.Canceled) || entry != nil {
		t.Fatalf("cancelled observation: entry=%v error=%v", entry, err)
	}
	if _, err := database.Exec(`UPDATE Playlist SET title='After cancellation' WHERE id=1`); err != nil {
		t.Fatalf("cancelled observation retained native writer: %v", err)
	}
}
