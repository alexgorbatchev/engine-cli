package performance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
)

func fixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	if _, err := db.CreateLibrary(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := d.Exec("INSERT INTO Track(id,path,title) VALUES(1,'song.wav','Original')"); err != nil {
		t.Fatal(err)
	}
	return d, root
}

func code(err error) string {
	var coded *progress.Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

func inspect(t *testing.T, d *sql.DB, root string) *Saved {
	t.Helper()
	result, err := Inspect(context.Background(), d, root, "1")
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func prepare(t *testing.T, d *sql.DB, root string, edit Edit) *Prepared {
	t.Helper()
	edit.Identifier = "1"
	edit.Revision = inspect(t, d, root).Revision
	p, err := Prepare(context.Background(), d, root, edit)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func nativeExtentBlob(t *testing.T) []byte {
	t.Helper()
	data := binary.BigEndian.AppendUint64(nil, math.Float64bits(48000))
	data = binary.BigEndian.AppendUint64(data, 480000)
	data = append(data, 1, 2, 3, 4)
	blob, err := engine.QCompress(data)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func TestSavedCueLifecyclePreservesNativeColumns(t *testing.T) {
	d, root := fixture(t)
	track := nativeExtentBlob(t)
	if _, err := d.Exec("UPDATE PerformanceData SET trackData=?,overviewWaveFormData=x'1234',thirdPartySourceId=7,activeOnLoadLoops=3 WHERE trackId=1", track); err != nil {
		t.Fatal(err)
	}
	var previousRevision string
	for slot := 1; slot <= 8; slot++ {
		position := float64(slot) + 0.000123
		p := prepare(t, d, root, Edit{Slot: slot, Position: position, Name: "Cue " + string(rune('A'+slot-1)), Color: "#A1B2C3"})
		result, err := p.Apply(context.Background(), d)
		if err != nil || !result.Changed || result.TrackID != 1 || result.Slot != slot || result.Revision == previousRevision {
			t.Fatalf("set result=%+v err=%v", result, err)
		}
		saved := inspect(t, d, root)
		// Native samples multiply and divide float64 seconds by the sample rate.
		ulp := math.Nextafter(position, math.Inf(1)) - position
		if saved.Revision != result.Revision || len(saved.Cues) != slot || math.Abs(saved.Cues[slot-1].Position-position) > ulp {
			t.Fatalf("saved=%+v result=%+v", saved, result)
		}
		previousRevision = result.Revision
	}
	before, err := load(context.Background(), d, root, 1)
	if err != nil {
		t.Fatal(err)
	}
	p := prepare(t, d, root, Edit{Slot: 4, Clear: true})
	result, err := p.Apply(context.Background(), d)
	if err != nil || !result.Changed {
		t.Fatalf("clear=%+v err=%v", result, err)
	}
	after, err := load(context.Background(), d, root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Beat, after.Beat) || !bytes.Equal(before.Track, after.Track) || !bytes.Equal(before.Loops, after.Loops) {
		t.Fatal("clear rewrote unrelated performance fields")
	}
	var waveform []byte
	var source, active int
	if err := d.QueryRow("SELECT overviewWaveFormData,thirdPartySourceId,activeOnLoadLoops FROM PerformanceData WHERE trackId=1").Scan(&waveform, &source, &active); err != nil || !bytes.Equal(waveform, []byte{0x12, 0x34}) || source != 7 || active != 3 {
		t.Fatalf("unrelated fields changed: %x %d %d %v", waveform, source, active, err)
	}
	saved := inspect(t, d, root)
	if len(saved.Cues) != 7 || saved.Cues[3].Slot != 5 {
		t.Fatalf("wrong cue cleared: %+v", saved.Cues)
	}
	result, err = prepare(t, d, root, Edit{Slot: 4, Clear: true}).Apply(context.Background(), d)
	if err != nil || result.Changed || result.Revision != saved.Revision {
		t.Fatalf("second clear=%+v err=%v", result, err)
	}
}

func TestRevisionRejectsChangedNativeInterpretationBeforeAndInsideTransaction(t *testing.T) {
	for _, statement := range []string{
		"UPDATE PerformanceData SET trackData=x'01' WHERE trackId=1",
		"UPDATE PerformanceData SET beatData=x'01' WHERE trackId=1",
		"UPDATE PerformanceData SET loops=x'01' WHERE trackId=1",
		"UPDATE PerformanceData SET quickCues=x'01' WHERE trackId=1",
		"UPDATE Track SET originDatabaseUuid='replacement' WHERE id=1",
		"UPDATE Track SET originTrackId=2 WHERE id=1",
		"UPDATE Track SET path='replacement.wav' WHERE id=1",
		"DELETE FROM PerformanceData WHERE trackId=1",
		"DROP TABLE PerformanceData",
	} {
		t.Run(statement, func(t *testing.T) {
			d, root := fixture(t)
			if _, err := d.Exec("UPDATE PerformanceData SET trackData=? WHERE trackId=1", nativeExtentBlob(t)); err != nil {
				t.Fatal(err)
			}
			edit := Edit{Identifier: "1", Slot: 1, Position: 1.25, Name: "Start", Color: "#102030", Revision: inspect(t, d, root).Revision}
			p, err := Prepare(context.Background(), d, root, edit)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(context.Background(), d, root, edit); code(err) != "cue_revision_stale" {
				t.Fatalf("preflight=%v", err)
			}
			if _, err := p.Apply(context.Background(), d); code(err) != "cue_revision_stale" {
				t.Fatalf("transaction=%v", err)
			}
		})
	}
}

func TestCueValidationAndCancellationLeaveNativeStateUnchanged(t *testing.T) {
	for _, change := range []string{"slot low", "slot high", "missing revision", "negative", "beyond extent", "NaN", "infinite", "name too long", "invalid color", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			d, root := fixture(t)
			if _, err := d.Exec("UPDATE PerformanceData SET trackData=? WHERE trackId=1", nativeExtentBlob(t)); err != nil {
				t.Fatal(err)
			}
			before := inspect(t, d, root)
			edit := Edit{Identifier: "1", Slot: 1, Position: 1, Name: "Reviewed", Color: "#ABCDEF", Revision: before.Revision}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch change {
			case "slot low":
				edit.Slot = 0
			case "slot high":
				edit.Slot = 9
			case "missing revision":
				edit.Revision = ""
			case "negative":
				edit.Position = -1
			case "beyond extent":
				edit.Position = 10.001
			case "NaN":
				edit.Position = math.NaN()
			case "infinite":
				edit.Position = math.Inf(1)
			case "name too long":
				edit.Name = string(make([]byte, 256))
			case "invalid color":
				edit.Color = "red"
			case "cancelled":
				cancel()
			}
			_, err := Prepare(ctx, d, root, edit)
			if change == "cancelled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost identity: %v", err)
				}
			} else if code(err) != "cue_invalid" {
				t.Fatalf("invalid cue accepted or misclassified: %v", err)
			}
			if after := inspect(t, d, root); after.Revision != before.Revision || len(after.Cues) != 0 {
				t.Fatalf("rejected edit changed performance: %+v", after)
			}
		})
	}
}

