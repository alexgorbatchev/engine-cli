package beatgrid

import (
	"bytes"
	"compress/zlib"
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/nateranda/djtools/engine"
)

// nativeLibrary creates a library with Engine's native schema, whose trigger gives each track a PerformanceData row.
func nativeLibrary(t *testing.T) *sql.DB {
	t.Helper()
	root := t.TempDir()
	if _, err := db.CreateLibrary(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.Exec("INSERT INTO Track(id, path, filename, length, bpm, bpmAnalyzed) VALUES(1, 'a.mp3', 'a.mp3', 10, 90, 90)"); err != nil {
		t.Fatal(err)
	}
	return d
}

func inTx(t *testing.T, d *sql.DB, run func(*sql.Tx) error) error {
	t.Helper()
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := run(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func TestSaveAndUpdateKeepTheDefaultGridAndTrackBPM(t *testing.T) {
	d := nativeLibrary(t)
	ctx := context.Background()
	if data, err := Load(ctx, d, 1); err != nil || data != nil {
		t.Fatalf("unanalyzed track: %v %v", data, err)
	}
	if err := inTx(t, d, func(tx *sql.Tx) error { return Update(ctx, tx, 1, steady().Adjusted) }); code(err) != "beatgrid_missing" {
		t.Fatalf("editing without a grid: %v", err)
	}
	analyzed, err := engine.NewBeatData(rate, 11*rate, steady().Adjusted)
	if err != nil {
		t.Fatal(err)
	}
	if err := inTx(t, d, func(tx *sql.Tx) error { return Save(ctx, tx, 1, analyzed) }); err != nil {
		t.Fatal(err)
	}
	// Saving a grid alone leaves the track's tempo for callers that chose to analyze it.
	var bpm int64
	var precise float64
	if err := d.QueryRow("SELECT bpm, bpmAnalyzed FROM Track WHERE id=1").Scan(&bpm, &precise); err != nil || bpm != 90 || precise != 90 {
		t.Fatalf("bpm=%d precise=%v err=%v", bpm, precise, err)
	}

	edited, err := Apply(SourceOf(analyzed), nil, Edit{Kind: EditTempo, BPM: 124.5})
	if err != nil {
		t.Fatal(err)
	}
	if err := inTx(t, d, func(tx *sql.Tx) error { return Update(ctx, tx, 1, edited.Anchors) }); err != nil {
		t.Fatal(err)
	}
	saved, err := Load(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Default[0] != analyzed.Default[0] || saved.Adjusted[1] != edited.Anchors[1] || saved.SampleLimit != analyzed.SampleLimit || saved.Source != analyzed.Source {
		t.Fatalf("saved=%+v", saved)
	}
	if err := d.QueryRow("SELECT bpm, bpmAnalyzed FROM Track WHERE id=1").Scan(&bpm, &precise); err != nil || bpm != 125 || math.Abs(precise-edited.BPM) > 1e-9 {
		t.Fatalf("bpm=%d precise=%v err=%v", bpm, precise, err)
	}

	bad := []engine.BeatAnchor{{Sample: 0, BeatsUntilNext: 1}, {Sample: 100, Beat: 1}}
	if err := inTx(t, d, func(tx *sql.Tx) error { return Update(ctx, tx, 1, bad) }); code(err) != "beatgrid_tempo_range" {
		t.Fatalf("invalid edit: %v", err)
	}
}

func TestUpdateRepairsAnUnusableDefaultGrid(t *testing.T) {
	d := nativeLibrary(t)
	ctx := context.Background()
	// A single-anchor default grid leaves Engine's reset with its built-in fallback.
	unusable := engine.BeatData{SampleRate: rate, SampleLimit: 11 * rate, Default: steady().Adjusted[:1], Adjusted: steady().Adjusted}
	if _, err := d.Exec("UPDATE PerformanceData SET beatData=? WHERE trackId=1", rawBlob(t, unusable)); err != nil {
		t.Fatal(err)
	}
	if err := inTx(t, d, func(tx *sql.Tx) error { return Update(ctx, tx, 1, steady().Adjusted) }); err != nil {
		t.Fatal(err)
	}
	saved, err := Load(ctx, d, 1)
	if err != nil || len(saved.Default) != 2 || !saved.IsSet {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
}

func TestLoadReportsUnreadableGridsAndMissingRowsAreInserted(t *testing.T) {
	d := nativeLibrary(t)
	ctx := context.Background()
	if _, err := d.Exec("UPDATE PerformanceData SET beatData=X'0000000501' WHERE trackId=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ctx, d, 1); code(err) != "invalid_beatgrid" {
		t.Fatalf("err=%v", err)
	}
	if _, err := d.Exec("DELETE FROM PerformanceData"); err != nil {
		t.Fatal(err)
	}
	analyzed, _ := engine.NewBeatData(rate, 11*rate, steady().Adjusted)
	if err := inTx(t, d, func(tx *sql.Tx) error { return Save(ctx, tx, 1, analyzed) }); err != nil {
		t.Fatal(err)
	}
	if data, err := Load(ctx, d, 1); err != nil || data == nil {
		t.Fatalf("data=%v err=%v", data, err)
	}
	if err := inTx(t, d, func(tx *sql.Tx) error { return Save(ctx, tx, 1, engine.BeatData{}) }); code(err) != "beatgrid_invalid" {
		t.Fatalf("err=%v", err)
	}
	if _, err := d.Exec("DROP TABLE PerformanceData"); err != nil {
		t.Fatal(err)
	}
	if data, err := Load(ctx, d, 1); err != nil || data != nil {
		t.Fatalf("data=%v err=%v", data, err)
	}
}

// rawBlob serializes beat data without validation, to store grids Engine CLI itself never writes.
func rawBlob(t *testing.T, data engine.BeatData) []byte {
	t.Helper()
	payload := binary.BigEndian.AppendUint64(nil, math.Float64bits(data.SampleRate))
	payload = binary.BigEndian.AppendUint64(payload, math.Float64bits(data.SampleLimit))
	payload = append(payload, 0)
	for _, anchors := range [][]engine.BeatAnchor{data.Default, data.Adjusted} {
		payload = binary.BigEndian.AppendUint64(payload, uint64(len(anchors)))
		for _, a := range anchors {
			payload = binary.LittleEndian.AppendUint64(payload, math.Float64bits(a.Sample))
			payload = binary.LittleEndian.AppendUint64(payload, uint64(a.Beat))
			payload = binary.LittleEndian.AppendUint32(payload, a.BeatsUntilNext)
			payload = binary.LittleEndian.AppendUint32(payload, 0)
		}
	}
	var out bytes.Buffer
	out.Write(binary.BigEndian.AppendUint32(nil, uint32(len(payload))))
	w := zlib.NewWriter(&out)
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestUpdateRefusesLockedGridsAndGridsEndingEarly(t *testing.T) {
	d := nativeLibrary(t)
	ctx := context.Background()
	analyzed, _ := engine.NewBeatData(rate, 11*rate, steady().Adjusted)
	if err := inTx(t, d, func(tx *sql.Tx) error { return Save(ctx, tx, 1, analyzed) }); err != nil {
		t.Fatal(err)
	}
	short := []engine.BeatAnchor{{Sample: 0, Beat: 0, BeatsUntilNext: 8}, {Sample: 8 * 22050, Beat: 8}}
	if err := inTx(t, d, func(tx *sql.Tx) error { return Update(ctx, tx, 1, short) }); code(err) != "beatgrid_invalid" {
		t.Fatalf("short grid: %v", err)
	}
	if _, err := d.Exec("UPDATE Track SET isBeatGridLocked=1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if locked, err := Locked(ctx, d, 1); err != nil || !locked {
		t.Fatalf("locked=%v err=%v", locked, err)
	}
	if err := inTx(t, d, func(tx *sql.Tx) error { return Update(ctx, tx, 1, steady().Adjusted) }); code(err) != "beatgrid_locked" {
		t.Fatalf("locked grid: %v", err)
	}
	if _, err := Locked(ctx, d, 99); err == nil {
		t.Fatal("missing track reported a lock state")
	}
}
