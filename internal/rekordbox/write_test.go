package rekordbox

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/nateranda/djtools/engine"
	"github.com/nateranda/djtools/lib"
	"github.com/nateranda/djtools/rbxml"
)

func TestPrepareTrackRejectsInvalidInputsWithoutPartialNativeData(t *testing.T) {
	for name, song := range map[string]lib.Song{
		"cue beyond observed audio":  {Cues: []lib.HotCue{{Position: 1, Offset: 20}}},
		"loop beyond observed audio": {Loops: []lib.Loop{{Position: 1, Start: 1, End: 20}}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := prepareTrack(rbxml.Track{Name: "Do not partially prepare"}, song, audioExtent{rate: 48000, frames: 480000})
			if err == nil || got.metadata.Title.Valid || got.media.beat != nil || got.added != nil {
				t.Fatalf("invalid performance retained partial metadata: %+v, %v", got, err)
			}
		})
	}
	if got, err := prepareTrack(rbxml.Track{Name: "Empty audio"}, lib.Song{}, audioExtent{}); err == nil || got.metadata.Title.Valid || got.media.beat != nil {
		t.Fatalf("invalid audio retained partial preparation: %+v, %v", got, err)
	}
}

func TestInsertPreparedWritesNativeMetadataDatesAndPerformanceTogether(t *testing.T) {
	ctx := context.Background()
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Name="Imported" DateAdded="1970-01-01" LastPlayed="2026-10-30" Location="file:///music/a.wav"><TEMPO Inizio="0" Bpm="120" Metro="4/4" Battito="1"/><POSITION_MARK Type="0" Start="1.125" Num="7"/></TRACK></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareTrack(src.xml.Collection.Tracks[0], src.library.Songs[0], audioExtent{rate: 48000, frames: 480000, bytes: 960000})
	if err != nil {
		t.Fatal(err)
	}
	for name, trigger := range map[string]string{
		"commit":                         "",
		"performance failure rolls back": `CREATE TRIGGER reject_import BEFORE UPDATE ON PerformanceData BEGIN SELECT RAISE(ABORT,'fixture performance failure'); END`,
		"missing native performance row rolls back": `DROP TRIGGER trigger_after_insert_Track_insert_performance_data`,
		"date failure rolls back":                   `CREATE TRIGGER reject_date BEFORE UPDATE OF dateAdded ON Track BEGIN SELECT RAISE(ABORT,'fixture date failure'); END`,
	} {
		t.Run(name, func(t *testing.T) {
			d := importDatabase(t)
			if trigger != "" {
				if _, err := d.ExecContext(ctx, trigger); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := d.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			id, err := insertPrepared(ctx, tx, prepared)
			if trigger != "" {
				if err == nil || id != 0 {
					t.Fatalf("failed performance produced a successful track: %d, %v", id, err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM Track").Scan(&count); err != nil || count != 0 {
					t.Fatalf("partial metadata survived rollback: %d, %v", count, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			verifyImportedTrack(t, d, id)
		})
	}
}

func verifyImportedTrack(t *testing.T, d *sql.DB, id int64) {
	t.Helper()
	var added time.Time
	var played sql.NullString
	var origin string
	var originID int64
	if err := d.QueryRow("SELECT dateAdded,timeLastPlayed,originDatabaseUuid,originTrackId FROM Track WHERE id=?", id).Scan(&added, &played, &origin, &originID); err != nil {
		t.Fatal(err)
	}
	if !added.Equal(time.Unix(0, 0)) || played.Valid || origin == "" || originID != id {
		t.Fatalf("native date/origin or invented history: %v, %v, %s/%d", added, played, origin, originID)
	}
	var beat, cues, loops []byte
	if err := d.QueryRow("SELECT beatData,quickCues,loops FROM PerformanceData WHERE trackId=?", id).Scan(&beat, &cues, &loops); err != nil {
		t.Fatal(err)
	}
	saved, err := engine.DecodeSavedPerformance(beat, cues, loops)
	if err != nil || len(saved.HotCues) != 1 || saved.HotCues[0].Offset != 1.125 || saved.HotCues[0].Position != 8 {
		t.Fatalf("native performance was not written with metadata: %+v, %v", saved, err)
	}
}

func importDatabase(t *testing.T) *sql.DB {
	t.Helper()
	root := t.TempDir()
	if _, err := db.CreateLibrary(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}