func TestRejectedNativeWriteRollsBackBeatInitialization(t *testing.T) {
	d, root := fixture(t)
	if _, err := d.Exec("UPDATE PerformanceData SET trackData=? WHERE trackId=1", nativeExtentBlob(t)); err != nil {
		t.Fatal(err)
	}
	p := prepare(t, d, root, Edit{Slot: 8, Position: 9.875, Name: "End", Color: "#ABCDEF"})
	if _, err := d.Exec("CREATE TRIGGER reject_cue BEFORE UPDATE ON PerformanceData BEGIN SELECT RAISE(ABORT,'injected native write failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(context.Background(), d); err == nil {
		t.Fatal("accepted failed write")
	}
	current, err := load(context.Background(), d, root, 1)
	if err != nil || len(current.Cues) != 0 || len(current.Beat) != 0 || !bytes.Equal(current.Track, nativeExtentBlob(t)) {
		t.Fatalf("partial native write: %+v %v", current, err)
	}
}

func TestOfflineReadAndClearKeepAvailabilityInRevision(t *testing.T) {
	d, root := fixture(t)
	before := inspect(t, d, root)
	result, err := prepare(t, d, root, Edit{Slot: 1, Clear: true}).Apply(context.Background(), d)
	if err != nil || result.Changed {
		t.Fatalf("offline clear=%+v %v", result, err)
	}
	if _, err := Prepare(context.Background(), d, root, Edit{Identifier: "1", Slot: 1, Position: 0, Name: "Start", Color: "#FFFFFF", Revision: before.Revision}); code(err) != "cue_media_unavailable" {
		t.Fatalf("offline set=%v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "song.wav"), []byte("new media"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(context.Background(), d, root, Edit{Identifier: "1", Slot: 1, Clear: true, Revision: before.Revision}); code(err) != "cue_revision_stale" {
		t.Fatalf("media appeared but old review accepted: %v", err)
	}
}

func TestNativeReadFailuresRejectPreparedCueWrites(t *testing.T) {
	for _, tt := range []struct {
		name, statement, code string
	}{
		{"removed track", "DELETE FROM Track WHERE id=1", "track_not_found"},
		{"invalid origin", "UPDATE Track SET originTrackId=-1 WHERE id=1", "cue_track_identity_invalid"},
		{"damaged schema", "ALTER TABLE PerformanceData RENAME COLUMN loops TO brokenLoops", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, root := fixture(t)
			p := prepare(t, d, root, Edit{Slot: 1, Clear: true})
			if _, err := d.Exec(tt.statement); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Apply(t.Context(), d); err == nil || code(err) != tt.code {
				t.Fatalf("prepared write accepted native read failure: %v", err)
			}
			if _, err := Inspect(t.Context(), d, root, "1"); err == nil || code(err) != tt.code {
				t.Fatalf("inspection accepted native read failure: %v", err)
			}
			var changed int
			if err := d.QueryRow("SELECT COUNT(*) FROM PerformanceData WHERE length(quickCues)>0 OR length(beatData)>0").Scan(&changed); err != nil || changed != 0 {
				t.Fatalf("failed write published native bytes: count=%d err=%v", changed, err)
			}
		})
	}
}

