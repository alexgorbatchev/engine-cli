package playhistory

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/db/dbtest"
	"github.com/alexgorbatchev/engine-cli/internal/playhistory/historytest"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const libraryUUID = "library-uuid"

// fixture is a library with three tracks and a history database whose copies
// resolve by path (1), by origin identity after a move (2), or not at all.
func fixture(t *testing.T, history string) (string, *sql.DB) {
	t.Helper()
	root := dbtest.Library(t, libraryUUID)
	library, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { library.Close() })
	if _, err := library.Exec(`INSERT INTO Track(id,path,title,artist,length) VALUES
		(1,'../Music/one.mp3','One','A',200),(2,'../Music/moved/two.mp3','Two','B',300),(3,'../Music/three.mp3','Three','C',400)`); err != nil {
		t.Fatal(err)
	}
	writeHistory(t, root, historytest.Schema+`
		INSERT INTO Track(id,path,title,artist,length,originDatabaseUuid,originTrackId) VALUES
		(10,'../Music/one.mp3','One','A',200,'`+libraryUUID+`',1),
		(11,'../Music/two.mp3','Two','B',300,'`+libraryUUID+`',2),
		(12,'../Music/gone.mp3','Gone','D',100,'`+libraryUUID+`',99);`+history)
	return root, library
}

func writeHistory(t *testing.T, root, statements string) {
	t.Helper()
	historytest.Write(t, root, statements)
}

func code(err error) string {
	var coded *progress.Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

func TestListFollowsEngineHistoryOrderTitlesAndVisibility(t *testing.T) {
	root, _ := fixture(t, `INSERT INTO Historylist(id,title,startTime,timezone,originDriveName,isDeleted) VALUES
		(1,'Warm-up',1700000000,'Europe/Berlin','',0),
		(2,NULL,1700003600,'America/Los_Angeles','USB',0),
		(3,'Removed',1700007200,'Europe/Berlin','',1),
		(4,'',1700003600,'',NULL,0),
		(5,'Undated',0,'UTC','',0);
		INSERT INTO HistorylistEntity(listId,trackId,startTime) VALUES(1,10,1700000100),(1,11,1700000400),(2,10,1700003700);`)
	got, err := List(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	local := time.Unix(1700003600, 0).Local().Format("02/01/2006 15:04")
	start := func(v int64) *int64 { return &v }
	want := []Session{
		{ID: 4, Title: local, StartTime: start(1700003600), TrackCount: 0},
		{ID: 2, Title: "14/11/2023 15:13 (Los_Angeles)", StartTime: start(1700003600), Timezone: "America/Los_Angeles", Drive: "USB", TrackCount: 1},
		{ID: 1, Title: "Warm-up", IsTitled: true, StartTime: start(1700000000), Timezone: "Europe/Berlin", TrackCount: 2},
		{ID: 5, Title: "Undated", IsTitled: true, Timezone: "UTC"},
	}
	if !got.Available || !reflect.DeepEqual(got.Sessions, want) {
		t.Fatalf("sessions = %+v, want %+v", got, want)
	}
}

func TestListWithoutHistoryDatabaseIsUnavailableAndCreatesNothing(t *testing.T) {
	root := dbtest.Library(t, libraryUUID)
	got, err := List(context.Background(), root)
	if err != nil || got.Available || got.Sessions == nil || len(got.Sessions) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "Database2", File)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("history database was created: %v", err)
	}
	writeHistory(t, root, "CREATE TABLE Unrelated (id INTEGER)")
	if got, err := List(context.Background(), root); err != nil || got.Available {
		t.Fatalf("a database without history tables = %+v, %v", got, err)
	}
}

