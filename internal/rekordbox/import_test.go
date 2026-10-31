package rekordbox

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
)

func TestPreparedImportDecodesAndPersistsNativeAudioPerformance(t *testing.T) {
	root, d, _ := publicationFixture(t)
	media := filepath.Join(t.TempDir(), "source.wav")
	writeNativeWAV(t, media, 48000, 12000)
	location := (&url.URL{Scheme: "file", Host: "localhost", Path: filepath.ToSlash(media)}).String()
	path := filepath.Join(t.TempDir(), "source.xml")
	xml := fmt.Sprintf(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="9" Name="Native import" Artist="Source artist" Location="%s"><POSITION_MARK Name="Fractional cue" Type="0" Start="0.125" Num="7"/></TRACK></COLLECTION></DJ_PLAYLISTS>`, location)
	if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	opts := Options{Path: path, Name: "Native audio", DryRun: true}
	p, err := Prepare(t.Context(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	review, err := p.Review()
	if err != nil || !review.CanApply || review.NewCount != 1 {
		t.Fatalf("native preview=%+v error=%v", review, err)
	}
	opts.DryRun, opts.PlanToken = false, review.PlanToken
	p, err = Prepare(t.Context(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := history.Run(t.Context(), root, "migrate.import", history.ScopeCollection, nil, func(ctx context.Context) error {
		result, err := p.Apply(ctx, d)
		if err == nil && (result.CreatedCount != 1 || result.ReusedCount != 0 || result.RootID <= 0) {
			t.Errorf("native receipt=%+v", result)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var beat, cues, loops []byte
	var title, artist string
	if err := d.QueryRow(`SELECT t.title,t.artist,p.beatData,p.quickCues,p.loops FROM Track t JOIN PerformanceData p ON p.trackId=t.id WHERE t.title=?`, "Native import").Scan(&title, &artist, &beat, &cues, &loops); err != nil {
		t.Fatal(err)
	}
	saved, err := engine.DecodeSavedPerformance(beat, cues, loops)
	if err != nil || title != "Native import" || artist != "Source artist" || len(saved.HotCues) != 1 || saved.HotCues[0].Position != 8 || saved.HotCues[0].Offset != 0.125 {
		t.Fatalf("stored metadata/performance=%q %q %+v error=%v", title, artist, saved, err)
	}
	if _, err := history.Undo(t.Context(), root, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := d.QueryRow(`SELECT COUNT(*) FROM Track WHERE title=?`, "Native import").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("undo retained decoded imported track: %d %v", remaining, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != xml {
		t.Fatalf("import/undo changed original XML: %v", err)
	}
	if info, err := os.Stat(media); err != nil || info.Size() != 44+12000*2 {
		t.Fatalf("import/undo changed source audio: %v", err)
	}
}

func preparedImportFixture(t *testing.T) (string, *sql.DB, Options) {
	t.Helper()
	root, d, _ := publicationFixture(t)
	path := filepath.Join(t.TempDir(), "source.xml")
	media := (&url.URL{Scheme: "file", Host: "localhost", Path: filepath.ToSlash(filepath.Join(t.TempDir(), "absent.wav"))}).String()
	if err := os.WriteFile(path, []byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="`+media+`"/></COLLECTION></DJ_PLAYLISTS>`), 0600); err != nil {
		t.Fatal(err)
	}
	return root, d, Options{Path: path, Name: "Reviewed import", MissingMedia: policySkip, DryRun: true}
}