func TestUnavailableNativePerformanceRejectsEditing(t *testing.T) {
	for _, tt := range []struct {
		name, statement, code string
	}{
		{"missing table", "DROP TABLE PerformanceData", "saved_performance_unavailable"},
		{"damaged cues", "UPDATE PerformanceData SET quickCues=x'01' WHERE trackId=1", "invalid_saved_performance"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, root := fixture(t)
			if _, err := d.Exec(tt.statement); err != nil {
				t.Fatal(err)
			}
			s, err := load(t.Context(), d, root, 1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(t.Context(), d, root, Edit{Identifier: "1", Slot: 1, Clear: true, Revision: s.revision()}); code(err) != tt.code {
				t.Fatalf("unavailable performance accepted: %v", err)
			}
			after, err := load(t.Context(), d, root, 1)
			if err != nil || after.revision() != s.revision() {
				t.Fatalf("rejected edit changed native storage: %+v %v", after, err)
			}
		})
	}
}

func TestMissingTrackAndCancelledApplyDoNotWrite(t *testing.T) {
	d, root := fixture(t)
	if _, err := Inspect(t.Context(), d, root, "999"); code(err) != "track_not_found" {
		t.Fatalf("missing selector accepted: %v", err)
	}
	p := prepare(t, d, root, Edit{Slot: 1, Clear: true})
	before := inspect(t, d, root)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Apply(ctx, d); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled apply lost cancellation: %v", err)
	}
	if after := inspect(t, d, root); after.Revision != before.Revision {
		t.Fatal("cancelled apply changed native storage")
	}
}