func TestUnreadableHistoryIsCoded(t *testing.T) {
	root := dbtest.Library(t, libraryUUID)
	if err := os.WriteFile(filepath.Join(root, "Database2", File), []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := List(context.Background(), root); code(err) != "play_history_unreadable" {
		t.Fatalf("err = %v", err)
	}
}

func TestInspectOrdersPlaysAndResolvesLibraryTracks(t *testing.T) {
	root, library := fixture(t, `INSERT INTO Historylist(id,title,startTime,timezone,isDeleted) VALUES
		(1,'Set',1700000000,'UTC',0),(2,'Untimed',1700000000,NULL,0),(3,'Removed',1700000000,'UTC',1);
		INSERT INTO HistorylistEntity(id,listId,trackId,startTime) VALUES
		(7,1,12,1700000300),(5,1,10,1700000100),(6,1,11,1700000100),(4,1,999,1700000400),(9,2,10,1700000500);`)
	got, err := Inspect(context.Background(), root, library, 1)
	if err != nil {
		t.Fatal(err)
	}
	type play struct {
		title, status string
		trackID       int64
		at            int64
	}
	var plays []play
	for _, e := range got.Entries {
		at := int64(-1)
		if e.PlayedAt != nil {
			at = *e.PlayedAt
		}
		plays = append(plays, play{e.Title, e.Status, e.TrackID, at})
		if e.Position != len(plays) {
			t.Fatalf("position %d at index %d", e.Position, len(plays))
		}
	}
	want := []play{
		{"One", statusAvailable, 1, 1700000100},
		{"Two", statusAvailable, 2, 1700000100},
		{"Gone", statusMissing, 0, 1700000300},
		{"", statusMissing, 0, 1700000400},
	}
	if !reflect.DeepEqual(plays, want) || got.Title != "Set" || got.TrackCount != 4 {
		t.Fatalf("session = %+v, plays %+v", got.Session, plays)
	}
	untimed, err := Inspect(context.Background(), root, library, 2)
	if err != nil || untimed.Entries[0].PlayedAt != nil {
		t.Fatalf("untimed session = %+v, %v", untimed, err)
	}
	for _, id := range []int64{3, 404} {
		if _, err := Inspect(context.Background(), root, library, id); code(err) != "session_not_found" {
			t.Fatalf("session %d err = %v", id, err)
		}
	}
}

func TestInspectWithoutHistoryIsUnavailable(t *testing.T) {
	root := dbtest.Library(t, libraryUUID)
	library, err := db.Open(filepath.Join(root, "Database2", "m.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer library.Close()
	if _, err := Inspect(context.Background(), root, library, 1); code(err) != "play_history_unavailable" {
		t.Fatalf("err = %v", err)
	}
}

func TestCountsSumVisiblePlaysAndKeepTheLatestTimedOne(t *testing.T) {
	root, library := fixture(t, `INSERT INTO Track(id,path,originDatabaseUuid,originTrackId) VALUES(13,'../Music/old/one.mp3','`+libraryUUID+`',1);
		INSERT INTO Historylist(id,startTime,timezone,isDeleted) VALUES(1,1,'UTC',0),(2,1,NULL,0),(3,1,'UTC',1);
		INSERT INTO HistorylistEntity(listId,trackId,startTime) VALUES
		(1,10,100),(1,10,300),(1,13,200),(2,10,900),(3,10,1000),(1,11,0),(1,12,50);`)
	got, err := Counts(context.Background(), root, library)
	if err != nil {
		t.Fatal(err)
	}
	// Track 1: three timed plays across two copies and one untimed play; the removed session is not counted.
	want := map[int64]Plays{1: {Count: 4, LastPlayed: 300}, 2: {Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
	empty := dbtest.Library(t, libraryUUID)
	if got, err := Counts(context.Background(), empty, library); got != nil || err != nil {
		t.Fatalf("no history = %+v, %v", got, err)
	}
}

func TestDateTitleUsesTheRecordedZoneAndItsCity(t *testing.T) {
	if got := dateTitle(1700000000, "Asia/Kolkata"); got != "15/11/2023 03:43 (Kolkata)" {
		t.Fatalf("title = %q", got)
	}
	local := time.Unix(1700000000, 0).Local().Format("02/01/2006 15:04")
	if got := dateTitle(1700000000, "Not/AZone"); got != local {
		t.Fatalf("unknown zone title = %q, want %q", got, local)
	}
}

func TestDamagedHistorySchemaIsCodedForEveryRead(t *testing.T) {
	root := dbtest.Library(t, libraryUUID)
	library, err := db.Open(filepath.Join(root, "Database2", "m.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer library.Close()
	// The tables exist, so history is present, but Engine's columns are not.
	writeHistory(t, root, "CREATE TABLE Track (id INTEGER); CREATE TABLE Historylist (id INTEGER); CREATE TABLE HistorylistEntity (id INTEGER);")
	if _, err := List(context.Background(), root); code(err) != "play_history_unreadable" {
		t.Fatalf("list err = %v", err)
	}
	if _, err := Inspect(context.Background(), root, library, 1); code(err) != "play_history_unreadable" {
		t.Fatalf("inspect err = %v", err)
	}
	if _, err := Counts(context.Background(), root, library); code(err) != "play_history_unreadable" {
		t.Fatalf("counts err = %v", err)
	}
	if _, err := PreviewImport(context.Background(), root, library, ImportOptions{Session: 1, DryRun: true}); code(err) != "play_history_unreadable" {
		t.Fatalf("import err = %v", err)
	}
}

func TestCancelledReadsStayCancellations(t *testing.T) {
	root, library := fixture(t, `INSERT INTO Historylist(id,startTime,timezone,isDeleted) VALUES(1,1,'UTC',0);`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, read := range map[string]func() error{
		"list":    func() error { _, err := List(ctx, root); return err },
		"inspect": func() error { _, err := Inspect(ctx, root, library, 1); return err },
		"counts":  func() error { _, err := Counts(ctx, root, library); return err },
	} {
		if err := read(); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s err = %v", name, err)
		}
	}
}

func TestResolvePrefersThePathOverOriginIdentity(t *testing.T) {
	tracks := libraryTracks{byPath: map[string]int64{"a.mp3": 3}, byOrigin: map[origin]int64{{"library", 1}: 1}}
	copied := func(path string, id int64, uuid string) historyTrack {
		return historyTrack{path: sql.NullString{String: path, Valid: path != ""}, originID: sql.NullInt64{Int64: id, Valid: id != 0}, originUUID: sql.NullString{String: uuid, Valid: uuid != ""}}
	}
	for _, tc := range []struct {
		track historyTrack
		want  int64
	}{
		{copied("a.mp3", 1, "library"), 3},
		{copied("moved.mp3", 1, "library"), 1},
		{copied("", 1, "library"), 1},
		{copied("moved.mp3", 1, ""), 0},
		{copied("moved.mp3", 0, "library"), 0},
	} {
		if got, ok := tracks.resolve(tc.track); got != tc.want || ok != (tc.want != 0) {
			t.Fatalf("resolve(%+v) = %d, %v; want %d", tc.track, got, ok, tc.want)
		}
	}
}