func TestPreparedImportRechecksXMLAfterAcquiringTransaction(t *testing.T) {
	root, d, opts := preparedImportFixture(t)
	preview, err := Prepare(t.Context(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	review, err := preview.Review()
	if err != nil {
		t.Fatal(err)
	}
	opts.DryRun, opts.PlanToken = false, review.PlanToken
	prepared, err := Prepare(t.Context(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	conn, err := d.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	before := d.Stats().WaitCount
	done := make(chan error, 1)
	go func() {
		_, err := prepared.Apply(t.Context(), d)
		done <- err
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for d.Stats().WaitCount == before {
		select {
		case err := <-done:
			t.Fatalf("apply finished before waiting for native connection: %v", err)
		case <-ctx.Done():
			t.Fatal("apply never reached native transaction acquisition")
		case <-tick.C:
		}
	}
	// BeginTx is now waiting, after the first source check. Preserve physical
	// metadata so only the exact-byte check at the write boundary can catch it.
	data := append([]byte(nil), prepared.input.source.document.XML...)
	for i := range data {
		if data[i] == '1' {
			data[i] = '2'
			break
		}
	}
	if err := os.WriteFile(opts.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	stamp := prepared.input.files.document.modified
	if err := os.Chtimes(opts.Path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if progress.ErrorCode(err) != staleCode {
			t.Fatalf("source changed while acquiring transaction was accepted: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("apply did not settle after releasing native connection")
	}
	var roots int
	if err := d.QueryRow(`SELECT COUNT(*) FROM Playlist`).Scan(&roots); err != nil || roots != 0 {
		t.Fatalf("stale source inserted hierarchy: %d %v", roots, err)
	}
}

func TestPreparedImportOwnsConsentAndPublishesOnlyAfterApply(t *testing.T) {
	root, d, opts := preparedImportFixture(t)
	p, err := Prepare(t.Context(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	review, err := p.Review()
	if err != nil || !review.CanApply || review.MissingCount != 1 || review.PlanToken == "" {
		t.Fatalf("preview=%+v error=%v", review, err)
	}
	if _, err := os.Stat(filepath.Join(root, "Database2", "imports.db")); !os.IsNotExist(err) {
		t.Fatalf("preview enrolled companion: %v", err)
	}
	if _, err := p.Apply(t.Context(), d); progress.ErrorCode(err) != reviewCode {
		t.Fatalf("preview applied changes: %v", err)
	}
	review.Entries[0].Status = statusNew
	again, err := p.Review()
	if err != nil || again.Entries[0].Status != statusMissing {
		t.Fatalf("caller changed prepared review: %+v %v", again, err)
	}
	opts.DryRun, opts.PlanToken = false, review.PlanToken
	prepared, err := Prepare(t.Context(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	var result Result
	entry, err := history.Run(t.Context(), root, "migrate.import", history.ScopeCollection, nil, func(ctx context.Context) error {
		var err error
		result, err = prepared.Apply(ctx, d)
		return err
	})
	if err != nil || result.RootID <= 0 || result.SkippedCount != 1 || result.CreatedCount != 0 || entry == nil {
		t.Fatalf("applied result=%+v history=%+v error=%v", result, entry, err)
	}
	archive, err := importsource.Read(t.Context(), root)
	if err != nil || len(archive.Documents) != 1 || archive.Documents[0].ID != result.DocumentID {
		t.Fatalf("source evidence was not published: %+v %v", archive, err)
	}
	if _, err := history.Undo(t.Context(), root, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var roots int
	if err := d.QueryRow(`SELECT COUNT(*) FROM Playlist`).Scan(&roots); err != nil || roots != 0 {
		t.Fatalf("undo retained imported hierarchy: %d %v", roots, err)
	}
	archive, err = importsource.Read(t.Context(), root)
	if err != nil || len(archive.Documents) != 0 {
		t.Fatalf("undo retained source evidence: %+v %v", archive, err)
	}
}

func TestPreparedImportRejectsChangedConsentAndLiveSourceBeforeWrites(t *testing.T) {
	for _, change := range []string{"token", "XML replacement", "new root"} {
		t.Run(change, func(t *testing.T) {
			root, d, opts := preparedImportFixture(t)
			preview, err := Prepare(t.Context(), d, root, opts)
			if err != nil {
				t.Fatal(err)
			}
			review, err := preview.Review()
			if err != nil {
				t.Fatal(err)
			}
			opts.DryRun, opts.PlanToken = false, review.PlanToken
			if change == "token" {
				opts.PlanToken = "different consent"
				if _, err := Prepare(t.Context(), d, root, opts); progress.ErrorCode(err) != staleCode {
					t.Fatalf("accepted different consent: %v", err)
				}
				return
			}
			prepared, err := Prepare(t.Context(), d, root, opts)
			if err != nil {
				t.Fatal(err)
			}
			if change == "XML replacement" {
				if err := os.Rename(opts.Path, opts.Path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(opts.Path, preview.input.source.document.XML, 0600); err != nil {
					t.Fatal(err)
				}
			} else if _, err := d.Exec(`INSERT INTO Playlist(title,parentListId) VALUES(?,0)`, opts.Name); err != nil {
				t.Fatal(err)
			}
			if got, err := prepared.Apply(t.Context(), d); err == nil || got != (Result{}) {
				t.Fatalf("changed source/destination applied: %+v %v", got, err)
			}
			archive, err := importsource.Read(t.Context(), root)
			if err != nil || len(archive.Documents) != 0 {
				t.Fatalf("rejected import published evidence: %+v %v", archive, err)
			}
		})
	}
}
